import { ChevronDown } from 'lucide-react';
import type { TokenCounts } from '../api';
import { cn } from '../lib/cn';
import { compactTokens } from '../lib/cost';
import { aggregateUsageModels, cacheHitRate, splitTokens, type UsageModel } from '../lib/token-usage';
import { Button } from './ui/button';
import { HelpTip } from './ui/tooltip';

export const MODEL_COLORS = ['bg-badge-blue', 'bg-badge-orange', 'bg-badge-teal', 'bg-badge-pink', 'bg-badge-violet', 'bg-badge-cyan', 'bg-badge-green', 'bg-badge-amber', 'bg-badge-lime', 'bg-muted', 'bg-ink'];
const PARTS = [
  { key: 'input', label: 'Input', shade: 'opacity-100' },
  { key: 'output', label: 'Output', shade: 'opacity-60' },
  { key: 'cache', label: 'Cache', shade: 'opacity-25' },
] as const;
export type UsageSort = 'tokens' | 'cost';
type Colors = ReadonlyMap<string, string>;
const modelName = (model: UsageModel) => model.model || 'Unspecified model';

function cacheHitText(model: UsageModel): string {
  return cacheHitRate(model)?.toLocaleString('en-US', { style: 'percent', maximumFractionDigits: 1 }) ?? '—';
}

export function costText(value: number | null): string {
  if (value === null) return '—';
  if (value >= 1e6) {
    const scale = value >= 1e9 ? 1e9 : 1e6;
    return `${(value / scale).toLocaleString('en-US', { style: 'currency', currency: 'USD', minimumFractionDigits: 0, maximumFractionDigits: 2 })}${scale === 1e9 ? 'B' : 'M'}`;
  }
  return value > 0 && value < 0.01 ? '<$0.01' : value.toLocaleString('en-US', { style: 'currency', currency: 'USD' });
}

export function Count({ value }: Readonly<{ value: number }>) {
  return <span title={`${compactTokens(value)} tokens`}>{compactTokens(value)}</span>;
}

export function TokenSplitValues({ value }: Readonly<{ value: TokenCounts }>) {
  const split = splitTokens(value);
  return <dl className="grid grid-cols-3 gap-2 text-meta">
    {PARTS.map(({ key, label, shade }) => <div key={key}>
      <dt className="flex items-center gap-1.5 text-muted"><span aria-hidden="true" className={cn('size-2 shrink-0 rounded-xs bg-muted', shade)} />{label}</dt>
      <dd className="mt-1 text-caption text-ink tabular-nums"><Count value={split[key]} /></dd>
    </div>)}
  </dl>;
}

function TokenBar({ model, color }: Readonly<{ model: UsageModel; color: string }>) {
  const split = splitTokens(model);
  const total = split.input + split.output + split.cache;
  return <div role="img" aria-label={`${modelName(model)} token split: ${PARTS.map(({ key, label }) => `${label} ${split[key].toLocaleString('en-US')}`).join('; ')}`} className="flex h-2 w-full overflow-hidden rounded-xs bg-sunken">
    {PARTS.map(({ key, shade }) => <span key={key} className={cn('h-full', color, shade)} style={{ width: `${split[key] / Math.max(1, total) * 100}%` }} />)}
  </div>;
}

function TokenSplitTip({ model }: Readonly<{ model: UsageModel }>) {
  const split = splitTokens(model);
  const total = split.input + split.output + split.cache;
  return <HelpTip label={`${modelName(model)} token split`}>
    <span className="block font-medium [overflow-wrap:anywhere]">{modelName(model)}</span>
    <span className="mt-2 block space-y-1 tabular-nums">
      {PARTS.map(({ key, label }) => <span key={key} className="flex justify-between gap-4">
        <span>{label}</span><span>{compactTokens(split[key])} <span className="opacity-70">({Math.round(split[key] / Math.max(1, total) * 100)}%)</span></span>
      </span>)}
    </span>
    <span className="mt-2 block text-meta opacity-70">Cache: {compactTokens(model.cache_read)} read · {compactTokens(model.cache_write)} written.</span>
    <span className="mt-2 block">{model.input > 0 ? `Cache hit: ${cacheHitText(model)} of input tokens served by cache reads. Cache writes are not hits.` : 'Cache hit unavailable: no recorded input tokens.'}</span>
    {model.total !== total && <span className="mt-2 block">Reported total: {compactTokens(model.total)} tokens. The available split accounts for {compactTokens(total)} tokens.</span>}
    {model.cost_usd === null ? <span className="mt-2 block">Cost unavailable: prices or token counts are missing for this model.</span> : model.cost_partial && <span className="mt-2 block">Partial cost estimate: includes known costs only. Some usage is unpriced.</span>}
    {model.tools.length > 1 && <span className="mt-2 block space-y-1 pt-2">
      <span className="block text-meta opacity-70">By tool</span>
      {model.tools.map((tool) => <span key={tool.provider} className="flex justify-between gap-4 tabular-nums">
        <span className="min-w-0 [overflow-wrap:anywhere]">{tool.provider}</span><span className="shrink-0"><Count value={tool.total} /> tokens</span>
      </span>)}
    </span>}
  </HelpTip>;
}

function ModelName({ model }: Readonly<{ model: UsageModel }>) {
  return <div className="flex min-w-0 flex-1 items-center gap-1">
    <span className="min-w-0 truncate text-ink" title={modelName(model)}>{modelName(model)}</span>
    <TokenSplitTip model={model} />
  </div>;
}

export function ModelOverview({ models, colors }: Readonly<{ models: UsageModel[]; colors: Colors }>) {
  const remainder = models.slice(5);
  const rows = models.slice(0, 5);
  if (remainder.length) rows.push(aggregateUsageModels(remainder, `Other ${remainder.length} models`));
  return <figure aria-label="Token proportions by model, with total tokens and estimated cost in USD" className="pb-1 pt-1">
    <div className="space-y-3">
      {rows.map((model, i) => <div key={i === 5 ? 'other' : model.model}>
        <div className="mb-1 flex min-w-0 items-center gap-2 text-caption">
          <ModelName model={model} />
          <span className="min-w-0 max-w-[60%] text-right tabular-nums [overflow-wrap:anywhere]" title={`Estimated cost in USD; ${compactTokens(model.total)} tokens`}>{costText(model.cost_usd)} <span className="text-muted">(<Count value={model.total} /> tokens)</span><span className="inline-block text-muted"> · {cacheHitText(model)} cache hit</span></span>
        </div>
        <TokenBar model={model} color={i === 5 ? 'bg-faint' : colors.get(model.model) ?? MODEL_COLORS[0]} />
      </div>)}
    </div>
    <figcaption className="sr-only">Each full-width bar shows the model's recorded Input, Output, and Cache proportions.</figcaption>
  </figure>;
}

export function ModelTable({ models, colors, period, sort, onSort }: Readonly<{ models: UsageModel[]; colors: Colors; period: string; sort: UsageSort; onSort: (sort: UsageSort) => void }>) {
  const sorted = [...models].sort((a, b) => (sort === 'tokens' ? b.total - a.total : (b.cost_usd ?? -1) - (a.cost_usd ?? -1)) || a.model.localeCompare(b.model));
  return <table className="w-full table-fixed text-caption">
    <caption className="sr-only">{period} token usage by model</caption>
    <thead className="sticky top-0 z-10 bg-raised text-meta text-muted">
      <tr>
        <th scope="col" className="w-[33%] py-2 text-left font-normal">Model</th>
        {(['tokens', 'cost'] as const).map((key) => <th key={key} scope="col" aria-sort={sort === key ? 'descending' : 'none'} className={cn('py-2 text-right font-normal', key === 'tokens' ? 'w-[20%]' : 'w-[30%]')}>
          <Button size="sm" className="h-7 gap-0.5 px-0 text-meta" onClick={() => onSort(key)}>{key === 'tokens' ? 'Tokens' : 'Est. cost'}{sort === key && <ChevronDown aria-hidden="true" className="size-3!" />}</Button>
        </th>)}
        <th scope="col" className="py-2 pl-1 text-right font-normal">Cache hit</th>
      </tr>
    </thead>
    {sorted.map((model) => <tbody key={model.model}>
      <tr className="hover:bg-surface">
        <th scope="row" className="py-1.5 pr-2 text-left font-normal"><ModelName model={model} /></th>
        <td className="py-1.5 pl-1 text-right tabular-nums"><Count value={model.total} /></td>
        <td className="py-1.5 pl-1 text-right tabular-nums [overflow-wrap:anywhere]">{costText(model.cost_usd)}</td>
        <td className="py-1.5 pl-1 text-right tabular-nums">{cacheHitText(model)}</td>
      </tr>
      <tr><td colSpan={4} className="pb-3"><TokenBar model={model} color={colors.get(model.model) ?? MODEL_COLORS[0]} /></td></tr>
    </tbody>)}
  </table>;
}
