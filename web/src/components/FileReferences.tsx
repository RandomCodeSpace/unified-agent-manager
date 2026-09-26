import { createContext, useCallback, useContext, useEffect, useLayoutEffect, useMemo, useSyncExternalStore, type ReactNode } from 'react';
import { api, subscribeAuthLoss, type Item } from '../api';
import { FileReferences } from '../lib/fileReferences';
import { useDetailVisibility } from './Details';

const Files = createContext<FileReferences | null>(null);
const NO_SUBSCRIBE = () => () => {};

/** Keyed by task, workdir and history/auth lifetime at the Task boundary. */
export function FileReferencesProvider({ sessionId, workdir, generation, active, items, children }: { sessionId: string; workdir: string; generation: string; active: boolean; items: Item[]; children: ReactNode }) {
  // A new lifecycle clears lookup state without remounting the transcript or composer.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const owner = useMemo(() => new FileReferences(workdir, (paths, signal) => api.resolveFiles(sessionId, paths, signal)), [sessionId, workdir, generation, active]);
  useLayoutEffect(() => {
    if (!active) return;
    owner.start();
    const unsubscribe = subscribeAuthLoss(() => owner.stop());
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

export function useFileReference(path: string | undefined, inlineText?: string) {
  const owner = useContext(Files);
  const lookup = path ? owner : null;
  const snapshot = useCallback(() => {
    if (!lookup || !path || (inlineText !== undefined && !lookup.eligible(inlineText, path))) return 0;
    return lookup.cache.get(path) === true ? 2 : 1;
  }, [lookup, path, inlineText]);
  // Unknown and unavailable render alike; unrelated resolver answers need no render.
  const state = useSyncExternalStore(lookup?.subscribe ?? NO_SUBSCRIBE, snapshot);
  return { eligible: state !== 0, exists: state === 2 };
}

export function useFileDemand(text: string) {
  const owner = useContext(Files);
  const visible = useDetailVisibility();
  const attach = useCallback((element: HTMLDivElement | null) => {
    if (element && visible) return owner?.observe(element);
  }, [owner, visible]);
  useEffect(() => { if (visible) owner?.demand(); }, [owner, visible, text]);
  return attach;
}
