// Caption timing helpers.
//
// demo-recorder delivers captions timed against the raw clip (0-based). The
// assembled timeline prepends intro stings, so a 0-based cue would surface over
// the brand card. preRollSec measures that lead-in and shiftCaptions re-times
// the cues (SRT comma or VTT dot timestamps) so burned and sidecar captions
// both line up with the final cut — an a11y correctness requirement, not polish.
//
// ponytail: v1 only offsets by the GENERATED leading stings (known duration)
// and does NOT compensate for a `trim.in` on the first clip (the caption file
// is assumed authored against the untrimmed clip head). Upgrade path: subtract
// the first clip's trim.in too, or author captions against the assembled cut.

// Seconds of lead-in before the first real clip: the generated intro stings.
// Stops at the first clip; a fileless-duration sting contributes 0 (unknown).
export function preRollSec(segments = []) {
  let sec = 0;
  for (const seg of segments) {
    if (seg.kind === "clip") break;
    if ((seg.kind === "sting-intro" || seg.kind === "sting-outro") && typeof seg.duration === "number") {
      sec += seg.duration;
    }
  }
  return sec;
}

const TS = /(\d{2}):(\d{2}):(\d{2})([.,])(\d{3})/g;

// Add offsetSec to every HH:MM:SS,mmm / HH:MM:SS.mmm timestamp, preserving the
// separator (so SRT stays SRT, VTT stays VTT). Negative results clamp to 0.
export function shiftCaptions(text, offsetSec) {
  if (!offsetSec) return text;
  const offMs = Math.round(offsetSec * 1000);
  return text.replace(TS, (_m, hh, mm, ss, sep, ms) => {
    let total = (((+hh * 60 + +mm) * 60 + +ss) * 1000 + +ms) + offMs;
    if (total < 0) total = 0;
    const h = Math.floor(total / 3600000);
    const m = Math.floor((total % 3600000) / 60000);
    const s = Math.floor((total % 60000) / 1000);
    const milli = total % 1000;
    const p = (n, w = 2) => String(n).padStart(w, "0");
    return `${p(h)}:${p(m)}:${p(s)}${sep}${p(milli, 3)}`;
  });
}
