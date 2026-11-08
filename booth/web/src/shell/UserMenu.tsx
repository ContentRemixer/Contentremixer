import { useNavigate } from "react-router-dom";
import { useAuth } from "../app/auth";
import { ChevronDownIcon, LogoutIcon } from "../components/icons";
import { useDismissable } from "./useDismissable";

function initials(subject: string): string {
  const parts = subject.replace(/[._-]+/g, " ").trim().split(/\s+/);
  const letters = parts.slice(0, 2).map((p) => p[0] ?? "");
  return (letters.join("") || subject.slice(0, 2)).toUpperCase();
}

export function UserMenu() {
  const { session, logout } = useAuth();
  const { ref, open, toggle, setOpen } = useDismissable<HTMLDivElement>();
  const navigate = useNavigate();

  if (!session) return null;

  async function onLogout() {
    setOpen(false);
    await logout();
    navigate("/login", { replace: true });
  }

  return (
    <div className="relative" ref={ref}>
      <button
        type="button"
        onClick={toggle}
        aria-haspopup="menu"
        aria-expanded={open}
        className="flex items-center gap-2 rounded-md py-1 pl-1 pr-2 hover:bg-surface-2 transition-colors"
      >
        <span className="grid h-8 w-8 place-items-center rounded-pill bg-fade text-xs font-semibold text-primary-contrast">
          {initials(session.sub)}
        </span>
        <span className="hidden text-left sm:block">
          <span className="block text-sm font-medium leading-tight text-text">{session.sub}</span>
          <span className="block text-xs leading-tight text-text-muted">{session.role}</span>
        </span>
        <ChevronDownIcon size={16} className="text-text-muted" />
      </button>

      {open ? (
        <div
          role="menu"
          className="absolute right-0 z-30 mt-2 w-56 overflow-hidden rounded-lg border border-border bg-surface-raised shadow-card"
        >
          <div className="border-b border-border px-4 py-3">
            <p className="text-sm font-semibold text-text">{session.sub}</p>
            <p className="text-xs text-text-muted">Signed in as {session.role}</p>
          </div>
          <button
            type="button"
            role="menuitem"
            onClick={onLogout}
            className="flex w-full items-center gap-2 px-4 py-2.5 text-left text-sm text-text hover:bg-surface-2 transition-colors"
          >
            <LogoutIcon size={16} className="text-text-muted" />
            Sign out
          </button>
        </div>
      ) : null}
    </div>
  );
}
