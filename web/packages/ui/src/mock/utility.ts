// Development-only Background AI for the in-browser mock service (see install.ts): a Utility log over a few days,
// with today's limit reached, served by GET /api/utility in pages like the service's.

import type { UtilityCall, UtilityDay, UtilityLog } from '../api';

const pad = (n: number) => String(n).padStart(2, '0');

/** The browser's local date, standing in for the server's. */
export const localDay = (d: Date) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;

/** A timestamp with the local offset, as the service writes it. */
function localStamp(d: Date): string {
  const off = -d.getTimezoneOffset();
  const sign = off < 0 ? '-' : '+';
  return `${localDay(d)}T${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}${sign}${pad(Math.floor(Math.abs(off) / 60))}:${pad(Math.abs(off) % 60)}`;
}

/** Today's calls up to `limit` and two skipped past it, a busy yesterday with a failure, and a quiet day before; oldest first. */
export function seedUtility(limit: number): UtilityCall[] {
  const out: UtilityCall[] = [];
  const start = new Date();
  start.setHours(0, 0, 0, 0);
  const tasks = ['t1', 't2', 't3', 't4', 't5'];
  const purposes = ['title', 'subagent-summary', 'subagent-summary', 'title', 'planner-triage'];
  const day = (back: number, count: number, extra: (c: UtilityCall, i: number) => void) => {
    const base = new Date(start);
    base.setDate(base.getDate() - back);
    for (let i = 0; i < count; i++) {
      const at = new Date(base.getTime() + (8 * 60 + i * 9) * 60000);
      const purpose = purposes[i % purposes.length];
      const prompt = purpose === 'title' ? 40 + ((i * 37) % 300) : 900 + ((i * 211) % 3000);
      const reply = purpose === 'title' ? 24 + (i % 20) : 110 + (i % 50);
      const c: UtilityCall = {
        id: 0, at: localStamp(at), day: localDay(at), purpose, provider: 'copilot', model: 'gpt-6-luna',
        ...(purpose === 'planner-triage' ? { project_id: 'p1' } : { task_id: tasks[i % tasks.length], project_id: 'p1' }),
        prompt_chars: prompt, reply_chars: reply, input_tokens: 180 + Math.ceil(prompt / 4), output_tokens: Math.ceil(reply / 4), credits: 0.0012 + (i % 4) * 0.0003,
        duration_ms: 700 + ((i * 131) % 2400), outcome: 'ok',
      };
      extra(c, i);
      out.push(c);
    }
  };
  day(3, 12, () => {});
  day(1, 31, (c, i) => {
    if (i === 7) Object.assign(c, { outcome: 'error', reason: 'copilot title: model request timed out', reply_chars: 0, output_tokens: 0, credits: 0 });
    if (i % 6 === 5) Object.assign(c, { estimated: true, input_tokens: Math.ceil(c.prompt_chars / 4), credits: undefined });
  });
  day(0, limit + 2, (c, i) => {
    // A title made with the Task's own model, no Utility model being set.
    if (c.purpose === 'title' && i === limit - 2) c.session_model = true;
    if (i >= limit) Object.assign(c, { outcome: 'skipped', reason: 'daily_limit', prompt_chars: 0, reply_chars: 0, input_tokens: 0, output_tokens: 0, credits: undefined, duration_ms: 0, model: 'gpt-6-luna' });
  });
  out.forEach((c, i) => (c.id = i + 1));
  return out;
}

/** GET /api/utility over `calls` (oldest first) with `limit` in force, `page` calls a page. */
export function utilityLog(calls: UtilityCall[], limit: number, before: number, page = 50): UtilityLog {
  const now = new Date();
  const today = localDay(now);
  const reset = new Date(now);
  reset.setHours(24, 0, 0, 0);
  const days = new Map<string, UtilityDay>();
  for (const c of calls) {
    const d = days.get(c.day) ?? { day: c.day, calls: 0, errors: 0, skipped: 0, prompt_chars: 0, reply_chars: 0, input_tokens: 0, output_tokens: 0 };
    days.set(c.day, d);
    if (c.outcome === 'skipped') {
      d.skipped++;
      continue;
    }
    if (c.outcome === 'error') d.errors++;
    d.calls++;
    d.prompt_chars += c.prompt_chars;
    d.reply_chars += c.reply_chars;
    d.input_tokens += c.input_tokens;
    d.output_tokens += c.output_tokens;
    d.estimated ||= c.estimated;
    d.credits = (d.credits ?? 0) + (c.credits ?? 0);
  }
  const used = days.get(today)?.calls ?? 0;
  const newest = [...calls].reverse().filter((c) => !before || c.id < before);
  const shown = newest.slice(0, page);
  return {
    today: { day: today, calls: used, limit, paused: used >= limit, resets_at: reset.toISOString() },
    days: [...days.values()].sort((a, b) => b.day.localeCompare(a.day)),
    calls: shown,
    ...(newest.length > page ? { next: shown[shown.length - 1].id } : {}),
  };
}
