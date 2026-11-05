// The ContentRemixer operator manager (design §1): leader-elected manager,
// two reconcilers (ModelProfile, ReferenceRepo) and the admission webhook
// server for the two validating webhooks. The GitHub delivery receiver on
// :8443 is a separate non-admission HTTP server owned by subtask D.
package main

import (
	"flag"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

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

func main() {
	var metricsAddr string
	var probeAddr string
	var certDir string
	var githubWebhookAddr string
	var enableLeaderElection bool
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.StringVar(&certDir, "webhook-cert-path", webhookCertDir, "Directory containing tls.crt/tls.key for the admission webhook server.")
	flag.StringVar(&githubWebhookAddr, "github-webhook-bind-address", ":8443", "The address the GitHub delivery receiver binds to (TLS terminated at the Ingress).")
	flag.BoolVar(&enableLeaderElection, "leader-elect", true,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	opts := zap.Options{Development: false}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

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
	// +kubebuilder:scaffold:builder

	// GitHub delivery receiver (subtask D): non-admission HTTP server behind
	// the TLS Ingress, acks recycle:* issues with labelRoutes-resolved formats.
	if err := mgr.Add(&githubapp.Server{Addr: githubWebhookAddr, Client: mgr.GetClient()}); err != nil {
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
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
