import * as Primitive from "@radix-ui/react-avatar";
import { cx } from "./cx";

export interface AvatarProps {
  name: string;
  src?: string;
  size?: "sm" | "md" | "lg";
  /** Marks agent/bot identities with a gradient ring (assign-an-agent flows). */
  agent?: boolean;
  className?: string;
}

const sizes = {
  sm: "h-6 w-6 text-[10px]",
  md: "h-8 w-8 text-xs",
  lg: "h-10 w-10 text-sm",
};

function initials(name: string): string {
  const parts = name.trim().split(/\s+/).slice(0, 2);
  return parts.map((p) => p[0]?.toUpperCase() ?? "").join("") || "?";
}

export function Avatar({ name, src, size = "md", agent, className }: AvatarProps) {
  return (
    <Primitive.Root
      className={cx(
        "relative inline-flex shrink-0 select-none items-center justify-center overflow-hidden rounded-pill",
        agent && "ring-2 ring-offset-2 ring-offset-bg [background-image:var(--cr-gradient-fade)] ring-primary p-[2px]",
        sizes[size],
        className,
      )}
      title={name}
    >
      <span className="flex h-full w-full items-center justify-center overflow-hidden rounded-pill bg-surface-2 font-semibold text-text">
        <Primitive.Image src={src} alt={name} className="h-full w-full object-cover" />
        <Primitive.Fallback delayMs={src ? 300 : 0}>{initials(name)}</Primitive.Fallback>
      </span>
    </Primitive.Root>
  );
}

export default Avatar;
