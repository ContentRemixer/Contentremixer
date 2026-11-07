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

// PersonaOverlay is the persona voice overlay the conditioner supplies for a
// run (design §5.1): the deterministic self-description anchor plus the
// retrieved top-k voice exemplars. The zero value (no self-description, no
// exemplars) is the Phase-1 style-only prompt — weaving it changes nothing.
type PersonaOverlay struct {
	// SelfDescription is DevRelPersona.spec.selfDescription — the deterministic
	// half of conditioning. It anchors the voice even when retrieval is empty
	// or degraded and is woven into the system prompt after the style guide.
	SelfDescription string
	// Exemplars are the top-k owner-scoped voice chunks (the retrieval half),
	// woven into the user prompt as tone references only.
	Exemplars []Exemplar
	// Degraded is true when retrieval was unavailable or returned nothing; the
	// run still proceeds (RAG is additive, never a gate — §5.4). It is carried
	// for observability (the §4.6 outcome ladder) and does not alter the prompt.
	Degraded bool
}

// Exemplar is one retrieved voice chunk used as a tone reference (design §5.1).
type Exemplar struct {
	// Text is the chunk body — a tone/register reference, never a fact source.
	Text string
	// SourceURL traces the exemplar to its ingested reference (provenance).
	SourceURL string
	// Score is the cosine similarity of the exemplar to the retrieval seed.
	Score float64
}

// PersonaConditioner supplies the persona voice overlay for a run (design
// §5.1). Nil on the Writer => Phase-1 behaviour (style-only). Implemented by
// internal/pipeline/persona over internal/rag/retrieve backed by a
// VectorStore; tests inject stubs.
//
// BEST-EFFORT: Condition returns an empty overlay (not an error) when the run
// has no persona or retrieval finds nothing, and degrades to style-only
// (overlay.Degraded=true) on a retrieval/store error. A recycle run never
// blocks on RAG (§2.3 step 5 / §5.4) — the writer treats a Condition error as
// a degrade, not a failure.
type PersonaConditioner interface {
	Condition(ctx context.Context, rc pipeline.RunContext, s pipeline.Sample) (PersonaOverlay, error)
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
	// Conditioner supplies the persona voice overlay (design §5). Optional:
	// nil (or an empty rc.PersonaName) is the Phase-1 style-only path,
	// byte-for-byte unchanged. When set and the run names a persona, the
	// overlay is woven into the prompts additively (never replacing the style
	// guide) and best-effort — a Condition error degrades to style-only, it
	// never fails the write (§5.4).
	Conditioner PersonaConditioner
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

	// Persona conditioning (design §5): additive to the style-only prompt and
	// best-effort. Only runs when a conditioner is wired AND the run names a
	// persona; a Condition error degrades to style-only (overlay stays zero)
	// and never fails the write — RAG is an enhancement, not a gate (§5.4).
	var overlay PersonaOverlay
	if w.Conditioner != nil && strings.TrimSpace(rc.PersonaName) != "" {
		overlay, err = w.Conditioner.Condition(ctx, rc, s)
		if err != nil {
			// Degrade, do not fail: the Phase-1 style-only prompt still runs.
			overlay = PersonaOverlay{Degraded: true}
		}
	}

	resp, err := w.Generate.Generate(ctx, GenerateRequest{
		Model:  strings.TrimSpace(rc.ModelName),
		System: systemPrompt(format, style, overlay),
		Prompt: userPrompt(rc, s, overlay),
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

// systemPrompt binds the style guide into the model's instructions. When the
// persona overlay carries a self-description, a `persona self-description`
// block is appended AFTER the style guide (design §5.3): the deterministic
// voice anchor that applies even when retrieval is empty/degraded. Precedence
// is explicit — style guide outranks the persona voice — so the overlay never
// overrides a correct-format draft.
func systemPrompt(format, style string, overlay PersonaOverlay) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are the ContentRemixer writer agent. Produce a %s-format Mix (a recycled derivative of the source provided in the user message).\n", format)
	fmt.Fprintf(&b, "Follow this style guide exactly — it is the definition of a correct %s draft:\n\n--- styles/%s.md ---\n%s\n--- end of style guide ---\n", format, format, style)
	if sd := strings.TrimSpace(overlay.SelfDescription); sd != "" {
		fmt.Fprintf(&b, "\nWrite as this specific DevRel persona. Their voice:\n\n--- persona self-description ---\n%s\n--- end of persona self-description ---\n", sd)
		b.WriteString("The style guide outranks the persona voice: never break the required format to match the voice.\n")
	}
	b.WriteString("Rules:\n- Output ONLY the finished draft as Markdown, with no preamble or explanation.\n- The first line must be the H1 title heading: \"# <title>\".\n- Reuse the substance of the source; do not invent facts, numbers, or quotes.\n- No marketing voice.\n")
	return b.String()
}

// userPrompt carries the Sample brief (meta.yaml summary + body) and the
// run Notes (angle/audience guidance from the issue form). When the persona
// overlay carries exemplars, a `voice exemplars` block is appended (design
// §5.3) — tone references ONLY: the no-invented-facts / reuse-the-source rules
// from the system prompt still bind, so exemplars must not leak as content.
func userPrompt(rc pipeline.RunContext, s pipeline.Sample, overlay PersonaOverlay) string {
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
	if ex := overlay.Exemplars; len(ex) > 0 {
		b.WriteString("\n--- voice exemplars (do not copy facts, match tone/register) ---\n")
		b.WriteString("These are prior writings by the persona. Mirror their tone, rhythm, and register ONLY — do not reuse their facts, numbers, or claims; the source body above is the sole source of substance.\n")
		for i, e := range ex {
			if t := strings.TrimSpace(e.Text); t != "" {
				fmt.Fprintf(&b, "\nExemplar %d:\n%s\n", i+1, t)
			}
		}
		b.WriteString("--- end of voice exemplars ---\n")
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
