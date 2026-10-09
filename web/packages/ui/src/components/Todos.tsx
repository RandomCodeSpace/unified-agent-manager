// The todo reader (DESIGN.md Todo reader): the list the agents keep, opened from the status line,
// and the list as a turn left it, opened from its reply's foot (`TurnTodo`). On a wide screen it
// is an anchored reader: a modal popover beside what opened it, that lifted above the dim, a
// compact head with the count and meter, eyebrow sections and a keycap foot. A phone gets the
// subagents' sheet from the bottom. It only shows the list: the owner changes it by asking in the
// chat.

import { Dialog as BaseDialog } from '@base-ui/react/dialog';
import { Popover as BasePopover } from '@base-ui/react/popover';
import { Ban, Check, Circle, CircleDot, ListChecks, X, type LucideIcon } from 'lucide-react';
import { Fragment, useContext, useEffect, useId, useRef, useState, type ReactNode, type RefObject } from 'react';
import { useApi } from '../ApiContext';
import { describeError, isStatus, type Subagent, type Todo, type TodoCounts, type TodoStatus, type TodoView, type TurnTiming, type TurnTodos } from '../api';
import { cn } from '../lib/cn';
import { METER_ROWS, keepSnapshot, recallSnapshot, snapshotFacts, snapshotSections, todoSections, todoTally, todoWord, turnTodoName, type TodoSection, type TodoTally } from '../lib/todos';
import { SessionContext, clockTime } from './common';
import { Key } from './InlinePicker';
import { BottomSheet, DOT as IDENTITY_DOT, LiftedRow, useSubagentReplies } from './Subagents';
import { Button } from './ui/button';
import { backdropClass } from './ui/dialog';
import { PanelFoot, PanelHead, PanelSection } from './ui/panel';

/** The narrow layout, where the reader is a sheet (as the subagents' is). */
export const PHONE = '(max-width: 639px)';

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

/**
 * The open stages' counts after "2/7", each behind `sep`, its glyph and number in its colour, then
 * its words: "◉ 2 in progress · ⊘ 1 blocked · ○ 3 to do"; counts kept before the split add "1 open".
 * A phone drops the words, never the glyphs (the names say them all), so no count is colour alone.
 */
export function TodoStages({ tally, sep }: Readonly<{ tally: TodoTally; sep: ReactNode }>) {
  return (
    <>
      {tally.stages.map(({ status, n }) => (
        <Fragment key={status}>
          {sep}
          <span className={cn('flex shrink-0 items-center gap-1 tabular-nums', TONE[status].text)}>
            <TodoMark status={status} className="size-3" />
            {n}
            <span className="max-sm:hidden"> {todoWord(status).toLowerCase()}</span>
          </span>
        </Fragment>
      ))}
      {tally.open > 0 && (
        <>
          {sep}
          <span className="shrink-0 tabular-nums">{tally.open} open</span>
        </>
      )}
    </>
  );
}

/** The subagent that first wrote a row: its identity dot, as on its row, and its name. */
function AgentTag({ id, name }: Readonly<{ id: string; name?: string }>) {
  const tone = useSubagentReplies()?.tones.get(id);
  return (
    <span className="flex h-5 max-w-36 shrink-0 items-center gap-1 text-caption text-muted">
      <span aria-hidden="true" className={cn('size-1.5 shrink-0 rounded-full', tone ? IDENTITY_DOT[tone] : 'bg-faint')} />
      <span className="sr-only">, by </span>
      <span className="truncate">{name || 'subagent'}</span>
    </span>
  );
}

function TodoRow({ todo, name }: Readonly<{ todo: Todo; name?: string }>) {
  return (
    <li className="flex min-h-7 items-start gap-2 py-1 pointer-coarse:min-h-9">
      <span className="flex h-5 w-4 shrink-0 items-center justify-center">
        <TodoMark status={todo.status} />
      </span>
      <span className="flex min-w-0 flex-1 flex-col">
        <span className={cn('text-ui break-words', todo.status === 'done' ? 'text-muted' : todo.status === 'in_progress' ? 'font-medium text-ink' : 'text-body')}>{todo.title}</span>
        {todo.status === 'blocked' && todo.note && <span className="text-caption break-words text-warning">{todo.note}</span>}
      </span>
      {todo.agent_id && <AgentTag id={todo.agent_id} name={name} />}
      <span className="sr-only">, {todoWord(todo.status)}</span>
    </li>
  );
}

/** What a reader shows: the rows the meter draws, the counts, the facts line, the sections, the foot's words and each row's subagent name. */
interface ReaderParts { todos: readonly Todo[]; counts: TodoCounts; facts: string[]; sections: TodoSection[]; foot: string; name: (todo: Todo) => string | undefined }

/** The list in the reader: the count and meter in the head, the facts, then the sections, each row with the subagent that wrote it. */
function TodoBody({ parts, sheet, heading, close, label }: Readonly<{ parts: ReaderParts; sheet: boolean; heading: RefObject<HTMLHeadingElement | null>; close: ReactNode; label: string }>) {
  const { done = 0, total = 0 } = parts.counts;
  return (
    <div className="flex min-h-0 flex-col">
      <PanelHead className={cn('gap-1 pt-2 pb-2', sheet ? 'pr-2 pl-4' : 'pr-3 pl-4')}>
        <div className="flex min-h-7 min-w-0 items-center gap-2">
          <ListChecks aria-hidden="true" className="size-4 shrink-0 text-muted" />
          <h2 ref={heading} tabIndex={-1} className="min-w-0 flex-1 truncate text-title text-ink outline-hidden">
            {label}
          </h2>
          {total <= METER_ROWS && parts.todos.length === total && <TodoMeter statuses={parts.todos.map((t) => t.status)} />}
          <span className="shrink-0 text-caption tabular-nums text-muted">
            <span className="text-body">{done} done</span> of {total}
          </span>
          {close}
        </div>
        {parts.facts.length > 0 && <p className="pl-6 text-caption text-muted">{parts.facts.join(' · ')}</p>}
      </PanelHead>
      <div className="flex min-h-0 flex-col gap-3 overflow-x-hidden overflow-y-auto overscroll-contain px-4 pt-1 pb-3">
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
                <TodoRow key={todo.id} todo={todo} name={todo.agent_id ? parts.name(todo) : undefined} />
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

/** The 160ms dim behind the reader (the shared one takes 240ms). */
const dim = cn(backdropClass, 'duration-160');

/**
 * The live list beside `anchor`, the status line: In progress, Blocked, To do and Done;
 * `view.touched` unset says the running turn has not changed it yet.
 */
export function TodoReader({ view, subagents, label = 'Todo', ...shell }: Readonly<{
  id: string;
  open: boolean;
  phone: boolean;
  anchor: HTMLElement | null;
  view: TodoView;
  subagents: readonly Subagent[];
  label?: 'Todo' | 'Plan';
  onClose: () => void;
  onClosed: () => void;
}>) {
  const names = new Map(subagents.map((s) => [s.id, s.name]));
  const tagged = view.todos.filter((t) => t.agent_id).length;
  const facts = [
    view.touched ? '' : 'Left by the last turn; this turn has not changed it yet',
    tagged ? `${tagged} ${tagged === 1 ? 'row' : 'rows'} from subagents` : '',
    view.omitted ? `${view.omitted} more not shown` : '',
  ].filter(Boolean);
  const parts: ReaderParts = { todos: view.todos, counts: view.counts, facts, sections: todoSections(view), foot: 'To change it, ask in the chat', name: (t) => names.get(t.agent_id!) };
  return <Reader {...shell} side="top" label={label} parts={parts} />;
}

/**
 * The reader beside `anchor` on its `side`, or on a phone (`phone`, chosen when it opened, so a
 * resize does not remount it) the sheet. It opens on its "Todo" heading and gives focus back to
 * `anchor` when it closes; Esc, its close button and a press outside close it.
 */
function Reader({ id, open, phone, anchor, side, label, parts, onClose, onClosed }: Readonly<{
  id: string;
  open: boolean;
  phone: boolean;
  anchor: HTMLElement | null;
  side: 'top' | 'bottom';
  label: string;
  parts: ReaderParts;
  onClose: () => void;
  onClosed: () => void;
}>) {
  const heading = useRef<HTMLHeadingElement>(null);
  const back = () => anchor;
  if (phone || !anchor) {
    return (
      <BottomSheet id={id} open={open} onClose={onClose} onClosed={onClosed} label={label} className="duration-160" backdropClassName="duration-160" initialFocus={heading} finalFocus={back}>
        <TodoBody
          label={label}
          parts={parts}
          sheet
          heading={heading}
          close={
            <BaseDialog.Close render={<Button size="icon-sm" aria-label="Close" className="text-muted" />}>
              <X />
            </BaseDialog.Close>
          }
        />
      </BottomSheet>
    );
  }
  return (
    <BasePopover.Root open={open} modal onOpenChange={(o) => !o && onClose()} onOpenChangeComplete={(o) => !o && onClosed()}>
      <BasePopover.Portal>
        <BasePopover.Backdrop className={dim} />
        {open && anchor.isConnected && <LiftedRow row={anchor} />}
        <BasePopover.Positioner anchor={anchor} side={side} align="start" sideOffset={10} collisionPadding={12} className="z-50 outline-hidden">
          <BasePopover.Popup
            id={id}
            data-popup=""
            aria-label={label}
            aria-modal="true"
            initialFocus={heading}
            finalFocus={back}
            className="flex max-h-[min(80vh,var(--available-height))] w-[440px] max-w-(--available-width) origin-(--transform-origin) flex-col overflow-hidden rounded-lg bg-raised text-body shadow-modal outline-hidden transition-[opacity,scale] duration-160 ease-app data-starting-style:scale-[0.98] data-starting-style:opacity-0 data-ending-style:scale-[0.98] data-ending-style:opacity-0"
          >
            <TodoBody
              label={label}
              parts={parts}
              sheet={false}
              heading={heading}
              close={
                <BasePopover.Close render={<Button size="icon-sm" aria-label="Close" className="text-muted" />}>
                  <X />
                </BasePopover.Close>
              }
            />
          </BasePopover.Popup>
        </BasePopover.Positioner>
      </BasePopover.Portal>
    </BasePopover.Root>
  );
}

/**
 * A reply's foot after its tokens, for a turn that changed the todo list: "Todo 5/7 · 1 in
 * progress · 1 blocked · 1 to do" (a phone drops "Todo" and the stages' words into the button's
 * name). It opens the list as the turn left it, which uam kept when the turn ended; a list read
 * stays in a small memory cache.
 */
export function TurnTodo({ timing }: Readonly<{ timing: TurnTiming }>) {
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
  const tally = todoTally(counts);
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
    <>
      <span aria-hidden="true"> · </span>
      <button
        ref={setButton}
        type="button"
        aria-haspopup="dialog"
        aria-expanded={!!reader?.open}
        aria-controls={reader?.open ? id : undefined}
        aria-label={turnTodoName(counts)}
        className="relative flex h-6 items-center gap-1 rounded-sm px-1 text-stamp whitespace-nowrap tabular-nums text-faint transition-colors duration-100 after:absolute after:inset-x-0 after:-inset-y-1 after:content-[''] hover:bg-tint-hover hover:text-ink aria-expanded:text-ink pointer-coarse:after:-inset-y-2.5"
        onClick={show}
      >
        <ListChecks aria-hidden="true" className="size-3 shrink-0" />
        <span>
          <span className="max-sm:sr-only">Todo </span>
          {tally.done}/{tally.total}
        </span>
        <TodoStages tally={tally} sep={<span className="max-sm:hidden">·</span>} />
      </button>
      {reader && (
        <Reader id={id} open={reader.open} phone={reader.phone} anchor={button} side="bottom" label="Todo at the end of this turn" parts={parts} onClose={() => setReader((r) => r && { ...r, open: false })} onClosed={() => setReader(null)} />
      )}
    </>
  );
}
