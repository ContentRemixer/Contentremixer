package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/booth/auth"
	"github.com/ContentRemixer/Contentremixer/booth/internal/approve"
	"github.com/ContentRemixer/Contentremixer/booth/internal/audit"
	"github.com/ContentRemixer/Contentremixer/booth/internal/store"
)

// --- fakes satisfying the approve.Store collaborators ---

type apFakeGate struct {
	err error
}

func (f apFakeGate) GetPersonaConditioning(context.Context, auth.Session, string) (store.PersonaConditioning, error) {
	return store.PersonaConditioning{}, f.err
}

type apFakeReflector struct {
	out approve.Reflection
	err error
}

func (f apFakeReflector) Reflect(context.Context, string, int, approve.Approval) (approve.Reflection, error) {
	return f.out, f.err
}

// newApproveServer builds a Server whose approve surface is wired to a gating
// store over the supplied fakes, reusing the auth harness.
func newApproveServer(t *testing.T, gate apFakeGate, refl apFakeReflector) (http.Handler, *auth.TokenCodec) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	st := store.New(c, ns, audit.NewSlogLogger(nil))

	adminHash, err := auth.HashPassword("admin-pw")
	if err != nil {
		t.Fatal(err)
	}
	prov, err := auth.Bootstrap(auth.Config{
		SessionKey:    testKey,
		SessionTTL:    time.Hour,
		AdminUsername: "admin",
		AdminHash:     adminHash,
		SecureCookie:  false,
	})
	if err != nil {
		t.Fatal(err)
	}
	ap := approve.New(gate, refl, audit.NewSlogLogger(nil))
	return New(prov, st, nil, nil, nil, nil, ap, nil).Handler(), prov.Codec
}

func okReflector() apFakeReflector {
	return apFakeReflector{out: approve.Reflection{Mode: approve.ModeApprove, ReviewID: 1, URL: "https://gh/review"}}
}

const approveBody = `{"repo":"ContentRemixer/Contentremixer","prNumber":42,"personaName":"ada","note":"lgtm"}`

func TestApproveRouteUnmountedWhenNil(t *testing.T) {
	h, _ := newTestServer(t) // nil approve store → route not registered
	w := do(t, h, "POST", "/v1/approve", "", approveBody)
	if w.Code == http.StatusUnauthorized {
		t.Fatalf("approve route appears mounted despite a nil store (got 401)")
	}
	if ct := w.Header().Get("Content-Type"); strings.Contains(ct, "application/json") {
		t.Fatalf("nil approve store should fall through to the SPA, not our JSON handler; content-type=%q", ct)
	}
}

func TestApproveHappyPath(t *testing.T) {
	h, codec := newApproveServer(t, apFakeGate{}, okReflector())
	tok := tokenFor(t, codec, "sub-alice", auth.RoleDevRel)
	w := do(t, h, "POST", "/v1/approve", tok, approveBody)
	if w.Code != http.StatusOK {
		t.Fatalf("approve = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"mode":"approve"`) || !strings.Contains(w.Body.String(), `"approver":"sub-alice"`) {
		t.Fatalf("response missing mode/approver: %s", w.Body.String())
	}
}

func TestApproveUnauthenticated(t *testing.T) {
	h, _ := newApproveServer(t, apFakeGate{}, okReflector())
	if w := do(t, h, "POST", "/v1/approve", "", approveBody); w.Code != http.StatusUnauthorized {
		t.Fatalf("no token = %d, want 401", w.Code)
	}
}

func TestApproveBadJSON(t *testing.T) {
	h, codec := newApproveServer(t, apFakeGate{}, okReflector())
	tok := tokenFor(t, codec, "sub-alice", auth.RoleDevRel)
	if w := do(t, h, "POST", "/v1/approve", tok, "{not json"); w.Code != http.StatusBadRequest {
		t.Fatalf("bad JSON = %d, want 400", w.Code)
	}
}

func TestApproveBadRequest(t *testing.T) {
	h, codec := newApproveServer(t, apFakeGate{}, okReflector())
	tok := tokenFor(t, codec, "sub-alice", auth.RoleDevRel)
	if w := do(t, h, "POST", "/v1/approve", tok, `{"repo":"","prNumber":1,"personaName":"ada"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("empty repo = %d, want 400", w.Code)
	}
}

func TestApproveForbidden(t *testing.T) {
	h, codec := newApproveServer(t, apFakeGate{err: store.ErrForbidden}, okReflector())
	tok := tokenFor(t, codec, "sub-bob", auth.RoleDevRel)
	if w := do(t, h, "POST", "/v1/approve", tok, approveBody); w.Code != http.StatusForbidden {
		t.Fatalf("unowned persona = %d, want 403", w.Code)
	}
}

func TestApproveNotFound(t *testing.T) {
	h, codec := newApproveServer(t, apFakeGate{err: store.ErrNotFound}, okReflector())
	tok := tokenFor(t, codec, "sub-alice", auth.RoleDevRel)
	if w := do(t, h, "POST", "/v1/approve", tok, `{"repo":"o/r","prNumber":9,"personaName":"ghost"}`); w.Code != http.StatusNotFound {
		t.Fatalf("missing persona = %d, want 404", w.Code)
	}
}

func TestApproveReflectFailureIs502(t *testing.T) {
	h, codec := newApproveServer(t, apFakeGate{}, apFakeReflector{err: errors.New("github down")})
	tok := tokenFor(t, codec, "sub-alice", auth.RoleAdmin)
	if w := do(t, h, "POST", "/v1/approve", tok, approveBody); w.Code != http.StatusBadGateway {
		t.Fatalf("reflect failure = %d, want 502", w.Code)
	}
}
