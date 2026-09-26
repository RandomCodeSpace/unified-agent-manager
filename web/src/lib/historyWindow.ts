import type { Item } from '../api';

export const ACTIVE_ITEMS = 150;
export const ACTIVE_BYTES = 4 * 1024 * 1024;
export const TAIL_ITEMS = 50;

/** Account retained strings and records without copying or serializing their text. */
export function accountItem(item: Item): number {
  return account(item);
}

function account(value: unknown): number {
  if (typeof value === 'string') return 24 + value.length * 2;
  if (typeof value === 'number') return 8;
  if (typeof value === 'boolean') return 4;
  if (value == null) return 4;
  if (Array.isArray(value)) return value.reduce((bytes, entry) => bytes + 8 + account(entry), 32);
  if (typeof value === 'object') {
    return Object.entries(value).reduce((bytes, [key, entry]) => bytes + 32 + key.length * 2 + account(entry), 64);
  }
  return 0;
}

const encoder = new TextEncoder();

/** The server's opaque item boundary is UTF-8 encoded base64url. */
export function itemCursor(id: string): string {
  let binary = '';
  for (const byte of encoder.encode(id)) binary += String.fromCharCode(byte);
  return btoa(binary).replaceAll('+', '-').replaceAll('/', '_').replace(/=+$/, '');
}

/** Keep a contiguous window at the requested edge, including one oversized item intact. */
export function boundItems(items: Item[], direction: 'older' | 'newer', maxItems = ACTIVE_ITEMS, maxBytes = ACTIVE_BYTES): {
  items: Item[]; droppedBefore: Item[]; droppedAfter: Item[];
} {
  let count = 0;
  let bytes = 0;
  while (count < items.length && count < Math.max(1, maxItems)) {
    const index = direction === 'older' ? count : items.length - count - 1;
    const next = accountItem(items[index]);
    if (count > 0 && bytes + next > maxBytes) break;
    bytes += next;
    count++;
    if (bytes >= maxBytes) break;
  }
  const start = direction === 'older' ? 0 : items.length - count;
  const end = start + count;
  return { items: items.slice(start, end), droppedBefore: items.slice(0, start), droppedAfter: items.slice(end) };
}

/** Only a confirmed idle echo moves a provisional steer into a new turn. */
export const idleSteerEcho = (previous: Item, next: Item) => previous.kind === 'user' && previous.delivery === 'steer' && !!previous.steer_status && next.kind === 'user' && !next.delivery && !next.steer_status;

/** Place only confirmed receipts by the surrounding IDs in the provider page. */
export function placeIdleSteerEchoes(items: Item[], page: Item[], moved: Set<string>): Item[] {
  if (!moved.size) return items;
  const result = items.slice();
  // Reverse order lets an earlier echo use a later echo as its next anchor.
  for (let at = page.length - 1; at >= 0; at--) {
    const echo = page[at];
    if (!moved.has(echo.id)) continue;
    const old = result.findIndex(item => item.id === echo.id);
    if (old >= 0) result.splice(old, 1);
    let position = -1;
    for (let next = at + 1; next < page.length; next++) {
      position = result.findIndex(item => item.id === page[next].id);
      if (position >= 0) break;
    }
    if (position < 0) {
      for (let previous = at - 1; previous >= 0; previous--) {
        position = result.findIndex(item => item.id === page[previous].id);
        if (position >= 0) { position++; break; }
      }
    }
    result.splice(position < 0 ? result.length : position, 0, echo);
  }
  return result;
}

/** Page overlaps keep their timeline position and the existing live value. */
export function mergeItems(existing: Item[], incoming: Item[], direction: 'older' | 'newer'): Item[] {
  const live = new Map(existing.map(item => [item.id, item]));
  const moved = new Set(incoming.filter(item => {
    const previous = live.get(item.id);
    return previous && idleSteerEcho(previous, item);
  }).map(item => item.id));
  const seen = new Set<string>();
  const merged: Item[] = [];
  const pages: Array<[Item[], boolean]> = direction === 'older' ? [[incoming, false], [existing, true]] : [[existing, true], [incoming, false]];
  for (const [page, fromLive] of pages) {
    for (const item of page) {
      if (fromLive && moved.has(item.id)) continue;
      if (seen.has(item.id)) continue;
      seen.add(item.id);
      merged.push(moved.has(item.id) ? item : live.get(item.id) ?? item);
    }
  }
  return placeIdleSteerEchoes(merged, incoming, moved);
}
