# uam: Surfaces: uam_surface

`uam_surface` draws a live card in the conversation from A2UI v1.0 messages
and returns at once. Use it for a card the owner acts on or one you update as
work goes on, such as a build board or a review queue. Never for a static
report: write Markdown. To ask and wait for answers, use `uam_form`.

Pass `messages`, applied in order, all or none. Each is
`{"version":"1.0", <kind>: {...}}`:

- `createSurface`: `surfaceId`, `catalogId` `"urn:uam:catalog:v1"`,
  `components`, optional `dataModel` (an object). One per call.
- `updateComponents`: `surfaceId`, `components`; each replaces the one with
  its `id` whole, or is added.
- `updateDataModel`: `surfaceId`, `path` (JSON Pointer; `/jobs/-` appends;
  none = whole model), `value` (`null` deletes).
- `deleteSurface`: `surfaceId`.

Components are a flat list `{"id", "component", ...props}`; one has id
`root`, and the others are referred to by id. Any component takes `weight`
(above 0, up to 12): its share of a Row or Column. Catalog:

- Layout: `Card` child · `Column`/`Row` children, justify, align · `List`
  children, direction · `Tabs` tabs [{title, child}] · `Divider` axis ·
  `Modal` trigger (a Button; it needs no action), content.
- Content: `Text` text, variant title|body|caption · `Icon` name (check,
  info, warning, error, star…) · `Status` label, tone
  neutral|running|ok|warn|fail · `Progress` value, max, label · `Image` url,
  description, fit · `Video` url, posterUrl · `AudioPlayer` url, description.
- Input: `Button` child, action, variant default|primary|borderless ·
  `CheckBox` label, value · `TextField` label, value, placeholder, variant
  shortText|longText|number · `ChoicePicker` label, options [{label, value}],
  value (list), variant mutuallyExclusive|multipleSelection · `Slider` label,
  value, min, max · `DateTimeInput` label, value, enableDate, enableTime, min,
  max (ISO 8601).

Media: a `url` is the path of a file in your working folder, relative with `/`
separators, never a web URL. Save the file there first.

Layout: one root `Card`; a `title` Text first; then one `Row` per item,
sized with weights, never a Card in the Card. A job row is `Status`,
`Progress` and a Button; a review row is its Text and a `ChoicePicker`.
Repeat rows with a template rather than one component each.

Any text or value can be bound: `{"@path": "/build/state"}`. Input values are
two-way: the owner's edits land in the data model. A template repeats one
component per list item: `"children": {"componentId": "row", "path": "/jobs"}`;
inside it, a path without a leading `/` is relative to the item.

A Button's `action` is `{"event": {"name": "retry", "context": {"job":
{"@path": "name"}}}}`. A press arrives later as your next prompt, one line:
`[uam_surface action] {"version":"1.0","action":{"name":"retry",
"surfaceId":...,"sourceComponentId":...,"timestamp":...,"context":{...}}}`.

Once drawn, change state with `updateDataModel` at a path, not a redraw.
Errors name the message, component and property; fix and call again.

Example, then later `{"version":"1.0","updateDataModel":{"surfaceId":"ci",
"path":"/jobs/0/tone","value":"ok"}}`:

```json
{"messages":[{"version":"1.0","createSurface":{"surfaceId":"ci",
 "catalogId":"urn:uam:catalog:v1",
 "components":[
  {"id":"root","component":"Card","child":"col"},
  {"id":"col","component":"Column","children":["title","jobs"]},
  {"id":"title","component":"Text","text":"CI on main","variant":"title"},
  {"id":"jobs","component":"Column","children":{"componentId":"job","path":"/jobs"}},
  {"id":"job","component":"Row","children":["st","pg","retry"],"align":"center"},
  {"id":"st","component":"Status","label":{"@path":"name"},"tone":{"@path":"tone"},"weight":2},
  {"id":"pg","component":"Progress","value":{"@path":"pct"},"weight":3},
  {"id":"retry","component":"Button","child":"rl","variant":"borderless","action":{"event":
   {"name":"retry","context":{"job":{"@path":"name"}}}}},
  {"id":"rl","component":"Text","text":"Retry"}],
 "dataModel":{"jobs":[{"name":"lint","tone":"running","pct":40},{"name":"test","tone":"fail","pct":100}]}}}]}
```
