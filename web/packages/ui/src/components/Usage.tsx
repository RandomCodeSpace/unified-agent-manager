import { useApi } from '../ApiContext';
import { Check, Info, TriangleAlert, X } from 'lucide-react';
import { useCallback, useEffect, useId, useRef, useState } from 'react';
import { describeError, type ApiClient, type ConnectedStatus, type Quota, type TokenPeriodKey, type TokenPriceCatalog, type TokenUsageReport } from '../api';
import { useFederation, type Machine } from '../FederationContext';
import { cn } from '../lib/cn';
import { accountQuota, quotaBurn, quotaFace, quotaLabel, quotaPace } from '../lib/cost';
import { dateTime, Note, timeAgo, useApp } from './common';
import { aggregateUsageModels, estimateCacheSaving, groupUsageModels, mergeTokenReports, sumCacheSavings } from '../lib/token-usage';
import { Count, costText, MODEL_COLORS, modelNames, ModelRows, TokenSplitValues } from './UsageModels';
import { Button } from './ui/button';
import { PanelFoot, PanelHead, PanelSection } from './ui/panel';
import { Popover } from './ui/popover';
import { Select } from './ui/select';
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

/** One machine's last reads: its recorded usage and its price catalog. */
interface Reading { report: TokenUsageReport | null; error: string | null; prices: TokenPriceCatalog | null; priceError: boolean }
const UNREAD: Reading = { report: null, error: null, prices: null, priceError: false };

/** A machine the Tokens section reads; `down` says why it cannot report, without asking it. */
interface Source { id: string; label: string; client: ApiClient; down?: string }

const ALL_MACHINES = '*';
const DOWN: Partial<Record<ConnectedStatus['status'], string>> = { offline: 'offline', 'auth-required': 'needs a new access key', unsupported: 'needs an update', 'account-mismatch': 'linked to a different Copilot account' };

/** Why a connected machine cannot report usage now, said after "not included:". */
function cannotReport(machine: Machine): string | undefined {
  if (!machine.connection) return undefined;
  if (!machine.connection.capabilities.includes('usage-v1')) return 'needs an update';
  return DOWN[machine.status.status];
}

/** Reads one machine's recorded usage and prices while the popover is open, every 15 seconds. */
function UsageReader({ id, client, retry, onRead }: Readonly<{ id: string; client: ApiClient; retry: number; onRead: (id: string, patch: Partial<Reading>) => void }>) {
  useEffect(() => {
    let current = true;
    let usagePending = false;
    let pricesPending = false;
    const readUsage = async () => {
      if (usagePending) return;
      usagePending = true;
      try {
        const result = await client.tokenUsage();
        if (current) onRead(id, { report: result, error: null });
      } catch (e) { if (current) onRead(id, { error: describeError(e) }); }
      finally { usagePending = false; }
    };
    const readPrices = async () => {
      if (pricesPending) return;
      pricesPending = true;
      try {
        const result = await client.tokenPrices();
        if (current) onRead(id, { prices: result, priceError: false });
      } catch { if (current) onRead(id, { prices: null, priceError: true }); }
      finally { pricesPending = false; }
    };
    // A slow price request must not delay usage or its subsequent refreshes.
    const read = () => { void readUsage(); void readPrices(); };
    read();
    const timer = window.setInterval(read, 15000);
    return () => { current = false; window.clearInterval(timer); };
  }, [id, client, retry, onRead]);
  return null;
}

function UsageContent({ onClose, onAddPrices, now }: Readonly<{ onClose: () => void; onAddPrices?: () => void; now: number }>) {
  const api = useApi();
  const federation = useFederation();
  const { usage, meta } = useApp();
  const quotas = (usage?.quotas ?? []).filter((q, _i, all) => !q.unlimited || !all.some((other) => other.provider === q.provider && !other.unlimited));
  const [period, setPeriod] = useState<TokenPeriodKey>('today');
  const [readings, setReadings] = useState<Record<string, Reading>>({});
  const [retry, setRetry] = useState(0);
  const [all, setAll] = useState(false);
  const [colors, setColors] = useState<Map<string, string>>(() => new Map());
  const onRead = useCallback((id: string, patch: Partial<Reading>) => {
    setReadings((previous) => ({ ...previous, [id]: { ...(previous[id] ?? UNREAD), ...patch } }));
    const report = patch.report;
    if (report) setColors((previous) => {
      const next = new Map(previous);
      const models = groupUsageModels(report.periods.lifetime.models);
      for (const model of models) if (!next.has(model.model)) next.set(model.model, MODEL_COLORS[next.size % MODEL_COLORS.length]);
      return next;
    });
  }, []);
  // With an enabled connection, tokens are every machine's (they never overlap); the allowance is the account's, so it is not.
  const machines = federation?.machines && federation.machines.length > 1 ? federation.machines : null;
  const sources: Source[] = machines ? machines.map((m) => ({ id: m.id, label: m.label, client: m.client, down: cannotReport(m) })) : [{ id: '', label: 'This instance', client: api }];
  const [scope, setScope] = useState(ALL_MACHINES);
  const one = machines ? sources.find((source) => source.id === scope) : sources[0];
  const states = sources.map((source) => {
    const reading = readings[source.id] ?? UNREAD;
    return { source, reading, reason: source.down ?? (reading.error ? 'could not read usage' : reading.report ? undefined : 'still loading') };
  });
  const included = states.filter((state) => !state.reason);
  const excluded = one ? [] : states.filter((state) => state.reason);
  const reading = one ? readings[one.id] ?? UNREAD : null;
  const report = reading ? reading.report : included.length ? mergeTokenReports(included.map((state) => state.reading.report!)) : null;
  const error = one ? (one.down ? `${one.label}: ${one.down}.` : reading!.error) : !included.length && excluded.every((state) => state.reason !== 'still loading') ? 'No machine is available.' : null;
  const priceFailures = one ? (reading!.priceError ? [one] : []) : included.filter((state) => state.reading.priceError).map((state) => state.source);
  const pricesLoading = (one ? [reading!] : included.map((state) => state.reading)).some((r) => r.prices === null && !r.priceError);
  const shown = report?.periods[period];
  const models = groupUsageModels(shown?.models ?? []);
  const combined = aggregateUsageModels(models, 'All models');
  const saved = one ? estimateCacheSaving(shown?.models ?? [], reading!.prices) : sumCacheSavings(included.map((state) => estimateCacheSaving(state.reading.report!.periods[period].models, state.reading.prices)));
  const unpriced = models.filter((model) => model.cost_usd === null || model.cost_partial).length;
  const names = modelNames(meta);
  const account = accountQuota(quotas);
  const [scrolled, setScrolled] = useState(false);
  return <>
    {sources.map((source) => !source.down && <UsageReader key={source.id} id={source.id} client={source.client} retry={retry} onRead={onRead} />)}
    <PanelHead scrolled={scrolled} className="px-4 pt-3 pb-2">
      <div className="flex items-center gap-1">
        <Popover.Title className="text-display-sm text-ink">Usage</Popover.Title>
        <HelpTip label="Usage">
          <span className="block">Input, Output, and Cache are separate parts of the recorded token split. Cache includes reads and writes. Bars show proportions within each model; totals retain the source-reported count.</span>
          {report && <span className="mt-2 block">UAM tracking started {new Date(report.since).toLocaleDateString(undefined, { dateStyle: 'medium' })}. Days use server time. Includes {report.collection ? 'local harness records and UAM ' : ''}task, subagent, and Background AI usage.</span>}
          {report?.collection && <>
            <span className="mt-2 block">External Copilot usage requires local telemetry files and is included from {dateTime(report.collection.copilot_since)}. Other harness history may start earlier.</span>
            {report.collection.status !== 'ready' && <span className="mt-2 block">{COLLECTION_STATUS[report.collection.status]}</span>}
          </>}
        </HelpTip>
        <span className="flex-1" />
        {account && <span className="mr-1 text-title tabular-nums text-ink">{quotaFace(account)}</span>}
        <Button size="icon-sm" aria-label="Close usage" className="text-muted" onClick={onClose}><X aria-hidden="true" /></Button>
      </div>
    </PanelHead>
    {/* One column: the allowance above the tokens. In All models only the model list scrolls; the whole body scrolls only when the screen is too short for the rest. */}
    <div className="flex min-h-0 flex-1 flex-col overflow-x-hidden overflow-y-auto overscroll-contain px-4 pt-1 pb-4" onScroll={(e) => setScrolled(e.currentTarget.scrollTop > 4)}>
      <div className="flex min-h-0 flex-1 flex-col gap-5">
      <div className="flex min-w-0 shrink-0 flex-col gap-6">
        {quotas.length === 0 && <PanelSection label="Allowance"><p className="text-caption text-muted">Account usage not reported yet.</p></PanelSection>}
        {quotas.map((quota) => <Allowance key={`${quota.provider}:${quota.type}`} quota={quota} now={now} stale={usage?.stale} provider={meta?.providers.find((p) => p.name === quota.provider)?.display_name ?? quota.provider} />)}
      </div>
      <PanelSection label="Tokens" meta="recorded by UAM" className={cn('min-w-0', all ? 'min-h-48 flex-1' : 'shrink-0')} action={machines && (
        // Compact like the period tabs: 24px, with a hit area grown as a small button's.
        <Select aria-label="Machine" value={one ? scope : ALL_MACHINES} onValueChange={setScope} className="relative h-6 w-auto max-w-40 gap-1 px-2 text-caption after:absolute after:-inset-y-1 after:inset-x-0 after:content-[''] pointer-coarse:min-h-6 pointer-coarse:after:-inset-y-2.5" items={[
          { value: ALL_MACHINES, label: 'All machines' },
          ...sources.map((source) => ({ value: source.id, label: source.label })),
        ]} />
      )}>
      <div role="group" aria-label="Usage period" className="flex rounded-sm bg-sunken p-0.5">
        {PERIODS.map(({ key, label }) => <Button key={key} size="sm" className={cn('flex-1 px-1', period === key && 'bg-raised text-ink shadow-raised')} aria-pressed={key === period} onClick={() => setPeriod(key)}>{label}</Button>)}
      </div>
      {error && <div role="alert" className="mt-3 flex items-center gap-2 text-caption text-error">
        <span className="min-w-0 flex-1">{report ? 'Could not refresh usage. Showing the last read. ' : 'Could not load usage. '}{error}</span>
        <Button size="sm" onClick={() => setRetry((n) => n + 1)}>Retry</Button>
      </div>}
      {priceFailures.length > 0 && shown && <div className="mt-3 flex items-center gap-2 text-caption text-muted">
        <span className="min-w-0 flex-1">Could not load token prices{one ? '' : ` for ${priceFailures.map((source) => source.label).join(', ')}`}.</span>
        <Button size="sm" onClick={() => setRetry((n) => n + 1)}>Retry prices</Button>
      </div>}
      {/* A machine left out of All machines is named, never silently skipped. */}
      {shown && excluded.map(({ source, reason }) => <Note key={source.id} className="mt-2 [overflow-wrap:anywhere]">{source.label} not included: {reason}</Note>)}
      {!shown && !error && <p role="status" className="py-4 text-muted">Loading usage…</p>}
      {shown && <>
        <div className="grid grid-cols-3 gap-3 py-2 @max-[22rem]:grid-cols-2">
          <div className="@max-[22rem]:col-span-2"><p className="flex min-h-6 items-center text-caption text-muted">Total tokens</p><p className="mt-1 text-display-md text-ink tabular-nums"><Count value={shown.total.total} /></p></div>
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
            <div className="flex min-h-6 items-center gap-0.5 text-caption text-muted"><span>Cache saving</span>
              <HelpTip label="Cache saving">
                <span className="block">What prompt caching saved: the same tokens with every cached token charged at the standard input rate, less the estimated cost.</span>
                {saved.saving === null ? <span className="mt-2 block">Estimate unavailable: prices or token counts are missing.</span> : saved.partial && <span className="mt-2 block">Partial estimate: some usage has missing prices, token counts or costs.</span>}
                <span className="mt-2 block">Uses current configured prices. Records without prices, token counts or a cost are left out. An estimate, not a refund.</span>
              </HelpTip>
            </div>
            <p className={cn('mt-1 text-muted tabular-nums [overflow-wrap:anywhere]', pricesLoading && models.length ? 'text-caption' : 'text-title')}>{pricesLoading && models.length ? 'Loading…' : costText(saved.saving)}</p>
          </div>
        </div>
        <div className="mb-2 rounded-sm bg-surface px-3 py-1.5" role="group" aria-label="Total token split"><TokenSplitValues value={combined} colors={models.slice(0, 3).map((model) => colors.get(model.model) ?? MODEL_COLORS[0])} /></div>
        <div role="group" aria-label="Usage view" className="mb-2 flex gap-1">
          <Button size="sm" aria-pressed={!all} onClick={() => setAll(false)}>Overview</Button>
          <Button size="sm" aria-pressed={all} onClick={() => setAll(true)}>All models · {models.length}</Button>
        </div>
        {models.length > 0 && <div className="flex items-center justify-between gap-2 pb-1"><h3 className="font-medium text-ink">{all ? 'All models' : models.length > 5 ? 'Top 5 models' : 'Models'}</h3><span className="text-meta text-muted">By token usage</span></div>}
      </>}
    {shown && <>
      {/* Keyed by machine and view, so another machine's rows or the other view cross-fade in (`base`); the totals above change in place. */}
      {/* eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- A labelled scroll region must accept keyboard scrolling. */}
      <div key={`${one?.id ?? ALL_MACHINES}:${all}`} className={cn('min-w-0 animate-fade-in', all && 'min-h-24 flex-1 overflow-x-hidden overflow-y-auto overscroll-contain')} role="region" aria-label="Model usage" tabIndex={all ? 0 : undefined}>

        {models.length === 0 ? <p className="py-4 text-caption text-muted">No usage recorded for this period.</p> : <ModelRows models={models} colors={colors} names={names} limit={all ? undefined : 5} />}
      </div>
    </>}
      </PanelSection>
      </div>
    </div>
    {/* Narrow, the foot wraps rather than cutting "Updated" short. */}
    <PanelFoot className="gap-x-3 gap-y-1 max-[959px]:h-auto max-[959px]:flex-wrap max-[959px]:py-2">
      {report?.collection?.updated_at && <p role="status" className="shrink-0">Updated <time dateTime={report.collection.updated_at} title={dateTime(report.collection.updated_at)}>{timeAgo(report.collection.updated_at)}</time></p>}
      <span className="flex-1" />
      {shown && unpriced > 0 && <p className="shrink-0">{unpriced} {unpriced === 1 ? 'model' : 'models'} unpriced · <a href="#settings" className="text-ink underline underline-offset-2" onClick={(event) => { if (onAddPrices) { event.preventDefault(); onAddPrices(); } onClose(); }}>Add prices</a></p>}
      <Tip label="Estimates include known costs only. They are not provider bills.">
        <button type="button" className="flex shrink-0 items-center gap-1 rounded-xs hover:text-body"><Info aria-hidden="true" className="size-3" />Estimates</button>
      </Tip>
    </PanelFoot>
  </>;
}

const PACE_CHIPS = { healthy: 'bg-success-wash text-success', attention: 'bg-warning-wash text-warning', danger: 'bg-error-wash text-error', muted: 'bg-tint-well text-muted' };

/** "Nov 1": a day of the month, in the browser's locale and time zone, or in `timeZone` (the reset's is UTC, where Copilot's month turns). */
const day = (at: number, timeZone?: string) => new Date(at).toLocaleDateString(undefined, { month: 'short', day: 'numeric', timeZone });

/**
 * One allowance (DESIGN.md Account allowance in Usage): used of the whole with its pace, the month as a
 * bar where it is known (`BurnBar`), then when it resets and where the average pace so far lands.
 */
function Allowance({ quota, now, stale, provider }: Readonly<{ quota: Quota; now: number; stale?: boolean; provider: string }>) {
  const pace = quotaPace(quota, now, stale);
  const burn = stale ? null : quotaBurn(quota, now);
  const unit = quotaLabel(quota.type);
  const n = (v: number) => Math.round(v).toLocaleString('en-US');
  // With the bar its legend says where the pace lands; without it, a row does.
  const projection = !burn && pace.daysAtPace !== null ? `About ${daysText(pace.daysAtPace, 'working day')} left` : null;
  const resetAt = pace.daysUntilReset !== null ? now + pace.daysUntilReset * 86400000 : null;
  return (
    <PanelSection label="Allowance" meta={`${provider} · ${allowanceName(quota.type)}`}>
      {quota.unlimited ? (
        <p className="text-ui text-body">Unlimited {unit}{stale ? ' · previous report; refresh failed' : ''}</p>
      ) : (
        <>
          <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1">
            <p className="tabular-nums">
              <span className="text-display-md text-ink">{n(quota.used)}</span>
              <span className="text-ui text-muted"> / {n(quota.entitlement)} used</span>
              {quota.overage > 0 && <span className="text-ui text-error"> · {n(quota.overage)} over</span>}
              {resetAt !== null && <span className="text-ui text-muted" title={`${dateTime(new Date(resetAt))} · ${daysText(pace.daysUntilReset!)}`}> · Resets {day(resetAt, 'UTC')}</span>}
            </p>
            {!QUIET_PACE.has(pace.label) && (
              <span className={cn('flex h-6 shrink-0 items-center gap-1 rounded-full px-2 text-caption font-medium', PACE_CHIPS[pace.tone])}>
                {pace.tone === 'healthy' ? <Check aria-hidden="true" className="size-3.5" /> : <TriangleAlert aria-hidden="true" className="size-3.5" />}
                {pace.label}
              </span>
            )}
          </div>
          {burn && <BurnBar burn={burn} tone={pace.tone} entitlement={quota.entitlement} />}
          {projection && (
            <dl className="flex flex-col text-ui">
              <div className="flex items-baseline justify-between gap-3 py-1.5">
                <dt className="flex items-center gap-1 text-muted">
                  At this pace
                  <HelpTip label="At this pace">The average spent per working day (Monday to Friday) so far this month, carried on to the reset. Pace allows 5% either way.</HelpTip>
                </dt>
                <dd className="tabular-nums text-ink">{projection}</dd>
              </div>
            </dl>
          )}
        </>
      )}
    </PanelSection>
  );
}

/**
 * The allowance month as one bar, from what the account reports (only the total so far): what is
 * used (the pace tone), that pace carried on to the reset (the same tone, faint; to the end when it
 * runs out first), and a tick at the even pace's budget to date. A legend line under it says each in
 * words; the bar is an image named with the same.
 */
function BurnBar({ burn, tone, entitlement }: Readonly<{ burn: NonNullable<ReturnType<typeof quotaBurn>>; tone: keyof typeof QUOTA_TONES; entitlement: number }>) {
  const n = (share: number) => Math.round(share * entitlement).toLocaleString('en-US');
  const label = `${n(burn.used)} used; ${n(burn.elapsed)} is the even budget to date; at this pace ${burn.runsOut ? `it runs out ${day(burn.runsOut)}` : `about ${n(burn.projected)} by the reset`}.`;
  return (
    <figure className="flex flex-col gap-1.5">
      <div role="img" aria-label={label} className="relative h-2">
        <span className="absolute inset-0 overflow-hidden rounded-full bg-hairline">
          <span className={cn('absolute inset-0 origin-left opacity-30', QUOTA_FILLS[tone])} style={{ transform: `scaleX(${Math.min(1, burn.projected)})` }} />
          <span className={cn('absolute inset-0 origin-left rounded-full', QUOTA_FILLS[tone])} style={{ transform: `scaleX(${Math.min(1, burn.used)})` }} />
        </span>
        <span aria-hidden="true" className="absolute -top-1 -bottom-1 w-0.5 -translate-x-1/2 rounded-full bg-ink" style={{ left: `${burn.elapsed * 100}%` }} />
      </div>
      <figcaption aria-hidden="true" className="flex flex-wrap items-center gap-x-3 gap-y-0.5 text-meta tabular-nums text-muted">
        <span className="flex items-center gap-1"><span className={cn('size-2 rounded-xs', QUOTA_FILLS[tone])} />Used {n(burn.used)}</span>
        <span className="flex items-center gap-1"><span className="h-2.5 w-0.5 rounded-full bg-ink" />Budget to date {n(burn.elapsed)}</span>
        <span className={cn('flex items-center gap-1', burn.runsOut && 'text-error')}><span className={cn('size-2 rounded-xs opacity-30', QUOTA_FILLS[tone])} />{burn.runsOut ? `Runs out ~${day(burn.runsOut)}` : `At this pace ~${n(burn.projected)}`}</span>
        <span className="ml-auto">{entitlement.toLocaleString('en-US')}</span>
      </figcaption>
    </figure>
  );
}

const QUOTA_TONES = { healthy: 'text-success', attention: 'text-warning', danger: 'text-error', muted: 'text-muted' };

function daysText(days: number, unit = 'day'): string {
  if (days < 1) return `less than 1 ${unit}`;
  const count = Math.round(days);
  return `${count} ${unit}${count === 1 ? '' : 's'}`;
}

const QUOTA_FILLS = { healthy: 'bg-success', attention: 'bg-warning', danger: 'bg-error', muted: 'bg-faint' };
/** Pace labels that say nothing the face does not: left out of the footer's line. */
const QUIET_PACE = new Set(['Account usage unavailable', 'Usage pace unavailable', 'Unlimited allowance']);

/** "Premium requests", "AI credits": the allowance's unit, as a label. */
function allowanceName(type: string): string {
  const label = quotaLabel(type).replace(/\bai\b/, 'AI');
  return label.charAt(0).toUpperCase() + label.slice(1);
}

/** Pace words worth a place on the chip: anything but the calm ones, which the tip carries. */
const CHIP_PACE: Record<string, string> = { 'Ahead of pace': 'ahead', 'Allowance exhausted': 'used up', 'Previous quota; refresh failed': 'stale', 'Waiting for the refreshed allowance': 'resetting' };

/** A short meter of what is left: `hairline` track, the fill in the pace tone, drawn with `scaleX`. */
function Meter({ remaining, tone, className }: Readonly<{ remaining: number; tone: keyof typeof QUOTA_FILLS; className?: string }>) {
  return (
    <span aria-hidden="true" className={cn('relative block h-1 overflow-hidden rounded-full bg-hairline', className)}>
      <span className={cn('absolute inset-0 origin-left rounded-full transition-transform duration-300', QUOTA_FILLS[tone])} style={{ transform: `scaleX(${remaining / 100})` }} />
    </span>
  );
}

/**
 * Account allowance is live from SSE; recorded usage reads only while open. `chip` is the sidebar
 * footer's small pill (a meter and the share left, the pace word when it needs saying); `rail` is
 * the collapsed rail's stacked share and meter. Both carry the whole sentence in their tip.
 */
export function UsageButton({ side = 'top', variant = 'rail', className, onAddPrices }: Readonly<{ side?: 'top' | 'right'; variant?: 'chip' | 'rail'; className?: string; onAddPrices?: () => void }>) {
  const { usage } = useApp();
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 60000);
    return () => window.clearInterval(timer);
  }, []);
  const quota = accountQuota(usage?.quotas);
  const pace = quotaPace(quota, now, usage?.stale);
  const known = !!quota && (quota.unlimited || pace.remaining !== null);
  const share = !quota || !known ? '—' : quota.unlimited ? '∞' : `${Math.round(pace.remaining ?? 0)}%`;
  const word = quota ? CHIP_PACE[pace.label] : undefined;
  const description = quota ? `${allowanceName(quota.type)}: ${quotaFace(quota)}. ${pace.label}${!quota.unlimited && pace.daysUntilReset !== null ? `. Resets in ${daysText(pace.daysUntilReset)}` : ''}` : 'Account usage unavailable';
  const descriptionId = useId();
  const [open, setOpen] = useState(false);
  const popup = useRef<HTMLDivElement>(null);
  const onOpenChange = (next: boolean) => setOpen(next);
  const meter = known && !quota.unlimited;
  return <Popover.Root open={open} onOpenChange={onOpenChange}>
    <Tip label={`Usage · ${description}`} side={side} disabled={open}>
      {variant === 'chip' ? (
        // A small pill like a chip: it reads as something to press, and lifts on hover.
        <Popover.Trigger render={<Button data-account-usage size="sm" aria-label="Usage" aria-describedby={descriptionId} className={cn('lift h-7 min-w-0 gap-1.5 rounded-full bg-raised px-2.5 font-normal shadow-raised hover:bg-raised data-popup-open:bg-tint-selected', className)} />}>
          {meter && <Meter remaining={pace.remaining ?? 0} tone={pace.tone} className="w-8 shrink-0" />}
          <span className={cn('shrink-0 text-caption font-medium tabular-nums', known ? 'text-ink' : 'text-muted')}>{known ? (quota.unlimited ? 'Unlimited' : `${share} left`) : 'Usage'}</span>
          {word && <span className={cn('min-w-0 truncate text-caption', QUOTA_TONES[pace.tone])}>{word}</span>}
        </Popover.Trigger>
      ) : (
        // The rail's: the share left in words over a short meter, as wide as the rail allows.
        <Popover.Trigger render={<Button data-account-usage aria-label="Usage" aria-describedby={descriptionId} className={cn('h-auto w-10 flex-col gap-1 px-0 py-1.5', className)} />}>
          <span className={cn('text-caption font-medium leading-none tabular-nums', pace.tone === 'healthy' ? 'text-ink' : QUOTA_TONES[pace.tone])}>{share}</span>
          {meter ? <Meter remaining={pace.remaining ?? 0} tone={pace.tone} className="w-6" /> : <span aria-hidden="true" className="block h-1" />}
        </Popover.Trigger>
      )}
    </Tip>
    <span id={descriptionId} className="sr-only">{description}</span>
    {/* As tall as its content up to 46rem, so a short model list leaves no blank space; past that only the model list scrolls. */}
    <Popover.Content ref={popup} initialFocus={popup} side={side} align={side === 'right' ? 'end' : 'start'} sideOffset={side === 'right' ? 12 : 6} className="w-[36rem] max-w-[calc(100vw-1rem)] max-h-[min(46rem,calc(100dvh-2rem),var(--available-height))] gap-0 overflow-hidden p-0 ">
      {open && <UsageContent now={now} onClose={() => setOpen(false)} onAddPrices={onAddPrices} />}
    </Popover.Content>
  </Popover.Root>;
}
