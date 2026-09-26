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

/** Page overlaps keep their timeline position and the existing live value. */
export function mergeItems(existing: Item[], incoming: Item[], direction: 'older' | 'newer'): Item[] {
  const live = new Map(existing.map(item => [item.id, item]));
  const seen = new Set<string>();
  const merged: Item[] = [];
  const pages = direction === 'older' ? [incoming, existing] : [existing, incoming];
  for (const page of pages) {
    for (const item of page) {
      if (seen.has(item.id)) continue;
      seen.add(item.id);
      merged.push(live.get(item.id) ?? item);
    }
  }
  return merged;
}
