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
  // teleprompter-script is a no-fetch inline kind; everything else fetches a URL.
  // We still collect a URL/identifier for all kinds (the scrape job requires a
  // sourceUrl), but the hint tells the user what to put there.
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
  {
    kind: "teleprompter-script",
    label: "Teleprompter script",
    hint: "A talk/teleprompter script page. Paste a URL pointing to the script text.",
    placeholder: "https://…/my-talk-script.txt",
  },
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
