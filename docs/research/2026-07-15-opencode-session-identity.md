# OpenCode session identity research

Date: 2026-07-15
Scope: OpenCode v1.18.1 and UAM's `uam-identity-plugin.mjs` bridge

## Conclusion

Keep the generated identity plugin for the current UAM architecture. OpenCode
v1.18.1 has reliable native primitives for resuming an **existing** exact
session, but its supported TUI CLI cannot create a new session with a
caller-selected ID or print the ID of the session it creates. The plugin remains
the smallest supported way for UAM to learn the active root session after TUI
startup and after `/new`.

There is a plugin-free design available through OpenCode's documented server
and SDK: start `opencode serve`, call `POST /session`, retain the returned ID,
then run `opencode attach <url> --session <id>`. That is not a drop-in change.
UAM would become responsible for a second process, local authentication, port
allocation, readiness, shutdown, and event correlation. `/new` would still
require watching the server event stream; a dedicated OpenCode server per UAM
managed session would make that correlation unambiguous.

## Upstream facts

- The latest non-draft, non-prerelease release at the time of research is
  [v1.18.1](https://github.com/anomalyco/opencode/releases/tag/v1.18.1),
  published 2026-07-14 at 21:37:54 UTC.
- The TUI accepts `--continue`/`-c` for the latest session and
  `--session`/`-s` for an exact existing session. `--fork` creates a branch of
  the selected session. These are documented in the
  [CLI reference](https://github.com/anomalyco/opencode/blob/v1.18.1/packages/web/src/content/docs/cli.mdx)
  and defined by the
  [TUI command](https://github.com/anomalyco/opencode/blob/v1.18.1/packages/opencode/src/cli/cmd/tui.ts).
- `--session` is not a create-or-resume flag. The TUI validates the ID before
  starting, and `opencode run --session` fetches the session and exits with
  "Session not found" when it does not exist
  ([validation source](https://github.com/anomalyco/opencode/blob/v1.18.1/packages/opencode/src/cli/tui/validate-session.ts),
  [run source](https://github.com/anomalyco/opencode/blob/v1.18.1/packages/opencode/src/cli/cmd/run.ts#L456-L533)).
- The supported `POST /session` API creates a session and returns the resulting
  `Session`, including its generated ID. Its public request accepts fields such
  as `parentID`, `title`, `agent`, `model`, metadata, permissions, and workspace,
  but not a caller-provided session ID
  ([v1.18.1 OpenAPI](https://github.com/anomalyco/opencode/blob/v1.18.1/packages/sdk/openapi.json#L5404-L5508),
  [server docs](https://github.com/anomalyco/opencode/blob/v1.18.1/packages/web/src/content/docs/server.mdx#L146-L167)).
- OpenCode officially supports `opencode serve`, an SDK client, an SSE event
  stream at `GET /event`, and `opencode attach <url> --session <id>`
  ([server docs](https://github.com/anomalyco/opencode/blob/v1.18.1/packages/web/src/content/docs/server.mdx),
  [SDK docs](https://github.com/anomalyco/opencode/blob/v1.18.1/packages/web/src/content/docs/sdk.mdx),
  [attach source](https://github.com/anomalyco/opencode/blob/v1.18.1/packages/opencode/src/cli/cmd/attach.ts)).
- The OpenAPI also contains a newer `/api/session` surface whose create body can
  accept an `id`. It should not yet be the UAM compatibility contract: upstream's
  own V2 session specification describes the V2 event family as experimental and
  unshipped and lists remaining V1 parity work
  ([V2 specification](https://github.com/anomalyco/opencode/blob/v1.18.1/specs/v2/session.md),
  [OpenAPI preview](https://github.com/anomalyco/opencode/blob/v1.18.1/packages/sdk/openapi.json#L10110-L10188)).
- Plugins and session events are a documented public extension mechanism.
  `session.created` and `session.updated` are listed events, and the published
  plugin type exposes `Hooks.event({ event })`
  ([plugin docs](https://github.com/anomalyco/opencode/blob/v1.18.1/packages/web/src/content/docs/plugins.mdx#L126-L190),
  [plugin types](https://github.com/anomalyco/opencode/blob/v1.18.1/packages/plugin/src/index.ts)).
  The event schema includes both `properties.sessionID` and the full
  `properties.info`, so UAM's current root-ID extraction shape remains valid in
  v1.18.1
  ([event schema](https://github.com/anomalyco/opencode/blob/v1.18.1/packages/sdk/openapi.json#L33885-L33935)).
  No explicit long-term event ABI guarantee was found, so UAM should keep its
  compatibility tests and version probing.

## Multiple sessions and `/new`

OpenCode stores multiple root sessions in one project and can resume each by
exact ID. The ambiguous operation is `--continue`, which deliberately selects
the most recent root session. Therefore multiple concurrent UAM sessions in one
workspace are safe only after each UAM record has learned and retained its own
OpenCode ID; the existing `-c` fallback for legacy/unidentified records can
resume the wrong conversation.

In v1.18.1, `/new` routes the TUI back to its home screen; the next submitted
prompt creates another root session
([TUI command source](https://github.com/anomalyco/opencode/blob/v1.18.1/packages/tui/src/app.tsx#L570-L594)).
UAM's plugin observes that new root `session.created` event and replaces the
identity handoff for that managed UAM session. Consequently, reattaching the UAM
session resumes the conversation created by `/new`; the previous OpenCode
conversation still exists but is no longer the one mapped to that UAM record.
This is expected from the current one-UAM-record-to-one-active-provider-session
model, not evidence that OpenCode lacks multiple sessions.

## Recommendation

1. Retain the plugin and add the guided permission bootstrap for UAM-owned
   directories (`0700`) and the generated module (`0600`).
2. Add an OpenCode v1.18.1 compatibility test for `session.created` and
   `session.updated`, including `/new` replacing only the owning UAM identity.
3. Keep `--session <learned-id>` as the normal resume path and surface a clear
   warning whenever UAM must fall back to `-c` in a workspace with more than one
   retained OpenCode session.
4. Re-evaluate a plugin-free server adapter only when UAM is prepared to own the
   server lifecycle, or after OpenCode promotes caller-supplied session creation
   to its documented stable API/CLI.
