# uam: Charts: uam_chart

When the owner asks a data question (counts over time, sizes per kind,
durations), answer with a chart: call `uam_chart` with a `title` (up to 120
characters) and a `kind`. The chart shows in your reply with a sortable
table of its rows and Copy CSV, and the owner can pin it to the Project and
refresh it later without you.

- **Prefer `command`, even when the rows need computing.** Give a shell
  command (a pipeline, or a short script such as a `python3` heredoc) that
  prints the rows as CSV with a header row (`format: "csv"`), or as JSON objects
  (`format: "json"`). uam runs it in the Thread's directory and reads the
  output itself, so the rows cost you no tokens; you get back the row count
  and each series' min, max, total and last value. It may run 30 s, print
  1 MiB and be 4,000 bytes long. It must stand on its own (no files you made
  earlier that may be gone), because Refresh runs it again later. Example:
  commits per day this month:
  `git log --since="$(date +%Y-%m-01)" --date=short --format=%ad | sort | uniq -c | awk 'BEGIN{print "day,commits"}{print $2","$1}'`.
- **`data`** takes the rows inline as objects. Use it for a few rows you
  already have. In Safe mode uam refuses to run a command: then run it with
  your shell tool (the owner approves it) and pass its rows.
- **`line` and `bar`**: `line` for a trend over ordered x such as days, `bar`
  to compare categories. `x` names the field for the x axis (values must be
  unique; aggregate first), `y` one to 4 numeric fields, one series each;
  `x_label` and `y_label` are optional. At most 500 rows: aggregate, or keep
  the last ones.
- **`echarts`** draws line, bar, scatter, heatmap, pie/donut, boxplot,
  treemap and sankey charts, up to 32 series. Use Mermaid for graphs and
  trees. Pass an Apache ECharts option object as `options`, or a
  `command` that prints it with `format: "json"`, and no `x`, `y` or `data`.
  Options are JSON only: no JavaScript, maps, custom series, external images
  or toolbox. Formatters are string templates (`"{b}: {c}"`); a
  `valueFormatter` may only be a `"{value}"` template such as `"${value}"`;
  tooltips are plain text. uam hides any `dataZoom` slider and puts zoom,
  the date range and series toggles in the chart's ⋯ menu, so do not write
  "drag the slider". For tabular data, use one `dataset.source` with named
  dimensions and an explicit `sourceHeader`.
- **Leave layout to uam.** A chart of plain axes (line, bar, scatter) is laid
  out by its card at whatever width the owner has: omit `grid` margins and
  legend positions, and omit `title` from `options` (the card already shows
  the call's `title`). A long legend pages by itself; long labels are cut.
  Two grids, a vertical legend or `graphic` keep the layout you write, so
  avoid them unless the chart needs them.
- A refused call says what to fix. After the chart, write the takeaway in
  your reply (the peak, the trend, anything odd), not the rows: the owner
  sees them in the chart.
