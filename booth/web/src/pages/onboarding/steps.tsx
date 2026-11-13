import { useState } from "react";
import {
  Button,
  Card,
  CardBody,
  Field,
  Input,
  Textarea,
  Badge,
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
  cx,
} from "../../ui";
import { Spinner } from "../../components/Loader";
import {
  SparkleIcon,
  PlusIcon,
  TrashIcon,
  CheckIcon,
  RefreshIcon,
  AlertTriangleIcon,
} from "../../components/icons";
import { StepScaffold } from "./WizardFrame";
import { SOURCE_KINDS, sourceKindLabel, PRIMARY_KINDS } from "./kinds";
import type {
  SourceDraft,
  SourceKind,
  PrimaryContentKind,
  StyleProfile,
  ContentTestResult,
} from "./types";
import type { SourceProgress, IngestPhase } from "../OnboardingWizard";

const backBtn = (onBack: () => void, label = "Back") => (
  <Button variant="ghost" onClick={onBack} type="button">
    {label}
  </Button>
);

// --- O1 Welcome -----------------------------------------------------------
export function StepWelcome({ onNext }: { onNext: () => void }) {
  return (
    <StepScaffold
      title="Let’s capture your voice"
      lead="We’ll learn your writing style from a few samples, then you can review and approve it. It takes about five minutes."
      footer={
        <>
          <span />
          <Button onClick={onNext}>Get started</Button>
        </>
      }
    >
      <ol className="space-y-3 text-sm text-text-muted">
        {[
          "Pick the kind of content you make and point us at a few examples.",
          "We ingest them and synthesize a named style profile.",
          "You review the style, run a test, and validate — then you’re set.",
        ].map((t, i) => (
          <li key={i} className="flex gap-3">
            <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full border border-border text-xs text-text">
              {i + 1}
            </span>
            <span className="pt-0.5">{t}</span>
          </li>
        ))}
      </ol>
    </StepScaffold>
  );
}

// --- O2 Primary content kind ---------------------------------------------
export function StepPrimaryKind({
  value,
  onChange,
  onBack,
  onNext,
}: {
  value: PrimaryContentKind | null;
  onChange: (k: PrimaryContentKind) => void;
  onBack: () => void;
  onNext: () => void;
}) {
  return (
    <StepScaffold
      title="What do you mostly create?"
      lead="This orients how we synthesize your style and generate test content. You can refine it later."
      footer={
        <>
          {backBtn(onBack)}
          <Button onClick={onNext} disabled={!value}>
            Continue
          </Button>
        </>
      }
    >
      <div className="grid gap-3 sm:grid-cols-2">
        {PRIMARY_KINDS.map((k) => {
          const selected = value === k.kind;
          return (
            <button
              key={k.kind}
              type="button"
              onClick={() => onChange(k.kind)}
              aria-pressed={selected}
              className={cx(
                "rounded-lg border p-4 text-left transition-colors",
                selected
                  ? "border-accent bg-surface-2 ring-1 ring-accent"
                  : "border-border bg-surface hover:border-border-strong",
              )}
            >
              <div className="flex items-center justify-between">
                <span className="text-sm font-medium text-text">{k.label}</span>
                {selected ? <CheckIcon width={16} height={16} /> : null}
              </div>
              <p className="mt-1 text-sm text-text-muted">{k.blurb}</p>
            </button>
          );
        })}
      </div>
    </StepScaffold>
  );
}

// --- O3 Add sources -------------------------------------------------------
export function StepSources({
  sources,
  onChange,
  onBack,
  onNext,
}: {
  sources: SourceDraft[];
  onChange: (next: SourceDraft[]) => void;
  onBack: () => void;
  onNext: () => void;
}) {
  function addRow(kind: SourceKind) {
    onChange([...sources, { id: cryptoId(), kind, url: "" }]);
  }
  function update(id: string, patch: Partial<SourceDraft>) {
    onChange(sources.map((s) => (s.id === id ? { ...s, ...patch } : s)));
  }
  function remove(id: string) {
    onChange(sources.filter((s) => s.id !== id));
  }
  const validCount = sources.filter((s) => s.url.trim() !== "").length;

  return (
    <StepScaffold
      title="Add a few writing samples"
      lead="Point us at examples of your work. Three to five across formats gives the best style profile."
      footer={
        <>
          {backBtn(onBack)}
          <Button onClick={onNext} disabled={validCount === 0}>
            Continue{validCount ? ` with ${validCount}` : ""}
          </Button>
        </>
      }
    >
      <div className="mb-4 flex flex-wrap gap-2">
        {SOURCE_KINDS.map((k) => (
          <Button key={k.kind} variant="secondary" size="sm" onClick={() => addRow(k.kind)} type="button">
            <PlusIcon width={14} height={14} /> {k.label}
          </Button>
        ))}
      </div>

      {sources.length === 0 ? (
        <Card>
          <CardBody className="text-sm text-text-muted">
            No sources yet — add one with a button above. Pick the type that matches each sample.
          </CardBody>
        </Card>
      ) : (
        <ul className="space-y-3">
          {sources.map((s) => {
            const meta = SOURCE_KINDS.find((k) => k.kind === s.kind);
            return (
              <li key={s.id} className="rounded-lg border border-border bg-surface p-3">
                <div className="flex flex-col gap-2 sm:flex-row sm:items-start">
                  <div className="w-full sm:w-52">
                    <Select value={s.kind} onValueChange={(v) => update(s.id, { kind: v as SourceKind })}>
                      <SelectTrigger aria-label="Source type">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {SOURCE_KINDS.map((k) => (
                          <SelectItem key={k.kind} value={k.kind}>
                            {k.label}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </div>
                  <div className="min-w-0 flex-1">
                    <Input
                      value={s.url}
                      placeholder={meta?.placeholder}
                      onChange={(e) => update(s.id, { url: e.target.value })}
                      aria-label="Source URL"
                    />
                    <p className="mt-1 text-xs text-text-muted">{meta?.hint}</p>
                  </div>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => remove(s.id)}
                    aria-label="Remove source"
                    type="button"
                  >
                    <TrashIcon width={16} height={16} />
                  </Button>
                </div>
              </li>
            );
          })}
        </ul>
      )}
    </StepScaffold>
  );
}

// --- O4 Bio (SelfDescription) --------------------------------------------
export function StepBio({
  value,
  onChange,
  onBack,
  onNext,
}: {
  value: string;
  onChange: (v: string) => void;
  onBack: () => void;
  onNext: () => void;
}) {
  return (
    <StepScaffold
      title="Describe your voice"
      lead="A sentence or two about your tone, audience, and what makes your writing yours. This seeds the style even before samples are analyzed. Optional, but it helps."
      footer={
        <>
          {backBtn(onBack)}
          <Button onClick={onNext}>Continue</Button>
        </>
      }
    >
      <Field label="About your writing" hint="e.g. “Warm and concrete, for working developers. I favor short sentences and real examples over hype.”">
        {({ id }) => (
          <Textarea
            id={id}
            value={value}
            onChange={(e) => onChange(e.target.value)}
            rows={6}
            placeholder="How would you describe your style?"
          />
        )}
      </Field>
    </StepScaffold>
  );
}

// --- O5 Review & start ----------------------------------------------------
export function StepReview({
  primaryKind,
  sources,
  bio,
  starting,
  error,
  onBack,
  onStart,
}: {
  primaryKind: PrimaryContentKind | null;
  sources: SourceDraft[];
  bio: string;
  starting: boolean;
  error: string | null;
  onBack: () => void;
  onStart: () => void;
}) {
  const valid = sources.filter((s) => s.url.trim() !== "");
  return (
    <StepScaffold
      title="Review & start ingestion"
      lead="We’ll create your persona, scrape each source, and synthesize your style. You can review the result before anything is finalized."
      footer={
        <>
          {backBtn(onBack)}
          <Button onClick={onStart} disabled={starting || valid.length === 0}>
            {starting ? (
              <>
                <Spinner size={16} /> Starting…
              </>
            ) : (
              "Start ingestion"
            )}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <SummaryRow label="Primary content">
          {primaryKind ? <Badge tone="info">{PRIMARY_KINDS.find((k) => k.kind === primaryKind)?.label}</Badge> : "—"}
        </SummaryRow>
        <SummaryRow label={`Sources (${valid.length})`}>
          <ul className="space-y-1">
            {valid.map((s) => (
              <li key={s.id} className="flex items-center gap-2 text-sm">
                <Badge tone="neutral">{sourceKindLabel(s.kind)}</Badge>
                <span className="truncate text-text-muted">{s.url}</span>
              </li>
            ))}
          </ul>
        </SummaryRow>
        <SummaryRow label="About you">
          <span className="text-sm text-text-muted">{bio.trim() ? bio : "— (none provided)"}</span>
        </SummaryRow>
        {error ? <ErrorNote>{error}</ErrorNote> : null}
      </div>
    </StepScaffold>
  );
}

function SummaryRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid gap-1 sm:grid-cols-[10rem_1fr] sm:gap-4">
      <div className="text-sm font-medium text-text">{label}</div>
      <div className="min-w-0">{children}</div>
    </div>
  );
}

// --- O6 Ingestion progress ------------------------------------------------
const PHASE_STEPS: { phase: IngestPhase; label: string }[] = [
  { phase: "scraping", label: "Scraping & chunking sources" },
  { phase: "accepting", label: "Embedding into your corpus" },
  { phase: "synthesizing", label: "Synthesizing your style" },
];

export function StepIngestion({
  phase,
  progress,
  error,
  onRetry,
  onBack,
}: {
  phase: IngestPhase;
  progress: SourceProgress[];
  error: string | null;
  onRetry: () => void;
  onBack: () => void;
}) {
  const phaseIndex = PHASE_STEPS.findIndex((p) => p.phase === phase);
  return (
    <StepScaffold
      title="Building your style profile"
      lead="This runs on its own — a scrape job per source, then embedding, then one synthesis pass. Hang tight."
      footer={
        error ? (
          <>
            {backBtn(onBack, "Edit sources")}
            <Button onClick={onRetry}>
              <RefreshIcon width={15} height={15} /> Retry
            </Button>
          </>
        ) : undefined
      }
    >
      <ol className="mb-6 space-y-2">
        {PHASE_STEPS.map((p, i) => {
          const done = phase === "done" || (phaseIndex >= 0 && i < phaseIndex);
          const active = p.phase === phase;
          return (
            <li key={p.phase} className="flex items-center gap-3 text-sm">
              <span
                className={cx(
                  "flex h-6 w-6 items-center justify-center rounded-full border",
                  done && "border-accent bg-accent text-primary-contrast",
                  active && !error && "border-accent text-accent",
                  !done && !active && "border-border text-text-muted",
                )}
              >
                {done ? <CheckIcon width={14} height={14} /> : active && !error ? <Spinner size={14} /> : i + 1}
              </span>
              <span className={cx(done || active ? "text-text" : "text-text-muted")}>{p.label}</span>
            </li>
          );
        })}
      </ol>

      <div className="space-y-2">
        {progress.map((s) => (
          <div
            key={s.id}
            className="flex items-center gap-3 rounded-md border border-border bg-surface px-3 py-2 text-sm"
          >
            <SourceStatusDot status={s.status} />
            <span className="min-w-0 flex-1 truncate text-text-muted">{s.url}</span>
            <Badge tone={s.status === "failed" ? "warning" : s.status === "succeeded" ? "success" : "neutral"}>
              {s.status}
            </Badge>
          </div>
        ))}
      </div>

      {error ? <ErrorNote>{error}</ErrorNote> : null}
    </StepScaffold>
  );
}

function SourceStatusDot({ status }: { status: SourceProgress["status"] }) {
  if (status === "succeeded") return <CheckIcon width={16} height={16} />;
  if (status === "failed") return <AlertTriangleIcon width={16} height={16} />;
  return <Spinner size={14} />;
}

// --- O7 Style-profile review ----------------------------------------------
export function StepStyleProfile({
  profile,
  resynthesizing,
  onResynthesize,
  onBack,
  onNext,
}: {
  profile: StyleProfile;
  resynthesizing: boolean;
  onResynthesize: () => void;
  onBack: () => void;
  onNext: () => void;
}) {
  return (
    <StepScaffold
      title="Here’s your style"
      lead={
        <>
          Synthesized from{" "}
          <strong className="text-text">
            {profile.sourceCount} source{profile.sourceCount === 1 ? "" : "s"}
          </strong>
          . Read it over — if it doesn’t sound like you, re-synthesize or add more samples.
        </>
      }
      footer={
        <>
          {backBtn(onBack)}
          <div className="flex gap-2">
            <Button variant="secondary" onClick={onResynthesize} disabled={resynthesizing} type="button">
              {resynthesizing ? <Spinner size={16} /> : <RefreshIcon width={15} height={15} />} Re-synthesize
            </Button>
            <Button onClick={onNext}>Looks right — continue</Button>
          </div>
        </>
      }
    >
      <div className="space-y-4">
        <Card>
          <CardBody>
            <div className="mb-2 flex items-center gap-2">
              <span className="text-accent">
                <SparkleIcon width={16} height={16} />
              </span>
              <h2 className="text-sm font-semibold text-text">Style traits</h2>
            </div>
            <p className="whitespace-pre-wrap text-sm leading-relaxed text-text-muted">{profile.traits}</p>
          </CardBody>
        </Card>
        <Card>
          <CardBody>
            <h2 className="mb-2 text-sm font-semibold text-text">Sample in your voice</h2>
            <p className="whitespace-pre-wrap text-sm leading-relaxed text-text-muted">{profile.sample}</p>
          </CardBody>
        </Card>
      </div>
    </StepScaffold>
  );
}

// --- O8 Content test + validate -------------------------------------------
export function StepContentTest({
  result,
  testing,
  validating,
  error,
  onRunTest,
  onValidate,
  onBack,
}: {
  result: ContentTestResult | null;
  testing: boolean;
  validating: boolean;
  error: string | null;
  onRunTest: (topic: string) => void;
  onValidate: () => void;
  onBack: () => void;
}) {
  const [topic, setTopic] = useState("");
  return (
    <StepScaffold
      title="Try it on a real topic"
      lead="Give us a topic and we’ll draft a short sample in your newly-synthesized voice. When it reads right, validate to finish."
      footer={
        <>
          {backBtn(onBack)}
          <Button onClick={onValidate} disabled={validating || !result}>
            {validating ? (
              <>
                <Spinner size={16} /> Validating…
              </>
            ) : (
              "Validate & finish"
            )}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <div className="flex flex-col gap-2 sm:flex-row sm:items-end">
          <div className="flex-1">
            <Field label="Topic">
              {({ id }) => (
                <Input
                  id={id}
                  value={topic}
                  placeholder="e.g. Why idempotent deploys matter"
                  onChange={(e) => setTopic(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" && topic.trim() && !testing) onRunTest(topic.trim());
                  }}
                />
              )}
            </Field>
          </div>
          <Button onClick={() => onRunTest(topic.trim())} disabled={testing || topic.trim() === ""} type="button">
            {testing ? <Spinner size={16} /> : <SparkleIcon width={15} height={15} />}
            {result ? " Regenerate" : " Generate sample"}
          </Button>
        </div>

        {error ? <ErrorNote>{error}</ErrorNote> : null}

        {result ? (
          <Card>
            <CardBody>
              <div className="mb-2 flex items-center justify-between">
                <h2 className="text-sm font-semibold text-text">Sample</h2>
                <Badge tone="neutral">{result.modelUsed}</Badge>
              </div>
              <p className="whitespace-pre-wrap text-sm leading-relaxed text-text-muted">{result.sample}</p>
            </CardBody>
          </Card>
        ) : (
          <Card>
            <CardBody className="text-sm text-text-muted">
              Enter a topic and generate a sample to preview your voice before validating.
            </CardBody>
          </Card>
        )}
      </div>
    </StepScaffold>
  );
}

// --- O9 Complete ----------------------------------------------------------
export function StepComplete({ onDone }: { onDone: () => void }) {
  return (
    <StepScaffold
      title="You’re all set"
      lead="Your persona is validated and ready. Content generated from now on is conditioned on your style."
      footer={
        <>
          <span />
          <Button onClick={onDone}>Go to dashboard</Button>
        </>
      }
    >
      <div className="flex flex-col items-center gap-3 rounded-lg border border-border bg-surface py-10 text-center">
        <span className="flex h-12 w-12 items-center justify-center rounded-full bg-accent text-primary-contrast">
          <CheckIcon width={24} height={24} />
        </span>
        <p className="text-sm text-text-muted">
          You can revisit and refine your sources, bio, and style any time from your persona settings.
        </p>
      </div>
    </StepScaffold>
  );
}

// --- shared bits ----------------------------------------------------------
function ErrorNote({ children }: { children: React.ReactNode }) {
  return (
    <div
      role="alert"
      className="flex items-start gap-2 rounded-md border border-warn/40 bg-warn/10 px-3 py-2 text-sm text-text"
    >
      <span className="mt-0.5 shrink-0 text-warn">
        <AlertTriangleIcon width={16} height={16} />
      </span>
      <span>{children}</span>
    </div>
  );
}

// cryptoId returns a short unique id for a source row. crypto.randomUUID is
// available in every browser the SPA targets; the fallback keeps TS + older
// environments happy without pulling a dependency.
export function cryptoId(): string {
  const c = globalThis.crypto as Crypto | undefined;
  if (c && typeof c.randomUUID === "function") return c.randomUUID();
  return `src-${Math.random().toString(36).slice(2)}`;
}
