import { forwardRef } from "react";
import type { ButtonHTMLAttributes, ReactNode } from "react";
import { cx } from "./cx";
import { Spinner } from "../components/Loader";

export type ButtonVariant = "primary" | "secondary" | "ghost" | "danger";
export type ButtonSize = "sm" | "md" | "lg";

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: ButtonSize;
  /** Shows a spinner and disables the button. */
  loading?: boolean;
  /** Icon rendered before the label. */
  iconLeft?: ReactNode;
  /** Icon rendered after the label. */
  iconRight?: ReactNode;
}

// All colour/radius/shadow come from the exported design tokens via Tailwind
// (tokens.css → theme.extend), so the brand is 1:1 and theme-aware — no hex here.
const base =
  "inline-flex items-center justify-center gap-2 font-medium whitespace-nowrap " +
  "rounded-md transition-colors select-none " +
  "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary focus-visible:ring-offset-2 " +
  "focus-visible:ring-offset-bg disabled:pointer-events-none disabled:opacity-50";

const variants: Record<ButtonVariant, string> = {
  primary: "bg-primary text-primary-contrast hover:opacity-90 active:opacity-100 shadow-card",
  secondary: "bg-surface-2 text-text border border-border hover:border-border-strong",
  ghost: "bg-transparent text-text-muted hover:bg-surface-2 hover:text-text",
  danger: "bg-warn text-white hover:opacity-90",
};

const sizes: Record<ButtonSize, string> = {
  sm: "h-8 px-3 text-sm",
  md: "h-10 px-4 text-sm",
  lg: "h-11 px-5 text-base",
};

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant = "primary", size = "md", loading, iconLeft, iconRight, className, children, disabled, ...rest },
  ref,
) {
  return (
    <button
      ref={ref}
      disabled={disabled || loading}
      className={cx(base, variants[variant], sizes[size], className)}
      {...rest}
    >
      {loading ? <Spinner size={size === "lg" ? 18 : 16} /> : iconLeft}
      {children}
      {!loading && iconRight}
    </button>
  );
});

export default Button;
