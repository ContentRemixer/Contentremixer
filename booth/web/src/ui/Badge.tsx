import type { HTMLAttributes } from "react";
import { cx } from "./cx";

export type BadgeTone =
  | "neutral"
  | "primary"
  | "accent"
  | "success"
  | "warning"
  | "info";

export interface BadgeProps extends HTMLAttributes<HTMLSpanElement> {
  tone?: BadgeTone;
  /** Renders a leading status dot. */
  dot?: boolean;
}

// Tones are soft, tinted surfaces built from the semantic palette (uses
// color-mix so every tone follows the active theme without extra tokens).
const tones: Record<BadgeTone, string> = {
  neutral: "bg-surface-2 text-text-muted border border-border",
  primary: "bg-primary/10 text-primary border border-primary/20",
  accent: "bg-accent/10 text-accent border border-accent/20",
  success: "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border border-emerald-500/20",
  warning: "bg-warn/10 text-warn border border-warn/25",
  info: "bg-sky-500/10 text-sky-600 dark:text-sky-400 border border-sky-500/20",
};

const dotColors: Record<BadgeTone, string> = {
  neutral: "bg-text-muted",
  primary: "bg-primary",
  accent: "bg-accent",
  success: "bg-emerald-500",
  warning: "bg-warn",
  info: "bg-sky-500",
};

export function Badge({ tone = "neutral", dot, className, children, ...rest }: BadgeProps) {
  return (
    <span
      className={cx(
        "inline-flex items-center gap-1.5 rounded-pill px-2.5 py-0.5 text-xs font-medium",
        tones[tone],
        className,
      )}
      {...rest}
    >
      {dot ? <span className={cx("h-1.5 w-1.5 rounded-pill", dotColors[tone])} /> : null}
      {children}
    </span>
  );
}

export default Badge;
