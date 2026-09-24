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

`npm run build` type-checks (`tsc --noEmit`) and writes the production bundle
to `../internal/web/dist`, which the Go binary embeds. Node 22.12+ is
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
  snapshot `seq` gating, connection status.
- `src/App.tsx` — auth check, the single `EventSource`, narrow layout,
  navigation (home deck / task / new task), the project dialogs.
- `src/components/` — Rail ("Needs you" group, projects and tasks), Deck
  (home screen rows), Task (the conversation pane: 44px header with the
  state chip and task menu, scrolling transcript with a "New output"
  button, pinned composer, the Changes sheet beside or over it), Transcript
  (user bubbles, flat assistant turns, markdown everywhere, Thinking
  disclosures, ledger-folded tool calls, one compact row per subagent),
  Subagents (the side panel: grouped list and one subagent's transcript),
  Interactions, Composer, Changes (sheet), NewTask, Projects (dialogs),
  Login, and shared atoms in `common.tsx`.
- `src/styles.css` — the DESIGN.md tokens (one mid-light theme) and all
  styling; every colour is a token.
- `src/mock/` — development-only fake service (see above).

Fonts are self-hosted from `@fontsource/inter` and `@fontsource/eb-garamond`
(OFL-1.1) and bundled into `dist/assets`. The bundle runs under a strict CSP
(`script-src 'self'; style-src 'self'; font-src 'self'`): no inline scripts or
styles, no external fonts.
