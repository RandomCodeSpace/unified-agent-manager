import { Slider } from '@base-ui/react/slider';
import type { EChartsOption } from 'echarts';
import type { EChartsType } from 'echarts/core';
import { Minus, Plus, RotateCcw } from 'lucide-react';
import { useEffect, useRef, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { cn } from '../lib/cn';
import { Note } from './common';
import { Button } from './ui/button';

type ZoomRange = { type: string; start: number; end: number; disabled?: boolean; zoomLock?: boolean; xAxisIndex?: number | number[]; yAxisIndex?: number | number[] };
type MenuRange = { index: number; start: number; end: number; first: string; last: string };
const adjustable = (range: ZoomRange) => (range.type === 'inside' || range.type === 'slider') && !range.disabled && !range.zoomLock;
const axisIndexes = (indexes: number | number[] | undefined) => indexes === undefined ? [] : (Array.isArray(indexes) ? [...indexes] : [indexes]).sort((a, b) => a - b);
const axesKey = (range: ZoomRange) => JSON.stringify([axisIndexes(range.xAxisIndex ?? (range.yAxisIndex === undefined ? 0 : undefined)), axisIndexes(range.yAxisIndex)]);
// ECharts keeps a legend's resolved entries (series or data names) only on its model.
type LegendModel = { get(key: 'show' | 'selectedMode'): unknown; getData(): { get(key: 'name'): unknown }[]; isSelected(name: string): boolean };
const legendModel = (drawing: EChartsType) => (drawing as unknown as { getModel(): { getComponent(type: string): LegendModel | undefined } }).getModel().getComponent('legend');

/** A locally bundled SVG drawing. The owner supplies accessible controls for planner nodes. */
export function EChart({ option, width, height, className, label, zoomControls = false, controlsContainer, legend }: Readonly<{
  option: EChartsOption;
  width: number;
  height: number;
  className?: string;
  label?: string;
  zoomControls?: boolean;
  /** null hides controls until the owner's tools popover mounts its container. */
  controlsContainer?: HTMLElement | null;
  legend?: ReactNode;
}>) {
  const host = useRef<HTMLDivElement>(null);
  const chart = useRef<EChartsType | null>(null);
  const [error, setError] = useState(false);
  const [ready, setReady] = useState(false);
  const [hasControls, setHasControls] = useState(false);
  const [menuRanges, setMenuRanges] = useState<MenuRange[]>([]);
  const [entries, setEntries] = useState<{ name: string; shown: boolean }[]>([]);
  const ownLegend = legend !== undefined;
  const controls = zoomControls && hasControls;
  const externalControls = controlsContainer !== undefined;
  const narrowControls = controls && width < 480 && !externalControls;
  const footer = legend !== undefined || narrowControls;

  useEffect(() => {
    const element = host.current;
    if (!element) return;
    let active = true;
    let observer: ResizeObserver | undefined;
    void import('../lib/echarts').then(({ init }) => {
      if (!active) return;
      try {
        const drawing = chart.current ??= init(element, undefined, { renderer: 'svg', width: element.clientWidth || width, height: element.clientHeight || height });
        const syncControls = () => setHasControls(((drawing.getOption()?.dataZoom ?? []) as ZoomRange[]).some(adjustable));
        const resize = () => {
          drawing.resize({ width: element.clientWidth || width, height: element.clientHeight || height });
          syncControls();
        };
        resize();
        drawing.setOption(option, { notMerge: true });
        syncControls();
        observer = new ResizeObserver(resize);
        observer.observe(element);
        setError(false);
        setReady(true);
      } catch {
        setError(true);
      }
    }, () => { if (active) setError(true); });
    return () => {
      active = false;
      observer?.disconnect();
    };
  }, [option, width, height]);

  useEffect(() => () => {
    chart.current?.dispose();
    chart.current = null;
  }, []);

  useEffect(() => {
    const drawing = chart.current;
    if (!drawing || !controlsContainer || !ready) return;
    const sync = () => {
      const current = drawing.getOption();
      const ranges = (current.dataZoom ?? []) as ZoomRange[];
      setMenuRanges(ranges.flatMap((range, index) => {
        if (!adjustable(range)) return [];
        // Linked inside/slider components share one range. Independent axes
        // still need their own selector even when their old slider is hidden.
        if (ranges.slice(0, index).some((z) => adjustable(z) && axesKey(z) === axesKey(range))) return [];
        const axisIndex = range.xAxisIndex ?? range.yAxisIndex ?? 0;
        const axes = (range.xAxisIndex !== undefined || range.yAxisIndex === undefined ? current.xAxis : current.yAxis) as { type?: string; data?: (string | number | { value: string | number })[] }[] | undefined;
        const axis = axes?.[Array.isArray(axisIndex) ? axisIndex[0] : axisIndex];
        const endpoint = (percent: number) => {
          const values = axis?.type === 'category' ? axis.data : undefined;
          const value = values?.[Math.round(percent / 100 * (values.length - 1))];
          return value === undefined ? `${Math.round(percent)}%` : String(typeof value === 'object' ? value.value : value);
        };
        return [{ index, start: range.start, end: range.end, first: endpoint(range.start), last: endpoint(range.end) }];
      }));
      // The drawing's own legend takes no keyboard focus; the menu repeats its toggles.
      const model = ownLegend ? undefined : legendModel(drawing);
      setEntries(model && model.get('show') !== false && model.get('selectedMode') !== false
        ? model.getData().map((item) => { const name = String(item.get('name')); return { name, shown: model.isSelected(name) }; }) : []);
    };
    sync();
    drawing.on('datazoom', sync);
    drawing.on('finished', sync);
    drawing.on('legendselectchanged', sync);
    return () => {
      if (drawing.isDisposed()) return;
      drawing.off('datazoom', sync);
      drawing.off('finished', sync);
      drawing.off('legendselectchanged', sync);
    };
  }, [controlsContainer, ready, option, ownLegend]);

  useEffect(() => {
    const drawing = chart.current;
    if (!drawing || !ready) return;
    let shown = false;
    const show = () => { shown = true; };
    // Content scrolling under a still pointer moves no pointer, so ECharts would keep its tip
    // and axis pointer drawn over whatever arrives there.
    const hide = () => {
      if (!shown) return;
      shown = false;
      drawing.dispatchAction({ type: 'hideTip' });
      drawing.dispatchAction({ type: 'updateAxisPointer', currTrigger: 'leave' });
    };
    drawing.on('showtip', show);
    document.addEventListener('scroll', hide, { capture: true, passive: true });
    return () => {
      document.removeEventListener('scroll', hide, true);
      if (!drawing.isDisposed()) drawing.off('showtip', show);
    };
  }, [ready]);

  useEffect(() => {
    const element = host.current;
    if (!element || !label) return;
    const wheel = (event: WheelEvent) => {
      // ECharts also captures wheels for graph/tree roaming, outside dataZoom.
      // Keep native scrolling unless the user explicitly asks to zoom.
      if (!event.ctrlKey) event.stopImmediatePropagation();
    };
    element.addEventListener('wheel', wheel, { capture: true, passive: true });
    return () => element.removeEventListener('wheel', wheel, true);
  }, [label]);

  const zoom = (scale: number | 'reset') => {
    const drawing = chart.current;
    if (!drawing) return;
    const ranges = drawing.getOption().dataZoom as ZoomRange[];
    drawing.dispatchAction({ type: 'dataZoom', batch: ranges.flatMap((range, dataZoomIndex) => {
      if (!adjustable(range)) return [];
      const span = scale === 'reset' ? 100 : Math.min(100, Math.max(1, (range.end - range.start) * scale));
      const start = Math.max(0, Math.min(100 - span, (range.start + range.end - span) / 2));
      return [{ dataZoomIndex, start, end: start + span }];
    }) });
  };

  const navigation = controls && (
    <>
    <div role="group" aria-label="Chart zoom" className={cn('z-10 flex items-center gap-1 rounded-sm bg-raised', externalControls ? '' : narrowControls ? 'self-start' : 'pointer-events-none absolute top-1 right-1 shadow-raised opacity-0 transition-opacity group-hover/chart:pointer-events-auto group-hover/chart:opacity-100 group-focus-within/chart:pointer-events-auto group-focus-within/chart:opacity-100 pointer-coarse:pointer-events-auto pointer-coarse:opacity-100 [@media(hover:none)]:pointer-events-auto [@media(hover:none)]:opacity-100 motion-reduce:transition-none')}>
      {externalControls && <span className="mr-auto text-caption text-muted" title="Hold Ctrl while scrolling to zoom">Zoom</span>}
      <Button size="icon-md" aria-label="Zoom in on chart" title="Zoom in (Ctrl+scroll up)" disabled={!ready || error} onClick={() => zoom(0.5)}><Plus /></Button>
      <Button size="icon-md" aria-label="Zoom out on chart" title="Zoom out (Ctrl+scroll down)" disabled={!ready || error} onClick={() => zoom(2)}><Minus /></Button>
      <Button size="icon-md" aria-label="Reset chart zoom" title="Reset zoom" disabled={!ready || error} onClick={() => zoom('reset')}><RotateCcw /></Button>
    </div>
    {externalControls && menuRanges.map((range, i) => (
      <Slider.Root key={range.index} value={[range.start, range.end]} minStepsBetweenValues={1} thumbCollisionBehavior="none" disabled={!ready || error} className="px-2 pt-2 pb-1" onValueChange={([start, end]) => chart.current?.dispatchAction({ type: 'dataZoom', dataZoomIndex: range.index, start, end })}>
        <Slider.Label className="text-caption text-muted">{menuRanges.length > 1 ? `Range ${i + 1}` : 'Range'}</Slider.Label>
        <div className="flex justify-between gap-2 text-meta text-ink"><span className="truncate" title={range.first}>{range.first}</span><span className="truncate text-right" title={range.last}>{range.last}</span></div>
        <Slider.Control className="relative flex h-8 w-full touch-none items-center select-none pointer-coarse:h-11">
          <Slider.Track className="relative h-1 w-full rounded-full bg-selection"><Slider.Indicator className="absolute h-full rounded-full bg-accent" /></Slider.Track>
          {[range.first, range.last].map((value, index) => <Slider.Thumb key={index} index={index} aria-label={`${index === 0 ? 'Start' : 'End'} of chart range${menuRanges.length > 1 ? ` ${i + 1}` : ''}`} aria-valuetext={value} className="size-4 rounded-full border-2 border-accent bg-raised shadow-xs outline-none focus-within:ring-2 focus-within:ring-accent focus-within:ring-offset-2 pointer-coarse:size-5" />)}
        </Slider.Control>
      </Slider.Root>
    ))}
    </>
  );
  const series = externalControls && entries.length > 0 && (
    <ul aria-label="Series" className="flex max-h-48 flex-col overflow-y-auto overflow-x-hidden pt-1 text-caption text-body before:pb-1 before:text-muted before:content-['Series']">
      {entries.map((entry) => (
        <li key={entry.name}>
          <button type="button" aria-pressed={entry.shown} aria-label={`Show ${entry.name} series`} className={cn('flex min-h-7 w-full items-center rounded-xs px-2 text-left [overflow-wrap:anywhere] pointer-coarse:min-h-11', !entry.shown && 'text-muted line-through')} onClick={() => chart.current?.dispatchAction({ type: 'legendToggleSelect', name: entry.name })}>{entry.name}</button>
        </li>
      ))}
    </ul>
  );

  return (
    <div className={cn('group/chart relative flex w-full flex-col', className)} style={{ height: footer ? undefined : height }}>
      <div ref={host} role={label ? 'img' : undefined} aria-label={label} aria-hidden={label ? undefined : true} className={cn('min-h-0 w-full', !footer && 'flex-1')} style={footer ? { height } : undefined} />
      {externalControls ? controlsContainer && createPortal(<>{navigation}{series}</>, controlsContainer) : navigation}
      {legend !== undefined && <div className="pt-1 pl-2">{legend}</div>}
      {error && <Note role="status" className="absolute inset-0 bg-raised px-1 py-2">The chart could not be drawn. Try reopening it.</Note>}
    </div>
  );
}
