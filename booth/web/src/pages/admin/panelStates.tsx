import { Button } from "../../ui";
import { Spinner } from "../../components/Loader";

// Small shared states for the admin panels. Kept dependency-light (no illustration
// assets) so every panel renders a consistent loading/empty/error/forbidden box.

export function PanelLoading() {
  return (
    <div className="flex items-center justify-center gap-2 px-5 py-12 text-sm text-text-muted">
      <Spinner size={16} /> Loading…
    </div>
  );
}

export function PanelEmpty({ label }: { label: string }) {
  return <div className="px-5 py-12 text-center text-sm text-text-muted">{label}</div>;
}

export function PanelError({ message, onRetry }: { message: string; onRetry: () => void }) {
  return (
    <div className="flex flex-col items-center gap-3 px-5 py-12 text-center">
      <p className="text-sm text-text-muted">{message}</p>
      <Button variant="secondary" size="sm" onClick={onRetry}>
        Retry
      </Button>
    </div>
  );
}

// PanelForbidden is the server's authoritative 403 made visible: a DevRel who
// reaches an admin panel (directly, or via a stale nav) sees the denial, not an
// empty table that could be mistaken for "no data" (§4).
export function PanelForbidden() {
  return (
    <div className="rounded-lg border border-dashed border-border-strong bg-surface p-10 text-center">
      <p className="text-sm font-medium text-text">Admin only</p>
      <p className="mt-1 text-sm text-text-muted">
        Your account is not authorised for this section. The server denied the request (403).
      </p>
    </div>
  );
}
