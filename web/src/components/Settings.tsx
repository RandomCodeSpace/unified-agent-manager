import { X } from 'lucide-react';
import { useRef, useState, type FormEvent, type ReactNode } from 'react';
import { api, describeError, type CustomModel, type Model, type SendDefault, type Settings } from '../api';
import { Note, Spinner, useApp } from './common';
import { Field } from './TaskDefaults';
import { customProviders, matchingIds, withProvider, type CustomProvider } from '../lib/customModels';
import { modelCostLine } from '../lib/cost';
import { modelChoices } from '../lib/models';
import { Select } from './ui/select';
import { Switch } from './ui/switch';
import { Button } from './ui/button';
import { Chip } from './ui/chip';
import { Input } from './ui/input';
import { Segmented } from './ui/segmented';
import { Tip } from './ui/tooltip';

/** One titled group of settings; a new group is another `Section` below the last. */
function Section({ id, title, children }: { id: string; title: string; children: ReactNode }) {
  return (
    <section aria-labelledby={`${id}-title`} className="flex flex-col gap-3 border-t border-hairline py-4 first:border-t-0 first:pt-0">
      <h2 id={`${id}-title`} className="text-title text-ink">
        {title}
      </h2>
      {children}
    </section>
  );
}

/** A label and its help on the left, the control on the right; stacked on a phone. */
function Row({ id, label, help, children }: { id: string; label: string; help: ReactNode; children: ReactNode }) {
  return (
    <div className="grid items-start gap-x-6 gap-y-2 sm:grid-cols-[minmax(0,1fr)_auto]">
      <div className="flex min-w-0 max-w-3xl flex-col gap-1">
        <span id={`${id}-label`} className="text-ui font-medium text-ink">
          {label}
        </span>
        <Note id={`${id}-help`}>{help}</Note>
      </div>
      {children}
    </div>
  );
}

/** The provider being added or edited; `original` is its saved name, none for a new one. */
interface ProviderDraft {
  original?: string;
  name: string;
  base_url: string;
  api_key_env: string;
  /** The IDs offered in the checklist: loaded from the endpoint, saved, or typed in. */
  ids: string[];
  selected: string[];
  query: string;
  manual: string;
}

const newProvider: ProviderDraft = { name: '', base_url: '', api_key_env: '', ids: [], selected: [], query: '', manual: '' };

/**
 * Custom (BYOM) models, by provider: an OpenAI-compatible endpoint whose models are chosen
 * from what it lists (the service loads the list) or typed in. The key never passes through
 * here; each provider names the service environment variable that holds it.
 */
function CustomModels({ models, disabled, onSave }: { models: CustomModel[]; disabled: boolean; onSave: (next: CustomModel[]) => Promise<boolean> }) {
  const [draft, setDraft] = useState<ProviderDraft | null>(null);
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);
  const providers = customProviders(models);
  const busy = disabled || loading;

  function edit(p?: CustomProvider) {
    setLoadError(null);
    setDraft(p ? { ...newProvider, original: p.name, name: p.name, base_url: p.base_url, api_key_env: p.api_key_env, ids: p.models.map((m) => m.model_id), selected: p.models.map((m) => m.model_id) } : newProvider);
  }
  async function load(d: ProviderDraft) {
    setLoading(true);
    setLoadError(null);
    try {
      const res = await api.discoverModels({ base_url: d.base_url.trim(), api_key_env: d.api_key_env.trim() });
      setDraft((cur) => cur && { ...cur, ids: [...new Set([...res.models, ...cur.selected])].sort() });
      if (res.truncated) setLoadError(`Showing the first ${res.models.length} models the endpoint lists.`);
    } catch (e) {
      setLoadError(`Could not load the models: ${describeError(e)}`);
    } finally {
      setLoading(false);
    }
  }
  async function save(e: FormEvent, d: ProviderDraft) {
    e.preventDefault();
    const provider = { name: d.name.trim(), base_url: d.base_url.trim(), api_key_env: d.api_key_env.trim() };
    if (await onSave(withProvider(models, d.original, provider, d.selected))) setDraft(null);
  }
  const field = (d: ProviderDraft, key: 'name' | 'base_url' | 'api_key_env', label: string, placeholder: string) => (
    <Field id={`custom-${key}`} label={label}>
      <Input
        id={`custom-${key}`}
        className="font-mono text-code-sm"
        spellCheck={false}
        autoComplete="off"
        required
        placeholder={placeholder}
        disabled={busy}
        value={d[key]}
        onChange={(e) => setDraft({ ...d, [key]: e.target.value })}
      />
    </Field>
  );

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-3">
        <h3 className="flex-1 text-ui font-medium">Custom models</h3>
        {!draft && (
          <Button size="sm" variant="secondary" disabled={disabled} onClick={() => edit()}>
            Add provider
          </Button>
        )}
      </div>
      <Note>
        OpenAI-compatible endpoints, offered with GitHub Copilot's models. The API key stays in the service's environment: export it as a variable named UAM_BYOM_&lt;NAME&gt; where the
        service starts (for example in ~/.bashrc), restart the service, and name that variable here.
      </Note>
      {providers.map((p) => (
        <section key={p.name} aria-label={`${p.name} models`} className="flex flex-col border-b border-hairline py-2">
          <div className="flex min-h-8 items-center gap-2">
            <div className="flex min-w-0 flex-1 flex-col">
              <span className="text-ui font-medium text-ink">{p.name}</span>
              <span className="min-w-0 break-all font-mono text-meta text-muted">{p.base_url}</span>
              <Note tone={p.key_present ? 'muted' : 'warn'}>{p.key_present ? `Key from ${p.api_key_env}` : `${p.api_key_env} is not set in the service's environment`}</Note>
            </div>
            <Button size="sm" disabled={busy || !!draft} onClick={() => edit(p)}>
              Edit
            </Button>
            <Button size="sm" variant="danger" disabled={busy} aria-label={`Remove provider ${p.name}`} onClick={() => void onSave(withProvider(models, p.name, p, []))}>
              Remove
            </Button>
          </div>
          {p.models.map((m) => (
            <div key={m.model_id} className="flex min-h-8 items-center gap-2 pl-3">
              <span className="min-w-0 flex-1 truncate text-ui text-ink" title={`${p.name}/${m.model_id}`}>
                {m.display_name || m.model_id}
                {m.display_name && m.display_name !== m.model_id && <span className="ml-2 font-mono text-meta text-muted">{m.model_id}</span>}
              </span>
              <Button size="sm" variant="danger" disabled={busy} aria-label={`Remove ${p.name}/${m.model_id}`} onClick={() => void onSave(withProvider(models, p.name, p, p.models.filter((o) => o !== m).map((o) => o.model_id)))}>
                Remove
              </Button>
            </div>
          ))}
        </section>
      ))}
      {draft && (
        <form aria-label={draft.original ? `Edit provider ${draft.original}` : 'Add a provider'} className="flex flex-col gap-3 border-b border-hairline py-2" onSubmit={(e) => void save(e, draft)}>
          <div className="grid grid-cols-1 items-end gap-3 sm:grid-cols-3">
            {field(draft, 'name', 'Provider name', 'ollama')}
            {field(draft, 'base_url', 'Base URL', 'https://ollama.com/v1')}
            {field(draft, 'api_key_env', 'API key variable', 'UAM_BYOM_OLLAMA')}
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Button size="sm" variant="secondary" loading={loading} disabled={busy || !draft.base_url.trim() || !draft.api_key_env.trim()} onClick={() => void load(draft)}>
              Load models
            </Button>
            <Input
              aria-label="Search models"
              size="md"
              className="w-48 font-mono text-code-sm"
              type="search"
              placeholder="Search"
              disabled={busy || !draft.ids.length}
              value={draft.query}
              onChange={(e) => setDraft({ ...draft, query: e.target.value })}
            />
            <Button size="sm" disabled={busy || !draft.ids.length} onClick={() => setDraft({ ...draft, selected: [...new Set([...draft.selected, ...matchingIds(draft.ids, draft.query)])] })}>
              Select all
            </Button>
            <Button size="sm" disabled={busy || !draft.selected.length} onClick={() => setDraft({ ...draft, selected: draft.selected.filter((id) => !matchingIds(draft.ids, draft.query).includes(id)) })}>
              None
            </Button>
            <Chip className="tabular-nums">{draft.selected.length} selected</Chip>
          </div>
          {loadError && <Note tone="warn" role="alert">{loadError}</Note>}
          {draft.ids.length > 0 && (
            <fieldset aria-label="Models to offer" className="flex max-h-64 flex-col overflow-y-auto rounded-sm border border-hairline px-2 py-1">
              {matchingIds(draft.ids, draft.query).map((id) => (
                <label key={id} className="flex min-h-7 items-center gap-2 font-mono text-code-sm text-ink">
                  <input
                    type="checkbox"
                    className="size-3.5 accent-accent"
                    disabled={busy}
                    checked={draft.selected.includes(id)}
                    onChange={(e) => setDraft({ ...draft, selected: e.target.checked ? [...draft.selected, id] : draft.selected.filter((s) => s !== id) })}
                  />
                  {id}
                </label>
              ))}
            </fieldset>
          )}
          <div className="flex flex-wrap items-end gap-2">
            <Field id="custom-manual" label="Add a model ID the endpoint does not list">
              <Input
                id="custom-manual"
                className="w-64 font-mono text-code-sm"
                spellCheck={false}
                autoComplete="off"
                disabled={busy}
                value={draft.manual}
                onChange={(e) => setDraft({ ...draft, manual: e.target.value })}
              />
            </Field>
            <Button
              size="lg"
              disabled={busy || !draft.manual.trim()}
              onClick={() => {
                const id = draft.manual.trim();
                setDraft({ ...draft, manual: '', ids: [...new Set([...draft.ids, id])].sort(), selected: [...new Set([...draft.selected, id])] });
              }}
            >
              Add ID
            </Button>
          </div>
          <div className="flex gap-2">
            <Button type="submit" variant="primary" size="lg" disabled={busy}>
              Save provider
            </Button>
            <Button size="lg" disabled={loading} onClick={() => setDraft(null)}>
              Cancel
            </Button>
          </div>
        </form>
      )}
    </div>
  );
}

/**
 * The Settings view (issue #183): a page in the main pane, not a dialog, reached from the
 * sidebar's gear and `#settings`. A change shows at once and is saved through PATCH; a
 * refusal puts the old value back and says why.
 */
export function SettingsView({ leading, onClose }: { leading?: ReactNode; onClose: () => void }) {
  const { settings, dispatch, meta } = useApp();
  const saveSequence = useRef(0);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function save(patch: Partial<Settings>) {
    const sequence = ++saveSequence.current;
    const before = settings;
    dispatch({ type: 'settings', settings: { ...settings, ...patch } });
    setError(null);
    setSaving(true);
    try {
      const saved = await api.updateWebSettings(patch);
      if (sequence === saveSequence.current) dispatch({ type: 'settings', settings: saved });
    } catch (e) {
      if (sequence === saveSequence.current) {
        dispatch({ type: 'settings', settings: before });
        setError(`Could not save the setting: ${describeError(e)}`);
      }
    } finally {
      if (sequence === saveSequence.current) setSaving(false);
    }
  }

  /** Custom models are saved without the optimistic step: the model lists reload from what the service stored. */
  async function saveCustom(custom_models: CustomModel[]) {
    const sequence = ++saveSequence.current;
    setError(null);
    setSaving(true);
    try {
      const saved = await api.updateWebSettings({ custom_models });
      if (sequence === saveSequence.current) dispatch({ type: 'settings', settings: saved });
      return true;
    } catch (e) {
      if (sequence === saveSequence.current) setError(`Could not save the custom models: ${describeError(e)}`);
      return false;
    } finally {
      if (sequence === saveSequence.current) setSaving(false);
    }
  }

  const other = settings.send_default === 'steer' ? 'Queue' : 'Steer';
  const titled = (meta?.providers ?? []).filter((p) => p.capabilities.titles);

  return (
    <div className="flex min-h-0 flex-1 flex-col animate-rise">
      <header className="flex h-header shrink-0 items-center gap-1.5 border-b border-hairline pr-2 pl-3">
        {leading}
        <h1 className="min-w-0 flex-1 truncate text-display-sm text-ink">Settings</h1>
        {saving && <Spinner className="shrink-0" />}
        <Tip label="Close settings">
          <Button size="icon-md" aria-label="Close settings" className="text-muted" onClick={onClose}>
            <X />
          </Button>
        </Tip>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain">
        <div className="flex w-full min-w-0 flex-col px-4 py-4 md:px-6">
          <p className="mb-4 text-caption text-muted">Kept by the service, so they apply in every browser.</p>
          <Section id="composer" title="Composer">
            <Row
              id="send-default"
              label="While a task is running, Enter…"
              help={
                <>
                  Steer adds the message to the running turn; Queue holds it for the next one. {other} stays on Ctrl+Enter (⌘+Enter on a Mac) and on its own button. A message with files or
                  attachments always queues.
                </>
              }
            >
              <Segmented
                aria-labelledby="send-default-label"
                aria-describedby="send-default-help"
                disabled={saving}
                value={settings.send_default}
                onValueChange={(v) => void save({ send_default: v as SendDefault })}
                items={[
                  { value: 'steer', label: 'Steer' },
                  { value: 'queue', label: 'Queue' },
                ]}
              />
            </Row>
          </Section>
          {titled.length > 0 && <Section id="titles" title="Task titles">
            {titled.map((p) => {
              const current = settings.title_model?.[p.name] ?? '';
              const choices = modelChoices(p.models, settings.hidden_models?.[p.name], current);
              return <Row key={p.name} id={`titles-${p.name}`} label={p.display_name} help="Use the provider's own title, or generate a short title with a chosen model. Applies to new tasks without a name.">
                <Select aria-label={`${p.display_name} task title model`} value={current} disabled={saving} className="sm:w-64" items={[
                  { value: '', label: `${p.display_name}'s own title` },
                  ...choices.map(({ model, note }) => ({ value: model.id, label: `${model.name}${note ? ` (${note.toLowerCase()})` : ''}`, hidden: !!note })),
                ]} onValueChange={(id) => void save({ title_model: { ...settings.title_model, [p.name]: id } })} />
              </Row>;
            })}
          </Section>}
          <Section id="models" title="Models">
            <Note>Hidden models leave the selection menus. Tasks already using one keep it. New models appear automatically.</Note>
            <CustomModels models={settings.custom_models ?? []} disabled={saving} onSave={saveCustom} />
            {(meta?.providers ?? []).map((p) => {
              const hidden = settings.hidden_models?.[p.name] ?? [];
              const models: Model[] = [...p.models, ...hidden.filter((id) => !p.models.some((m) => m.id === id)).map((id) => ({ id, name: id }))];
              return <div key={p.name} className="flex flex-col gap-1">
                <h3 className="mb-1 text-ui font-medium">{p.display_name}</h3>
                <div className="grid grid-cols-1 gap-x-6 lg:grid-cols-2 xl:grid-cols-3">
                {models.map((m) => {
                  const shown = !hidden.includes(m.id);
                  const offered = p.models.some((v) => v.id === m.id);
                  return <div key={m.id} className="flex min-h-12 items-center gap-3 border-b border-hairline py-2">
                    <div className="flex min-w-0 flex-1 flex-col">
                      <span className="text-ui font-medium text-ink">{m.name}</span>
                      <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5 text-meta text-muted">
                        <span className="min-w-0 break-all font-mono">{m.id}{!offered ? ' · not offered now' : ''}</span>
                        {p.capabilities.usage && <span className="tabular-nums">{modelCostLine(m)}</span>}
                      </div>
                    </div>
                    {!shown && <span className="text-meta text-muted">Hidden</span>}
                    <Switch aria-label={`Show ${m.name}`} checked={shown} disabled={saving} onCheckedChange={(value) => void save({ hidden_models: { ...settings.hidden_models, [p.name]: value ? hidden.filter((id) => id !== m.id) : [...hidden, m.id] } })} />
                  </div>;
                })}
                </div>
              </div>;
            })}
          </Section>
          {error && (
            <Note tone="error" role="alert">
              {error}
            </Note>
          )}
        </div>
      </div>
    </div>
  );
}
