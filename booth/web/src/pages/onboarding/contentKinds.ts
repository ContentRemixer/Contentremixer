// Pure helpers for the O2 multi-select (ISI-7424 / ISI-7428). The DevRel picks
// one or more content kinds; the first entry is the derived *primary* medium.
// Order matters locally (it encodes the primary), even though the CRD stores the
// set unordered (listType=set) — which is exactly why the wizard sends the
// derived primaryContentKind explicitly rather than relying on round-tripped
// order. Keeping the list logic here keeps the step component declarative and
// the behavior exercised by the onboarding E2E suite against a stable surface.

import type { PrimaryContentKind } from "./types";

// toggleKind adds the kind when absent, removes it when present. Removing the
// current primary (index 0) promotes the next remaining kind to primary, which
// matches "primary = first selected".
export function toggleKind(
  selected: PrimaryContentKind[],
  kind: PrimaryContentKind,
): PrimaryContentKind[] {
  return selected.includes(kind)
    ? selected.filter((k) => k !== kind)
    : [...selected, kind];
}

// makePrimary re-orders the set so `kind` is first (the primary). A no-op if the
// kind isn't selected — the UI only offers "Set primary" on selected cards.
export function makePrimary(
  selected: PrimaryContentKind[],
  kind: PrimaryContentKind,
): PrimaryContentKind[] {
  if (!selected.includes(kind)) return selected;
  return [kind, ...selected.filter((k) => k !== kind)];
}

// primaryKind is the derived dominant medium: the first selected kind, or null
// when nothing is selected (Continue is gated on >= 1 selected).
export function primaryKind(selected: PrimaryContentKind[]): PrimaryContentKind | null {
  return selected.length > 0 ? selected[0] : null;
}
