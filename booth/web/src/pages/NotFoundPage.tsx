import { Link } from "react-router-dom";
import { MessageScreen } from "../components/states/MessageScreen";

// A3 404: the route didn't match any known screen.
export function NotFoundPage() {
  return (
    <MessageScreen
      code="404"
      title="Page not found"
      actions={
        <Link
          to="/"
          className="rounded-md bg-primary px-4 py-2 text-sm font-semibold text-primary-contrast hover:opacity-90"
        >
          Back to home
        </Link>
      }
    >
      The page you&apos;re looking for doesn&apos;t exist or has moved.
    </MessageScreen>
  );
}
