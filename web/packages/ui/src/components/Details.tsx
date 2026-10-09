import { useApi } from '../ApiContext';
import { createContext, useCallback, useContext, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from 'react';
import { type BodyReference, type DetailFrame, type Item, type SessionDetail } from '../api';
import { bodyKey, detailReducer, emptyDetails, type BodyState, type DetailAction, type DetailState } from '../lib/detail-state';
import { describeError } from '../api';
import { itemCursor } from '../lib/historyWindow';
import { EditDiffCache } from '../lib/edit-diff-cache';

/** Mounted children of a collapsing group retain their preference, but no interest. */
const Visible = createContext(true);
export const useDetailVisibility = () => useContext(Visible);
export function DetailVisibility({ open, children }: Readonly<{ open: boolean; children: ReactNode }>) {
  const parent = useContext(Visible);
  return <Visible.Provider value={parent && open}>{children}</Visible.Provider>;
}

class DetailStore {
  value: DetailState;
  listeners = new Set<() => void>();
  constructor(epoch: string) { this.value = emptyDetails(epoch); }
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  action = (action: DetailAction) => {
    const next = detailReducer(this.value, action);
    if (next === this.value) return;
    this.value = next;
    this.listeners.forEach(listener => listener());
  };
  clear(epoch: string) { this.value = emptyDetails(epoch); this.listeners.forEach(listener => listener()); }
}
interface Interest { ref: BodyReference; visible: boolean; order: number }
interface DetailContextValue {
  compact: boolean;
  store: DetailStore;
  register: (ref: BodyReference, element: Element | null) => () => void;
  openAgent: (id: string) => () => void;
  retry: () => void;
  read: (item: Item) => Promise<Item>;
  sessionId: string;
  disclosures: Map<string, boolean>;
  editDiffs: EditDiffCache;
  editScope: string;
  active: boolean;
  epoch: string;
}
const Details = createContext<DetailContextValue | null>(null);
const EMPTY_BODY: BodyState = { status: 'unloaded', seq: -1, floor: -1 };
const NO_SUBSCRIBE = () => () => {};

export function DetailsProvider({ session, active, generation, versions, onAuthLost, children }: Readonly<{
  session: SessionDetail; active: boolean; generation: number; versions: Record<string, number>; onAuthLost: () => void; children: ReactNode;
}>) {
  const api = useApi();
  const compact = session.representation === 'compact-v1' && session.detail_stream === true && !!session.epoch;
  const [store] = useState(() => new DetailStore(session.epoch ?? ''));
  const [disclosures] = useState(() => new Map<string, boolean>());
  const [editDiffs] = useState(() => new EditDiffCache());
  const interests = useRef(new Map<symbol, Interest>());
  const blocked = useRef(new Set<string>());
  const agentId = useRef('');
  const order = useRef(0);
  const tick = useRef(0);
  const mounted = useRef(true);
  const retryPending = useRef(false);
  const [interestKey, setInterestKey] = useState(JSON.stringify(['', []]));
  const [retryRevision, setRetryRevision] = useState(0);
  const reads = useRef(new Map<string, { controller: AbortController; promise: Promise<Item> }>());
  const lifetime = useRef(0);
  const authLost = useRef(onAuthLost);
  useLayoutEffect(() => { authLost.current = onAuthLost; }, [onAuthLost]);
  const changed = useCallback(() => {
    if (!mounted.current || tick.current) return;
    tick.current = window.setTimeout(() => {
      tick.current = 0;
      const desired = [...interests.current.values()].sort((a, b) => Number(b.visible) - Number(a.visible) || b.order - a.order);
      const admitted = new Map<string, BodyReference>();
      for (const interest of desired) {
        const key = bodyKey(interest.ref);
        if (!blocked.current.has(key) && admitted.size < 8) admitted.set(key, interest.ref);
      }
      const bodies = [...admitted.values()].sort((a, b) => bodyKey(a).localeCompare(bodyKey(b)));
      setInterestKey(JSON.stringify([agentId.current, bodies]));
      if (retryPending.current) { retryPending.current = false; setRetryRevision(value => value + 1); }
    }, 0);
  }, []);
  const register = useCallback((ref: BodyReference, element: Element | null) => {
    const token = Symbol();
    const key = bodyKey(ref);
    blocked.current.delete(key);
    const rect = element?.getBoundingClientRect();
    interests.current.set(token, { ref, order: ++order.current, visible: !rect || (rect.bottom >= 0 && rect.top <= window.innerHeight) });
    changed();
    const observer = element ? new IntersectionObserver(([entry]) => {
      const interest = interests.current.get(token);
      if (interest && interest.visible !== entry.isIntersecting) { interest.visible = entry.isIntersecting; changed(); }
    }, { rootMargin: '200px' }) : undefined;
    if (element) observer?.observe(element);
    return () => { observer?.disconnect(); interests.current.delete(token); if (![...interests.current.values()].some(interest => bodyKey(interest.ref) === key)) { blocked.current.delete(key); store.action({ type: 'release', key }); } changed(); };
  }, [changed, store]);
  const openAgent = useCallback((id: string) => {
    agentId.current = id; changed();
    return () => { if (agentId.current === id) { agentId.current = ''; changed(); } };
  }, [changed]);
  const retry = useCallback(() => { blocked.current.clear(); retryPending.current = true; changed(); }, [changed]);

  useLayoutEffect(() => {
    lifetime.current++;
    disclosures.clear();
    editDiffs.clear();
    for (const read of reads.current.values()) read.controller.abort();
    reads.current.clear();
    blocked.current.clear();
    store.clear(session.epoch ?? '');
    changed();
  }, [session.id, session.epoch, generation, store, changed, disclosures, editDiffs, api]);
  useEffect(() => {
    mounted.current = true;
    changed();
    const pendingReads = reads.current;
    return () => {
      mounted.current = false;
      window.clearTimeout(tick.current);
      tick.current = 0;
      for (const read of pendingReads.values()) read.controller.abort();
      pendingReads.clear();
      editDiffs.clear();
    };
  }, [changed, editDiffs]);
  useLayoutEffect(() => { if (!active) editDiffs.clear(); }, [active, editDiffs]);
  useLayoutEffect(() => { store.action({ type: 'invalidate', versions }); }, [store, versions]);

  useEffect(() => {
    if (!compact || !active) return;
    const [agent, bodies] = JSON.parse(interestKey) as [string, BodyReference[]];
    store.action({ type: 'interests', bodies, agentId: agent });
    store.action({ type: 'invalidate', versions });
    if (!agent && !bodies.length) return;
    let alive = true;
    let es: EventSource | undefined;
    let retryTimer: number | undefined;
    let attempts = 0;
    const open = () => {
      if (!alive) return;
      const first = store.value.agent?.id === agent ? store.value.agent.items[0]?.id : undefined;
      const before = first ? itemCursor(first) : undefined;
      const last = store.value.agent?.id === agent ? store.value.agent.items.at(-1)?.id : undefined;
      const until = last ? itemCursor(last) : undefined;
      const covered = bodies.map(ref => {
        const body = store.value.bodies[bodyKey(ref)];
        return body?.item && body.seq >= 0 ? { ...ref, coveredSeq: body.seq } : ref;
      });
      es = new EventSource(api.detailEventsUrl(session.id, agent, covered, before, session.epoch, until));
      const current = es;
      for (const name of ['detail_snapshot', 'detail_page', 'body', 'body_delta', 'body_output', 'body_current', 'body_unavailable', 'detail_ready', 'detail_reset', 'item', 'delta', 'items_trimmed'] as const) {
        current.addEventListener(name, event => {
          if (!alive || es !== current) return;
          const data = { name, ...JSON.parse((event as MessageEvent).data) } as DetailFrame;
          if (data.session_id !== session.id) return;
          if (data.epoch && data.epoch !== session.epoch) {
            current.close();
            store.action({ type: 'failed', terminal: true, error: 'The service restarted. Waiting for the task to reconnect.' });
            return;
          }
          if (name === 'detail_ready') attempts = 0;
          if (data.name === 'body_unavailable') blocked.current.add(bodyKey({ agentId: data.agent_id ?? '', itemId: data.item_id }));
          if (data.name === 'items_trimmed') for (const item of data.items) {
            const key = bodyKey({ agentId: item.agent_id ?? '', itemId: item.id });
            if ([...interests.current.values()].some(interest => bodyKey(interest.ref) === key)) blocked.current.add(key);
          }
          store.action({ type: 'frame', data });
          if (data.name === 'body_unavailable' || data.name === 'items_trimmed') changed();
          if (data.name === 'detail_reset') current.close(); // main history generation reopens after confirmation
        });
      }
      current.onerror = () => {
        if (!alive || es !== current) return;
        const terminal = current.readyState === EventSource.CLOSED;
        current.close(); // explicit retry is the sole retry owner
        const later = () => {
          if (!alive || es !== current) return;
          const exhausted = attempts >= 4;
          store.action({ type: 'failed', terminal: exhausted, error: exhausted ? 'Details are unavailable. Retry after checking the task.' : 'Reconnecting details…' });
          if (!exhausted) retryTimer = window.setTimeout(open, [1000, 2000, 5000, 10000][attempts++]);
        };
        if (terminal) {
          void api.auth().then(auth => { if (alive && es === current) { if (!auth.authenticated) authLost.current(); else later(); } }).catch(later);
        } else later();
      };
    };
    open();
    return () => { alive = false; es?.close(); window.clearTimeout(retryTimer); };
    // Revision permits explicit retry; streamed item versions never reconnect the stream.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [compact, active, session.id, session.epoch, generation, interestKey, retryRevision, store, changed]);

  const read = useCallback((item: Item): Promise<Item> => {
    if (!item.compact && !item.clipped) return Promise.resolve(item);
    const ref = { agentId: item.agent_id ?? '', itemId: item.id };
    const key = bodyKey(ref);
    const current = store.value.bodies[key];
    // A clipped item's streamed body may be the held copy (a body over the frame limit); the route serves it whole.
    if (current?.status === 'loaded' && current.item && !item.clipped) return Promise.resolve(current.item);
    const pending = reads.current.get(key);
    if (pending) return pending.promise;
    const controller = new AbortController();
    const identity = lifetime.current;
    const timer = window.setTimeout(() => controller.abort(new Error('Loading details timed out. Try again.')), 10000);
    const promise = api.itemBody(session.id, item.id, ref.agentId, controller.signal).then(data => {
      if (!mounted.current || identity !== lifetime.current || controller.signal.aborted || data.session_id !== session.id || data.epoch !== session.epoch) throw new DOMException('The task changed.', 'AbortError');
      return data.item; // one-off copy does not populate the live body map
    }).finally(() => { window.clearTimeout(timer); if (reads.current.get(key)?.controller === controller) reads.current.delete(key); });
    reads.current.set(key, { controller, promise });
    return promise;
    // The store is read when called: a dependency on its bodies would renew the context at every
    // frame, and each renewal unregisters every body, whose release drops it from the store.
  }, [store, api, session.id, session.epoch]);
  const editScope = JSON.stringify([api.cacheKey(session.id), api.owner?.generation, session.epoch, generation]);
  const context = useMemo(() => ({ compact, store, register, openAgent, retry, read, sessionId: session.id, disclosures, editDiffs, editScope, active, epoch: session.epoch ?? '' }), [compact, store, register, openAgent, retry, read, session.id, disclosures, editDiffs, editScope, active, session.epoch]);
  return <Details.Provider value={context}>{children}</Details.Provider>;
}

/** Native patches live outside transcript bodies and the persisted history cache. */
export function useEditDiff(item: Item, eventId: string, path: string, open: boolean) {
  const context = useContext(Details);
  const visible = useContext(Visible);
  const api = useApi();
  const cache = context?.editDiffs;
  const sessionId = context?.sessionId ?? '';
  const epoch = context?.epoch ?? '';
  const itemId = item.id;
  const agentId = item.agent_id ?? '';
  const key = JSON.stringify([context?.editScope, agentId, itemId, eventId, path]);
  const get = useCallback(() => cache?.get(key), [cache, key]);
  const state = useSyncExternalStore(cache?.subscribe ?? NO_SUBSCRIBE, get, get);
  const enabled = !!context?.active && visible && open;
  const read = useCallback(async (signal: AbortSignal) => {
    const data = await api.itemDiff(sessionId, itemId, agentId, eventId, path, signal);
    if (data.session_id !== sessionId || data.epoch !== epoch || data.item_id !== itemId || data.agent_id !== agentId || data.event_id !== eventId || data.path !== path) throw new Error('The recorded edit changed.');
    return data;
  }, [api, sessionId, epoch, itemId, agentId, eventId, path]);
  useEffect(() => {
    if (!enabled || !cache) return;
    cache.load(key, read);
    return () => cache.cancel(key);
  }, [enabled, cache, key, read]);
  return { state: enabled ? state : undefined, visible: enabled, retry: () => { if (enabled) cache!.load(key, read, true); } };
}

/** Reads an item's whole body once (the item itself when it is whole); absent outside a Task. */
export function useBodyRead(): ((item: Item) => Promise<Item>) | undefined {
  return useContext(Details)?.read;
}

export function useItemBody(item: Item, open: boolean) {
  const context = useContext(Details);
  const parentVisible = useContext(Visible);
  const element = useRef<HTMLDivElement>(null);
  const ref = useCallback((node: HTMLDivElement | null) => { element.current = node; }, []);
  const deferred = !!context?.compact && !!item.compact;
  const key = bodyKey({ agentId: item.agent_id ?? '', itemId: item.id });
  const get = useCallback(() => context?.store.value.bodies[key] ?? EMPTY_BODY, [context, key]);
  const body = useSyncExternalStore(context?.store.subscribe ?? NO_SUBSCRIBE, get, get);
  useEffect(() => {
    if (!deferred || !open || !parentVisible) return;
    return context!.register({ agentId: item.agent_id ?? '', itemId: item.id }, element.current);
  }, [context, deferred, open, parentVisible, item.agent_id, item.id]);
  return { item: deferred ? body.item : item, body: deferred ? body : undefined, attach: ref, retry: context?.retry };
}
/** What a body still on its way says: a refresh, a wait for the scroll, or the read itself. */
function loadingText(status: BodyState['status']): string {
  if (status === 'refreshing') return 'Refreshing details…';
  if (status === 'unloaded') return 'Details load as you scroll here…';
  return 'Loading details…';
}
export function BodyNotice({ body, retry }: Readonly<{ body?: BodyState; retry?: () => void }>) {
  if (!body || body.status === 'loaded') return null;
  if (body.status === 'unavailable') return <p role="status" className="text-caption text-muted">This item is no longer retained.</p>;
  if (body.status === 'error') return <p role="alert" className="text-caption text-error">{body.error} <button type="button" onClick={retry} className="underline">Retry</button></p>;
  return <p role="status" className="text-caption text-muted">{loadingText(body.status)}</p>;
}
export function useBodyCopy(item: Item, copy: (text: string) => void) {
  const context = useContext(Details);
  const [error, setError] = useState('');
  const alive = useRef(true);
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  const run = (field: 'input' | 'output' | 'text') => {
    setError('');
    void (context?.read(item) ?? Promise.resolve(item)).then(body => {
      if (alive.current) copy(field === 'text' ? body.text ?? '' : body.tool?.[field] ?? '');
    }).catch(reason => { if (alive.current && !(reason instanceof DOMException && reason.name === 'AbortError')) setError(describeError(reason)); });
  };
  return { copyBody: run, copyError: error };
}
/** How the service marks the end of a text it shortened. */
const CUT = '\n[truncated by uam]';
/** At most this much of a clipped message renders, as Markdown, before its whole text is asked for: the service may hold megabytes of it, and parsing that would hold the page for seconds. */
const HELD_PREVIEW = 64 << 10;

/** `text` cut to HELD_PREVIEW, at a line break when one is near, never inside a surrogate pair. */
function heldPreview(text: string): string {
  if (text.length <= HELD_PREVIEW) return text;
  const line = text.lastIndexOf('\n', HELD_PREVIEW);
  let cut = line > HELD_PREVIEW / 2 ? line : HELD_PREVIEW;
  const unit = text.charCodeAt(cut - 1);
  if (unit >= 0xd800 && unit <= 0xdbff) cut--;
  return text.slice(0, cut);
}

export type WholeText = ReturnType<typeof useWholeText>;
/**
 * A message the service holds shortened (`clipped`): `text` is the part held, without the cut
 * marker, until `show` reads the whole text from the item route. Nothing is read before that, as
 * the whole text can run to many megabytes; `read` reads it for a copy without showing it.
 */
export function useWholeText(item: Item) {
  const context = useContext(Details);
  const [state, setState] = useState<{ status: 'held' | 'loading' | 'whole' | 'error'; text?: string; error?: string }>({ status: 'held' });
  const held = useMemo(() => heldPreview(item.text?.endsWith(CUT) ? item.text.slice(0, -CUT.length) : item.text ?? ''), [item.text]);
  const alive = useRef(true);
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  const read = async () => {
    try {
      const text = (await (context?.read(item) ?? item)).text ?? '';
      // The route answers with the held copy when the record cannot be read.
      if (text.endsWith(CUT)) throw new Error('The full message could not be read.');
      return text;
    } catch (reason) {
      if (alive.current) setState(reason instanceof DOMException && reason.name === 'AbortError' ? { status: 'held' } : { status: 'error', error: describeError(reason) });
      throw reason;
    }
  };
  const show = () => {
    setState({ status: 'loading' });
    read().then(text => { if (alive.current) setState({ status: 'whole', text }); }, () => {});
  };
  return { ...state, text: state.status === 'whole' ? state.text! : held, show, read };
}

export function useDetailAgent(id: string, open: boolean) {
  const context = useContext(Details);
  const visible = useContext(Visible);
  const get = useCallback(() => context?.store.value.agent?.id === id ? context.store.value.agent : undefined, [context, id]);
  const agent = useSyncExternalStore(context?.store.subscribe ?? NO_SUBSCRIBE, get, get);
  useEffect(() => {
    if (!context?.compact || !open || !visible) return;
    return context.openAgent(id);
  }, [context, id, open, visible]);
  return { compact: context?.compact ?? false, agent, store: context?.store, retry: context?.retry };
}

/** Keep only disclosure booleans when a history row is evicted. */
export function useDisclosure(key: string) {
  const context = useContext(Details);
  const [open, setOpen] = useState(() => context?.disclosures.get(key) ?? false);
  const update = useCallback((value: boolean | ((current: boolean) => boolean)) => {
    setOpen(current => {
      const next = typeof value === 'function' ? value(current) : value;
      if (context) {
        context.disclosures.delete(key);
        context.disclosures.set(key, next);
        while (context.disclosures.size > 2000) context.disclosures.delete(context.disclosures.keys().next().value!);
      }
      return next;
    });
  }, [context, key]);
  return [open, update] as const;
}
