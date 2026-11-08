import { NavLink } from "react-router-dom";
import { navForRole } from "./nav";
import type { Role } from "../app/auth";

export function Sidebar({ role, onNavigate }: { role: Role; onNavigate?: () => void }) {
  const items = navForRole(role);
  return (
    <nav aria-label="Primary" className="flex h-full flex-col gap-1 p-3">
      {items.map((item) => {
        const Icon = item.icon;
        return (
          <NavLink
            key={item.to}
            to={item.to}
            end={item.end}
            onClick={onNavigate}
            className={({ isActive }) =>
              [
                "flex items-center gap-3 rounded-md px-3 py-2 text-sm font-medium transition-colors",
                isActive
                  ? "bg-surface-2 text-primary"
                  : "text-text-muted hover:bg-surface-2 hover:text-text",
              ].join(" ")
            }
          >
            <Icon size={18} />
            {item.label}
          </NavLink>
        );
      })}
    </nav>
  );
}
