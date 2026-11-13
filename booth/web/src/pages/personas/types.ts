// TS mirrors of the DevRel persona-management contracts (ISI-7362 D7, plan
// ISI-7350 §6): the DevRelPersona CR served by the Booth CRUD routes, the
// content-test / style-profile / promote JSON shapes, and the scrape-job
// enqueue payload. Hand-written and minimal like the scraper mirror: only the
// public wire shapes the persona surfaces consume. Field names match the Go
// `json` tags exactly (app/api/v1alpha1 + booth/internal/contenttest).

// Reference kinds — the closed set the scraper dispatches on (D1/D2). Kept in
// lock-step with v1alpha1.KnownReferenceKinds.
export const REFERENCE_KINDS = [
  "article",
  "youtube",
  "github-repo",
  "teleprompter-script",
  "talk-recording",
] as const;
export type ReferenceKind = (typeof REFERENCE_KINDS)[number];

export const REFERENCE_KIND_META: Record<ReferenceKind, { label: string; hint: string }> = {
  article: { label: "Blog / article", hint: "A blog post or web article URL" },
  youtube: { label: "YouTube video", hint: "A YouTube video URL (captions are fetched)" },
  "github-repo": { label: "GitHub repo", hint: "A GitHub repository URL (Markdown is fetched)" },
  "teleprompter-script": {
    label: "Teleprompter script",
    hint: "A hosted script document URL",
  },
  "talk-recording": { label: "Talk recording", hint: "A captioned talk recording URL" },
};

// PersonaReference is one style-exemplar source row.
export interface PersonaReference {
  url: string;
  kind: ReferenceKind;
}

// Output content types for ContentFormatTargets (D1b, ISI-7370) — the closed
// set mirrored from v1alpha1.KnownContentFormatTypes.
export const CONTENT_FORMAT_TYPES = [
  "blog",
  "youtube-short",
  "youtube-video",
  "linkedin-post",
] as const;
export type ContentFormatType = (typeof CONTENT_FORMAT_TYPES)[number];

export const CONTENT_FORMAT_META: Record<ContentFormatType, string> = {
  blog: "Blog post (e.g. ~1500 words)",
  "youtube-short": "YouTube short (e.g. ≤3 min)",
  "youtube-video": "YouTube video (e.g. ~25 min)",
  "linkedin-post": "LinkedIn post (e.g. ~1300 chars)",
};

// PrimaryContentKind — the DevRel's dominant output medium (plan O2).
export const PRIMARY_CONTENT_KINDS = ["blog", "video", "talk", "tutorial"] as const;
export type PrimaryContentKind = (typeof PRIMARY_CONTENT_KINDS)[number];

export interface ContentFormatTarget {
  contentType: ContentFormatType;
  lengthSpec: string;
}

export interface PersonaCondition {
  type: string;
  status: string;
  reason?: string;
  message?: string;
  lastTransitionTime?: string;
}

// DevRelPersona CR as served by GET/PUT /v1/devrelpersonas (bare-array list,
// single-object get/put). Server-side §6.3 is authoritative; the owner field
// must round-trip on every PUT (Update gates the incoming owner too).
export interface DevRelPersona {
  metadata: { name: string; creationTimestamp?: string; [k: string]: unknown };
  spec: {
    owner: { subject: string };
    selfDescription?: string;
    primaryContentKind?: PrimaryContentKind | "";
    references?: PersonaReference[];
    contentFormatTargets?: ContentFormatTarget[];
  };
  status?: {
    phase?: string;
    exemplarCount?: number;
    conditions?: PersonaCondition[];
  };
}

// review.StyleProfile (D3) as served by GET style-profile / POST validate.
export interface StyleProfile {
  personaName: string;
  owner: string;
  traits: string;
  sample: string;
  validated: boolean;
  validatedAt?: string;
  sourceCount: number;
  synthesizedAt: string;
}

// booth/internal/contenttest TestResult — POST /v1/content-test response.
export interface ContentTestResult {
  personaName: string;
  sample: string;
  modelUsed: string;
  validated: boolean;
}

// booth/internal/promote Result — POST /v1/promote response (D9, ISI-7369).
// Optional surface: the route is unmounted on a Booth without the promote
// store, so the UI degrades when it 404s.
export interface PromoteResult {
  personaName: string;
  hook: string;
  body: string;
  cta: string;
  post: string;
  modelUsed: string;
}

// POST /v1/scrape-jobs payload (booth/internal/scrapejobs CreateJobRequest).
export interface CreateScrapeJobRequest {
  personaName: string;
  sourceUrl: string;
  sourceKind: ReferenceKind;
}
