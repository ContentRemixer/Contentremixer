package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	apppromote "github.com/ContentRemixer/Contentremixer/app/promote"
	"github.com/ContentRemixer/Contentremixer/app/review"
	"github.com/ContentRemixer/Contentremixer/booth/auth"
	"github.com/ContentRemixer/Contentremixer/booth/internal/audit"
	"github.com/ContentRemixer/Contentremixer/booth/internal/promote"
	"github.com/ContentRemixer/Contentremixer/booth/internal/store"
)

// --- fakes satisfying the promote.Store collaborators ---

type prFakeProfiles struct {
	profile review.StyleProfile
	getErr  error
}

func (f prFakeProfiles) GetStyleProfile(_ context.Context, _, _ string) (review.StyleProfile, error) {
	return f.profile, f.getErr
}

type prFakePersonas struct {
	cond    store.PersonaConditioning
	condErr error
}

func (f prFakePersonas) GetPersonaConditioning(_ context.Context, _ auth.Session, _ string) (store.PersonaConditioning, error) {
	return f.cond, f.condErr
}

type prFakeSuggester struct {
	res apppromote.Suggestion
	err error
}

func (f prFakeSuggester) Suggest(_ context.Context, _ apppromote.Request) (apppromote.Suggestion, error) {
	return f.res, f.err
}

type prDenyLimiter struct{}

func (prDenyLimiter) Allow(string) bool { return false }

// newPromoteServer builds a Server whose promote surface is wired to the supplied
// gating store, reusing the auth harness from server_test.go.
func newPromoteServer(t *testing.T, pr *promote.Store) (http.Handler, *auth.TokenCodec) {
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
	return New(prov, st, nil, nil, nil, pr, nil).Handler(), prov.Codec
}

// prStore builds a promote gating store over concrete fakes; Go satisfies the
// (unexported) interface params structurally.
func prStore(p prFakeProfiles, pe prFakePersonas, s prFakeSuggester, lim interface{ Allow(string) bool }) *promote.Store {
	if lim == nil {
		return promote.New(p, pe, s, nil, audit.NewSlogLogger(nil))
	}
	return promote.New(p, pe, s, lim, audit.NewSlogLogger(nil))
}

// validatedProfile is a validated StyleProfile owned by owner (the promote
// precondition).
func validatedProfile(owner string) review.StyleProfile {
	return review.StyleProfile{OwnerSubject: owner, Traits: "dry wit", Validated: true}
}

func TestPromoteRouteUnmountedWhenNil(t *testing.T) {
	h, _ := newTestServer(t) // nil promote store → route not registered
	w := do(t, h, "POST", "/v1/promote", "", `{"personaName":"ada","content":"x"}`)
	if w.Code == http.StatusUnauthorized {
		t.Fatalf("promote route appears mounted despite a nil store (got 401)")
	}
	if ct := w.Header().Get("Content-Type"); strings.Contains(ct, "application/json") {
		t.Fatalf("nil promote store should fall through to the SPA, not our JSON handler; content-type=%q", ct)
	}
}

func TestPromoteHappyPath(t *testing.T) {
	p := prFakeProfiles{profile: validatedProfile("sub-alice")}
	pe := prFakePersonas{cond: store.PersonaConditioning{OwnerSubject: "sub-alice", PrimaryContentKind: "blog"}}
	s := prFakeSuggester{res: apppromote.Suggestion{Hook: "h", Body: "b", CTA: "c", Post: "h\n\nb\n\nc", ModelUsed: "gpt-4o"}}
	h, codec := newPromoteServer(t, prStore(p, pe, s, nil))
	tok := tokenFor(t, codec, "sub-alice", auth.RoleDevRel)

	w := do(t, h, "POST", "/v1/promote", tok, `{"personaName":"ada","content":"my blog","title":"T","url":"https://x/y"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("promote = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"post":"h\n\nb\n\nc"`) {
		t.Fatalf("response missing assembled post: %s", w.Body.String())
	}
}

func TestPromoteUnauthenticated(t *testing.T) {
	h, _ := newPromoteServer(t, prStore(prFakeProfiles{}, prFakePersonas{}, prFakeSuggester{}, nil))
	if w := do(t, h, "POST", "/v1/promote", "", `{"personaName":"ada","content":"x"}`); w.Code != http.StatusUnauthorized {
		t.Fatalf("no token = %d, want 401", w.Code)
	}
}

func TestPromoteBadJSON(t *testing.T) {
	h, codec := newPromoteServer(t, prStore(prFakeProfiles{}, prFakePersonas{}, prFakeSuggester{}, nil))
	tok := tokenFor(t, codec, "sub-alice", auth.RoleDevRel)
	if w := do(t, h, "POST", "/v1/promote", tok, "{not json"); w.Code != http.StatusBadRequest {
		t.Fatalf("bad JSON = %d, want 400", w.Code)
	}
}

func TestPromoteMissingFields(t *testing.T) {
	h, codec := newPromoteServer(t, prStore(prFakeProfiles{}, prFakePersonas{}, prFakeSuggester{}, nil))
	tok := tokenFor(t, codec, "sub-alice", auth.RoleDevRel)
	if w := do(t, h, "POST", "/v1/promote", tok, `{"personaName":"ada"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("missing content = %d, want 400", w.Code)
	}
}

func TestPromoteForbidden(t *testing.T) {
	pe := prFakePersonas{condErr: store.ErrForbidden}
	h, codec := newPromoteServer(t, prStore(prFakeProfiles{}, pe, prFakeSuggester{}, nil))
	tok := tokenFor(t, codec, "sub-bob", auth.RoleDevRel)
	if w := do(t, h, "POST", "/v1/promote", tok, `{"personaName":"ada","content":"x"}`); w.Code != http.StatusForbidden {
		t.Fatalf("forbidden persona = %d, want 403", w.Code)
	}
}

func TestPromoteNotFound(t *testing.T) {
	pe := prFakePersonas{condErr: store.ErrNotFound}
	h, codec := newPromoteServer(t, prStore(prFakeProfiles{}, pe, prFakeSuggester{}, nil))
	tok := tokenFor(t, codec, "sub-alice", auth.RoleDevRel)
	if w := do(t, h, "POST", "/v1/promote", tok, `{"personaName":"ghost","content":"x"}`); w.Code != http.StatusNotFound {
		t.Fatalf("missing persona = %d, want 404", w.Code)
	}
}

func TestPromoteNoProfileIsBadRequest(t *testing.T) {
	p := prFakeProfiles{getErr: review.ErrNotFound}
	pe := prFakePersonas{cond: store.PersonaConditioning{OwnerSubject: "sub-alice"}}
	h, codec := newPromoteServer(t, prStore(p, pe, prFakeSuggester{}, nil))
	tok := tokenFor(t, codec, "sub-alice", auth.RoleDevRel)
	if w := do(t, h, "POST", "/v1/promote", tok, `{"personaName":"ada","content":"x"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("no profile = %d, want 400", w.Code)
	}
}

// The ISI-7369 precondition surfaced over HTTP: unvalidated persona → 409.
func TestPromoteUnvalidatedIsConflict(t *testing.T) {
	p := prFakeProfiles{profile: review.StyleProfile{OwnerSubject: "sub-alice", Traits: "t", Validated: false}}
	pe := prFakePersonas{cond: store.PersonaConditioning{OwnerSubject: "sub-alice"}}
	h, codec := newPromoteServer(t, prStore(p, pe, prFakeSuggester{}, nil))
	tok := tokenFor(t, codec, "sub-alice", auth.RoleDevRel)
	if w := do(t, h, "POST", "/v1/promote", tok, `{"personaName":"ada","content":"x"}`); w.Code != http.StatusConflict {
		t.Fatalf("unvalidated persona = %d, want 409", w.Code)
	}
}

func TestPromoteRateLimited(t *testing.T) {
	p := prFakeProfiles{profile: validatedProfile("sub-alice")}
	pe := prFakePersonas{cond: store.PersonaConditioning{OwnerSubject: "sub-alice"}}
	s := prFakeSuggester{res: apppromote.Suggestion{Post: "x"}}
	h, codec := newPromoteServer(t, prStore(p, pe, s, prDenyLimiter{}))
	tok := tokenFor(t, codec, "sub-alice", auth.RoleDevRel)
	if w := do(t, h, "POST", "/v1/promote", tok, `{"personaName":"ada","content":"x"}`); w.Code != http.StatusTooManyRequests {
		t.Fatalf("rate limited = %d, want 429", w.Code)
	}
}

func TestPromoteGeneratorErrorIs500(t *testing.T) {
	p := prFakeProfiles{profile: validatedProfile("sub-alice")}
	pe := prFakePersonas{cond: store.PersonaConditioning{OwnerSubject: "sub-alice"}}
	s := prFakeSuggester{err: context.DeadlineExceeded}
	h, codec := newPromoteServer(t, prStore(p, pe, s, nil))
	tok := tokenFor(t, codec, "sub-alice", auth.RoleDevRel)
	if w := do(t, h, "POST", "/v1/promote", tok, `{"personaName":"ada","content":"x"}`); w.Code != http.StatusInternalServerError {
		t.Fatalf("generator error = %d, want 500", w.Code)
	}
}
