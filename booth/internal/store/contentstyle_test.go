package store

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/booth/internal/audit"
)

func cs(name, guide string) *v1alpha1.ContentStyle {
	return &v1alpha1.ContentStyle{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       v1alpha1.ContentStyleSpec{OutputFormat: name, Guide: guide},
	}
}

// ContentStyleKind is the admin-only, platform-owned class (ISI-7965 W3): the
// guides are global config with no owner subject, so only clause 1 (Admin)
// admits — a DevRel is filtered from List and forbidden on Get/Create.
func TestContentStyleKindAdminOnly(t *testing.T) {
	seed := []client.Object{cs("blog", "# Blog guide"), cs("short", "# Short guide")}

	t.Run("admin lists all styles", func(t *testing.T) {
		st, _ := newStore(t, seed...)
		got, err := st.List(context.Background(), admin, ContentStyleKind)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Fatalf("admin should see both styles, got %v", names(got))
		}
	})

	t.Run("devrel lists none and audits deny", func(t *testing.T) {
		st, aud := newStore(t, seed...)
		got, err := st.List(context.Background(), alice, ContentStyleKind)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("devrel should see no styles, got %v", names(got))
		}
		if aud.last().Decision != audit.DecisionDeny {
			t.Errorf("devrel list of ContentStyle should audit a deny")
		}
	})

	t.Run("devrel get forbidden", func(t *testing.T) {
		st, _ := newStore(t, seed...)
		if _, err := st.Get(context.Background(), alice, ContentStyleKind, "blog"); err != ErrForbidden {
			t.Fatalf("want ErrForbidden, got %v", err)
		}
	})

	t.Run("devrel create forbidden, admin create allowed", func(t *testing.T) {
		st, _ := newStore(t, seed...)
		if err := st.Create(context.Background(), alice, ContentStyleKind, cs("tutorial", "# T")); err != ErrForbidden {
			t.Fatalf("want ErrForbidden, got %v", err)
		}
		if err := st.Create(context.Background(), admin, ContentStyleKind, cs("tutorial", "# T")); err != nil {
			t.Fatalf("admin create should pass, got %v", err)
		}
	})
}
