// Package contenttest runs an interactive, PR-less persona content test: given a
// topic and a persona's synthesized style profile, it drives the same
// writer-conditioning path the recycle pipeline uses (app/internal/pipeline
// writer + litellm) to produce one short sample demonstrating the persona's
// voice on that topic (ISI-7359, plan §5). It is NOT a recycle run — it writes
// nothing, opens no PR, and emits no Mix; it returns the sample to the caller
// synchronously so the onboarding "test your style" surface (O8) can render it.
//
// It is a PUBLIC app package on purpose, mirroring app/review: the Booth API is
// a separate module and cannot import app/internal, so it reaches the generator
// (the LiteLLM gateway client) only through this contract. The §6.3 ownership
// gate, the StyleProfile lookup, and the rate/cost guard live in the Booth
// gating store (booth/internal/contenttest) that wraps this Runner; this package
// performs NO authorization.
//
// Conditioning inputs come from the frozen D1 model (ISI-7356): the synthesized
// StyleProfile.Traits (the reviewed voice description), the persona's
// spec.selfDescription (the deterministic voice anchor), and the persona's
// spec.primaryContentKind (the target output medium). The test deliberately does
// not retrieve voice exemplars — the synthesized Traits already distil them, and
// a content test must stay a cheap single generation, not a retrieval run.
package contenttest

import (
	"context"
	"fmt"
	"strings"

	"github.com/ContentRemixer/Contentremixer/app/internal/litellm"
	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline/writer"
)

// Request is one content test: the persona under test, the topic to write about,
// and the conditioning inputs resolved from the persona CR + its StyleProfile.
type Request struct {
	// PersonaName is the persona under test (identity/labelling only).
	PersonaName string
	// Topic is the subject the sample should be written about. Required.
	Topic string
	// PrimaryContentKind is the target output medium (blog|video|talk|tutorial)
	// from spec.primaryContentKind. Empty = medium-agnostic (plan O2 / ISI-7356).
	PrimaryContentKind string
	// StyleTraits is the synthesized voice/tone description (StyleProfile.Traits,
	// D3). It is the primary conditioning signal. May be empty if synthesis has
	// not run — the test then falls back to SelfDescription only.
	StyleTraits string
	// SelfDescription is the persona's deterministic voice anchor
	// (spec.selfDescription). Optional.
	SelfDescription string
}

// Result is the generated sample and the model that served it.
type Result struct {
	// Sample is the generated passage demonstrating the persona's voice.
	Sample string
	// ModelUsed is the served model/alias (for display + cost attribution).
	ModelUsed string
}

// Runner executes a content test over an injected Generator (the LiteLLM gateway
// client in production; a stub in tests). It is safe for concurrent use.
type Runner struct {
	gen   writer.Generator
	model string
}

// NewRunner builds a Runner backed by the LiteLLM gateway at baseURL. model is
// the alias to request (empty = the gateway's configured default); the Booth
// deploy passes BOOTH_CONTENT_TEST_MODEL. A nil/empty baseURL yields a Runner
// whose Run fails fast with a clear "no gateway" error (matching the writer).
func NewRunner(baseURL, apiKey, model string) *Runner {
	client := &litellm.Client{BaseURL: baseURL, APIKey: apiKey, DefaultModel: model}
	return &Runner{gen: writer.LiteLLMGenerator(client), model: strings.TrimSpace(model)}
}

// NewRunnerWithGenerator builds a Runner over an explicit Generator, for tests
// and alternate wirings.
func NewRunnerWithGenerator(gen writer.Generator, model string) *Runner {
	return &Runner{gen: gen, model: strings.TrimSpace(model)}
}

// DefaultModelUsed is reported when neither the generator nor the request names a
// model — the gateway default resolved it (mirrors writer.DefaultModelUsed).
const DefaultModelUsed = "default"

// Run performs the content test: build the style-conditioned prompt and generate
// one sample. It fails closed on an empty topic, a missing generator, a model
// error, or an empty completion — the onboarding surface then shows a real error
// rather than a blank sample.
func (r *Runner) Run(ctx context.Context, req Request) (Result, error) {
	if r == nil || r.gen == nil {
		return Result{}, fmt.Errorf("contenttest: no generator configured")
	}
	topic := strings.TrimSpace(req.Topic)
	if topic == "" {
		return Result{}, fmt.Errorf("contenttest: empty topic; nothing to write about")
	}
	resp, err := r.gen.Generate(ctx, writer.GenerateRequest{
		Model:  r.model,
		System: systemPrompt(req),
		Prompt: userPrompt(req, topic),
	})
	if err != nil {
		return Result{}, fmt.Errorf("contenttest: generating sample for persona %q: %w", req.PersonaName, err)
	}
	sample := strings.TrimSpace(resp.Content)
	if sample == "" {
		return Result{}, fmt.Errorf("contenttest: model returned an empty sample for persona %q", req.PersonaName)
	}
	modelUsed := strings.TrimSpace(resp.Model)
	if modelUsed == "" {
		modelUsed = r.model
	}
	if modelUsed == "" {
		modelUsed = DefaultModelUsed
	}
	return Result{Sample: sample, ModelUsed: modelUsed}, nil
}

// mediumGuidance maps a primaryContentKind to a one-line brief describing the
// shape of a sample for that medium. An unknown/empty kind gets a generic brief.
func mediumGuidance(kind string) string {
	switch strings.TrimSpace(kind) {
	case "blog":
		return "a few short paragraphs of a blog post"
	case "video":
		return "a short spoken-style video script segment"
	case "talk":
		return "a short passage of conference-talk narration"
	case "tutorial":
		return "the opening of a hands-on technical tutorial"
	default:
		return "a few short paragraphs"
	}
}

// systemPrompt binds the synthesized style profile and self-description into the
// model instructions. Precedence mirrors the recycle writer (§5.3): the style
// profile is the definition of the voice; the self-description anchors it. The
// instruction is explicit that this is an unpublished STYLE PREVIEW — original
// prose on the topic in the persona's voice, not a recycle of any source.
func systemPrompt(req Request) string {
	var b strings.Builder
	b.WriteString("You are the ContentRemixer writer agent running an interactive STYLE TEST for a DevRel persona.\n")
	_, _ = fmt.Fprintf(&b, "Write %s on the topic given in the user message, in this persona's voice. This is an unpublished preview the DevRel uses to judge their synthesized style — it is not a recycled derivative of any source.\n", mediumGuidance(req.PrimaryContentKind))
	if traits := strings.TrimSpace(req.StyleTraits); traits != "" {
		_, _ = fmt.Fprintf(&b, "\nThe persona's synthesized writing style — match it exactly; it is the definition of a correct sample:\n\n--- style profile ---\n%s\n--- end of style profile ---\n", traits)
	}
	if sd := strings.TrimSpace(req.SelfDescription); sd != "" {
		_, _ = fmt.Fprintf(&b, "\nThe persona also describes their own voice as:\n\n--- persona self-description ---\n%s\n--- end of persona self-description ---\n", sd)
	}
	b.WriteString("Rules:\n- Output ONLY the sample prose as Markdown, with no preamble, title, or explanation.\n- Keep it short — a demonstration of voice, not a finished article.\n- Write about the given topic specifically; do not invent statistics, quotes, or product claims.\n- No marketing voice.\n")
	return b.String()
}

// userPrompt carries the topic (and target medium for context).
func userPrompt(req Request, topic string) string {
	var b strings.Builder
	if kind := strings.TrimSpace(req.PrimaryContentKind); kind != "" {
		_, _ = fmt.Fprintf(&b, "Target medium: %s\n", kind)
	}
	_, _ = fmt.Fprintf(&b, "Topic: %s\n\nWrite the style-test sample now.\n", topic)
	return b.String()
}
