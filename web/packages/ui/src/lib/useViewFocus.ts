import { startTransition, useCallback, useEffect, useRef, type Dispatch, type RefObject, type SetStateAction } from 'react';
import { editing, keysTaken } from './overlays';

/** Where focus lands when the view it came from is gone: the open Task's conversation, else the main pane (also while a view covers the Thread). */
export const conversation = () => {
  const section = document.querySelector<HTMLElement>('main section[aria-label="Conversation"]');
  return section && !section.closest('[inert]') ? section : document.querySelector<HTMLElement>('main');
};

/**
 * Focus around Settings and Routines, the views that open over the Thread: `noteOpener` remembers what opened one,
 * and `closeView` (their ×, or Esc) closes it and gives that element focus back once the view has left the screen.
 */
export function useViewFocus({ viewOpen, viewShown, pushView: pushViewRef, setSettingsOpen, setRoutinesFor }: {
  viewOpen: boolean;
  /** The same, as of the last commit. */
  viewShown: RefObject<boolean>;
  /** Set by a move between views (see useHashRoute). */
  pushView: RefObject<boolean>;
  setSettingsOpen: Dispatch<SetStateAction<boolean>>;
  setRoutinesFor: Dispatch<SetStateAction<string | null>>;
}) {
  // What opened Settings or Routines: closing the view gives it focus back.
  const opener = useRef<HTMLElement | null>(null);
  const noteOpener = useCallback(() => {
    pushViewRef.current = true;
    const el = document.activeElement;
    // A move inside the open view keeps the button that opened it.
    if (viewShown.current && el?.closest('main')) return;
    opener.current = el instanceof HTMLElement && el !== document.body ? el : null;
  }, [pushViewRef, viewShown]);
  // What gets focus back once a closed view has left the screen (undefined: no close pending).
  const restoreTo = useRef<HTMLElement | null | undefined>(undefined);
  /** Closes Settings or Routines (their ×, or Esc): focus goes back to what opened the view, or to the Task's conversation once that is gone. */
  const closeView = useCallback(() => {
    pushViewRef.current = true;
    // A second Esc before the close commits keeps the first one's target.
    restoreTo.current ??= opener.current;
    opener.current = null;
    startTransition(() => {
      setSettingsOpen(false);
      setRoutinesFor(null);
    });
  }, [pushViewRef, setSettingsOpen, setRoutinesFor]);
  // After the close commits: until then the Thread is still inert under the view.
  useEffect(() => {
    if (viewOpen || restoreTo.current === undefined) return;
    const back = restoreTo.current;
    restoreTo.current = undefined;
    back?.focus();
    // An opener that cannot take focus (gone, inert, in the closed drawer) gives way to the conversation.
    if (!back || document.activeElement !== back) conversation()?.focus();
  }, [viewOpen]);
  // Esc closes Settings or Routines, like their ×; not while typing, in the terminal, or with a popup open (it closes that).
  useEffect(() => {
    if (!viewOpen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || e.defaultPrevented || keysTaken() || editing()) return;
      if (e.target instanceof Element && e.target.closest('.xterm')) return;
      closeView();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [viewOpen, closeView]);
  return { noteOpener, closeView };
}
