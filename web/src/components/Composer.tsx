import { useRef, useState, type KeyboardEvent } from 'react';
import {
  LIVE,
  api,
  describeError,
  isStatus,
  modelCatalog,
  needsYou,
  newRequestId,
  type SessionDetail,
  type SessionSummary,
} from '../api';
import { useApp } from './common';

function blockReason(s: SessionDetail): string | null {
  if (needsYou(s)) return 'Waiting for your decision above.';
  switch (s.state) {
    case 'starting':
      return 'The task is starting.';
    case 'working':
      return 'A turn is running. Wait for it to finish or use Stop turn.';
    default:
      return null;
  }
}

function footNote(s: SessionDetail, blocked: string | null): string {
  if (blocked) return 'Work continues on the server when you close this page.';
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

export function Composer({ session, onSessionUpdate }: { session: SessionDetail; onSessionUpdate: (s: SessionSummary) => void }) {
  const { meta } = useApp();
  const [text, setText] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // request_id is minted once per composed text and reused on retry of that same text.
  const pending = useRef<{ text: string; id: string } | null>(null);
  const blocked = blockReason(session);
  const live = LIVE.includes(session.state);
  const last = session.last_submission;
  const catalog = modelCatalog(meta, session.provider);
  const models = catalog.some((m) => m.id === session.model) ? catalog : [{ id: session.model, name: session.model || 'Default model' }, ...catalog];

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
      if (isStatus(e, 409)) setError('A turn is already running. Wait for it to finish.');
      else setError(`${describeError(e)}. Sending again retries the same request (${id.slice(0, 8)}).`);
    } finally {
      setBusy(false);
    }
  }

  async function changeModel(model: string) {
    setError(null);
    try {
      onSessionUpdate(await api.setModel(session.id, model));
    } catch (e) {
      if (isStatus(e, 409)) setError('The model can change between turns, not during one.');
      else setError(describeError(e));
    }
  }

  async function stop() {
    setError(null);
    try {
      onSessionUpdate(await api.cancel(session.id));
    } catch (e) {
      setError(describeError(e));
    }
  }

  function onKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      void send();
    }
  }

  const note = footNote(session, blocked);

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
      {last?.status === 'rejected' && <p className="error">Last prompt rejected{last.error ? `: ${last.error}` : '.'}</p>}
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
        className="composer-text"
        rows={2}
        value={text}
        placeholder={blocked ?? 'Message the agent'}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={onKeyDown}
        disabled={busy || !!blocked}
      />
      <div className="composer-foot">
        {models.length > 0 && (
          <span className="control">
            <label htmlFor="composer-model" className="control-label">
              Model
            </label>
            <select
              id="composer-model"
              className="select"
              value={session.model}
              disabled={live}
              title={live ? 'The model can change between turns, not during one.' : 'Applies from the next turn.'}
              onChange={(e) => void changeModel(e.target.value)}
            >
              {models.map((m) => (
                <option key={m.id} value={m.id}>
                  {m.name}
                </option>
              ))}
            </select>
          </span>
        )}
        {note && <span className="muted small composer-note">{note}</span>}
        <span className="spacer" />
        {last?.status === 'accepted' && !blocked && (
          <span className="muted small composer-hint">Last prompt accepted {new Date(last.time).toLocaleTimeString()}</span>
        )}
        {!blocked && !last && <span className="muted small composer-hint">Enter to send · Shift+Enter for a new line</span>}
        {live && !needsYou(session) && (
          <button
            type="button"
            className="pill pill-outline"
            disabled={!session.capabilities.cancel}
            title={session.capabilities.cancel ? undefined : 'This provider cannot cancel a turn'}
            onClick={() => void stop()}
          >
            Stop turn
          </button>
        )}
        <button type="submit" className="pill pill-primary" disabled={busy || !!blocked || !text.trim()}>
          {busy ? 'Sending…' : 'Send'}
        </button>
      </div>
    </form>
  );
}
