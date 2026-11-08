import { createContext, useContext, useCallback, useEffect, useState, type ReactNode } from "react";
import { api, ApiError } from "./api";

// Mirrors booth/auth.Session JSON ({sub, role}). Role vocabulary is frozen by
// design §6.1 — the server is authoritative; this only drives cosmetic nav (§4).
export type Role = "Admin" | "DevRel";

export interface Session {
  sub: string;
  role: Role;
}

export type AuthStatus = "loading" | "authenticated" | "anonymous";

interface AuthCtx {
  status: AuthStatus;
  session: Session | null;
  refresh: () => Promise<void>;
  logout: () => Promise<void>;
}

const Ctx = createContext<AuthCtx | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<AuthStatus>("loading");
  const [session, setSession] = useState<Session | null>(null);

  const refresh = useCallback(async () => {
    try {
      const s = await api.get<Session>("/v1/auth/whoami");
      setSession(s);
      setStatus("authenticated");
    } catch (err) {
      // 401 (and any failure to resolve a session) → treated as anonymous; the
      // router sends the user to the login/session-expired surface (W1).
      if (!(err instanceof ApiError) || err.status === 401) {
        setSession(null);
        setStatus("anonymous");
        return;
      }
      // Non-auth error (server down): still show anonymous so the app is usable
      // rather than stuck on a spinner; login attempt will surface the real error.
      setSession(null);
      setStatus("anonymous");
    }
  }, []);

  const logout = useCallback(async () => {
    try {
      await api.post("/v1/auth/logout");
    } catch {
      /* logout is idempotent server-side; clear locally regardless */
    }
    setSession(null);
    setStatus("anonymous");
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  return <Ctx.Provider value={{ status, session, refresh, logout }}>{children}</Ctx.Provider>;
}

export function useAuth(): AuthCtx {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error("useAuth must be used within AuthProvider");
  return ctx;
}
