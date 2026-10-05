import { useId, useState, type SubmitEvent } from 'react';
import { describeError, type AddConnectionInput, type ConnectedInstance, type ConnectedStatus, type UpdateConnectionInput } from '../api';
import { Note } from './common';
import { Appear } from './ui/appear';
import { Button } from './ui/button';
import { AlertDialog, useConfirm } from './ui/dialog';
import { Input } from './ui/input';
import { Switch } from './ui/switch';

export interface ConnectedInstancesSettingsProps {
  homeInstanceID: string;
  connections: ConnectedInstance[];
  statuses?: Record<string, ConnectedStatus>;
  activeTerminalConnectionID?: string | null;
  onAdd: (input: AddConnectionInput) => Promise<void>;
  onUpdate: (id: string, patch: UpdateConnectionInput) => Promise<void>;
  onRemove: (id: string) => Promise<void>;
  onRefresh: () => Promise<void>;
  /** Opens that instance's Settings at its GitHub Copilot account. */
  onOpenAccount?: (id: string) => void;
}

const statusText: Record<ConnectedStatus['status'], string> = {
  connecting: 'Connecting…',
  online: 'Connected',
  offline: 'Unavailable',
  'auth-required': 'Access needs renewal',
  unsupported: 'Update required',
  'account-mismatch': 'Linked to a different Copilot account',
};

/** The optional features a paired instance can offer this one, named as Settings names them, in its order. */
const FEATURES: readonly (readonly [string, string])[] = [['files-v1', 'Files'], ['terminal-v1', 'Terminal'], ['configuration-v1', 'Configuration'], ['provider-accounts-v1', 'Provider accounts'], ['planner-v1', 'Planner'], ['routines-v1', 'Routines'], ['usage-v1', 'Usage']];

/** "v0.7.1 · Files, Terminal, Planner": what the paired instance runs and offers, one line, or nothing known. */
function offers(connection: ConnectedInstance): string {
  const features = FEATURES.filter(([capability]) => connection.capabilities.includes(capability)).map(([, label]) => label);
  return [connection.version, features.join(', ')].filter(Boolean).join(' · ');
}

/** Credentials live only in the form until submitted; the server returns redacted records. */
function ConnectionForm({ initial, busy, onSubmit, onCancel }: Readonly<{
  initial?: ConnectedInstance;
  busy: boolean;
  onSubmit: (input: AddConnectionInput) => Promise<void>;
  onCancel: () => void;
}>) {
  const id = useId();
  const [label, setLabel] = useState(initial?.label ?? '');
  const [url, setURL] = useState(initial?.base_url ?? '');
  const [token, setToken] = useState('');
  const [allowPrivate, setAllowPrivate] = useState(initial?.allow_private ?? false);

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (busy || !label.trim() || !url.trim() || (!initial && !token.trim())) return;
    try {
      await onSubmit({ label: label.trim(), base_url: url.trim(), token: token.trim(), allow_private: allowPrivate });
    } finally {
      setToken('');
    }
  }

  return (
    <form aria-label={initial ? `Edit ${initial.label}` : 'Add an instance'} className="flex flex-col gap-3 rounded-md bg-tint-well p-3" onSubmit={(event) => void submit(event)}>
      <div className="grid gap-3 sm:grid-cols-2">
        <div className="flex min-w-0 flex-col gap-1">
          <label htmlFor={`${id}-label`} className="text-caption text-muted">Name</label>
          <Input id={`${id}-label`} value={label} onChange={(event) => setLabel(event.target.value)} placeholder="Work server" required maxLength={80} disabled={busy} />
        </div>
        <div className="flex min-w-0 flex-col gap-1">
          <label htmlFor={`${id}-url`} className="text-caption text-muted">Web URL</label>
          <Input id={`${id}-url`} type="url" value={url} onChange={(event) => setURL(event.target.value)} placeholder="https://uam.example.com" pattern="https://.*" required disabled={busy} aria-describedby={`${id}-url-help`} />
          <Note id={`${id}-url-help`}>Use the instance’s HTTPS address with a valid certificate.</Note>
        </div>
      </div>
      <div className="flex min-w-0 flex-col gap-1">
        <label htmlFor={`${id}-token`} className="text-caption text-muted">{initial ? 'New access key (optional)' : 'Access key'}</label>
        <Input id={`${id}-token`} type="password" autoComplete="off" autoCapitalize="off" spellCheck={false} value={token} onChange={(event) => setToken(event.target.value)} required={!initial} disabled={busy} aria-describedby={`${id}-key-help`} />
        <Note id={`${id}-key-help`}>
          {initial ? 'Leave blank to keep the saved access. ' : ''}This server saves a dedicated connection credential so your other devices can use it. The supplied access key is discarded after pairing.
        </Note>
      </div>
      <div className="flex items-start gap-3">
        <Switch checked={allowPrivate} onCheckedChange={setAllowPrivate} disabled={busy} aria-label="Allow a private network address" aria-describedby={`${id}-private-help`} className="mt-1" />
        <div>
          <span className="text-ui text-ink">Allow a private network address</span>
          <Note id={`${id}-private-help`}>Enable only for a server you trust on this server’s private network. HTTPS is still required.</Note>
        </div>
      </div>
      <div className="flex flex-wrap gap-2">
        <Button type="submit" variant="primary" loading={busy} disabled={!label.trim() || !url.trim() || (!initial && !token.trim())}>{initial ? 'Save connection' : 'Connect instance'}</Button>
        <Button variant="secondary" disabled={busy} onClick={onCancel}>Cancel</Button>
      </div>
    </form>
  );
}

/** Home-owned registry controls. Changing a connection never changes its remote task data. */
export function ConnectedInstancesSettings({ homeInstanceID, connections, statuses = {}, activeTerminalConnectionID, onAdd, onUpdate, onRemove, onRefresh, onOpenAccount }: Readonly<ConnectedInstancesSettingsProps>) {
  const [editor, setEditor] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const confirm = useConfirm<{ connection: ConnectedInstance; action: 'disable' | 'remove' }>();

  async function run(action: () => Promise<void>): Promise<boolean> {
    if (busy) return false;
    setBusy(true);
    setError(null);
    try {
      await action();
      return true;
    } catch (e) {
      setError(describeError(e));
      return false;
    } finally {
      setBusy(false);
    }
  }

  async function save(initial: ConnectedInstance | undefined, input: AddConnectionInput) {
    const patch: UpdateConnectionInput = { label: input.label, allow_private: input.allow_private };
    if (initial && input.base_url !== initial.base_url) patch.base_url = input.base_url;
    if (input.token) patch.token = input.token;
    if (await run(() => initial ? onUpdate(initial.id, patch) : onAdd(input))) {
      setEditor(null);
      setAdding(false);
    }
  }

  function toggle(connection: ConnectedInstance, enabled: boolean) {
    if (!enabled && activeTerminalConnectionID === connection.id) confirm.ask({ connection, action: 'disable' });
    else void run(() => onUpdate(connection.id, { enabled }));
  }

  function applyConfirmation() {
    const target = confirm.target;
    if (!target) return;
    confirm.close();
    void run(() => target.action === 'remove' ? onRemove(target.connection.id) : onUpdate(target.connection.id, { enabled: false }));
  }

  return (
    <div className="flex flex-col gap-4">
      <Note>Connect your other UAM servers here. Each server keeps its own tasks, files, settings, and Copilot account. Only the instances listed here are included, even when they connect back to this server.</Note>
      {homeInstanceID && <Note>This instance: <span className="break-all font-mono">{homeInstanceID}</span></Note>}
      <div className="flex flex-wrap gap-2">
        <Button variant="primary" disabled={busy || adding} onClick={() => { setAdding(true); setEditor(null); setError(null); }}>Add instance</Button>
        <Button variant="secondary" disabled={busy} onClick={() => void run(onRefresh)}>Check again</Button>
      </div>
      {error && <Note tone="error" role="alert">Could not update connected instances: {error}</Note>}
      {adding && <ConnectionForm busy={busy} onSubmit={(input) => save(undefined, input)} onCancel={() => setAdding(false)} />}
      {connections.length === 0 && !adding && <Note>No connected instances yet. Your local tasks are available as usual.</Note>}
      {connections.map((connection) => {
        const status = statuses[connection.id];
        return (
          <section key={connection.id} aria-label={connection.label} className="flex flex-col gap-3 rounded-md bg-tint-well p-3">
            <div className="flex flex-wrap items-start gap-3">
              <div className="min-w-0 flex-1">
                <h3 className="break-words text-ui font-medium text-ink">{connection.label}</h3>
                <Note><span className="break-all">{connection.base_url}</span></Note>
                <Note><span className="break-all font-mono">{connection.instance_id}</span></Note>
                {offers(connection) && <Note><span className="block truncate" title={offers(connection)}>{offers(connection)}</span></Note>}
              </div>

              <Switch checked={connection.enabled} disabled={busy} onCheckedChange={(enabled) => toggle(connection, enabled)} aria-label={`Enable ${connection.label}`} />
            </div>
            <Note role="status" className="transition-colors duration-160 ease-app" tone={connection.enabled && status && status.status !== 'online' && status.status !== 'connecting' ? 'warn' : 'muted'}>
              {!connection.enabled ? 'Disabled' : status ? statusText[status.status] : 'Waiting for connection status'}
              {connection.enabled && status?.error ? ` — ${status.error}` : ''}
            </Note>
            <div className="flex flex-wrap gap-2">
              {onOpenAccount && <Appear show={connection.enabled && status?.status === 'account-mismatch'}><Button variant="primary" onClick={() => onOpenAccount(connection.id)}>Open its GitHub Copilot settings</Button></Appear>}
              <Button variant="secondary" disabled={busy} onClick={() => { setEditor(connection.id); setAdding(false); setError(null); }}>Edit connection</Button>
              <Button variant="danger" disabled={busy} onClick={() => confirm.ask({ connection, action: 'remove' })}>Remove connection</Button>
            </div>
            {editor === connection.id && <ConnectionForm key={connection.id} initial={connection} busy={busy} onSubmit={(input) => save(connection, input)} onCancel={() => setEditor(null)} />}
          </section>
        );
      })}
      <AlertDialog
        {...confirm.props}
        title={confirm.target?.action === 'disable' ? `Disable ${confirm.target.connection.label}?` : `Remove ${confirm.target?.connection.label ?? 'connection'}?`}
        description={confirm.target?.action === 'disable'
          ? 'The active terminal shell will close. Running agent tasks continue on that instance. You can enable the connection again later.'
          : 'This removes the saved connection and closes any active terminal shell. Tasks and files on the other instance are not deleted. To connect again, you will need its access key.'}
        confirmLabel={confirm.target?.action === 'disable' ? 'Disable connection' : 'Remove connection'}

        busy={busy}
        onConfirm={applyConfirmation}
      />
    </div>
  );
}
