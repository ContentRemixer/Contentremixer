import type { ComponentType, ReactNode } from "react";
import { InboxIcon } from "../icons";

type IconComponent = ComponentType<{ size?: number; className?: string }>;

// A3 empty state: an inline slot for a list/section that loaded successfully but
// has no rows yet (e.g. no scraped sources, no content, no users). Distinct from
// a loading skeleton (data still arriving) and from an error screen (the call
// failed). Reused by the W3/W5/W6 data screens.
export function EmptyState({
  icon: Icon = InboxIcon,
  title,
  description,
  action,
}: {
  icon?: IconComponent;
  title: string;
  description?: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className="flex flex-col items-center justify-center gap-3 rounded-lg border border-dashed border-border bg-surface/40 px-6 py-14 text-center">
      <span className="flex h-12 w-12 items-center justify-center rounded-full bg-surface-2 text-text-muted">
        <Icon size={24} />
      </span>
      <p className="text-sm font-semibold text-text">{title}</p>
      {description ? <p className="max-w-sm text-sm text-text-muted">{description}</p> : null}
      {action ? <div className="mt-1">{action}</div> : null}
    </div>
  );
}
