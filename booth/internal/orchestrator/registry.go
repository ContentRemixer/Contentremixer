package orchestrator

import "fmt"

// Skill-kind registry (ISI-7803 deliverable 3). The name→impl map that resolves
// an Agent.spec.skillKind to the executable Skill it runs. It is the single
// source the ISI-7804 engine loader builds a team's Engine from: a team node's
// agentRef → Agent.spec.skillKind → this registry → a fresh Skill. Before this,
// DefaultEngine() hardcoded the five scaffold skills inline; now that list is
// derived from the registry so the two cannot drift.
//
// The map is keyed by the same skill-kind strings as v1alpha1.SkillKind (the
// CRD enum) so an Agent's kind looks up directly. The app-side
// v1alpha1.KnownSkillKinds enum + the Agent admission webhook are the fail-closed
// contract at write time; this registry is the fail-closed contract at run time
// (a kind with no impl cannot be scheduled). TestRegistryMatchesKnownSkillKinds
// guards the lock-step.
//
// The "writer" kind (v1alpha1.SkillKindWriter) has NO entry here on purpose: it
// is the Phase-1 linear content writer (app/internal/pipeline/writer), not a DAG
// scaffold skill. A single-writer team (the content-default seed) runs the linear
// path, not this engine — the loader special-cases it rather than looking it up.
var skillKindRegistry = map[string]func() Skill{
	SkillArchitect:     func() Skill { return NewArchitectSkill() },
	SkillResearch:      func() Skill { return NewResearchSkill() },
	SkillCoding:        func() Skill { return NewCodingSkill() },
	SkillObservability: func() Skill { return NewObservabilitySkill() },
	SkillGraphic:       func() Skill { return NewGraphicSkill() },
}

// defaultKinds is the DAG order DefaultEngine() wires (architect first, then the
// four fan-out fillers). Kept as an ordered slice because Go map iteration order
// is unspecified and the engine's topo-sort is seeded from this order.
var defaultKinds = []string{
	SkillArchitect, SkillResearch, SkillCoding, SkillObservability, SkillGraphic,
}

// SkillForKind returns a fresh Skill implementation for a skill-kind name. ok is
// false when the kind has no registered DAG impl (an unknown kind, or "writer"
// which runs the linear content path, not this engine).
func SkillForKind(kind string) (Skill, bool) {
	mk, ok := skillKindRegistry[kind]
	if !ok {
		return nil, false
	}
	return mk(), true
}

// RegisteredSkillKinds returns the skill-kind names that have a DAG impl, in the
// DefaultEngine DAG order. It is the set the engine loader (and a test mirror of
// the CRD enum) checks against.
func RegisteredSkillKinds() []string {
	out := make([]string, len(defaultKinds))
	copy(out, defaultKinds)
	return out
}

// EngineForKinds builds an Engine from skill-kind names, each resolved through
// the registry in the given order. An unregistered kind is a hard error (fail
// closed) — the loader never schedules a kind with no executable logic. This is
// the seam the ISI-7804 engine loader calls once it has mapped a team's nodes to
// their agents' skill kinds.
func EngineForKinds(kinds ...string) (*Engine, error) {
	skills := make([]Skill, 0, len(kinds))
	for _, k := range kinds {
		sk, ok := SkillForKind(k)
		if !ok {
			return nil, fmt.Errorf("orchestrator: no skill registered for kind %q", k)
		}
		skills = append(skills, sk)
	}
	return NewEngine(skills...), nil
}
