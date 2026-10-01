// Development-only: the chart routes of the in-browser mock service (see install.ts). One Task,
// t-chart, drew two charts with uam_chart; the first Project has two of them pinned.

import type { Chart, PinnedChart, Project } from '../api';

const ago = (min: number) => new Date(Date.now() - min * 60_000).toISOString();

const days = Array.from({ length: 30 }, (_, i) => `09-${String(i + 1).padStart(2, '0')}`);
const commits = [3, 5, 2, 0, 0, 7, 4, 6, 3, 2, 0, 1, 8, 5, 4, 3, 6, 0, 0, 9, 4, 5, 3, 2, 6, 0, 1, 4, 7, 5];

export const MOCK_CHARTS: Record<string, Chart> = {
  'call-chart-1': {
    title: 'Commits per day, September',
    kind: 'line',
    x_label: 'Day',
    y_label: 'Commits',
    command: "git log --since=2026-09-01 --until=2026-10-01 --date=format:%m-%d --format=%ad | sort | uniq -c | awk 'BEGIN{print \"day,commits\"}{print $2\",\"$1}'",
    format: 'csv',
    x: 'day',
    y: ['commits'],
    labels: days,
    series: [{ name: 'commits', values: commits }],
    at: ago(12),
  },
  'call-chart-2': {
    title: 'Lines of code per file type',
    kind: 'bar',
    x_label: 'File type',
    y_label: 'Lines',
    command: "git ls-files | sed -n 's/.*\\.//p' | sort | uniq -c",
    format: 'csv',
    x: 'type',
    y: ['lines'],
    labels: ['go', 'tsx', 'ts', 'md', 'css', 'yml'],
    series: [{ name: 'lines', values: [41280, 18950, 9120, 4310, 1180, 640] }],
    at: ago(11),
  },
};

export function chartMock(projects: Project[], onProject: (p: Project) => void) {
  const pins: Record<string, PinnedChart[]> = {
    p1: [
      { ...MOCK_CHARTS['call-chart-1'], id: 'pin-1', project_id: 'p1', pinned_at: ago(10), at: ago(10) },
      {
        title: 'Open pull requests', kind: 'bar', command: 'gh pr list --state open --json number,createdAt', format: 'json', x: 'week', y: ['open'],
        labels: ['W33', 'W34', 'W35', 'W36', 'W37', 'W38', 'W39', 'W40'], series: [{ name: 'open', values: [3, 5, 4, 6, 7, 5, 4, 4] }],
        at: ago(190), id: 'pin-2', project_id: 'p1', pinned_at: ago(60 * 30),
      },
    ],
  };
  const pinnedBy: Record<string, string> = { 'call-chart-1': 'pin-1' };
  const count = (id: string) => {
    const p = projects.find((x) => x.id === id);
    if (!p) return;
    p.charts = pins[id]?.length || undefined;
    onProject(p);
  };
  for (const p of projects) p.charts = pins[p.id]?.length || undefined;
  const reply = (status: number, body?: unknown) =>
    body === undefined ? new Response(null, { status }) : new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
  const last: Record<string, number> = {};

  return function route(method: string, url: URL, body: Record<string, unknown>): Response | null {
    let r: RegExpMatchArray | null;
    if ((r = url.pathname.match(/^\/api\/sessions\/([^/]+)\/chart$/)) && method === 'GET') {
      const call = url.searchParams.get('call') ?? '';
      const chart = MOCK_CHARTS[call];
      return chart ? reply(200, { ...chart, pinned_id: pinnedBy[call] }) : reply(404, { error: 'chart not found' });
    }
    if ((r = url.pathname.match(/^\/api\/sessions\/([^/]+)\/chart\/pin$/)) && method === 'POST') {
      const call = String(body.call_id ?? '');
      const chart = MOCK_CHARTS[call];
      if (!chart) return reply(404, { error: 'chart not found' });
      const list = (pins.p1 ??= []);
      const pin: PinnedChart = { ...chart, id: `pin-${call}`, project_id: 'p1', pinned_at: new Date().toISOString() };
      if (!pinnedBy[call]) list.push(pin);
      pinnedBy[call] = pin.id;
      count('p1');
      return reply(200, pin);
    }
    if ((r = url.pathname.match(/^\/api\/projects\/([^/]+)\/charts$/)) && method === 'GET') return reply(200, { charts: pins[decodeURIComponent(r[1])] ?? [] });
    if ((r = url.pathname.match(/^\/api\/projects\/([^/]+)\/charts\/([^/]+)\/refresh$/)) && method === 'POST') {
      const list = pins[decodeURIComponent(r[1])] ?? [];
      const i = list.findIndex((c) => c.id === decodeURIComponent(r![2]));
      if (i < 0) return reply(404, { error: 'chart not found' });
      const c = list[i];
      if (body.auto) return reply(200, c);
      if (Date.now() - (last[c.id] ?? 0) < 60_000) return reply(429, { error: 'this chart refreshed less than a minute ago; try again in 60s' });
      last[c.id] = Date.now();
      const values = c.series[0].values;
      list[i] = { ...c, at: new Date().toISOString(), series: [{ ...c.series[0], values: [...values.slice(1), Math.max(0, values.at(-1)! + 1)] }, ...c.series.slice(1)] };
      return reply(200, list[i]);
    }
    if ((r = url.pathname.match(/^\/api\/projects\/([^/]+)\/charts\/([^/]+)$/)) && method === 'DELETE') {
      const pid = decodeURIComponent(r[1]);
      pins[pid] = (pins[pid] ?? []).filter((c) => c.id !== decodeURIComponent(r![2]));
      for (const [call, id] of Object.entries(pinnedBy)) if (id === decodeURIComponent(r[2])) delete pinnedBy[call];
      count(pid);
      return reply(204);
    }
    return null;
  };
}
