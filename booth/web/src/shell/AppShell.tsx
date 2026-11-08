import { useState } from "react";
import { Outlet } from "react-router-dom";
import { TopBar } from "./TopBar";
import { Sidebar } from "./Sidebar";
import { useAuth } from "../app/auth";

// A2 app shell: sticky top bar + persistent sidebar (desktop) / dismissable
// drawer (mobile) + themed content area. The authenticated session always exists
// here (RequireAuth gates the route), so role-driven nav is safe to render.
export function AppShell() {
  const { session } = useAuth();
  const [drawerOpen, setDrawerOpen] = useState(false);
  const role = session!.role;

  return (
    <div className="flex h-full flex-col bg-bg text-text">
      <TopBar onToggleSidebar={() => setDrawerOpen((v) => !v)} />

      <div className="flex min-h-0 flex-1">
        {/* Desktop sidebar */}
        <aside className="hidden w-60 shrink-0 border-r border-border bg-sidebar-bg lg:block">
          <Sidebar role={role} />
        </aside>

        {/* Mobile drawer */}
        {drawerOpen ? (
          <div className="fixed inset-0 z-30 lg:hidden">
            <div
              className="absolute inset-0 bg-black/40"
              onClick={() => setDrawerOpen(false)}
              aria-hidden="true"
            />
            <aside className="absolute left-0 top-0 h-full w-64 border-r border-border bg-sidebar-bg shadow-card">
              <Sidebar role={role} onNavigate={() => setDrawerOpen(false)} />
            </aside>
          </div>
        ) : null}

        <main className="min-w-0 flex-1 overflow-y-auto">
          <div className="mx-auto w-full max-w-6xl px-4 py-6 sm:px-6">
            <Outlet />
          </div>
        </main>
      </div>
    </div>
  );
}
