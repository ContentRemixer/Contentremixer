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

func newStore(p *fakeProfiles, pe *fakePersonas, r *fakeRunner, lim rateGuard) (*Store, *capAuditor) {
	a := &capAuditor{}
	return New(p, pe, r, lim, a), a
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
