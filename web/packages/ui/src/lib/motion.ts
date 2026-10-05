// The Motion setting, kept per browser (`uam.motion`). By default everything animates, even
// when the OS asks for reduced motion (Windows with animation effects off, Remote Desktop);
// "Match system" honours that request. The choice is `data-motion="system"` on <html>, which
// the reduced-motion rules in index.css (and the `motion-reduce:` variant) require.

export type Motion = 'on' | 'system';

export const MOTION_KEY = 'uam.motion';

/** A stored value as a setting: anything but "system" (nothing stored, or garbage) is on. */
export function parseMotion(raw: string | null): Motion {
  return raw === 'system' ? 'system' : 'on';
}

export function loadMotion(): Motion {
  try {
    return parseMotion(localStorage.getItem(MOTION_KEY));
  } catch {
    return 'on';
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
