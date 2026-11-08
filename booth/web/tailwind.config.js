/** @type {import('tailwindcss').Config} */
// Tailwind's theme is mapped ONTO the exported CSS variables (src/styles/tokens.css,
// generated from branding/kit/tokens.json). No hex is hand-copied here — the brand
// is the single source of truth, and the light/dark toggle flips [data-theme] which
// re-points the same semantic vars (§2.1).
export default {
  darkMode: ['selector', '[data-theme="dark"]'],
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        // brand primitives
        violet: 'var(--cr-violet)',
        magenta: 'var(--cr-magenta)',
        orange: 'var(--cr-orange)',
        'glyph-lavender': 'var(--cr-glyph-lavender)',
        'glyph-orange': 'var(--cr-glyph-orange)',
        // semantic (theme-aware)
        bg: 'var(--cr-bg)',
        surface: 'var(--cr-surface)',
        'surface-2': 'var(--cr-surface-2)',
        'surface-raised': 'var(--cr-surface-raised)',
        border: 'var(--cr-border)',
        'border-strong': 'var(--cr-border-strong)',
        text: 'var(--cr-text)',
        'text-muted': 'var(--cr-text-muted)',
        'text-inverted': 'var(--cr-text-inverted)',
        primary: 'var(--cr-primary)',
        'primary-contrast': 'var(--cr-primary-contrast)',
        accent: 'var(--cr-accent)',
        warn: 'var(--cr-warn)',
        'sidebar-bg': 'var(--cr-sidebar-bg)',
        'topbar-bg': 'var(--cr-topbar-bg)',
      },
      borderRadius: {
        sm: 'var(--cr-radius-sm)',
        md: 'var(--cr-radius-md)',
        lg: 'var(--cr-radius-lg)',
        pill: 'var(--cr-radius-pill)',
      },
      fontFamily: {
        sans: 'var(--cr-font-sans)',
      },
      boxShadow: {
        card: 'var(--cr-shadow-card)',
        glow: 'var(--cr-glow)',
      },
      backgroundImage: {
        fade: 'var(--cr-gradient-fade)',
        tile: 'var(--cr-gradient-tile-bg)',
      },
    },
  },
  plugins: [],
};
