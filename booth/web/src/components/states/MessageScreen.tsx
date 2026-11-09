import type { ReactNode } from "react";

// Shared full-viewport message layout for the A3 terminal states (403 / 404 /
// session-expired / generic error). One layout keeps the states visually
// consistent; each page supplies the big gradient code, heading, copy, and the
// recovery action(s). Used for screens that replace the page, not inline slots
// (those use EmptyState).
export function MessageScreen({
  code,
  title,
  children,
  actions,
}: {
  code?: string;
  title: string;
  children?: ReactNode;
  actions?: ReactNode;
}) {
  return (
    <div className="flex min-h-full flex-col items-center justify-center gap-3 bg-bg px-4 py-20 text-center">
      {code ? (
        <p className="bg-fade bg-clip-text text-5xl font-bold text-transparent">{code}</p>
      ) : null}
      <h1 className="text-lg font-semibold text-text">{title}</h1>
      {children ? <div className="max-w-sm text-sm text-text-muted">{children}</div> : null}
      {actions ? <div className="mt-2 flex flex-wrap items-center justify-center gap-3">{actions}</div> : null}
    </div>
  );
}
