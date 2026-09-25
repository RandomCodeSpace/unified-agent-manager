/**
 * Terminal-style prompt history for the composer: Up recalls earlier prompts, Down comes back,
 * Escape restores the draft. Pure; the Composer keeps one `Browsing | null` and calls `historyKey`.
 */

export interface HistoryItem {
  kind: string;
  text?: string;
}

export interface HistoryQueued {
  text: string;
}

/** Where the user is in the list. `shown` is the text history put in the textarea; if the textarea says something else, the user edited it and browsing is over. */
export interface Browsing {
  index: number;
  draft: string;
  shown: string;
}

export interface HistoryStep {
  browsing: Browsing | null;
  text: string;
}

/** The Task's prompts newest first: queued ones, then the transcript's user messages. Blank entries are dropped and consecutive repeats collapse. */
export function historyEntries(items: readonly HistoryItem[] | undefined, queue: readonly HistoryQueued[] | undefined): string[] {
  const texts = [...(items ?? []).filter((i) => i.kind === 'user').map((i) => i.text ?? ''), ...(queue ?? []).map((q) => q.text)];
  const entries: string[] = [];
  for (let i = texts.length - 1; i >= 0; i--) {
    const t = texts[i];
    if (!t.trim() || entries[entries.length - 1] === t) continue;
    entries.push(t);
  }
  return entries;
}

/** The newest user message with text (the one a failed, stopped or interrupted turn ran on), for Resend; null when there is none. */
export function lastPrompt<T extends HistoryItem>(items: readonly T[] | undefined): T | null {
  for (let i = (items?.length ?? 0) - 1; i >= 0; i--) {
    const it = items![i];
    if (it.kind === 'user' && it.text?.trim()) return it;
  }
  return null;
}

/** The caret sits before the first line break, so a native Up would leave the text. */
export function onFirstLine(text: string, caret: number): boolean {
  return !text.slice(0, caret).includes('\n');
}

/** The caret sits after the last line break, so a native Down would leave the text. */
export function onLastLine(text: string, caret: number): boolean {
  return !text.slice(caret).includes('\n');
}

/**
 * What an unmodified ArrowUp, ArrowDown or Escape does to the textarea; null means the key is not
 * for history and the browser keeps it. Recalled text goes in whole, so the caller puts the caret at its end.
 */
export function historyKey(browsing: Browsing | null, entries: readonly string[], text: string, caret: number, key: string): HistoryStep | null {
  const active = browsing && browsing.shown === text ? browsing : null;
  if (key === 'ArrowUp') {
    if (text !== '' && !onFirstLine(text, caret)) return null;
    const index = active ? active.index + 1 : 0;
    if (index >= entries.length) return active ? { browsing: active, text } : null;
    const shown = entries[index];
    return { browsing: { index, draft: active ? active.draft : text, shown }, text: shown };
  }
  if (!active) return null;
  if (key === 'Escape') return { browsing: null, text: active.draft };
  if (key === 'ArrowDown') {
    if (!onLastLine(text, caret)) return null;
    const index = active.index - 1;
    if (index < 0) return { browsing: null, text: active.draft };
    const shown = entries[index];
    return { browsing: { ...active, index, shown }, text: shown };
  }
  return null;
}
