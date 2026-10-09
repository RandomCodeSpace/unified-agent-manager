import { useApi } from '../ApiContext';
import { ExternalLink } from 'lucide-react';
import { useEffect, useState, type ReactNode, type SubmitEvent } from 'react';
import { describeError, type McpSignIn, type McpServerStatus, type McpStatusSnapshot, type McpTaskStatus, type McpTool } from '../api';
import { Dot, Note, Skeleton, type Tone } from './common';
import { Collapse } from './ui/collapse';
import { Dialog } from './ui/dialog';
import { Button } from './ui/button';
import { Input } from './ui/input';
import { Switch } from './ui/switch';

const STATE: Record<string, { label: string; tone: Tone }> = {
  connected: { label: 'Connected', tone: 'success' },
  failed: { label: 'Failed', tone: 'error' },
  'needs-auth': { label: 'Needs sign-in', tone: 'warning' },
  pending: { label: 'Starting…', tone: 'muted' },
  disabled: { label: 'Off for this task', tone: 'faint' },
  stopped: { label: 'Stopped', tone: 'faint' },
  not_configured: { label: 'Not configured', tone: 'faint' },
};
const SOURCE: Record<string, string> = { plugin: 'from a plugin', builtin: 'built in', workspace: 'from the project', managed: 'managed' };

/** A sign-in in progress for one server: the page to open and whether the address the browser ends on can be pasted here. */
interface SigningIn extends McpSignIn {
  name: string;
  pasted: string;
  busy: boolean;
  error?: string;
  /** The service passed the pasted address on; the list shows the outcome. */
  sent?: boolean;
  /** The server's state when the sign-in started: a sign-in on the server's own desktop finishes without a paste. */
  from: string;
}

function SignInPanel({ s, onPaste, onFinish, onCancel }: Readonly<{ s: SigningIn; onPaste: (v: string) => void; onFinish: (e: SubmitEvent) => void; onCancel: () => void }>) {
  return (
    <form aria-label={`Sign in to ${s.name}`} className="mt-2 flex flex-col gap-2 rounded-md bg-tint-well p-3" onSubmit={onFinish}>
      <ol className="flex list-decimal flex-col gap-2 pl-5 text-ui text-body">
        <li>
          <a href={s.url} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1 font-medium text-accent underline-offset-2 hover:underline">
            Open the sign-in page
            <ExternalLink aria-hidden="true" className="size-3.5" />
          </a>{' '}
          and approve access.
        </li>
        {s.callback ? (
          <li>Finish in that tab, then return to this task. Its server status updates when sign-in completes; use Refresh if needed.</li>
        ) : s.relay ? (
          <li>
            The browser then goes to an address on 127.0.0.1 or localhost. From another computer that page does not load; that is expected. Copy the whole address from the address bar and paste it here. (On the server's own desktop it finishes by itself.)
          </li>
        ) : (
          <li>Finish in that tab. If it ends on a page that does not load, sign in on the server machine instead: run copilot there and use /mcp.</li>
        )}
      </ol>
      {s.relay && !s.sent && (
        <div className="flex flex-wrap items-center gap-2">
          <Input aria-label="Address the browser ended on" size="md" className="min-w-0 flex-1 font-mono text-code-sm" spellCheck={false} autoComplete="off" placeholder="http://127.0.0.1:…/?code=…&state=…" disabled={s.busy} value={s.pasted} onChange={(e) => onPaste(e.target.value)} />
          <Button type="submit" size="md" variant="primary" loading={s.busy} disabled={!s.pasted.trim()}>
            Finish sign-in
          </Button>
        </div>
      )}
      {s.sent && <Note role="status">Sent. The server connects in a moment.</Note>}
      {s.error && <Note tone="error" role="alert">{s.error}</Note>}
      <Button size="sm" className="self-start" disabled={s.busy} onClick={onCancel}>
        {s.sent ? 'Close' : 'Cancel'}
      </Button>
    </form>
  );
}

function ServerRow({ sessionId, legacyService, s, busy, signingIn, onToggle, onRestart, onSignIn, children }: Readonly<{ sessionId: string; legacyService: boolean; s: McpServerStatus; busy: boolean; signingIn: boolean; onToggle: (on: boolean) => void; onRestart: () => void; onSignIn: (again: boolean) => void; children?: ReactNode }>) {
  const api = useApi();
  const [toolsOpen, setToolsOpen] = useState(false);
  const [tools, setTools] = useState<McpTool[] | null>(null);
  const [toolsError, setToolsError] = useState<string | null>(null);
  const state = s.needs_reconnect ? { label: 'Reconnect needed', tone: 'warning' as Tone } : STATE[s.status] ?? { label: s.status, tone: 'muted' as Tone };
  useEffect(() => {
    if (!toolsOpen || s.status !== 'connected') return;
    let current = true;
    const read = legacyService ? api.taskMcp(sessionId).then((r) => {
      const row = r.servers.find((server) => server.name === s.name);
      if (!row) throw new Error('This MCP server is no longer listed.');
      return { tools: row.tools ?? [] };
    }) : api.taskMcpTools(sessionId, s.name);
    read.then((r) => { if (current) { setTools(r.tools); setToolsError(null); } }).catch((e: unknown) => { if (current) setToolsError(describeError(e)); });
    return () => { current = false; };
  }, [api, sessionId, legacyService, s.name, s.status, s.needs_reconnect, toolsOpen]);
  const on = s.status !== 'disabled';
  return (
    <li className="flex flex-col py-2">
      <div className="flex min-h-11 items-center gap-3">
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <span className="flex min-w-0 flex-wrap items-center gap-x-2">
            <span className="min-w-0 break-all text-ui font-medium text-ink">{s.name}</span>
            {s.source && SOURCE[s.source] && <span className="text-meta text-muted">{SOURCE[s.source]}</span>}
          </span>
          <span className="flex items-center gap-1.5 text-meta text-muted">
            <Dot tone={state.tone} />
            {state.label}
            {s.status === 'connected' && (
              <>
                <span aria-hidden="true">·</span>
                <button type="button" className="rounded-xs text-meta text-muted underline-offset-2 hover:text-ink hover:underline pointer-coarse:min-h-11" aria-expanded={toolsOpen} onClick={() => { if (!toolsOpen) { setTools(null); setToolsError(null); } setToolsOpen(!toolsOpen); }}>
                  {tools === null ? 'Tools' : tools.length === 1 ? '1 tool' : `${tools.length} tools`}
                </button>
              </>
            )}
          </span>
        </div>
        {s.status === 'needs-auth' && (
          <Button size="sm" variant="secondary" disabled={busy || signingIn} onClick={() => onSignIn(false)}>
            Sign in
          </Button>
        )}
        {s.status === 'connected' && s.remote && (
          <Button size="sm" disabled={busy || signingIn} onClick={() => onSignIn(true)}>
            Sign in again
          </Button>
        )}
        {(s.status === 'failed' || s.status === 'stopped' || s.needs_reconnect) && (
          <Button size="sm" variant="secondary" loading={busy} onClick={onRestart}>
            Restart
          </Button>
        )}
        <Switch aria-label={`Use ${s.name} in this task`} checked={on} disabled={busy} onCheckedChange={onToggle} />
      </div>
      {s.error && <p className="text-caption break-words text-error">{s.error}</p>}
      {children}
      <Collapse open={toolsOpen && s.status === 'connected'} soft>
        {!tools && !toolsError && <Skeleton label={`Reading ${s.name} tools…`} rows={2} />}
        {toolsError && <Note tone="error" role="alert">{toolsError}</Note>}
        {tools && tools.length === 0 && <Note>This server offers no tools.</Note>}
        {tools && tools.length > 0 && (
          <ul aria-label={`${s.name} tools`} className="mt-1 flex flex-col gap-1 rounded-md bg-tint-well px-3 py-2">
            {tools.map((t) => (
              <li key={t.name} className="flex min-w-0 flex-col">
                <code className="font-mono text-code-sm break-all text-ink">{t.name}</code>
                {t.description && <span className="line-clamp-2 text-caption text-muted">{t.description}</span>}
              </li>
            ))}
          </ul>
        )}
      </Collapse>
    </li>
  );
}

/**
 * A Task's MCP servers (task menu → MCP servers): each server's state as this Task's
 * conversation sees it, its tools, a switch that turns it off or on for this Task only,
 * Restart for one that failed, and the sign-in of a remote server that needs one, finished
 * from a remote browser at the configured HTTPS origin or by pasting its callback. Applying settings
 * reloads the conversation's configuration for its next turn.
 */
function snapshotOf(result: McpTaskStatus): McpStatusSnapshot {
  return result.mcp_status ?? { supported: false, ready: true, servers: result.servers.map(({ name, status, error, source, remote, needs_reconnect }) => ({ name, status, error, source, remote, needs_reconnect })) };
}

export function McpTaskDialog({ sessionId, snapshot, onClose }: Readonly<{ sessionId: string; snapshot?: McpStatusSnapshot; onClose: () => void }>) {
  const api = useApi();
  const [open, setOpen] = useState(true);
  const [initialStatus, setInitialStatus] = useState<McpStatusSnapshot | null>(null);
  const [legacyService, setLegacyService] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [signIn, setSignIn] = useState<SigningIn | null>(null);
  const status = snapshot ?? initialStatus;
  const servers = status?.servers ?? null;
  const activeSignIn = signIn && !servers?.some((s) => s.name === signIn.name && s.status === 'connected' && (signIn.sent || signIn.from !== 'connected')) ? signIn : null;
  // A direct reauthorization starts while still connected; observe its new
  // connection attempt before treating the next connected state as completion.
  const attempting = signIn?.callback && signIn.from === 'connected' && servers?.find((s) => s.name === signIn.name && s.status !== 'connected');
  if (signIn && attempting) setSignIn({ ...signIn, from: attempting.status });
  // Release the completed URL permanently; a later pending state must not revive it.
  if (signIn && !activeSignIn) setSignIn(null);

  async function run(key: string, action: () => Promise<McpTaskStatus>) {
    setBusy(key);
    setError(null);
    try {
      const result = await action();
      setLegacyService(!result.mcp_status);
      setInitialStatus(snapshotOf(result));
    } catch (e) {
      setError(describeError(e));
    } finally {
      setBusy(null);
    }
  }

  async function refresh() {
    await run('refresh', () => api.taskMcp(sessionId, true));
  }
  useEffect(() => {
    let current = true;
    api.taskMcp(sessionId, true).then((r) => { if (current) { setLegacyService(!r.mcp_status); setInitialStatus(snapshotOf(r)); } }).catch((e: unknown) => { if (current) setError(describeError(e)); });
    return () => { current = false; };
  }, [api, sessionId]);

  async function startSignIn(name: string, from: string, again: boolean) {
    setBusy(name);
    setError(null);
    try {
      const started = await api.startMcpSignIn(sessionId, name, again);
      if (started.url) setSignIn({ ...started, name, from, pasted: '', busy: false });
      else setInitialStatus(snapshotOf(await api.taskMcp(sessionId, true)));
    } catch (e) {
      setError(describeError(e));
    } finally {
      setBusy(null);
    }
  }

  async function finishSignIn(e: SubmitEvent) {
    e.preventDefault();
    if (!signIn) return;
    const current = signIn;
    setSignIn({ ...current, busy: true, error: undefined });
    try {
      await api.finishMcpSignIn(sessionId, current.name, current.pasted.trim());
      setSignIn({ ...current, busy: false, sent: true, pasted: '' });
      if (!status?.supported) void refresh();
    } catch (err) {
      setSignIn({ ...current, busy: false, error: describeError(err) });
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={setOpen}
      onClosed={onClose}
      title="MCP servers"
      description="The tool servers this task's agent can use. Switches here last until configuration reloads or the conversation closes. Apply current settings to refresh configuration for the next turn."
      footer={
        <>
          <Button size="lg" loading={busy === 'refresh'} disabled={!!busy} onClick={() => void refresh()}>Refresh</Button>
          <Button size="lg" loading={busy === 'reconnect'} disabled={!!busy || !!activeSignIn} onClick={() => void run('reconnect', () => api.reconnectTaskMcp(sessionId))}>
            Apply current settings
          </Button>
        </>
      }
    >
      {!status?.ready && !error && <Skeleton label="Reading this task's MCP servers…" rows={3} />}
      {status?.truncated && <Note tone="warn">Some server states could not fit in this view. Refresh to read current status.</Note>}
      {status && !status.supported && <Note>Live status is unavailable from this provider. Use Refresh to read it again.</Note>}
      {status?.ready && servers?.length === 0 && <Note>This task has no MCP servers. Add one in Settings → MCP servers, then apply current settings.</Note>}
      {servers && servers.length > 0 && (
        <ul aria-label="This task's MCP servers" className="flex flex-col">
          {servers.map((s) => (
            <ServerRow
              key={s.name}
              sessionId={sessionId}
              legacyService={!snapshot && legacyService}
              s={s}
              busy={busy === s.name}
              signingIn={!!activeSignIn}
              onToggle={(on) => void run(s.name, () => api.taskMcpAction(sessionId, s.name, on ? 'enable' : 'disable'))}
              onRestart={() => void run(s.name, () => api.taskMcpAction(sessionId, s.name, 'restart'))}
              onSignIn={(again) => void startSignIn(s.name, s.status, again)}
            >
              {activeSignIn?.name === s.name && (
                <SignInPanel s={activeSignIn} onPaste={(pasted) => setSignIn({ ...activeSignIn, pasted })} onFinish={(e) => void finishSignIn(e)} onCancel={() => setSignIn(null)} />
              )}
            </ServerRow>
          ))}
        </ul>
      )}
      {error && (
        <Note tone="error" role="alert" className="mt-2">
          {error}
        </Note>
      )}
    </Dialog>
  );
}
