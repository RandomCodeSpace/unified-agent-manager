import { useEffect, useState } from 'react';
import { useApi } from '../ApiContext';
import { describeError, readOnly, type SessionDetail, type TaskUsageMetrics, type UsageMetricModel, type UsageTokenDetail } from '../api';
import { formatCredits } from '../lib/cost';
import { Button } from './ui/button';
import { PanelSection } from './ui/panel';

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

/** The Tools panel's Usage tab: the recorded Task report, then the native metrics. Mounted only while shown. */
export function UsageTab({ session }: Readonly<{ session: SessionDetail }>) {
  return <>
    <PanelSection label="Recorded Task report"><p className="text-ui tabular-nums">{session.usage ? units(session.usage.ai_units) : 'AI units not reported yet'}</p><p className="mt-1 text-caption text-muted">Combined main-agent and subagent total from usage events.</p></PanelSection>
    <Metrics session={session} />
  </>;
}
