import { Dialog as BaseDialog } from '@base-ui/react/dialog';
import { Popover as BasePopover } from '@base-ui/react/popover';
import { MessageCircleQuestion, X } from 'lucide-react';
import { Fragment, useEffect, useId, useLayoutEffect, useRef, useState, useSyncExternalStore, type ReactNode } from 'react';
import { useApi } from '../ApiContext';
import { describeError, type ApiClient, type Aside, type AsideAnswer, type Command, type SessionDetail } from '../api';
import { asideQuestion, asideTurn } from '../lib/aside';
import { cn } from '../lib/cn';
import { Markdown, clockTime, dateTime } from './common';
import { Key } from './InlinePicker';
import { BottomSheet, LiftedRow } from './Subagents';
import { PHONE } from './Todos';
import { Button } from './ui/button';
import { backdropClass } from './ui/dialog';
import { Clamp, PanelFoot, PanelHead, PanelSection } from './ui/panel';
import { Spinner } from './ui/spinner';

/** `/btw <question>`: a question to the open conversation, answered by its current model, kept with the Task and never sent to the agent. A bare `/btw` shows the Task's asides. */
export const ASIDE_COMMAND = 'btw';
const CLOSED_REASON = "Ask aside needs the Task's conversation open. Send a prompt to open it first.";
const ASIDE: Command = { name: ASIDE_COMMAND, description: 'Ask aside: a quick question about this Task, not added to it', kind: 'command', input_hint: 'question', allow_during_turn: true };
const listed = new WeakMap<readonly Command[], { open: Command[]; closed: Command[] }>();

/**
 * The Task's command list with `/btw` first, in place of any command of that name; disabled with the
 * reason while the conversation is closed. One array per list and state, so the composer's memos hold.
 */
export function withAside(commands: readonly Command[], open: boolean): Command[] {
  let both = listed.get(commands);
  if (!both) {
    const rest = commands.filter((c) => c.name !== ASIDE_COMMAND);
    both = { open: [ASIDE, ...rest], closed: [{ ...ASIDE, disabled_reason: CLOSED_REASON }, ...rest] };
    listed.set(commands, both);
  }
  return open ? both.open : both.closed;
}

/** An aside this page asked that the Task has not kept: waiting, failed, or answered when keeping it failed. */
export interface LocalAside {
  question: string;
  asked_at: string;
  /** The turn it will belong to, as this page sees the transcript; the kept record's is the service's. */
  turn: string;
  working: boolean;
  answer?: AsideAnswer;
  error?: string;
}

/** A Task's kept asides, oldest first, read once per page (and again after the stream reconnects), and this page's own one. */
interface TaskAsides {
  kept: readonly Aside[];
  read: 'no' | 'reading' | 'yes' | 'failed';
  local: LocalAside | null;
}

const NONE: TaskAsides = { kept: [], read: 'no', local: null };
let pages = new WeakMap<object, Map<string, TaskAsides>>();
const listeners = new Set<() => void>();
const subscribe = (fn: () => void) => {
  listeners.add(fn);
  return () => { listeners.delete(fn); };
};
const readOf = (owner: object, task: string) => pages.get(owner)?.get(task) ?? NONE;
function write(owner: object, task: string, change: (t: TaskAsides) => TaskAsides) {
  let page = pages.get(owner);
  if (!page) pages.set(owner, (page = new Map()));
  page.set(task, change(readOf(owner, task)));
  listeners.forEach((fn) => fn());
}
/** Forgets every page's asides, as a reload does (tests start from an empty page). */
export function forgetAsides() {
  pages = new WeakMap();
}

const asked = (a: { asked_at: string }) => Date.parse(a.asked_at);
/** `kept` with `more` it lacks, oldest first. Kept asides never change, so one seen twice is the same. */
function merged(kept: readonly Aside[], more: readonly Aside[]): readonly Aside[] {
  const ids = new Set(kept.map((a) => a.id));
  const add = more.filter((a) => !ids.has(a.id));
  return add.length ? [...kept, ...add].sort((a, b) => asked(a) - asked(b)) : kept;
}

/** A kept aside: the Task's `aside` event, or the reply to this page's question. */
export function keepAside(owner: ApiClient, task: string, aside: Aside) {
  write(owner, task, (t) => ({ ...t, kept: merged(t.kept, [aside]) }));
}

/** The event stream reconnected and may have missed asides: each Task read is read again when next shown. */
export function rereadAsides(owner: ApiClient) {
  const page = pages.get(owner);
  if (!page) return;
  for (const [task, t] of page) if (t.read !== 'no') page.set(task, { ...t, read: 'no' });
  listeners.forEach((fn) => fn());
}

/** Task `task`'s asides (none for ''). The view that `load`s them (the Task's composer) reads the kept ones from the service the first time. */
export function useTaskAsides(task: string, load = false): TaskAsides {
  const api = useApi();
  const t = useSyncExternalStore(subscribe, () => (task ? readOf(api, task) : NONE));
  useEffect(() => {
    if (!load || !task || readOf(api, task).read !== 'no') return;
    write(api, task, (x) => ({ ...x, read: 'reading' }));
    api.asides(task).then(
      (list) => write(api, task, (x) => ({ ...x, kept: merged(x.kept, list), read: 'yes' })),
      () => write(api, task, (x) => ({ ...x, read: 'failed' })),
    );
  }, [api, task, load, t.read]);
  return t;
}

const waits = (a: LocalAside | null) => !!a && !a.answer && a.error === undefined;

/**
 * This Task's asides and the one question that may wait at a time. A new question carries the latest
 * kept asides as context (lib/aside); its answer is kept with the Task, and only an answer whose record
 * could not be kept stays on this page alone. A change of Task, conversation, selection, stage or open
 * state, a change of the owning API and unmounting abort the wait and drop the question.
 */
export function useAsides(session: SessionDetail, live: boolean) {
  const api = useApi();
  const t = useTaskAsides(session.id, true);
  const waiting = useRef<{ controller: AbortController; owner: ApiClient; task: string } | null>(null);
  const pending = waits(t.local);
  const scope = [session.id, session.provider, session.conversation_id, session.model, session.effort, session.context_size, session.stage, session.open].join('\u0000');

  const cancel = () => {
    const w = waiting.current;
    if (!w) return;
    waiting.current = null;
    w.controller.abort();
    write(w.owner, w.task, (x) => ({ ...x, local: null }));
  };
  // `cancel` reads only the ref: the wait ends with its scope or owner.
  useEffect(() => cancel, [api, scope]);

  const ask = (question: string) => {
    const text = question.trim();
    if (!text || pending) return;
    const owner = api, task = session.id;
    const prior = t.kept.map((a) => ({ question: a.question, answer: a.answer }));
    const local: LocalAside = { question: text, asked_at: new Date().toISOString(), turn: asideTurn(session.items), working: live };
    const controller = new AbortController();
    waiting.current = { controller, owner, task };
    write(owner, task, (x) => ({ ...x, local }));
    const settle = (next: LocalAside | null, kept?: Aside) => {
      if (controller.signal.aborted) return;
      waiting.current = null;
      write(owner, task, (x) => ({ ...x, kept: kept ? merged(x.kept, [kept]) : x.kept, local: next }));
    };
    owner.askAside(task, asideQuestion(prior, text), text, controller.signal).then(
      (answer) => settle(answer.aside ? null : { ...local, answer }, answer.aside),
      // Copy bounded code units so a substring cannot retain a large remote error.
      (err: unknown) => settle({ ...local, error: describeError(err).slice(0, 512).split('').join('') }),
    );
  };
  return { kept: t.kept, local: t.local, read: t.read, pending, ask };
}

/** One aside as a reader lists it, kept or this page's own. */
interface Row {
  key: string;
  question: string;
  asked_at: string;
  working: boolean;
  answer?: string;
  truncated?: boolean;
  error?: string;
}

const rowOf = (a: Aside): Row => ({ key: a.id, question: a.question, asked_at: a.asked_at, working: !!a.working, answer: a.answer, truncated: a.truncated });
const localRow = (a: LocalAside): Row => ({ key: 'local', question: a.question, asked_at: a.asked_at, working: a.working, answer: a.answer?.text, truncated: a.answer?.truncated, error: a.error });

/** `code` spans in a question, the rest as typed. */
function Question({ text }: Readonly<{ text: string }>) {
  return <>{text.split('`').map((part, n) => (n % 2 ? <code key={n} className="rounded-xs bg-sunken px-1 font-mono text-code-sm">{part}</code> : <Fragment key={n}>{part}</Fragment>))}</>;
}

function Entry({ row }: Readonly<{ row: Row }>) {
  return (
    <article aria-label="Aside">
      <PanelSection label={clockTime(row.asked_at)} meta={<span title={dateTime(row.asked_at)}>{row.working ? 'while the agent worked' : 'after the turn ended'}</span>}>
        <Clamp noun="question" height={44}><p className="break-words text-ui font-medium text-ink"><Question text={row.question} /></p></Clamp>
        {row.error !== undefined ? <p role="status" className="text-caption text-error">{row.error}</p>
          : row.answer !== undefined ? <>
            <Markdown text={row.answer} className="text-ui" />
            {row.truncated && <p className="text-caption text-muted">The answer was longer; only its start is shown.</p>}
          </>
            : <p className="text-caption text-muted">Waiting for the answer…</p>}
      </PanelSection>
    </article>
  );
}

/** Where a reader opens: its anchor, the row lifted above the dimmed page (none over the composer), and whether it is a phone's sheet. */
export interface AsideReaderAt {
  open: boolean;
  phone: boolean;
  anchor: HTMLElement | null;
  lift?: Element;
}

/**
 * The anchored reader of asides (DESIGN.md Floating panels): compact head, one eyebrow section per
 * aside, oldest first, and a keycap foot; a bottom sheet on a phone. `newest` scrolls to the last one,
 * where a new question lands.
 */
export function AsideReader({ at, title, facts, rows, empty, newest = false, onClose, onClosed, finalFocus }: Readonly<{
  at: AsideReaderAt;
  title: string;
  facts: ReactNode;
  rows: readonly Row[];
  empty?: ReactNode;
  newest?: boolean;
  onClose: () => void;
  onClosed: () => void;
  finalFocus: () => HTMLElement | null;
}>) {
  const id = useId();
  const heading = useRef<HTMLHeadingElement>(null);
  const scroller = useRef<HTMLDivElement>(null);
  const sheet = at.phone || !at.anchor;
  const last = rows.at(-1);
  const settled = !!last && (last.answer !== undefined || last.error !== undefined);
  // Opening, a new question and its answer land at the bottom, where the newest aside is.
  useLayoutEffect(() => {
    const el = scroller.current;
    if (el && newest) el.scrollTop = el.scrollHeight;
  }, [newest, at.open, rows.length, settled]);
  const body = (close: ReactNode) => <div className="flex min-h-0 flex-1 flex-col">
    <PanelHead className={cn('gap-0.5 pt-2 pb-2 pl-4', sheet ? 'pr-2' : 'pr-3')}>
      <div className="flex min-h-7 min-w-0 items-center gap-2">
        <MessageCircleQuestion aria-hidden="true" className="size-4 shrink-0 text-muted" />
        <h2 ref={heading} tabIndex={-1} className="min-w-0 flex-1 truncate text-title text-ink outline-hidden">{title}</h2>
        {close}
      </div>
      <p className="pl-6 text-caption text-muted">{facts}</p>
    </PanelHead>
    <div ref={scroller} className="flex min-h-0 flex-1 flex-col gap-3 overflow-x-hidden overflow-y-auto overscroll-contain px-4 pt-1 pb-4">
      {rows.length ? rows.map((row, n) => <Fragment key={row.key}>
        {n > 0 && <div aria-hidden="true" className="fade-rule" />}
        <Entry row={row} />
      </Fragment>) : empty}
    </div>
    {sheet
      ? <p className="flex min-h-11 shrink-0 items-center gap-1.5 px-4 pb-2 text-caption text-muted"><Key>/btw</Key>ask another · Never sent to the agent</p>
      : <PanelFoot>
        <span className="flex items-center gap-1.5"><Key>Esc</Key>close</span>
        <span className="flex items-center gap-1.5"><Key>/btw</Key>ask another</span>
        <span className="flex-1" />
        <span>Never sent to the agent</span>
      </PanelFoot>}
  </div>;
  if (sheet) return <BottomSheet id={id} open={at.open} onClose={onClose} onClosed={onClosed} label={title} initialFocus={heading} finalFocus={finalFocus} className="duration-160" backdropClassName="duration-160">
    {body(<BaseDialog.Close render={<Button size="icon-sm" aria-label="Close" className="text-muted" />}><X /></BaseDialog.Close>)}
  </BottomSheet>;
  return <BasePopover.Root open={at.open} modal onOpenChange={(o) => !o && onClose()} onOpenChangeComplete={(o) => !o && onClosed()}>
    <BasePopover.Portal>
      <BasePopover.Backdrop className={`${backdropClass} duration-160`} />
      {at.lift && at.open && at.lift.isConnected && <LiftedRow row={at.lift} />}
      <BasePopover.Positioner anchor={at.anchor} side={at.lift ? 'bottom' : 'top'} align="start" sideOffset={8} collisionPadding={12} className="z-50 outline-hidden">
        <BasePopover.Popup id={id} data-popup="" aria-label={title} aria-modal="true" initialFocus={heading} finalFocus={finalFocus} className={cn('flex max-h-[min(70vh,var(--available-height))] max-w-(--available-width) origin-(--transform-origin) flex-col overflow-hidden rounded-lg bg-raised text-body shadow-modal outline-hidden transition-[opacity,scale] duration-160 ease-app data-starting-style:scale-[0.98] data-starting-style:opacity-0 data-ending-style:scale-[0.98] data-ending-style:opacity-0', at.lift ? 'w-[560px]' : 'w-[min(640px,var(--anchor-width))]')}>
          {body(<BasePopover.Close render={<Button size="icon-sm" aria-label="Close" className="text-muted" />}><X /></BasePopover.Close>)}
        </BasePopover.Popup>
      </BasePopover.Positioner>
    </BasePopover.Portal>
  </BasePopover.Root>;
}

const count = (n: number) => (n === 1 ? '1 aside' : `${n} asides`);
const NOT_READ = 'Earlier asides could not be read; they show after the page reconnects.';

/** Bare `/btw` and a new question: every aside of the Task, oldest first, over the composer. */
export function TaskAsideReader({ asides, at, onClose, onClosed, finalFocus }: Readonly<{ asides: ReturnType<typeof useAsides>; at: AsideReaderAt; onClose: () => void; onClosed: () => void; finalFocus: () => HTMLElement | null }>) {
  const rows = [...asides.kept.map(rowOf), ...(asides.local ? [localRow(asides.local)] : [])];
  const facts = `${count(rows.length)}, oldest first. ${asides.read === 'failed' ? NOT_READ : 'Answered from this conversation with its current model, without tools.'}`;
  const empty = <p className="py-2 text-ui text-muted">{asides.read === 'reading' || asides.read === 'no' ? 'Reading the asides…' : 'No asides yet. Type /btw and a question to ask one.'}</p>;
  return <AsideReader at={at} title="Asides in this Task" facts={facts} rows={rows} empty={empty} newest onClose={onClose} onClosed={onClosed} finalFocus={finalFocus} />;
}

/**
 * The foot chip of a turn with asides (`btw 2`): those asked while it ran or after it ended, the ring
 * while one of them waits. A press lists them in the reader under the lifted chip.
 */
export function AsideChip({ task, turn }: Readonly<{ task: string; turn: string }>) {
  const { kept, local } = useTaskAsides(task);
  const [at, setAt] = useState<AsideReaderAt | null>(null);
  const button = useRef<HTMLButtonElement>(null);
  const mine = kept.filter((a) => a.turn === turn);
  const waiting = waits(local) && local!.turn === turn;
  const n = mine.length + (waiting ? 1 : 0);
  if (!n) return null;
  const rows = [...mine.map(rowOf), ...(waiting ? [localRow(local!)] : [])];
  return (
    <>
      {/* A phone's foot is already full: there the chip is its glyph and count, so Turn actions stay in view. */}
      <span aria-hidden="true" className="max-sm:hidden"> · </span>
      <button ref={button} type="button" data-aside-chip="" aria-haspopup="dialog" aria-expanded={!!at?.open} aria-label={`${count(n)} in this turn${waiting ? ', one waiting for its answer' : ''}`} className="flex h-6 items-center gap-1 rounded-sm bg-raised px-1.5 text-stamp max-sm:px-1 text-muted transition-colors duration-100 hover:bg-tint-hover hover:text-ink aria-expanded:text-ink" onClick={(e) => setAt({ open: true, phone: window.matchMedia(PHONE).matches, anchor: e.currentTarget, lift: e.currentTarget })}>
        {waiting ? <Spinner className="size-2.5 border-current border-r-transparent text-accent" /> : <MessageCircleQuestion aria-hidden="true" className="size-3" />}
        <span className="font-medium max-sm:hidden">btw</span>
        <span className="tabular-nums">{n}</span>
      </button>
      {at && <AsideReader at={at} title="Asides in this turn" facts={`${count(n)} asked while it ran or after it ended, oldest first.`} rows={rows} onClose={() => setAt((a) => a && { ...a, open: false })} onClosed={() => setAt(null)} finalFocus={() => (button.current?.isConnected ? button.current : null)} />}
    </>
  );
}

/** Whether the turn has asides to chip, so its foot stays in view. */
export function useTurnHasAsides(task: string, turn: string | undefined): boolean {
  const { kept, local } = useTaskAsides(turn ? task : '');
  return !!turn && (kept.some((a) => a.turn === turn) || (waits(local) && local!.turn === turn));
}
