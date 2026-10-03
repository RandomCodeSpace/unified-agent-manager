import type { EChartsOption } from 'echarts';
import type { EChartsType } from 'echarts/core';
import { Minus, Plus, RotateCcw } from 'lucide-react';
import { useEffect, useRef, useState, type ReactNode } from 'react';
import { cn } from '../lib/cn';
import { Note } from './common';
import { Button } from './ui/button';

/** A locally bundled SVG drawing. The owner supplies accessible controls for planner nodes. */
export function EChart({ option, width, height, className, label, zoomControls = false, legend }: Readonly<{
  option: EChartsOption;
  width: number;
  height: number;
  className?: string;
  label?: string;
  zoomControls?: boolean;
  legend?: ReactNode;
}>) {
  const host = useRef<HTMLDivElement>(null);
  const chart = useRef<EChartsType | null>(null);
  const [error, setError] = useState(false);
  const [ready, setReady] = useState(false);
  const zooms = option.dataZoom ? Array.isArray(option.dataZoom) ? option.dataZoom : [option.dataZoom] : [];
  const controls = zoomControls && zooms.some((z) => z.type === 'inside' && !('disabled' in z && z.disabled) && !z.zoomLock);
  const footer = legend !== undefined;

  useEffect(() => {
    const element = host.current;
    if (!element) return;
    let active = true;
    let observer: ResizeObserver | undefined;
    void import('../lib/echarts').then(({ init }) => {
      if (!active) return;
      try {
        const drawing = chart.current ??= init(element, undefined, { renderer: 'svg', width: element.clientWidth || width, height: element.clientHeight || height });
        const resize = () => drawing.resize({ width: element.clientWidth || width, height: element.clientHeight || height });
        resize();
        drawing.setOption(option, { notMerge: true });
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

  const zoom = (scale: number | 'reset') => {
    const drawing = chart.current;
    if (!drawing) return;
    const ranges = drawing.getOption().dataZoom as { type: string; start: number; end: number; disabled?: boolean; zoomLock?: boolean }[];
    drawing.dispatchAction({ type: 'dataZoom', batch: ranges.flatMap((range, dataZoomIndex) => {
      if (range.type !== 'inside' || range.disabled || range.zoomLock) return [];
      const span = scale === 'reset' ? 100 : Math.min(100, Math.max(1, (range.end - range.start) * scale));
      const start = Math.max(0, Math.min(100 - span, (range.start + range.end - span) / 2));
      return [{ dataZoomIndex, start, end: start + span }];
    }) });
  };

  const navigation = controls && (
    <div role="group" aria-label="Chart zoom" className="pointer-events-none absolute top-1 right-1 z-10 flex items-center gap-1 rounded-sm bg-raised shadow-raised opacity-0 transition-opacity group-hover/chart:pointer-events-auto group-hover/chart:opacity-100 group-focus-within/chart:pointer-events-auto group-focus-within/chart:opacity-100 pointer-coarse:pointer-events-auto pointer-coarse:opacity-100 [@media(hover:none)]:pointer-events-auto [@media(hover:none)]:opacity-100 motion-reduce:transition-none">
      <Button size="icon-md" aria-label="Zoom in on chart" title="Zoom in (Ctrl/Cmd + scroll)" disabled={!ready || error} onClick={() => zoom(0.5)}><Plus /></Button>
      <Button size="icon-md" aria-label="Zoom out on chart" title="Zoom out (Ctrl/Cmd + scroll)" disabled={!ready || error} onClick={() => zoom(2)}><Minus /></Button>
      <Button size="icon-md" aria-label="Reset chart zoom" title="Reset zoom" disabled={!ready || error} onClick={() => zoom('reset')}><RotateCcw /></Button>
    </div>
  );

  return (
    <div className={cn('group/chart relative flex w-full flex-col', className)} style={{ height: footer ? undefined : height }}>
      <div ref={host} role={label ? 'img' : undefined} aria-label={label} aria-hidden={label ? undefined : true} className={cn('min-h-0 w-full', !footer && 'flex-1')} style={footer ? { height } : undefined} onWheelCapture={label ? (event) => {
        // Plain wheel scrolls the conversation. Ctrl/Cmd wheel reaches ECharts for zoom.
        if (!event.ctrlKey && !event.metaKey) event.stopPropagation();
      } : undefined} />
      {navigation}
      {footer && <div className="pt-1 pl-2">{legend}</div>}
      {error && <Note role="status" className="absolute inset-0 bg-raised px-1 py-2">The chart could not be drawn. Try reopening it.</Note>}
    </div>
  );
}
