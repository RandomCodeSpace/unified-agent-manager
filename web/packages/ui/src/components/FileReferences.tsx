import { useApi } from '../ApiContext';
import { createContext, startTransition, useCallback, useContext, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from 'react';
import { subscribeAuthLoss, type Item } from '../api';
import { FileReferences, taskFileCache } from '../lib/fileReferences';
import { useDetailVisibility } from './Details';

const Files = createContext<FileReferences | null>(null);

/** Keyed by task, workdir and history/auth lifetime at the Task boundary. */
export function FileReferencesProvider({ sessionId, workdir, generation, active, items, children }: Readonly<{ sessionId: string; workdir: string; generation: string; active: boolean; items: Item[]; children: ReactNode }>) {
  const api = useApi();
  // A new lifecycle clears lookup state without remounting the transcript or composer.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const owner = useMemo(() => new FileReferences(workdir, (paths, signal) => api.resolveFiles(sessionId, paths, signal), taskFileCache(api, sessionId, workdir)), [sessionId, workdir, generation, active]);
  useLayoutEffect(() => {
    if (!active) return;
    owner.start();
    const unsubscribe = subscribeAuthLoss(() => { owner.stop(); owner.cache.clear(); });
    window.addEventListener('focus', owner.focus);
    window.addEventListener('scroll', owner.scrolled, true);
    return () => {
      unsubscribe();
      window.removeEventListener('focus', owner.focus);
      window.removeEventListener('scroll', owner.scrolled, true);
      owner.stop();
    };
  }, [owner, active]);
  useLayoutEffect(() => { if (active) owner.setItems('main', items); }, [owner, items, active]);
  return <Files.Provider value={owner}>{children}</Files.Provider>;
}

export function useFileHintItems(source: string, items: readonly Item[], open: boolean) {
  const owner = useContext(Files);
  useLayoutEffect(() => { if (open) owner?.setItems(source, items); else owner?.removeItems(source); }, [owner, source, items, open]);
  useLayoutEffect(() => () => owner?.removeItems(source), [owner, source]);
}

/** Not a candidate, and a candidate not known to be a file: never a path, as paths hold no NUL. */
const INELIGIBLE = '\0';
const UNKNOWN = '\0?';

/** Whether `path` may name a file, whether it is known to, and the file it names: itself, or the one file a bare name means. */
export function useFileReference(path: string | undefined, inlineText?: string) {
  const owner = useContext(Files);
  const lookup = path ? owner : null;
  const snapshot = useCallback(() => {
    if (!lookup || !path || (inlineText !== undefined && !lookup.eligible(inlineText, path))) return INELIGIBLE;
    return lookup.cache.get(path) === true ? lookup.cache.target(path) ?? path : UNKNOWN;
  }, [lookup, path, inlineText]);
  // Unknown and unavailable render alike; unrelated resolver answers need no render. Every render reads the store
  // (answers expire); an answer renders as a transition, since a store React must render at once
  // (useSyncExternalStore) restarts a history page's render under way.
  const state = snapshot();
  const rendered = useRef(state);
  const [, render] = useState(0);
  useEffect(() => { rendered.current = state; });
  useEffect(() => {
    if (!lookup) return;
    const update = () => { if (snapshot() !== rendered.current) startTransition(() => render(n => n + 1)); };
    update();
    return lookup.subscribe(update);
  }, [lookup, snapshot]);
  const exists = state !== INELIGIBLE && state !== UNKNOWN;
  return { eligible: state !== INELIGIBLE, exists, file: exists ? state : undefined };
}

/** A count that grows each time a completed tool in this Thread changes `path`: an open view of it reloads on change. */
export function useFileVersion(path: string | null | undefined): number {
  const owner = useContext(Files);
  const subscribe = useCallback((listener: () => void) => owner?.subscribeChanges(listener) ?? (() => {}), [owner]);
  return useSyncExternalStore(subscribe, () => (owner && path ? owner.changeCount(path) : 0));
}

export function useFileDemand(text: string) {
  const owner = useContext(Files);
  const visible = useDetailVisibility();
  const attach = useCallback((element: HTMLElement | null) => {
    if (element && visible) return owner?.observe(element);
  }, [owner, visible]);
  useEffect(() => { if (visible) owner?.demand(); }, [owner, visible, text]);
  return attach;
}
