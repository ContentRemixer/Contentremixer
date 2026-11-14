package writer

import (
	"context"
	"strings"
	"testing"
	"time"
)

// allContentPacks is a fixture tree with styles/blog.md plus every pack the
// content-writer skill composes, each with a distinct marker body.
func allContentPacks() map[string]string {
	return map[string]string{
		"styles/blog.md":                     "# blog style guide body",
		"skills/blog-writing/SKILL.md":       "BLOGWRITING_MARKER",
		"skills/human-voice-review/SKILL.md": "HUMANVOICE_MARKER",
		"skills/seo/SKILL.md":                "SEO_MARKER",
		"skills/technical-tutorial/SKILL.md": "TUTORIAL_MARKER",
		"skills/teleprompter/SKILL.md":       "TELEPROMPTER_MARKER",
		"skills/visual-design/SKILL.md":      "VISUALDESIGN_MARKER",
	}
}

func TestResolveSkillPacksComposesContentWriter(t *testing.T) {
	root := t.TempDir()
	writeFixtureTree(t, root, allContentPacks())
	packs := resolveSkillPacks(fixtureFS{root: root}, "content-writer")

	wantNames := []string{
		"skills/blog-writing/SKILL.md",
		"skills/human-voice-review/SKILL.md",
		"skills/seo/SKILL.md",
	}
	if len(packs) != len(wantNames) {
		t.Fatalf("content-writer composed %d packs, want %d: %+v", len(packs), len(wantNames), packs)
	}
	for i, want := range wantNames {
		if packs[i].name != want {
			t.Fatalf("pack %d = %q, want %q (order matters)", i, packs[i].name, want)
		}
	}
}

func TestResolveSkillPacksDegradesMissing(t *testing.T) {
	root := t.TempDir()
	// Only one of content-writer's three packs is present.
	writeFixtureTree(t, root, map[string]string{
		"skills/blog-writing/SKILL.md": "BLOGWRITING_MARKER",
	})
	packs := resolveSkillPacks(fixtureFS{root: root}, "content-writer")
	if len(packs) != 1 || packs[0].name != "skills/blog-writing/SKILL.md" {
		t.Fatalf("missing packs must be skipped, want only blog-writing, got %+v", packs)
	}

	// No packs present at all → nothing (degrade to style-only).
	if got := resolveSkillPacks(fixtureFS{root: t.TempDir()}, "content-writer"); got != nil {
		t.Fatalf("all packs missing must degrade to nil, got %+v", got)
	}
}

func TestResolveSkillPacksEmptyFileSkipped(t *testing.T) {
	root := t.TempDir()
	writeFixtureTree(t, root, map[string]string{
		"skills/seo/SKILL.md": "   \n\t ", // whitespace-only
	})
	if got := resolveSkillPacks(fixtureFS{root: root}, "seo"); got != nil {
		t.Fatalf("empty/whitespace SKILL.md must be skipped, got %+v", got)
	}
}

func TestResolveSkillPacksDynamicByName(t *testing.T) {
	root := t.TempDir()
	writeFixtureTree(t, root, allContentPacks())

	// A skill not in the catalog resolves to the single pack named after it.
	for _, tc := range []struct{ skill, dir string }{
		{"teleprompter", "teleprompter"},   // catalog-absent, dir == name
		{"visual-design", "visual-design"}, // previously-unused pack, by name
	} {
		packs := resolveSkillPacks(fixtureFS{root: root}, tc.skill)
		want := "skills/" + tc.dir + "/SKILL.md"
		if len(packs) != 1 || packs[0].name != want {
			t.Fatalf("skill %q: want single pack %q, got %+v", tc.skill, want, packs)
		}
	}

	// Empty skill resolves to nothing.
	if got := resolveSkillPacks(fixtureFS{root: root}, "  "); got != nil {
		t.Fatalf("empty skill must resolve to nil, got %+v", got)
	}
}

func TestDefaultSkillForFormat(t *testing.T) {
	for format, want := range map[string]string{
		FormatBlog:         "content-writer",
		FormatTutorial:     "tutorial",
		FormatTeleprompter: "teleprompter",
		FormatShort:        "", // short stays style-only
	} {
		if got := defaultSkillForFormat(format); got != want {
			t.Fatalf("defaultSkillForFormat(%q) = %q, want %q", format, got, want)
		}
	}
}

// TestWriteComposesMultiplePacks proves the content template (blog → the
// content-writer skill) weaves all composed packs into the system prompt in
// order — ISI-7394 acceptance: "content template composes multiple packs".
func TestWriteComposesMultiplePacks(t *testing.T) {
	last, gen := captureGen(GenerateResponse{Content: blogDraft, Model: "m"}, nil)
	root := t.TempDir()
	writeFixtureTree(t, root, allContentPacks())
	w := &Writer{FS: fixtureFS{root: root}, Generate: gen, Now: func() time.Time { return fixedNow }}
	if _, err := w.Write(context.Background(), blogRunContext(), teleprompterSample()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	for _, marker := range []string{"BLOGWRITING_MARKER", "HUMANVOICE_MARKER", "SEO_MARKER"} {
		if !strings.Contains(last.System, marker) {
			t.Fatalf("system prompt missing composed pack %q:\n%s", marker, last.System)
		}
	}
	// Order: blog-writing before human-voice-review before seo.
	iBlog := strings.Index(last.System, "BLOGWRITING_MARKER")
	iVoice := strings.Index(last.System, "HUMANVOICE_MARKER")
	iSEO := strings.Index(last.System, "SEO_MARKER")
	if !(iBlog < iVoice && iVoice < iSEO) {
		t.Fatalf("packs out of composition order: blog=%d voice=%d seo=%d", iBlog, iVoice, iSEO)
	}
}

// TestWriteSkillOverridesFormat proves an explicit RunContext.Skill resolves to
// its pack regardless of OutputFormat — the ISI-7394 generalization. A blog run
// pinned to skill "tutorial" loads technical-tutorial, not the content-writer
// stack.
func TestWriteSkillOverridesFormat(t *testing.T) {
	last, gen := captureGen(GenerateResponse{Content: blogDraft, Model: "m"}, nil)
	root := t.TempDir()
	writeFixtureTree(t, root, allContentPacks())
	rc := blogRunContext() // OutputFormat = blog
	rc.Skill = "tutorial"
	w := &Writer{FS: fixtureFS{root: root}, Generate: gen, Now: func() time.Time { return fixedNow }}
	if _, err := w.Write(context.Background(), rc, teleprompterSample()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !strings.Contains(last.System, "TUTORIAL_MARKER") {
		t.Fatalf("explicit skill=tutorial did not load technical-tutorial:\n%s", last.System)
	}
	if strings.Contains(last.System, "BLOGWRITING_MARKER") || strings.Contains(last.System, "SEO_MARKER") {
		t.Fatalf("blog format default leaked despite explicit skill override:\n%s", last.System)
	}
	// Style guide is still the blog style (format-driven, skill-independent).
	if !strings.Contains(last.System, "styles/blog.md") {
		t.Fatalf("style guide must still follow OutputFormat:\n%s", last.System)
	}
}

// TestWriteShortHasNoSkillPack keeps short's pre-ISI-7394 style-only behavior:
// no default skill, so no skill block even when packs exist on disk.
func TestWriteShortHasNoSkillPack(t *testing.T) {
	last, gen := captureGen(GenerateResponse{Content: "# Hook!\n\nOne idea.", Model: "m"}, nil)
	root := t.TempDir()
	files := allContentPacks()
	files["styles/short.md"] = "# short style guide body"
	writeFixtureTree(t, root, files)
	rc := blogRunContext()
	rc.OutputFormat = FormatShort
	w := &Writer{FS: fixtureFS{root: root}, Generate: gen, Now: func() time.Time { return fixedNow }}
	if _, err := w.Write(context.Background(), rc, teleprompterSample()); err != nil {
		t.Fatalf("Write (short): %v", err)
	}
	if strings.Contains(last.System, "SKILL.md") || strings.Contains(last.System, "skill pack") {
		t.Fatalf("short must stay style-only (no skill pack):\n%s", last.System)
	}
}
