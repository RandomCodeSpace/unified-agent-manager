import { useCallback, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import {
  LIVE,
  api,
  describeError,
  needsYou,
  readOnly,
  stageLabel,
  taskName,
  type Changes as ChangesData,
  type Interaction,
  type Project,
  type SessionDetail,
  type SessionSummary,
} from '../api';
import type { AgentTranscript } from '../state';
import { ChangesSheet } from './Changes';
import { ConfirmDialog, INTERRUPTED_TEXT, Menu, NameDialog, Spinner, StateMark, TaskTitle } from './common';
import { Composer } from './Composer';
import { InteractionCard } from './Interactions';
import { SubagentPanel, type PanelView } from './Subagents';
import { Transcript } from './Transcript';

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
  onDeleted: (id: string) => void;
  onInteractionUpdate: (sessionId: string, i: Interaction) => void;
  /** Leading header control (the drawer button on narrow screens). */
  leading?: ReactNode;
}

/** Distance from the bottom, in px, under which the view counts as "at the bottom". */
const BOTTOM_SLACK = 32;

/** The conversation pane: a 44px header, the transcript scrolling in a fixed column, the composer pinned below. */
export function Task({
  session,
  project,
  agents,
  snapshotSeq,
  sheetOpen,
  sidePanelInline,
  onSheet,
  onSessionUpdate,
  onDeleted,
  onInteractionUpdate,
  leading,
}: Props) {
  const [dialog, setDialog] = useState<'rename' | 'close' | 'archive' | 'delete' | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [changes, setChanges] = useState<ChangesData | null>(null);
  const [changesTick, setChangesTick] = useState(0);
  const [showJump, setShowJump] = useState(false);
  const [panel, setPanel] = useState<PanelView | null>(null);
  const panelOpener = useRef<HTMLElement | null>(null);
  const live = LIVE.includes(session.state);
  const stage = session.stage || 'active';
  const stageBlocked = live || needsYou(session) || (session.queued ?? 0) > 0;
  const stageReason = stageBlocked ? 'Stop the turn, resolve pending requests and clear queued prompts before settling or archiving.' : '';
  const working = session.state === 'working' || session.state === 'starting';
  const scroller = useRef<HTMLDivElement>(null);
  const atBottom = useRef(true);

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

  useLayoutEffect(() => {
    atBottom.current = true;
    setShowJump(false);
    setPanel(null);
  }, [session.id]);

  // Follow new content only while the reader is at the bottom; otherwise offer a way back.
  useLayoutEffect(() => {
    const el = scroller.current;
    if (!el) return;
    if (atBottom.current) el.scrollTop = el.scrollHeight;
    else if (el.scrollHeight - el.scrollTop - el.clientHeight > BOTTOM_SLACK) setShowJump(true);
  }, [session.id, session.items, session.interactions]);

  // One side panel at a time: opening the Changes sheet closes the subagent panel and vice versa.
  useEffect(() => {
    if (sheetOpen) setPanel(null);
  }, [sheetOpen]);

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

  // Esc closes the panel; native dialogs handle their own Esc.
  useEffect(() => {
    if (!panel) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !document.querySelector('dialog[open]')) closePanel();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [panel, closePanel]);

  /** Scroll the transcript to the `task` row that spawned a subagent and flash it. */
  function locate(toolCallId: string) {
    const el = document.getElementById(`item-${toolCallId}`);
    if (!el) return;
    el.scrollIntoView({ block: 'center' });
    el.classList.add('flash');
    window.setTimeout(() => el.classList.remove('flash'), 1400);
  }

  function onScroll() {
    const el = scroller.current;
    if (!el) return;
    atBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < BOTTOM_SLACK;
    if (atBottom.current) setShowJump(false);
  }

  async function run(op: () => Promise<SessionSummary>) {
    setError(null);
    setBusy(true);
    try {
      onSessionUpdate(await op());
    } catch (e) {
      setError(describeError(e));
    } finally { setBusy(false); }
  }

  const name = taskName(session);
  const fileCount = changes?.supported ? changes.files.length : null;
  const detail = session.state_detail && session.state !== 'failed' ? session.state_detail : undefined;
  const agentsRunning = session.subagents.filter((s) => s.status === 'running').length;

  return (
    <div className="pane">
      <div className="pane-main">
        <header className="main-header">
          {leading}
          <h1 className="task-title" title={name || undefined}>
            <TaskTitle session={session} />
          </h1>
          {readOnly(session) ? <span className="chip">{stageLabel(session)}</span> : <StateMark state={session.state} title={detail} />}
          <span className="spacer" />
          {session.subagents.length > 0 && (
            <button
              type="button"
              id="subagents-link"
              className="btn btn-ghost btn-sm"
              aria-pressed={!!panel}
              onClick={(e) => (panel ? closePanel() : openPanel({ view: 'list' }, e.currentTarget))}
            >
              <span className="changes-label">Subagents</span>
              <span className="count num">
                {session.subagents.length}
                <span className="sr-only"> subagents</span>
              </span>
              {agentsRunning > 0 && (
                <>
                  <Spinner />
                  <span className="count num">{agentsRunning} running</span>
                </>
              )}
            </button>
          )}
          <button type="button" id="changes-link" className="btn btn-ghost btn-sm" aria-pressed={sheetOpen} onClick={() => onSheet(!sheetOpen)}>
            <span className={fileCount === null ? 'changes-label changes-label-only' : 'changes-label'}>Changes</span>
            {fileCount !== null && (
              <span className="count num">
                {fileCount}
                <span className="sr-only"> {fileCount === 1 ? 'file' : 'files'} changed</span>
              </span>
            )}
          </button>
          <Menu label="Task actions">
            <button type="button" role="menuitem" className="menu-item" disabled={busy || stage === 'archived'} onClick={() => setDialog('rename')}>Rename</button>
            {stage === 'active' && session.open && <button type="button" role="menuitem" className="menu-item" disabled={busy} onClick={() => setDialog('close')}>Close</button>}
            {stage === 'active' && <button type="button" role="menuitem" className="menu-item" disabled={busy || stageBlocked} title={stageReason}
              onClick={() => void run(() => api.stage(session.id, 'settle'))}>Settle task</button>}
            {stage === 'settled' && <button type="button" role="menuitem" className="menu-item" disabled={busy}
              onClick={() => void run(() => api.stage(session.id, 'reopen'))}>Reopen task</button>}
            {stage !== 'archived' && <button type="button" role="menuitem" className="menu-item" disabled={busy || (stage === 'active' && stageBlocked)} title={stageReason}
              onClick={() => setDialog('archive')}>Archive task</button>}
            {stage === 'archived' && <button type="button" role="menuitem" className="menu-item menu-item-danger" onClick={() => setDialog('delete')}>Delete task</button>}
            {stage === 'active' && stageReason && <p className="caption menu-help">{stageReason}</p>}
          </Menu>
        </header>
        {session.context && (
          <div className="context-usage task-context">
            <label htmlFor="context-meter" className="caption num">
              Context:{' '}
              <span aria-hidden="true">
                {session.context.used.toLocaleString(undefined, { notation: 'compact', maximumFractionDigits: 1 })}
                {' / '}
                {session.context.limit.toLocaleString(undefined, { notation: 'compact', maximumFractionDigits: 1 })} tokens
              </span>
              <span className="sr-only">{session.context.used.toLocaleString()} of {session.context.limit.toLocaleString()} tokens used</span>
            </label>
            <meter id="context-meter" min={0} max={session.context.limit || 1} value={session.context.used}
              aria-valuetext={`${session.context.used.toLocaleString()} of ${session.context.limit.toLocaleString()} tokens used`} />
          </div>
        )}

        <div className="pane-body" ref={scroller} onScroll={onScroll}>
          <div className="column" role="log">
            {busy && <p className="caption" role="status">Updating task…</p>}
            {error && (
              <p className="notice-line notice-error" role="alert">
                {error}
              </p>
            )}
            {session.history_truncated && <p className="notice-line">Earlier history was truncated; only the most recent part is shown.</p>}
            {session.items.length === 0 && session.state === 'idle' && !readOnly(session) && (
              <p className="notice-line">Your first message gives this task its title.</p>
            )}
            <Transcript
              items={session.items}
              subagents={session.subagents}
              live={live}
              working={working}
              provider={session.provider}
              model={session.last_model || session.model}
              onOpenAgent={(id, opener) => openPanel({ view: 'agent', id }, opener)}
            />
            {session.interactions.map((i) => (
              <InteractionCard key={i.id} session={session} interaction={i} onUpdate={(next) => onInteractionUpdate(session.id, next)} />
            ))}
            {session.state === 'interrupted' && <p className="notice-line notice-warning">{INTERRUPTED_TEXT}</p>}
            {session.state === 'failed' && (
              <p className="notice-line notice-error" role="alert">
                Turn failed{session.state_detail ? `: ${session.state_detail}` : '.'}
              </p>
            )}
          </div>
        </div>

        <div className="pane-foot">
          <div className="column">
            {showJump && (
              <button type="button" className="btn btn-secondary btn-sm jump" onClick={scrollToBottom}>
                ↓ New output
              </button>
            )}
            <Composer key={session.id} session={session} onSessionUpdate={onSessionUpdate} />
          </div>
        </div>
      </div>

      {sheetOpen && (
        <ChangesSheet
          session={session}
          projectName={project?.name ?? 'Project'}
          changes={changes}
          inline={sidePanelInline}
          onRefresh={() => setChangesTick((t) => t + 1)}
          onClose={() => onSheet(false)}
        />
      )}
      {panel && !sidePanelInline && <button type="button" className="scrim" aria-label="Close subagents" onClick={closePanel} />}
      {panel && (
        <SubagentPanel
          session={session}
          agents={agents}
          snapshotSeq={snapshotSeq}
          view={panel}
          inline={sidePanelInline}
          onView={setPanel}
          onClose={closePanel}
          onLocate={locate}
        />
      )}
      {dialog === 'rename' && (
        <NameDialog
          title="Rename task"
          label="Name"
          initial={session.name}
          hint="Leave it empty to show the provider's title again."
          allowEmpty
          onSubmit={async (name) => onSessionUpdate(await api.rename(session.id, name))}
          onClose={() => setDialog(null)}
        />
      )}
      {dialog === 'close' && (
        <ConfirmDialog
          title="Close this task?"
          confirmLabel="Close task"
          danger={false}
          onConfirm={async () => onSessionUpdate(await api.close(session.id))}
          onClose={() => setDialog(null)}
        >
          <p>
            Closing disconnects the provider conversation. The task and its conversation ID are kept, so nothing is deleted;
            sending another prompt reopens the same conversation.
          </p>
          <p className="caption">To interrupt the current turn without closing, use Stop turn instead.</p>
        </ConfirmDialog>
      )}
      {dialog === 'archive' && (
        <ConfirmDialog title="Archive this task?" confirmLabel="Archive task" danger={false}
          onConfirm={async () => onSessionUpdate(await api.stage(session.id, 'archive'))} onClose={() => setDialog(null)}>
          <p>Archiving makes this task permanently read-only. It stays visible with its recorded conversation. It cannot be reopened.</p>
        </ConfirmDialog>
      )}
      {dialog === 'delete' && (
        <ConfirmDialog
          title="Delete this task?"
          confirmLabel="Delete task"
          onConfirm={async () => {
            await api.deleteSession(session.id);
            onDeleted(session.id);
          }}
          onClose={() => setDialog(null)}
        >
          <p>This removes the task record from UAM. The provider conversation on the host is untouched.</p>
        </ConfirmDialog>
      )}
    </div>
  );
}
