// The in-browser fake's routines (docs/web.md, Routines): an in-memory list per Project with
// Run now, pause, edit and delete. Runs fire only through Run now; each is skipped while the
// previous one runs, and finishes a few seconds later.

import type { Routine, RoutineInput, RoutineRun } from '../api';

type Json = Record<string, unknown>;

const ago = (min: number) => new Date(Date.now() - min * 60_000).toISOString();
const ahead = (min: number) => new Date(Date.now() + min * 60_000).toISOString();

function json(status: number, body?: unknown): Response {
  if (body === undefined) return new Response(null, { status });
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

const fail = (status: number, error: string) => json(status, { error });

function seedRoutines(): Routine[] {
  const run = (id: string, minAgo: number, outcome: RoutineRun['outcome'], extra: Partial<RoutineRun> = {}): RoutineRun => ({ id, at: ago(minAgo), trigger: 'schedule', outcome, ended_at: outcome === 'running' ? undefined : ago(minAgo - 4), ...extra });
  return [
    {
      id: 'r1',
      project_id: 'p1',
      provider: 'copilot',
      name: 'Dependency check',
      prompt: 'Check go.mod and web/package.json for dependency updates. List each update with its changelog highlights and open no pull request.',
      model: 'gpt-5-mini',
      schedule: { kind: 'weekdays', time: '09:00' },
      enabled: true,
      mode: 'safe',
      max_runs_per_day: 2,
      max_minutes: 30,
      created_at: ago(60 * 24 * 9),
      next_run: ahead(60 * 15 + 12),
      runs: [
        run('rr4', 60 * 3, 'finished', { task_id: 't4' }),
        run('rr3', 60 * 27, 'time_limit', { task_id: 't2', reason: 'cancelled after the limit of 30 minutes' }),
        run('rr2', 60 * 51, 'skipped', { reason: 'still running', trigger: 'manual' }),
        run('rr1', 60 * 75, 'failed', { reason: 'the project directory /home/user/projects/unified-agent-manager no longer exists', trigger: 'missed' }),
      ],
    },
    {
      id: 'r2',
      project_id: 'p1',
      provider: 'copilot',
      name: 'Flaky test sweep',
      prompt: 'Run go test -count=3 ./internal/... and report the tests that failed at least once, with the failure output.',
      model: 'auto',
      schedule: { kind: 'hours', hours: 6 },
      enabled: false,
      mode: 'yolo',
      max_runs_per_day: 4,
      max_minutes: 20,
      created_at: ago(60 * 24 * 2),
      runs: [],
    },
    {
      id: 'r3',
      project_id: 'p3',
      provider: 'copilot',
      name: 'Broken link check',
      prompt: 'Build the site and list every broken internal link with the page it is on.',
      model: 'gpt-5-mini',
      schedule: { kind: 'weekly', time: '07:30', weekday: 1 },
      enabled: true,
      mode: 'safe',
      max_runs_per_day: 1,
      max_minutes: 15,
      created_at: ago(60 * 24),
      next_run: ahead(60 * 24 * 3),
      runs: [],
    },
  ];
}

export function routinesMock(projectExists: (id: string) => boolean) {
  let routines = seedRoutines();
  let counter = 0;
  const nextId = (prefix: string) => `${prefix}${++counter}`;
  const find = (id: string) => routines.find((r) => r.id === id);

  function route(method: string, path: string, body: Json): Response | null {
    let r: RegExpMatchArray | null;
    if (path === '/api/routines' && method === 'GET') return json(200, { routines: routines.filter((x) => projectExists(x.project_id)) });
    if ((r = path.match(/^\/api\/projects\/([^/]+)\/routines$/))) {
      const project = decodeURIComponent(r[1]);
      if (!projectExists(project)) return fail(404, 'project not found');
      if (method === 'GET') return json(200, { routines: routines.filter((x) => x.project_id === project) });
      if (method === 'POST') {
        const input = body as unknown as RoutineInput;
        if (!input.name?.trim() || !input.prompt?.trim()) return fail(400, 'name, prompt and schedule are required');
        const created: Routine = { ...input, id: nextId('r-new-'), project_id: project, provider: 'copilot', created_at: new Date().toISOString(), next_run: input.enabled ? ahead(60) : undefined, runs: [] };
        routines = [...routines, created];
        return json(201, created);
      }
    }
    if ((r = path.match(/^\/api\/routines\/([^/]+)(\/run)?$/))) {
      const routine = find(decodeURIComponent(r[1]));
      if (!routine) return fail(404, 'routine not found');
      if (r[2] && method === 'POST') {
        const running = routine.runs[0]?.outcome === 'running';
        const run: RoutineRun = running
          ? { id: nextId('rr-'), at: new Date().toISOString(), trigger: 'manual', outcome: 'skipped', reason: 'still running', ended_at: new Date().toISOString() }
          : { id: nextId('rr-'), at: new Date().toISOString(), trigger: 'manual', outcome: 'running' };
        routine.runs = [run, ...routine.runs];
        if (!running) {
          setTimeout(() => {
            run.outcome = 'finished';
            run.ended_at = new Date().toISOString();
          }, 8000);
        }
        return json(202, routine);
      }
      if (method === 'PATCH') {
        Object.assign(routine, body);
        routine.next_run = routine.enabled ? routine.next_run ?? ahead(60) : undefined;
        return json(200, routine);
      }
      if (method === 'DELETE') {
        routines = routines.filter((x) => x !== routine);
        return json(204);
      }
    }
    return null;
  }
  return { route };
}
