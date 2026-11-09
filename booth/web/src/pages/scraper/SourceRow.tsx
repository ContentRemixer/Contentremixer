import { Card, Badge, Button } from "../../ui";
import {
  ExternalLinkIcon,
  LayersIcon,
  CheckIcon,
  RefreshIcon,
  TrashIcon,
} from "../../components/icons";
import { SOURCE_STATUS_META } from "./status";
import type { ScrapedSource, SourceStatus } from "./types";

export type SourceAction = "accept" | "rescrape" | "discard";

interface SourceRowProps {
  source: ScrapedSource;
  /** Which action (if any) is currently in flight for this row. */
  pending?: SourceAction;
  onPreview: (source: ScrapedSource) => void;
  onAction: (source: ScrapedSource, action: SourceAction) => void;
}

// Only pending sources are actionable — the review state machine lets a source
// leave `pending` exactly once (app/review.validSourceTransitions).
const ACTIONABLE: Record<SourceStatus, boolean> = {
  pending: true,
  accepted: false,
  rescraping: false,
  discarded: false,
};

export function SourceRow({ source, pending, onPreview, onAction }: SourceRowProps) {
  const meta = SOURCE_STATUS_META[source.status];
  const actionable = ACTIONABLE[source.status];
  const busy = pending !== undefined;

  return (
    <Card className="p-4">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <h3 className="truncate text-sm font-semibold text-text">
              {source.title || "Untitled source"}
            </h3>
            <Badge tone={meta.tone} dot>
              {meta.label}
            </Badge>
          </div>

          <a
            href={source.sourceUrl}
            target="_blank"
            rel="noopener noreferrer"
            className="mt-1 inline-flex max-w-full items-center gap-1 text-xs text-text-muted hover:text-primary"
          >
            <span className="truncate">{source.sourceUrl}</span>
            <ExternalLinkIcon size={12} className="shrink-0" />
          </a>

          <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-text-muted">
            <span className="inline-flex items-center gap-1">
              <LayersIcon size={13} />
              {source.chunkCount} chunk{source.chunkCount === 1 ? "" : "s"}
            </span>
            <span>·</span>
            <span>{source.sourceKind}</span>
            <span>·</span>
            <span className="truncate">persona: {source.personaName}</span>
          </div>
        </div>

        <div className="flex shrink-0 flex-wrap items-center gap-2">
          <Button
            variant="secondary"
            size="sm"
            onClick={() => onPreview(source)}
            disabled={busy}
          >
            Preview
          </Button>

          {actionable && (
            <>
              <Button
                variant="primary"
                size="sm"
                iconLeft={<CheckIcon size={15} />}
                loading={pending === "accept"}
                disabled={busy}
                onClick={() => onAction(source, "accept")}
              >
                Accept
              </Button>
              <Button
                variant="secondary"
                size="sm"
                iconLeft={<RefreshIcon size={15} />}
                loading={pending === "rescrape"}
                disabled={busy}
                onClick={() => onAction(source, "rescrape")}
              >
                Rescrape
              </Button>
              <Button
                variant="ghost"
                size="sm"
                iconLeft={<TrashIcon size={15} />}
                loading={pending === "discard"}
                disabled={busy}
                onClick={() => onAction(source, "discard")}
              >
                Discard
              </Button>
            </>
          )}
        </div>
      </div>
    </Card>
  );
}
