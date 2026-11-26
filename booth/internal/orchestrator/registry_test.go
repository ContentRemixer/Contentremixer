package orchestrator

import (
	"testing"

	v1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

// TestRegistryMatchesKnownSkillKinds locks the run-time registry to the CRD enum
// (ISI-7800 agent_types.go): every DAG kind must be a known skill kind, and the
// only known kind WITHOUT a DAG impl must be "writer" (the linear content path).
// A new skill kind added to the enum but not the registry — or vice versa — fails
// here before it can drift into an Agent that resolves to nothing.
func TestRegistryMatchesKnownSkillKinds(t *testing.T) {
	known := map[string]bool{}
	for _, k := range v1alpha1.KnownSkillKinds {
		known[string(k)] = true
	}
	// Every registered kind is a known CRD kind.
	for _, k := range RegisteredSkillKinds() {
		if !known[k] {
			t.Errorf("registered skill kind %q is not in v1alpha1.KnownSkillKinds", k)
		}
	}
	// Every known kind resolves to an impl, except "writer".
	for k := range known {
		_, ok := SkillForKind(k)
		if k == string(v1alpha1.SkillKindWriter) {
			if ok {
				t.Errorf("writer kind unexpectedly has a DAG impl; it runs the linear content path")
			}
			continue
		}
		if !ok {
			t.Errorf("known skill kind %q has no registered DAG impl", k)
		}
	}
}

func TestEngineForKinds(t *testing.T) {
	eng, err := EngineForKinds(SkillArchitect, SkillResearch)
	if err != nil {
		t.Fatalf("EngineForKinds: %v", err)
	}
	if got := len(eng.skills); got != 2 {
		t.Fatalf("want 2 skills, got %d", got)
	}
	if _, err := EngineForKinds(SkillArchitect, "writer"); err == nil {
		t.Fatal("want error for unregistered kind \"writer\", got nil")
	}
	if _, err := EngineForKinds("nope"); err == nil {
		t.Fatal("want error for unknown kind, got nil")
	}
}

// TestDefaultEngineFromRegistry proves DefaultEngine is now the registry's
// defaultKinds, not a second hardcoded list.
func TestDefaultEngineFromRegistry(t *testing.T) {
	if got := len(DefaultEngine().skills); got != len(defaultKinds) {
		t.Fatalf("DefaultEngine has %d skills, defaultKinds has %d", got, len(defaultKinds))
	}
}
