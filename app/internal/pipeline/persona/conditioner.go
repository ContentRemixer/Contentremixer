// Package persona is P2-D's (ISI-7147) concrete writer-conditioning stage: it
// implements the writer's PersonaConditioner seam (design §5.1) by resolving a
// DevRelPersona's deterministic self-description and running the §4 owner-scoped
// top-k voice-exemplar retrieval (internal/rag/retrieve), then handing the
// writer a PersonaOverlay to weave into the prompt (§5.3).
//
// Direction of the dependency: this package imports the writer (for the frozen
// overlay types + the PersonaConditioner interface it satisfies) and
// internal/rag/retrieve (the owner-scoped query mechanics); neither imports
// this one, so the writer stays Phase-1-pure and testable without RAG.
//
// Everything here is BEST-EFFORT (§5.4): a missing persona, a not-Ready
// persona, a sparse corpus, or a store/embed outage all degrade to
// self-description-only (or style-only) generation — they NEVER fail the
// writer. RAG is an enhancement, not a gate.
package persona

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline"
	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline/writer"
	"github.com/ContentRemixer/Contentremixer/app/internal/rag"
	"github.com/ContentRemixer/Contentremixer/app/internal/rag/retrieve"
	"github.com/ContentRemixer/Contentremixer/app/internal/telemetry"
	"github.com/ContentRemixer/Contentremixer/app/internal/telemetry/metricconv"
)

// DefaultSeedLimit bounds the retrieval seed (Sample body + Notes) to keep the
// embedding call cheap and within the embedding model's context — the seed only
// needs enough signal to locate on-topic voice exemplars, not the whole source.
const DefaultSeedLimit = 4000

// DefaultTopK is the exemplar count used when a persona does not pin one via
// spec.retrieval.topK (mirrors the CRD +kubebuilder:default=5).
const DefaultTopK = 5

// PersonaProfile is the conditioning-relevant projection of a DevRelPersona CR
// (design §2/§5): the deterministic self-description, the retrieval tuning, and
// whether the style index is Ready to query.
type PersonaProfile struct {
	// SelfDescription is spec.selfDescription — the deterministic voice anchor.
	SelfDescription string
	// TopK / MinScore are spec.retrieval, already parsed. TopK <= 0 means use
	// DefaultTopK; MinScore is the cosine floor (0 keeps all top-k).
	TopK     int
	MinScore float64
	// Ready reports whether the persona's style index is queryable (status
	// Ready condition True / phase Ready, owned by P2-C). A not-Ready persona
	// conditions on self-description only — retrieval would read a stale/absent
	// index (§5.4).
	Ready bool
}

// PersonaProvider resolves a persona's conditioning inputs by (owner subject,
// CR name). The production implementation (CRPersonaProvider) reads the
// DevRelPersona CR and enforces the owner match; tests inject a stub.
type PersonaProvider interface {
	Lookup(ctx context.Context, ownerSubject, personaName string) (PersonaProfile, error)
}

// Conditioner implements writer.PersonaConditioner over a PersonaProvider and a
// retrieve.Retriever. It is wired onto the writer (writer.Writer.Conditioner);
// nil there is the Phase-1 style-only path.
type Conditioner struct {
	// Provider resolves the persona's self-description + retrieval tuning +
	// readiness. Required.
	Provider PersonaProvider
	// Retriever runs the owner-scoped top-k query (P2-C). Required.
	Retriever *retrieve.Retriever
	// SeedLimit caps the retrieval seed length; <= 0 uses DefaultSeedLimit.
	SeedLimit int
}

// Conditioner satisfies the writer's frozen conditioning seam.
var _ writer.PersonaConditioner = (*Conditioner)(nil)

// Condition returns the persona overlay for a run (design §5.3/§5.4).
//
//   - No owner/persona on the run → empty overlay, nil (style-only; the writer
//     only calls this when rc.PersonaName != "", but we stay defensive).
//   - Provider lookup fails → Degraded overlay + error; the writer treats a
//     Condition error as a degrade, so the run still proceeds style-only.
//   - Persona not Ready, retrieval error, or sparse corpus → self-description
//     only, Degraded=true, nil (never an error — the run must not block).
//   - Ready + ≥1 match → self-description + exemplars (the full overlay).
func (c *Conditioner) Condition(ctx context.Context, rc pipeline.RunContext, s pipeline.Sample) (writer.PersonaOverlay, error) {
	if c == nil || c.Provider == nil || c.Retriever == nil {
		// Misconfiguration, not a runtime degrade — there is no persona context
		// to attribute telemetry to; the writer still degrades to style-only.
		return writer.PersonaOverlay{Degraded: true}, fmt.Errorf("persona: conditioner not fully wired (provider, retriever)")
	}
	owner := strings.TrimSpace(rc.Owner.Subject)
	name := strings.TrimSpace(rc.PersonaName)
	tel := telemetry.Default()
	start := time.Now()
	if owner == "" || name == "" {
		// No persona attribution — Phase-1 style-only, not an error (§4.6
		// no_persona). No span: nothing conditioned, only the ladder counter.
		tel.RecordCondition(ctx, name, metricconv.ConditionOutcomeNoPersona, time.Since(start))
		return writer.PersonaOverlay{}, nil
	}

	// §6.2 persona.condition span — child of the recycle-run writer step via the
	// ctx already threaded in; the retrieval's embed/store spans nest under it.
	ctx, span := tel.StartCondition(ctx, name, owner)
	// outcome is resolved on the way out; it defaults to degraded so an
	// unexpected early path is never mis-reported as healthy.
	outcome := metricconv.ConditionOutcomeDegraded
	defer func() {
		telemetry.AnnotateCondition(span, outcome, outcome == metricconv.ConditionOutcomeDegraded)
		tel.RecordCondition(ctx, name, outcome, time.Since(start))
		telemetry.EndSpan(span, nil)
	}()
	// degrade marks the §5.4 fallback-to-style-only outcome by root cause: it
	// sets outcome=degraded, increments condition.degraded.total{reason}, and
	// adds the §5 persona.condition.degraded WARN event onto the span.
	degrade := func(reason metricconv.ConditionDegradedReason) {
		outcome = metricconv.ConditionOutcomeDegraded
		tel.RecordConditionDegraded(ctx, name, reason)
		telemetry.ConditionDegradedEvent(span, reason, runID(rc))
	}

	profile, err := c.Provider.Lookup(ctx, owner, name)
	if err != nil {
		// Could not resolve/confirm the persona — treat as not-ready (the index
		// may be stale/absent). Surface the error so the writer logs the degrade;
		// the overlay is still style-only so generation proceeds.
		degrade(metricconv.ConditionDegradedReasonPersonaNotReady)
		return writer.PersonaOverlay{Degraded: true}, fmt.Errorf("persona: resolving %q for owner %q: %w", name, owner, err)
	}

	overlay := writer.PersonaOverlay{SelfDescription: strings.TrimSpace(profile.SelfDescription)}
	if !profile.Ready {
		// Index not queryable yet: deterministic anchor only (§5.4).
		overlay.Degraded = true
		degrade(metricconv.ConditionDegradedReasonPersonaNotReady)
		return overlay, nil
	}

	topK := profile.TopK
	if topK <= 0 {
		topK = DefaultTopK
	}
	matches, err := c.Retriever.Retrieve(ctx, retrieve.Params{
		OwnerSubject: owner,
		PersonaName:  name,
		Seed:         buildSeed(s.Body, rc.Notes, c.seedLimit()),
		TopK:         topK,
		MinScore:     profile.MinScore,
	})
	if err != nil {
		// Store/embedding unavailable: self-description only, never fail (§5.4).
		// The retrieve sentinel attributes the cause to the right reason.
		overlay.Degraded = true
		if errors.Is(err, retrieve.ErrEmbed) {
			degrade(metricconv.ConditionDegradedReasonEmbedFailed)
		} else {
			degrade(metricconv.ConditionDegradedReasonStoreUnavailable)
		}
		return overlay, nil
	}

	overlay.Exemplars = toExemplars(matches)
	if len(overlay.Exemplars) == 0 {
		// Sparse corpus (seed matched nothing above MinScore, or all matches had
		// empty text): self-description only. This is CORRECT behaviour (§5.4),
		// NOT a degrade — outcome self_description_only, no degraded.total, and
		// excluded from the conditioning-availability error budget (§10). Degraded
		// still marks the style-only prompt fallback for the writer (unchanged).
		overlay.Degraded = true
		outcome = metricconv.ConditionOutcomeSelfDescriptionOnly
		return overlay, nil
	}

	// Full conditioning: self-description + ≥1 exemplar (§4.6 full).
	outcome = metricconv.ConditionOutcomeFull
	return overlay, nil
}

// runID is the recycle-run identifier stamped on the §5
// persona.condition.degraded event (repo + recycle issue number). It is a
// span/log field only (unbounded, never a metric label — §3).
func runID(rc pipeline.RunContext) string {
	if strings.TrimSpace(rc.Repo) != "" {
		return fmt.Sprintf("%s#%d", rc.Repo, rc.IssueNumber)
	}
	return fmt.Sprintf("#%d", rc.IssueNumber)
}

// seedLimit resolves the configured seed cap.
func (c *Conditioner) seedLimit() int {
	if c.SeedLimit > 0 {
		return c.SeedLimit
	}
	return DefaultSeedLimit
}

// buildSeed composes the retrieval seed from the source body and the run notes,
// truncated to limit runes (design §5.3). The body carries the topic; the notes
// carry the angle/audience — both shape which voice exemplars are most relevant.
func buildSeed(body, notes string, limit int) string {
	var b strings.Builder
	if body = strings.TrimSpace(body); body != "" {
		b.WriteString(body)
	}
	if notes = strings.TrimSpace(notes); notes != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(notes)
	}
	return truncateRunes(b.String(), limit)
}

// truncateRunes caps s to at most limit runes (not bytes) so multibyte text is
// never split mid-rune.
func truncateRunes(s string, limit int) string {
	if limit <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit])
}

// toExemplars projects retrieval matches to the writer's tone-reference
// exemplars, dropping any with empty text.
func toExemplars(matches []rag.Match) []writer.Exemplar {
	out := make([]writer.Exemplar, 0, len(matches))
	for _, m := range matches {
		if strings.TrimSpace(m.Text) == "" {
			continue
		}
		out = append(out, writer.Exemplar{
			Text:      m.Text,
			SourceURL: m.SourceURL,
			Score:     m.Score,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
