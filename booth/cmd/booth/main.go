// Command booth is the Booth API server: the single front door to the
// ContentRemixer CRs, enforcing the frozen design §6.3 authz predicate on every
// read and write (see internal/authz, internal/store).
//
// Phase 1 (ISI-7149) ships the authz core, the audit log, the bcrypt built-in
// user table (board decision #8a), and CRUD over the admin-only ModelProfile
// and ReferenceRepo kinds. OIDC login and the DevRelPersona kind slot in behind
// the same interfaces without touching the enforcement path.
package main

import (
	"encoding/json"
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

	"github.com/ContentRemixer/Contentremixer/booth/internal/audit"
	"github.com/ContentRemixer/Contentremixer/booth/internal/server"
	"github.com/ContentRemixer/Contentremixer/booth/internal/session"
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

	auth, err := loadAuthenticator()
	if err != nil {
		return fmt.Errorf("load authenticator: %w", err)
	}

	st := store.New(k8s, namespace, audit.NewSlogLogger(log))
	srv := server.New(auth, st, log)

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Info("booth listening", slog.String("addr", addr), slog.String("namespace", namespace))
	return httpServer.ListenAndServe()
}

// loadAuthenticator builds the Phase-1 bcrypt authenticator from BOOTH_USERS, a
// JSON array of {username, subject, role, passwordHash}. Board decision #8b:
// the initial Admin is seeded by the board/owner, so the user table is supplied
// at deploy time (a Secret), never hard-coded here.
func loadAuthenticator() (session.Authenticator, error) {
	raw := os.Getenv("BOOTH_USERS")
	if raw == "" {
		return nil, fmt.Errorf("BOOTH_USERS is required (JSON user table; see README)")
	}
	var rows []struct {
		Username     string `json:"username"`
		Subject      string `json:"subject"`
		Role         string `json:"role"`
		PasswordHash string `json:"passwordHash"`
	}
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		return nil, fmt.Errorf("parse BOOTH_USERS: %w", err)
	}
	users := make(map[string]session.User, len(rows))
	for _, r := range rows {
		if r.Username == "" || r.Subject == "" || r.Role == "" || r.PasswordHash == "" {
			return nil, fmt.Errorf("BOOTH_USERS row for %q missing a required field", r.Username)
		}
		users[r.Username] = session.User{
			Subject:      r.Subject,
			Role:         r.Role,
			PasswordHash: []byte(r.PasswordHash),
		}
	}
	return session.NewBcryptAuthenticator(users), nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
