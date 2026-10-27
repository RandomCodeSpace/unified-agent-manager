import { Check, Copy, RefreshCw } from 'lucide-react';
import { useCallback, useEffect, useState, type ReactNode } from 'react';
import { useApi } from '../ApiContext';
import { describeError, type SessionStats } from '../api';
import { useCopied } from '../lib/clipboard';
import { Note, Skeleton } from './common';
import { Button } from './ui/button';
import { Segmented } from './ui/segmented';

/** A key that may wrap after each `_` or `-` (wbr: copied text stays the key). */
function breakable(name: string) {
  return name.split(/(?<=[_-])/).flatMap((part, i) => (i ? [<wbr key={i} />, part] : [part]));
}

type Counts = Map<string, string>;
type Stat = SessionStats['stats'][number];

const num = (c: Counts, key: string) => Number(c.get(key)) || 0;
/** A group's `<name>` entries in the server's order, which is biggest first for ranked groups. */
const entries = (c: Counts, group: string) => [...c].filter(([k]) => k.startsWith(group + '.')).map(([k, v]) => [k.slice(group.length + 1), Number(v) || 0] as const);
const fmt = (v: number, digits = 0) => v.toLocaleString('en-US', { maximumFractionDigits: digits, minimumFractionDigits: digits });
const pct = (part: number, whole: number, digits = 1) => (whole ? `${fmt((part * 100) / whole, digits)}%` : '–');
const lines = (stats: SessionStats) =>
  [...stats.stats, ...(stats.recent ?? []).filter((s) => !s.key.startsWith('scan.')).map((s) => ({ key: `7d.${s.key}`, value: s.value }))].map((s) => `${s.key}=${s.value}\n`).join('');

/** The element's content width, for charts drawn in real pixels so their text never scales; fallback before the first measure. */
function useWidth(fallback: number) {
  const [width, setWidth] = useState(0);
  const ref = useCallback((el: HTMLElement | null) => {
    if (!el || typeof ResizeObserver === 'undefined') return;
    const ro = new ResizeObserver(([e]) => setWidth(Math.floor(e.contentRect.width)));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  return [ref, width || fallback] as const;
}

function Card({ title, children, className }: Readonly<{ title: string; children: ReactNode; className?: string }>) {
  return (
    <section aria-label={title} className={`flex min-w-0 flex-col gap-2 ${className ?? ''}`}>
      <h3 className="text-eyebrow uppercase text-muted">{title}</h3>
      {children}
    </section>
  );
}

const SANKEY_H = 180;

/** Tool calls flowing from each tool into succeeded and failed: the tools with a failure count, the rest as other tools. */
function ToolFlow({ c }: Readonly<{ c: Counts }>) {
  const [ref, w] = useWidth(545);
  const total = num(c, 'work.tool_calls');
  const failed = num(c, 'friction.tool_failures');
  const failures = new Map(entries(c, 'tool_failures'));
  const named = entries(c, 'tools').filter(([name]) => failures.has(name)).map(([name, calls]) => ({ name, calls, failed: failures.get(name)! }));
  const rest = { name: 'other tools', calls: total - named.reduce((a, t) => a + t.calls, 0), failed: failed - named.reduce((a, t) => a + t.failed, 0) };
  const tools = [...named, rest].filter((t) => t.calls > 0);
  if (!total) return <p className="text-caption text-muted">No tool calls.</p>;
  // Each node keeps an 18px slot so its label fits; the scale shares what is left.
  const gap = 6, slot = 18, lw = 172, rw = 112, nodeW = 8;
  const k0 = (SANKEY_H - gap * (tools.length - 1)) / total;
  const small = tools.filter((t) => t.calls * k0 < slot);
  const k = (SANKEY_H - gap * (tools.length - 1) - slot * small.length) / Math.max(1, total - small.reduce((a, t) => a + t.calls, 0));
  const xl = lw, xr = w - rw - nodeW, xm = (xl + nodeW + xr) / 2;
  const band = (y0: number, y1: number, t: number) => `M${xl + nodeW},${y0} C${xm},${y0} ${xm},${y1} ${xr},${y1} L${xr},${y1 + t} C${xm},${y1 + t} ${xm},${y0 + t} ${xl + nodeW},${y0 + t} Z`;
  const okH = (total - failed) * k, failY = okH + gap * 2;
  let y = 0, okAt = 0, failAt = failY;
  const left = tools.map((t) => {
    const h = Math.max(2, t.calls * k), top = y, okT = (t.calls - t.failed) * k, failT = t.failed ? Math.max(1, t.failed * k) : 0;
    y += Math.max(h, slot) + gap;
    const links = [band(top, okAt, okT), failT ? band(top + okT, failAt, failT) : ''];
    okAt += okT;
    failAt += failT;
    return { ...t, top, h, mid: top + Math.max(h, slot) / 2, links };
  });
  const failH = Math.max(2, failAt - failY);
  return (
    <div ref={ref} className="min-w-0">
      <svg width={w} height={Math.max(y, failY + failH + 30)} role="img" aria-label={`${fmt(total)} tool calls, ${fmt(failed)} failed`} className="block overflow-visible">
        {left.map((t) => (
          <g key={t.name}>
            <title>{`${t.name}: ${fmt(t.calls)} calls, ${fmt(t.failed)} failed (${pct(t.failed, t.calls)})`}</title>
            <path d={t.links[0]} className="fill-badge-blue/20" />
            {t.links[1] && <path d={t.links[1]} className="fill-error/55" />}
            <rect x={xl} y={t.top} width={nodeW} height={t.h} rx={2} className="fill-ink" />
            {t.h + gap >= 34 ? (
              <>
                <text x={xl - 8} y={t.mid - 3} textAnchor="end" className="fill-ink text-caption">{t.name}</text>
                <text x={xl - 8} y={t.mid + 12} textAnchor="end" className="fill-muted text-caption tabular-nums">{fmt(t.calls)} · {pct(t.failed, t.calls)} failed</text>
              </>
            ) : (
              <text x={xl - 8} y={t.mid + 4} textAnchor="end" className="fill-ink text-caption tabular-nums">{t.name} <tspan className="fill-muted">{fmt(t.calls)} · {pct(t.failed, t.calls)}</tspan></text>
            )}
          </g>
        ))}
        <rect x={xr} y={0} width={nodeW} height={Math.max(2, okH)} rx={2} className="fill-badge-blue" />
        <rect x={xr} y={failY} width={nodeW} height={failH} rx={2} className="fill-error" />
        <text x={xr + nodeW + 8} y={Math.min(okH / 2, 40) - 3} className="fill-ink text-caption">succeeded</text>
        <text x={xr + nodeW + 8} y={Math.min(okH / 2, 40) + 12} className="fill-ink text-caption font-medium tabular-nums">{fmt(total - failed)} · {pct(total - failed, total)}</text>
        <text x={xr + nodeW + 8} y={failY + failH / 2 + 1} className="fill-ink text-caption">failed</text>
        <text x={xr + nodeW + 8} y={failY + failH / 2 + 16} className="fill-error text-caption font-medium tabular-nums">{fmt(failed)} · {pct(failed, total)}</text>
      </svg>
    </div>
  );
}

/** Median and p90 of the first token and of a whole turn on one seconds scale. */
function TurnTime({ c }: Readonly<{ c: Counts }>) {
  const rows = [
    { label: 'first token', p50: num(c, 'models.ttfb_median_ms') / 1000, p90: num(c, 'models.ttfb_p90_ms') / 1000, bar: 'bg-accent', band: 'bg-accent-wash' },
    { label: 'whole turn', p50: num(c, 'turns.median_s'), p90: num(c, 'turns.p90_s'), bar: 'bg-ink', band: 'bg-hairline' },
  ];
  const max = Math.max(1, ...rows.map((r) => r.p90));
  const share = rows[1].p50 ? Math.round((rows[0].p50 * 100) / rows[1].p50) : 0;
  return (
    <Card title="Turn time">
      <div className="grid grid-cols-[4.75rem_minmax(0,1fr)_auto] items-center gap-x-2 gap-y-1.5">
        {rows.map((r) => (
          <div key={r.label} className="contents" title={`${r.label}: median ${fmt(r.p50, 1)} s, p90 ${fmt(r.p90, 1)} s`}>
            <span className="text-caption text-body">{r.label}</span>
            <span className="relative h-3.5">
              <span className={`absolute inset-y-0 left-0 rounded-[3px] ${r.band}`} style={{ width: `${(r.p90 * 100) / max}%` }} />
              <span className={`absolute inset-y-1 left-0 rounded-[2px] ${r.bar}`} style={{ width: `${(r.p50 * 100) / max}%` }} />
            </span>
            <span className="text-caption tabular-nums text-ink">{fmt(r.p50, 1)} s <span className="text-muted">· p90 {fmt(r.p90, 1)}</span></span>
          </div>
        ))}
      </div>
      <p className="text-caption text-muted">
        {share ? <>Waiting for the first token is <b className="font-medium text-ink">{share}%</b> of a median turn. </> : null}
        Median {num(c, 'turns.per_prompt_median')} turns per prompt, {num(c, 'turns.tool_calls_median')} tool call per turn.
      </p>
    </Card>
  );
}

const PERMISSION_NAMES: Record<string, string> = {
  'approved-for-location': 'approved for location',
  'denied-interactively-by-user': 'denied by you',
  'denied-no-approval-rule-and-could-not-request-from-user': 'denied, no rule',
};
const PERMISSION_TONES = ['bg-badge-blue', 'bg-hairline-strong', 'bg-error', 'bg-badge-amber', 'bg-badge-pink', 'bg-badge-teal'];

/** Permission requests: the plain approvals as one bar, the rest magnified below it. */
function Permissions({ c, all }: Readonly<{ c: Counts; all: Counts }>) {
  const total = num(c, 'friction.permission_prompts');
  const approved = num(c, 'permissions.approved');
  const others = entries(c, 'permissions').filter(([name, v]) => name !== 'approved' && v > 0);
  const rest = others.reduce((a, [, v]) => a + v, 0);
  const threads = num(all, 'config.threads');
  return (
    <Card title="Permission requests">
      <p className="text-caption text-body">
        <b className="text-title font-semibold tabular-nums text-ink">{fmt(approved)}</b> of {fmt(total)} approved without you · {pct(approved, total, 2)}
      </p>
      {rest > 0 && (
        <>
          <span className="flex h-2.5 gap-0.5" title={`The other ${fmt(rest)}`}>
            {others.map(([name, v], i) => <span key={name} className={`rounded-[3px] ${PERMISSION_TONES[i % PERMISSION_TONES.length]}`} style={{ flexGrow: v }} />)}
          </span>
          <ul className="grid grid-cols-2 gap-x-3 text-caption">
            {others.map(([name, v], i) => (
              <li key={name} className="flex items-center gap-1.5 truncate" title={name}>
                <span aria-hidden="true" className={`size-2 shrink-0 rounded-[2px] ${PERMISSION_TONES[i % PERMISSION_TONES.length]}`} />
                <span className="truncate">{PERMISSION_NAMES[name] ?? name.replaceAll('-', ' ')}</span>
                <b className="font-medium tabular-nums text-ink">{fmt(v)}</b>
              </li>
            ))}
          </ul>
        </>
      )}
      <p className="text-caption text-muted">
        Waits: median {c.get('approvals.wait_median_s') ?? '–'} s, p90 {c.get('approvals.wait_p90_s') ?? '–'} s, total {fmt(num(c, 'approvals.wait_total_s') / 3600, 1)} h.
        {threads > 0 && num(all, 'config.threads_yolo') === threads && ` All ${fmt(threads)} Threads are YOLO.`}
      </p>
    </Card>
  );
}

function WorkView({ c, all }: Readonly<{ c: Counts; all: Counts }>) {
  return (
    <div className="@container flex flex-col gap-5">
      <Card title={`${fmt(num(c, 'work.tool_calls'))} tool calls`}>
        <ToolFlow c={c} />
      </Card>
      <div className="grid gap-5 @lg:grid-cols-2">
        <TurnTime c={c} />
        <Permissions c={c} all={all} />
      </div>
    </div>
  );
}

const WHO = [
  { group: 'user', label: 'user', fill: 'fill-badge-blue', bg: 'bg-badge-blue' },
  { group: 'subagent', label: 'subagent', fill: 'fill-badge-pink', bg: 'bg-badge-pink' },
  { group: 'other', label: 'other', fill: 'fill-badge-amber', bg: 'bg-badge-amber' },
] as const;

/** Cost units added up by who started the call: user, subagent, other, then the total. */
function CostSteps({ c }: Readonly<{ c: Counts }>) {
  const [ref, w] = useWidth(260);
  const total = num(c, 'tokens.total_cost');
  const h = 150, top = 18, base = h - 20, colGap = 10, bw = (w - colGap * 3) / 4;
  const scale = total ? (base - top) / total : 0;
  const costs = WHO.map((who) => num(c, `tokens.${who.group}_cost`));
  const steps = WHO.map((who, i) => ({ ...who, v: costs[i], x: i * (bw + colGap), y: base - costs.slice(0, i + 1).reduce((a, v) => a + v, 0) * scale }));
  return (
    <Card title="Cost units, by who started the call">
      <div ref={ref} className="min-w-0">
        <svg width={w} height={h} role="img" aria-label={`${fmt(total)} cost units`} className="block">
          {steps.map(({ v, x, y, ...who }) => (
            <g key={who.group}>
              <title>{`${who.label}: ${fmt(v)} cost units over ${fmt(num(c, `tokens.${who.group}_calls`))} calls`}</title>
              <rect x={x} y={y} width={bw} height={Math.max(1, v * scale)} rx={3} className={who.fill} />
              <line x1={x + bw} x2={x + bw + colGap} y1={y} y2={y} className="stroke-hairline-strong" strokeDasharray="2 2" />
              <text x={x} y={y - 5} className="fill-ink text-caption font-medium tabular-nums">+{fmt(v)}</text>
              <text x={x} y={h - 4} className="fill-muted text-caption">{who.label}</text>
            </g>
          ))}
          <rect x={3 * (bw + colGap)} y={base - total * scale} width={bw} height={total * scale} rx={3} className="fill-ink" />
          <text x={3 * (bw + colGap)} y={base - total * scale - 5} className="fill-ink text-caption font-medium tabular-nums">{fmt(total)}</text>
          <text x={3 * (bw + colGap)} y={h - 4} className="fill-muted text-caption">total</text>
        </svg>
      </div>
      <p className="text-caption text-muted">
        Per call: {WHO.map((who) => `${who.label} ${fmt(num(c, `tokens.${who.group}_cost`) / Math.max(1, num(c, `tokens.${who.group}_calls`)), 2)}`).join(' · ')}.
      </p>
    </Card>
  );
}

/** Input tokens per group (column width), each split into read-from-cache and fresh. */
function CacheMosaic({ c }: Readonly<{ c: Counts }>) {
  const groups = WHO.map((who) => ({ ...who, input: num(c, `tokens.${who.group}_input`), cached: num(c, `tokens.${who.group}_cache_read`) })).filter((g) => g.input > 0);
  const total = groups.reduce((a, g) => a + g.input, 0);
  return (
    <Card title="Input tokens, cached or fresh">
      <div className="flex h-[150px] gap-0.5" role="img" aria-label={`${pct(num(c, 'tokens.total_cache_read'), num(c, 'tokens.total_input'), 0)} of input tokens read from cache`}>
        {groups.map((g) => (
          <div key={g.group} className="flex min-w-0 flex-col gap-0.5" style={{ flexGrow: g.input / Math.max(1, total) }} title={`${g.label}: ${fmt(g.input / 1e6, 1)}M input, ${pct(g.cached, g.input, 0)} cached`}>
            <div className="min-h-0 overflow-hidden rounded-[3px] bg-accent-wash px-1.5 py-1 text-caption text-ink" style={{ flexGrow: g.cached }}>
              <span className="block truncate font-medium">{g.label}</span>
              <span className="block truncate tabular-nums">{pct(g.cached, g.input, 0)}</span>
            </div>
            <div className="min-h-[3px] rounded-[3px] bg-accent" style={{ flexGrow: g.input - g.cached }} />
          </div>
        ))}
      </div>
      <p className="text-caption text-muted">
        Width is input tokens: {groups.map((g) => `${g.label} ${fmt(g.input / 1e6, g.input < 1e7 ? 1 : 0)}M`).join(' · ')}. Light is read from cache, dark is fresh.
      </p>
    </Card>
  );
}

const MODEL_TONES = ['bg-badge-blue', 'bg-badge-pink', 'bg-badge-amber'];

/** Model calls as one strip: the three biggest models in colour, the rest grey. */
function ModelStrip({ c }: Readonly<{ c: Counts }>) {
  const models = entries(c, 'model_calls').filter(([, v]) => v > 0);
  const total = models.reduce((a, [, v]) => a + v, 0);
  if (!total) return null;
  return (
    <Card title={`${fmt(total)} model calls, by model`}>
      <div className="flex h-7 gap-0.5" role="img" aria-label="Model calls by model">
        {models.map(([name, v], i) => (
          <span key={name} className={`flex min-w-[3px] items-center overflow-hidden rounded-[3px] px-1.5 text-caption font-medium text-on-primary ${MODEL_TONES[i] ?? 'bg-hairline-strong'}`} style={{ flexGrow: v }} title={`${name}: ${fmt(v)} (${pct(v, total, 0)})`}>
            {v / total >= 0.15 && <span className="truncate">{pct(v, total, 0)}</span>}
          </span>
        ))}
      </div>
      <ul className="flex flex-wrap gap-x-3 gap-y-0.5 text-caption">
        {models.map(([name, v], i) => (
          <li key={name} className="flex items-center gap-1.5">
            <span aria-hidden="true" className={`size-2 rounded-[2px] ${MODEL_TONES[i] ?? 'bg-hairline-strong'}`} />
            {name} <b className="font-medium tabular-nums text-ink">{fmt(v)}</b>
          </li>
        ))}
        <li className="text-muted">· changed by hand {fmt(num(c, 'model_changes.model_picker'))}</li>
      </ul>
    </Card>
  );
}

function SpendView({ c }: Readonly<{ c: Counts }>) {
  return (
    <div className="@container flex flex-col gap-5">
      <div className="grid gap-5 @lg:grid-cols-2">
        <CostSteps c={c} />
        <CacheMosaic c={c} />
      </div>
      <ModelStrip c={c} />
      <p className="text-caption text-muted">“other” is calls the CLI gave no initiator; its documentation says that marks user-initiated calls.</p>
    </div>
  );
}

/** Every key, grouped by prefix, with a hairline bar under each number scaled within its group. */
function EveryCounter({ list, allTime }: Readonly<{ list: Stat[]; allTime: Set<string> }>) {
  const groups = new Map<string, { name: string; value: string }[]>();
  for (const { key, value } of list) {
    if (key.startsWith('scan.')) continue;
    const dot = key.indexOf('.');
    const group = key.slice(0, dot);
    groups.set(group, [...(groups.get(group) ?? []), { name: key.slice(dot + 1), value }]);
  }
  return (
    // Columns, not a grid: groups of different lengths pack without gaps.
    <div className="columns-[10rem] gap-x-4">
      {[...groups].map(([group, rows]) => {
        const max = Math.max(0, ...rows.map((r) => Number(r.value) || 0));
        return (
          <section key={group} aria-labelledby={`session-stats-${group}`} className="mb-3 flex min-w-0 break-inside-avoid flex-col">
            <h3 id={`session-stats-${group}`} className="text-eyebrow uppercase text-muted">
              {group}
              {allTime.has(group) && <span className="normal-case"> · all time</span>}
            </h3>
            <dl className="flex flex-col">
              {rows.map((row) => (
                <div key={row.name} className="relative flex items-baseline gap-2 border-b border-hairline text-caption leading-[1.35] last:border-b-0">
                  <dt className="min-w-0 flex-1 font-mono text-[0.6875rem] break-words text-body">{breakable(row.name)}</dt>
                  <dd className="font-medium text-ink tabular-nums">{row.value}</dd>
                  {max > 0 && Number(row.value) > 0 && <span aria-hidden="true" className="absolute -bottom-px left-0 h-0.5 bg-accent/45" style={{ width: `${(Number(row.value) * 100) / max}%` }} />}
                </div>
              ))}
            </dl>
          </section>
        );
      })}
    </div>
  );
}

/**
 * Settings → Session stats: counts over every Copilot session journal on the server, drawn as charts on Work and Spend
 * and listed whole on Every counter; each view fits one 14-inch laptop screen for a screenshot. Last 7 days swaps in
 * the journal counts of that window; uam's own counts and the configuration have no window. Opening the page or Scan
 * again rescans and rewrites the key=value file.
 */
export function SessionStatsSettings() {
  const api = useApi();
  const [stats, setStats] = useState<SessionStats | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [scanning, setScanning] = useState(false);
  const [view, setView] = useState('work');
  const [range, setRange] = useState('all');
  const scan = useCallback(() => api.sessionStats().then((s) => { setStats(s); setError(null); }, (e: unknown) => setError(describeError(e))).finally(() => setScanning(false)), [api]);
  useEffect(() => { void scan(); }, [scan]);
  const [copied, copy] = useCopied();
  if (!stats) return error ? <Note tone="error" role="alert">Could not read the sessions: {error}</Note> : <Skeleton label="Reading every session…" rows={4} />;
  const all: Counts = new Map(stats.stats.map((s) => [s.key, s.value]));
  const recent = range === 'recent' && stats.recent ? stats.recent : null;
  const c: Counts = recent ? new Map(recent.map((s) => [s.key, s.value])) : all;
  // uam's counts and the configuration have no window: Every counter keeps their all-time rows.
  const allTime = new Set(['uam', 'config']);
  const list = recent ? [...recent.filter((s) => !s.key.startsWith('scan.')), ...stats.stats.filter((s) => allTime.has(s.key.slice(0, s.key.indexOf('.'))))] : stats.stats;
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-3">
        <span className="flex min-w-0 flex-1 flex-col text-caption text-muted">
          <span className="truncate font-mono">{stats.path}</span>
          <span className="tabular-nums">Scanned {all.get('scan.at')?.replace('T', ' ').replace('Z', ' UTC')}{all.get('scan.skipped') !== '0' && ` · ${all.get('scan.skipped')} unreadable lines skipped`}</span>
        </span>
        <Button size="sm" variant="secondary" disabled={scanning} onClick={() => { setScanning(true); void scan(); }}>
          <RefreshCw aria-hidden="true" className={scanning ? 'animate-spin' : undefined} />
          Scan again
        </Button>
        {/* The same key=value lines as the file, for pasting where the file cannot go. */}
        <Button size="sm" variant="secondary" onClick={() => copy(lines(stats))}>
          {copied ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}
          {copied ? 'Copied' : 'Copy'}
        </Button>
      </div>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <Segmented aria-label="View" value={view} onValueChange={setView} items={[{ value: 'work', label: 'Work' }, { value: 'spend', label: 'Spend' }, { value: 'every', label: 'Every counter' }]} className="w-80 max-w-full" />
        {stats.recent && <Segmented aria-label="Window" value={range} onValueChange={setRange} items={[{ value: 'recent', label: 'Last 7 days' }, { value: 'all', label: 'All time' }]} className="w-56" />}
      </div>
      {error && <Note tone="error" role="alert">{error}</Note>}
      {recent && !num(c, 'sessions.total') ? (
        <Note>No session has events from the last 7 days.</Note>
      ) : view === 'work' ? (
        <WorkView c={c} all={all} />
      ) : view === 'spend' ? (
        <SpendView c={c} />
      ) : (
        <EveryCounter list={list} allTime={recent ? allTime : new Set()} />
      )}
    </div>
  );
}
