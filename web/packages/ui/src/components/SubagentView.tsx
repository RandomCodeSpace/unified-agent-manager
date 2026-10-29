import { ArrowLeft, ChevronRight, Ellipsis, Square, Workflow } from 'lucide-react';
import { useLayoutEffect, useRef, type ReactNode } from 'react';
import { taskName, type Badge, type SessionDetail, type Subagent, type SubagentStatus } from '../api';
import { awaitsUser } from '../lib/transcript';
import { BAR_ROW, barCard, ProjectBadge, STATE_LABELS, STATE_TONE, useMedia, type Tone } from './common';
import { AgentTranscriptView, NOT_STOPPING, STATUS_LABEL, StopSubagent, SubagentMark, Took, useActions, useScope } from './Subagents';
import { Button } from './ui/button';
import { PHONE } from './ui/dialog';
import { Menu, type ActionItem } from './ui/menu';
import { Tip } from './ui/tooltip';

/** A subagent's state as the bar's still fill (DESIGN.md subagent view): running blue, failed red, done green; the rest neutral. */
const TONE: Partial<Record<SubagentStatus, Tone>> = { running: 'accent', failed: 'error', completed: 'success' };

/** The subagents that spawned `s`, outermost first. */
function spawners(s: Subagent, all: readonly Subagent[]): Subagent[] {
  const out: Subagent[] = [];
  let at = s.parent_agent_id ? all.find((x) => x.id === s.parent_agent_id) : undefined;
  while (at && !out.includes(at)) {
    out.unshift(at);
    const parent = at.parent_agent_id;
    at = parent ? all.find((x) => x.id === parent) : undefined;
  }
  return out;
}

const threadName = (session: SessionDetail) => taskName(session) || 'this thread';
const agentName = (s: Subagent) => s.name || 'Subagent';
/** One glyph for every subagent level, in the trail's 16px identity box. */
const AgentGlyph = () => <Workflow aria-hidden="true" className="size-4 shrink-0 text-muted" />;
const SEP = <ChevronRight aria-hidden="true" className="size-3.5 shrink-0 text-faint" />;
const CRUMB = 'flex h-7 min-w-0 items-center gap-1.5 rounded-sm px-1.5 text-ui text-body transition-colors duration-100 hover:bg-tint-hover hover:text-ink pointer-coarse:min-h-11';

/** Back to the Thread at the spawn point: the outermost spawner's call is in the Thread's conversation (Task `locate`). */
function useBack(s: Subagent, onBack: () => void): () => void {
  const actions = useActions();
  const subagents = useScope((d) => d.subagents) ?? [];
  const root = spawners(s, subagents)[0] ?? s;
  return () => {
    onBack();
    if (root.parent_tool_call_id) actions?.locate(root.parent_tool_call_id);
  };
}

/**
 * The Thread header's bar while a subagent fills the Conversation pane (DESIGN.md subagent view): ← and the trail
 * "Thread › spawners › subagent" at left, each crumb a way there; "type · time" and Stop (while it runs) at right.
 * The fill follows the subagent's state. On a phone the levels above it fold into a "…" menu and Stop is a glyph.
 */
export function SubagentBar({ session, subagent: s, badge, leading, onBack }: Readonly<{ session: SessionDetail; subagent: Subagent; badge?: Badge; leading?: ReactNode; onBack: () => void }>) {
  const actions = useActions();
  const subagents = useScope((d) => d.subagents) ?? [];
  const task = useScope((d) => d.task);
  const stopping = useScope((d) => d.stops[s.id]) ?? NOT_STOPPING;
  const phone = useMedia(PHONE);
  const back = useBack(s, onBack);
  if (!actions || !task) return null;
  const chain = spawners(s, subagents);
  const name = threadName(session);
  // A request of its own waiting for the owner is the Thread's needs-you state, in its word and tone.
  const needsYou = task.interactions.some((i) => i.agent_id === s.id && awaitsUser(i));
  const word = needsYou ? STATE_LABELS.awaiting_permission : STATUS_LABEL[s.status];
  const levels: ActionItem[] = [
    { key: 'thread', label: name, icon: badge ? <ProjectBadge badge={badge} /> : undefined, onSelect: back },
    ...chain.map((a) => ({ key: a.id, label: agentName(a), icon: <Workflow />, onSelect: () => actions.show(a.id) })),
  ];
  return (
    <header className={BAR_ROW}>
      <div role="group" aria-label={`State: ${word}`} className={barCard(needsYou ? STATE_TONE.awaiting_permission : TONE[s.status])}>
        {/* A phone gives the drawer's toggle to ←: the Thread it returns to has it. */}
        {!phone && leading}
        <Tip label={`Back to ${name} · Esc`}>
          <Button size="icon" aria-label={`Back to ${name}`} className="shrink-0 text-muted" onClick={back}>
            <ArrowLeft />
          </Button>
        </Tip>
        <nav aria-label="Trail" className="flex min-w-0 flex-1 items-center gap-1">
          {phone ? (
            <Menu.Root modal={false}>
              <Menu.Trigger render={<Button size="icon" aria-label="Show the levels above" className="shrink-0 text-muted" />}>
                <Ellipsis />
              </Menu.Trigger>
              <Menu.Content align="start" className="max-w-72">
                <Menu.Actions items={levels} />
              </Menu.Content>
            </Menu.Root>
          ) : (
            <>
              <Tip label={`Back to ${name}, where it was spawned`}>
                <button type="button" className={CRUMB} onClick={back}>
                  {badge && <ProjectBadge badge={badge} />}
                  <span className="max-w-64 truncate">{name}</span>
                </button>
              </Tip>
              {chain.map((a) => (
                <span key={a.id} className="flex min-w-0 items-center gap-1">
                  {SEP}
                  <button type="button" className={CRUMB} title={`Open ${agentName(a)}`} onClick={() => actions.show(a.id)}>
                    <AgentGlyph />
                    <span className="max-w-48 truncate">{agentName(a)}</span>
                  </button>
                </span>
              ))}
            </>
          )}
          {SEP}
          <span aria-current="page" className="flex h-7 min-w-0 items-center gap-1.5 px-1.5 text-ui font-medium text-ink">
            {!phone && <AgentGlyph />}
            <span className="truncate">{agentName(s)}</span>
            {phone && <span className="flex size-4 shrink-0 items-center justify-center"><SubagentMark status={s.status} /><span className="sr-only">{word}</span></span>}
          </span>
        </nav>
        {!phone && (
          <span className="flex shrink-0 items-center gap-2 pr-1 text-caption text-muted">
            {s.type && <><span>{s.type}</span><span aria-hidden="true" className="text-faint">·</span></>}
            <Took subagent={s} className="text-caption text-muted" />
          </span>
        )}
        {/* The confirmed stop; a phone keeps its glyph, so the name keeps the room. */}
        {s.status === 'running' && (phone ? (
          <Tip label={stopping.error ?? (stopping.requested ? 'Stop requested' : 'Stop this subagent')}>
            <Button size="icon" variant="secondary" aria-label={`Stop subagent ${agentName(s)}`} disabled={stopping.requested || stopping.busy} className="shrink-0" onClick={() => actions.stop(s)}>
              <Square className="!size-3" fill="currentColor" />
            </Button>
          </Tip>
        ) : <StopSubagent session={task} subagent={s} stopping={stopping} onStop={() => actions.stop(s)} />)}
      </div>
    </header>
  );
}

/**
 * A subagent in the Conversation pane, over the Thread's conversation (kept beneath, where it was): its transcript read
 * as a conversation (AgentTranscriptView). The strip's subagents section switches between them, as in the Thread.
 */
export function SubagentView({ session, subagent: s }: Readonly<{ session: SessionDetail; subagent: Subagent }>) {
  const subagents = useScope((d) => d.subagents) ?? [];
  const task = useScope((d) => d.task);
  const transcript = useScope((d) => d.agents[s.id]);
  const snapshotSeq = useScope((d) => d.snapshotSeq) ?? 0;
  const parent = useScope((d) => d.summaries.calls.get(s.parent_tool_call_id ?? ''));
  const seek = useScope((d) => (d.seek?.agent === s.id ? d.seek : null));
  const root = useRef<HTMLDivElement>(null);
  // The row that opened it is under the view now: the transcript takes the focus, so the keys scroll it at once.
  useLayoutEffect(() => {
    // No ring: the reader did not move there by keyboard.
    if (!root.current?.contains(document.activeElement)) root.current?.querySelector<HTMLElement>('[role="region"]')?.focus({ preventScroll: true, focusVisible: false });
  }, [s.id]);
  if (!task) return null;
  const spawner = spawners(s, subagents).at(-1);
  return (
    <div ref={root} className="absolute inset-0 z-10 flex flex-col bg-canvas">
      <AgentTranscriptView
        key={s.id}
        name={agentName(s)}
        provider={task.provider}
        sessionId={task.id}
        workdir={task.workdir}
        subagent={s}
        interactions={task.interactions}
        transcript={transcript}
        snapshotSeq={snapshotSeq}
        parent={parent}
        spawner={spawner ? agentName(spawner) : threadName(session)}
        result={(s.status === 'completed' || s.status === 'idle') && !s.background ? parent?.tool?.output : undefined}
        seek={seek}
      />
    </div>
  );
}
