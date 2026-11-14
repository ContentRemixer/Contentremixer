// Display metadata for the onboarding typed source cards (O3) and primary-content
// picker (O2). Keeping labels/hints/placeholders here keeps the step components
// declarative — the option lists drive both the pickers and the review summary.

import type { SourceKind, PrimaryContentKind } from "./types";

export interface SourceKindMeta {
  kind: SourceKind;
  label: string;
  // What the card tells the DevRel to paste.
  hint: string;
  placeholder: string;
  // Every kind here fetches from a URL/identifier (the scrape job requires a
  // sourceUrl); the hint tells the user what to put there. The no-fetch
  // teleprompter-script kind is deliberately NOT offered — see below.
}

export const SOURCE_KINDS: SourceKindMeta[] = [
  {
    kind: "article",
    label: "Blog / article",
    hint: "A blog post or written article. The scraper extracts the readable body.",
    placeholder: "https://example.com/posts/shipping-faster",
  },
  {
    kind: "youtube",
    label: "YouTube video",
    hint: "A YouTube video; its caption track is fetched as the exemplar text.",
    placeholder: "https://www.youtube.com/watch?v=…",
  },
  {
    kind: "talk-recording",
    label: "Talk recording",
    hint: "A recorded talk with captions (VTT/SRT/XML or a captioned video URL).",
    placeholder: "https://…/keynote.vtt",
  },
  {
    kind: "github-repo",
    label: "GitHub tutorial repo",
    hint: "A GitHub repository; its markdown files are read as writing samples.",
    placeholder: "https://github.com/you/your-tutorial",
  },
  // NOTE (ISI-7435): "teleprompter-script" is a no-fetch kind — the scraper
  // ingests an inline script BODY (SCRAPE_SOURCE_TEXT), not a URL, and the
  // scrape-job fails closed without it. The wizard only captures a sourceUrl
  // per source (no paste-the-script textarea yet), so offering this kind here
  // would enqueue a job that errors at ingestion. It is intentionally omitted
  // until sourceText capture is designed; the backend keeps full support so an
  // API client can still ingest a script directly. Follow-up: ISI-7438.
];

export function sourceKindLabel(kind: string): string {
  return SOURCE_KINDS.find((k) => k.kind === kind)?.label ?? kind;
}

export interface PrimaryKindMeta {
  kind: PrimaryContentKind;
  label: string;
  blurb: string;
}

export const PRIMARY_KINDS: PrimaryKindMeta[] = [
  { kind: "blog", label: "Blog posts", blurb: "Written articles and long-form posts." },
  { kind: "video", label: "Video", blurb: "YouTube videos, shorts, and screencasts." },
  { kind: "talk", label: "Talks", blurb: "Conference talks and live presentations." },
  { kind: "tutorial", label: "Tutorials", blurb: "Hands-on guides and code walkthroughs." },
];
