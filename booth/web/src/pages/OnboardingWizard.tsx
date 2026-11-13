import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useAuth } from "../app/auth";
import { ApiError } from "../app/api";
import { useToast } from "../ui";
import { WizardFrame } from "./onboarding/WizardFrame";
import {
  StepWelcome,
  StepPrimaryKind,
  StepSources,
  StepBio,
  StepReview,
  StepIngestion,
  StepStyleProfile,
  StepContentTest,
  StepComplete,
  cryptoId,
} from "./onboarding/steps";
import { useOnboardingGate } from "./onboarding/gate";
import * as apiFns from "./onboarding/api";
import type {
  SourceDraft,
  PrimaryContentKind,
  JobStatus,
  StyleProfile,
  ContentTestResult,
  PersonaReference,
} from "./onboarding/types";

// Sub-phases of the O6 ingestion run, surfaced as a checklist to the user.
export type IngestPhase = "scraping" | "accepting" | "synthesizing" | "done";

// Per-source scrape progress tracked during O6.
export interface SourceProgress {
  id: string;
  url: string;
  jobId: string;
  status: JobStatus;
}

const POLL_INTERVAL_MS = 2500;
const POLL_MAX_TICKS = 160; // ~6½ min ceiling before we surface a timeout.

// personaNameFor derives a stable, DNS-1123-safe persona name from the caller's
// subject (booth:local:<username>). Onboarding gives a DevRel a single persona;
// keying it off the subject makes the wizard idempotent on re-entry.
function personaNameFor(subject: string): string {
  const tail = subject.split(":").pop() ?? subject;
  const slug = tail
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 50);
  return slug || "my-persona";
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

// errMsg pulls a human message out of an ApiError, falling back to a default.
function errMsg(err: unknown, fallback: string): string {
  if (err instanceof ApiError) return err.message || fallback;
  return fallback;
}

// OnboardingWizard is the first-run DevRel flow (ISI-7361 D6, plan §6). It owns
// all wizard state + the async orchestration (create persona → scrape → accept →
// synthesize → review → content-test → validate); the step components under
// ./onboarding are presentational.
export function OnboardingWizard() {
  const { session } = useAuth();
  const { toast } = useToast();
  const navigate = useNavigate();
  const { markDone } = useOnboardingGate();

  const personaName = useRef(personaNameFor(session?.sub ?? "")).current;

  const [step, setStep] = useState(1);

  // O2–O4 collected inputs.
  const [primaryKind, setPrimaryKind] = useState<PrimaryContentKind | null>(null);
  const [sources, setSources] = useState<SourceDraft[]>([{ id: cryptoId(), kind: "article", url: "" }]);
  const [bio, setBio] = useState("");

  // O5 start.
  const [starting, setStarting] = useState(false);
  const [startError, setStartError] = useState<string | null>(null);

  // O6 ingestion.
  const [phase, setPhase] = useState<IngestPhase>("scraping");
  const [progress, setProgress] = useState<SourceProgress[]>([]);
  const [ingestError, setIngestError] = useState<string | null>(null);
  const [runToken, setRunToken] = useState(0);

  // O7 style profile.
  const [profile, setProfile] = useState<StyleProfile | null>(null);
  const [resynthesizing, setResynthesizing] = useState(false);

  // O8 content test + validate.
  const [testResult, setTestResult] = useState<ContentTestResult | null>(null);
  const [testing, setTesting] = useState(false);
  const [testError, setTestError] = useState<string | null>(null);
  const [validating, setValidating] = useState(false);

  // --- O5: create persona + enqueue a scrape job per source ---------------
  const start = useCallback(async () => {
    const valid = sources.filter((s) => s.url.trim() !== "");
    if (valid.length === 0) return;
    setStarting(true);
    setStartError(null);
    try {
      const references: PersonaReference[] = valid.map((s) => ({ url: s.url.trim(), kind: s.kind }));
      await apiFns.upsertPersona(personaName, {
        selfDescription: bio.trim() || undefined,
        primaryContentKind: primaryKind ?? undefined,
        references,
      });

      const started: SourceProgress[] = [];
      for (const s of valid) {
        const job = await apiFns.createScrapeJob({
          personaName,
          sourceUrl: s.url.trim(),
          sourceKind: s.kind,
        });
        started.push({ id: s.id, url: s.url.trim(), jobId: job.id, status: job.status });
      }
      setProgress(started);
      setPhase("scraping");
      setIngestError(null);
      setStep(6);
      setRunToken((t) => t + 1); // kick the ingestion effect
    } catch (err) {
      setStartError(errMsg(err, "Could not start ingestion. Check your sources and try again."));
    } finally {
      setStarting(false);
    }
  }, [sources, bio, primaryKind, personaName]);

  // --- O6: drive the ingestion run (scrape → accept → synthesize) ---------
  // Keyed on runToken so a retry re-runs it. The async loop updates per-source
  // status and the phase checklist; a cancelled flag stops writes after unmount.
  useEffect(() => {
    if (runToken === 0) return;
    let cancelled = false;
    const safeSet = <T,>(setter: (v: T) => void, v: T) => {
      if (!cancelled) setter(v);
    };

    (async () => {
      try {
        // Phase 1 — poll every scrape job to a terminal state.
        safeSet(setPhase, "scraping");
        let working = [...progress];
        for (let tick = 0; tick < POLL_MAX_TICKS; tick++) {
          const pending = working.filter((p) => p.status !== "succeeded" && p.status !== "failed");
          if (pending.length === 0) break;
          await sleep(POLL_INTERVAL_MS);
          if (cancelled) return;
          working = await Promise.all(
            working.map(async (p) => {
              if (p.status === "succeeded" || p.status === "failed") return p;
              try {
                const job = await apiFns.getScrapeJob(p.jobId);
                return { ...p, status: job.status };
              } catch {
                return p; // transient fetch error — keep polling
              }
            }),
          );
          safeSet(setProgress, working);
          if (tick === POLL_MAX_TICKS - 1) {
            throw new Error("Ingestion is taking longer than expected. You can retry in a moment.");
          }
        }
        const failed = working.filter((p) => p.status === "failed");
        const succeeded = working.filter((p) => p.status === "succeeded");
        if (succeeded.length === 0) {
          throw new Error("No sources could be scraped. Edit your sources and try again.");
        }
        if (failed.length > 0) {
          toast({
            tone: "warning",
            title: "Some sources failed",
            description: `${failed.length} source${failed.length === 1 ? "" : "s"} couldn’t be scraped; continuing with the rest.`,
          });
        }

        // Phase 2 — accept the pending scraped sources into the corpus.
        if (cancelled) return;
        safeSet(setPhase, "accepting");
        const pendingSources = (await apiFns.listScrapedSources("pending")).filter(
          (s) => s.personaName === personaName,
        );
        for (const src of pendingSources) {
          if (cancelled) return;
          try {
            await apiFns.acceptSource(src.id);
          } catch {
            // A source that can't be accepted is skipped; synthesis still runs
            // on whatever made it in. If nothing makes it in, synthesize 409s.
          }
        }

        // Phase 3 — synthesize the style profile.
        if (cancelled) return;
        safeSet(setPhase, "synthesizing");
        const sp = await apiFns.synthesizeStyleProfile(personaName);
        if (cancelled) return;
        safeSet(setProfile, sp);
        safeSet(setPhase, "done");
        safeSet(setStep, 7);
      } catch (err) {
        safeSet(setIngestError, errMsg(err, "Ingestion failed. You can retry."));
      }
    })();

    return () => {
      cancelled = true;
    };
    // progress is intentionally read fresh at run start; re-running is driven by
    // runToken (a retry increments it), not by every progress write.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [runToken]);

  const retryIngestion = useCallback(() => {
    setIngestError(null);
    // Reset any failed/succeeded so the run re-polls from scratch.
    setProgress((prev) => prev.map((p) => ({ ...p, status: "queued" as JobStatus })));
    setRunToken((t) => t + 1);
  }, []);

  // --- O7: re-synthesize ---------------------------------------------------
  const resynthesize = useCallback(async () => {
    setResynthesizing(true);
    try {
      const sp = await apiFns.synthesizeStyleProfile(personaName);
      setProfile(sp);
      toast({ tone: "success", title: "Style re-synthesized" });
    } catch (err) {
      const msg =
        err instanceof ApiError && err.status === 429
          ? "You just synthesized — give it a minute before re-running."
          : errMsg(err, "Could not re-synthesize. Try again shortly.");
      toast({ tone: "warning", title: "Re-synthesis failed", description: msg });
    } finally {
      setResynthesizing(false);
    }
  }, [personaName, toast]);

  // --- O8: content test + validate ----------------------------------------
  const runTest = useCallback(
    async (topic: string) => {
      if (!topic) return;
      setTesting(true);
      setTestError(null);
      try {
        const res = await apiFns.runContentTest(personaName, topic);
        setTestResult(res);
      } catch (err) {
        const msg =
          err instanceof ApiError && err.status === 429
            ? "You’re testing quickly — wait a moment and try again."
            : errMsg(err, "Could not generate a sample. Try again.");
        setTestError(msg);
      } finally {
        setTesting(false);
      }
    },
    [personaName],
  );

  const validate = useCallback(async () => {
    setValidating(true);
    setTestError(null);
    try {
      await apiFns.validatePersona(personaName);
      markDone(); // stop the first-run gate from redirecting back into the wizard
      setStep(9);
    } catch (err) {
      setTestError(errMsg(err, "Could not validate your persona. Try again."));
    } finally {
      setValidating(false);
    }
  }, [personaName, markDone]);

  const finish = useCallback(() => {
    markDone();
    navigate("/", { replace: true });
  }, [markDone, navigate]);

  return (
    <WizardFrame step={step}>
      {step === 1 && <StepWelcome onNext={() => setStep(2)} />}
      {step === 2 && (
        <StepPrimaryKind
          value={primaryKind}
          onChange={setPrimaryKind}
          onBack={() => setStep(1)}
          onNext={() => setStep(3)}
        />
      )}
      {step === 3 && (
        <StepSources sources={sources} onChange={setSources} onBack={() => setStep(2)} onNext={() => setStep(4)} />
      )}
      {step === 4 && <StepBio value={bio} onChange={setBio} onBack={() => setStep(3)} onNext={() => setStep(5)} />}
      {step === 5 && (
        <StepReview
          primaryKind={primaryKind}
          sources={sources}
          bio={bio}
          starting={starting}
          error={startError}
          onBack={() => setStep(4)}
          onStart={start}
        />
      )}
      {step === 6 && (
        <StepIngestion
          phase={phase}
          progress={progress}
          error={ingestError}
          onRetry={retryIngestion}
          onBack={() => {
            setIngestError(null);
            setStep(3);
          }}
        />
      )}
      {step === 7 && profile && (
        <StepStyleProfile
          profile={profile}
          resynthesizing={resynthesizing}
          onResynthesize={resynthesize}
          onBack={() => setStep(6)}
          onNext={() => setStep(8)}
        />
      )}
      {step === 8 && (
        <StepContentTest
          result={testResult}
          testing={testing}
          validating={validating}
          error={testError}
          onRunTest={runTest}
          onValidate={validate}
          onBack={() => setStep(7)}
        />
      )}
      {step === 9 && <StepComplete onDone={finish} />}
    </WizardFrame>
  );
}

export default OnboardingWizard;
