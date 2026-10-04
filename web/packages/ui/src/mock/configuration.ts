import type { Configuration, ConfigurationFile, ConfigurationKind } from '../api';

/** Native configuration fixtures for the development preview. Nothing reaches the filesystem. */
export function configurationMock(terminal: () => boolean) {
  const scopes = new Map<string, Configuration>();
  let revision = 1;
  const json = (status: number, body: unknown) => Response.json(body, { status });
  function scope(projectId: string): Configuration {
    let data = scopes.get(projectId);
    if (!data) {
      const root = projectId ? `/projects/${projectId}/.github` : '/home/demo/.copilot';
      const file = (name: string, path: string, content: string): ConfigurationFile => ({ name, path: `${root}/${path}`, content, revision: content ? String(revision++) : '', editable: true });
      data = { scope: projectId ? 'project' : 'global', project_id: projectId || undefined, terminal_allowed: terminal(), agents: [], hooks: [], skills: [], instructions: file('copilot-instructions', 'copilot-instructions.md', '') };
      data.instruction_files = [data.instructions];
      if (projectId) data.instruction_files.push({ ...file('agents', 'AGENTS.md', ''), path: `/projects/${projectId}/AGENTS.md` });
      if (!projectId) {
        data.agents.push(file('reviewer', 'agents/reviewer.agent.md', '---\nname: reviewer\ndescription: Review changes\ncustom-field: keep-this\n---\n\nReview correctness and tests.\n'));
        data.skills.push(file('release-check', 'skills/release-check/SKILL.md', '---\nname: release-check\ndescription: Check a release\n---\n\nInspect the release checklist.\n'));
        data.hooks.push(file('audit', 'hooks/audit.json', JSON.stringify({ version: 1, hooks: { postToolUse: [{ type: 'command', bash: 'logger "tool finished"', timeoutSec: 10 }] } }, null, 2)));
      }
      scopes.set(projectId, data);
    }
    data.terminal_allowed = terminal();
    return data;
  }
  return { route(method: string, url: URL, body: Record<string, unknown>): Response | undefined {
    if (!url.pathname.startsWith('/api/configuration')) return;
    const data = scope(url.searchParams.get('project_id') ?? '');
    if (url.pathname === '/api/configuration' && method === 'GET') {
      data.conflicts = [];
      for (const kind of ['agents', 'skills'] as const) {
        const groups = new Map<string, Set<string>>();
        for (const file of [...data[kind], ...(data.project_id ? scope('')[kind] : [])]) {
          if (file.disabled || file.error || !file.revision) continue;
          const paths = groups.get(file.name) ?? new Set<string>();
          paths.add(file.path); groups.set(file.name, paths);
        }
        for (const [name, paths] of groups) if (paths.size > 1) data.conflicts.push({ kind, name, paths: [...paths].sort() });
      }
      return json(200, data);
    }
    const draft = /^\/api\/configuration\/(agents|skills|hooks)\/draft$/.exec(url.pathname);
    if (draft && method === 'POST') {
      if (!terminal()) return json(403, { error: 'Terminal must be enabled to generate a configuration draft' });
      if (!String(body.brief ?? '').trim()) return json(400, { error: 'Describe the draft you want to create' });
      const source = { provider: 'copilot', utility_model: 'gpt-5-mini', similar_skills: scope('').skills.slice(0, 1).map((skill) => ({ name: skill.name, path: skill.path, reason: 'This installed checklist covers reviewing a change before release.' })) };
      if (draft[1] === 'hooks') return json(200, { ...source, name: 'tool-audit', event: 'postToolUse', bash: 'logger "tool finished"', timeout_sec: 10, env: { LOG_LEVEL: 'info' } });
      return json(200, { ...source, name: draft[1] === 'agents' ? 'focused-reviewer' : 'review-checklist', description: 'Review a focused change', prompt: 'Inspect the requested change and its tests. Report concrete findings.', ...(/manual.only/i.test(String(body.brief)) ? { disable_model_invocation: true, user_invocable: true } : {}), ...(draft[1] === 'agents' ? { model: 'gpt-5-mini', tools: ['read', 'search'] } : {}) });
    }
    if (method === 'POST' && /\/skills\/(list|install)$/.test(url.pathname)) {
      if (!terminal()) return json(403, { error: 'Terminal must be enabled to run npx skills' });
      if (String(body.source).includes('unavailable')) return json(502, { error: 'Could not read the repository. Check the URL and try again.' });
      if (url.pathname.endsWith('/list')) return json(200, { output: 'Available skills:\n  web-design-guidelines\n  react-best-practices\n' });
      const names = body.skills as string[];
      if (!names?.length) return json(400, { error: 'Choose at least one skill' });
      if (names.some((name) => data.skills.some((file) => file.name === name))) return json(409, { error: 'A selected skill already exists' });
      for (const name of names) data.skills.push({ name, path: data.instructions.path.replace('copilot-instructions.md', `skills/${name}/SKILL.md`), content: `---\nname: ${name}\ndescription: Installed skill\n---\n\nMock installed instructions.\n`, revision: String(revision++), editable: true });
      return json(200, { output: `Installed ${names.join(', ')}.`, installed: names });
    }
    const match = /^\/api\/configuration\/(agents|skills|hooks|instructions)\/([^/]+)$/.exec(url.pathname);
    if (!match) return json(404, { error: 'Unknown configuration route' });
    const kind = match[1] as ConfigurationKind;
    const name = decodeURIComponent(match[2]);
    const files = kind === 'instructions' ? data.instruction_files ?? [data.instructions] : data[kind];
    const existing = files.find((file) => file.name === name && (body.path ? file.path === body.path : !file.disabled));
    if (body.path && !body.revision) return json(409, { error: 'An existing file path requires its saved revision.' });
    if (body.path && !existing) return json(409, { error: 'The selected file is no longer available in this scope. Reload the configuration.' });
    if (existing && !existing.editable) return json(403, { error: existing.read_only_reason || 'This file is read only.' });
    if (kind === 'instructions' && !existing) return json(404, { error: 'Unknown instruction file for this scope' });
    if (kind !== 'instructions' && !terminal()) return json(403, { error: 'Terminal must be enabled to manage this file' });
    if ((existing?.revision ?? '') !== body.revision) return json(409, { error: 'The file changed since it was loaded. Reload the saved version before editing again.' });
    if (method === 'PUT' && 'disabled' in body) {
      if (kind === 'instructions' || typeof body.disabled !== 'boolean') return json(400, { error: 'Only agents, skills and hooks can be enabled or disabled.' });
      if (!body.path || !body.revision || !existing) return json(409, { error: 'An existing file path and revision are required.' });
      if (!!existing.disabled === body.disabled) return json(200, existing);
      const path = body.disabled ? existing.path + '.uam-disabled' : existing.path.replace(/\.uam-disabled$/, '');
      if (files.some((file) => file.path === path)) return json(409, { error: 'The destination already exists. Remove or rename it before changing this file.' });
      const file = { ...existing, path, disabled: body.disabled };
      data[kind] = files.map((entry) => entry === existing ? file : entry);
      return json(200, file);
    }
    if (method === 'DELETE') {
      if (kind === 'instructions') {
        const cleared = { ...existing!, content: '', revision: '' };
        data.instruction_files = files.map((file) => file === existing ? cleared : file);
        if (name === 'copilot-instructions') data.instructions = cleared;
      }
      else data[kind] = files.filter((file) => file !== existing);
      return new Response(null, { status: 204 });
    }
    if (method === 'PUT') {
      if (existing?.disabled) return json(409, { error: 'Enable this file before editing it.' });
      if (kind === 'hooks') {
        try { JSON.parse(String(body.content)); } catch { return json(400, { error: 'Invalid hook JSON' }); }
      }
      const suffix = kind === 'agents' ? `agents/${name}.agent.md` : kind === 'skills' ? `skills/${name}/SKILL.md` : kind === 'hooks' ? `hooks/${name}.json` : 'copilot-instructions.md';
      const file = { name, path: existing?.path ?? data.instructions.path.replace('copilot-instructions.md', suffix), content: String(body.content), revision: String(revision++), editable: true };
      if (kind === 'instructions') {
        data.instruction_files = files.map((entry) => entry === existing ? file : entry);
        if (name === 'copilot-instructions') data.instructions = file;
      }
      else if (existing) data[kind] = files.map((item) => item === existing ? file : item);
      else data[kind].push(file);
      return json(200, file);
    }
    return json(405, { error: 'Method not allowed' });
  } };
}
