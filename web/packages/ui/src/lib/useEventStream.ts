import { useEffect, useEffectEvent, useRef, useState, type Dispatch, type RefObject, type SetStateAction } from 'react';
import { UPDATE_EVENTS, isStatus, type ApiClient, type Aside, type SessionDetail, type SnapshotData, type UpdateData } from '../api';
import { loggedIn, type Action, type Auth } from '../state';
import { keepAside, rereadAsides } from '../components/AskAside';
import { staleDraftKeys } from './drafts';
import { openEventStream } from './eventStream';
import { forgetArchive, retainArchive } from './historyArchive';
import { handleNotice, streamOpened, type Notice } from './notify';
import { sessionsUpdate } from './overlays';
import type { RecentTasks } from './recentTasks';
import { staleReviewKeys } from './review';
import { readJSON } from './storage';

/** An event stream with no snapshot after this long never answered (a proxy holding it, a stalled connection): it is reopened. */
const STREAM_STALL_MS = 20_000;

/** Hoisted: the stream's retry timer sits five functions deep, and its updater would be a sixth. */
export const increment = (n: number) => n + 1;

/** What the Task list's storage sweep last covered: selection restarts the stream, and only a changed membership needs another full sweep. */
export type SweptMembership = { owner: string; tasks: Set<string>; projects: Set<string> } | null;

/**
 * The service's event stream for the selected Thread: it feeds the reducer (and the federation's copy of this
 * machine), keeps this browser's caches, drafts and marks in step with what the service lists, and reopens itself
 * after a stall or a lost connection. Returns the setter that reopens it.
 */
export function useEventStream({ api, auth, selectedId, dispatch, forwardEvent, recentTasks, sweptMembership, confirmedDetail, checkVersion, markViewed, setAuth, setNotice, setFilter, setViewed, FILTER_KEY, LOOKED_KEY, VIEWED_KEY }: {
  api: ApiClient;
  auth: Auth;
  selectedId: string | null;
  dispatch: Dispatch<Action>;
  /** The federation's handler for this machine's events, when it keeps a copy. */
  forwardEvent: ((action: Action) => void) | undefined;
  recentTasks: RecentTasks;
  sweptMembership: RefObject<SweptMembership>;
  confirmedDetail: RefObject<SessionDetail | null>;
  checkVersion: (force: boolean) => void;
  markViewed: (id: string, at: string) => void;
  setAuth: Dispatch<SetStateAction<Auth>>;
  setNotice: Dispatch<SetStateAction<string | null>>;
  setFilter: Dispatch<SetStateAction<string | null>>;
  setViewed: Dispatch<SetStateAction<Record<string, string>>>;
  FILTER_KEY: string;
  LOOKED_KEY: string;
  VIEWED_KEY: string;
}) {
  const [streamKey, setStreamKey] = useState(0);
  // Whether the stream has been down since it last opened.
  const wasDown = useRef(false);

  // The stream hands what it hears to the federation's copy of this machine through whichever handler is current, so
  // the handler appearing or going does not reopen it. Forwarding that starts while it is open (a first connection
  // added) reopens it once, so the copy starts from a snapshot.
  const onEvent = useEffectEvent((action: Action) => forwardEvent?.(action));
  const [forwarding, setForwarding] = useState(!!forwardEvent);
  if (forwarding !== !!forwardEvent) {
    setForwarding(!!forwardEvent);
    if (forwardEvent) setStreamKey(increment);
  }

  // One EventSource at a time, scoped to the selected session.
  useEffect(() => {
    if (auth !== 'in') return;
    const es = openEventStream(api.eventsUrl(selectedId));
    let alive = true;
    let retry: number | undefined;
    // Opening a stream is a load; a retry after a failure keeps that failure on screen until the snapshot.
    if (!wasDown.current) { dispatch({ type: 'connection', status: 'connecting' }); onEvent?.({ type: 'connection', status: 'connecting' }); }
    // A stream that never sends its snapshot would leave the load on screen for good: it is a lost connection, shown and reopened.
    const stalled = window.setTimeout(() => {
      if (!alive) return;
      es.close();
      sweptMembership.current = null;
      wasDown.current = true;
      dispatch({ type: 'connection', status: 'reconnecting' });
      onEvent?.({ type: 'connection', status: 'reconnecting' });
      setStreamKey(increment);
    }, STREAM_STALL_MS);
    es.onopen = () => {
      if (!alive) return;
      // The snapshot confirms this connection; opening TCP alone does not.
      if (wasDown.current) {
        wasDown.current = false;
        checkVersion(true);
      }
    };
    es.onerror = () => {
      if (!alive) return;
      // Missed removal events during a disconnect must not suppress the reconnect's cleanup.
      sweptMembership.current = null;
      wasDown.current = true;
      if (es.readyState !== EventSource.CLOSED) {
        dispatch({ type: 'connection', status: 'reconnecting' });
        onEvent?.({ type: 'connection', status: 'reconnecting' });
        return;
      }
      window.clearTimeout(stalled);
      // The browser gave up (non-200 response). A selected Task the service does not know (a stale
      // link, one deleted elsewhere) is dropped with a notice; otherwise re-check auth, then reopen.
      const later = () => {
        if (!alive) return;
        retry = window.setTimeout(() => { if (alive) setStreamKey(increment); }, 5000);
      };
      const reconnect = () => {
        if (!alive) return;
        dispatch({ type: 'connection', status: 'offline' });
        onEvent?.({ type: 'connection', status: 'offline' });
        api
          .auth()
          .then((r) => { if (alive) { if (loggedIn(r)) later(); else { recentTasks.clear(); confirmedDetail.current = null; dispatch({ type: 'reset' }); setAuth('out'); } } })
          .catch(later);
      };
      const opened = selectedId;
      if (!opened) return reconnect();
      api.session(opened).then(reconnect, (err: unknown) => {
        if (!alive) return;
        if (!isStatus(err, 404)) return reconnect();
        recentTasks.remove(opened);
        forgetArchive(api.cacheKey(opened));
        dispatch({ type: 'select', id: null });
        setNotice('That thread no longer exists.');
      });
    };
    const selected = selectedId;
    // Transcript updates wait for the next animation frame and land in one dispatch; any other
    // frame (and a snapshot) flushes them first, so the order on the wire is kept.
    let queue: UpdateData[] = [];
    let frame = 0;
    const flush = () => {
      cancelAnimationFrame(frame);
      frame = 0;
      if (!queue.length) return;
      const data = queue;
      queue = [];
      dispatch({ type: 'updates', data });
    };
    es.addEventListener('snapshot', (e) => {
      if (!alive) return;
      window.clearTimeout(stalled);
      const data = JSON.parse((e as MessageEvent).data) as SnapshotData;
      flush();
      confirmedDetail.current = null;
      recentTasks.confirm(data);
      // Asides kept while the stream was down come with the next read.
      rereadAsides(api);
      // Open the Task as soon as its snapshot arrives; a transition would delay readiness.
      onEvent?.({ type: 'snapshot', data });
      dispatch({ type: 'snapshot', data });
      setFilter((current) => {
        if (!current || data.projects.some((p) => p.id === current)) return current;
        localStorage.removeItem(FILTER_KEY);
        return null;
      });
      if (data.session?.id === selected) markViewed(selected, data.session.updated_at);
      // This stream is new to the service: it learns again which Task this tab shows.
      streamOpened();
      // Composer drafts of Tasks that no longer exist go with them.
      const taskIds = data.sessions.map((s) => s.id);
      const projectIds = data.projects.map((p) => p.id);
      const owner = api.storageKey('');
      const previous = sweptMembership.current;
      if (!previous || previous.owner !== owner || previous.tasks.size !== taskIds.length || previous.projects.size !== projectIds.length || !taskIds.every(id => previous.tasks.has(id)) || !projectIds.every(id => previous.projects.has(id))) {
        // A failed sweep must retry even if the next snapshot lists the same IDs.
        sweptMembership.current = null;
        let swept = true;
        try {
          const prefix = api.owner ? owner : '';
          const storageKeys = Object.keys(localStorage).filter(key => !prefix || key.startsWith(prefix)).map(key => prefix ? key.slice(prefix.length) : key);
          for (const key of staleDraftKeys(storageKeys, taskIds, projectIds)) localStorage.removeItem(api.storageKey(key));
          for (const key of staleReviewKeys(Object.keys(localStorage).filter(key => api.owner ? key.startsWith(`uam.review.${api.cacheKey('')}`) : !key.startsWith('uam.review.@uam:')), taskIds.map(id => api.cacheKey(id)))) localStorage.removeItem(key);
        } catch {
          // Storage unavailable: nothing to sweep.
          swept = false;
        }
        if (!retainArchive(taskIds.map(id => api.cacheKey(id)), api.owner ? api.cacheKey('') : '')) swept = false;
        if (swept) sweptMembership.current = { owner, tasks: new Set(taskIds), projects: new Set(projectIds) };
      }
      // So are their read and last-looked marks.
      const live = new Set(taskIds);
      try {
        const looked = readJSON<Record<string, string>>(LOOKED_KEY, {});
        if (!Object.keys(looked).every((id) => live.has(id))) localStorage.setItem(LOOKED_KEY, JSON.stringify(Object.fromEntries(Object.entries(looked).filter(([id]) => live.has(id)))));
      } catch {
        // Storage unavailable: nothing to prune.
      }
      setViewed((v) => {
        if (Object.keys(v).every((id) => live.has(id))) return v;
        const next = Object.fromEntries(Object.entries(v).filter(([id]) => live.has(id)));
        try {
          localStorage.setItem(VIEWED_KEY, JSON.stringify(next));
        } catch {
          // Storage unavailable: the marks shrink for this page only.
        }
        return next;
      });
    });
    // An aside kept with the selected Task, from this page or another (AskAside).
    es.addEventListener('aside', (e) => {
      if (!alive) return;
      const data = JSON.parse((e as MessageEvent).data) as { session_id: string; aside: Aside };
      keepAside(api, data.session_id, data.aside);
    });
    // A Task needs you or finished: a notification, when this browser asked for them (lib/notify.ts).
    es.addEventListener('notify', (e) => {
      if (alive && !api.owner) void handleNotice(JSON.parse((e as MessageEvent).data) as Notice);
    });
    for (const name of UPDATE_EVENTS) {
      es.addEventListener(name, (e) => {
        if (!alive) return;
        const data = { name, ...JSON.parse((e as MessageEvent).data) } as UpdateData;
        if (['session', 'session_removed', 'project', 'project_removed', 'settings', 'usage'].includes(data.name)) onEvent?.({ type: 'update', data });
        recentTasks.invalidate(data);
        if (data.name === 'session_removed' || data.name === 'project_removed') sweptMembership.current = null;
        if (data.name === 'session_removed') forgetArchive(api.cacheKey(data.session_id));
        // A rewind may drop recorded events that cached archive pages still hold.
        if (data.name === 'session' && data.session.rewind) forgetArchive(api.cacheKey(data.session.id));
        // Invalidations precede React's commit. A click in between must not
        // put the old confirmed reference straight back into the cache.
        const current = confirmedDetail.current;
        if (current && (((data.name === 'history' || data.name === 'items_trimmed' || data.name === 'session_removed') && current.id === data.session_id)
          || (data.name === 'project_removed' && current.project_id === data.project_id))) confirmedDetail.current = null;
        // A hidden tab gets no animation frames: apply at once there.
        if ((data.name === 'delta' || data.name === 'tool_output' || data.name === 'item') && !document.hidden) {
          queue.push(data);
          frame ||= requestAnimationFrame(flush);
          return;
        }
        flush();
        // Task row changes render in a transition; everything else commits at once.
        if (data.name === 'session' || data.name === 'session_removed') sessionsUpdate(() => dispatch({ type: 'update', data }));
        else dispatch({ type: 'update', data });
        if (data.name === 'project_removed') {
          setFilter((current) => {
            if (current !== data.project_id) return current;
            localStorage.removeItem(FILTER_KEY);
            return null;
          });
        }
        if (data.name === 'session' && data.session.id === selected) markViewed(selected, data.session.updated_at);
      });
    }
    return () => {
      alive = false;
      es.close();
      window.clearTimeout(retry);
      window.clearTimeout(stalled);
      // Deltas still queued belong to this stream; the next one starts with a snapshot.
      cancelAnimationFrame(frame);
    };
  }, [auth, selectedId, streamKey, markViewed, checkVersion, recentTasks, api, FILTER_KEY, LOOKED_KEY, VIEWED_KEY, dispatch, setAuth, setNotice, setFilter, setViewed, sweptMembership, confirmedDetail]);
  return setStreamKey;
}
