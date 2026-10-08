// The Theme setting, kept per browser (`uam.theme`): Light, Dark, or Match system (the default),
// which follows the OS colour scheme as it changes. The page's scheme is `data-theme="light|dark"`
// on <html>, which index.css keys the dark tokens on. src/theme.js sets it before the first
// paint with the same rule, so a dark page never flashes light; this module keeps it current.

import { useSyncExternalStore } from 'react';

export type Theme = 'light' | 'dark' | 'system';
export type Scheme = 'light' | 'dark';

export const THEME_KEY = 'uam.theme';

const DARK_QUERY = '(prefers-color-scheme: dark)';

/** A stored value as a setting: anything but "light" or "dark" (nothing stored, or garbage) matches the system. */
export function parseTheme(raw: string | null): Theme {
  return raw === 'light' || raw === 'dark' ? raw : 'system';
}

/** The scheme a setting shows: Match system takes the OS's. */
export function resolveTheme(theme: Theme, systemDark: boolean): Scheme {
  if (theme === 'system') return systemDark ? 'dark' : 'light';
  return theme;
}

export function loadTheme(): Theme {
  try {
    return parseTheme(localStorage.getItem(THEME_KEY));
  } catch {
    return 'system';
  }
}

const listeners = new Set<() => void>();

/** The scheme on screen. */
export function currentScheme(): Scheme {
  return document.documentElement.dataset.theme === 'dark' ? 'dark' : 'light';
}

/** Calls `listener` whenever the scheme on screen changes; returns the unsubscribe. */
export function subscribeScheme(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

/** The scheme on screen, for views that draw with token values (charts, diagrams). */
export function useScheme(): Scheme {
  return useSyncExternalStore(subscribeScheme, currentScheme, currentScheme);
}

export function applyTheme(theme: Theme): void {
  const scheme = resolveTheme(theme, matchMedia(DARK_QUERY).matches);
  const root = document.documentElement;
  const changed = root.dataset.theme !== scheme;
  root.dataset.theme = scheme;
  // The browser chrome (Android's address bar, an installed app's title bar) takes the canvas.
  const canvas = getComputedStyle(root).getPropertyValue('--color-canvas').trim();
  if (canvas) document.querySelector('meta[name="theme-color"]')?.setAttribute('content', canvas);
  if (changed) for (const listener of listeners) listener();
}

/** Stores the choice and applies it at once; storage that refuses still changes this page. */
export function saveTheme(theme: Theme): void {
  try {
    localStorage.setItem(THEME_KEY, theme);
  } catch {
    // Private mode or a full quota: the page still follows the choice until it reloads.
  }
  applyTheme(theme);
}

/** Applies the stored setting and follows OS scheme changes while it is Match system; returns the stop. */
export function startTheme(): () => void {
  applyTheme(loadTheme());
  const query = matchMedia(DARK_QUERY);
  const follow = () => applyTheme(loadTheme());
  query.addEventListener('change', follow);
  return () => query.removeEventListener('change', follow);
}
