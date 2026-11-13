import type { ReactNode } from "react";
import { cx } from "../../ui";
import { CheckIcon, SparkleIcon } from "../../components/icons";

// The nine onboarding steps (O1–O9, plan ISI-7350 §6). The rail renders them in
// order with done/current/upcoming state; the titles double as the review
// summary headings.
export const STEP_TITLES = [
  "Welcome",
  "Primary content",
  "Add sources",
  "About you",
  "Review & start",
  "Ingestion",
  "Your style",
  "Content test",
  "Done",
] as const;

export const TOTAL_STEPS = STEP_TITLES.length;

// WizardFrame is the full-screen onboarding chrome: a brand header, a vertical
// step rail (desktop) / compact progress (mobile), and the active step's content.
// It replaces the normal AppShell for the first-run flow (no sidebar, no nav).
export function WizardFrame({ step, children }: { step: number; children: ReactNode }) {
  return (
    <div className="flex min-h-full flex-col bg-bg text-text">
      <header className="flex items-center gap-2 border-b border-border px-6 py-4">
        <span className="text-accent">
          <SparkleIcon width={20} height={20} />
        </span>
        <span className="text-sm font-semibold">ContentRemixer · DevRel setup</span>
        <span className="ml-auto text-xs text-text-muted">
          Step {Math.min(step, TOTAL_STEPS)} of {TOTAL_STEPS}
        </span>
      </header>

      <div className="mx-auto flex w-full max-w-5xl flex-1 gap-8 px-4 py-8 sm:px-6">
        <StepRail step={step} />
        <main className="min-w-0 flex-1">{children}</main>
      </div>
    </div>
  );
}

function StepRail({ step }: { step: number }) {
  return (
    <nav aria-label="Onboarding progress" className="hidden w-48 shrink-0 lg:block">
      <ol className="space-y-1">
        {STEP_TITLES.map((title, i) => {
          const n = i + 1;
          const done = n < step;
          const current = n === step;
          return (
            <li key={title}>
              <div
                className={cx(
                  "flex items-center gap-3 rounded-md px-2 py-1.5 text-sm",
                  current && "bg-surface-2 font-medium text-text",
                  !current && "text-text-muted",
                )}
                aria-current={current ? "step" : undefined}
              >
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
                <span className="truncate">{title}</span>
              </div>
            </li>
          );
        })}
      </ol>
    </nav>
  );
}

// StepScaffold is the shared per-step layout: a heading + lead paragraph, the
// step body, and a sticky footer with Back / primary actions.
export function StepScaffold({
  title,
  lead,
  children,
  footer,
}: {
  title: string;
  lead?: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
}) {
  return (
    <section className="flex min-h-full flex-col">
      <header className="mb-6">
        <h1 className="text-xl font-semibold text-text">{title}</h1>
        {lead ? <p className="mt-1.5 text-sm text-text-muted">{lead}</p> : null}
      </header>
      <div className="flex-1">{children}</div>
      {footer ? (
        <footer className="mt-8 flex items-center justify-between gap-3 border-t border-border pt-5">
          {footer}
        </footer>
      ) : null}
    </section>
  );
}
