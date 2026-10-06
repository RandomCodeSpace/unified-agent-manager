// The in-browser fake's Copilot CLI version and update (Settings → Providers). By default the newest release is
// installed; `?cli=outdated` runs 1.0.89 with 1.0.92 out, `?cli=manual` cannot update here, `?cli=incompatible` has
// a newest release the SDK refused, `?cli=fail` ends an update failed, `?cli=hold` never finishes one and `?cli=busy`
// ends one with a Task still working, so 1.0.89 keeps running until it restarts. A read 1 s after an update started
// ends it.

import type { Meta, ProviderCLI } from '../api';

function json(status: number, body?: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

const fail = (status: number, error: string) => json(status, { error });

function initial(mode: string | null): ProviderCLI {
  const checked_at = new Date(Date.now() - 5 * 60_000).toISOString();
  const outdated: ProviderCLI = { installed: '1.0.89', latest: '1.0.92', update_available: true, checked_at, state: 'idle' };
  switch (mode) {
    case 'outdated':
    case 'fail':
    case 'hold':
    case 'busy':
      return outdated;
    case 'manual':
      return { ...outdated, update_available: false, manual: 'copilot at /usr/local/bin/copilot is not an npm global install; update it the way it was installed' };
    case 'incompatible':
      return { installed: '1.0.92', latest: '1.0.95', incompatible: '1.0.95', update_available: false, checked_at, state: 'idle' };
    default:
      return { installed: '1.0.92', latest: '1.0.92', update_available: false, checked_at, state: 'idle' };
  }
}

/** Routes /api/providers/copilot/cli*, keeping `meta`'s Copilot entry in step; null for any other path. */
export function cliMock(meta: Meta) {
  const mode = new URLSearchParams(window.location.search).get('cli');
  let cli = initial(mode);
  let began = 0;
  const copilot = meta.providers.find((p) => p.name === 'copilot');
  if (copilot) copilot.capabilities = { ...copilot.capabilities, cli_update: true };
  function sync() {
    if (copilot) copilot.cli_update = cli.update_available ? cli.latest : undefined;
  }
  sync();
  return (method: string, url: URL): Response | null => {
    const base = '/api/providers/copilot/cli';
    const path = url.pathname;
    if (path !== base && !path.startsWith(`${base}/`)) return null;
    if (path === base && method === 'GET') {
      if (cli.state === 'updating' && mode !== 'hold' && Date.now() - began >= 1000) {
        if (mode === 'fail') cli = { ...cli, state: 'failed', error: `npm install -g @github/copilot@${cli.target} failed: EACCES: permission denied` };
        else {
          cli = { ...cli, installed: cli.target, running: mode === 'busy' ? cli.installed : undefined, update_available: false, state: 'updated' };
          sync();
        }
      }
      if (url.searchParams.get('refresh') === '1') cli = { ...cli, checked_at: new Date().toISOString() };
      return json(200, cli);
    }
    if (path === `${base}/update` && method === 'POST') {
      if (cli.state === 'updating') return json(200, cli);
      if (cli.manual) return fail(409, cli.manual);
      if (!cli.update_available) return fail(409, 'no Copilot CLI update is available');
      cli = { ...cli, state: 'updating', target: cli.latest, error: undefined };
      began = Date.now();
      return json(200, cli);
    }
    return fail(404, 'not found');
  };
}
