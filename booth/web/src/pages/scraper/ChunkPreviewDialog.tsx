import { useEffect, useState } from "react";
import {
  Dialog,
  DialogContent,
  DialogBody,
  Badge,
} from "../../ui";
import { Spinner } from "../../components/Loader";
import { ApiError } from "../../app/api";
import { getSourceChunks } from "./api";
import type { ScrapedSource, ScrapedChunk } from "./types";
import { SOURCE_STATUS_META } from "./status";

interface ChunkPreviewDialogProps {
  /** The source whose chunks to preview; null closes the dialog. */
  source: ScrapedSource | null;
  onOpenChange: (open: boolean) => void;
}

// Lazily fetches and renders the text-only preview chunks of a scraped source
// (GET /v1/scraped-sources/{id}/chunks). Chunks carry no embeddings — this is
// exactly the text a human reviews before accepting it into the corpus.
export function ChunkPreviewDialog({ source, onOpenChange }: ChunkPreviewDialogProps) {
  const [chunks, setChunks] = useState<ScrapedChunk[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!source) return;
    let cancelled = false;
    setChunks(null);
    setError(null);
    getSourceChunks(source.id)
      .then((res) => {
        if (!cancelled) setChunks(res.chunks);
      })
      .catch((err) => {
        if (cancelled) return;
        setError(err instanceof ApiError ? err.message : "Failed to load chunks");
      });
    return () => {
      cancelled = true;
    };
  }, [source]);

  const meta = source ? SOURCE_STATUS_META[source.status] : null;

  return (
    <Dialog open={source !== null} onOpenChange={onOpenChange}>
      {source && (
        <DialogContent
          className="w-[min(94vw,46rem)]"
          title={source.title || "Scraped source"}
          description={source.sourceUrl}
        >
          <DialogBody className="max-h-[70vh] overflow-y-auto">
            <div className="mb-4 flex flex-wrap items-center gap-2">
              {meta && (
                <Badge tone={meta.tone} dot>
                  {meta.label}
                </Badge>
              )}
              <Badge tone="neutral">{source.sourceKind}</Badge>
              <Badge tone="neutral">
                {source.chunkCount} chunk{source.chunkCount === 1 ? "" : "s"}
              </Badge>
            </div>

            {error ? (
              <p className="rounded-md border border-warn/25 bg-warn/10 px-3 py-2 text-sm text-warn">
                {error}
              </p>
            ) : chunks === null ? (
              <div className="flex items-center gap-2 py-8 text-sm text-text-muted">
                <Spinner size={18} /> Loading chunks…
              </div>
            ) : chunks.length === 0 ? (
              <p className="py-8 text-center text-sm text-text-muted">
                This source produced no preview chunks.
              </p>
            ) : (
              <ol className="flex flex-col gap-3">
                {chunks.map((chunk) => (
                  <li
                    key={chunk.id}
                    className="rounded-md border border-border bg-surface-2 p-3"
                  >
                    <div className="mb-1.5 text-xs font-medium text-text-muted">
                      Chunk {chunk.ordinal + 1}
                    </div>
                    <p className="whitespace-pre-wrap text-sm leading-relaxed text-text">
                      {chunk.text}
                    </p>
                  </li>
                ))}
              </ol>
            )}
          </DialogBody>
        </DialogContent>
      )}
    </Dialog>
  );
}
