// The in-browser fake's UAM version and restart (Settings → General → UAM). By default the running version is the one
// installed; `?uam=installed` has a newer one installed, `?uam=busy` has one too while a Task works (a restart waits),
// and `?uam=error` cannot read the installed binary. A restart never comes back: the fake stays restarting.

import type { Meta, ServiceStatus } from '../api';

function json(status: number, body?: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

function initial(mode: string | null, running: string): ServiceStatus {
  switch (mode) {
    case 'installed':
    case 'busy':
      return { running, installed: 'v0.16.0', restart: 'available' };
    case 'error':
      return { running, restart: 'none', error: 'run uam version: exit status 2' };
    default:
      return { running, installed: running, restart: 'none' };
  }
}

/** Routes /api/service*, keeping `meta.service` in step; null for any other path. */
export function serviceMock(meta: Meta) {
  const mode = new URLSearchParams(window.location.search).get('uam');
  let status = initial(mode, meta.version);
  function sync() {
    meta.service = { ...status };
  }
  sync();
  return (method: string, url: URL): Response | null => {
    const path = url.pathname;
    if (path === '/api/service' && method === 'GET') return json(200, status);
    if (path === '/api/service/restart' && method === 'POST') {
      if (status.restart === 'none') return json(409, { error: status.error ? `cannot restart: ${status.error}` : `uam ${status.running} is the version installed; there is nothing to restart onto` });
      if (status.restart === 'available') status = { ...status, restart: mode === 'busy' ? 'pending' : 'restarting' };
      sync();
      return json(200, status);
    }
    return null;
  };
}
