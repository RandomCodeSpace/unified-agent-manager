import { ChevronRight } from 'lucide-react';
import { useEffect, useId, useState, type FormEvent, type ReactNode } from 'react';
import { useApi } from '../ApiContext';
import { describeError, readOnly, type SessionDetail, type TaskUsageMetrics, type UsageMetricAgent, type UsageMetricModel } from '../api';
import { cn } from '../lib/cn';
import { formatCredits } from '../lib/cost';
import { Note } from './common';
import { plural, shares, time, ToolTimes, useTimeSplit } from './TimeSplit';
import { Button } from './ui/button';
import { Input } from './ui/input';
import { Meter } from './ui/meter';
import { PanelSection } from './ui/panel';
import { HelpTip } from './ui/tooltip';

const number = new Intl.NumberFormat('en-US');
/** 1.18M, 41.9K, else the plain number. */
const compactNumber = new Intl.NumberFormat('en-US', { notation: 'compact', maximumSignificantDigits: 3 });
const requestCost = new Intl.NumberFormat('en-US', { maximumSignificantDigits: 15 });
/** The budget's own figures as typed: a limit of 12.4 is not "12". */
const budgetNumber = new Intl.NumberFormat('en-US', { maximumFractionDigits: 2 });
const count = (n: number | undefined) => n === undefined ? 'Not reported' : number.format(n);
const compact = (n: number | undefined) => n === undefined ? 'Not reported' : compactNumber.format(n);
const units = (n: number | undefined) => n === undefined ? 'Not reported' : formatCredits(n);
const duration = (n: number) => `${number.format(n)} ms`;
/** The models' sum, or undefined while none reports the figure. */
const total = (models: readonly UsageMetricModel[], key: 'input' | 'output' | 'cache_read' | 'reasoning') => models.some((m) => m[key] !== undefined) ? models.reduce((n, m) => n + (m[key] ?? 0), 0) : undefined;
const premium = <span className="inline-flex items-center gap-0.5">Premium<HelpTip label="Premium request cost">A multiplier, not USD.</HelpTip></span>;

function Counts({ rows }: Readonly<{ rows: readonly (readonly [ReactNode, string])[] }>) {
  return <dl className="space-y-1 tabular-nums">{rows.map(([label, value], i) => <div key={i} className="flex items-start justify-between gap-4"><dt className="text-muted">{label}</dt><dd className="shrink-0 text-body">{value}</dd></div>)}</dl>;
}

/** A small table: the first column names the row, `numeric` columns are right-aligned figures. */
function Table({ head, rows, numeric = (i) => i > 0 }: Readonly<{ head: readonly ReactNode[]; rows: readonly (readonly ReactNode[])[]; numeric?: (column: number) => boolean }>) {
  return <table className="w-full text-ui tabular-nums">
    <thead><tr>{head.map((h, i) => <th key={i} scope="col" className={cn('pb-1 font-normal text-caption text-muted', numeric(i) ? 'pl-2 text-right' : 'text-left')}>{h}</th>)}</tr></thead>
    <tbody>{rows.map((cells, r) => <tr key={r}>{cells.map((c, i) => <td key={i} className={cn('py-0.5 align-top', numeric(i) ? 'pl-2 text-right whitespace-nowrap text-body' : '[overflow-wrap:anywhere]')}>{c}</td>)}</tr>)}</tbody>
  </table>;
}

/** One folded breakdown: a chevron, its name and a muted count; closed by default. */
function Fold({ label, meta, children }: Readonly<{ label: string; meta: ReactNode; children: ReactNode }>) {
  return <details className="group">
    <summary className="flex h-6 cursor-pointer list-none items-center gap-1.5 [&::-webkit-details-marker]:hidden">
      <ChevronRight aria-hidden="true" className="size-3 shrink-0 text-faint group-open:rotate-90" />
      <span className="text-ui text-ink">{label}</span>
      <span className="min-w-0 truncate text-caption text-muted">{meta}</span>
    </summary>
    <div className="pb-2 pl-5">{children}</div>
  </details>;
}

function Tile({ label, value, sub, muted }: Readonly<{ label: string; value: string; sub?: ReactNode; muted?: boolean }>) {
  return <div className="rounded-md bg-tint-well px-3 py-1.5">
    <p className="text-caption text-muted">{label}</p>
    <p className={cn('text-title tabular-nums', muted ? 'text-muted' : 'text-ink')}>{value}</p>
    {sub && <p className="text-caption tabular-nums text-muted">{sub}</p>}
  </div>;
}

/** An agent row's name: the main agent, else what its subagent was asked to do with its kind beside it, else the reported name and ID. */
function AgentName({ agent, session }: Readonly<{ agent: UsageMetricAgent; session: SessionDetail }>) {
  if (agent.id === 'main') return <>Main agent</>;
  const asked = session.subagents?.find((s) => s.id === agent.id)?.description?.trim();
  const kind = agent.display_name || agent.name;
  if (asked) return <>{asked}{kind && <span className="ml-1 text-caption text-muted">({kind})</span>}</>;
  return <>{kind || 'Subagent'}<span className="ml-1 text-caption text-muted">({agent.id})</span></>;
}

/** A limit field's number: empty is no limit (0); anything else must be a finite number of at least 0. */
function limit(text: string): number {
  return text.trim() === '' ? 0 : Number(text);
}

/**
 * The Thread's budget, set or changed while it runs: AI credits it may still spend, and how much of the account quota
 * may be used. At either the Thread wraps up with a handoff; it then takes no messages until the budget changes.
 * A set budget is a meter; the form opens under it on Change (Set a budget without one) and closes once saved.
 */
function Budget({ session }: Readonly<{ session: SessionDetail }>) {
  const api = useApi();
  const id = useId();
  const b = session.budget;
  const set = !!(b?.credits || b?.quota_percent);
  const [editing, setEditing] = useState(false);
  const [credits, setCredits] = useState(b?.credits ? String(b.credits) : '');
  const [quota, setQuota] = useState(b?.quota_percent ? String(b.quota_percent) : '');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const save = (budget: { credits: number; quota_percent: number }) => {
    setSaving(true);
    api.settings(session.id, { budget }).then(() => { setError(''); setEditing(false); }, (e: unknown) => setError(describeError(e))).finally(() => setSaving(false));
  };
  const parsed = { credits: limit(credits), quota_percent: limit(quota) };
  const invalid = !(parsed.credits >= 0) || !(parsed.quota_percent >= 0 && parsed.quota_percent <= 100);
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    if (!invalid && !saving) save(parsed);
  };
  return <PanelSection label="Budget" action={<Button type="button" size="sm" variant="secondary" aria-expanded={editing} onClick={() => setEditing((o) => !o)}>{set ? 'Change' : 'Set a budget'}</Button>}>
    {b?.credits ? <>
      <Meter share={b.spent / b.credits} className="h-2" fill="bg-accent" />
      <p className="flex justify-between gap-4 text-ui tabular-nums text-muted">
        <span><span className="font-medium text-ink">{budgetNumber.format(b.spent)}</span> of {budgetNumber.format(b.credits)} AI credits since set · {Math.round((b.spent / b.credits) * 100)}%</span>
        <span className="shrink-0">stops at {budgetNumber.format(b.credits)}</span>
      </p>
    </> : null}
    {b?.quota_percent ? <>
      <Meter share={(b.quota_used ?? 0) / b.quota_percent} className="h-1" fill="bg-hairline-strong" />
      <p className="flex justify-between gap-4 text-caption tabular-nums text-muted">
        <span>Account quota {b.quota_used === undefined ? 'not read yet' : `${formatCredits(b.quota_used)}% used`}</span>
        <span className="shrink-0">stops at {b.quota_percent}%</span>
      </p>
    </> : null}
    {!set && <p className="text-ui text-muted">No limit. Set how many AI credits this Thread may still spend, or at what account quota it stops.</p>}
    {b?.wrapping_up
      ? <Note tone="warn" role="status" className="mt-2">Budget reached: the Thread is wrapping up. A tool already running finishes; uam denies new ones and has the agent write a handoff.</Note>
      : b?.reached && <Note tone="warn" role="status" className="mt-2">Budget reached: {session.handoff ? 'the handoff is at the end of the conversation.' : 'uam stopped this Thread.'} Raise or remove the budget to send more.</Note>}
    {editing && <form className="mt-2 flex flex-col gap-2" onSubmit={onSubmit}>
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
    </form>}
  </PanelSection>;
}

/**
 * The Tools panel's Usage tab: the budget first, then six figures for this Thread and four folded breakdowns. Mounted
 * only while shown. The native metrics live only inside the open reader: close/navigation aborts and drops them, and
 * they are read again when the Thread's pushed usage, context report or state changes, so the counts stay current.
 */
export function UsageTab({ session }: Readonly<{ session: SessionDetail }>) {
  const api = useApi();
  const inactive = readOnly(session);
  // A draft (NewTaskPane) has no id yet: it is not closed, it has not started.
  const native = !!session.capabilities.usage_metrics && !!session.id && session.open && !inactive;
  const [snapshot, setSnapshot] = useState<TaskUsageMetrics | null>(null);
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);
  const live = [session.usage?.ai_units, session.context?.used, session.state].join('\u0000');
  useEffect(() => {
    if (!native) return;
    const controller = new AbortController();
    api.taskUsageMetrics(session.id, controller.signal).then(
      (data) => { if (!controller.signal.aborted) { setSnapshot(data); setError(''); } },
      // Copy bounded code units so a substring cannot retain a large remote error.
      (err: unknown) => { if (!controller.signal.aborted) setError(describeError(err).slice(0, 512).split('').join('')); },
    );
    return () => controller.abort();
  }, [api, session.id, native, attempt, live]);
  const { supported, split, error: splitError } = useTimeSplit(session);
  const refresh = () => { setSnapshot(null); setError(''); setAttempt((n) => n + 1); };
  const loading = native && !snapshot && !error;
  const metric = (value: (s: TaskUsageMetrics) => string, sub?: (s: TaskUsageMetrics) => ReactNode) => snapshot ? { value: value(snapshot), sub: sub?.(snapshot) } : { value: loading ? '…' : 'Not available', muted: true };
  const part = (n: number | undefined, words: string) => n === undefined ? undefined : `${compactNumber.format(n)} ${words}`;
  const pct = split && shares(split);
  const timed = (ms: number | null, sub: string) => !supported || splitError ? { value: 'Not available', muted: true } : !split ? { value: '…', muted: true } : ms === null ? { value: 'Not recorded', muted: true, sub } : { value: time(ms), sub };
  const tokenTypes = snapshot ? [...snapshot.token_details.map((row) => [row.type, count(row.tokens)] as const), ...snapshot.models.flatMap((m) => m.token_details.map((row) => [`${m.model} · ${row.type}`, count(row.tokens)] as const))] : [];
  return <>
    {session.id && !inactive && api.supports('thread-budget-v1') && <Budget session={session} />}
    <PanelSection label="This Thread" meta={split && split.waiting_ms > 0 ? `${time(split.waiting_ms)} waiting on you (${plural(split.waits, 'request')}), not counted` : undefined} action={<Button size="sm" variant="subtle" onClick={refresh} disabled={!snapshot && !error}>Refresh</Button>}>
      <div className="grid grid-cols-3 gap-2 max-sm:grid-cols-2">
        <Tile label="AI credits" value={session.usage ? formatCredits(session.usage.ai_units) : 'Not reported yet'} muted={!session.usage} sub={snapshot && plural(snapshot.user_requests, 'request')} />
        <Tile label="Model time" {...timed(split?.model_ms ?? null, `${plural(split?.model_calls ?? 0, 'call')}${pct ? ` · ${pct.model}%` : ''}`)} />
        <Tile label="Local time" {...timed(split?.local_ms ?? 0, `${plural(split?.local_runs ?? 0, 'run')}${pct ? ` · ${pct.local}%` : ''}`)} />
        <Tile label="Tokens in" {...metric((s) => compact(total(s.models, 'input')), (s) => part(total(s.models, 'cache_read'), 'from cache'))} />
        <Tile label="Tokens out" {...metric((s) => compact(total(s.models, 'output')), (s) => part(total(s.models, 'reasoning'), 'reasoning'))} />
        <Tile label="Code" {...metric((s) => plural(s.code_changes.files, 'file'), (s) => <><span className="text-success">+{number.format(s.code_changes.added)}</span> <span className="text-error">−{number.format(s.code_changes.removed)}</span></>)} />
      </div>
      {session.capabilities.usage_metrics && (!session.id ? <p className="text-caption text-muted">Native usage metrics are reported once the Thread starts.</p>
        : !session.open || inactive ? <p className="text-caption text-muted">Native usage metrics are unavailable for a closed or inactive Thread.</p>
          : error && <p role="status" className="text-caption text-muted">{error}</p>)}
      {splitError && <p className="text-caption text-muted">{splitError}</p>}
      {snapshot?.current_model && <p className="break-words text-caption text-muted">Current model: {snapshot.current_model}</p>}
      {snapshot && <p className="text-caption text-muted">Native AI credits {units(snapshot.ai_units)} · Premium request cost {requestCost.format(snapshot.premium_request_cost)} · Model API time {duration(snapshot.api_duration_ms)}</p>}
      {snapshot?.truncated && <p className="text-caption text-muted">Some breakdown rows are not shown. Reported session counters are unchanged.</p>}
    </PanelSection>
    {(snapshot || split) && <div className="flex flex-col">
      {snapshot && <Fold label="By model" meta={plural(snapshot.models.length, 'model')}>
        {snapshot.models.length ? <Table head={['Model', 'Requests', premium, 'AI credits', 'In', 'Out']} rows={snapshot.models.map((m) => [m.model, count(m.requests), requestCost.format(m.premium_request_cost), units(m.ai_units), compact(m.input), compact(m.output)])} /> : <p className="text-caption text-muted">No model metrics reported.</p>}
        {snapshot.models.map((m) => m.cache_expires_at && <p key={m.model} className="mt-1 text-caption text-muted">{m.model}: last reported prompt cache expiry {new Date(m.cache_expires_at).toLocaleString()}</p>)}
      </Fold>}
      {snapshot && <Fold label="By agent" meta={plural(snapshot.agents.length, 'agent')}>
        {snapshot.agents.length ? <Table head={['Agent', 'AI credits', 'Model time', 'Model']} numeric={(i) => i === 1 || i === 2} rows={snapshot.agents.map((a) => [<AgentName key="name" agent={a} session={session} />, units(a.ai_units), time(a.api_duration_ms), <span key="models" className="text-muted">{a.models.map((m) => m.model).join(', ')}</span>])} /> : <p className="text-caption text-muted">No agent metrics reported.</p>}
      </Fold>}
      {split && split.tools.length > 0 && <Fold label="Local time by tool" meta={plural(split.tools.length, 'tool')}><ToolTimes split={split} /></Fold>}
      {snapshot && <Fold label="Tokens" meta="by model and type">
        {snapshot.models.length > 0 && <Table head={['Model', 'In', 'Out', 'Cache read', 'Cache write', 'Reasoning']} rows={snapshot.models.map((m) => [m.model, count(m.input), count(m.output), count(m.cache_read), count(m.cache_write), count(m.reasoning)])} />}
        <div className="mt-2"><Counts rows={[...tokenTypes, ['Latest main input tokens', count(snapshot.last_input)], ['Latest main output tokens', count(snapshot.last_output)]]} /></div>
      </Fold>}
    </div>}
  </>;
}
