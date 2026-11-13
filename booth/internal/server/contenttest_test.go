package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	apptest "github.com/ContentRemixer/Contentremixer/app/contenttest"
	"github.com/ContentRemixer/Contentremixer/app/review"
	"github.com/ContentRemixer/Contentremixer/booth/auth"
	"github.com/ContentRemixer/Contentremixer/booth/internal/audit"
	"github.com/ContentRemixer/Contentremixer/booth/internal/contenttest"
	"github.com/ContentRemixer/Contentremixer/booth/internal/store"
)

// --- fakes satisfying the contenttest.Store collaborators ---

type ctFakeProfiles struct {
	profile review.StyleProfile
	getErr  error
	valErr  error
}

func (f ctFakeProfiles) GetStyleProfile(_ context.Context, _, _ string) (review.StyleProfile, error) {
	return f.profile, f.getErr
}
func (f ctFakeProfiles) ValidateStyleProfile(_ context.Context, _, _ string) (review.StyleProfile, error) {
	if f.valErr != nil {
		return review.StyleProfile{}, f.valErr
	}
	sp := f.profile
	sp.Validated = true
	return sp, nil
}

type ctFakePersonas struct {
	cond    store.PersonaConditioning
	condErr error
	setErr  error
}

func (f ctFakePersonas) GetPersonaConditioning(_ context.Context, _ auth.Session, _ string) (store.PersonaConditioning, error) {
	return f.cond, f.condErr
}
func (f ctFakePersonas) SetPersonaValidated(_ context.Context, _ auth.Session, _ string, _ bool) error {
	return f.setErr
}

type ctFakeRunner struct {
	res apptest.Result
	err error
}

func (f ctFakeRunner) Run(_ context.Context, _ apptest.Request) (apptest.Result, error) {
	return f.res, f.err
}

type ctDenyLimiter struct{}

func (ctDenyLimiter) Allow(string) bool { return false }

type ctFakeSynth struct {
	profile review.StyleProfile
	err     error
}

func (f ctFakeSynth) Synthesize(_ context.Context, _ review.SynthesizeRequest) (review.StyleProfile, error) {
	return f.profile, f.err
}

// newContentTestServer builds a Server whose content-test surface is wired to
// the supplied gating store, reusing the auth harness from server_test.go.
func newCTServer(t *testing.T, ct *contenttest.Store) (http.Handler, *auth.TokenCodec) {
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
	return New(prov, st, nil, nil, ct, nil, nil).Handler(), prov.Codec
}

// ctStore builds a content-test gating store over concrete fakes. The fakes are
// passed straight to contenttest.New; Go satisfies its (unexported) interface
// params structurally, so no exported interface is needed here.
func ctStore(p ctFakeProfiles, pe ctFakePersonas, r ctFakeRunner, lim interface{ Allow(string) bool }) *contenttest.Store {
	if lim == nil {
		return contenttest.New(p, pe, r, nil, nil, audit.NewSlogLogger(nil))
	}
	return contenttest.New(p, pe, r, nil, lim, audit.NewSlogLogger(nil))
}

// ctStoreSynth builds a content-test gating store with a synthesizer wired, so
// the O6 style-profile synthesize route (ISI-7361 D6) is mounted.
func ctStoreSynth(p ctFakeProfiles, pe ctFakePersonas, synth ctFakeSynth) *contenttest.Store {
	return contenttest.New(p, pe, ctFakeRunner{}, synth, nil, audit.NewSlogLogger(nil))
}

func TestContentTestRoutesUnmountedWhenNil(t *testing.T) {
	h, _ := newTestServer(t) // nil content-test store → route not registered
	// With the route unmounted, the request falls through to the SPA catch-all
	// (served for any unmatched path). The telltale that our protected handler is
	// absent: an UNauthenticated POST is not 401'd — it reaches the public SPA.
	w := do(t, h, "POST", "/v1/content-test", "", `{"personaName":"ada","topic":"x"}`)
	if w.Code == http.StatusUnauthorized {
		t.Fatalf("content-test route appears mounted despite a nil store (got 401)")
	}
	if ct := w.Header().Get("Content-Type"); strings.Contains(ct, "application/json") {
		t.Fatalf("nil content-test store should fall through to the SPA, not our JSON handler; content-type=%q", ct)
	}
}

func TestContentTestHappyPath(t *testing.T) {
	p := ctFakeProfiles{profile: review.StyleProfile{OwnerSubject: "sub-alice", Traits: "dry wit", Validated: false}}
	pe := ctFakePersonas{cond: store.PersonaConditioning{OwnerSubject: "sub-alice", PrimaryContentKind: "blog"}}
	r := ctFakeRunner{res: apptest.Result{Sample: "a sample", ModelUsed: "gpt-4o"}}
	ct := ctStore(p, pe, r, nil)
	h, codec := newCTServer(t, ct)
	tok := tokenFor(t, codec, "sub-alice", auth.RoleDevRel)

	w := do(t, h, "POST", "/v1/content-test", tok, `{"personaName":"ada","topic":"deploys"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("content-test = %d, want 200; body=%s", w.Code, w.Body.String())
	}
}

func TestContentTestUnauthenticated(t *testing.T) {
	ct := ctStore(ctFakeProfiles{}, ctFakePersonas{}, ctFakeRunner{}, nil)
	h, _ := newCTServer(t, ct)
	if w := do(t, h, "POST", "/v1/content-test", "", `{"personaName":"ada","topic":"x"}`); w.Code != http.StatusUnauthorized {
		t.Fatalf("no token = %d, want 401", w.Code)
	}
}

func TestContentTestBadJSON(t *testing.T) {
	ct := ctStore(ctFakeProfiles{}, ctFakePersonas{}, ctFakeRunner{}, nil)
	h, codec := newCTServer(t, ct)
	tok := tokenFor(t, codec, "sub-alice", auth.RoleDevRel)
	if w := do(t, h, "POST", "/v1/content-test", tok, "{not json"); w.Code != http.StatusBadRequest {
		t.Fatalf("bad JSON = %d, want 400", w.Code)
	}
}

func TestContentTestMissingFields(t *testing.T) {
	ct := ctStore(ctFakeProfiles{}, ctFakePersonas{}, ctFakeRunner{}, nil)
	h, codec := newCTServer(t, ct)
	tok := tokenFor(t, codec, "sub-alice", auth.RoleDevRel)
	if w := do(t, h, "POST", "/v1/content-test", tok, `{"personaName":"ada"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("missing topic = %d, want 400", w.Code)
	}
}

func TestContentTestForbidden(t *testing.T) {
	pe := ctFakePersonas{condErr: store.ErrForbidden}
	ct := ctStore(ctFakeProfiles{}, pe, ctFakeRunner{}, nil)
	h, codec := newCTServer(t, ct)
	tok := tokenFor(t, codec, "sub-bob", auth.RoleDevRel)
	if w := do(t, h, "POST", "/v1/content-test", tok, `{"personaName":"ada","topic":"x"}`); w.Code != http.StatusForbidden {
		t.Fatalf("forbidden persona = %d, want 403", w.Code)
	}
}

func TestContentTestNotFound(t *testing.T) {
	pe := ctFakePersonas{condErr: store.ErrNotFound}
	ct := ctStore(ctFakeProfiles{}, pe, ctFakeRunner{}, nil)
	h, codec := newCTServer(t, ct)
	tok := tokenFor(t, codec, "sub-alice", auth.RoleDevRel)
	if w := do(t, h, "POST", "/v1/content-test", tok, `{"personaName":"ghost","topic":"x"}`); w.Code != http.StatusNotFound {
		t.Fatalf("missing persona = %d, want 404", w.Code)
	}
}

func TestContentTestNoProfileIsBadRequest(t *testing.T) {
	p := ctFakeProfiles{getErr: review.ErrNotFound}
	pe := ctFakePersonas{cond: store.PersonaConditioning{OwnerSubject: "sub-alice"}}
	ct := ctStore(p, pe, ctFakeRunner{}, nil)
	h, codec := newCTServer(t, ct)
	tok := tokenFor(t, codec, "sub-alice", auth.RoleDevRel)
	if w := do(t, h, "POST", "/v1/content-test", tok, `{"personaName":"ada","topic":"x"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("no profile = %d, want 400", w.Code)
	}
}

func TestContentTestRateLimited(t *testing.T) {
	p := ctFakeProfiles{profile: review.StyleProfile{OwnerSubject: "sub-alice"}}
	pe := ctFakePersonas{cond: store.PersonaConditioning{OwnerSubject: "sub-alice"}}
	ct := ctStore(p, pe, ctFakeRunner{res: apptest.Result{Sample: "x"}}, ctDenyLimiter{})
	h, codec := newCTServer(t, ct)
	tok := tokenFor(t, codec, "sub-alice", auth.RoleDevRel)
	if w := do(t, h, "POST", "/v1/content-test", tok, `{"personaName":"ada","topic":"x"}`); w.Code != http.StatusTooManyRequests {
		t.Fatalf("rate limited = %d, want 429", w.Code)
	}
}

func TestContentTestGeneratorErrorIs500(t *testing.T) {
	p := ctFakeProfiles{profile: review.StyleProfile{OwnerSubject: "sub-alice"}}
	pe := ctFakePersonas{cond: store.PersonaConditioning{OwnerSubject: "sub-alice"}}
	r := ctFakeRunner{err: context.DeadlineExceeded}
	ct := ctStore(p, pe, r, nil)
	h, codec := newCTServer(t, ct)
	tok := tokenFor(t, codec, "sub-alice", auth.RoleDevRel)
	if w := do(t, h, "POST", "/v1/content-test", tok, `{"personaName":"ada","topic":"x"}`); w.Code != http.StatusInternalServerError {
		t.Fatalf("generator error = %d, want 500", w.Code)
	}
}

func TestValidatePersonaHappyPath(t *testing.T) {
	p := ctFakeProfiles{profile: review.StyleProfile{OwnerSubject: "sub-alice", PersonaName: "ada"}}
	pe := ctFakePersonas{cond: store.PersonaConditioning{OwnerSubject: "sub-alice"}}
	ct := ctStore(p, pe, ctFakeRunner{}, nil)
	h, codec := newCTServer(t, ct)
	tok := tokenFor(t, codec, "sub-alice", auth.RoleDevRel)
	if w := do(t, h, "POST", "/v1/devrelpersonas/ada/validate", tok, ""); w.Code != http.StatusOK {
		t.Fatalf("validate = %d, want 200; body=%s", w.Code, w.Body.String())
	}
}

func TestValidatePersonaNoProfileIsBadRequest(t *testing.T) {
	p := ctFakeProfiles{valErr: review.ErrNotFound}
	pe := ctFakePersonas{cond: store.PersonaConditioning{OwnerSubject: "sub-alice"}}
	ct := ctStore(p, pe, ctFakeRunner{}, nil)
	h, codec := newCTServer(t, ct)
	tok := tokenFor(t, codec, "sub-alice", auth.RoleDevRel)
	if w := do(t, h, "POST", "/v1/devrelpersonas/ada/validate", tok, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("validate no profile = %d, want 400", w.Code)
	}
}

func TestValidatePersonaUnauthenticated(t *testing.T) {
	ct := ctStore(ctFakeProfiles{}, ctFakePersonas{}, ctFakeRunner{}, nil)
	h, _ := newCTServer(t, ct)
	if w := do(t, h, "POST", "/v1/devrelpersonas/ada/validate", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no token = %d, want 401", w.Code)
	}
}

func TestStyleProfileHappyPath(t *testing.T) {
	p := ctFakeProfiles{profile: review.StyleProfile{OwnerSubject: "sub-alice", PersonaName: "ada", Traits: "dry wit", Sample: "a sample"}}
	pe := ctFakePersonas{cond: store.PersonaConditioning{OwnerSubject: "sub-alice"}}
	ct := ctStore(p, pe, ctFakeRunner{}, nil)
	h, codec := newCTServer(t, ct)
	tok := tokenFor(t, codec, "sub-alice", auth.RoleDevRel)

	w := do(t, h, "GET", "/v1/devrelpersonas/ada/style-profile", tok, "")
	if w.Code != http.StatusOK {
		t.Fatalf("style-profile = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var sp review.StyleProfile
	if err := json.Unmarshal(w.Body.Bytes(), &sp); err != nil {
		t.Fatalf("bad JSON profile: %v", err)
	}
	if sp.PersonaName != "ada" || sp.Traits != "dry wit" {
		t.Fatalf("profile = %+v, want ada/dry wit", sp)
	}
}

func TestStyleProfileNotFoundYet(t *testing.T) {
	p := ctFakeProfiles{getErr: review.ErrNotFound}
	pe := ctFakePersonas{cond: store.PersonaConditioning{OwnerSubject: "sub-alice"}}
	ct := ctStore(p, pe, ctFakeRunner{}, nil)
	h, codec := newCTServer(t, ct)
	tok := tokenFor(t, codec, "sub-alice", auth.RoleDevRel)

	// Pre-synthesis (O7) state: the profile does not exist yet → 404, the UI's
	// "awaiting synthesis" empty state, not an internal error.
	if w := do(t, h, "GET", "/v1/devrelpersonas/ada/style-profile", tok, ""); w.Code != http.StatusNotFound {
		t.Fatalf("no profile = %d, want 404", w.Code)
	}
}

func TestStyleProfileUnauthenticated(t *testing.T) {
	ct := ctStore(ctFakeProfiles{}, ctFakePersonas{}, ctFakeRunner{}, nil)
	h, _ := newCTServer(t, ct)
	if w := do(t, h, "GET", "/v1/devrelpersonas/ada/style-profile", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no token = %d, want 401", w.Code)
	}
}

func TestStyleProfileForbidden(t *testing.T) {
	pe := ctFakePersonas{condErr: store.ErrForbidden}
	ct := ctStore(ctFakeProfiles{}, pe, ctFakeRunner{}, nil)
	h, codec := newCTServer(t, ct)
	tok := tokenFor(t, codec, "sub-bob", auth.RoleDevRel)
	if w := do(t, h, "GET", "/v1/devrelpersonas/ada/style-profile", tok, ""); w.Code != http.StatusForbidden {
		t.Fatalf("forbidden persona = %d, want 403", w.Code)
	}
}

// --- style-profile synthesize route (ISI-7361 D6, O6) ---

func TestSynthesizeHappyPath(t *testing.T) {
	pe := ctFakePersonas{cond: store.PersonaConditioning{OwnerSubject: "sub-alice", PrimaryContentKind: "blog", SelfDescription: "warm, concrete"}}
	synth := ctFakeSynth{profile: review.StyleProfile{OwnerSubject: "sub-alice", PersonaName: "ada", Traits: "dry wit", Sample: "x", SourceCount: 3}}
	ct := ctStoreSynth(ctFakeProfiles{}, pe, synth)
	h, codec := newCTServer(t, ct)
	tok := tokenFor(t, codec, "sub-alice", auth.RoleDevRel)
	w := do(t, h, "POST", "/v1/devrelpersonas/ada/style-profile/synthesize", tok, "")
	if w.Code != http.StatusOK {
		t.Fatalf("synthesize = %d, want 200; body=%s", w.Code, w.Body.String())
	}
}

func TestSynthesizeGeneratorErrorIs500(t *testing.T) {
	pe := ctFakePersonas{cond: store.PersonaConditioning{OwnerSubject: "sub-alice"}}
	synth := ctFakeSynth{err: context.DeadlineExceeded}
	ct := ctStoreSynth(ctFakeProfiles{}, pe, synth)
	h, codec := newCTServer(t, ct)
	tok := tokenFor(t, codec, "sub-alice", auth.RoleDevRel)
	if w := do(t, h, "POST", "/v1/devrelpersonas/ada/style-profile/synthesize", tok, ""); w.Code != http.StatusInternalServerError {
		t.Fatalf("synth error = %d, want 500", w.Code)
	}
}

func TestSynthesizeForbidden(t *testing.T) {
	pe := ctFakePersonas{condErr: store.ErrForbidden}
	ct := ctStoreSynth(ctFakeProfiles{}, pe, ctFakeSynth{})
	h, codec := newCTServer(t, ct)
	tok := tokenFor(t, codec, "sub-bob", auth.RoleDevRel)
	if w := do(t, h, "POST", "/v1/devrelpersonas/ada/style-profile/synthesize", tok, ""); w.Code != http.StatusForbidden {
		t.Fatalf("forbidden = %d, want 403", w.Code)
	}
}

func TestSynthesizeRouteUnmountedWithoutSynth(t *testing.T) {
	// A store built without a synthesizer (no LiteLLM gateway) must NOT mount the
	// synthesize route — the request falls through to the SPA catch-all, so an
	// unauthenticated POST is not 401'd by our protected handler.
	ct := ctStore(ctFakeProfiles{}, ctFakePersonas{}, ctFakeRunner{}, nil)
	h, _ := newCTServer(t, ct)
	w := do(t, h, "POST", "/v1/devrelpersonas/ada/style-profile/synthesize", "", "")
	if w.Code == http.StatusUnauthorized {
		t.Fatalf("synthesize route appears mounted despite a nil synthesizer (got 401)")
	}
}
