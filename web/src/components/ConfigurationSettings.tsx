import { ChevronRight } from 'lucide-react';
import { Fragment, useEffect, useRef, useState, type ComponentProps, type ReactNode, type SubmitEvent } from 'react';
import { api, describeError, isStatus, routeMissing, type Configuration, type ConfigurationDraft, type ConfigurationFile, type ConfigurationKind, type Model, type Project } from '../api';
import { cn } from '../lib/cn';
import { Markdown, Note, Skeleton, useApp } from './common';
import { UTILITY_NONE, visibleModels } from '../lib/models';
import { Field, SectionAction, useInlineForm } from './TaskDefaults';
import { AlertDialog, Dialog, useConfirm } from './ui/dialog';
import { Button } from './ui/button';
import { Input } from './ui/input';
import { Select } from './ui/select';
import { Segmented } from './ui/segmented';
import { HelpTip } from './ui/tooltip';

const REFERENCES: Record<ConfigurationKind, string> = { agents: 'https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-command-reference#custom-agent-frontmatter-fields', skills: 'https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-command-reference#skill-frontmatter-fields', hooks: 'https://docs.github.com/en/copilot/reference/hooks-reference', instructions: 'https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-custom-instructions' };
const LABELS: Record<ConfigurationKind, string> = { agents: 'agent', skills: 'skill', hooks: 'hook file', instructions: 'instructions' };
const HOOK_EVENTS = ['sessionStart', 'sessionEnd', 'userPromptSubmitted', 'userPromptTransformed', 'preToolUse', 'postToolUse', 'postToolUseFailure', 'agentStop', 'subagentStart', 'subagentStop', 'permissionRequest', 'preCompact', 'errorOccurred', 'notification'];
const HOOK_MATCHERS: Record<string, string> = { notification: 'notification_type', permissionRequest: 'toolName', preToolUse: 'toolName', postToolUse: 'toolName', preCompact: 'trigger', subagentStart: 'agentName' };

function TextArea(props: ComponentProps<'textarea'>) {
  return <textarea rows={6} spellCheck={false} {...props} className="w-full min-w-0 resize-y rounded-sm bg-sunken px-2.5 py-2 font-mono text-code-sm text-ink shadow-well placeholder:text-muted focus-visible:shadow-focus focus-visible:outline-none disabled:opacity-45" />;
}

interface Draft {
  name: string;
  path: string;
  revision: string;
  raw: string | null;
  description: string;
  prompt: string;
  model: string;
  modelOrder: string[];
  modelPolicy: string;
  reasoningEffort: string;
  includeInstructions: boolean;
  tools: string;
  event: string;
  bash: string;
  powershell: string;
  hookType: string;
  execution: string;
  command: string;
  executable: string;
  args: string;
  url: string;
  headers: string;
  allowedEnv: string;
  matcher: string;
  disableHooks: boolean;
  cwd: string;
  timeout: string;
  env: string;
  metadata: string;
  mcp: string;
  target: string;
  automatic: boolean;
  invocable: boolean;
  noTools: boolean;
  license: string;
  compatibility: string;
  argumentHint: string;
}

function createDraft(file?: ConfigurationFile): Draft {
  return { name: file?.name ?? '', path: file?.path ?? '', revision: file?.revision ?? '', raw: file?.content ?? null, description: '', prompt: '', model: '', modelOrder: [], modelPolicy: '', reasoningEffort: '', includeInstructions: false, tools: '', event: HOOK_EVENTS[0], bash: '', powershell: '', hookType: 'command', execution: 'shell', command: '', executable: '', args: '', url: '', headers: '', allowedEnv: '', matcher: '', disableHooks: false, cwd: '', timeout: '', env: '', metadata: '', mcp: '', target: '', automatic: true, invocable: true, noTools: false, license: '', compatibility: '', argumentHint: '' };
}

function authoredModels(draft: Draft): string[] { return draft.modelOrder.length ? draft.modelOrder : draft.model ? [draft.model] : []; }
function agentEfforts(draft: Draft, models: Model[]): string[] {
  const selected = authoredModels(draft).map((id) => models.find((model) => model.id === id));
  return (selected[0]?.efforts ?? []).filter((effort) => selected.every((model) => model?.efforts?.includes(effort)));
}

function fileName(file: Pick<ConfigurationFile, 'name' | 'path'>): string {
  return file.path.split(/[\\/]/).pop() || file.name;
}

/** A disclosure's summary with the chevron Settings uses elsewhere (Background AI's log), not the native marker. Its `details` carries `group`. */
function Summary({ children }: Readonly<{ children: ReactNode }>) {
  return <summary className="flex cursor-pointer list-none items-center gap-1.5 text-ui font-medium [&::-webkit-details-marker]:hidden">
    <ChevronRight aria-hidden="true" className="size-4 shrink-0 text-muted transition-transform duration-160 group-open:rotate-90" />
    {children}
  </summary>;
}

/** A path that wraps after a slash before it breaks inside a name. */
function PathText({ path, className }: Readonly<{ path: string; className?: string }>) {
  return <span className={cn('block font-mono text-meta [overflow-wrap:anywhere]', className)}>{path.split('/').map((part, i) => <Fragment key={i}>{i > 0 && <>/<wbr /></>}{part}</Fragment>)}</span>;
}

/** System error tails in words; the raw message stays one disclosure away. */
const ERROR_WORDS: [RegExp, string][] = [
  [/no such file or directory/i, 'a file or link target is missing'],
  [/permission denied/i, 'the service is not allowed to read it'],
  [/not a directory/i, 'part of its path is not a folder'],
  [/too many levels of symbolic links/i, 'its links point in a loop'],
];

/** A service error about one file: readable words first ("Skill cannot be read: a file or link target is missing"), then its path, the raw text on demand. */
function FileError({ message, path, tone = 'error', role }: Readonly<{ message: string; path?: string; tone?: 'error' | 'warn'; role?: 'status' }>) {
  const words = ERROR_WORDS.find(([pattern]) => pattern.test(message))?.[1];
  const head = message.split(': ')[0];
  return <div role={role} className={cn('min-w-0 text-caption', tone === 'error' ? 'text-error' : 'text-warning')}>
    {words ? `${head}: ${words}.` : message}
    {path && <PathText path={path} className="mt-1" />}
    {words && <details className="group mt-1 text-muted">
      <Summary><span className="text-caption font-normal">Details</span></Summary>
      <code className="mt-1 block font-mono text-meta [overflow-wrap:anywhere]">{message}</code>
    </details>}
  </div>;
}

/** Where a skill was found, for its group heading: the dot-directory holding `skills/` (.copilot, .github, .agents, .claude), or UAM's own. */
function skillSource(file: ConfigurationFile): string {
  if (file.read_only_reason === 'Built-in skills are managed by UAM.') return 'Built in';
  return /[\\/](\.[^\\/]+)[\\/]skills[\\/]/.exec(file.path)?.[1] ?? 'Other';
}

function ConfigurationViewer({ file, kind, draftOpen, onClose }: Readonly<{ file: ConfigurationFile; kind: ConfigurationKind; draftOpen: boolean; onClose: () => void }>) {
  const [open, setOpen] = useState(true);
  const [view, setView] = useState('preview');
  // Separate only the Markdown frontmatter delimiters. Metadata remains literal and Source stays exact.
  const frontmatter = /^\uFEFF?---[ \t]*\r?\n([\s\S]*?)\r?\n---[ \t]*(?:\r?\n|$)/.exec(file.content);
  const body = frontmatter ? file.content.slice(frontmatter[0].length) : file.content;
  // An instructions file need not exist yet (no revision): the dialog says so, as its row does, rather than "empty".
  const missing = !file.revision && !file.error && !file.content;
  let emptyText = 'This file is empty.';
  if (file.error) emptyText = 'No document content is available.';
  else if (missing) emptyText = 'No saved file at this path.';
  return <Dialog open={open} onOpenChange={setOpen} onClosed={onClose} title={<span className="break-all">{kind === 'instructions' ? fileName(file) : file.name}</span>} description={<span className="break-all">{file.path}</span>} className="min-w-0 max-w-sheet-wide">
    <div className="flex min-w-0 flex-col gap-3">
      {!missing && <Note>Read only. This shows the saved file{draftOpen ? '; any unsaved editor changes stay in the editor' : ''}.</Note>}
      {kind !== 'hooks' && !missing && <Segmented aria-label="Show document as" className="self-start" value={view} onValueChange={setView} items={[{ value: 'preview', label: 'Preview' }, { value: 'source', label: 'Source' }]} />}
      {file.error && <Note tone="error" role="alert">{file.error}</Note>}
      {!file.content && <Note>{emptyText}</Note>}
      {view === 'source' || kind === 'hooks' ?
        // eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- A labelled scroll region must accept keyboard scrolling.
        <pre role="region" aria-label={`${file.name} source`} tabIndex={0} className="max-h-[55dvh] max-w-full overflow-auto whitespace-pre-wrap break-all rounded-sm bg-sunken p-3 font-mono text-code-sm">{file.content}</pre> : <>
          {frontmatter && <div className="min-w-0">
            <p className="mb-1 text-ui font-medium">Configuration</p>
            {/* eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- A labelled scroll region must accept keyboard scrolling. */}
            <pre role="region" aria-label={`${file.name} configuration`} tabIndex={0} className="max-h-64 max-w-full overflow-auto whitespace-pre-wrap break-all rounded-sm bg-sunken p-3 font-mono text-code-sm">{frontmatter[1]}</pre>
          </div>}
          {body.trim() && <Markdown text={body} className="min-w-0 text-chat text-ink" />}
        </>}
    </div>
  </Dialog>;
}

function suggestedDraft(kind: ConfigurationKind, suggestion: ConfigurationDraft): Draft {
  return { ...createDraft(), name: suggestion.name, description: suggestion.description ?? '', prompt: suggestion.prompt ?? '', model: suggestion.model ?? '', tools: suggestion.tools?.join('\n') ?? '', noTools: kind === 'agents' && suggestion.tools?.length === 0, automatic: !suggestion.disable_model_invocation, invocable: suggestion.user_invocable !== false, event: suggestion.event ?? HOOK_EVENTS[0], bash: suggestion.bash ?? '', powershell: suggestion.powershell ?? '', cwd: suggestion.cwd ?? '', timeout: String(suggestion.timeout_sec ?? ''), env: suggestion.env ? JSON.stringify(suggestion.env, null, 2) : '' };
}

function jsonObject(value: string, name: string, strings = false): Record<string, unknown> {
  const parsed = JSON.parse(value) as unknown;
  if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object' || (strings && Object.values(parsed).some((item) => typeof item !== 'string'))) throw new Error(`${name} must be a JSON object${strings ? ' of string values' : ''}.`);
  return parsed as Record<string, unknown>;
}

/** JSON scalar and list values are valid YAML; no handwritten escaping or YAML parsing. */
function documentOf(kind: ConfigurationKind, draft: Draft): string {
  if (draft.raw !== null) return draft.raw;
  if (kind === 'hooks') {
    const action: Record<string, unknown> = { type: draft.hookType };
    if (draft.hookType === 'http') {
      action.url = draft.url.trim();
      if (draft.headers.trim()) action.headers = jsonObject(draft.headers, 'Headers', true);
      if (draft.allowedEnv.trim()) action.allowedEnvVars = draft.allowedEnv.split('\n').map((s) => s.trim()).filter(Boolean);
    } else {
      if (draft.execution === 'exec') {
        action.exec = draft.executable.trim();
        if (draft.args.trim()) {
          const args = JSON.parse(draft.args) as unknown;
          if (!Array.isArray(args) || args.some((arg) => typeof arg !== 'string')) throw new Error('Arguments must be a JSON array of strings.');
          action.args = args;
        }
      } else {
        if (draft.bash.trim()) action.bash = draft.bash;
        if (draft.powershell.trim()) action.powershell = draft.powershell;
        if (draft.command.trim()) action.command = draft.command;
      }
      if (draft.cwd.trim()) action.cwd = draft.cwd;
      if (draft.env.trim()) action.env = jsonObject(draft.env, 'Environment', true);
    }
    if (draft.timeout) action.timeoutSec = Number(draft.timeout);
    if (HOOK_MATCHERS[draft.event] && draft.matcher.trim()) {
      try { new RegExp(draft.matcher); } catch { throw new Error('Matcher must be a valid regular expression.'); }
      action.matcher = draft.matcher;
    }
    return JSON.stringify({ version: 1, ...(draft.disableHooks ? { disableAllHooks: true } : {}), hooks: { [draft.event]: [action] } }, null, 2) + '\n';
  }
  const fields: Record<string, unknown> = { name: draft.name.trim(), description: draft.description.trim() };
  if (kind === 'agents' && authoredModels(draft).length) {
    fields.model = draft.modelOrder.length ? draft.modelOrder : draft.model.trim();
    if (draft.modelPolicy) fields['model-policy'] = draft.modelPolicy;
    if (draft.reasoningEffort) fields['reasoning-effort'] = draft.reasoningEffort;
  }
  if (draft.tools.trim()) fields[kind === 'agents' ? 'tools' : 'allowed-tools'] = draft.tools.split('\n').map((s) => s.trim()).filter(Boolean);
  if (draft.metadata.trim()) fields.metadata = jsonObject(draft.metadata, 'Metadata', true);
  if (!draft.automatic) fields['disable-model-invocation'] = true;
  if (!draft.invocable) fields['user-invocable'] = false;
  if (kind === 'agents') {
    if (draft.noTools) fields.tools = [];
    if (draft.target) fields.target = draft.target;
    if (draft.mcp.trim()) fields['mcp-servers'] = jsonObject(draft.mcp, 'MCP servers');
    if (draft.includeInstructions) fields['include-custom-instructions'] = true;
  } else {
    if (draft.argumentHint.trim()) fields['argument-hint'] = draft.argumentHint;
    if (draft.license.trim()) fields.license = draft.license;
    if (draft.compatibility.trim()) fields.compatibility = draft.compatibility;
  }
  return `---\n${Object.entries(fields).map(([key, value]) => `${key}: ${JSON.stringify(value)}`).join('\n')}\n---\n\n${draft.prompt}\n`;
}

function ConfigurationForm({ kind, draft, busy, locked, error, models, modelStatus, invalidModel, invalidEffort, onChange, onSave, onCancel }: Readonly<{ kind: ConfigurationKind; draft: Draft; busy: boolean; locked: boolean; error: string | null; models: Model[]; modelStatus: string; invalidModel: boolean; invalidEffort: boolean; onChange: (value: Draft) => void; onSave: (event: SubmitEvent) => void; onCancel: () => void }>) {
  const prefix = `configuration-${kind}`;
  const label = kind === 'instructions' ? fileName(draft) : LABELS[kind];
  const editing = !!draft.revision;
  const selectedModel = draft.modelOrder[0] ?? draft.model;
  const hasModel = authoredModels(draft).length > 0;
  const supportedEfforts = agentEfforts(draft, models);
  // Keep catalog entries mounted when selection changes; hide unsupported efforts from the menu.
  const effortOptions = [...new Set([...models.flatMap((model) => model.efforts ?? []), ...(draft.reasoningEffort ? [draft.reasoningEffort] : [])])];
  const change = (patch: Partial<Draft>) => onChange({ ...draft, ...patch });
  const [previewError, setPreviewError] = useState<string | null>(null);
  const nativeEditor = <Field id={`${prefix}-document`} label={kind === 'instructions' ? `${label} (Markdown)` : 'Full native document'} hint={kind === 'instructions' ? 'Written to this file in the selected scope. These instructions are shared with Copilot CLI.' : 'All native fields are preserved. Edit Markdown with YAML frontmatter, or the complete JSON hook file.'}>
    <TextArea id={`${prefix}-document`} rows={16} disabled={busy || locked} value={draft.raw ?? ''} onChange={(e) => change({ raw: e.target.value })} />
  </Field>;
  const formRef = useInlineForm<HTMLFormElement>(() => { if (!busy) onCancel(); });
  return <form ref={formRef} aria-label={`${editing ? 'Edit' : 'Add'} ${label}`} className="flex min-w-0 flex-col gap-3 rounded-md bg-tint-well p-3" onSubmit={onSave}>
    {kind !== 'instructions' && <Field id={`${prefix}-name`} label="Name" hint="Lowercase letters, numbers and hyphens. The service chooses the file path.">
      <Input id={`${prefix}-name`} required pattern={kind === 'skills' ? '[a-z0-9]+(-[a-z0-9]+)*' : '[a-z0-9][a-z0-9-]*'} maxLength={kind === 'skills' ? 64 : undefined} value={draft.name} disabled={busy || locked || editing} onChange={(e) => change({ name: e.target.value })} />
    </Field>}
    {draft.path && <Note className="break-all">{draft.path}{kind === 'skills' && <span className="mt-1 block">Saving changes this resolved file and any aliases that link to it.</span>}</Note>}
    {draft.raw !== null ? kind === 'instructions' ? nativeEditor : <details open className="group"><Summary>Advanced settings</Summary><div className="mt-3">{nativeEditor}</div></details> : <>
      {kind === 'hooks' ? <>
        <Field id={`${prefix}-event`} label="Event"><Select id={`${prefix}-event`} value={draft.event} disabled={busy || locked} items={HOOK_EVENTS.map((value) => ({ value, label: value }))} onValueChange={(event) => change({ event })} /></Field>
        {draft.hookType === 'http' ? <Field id={`${prefix}-url`} label="Webhook URL" hint="Receives a POST with the hook event. HTTPS is required for permission events or header environment variables."><Input id={`${prefix}-url`} type="url" required pattern={draft.allowedEnv.trim() || ['preToolUse', 'permissionRequest'].includes(draft.event) ? 'https://.+' : 'https?://.+'} value={draft.url} disabled={busy || locked} onChange={(e) => change({ url: e.target.value })} /></Field> : draft.execution === 'exec' ?
          <Field id={`${prefix}-exec`} label="Executable" hint="Runs directly without shell expansion."><Input id={`${prefix}-exec`} required value={draft.executable} disabled={busy || locked} onChange={(e) => change({ executable: e.target.value })} /></Field> :
          <Field id={`${prefix}-bash`} label="Bash command" hint="Runs on Linux and macOS as the service user."><TextArea id={`${prefix}-bash`} rows={3} disabled={busy || locked} value={draft.bash} required={!draft.powershell.trim() && !draft.command.trim()} onChange={(e) => change({ bash: e.target.value })} /></Field>}
      </> : <>
        <Field id={`${prefix}-description`} label="Description" hint={kind === 'skills' ? 'Describe when the agent should use this skill.' : 'Describe the work this agent should handle.'}><Input id={`${prefix}-description`} required maxLength={kind === 'skills' ? 1024 : undefined} value={draft.description} disabled={busy || locked} onChange={(e) => change({ description: e.target.value })} /></Field>
        {kind === 'agents' && <Field id={`${prefix}-model`} label="Model (optional)" hintVisible={!!modelStatus || draft.modelOrder.length > 0} hint={draft.modelOrder.length ? 'The priority list in Advanced settings replaces this single-model choice.' : modelStatus || 'Choose an available Copilot model, or inherit the task model.'}>
          <Select id={`${prefix}-model`} value={selectedModel} disabled={busy || locked || draft.modelOrder.length > 0} items={[
            { value: '', label: 'Inherit task model' },
            ...(selectedModel && !models.some((model) => model.id === selectedModel) ? [{ value: selectedModel, label: `${selectedModel} (unavailable)`, hidden: true }] : []),
            ...models.map((model) => ({ value: model.id, label: model.name })),
          ]} onValueChange={(model) => change({ model })} />
          {invalidModel && <Note tone="error" role="alert">A selected model is no longer available. Choose available models or inherit the task model before adding this agent.</Note>}
        </Field>}
        <Field id={`${prefix}-prompt`} label={kind === 'agents' ? 'Agent instructions' : 'Skill instructions'}><TextArea id={`${prefix}-prompt`} rows={10} required value={draft.prompt} disabled={busy || locked} onChange={(e) => change({ prompt: e.target.value })} /></Field>
      </>}
      <details className="group"><Summary>Advanced settings</Summary><div className="mt-3 flex flex-col gap-3">
        {kind === 'hooks' ? <>
          <Field id={`${prefix}-type`} label="Hook type"><Select id={`${prefix}-type`} value={draft.hookType} disabled={busy || locked} items={[{ value: 'command', label: 'Command' }, { value: 'http', label: 'HTTP request' }]} onValueChange={(hookType) => change({ hookType })} /></Field>
          {draft.hookType === 'command' ? <>
            <Field id={`${prefix}-execution`} label="Command mode"><Select id={`${prefix}-execution`} value={draft.execution} disabled={busy || locked} items={[{ value: 'shell', label: 'Shell commands' }, { value: 'exec', label: 'Direct executable' }]} onValueChange={(execution) => change({ execution })} /></Field>
            {draft.execution === 'shell' ? <>
              <Field id={`${prefix}-powershell`} label="PowerShell command" hint="Runs on Windows."><TextArea id={`${prefix}-powershell`} rows={3} disabled={busy || locked} value={draft.powershell} onChange={(e) => change({ powershell: e.target.value })} /></Field>
              <Field id={`${prefix}-command`} label="Fallback command" hint="Used on either platform when its Bash or PowerShell command is absent."><TextArea id={`${prefix}-command`} rows={3} disabled={busy || locked} value={draft.command} onChange={(e) => change({ command: e.target.value })} /></Field>
            </> : <Field id={`${prefix}-args`} label="Executable arguments (optional JSON)" hint="A JSON array of strings, passed without shell interpretation."><TextArea id={`${prefix}-args`} rows={3} value={draft.args} placeholder={'["--check", "file name"]'} disabled={busy || locked} onChange={(e) => change({ args: e.target.value })} /></Field>}
            <Field id={`${prefix}-cwd`} label="Working folder (optional)"><Input id={`${prefix}-cwd`} value={draft.cwd} disabled={busy || locked} onChange={(e) => change({ cwd: e.target.value })} /></Field>
            <Field id={`${prefix}-env`} label="Environment variables (optional JSON)" hint="Values are stored in the native hook file and visible to anyone signed in. Use variable references instead of credentials."><TextArea id={`${prefix}-env`} rows={3} placeholder={'{"LOG_LEVEL": "info"}'} value={draft.env} disabled={busy || locked} onChange={(e) => change({ env: e.target.value })} /></Field>
          </> : <>
            <Field id={`${prefix}-headers`} label="HTTP headers (optional JSON)" hint="Use environment variable references for credentials."><TextArea id={`${prefix}-headers`} rows={3} value={draft.headers} disabled={busy || locked} onChange={(e) => change({ headers: e.target.value })} /></Field>
            <Field id={`${prefix}-allowed-env`} label="Header environment variables (one per line)"><TextArea id={`${prefix}-allowed-env`} rows={3} value={draft.allowedEnv} disabled={busy || locked} onChange={(e) => change({ allowedEnv: e.target.value })} /></Field>
            <Note>Copilot accepts HTTPS by default. Localhost HTTP also needs COPILOT_HOOK_ALLOW_LOCALHOST=1 on the service.</Note>
          </>}
          <Note>Only the selected hook type and command mode are saved. Switching modes keeps your other inputs for switching back.</Note>
          <Field id={`${prefix}-timeout`} label="Timeout (seconds)" hint="Leave empty for Copilot's 30-second default."><Input id={`${prefix}-timeout`} type="number" min="1" placeholder="30" value={draft.timeout} disabled={busy || locked} onChange={(e) => change({ timeout: e.target.value })} /></Field>
          {HOOK_MATCHERS[draft.event] && <Field id={`${prefix}-matcher`} label="Event matcher (optional regex)" hint={`Matches the complete ${HOOK_MATCHERS[draft.event]} value. Leave empty for all events.`}><Input id={`${prefix}-matcher`} value={draft.matcher} disabled={busy || locked} onChange={(e) => change({ matcher: e.target.value })} /></Field>}
          <label className="flex items-center gap-2 text-ui"><input type="checkbox" checked={draft.disableHooks} disabled={busy || locked} onChange={(e) => change({ disableHooks: e.target.checked })} />Disable hooks in this file</label>
          <Note>Prompt hooks require a new interactive Copilot CLI session. They can be kept in the full document, but are not guaranteed to run in UAM.</Note>
        </> : <>
          <Field id={`${prefix}-tools`} label={kind === 'agents' ? 'Tools (optional, one per line)' : 'Allowed tools (optional, one per line)'} hint={kind === 'agents' ? 'Leave empty for the provider defaults. Use native tool names.' : 'Tools preapproved while this skill is active. Saved as a YAML array.'}><TextArea id={`${prefix}-tools`} rows={3} value={draft.tools} disabled={busy || locked || (kind === 'agents' && draft.noTools)} onChange={(e) => change({ tools: e.target.value })} /></Field>
          {kind === 'agents' ? <>
            <Field id={`${prefix}-model-order`} label="Model priority list (optional)" hint="Replaces the single-model choice. Copilot tries these available models in order."><Select id={`${prefix}-model-order`} value="" disabled={busy || locked || !models.some((model) => !draft.modelOrder.includes(model.id))} items={[{ value: '', label: 'Add an available model' }, ...models.filter((model) => !draft.modelOrder.includes(model.id)).map((model) => ({ value: model.id, label: model.name }))]} onValueChange={(model) => { if (model) change({ modelOrder: [...draft.modelOrder, model] }); }} /></Field>
            {draft.modelOrder.map((model, index) => <div key={model} className="flex min-w-0 flex-wrap items-center gap-2">
              <span className="min-w-0 flex-1 break-words text-ui">{index + 1}. {models.find((entry) => entry.id === model)?.name ?? `${model} (unavailable)`}</span>
              <Button size="sm" aria-label={`Move model ${index + 1} earlier`} disabled={busy || locked || index === 0} onClick={() => { const order = [...draft.modelOrder]; [order[index - 1], order[index]] = [order[index], order[index - 1]]; change({ modelOrder: order }); }}>Move up</Button>
              <Button size="sm" aria-label={`Remove model ${index + 1} from priority list`} disabled={busy || locked} onClick={() => change({ modelOrder: draft.modelOrder.filter((_, at) => at !== index) })}>Remove</Button>
            </div>)}
            <Field id={`${prefix}-model-policy`} label="Model policy" hintVisible={!hasModel} hint={hasModel ? 'Required refuses dispatch when the configured models cannot be used.' : 'Choose a model to set a policy. No policy override is saved while inheriting.'}><Select id={`${prefix}-model-policy`} value={hasModel ? draft.modelPolicy : ''} disabled={busy || locked || !hasModel} items={[{ value: '', label: 'Preferred (default)' }, { value: 'required', label: 'Required' }]} onValueChange={(modelPolicy) => change({ modelPolicy })} /></Field>
            <Field id={`${prefix}-effort`} label="Reasoning effort" hintVisible={!hasModel} hint={hasModel ? 'Only effort levels supported by every selected model are offered.' : 'Choose a model to override effort. Otherwise the task effort is inherited.'}><Select id={`${prefix}-effort`} value={hasModel ? draft.reasoningEffort : ''} disabled={busy || locked || !hasModel} items={[{ value: '', label: 'Inherit task effort' }, ...effortOptions.map((effort) => ({ value: effort, label: supportedEfforts.includes(effort) ? effort : `${effort} (unavailable)`, hidden: !supportedEfforts.includes(effort) }))]} onValueChange={(reasoningEffort) => change({ reasoningEffort })} /></Field>
            <label className="flex items-center gap-2 text-ui"><input type="checkbox" checked={draft.includeInstructions} disabled={busy || locked} onChange={(e) => change({ includeInstructions: e.target.checked })} />Include repository instructions when used as a subagent</label>
            <Field id={`${prefix}-target`} label="Target (other environments)" hint="Used by other Copilot environments; the current CLI ignores this field."><Select id={`${prefix}-target`} value={draft.target} disabled={busy || locked} items={[{ value: '', label: 'All environments' }, { value: 'github-copilot', label: 'GitHub Copilot' }, { value: 'vscode', label: 'VS Code' }]} onValueChange={(target) => change({ target })} /></Field>
            <label className="flex items-center gap-2 text-ui"><input type="checkbox" checked={draft.noTools} disabled={busy || locked} onChange={(e) => change({ noTools: e.target.checked })} />Disable all tools</label>
            <Field id={`${prefix}-mcp`} label="Agent MCP servers (optional JSON)" hint="Native mcp-servers object. Shared servers can be configured in MCP servers settings."><TextArea id={`${prefix}-mcp`} rows={4} value={draft.mcp} disabled={busy || locked} onChange={(e) => change({ mcp: e.target.value })} /></Field>
          </> : <>
            <Field id={`${prefix}-argument-hint`} label="Argument hint (optional)" hint="Shown in the skill picker, for example [target] [mode]."><Input id={`${prefix}-argument-hint`} value={draft.argumentHint} disabled={busy || locked} onChange={(e) => change({ argumentHint: e.target.value })} /></Field>
            <Field id={`${prefix}-license`} label="License (optional)"><Input id={`${prefix}-license`} value={draft.license} disabled={busy || locked} onChange={(e) => change({ license: e.target.value })} /></Field>
            <Field id={`${prefix}-compatibility`} label="Compatibility (optional)" hint="Required tools, runtimes or environment."><Input id={`${prefix}-compatibility`} maxLength={500} value={draft.compatibility} disabled={busy || locked} onChange={(e) => change({ compatibility: e.target.value })} /></Field>
          </>}
          <label className="flex items-center gap-2 text-ui"><input type="checkbox" checked={draft.automatic} disabled={busy || locked} onChange={(e) => change({ automatic: e.target.checked })} />{kind === 'agents' ? 'Allow automatic agent selection' : 'Allow automatic skill invocation'}</label>
          <label className="flex items-center gap-2 text-ui"><input type="checkbox" checked={draft.invocable} disabled={busy || locked} onChange={(e) => change({ invocable: e.target.checked })} />{kind === 'agents' ? 'Allow manual agent selection' : 'Allow manual skill invocation'}</label>
          {!draft.automatic && !draft.invocable && <Note tone="warn">Both automatic and manual invocation are disabled.{kind === 'agents' ? ' The agent can still be invoked programmatically.' : ' This skill will not be available through either method.'}</Note>}
          <Field id={`${prefix}-metadata`} label="Metadata (optional JSON)" hint={kind === 'agents' ? 'Optional metadata for other environments; the current Copilot CLI ignores it.' : 'An object of string keys and values.'}><TextArea id={`${prefix}-metadata`} rows={3} value={draft.metadata} disabled={busy || locked} onChange={(e) => change({ metadata: e.target.value })} /></Field>
        </>}
        <Button className="self-start" size="sm" disabled={busy || locked || invalidModel || invalidEffort} onClick={() => {
          try { change({ raw: documentOf(kind, draft) }); setPreviewError(null); }
          catch (e) { setPreviewError(describeError(e)); }
        }}>Edit full document for advanced options</Button>
      </div></details>
    </>}
    {(error || previewError) && <Note tone="error" role="alert">{error || previewError}</Note>}
    {invalidEffort && <Note tone="error" role="alert">The selected reasoning effort is not supported by every selected model. Choose another effort in Advanced settings.</Note>}
    <div className="flex flex-wrap gap-2">
      <Button variant="primary" type="submit" disabled={locked || invalidModel || invalidEffort} loading={busy}>{editing || kind === 'instructions' ? 'Save' : 'Add'} {label}</Button>
      <Button disabled={busy} onClick={onCancel}>Cancel</Button>
    </div>
  </form>;
}

function SkillInstaller({ projectId, allowed, locked, onInstalled, onBusy }: Readonly<{ projectId: string; allowed: boolean; locked: boolean; onInstalled: () => void; onBusy: (busy: boolean) => void }>) {
  const [source, setSource] = useState('');
  const [names, setNames] = useState('');
  const [busy, setBusy] = useState(false);
  const [output, setOutput] = useState('');
  const [error, setError] = useState<string | null>(null);
  async function run(install: boolean) {
    if (busy || locked || !allowed) return;
    setBusy(true); onBusy(true); setError(null); setOutput('');
    try {
      const result = install ? await api.installSkills(source.trim(), names.split('\n').map((name) => name.trim()).filter(Boolean), projectId) : await api.listSkills(source.trim(), projectId);
      setOutput(result.output || (install ? 'Skills installed.' : 'No skills listed.'));
      if (install) onInstalled();
    } catch (e) { setError(describeError(e)); }
    finally { setBusy(false); onBusy(false); }
  }
  return <details className="group rounded-md bg-tint-well p-3">
    <Summary>Install with npx skills</Summary>
    <div className="mt-3 flex flex-col gap-3">
      <Note>Browse a repository, then install named skills into this scope. Existing skill names are kept. The server needs Node, npx and git.</Note>
      {!allowed && <Note>Turn on Terminal in General to allow npx to run on the server.</Note>}
      <Field id="skills-source" label="Repository" hint="GitHub owner/repo or an HTTPS repository URL."><Input id="skills-source" disabled={busy || locked || !allowed} value={source} placeholder="vercel-labs/agent-skills" onChange={(e) => setSource(e.target.value)} /></Field>
      <Button className="self-start" variant="secondary" disabled={busy || locked || !allowed || !source.trim()} onClick={() => void run(false)}>List skills</Button>
      <Field id="skills-names" label="Skill names to install, one per line"><TextArea id="skills-names" rows={3} disabled={busy || locked || !allowed} value={names} onChange={(e) => setNames(e.target.value)} /></Field>
      <Button className="self-start" variant="primary" disabled={locked || !allowed || !source.trim() || !names.trim()} loading={busy} onClick={() => void run(true)}>Install skills</Button>
      {busy && <Note role="status">Running npx skills on the server…</Note>}
      {error && <Note tone="error" role="alert">{error}</Note>}
      {/* eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- A labelled scroll region must accept keyboard scrolling. */}
      {output && <pre role="region" aria-label="Skill installer output" aria-live="polite" tabIndex={0} className="max-h-64 overflow-auto whitespace-pre-wrap break-words text-code-sm">{output}</pre>}
    </div>
  </details>;
}

export function ConfigurationSettings({ kind, projects, terminal }: Readonly<{ kind: ConfigurationKind; projects: Project[]; terminal: boolean }>) {
  const { meta, metaError, loaded, settings, refreshMeta } = useApp();
  const [projectId, setProjectId] = useState('');
  const [reload, setReload] = useState(0);
  const [data, setData] = useState<Configuration | null>(null);
  const [globalData, setGlobalData] = useState<Configuration | null>(null);
  const [globalError, setGlobalError] = useState<string | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [draft, setDraft] = useState<Draft | null>(null);
  const [viewed, setViewed] = useState<{ file: ConfigurationFile; kind: ConfigurationKind } | null>(null);
  const [busy, setBusy] = useState(false);
  const [installing, setInstalling] = useState(false);
  const [brief, setBrief] = useState('');
  const [generating, setGenerating] = useState(false);
  const [generationError, setGenerationError] = useState<string | null>(null);
  const [generatedBy, setGeneratedBy] = useState('');
  const [similarSkills, setSimilarSkills] = useState<NonNullable<ConfigurationDraft['similar_skills']>>([]);
  const [openingSkill, setOpeningSkill] = useState<string | null>(null);
  const [skillViewError, setSkillViewError] = useState<string | null>(null);
  const skillViewRequest = useRef(0);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);
  const [conflict, setConflict] = useState(false);
  const [filter, setFilter] = useState('');
  const removal = useConfirm<{ file: ConfigurationFile; projectId: string }>();
  const activation = useConfirm<{ file: ConfigurationFile; projectId: string }>();
  const discard = useConfirm<boolean>();
  const label = LABELS[kind];
  const locked = kind !== 'instructions' && !terminal;
  const copilot = meta?.providers.find((provider) => provider.name === 'copilot');
  const unavailableCustomModels = new Set(settings.custom_models?.filter((model) => model.key_present === false).map((model) => `${model.name}/${model.model_id}`));
  const models = loaded && !metaError && copilot?.available ? visibleModels(copilot.models, settings.hidden_models?.copilot).filter((model) => model.id !== 'auto' && !unavailableCustomModels.has(model.id)) : [];
  const invalidModel = kind === 'agents' && draft?.raw === null && authoredModels(draft).some((id) => !models.some((model) => model.id === id));
  const invalidEffort = kind === 'agents' && draft?.raw === null && authoredModels(draft).length > 0 && !!draft.reasoningEffort && !agentEfforts(draft, models).includes(draft.reasoningEffort);
  let modelStatus = '';
  if (!loaded || (!meta && !metaError)) modelStatus = 'Loading available models. You can still inherit the task model.';
  else if (metaError) modelStatus = 'The model catalog could not be loaded. Retry below, or inherit the task model.';
  else if (!copilot?.available) modelStatus = 'Copilot is unavailable. You can still inherit the task model.';
  else if (models.length === 0) modelStatus = 'No visible Copilot models are available. You can still inherit the task model.';
  const utilityProviders = meta?.providers.filter((provider) => provider.available && provider.capabilities.host_tools) ?? [];
  const utilityProvider = utilityProviders.find((provider) => {
    const model = settings.title_model?.[provider.name] || provider.cheapest_model;
    return !!model && model !== UTILITY_NONE;
  });
  const utilityModel = utilityProvider && (settings.title_model?.[utilityProvider.name] || utilityProvider.cheapest_model);
  let generationUnavailable = '';
  if (!loaded || (!meta && !metaError)) generationUnavailable = 'Loading Utility AI settings…';
  else if (locked) generationUnavailable = 'Turn on Terminal in General to create a draft with AI.';
  else if (metaError) generationUnavailable = 'The model catalog could not be loaded. Retry before generating a draft.';
  else if (settings.utility_daily_limit === 0) generationUnavailable = 'Background AI is off. Set a daily limit in General to create a draft with AI.';
  else if (!utilityProviders.length) generationUnavailable = 'No available provider supports Utility AI. Check Providers.';
  else if (!utilityProvider || !utilityModel || unavailableCustomModels.has(utilityModel) || !utilityProvider.models.some((model) => model.id === utilityModel)) generationUnavailable = 'No available Utility model is selected. Choose one in Models.';
  const briefTooLong = new TextEncoder().encode(brief.trim()).length > 8192;
  useEffect(() => {
    let current = true;
    api.configuration(projectId).then((next) => {
      if (!current) return;
      setData(next); setLoadError(null); setGlobalData(null); setGlobalError(null);
      if (projectId && kind !== 'instructions' && next.conflicts?.some((conflict) => conflict.kind === kind && conflict.paths.some((path) => !next[kind].some((file) => file.path === path)))) {
        api.configuration().then((global) => { if (current) setGlobalData(global); }, (e: unknown) => { if (current) setGlobalError(describeError(e)); });
      }
    }, (e: unknown) => {
      if (current) setLoadError(routeMissing(e) ? 'This service does not support configuration management yet. Update the service and retry.' : describeError(e));
    });
    return () => { current = false; };
  }, [projectId, reload, kind]);
  function refresh() { setGlobalData(null); setGlobalError(null); setReload((value) => value + 1); }
  function clearSuggestions() { skillViewRequest.current++; setSimilarSkills([]); setOpeningSkill(null); setSkillViewError(null); }
  function viewFile(file: ConfigurationFile, fileKind: ConfigurationKind) { skillViewRequest.current++; setOpeningSkill(null); setSkillViewError(null); setViewed({ file, kind: fileKind }); }
  async function viewSimilarSkill(skill: NonNullable<ConfigurationDraft['similar_skills']>[number]) {
    if (openingSkill) return;
    const request = ++skillViewRequest.current;
    setOpeningSkill(skill.path); setSkillViewError(null);
    try {
      const file = data?.skills.find((entry) => entry.path === skill.path) ?? (await api.configuration(skill.project_id ?? '')).skills.find((entry) => entry.path === skill.path);
      if (!file) throw new Error(`${skill.name} is no longer available at the suggested path.`);
      if (request === skillViewRequest.current) setViewed({ file, kind: 'skills' });
    } catch (e) { if (request === skillViewRequest.current) setSkillViewError(`Could not open ${skill.name}: ${describeError(e)}`); }
    finally { if (request === skillViewRequest.current) setOpeningSkill(null); }
  }
  function edit(file?: ConfigurationFile) { clearSuggestions(); setError(null); setSuccess(null); setConflict(false); setGeneratedBy(''); setDraft(createDraft(file)); }
  async function generate(event: SubmitEvent) {
    event.preventDefault();
    if (kind === 'instructions' || draft || generating || busy || installing || generationUnavailable || !brief.trim() || briefTooLong) return;
    clearSuggestions(); setGenerating(true); setGenerationError(null); setError(null); setSuccess(null);
    try {
      const suggestion = await api.draftConfiguration(kind, brief.trim(), projectId);
      setDraft(suggestedDraft(kind, suggestion));
      setGeneratedBy(`${suggestion.provider} / ${suggestion.utility_model}`);
      setSimilarSkills(suggestion.similar_skills ?? []);
      setConflict(false);
    } catch (e) { setGenerationError(routeMissing(e) ? 'This service does not support AI configuration drafts yet. Update the service and retry.' : describeError(e)); }
    finally { setGenerating(false); }
  }
  async function reloadSaved() {
    if (!draft) return;
    setBusy(true);
    try {
      const next = await api.configuration(projectId);
      const files = kind === 'instructions' ? next.instruction_files ?? [next.instructions] : next[kind];
      const matches = files.filter((entry) => entry.editable && !entry.error && (draft.path ? entry.path === draft.path : entry.name === draft.name));
      setData(next); discard.close();
      if (matches.length !== 1) {
        setError(draft.path ? `The file at ${draft.path} is no longer available for editing. Your draft is kept; cancel to select another file.` : 'Could not select one saved definition with this name. Your draft is kept; cancel to choose the file to edit.');
        return;
      }
      setDraft(createDraft(matches[0])); setError(null); setConflict(false);
    } catch (e) { setError(describeError(e)); discard.close(); }
    finally { setBusy(false); }
  }
  async function save(event: SubmitEvent) {
    event.preventDefault();
    if (!draft || busy || locked || invalidModel || invalidEffort) return;
    setError(null); setSuccess(null);
    try {
      const content = documentOf(kind, draft);
      setBusy(true);
      const saved = await api.saveConfiguration(kind, draft.name.trim(), { content, revision: draft.revision, ...(draft.revision && draft.path ? { path: draft.path } : {}) }, projectId);
      setData((current) => {
        if (!current) return current;
        if (kind === 'instructions') return { ...current, instructions: current.instructions.path === saved.path ? saved : current.instructions, instruction_files: current.instruction_files?.map((file) => file.path === saved.path ? saved : file) };
        return { ...current, [kind]: current[kind].map((file) => file.path === saved.path ? saved : file) };
      });
      clearSuggestions(); setDraft(null); setSuccess(`${kind === 'instructions' ? fileName(draft) : draft.name} saved. New and reopened tasks use the updated file.`); refresh();
    } catch (e) { setError(describeError(e)); setConflict(isStatus(e, 409)); }
    finally { setBusy(false); }
  }
  async function remove() {
    const target = removal.target;
    if (!target) return;
    const { file, projectId: scope } = target;
    setBusy(true); setError(null); setSuccess(null);
    try {
      await api.deleteConfiguration(kind, file.name, file.revision, scope, file.path);
      removal.close(); setSuccess(`${file.name} removed.`); refresh();
    } catch (e) { setError(describeError(e)); removal.close(); }
    finally { setBusy(false); }
  }
  async function setDisabled() {
    const target = activation.target;
    if (!target) return;
    const { file, projectId: scope } = target;
    setBusy(true); setError(null); setSuccess(null);
    try {
      await api.setConfigurationDisabled(kind, file.name, { path: file.path, revision: file.revision, disabled: !file.disabled }, scope);
      activation.close(); setSuccess(`${file.name} ${file.disabled ? 'enabled' : 'disabled'}. New and reopened tasks use the updated configuration.`); refresh();
    } catch (e) { setError(describeError(e)); activation.close(); }
    finally { setBusy(false); }
  }
  const discoveredFiles = data ? kind === 'instructions' ? data.instruction_files ?? [data.instructions] : data[kind] : [];
  const files = kind === 'skills' ? discoveredFiles.filter((file) => !file.error) : discoveredFiles;
  const conflictDetails = data?.conflict_details?.filter((detail) => detail.kind === kind) ?? [];
  const unreadableSkills = kind === 'skills' ? discoveredFiles.filter((file) => file.error && !conflictDetails.some((detail) => detail.path === file.path)) : [];
  // Skills: the filter's matches, grouped by source in the order the service lists the sources (the scope's own first).
  const sources = [...new Set(files.map(skillSource))];
  const query = filter.trim().toLowerCase();
  const shown = kind === 'skills' ? files.filter((file) => !query || file.name.toLowerCase().includes(query) || file.path.toLowerCase().includes(query)).sort((a, b) => sources.indexOf(skillSource(a)) - sources.indexOf(skillSource(b))) : files;
  return <div className="flex min-w-0 flex-col gap-4">
    <Field id={`${kind}-scope`} label="Scope" hint={projectId ? 'Project files are shared with Copilot CLI in this project.' : 'Global files apply to Copilot tasks across projects on this server.'}>
      <Select id={`${kind}-scope`} className="max-w-xl" value={projectId} disabled={busy || installing || generating || !!draft} items={[{ value: '', label: 'Global (all projects)' }, ...projects.map((project) => ({ value: project.id, label: project.name }))]} onValueChange={(id) => { setProjectId(id); setData(null); setGlobalData(null); setGlobalError(null); setLoadError(null); setError(null); setSuccess(null); setGenerationError(null); }} />
    </Field>
    {kind === 'instructions' && !projectId && <Note>Copilot uses copilot-instructions.md for global instructions. AGENTS.md belongs to a project unless an extra instruction directory is explicitly configured with COPILOT_CUSTOM_INSTRUCTIONS_DIRS.</Note>}
    <div className="flex items-center gap-1 text-ui"><a className="text-accent underline" href={REFERENCES[kind]} target="_blank" rel="noreferrer">Native {kind} reference</a><HelpTip label={`${label} configuration`}>Advanced documents support the full native format, including fields not shown in the form. Available options depend on the installed Copilot CLI version. Copilot reads these files when a task opens or reopens. A running task keeps its current configuration.</HelpTip></div>
    {locked && <Note>Turn on Terminal in General to manage {kind}. These files can configure commands that run as the service user.</Note>}
    {loadError ? <Note tone="error" role="alert">{loadError} <Button size="sm" onClick={refresh}>Retry</Button></Note> : !data ? <Skeleton label={`Loading ${kind}…`} rows={3} /> : <>
      {!draft && kind !== 'instructions' && <>
        <SectionAction><Button size="sm" variant="secondary" data-section-add="" disabled={locked || busy || installing || generating} onClick={() => edit()}>Add {label}</Button></SectionAction>
        <details className="group rounded-md bg-tint-well p-3">
          <Summary>Create with AI</Summary>
          <form aria-label={`Create ${label} with AI`} className="mt-3 flex flex-col gap-3" aria-busy={generating || undefined} onSubmit={(event) => void generate(event)}>
            <Note>Describe what this {label} should do. The Utility model compares installed skills and creates an editable draft. Review it, then select Add {label} to save it.</Note>
            {generationUnavailable && <Note>{generationUnavailable}</Note>}
            {!generationUnavailable && <Note>Uses {utilityProvider?.display_name} / {utilityProvider?.models.find((model) => model.id === utilityModel)?.name}. Counts toward the Background AI daily limit.</Note>}
            <Field id={`${kind}-brief`} label={`Describe your ${label}`}><TextArea id={`${kind}-brief`} rows={4} maxLength={8192} required value={brief} disabled={generating || busy || installing || !!generationUnavailable} onChange={(event) => setBrief(event.target.value)} /></Field>
            {briefTooLong && <Note tone="error" role="alert">Shorten the description to 8 KB of text or less.</Note>}
            <Button className="self-start" variant="secondary" type="submit" loading={generating} disabled={busy || installing || !!generationUnavailable || !brief.trim() || briefTooLong}>Generate draft</Button>
            {generating && <Note role="status">Generating a draft… Nothing has been saved.</Note>}
            {generationError && <Note tone="error" role="alert">{generationError}</Note>}
          </form>
        </details>
      </>}
      {kind === 'skills' && !draft && <SkillInstaller key={projectId} projectId={projectId} allowed={terminal} locked={generating || busy} onBusy={setInstalling} onInstalled={refresh} />}
      {kind !== 'instructions' && metaError && <Button className="self-start" size="sm" disabled={generating} onClick={refreshMeta}>Retry model catalog</Button>}
      {draft && generatedBy && <Note role="status">Draft generated by {generatedBy}. Review every field{kind === 'hooks' ? ', especially commands and environment variables' : ''} before selecting Add {label}. Nothing has been saved or run.</Note>}
      {draft && similarSkills.length > 0 && <section aria-label="Similar installed skills" className="flex min-w-0 flex-col gap-2">
        <h3 className="text-ui font-medium">Similar installed skills</h3>
        <Note>The Utility model suggested these existing skills. View them before deciding whether to add the draft.</Note>
        <ul className="flex flex-col gap-3">
          {similarSkills.map((skill) => <li key={skill.path} className="flex min-w-0 flex-wrap items-start gap-2">
            <div className="min-w-0 flex-1">
              <p className="break-words text-ui font-medium">{skill.name}</p>
              <p className="break-words text-caption">{skill.reason}</p>
              <p className="break-all font-mono text-meta text-muted">{skill.path}</p>
            </div>
            <Button size="sm" aria-label={`View existing skill ${skill.name}`} loading={openingSkill === skill.path} disabled={!!openingSkill} onClick={() => void viewSimilarSkill(skill)}>View skill</Button>
          </li>)}
        </ul>
        {openingSkill && <Note role="status">Loading the existing skill…</Note>}
        {skillViewError && <Note tone="error" role="alert">{skillViewError}</Note>}
      </section>}
      {draft && <ConfigurationForm kind={kind} draft={draft} busy={busy} locked={locked} error={error} models={models} modelStatus={modelStatus} invalidModel={invalidModel} invalidEffort={invalidEffort} onChange={setDraft} onSave={(event) => void save(event)} onCancel={() => { clearSuggestions(); setDraft(null); setError(null); setGeneratedBy(''); }} />}
      {draft && conflict && <div className="flex flex-wrap items-center gap-2"><Note>Your draft is kept above. Copy any changes you need before reloading the saved version.</Note><Button disabled={busy} variant="secondary" onClick={() => discard.ask(true)}>Reload saved version</Button></div>}
      {!draft && error && <Note tone="error" role="alert">{error}</Note>}
      {success && <Note role="status">{success}</Note>}
      {conflictDetails.map((detail) => <FileError key={`${detail.path ?? ''}:${detail.message}`} tone="warn" role="status" message={detail.message} path={detail.path} />)}
      {unreadableSkills.map((file) => <FileError key={file.path} tone="warn" role="status" message={file.error?.startsWith('Skill ') ? file.error : `Skill unavailable: ${file.error}`} path={file.path} />)}
      {globalError && <Note tone="error" role="alert">Could not load Global definitions: {globalError} <Button size="sm" onClick={refresh}>Retry Global definitions</Button></Note>}
      {data.conflicts?.filter((conflict) => conflict.kind === kind).map((conflict) => <div key={conflict.name} className="min-w-0" role="status">
        <Note tone="warn">Multiple {conflict.kind === 'agents' ? 'agent files share the filename' : 'skill folders share the directory name'} {conflict.name}. Review each file, then disable or remove the unwanted definition.</Note>
        <ul className="mt-2 flex flex-col gap-3">
          {conflict.paths.map((path) => {
            const local = files.find((file) => file.path === path);
            const file = local ?? globalData?.[conflict.kind].find((file) => file.path === path);
            const target = file && { file, projectId: local ? projectId : '' };
            const unavailable = !file ? globalError ? 'Global definition unavailable. Retry above.' : globalData || !projectId ? 'This file is no longer in the listing. Reload before changing it.' : 'Loading Global definition…' : !file.editable || file.error || !file.revision ? file.read_only_reason || file.error || 'Read only here; manage this file at its source.' : '';
            return <li key={path} className="flex min-w-0 flex-wrap items-start gap-2">
              <div className="min-w-0 flex-1 max-sm:basis-full"><PathText path={path} className="text-muted" /><span className="text-meta text-muted">{local && projectId ? 'Project' : 'Global'}</span>{unavailable && <Note>{unavailable}</Note>}</div>
              <Button size="sm" disabled={!file} onClick={() => file && viewFile(file, kind)}>View</Button>
              <Button size="sm" disabled={!!unavailable || locked || busy || installing || generating || !!draft} onClick={() => target && activation.ask(target)}>Disable</Button>
              <Button size="sm" variant="danger" disabled={!!unavailable || locked || busy || installing || generating || !!draft} onClick={() => target && removal.ask(target)}>Remove</Button>
            </li>;
          })}
        </ul>
      </div>)}
      {kind === 'skills' && files.length > 0 && <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <Input type="search" size="md" aria-label="Filter skills" placeholder="Filter by name or path" className="w-64 max-w-full" value={filter} onChange={(event) => setFilter(event.target.value)} />
        <span aria-live="polite" className="text-caption text-muted tabular-nums">{shown.length === files.length ? `${files.length} ${files.length === 1 ? 'skill' : 'skills'}` : `${shown.length} of ${files.length} skills`}</span>
      </div>}
      {/* One grid for every row: the name column, then View, Edit, Disable/Enable and Remove in fixed columns (subgrid), so actions line up across rows and groups; stacked under the name on a phone. */}
      <ul aria-label={`${kind} in this scope`} className="flex flex-col gap-3 sm:grid sm:grid-cols-[minmax(0,1fr)_repeat(4,auto)] sm:gap-x-1">
        {shown.map((file, index) => {
          const source = kind === 'skills' ? skillSource(file) : '';
          const heading = source && source !== (index > 0 ? skillSource(shown[index - 1]) : '') ? `${source} · ${shown.filter((other) => skillSource(other) === source).length}` : '';
          return <li key={file.path} className="flex min-w-0 flex-col gap-1.5 sm:col-span-full sm:grid sm:grid-cols-subgrid sm:items-start sm:gap-x-1">
            {heading && <h3 className="pt-1 text-caption font-medium text-muted sm:col-span-full">{heading}</h3>}
            <div className="min-w-0">
              <p className="flex flex-wrap items-baseline gap-x-2 text-ui font-medium">
                {kind === 'instructions' ? fileName(file) : file.name}
                {kind === 'skills' && files.some((other) => other !== file && other.name === file.name) && <span className="text-meta font-normal text-muted">in {source}</span>}
              </p>
              {file.disabled && <Note>Disabled — enable to make this file available to new and reopened tasks.</Note>}
              <PathText path={file.path} className="text-muted" />
              {!file.editable && <Note>{file.read_only_reason || 'Discovered from another source. Read only here; manage this file at the path shown above.'}</Note>}
              {file.error && <FileError message={file.error} />}
              {kind === 'instructions' && !file.revision && <Note>No saved file at this path.</Note>}
            </div>
            <div className="flex flex-wrap gap-1 sm:contents">
              <Button size="sm" className="sm:col-start-2" aria-label={`View ${kind === 'instructions' ? fileName(file) : `${label} ${file.name}`}`} onClick={() => viewFile(file, kind)}>View</Button>
              {file.editable && !file.disabled && <Button size="sm" className="sm:col-start-3" disabled={locked || busy || installing || generating || !!draft} onClick={() => edit(file)}>{kind === 'instructions' ? `${file.revision ? 'Edit' : 'Add'} ${fileName(file)}` : 'Edit'}</Button>}
              {file.editable && file.revision && kind !== 'instructions' && <>
                <Button size="sm" className="sm:col-start-4" disabled={locked || busy || installing || generating || !!draft} onClick={() => activation.ask({ file, projectId })}>{file.disabled ? 'Enable' : 'Disable'}</Button>
                <Button size="sm" variant="danger" className="sm:col-start-5" disabled={locked || busy || installing || generating || !!draft} onClick={() => removal.ask({ file, projectId })}>Remove</Button>
              </>}
            </div>
          </li>;
        })}
      </ul>
      {files.length === 0 && <Note>No {kind} in this scope. Add one to get started.</Note>}
      {files.length > 0 && shown.length === 0 && <Note>No skills match “{filter.trim()}”.</Note>}
    </>}
    {viewed && <ConfigurationViewer file={viewed.file} kind={viewed.kind} draftOpen={!!draft} onClose={() => setViewed(null)} />}
    <AlertDialog {...removal.props} title={`Remove ${removal.target?.file.name ?? label}?`} description={<>{kind === 'skills' ? 'Removes the skill definition while keeping supporting files.' : `Deletes this ${label} file.`}<span className="mt-2 block">Scope: {removal.target?.projectId ? projects.find((project) => project.id === removal.target?.projectId)?.name ?? 'Project' : 'Global (all projects)'}</span><span className="mb-2 block break-all font-mono text-meta">{removal.target?.file.path}</span>If other paths link to this resolved file, removing it affects those aliases too. This cannot be undone here. Running tasks keep their current configuration.</>} confirmLabel="Remove" busy={busy} onConfirm={() => void remove()} />
    <AlertDialog {...activation.props} title={`${activation.target?.file.disabled ? 'Enable' : 'Disable'} ${activation.target?.file.name ?? label}?`} description={<>{activation.target?.file.disabled ? 'Restores the saved file to its discovery name. Any same-name definitions will be checked again.' : 'Keeps the file with a disabled filename so Copilot does not load it. You can enable it again here.'}<span className="mt-2 block">Scope: {activation.target?.projectId ? projects.find((project) => project.id === activation.target?.projectId)?.name ?? 'Project' : 'Global (all projects)'}</span><span className="mb-2 block break-all font-mono text-meta">{activation.target?.file.path}</span>This also affects aliases of the resolved file. Running tasks keep their current configuration.</>} confirmLabel={activation.target?.file.disabled ? 'Enable' : 'Disable'} busy={busy} onConfirm={() => void setDisabled()} />
    <AlertDialog {...discard.props} title="Reload saved version?" description="Discards your unsaved draft and loads the file currently saved on the server. Copy any changes you want to keep before continuing." confirmLabel="Discard draft and reload" busy={busy} onConfirm={() => void reloadSaved()} />
  </div>;
}
