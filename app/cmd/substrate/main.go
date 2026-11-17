// Command substrate is the in-repo substrate control plane (ISI-7507, board
// fork option B). It serves the actor-lifecycle HTTP contract
// (docs/substrate/control-plane-contract.md §3) over a durable Postgres store,
// running each actor as a Kubernetes Job of its template image. It is the
// backend the agentgateway front door (deploy/base/substrate) fronts and the
// endpoint the ISI-7397 SubstrateStrategy targets.
//
// Config (env):
//
//	SUBSTRATE_ADDR            listen address (default ":8080")
//	SUBSTRATE_STORE_DSN       Postgres DSN for the durable actor store (required)
//	SUBSTRATE_NAMESPACE       namespace to create actor Jobs in (default "contentremixer")
//	SUBSTRATE_ACTOR_SA        ServiceAccount the actor Jobs run as (optional)
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/client/config"

	"github.com/ContentRemixer/Contentremixer/app/internal/substrate"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("substrate control plane exited", slog.Any("err", err))
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	addr := envOr("SUBSTRATE_ADDR", ":8080")
	dsn := os.Getenv("SUBSTRATE_STORE_DSN")
	if dsn == "" {
		return fmt.Errorf("SUBSTRATE_STORE_DSN is required (the durable actor store)")
	}
	namespace := envOr("SUBSTRATE_NAMESPACE", "contentremixer")
	actorSA := os.Getenv("SUBSTRATE_ACTOR_SA")

	ctx := context.Background()
	store, err := substrate.OpenPG(ctx, dsn)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer store.Close()

	// In-cluster (or kubeconfig) controller-runtime client for Job management.
	cfg, err := ctrlconfig.GetConfig()
	if err != nil {
		return fmt.Errorf("kube config: %w", err)
	}
	kc, err := client.New(cfg, client.Options{})
	if err != nil {
		return fmt.Errorf("kube client: %w", err)
	}
	rt := &substrate.K8sRuntime{Client: kc, Namespace: namespace, ServiceAccount: actorSA}

	srv := substrate.NewServer(store, rt, log)
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Info("substrate control plane listening",
		slog.String("addr", addr), slog.String("namespace", namespace))
	return httpServer.ListenAndServe()
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
