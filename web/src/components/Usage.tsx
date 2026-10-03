import { ChartNoAxesColumn, X } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { api, describeError, type TokenCounts, type TokenPeriodKey, type TokenUsageReport } from '../api';
import { cn } from '../lib/cn';
import { compactTokens } from '../lib/cost';
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

function costText(value: number | null): string {
  if (value === null) return 'Unpriced';
  if (value > 0 && value < 0.01) return '<$0.01';
  return value.toLocaleString('en-US', { style: 'currency', currency: 'USD', minimumFractionDigits: 2, maximumFractionDigits: 2 });
}

function Count({ value }: Readonly<{ value: number }>) {
  return <span title={`${value.toLocaleString('en-US')} tokens`}>{compactTokens(value)}</span>;
}

function Counts({ value }: Readonly<{ value: TokenCounts }>) {
  return <>
    <td className="py-2 pl-3 text-right tabular-nums"><Count value={value.input} /></td>
    <td className="py-2 pl-3 text-right tabular-nums"><Count value={value.output} /></td>
    <td className="py-2 pl-3 text-right tabular-nums">
      <Tip label={`Read: ${value.cache_read.toLocaleString('en-US')} · Write: ${value.cache_write.toLocaleString('en-US')}`} openOnClick>
        <Button size="sm" className="-mr-1 h-auto px-1 py-0 text-ui font-normal tabular-nums pointer-coarse:min-h-11 pointer-coarse:min-w-11 pointer-coarse:after:inset-0" aria-label={`Cache: ${value.cache_read.toLocaleString('en-US')} read, ${value.cache_write.toLocaleString('en-US')} written`}>
          {compactTokens(value.cache_read + value.cache_write)}
        </Button>
      </Tip>
    </td>
  </>;
}

function UsageContent({ onClose }: Readonly<{ onClose: () => void }>) {
  const [period, setPeriod] = useState<TokenPeriodKey>('today');
  const [report, setReport] = useState<TokenUsageReport | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    let current = true;
    let pending = false;
    const read = async () => {
      if (pending) return;
      pending = true;
      try {
        const result = await api.tokenUsage();
        if (current) { setReport(result); setError(null); }
      } catch (e) {
        if (current) setError(describeError(e));
      } finally { pending = false; }
    };
    void read();
    const timer = window.setInterval(() => void read(), 15000);
    return () => { current = false; window.clearInterval(timer); };
  }, [retry]);
  const shown = report?.periods[period];
  return <>
    <div className="flex items-center justify-between gap-3">
      <div className="flex items-center gap-1">
        <Popover.Title>Usage</Popover.Title>
        <HelpTip label="Usage">
          <span className="block">Cache is read + write tokens, already included in Input.</span>
          {report && <span className="mt-2 block">UAM tracking started {new Date(report.since).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' })}. Days use server time. Includes {report.collection ? 'local harness records and UAM ' : ''}task, subagent, and Background AI usage.</span>}
          {report?.collection && <>
            <span className="mt-2 block">External Copilot usage requires local telemetry files and is included from {new Date(report.collection.copilot_since).toLocaleString()}. Other harness history may start earlier.</span>
            {report.collection.status !== 'ready' && <span className="mt-2 block">{COLLECTION_STATUS[report.collection.status]}</span>}
          </>}
        </HelpTip>
      </div>
      <Button size="icon-sm" aria-label="Close usage" onClick={onClose}><X aria-hidden="true" /></Button>
    </div>
    {report?.collection?.updated_at && <p role="status" className="text-caption text-muted">Last fetched {new Date(report.collection.updated_at).toLocaleString()}.</p>}
    <div role="group" aria-label="Usage period" className="flex gap-1 py-2">
      {PERIODS.map(({ key, label }) => <Button key={key} size="sm" className="flex-1" aria-pressed={key === period} onClick={() => setPeriod(key)}>{label}</Button>)}
    </div>
    {error && <div role="alert" className="flex items-center gap-2 text-caption text-error">
      <span className="min-w-0 flex-1">{report ? 'Could not refresh usage. Showing the last read. ' : 'Could not load usage. '}{error}</span>
      <Button size="sm" onClick={() => setRetry((n) => n + 1)}>Retry</Button>
    </div>}
    {!shown && !error && <p role="status" className="py-4 text-muted">Loading usage…</p>}
    {shown && <>
      <div className="flex items-baseline justify-between gap-4 pb-2">
        <span className="text-muted">Total tokens</span>
        <span className="text-title font-semibold text-ink tabular-nums"><Count value={shown.total.total} /></span>
      </div>
      <div className="flex items-baseline justify-between gap-4 pb-2">
        <span className="flex items-center gap-1 text-muted">Estimated cost
          <HelpTip label="Estimated cost">
            <span className="block">Costs use current base token prices or source-reported amounts when available. These are not provider bills.</span>
            {shown.unpriced_models > 0 && <span className="mt-2 block">{shown.unpriced_models} {shown.unpriced_models === 1 ? 'model is' : 'models are'} unpriced. Add prices in Settings → Models → Token costs.</span>}
          </HelpTip>
        </span>
        <span className="font-medium text-ink tabular-nums">{costText(shown.cost_usd)}{shown.cost_usd !== null && (shown.unpriced_models > 0 || shown.models.some((model) => model.cost_partial)) ? ' · partial' : ''}</span>
      </div>
      <div className="min-h-0 overflow-y-auto overscroll-contain">
        <table className="w-full table-fixed text-ui">
          <caption className="sr-only">{PERIODS.find((p) => p.key === period)?.label} token usage by model</caption>
          <thead className="sticky top-0 bg-raised text-caption text-muted">
            <tr><th scope="col" className="w-2/5 py-1 text-left font-normal">Model</th>{['Input', 'Output', 'Cache'].map((name) => <th key={name} scope="col" className="py-1 pl-3 text-right font-normal">{name}</th>)}</tr>
          </thead>
          <tbody>
            {shown.models.map((model) => <tr key={`${model.provider}/${model.model}`}>
              <th scope="row" className="py-2 text-left font-normal">
                <span className="block truncate text-ink" title={model.model || 'Unspecified model'}>{model.model || 'Unspecified model'}</span>
                <span className="block truncate text-meta text-muted">{model.provider}</span>
                <span className="block truncate text-caption text-muted tabular-nums" title="Estimated or source-reported cost in USD">{costText(model.cost_usd)}{model.cost_partial && model.cost_usd !== null ? ' · partial' : ''}</span>
              </th>
              <Counts value={model} />
            </tr>)}
          </tbody>
          <tfoot className="font-medium text-ink"><tr><th scope="row" className="py-2 text-left">All models</th><Counts value={shown.total} /></tr></tfoot>
        </table>
        {shown.models.length === 0 && <p className="py-3 text-caption text-muted">No usage recorded for this period.</p>}
      </div>
    </>}
  </>;
}

/** Workspace-wide recorded tokens. Reads only while the popover is open. */
export function UsageButton({ side = 'top', className }: Readonly<{ side?: 'top' | 'right'; className?: string }>) {
  const [open, setOpen] = useState(false);
  const popup = useRef<HTMLDivElement>(null);
  return <Popover.Root open={open} onOpenChange={setOpen}>
    <Tip label="Usage" side={side} disabled={open}>
      <Popover.Trigger render={<Button size="icon" aria-label="Usage" className={cn('text-muted', className)} />}>
        <ChartNoAxesColumn aria-hidden="true" />
      </Popover.Trigger>
    </Tip>
    <Popover.Content ref={popup} initialFocus={popup} side={side} className="w-[28rem] max-w-[calc(100vw-1rem)] max-h-[min(36rem,calc(100dvh-2rem))] gap-1 overflow-y-auto p-4">
      {open && <UsageContent onClose={() => setOpen(false)} />}
    </Popover.Content>
  </Popover.Root>;
}
