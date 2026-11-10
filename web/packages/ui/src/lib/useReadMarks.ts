import { useCallback, useEffect, useEffectEvent, useMemo, useState, type RefObject } from 'react';
import type { ApiClient, SessionSummary } from '../api';
import { readJSON, writeJSON } from './storage';
import { newsReader } from './tasks';

/**
 * This browser's read marks: what each Thread showed when the owner last had it on screen (`viewed`, the unread
 * dots), and when they last looked at it (`opened.mark`, where "Since you left" starts). The open Thread is marked
 * while the page is visible and no view covers it (`viewShown`).
 */
export function useReadMarks({ api, selectedId, sessions, viewOpen, viewShown, LOOKED_KEY, VIEWED_KEY }: {
  api: ApiClient;
  selectedId: string | null;
  sessions: SessionSummary[];
  /** Settings or Routines cover the selected Thread. */
  viewOpen: boolean;
  /** The same, as of the last commit: read when a frame arrives. */
  viewShown: RefObject<boolean>;
  LOOKED_KEY: string;
  VIEWED_KEY: string;
}) {
  const VIEWED_SINCE_KEY = api.storageKey('uam.viewedSince');
  const [viewed, setViewed] = useState<Record<string, string>>(() => readJSON(VIEWED_KEY, {}));
  // A Task never opened in this browser counts as read up to the first visit, so a reload (the phone drops pages often) never forgets what finished meanwhile.
  const [loadedAt] = useState(() => {
    const since = readJSON<string | null>(VIEWED_SINCE_KEY, null);
    if (since) return since;
    const now = new Date().toISOString();
    writeJSON(VIEWED_SINCE_KEY, now);
    return now;
  });
  // When the owner last looked at each Task: while it is on screen, and as they leave it.
  const markLooked = useCallback((id: string) => {
    try {
      localStorage.setItem(LOOKED_KEY, JSON.stringify({ ...readJSON<Record<string, string>>(LOOKED_KEY, {}), [id]: new Date().toISOString() }));
    } catch {
      // Storage unavailable: no mark, so no "Since you left".
    }
  }, [LOOKED_KEY]);

  // The selected task counts as viewed as long as it is on screen: every frame that carries
  // its updated_at marks it, so nothing it does while open shows as unread later. A hidden
  // page, or Settings over the Task, shows nothing: what the Task does meanwhile stays unread
  // until the page is back.
  const markViewed = useCallback((id: string, at: string) => {
    if (document.visibilityState !== 'visible' || viewShown.current) return;
    markLooked(id);
    setViewed((v) => {
      if (v[id] === at) return v;
      const next = { ...v, [id]: at };
      writeJSON(VIEWED_KEY, next);
      return next;
    });
  }, [VIEWED_KEY, markLooked, viewShown]);
  // Leaving a Task, hiding the page or closing it ends the look at the Task on screen; so does opening Settings over it.
  useEffect(() => {
    const id = selectedId;
    if (!id || viewOpen) return;
    const onHide = () => markLooked(id);
    const onVisibility = () => { if (document.hidden) markLooked(id); };
    document.addEventListener('visibilitychange', onVisibility);
    window.addEventListener('pagehide', onHide);
    return () => {
      document.removeEventListener('visibilitychange', onVisibility);
      window.removeEventListener('pagehide', onHide);
      // A hidden page recorded the leave when it hid; it shows nothing since.
      if (!document.hidden) markLooked(id);
    };
  }, [selectedId, viewOpen, markLooked]);
  const markShown = useEffectEvent(() => {
    const shown = sessions.find((s) => s.id === selectedId);
    if (shown) markViewed(shown.id, shown.updated_at);
  });
  useEffect(() => {
    document.addEventListener('visibilitychange', markShown);
    return () => document.removeEventListener('visibilitychange', markShown);
  }, []);
  const hasNews = useMemo(() => newsReader(selectedId, viewed, loadedAt), [selectedId, viewed, loadedAt]);
  // The owner's last look at the Task just opened, read before this visit marks it: "Since you left" starts there.
  // A Settings or Routines visit is not leaving: the Thread stays mounted under it and comes back as it was.
  const [opened, setOpened] = useState<{ id: string | null; mark?: string }>({ id: null });
  if (opened.id !== selectedId) setOpened({ id: selectedId, mark: selectedId ? readJSON<Record<string, string>>(LOOKED_KEY, {})[selectedId] ?? viewed[selectedId] : undefined });
  return { setViewed, markViewed, hasNews, opened };
}
