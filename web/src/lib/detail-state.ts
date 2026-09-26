import type { BodyReference, DetailFrame, HistoryPage, Item } from '../api';
import { boundItems, itemCursor, mergeItems, TAIL_ITEMS } from './historyWindow.ts';
import { indexPage } from './historyState.ts';

export const bodyKey = (ref: BodyReference) => JSON.stringify([ref.agentId, ref.itemId]);
export type BodyStatus = 'unloaded' | 'loading' | 'loaded' | 'refreshing' | 'unavailable' | 'error';
export interface BodyState { status: BodyStatus; seq: number; floor: number; item?: Item; error?: string }
export interface DetailAgent {
  id: string; items: Item[]; seq: number; before: string; after?: string; loading: boolean; error?: string; pageError?: string;
  recentItems?: Item[]; recentBefore?: string; index?: Item[];
  page?: { before: string; direction?: 'older' | 'newer'; token: number; frames: DetailFrame[]; bytes: number };
  itemSeq: Record<string, number>;
  pending?: { seq: number; items: Item[]; before: string; after?: string; recentItems: Item[]; recentBefore: string; range: boolean; reset?: boolean };
}
export interface DetailState { epoch: string; bodies: Record<string, BodyState>; agent?: DetailAgent }
export type DetailAction =
  | { type: 'release'; key: string }
  | { type: 'interests'; bodies: BodyReference[]; agentId: string }
  | { type: 'invalidate'; versions: Record<string, number> }
  | { type: 'frame'; data: DetailFrame }
  | { type: 'failed'; error: string; terminal: boolean }
  | { type: 'latest'; agentId: string }
  | { type: 'page_start'; agentId: string; before: string; token: number; direction?: 'older' | 'newer' }
  | { type: 'page_done'; agentId: string; before: string; token: number; page: HistoryPage }
  | { type: 'page_failed'; agentId: string; token: number; error: string };
export const emptyDetails = (epoch: string): DetailState => ({ epoch, bodies: {} });

export function detailReducer(state: DetailState, action: DetailAction): DetailState {
  switch (action.type) {
    case 'release': {
      const bodies = { ...state.bodies }; delete bodies[action.key]; return { ...state, bodies };
    }
    case 'interests': {
      const bodies: DetailState['bodies'] = Object.fromEntries(Object.entries(state.bodies).filter(([, body]) => body.status === 'unavailable'));
      for (const ref of action.bodies) {
        const key = bodyKey(ref), old = state.bodies[key];
        bodies[key] = old ? { ...old, status: old.item ? 'refreshing' : 'loading', error: undefined } : { status: 'loading', seq: -1, floor: -1 };
      }
      const agent = action.agentId ? state.agent?.id === action.agentId
        ? { ...state.agent, loading: true, error: undefined, page: undefined, pending: undefined }
        : { id: action.agentId, items: [], seq: -1, before: '', loading: true, itemSeq: {} } : undefined;
      return { ...state, bodies, agent };
    }
    case 'invalidate': {
      let bodies = state.bodies;
      for (const [key, floor] of Object.entries(action.versions)) {
        const old = bodies[key];
        if (!old || floor <= old.floor) continue;
        if (bodies === state.bodies) bodies = { ...bodies };
        bodies[key] = { ...old, floor, status: old.seq >= floor ? 'loaded' : old.item ? 'refreshing' : 'loading' };
      }
      return bodies === state.bodies ? state : { ...state, bodies };
    }
    case 'failed':
      return { ...state, bodies: Object.fromEntries(Object.entries(state.bodies).map(([key, body]) => [key, { ...body, status: action.terminal ? 'error' : body.item ? 'refreshing' : 'loading', error: action.error }])), agent: state.agent ? { ...state.agent, loading: !action.terminal, error: action.terminal ? action.error : undefined } : undefined };
    case 'latest': {
      const agent = state.agent;
      if (!agent || agent.id !== action.agentId || !agent.recentItems) return state;
      return { ...state, agent: { ...agent, items: agent.recentItems, before: agent.recentBefore ?? '', after: '', page: undefined, pageError: undefined } };
    }
    case 'page_start': {
      const agent = state.agent;
      if (!agent || agent.id !== action.agentId || agent.page || (action.direction === 'newer' ? agent.after : agent.before) !== action.before) return state;
      return { ...state, agent: { ...agent, pageError: undefined, page: { before: action.before, direction: action.direction, token: action.token, frames: [], bytes: 0 } } };
    }
    case 'page_failed':
      if (state.agent?.id !== action.agentId || state.agent.page?.token !== action.token) return state;
      return { ...state, agent: { ...state.agent, page: undefined, pageError: action.error } };
    case 'page_done': {
      const agent = state.agent;
      if (!agent || agent.id !== action.agentId || agent.page?.token !== action.token || agent.page.before !== action.before || (action.page.epoch && action.page.epoch !== state.epoch)) return state;
      const direction = agent.page.direction ?? 'older';
      let incoming = action.page.items;
      for (const frame of agent.page.frames) if (frame.seq > action.page.seq) incoming = applyAgent(incoming, frame, agent.id, false);
      const held = new Map([...(agent.recentItems ?? []), ...agent.items].map(item => [item.id, item]));
      incoming = incoming.map(item => (agent.itemSeq[item.id] ?? -1) > action.page.seq ? held.get(item.id) ?? item : item);
      const received = new Map(incoming.map(item => [item.id, item]));
      const existing = agent.items.map(item => received.get(item.id) ?? item);
      const merged = mergeItems(existing, incoming, direction);
      // Older compact servers have no forward cursor. Keep their original paging behavior.
      const bounded = action.page.after === undefined ? { items: merged, droppedBefore: [], droppedAfter: [] } : boundItems(merged, direction);
      let recent = agent.recentItems ? boundItems(agent.recentItems.map(item => received.get(item.id) ?? item), 'newer', TAIL_ITEMS) : undefined;
      let recentItems = recent?.items;
      let detachedAfter = '';
      const itemSeq = { ...agent.itemSeq };
      for (const item of incoming) itemSeq[item.id] = Math.max(itemSeq[item.id] ?? -1, action.page.seq);
      if (direction === 'newer' && action.page.after === '') {
        const ids = new Set(bounded.items.map(item => item.id));
        const newerLive = (recentItems ?? []).filter(item => !ids.has(item.id) && (itemSeq[item.id] ?? -1) > action.page.seq);
        if (newerLive.length && bounded.items.length) detachedAfter = itemCursor(bounded.items.at(-1)!.id);
        recent = boundItems([...bounded.items, ...newerLive], 'newer', TAIL_ITEMS);
        recentItems = recent.items;
      }
      const retained = new Set([...bounded.items, ...(recentItems ?? [])].map(item => item.id));
      for (const id of Object.keys(itemSeq)) if (!retained.has(id)) delete itemSeq[id];
      return { ...state, agent: { ...agent, items: bounded.items, recentItems, recentBefore: recent?.items.length && (recent.droppedBefore.length || (direction === 'newer' && action.page.after === '' && (bounded.droppedBefore.length || agent.before))) ? itemCursor(recent.items[0].id) : agent.recentBefore, itemSeq,
        before: bounded.droppedBefore.length ? itemCursor(bounded.items[0].id) : direction === 'older' ? action.page.before : agent.before,
        after: detachedAfter || (bounded.droppedAfter.length ? itemCursor(bounded.items.at(-1)!.id) : direction === 'newer' ? action.page.after ?? '' : agent.after),
        index: indexPage(agent.index, incoming, direction), page: undefined } };
    }
    case 'frame': break;
  }
  const frame = action.data;
  if (frame.epoch && frame.epoch !== state.epoch) return state;
  if (frame.name === 'detail_reset') return emptyDetails(state.epoch);
  if (frame.name === 'detail_ready') {
    const agent = state.agent;
    if (!agent?.pending || agent.pending.seq !== frame.seq) return state;
    const { range: _range, reset, ...pending } = agent.pending;
    const replaced = new Set([...agent.items, ...(agent.recentItems ?? [])].map(item => item.id));
    const received = new Set([...pending.items, ...pending.recentItems].map(item => item.id));
    const index = reset ? [] : agent.index?.filter(item => !replaced.has(item.id) || received.has(item.id));
    return { ...state, agent: { ...agent, ...pending, index: indexPage(indexPage(index, pending.items), pending.recentItems), loading: false, error: undefined, pending: undefined, itemSeq: Object.fromEntries([...pending.items, ...pending.recentItems].map(item => [item.id, pending.seq])) } };
  }
  if (frame.name === 'body_current') {
    const key = bodyKey({ agentId: frame.agent_id ?? '', itemId: frame.item_id }), old = state.bodies[key];
    if (!old?.item || frame.seq < old.seq) return state;
    return { ...state, bodies: { ...state.bodies, [key]: { ...old, seq: frame.seq, status: frame.seq >= old.floor ? 'loaded' : 'refreshing', error: undefined } } };
  }
  if (frame.name === 'body_unavailable') {
    const key = bodyKey({ agentId: frame.agent_id ?? '', itemId: frame.item_id });
    if (!state.bodies[key]) return state;
    return { ...state, bodies: { ...state.bodies, [key]: { status: 'unavailable', seq: frame.seq, floor: frame.seq } } };
  }
  if (frame.name === 'detail_snapshot' || frame.name === 'detail_page') {
    if (!frame.agent_id || !state.agent || frame.agent_id !== state.agent.id || frame.seq < state.agent.seq) return state;
    const old = state.agent, items = frame.items ?? [];
    if (frame.name === 'detail_page' && old.pending?.seq !== frame.seq) return state;
    let pending: NonNullable<DetailAgent['pending']>;
    if (frame.name === 'detail_snapshot') {
      const recent = boundItems(items, 'newer', TAIL_ITEMS).items;
      const range = !!frame.range && !frame.range_reset;
      pending = { seq: frame.seq, items: range ? [] : boundItems(items, 'newer').items, before: frame.before ?? '', after: '', recentItems: recent, recentBefore: frame.before ?? '', range, reset: frame.range_reset };
    } else {
      const previous = old.pending!;
      const window = previous.range && frame.scope === 'window';
      const merged = boundItems(mergeItems(previous.items, items, 'older'), 'older');
      pending = { ...previous, items: merged.items, before: frame.before ?? '', after: merged.droppedAfter.length ? itemCursor(merged.items.at(-1)!.id) : window && !previous.items.length ? frame.after ?? '' : previous.after };
    }
    return { ...state, agent: { ...old, pending, loading: true, error: undefined } };
  }
  if (frame.name === 'body' || frame.name === 'body_delta' || frame.name === 'body_output') {
    const key = bodyKey({ agentId: frame.agent_id ?? '', itemId: frame.name === 'body' ? frame.item.id : frame.item_id });
    const old = state.bodies[key];
    if (!old || frame.seq < old.seq || (frame.seq === old.seq && frame.name !== 'body')) return state;
    let item = frame.name === 'body' ? frame.item : old.item;
    if (!item) return state;
    if (frame.name === 'body_delta') item = { ...item, text: (item.text ?? '') + frame.text };
    if (frame.name === 'body_output' && item.tool) item = { ...item, tool: { ...item.tool, output: (item.tool.output ?? '') + frame.text } };
    return { ...state, bodies: { ...state.bodies, [key]: { ...old, item, seq: frame.seq, status: frame.seq >= old.floor ? 'loaded' : 'refreshing', error: undefined } } };
  }
  if (frame.name === 'items_trimmed') {
    const bodies = { ...state.bodies };
    for (const item of frame.items) {
      const key = bodyKey({ agentId: item.agent_id ?? '', itemId: item.id });
      if (bodies[key]) bodies[key] = { status: 'unavailable', seq: frame.seq, floor: frame.seq, error: 'This item is no longer retained.' };
    }
    state = { ...state, bodies };
  }
  if (frame.name === 'item' && frame.item.compact) state = detailReducer(state, { type: 'invalidate', versions: { [bodyKey({ agentId: frame.agent_id ?? '', itemId: frame.item.id })]: frame.seq } });
  const agent = state.agent;
  if (!agent || frame.seq <= agent.seq) return state;
  if (frame.name !== 'items_trimmed' && frame.agent_id !== agent.id) return state;
  let page = agent.page;
  let pageError = agent.pageError;
  if (page) {
    const bytes = JSON.stringify(frame).length * 2;
    if (page.frames.length >= 256 || page.bytes + bytes > 2 * 1024 * 1024) {
      page = undefined;
      pageError = 'History changed too quickly. Scroll up to retry.';
    } else page = { ...page, bytes: page.bytes + bytes, frames: [...page.frames, frame] };
  }
  const id = frame.name === 'item' ? frame.item.id : frame.name === 'delta' ? frame.item_id : '';
  if (id && frame.seq <= (agent.itemSeq[id] ?? -1)) return { ...state, agent: { ...agent, page, pageError } };
  const bounded = boundItems(applyAgent(agent.items, frame, agent.id, !agent.after), agent.after ? 'older' : 'newer');
  const tail = boundItems(applyAgent(agent.recentItems ?? agent.items, frame, agent.id, true), 'newer', TAIL_ITEMS);
  const itemSeq = { ...agent.itemSeq };
  const retained = new Set([...bounded.items, ...tail.items].map(item => item.id));
  if (id && retained.has(id)) itemSeq[id] = frame.seq;
  for (const key of Object.keys(itemSeq)) if (!retained.has(key)) delete itemSeq[key];
  let index = agent.index;
  if (frame.name === 'item') index = indexPage(index, [frame.item]);
  if (frame.name === 'items_trimmed') index = applyAgent(index ?? [], frame, agent.id, false);
  return { ...state, agent: { ...agent, page, pageError, itemSeq, items: bounded.items, recentItems: tail.items, index,
    before: bounded.droppedBefore.length ? itemCursor(bounded.items[0].id) : agent.before,
    after: bounded.droppedAfter.length ? itemCursor(bounded.items.at(-1)!.id) : agent.after,
    recentBefore: tail.droppedBefore.length ? itemCursor(tail.items[0].id) : agent.recentBefore } };
}

function applyAgent(items: Item[], frame: DetailFrame, agentId: string, append: boolean): Item[] {
  if (frame.name === 'items_trimmed') {
    const ids = new Set(frame.items.filter(item => (item.agent_id ?? '') === agentId).map(item => item.id));
    return items.filter(item => !ids.has(item.id));
  }
  if (frame.name !== 'item' && frame.name !== 'delta') return items;
  const id = frame.name === 'item' ? frame.item.id : frame.item_id;
  const index = items.findIndex(item => item.id === id);
  if (index < 0) return append && frame.name === 'item' && frame.append ? [...items, frame.item] : items;
  const next = items.slice();
  next[index] = frame.name === 'item' ? frame.item : { ...items[index], text: (items[index].text ?? '') + frame.text };
  return next;
}
