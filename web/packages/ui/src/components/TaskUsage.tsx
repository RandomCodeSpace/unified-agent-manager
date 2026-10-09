import { Dialog as BaseDialog } from '@base-ui/react/dialog';
import { Popover as BasePopover } from '@base-ui/react/popover';
import { X } from 'lucide-react';
import { useEffect, useId, useRef, useState, type ReactNode } from 'react';
import { useApi } from '../ApiContext';
import { describeError, readOnly, type SessionDetail, type TaskUsageMetrics, type UsageMetricModel, type UsageTokenDetail } from '../api';
import { formatCredits } from '../lib/cost';
import { BottomSheet, LiftedRow } from './Subagents';
import { PHONE } from './Todos';
import { Button } from './ui/button';
import { backdropClass } from './ui/dialog';
import { PanelFoot, PanelHead, PanelSection } from './ui/panel';
import { Tip } from './ui/tooltip';

const number = new Intl.NumberFormat('en-US');
const requestCost = new Intl.NumberFormat('en-US', { maximumSignificantDigits: 15 });
const count = (n: number | undefined) => n === undefined ? 'Not reported' : number.format(n);
const units = (n: number | undefined) => n === undefined ? 'Not reported' : `${formatCredits(n)} AI units`;
const duration = (n: number) => `${number.format(n)} ms`;

function Counts({ rows }: Readonly<{ rows: readonly (readonly [string, string])[] }>) {
  return <dl className="space-y-1 tabular-nums">{rows.map(([label, value]) => <div key={label} className="flex items-start justify-between gap-4"><dt className="text-muted">{label}</dt><dd className="shrink-0 text-body">{value}</dd></div>)}</dl>;
}

function TokenTypes({ entries }: Readonly<{ entries: readonly UsageTokenDetail[] }>) {
  return entries.length ? <details className="mt-2"><summary className="cursor-pointer text-caption text-muted">Reported token types</summary><div className="mt-2"><Counts rows={entries.map((row) => [row.type, count(row.tokens)])} /></div></details> : null;
}

function ModelMetrics({ models }: Readonly<{ models: readonly UsageMetricModel[] }>) {
  return models.length ? <div className="space-y-2">{models.map((model) => <details key={model.model} className="py-1">
    <summary className="cursor-pointer break-words text-ui text-ink">{model.model}</summary>
    <div className="mt-2 space-y-2"><Counts rows={[
      ['Model API requests', count(model.requests)], ['Premium request cost', requestCost.format(model.premium_request_cost)], ['AI units', units(model.ai_units)],
      ['Input tokens', count(model.input)], ['Output tokens', count(model.output)], ['Cache read tokens', count(model.cache_read)], ['Cache write tokens', count(model.cache_write)], ['Reasoning output tokens', count(model.reasoning)],
    ]} />
      {model.cache_expires_at && <p className="text-caption text-muted">Last reported prompt cache expiry: {new Date(model.cache_expires_at).toLocaleString()}</p>}
      <TokenTypes entries={model.token_details} />
    </div>
  </details>)}</div> : <p className="text-caption text-muted">No model metrics reported.</p>;
}

/** Metrics live only inside the open reader. Close/navigation aborts and drops them. */
function Metrics({ session }: Readonly<{ session: SessionDetail }>) {
  const api = useApi();
  const [snapshot, setSnapshot] = useState<TaskUsageMetrics | null>(null);
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);
  const inactive = readOnly(session);
  useEffect(() => {
    if (!session.open || inactive) return;
    const controller = new AbortController();
    api.taskUsageMetrics(session.id, controller.signal).then(
      (data) => { if (!controller.signal.aborted) setSnapshot(data); },
      // Copy bounded code units so a substring cannot retain a large remote error.
      (err: unknown) => { if (!controller.signal.aborted) setError(describeError(err).slice(0, 512).split('').join('')); },
    );
    return () => controller.abort();
  }, [api, session.id, session.open, inactive, attempt]);
  if (!session.open || inactive) return <p className="text-caption text-muted">Native usage metrics are unavailable for a closed or inactive Task.</p>;
  const refresh = () => { setSnapshot(null); setError(''); setAttempt((n) => n + 1); };
  return <>
    {error ? <p role="status" className="text-caption text-muted">{error}</p> : !snapshot ? <p role="status" className="text-caption text-muted">Loading native usage metrics…</p> : <>
      <PanelSection label="Native session snapshot">
        <p className="mb-2 text-caption text-muted">Accumulated in this conversation, including its subagents. These counters are separate from account usage and the recorded Task AI-unit report.</p>
        {snapshot.current_model && <p className="mb-2 break-words text-caption text-muted">Current model: {snapshot.current_model}</p>}
        <Counts rows={[
          ['User-initiated requests', count(snapshot.user_requests)], ['Premium request cost', requestCost.format(snapshot.premium_request_cost)], ['Native AI units', units(snapshot.ai_units)], ['Model API time', duration(snapshot.api_duration_ms)],
          ['Latest main input tokens', count(snapshot.last_input)], ['Latest main output tokens', count(snapshot.last_output)],
        ]} />
        <TokenTypes entries={snapshot.token_details} />
      </PanelSection>
      <PanelSection label="By model">
        <ModelMetrics models={snapshot.models} />
        <p className="mt-2 text-caption text-muted">Cache and reasoning counters are reported separately; they are not added to input or output here. Model API request counts differ from user-initiated requests.</p>
      </PanelSection>
      <PanelSection label="By agent">
        <p className="mb-2 text-caption text-muted">An agent's model rows are included in the session model rows above. Do not add the two views together.</p>
        {snapshot.agents.length ? snapshot.agents.map((agent) => <details key={agent.id} className="py-1">
          <summary className="cursor-pointer break-words text-ui text-ink">{agent.id === 'main' ? 'Main agent' : agent.name || 'Subagent'}{agent.id !== 'main' && <span className="ml-1 text-caption text-muted">({agent.id})</span>}</summary>
          <div className="mt-2 space-y-2">{agent.display_name && <p className="break-words text-caption text-muted">{agent.display_name}</p>}<Counts rows={[
            ['AI units', units(agent.ai_units)], ['Model API time', duration(agent.api_duration_ms)],
          ]} /><ModelMetrics models={agent.models} /></div>
        </details>) : <p className="text-caption text-muted">No agent metrics reported.</p>}
      </PanelSection>
      <PanelSection label="Native code counters">
        <Counts rows={[
          ['Files modified', count(snapshot.code_changes.files)], ['Lines added', count(snapshot.code_changes.added)], ['Lines removed', count(snapshot.code_changes.removed)],
        ]} />
        <p className="mt-2 text-caption text-muted">Reported by the runtime; the Changes reader has its own scope.</p>
      </PanelSection>
      {snapshot.truncated && <p className="text-caption text-muted">Some breakdown rows are not shown. Reported session counters are unchanged.</p>}
    </>}
    <div><Button size="sm" variant="subtle" onClick={refresh} disabled={!snapshot && !error}>Refresh</Button></div>
  </>;
}

export function TaskUsage({ session }: Readonly<{ session: SessionDetail }>) {
  const api = useApi();
  const [owner, setOwner] = useState(api);
  const id = useId();
  const heading = useRef<HTMLHeadingElement>(null);
  const [anchor, setAnchor] = useState<HTMLButtonElement | null>(null);
  const [reader, setReader] = useState<{ open: boolean; phone: boolean } | null>(null);
  if (owner !== api) { setOwner(api); setReader(null); }
  const close = () => setReader(null);
  const body = (closeButton: ReactNode) => <div className="flex min-h-0 flex-col">
    <PanelHead className="pr-2 pl-4"><div className="flex min-h-7 items-center gap-2"><h2 ref={heading} tabIndex={-1} className="min-w-0 flex-1 text-title text-ink outline-hidden">Task usage</h2>{closeButton}</div></PanelHead>
    <div className="flex min-h-0 flex-col gap-4 overflow-x-hidden overflow-y-auto overscroll-contain px-4 pt-1 pb-4">
      <PanelSection label="Recorded Task report"><p className="text-ui tabular-nums">{session.usage ? units(session.usage.ai_units) : 'AI units not reported yet'}</p><p className="mt-1 text-caption text-muted">Combined main-agent and subagent total from usage events.</p></PanelSection>
      {reader?.open && <Metrics session={session} />}
    </div>
    <PanelFoot><span className="text-caption text-muted">Read on open or Refresh. Premium request cost is a native request multiplier, not USD.</span></PanelFoot>
  </div>;
  return <>
    <Tip label="Task usage"><Button ref={setAnchor} id="composer-task-usage" size="sm" variant="subtle" aria-label="Task usage" aria-haspopup="dialog" aria-expanded={!!reader?.open} aria-controls={reader?.open ? id : undefined} className="px-1.5 text-caption tabular-nums pointer-coarse:min-w-11" onClick={() => setReader({ open: true, phone: window.matchMedia(PHONE).matches })}>Usage</Button></Tip>
    {reader && (reader.phone || !anchor ? <BottomSheet id={id} open={reader.open} onClose={close} onClosed={close} label="Task usage" initialFocus={heading} finalFocus={() => anchor}>
      {body(<BaseDialog.Close render={<Button size="icon-sm" aria-label="Close" className="text-muted" />}><X /></BaseDialog.Close>)}
    </BottomSheet> : <BasePopover.Root open={reader.open} modal onOpenChange={(open) => !open && close()}>
      <BasePopover.Portal><BasePopover.Backdrop className={backdropClass} />{anchor.isConnected && <LiftedRow row={anchor} />}
        <BasePopover.Positioner anchor={anchor} side="top" align="end" sideOffset={10} collisionPadding={12} className="z-50 outline-hidden">
          <BasePopover.Popup id={id} data-popup="" aria-label="Task usage" aria-modal="true" initialFocus={heading} finalFocus={() => anchor} className="flex max-h-[min(80vh,var(--available-height))] w-[440px] max-w-(--available-width) flex-col overflow-hidden rounded-lg bg-raised text-body shadow-modal outline-hidden">
            {body(<BasePopover.Close render={<Button size="icon-sm" aria-label="Close" className="text-muted" />}><X /></BasePopover.Close>)}
          </BasePopover.Popup>
        </BasePopover.Positioner>
      </BasePopover.Portal>
    </BasePopover.Root>)}
  </>;
}
