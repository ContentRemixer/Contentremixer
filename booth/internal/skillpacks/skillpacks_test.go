package skillpacks

import (
	"testing"
	"testing/fstest"
)

// skillMD builds a SKILL.md body with the given frontmatter description and H1.
func skillMD(desc, h1 string) string {
	return "---\nname: ignored\ndescription: " + desc + "\n---\n# " + h1 + "\n\nbody\n"
}

func TestCatalogDiscoversAndParses(t *testing.T) {
	fsys := fstest.MapFS{
		"blog-writing/SKILL.md": {Data: []byte(skillMD("Write blog posts.", "Blog writing skill"))},
		"seo/SKILL.md":          {Data: []byte(skillMD("Optimize for search.", "SEO skill"))},
		// A directory with no SKILL.md is not a pack (degrade, skipped).
		"not-a-pack/README.md": {Data: []byte("nope")},
		// A stray top-level file is ignored (not a directory).
		"LICENSE": {Data: []byte("mit")},
	}
	packs, err := Catalog(fsys)
	if err != nil {
		t.Fatalf("Catalog error: %v", err)
	}
	if len(packs) != 2 {
		t.Fatalf("got %d packs, want 2: %+v", len(packs), packs)
	}
	// Sorted by name: blog-writing before seo.
	if packs[0].Name != "blog-writing" || packs[1].Name != "seo" {
		t.Fatalf("packs not sorted by name: %+v", packs)
	}
	if packs[0].Title != "Blog writing skill" || packs[0].Description != "Write blog posts." {
		t.Fatalf("blog-writing parsed wrong: %+v", packs[0])
	}
	if packs[1].Description != "Optimize for search." {
		t.Fatalf("seo description wrong: %+v", packs[1])
	}
}

func TestCatalogTitleFallsBackToName(t *testing.T) {
	// No H1 and no description: title falls back to the directory name.
	fsys := fstest.MapFS{
		"concise/SKILL.md": {Data: []byte("just a body, no frontmatter, no heading\n")},
	}
	packs, err := Catalog(fsys)
	if err != nil {
		t.Fatalf("Catalog error: %v", err)
	}
	if len(packs) != 1 || packs[0].Name != "concise" {
		t.Fatalf("want one pack named concise, got %+v", packs)
	}
	if packs[0].Title != "concise" {
		t.Fatalf("title should fall back to name, got %q", packs[0].Title)
	}
	if packs[0].Description != "" {
		t.Fatalf("description should be empty, got %q", packs[0].Description)
	}
}

func TestCatalogDegradesNever(t *testing.T) {
	// nil FS → no packs, no error.
	if packs, err := Catalog(nil); err != nil || packs != nil {
		t.Fatalf("nil FS: got (%v, %v), want (nil, nil)", packs, err)
	}
	// Empty FS → no packs, no error.
	if packs, err := Catalog(fstest.MapFS{}); err != nil || len(packs) != 0 {
		t.Fatalf("empty FS: got (%v, %v), want ([], nil)", packs, err)
	}
}
