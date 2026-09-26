import type { HistoryPage, Item, SessionDetail } from '../api';
import { boundItems, idleSteerEcho, itemCursor, mergeItems, placeIdleSteerEchoes, TAIL_ITEMS } from './historyWindow.ts';
import { isFileDeclaration, questionOf } from './transcript.ts';

/** Identity/grouping records survive page eviction; no deferred or chat text does. */
export function indexItem(item: Item): Item {
  const tool = item.tool;
  return {
    id: item.id, kind: item.kind, time: item.time, ended_at: item.ended_at, agent_id: item.agent_id, delivery: item.delivery,
    ...(item.steer_status ? { steer_status: item.steer_status } : {}),
    compact: item.compact,
    ...(tool ? { tool: { name: tool.name, status: tool.status, question_outcome: tool.question_outcome ?? questionOf(tool, undefined, true)?.outcome, ...(isFileDeclaration(item) ? { declaration_boundary: true } : {}) } } : {}),

  };
}
/** A provider idle echo starts a new turn at its actual position, after any old-turn output. */
export function upsertTranscriptItem(items: Item[], item: Item): Item[] {
  const at = items.findIndex(previous => previous.id === item.id);
  if (at < 0) return [...items, item];
  if (idleSteerEcho(items[at], item)) return [...items.slice(0, at), ...items.slice(at + 1), item];
  const next = items.slice();
  next[at] = item;
  return next;
}
export function indexPage(index: Item[] = [], items: Item[], direction: 'older' | 'newer' = 'newer'): Item[] {
  const incoming = items.map(indexItem), fresh = new Map(incoming.map(item => [item.id, item]));
  const known = new Map(index.map(item => [item.id, item]));
  const moved = new Set(incoming.filter(item => {
    const previous = known.get(item.id);
    return previous && idleSteerEcho(previous, item);
  }).map(item => item.id));
  const added = incoming.filter(item => !known.has(item.id));
  const updated = index.filter(item => !moved.has(item.id)).map(item => fresh.get(item.id) ?? item);
  const echoes = incoming.filter(item => moved.has(item.id));
  const merged = direction === 'older' ? [...added, ...updated, ...echoes] : [...updated, ...incoming.filter(item => !known.has(item.id) || moved.has(item.id))];
  return placeIdleSteerEchoes(merged, incoming, moved).slice(-2000);
}
export function initialWindow(detail: SessionDetail): SessionDetail {
  if (detail.representation !== 'compact-v1') return detail;
  const bounded = boundItems(detail.items, 'newer');
  const tail = boundItems(detail.items, 'newer', TAIL_ITEMS);
  return { ...detail, items: bounded.items, history_before: bounded.droppedBefore.length ? itemCursor(bounded.items[0].id) : detail.history_before, history_after: '', recent_items: tail.items, recent_before: tail.droppedBefore.length ? itemCursor(tail.items[0].id) : detail.history_before, history_index: indexPage([], detail.items) };
}
export function windowPage(detail: SessionDetail, page: HistoryPage, direction: 'older' | 'newer'): SessionDetail {
  if (detail.representation !== 'compact-v1' || page.after === undefined) return { ...detail, items: mergeItems(detail.items, page.items, direction), history_before: page.before };
  const merged = mergeItems(detail.items, page.items, direction);
  const bounded = boundItems(merged, direction);
  const before = bounded.droppedBefore.length ? itemCursor(bounded.items[0].id) : direction === 'older' ? page.before : detail.history_before;
  const after = bounded.droppedAfter.length ? itemCursor(bounded.items.at(-1)!.id) : direction === 'newer' ? page.after ?? '' : detail.history_after ?? '';
  return { ...detail, items: bounded.items, history_before: before, history_after: after, history_index: indexPage(detail.history_index, page.items, direction) };
}
export function recentProjection(detail: SessionDetail): SessionDetail {
  const { recent_items, recent_before, history_index: _index, ...rest } = detail;
  return { ...rest, items: recent_items ?? detail.items, history_before: recent_before ?? detail.history_before, history_after: '' };
}
/** Reapply limits after live changes, preserving the reader's end when detached. */
export function liveWindow(detail: SessionDetail, items: Item[], tail: Item[], changed: Item[] = []): SessionDetail {
  if (detail.representation !== 'compact-v1') return { ...detail, items };
  const bounded = boundItems(items, detail.history_after ? 'older' : 'newer');
  const latest = boundItems(tail, 'newer', TAIL_ITEMS);
  return { ...detail, items: bounded.items,
    history_before: bounded.droppedBefore.length ? itemCursor(bounded.items[0].id) : detail.history_before,
    history_after: bounded.droppedAfter.length ? itemCursor(bounded.items.at(-1)!.id) : detail.history_after,
    recent_items: latest.items, recent_before: latest.droppedBefore.length ? itemCursor(latest.items[0].id) : detail.recent_before,
    history_index: changed.length ? indexPage(detail.history_index, changed) : detail.history_index,
  };
}

/** An outer work group can contain several separate runs of tool rows. */
export function groupIdentities(items: Item[], toolsOnly: boolean | 'turn' = false, special: (item: Item) => boolean = () => false, interruptions: string[] = [], previous?: Map<string, string>): Map<string, string> {
  const identities = new Map<string, string>();
  const used = new Set<string>();
  const known = new Set(items.map(item => item.id));
  let group: string[] = [];
  const flush = () => {
    if (!group.length) return;
    const established = group.map(id => previous?.get(id)).find(id => id && !used.has(id) && (!known.has(id) || group.includes(id)));
    const key = established ?? group[0];
    used.add(key);
    for (const id of group) identities.set(id, key);
    group = [];
  };
  const times = [...interruptions].sort();
  let interruption = 0;
  for (const item of items) {
    while (interruption < times.length && times[interruption] < item.time) { flush(); interruption++; }
    if (toolsOnly === 'turn') {
      if (item.kind === 'user' && !item.delivery) flush();
      else group.push(item.id);
      continue;
    }
    if (toolsOnly && item.kind === 'reasoning' && !item.text?.trim() && !item.compact?.has_reasoning) continue;
    const question = item.tool?.question_outcome;
    const work = toolsOnly ? item.kind === 'tool' && (!question || question === 'pending') : item.kind === 'tool' || item.kind === 'reasoning';
    if (work && !special(item)) group.push(item.id);
    else flush();
  }
  flush();
  return identities;
}
