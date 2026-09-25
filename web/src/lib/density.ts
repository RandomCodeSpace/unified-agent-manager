// The Activity density setting, kept per browser (`uam.activity`). Compact (the default) folds
// every thought and tool call of a turn into its head row; Detailed keeps one activity row per
// run of work between two paragraphs, as the transcript drew before. Components read it through
// `useDensity`, which follows a change made in Settings without a reload.

import { useSyncExternalStore } from 'react';

export type Density = 'compact' | 'detailed';

export const DENSITY_KEY = 'uam.activity';

/** A stored value as a setting: anything but "detailed" (nothing stored, or garbage) is compact. */
export function parseDensity(raw: string | null): Density {
  return raw === 'detailed' ? 'detailed' : 'compact';
}

export function loadDensity(): Density {
  try {
    return parseDensity(localStorage.getItem(DENSITY_KEY));
  } catch {
    return 'compact';
  }
}

const listeners = new Set<() => void>();

/** Stores the choice and tells every open view; storage that refuses still changes this page. */
export function saveDensity(density: Density): void {
  try {
    localStorage.setItem(DENSITY_KEY, density);
  } catch {
    // Private mode or a full quota: the page still follows the choice until it reloads.
  }
  current = density;
  for (const fn of listeners) fn();
}

let current: Density | null = null;
const read = () => (current ??= loadDensity());
const subscribe = (fn: () => void) => {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
};

export function useDensity(): Density {
  return useSyncExternalStore(subscribe, read, read);
}
