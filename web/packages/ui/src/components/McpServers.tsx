import { useApi } from '../ApiContext';
import { Plus, X } from 'lucide-react';
import { useEffect, useState, type SubmitEvent } from 'react';
import { describeError, type McpSecret, type McpServer, type McpServerInput, type McpServers, type McpType } from '../api';
import { Note, Skeleton } from './common';
import { Field, SectionAction, useInlineForm } from './TaskDefaults';
import { AlertDialog, useConfirm } from './ui/dialog';
import { Button } from './ui/button';
import { Chip } from './ui/chip';
import { Input } from './ui/input';
import { Segmented } from './ui/segmented';
import { Switch } from './ui/switch';
import { Tip } from './ui/tooltip';

/** An env variable or header row of the form: `stored` marks one whose value the service keeps, left as is while `value` is empty. */
interface SecretRow {
  key: string;
  value: string;
  stored: boolean;
}

interface Draft {
  /** The saved name of the server being edited; none for a new one. */
  original?: string;
  name: string;
  type: McpType;
  command: string;
  /** One argument per line, so no shell parsing is involved. */
  args: string;
  cwd: string;
  url: string;
  env: SecretRow[];
  headers: SecretRow[];
}

export const TYPE_LABEL: Record<string, string> = { stdio: 'Command', http: 'HTTP', sse: 'SSE' };
const SOURCE_LABEL: Record<string, string> = { plugin: 'From a plugin', builtin: 'Built in', managed: 'Managed' };
export const STDIO_OFF = 'A server that runs a command runs it on this machine for anyone signed in, so adding or editing one needs Settings → Shell access → Terminal on.';
/** Copilot's built-in GitHub MCP server: UAM's own setting (`Settings.github_mcp`) turns it on or off for tasks. */
const GITHUB_MCP = 'github-mcp-server';
const GITHUB_MCP_HELP = "UAM's own switch, not shared with the copilot command. Off by default: starting it takes about a second each time a task opens, and a message sent meanwhile waits. A change reaches open tasks too.";

/** Settings → GitHub MCP server as saved now, and how to change it. */
export interface GithubMcpSetting {
  on: boolean;
  saving: boolean;
  onChange: (on: boolean) => void;
}

const stored = (s: McpSecret[]): SecretRow[] => s.map((e) => ({ key: e.key, value: '', stored: e.set }));

function draftOf(s?: McpServer): Draft {
  if (!s) return { name: '', type: 'http', command: '', args: '', cwd: '', url: '', env: [], headers: [] };
  return {
    original: s.name,
    name: s.name,
    type: (s.type || 'http') as McpType,
    command: s.command ?? '',
    args: (s.args ?? []).join('\n'),
    cwd: s.cwd ?? '',
    url: s.url ?? '',
    env: stored(s.env),
    headers: stored(s.headers),
  };
}

/** The body to save: secret rows without a typed value keep the stored one (no `value`). */
export function inputOf(d: Draft): McpServerInput {
  const rows = (r: SecretRow[]) => r.filter((e) => e.key.trim()).map((e) => (e.stored && !e.value ? { key: e.key.trim() } : { key: e.key.trim(), value: e.value }));
  if (d.type === 'stdio') {
    return { name: d.name.trim(), type: 'stdio', command: d.command.trim(), args: d.args.split('\n').map((a) => a.trim()).filter(Boolean), cwd: d.cwd.trim() || undefined, env: rows(d.env) };
  }
  return { name: d.name.trim(), type: d.type, url: d.url.trim(), headers: rows(d.headers) };
}

/** The env variables or headers of the form: a name and a write-only value per row; a stored value shows as dots until replaced. */
function SecretRows({ label, rows, keyHint, disabled, onChange }: Readonly<{ label: string; rows: SecretRow[]; keyHint: string; disabled: boolean; onChange: (rows: SecretRow[]) => void }>) {
  const set = (i: number, patch: Partial<SecretRow>) => onChange(rows.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  return (
    <fieldset className="flex min-w-0 flex-col gap-2">
      <legend className="mb-1 text-caption text-muted">{label}</legend>
      {rows.some((r) => r.stored) && <Note>Stored values are never shown. Leave one empty to keep it, or type a new one.</Note>}
      {rows.map((r, i) => (
        <div key={i} className="flex min-w-0 items-center gap-2">
          <Input aria-label={`${label} ${i + 1} name`} size="md" className="w-28 shrink-0 font-mono text-code-sm sm:w-48" spellCheck={false} autoComplete="off" placeholder={keyHint} disabled={disabled || r.stored} value={r.key} onChange={(e) => set(i, { key: e.target.value })} />
          <Input
            aria-label={`${label} ${i + 1} value`}
            size="md"
            type="password"
            autoComplete="new-password"
            spellCheck={false}
            placeholder={r.stored ? '•••• set' : 'Value'}
            title={r.stored ? 'A value is stored. Type a new one to replace it; leave it empty to keep it.' : undefined}
            disabled={disabled}
            value={r.value}
            onChange={(e) => set(i, { value: e.target.value })}
          />
          <Button size="icon-md" aria-label={`Remove ${r.key || `${label} ${i + 1}`}`} className="text-muted" disabled={disabled} onClick={() => onChange(rows.filter((_, j) => j !== i))}>
            <X />
          </Button>
        </div>
      ))}
      <Button size="sm" className="self-start" disabled={disabled} onClick={() => onChange([...rows, { key: '', value: '', stored: false }])}>
        <Plus />
        Add
      </Button>
    </fieldset>
  );
}

function ServerForm({ draft, stdioAllowed, busy, error, onChange, onSave, onCancel }: Readonly<{ draft: Draft; stdioAllowed: boolean; busy: boolean; error: string | null; onChange: (d: Draft) => void; onSave: (e: SubmitEvent) => void; onCancel: () => void }>) {
  const editing = !!draft.original;
  const formRef = useInlineForm<HTMLFormElement>(() => { if (!busy) onCancel(); });
  // An existing command server stays editable only while Terminal is on; the service refuses it too.
  const locked = editing && draft.type === 'stdio' && !stdioAllowed;
  const types = [
    { value: 'http', label: 'HTTP' },
    { value: 'sse', label: 'SSE' },
    ...(stdioAllowed || draft.type === 'stdio' ? [{ value: 'stdio', label: 'Command' }] : []),
  ];
  return (
    <form ref={formRef} aria-label={editing ? `Edit MCP server ${draft.original}` : 'Add an MCP server'} className="flex flex-col gap-3 rounded-md bg-tint-well p-3" onSubmit={onSave}>
      <div className="grid grid-cols-1 items-end gap-3 sm:grid-cols-[minmax(0,16rem)_minmax(0,22rem)]">
        <Field id="mcp-name" label="Name">
          <Input id="mcp-name" spellCheck={false} autoComplete="off" placeholder="docs" disabled={busy || editing} value={draft.name} onChange={(e) => onChange({ ...draft, name: e.target.value })} />
        </Field>
        <Segmented aria-label="Server type" disabled={busy || locked} value={draft.type} onValueChange={(type) => onChange({ ...draft, type: type as McpType })} items={types} />
      </div>
      {!stdioAllowed && <Note>{locked ? STDIO_OFF : `${STDIO_OFF} Remote servers (HTTP or SSE) need no Terminal.`}</Note>}
      {draft.type === 'stdio' ? (
        <>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <Field id="mcp-command" label="Command">
              <Input id="mcp-command" className="font-mono text-code-sm" spellCheck={false} autoComplete="off" placeholder="npx" disabled={busy || locked} value={draft.command} onChange={(e) => onChange({ ...draft, command: e.target.value })} />
            </Field>
            <Field id="mcp-cwd" label="Working folder (optional, absolute)">
              <Input id="mcp-cwd" className="font-mono text-code-sm" spellCheck={false} autoComplete="off" placeholder="/home/me/tools" disabled={busy || locked} value={draft.cwd} onChange={(e) => onChange({ ...draft, cwd: e.target.value })} />
            </Field>
          </div>
          <Field id="mcp-args" label="Arguments, one per line (no shell: quotes and spaces are kept as typed)">
            <textarea
              id="mcp-args"
              rows={3}
              spellCheck={false}
              className="w-full min-w-0 resize-y rounded-sm bg-sunken px-2.5 py-2 font-mono text-code-sm text-ink shadow-well placeholder:text-muted focus-visible:bg-raised focus-visible:shadow-focus focus-visible:outline-none disabled:opacity-45"
              placeholder={'-y\n@scope/server'}
              disabled={busy || locked}
              value={draft.args}
              onChange={(e) => onChange({ ...draft, args: e.target.value })}
            />
          </Field>
          <SecretRows label="Environment variables" keyHint="API_KEY" rows={draft.env} disabled={busy || locked} onChange={(env) => onChange({ ...draft, env })} />
        </>
      ) : (
        <>
          <Field id="mcp-url" label="Address" hint="Shown to anyone signed in: put keys in a header, not in the address.">
            <Input id="mcp-url" className="font-mono text-code-sm" type="url" spellCheck={false} autoComplete="off" placeholder="https://example.com/mcp" disabled={busy} value={draft.url} onChange={(e) => onChange({ ...draft, url: e.target.value })} />
          </Field>
          <SecretRows label="Headers" keyHint="Authorization" rows={draft.headers} disabled={busy} onChange={(headers) => onChange({ ...draft, headers })} />
        </>
      )}
      {error && <Note tone="error" role="alert">{error}</Note>}
      <div className="flex gap-2">
        <Button type="submit" variant="primary" size="lg" loading={busy} disabled={locked}>
          {editing ? 'Save server' : 'Add server'}
        </Button>
        <Button size="lg" disabled={busy} onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </form>
  );
}

/** What a configured server is: the command line or the address, then its env variable or header names with dots for the values. */
function ServerDetail({ s }: Readonly<{ s: McpServer }>) {
  const line = s.type === 'stdio' ? [s.command, ...(s.args ?? [])].join(' ') : s.url;
  const secrets = s.type === 'stdio' ? s.env : s.headers;
  return (
    <div className="flex min-w-0 flex-col gap-0.5 text-meta text-muted">
      {line && <code className="min-w-0 font-mono text-code-sm break-all text-body">{line}</code>}
      {s.cwd && <span className="min-w-0 break-all">in {s.cwd}</span>}
      {secrets.length > 0 && (
        <span className="flex min-w-0 flex-wrap gap-x-3">
          {secrets.map((e) => (
            <span key={e.key} className="font-mono text-code-sm">
              {e.key}=<span aria-label={e.set ? 'value set' : 'no value'}>{e.set ? '••••' : ''}</span>
            </span>
          ))}
        </span>
      )}
    </div>
  );
}

/**
 * Settings → MCP servers: the provider's user-wide MCP configuration (shared with its own
 * command line on this machine), edited through the provider's API. Env and header values
 * are write-only. Changes reach new Tasks; an open Task picks them up when it reconnects.
 * Adding or editing a server that runs a command needs Terminal on (the service checks too).
 * The built-in GitHub server's switch is UAM's own setting instead, offered when the instance has it.
 */
export function McpServersSettings({ terminal, githubMcp }: Readonly<{ /** Settings → Shell access → Terminal as saved now; the list's own copy is from its last read. */ terminal: boolean; githubMcp?: GithubMcpSetting }>) {
  const api = useApi();
  const [data, setData] = useState<McpServers | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [draft, setDraft] = useState<Draft | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const removal = useConfirm<string>();

  const [reads, setReads] = useState(0);
  useEffect(() => {
    let current = true;
    api.mcpServers().then((d) => { if (current) setData(d); }).catch((e: unknown) => { if (current) setLoadError(describeError(e)); });
    return () => { current = false; };
  }, [api, reads]);

  async function run(key: string, action: () => Promise<McpServers>) {
    setBusy(key);
    setError(null);
    try {
      setData(await action());
      return true;
    } catch (e) {
      setError(describeError(e));
      return false;
    } finally {
      setBusy(null);
    }
  }

  async function save(e: SubmitEvent, d: Draft) {
    e.preventDefault();
    const body = inputOf(d);
    if (await run('form', () => (d.original ? api.updateMcpServer(d.original, body) : api.addMcpServer(body)))) setDraft(null);
  }

  if (loadError) {
    return (
      <Note tone="error" role="alert" className="flex flex-wrap items-center gap-2">
        <span className="min-w-0 flex-1">Could not load the MCP servers: {loadError}</span>
        <Button size="sm" variant="secondary" onClick={() => { setLoadError(null); setReads(reads + 1); }}>
          Retry
        </Button>
      </Note>
    );
  }
  if (!data) return <Skeleton label="Loading MCP servers…" rows={2} />;
  if (!data.available) return <Note>No provider here manages MCP servers.</Note>;
  const pending = removal.target;
  return (
    <div className="flex flex-col gap-3">
      <Note className="max-w-3xl">
        Tools the agent can call, from a command this machine runs or a remote address. This is GitHub Copilot's own MCP configuration, shared with the copilot command here. New tasks start
        with it; an open task keeps the servers it started with until you reconnect it (task menu → MCP servers).
      </Note>
      {!draft && (
        <SectionAction>
          <Button size="sm" variant="secondary" data-section-add="" disabled={!!busy} onClick={() => { setError(null); setDraft(draftOf()); }}>
            Add server
          </Button>
        </SectionAction>
      )}
      {data.servers.length === 0 && !draft && <Note>No MCP servers yet.</Note>}
      {data.servers.length > 0 && (
        <ul aria-label="MCP servers" className="flex flex-col">
          {data.servers.map((s) => {
            const own = s.source === 'user';
            const lockedStdio = s.type === 'stdio' && !terminal;
            const github = s.source === 'builtin' && s.name === GITHUB_MCP ? githubMcp : undefined;
            const enabled = github ? github.on : s.enabled;
            return (
              <li key={s.name} className="flex min-h-12 items-start gap-3 py-2">
                <div className="flex min-w-0 flex-1 flex-col gap-1">
                  <div className="flex min-w-0 flex-wrap items-center gap-x-2">
                    <span className="min-w-0 break-all text-ui font-medium text-ink">{s.name}</span>
                    <Chip fill="outline">{TYPE_LABEL[s.type] ?? 'Other'}</Chip>
                    {!own && <Chip>{SOURCE_LABEL[s.source] ?? s.source}</Chip>}
                    {!enabled && <span className="text-meta text-muted">Off</span>}
                  </div>
                  <ServerDetail s={s} />
                  {github && <p id="github-mcp-help" className="text-meta text-muted">{GITHUB_MCP_HELP}</p>}
                </div>
                {own && (
                  <div className="flex shrink-0 items-center gap-1">
                    <Tip label={lockedStdio ? STDIO_OFF : `Edit ${s.name}`}>
                      <Button size="sm" aria-disabled={lockedStdio || undefined} disabled={!!busy || !!draft} onClick={() => { if (!lockedStdio) { setError(null); setDraft(draftOf(s)); } }}>
                        Edit
                      </Button>
                    </Tip>
                    <Button size="sm" variant="danger" disabled={!!busy} aria-label={`Remove ${s.name}`} onClick={() => removal.ask(s.name)}>
                      Remove
                    </Button>
                    <Switch aria-label={`Use ${s.name} in new tasks`} checked={s.enabled} disabled={!!busy} onCheckedChange={(on) => void run(s.name, () => api.enableMcpServer(s.name, on))} />
                  </div>
                )}
                {github && (
                  <div className="flex shrink-0 items-center gap-1">
                    <Switch aria-label={`Use ${s.name} in tasks`} aria-describedby="github-mcp-help" checked={github.on} disabled={github.saving} onCheckedChange={github.onChange} />
                  </div>
                )}
              </li>
            );
          })}
        </ul>
      )}
      {error && !draft && <Note tone="error" role="alert">{error}</Note>}
      {draft && <ServerForm draft={draft} stdioAllowed={terminal} busy={busy === 'form'} error={error} onChange={setDraft} onSave={(e) => void save(e, draft)} onCancel={() => { setDraft(null); setError(null); }} />}
      <AlertDialog
        {...removal.props}
        title={pending ? `Remove MCP server ${pending}?` : 'Remove?'}
        description="It leaves GitHub Copilot's MCP configuration, also for the copilot command on this machine. Open tasks keep it until they reconnect."
        confirmLabel="Remove server"
        busy={!!busy && busy === pending}
        onConfirm={() => {
          if (!pending) return;
          void run(pending, () => api.removeMcpServer(pending)).then(() => removal.close());
        }}
      />
    </div>
  );
}
