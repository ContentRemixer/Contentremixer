// Small pure helpers over the DevRelPersona CR the P1–P4 surfaces share:
// condition lookups and the badge tones the plan's mock inventory prescribes
// (P1 renders Ready/Validated badges, ingestion phase, exemplar count).
import type { BadgeTone } from "../../ui";
import type { DevRelPersona, PersonaCondition } from "./types";

// condition returns the named status condition (CRD conditions are a type-keyed
// list) — undefined when the controller has not set it yet.
export function condition(p: DevRelPersona, type: string): PersonaCondition | undefined {
  return (p.status?.conditions ?? []).find((c) => c.type === type);
}

export function isReady(p: DevRelPersona): boolean {
  return condition(p, "Ready")?.status === "True";
}

export function isValidated(p: DevRelPersona): boolean {
  return condition(p, "Validated")?.status === "True";
}

const PHASE_TONES: Record<string, BadgeTone> = {
  Ready: "success",
  Error: "warning",
  Scraping: "info",
  Embedding: "info",
  Pending: "info",
  Idle: "neutral",
};

export function phase(p: DevRelPersona): string {
  return p.status?.phase || "Idle";
}

export function phaseTone(p: DevRelPersona): BadgeTone {
  return PHASE_TONES[phase(p)] ?? "neutral";
}

export function exemplarCount(p: DevRelPersona): number {
  return p.status?.exemplarCount ?? 0;
}
