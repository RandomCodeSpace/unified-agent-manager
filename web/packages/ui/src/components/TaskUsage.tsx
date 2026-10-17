import { useEffect, useId, useState, type FormEvent, type ReactNode } from 'react';
import { useApi } from '../ApiContext';
import { describeError, readOnly, type SessionDetail, type TaskUsageMetrics, type UsageMetricAgent, type UsageMetricModel, type UsageTokenDetail } from '../api';
import { formatCredits } from '../lib/cost';
import { Note } from './common';
import { TimeSplitSection } from './TimeSplit';
import { Button } from './ui/button';
import { Input } from './ui/input';
import { PanelSection } from './ui/panel';
import { HelpTip } from './ui/tooltip';

const number = new Intl.NumberFormat('en-US');
const requestCost = new Intl.NumberFormat('en-US', { maximumSignificantDigits: 15 });
const count = (n: number | undefined) => n === undefined ? 'Not reported' : number.format(n);
const units = (n: number | undefined) => n === undefined ? 'Not reported' : `${formatCredits(n)} AI credits`;
const duration = (n: number) => `${number.format(n)} ms`;
const premium = <span className="inline-flex items-center gap-0.5">Premium request cost<HelpTip label="Premium request cost">A multiplier, not USD.</HelpTip></span>;

function Counts({ rows }: Readonly<{ rows: readonly (readonly [ReactNode, string])[] }>) {
  return <dl className="space-y-1 tabular-nums">{rows.map(([label, value], i) => <div key={i} className="flex items-start justify-between gap-4"><dt className="text-muted">{label}</dt><dd className="shrink-0 text-body">{value}</dd></div>)}</dl>;
}

function TokenTypes({ entries }: Readonly<{ entries: readonly UsageTokenDetail[] }>) {
  return entries.length ? <details className="mt-2"><summary className="cursor-pointer text-caption text-muted">Reported token types</summary><div className="mt-2"><Counts rows={entries.map((row) => [row.type, count(row.tokens)])} /></div></details> : null;
}

function ModelMetrics({ models }: Readonly<{ models: readonly UsageMetricModel[] }>) {
  return models.length ? <div className="space-y-2">{models.map((model) => <details key={model.model} className="py-1">
    <summary className="cursor-pointer break-words text-ui text-ink">{model.model}</summary>
    <div className="mt-2 space-y-2"><Counts rows={[
      ['Model API requests', count(model.requests)], ['Premium request cost', requestCost.format(model.premium_request_cost)], ['AI credits', units(model.ai_units)],
      ['Input tokens', count(model.input)], ['Output tokens', count(model.output)], ['Cache read tokens', count(model.cache_read)], ['Cache write tokens', count(model.cache_write)], ['Reasoning output tokens', count(model.reasoning)],
    ]} />
      {model.cache_expires_at && <p className="text-caption text-muted">Last reported prompt cache expiry: {new Date(model.cache_expires_at).toLocaleString()}</p>}
      <TokenTypes entries={model.token_details} />
    </div>
  </details>)}</div> : <p className="text-caption text-muted">No model metrics reported.</p>;
}

/** An agent row's name: the main agent, else what its subagent was asked to do with its kind beside it, else the reported name and ID. */
function AgentName({ agent, session }: Readonly<{ agent: UsageMetricAgent; session: SessionDetail }>) {
  if (agent.id === 'main') return <>Main agent</>;
  const asked = session.subagents?.find((s) => s.id === agent.id)?.description?.trim();
  const kind = agent.display_name || agent.name;
  if (asked) return <>{asked}{kind && <span className="ml-1 text-caption text-muted">({kind})</span>}</>;
  return <>{kind || 'Subagent'}<span className="ml-1 text-caption text-muted">({agent.id})</span></>;
}

/**
 * Metrics live only inside the open reader. Close/navigation aborts and drops them. It reads again when
 * the Task's pushed usage, context report or state changes, so the counts stay current while open.
 */
function Metrics({ session }: Readonly<{ session: SessionDetail }>) {
  const api = useApi();
  const [snapshot, setSnapshot] = useState<TaskUsageMetrics | null>(null);
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);
  const inactive = readOnly(session);
  const live = [session.usage?.ai_units, session.context?.used, session.state].join('\u0000');
  useEffect(() => {
    if (!session.open || inactive) return;
    const controller = new AbortController();
    api.taskUsageMetrics(session.id, controller.signal).then(
      (data) => { if (!controller.signal.aborted) { setSnapshot(data); setError(''); } },
      // Copy bounded code units so a substring cannot retain a large remote error.
      (err: unknown) => { if (!controller.signal.aborted) setError(describeError(err).slice(0, 512).split('').join('')); },
    );
    return () => controller.abort();
  }, [api, session.id, session.open, inactive, attempt, live]);
  // A draft (NewTaskPane) has no id yet: it is not closed, it has not started.
  if (!session.id) return <p className="text-caption text-muted">Native usage metrics are reported once the Thread starts.</p>;
  if (!session.open || inactive) return <p className="text-caption text-muted">Native usage metrics are unavailable for a closed or inactive Thread.</p>;
  const refresh = () => { setSnapshot(null); setError(''); setAttempt((n) => n + 1); };
  return <>
    {error ? <p role="status" className="text-caption text-muted">{error}</p> : !snapshot ? <p role="status" className="text-caption text-muted">Loading native usage metrics…</p> : <>
      <PanelSection label="Native session snapshot">
        {snapshot.current_model && <p className="mb-2 break-words text-caption text-muted">Current model: {snapshot.current_model}</p>}
        <Counts rows={[
          ['User-initiated requests', count(snapshot.user_requests)], [premium, requestCost.format(snapshot.premium_request_cost)], ['Native AI credits', units(snapshot.ai_units)], ['Model API time', duration(snapshot.api_duration_ms)],
          ['Latest main input tokens', count(snapshot.last_input)], ['Latest main output tokens', count(snapshot.last_output)],
        ]} />
        <TokenTypes entries={snapshot.token_details} />
      </PanelSection>
      <PanelSection label="By model">
        <ModelMetrics models={snapshot.models} />
      </PanelSection>
      <PanelSection label="By agent">
        {snapshot.agents.length ? snapshot.agents.map((agent) => <details key={agent.id} className="py-1">
          <summary className="cursor-pointer break-words text-ui text-ink"><AgentName agent={agent} session={session} /></summary>
          <div className="mt-2 space-y-2"><Counts rows={[
            ['AI credits', units(agent.ai_units)], ['Model API time', duration(agent.api_duration_ms)],
          ]} /><ModelMetrics models={agent.models} /></div>
        </details>) : <p className="text-caption text-muted">No agent metrics reported.</p>}
      </PanelSection>
      <PanelSection label="Native code counters">
        <Counts rows={[
          ['Files modified', count(snapshot.code_changes.files)], ['Lines added', count(snapshot.code_changes.added)], ['Lines removed', count(snapshot.code_changes.removed)],
        ]} />
      </PanelSection>
      {snapshot.truncated && <p className="text-caption text-muted">Some breakdown rows are not shown. Reported session counters are unchanged.</p>}
    </>}
    <div><Button size="sm" variant="subtle" onClick={refresh} disabled={!snapshot && !error}>Refresh</Button></div>
  </>;
}

/** A limit field's number: empty is no limit (0); anything else must be a finite number of at least 0. */
function limit(text: string): number {
  return text.trim() === '' ? 0 : Number(text);
}

/**
 * The Thread's budget, set or changed while it runs: AI credits it may still spend, and how much of the account quota
 * may be used. At either the Thread wraps up with a handoff; it then takes no messages until the budget changes.
 */
function Budget({ session }: Readonly<{ session: SessionDetail }>) {
  const api = useApi();
  const id = useId();
  const b = session.budget;
  const [credits, setCredits] = useState(b?.credits ? String(b.credits) : '');
  const [quota, setQuota] = useState(b?.quota_percent ? String(b.quota_percent) : '');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const save = (budget: { credits: number; quota_percent: number }) => {
    setSaving(true);
    api.settings(session.id, { budget }).then(() => setError(''), (e: unknown) => setError(describeError(e))).finally(() => setSaving(false));
  };
  const parsed = { credits: limit(credits), quota_percent: limit(quota) };
  const invalid = !(parsed.credits >= 0) || !(parsed.quota_percent >= 0 && parsed.quota_percent <= 100);
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    if (!invalid && !saving) save(parsed);
  };
  const rows: [string, string][] = [];
  if (b?.credits) rows.push(['Spent since set', `${formatCredits(b.spent)} of ${units(b.credits)}`]);
  if (b?.quota_percent) rows.push(['Account quota used', `${b.quota_used === undefined ? 'Not read yet' : `${formatCredits(b.quota_used)}%`} · limit ${b.quota_percent}%`]);
  return <PanelSection label="Budget">
    {rows.length > 0 && <Counts rows={rows} />}
    {b?.wrapping_up
      ? <Note tone="warn" role="status" className="mt-2">Budget reached: the Thread is wrapping up. A tool already running finishes; uam denies new ones and has the agent write a handoff.</Note>
      : b?.reached && <Note tone="warn" role="status" className="mt-2">Budget reached: {session.handoff ? 'the handoff is at the end of the conversation.' : 'uam stopped this Thread.'} Raise or remove the budget to send more.</Note>}
    <form className="mt-2 flex flex-col gap-2" onSubmit={onSubmit}>
      {/* One grid, so both fields line up whatever their labels' widths. */}
      <div className="grid grid-cols-[auto_6rem_1fr] items-center gap-x-2 gap-y-2 text-ui text-body">
        <label htmlFor={`${id}-credits`}>Stop after</label>
        <Input id={`${id}-credits`} size="md" inputMode="decimal" className="tabular-nums" placeholder="No limit" value={credits} aria-invalid={!(parsed.credits >= 0)} onChange={(e) => setCredits(e.target.value)} />
        <span className="min-w-0">more AI credits</span>
        <label htmlFor={`${id}-quota`}>Stop at</label>
        <Input id={`${id}-quota`} size="md" inputMode="decimal" className="tabular-nums" placeholder="No limit" value={quota} aria-invalid={!(parsed.quota_percent >= 0 && parsed.quota_percent <= 100)} onChange={(e) => setQuota(e.target.value)} />
        <span className="min-w-0">% account quota used</span>
      </div>
      <p className="text-caption text-muted">Near the limit the agent is told to wrap up. At it, uam denies new tools and has the agent write a handoff, then holds the Thread. Changing the budget counts AI credits from then.</p>
      <div className="flex gap-2">
        <Button type="submit" size="sm" variant="primary" disabled={saving || invalid || (parsed.credits === 0 && parsed.quota_percent === 0)}>Set budget</Button>
        {b && <Button type="button" size="sm" variant="secondary" disabled={saving} onClick={() => { setCredits(''); setQuota(''); save({ credits: 0, quota_percent: 0 }); }}>Remove</Button>}
      </div>
      {error && <Note tone="error" role="alert">{error}</Note>}
    </form>
  </PanelSection>;
}

/** The Tools panel's Usage tab: the time split, the recorded Task report, the budget, then the native metrics where the provider reads them. Mounted only while shown. */
export function UsageTab({ session }: Readonly<{ session: SessionDetail }>) {
  const api = useApi();
  return <>
    <TimeSplitSection session={session} />
    <PanelSection label="Recorded Thread report"><p className="text-ui tabular-nums">{session.usage ? units(session.usage.ai_units) : 'AI credits not reported yet'}</p></PanelSection>
    {session.id && !readOnly(session) && api.supports('thread-budget-v1') && <Budget session={session} />}
    {session.capabilities.usage_metrics && <Metrics session={session} />}
  </>;
}
