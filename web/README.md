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
required (CI uses Node 24).

## Layout

- `src/api.ts` — typed mirror of the HTTP/SSE contract.
- `src/state.ts` — one reducer for server state (sessions, selected session
  detail, snapshot `seq` gating, connection status).
- `src/App.tsx` — auth check, the single `EventSource`, layout and drawers.
- `src/components/` — Login, Sidebar, Conversation (header, transcript,
  composer), Interactions, Changes, and shared Dialog/StateBadge/Markdown.

The bundle runs under a strict CSP (`script-src 'self'; style-src 'self'`):
no inline scripts or styles, no external fonts.
