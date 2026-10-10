// The in-browser fake's Copilot sign-in (docs/web.md, GitHub Copilot sign-in). `?copilot=out` starts signed out,
// `?copilot=env` with a token in GH_TOKEN, `?copilot=gh` through the GitHub CLI; otherwise a stored sign-in.
// A token starting github_pat_ signs in; any other is refused the way GitHub refuses a bad one.
// Sign in with GitHub shows a code; a read 1.5 s after it started signs in. `?device=off` lacks the capability,
// `?device=waiting` has a sign-in waiting already, `?device=fail` fails it and `?device=hold` never finishes it.
// `?device=keychain` is a server with no keychain: the first approval brings a second code with a notice.
// Both sign in as octocat. The first read of a signed-in account, or the first sign-in, links the server to it;
// `?linked=<login>` starts linked to that login, so a sign-in as octocat is refused, and `?mismatch=1` starts
// signed in as octocat while linked to monalisa (or the `?linked` login). Unlink clears the link and signs out a
// stored sign-in.

import type { DeviceSignIn, Meta, ProviderAccount } from '../api';

type Json = Record<string, unknown>;

function json(status: number, body?: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

const fail = (status: number, error: string, code?: string) => json(status, code ? { error, code } : { error });

const SIGNED_IN: ProviderAccount = { signed_in: true, login: 'octocat', host: 'https://github.com', source: 'stored', message: 'octocat' };
const SIGNED_OUT: ProviderAccount = { signed_in: false, message: 'Not authenticated' };
const REASON = 'GitHub Copilot is signed out. Sign in in Settings.';
const GITHUB = 'https://github.com';
const notLinked = (login: string) => `This server is linked to Copilot account ${login}. Sign in with that account.`;

function initial(mode: string | null): ProviderAccount {
  if (mode === 'out') return SIGNED_OUT;
  if (mode === 'env') return { ...SIGNED_IN, source: 'env', env_var: 'GH_TOKEN', message: 'octocat (via GH_TOKEN)' };
  if (mode === 'gh') return { ...SIGNED_IN, source: 'gh-cli', message: 'octocat (via gh)' };
  return SIGNED_IN;
}

/** Routes /api/providers/copilot/account*, keeping `meta`'s Copilot entry in step; null for any other path. */
export function accountMock(meta: Meta) {
  const query = new URLSearchParams(window.location.search);
  let account = initial(query.get('copilot'));
  const linkedLogin = query.get('linked') || (query.get('mismatch') === '1' ? 'monalisa' : null);
  let linked: ProviderAccount['linked'] = linkedLogin ? { login: linkedLogin, host: GITHUB, linked_at: new Date().toISOString() } : undefined;
  /** The account a sign-in would bring is not the linked one. */
  const other = (a: ProviderAccount) => !!linked && a.signed_in && (a.login?.toLowerCase() !== linked.login.toLowerCase() || (a.host ?? GITHUB) !== linked.host);
  /** A signed-in account links the server when nothing is linked yet. */
  function link() {
    if (!linked && account.signed_in && account.login) linked = { login: account.login, host: account.host ?? GITHUB, linked_at: new Date().toISOString() };
  }
  const view = (): ProviderAccount => (linked ? { ...account, linked } : account);
  const deviceMode = query.get('device');
  const WAITING: DeviceSignIn = { state: 'waiting', verification_uri: 'https://github.com/login/device', user_code: 'B4F2-9C1D' };
  let device: DeviceSignIn = deviceMode === 'waiting' ? WAITING : { state: 'idle' };
  // When the waiting sign-in started; one waiting at load starts at its first read.
  let began = 0;
  const copilot = meta.providers.find((p) => p.name === 'copilot');
  if (copilot) copilot.capabilities = { ...copilot.capabilities, account: true, device_sign_in: deviceMode !== 'off' };
  const mismatchReason = () => `GitHub Copilot is signed in as ${account.login}, but this server is linked to ${linked?.login}. Sign in as ${linked?.login}, or unlink the account in Settings.`;
  function sync() {
    if (!copilot) return;
    const mismatch = other(account);
    copilot.available = account.signed_in && !mismatch;
    copilot.signed_out = !account.signed_in || undefined;
    copilot.account_mismatch = mismatch || undefined;
    copilot.reason = !account.signed_in ? REASON : mismatch ? mismatchReason() : undefined;
  }
  sync();
  /** The refusal a create or send gets while signed out or signed in as another account, or null. */
  function refusal() {
    if (!account.signed_in) return fail(409, REASON, 'provider_signed_out');
    return other(account) ? fail(409, mismatchReason(), 'account_not_linked') : null;
  }
  /** Signs in as `next`, or refuses another account than the linked one and removes that sign-in again. */
  function signInAs(next: ProviderAccount): string | null {
    if (other(next)) {
      if (account.source === 'stored') account = SIGNED_OUT;
      sync();
      return notLinked(linked?.login ?? '');
    }
    account = next;
    link();
    sync();
    return null;
  }
  function route(method: string, path: string, body: Json): Response | null {
    const base = '/api/providers/copilot/account';
    if (!path.startsWith(base)) return null;
    if (path === base && method === 'GET') {
      link();
      sync();
      return json(200, view());
    }
    if (path === `${base}/sign-in` && method === 'POST') {
      if (account.env_var) return fail(409, `${account.env_var} in the service environment takes precedence over a sign-in made here; change or remove it where the service starts`);
      const token = String(body.token ?? '').trim();
      if (!token) return fail(400, 'enter a token');
      if (!token.startsWith('github_pat_')) return fail(400, 'Failed to fetch Copilot user info: 401 Unauthorized: {"message":"Bad credentials"}');
      const refused = signInAs({ ...SIGNED_IN, stored: true });
      if (refused) return fail(409, refused, 'account_not_linked');
      return json(200, view());
    }
    if (path === `${base}/device` && method === 'POST') {
      if (account.env_var) return fail(409, `${account.env_var} in the service environment takes precedence over a sign-in made here; change or remove it where the service starts`);
      if (device.state !== 'waiting') {
        device = WAITING;
        began = Date.now();
      }
      return json(200, device);
    }
    if (path === `${base}/device` && method === 'GET') {
      if (device.state === 'waiting') {
        began ||= Date.now();
        if (deviceMode !== 'hold' && Date.now() - began >= 1500) {
          if (deviceMode === 'fail') device = { state: 'failed', error: 'the code expired before it was approved on GitHub' };
          else if (deviceMode === 'keychain' && !device.notice) {
            device = { ...WAITING, user_code: 'K7Q3-2M8P', notice: 'This server has no system keychain, so Copilot will keep the sign-in in its own settings folder, readable only by this user. Approve this new code to finish.' };
            began = Date.now();
          }
          else {
            const refused = signInAs({ ...SIGNED_IN, stored: true });
            device = refused ? { state: 'failed', error: refused } : { state: 'signed_in', account: view() };
          }
        }
      }
      return json(200, device);
    }
    if (path === `${base}/device` && method === 'DELETE') {
      if (device.state === 'waiting') device = { state: 'canceled' };
      return json(204);
    }
    if (path === `${base}/sign-out` && method === 'POST') {
      if (account.env_var) return fail(409, `${account.env_var} in the service environment takes precedence over a sign-in made here; change or remove it where the service starts`);
      if (account.signed_in && account.source !== 'stored') return fail(400, 'only a sign-in stored by Copilot can be signed out here');
      account = SIGNED_OUT;
      sync();
      return json(200, view());
    }
    if (path === `${base}/link` && method === 'DELETE') {
      // An environment token or a gh sign-in cannot be signed out here: the next read links it again.
      if (account.source === 'stored') account = SIGNED_OUT;
      linked = undefined;
      sync();
      return json(200, view());
    }
    return fail(404, 'not found');
  }
  return { route, refusal };
}
