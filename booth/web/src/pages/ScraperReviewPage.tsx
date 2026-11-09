import { useCallback, useEffect, useState } from "react";
import {
  Tabs,
  TabsList,
  TabsTrigger,
  TabsContent,
  Card,
  Button,
  useToast,
} from "../ui";
import { ScraperIcon } from "../components/icons";
import { ApiError } from "../app/api";
import {
  listScrapedSources,
  acceptSource,
  rescrapeSource,
  discardSource,
} from "./scraper/api";
import { SOURCE_FILTERS } from "./scraper/status";
import { SourceRow } from "./scraper/SourceRow";
import type { SourceAction } from "./scraper/SourceRow";
import { ChunkPreviewDialog } from "./scraper/ChunkPreviewDialog";
import type { ScrapedSource, SourceStatus } from "./scraper/types";

type Filter = SourceStatus | "all";

// Verb + toast copy per action, keyed off the review state machine outcomes.
const ACTION_COPY: Record<SourceAction, { verb: string; done: string }> = {
  accept: { verb: "accept", done: "accepted — chunks queued for the corpus" },
  rescrape: { verb: "rescrape", done: "rescraping — a fresh scrape job was enqueued" },
  discard: { verb: "discard", done: "discarded" },
};

// B6 scraper-review screen (ISI-7211, W3). Lists the sources a scrape Job
// produced, lets the owner preview their chunks, and accept / rescrape / discard
// each pending one. Consumes the W2 Booth API (scrapejobs.go); authz is enforced
// server-side (§6.3).
export function ScraperReviewPage() {
  const { toast } = useToast();
  const [filter, setFilter] = useState<Filter>("pending");
  const [sources, setSources] = useState<ScrapedSource[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [previewing, setPreviewing] = useState<ScrapedSource | null>(null);
  // Tracks the in-flight action per source id so only that row shows a spinner.
  const [busy, setBusy] = useState<Record<string, SourceAction>>({});

  const load = useCallback(
    (f: Filter) => {
      setSources(null);
      setError(null);
      listScrapedSources(f === "all" ? undefined : f)
        .then(setSources)
        .catch((err) =>
          setError(err instanceof ApiError ? err.message : "Failed to load scraped sources"),
        );
    },
    [],
  );

  useEffect(() => {
    load(filter);
  }, [filter, load]);

  const runAction = useCallback(
    async (source: ScrapedSource, action: SourceAction) => {
      setBusy((b) => ({ ...b, [source.id]: action }));
      const copy = ACTION_COPY[action];
      try {
        if (action === "accept") await acceptSource(source.id);
        else if (action === "rescrape") await rescrapeSource(source.id);
        else await discardSource(source.id);

        toast({
          tone: action === "discard" ? "neutral" : "success",
          title: `"${source.title || source.sourceUrl}"`,
          description: copy.done,
        });
        // The source leaves `pending`, so the current (pending) view no longer
        // includes it — reload to reflect the new state machine position.
        load(filter);
      } catch (err) {
        toast({
          tone: "warning",
          title: `Could not ${copy.verb} source`,
          description: err instanceof ApiError ? err.message : "Unexpected error",
        });
      } finally {
        setBusy((b) => {
          const next = { ...b };
          delete next[source.id];
          return next;
        });
      }
    },
    [filter, load, toast],
  );

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
      <header className="flex items-start gap-3">
        <span className="mt-0.5 grid h-10 w-10 shrink-0 place-items-center rounded-md bg-primary/10 text-primary">
          <ScraperIcon size={22} />
        </span>
        <div>
          <h1 className="text-lg font-semibold text-text">Scraper review</h1>
          <p className="mt-0.5 text-sm text-text-muted">
            Review scraped reference sources and accept their chunks into the persona corpus.
          </p>
        </div>
      </header>

      <Tabs value={filter} onValueChange={(v: string) => setFilter(v as Filter)}>
        <TabsList>
          {SOURCE_FILTERS.map((f) => (
            <TabsTrigger key={f.value} value={f.value}>
              {f.label}
            </TabsTrigger>
          ))}
        </TabsList>

        {/* Single content region bound to the active filter so it always renders. */}
        <TabsContent value={filter}>
          {error ? (
            <Card className="flex flex-col items-center gap-3 p-10 text-center">
              <p className="text-sm text-warn">{error}</p>
              <Button variant="secondary" size="sm" onClick={() => load(filter)}>
                Try again
              </Button>
            </Card>
          ) : sources === null ? (
            <SourceListSkeleton />
          ) : sources.length === 0 ? (
            <EmptyState filter={filter} />
          ) : (
            <ul className="flex flex-col gap-3">
              {sources.map((s) => (
                <li key={s.id}>
                  <SourceRow
                    source={s}
                    pending={busy[s.id]}
                    onPreview={setPreviewing}
                    onAction={runAction}
                  />
                </li>
              ))}
            </ul>
          )}
        </TabsContent>
      </Tabs>

      <ChunkPreviewDialog
        source={previewing}
        onOpenChange={(open) => !open && setPreviewing(null)}
      />
    </div>
  );
}

function SourceListSkeleton() {
  return (
    <ul className="flex flex-col gap-3" aria-hidden>
      {[0, 1, 2].map((i) => (
        <li key={i}>
          <Card className="p-4">
            <div className="animate-pulse space-y-3">
              <div className="h-4 w-1/2 rounded bg-surface-2" />
              <div className="h-3 w-2/3 rounded bg-surface-2" />
              <div className="h-3 w-1/3 rounded bg-surface-2" />
            </div>
          </Card>
        </li>
      ))}
    </ul>
  );
}

function EmptyState({ filter }: { filter: Filter }) {
  const label =
    filter === "pending"
      ? "No sources are waiting for review."
      : filter === "all"
        ? "No scraped sources yet."
        : `No ${filter} sources.`;
  return (
    <Card className="flex flex-col items-center gap-2 p-12 text-center">
      <span className="grid h-12 w-12 place-items-center rounded-full bg-surface-2 text-text-muted">
        <ScraperIcon size={24} />
      </span>
      <p className="text-sm font-medium text-text">{label}</p>
      <p className="max-w-sm text-xs text-text-muted">
        Scraped sources appear here after a scrape job runs. Pending ones can be
        accepted into the corpus, rescraped, or discarded.
      </p>
    </Card>
  );
}
