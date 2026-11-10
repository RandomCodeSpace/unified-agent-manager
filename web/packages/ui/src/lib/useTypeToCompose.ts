import { useEffect } from 'react';
import type { Auth } from '../state';
import { keysTaken } from './overlays';

export function useTypeToCompose(auth: Auth) {
  // Start typing in the viewed Task without first clicking its message box.
  // Focus synchronously so the browser inserts the first character normally.
  useEffect(() => {
    if (auth !== 'in') return;
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented || e.isComposing || e.metaKey || ((e.ctrlKey || e.altKey) && !e.getModifierState('AltGraph'))) return;
      if (e.key !== 'Dead' && ([...e.key].length !== 1 || !e.key.trim())) return;
      const target = e.target;
      if (!(target instanceof HTMLElement) || target.isContentEditable || target.closest('input, textarea, select, [role="textbox"], [role="combobox"], [role="listbox"], [role="menu"], [role="tree"], [role="grid"], [role="tablist"], [role="slider"], [role="spinbutton"], [role="separator"]')) return;
      if (keysTaken() || window.getSelection()?.isCollapsed === false) return;
      const composer = document.getElementById('composer-text');
      if (!(composer instanceof HTMLTextAreaElement) || composer.disabled || composer.readOnly || composer.closest('[inert]') || !composer.getClientRects().length) return;
      composer.focus({ preventScroll: true });
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [auth]);
}
