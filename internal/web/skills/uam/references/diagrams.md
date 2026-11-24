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
