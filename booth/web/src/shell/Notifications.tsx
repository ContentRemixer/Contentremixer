import { BellIcon } from "../components/icons";
import { useDismissable } from "./useDismissable";

// Shell notifications affordance (A2). Foundation stage ships the bell + empty
// panel; the live feed (review requests, illustration assignments, scrape-job
// completions) is wired by the feature issues (W3/W5/W6) against the API.
export function Notifications() {
  const { ref, open, toggle } = useDismissable<HTMLDivElement>();

  return (
    <div className="relative" ref={ref}>
      <button
        type="button"
        onClick={toggle}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label="Notifications"
        className="relative grid h-9 w-9 place-items-center rounded-md text-text-muted hover:bg-surface-2 hover:text-text transition-colors"
      >
        <BellIcon size={18} />
      </button>

      {open ? (
        <div
          role="menu"
          className="absolute right-0 z-30 mt-2 w-80 overflow-hidden rounded-lg border border-border bg-surface-raised shadow-card"
        >
          <div className="border-b border-border px-4 py-3 text-sm font-semibold text-text">
            Notifications
          </div>
          <div className="px-4 py-10 text-center text-sm text-text-muted">
            You&apos;re all caught up.
          </div>
        </div>
      ) : null}
    </div>
  );
}
