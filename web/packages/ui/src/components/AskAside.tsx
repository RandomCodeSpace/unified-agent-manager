import { Dialog as BaseDialog } from '@base-ui/react/dialog';
import { Popover as BasePopover } from '@base-ui/react/popover';
import { MessageCircleQuestion, X } from 'lucide-react';
import { useEffect, useId, useLayoutEffect, useReducer, useRef, type ReactNode } from 'react';
import { useApi } from '../ApiContext';
import { describeError, type AsideAnswer, type Command, type SessionDetail } from '../api';
import { asideQuestion } from '../lib/aside';
import { cn } from '../lib/cn';
import { Markdown } from './common';
import { Key } from './InlinePicker';
import { BottomSheet } from './Subagents';
import { Button } from './ui/button';
import { backdropClass } from './ui/dialog';
import { Clamp, PanelFoot, PanelHead, PanelSection } from './ui/panel';

/** `/btw <question>`: a transient question to the open conversation, answered by its current model and kept out of the transcript. */
export const ASIDE_COMMAND = 'btw';
const CLOSED_REASON = "Ask aside needs the Task's conversation open. Send a prompt to open it first.";
const ASIDE: Command = { name: ASIDE_COMMAND, description: 'Ask aside: a quick question about this Task, not added to it', kind: 'command', input_hint: 'question', input_required: true, allow_during_turn: true };
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

export interface AsideEntry {
  id: number;
  /** What the owner typed; the follow-up context sent with it is never shown. */
  question: string;
  answer?: AsideAnswer;
  error?: string;
}

/**
 * The asides asked on this page, per owning API and Task: kept in memory only, so a reload clears
 * them, and never added to the Task.
 */
let pages = new WeakMap<object, Map<string, AsideEntry[]>>();
/** Forgets every page history, as a reload does (tests start from an empty page). */
export function forgetAsides() {
  pages = new WeakMap();
}
let lastId = 0;
function pageOf(owner: object): Map<string, AsideEntry[]> {
  let page = pages.get(owner);
  if (!page) pages.set(owner, (page = new Map()));
  return page;
}

/**
 * This Task's page history and the one question that may wait at a time. A new question carries the
 * earlier answered asides as context (lib/aside). A change of Task, conversation, selection, stage or
 * open state, a change of the owning API, `cancel` and unmounting abort the wait and drop the question.
 */
export function useAsides(session: SessionDetail) {
  const api = useApi();
  const [, render] = useReducer((n: number) => n + 1, 0);
  const waiting = useRef<{ controller: AbortController; owner: object; task: string; id: number } | null>(null);
  const page = pageOf(api);
  const entries = page.get(session.id) ?? [];
  const pending = entries.some((e) => !e.answer && e.error === undefined);
  const scope = [session.id, session.provider, session.conversation_id, session.model, session.effort, session.context_size, session.stage, session.open].join('\u0000');

  const update = (owner: object, task: string, id: number, change: ((e: AsideEntry) => AsideEntry) | null) => {
    const list = pageOf(owner).get(task) ?? [];
    pageOf(owner).set(task, change ? list.map((e) => (e.id === id ? change(e) : e)) : list.filter((e) => e.id !== id));
  };
  const cancel = () => {
    const w = waiting.current;
    if (!w) return;
    waiting.current = null;
    w.controller.abort();
    update(w.owner, w.task, w.id, null);
    render();
  };
  // eslint-disable-next-line react-hooks/exhaustive-deps -- `cancel` reads only the ref; the wait ends with its scope or owner.
  useEffect(() => cancel, [api, scope]);

  const ask = (question: string) => {
    const text = question.trim();
    if (!text || pending) return;
    const owner = api, task = session.id;
    const prior = entries.flatMap((e) => (e.answer ? [{ question: e.question, answer: e.answer.text }] : []));
    const id = ++lastId;
    page.set(task, [...entries, { id, question: text }]);
    const controller = new AbortController();
    waiting.current = { controller, owner, task, id };
    render();
    const settle = (change: (e: AsideEntry) => AsideEntry) => {
      if (controller.signal.aborted) return;
      waiting.current = null;
      update(owner, task, id, change);
      render();
    };
    owner.askAside(task, asideQuestion(prior, text), controller.signal).then(
      (answer) => settle((e) => ({ ...e, answer })),
      // Copy bounded code units so a substring cannot retain a large remote error.
      (err: unknown) => settle((e) => ({ ...e, error: describeError(err).slice(0, 512).split('').join('') })),
    );
  };
  return { entries, pending, ask, cancel };
}

function Entry({ entry }: Readonly<{ entry: AsideEntry }>) {
  return <article aria-label="Aside" className="flex flex-col gap-1.5">
    <Clamp noun="question" height={44}><p className="break-words text-ui text-ink">{entry.question}</p></Clamp>
    {entry.error !== undefined ? <p role="status" className="text-caption text-error">{entry.error}</p>
      : entry.answer ? <>
        <Markdown text={entry.answer.text} className="text-ui" />
        {entry.answer.truncated && <p className="text-caption text-muted">The answer was longer; only its start is shown.</p>}
      </>
        : <p className="text-caption text-muted">Waiting for the answer…</p>}
  </article>;
}

/**
 * The card over the composer that shows this Task's asides on this page, oldest first, scrolled to the
 * newest. An anchored reader on the composer (a bottom sheet on phones); Esc or Close hides it, and the
 * next `/btw` opens it again with the history.
 */
export function AsideCard({ entries, open, phone, anchor, onClose, onClosed, finalFocus }: Readonly<{
  entries: readonly AsideEntry[];
  open: boolean;
  phone: boolean;
  anchor: HTMLElement | null;
  onClose: () => void;
  onClosed: () => void;
  finalFocus: () => HTMLElement | null;
}>) {
  const id = useId();
  const heading = useRef<HTMLHeadingElement>(null);
  const scroller = useRef<HTMLDivElement>(null);
  const sheet = phone || !anchor;
  const newest = entries.at(-1);
  const settled = !!newest && (!!newest.answer || newest.error !== undefined);
  // Opening, a new question and its answer land at the bottom, where the newest aside is.
  useLayoutEffect(() => {
    const el = scroller.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [open, entries.length, settled]);
  const body = (close: ReactNode) => <div className="flex min-h-0 flex-1 flex-col">
    <PanelHead className={cn('gap-1 pt-2 pb-2 pl-4', sheet ? 'pr-2' : 'pr-3')}>
      <div className="flex min-h-7 min-w-0 items-center gap-2">
        <MessageCircleQuestion aria-hidden="true" className="size-4 shrink-0 text-muted" />
        <h2 ref={heading} tabIndex={-1} className="min-w-0 flex-1 truncate text-title text-ink outline-hidden">Ask aside</h2>
        {close}
      </div>
      <p className="pl-6 text-caption text-muted">Answered from this conversation with its current model, without tools.</p>
    </PanelHead>
    <div ref={scroller} className="flex min-h-0 flex-1 flex-col overflow-x-hidden overflow-y-auto overscroll-contain px-4 pt-1 pb-4">
      <PanelSection label="On this page" meta="Kept until you leave or reload">
        <div className="flex flex-col gap-3">
          {entries.map((e, n) => <div key={e.id} className="flex flex-col gap-3">
            {n > 0 && <div aria-hidden="true" className="fade-rule" />}
            <Entry entry={e} />
          </div>)}
        </div>
      </PanelSection>
    </div>
    {sheet
      ? <p className="flex min-h-11 shrink-0 items-center px-4 pb-2 text-caption text-muted">Not added to the Task</p>
      : <PanelFoot>
        <span className="flex items-center gap-1.5"><Key>Esc</Key>dismiss</span>
        <span className="flex items-center gap-1.5"><Key>/btw</Key>ask another</span>
        <span className="flex-1" />
        <span>Not added to the Task</span>
      </PanelFoot>}
  </div>;
  if (sheet) return <BottomSheet id={id} open={open} onClose={onClose} onClosed={onClosed} label="Ask aside" initialFocus={heading} finalFocus={finalFocus} className="duration-160" backdropClassName="duration-160">
    {body(<BaseDialog.Close render={<Button size="icon-sm" aria-label="Close" className="text-muted" />}><X /></BaseDialog.Close>)}
  </BottomSheet>;
  return <BasePopover.Root open={open} modal onOpenChange={(o) => !o && onClose()} onOpenChangeComplete={(o) => !o && onClosed()}>
    <BasePopover.Portal>
      <BasePopover.Backdrop className={`${backdropClass} duration-160`} />
      <BasePopover.Positioner anchor={anchor} side="top" align="start" sideOffset={8} collisionPadding={12} className="z-50 outline-hidden">
        <BasePopover.Popup id={id} data-popup="" aria-label="Ask aside" aria-modal="true" initialFocus={heading} finalFocus={finalFocus} className="flex max-h-[min(70vh,var(--available-height))] w-[min(640px,var(--anchor-width))] max-w-(--available-width) origin-(--transform-origin) flex-col overflow-hidden rounded-lg bg-raised text-body shadow-modal outline-hidden transition-[opacity,scale] duration-160 ease-app data-starting-style:scale-[0.98] data-starting-style:opacity-0 data-ending-style:scale-[0.98] data-ending-style:opacity-0">
          {body(<BasePopover.Close render={<Button size="icon-sm" aria-label="Close" className="text-muted" />}><X /></BasePopover.Close>)}
        </BasePopover.Popup>
      </BasePopover.Positioner>
    </BasePopover.Portal>
  </BasePopover.Root>;
}
