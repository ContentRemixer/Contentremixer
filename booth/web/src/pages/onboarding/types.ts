// TS mirror of the Booth JSON shapes the DevRel onboarding wizard consumes
// (ISI-7361 D6, plan ISI-7350 §6). Field names match the Go `json` tags exactly.
// The wizard spans five backend surfaces: persona CRUD (D1/D5), scrape jobs (D2),
// style-profile synthesize + read (D3/D6), content-test + validate (D4), and the
// first-run probe (D5, in app/onboarding.ts).

// Reference / scrape kinds — the closed set the scraper supports
// (app/review.KnownSourceKinds / v1alpha1.KnownReferenceKinds). "article" covers
// blog posts (there is no separate blog kind).
export type SourceKind =
  | "youtube"
  | "article"
  | "github-repo"
  | "teleprompter-script"
  | "talk-recording";

// Output medium the persona primarily produces (v1alpha1.KnownPrimaryContentKinds).
export type PrimaryContentKind = "blog" | "video" | "talk" | "tutorial";

// One style exemplar source the DevRel adds in O3.
export interface SourceDraft {
  // Stable local id so React can key rows and per-row progress survives reorder.
  id: string;
  kind: SourceKind;
  url: string;
}

// The DevRelPersona CR envelope the generic CRUD routes accept/return. The wizard
// only sets metadata.name + the spec fields it collects; owner is omitted so the
// server stamps the caller's subject (ISI-7360 D5 self-service).
export interface PersonaReference {
  url: string;
  kind: SourceKind;
}

export interface DevRelPersonaSpec {
  owner?: { subject?: string };
  selfDescription?: string;
  primaryContentKind?: PrimaryContentKind;
  references?: PersonaReference[];
}

export interface DevRelPersona {
  metadata: { name: string; creationTimestamp?: string };
  spec?: DevRelPersonaSpec;
  status?: Record<string, unknown>;
}

// POST /v1/scrape-jobs request + the ScrapeJob it returns (app/review.ScrapeJob).
export type JobStatus = "queued" | "running" | "succeeded" | "failed";

export interface CreateScrapeJobRequest {
  personaName: string;
  sourceUrl: string;
  sourceKind: SourceKind;
  referenceRepo?: string;
}

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

// Subset of ScrapedSource the wizard needs to accept pending sources into the
// corpus before synthesis (app/review.ScrapedSource).
export type SourceStatus = "pending" | "accepted" | "rescraping" | "discarded";

export interface ScrapedSource {
  id: string;
  jobId: string;
  owner: string;
  personaName: string;
  sourceUrl: string;
  sourceKind: string;
  title: string;
  status: SourceStatus;
  chunkCount: number;
}

// GET/POST style-profile response (app/review.StyleProfile). Traits + Sample are
// human-readable markdown the O7 review surface renders verbatim; SourceCount is
// the "derived from N sources" confidence read.
export interface StyleProfile {
  personaName: string;
  owner: string;
  referenceRepo?: string;
  traits: string;
  sample: string;
  validated: boolean;
  validatedAt?: string;
  sourceCount: number;
  synthesizedAt?: string;
}

// POST /v1/content-test response (booth contenttest.TestResult).
export interface ContentTestResult {
  personaName: string;
  sample: string;
  modelUsed: string;
  validated: boolean;
}
