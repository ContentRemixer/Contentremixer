package writer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline"
)

// fixtureFS serves the testdata tree (style-guide fixtures) without a git
// dependency.
type fixtureFS struct {
	root string
}

func (f fixtureFS) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(f.root, filepath.FromSlash(name)))
}

const testCommit = "3ae0a89b00000000000000000000000000000000"

const blogDraft = `# Kubernetes secrets keep leaking into git — here is the boring fix

You committed a token. It happens to everyone.

## Why .gitignore is not enough

` + "```bash" + `
kubectl create secret generic models --from-literal=key=xxx
` + "```" + `

## What we learned

Rotate early, rotate often.
`

// fixedNow keeps Mix.Meta["generated"] deterministic.
var fixedNow = time.Date(2026, 11, 5, 4, 0, 0, 0, time.UTC)

func newWriter(gen Generator) *Writer {
	return &Writer{FS: fixtureFS{root: "testdata"}, Generate: gen, Now: func() time.Time { return fixedNow }}
}

func teleprompterSample() pipeline.Sample {
	return pipeline.Sample{
		SourcePath:    "content/videos/2026/demo-video",
		SourceCommit:  testCommit,
		DetectedInput: FormatTeleprompter,
		Meta: map[string]any{
			"title":  "Demo video",
			"author": "henrik",
			"tags":   []any{"kubernetes", "secrets"},
			"files":  map[string]any{"teleprompter": "teleprompter.md"},
		},
		Body: "Welcome back to the show. Today: how secrets end up in git, and the boring fix that actually works.",
	}
}

func blogRunContext() pipeline.RunContext {
	return pipeline.RunContext{
		Repo:         "ContentRemixer/Contentremixer",
		IssueNumber:  42,
		SourcePath:   "content/videos/2026/demo-video",
		InputFormat:  FormatTeleprompter,
		OutputFormat: FormatBlog,
		Notes:        "Audience: platform engineers; angle: prevention over cleanup",
		ModelName:    "fast",
	}
}

// captureGen records the last (model, system, user) and returns a canned
// draft — the LiteLLM client stands in for it in production.
func captureGen(draft string, err error) (*struct {
	model, system, user string
}, Generator) {
	captured := &struct{ model, system, user string }{}
	return captured, Generator(func(_ context.Context, model, system, user string) (string, error) {
		captured.model, captured.system, captured.user = model, system, user
		return draft, err
	})
}

// --- acceptance: recycle:blog style from a teleprompter brief ---

func TestWriteBlogFromTeleprompterBrief(t *testing.T) {
	cap, gen := captureGen(blogDraft, nil)
	mix, err := newWriter(gen).Write(context.Background(), blogRunContext(), teleprompterSample())
	if err != nil {
		t.Fatalf("Write (blog from teleprompter) unexpected error: %v", err)
	}
	if mix.OutputFormat != FormatBlog {
		t.Fatalf("OutputFormat = %q, want blog", mix.OutputFormat)
	}
	if mix.Title != "Kubernetes secrets keep leaking into git — here is the boring fix" {
		t.Fatalf("Title = %q", mix.Title)
	}
	if !strings.Contains(mix.Markdown, "# Kubernetes secrets keep leaking into git") ||
		!strings.Contains(mix.Markdown, "## Why .gitignore is not enough") ||
		!strings.Contains(mix.Markdown, "## What we learned") {
		t.Fatalf("Markdown is not a correctly-styled blog draft (H1 + H2 sections + what-we-learned):\n%s", mix.Markdown)
	}
	if mix.ModelUsed != "fast" {
		t.Fatalf("ModelUsed = %q, want the requested alias", mix.ModelUsed)
	}

	clearance, ok := mix.Meta["sample_clearance"].(map[string]any)
	if !ok {
		t.Fatalf("Meta.sample_clearance missing: %+v", mix.Meta)
	}
	want := map[string]any{
		"source_path":   "content/videos/2026/demo-video",
		"source_commit": testCommit,
		"model":         "fast",
		"input_format":  FormatTeleprompter,
		"output_format": FormatBlog,
		"recycle_issue": 42,
	}
	for k, v := range want {
		if clearance[k] != v {
			t.Fatalf("sample_clearance.%s = %v, want %v", k, clearance[k], v)
		}
	}
	if mix.Meta["title"] != mix.Title || mix.Meta["format"] != FormatBlog {
		t.Fatalf("Meta draft identity wrong: %+v", mix.Meta)
	}
	if mix.Meta["generated"] != fixedNow.Format(time.RFC3339) {
		t.Fatalf("Meta.generated = %v, want the injected clock value", mix.Meta["generated"])
	}
	if mix.Meta["notes"] != "Audience: platform engineers; angle: prevention over cleanup" {
		t.Fatalf("Meta.notes = %v, want the issue notes carried", mix.Meta["notes"])
	}
	_ = cap
}

func TestModelUsedDefaultsWhenNoAlias(t *testing.T) {
	_, gen := captureGen("# T\n\nbody", nil)
	rc := blogRunContext()
	rc.ModelName = ""
	mix, err := newWriter(gen).Write(context.Background(), rc, teleprompterSample())
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if mix.ModelUsed != DefaultModelUsed {
		t.Fatalf("ModelUsed = %q, want %q", mix.ModelUsed, DefaultModelUsed)
	}
}

// --- conditioning: prompt carries style guide + brief + notes ---

func TestPromptConditionsOnStyleBriefAndNotes(t *testing.T) {
	cap, gen := captureGen("# T\n\nbody", nil)
	if _, err := newWriter(gen).Write(context.Background(), blogRunContext(), teleprompterSample()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	for _, wantSub := range []string{
		"styles/blog.md",           // the selected style file is named
		"Title with the problem in it", // style guide verbatim
		"no marketing voice",           // style guide verbatim
		"input format: teleprompter",   // detected input in the user prompt
		"title: Demo video",            // source meta.yaml rendered
		"author: henrik",               // source meta.yaml rendered
		"Welcome back to the show",     // the source body itself
		"Angle / audience guidance from the recycle issue #42",
		"Audience: platform engineers; angle: prevention over cleanup",
		"Target output format: blog",   // route output
	} {
		if !strings.Contains(cap.system+cap.user, wantSub) {
			t.Fatalf("prompt missing %q\nsystem:\n%s\nuser:\n%s", wantSub, cap.system, cap.user)
		}
	}
	if cap.model != "fast" {
		t.Fatalf("generator received model %q, want the rc alias", cap.model)
	}
}

func TestPromptSelectsStyleFileByOutputFormat(t *testing.T) {
	cap, gen := captureGen("# Hook!\n\nSixty seconds, one idea.", nil)
	rc := blogRunContext()
	rc.OutputFormat = FormatShort
	rc.Notes = ""
	if _, err := newWriter(gen).Write(context.Background(), rc, teleprompterSample()); err != nil {
		t.Fatalf("Write (short): %v", err)
	}
	if !strings.Contains(cap.system, "styles/short.md") || !strings.Contains(cap.system, "Hook in the first 3 seconds") {
		t.Fatalf("short run did not load styles/short.md verbatim:\n%s", cap.system)
	}
	if strings.Contains(cap.system, "styles/blog.md") {
		t.Fatalf("blog style leaked into a short run:\n%s", cap.system)
	}
	if strings.Contains(cap.user, "Angle / audience guidance") {
		t.Fatalf("notes section emitted for empty Notes:\n%s", cap.user)
	}
}

// --- fail-closed errors ---

func TestWriteErrors(t *testing.T) {
	okGen := Generator(func(context.Context, string, string, string) (string, error) {
		return "# T\n\nbody", nil
	})
	cases := []struct {
		name    string
		w       *Writer
		rc      pipeline.RunContext
		sample  pipeline.Sample
		wantSub string
	}{
		{
			name:    "empty outputFormat",
			w:       newWriter(okGen),
			rc:      pipeline.RunContext{IssueNumber: 42},
			sample:  teleprompterSample(),
			wantSub: "empty outputFormat",
		},
		{
			name:    "unknown outputFormat",
			w:       newWriter(okGen),
			rc:      pipeline.RunContext{OutputFormat: "newsletter"},
			sample:  teleprompterSample(),
			wantSub: "unsupported outputFormat",
		},
		{
			name:    "empty sample body",
			w:       newWriter(okGen),
			rc:      blogRunContext(),
			sample:  pipeline.Sample{SourcePath: "content/videos/2026/none", DetectedInput: FormatTeleprompter},
			wantSub: "empty body",
		},
		{
			name:    "missing style file",
			w:       newWriter(okGen),
			rc:      pipeline.RunContext{OutputFormat: FormatTutorial},
			sample:  teleprompterSample(),
			wantSub: `loading style guide "styles/tutorial.md"`,
		},
		{
			name:    "empty style file",
			w:       newWriter(okGen),
			rc:      pipeline.RunContext{OutputFormat: FormatTeleprompter},
			sample:  teleprompterSample(),
			wantSub: `style guide "styles/teleprompter.md" is empty`,
		},
		{
			name:    "generator error propagates",
			w:       newWriter(Generator(func(context.Context, string, string, string) (string, error) { return "", errors.New("litellm: 503 unavailable") })),
			rc:      blogRunContext(),
			sample:  teleprompterSample(),
			wantSub: "litellm: 503 unavailable",
		},
		{
			name:    "empty completion",
			w:       newWriter(Generator(func(context.Context, string, string, string) (string, error) { return "   ", nil })),
			rc:      blogRunContext(),
			sample:  teleprompterSample(),
			wantSub: "empty draft",
		},
		{
			name:    "nil generator",
			w:       &Writer{FS: fixtureFS{root: "testdata"}, Now: func() time.Time { return fixedNow }},
			rc:      blogRunContext(),
			sample:  teleprompterSample(),
			wantSub: "no model generator configured",
		},
		{
			name:    "nil FS",
			w:       &Writer{Generate: okGen, Now: func() time.Time { return fixedNow }},
			rc:      blogRunContext(),
			sample:  teleprompterSample(),
			wantSub: "no style filesystem configured",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.w.Write(context.Background(), tc.rc, tc.sample)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantSub)
			}
		})
	}
}

// --- title extraction ---

func TestExtractTitle(t *testing.T) {
	cases := []struct {
		draft, want string
		wantErr     bool
	}{
		{draft: "# Hello\n\nbody", want: "Hello"},
		{draft: "\n\n#  Padded title \nbody", want: "Padded title"},
		{draft: "## only h2", want: "only h2"}, // fallback: first non-empty line, heading marks trimmed
		{draft: "---\nfront-matter\n# Real", want: "front-matter"}, // --- skipped, next line stands in
		{draft: "", wantErr: true},
	}
	for _, tc := range cases {
		got, err := extractTitle(tc.draft)
		if tc.wantErr {
			if err == nil {
				t.Errorf("extractTitle(%q): expected error, got %q", tc.draft, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("extractTitle(%q): %v", tc.draft, err)
			continue
		}
		if got != tc.want {
			t.Errorf("extractTitle(%q) = %q, want %q", tc.draft, got, tc.want)
		}
	}
}

// --- lock-step guard: the real repo ships a style file per CRD enum value ---

func TestRepoStyleSetMatchesOutputFormats(t *testing.T) {
	fs := fixtureFS{root: filepath.Join("..", "..", "..")} // repo root
	for format := range validFormats {
		style := "styles/" + format + ".md"
		b, err := fs.ReadFile(style)
		if err != nil {
			t.Errorf("CRD outputFormat %q has no %s (design §2.2 enum/style lock-step): %v", format, style, err)
			continue
		}
		if strings.TrimSpace(string(b)) == "" {
			t.Errorf("%s is empty", style)
		}
	}
}
