// The todo list the agents keep, read-only: the live list, opened from the status strip's todo
// button (see StatusLine), and the list as a turn left it, opened from its reply's foot
// (`TurnTodo`). Both are the one anchored reader (`Reader`): a modal popover beside what opened
// it, that lifted above the dim, a compact head with the count and meter, eyebrow sections by
// stage and a keycap foot. A phone gets a sheet from the bottom. The owner changes the list by
// asking in the chat.

import { Ban, Check, Circle, CircleDot, ListChecks, type LucideIcon } from 'lucide-react';
import { useContext, useEffect, useId, useRef, useState, type ReactNode, type RefObject } from 'react';
import { useApi } from '../ApiContext';
import { describeError, isStatus, type Todo, type TodoCounts, type TodoStatus, type TurnTiming, type TurnTodos } from '../api';
import { cn } from '../lib/cn';
import { METER_ROWS, keepSnapshot, recallSnapshot, snapshotFacts, snapshotSections, todoWord, turnTodoName, type TodoSection } from '../lib/todos';
import { SessionContext, clockTime } from './common';
import { Key } from './InlinePicker';
import { DOT as IDENTITY_DOT, useSubagentReplies } from './Subagents';
import { Blocks, type Block } from './ui/blocks';
import { PHONE } from './ui/dialog';
import { PanelFoot, PanelHead, PanelSection } from './ui/panel';
import { AnchoredReader } from './ui/reader';

/** Each stage's colour, the same on every surface: its words, numbers and glyph (`text`) and its meter segments (`fill`). */
const TONE: Record<TodoStatus, { text: string; fill: string }> = {
  in_progress: { text: 'text-accent', fill: 'bg-accent' },
  blocked: { text: 'text-warning', fill: 'bg-warning' },
  pending: { text: 'text-muted', fill: 'bg-muted' },
  done: { text: 'text-success', fill: 'bg-success' },
};

const GLYPH: Record<TodoStatus, LucideIcon> = { in_progress: CircleDot, blocked: Ban, pending: Circle, done: Check };

/** One short segment per row in its state's colour; the words beside it always say the same. */
export function TodoMeter({ statuses, className }: Readonly<{ statuses: readonly TodoStatus[]; className?: string }>) {
  return (
    <span aria-hidden="true" className={cn('flex shrink-0 items-center gap-0.5', className)}>
      {statuses.map((status, i) => (
        <span key={i} className={cn('h-1 w-3 rounded-full', TONE[status].fill)} />
      ))}
    </span>
  );
}

/** A stage's glyph in its colour, never a ring (the ring stays at the running step): `◉` in progress, `⊘` blocked, `○` to do, `✓` done. */
function TodoMark({ status, className }: Readonly<{ status: TodoStatus; className?: string }>) {
  const Glyph = GLYPH[status];
  return <Glyph aria-hidden="true" className={cn('size-3.5 shrink-0', TONE[status].text, className)} strokeWidth={status === 'done' ? 2.5 : undefined} />;
}

/** The subagent that first wrote a row: its identity dot, as on its row, and its name. */
function AgentTag({ id, name, quiet = false }: Readonly<{ id: string; name?: string; /** The dot alone on screen: the row's title already says the name. */ quiet?: boolean }>) {
  const tone = useSubagentReplies()?.tones.get(id);
  return (
    <span className="flex h-5 max-w-36 shrink-0 items-center gap-1 text-caption text-muted">
      <span aria-hidden="true" className={cn('size-1.5 shrink-0 rounded-full', tone ? IDENTITY_DOT[tone] : 'bg-faint')} />
      <span className="sr-only">, by </span>
      <span className={quiet ? 'sr-only' : 'truncate'}>{name || 'subagent'}</span>
    </span>
  );
}

const sameWords = (a: string, b?: string) => !!b && a.trim().toLowerCase() === b.trim().toLowerCase();

/** One row: its stage's glyph, its title ("now" after the row the work is at), the blocked reason under it, the ledger's stamp under that, and the subagent that first wrote it. */
function TodoRow({ todo, name, now = false, quietName = false, stamp }: Readonly<{ todo: Todo; name?: string; now?: boolean; /** A name that repeats the title shows as the dot alone. */ quietName?: boolean; stamp?: ReactNode }>) {
  return (
    <li className="flex min-h-7 items-start gap-2 py-1 pointer-coarse:min-h-9">
      <span className="flex h-5 w-4 shrink-0 items-center justify-center">
        <TodoMark status={todo.status} />
      </span>
      <span className="flex min-w-0 flex-1 flex-col">
        <span className={cn('text-ui break-words', todo.status === 'done' ? 'text-muted' : todo.status === 'in_progress' ? 'font-medium text-ink' : 'text-body')}>
          {todo.title}
          {now && <span className="ml-1.5 text-caption font-normal text-accent">now</span>}
        </span>
        {todo.status === 'blocked' && todo.note && <span className="text-caption break-words text-warning">{todo.note}</span>}
        {stamp && <span className="text-caption tabular-nums text-muted">{stamp}</span>}
      </span>
      {todo.agent_id && <AgentTag id={todo.agent_id} name={name} quiet={quietName && sameWords(todo.title, name)} />}
      <span className="sr-only">, {todoWord(todo.status)}</span>
    </li>
  );
}

/** What a reader shows: the rows the meter draws, the counts, the facts line, the sections, the row the work is at (`now`, the live list), the foot's words, each row's subagent name, and the strip's blocks a phone's sheet repeats on top (it covers the strip). The history reader swaps the head's `glyph` and its `right`-hand words, and stamps each row. */
export interface ReaderParts { todos: readonly Todo[]; counts: TodoCounts; facts: string[]; sections: TodoSection[]; now?: string; foot: string; name: (todo: Todo) => string | undefined; blocks?: ReactNode; glyph?: LucideIcon; right?: ReactNode; stamp?: (todo: Todo) => ReactNode }

/** The list in the reader: the count and meter in the head, the facts, then the sections, each row with the subagent that wrote it. */
function TodoBody({ parts, sheet, heading, close, title }: Readonly<{ parts: ReaderParts; sheet: boolean; heading: RefObject<HTMLHeadingElement | null>; close: ReactNode; title: string }>) {
  const { done = 0, total = 0 } = parts.counts;
  const Glyph = parts.glyph ?? ListChecks;
  return (
    <div className="flex min-h-0 flex-col">
      <PanelHead className={cn('gap-1 pt-2 pb-2', sheet ? 'pr-2 pl-4' : 'pr-3 pl-4')}>
        <div className="flex min-h-7 min-w-0 items-center gap-2">
          <Glyph aria-hidden="true" className="size-4 shrink-0 text-muted" />
          <h2 ref={heading} tabIndex={-1} className="min-w-0 flex-1 truncate text-title text-ink outline-hidden">
            {title}
          </h2>
          {parts.right ?? (
            <>
              {total <= METER_ROWS && parts.todos.length === total && <TodoMeter statuses={parts.todos.map((t) => t.status)} />}
              <span className="shrink-0 text-caption tabular-nums text-muted">
                <span className="text-body">{done} done</span> of {total}
              </span>
            </>
          )}
          {close}
        </div>
        {parts.facts.length > 0 && <p className="pl-6 text-caption text-muted">{parts.facts.join(' · ')}</p>}
      </PanelHead>
      <div className="flex min-h-0 flex-col gap-3 overflow-x-hidden overflow-y-auto overscroll-contain px-4 pt-1 pb-3">
        {sheet && parts.blocks}
        {parts.sections.map((section) => (
          <PanelSection
            key={section.status}
            label={section.label}
            icon={
              <span className="flex w-4 shrink-0 justify-center">
                <TodoMark status={section.status} className="size-3" />
              </span>
            }
            meta={<span className={cn('tabular-nums', TONE[section.status].text)}>{section.todos.length}</span>}
          >
            <ul>
              {section.todos.map((todo) => (
                <TodoRow key={todo.id} todo={todo} now={todo.id === parts.now && todo.status === 'in_progress'} name={todo.agent_id ? parts.name(todo) : undefined} quietName stamp={parts.stamp?.(todo)} />
              ))}
            </ul>
          </PanelSection>
        ))}
      </div>
      {sheet ? (
        <p className="flex min-h-11 shrink-0 items-center px-4 pb-2 text-caption text-muted">{parts.foot}</p>
      ) : (
        <PanelFoot>
          <span className="flex items-center gap-1.5">
            <Key>Esc</Key>close
          </span>
          <span className="flex-1" />
          <span>{parts.foot}</span>
        </PanelFoot>
      )}
    </div>
  );
}

/**
 * The reader beside `anchor` on its `side`, or on a phone (`phone`, chosen when it opened, so a
 * resize does not remount it) the sheet. It opens on its "Todo" heading and gives focus back to
 * `anchor` when it closes; Esc, its close button and a press outside close it.
 */
export function Reader({ id, open, phone, anchor, side, label, title = 'Todo', parts, onClose, onClosed }: Readonly<{
  id: string;
  open: boolean;
  phone: boolean;
  anchor: HTMLElement | null;
  side: 'top' | 'bottom';
  label: string;
  title?: string;
  parts: ReaderParts;
  onClose: () => void;
  onClosed: () => void;
}>) {
  const heading = useRef<HTMLHeadingElement>(null);
  const sheet = phone || !anchor;
  return (
    <AnchoredReader id={id} open={open} sheet={sheet} anchor={anchor} side={side} label={label} className="w-[440px]" initialFocus={heading} finalFocus={() => anchor} onClose={onClose} onClosed={onClosed}>
      {(close) => <TodoBody title={title} parts={parts} sheet={sheet} heading={heading} close={close} />}
    </AnchoredReader>
  );
}

/**
 * A reply's outcome bar, for a turn that changed the todo list: "Todo" and its blocks (the
 * button's name says them in words). It opens the list as the turn left it, which uam kept when
 * the turn ended; a list read stays in a small memory cache.
 */
export function TurnTodo({ timing, blocks }: Readonly<{ timing: TurnTiming; /** Its counts as the strip draws them (`todoBlocks`). */ blocks: Block[] }>) {
  const api = useApi();
  const sessionId = useContext(SessionContext);
  const id = useId();
  const [button, setButton] = useState<HTMLButtonElement | null>(null);
  const [reader, setReader] = useState<{ open: boolean; phone: boolean } | null>(null);
  const [snap, setSnap] = useState<TurnTodos | { error: string } | null>(null);
  const reading = useRef<AbortController | null>(null);
  useEffect(() => () => reading.current?.abort(), []);
  const counts = timing.todo;
  if (!sessionId || !counts?.total) return null;
  const show = () => {
    setReader({ open: true, phone: window.matchMedia(PHONE).matches });
    reading.current?.abort();
    const kept = recallSnapshot(sessionId, timing.id);
    setSnap(kept ?? null);
    if (kept) return;
    const controller = new AbortController();
    reading.current = controller;
    api.turnTodos(sessionId, timing.id, controller.signal).then(
      (s) => {
        keepSnapshot(sessionId, timing.id, s);
        setSnap(s);
      },
      (err: unknown) => {
        if (!controller.signal.aborted) setSnap({ error: isStatus(err, 404) ? 'This turn’s list is no longer kept' : describeError(err) });
      },
    );
  };
  const clock = clockTime(timing.ended_at ?? timing.started_at);
  const rows = snap && 'todos' in snap ? snap : null;
  const agents = new Map(rows?.todos.map((r) => [r.id, r.agent]));
  const parts: ReaderParts = {
    todos: rows?.todos ?? [],
    counts: rows?.counts ?? counts,
    facts: rows ? snapshotFacts(rows.counts, clock) : [snap && 'error' in snap ? snap.error : 'Loading the list…'],
    sections: rows ? snapshotSections(rows.todos) : [],
    foot: 'Kept by uam when the turn ended',
    name: (t) => agents.get(t.id),
  };
  return (
    <span className="flex items-center gap-2">
      <span aria-hidden="true" className="text-eyebrow uppercase text-muted">Todo</span>
      <button
        ref={setButton}
        type="button"
        aria-haspopup="dialog"
        aria-expanded={!!reader?.open}
        aria-controls={reader?.open ? id : undefined}
        aria-label={turnTodoName(counts)}
        className="relative flex h-6 items-center rounded-sm transition-opacity duration-100 after:absolute after:inset-x-0 after:-inset-y-1 after:content-[''] hover:opacity-80 pointer-coarse:after:-inset-y-2.5"
        onClick={show}
      >
        <Blocks items={blocks} />
      </button>
      {reader && (
        <Reader id={id} open={reader.open} phone={reader.phone} anchor={button} side="bottom" label="Todo at the end of this turn" parts={parts} onClose={() => setReader((r) => r && { ...r, open: false })} onClosed={() => setReader(null)} />
      )}
    </span>
  );
}
