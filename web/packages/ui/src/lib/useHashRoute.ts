import { useEffect, useEffectEvent, useRef, type Dispatch, type RefObject, type SetStateAction } from 'react';
import type { ApiClient } from '../api';
import type { FederationView } from '../FederationContext';
import { decodePath } from './markdown';

export const HASH_PREFIX = '#task=';
export const SETTINGS_HASH = '#settings';
/** Every Project's routines: `#routines` (or `#routines=all`); one Project's: `#routines=<project id>`. */
export const ROUTINES_HASH = '#routines';
export const ROUTINES_PREFIX = '#routines=';
export const ALL_ROUTINES = 'all';

/** The routines the fragment shows: a Project id, `all`, or null for another view. */
export function hashRoutines(path = window.location.hash): string | null {
  const h = path.split('&')[0];
  if (h === ROUTINES_HASH) return ALL_ROUTINES;
  return h.startsWith(ROUTINES_PREFIX) ? decodePath(h.slice(ROUTINES_PREFIX.length)) || ALL_ROUTINES : null;
}

export function hashSelection(path = window.location.hash): string | null {
  const h = path.split('&')[0];
  return h.startsWith(HASH_PREFIX) ? new URLSearchParams(h.slice(1)).get('task') || null : null;
}

/** A subagent open in its Thread's Conversation pane: `#task=<id>&agent=<agent id>`. */
export function hashAgent(path = window.location.hash): { task: string; agent: string } | null {
  const task = hashSelection(path);
  const agent = new URLSearchParams(path.replace(/^#/, '')).get('agent');
  return task && agent ? { task, agent } : null;
}

/**
 * The view and the URL fragment, kept in step both ways: the view on screen is written to the fragment (a move
 * between views adds a history entry, flagged in `pushView`), and a fragment the user navigates to shows its view.
 */
export function useHashRoute({ api, federated, onRoute, pushView: pushViewRef, selectedId, shownAgent, settingsOpen, routinesFor, select, showSettings, showRoutines, setAgentView }: {
  api: ApiClient;
  federated: boolean;
  onRoute: FederationView['onRoute'] | undefined;
  /** Set by a move between views; named as a ref inside, which is what lets the lint rules allow writing it. */
  pushView: RefObject<boolean>;
  selectedId: string | null;
  shownAgent: string | null;
  settingsOpen: boolean;
  routinesFor: string | null;
  select: (id: string | null) => void;
  showSettings: () => void;
  showRoutines: (projectId: string) => void;
  setAgentView: Dispatch<SetStateAction<{ task: string; agent: string } | null>>;
}) {
  // Keep the view in the URL fragment so a reload lands on it: `#settings`, `#routines…`, else the selected task.
  let viewHash = '';
  if (settingsOpen) viewHash = SETTINGS_HASH;
  else if (routinesFor) viewHash = routinesFor === ALL_ROUTINES ? ROUTINES_HASH : `${ROUTINES_PREFIX}${encodeURIComponent(routinesFor)}`;
  else if (selectedId) viewHash = `${HASH_PREFIX}${encodeURIComponent(selectedId)}${shownAgent ? `&agent=${encodeURIComponent(shownAgent)}` : ''}`;
  // Written when the view changes, never over a fragment the user just navigated to (its event may come
  // after another render). Checked after every render, so the flag a navigation set is spent on the render that shows it:
  // an earlier render's effect that lands after the click (a blur the press caused) leaves it alone.
  const writtenHash = useRef<string | null>(null);
  useEffect(() => {
    if (writtenHash.current === viewHash) return;
    const push = pushViewRef.current;
    // Under federation the owner writes the fragment (with the instance identity); it declines until the route is resolved, and a later render retries.
    if (onRoute && !onRoute(viewHash, push)) return;
    pushViewRef.current = false;
    writtenHash.current = viewHash;
    if (onRoute || window.location.hash === viewHash) return;
    const url = `${window.location.pathname}${window.location.search}${viewHash}`;
    if (push) history.pushState(null, '', url);
    else history.replaceState(null, '', url);
  });
  // A fragment the user navigates to (Back/Forward, a pasted or edited URL) shows its view; the writes
  // above fire neither event. Traversing history fires both, so a fragment already on screen is skipped.
  // Under federation the owner resolves the fragment and announces this instance's part of it.
  // `force`: a click on the view already shown still runs its navigation (it closes the drawer).
  const followHash = useEffectEvent((h: string, force = false) => {
    if (!force && h === viewHash) return;
    const routines = hashRoutines(h);
    if (h === SETTINGS_HASH) showSettings();
    else if (routines) showRoutines(routines);
    else {
      // Back from a subagent to its Thread (or Forward again) keeps the Thread selected.
      const id = hashSelection(h);
      if (id !== selectedId || settingsOpen || routinesFor) select(id);
      setAgentView(hashAgent(h));
    }
    // The entry is already in history.
    pushViewRef.current = false;
  });
  useEffect(() => {
    if (federated) {
      const navigate = (event: Event) => {
        const detail = (event as CustomEvent<{path: string; connection: string; force?: boolean}>).detail;
        if (detail.connection === (api.owner?.id ?? '')) followHash(detail.path, detail.force);
      };
      window.addEventListener('uam-route', navigate);
      return () => window.removeEventListener('uam-route', navigate);
    }
    const onHash = () => followHash(window.location.hash);
    window.addEventListener('hashchange', onHash);
    window.addEventListener('popstate', onHash);
    return () => {
      window.removeEventListener('hashchange', onHash);
      window.removeEventListener('popstate', onHash);
    };
  }, [api, federated]);
}
