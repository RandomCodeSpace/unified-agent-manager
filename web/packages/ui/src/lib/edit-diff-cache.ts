import type { ItemDiffData } from '../api';
import { describeError } from '../api';

export const EDIT_DIFF_LIMIT = 8;
export const EDIT_DIFF_BYTES = 2 << 20;
export type EditDiffState = { status: 'unloaded' | 'loading' | 'error'; error?: string } | { status: 'loaded'; data: ItemDiffData };
const UNLOADED: EditDiffState = { status: 'unloaded' };
interface Entry { state: EditDiffState; bytes: number; controller?: AbortController }

/** Task-scoped, memory-only native patch cache. Rows hold keys, never patch copies. */
export class EditDiffCache {
  private entries = new Map<string, Entry>();
  private listeners = new Set<() => void>();
  private bytes = 0;
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  get = (key: string): EditDiffState => this.entries.get(key)?.state ?? UNLOADED;
  private notify() { this.listeners.forEach(listener => listener()); }
  private remove(key: string) { const entry = this.entries.get(key); if (entry) { entry.controller?.abort(); this.bytes -= entry.bytes; this.entries.delete(key); } }
  clear() { for (const key of this.entries.keys()) this.remove(key); this.notify(); }
  cancel(key: string) { if (this.entries.get(key)?.controller) { this.remove(key); this.notify(); } }
  load(key: string, read: (signal: AbortSignal) => Promise<ItemDiffData>, retry = false) {
    const prior = this.entries.get(key);
    if (prior && !retry) { this.entries.delete(key); this.entries.set(key, prior); return; }
    this.remove(key);
    const controller = new AbortController();
    const entry: Entry = { state: { status: 'loading' }, bytes: 256 + 2 * key.length, controller };
    this.bytes += entry.bytes;
    this.entries.set(key, entry);
    while (this.entries.size > EDIT_DIFF_LIMIT || this.bytes > EDIT_DIFF_BYTES) this.remove(this.entries.keys().next().value!);
    if (this.entries.get(key) !== entry) return;
    this.notify();
    const timer = window.setTimeout(() => controller.abort(new Error('Loading the diff timed out. Try again.')), 10000);
    void read(controller.signal).then(data => {
      if (this.entries.get(key) !== entry || controller.signal.aborted) return;
      const bytes = 256 + 2 * key.length + Object.values(data).reduce((sum, value) => sum + (typeof value === 'string' ? 2 * value.length : 8), 0);
      if (bytes > EDIT_DIFF_BYTES) throw new Error('This recorded diff exceeds the display limit.');
      this.bytes += bytes - entry.bytes;
      entry.state = { status: 'loaded', data }; entry.bytes = bytes; entry.controller = undefined;
      while (this.bytes > EDIT_DIFF_BYTES) this.remove(this.entries.keys().next().value!);
      this.notify();
    }).catch(error => {
      if (this.entries.get(key) !== entry) return;
      // Copy a bounded prefix so a substring cannot retain an oversized response.
      const message = Array.from(describeError(controller.signal.reason ?? error).slice(0, 512)).join('');
      const bytes = 256 + 2 * (key.length + message.length);
      this.bytes += bytes - entry.bytes;
      entry.state = { status: 'error', error: message }; entry.bytes = bytes; entry.controller = undefined;
      while (this.bytes > EDIT_DIFF_BYTES) this.remove(this.entries.keys().next().value!);
      this.notify();
    }).finally(() => window.clearTimeout(timer));
  }
}
