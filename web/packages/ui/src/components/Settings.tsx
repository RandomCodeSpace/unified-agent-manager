import { useApi } from '../ApiContext';
import { useFederation } from '../FederationContext';
import { ChevronLeft, ChevronRight, FolderOpen, LogOut, X } from 'lucide-react';
import { lazy, Suspense, useCallback, useContext, useEffect, useRef, useState, type ReactNode, type SubmitEvent } from 'react';
import { DEFAULT_COMPACT_THRESHOLD, describeError, resolveTaskDefaults, type ConfigurationKind, type CustomModel, type Model, type ProviderInfo, type SendDefault, type Settings } from '../api';
import { ArchivedTasks } from './ArchivedTasks';
import { BackgroundAI } from './BackgroundAI';
import { CopilotAccount } from './CopilotAccount';
import { CopilotCli } from './CopilotCli';
import { GitLabSettings } from './GitLabSettings';
import { restartOffered, UamService } from './UamService';
import { InstanceSwitcher } from './InstanceSwitcher';
import { ConfigurationSettings } from './ConfigurationSettings';
import { McpServersSettings } from './McpServers';
import { TaskList } from './taskActions';
import { Note, Skeleton, Spinner, useApp, useMedia, useScrolled, ScrollSentinel } from './common';
import { SETTINGS_PAGES, SettingsRail, isSettingsPage, pageMatches, type PageAttention, type SettingsPage, type SettingsScope } from './SettingsNav';
import { cn } from '../lib/cn';
import { byCodeUnit } from '../lib/order';

import { Field, FieldHelpProvider, ROW_GRID, Row, SectionAction, SectionActionSlot, TaskDefaultsFields, choiceLabel, useInlineForm } from './TaskDefaults';
import { customProviders, matchingIds, withProvider, type CustomProvider } from '../lib/customModels';
import { modelCostLine } from '../lib/cost';
import { TokenPricing } from './TokenPricing';
import { byModelName, cheapestLabel, effortLabel, modelChoices, UTILITY_NONE, visibleModels } from '../lib/models';
import { loadMotion, saveMotion, type Motion } from '../lib/motion';
import { loadTheme, saveTheme, type Theme } from '../lib/theme';
import { disableNotifications, enableNotifications, loadNotifyMode, notifySupport } from '../lib/notify';
import { AlertDialog, useConfirm } from './ui/dialog';
import { Select } from './ui/select';
import { Switch } from './ui/switch';
import { Button } from './ui/button';
import { Chip } from './ui/chip';
import { Collapse, usePresence } from './ui/collapse';
import { Input } from './ui/input';
import { Segmented } from './ui/segmented';
import { HelpTip, Tip } from './ui/tooltip';

const FolderPicker = lazy(() => import('./FolderPicker').then((m) => ({ default: m.FolderPicker })));

/** Wide enough for the rail beside the page; narrower, the rail is a list of pages that opens one at a time. */
const WIDE = '(min-width: 768px)';

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
const OPTIONAL_FEATURES = ['files-v1', 'terminal-v1', 'configuration-v1', 'provider-accounts-v1', 'routines-v1', 'usage-v1', 'project-location-v1'];

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

const SUBAGENT_HELP = "Subagents run only on the models listed. The first is the fallback, used when neither the requested model nor the task's model is listed. An empty list allows any model.";

/**
 * The models one provider's subagents may use, the fallback first, and a menu adding its visible models not listed yet.
 * An ID the catalog no longer lists keeps its row, so it can be removed.
 */
function SubagentModels({ provider: p, settings, saving, onSave }: Readonly<{ provider: ProviderInfo; settings: Settings; saving: boolean; onSave: (patch: Partial<Settings>) => void }>) {
  const allowed = settings.subagent_models?.[p.name] ?? [];
  const change = (next: string[]) => onSave({ subagent_models: { ...settings.subagent_models, [p.name]: next } });
  const addable = visibleModels(p.models, settings.hidden_models?.[p.name]).filter((m) => !allowed.includes(m.id)).sort(byModelName);
  return <div className="flex flex-col gap-1">
    {allowed.length === 0 && <Note>No limit: subagents may use any model.</Note>}
    {allowed.map((id, index) => {
      const listed = p.models.find((m) => m.id === id);
      const name = listed?.name || id;
      return <div key={id} className="flex min-h-12 items-center gap-3 py-2">
        <div className="flex min-w-0 flex-1 flex-col">
          <span className="flex items-center gap-2 text-ui font-medium text-ink">{name}{index === 0 && <Chip tone="accent">Fallback</Chip>}</span>
          {(!listed || name !== id) && <span className="min-w-0 break-all text-meta text-muted">{listed ? id : 'Not offered now'}</span>}
        </div>
        {index > 0 && <Button size="sm" disabled={saving} aria-label={`Make ${name} the fallback`} onClick={() => change([id, ...allowed.filter((a) => a !== id)])}>Make fallback</Button>}
        <Button size="sm" variant="danger" disabled={saving} aria-label={`Remove ${name} from subagent models`} onClick={() => change(allowed.filter((a) => a !== id))}>Remove</Button>
      </div>;
    })}
    <Select aria-label={`Add a ${p.display_name} subagent model`} value="" disabled={saving || addable.length === 0} className="mt-1 sm:w-72" items={[
      { value: '', label: 'Add a model', hidden: true },
      ...addable.map((m) => ({ value: m.id, label: m.name || m.id })),
    ]} onValueChange={(id) => { if (id) change([...allowed, id]); }} />
  </div>;
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

export function SettingsView({ leading, onClose, onLogout, tokenPricesRequest = 0, connectionsRequest = 0, connections, focusAccount }: Readonly<{ leading?: ReactNode; onClose: () => void; onLogout?: () => void; tokenPricesRequest?: number; /** Counts requests to show Connected instances (the instance switcher's Connect another instance). */ connectionsRequest?: number; connections?: ReactNode; focusAccount?: string }>) {
  const api = useApi();
  const federation = useFederation();
  // Switching the instance on screen keeps the page open (connected instances only).
  const carry = federation?.carry;
  // The connected instance on screen, or this one; null without connected instances, where nothing names it.
  const instance = federation?.machines ? api.owner?.label ?? 'Main Server' : null;
  const unsupported = OPTIONAL_FEATURES.filter((feature) => !api.supports(feature)).map((feature) => feature.replace('-v1', '').replaceAll('-', ' '));
  const { settings, dispatch, meta, metaError, loaded, refreshMeta } = useApp();
  const [projectDir, setProjectDir] = useState<string | null>(null);
  const [projectBrowsing, setProjectBrowsing] = useState(false);
  const projectPicker = usePresence(projectBrowsing);
  const projectInput = useRef<HTMLInputElement>(null);
  const projectBrowse = useRef<HTMLButtonElement>(null);
  const { projects } = useContext(TaskList);
  // The catalogs are not here yet and have not failed: their sections are skeletons, never absent or empty.
  const catalogPending = !meta && !metaError;
  // A phone shows the rail as a list of pages and opens one at a time; wider screens keep the rail beside the page.
  const wide = useMedia(WIDE);
  // Opened for Token costs, or on a provider's account (`focusAccount`, the blocked app's Open Settings); none is the
  // phone's list, or New tasks beside the rail.
  const [page, setPage] = useState<SettingsPage | null>(() => {
    if (tokenPricesRequest) return 'token-costs';
    if (focusAccount) return 'copilot';
    if (connectionsRequest && connections) return 'connections';
    const next = carry?.read().nextSection;
    return isSettingsPage(next) && (next !== 'connections' || Boolean(connections)) ? next : null;
  });
  const shown = page ?? (wide ? 'new-tasks' : null);
  const [query, setQuery] = useState('');
  const searching = !!query.trim();
  // Opened by switching the instance on screen: the header stays put and the body cross-fades, instead of the view rising in.
  const [switched] = useState(() => !!carry?.read().nextSection);
  // Pages shown once stay mounted (hidden), so their forms and lists survive moving between pages.
  const [visited, setVisited] = useState<ReadonlySet<SettingsPage>>(() => new Set(page ? [page] : []));
  useEffect(() => carry?.write({ nextSection: null }), [carry]);
  useEffect(() => carry?.write({ section: shown ?? 'new-tasks' }), [carry, shown]);
  const [handledConnectionsRequest, setHandledConnectionsRequest] = useState(connectionsRequest);
  if (handledConnectionsRequest !== connectionsRequest) {
    setHandledConnectionsRequest(connectionsRequest);
    if (connectionsRequest && connections) {
      setPage('connections');
      setQuery('');
    }
  }
  const [handledPriceRequest, setHandledPriceRequest] = useState(tokenPricesRequest);
  if (handledPriceRequest !== tokenPricesRequest) {
    setHandledPriceRequest(tokenPricesRequest);
    if (tokenPricesRequest) {
      setPage('token-costs');
      setQuery('');
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
  const [theme, setTheme] = useState(loadTheme);
  const [scrolled, sentinel] = useScrolled();
  const cliUpdate = meta?.providers.find((p) => p.cli_update)?.cli_update;
  const restart = restartOffered(meta?.service);
  const [utilityPaused, setUtilityPaused] = useState(false);
  // Agents, Skills, Hooks and Instructions share one scope; a kind with a form open holds it.
  const [toolkitScope, setToolkitScope] = useState('');
  const [held, setHeld] = useState<readonly ConfigurationKind[]>([]);
  const hold = useCallback((kind: ConfigurationKind, on: boolean) => setHeld((before) => (before.includes(kind) === on ? before : on ? [...before, kind] : before.filter((k) => k !== kind))), []);
  // A phone opening a page moves focus to its back button; going back returns it to the page's row.
  const back = useRef<HTMLButtonElement>(null);
  const returnTo = useRef<SettingsPage | null>(null);
  useEffect(() => {
    if (wide) return;
    if (page) back.current?.focus();
    else if (returnTo.current) document.querySelector<HTMLElement>(`[data-settings-page="${returnTo.current}"]`)?.focus();
    returnTo.current = null;
  }, [page, wide]);
  function open(id: SettingsPage) {
    setPage(id);
    setQuery('');
    if (scrollArea.current) scrollArea.current.scrollTop = 0;
  }

  async function save(patch: Partial<Settings>) {
    const sequence = ++saveSequence.current;
    const before = settings;
    dispatch({ type: 'settings', settings: { ...settings, ...patch } });
    setError(null);
    setSaving(true);
    try {
      const saved = await api.updateWebSettings(patch);
      if (sequence === saveSequence.current) {
        dispatch({ type: 'settings', settings: saved });
        if (patch.default_project_dir !== undefined) setProjectDir(null);
      }
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
  // A connected instance older than the subagent limit does not report it: no section, rather than a form that fails.
  const limited = (meta?.providers ?? []).filter((p) => p.capabilities.subagent_models);
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

  const accounts = (meta?.providers ?? []).filter((p) => p.capabilities.account);
  const missingKeys = configured.filter((p) => !p.key_present);
  // What needs the user, marked on its page in the rail.
  const attention: Partial<Record<SettingsPage, PageAttention>> = {};
  if (utilityPaused) attention['background-ai'] = { tone: 'warning', text: 'Background AI is paused for today' };
  if (missingKeys.length) attention.providers = { tone: 'error', text: `API key missing: ${missingKeys.map((p) => p.name).join(', ')}` };
  if (accounts.some((p) => p.signed_out)) attention.copilot = { tone: 'error', text: 'Signed out' };
  else if (cliUpdate) attention.copilot = { tone: 'warning', text: 'Copilot CLI update available' };
  if (restart) attention.uam = { tone: 'warning', text: 'Restart available' };
  // The current values under each page's name in the phone's list.
  const defaultModel = taskDefaults && meta?.providers.find((p) => p.name === taskDefaults.provider)?.models.find((m) => m.id === taskDefaults.model);
  const menuModels = modelGroups.flatMap((g) => g.models.map((m) => ({ owner: g.owner, id: m.id })));
  const utility = titled[0];
  const utilityChoice = utility ? settings.title_model?.[utility.name] ?? '' : '';
  const customCount = configured.reduce((n, p) => n + p.models.length, 0);
  const summary: Partial<Record<SettingsPage, string>> = {
    'new-tasks': taskDefaults ? [defaultModel?.name ?? 'Provider default', taskDefaults.effort && `${effortLabel(taskDefaults.effort)} effort`, taskDefaults.mode === 'yolo' ? 'Yolo' : 'Safe'].filter(Boolean).join(' · ') : undefined,
    composer: `Enter while running: ${settings.send_default === 'steer' ? 'Send now' : 'After this turn'} · Suggested replies ${settings.suggest_replies === false ? 'off' : 'on'}`,
    models: meta ? `${menuModels.filter((m) => !settings.hidden_models?.[m.owner]?.includes(m.id)).length} of ${menuModels.length} in the menus` : undefined,
    'background-ai': utility && `Utility model: ${utilityChoice === UTILITY_NONE ? 'None' : utilityChoice ? utility.models.find((m) => m.id === utilityChoice)?.name ?? utilityChoice : cheapestLabel(utility)}`,
    copilot: accounts.length ? [accounts.some((p) => p.signed_out) ? 'Signed out' : 'Signed in', cliUpdate && `CLI ${cliUpdate} available`].filter(Boolean).join(' · ') : undefined,
    providers: configured.length ? `${configured.map((p) => p.name).join(', ')} · ${customCount} ${customCount === 1 ? 'model' : 'models'}` : 'None added',
    uam: [meta?.service && `Running ${meta.service.running}`, `Terminal ${settings.terminal ? 'on' : 'off'}`].filter(Boolean).join(' · '),
    browser: `Theme: ${{ light: 'Light', dark: 'Dark', system: 'Match system' }[theme]}`,
  };
  // A page the instance on screen cannot fill is left out of the rail.
  const pages = SETTINGS_PAGES.filter((p) => {
    if (p.id === 'connections') return Boolean(connections);
    if (p.id === 'copilot') return !meta || accounts.length > 0;
    if (p.id === 'mcp') return !meta || meta.providers.some((provider) => provider.capabilities.mcp);
    return true;
  });
  const visible = new Set(searching && wide ? pages.filter((p) => pageMatches(p, query, summary[p.id])).map((p) => p.id) : shown ? [shown] : []);
  if ([...visible].some((id) => !visited.has(id))) setVisited(new Set([...visited, ...visible]));
  const scopeLine = (scope: SettingsScope) => {
    if (scope === 'browser') return 'Saved in this browser only';
    if (scope === 'copilot') return `Copilot's own files on ${api.owner ? api.owner.label : 'the server'}, global or for one project`;
    return `Saved on ${api.owner ? api.owner.label : 'the server'}, for every browser`;
  };
  const current = pages.find((p) => p.id === shown);

  // Each page's cards. Token costs, Archived and Connected instances mount only while on screen; Agents, Skills, Hooks
  // and Instructions from their first showing; the rest stay mounted, hidden, from the start.
  const cards: Record<SettingsPage, ReactNode> = {
    gitlab: <Section id="gitlab" title="GitLab" subtitle={`Pipeline investigations and glab access on ${api.owner?.label ?? 'this server'}.`}>{api.supports('gitlab-status-v1') ? <GitLabSettings automatic={api.supports('gitlab-pipelines-v1') && loaded ? { on: !!settings.gitlab_auto_investigate, saving, onChange: (gitlab_auto_investigate) => void save({ gitlab_auto_investigate }) } : undefined} /> : <Unsupported what="GitLab access checks" />}</Section>,
    'new-tasks': !loaded || catalogPending ? <PendingSection id="new-tasks" title="New tasks" label={loaded ? 'Loading the model catalog…' : ''} /> : taskDefaults && (
      <Section id="new-tasks" title="New tasks" help="What a new task starts with, in every project. The composer can still change each one before the first message.">
        {/* One stacked column of fields, the compaction threshold with them rather than out at the card's edge. */}
        <div className="flex max-w-xl flex-col gap-3">
          <TaskDefaultsFields prefix="new-tasks" value={taskDefaults} disabled={saving} onChange={(next) => void save({ task_defaults: next })} />
          <Field id="compact-threshold" label="Compact the conversation when its context reaches" hint="Compacting earlier keeps answers faster and cheaper but drops older detail sooner. A task already open picks up a change the next time it reopens.">
            <Select id="compact-threshold" aria-describedby="compact-threshold-hint" value={String(settings.compact_threshold ?? DEFAULT_COMPACT_THRESHOLD)} disabled={saving} items={COMPACT_THRESHOLDS.map((n) => ({ value: String(n), label: n === DEFAULT_COMPACT_THRESHOLD ? `${n}% (default)` : `${n}%` }))} onValueChange={(v) => void save({ compact_threshold: Number(v) === DEFAULT_COMPACT_THRESHOLD ? null : Number(v) })} />
          </Field>
        </div>
      </Section>
    ),
    composer: loaded ? <Section id="composer" title="Composer">
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
    </Section> : <PendingSection id="composer" title="Composer" label="Loading settings…" />,
    archived: loaded ? <Section id="archived" title="Archived" help="Open an archived task to read its conversation. Choose Unarchive from its menu to return it to Finished. Search in the sidebar finds archived tasks too.">
      <ArchivedTasks />
    </Section> : <PendingSection id="archived" title="Archived" label="Loading tasks…" />,
    models: <>
      {!loaded || catalogPending ? <PendingSection id="models" title="Models" label="" /> : <Section id="models" title="Models" help="Hidden models leave the selection menus. Tasks already using one keep it. New models appear automatically.">
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
            {p.custom?.key_present === false && <Note tone="error">{p.custom.api_key_env ? `${p.custom.api_key_env} is not set in the service's environment.` : 'The API key is missing.'} Check it in Custom providers.</Note>}
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
      {/* As Utility model: one provider is the card's secondary line; several get a group each, named by provider. */}
      {loaded && !catalogPending && limited.length > 0 && <Section id="subagent-models" title="Subagent models" help={SUBAGENT_HELP} subtitle={limited.length === 1 ? limited[0].display_name : undefined}>
        {limited.length === 1 ? <SubagentModels provider={limited[0]} settings={settings} saving={saving} onSave={(patch) => void save(patch)} /> : limited.map((p) => (
          <div key={p.name} role="group" aria-label={`${p.display_name} subagent models`} className="flex flex-col gap-1">
            <h3 className="mb-1 text-ui font-medium">{p.display_name}</h3>
            <SubagentModels provider={p} settings={settings} saving={saving} onSave={(patch) => void save(patch)} />
          </div>
        ))}
      </Section>}
    </>,
    'background-ai': loaded && <>
      {/* One provider (the usual case): the card's title labels its select and the provider is the secondary line; several get a row each, named by provider. */}
      {catalogPending ? <PendingSection id="utility" title="Utility model" label="" /> : titled.length > 0 && <Section id="utility" title="Utility model" help={titled.length === 1 ? UTILITY_HELP : undefined} subtitle={titled.length === 1 ? titled[0].display_name : undefined} control={titled.length === 1 ? <UtilitySelect provider={titled[0]} settings={settings} saving={saving} describedBy="utility-help" onSave={(patch) => void save(patch)} /> : undefined}>
        {titled.length > 1 && titled.map((p) => (
          <Row key={p.name} id={`utility-${p.name}`} label={p.display_name} help={UTILITY_HELP}>
            <UtilitySelect provider={p} settings={settings} saving={saving} describedBy={`utility-${p.name}-help`} onSave={(patch) => void save(patch)} />
          </Row>
        ))}
      </Section>}
      <Section id="background-ai" title="Background AI" help="UAM's own AI calls on the Utility model: task titles, suggested replies, outcome lines, and agent, skill and hook drafts. Each one costs AI credits. Every call is kept here for 30 days.">
        <BackgroundAI limitSetting={settings.utility_daily_limit} unlimited={!!settings.utility_unlimited} saving={saving} onSaveLimit={(utility_daily_limit) => save({ utility_daily_limit })} onSaveUnlimited={(utility_unlimited) => save({ utility_unlimited })} onPaused={setUtilityPaused} />
      </Section>
    </>,
    'token-costs': loaded && <Section id="token-prices" title="Token costs">{api.supports('usage-v1') ? <TokenPricing /> : <Unsupported what="token costs" />}</Section>,
    copilot: <>
      {accounts.map((p) => (
        <Section key={p.name} id={`account-${p.name}`} title={p.display_name}>
          {api.supports('provider-accounts-v1') ? <CopilotAccount provider={p} /> : <Unsupported what="provider sign-in" />}
          {p.capabilities.cli_update && <CopilotCli provider={p} />}
        </Section>
      ))}
    </>,
    providers: loaded ? <Section id="providers" title="Custom providers" help="OpenAI-compatible endpoints and their keys. Choose which of their models the menus offer in Models.">
      {catalogPending && <Note role="status">Loading provider accounts…</Note>}
      {metaError && <Note tone="error" role="alert">Could not load provider accounts: {metaError} <Button size="sm" onClick={refreshMeta}>Retry</Button></Note>}
      <CustomModels models={settings.custom_models ?? []} disabled={saving} onSave={saveCustom} />
    </Section> : <PendingSection id="providers" title="Custom providers" label="Loading settings…" />,
    agents: null,
    skills: null,
    hooks: null,
    instructions: null,
    mcp: loaded && (meta?.providers ?? []).some((p) => p.capabilities.mcp) && <Section id="mcp" title="MCP servers">
      <McpServersSettings terminal={!!settings.terminal} githubMcp={(meta?.providers ?? []).some((p) => p.capabilities.github_mcp) ? { on: !!settings.github_mcp, saving, onChange: (github_mcp) => void save({ github_mcp }) } : undefined} />
    </Section>,
    uam: <>
      {/* A service too old to report its binary has no card: nothing to show or restart. */}
      {meta?.service && <Section id="uam" title="UAM">
        <UamService />
      </Section>}
      {loaded && api.supports('project-location-v1') && <Section id="project-folders" title="Project folders">
        <form className="flex flex-col gap-3" onSubmit={(e) => {
          e.preventDefault();
          if (!saving && !projectBrowsing) void save({ default_project_dir: (projectDir ?? settings.default_project_dir ?? '').trim() });
        }}>
          <Field id="default-project-dir" label="Default project location" hint={`Add project starts browsing here on ${api.owner?.label ?? 'this instance'}. Use this folder saves your choice; press Enter to save a typed path. Leave empty to use the home directory. Existing projects stay where they are.`}>
            <div className="flex flex-wrap items-center gap-2">
              <Input ref={projectInput} id="default-project-dir" className="min-w-0 flex-1 basis-full text-ui sm:basis-48" spellCheck={false} autoComplete="off" placeholder="/path/to/projects" aria-describedby="default-project-dir-hint" value={projectDir ?? settings.default_project_dir ?? ''} readOnly={saving} onChange={(e) => setProjectDir(e.target.value)} />
              <Button ref={projectBrowse} variant="secondary" disabled={saving || !api.supports('files-v1')} aria-describedby={api.supports('files-v1') ? undefined : 'default-project-dir-browse-note'} aria-expanded={projectBrowsing} aria-controls={projectBrowsing ? 'default-project-dir-picker' : undefined} onClick={() => setProjectBrowsing(!projectBrowsing)}>
                <FolderOpen />
                Browse
              </Button>
            </div>
          </Field>
          {!api.supports('files-v1') && <Note id="default-project-dir-browse-note">{`${api.owner?.label ?? 'Main Server'} does not offer folder browsing. Type the folder's path, or update UAM there.`}</Note>}
          {projectPicker.mounted && (
            <Collapse open={projectBrowsing} appear soft onClosed={projectPicker.onClosed}>
              <Suspense fallback={null}>
                <FolderPicker id="default-project-dir-picker" start={projectDir ?? settings.default_project_dir ?? ''} onUse={(path) => {
                  setProjectDir(path);
                  void save({ default_project_dir: path });
                  setProjectBrowsing(false);
                  projectInput.current?.focus();
                }} onClose={() => {
                  setProjectBrowsing(false);
                  projectBrowse.current?.focus();
                }} />
              </Suspense>
            </Collapse>
          )}
        </form>
      </Section>}
      {loaded && <Section id="shell" title="Shell access">
        {api.supports('terminal-v1') ? <Row id="terminal" label="Terminal" help={`Open a shell in the project folder from a Task's header. Anyone signed in can then run commands on ${api.owner ? api.owner.label : 'this machine'} as the uam user, without the agent's permission prompts.`}>
          <Switch aria-label="Terminal" aria-describedby="terminal-help" checked={!!settings.terminal} disabled={saving} onCheckedChange={(terminal) => void save({ terminal })} />
        </Row> : <Unsupported what="the terminal" />}
      </Section>}
      {onLogout && <Section id="session" title="Session">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <Note>Sign out of UAM in this browser.</Note>
          <Button variant="secondary" onClick={onLogout}><LogOut aria-hidden="true" />Log out</Button>
        </div>
      </Section>}
    </>,
    connections: <Section id="connections" title="Connected instances">{connections}</Section>,
    browser: <Section id="browser" title="Appearance & alerts">
      {api.owner && <Note>Kept in this browser, not on {api.owner.label}: these apply whichever instance is on screen.</Note>}
      <NotifyRow />
      <Row id="theme" label="Theme" help="Match system follows your OS's light or dark setting as it changes.">
        <Segmented
          aria-labelledby="theme-label"
          aria-describedby="theme-help"
          value={theme}
          onValueChange={(v) => {
            setTheme(v as Theme);
            saveTheme(v as Theme);
          }}
          items={[
            { value: 'light', label: 'Light' },
            { value: 'dark', label: 'Dark' },
            { value: 'system', label: 'Match system' },
          ]}
        />
      </Row>
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
    </Section>,
  };
  for (const kind of ['agents', 'skills', 'hooks', 'instructions'] as const) cards[kind] = <Section id={kind} title={SETTINGS_PAGES.find((p) => p.id === kind)!.label}>{api.supports('configuration-v1') ? <ConfigurationSettings kind={kind} projects={projects} terminal={!!settings.terminal} scope={toolkitScope} onScope={setToolkitScope} heldBy={held.find((k) => k !== kind)} onHold={hold} /> : <Unsupported what="agents, skills, hooks and instructions" />}</Section>;
  const lazy = (id: SettingsPage) => id === 'token-costs' || id === 'archived' || id === 'connections' || id === 'gitlab';

  return (
    <FieldHelpProvider><div className={cn('flex min-h-0 flex-1 flex-col', !switched && 'animate-rise')}>
      <header className="pane-header flex h-header shrink-0 items-center gap-1.5 pr-2 pl-3" data-scrolled={((wide || shown) && scrolled) || undefined}>
        {leading}
        <div className="flex min-w-0 flex-1 items-center gap-1">
          {!wide && current ? <>
            <Tip label="All settings">
              <Button ref={back} size="icon-md" aria-label="All settings" className="-ml-1 text-muted" onClick={() => { returnTo.current = current.id; setPage(null); }}>
                <ChevronLeft />
              </Button>
            </Tip>
            <h1 className="min-w-0 truncate text-display-sm text-ink">{current.label}<span className="sr-only"> · Settings{instance && ` · ${instance}`}</span></h1>
          </> : <h1 className="shrink-0 text-display-sm text-ink">Settings{instance && <span className="sr-only"> · {instance}</span>}</h1>}
          {instance && <><span aria-hidden="true" className="text-muted">·</span><InstanceSwitcher place="settings" onConnect={() => { setPage('connections'); setQuery(''); }} /></>}
        </div>
        {saving && <Spinner className="shrink-0" />}
        <Tip label="Close settings">
          <Button size="icon-md" aria-label="Close settings" className="text-muted" onClick={onClose}>
            <X />
          </Button>
        </Tip>
      </header>
      <div className="flex min-h-0 flex-1">
        {(wide || !shown) && <SettingsRail pages={pages} current={searching ? null : shown} query={query} onQuery={setQuery} onOpen={open} attention={attention} summary={summary} list={!wide} />}
        {/* Mounted under the phone's list too: hidden cards keep their forms, and Background AI keeps reading today's state for the rail. */}
        <div ref={scrollArea} hidden={!shown && !wide} className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden overscroll-contain [scrollbar-gutter:stable]">
          <ScrollSentinel sentinelRef={sentinel} />
          <div className={cn('flex w-full min-w-0 flex-col px-4 py-4 md:pr-6 md:pl-2', searching ? 'gap-6' : 'gap-4', switched && 'animate-fade-in')}>
            {api.owner && unsupported.length > 0 && <Note>Unavailable on {api.owner.label}: {unsupported.join(', ')}.</Note>}
            {searching && visible.size === 0 && <Note role="status">No setting matches “{query.trim()}”.</Note>}
            {pages.map((p) => {
              const on = visible.has(p.id);
              if (!on && (lazy(p.id) || (p.group.label === 'Agent toolkit' && p.id !== 'mcp' && !visited.has(p.id)))) return null;
              return <div key={p.id} hidden={!on} className="flex flex-col gap-4">
                {searching ? <button type="button" className="flex items-center gap-1 self-start rounded-sm px-1 text-caption text-muted hover:text-ink" onClick={() => open(p.id)}>
                  {p.group.label}<ChevronRight aria-hidden="true" className="size-3.5" /><span className="font-medium text-ink">{p.label}</span>
                </button> : <p className="px-1 text-caption text-muted">{p.blurb} {scopeLine(p.group.scope)}.</p>}
                {cards[p.id]}
              </div>;
            })}
            {error && (
              <Note tone="error" role="alert">
                {error}
              </Note>
            )}
          </div>
        </div>
      </div>
    </div></FieldHelpProvider>
  );
}
