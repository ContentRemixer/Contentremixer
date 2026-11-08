import { useState, type FormEvent } from "react";
import { Navigate, useLocation, useNavigate } from "react-router-dom";
import { api, ApiError } from "../app/api";
import { useAuth, type Session } from "../app/auth";
import { Logo } from "../components/Logo";
import { Spinner } from "../components/Loader";

// Foundation login so auth-gated routing works end to end. W1 expands this into
// the full A1 surface (first-run Admin bootstrap, branding, A3 states). Already
// authenticated → bounce to the app.
export function LoginPage() {
  const { status, refresh } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

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
      await refresh();
      navigate("/", { replace: true });
    } catch (err) {
      setError(err instanceof ApiError && err.status === 401 ? "Invalid username or password." : "Sign-in failed. Try again.");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="flex min-h-full items-center justify-center bg-bg px-4 py-12">
      <div className="w-full max-w-sm">
        <div className="mb-8 flex justify-center">
          <Logo height={36} />
        </div>
        <form
          onSubmit={onSubmit}
          className="rounded-lg border border-border bg-surface p-6 shadow-card"
        >
          <h1 className="mb-1 text-lg font-semibold text-text">Sign in</h1>
          <p className="mb-5 text-sm text-text-muted">Access your ContentRemixer console.</p>

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
            Sign in
          </button>
        </form>
      </div>
    </div>
  );
}
