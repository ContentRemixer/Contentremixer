package review

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// GetStyleProfile loads the synthesized style profile for (owner, persona), or
// ErrNotFound when none has been synthesized yet. The Booth content-test store
// (§6.3) resolves the owner from the authorized persona before calling this —
// this package performs no authorization, exactly like the source queries.
func (s *Store) GetStyleProfile(ctx context.Context, owner, persona string) (StyleProfile, error) {
	row := s.pool.QueryRow(ctx, `
SELECT owner_subject, persona_name, reference_repo, traits, sample, validated, validated_at, source_count, synthesized_at, created_at, updated_at
FROM style_profiles WHERE owner_subject=$1 AND persona_name=$2`, owner, persona)
	sp, err := scanStyleProfile(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return StyleProfile{}, ErrNotFound
	}
	if err != nil {
		return StyleProfile{}, fmt.Errorf("review: get style profile (%s/%s): %w", owner, persona, err)
	}
	return sp, nil
}

// UpsertStyleProfile writes a freshly-synthesized profile for (owner, persona),
// replacing any prior row (style synthesis, D3). Re-synthesis INVALIDATES a
// prior validation: a new profile is new output the owner has not yet reviewed,
// so validated is reset to false and validated_at cleared. The owning DevRel
// re-validates via the Booth validate action, which re-sets both this flag and
// the CRD `Validated` condition. synthesized_at/created_at default to now() on
// first insert; updated_at always advances.
func (s *Store) UpsertStyleProfile(ctx context.Context, sp StyleProfile) error {
	if sp.OwnerSubject == "" || sp.PersonaName == "" {
		return fmt.Errorf("review: upsert style profile needs owner+persona (got owner=%q persona=%q)", sp.OwnerSubject, sp.PersonaName)
	}
	_, err := s.pool.Exec(ctx, `
INSERT INTO style_profiles (owner_subject, persona_name, reference_repo, traits, sample, source_count, synthesized_at, validated, validated_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6, now(), false, NULL, now())
ON CONFLICT (owner_subject, persona_name) DO UPDATE SET
    reference_repo = EXCLUDED.reference_repo,
    traits         = EXCLUDED.traits,
    sample         = EXCLUDED.sample,
    source_count   = EXCLUDED.source_count,
    synthesized_at = now(),
    validated      = false,
    validated_at   = NULL,
    updated_at     = now()`,
		sp.OwnerSubject, sp.PersonaName, sp.ReferenceRepo, sp.Traits, sp.Sample, sp.SourceCount)
	if err != nil {
		return fmt.Errorf("review: upsert style profile (%s/%s): %w", sp.OwnerSubject, sp.PersonaName, err)
	}
	return nil
}

// ValidateStyleProfile flips the owner's accept decision for (owner, persona):
// validated=true, validated_at=now. It is the Postgres half of the Booth
// validate action (the CRD `Validated` condition is the other half, written by
// the gating store in the same operation — ISI-7356 §4). Idempotent: validating
// an already-validated profile refreshes validated_at. Returns ErrNotFound when
// there is no profile to validate (synthesis must run first).
func (s *Store) ValidateStyleProfile(ctx context.Context, owner, persona string) (StyleProfile, error) {
	row := s.pool.QueryRow(ctx, `
UPDATE style_profiles SET validated=true, validated_at=now(), updated_at=now()
WHERE owner_subject=$1 AND persona_name=$2
RETURNING owner_subject, persona_name, reference_repo, traits, sample, validated, validated_at, source_count, synthesized_at, created_at, updated_at`,
		owner, persona)
	sp, err := scanStyleProfile(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return StyleProfile{}, ErrNotFound
	}
	if err != nil {
		return StyleProfile{}, fmt.Errorf("review: validate style profile (%s/%s): %w", owner, persona, err)
	}
	return sp, nil
}

func scanStyleProfile(r rowScanner) (StyleProfile, error) {
	var sp StyleProfile
	var validatedAt *time.Time
	if err := r.Scan(&sp.OwnerSubject, &sp.PersonaName, &sp.ReferenceRepo, &sp.Traits, &sp.Sample,
		&sp.Validated, &validatedAt, &sp.SourceCount, &sp.SynthesizedAt, &sp.CreatedAt, &sp.UpdatedAt); err != nil {
		return StyleProfile{}, err
	}
	sp.ValidatedAt = validatedAt
	return sp, nil
}
