// PROTOTYPE (throwaway): Demo K "Graphs", inside the Command Center layout. The agent answers a
// data question with a chart: it runs a command for the rows, then calls a uam chart tool with a
// small spec (kind, axes, the rows). The browser draws it, so a chart costs the tokens of its
// rows, never an image. Pinning keeps the command; Refresh re-runs it with no model call.

import { ArrowUp, BarChart3, Copy, Keyboard, Paperclip, Pin, RefreshCw, Table2 } from 'lucide-react';
import { cn } from '../../lib/cn';
import { nameOf, projectOf, tasks } from './data';
import { ProjectBadge } from './shared';
import { CommandList } from './VariantG';

export const name = 'Graphs';

// Minutes per CI run on main, oldest first; run 241 is the Go bump that missed the cache.
const RUNS = [6.2, 5.9, 6.4, 6.1, 6.0, 6.3, 5.8, 6.1, 13.9, 6.4, 6.2, 6.0, 5.9, 6.1, 6.6, 6.3, 6.0, 6.2, 5.9, 6.4, 6.1, 6.0, 14.2, 6.5, 6.3, 6.1, 6.2, 5.9, 6.0, 6.1];
const FIRST = 222;
const SPIKES = [8, 22];

export function VariantK({ openId, open }: { openId: string | null; open: (id: string | null) => void }) {
  const task = tasks.find((t) => t.id === openId) ?? tasks.find((t) => t.id === 't3')!;
  const p = projectOf(task);
  return (
    <div className="flex h-dvh bg-canvas text-body">
      <CommandList current={task} open={open} />
      <main className="flex min-w-0 flex-1 flex-col">
        <header className="flex items-center gap-3 px-8 py-4">
          <ProjectBadge badge={p.badge} />
          <div className="min-w-0">
            <h1 className="truncate text-display-sm text-ink">{nameOf(task)}</h1>
            <p className="text-caption text-muted">{p.name} · Completed</p>
          </div>
        </header>

        <div className="flex min-h-0 flex-1 gap-6 px-8 pb-6">
          <div className="min-w-0 flex-1 space-y-4 overflow-y-auto">
            <div className="ml-auto w-fit max-w-[70%] rounded-lg rounded-br-xs bg-bubble px-4 py-2.5 text-ui text-ink">
              How has CI time on main moved over the last 30 runs? Flag anything odd.
            </div>
            <Tool label="Ran" text="gh run list --branch main --limit 30 --json databaseId,createdAt,durationMs" meta="30 rows · 1.4 s" />
            <Tool label="Chart" text="Line · minutes per run · 30 points" meta="spec 1.1 KB" />

            <figure className="rounded-lg bg-raised shadow-raised">
              <header className="flex items-center gap-2 px-5 pt-4">
                <BarChart3 className="size-4 text-accent" />
                <figcaption className="text-ui font-medium text-ink">CI duration on main, last 30 runs</figcaption>
                <div className="ml-auto flex items-center gap-1 text-caption">
                  <Action icon={Table2} label="Table" />
                  <Action icon={Copy} label="Copy CSV" />
                  <Action icon={Pin} label="Pin to project" strong />
                </div>
              </header>
              <LineChart />
              <p className="flex flex-wrap items-center gap-x-3 gap-y-1 border-t border-hairline px-5 py-2.5 text-meta text-muted">
                <span>Data: <code className="font-mono">gh run list</code>, just now</span>
                <span>· Drawn in your browser from 30 rows</span>
                <span>· Hover a point for the run</span>
              </p>
            </figure>

            <div className="space-y-3 text-chat text-ink">
              <p>Median is <b>6.1 min</b> and flat. Two runs took more than twice that:</p>
              <ul className="list-disc space-y-1 pl-5">
                <li><b>#230</b> (13.9 min) and <b>#244</b> (14.2 min). Both changed <code className="rounded-xs bg-code-bg px-1 font-mono text-[0.88em]">go.mod</code>, so the module cache missed and every dependency downloaded again.</li>
                <li>Nothing else stands out. No upward trend.</li>
              </ul>
              <p>Keying the cache on <code className="rounded-xs bg-code-bg px-1 font-mono text-[0.88em]">go.sum</code> alone would not help here; a restore-keys fallback would.</p>
            </div>
          </div>

          <aside className="flex w-[340px] shrink-0 flex-col gap-3 overflow-y-auto">
            <h2 className="flex items-center gap-2 text-eyebrow text-muted uppercase"><Pin className="size-3.5" /> Pinned for {p.name}</h2>
            <Pinned title="CI duration on main" value="6.1 min" note="median of 30" points={RUNS} tone="accent" refreshed="just now" />
            <Pinned title="Open pull requests" value="4" note="2 waiting on review" points={[3, 5, 4, 6, 7, 5, 4, 4]} tone="success" refreshed="3h ago" bars />
            <Pinned title="Test count" value="1,284" note="+36 this week" points={[1180, 1196, 1210, 1225, 1238, 1248, 1270, 1284]} tone="info" refreshed="1d ago" />
            <div className="rounded-md bg-sunken p-3 text-caption text-body">
              <p className="font-medium text-ink">What refreshing costs</p>
              <p className="mt-1">Refresh re-runs the saved command and redraws. <b>No model call.</b> Only asking a new question uses the agent.</p>
              <p className="mt-2 text-muted">Charts refresh when you open the project, at most once an hour.</p>
            </div>
          </aside>
        </div>

        <footer className="px-8 pb-5">
          <div className="rounded-lg bg-raised px-4 pt-3 pb-2 shadow-float">
            <p className="text-ui text-muted">Ask a follow-up…</p>
            <div className="mt-2 flex items-center gap-2">
              <Paperclip className="size-4 text-faint" />
              <span className="text-caption text-muted">Auto · Ask me first</span>
              <span className="ml-auto flex items-center gap-1 text-meta text-muted"><Keyboard className="size-3.5" /> Enter sends now</span>
              <button className="inline-flex items-center gap-1 rounded-sm bg-primary px-3 py-1.5 text-caption font-medium text-on-primary">Send <ArrowUp className="size-3.5" /></button>
            </div>
          </div>
        </footer>
      </main>
    </div>
  );
}

const Tool = ({ label, text, meta }: { label: string; text: string; meta: string }) => (
  <p className="flex items-center gap-2 text-caption">
    <span className="w-12 shrink-0 text-muted">{label}</span>
    <code className="min-w-0 truncate font-mono text-code-sm text-ink">{text}</code>
    <span className="shrink-0 text-muted">· {meta}</span>
  </p>
);

const Action = ({ icon: Icon, label, strong }: { icon: typeof Pin; label: string; strong?: boolean }) => (
  <button className={cn('inline-flex items-center gap-1 rounded-sm px-2 py-1', strong ? 'bg-accent-wash text-accent' : 'text-muted hover:bg-tint-hover')}>
    <Icon className="size-3.5" /> {label}
  </button>
);

function LineChart() {
  const W = 900, H = 260, L = 44, R = 16, T = 18, B = 34;
  const max = 16;
  const x = (i: number) => L + (i * (W - L - R)) / (RUNS.length - 1);
  const y = (v: number) => T + (1 - v / max) * (H - T - B);
  const path = RUNS.map((v, i) => `${i ? 'L' : 'M'}${x(i).toFixed(1)},${y(v).toFixed(1)}`).join(' ');
  return (
    <svg viewBox={`0 0 ${W} ${H}`} className="block w-full px-2" role="img" aria-label="CI minutes per run; median 6.1, spikes at runs 230 and 244">
      {[0, 4, 8, 12, 16].map((v) => (
        <g key={v}>
          <line x1={L} x2={W - R} y1={y(v)} y2={y(v)} className="stroke-hairline" />
          <text x={L - 8} y={y(v) + 4} textAnchor="end" className="fill-muted text-[13px]">{v}m</text>
        </g>
      ))}
      <line x1={L} x2={W - R} y1={y(6.1)} y2={y(6.1)} strokeDasharray="4 4" className="stroke-success" />
      <text x={W - R} y={y(6.1) - 6} textAnchor="end" className="fill-success text-[13px]">median 6.1m</text>
      <path d={path} fill="none" strokeWidth={2} strokeLinejoin="round" className="stroke-accent" />
      {RUNS.map((v, i) => (
        <circle key={i} cx={x(i)} cy={y(v)} r={SPIKES.includes(i) ? 5 : 2.5} className={SPIKES.includes(i) ? 'fill-error' : 'fill-accent'} />
      ))}
      {SPIKES.map((i) => (
        <text key={i} x={x(i) + 9} y={y(RUNS[i]) + 4} className="fill-error text-[13px] font-semibold">#{FIRST + i} · {RUNS[i]}m · go.mod changed</text>
      ))}
      {[0, 5, 10, 15, 20, 25, 29].map((i) => (
        <text key={i} x={x(i)} y={H - 10} textAnchor="middle" className="fill-muted text-[13px]">#{FIRST + i}</text>
      ))}
    </svg>
  );
}

function Pinned({ title, value, note, points, tone, refreshed, bars }: {
  title: string; value: string; note: string; points: number[]; tone: 'accent' | 'success' | 'info'; refreshed: string; bars?: boolean;
}) {
  const W = 300, H = 56;
  const lo = Math.min(...points), hi = Math.max(...points);
  const y = (v: number) => H - 4 - ((v - lo) / (hi - lo || 1)) * (H - 8);
  const x = (i: number) => (i * W) / (points.length - 1);
  return (
    <section className="rounded-lg bg-raised p-3.5 shadow-raised">
      <div className="flex items-baseline gap-2">
        <h3 className="text-caption text-muted">{title}</h3>
        <button aria-label={`Refresh ${title}`} className="ml-auto inline-flex items-center gap-1 text-meta text-muted"><RefreshCw className="size-3" /> {refreshed}</button>
      </div>
      <p className="mt-0.5 flex items-baseline gap-2"><span className="text-display-sm text-ink">{value}</span><span className="text-meta text-muted">{note}</span></p>
      <svg viewBox={`0 0 ${W} ${H}`} className="mt-2 block w-full" aria-hidden>
        {bars
          ? points.map((v, i) => <rect key={i} x={i * (W / points.length) + 4} width={W / points.length - 8} y={y(v)} height={H - y(v)} rx={2} className="fill-success/70" />)
          : <polyline points={points.map((v, i) => `${x(i)},${y(v)}`).join(' ')} fill="none" strokeWidth={2} className={tone === 'info' ? 'stroke-info' : 'stroke-accent'} />}
      </svg>
    </section>
  );
}
