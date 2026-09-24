import { useEffect, useLayoutEffect, useRef, useState, type RefObject } from 'react';
import {
  LIVE,
  api,
  describeError,
  isStatus,
  modelName,
  providerLabel,
  type Changes as ChangesData,
  type Interaction,
  type Project,
  type SessionDetail,
  type SessionSummary,
} from '../api';
import type { AgentTranscript } from '../state';
import { ChangesSheet } from './Changes';
import { ConfirmDialog, INTERRUPTED_TEXT, NameDialog, StateMark, TaskTitle, useApp } from './common';
import { Composer } from './Composer';
import { InteractionCard } from './Interactions';
import { Transcript } from './Transcript';

interface Props {
  session: SessionDetail;
  project: Project | undefined;
  agents: Record<string, AgentTranscript>;
  snapshotSeq: number;
  sheetOpen: boolean;
  onSheet: (open: boolean) => void;
  onSessionUpdate: (s: SessionSummary) => void;
  onDeleted: (id: string) => void;
  onInteractionUpdate: (sessionId: string, i: Interaction) => void;
  /** The scrolling element; the composer sticks to its bottom. */
  scroller: RefObject<HTMLDivElement | null>;
}

/** The reading column: kicker, serif title, meta line of text links, transcript, cards, composer. */
export function Task({
  session,
  project,
  agents,
  snapshotSeq,
  sheetOpen,
  onSheet,
  onSessionUpdate,
  onDeleted,
  onInteractionUpdate,
  scroller,
}: Props) {
  const { meta } = useApp();
  const [dialog, setDialog] = useState<'rename' | 'close' | 'delete' | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [changes, setChanges] = useState<ChangesData | null>(null);
  const [changesTick, setChangesTick] = useState(0);
  const live = LIVE.includes(session.state);
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

  useLayoutEffect(() => {
    atBottom.current = true;
  }, [session.id]);

  useLayoutEffect(() => {
    const el = scroller.current;
    if (el && atBottom.current) el.scrollTop = el.scrollHeight;
  }, [session.id, session.items, session.interactions, agents, scroller]);

  useEffect(() => {
    const el = scroller.current;
    if (!el) return;
    const onScroll = () => {
      atBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 24;
    };
    el.addEventListener('scroll', onScroll);
    return () => el.removeEventListener('scroll', onScroll);
  }, [scroller]);

  async function run(op: () => Promise<SessionSummary>) {
    setError(null);
    try {
      onSessionUpdate(await op());
    } catch (e) {
      setError(describeError(e));
    }
  }

  const model = modelName(meta, session.provider, session.model);
  const routed = session.last_model && session.last_model !== session.model ? modelName(meta, session.provider, session.last_model) : null;
  const fileCount = changes?.supported ? changes.files.length : null;

  return (
    <>
      <article className="column">
        <div className="kicker">
          <span>{project?.name ?? 'Project'}</span>
          <span aria-hidden="true">·</span>
          <span>{providerLabel(meta, session.provider)}</span>
        </div>
        <h1 className="display title">
          <TaskTitle session={session} />
        </h1>
        <div className="meta">
          <StateMark state={session.state} />
          <span className="muted" title={routed ? 'The latest turn reported a different model than the one selected' : undefined}>
            {routed ? `${model} → ${routed}` : model}
          </span>
          {session.subagents_running > 0 && (
            <span className="muted">
              {session.subagents_running} subagent{session.subagents_running === 1 ? '' : 's'} running
            </span>
          )}
          <button type="button" id="changes-link" className="link" aria-pressed={sheetOpen} onClick={() => onSheet(!sheetOpen)}>
            {fileCount === null ? 'Changes' : `${fileCount} ${fileCount === 1 ? 'file' : 'files'} changed`}
          </button>
          <button type="button" className="link" onClick={() => setDialog('rename')}>
            Rename
          </button>
          {session.open && (
            <button type="button" className="link" onClick={() => setDialog('close')}>
              Close
            </button>
          )}
          <button type="button" className="link" onClick={() => setDialog('delete')}>
            Delete
          </button>
          {session.state_detail && session.state !== 'failed' && <span className="muted">{session.state_detail}</span>}
        </div>
        {error && (
          <p className="error" role="alert">
            {error}
          </p>
        )}
        {session.history_truncated && <p className="notice">Earlier history was truncated; only the most recent part is shown.</p>}
        <Transcript
          sessionId={session.id}
          items={session.items}
          subagents={session.subagents}
          agents={agents}
          snapshotSeq={snapshotSeq}
          live={live}
        />
        {session.interactions.map((i) => (
          <InteractionCard key={i.id} session={session} interaction={i} onUpdate={(next) => onInteractionUpdate(session.id, next)} />
        ))}
        {session.state === 'interrupted' && <p className="notice">{INTERRUPTED_TEXT}</p>}
        {session.state === 'failed' && (
          <p className="error" role="alert">
            Turn failed{session.state_detail ? `: ${session.state_detail}` : '.'}
          </p>
        )}
      </article>
      <div className="composer-wrap">
        <Composer session={session} onSessionUpdate={onSessionUpdate} />
      </div>

      {sheetOpen && (
        <ChangesSheet
          session={session}
          projectName={project?.name ?? 'Project'}
          changes={changes}
          onRefresh={() => setChangesTick((t) => t + 1)}
          onClose={() => onSheet(false)}
        />
      )}
      {dialog === 'rename' && (
        <NameDialog
          title="Rename task"
          label="Name"
          initial={session.name}
          hint="Leave it empty to show the provider's title again."
          allowEmpty
          onSubmit={(name) => run(() => api.rename(session.id, name))}
          onClose={() => setDialog(null)}
        />
      )}
      {dialog === 'close' && (
        <ConfirmDialog
          title="Close this task?"
          confirmLabel="Close task"
          danger={false}
          onConfirm={() => run(() => api.close(session.id))}
          onClose={() => setDialog(null)}
        >
          <p>
            Closing disconnects the provider conversation. The task and its conversation ID are kept, so nothing is deleted;
            sending another prompt reopens the same conversation.
          </p>
          <p className="muted small">To interrupt the current turn without closing, use Stop turn instead.</p>
        </ConfirmDialog>
      )}
      {dialog === 'delete' && (
        <ConfirmDialog
          title="Delete this task?"
          confirmLabel="Delete task"
          onConfirm={async () => {
            try {
              await api.deleteSession(session.id);
            } catch (e) {
              if (isStatus(e, 409)) throw new Error('The task is still busy. Wait for it to finish or stop its turn first.');
              throw e;
            }
            onDeleted(session.id);
          }}
          onClose={() => setDialog(null)}
        >
          <p>This removes the task record from UAM. The provider conversation on the host is untouched.</p>
        </ConfirmDialog>
      )}
    </>
  );
}
