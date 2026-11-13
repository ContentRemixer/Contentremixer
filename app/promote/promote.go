// Package promote turns a piece of already-validated DevRel content into a
// suggested LinkedIn promotion post, written in the persona's synthesized voice
// (ISI-7369, plan §5 O8b/P4). It reuses the same writer-conditioning path the
// recycle pipeline and the content-test use (app/internal/pipeline writer +
// litellm): a promotion post is a repurpose of the content, not a new recycle,
// and it writes nothing, publishes nothing, and opens no PR.
//
// v1 is suggestion-only (plan §10 decision 5): there is NO LinkedIn OAuth and NO
// auto-publish. The output is an EDITABLE draft — a LinkedIn-shaped hook + body +
// CTA the DevRel copies, edits, or regenerates on the O8b promote screen. The
// decomposition is best-effort: the authoritative deliverable is Suggestion.Post
// (the full assembled text); Hook/Body/CTA are a structural convenience for a UI
// that wants to render or edit the parts separately, and always yield something
// even if the model ignores the requested delimiters.
//
// Like app/review and app/contenttest, this is a PUBLIC app package on purpose:
// the Booth API is a separate module and cannot import app/internal, so it
// reaches the generator only through this contract. The §6.3 ownership gate, the
// "style must be validated first" precondition, the StyleProfile lookup, and the
// rate/cost guard live in the Booth gating store (booth/internal/promote) that
// wraps this Writer; this package performs NO authorization.
package promote

import (
	"context"
	"fmt"
	"strings"

	"github.com/ContentRemixer/Contentremixer/app/internal/litellm"
	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline/writer"
)

// Request is one promotion-suggestion call: the content to promote plus the
// conditioning inputs resolved from the persona CR + its StyleProfile.
type Request struct {
	// PersonaName is the persona whose voice the post is written in
	// (identity/labelling only).
	PersonaName string
	// Content is the validated content being promoted — the text the LinkedIn
	// post should drive readers to. Required.
	Content string
	// Title is the content's title, used in the hook/CTA when present. Optional.
	Title string
	// URL is the canonical link to the content, appended to the CTA when present.
	// v1 never auto-publishes, so this is only woven into the editable draft.
	// Optional.
	URL string
	// PrimaryContentKind is the content's medium (blog|video|talk|tutorial) from
	// spec.primaryContentKind, used to phrase the promo naturally ("new post",
	// "new video"). Empty = generic phrasing.
	PrimaryContentKind string
	// StyleTraits is the synthesized voice/tone description (StyleProfile.Traits,
	// D3). It is the primary conditioning signal. May be empty.
	StyleTraits string
	// SelfDescription is the persona's deterministic voice anchor
	// (spec.selfDescription). Optional.
	SelfDescription string
}

// Suggestion is the suggested LinkedIn post. Post is the authoritative editable
// draft; Hook/Body/CTA are the decomposed parts for a UI that edits them
// separately.
type Suggestion struct {
	// Hook is the scroll-stopping opening line.
	Hook string
	// Body is the middle of the post — why the content matters.
	Body string
	// CTA is the closing call to action (read/watch it, with the link).
	CTA string
	// Post is the full assembled, ready-to-edit LinkedIn post.
	Post string
	// ModelUsed is the served model/alias (for display + cost attribution).
	ModelUsed string
}

// Writer generates promotion suggestions over an injected Generator (the LiteLLM
// gateway client in production; a stub in tests). It is safe for concurrent use.
type Writer struct {
	gen   writer.Generator
	model string
}

// NewWriter builds a Writer backed by the LiteLLM gateway at baseURL. model is
// the alias to request (empty = the gateway's configured default); the Booth
// deploy passes BOOTH_PROMOTE_MODEL. A nil/empty baseURL yields a Writer whose
// Suggest fails fast with a clear "no gateway" error.
func NewWriter(baseURL, apiKey, model string) *Writer {
	client := &litellm.Client{BaseURL: baseURL, APIKey: apiKey, DefaultModel: model}
	return &Writer{gen: writer.LiteLLMGenerator(client), model: strings.TrimSpace(model)}
}

// NewWriterWithGenerator builds a Writer over an explicit Generator, for tests
// and alternate wirings.
func NewWriterWithGenerator(gen writer.Generator, model string) *Writer {
	return &Writer{gen: gen, model: strings.TrimSpace(model)}
}

// DefaultModelUsed is reported when neither the generator nor the request names a
// model — the gateway default resolved it (mirrors writer.DefaultModelUsed).
const DefaultModelUsed = "default"

// Suggest generates the promotion post: build the style-conditioned prompt,
// generate, and decompose the completion into hook/body/CTA. It fails closed on
// empty content, a missing generator, a model error, or an empty completion — so
// the promote surface shows a real error rather than a blank suggestion.
func (w *Writer) Suggest(ctx context.Context, req Request) (Suggestion, error) {
	if w == nil || w.gen == nil {
		return Suggestion{}, fmt.Errorf("promote: no generator configured")
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		return Suggestion{}, fmt.Errorf("promote: empty content; nothing to promote")
	}
	resp, err := w.gen.Generate(ctx, writer.GenerateRequest{
		Model:  w.model,
		System: systemPrompt(req),
		Prompt: userPrompt(req, content),
	})
	if err != nil {
		return Suggestion{}, fmt.Errorf("promote: generating LinkedIn suggestion for persona %q: %w", req.PersonaName, err)
	}
	raw := strings.TrimSpace(resp.Content)
	if raw == "" {
		return Suggestion{}, fmt.Errorf("promote: model returned an empty suggestion for persona %q", req.PersonaName)
	}
	hook, body, cta := decompose(raw)
	modelUsed := strings.TrimSpace(resp.Model)
	if modelUsed == "" {
		modelUsed = w.model
	}
	if modelUsed == "" {
		modelUsed = DefaultModelUsed
	}
	return Suggestion{
		Hook:      hook,
		Body:      body,
		CTA:       cta,
		Post:      assemble(hook, body, cta),
		ModelUsed: modelUsed,
	}, nil
}

// Section markers the model is asked to emit, one per line. Parsing is
// best-effort: a model that ignores them still yields a usable decomposition via
// the paragraph fallback below.
const (
	markHook = "[HOOK]"
	markBody = "[BODY]"
	markCTA  = "[CTA]"
)

// decompose splits the completion into hook/body/CTA. It first tries the
// requested [HOOK]/[BODY]/[CTA] markers; if they are absent it falls back to a
// paragraph split (first paragraph = hook, last = CTA, middle = body), and if
// there is only one paragraph the whole text becomes the body. It never drops
// text: assemble(decompose(x)) reproduces x's content.
func decompose(raw string) (hook, body, cta string) {
	if strings.Contains(raw, markHook) || strings.Contains(raw, markBody) || strings.Contains(raw, markCTA) {
		hook = section(raw, markHook, markBody, markCTA)
		body = section(raw, markBody, markHook, markCTA)
		cta = section(raw, markCTA, markHook, markBody)
		// If markers were present but a part came back empty, fold any text that
		// precedes the first marker into the body so nothing is lost.
		if pre := beforeFirstMarker(raw); pre != "" {
			body = strings.TrimSpace(pre + "\n\n" + body)
		}
		if hook != "" || body != "" || cta != "" {
			return hook, body, cta
		}
	}
	paras := paragraphs(raw)
	switch len(paras) {
	case 0:
		return "", "", ""
	case 1:
		return "", paras[0], ""
	case 2:
		return paras[0], "", paras[1]
	default:
		return paras[0], strings.Join(paras[1:len(paras)-1], "\n\n"), paras[len(paras)-1]
	}
}

// section returns the text following marker up to whichever of the other markers
// appears next (or end of string), trimmed.
func section(raw, marker string, others ...string) string {
	i := strings.Index(raw, marker)
	if i < 0 {
		return ""
	}
	rest := raw[i+len(marker):]
	end := len(rest)
	for _, o := range others {
		if j := strings.Index(rest, o); j >= 0 && j < end {
			end = j
		}
	}
	return strings.TrimSpace(rest[:end])
}

// beforeFirstMarker returns any text preceding the earliest marker, trimmed.
func beforeFirstMarker(raw string) string {
	first := len(raw)
	for _, m := range []string{markHook, markBody, markCTA} {
		if i := strings.Index(raw, m); i >= 0 && i < first {
			first = i
		}
	}
	return strings.TrimSpace(raw[:first])
}

// paragraphs splits text on blank lines into non-empty, trimmed paragraphs.
func paragraphs(raw string) []string {
	var out []string
	for _, p := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n\n") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// assemble joins the non-empty parts into the full editable post.
func assemble(hook, body, cta string) string {
	var parts []string
	for _, p := range []string{hook, body, cta} {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "\n\n")
}

// contentNoun describes the thing being promoted, for natural promo phrasing.
func contentNoun(kind string) string {
	switch strings.TrimSpace(kind) {
	case "blog":
		return "blog post"
	case "video":
		return "video"
	case "talk":
		return "talk"
	case "tutorial":
		return "tutorial"
	default:
		return "post"
	}
}

// systemPrompt binds the synthesized style profile and self-description into the
// model instructions and specifies the LinkedIn hook/body/CTA shape. It is
// explicit that this is a SUGGESTED, UNPUBLISHED draft (v1 decision 5): the DevRel
// edits it; nothing is posted to LinkedIn.
func systemPrompt(req Request) string {
	noun := contentNoun(req.PrimaryContentKind)
	var b strings.Builder
	b.WriteString("You are the ContentRemixer writer agent drafting a SUGGESTED LinkedIn promotion post for a DevRel persona.\n")
	_, _ = fmt.Fprintf(&b, "The DevRel has published a %s and wants a LinkedIn post that promotes it in their own voice. This is an unpublished draft the DevRel will edit before posting — nothing is posted automatically.\n", noun)
	if traits := strings.TrimSpace(req.StyleTraits); traits != "" {
		_, _ = fmt.Fprintf(&b, "\nThe persona's synthesized writing style — match it; it is the definition of a correct voice:\n\n--- style profile ---\n%s\n--- end of style profile ---\n", traits)
	}
	if sd := strings.TrimSpace(req.SelfDescription); sd != "" {
		_, _ = fmt.Fprintf(&b, "\nThe persona also describes their own voice as:\n\n--- persona self-description ---\n%s\n--- end of persona self-description ---\n", sd)
	}
	b.WriteString("\nShape the post as LinkedIn copy with three parts, each on its own line prefixed by its marker exactly:\n")
	b.WriteString(markHook + " a single scroll-stopping opening line (no hashtags).\n")
	b.WriteString(markBody + " 2-4 short lines on why this matters and what the reader will learn. Blank lines between thoughts, LinkedIn-style.\n")
	_, _ = fmt.Fprintf(&b, "%s a closing call to action inviting the reader to read the %s", markCTA, noun)
	if strings.TrimSpace(req.URL) != "" {
		b.WriteString(", and include the link given in the user message")
	}
	b.WriteString(".\n")
	b.WriteString("\nRules:\n- Emit ONLY the three marked sections, nothing before or after.\n- Write in the persona's voice; no generic marketing hype.\n- Do not invent statistics, quotes, or claims not supported by the content.\n- Keep it tight — LinkedIn rewards brevity. A few short hashtags at the end of the CTA are fine.\n")
	return b.String()
}

// userPrompt carries the content to promote plus its title/URL context.
func userPrompt(req Request, content string) string {
	var b strings.Builder
	if t := strings.TrimSpace(req.Title); t != "" {
		_, _ = fmt.Fprintf(&b, "Title: %s\n", t)
	}
	if u := strings.TrimSpace(req.URL); u != "" {
		_, _ = fmt.Fprintf(&b, "Link: %s\n", u)
	}
	_, _ = fmt.Fprintf(&b, "\nThe content to promote:\n\n--- content ---\n%s\n--- end of content ---\n\nWrite the suggested LinkedIn post now.\n", content)
	return b.String()
}
