// The assist features' pure parts (docs/web.md): inserting text at the caret, when a Task's last
// turn may get suggested replies, and saving a downloaded file. Pure, so the node
// tests run them.

import type { Item, SessionSummary } from '../api';

/** `text` with `insert` at `caret`, on its own line when the text around it is not empty; and the caret after it. */
export function insertAt(text: string, caret: number, insert: string): { text: string; caret: number } {
  const at = Math.max(0, Math.min(caret, text.length));
  const before = text.slice(0, at);
  const after = text.slice(at);
  const lead = before && !before.endsWith('\n') ? '\n' : '';
  const tail = after && !after.startsWith('\n') ? '\n' : '';
  const next = before + lead + insert + tail + after;
  return { text: next, caret: (before + lead + insert).length };
}

/**
 * The ID of the item the Task's main transcript ends with when replies may be suggested for it:
 * the Task is active, its last turn completed, nothing waits or is queued, and the transcript
 * ends with the agent's answer (thoughts aside). Otherwise empty: while it works or needs an
 * answer, the question UI and the working state speak instead.
 */
export function suggestionKey(s: Pick<SessionSummary, 'stage' | 'state' | 'queued' | 'pending'>, items: readonly Item[]): string {
  if ((s.stage ?? 'active') !== 'active' || s.state !== 'completed' || (s.queued ?? 0) > 0 || Number(s.pending) > 0) return '';
  for (let i = items.length - 1; i >= 0; i--) {
    const it = items[i];
    // A turn can record an empty thought after its answer.
    if (it.agent_id || it.kind === 'reasoning') continue;
    return it.kind === 'assistant' ? it.id : '';
  }
  return '';
}

/** Saves `blob` as a file named `name` through a temporary link. */
export function saveBlob(blob: Blob, name: string): void {
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = name;
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 60_000);
}
