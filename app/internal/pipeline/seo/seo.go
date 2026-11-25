// Package seo is the ISI-7806 post-write SEO enrichment stage: a single
// bounded LLM pass over the generated Mix body that produces the SEO metadata
// the rest of the pipeline already expects but never had a producer for —
// the meta description (checker.seoMetaCheck), the primary keyword and schema
// block (ISI-7807 warn rules), and the blog PR-template fields (pr.template:
// meta_description, canonical_url, primary_keyword, tags).
//
// It is modeled on the D3 style synthesizer (app/internal/pipeline/style): it
// imports ONLY the LiteLLM Completer seam — no store, no DB, no CR — and the
// app wires it in cmd/main.go behind a config gate, exactly like the writer's
// persona conditioner.
//
// The ONE discipline that differs from style (which is fail-closed) is the
// whole point of this stage: it is DEGRADE-NEVER-FAIL. SEO advises on
// discoverability; it must never block a run. Every failure path — no
// completer, empty body, LLM error, unparseable output — returns the input Mix
// UNCHANGED. The Runner inserts it between Write and Check; a run with SEO
// disabled or failing produces exactly the draft the Writer wrote.
package seo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline"
)

// Length budget for the meta description, in RUNES (ISI-7805 review note:
// "chars" means runes, not bytes — multibyte-heavy copy measured short under
// the P0 byte budget). This stage owns the length contract end-to-end: the
// optimizer trims to MaxDescRunes and the checker measures the same unit.
const (
	// MinDescRunes is the lower bound seoMetaCheck warns below (advisory).
	MinDescRunes = 140
	// MaxDescRunes is the hard ceiling the optimizer trims to.
	MaxDescRunes = 160
)

// DefaultMaxBodyChars caps the draft text sent to the model (cost guard),
// mirroring style.DefaultMaxChars: ≈6k tokens, comfortably inside a small
// context window alongside the system prompt.
const DefaultMaxBodyChars = 24000

// Guard bounds one optimization. The zero value uses the defaults. SEO runs
// once per generation (not a re-synthesis like style), so there is no rate
// guard — only the cost cap on the body payload.
type Guard struct {
	// MaxBodyChars caps the draft characters sent to the model. <= 0 →
	// DefaultMaxBodyChars.
	MaxBodyChars int
}

func (g Guard) maxBodyChars() int {
	if g.MaxBodyChars > 0 {
		return g.MaxBodyChars
	}
	return DefaultMaxBodyChars
}

// Completer is the LiteLLM seam (*litellm.Client satisfies it): one system +
// one user message; the second return is the served model (unused here —
// clearance already records the writer's model).
type Completer interface {
	Complete(ctx context.Context, model, system, user string) (string, string, error)
}

// Optimizer enriches a Mix through a LiteLLM-compatible Completer. A nil
// Optimizer, or one with a nil Completer, is a no-op (degrade): Optimize
// returns the Mix unchanged. This is what lets cmd/main.go leave the stage
// unwired without any branch in the Runner.
type Optimizer struct {
	complete Completer
	// Model is the LiteLLM alias serving the call; empty = gateway default.
	Model string
	guard Guard
}

// New builds an Optimizer. guard's zero value applies the defaults. c may be
// nil — the resulting Optimizer is a no-op, so callers can wire it
// unconditionally and gate on config elsewhere.
func New(c Completer, model string, guard Guard) *Optimizer {
	return &Optimizer{complete: c, Model: strings.TrimSpace(model), guard: guard}
}

// seoFields is the strict JSON contract the model must return. Schema is kept
// raw so a nested JSON-LD object round-trips without a second decode; it is
// stored compacted under Mix.Meta["schema"] where checker.hasJSONLD finds it.
type seoFields struct {
	Description    string          `json:"description"`
	CanonicalURL   string          `json:"canonical_url"`
	PrimaryKeyword string          `json:"primary_keyword"`
	Tags           []string        `json:"tags"`
	Schema         json.RawMessage `json:"schema"`
}

// Optimize runs the one bounded LLM pass and merges the result into a COPY of
// m.Meta. DEGRADE-NEVER-FAIL: any failure returns the input Mix unchanged with
// a nil error, so the Runner never has to special-case SEO. (It returns a
// non-nil error only when m itself is unusable — an empty body — which the
// Runner still treats as non-fatal.)
func (o *Optimizer) Optimize(ctx context.Context, rc pipeline.RunContext, m pipeline.Mix) (pipeline.Mix, error) {
	if o == nil || o.complete == nil {
		return m, nil // disabled → no-op
	}
	body := strings.TrimSpace(m.Markdown)
	if body == "" {
		return m, nil // nothing to describe
	}

	content, _, err := o.complete.Complete(ctx, o.Model, systemPrompt(rc, m), o.userPrompt(body))
	if err != nil {
		// degrade: the writer's draft (and any P0 fallback description) stands.
		return m, nil
	}
	fields, ok := parse(content)
	if !ok {
		return m, nil
	}
	return merge(m, fields), nil
}

// merge applies the parsed fields onto a copy of m.Meta, additively: a field
// the model left empty never clears an existing value, and SEO tags union with
// any tags the writer already derived from the source (provenance tags are
// never dropped). Only description is authoritative here — this stage
// supersedes the P0 body-derived stopgap — so it overwrites.
func merge(m pipeline.Mix, f seoFields) pipeline.Mix {
	meta := make(map[string]any, len(m.Meta)+4)
	for k, v := range m.Meta {
		meta[k] = v
	}
	if d := trimToRunes(strings.TrimSpace(f.Description), MaxDescRunes); d != "" {
		meta["description"] = d
	}
	if u := strings.TrimSpace(f.CanonicalURL); u != "" {
		meta["canonical_url"] = u
	}
	if kw := strings.TrimSpace(f.PrimaryKeyword); kw != "" {
		meta["primary_keyword"] = kw
	}
	if tags := mergeTags(meta["tags"], f.Tags); len(tags) > 0 {
		meta["tags"] = tags
	}
	if s := compactJSON(f.Schema); s != "" {
		meta["schema"] = s
	}
	m.Meta = meta
	return m
}

// mergeTags unions existing meta tags (string or []string / []any) with the
// model's tags, trimmed and de-duplicated case-insensitively, existing-first
// so provenance order is preserved.
func mergeTags(existing any, add []string) []string {
	seen := map[string]bool{}
	var out []string
	push := func(t string) {
		t = strings.TrimSpace(t)
		if t == "" {
			return
		}
		if k := strings.ToLower(t); !seen[k] {
			seen[k] = true
			out = append(out, t)
		}
	}
	switch e := existing.(type) {
	case []string:
		for _, t := range e {
			push(t)
		}
	case []any:
		for _, v := range e {
			if s, ok := v.(string); ok {
				push(s)
			}
		}
	case string:
		push(e)
	}
	for _, t := range add {
		push(t)
	}
	return out
}

// trimToRunes caps s at max runes on a rune boundary (the length unit is
// runes, not bytes — ISI-7805 review note). It trims a dangling partial word
// so a mid-word cut does not ship.
func trimToRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)[:max]
	// Back off to the last space so we cut on a word boundary when possible.
	cut := string(r)
	if i := strings.LastIndexByte(cut, ' '); i > 0 {
		cut = cut[:i]
	}
	return strings.TrimSpace(cut)
}

// compactJSON compacts a raw JSON value to a single-line string, or "" when
// it is absent / null / invalid (degrade: a bad schema simply does not ship).
func compactJSON(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return ""
	}
	out := buf.String()
	if out == "{}" || out == "[]" {
		return ""
	}
	return out
}

// parse extracts the JSON object from a completion. The model is told to
// return bare JSON, but tolerate a ```json fence or surrounding prose by
// slicing the first '{' .. last '}' object. Returns ok=false (degrade) when no
// object is found or it does not decode.
func parse(content string) (seoFields, bool) {
	start := strings.IndexByte(content, '{')
	end := strings.LastIndexByte(content, '}')
	if start < 0 || end <= start {
		return seoFields{}, false
	}
	var f seoFields
	if err := json.Unmarshal([]byte(content[start:end+1]), &f); err != nil {
		return seoFields{}, false
	}
	return f, true
}

// systemPrompt is the SEO-analyst contract. It names the exact keys, the
// rune-based description budget, and the Schema.org type matched to the output
// format so the model's JSON-LD lands in a shape the checker recognizes.
func systemPrompt(rc pipeline.RunContext, m pipeline.Mix) string {
	format := strings.TrimSpace(m.OutputFormat)
	if format == "" {
		format = strings.TrimSpace(rc.OutputFormat)
	}
	var b strings.Builder
	b.WriteString("You are an SEO analyst. You receive a finished article (markdown) and return ONLY its SEO metadata as a single JSON object. No prose, no code fence, no commentary — JSON only.\n\n")
	b.WriteString("Return exactly these keys:\n")
	fmt.Fprintf(&b, "- \"description\": a compelling meta description, %d-%d characters, leading with a verb and stating the payoff. Plain text, no markdown.\n", MinDescRunes, MaxDescRunes)
	b.WriteString("- \"canonical_url\": a clean root-relative URL path for this article derived from its topic (e.g. \"/blog/opentelemetry-on-kubernetes\"). Lowercase, hyphenated, no domain.\n")
	b.WriteString("- \"primary_keyword\": the single search phrase this article should rank for. It MUST already appear in the title and the opening paragraph.\n")
	b.WriteString("- \"tags\": 3-6 lowercase topic tags as a JSON array of strings.\n")
	b.WriteString("- \"schema\": a JSON-LD object (an actual nested JSON object, not a string) for this article. ")
	b.WriteString(schemaGuidance(format))
	b.WriteString("\n\nBase every field on the article's real content. Do not invent facts, URLs, or a domain.")
	return b.String()
}

// schemaGuidance maps the output format to the Schema.org @type the checker
// expects for that format (checker.schemaExpected: blog/tutorial ship schema).
func schemaGuidance(format string) string {
	switch strings.ToLower(format) {
	case "tutorial", "howto":
		return `Use @context "https://schema.org" and @type "HowTo" with "name" and a "step" list drawn from the article's steps.`
	case "faq", "faqpage":
		return `Use @context "https://schema.org" and @type "FAQPage" with a "mainEntity" list of Question/Answer pairs from the article.`
	default:
		return `Use @context "https://schema.org" and @type "Article" with "headline" and "description" from the article.`
	}
}

// userPrompt is the article body, cost-capped to the guard budget on a rune
// boundary so the payload stays valid UTF-8.
func (o *Optimizer) userPrompt(body string) string {
	max := o.guard.maxBodyChars()
	if len(body) > max {
		cut := max
		for cut > 0 && !utf8.RuneStart(body[cut]) {
			cut--
		}
		body = body[:cut]
	}
	return "Article:\n\n" + body
}
