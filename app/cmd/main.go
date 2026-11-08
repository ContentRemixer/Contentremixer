// The ContentRemixer operator manager (design §1): leader-elected manager,
// two reconcilers (ModelProfile, ReferenceRepo) and the admission webhook
// server for the two validating webhooks. The GitHub delivery receiver on
// :8443 is a separate non-admission HTTP server owned by subtask D.
package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/app/internal/controller"
	"github.com/ContentRemixer/Contentremixer/app/internal/githubapp"
	"github.com/ContentRemixer/Contentremixer/app/internal/litellm"
	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline"
	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline/analyzer"
	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline/checker"
	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline/persona"
	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline/pr"
	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline/writer"
	"github.com/ContentRemixer/Contentremixer/app/internal/rag/embed"
	"github.com/ContentRemixer/Contentremixer/app/internal/rag/retrieve"
	"github.com/ContentRemixer/Contentremixer/app/internal/rag/store"
	opwebhook "github.com/ContentRemixer/Contentremixer/app/internal/webhook"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

var version = "dev"

// webhookCertDir is where the admission webhook serving certs are mounted
// (tls.crt / tls.key). Overridable for envtest.
var webhookCertDir = "/tmp/k8s-webhook-server/serving-certs"

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(contentremixerv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

// recycleRunner adapts the recycle pipeline (subtasks E–H) to the webhook's
// RouteRunner hook: it rebuilds the RunContext from the enqueue request and
// executes analyze → write → check → PR assembly. ModelName stays empty so
// the LiteLLM gateway default (the ModelProfile with spec.default=true)
// resolves it.
type recycleRunner struct {
	runner *pr.Runner
}

func (a recycleRunner) RunRoute(ctx context.Context, req githubapp.RouteRequest) error {
	rc := pipeline.RunContext{
		Repo:         req.Repo,
		IssueNumber:  req.IssueNumber,
		SourcePath:   req.SourcePath,
		InputFormat:  req.Route.InputFormat,
		OutputFormat: req.Route.OutputFormat,
		Notes:        req.Notes,
	}
	_, err := a.runner.Run(ctx, rc, req.PRTarget, req.Token)
	return err
}

// setupOTLP wires the process-wide OpenTelemetry SDK providers to an OTLP/gRPC
// exporter so the persona/RAG instrumentation (app/internal/telemetry, O11y-2/3)
// — which records through the global providers and is a no-op until a real
// provider is installed — starts emitting to the in-cluster Collector
// (O11y-4 / deploy, ISI-7161).
//
// It is strictly opt-in: with OTEL_EXPORTER_OTLP_ENDPOINT unset (kind-e2e,
// envtest, local runs) it installs nothing and telemetry stays a cheap no-op,
// so this changes no existing behaviour. When set (k8Squad-test overlay) the
// exporters read all connection settings — endpoint, http/https scheme →
// (in)secure, headers, timeout — from the standard OTEL_EXPORTER_OTLP_* env,
// and resource attributes from OTEL_SERVICE_NAME / OTEL_RESOURCE_ATTRIBUTES.
//
// The gRPC dials are non-blocking: an unreachable Collector never stalls
// startup, it just retries in the background, and batched spans/metrics are
// flushed by the returned shutdown func on clean exit.
func setupOTLP(ctx context.Context) (func(context.Context) error, error) {
	noop := func(context.Context) error { return nil }
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" &&
		os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") == "" &&
		os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT") == "" {
		setupLog.Info("OTLP export disabled (no OTEL_EXPORTER_OTLP_ENDPOINT); telemetry is a no-op")
		return noop, nil
	}

	res, err := resource.New(ctx,
		resource.WithFromEnv(),      // OTEL_SERVICE_NAME + OTEL_RESOURCE_ATTRIBUTES
		resource.WithTelemetrySDK(), // telemetry.sdk.* identity
		resource.WithProcess(),      // pid/executable for the operator process
		resource.WithContainer(),    // container.id when running in k8s
	)
	if err != nil {
		// Partial resources (e.g. container.id unavailable) are non-fatal; use
		// what we resolved rather than failing the operator over telemetry.
		setupLog.Error(err, "partial OTLP resource detection; continuing")
	}

	traceExp, err := otlptracegrpc.New(ctx)
	if err != nil {
		return noop, err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(traceExp),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))

	metricExp, err := otlpmetricgrpc.New(ctx)
	if err != nil {
		_ = tp.Shutdown(ctx)
		return noop, err
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp)),
	)
	otel.SetMeterProvider(mp)

	setupLog.Info("OTLP export enabled", "endpoint", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	return func(c context.Context) error {
		// Flush metrics first, then traces; both are best-effort on shutdown.
		return errors.Join(mp.Shutdown(c), tp.Shutdown(c))
	}, nil
}

func main() {
	var metricsAddr string
	var probeAddr string
	var certDir string
	var githubWebhookAddr string
	var recycleCheckout string
	var litellmEndpoint string
	var pgvectorDSN string
	var personaNamespace string
	var enableLeaderElection bool
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.StringVar(&certDir, "webhook-cert-path", webhookCertDir, "Directory containing tls.crt/tls.key for the admission webhook server.")
	flag.StringVar(&githubWebhookAddr, "github-webhook-bind-address", ":8443", "The address the GitHub delivery receiver binds to (TLS terminated at the Ingress).")
	flag.StringVar(&recycleCheckout, "recycle-checkout", os.Getenv("RECYCLE_CHECKOUT_PATH"),
		"Local checkout of the reference repo (content/, styles/) the pipeline reads. Empty keeps the receiver ack-only.")
	flag.StringVar(&litellmEndpoint, "litellm-endpoint", os.Getenv("LITELLM_ENDPOINT"),
		"LiteLLM gateway origin the writer generates through (e.g. http://litellm:4000). Required for recycle runs.")
	flag.StringVar(&pgvectorDSN, "pgvector-dsn", os.Getenv("PGVECTOR_DSN"),
		"Postgres/pgvector DSN for persona voice retrieval (P2-D). Empty disables persona conditioning (recycle runs stay style-only, Phase-1).")
	flag.StringVar(&personaNamespace, "persona-namespace", os.Getenv("PERSONA_NAMESPACE"),
		"Namespace DevRelPersona CRs live in, for write-time conditioning lookup. Defaults to the pod namespace (POD_NAMESPACE) when empty.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", true,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	opts := zap.Options{Development: false}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// Wire OTLP export (O11y-4) before anything records telemetry. Opt-in via
	// OTEL_EXPORTER_OTLP_ENDPOINT; a no-op otherwise, so non-cluster runs are
	// unaffected. Non-blocking dials: an unreachable Collector never stalls boot.
	otelShutdown, err := setupOTLP(context.Background())
	if err != nil {
		setupLog.Error(err, "unable to set up OTLP export")
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		WebhookServer: webhook.NewServer(webhook.Options{
			CertDir: certDir,
		}),
		LeaderElection:   enableLeaderElection,
		LeaderElectionID: "contentremixer-operator.lock",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err := (&controller.ModelProfileReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ModelProfile")
		os.Exit(1)
	}
	if err := (&controller.ReferenceRepoReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ReferenceRepo")
		os.Exit(1)
	}
	if err := (&controller.DevRelPersonaReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "DevRelPersona")
		os.Exit(1)
	}

	mpValidator := &opwebhook.ModelProfileCustomValidator{Client: mgr.GetClient()}
	if err := mpValidator.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "ModelProfile")
		os.Exit(1)
	}
	rrValidator := &opwebhook.ReferenceRepoCustomValidator{}
	if err := rrValidator.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "ReferenceRepo")
		os.Exit(1)
	}
	dpValidator := &opwebhook.DevRelPersonaCustomValidator{}
	if err := dpValidator.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "DevRelPersona")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	// One signal-aware context drives startup dials (pgvector) and the manager.
	runCtx := ctrl.SetupSignalHandler()

	// GitHub delivery receiver (subtask D): non-admission HTTP server behind
	// the TLS Ingress, acks recycle:* issues with labelRoutes-resolved
	// formats. When a checkout + LiteLLM endpoint are configured, matched
	// routes also execute the recycle pipeline (E–H: analyzer → writer →
	// checker → PR assembly) on the run worker.
	ghServer := &githubapp.Server{Addr: githubWebhookAddr, Client: mgr.GetClient()}
	if recycleCheckout != "" && litellmEndpoint != "" {
		sourceFS := &analyzer.LocalFS{Root: recycleCheckout}
		gatewayClient := &litellm.Client{
			BaseURL:      litellmEndpoint,
			APIKey:       os.Getenv("LITELLM_API_KEY"),
			DefaultModel: os.Getenv("LITELLM_DEFAULT_MODEL"),
		}
		runWriter := &writer.Writer{
			FS:       sourceFS,
			Generate: writer.LiteLLMGenerator(gatewayClient),
		}
		// P2-D persona conditioning (design §5): best-effort voice overlay,
		// wired only when a pgvector DSN is configured (ISI-7148 deploy). Without
		// it, runWriter.Conditioner stays nil and runs are style-only (Phase-1).
		if pgvectorDSN != "" {
			nsForPersona := personaNamespace
			if nsForPersona == "" {
				nsForPersona = os.Getenv("POD_NAMESPACE")
			}
			vstore, err := store.Open(runCtx, pgvectorDSN, store.DefaultDim)
			if err != nil {
				setupLog.Error(err, "persona conditioning disabled: cannot open pgvector store (runs stay style-only)")
			} else if nsForPersona == "" {
				setupLog.Info("persona conditioning disabled: no persona namespace (set --persona-namespace or POD_NAMESPACE)")
			} else {
				retriever := retrieve.New(embed.NewLiteLLM(gatewayClient, embed.Alias), vstore)
				runWriter.Conditioner = &persona.Conditioner{
					Provider:  &persona.CRPersonaProvider{Reader: mgr.GetClient(), Namespace: nsForPersona},
					Retriever: retriever,
				}
				setupLog.Info("persona conditioning enabled", "personaNamespace", nsForPersona)
			}
		}
		ghServer.Runner = recycleRunner{runner: &pr.Runner{
			Analyzer:  &analyzer.Analyzer{FS: sourceFS},
			Writer:    runWriter,
			Checker:   checker.New(),
			Assembler: &pr.Assembler{GitHub: &pr.GitHubClient{}},
			Comments:  &githubapp.GitHubClient{},
		}}
		setupLog.Info("recycle pipeline enabled", "checkout", recycleCheckout, "litellm", litellmEndpoint)
	} else {
		setupLog.Info("recycle pipeline ack-only (set --recycle-checkout and --litellm-endpoint to enable runs)")
	}
	if err := mgr.Add(ghServer); err != nil {
		setupLog.Error(err, "unable to add GitHub webhook receiver")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager", "version", version)
	runErr := mgr.Start(runCtx)
	// Flush batched spans/metrics before exit. Use a fresh context: runCtx is
	// already cancelled by the signal that stopped the manager, so reusing it
	// would abort the export. Best-effort — log, never mask the run error.
	flushCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := otelShutdown(flushCtx); err != nil {
		setupLog.Error(err, "error flushing OTLP telemetry on shutdown")
	}
	if runErr != nil {
		setupLog.Error(runErr, "problem running manager")
		os.Exit(1)
	}
}
