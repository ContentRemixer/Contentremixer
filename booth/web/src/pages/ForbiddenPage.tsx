import { Link } from "react-router-dom";
import { MessageScreen } from "../components/states/MessageScreen";

// A3 403: the server denied an admin-only surface to a DevRel session (§4). The
// client never trusts role nav — this is what a direct hit to a gated route shows.
export function ForbiddenPage() {
  return (
    <MessageScreen
      code="403"
      title="Access denied"
      actions={
        <Link
          to="/"
          className="rounded-md bg-primary px-4 py-2 text-sm font-semibold text-primary-contrast hover:opacity-90"
        >
          Back to home
        </Link>
      }
    >
      You don&apos;t have permission to view this page. If you believe this is a
      mistake, contact an administrator.
    </MessageScreen>
  );
}
