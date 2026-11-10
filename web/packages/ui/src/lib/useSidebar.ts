import { startTransition, useCallback, useEffect, useRef, useState, type Dispatch, type SetStateAction } from 'react';
import type { Auth } from '../state';
import { popupOpen } from './overlays';
import { readJSON, writeJSON } from './storage';

const SIDEBAR_KEY = 'uam.sidebar';

/** The narrow screen's drawer: whether it is open, and its Thread list kept mounted once it has opened. */
export function useDrawer(narrow: boolean, loaded: boolean) {
  const [drawerOpen, setDrawerOpen] = useState(false);
  // Once opened, the drawer keeps its Thread list while closed (Sheet keepMounted): opening it again renders nothing new.
  const [drawerKept, setDrawerKept] = useState(false);
  if (drawerOpen && !drawerKept) setDrawerKept(true);
  // On a narrow screen the list mounts in the closed drawer once the first snapshot is in and the page is idle, in a
  // transition React renders in slices: the first opening then only slides it in, as later ones do.
  useEffect(() => {
    if (!narrow || drawerKept || !loaded) return;
    const idle = window.requestIdleCallback ?? ((cb: () => void) => window.setTimeout(cb, 1000));
    const cancel = window.cancelIdleCallback ?? window.clearTimeout;
    const handle = idle(() => startTransition(() => setDrawerKept(true)));
    return () => cancel(handle);
  }, [narrow, drawerKept, loaded]);
  // The drawer itself takes focus as it opens, not its Hide button: focus moved there after a key press shows the
  // button's tip, and Esc would close that tip instead of the drawer.
  const drawer = useRef<HTMLDivElement>(null);
  return { drawerOpen, setDrawerOpen, drawerKept, drawer };
}

/** The wide layout's sidebar, open or collapsed to its rail (remembered per browser), and Ctrl/Cmd+B for it or the drawer. */
export function useSidebar({ auth, narrow, emptyWorkspace, setDrawerOpen }: { auth: Auth; narrow: boolean; emptyWorkspace: boolean; setDrawerOpen: Dispatch<SetStateAction<boolean>> }) {
  const [sidebarState, setSidebarState] = useState(() => ({ emptyWorkspace, open: readJSON<boolean>(SIDEBAR_KEY, true) }));
  // Apply the empty-workspace default only when entering it; subsequent snapshots preserve a manual toggle.
  // The browser's saved preference still applies once a Task exists, and after signing in again.
  if (sidebarState.emptyWorkspace !== emptyWorkspace) {
    setSidebarState({ emptyWorkspace, open: emptyWorkspace ? false : readJSON<boolean>(SIDEBAR_KEY, true) });
  }
  const sidebarOpen = sidebarState.open;
  const aside = useRef<HTMLElement>(null);

  /**
   * Collapses the sidebar to its icon rail or expands it (wide layout), remembered per browser.
   * Focus on the sidebar or the rail moves to the other's toggle, so a keyboard user is never
   * left on an inert or removed element; focus anywhere else (the composer) is left alone.
   */
  const toggleSidebar = useCallback(() => {
    const next = !sidebarOpen;
    setSidebarState((current) => ({ ...current, open: next }));
    writeJSON(SIDEBAR_KEY, next);
    if (aside.current?.contains(document.activeElement)) requestAnimationFrame(() => document.getElementById(next ? 'sidebar-hide' : 'sidebar-show')?.focus());
  }, [sidebarOpen]);

  // Ctrl/Cmd+B toggles the sidebar, or the drawer on a narrow screen.
  useEffect(() => {
    if (auth !== 'in') return;
    const onKey = (e: KeyboardEvent) => {
      const modifier = navigator.platform.startsWith('Mac') ? e.metaKey : e.ctrlKey;
      if (!modifier || e.altKey || e.shiftKey || e.key.toLowerCase() !== 'b' || e.defaultPrevented || popupOpen()) return;
      e.preventDefault();
      if (narrow) setDrawerOpen((o) => !o);
      else toggleSidebar();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [auth, narrow, toggleSidebar, setDrawerOpen]);
  return { sidebarOpen, toggleSidebar, aside };
}
