package promote

import (
	"context"
	"errors"
	"testing"

	apppromote "github.com/ContentRemixer/Contentremixer/app/promote"
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
	profile  review.StyleProfile
	getErr   error
	gotOwner string
}

func (f *fakeProfiles) GetStyleProfile(_ context.Context, owner, _ string) (review.StyleProfile, error) {
	f.gotOwner = owner
	if f.getErr != nil {
		return review.StyleProfile{}, f.getErr
	}
	return f.profile, nil
}

type fakePersonas struct {
	conditioning bstore.PersonaConditioning
	condErr      error
}

func (f *fakePersonas) GetPersonaConditioning(_ context.Context, _ auth.Session, _ string) (bstore.PersonaConditioning, error) {
	if f.condErr != nil {
		return bstore.PersonaConditioning{}, f.condErr
	}
	return f.conditioning, nil
}

type fakeSuggester struct {
	req apppromote.Request
	res apppromote.Suggestion
	err error
}

func (f *fakeSuggester) Suggest(_ context.Context, req apppromote.Request) (apppromote.Suggestion, error) {
	f.req = req
	return f.res, f.err
}

type capAuditor struct{ entries []audit.Entry }

func (c *capAuditor) Log(_ context.Context, e audit.Entry) { c.entries = append(c.entries, e) }
func (c *capAuditor) last() audit.Entry                    { return c.entries[len(c.entries)-1] }

type denyLimiter struct{}

func (denyLimiter) Allow(string) bool { return false }

func newStore(p *fakeProfiles, pe *fakePersonas, s *fakeSuggester, lim rateGuard) (*Store, *capAuditor) {
	a := &capAuditor{}
	return New(p, pe, s, lim, a), a
}

// validated builds a validated StyleProfile owned by owner.
func validated(owner, persona, traits string) review.StyleProfile {
	return review.StyleProfile{OwnerSubject: owner, PersonaName: persona, Traits: traits, Validated: true}
}

// --- Suggest ---

func TestSuggest_HappyPath_ConditionsAndPassesContent(t *testing.T) {
	p := &fakeProfiles{profile: validated(aliceSub, "ada", "dry wit")}
	pe := &fakePersonas{conditioning: bstore.PersonaConditioning{OwnerSubject: aliceSub, SelfDescription: "friendly", PrimaryContentKind: "blog"}}
	s := &fakeSuggester{res: apppromote.Suggestion{Hook: "h", Body: "b", CTA: "c", Post: "h\n\nb\n\nc", ModelUsed: "gpt-4o"}}
	st, aud := newStore(p, pe, s, nil)

	out, err := st.Suggest(context.Background(), alice, Request{PersonaName: "ada", Content: "my blog", Title: "T", URL: "https://x/y"})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if out.Hook != "h" || out.Body != "b" || out.CTA != "c" || out.Post != "h\n\nb\n\nc" || out.ModelUsed != "gpt-4o" {
		t.Fatalf("result = %+v", out)
	}
	if s.req.Content != "my blog" || s.req.Title != "T" || s.req.URL != "https://x/y" {
		t.Fatalf("content/title/url not passed: %+v", s.req)
	}
	if s.req.StyleTraits != "dry wit" || s.req.SelfDescription != "friendly" || s.req.PrimaryContentKind != "blog" {
		t.Fatalf("suggester request not conditioned: %+v", s.req)
	}
	if e := aud.last(); e.Verb != verbPromote || e.Decision != audit.DecisionAllow {
		t.Fatalf("audit = %+v", e)
	}
}

func TestSuggest_MissingFields(t *testing.T) {
	st, _ := newStore(&fakeProfiles{}, &fakePersonas{}, &fakeSuggester{}, nil)
	if _, err := st.Suggest(context.Background(), alice, Request{Content: "x"}); !IsBadRequest(err) {
		t.Fatalf("want bad request for missing persona, got %v", err)
	}
	if _, err := st.Suggest(context.Background(), alice, Request{PersonaName: "ada"}); !IsBadRequest(err) {
		t.Fatalf("want bad request for missing content, got %v", err)
	}
}

func TestSuggest_ForbiddenPersonaDenies(t *testing.T) {
	pe := &fakePersonas{condErr: bstore.ErrForbidden}
	st, _ := newStore(&fakeProfiles{}, pe, &fakeSuggester{}, nil)
	if _, err := st.Suggest(context.Background(), alice, Request{PersonaName: "bob-persona", Content: "x"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
}

func TestSuggest_NotFoundPersona(t *testing.T) {
	pe := &fakePersonas{condErr: bstore.ErrNotFound}
	st, _ := newStore(&fakeProfiles{}, pe, &fakeSuggester{}, nil)
	if _, err := st.Suggest(context.Background(), alice, Request{PersonaName: "ghost", Content: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestSuggest_NoProfileYetIsBadRequest(t *testing.T) {
	p := &fakeProfiles{getErr: review.ErrNotFound}
	pe := &fakePersonas{conditioning: bstore.PersonaConditioning{OwnerSubject: aliceSub}}
	s := &fakeSuggester{}
	st, aud := newStore(p, pe, s, nil)
	if _, err := st.Suggest(context.Background(), alice, Request{PersonaName: "ada", Content: "x"}); !IsBadRequest(err) {
		t.Fatalf("want bad request, got %v", err)
	}
	if s.req.Content != "" {
		t.Fatalf("suggester must not run without a profile")
	}
	if aud.last().Decision != audit.DecisionDeny {
		t.Fatalf("expected deny audit for missing profile")
	}
}

// The defining precondition of ISI-7369: promote is the step AFTER validate, so
// an unvalidated profile is a 409 and the generator never runs.
func TestSuggest_UnvalidatedProfileIsConflict(t *testing.T) {
	p := &fakeProfiles{profile: review.StyleProfile{OwnerSubject: aliceSub, PersonaName: "ada", Traits: "t", Validated: false}}
	pe := &fakePersonas{conditioning: bstore.PersonaConditioning{OwnerSubject: aliceSub}}
	s := &fakeSuggester{}
	st, aud := newStore(p, pe, s, nil)
	if _, err := st.Suggest(context.Background(), alice, Request{PersonaName: "ada", Content: "x"}); !IsNotValidated(err) {
		t.Fatalf("want ErrNotValidated, got %v", err)
	}
	if s.req.Content != "" {
		t.Fatalf("suggester must not run for an unvalidated persona")
	}
	if aud.last().Decision != audit.DecisionDeny {
		t.Fatalf("expected deny audit for unvalidated persona")
	}
}

func TestSuggest_RateLimitedBeforeGeneration(t *testing.T) {
	p := &fakeProfiles{profile: validated(aliceSub, "ada", "t")}
	pe := &fakePersonas{conditioning: bstore.PersonaConditioning{OwnerSubject: aliceSub}}
	s := &fakeSuggester{res: apppromote.Suggestion{Post: "x"}}
	st, aud := newStore(p, pe, s, denyLimiter{})
	if _, err := st.Suggest(context.Background(), alice, Request{PersonaName: "ada", Content: "x"}); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("want ErrRateLimited, got %v", err)
	}
	if s.req.Content != "" {
		t.Fatalf("generator must not run when rate-limited")
	}
	if aud.last().Decision != audit.DecisionDeny {
		t.Fatalf("expected deny audit on rate limit")
	}
}

func TestSuggest_GeneratorErrorAuditsDeny(t *testing.T) {
	p := &fakeProfiles{profile: validated(aliceSub, "ada", "t")}
	pe := &fakePersonas{conditioning: bstore.PersonaConditioning{OwnerSubject: aliceSub}}
	s := &fakeSuggester{err: errors.New("gateway down")}
	st, aud := newStore(p, pe, s, nil)
	if _, err := st.Suggest(context.Background(), alice, Request{PersonaName: "ada", Content: "x"}); err == nil {
		t.Fatal("expected generator error")
	}
	if aud.last().Decision != audit.DecisionDeny {
		t.Fatalf("expected deny audit on generator error")
	}
}

// Admin promoting another DevRel's persona: the StyleProfile is loaded under the
// persona's REAL owner (from conditioning), not the admin's subject.
func TestSuggest_AdminUsesPersonaOwnerForProfile(t *testing.T) {
	p := &fakeProfiles{profile: validated(bobSub, "bob-persona", "t")}
	pe := &fakePersonas{conditioning: bstore.PersonaConditioning{OwnerSubject: bobSub}}
	s := &fakeSuggester{res: apppromote.Suggestion{Post: "p"}}
	st, _ := newStore(p, pe, s, nil)
	if _, err := st.Suggest(context.Background(), admin, Request{PersonaName: "bob-persona", Content: "x"}); err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if p.gotOwner != bobSub {
		t.Fatalf("profile loaded under owner %q, want %q", p.gotOwner, bobSub)
	}
	if s.req.StyleTraits != "t" {
		t.Fatalf("profile not loaded under persona owner")
	}
}
