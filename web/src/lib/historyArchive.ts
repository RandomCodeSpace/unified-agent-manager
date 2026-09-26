import type { HistoryPage } from '../api';

/**
 * The entry-chunk side of the archive page cache. Older history the service reads back from
 * Copilot's record comes in pages marked `archive: true`, requested with archive cursors: `a.`
 * then the item ID (docs/adr/0004-web-interface.md, Record paging). They never change, so this
 * browser keeps them in IndexedDB (historyCache.ts). That module and its database load only when a
 * Task pages with an archive cursor; any other request goes straight to the network. A miss or any
 * storage error reads from the network, as without the cache.
 *
 * The marks (localStorage) list every Task that may hold pages: a Task is marked before its first
 * write, the sweep drops pages of unmarked Tasks, and removing a Task needs the database only when
 * marked, so no page load opens it to clean up.
 */

export const ARCHIVE_DB = 'uam-history';
const MARKS = 'uam.history-archive';
/** Service archive cursors start so; retained cursors (base64url) never hold a `.`. */
const ARCHIVE_CURSOR = 'a.';

type Cache = typeof import('./historyCache.ts');
/** Marked Tasks by ID; the values carry nothing (an earlier format kept cursors there). */
type Marks = Record<string, unknown>;

let loading: Promise<Cache> | null = null;
let cache: Cache | null = null;
let clears = 0;

function load(): Promise<Cache> {
  return (loading ??= import('./historyCache.ts').then(module => (cache = module), error => { loading = null; throw error; }));
}

/** Work started before a clear must not write afterwards. */
export const clearCount = () => clears;

/** The marks, or null when storage is unavailable (then nothing is written, and nothing is swept for being unmarked). */
export function readMarks(): Marks | null {
  try { return JSON.parse(localStorage.getItem(MARKS) ?? '{}') as Marks; } catch { return null; }
}

function saveMarks(marks: Marks): boolean {
  try {
    if (Object.keys(marks).length) localStorage.setItem(MARKS, JSON.stringify(marks));
    else localStorage.removeItem(MARKS);
    return true;
  } catch { return false; }
}

/** Tasks the sweep dropped whole lose their marks. */
export function unmarkTasks(tasks: Iterable<string>) {
  const marks = readMarks();
  if (!marks) return;
  let changed = false;
  for (const task of tasks) if (task in marks) { delete marks[task]; changed = true; }
  if (changed) saveMarks(marks);
}

function mark(task: string): boolean {
  const marks = readMarks();
  if (!marks) return false;
  if (task in marks) return true;
  marks[task] = 1;
  return saveMarks(marks);
}

/**
 * One older page of a Task (`agent` '' is the main transcript), through the cache when `before`
 * is an archive cursor. A cached page gets the current `epoch` and seq -1: archive items never
 * change and no live frame names them, so the page claims no frames. The reducers then replay
 * every frame buffered during the read and keep any held item a frame touched
 * (`historyItemSeq`/`itemSeq` > -1), so it never discards live data. A newer-direction read and a
 * read before a retained cursor pass through.
 */
export async function historyPage(task: string, agent: string, before: string, direction: 'older' | 'newer', epoch: string | undefined, fetch: () => Promise<HistoryPage>): Promise<HistoryPage> {
  if (direction !== 'older' || !before.startsWith(ARCHIVE_CURSOR)) return fetch();
  // A read slower than this (a busy database, a slow chunk) yields to the network.
  const hit = await Promise.race([load().then(c => c.read(task, agent, before, epoch)), new Promise<undefined>(resolve => setTimeout(resolve, 1000))]).catch(() => undefined);
  if (hit) return hit;
  let page: HistoryPage;
  try {
    page = await fetch();
  } catch (error) {
    // The record changed under an archive cursor: this Task's cached pages may be stale.
    if ((error as { status?: unknown }).status === 409) forgetArchive(task);
    throw error;
  }
  if (page.archive && page.representation === 'compact-v1' && mark(task)) {
    const at = clears;
    void load().then(c => { if (at === clears) c.write(task, agent, before, page); }).catch(() => {});
  }
  return page;
}

/** A Task deleted, removed or not found: its pages go. */
export function forgetArchive(task: string) {
  const marks = readMarks();
  const marked = !!marks && task in marks;
  if (marked) unmarkTasks([task]);
  if (marked || cache) void load().then(c => c.forget(task)).catch(() => {});
}

/** Tasks the service no longer lists (a snapshot) lose their pages, as their drafts do. */
export function retainArchive(tasks: readonly string[]) {
  const listed = new Set(tasks);
  for (const task of Object.keys(readMarks() ?? {})) if (!listed.has(task)) forgetArchive(task);
}

/** Sign-out and lost authentication: the whole database goes, whether or not this page loaded it. */
export function clearArchive() {
  clears++;
  cache?.close();
  try { localStorage.removeItem(MARKS); } catch { /* unavailable: nothing was written */ }
  try { indexedDB.deleteDatabase(ARCHIVE_DB); } catch { /* unavailable: nothing was stored */ }
}
