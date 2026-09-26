# UAM web performance implementation plan

Date: 2026-09-25. Status: implementation, independent review and scoped verification complete, including the backend-compression followup authorized on 2026-09-26. See the implementation results for measured gains, tradeoffs and limits.

Repository: `/home/dev/projects/unified-agent-manager`. Inspected base: `main` at `606cb8e7c956292497e7d9d118a908ce19b39524`, plus the existing uncommitted transition/history changes.

This is the canonical implementation plan. It incorporates the original proposal, two agents' reviews, and the follow-up corrections. The [complete draft and review history](2026-09-25-web-lazy-data-and-cache-review-history.md) is preserved separately. Historical recommendations there do not override this document.

The user approved implementation of this consolidated plan with isolated subagents. Installation, release, and changes to provider execution remain outside this implementation run.

Current measurements, corrections and outstanding checks are recorded in the [implementation results](2026-09-25-web-lazy-data-and-cache-results.md).

## 1. Problem and intended result

The user reports frontend lag despite normal backend CPU, slow task switching, and EventSource transfers reaching 3–4 MB quickly. The original affected system is a private VM unavailable for direct testing. Local tests reproduce task-transition delay and page-insertion stalls, but have not established every cause on that VM.

The browser receives substantially more content than the visible UI needs. Collapsed tools already carry full arguments and output. Closed subagents still send transcript events. Reasoning text is normally hidden behind disclosures. Delaying React rendering avoids some DOM work, but leaves network transfer, JSON parsing, routing, and retention costs.

Recent-history pagination reduces initial transfer, but bulky hidden content makes each page cover little visible conversation. More small requests create waits during scrolling. Larger pages may worsen insertion stalls. Caching can speed revisits, but must not introduce stale content or retained-memory growth.

Deliver recent chat quickly, transfer large bodies when they are revealed, preserve live behavior and all existing controls, and keep traffic, requests, retained data, and DOM work bounded. Preserve automatic older-history loading, composer focus, drafts, and scroll position.

An accumulated SSE transfer size is not a heap measurement or proof of a leak. Measure bytes per second, snapshots, retained heap, listeners, and open connections separately. Completion/status changes can resend accumulated tool output today; their share of the reported traffic is unmeasured. Keep authoritative completion data intact.

## 2. Starting point and measured evidence

### Existing work to preserve

- Task selection and snapshot application run immediately. The task-pane view transition and existing-task entrance animation were removed.
- Selected-task reads/SSE opt into recent history. Older history loads automatically on upward scroll with cursor ordering, cancellation, and anchor compensation.
- Current pages target 64 KiB and at most 50 complete items. One oversized item is returned intact. The 64 KiB target is not a hard response or heap bound.
- Subagent history is fetched when its panel opens, but closed subagents still produce events on the selected-task stream.
- No cross-task browser cache, lazy tool/reasoning transport, or scrolling-stall fix has been implemented.

These changes remain dirty and uncommitted. This planning work has not installed or released them. Recheck the running artifact separately before making deployment claims.

### Historical local measurements

| Experiment | Baseline | Result |
|---|---:|---:|
| Task transition fix, median readiness | 279.0 ms | 49.4 ms |
| Recent-history change, median readiness; baseline includes transition fix | 54.8 ms | 33.9 ms |
| Initial SSE snapshot for captured 218-item task | 433,695 bytes | 34,805 bytes |
| Task detail response in that fixture | 430,867 bytes | 31,977 bytes |
| Recent page / additional requests to read all retained items | 27 items | 7 older-history requests |
| Continuous scroll with local responses | — | 50 ms and 119 ms insertion-associated main-thread tasks |
| Continuous scroll with 1.2-second page delay | — | 98 ms insertion task and 6.083 seconds of boundary waiting over 6 pages |
| Original capture with only tool input/output omitted | 427,748 bytes | 136,523 bytes, 68.1% smaller |

The last row is a serialization calculation, not a runtime benchmark. Different experiments use different generated responses and must not be combined into one cumulative improvement. Raw text, escaped JSON, compressed transfer, and JavaScript heap sizes are different measurements.

Prior verification included 30 focused transition tests, 13 browser assertions, 67 focused paging/frontend tests, scoped Go history/retention/stream checks, TypeScript, changed-file lint, and production frontend builds. These were not rerun for this document. Trusted CDP wheel testing later reproduced the scrolling stalls despite earlier successful switch tests. Private-VM behavior and physical touch devices remain unverified.

## 3. Scope and chosen implementation baseline

Use the smaller design recommended by the reviews. The following are explicit choices for this plan, rather than unresolved alternatives spread across comments.

| Area | Planned behavior |
|---|---|
| Initial task data | Recent chat plus lightweight tool/reasoning records and subagent summaries |
| Activity grouping | Keep existing frontend grouping and presentation rules |
| Tool and reasoning bodies | Load when the existing UI reveals them; keep revealed content live |
| Closed subagents | Bounded latest-message/activity preview and status; preserve completed result/error summary |
| Main SSE | Keep one selected-task stream, recreated on task selection |
| Detail SSE | At most one optional stream for the combined currently relevant open details |
| Task revisit cache | Add a small recent-page cache only if paired measurements show a useful revisit improvement |
| Body reuse after collapse | Initially release closed bodies; add reuse only with measured need and a tested freshness rule |

The first slice sends lightweight tool records before their group is expanded. This reduces bytes and client parsing without server-owned activity groups, but is a deliberate relaxation of the literal tool-list-on-expansion mechanism. Full inputs, outputs, and reasoning remain deferred. If strict list-on-expansion is retained as a product requirement, this slice must be reported as intermediate and group endpoints added in a separately scoped follow-up.

The plan permits two bounded EventSources instead of one stable stream with a control API. Connection count, resnapshot bytes, and cleanup are acceptance criteria. This choice is subject to the transport prototype gate below; it is not justified merely by being simpler on paper.

Non-goals are IndexedDB, offline editing, multi-tab cache synchronization, changes to providers/prompts/tool execution, new approval semantics, generic subscription/cache frameworks, speculative dependency upgrades, and unrelated UI cleanup. No new dependency is currently required. If measured rendering work needs a nontrivial commodity component, evaluate a maintained library before writing a custom replacement.

Root remains the sole writer in this worktree. Preserve all existing changes. Future parallel writers need isolated worktrees and explicit ownership. Stop after scoped implementation and agreed checks; commits, push/merge, installation, restart, and release remain separate user-directed actions.

## 4. User-visible contract

| User action | Required behavior |
|---|---|
| Open a task | Recent chat and compact activity arrive; no unopened tool/reasoning bodies or closed-subagent transcript |
| Expand an activity group | Show its already loaded compact rows; fetch only bodies whose own disclosure is visible/open |
| Expand a tool | Show its full retained input/output, with loading/error state, then live updates if it changes |
| Expand reasoning | Fetch the text and keep it live; restore remembered expansion state on mount |
| Close a detail | Stop that detailed interest and release its active body references |
| Open a subagent | Load its recent lightweight transcript; older pages load on upward scroll |
| Close a subagent | Return to preview/status-only traffic; no background transcript hydration |
| Scroll upward | Automatically fetch older pages without a normal "Show more" button; preserve the reading anchor |
| Copy unloaded command/output/thought | Fetch that one body and copy its snapshot once; never copy an empty placeholder |
| Revisit a cached task | Show its matching recent content immediately, marked refreshing until confirmed |

Preserve pending approvals/questions regardless of disclosure state. Keep sufficient content to make decisions. Preserve answered questions, file/change summaries, images, attachment references, parent results, subagent follow-up/stop, and locating the parent spawn call.

Small metadata required for display stays inline. Large retained diagnostic fields load on disclosure only if such fields exist. Do not invent provider metadata or a new metadata tab. Display-truncated paths are not valid file-identity keys.

Typing must always target the selected task's draft. On a cached selection, permit drafting in that task's composer while send and other state-dependent actions remain disabled until fresh confirmation. Never enable an old task's actions from cached status. Preserve the caret and draft when the fresh snapshot arrives. If splitting composer editability from the stale transcript cannot be done safely in the cache stage, retain the current inert guard and report the remaining typing-delay limitation; do not silently send using stale context.

## 5. Representation and compatibility contract

### Lightweight items

Add an opt-in representation, provisionally `view=compact-v1`, to task detail, history, and main SSE reads. Default legacy responses and `tool_output=delta` behavior remain unchanged.

Use explicit lightweight web types or a discriminated representation. A missing body must not mean an empty successful result. Records retain stable item/agent identity, kind, timestamps, status, bounded display argument, semantic path where needed, and flags such as `has_input`, `has_output`, and `has_reasoning`. User/assistant chat stays inline. Body loading states are unloaded, loading, loaded, refreshing, unavailable, and error.

Audit and replace the exact existing body consumers: tool labels/current step, file counts and change paths, question matching/answers, subagent previews, parent result display, and copy actions. Approvals and questions retain required semantic content under existing limits. Do not assume every `ask_user` body is small; report exceptions to the normal page budget.

Generate compact fields before serialization. Do not send full records and strip them in the client. Closed reasoning sends presence/timing changes, not text deltas. Closed tool output does not produce a stream of changing output-length counters solely to advertise hidden bytes.

### Existing-response capability marker

The existing snapshot/detail envelope declares `representation: "compact-v1"`, supported detail behavior, and a service-instance `epoch`. This is metadata on a response already needed for rendering, not an extra handshake request. It works for empty tasks too.

The epoch is a small opaque identifier created once per manager/service instance using existing standard-library ID facilities. Sequences are comparable only within that epoch. One epoch marker avoids resetting healthy chat on every detail-network error and removes ambiguity after restart. Do not add per-item ETags, public history versions, or viewer-control generations in the first implementation.

Old servers may ignore the query flag and return full items. Absence of explicit support selects the existing client path once. Do not call unsupported detail routes, enter a fallback loop, or hold old/new main streams simultaneously. Full-body legacy items remain valid loaded data.

## 6. Read APIs and stream ownership

| API | Contract |
|---|---|
| Existing task detail and `/api/events?session=...&view=compact-v1` | Recent compact main-agent items and ordinary task/global state; response declares representation and epoch |
| Existing older-history route with representation option | Cursor page of compact items; same ordering, oversized-item exception, and retention behavior |
| `GET /api/sessions/{id}/items/{item_id}?agent_id=...` | One complete retained item body plus task/agent/item identity, epoch, and snapshot sequence; primarily for copy and other one-off reads |
| `GET /api/events/detail?session=...&agent=...&item=...` | Atomic initial snapshot and subsequent updates for one opened subagent and the requested visible bodies |
| Existing subagent read plus an older-page form | Recent compact subagent history and cursor paging, negotiated so legacy full-transcript callers continue working |

The agreed body query encoding is one repeated `item` parameter per JSON array `[agent_id, item_id]`, URL encoded; an empty agent ID means the main agent. Following the measured transport failure below, a continuing body may supply a third member, its last fully applied sequence, with the matching service `epoch` query parameter. This only acknowledges content still held by the open view. `agent` selects at most one open subagent transcript. The initial limit is eight unique body references. Body references contain agent and item identity, including when main-agent and subagent bodies are open together. Canonicalize ordering so an unchanged set does not reconnect. Validate uniqueness, lengths, task ownership, and the requested count. An invalid member must not silently subscribe to unrelated content.

The frozen compact fields are `tool.display_arg`, `tool.path`, `tool.has_input`, and `tool.has_output`, plus `item.compact.has_reasoning` and `item.compact.has_text` for deferred text. Subagents add `preview` and `result_summary`, each capped at 512 UTF-8 bytes. `ask_user` retains its semantic input/output under the existing retention limits. Capability markers are `representation`, `epoch`, and `detail_stream` in the existing main snapshot/detail envelopes.

The detail stream starts with `detail_snapshot`, then individual `body` frames, then `detail_ready`, all at the same captured sequence. The initial snapshot carries `{seq, epoch, session_id, agent_id?, subagent?, items?, before?}`. A body carries `{seq, epoch, session_id, agent_id, item}`. Live authoritative replacements use `body`; suffix events use `body_delta` or `body_output`. Open-subagent compact events retain `item`, `delta`, and `items_trimmed`. `detail_reset` invalidates detail state after a history replacement. Equal-sequence initial resource frames must all be applied before the live sequence guard takes effect.

Use existing authentication, host checks, JSON/error conventions, and API `Cache-Control: no-store`. Reads must not open/prompt a provider conversation or run tools. The existing read-only history reader is allowed. Never expose credentials or raw bodies in diagnostics.

Use 400 for malformed requests, 401/403 for auth/access failures, 404 for missing/unretained resources, 409 for a changed/evicted page boundary, and 503 for temporary inability to read. A missing retained body is unavailable, not proof that the provider conversation was deleted. Preserve complete retained content; never truncate it silently to satisfy a benchmark.

### Ownership table

| State | Owner |
|---|---|
| Main chat, lightweight items, interactions, task status, sidebar/global state | Main stream |
| Closed-subagent preview/status | Main stream |
| Opened subagent's compact transcript | Detail stream |
| Revealed tool/reasoning bodies, including completed bodies still open | Detail stream |
| One-off copy response | Its request callback; never merged into live timeline/body state |

Visible completed bodies remain subscribed while open because completion does not guarantee immutability. For a body provided by the detail snapshot, do not also issue a redundant GET. Keep one task-scoped body map separate from timeline records. Closing drops its detail interest; the initial version does not promise a warm body cache on reopening.

### Initial delivery and ordering

Register the detail subscriber and capture its initial retained data at one sequence barrier under the manager's synchronization. Perform no provider I/O while holding the manager lock. Audit whether captured item/tool values are immutable or need copies before serialization.

The logical detail snapshot contains the opened subagent's recent compact page and the selected body records. Serialize and send bounded resource frames if the combined snapshot would be large, followed by a readiness marker; do not marshal many complete bodies into one giant JSON event. All initial resource frames carry the captured epoch/sequence. Live queued events follow the initial delivery. Keep queue backpressure and close promptly on cancellation.

Publish a compact mutation notification before the corresponding detailed replacement/completion event. Its sequence becomes the affected resource's invalidation floor. Then a later detailed body snapshot or ordered update can prove that it covers the mutation even if network arrival order differs. Do not compare a body to an unrelated global `detailSeq`. Test the publication order explicitly; the current filtered broadcasts allocate separate sequence values.

A summary completion never proves the body is final. If it arrives first, keep the body refreshing until covered by authoritative detail content. If detail content arrives first, a later-delivered older summary must not erase it. Final-only output, final suffixes, rewrites, and image-related replacements must remain correct. Legacy completion frames continue carrying their current full item.

### Request and connection identity

Keep separate local identities for selected-task loads, detail connections, and one-off item requests. Every callback checks its identity and task before applying effects. Abort is useful but does not replace identity checks on callbacks already queued.

Reuse the existing snapshot/buffer/replay approach for HTTP history hydration. Replay only resource events newer than the response's snapshot. Keep trims and invalidations effective while a request is pending, so a late response cannot recreate an evicted item. The existing loading boolean alone cannot identify two successive loads for the same resource.

Old callbacks from a closed detail stream are ignored. Closing and reopening the same item creates a new request/connection identity. A history replacement resets affected cursors and bodies, even if item IDs happen to recur. HTTP body copy is explicitly a point-in-time snapshot; it does not create a reusable live record.

## 7. Connection lifecycle, recovery, and repeated snapshots

- Task selection closes its previous detail stream, cancels abandoned reads, and follows the existing main-stream selection lifecycle. Only the selected task's confirmed state enables actions.
- Disclosure changes update the desired detail set once per UI tick. Close the old detail EventSource before opening its replacement. Keep continuing content readable as refreshing until the replacement snapshot covers the gap.
- Collapsed parents suspend child-body interests even if child expansion preferences remain remembered. Restore those interests when the parent becomes visible again. Merely keeping a component mounted through a collapse animation does not keep its subscription active.
- A transient detail connection failure retries the detail stream with bounded backoff while healthy main chat continues. A same-epoch detail snapshot resynchronizes only the affected detail data.
- A new epoch, a main-stream resnapshot after connection loss, or history replacement invalidates relevant sequence assumptions and stale callbacks. Stop dependent detail delivery until the main context is confirmed, then reopen only the current desired set.
- Invalid interest, unretained body, or an unsupported route is a request/content error, not a reason to reconnect healthy main chat repeatedly. Show the unavailable/error state, remove invalid interest, or take the negotiated legacy fallback as appropriate.
- Auth failure closes both streams, aborts requests, clears sensitive state/cache, and follows the existing login flow.
- Use one owner for retries. If native EventSource reconnect is active, do not run a competing reconnect timer. If closing and rebuilding explicitly, cancel the native connection first. Bound application-managed delays to 1, 2, 5, then 10 seconds and pause retries after repeated unchanged request errors until user/view state changes.

The detail snapshot may resend unchanged open bodies when another disclosure opens. This is a known cost of the simpler transport. Measure it explicitly; do not describe two streams as proof of lower traffic. Prototype repeated expansion with a large tool left open and an active subagent. Count bytes for unchanged bodies separately from newly requested data.

Keep this transport only if the complete tested browsing sequence reduces transferred bytes versus the current client and does not create repeated main-pane resets, lost pages, typing interruption, or excessive expansion stalls. If resnapshot churn defeats the benefit, first narrow interest to actually revealed/visible content. If still insufficient, stop promotion of this transport and compare a bounded subscription-update design using the same fixture. Do not quietly adopt a fallback that discards loaded older pages on every expansion.

### Measured transport correction during implementation

The first prototype failed the repeated-expansion gate. Holding a 256 KiB tool output and 1 MiB reasoning body open while toggling a small tool ten times produced 21 detail snapshots: 27,907,281 raw SSE bytes and 14,109,252 bytes when gzipped for comparison. One legacy full snapshot was 1,333,156 raw bytes and 672,826 gzip bytes. Splitting initial resources into frames bounded allocations but did not prevent redundant transfer. Evidence: `/tmp/uam-lazy-20260925/verification/transport-gate.json`.

The implementation therefore adds a bounded same-epoch resume check for continuing bodies. The server records each retained item's last mutation sequence. When the browser's covered sequence proves its held body current, the initial stream sends `body_current` with the new snapshot barrier instead of resending that body. Otherwise it sends the complete body. Every body mutation, including hidden suffixes and history replacement, must advance this evidence; eviction removes it. Closed bodies have no reusable coverage. There is no mutation journal, disk cache, or per-item ETag.

Independent review also found that a recent-only subagent snapshot cannot refresh older loaded pages after a connection gap. Reopening the detail stream therefore supplies the oldest loaded subagent boundary with `agent_before`. Initial `detail_page` frames cover that retained window at the same barrier, and the browser applies the authoritative window at readiness while preserving its reading anchor. This closes missed-correction and missed-trim gaps without discarding the reader's loaded pages. Both changes require focused correctness tests and a repeated transport benchmark before activation.

## 8. Bounds, pagination, and memory

### Initial working limits

| Resource | Initial policy |
|---|---|
| Recent and older compact pages | Existing 50-item / 64 KiB target; one oversized complete item can exceed it |
| Closed-subagent preview | At most 512 UTF-8 bytes; text replacement no more than every 250 ms; status/final changes flush promptly |
| Network streams | One main plus at most one detail stream per tab |
| Detail body interests | Start with at most 8 simultaneously relevant bodies; prioritize bodies intersecting the viewport, then nearby revealed content |
| Foreground HTTP reads | At most 2 across the selected task; at most one older-page read per transcript |
| Read-ahead | At most one page total, only after upward-scroll demand, using the same request/cache budget |
| HTTP deadline | 10 seconds with cancellation; no infinite automatic retry of invalid requests |
| Pending hydration buffer | At most 256 frames per load, plus a shared 4 MiB accounted-string-byte limit across loads |
| Recent-task reuse | At most 5 recent-page projections and 16 MiB total accounted reusable data |

These are implementation constants, not claims of achieved performance or exact JavaScript heap size. Main history and detail hydration each reserve 2 MiB, charging serialized JavaScript strings at two bytes per UTF-16 code unit. Together they fit the 4 MiB allowance. Existing server per-item/session limits remain in force. The current 256-frame/32-MiB subscriber limits remain the outer slow-client bounds unless focused evidence supports smaller compact-mode limits.

The interest count must not make content inaccessible. Overflowing revealed interests wait in a bounded viewport-prioritized queue and become active as the reader reaches them. Preserve expansion state and layout. If the prototype shows visible placeholders or churn in an ordinary reading viewport, change the interest/admission policy before activation. Test remembered reasoning expansion and multiple open parent groups; explicit clicks alone do not bound the set.

Do not assume eight interests or five cached tasks bounds all bytes. Validate actual snapshot/frame sizes against subscriber limits, send individual initial resource frames, and skip reusable-cache admission for oversized entries. Large-content fixtures must prove forward progress under backpressure without reconnect loops. Source-side retention remains the limit on any one complete body; no new silent clipping is allowed.

### Recent-task cache

Implement this stage only after a paired warm-revisit benchmark demonstrates a useful benefit. A cache entry contains the selected task's recent compact page and minimal associated display state, not all loaded older pages, every task in the sidebar, subagent transcripts, or copied full bodies.

Use one owner, shared immutable references where safe, byte accounting, and least-recently-viewed eviction. Count strings conservatively and update accounting without serializing the whole cache on each event. An entry larger than the allowance is displayed normally but not admitted for reuse. TTL and persistent storage are unnecessary for this first cache.

Five entries can exceed 320 KiB because page size is a soft target and session metadata is additional. Keep the byte limit. Removing an entry must release cache-owned references; active component state and DOM need separate lifecycle checks.

Cached content is presentation only until fresh confirmation. Do not update `snapshotSeq` from it or treat it as a replay base across reconnects. Replace/reconcile from the normal fresh recent snapshot. Clear cache on logout/auth-context change, service epoch change, task removal, and affected history replacement. Drafts remain in their existing storage path and are never evicted with transcript cache entries.

Initially release closed bodies rather than maintaining them indefinitely in a second map. A later body-reuse stage would require a shared byte budget and explicit revalidation; it is not part of this first cache.

### Active history and DOM

Automatic older-history loading must not become an unbounded accumulation policy. Measure active item/DOM counts and retained heap during a long upward traversal. If the current rendering window does not release distant rows/data, add a bounded page window that retains cursor boundaries and measured spacers, with an anchor-preserving path to refetch evicted pages. Use existing paging behavior and a maintained virtualization library if nontrivial machinery is required.

Choose the window size from the profile, and record the final value in the implementation report before calling memory behavior verified. A reusable-cache cap does not establish a cap on React state, code-highlight nodes, images, SSE queues, or in-flight buffers. A necessary oversized item is a named transient exception, not a reason to retain every page permanently.

The implementation traversal exposed this failure before completion: loading all 1,800 retained synthetic chat rows grew the active view to 18,006 transcript elements and 149.89 MB of JavaScript heap after GC. The initial 50-row view used 507 elements and 9.94 MB. Navigation cleanup passed, but did not solve active-view accumulation. The bounded-window stage is therefore required. Its initial target is 150 compact reading-window items and 4 MiB accounted data, plus one recent live page of at most 50 items, with the existing complete-oversized-item exception. Preserve compact metadata and page boundary/height descriptors without retaining evicted chat strings or React nodes.

The window adds compact forward paging through mutually exclusive `before` or `after` cursors. An `after` cursor excludes its named item and returns the next bounded page in chronological order. Compact responses carry both available boundaries; legacy paging stays unchanged. For a historical subagent window, `agent_before` and an inclusive `agent_until` describe the held range. A reconnect atomically refreshes that range and the small recent tail, skipping the evicted gap. This prevents window eviction from turning into repeated whole-history hydration.

Use the existing transcript renderer over the bounded contiguous slice first. A row virtualizer is only needed if that measured window still fails the rendering or variable-height recovery checks. Preserve upward/downward scroll, focused controls, remembered disclosures, grouping/interaction identity, parent locate and jump-to-latest. The result report must show the actual long-traversal plateau and any oversized exception; implementation constants alone do not pass the check.

## 9. Scrolling and rendering work

Do not guess which part of page insertion dominates. Capture a CPU trace around the measured 50–119 ms tasks after applying the smaller representation. Inspect `flushSync`, anchor/layout reads, arrival callback identity, grouping/linking work, Markdown/code rendering, and mounted-node count.

Apply the smallest measured fix. Preserve the first visible message/item anchor immediately before insertion; do not anchor to where the user was when a slow request began. Historical rows do not replay arrival animation. Keep trusted wheel, touch, keyboard, and parent-locate paths working.

Keep the 64 KiB target initially. Compare 128 and 256 KiB older-page targets only after the insertion cost is controlled. Keep item-count caps and oversized-item handling explicit. Report requests and bytes independently: one larger request may reduce round trips while increasing parsing and rendering time.

Single-page read-ahead is optional and demand-driven. Deduplicate it with the foreground cursor request and give explicit expansion priority. Never recursively fill the whole task. Tune the trigger using measured latency/scroll speed within a bounded distance. A reader already at the loaded boundary can still wait on a 1.2-second request; record that wait instead of claiming it disappeared.

## 10. Implementation work packages

Each package has one writer. Later packages depend on the listed exits; none authorizes deployment.

### A. Preserve the baseline and fix the contract in tests

- Record SHA, dirty diff, local toolchain, and current fixture/build identities. Do not reset or clean the worktree.
- Save the current production frontend bundle and capture fresh paired baseline results if earlier artifacts are missing or changed.
- Add focused fixtures for final-only output, completion suffix/rewrite, two agents sharing an item ID, expanded live reasoning, oversized chat/body, and retention replacement.
- Record the chosen lightweight fields, response marker, epoch, detail ownership, and initial resource-frame format in the existing web ADR when implementation begins.
- Exit: baseline is reproducible and new correctness cases identify the contracts the change must preserve. No model calls are needed.

### B. Backend projection and read paths

- Add compact web representations and computed display fields while leaving provider records and legacy responses intact.
- Apply projection consistently to initial snapshots, HTTP reads, history replacements, older pages, and live summary events.
- Add one-item body reads, recent/older compact subagent reads, and explicit representation support in existing envelopes.
- Generate bounded active previews and completed result summaries. Coalesce preview text without delaying actionable state.
- Cover auth, malformed requests, unavailable items, invalid cursors, complete oversized items, and no provider execution on reads.
- Exit: API tests pass and compact responses contain no deferred bodies except documented question/approval content. Mode remains inactive in the client.

### C. Detail transport prototype and ordering

- Implement one combined optional detail stream with atomic capture/registration, bounded initial resource frames, and cleanup.
- Add task/request/connection identity, service-epoch comparison, resource sequence barriers, and explicit ownership in the reducer.
- Test both delivery orders, authoritative completion, delayed snapshots, trim/history reset, same-epoch detail retry, new-epoch recovery, and invalid-interest errors without main-stream reset loops.
- Measure opening/closing multiple details while a large body and a subagent remain open. Include compressed and uncompressed bytes, initial resource-frame costs, and event counts.
- Exit: the transport gate in section 7 passes. If it fails, document the trace and smallest alternative before changing the transport. Do not enable body suppression yet.

### D. Frontend disclosures and complete behavior

- Wire tool bodies, nested reasoning disclosure and remembered expansion, subagent transcript paging, and parent results to the detail owner.
- Keep compact summaries in the timeline and loaded bodies separate. Suspend child interests when a parent collapses.
- Implement body-copy reads, loading/refreshing/unavailable/error states, and bounded foreground request scheduling.
- Preserve approvals/questions, images, change summaries, follow-up/stop, parent locate, keyboard/mouse behavior, drafts, and focus.
- Exit: every currently visible body path works with compact mode. Enabling the representation cannot leave running reasoning or tool output frozen.

### E. Rendering, paging, and active memory

- Profile the compact-mode insertion path and fix demonstrated synchronous work.
- Measure long-history DOM/data retention. Add a bounded window only as required to pass memory/scroll checks.
- Evaluate follow-up page sizes and one-page read-ahead as separate experiments.
- Exit: scrolling, anchor, request-count, and retention criteria pass, with final constants recorded.

### F. Recent-task cache, if justified

- Compare revisits without and with a five-entry recent-page map using the shared byte cap.
- Preserve draft editability and gate state-dependent actions until confirmation.
- Test invalidation, oversized-entry non-admission, least-recently-viewed eviction, logout, restart, and released references.
- Exit: cached presentation improves the delayed-network revisit workload and does not regress correctness or retention. If there is no material benefit, omit this package and report the measured result.

### G. Consolidated verification and handoff

- Run affected Go/frontend behavior tests, scoped diagnostics, and the production browser comparison.
- Inspect the final diff and update the web ADR/design contract around the final implementation, not abandoned approaches.
- Obtain the requested independent code review before any later push; keep local success, review, hosted CI, deployment, and release status distinct.
- Exit: write the result report with exact source/build identity, scope/results, remaining limits, and release eligibility. Stop. Publishing and installation occur only when requested.

## 11. Acceptance matrix

### Required correctness checks

| Case | Passing result |
|---|---|
| Task opens with all disclosures closed | Recent chat/records only; no tool/reasoning bodies or subagent transcript hydration |
| Empty task or legacy server | Explicit supported mode or one-time legacy fallback; no ambiguous field detection or retry loop |
| Open running tool or reasoning | Complete initial body plus updates; no duplicated/missing chunks |
| Completion-only, suffix, rewrite, images | Correct final retained content regardless of summary/detail arrival order |
| Open completed item, then provider corrects it | Open view updates or refreshes; completed status never implies immutability |
| Pending read invalidated, then old response arrives | Old response cannot replace newer data or resurrect a trimmed/deleted record |
| Same item ID in two agents/tasks | Body, request, and sequence state remain isolated |
| Reconfigure detail set during streaming | Continuing views recover the gap; old callbacks ignored; no main-status rollback |
| Expanded reasoning restored from sessionStorage | Body loads when actually revealed and remains live; collapsed ancestors suppress interest |
| Detail endpoint error while main is healthy | Bounded local recovery/unavailable state; chat does not enter a reset loop |
| Restart or auth change | Epoch/request guards prevent cross-instance merges; sensitive state cleaned up |
| Rapid task A → B → C | C wins; correct draft and actions; no late A/B apply |
| Slow/failed older page, trim, parent locate | Retry and cursor recovery without gaps, duplication, or lost reading anchor |
| Repeated collapse/navigation | Streams, listeners, timers, body refs, pending buffers, and cache entries clean up |
| Cached display before fresh snapshot | Correct selected-task draft; no stale send/stop/approval action; no caret reset on confirmation |

### Performance and resource criteria

- Paired cold-switch median must not regress by more than 10% versus the dirty recent-history baseline in repeated local runs. Report p95/range and variance; do not claim a win from a single noisy sample.
- If caching is implemented, cached presentation should be under 100 ms at p95 on the fixed fixture and independent of injected network delay. Measure confirmed read/write readiness separately.
- No unopened tool/reasoning bodies or closed-subagent transcript/output frames in negotiated compact mode. Report required interaction exceptions separately.
- At most two active EventSources per tab, zero orphaned streams, and no main-stream recreation merely because a detail opens or has a request error.
- No duplicate concurrent cursor/body read; no redundant HTTP hydration for a body already in a detail snapshot. Report control/handshake traffic, per-expansion calls, and resnapshot bytes as well as history calls.
- On the fixed 60 Hz scroll fixture, at least 95% of frame intervals should be within one frame plus 2 ms; anchor displacement should be at most 1 CSS pixel. No repeatable application-attributable insertion task over 50 ms. Record p99/max and trace failures.
- Test local responses plus 200 ms and 1,200 ms delays. Report boundary waiting and unused read-ahead bytes. An unavoidable first-boundary wait is a measured limitation, not a hidden failure.
- Reusable cache stays within count and accounted-byte limits. Selected interests, transient response frames, loading buffers, and active history have demonstrated bounds and oversized-item behavior.
- After at least 100 switch/open/close cycles and cleanup, retained heap, listeners, and subscriptions plateau rather than grow with cycles. Use heap/allocation evidence when growth appears; RSS alone is insufficient.
- Zero uncaught browser errors, data-loss cases, stale-task submissions, or silent retries that multiply traffic in the exercised paths.

Do not claim strict tool-list-on-expansion or a single stable stream in the result report. This plan deliberately uses upfront lightweight records and up to two streams.

## 12. Verification method and scoped commands

Use captured and synthetic fixtures with fake providers. Required workloads include the existing 218-item transcript, tool-heavy history, multiple hidden active subagents, repeated detail reconfiguration, large bodies, completed-item corrections, eviction, slow network, and long-history traversal. No model calls are required. Any necessary provider-backed check retains the user's Luna/Ollama-only restriction and must fill a specific evidence gap.

Fix browser version, viewport, assets, dataset, and CPU/network settings. Use four warm-up switches and at least thirty measured switches for new comparisons. Compare the current paging build, compact mode without cache, and compact mode with cache separately. Preserve raw samples and describe whether readiness means cached display or confirmed active task.

Capture trusted wheel/keyboard input, RAF intervals, long tasks and CPU traces, requests/bytes by type, resource-frame size, cache charges, DOM counts, heap cleanup, and active connections. Existing CLI scroll commands that only change scrollTop do not prove trusted wheel behavior. Physical touch-device coverage remains explicitly separate.

All shell commands use `rtk`. Pin Go to `GOTOOLCHAIN=go1.25.14`; do not exceed the user's Go 1.26.5 ceiling. The following show scoped existing checks; extend the file/test-name allowlist for new contracts rather than running repository-wide suites by default.

```bash
# Repository root: existing relevant backend coverage.
rtk proxy env GOTOOLCHAIN=go1.25.14 go test ./internal/web -run 'Test(History|ClosedTasksShow|FailedHistory|IdleHistory|DeleteCancelsHistory|SendCancelsRead|ToolOutput|TranscriptRetention|EventStreamOverHTTP|AcceptanceStreamFailure)' -count=1

# From web/: existing relevant frontend behavior checks.
rtk proxy node --experimental-strip-types --test tests/history-paging.test.mjs tests/history-api.test.mjs tests/state.test.mjs tests/transcript.test.mjs tests/motion.test.mjs tests/drafts.test.mjs

# From web/: required when frontend contracts change.
rtk proxy ./node_modules/.bin/tsc --noEmit
rtk proxy ./node_modules/.bin/eslint src/App.tsx src/api.ts src/state.ts src/components/Task.tsx src/components/Transcript.tsx src/components/Subagents.tsx src/lib/transcript.ts

# From web/: production assets for browser fixtures, without broad npm prebuild hooks.
rtk proxy ./node_modules/.bin/vite build

# Repository root: whitespace/diff inspection.
rtk git diff --check
```

Add focused race-enabled Go checks if the new snapshot/subscriber implementation changes concurrent ownership. Run only the relevant cases first. Existing local builds reported a Mermaid IIFE `import.meta` warning; verify its current status rather than labeling a new failure pre-existing without evidence.

## 13. Source map and reproducible evidence

| Area | Owner today |
|---|---|
| Selection, EventSource, confirmation/action guard | [App](../../../web/src/App.tsx) |
| Scroll loading, anchor, side panels | [Task](../../../web/src/components/Task.tsx) |
| Activity/tool/reasoning rendering and disclosures | [Transcript](../../../web/src/components/Transcript.tsx) |
| Labels, grouping, questions, file summaries | [Transcript helpers](../../../web/src/lib/transcript.ts) |
| Open subagent lifecycle and result view | [Subagents](../../../web/src/components/Subagents.tsx) |
| Types, HTTP client, ordering, request state | [API](../../../web/src/api.ts), [state](../../../web/src/state.ts) |
| Routes, web responses, subscriptions | [Server](../../../internal/web/server.go), [types](../../../internal/web/types.go), [events](../../../internal/web/events.go) |
| Item mutation, output deltas, retention | [Items](../../../internal/web/items.go), [manager](../../../internal/web/manager.go) |
| History reader and paging | [History](../../../internal/web/history.go), [history pages](../../../internal/web/history_page.go) |
| Existing written contracts | [Web ADR](../../adr/0004-web-interface.md), [design](../../../DESIGN.md) |

Existing test roots are `internal/web/history_page_test.go`, `internal/web/tool_output_test.go`, and the frontend test files in section 12. Reuse their fixtures and assertion style before adding new infrastructure.

Local evidence paths, which may be absent on another machine:

- `/tmp/uam-task-switch-20260925/transition-fix.md` and nearby captured snapshots/baseline assets.
- `/tmp/uam-history-paging-20260925/verification.md`, comparison files, `browser_fixture_test.go`, and `overlay.json`.
- `/tmp/uam-scroll-smoothness-20260925/findings.md`, raw traces, and `scroll-bench.mjs`.

The Go fixture regenerates task IDs on restart. The scrolling harness needs a fresh browser CDP URL. Preserve baseline bundles rather than overwriting them with current assets. Do not publish captured private content, credentials, or process environments; use synthetic fixtures for portable regression tests.

## 14. Activation, rollback, and completion report

Keep compact mode inactive until backend projection, detail transport, and every disclosure path pass the acceptance checks. Capability negotiation handles mixed frontend/backend versions; do not depend on browser cache clearing as a compatibility strategy.

Activation must have a simple path back to the existing recent-history representation without changing durable provider data or drafts. On rollback, close compact/detail streams, clear representation-specific body/cache state, and open one legacy/recent main stream. Verify that this does not retain a second connection or misinterpret deferred content as an empty result.

The implementation handoff must state:

1. Exact source SHA plus uncommitted state, generated asset identity, and active representation.
2. Which packages were implemented, whether the optional cache was worthwhile, and any remaining product-scope gap.
3. Actual focused test/review results and paired metrics for switches, expansion, scrolling, request counts, resnapshot bytes, and retention.
4. Unverified private-VM/physical-device/hosted-CI behavior and any required follow-up.
5. Whether source is merely local, committed, reviewed, pushed, installed, or released. These are separate states.

This planning task ends after saving and checking the documents. A future implementation ends after its agreed local/review gates and report. No service restart, publication, or version bump is included in creating this plan.


## 14. Authorized compression followup, 2026-09-26

After requesting the v0.10.8 comparison, the user asked whether the backend compressed responses. Inspection and actual response headers confirmed that it did not. The user then authorized compression after completion of the existing verification. The uncompressed checkpoint is complete and preserved in the result report before this followup begins.

Use standard-library gzip at the existing mux boundary after security checks. Cover API JSON, both main/detail SSE and embedded compressible assets. Preserve identity for binary, already encoded, file/attachment and range responses. Keep HEAD metadata consistent without sending a body. Preserve Vary and remove stale Content-Length when compression applies. Each SSE flush must flush the compressor and the underlying response, while existing deadlines and cancellation still work.

Measure actual encoded transfer, decoded-content equality and incremental event arrival. Compare the compressed final build with the preserved uncompressed checkpoint and actual v0.10.8. Do not relabel calculated gzip estimates as measured traffic. No new dependency, provider change, installation or release is authorized by this followup. Stop after focused tests, review, measured results and owned-fixture cleanup.
