// Compact crossfader lockup for the top bar: blog lines (left) + play (right)
// + knob mid-mix, on the brand fade gradient. Simplified from the master mark
// (branding/kit); the full SVG lockup ships as a static asset.
export function Logo({ height = 28, withWordmark = true }: { height?: number; withWordmark?: boolean }) {
  return (
    <span className="inline-flex items-center gap-2 select-none" aria-label="ContentRemixer">
      <svg width={height} height={height} viewBox="0 0 48 48" fill="none" aria-hidden="true">
        <defs>
          <linearGradient id="crlogo-fade" x1="0" y1="0" x2="1" y2="0">
            <stop offset="0" stopColor="var(--cr-violet)" />
            <stop offset="0.5" stopColor="var(--cr-magenta)" />
            <stop offset="1" stopColor="var(--cr-orange)" />
          </linearGradient>
        </defs>
        <rect x="2" y="2" width="44" height="44" rx="12" fill="var(--cr-gradient-tile-bg)" />
        <rect x="2" y="2" width="44" height="44" rx="12" fill="url(#crlogo-fade)" opacity="0.14" />
        <path d="M11 17h9M11 22h9M11 27h6" stroke="var(--cr-glyph-lavender)" strokeWidth="2.2" strokeLinecap="round" />
        <path d="M29 16l9 6-9 6z" fill="var(--cr-glyph-orange)" />
        <rect x="8" y="33" width="32" height="3" rx="1.5" fill="url(#crlogo-fade)" />
        <circle cx="24" cy="34.5" r="4" fill="var(--cr-white)" />
      </svg>
      {withWordmark ? (
        <span className="text-[15px] font-semibold tracking-tight text-text">ContentRemixer</span>
      ) : null}
    </span>
  );
}
