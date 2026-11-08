import type { HTMLAttributes, ReactNode } from "react";
import { cx } from "./cx";

export interface CardProps extends HTMLAttributes<HTMLDivElement> {
  /** Raised surface + glow accent for emphasised panels. */
  raised?: boolean;
  /** Hover affordance for clickable cards. */
  interactive?: boolean;
}

export function Card({ raised, interactive, className, ...rest }: CardProps) {
  return (
    <div
      className={cx(
        "rounded-lg border border-border bg-surface shadow-card",
        raised && "bg-surface-raised",
        interactive && "cursor-pointer transition-colors hover:border-border-strong",
        className,
      )}
      {...rest}
    />
  );
}

export function CardHeader({
  title,
  subtitle,
  action,
  className,
}: {
  title: ReactNode;
  subtitle?: ReactNode;
  action?: ReactNode;
  className?: string;
}) {
  return (
    <div className={cx("flex items-start justify-between gap-3 border-b border-border px-5 py-4", className)}>
      <div className="min-w-0">
        <h3 className="truncate text-sm font-semibold text-text">{title}</h3>
        {subtitle ? <p className="mt-0.5 text-xs text-text-muted">{subtitle}</p> : null}
      </div>
      {action ? <div className="shrink-0">{action}</div> : null}
    </div>
  );
}

export function CardBody({ className, ...rest }: HTMLAttributes<HTMLDivElement>) {
  return <div className={cx("px-5 py-4", className)} {...rest} />;
}

export function CardFooter({ className, ...rest }: HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cx("flex items-center justify-end gap-2 border-t border-border px-5 py-3", className)}
      {...rest}
    />
  );
}

export default Card;
