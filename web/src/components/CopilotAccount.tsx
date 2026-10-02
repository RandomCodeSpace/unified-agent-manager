import { useEffect, useRef, useState, type ReactNode, type SubmitEvent } from 'react';
import { api, describeError, type ProviderAccount, type ProviderInfo } from '../api';
import { Dot, Note, Skeleton, useApp } from './common';
import { AlertDialog, useConfirm } from './ui/dialog';
import { Button } from './ui/button';
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

/**
 * Settings → GitHub Copilot: whether the server's Copilot is signed in, as whom and how, read again each time the
 * section opens; sign in with a token (sent straight to Copilot, never kept here) and sign out of a stored sign-in.
 * Either applies to every Task on this server, so a replaced or removed sign-in is confirmed first.
 */
export function CopilotAccount({ provider }: Readonly<{ provider: ProviderInfo }>) {
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
  const name = provider.name;
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
  }, [name, reads]);
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
      const who = a.login ? ` as ${a.login}` : '';
      setDone(a.stored === false
        ? `Signed in${who} until the service restarts: Copilot could not store the sign-in on this server. Every task on this server uses this account.`
        : `Signed in${who}. Every task on this server uses this account from its next message.`);
      refresh.current();
    } catch (e) {
      setFormError(`Could not sign in: ${describeError(e)}`);
    } finally {
      // The field never keeps a token, accepted or not.
      setToken('');
      setBusy(false);
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
  const showForm = !env && (!account.signed_in || replacing);
  const host = account.host && account.host !== 'https://github.com' ? ` on ${account.host.replace(/^https?:\/\//, '')}` : '';

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
      {showForm && (
        <form className="flex max-w-xl flex-col gap-3" onSubmit={submit}>
          <div className="flex min-w-0 flex-col gap-1">
            <label htmlFor={`${name}-token`} className="text-ui font-medium text-ink">
              Sign in with a token
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
            <Button type="submit" size="lg" variant="primary" loading={busy} disabled={!token.trim()} className="min-w-24">
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
      <Note>
        Signing in with a code shown in the browser is not supported here. To use it, run <Code>copilot login</Code> on the server as the user that runs UAM, then choose Check again.
      </Note>
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
    </>
  );
}
