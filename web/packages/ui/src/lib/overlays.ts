import { startTransition } from 'react';

/** True while a Base UI popup (menu, dialog, tooltip) is open; app-level Esc handlers stand back. */
export const popupOpen = () => !!document.querySelector('[data-popup]');
/** A menu, dialog or other popup owns the keyboard; an open tooltip (Base UI marks it `data-popup="tooltip"`, with no role) does not. */
export const keysTaken = () => !!document.querySelector('[data-popup]:not([data-popup="tooltip"])');
/** Settings or Routines cover the Thread, which stays mounted beneath: its own keys (Alt+P, a side panel's Esc) stand back. */
export const threadCovered = () => !!document.querySelector('[data-view]');

/** Typing anywhere (Settings forms, a subagent follow-up) counts as unsent work, like a composer draft. */
export function editing(): boolean {
  const el = document.activeElement;
  return el instanceof HTMLElement && (el.isContentEditable || el.matches('textarea, input:not([type=checkbox]):not([type=radio]):not([type=button]):not([type=submit])'));
}

/** Task streams render concurrently, without snapshot animations. Open overlays keep their immediate updates. */
export function sessionsUpdate(update: () => void) {
  if (keysTaken()) return update();
  startTransition(update);
}
