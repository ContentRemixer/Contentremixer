import { Link } from "react-router-dom";
import { EmptyState } from "../components/states/EmptyState";
import { SkeletonList } from "../components/states/Skeleton";
import { AlertTriangleIcon, ClockIcon, InboxIcon, LockIcon } from "../components/icons";

// A3 system-states reference (W1). A single screen that renders every shared
// state primitive — loading skeleton, empty, session-expired, 403, 404, error —
// so they can be design-reviewed (Graphic Designer be1ec900) in both themes and
// so the W3/W5/W6 screens have a living catalogue to reuse. Served behind auth.
function Section({ title, note, children }: { title: string; note: string; children: React.ReactNode }) {
  return (
    <section className="mb-10">
      <h2 className="text-sm font-semibold text-text">{title}</h2>
      <p className="mb-3 text-xs text-text-muted">{note}</p>
      {children}
    </section>
  );
}

function MiniScreen({
  icon: Icon,
  code,
  title,
  children,
}: {
  icon?: React.ComponentType<{ size?: number; className?: string }>;
  code?: string;
  title: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex flex-col items-center justify-center gap-2 rounded-lg border border-border bg-surface px-4 py-10 text-center shadow-card">
      {code ? <p className="bg-fade bg-clip-text text-4xl font-bold text-transparent">{code}</p> : null}
      {Icon ? (
        <span className="flex h-11 w-11 items-center justify-center rounded-full bg-surface-2 text-text-muted">
          <Icon size={22} />
        </span>
      ) : null}
      <p className="text-sm font-semibold text-text">{title}</p>
      <p className="max-w-xs text-sm text-text-muted">{children}</p>
    </div>
  );
}

export function StatesPage() {
  return (
    <section>
      <header className="mb-6">
        <h1 className="text-xl font-semibold text-text">System states (A3)</h1>
        <p className="mt-1 text-sm text-text-muted">
          Reference catalogue of the shared loading, empty, and error states. Toggle the theme in
          the top bar to review light and dark.
        </p>
      </header>

      <Section title="Loading skeleton" note="Shown while a fetch is in flight — reserves layout instead of a spinner flash.">
        <SkeletonList count={3} label="Loading sources…" />
      </Section>

      <Section title="Empty" note="A list that loaded but has no rows yet.">
        <EmptyState
          icon={InboxIcon}
          title="No scraped sources yet"
          description="Sources you enqueue for scraping will appear here for review before entering the corpus."
          action={
            <span className="rounded-md bg-primary px-4 py-2 text-sm font-semibold text-primary-contrast">
              Add a source
            </span>
          }
        />
      </Section>

      <Section title="Terminal states" note="Full-screen states a route can land on.">
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <MiniScreen icon={ClockIcon} title="Session expired">
            A 401 from any call ends the session and returns to sign-in.
          </MiniScreen>
          <MiniScreen icon={LockIcon} code="403" title="Access denied">
            An admin-only surface hit by a DevRel session.
          </MiniScreen>
          <MiniScreen code="404" title="Page not found">
            The route didn&apos;t match any known screen.
          </MiniScreen>
          <MiniScreen icon={AlertTriangleIcon} title="Something went wrong">
            A non-auth error; retry or head back home.
          </MiniScreen>
        </div>
      </Section>

      <Link to="/" className="text-sm font-medium text-primary hover:underline">
        ← Back to home
      </Link>
    </section>
  );
}
