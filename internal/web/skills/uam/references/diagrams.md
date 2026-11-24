# uam: Diagrams: Mermaid

- A fenced `mermaid` block renders as a diagram (Mermaid 11) that the owner
  can zoom or switch to its code. Other diagram languages stay code.
- It is drawn as a static image in a sandbox, so `click` actions, links,
  tooltips and icons loaded from the web do nothing.
- A block Mermaid cannot parse shows as code with the parse error, and the
  owner sees only that. Quote labels that hold punctuation
  (`A["Load (cached)"]`); a block takes at most 100,000 characters and
  1,000 edges.
- A `flowchart`/`graph` of up to 60 nodes opens in a React Flow view drawn
  in uam's own style; the Mermaid image is one toggle away. There:
  - shapes fold into four kinds: `{}` decision, `[()]` store, `(())`/`([])`
    start or end, anything else a step;
  - `style`, `classDef`, `linkStyle` and icons show only in the Mermaid view;
  - list nodes and edges in flow order: the first-listed edge of a cycle
    points forward;
  - subgraphs become labelled groups; dotted (`-.->`) and thick (`==>`)
    links keep their look.
- Other box-and-arrow diagrams (state, class, ER and the like) open as the
  Mermaid image; the owner can still ask for the React Flow view, which
  converts them with a model call.
- For numbers, use `uam_chart` rather than a Mermaid chart.
