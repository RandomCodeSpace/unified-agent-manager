// The Interface setting, kept per browser (`uam.layout`): Current (the default) or Notebook, the
// beta layout. The page's layout is `data-layout="classic|notebook"` on <html>, which index.css
// keys the notebook rules on. Both layouts draw the same components over the same state: only
// where they stand differs, so a feature built once shows in both (components/notebook).

import { useSyncExternalStore } from 'react';

export type Layout = 'classic' | 'notebook';

export const LAYOUT_KEY = 'uam.layout';

/** A stored value as a setting: anything but "notebook" (nothing stored, or garbage) is the current layout. */
export function parseLayout(raw: string | null): Layout {
  return raw === 'notebook' ? 'notebook' : 'classic';
}

export function loadLayout(): Layout {
  try {
    return parseLayout(localStorage.getItem(LAYOUT_KEY));
  } catch {
    return 'classic';
  }
}

const listeners = new Set<() => void>();
let current: Layout | undefined;

/** The layout on screen. */
export function currentLayout(): Layout {
  if (current === undefined) applyLayout(loadLayout());
  return current!;
}

/** Calls `listener` whenever the layout on screen changes; returns the unsubscribe. */
export function subscribeLayout(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

/** The layout on screen, for the places where the two layouts differ. */
export function useLayout(): Layout {
  return useSyncExternalStore(subscribeLayout, currentLayout, currentLayout);
}

export function applyLayout(layout: Layout): void {
  const changed = current !== layout;
  current = layout;
  if (typeof document !== 'undefined') document.documentElement.dataset.layout = layout;
  if (changed) for (const listener of listeners) listener();
}

/** Stores the setting and shows it; blocked storage keeps it for this page only. */
export function saveLayout(layout: Layout): void {
  try {
    localStorage.setItem(LAYOUT_KEY, layout);
  } catch {
    // Storage unavailable: kept for this page only.
  }
  applyLayout(layout);
}
