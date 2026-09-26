# UAM web lazy data: ideas log

Status: brainstorm only. Not part of the acceptance contract in [the implementation plan](2026-09-25-web-lazy-data-and-cache.md) and not authorized for implementation. Kept separate so the implementing agent's plan and [results](2026-09-25-web-lazy-data-and-cache-results.md) files have one writer.

Author: the reviewer of sections 14 and 17 in the [review history](2026-09-25-web-lazy-data-and-cache-review-history.md). Each idea records what was checked, the reasons, and a recommendation. Nothing here was implemented or benchmarked in a browser.

Baseline assumed: the implementation described in the results file on 2026-09-26: one selected-task EventSource, one optional detail EventSource (`web/src/components/Details.tsx:139`, `internal/web/detail_events.go`), compact items, body reads with epoch/request guards, covered-sequence acknowledgements on detail reconnect, and a five-page recent-task cache.

## 0. Correction to the earlier review

Section 14 of the review history says compression is not a missing win. That is wrong for the event stream. The 2026-09-25 probe hit `/api/meta`, a JSON route. A 2026-09-26 probe of `/api/events` on the deployed host returned `content-type: text/event-stream` with no `Content-Encoding`, and the body began with plain `retry:`. The Caddy site config excludes the stream on purpose:

```text
# Compress the page, assets and JSON. The live event stream
# (text/event-stream) is left uncompressed so events are never buffered.
```

So the 3–4 MB EventSource transfer in the original report was raw bytes. Idea 1 follows from this.

## 1. Compress the event streams in the server

### Evidence

Deflate ratios computed over the retained 218-item capture, with a sync flush after every frame so the numbers reflect streaming with per-connection context, not whole-file compression:

| Stream shape | Raw bytes | Compressed | Ratio |
|---|---:|---:|---:|
| Full legacy snapshot frame, level 1 | 434,969 | 144,445 | 3.0x |
| One `item` frame per item, full bodies, level 1 | 448,099 | 148,809 | 3.0x |
| One `item` frame per item, compact records, level 1 | 52,437 | 13,035 | 4.0x |
| 500 small `delta` frames, level 1 | 74,500 | 6,141 | 12.1x |

Delta-heavy streaming, which is what a working agent produces, compresses best because every frame repeats the same keys, session ID and item ID.

### Proposal

- When the request's `Accept-Encoding` includes `gzip`, wrap both SSE handlers' writes in `compress/gzip` at `BestSpeed`. After each frame and each heartbeat, flush the gzip writer, then the `http.Flusher`. Set `Content-Encoding: gzip` and `Vary: Accept-Encoding`.
- Caddy needs no change. Its `encode` matcher does not include `text/event-stream`, so it passes the encoded body through, and `flush_interval -1` already forwards each flush.
- Standard library only. Roughly 30 lines in the SSE write loop.

### Reasons

- It attacks the exact metric the user reported, on every deployment: the public host, direct loopback, and the private VM whose proxy is unknown. Changing Caddy would cover only the public host.
- Compression runs in each stream's writer goroutine, outside `m.mu`. Frames are still encoded once under the lock as today.
- Decompression happens in the browser's network stack, so it adds no main-thread work.
- It is independent of every other open decision and also applies to WebSockets if Idea 2 is ever adopted, where `permessage-deflate` is the equivalent.

### Costs and risks

- Memory: the standard library's deflate compressor holds fixed hash tables, on the order of several hundred KiB to about 1 MiB per open stream. This is an estimate, not a measurement. It scales with open streams, which are two per tab at most.
- The same frame is compressed once per viewer, because each connection keeps its own context. Negligible at this scale.
- Incremental decoding must be verified in a browser test that asserts a frame is dispatched before the stream ends, in Chromium and Firefox at minimum, Safari if available. Some intermediaries buffer compressed streams; `X-Accel-Buffering: no` is already sent.
- 🔒 The same transcript content is already gzip-compressed on the JSON detail and history routes, so this adds no new exposure class.

### Recommendation

Do this first, after the current implementation lands. It is the largest remaining byte reduction for the smallest change. Measure encoded bytes per minute of an active agent before and after.

## 2. WebSocket instead of EventSource

### What was checked

- Deployed path: browser to Caddy 2.11.4 over HTTP/2, with HTTP/3 advertised. Caddy proxies to the service over HTTP/1.1.
- Direct access: `internal/web/daemon.go` serves a plain `http.Server` with no TLS. Browsers never use cleartext HTTP/2, so loopback access is HTTP/1.1.
- Library: `github.com/coder/websocket v1.8.15` is already in the module graph as an indirect dependency through `github.com/github/copilot-sdk/go`. It is ISC-licensed. Promoting it to a direct dependency adds no new module. It still needs a `govulncheck` pass under the pinned toolchain.
- Request guards: `http.CrossOriginProtection` runs only for unsafe methods (`server.go`, around line 225). A WebSocket upgrade is a GET, so it would not be covered.
- CSP: `connect-src 'self'`.

### What it would buy

1. **One ordered channel for data and interest changes.** Opening a subagent, revealing a body, or closing one becomes a small message on the same socket. The server handles it under `m.mu`, installs the interest, and enqueues the snapshot or body into the same subscriber queue. Every later frame follows it. That is the atomicity `Subscribe` already provides, extended to interest changes. It would remove the cross-stream ordering rules, the detail reconnect on every disclosure change, the covered-sequence acknowledgement protocol, and most request-token checks for bodies delivered in-band. The client only needs to echo a counter so it can drop replies for interests it has since closed.
2. **No HTTP/1.1 connection-pool exhaustion on loopback.** Browsers allow six HTTP/1.1 connections per host. With one main and one detail stream per tab, three tabs with an open detail use all six, and every fetch from every tab to that host queues behind them. The deployed host is unaffected because HTTP/2 multiplexes. WebSocket connections are not drawn from that pool in current browsers; this is a browser implementation detail and should be verified.
3. **Task switch without reconnect.** Small. On the deployed HTTP/2 host a reconnect is one request, and it saves about 4 KB of global snapshot. This is not a reason to adopt it.

### What it would cost

- 🔒 **Origin check is mandatory.** EventSource is protected by CORS read rules; a WebSocket has none. Use the library's default same-origin check and pass the configured public origins as allowed patterns. Keep every state-changing action on HTTP POST so the existing cross-origin and JSON checks and request IDs still apply. The socket carries view interests only. Cap client message size, reuse the existing interest bounds, and rate-limit interest messages.
- **CSP.** CSP Level 3 browsers match same-host `wss:` under `'self'`. Verify Safari; otherwise add the explicit origin.
- **Reconnect is hand-written.** EventSource reconnects on its own with the `retry: 2000` hint. A WebSocket client needs backoff with jitter and a ping to detect dead connections. Small.
- **Head-of-line blocking.** A 256 KiB tool output or a large reasoning body sent in-band delays chat frames behind it. Deliver only live bodies in-band, under a cap, and keep large completed bodies on the existing HTTP read path.
- **Compression.** Without `permessage-deflate`, a WebSocket is as uncompressed as today's stream. The library supports it and Caddy passes the extension through.
- **Fallback.** Some proxies block WebSockets, and the private VM's network is unknown. Fall back to the legacy recent-history SSE path with full items, which already exists for old clients. Do not also keep the two-stream lazy SSE mode as a fallback, or there are three transports to maintain.
- **Rewrite risk.** It replaces about 460 lines of `detail_events.go`, the detail stream in `Details.tsx`, and their tests, all of which were just verified by the implementing agent.

### Will it make frontend performance better?

Not materially. Both transports deliver one string per message as a `MessageEvent` on the main thread. `JSON.parse`, the reducer, and React rendering dominate, and those are identical either way. Moving parsing off the main thread is possible with both, since EventSource also works in dedicated workers. The only indirect gain is fewer detail-stream reconnect snapshots re-hydrating held bodies, and the implementing agent already cut that cost with unchanged-body acknowledgements. The frontend levers that do matter are in Idea 3.

### Recommendation

Not now. Land and measure the current implementation first. Revisit WebSockets as a simplification refactor if any of these happen:

- Cross-stream race bugs keep appearing between the main and detail streams.
- Connection-pool exhaustion is observed on loopback or the private VM.
- The epoch, covered-sequence, and acknowledgement protocol becomes the maintenance cost.

A cheap mitigation for the pool issue without WebSockets: close the detail stream while the tab is hidden and reopen it on `visibilitychange`.

WebTransport was considered and rejected. It needs a QUIC server dependency, Safari support is recent, and nothing here needs unreliable datagrams.

## 3. Frontend per-frame costs that are independent of transport

Unprofiled candidates found by reading the current code. Measure before changing any of them.

- **Buffered frames are serialized a second time.** `web/src/state.ts:18` and `web/src/lib/detail-state.ts:167` call `JSON.stringify` on every buffered frame to account its size. The raw event string's length is already known in the listener before `JSON.parse`. Passing it through removes one full serialization per buffered frame, which matters most for large tool-output frames during hydration.
- **Arrays are copied per frame.** `appendDelta` and `upsert` in `state.ts` copy the item array for every frame, so a batch of deltas copies the window once per delta. The window is at most 150 items, so this is likely small.
- **Cache admission encodes each item.** `web/src/lib/recentTasks.ts:47` runs `TextEncoder` over each item's JSON on admission. It runs only on admission, so it is likely fine.

The larger frontend costs remain the ones in the plan's scrolling section: page insertion, grouping, and Markdown rendering. A CPU trace should attribute them before any of the above is touched.

## 4. Sequencing against the in-progress implementation

Decision recorded 2026-09-26: the plan implementation continues unchanged. Ideas in this log are out of plan scope.

- **Code waits.** Ideas 1 and 2 touch the SSE write loops in `internal/web/server.go` and the detail stream, which the implementing agent is editing in this worktree. That work is uncommitted, so there is no branch to base a parallel change on. Adding compression mid-implementation would also confound the byte measurements the results file reports; each change should be measured on its own.
- **De-risking does not wait.** The one open unknown for Idea 1 is whether browsers dispatch gzip-encoded EventSource frames one at a time through Caddy. That can be answered now with a throwaway prototype outside this worktree: a minimal Go SSE server with per-frame gzip flush behind a local Caddy site using the same `encode` matcher and `flush_interval -1`, checked in Chromium and Firefox. It touches no repository file.
- **After the implementation is committed:** land Idea 1 as its own small change and measure encoded bytes per minute of an active agent before and after. Decide on Idea 2 only from measurements of the finished two-stream implementation, against the triggers listed in Idea 2.

## 5. UI stack: Tailwind, shadcn/Base UI, and whether a lighter framework helps

Question from the owner, 2026-09-26: are Tailwind and shadcn hurting frontend performance, and should the UI move to a lighter framework?

### What was measured

Offline measurements on the dev host. Render timings use React's server renderer in Node over the 27 real assistant messages in the retained capture (43,853 characters). Server rendering excludes DOM creation, effects and layout, so these are relative costs, not browser timings. The stylesheet is the one the deployed host served on 2026-09-26.

| Item | Result |
|---|---:|
| Deployed stylesheet | 64,901 bytes raw, 12,904 gzipped, about 864 rules |
| `:has()` selectors, mask images, blur or backdrop filters in use | none |
| `cn()` call, cached (tailwind-merge LRU hit) | 0.06 µs |
| `cn()` call, uncached | 9.2 µs |
| 27 messages as plain text | 0.63 ms, 28 elements |
| Same, each wrapped like `Copyable` (context menu, tooltip, button) | 8.19 ms, 163 elements |
| 27 messages through `react-markdown` + `remark-gfm` | 40.99 ms, 628 elements |
| Same, with the wrapper | 51.67 ms, 763 elements |
| Markdown parse to HTML syntax tree only (remark-parse, gfm, remark-rehype) | 34.07 ms |
| `marked` lexer, GFM, same messages | 2.10 ms |

The `backdrop-filter` and `blur` strings in the stylesheet are false positives: one is Tailwind's generic `.transition` property list, the other an unused `.blur` utility generated from a stray word in source. No source file uses them.

### Findings

1. **Tailwind has no measurable runtime cost.** It is build-time CSS. The stylesheet is small and uses only cheap selectors. tailwind-merge costs 0.06 µs per cached call; class strings repeat across rows, so nearly every call hits the cache.
2. **shadcn itself is not the cost; how its Base UI pieces are mounted is.** shadcn is source copied into `web/src/components/ui/` over `@base-ui/react`. Every user and assistant message mounts a context-menu root, a tooltip root, and a button through `Copyable` (`web/src/components/Transcript.tsx:516`), and every opened tool row mounts a tooltip and a menu. That adds about 0.28 ms and five elements per message in the render measurement, before any DOM or effect cost.
3. **Markdown dominates, and most of it is the parser, not React.** About 83% of `react-markdown`'s time is the remark/unified parse, which is framework-independent. Streaming is already handled well: `Markdown` in `web/src/components/common.tsx:698` splits text into blocks and memoizes each, so only the last block re-parses while text streams.
4. **A lighter framework would not fix this.** Preact, Solid or Svelte change only React's share. The parser cost stays. Preact would save roughly 40 KB gzipped of initial download but risks compatibility with Base UI and the React 19 view-transition APIs used in `App.tsx` and `Sidebar.tsx`. Solid or Svelte means rewriting about 15,600 lines of frontend code.

### Ideas, in order of payoff per effort

1. **Replace the remark pipeline with `marked`'s lexer, rendered to React elements.**
   - Reason: the lexer parses the same messages in 2.10 ms against 34.07 ms, about 16 times faster. `marked` 16.4.2 is MIT-licensed and already in the dependency tree through `mermaid`. Rendering its tokens to React elements, not an HTML string, keeps raw HTML escaped and keeps the existing `CodeBlock`, Mermaid and link handling. It would likely shrink the main bundle too, by dropping `react-markdown`, `remark-gfm` and the unified stack. That is an estimate; measure the bundle.
   - Risk: `marked` is less strictly CommonMark-compliant than micromark. Add golden tests rendering the captured messages both ways, and review the differences before switching.
2. **Mount the menu and tooltip only when used.**
   - Reason: render the copy button as a plain button, and mount the Base UI tooltip and context menu on first hover, focus or right-click. An alternative is one shared menu and tooltip per transcript, driven by event delegation. Either keeps Base UI's accessibility and positioning while removing per-row cost. It would save about 7.5 ms per 27 messages in this measurement; after idea 1, that becomes close to half of the remaining render cost.
   - Risk: keyboard and screen-reader behavior must be retested. Browser-native `popover` with CSS anchor positioning is a later option once support is confirmed in every target browser.
3. **Keep Tailwind, tailwind-merge and React.** No measured cost justifies changing them.

Estimated combined effect on the 27-message case: about 51.7 ms down to roughly 10 ms of render work. This is an estimate from the measurements above, not a browser result. Confirm it with a CPU trace of page insertion in the browser fixture before and after.

### Sequencing

Both ideas touch `Transcript.tsx` and `common.tsx`, which the implementing agent is editing. They wait for the implementation to be committed, per section 4. The golden-test comparison for idea 1 can be prepared now outside the repository.

## 6. Parity check: `marked` in place of `react-markdown`

Question from the owner, 2026-09-26: would switching to `marked` lose existing functionality?

### Method

Both pipelines rendered every assistant, reasoning and notice message in the retained capture, plus 36 probe documents covering GFM, CommonMark edge cases, raw HTML, entities and URL handling. Outputs were compared as normalized tag-and-text sequences. Both used the same URL sanitizer, so only parsing differs. Streaming was tested by rendering each real message at 25 cut points. Scripts: `/tmp/uam-ui-perf/parity.mjs` and `parity2.mjs`, outside the repository.

Two test artifacts were filtered out. React's server renderer inserts image preload links, and the two sanitizers blank an unsafe URL differently: one omits `src`, the other leaves it empty.

| Check | Identical | Different |
|---|---:|---:|
| Real messages, whole text | 83 of 85 | 2, both raw HTML |
| Real messages, streaming prefixes | 2,122 of 2,125 | 3: 2 raw HTML, 1 transient list-versus-paragraph |
| Real messages, `marked` per block versus whole text | 85 of 85 | 0 |
| Probe documents | 28 of 36 | 8: footnotes, 3 raw HTML, entities, 3 test artifacts |

The per-block result matters because `splitBlocks` (`web/src/lib/markdown.ts`) is what keeps streaming cheap. It holds with `marked`.

### Findings

Identical on every probe for tables with alignment, task lists, strikethrough, bare, `www.` and email autolinks, angle autolinks, setext and closing-hash headings, reference links, escapes, intraword underscores, emphasis edge cases, nested loose lists, ordered list start numbers, lazy blockquotes, backtick code spans, links containing parentheses, tables without leading pipes, tilde fences, fences with info attributes, indented code and unclosed fences.

Real differences, each with its fix:

1. **Footnotes are not supported by `marked`.** `remark-gfm` renders them. `marked` treats `[^1]: note` as a reference definition and links the marker to the relative URL `note`, which the file-link code would then try to resolve. None of the 85 real messages uses footnotes. Fix: add `marked-footnote` after a dependency review, or render footnote syntax as plain text and accept the loss explicitly.
2. **Raw HTML is parsed, not shown as text.** Today raw HTML, including comments, appears as literal text. `marked` produces `html` tokens. 🔒 The React renderer must output those tokens as plain text and must never use `dangerouslySetInnerHTML`. Two real messages contain a raw `<img>` tag, so this is exercised by real data.
3. **Entities are not decoded in lexer tokens.** The lexer leaves `&amp;` and `&copy;` as written, so rendering token text directly would show them literally. The renderer must decode named and numeric references outside code spans. `decode-named-character-reference` does this and is currently a transitive dependency through micromark; it would need to become direct once `react-markdown` is removed.
4. **The URL sanitizer goes away with `react-markdown`.** `mdUrl` in `web/src/components/common.tsx` wraps react-markdown's `defaultUrlTransform`. Port its protocol allowlist exactly so links and images behave as today.

Behavior that must be ported, not lost:

- **Mermaid readiness while streaming** reads syntax-tree offsets and `fenceClosed`. `marked` tokens carry no offsets; derive readiness from whether the code token's `raw` ends with a closing fence.
- **Code blocks** get simpler. `marked`'s single `code` token carries `lang` and `text`, where `react-markdown` split them across `pre` and `code`.
- **Everything else stays as React components**: `MdLink`, `MdImage`, `LocalImage` with the lightbox, `InlineCode` file probing, `CodeBlock` with copy, `lowlight` highlighting, and the per-block memoization.

Minor: one of 2,125 streaming prefixes briefly rendered a list as a paragraph, where the text was cut right after a list marker. It corrects itself when the next characters arrive.

Report-only, unrelated to the switch: `defaultUrlTransform` strips `data:` URLs, so `MdImage`'s inline `data:`/`blob:` branch is unreachable from Markdown today. A `data:` image renders as an image note. Keep that behavior during the switch and decide separately whether it is intended.

### Conclusion

Nothing the real captured data uses would be lost. Footnotes would be lost unless an extension is added. Raw HTML, entity decoding and URL sanitizing need deliberate handling in the new renderer, and each needs a regression test. Keep these parity scripts as golden tests: every real message and probe must match before the switch ships.

## 7. Response to the isolated Marked assessment

Source: `/tmp/uam-markdown-eval-20260926/assessment.md`, 2026-09-26, by another agent. Reviewed and partly re-verified here; its benchmarks were not rerun.

### Agreed

- **The payoff is long replies and streaming, not startup or task switching.** Re-verified: the capture's recent 50-item page holds 31 tool items, 17 reasoning items and 2 assistant messages totaling 939 characters. Compact mode keeps reasoning text unloaded until it is revealed, so Markdown on first paint is about one millisecond either way. Section 5 was right that the parser is about 16 times faster, but wrong to imply this addresses page-insertion or cold-start stalls. The 84–96 ms cold-start long task is still unattributed and needs a CPU profile.
- **The measured wins are real where they apply.** The assessment reports 48.4 ms down to 4.3 ms for all 27 assistant messages, and 170 ms down to 26 ms total for 100 streamed updates of the longest reply. That matches section 5's parse-only measurement.
- **No off-the-shelf adapter.** `marked-react` decodes `&amp;` inside inline code, changing displayed code, and shows footnote source literally. Use Marked's lexer with a UAM-owned token renderer that keeps the existing React components, as section 6 proposed.
- **Never insert `marked.parse()` output into the page.** Marked does not sanitize.

### Added by the assessment

- **Code blocks lose their trailing newline.** Re-verified: Marked's code token text for a one-line fence is `"const a = 1"` with no trailing newline, while the current pipeline keeps one. Copy-code reads the rendered text, so copied content would change by one newline. Decide explicitly which is correct and pin it with a test.
- **Version choice affects the "already installed" argument.** `mermaid` requires `marked ^16.3.0`. The assessment evaluated 18.0.14. Choosing 18 adds a second major version to the tree. It would not ship twice to the main page, because Mermaid lives in the separate diagram-frame entry, but section 5's "already in the tree" point holds only for a 16.x pin. Pick the version during the dependency review.

### Revised priority

Marked is worth doing for streaming smoothness on long replies and for opening long assistant messages. It does not belong on the startup-performance path. Order after the in-progress implementation lands:

1. CPU-profile the cold-start long task and page insertion. Fix what the profile attributes.
2. Compress the event streams, per section 1.
3. Move Markdown to Marked's lexer with the section 6 and section 7 parity gates, if streamed long replies still stutter in a browser trace.
4. Lazy-mount the per-message menu and tooltip, per section 5, if the profile shows them.

## 8. File and link references: tool, MCP, or server resolver

Discussion moved to GitHub issue #227, created 2026-09-26. Summary of the reviewer's position:

- The browser guesses paths from inline code and sends one `HEAD` per unique candidate, two with sign-in on. On the captured task that is 17 unique probes, 34 requests, and 10 404s.
- Recommended first: a bounded server-side batch resolver that reuses `resolveTaskFile` outside `Manager.mu`, invalidation from `edit`/`write`/`create` completions, resolution only near the viewport, and the same treatment for links and images.
- A custom tool works only for Copilot, the only registered web provider, through `SessionConfig.Tools`. It costs model turns, needs `SkipPermission`, and cannot replace the heuristics, because old conversations and non-compliant models still write Markdown paths. Deferred until there is an explicit product need. An MCP server is only needed for OpenCode, which is not a registered web provider.
- The agreed UI performance order on issue #226 is separate and final.

## 9. Owner follow-up on #227: files outside the project directory, and in-app preview

Questions from the owner, 2026-09-26: should files outside the project directory be reachable, and can a file open as a preview inside the web UI instead of a new tab?

### Security context

🔒 The public deployment runs with `--no-auth` by owner decision. On 2026-09-26 an unauthenticated `GET /api/sessions` returned 200. Anyone who reaches the host can list tasks and read any file inside a task's workdir through the view route. This is existing, owner-accepted behavior and report-only here. The consequence for this question: any root the file routes may serve must be treated as public.

**Resolved 2026-09-26:** at the owner's request the deployment now requires sign-in. The access token was rotated, and unauthenticated API, event-stream and file requests return 401. The deploy helper no longer passes `--no-auth` and asserts the 401. Later the same day the owner decided `--no-auth` is removed from the product entirely; authenticated operation is the only mode (pending implementation, agreed on #227). Workdir confinement still stands, and outside-root exceptions were rejected on #227 on their own merits: tool paths are display metadata, not serving grants.

### Outside the project directory: evidence

Tool calls in the captured task that named paths outside the workdir:

- `view` of an agent skill configuration file under the home directory.
- `view` of the Copilot CLI's own session-state directory for that session.
- A shell command writing an HTML report to `/tmp`.
- A shell command reading a Copilot tool-output dump in `/tmp`.

Only the `/tmp` HTML report is something a user plausibly wants to open. It was written by a shell command, so a rule based on file-tool paths would not catch it. The rest is provider internals and configuration, which should never be served.

### Outside the project directory: recommendation

- **Keep workdir confinement as the default.** Serving `$HOME`, `/tmp` or provider state on a no-sign-in host exposes credentials, other processes' temporary files and tool-output dumps to anyone.
- **Narrow extension, if wanted:** exact paths that the session's own `create`, `edit` or `write` tools produced outside the workdir.
  - Served read-only and re-resolved at request time.
  - Regular files only. The path after symlink resolution must equal the recorded path.
  - Never under home-directory dot-folders or provider state directories.
  - Size-capped.
  - Reason: the content of a created file is already in the transcript, so exposure does not grow.
  - Limitation: it does not cover files written by shell commands.
- **For shell-written reports,** the better fix is getting agents to write artifacts inside the workdir, for example a `.uam/out/` folder. That needs session instructions, and UAM injects none today by design, so it is an owner product decision.
- **Rejected:** configurable extra roots such as `/tmp` or `$HOME` while `--no-auth` is in use. With sign-in on, they could become an explicit owner opt-in, off by default.

### In-app preview: recommendation

Yes. It fits the #227 design, because a preview loads only when clicked. It adds nothing to page load and needs no probing.

| Type | Preview | Existing pieces |
|---|---|---|
| Images | Already in-app | `LocalImage` lightbox |
| HTML reports | Sandboxed iframe of the same view or file-key URL | View route and its CSP sandbox; the diagram-frame pattern |
| Text, code, Markdown, JSON | Fetched through the view route with a size cap, rendered in-app | `CodeBlock`, highlighter, `Markdown` |
| PDF | Sandboxed iframe | Chromium's viewer already renders under the sandbox; Firefox and Safari unverified |
| Other binary | Name, size, download | View route attachment disposition |

- **Placement:** a side panel like Changes and Subagents on wide screens, a full-screen sheet on narrow ones. "Open in new tab" stays as a secondary action.
- **Required header change:** view responses currently inherit the global `X-Frame-Options: DENY`, so they cannot be framed. Add `frame-ancestors 'self'` and `X-Frame-Options: SAMEORIGIN` to view responses only, as the Mermaid diagram frame does.
- 🔒 **Keep the opaque origin.** Keep the CSP `sandbox` directive, and give the iframe a `sandbox` attribute without `allow-same-origin`. An agent-written HTML file must never run with the app's origin. Relative assets keep working through the file-key path, as they do in a new tab today.
- **Performance:** text previews must respect a byte cap, for example 1 MiB. Larger files show metadata and a download action.

Posted to #227 for the implementing agent.

## 10. Files in the system temp directory

Owner, 2026-09-26: most agent output lands in the project or in `/tmp`. The #227 amendment deferred outside roots to their own scope. Proposal posted on #227 as items T1 to T6: explicit absolute temp-directory paths only; server checks for containment, own uid, link count 1 and creation after the task started; a separate per-file or task-created-subdirectory signed key with a short expiry, bound to the inode, never the workdir key; single-file grants for files directly in `/tmp`. Reason: one shared Copilot CLI process rules out a per-task `TMPDIR`, and agents write literal `/tmp` paths from shell commands.

**Revision, same day (owner prompt):** for the temp directory, a declaration tool is the authority, not mentioned paths. Posted on #227 as D1 to D7: an in-process Copilot SDK tool `uam_show_file` (not MCP), validated at call time with the T2 checks, an inode-bound per-artifact key, artifact cards on the declaring turn, and temp-directory access only by declaration. This reverses section 8's deferral of the tool for this case: for workdir mentions the resolver stays sufficient, but for access outside the workdir explicit intent is worth one model round trip. Gated on a probe with Luna or Ollama models for journal persistence, subagent inheritance, and model compliance.

**Reconciled with the implementing agent (proposed final amendment 2 on #227):** timestamps, UID and link count cannot prove which task produced a file (`ctime` changes on `chmod`; birth time does not identify the task), so the T2 and D3 provenance checks are withdrawn.
- Slice A: authority is the owner's exact-file click, minting a short-lived, inode-bound, opaque grant, served through a descriptor opened and verified via `os.Root`, with no directory widening.
- Slice B: the declaration tool stays, for discovery only. It provides artifact cards and certainty about which files the agent meant for the owner, and never mints a grant. Gated on the Luna or Ollama probe.
- Slice C, later: report bundles with sibling assets, through an explicit directory grant or a UAM-registered task artifact directory.
