import type { ReactNode } from "react";

// Foundation placeholder for screens the feature issues (W3/W5/W6) fill in. Keeps
// the shell navigable and the design-system wiring visible before those land.
export function PagePlaceholder({
  title,
  description,
  issue,
  children,
}: {
  title: string;
  description: string;
  issue?: string;
  children?: ReactNode;
}) {
  return (
    <section>
      <header className="mb-6">
        <h1 className="text-xl font-semibold text-text">{title}</h1>
        <p className="mt-1 text-sm text-text-muted">{description}</p>
      </header>
      <div className="rounded-lg border border-dashed border-border-strong bg-surface p-8 text-center">
        <p className="text-sm text-text-muted">
          This screen is scaffolded and lands in a follow-up issue
          {issue ? <span className="font-medium text-text"> ({issue})</span> : null}.
        </p>
        {children}
      </div>
    </section>
  );
}
