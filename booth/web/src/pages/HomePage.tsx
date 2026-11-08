import { useAuth } from "../app/auth";

export function HomePage() {
  const { session } = useAuth();
  return (
    <section>
      <header className="mb-6">
        <h1 className="text-xl font-semibold text-text">
          Welcome{session ? `, ${session.sub}` : ""}
        </h1>
        <p className="mt-1 text-sm text-text-muted">
          ContentRemixer console — create once, publish everywhere.
        </p>
      </header>

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <div className="rounded-lg border border-border bg-surface p-5 shadow-card">
          <p className="text-sm font-medium text-text">Content</p>
          <p className="mt-1 text-sm text-text-muted">Draft, review, and publish articles.</p>
        </div>
        <div className="rounded-lg border border-border bg-surface p-5 shadow-card">
          <p className="text-sm font-medium text-text">Scraper review</p>
          <p className="mt-1 text-sm text-text-muted">Vet scraped sources before they enter the corpus.</p>
        </div>
        {session?.role === "Admin" ? (
          <div className="rounded-lg border border-border bg-surface p-5 shadow-card">
            <p className="text-sm font-medium text-text">Admin</p>
            <p className="mt-1 text-sm text-text-muted">Personas, repos, models, users &amp; roles.</p>
          </div>
        ) : null}
      </div>
    </section>
  );
}
