// The in-browser fake's Copilot sign-in (docs/web.md, GitHub Copilot sign-in). `?copilot=out` starts signed out,
// `?copilot=env` with a token in GH_TOKEN, `?copilot=gh` through the GitHub CLI; otherwise a stored sign-in.
// A token starting github_pat_ signs in; any other is refused the way GitHub refuses a bad one.
// Sign in with GitHub shows a code; a read 1.5 s after it started signs in. `?device=off` lacks the capability,
// `?device=waiting` has a sign-in waiting already, `?device=fail` fails it and `?device=hold` never finishes it.

import type { DeviceSignIn, Meta, ProviderAccount } from '../api';

type Json = Record<string, unknown>;

function json(status: number, body?: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

const fail = (status: number, error: string, code?: string) => json(status, code ? { error, code } : { error });

const SIGNED_IN: ProviderAccount = { signed_in: true, login: 'octocat', host: 'https://github.com', source: 'stored', message: 'octocat' };
const REASON = 'GitHub Copilot is signed out. Sign in in Settings.';

function initial(mode: string | null): ProviderAccount {
  if (mode === 'out') return { signed_in: false, message: 'Not authenticated' };
  if (mode === 'env') return { ...SIGNED_IN, source: 'env', env_var: 'GH_TOKEN', message: 'octocat (via GH_TOKEN)' };
  if (mode === 'gh') return { ...SIGNED_IN, source: 'gh-cli', message: 'octocat (via gh)' };
  return SIGNED_IN;
}

/** Routes /api/providers/copilot/account*, keeping `meta`'s Copilot entry in step; null for any other path. */
export function accountMock(meta: Meta) {
  const query = new URLSearchParams(window.location.search);
  let account = initial(query.get('copilot'));
  const deviceMode = query.get('device');
  const WAITING: DeviceSignIn = { state: 'waiting', verification_uri: 'https://github.com/login/device', user_code: 'B4F2-9C1D' };
  let device: DeviceSignIn = deviceMode === 'waiting' ? WAITING : { state: 'idle' };
  // When the waiting sign-in started; one waiting at load starts at its first read.
  let began = 0;
  const copilot = meta.providers.find((p) => p.name === 'copilot');
  if (copilot) copilot.capabilities = { ...copilot.capabilities, account: true, device_sign_in: deviceMode !== 'off' };
  function sync() {
    if (!copilot) return;
    copilot.available = account.signed_in;
    copilot.signed_out = !account.signed_in || undefined;
    copilot.reason = account.signed_in ? undefined : REASON;
  }
  sync();
  /** The refusal a create or send gets while signed out, or null. */
  const refusal = () => (account.signed_in ? null : fail(409, REASON, 'provider_signed_out'));
  function route(method: string, path: string, body: Json): Response | null {
    const base = '/api/providers/copilot/account';
    if (!path.startsWith(base)) return null;
    if (path === base && method === 'GET') return json(200, account);
    if (path === `${base}/sign-in` && method === 'POST') {
      if (account.env_var) return fail(409, `${account.env_var} in the service environment takes precedence over a sign-in made here; change or remove it where the service starts`);
      const token = String(body.token ?? '').trim();
      if (!token) return fail(400, 'enter a token');
      if (!token.startsWith('github_pat_')) return fail(400, 'Failed to fetch Copilot user info: 401 Unauthorized: {"message":"Bad credentials"}');
      account = { ...SIGNED_IN, stored: true };
      sync();
      return json(200, account);
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
          else {
            account = { ...SIGNED_IN, stored: true };
            sync();
            device = { state: 'signed_in', account };
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
      account = { signed_in: false, message: 'Not authenticated' };
      sync();
      return json(200, account);
    }
    return fail(404, 'not found');
  }
  return { route, refusal };
}
