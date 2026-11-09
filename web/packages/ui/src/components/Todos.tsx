// The todo list the agents keep, read-only: the live list, opened from the status strip's todo
// button (see StatusLine), and the list as a turn left it, opened from its reply's foot
// (`TurnTodo`). Both are the one anchored reader (`Reader`): a modal popover beside what opened
// it, that lifted above the dim, a compact head with the count and meter, eyebrow sections (by the
// turn that added the rows once the kept records are read, else by stage), and a keycap foot. A
// phone gets a sheet from the bottom. Dependencies are drawn as rails beside the rows (`Rails`), or,
// when this browser's setting says so, as a Mermaid flowchart (`TodoGraph`). A row's title opens
// its detail. The owner changes the list by asking in the chat.

import { ArrowUpRight, Ban, Check, ChevronRight, Circle, CircleDot, ListChecks, RotateCw, type LucideIcon } from 'lucide-react';
import { flushSync } from 'react-dom';
import { useContext, useEffect, useId, useLayoutEffect, useRef, useState, type ComponentProps, type ReactNode, type RefObject } from 'react';
import { useApi } from '../ApiContext';
import { describeError, isStatus, type Todo, type TodoCounts, type TodoHistory, type TodoStatus, type TurnTiming, type TurnTodos } from '../api';
import { cn } from '../lib/cn';
import { renderDiagram, svgDataUrl, type Rendered } from '../lib/diagram';
import { useScheme } from '../lib/theme';
import { loadTodoGraph, todoMermaid, type TodoGroup } from '../lib/todoGraph';
import { DONE_ROWS, METER_ROWS, firstLine, keepSnapshot, recallSnapshot, snapshotFacts, snapshotSections, todoDetail, todoLinks, todoOrigins, todoWord, turnSections, turnTally, turnTodoName, type RowDetail, type TodoLink, type TodoSection, type TurnOrigin, type TurnSection } from '../lib/todos';
import { SessionContext, clockTime } from './common';
import { Key } from './InlinePicker';
import { SubagentTag, useActions, useScope } from './Subagents';
import { plural } from './TimeSplit';
import { Blocks, type Block } from './ui/blocks';
import { Button } from './ui/button';
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

/** The subagent that first wrote a row: its identity dot, as on its row, and its name; a button that shows it once its name is known. */
function AgentTag({ id, name, quiet = false }: Readonly<{ id: string; name?: string; /** The dot alone on screen: the row's title already says the name. */ quiet?: boolean }>) {
  return (
    <span className="flex h-5 max-w-36 shrink-0 items-center text-caption text-muted">
      <span className="sr-only">, by </span>
      <SubagentTag id={id} name={name || 'subagent'} still={!name} className="max-w-full">
        {quiet ? <span className="sr-only">{name}</span> : undefined}
      </SubagentTag>
    </span>
  );
}

const sameWords = (a: string, b?: string) => !!b && a.trim().toLowerCase() === b.trim().toLowerCase();

/** One row: its stage's glyph, its title ("now" after the row the work is at, then how many times it was reopened), the blocked reason under it, the ledger's stamp under that, and the subagent that first wrote it. `waits` names the rows it waits on, for those who cannot see the rails. With `onToggle` the title is a button that opens the row's detail (`children`) under it. */
function TodoRow({ todo, name, now = false, quietName = false, stamp, waits, open = false, onToggle, children, ...li }: Readonly<{ todo: Todo; name?: string; now?: boolean; /** A name that repeats the title shows as the dot alone. */ quietName?: boolean; stamp?: ReactNode; waits?: string; open?: boolean; onToggle?: () => void; children?: ReactNode } & Pick<ComponentProps<'li'>, 'onPointerEnter' | 'onPointerLeave' | 'onPointerDown'>>) {
  const words = (
    <>
      <span className={cn('text-ui break-words', todo.status === 'done' ? 'text-muted' : todo.status === 'in_progress' ? 'font-medium text-ink' : 'text-body')}>
        {todo.title}
        {now && <span className="ml-1.5 text-caption font-normal text-accent">now</span>}
        {!!todo.reopened && (
          <span className="ml-1.5 inline-flex items-center gap-0.5 align-[-1px] text-caption font-normal tabular-nums text-accent">
            <RotateCw aria-hidden="true" className="size-3" />
            <span aria-hidden="true">{todo.reopened}</span>
            <span className="sr-only">, reopened {todo.reopened === 1 ? 'once' : `${todo.reopened} times`}</span>
          </span>
        )}
      </span>
      {todo.status === 'blocked' && todo.note && <span className="text-caption break-words text-warning">{todo.note}</span>}
      {stamp && <span className="text-caption tabular-nums text-muted">{stamp}</span>}
    </>
  );
  return (
    <li data-row={todo.id} className="py-1" {...li}>
      <div className="flex min-h-5 items-start gap-2 pointer-coarse:min-h-7">
        <span className="flex h-5 w-4 shrink-0 items-center justify-center">
          <TodoMark status={todo.status} />
        </span>
        {onToggle ? (
          <button type="button" aria-expanded={open} onClick={onToggle} className={cn('-mx-1 -my-0.5 flex min-w-0 flex-1 flex-col rounded-sm px-1 py-0.5 text-left transition-colors duration-100 hover:bg-tint-hover', open && 'bg-tint-selected')}>
            {words}
          </button>
        ) : (
          <span className="flex min-w-0 flex-1 flex-col">{words}</span>
        )}
        {todo.agent_id && <AgentTag id={todo.agent_id} name={name} quiet={quietName && sameWords(todo.title, name)} />}
        <span className="sr-only">, {todoWord(todo.status)}{waits && `, waits on ${waits}`}</span>
      </div>
      {open && children}
    </li>
  );
}

/** A dependency in a row's detail: its glyph and title, a button that opens that row's detail. */
function DetailLink({ todo, onRow }: Readonly<{ todo: Todo; onRow: (id: string) => void }>) {
  return (
    <li>
      <button type="button" onClick={() => onRow(todo.id)} className="-mx-1 flex max-w-full items-start gap-1.5 rounded-sm px-1 text-left text-body transition-colors duration-100 hover:bg-tint-hover pointer-coarse:min-h-11 pointer-coarse:items-center">
        <span className="flex h-4.5 shrink-0 items-center">
          <TodoMark status={todo.status} className="size-3" />
        </span>
        <span className="min-w-0 break-words">{todo.title}</span>
        <span className="sr-only">, {todoWord(todo.status)}</span>
      </button>
    </li>
  );
}

/**
 * A row's detail under it: its note (a blocked row's already stands under its title), the rows it
 * waits on and those waiting on it, each opening its own row, its history from the turn records,
 * and the way to the turn that added it and to the subagent that wrote it.
 */
function TodoDetail({ todo, detail, turn, onTurn, name, onRow }: Readonly<{ todo: Todo; detail: RowDetail; turn?: number; onTurn?: () => void; name?: string; onRow: (id: string) => void }>) {
  const show = useActions()?.show;
  const note = todo.status !== 'blocked' && todo.note;
  return (
    <div className="mt-1 mb-1 ml-6 flex flex-col gap-1.5 rounded-md bg-tint-well px-3 py-2 text-caption">
      {note && <p className="break-words text-body">{note}</p>}
      <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1">
        {detail.waits.length > 0 && (
          <>
            <dt className="text-muted">Waits on</dt>
            <dd>
              <ul>{detail.waits.map((t) => <DetailLink key={t.id} todo={t} onRow={onRow} />)}</ul>
            </dd>
          </>
        )}
        {detail.unblocks.length > 0 && (
          <>
            <dt className="text-muted">Unblocks</dt>
            <dd>
              <ul>{detail.unblocks.map((t) => <DetailLink key={t.id} todo={t} onRow={onRow} />)}</ul>
            </dd>
          </>
        )}
        {detail.history.length > 0 && (
          <>
            <dt className="text-muted">History</dt>
            <dd className="tabular-nums text-muted">
              {detail.history.map((s, i) => (
                <span key={i}>
                  {i > 0 && ' · '}
                  <span className="text-body">{s.word}</span>
                  {s.at && ` ${clockTime(s.at)}`}
                </span>
              ))}
            </dd>
          </>
        )}
      </dl>
      {(onTurn || (todo.agent_id && name && show)) && (
        <div className="flex flex-wrap gap-x-3">
          {onTurn && (
            <Button variant="link" onClick={onTurn}>
              {turn ? `Go to turn ${turn}` : 'Go to turn'}
              <ArrowUpRight aria-hidden="true" />
            </Button>
          )}
          {todo.agent_id && name && show && (
            <Button variant="link" onClick={() => show(todo.agent_id!)}>
              {`Open ${name}`}
              <ChevronRight aria-hidden="true" />
            </Button>
          )}
        </div>
      )}
    </div>
  );
}

/** A rail's colour: a loop in the accent, a blocked row's wait in the warning tone, any other link quiet. */
const RAIL: Record<TodoLink['kind'], string> = { loop: 'text-accent', wait: 'text-warning', link: 'text-faint' };
/** Quiet links first, so a loop or a wait sharing a trunk draws over them. */
const RAIL_ORDER: Record<TodoLink['kind'], number> = { link: 0, wait: 1, loop: 2 };
/** A lane's width, and the y of a row's glyph centre below the row's top (its `py-1` plus half its 20 px line). */
const LANE = 7;
const GLYPH_Y = 14;

/**
 * The dependencies among `rows` drawn in one gutter left of them, from the row waited on to the
 * arrow at the row that waits (`todoLinks`), across whatever stands between them (the section
 * heads). `children` holds the rows' `li`s (`data-row`), measured here; `focus`, the row pointed
 * at (an index in `rows`), lights its links and dims the others.
 */
function Rails({ rows, links, lanes, focus, children }: Readonly<{ rows: readonly Todo[]; links: readonly TodoLink[]; lanes: number; focus: number | null; children: ReactNode }>) {
  const box = useRef<HTMLDivElement>(null);
  const [ys, setYs] = useState<number[]>([]);
  const [height, setHeight] = useState(0);
  useLayoutEffect(() => {
    const el = box.current;
    if (!el) return;
    const measure = () => {
      const top = el.getBoundingClientRect().top;
      const at = new Map(Array.from(el.querySelectorAll<HTMLElement>('li[data-row]'), (li) => [li.dataset.row, li.getBoundingClientRect().top - top + GLYPH_Y]));
      const next = rows.map((t) => at.get(t.id) ?? NaN);
      setYs((was) => (was.length === next.length && was.every((y, i) => y === next[i]) ? was : next));
      setHeight(el.offsetHeight);
    };
    measure();
    if (typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => observer.disconnect();
  }, [rows]);
  const gutter = lanes * LANE + 6;
  return (
    <div ref={box} className="relative">
      <svg aria-hidden="true" width={gutter} height={height} className="pointer-events-none absolute top-0 left-0 overflow-visible" fill="none" strokeWidth={1.25} strokeLinecap="round" strokeLinejoin="round">
        {ys.length === rows.length &&
          [...links].sort((a, b) => RAIL_ORDER[a.kind] - RAIL_ORDER[b.kind]).map((l) => {
            const x = gutter - 4 - l.lane * LANE, from = ys[l.from], to = ys[l.to], dir = to > from ? 1 : -1, r = Math.min(4, Math.abs(to - from) / 2);
            if (Number.isNaN(from) || Number.isNaN(to)) return null;
            const end = gutter + 2;
            const lit = focus === null || focus === l.from || focus === l.to;
            return (
              <g key={`${l.from}>${l.to}`} stroke="currentColor" className={cn(RAIL[l.kind], 'transition-opacity duration-100', !lit && 'opacity-25')}>
                <path d={`M${end} ${from} H${x + r} Q${x} ${from} ${x} ${from + dir * r} V${to - dir * r} Q${x} ${to} ${x + r} ${to} H${end}`} />
                <path d={`M${end - 3} ${to - 2.5} L${end} ${to} L${end - 3} ${to + 2.5}`} />
              </g>
            );
          })}
      </svg>
      <div style={{ paddingLeft: gutter + 2 }}>{children}</div>
    </div>
  );
}

/**
 * The dependencies as a Mermaid flowchart (`todoMermaid`), rendered in the sandboxed diagram frame
 * and shown as an image, as any Mermaid fence. The last drawing stays while the next renders, so
 * a change never blanks it; one that fails to render says so. The rows stay in a list for screen
 * readers, each saying what it waits on.
 */
function TodoGraph({ rows, links, now, group }: Readonly<{ rows: readonly Todo[]; links: readonly TodoLink[]; now?: string; group?: (todo: Todo) => TodoGroup }>) {
  const scheme = useScheme();
  const source = todoMermaid(rows, links, now, group);
  const [drawn, setDrawn] = useState<{ rendered?: Rendered; error?: string } | null>(null);
  useEffect(() => {
    let on = true;
    renderDiagram(source).then(
      (rendered) => on && setDrawn({ rendered }),
      (err: unknown) => on && setDrawn((d) => ({ rendered: d?.rendered, error: err instanceof Error ? err.message : 'Rendering failed' })),
    );
    return () => {
      on = false;
    };
  }, [source, scheme]);
  const titles = new Map(rows.map((t) => [t.id, t.title]));
  const rendered = drawn?.rendered;
  return (
    <>
      {rendered ? (
        <img src={svgDataUrl(rendered.svg)} width={rendered.width} height={rendered.height} alt="" decoding="async" className="mx-auto block h-auto max-w-full" />
      ) : (
        <p className="py-6 text-center text-caption text-muted">{drawn?.error ? 'The graph could not be drawn' : 'Drawing the graph…'}</p>
      )}
      {rendered && drawn?.error && <p className="text-caption text-muted">The graph could not be redrawn; this is the last one</p>}
      <ul className="sr-only">
        {rows.map((todo) => (
          <TodoRow key={todo.id} todo={todo} now={todo.id === now && todo.status === 'in_progress'} waits={todo.depends_on?.map((id) => titles.get(id)).filter(Boolean).join(', ')} />
        ))}
      </ul>
    </>
  );
}

/** What a reader shows: the rows the meter draws, the counts, the facts line, the sections, the rows in the list's own order the rails need when some wait on others (`rows`), the row the work is at (`now`, the live list), the foot's words, each row's subagent name, and the strip's blocks a phone's sheet repeats on top (it covers the strip). The history reader swaps the head's `glyph` and its `right`-hand words, and stamps each row. With the Thread's kept records (`history`) and its `timings`, the rows group by the turn that added them. */
export interface ReaderParts { todos: readonly Todo[]; counts: TodoCounts; facts: string[]; sections: TodoSection[]; rows?: readonly Todo[]; /** Where the graph puts a row: the turn tag and the subagent box. */ group?: (todo: Todo) => TodoGroup; now?: string; foot: string; name: (todo: Todo) => string | undefined; blocks?: ReactNode; glyph?: LucideIcon; right?: ReactNode; stamp?: (todo: Todo) => ReactNode; history?: TodoHistory | null; timings?: readonly TurnTiming[] }

/** One stage's section; Done shows its newest `DONE_ROWS` rows until "Show all". */
function StageSection({ section, row }: Readonly<{ section: TodoSection; row: (todo: Todo) => ReactNode }>) {
  const [all, setAll] = useState(false);
  const capped = section.status === 'done' && section.todos.length > DONE_ROWS;
  const rows = capped && !all ? section.todos.slice(0, DONE_ROWS) : section.todos;
  return (
    <PanelSection
      label={section.label}
      icon={
        <span className="flex w-4 shrink-0 justify-center">
          <TodoMark status={section.status} className="size-3" />
        </span>
      }
      meta={<span className={cn('tabular-nums', TONE[section.status].text)}>{section.todos.length}</span>}
    >
      <ul>{rows.map(row)}</ul>
      {capped && (
        <Button size="sm" variant="subtle" className="w-full justify-start text-caption font-normal text-muted pointer-coarse:h-11" onClick={() => setAll((a) => !a)}>
          {all ? 'Show fewer' : `Show all ${section.todos.length}`}
        </Button>
      )}
    </PanelSection>
  );
}

/** A turn section's name: "Turn 3 · 3:30 PM", from its timing's start (else its record's end). */
function turnLabel(s: TurnSection): string {
  const at = s.timing?.started_at ?? s.record?.ended_at;
  return [s.turn ? `Turn ${s.turn}` : 'Latest turn', at ? clockTime(at) : ''].filter(Boolean).join(' · ');
}

/**
 * One turn's section: "Turn 3 · 3:30 PM" with its tally at the end, the first line of the prompt
 * that started it and "Go to turn", then the rows it added. A turn whose rows are all done folds to
 * one line that opens it.
 */
function TurnSectionView({ section, prompt, onTurn, unfolded, onFold, row }: Readonly<{ section: TurnSection; prompt?: string; onTurn?: () => void; unfolded: boolean; onFold: () => void; row: (todo: Todo) => ReactNode }>) {
  const label = turnLabel(section);
  const tally = turnTally(section);
  const quote = prompt && <span className="min-w-0 truncate text-body">{`“${prompt}”`}</span>;
  if (section.folded) {
    return (
      <section aria-label={label} className="flex min-w-0 flex-col gap-1.5">
        <button type="button" aria-expanded={unfolded} onClick={onFold} className="-mx-1 flex h-6 min-w-0 items-center gap-2 rounded-sm px-1 text-left text-caption text-muted transition-colors duration-100 hover:bg-tint-hover pointer-coarse:min-h-11">
          <ChevronRight aria-hidden="true" className={cn('size-3 shrink-0 transition-transform duration-100', unfolded && 'rotate-90')} />
          <span className="shrink-0 text-eyebrow uppercase">{label}</span>
          <span className="shrink-0 tabular-nums">{tally}</span>
          {!unfolded && quote}
        </button>
        {unfolded && (
          <>
            {(quote || onTurn) && <TurnPrompt quote={quote} onTurn={onTurn} />}
            <ul>{section.todos.map(row)}</ul>
          </>
        )}
      </section>
    );
  }
  return (
    <PanelSection label={label} action={<span className="shrink-0 text-caption tabular-nums text-muted">{tally}</span>}>
      {(quote || onTurn) && <TurnPrompt quote={quote} onTurn={onTurn} />}
      <ul>{section.todos.map(row)}</ul>
    </PanelSection>
  );
}

function TurnPrompt({ quote, onTurn }: Readonly<{ quote: ReactNode; onTurn?: () => void }>) {
  return (
    <p className="-mt-1 flex min-w-0 items-center gap-3 text-caption">
      {quote}
      <span className="flex-1" />
      {onTurn && (
        <Button variant="link" className="shrink-0" onClick={onTurn}>
          Go to turn
          <ArrowUpRight aria-hidden="true" />
        </Button>
      )}
    </p>
  );
}

/**
 * The list in the reader: the count and meter in the head, the facts, then the sections, each row
 * with the subagent that wrote it. With the kept records the sections are the turns that added the
 * rows, newest first, and any dependencies among the rows shown run in one gutter across them.
 * Without them the sections are the stages; when open rows wait on others, the open rows show as
 * one list on rails (by stage, newest first) and Done stays its own section below. A row's title
 * opens its detail under it, one row at a time.
 */
function TodoBody({ parts, sheet, heading, close, title, onClose }: Readonly<{ parts: ReaderParts; sheet: boolean; heading: RefObject<HTMLHeadingElement | null>; close: ReactNode; title: string; onClose: () => void }>) {
  const { done = 0, total = 0 } = parts.counts;
  const Glyph = parts.glyph ?? ListChecks;
  const actions = useActions();
  const messages = useScope((d) => d.messages);
  const body = useRef<HTMLDivElement>(null);
  const [unfolded, setUnfolded] = useState<ReadonlySet<string>>(() => new Set());
  const [openRow, setOpenRow] = useState<string | null>(null);
  const [focus, setFocus] = useState<number | null>(null);
  const graph = loadTodoGraph() === 'graph';
  const origin = parts.history ? todoOrigins(parts.history, parts.timings ?? []) : null;
  const turns = origin && !graph ? turnSections(parts.sections.flatMap((s) => s.todos), origin) : null;
  const open = parts.sections.filter((s) => s.status !== 'done');
  const openRows = open.flatMap((s) => s.todos);
  const shownTurns = turns?.filter((s) => !s.folded || unfolded.has(s.key));
  // Rails link open rows only, as the stages do: a wait on a done row is met.
  const railRows = graph ? parts.rows : (shownTurns?.flatMap((s) => s.todos.filter((t) => t.status !== 'done')) ?? openRows);
  const rails = parts.rows && railRows ? todoLinks(railRows) : null;
  const facts = [...parts.facts, turns && !parts.right ? plural(turns.length, 'turn') : '', rails?.loops ? (rails.loops === 1 ? '1 loop' : `${rails.loops} loops`) : '', rails?.hidden ? `${rails.hidden} more links not drawn` : ''].filter(Boolean);
  const all = parts.rows ?? parts.todos;
  const railed = rails ? (railRows ?? []) : [];
  const railAt = new Map(railed.map((t, i) => [t.id, i]));
  const titles = new Map(railed.map((t) => [t.id, t.title]));
  const goTo = (o: TurnOrigin) => {
    const user = o.timing?.user_item_id;
    return user && actions ? () => {
      onClose();
      actions.jumpToReply(user);
    } : undefined;
  };
  const openTo = (id: string) => {
    const key = origin?.(id).key;
    // The row may sit in a folded turn: it renders before it takes the focus.
    flushSync(() => {
      if (key !== undefined) setUnfolded((u) => (u.has(key) ? u : new Set(u).add(key)));
      setOpenRow(id);
    });
    body.current?.querySelector<HTMLElement>(`li[data-row="${CSS.escape(id)}"] button[aria-expanded]`)?.focus();
  };
  const row = (todo: Todo) => {
    const i = railAt.get(todo.id);
    const o = origin?.(todo.id);
    const name = todo.agent_id ? parts.name(todo) : undefined;
    return (
      <TodoRow
        key={todo.id}
        todo={todo}
        now={todo.id === parts.now && todo.status === 'in_progress'}
        name={name}
        quietName
        stamp={parts.stamp?.(todo)}
        waits={i === undefined ? undefined : todo.depends_on?.map((id) => titles.get(id)).filter(Boolean).join(', ')}
        open={openRow === todo.id}
        onToggle={() => setOpenRow((r) => (r === todo.id ? null : todo.id))}
        {...(i !== undefined && {
          onPointerEnter: (e) => e.pointerType === 'mouse' && setFocus(i),
          onPointerLeave: (e) => e.pointerType === 'mouse' && setFocus(null),
          onPointerDown: (e) => e.pointerType !== 'mouse' && setFocus((f) => (f === i ? null : i)),
        })}
      >
        <TodoDetail todo={todo} detail={todoDetail(todo, all, parts.history)} turn={o?.turn} onTurn={o && goTo(o)} name={name} onRow={openTo} />
      </TodoRow>
    );
  };
  const prompt = (s: TurnSection) => firstLine(messages?.find((m) => m.id === s.timing?.user_item_id)?.text) ?? firstLine(s.record?.intent);
  const turnList = turns?.map((s) => (
    <TurnSectionView
      key={s.key}
      section={s}
      prompt={prompt(s)}
      onTurn={goTo(s)}
      unfolded={unfolded.has(s.key)}
      onFold={() => setUnfolded((u) => {
        const next = new Set(u);
        if (!next.delete(s.key)) next.add(s.key);
        return next;
      })}
      row={row}
    />
  ));
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
        {facts.length > 0 && <p className="pl-6 text-caption text-muted">{facts.join(' · ')}</p>}
      </PanelHead>
      <div ref={body} className="flex min-h-0 flex-col gap-3 overflow-x-hidden overflow-y-auto overscroll-contain px-4 pt-1 pb-3">
        {sheet && parts.blocks}
        {rails && parts.rows && graph && <TodoGraph rows={parts.rows} links={rails.links} now={parts.now} group={parts.group} />}
        {turnList ? (
          rails ? (
            <Rails rows={railRows!} links={rails.links} lanes={rails.lanes} focus={focus}>
              <div className="flex flex-col gap-3">{turnList}</div>
            </Rails>
          ) : turnList
        ) : (
          <>
            {rails && !graph && (
              <Rails rows={openRows} links={rails.links} lanes={rails.lanes} focus={focus}>
                <ul>{openRows.map(row)}</ul>
              </Rails>
            )}
            {!(rails && graph) && (rails ? parts.sections.filter((s) => s.status === 'done') : parts.sections).map((section) => <StageSection key={section.status} section={section} row={row} />)}
          </>
        )}
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
      {(close) => <TodoBody title={title} parts={parts} sheet={sheet} heading={heading} close={close} onClose={onClose} />}
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
    rows: rows?.todos,
    group: (t) => ({ agent: agents.get(t.id) }),
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
