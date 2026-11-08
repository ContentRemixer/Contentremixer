import { forwardRef } from "react";
import type { ReactNode } from "react";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import { cx } from "./cx";
import { CloseIcon } from "../components/icons";

// Accessible modal built on Radix Dialog (focus trap, ESC, scroll-lock,
// aria wiring) with brand tokens applied. Re-exports the primitive parts so
// callers can compose trigger/close as needed.
export const Dialog = DialogPrimitive.Root;
export const DialogTrigger = DialogPrimitive.Trigger;
export const DialogClose = DialogPrimitive.Close;

export const DialogContent = forwardRef<
  React.ElementRef<typeof DialogPrimitive.Content>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Content> & {
    title?: ReactNode;
    description?: ReactNode;
    /** Hide the default close (×) button. */
    hideClose?: boolean;
  }
>(function DialogContent({ className, children, title, description, hideClose, ...props }, ref) {
  return (
    <DialogPrimitive.Portal>
      <DialogPrimitive.Overlay className="fixed inset-0 z-50 bg-black/50 backdrop-blur-sm" />
      <DialogPrimitive.Content
        ref={ref}
        className={cx(
          "fixed left-1/2 top-1/2 z-50 w-[min(94vw,32rem)] -translate-x-1/2 -translate-y-1/2",
          "rounded-lg border border-border bg-surface shadow-card focus:outline-none",
          className,
        )}
        {...props}
      >
        {(title || description) && (
          <div className="border-b border-border px-5 py-4">
            {title ? (
              <DialogPrimitive.Title className="text-base font-semibold text-text">
                {title}
              </DialogPrimitive.Title>
            ) : null}
            {description ? (
              <DialogPrimitive.Description className="mt-1 text-sm text-text-muted">
                {description}
              </DialogPrimitive.Description>
            ) : null}
          </div>
        )}
        {children}
        {!hideClose && (
          <DialogPrimitive.Close
            aria-label="Close"
            className="absolute right-4 top-4 grid h-8 w-8 place-items-center rounded-md text-text-muted hover:bg-surface-2 hover:text-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary"
          >
            <CloseIcon size={18} />
          </DialogPrimitive.Close>
        )}
      </DialogPrimitive.Content>
    </DialogPrimitive.Portal>
  );
});

export function DialogBody({ className, ...rest }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cx("px-5 py-4", className)} {...rest} />;
}

export function DialogFooter({ className, ...rest }: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cx("flex items-center justify-end gap-2 border-t border-border px-5 py-3", className)}
      {...rest}
    />
  );
}
