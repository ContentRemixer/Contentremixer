package v1alpha1

// Skill-pack composition catalog (ISI-7856). This is the CRD-level mirror of the
// writer's private skillComposition map (app/internal/pipeline/writer/skills.go):
// a skill name → the ordered skill-pack directories it composes into the writer
// system prompt. It exists here so surfaces OUTSIDE the app module — notably
// Booth's read-only GET /v1/skillpacks discovery endpoint — can render the
// per-skill default composition without importing app/internal.
//
// The two copies MUST agree: the writer loads packs from its own map, while this
// one only describes that behavior to operators. A writer-package lock-step test
// (TestSkillCompositionLockstepWithV1alpha1) fails the build if they drift,
// exactly as TestDefaultSkillLockstepWithV1alpha1 guards DefaultSkill. The writer
// runtime stays decoupled from this package (it does not import v1alpha1), so the
// degrade-never write path is unchanged — this is a description, not a dependency.
//
// A skill name ABSENT from this map resolves dynamically to a single pack whose
// directory equals the name (skills/<name>/SKILL.md), so any pack is reachable by
// name without a catalog entry. PackDirsForSkill encodes that fallback.
var SkillComposition = map[string][]string{
	"content-writer": {"blog-writing", "human-voice-review", "seo"},
	"tutorial":       {"technical-tutorial", "seo"},
}

// PackDirsForSkill returns the ordered pack directories a skill composes: its
// catalog entry, or the skill name itself as a single dynamic pack. It mirrors
// the writer's packDirsForSkill (lock-step guarded). An empty skill composes
// nothing.
func PackDirsForSkill(skill string) []string {
	if skill == "" {
		return nil
	}
	if dirs, ok := SkillComposition[skill]; ok {
		return dirs
	}
	return []string{skill}
}
