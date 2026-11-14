// Fake cursor for demo recordings.
//
// Headless Chromium renders NO cursor, so a recorded click looks like the UI
// reacts to nothing. We inject a DOM cursor that follows `page.mouse` moves and
// flashes a ripple on click, via `context.addInitScript` so it survives every
// navigation (the script re-runs on each new document).
//
// This is the dependency-free floor described in the plan (R2): a self-contained
// init script modelled on `mouse-helper`. `demowright` (purpose-built: SVG
// cursor + ripples + keystroke HUD + SRT) was evaluated as the richer option -
// see README "demowright evaluation". We ship the lean auditable cursor here and
// leave demowright as a documented upgrade path.

// The browser-side script. Self-contained (no imports) because it is serialized
// and injected into the page context. Guards against double-install and against
// running before <body> exists.
export function cursorInitScript() {
  return `(() => {
    if (window.__demoCursorInstalled) return;
    window.__demoCursorInstalled = true;

    const install = () => {
      if (!document.body) return;
      const dot = document.createElement('div');
      dot.id = '__demo-cursor';
      dot.style.cssText = [
        'position:fixed', 'top:0', 'left:0', 'z-index:2147483647',
        'width:22px', 'height:22px', 'margin:-11px 0 0 -11px',
        'border-radius:50%', 'pointer-events:none',
        'background:rgba(124,58,237,0.35)', 'border:2px solid #7C3AED',
        'box-shadow:0 0 0 1px rgba(255,255,255,0.6)',
        'transition:transform 60ms linear', 'will-change:transform',
      ].join(';');
      document.body.appendChild(dot);

      const move = (e) => {
        dot.style.left = e.clientX + 'px';
        dot.style.top = e.clientY + 'px';
      };
      document.addEventListener('mousemove', move, true);

      const ripple = (e) => {
        const r = document.createElement('div');
        r.style.cssText = [
          'position:fixed', 'z-index:2147483646', 'pointer-events:none',
          'left:' + e.clientX + 'px', 'top:' + e.clientY + 'px',
          'width:12px', 'height:12px', 'margin:-6px 0 0 -6px',
          'border-radius:50%', 'border:2px solid #EC4899',
          'opacity:0.9', 'transform:scale(1)',
          'transition:transform 400ms ease-out, opacity 400ms ease-out',
        ].join(';');
        document.body.appendChild(r);
        requestAnimationFrame(() => {
          r.style.transform = 'scale(4)';
          r.style.opacity = '0';
        });
        setTimeout(() => r.remove(), 450);
      };
      document.addEventListener('mousedown', ripple, true);
    };

    if (document.readyState === 'loading') {
      document.addEventListener('DOMContentLoaded', install);
    } else {
      install();
    }
  })();`;
}

// Install the fake cursor on a Playwright browser context. Must be called before
// the first navigation; addInitScript re-runs the script on every new document.
export async function installCursor(context) {
  await context.addInitScript(cursorInitScript());
}
