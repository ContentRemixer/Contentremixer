import { useCallback, useEffect, useState, type ReactNode } from "react";
import { Button, cx, useToast } from "../../ui";
import { CheckIcon, CloseIcon, ExternalLinkIcon, RefreshIcon, SparkleIcon } from "../../components/icons";
import { api, ApiError } from "../../app/api";
import type { CrObject } from "./CrPanel";
import { StepScaffold } from "../onboarding/WizardFrame";
import {
  ReferenceRepoDialog,
  InstallGithubAppButton,
  SeedTemplateIssuesButton,
  isAppInstalled,
} from "./ReferenceRepoForm";

// RepoOnboardWizard — W4 of the repo onboarding (ISI-7897, plan ISI-7887). It
// ties W1–W3 into one guided, full-screen flow for an Admin standing up a new
// reference repo:
//   ① repo details        → the existing bespoke create form (ISI-7782)
//   ② install GitHub App   → the W2 install step (ISI-7895, status.appInstalled)
//   ③ seed template issues → the W3 idempotent seeder (ISI-7896)
//   ④ verify first recycle PR → a deep link + checklist (manual verification)
//
// Nothing here re-implements the step actions: each step hosts the exact
// component the flat Admin panel already uses, so there is one source of truth
// for validation, endpoints, and error handling. The wizard only sequences them,
// tracks progress, and carries the created repo forward between steps.
//
// Degrade-never-trap: steps ②–④ are advisory, not gates. The App-install webhook
// (ISI-7895) and the recycle PR arrive asynchronously and may not be observable
// in a test cluster, so the wizard lets the Admin proceed with a soft hint
// rather than stranding them on a step whose signal never flips.

const PATH = "/v1/referencerepos";
const STEPS = ["Repo details", "Install GitHub App", "Seed template issues", "Verify first PR"] as const;
const TOTAL = STEPS.length;

// OnboardRepoButton is the Admin entry point: it sits beside "New" in the
// ReferenceRepo panel header and launches the wizard as a full-screen overlay.
export function OnboardRepoButton({ onComplete }: { onComplete: () => void }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button variant="secondary" size="sm" onClick={() => setOpen(true)}>
        Onboard a repo
      </Button>
      {open && (
        <RepoOnboardWizard
          onClose={() => setOpen(false)}
          onComplete={() => {
            setOpen(false);
            onComplete();
          }}
        />
      )}
    </>
  );
}

function RepoOnboardWizard({ onClose, onComplete }: { onClose: () => void; onComplete: () => void }) {
  const { toast } = useToast();
  const [step, setStep] = useState(1);
  const [repoName, setRepoName] = useState<string | null>(null);
  const [repo, setRepo] = useState<CrObject | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const [reloading, setReloading] = useState(false);

  // loadRepo re-reads the CrObject so steps ②–④ see the current status
  // (appInstalled flips asynchronously via the installation webhook). A failed
  // read keeps the prior value rather than blanking the step.
  const loadRepo = useCallback(
    async (name: string) => {
      setReloading(true);
      try {
        setRepo(await api.get<CrObject>(`${PATH}/${encodeURIComponent(name)}`));
      } catch (e) {
        toast({
          title: "Could not refresh repo status",
          description: e instanceof ApiError ? e.message : "request failed",
          tone: "warning",
        });
      } finally {
        setReloading(false);
      }
    },
    [toast],
  );

  // After create, load once so step ② has the object immediately on advance.
  useEffect(() => {
    if (repoName) void loadRepo(repoName);
  }, [repoName, loadRepo]);

  const next = () => setStep((s) => Math.min(s + 1, TOTAL));
  const back = () => setStep((s) => Math.max(s - 1, 1));
  const repoSlug = typeof repo?.spec?.repo === "string" ? (repo.spec.repo as string) : null;

  return (
    <div className="fixed inset-0 z-50 flex flex-col bg-bg text-text">
      <header className="flex items-center gap-2 border-b border-border px-6 py-4">
        <span className="text-accent">
          <SparkleIcon width={20} height={20} />
        </span>
        <span className="text-sm font-semibold">ContentRemixer · Onboard a reference repo</span>
        <span className="ml-auto mr-4 text-xs text-text-muted">
          Step {Math.min(step, TOTAL)} of {TOTAL}
        </span>
        <Button variant="ghost" size="sm" onClick={onClose} aria-label="Close onboarding wizard">
          <CloseIcon width={16} height={16} />
        </Button>
      </header>

      <div className="mx-auto flex w-full max-w-3xl flex-1 flex-col overflow-y-auto px-4 py-8 sm:px-6">
        <StepIndicator step={step} />
        <div className="mt-8 flex-1">
          {step === 1 && (
            <RepoDetailsStep
              repoName={repoName}
              createOpen={createOpen}
              setCreateOpen={setCreateOpen}
              onCreated={(name) => {
                setRepoName(name);
                setCreateOpen(false);
                next();
              }}
              footer={
                <>
                  <span />
                  <Button onClick={next} disabled={!repoName}>
                    Next
                  </Button>
                </>
              }
            />
          )}

          {step === 2 && (
            <InstallStep
              repo={repo}
              reloading={reloading}
              onRefresh={() => repoName && loadRepo(repoName)}
              footer={<NavFooter onBack={back} onNext={next} />}
            />
          )}

          {step === 3 && (
            <SeedStep repo={repo} footer={<NavFooter onBack={back} onNext={next} />} />
          )}

          {step === 4 && (
            <VerifyStep
              repoSlug={repoSlug}
              footer={
                <>
                  <Button variant="ghost" onClick={back}>
                    Back
                  </Button>
                  <Button onClick={onComplete}>Finish</Button>
                </>
              }
            />
          )}
        </div>
      </div>
    </div>
  );
}

// StepIndicator is the compact horizontal progress rail (the full-screen chrome
// has no sidebar). It mirrors the done/current/upcoming styling of the DevRel
// WizardFrame's vertical rail so the two onboarding flows read as one system.
function StepIndicator({ step }: { step: number }) {
  return (
    <nav aria-label="Onboarding progress">
      <ol className="flex flex-wrap items-center gap-x-2 gap-y-2">
        {STEPS.map((title, i) => {
          const n = i + 1;
          const done = n < step;
          const current = n === step;
          return (
            <li key={title} className="flex items-center gap-2">
              <span
                className={cx(
                  "flex h-6 w-6 shrink-0 items-center justify-center rounded-full border text-xs",
                  done && "border-accent bg-accent text-primary-contrast",
                  current && "border-accent text-accent",
                  !done && !current && "border-border text-text-muted",
                )}
              >
                {done ? <CheckIcon width={14} height={14} /> : n}
              </span>
              <span
                className={cx(
                  "text-sm",
                  current ? "font-medium text-text" : "text-text-muted",
                )}
                aria-current={current ? "step" : undefined}
              >
                {title}
              </span>
              {n < TOTAL && <span className="mx-1 h-px w-6 bg-border" aria-hidden />}
            </li>
          );
        })}
      </ol>
    </nav>
  );
}

// NavFooter is the shared Back / Next footer for the advisory middle steps.
function NavFooter({ onBack, onNext }: { onBack: () => void; onNext: () => void }) {
  return (
    <>
      <Button variant="ghost" onClick={onBack}>
        Back
      </Button>
      <Button onClick={onNext}>Next</Button>
    </>
  );
}

function RepoDetailsStep({
  repoName,
  createOpen,
  setCreateOpen,
  onCreated,
  footer,
}: {
  repoName: string | null;
  createOpen: boolean;
  setCreateOpen: (v: boolean) => void;
  onCreated: (name: string) => void;
  footer: ReactNode;
}) {
  return (
    <StepScaffold
      title="Repo details"
      lead="Register the GitHub repository and its PR target. Secret values are provisioned separately — this step only references them, exactly like the Admin panel's New form."
      footer={footer}
    >
      {repoName ? (
        <div className="flex items-center gap-2 rounded-md border border-border bg-surface-2 px-3 py-2 text-sm">
          <span className="text-accent">
            <CheckIcon width={16} height={16} />
          </span>
          <span>
            Created reference repo <span className="font-medium text-text">{repoName}</span>.
          </span>
        </div>
      ) : (
        <Button onClick={() => setCreateOpen(true)}>Create reference repo</Button>
      )}
      {createOpen && (
        <ReferenceRepoDialog
          mode="create"
          repo={null}
          path={PATH}
          onClose={() => setCreateOpen(false)}
          onSaved={(name) => onCreated(name)}
        />
      )}
    </StepScaffold>
  );
}

function InstallStep({
  repo,
  reloading,
  onRefresh,
  footer,
}: {
  repo: CrObject | null;
  reloading: boolean;
  onRefresh: () => void;
  footer: ReactNode;
}) {
  const installed = repo ? isAppInstalled(repo) : false;
  return (
    <StepScaffold
      title="Install the GitHub App"
      lead="Install the ContentRemixer recycler App on this repository. Installing happens on GitHub; the status below flips once GitHub delivers the installation webhook, so re-check after installing."
      footer={footer}
    >
      {repo ? (
        <div className="space-y-4">
          <div className="flex flex-wrap items-center gap-3">
            <InstallGithubAppButton repo={repo} />
            <Button variant="ghost" size="sm" onClick={onRefresh} loading={reloading}>
              <RefreshIcon width={14} height={14} />
              <span className="ml-1.5">Check status</span>
            </Button>
          </div>
          {!installed && (
            <p className="text-sm text-text-muted">
              Not detected yet — the webhook can take a moment, and may not reach a test cluster. You can
              continue and verify later.
            </p>
          )}
        </div>
      ) : (
        <RepoUnavailable />
      )}
    </StepScaffold>
  );
}

function SeedStep({ repo, footer }: { repo: CrObject | null; footer: ReactNode }) {
  return (
    <StepScaffold
      title="Seed template issues"
      lead="Seed the versioned content-template pack into the repo as GitHub issues. This is idempotent — templates already present are skipped — so it is safe to run more than once. Requires a GitHub token on the repo."
      footer={footer}
    >
      {repo ? <SeedTemplateIssuesButton repo={repo} /> : <RepoUnavailable />}
    </StepScaffold>
  );
}

function VerifyStep({ repoSlug, footer }: { repoSlug: string | null; footer: ReactNode }) {
  const prsUrl = repoSlug ? `https://github.com/${repoSlug}/pulls` : null;
  return (
    <StepScaffold
      title="Verify the first recycle PR"
      lead="Once a seeded issue is picked up, the recycler opens a pull request against your PR target. Confirm the end-to-end flow below, then finish."
      footer={footer}
    >
      <ol className="list-decimal space-y-2 pl-5 text-sm text-text-muted">
        <li>Open a seeded template issue on the repo and let the recycler pick it up.</li>
        <li>Watch for a branch under your configured prefix and a PR against the base branch.</li>
        <li>Review the PR — its presence confirms the App, token, and routing are all wired.</li>
      </ol>
      {prsUrl && (
        <a
          href={prsUrl}
          target="_blank"
          rel="noopener noreferrer"
          className="mt-4 inline-flex items-center gap-1.5 text-sm font-medium text-accent hover:underline"
        >
          Open pull requests on GitHub
          <ExternalLinkIcon width={14} height={14} />
        </a>
      )}
    </StepScaffold>
  );
}

function RepoUnavailable() {
  return (
    <p className="text-sm text-text-muted">
      The repo isn’t loaded yet. Go back to step 1, or close and reopen the wizard.
    </p>
  );
}
