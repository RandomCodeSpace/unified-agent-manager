import { memo, useLayoutEffect, useRef, useState, type FormEvent, type KeyboardEvent } from 'react';
import {
  api,
  ApiError,
  describeError,
  newRequestId,
  providerLabel,
  sessionName,
  type Interaction,
  type Item,
  type Meta,
  type SessionDetail,
  type SessionState,
  type SessionSummary,
} from '../api';
import { Dialog, INTERRUPTED_TEXT, Markdown, Sep, StateBadge } from './common';
import { InteractionCard } from './Interactions';

interface Props {
  session: SessionDetail;
  meta: Meta | null;
  onSessionUpdate: (s: SessionSummary) => void;
  onInteractionUpdate: (sessionId: string, i: Interaction) => void;
}

const ACTIVE: SessionState[] = ['working', 'awaiting_permission', 'awaiting_answer'];
/** States in which the provider still holds the turn, so tool results may still arrive. */
const LIVE: SessionState[] = ['starting', ...ACTIVE];

export function Conversation({ session, meta, onSessionUpdate, onInteractionUpdate }: Props) {
  const scroller = useRef<HTMLDivElement>(null);
  const atBottom = useRef(true);

  useLayoutEffect(() => {
    atBottom.current = true;
  }, [session.id]);

  useLayoutEffect(() => {
    const el = scroller.current;
    if (el && atBottom.current) el.scrollTop = el.scrollHeight;
  }, [session.id, session.items, session.interactions]);

  function onScroll() {
    const el = scroller.current;
    if (el) atBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 24;
  }

  return (
    <>
      <SessionHeader session={session} meta={meta} onSessionUpdate={onSessionUpdate} />
      <div className="transcript" ref={scroller} onScroll={onScroll}>
        {session.history_truncated && (
          <p className="notice-line">Earlier history was truncated; only the most recent part is shown.</p>
        )}
        {session.items.map((item) => (
          <ItemView key={item.id} item={item} live={LIVE.includes(session.state)} />
        ))}
        {session.interactions.map((i) => (
          <InteractionCard
            key={i.id}
            session={session}
            interaction={i}
            onUpdate={(next) => onInteractionUpdate(session.id, next)}
          />
        ))}
        {session.state === 'interrupted' && <p className="notice-line">{INTERRUPTED_TEXT}</p>}
        {session.state === 'failed' && (
          <p className="error" role="alert">
            Turn failed{session.state_detail ? `: ${session.state_detail}` : '.'}
          </p>
        )}
      </div>
      <Composer session={session} />
    </>
  );
}

function SessionHeader({
  session,
  meta,
  onSessionUpdate,
}: {
  session: SessionDetail;
  meta: Meta | null;
  onSessionUpdate: (s: SessionSummary) => void;
}) {
  const [renaming, setRenaming] = useState(false);
  const [closing, setClosing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const active = ACTIVE.includes(session.state);

  async function run(op: () => Promise<SessionSummary>) {
    setError(null);
    try {
      onSessionUpdate(await op());
    } catch (e) {
      setError(describeError(e));
    }
  }

  return (
    <header className="conv-head">
      <div className="conv-title">
        <h2>{sessionName(session)}</h2>
        <button type="button" className="btn small" onClick={() => setRenaming(true)}>
          Rename
        </button>
        <span className="spacer" />
        {active && (
          <button
            type="button"
            className="btn"
            disabled={!session.capabilities.cancel}
            title={session.capabilities.cancel ? undefined : 'This provider cannot cancel a turn'}
            onClick={() => run(() => api.cancel(session.id))}
          >
            Stop turn
          </button>
        )}
        {session.open && (
          <button type="button" className="btn" onClick={() => setClosing(true)}>
            Close session
          </button>
        )}
      </div>
      <div className="conv-meta">
        <span>{providerLabel(meta, session.provider)}</span>
        <span className="mono" title={session.workdir}>
          {session.workdir}
        </span>
        <StateBadge state={session.state} />
        {session.state_detail && session.state !== 'failed' && <span className="muted">{session.state_detail}</span>}
      </div>
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      {renaming && (
        <RenameDialog
          name={session.name}
          onClose={() => setRenaming(false)}
          onSubmit={async (name) => {
            await run(() => api.rename(session.id, name));
            setRenaming(false);
          }}
        />
      )}
      {closing && (
        <Dialog title="Close session?" onClose={() => setClosing(false)}>
          <p>
            Closing disconnects the provider conversation. The session record and its conversation ID are kept, so
            nothing is deleted. Running work in other sessions is not affected.
          </p>
          <p className="muted">To interrupt the current turn without closing, use Stop turn instead.</p>
          <div className="actions">
            <button type="button" className="btn" onClick={() => setClosing(false)} autoFocus>
              Keep open
            </button>
            <button
              type="button"
              className="btn danger"
              onClick={() => {
                setClosing(false);
                void run(() => api.close(session.id));
              }}
            >
              Close session
            </button>
          </div>
        </Dialog>
      )}
    </header>
  );
}

function RenameDialog({
  name,
  onClose,
  onSubmit,
}: {
  name: string;
  onClose: () => void;
  onSubmit: (name: string) => Promise<void>;
}) {
  const [value, setValue] = useState(name);
  const [busy, setBusy] = useState(false);
  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      await onSubmit(value.trim());
    } finally {
      setBusy(false);
    }
  }
  return (
    <Dialog title="Rename session" onClose={onClose}>
      <form className="form" onSubmit={submit}>
        <label>
          Name
          <input type="text" required autoFocus value={value} onChange={(e) => setValue(e.target.value)} />
        </label>
        <div className="actions">
          <button type="button" className="btn" onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className="btn primary" disabled={busy || !value.trim()}>
            Save
          </button>
        </div>
      </form>
    </Dialog>
  );
}

const ItemView = memo(function ItemView({ item, live }: { item: Item; live: boolean }) {
  switch (item.kind) {
    case 'user':
      return (
        <article className="item user">
          <div className="item-label">You</div>
          <div className="plain">{item.text}</div>
        </article>
      );
    case 'assistant':
      return (
        <article className="item assistant">
          <div className="item-label">Assistant</div>
          <Markdown text={item.text ?? ''} />
        </article>
      );
    case 'reasoning':
      return (
        <details className="item reasoning">
          <summary>Reasoning</summary>
          <div className="plain muted">{item.text}</div>
        </details>
      );
    case 'notice':
      return <p className="item notice-line">{item.text}</p>;
    case 'tool': {
      const t = item.tool;
      const status = t?.status ?? 'pending';
      // Display only: a tool still pending/running after the turn ended never reported a result.
      const ended = !live && (status === 'pending' || status === 'running');
      return (
        <details className="item tool">
          <summary>
            <span className="tool-name">{t?.title || t?.name || 'Tool'}</span>
            <Sep />
            <span
              className={`tool-status tool-${ended ? 'ended' : status}`}
              title={ended ? 'The turn ended before this tool reported a result' : undefined}
            >
              {ended ? 'ended' : status}
            </span>
          </summary>
          {item.text && <div className="plain">{item.text}</div>}
          {t?.input && (
            <>
              <div className="item-label">Input</div>
              <pre>{t.input}</pre>
            </>
          )}
          {t?.output && (
            <>
              <div className="item-label">Output</div>
              <pre>{t.output}</pre>
            </>
          )}
        </details>
      );
    }
    default:
      return <p className="item plain">{item.text}</p>;
  }
});

function blockReason(s: SessionDetail): string | null {
  switch (s.state) {
    case 'starting':
      return 'The session is starting.';
    case 'working':
      return 'A turn is running. Wait for it to finish or use Stop turn.';
    case 'awaiting_permission':
      return 'The provider is waiting for a permission decision above.';
    case 'awaiting_answer':
      return 'The provider is waiting for your answer above.';
    default:
      return null;
  }
}

function footNote(s: SessionDetail, blocked: string | null): string {
  if (blocked) return `${blocked} Work continues on the server when you close this page.`;
  switch (s.state) {
    case 'failed':
    case 'closed':
    case 'interrupted':
    case 'cancelled':
      return 'Sending a prompt reopens the same provider conversation.';
    default:
      return '';
  }
}

function Composer({ session }: { session: SessionDetail }) {
  const [text, setText] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // request_id is minted once per composed text and reused on retry of that same text.
  const pending = useRef<{ text: string; id: string } | null>(null);
  const blocked = blockReason(session);
  const last = session.last_submission;

  async function send() {
    const t = text.trim();
    if (!t || busy || blocked) return;
    if (!pending.current || pending.current.text !== t) pending.current = { text: t, id: newRequestId() };
    const id = pending.current.id;
    setBusy(true);
    setError(null);
    try {
      const sub = await api.prompt(session.id, t, id);
      if (sub.status === 'accepted') {
        pending.current = null;
        setText('');
      } else {
        // Recorded as rejected/uncertain: the same request_id would replay that record,
        // so a deliberate resend gets a fresh ID. The text stays for the user to decide.
        pending.current = null;
        if (sub.status === 'rejected') setError(`The provider rejected the prompt${sub.error ? `: ${sub.error}` : '.'}`);
      }
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) setError('A turn is already running. Wait for it to finish.');
      else setError(`${describeError(e)}. Sending again retries the same request (${id.slice(0, 8)}).`);
    } finally {
      setBusy(false);
    }
  }

  function onKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      void send();
    }
  }

  return (
    <form
      className="composer"
      onSubmit={(e) => {
        e.preventDefault();
        void send();
      }}
    >
      {last?.status === 'uncertain' && (
        <p className="warn" role="alert">
          The provider may or may not have received your last prompt. Check the conversation above before sending it
          again; it will not be resent automatically.
        </p>
      )}
      {last?.status === 'rejected' && (
        <p className="error">Last prompt rejected{last.error ? `: ${last.error}` : '.'}</p>
      )}
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      <label className="sr-only" htmlFor="composer-text">
        Message
      </label>
      <textarea
        id="composer-text"
        rows={3}
        value={text}
        placeholder="Message (Enter to send, Shift+Enter for a new line)"
        onChange={(e) => setText(e.target.value)}
        onKeyDown={onKeyDown}
        disabled={busy}
      />
      <div className="composer-foot">
        <span className="muted">{footNote(session, blocked)}</span>
        <span className="spacer" />
        {last?.status === 'accepted' && (
          <span className="muted">Last prompt accepted {new Date(last.time).toLocaleTimeString()}</span>
        )}
        <button type="submit" className="btn primary" disabled={busy || !!blocked || !text.trim()}>
          {busy ? 'Sending…' : 'Send'}
        </button>
      </div>
    </form>
  );
}
