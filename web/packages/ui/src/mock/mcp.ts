// The in-browser fake's MCP servers (docs/web.md, MCP servers): a user-wide list with a
// remote server, a command server and a built-in one, and each Task's view of them. A
// command server needs Terminal on; values are never returned. Tasks start the built-in
// GitHub server only while Settings turns it on, as the service does. Signing in to "tracker"
// accepts any pasted 127.0.0.1 address with the state the fake issued.

import type { McpServer, McpServerInput, McpStatus } from '../api';

type Json = Record<string, unknown>;

function json(status: number, body?: unknown): Response {
  if (body === undefined) return new Response(null, { status });
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

const fail = (status: number, error: string) => json(status, { error });

interface Stored extends Omit<McpServer, 'env' | 'headers'> {
  env: Record<string, string>;
  headers: Record<string, string>;
}

function seed(): Stored[] {
  return [
    { name: 'docs-search', type: 'http', url: 'https://mcp.example.com/docs', env: {}, headers: { Authorization: 'x' }, enabled: true, source: 'user' },
    { name: 'echo', type: 'stdio', command: '/opt/mcp/echo-server', args: ['--stdio'], env: { ECHO_TOKEN: 'x' }, headers: {}, enabled: true, source: 'user' },
    { name: 'tracker', type: 'http', url: 'https://tracker.example.com/mcp', env: {}, headers: {}, enabled: true, source: 'user' },
    { name: 'github-mcp-server', type: 'http', env: {}, headers: {}, enabled: true, source: 'builtin' },
  ];
}

const view = (s: Stored): McpServer => ({
  ...s,
  env: Object.keys(s.env).sort().map((key) => ({ key, set: true })),
  headers: Object.keys(s.headers).sort().map((key) => ({ key, set: true })),
});

export function mcpMock(terminal: () => boolean, githubMcp: () => boolean) {
  let servers = seed();
  // Per Task: servers turned off or on in it, and whether tracker has signed in.
  const off = new Map<string, Set<string>>();
  const on = new Map<string, Set<string>>();
  let signedIn = false;
  let pending: { task: string; state: string } | null = null;

  // Like Copilot CLI 1.0.93's discovery, the instance's list leaves the built-in GitHub server out; tasks have it.
  const list = () => json(200, { available: true, stdio_allowed: terminal(), servers: servers.filter((s) => s.name !== 'github-mcp-server').map(view) });

  function status(task: string): McpStatus[] {
    const disabled = off.get(task) ?? new Set();
    const enabled = on.get(task) ?? new Set();
    return servers
      .filter((s) => s.enabled)
      .map((s): McpStatus => {
        if (disabled.has(s.name) || (s.name === 'github-mcp-server' && !githubMcp() && !enabled.has(s.name))) return { name: s.name, status: 'disabled', source: s.source };
        if (s.name === 'echo') return { name: s.name, status: 'failed', error: 'failed to spawn MCP server process: No such file or directory (os error 2)' };
        if (s.name === 'tracker' && !signedIn) return { name: s.name, status: 'needs-auth', remote: true };
        const tools =
          s.name === 'github-mcp-server'
            ? [{ name: 'search_code', description: 'Search code across repositories.' }, { name: 'get_file_contents', description: 'Get the contents of a file or directory.' }]
            : s.name === 'tracker'
              ? [{ name: 'list_issues', description: 'List open issues.' }]
              : [{ name: 'search', description: 'Search the documentation.' }, { name: 'fetch_page', description: 'Fetch one page as Markdown.' }];
        return { name: s.name, status: 'connected', source: s.source === 'user' ? undefined : s.source, remote: s.type !== 'stdio' && s.source === 'user', tools };
      });
  }

  function save(input: McpServerInput, existing?: Stored): Response {
    if (!input.name || !/^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$/.test(input.name)) return fail(400, 'a name is 1 to 64 letters, digits, . _ or -');
    if ((input.type === 'stdio' || existing?.type === 'stdio') && !terminal()) return fail(403, 'a server that runs a command needs Settings → Shell access → Terminal on; add a remote (HTTP or SSE) server instead');
    if (!existing && servers.some((s) => s.name === input.name)) return fail(409, `an MCP server named "${input.name}" already exists`);
    const merge = (rows: McpServerInput['env'], stored: Record<string, string>) => Object.fromEntries((rows ?? []).map((r) => [r.key, r.value ?? stored[r.key] ?? '']));
    const next: Stored = {
      name: input.name,
      type: input.type,
      command: input.command,
      args: input.args,
      cwd: input.cwd,
      url: input.url,
      env: merge(input.env, existing?.env ?? {}),
      headers: merge(input.headers, existing?.headers ?? {}),
      enabled: existing?.enabled ?? true,
      source: 'user',
    };
    servers = existing ? servers.map((s) => (s === existing ? next : s)) : [...servers, next];
    return list();
  }

  function route(method: string, path: string, body: Json): Response | null {
    let r: RegExpMatchArray | null;
    if (path === '/api/mcp' && method === 'GET') return list();
    if (path === '/api/mcp/servers' && method === 'POST') return save(body as unknown as McpServerInput);
    if ((r = path.match(/^\/api\/mcp\/servers\/([^/]+)$/))) {
      const s = servers.find((x) => x.name === decodeURIComponent(r![1]));
      if (!s) return fail(404, 'MCP server not found');
      if (method === 'PUT') return save({ ...(body as unknown as McpServerInput), name: s.name }, s);
      if (method === 'PATCH') {
        s.enabled = !!body.enabled;
        return list();
      }
      if (method === 'DELETE') {
        servers = servers.filter((x) => x !== s);
        return list();
      }
    }
    if ((r = path.match(/^\/api\/sessions\/([^/]+)\/mcp(?:\/(reconnect)|\/servers\/([^/]+)\/(tools|enable|disable|restart|sign-in|sign-in\/finish))?$/))) {
      const task = decodeURIComponent(r[1]);
      const name = r[3] ? decodeURIComponent(r[3]) : '';
      const action = r[2] ?? r[4];
      if (method === 'GET' && !action) return json(200, { servers: status(task) });
      if (method === 'GET' && action === 'tools') return json(200, { tools: status(task).find((s) => s.name === name)?.tools ?? [] });
      if (method !== 'POST') return null;
      if (action === 'reconnect') {
        off.delete(task);
        on.delete(task);
        return json(200, { servers: status(task) });
      }
      if (action === 'enable' || action === 'disable') {
        const set = off.get(task) ?? new Set<string>();
        const turnedOn = on.get(task) ?? new Set<string>();
        if (action === 'disable') {
          set.add(name);
          turnedOn.delete(name);
        } else {
          set.delete(name);
          turnedOn.add(name);
        }
        off.set(task, set);
        on.set(task, turnedOn);
        return json(200, { servers: status(task) });
      }
      if (action === 'restart') return fail(502, 'restart the MCP server: failed to spawn MCP server process: No such file or directory (os error 2)');
      if (action === 'sign-in') {
        if (body.again === false && signedIn) return json(200, {});
        pending = { task, state: 'mock-state' };
        return json(200, { url: 'https://tracker.example.com/authorize?redirect_uri=http%3A%2F%2F127.0.0.1%3A41234%2F&state=mock-state', relay: true });
      }
      if (action === 'sign-in/finish') {
        const pasted = String(body.url ?? '');
        if (!pending || pending.task !== task) return fail(409, 'no sign-in is waiting for this server; start it again');
        if (!/^http:\/\/(127\.0\.0\.1|localhost):41234\/\?/.test(pasted.trim())) return fail(400, "that address is not this sign-in's; paste the address the browser ended on after this sign-in");
        if (!pasted.includes('state=mock-state')) return fail(400, 'that address belongs to another sign-in; start the sign-in again');
        pending = null;
        signedIn = true;
        return json(204);
      }
    }
    return null;
  }
  return { route };
}
