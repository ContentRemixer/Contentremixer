// A3 loading skeletons: shimmer placeholders shown while a fetch is in flight,
// so a screen reserves its layout instead of flashing a spinner then jumping.
// The pulse uses Tailwind's animate-pulse over a token-driven surface colour so
// it tracks the light/dark theme. Reused by the W3/W5/W6 data screens.

export function Skeleton({ className = "" }: { className?: string }) {
  return <div className={`animate-pulse rounded-md bg-surface-2 ${className}`} aria-hidden="true" />;
}

// A block of fake text lines; the last line is shortened to read as a paragraph.
export function SkeletonText({ lines = 3, className = "" }: { lines?: number; className?: string }) {
  return (
    <div className={`flex flex-col gap-2 ${className}`} aria-hidden="true">
      {Array.from({ length: lines }).map((_, i) => (
        <Skeleton key={i} className={`h-3.5 ${i === lines - 1 ? "w-2/3" : "w-full"}`} />
      ))}
    </div>
  );
}

// A card-shaped placeholder matching the dashboard/list tiles.
export function SkeletonCard() {
  return (
    <div className="rounded-lg border border-border bg-surface p-5 shadow-card" aria-hidden="true">
      <Skeleton className="mb-3 h-4 w-1/3" />
      <SkeletonText lines={2} />
    </div>
  );
}

// A labelled wrapper that announces the loading state to assistive tech while
// rendering the visual placeholder for everyone else.
export function SkeletonList({ count = 3, label = "Loading…" }: { count?: number; label?: string }) {
  return (
    <div role="status" aria-busy="true" aria-label={label} className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
      {Array.from({ length: count }).map((_, i) => (
        <SkeletonCard key={i} />
      ))}
    </div>
  );
}
