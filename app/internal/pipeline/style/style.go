// Package style is the D3 style-recognition synthesis stage (ISI-7358,
// ISI-7350 plan §5): after a persona's sources are accepted into the embedding
// corpus, one bounded LLM call over the owner-scoped exemplars produces the
// DevRel-reviewable style profile — named style traits (tone, vocabulary tier,
// sentence rhythm, structure) plus a short sample paragraph — which is stored
// on the StyleProfile surface the D1 contract froze (Postgres row,
// ISI-7356 §4; the DevRel reads and validates it in O7).
//
// Direction of the dependency, same discipline as the rest of the pipeline:
// this package imports only the LiteLLM client seam (Completer, satisfied by
// *litellm.Client) — no store, no DB, no CR. The app/review factory
// (NewSynthesizer) is the glue that fetches owner-scoped exemplars, applies the
// rate/cost guard against the last profile, calls Synthesize, and persists the
// result; Booth (§6.3-gated routes, D5/D7) reaches it only through that public
// contract, exactly like the accept-time Corpus.
//
// Everything here is FAIL-CLOSED, unlike the best-effort writer-conditioning
// (pipeline/persona): a falsified or missing profile must never reach the
// DevRel's review screen. No exemplars → ErrNoExemplars; output that does not
// follow the section contract → ErrMalformed; both surface as actionable
// errors instead of a plausible-looking fake profile.
package style

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// namedDimensions are the four trait dimensions the profile MUST name
// (issue ISI-7358): the synthesizer's system prompt and the parse contract are
// both defined in terms of this list.
const namedDimensions = "tone, vocabulary tier, sentence rhythm, structure"

// traitsHeader / sampleHeader are the EXACT markdown section markers the model
// must emit. They are the parse contract: Prompt fully specifies them, Parse
// accepts case-insensitively.
const (
	traitsHeader = "## Style traits"
	sampleHeader = "## Sample paragraph"
)

// Guard bounds one synthesis. The zero value uses the defaults — a simple
// rate/cost guard per ISI-7358: rate = minimum spacing between re-syntheses of
// the same persona, cost = hard caps on the exemplar payload sent to the
// model, so the guard is owner-independent and needs NO external state beyond
// the profile's own synthesized_at timestamp.
type Guard struct {
	// MinInterval is how long after a synthesis of the same (owner, persona)
	// a re-synthesis is refused (rate guard; the profile rarely needs to be
	// recomputed and each run is a paid LLM call). <= 0 → DefaultMinInterval.
	MinInterval time.Duration
	// MaxExemplars caps the number of exemplar chunks in the prompt. <= 0 →
	// DefaultMaxExemplars.
	MaxExemplars int
	// MaxChars caps the total exemplar characters in the prompt. <= 0 →
	// DefaultMaxChars.
	MaxChars int
}

// Guard defaults (D3 plan: "add a simple rate/cost guard"). MaxExemplars
// mirrors review.DefaultExemplarLimit — the store fetch and the prompt cap are
// the same budget by construction; the store limit remains the contract, this
// one the synthesis-side mirror.
const (
	// DefaultMinInterval — at most one synthesis per persona per hour.
	DefaultMinInterval = time.Hour
	// DefaultMaxExemplars — mirrors review.DefaultExemplarLimit.
	DefaultMaxExemplars = 48
	// DefaultMaxChars keeps the exemplar payload ≈6k tokens — comfortably
	// inside a small context window even with a long system prompt.
	DefaultMaxChars = 24000
)

// ErrRateLimited reports a re-synthesis inside the guard interval. RetryAfter
// carries how long the caller must wait before trying again.
type ErrRateLimited struct{ RetryAfter time.Duration }

func (e ErrRateLimited) Error() string {
	return fmt.Sprintf("style: synthesis rate-limited; retry after %s", e.RetryAfter.Round(time.Second))
}

// ErrNoExemplars reports an empty corpus: there is nothing to recognize a
// style from. Synthesis is refused rather than hallucinated (fail-closed; the
// persona has no accepted sources yet).
var ErrNoExemplars = errors.New("style: no accepted exemplars to synthesize from")

// ErrMalformed reports a completion that does not follow the two-section
// contract. Partial never ships: callers should re-run, not repair.
type ErrMalformed struct{ Detail string }

func (e ErrMalformed) Error() string { return "style: malformed synthesis output: " + e.Detail }

// TooSoon reports whether a synthesis at 'now' is inside the guard interval
// for a profile last synthesized at 'last' (zero = never). It returns the
// remaining wait and whether the caller must hold off.
func (g Guard) TooSoon(now, last time.Time) (time.Duration, bool) {
	if last.IsZero() {
		return 0, false
	}
	wait := g.minInterval() - now.Sub(last)
	if wait <= 0 {
		return 0, false
	}
	return wait, true
}

func (g Guard) minInterval() time.Duration {
	if g.MinInterval > 0 {
		return g.MinInterval
	}
	return DefaultMinInterval
}

// Exemplar is one voice exemplar chunk from the persona's accepted corpus.
// SourceURL/SourceKind are carried so the named traits can cite evidence
// without the synthesizer re-deriving provenance.
type Exemplar struct {
	SourceURL  string
	SourceKind string
	Text       string
}

// Request scopes one synthesis. Owner and persona are echoed for error
// messages but used for no lookup here — the caller (review.NewSynthesizer)
// owns the store side; this package treats them as opaque.
type Request struct {
	OwnerSubject string
	PersonaName  string
	// SelfDescription is the persona's own bio (DevRelPersona
	// spec.selfDescription) — context the traits should honor, never the
	// whole basis of them. Optional.
	SelfDescription string
	// PrimaryContentKind orients the sample paragraph to the persona's
	// dominant output medium (blog|video|talk|tutorial). Empty =
	// medium-agnostic.
	PrimaryContentKind string
	// Exemplars are the accepted owner-scoped chunks; the guard caps what
	// actually reaches the prompt.
	Exemplars []Exemplar
}

// Result is the parsed profile, ready to store.
type Result struct {
	Traits string
	Sample string
}

// Completer is the LiteLLM seam (*litellm.Client satisfies it): one system +
// one user message; the second return is the served model.
type Completer interface {
	Complete(ctx context.Context, model, system, user string) (string, string, error)
}

// Synthesizer produces style profiles through a LiteLLM-compatible Completer.
type Synthesizer struct {
	complete Completer
	// Model is the LiteLLM alias serving the call; empty = gateway default.
	Model string
	guard Guard
}

// New builds a Synthesizer. guard's zero value applies the defaults.
func New(c Completer, model string, guard Guard) *Synthesizer {
	return &Synthesizer{complete: c, Model: model, guard: guard}
}

// Synthesize runs the one bounded LLM call and parses the profile.
// Request.Exemplars are truncated to the guard caps BEFORE the call (cost
// guard); an empty exemplar set is refused outright. The completion must carry
// both parse-contract sections, else ErrMalformed — a partial profile is
// never returned.
func (s *Synthesizer) Synthesize(ctx context.Context, req Request) (Result, error) {
	if s == nil || s.complete == nil {
		return Result{}, fmt.Errorf("style: synthesizer not fully initialised")
	}
	if strings.TrimSpace(req.OwnerSubject) == "" || strings.TrimSpace(req.PersonaName) == "" {
		return Result{}, fmt.Errorf("style: synthesis needs owner+persona")
	}
	exemplars := s.truncate(req.Exemplars)
	if len(exemplars) == 0 {
		return Result{}, ErrNoExemplars
	}

	content, _, err := s.complete.Complete(ctx, s.Model, prompt(req), exemplarBlock(exemplars))
	if err != nil {
		return Result{}, fmt.Errorf("style: style-recognition call failed: %w", err)
	}
	traits, sample, err := Parse(content)
	if err != nil {
		return Result{}, err
	}
	return Result{Traits: traits, Sample: sample}, nil
}

// truncate applies the guard's exemplar-count and character caps, in input
// order, so a deterministic caller yields a deterministic prompt.
func (s *Synthesizer) truncate(ex []Exemplar) []Exemplar {
	maxN := s.guard.MaxExemplars
	if maxN <= 0 {
		maxN = DefaultMaxExemplars
	}
	maxChars := s.guard.MaxChars
	if maxChars <= 0 {
		maxChars = DefaultMaxChars
	}
	if len(ex) > maxN {
		ex = ex[:maxN]
	}
	out := make([]Exemplar, 0, len(ex))
	budget := maxChars
	for _, e := range ex {
		text := e.Text
		if len(text) > budget {
			// Take the head of the final overflowing chunk rather than
			// dropping it entirely: a partial chunk still carries voice
			// signal. Cut at a rune boundary so the prompt stays valid
			// UTF-8 regardless of where the byte budget lands.
			cut := budget
			for cut > 0 && !utf8.RuneStart(text[cut]) {
				cut--
			}
			text = text[:cut]
		}
		budget -= len(text)
		out = append(out, Exemplar{SourceURL: e.SourceURL, SourceKind: e.SourceKind, Text: text})
		if budget <= 0 {
			break
		}
	}
	return out
}

// prompt builds the system message: the profile contract the model must follow
// and the four named dimensions the traits must cover. The user message is the
// exemplars themselves (exemplarBlock).
func prompt(req Request) string {
	sampleGuidance := "Pick the medium the excerpts suggest and write the sample in it."
	switch strings.ToLower(strings.TrimSpace(req.PrimaryContentKind)) {
	case "blog":
		sampleGuidance = "Write the sample as one paragraph from a technical blog post, in this voice."
	case "video":
		sampleGuidance = "Write the sample as one paragraph of a video description or intro narration, in this voice."
	case "talk":
		sampleGuidance = "Write the sample as one paragraph of a conference-talk abstract, in this voice."
	case "tutorial":
		sampleGuidance = "Write the sample as one paragraph of a hands-on tutorial introduction, in this voice."
	}

	var b strings.Builder
	b.WriteString("You are a writing-style analyst. You receive excerpts from texts written by ONE author and produce the style profile that the author will read, adjust, and accept or reject.\n\n")
	b.WriteString("Output EXACTLY two markdown sections, in this order, with no preamble and no closing remarks:\n\n")
	b.WriteString(traitsHeader + "\n")
	b.WriteString("4-8 named trait dimensions about how this author writes. The profile covers at minimum: ")
	b.WriteString(namedDimensions)
	b.WriteString(". Each trait: a bold short name, then one or two sentences grounded in the excerpts (quote at most a short phrase per trait). Write it to be read by the author themselves.\n\n")
	b.WriteString(sampleHeader + "\n")
	b.WriteString("One short paragraph (3-6 sentences) written IN this author's voice, on a neutral technical topic (observability, containers, PostgreSQL, developer workflows). Do not imitate a specific excerpt's subject — imitate the voice. ")
	b.WriteString(sampleGuidance)
	if strings.TrimSpace(req.SelfDescription) != "" {
		b.WriteString("\n\nThe author describes themselves as:\n")
		b.WriteString(req.SelfDescription)
		b.WriteString("\nHonor this as context, but where it disagrees with the excerpts, the excerpts win.")
	}
	return b.String()
}

// exemplarBlock renders the user message from the exemplars, in order and
// cited by source, so any named trait stays traceable to its evidence.
func exemplarBlock(ex []Exemplar) string {
	var b strings.Builder
	b.WriteString("Author excerpts:\n")
	for i, e := range ex {
		fmt.Fprintf(&b, "\n--- excerpt %d (%s, %s) ---\n", i+1, e.SourceKind, e.SourceURL)
		b.WriteString(strings.TrimSpace(e.Text))
		b.WriteString("\n")
	}
	return b.String()
}

// Parse extracts the traits and the sample from a completion. Headers match
// case-insensitively and tolerate surrounding blanks; a preamble before the
// traits section is ignored (the model was told not to write one), but BOTH
// sections must be present, non-empty, and ordered — a partial or reordered
// profile is malformed rather than repaired. Section bodies are trimmed
// only; text inside a section is left for the author to read.
func Parse(content string) (traits, sample string, err error) {
	tBody := indexSection(content, traitsHeader)
	sBody := indexSection(content, sampleHeader)
	if tBody < 0 || sBody < 0 {
		return "", "", ErrMalformed{Detail: fmt.Sprintf("missing section header (need %q and %q)", traitsHeader, sampleHeader)}
	}
	// The traits section spans from its header's end to the START of the
	// sample header; indexSection returns body offsets (just past the
	// header), so walk back over the sample header to slice correctly.
	sStart := sBody - len(sampleHeader)
	if sStart < tBody {
		return "", "", ErrMalformed{Detail: "sample paragraph section precedes style traits"}
	}
	traits = strings.TrimSpace(content[tBody:sStart])
	sample = strings.TrimSpace(content[sBody:])
	if traits == "" || sample == "" {
		return "", "", ErrMalformed{Detail: "empty section"}
	}
	return traits, sample, nil
}

// indexSection finds a header's section BODY start (just after the header
// marker) when the header starts a line, else -1.
func indexSection(content, header string) int {
	rest := content
	base := 0
	for {
		i := indexOfFold(rest, header)
		if i < 0 {
			return -1
		}
		if i == 0 || rest[i-1] == '\n' || rest[i-1] == '\r' {
			return base + i + len(header)
		}
		// Match inside a line (e.g. quoted) — keep scanning.
		rest = rest[i+len(header):]
		base += i + len(header)
	}
}

// indexOfFold is a case-insensitive strings.Index, kept local to avoid an
// x/* dependency for one call.
func indexOfFold(s, substr string) int {
	if substr == "" || len(substr) > len(s) {
		return -1
	}
	return strings.Index(strings.ToLower(s), strings.ToLower(substr))
}
