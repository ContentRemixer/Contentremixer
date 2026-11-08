import { Link } from "react-router-dom";

export function NotFoundPage() {
  return (
    <div className="flex flex-col items-center justify-center gap-3 py-20 text-center">
      <p className="bg-fade bg-clip-text text-5xl font-bold text-transparent">404</p>
      <h1 className="text-lg font-semibold text-text">Page not found</h1>
      <p className="max-w-sm text-sm text-text-muted">
        The page you&apos;re looking for doesn&apos;t exist or has moved.
      </p>
      <Link to="/" className="mt-2 rounded-md bg-primary px-4 py-2 text-sm font-semibold text-primary-contrast hover:opacity-90">
        Back to home
      </Link>
    </div>
  );
}
