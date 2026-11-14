package contenttest

import (
	"context"
	"errors"
	"testing"

	apptest "github.com/ContentRemixer/Contentremixer/app/contenttest"
	"github.com/ContentRemixer/Contentremixer/app/review"
	"github.com/ContentRemixer/Contentremixer/booth/auth"
	"github.com/ContentRemixer/Contentremixer/booth/internal/audit"
	bstore "github.com/ContentRemixer/Contentremixer/booth/internal/store"
)

const (
	aliceSub = "sub-alice"
	bobSub   = "sub-bob"
)

var (
	alice = auth.Session{Subject: aliceSub, Role: auth.RoleDevRel}
	admin = auth.Session{Subject: "sub-admin", Role: auth.RoleAdmin}
)

// --- fakes ---

type fakeProfiles struct {
	profile   review.StyleProfile
	getErr    error
	valErr    error
	gotValOwn string
}

func (f *fakeProfiles) GetStyleProfile(_ context.Context, owner, persona string) (review.StyleProfile, error) {
	if f.getErr != nil {
		return review.StyleProfile{}, f.getErr
	}
	return f.profile, nil
}

func (f *fakeProfiles) ValidateStyleProfile(_ context.Context, owner, persona string) (review.StyleProfile, error) {
	f.gotValOwn = owner
	if f.valErr != nil {
		return review.StyleProfile{}, f.valErr
	}
	sp := f.profile
	sp.Validated = true
	return sp, nil
}

type fakePersonas struct {
	conditioning bstore.PersonaConditioning
	condErr      error
	setErr       error
	setCalls     int
	lastValidate bool
}

func (f *fakePersonas) GetPersonaConditioning(_ context.Context, _ auth.Session, _ string) (bstore.PersonaConditioning, error) {
	if f.condErr != nil {
		return bstore.PersonaConditioning{}, f.condErr
	}
	return f.conditioning, nil
}

func (f *fakePersonas) SetPersonaValidated(_ context.Context, _ auth.Session, _ string, validated bool) error {
	f.setCalls++
	f.lastValidate = validated
	return f.setErr
}

type fakeRunner struct {
	req apptest.Request
	res apptest.Result
	err error
}

func (f *fakeRunner) Run(_ context.Context, req apptest.Request) (apptest.Result, error) {
	f.req = req
	return f.res, f.err
}

type capAuditor struct{ entries []audit.Entry }

func (c *capAuditor) Log(_ context.Context, e audit.Entry) { c.entries = append(c.entries, e) }
func (c *capAuditor) last() audit.Entry                    { return c.entries[len(c.entries)-1] }

type denyLimiter struct{}

func (denyLimiter) Allow(string) bool { return false }

type fakeSynth struct {
	req     review.SynthesizeRequest
	profile review.StyleProfile
	err     error
}

func (f *fakeSynth) Synthesize(_ context.Context, req review.SynthesizeRequest) (review.StyleProfile, error) {
	f.req = req
	return f.profile, f.err
}

func newStore(p *fakeProfiles, pe *fakePersonas, r *fakeRunner, lim rateGuard) (*Store, *capAuditor) {
	a := &capAuditor{}
	return New(p, pe, r, nil, lim, a), a
}

// newStoreSynth wires a synthesizer so the Synthesize action (ISI-7361 D6) can
// be exercised.
func newStoreSynth(p *fakeProfiles, pe *fakePersonas, synth *fakeSynth) (*Store, *capAuditor) {
	a := &capAuditor{}
	return New(p, pe, &fakeRunner{}, synth, nil, a), a
}

// --- RunTest ---

func TestRunTest_HappyPath_ConditionsOnProfileAndSpec(t *testing.T) {
	p := &fakeProfiles{profile: review.StyleProfile{OwnerSubject: aliceSub, PersonaName: "ada", Traits: "dry wit", Validated: false}}
	pe := &fakePersonas{conditioning: bstore.PersonaConditioning{OwnerSubject: aliceSub, SelfDescription: "friendly", PrimaryContentKind: "blog"}}
	r := &fakeRunner{res: apptest.Result{Sample: "a sample", ModelUsed: "gpt-4o"}}
	s, aud := newStore(p, pe, r, nil)

	out, err := s.RunTest(context.Background(), alice, TestRequest{PersonaName: "ada", Topic: "deploys"})
	if err != nil {
		t.Fatalf("RunTest: %v", err)
	}
	if out.Sample != "a sample" || out.ModelUsed != "gpt-4o" || out.Validated {
		t.Fatalf("result = %+v", out)
	}
	if r.req.StyleTraits != "dry wit" || r.req.SelfDescription != "friendly" || r.req.PrimaryContentKind != "blog" || r.req.Topic != "deploys" {
		t.Fatalf("runner request not conditioned: %+v", r.req)
	}
	if e := aud.last(); e.Verb != verbTest || e.Decision != audit.DecisionAllow {
		t.Fatalf("audit = %+v", e)
	}
}

func TestRunTest_MissingFields(t *testing.T) {
	s, _ := newStore(&fakeProfiles{}, &fakePersonas{}, &fakeRunner{}, nil)
	if _, err := s.RunTest(context.Background(), alice, TestRequest{Topic: "x"}); !IsBadRequest(err) {
		t.Fatalf("want bad request, got %v", err)
	}
	if _, err := s.RunTest(context.Background(), alice, TestRequest{PersonaName: "ada"}); !IsBadRequest(err) {
		t.Fatalf("want bad request, got %v", err)
	}
}

func TestRunTest_ForbiddenPersonaDenies(t *testing.T) {
	pe := &fakePersonas{condErr: bstore.ErrForbidden}
	s, _ := newStore(&fakeProfiles{}, pe, &fakeRunner{}, nil)
	if _, err := s.RunTest(context.Background(), alice, TestRequest{PersonaName: "bob-persona", Topic: "x"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
}

func TestRunTest_NotFoundPersona(t *testing.T) {
	pe := &fakePersonas{condErr: bstore.ErrNotFound}
	s, _ := newStore(&fakeProfiles{}, pe, &fakeRunner{}, nil)
	if _, err := s.RunTest(context.Background(), alice, TestRequest{PersonaName: "ghost", Topic: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestRunTest_NoProfileYetIsBadRequest(t *testing.T) {
	p := &fakeProfiles{getErr: review.ErrNotFound}
	pe := &fakePersonas{conditioning: bstore.PersonaConditioning{OwnerSubject: aliceSub}}
	r := &fakeRunner{}
	s, aud := newStore(p, pe, r, nil)
	if _, err := s.RunTest(context.Background(), alice, TestRequest{PersonaName: "ada", Topic: "x"}); !IsBadRequest(err) {
		t.Fatalf("want bad request, got %v", err)
	}
	if aud.last().Decision != audit.DecisionDeny {
		t.Fatalf("expected deny audit for missing profile")
	}
}

func TestRunTest_RateLimitedBeforeGeneration(t *testing.T) {
	p := &fakeProfiles{profile: review.StyleProfile{OwnerSubject: aliceSub}}
	pe := &fakePersonas{conditioning: bstore.PersonaConditioning{OwnerSubject: aliceSub}}
	r := &fakeRunner{res: apptest.Result{Sample: "x"}}
	s, aud := newStore(p, pe, r, denyLimiter{})
	if _, err := s.RunTest(context.Background(), alice, TestRequest{PersonaName: "ada", Topic: "x"}); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("want ErrRateLimited, got %v", err)
	}
	if r.req.Topic != "" {
		t.Fatalf("generator must not run when rate-limited")
	}
	if aud.last().Decision != audit.DecisionDeny {
		t.Fatalf("expected deny audit on rate limit")
	}
}

func TestRunTest_GeneratorErrorAuditsDeny(t *testing.T) {
	p := &fakeProfiles{profile: review.StyleProfile{OwnerSubject: aliceSub}}
	pe := &fakePersonas{conditioning: bstore.PersonaConditioning{OwnerSubject: aliceSub}}
	r := &fakeRunner{err: errors.New("gateway down")}
	s, aud := newStore(p, pe, r, nil)
	if _, err := s.RunTest(context.Background(), alice, TestRequest{PersonaName: "ada", Topic: "x"}); err == nil {
		t.Fatal("expected generator error")
	}
	if aud.last().Decision != audit.DecisionDeny {
		t.Fatalf("expected deny audit on generator error")
	}
}

// Admin testing another DevRel's persona: the StyleProfile is loaded under the
// persona's REAL owner (from conditioning), not the admin's subject.
func TestRunTest_AdminUsesPersonaOwnerForProfile(t *testing.T) {
	p := &fakeProfiles{profile: review.StyleProfile{OwnerSubject: bobSub, PersonaName: "bob-persona", Traits: "t"}}
	pe := &fakePersonas{conditioning: bstore.PersonaConditioning{OwnerSubject: bobSub}}
	r := &fakeRunner{res: apptest.Result{Sample: "s"}}
	s, _ := newStore(p, pe, r, nil)
	if _, err := s.RunTest(context.Background(), admin, TestRequest{PersonaName: "bob-persona", Topic: "x"}); err != nil {
		t.Fatalf("RunTest: %v", err)
	}
	if r.req.StyleTraits != "t" {
		t.Fatalf("profile not loaded under persona owner")
	}
}

// --- Validate ---

func TestValidate_WritesPostgresThenCRD(t *testing.T) {
	p := &fakeProfiles{profile: review.StyleProfile{OwnerSubject: aliceSub, PersonaName: "ada"}}
	pe := &fakePersonas{conditioning: bstore.PersonaConditioning{OwnerSubject: aliceSub}}
	s, aud := newStore(p, pe, &fakeRunner{}, nil)

	sp, err := s.Validate(context.Background(), alice, "ada")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !sp.Validated {
		t.Fatalf("returned profile not validated")
	}
	if p.gotValOwn != aliceSub {
		t.Fatalf("validated under owner %q, want %q", p.gotValOwn, aliceSub)
	}
	if pe.setCalls != 1 || !pe.lastValidate {
		t.Fatalf("CRD condition not written: calls=%d validate=%v", pe.setCalls, pe.lastValidate)
	}
	if e := aud.last(); e.Verb != verbValidate || e.Decision != audit.DecisionAllow {
		t.Fatalf("audit = %+v", e)
	}
}

func TestValidate_NoProfileIsBadRequest(t *testing.T) {
	p := &fakeProfiles{valErr: review.ErrNotFound}
	pe := &fakePersonas{conditioning: bstore.PersonaConditioning{OwnerSubject: aliceSub}}
	s, _ := newStore(p, pe, &fakeRunner{}, nil)
	if _, err := s.Validate(context.Background(), alice, "ada"); !IsBadRequest(err) {
		t.Fatalf("want bad request, got %v", err)
	}
	if pe.setCalls != 0 {
		t.Fatalf("CRD must not be written when there is no profile")
	}
}

func TestValidate_ForbiddenDenies(t *testing.T) {
	pe := &fakePersonas{condErr: bstore.ErrForbidden}
	s, _ := newStore(&fakeProfiles{}, pe, &fakeRunner{}, nil)
	if _, err := s.Validate(context.Background(), alice, "bob-persona"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
}

func TestValidate_CRDFailureAfterPostgresReturnsError(t *testing.T) {
	p := &fakeProfiles{profile: review.StyleProfile{OwnerSubject: aliceSub, PersonaName: "ada"}}
	pe := &fakePersonas{conditioning: bstore.PersonaConditioning{OwnerSubject: aliceSub}, setErr: errors.New("status update failed")}
	s, _ := newStore(p, pe, &fakeRunner{}, nil)
	sp, err := s.Validate(context.Background(), alice, "ada")
	if err == nil {
		t.Fatal("expected CRD write error")
	}
	// Postgres flip already happened — the returned profile reflects it (retry idempotent).
	if !sp.Validated {
		t.Fatalf("expected the Postgres-validated profile to be returned")
	}
}

// --- GetProfile (read mirror for the persona-management UI, ISI-7362 D7) ---

func TestGetProfile_OwnerScopedFetch(t *testing.T) {
	p := &fakeProfiles{profile: review.StyleProfile{OwnerSubject: aliceSub, PersonaName: "ada", Traits: "dry wit", Sample: "a sample"}}
	pe := &fakePersonas{conditioning: bstore.PersonaConditioning{OwnerSubject: aliceSub, SelfDescription: "deep dives"}}
	s, _ := newStore(p, pe, &fakeRunner{}, nil)
	sp, err := s.GetProfile(context.Background(), alice, "ada")
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if sp.PersonaName != "ada" || sp.Traits != "dry wit" {
		t.Fatalf("profile = %+v, want persona ada with traits", sp)
	}
}

func TestGetProfile_EmptyNameIsBadRequest(t *testing.T) {
	s, _ := newStore(&fakeProfiles{}, &fakePersonas{}, &fakeRunner{}, nil)
	if _, err := s.GetProfile(context.Background(), alice, ""); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("want ErrBadRequest, got %v", err)
	}
}

func TestGetProfile_ForbiddenPersonaDenies(t *testing.T) {
	pe := &fakePersonas{condErr: bstore.ErrForbidden}
	s, _ := newStore(&fakeProfiles{}, pe, &fakeRunner{}, nil)
	if _, err := s.GetProfile(context.Background(), alice, "bob-persona"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
}

func TestGetProfile_MissingPersonaIsNotFound(t *testing.T) {
	pe := &fakePersonas{condErr: bstore.ErrNotFound}
	s, _ := newStore(&fakeProfiles{}, pe, &fakeRunner{}, nil)
	if _, err := s.GetProfile(context.Background(), alice, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// No profile yet is 404 for a read (the O7 pre-synthesis state), unlike a test
// run where it is a 400 — the UI must distinguish "nothing to show yet".
func TestGetProfile_NoProfileYetIsNotFound(t *testing.T) {
	p := &fakeProfiles{getErr: review.ErrNotFound}
	pe := &fakePersonas{conditioning: bstore.PersonaConditioning{OwnerSubject: aliceSub}}
	s, _ := newStore(p, pe, &fakeRunner{}, nil)
	if _, err := s.GetProfile(context.Background(), alice, "ada"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// A read must never flip validation state.
func TestGetProfile_DoesNotValidateOrAudit(t *testing.T) {
	p := &fakeProfiles{profile: review.StyleProfile{OwnerSubject: aliceSub}}
	pe := &fakePersonas{conditioning: bstore.PersonaConditioning{OwnerSubject: aliceSub}}
	s, a := newStore(p, pe, &fakeRunner{}, nil)
	if _, err := s.GetProfile(context.Background(), alice, "ada"); err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if pe.setCalls != 0 {
		t.Fatalf("read must not write the Validated condition (setCalls=%d)", pe.setCalls)
	}
	if len(a.entries) != 0 {
		t.Fatalf("read must not audit (got %d entries)", len(a.entries))
	}
}

// --- Synthesize (ISI-7361 D6, O6 trigger) ---

func TestSynthesize_HappyPath_StampsConditioningFromPersona(t *testing.T) {
	pe := &fakePersonas{conditioning: bstore.PersonaConditioning{OwnerSubject: aliceSub, SelfDescription: "warm", PrimaryContentKind: "blog"}}
	synth := &fakeSynth{profile: review.StyleProfile{OwnerSubject: aliceSub, PersonaName: "ada", Traits: "t", SourceCount: 3}}
	s, aud := newStoreSynth(&fakeProfiles{}, pe, synth)
	sp, err := s.Synthesize(context.Background(), alice, "ada")
	if err != nil {
		t.Fatalf("Synthesize = %v, want nil", err)
	}
	if sp.Traits != "t" {
		t.Fatalf("unexpected profile %+v", sp)
	}
	if synth.req.OwnerSubject != aliceSub || synth.req.SelfDescription != "warm" || synth.req.PrimaryContentKind != "blog" {
		t.Fatalf("synthesize request not stamped from persona conditioning: %+v", synth.req)
	}
	if aud.last().Decision != audit.DecisionAllow {
		t.Fatalf("expected an allow audit entry")
	}
}

func TestSynthesize_MissingName(t *testing.T) {
	s, _ := newStoreSynth(&fakeProfiles{}, &fakePersonas{}, &fakeSynth{})
	if _, err := s.Synthesize(context.Background(), alice, ""); !IsBadRequest(err) {
		t.Fatalf("empty name = %v, want bad request", err)
	}
}

func TestSynthesize_NotConfiguredIsBadRequest(t *testing.T) {
	// A store built without a synthesizer rejects synthesis as a bad request
	// (the route is normally unmounted, but the guard is belt-and-suspenders).
	pe := &fakePersonas{conditioning: bstore.PersonaConditioning{OwnerSubject: aliceSub}}
	s, _ := newStore(&fakeProfiles{}, pe, &fakeRunner{}, nil)
	if _, err := s.Synthesize(context.Background(), alice, "ada"); !IsBadRequest(err) {
		t.Fatalf("no synth = %v, want bad request", err)
	}
	if s.CanSynthesize() {
		t.Fatalf("CanSynthesize should be false without a synthesizer")
	}
}

func TestSynthesize_ForbiddenDenies(t *testing.T) {
	pe := &fakePersonas{condErr: bstore.ErrForbidden}
	s, _ := newStoreSynth(&fakeProfiles{}, pe, &fakeSynth{})
	if _, err := s.Synthesize(context.Background(), alice, "bob-persona"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("forbidden = %v, want ErrForbidden", err)
	}
}

func TestSynthesize_UpstreamErrorAuditsDeny(t *testing.T) {
	pe := &fakePersonas{conditioning: bstore.PersonaConditioning{OwnerSubject: aliceSub}}
	synth := &fakeSynth{err: errors.New("gateway down")}
	s, aud := newStoreSynth(&fakeProfiles{}, pe, synth)
	if _, err := s.Synthesize(context.Background(), alice, "ada"); err == nil {
		t.Fatal("expected a synthesis error")
	}
	if aud.last().Decision != audit.DecisionDeny {
		t.Fatalf("expected a deny audit entry on synthesis failure")
	}
}

func TestCanSynthesize_TrueWhenWired(t *testing.T) {
	s, _ := newStoreSynth(&fakeProfiles{}, &fakePersonas{}, &fakeSynth{})
	if !s.CanSynthesize() {
		t.Fatal("CanSynthesize should be true when a synthesizer is wired")
	}
}
