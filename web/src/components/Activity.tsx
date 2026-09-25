import { Activity, ArrowLeft, Bot, Brain, Crosshair, FileText, MessageCircleQuestion, Search, ShieldCheck, Terminal, Wrench, X } from 'lucide-react';
import { memo, useEffect, useMemo, useRef, useState, type ComponentType, type SVGProps } from 'react';
import type { Interaction, SessionDetail } from '../api';
import { cn } from '../lib/cn';
import { activityTurns, approvalMark, askedOn, completedDuration, filterCounts, matchesFilter, questionOf, FILTERS, type ActivityEntry, type ActivityFilter, type ActivityKind, type ActivityTurn } from '../lib/transcript';
import { ImageThumbs } from './Attachments';
import { Markdown, Note, useApp } from './common';
import { PanelHeader, SidePanel } from './Subagents';
import { QuestionBlock, ToolDetails, ToolMark } from './Transcript';
import { Button } from './ui/button';
import { Collapse } from './ui/collapse';
import { Tip } from './ui/tooltip';

const KIND_ICON: Record<ActivityKind, ComponentType<SVGProps<SVGSVGElement>>> = {
  command: Terminal,
  file: FileText,
  search: Search,
  thought: Brain,
  question: MessageCircleQuestion,
  request: ShieldCheck,
  subagent: Bot,
  tool: Wrench,
};

const clock = (iso: string) => new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });

const STATUS_WORD: Record<ActivityEntry['status'], string> = { completed: 'done', failed: 'failed', running: 'running', pending: 'pending', ended: 'no result', decided: 'decided' };

/**
 * The Activity panel (DESIGN.md side panels): the Task's whole timeline grouped by turn,
 * every thought, tool call, question and decided request as one row with its kind, label,
 * state and duration, which opens onto its details. Filter chips narrow it to commands,
 * files, thinking or failures, with counts; a row's jump scrolls the conversation to its
 * turn. Beside the column (wide) or over it, like Changes and Subagents.
 */
export function ActivityPanel({ session, live, streamingId, inline, open, onClose, onClosed, onLocate }: { session: SessionDetail; live: boolean; streamingId?: string; inline: boolean; /** False while the panel leaves; `onClosed` follows, and the owner unmounts it. */ open: boolean; onClose: () => void; onClosed: () => void; /** Scroll the conversation to the turn a user message began. */ onLocate: (turnId: string) => void }) {
  const { narrow } = useApp();
  const lead = useRef<HTMLButtonElement>(null);
  const [filter, setFilter] = useState<ActivityFilter>('all');
  useEffect(() => {
    lead.current?.focus({ preventScroll: true });
  }, []);
  const { turns, approvals } = useMemo(() => activityTurns(session.items, session.interactions, session.turn_timings, live, streamingId), [session.items, session.interactions, session.turn_timings, live, streamingId]);
  const counts = filterCounts(turns);
  const shown = turns.map((turn) => ({ ...turn, entries: turn.entries.filter((entry) => matchesFilter(entry, filter)) })).filter((turn) => turn.entries.length > 0);
  return (
    <SidePanel id="activity" inline={inline} open={open} onClose={onClose} onClosed={onClosed} label="Activity">
      <PanelHeader>
        {narrow && (
          <Button ref={lead} size="icon-md" aria-label="Back to the task" className="-ml-1 text-muted" onClick={onClose}>
            <ArrowLeft />
          </Button>
        )}
        <Activity aria-hidden="true" className="size-4 text-muted" />
        <span className="text-title text-ink">Activity</span>
        <span className="text-caption tabular-nums text-muted">{counts.all}</span>
        <span className="flex-1" />
        {!narrow && (
          <Button ref={lead} size="icon-md" aria-label="Close activity" className="text-muted" onClick={onClose}>
            <X />
          </Button>
        )}
      </PanelHeader>
      <div role="group" aria-label="Show" className="flex shrink-0 flex-wrap gap-1 px-2 py-1.5">
        {FILTERS.map((f) => (
          <Button key={f.id} size="sm" aria-pressed={filter === f.id} className="px-2 text-caption text-muted" onClick={() => setFilter(f.id)}>
            {f.label}
            <span className="tabular-nums text-faint">{counts[f.id]}</span>
          </Button>
        ))}
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-2 pb-3">
        {turns.length === 0 && <Note className="px-2 py-1">Nothing recorded yet.</Note>}
        {turns.length > 0 && shown.length === 0 && <Note className="px-2 py-1">Nothing matches this filter.</Note>}
        {shown.map((turn) => (
          <TurnSection key={turn.id} turn={turn} sessionId={session.id} live={live} approvals={approvals} onLocate={onLocate} />
        ))}
      </div>
    </SidePanel>
  );
}

/** One turn: the user message's first line (a button to the turn), its time and duration, then its rows. */
function TurnSection({ turn, sessionId, live, approvals, onLocate }: { turn: ActivityTurn; sessionId: string; live: boolean; approvals: Map<string, Interaction[]>; onLocate: (turnId: string) => void }) {
  const took = completedDuration(turn.timing);
  const titleId = `activity-turn-${turn.id}`;
  return (
    <section aria-labelledby={titleId} className="mb-3">
      <div className="flex h-7 items-center gap-2 px-2 text-caption text-muted">
        <button id={titleId} type="button" title={`${turn.title}\nShow in the conversation`} className="min-w-0 flex-1 truncate rounded-xs text-left text-body transition-colors duration-100 hover:text-ink" onClick={() => onLocate(turn.id)}>
          {turn.title}
        </button>
        {turn.time && <span className="shrink-0 tabular-nums">{clock(turn.time)}</span>}
        {took && <span className="shrink-0 tabular-nums">{took}</span>}
      </div>
      <ul className="flex flex-col gap-px">
        {turn.entries.map((entry) => (
          <EntryRow key={entry.id} entry={entry} sessionId={sessionId} live={live} approvals={approvals} onJump={() => onLocate(turn.id)} />
        ))}
      </ul>
    </section>
  );
}

/**
 * One row of the timeline: the kind's glyph, the state mark, the label (mono for a call,
 * as its tool row), the duration; it opens onto the details, mounted on the first open only.
 */
const EntryRow = memo(function EntryRow({ entry, sessionId, live, approvals, onJump }: { entry: ActivityEntry; sessionId: string; live: boolean; approvals: Map<string, Interaction[]>; onJump: () => void }) {
  const [open, setOpen] = useState(false);
  const [opened, setOpened] = useState(false);
  const Icon = KIND_ICON[entry.kind];
  const mono = entry.kind !== 'thought' && entry.kind !== 'question' && entry.kind !== 'request';
  const detailsId = `activity-${entry.id}`;
  return (
    <li className="group/entry relative">
      <button
        type="button"
        aria-expanded={open}
        aria-controls={opened ? detailsId : undefined}
        className={cn('flex h-7 w-full items-center gap-2 rounded-sm pr-8 pl-2 text-left transition-colors focus-visible:-outline-offset-2 hover:bg-tint-hover pointer-coarse:min-h-11 pointer-coarse:pr-11', mono ? 'font-mono text-code-sm' : 'text-caption', entry.failed ? 'text-error' : 'text-muted')}
        title={[entry.name, entry.arg].filter(Boolean).join(' ')}
        onClick={() => {
          setOpened(true);
          setOpen((o) => !o);
        }}
      >
        <Icon aria-hidden="true" className="size-3.5 shrink-0 text-faint" />
        <span className="flex size-4 shrink-0 items-center justify-center">
          <ToolMark tone={entry.status} />
        </span>
        <span className="min-w-0 flex-1 truncate">
          <span className={cn('font-medium', !entry.failed && 'text-body')}>{entry.name}</span>
          {entry.arg && <> {entry.arg}</>}
        </span>
        <span className="sr-only">, {STATUS_WORD[entry.status]}</span>
        {entry.took && <span className="shrink-0 font-sans text-caption tabular-nums text-muted">{entry.took}</span>}
      </button>
      <Tip label="Show in the conversation">
        <Button size="icon-sm" aria-label={`Show ${entry.name} in the conversation`} className="absolute top-0.5 right-1 text-muted opacity-0 transition-opacity group-hover/entry:opacity-100 focus-visible:opacity-100 pointer-coarse:opacity-100" onClick={onJump}>
          <Crosshair />
        </Button>
      </Tip>
      {opened && (
        <Collapse open={open} appear>
          <div id={detailsId} className="px-2 pb-2 pl-8">
            <EntryDetails entry={entry} sessionId={sessionId} live={live} approvals={approvals} />
          </div>
        </Collapse>
      )}
    </li>
  );
});

/** What a row opens onto: a thought's text `muted`; a call's details and images; a question block; a request's full resolution. */
function EntryDetails({ entry, sessionId, live, approvals }: { entry: ActivityEntry; sessionId: string; live: boolean; approvals: Map<string, Interaction[]> }) {
  const { item, interaction } = entry.entry;
  if (interaction) {
    const asked = questionOf(undefined, interaction, live);
    if (asked) return <QuestionBlock id={`activity-question-${interaction.id}`} asked={asked} />;
    return <p className="text-caption text-muted">{approvalMark(interaction).full}</p>;
  }
  if (item.kind === 'reasoning') return <Markdown text={item.text ?? ''} className="md-quiet text-ui text-muted" />;
  const asked = askedOn(item, approvals, live);
  if (asked && asked.outcome !== 'pending') return <QuestionBlock id={`activity-question-${item.id}`} asked={asked} />;
  const marks = (approvals.get(item.id) ?? []).filter((ix) => ix.state !== 'pending').map(approvalMark);
  return (
    <>
      {marks.length > 0 && <p className="text-caption text-muted">{marks.map((m) => m.full).join('; ')}</p>}
      <ToolDetails item={item} />
      {(item.images?.length ?? 0) > 0 && <ImageThumbs sessionId={sessionId} images={item.images!} className="mt-1" />}
      {item.images_note && <p className="text-caption text-muted">{item.images_note}</p>}
    </>
  );
}
