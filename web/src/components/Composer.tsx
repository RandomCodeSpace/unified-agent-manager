import { ArrowUp, ChevronDown, Cpu, Gauge, Layers, ListEnd, ListPlus, Shield, ShieldOff, Square, X, Zap } from 'lucide-react';
import { useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import { LIVE, api, describeError, modelCatalog, modelName, newRequestId, readOnly, type Model, type PromptMode, type SessionDetail, type SessionSummary, type Submission } from '../api';
import { cn } from '../lib/cn';
import { Note, Spinner, useApp } from './common';
import { Button } from './ui/button';
import { Menu } from './ui/menu';
import { Tip } from './ui/tooltip';

export const MODE_TEXT = {
  safe: 'Asks before allowing permission requests.',
  yolo: 'Allows permission requests automatically. Questions and managed-policy requests still need you.',
} as const;

export const sizeLabel = (id: string) => (id === 'long_context' ? 'Long context' : id === 'default' ? 'Default' : id);

/** Why effort or context size cannot be chosen for this model and provider; empty when they can. */
export function effortReason(model?: Model): string {
  return (model?.efforts?.length ?? 0) === 0 ? 'This model uses its default effort.' : '';
}
export function contextReason(model: Model | undefined, supported: boolean): string {
  if (!supported) return 'Context size selection is unavailable for this provider.';
  return model?.context_sizes?.some((s) => s.id !== 'default') ? '' : 'This model uses its default context size.';
}

interface Choice {
  value: string;
  label: ReactNode;
  description?: ReactNode;
}

/**
 * One compact toolbar picker: the current value on a small ghost trigger, a radio menu to
 * change it. When the value cannot be changed the trigger stays visible, disabled, and
 * says why on hover and focus, so the rule is visible instead of a missing control.
 */
function Picker({
  id,
  icon,
  label,
  value,
  display,
  choices,
  disabled,
  reason,
  mono = false,
  onChange,
}: {
  id: string;
  icon: ReactNode;
  label: string;
  value: string;
  display: string;
  choices: Choice[];
  disabled: boolean;
  reason?: string;
  mono?: boolean;
  onChange: (value: string) => void;
}) {
  const face = (
    <>
      {icon}
      <span className={cn('max-w-36 truncate', mono && 'font-mono text-code-sm')}>{display}</span>
      <ChevronDown aria-hidden="true" className="!size-3 text-faint" />
    </>
  );
  if (disabled) {
    return (
      <Tip label={reason ?? `${label} cannot change now`}>
        <Button id={id} size="sm" variant="subtle" aria-disabled="true" aria-label={`${label}: ${display}. ${reason ?? ''}`} className="cursor-not-allowed text-muted opacity-60 hover:bg-transparent hover:text-muted">
          {face}
        </Button>
      </Tip>
    );
  }
  return (
    <Menu.Root modal={false}>
      <Tip label={label}>
        <Menu.Trigger render={<Button id={id} size="sm" variant="subtle" aria-label={`${label}: ${display}`} className="text-body" />}>{face}</Menu.Trigger>
      </Tip>
      <Menu.Content side="top" align="start" sideOffset={6} className="min-w-52">
        <Menu.RadioGroup value={value} onValueChange={(v) => onChange(v as string)}>
          <Menu.Label>{label}</Menu.Label>
          {choices.map((c) => (
            <Menu.RadioItem key={c.value} value={c.value} description={c.description} className={mono ? 'font-mono text-code-sm [&_.text-caption]:font-sans' : undefined}>
              {c.label}
            </Menu.RadioItem>
          ))}
        </Menu.RadioGroup>
      </Menu.Content>
    </Menu.Root>
  );
}

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
  const effort = session.effort ?? '';
  const contextSize = session.context_size || 'default';
  const sizes = selectedModel?.context_sizes ?? [];
  const noEffort = effortReason(selectedModel);
  const noContext = contextReason(selectedModel, !!session.capabilities.context_size);
  const settingsLocked = live || !!busy || locked;
  const mode = session.mode ?? 'safe';

  async function send(promptMode: PromptMode) {
    const t = text.trim();
    if (cannotSubmit || (promptMode === 'send' && live)) return;
    if (!pending.current || pending.current.text !== t) pending.current = { text: t, id: newRequestId() };
    const id = pending.current.id;
    setBusy(promptMode);
    setError(null);
    try {
      const sub = await api.prompt(session.id, t, id, promptMode);
      setOutcome(sub);
      pending.current = null;
      if (sub.status === 'accepted' || sub.status === 'queued') setText('');
    } catch (e) {
      setError(`${describeError(e)}. Nothing will be retried automatically. Repeating this action with unchanged text uses the same request (${id.slice(0, 8)}).`);
    } finally {
      setBusy(null);
    }
  }

  async function action(label: string, op: () => Promise<unknown>) {
    if (busy) return;
    setBusy(label);
    setError(null);
    try {
      await op();
    } catch (e) {
      setError(describeError(e));
    } finally {
      setBusy(null);
    }
  }
  const settings = (body: Parameters<typeof api.settings>[1]) => action('settings', async () => onSessionUpdate(await api.settings(session.id, body)));

  function onKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      void send(e.ctrlKey || e.metaKey ? 'steer' : live ? 'queue' : 'send');
    }
  }

  const sendLabel = busy === 'send' || busy === 'queue' ? 'Submitting…' : live ? 'Queue' : 'Send';

  return (
    <form
      className={cn(
        'flex flex-col rounded-md border border-hairline bg-raised shadow-raised transition-[border-color,box-shadow] duration-160 focus-within:border-hairline-strong focus-within:shadow-float',
        locked && 'bg-surface',
      )}
      onSubmit={(e) => {
        e.preventDefault();
        void send(live ? 'queue' : 'send');
      }}
    >
      {(locked || last?.status === 'uncertain' || last?.status === 'rejected' || error) && (
        <div className="flex flex-col gap-1 border-b border-hairline px-3.5 py-2">
          {locked && <Note>{session.stage === 'settled' ? 'Settled. Reopen this task to continue the same conversation.' : 'Archived. This task is read-only.'}</Note>}
          {last?.status === 'uncertain' && (
            <Note tone="warn" role="alert">
              The provider may or may not have received your last prompt. Check the conversation before sending again; it will not be resent automatically.
            </Note>
          )}
          {last?.status === 'rejected' && (
            <Note tone="error" role="alert">
              Last prompt rejected{last.error ? `: ${last.error}` : '.'}
            </Note>
          )}
          {error && (
            <Note tone="error" role="alert">
              {error}
            </Note>
          )}
        </div>
      )}
      {queue.length > 0 && (
        <details className="group/queue border-b border-hairline px-3.5 py-1.5" open>
          <summary className="flex h-6 list-none items-center gap-2 text-caption text-muted select-none [&::-webkit-details-marker]:hidden">
            <ListEnd aria-hidden="true" className="size-3.5" />
            <span className="tabular-nums">{queue.length} queued</span>
            <span aria-hidden="true">·</span>
            <span>{session.queue_paused ? 'Paused' : 'Waiting for the current turn'}</span>
            <span className="flex-1" />
            {session.queue_paused && (
              <Button size="sm" variant="secondary" className="h-6" disabled={!!busy || locked} onClick={() => void action('queue', () => api.queueAction(session.id, 'resume'))}>
                Resume
              </Button>
            )}
            <Button size="sm" variant="subtle" className="h-6 text-muted" disabled={!!busy || locked} onClick={() => void action('queue', () => api.queueAction(session.id, 'clear'))}>
              Clear
            </Button>
          </summary>
          <ol className="flex flex-col gap-0.5 pb-1">
            {queue.map((q, i) => (
              <li key={q.request_id} className="flex items-start gap-2 text-ui text-body">
                <span className="mt-0.5 w-4 shrink-0 text-right text-caption tabular-nums text-faint">{i + 1}</span>
                <span className="min-w-0 flex-1 truncate">{q.text}</span>
                <Button size="icon" variant="subtle" className="size-6 text-muted" aria-label={`Cancel queued prompt: ${q.text}`} disabled={!!busy || locked} onClick={() => void action('queue', () => api.cancelQueued(session.id, q.request_id))}>
                  <X />
                </Button>
              </li>
            ))}
          </ol>
        </details>
      )}

      <label className="sr-only" htmlFor="composer-text">
        Message
      </label>
      <textarea
        id="composer-text"
        rows={2}
        value={text}
        placeholder={locked ? '' : live ? 'Queue a follow-up, or steer this turn…' : 'Message the agent…'}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={onKeyDown}
        disabled={!!busy || locked}
        className={cn('max-h-[40dvh] min-h-14 w-full resize-none bg-transparent px-3.5 pt-3 pb-1 text-chat text-ink outline-hidden [field-sizing:content] disabled:text-muted max-sm:text-chat-lg', locked && 'min-h-0 h-2 pt-0')}
      />

      <div className="flex flex-wrap items-center gap-0.5 px-2 pt-1 pb-2">
        <Picker
          id="composer-model"
          icon={<Cpu aria-hidden="true" className="text-faint" />}
          label="Model"
          value={session.model}
          display={modelName(meta, session.provider, session.model)}
          mono
          choices={models.map((m) => ({ value: m.id, label: m.name }))}
          disabled={settingsLocked}
          reason={locked ? 'This task is read-only.' : live ? 'The model changes between turns.' : undefined}
          onChange={(v) => void settings({ model: v })}
        />
        <Picker
          id="composer-effort"
          icon={<Gauge aria-hidden="true" className="text-faint" />}
          label="Effort"
          value={effort}
          display={effort || 'Default'}
          choices={[{ value: '', label: 'Default', description: 'Leaves the choice to the provider' }, ...(selectedModel?.efforts ?? []).map((e) => ({ value: e, label: e }))]}
          disabled={settingsLocked || !!noEffort}
          reason={locked ? 'This task is read-only.' : live ? 'Effort changes between turns.' : noEffort || undefined}
          onChange={(v) => void settings({ effort: v })}
        />
        <Picker
          id="composer-context-size"
          icon={<Layers aria-hidden="true" className="text-faint" />}
          label="Context size"
          value={contextSize}
          display={sizeLabel(contextSize)}
          choices={[
            ...(sizes.some((s) => s.id === 'default') ? [] : [{ value: 'default', label: 'Default' }]),
            ...sizes.map((s) => ({ value: s.id, label: sizeLabel(s.id), description: `${s.tokens.toLocaleString()} tokens${s.id === 'long_context' ? ' · may cost more' : ''}` })),
          ]}
          disabled={settingsLocked || !!noContext}
          reason={locked ? 'This task is read-only.' : live ? 'Context size changes between turns.' : noContext || undefined}
          onChange={(v) => void settings({ context_size: v })}
        />
        <Picker
          id="composer-mode"
          icon={mode === 'yolo' ? <ShieldOff aria-hidden="true" className="text-attention" /> : <Shield aria-hidden="true" className="text-faint" />}
          label="Mode"
          value={mode}
          display={mode === 'yolo' ? 'Yolo' : 'Safe'}
          choices={[
            { value: 'safe', label: 'Safe', description: MODE_TEXT.safe },
            { value: 'yolo', label: 'Yolo', description: MODE_TEXT.yolo },
          ]}
          disabled={!!busy || locked}
          reason={locked ? 'This task is read-only.' : undefined}
          onChange={(v) => void settings({ mode: v as 'safe' | 'yolo' })}
        />
        <span className="flex-1" />
        {busy === 'settings' && <Spinner className="mr-1" />}
        {live && (
          <Tip label={!session.capabilities.cancel ? 'This provider cannot cancel a turn' : 'Stop the turn and pause queued follow-ups'}>
            <Button size="icon-md" variant="secondary" aria-label="Stop turn" className="animate-rise" disabled={!!busy || locked || !session.capabilities.cancel} onClick={() => void action('stop', async () => onSessionUpdate(await api.cancel(session.id)))}>
              {busy === 'stop' ? <Spinner /> : <Square className="!size-3.5" fill="currentColor" />}
            </Button>
          </Tip>
        )}
        {live && (
          <Tip label="Steer this turn (Ctrl+Enter)">
            <Button size="md" variant="secondary" className="animate-rise" disabled={cannotSubmit} onClick={() => void send('steer')}>
              {busy === 'steer' ? <Spinner /> : <Zap />}
              Steer
            </Button>
          </Tip>
        )}
        {!locked && <Tip
          label={
            live ? (
              <>
                Queue for the next turn (Enter)
                <span className="block text-on-primary/70">Ctrl+Enter steers · Shift+Enter adds a line</span>
              </>
            ) : (
              <>
                Send (Enter)
                <span className="block text-on-primary/70">Shift+Enter adds a line</span>
              </>
            )
          }
        >
          <Button type="submit" size="icon-md" variant="primary" aria-label={sendLabel} className="ml-1 transition-transform duration-100 active:scale-95" disabled={cannotSubmit}>
            {busy === 'send' || busy === 'queue' ? <Spinner className="border-on-primary border-r-transparent" /> : live ? <ListPlus /> : <ArrowUp strokeWidth={2.25} />}
          </Button>
        </Tip>}
      </div>
      {routed && (
        <p className="border-t border-hairline/60 px-3.5 py-1 text-caption text-muted">
          Latest turn ran on <span className="font-mono">{routed}</span>
        </p>
      )}
      {busy && busy !== 'settings' && (
        <span className="sr-only" role="status">
          Updating {busy}…
        </span>
      )}
    </form>
  );
}
