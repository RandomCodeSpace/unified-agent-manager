import { useRef, useState, type KeyboardEvent } from 'react';
import { LIVE, api, describeError, modelCatalog, modelName, newRequestId, readOnly, type PromptMode, type SessionDetail, type SessionSummary, type Submission } from '../api';
import { useApp } from './common';
import { GenerationSettings } from './GenerationSettings';

export function Composer({ session, onSessionUpdate }: { session: SessionDetail; onSessionUpdate: (s: SessionSummary) => void }) {
  const { meta } = useApp();
  const [text, setText] = useState('');
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [outcome, setOutcome] = useState<Submission | null>(null);
  const pending = useRef<{ text: string; id: string } | null>(null);
  const live = LIVE.includes(session.state);
  const locked = readOnly(session);
  const last = outcome && (!session.last_submission || outcome.time >= session.last_submission.time) ? outcome : session.last_submission;
  const catalog = modelCatalog(meta, session.provider);
  const models = catalog.some((m) => m.id === session.model) ? catalog : [{ id: session.model, name: session.model || 'Default model' }, ...catalog];
  const selectedModel = catalog.find((m) => m.id === session.model);
  const routed = session.last_model && session.last_model !== session.model ? modelName(meta, session.provider, session.last_model) : null;
  const queue = session.queue ?? [];
  const cannotSubmit = !!busy || locked || session.state === 'starting' || !text.trim();

  async function send(mode: PromptMode) {
    const t = text.trim();
    if (cannotSubmit || (mode === 'send' && live)) return;
    if (!pending.current || pending.current.text !== t) pending.current = { text: t, id: newRequestId() };
    const id = pending.current.id;
    setBusy(mode);
    setError(null);
    try {
      const sub = await api.prompt(session.id, t, id, mode);
      setOutcome(sub);
      pending.current = null;
      if (sub.status === 'accepted' || sub.status === 'queued') setText('');
    } catch (e) {
      setError(`${describeError(e)}. Nothing will be retried automatically. Repeating this action with unchanged text uses the same request (${id.slice(0, 8)}).`);
    } finally { setBusy(null); }
  }

  async function action(label: string, op: () => Promise<unknown>) {
    if (busy) return;
    setBusy(label);
    setError(null);
    try { await op(); } catch (e) { setError(describeError(e)); } finally { setBusy(null); }
  }
  const settings = (body: Parameters<typeof api.settings>[1]) => action('settings', async () => onSessionUpdate(await api.settings(session.id, body)));
  function onKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      void send(e.ctrlKey || e.metaKey ? 'steer' : live ? 'queue' : 'send');
    }
  }

  return <form className="composer" onSubmit={(e) => { e.preventDefault(); void send(live ? 'queue' : 'send'); }}>
    {locked && <p className="caption">{session.stage === 'settled' ? 'Settled. Reopen this task to continue the same conversation.' : 'Archived. This task is read-only.'}</p>}
    {last?.status === 'uncertain' && <p className="warn" role="alert">The provider may or may not have received your last prompt. Check the conversation before sending again; it will not be resent automatically.</p>}
    {last?.status === 'rejected' && <p className="error" role="alert">Last prompt rejected{last.error ? `: ${last.error}` : '.'}</p>}
    {error && <p className="error" role="alert">{error}</p>}
    {queue.length > 0 && <details className="prompt-queue" open>
      <summary>{queue.length} queued · {session.queue_paused ? 'Paused' : 'Waiting for the current turn'}</summary>
      <ol>{queue.map((q) => <li key={q.request_id}>
        <span>{q.text}</span>
        <button type="button" className="btn btn-ghost btn-sm" aria-label={`Cancel queued prompt: ${q.text}`} disabled={!!busy || locked}
          onClick={() => void action('queue', () => api.cancelQueued(session.id, q.request_id))}>Cancel</button>
      </li>)}</ol>
      <div className="actions actions-start">
        {session.queue_paused && <button type="button" className="btn btn-secondary btn-sm" disabled={!!busy || locked} onClick={() => void action('queue', () => api.queueAction(session.id, 'resume'))}>Resume queue</button>}
        <button type="button" className="btn btn-ghost btn-sm" disabled={!!busy || locked} onClick={() => void action('queue', () => api.queueAction(session.id, 'clear'))}>Clear queue</button>
      </div>
    </details>}
    <label className="sr-only" htmlFor="composer-text">Message</label>
    <textarea id="composer-text" className="composer-text" rows={2} value={text}
      placeholder={live ? 'Queue a follow-up, or steer this turn…' : 'Message the agent…'}
      onChange={(e) => setText(e.target.value)} onKeyDown={onKeyDown} disabled={!!busy || locked} />
    <div className="generation-settings">
      <label className="control generation-control" htmlFor="composer-model"><span className="control-label">Model</span>
        <select id="composer-model" className="input mono" value={session.model} disabled={live || !!busy || locked}
          onChange={(e) => void settings({ model: e.target.value })}>
          {models.map((m) => <option key={m.id} value={m.id}>{m.name}</option>)}
        </select>
      </label>
      <GenerationSettings prefix="composer" model={selectedModel} effort={session.effort ?? ''} contextSize={session.context_size ?? 'default'}
        contextSupported={!!session.capabilities.context_size} disabled={live || !!busy || locked} onChange={(value) => void settings(value)} />
    </div>
    {live && <p className="caption settings-note">Model, effort and context size can change between turns.</p>}
    {routed && <p className="caption mono">Latest turn: {routed}</p>}
    <div className="composer-bar">
      <label className="mode-control">Mode
        <select id="composer-mode" className="input" value={session.mode ?? 'safe'} disabled={!!busy || locked}
          onChange={(e) => void settings({ mode: e.target.value as 'safe' | 'yolo' })}>
          <option value="safe">Safe</option><option value="yolo">Yolo</option>
        </select>
      </label>
      <span className="spacer" />
      {live && <button type="button" className="btn btn-secondary" disabled={!!busy || locked || !session.capabilities.cancel}
        title={!session.capabilities.cancel ? 'This provider cannot cancel a turn' : 'Stop turn and pause queued follow-ups'}
        onClick={() => void action('stop', async () => onSessionUpdate(await api.cancel(session.id)))}>Stop turn</button>}
      {live && <button type="button" className="btn btn-secondary" disabled={cannotSubmit} title="Steer this turn (Ctrl+Enter)" onClick={() => void send('steer')}>{busy === 'steer' ? 'Steering…' : 'Steer'}</button>}
      {!live && queue.length > 0 && <button type="button" className="btn btn-secondary" disabled={cannotSubmit} onClick={() => void send('queue')}>Queue</button>}
      <button type="submit" className="btn btn-primary" disabled={cannotSubmit}>{busy === 'send' || busy === 'queue' ? 'Submitting…' : live ? 'Queue' : 'Send'}</button>
    </div>
    <p className="caption settings-note">{session.mode === 'yolo' ? 'Yolo allows permission requests automatically. Questions and managed-policy requests still need you.' : 'Safe asks before allowing permission requests.'}</p>
    {live && <p className="caption settings-note">Enter queues a follow-up. Ctrl+Enter steers the current turn. Shift+Enter adds a line.</p>}
    {busy && <span className="caption" role="status">Updating {busy}…</span>}
  </form>;
}
