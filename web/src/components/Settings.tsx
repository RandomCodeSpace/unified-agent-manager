import { X } from 'lucide-react';
import { useContext, useRef, useState, type ReactNode, type SubmitEvent } from 'react';
import { PlannerContext } from './planner/context';
import { DEFAULT_COMPACT_THRESHOLD, api, describeError, plannerErrorText, resolveTaskDefaults, routeMissing, type CustomModel, type ImportReport, type Model, type Project, type ProviderInfo, type SendDefault, type Settings } from '../api';
import { BackgroundAI } from './BackgroundAI';
import { CopilotAccount } from './CopilotAccount';
import { McpServersSettings } from './McpServers';
import { Note, Skeleton, Spinner, useApp, useScrolled, ScrollSentinel } from './common';
import { byCodeUnit } from '../lib/order';
import { Field, TaskDefaultsFields, choiceLabel } from './TaskDefaults';
import { customProviders, matchingIds, withProvider, type CustomProvider } from '../lib/customModels';
import { modelCostLine } from '../lib/cost';
import { cheapestLabel, modelChoices, UTILITY_NONE } from '../lib/models';
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
import { Tip } from './ui/tooltip';

const NO_PROJECTS: Project[] = [];

/** One titled group of settings, a floating card (DESIGN.md Settings view); a new group is another `Section` below the last. */
function Section({ id, title, children }: Readonly<{ id: string; title: string; children: ReactNode }>) {
  return (
    <section aria-labelledby={`${id}-title`} className="flex flex-col gap-4 rounded-lg bg-raised p-5 shadow-raised">
      <h2 id={`${id}-title`} className="text-title text-ink">
        {title}
      </h2>
      {children}
    </section>
  );
}

/** A section whose content is still on its way (the first snapshot, the catalogs): the card with its title over a skeleton. */
function PendingSection({ id, title, label }: Readonly<{ id: string; title: string; label: string }>) {
  return (
    <section aria-labelledby={`${id}-title`} aria-busy="true" className="flex flex-col gap-4 rounded-lg bg-raised p-5 shadow-raised">
      <h2 id={`${id}-title`} className="text-title text-ink">
        {title}
      </h2>
      <Skeleton label={label} rows={3} />
    </section>
  );
}

/** The compaction thresholds Settings offers, in percent. */
const COMPACT_THRESHOLDS = [50, 55, 60, 65, 70, 75, 80, 85, 90];

/** A label and its help on the left, the control on the right; stacked on a phone. */
function Row({ id, label, help, children }: Readonly<{ id: string; label: string; help: ReactNode; children: ReactNode }>) {
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
 * from what it lists (the service loads the list) or typed in. The key never passes through
 * here; each provider names the service environment variable that holds it.
 */
function CustomModels({ models, disabled, onSave }: Readonly<{ models: CustomModel[]; disabled: boolean; onSave: (next: CustomModel[]) => Promise<boolean> }>) {
  const [draft, setDraft] = useState<ProviderDraft | null>(null);
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);
  const providers = customProviders(models);
  const busy = disabled || loading;
  // Removing a provider or one of its models is confirmed first (DESIGN.md Confirmations).
  const removal = useConfirm<Removal>();
  const pending = removal.target;
  function remove() {
    const r = removal.target;
    if (!r) return;
    removal.close();
    void onSave(withProvider(models, r.provider.name, r.provider, r.model ? r.provider.models.filter((o) => o.model_id !== r.model).map((o) => o.model_id) : []));
  }

  function edit(p?: CustomProvider) {
    setLoadError(null);
    setDraft(p ? { ...newProvider, original: p.name, name: p.name, base_url: p.base_url, api_key_env: p.api_key_env, ids: p.models.map((m) => m.model_id), selected: p.models.map((m) => m.model_id) } : newProvider);
  }
  async function load(d: ProviderDraft) {
    setLoading(true);
    setLoadError(null);
    try {
      const res = await api.discoverModels({ base_url: d.base_url.trim(), api_key_env: d.api_key_env.trim() });
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
    const provider = { name: d.name.trim(), base_url: d.base_url.trim(), api_key_env: d.api_key_env.trim() };
    if (await onSave(withProvider(models, d.original, provider, d.selected))) setDraft(null);
  }
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
        <section key={p.name} aria-label={`${p.name} models`} className="flex flex-col rounded-md bg-tint-well px-3 py-2">
          <div className="flex min-h-8 items-center gap-2">
            <div className="flex min-w-0 flex-1 flex-col">
              <span className="text-ui font-medium text-ink">{p.name}</span>
              <span className="min-w-0 break-all text-meta text-muted">{p.base_url}</span>
              <Note tone={p.key_present ? 'muted' : 'warn'}>{p.key_present ? `Key from ${p.api_key_env}` : `${p.api_key_env} is not set in the service's environment`}</Note>
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
      {draft && (
        <form aria-label={draft.original ? `Edit provider ${draft.original}` : 'Add a provider'} className="flex flex-col gap-3 rounded-md bg-tint-well p-3" onSubmit={(e) => void save(e, draft)}>
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
            <fieldset aria-label="Models to offer" className="flex max-h-64 flex-col overflow-y-auto rounded-sm bg-raised px-2 py-1 shadow-well">
              {matchingIds(draft.ids, draft.query).map((id) => (
                <label key={id} className="flex min-h-7 items-center gap-2 text-ui text-ink">
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
 * Settings → Planner (ADR 0005 §17): the switch (off by default; it cannot turn on without a git
 * binary), the Utility model its suggestions and triage use, and the one-time import of a board
 * from the kb app: a source directory in, a report out.
 */
function PlannerSection({ settings, saving, projects, providers, onSave }: Readonly<{ settings: Settings; saving: boolean; projects: Project[]; providers: ProviderInfo[]; onSave: (patch: Partial<Settings>) => void }>) {
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
    <Section id="planner" title="Planner">
      <Row id="planner-switch" label="Planner" help={noGit ? 'Git is not installed on the server, so the planner cannot turn on.' : 'Epics, stories and subtasks for each git project, which agents decompose and carry out and you confirm, launch and close. Off, nothing of it shows.'}>
        <Switch aria-label="Planner" aria-describedby="planner-switch-help" checked={!!settings.planner} disabled={saving || (noGit && !settings.planner)} onCheckedChange={(planner) => onSave({ planner })} />
      </Row>
      {settings.planner && utility.length > 0 && <Note>Suggestions and triage use the Utility model: {utility.join('; ')}.</Note>}
      {settings.planner && (
        <form aria-label="Import from kb" className="flex flex-col gap-2" onSubmit={(e) => void run(e)}>
          <span id="planner-import-label" className="text-ui font-medium text-ink">Import from kb</span>
          <Note id="planner-import-help">Copy cards from a kb board directory. Cards whose project name matches a project here join its board; the rest wait in Unassigned. Running it again adds no duplicates.</Note>
          <div className="flex flex-wrap items-center gap-2">
            <Input aria-labelledby="planner-import-label" aria-describedby="planner-import-help" className="max-w-md flex-1 text-ui" spellCheck={false} autoComplete="off" placeholder="/home/you/.local/share/kb" value={dir} disabled={importing || importMissing} onChange={(e) => setDir(e.target.value)} />
            <Button type="submit" variant="secondary" size="lg" loading={importing} disabled={!dir.trim() || importMissing}>
              Import
            </Button>
          </div>
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
  let help = 'When a Task asks you something, needs your permission, fails or finishes, unless you are looking at it.';
  if (support === 'home-screen') help = 'On iPhone and iPad this works in the Home Screen app only: tap Share, then Add to Home Screen, open UAM from there and turn this on.';
  else if (support === 'none') help = 'This browser cannot show notifications.';
  else if (mode === 'push') help += ' They arrive even with UAM closed.';
  else if (mode === 'page') help += ' This browser shows them only while UAM is open in a tab.';
  return (
    <>
      <Row id="notify" label="Notify me when a Task needs me or finishes" help={help}>
        <Switch aria-label="Notify me when a Task needs me or finishes" aria-describedby="notify-help" checked={mode !== null} disabled={busy || support !== 'ok'} onCheckedChange={(on) => void toggle(on)} />
      </Row>
      {problem && (
        <Note tone="error" role="alert">
          {problem}
        </Note>
      )}
    </>
  );
}

export function SettingsView({ leading, onClose }: Readonly<{ leading?: ReactNode; onClose: () => void }>) {
  const { settings, dispatch, meta, metaError, loaded, refreshMeta } = useApp();
  const projects = useContext(PlannerContext)?.projects ?? NO_PROJECTS;
  // The catalogs are not here yet and have not failed: their sections are skeletons, never absent or empty.
  const catalogPending = !meta && !metaError;
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

  return (
    <div className="flex min-h-0 flex-1 flex-col animate-rise">
      <header className="pane-header flex h-header shrink-0 items-center gap-1.5 pr-2 pl-3" data-scrolled={scrolled || undefined}>
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
        <ScrollSentinel sentinelRef={sentinel} />
        <div className="flex w-full min-w-0 flex-col gap-4 px-4 py-4 md:px-6">
          <p className="text-caption text-muted">Kept by the service, so they apply in every browser. This browser's own settings are at the end.</p>
          {(meta?.providers ?? []).filter((p) => p.capabilities.account).map((p) => (
            <Section key={p.name} id={`account-${p.name}`} title={p.display_name}>
              <CopilotAccount provider={p} />
            </Section>
          ))}
          {!loaded && (
            <>
              <PendingSection id="composer" title="Composer" label="Loading settings…" />
              <PendingSection id="new-tasks" title="New tasks" label="" />
              <PendingSection id="models" title="Models" label="" />
            </>
          )}
          {loaded && <Section id="composer" title="Composer">
            <Row
              id="send-default"
              label="While a task is running, Enter…"
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
            <Row id="suggest-replies" label="Suggest replies" help="After a turn, up to three short replies you might send next show above the composer. Choosing one fills the composer; nothing is sent. It takes one Utility model call per finished turn you open.">
              <Switch aria-label="Suggest replies" aria-describedby="suggest-replies-help" checked={settings.suggest_replies !== false} disabled={saving} onCheckedChange={(suggest_replies) => void save({ suggest_replies })} />
            </Row>
          </Section>}
          {loaded && catalogPending && <PendingSection id="new-tasks" title="New tasks" label="Loading the model catalog…" />}
          {loaded && catalogPending && <PendingSection id="utility" title="Utility model" label="" />}
          {loaded && catalogPending && <PendingSection id="models" title="Models" label="" />}
          {loaded && taskDefaults && (
            <Section id="new-tasks" title="New tasks">
              <Note>What a new task starts with, in every project. The composer can still change each one before the first message.</Note>
              <div className="max-w-xl">
                <TaskDefaultsFields prefix="new-tasks" value={taskDefaults} disabled={saving} onChange={(next) => void save({ task_defaults: next })} />
              </div>
              <Row id="compact-threshold" label="Compact the conversation when its context reaches" help="Compacting earlier keeps answers faster and cheaper but drops older detail sooner. A task already open picks up a change the next time it reopens.">
                <Select aria-label="Compact the conversation when its context reaches" aria-describedby="compact-threshold-help" value={String(settings.compact_threshold ?? DEFAULT_COMPACT_THRESHOLD)} disabled={saving} className="sm:w-44" items={COMPACT_THRESHOLDS.map((n) => ({ value: String(n), label: n === DEFAULT_COMPACT_THRESHOLD ? `${n}% (default)` : `${n}%` }))} onValueChange={(v) => void save({ compact_threshold: Number(v) === DEFAULT_COMPACT_THRESHOLD ? null : Number(v) })} />
              </Row>
            </Section>
          )}
          {loaded && titled.length > 0 && <Section id="utility" title="Utility model">
            {titled.map((p) => {
              const current = settings.title_model?.[p.name] ?? '';
              const choices = modelChoices(p.models, settings.hidden_models?.[p.name], current === UTILITY_NONE ? '' : current);
              const cost = (m: Model) => (p.capabilities.usage ? modelCostLine(m) : '');
              const cheapest = p.models.find((m) => m.id === p.cheapest_model);
              return <Row key={p.name} id={`utility-${p.name}`} label={p.display_name} help="The model UAM uses to title new tasks, summarize completed subagent results, suggest replies and phrase turn outcomes. Left as Cheapest, a new task is titled by its own model at its lowest effort, which spends that model's credits. None keeps provider titles and result excerpts without utility AI calls.">
                <Select aria-label={`${p.display_name} utility model`} aria-describedby={`utility-${p.name}-help`} value={current} disabled={saving} className="sm:w-72" items={[
                  { value: '', label: cheapestLabel(p), description: cheapest && cost(cheapest) },
                  { value: UTILITY_NONE, label: 'None (no utility AI)' },
                  ...choices.map(({ model, note }) => ({ value: model.id, label: choiceLabel(model.name, note), description: cost(model), hidden: !!note })),
                ]} onValueChange={(id) => void save({ title_model: { ...settings.title_model, [p.name]: id } })} />
              </Row>;
            })}
          </Section>}
          {loaded && <Section id="background-ai" title="Background AI">
            <BackgroundAI limitSetting={settings.utility_daily_limit} saving={saving} onSaveLimit={(utility_daily_limit) => save({ utility_daily_limit })} />
          </Section>}
          {loaded && !catalogPending && <Section id="models" title="Models">
            <Note>Hidden models leave the selection menus. Tasks already using one keep it. New models appear automatically.</Note>
            {metaError && (
              <Note tone="error" role="alert" className="flex flex-wrap items-center gap-2">
                <span className="min-w-0 flex-1">Could not load the model catalog: {metaError}</span>
                <Button size="sm" variant="secondary" onClick={refreshMeta}>
                  Retry
                </Button>
              </Note>
            )}
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
                  return <div key={m.id} className="flex min-h-12 items-center gap-3 py-2">
                    <div className="flex min-w-0 flex-1 flex-col">
                      <span className="text-ui font-medium text-ink">{m.name}</span>
                      <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5 text-meta text-muted">
                        <span className="min-w-0 break-all">{m.id}{!offered ? ' · not offered now' : ''}</span>
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
          </Section>}
          {/* A service that does not know the planner setting yet has no planner: no row at all. */}
          {loaded && settings.planner !== undefined && <PlannerSection settings={settings} saving={saving} projects={projects} providers={meta?.providers ?? []} onSave={(patch) => void save(patch)} />}
          {loaded && <Section id="shell" title="Shell access">
            <Row id="terminal" label="Terminal" help="Open a shell in the project folder from a Task's header. Anyone signed in can then run commands on this machine as the uam user, without the agent's permission prompts.">
              <Switch aria-label="Terminal" aria-describedby="terminal-help" checked={!!settings.terminal} disabled={saving} onCheckedChange={(terminal) => void save({ terminal })} />
            </Row>
          </Section>}
          {loaded && (meta?.providers ?? []).some((p) => p.capabilities.mcp) && <Section id="mcp" title="MCP servers">
            <McpServersSettings terminal={!!settings.terminal} />
          </Section>}
          <Section id="browser" title="This browser">
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
    </div>
  );
}
