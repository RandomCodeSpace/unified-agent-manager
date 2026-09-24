import { ArrowDown, Bot, Ellipsis, Pencil } from 'lucide-react';
import { useCallback, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import { LIVE, api, describeError, readOnly, stageLabel, taskName, type BackgroundTasks, type Changes as ChangesData, type Interaction, type Project, type SessionDetail, type SessionSummary } from '../api';
import type { AgentTranscript } from '../state';
import { popupOpen } from '../App';
import { ChangesSheet } from './Changes';
import { INTERRUPTED_TEXT, InlineName, Note, ProjectBadge, Spinner, StateMark, TaskTitle } from './common';
import { Composer } from './Composer';
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
  if (!shown?.tasks.length) return null;
  const running = shown.tasks.filter((task) => task.status === 'running').length;
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
    <details className="mb-2 text-caption text-muted" open={running > 0 || undefined}>
      <summary className="cursor-pointer py-2">
        Background tasks · {shown.known ? `${running} running` : 'Status unavailable'}
      </summary>
      {!shown.known && <p className="pb-2">Last reported tasks. Their current status is unavailable.</p>}
      <ul className="max-h-40 space-y-2 overflow-y-auto overscroll-contain pb-2">
        {shown.tasks.map((task) => (
          <li key={task.id} className="min-w-0">
            <div className="flex items-center gap-2">
              <span className="min-w-0 flex-1 truncate text-body" title={task.description || task.command}>{task.description || 'Shell task'}</span>
              <span className="shrink-0 capitalize">{shown.known ? task.status : 'Unknown'}</span>
              {task.status === 'running' && <Tip label={locked ? 'This task is read-only.' : !shown.known ? 'Refresh the connection to check this task before stopping it.' : 'Stop this background shell'}>
                <Button size="sm" variant="subtle" aria-label={`Stop background task: ${task.description || task.command}`} disabled={locked || !shown.known || requests[task.id]?.pending || requests[task.id]?.requested} onClick={() => void stop(task.id)}>
                  {requests[task.id]?.pending ? <><Spinner /> Stopping…</> : requests[task.id]?.requested ? 'Stop requested' : 'Stop'}
                </Button>
              </Tip>}
            </div>
            <code className="block truncate font-mono text-code-sm" title={task.command}>{task.command}</code>
            {requests[task.id]?.error && <p role="alert" className="pt-1 text-error">{requests[task.id].error}</p>}
          </li>
        ))}
      </ul>
    </details>
  );
}

/** The conversation pane: a 44px header, the transcript scrolling in a fixed column, the composer pinned below. */
export function Task({ session, project, agents, snapshotSeq, sheetOpen, sidePanelInline, onSheet, onSessionUpdate, onInteractionUpdate, leading }: Props) {
  const actions = useTaskActions();
  const [changes, setChanges] = useState<ChangesData | null>(null);
  const [changesTick, setChangesTick] = useState(0);
  const [showJump, setShowJump] = useState(false);
  const [panel, setPanel] = useState<PanelView | null>(null);
  const panelOpener = useRef<HTMLElement | null>(null);
  const live = LIVE.includes(session.state);
  const working = session.state === 'working' || session.state === 'starting';
  const scroller = useRef<HTMLDivElement>(null);
  const atBottom = useRef(true);
  const renaming = actions.renaming?.id === session.id && actions.renaming.place === 'header';
  const busy = actions.busy === session.id;

  // The "n files changed" count: fetched on open, again when a turn starts or ends, and on Refresh.
  useEffect(() => {
    let alive = true;
    api
      .changes(session.id, session.capabilities.session_diff ? 'session' : 'workspace')
      .then((c) => alive && setChanges(c))
      .catch(() => alive && setChanges(null));
    return () => {
      alive = false;
    };
  }, [session.id, session.capabilities.session_diff, live, changesTick]);

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

  const closePanel = useCallback(() => {
    setPanel(null);
    panelOpener.current?.focus();
    panelOpener.current = null;
  }, []);

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

  const name = taskName(session);
  const fileCount = changes?.supported ? changes.files.length : null;
  const detail = session.state_detail && session.state !== 'failed' ? session.state_detail : undefined;
  const agentsRunning = session.subagents.filter((s) => s.status === 'running').length;
  const items = taskMenuItems(session, actions, 'header');
  const renamable = canRename(session, actions);

  return (
    <div className="flex min-h-0 flex-1 animate-rise">
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-header shrink-0 items-center gap-1.5 border-b border-hairline pr-2 pl-3">
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
                {renamable && (
                  <Tip label="Rename">
                    <Button size="icon" aria-label="Rename task" className="text-muted opacity-0 transition-opacity group-hover/title:opacity-100 focus-visible:opacity-100 max-sm:hidden" onClick={() => actions.startRename(session.id, 'header')}>
                      <Pencil />
                    </Button>
                  </Tip>
                )}
              </>
            )}
            {readOnly(session) ? (
              <span className="inline-flex h-5 shrink-0 items-center rounded-xs border border-hairline-strong px-1.5 text-caption text-muted">{stageLabel(session)}</span>
            ) : (
              <StateMark state={session.state} label title={detail} className="shrink-0" />
            )}
            {busy && <Spinner className="shrink-0" />}
          </div>
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
                {agentsRunning > 0 && <Spinner />}
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
          <div className="mx-auto flex w-full flex-col gap-6 px-3 py-6 sm:px-4 md:px-6" role="log">
            <HistoryStatus key={`${session.history}:${session.history_reason}`} session={session} />
            {session.terminal_session && <Note>Also open in the terminal{session.terminal_session.name ? `: ${session.terminal_session.name}` : ''}</Note>}
            {session.history_truncated && <Note>Earlier history was truncated; only the most recent part is shown.</Note>}
            {session.items.length === 0 && session.state === 'idle' && !readOnly(session) && (
              <div className="flex flex-col items-center gap-1 py-10 text-center animate-rise">
                <p className="text-title text-ink">New task in {project?.name ?? 'this project'}</p>
                <p className="text-ui text-muted">Your first message gives this task its title.</p>
              </div>
            )}
            <Transcript
              sessionId={session.id}
              items={session.items}
              interactions={session.interactions}
              subagents={session.subagents}
              live={live}
              working={working}
              provider={session.provider}
              onOpenAgent={(id, opener) => openPanel({ view: 'agent', id }, opener)}
            />
            {session.interactions
              .filter((i) => i.state === 'pending')
              .map((i) => (
                <InteractionCard key={i.id} session={session} interaction={i} onUpdate={(next) => onInteractionUpdate(session.id, next)} />
              ))}
            {session.state === 'interrupted' && <Note tone="warn">{INTERRUPTED_TEXT}</Note>}
            {session.state === 'failed' && (
              <Note tone="error" role="alert">
                Turn failed{session.state_detail ? `: ${session.state_detail}` : '.'}
              </Note>
            )}
          </div>
        </div>

        <div className="relative shrink-0 px-3 pb-[max(12px,env(safe-area-inset-bottom))] sm:px-4 md:px-6">
          {showJump && (
            <Button variant="secondary" size="sm" className="absolute -top-10 left-1/2 -translate-x-1/2 shadow-float animate-rise" onClick={scrollToBottom}>
              <ArrowDown />
              New output
            </Button>
          )}
          <BackgroundTaskList key={`background-${session.id}`} sessionId={session.id} snapshot={session.background_tasks} locked={readOnly(session)} />
          <Composer key={session.id} session={session} project={project} fileCount={fileCount} onChanges={() => { setPanel(null); onSheet(true); }} onRename={() => actions.startRename(session.id, 'header')} onSessionUpdate={onSessionUpdate} />
        </div>
      </div>

      {sheetOpen && <ChangesSheet session={session} projectName={project?.name ?? 'Project'} changes={changes} inline={sidePanelInline} onRefresh={() => setChangesTick((t) => t + 1)} onClose={() => onSheet(false)} />}
      {shownPanel && !sidePanelInline && <button type="button" className="fixed inset-0 z-30 bg-backdrop animate-fade-in" aria-label="Close subagents" onClick={closePanel} />}
      {shownPanel && <SubagentPanel session={session} agents={agents} snapshotSeq={Math.max(snapshotSeq, session.seq ?? -1)} view={shownPanel} inline={sidePanelInline} onView={setPanel} onClose={closePanel} onLocate={locate} />}
    </div>
  );
}
