// The agents' todo list (DESIGN.md Status line and Todo reader): what the status line says of
// it, the reader's sections, and what the live region announces. The service counts the rows
// and picks the one the work is at; this only words them.

import type { Todo, TodoCounts, TodoHistory, TodoStatus, TodoView, TurnTiming, TurnTodos } from '../api';

/** The meter draws one segment per row, up to this many rows; past them the words say it alone. */
export const METER_ROWS = 12;

/** The stages in the order every surface gives them: the readers' sections, and the counts on the line and a reply's foot. */
const STAGES: readonly TodoStatus[] = ['in_progress', 'blocked', 'pending', 'done'];

const WORD: Record<TodoStatus, string> = { in_progress: 'In progress', blocked: 'Blocked', pending: 'To do', done: 'Done' };

/** A stage's name: its reader section, and a row's state as words for those who cannot see its glyph. */
export const todoWord = (status: TodoStatus) => WORD[status];

/** One open stage and its rows. */
export interface StageCount { status: TodoStatus; n: number }

/**
 * How far a list got: done of total, then the open stages with rows (in progress, blocked, to do)
 * and `open`, the rows counts kept before uam split in progress from to do say only were one or
 * the other.
 */
export interface TodoTally { done: number; total: number; stages: StageCount[]; open: number }

export function todoTally(counts: TodoCounts): TodoTally {
  const { done = 0, total = 0, in_progress = 0, blocked = 0, pending = 0, open = 0 } = counts;
  const rows: Partial<Record<TodoStatus, number>> = { in_progress, blocked, pending };
  const stages = STAGES.flatMap((status) => (rows[status] ? [{ status, n: rows[status] }] : []));
  return { done, total, stages, open: Math.max(0, open - in_progress - pending) };
}

/** "2 of 7 done, 1 in progress, 1 blocked, 3 to do"; an unsplit count reads as `openWords` ("left open"). */
export function tallyWords(tally: TodoTally, openWords = 'open'): string {
  const rest = [...tally.stages.map((s) => `${s.n} ${WORD[s.status].toLowerCase()}`), tally.open ? `${tally.open} ${openWords}` : ''].filter(Boolean);
  return [`${tally.done} of ${tally.total} done`, ...rest].join(', ');
}

/** The list on the status line, or undefined when the line says nothing of it. */
export type TodoLine =
  /** This turn touched the list: "Todo 2/7", the row the work is at, then each open stage's count. */
  | ({ kind: 'list'; now?: string; meter?: TodoStatus[]; cue?: TodoCue } & TodoTally)
  /** The running turn has not touched it yet, and the last turn left rows open (blocked ones too): the same counts, from the last turn. */
  | ({ kind: 'carried'; meter?: TodoStatus[] } & TodoTally);

/** One announcement: spoken once per key. */
export interface TodoCue { key: string; words: string }

/**
 * What the status line says of `view`: nothing while it is unknown or empty, or untouched with
 * nothing open; `since` is when the turn started, so only its changes are announced.
 */
export function todoLine(view: TodoView | undefined, since?: string): TodoLine | undefined {
  if (!view?.known || !view.counts.total) return undefined;
  const tally = todoTally(view.counts);
  const meter = tally.total <= METER_ROWS && view.todos.length === tally.total ? view.todos.map((t) => t.status) : undefined;
  if (!view.touched) return tally.stages.length > 0 || tally.open > 0 ? { kind: 'carried', ...tally, meter } : undefined;
  const now = view.todos.find((t) => t.id === view.now)?.title;
  return { kind: 'list', ...tally, now, meter, cue: todoCue(view, since) };
}

/**
 * The latest change this turn made that is news: a row turning blocked ("Blocked: …") or done
 * ("3 of 7 done, 1 in progress, 3 to do"). Rows changed together share a time; a blocked one wins.
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
  return { key: `done ${view.counts.done ?? 0}`, words: tallyWords(todoTally(view.counts)) };
}

/** The status line's todo words in the button's sentence, after the clock. */
export function todoSentence(line: TodoLine): string {
  if (line.kind === 'carried') return `. Todo from the last turn: ${tallyWords(line)}`;
  return `. Todo ${tallyWords(line)}${line.now ? `. Now: ${line.now}` : ''}`;
}

export interface TodoSection { status: TodoStatus; label: string; todos: Todo[] }

/** A reader's sections in the stages' order, empty ones left out, each in the order given; `now`, the row the work is at, first among those in progress. */
function stageSections(todos: readonly Todo[], now?: string): TodoSection[] {
  return STAGES.map((status) => {
    const rows = todos.filter((t) => t.status === status);
    const first = rows.findIndex((t) => t.id === now);
    if (first > 0) rows.unshift(...rows.splice(first, 1));
    return { status, label: WORD[status], todos: rows };
  }).filter((s) => s.todos.length > 0);
}

/** The live reader's sections: In progress (the Now row first), Blocked, To do and Done, each in the provider's order. */
export const todoSections = (view: TodoView) => stageSections(view.todos, view.now);

/** A reply's foot button's name, from the counts uam kept when its turn ended (on screen "Todo 5/7 · 1 in progress · 1 blocked"). */
export function turnTodoName(counts: TodoCounts): string {
  return `Todo at the end of this turn: ${tallyWords(todoTally(counts), 'left open')}`;
}

/** The kept list's facts: when the turn left it (`clock`), how many rows it left open or "all done", and the rows past those kept. */
export function snapshotFacts(counts: TodoCounts, clock: string): string[] {
  const { blocked = 0, open = 0, omitted = 0 } = counts;
  return [`As this turn left it, ${clock}`, open ? `${open} left open` : blocked ? '' : 'all done', omitted ? `${omitted} more not shown` : ''].filter(Boolean);
}

/** The kept list's sections, as the live reader's: In progress, Blocked, To do and Done (newest first), each in the order uam kept them. */
export const snapshotSections = (todos: readonly Todo[]) => stageSections(todos);

/** A row of the history reader's ledger: its latest state, when its status last changed (`at`) and in which turn (`turn`, 1-based among the Thread's timings; none for the running turn's change), and whether the agents removed it from the live list. */
export interface LedgerRow { todo: Todo & { agent?: string }; at?: string; turn?: number; removed: boolean }

/**
 * Every row the Thread's turns kept, merged by id: `turns` oldest to newest, each row keeping the
 * latest status, title, note and agent, with `at` (the row's `changed_at`, else the record's end)
 * and `turn` from the record that last changed its status or first saw it; then the live list,
 * the newest state, whose rows no record holds stamp without a turn (the running turn has no
 * record yet). `removed`: the live list is known, whole, and the row is not in it.
 */
export function todoLedger(history: TodoHistory, timings: readonly TurnTiming[] = []): LedgerRow[] {
  const rows = new Map<string, LedgerRow>();
  const take = (t: Todo & { agent?: string }, at?: string, turn?: number) => {
    const was = rows.get(t.id);
    if (was && was.todo.status === t.status) was.todo = { ...was.todo, ...t };
    else rows.set(t.id, { todo: { ...was?.todo, ...t }, at, turn, removed: false });
  };
  for (const rec of history.turns) {
    const turn = timings.findIndex((t) => t.id === rec.timing_id) + 1 || undefined;
    for (const t of rec.todos) take(t, t.changed_at ?? rec.ended_at, turn);
  }
  const live = history.live;
  if (live?.known) {
    for (const t of live.todos) take(t, t.changed_at);
    // The live list holds its first 100 rows: past them a row absent from it may still be there.
    if (!live.omitted) {
      const kept = new Set(live.todos.map((t) => t.id));
      for (const row of rows.values()) row.removed = !kept.has(row.todo.id);
    }
  }
  return [...rows.values()];
}

const STAMP: Record<TodoStatus, string> = { done: 'done', in_progress: 'in progress', blocked: 'blocked', pending: 'added' };

/** A ledger row's stamp: "done 13:58 · turn 2", "in progress since 14:03", "added 13:40 · turn 1"; `clock` is `at` as a clock time, "" without one. */
export function ledgerStamp(row: LedgerRow, clock: string): string {
  const state = STAMP[row.todo.status];
  return [clock ? `${state}${row.todo.status === 'in_progress' ? ' since' : ''} ${clock}` : state, row.turn ? `turn ${row.turn}` : ''].filter(Boolean).join(' · ');
}

/** The ledger's sections in the stages' order, each newest change first (a change without a time, the running turn's, first). */
export function ledgerSections(rows: readonly LedgerRow[]): TodoSection[] {
  const when = (r: LedgerRow) => (r.at ? Date.parse(r.at) : Number.MAX_SAFE_INTEGER);
  return stageSections([...rows].sort((a, b) => when(b) - when(a)).map((r) => r.todo));
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
