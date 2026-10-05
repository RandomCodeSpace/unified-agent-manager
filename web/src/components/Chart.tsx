import { BarChart3, Check, ChevronDown, Copy, Ellipsis, LineChart, Pin, RefreshCw, Table2, X } from 'lucide-react';
import { memo, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { api, describeError, isStatus, type Chart, type PinnedChart, type Project } from '../api';
import { popupOpen } from '../App';
import { chartCsv, chartOption, formatNumber, headline, seriesColorIndexes, type ChartLook } from '../lib/chart';
import { chartTable, type ChartTable } from '../lib/chart-table';
import { DataTable } from './DataTable';
import { useCopied } from '../lib/clipboard';
import { cn } from '../lib/cn';
import { EChart } from './EChart';
import { Note, Skeleton, Spinner, timeAgo, useMedia } from './common';
import { PanelHeader, SidePanel } from './Subagents';
import { Button } from './ui/button';
import { Popover } from './ui/popover';

const PHONE = '(width < 40rem)';
/** Pinned charts refresh on their own when the panel opens, at most this often (the service holds the same line). */
const AUTO_EVERY = 60 * 60_000;

type ChartRows = Pick<Chart, 'title' | 'kind' | 'x_label' | 'y_label' | 'x' | 'labels' | 'series' | 'options'>;

/** Swatches in the order of SERIES_TOKENS (lib/chart), spelled out so the classes are generated. */
const SWATCHES = ['bg-badge-blue', 'bg-badge-orange', 'bg-badge-teal', 'bg-badge-pink', 'bg-badge-violet', 'bg-badge-cyan', 'bg-badge-green'];

/** A script's first line and an ellipsis; the whole command is in the title and the pin prompt. */

/** The chart's data is rendered directly, with the same drawing used for pinned sparklines. */
export function ChartImage({ chart, look: { width, height, spark }, className, hidden, zoomControls = true, controlsContainer, legend }: Readonly<{ chart: ChartRows; look: ChartLook; className?: string; hidden?: ReadonlySet<string>; zoomControls?: boolean; controlsContainer?: HTMLElement | null; legend?: ReactNode }>) {
  const compact = legend !== undefined;
  const option = useMemo(() => chartOption(chart, { width, height, spark, compact }, hidden), [chart, width, height, spark, compact, hidden]);
  const label = chart.kind === 'echarts' ? `Chart: ${chart.title}` : `${chart.kind === 'bar' ? 'Bar' : 'Line'} chart: ${chart.title}, ${chart.labels.length} ${chart.labels.length === 1 ? 'point' : 'points'}`;
  return <EChart option={option} width={width} height={height} label={label} className={className} zoomControls={zoomControls && !spark} controlsContainer={controlsContainer} legend={legend} />;
}

/** Each series' colour and name, when there is more than one; a bar chart's first series is its bars. */
function Legend({ chart, hidden, onToggle }: Readonly<{ chart: ChartRows; hidden: ReadonlySet<string>; onToggle: (name: string) => void }>) {
  if (chart.series.length < 2) return null;
  const colors = seriesColorIndexes(chart.series);
  return (
    <ul className="flex min-w-0 flex-1 flex-wrap gap-x-3 gap-y-1 text-caption text-body" aria-label="Series">
      {chart.series.map((s, i) => (
        <li key={s.name} className="min-w-0 max-w-full">
          <button type="button" aria-pressed={!hidden.has(s.name)} aria-label={`Show ${s.name} series`} className={cn('flex min-h-7 max-w-full items-center gap-1.5 rounded-xs px-1 text-left pointer-coarse:min-h-11', hidden.has(s.name) && 'text-muted line-through')} onClick={() => onToggle(s.name)}>
            <span aria-hidden="true" className={cn('inline-block shrink-0 rounded-full', chart.kind === 'bar' && i === 0 ? 'h-2.5 w-2.5 rounded-xs' : 'h-0.5 w-3', SWATCHES[colors[i]])} />
            <span className="[overflow-wrap:anywhere]">{s.name}</span>
          </button>
        </li>
      ))}
    </ul>
  );
}

function RowsTable({ data, title }: Readonly<{ data: ChartTable; title: string }>) {
  const columns = useMemo(() => data.columns.map((name, index) => ({ name, align: data.rows.some(row => typeof row[index] === 'number') ? 'right' as const : 'left' as const })), [data]);
  const rows = useMemo(() => data.rows.map((values, id) => ({ id, values, cells: values.map(value => typeof value === 'number' ? formatNumber(value) : value ?? '—') })), [data]);
  return <div className="px-3"><DataTable columns={columns} rows={rows} label={`Rows of ${title}`} rowHeaders /></div>;
}

/** The original data and chart configuration remain available to inspect and copy. */
function ChartData({ chart }: Readonly<{ chart: ChartRows }>) {
  return (
    // eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- The labelled data region accepts keyboard scrolling.
    <div role="region" aria-label={`Data for ${chart.title}`} tabIndex={0} className="max-h-80 overflow-auto px-5">
      <pre className="font-mono text-code-sm whitespace-pre-wrap text-ink [overflow-wrap:anywhere]">{JSON.stringify(chart.options, null, 2)}</pre>
    </div>
  );
}

/** Pin asks first: pinning approves the command Refresh will run again. */
function PinButton({ sessionId, callId, chart, onPinned }: Readonly<{ sessionId: string; callId: string; chart: Chart; onPinned: (id: string) => void }>) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  if (chart.pinned_id) {
    return (
      <span className="inline-flex h-8 items-center gap-1.5 px-2 text-ui font-medium text-success [&_svg]:size-4">
        <Check aria-hidden="true" />
        <span>Pinned</span>
      </span>
    );
  }
  const pin = async () => {
    setBusy(true);
    setError('');
    try {
      onPinned((await api.pinChart(sessionId, callId)).id);
      setOpen(false);
    } catch (err) {
      setError(describeError(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Popover.Root open={open} onOpenChange={(o) => { setOpen(o); setError(''); }}>
      <Popover.Trigger render={<Button size="md" className="w-full justify-start bg-accent-wash px-2 text-accent not-aria-disabled:hover:bg-selection" />}>
        <Pin />
        <span>Pin to project</span>
      </Popover.Trigger>
      <Popover.Content side="bottom" align="end" className="max-w-80 gap-2">
        <Popover.Title>Pin to the project</Popover.Title>
        {chart.command ? (
          <>
            <Popover.Description>Refresh runs this command again in the project folder, with no agent and no model call:</Popover.Description>
            <pre translate="no" className="max-h-60 overflow-auto rounded-xs bg-code-bg px-2 py-1 font-mono text-code-sm whitespace-pre-wrap text-ink [overflow-wrap:anywhere]">{chart.command}</pre>
          </>
        ) : (
          <Popover.Description>{chart.kind === 'echarts' ? 'The agent supplied this chart’s data' : `The agent gave these ${chart.labels.length} rows itself`}, so the pinned chart is a snapshot: it has no command and does not refresh.</Popover.Description>
        )}
        {error && <Note tone="error" role="alert">{error}</Note>}
        <div className="flex justify-end gap-1.5 pt-1">
          <Button size="md" onClick={() => setOpen(false)}>Cancel</Button>
          <Button size="md" variant="primary" loading={busy} disabled={busy} onClick={() => void pin()}>Pin</Button>
        </div>
      </Popover.Content>
    </Popover.Root>
  );
}

/**
 * The width a chart is drawn at: its box's, in 40px steps so a resize redraws rarely and the
 * label density changes in steps, so text keeps its size as the drawing follows the box.
 */
function useDrawWidth(fallback: number) {
  const [width, setWidth] = useState(0);
  const observer = useRef<ResizeObserver | null>(null);
  const ref = useCallback((el: HTMLElement | null) => {
    observer.current?.disconnect();
    if (!el) return;
    observer.current = new ResizeObserver(([entry]) => setWidth(Math.max(280, Math.floor(entry.contentRect.width / 40) * 40)));
    observer.current.observe(el);
  }, []);
  useEffect(() => () => observer.current?.disconnect(), []);
  return [ref, width || fallback] as const;
}

/**
 * A `uam_chart` call's card in the transcript: the title, the chart, the rows as a table on
 * demand, Copy CSV and Pin to project, and where the rows came from. The rows are kept with
 * the Task, so the card reads the same after a reload.
 */
export const ChartCard = memo(function ChartCard({ sessionId, callId }: Readonly<{ sessionId: string; callId: string }>) {
  const phone = useMedia(PHONE);
  const [box, width] = useDrawWidth(phone ? 360 : 840);
  const [chart, setChart] = useState<Chart | null>(null);
  const [error, setError] = useState('');
  const [table, setTable] = useState(false);
  const [controlsContainer, setControlsContainer] = useState<HTMLDivElement | null>(null);
  const [hidden, setHidden] = useState<ReadonlySet<string>>(() => new Set());
  const data = useMemo(() => chart ? chartTable(chart) : null, [chart]);
  const [copied, copy] = useCopied();
  useEffect(() => {
    const controller = new AbortController();
    api.chart(sessionId, callId, controller.signal).then(setChart, (err: unknown) => {
      if (!controller.signal.aborted) setError(isStatus(err, 404) ? 'This chart is no longer kept.' : describeError(err));
    });
    return () => controller.abort();
  }, [sessionId, callId]);
  if (error) return <Note>{error}</Note>;
  if (!chart) return <Skeleton label="Loading the chart…" rows={3} className="rounded-lg bg-raised p-4 shadow-raised" />;
  const advanced = chart.kind === 'echarts';
  const Icon = chart.kind === 'line' ? LineChart : BarChart3;
  const rows = chart.labels.length;
  // Keep the plotted area after removing the toolbar row and excess axis padding.
  const height = (phone ? 260 : 320) - (advanced ? 0 : 56);
  const legend = advanced ? undefined : <Legend chart={chart} hidden={hidden} onToggle={(name) => setHidden((previous) => { const next = new Set(previous); if (next.has(name)) next.delete(name); else next.add(name); return next; })} />;
  return (
    <figure aria-label={`Chart: ${chart.title}`} className="flex flex-col gap-2 rounded-lg bg-raised pt-2 shadow-raised">
      <header className="flex items-center gap-2 pr-2 pl-5">
        <Icon aria-hidden="true" className="size-4 shrink-0 text-accent" />
        <figcaption className="min-w-0 flex-1 truncate text-ui font-medium text-ink" title={chart.title}>{chart.title}</figcaption>
        <Popover.Root>
          <Popover.Trigger openOnHover render={<Button size="icon-md" aria-label="Chart tools" className="text-muted" />}><Ellipsis /></Popover.Trigger>
          <Popover.Content side="bottom" align="end" className="w-56 gap-2">
            <Popover.Title className="sr-only">Chart tools</Popover.Title>
            <div className="flex flex-col items-stretch gap-1">
              <Button size="md" aria-pressed={table} className="justify-start px-2 text-muted" onClick={() => setTable((t) => !t)}>
                {table ? <Icon /> : <Table2 />}
                <span>{table ? 'Chart' : data ? 'Table' : 'Data'}</span>
              </Button>
              <Button size="md" className="justify-start px-2 text-muted" onClick={() => copy(advanced ? JSON.stringify(chart.options, null, 2) : chartCsv(chart))}>
                {copied ? <Check /> : <Copy />}
                <span>{copied ? 'Copied' : advanced ? 'Copy JSON' : 'Copy CSV'}</span>
              </Button>
              <PinButton sessionId={sessionId} callId={callId} chart={chart} onPinned={(id) => setChart({ ...chart, pinned_id: id })} />
            </div>
            <div ref={setControlsContainer} className="border-t border-hairline pt-2 empty:hidden" />
          </Popover.Content>
        </Popover.Root>
      </header>
      {table ? data ? <RowsTable data={data} title={chart.title} /> : <ChartData chart={chart} /> : <div ref={box} className="px-3"><ChartImage chart={chart} look={{ width, height }} hidden={hidden} legend={legend} controlsContainer={controlsContainer} /></div>}
      {table && legend && <div className="px-5">{legend}</div>}
      <p className="flex flex-wrap items-center gap-x-3 gap-y-1 border-t border-hairline px-5 py-2.5 text-meta text-muted">
        <span>{chart.command ? 'From a command' : advanced ? 'From data the agent gave' : 'From rows the agent gave'}, {timeAgo(chart.at)}</span>
        {!advanced && <span>· Drawn from {rows} {rows === 1 ? 'row' : 'rows'}</span>}
      </p>
    </figure>
  );
});

function PinnedCard({ chart, busy, notice, onRefresh, onUnpin }: Readonly<{ chart: PinnedChart; busy: boolean; notice?: string; onRefresh: () => void; onUnpin: () => void }>) {
  const [expanded, setExpanded] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const [box, width] = useDrawWidth(560);
  const head = headline(chart);
  const refreshed = chart.labels.length ? timeAgo(chart.at) : 'not read yet';
  return (
    <section aria-label={chart.title} className="flex flex-col gap-1 rounded-lg bg-raised p-3.5 shadow-raised">
      <div className="flex items-center gap-1">
        <h3 className="min-w-0 flex-1 truncate text-caption text-muted" title={chart.title}>{chart.title}</h3>
        {chart.command ? (
          <Button size="sm" aria-label={`Refresh ${chart.title}, refreshed ${refreshed}`} disabled={busy} className="px-1.5 text-meta font-normal text-muted" onClick={onRefresh}>
            {busy ? <Spinner /> : <RefreshCw className="size-3!" />}
            {refreshed}
          </Button>
        ) : (
          <span className="px-1.5 text-meta text-muted">Snapshot, {refreshed}</span>
        )}
        <Popover.Root open={confirm} onOpenChange={setConfirm}>
          <Popover.Trigger render={<Button size="icon" aria-label={`Unpin ${chart.title}`} className="text-muted" />}>
            <X />
          </Popover.Trigger>
          <Popover.Content side="bottom" align="end" className="gap-2">
            <Popover.Title>Unpin this chart?</Popover.Title>
            <Popover.Description>{chart.command ? 'Its saved command and rows are removed. The chart stays in its task.' : 'Its rows are removed. The chart stays in its task.'}</Popover.Description>
            <div className="flex justify-end gap-1.5 pt-1">
              <Button size="md" onClick={() => setConfirm(false)}>Cancel</Button>
              <Button size="md" variant="danger" onClick={() => { setConfirm(false); onUnpin(); }}>Unpin</Button>
            </div>
          </Popover.Content>
        </Popover.Root>
      </div>
      {head && (
        <p className="flex min-w-0 items-baseline gap-2">
          <span className="text-display-sm text-ink tabular-nums">{head.value}</span>
          <span className="min-w-0 truncate text-meta text-muted">{head.note}</span>
        </p>
      )}
      {(chart.kind === 'echarts' ? !!chart.options : chart.labels.length > 0) && (
        <div ref={box} className="relative -mx-1 px-1 pt-1">
          <ChartImage chart={chart} look={expanded ? { width, height: 320 } : chart.kind === 'echarts' ? { width, height: 180 } : { width: 300, height: 64, spark: true }} zoomControls={expanded} />
          <button type="button" aria-expanded={expanded} aria-label={expanded ? `Show ${chart.title} small` : `Show ${chart.title} large`} className={cn('group/spark absolute rounded-sm outline-hidden focus-visible:outline-2 focus-visible:outline-focus', expanded ? 'right-0 bottom-0 grid size-7 place-items-center bg-raised pointer-coarse:size-11' : 'inset-0')} onClick={() => setExpanded((e) => !e)}>
            <ChevronDown aria-hidden="true" className={cn('size-3.5 text-faint transition-[opacity,transform]', expanded ? 'rotate-180' : 'absolute right-0 bottom-0 opacity-0 group-hover/spark:opacity-100 group-focus-visible/spark:opacity-100')} />
          </button>
        </div>
      )}
      {chart.error && <Note tone="warn" className="[overflow-wrap:anywhere]">Last refresh failed{chart.error_at ? `, ${timeAgo(chart.error_at)}` : ''}: {chart.error}</Note>}
      {notice && <Note tone="warn" role="status">{notice}</Note>}
    </section>
  );
}

/**
 * The charts pinned to a Project, beside the conversation. Opening it refreshes each chart whose
 * rows are over an hour old; Refresh re-runs a chart's saved command on demand, at most once a
 * minute. Neither involves the agent or a model.
 */
export function PinnedChartsPanel({ project, inline, open, onClose, onClosed }: Readonly<{
  project: Project;
  inline: boolean;
  /** False while the panel leaves; `onClosed` follows, and the owner unmounts it. */
  open: boolean;
  onClose: () => void;
  onClosed: () => void;
}>) {
  const [charts, setCharts] = useState<PinnedChart[] | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState<ReadonlySet<string>>(() => new Set());
  const [notices, setNotices] = useState<Record<string, string>>({});
  const projectId = project.id;
  const count = project.charts ?? 0;

  const refresh = useCallback(async (chart: PinnedChart, auto: boolean) => {
    setBusy((b) => new Set(b).add(chart.id));
    setNotices((n) => Object.fromEntries(Object.entries(n).filter(([id]) => id !== chart.id)));
    try {
      const next = await api.refreshChart(projectId, chart.id, auto);
      setCharts((list) => list?.map((c) => (c.id === next.id ? next : c)) ?? list);
    } catch (err) {
      if (!auto) setNotices((n) => ({ ...n, [chart.id]: describeError(err) }));
    } finally {
      setBusy((b) => { const n = new Set(b); n.delete(chart.id); return n; });
    }
  }, [projectId]);

  // Read again when the count changes (a pin from a transcript); stale ones refresh on their own.
  useEffect(() => {
    const controller = new AbortController();
    api.pinnedCharts(projectId, controller.signal).then(
      (list) => {
        setCharts(list);
        setError('');
        void (async () => {
          for (const c of list) {
            const last = Math.max(Date.parse(c.at) || 0, Date.parse(c.error_at ?? '') || 0);
            if (c.command && Date.now() - last > AUTO_EVERY && !controller.signal.aborted) await refresh(c, true);
          }
        })();
      },
      (err: unknown) => { if (!controller.signal.aborted) setError(describeError(err)); },
    );
    return () => controller.abort();
  }, [projectId, count, refresh]);

  // Inline, the panel is no dialog: Esc closes it unless a popup owns the key.
  useEffect(() => {
    if (!inline || !open) return;
    const escape = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !event.defaultPrevented && !popupOpen()) onClose();
    };
    document.addEventListener('keydown', escape);
    return () => document.removeEventListener('keydown', escape);
  }, [inline, open, onClose]);

  const unpin = async (chart: PinnedChart) => {
    try {
      await api.unpinChart(projectId, chart.id);
      setCharts((list) => list?.filter((c) => c.id !== chart.id) ?? list);
    } catch (err) {
      setNotices((n) => ({ ...n, [chart.id]: describeError(err) }));
    }
  };

  return (
    <SidePanel id="pinned-charts" inline={inline} open={open} onClose={onClose} onClosed={onClosed} label="Pinned charts" defaultWidth={380}>
      <PanelHeader>
        <Pin aria-hidden="true" className="size-4 shrink-0 text-muted" />
        <span className="min-w-0 truncate text-title text-ink">Pinned for {project.name}</span>
        <span className="flex-1" />
        <Button size="icon-md" aria-label="Close pinned charts" className="text-muted" onClick={onClose}>
          <X />
        </Button>
      </PanelHeader>
      {/* eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- A labelled scroll region must accept keyboard scrolling. */}
      <div role="region" aria-label={`Charts pinned to ${project.name}`} tabIndex={0} className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto overscroll-contain px-3 pt-1 pb-4">
        {error && <Note tone="error" role="alert">Could not load the pinned charts: {error}</Note>}
        {!charts && !error && <Skeleton label="Loading the pinned charts…" rows={4} />}
        {charts?.length === 0 && <Note>No charts are pinned to {project.name}. Pin one from a chart an agent drew in a task.</Note>}
        {charts?.map((c) => <PinnedCard key={c.id} chart={c} busy={busy.has(c.id)} notice={notices[c.id]} onRefresh={() => void refresh(c, false)} onUnpin={() => void unpin(c)} />)}
        <div className="rounded-md bg-sunken p-3 text-caption text-body">
          <p className="font-medium text-ink">What refreshing costs</p>
          <p className="mt-1">Refresh re-runs the saved command in the project folder and redraws. <b>No model call.</b> Only asking a task a new question uses the agent.</p>
          <p className="mt-2 text-muted">Charts refresh when you open this panel, at most once an hour.</p>
        </div>
      </div>
    </SidePanel>
  );
}
