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

// captureGen records the last GenerateRequest and returns a canned
// response — the LiteLLM client stands in for it in production.
func captureGen(resp GenerateResponse, err error) (*GenerateRequest, Generator) {
	var last GenerateRequest
	return &last, GeneratorFunc(func(_ context.Context, req GenerateRequest) (GenerateResponse, error) {
		last = req
		return resp, err
	})
}

// --- acceptance: recycle:blog style from a teleprompter brief ---

func TestWriteBlogFromTeleprompterBrief(t *testing.T) {
	last, gen := captureGen(GenerateResponse{Content: blogDraft, Model: "anthropic/claude-opus-4"}, nil)
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
	if mix.ModelUsed != "anthropic/claude-opus-4" {
		t.Fatalf("ModelUsed = %q, want the gateway-echoed model", mix.ModelUsed)
	}

	clearance, ok := mix.Meta["sample_clearance"].(map[string]any)
	if !ok {
		t.Fatalf("Meta.sample_clearance missing: %+v", mix.Meta)
	}
	want := map[string]any{
		"source_path":   "content/videos/2026/demo-video",
		"source_commit": testCommit,
		"model":         "anthropic/claude-opus-4",
		"input_format":  FormatTeleprompter,
		"output_format": FormatBlog,
		"recycle_issue": 42,
	}
	for k, v := range want {
		if clearance[k] != v {
			t.Fatalf("sample_clearance.%s = %v, want %v", k, clearance[k], v)
		}
	}
	if mix.Meta["title"] != mix.Title || mix.Meta["output_format"] != FormatBlog {
		t.Fatalf("Meta draft identity wrong: %+v", mix.Meta)
	}
	if mix.Meta["author"] != "henrik" {
		t.Fatalf("Meta.author = %v, want source author carried over", mix.Meta["author"])
	}
	tags, _ := mix.Meta["tags"].([]string)
	if len(tags) != 2 || tags[0] != "kubernetes" || tags[1] != "secrets" {
		t.Fatalf("Meta.tags = %#v, want source tags carried over", mix.Meta["tags"])
	}
	if mix.Meta["generated"] != fixedNow.Format(time.RFC3339) {
		t.Fatalf("Meta.generated = %v, want the injected clock value", mix.Meta["generated"])
	}
	if mix.Meta["notes"] != "Audience: platform engineers; angle: prevention over cleanup" {
		t.Fatalf("Meta.notes = %v, want the issue notes carried", mix.Meta["notes"])
	}
	src, _ := mix.Meta["source"].(map[string]any)
	if src["path"] != "content/videos/2026/demo-video" || src["commit"] != testCommit || src["input_format"] != FormatTeleprompter {
		t.Fatalf("Meta.source wrong: %+v", mix.Meta["source"])
	}
	if last.Model != "fast" {
		t.Fatalf("generator received model %q, want the rc alias", last.Model)
	}

	// The output meta.yaml must serialize cleanly (PR assembler writes it).
	yaml, err := RenderMetaYAML(mix.Meta)
	if err != nil {
		t.Fatalf("RenderMetaYAML: %v", err)
	}
	for _, wantSub := range []string{"sample_clearance:", "source_commit: " + testCommit, "recycle_issue: 42"} {
		if !strings.Contains(yaml, wantSub) {
			t.Fatalf("rendered meta.yaml missing %q:\n%s", wantSub, yaml)
		}
	}
}

// --- ModelUsed resolution: response echo > rc alias > default ---

func TestModelUsedFallsBackToAliasThenDefault(t *testing.T) {
	// No echoed model, alias present.
	_, gen := captureGen(GenerateResponse{Content: "# T\n\nb"}, nil)
	mix, err := newWriter(gen).Write(context.Background(), blogRunContext(), teleprompterSample())
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if mix.ModelUsed != "fast" {
		t.Fatalf("ModelUsed = %q, want the rc alias fallback", mix.ModelUsed)
	}

	// No echoed model, no alias.
	_, gen = captureGen(GenerateResponse{Content: "# T\n\nb"}, nil)
	rc := blogRunContext()
	rc.ModelName = ""
	mix, err = newWriter(gen).Write(context.Background(), rc, teleprompterSample())
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if mix.ModelUsed != DefaultModelUsed {
		t.Fatalf("ModelUsed = %q, want %q", mix.ModelUsed, DefaultModelUsed)
	}
}

// --- conditioning: prompt carries style guide + brief + notes ---

func TestPromptConditionsOnStyleBriefAndNotes(t *testing.T) {
	last, gen := captureGen(GenerateResponse{Content: "# T\n\nb"}, nil)
	if _, err := newWriter(gen).Write(context.Background(), blogRunContext(), teleprompterSample()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	for _, wantSub := range []string{
		"styles/blog.md",                   // the selected style file is named
		"Title with the problem in it",     // style guide verbatim
		"no marketing voice",               // style guide verbatim
		"# <title>",                        // output contract
		"Input format: teleprompter",       // detected input
		"Output format: blog",              // route output
		"Recycle issue #42",                // traceability
		"Source title: Demo video",         // source meta.yaml title
		"Source author: henrik",            // source meta.yaml author
		"Source tags: kubernetes, secrets", // source meta.yaml tags
		"Angle / audience notes from the issue:\nAudience: platform engineers; angle: prevention over cleanup",
		"Welcome back to the show", // the source body itself
	} {
		if !strings.Contains(last.System+last.Prompt, wantSub) {
			t.Fatalf("prompt missing %q\nsystem:\n%s\nprompt:\n%s", wantSub, last.System, last.Prompt)
		}
	}
}

func TestPromptSelectsStyleFileByOutputFormat(t *testing.T) {
	last, gen := captureGen(GenerateResponse{Content: "# Hook!\n\nSixty seconds, one idea."}, nil)
	rc := blogRunContext()
	rc.OutputFormat = FormatShort
	rc.Notes = ""
	if _, err := newWriter(gen).Write(context.Background(), rc, teleprompterSample()); err != nil {
		t.Fatalf("Write (short): %v", err)
	}
	if !strings.Contains(last.System, "styles/short.md") || !strings.Contains(last.System, "Hook in the first 3 seconds") {
		t.Fatalf("short run did not load styles/short.md verbatim:\n%s", last.System)
	}
	if strings.Contains(last.System, "styles/blog.md") {
		t.Fatalf("blog style leaked into a short run:\n%s", last.System)
	}
	if strings.Contains(last.Prompt, "Angle / audience notes") {
		t.Fatalf("notes section emitted for empty Notes:\n%s", last.Prompt)
	}
}

// --- fail-closed errors ---

func TestWriteErrors(t *testing.T) {
	okGen := GeneratorFunc(func(context.Context, GenerateRequest) (GenerateResponse, error) {
		return GenerateResponse{Content: "# T\n\nbody"}, nil
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
			wantSub: "reading styles/tutorial.md",
		},
		{
			name:    "empty style file",
			w:       newWriter(okGen),
			rc:      pipeline.RunContext{OutputFormat: FormatTeleprompter},
			sample:  teleprompterSample(),
			wantSub: "styles/teleprompter.md is empty",
		},
		{
			name: "generator error propagates",
			w: newWriter(GeneratorFunc(func(context.Context, GenerateRequest) (GenerateResponse, error) {
				return GenerateResponse{}, errors.New("litellm: 503 unavailable")
			})),
			rc:      blogRunContext(),
			sample:  teleprompterSample(),
			wantSub: "litellm: 503 unavailable",
		},
		{
			name: "empty completion",
			w: newWriter(GeneratorFunc(func(context.Context, GenerateRequest) (GenerateResponse, error) {
				return GenerateResponse{Content: "   "}, nil
			})),
			rc:      blogRunContext(),
			sample:  teleprompterSample(),
			wantSub: "empty completion",
		},
		{
			name: "completion without leading H1 fails closed",
			w: newWriter(GeneratorFunc(func(context.Context, GenerateRequest) (GenerateResponse, error) {
				return GenerateResponse{Content: "Just prose, no heading."}, nil
			})),
			rc:      blogRunContext(),
			sample:  teleprompterSample(),
			wantSub: `does not lead with a "# <title>" heading`,
		},
		{
			name:    "nil generator",
			w:       &Writer{FS: fixtureFS{root: "testdata"}, Now: func() time.Time { return fixedNow }},
			rc:      blogRunContext(),
			sample:  teleprompterSample(),
			wantSub: "no generator configured",
		},
		{
			name:    "nil FS",
			w:       &Writer{Generate: okGen, Now: func() time.Time { return fixedNow }},
			rc:      blogRunContext(),
			sample:  teleprompterSample(),
			wantSub: "no styles filesystem configured",
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

func TestLeadingTitle(t *testing.T) {
	cases := map[string]string{
		"# Hello\n\nbody":            "Hello",
		"\n\n#  Padded title \nbody": "Padded title",
		"No heading":                 "",
		"para\n# later heading":      "",
		"## h2 first":                "",
		"#":                          "",
		"":                           "",
	}
	for in, want := range cases {
		if got := leadingTitle(in); got != want {
			t.Errorf("leadingTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- lock-step guard: the real repo ships a style file per CRD enum value ---

func TestRepoStyleSetMatchesOutputFormats(t *testing.T) {
	fs := fixtureFS{root: filepath.Join("..", "..", "..", "..")} // repo root
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
