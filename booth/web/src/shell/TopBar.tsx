import { Logo } from "../components/Logo";
import { ThemeToggle } from "./ThemeToggle";
import { Notifications } from "./Notifications";
import { UserMenu } from "./UserMenu";

export function TopBar({ onToggleSidebar }: { onToggleSidebar: () => void }) {
  return (
    <header className="sticky top-0 z-20 flex h-14 items-center justify-between gap-3 border-b border-border bg-topbar-bg px-3 backdrop-blur-md sm:px-4">
      <div className="flex items-center gap-2">
        <button
          type="button"
          onClick={onToggleSidebar}
          aria-label="Toggle navigation"
          className="grid h-9 w-9 place-items-center rounded-md text-text-muted hover:bg-surface-2 hover:text-text lg:hidden"
        >
          <svg width={20} height={20} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.75} strokeLinecap="round">
            <path d="M4 7h16M4 12h16M4 17h16" />
          </svg>
        </button>
        <Logo />
      </div>

      <div className="flex items-center gap-1 sm:gap-2">
        <ThemeToggle />
        <Notifications />
        <div className="mx-1 h-6 w-px bg-border" aria-hidden="true" />
        <UserMenu />
      </div>
    </header>
  );
}
