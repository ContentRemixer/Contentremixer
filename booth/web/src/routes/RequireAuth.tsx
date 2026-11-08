import { Navigate, useLocation } from "react-router-dom";
import type { ReactNode } from "react";
import { useAuth, type Role } from "../app/auth";

// Client-side gate. Presence-gates on an authenticated session and, optionally,
// a role. This is cosmetic UX only (§4): the Booth server independently enforces
// §6.3 on every call and returns 403 to a DevRel hitting an admin surface
// directly, so a routing bug cannot leak data — it only affects which screen the
// browser paints.
export function RequireAuth({ children, role }: { children: ReactNode; role?: Role }) {
  const { status, session } = useAuth();
  const location = useLocation();

  if (status !== "authenticated" || !session) {
    return <Navigate to="/login" replace state={{ from: location.pathname }} />;
  }

  if (role && session.role !== role) {
    return <Navigate to="/403" replace />;
  }

  return <>{children}</>;
}
