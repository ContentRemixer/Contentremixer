export function Spinner({ size = 20 }: { size?: number }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      role="status"
      aria-label="Loading"
      className="animate-spin"
    >
      <circle cx="12" cy="12" r="9" stroke="var(--cr-border-strong)" strokeWidth="3" />
      <path d="M21 12a9 9 0 0 0-9-9" stroke="var(--cr-primary)" strokeWidth="3" strokeLinecap="round" />
    </svg>
  );
}

export function FullScreenLoader({ label }: { label?: string }) {
  return (
    <div className="flex h-full w-full flex-col items-center justify-center gap-3 bg-bg text-text-muted">
      <Spinner size={28} />
      {label ? <p className="text-sm">{label}</p> : null}
    </div>
  );
}
