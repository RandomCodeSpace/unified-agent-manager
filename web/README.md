# UAM web interface

Static React + TypeScript single-page app for `uam web`. The browser is
presentation and input only; the Go service in `internal/web` owns sessions
and provider conversations (see `docs/adr/0004-web-interface.md`).

## Build

```sh
cd web
npm ci
npm run build
```

`npm test` runs the reducer, sidebar-grouping, composer, attachment and
folder-path (`tests/folders.test.mjs`) regressions with Node's
built-in test runner; `npm run lint` runs ESLint (typescript-eslint,
react-hooks, jsx-a11y). `npm run build` runs both, type-checks
(`tsc --noEmit`) and writes the production bundle to `../internal/web/dist`,
which the Go binary embeds. Node 22.12+ is
required (`.node-version` pins the version used for CI and release preparation).
The generated `internal/web/dist` directory is ignored on source branches.
`make build` or `make install` from the repository root builds it before Go.
Release tags include it, so versioned `go install` does not need Node.js.
See [release preparation](../docs/releasing.md).

## Develop without the service

```sh
npm run dev
```

Then open the printed URL with `?mock` appended (for example
`http://localhost:5173/?mock`). `src/mock/` replaces `fetch` and
`EventSource` with an in-browser fake of the service, seeded with a few
projects and tasks in every state, that plays scripted replies. It is loaded
only under `vite dev` and only with `?mock`; the production bundle does not
contain it. `#task=<id>` in the URL opens a task directly.

## Layout

- `src/api.ts` — typed mirror of the HTTP/SSE contract (ADR 0004 plus the
  projects, model catalog, title and subagent additions).
- `src/state.ts` — one reducer for server state: projects, sessions, the
  selected session's detail, subagent transcripts (routed by `agent_id`),
  snapshot `seq` gating, service settings, connection status.
- `src/App.tsx` — auth check, the single `EventSource`, the collapsible sidebar
  or narrow drawer, Project filter, navigation (empty pane / task / Settings), the task lifecycle
  actions and confirmations, the project dialogs.
- `src/components/` — Sidebar (search and brand header, flat task list, project filter, task rows with
  hover "…" and context menus, inline rename, Settled/Archived shelves,
  keyboard navigation), Task (the conversation pane: 44px header with
  inline rename, state chip, Subagents and Changes toggles and the actions
  menu; branch/model/context meter line; scrolling transcript with a "New
  output" button; pinned composer; a resizable Changes or Subagents panel
  beside it), Transcript (user bubbles, flat assistant turns, markdown
  everywhere, Thinking disclosures, compact expandable tool summaries with copy
  menus, one compact row per subagent), Subagents (the side panel and the
  shared `SidePanel` with its drag handle), Interactions, Composer (the
  toolbar of model/effort/context/mode pickers plus send, stop, steer and
  queue), Changes (file list and diff), TaskDefaults, Projects (dialogs),
  FolderPicker (the Add project dialog's inline folder browser: breadcrumb,
  listbox, New folder, Use this folder), Settings (service-wide preferences), Login, `taskActions.tsx` (the
  shared Rename/Settle/Reopen/Archive/Delete menu items and their rules)
  Diagram (the `mermaid` card: Diagram / Code toggle, lightbox, fallback note)
  and shared atoms in `common.tsx` (markdown with lazy code highlighting).
- `src/components/ui/` — the Base UI wrappers (button, menu and context
  menu, dialog, alert dialog, sheet, tooltip, select, segmented control) styled with the
  tokens.
- `src/lib/` — `cn` (clsx + tailwind-merge), clipboard helpers, the
  `useResizable` hook, flat sidebar filtering and lifecycle helpers, folder path and breadcrumb logic (`folders.ts`), `diagram.ts` (the frame
  protocol, queue and theme; see ADR 0004) and `highlight.ts` (lowlight
  with the grammars, loaded on demand).
- `src/diagram-frame/` — the entry of the second bundle: the classic script
  that runs Mermaid inside `/diagram-frame.html`, built by the
  `diagramFrame` plugin in `vite.config.ts` after the app.
- `src/index.css` — Tailwind v4 entry: the DESIGN.md tokens as `@theme`
  (colours, type scale, radii, shadows, motion), base styles, and the
  markdown and diff component styles. There is one cool light theme, matching the owner's screenshot references.
- `src/mock/` — development-only fake service (see above).

Fonts are self-hosted from `@fontsource-variable/figtree` and
`@fontsource-variable/jetbrains-mono` (OFL-1.1) and bundled into
`dist/assets`. The bundle runs under a strict CSP (`script-src 'self';
style-src 'self'; font-src 'self'`): no inline scripts, no `<style>`
elements (the app wraps itself in Base UI's `CSPProvider
disableStyleElements`), no inline `style` markup, no external fonts.
Measured values such as the panel width go through CSSOM custom
properties. Mermaid, which needs inline styles, runs only in the sandboxed
frame document, which the service serves with its own policy; the page shows
the result as a `data:` image.

The sidebar lists Tasks without project group headings. Select a Project in the
filter to manage it or open Previous sessions. Task titles use 12px, metadata 11px.
The Copilot provider mark is the official Primer Octicon, vendored as
`src/assets/copilot.svg` with its pinned source and MIT notice. No icon package
or provider lookup is added.
