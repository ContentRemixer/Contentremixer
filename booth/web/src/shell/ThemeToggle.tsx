import { useTheme } from "../app/theme";
import { SunIcon, MoonIcon } from "../components/icons";

export function ThemeToggle() {
  const { theme, toggle } = useTheme();
  const isDark = theme === "dark";
  return (
    <button
      type="button"
      onClick={toggle}
      aria-label={isDark ? "Switch to light theme" : "Switch to dark theme"}
      title={isDark ? "Light theme" : "Dark theme"}
      className="grid h-9 w-9 place-items-center rounded-md text-text-muted hover:bg-surface-2 hover:text-text transition-colors"
    >
      {isDark ? <SunIcon size={18} /> : <MoonIcon size={18} />}
    </button>
  );
}
