// The Motion setting, kept per browser (`uam.motion`). "Match system" honours the OS request for
// reduced motion; "Always on" animates even then (Windows with animation effects off, Remote
// Desktop). With no choice stored, a touch-first device (a phone, a tablet) matches the system
// and a desktop animates. The choice is `data-motion="system"` on <html>, which the
// reduced-motion rules in index.css (and the `motion-reduce:` variant) require.

export type Motion = 'on' | 'system';

export const MOTION_KEY = 'uam.motion';

/** The setting with nothing stored: "Match system" on a coarse pointer, else "Always on". */
export function defaultMotion(): Motion {
  try {
    return globalThis.matchMedia?.('(pointer: coarse)').matches ? 'system' : 'on';
  } catch {
    return 'on';
  }
}

/** A stored value as a setting: "on" or "system" as chosen; nothing stored, or garbage, is `fallback`. */
export function parseMotion(raw: string | null, fallback: Motion = 'on'): Motion {
  return raw === 'system' || raw === 'on' ? raw : fallback;
}

export function loadMotion(): Motion {
  try {
    return parseMotion(localStorage.getItem(MOTION_KEY), defaultMotion());
  } catch {
    return defaultMotion();
  }
}

export function applyMotion(motion: Motion): void {
  if (motion === 'system') document.documentElement.dataset.motion = 'system';
  else delete document.documentElement.dataset.motion;
}

/** Stores the choice and applies it at once; storage that refuses still changes this page. */
export function saveMotion(motion: Motion): void {
  try {
    localStorage.setItem(MOTION_KEY, motion);
  } catch {
    // Private mode or a full quota: the page still follows the choice until it reloads.
  }
  applyMotion(motion);
}
