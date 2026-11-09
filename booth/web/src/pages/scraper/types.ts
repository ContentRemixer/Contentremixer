// TS mirror of the app/review scraper-review contract (ISI-7207, plan §3.2).
// Kept hand-written and minimal: the frontend only consumes the public JSON
// shapes the Booth scrapejobs handlers emit. Field names match the Go `json`
// tags exactly.

export type JobStatus = "queued" | "running" | "succeeded" | "failed";

export type SourceStatus =
  | "pending"
  | "accepted"
  | "rescraping"
  | "discarded";

export interface ScrapeJob {
  id: string;
  owner: string;
  referenceRepo: string;
  personaName: string;
  sourceUrl: string;
  sourceKind: string;
  status: JobStatus;
  k8sJobName?: string;
  error?: string;
  createdAt: string;
  updatedAt: string;
}

export interface ScrapedSource {
  id: string;
  jobId: string;
  owner: string;
  referenceRepo: string;
  personaName: string;
  sourceUrl: string;
  sourceKind: string;
  title: string;
  status: SourceStatus;
  chunkCount: number;
  createdAt: string;
  updatedAt: string;
}

export interface ScrapedChunk {
  id: string;
  sourceId: string;
  ordinal: number;
  text: string;
  metadata?: Record<string, unknown>;
}

// GET /v1/scraped-sources/{id}/chunks response shape.
export interface SourceChunks {
  source: ScrapedSource;
  chunks: ScrapedChunk[];
}

// POST /v1/scraped-sources/{id}/rescrape response shape.
export interface RescrapeResult {
  source: ScrapedSource;
  job: ScrapeJob;
}
