import { useCallback, useEffect, useRef, useState } from 'react';
import { popupOpen, threadCovered } from './overlays';

/** The open Thread's Changes sheet: whether it is open, its Esc, and where focus goes back as it closes. */
export function useChangesSheet() {
  const [sheetOpen, setSheetOpen] = useState(false);
  // What had focus when Changes opened (the header button, a turn's "View changes"); focus goes back there on
  // close, or to the header button once that is gone.
  const changesOpener = useRef<Element | null>(null);
  const focusChangesOpener = () => {
    const opener = changesOpener.current;
    changesOpener.current = null;
    const back = opener instanceof HTMLElement && opener.isConnected && opener !== document.body ? opener : document.getElementById('changes-link');
    back?.focus();
  };

  // Esc closes the Changes sheet when no popup owns the key (popups and the drawer handle their own), and only
  // from inside the panel or with nothing focused: Esc in the conversation or the composer is theirs.
  useEffect(() => {
    if (!sheetOpen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || popupOpen() || threadCovered()) return;
      const at = document.activeElement;
      if (at && at !== document.body && !at.closest('[aria-label="Changes"]')) return;
      setSheetOpen(false);
      focusChangesOpener();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [sheetOpen]);

  const onSheet = useCallback((open: boolean, restoreFocus = true) => {
    if (open) changesOpener.current = document.activeElement;
    setSheetOpen(open);
    if (!open && restoreFocus) focusChangesOpener();
  }, []);
  return { sheetOpen, setSheetOpen, onSheet };
}
