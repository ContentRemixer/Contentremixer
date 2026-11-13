import type { ComponentType, SVGProps } from "react";
import type { Role } from "../app/auth";
import { HomeIcon, ContentIcon, ScraperIcon, AdminIcon, PersonaIcon } from "../components/icons";

export interface NavItem {
  to: string;
  label: string;
  icon: ComponentType<SVGProps<SVGSVGElement> & { size?: number }>;
  /** Roles that see the item. Cosmetic only — server enforces access (§4). */
  roles: Role[];
  end?: boolean;
}

export const NAV_ITEMS: NavItem[] = [
  { to: "/", label: "Home", icon: HomeIcon, roles: ["Admin", "DevRel"], end: true },
  { to: "/content", label: "Content", icon: ContentIcon, roles: ["Admin", "DevRel"] },
  {
    to: "/personas",
    label: "Personas",
    icon: PersonaIcon,
    // DevRel self-service persona dashboard (ISI-7362 D7): until now personas
    // lived only in the Admin console; the DevRel owns theirs here.
    roles: ["Admin", "DevRel"],
  },
  { to: "/scraper-review", label: "Scraper review", icon: ScraperIcon, roles: ["Admin", "DevRel"] },
  { to: "/admin", label: "Admin", icon: AdminIcon, roles: ["Admin"] },
];

export function navForRole(role: Role): NavItem[] {
  return NAV_ITEMS.filter((item) => item.roles.includes(role));
}
