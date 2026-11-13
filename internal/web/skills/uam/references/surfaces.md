<!-- PROTOTYPE: uam_surface (A2UI v1.0). Throwaway. -->
# uam: Surfaces: uam_surface

`uam_surface` draws a live card in the conversation from A2UI v1.0 messages
and returns at once. Use it for a card the owner acts on or one you will
update as work goes on, such as a build board. Never for a static report:
write Markdown. To ask and wait for answers, use `uam_form`.

Pass `messages`, applied in order, all or none. Each is
`{"version":"1.0", <kind>: {...}}`:

- `createSurface`: `surfaceId`, `catalogId` `"urn:uam:catalog:v1"`,
  `components`, optional `dataModel` (an object).
- `updateComponents`: `surfaceId`, `components`; each replaces the one with
  its `id` whole, or adds it.
- `updateDataModel`: `surfaceId`, `path` (JSON Pointer; none = whole model),
  `value` (`null` deletes).
- `deleteSurface`: `surfaceId`.

Components are a flat list `{"id", "component", ...props}`; one has id
`root`, and children refer to ids. Catalog:
`Text` text, variant caption|body · `Row`/`Column` children, justify, align ·
`List` children, direction · `Card` child · `Tabs` tabs [{title, child}] ·
`Divider` axis · `Button` child, action, variant default|primary|borderless ·
`CheckBox` label, value · `TextField` label, value, placeholder, variant
shortText|longText|number · `ChoicePicker` label, options [{label, value}], value
(list), variant mutuallyExclusive|multipleSelection · `Slider` label, value, min, max ·
`Status` label, tone neutral|running|ok|warn|fail · `Progress` value, max,
label · `Icon` name (check, star, warning…) · `Image` url, description ·
`Video` url, posterUrl · `AudioPlayer` url, description · `Modal` trigger (a
Button; opening sends nothing), content · `DateTimeInput` label, value,
enableDate, enableTime, min, max (ISO 8601).

A media `url` is the path of a file in your working folder, never a URL:
save the file there first.

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
  {"id":"col","component":"Column","children":{"componentId":"job","path":"/jobs"}},
  {"id":"job","component":"Row","children":["st","retry"],"justify":"spaceBetween"},
  {"id":"st","component":"Status","label":{"@path":"name"},"tone":{"@path":"tone"}},
  {"id":"retry","component":"Button","child":"rl","action":{"event":
   {"name":"retry","context":{"job":{"@path":"name"}}}}},
  {"id":"rl","component":"Text","text":"Retry"}],
 "dataModel":{"jobs":[{"name":"lint","tone":"running"},{"name":"test","tone":"fail"}]}}}]}
```
