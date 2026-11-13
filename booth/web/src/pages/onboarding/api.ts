// Typed Booth API wrappers for the onboarding wizard (ISI-7361 D6). Authz is
// enforced server-side (§6.3); a 401 propagates as ApiError and the AuthProvider
// treats it as logged-out. Every call is owner-scoped to the caller.

import { api, ApiError } from "../../app/api";
import type {
  DevRelPersona,
  DevRelPersonaSpec,
  CreateScrapeJobRequest,
  ScrapeJob,
  ScrapedSource,
  StyleProfile,
  ContentTestResult,
  SourceStatus,
} from "./types";

// POST /v1/devrelpersonas — create the caller's persona. Owner is omitted so the
// server stamps the caller's subject (ISI-7360 D5). Returns the created CR.
export function createPersona(name: string, spec: DevRelPersonaSpec): Promise<DevRelPersona> {
  return api.post<DevRelPersona>("/v1/devrelpersonas", {
    metadata: { name },
    spec,
  });
}

// GET /v1/devrelpersonas/{name} — read one (used to detect an existing persona so
// a re-entered wizard does not try to re-create it).
export function getPersona(name: string): Promise<DevRelPersona> {
  return api.get<DevRelPersona>(`/v1/devrelpersonas/${encodeURIComponent(name)}`);
}

// upsertPersona creates the persona on first run, or updates its spec if the
// wizard is re-entered (the persona already exists). On update we preserve the
// existing owner so the webhook's non-empty-owner invariant holds — the create
// path lets the server stamp it. 404 on the probe means "does not exist → create".
export async function upsertPersona(name: string, spec: DevRelPersonaSpec): Promise<DevRelPersona> {
  let existing: DevRelPersona | null = null;
  try {
    existing = await getPersona(name);
  } catch (err) {
    if (!(err instanceof ApiError) || err.status !== 404) throw err;
  }
  if (!existing) return createPersona(name, spec);
  return api.put<DevRelPersona>(`/v1/devrelpersonas/${encodeURIComponent(name)}`, {
    metadata: { name },
    spec: { ...spec, owner: existing.spec?.owner },
  });
}

// POST /v1/scrape-jobs — enqueue a scrape for one source. Owner is stamped from
// the session server-side.
export function createScrapeJob(req: CreateScrapeJobRequest): Promise<ScrapeJob> {
  return api.post<ScrapeJob>("/v1/scrape-jobs", req);
}

// GET /v1/scrape-jobs/{id} — poll one job's progress.
export function getScrapeJob(id: string): Promise<ScrapeJob> {
  return api.get<ScrapeJob>(`/v1/scrape-jobs/${encodeURIComponent(id)}`);
}

// GET /v1/scraped-sources?status= — list the caller's scraped sources (optionally
// filtered). The wizard lists `pending` to accept them into the corpus.
export function listScrapedSources(status?: SourceStatus): Promise<ScrapedSource[]> {
  const qs = status ? `?status=${encodeURIComponent(status)}` : "";
  return api.get<ScrapedSource[]>(`/v1/scraped-sources${qs}`);
}

// POST /v1/scraped-sources/{id}/accept — ingest a source's chunks into pgvector,
// making it an accepted exemplar the synthesizer can draw on.
export function acceptSource(id: string): Promise<ScrapedSource> {
  return api.post<ScrapedSource>(`/v1/scraped-sources/${encodeURIComponent(id)}/accept`);
}

// POST /v1/devrelpersonas/{name}/style-profile/synthesize — run D3 synthesis and
// return the freshly stored (unvalidated) profile.
export function synthesizeStyleProfile(name: string): Promise<StyleProfile> {
  return api.post<StyleProfile>(
    `/v1/devrelpersonas/${encodeURIComponent(name)}/style-profile/synthesize`,
  );
}

// GET /v1/devrelpersonas/{name}/style-profile — read the profile for review
// WITHOUT validating it (404 before the first synthesis).
export function getStyleProfile(name: string): Promise<StyleProfile> {
  return api.get<StyleProfile>(
    `/v1/devrelpersonas/${encodeURIComponent(name)}/style-profile`,
  );
}

// POST /v1/content-test — generate a sample on a topic in the persona's voice.
export function runContentTest(personaName: string, topic: string): Promise<ContentTestResult> {
  return api.post<ContentTestResult>("/v1/content-test", { personaName, topic });
}

// POST /v1/devrelpersonas/{name}/validate — the owner accepts the profile. This
// flips Validated (Postgres + CRD condition) and completes onboarding.
export function validatePersona(name: string): Promise<StyleProfile> {
  return api.post<StyleProfile>(
    `/v1/devrelpersonas/${encodeURIComponent(name)}/validate`,
  );
}
