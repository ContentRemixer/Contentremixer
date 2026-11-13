import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useParams } from "react-router-dom";
import {
  Badge,
  Button,
  Card,
  CardBody,
  CardHeader,
  Input,
  Label,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  useToast,
} from "../../ui";
import { ApiError } from "../../app/api";
import { createScrapeJob, getPersona, getScrapeJob, updatePersona } from "./api";
import { REFERENCE_KINDS, REFERENCE_KIND_META, type DevRelPersona, type ReferenceKind } from "./types";
import type { JobStatus, ScrapeJob } from "../scraper/types";

// P2 — Edit sources / re-ingest (ISI-7362 D7, plan §6 from the D0 mocks):
// manage the persona's style-exemplar references, then kick ingestion for the
// current source set into the scrape-job pipeline. The spec is a full PUT
// (server replaces the CR), so the complete spec is round-tripped with only the
// references rows replaced. Ingestion is per-source scrape jobs whose owner is
// stamped from the session (§6.3); their sources land in scraper-review for
// accept-into-corpus.
interface Row {
  url: string;
  kind: ReferenceKind;
}

const TERMINAL: JobStatus[] = ["succeeded", "failed"];

const JOB_TONES: Record<JobStatus, "info" | "success" | "warning" | "neutral"> = {
  queued: "info",
  running: "info",
  succeeded: "success",
  failed: "warning",
};

export function PersonaSourcesPanel() {
  const { name = "" } = useParams();
  const { toast } = useToast();
  const [persona, setPersona] = useState<DevRelPersona | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [rows, setRows] = useState<Row[]>([]);
  const [saving, setSaving] = useState(false);
  const [ingesting, setIngesting] = useState(false);
  const [jobs, setJobs] = useState<ScrapeJob[]>([]);
  const timer = useRef<number | null>(null);

  const load = useCallback(() => {
    setError(null);
    getPersona(name)
      .then((p) => {
        setPersona(p);
        setRows((p.spec.references ?? []).map((r) => ({ url: r.url, kind: r.kind })));
      })
      .catch((err) =>
        setError(err instanceof ApiError ? err.message : "Failed to load persona"),
      );
  }, [name]);

  useEffect(load, [load]);

  // Poll non-terminal jobs until they settle; the scraper-review surface takes
  // over once chunks arrive (accept/discard).
  useEffect(() => {
    const pendingJobs = jobs.filter((j) => !TERMINAL.includes(j.status));
    if (pendingJobs.length === 0) return;
    timer.current = window.setInterval(() => {
      Promise.allSettled(
        pendingJobs.map((j) => getScrapeJob(j.id)),
      ).then((settled) => {
        setJobs((cur) =>
          cur.map((j) => {
            const match = settled.find(
              (s) => s.status === "fulfilled" && s.value.id === j.id,
            );
            return match && match.status === "fulfilled" ? match.value : j;
          }),
        );
      });
    }, 2500);
    return () => {
      if (timer.current) window.clearInterval(timer.current);
    };
  }, [jobs]);

  const dirty =
    JSON.stringify(rows) !==
    JSON.stringify((persona?.spec.references ?? []).map((r) => ({ url: r.url, kind: r.kind })));

  function addRow() {
    setRows((r) => [...r, { url: "", kind: "article" }]);
  }

  function removeRow(i: number) {
    setRows((r) => r.filter((_, idx) => idx !== i));
  }

  function setRow(i: number, patch: Partial<Row>) {
    setRows((r) => r.map((row, idx) => (idx === i ? { ...row, ...patch } : row)));
  }

  async function save(): Promise<boolean> {
    const cleaned = rows.filter((r) => r.url.trim() !== "");
    const badKind = cleaned.find((r) => !REFERENCE_KINDS.includes(r.kind));
    if (badKind) {
      toast({ tone: "warning", title: "Pick a source type for every row first" });
      return false;
    }
    if (!persona) return false;
    setSaving(true);
    try {
      const updated = await updatePersona(name, {
        ...persona,
        spec: { ...persona.spec, references: cleaned },
      });
      setPersona(updated);
      setRows((updated.spec.references ?? []).map((r) => ({ url: r.url, kind: r.kind })));
      toast({ title: "Sources saved", tone: "success" });
      return true;
    } catch (err) {
      toast({
        tone: "warning",
        title: "Could not save sources",
        description: err instanceof ApiError ? err.message : "Unexpected error",
      });
      return false;
    } finally {
      setSaving(false);
    }
  }

  // Re-ingest enqueues one scrape job per saved source row. Jobs are owned by
  // the caller; their outputs become pending scraped sources to accept.
  async function reingest() {
    const current = rows.filter((r) => r.url.trim() !== "");
    if (dirty) {
      const ok = await save();
      if (!ok) return;
    }
    if (current.length === 0) {
      toast({ tone: "neutral", title: "Nothing to ingest", description: "Add a source first." });
      return;
    }
    setIngesting(true);
    try {
      const started: ScrapeJob[] = [];
      for (const r of current) {
        started.push(
          await createScrapeJob({
            personaName: name,
            sourceUrl: r.url.trim(),
            sourceKind: r.kind,
          }),
        );
      }
      setJobs((j) => [...started, ...j].slice(0, 25));
      toast({
        title: `Re-ingesting ${started.length} source${started.length === 1 ? "" : "s"}`,
        description: "Track each job below; accept the sources in scraper review when done.",
        tone: "success",
      });
    } catch (err) {
      toast({
        tone: "warning",
        title: "Could not start ingestion",
        description: err instanceof ApiError ? err.message : "Unexpected error",
      });
    } finally {
      setIngesting(false);
    }
  }

  if (error) {
    return (
      <div className="flex flex-col items-center gap-3 py-12">
        <p className="text-sm text-text-muted">{error}</p>
        <Button variant="secondary" size="sm" onClick={load}>
          Retry
        </Button>
      </div>
    );
  }

  if (!persona) {
    return <div className="py-12 text-center text-sm text-text-muted">Loading persona…</div>;
  }

  return (
    <div className="flex flex-col gap-6">
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold text-text">Sources — {name}</h1>
          <p className="mt-1 text-sm text-text-muted">
            Blog posts, videos, repos and scripts the platform learns your style from.
          </p>
        </div>
        <div className="flex gap-2">
          <Button variant="secondary" loading={saving} disabled={!dirty} onClick={save}>
            Save changes
          </Button>
          <Button loading={ingesting} onClick={reingest}>
            Save &amp; re-ingest all
          </Button>
        </div>
      </header>

      <Card>
        <CardHeader
          title="Style exemplar sources"
          subtitle="Editing the list is what drives re-ingestion — saved changes move the persona to Pending."
          action={
            <Button size="sm" variant="secondary" onClick={addRow}>
              Add source
            </Button>
          }
        />
        <CardBody className="flex flex-col gap-3">
          {rows.length === 0 ? (
            <p className="text-sm text-text-muted">No sources yet — add your first one.</p>
          ) : (
            rows.map((r, i) => (
              <div key={i} className="flex flex-wrap items-end gap-2">
                <div className="w-full sm:w-56">
                  <Label htmlFor={`kind-${i}`} className="text-xs text-text-muted">
                    Type
                  </Label>
                  <Select value={r.kind} onValueChange={(v) => setRow(i, { kind: v as ReferenceKind })}>
                    <SelectTrigger id={`kind-${i}`} className="mt-1" />
                    <SelectContent>
                      {REFERENCE_KINDS.map((k) => (
                        <SelectItem key={k} value={k}>
                          {REFERENCE_KIND_META[k].label}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <div className="min-w-0 flex-1">
                  <Label htmlFor={`url-${i}`} className="text-xs text-text-muted">
                    URL
                  </Label>
                  <Input
                    id={`url-${i}`}
                    className="mt-1"
                    placeholder={REFERENCE_KIND_META[r.kind].hint}
                    value={r.url}
                    onChange={(e) => setRow(i, { url: e.target.value })}
                  />
                </div>
                <Button variant="ghost" size="sm" onClick={() => removeRow(i)}>
                  Remove
                </Button>
              </div>
            ))
          )}
        </CardBody>
      </Card>

      {jobs.length > 0 ? (
        <Card>
          <CardHeader
            title="Ingestion jobs"
            subtitle="Queued → scraping → succeeded. Accepted sources feed your style profile."
            action={
              <Button size="sm" variant="secondary">
                <Link to="/scraper-review">Open scraper review</Link>
              </Button>
            }
          />
          <CardBody className="flex flex-col gap-2">
            {jobs.map((j) => (
              <div key={j.id} className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                  <p className="truncate text-sm text-text">{j.sourceUrl}</p>
                  {j.error ? <p className="text-xs text-warn">{j.error}</p> : null}
                </div>
                <Badge tone={JOB_TONES[j.status]} dot>
                  {j.status}
                </Badge>
              </div>
            ))}
          </CardBody>
        </Card>
      ) : null}
    </div>
  );
}
