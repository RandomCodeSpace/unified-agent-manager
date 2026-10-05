import { useApi } from '../ApiContext';
import { ExternalLink } from 'lucide-react';
import { useEffect, useEffectEvent, useState, type ReactNode, type SubmitEvent } from 'react';
import { describeError, type McpSignIn, type McpStatus } from '../api';
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

/** How often the list is read again while a server starts or a sign-in waits. */
const POLL_MS = 2000;

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
        {s.relay ? (
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

function ServerRow({ s, busy, signingIn, onToggle, onRestart, onSignIn, children }: Readonly<{ s: McpStatus; busy: boolean; signingIn: boolean; onToggle: (on: boolean) => void; onRestart: () => void; onSignIn: (again: boolean) => void; children?: ReactNode }>) {
  const [toolsOpen, setToolsOpen] = useState(false);
  const state = STATE[s.status] ?? { label: s.status, tone: 'muted' as Tone };
  const tools = s.tools ?? [];
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
            <Dot tone={state.tone} pulse={s.status === 'pending'} />
            {state.label}
            {tools.length > 0 && (
              <>
                <span aria-hidden="true">·</span>
                <button type="button" className="rounded-xs text-meta text-muted underline-offset-2 hover:text-ink hover:underline pointer-coarse:min-h-11" aria-expanded={toolsOpen} onClick={() => setToolsOpen(!toolsOpen)}>
                  {tools.length === 1 ? '1 tool' : `${tools.length} tools`}
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
        {(s.status === 'failed' || s.status === 'stopped') && (
          <Button size="sm" variant="secondary" loading={busy} onClick={onRestart}>
            Restart
          </Button>
        )}
        <Switch aria-label={`Use ${s.name} in this task`} checked={on} disabled={busy} onCheckedChange={onToggle} />
      </div>
      {s.error && <p className="text-caption break-words text-error">{s.error}</p>}
      {children}
      <Collapse open={toolsOpen && tools.length > 0} soft>
        <ul aria-label={`${s.name} tools`} className="mt-1 flex flex-col gap-1 rounded-md bg-tint-well px-3 py-2">
          {tools.map((t) => (
            <li key={t.name} className="flex min-w-0 flex-col">
              <code className="font-mono text-code-sm break-all text-ink">{t.name}</code>
              {t.description && <span className="line-clamp-2 text-caption text-muted">{t.description}</span>}
            </li>
          ))}
        </ul>
      </Collapse>
    </li>
  );
}

/**
 * A Task's MCP servers (task menu → MCP servers): each server's state as this Task's
 * conversation sees it, its tools, a switch that turns it off or on for this Task only,
 * Restart for one that failed, and the sign-in of a remote server that needs one, finished
 * from a remote browser by pasting the address it ended on. Reconnect reopens the
 * conversation so it starts the servers Settings configures now.
 */
export function McpTaskDialog({ sessionId, onClose }: Readonly<{ sessionId: string; onClose: () => void }>) {
  const api = useApi();
  const [open, setOpen] = useState(true);
  const [servers, setServers] = useState<McpStatus[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [signIn, setSignIn] = useState<SigningIn | null>(null);

  async function run(key: string, action: () => Promise<{ servers: McpStatus[] }>) {
    setBusy(key);
    setError(null);
    try {
      setServers((await action()).servers);
    } catch (e) {
      setError(describeError(e));
    } finally {
      setBusy(null);
    }
  }

  async function refresh() {
    try {
      const next = (await api.taskMcp(sessionId)).servers;
      setServers(next);
      if (signIn && next.some((s) => s.name === signIn.name && s.status === 'connected' && (signIn.sent || signIn.from !== 'connected'))) setSignIn(null);
    } catch {
      // The next read retries; the last list stays.
    }
  }
  const tick = useEffectEvent(() => void refresh());
  const waiting = !!signIn || !!servers?.some((s) => s.status === 'pending');
  useEffect(() => {
    let current = true;
    api.taskMcp(sessionId).then((r) => { if (current) setServers(r.servers); }).catch((e: unknown) => { if (current) setError(describeError(e)); });
    return () => { current = false; };
  }, [api, sessionId]);
  useEffect(() => {
    if (!open || !waiting) return;
    const timer = window.setInterval(tick, POLL_MS);
    return () => window.clearInterval(timer);
  }, [open, waiting]);

  async function startSignIn(name: string, from: string, again: boolean) {
    setBusy(name);
    setError(null);
    try {
      const started = await api.startMcpSignIn(sessionId, name, again);
      if (started.url) setSignIn({ ...started, name, from, pasted: '', busy: false });
      else setServers((await api.taskMcp(sessionId)).servers);
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
      void refresh();
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
      description="The tool servers this task's agent can use. Switches here last until the conversation closes; Settings → MCP servers sets what new tasks start with."
      footer={
        <Button size="lg" loading={busy === 'reconnect'} disabled={!!busy || !!signIn} onClick={() => void run('reconnect', () => api.reconnectTaskMcp(sessionId))}>
          Reconnect with current settings
        </Button>
      }
    >
      {!servers && !error && <Skeleton label="Reading this task's MCP servers…" rows={3} />}
      {servers && servers.length === 0 && <Note>This task has no MCP servers. Add one in Settings → MCP servers, then reconnect.</Note>}
      {servers && servers.length > 0 && (
        <ul aria-label="This task's MCP servers" className="flex flex-col">
          {servers.map((s) => (
            <ServerRow
              key={s.name}
              s={s}
              busy={busy === s.name}
              signingIn={!!signIn}
              onToggle={(on) => void run(s.name, () => api.taskMcpAction(sessionId, s.name, on ? 'enable' : 'disable'))}
              onRestart={() => void run(s.name, () => api.taskMcpAction(sessionId, s.name, 'restart'))}
              onSignIn={(again) => void startSignIn(s.name, s.status, again)}
            >
              {signIn?.name === s.name && (
                <SignInPanel s={signIn} onPaste={(pasted) => setSignIn({ ...signIn, pasted })} onFinish={(e) => void finishSignIn(e)} onCancel={() => setSignIn(null)} />
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
