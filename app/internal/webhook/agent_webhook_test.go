package webhook

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

func agentScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := contentremixerv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	return s
}

func agent(name string, kind contentremixerv1alpha1.SkillKind, isDefault bool) *contentremixerv1alpha1.Agent {
	return &contentremixerv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "contentremixer"},
		Spec: contentremixerv1alpha1.AgentSpec{
			Role:      "Role " + name,
			SkillKind: kind,
			Default:   isDefault,
		},
	}
}

func agentValidator(t *testing.T, existing ...client.Object) *AgentCustomValidator {
	t.Helper()
	cl := fake.NewClientBuilder().WithScheme(agentScheme(t)).WithObjects(existing...).Build()
	return &AgentCustomValidator{Client: cl}
}

func TestAgentValidate_SkillKind(t *testing.T) {
	v := agentValidator(t)
	if _, err := v.ValidateCreate(context.Background(), agent("coding-fast", contentremixerv1alpha1.SkillKindCoding, false)); err != nil {
		t.Fatalf("valid skillKind rejected: %v", err)
	}
	_, err := v.ValidateCreate(context.Background(), agent("bogus", "nonsense", false))
	if err == nil || !strings.Contains(err.Error(), "skillKind") {
		t.Fatalf("unknown skillKind must fail on spec.skillKind, got %v", err)
	}
}

func TestAgentValidate_DefaultPerKind(t *testing.T) {
	// A seeded default coding Agent already exists. A persisted object carries a
	// UID (the apiserver assigns it); the incoming create does not yet, so the
	// self-skip never false-matches a real create against a stored row.
	existing := agent("coding", contentremixerv1alpha1.SkillKindCoding, true)
	existing.UID = "coding-uid"
	v := agentValidator(t, existing)

	// A second default for the SAME kind is rejected.
	dupe := agent("coding-fast", contentremixerv1alpha1.SkillKindCoding, true)
	if _, err := v.ValidateCreate(context.Background(), dupe); err == nil || !strings.Contains(err.Error(), "default") {
		t.Fatalf("a second default for skillKind coding must fail, got %v", err)
	}

	// A non-default for the same kind is fine ("add extra agent").
	extra := agent("coding-fast", contentremixerv1alpha1.SkillKindCoding, false)
	if _, err := v.ValidateCreate(context.Background(), extra); err != nil {
		t.Fatalf("a non-default extra agent must be allowed: %v", err)
	}

	// A default for a DIFFERENT kind is fine.
	other := agent("research-pro", contentremixerv1alpha1.SkillKindResearch, true)
	if _, err := v.ValidateCreate(context.Background(), other); err != nil {
		t.Fatalf("a default for a different kind must be allowed: %v", err)
	}
}

func TestAgentValidate_ModelProfile(t *testing.T) {
	mp := &contentremixerv1alpha1.ModelProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "glm-cheap", Namespace: "contentremixer"},
		Spec:       contentremixerv1alpha1.ModelProfileSpec{LiteLLMName: "glm-cheap", Provider: "zai", Model: "glm"},
	}
	v := agentValidator(t, mp)

	// "default" and "" resolve without a lookup.
	for _, name := range []string{"", "default"} {
		a := agent("a", contentremixerv1alpha1.SkillKindWriter, false)
		a.Spec.ModelProfile = name
		if _, err := v.ValidateCreate(context.Background(), a); err != nil {
			t.Fatalf("modelProfile %q must be allowed: %v", name, err)
		}
	}

	// A litellmName that exists resolves.
	a := agent("a", contentremixerv1alpha1.SkillKindWriter, false)
	a.Spec.ModelProfile = "glm-cheap"
	if _, err := v.ValidateCreate(context.Background(), a); err != nil {
		t.Fatalf("existing modelProfile must resolve: %v", err)
	}

	// A litellmName that does not exist fails closed.
	a.Spec.ModelProfile = "ghost-model"
	if _, err := v.ValidateCreate(context.Background(), a); err == nil || !strings.Contains(err.Error(), "modelProfile") {
		t.Fatalf("unknown modelProfile must fail on spec.modelProfile, got %v", err)
	}
}

func TestAgentValidate_SkillPacksNeverGate(t *testing.T) {
	// Unknown packs degrade at run time — they must NOT fail admission.
	v := agentValidator(t)
	a := agent("a", contentremixerv1alpha1.SkillKindWriter, false)
	a.Spec.SkillPacks = []string{"does-not-exist", "also-unknown"}
	if _, err := v.ValidateCreate(context.Background(), a); err != nil {
		t.Fatalf("unknown skillPacks must never gate admission: %v", err)
	}
}

func TestAgentValidate_Delete(t *testing.T) {
	v := agentValidator(t)
	if _, err := v.ValidateDelete(context.Background(), agent("coding", contentremixerv1alpha1.SkillKindCoding, true)); err != nil {
		t.Fatalf("ValidateDelete must allow (seeder re-creates defaults): %v", err)
	}
	// Wrong type is rejected, not panicked.
	if _, err := v.ValidateCreate(context.Background(), &contentremixerv1alpha1.ModelProfile{}); err == nil {
		t.Fatal("wrong type must error, not panic")
	}
}
