package writer

import "strings"

// Dynamic skill registry (ISI-7394, parent ISI-7389 ADR-2). Replaces the old
// hardcoded format→directory map: a run names a *skill* (RunContext.Skill /
// WorkOrder.Skill), and the registry resolves that name to one or more
// skill-pack directories (skills/<dir>/SKILL.md) composed in order into the
// writer's system prompt. Selection is now by skill, not by output format, so
// the same skill pack is reusable across formats and new packs wire in by
// name alone.
//
// Degrade, never gate: a pack whose SKILL.md is missing, unreadable, or empty
// is skipped. An empty skill (no default for the format) composes nothing.
// styles/<format>.md stays the sole fail-closed contract — skills are an
// enhancement layer (mirrors the persona conditioner, §5.4).

// skillComposition is the composition catalog: a skill name → the ordered
// skill-pack directories it composes. A skill name ABSENT from this catalog
// resolves dynamically to a single pack whose directory equals the name
// (skills/<name>/SKILL.md) — so any pack is reachable by name without a catalog
// entry. Catalog entries exist for skills that compose multiple packs, or
// whose directory name differs from the skill name (e.g. tutorial).
//
// content-writer composes the public-content stack (ISI-7394 acceptance):
// blog-writing structure + human-voice-review (strip AI tells) + seo. This
// wires the previously-unused human-voice-review and seo packs; visual-design
// and workshop-builder stay reachable dynamically by name.
//
// tutorial layers the seo pack on top of technical-tutorial (ISI-7805): a
// published tutorial is a public, search-indexed page, so it gets the same
// keyword/metadata/structure discipline as a blog post.
var skillComposition = map[string][]string{
	"content-writer": {"blog-writing", "human-voice-review", "seo"},
	"tutorial":       {"technical-tutorial", "seo"},
}

// loadedPack is one resolved skill pack: its repo-relative SKILL.md path (the
// prompt block header + provenance) and its trimmed body.
type loadedPack struct {
	name string // "skills/<dir>/SKILL.md"
	body string
}

// packDirsForSkill returns the ordered pack directories a skill composes: its
// catalog entry, or the skill name itself as a single dynamic pack.
func packDirsForSkill(skill string) []string {
	if dirs, ok := skillComposition[skill]; ok {
		return dirs
	}
	return []string{skill}
}

// defaultSkillForFormat derives the skill name from an output format for a run
// that carries no explicit Skill (the pre-seam path, before the Dispatcher
// threads WorkOrder.Skill). It mirrors v1alpha1.DefaultSkill for the
// content-template formats so an unset Skill resolves exactly as the route
// default would. short stays style-only (no skill pack), preserving its
// pre-ISI-7394 behavior.
func defaultSkillForFormat(format string) string {
	switch format {
	case FormatBlog:
		return "content-writer"
	case FormatTutorial:
		return "tutorial"
	case FormatTeleprompter:
		return "teleprompter"
	default: // short, and any format with no skill pack
		return ""
	}
}

// resolveSkillPacks loads every pack the skill composes, in order, from the
// reference-repo checkout. A pack whose SKILL.md is missing, unreadable, or
// empty is skipped (degrade, never gate). An empty skill resolves to nothing.
//
// override (ISI-7803) is the selected Agent.spec.skillPacks[]: when non-empty it
// REPLACES the catalog composition for the skill, so an admin configures packs on
// an Agent instead of editing skillComposition. The override is still resolved
// through the same degrade-never-gate path, so an unknown pack an admin typed is
// skipped at run time rather than failing the write (matches the CRD note that
// unknown packs warn, they do not gate). An override given with an empty skill is
// honoured — the packs are what was configured, independent of a skill name.
func resolveSkillPacks(fs StyleFS, skill string, override []string) []loadedPack {
	skill = strings.TrimSpace(skill)
	dirs := override
	if len(dirs) == 0 {
		if skill == "" {
			return nil
		}
		dirs = packDirsForSkill(skill)
	}
	var packs []loadedPack
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		name := "skills/" + dir + "/SKILL.md"
		b, err := fs.ReadFile(name)
		if err != nil {
			continue
		}
		if body := strings.TrimSpace(string(b)); body != "" {
			packs = append(packs, loadedPack{name: name, body: body})
		}
	}
	return packs
}
