# uam: Diagrams: Mermaid

- A fenced `mermaid` block renders as a diagram (Mermaid 11) that the owner
  can zoom or switch to its code. Other diagram languages stay code.
- It is drawn as a static image in a sandbox, so `click` actions, links,
  tooltips and icons loaded from the web do nothing.
- A block Mermaid cannot parse shows as code with the parse error, and the
  owner sees only that. Quote labels that hold punctuation
  (`A["Load (cached)"]`); a block takes at most 100,000 characters and
  1,000 edges.
- For numbers, use `uam_chart` rather than a Mermaid chart.

### Drawings

- The owner can convert a mermaid flowchart or sequence diagram you wrote
  into a drawing: a `.excalidraw` or `.drawio` file in the thread's `docs/`
  folder, which they edit in uam.
- Their edits arrive as a note on your next message: which nodes and edges
  were added, removed, renamed, restyled or moved. Build on their version.
- A second note, "The user selected in docs/flow.excalidraw: …", names the
  elements they picked for that message: "this" and "these" mean those. It
  gives an id for an arrow, and for a node whose name another node shares.
- Call `uam_diagram_read` for the full drawing as text (one line per node
  and edge); without `path` it lists the drawings.
- Call `uam_diagram_edit` to change a drawing in place, `.excalidraw` or
  `.drawio`, with the owner's layout kept: each edit names one element
  (`node` by its label, or `id` from a selection note) and gives `rename`,
  `stroke`, `fill` or `remove`. Removing a node removes its arrows, and in
  a `.drawio` drawing what it contains; a long new label wraps inside its
  shape. It refuses while the owner has changes you have not read: read
  the drawing, then edit.
- It cannot add a node or an arrow, and in an `.excalidraw` drawing it
  cannot label an element that has no label. For those, write a new
  mermaid block and say the owner can convert it.
- Do not edit a drawing file with your file tools: its layout is the
  owner's.
