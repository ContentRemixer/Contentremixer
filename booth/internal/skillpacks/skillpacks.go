// Package skillpacks discovers the skill-pack catalog from a reference-repo
// checkout (ISI-7856). A pack is a directory skills/<name>/ holding a SKILL.md
// whose YAML frontmatter carries a name + description; the writer composes these
// packs into its system prompt (app/internal/pipeline/writer). Booth surfaces
// the catalog read-only on GET /v1/skillpacks so the Agent "Skills…" editor can
// suggest real packs and show each pack's purpose instead of a blind text field.
//
// Discovery is degrade-never: a directory with no readable SKILL.md is skipped,
// and a nil or empty filesystem yields no packs (not an error). The endpoint
// still returns the default composition, which is static Go data.
package skillpacks

import (
	"io/fs"
	"sort"
	"strings"
)

// Pack is one discovered skill pack.
type Pack struct {
	// Name is the pack directory, e.g. "blog-writing" — the identifier the writer
	// and Agent.spec.skillPacks[] reference.
	Name string `json:"name"`
	// Title is a human label: the SKILL.md first "# heading", falling back to Name.
	Title string `json:"title"`
	// Description is the one-line purpose from the SKILL.md frontmatter (empty if
	// the frontmatter has none).
	Description string `json:"description"`
}

// Catalog walks <dir>/SKILL.md for each child directory of fsys and returns the
// discovered packs sorted by name. fsys is rooted at the skills directory (its
// children are the pack directories). A nil fsys, an unreadable root, or a
// directory whose SKILL.md is missing/unreadable all degrade to fewer (or zero)
// packs rather than an error — discovery never gates the endpoint.
func Catalog(fsys fs.FS) ([]Pack, error) {
	if fsys == nil {
		return nil, nil
	}
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		// No skills directory wired — a valid empty catalog, not a fault.
		return nil, nil
	}
	var packs []Pack
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		b, err := fs.ReadFile(fsys, e.Name()+"/SKILL.md")
		if err != nil {
			continue // degrade: a dir without a readable SKILL.md is not a pack
		}
		title, desc := parseSkillMD(string(b))
		if title == "" {
			title = e.Name()
		}
		packs = append(packs, Pack{Name: e.Name(), Title: title, Description: desc})
	}
	sort.Slice(packs, func(i, j int) bool { return packs[i].Name < packs[j].Name })
	return packs, nil
}

// parseSkillMD extracts the title (first "# " heading) and the frontmatter
// description from a SKILL.md body. The frontmatter is the leading block fenced
// by "---" lines holding "key: value" pairs; only description is read. Both
// return "" when absent — the caller supplies a fallback title.
func parseSkillMD(body string) (title, description string) {
	lines := strings.Split(body, "\n")
	i := 0
	// YAML frontmatter: a leading "---" opens it, the next "---" closes it.
	if i < len(lines) && strings.TrimSpace(lines[i]) == "---" {
		i++
		for ; i < len(lines); i++ {
			line := strings.TrimSpace(lines[i])
			if line == "---" {
				i++
				break
			}
			if key, val, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(key) == "description" {
				description = strings.TrimSpace(val)
			}
		}
	}
	// Title: the first markdown H1 anywhere after the frontmatter.
	for ; i < len(lines); i++ {
		if h, ok := strings.CutPrefix(strings.TrimSpace(lines[i]), "# "); ok {
			title = strings.TrimSpace(h)
			break
		}
	}
	return title, description
}
