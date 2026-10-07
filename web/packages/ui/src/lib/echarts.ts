import { BarChart, BoxplotChart, CandlestickChart, ChordChart, EffectScatterChart, FunnelChart, GaugeChart, GraphChart, HeatmapChart, LineChart, LinesChart, ParallelChart, PictorialBarChart, PieChart, RadarChart, SankeyChart, ScatterChart, SunburstChart, ThemeRiverChart, TreeChart, TreemapChart } from 'echarts/charts';
import { AriaComponent, CalendarComponent, DatasetComponent, DataZoomComponent, GraphicComponent, GridComponent, LegendComponent, MarkAreaComponent, MarkLineComponent, MarkPointComponent, MatrixComponent, ParallelComponent, PolarComponent, RadarComponent, SingleAxisComponent, TitleComponent, TooltipComponent, TransformComponent, VisualMapComponent } from 'echarts/components';
import { init, use as register } from 'echarts/core';
import { SVGRenderer } from 'echarts/renderers';

// Loaded only when a chart is drawn; no external scripts or CDN.
register([
  BarChart, LineChart, PieChart, ScatterChart, EffectScatterChart, RadarChart, TreeChart, TreemapChart,
  SunburstChart, BoxplotChart, CandlestickChart, HeatmapChart, ParallelChart, LinesChart, GraphChart,
  SankeyChart, FunnelChart, GaugeChart, PictorialBarChart, ThemeRiverChart, ChordChart,
  GraphicComponent, GridComponent, TooltipComponent, DataZoomComponent, AriaComponent, LegendComponent,
  CalendarComponent, DatasetComponent, MarkAreaComponent, MarkLineComponent, MarkPointComponent,
  MatrixComponent, ParallelComponent, PolarComponent, RadarComponent, SingleAxisComponent,
  TitleComponent, TransformComponent, VisualMapComponent, SVGRenderer,
]);

export { init };
