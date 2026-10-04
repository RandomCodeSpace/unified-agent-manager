import { Info, Search, X } from 'lucide-react';
import { useEffect, useId, useRef, useState } from 'react';
import { api, describeError, type TokenPeriodKey, type TokenPriceCatalog, type TokenUsageReport } from '../api';
import { cn } from '../lib/cn';
import { accountQuota, quotaFace, quotaPace, quotaText } from '../lib/cost';
import { useApp } from './common';
import { aggregateUsageModels, estimateWithoutCache, groupUsageModels } from '../lib/token-usage';
import { Count, costText, MODEL_COLORS, ModelOverview, ModelTable, TokenSplitValues, type UsageSort } from './UsageModels';
import { Button } from './ui/button';
import { Popover } from './ui/popover';
import { HelpTip, Tip } from './ui/tooltip';

const PERIODS: { key: TokenPeriodKey; label: string }[] = [
  { key: 'today', label: 'Today' }, { key: '7d', label: '7 days' },
  { key: '30d', label: '30 days' }, { key: 'lifetime', label: 'Lifetime' },
];

const COLLECTION_STATUS = {
  starting: 'Reading local harness usage. Totals may be incomplete.',
  ready: '',
  partial: 'Some local harness usage is missing. Totals may be incomplete.',
  unavailable: 'Local harness usage is unavailable. Showing available usage.',
};

function UsageContent({ onClose, onAddPrices, now }: Readonly<{ onClose: () => void; onAddPrices?: () => void; now: number }>) {
  const { usage, meta } = useApp();
  const quotas = (usage?.quotas ?? []).filter((q, _i, all) => !q.unlimited || !all.some((other) => other.provider === q.provider && !other.unlimited));
  const [period, setPeriod] = useState<TokenPeriodKey>('today');
  const [report, setReport] = useState<TokenUsageReport | null>(null);
  const [prices, setPrices] = useState<TokenPriceCatalog | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [priceError, setPriceError] = useState(false);
  const [retry, setRetry] = useState(0);
  const [all, setAll] = useState(false);
  const [query, setQuery] = useState('');
  const [sort, setSort] = useState<UsageSort>('tokens');
  const [colors, setColors] = useState<Map<string, string>>(() => new Map());
  useEffect(() => {
    let current = true;
    let usagePending = false;
    let pricesPending = false;
    const readUsage = async () => {
      if (usagePending) return;
      usagePending = true;
      try {
        const result = await api.tokenUsage();
        if (current) {
          setReport(result);
          setError(null);
          setColors((previous) => {
            const next = new Map(previous);
            const models = groupUsageModels(result.periods.lifetime.models);
            for (const model of models) if (!next.has(model.model)) next.set(model.model, MODEL_COLORS[next.size % MODEL_COLORS.length]);
            return next;
          });
        }
      } catch (e) { if (current) setError(describeError(e)); }
      finally { usagePending = false; }
    };
    const readPrices = async () => {
      if (pricesPending) return;
      pricesPending = true;
      try {
        const result = await api.tokenPrices();
        if (current) { setPrices(result); setPriceError(false); }
      } catch { if (current) { setPrices(null); setPriceError(true); } }
      finally { pricesPending = false; }
    };
    // A slow price request must not delay usage or its subsequent refreshes.
    const read = () => { void readUsage(); void readPrices(); };
    read();
    const timer = window.setInterval(read, 15000);
    return () => { current = false; window.clearInterval(timer); };
  }, [retry]);
  const shown = report?.periods[period];
  const models = groupUsageModels(shown?.models ?? []);
  const combined = aggregateUsageModels(models, 'All models');
  const noCache = estimateWithoutCache(shown?.models ?? [], prices);
  const unpriced = models.filter((model) => model.cost_usd === null || model.cost_partial).length;
  const filtered = models.filter((model) => (model.model || 'Unspecified model').toLowerCase().includes(query.toLowerCase()));
  return <>
    <div className="shrink-0 px-4 pt-3">
      <div className="flex items-center justify-between gap-3">
        <div className="flex items-center gap-1">
          <Popover.Title className="text-title">Usage</Popover.Title>
          <HelpTip label="Usage">
            <span className="block">Input, Output, and Cache are separate parts of the recorded token split. Cache includes reads and writes. Bars show proportions within each model; totals retain the source-reported count.</span>
            {report && <span className="mt-2 block">UAM tracking started {new Date(report.since).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' })}. Days use server time. Includes {report.collection ? 'local harness records and UAM ' : ''}task, subagent, and Background AI usage.</span>}
            {report?.collection && <>
              <span className="mt-2 block">External Copilot usage requires local telemetry files and is included from {new Date(report.collection.copilot_since).toLocaleString()}. Other harness history may start earlier.</span>
              {report.collection.status !== 'ready' && <span className="mt-2 block">{COLLECTION_STATUS[report.collection.status]}</span>}
            </>}
          </HelpTip>
        </div>
        <Button size="icon-sm" aria-label="Close usage" onClick={onClose}><X aria-hidden="true" /></Button>
      </div>
      <section aria-label="AI allowance" className="mt-3 flex flex-col gap-2 rounded-sm bg-surface px-3 py-2">
        {quotas.length === 0 && <p className="text-caption text-muted">Account usage not reported yet.</p>}
        {quotas.map((quota) => {
          const pace = quotaPace(quota, now, usage?.stale);
          const provider = meta?.providers.find((p) => p.name === quota.provider)?.display_name ?? quota.provider;
          return <div key={`${quota.provider}:${quota.type}`} className="text-caption">
            <div className="flex items-baseline justify-between gap-3"><span className="font-medium text-ink">{provider} allowance</span><span className="font-medium tabular-nums text-ink">{quotaFace(quota)}</span></div>
            <p className="mt-1 text-muted">{quotaText(quota)}</p>
            <p className={cn('mt-1', QUOTA_TONES[pace.tone])}>{pace.label}</p>
            {pace.daysUntilReset !== null && <p className="text-muted" title={new Date(now + pace.daysUntilReset * 86400000).toLocaleString()}>{daysText(pace.daysUntilReset)} until reset{pace.daysAtPace !== null ? ` · about ${daysText(pace.daysAtPace)} left at this pace` : ''}</p>}
          </div>;
        })}
        {quotas.some((q) => quotaPace(q, now, usage?.stale).daysAtPace !== null) && <p className="text-meta text-muted">Pace uses average allowance spent per day this month.</p>}
      </section>
      {report?.collection?.updated_at && <p role="status" className="mt-1 text-meta text-muted">Last fetched {new Date(report.collection.updated_at).toLocaleString()}.</p>}
      <div role="group" aria-label="Usage period" className="mt-2 flex rounded-sm bg-sunken p-0.5">
        {PERIODS.map(({ key, label }) => <Button key={key} size="sm" className={cn('flex-1 px-1', period === key && 'bg-raised text-ink shadow-raised')} aria-pressed={key === period} onClick={() => setPeriod(key)}>{label}</Button>)}
      </div>
      {error && <div role="alert" className="mt-3 flex items-center gap-2 text-caption text-error">
        <span className="min-w-0 flex-1">{report ? 'Could not refresh usage. Showing the last read. ' : 'Could not load usage. '}{error}</span>
        <Button size="sm" onClick={() => setRetry((n) => n + 1)}>Retry</Button>
      </div>}
      {priceError && shown && <div className="mt-3 flex items-center gap-2 text-caption text-muted">
        <span className="min-w-0 flex-1">Could not load token prices.</span>
        <Button size="sm" onClick={() => setRetry((n) => n + 1)}>Retry prices</Button>
      </div>}
      {!shown && !error && <p role="status" className="py-4 text-muted">Loading usage…</p>}
      {shown && <>
        <div className="grid grid-cols-3 gap-3 py-2 max-[360px]:grid-cols-2">
          <div className="max-[360px]:col-span-2"><p className="flex min-h-6 items-center text-caption text-muted">Total tokens</p><p className="mt-1 text-display-md text-ink tabular-nums"><Count value={shown.total.total} /></p><p className="mt-1 text-meta text-muted">Across {models.length} {models.length === 1 ? 'model' : 'models'}</p></div>
          <div>
            <div className="flex min-h-6 items-center gap-0.5 text-caption text-muted"><span>Estimated cost</span>
              <HelpTip label="Estimated cost">
                <span className="block">Costs use current base token prices or source-reported amounts when available. These are not provider bills.</span>
                {shown.cost_usd === null ? <span className="mt-2 block">Cost unavailable: prices or token counts are missing.</span> : (shown.unpriced_models > 0 || unpriced > 0) && <span className="mt-2 block">Partial estimate: includes known costs only.</span>}
              </HelpTip>
            </div>
            <p className="mt-1 text-display-md text-ink tabular-nums [overflow-wrap:anywhere]">{costText(shown.cost_usd)}</p>
          </div>
          <div>
            <div className="flex min-h-6 items-center gap-0.5 text-caption text-muted"><span>Without cache</span>
              <HelpTip label="Cost without cache">
                <span className="block">Input and cache tokens charged once at each tool's standard input rate. Output keeps its standard output rate.</span>
                {noCache.cost === null ? <span className="mt-2 block">Estimate unavailable: prices or token counts are missing.</span> : noCache.partial && <span className="mt-2 block">Partial estimate: some usage has missing prices or token counts.</span>}
                <span className="mt-2 block">Uses current configured prices and available token counts. Records without prices or token counts are excluded. Source-only charges cannot be reconstructed. This is an alternative total estimate, not an extra charge.</span>
              </HelpTip>
            </div>
            <p className={cn('mt-1 text-ink tabular-nums [overflow-wrap:anywhere]', prices === null && !priceError && models.length ? 'text-caption' : 'text-display-md')}>{prices === null && !priceError && models.length ? 'Loading…' : costText(noCache.cost)}</p>
          </div>
        </div>
        <div className="mb-2 rounded-sm bg-surface px-3 py-1.5" role="group" aria-label="Total token split"><TokenSplitValues value={combined} /></div>
        <div role="group" aria-label="Usage view" className="mb-2 flex gap-1">
          <Button size="sm" aria-pressed={!all} onClick={() => { setAll(false); setQuery(''); }}>Overview</Button>
          <Button size="sm" aria-pressed={all} onClick={() => setAll(true)}>All models · {models.length}</Button>
        </div>
        {!all && models.length > 0 && <div className="flex items-center justify-between gap-2 pb-1"><h3 className="font-medium text-ink">{models.length > 5 ? 'Top 5 models' : 'Models'}</h3><span className="text-meta text-muted">By token usage</span></div>}
        {all && <label className="mb-2 flex items-center gap-2 rounded-sm bg-sunken px-3 py-2 shadow-well focus-within:outline-2 focus-within:outline-focus">
          <Search aria-hidden="true" className="size-4 shrink-0 text-muted" />
          <input aria-label="Find a model" placeholder="Find a model" value={query} onChange={(event) => setQuery(event.target.value)} className="min-w-0 flex-1 bg-transparent text-ui outline-none" />
        </label>}
      </>}
    </div>
    {shown && <>
      {/* eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- A labelled scroll region must accept keyboard scrolling. */}
      <div className={cn('min-w-0 px-4 pb-1', all ? 'min-h-0 overflow-x-hidden overflow-y-auto overscroll-contain [@media(max-height:600px)]:shrink-0 [@media(max-height:600px)]:overflow-x-clip [@media(max-height:600px)]:overflow-y-visible' : 'shrink-0 overflow-x-clip')} role="region" aria-label="Model usage" tabIndex={all ? 0 : undefined}>
        {models.length === 0 ? <p className="py-4 text-caption text-muted">No usage recorded for this period.</p> : all ? <>
          <ModelTable models={filtered} colors={colors} period={PERIODS.find((p) => p.key === period)!.label} sort={sort} onSort={setSort} />
          {filtered.length === 0 && <p className="py-8 text-center text-muted">No matching models.</p>}
        </> : <ModelOverview models={models} colors={colors} />}
      </div>
      <div className="shrink-0 bg-surface px-4 py-2">
        {all && <p className="mb-2 text-meta text-muted">{query ? `${filtered.length} of ${models.length} models` : `${models.length} models · Scroll for more`} · Token splits in the info tooltips.</p>}
        {unpriced > 0 && <p className="mb-1 flex flex-wrap items-baseline gap-x-2 gap-y-1 text-meta text-muted">
          <span>Prices missing for {unpriced} {unpriced === 1 ? 'model' : 'models'}.</span>
          <a href="#settings" className="text-ink underline underline-offset-2" onClick={(event) => { if (onAddPrices) { event.preventDefault(); onAddPrices(); } onClose(); }}>Add prices</a>
        </p>}
        <p className="flex gap-1.5 text-meta text-muted"><Info aria-hidden="true" className="mt-0.5 size-3 shrink-0" />Estimates include known costs only. They are not provider bills.</p>
      </div>
    </>}
  </>;
}

const QUOTA_TONES = { healthy: 'text-success', attention: 'text-warning', danger: 'text-error', muted: 'text-muted' };

function daysText(days: number): string {
  if (days < 1) return 'less than 1 day';
  const count = Math.round(days);
  return `${count} ${count === 1 ? 'day' : 'days'}`;
}

/** Account allowance is live from SSE; recorded usage reads only while open. */
export function UsageButton({ side = 'top', className, onAddPrices }: Readonly<{ side?: 'top' | 'right'; className?: string; onAddPrices?: () => void }>) {
  const { usage } = useApp();
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 60000);
    return () => window.clearInterval(timer);
  }, []);
  const quota = accountQuota(usage?.quotas);
  const pace = quotaPace(quota, now, usage?.stale);
  const value = quota?.unlimited ? '∞' : pace.remaining === null ? '—' : String(Math.round(pace.remaining));
  const description = `${quota ? quotaFace(quota) : 'Account usage unavailable'}. ${pace.label}`;
  const descriptionId = useId();
  const [open, setOpen] = useState(false);
  const popup = useRef<HTMLDivElement>(null);
  return <Popover.Root open={open} onOpenChange={setOpen}>
    <Tip label={`Usage · ${description}`} side={side} disabled={open}>
      <Popover.Trigger render={<Button id="account-usage" size="icon" aria-label="Usage" aria-describedby={descriptionId} className={cn('[&_svg]:size-full', QUOTA_TONES[pace.tone], className)} />}>
        <span aria-hidden="true" className={cn('relative block size-4 shrink-0', QUOTA_TONES[pace.tone])}>
          <svg viewBox="0 0 36 36" className="absolute inset-0 -rotate-90" fill="none">
            <circle cx="18" cy="18" r="15.5" stroke="currentColor" strokeWidth="2.5" className="text-hairline-strong" />
            <circle cx="18" cy="18" r="15.5" pathLength="100" stroke="currentColor" strokeWidth="2.5" strokeDasharray={`${pace.remaining ?? 0} 100`} />
          </svg>
          <span className="absolute inset-0 flex items-center justify-center text-[7px] leading-none font-semibold tabular-nums">{value}</span>
        </span>
      </Popover.Trigger>
    </Tip>
    <span id={descriptionId} className="sr-only">{description}</span>
    <Popover.Content ref={popup} initialFocus={popup} side={side} className="w-[29rem] max-w-[calc(100vw-1rem)] max-h-[min(calc(100dvh-2rem),var(--available-height))] gap-0 overflow-x-hidden overflow-y-auto p-0">
      {open && <UsageContent now={now} onClose={() => setOpen(false)} onAddPrices={onAddPrices} />}
    </Popover.Content>
  </Popover.Root>;
}
