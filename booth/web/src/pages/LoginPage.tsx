import { useEffect, useState, type FormEvent } from "react";
import { Navigate, useLocation, useNavigate } from "react-router-dom";
import { api, ApiError } from "../app/api";
import { useAuth, type Session } from "../app/auth";
import { useBootstrapStatus } from "../app/bootstrap";
import { Logo } from "../components/Logo";
import { Spinner } from "../components/Loader";
import { ClockIcon } from "../components/icons";

// A1 login + first-run Admin bootstrap (W1). One surface, three modes:
//   • normal      — sign in to the console.
//   • session-expired — reached here because a 401 tore down a live session
//                   (auth.expired); shows the A3 session-expired notice.
//   • first-run   — a freshly deployed Booth whose seed Admin (provisioned from
//                   the deployment Secret) has never signed in; shows a welcome
//                   panel, prefills the seed username, nudges a password change.
// Already authenticated → bounce to the app (or the route the user was headed to).
export function LoginPage() {
  const { status, expired, refresh, clearExpired } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  // Probe first-run only while anonymous (no point once authenticated/redirecting).
  const bootstrap = useBootstrapStatus(status === "anonymous");
  const firstRun = bootstrap.state === "ready" && bootstrap.status.firstRun;
  const seedUsername = bootstrap.state === "ready" ? bootstrap.status.adminUsername : "";

  // Prefill the seed Admin username once the first-run probe resolves, so the
  // operator only needs the secret-provisioned password.
  useEffect(() => {
    if (firstRun && seedUsername) setUsername((u) => (u === "" ? seedUsername : u));
  }, [firstRun, seedUsername]);

  if (status === "authenticated") {
    const from = (location.state as { from?: string } | null)?.from;
    return <Navigate to={from && from !== "/login" ? from : "/"} replace />;
  }

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      await api.post<Session>("/v1/auth/login", { username, password });
      clearExpired();
      await refresh();
      navigate("/", { replace: true });
    } catch (err) {
      setError(
        err instanceof ApiError && err.status === 401
          ? "Invalid username or password."
          : "Sign-in failed. Try again.",
      );
    } finally {
      setSubmitting(false);
    }
  }

  // Hold the card until the first-run probe resolves so we don't flash the normal
  // form then swap to the welcome variant.
  const probing = status === "anonymous" && bootstrap.state === "loading";

  return (
    <div className="flex min-h-full items-center justify-center bg-bg px-4 py-12">
      <div className="w-full max-w-sm">
        <div className="mb-8 flex justify-center">
          <Logo height={36} />
        </div>

        {expired ? (
          <div
            role="status"
            className="mb-4 flex items-start gap-2.5 rounded-md border border-border bg-surface-2 px-3 py-2.5 text-sm text-text-muted"
          >
            <ClockIcon size={18} className="mt-0.5 shrink-0 text-warn" />
            <span>
              <span className="font-medium text-text">Your session expired.</span> Sign in
              again to pick up where you left off.
            </span>
          </div>
        ) : null}

        <form onSubmit={onSubmit} className="rounded-lg border border-border bg-surface p-6 shadow-card">
          {probing ? (
            <div className="flex flex-col items-center justify-center gap-3 py-10 text-text-muted">
              <Spinner size={24} />
              <p className="text-sm">Preparing sign-in…</p>
            </div>
          ) : (
            <>
              <h1 className="mb-1 text-lg font-semibold text-text">
                {firstRun ? "Welcome to ContentRemixer" : "Sign in"}
              </h1>
              <p className="mb-5 text-sm text-text-muted">
                {firstRun
                  ? "First-run setup. Sign in with the seed administrator credentials from your deployment secret, then rotate the password."
                  : "Access your ContentRemixer console."}
              </p>

              <label className="mb-1 block text-sm font-medium text-text" htmlFor="username">
                Username
              </label>
              <input
                id="username"
                name="username"
                autoComplete="username"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                required
                className="mb-4 w-full rounded-md border border-border bg-bg px-3 py-2 text-sm text-text outline-none focus:border-primary"
              />

              <label className="mb-1 block text-sm font-medium text-text" htmlFor="password">
                Password
              </label>
              <input
                id="password"
                name="password"
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
                className="mb-4 w-full rounded-md border border-border bg-bg px-3 py-2 text-sm text-text outline-none focus:border-primary"
              />

              {error ? (
                <p role="alert" className="mb-4 rounded-md bg-magenta/10 px-3 py-2 text-sm text-accent">
                  {error}
                </p>
              ) : null}

              <button
                type="submit"
                disabled={submitting}
                className="flex w-full items-center justify-center gap-2 rounded-md bg-primary px-4 py-2.5 text-sm font-semibold text-primary-contrast transition-opacity hover:opacity-90 disabled:opacity-60"
              >
                {submitting ? <Spinner size={16} /> : null}
                {firstRun ? "Sign in & set up" : "Sign in"}
              </button>

              {firstRun ? (
                <p className="mt-4 text-xs text-text-muted">
                  The seed administrator is provisioned from the Booth deployment
                  secret. Additional users and roles are managed from Admin once you&apos;re in.
                </p>
              ) : null}
            </>
          )}
        </form>
      </div>
    </div>
  );
}
