import { useEffect, useRef, useState, type RefObject } from "react";

// Small popover-dismissal hook used by the user menu and notifications dropdown:
// closes on outside click or Escape. (W7's component lib may swap these ad-hoc
// menus for Radix primitives; the shell contract — a labelled trigger + a
// dismissable panel — stays the same.)
export function useDismissable<T extends HTMLElement>(): {
  ref: RefObject<T>;
  open: boolean;
  setOpen: (v: boolean) => void;
  toggle: () => void;
} {
  const ref = useRef<T>(null);
  const [open, setOpen] = useState(false);

  useEffect(() => {
    if (!open) return;
    function onPointer(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") setOpen(false);
    }
    document.addEventListener("mousedown", onPointer);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onPointer);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  return { ref, open, setOpen, toggle: () => setOpen((v) => !v) };
}
