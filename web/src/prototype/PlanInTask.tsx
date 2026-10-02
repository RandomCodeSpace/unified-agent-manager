/**
 * THROWAWAY PROTOTYPE (branch prototype/planner-in-task, never merged): the Project's plan seen
 * from inside a Task, in three variations, so the owner can compare them. Mock mode only.
 *
 * Run (one command, from the repo root):
 *   cd web && npx vite --host 127.0.0.1 --port 8440 --strictPort
 * then open
 *   http://127.0.0.1:8440/?mock&plan=1#task=t21   1 = Plan side panel, 2 = story strip, 3 = stories rail
 * The floating bar at the bottom switches variation. Useful Tasks: t21 (works on #40, card chips in
 * its transcript), t20 (no card yet), t22 (a Project without a plan). `&open=1` opens the surface,
 * `&focus=41` opens card #41 in it, `&story=38` picks a story (strip and rail).
 *
 * Dependencies follow the layered rule: an epic waits for epics, a story for stories in the same
 * epic, a subtask for subtasks in the same story; a card also waits for what its story and epic wait for.
 */
import { Check, ChevronDown, ChevronRight, CircleAlert, Link2, ListTree, Lock, Pencil, Plus, X } from 'lucide-react';
import { useContext, useEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from 'react';
import { api, describeError, taskName, type BoardRequest, type Card, type Project, type SessionSummary } from '../api';
import { cardPath, childIndex, deriveBoard } from '../lib/board';
import { cn } from '../lib/cn';
import { Markdown, useMedia } from '../components/common';
import { PlannerContext, usePlannerTasks } from '../components/planner/context';
import { KindIcon, StatusGlyph } from '../components/planner/parts';
import { PanelHeader, SidePanel } from '../components/Subagents';
import { Button } from '../components/ui/button';
import { Chip } from '../components/ui/chip';
import { Input } from '../components/ui/input';
import { Popover } from '../components/ui/popover';
import { Tip } from '../components/ui/tooltip';

/* ---------- The prototype's state: one tiny store, read by the Task and the transcript chips ---------- */

export type Variant = 0 | 1 | 2 | 3;
interface ProtoState {
  variant: Variant;
  /** The surface is open (panel, drawer, rail popover). */
  open: boolean;
  /** A card to show opened (`cp1-41` or a `#seq` from the URL). */
  focus: string | null;
  /** The story or epic the drawer / rail popover shows, when the owner picked one. */
  story: string | null;
  /** Task id → card id: Tasks attached to a card in this prototype (the service has no such call yet). */
  attach: Record<string, string>;
}

const params = new URLSearchParams(window.location.search);
const asVariant = (v: string | null): Variant => (v === '1' || v === '2' || v === '3' ? (Number(v) as Variant) : 0);
let state: ProtoState = {
  variant: import.meta.env.DEV && params.has('mock') ? asVariant(params.get('plan')) : 0,
  open: params.get('open') === '1' || params.has('focus'),
  focus: params.get('focus') ? `#${params.get('focus')}` : null,
  story: params.get('story') ? `#${params.get('story')}` : null,
  attach: {},
};
const listeners = new Set<() => void>();
const subscribe = (l: () => void) => {
  listeners.add(l);
  return () => listeners.delete(l);
};
export const proto = {
  get: () => state,
  set(patch: Partial<ProtoState>) {
    state = { ...state, ...patch };
    for (const l of listeners) l();
  },
  variant(v: Variant) {
    const url = new URL(window.location.href);
    url.searchParams.set('plan', String(v));
    url.searchParams.delete('focus');
    url.searchParams.delete('open');
    url.searchParams.delete('story');
    window.history.replaceState(window.history.state, '', url);
    proto.set({ variant: v, open: false, focus: null, story: null });
  },
};
export const useProto = () => useSyncExternalStore(subscribe, proto.get);

/** For a transcript card chip: open the card in this Task's plan surface, or null outside the prototype. */
export function usePlanProtoOpen(): ((id: string) => void) | null {
  const { variant } = useProto();
  return variant ? (id: string) => proto.set({ open: true, focus: id, story: null }) : null;
}

/** One side panel at a time (variation 1): the Plan panel closes the others, and any other closes it. */
export function usePlanSlot(otherOpen: boolean, closeOthers: () => void) {
  const { variant, open } = useProto();
  const planOpen = variant === 1 && open;
  const prev = useRef({ planOpen, otherOpen });
  useEffect(() => {
    const was = prev.current;
    prev.current = { planOpen, otherOpen };
    if (planOpen && !was.planOpen && otherOpen) closeOthers();
    else if (otherOpen && !was.otherOpen && planOpen) proto.set({ open: false });
  }, [planOpen, otherOpen, closeOthers]);
}

/* ---------- The plan as one Task sees it ---------- */

const STATUS_WORD: Record<Card['status'], string> = { planned: 'Not started', todo: 'Ready to start', doing: 'In progress', done: 'Done', cancelled: 'Dropped' };
const KIND_WORD: Record<Card['kind'], string> = { epic: 'Epic', story: 'Story', subtask: 'Subtask' };
const isOpen = (c: Card) => c.status !== 'done' && c.status !== 'cancelled';

interface Wait {
  card: Card;
  /** Inherited: what this card's story or epic waits for. */
  via?: 'story' | 'epic';
}

interface Plan {
  status: 'off' | 'loading' | 'none' | 'ready';
  project: Project | undefined;
  session: SessionSummary;
  cards: Card[];
  byId: Map<string, Card>;
  index: Map<string, Card[]>;
  mine?: Card;
  story?: Card;
  epic?: Card;
  focus?: Card;
  find: (ref: string | null | undefined) => Card | undefined;
  children: (id: string) => Card[];
  waitsFor: (c: Card) => Card[];
  neededBy: (c: Card) => Card[];
  /** Own-level blockers, then the story's and the epic's, still open. */
  waiting: (c: Card) => Wait[];
  storyOf: (c: Card) => Card | undefined;
  epicOf: (c: Card) => Card | undefined;
  /** Epics with their stories, in plan order; stories outside an epic last. */
  groups: { epic?: Card; stories: Card[] }[];
  holder: (c: Card) => string;
}

function usePlan(session: SessionSummary, project: Project | undefined): Plan {
  const ctx = useContext(PlannerContext);
  const { attach, focus } = useProto();
  const { sessions } = usePlannerTasks();
  const board = project && !project.no_git ? ctx?.boards[project.id] : undefined;
  const raw = board?.data?.cards;
  return useMemo(() => {
    const cards = deriveBoard(raw ?? []).filter((c) => c.status !== 'cancelled');
    const byId = new Map(cards.map((c) => [c.id, c]));
    const index = childIndex(cards);
    const find = (ref: string | null | undefined) => {
      if (!ref) return undefined;
      return ref.startsWith('#') ? cards.find((c) => c.seq === Number(ref.slice(1))) : byId.get(ref);
    };
    const children = (id: string) => index.get(id) ?? [];
    const waitsFor = (c: Card) => c.blocked_by.map((id) => byId.get(id)).filter((b): b is Card => !!b);
    const neededBy = (c: Card) => c.blocks.map((id) => byId.get(id)).filter((b): b is Card => !!b);
    const path = (c: Card) => cardPath(c, byId);
    const storyOf = (c: Card) => path(c).find((x) => x.kind === 'story');
    const epicOf = (c: Card) => path(c).find((x) => x.kind === 'epic');
    const waiting = (c: Card): Wait[] => {
      const out: Wait[] = waitsFor(c).filter(isOpen).map((card) => ({ card }));
      for (const a of path(c).slice(0, -1).reverse()) for (const b of waitsFor(a).filter(isOpen)) out.push({ card: b, via: a.kind === 'epic' ? 'epic' : 'story' });
      return out;
    };
    const mine = find(attach[session.id]) ?? cards.find((c) => c.held_by === session.id);
    const roots = children('');
    const groups: Plan['groups'] = roots.filter((c) => c.kind === 'epic').map((epic) => ({ epic, stories: children(epic.id).filter((c) => c.kind === 'story') }));
    const loose = roots.filter((c) => c.kind === 'story');
    if (loose.length) groups.push({ stories: loose });
    const holder = (c: Card) => {
      if (!c.held_by) return '';
      if (c.held_by === session.id) return 'this task';
      const t = sessions.find((s) => s.id === c.held_by);
      return t ? taskName(t) || 'another task' : 'another task';
    };
    let status: Plan['status'] = 'ready';
    if (!project || project.no_git || !ctx?.enabled) status = 'off';
    else if (!board?.data) status = 'loading';
    else if (!cards.length) status = 'none';
    return { status, project, session, cards, byId, index, mine, story: mine && storyOf(mine), epic: mine && epicOf(mine), focus: find(focus), find, children, waitsFor, neededBy, waiting, storyOf, epicOf, groups, holder };
  }, [raw, board?.data, project, ctx?.enabled, attach, focus, session, sessions]);
}

/* ---------- Small parts ---------- */

function Progress({ card, className }: Readonly<{ card: Card; className?: string }>) {
  const p = card.progress ?? { done: 0, total: 0, proposed: 0 };
  const share = p.total ? p.done / p.total : 0;
  return (
    <span className={cn('flex min-w-0 items-center gap-1.5', className)} title={`${p.done} of ${p.total} done${p.proposed ? `, ${p.proposed} proposed` : ''}`}>
      <span aria-hidden="true" className="relative h-1 w-full min-w-6 overflow-hidden rounded-full bg-sunken">
        <span className="absolute inset-0 origin-left rounded-full bg-success" style={{ transform: `scaleX(${share})` }} />
      </span>
      <span className="shrink-0 text-meta text-muted tabular-nums">
        {p.done}/{p.total}
      </span>
    </span>
  );
}

function ProposedChip() {
  return <Chip className="border border-dashed border-hairline-strong" title="Proposed by an agent. It can be edited and linked now; it is confirmed when work on it starts.">Proposed</Chip>;
}

function Seq({ card }: Readonly<{ card: Card }>) {
  return <span className="shrink-0 text-caption text-muted tabular-nums">#{card.seq}</span>;
}

const viaText = (w: Wait) => (w.via ? ` (through its ${w.via})` : '');

/** "Waits for #18 Release automation (through its epic)": how waiting reads, own level first, then inherited. */
function WaitingLine({ plan, card, className }: Readonly<{ plan: Plan; card: Card; className?: string }>) {
  const waits = plan.waiting(card);
  if (!waits.length) return null;
  return (
    <p className={cn('flex min-w-0 items-start gap-1.5 text-caption text-warning', className)}>
      <Lock aria-hidden="true" className="mt-0.5 size-3 shrink-0" />
      <span className="min-w-0">
        Waits for{' '}
        {waits.map((w, i) => (
          <span key={`${w.card.id}:${w.via ?? ''}`}>
            {i > 0 && (i === waits.length - 1 ? ' and ' : ', ')}
            <FocusLink card={w.card} />
            <span className="text-muted">{viaText(w)}</span>
          </span>
        ))}
      </span>
    </p>
  );
}

function FocusLink({ card }: Readonly<{ card: Card }>) {
  return (
    <button type="button" className="text-left underline decoration-hairline-strong underline-offset-2 hover:text-ink" onClick={(e) => { e.stopPropagation(); proto.set({ open: true, focus: card.id, story: null }); }}>
      #{card.seq} {card.title}
    </button>
  );
}

/**
 * One card as a row: the status glyph, #seq and title; this Task's card is marked, proposals are
 * dashed, a card held by another Task names it, and a waiting card says after what.
 */
function CardRow({ plan, card, open, onClick, chevron, className, id, waitChip = true, compact = false }: Readonly<{ plan: Plan; card: Card; open: boolean; onClick: () => void; chevron?: boolean; className?: string; id?: string; /** Containers in the outline say it on their own line. */ waitChip?: boolean; /** A narrow list: a lock for waiting, the dashed edge alone for a proposal. */ compact?: boolean }>) {
  const mine = card.id === plan.mine?.id;
  const holder = plan.holder(card);
  const own = plan.waitsFor(card).filter(isOpen);
  const container = card.kind !== 'subtask';
  return (
    <button
      type="button"
      id={id}
      aria-expanded={open}
      onClick={onClick}
      className={cn(
        'flex min-h-8 w-full min-w-0 items-center gap-1.5 rounded-sm px-1.5 text-left text-ui text-body transition-colors hover:bg-tint-hover pointer-coarse:min-h-11',
        !card.confirmed && 'border border-dashed border-hairline-strong',
        mine && 'bg-tint-selected text-ink shadow-[inset_2px_0_0_var(--color-accent)] hover:bg-tint-selected',
        card.kind === 'epic' && 'font-semibold text-ink',
        className,
      )}
    >
      {chevron && (open ? <ChevronDown aria-hidden="true" className="size-3.5 shrink-0 text-faint" /> : <ChevronRight aria-hidden="true" className="size-3.5 shrink-0 text-faint" />)}
      {container ? <KindIcon kind={card.kind} /> : <StatusGlyph status={card.status} />}
      <Seq card={card} />
      <span className="min-w-0 flex-1 truncate">{card.title}</span>
      {own.length > 0 && compact && <Lock aria-label={`Waits for ${own.map((b) => `#${b.seq} ${b.title}`).join(', ')}`} className="size-3 shrink-0 text-warning" />}
      {own.length > 0 && waitChip && !compact && (
        <span className="flex shrink-0 items-center gap-0.5 text-meta text-warning" title={`Waits for ${own.map((b) => `#${b.seq} ${b.title}`).join(', ')}`}>
          <Lock aria-hidden="true" className="size-3" />
          after {own.map((b) => `#${b.seq}`).join(', ')}
        </span>
      )}
      {!card.confirmed && !compact && <ProposedChip />}
      {mine && <Chip tone="accent" className="font-medium">This task</Chip>}
      {!mine && holder && <Chip className="max-w-28 max-sm:hidden" title={`Worked on in ${holder}`}><span className="truncate">{holder}</span></Chip>}
      {container && <Progress card={card} className="w-16 shrink-0" />}
    </button>
  );
}

/* ---------- A card's details, opened in place ---------- */

const REQUEST_WORD: Record<BoardRequest['kind'], string> = {
  done: 'The agent says this is done',
  cancel: 'The agent suggests dropping this',
  blocked: 'The agent is stuck on this',
  split: 'The agent suggests splitting this',
  change: 'The agent suggests a change',
};

function CardDetails({ plan, card, className }: Readonly<{ plan: Plan; card: Card; className?: string }>) {
  const ctx = useContext(PlannerContext);
  const requests = (plan.project ? ctx?.boards[plan.project.id]?.data?.requests : undefined)?.filter((r) => r.card_id === card.id) ?? [];
  const [editing, setEditing] = useState(false);
  const [title, setTitle] = useState(card.title);
  const [done, setDone] = useState(card.win_condition);
  const [error, setError] = useState('');
  const run = (p: Promise<unknown>) => p.then(() => setError(''), (e: unknown) => setError(describeError(e)));
  const holder = plan.holder(card);
  const inherited = plan.waiting(card).filter((w) => w.via);
  return (
    <div className={cn('flex flex-col gap-2.5 rounded-md bg-raised px-3 py-2.5 text-ui shadow-raised', className)}>
      <p className="flex flex-wrap items-center gap-x-2 gap-y-1 text-caption text-muted">
        <span>{KIND_WORD[card.kind]}</span>
        <span aria-hidden="true">·</span>
        <span className="flex items-center gap-1"><StatusGlyph status={card.status} />{STATUS_WORD[card.status]}</span>
        {holder && <><span aria-hidden="true">·</span><span>{holder === 'this task' ? 'This task works on it' : `Worked on in ${holder}`}</span></>}
      </p>
      {!card.confirmed && <p className="rounded-sm border border-dashed border-hairline-strong px-2 py-1.5 text-caption text-body">Proposed by an agent. You can edit it and link it now; it is confirmed when work on it starts.</p>}
      {editing ? (
        <form className="flex flex-col gap-2" onSubmit={(e) => { e.preventDefault(); void run(api.planner.edit(card.id, { title, win_condition: done })).then(() => setEditing(false)); }}>
          <label className="flex flex-col gap-1 text-caption text-muted">Title<Input size="md" value={title} onChange={(e) => setTitle(e.target.value)} /></label>
          <label className="flex flex-col gap-1 text-caption text-muted">Done when<Input size="md" value={done} onChange={(e) => setDone(e.target.value)} /></label>
          <span className="flex gap-1.5"><Button type="submit" variant="primary" size="sm">Save</Button><Button size="sm" onClick={() => setEditing(false)}>Cancel</Button></span>
        </form>
      ) : (
        card.win_condition && (
          <div>
            <p className="text-eyebrow text-muted uppercase">Done when</p>
            <div className="text-body [&_p]:my-0"><Markdown text={card.win_condition} /></div>
          </div>
        )
      )}
      {card.desc && <div className="text-body [&_p]:my-0"><Markdown text={card.desc} /></div>}
      {card.checklist.length > 0 && (
        <ul className="flex flex-col gap-0.5">
          {card.checklist.map((item, i) => (
            <li key={item.text}>
              <button
                type="button"
                className="flex min-h-7 w-full items-center gap-2 rounded-sm px-1 text-left hover:bg-tint-hover pointer-coarse:min-h-11"
                onClick={() => void run(api.planner.edit(card.id, { checklist: card.checklist.map((x, j) => (j === i ? { ...x, done: !x.done } : x)) }))}
              >
                <span className={cn('flex size-4 shrink-0 items-center justify-center rounded-xs shadow-well', item.done && 'bg-success text-on-accent')}>{item.done && <Check aria-hidden="true" className="size-3" />}</span>
                <span className={cn(item.done && 'text-muted line-through')}>{item.text}</span>
              </button>
            </li>
          ))}
        </ul>
      )}
      <Dependencies plan={plan} card={card} onError={setError} />
      {inherited.length > 0 && (
        <p className="text-caption text-muted">
          Also waits for {inherited.map((w, i) => <span key={w.card.id}>{i > 0 && ', '}<FocusLink card={w.card} />{viaText(w)}</span>)}.
        </p>
      )}
      {requests.map((r) => (
        <div key={r.id} className="flex flex-col gap-1.5 rounded-sm bg-attention-wash px-2 py-1.5 text-caption text-attention">
          <p className="flex items-center gap-1 font-medium"><CircleAlert aria-hidden="true" className="size-3.5" />{REQUEST_WORD[r.kind]}</p>
          <p className="text-body">{r.comment}</p>
          <span className="flex gap-1.5">
            <Button size="sm" variant="secondary" onClick={() => void run(api.planner.accept(r.id, ''))}>Accept</Button>
            <Button size="sm" onClick={() => void run(api.planner.reject(r.id, 'Declined from the task'))}>Decline</Button>
          </span>
        </div>
      ))}
      {!editing && (
        <span className="flex flex-wrap gap-1.5">
          <Button size="sm" variant="secondary" onClick={() => { setTitle(card.title); setDone(card.win_condition); setEditing(true); }}><Pencil />Edit</Button>
          {!card.confirmed && <Button size="sm" onClick={() => void run(api.planner.dismiss(card.id))}><X />Discard</Button>}
        </span>
      )}
      {error && <p role="alert" className="text-caption text-error">{error}</p>}
    </div>
  );
}

/** The card's own-level dependencies only: epics with epics, stories within their epic, subtasks within their story. */
function Dependencies({ plan, card, onError }: Readonly<{ plan: Plan; card: Card; onError: (e: string) => void }>) {
  const [adding, setAdding] = useState(false);
  const waits = plan.waitsFor(card);
  const needed = plan.neededBy(card);
  const level = card.kind === 'epic' ? 'epics' : card.kind === 'story' ? 'stories in this epic' : 'subtasks in this story';
  const siblings = plan.children(card.parent_id ?? '').filter((c) => c.kind === card.kind && c.id !== card.id && !card.blocked_by.includes(c.id) && !card.blocks.includes(c.id));
  const run = (p: Promise<unknown>) => p.then(() => onError(''), (e: unknown) => onError(describeError(e)));
  const row = (c: Card, unlink?: () => void) => (
    <li key={c.id} className="flex min-w-0 items-center gap-1.5">
      <StatusGlyph status={c.status} />
      <span className="min-w-0 flex-1 truncate"><FocusLink card={c} /></span>
      {!c.confirmed && <ProposedChip />}
      {unlink && <Tip label="Remove this dependency"><Button size="icon-sm" aria-label={`Stop waiting for #${c.seq}`} className="text-muted" onClick={unlink}><X /></Button></Tip>}
    </li>
  );
  return (
    <div className="flex flex-col gap-1">
      <p className="flex items-center gap-1 text-eyebrow text-muted uppercase"><Link2 aria-hidden="true" className="size-3" />Depends on ({level})</p>
      {waits.length ? <ul className="flex flex-col gap-0.5">{waits.map((c) => row(c, () => void run(api.planner.unlink(c.id, card.id))))}</ul> : <p className="text-caption text-muted">None at this level.</p>}
      {needed.length > 0 && (
        <>
          <p className="mt-1 text-eyebrow text-muted uppercase">Needed by</p>
          <ul className="flex flex-col gap-0.5">{needed.map((c) => row(c))}</ul>
        </>
      )}
      {adding ? (
        <div className="flex flex-col gap-0.5 rounded-sm bg-tint-well p-1">
          <p className="px-1 text-caption text-muted">Waits for which of the {level}?</p>
          {siblings.length === 0 && <p className="px-1 text-caption text-muted">No other {level}.</p>}
          {siblings.map((c) => (
            <button key={c.id} type="button" className="flex min-h-7 items-center gap-1.5 rounded-sm px-1 text-left text-ui hover:bg-tint-hover pointer-coarse:min-h-11" onClick={() => void run(api.planner.link(c.id, card.id)).then(() => setAdding(false))}>
              <StatusGlyph status={c.status} /><Seq card={c} /><span className="min-w-0 flex-1 truncate">{c.title}</span>{!c.confirmed && <ProposedChip />}
            </button>
          ))}
          <Button size="sm" className="self-start" onClick={() => setAdding(false)}>Cancel</Button>
        </div>
      ) : (
        <Button size="sm" className="self-start text-muted" onClick={() => setAdding(true)}><Plus />Add a dependency</Button>
      )}
    </div>
  );
}

/* ---------- No card, no plan ---------- */

/** A Task with no card: add it to a story (a new subtask named after the Task), or start a new story. */
function AttachBox({ plan, compact = false }: Readonly<{ plan: Plan; compact?: boolean }>) {
  const [mode, setMode] = useState<'idle' | 'pick' | 'new'>('idle');
  const name = taskName(plan.session) || 'This task';
  const [title, setTitle] = useState(name);
  const [epic, setEpic] = useState(plan.groups.find((g) => g.epic)?.epic?.id ?? '');
  const [error, setError] = useState('');
  const projectId = plan.project?.id ?? '';
  const attachUnder = async (storyId: string) => {
    const card = await api.planner.create({ project_id: projectId, kind: 'subtask', parent_id: storyId, title: name });
    proto.set({ attach: { ...proto.get().attach, [plan.session.id]: card.id }, focus: card.id });
  };
  const run = (p: Promise<unknown>) => p.then(() => setMode('idle'), (e: unknown) => setError(describeError(e)));
  return (
    <div className="flex flex-col gap-2 rounded-md bg-tint-well px-3 py-2.5 text-ui">
      <p className="text-body">This task is not part of a story yet.{!compact && ' Add it to one so its progress shows in the plan.'}</p>
      {mode === 'idle' && (
        <span className="flex flex-wrap gap-1.5">
          <Button size="sm" variant="secondary" onClick={() => setMode('pick')}><Plus />Add to a story</Button>
          <Button size="sm" onClick={() => setMode('new')}>New story…</Button>
        </span>
      )}
      {mode === 'pick' && (
        <div className="flex max-h-72 flex-col gap-0.5 overflow-y-auto">
          {plan.groups.map((g) => (
            <div key={g.epic?.id ?? 'loose'} className="flex flex-col gap-0.5">
              <p className="px-1 pt-1 text-eyebrow text-muted uppercase">{g.epic?.title ?? 'Not in an epic'}</p>
              {g.stories.map((s) => (
                <button key={s.id} type="button" className="flex min-h-8 items-center gap-1.5 rounded-sm px-1 text-left hover:bg-tint-hover pointer-coarse:min-h-11" onClick={() => void run(attachUnder(s.id))}>
                  <KindIcon kind="story" /><Seq card={s} /><span className="min-w-0 flex-1 truncate">{s.title}</span>{!s.confirmed && <ProposedChip />}
                </button>
              ))}
            </div>
          ))}
          <Button size="sm" className="self-start" onClick={() => setMode('idle')}>Cancel</Button>
        </div>
      )}
      {mode === 'new' && (
        <form
          className="flex flex-col gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            void run(api.planner.create({ project_id: projectId, kind: 'story', parent_id: epic || null, title }).then((s) => attachUnder(s.id)));
          }}
        >
          <label className="flex flex-col gap-1 text-caption text-muted">Story title<Input size="md" value={title} onChange={(e) => setTitle(e.target.value)} /></label>
          <p className="text-caption text-muted">In epic</p>
          <div className="flex flex-wrap gap-1">
            {plan.groups.filter((g) => g.epic).map((g) => (
              <Button key={g.epic!.id} size="sm" variant={epic === g.epic!.id ? 'secondary' : 'ghost'} aria-pressed={epic === g.epic!.id} onClick={() => setEpic(g.epic!.id)}>{g.epic!.title}</Button>
            ))}
            <Button size="sm" variant={epic === '' ? 'secondary' : 'ghost'} aria-pressed={epic === ''} onClick={() => setEpic('')}>No epic</Button>
          </div>
          <span className="flex gap-1.5"><Button type="submit" variant="primary" size="sm">Create and add</Button><Button size="sm" onClick={() => setMode('idle')}>Cancel</Button></span>
        </form>
      )}
      {error && <p role="alert" className="text-caption text-error">{error}</p>}
    </div>
  );
}

function NoPlan({ plan }: Readonly<{ plan: Plan }>) {
  return (
    <div className="flex flex-col gap-1 px-1 py-6 text-center">
      <p className="text-ui text-body">No plan for {plan.project?.name ?? 'this project'} yet.</p>
      <p className="text-caption text-muted">When you or an agent plan the work as epics, stories and subtasks, they show here.</p>
    </div>
  );
}

/** Where this Task sits: Epic › Story, its card, and what it waits for. */
function ThisTask({ plan }: Readonly<{ plan: Plan }>) {
  const { mine, story, epic } = plan;
  if (!mine) return <AttachBox plan={plan} />;
  return (
    <div className="flex flex-col gap-1 rounded-md bg-tint-selected px-3 py-2">
      <p className="text-eyebrow text-muted uppercase">This task works on</p>
      <p className="min-w-0 truncate text-caption text-muted">{[epic, story].filter(Boolean).map((c) => c!.title).join(' › ')}</p>
      <p className="flex min-w-0 items-center gap-1.5 text-ui text-ink"><StatusGlyph status={mine.status} /><Seq card={mine} /><span className="truncate font-medium">{mine.title}</span></p>
      <WaitingLine plan={plan} card={mine} />
    </div>
  );
}

/* ---------- Variation 1: the Plan side panel ---------- */

/** The header toggle, beside Changes and Files. */
export function PlanToggle({ open, onToggle }: Readonly<{ open: boolean; onToggle: () => void }>) {
  return (
    <Tip label="The project's plan">
      <Button id="plan-link" size="md" aria-pressed={open} aria-label="Plan" className="px-2 text-muted" onClick={onToggle}>
        <ListTree />
        <span className="max-xl:hidden">Plan</span>
      </Button>
    </Tip>
  );
}

export function PlanPanel({ session, project, inline, open, onClose, onClosed }: Readonly<{ session: SessionSummary; project: Project | undefined; inline: boolean; open: boolean; onClose: () => void; onClosed: () => void }>) {
  const plan = usePlan(session, project);
  return (
    <SidePanel id="plan" inline={inline} open={open} onClose={onClose} onClosed={onClosed} label="Plan" defaultWidth={460}>
      <PanelHeader>
        <ListTree aria-hidden="true" className="size-4 text-muted" />
        <h2 className="text-title text-ink">Plan</h2>
        <span className="min-w-0 flex-1 truncate text-caption text-muted">{project?.name}</span>
        <Button size="icon-md" aria-label="Close plan" className="text-muted" onClick={onClose}><X /></Button>
      </PanelHeader>
      <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto overscroll-contain px-3 pt-1 pb-6">
        {plan.status === 'loading' && <p className="px-1 text-caption text-muted">Loading the plan…</p>}
        {plan.status === 'none' && <NoPlan plan={plan} />}
        {plan.status === 'ready' && (
          <>
            <ThisTask plan={plan} />
            <Outline plan={plan} />
          </>
        )}
      </div>
    </SidePanel>
  );
}

/** Epics › stories › subtasks. Chevrons fold; a click on a card opens its details under it. */
function Outline({ plan }: Readonly<{ plan: Plan }>) {
  const opened = (c?: Card) => (c ? cardPath(c, plan.byId).map((x) => x.id) : []);
  const [folds, setFolds] = useState<Set<string>>(() => new Set([...opened(plan.mine), ...opened(plan.focus)]));
  const [details, setDetails] = useState<string | null>(() => plan.focus?.id ?? null);
  const focusId = plan.focus?.id;
  const [seenFocus, setSeenFocus] = useState(focusId);
  if (focusId !== seenFocus) {
    setSeenFocus(focusId);
    if (plan.focus) {
      setFolds(new Set([...folds, ...opened(plan.focus)]));
      setDetails(plan.focus.id);
    }
  }
  useEffect(() => {
    if (focusId) document.getElementById(`plan-row-${focusId}`)?.scrollIntoView({ block: 'nearest' });
  }, [focusId]);
  const toggleFold = (id: string) => setFolds((s) => { const n = new Set(s); if (n.has(id)) n.delete(id); else n.add(id); return n; });
  const node = (c: Card, depth: number): ReactNode => {
    const kids = plan.children(c.id);
    const folded = !folds.has(c.id);
    const container = c.kind !== 'subtask';
    return (
      <li key={c.id} className="flex flex-col gap-0.5">
        <div className="flex min-w-0 items-center">
          {container && (
            <Button size="icon" aria-label={folded ? `Show ${c.title}` : `Hide ${c.title}`} aria-expanded={!folded} className="text-faint" onClick={() => toggleFold(c.id)}>
              {folded ? <ChevronRight /> : <ChevronDown />}
            </Button>
          )}
          <CardRow plan={plan} card={c} open={details === c.id} id={`plan-row-${c.id}`} waitChip={!container} className={cn(!container && 'ml-7')} onClick={() => setDetails(details === c.id ? null : c.id)} />
        </div>
        {container && plan.waitsFor(c).some(isOpen) && <p className="ml-9 flex items-center gap-1 text-meta text-warning"><Lock aria-hidden="true" className="size-3" />Waits for {plan.waitsFor(c).filter(isOpen).map((b) => b.title).join(', ')}</p>}
        {details === c.id && <CardDetails plan={plan} card={c} className="mb-1 ml-7" />}
        {container && !folded && kids.length > 0 && <ul className={cn('flex flex-col gap-0.5', depth === 0 ? 'ml-3' : 'ml-4')}>{kids.map((k) => node(k, depth + 1))}</ul>}
      </li>
    );
  };
  return <ul className="flex flex-col gap-1">{plan.children('').map((c) => node(c, 0))}</ul>;
}

/* ---------- Shared by variations 2 and 3: one epic or story, with its children ---------- */

/** An epic (its stories) or a story (its subtasks, each opening in place); a subtask shows its story. */
function FocusDetail({ plan, target, onPick }: Readonly<{ plan: Plan; target: Card; onPick: (id: string) => void }>) {
  const subject = target.kind === 'subtask' ? (plan.storyOf(target) ?? target) : target;
  const [details, setDetails] = useState<string | null>(target.kind === 'subtask' ? target.id : null);
  const [seen, setSeen] = useState(target.id);
  if (seen !== target.id) {
    setSeen(target.id);
    setDetails(target.kind === 'subtask' ? target.id : null);
  }
  const [headOpen, setHeadOpen] = useState(false);
  const epic = subject.kind === 'story' ? plan.epicOf(subject) : undefined;
  const kids = plan.children(subject.id);
  const next = kids.find((k) => k.id !== plan.mine?.id && k.kind === 'subtask' && (k.status === 'planned' || k.status === 'todo'));
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-col gap-1">
        {epic && (
          <button type="button" className="flex min-h-6 min-w-0 items-center gap-1 self-start text-caption text-muted hover:text-ink pointer-coarse:min-h-11" onClick={() => onPick(epic.id)}>
            <KindIcon kind="epic" /><span className="truncate">{epic.title}</span>
          </button>
        )}
        <div className="flex min-w-0 items-center gap-1.5">
          <KindIcon kind={subject.kind} />
          <Seq card={subject} />
          <h3 className="min-w-0 flex-1 truncate text-title text-ink">{subject.title}</h3>
          {!subject.confirmed && <ProposedChip />}
        </div>
        <Progress card={subject} className="w-full" />
        <p className="text-caption text-muted">
          {subject.progress?.done ?? 0} of {subject.progress?.total ?? 0} done
          {subject.progress?.proposed ? `, ${subject.progress.proposed} proposed` : ''}
          {next && <> · next: <FocusLink card={next} /></>}
        </p>
        <WaitingLine plan={plan} card={subject} />
        <Button size="sm" className="self-start text-muted" aria-expanded={headOpen} onClick={() => setHeadOpen(!headOpen)}>
          {headOpen ? <ChevronDown /> : <ChevronRight />}
          {KIND_WORD[subject.kind]} details and dependencies
        </Button>
        {headOpen && <CardDetails plan={plan} card={subject} />}
      </div>
      <ul className="flex flex-col gap-0.5">
        {kids.map((k) => (
          <li key={k.id} className="flex flex-col gap-0.5">
            <CardRow plan={plan} card={k} open={details === k.id} chevron={k.kind !== 'subtask'} onClick={() => (k.kind === 'subtask' ? setDetails(details === k.id ? null : k.id) : onPick(k.id))} />
            {details === k.id && <CardDetails plan={plan} card={k} className="mb-1 ml-2" />}
          </li>
        ))}
        {kids.length === 0 && <li className="px-1.5 text-caption text-muted">Nothing under it yet.</li>}
      </ul>
    </div>
  );
}

/** The Project's stories by epic: a list to browse (the drawer's left column). */
function StoryList({ plan, current, onPick }: Readonly<{ plan: Plan; current?: string; onPick: (id: string) => void }>) {
  return (
    <ul className="flex flex-col gap-2">
      {plan.groups.map((g) => (
        <li key={g.epic?.id ?? 'loose'} className="flex flex-col gap-0.5">
          {g.epic ? (
            <button type="button" className={cn('flex min-h-7 min-w-0 items-center gap-1.5 rounded-sm px-1.5 text-left hover:bg-tint-hover pointer-coarse:min-h-11', current === g.epic.id && 'bg-tint-hover', !g.epic.confirmed && 'border border-dashed border-hairline-strong')} onClick={() => onPick(g.epic!.id)}>
              <span className="min-w-0 flex-1 truncate text-eyebrow text-muted uppercase">{g.epic.title}</span>
              {plan.waitsFor(g.epic).some(isOpen) && <Lock aria-label="Waits for another epic" className="size-3 shrink-0 text-warning" />}
            </button>
          ) : (
            <p className="px-1.5 text-eyebrow text-muted uppercase">Not in an epic</p>
          )}
          {g.stories.map((s) => (
            <CardRow key={s.id} plan={plan} card={s} compact open={current === s.id} className={cn(current === s.id && 'bg-tint-hover', s.id === plan.story?.id && 'shadow-[inset_2px_0_0_var(--color-accent)]')} onClick={() => onPick(s.id)} />
          ))}
        </li>
      ))}
    </ul>
  );
}

/* ---------- Variation 2: the story strip and its drawer ---------- */

export function StoryStrip({ session, project }: Readonly<{ session: SessionSummary; project: Project | undefined }>) {
  const plan = usePlan(session, project);
  const { open, story } = useProto();
  const phone = useMedia('(width < 30.0625rem)');
  const [listOnPhone, setListOnPhone] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const away = (e: PointerEvent) => { if (!box.current?.contains(e.target as Node) && !(e.target as Element).closest?.('[data-plan-chip]')) proto.set({ open: false }); };
    const esc = (e: KeyboardEvent) => { if (e.key === 'Escape') proto.set({ open: false }); };
    document.addEventListener('pointerdown', away);
    document.addEventListener('keydown', esc);
    return () => { document.removeEventListener('pointerdown', away); document.removeEventListener('keydown', esc); };
  }, [open]);
  if (plan.status === 'off' || plan.status === 'loading') return null;
  const shown = plan.find(story) ?? plan.focus ?? plan.story ?? plan.groups[0]?.stories[0];
  const pick = (id: string) => { proto.set({ story: id, focus: null }); setListOnPhone(false); };
  const { mine, epic } = plan;
  const sStory = plan.story;
  const next = sStory && plan.children(sStory.id).find((k) => k.id !== mine?.id && k.kind === 'subtask' && (k.status === 'planned' || k.status === 'todo'));
  const waits = mine ? plan.waiting(mine) : [];
  const storyCount = plan.groups.reduce((n, g) => n + g.stories.length, 0);
  let line: ReactNode;
  if (plan.status === 'none') line = <span className="text-muted">No plan for {project?.name} yet</span>;
  else if (mine && sStory) {
    line = (
      <>
        <span className="min-w-0 truncate text-muted max-sm:hidden">{epic?.title} ›</span>
        <span className="min-w-0 truncate font-medium text-ink">{sStory.title}</span>
        <span className="shrink-0 text-muted tabular-nums">· {sStory.progress?.done ?? 0}/{sStory.progress?.total ?? 0}<span className="max-sm:hidden"> done</span></span>
        <Chip tone="accent" className="shrink-0 max-sm:hidden">This task #{mine.seq}</Chip>
        {next && <span className="shrink-0 text-muted max-md:hidden">· next: #{next.seq}</span>}
        {waits.length > 0 && <span className="flex shrink-0 items-center gap-0.5 text-warning"><Lock aria-hidden="true" className="size-3" /><span className="max-sm:sr-only">waits for {waits[0].card.title}{waits.length > 1 ? ` +${waits.length - 1}` : ''}</span></span>}
      </>
    );
  } else line = <><span className="text-body">Not part of a story yet</span><span className="text-muted max-sm:hidden">· {storyCount} stories in {project?.name}</span></>;
  return (
    <div ref={box} className="relative z-20 shrink-0">
      <button
        type="button"
        aria-expanded={open}
        disabled={plan.status === 'none'}
        className="flex h-9 w-full min-w-0 items-center gap-1.5 border-b border-hairline bg-canvas px-3 text-left text-caption hover:bg-tint-hover disabled:hover:bg-canvas pointer-coarse:h-11 sm:px-4 md:px-6"
        onClick={() => proto.set({ open: !open })}
      >
        <ListTree aria-hidden="true" className="size-3.5 shrink-0 text-muted" />
        {line}
        <span className="flex-1" />
        {plan.status === 'ready' && <span className="flex shrink-0 items-center gap-0.5 text-muted">{open ? 'Hide' : mine ? 'Story' : 'Stories'}{open ? <ChevronDown aria-hidden="true" className="size-3.5 rotate-180" /> : <ChevronDown aria-hidden="true" className="size-3.5" />}</span>}
      </button>
      {open && plan.status === 'ready' && (
        <div className="absolute inset-x-0 top-full flex max-h-[min(72dvh,620px)] min-h-0 animate-fade-in overflow-hidden rounded-b-md bg-raised shadow-float">
          {(!phone || listOnPhone) && (
            <nav aria-label="Stories" className={cn('flex min-h-0 flex-col gap-2 overflow-y-auto overscroll-contain p-2', phone ? 'w-full' : 'w-80 shrink-0 border-r border-hairline')}>
              <p className="px-1.5 pt-1 text-caption text-muted">Stories in {project?.name}</p>
              <StoryList plan={plan} current={shown?.id} onPick={pick} />
            </nav>
          )}
          {(!phone || !listOnPhone) && (
            <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-y-auto overscroll-contain p-3">
              {phone && <Button size="sm" className="self-start text-muted" onClick={() => setListOnPhone(true)}><ChevronRight className="rotate-180" />All stories</Button>}
              {!mine && <AttachBox plan={plan} compact />}
              {shown && <FocusDetail plan={plan} target={shown} onPick={pick} />}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

/* ---------- Variation 3: the stories rail ---------- */

export function StoryRail({ session, project }: Readonly<{ session: SessionSummary; project: Project | undefined }>) {
  const plan = usePlan(session, project);
  const { open, story, focus } = useProto();
  const phone = useMedia('(width < 30.0625rem)');
  if (plan.status === 'off') return null;
  // The popover shows the picked card, else the focused one; it hangs off that card's story row (or its epic's heading).
  const target = open ? (plan.find(story) ?? plan.find(focus)) : undefined;
  const anchor = target && (target.kind === 'epic' ? target : (plan.storyOf(target) ?? target));
  const setOpen = (id: string | null) => proto.set(id ? { open: true, story: id, focus: null } : { open: false, story: null, focus: null });
  const pop = (card: Card, trigger: ReactNode) => (
    <Popover.Root key={card.id} open={anchor?.id === card.id} onOpenChange={(o) => { if (o) setOpen(card.id); else if (anchor?.id === card.id) setOpen(null); }}>
      {trigger}
      <Popover.Content side="left" align="start" sideOffset={8} initialFocus={false} className="max-h-[min(80dvh,640px)] w-[400px] max-w-[calc(100vw-72px)] overflow-y-auto p-3">
        {target && anchor?.id === card.id && <FocusDetail plan={plan} target={target} onPick={(id) => setOpen(id)} />}
      </Popover.Content>
    </Popover.Root>
  );
  // A narrow strip on a phone, and wherever there is no plan to list (the quiet empty state).
  if (phone || plan.status !== 'ready') {
    return (
      <aside aria-label="Stories" className="flex w-12 shrink-0 flex-col items-center gap-1 overflow-y-auto border-l border-hairline bg-rail py-1">
        <Tip label={plan.status === 'none' ? `No plan for ${project?.name} yet` : 'Stories'} side="left">
          <span className="flex size-11 items-center justify-center text-muted"><ListTree aria-hidden="true" className="size-4" /></span>
        </Tip>
        {plan.status === 'ready' && !plan.mine && <Popover.Root><Popover.Trigger render={<Button size="icon-md" aria-label="Add this task to a story" className="text-accent" />}><Plus /></Popover.Trigger><Popover.Content side="left" className="w-80 max-w-[calc(100vw-72px)]"><AttachBox plan={plan} compact /></Popover.Content></Popover.Root>}
        {plan.status === 'ready' && plan.groups.map((g) => (
          <div key={g.epic?.id ?? 'loose'} className="flex flex-col items-center gap-1 border-t border-hairline pt-1">
            {g.stories.map((s) => {
              const p = s.progress ?? { done: 0, total: 0, proposed: 0 };
              const mine = s.id === plan.story?.id;
              return pop(s, (
                <Popover.Trigger
                  render={
                    <button
                      type="button"
                      aria-label={`#${s.seq} ${s.title}, ${p.done} of ${p.total} done${mine ? ', this task’s story' : ''}`}
                      className={cn('relative flex size-11 flex-col items-center justify-center gap-0.5 rounded-sm text-meta text-muted tabular-nums hover:bg-tint-hover', mine && 'bg-tint-selected text-accent', !s.confirmed && 'border border-dashed border-hairline-strong')}
                    />
                  }
                >
                  <span>{p.done}/{p.total}</span>
                  <span aria-hidden="true" className="relative h-1 w-6 overflow-hidden rounded-full bg-sunken"><span className="absolute inset-0 origin-left bg-success" style={{ transform: `scaleX(${p.total ? p.done / p.total : 0})` }} /></span>
                  {plan.waiting(s).length > 0 && <Lock aria-hidden="true" className="absolute top-0.5 right-0.5 size-2.5 text-warning" />}
                </Popover.Trigger>
              ));
            })}
          </div>
        ))}
      </aside>
    );
  }
  return (
    <aside aria-label="Stories" className="flex w-64 shrink-0 flex-col border-l border-hairline bg-rail">
      <div className="flex h-header shrink-0 items-center gap-1.5 px-3">
        <ListTree aria-hidden="true" className="size-4 text-muted" />
        <h2 className="text-title text-ink">Stories</h2>
        <span className="min-w-0 truncate text-caption text-muted">{project?.name}</span>
      </div>
      <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto overscroll-contain px-2 pb-6">
        {plan.status === 'ready' && !plan.mine && <AttachBox plan={plan} compact />}
        {plan.status === 'ready' && plan.mine && (
          <div className="flex flex-col gap-0.5 px-1.5">
            <p className="text-eyebrow text-muted uppercase">This task</p>
            <p className="flex min-w-0 items-center gap-1 text-caption text-ink"><StatusGlyph status={plan.mine.status} /><Seq card={plan.mine} /><span className="truncate">{plan.mine.title}</span></p>
            <WaitingLine plan={plan} card={plan.mine} />
          </div>
        )}
        {plan.status === 'ready' && plan.groups.map((g) => (
          <section key={g.epic?.id ?? 'loose'} className="flex flex-col gap-1">
            {g.epic ? pop(g.epic, (
              <Popover.Trigger openOnHover delay={250} render={<button type="button" className={cn('flex min-h-7 min-w-0 flex-col items-start rounded-sm px-1.5 py-0.5 text-left hover:bg-tint-hover', !g.epic.confirmed && 'border border-dashed border-hairline-strong')} />}>
                <span className="flex w-full min-w-0 items-center gap-1"><span className="min-w-0 flex-1 truncate text-eyebrow text-muted uppercase">{g.epic.title}</span>{!g.epic.confirmed && <ProposedChip />}</span>
                {plan.waitsFor(g.epic).some(isOpen) && <span className="flex items-center gap-0.5 text-meta text-warning"><Lock aria-hidden="true" className="size-2.5" />after {plan.waitsFor(g.epic).filter(isOpen).map((b) => b.title).join(', ')}</span>}
              </Popover.Trigger>
            )) : <p className="px-1.5 text-eyebrow text-muted uppercase">Not in an epic</p>}
            {g.stories.map((s) => {
              const mine = s.id === plan.story?.id;
              const own = plan.waitsFor(s).filter(isOpen);
              return pop(s, (
                <Popover.Trigger
                  openOnHover
                  delay={250}
                  render={
                    <button
                      type="button"
                      className={cn('flex min-w-0 flex-col gap-1 rounded-sm px-1.5 py-1.5 text-left hover:bg-tint-hover', mine && 'bg-tint-selected shadow-[inset_2px_0_0_var(--color-accent)] hover:bg-tint-selected', !s.confirmed && 'border border-dashed border-hairline-strong', anchor?.id === s.id && 'bg-tint-hover')}
                    />
                  }
                >
                  <span className="flex w-full min-w-0 items-center gap-1 text-caption text-body">
                    <span className="line-clamp-2 min-w-0 flex-1">{s.title}</span>
                    {own.length > 0 && <Lock aria-label={`After ${own.map((b) => b.title).join(', ')}`} className="size-3 shrink-0 text-warning" />}
                  </span>
                  <Progress card={s} className="w-full" />
                  {(mine || !s.confirmed) && <span className="flex gap-1">{mine && <Chip tone="accent" className="h-4 px-0 text-meta">This task’s story</Chip>}{!s.confirmed && <ProposedChip />}</span>}
                </Popover.Trigger>
              ));
            })}
          </section>
        ))}
      </div>
    </aside>
  );
}

/* ---------- The prototype's switcher ---------- */

export function ProtoSwitcher() {
  const { variant } = useProto();
  if (!variant) return null;
  const names: [Variant, string][] = [[1, 'Side panel'], [2, 'Story strip'], [3, 'Stories rail']];
  return (
    <div role="toolbar" aria-label="Prototype: plan view" className="fixed bottom-1.5 left-1/2 z-50 flex -translate-x-1/2 items-center gap-0.5 rounded-md border border-dashed border-attention bg-raised p-0.5 shadow-float">
      <span className="px-1.5 text-meta font-semibold text-attention uppercase">Prototype</span>
      {names.map(([v, name]) => (
        <Button key={v} size="sm" variant={variant === v ? 'secondary' : 'ghost'} aria-pressed={variant === v} onClick={() => proto.variant(v)}>
          {v}<span className="max-sm:hidden">. {name}</span>
        </Button>
      ))}
    </div>
  );
}
