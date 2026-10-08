// The agents' todo list (DESIGN.md Status line and Todo reader): what the status line says of
// it, the reader's sections, and what the live region announces. The service counts the rows
// and picks the one the work is at; this only words them.

import type { Todo, TodoCounts, TodoStatus, TodoView, TurnTodos } from '../api';

/** The meter draws one segment per row, up to this many rows; past them the words say it alone. */
export const METER_ROWS = 12;

/** The list on the status line, or undefined when the line says nothing of it. */
export type TodoLine =
  /** This turn touched the list: "Todo 2/7", the row the work is at and how many more are in progress, the blocked rows. */
  | { kind: 'list'; done: number; total: number; blocked: number; now?: string; more: number; meter?: TodoStatus[]; cue?: TodoCue }
  /** The running turn has not touched it yet, and the last turn left rows open (blocked ones too): "Todo · 3 open from the last turn". */
  | { kind: 'carried'; open: number };

/** One announcement: spoken once per key. */
export interface TodoCue { key: string; words: string }

/**
 * What the status line says of `view`: nothing while it is unknown or empty, or untouched with
 * nothing open; `since` is when the turn started, so only its changes are announced.
 */
export function todoLine(view: TodoView | undefined, since?: string): TodoLine | undefined {
  if (!view?.known || !view.counts.total) return undefined;
  const { done = 0, total = 0, blocked = 0, open = 0 } = view.counts;
  if (!view.touched) return open + blocked > 0 ? { kind: 'carried', open: open + blocked } : undefined;
  const running = view.todos.filter((t) => t.status === 'in_progress').length;
  const now = view.todos.find((t) => t.id === view.now)?.title;
  return {
    kind: 'list',
    done,
    total,
    blocked,
    now,
    more: now ? Math.max(0, running - 1) : 0,
    meter: total <= METER_ROWS && view.todos.length === total ? view.todos.map((t) => t.status) : undefined,
    cue: todoCue(view, since),
  };
}

/**
 * The latest change this turn made that is news: a row turning blocked ("Blocked: …") or done
 * ("3 of 7 done"). Rows changed together share a time; a blocked one wins.
 */
export function todoCue(view: TodoView, since?: string): TodoCue | undefined {
  const from = since ? Date.parse(since) : -Infinity;
  let last: Todo | undefined;
  let at = -Infinity;
  for (const t of view.todos) {
    if ((t.status !== 'done' && t.status !== 'blocked') || !t.changed_at) continue;
    const when = Date.parse(t.changed_at);
    if (!(when >= from)) continue;
    if (when > at || (when === at && t.status === 'blocked' && last?.status !== 'blocked')) [last, at] = [t, when];
  }
  if (!last) return undefined;
  if (last.status === 'blocked') return { key: `blocked ${last.id}`, words: `Blocked: ${last.title}` };
  const { done = 0, total = 0 } = view.counts;
  return { key: `done ${done}`, words: `${done} of ${total} done` };
}

/** The status line's todo words in the button's sentence, after the clock. */
export function todoSentence(line: TodoLine): string {
  if (line.kind === 'carried') return `. Todo: ${line.open} open from the last turn`;
  const now = line.now ? `. Now: ${line.now}${line.more ? `, and ${line.more} more in progress` : ''}` : '';
  return `. Todo ${line.done} of ${line.total} done${line.blocked ? `, ${line.blocked} blocked` : ''}${now}`;
}

export interface TodoSection { label: 'Now' | 'Blocked' | 'Next' | 'Done' | 'Left open'; todos: Todo[] }

/** The reader's sections, empty ones left out: in progress (the Now row first), blocked, pending, done; each in the provider's order. */
export function todoSections(view: TodoView): TodoSection[] {
  const of = (status: TodoStatus) => view.todos.filter((t) => t.status === status);
  const now = of('in_progress');
  const first = now.findIndex((t) => t.id === view.now);
  if (first > 0) now.unshift(...now.splice(first, 1));
  const sections: TodoSection[] = [
    { label: 'Now', todos: now },
    { label: 'Blocked', todos: of('blocked') },
    { label: 'Next', todos: of('pending') },
    { label: 'Done', todos: of('done') },
  ];
  return sections.filter((s) => s.todos.length > 0);
}

const WORD: Record<TodoStatus, string> = { in_progress: 'Now', blocked: 'Blocked', pending: 'Next', done: 'Done' };

/** A row's state as words, for those who cannot see its glyph. */
export const todoWord = (status: TodoStatus) => WORD[status];

/** A reply's foot button's name, from the counts uam kept when its turn ended (on screen "Todo 5/7 · 1 blocked · 1 left open"). */
export function turnTodoName(counts: TodoCounts): string {
  const { done = 0, total = 0, blocked = 0, open = 0 } = counts;
  return `Todo at the end of this turn: ${done} of ${total} done${blocked ? `, ${blocked} blocked` : ''}${open ? `, ${open} left open` : ''}`;
}

/** The kept list's facts: when the turn left it (`clock`), "all done" when nothing was left, and the rows past those kept. */
export function snapshotFacts(counts: TodoCounts, clock: string): string[] {
  const { blocked = 0, open = 0, omitted = 0 } = counts;
  return [`As this turn left it, ${clock}`, blocked + open ? '' : 'all done', omitted ? `${omitted} more not shown` : ''].filter(Boolean);
}

/** The kept list's sections, empty ones left out: blocked, left open (in progress, then pending) and done (newest first), in the order uam kept them. */
export function snapshotSections(todos: readonly Todo[]): TodoSection[] {
  const sections: TodoSection[] = [
    { label: 'Blocked', todos: todos.filter((t) => t.status === 'blocked') },
    { label: 'Left open', todos: todos.filter((t) => t.status === 'in_progress' || t.status === 'pending') },
    { label: 'Done', todos: todos.filter((t) => t.status === 'done') },
  ];
  return sections.filter((s) => s.todos.length > 0);
}

/** How many turn lists stay in memory: one never changes once its turn ended, and none is kept anywhere else. */
export const KEPT_SNAPSHOTS = 8;
const snapshots = new Map<string, TurnTodos>();

/** A turn's list read before, now the most recently used. */
export function recallSnapshot(task: string, timing: string): TurnTodos | undefined {
  const key = `${task}\n${timing}`;
  const snap = snapshots.get(key);
  if (snap) {
    snapshots.delete(key);
    snapshots.set(key, snap);
  }
  return snap;
}

/** Keeps a turn's list read, dropping the least recently used past `KEPT_SNAPSHOTS`. */
export function keepSnapshot(task: string, timing: string, snap: TurnTodos): void {
  const key = `${task}\n${timing}`;
  snapshots.delete(key);
  snapshots.set(key, snap);
  for (const old of snapshots.keys()) {
    if (snapshots.size <= KEPT_SNAPSHOTS) break;
    snapshots.delete(old);
  }
}
