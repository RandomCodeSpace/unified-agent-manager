import { ArrowDown, Bot, ChevronRight, Ellipsis, FileDiff, GitBranch, Pencil } from 'lucide-react';
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { LIVE, api, describeError, provider, readOnly, stageLabel, taskName, type BackgroundTasks, type Changes as ChangesData, type Interaction, type Item, type Project, type SessionDetail, type SessionSummary, type TaskDefaults } from '../api';
import type { AgentTranscript } from '../state';
import { popupOpen } from '../App';
import { cn } from '../lib/cn';
import { awaitsUser, completedChanges, foregroundItems } from '../lib/transcript';
import { ChangesSheet } from './Changes';
import { INTERRUPTED_TEXT, InlineName, Note, ProjectBadge, ScrollSentinel, Spinner, StateMark, TaskTitle, WorkingMark, useApp, useScrolled } from './common';
import { Chip } from './ui/chip';
import { Appear } from './ui/appear';
import { Collapse, usePresence } from './ui/collapse';
import { Composer, type FirstMessage } from './Composer';
import { HistoryStatus } from './PreviousSessions';
import { InteractionCard } from './Interactions';
import { SubagentPanel, type PanelView } from './Subagents';
import { canRename, taskMenuItems, useTaskActions } from './taskActions';
import { Transcript } from './Transcript';
import { Button } from './ui/button';
import { Menu } from './ui/menu';
import { Tip } from './ui/tooltip';

interface Props {
  session: SessionDetail;
  project: Project | undefined;
  agents: Record<string, AgentTranscript>;
  /** Each subagent's latest step from live frames, for its row's summary before its transcript is open. */
  agentSteps: Record<string, Item>;
  snapshotSeq: number;
  sheetOpen: boolean;
  /** Side panels (Changes, Subagents) sit beside the column (wide) rather than over it. */
  sidePanelInline: boolean;
  onSheet: (open: boolean) => void;
  onSessionUpdate: (s: SessionSummary) => void;
  onInteractionUpdate: (sessionId: string, i: Interaction) => void;
  /** Leading header control (the drawer button on narrow screens). */
  leading?: ReactNode;
}

/** Distance from the bottom, in px, under which the view counts as "at the bottom". */
const BOTTOM_SLACK = 32;

function BackgroundTaskList({ sessionId, snapshot, locked }: { sessionId: string; snapshot: BackgroundTasks | undefined; locked: boolean }) {
  const [response, setResponse] = useState<{ source: BackgroundTasks | undefined; snapshot: BackgroundTasks } | null>(null);
  const [requests, setRequests] = useState<Record<string, { pending?: boolean; requested?: boolean; error?: string }>>({});
  // A newer SSE observation wins over a cancellation response started from an older snapshot.
  const shown = response && response.source === snapshot ? response.snapshot : snapshot;
  const running = shown?.tasks.filter((task) => task.status === 'running').length ?? 0;
  const [open, setOpen] = useState(running > 0);
  // Opens itself when a background task starts, as the list did before; closing it stays the user's choice.
  const [wasRunning, setWasRunning] = useState(running);
  if (running !== wasRunning) {
    setWasRunning(running);
    if (wasRunning === 0 && running > 0) setOpen(true);
  }
  if (!shown?.tasks.length) return null;
  async function stop(id: string) {
    if (requests[id]?.pending || locked || !shown?.known) return;
    setRequests((r) => ({ ...r, [id]: { pending: true } }));
    try {
      const result = await api.cancelBackgroundTask(sessionId, id);
      setResponse({ source: snapshot, snapshot: result.background_tasks });
      setRequests((r) => ({ ...r, [id]: { requested: result.accepted } }));
    } catch (e) {
      setRequests((r) => ({ ...r, [id]: { error: describeError(e) } }));
    }
  }
  return (
    <div className="mb-2 text-caption text-muted">
      <button type="button" aria-expanded={open} className="flex h-8 items-center gap-1.5 rounded-sm text-left transition-colors duration-100 hover:text-body" onClick={() => setOpen((o) => !o)}>
        <ChevronRight aria-hidden="true" className={cn('size-3 shrink-0 text-faint transition-transform duration-160 ease-app', open && 'rotate-90')} />
        Background tasks · {shown.known ? `${running} running` : 'Status unavailable'}
        {shown.known && running > 0 && <WorkingMark />}
      </button>
      <Collapse open={open}>
      {!shown.known && <p className="pb-2">Last reported tasks. Their current status is unavailable.</p>}
      <ul className="max-h-40 space-y-2 overflow-y-auto overscroll-contain pb-2">
        {shown.tasks.map((task) => (
          <li key={task.id} className="min-w-0">
            <div className="flex items-center gap-2">
              <span className="min-w-0 flex-1 truncate text-body" title={task.description || task.command}>{task.description || 'Shell task'}</span>
              {shown.known && task.status === 'running' ? (
                <Chip tone="accent">
                  <WorkingMark />
                  Running
                </Chip>
              ) : (
                <span className="shrink-0 capitalize">{shown.known ? task.status : 'Unknown'}</span>
              )}
              {task.status === 'running' && <Tip label={locked ? 'This task is read-only.' : !shown.known ? 'Refresh the connection to check this task before stopping it.' : 'Stop this background shell'}>
                <Button size="sm" variant="subtle" aria-label={`Stop background task: ${task.description || task.command}`} loading={!!requests[task.id]?.pending} disabled={locked || !shown.known || requests[task.id]?.requested} onClick={() => void stop(task.id)}>
                  {requests[task.id]?.requested ? 'Stop requested' : 'Stop'}
                </Button>
              </Tip>}
            </div>
            <code className="block truncate font-sans text-caption" title={task.command}>{task.command}</code>
            {requests[task.id]?.error && <p role="alert" className="pt-1 text-error">{requests[task.id].error}</p>}
          </li>
        ))}
      </ul>
      </Collapse>
    </div>
  );
}

/** The conversation pane: a 44px header, the transcript scrolling across the pane, the composer pinned below. */
export function Task({ session, project, agents, agentSteps, snapshotSeq, sheetOpen, sidePanelInline, onSheet, onSessionUpdate, onInteractionUpdate, leading }: Props) {
  const actions = useTaskActions();
  const [changes, setChanges] = useState<ChangesData | null>(null);
  const [changesError, setChangesError] = useState<string | null>(null);
  const [changesTick, setChangesTick] = useState(0);
  const [showJump, setShowJump] = useState(false);
  const [panel, setPanel] = useState<PanelView | null>(null);
  const panelOpener = useRef<HTMLElement | null>(null);
  const live = LIVE.includes(session.state);
  const working = session.state === 'working' || session.state === 'starting';
  const scroller = useRef<HTMLDivElement>(null);
  const atBottom = useRef(true);
  const [scrolled, sentinel] = useScrolled();
  const renaming = actions.renaming?.id === session.id && actions.renaming.place === 'header';
  const busy = !!actions.busy[session.id];

  // The "n files changed" count: fetched on open, again when a turn starts or ends, on Refresh, and a
  // second after a file-changing tool call completes; one request at a time, a rise during one queues another.
  const fetching = useRef(false);
  const again = useRef(false);
  useEffect(() => {
    let alive = true;
    fetching.current = true;
    api
      .changes(session.id, session.capabilities.session_diff ? 'session' : 'workspace')
      .then((c) => {
        if (!alive) return;
        setChanges(c);
        setChangesError(null);
      })
      .catch((e: unknown) => {
        if (!alive) return;
        setChanges(null);
        setChangesError(describeError(e));
      })
      .finally(() => {
        if (!alive) return;
        fetching.current = false;
        if (again.current) {
          again.current = false;
          setChangesTick((t) => t + 1);
        }
      });
    return () => {
      alive = false;
    };
  }, [session.id, session.capabilities.session_diff, live, changesTick]);
  const edits = completedChanges(session.items);
  const seenEdits = useRef(edits);
  useEffect(() => {
    if (edits <= seenEdits.current) return;
    seenEdits.current = edits;
    const timer = window.setTimeout(() => {
      if (fetching.current) again.current = true;
      else setChangesTick((t) => t + 1);
    }, 1000);
    return () => window.clearTimeout(timer);
  }, [edits]);

  const scrollToBottom = useCallback(() => {
    const el = scroller.current;
    if (!el) return;
    el.scrollTop = el.scrollHeight;
    atBottom.current = true;
    setShowJump(false);
  }, []);

  // Follow new content only while the reader is at the bottom; otherwise offer a way back.
  useLayoutEffect(() => {
    const el = scroller.current;
    if (!el) return;
    if (atBottom.current) el.scrollTop = el.scrollHeight;
    else if (el.scrollHeight - el.scrollTop - el.clientHeight > BOTTOM_SLACK) setShowJump(true);
  }, [session.id, session.items, session.interactions]);

  // One side panel at a time: the Changes sheet wins while it is open; opening the other closes it.
  const shownPanel = sheetOpen ? null : panel;
  // A closing panel stays mounted, showing its last view, until its exit has run.
  const panelPresence = usePresence(!!shownPanel);
  const sheetPresence = usePresence(sheetOpen);
  const [lastPanel, setLastPanel] = useState<PanelView | null>(null);
  if (shownPanel && shownPanel !== lastPanel) setLastPanel(shownPanel);
  const panelView = shownPanel ?? lastPanel;

  const closePanel = useCallback(() => {
    setPanel(null);
    panelOpener.current?.focus();
    panelOpener.current = null;
  }, []);

  const openChanges = useCallback(() => {
    setPanel(null);
    onSheet(true);
  }, [onSheet]);
  const { startRename } = actions;
  const renameInHeader = useCallback(() => startRename(session.id, 'header'), [startRename, session.id]);

  function openPanel(view: PanelView, opener: HTMLElement) {
    panelOpener.current = opener;
    if (sheetOpen) onSheet(false);
    setPanel(view);
  }

  // Esc closes the panel when no popup owns the key.
  useEffect(() => {
    if (!shownPanel) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !popupOpen()) closePanel();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [shownPanel, closePanel]);

  /** Scroll the transcript to the `task` row that spawned a subagent and flash it. */
  function locate(toolCallId: string) {
    const el = document.getElementById(`item-${toolCallId}`);
    if (!el) return;
    el.scrollIntoView({ block: 'center' });
    el.classList.add('animate-flash');
    window.setTimeout(() => el.classList.remove('animate-flash'), 1400);
  }

  function onScroll() {
    const el = scroller.current;
    if (!el) return;
    atBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < BOTTOM_SLACK;
    if (atBottom.current) setShowJump(false);
  }

  // A decided card collapses in place (its last pending look, inert) instead of vanishing; it leaves once the collapse has run.
  // A request yolo mode is answering is never a card.
  const pendingIds = session.interactions.filter(awaitsUser).map((i) => i.id).join(',');
  const [seenPending, setSeenPending] = useState(pendingIds);
  const [lingering, setLingering] = useState<Interaction[]>([]);
  if (pendingIds !== seenPending) {
    setSeenPending(pendingIds);
    const gone = seenPending.split(',').filter((id) => id && !pendingIds.split(',').includes(id));
    const decided = gone.flatMap((id) => session.interactions.filter((i) => i.id === id)).map((i) => ({ ...i, state: 'pending' as const }));
    if (decided.length) setLingering((l) => [...l, ...decided.filter((i) => !l.some((x) => x.id === i.id))]);
  }
  const cards = [...session.interactions.filter(awaitsUser), ...lingering.filter((i) => !session.interactions.some((x) => x.id === i.id && awaitsUser(x)))];

  // The provider's own notice about a failure already stands in the transcript: the failed line is not repeated under it.
  const failure = session.state === 'failed' ? session.state_detail?.toLowerCase() ?? '' : '';
  const noticed = !!failure && foregroundItems(session.items).some((i) => i.kind === 'notice' && (i.text ?? '').toLowerCase().includes(failure));

  const name = taskName(session);
  const fileCount = changes?.supported ? changes.files.length : null;
  const detail = session.state_detail && session.state !== 'failed' ? session.state_detail : undefined;
  const agentsRunning = session.subagents.filter((s) => s.status === 'running').length;
  const items = taskMenuItems(session, actions, 'header');
  const renamable = canRename(session, actions);

  return (
    <div className="flex min-h-0 flex-1 animate-rise">
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="pane-header flex h-header shrink-0 items-center gap-1.5 pr-2 pl-3" data-scrolled={scrolled || undefined}>
          {leading}
          <div className="group/title flex min-w-0 flex-1 items-center gap-1.5">
            {project && <ProjectBadge badge={project.badge} className="mr-0.5" />}
            {renaming ? (
              <InlineName initial={session.name} label="Task name" className="h-8 max-w-md text-display-sm font-semibold" onSave={(v) => void actions.rename(session.id, v)} onCancel={actions.cancelRename} />
            ) : (
              <>
                <h1
                  className="min-w-0 truncate text-display-sm text-ink"
                  title={name || undefined}
                  onDoubleClick={() => renamable && actions.startRename(session.id, 'header')}
                >
                  <TaskTitle session={session} />
                </h1>
              </>
            )}
            {readOnly(session) ? (
              <Chip fill="outline">{stageLabel(session)}</Chip>
            ) : (
              <StateMark state={session.state} label title={detail} className="shrink-0" />
            )}
            {busy && <Spinner className="shrink-0" />}
            {/* The pencil takes no room until the title is hovered or it is focused, so the state chip sits by the title. */}
            {renamable && !renaming && (
              <Tip label="Rename">
                <Button size="icon" aria-label="Rename task" className="-ml-1.5 w-0 min-w-0 overflow-hidden px-0 text-muted opacity-0 transition-[width,opacity,margin] duration-100 group-hover/title:ml-0 group-hover/title:w-7 group-hover/title:opacity-100 focus-visible:ml-0 focus-visible:w-7 focus-visible:opacity-100 max-sm:hidden" onClick={() => actions.startRename(session.id, 'header')}>
                  <Pencil />
                </Button>
              </Tip>
            )}
          </div>
          {/* The project strip (DESIGN.md D3): the branch, then Changes, beside the title. */}
          {project?.branch && (
            <span className="flex min-w-0 max-w-40 items-center gap-1 text-meta text-muted max-sm:hidden" title={`Project branch: ${project.branch}\nProject folder: ${session.workdir}`}>
              <GitBranch aria-hidden="true" className="size-3 shrink-0" />
              <span className="truncate">{project.branch}</span>
            </span>
          )}
          <Tip label={`Changes in ${project?.name ?? 'the project'}`}>
            <Button id="changes-link" size="md" aria-pressed={sheetOpen} aria-label={`Open changes${fileCount !== null ? `, ${fileCount} files` : ''}`} className="px-2 text-muted" onClick={openChanges}>
              <FileDiff />
              <span className="max-sm:hidden">Changes</span>
              {fileCount !== null && <span className="tabular-nums text-ink">{fileCount}</span>}
            </Button>
          </Tip>
          {session.subagents.length > 0 && (
            <Tip label="Subagents">
              <Button
                id="subagents-link"
                size="md"
                aria-pressed={!!shownPanel}
                aria-label={`Subagents, ${session.subagents.length}${agentsRunning ? `, ${agentsRunning} running` : ''}`}
                className="px-2 text-muted"
                onClick={(e) => (shownPanel ? closePanel() : openPanel({ view: 'list' }, e.currentTarget))}
              >
                <Bot />
                <span className="max-sm:hidden">Subagents</span>
                <span className="tabular-nums text-ink">{session.subagents.length}</span>
                {agentsRunning > 0 && <WorkingMark />}
              </Button>
            </Tip>
          )}
          <Menu.Root modal={false}>
            <Menu.Trigger render={<Button size="icon-md" aria-label="Task actions" className="text-muted" />}>
              <Ellipsis />
            </Menu.Trigger>
            <Menu.Content align="end">
              <Menu.Actions items={items} />
            </Menu.Content>
          </Menu.Root>
        </header>

        <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain" ref={scroller} onScroll={onScroll}>
          <ScrollSentinel sentinelRef={sentinel} />
          {/* The foot's extra padding is the dock's overlap plus a gap, so the last row can still scroll clear of the composer. */}
          <div className="flex w-full flex-col gap-6 px-3 pt-6 pb-16 sm:px-4 md:px-6" role="log">
            <HistoryStatus key={`${session.history}:${session.history_reason}`} session={session} />
            {session.terminal_session && <Note>Also open in the terminal{session.terminal_session.name ? `: ${session.terminal_session.name}` : ''}</Note>}
            {session.history_truncated && <Note>Earlier history was truncated; only the most recent part is shown.</Note>}
            {session.items.length === 0 && session.state === 'idle' && !readOnly(session) && <NewTaskIntro project={project} />}
            <Transcript
              sessionId={session.id}
              items={session.items}
              turnTimings={session.turn_timings}
              interactions={session.interactions}
              subagents={session.subagents}
              agents={agents}
              agentSteps={agentSteps}
              live={live}
              working={working}
              provider={session.provider}
              workdir={session.workdir}
              onOpenAgent={(id, opener) => openPanel({ view: 'agent', id }, opener)}
            />
            {cards.map((i) => (
              <Collapse key={i.id} open={session.interactions.some((x) => x.id === i.id && awaitsUser(x))} className="-mt-6" inner="pt-6" onClosed={() => setLingering((l) => l.filter((x) => x.id !== i.id))}>
                <InteractionCard session={session} interaction={i} onUpdate={(next) => onInteractionUpdate(session.id, next)} />
              </Collapse>
            ))}
            {session.state === 'interrupted' && <Note tone="warn">{INTERRUPTED_TEXT}</Note>}
            {session.state === 'failed' && !noticed && (
              <Note tone="error" role="alert">
                Turn failed{session.state_detail ? `: ${session.state_detail}` : '.'}
              </Note>
            )}
          </div>
        </div>

        {/* The floating control plane: the dock overlaps the transcript's foot by 40px and fades it out beneath the composer. */}
        <div className="transcript-dock -mt-10 w-full shrink-0 px-3 pt-10 pb-4 sm:px-4 md:px-6">
          <Appear show={showJump} className="absolute top-0 left-1/2 -translate-x-1/2">
            <Button variant="secondary" size="sm" className="shadow-float" onClick={scrollToBottom}>
              <ArrowDown />
              New output
            </Button>
          </Appear>
          <BackgroundTaskList key={`background-${session.id}`} sessionId={session.id} snapshot={session.background_tasks} locked={readOnly(session)} />
          <Composer key={session.id} session={session} onRename={renameInHeader} onSessionUpdate={onSessionUpdate} />
        </div>
      </div>

      {sheetPresence.mounted && <ChangesSheet session={session} projectName={project?.name ?? 'Project'} changes={changes} changesError={changesError} inline={sidePanelInline} open={sheetOpen} onRefresh={() => { setChangesError(null); setChangesTick((t) => t + 1); }} onClose={() => onSheet(false)} onClosed={sheetPresence.onClosed} />}
      {panelPresence.mounted && panelView && <SubagentPanel session={session} agents={agents} snapshotSeq={Math.max(snapshotSeq, session.seq ?? -1)} view={panelView} inline={sidePanelInline} open={!!shownPanel} onView={setPanel} onClose={closePanel} onClosed={panelPresence.onClosed} onLocate={locate} />}
    </div>
  );
}

function NewTaskIntro({ project }: { project: Project | undefined }) {
  return (
    <div className="flex flex-col items-center gap-1 py-10 text-center animate-rise">
      <p className="text-title text-ink">New task in {project?.name ?? 'this project'}</p>
      <p className="text-ui text-muted">Your first message gives this task its title.</p>
    </div>
  );
}

const noRename = () => {};

/**
 * A new Task before its first message: nothing exists on the service yet, so there is no
 * state, Changes or menu, only the Project's badge and a composer on the Project's
 * defaults. `onSend` creates the Task and delivers the message (App `createTask`).
 */
export function NewTaskPane({ project, defaults, onSend, leading }: { project: Project; defaults: TaskDefaults; onSend: (projectId: string, first: FirstMessage) => Promise<void>; leading?: ReactNode }) {
  const { meta } = useApp();
  const [session, setSession] = useState<SessionDetail>(() => ({
    id: '', project_id: project.id, provider: defaults.provider, name: '', title: '', workdir: project.dir, conversation_id: '',
    model: defaults.model, last_model: '', effort: defaults.effort, context_size: defaults.context_size, mode: defaults.mode,
    stage: 'active', state: 'idle', open: false, pending: 0, subagents_running: 0, created_at: '', updated_at: '',
    capabilities: provider(meta, defaults.provider)?.capabilities ?? { cancel: false, permissions: false, questions: false, session_diff: false, history: false },
    items: [], interactions: [], subagents: [], history_truncated: false, last_submission: null,
  }));
  const onSettings = useCallback((s: SessionSummary) => setSession((d) => ({ ...d, ...s })), []);
  const newTask = useMemo(() => ({ projectId: project.id, send: (first: FirstMessage) => onSend(project.id, first) }), [project.id, onSend]);
  return (
    <div className="flex min-h-0 flex-1 flex-col animate-rise">
      <header className="pane-header flex h-header shrink-0 items-center gap-1.5 pr-2 pl-3">
        {leading}
        <ProjectBadge badge={project.badge} className="mr-0.5" />
        <h1 className="min-w-0 truncate text-display-sm text-ink">New task</h1>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain">
        <div className="flex w-full flex-col gap-6 px-3 pt-6 pb-16 sm:px-4 md:px-6">
          <NewTaskIntro project={project} />
        </div>
      </div>
      <div className="transcript-dock -mt-10 w-full shrink-0 px-3 pt-10 pb-4 sm:px-4 md:px-6">
        <Composer session={session} onRename={noRename} onSessionUpdate={onSettings} newTask={newTask} />
      </div>
    </div>
  );
}
