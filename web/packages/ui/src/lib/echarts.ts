import { AriaComponent, CalendarComponent, DatasetComponent, DataZoomComponent, GraphicComponent, GridComponent, LegendComponent, MarkAreaComponent, MarkLineComponent, MarkPointComponent, MatrixComponent, ParallelComponent, PolarComponent, RadarComponent, SingleAxisComponent, TitleComponent, TooltipComponent, TransformComponent, VisualMapComponent } from 'echarts/components';
import { init, use as register } from 'echarts/core';
import { SVGRenderer } from 'echarts/renderers';

// Loaded only when a chart is drawn; no external scripts or CDN. Each drawing loads the chart
// types it uses beside this (EChart.tsx).
register([
  GraphicComponent, GridComponent, TooltipComponent, DataZoomComponent, AriaComponent, LegendComponent,
  CalendarComponent, DatasetComponent, MarkAreaComponent, MarkLineComponent, MarkPointComponent,
  MatrixComponent, ParallelComponent, PolarComponent, RadarComponent, SingleAxisComponent,
  TitleComponent, TransformComponent, VisualMapComponent, SVGRenderer,
]);

export { init };
