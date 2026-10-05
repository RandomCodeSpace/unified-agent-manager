import { useEffect, useState, type SubmitEvent } from 'react';
import { api, describeError, type TokenPrice, type TokenPriceCatalog } from '../api';
import { useApp } from './common';
import { Field } from './TaskDefaults';
import { Button } from './ui/button';
import { Input } from './ui/input';
import { Select } from './ui/select';

const FIELDS = [
  { key: 'input', label: 'Input', required: true },
  { key: 'output', label: 'Output', required: true },
  { key: 'cache_read', label: 'Cache read', required: false },
  { key: 'cache_write', label: 'Cache write', required: false },
] as const;
type Draft = Record<keyof TokenPrice, string>;
const rowKey = (row: { provider: string; model: string }) => JSON.stringify([row.provider, row.model]);

function PriceForm({ row, onSaved }: Readonly<{ row: TokenPriceCatalog['models'][number]; onSaved: () => void }>) {
  const { settings, dispatch } = useApp();
  const [draft, setDraft] = useState<Draft>(() => ({ input: String(row.rates?.input ?? ''), output: String(row.rates?.output ?? ''), cache_read: String(row.rates?.cache_read ?? ''), cache_write: String(row.rates?.cache_write ?? '') }));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const valid = FIELDS.every(({ key, required }) => draft[key].trim() === '' ? !required : Number.isFinite(Number(draft[key])) && Number(draft[key]) >= 0 && Number(draft[key]) <= 1e9);

  async function save(rates: TokenPrice | null) {
    setBusy(true);
    setError(null);
    const models = { ...settings.token_prices?.[row.provider] };
    if (rates) models[row.model] = rates;
    else delete models[row.model];
    const token_prices = { ...settings.token_prices, [row.provider]: models };
    if (Object.keys(models).length === 0) delete token_prices[row.provider];
    try {
      const next = await api.updateWebSettings({ token_prices });
      dispatch({ type: 'settings', settings: next });
      onSaved();
    } catch (e) { setError(`Could not save token prices: ${describeError(e)}`); }
    finally { setBusy(false); }
  }

  function submit(event: SubmitEvent) {
    event.preventDefault();
    if (!valid) return;
    void save({ input: Number(draft.input), output: Number(draft.output),
      ...(draft.cache_read.trim() !== '' ? { cache_read: Number(draft.cache_read) } : {}),
      ...(draft.cache_write.trim() !== '' ? { cache_write: Number(draft.cache_write) } : {}),
    });
  }

  return <form onSubmit={submit} className="flex max-w-xl flex-col gap-3">
    <p className="text-caption text-muted">{row.source === 'manual' ? 'Manual prices' : row.source === 'bundled' ? 'Bundled LiteLLM prices' : 'Unpriced. Add input and output prices to estimate cost.'}</p>
    <div className="grid grid-cols-2 gap-3">
      {FIELDS.map(({ key, label, required }) => <Field key={key} id={`token-price-${key}`} label={label} hint={!required ? 'Optional. Leave blank to use the input price.' : undefined}>
        <Input id={`token-price-${key}`} type="number" min="0" max="1000000000" step="any" required={required} disabled={busy} value={draft[key]} placeholder={required ? 'USD / 1M tokens' : 'Input rate'} onChange={(event) => setDraft((value) => ({ ...value, [key]: event.target.value }))} />
      </Field>)}
    </div>
    <p className="text-caption text-muted">Cache prices are optional. Blank uses the Input rate; 0 means free.</p>
    {error && <p role="alert" className="text-caption text-error">{error}</p>}
    <div className="flex flex-wrap gap-2">
      <Button type="submit" variant="secondary" size="sm" disabled={!valid || busy} loading={busy}>Save token prices</Button>
      {row.source === 'manual' && <Button size="sm" disabled={busy} onClick={() => void save(null)}>Remove override</Button>}
    </div>
  </form>;
}

export function TokenPricing() {
  const [catalog, setCatalog] = useState<TokenPriceCatalog | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState('');
  const [showAll, setShowAll] = useState(false);
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    let current = true;
    api.tokenPrices().then((result) => { if (current) { setCatalog(result); setError(null); } }).catch((e: unknown) => { if (current) setError(describeError(e)); });
    return () => { current = false; };
  }, [revision]);
  const { meta } = useApp();
  const models = catalog?.models.filter((model) => showAll || !model.rates) ?? [];
  // The catalog also holds models seen only in usage records; start on one the Models list offers.
  const listed = (row: { provider: string; model: string }) => !!meta?.providers.some((p) => p.name === row.provider && p.models.some((m) => m.id === row.model));
  const row = models.find((model) => rowKey(model) === selected) ?? models.find(listed) ?? models[0];
  return <div className="flex flex-col gap-3">
    <p className="text-caption text-muted">USD per million tokens. Usage estimates use these current base rates for every period, not provider bills or Copilot credits.</p>
    <label className="flex min-h-8 items-center gap-2 self-start text-ui text-ink">
      <input type="checkbox" className="size-3.5 accent-accent" checked={showAll} onChange={(event) => setShowAll(event.target.checked)} />
      Show all models
    </label>
    {error && <div role="alert" className="flex items-center gap-2 text-caption text-error">Could not load prices: {error}<Button size="sm" onClick={() => setRevision((n) => n + 1)}>Retry prices</Button></div>}
    {!catalog && !error && <p role="status" className="text-caption text-muted">Loading token prices…</p>}
    {catalog && !row && <p role="status" className="text-caption text-muted">{catalog.models.length ? 'All models have prices. Select Show all models to edit them.' : 'No models available yet.'}</p>}
    {row && <>
      <Field id="token-price-model" label="Model">
        <Select id="token-price-model" className="max-w-xl" value={rowKey(row)} onValueChange={setSelected} items={models.map((model) => ({ value: rowKey(model), label: `${model.provider} · ${model.model}`, description: model.source === 'unpriced' ? 'Unpriced' : model.source === 'manual' ? 'Manual prices' : 'Bundled prices' }))} />
      </Field>
      <PriceForm key={`${rowKey(row)}/${revision}`} row={row} onSaved={() => { setSelected(rowKey(row)); setCatalog(null); setRevision((n) => n + 1); }} />
    </>}
    {catalog && <p className="text-meta text-muted">Pricing snapshot <a className="underline underline-offset-2" href={`https://github.com/BerriAI/litellm/blob/${catalog.commit}/model_prices_and_context_window.json`} target="_blank" rel="noreferrer">{catalog.commit.slice(0, 7)}</a>. Unmatched models need manual prices.</p>}
  </div>;
}
