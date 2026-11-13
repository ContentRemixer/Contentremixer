// Persona-management API client (ISI-7362 D7). Thin typed wrappers over the
// Booth endpoints the persona surfaces hit: the owner-scoped persona CRUD
// (D5/ISI-7360), the style-profile read + content-test + validate (D4/
// ISI-7359 + the D7 read route), the promote suggestion (D9/ISI-7369), and the
// scrape-job enqueue used by the re-ingest flow. Authz is enforced server-side
// (§6.3); a 401 propagates as ApiError and the AuthProvider treats it as
// logged-out. All-but-none of these routes are unmounted on a Booth built
// without their backing stores — the UI degrades on 404 rather than assuming.

import { api, ApiError } from "../../app/api";
import type { ScrapeJob } from "../scraper/types";
import type {
  ContentTestResult,
  CreateScrapeJobRequest,
  DevRelPersona,
  PromoteResult,
  StyleProfile,
} from "./types";

const BASE = "/v1/devrelpersonas";

// GET /v1/devrelpersonas — the caller's personas (an Admin sees every one;
// a DevRel only their own — the store scopes by §6.3).
export function listPersonas(): Promise<DevRelPersona[]> {
  return api.get<DevRelPersona[]>(BASE);
}

// GET /v1/devrelpersonas/{name}.
export function getPersona(name: string): Promise<DevRelPersona> {
  return api.get<DevRelPersona>(`${BASE}/${encodeURIComponent(name)}`);
}

// PUT /v1/devrelpersonas/{name} — full-object semantics (server replaces the
// stored CR), so the caller must send the complete spec with the owner
// round-tripped: an omitted/edited owner fails the §6.3 gate.
export function updatePersona(name: string, persona: DevRelPersona): Promise<DevRelPersona> {
  return api.put<DevRelPersona>(`${BASE}/${encodeURIComponent(name)}`, persona);
}

// GET /v1/devrelpersonas/{name}/style-profile — the synthesized profile
// (traits + sample, D3) for display. 404 means "no profile yet" (O7
// pre-synthesis), which callers render as an empty state, not an error.
export function getStyleProfile(name: string): Promise<StyleProfile> {
  return api.get<StyleProfile>(`${BASE}/${encodeURIComponent(name)}/style-profile`);
}

// POST /v1/content-test — one bounded LLM generation conditioned on the
// persona's profile + spec. Rate-limited server-side (429).
export function runContentTest(personaName: string, topic: string): Promise<ContentTestResult> {
  return api.post<ContentTestResult>("/v1/content-test", { personaName, topic });
}

// POST /v1/devrelpersonas/{name}/validate — the owner's accept decision;
// writes Postgres first, then mirrors to the CRD Validated condition.
// Returns the now-validated profile.
export function validatePersona(name: string): Promise<StyleProfile> {
  return api.post<StyleProfile>(`${BASE}/${encodeURIComponent(name)}/validate`);
}

// POST /v1/promote — a suggested LinkedIn post for a piece of VALIDATED
// content (D9). Optional surface: 404 when the Booth has no promote store.
export function suggestPromotion(req: {
  personaName: string;
  content: string;
  title?: string;
  url?: string;
}): Promise<PromoteResult> {
  return api.post<PromoteResult>("/v1/promote", req);
}

// POST /v1/scrape-jobs — enqueue ingestion for one reference source. The job
// owner is stamped from the session, so this only works for the caller's own
// persona. Poll via getScrapeJob; the scraped source lands in scraper-review.
export function createScrapeJob(req: CreateScrapeJobRequest): Promise<ScrapeJob> {
  return api.post<ScrapeJob>("/v1/scrape-jobs", req);
}

// GET /v1/scrape-jobs/{id}.
export function getScrapeJob(id: string): Promise<ScrapeJob> {
  return api.get<ScrapeJob>(`/v1/scrape-jobs/${encodeURIComponent(id)}`);
}

// isNoProfileYet classifies the 404 the style-profile read returns when no
// profile has been synthesized yet (the UI's "awaiting synthesis" state).
export function isNoProfileYet(err: unknown): boolean {
  return err instanceof ApiError && err.status === 404;
}
