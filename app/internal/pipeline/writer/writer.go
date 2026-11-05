// Package writer implements pipeline subtask F (ISI-7081, design §3
// consumer 3 + §4 Writer): generation driven by outputFormat — it loads
// styles/<outputFormat>.md, conditions on the analyzer Sample brief (source
// meta.yaml + body) and the run Notes, and emits the Mix (draft + output
// meta.yaml with the Sample-clearance source refs, design §5).
//
// Style selection (design §3 consumer 3):
//
//	blog         → styles/blog.md
//	short        → styles/short.md
//	tutorial     → styles/tutorial.md
//	teleprompter → styles/teleprompter.md
//
// The output-format set is closed (it mirrors the ReferenceRepo
// LabelRoute.OutputFormat CRD enum, design §2.2); an unknown format or a
// missing/unreadable style file fails closed. TestRepoStyleSetMatchesOutputFormats
// guards the enum ↔ styles/ lock-step. The persona/author voice card
// (styles/authors/<author>.md) is Phase 2 per design §3.
//
// Model calls go through the injectable Generator (the concrete default is
// the LiteLLM gateway client, internal/litellm, via LiteLLMGenerator). An
// empty GenerateRequest.Model lets the gateway default resolve it; the
// response's echoed model wins Mix.ModelUsed, falling back to the requested
// alias, then "default" — matching the Sample-clearance rendering
// "default (… via LiteLLM)" (§5).
package writer

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/ContentRemixer/Contentremixer/app/internal/litellm"
	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline"
)

// Output formats (mirrors the ReferenceRepo LabelRoute.OutputFormat CRD
// enum, design §2.2). Adding a format = add styles/<new>.md, extend the
// enum, `make manifests` (documented in design §2.2a).
const (
	FormatBlog         = "blog"
	FormatShort        = "short"
	FormatTutorial     = "tutorial"
	FormatTeleprompter = "teleprompter"
)

// validFormats is the closed Phase-1 output-format set.
var validFormats = map[string]bool{
	FormatBlog:         true,
	FormatShort:        true,
	FormatTutorial:     true,
	FormatTeleprompter: true,
}

// DefaultModelUsed is recorded in Mix.ModelUsed when neither the generator
// nor the run names a model — the gateway default
// (general_settings.default_model) resolved the request.
const DefaultModelUsed = "default"

// StyleFS abstracts read access to the reference repository checkout for
// style guides. analyzer.LocalFS satisfies it structurally; the abstraction
// keeps the writer testable against fixture trees (stages never import each
// other, design §4).
type StyleFS interface {
	// ReadFile returns the contents of the repo-relative file name.
	ReadFile(name string) ([]byte, error)
}

// GenerateRequest is one model call: the alias to serve (empty = gateway
// default) and the two prompt parts.
type GenerateRequest struct {
	// Model is the LiteLLM alias; empty means the gateway default.
	Model string
	// System carries the style-guide instructions.
	System string
	// Prompt carries the Sample brief + Notes.
	Prompt string
}

// GenerateResponse is the model completion. Model echoes the concrete
// model the gateway served (for Sample clearance) and may be empty when
// the generator cannot report it.
type GenerateResponse struct {
	// Content is the completion text.
	Content string
	// Model is the served model/alias, when known.
	Model string
}

// Generator performs the model call. Production wiring:
// LiteLLMGenerator(*litellm.Client); tests inject stubs.
type Generator interface {
	Generate(ctx context.Context, req GenerateRequest) (GenerateResponse, error)
}

// GeneratorFunc adapts a function to Generator.
type GeneratorFunc func(ctx context.Context, req GenerateRequest) (GenerateResponse, error)

// Generate implements Generator.
func (f GeneratorFunc) Generate(ctx context.Context, req GenerateRequest) (GenerateResponse, error) {
	return f(ctx, req)
}

// LiteLLMGenerator adapts a LiteLLM gateway client to Generator.
func LiteLLMGenerator(c *litellm.Client) Generator {
	return GeneratorFunc(func(ctx context.Context, req GenerateRequest) (GenerateResponse, error) {
		content, model, err := c.Complete(ctx, req.Model, req.System, req.Prompt)
		if err != nil {
			return GenerateResponse{}, err
		}
		return GenerateResponse{Content: content, Model: model}, nil
	})
}

// Writer is the subtask-F implementation of pipeline.Writer.
var _ pipeline.Writer = (*Writer)(nil)

type Writer struct {
	// FS provides read access to styles/<format>.md in the reference repo
	// checkout. Required.
	FS StyleFS
	// Generate performs the model call. Required — Phase 1 wires the
	// LiteLLM gateway client via LiteLLMGenerator; tests inject fixtures.
	Generate Generator
	// Now clocks the generated timestamp in Mix.Meta. Defaults to
	// time.Now; injectable for deterministic tests.
	Now func() time.Time
}

// Write implements pipeline.Writer: load the style guide, condition on the
// Sample + Notes, generate, and assemble the Mix with clearance refs.
func (w *Writer) Write(ctx context.Context, rc pipeline.RunContext, s pipeline.Sample) (pipeline.Mix, error) {
	if w == nil || w.FS == nil {
		return pipeline.Mix{}, fmt.Errorf("writer: no styles filesystem configured")
	}
	if w.Generate == nil {
		return pipeline.Mix{}, fmt.Errorf("writer: no generator configured (wire the LiteLLM gateway client via LiteLLMGenerator)")
	}
	format := strings.TrimSpace(rc.OutputFormat)
	if format == "" {
		return pipeline.Mix{}, fmt.Errorf("writer: empty outputFormat in run context (issue %d)", rc.IssueNumber)
	}
	if !validFormats[format] {
		return pipeline.Mix{}, fmt.Errorf("writer: unsupported outputFormat %q (want one of blog, short, tutorial, teleprompter)", rc.OutputFormat)
	}
	if strings.TrimSpace(s.Body) == "" {
		return pipeline.Mix{}, fmt.Errorf("writer: sample from %q has an empty body; nothing to recycle", s.SourcePath)
	}

	styleName := "styles/" + format + ".md"
	styleBytes, err := w.FS.ReadFile(styleName)
	if err != nil {
		// §2.2a: the webhook names the missing style file to aid Admins;
		// the writer is the runtime fail-closed enforcement point.
		return pipeline.Mix{}, fmt.Errorf("writer: reading %s: %w", styleName, err)
	}
	style := strings.TrimSpace(string(styleBytes))
	if style == "" {
		return pipeline.Mix{}, fmt.Errorf("writer: style guide %s is empty", styleName)
	}

	resp, err := w.Generate.Generate(ctx, GenerateRequest{
		Model:  strings.TrimSpace(rc.ModelName),
		System: systemPrompt(format, style),
		Prompt: userPrompt(rc, s),
	})
	if err != nil {
		return pipeline.Mix{}, fmt.Errorf("writer: generating %s mix from %q: %w", format, s.SourcePath, err)
	}
	draft := strings.TrimSpace(resp.Content)
	if draft == "" {
		return pipeline.Mix{}, fmt.Errorf("writer: model returned an empty completion for %q (format %s)", s.SourcePath, format)
	}
	title := leadingTitle(draft)
	if title == "" {
		return pipeline.Mix{}, fmt.Errorf("writer: draft for %q does not lead with a \"# <title>\" heading (format %s)", s.SourcePath, format)
	}

	modelUsed := resp.Model
	if modelUsed == "" {
		modelUsed = strings.TrimSpace(rc.ModelName)
	}
	if modelUsed == "" {
		modelUsed = DefaultModelUsed
	}
	now := time.Now()
	if w.Now != nil {
		now = w.Now()
	}

	return pipeline.Mix{
		OutputFormat: format,
		Title:        title,
		Markdown:     draft,
		Meta:         mixMeta(now, rc, s, modelUsed, title),
		ModelUsed:    modelUsed,
	}, nil
}

// systemPrompt binds the style guide into the model's instructions.
func systemPrompt(format, style string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are the ContentRemixer writer agent. Produce a %s-format Mix (a recycled derivative of the source provided in the user message).\n", format)
	fmt.Fprintf(&b, "Follow this style guide exactly — it is the definition of a correct %s draft:\n\n--- styles/%s.md ---\n%s\n--- end of style guide ---\n", format, format, style)
	b.WriteString("Rules:\n- Output ONLY the finished draft as Markdown, with no preamble or explanation.\n- The first line must be the H1 title heading: \"# <title>\".\n- Reuse the substance of the source; do not invent facts, numbers, or quotes.\n- No marketing voice.\n")
	return b.String()
}

// userPrompt carries the Sample brief (meta.yaml summary + body) and the
// run Notes (angle/audience guidance from the issue form).
func userPrompt(rc pipeline.RunContext, s pipeline.Sample) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Input format: %s\n", s.DetectedInput)
	fmt.Fprintf(&b, "Output format: %s\n", rc.OutputFormat)
	fmt.Fprintf(&b, "Recycle issue #%d\n", rc.IssueNumber)
	if t := metaString(s.Meta, "title"); t != "" {
		fmt.Fprintf(&b, "Source title: %s\n", t)
	}
	if a := metaString(s.Meta, "author"); a != "" {
		fmt.Fprintf(&b, "Source author: %s\n", a)
	}
	if tags := metaTags(s.Meta); len(tags) > 0 {
		fmt.Fprintf(&b, "Source tags: %s\n", strings.Join(tags, ", "))
	}
	if notes := strings.TrimSpace(rc.Notes); notes != "" {
		fmt.Fprintf(&b, "\nAngle / audience notes from the issue:\n%s\n", notes)
	}
	fmt.Fprintf(&b, "\nSource body:\n\n%s\n", s.Body)
	fmt.Fprintf(&b, "\nWrite the %s draft now.\n", rc.OutputFormat)
	return b.String()
}

// mixMeta builds the output meta.yaml map: Mix identity, source-derived
// fields carried over, the source block, and the sample_clearance block
// (design §5 — source path, source commit, model, input → output, recycle
// issue), so clearance survives outside the PR.
func mixMeta(now time.Time, rc pipeline.RunContext, s pipeline.Sample, modelUsed, title string) map[string]any {
	meta := map[string]any{
		"title":         title,
		"output_format": rc.OutputFormat,
		"generated":     now.UTC().Format(time.RFC3339),
		"source": map[string]any{
			"path":         s.SourcePath,
			"commit":       s.SourceCommit,
			"input_format": s.DetectedInput,
		},
		"sample_clearance": map[string]any{
			"source_path":   s.SourcePath,
			"source_commit": s.SourceCommit,
			"model":         modelUsed,
			"input_format":  s.DetectedInput,
			"output_format": rc.OutputFormat,
			"recycle_issue": rc.IssueNumber,
		},
	}
	if a := metaString(s.Meta, "author"); a != "" {
		meta["author"] = a
	}
	if tags := metaTags(s.Meta); len(tags) > 0 {
		meta["tags"] = tags
	}
	if notes := strings.TrimSpace(rc.Notes); notes != "" {
		meta["notes"] = notes
	}
	return meta
}

// RenderMetaYAML serializes a Mix meta map the way the PR assembler
// (subtask H) writes recycled/<slug>/meta.yaml.
func RenderMetaYAML(meta map[string]any) (string, error) {
	b, err := yaml.Marshal(meta)
	if err != nil {
		return "", fmt.Errorf("writer: rendering meta.yaml: %w", err)
	}
	return string(b), nil
}

// leadingTitle returns the H1 text when the draft leads with an H1 title
// (the writer's output contract), else "". A draft that does not lead with
// "# title" fails closed in Write.
func leadingTitle(draft string) string {
	for _, line := range strings.Split(draft, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "# ") {
			return ""
		}
		return strings.TrimSpace(strings.TrimPrefix(line, "# "))
	}
	return ""
}

// metaString returns meta.<key> as a non-empty string, or "".
func metaString(meta map[string]any, key string) string {
	s, _ := meta[key].(string)
	return strings.TrimSpace(s)
}

// metaTags returns meta.tags as a clean string slice, or nil.
func metaTags(meta map[string]any) []string {
	raw, ok := meta["tags"].([]any)
	if !ok {
		return nil
	}
	tags := make([]string, 0, len(raw))
	for _, t := range raw {
		if s, ok := t.(string); ok && strings.TrimSpace(s) != "" {
			tags = append(tags, strings.TrimSpace(s))
		}
	}
	if len(tags) == 0 {
		return nil
	}
	return tags
}
