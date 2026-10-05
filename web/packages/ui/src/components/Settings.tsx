import { useApi } from '../ApiContext';
import { useFederation } from '../FederationContext';
import { LogOut, X } from 'lucide-react';
import { useContext, useEffect, useRef, useState, type ReactNode, type SubmitEvent } from 'react';
import { PlannerContext } from './planner/context';
import { DEFAULT_COMPACT_THRESHOLD, describeError, plannerErrorText, resolveTaskDefaults, routeMissing, type CustomModel, type ImportReport, type Model, type Project, type ProviderInfo, type SendDefault, type Settings } from '../api';
import { BackgroundAI } from './BackgroundAI';
import { CopilotAccount } from './CopilotAccount';
import { ConfigurationSettings } from './ConfigurationSettings';
import { McpServersSettings } from './McpServers';
import { Note, Skeleton, Spinner, useApp, useScrolled, ScrollSentinel } from './common';
import { byCodeUnit } from '../lib/order';
import { Field, FieldHelpProvider, ROW_GRID, Row, SectionAction, SectionActionSlot, TaskDefaultsFields, choiceLabel, useInlineForm } from './TaskDefaults';
import { customProviders, matchingIds, withProvider, type CustomProvider } from '../lib/customModels';
import { modelCostLine } from '../lib/cost';
import { TokenPricing } from './TokenPricing';
import { byModelName, cheapestLabel, modelChoices, UTILITY_NONE } from '../lib/models';
import { loadDensity, saveDensity, type Density } from '../lib/density';
import { loadMotion, saveMotion, type Motion } from '../lib/motion';
import { disableNotifications, enableNotifications, loadNotifyMode, notifySupport } from '../lib/notify';
import { AlertDialog, useConfirm } from './ui/dialog';
import { Select } from './ui/select';
import { Switch } from './ui/switch';
import { Button } from './ui/button';
import { Chip } from './ui/chip';
import { Input } from './ui/input';
import { Segmented } from './ui/segmented';
import { HelpTip, Tip } from './ui/tooltip';

const NO_PROJECTS: Project[] = [];
const SETTINGS_SECTIONS = [
  { id: 'connections', label: 'Connected instances' },
  { id: 'general', label: 'General' },
  { id: 'models', label: 'Models' },
  { id: 'providers', label: 'Providers' },
  { id: 'agents', label: 'Agents' },
  { id: 'skills', label: 'Skills' },
  { id: 'hooks', label: 'Hooks' },
  { id: 'instructions', label: 'Instructions' },
  { id: 'mcp', label: 'MCP servers' },
  { id: 'browser', label: 'This browser' },
] as const;
type SettingsSection = typeof SETTINGS_SECTIONS[number]['id'];

/**
 * One titled group of settings, a floating card (DESIGN.md Settings view); a new group is another `Section` below the last.
 * A card that is one switch or one select takes it as `control`: the title labels it, in the rows' column system. Its
 * Add button reaches the header's right through `SectionAction`.
 */
function Section({ id, title, subtitle, help, control, hidden = false, children }: Readonly<{ id: string; title: string; subtitle?: string; help?: ReactNode; control?: ReactNode; hidden?: boolean; children?: ReactNode }>) {
  const [slot, setSlot] = useState<HTMLDivElement | null>(null);
  const heading = (
    <div className="flex min-w-0 flex-col gap-0.5">
      <div className={control ? 'flex min-h-8 items-center gap-1' : 'flex items-center gap-1'}>
        <h2 id={`${id}-title`} tabIndex={id === 'token-prices' || id.startsWith('account-') ? -1 : undefined} className="text-title text-ink">
          {title}
        </h2>
        {help && <HelpTip label={title} id={`${id}-help`}>{help}</HelpTip>}
      </div>
      {subtitle && <p className="text-caption text-muted">{subtitle}</p>}
    </div>
  );
  return (
    // Plain `flex`: `not-hidden:flex` never applied (the cards stayed display block and lost their gaps); preflight keeps `[hidden]` at display none.
    <section hidden={hidden} aria-labelledby={`${id}-title`} className="flex flex-col gap-4 rounded-lg bg-raised p-5 shadow-raised">
      <div className="flex items-start gap-3">
        <div className="min-w-0 flex-1">
          {control ? <div className={ROW_GRID}>{heading}<div className="flex min-h-8 items-center">{control}</div></div> : heading}
        </div>
        <div ref={setSlot} className="flex shrink-0 items-center gap-2 empty:hidden" />
      </div>
      <SectionActionSlot value={slot}>{children}</SectionActionSlot>
    </section>
  );
}

/** A section whose content is still on its way (the first snapshot, the catalogs): the card with its title over a skeleton. */
function PendingSection({ id, title, label, hidden = false }: Readonly<{ id: string; title: string; label: string; hidden?: boolean }>) {
  return (
    <section hidden={hidden} aria-labelledby={`${id}-title`} aria-busy="true" className="flex flex-col gap-4 rounded-lg bg-raised p-5 shadow-raised">
      <h2 id={`${id}-title`} className="text-title text-ink">
        {title}
      </h2>
      <Skeleton label={label} rows={3} />
    </section>
  );
}

/** The optional connected-instance features (Federation capabilities), named as Settings names them. */
const OPTIONAL_FEATURES = ['files-v1', 'terminal-v1', 'configuration-v1', 'provider-accounts-v1', 'planner-v1', 'routines-v1', 'usage-v1'];

/** In place of a section whose routes the connected instance on screen does not offer: why, not a form that fails. */
function Unsupported({ what }: Readonly<{ what: string }>) {
  const name = useApi().owner?.label;
  return <Note role="status">{name} does not offer {what} to connected instances. Update UAM on {name} to manage it here.</Note>;
}

const UTILITY_HELP = "The model UAM uses to title new tasks, summarize completed subagent results, suggest replies, phrase turn outcomes and draft agents, skills and hooks. Utility calls use the lowest supported reasoning effort. Left as Cheapest, a new task is titled by its own model at its lowest effort, which spends that model's credits. None keeps provider titles and result excerpts without utility AI calls.";

/** The Utility model menu of one provider: Cheapest (the default), None, then its visible models with their prices. */
function UtilitySelect({ provider: p, settings, saving, describedBy, onSave }: Readonly<{ provider: ProviderInfo; settings: Settings; saving: boolean; describedBy: string; onSave: (patch: Partial<Settings>) => void }>) {
  const current = settings.title_model?.[p.name] ?? '';
  const choices = modelChoices(p.models, settings.hidden_models?.[p.name], current === UTILITY_NONE ? '' : current);
  const cost = (m: Model) => (p.capabilities.usage ? modelCostLine(m) : '');
  const cheapest = p.models.find((m) => m.id === p.cheapest_model);
  return <Select aria-label={`${p.display_name} utility model`} aria-describedby={describedBy} value={current} disabled={saving} className="sm:w-72" items={[
    { value: '', label: cheapestLabel(p), description: cheapest && cost(cheapest) },
    { value: UTILITY_NONE, label: 'None (no utility AI)' },
    ...choices.map(({ model, note }) => ({ value: model.id, label: choiceLabel(model.name, note), description: cost(model), hidden: !!note })),
  ]} onValueChange={(id) => onSave({ title_model: { ...settings.title_model, [p.name]: id } })} />;
}

/** The compaction thresholds Settings offers, in percent. */
const COMPACT_THRESHOLDS = [50, 55, 60, 65, 70, 75, 80, 85, 90];

/** The provider being added or edited; `original` is its saved name, none for a new one. */
interface ProviderDraft {
  original?: string;
  name: string;
  base_url: string;
  api_key_env: string;
  api_key: string;
  key_source: 'direct' | 'env';
  wire_api?: CustomModel['wire_api'];
  /** The IDs offered in the checklist: loaded from the endpoint, saved, or typed in. */
  ids: string[];
  selected: string[];
  vision: string[];
  query: string;
  manual: string;
}

const newProvider: ProviderDraft = { name: '', base_url: '', api_key_env: '', api_key: '', key_source: 'direct', ids: [], selected: [], vision: [], query: '', manual: '' };

/** What Remove is about to take away: one of a provider's models, or the provider with all of them. */
interface Removal {
  provider: CustomProvider;
  model?: string;
}

function removalTitle(pending: Removal | null): string {
  if (!pending) return 'Remove?';
  return pending.model ? `Remove ${pending.provider.name}/${pending.model}?` : `Remove provider ${pending.provider.name}?`;
}

function removalDescription(pending: Removal | null): string | undefined {
  if (!pending) return undefined;
  if (pending.model) return 'It leaves the model menus. Tasks already using it keep it until their model is changed.';
  const count = pending.provider.models.length;
  const leave = count === 1 ? 'Its one model leaves' : `Its ${count} models leave`;
  return `${leave} the model menus. Tasks already using one keep it until their model is changed.`;
}

/**
 * Custom (BYOM) models, by provider: an OpenAI-compatible endpoint whose models are chosen
 * from what it lists (the service loads the list) or typed in. Direct keys are write-only;
 * the edit form can keep a saved key without receiving its value.
 */
function CustomModels({ models, disabled, onSave }: Readonly<{ models: CustomModel[]; disabled: boolean; onSave: (next: CustomModel[]) => Promise<boolean> }>) {
  const api = useApi();
  const [draft, setDraft] = useState<ProviderDraft | null>(null);
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);
  const providers = customProviders(models);
  const busy = disabled || loading;
  // Removing a provider or one of its models is confirmed first (DESIGN.md Confirmations).
  const removal = useConfirm<Removal>();
  const replacement = useConfirm<{ next: CustomModel[]; removed: string[] }>();
  const pending = removal.target;
  function remove() {
    const r = removal.target;
    if (!r) return;
    removal.close();
    void onSave(withProvider(models, r.provider.name, r.provider, r.model ? r.provider.models.filter((o) => o.model_id !== r.model).map((o) => o.model_id) : []));
  }

  function edit(p?: CustomProvider) {
    setLoadError(null);
    setDraft(p ? { ...newProvider, original: p.name, name: p.name, base_url: p.base_url, api_key_env: p.api_key_env, key_source: p.api_key_env ? 'env' : 'direct', wire_api: p.wire_api, ids: p.models.map((m) => m.model_id), selected: p.models.map((m) => m.model_id), vision: p.models.filter((m) => m.vision).map((m) => m.model_id) } : newProvider);
  }
  function connection(d: ProviderDraft) {
    return { name: d.name.trim(), base_url: d.base_url.trim(), wire_api: d.wire_api, api_key_env: d.key_source === 'env' ? d.api_key_env.trim() : '', ...(d.key_source === 'direct' && d.api_key.trim() ? { api_key: d.api_key.trim() } : {}) };
  }
  function keepsKey(d: ProviderDraft) {
    return models.some((m) => m.name === d.name.trim() && m.base_url === d.base_url.trim() && m.wire_api === d.wire_api && !m.api_key_env && m.key_present);
  }
  async function load(d: ProviderDraft) {
    setLoading(true);
    setLoadError(null);
    try {
      const res = await api.discoverModels(connection(d));
      setDraft((cur) => cur && { ...cur, ids: [...new Set([...res.models, ...cur.selected])].sort(byCodeUnit) });
      if (res.truncated) setLoadError(`Showing the first ${res.models.length} models the endpoint lists.`);
    } catch (e) {
      setLoadError(`Could not load the models: ${describeError(e)}`);
    } finally {
      setLoading(false);
    }
  }
  async function save(e: SubmitEvent, d: ProviderDraft) {
    e.preventDefault();
    const provider = connection(d);
    const next = withProvider(models, d.original, provider, d.selected, d.vision);
    const removed = models.filter((model) => !next.some((entry) => entry.name === model.name && entry.model_id === model.model_id)).map((model) => `${model.name}/${model.model_id}`);
    if (removed.length) replacement.ask({ next, removed });
    else if (await onSave(next)) setDraft(null);
  }
  const formRef = useInlineForm<HTMLFormElement>(() => { if (!loading) setDraft(null); }, !!draft);
  const field = (d: ProviderDraft, key: 'name' | 'base_url' | 'api_key_env', label: string, placeholder: string) => (
    <Field id={`custom-${key}`} label={label}>
      <Input
        id={`custom-${key}`}
        className="text-ui"
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
      <div className="flex items-center gap-1">
        <h3 className="text-ui font-medium">Custom models</h3>
        <HelpTip label="Custom models">OpenAI-compatible endpoints, offered with GitHub Copilot's models. Enter an API key here, or use a UAM_BYOM_&lt;NAME&gt; environment variable from the service.</HelpTip>
      </div>
      {!draft && (
        <SectionAction>
          <Button size="sm" variant="secondary" data-section-add="" disabled={disabled} onClick={() => edit()}>
            Add provider
          </Button>
        </SectionAction>
      )}
      {providers.map((p) => (
        <section key={p.name} aria-label={`${p.name} models`} className="flex flex-col rounded-md bg-tint-well px-3 py-2">
          <div className="flex min-h-8 items-center gap-2">
            <div className="flex min-w-0 flex-1 flex-col">
              <span className="text-ui font-medium text-ink">{p.name}</span>
              <span className="min-w-0 break-all text-meta text-muted">{p.base_url}</span>
              <Note tone={p.key_present ? 'muted' : 'warn'}>{p.api_key_env ? (p.key_present ? `Key from ${p.api_key_env}` : `${p.api_key_env} is not set in the service's environment`) : (p.key_present ? 'API key saved' : 'API key missing')}</Note>
            </div>
            <Button size="sm" disabled={busy || !!draft} onClick={() => edit(p)}>
              Edit
            </Button>
            <Button size="sm" variant="danger" disabled={busy} aria-label={`Remove provider ${p.name}`} onClick={() => removal.ask({ provider: p })}>
              Remove
            </Button>
          </div>
          {p.models.map((m) => (
            <div key={m.model_id} className="flex min-h-8 items-center gap-2 pl-3">
              <span className="min-w-0 flex-1 truncate text-ui text-ink" title={`${p.name}/${m.model_id}`}>
                {m.display_name || m.model_id}
                {m.display_name && m.display_name !== m.model_id && <span className="ml-2 text-meta text-muted">{m.model_id}</span>}
              </span>
              <Button size="sm" variant="danger" disabled={busy} aria-label={`Remove ${p.name}/${m.model_id}`} onClick={() => removal.ask({ provider: p, model: m.model_id })}>
                Remove
              </Button>
            </div>
          ))}
        </section>
      ))}
      <AlertDialog
        {...removal.props}
        title={removalTitle(pending)}
        description={removalDescription(pending)}
        confirmLabel={pending?.model ? 'Remove model' : 'Remove provider'}
        onConfirm={remove}
      />
      <AlertDialog {...replacement.props} title="Remove models from this provider?" description={<>Saving these changes removes the following model IDs from the selection menus. Tasks already using them keep their current model.<span className="mt-2 block">{replacement.target?.removed.map((id) => <span key={id} className="block break-all font-mono text-meta">{id}</span>)}</span></>} confirmLabel="Save and remove models" busy={busy} onConfirm={() => {
        const next = replacement.target?.next;
        if (next) void onSave(next).then((saved) => { if (saved) setDraft(null); replacement.close(); });
      }} />
      {draft && (
        <form ref={formRef} aria-label={draft.original ? `Edit provider ${draft.original}` : 'Add a provider'} className="flex flex-col gap-3 rounded-md bg-tint-well p-3" onSubmit={(e) => void save(e, draft)}>
          <div className="grid grid-cols-1 items-end gap-3 sm:grid-cols-2">
            {field(draft, 'name', 'Provider name', 'my-provider')}
            {field(draft, 'base_url', 'Base URL', 'https://api.example.com/v1')}
          </div>
          <Segmented aria-label="API key source" value={draft.key_source} disabled={busy} items={[{ value: 'direct', label: 'API key' }, { value: 'env', label: 'Environment variable' }]} onValueChange={(value) => { setDraft({ ...draft, key_source: value as ProviderDraft['key_source'], api_key: '' }); setLoadError(null); }} />
          {draft.key_source === 'env' ? <>
            {field(draft, 'api_key_env', 'API key variable', 'UAM_BYOM_MY_PROVIDER')}
            <Note>Export this variable where the service starts, then restart the service.</Note>
          </> : <Field id="custom-api-key" label="API key">
            <Input id="custom-api-key" type="password" autoComplete="new-password" spellCheck={false} disabled={busy} required={!keepsKey(draft)} value={draft.api_key} placeholder={keepsKey(draft) ? 'Leave blank to keep the saved key' : 'Enter API key'} onChange={(e) => setDraft({ ...draft, api_key: e.target.value })} />
            <Note>{keepsKey(draft) ? 'Leave blank to keep the saved key, or enter a new key to replace it.' : 'Saved privately on the server. The key is never shown again.'}</Note>
          </Field>}
          <div className="flex flex-wrap items-center gap-2">
            <Button size="sm" variant="secondary" loading={loading} disabled={busy || !draft.base_url.trim() || (draft.key_source === 'env' ? !draft.api_key_env.trim() : !draft.api_key.trim() && !keepsKey(draft))} onClick={() => void load(draft)}>
              Load models
            </Button>
            <Input
              aria-label="Search models"
              size="md"
              className="w-48"
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
            <fieldset aria-label="Models to offer" className="flex max-h-64 flex-col overflow-y-auto overflow-x-hidden rounded-sm bg-raised px-2 py-1 shadow-well">
              {matchingIds(draft.ids, draft.query).map((id) => (
                <div key={id} className="flex min-h-7 items-center gap-3 text-ui text-ink">
                  <label className="flex min-w-0 flex-1 items-center gap-2">
                    <input
                      type="checkbox"
                      className="size-3.5 shrink-0 accent-accent"
                      disabled={busy}
                      checked={draft.selected.includes(id)}
                      onChange={(e) => setDraft({ ...draft, selected: e.target.checked ? [...draft.selected, id] : draft.selected.filter((s) => s !== id) })}
                    />
                    <span className="break-all">{id}</span>
                  </label>
                  <label className="flex shrink-0 items-center gap-2 text-muted">
                    <input
                      type="checkbox"
                      aria-label={`Enable vision for ${id}`}
                      className="size-3.5 accent-accent"
                      disabled={busy || !draft.selected.includes(id)}
                      checked={draft.vision.includes(id)}
                      onChange={(e) => setDraft({ ...draft, vision: e.target.checked ? [...draft.vision, id] : draft.vision.filter((s) => s !== id) })}
                    />
                    Vision
                  </label>
                </div>
              ))}
            </fieldset>
          )}
          {!!draft.ids.length && <Note>Enable Vision only for models that support image input.</Note>}
          <div className="flex flex-wrap items-end gap-2">
            <Field id="custom-manual" label="Add a model ID the endpoint does not list">
              <Input
                id="custom-manual"
                className="w-64"
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
                setDraft({ ...draft, manual: '', ids: [...new Set([...draft.ids, id])].sort(byCodeUnit), selected: [...new Set([...draft.selected, id])] });
              }}
            >
              Add ID
            </Button>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Button type="submit" variant="primary" size="lg" disabled={busy}>
              Save provider
            </Button>
            <Button size="lg" disabled={loading} onClick={() => setDraft(null)}>
              Cancel
            </Button>
            {providers.some((p) => p.name !== draft.original) && <Note>Save or cancel this form to edit another provider.</Note>}
          </div>
        </form>
      )}
    </div>
  );
}

/**
 * Settings → Planner (ADR 0005 §17): the switch (off by default; it cannot turn on without a git
 * binary), the Utility model its suggestions and triage use, and the one-time import of a board
 * from the kb app: a source directory in, a report out.
 */
function PlannerSection({ settings, saving, projects, providers, onSave, hidden }: Readonly<{ settings: Settings; saving: boolean; projects: Project[]; providers: ProviderInfo[]; hidden?: boolean; onSave: (patch: Partial<Settings>) => void }>) {
  const api = useApi();
  const [dir, setDir] = useState('');
  const [report, setReport] = useState<ImportReport | null>(null);
  const [importing, setImporting] = useState(false);
  const [importError, setImportError] = useState<string | null>(null);
  // A service without the import route yet (it lands after the planner): the form stays, disabled.
  const [importMissing, setImportMissing] = useState(false);
  const noGit = projects.some((p) => p.no_git === 'not_installed');
  const utility = providers
    .filter((p) => p.capabilities.titles)
    .map((p) => {
      const id = settings.title_model?.[p.name] || p.cheapest_model;
      return id === UTILITY_NONE ? `${p.display_name}: none (suggestions and triage are unavailable)` : `${p.display_name}: ${p.models.find((m) => m.id === id)?.name ?? id ?? 'the provider default'}`;
    });
  const run = async (e: SubmitEvent) => {
    e.preventDefault();
    setImporting(true);
    setImportError(null);
    setReport(null);
    try {
      setReport(await api.planner.import(dir.trim()));
    } catch (err) {
      if (routeMissing(err)) setImportMissing(true);
      else setImportError(plannerErrorText(err));
    } finally {
      setImporting(false);
    }
  };
  return (
    <Section
      hidden={hidden}
      id="planner"
      title="Planner"
      help="Epics, stories and subtasks for each git project, which agents decompose and carry out and you confirm, launch and close. Off, nothing of it shows."
      control={<Switch aria-label="Planner" aria-describedby={noGit ? 'planner-switch-help' : undefined} checked={!!settings.planner} disabled={saving || (noGit && !settings.planner)} onCheckedChange={(planner) => onSave({ planner })} />}
    >
      {noGit && <Note id="planner-switch-help">Git is not installed on the server, so the planner cannot turn on.</Note>}
      {settings.planner && utility.length > 0 && <Note>Suggestions and triage use the Utility model: {utility.join('; ')}.</Note>}
      {settings.planner && (
        <form aria-label="Import from kb" className="flex flex-col gap-2" onSubmit={(e) => void run(e)}>
          <Row id="planner-import" label="Import from kb" htmlFor="planner-import-dir" help="Copy cards from a kb board directory. Cards whose project name matches a project here join its board; the rest wait in Unassigned. Running it again adds no duplicates.">
            <Input id="planner-import-dir" aria-describedby="planner-import-help" className="w-72 max-w-full text-ui" spellCheck={false} autoComplete="off" placeholder="/home/you/.local/share/kb" value={dir} disabled={importing || importMissing} onChange={(e) => setDir(e.target.value)} />
            <Button type="submit" variant="secondary" size="lg" loading={importing} disabled={!dir.trim() || importMissing}>
              Import
            </Button>
          </Row>
          {importMissing && <Note role="status">This service cannot import yet; an update adds it.</Note>}
          {importError && <Note tone="error" role="alert">Could not import: {importError}</Note>}
          {report && (
            <div role="status" className="flex flex-col gap-1 rounded-md bg-tint-well px-3 py-2 text-caption text-body">
              <span>
                {report.imported} imported, {report.updated} already here, {report.unassigned} to Unassigned, {report.comments} {report.comments === 1 ? 'comment' : 'comments'} and {report.links} blocker {report.links === 1 ? 'link' : 'links'} copied.
              </span>
              {report.skipped.length > 0 && (
                <>
                  <span className="text-muted">{report.skipped.length} skipped:</span>
                  <ul className="flex flex-col gap-0.5 pl-2">
                    {report.skipped.map((s) => (
                      <li key={s.id} className="min-w-0 text-muted [overflow-wrap:anywhere]">
                        {s.id}: {s.reason}
                      </li>
                    ))}
                  </ul>
                </>
              )}
            </div>
          )}
        </form>
      )}
    </Section>
  );
}

/**
 * The Settings view (issue #183): a page in the main pane, not a dialog, reached from the
 * sidebar's gear and `#settings`. A change shows at once and is saved through PATCH; a
 * refusal puts the old value back and says why.
 */
/** Notifications for this browser (lib/notify.ts): permission is asked only when the switch is turned on. */
function NotifyRow() {
  const [support] = useState(notifySupport);
  const [mode, setMode] = useState(loadNotifyMode);
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);
  async function toggle(on: boolean) {
    setProblem(null);
    setBusy(true);
    try {
      if (!on) {
        await disableNotifications();
        setMode(null);
        return;
      }
      const result = await enableNotifications();
      if ('error' in result) setProblem(result.error);
      else setMode(result.mode);
    } finally {
      setBusy(false);
    }
  }
  let help = 'When a task asks you something, needs your permission, fails or finishes, unless you are looking at it.';
  if (support === 'home-screen') help = 'On iPhone and iPad this works in the Home Screen app only: tap Share, then Add to Home Screen, open UAM from there and turn this on.';
  else if (support === 'none') help = 'This browser cannot show notifications.';
  else if (mode === 'push') help += ' They arrive even with UAM closed.';
  else if (mode === 'page') help += ' This browser shows them only while UAM is open in a tab.';
  return (
    <>
      <Row id="notify" label="Notify me when a task needs me or finishes" help={help} helpVisible={support !== 'ok'}>
        <Switch aria-label="Notify me when a task needs me or finishes" aria-describedby="notify-help" checked={mode !== null} disabled={busy || support !== 'ok'} onCheckedChange={(on) => void toggle(on)} />
      </Row>
      {problem && (
        <Note tone="error" role="alert">
          {problem}
        </Note>
      )}
    </>
  );
}

export function SettingsView({ leading, onClose, onLogout, tokenPricesRequest = 0, connections, focusAccount }: Readonly<{ leading?: ReactNode; onClose: () => void; onLogout?: () => void; tokenPricesRequest?: number; connections?: ReactNode; focusAccount?: string }>) {
  const api = useApi();
  const instanceControl = useFederation()?.sourceControl;
  // The connected instance on screen, or this one; null without connected instances, where nothing names it.
  const instance = instanceControl ? api.owner?.label ?? 'This instance' : null;
  const unsupported = OPTIONAL_FEATURES.filter((feature) => !api.supports(feature)).map((feature) => feature.replace('-v1', '').replaceAll('-', ' '));
  const { settings, dispatch, meta, metaError, loaded, refreshMeta } = useApp();
  const projects = useContext(PlannerContext)?.projects ?? NO_PROJECTS;
  // The catalogs are not here yet and have not failed: their sections are skeletons, never absent or empty.
  const catalogPending = !meta && !metaError;
  // Opened for Token costs, or on a provider's account (`focusAccount`, the blocked app's Open Settings).
  const [section, setSection] = useState<SettingsSection>(tokenPricesRequest ? 'models' : focusAccount ? 'providers' : 'general');
  const [visited, setVisited] = useState<Set<SettingsSection>>(() => new Set([section]));
  const [handledPriceRequest, setHandledPriceRequest] = useState(tokenPricesRequest);
  if (handledPriceRequest !== tokenPricesRequest) {
    setHandledPriceRequest(tokenPricesRequest);
    if (tokenPricesRequest) {
      setSection('models');
      setVisited((before) => new Set([...before, 'models']));
    }
  }
  const scrollArea = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!tokenPricesRequest || !loaded) return;
    const title = scrollArea.current?.querySelector<HTMLElement>('#token-prices-title');
    title?.focus({ preventScroll: true });
    title?.scrollIntoView({ block: 'start' });
  }, [tokenPricesRequest, loaded]);
  // The account's card title takes focus once the catalogs list the provider.
  const accountTitle = focusAccount && meta ? `account-${focusAccount}-title` : null;
  useEffect(() => {
    if (!accountTitle) return;
    const title = document.getElementById(accountTitle);
    title?.focus({ preventScroll: true });
    title?.scrollIntoView({ block: 'start' });
  }, [accountTitle]);
  const saveSequence = useRef(0);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [motion, setMotion] = useState(loadMotion);
  const [density, setDensity] = useState(loadDensity);
  const [scrolled, sentinel] = useScrolled();

  async function save(patch: Partial<Settings>) {
    const sequence = ++saveSequence.current;
    const before = settings;
    dispatch({ type: 'settings', settings: { ...settings, ...patch } });
    setError(null);
    setSaving(true);
    try {
      const saved = await api.updateWebSettings(patch);
      if (sequence === saveSequence.current) dispatch({ type: 'settings', settings: saved });
      // Hiding a model can change a provider's cheapest_model, which only the service computes.
      if (patch.hidden_models) refreshMeta();
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

  const other = settings.send_default === 'steer' ? 'After this turn' : 'Send now';
  const titled = (meta?.providers ?? []).filter((p) => p.capabilities.titles);
  // What New task uses today: the setting checked against the live catalog, or the provider's own defaults until it is set.
  const taskDefaults = resolveTaskDefaults(meta, settings.task_defaults, settings.hidden_models);
  const configured = customProviders(settings.custom_models ?? []);
  const customIds = new Set(configured.flatMap((p) => p.models.map((m) => `${p.name}/${m.model_id}`)));
  const copilotModels = meta?.providers.find((p) => p.name === 'copilot')?.models ?? [];
  // Custom providers own their display groups, but Copilot still owns their selection IDs and visibility settings.
  const modelGroups: { key: string; label: string; owner: string; models: Model[]; offered: Model[]; usage: boolean; custom?: CustomProvider }[] = [
    ...(meta?.providers ?? []).map((p) => ({
      key: p.name, label: p.display_name, owner: p.name, offered: p.models, usage: !!p.capabilities.usage,
      models: [...p.models, ...(settings.hidden_models?.[p.name] ?? []).filter((id) => !p.models.some((m) => m.id === id)).map((id) => ({ id, name: id }))]
        .filter((m) => p.name !== 'copilot' || !customIds.has(m.id)),
    })),
    ...configured.map((p) => ({
      key: `custom:${p.name}`, label: p.name, owner: 'copilot', offered: copilotModels, usage: false, custom: p,
      models: p.models.map((m) => {
        const id = `${p.name}/${m.model_id}`;
        return copilotModels.find((offered) => offered.id === id) ?? { id, name: m.display_name || m.model_id };
      }),
    })),
  ];

  return (
    <FieldHelpProvider><div className="flex min-h-0 flex-1 flex-col animate-rise">
      <header className="pane-header flex h-header shrink-0 items-center gap-1.5 pr-2 pl-3" data-scrolled={scrolled || undefined}>
        {leading}
        <div className="flex min-w-0 flex-1 items-center gap-1">
          <h1 className="shrink-0 text-display-sm text-ink">Settings{instance && <span className="sr-only"> · {instance}</span>}</h1>
          {instanceControl && <><span aria-hidden="true" className="text-muted">·</span>{instanceControl}</>}
          <HelpTip label="Settings">{section === 'browser' ? 'These preferences apply only in this browser, whichever instance is on screen.' : api.owner ? `Kept by ${api.owner.label} and shared across browsers.` : 'Kept by the service and shared across browsers.'}</HelpTip>
        </div>
        {saving && <Spinner className="shrink-0" />}
        <Tip label="Close settings">
          <Button size="icon-md" aria-label="Close settings" className="text-muted" onClick={onClose}>
            <X />
          </Button>
        </Tip>
      </header>
      {/* The tabs wrap onto more rows when narrow; nothing scrolls sideways. */}
      <div className="relative min-w-0 shrink-0">
        <nav aria-label="Settings sections" className="flex min-w-0 flex-wrap gap-1 py-2 pl-4 pr-4 md:px-6">
          {SETTINGS_SECTIONS.filter(item => item.id !== 'connections' || connections).map((item) => (
            <Button key={item.id} size="sm" className="md:h-8 md:px-3 md:after:inset-0 md:pointer-coarse:min-h-11 md:pointer-coarse:after:inset-0" aria-current={section === item.id ? 'page' : undefined} variant={section === item.id ? 'secondary' : 'ghost'} onClick={() => { setSection(item.id); setVisited((before) => new Set([...before, item.id])); if (scrollArea.current) scrollArea.current.scrollTop = 0; }}>
              {item.label}
            </Button>
          ))}
        </nav>
      </div>
      <div ref={scrollArea} className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden overscroll-contain [scrollbar-gutter:stable]">
        <ScrollSentinel sentinelRef={sentinel} />
        <div className="flex w-full min-w-0 flex-col gap-4 px-4 py-4 md:px-6">
          {api.owner && unsupported.length > 0 && <Note>Unavailable on {api.owner.label}: {unsupported.join(', ')}.</Note>}
          {section === 'connections' && <Section id="connections" title="Connected instances">{connections}</Section>}
          {(meta?.providers ?? []).filter((p) => p.capabilities.account).map((p) => (
            <Section hidden={section !== 'providers'} key={p.name} id={`account-${p.name}`} title={p.display_name}>
              {api.supports('provider-accounts-v1') ? <CopilotAccount provider={p} /> : <Unsupported what="provider sign-in" />}
            </Section>
          ))}
          {!loaded && (
            <>
              <PendingSection hidden={section !== 'general'} id="composer" title="Composer" label="Loading settings…" />
              <PendingSection hidden={section !== 'general'} id="new-tasks" title="New tasks" label="" />
              <PendingSection hidden={section !== 'models'} id="models" title="Models" label="" />
              <PendingSection hidden={section !== 'providers'} id="providers" title="Providers" label="Loading settings…" />
            </>
          )}
          {loaded && <Section hidden={section !== 'general'} id="composer" title="Composer">
            <Row
              id="send-default"
              label="Enter while a task is running"
              help={
                <>
                  Send now adds the message to the running turn; After this turn holds it until the turn ends. {other} stays on Ctrl+Enter (⌘+Enter on a Mac) and in the menu beside the send button.
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
                  { value: 'steer', label: 'Send now' },
                  { value: 'queue', label: 'After this turn' },
                ]}
              />
            </Row>
            <Row id="suggest-replies" label="Suggest replies" help="After a turn, a reply you might send next shows as faint text in the empty composer. Right Arrow or End fills it in; nothing is sent, and typing replaces it. It takes one Utility model call per finished turn you open.">
              <Switch aria-label="Suggest replies" aria-describedby="suggest-replies-help" checked={settings.suggest_replies !== false} disabled={saving} onCheckedChange={(suggest_replies) => void save({ suggest_replies })} />
            </Row>
          </Section>}
          {loaded && catalogPending && <PendingSection hidden={section !== 'general'} id="new-tasks" title="New tasks" label="Loading the model catalog…" />}
          {loaded && catalogPending && <PendingSection hidden={section !== 'models'} id="utility" title="Utility model" label="" />}
          {loaded && catalogPending && <PendingSection hidden={section !== 'models'} id="models" title="Models" label="" />}
          {loaded && taskDefaults && (
            <Section hidden={section !== 'general'} id="new-tasks" title="New tasks" help="What a new task starts with, in every project. The composer can still change each one before the first message.">
              {/* One stacked column of fields, the compaction threshold with them rather than out at the card's edge. */}
              <div className="flex max-w-xl flex-col gap-3">
                <TaskDefaultsFields prefix="new-tasks" value={taskDefaults} disabled={saving} onChange={(next) => void save({ task_defaults: next })} />
                <Field id="compact-threshold" label="Compact the conversation when its context reaches" hint="Compacting earlier keeps answers faster and cheaper but drops older detail sooner. A task already open picks up a change the next time it reopens.">
                  <Select id="compact-threshold" aria-describedby="compact-threshold-hint" value={String(settings.compact_threshold ?? DEFAULT_COMPACT_THRESHOLD)} disabled={saving} items={COMPACT_THRESHOLDS.map((n) => ({ value: String(n), label: n === DEFAULT_COMPACT_THRESHOLD ? `${n}% (default)` : `${n}%` }))} onValueChange={(v) => void save({ compact_threshold: Number(v) === DEFAULT_COMPACT_THRESHOLD ? null : Number(v) })} />
                </Field>
              </div>
            </Section>
          )}
          {/* One provider (the usual case): the card's title labels its select and the provider is the secondary line; several get a row each, named by provider. */}
          {loaded && titled.length > 0 && <Section hidden={section !== 'models'} id="utility" title="Utility model" help={titled.length === 1 ? UTILITY_HELP : undefined} subtitle={titled.length === 1 ? titled[0].display_name : undefined} control={titled.length === 1 ? <UtilitySelect provider={titled[0]} settings={settings} saving={saving} describedBy="utility-help" onSave={(patch) => void save(patch)} /> : undefined}>
            {titled.length > 1 && titled.map((p) => (
              <Row key={p.name} id={`utility-${p.name}`} label={p.display_name} help={UTILITY_HELP}>
                <UtilitySelect provider={p} settings={settings} saving={saving} describedBy={`utility-${p.name}-help`} onSave={(patch) => void save(patch)} />
              </Row>
            ))}
          </Section>}
          {loaded && <Section hidden={section !== 'general'} id="background-ai" title="Background AI" help="UAM's own AI calls on the Utility model: task titles, subagent summaries, suggested replies, outcome lines, planner suggestions and triage, and agent, skill and hook drafts. Each one costs AI credits. Every call is kept here for 30 days.">
            <BackgroundAI limitSetting={settings.utility_daily_limit} saving={saving} onSaveLimit={(utility_daily_limit) => save({ utility_daily_limit })} />
          </Section>}
          {loaded && !catalogPending && <Section hidden={section !== 'models'} id="models" title="Models" help="Hidden models leave the selection menus. Tasks already using one keep it. New models appear automatically.">
            {metaError && (
              <Note tone="error" role="alert" className="flex flex-wrap items-center gap-2">
                <span className="min-w-0 flex-1">Could not load the model catalog: {metaError}</span>
                <Button size="sm" variant="secondary" onClick={refreshMeta}>
                  Retry
                </Button>
              </Note>
            )}
            {modelGroups.map((p) => {
              const hidden = settings.hidden_models?.[p.owner] ?? [];
              return <div key={p.key} role="group" aria-label={`${p.label} models`} className="flex flex-col gap-1">
                <h3 className="mb-1 flex items-baseline gap-2 text-ui font-medium">{p.label}{p.custom && <span className="text-meta font-normal text-muted">Custom provider</span>}</h3>
                {p.custom?.key_present === false && <Note tone="error">{p.custom.api_key_env ? `${p.custom.api_key_env} is not set in the service's environment.` : 'The API key is missing.'} Check this provider in Providers.</Note>}
                <div className="grid grid-cols-1 gap-x-6 lg:grid-cols-2 xl:grid-cols-3">
                {[...p.models].sort(byModelName).map((m) => {
                  const shown = !hidden.includes(m.id);
                  const offered = p.offered.some((v) => v.id === m.id);
                  // A hidden ID the catalog no longer lists has no display name: its ID is the name, said once.
                  const idLine = m.name === m.id ? '' : m.id;
                  return <div key={m.id} className="flex min-h-12 items-center gap-3 py-2">
                    <div className="flex min-w-0 flex-1 flex-col">
                      <span className="text-ui font-medium text-ink">{m.name}</span>
                      <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5 text-meta text-muted">
                        <span className="min-w-0 break-all">{[idLine, !offered && (idLine ? 'not offered now' : 'Not offered now')].filter(Boolean).join(' · ')}</span>
                        {p.usage && <span className="tabular-nums">{modelCostLine(m)}</span>}
                      </div>
                    </div>
                    {!shown && <span className="text-meta text-muted">Hidden</span>}
                    <Switch aria-label={`Show ${m.name}`} checked={shown} disabled={saving} onCheckedChange={(value) => void save({ hidden_models: { ...settings.hidden_models, [p.owner]: value ? hidden.filter((id) => id !== m.id) : [...hidden, m.id] } })} />
                  </div>;
                })}
                </div>
              </div>;
            })}
          </Section>}
          {loaded && section === 'models' && <Section id="token-prices" title="Token costs">{api.supports('usage-v1') ? <TokenPricing /> : <Unsupported what="token costs" />}</Section>}
          {loaded && <Section hidden={section !== 'providers'} id="providers" title="Providers" help="Manage provider endpoints and credentials here. Choose visible models and the Utility model in Models.">
            {catalogPending && <Note role="status">Loading provider accounts…</Note>}
            {metaError && <Note tone="error" role="alert">Could not load provider accounts: {metaError} <Button size="sm" onClick={refreshMeta}>Retry</Button></Note>}
            <CustomModels models={settings.custom_models ?? []} disabled={saving} onSave={saveCustom} />
          </Section>}
          {/* A service that does not know the planner setting yet has no planner: no row at all. */}
          {loaded && settings.planner !== undefined && (api.supports('planner-v1')
            ? <PlannerSection hidden={section !== 'general'} settings={settings} saving={saving} projects={projects} providers={meta?.providers ?? []} onSave={(patch) => void save(patch)} />
            : <Section hidden={section !== 'general'} id="planner" title="Planner"><Unsupported what="the planner" /></Section>)}
          {loaded && <Section hidden={section !== 'general'} id="shell" title="Shell access">
            {api.supports('terminal-v1') ? <Row id="terminal" label="Terminal" help={`Open a shell in the project folder from a Task's header. Anyone signed in can then run commands on ${api.owner ? api.owner.label : 'this machine'} as the uam user, without the agent's permission prompts.`}>
              <Switch aria-label="Terminal" aria-describedby="terminal-help" checked={!!settings.terminal} disabled={saving} onCheckedChange={(terminal) => void save({ terminal })} />
            </Row> : <Unsupported what="the terminal" />}
          </Section>}
          {onLogout && <Section hidden={section !== 'general'} id="session" title="Session">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <Note>Sign out of UAM in this browser.</Note>
              <Button variant="secondary" onClick={onLogout}><LogOut aria-hidden="true" />Log out</Button>
            </div>
          </Section>}
          {(['agents', 'skills', 'hooks', 'instructions'] as const).map((kind) => visited.has(kind) && <Section key={kind} hidden={section !== kind} id={kind} title={SETTINGS_SECTIONS.find((item) => item.id === kind)!.label}>{api.supports('configuration-v1') ? <ConfigurationSettings kind={kind} projects={projects} terminal={!!settings.terminal} /> : <Unsupported what="agents, skills, hooks and instructions" />}</Section>)}
          {loaded && (meta?.providers ?? []).some((p) => p.capabilities.mcp) && <Section hidden={section !== 'mcp'} id="mcp" title="MCP servers">
            <McpServersSettings terminal={!!settings.terminal} />
          </Section>}
          <Section hidden={section !== 'browser'} id="browser" title="This browser">
            {api.owner && <Note>Kept in this browser, not on {api.owner.label}: these apply whichever instance is on screen.</Note>}
            <NotifyRow />
            <Row id="motion" label="Motion" help="Always on animates even when the OS asks for reduced motion; Match system follows your OS setting.">
              <Segmented
                aria-labelledby="motion-label"
                aria-describedby="motion-help"
                value={motion}
                onValueChange={(v) => {
                  setMotion(v as Motion);
                  saveMotion(v as Motion);
                }}
                items={[
                  { value: 'on', label: 'Always on' },
                  { value: 'system', label: 'Match system' },
                ]}
              />
            </Row>
            <Row id="density" label="Activity" help="Compact folds a turn's thinking and tool calls into its head row, which opens the timeline; Detailed keeps one activity row for every run of work between paragraphs.">
              <Segmented
                aria-labelledby="density-label"
                aria-describedby="density-help"
                value={density}
                onValueChange={(v) => {
                  setDensity(v as Density);
                  saveDensity(v as Density);
                }}
                items={[
                  { value: 'compact', label: 'Compact' },
                  { value: 'detailed', label: 'Detailed' },
                ]}
              />
            </Row>
          </Section>
          {error && (
            <Note tone="error" role="alert">
              {error}
            </Note>
          )}
        </div>
      </div>
    </div></FieldHelpProvider>
  );
}
