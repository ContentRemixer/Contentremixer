package store

import (
	"context"
	"errors"
	"testing"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/booth/internal/audit"
)

func persona(name, owner string) *v1alpha1.DevRelPersona {
	return &v1alpha1.DevRelPersona{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: v1alpha1.DevRelPersonaSpec{
			Owner:              v1alpha1.OwnerRef{Subject: owner},
			SelfDescription:    "friendly infra voice",
			PrimaryContentKind: v1alpha1.PrimaryContentBlog,
		},
	}
}

// newPersonaStore builds a store whose fake client supports the status
// subresource for DevRelPersona (required for Status().Update).
func newPersonaStore(t *testing.T, objs ...client.Object) (*Store, client.Client, *captureAuditor) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&v1alpha1.DevRelPersona{}).
		Build()
	cap := &captureAuditor{}
	return New(c, ns, cap), c, cap
}

func TestGetPersonaConditioning_OwnerAndAdmin(t *testing.T) {
	s, _, _ := newPersonaStore(t, persona("ada", aliceSub))

	pc, err := s.GetPersonaConditioning(context.Background(), alice, "ada")
	if err != nil {
		t.Fatalf("owner lookup: %v", err)
	}
	if pc.OwnerSubject != aliceSub || pc.SelfDescription != "friendly infra voice" || pc.PrimaryContentKind != "blog" {
		t.Fatalf("conditioning = %+v", pc)
	}
	// Admin may read any persona's conditioning (owner echoed, not the admin).
	pcAdmin, err := s.GetPersonaConditioning(context.Background(), admin, "ada")
	if err != nil || pcAdmin.OwnerSubject != aliceSub {
		t.Fatalf("admin lookup = %+v err=%v", pcAdmin, err)
	}
}

func TestGetPersonaConditioning_ForbiddenForOtherOwner(t *testing.T) {
	s, _, _ := newPersonaStore(t, persona("bob-persona", bobSub))
	if _, err := s.GetPersonaConditioning(context.Background(), alice, "bob-persona"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
}

func TestGetPersonaConditioning_NotFound(t *testing.T) {
	s, _, _ := newPersonaStore(t)
	if _, err := s.GetPersonaConditioning(context.Background(), alice, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestSetPersonaValidated_WritesCondition(t *testing.T) {
	s, c, cap := newPersonaStore(t, persona("ada", aliceSub))

	if err := s.SetPersonaValidated(context.Background(), alice, "ada", true); err != nil {
		t.Fatalf("SetPersonaValidated: %v", err)
	}
	var got v1alpha1.DevRelPersona
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: "ada"}, &got); err != nil {
		t.Fatalf("get back: %v", err)
	}
	cond := apimeta.FindStatusCondition(got.Status.Conditions, v1alpha1.PersonaConditionValidated)
	if cond == nil {
		t.Fatal("Validated condition not set")
	}
	if cond.Status != metav1.ConditionTrue || cond.Reason != v1alpha1.PersonaReasonProfileValidated {
		t.Fatalf("condition = %+v", cond)
	}
	if e := cap.last(); e.Verb != verbValidate || e.Decision != audit.DecisionAllow {
		t.Fatalf("audit = %+v", e)
	}
}

func TestSetPersonaValidated_FalseUsesSynthesizedReason(t *testing.T) {
	s, c, _ := newPersonaStore(t, persona("ada", aliceSub))
	if err := s.SetPersonaValidated(context.Background(), alice, "ada", false); err != nil {
		t.Fatalf("SetPersonaValidated: %v", err)
	}
	var got v1alpha1.DevRelPersona
	_ = c.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: "ada"}, &got)
	cond := apimeta.FindStatusCondition(got.Status.Conditions, v1alpha1.PersonaConditionValidated)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != v1alpha1.PersonaReasonProfileSynthesized {
		t.Fatalf("condition = %+v", cond)
	}
}

func TestSetPersonaValidated_ForbiddenForOtherOwner(t *testing.T) {
	s, _, cap := newPersonaStore(t, persona("bob-persona", bobSub))
	if err := s.SetPersonaValidated(context.Background(), alice, "bob-persona", true); !errors.Is(err, ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
	if cap.last().Decision != audit.DecisionDeny {
		t.Fatalf("expected deny audit")
	}
}

func TestSetPersonaValidated_NotFound(t *testing.T) {
	s, _, _ := newPersonaStore(t)
	if err := s.SetPersonaValidated(context.Background(), alice, "ghost", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
