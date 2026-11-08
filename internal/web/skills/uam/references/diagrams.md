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
- Call `uam_diagram_read` for the full drawing as text (one line per node
  and edge); without `path` it lists the drawings.
- Do not edit a drawing file with your file tools: its layout is the
  owner's. To change the picture, write a new mermaid block and say the
  owner can convert it.
