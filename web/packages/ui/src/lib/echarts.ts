import { AriaComponent, CalendarComponent, DatasetComponent, DataZoomComponent, GraphicComponent, GridComponent, LegendComponent, MarkAreaComponent, MarkLineComponent, MarkPointComponent, MatrixComponent, ParallelComponent, PolarComponent, RadarComponent, SingleAxisComponent, TitleComponent, TooltipComponent, TransformComponent, VisualMapComponent } from 'echarts/components';
import { init, registerTheme, use as register } from 'echarts/core';
import { SVGRenderer } from 'echarts/renderers';
import { chartTheme } from './chart.ts';
import { currentScheme } from './theme.ts';

// Loaded only when a chart is drawn; no external scripts or CDN. Each drawing loads the chart
// types it uses beside this (EChart.tsx).
register([
  GraphicComponent, GridComponent, TooltipComponent, DataZoomComponent, AriaComponent, LegendComponent,
  CalendarComponent, DatasetComponent, MarkAreaComponent, MarkLineComponent, MarkPointComponent,
  MatrixComponent, ParallelComponent, PolarComponent, RadarComponent, SingleAxisComponent,
  TitleComponent, TransformComponent, VisualMapComponent, SVGRenderer,
]);

export { init };

const registered = new Set<string>();
/** The registered uam theme of the scheme on screen, built from its tokens the first time it is drawn. */
export function schemeTheme(): string {
  const name = `uam-${currentScheme()}`;
  if (!registered.has(name)) {
    registerTheme(name, chartTheme());
    registered.add(name);
  }
  return name;
}
