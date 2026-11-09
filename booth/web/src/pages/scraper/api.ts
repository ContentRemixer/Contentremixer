// Scraper-review API client (ISI-7211, W3). Thin typed wrappers over the Booth
// endpoints mounted by booth/internal/server/scrapejobs.go. Authz is enforced
// server-side (§6.3); a 401 propagates as ApiError and the AuthProvider treats
// it as logged-out.

import { api } from "../../app/api";
import type {
  ScrapedSource,
  SourceChunks,
  SourceStatus,
  RescrapeResult,
} from "./types";

// GET /v1/scraped-sources[?status=]. Omitting status lists every source the
// caller owns, across states.
export function listScrapedSources(status?: SourceStatus): Promise<ScrapedSource[]> {
  const qs = status ? `?status=${encodeURIComponent(status)}` : "";
  return api.get<ScrapedSource[]>(`/v1/scraped-sources${qs}`);
}

// GET /v1/scraped-sources/{id}/chunks — the text-only preview chunks (no vectors).
export function getSourceChunks(id: string): Promise<SourceChunks> {
  return api.get<SourceChunks>(`/v1/scraped-sources/${encodeURIComponent(id)}/chunks`);
}

// POST /v1/scraped-sources/{id}/accept — ingest chunks into the pgvector corpus.
export function acceptSource(id: string): Promise<ScrapedSource> {
  return api.post<ScrapedSource>(`/v1/scraped-sources/${encodeURIComponent(id)}/accept`);
}

// POST /v1/scraped-sources/{id}/rescrape — enqueue a fresh scrape Job.
export function rescrapeSource(id: string): Promise<RescrapeResult> {
  return api.post<RescrapeResult>(`/v1/scraped-sources/${encodeURIComponent(id)}/rescrape`);
}

// POST /v1/scraped-sources/{id}/discard — reject; chunks never reach the corpus.
export function discardSource(id: string): Promise<ScrapedSource> {
  return api.post<ScrapedSource>(`/v1/scraped-sources/${encodeURIComponent(id)}/discard`);
}
