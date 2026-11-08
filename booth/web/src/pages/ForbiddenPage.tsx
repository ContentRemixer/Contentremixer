import { Link } from "react-router-dom";

// A3 403: the server denied an admin-only surface to a DevRel session (§4). The
// client never trusts role nav — this is what a direct hit to a gated route shows.
export function ForbiddenPage() {
  return (
    <div className="flex flex-col items-center justify-center gap-3 py-20 text-center">
      <p className="bg-fade bg-clip-text text-5xl font-bold text-transparent">403</p>
      <h1 className="text-lg font-semibold text-text">Access denied</h1>
      <p className="max-w-sm text-sm text-text-muted">
        You don&apos;t have permission to view this page. If you believe this is a
        mistake, contact an administrator.
      </p>
      <Link to="/" className="mt-2 rounded-md bg-primary px-4 py-2 text-sm font-semibold text-primary-contrast hover:opacity-90">
        Back to home
      </Link>
    </div>
  );
}
