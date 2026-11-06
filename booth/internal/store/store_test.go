package store

import (
	"context"
	"errors"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/booth/auth"
	"github.com/ContentRemixer/Contentremixer/booth/internal/audit"
)

const ns = "content-remixer"

const (
	adminSub = "sub-admin"
	aliceSub = "sub-devrel-alice"
	bobSub   = "sub-devrel-bob"
)

var (
	admin = auth.Session{Subject: adminSub, Role: auth.RoleAdmin}
	alice = auth.Session{Subject: aliceSub, Role: auth.RoleDevRel}
)

// ownerScopedKind reuses ModelProfile storage but is marked NOT admin-only, so
// the store tests can exercise the clause-2 (ownership) matrix that the Phase-2
// DevRelPersona kind will take. It proves the store applies §6.3 uniformly for
// owner-scoped classes; ModelProfileKind/ReferenceRepoKind cover the admin-only
// classes as shipped.
var ownerScopedKind = ResourceKind{
	Kind:      "OwnerScoped",
	AdminOnly: false,
	newObject: ModelProfileKind.newObject,
	newList:   ModelProfileKind.newList,
	ownerOf:   ModelProfileKind.ownerOf,
	items:     ModelProfileKind.items,
}

func mp(name, owner string) *v1alpha1.ModelProfile {
	return &v1alpha1.ModelProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: v1alpha1.ModelProfileSpec{
			Provider:    "openai",
			Model:       "gpt-4o",
			LiteLLMName: name,
			Owner:       v1alpha1.OwnerRef{Subject: owner},
		},
	}
}

func newStore(t *testing.T, objs ...client.Object) (*Store, *captureAuditor) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	cap := &captureAuditor{}
	return New(c, ns, cap), cap
}

type captureAuditor struct{ entries []audit.Entry }

func (c *captureAuditor) Log(_ context.Context, e audit.Entry) { c.entries = append(c.entries, e) }

func (c *captureAuditor) last() audit.Entry { return c.entries[len(c.entries)-1] }

// --- List: server-side filtering by §6.3 ---

func TestList(t *testing.T) {
	seed := []client.Object{mp("owned-by-alice", aliceSub), mp("owned-by-bob", bobSub), mp("platform", "")}

	t.Run("admin sees all on owner-scoped", func(t *testing.T) {
		st, _ := newStore(t, seed...)
		got, err := st.List(context.Background(), admin, ownerScopedKind)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 3 {
			t.Fatalf("admin should see all 3, got %d", len(got))
		}
	})

	t.Run("devrel sees only own on owner-scoped", func(t *testing.T) {
		st, _ := newStore(t, seed...)
		got, err := st.List(context.Background(), alice, ownerScopedKind)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].GetName() != "owned-by-alice" {
			t.Fatalf("alice should see only her row, got %v", names(got))
		}
	})

	t.Run("devrel sees none on admin-only class", func(t *testing.T) {
		st, aud := newStore(t, seed...)
		got, err := st.List(context.Background(), alice, ModelProfileKind)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("alice should see nothing on admin-only class, got %v", names(got))
		}
		if aud.last().Decision != audit.DecisionDeny {
			t.Errorf("devrel list of admin-only class should audit a deny")
		}
	})

	t.Run("admin sees all on admin-only class", func(t *testing.T) {
		st, _ := newStore(t, seed...)
		got, err := st.List(context.Background(), admin, ModelProfileKind)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 3 {
			t.Fatalf("admin should see all on admin-only class, got %d", len(got))
		}
	})
}

// --- Get / Delete / Approve: the read+write single-object matrix ---

func TestGetMatrix(t *testing.T) {
	cases := []struct {
		name    string
		sess    auth.Session
		kind    ResourceKind
		target  string
		wantErr error
	}{
		{"admin gets platform-owned admin-only", admin, ModelProfileKind, "platform", nil},
		{"devrel 403 on admin-only even if owner matches", alice, ModelProfileKind, "owned-by-alice", ErrForbidden},
		{"devrel gets own owner-scoped", alice, ownerScopedKind, "owned-by-alice", nil},
		{"devrel 403 on unowned owner-scoped", alice, ownerScopedKind, "owned-by-bob", ErrForbidden},
		{"devrel 403 on empty-owner owner-scoped", alice, ownerScopedKind, "platform", ErrForbidden},
		{"admin gets any owner-scoped", admin, ownerScopedKind, "owned-by-bob", nil},
		{"404 on missing", admin, ownerScopedKind, "nope", ErrNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, _ := newStore(t, mp("owned-by-alice", aliceSub), mp("owned-by-bob", bobSub), mp("platform", ""))
			_, err := st.Get(context.Background(), tc.sess, tc.kind, tc.target)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Get err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestDeleteAndApproveMatrix(t *testing.T) {
	cases := []struct {
		name    string
		sess    auth.Session
		kind    ResourceKind
		target  string
		wantErr error
	}{
		{"devrel deletes own", alice, ownerScopedKind, "owned-by-alice", nil},
		{"devrel 403 delete unowned", alice, ownerScopedKind, "owned-by-bob", ErrForbidden},
		{"devrel 403 delete admin-only", alice, ModelProfileKind, "owned-by-alice", ErrForbidden},
		{"admin deletes platform", admin, ModelProfileKind, "platform", nil},
	}
	for _, tc := range cases {
		t.Run("delete/"+tc.name, func(t *testing.T) {
			st, _ := newStore(t, mp("owned-by-alice", aliceSub), mp("owned-by-bob", bobSub), mp("platform", ""))
			err := st.Delete(context.Background(), tc.sess, tc.kind, tc.target)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Delete err = %v, want %v", err, tc.wantErr)
			}
		})
		t.Run("approve/"+tc.name, func(t *testing.T) {
			st, aud := newStore(t, mp("owned-by-alice", aliceSub), mp("owned-by-bob", bobSub), mp("platform", ""))
			err := st.Approve(context.Background(), tc.sess, tc.kind, tc.target)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Approve err = %v, want %v", err, tc.wantErr)
			}
			if aud.last().Verb != "approve" {
				t.Errorf("approve should audit verb=approve, got %q", aud.last().Verb)
			}
		})
	}
}

// --- Create: evaluated against the object's own declared owner ---

func TestCreateMatrix(t *testing.T) {
	cases := []struct {
		name    string
		sess    auth.Session
		kind    ResourceKind
		owner   string
		wantErr error
	}{
		{"devrel creates owned-by-self", alice, ownerScopedKind, aliceSub, nil},
		{"devrel 403 create owned-by-other", alice, ownerScopedKind, bobSub, ErrForbidden},
		{"devrel 403 create empty-owner", alice, ownerScopedKind, "", ErrForbidden},
		{"devrel 403 create admin-only", alice, ModelProfileKind, aliceSub, ErrForbidden},
		{"admin creates platform admin-only", admin, ModelProfileKind, "", nil},
		{"admin creates owned-for-devrel", admin, ownerScopedKind, aliceSub, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, _ := newStore(t)
			err := st.Create(context.Background(), tc.sess, tc.kind, mp("new-one", tc.owner))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Create err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// --- Update: gated against BOTH current and incoming owner ---

func TestUpdateReHomeForbidden(t *testing.T) {
	st, _ := newStore(t, mp("owned-by-alice", aliceSub))
	// Alice tries to re-home her resource to Bob — denied on the incoming owner.
	err := st.Update(context.Background(), alice, ownerScopedKind, mp("owned-by-alice", bobSub))
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("re-home to another owner should be forbidden, got %v", err)
	}
}

func TestUpdateOwnOK(t *testing.T) {
	st, _ := newStore(t, mp("owned-by-alice", aliceSub))
	updated := mp("owned-by-alice", aliceSub)
	updated.Spec.Model = "gpt-4o-mini"
	if err := st.Update(context.Background(), alice, ownerScopedKind, updated); err != nil {
		t.Fatalf("alice updating her own resource should succeed, got %v", err)
	}
}

func TestUpdateUnownedForbidden(t *testing.T) {
	st, _ := newStore(t, mp("owned-by-bob", bobSub))
	err := st.Update(context.Background(), alice, ownerScopedKind, mp("owned-by-bob", bobSub))
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("alice updating bob's resource should be forbidden, got %v", err)
	}
}

func TestUpdateMissingIsNotFound(t *testing.T) {
	st, _ := newStore(t)
	err := st.Update(context.Background(), admin, ownerScopedKind, mp("ghost", ""))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("update of missing resource should be 404, got %v", err)
	}
}

func TestListEmpty(t *testing.T) {
	st, _ := newStore(t)
	got, err := st.List(context.Background(), admin, ownerScopedKind)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("empty namespace should list nothing, got %d", len(got))
	}
}

// --- Audit: every mutation logs the §6.4 fields ---

func TestAuditRecordsMutation(t *testing.T) {
	st, aud := newStore(t)
	_ = st.Create(context.Background(), admin, ModelProfileKind, mp("m1", ""))
	e := aud.last()
	if e.Subject != adminSub || e.Role != string(auth.RoleAdmin) || e.Verb != "create" ||
		e.ResourceKind != "ModelProfile" || e.ResourceName != "m1" || e.Decision != audit.DecisionAllow {
		t.Fatalf("unexpected audit entry: %+v", e)
	}
}

func names(objs []client.Object) []string {
	out := make([]string, len(objs))
	for i, o := range objs {
		out[i] = o.GetName()
	}
	return out
}
