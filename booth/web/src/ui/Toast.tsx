import { createContext, useCallback, useContext, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import * as Primitive from "@radix-ui/react-toast";
import { cx } from "./cx";
import { CloseIcon } from "../components/icons";

export type ToastTone = "neutral" | "success" | "warning" | "info";

export interface ToastOptions {
  title: ReactNode;
  description?: ReactNode;
  tone?: ToastTone;
  /** Auto-dismiss in ms; 0 keeps it until dismissed. */
  duration?: number;
}

interface ToastEntry extends ToastOptions {
  id: number;
}

interface ToastApi {
  toast: (opts: ToastOptions) => void;
}

const ToastContext = createContext<ToastApi | null>(null);

const accent: Record<ToastTone, string> = {
  neutral: "border-l-border-strong",
  success: "border-l-emerald-500",
  warning: "border-l-warn",
  info: "border-l-sky-500",
};

// Mount <ToastProvider> once near the app root; call useToast().toast(...) anywhere.
export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<ToastEntry[]>([]);
  // Deterministic monotonic ids (no Date.now) so SSR/tests stay stable.
  const seq = useRef(0);

  const toast = useCallback((opts: ToastOptions) => {
    const id = (seq.current += 1);
    setItems((prev) => [...prev, { id, tone: "neutral", duration: 5000, ...opts }]);
  }, []);

  const dismiss = useCallback((id: number) => {
    setItems((prev) => prev.filter((t) => t.id !== id));
  }, []);

  const api = useMemo(() => ({ toast }), [toast]);

  return (
    <ToastContext.Provider value={api}>
      <Primitive.Provider swipeDirection="right">
        {children}
        {items.map((t) => (
          <Primitive.Root
            key={t.id}
            duration={t.duration}
            onOpenChange={(open) => !open && dismiss(t.id)}
            className={cx(
              "flex items-start gap-3 rounded-md border border-l-4 border-border bg-surface-raised px-4 py-3 shadow-card",
              "data-[swipe=move]:translate-x-[var(--radix-toast-swipe-move-x)] data-[state=closed]:opacity-0",
              accent[t.tone ?? "neutral"],
            )}
          >
            <div className="min-w-0 flex-1">
              <Primitive.Title className="text-sm font-semibold text-text">{t.title}</Primitive.Title>
              {t.description ? (
                <Primitive.Description className="mt-0.5 text-xs text-text-muted">
                  {t.description}
                </Primitive.Description>
              ) : null}
            </div>
            <Primitive.Close
              aria-label="Dismiss"
              className="grid h-6 w-6 shrink-0 place-items-center rounded-sm text-text-muted hover:bg-surface-2 hover:text-text"
            >
              <CloseIcon size={14} />
            </Primitive.Close>
          </Primitive.Root>
        ))}
        <Primitive.Viewport className="fixed bottom-4 right-4 z-[60] flex w-[min(92vw,22rem)] flex-col gap-2 outline-none" />
      </Primitive.Provider>
    </ToastContext.Provider>
  );
}

export function useToast(): ToastApi {
  const ctx = useContext(ToastContext);
  if (!ctx) throw new Error("useToast must be used within <ToastProvider>");
  return ctx;
}
