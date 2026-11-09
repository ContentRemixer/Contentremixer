// Presentation mapping for scraper-review statuses: label + Badge tone. Keeps
// the visual vocabulary in one place so the list rows, filter tabs, and chunk
// dialog all agree.

import type { BadgeTone } from "../../ui";
import type { SourceStatus, JobStatus } from "./types";

export const SOURCE_STATUS_META: Record<
  SourceStatus,
  { label: string; tone: BadgeTone }
> = {
  pending: { label: "Pending review", tone: "warning" },
  accepted: { label: "Accepted", tone: "success" },
  rescraping: { label: "Rescraping", tone: "info" },
  discarded: { label: "Discarded", tone: "neutral" },
};

export const JOB_STATUS_META: Record<JobStatus, { label: string; tone: BadgeTone }> = {
  queued: { label: "Queued", tone: "neutral" },
  running: { label: "Running", tone: "info" },
  succeeded: { label: "Succeeded", tone: "success" },
  failed: { label: "Failed", tone: "warning" },
};

// Filter order shown in the status tabs. "all" is a client-only pseudo-filter
// (omits the status query param).
export const SOURCE_FILTERS: { value: SourceStatus | "all"; label: string }[] = [
  { value: "pending", label: "Pending" },
  { value: "accepted", label: "Accepted" },
  { value: "rescraping", label: "Rescraping" },
  { value: "discarded", label: "Discarded" },
  { value: "all", label: "All" },
];
