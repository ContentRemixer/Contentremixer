// Tiny className combiner for the in-house UI library (ISI-7210, W7).
// Keeps the component lib dependency-free — no clsx/tailwind-merge — while
// still allowing conditional and falsy classes.
export type ClassValue = string | number | false | null | undefined;

export function cx(...parts: ClassValue[]): string {
  return parts.filter(Boolean).join(" ");
}

export default cx;
