// Command booth is the Booth API server: the single front door to the
// ContentRemixer CRs, enforcing the frozen design §6.3 authz predicate on every
// read and write (see internal/authz, internal/store).
//
// AuthN is the booth/auth provider (bcrypt user table + signed sessions, board
// decision #8a/#8b), bootstrapped from the k8s Secret via BootstrapFromEnv.
// AuthZ + the CR API are P3-1 (ISI-7149). Both live in this one module/binary.
package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	appv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"

	"github.com/ContentRemixer/Contentremixer/booth/auth"
	"github.com/ContentRemixer/Contentremixer/booth/internal/audit"
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

	st := store.New(k8s, namespace, audit.NewSlogLogger(log))
	srv := server.New(prov, st, log)

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Info("booth listening", slog.String("addr", addr), slog.String("namespace", namespace))
	return httpServer.ListenAndServe()
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
