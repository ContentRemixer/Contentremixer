// Command booth is the Booth API server: the single front door to the
// ContentRemixer CRs, enforcing the frozen design §6.3 authz predicate on every
// read and write (see internal/authz, internal/store).
//
// AuthN is the booth/auth provider (bcrypt user table + signed sessions, board
// decision #8a/#8b), bootstrapped from the k8s Secret via BootstrapFromEnv.
// AuthZ + the CR API are P3-1 (ISI-7149). Both live in this one module/binary.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	appv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	appcontent "github.com/ContentRemixer/Contentremixer/app/content"
	apptest "github.com/ContentRemixer/Contentremixer/app/contenttest"

	"github.com/ContentRemixer/Contentremixer/app/review"

	"github.com/ContentRemixer/Contentremixer/booth/auth"
	"github.com/ContentRemixer/Contentremixer/booth/internal/audit"
	"github.com/ContentRemixer/Contentremixer/booth/internal/content"
	"github.com/ContentRemixer/Contentremixer/booth/internal/contenttest"
	"github.com/ContentRemixer/Contentremixer/booth/internal/scrapejobs"
	"github.com/ContentRemixer/Contentremixer/booth/internal/server"
	"github.com/ContentRemixer/Contentremixer/booth/internal/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error("booth exited with error", slog.Any("err", err))
		os.Exit(1)
	}
}

func run() error {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)

	namespace := envOr("BOOTH_NAMESPACE", "content-remixer")
	addr := envOr("BOOTH_ADDR", ":8080")

	// AuthN provider: bcrypt user table + signed sessions, seeded Admin from the
	// mounted Secret (fails closed on a missing signing key / seed credential).
	prov, err := auth.BootstrapFromEnv(os.Getenv)
	if err != nil {
		return fmt.Errorf("bootstrap auth: %w", err)
	}

	// Kubernetes client, using the Booth ServiceAccount's in-cluster config (or
	// the ambient kubeconfig when run out-of-cluster). Per design §6.2, per-user
	// scoping is NOT k8s RBAC — this one SA client serves all callers and the
	// Booth API does the tenancy filtering.
	cfg, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("load kube config: %w", err)
	}
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return fmt.Errorf("register client-go scheme: %w", err)
	}
	if err := appv1alpha1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("register contentremixer scheme: %w", err)
	}
	k8s, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("build k8s client: %w", err)
	}

	auditor := audit.NewSlogLogger(log)
	st := store.New(k8s, namespace, auditor)

	// Scraper-review surface (ISI-7207): wired only when a Postgres DSN is
	// configured (the live pgvector PG, ISI-7148). Without it, Booth boots as the
	// CR-only API and the scrape routes are simply absent. The raw review store is
	// returned too so the content-test surface shares it (StyleProfile rows live
	// in the same review DB).
	scrape, reviewStore, cleanup, err := buildScrapeStore(k8s, namespace, auditor, log)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}

	// Content-collaboration surface (ISI-7208): wired only when a Postgres
	// DSN is configured (the live pgvector PG, ISI-7148). Without it, Booth
	// boots as the CR-only API and the content routes are simply absent.
	contentStore, err := buildContentStore(os.Getenv("BOOTH_CONTENT_DSN"), auditor, log)
	if err != nil {
		return err
	}

	// Interactive content-test + persona-validate surface (ISI-7359): wired only
	// when the review store (StyleProfile rows) AND a LiteLLM gateway are both
	// configured. Without either, the routes are absent.
	contentTestStore := buildContentTestStore(reviewStore, st, auditor, log)

	srv := server.New(prov, st, scrape, contentStore, contentTestStore, log)

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Info("booth listening", slog.String("addr", addr), slog.String("namespace", namespace))
	return httpServer.ListenAndServe()
}

// buildScrapeStore wires the scraper-review gating store from env, or returns
// (nil, nil, nil, nil) when BOOTH_REVIEW_DSN is unset (feature off). It also
// returns the raw review store so the content-test surface can share it (the
// StyleProfile rows live in the same review DB); the returned cleanup closes the
// DB pools on shutdown.
func buildScrapeStore(k8s client.Client, namespace string, auditor audit.Logger, log *slog.Logger) (*scrapejobs.Store, *review.Store, func(), error) {
	dsn := os.Getenv("BOOTH_REVIEW_DSN")
	if dsn == "" {
		log.Info("scraper-review disabled: BOOTH_REVIEW_DSN not set")
		return nil, nil, nil, nil
	}
	ctx := context.Background()

	// Accept-time corpus sink: embed via LiteLLM + upsert into the persona
	// pgvector corpus (ISI-7146). Shares the same Postgres instance as review.
	corpus, err := review.NewCorpus(ctx, dsn,
		os.Getenv("BOOTH_LITELLM_URL"), os.Getenv("LITELLM_API_KEY"))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open accept corpus: %w", err)
	}
	reviewStore, err := review.Open(ctx, dsn, review.WithCorpus(corpus))
	if err != nil {
		corpus.Close()
		return nil, nil, nil, fmt.Errorf("open review store: %w", err)
	}

	launcher := &scrapejobs.K8sLauncher{
		Client:         k8s,
		Namespace:      namespace,
		Image:          os.Getenv("BOOTH_SCRAPER_IMAGE"),
		ServiceAccount: envOr("BOOTH_SCRAPER_SA", "booth-scraper"),
		DSNSecretName:  envOr("BOOTH_REVIEW_DSN_SECRET_NAME", "booth-review-dsn"),
		DSNSecretKey:   envOr("BOOTH_REVIEW_DSN_SECRET_KEY", "dsn"),
	}
	cleanup := func() {
		reviewStore.Close()
		corpus.Close()
	}
	log.Info("scraper-review enabled")
	return scrapejobs.New(reviewStore, launcher, auditor), reviewStore, cleanup, nil
}

// buildContentTestStore wires the interactive content-test + persona-validate
// gating store (ISI-7359). It needs the review store (StyleProfile persistence)
// AND a LiteLLM gateway (the generator); with either absent the routes are
// unmounted. The per-subject rate guard caps BOOTH_CONTENT_TEST_RATE_PER_MIN
// generations/minute (default 10; <=0 disables). BOOTH_CONTENT_TEST_MODEL pins
// the model alias (empty = gateway default).
func buildContentTestStore(reviewStore *review.Store, st *store.Store, auditor audit.Logger, log *slog.Logger) *contenttest.Store {
	litellmURL := os.Getenv("BOOTH_LITELLM_URL")
	if reviewStore == nil || litellmURL == "" {
		log.Info("content-test disabled: needs BOOTH_REVIEW_DSN and BOOTH_LITELLM_URL")
		return nil
	}
	runner := apptest.NewRunner(litellmURL, os.Getenv("LITELLM_API_KEY"), os.Getenv("BOOTH_CONTENT_TEST_MODEL"))
	limiter := contenttest.NewLimiter(envIntOr("BOOTH_CONTENT_TEST_RATE_PER_MIN", 10), time.Minute)
	log.Info("content-test enabled")
	return contenttest.New(reviewStore, st, runner, limiter, auditor)
}

// buildContentStore wires the content-collaboration gating store from a
// DSN, or returns (nil, nil) when dsn is empty (feature off).
func buildContentStore(dsn string, auditor audit.Logger, log *slog.Logger) (*content.Store, error) {
	if dsn == "" {
		log.Info("content collaboration disabled: BOOTH_CONTENT_DSN not set")
		return nil, nil
	}
	repo, err := appcontent.Open(context.Background(), dsn)
	if err != nil {
		return nil, fmt.Errorf("open content store: %w", err)
	}
	return content.New(repo, auditor), nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// envIntOr parses key as an int, falling back to def when unset or malformed.
func envIntOr(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
