import { forwardRef, useId } from "react";
import type { InputHTMLAttributes, TextareaHTMLAttributes, ReactNode } from "react";
import * as LabelPrimitive from "@radix-ui/react-label";
import { cx } from "./cx";

const controlBase =
  "w-full rounded-md border border-border bg-surface px-3 text-sm text-text " +
  "placeholder:text-text-muted/70 transition-colors " +
  "hover:border-border-strong " +
  "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary focus-visible:border-primary " +
  "disabled:cursor-not-allowed disabled:opacity-50 " +
  "aria-[invalid=true]:border-warn aria-[invalid=true]:focus-visible:ring-warn";

export const Label = forwardRef<
  HTMLLabelElement,
  React.ComponentPropsWithoutRef<typeof LabelPrimitive.Root>
>(function Label({ className, ...props }, ref) {
  return (
    <LabelPrimitive.Root
      ref={ref}
      className={cx("text-sm font-medium text-text", className)}
      {...props}
    />
  );
});

export interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  invalid?: boolean;
}

export const Input = forwardRef<HTMLInputElement, InputProps>(function Input(
  { className, invalid, ...props },
  ref,
) {
  return (
    <input
      ref={ref}
      aria-invalid={invalid || undefined}
      className={cx(controlBase, "h-10", className)}
      {...props}
    />
  );
});

export interface TextareaProps extends TextareaHTMLAttributes<HTMLTextAreaElement> {
  invalid?: boolean;
}

export const Textarea = forwardRef<HTMLTextAreaElement, TextareaProps>(function Textarea(
  { className, invalid, ...props },
  ref,
) {
  return (
    <textarea
      ref={ref}
      aria-invalid={invalid || undefined}
      className={cx(controlBase, "min-h-[80px] py-2 leading-relaxed", className)}
      {...props}
    />
  );
});

export interface FieldProps {
  label?: ReactNode;
  /** Error message; also flips the control into its invalid state. */
  error?: string;
  /** Helper/description text under the control. */
  hint?: ReactNode;
  required?: boolean;
  className?: string;
  /** Receives the generated id + invalid flag so it can wire up the control. */
  children: (props: { id: string; invalid: boolean }) => ReactNode;
}

// Composes label + control + hint/error with correct a11y wiring. The render-prop
// hands the control a stable id and the invalid flag.
export function Field({ label, error, hint, required, className, children }: FieldProps) {
  const id = useId();
  const invalid = Boolean(error);
  return (
    <div className={cx("flex flex-col gap-1.5", className)}>
      {label ? (
        <Label htmlFor={id}>
          {label}
          {required ? <span className="ml-0.5 text-warn">*</span> : null}
        </Label>
      ) : null}
      {children({ id, invalid })}
      {error ? (
        <p id={`${id}-error`} role="alert" className="text-xs text-warn">
          {error}
        </p>
      ) : hint ? (
        <p id={`${id}-hint`} className="text-xs text-text-muted">
          {hint}
        </p>
      ) : null}
    </div>
  );
}

export default Field;
