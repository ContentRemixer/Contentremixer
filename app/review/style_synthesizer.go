package review

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ContentRemixer/Contentremixer/app/internal/litellm"
	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline/style"
)

// Synthesizer is the store-side glue for the D3 style-recognition synthesis
// (ISI-7358, plan §5): it fetches the persona's accepted owner-scoped
// exemplars, applies the rate/cost guard against the last stored profile, runs
// the ONE bounded LLM call through the LiteLLM gateway, and upserts the
// parsed profile onto the StyleProfile surface the D1 contract froze.
//
// It lives here (app/review, public) for the same reason NewCorpus does: the
// synthesis stage is app/internal, and Booth — the caller that will gate this
// behind §6.3 and expose the O6→O7 "synthesize → review" step — must reach it
// through a public contract without importing app/internal. This type does NO
// authorization: owner-scoping is stamped by the caller from the session, and
// the Booth gating store wraps every route (§6.3), exactly like the source
// review actions.
//
// The DevRel-facing read surface (O7) is GetStyleProfile/UpsertStyleProfile
// (styleprofile.go); validating is the Booth validate action (D4, ISI-7359,
// also writes the CRD Validated condition).
type Synthesizer struct {
	db       *Store
	synth    *style.Synthesizer
	guard    style.Guard
	fetchCap int
}

// SynthesizeRequest scopes one synthesis run. The Booth route stamps
// OwnerSubject from the verified session and resolves PersonaName,
// SelfDescription (spec.selfDescription) and PrimaryContentKind
// (spec.primaryContentKind) from the persona the caller is authorized for.
type SynthesizeRequest struct {
	OwnerSubject string
	PersonaName  string
	// ReferenceRepo, when non-empty, is recorded on the profile for
	// provenance (the reviewed sources' repo).
	ReferenceRepo string
	// SelfDescription is the persona bio (spec.selfDescription). Optional.
	SelfDescription string
	// PrimaryContentKind orients the sample (spec.primaryContentKind).
	// Optional; empty = medium-agnostic.
	PrimaryContentKind string
}

// NewSynthesizer builds the live synthesizer over an already-open review
// Store (the Booth binary owns that pool), a LiteLLM gateway origin + key, a
// model alias (empty = gateway default), and the synthesis guard (zero value =
// defaults). The Store must be the same instance the Booth review store gates,
// so the profile lands next to the corpus it was synthesized from.
func NewSynthesizer(db *Store, litellmBaseURL, apiKey, model string, guard style.Guard) *Synthesizer {
	client := &litellm.Client{BaseURL: litellmBaseURL, APIKey: apiKey}
	return &Synthesizer{
		db:       db,
		synth:    style.New(client, model, guard),
		guard:    guard,
		fetchCap: guardFetchCap(guard),
	}
}

func guardFetchCap(g style.Guard) int {
	if g.MaxExemplars > 0 {
		return g.MaxExemplars
	}
	return style.DefaultMaxExemplars
}

// Synthesize runs one synthesis for the (owner, persona) and returns the
// STORED profile (read back after the upsert, so timestamps are the
// database's, not the caller's).
//
// Fail-closed semantics (see pipeline/style): no accepted exemplars →
// style.ErrNoExemplars (nothing to recognize a style from); a re-synthesis
// sooner than guard.MinInterval after the last one → style.ErrRateLimited;
// a completion that breaks the section contract → style.ErrMalformed. The
// Booth route maps those to 409, 429, and 502 respectively.
//
// On success the upsert RESETS validated (styleprofile.go: a new profile is
// new output the owner has not reviewed), which mirrors the CRD Validated
// condition reason the operator recomputes (ProfileSynthesized, ISI-7356 §4).
// Synthesis itself deliberately does NOT touch the CRD: the condition
// reconciles from this row.
func (s *Synthesizer) Synthesize(ctx context.Context, req SynthesizeRequest) (StyleProfile, error) {
	if s == nil || s.db == nil || s.synth == nil {
		return StyleProfile{}, errors.New("review: synthesizer not fully initialised")
	}
	if req.OwnerSubject == "" || req.PersonaName == "" {
		return StyleProfile{}, fmt.Errorf("review: synthesis needs owner+persona (got owner=%q persona=%q)", req.OwnerSubject, req.PersonaName)
	}

	// Rate guard: the profile's own synthesized_at is the guard state — no
	// side table, no lock; the row IS the last-run record.
	if last, err := s.db.GetStyleProfile(ctx, req.OwnerSubject, req.PersonaName); err == nil {
		if wait, tooSoon := s.guard.TooSoon(time.Now(), last.SynthesizedAt); tooSoon {
			return StyleProfile{}, style.ErrRateLimited{RetryAfter: wait}
		}
	} else if !errors.Is(err, ErrNotFound) {
		return StyleProfile{}, fmt.Errorf("review: load prior profile: %w", err)
	}

	// Cost guard part 1: bound the fetch, in SQL, in the owner+persona scope.
	accepted, err := s.db.ListAcceptedExemplars(ctx, req.OwnerSubject, req.PersonaName, s.fetchCap)
	if err != nil {
		return StyleProfile{}, err
	}
	exemplars := make([]style.Exemplar, len(accepted))
	for i, e := range accepted {
		exemplars[i] = style.Exemplar{SourceURL: e.SourceURL, SourceKind: e.SourceKind, Text: e.Text}
	}

	res, err := s.synth.Synthesize(ctx, style.Request{
		OwnerSubject:       req.OwnerSubject,
		PersonaName:        req.PersonaName,
		SelfDescription:    req.SelfDescription,
		PrimaryContentKind: req.PrimaryContentKind,
		Exemplars:          exemplars,
	})
	if err != nil {
		return StyleProfile{}, err
	}

	sourceCount, err := s.db.CountAcceptedSources(ctx, req.OwnerSubject, req.PersonaName)
	if err != nil {
		return StyleProfile{}, err
	}

	// Cost guard part 2: pipeline/style's truncate already bounded the LLM
	// call itself; the stored row records what was drawn on for the
	// reviewer's confidence read (D1 §4: SourceCount).
	sp := StyleProfile{
		OwnerSubject:  req.OwnerSubject,
		PersonaName:   req.PersonaName,
		ReferenceRepo: req.ReferenceRepo,
		Traits:        res.Traits,
		Sample:        res.Sample,
		SourceCount:   sourceCount,
	}
	if err := s.db.UpsertStyleProfile(ctx, sp); err != nil {
		return StyleProfile{}, err
	}
	stored, err := s.db.GetStyleProfile(ctx, req.OwnerSubject, req.PersonaName)
	if err != nil {
		return StyleProfile{}, err
	}
	return stored, nil
}

// NewDefaultSynthesizer builds a Synthesizer with the rate/cost guard expressed
// as primitives (a duration + two counts) instead of a style.Guard. It exists
// so a caller OUTSIDE this module — Booth, which cannot import
// app/internal/pipeline/style and therefore cannot name a style.Guard — can
// still wire the D3 synthesizer behind its §6.3 route (ISI-7361 D6). A zero
// value for any parameter selects the package default for that knob.
func NewDefaultSynthesizer(db *Store, litellmBaseURL, apiKey, model string, minInterval time.Duration, maxExemplars, maxChars int) *Synthesizer {
	return NewSynthesizer(db, litellmBaseURL, apiKey, model, style.Guard{
		MinInterval:  minInterval,
		MaxExemplars: maxExemplars,
		MaxChars:     maxChars,
	})
}

// The Synthesize error contract (style.ErrNoExemplars / ErrRateLimited /
// ErrMalformed) is defined in app/internal/pipeline/style, which the Booth
// module cannot import. These public classifiers let the Booth route map those
// outcomes to HTTP status codes (409 / 429 / 502) without crossing the module
// boundary — the same seam rationale as NewSynthesizer itself.

// IsNoExemplars reports whether a synthesis failed because the persona has no
// accepted exemplars yet (→ 409: nothing to recognize a style from).
func IsNoExemplars(err error) bool { return errors.Is(err, style.ErrNoExemplars) }

// SynthesisRateLimited reports whether a re-synthesis was refused by the guard
// interval, and if so how long until it may run again (→ 429).
func SynthesisRateLimited(err error) (time.Duration, bool) {
	var rl style.ErrRateLimited
	if errors.As(err, &rl) {
		return rl.RetryAfter, true
	}
	return 0, false
}

// IsMalformed reports whether synthesis produced a completion that broke the
// section contract (→ 502: an upstream/model fault, not a caller error).
func IsMalformed(err error) bool {
	var m style.ErrMalformed
	return errors.As(err, &m)
}
