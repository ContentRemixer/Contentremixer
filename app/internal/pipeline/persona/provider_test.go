package persona

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

func personaScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := contentremixerv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	return s
}

func newPersonaCR(name, owner, selfDesc string, ready bool) *contentremixerv1alpha1.DevRelPersona {
	cr := &contentremixerv1alpha1.DevRelPersona{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "contentremixer"},
		Spec: contentremixerv1alpha1.DevRelPersonaSpec{
			Owner:           contentremixerv1alpha1.OwnerRef{Subject: owner},
			SelfDescription: selfDesc,
			Retrieval:       contentremixerv1alpha1.RetrievalSpec{TopK: 8, MinScore: "0.25"},
		},
	}
	if ready {
		cr.Status.Conditions = []metav1.Condition{{
			Type:    contentremixerv1alpha1.PersonaConditionReady,
			Status:  metav1.ConditionTrue,
			Reason:  "Embedded",
			Message: "ready",
		}}
	}
	return cr
}

func TestCRProvider_LookupHappyPath(t *testing.T) {
	cr := newPersonaCR("henrik-otel", "henrik", "dry, example-first", true)
	cl := fake.NewClientBuilder().WithScheme(personaScheme(t)).WithObjects(cr).Build()
	p := &CRPersonaProvider{Reader: cl, Namespace: "contentremixer"}

	profile, err := p.Lookup(context.Background(), "henrik", "henrik-otel")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if profile.SelfDescription != "dry, example-first" {
		t.Fatalf("SelfDescription = %q", profile.SelfDescription)
	}
	if profile.TopK != 8 || profile.MinScore != 0.25 {
		t.Fatalf("retrieval tuning = TopK %d MinScore %v", profile.TopK, profile.MinScore)
	}
	if !profile.Ready {
		t.Fatalf("expected Ready true from the Ready condition")
	}
}

func TestCRProvider_ReadyViaPhaseFallback(t *testing.T) {
	cr := newPersonaCR("henrik-otel", "henrik", "dry", false)
	cr.Status.Phase = contentremixerv1alpha1.PersonaPhaseReady
	cl := fake.NewClientBuilder().WithScheme(personaScheme(t)).WithObjects(cr).Build()
	p := &CRPersonaProvider{Reader: cl, Namespace: "contentremixer"}

	profile, err := p.Lookup(context.Background(), "henrik", "henrik-otel")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !profile.Ready {
		t.Fatalf("expected Ready true from phase fallback")
	}
}

func TestCRProvider_OwnerMismatchRefused(t *testing.T) {
	cr := newPersonaCR("henrik-otel", "henrik", "dry", true)
	cl := fake.NewClientBuilder().WithScheme(personaScheme(t)).WithObjects(cr).Build()
	p := &CRPersonaProvider{Reader: cl, Namespace: "contentremixer"}

	_, err := p.Lookup(context.Background(), "someone-else", "henrik-otel")
	if err == nil {
		t.Fatalf("expected cross-owner refusal")
	}
}

func TestCRProvider_NotFound(t *testing.T) {
	cl := fake.NewClientBuilder().WithScheme(personaScheme(t)).Build()
	p := &CRPersonaProvider{Reader: cl, Namespace: "contentremixer"}
	if _, err := p.Lookup(context.Background(), "henrik", "ghost"); err == nil {
		t.Fatalf("expected a not-found error")
	}
}

func TestParseMinScore(t *testing.T) {
	cases := map[string]float64{
		"":        0,
		"0":       0,
		"0.5":     0.5,
		"1":       1,
		"garbage": 0,
		"-0.3":    0, // negative floors clamp to 0 (best-effort)
	}
	for in, want := range cases {
		if got := parseMinScore(in); got != want {
			t.Errorf("parseMinScore(%q) = %v, want %v", in, got, want)
		}
	}
}
