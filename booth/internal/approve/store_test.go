package approve

import (
	"context"
	"errors"
	"testing"

	"github.com/ContentRemixer/Contentremixer/booth/auth"
	"github.com/ContentRemixer/Contentremixer/booth/internal/audit"
	bstore "github.com/ContentRemixer/Contentremixer/booth/internal/store"
)

// --- fakes -----------------------------------------------------------------

// fakeGate stands in for the §6.3-gated persona read. err models what the real
// store returns: ErrForbidden (unowned), ErrNotFound (missing), or nil (owned/
// Admin-admitted).
type fakeGate struct {
	err error
}

func (f fakeGate) GetPersonaConditioning(context.Context, auth.Session, string) (bstore.PersonaConditioning, error) {
	return bstore.PersonaConditioning{}, f.err
}

// fakeReflector records the reflection call so tests can assert the gate
// reflects only when authorized, and with the right arguments.
type fakeReflector struct {
	calls  int
	gotAp  Approval
	gotPR  int
	gotRep string
	out    Reflection
	err    error
}

func (f *fakeReflector) Reflect(_ context.Context, repo string, prNumber int, ap Approval) (Reflection, error) {
	f.calls++
	f.gotRep, f.gotPR, f.gotAp = repo, prNumber, ap
	return f.out, f.err
}

// capAudit captures the last audit entry.
type capAudit struct{ last audit.Entry }

func (c *capAudit) Log(_ context.Context, e audit.Entry) { c.last = e }

func devrel(sub string) auth.Session { return auth.Session{Subject: sub, Role: auth.RoleDevRel} }
func admin(sub string) auth.Session  { return auth.Session{Subject: sub, Role: auth.RoleAdmin} }

func req(persona string) Request {
	return Request{Repo: "ContentRemixer/Contentremixer", PRNumber: 42, PersonaName: persona, Note: "lgtm"}
}

// --- the §6.3 approve matrix ------------------------------------------------

func TestApproveAuthorizationMatrix(t *testing.T) {
	cases := []struct {
		name        string
		sess        auth.Session
		persona     string
		gateErr     error
		wantErr     error // nil = allowed
		wantReflect bool
	}{
		{"admin approves named persona PR", admin("sub-admin"), "ada", nil, nil, true},
		{"admin approves platform PR (empty persona)", admin("sub-admin"), "", nil, nil, true},
		{"devrel approves own persona PR", devrel("sub-alice"), "ada", nil, nil, true},
		{"devrel approves unowned persona PR", devrel("sub-bob"), "ada", bstore.ErrForbidden, ErrForbidden, false},
		{"devrel approves platform PR is admin-only", devrel("sub-alice"), "", nil, ErrForbidden, false},
		{"persona does not exist", devrel("sub-alice"), "ghost", bstore.ErrNotFound, ErrNotFound, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			refl := &fakeReflector{out: Reflection{Mode: ModeApprove, ReviewID: 1, URL: "u"}}
			aud := &capAudit{}
			s := New(fakeGate{err: tc.gateErr}, refl, aud)

			_, err := s.Approve(context.Background(), tc.sess, req(tc.persona))

			if tc.wantErr == nil && err != nil {
				t.Fatalf("want allowed, got err %v", err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("want err %v, got %v", tc.wantErr, err)
			}
			if got := refl.calls > 0; got != tc.wantReflect {
				t.Fatalf("reflect called = %v, want %v", got, tc.wantReflect)
			}
			// §6.4: every decision is audited with the approve verb on a PR.
			wantDecision := audit.DecisionAllow
			if tc.wantErr != nil {
				wantDecision = audit.DecisionDeny
			}
			if aud.last.Verb != verbApprove || aud.last.ResourceKind != kindPR {
				t.Fatalf("audit = %+v, want verb=approve kind=PullRequest", aud.last)
			}
			if aud.last.Decision != wantDecision {
				t.Fatalf("audit decision = %q, want %q", aud.last.Decision, wantDecision)
			}
		})
	}
}

// The reflector must receive the PR coordinates and a stable per-(PR, approver)
// idempotency key, plus the approver identity for attribution.
func TestApprovePassesStableKeyAndIdentity(t *testing.T) {
	refl := &fakeReflector{out: Reflection{Mode: ModeApprove, ReviewID: 7, URL: "https://gh/r/pull/42#r7"}}
	s := New(fakeGate{}, refl, &capAudit{})

	out, err := s.Approve(context.Background(), devrel("sub-alice"), req("ada"))
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if refl.gotRep != "ContentRemixer/Contentremixer" || refl.gotPR != 42 {
		t.Fatalf("reflector got %s#%d, want ContentRemixer/Contentremixer#42", refl.gotRep, refl.gotPR)
	}
	if want := "ContentRemixer/Contentremixer#42:sub-alice"; refl.gotAp.Key != want {
		t.Fatalf("idempotency key = %q, want %q", refl.gotAp.Key, want)
	}
	if refl.gotAp.Subject != "sub-alice" || refl.gotAp.Role != string(auth.RoleDevRel) {
		t.Fatalf("approver attribution = %q/%q, want sub-alice/DevRel", refl.gotAp.Subject, refl.gotAp.Role)
	}
	if out.Mode != string(ModeApprove) || out.ReviewID != 7 || out.Approver != "sub-alice" {
		t.Fatalf("result = %+v", out)
	}
}

// A COMMENT-fallback reflection surfaces its mode; an idempotent reflection
// surfaces idempotent=true. Both still count as success.
func TestApproveSurfacesReflectionMode(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  Reflection
		mode string
		idem bool
	}{
		{"comment fallback", Reflection{Mode: ModeComment, ReviewID: 2}, "comment", false},
		{"idempotent reuse", Reflection{Mode: ModeApprove, ReviewID: 3, Idempotent: true}, "approve", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New(fakeGate{}, &fakeReflector{out: tc.out}, &capAudit{})
			out, err := s.Approve(context.Background(), admin("a"), req("ada"))
			if err != nil {
				t.Fatalf("approve: %v", err)
			}
			if out.Mode != tc.mode || out.Idempotent != tc.idem {
				t.Fatalf("result mode=%q idempotent=%v, want %q/%v", out.Mode, out.Idempotent, tc.mode, tc.idem)
			}
		})
	}
}

func TestApproveBadRequest(t *testing.T) {
	refl := &fakeReflector{}
	s := New(fakeGate{}, refl, &capAudit{})
	for _, bad := range []Request{
		{Repo: "", PRNumber: 1, PersonaName: "ada"},
		{Repo: "noslash", PRNumber: 1, PersonaName: "ada"},
		{Repo: "o/r", PRNumber: 0, PersonaName: "ada"},
	} {
		if _, err := s.Approve(context.Background(), admin("a"), bad); !errors.Is(err, ErrBadRequest) {
			t.Fatalf("req %+v: want ErrBadRequest, got %v", bad, err)
		}
	}
	if refl.calls != 0 {
		t.Fatalf("reflector must not run on a bad request (ran %d times)", refl.calls)
	}
}

// A reflection fault after authorization passed is ErrReflect (→ 502), and the
// mutation is audited as a denied (un-recorded) approval.
func TestApproveReflectFailureIsErrReflect(t *testing.T) {
	refl := &fakeReflector{err: errors.New("github 500")}
	aud := &capAudit{}
	s := New(fakeGate{}, refl, aud)
	if _, err := s.Approve(context.Background(), admin("a"), req("ada")); !errors.Is(err, ErrReflect) {
		t.Fatalf("want ErrReflect, got %v", err)
	}
	if aud.last.Decision != audit.DecisionDeny {
		t.Fatalf("reflect failure audited as %q, want deny", aud.last.Decision)
	}
}
