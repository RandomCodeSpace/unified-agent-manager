import { useApi } from '../ApiContext';
import { Check, Copy, ExternalLink } from 'lucide-react';
import { useEffect, useEffectEvent, useRef, useState, type ReactNode, type SubmitEvent } from 'react';
import { ACCOUNT_NOT_LINKED, describeError, errorCode, type DeviceSignIn, type ProviderAccount, type ProviderInfo } from '../api';
import { useCopied } from '../lib/clipboard';
import { Dot, Note, Skeleton, useApp, WorkingMark } from './common';
import { AlertDialog, useConfirm } from './ui/dialog';
import { Button, buttonVariants } from './ui/button';
import { Input } from './ui/input';

/** GitHub's page for a new fine-grained personal access token. */
export const TOKEN_PAGE = 'https://github.com/settings/personal-access-tokens/new';

const codeClass = 'rounded-xs bg-sunken px-1 font-sans text-caption text-ink';

function Code({ children }: Readonly<{ children: ReactNode }>) {
  return <code className={codeClass}>{children}</code>;
}

/** How a signed-in Copilot gets its credential, in words; never the credential. */
export function sourceText(a: ProviderAccount): ReactNode {
  switch (a.source) {
    case 'stored':
      return 'Through a sign-in Copilot stored on this server.';
    case 'env':
      return <>Through the token in <Code>{a.env_var || 'an environment variable'}</Code>, set where the service starts.</>;
    case 'gh-cli':
      return 'Through the GitHub CLI (gh) sign-in on this server.';
    default:
      return 'Through a credential the Copilot CLI found on this server.';
  }
}

/** The notice after a sign-in, by token or by device code. */
function signedInText(a: ProviderAccount): string {
  const who = a.login ? ` as ${a.login}` : '';
  return a.stored === false
    ? `Signed in${who} until the service restarts: Copilot could not store the sign-in on this server. Every task on this server uses this account.`
    : `Signed in${who}. Every task on this server uses this account from its next message.`;
}

const GITHUB = 'https://github.com';

/** ` on <host>` for an account on another GitHub host than github.com, else nothing. */
const onHost = (host?: string) => (host && host !== GITHUB ? ` on ${host.replace(/^https?:\/\//, '')}` : '');

const inProgress = (d: DeviceSignIn | null) => d?.state === 'starting' || d?.state === 'waiting';

/**
 * Settings → GitHub Copilot: whether the server's Copilot is signed in, as whom and how, read again each time the
 * section opens; sign in with GitHub (a device code approved on GitHub) or with a token (sent straight to Copilot,
 * never kept here), and sign out of a stored sign-in. Each applies to every Task on this server, so a replaced or
 * removed sign-in is confirmed first. Device sign-in is offered only while signed out: its restart would cut open
 * conversations. The server is linked to one account, the first one signed in: a sign-in as another is refused, and
 * Unlink (confirmed) clears the link and signs out a stored sign-in.
 */
export function CopilotAccount({ provider }: Readonly<{ provider: ProviderInfo }>) {
  const api = useApi();
  const { refreshMeta } = useApp();
  const [account, setAccount] = useState<ProviderAccount | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [reads, setReads] = useState(0);
  const [token, setToken] = useState('');
  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [done, setDone] = useState<string | null>(null);
  const [replacing, setReplacing] = useState(false);
  const replace = useConfirm<true>();
  const signOut = useConfirm<true>();
  // The login being unlinked: the dialog keeps its title while it closes, after the link is gone.
  const unlink = useConfirm<string>();
  const [device, setDevice] = useState<DeviceSignIn | null>(null);
  const [starting, setStarting] = useState(false);
  const [copied, copy] = useCopied();
  const copyButton = useRef<HTMLButtonElement>(null);
  // Set by a click on Sign in with GitHub, so focus moves to Copy code once the code shows; never on a resumed one.
  const focusCopy = useRef(false);
  const name = provider.name;
  const deviceCapable = provider.capabilities.device_sign_in === true;
  const refresh = useRef(refreshMeta);
  useEffect(() => {
    refresh.current = refreshMeta;
  });

  // Read on every open and Check again; reading also brings the service's provider list in line with it.
  useEffect(() => {
    let current = true;
    api
      .account(name)
      .then((a) => {
        if (!current) return;
        setAccount(a);
        setError(null);
      })
      .catch((e: unknown) => current && setError(describeError(e)));
    return () => {
      current = false;
    };
  }, [api, name, reads]);
  // The catalogs disagree with what was just read: reload them, so the signed-out banner and the models follow.
  const listedOut = provider.signed_out === true;
  useEffect(() => {
    if (account && account.signed_in === listedOut) refresh.current();
  }, [account, listedOut]);

  async function signIn() {
    replace.close();
    setBusy(true);
    setFormError(null);
    setDone(null);
    try {
      const a = await api.signIn(name, token.trim());
      setAccount(a);
      setReplacing(false);
      setDone(signedInText(a));
      refresh.current();
    } catch (e) {
      setFormError(`Could not sign in: ${describeError(e)}`);
      // The server removed a sign-in as another account than the linked one: read what is left.
      if (errorCode(e) === ACCOUNT_NOT_LINKED) {
        setReplacing(false);
        setReads((n) => n + 1);
        refresh.current();
      }
    } finally {
      // The field never keeps a token, accepted or not.
      setToken('');
      setBusy(false);
    }
  }

  // A device sign-in already waiting (started in another tab, or before a reload) shows its code again.
  useEffect(() => {
    if (!deviceCapable) return;
    let current = true;
    api
      .deviceSignIn(name)
      .then((d) => current && inProgress(d) && setDevice(d))
      .catch(() => {});
    return () => {
      current = false;
    };
  }, [api, name, deviceCapable]);

  function follow(d: DeviceSignIn) {
    if (d.state === 'signed_in') {
      setDevice(null);
      if (d.account) setAccount(d.account);
      else setReads((n) => n + 1);
      setDone(signedInText(d.account ?? { signed_in: true }));
      refresh.current();
    } else if (d.state === 'idle' || d.state === 'canceled') setDevice(null);
    else setDevice(d);
  }
  const polled = useEffectEvent(follow);

  // Poll while GitHub waits for the code; a hidden page skips its turns.
  const waiting = inProgress(device);
  useEffect(() => {
    if (!waiting) return;
    let current = true;
    const timer = window.setInterval(() => {
      if (document.visibilityState === 'hidden') return;
      api
        .deviceSignIn(name)
        .then((d) => current && polled(d))
        .catch(() => {});
    }, 2000);
    return () => {
      current = false;
      window.clearInterval(timer);
    };
  }, [api, name, waiting]);

  const code = device?.user_code;
  useEffect(() => {
    if (!code || !focusCopy.current) return;
    focusCopy.current = false;
    copyButton.current?.focus();
  }, [code]);

  async function startDevice() {
    setStarting(true);
    setFormError(null);
    setDone(null);
    focusCopy.current = true;
    try {
      follow(await api.startDeviceSignIn(name));
    } catch (e) {
      focusCopy.current = false;
      setDevice(null);
      setFormError(`Could not sign in: ${describeError(e)}`);
    } finally {
      setStarting(false);
    }
  }

  async function cancelDevice() {
    setFormError(null);
    try {
      await api.cancelDeviceSignIn(name);
      setDevice(null);
    } catch (e) {
      setFormError(`Could not cancel the sign-in: ${describeError(e)}`);
    }
  }

  async function confirmSignOut() {
    signOut.close();
    setBusy(true);
    setFormError(null);
    setDone(null);
    try {
      setAccount(await api.signOut(name));
      setDone('Signed out. Tasks on this server cannot run until Copilot is signed in again.');
      refresh.current();
    } catch (e) {
      setFormError(`Could not sign out: ${describeError(e)}`);
    } finally {
      setBusy(false);
    }
  }

  async function confirmUnlink() {
    const was = unlink.target ?? 'the account';
    unlink.close();
    setBusy(true);
    setFormError(null);
    setDone(null);
    try {
      const a = await api.unlink(name);
      setAccount(a);
      setReplacing(false);
      setDone(
        a.signed_in
          ? `Unlinked ${was}. Copilot is still signed in${a.login ? ` as ${a.login}` : ''} in a way UAM cannot sign out, so this server links that account again.`
          : `Unlinked ${was} and signed out. The next sign-in links its account.`,
      );
      refresh.current();
    } catch (e) {
      setFormError(`Could not unlink: ${describeError(e)}`);
    } finally {
      setBusy(false);
    }
  }

  function submit(e: SubmitEvent) {
    e.preventDefault();
    if (!token.trim() || busy) return;
    if (account?.signed_in) replace.ask(true);
    else void signIn();
  }

  if (!account) {
    return error ? (
      <Note tone="error" role="alert" className="flex flex-wrap items-center gap-2">
        <span className="min-w-0 flex-1">Could not read the Copilot sign-in: {error}</span>
        <Button size="sm" variant="secondary" onClick={() => setReads((n) => n + 1)}>
          Retry
        </Button>
      </Note>
    ) : (
      <Skeleton label="Reading the Copilot sign-in…" rows={2} />
    );
  }

  const env = account.env_var;
  const offerDevice = deviceCapable && !env && !account.signed_in;
  const showForm = !env && (!account.signed_in || replacing) && !(offerDevice && waiting);
  const host = onHost(account.host);
  const linked = account.linked;
  // Signed in as another account than the one this server is linked to: Copilot is unavailable until that is fixed.
  const mismatch = account.signed_in && !!linked && (account.login?.toLowerCase() !== linked.login.toLowerCase() || (account.host || GITHUB) !== (linked.host || GITHUB));

  return (
    <>
      <div className="flex flex-wrap items-start gap-x-6 gap-y-3">
        <div className="flex min-w-0 flex-1 basis-64 flex-col gap-1" role="status">
          <span className="flex items-center gap-2 text-ui font-medium text-ink">
            <Dot tone={account.signed_in ? 'success' : 'warning'} />
            {account.signed_in ? <span className="min-w-0 [overflow-wrap:anywhere]">Signed in{account.login ? ` as ${account.login}` : ''}{host}</span> : 'Signed out'}
          </span>
          <Note>
            {account.signed_in ? sourceText(account) : 'Tasks cannot start or run until Copilot is signed in.'}
            {!account.signed_in && account.message ? <> Copilot says: {account.message}</> : null}
          </Note>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button size="md" variant="secondary" disabled={busy} onClick={() => { setDone(null); setReads((n) => n + 1); }}>
            Check again
          </Button>
          {account.signed_in && !env && !replacing && (
            <Button size="md" variant="secondary" disabled={busy} onClick={() => setReplacing(true)}>
              Use another token
            </Button>
          )}
          {account.signed_in && account.source === 'stored' && !env && (
            <Button size="md" variant="danger" disabled={busy} onClick={() => signOut.ask(true)}>
              Sign out
            </Button>
          )}
        </div>
      </div>
      {mismatch && linked && (
        <Note tone="warn" className="[overflow-wrap:anywhere]">
          GitHub Copilot is signed in as {account.login}{host}, but this server is linked to {linked.login}{onHost(linked.host)}. Sign in as {linked.login}, or unlink the account in Settings.
        </Note>
      )}
      {linked && (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
          <Note className="min-w-0 [overflow-wrap:anywhere]">
            Linked account: {linked.login}
            {onHost(linked.host)}
          </Note>
          <Button size="sm" variant="danger" disabled={busy} onClick={() => unlink.ask(linked.login)}>
            Unlink
          </Button>
        </div>
      )}
      {env && (
        <Note tone="warn">
          The token in <Code>{env}</Code>, set in the service environment, takes precedence over any sign-in made here, so signing in and out here is off. Change or remove it where the service starts, then restart the service.
        </Note>
      )}
      {account.signed_in && account.source === 'gh-cli' && !env && (
        <Note>
          To sign this account out, run <Code>gh auth logout</Code> on the server.
        </Note>
      )}
      {offerDevice && (
        <div className="flex max-w-xl flex-col gap-3">
          {device?.state === 'failed' ? (
            <Note tone="error" role="alert" className="flex flex-wrap items-center gap-2">
              <span className="min-w-0 flex-1">Could not sign in: {device.error || 'GitHub did not confirm the sign-in.'}</span>
              <Button size="sm" variant="secondary" loading={starting} onClick={() => void startDevice()}>
                Try again
              </Button>
            </Note>
          ) : waiting ? (
            <>
              {code && (
                <div role="group" aria-label="Device code" className="flex flex-wrap items-center gap-x-4 gap-y-2">
                  <span className="min-w-0 select-all font-mono text-display-md text-ink [overflow-wrap:anywhere]">{code}</span>
                  <Button ref={copyButton} size="md" variant="secondary" onClick={() => copy(code)}>
                    {copied ? <Check /> : <Copy />}
                    {copied ? 'Copied' : 'Copy code'}
                  </Button>
                </div>
              )}
              {code && <Note>Enter the code on GitHub and approve Copilot CLI. This page updates when you are done.</Note>}
              <div className="flex flex-wrap items-center gap-2">
                {code && device?.verification_uri && (
                  <a href={device.verification_uri} target="_blank" rel="noopener noreferrer" className={buttonVariants({ variant: 'primary', size: 'md' })}>
                    Open GitHub
                    <ExternalLink aria-hidden="true" />
                  </a>
                )}
                <Button size="md" variant="secondary" onClick={() => void cancelDevice()}>
                  Cancel
                </Button>
                <span role="status" className="flex items-center gap-2 text-caption text-muted">
                  <WorkingMark />
                  {code ? 'Waiting for approval on GitHub…' : 'Getting a code from GitHub…'}
                </span>
              </div>
            </>
          ) : (
            <>
              <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
                <Button size="lg" variant="primary" loading={starting} disabled={busy} onClick={() => void startDevice()}>
                  Sign in with GitHub
                </Button>
                <Note className="min-w-0 flex-1 basis-60">Get a code here, enter it on GitHub and approve Copilot CLI. Every task on this server then uses that account.</Note>
              </div>
              {linked && <Note className="[overflow-wrap:anywhere]">Use the GitHub account {linked.login}; this server is linked to it.</Note>}
            </>
          )}
        </div>
      )}
      {showForm && (
        <form className="flex max-w-xl flex-col gap-3" onSubmit={submit}>
          <div className="flex min-w-0 flex-col gap-1">
            <label htmlFor={`${name}-token`} className="text-ui font-medium text-ink">
              {offerDevice ? 'Or sign in with a token' : 'Sign in with a token'}
            </label>
            <Note id={`${name}-token-help`}>
              Use a fine-grained personal access token with the <strong className="font-medium text-body">Copilot Requests</strong> permission; classic tokens (<Code>ghp_…</Code>) do not work.{' '}
              <a href={TOKEN_PAGE} target="_blank" rel="noopener noreferrer" className="text-accent underline underline-offset-2">
                Create one on GitHub
              </a>
              . The token goes straight to Copilot, which checks it with GitHub and stores it; UAM does not keep it.
            </Note>
          </div>
          <div className="flex flex-wrap gap-2">
            <Input
              id={`${name}-token`}
              className="min-w-0 flex-1 basis-60"
              type="password"
              autoComplete="off"
              autoCapitalize="off"
              spellCheck={false}
              aria-describedby={`${name}-token-help ${name}-token-scope`}
              placeholder="github_pat_…"
              value={token}
              disabled={busy}
              onChange={(e) => setToken(e.target.value)}
            />
            <Button type="submit" size="lg" variant={offerDevice ? 'secondary' : 'primary'} loading={busy} disabled={!token.trim()} className="min-w-24">
              Sign in
            </Button>
            {replacing && (
              <Button size="lg" variant="secondary" disabled={busy} onClick={() => { setReplacing(false); setToken(''); setFormError(null); }}>
                Cancel
              </Button>
            )}
          </div>
          <Note id={`${name}-token-scope`} tone="warn">
            This changes the Copilot account for every task on this server, and for the copilot command run as the same user here.
          </Note>
        </form>
      )}
      {formError && (
        <Note tone="error" role="alert">
          {formError}
        </Note>
      )}
      {done && <Note role="status">{done}</Note>}
      {!deviceCapable && (
        <Note>
          Signing in with a code shown in the browser is not supported here. To use it, run <Code>copilot login</Code> on the server as the user that runs UAM, then choose Check again.
        </Note>
      )}
      <AlertDialog
        {...replace.props}
        title="Replace the Copilot sign-in?"
        description={`Every task on this server, and the copilot command run as the same user here, will use the token's account instead of ${account.login || 'the current one'}.`}
        confirmLabel="Sign in"
        danger={false}
        onConfirm={() => void signIn()}
      />
      <AlertDialog
        {...signOut.props}
        title="Sign out of GitHub Copilot?"
        description="Every task on this server stops working until Copilot is signed in again, and the copilot command run as the same user here is signed out too."
        confirmLabel="Sign out"
        onConfirm={() => void confirmSignOut()}
      />
      <AlertDialog
        {...unlink.props}
        title={`Unlink ${unlink.target ?? 'the account'}?`}
        description="Copilot signs out on this server, and the next sign-in links its account. Every task on this server uses that account."
        confirmLabel="Unlink"
        onConfirm={() => void confirmUnlink()}
      />
    </>
  );
}
