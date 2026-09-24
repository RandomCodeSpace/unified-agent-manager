import { useRef, useState, type KeyboardEvent } from 'react';
import {
  LIVE,
  api,
  describeError,
  isStatus,
  modelCatalog,
  modelName,
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

/**
 * The composer: textarea on top, a 28px control row below with the model select at the
 * left and Stop/Send at the right. The middle of that row is left free for the steer and
 * queue controls that are still being designed.
 */
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
  const routed = session.last_model && session.last_model !== session.model ? modelName(meta, session.provider, session.last_model) : null;

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
  const hint = note || (last?.status === 'accepted' && !blocked ? `Last prompt accepted ${new Date(last.time).toLocaleTimeString()}` : '');
  const canStop = session.capabilities.cancel;

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
        placeholder={blocked ?? `Message ${session.provider}… (Enter to send, Shift+Enter for a new line)`}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={onKeyDown}
        disabled={busy || !!blocked}
      />
      <div className="composer-bar">
        {models.length > 0 && (
          <span className="select-wrap">
            <span className="select-face" aria-hidden="true">
              {models.find((m) => m.id === session.model)?.name ?? session.model}
              <span className="chev-down">▾</span>
            </span>
            <label htmlFor="composer-model" className="sr-only">
              Model
            </label>
            <select
              id="composer-model"
              className="select"
              value={session.model}
              disabled={live}
              title={live ? 'The model can change between turns, not during one.' : 'Model for the next turn'}
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
        {routed && (
          <span className="caption mono composer-routed" title="The latest turn reported a different model than the one selected">
            → {routed}
          </span>
        )}
        <span className="spacer" />
        {hint && <span className="composer-hint">{hint}</span>}
        {live && !needsYou(session) && (
          <button
            type="button"
            className="btn btn-secondary btn-square"
            aria-label="Stop turn"
            title={canStop ? 'Stop turn' : 'This provider cannot cancel a turn'}
            disabled={!canStop}
            onClick={() => void stop()}
          >
            <span aria-hidden="true">■</span>
          </button>
        )}
        <button
          type="submit"
          className="btn btn-primary btn-square"
          aria-label={busy ? 'Sending…' : 'Send'}
          title="Send (Enter)"
          disabled={busy || !!blocked || !text.trim()}
        >
          <span aria-hidden="true">↑</span>
        </button>
      </div>
    </form>
  );
}
