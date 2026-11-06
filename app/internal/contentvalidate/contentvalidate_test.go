package contentvalidate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline/pr"
)

// writeRepo materializes a fixture reference-repo tree under a temp dir. files
// maps repo-relative slash paths to contents. It writes the real content/
// meta.template.yaml files so the template-driven rules exercise the shipped
// templates, not test-local copies.
func writeRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	// The four shipped templates (kept in lockstep with content/*/meta.template.yaml).
	base := map[string]string{
		"content/blog/meta.template.yaml":        "title: \"\"\nauthor: jeff\nurl: \"\"\npublished_date: \"\"\ntags: []\nfiles:\n  post: index.md\n",
		"content/videos/meta.template.yaml":      "title: \"\"\nauthor: henrik\nyoutube_url: \"\"\nrecorded_date: \"\"\nduration_min: 0\ntags: []\nfiles:\n  teleprompter: teleprompter.md\n",
		"content/podcasts/meta.template.yaml":    "title: \"\"\nauthor: adriana\nepisode: 0\nurl: \"\"\ndate: \"\"\ntags: []\n",
		"content/livestreams/meta.template.yaml": "title: \"\"\nhosts: [adriana, katriana]\nyoutube_url: \"\"\ndate: \"\"\nchapters: []\ntags: []\n",
	}
	for p, c := range base {
		if _, ok := files[p]; !ok {
			files[p] = c
		}
	}
	for p, c := range files {
		abs := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", abs, err)
		}
		if err := os.WriteFile(abs, []byte(c), 0o644); err != nil {
			t.Fatalf("write %s: %v", abs, err)
		}
	}
	return root
}

// findingsBy indexes findings by rule id.
func findingsBy(fs []Finding, rule string) []Finding {
	var out []Finding
	for _, f := range fs {
		if f.Rule == rule {
			out = append(out, f)
		}
	}
	return out
}

func hasMsg(fs []Finding, rule string, substr string) bool {
	for _, f := range findingsBy(fs, rule) {
		if strings.Contains(f.Message, substr) {
			return true
		}
	}
	return false
}

func TestCleanBlogPasses(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"content/blog/clean-post/meta.yaml": "title: A Perfectly Reasonable Blog Title\nauthor: jeff\nurl: https://example.com/clean\npublished_date: 2026-11-05\ntags: [kubernetes, platform]\nfiles:\n  post: index.md\n",
		"content/blog/clean-post/index.md":  "# A Perfectly Reasonable Blog Title\n\nThis is clean prose with no planted secrets.\n",
	})
	report := DefaultEngine().Run(context.Background(), discover(t, root, "content/blog/clean-post/index.md"))
	if report.Errors() != 0 {
		t.Fatalf("clean blog should have no errors; got %d: %+v", report.Errors(), report.Findings)
	}
	if !report.OK(false) {
		t.Fatalf("clean blog should pass")
	}
}

func TestRequiredAssetMissing(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"content/blog/no-body/meta.yaml": "title: Missing Its Body File\nauthor: jeff\nurl: https://example.com/x\npublished_date: 2026-11-05\ntags: [x]\nfiles:\n  post: index.md\n",
		// index.md deliberately absent
	})
	report := DefaultEngine().Run(context.Background(), discover(t, root, "content/blog/no-body/meta.yaml"))
	if !hasMsg(report.Findings, "required-assets", "index.md") {
		t.Fatalf("expected required-assets error for missing index.md; got %+v", report.Findings)
	}
	if report.OK(false) {
		t.Fatalf("missing required asset must fail closed")
	}
}

func TestAssetStructureTitleAndFields(t *testing.T) {
	root := writeRepo(t, map[string]string{
		// title empty (error), url + published_date empty (warnings).
		"content/blog/untitled/meta.yaml": "title: \"\"\nauthor: jeff\nurl: \"\"\npublished_date: \"\"\ntags: [x]\nfiles:\n  post: index.md\n",
		"content/blog/untitled/index.md":  "# Heading\n\nBody.\n",
	})
	report := DefaultEngine().Run(context.Background(), discover(t, root, "content/blog/untitled/meta.yaml"))
	if !hasMsg(report.Findings, "asset-structure", "title is empty") {
		t.Fatalf("expected title-empty error; got %+v", report.Findings)
	}
	if !hasMsg(report.Findings, "asset-structure", "\"url\"") {
		t.Fatalf("expected empty-url warning; got %+v", report.Findings)
	}
	if report.OK(false) {
		t.Fatalf("empty title must fail closed")
	}
}

func TestContentQualityPlantedSecretBlocks(t *testing.T) {
	// Construct secret-shaped strings by concatenation so no literal secret is
	// committed (keeps the gitleaks CI gate green without an allowlist entry).
	token := "sk-" + "abcDEF0123456789ghijKL"
	root := writeRepo(t, map[string]string{
		"content/blog/leaky/meta.yaml": "title: A Blog Post That Leaks A Token\nauthor: jeff\nurl: https://example.com/leaky\npublished_date: 2026-11-05\ntags: [x]\nfiles:\n  post: index.md\n",
		"content/blog/leaky/index.md":  "# A Blog Post That Leaks A Token\n\nSet the key: " + token + " and move on.\n",
	})
	report := DefaultEngine().Run(context.Background(), discover(t, root, "content/blog/leaky/index.md"))
	cq := findingsBy(report.Findings, "content-quality")
	if len(cq) == 0 {
		t.Fatalf("expected content-quality finding for planted token; got none: %+v", report.Findings)
	}
	gotError := false
	for _, f := range cq {
		if f.Severity == SeverityError {
			gotError = true
			if strings.Contains(f.Message, token) {
				t.Fatalf("content-quality must redact the secret, not echo it: %q", f.Message)
			}
		}
	}
	if !gotError {
		t.Fatalf("planted token must map to an error (fail-closed); got %+v", cq)
	}
	if report.OK(false) {
		t.Fatalf("planted secret must fail closed")
	}
}

func TestContentQualityBrandVoiceWarns(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"content/blog/salesy/meta.yaml": "title: A Game-Changer Of A Blog Post Here\nauthor: jeff\nurl: https://example.com/salesy\npublished_date: 2026-11-05\ntags: [x]\nfiles:\n  post: index.md\n",
		"content/blog/salesy/index.md":  "# A Game-Changer Of A Blog Post Here\n\nThis revolutionary approach will supercharge your team.\n",
	})
	report := DefaultEngine().Run(context.Background(), discover(t, root, "content/blog/salesy/index.md"))
	cq := findingsBy(report.Findings, "content-quality")
	if len(cq) == 0 {
		t.Fatalf("expected brand-voice warnings; got none")
	}
	for _, f := range cq {
		if f.Severity == SeverityError {
			t.Fatalf("brand-voice must be a warning, not error: %+v", f)
		}
	}
	if !report.OK(false) {
		t.Fatalf("brand-voice warnings alone must NOT fail (fail-on=error)")
	}
	if report.OK(true) {
		t.Fatalf("--fail-on=warning must promote brand-voice warnings to a failure")
	}
}

func TestSEOChecks(t *testing.T) {
	root := writeRepo(t, map[string]string{
		// short title (<10), no tags, two H1s.
		"content/blog/seo/meta.yaml": "title: Hi\nauthor: jeff\nurl: https://example.com/seo\npublished_date: 2026-11-05\ntags: []\nfiles:\n  post: index.md\n",
		"content/blog/seo/index.md":  "# First H1\n\nText.\n\n# Second H1\n\nMore.\n",
	})
	report := DefaultEngine().Run(context.Background(), discover(t, root, "content/blog/seo/index.md"))
	seo := findingsBy(report.Findings, "seo")
	if !hasMsg(seo, "seo", "chars") {
		t.Fatalf("expected title-length SEO warning; got %+v", seo)
	}
	if !hasMsg(seo, "seo", "no tags") {
		t.Fatalf("expected no-tags SEO warning; got %+v", seo)
	}
	if !hasMsg(seo, "seo", "H1") {
		t.Fatalf("expected multiple-H1 SEO warning; got %+v", seo)
	}
	for _, f := range seo {
		if f.Severity != SeverityWarning {
			t.Fatalf("seo findings must be warnings: %+v", f)
		}
	}
}

func TestPRTemplateRule(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"content/blog/p/meta.yaml": "title: A Fine Blog Title For The PR\nauthor: jeff\nurl: https://example.com/p\npublished_date: 2026-11-05\ntags: [x]\nfiles:\n  post: index.md\n",
		"content/blog/p/index.md":  "# A Fine Blog Title For The PR\n\nBody.\n",
	})
	targets := discover(t, root, "content/blog/p/index.md")

	// Empty PR body: pr-template no-ops.
	r := DefaultEngine().Run(context.Background(), targets)
	if len(findingsBy(r.Findings, "pr-template")) != 0 {
		t.Fatalf("pr-template must no-op with no PR body")
	}

	// Bad PR body: a blog-marked body missing every field + clearance → errors.
	targets.PRBody = "<!-- ContentRemixer content PR template: blog -->\n## Random Section\n\nnothing useful here\n"
	targets.PRBodyFile = "pr-body.md"
	rb := DefaultEngine().Run(context.Background(), targets)
	pt := findingsBy(rb.Findings, "pr-template")
	if len(pt) < 3 {
		t.Fatalf("expected several pr-template errors for an empty blog body; got %+v", pt)
	}
	if !hasMsg(pt, "pr-template", "Sample clearance") {
		t.Fatalf("expected a Sample clearance finding; got %+v", pt)
	}
	if rb.OK(false) {
		t.Fatalf("missing PR template fields must fail closed")
	}

	// Good PR body: the authoritative C9-rendered blog template passes
	// verbatim (zero-duplication contract).
	targets.PRBody = pr.RenderStaticTemplate(pr.ContentTypeBlog)
	rg := DefaultEngine().Run(context.Background(), targets)
	if n := len(findingsBy(rg.Findings, "pr-template")); n != 0 {
		t.Fatalf("the canonical C9 blog template must pass pr-template; got %d findings: %+v", n, findingsBy(rg.Findings, "pr-template"))
	}
}

func TestDiscoverSkipsNonContent(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"content/blog/x/meta.yaml": "title: A Blog Title Long Enough Here\nauthor: jeff\nurl: https://example.com/x\npublished_date: 2026-11-05\ntags: [x]\nfiles:\n  post: index.md\n",
		"content/blog/x/index.md":  "# A Blog Title Long Enough Here\n\nBody.\n",
		"styles/blog.md":           "style guide",
		"README.md":                "readme",
	})
	targets, skipped, err := Discover(context.Background(), root,
		[]string{"content/blog/x/index.md", "styles/blog.md", "README.md", "content/blog/meta.template.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Dir != "content/blog/x" {
		t.Fatalf("expected exactly content/blog/x; got %+v", targets)
	}
	// styles/, README.md, and the template itself are non-content → skipped.
	if len(skipped) != 3 {
		t.Fatalf("expected 3 skipped paths; got %v", skipped)
	}
}

func TestDiscoverAll(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"content/blog/a/meta.yaml":                            "title: Blog A Title Is Long Enough\nauthor: jeff\nurl: https://example.com/a\npublished_date: 2026-11-05\ntags: [x]\nfiles:\n  post: index.md\n",
		"content/blog/a/index.md":                             "# Blog A Title Is Long Enough\n\nBody.\n",
		"content/videos/2026/2026-01-01-demo/meta.yaml":       "title: A Video Teleprompter Title\nauthor: henrik\nyoutube_url: https://youtu.be/abc\nrecorded_date: 2026-01-01\nduration_min: 12\ntags: [k8s]\nfiles:\n  teleprompter: teleprompter.md\n",
		"content/videos/2026/2026-01-01-demo/teleprompter.md": "# A Video Teleprompter Title\n\nScript.\n",
	})
	targets, _, err := DiscoverAll(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 {
		t.Fatalf("expected 2 content folders discovered; got %d: %+v", len(targets), targets)
	}
	gotTypes := map[string]bool{}
	for _, tg := range targets {
		gotTypes[tg.Type] = true
	}
	if !gotTypes["blog"] || !gotTypes["videos"] {
		t.Fatalf("expected blog+videos types; got %v", gotTypes)
	}
}

func TestDiscoverRecycledDraft(t *testing.T) {
	// A remix PR lands recycled/<slug>/{index.md,meta.yaml} (ISI-7076 §4). The
	// draft has no content/<type>/ path segment; its type resolves from
	// meta.output_format via the C9 pr.ContentType mapping.
	root := writeRepo(t, map[string]string{
		// recycled blog draft → type resolves to "blog" (template-driven rules run).
		"recycled/hello-remix/meta.yaml": "title: A Recycled Blog Title Long Enough\noutput_format: blog\nfiles:\n  post: index.md\n",
		"recycled/hello-remix/index.md":  "# A Recycled Blog Title Long Enough\n\nClean prose.\n",
		// recycled short draft → output_format with no dedicated template → Type "".
		"recycled/quick-short/meta.yaml": "title: A Short Social Snippet\noutput_format: short\nfiles:\n  post: index.md\n",
		"recycled/quick-short/index.md":  "# A Short Social Snippet\n\nBody.\n",
	})

	// Changed-path discovery resolves each recycled draft folder.
	targets, _, err := Discover(context.Background(), root,
		[]string{"recycled/hello-remix/index.md", "recycled/quick-short/meta.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 {
		t.Fatalf("expected 2 recycled drafts discovered; got %d: %+v", len(targets), targets)
	}
	byDir := map[string]Target{}
	for _, tg := range targets {
		byDir[tg.Dir] = tg
	}
	if got := byDir["recycled/hello-remix"].Type; got != "blog" {
		t.Fatalf("recycled blog draft should resolve type=blog; got %q", got)
	}
	if got := byDir["recycled/quick-short"].Type; got != "" {
		t.Fatalf("recycled short draft should resolve type=\"\" (graceful); got %q", got)
	}

	// --all mode walks recycled/ too.
	all, _, err := DiscoverAll(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("DiscoverAll should find both recycled drafts; got %d: %+v", len(all), all)
	}

	// content-quality (the Mastering scrub) must actually run on a recycled
	// draft — the whole point of gating a remix PR. A leaked secret fails closed.
	leak := writeRepo(t, map[string]string{
		"recycled/leaky/meta.yaml": "title: A Leaky Recycled Draft Title\noutput_format: blog\nfiles:\n  post: index.md\n",
		"recycled/leaky/index.md":  "# A Leaky Recycled Draft Title\n\ntoken: ghp_0123456789abcdefghijABCDEFGHIJ0123456789\n",
	})
	lt, _, err := Discover(context.Background(), leak, []string{"recycled/leaky/index.md"})
	if err != nil {
		t.Fatal(err)
	}
	r := DefaultEngine().Run(context.Background(), &ValidateContext{RepoRoot: leak, Targets: lt})
	if r.OK(false) {
		t.Fatalf("a recycled draft leaking a secret must fail content-quality closed; findings: %+v", r.Findings)
	}
	if len(findingsBy(r.Findings, "content-quality")) == 0 {
		t.Fatalf("expected a content-quality finding on the leaky recycled draft; got %+v", r.Findings)
	}
}

func TestSummaryAndAnnotationsRender(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"content/blog/untitled/meta.yaml": "title: \"\"\nauthor: jeff\nurl: \"\"\npublished_date: \"\"\ntags: []\nfiles:\n  post: index.md\n",
		"content/blog/untitled/index.md":  "# H\n\nBody.\n",
	})
	report := DefaultEngine().Run(context.Background(), discover(t, root, "content/blog/untitled/meta.yaml"))

	var sum strings.Builder
	report.WriteSummary(&sum, false)
	s := sum.String()
	if !strings.Contains(s, "Content CI") || !strings.Contains(s, "| Severity | Rule |") || !strings.Contains(s, "FAIL") {
		t.Fatalf("summary missing heading/table/verdict:\n%s", s)
	}

	var ann strings.Builder
	report.WriteAnnotations(&ann)
	a := ann.String()
	if !strings.Contains(a, "::error ") || !strings.Contains(a, "file=content/blog/untitled/meta.yaml") {
		t.Fatalf("annotations missing ::error with file=:\n%s", a)
	}
}

func TestListRulesStable(t *testing.T) {
	ids := []string{}
	for _, r := range DefaultEngine().Rules() {
		ids = append(ids, r.ID())
	}
	want := "required-assets,asset-structure,content-quality,seo,pr-template"
	if strings.Join(ids, ",") != want {
		t.Fatalf("rule order drifted: got %q want %q", strings.Join(ids, ","), want)
	}
}

// discover is a test helper that runs Discover and fails on error, returning a
// ready ValidateContext.
func discover(t *testing.T, root string, changed ...string) *ValidateContext {
	t.Helper()
	targets, _, err := Discover(context.Background(), root, changed)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	return &ValidateContext{RepoRoot: root, Targets: targets}
}
