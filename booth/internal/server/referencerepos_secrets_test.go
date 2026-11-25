package server

import (
	"net/http"
	"strings"
	"testing"
	"time"

	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/booth/auth"
	"github.com/ContentRemixer/Contentremixer/booth/internal/audit"
	"github.com/ContentRemixer/Contentremixer/booth/internal/secrets"
	"github.com/ContentRemixer/Contentremixer/booth/internal/store"
)

// newSecretsServer wires a Server with the Secret-write path mounted. It uses
// clientgoscheme (which registers corev1 Secrets) plus the CR scheme.
func newSecretsServer(t *testing.T) (http.Handler, *auth.TokenCodec) {
	t.Helper()
	scheme := clientgoscheme.Scheme
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	auditor := audit.NewSlogLogger(nil)
	st := store.New(c, ns, auditor)

	adminHash, err := auth.HashPassword("admin-pw")
	if err != nil {
		t.Fatal(err)
	}
	prov, err := auth.Bootstrap(auth.Config{
		SessionKey: testKey, SessionTTL: time.Hour,
		AdminUsername: "admin", AdminHash: adminHash, SecureCookie: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := New(prov, st, nil, nil, nil, nil, nil, nil).
		WithReferenceRepoSecrets(secrets.New(c, ns, auditor))
	return srv.Handler(), prov.Codec
}

func TestReferenceRepoSecretsWrite(t *testing.T) {
	h, codec := newSecretsServer(t)
	adminTok := tokenFor(t, codec, "sub-admin", auth.RoleAdmin)
	devTok := tokenFor(t, codec, "sub-alice", auth.RoleDevRel)

	// DevRel is denied — Secrets are Admin-only platform config.
	if w := do(t, h, "POST", "/v1/referencerepos/r1/secrets", devTok, `{"token":"x"}`); w.Code != http.StatusForbidden {
		t.Fatalf("devrel write = %d, want 403", w.Code)
	}

	// Missing token → 400.
	if w := do(t, h, "POST", "/v1/referencerepos/r1/secrets", adminTok, `{}`); w.Code != http.StatusBadRequest {
		t.Fatalf("empty token = %d, want 400", w.Code)
	}

	// Admin write returns the refs and never echoes a value.
	w := do(t, h, "POST", "/v1/referencerepos/r1/secrets", adminTok, `{"token":"pat-123"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("admin write = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"tokenRef"`) || !strings.Contains(body, `"refrepo-r1"`) {
		t.Fatalf("response missing refs: %s", body)
	}
	if strings.Contains(body, "pat-123") {
		t.Fatalf("response leaked the PAT value: %s", body)
	}
	// The response carries only refs (name+key). A supplied HMAC must not come
	// back either: write one explicitly and confirm its value is absent.
	w = do(t, h, "POST", "/v1/referencerepos/r2/secrets", adminTok, `{"token":"t","webhookSecret":"secret-hmac-value"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("admin write (with hmac) = %d, want 200", w.Code)
	}
	if strings.Contains(w.Body.String(), "secret-hmac-value") {
		t.Fatalf("response leaked the HMAC value: %s", w.Body.String())
	}
}
