import type { HistoryPage, Item } from '../api';
import { ARCHIVE_DB, clearCount, readMarks, unmarkTasks } from './historyArchive.ts';
import { accountItem } from './historyWindow.ts';

/**
 * IndexedDB store of archive history pages. Only historyArchive.ts loads this module, on first use.
 *
 * `pages` holds a page's items and cursors, `meta` its size and last use, so eviction reads only
 * the small records. Both are keyed [task, agent, representation, cursor] (agent '' is the main
 * transcript). Archive cursors are content-derived and survive service restarts, so no epoch is
 * part of the key. Array keys sort by task first, so one key range covers a Task.
 *
 * Eviction (`evictions`) runs in idle time: once after first use (the sweep), and again after
 * writes so the budget holds within a page load.
 * - A Task unused for 14 days goes whole, as does one without a mark (see historyArchive.ts).
 * - A Task over its cap, 16 MiB or half the budget, loses its least recently used pages.
 * - Over the budget, min(64 MiB, 10% of the origin's quota) or 32 MiB without an estimate, whole
 *   Tasks go, least recently used first, until the rest fits in 90% of it.
 * - A write refused for quota evicts down to half of what is stored and retries once.
 * Sizes are the transcript window's own accounting (accountItem: string lengths plus fixed
 * charges, nothing serialized), taken at write time. Reads batch their access times into the
 * next idle pass.
 */

/** Bump with any change to the records or to the representation the client requests; the upgrade starts empty. */
export const SCHEMA = 1;
export const REPRESENTATION = 'compact-v1';
const MIB = 1024 * 1024;
export const TTL = 14 * 24 * 60 * 60 * 1000;
export const TASK_BYTES = 16 * MIB;
const PAGES = 'pages';
const META = 'meta';

export type Key = [task: string, agent: string, representation: string, cursor: string];
export interface Entry { key: Key; bytes: number; used: number }
interface Stored { items: Item[]; before: string; after?: string }

export const cacheKey = (task: string, agent: string, cursor: string): Key => [task, agent, REPRESENTATION, cursor];
export const budgetFor = (quota?: number) => (quota ? Math.min(64 * MIB, quota / 10) : 32 * MIB);
const size = (stored: Stored) => stored.items.reduce((bytes, item) => bytes + accountItem(item), 64 + (stored.before.length + (stored.after?.length ?? 0)) * 2);

/** A stored page as the reducers take it; see historyPage for the seq. */
export function stamp(stored: Stored, epoch: string | undefined): HistoryPage {
  return { items: stored.items, before: stored.before, after: stored.after, seq: -1, epoch, representation: REPRESENTATION, archive: true };
}

/** The keys the policy above deletes. `marked` is null when the marks are unreadable; then no Task goes for lacking one. */
export function evictions(entries: readonly Entry[], budget: number, now: number, marked: ReadonlySet<string> | null = null): Key[] {
  const tasks = new Map<string, { used: number; bytes: number; entries: Entry[] }>();
  for (const entry of entries) {
    const task = tasks.get(entry.key[0]);
    if (!task) tasks.set(entry.key[0], { used: entry.used, bytes: entry.bytes, entries: [entry] });
    else {
      task.used = Math.max(task.used, entry.used);
      task.bytes += entry.bytes;
      task.entries.push(entry);
    }
  }
  const victims: Key[] = [];
  const cap = Math.min(TASK_BYTES, budget / 2);
  let total = 0;
  for (const [id, task] of tasks) {
    if (now - task.used > TTL || (marked && !marked.has(id))) {
      victims.push(...task.entries.map(entry => entry.key));
      tasks.delete(id);
      continue;
    }
    task.entries.sort((a, b) => a.used - b.used);
    while (task.bytes > cap && task.entries.length) {
      const oldest = task.entries.shift()!;
      task.bytes -= oldest.bytes;
      victims.push(oldest.key);
    }
    total += task.bytes;
  }
  if (total <= budget) return victims;
  for (const task of [...tasks.values()].sort((a, b) => a.used - b.used)) {
    if (total <= budget * 0.9) break;
    victims.push(...task.entries.map(entry => entry.key));
    total -= task.bytes;
  }
  return victims;
}

let db: Promise<IDBDatabase> | null = null;
let quota: Promise<number> | null = null;
const touched = new Map<string, Key>();
let scheduled = false;
// The first pass after loading is the sweep.
let evict = true;

function open(): Promise<IDBDatabase> {
  db ??= new Promise<IDBDatabase>((resolve, reject) => {
    let failed = false;
    const request = indexedDB.open(ARCHIVE_DB, SCHEMA);
    request.onupgradeneeded = () => {
      const upgraded = request.result;
      for (const name of [...upgraded.objectStoreNames]) upgraded.deleteObjectStore(name);
      upgraded.createObjectStore(PAGES);
      upgraded.createObjectStore(META, { keyPath: 'key' });
    };
    request.onsuccess = () => {
      const opened = request.result;
      if (failed) return opened.close();
      // A clear or a newer schema elsewhere proceeds; the next use opens again.
      opened.onversionchange = () => { opened.close(); db = null; };
      opened.onclose = () => { db = null; };
      resolve(opened);
    };
    request.onerror = () => reject(request.error ?? new Error('The history cache could not open.'));
    // An older tab holds the database open: read from the network rather than wait.
    request.onblocked = () => { failed = true; db = null; reject(new Error('The history cache is busy.')); };
  });
  return db;
}

const settled = (tx: IDBTransaction) => new Promise<void>((resolve, reject) => {
  tx.oncomplete = () => resolve();
  tx.onabort = () => reject(tx.error ?? new DOMException('The history cache write was aborted.', 'AbortError'));
});
const result = <T>(request: IDBRequest<T>) => new Promise<T>((resolve, reject) => {
  request.onsuccess = () => resolve(request.result);
  request.onerror = () => reject(request.error ?? new Error('The history cache request failed.'));
});

function idle(run: () => void) {
  if (typeof requestIdleCallback === 'function') requestIdleCallback(run, { timeout: 5000 });
  else setTimeout(run, 200);
}

function schedule(wrote = false) {
  evict ||= wrote;
  if (scheduled) return;
  scheduled = true;
  idle(() => {
    scheduled = false;
    void maintain().catch(() => {});
  });
}

/** Records batched access times; evicts on the sweep, after writes, and with `shrink` (a quota refusal) to that share of what is stored. */
async function maintain(shrink = 0) {
  const at = clearCount();
  const opening = open();
  quota ??= (navigator.storage?.estimate?.() ?? Promise.resolve<StorageEstimate>({})).then(e => budgetFor(e.quota), () => budgetFor());
  const [database, budget] = await Promise.all([opening, quota]);
  if (at !== clearCount()) return;
  const time = Date.now();
  const touches = new Map(touched);
  touched.clear();
  const sweep = evict || shrink > 0;
  evict = false;
  if (!sweep && !touches.size) return;
  const tx = database.transaction([PAGES, META], 'readwrite');
  const pages = tx.objectStore(PAGES), meta = tx.objectStore(META);
  if (!sweep) {
    for (const key of touches.values()) {
      const request = meta.get(key) as IDBRequest<Entry | undefined>;
      request.onsuccess = () => { if (request.result) meta.put({ ...request.result, used: time }); };
    }
    return settled(tx);
  }
  const entries = await result(meta.getAll() as IDBRequest<Entry[]>);
  for (const entry of entries) {
    if (!touches.has(JSON.stringify(entry.key))) continue;
    entry.used = time;
    meta.put(entry);
  }
  const stored = entries.reduce((bytes, entry) => bytes + entry.bytes, 0);
  const marks = readMarks();
  const victims = evictions(entries, shrink ? Math.min(budget, stored * shrink) : budget, time, marks && new Set(Object.keys(marks)));
  for (const key of victims) {
    pages.delete(key);
    meta.delete(key);
  }
  await settled(tx);
  // Tasks left without pages lose their marks.
  const gone = new Set(victims.map(key => JSON.stringify(key)));
  const kept = new Set(entries.filter(entry => !gone.has(JSON.stringify(entry.key))).map(entry => entry.key[0]));
  unmarkTasks(new Set(victims.map(key => key[0]).filter(task => !kept.has(task))));
}

async function put(key: Key, stored: Stored) {
  const tx = (await open()).transaction([PAGES, META], 'readwrite');
  tx.objectStore(PAGES).put(stored, key);
  tx.objectStore(META).put({ key, bytes: size(stored), used: Date.now() } satisfies Entry);
  await settled(tx);
}

async function store(key: Key, stored: Stored, at: number) {
  try {
    await put(key, stored);
  } catch (error) {
    if (!(error instanceof DOMException && error.name === 'QuotaExceededError')) return;
    await maintain(0.5);
    if (at !== clearCount()) return;
    await put(key, stored);
  }
  schedule(true);
}

export async function read(task: string, agent: string, cursor: string, epoch: string | undefined): Promise<HistoryPage | undefined> {
  const at = clearCount();
  const key = cacheKey(task, agent, cursor);
  const stored = await open().then(database => result(database.transaction(PAGES).objectStore(PAGES).get(key) as IDBRequest<Stored | undefined>));
  if (!stored || !Array.isArray(stored.items) || typeof stored.before !== 'string' || at !== clearCount()) return undefined;
  touched.set(JSON.stringify(key), key);
  schedule();
  return stamp(stored, epoch);
}

/** Stores a page once the browser is idle: the page renders first, and storing clones it on the main thread. */
export function write(task: string, agent: string, cursor: string, page: HistoryPage) {
  const at = clearCount();
  const stored: Stored = { items: page.items, before: page.before, after: page.after };
  idle(() => { if (at === clearCount()) void store(cacheKey(task, agent, cursor), stored, at).catch(() => {}); });
}

export async function forget(task: string) {
  const tx = (await open()).transaction([PAGES, META], 'readwrite');
  const range = IDBKeyRange.bound([task], [task, []]);
  tx.objectStore(PAGES).delete(range);
  tx.objectStore(META).delete(range);
  await settled(tx);
}

/** A clear: pending work is dropped and the connection closes so the database can be deleted. */
export function close() {
  touched.clear();
  void db?.then(database => database.close(), () => {});
  db = null;
}
