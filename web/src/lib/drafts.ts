// A composer draft kept per Task in localStorage (`uam.draft.<id>`), so switching Tasks or
// reloading keeps what was typed. Pure text rules; the Composer owns the storage calls.

import type { Kind } from './attachments';

export const DRAFT_PREFIX = 'uam.draft.';

export const draftKey = (taskId: string): string => `${DRAFT_PREFIX}${taskId}`;

/** A finished upload: its stored id is enough to send it again after a reload. */
export interface DraftAttachment {
  id: string;
  name: string;
  size: number;
  kind: Kind;
}

export interface Draft {
  text: string;
  files: string[];
  attachments: DraftAttachment[];
}

const KINDS: readonly string[] = ['image', 'pdf', 'text'];

export const draftEmpty = (d: Draft): boolean => !d.text.trim() && d.files.length === 0 && d.attachments.length === 0;

/** The stored form, or null when there is nothing worth keeping (the key goes). */
export function serializeDraft(d: Draft): string | null {
  return draftEmpty(d) ? null : JSON.stringify(d);
}

/** A draft read back from storage; anything malformed reads as no draft. */
export function parseDraft(raw: string | null): Draft | null {
  if (!raw) return null;
  try {
    const v: unknown = JSON.parse(raw);
    if (!v || typeof v !== 'object') return null;
    const o = v as Record<string, unknown>;
    const text = typeof o.text === 'string' ? o.text : '';
    const files = Array.isArray(o.files) ? o.files.filter((f): f is string => typeof f === 'string' && !!f) : [];
    const attachments = Array.isArray(o.attachments)
      ? o.attachments.flatMap((a): DraftAttachment[] => {
          if (!a || typeof a !== 'object') return [];
          const { id, name, size, kind } = a as Record<string, unknown>;
          return typeof id === 'string' && id && typeof name === 'string' && KINDS.includes(String(kind)) ? [{ id, name, size: typeof size === 'number' ? size : 0, kind: kind as Kind }] : [];
        })
      : [];
    const d = { text, files, attachments };
    return draftEmpty(d) ? null : d;
  } catch {
    return null;
  }
}

/** Draft keys whose Task is gone, from every storage key and the ids that still exist. */
export function staleDraftKeys(keys: readonly string[], liveIds: Iterable<string>): string[] {
  const live = new Set(liveIds);
  return keys.filter((k) => k.startsWith(DRAFT_PREFIX) && !live.has(k.slice(DRAFT_PREFIX.length)));
}
