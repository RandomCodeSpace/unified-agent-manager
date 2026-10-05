import type { Meta, TokenCounts } from '../api';
import { cn } from '../lib/cn';
import { compactTokens } from '../lib/cost';
import { aggregateUsageModels, cacheHitRate, splitTokens, type UsageModel } from '../lib/token-usage';
import { HelpTip } from './ui/tooltip';

export const MODEL_COLORS = ['bg-badge-blue', 'bg-badge-orange', 'bg-badge-teal', 'bg-badge-pink', 'bg-badge-violet', 'bg-badge-cyan', 'bg-badge-green', 'bg-badge-amber', 'bg-badge-lime', 'bg-muted', 'bg-ink'];
const PARTS = [
  { key: 'input', label: 'Input', shade: 'opacity-100' },
  { key: 'output', label: 'Output', shade: 'opacity-60' },
  { key: 'cache', label: 'Cache', shade: 'opacity-25' },
] as const;
type Colors = ReadonlyMap<string, string>;
type Names = ReadonlyMap<string, string>;
const modelName = (model: UsageModel, names?: Names) => names?.get(model.model) || model.model || 'Unspecified model';

/** Model IDs to the names the providers' catalogs give them, as Settings shows them. */
export function modelNames(meta: Meta | null | undefined): Names {
  const names = new Map<string, string>();
  for (const provider of meta?.providers ?? []) for (const model of provider.models) if (model.name && !names.has(model.id)) names.set(model.id, model.name);
  return names;
}

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

/** The totals with a key to the bars: each part's swatch is that part's shade of the leading models' colours. */
export function TokenSplitValues({ value, colors = [] }: Readonly<{ value: TokenCounts; colors?: readonly string[] }>) {
  const split = splitTokens(value);
  const swatches = colors.length ? colors : ['bg-muted'];
  return <dl className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1 text-meta">
    {PARTS.map(({ key, label, shade }) => <div key={key} className="flex items-baseline gap-1.5">
      <dt className="flex items-center gap-1.5 text-muted"><span aria-hidden="true" className="flex shrink-0 overflow-hidden rounded-xs">{swatches.map((color, i) => <span key={i} className={cn('h-2 w-1.5', color, shade)} />)}</span>{label}</dt>
      <dd className="text-caption text-ink tabular-nums"><Count value={split[key]} /></dd>
    </div>)}
  </dl>;
}

function TokenBar({ model, color, names }: Readonly<{ model: UsageModel; color: string; names?: Names }>) {
  const split = splitTokens(model);
  const total = split.input + split.output + split.cache;
  return <div role="img" aria-label={`${modelName(model, names)} token split: ${PARTS.map(({ key, label }) => `${label} ${split[key].toLocaleString('en-US')}`).join('; ')}`} className="flex h-2 w-full overflow-hidden rounded-xs bg-sunken">
    {PARTS.map(({ key, shade }) => <span key={key} className={cn('h-full', color, shade)} style={{ width: `${split[key] / Math.max(1, total) * 100}%` }} />)}
  </div>;
}

function TokenSplitTip({ model, names }: Readonly<{ model: UsageModel; names?: Names }>) {
  const split = splitTokens(model);
  const total = split.input + split.output + split.cache;
  const name = modelName(model, names);
  return <HelpTip label={`${name} token split`}>
    <span className="block font-medium [overflow-wrap:anywhere]">{name}</span>
    {name !== model.model && model.model && <span className="block text-meta opacity-70 [overflow-wrap:anywhere]">{model.model}</span>}
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

function ModelName({ model, names }: Readonly<{ model: UsageModel; names?: Names }>) {
  const name = modelName(model, names);
  return <div className="flex min-w-0 flex-1 items-center gap-1">
    <span className="min-w-0 truncate text-ink" title={model.model && name !== model.model ? `${name} · ${model.model}` : name}>{name}</span>
    <TokenSplitTip model={model} names={names} />
  </div>;
}

/**
 * Model rows, one line each (name, then cost, tokens and cache hit) over the model's token bar. With
 * `limit`, the rows past it fold into one "Other n models" row; without it, every model shows.
 */
export function ModelRows({ models, colors, names, limit }: Readonly<{ models: UsageModel[]; colors: Colors; names?: Names; limit?: number }>) {
  const remainder = limit === undefined ? [] : models.slice(limit);
  const rows = remainder.length ? [...models.slice(0, limit), aggregateUsageModels(remainder, `Other ${remainder.length} models`)] : models;
  const other = remainder.length ? rows.length - 1 : -1;
  return <figure aria-label="Token proportions by model, with total tokens and estimated cost in USD" className="pb-1 pt-1">
    <div className="space-y-1">
      {rows.map((model, i) => <div key={i === other ? 'other' : model.model}>
        <div className="mb-1 flex min-w-0 items-center gap-2 text-caption">
          <ModelName model={model} names={names} />
          {/* One line: cost, tokens, cache hit; the tip says each in full. */}
          <span className="shrink-0 whitespace-nowrap text-right tabular-nums" title={`Estimated cost ${costText(model.cost_usd)} in USD · ${compactTokens(model.total)} tokens · ${cacheHitText(model)} cache hit`}>{costText(model.cost_usd)} <span className="text-muted">· <Count value={model.total} /> · {cacheHitText(model)} cached</span></span>
        </div>
        <TokenBar model={model} names={names} color={i === other ? 'bg-faint' : colors.get(model.model) ?? MODEL_COLORS[0]} />
      </div>)}
    </div>
    <figcaption className="sr-only">Each full-width bar shows the model's recorded Input, Output, and Cache proportions.</figcaption>
  </figure>;
}
