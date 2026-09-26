# Archived design and review history

This file preserves the complete draft and review discussion before consolidation on 2026-09-25. Its status lines, alternatives, and instructions are historical. Use the [canonical implementation plan](2026-09-25-web-lazy-data-and-cache.md) for the current recommended design, stages, and acceptance criteria.

The original document follows unchanged.

---

# UAM web performance: lazy data, live summaries, and bounded caching

Status: draft for independent review, not an approved implementation contract. Reviewed 2026-09-25. Section 16 is the current consolidated proposal; sections 14, 15 and 17 are the review record. Sections 5 through 8 describe the original proposal and are retained as alternatives; do not implement them as written. Two owner decisions in section 16.1 block implementation and are summarized in section 17.2.
Date: 2026-09-25.
Repository: `/home/dev/projects/unified-agent-manager`.
Inspected base: `main` at `606cb8e7c956292497e7d9d118a908ce19b39524`, with the uncommitted changes described below.

Reading order after review: start with the problem and evidence in sections 1–2, then read [the follow-up comments](#15-follow-up-review-comments-2026-09-25) and [the consolidated proposal](#16-consolidated-proposal-for-the-next-review). Section 16 is the current recommended plan for review and supersedes conflicting implementation recommendations in sections 5–11 and 14. The earlier design and reviewer text remain intact as rationale and alternatives. The user requirements in section 1 still apply; proposed scope tradeoffs in section 16 are not recorded as user-approved decisions. This document does not authorize implementation.

The reader is another engineering agent. Its job is to review the problem and this proposal, identify missing failure modes, challenge unnecessary complexity, and recommend the smallest complete implementation. Review may expand the investigation and propose alternatives. It does not authorize code changes, deployment, or unrelated fixes.

## 1. Problem statement

The user reports that UAM's browser UI lags while backend CPU appears normal. Switching tasks feels slow, and an EventSource request quickly accumulates 3–4 MB. The original affected system is a private VM that is unavailable for direct testing. Local tests have reproduced task-transition delay and scrolling stalls, but do not establish every cause on that VM.

The UI displays much less information than it receives. Most tool calls are collapsed. Closed subagents need only a working state and their latest message preview. Sending detailed tool output and every subagent event still makes the browser receive, decode, parse, route, and sometimes retain data that the user is not viewing. Deferring React rendering alone does not remove those costs.

Recent-history pagination already reduces initial payloads, but it introduces another tradeoff. Pages containing bulky hidden output cover little visible conversation, so reading older chat requires several requests. Increasing page sizes can reduce requests while worsening synchronous insertion and rendering. Caching can reduce repeated downloads, but unbounded or incorrectly invalidated caches can replace network latency with memory growth and stale UI.

The intended outcome is a responsive chat interface that transfers details in proportion to what the user opens, retains useful recent data within explicit limits, and stays correct while agents continue working.

### User requirements

- Task switching should feel immediate, including returning to a recently viewed task.
- Load recent chat first. Load older conversation automatically on upward scroll, without a normal "Show more" button.
- Fetch the tool list when its activity group is expanded. Fetch a tool's detailed content when that tool is expanded. Defer additional substantial metadata until its own disclosure is opened.
- A closed subagent shows its name, state, and a short latest-message preview. Its full conversation and internal tool activity are unnecessary until opened.
- Avoid excessive network calls, duplicate requests, growing streams of hidden details, and caches with unclear eviction rules.
- Preserve page-level typing into the composer, drafts, focus, scroll position, and existing task/subagent controls.

### What the reported symptoms do and do not prove

- A long-lived SSE request's accumulated transfer size is not the browser's retained heap size. A 4 MB transfer alone does not prove a memory leak. Measure byte rate, retained data, heap after cleanup, listener counts, and open connections separately.
- The inspected client holds one EventSource at a time, but closes and recreates it when the selected task changes. That causes repeated snapshots even without overlapping connections.
- Normal backend CPU does not exclude network latency, browser parsing, React work, layout, garbage collection, or proxy buffering.
- Caching will not by itself fix the measured page-insertion stalls. Lazy data, rendering work, and cache reuse need separate measurements.

### Questions the reviewer may broaden

Is payload volume the dominant remaining bottleneck, or are repeated grouping, Markdown work, mounted DOM, and forced layout more significant? Would compact item summaries achieve most of the benefit before server-owned activity groups? Does a stable stream justify its control protocol? Is a reusable browser cache necessary after payload reduction? Consider these alternatives against the same user requirements and evidence, rather than assuming this proposal must be accepted intact.

## 2. Current state and evidence

### Existing local work to preserve

Two changes are already implemented in the dirty worktree:

1. Immediate task selection and snapshot application, removing the task-pane view transition and existing-task entrance animation.
2. Recent-history negotiation for task detail/SSE, cursor-based older history, and automatic upward-scroll loading with anchor compensation.

The history implementation returns up to 50 complete items, targeting 64 KiB of serialized item data. One oversized item is returned alone and intact. It permits one older-page request at a time, aborts on navigation, and handles HTTP/SSE ordering with bounded buffering. It does not add a cross-task browser cache.

The frontend already fetches a subagent transcript when its panel opens. However, the selected-task SSE stream still carries subagent item, text-delta, and tool-output events while the panel is closed. The reducer keeps a small current-step record and discards much of the rest after parsing. Tool input/output are included in task items even when their disclosures have never opened.

The current activity grouping, compact turn labels, question extraction, tool labels, and some change summaries are derived in the frontend from full items. Removing those fields without replacement summaries would break visible behavior. This is a design constraint, not just a serialization change.

No summary protocol, tool-detail fetch API, stable viewer stream, cross-task memory cache, IndexedDB cache, or scrolling-stall fix described in this proposal has been implemented. The existing transition/history changes have not been committed, installed, or released by this work. The live daemon version was not rechecked for this document; do not infer deployed behavior from the checkout.

### Measurements already collected

These are controlled local headless Chromium results against captured task data and fake providers, not private-VM measurements. Separate benchmark runs must not be combined into one cumulative percentage.

| Experiment | Before | After / finding |
|---|---:|---:|
| Task transition fix, median readiness | 279.0 ms | 49.4 ms |
| Recent-history test, median readiness, baseline already has transition fix | 54.8 ms | 33.9 ms |
| Recent-history test, initial SSE snapshot for 218-item task | 433,695 bytes | 34,805 bytes |
| Recent-history test, task detail | 430,867 bytes | 31,977 bytes |
| Initial items / additional calls to read all 218 items | 27 items | 7 older-history calls |
| Continuous scrolling, local responses | — | 50 ms and 119 ms main-thread tasks after page insertion |
| Continuous scrolling, 1.2-second older-page latency | — | 98 ms insertion task; 6.083 seconds total boundary waiting across 6 pages |
| Removing only tool input/output from captured full detail | 427,748 bytes | 136,523 bytes, 68.1% smaller |

The last row is a compact JSON size calculation over the original capture, not a runtime benchmark or the same generated response used by the pagination fixture. It leaves other fields intact and does not measure grouping, live traffic, caching, or rendering improvements.

The earlier transition and paging runs used four warm-ups and twelve measured switches per bundle. Paging verification reconstructed 218 unique items, observed at most one older-page request in flight, and measured 0.39 px of anchor movement for the tested insertion. The later scrolling run used trusted CDP wheel input and reproduced stalls. Earlier successful switch tests therefore do not establish smooth continuous scrolling.

### Existing verification, not rerun for this document

- Transition work: 30 focused frontend tests and 13 browser assertions.
- Paging work: 67 focused frontend tests; scoped Go history/retention/tool-output/stream checks; TypeScript; changed-file ESLint; production frontend build.
- Browser checks included navigation cancellation, error retry, keyboard paging, locating an older subagent parent, and composer typing.
- Physical touch devices, the private VM, hosted CI, and an independent review of this dirty patch remain unverified. The existing Mermaid build warning was recorded in the earlier build.

## 3. Scope, ownership, and stop conditions

The present deliverable is this plan only. Root is the sole writer in this worktree. Another agent should start with a read-only review. Do not reset, clean, overwrite, or commit the existing dirty work.

The proposed implementation scope is the web transport, its bounded read models, frontend data ownership, and rendering changes needed to meet the stated performance requirements. Reuse the existing manager, authentication, event ordering, history readers, reducer tests, and browser fixture. Prefer native fetch, AbortController, maps, and the existing UI components; no dependency is proposed yet.

Non-goals:

- Persistent IndexedDB storage, offline editing, service workers, or multi-tab cache synchronization.
- Changes to model/provider execution, prompts, tool semantics, approval policy, or durable conversation storage.
- A new frontend data framework, a generic subscription platform, or provider-specific raw metadata that UAM does not retain.
- Unrelated redesign, dependency upgrades, cleanup, release, installation, or service restart.
- Claiming compatibility or performance from a local build without testing the affected contract.

For implementation, stop after the agreed stages pass their focused checks and the comparison report is written. Any release/install action requires the applicable user instruction. For review, stop after findings, alternatives, and unresolved decisions; do not implement them.

## 4. Proposed user-visible behavior

| View | Initial content | What opening it fetches | What closing it stops |
|---|---|---|---|
| Main conversation | Recent user/assistant chat, activity summaries, compact subagent rows, actionable requests | Older summary/chat pages on upward scroll | Previous task's detailed subscriptions |
| Collapsed activity group | Counts, working/error state, short current activity | One bounded page of ordered tool/thought summaries for that group | Group-only detailed updates |
| Tool row | Name, short argument preview, status, timing | That tool's retained input/output and associated content | Detailed output updates |
| Additional metadata disclosure | Availability indication | Substantial retained diagnostic/structured fields, if any | Metadata-specific work |
| Closed subagent | Name, state, latest-message preview and timestamp | Nothing until opened | All transcript and tool-output traffic for that subagent |
| Open subagent | Recent chat/activity page, same tool disclosure behavior | Older pages on scroll, details on expansion | Its transcript/detail subscription |

Interpret "metadata" by actual retained fields. Small IDs, timestamps, and status fields may accompany a summary. Do not invent an extra request for a few bytes already needed for display, or add a metadata tab merely to fit this table. Large raw fields should stay behind their own disclosure when such fields exist.

Preserve pending approvals and questions even when their tool group or subagent is closed. Include enough content to make the existing decision safely. Preserve question/answer presentation, images and attachments, edit summaries, errors, timing, copy actions, and "show where spawned." A copy action for unloaded content may fetch that specific content before copying; it must not silently copy an empty summary.

A cached task may become readable immediately. Its status must remain visibly refreshing until confirmed. Restore the correct task draft and composer focus immediately; sending and other state-dependent actions remain gated on fresh selection acknowledgement. Fresh confirmation must not remount the whole transcript or steal the caret.

## 5. Data model and ownership

### Separate summaries from full content

Add an explicitly negotiated web representation, provisionally `summary-v1`. Keep the provider's retained item model as the source of truth. Use separate web types for summary rows and loaded content, rather than interpreting absent input/output as an empty tool result.

The summary timeline contains:

- Chat rows with stable item ID, role/kind, text, order, and attachment references.
- Activity rows with stable group identity, range/turn identity, counts, status, timing, preview, and a revision.
- Subagent rows with agent ID, parent tool ID, name, state, bounded latest-message preview, and revision.
- References to actionable interactions, visible images, and change summaries required by the existing UI.

Tool detail records have explicit states: `unloaded`, `loading`, `loaded`, `refreshing`, `error`, and `unavailable`. Empty content is a valid loaded value. A retained-history eviction is distinguishable from a transient fetch failure.

Compute display summaries on the server before serialization. Do not serialize full items and then remove fields in the browser. Share already encoded summary events among viewers with equivalent interests where practical. Avoid a per-viewer full-transcript scan or re-encoding all tool output on each delta.

### Stable activity identity is a review gate

Current groups are derived from frontend item order. They are not existing server resources. The proposed server grouping must preserve those rules, including steers, interleaved prose, questions, tool images, and subagent rows.

Recommended identity is task + agent + history generation + the group's original first retained item ID. Build groups from retained history, not from the first item in a requested page. Append-only growth keeps the identity. A history replacement or eviction of the identifying boundary invalidates it explicitly. Continuation across pages must not create a second logical group or double its counts.

First verify this against golden fixtures for the existing compact and expanded layouts. If stable grouping requires disproportionate machinery, propose a smaller first stage that sends lightweight item summaries and still defers all heavy content. That alternative must disclose that the tool list itself is downloaded before expansion; it is an intermediate result, not full acceptance of the requested pattern.

### Distinguish versions with different jobs

| Token | Purpose |
|---|---|
| Service epoch | Changes on service restart; old sequence numbers and cache validators cannot be compared across epochs |
| History generation | Changes when retained history is replaced/reset so old page boundaries and group identities cannot be reused silently |
| Resource revision | Changes when that item's/group's/subagent summary or content changes; validates a cached representation |
| Existing event `seq` | Orders snapshots and live frames within one service epoch; gaps remain valid |
| Client view generation | Increases when desired selection/disclosures change; rejects late control replies and obsolete view data |

Use existing mutation ordering where it can safely supply revisions. Do not hash megabytes of output on every token or use the task's `updated_at` as a content revision. An HTTP snapshot sequence is not a global instruction to discard every earlier event for unrelated resources.

Revisions belong to a representation. Hidden tool output changing must not force a summary event for every chunk when the visible summary is unchanged. An opened content view still receives the ordered changes it needs. Closed cached content is validated when reopened, rather than kept current through an invisible subscription.

The manager's current internal history-read cancellation generation is not automatically a public content generation. Confirm its semantics before reuse.

## 6. Draft HTTP and SSE contract

Endpoint names below are proposals. Finalize them after reviewing whether existing handlers can serve the representation without ambiguity. Default legacy responses remain unchanged.

| Operation | Purpose and response |
|---|---|
| `GET /api/events?view=summary-v1` | One viewer stream; first frame declares capabilities, service epoch, and a stream ID. Global sidebar/settings events remain available. |
| `PUT /api/event-streams/{stream_id}/view` | Replace the complete desired task/disclosure set on that existing stream. Returns accepted generation; authoritative selection readiness arrives on SSE. |
| `GET /api/sessions/{id}/timeline?agent_id=...&before=...` | Recent or older chat/summary page for main agent or one subagent. Omitted `before` means recent; omitted `agent_id` means main agent. |
| `GET /api/sessions/{id}/activity/{group_id}?before=...` | Ordered compact tool/thought rows for one opened group; bounded and cursor-paged. |
| `GET /api/sessions/{id}/items/{item_id}?agent_id=...&view=content` | One retained item's content, including tool input/output when applicable. |
| Same item route with `view=metadata` | Only if substantial separately requested retained metadata exists; otherwise combine small fields with content. |

Page envelopes carry `epoch`, `history_generation`, `seq`, `revision`, ordered `rows`, `before`, and explicit retention/truncation state. Detail responses carry the same identity/version context plus the requested content. Item IDs are unique per agent, so every lookup, cache key, and event route includes the agent identity.

Cursors are opaque, scoped to task, agent, representation, history generation, and boundary. Validate that scope on every read. Invalid syntax returns 400; a formerly valid replaced/evicted boundary returns 409. Page boundaries remain stable under concurrent appends. An activity group ID must also encode or resolve its agent identity unambiguously.

Use the existing authentication, host checks, cross-origin write protection, JSON body limits, and error format. Stream IDs identify subscriptions; they are not credentials. Bind control requests to the stream's authenticated context, keep IDs unguessable using standard library facilities, and expire them on disconnect. Do not log sensitive content or control credentials.

Expected statuses are 200 for successful reads/control acceptance, 304 for a matching conditional detail read, 400 for malformed parameters, 401/403 for authentication/access failures, 404 for missing resources/expired streams, 409 for incompatible history/cursor/view generations, and 503 for temporary unavailability. Reuse existing errors rather than creating a parallel error framework. A removed retained item can return 404 with a clear unavailable reason; do not imply the provider conversation was deleted.

Retain the current API `Cache-Control: no-store` policy. Browser-managed memory caching is explicit. If adding ETags, the fetch wrapper must handle 304 without parsing an empty JSON body, using its retained matching value; no matching body means refetch unconditionally. Do not enable intermediary or disk caching of conversation data.

### Stable stream and changing interests

A desired view contains a monotonically increasing generation, selected task ID or null, opened activity group IDs, the currently viewed subagent if any, and opened item-content references. Send the whole set so retries are idempotent. Coalesce changes within one UI tick; impose explicit server limits on the set and reject invalid IDs before installing it.

The server rejects an older generation, acknowledges a retry of the same generation and body without repeating side effects, and rejects the same generation with different content. Serialize view installation so reordered HTTP requests cannot restore an obsolete selection.

Recommended protocol:

1. Open one EventSource after authentication. Create a fresh stream ID for every connection incarnation.
2. On task selection, show the matching cached presentation if available and send the desired view with a new generation.
3. Under the manager's synchronization, install that view and enqueue a `view_ready` event containing its generation, sequence barrier, history generation, and the selected task's bounded recent summary snapshot when selection changes. On disclosure-only changes, acknowledge the new interests without repeating the entire task snapshot.
4. Newly relevant events follow that barrier and carry enough task/resource/view identity to reject obsolete data. Global sidebar events remain independent of selection generations.
5. Only the latest acknowledged selection enables state-dependent task actions. Rapid A → B → C selection cannot let B's response replace C or enable B's composer actions.
6. Closing a view removes its detailed interests and aborts abandoned requests. Queued obsolete detail frames must not delay the new selection behind a large old-output backlog. Preserve required global events and queue-byte accounting when discarding obsolete frames.
7. On disconnect, remove all interests, timers, and stream-owned state. Reconnect with a new stream ID, reinstall the latest desired view, and obtain fresh bounded snapshots. Do not assume the existing server retains an arbitrary SSE replay log.

Changing a disclosure generation must not discard queued chat events for the same task or events for another resource that remains open. Process continuing interests through the ordered acknowledgement, then switch their generation. Drop only genuinely obsolete interests or data covered by a replacement snapshot. Do not make a global `frame.generation !== desiredGeneration` filter that loses chat whenever a tool opens.

The existing client recreates EventSource on task selection. Keeping it stable is a real protocol change and needs its own stage and tests. Do not claim it is achieved by adding a cache or by showing a previous snapshot.

### Opening a live resource without losing updates

For an opened group, tool, or subagent that needs an HTTP read:

1. Install its stream interest and wait for acknowledgement.
2. Buffer that resource's subsequent events while fetching its bounded snapshot, or validating a retained value.
3. Install the HTTP response only if its task, resource, epoch, history generation, and active request token still match. A disclosure change elsewhere must not cancel/restart a still-wanted resource fetch. Closing and reopening the same resource does invalidate its old request token.
4. Replay buffered events newer than the response's `seq` for that resource. Retain newer already-applied records and handle trims; do not resurrect evicted items.
5. Keep sequence barriers per resource. Do not advance the whole task's barrier from one tool response and lose pending chat deltas.
6. On buffer overflow, discard the partial detail state and retry from a fresh snapshot with bounded backoff. Never silently drop text chunks and present a corrupted result as complete.

Reuse the existing subagent/history buffering logic where possible. Its current bounds are 256 frames and 4,194,304 counted JavaScript string code units, not a precise 4 MiB heap bound. Add an aggregate accounted-byte bound across concurrent loads so opening multiple views cannot multiply memory without limit. Reads have cancellation and a 10-second deadline, following current paging behavior.

This subscribe-then-read protocol can add a control round trip before a cold expansion. Report that cost. The reviewer should compare it with an atomic subscribe-and-initial-snapshot response, especially on high-latency links, before freezing the protocol. Neither approach may omit the ordering barrier. Cache hits remove body downloads, not every required freshness check.

### Closed-subagent previews

Generate a compact summary from retained provider events. A proposed preview cap is 512 UTF-8 bytes, cut at a valid character boundary. Include preview kind/time so the UI can distinguish an actual assistant message from fallback activity text. Do not generate a new model summary or label tool output as an assistant message.

Coalesce text preview changes to at most one per 250 ms per active subagent. Status changes, failures, actionable requests, and final state should flush promptly. Keep at most one pending replacement preview per subagent; release scheduled work on disconnect/removal. The cap and interval are initial tuning values, not measured optimums.

Closed subagents receive no full transcript items, reasoning text, or tool-output deltas. Open subagents still use summary-first tool rows; opening the subagent alone does not subscribe to every internal tool's output.

## 7. Pagination, request counts, and scrolling

Keep initial loading small. Start with the existing 64 KiB target, applied to the new visible timeline representation, and a bounded row count. Count summary rows as visible rows, not as every hidden tool item they represent. Large chat messages remain complete and require an explicit oversized-item exception or a separately designed content path; never truncate them silently to meet a benchmark.

Proposed starting caps are 50 initial timeline rows and 100 rows per expanded activity page, both with a 64 KiB target; older timeline experiments may use up to 200 rows with the budgets below. Bound row labels and argument previews independently. The timeline target does not cover global sidebar data or required approval payloads; report those bytes separately so exceptions cannot conceal an oversized snapshot.

Measure follow-up page candidates at 64, 128, and 256 KiB with explicit row caps. Do not select a larger page until page insertion remains responsive. The earlier suggestion of larger older pages is an experiment, not an implemented or proven improvement.

Use one older-page request per visible transcript at a time. A single read-ahead page is permitted after upward scrolling indicates demand. It must replace, not duplicate, a foreground request for the same cursor. No recursive prefetch and no download of the whole task on initial load. Prefetch consumes the shared byte budget and is first to be evicted when unused.

Start fetching before the loaded boundary based on measured request latency and scroll speed, with a bounded threshold. The initial short-content case is special: the measured recent page filled barely more than one viewport. No threshold can conceal a 1.2-second round trip when the reader is already at the boundary. Allow one demand-triggered fill/read-ahead page, and measure any visible wait honestly.

At the first implementation stage, deduplicate all reads by resource/cursor/revision. Use a small shared scheduler, initially two foreground data reads across the active task and subagent; prefetch runs only when it does not block an explicit expansion. A view-control request is separate so closing or switching cannot wait behind a slow history read. Count data reads, conditional validations, control writes, and SSE handshakes separately in benchmarks.

Profile the existing `flushSync` prepend path before changing it. Use a stable message/summary anchor captured immediately before insertion. Preserve native wheel, touch, keyboard, and programmatic locate paths. Consider smaller rendering commits and memoized summary rows before adding a virtualization dependency. Avoid repeated Markdown/code parsing for unchanged content, arrival animations on historical rows, and forced layout across the entire transcript.

Keep DOM and active history bounded as the user scrolls. If a mounted-row window is needed to pass long-history tests, preserve measured spacers, cursor boundaries, expanded state, and anchor position when unmounting distant rows. Choose its implementation after profiling; do not claim cache eviction bounds DOM memory.

## 8. Bounded memory-cache policy

Start with a per-tab memory cache. IndexedDB is deferred until measurements show value from persistence across reloads sufficient to justify schema migration, storage quota behavior, and durable account isolation.

### Keys, ownership, and reuse

Namespace keys by server origin, authenticated context, service epoch, task ID, history generation, resource kind, agent ID, resource ID/cursor, and requested representation. Keep validators with values. Never share entries between account/server contexts.

Use one cache owner and one accounting/eviction policy across recent task snapshots, older pages, group lists, tool content, and subagent pages. The reducer and cache should share immutable records where feasible; do not retain a second serialized copy of each live snapshot or put a separate cache in each component.

Completed does not mean immutable. A completed tool can be corrected by history reconciliation, and an idle subagent can receive another prompt. Reuse full content without a body transfer only when its current revision has been confirmed. Where a view acknowledgement can return current validators for the explicitly requested resources, use that bounded list to avoid a separate conditional request per unchanged tool. Otherwise use a conditional GET.

Task switching may render a matching cached snapshot immediately while a fresh recent summary snapshot arrives. Initial implementation should revalidate with that small snapshot, not introduce an unbounded event replay store to fetch "only the difference." Incremental replay can be considered separately if measured summary refresh cost warrants it.

### Proposed initial limits

| Limit | Starting proposal |
|---|---|
| Accounted reusable data | 16 MiB across the tab |
| Recently viewed task entries | At most 5; byte budget takes precedence |
| Idle retention | Expire reusable entries after 10 minutes without access; TTL does not establish freshness |
| Speculative data | At most one older page, charged to the same budget |
| In-flight data reads | At most two foreground reads; prefetch yields to explicit work |
| Loading-event buffers | Existing per-load limits plus one explicit aggregate cap |

These are reviewable starting values. Measure accounting overhead and retained heap rather than treating serialized JSON bytes as exact JavaScript memory. Count strings conservatively, account for collection overhead, and update charges incrementally for live data. Avoid full serialization of all cached values on every delta.

Evict unused prefetch first, then least recently used closed details/pages/tasks. Preserve currently visible content and unsent drafts. Drafts remain in their existing storage path and are not cache entries. A cache hit must not restart a request unnecessarily, and dropping a cache entry must remove all cache-owned references and listeners.

### Active-data overflow must be explicit

The 16 MiB reuse target is not a claim that the entire tab uses 16 MiB. Active React state, DOM, decoded images, in-flight responses, and event buffers also retain memory. Removing an entry from a map cannot reclaim a value that a mounted component still references.

Before cache implementation is accepted, settle and test the active-data policy: a bounded rendered history window, removal of closed detail subscriptions/references, aggregate buffering, and an explicit oversized-response exception. A necessary single item may bypass reusable-cache admission and exist transiently, bounded by the existing server response limits. Do not silently drop visible messages or refuse access to older content to make accounting pass. If several pinned/open contents can exceed the intended working set, the review must choose bounded content rendering or fetching before declaring memory behavior solved.

### Invalidation and cleanup

| Event | Required behavior |
|---|---|
| Item/group revision changes | Invalidate the old body or apply its ordered live update; refresh only if viewed |
| Task history replacement/generation change | Drop affected page boundaries, groups, bodies, and snapshot assumptions; obtain a recent snapshot |
| Retention trim | Remove affected items and detail entries; invalidate cursors whose boundaries disappeared |
| Service epoch changes | Discard old sequence/validator assumptions and revalidate; do not merge epochs |
| Task/subagent deleted | Remove its entries and interests; late responses cannot restore it |
| Navigation/collapse | Cancel abandoned fetches, stop detail updates, and retain only budgeted reusable values |
| Logout, auth failure, server/account change | Clear sensitive cached values, buffers, inflight work, and subscriptions |
| Tab closes | Browser discards the memory cache; no persistence is promised |

## 9. Compatibility, failures, and rollback

Negotiate `summary-v1` explicitly. Existing clients continue receiving their existing full-item or recent-history representation. A new client detects capability support from the handshake before using control/detail routes. An old server may ignore the new query parameter, so do not assume success from HTTP 200 alone. On unsupported capability, fall back once to the current client path without a reconnect loop or two simultaneous streams.

Keep server-side provider retention behavior unchanged. Current live retention uses a 2,000-item limit and a 64 MiB limit on accounted item text per session, with separate per-field limits. Stored-history loading targets 16 MiB per task and 64 MiB aggregate. These are distinct policies and are not exact process-heap ceilings; a browser cache is not a durable replacement for either.

If a cursor/group boundary disappears, return a conflict and resynchronize the affected view. If a detail is no longer retained, show unavailable with the existing history limitation rather than an endless spinner. If a fetch fails transiently, retain a valid cached display marked stale where useful and allow a bounded retry. Navigation always wins over retries.

Keep request-size, output-size, queue, and subscription limits explicit for the new paths. Preserve the current slow-client disconnect behavior. Existing subscriber bounds are 256 frames and 32 MiB; review whether summary-mode queues can be smaller, but do not change legacy limits merely as part of this proposal.

Rollout should permit disabling the new representation/client path and returning to the tested recent-history behavior. No database migration is planned. Do not make rollback erase task history or drafts. Final activation and release are separate from implementing the contract.

## 10. Implementation stages and exit checks

1. **Review and freeze the smallest contract.** Reconcile the dirty worktree; validate requirements and evidence. Resolve activity identity, stream-interest ordering, active-memory overflow, and compatibility before implementing dependent stages. Produce the accepted API/type examples and error cases.
2. **Create server summaries and bounded read APIs.** Preserve legacy behavior. Test grouping parity and the fields needed by questions, approvals, images, change summaries, copy, and locate. Verify that summary responses contain no hidden full tool output or closed-subagent transcript. Do not switch the production client until these contracts work.
3. **Add the stable viewer stream and interest control.** Test one connection across task switches, ordered view acknowledgement, resource fetch races, reconnects, multiple viewers, cancellation, ownership, bounded queues, and obsolete-frame cleanup. Reads may use the existing read-only provider history reader, but must not open/prompt a conversation or trigger tool execution.
4. **Wire lazy disclosures into the frontend.** Task chat, expanded group, expanded tool, and opened subagent each request only their level. Preserve drafts/focus/actions, loading and error states, cursor paging, and parent-item locate. Verify that collapsing actually stops detailed network traffic.
5. **Resolve scrolling cost and page policy.** Profile the measured insertion stalls with the smaller representation. Test the page-size/read-ahead candidates and any required rendering window. Record requests, bytes, long tasks, frame intervals, boundary waiting, and anchor movement separately.
6. **Add the shared bounded cache.** First recent task snapshots and completed details, then older/subagent pages through the same owner. Test revision validation, inflight deduplication, LRU/TTL, invalidation, active-data limits, and cleanup. Compare uncached and cached navigation under the same latency.
7. **Run the focused acceptance matrix and write a comparison report.** Stop after accepted checks pass. Record exact source/artifact identities, remaining limits, and independently reviewed status. Do not bundle installation or release into this stage.

Stages are separable for review and regression isolation. They need not be released independently. Do not implement an intermediate stage as the final feature if it omits the user's requested disclosure behavior.

## 11. Verification and acceptance criteria

All targets below are proposed acceptance thresholds, not claims of achieved performance. Freeze them with the reviewer before performance tuning.

### Functional and ordering checks

| Scenario | Required result |
|---|---|
| Open task with every disclosure closed | Recent chat and summaries; zero tool-body/subagent-transcript reads |
| Expand one activity group | One bounded group read or validated cache hit; no per-tool body fan-out |
| Open one tool while its output changes | Complete snapshot plus each subsequent update exactly once |
| Close tool/subagent | Detailed traffic stops after acknowledged view change; only permitted summary/status updates continue |
| Reopen completed unchanged detail | No repeated body transfer; no validation request if current revision is already confirmed |
| A → B → C rapid navigation with delayed replies | C remains selected; correct draft/actions; no late A/B data applies |
| HTTP snapshot ahead of queued SSE | Per-resource barrier preserves unrelated newer chat and other tool events |
| Another disclosure opens during chat streaming/detail fetch | Continuing interests lose no frames; still-wanted reads are not restarted |
| Trim/history reset during a read | No resurrection, duplicates, or reuse of an invalid boundary |
| Reconnect/restart | Fresh view installed; stale epoch/generation cannot merge into current data |
| Same item ID in two subagents | Content and validators stay isolated |
| Multiple browser tabs/viewers | Closing one viewer does not change another's subscriptions or provider execution |
| Cache churn, auth change, deletion | Budgeted eviction and cleanup; no sensitive entry survives context change |
| Scroll/locate with unloaded groups | Stable order, automatic paging, correct parent reveal, no missing chat |
| Composer and action regression | Page typing, draft/caret preservation, approvals/questions, send/stop/follow-up behavior remain correct |

Use focused Go tests for representation, endpoints, subscriptions, and ordering. Extend existing frontend reducer/API/transcript tests for lazy state and cache behavior. Add behavior tests only for the new failure modes, not snapshots mirroring implementation. Run changed-file lint and TypeScript when frontend contracts change. Build a production frontend directly for browser measurements without invoking unrelated repository-wide prebuild checks.

### Performance and resource checks

- **Task response:** warm cached presentation under 100 ms at p95 on the fixed fixture. Measure fresh/read-write readiness separately from showing cached content. With injected latency, cached presentation should not wait for the server.
- **Cold response:** no greater than 10% median readiness regression against the existing recent-history branch in repeated paired local runs; investigate variance before declaring a regression. Cold readiness still includes unavoidable network latency.
- **Payload:** zero unopened tool bodies and zero closed-subagent transcript/output frames in negotiated summary mode. Report total encoded bytes and frame rate, not only the largest snapshot.
- **Connections:** one active EventSource per authenticated tab and no new handshake on ordinary task/disclosure changes. Reconnects on actual connection loss are expected.
- **Requests:** zero idle speculative detail calls; no duplicate concurrent resource reads; one page request per cursor. Report the complete 218-item reading path and a tool-heavy larger fixture. Do not promise a fixed call reduction from the 7-call baseline before grouping is implemented.
- **Scrolling:** anchor displacement no more than 1 CSS pixel in the fixture. At least 95% of frame intervals within one 60 Hz frame plus 2 ms tolerance on the test host; no repeatable application-attributable insertion task over 50 ms. Record p99/max and a CPU trace for failures rather than hiding them behind median FPS.
- **Slow network:** test normal latency plus injected 200 ms and 1,200 ms page delays. Report time at the loaded boundary and unused-prefetch bytes. A reader already at the boundary may still wait; do not claim prefetch removes network latency.
- **Retention:** accounted reusable data stays within its agreed budget; active/transient exceptions stay within their agreed independent bounds. After at least 100 switch/open/close cycles and cleanup, retained heap/listeners/subscriptions plateau rather than grow with cycle count. Use heap snapshots or allocation traces when counts grow; process RSS alone is insufficient.
- **Errors:** zero uncaught browser exceptions, lost/duplicated chunks, hidden retry loops, stale-task submissions, or orphaned subscriptions in the exercised paths.

Run the same workload with cache disabled and enabled. Compare the current dirty paging build, summary/lazy loading alone, and summary plus cache so each improvement has evidence. Reuse captured fixtures and fake providers; no model calls are required. If a provider-backed check becomes necessary, retain the user's Luna/Ollama-only model restriction and explain the remaining evidence gap first.

### Measurement method

Use a fixed viewport, browser version, asset build, dataset, and CPU/network conditions. Perform four warm-up switches and at least thirty measured switches for new comparisons; retain raw samples and report median/p95/range. Record both time to correct cached content and time to confirmed active task. Capture trusted wheel/keyboard input, animation-frame gaps, long tasks, network calls/bytes, cache accounting, and open connection/listener counts.

Add synthetic fixtures for concurrent main-agent streaming, multiple hidden subagents, long tool output, oversized chat, correction of completed items, and retention eviction. Replaying the captured closed task alone cannot validate live preview or cache invalidation behavior. Keep request timing and insertion timing separate; account for rendering/Markdown cost in expansion tests too.

## 12. Reviewer checklist and open decisions

Review from the problem statement outward. The following are decision gates, not permission to expand implementation silently:

1. Is server-owned activity grouping necessary now, and can it preserve every existing visible summary without needing full tool payloads in the browser?
2. Does the stable-stream control protocol earn its complexity? Is there a smaller alternative that still avoids reconnects and suppresses closed-view traffic without per-panel EventSources?
3. Are view acknowledgement, HTTP snapshot merging, queued old frames, and per-resource sequence barriers fully specified and testable?
4. Can epoch/history/revision identifiers reuse existing ownership safely? Which mutations invalidate completed-item caches?
5. What is the exact active-data/window/oversized-item policy alongside the 16 MiB reusable-cache target? Does it actually release references and DOM?
6. Can summary generation or preview coalescing create manager-lock contention, expensive repeated scans, or timer leaks on the backend?
7. Are approval/question content, image references, changes, copy, locate, and idle-subagent follow-up preserved despite omitted tool fields?
8. Are the initial preview cap, refresh interval, page budgets, concurrency, cache size, and TTL reasonable for measured workloads? Which values should remain internal constants?
9. Are warm/cold/scrolling targets realistic and measured independently? What evidence is still needed on a real slow device or the private VM?
10. Would a simpler summary-only implementation eliminate enough cost that caching or broader rendering changes can be deferred without missing the requested behavior?

Return findings ordered by severity, with an affected requirement/section, a concrete failure scenario, and the smallest correction. Distinguish required fixes from optional alternatives and measurements still needed. State whether implementation can begin, which stages are safe, and which decisions block dependent work.

## 13. Code and evidence map for the reviewer

These pointers describe the inspected checkout, not permanent line-number contracts. Inspect current status before acting; a clean worktree at the base SHA omits the existing transition/history patch.

| Area | Current source |
|---|---|
| EventSource creation, navigation, composer selection guard | [App](../../../web/src/App.tsx) |
| Task paging, prepend/scroll anchoring, panel ownership | [Task](../../../web/src/components/Task.tsx) |
| Tool/activity disclosures and subagent rows | [Transcript](../../../web/src/components/Transcript.tsx) |
| Existing grouping, tool labels, question extraction | [Transcript helpers](../../../web/src/lib/transcript.ts) |
| Subagent fetch and unload lifecycle | [Subagents](../../../web/src/components/Subagents.tsx) |
| HTTP/SSE types and routes | [API client](../../../web/src/api.ts), [server](../../../internal/web/server.go), [web types](../../../internal/web/types.go) |
| Event routing, sequence barriers, loading buffers | [Frontend state](../../../web/src/state.ts), [events](../../../internal/web/events.go) |
| Retention, item mutation, live tool output | [Items](../../../internal/web/items.go), [manager](../../../internal/web/manager.go) |
| Read-only history and paging | [History](../../../internal/web/history.go), [history pages](../../../internal/web/history_page.go) |
| Current product/API contracts | [Web ADR](../../adr/0004-web-interface.md), [design](../../../DESIGN.md) |
| Existing focused checks | [History page tests](../../../internal/web/history_page_test.go), [history paging reducer tests](../../../web/tests/history-paging.test.mjs), [API tests](../../../web/tests/history-api.test.mjs), [transcript tests](../../../web/tests/transcript.test.mjs) |

Local evidence is retained outside the repository. It may not be available to a reviewer on another machine. The measurement tables and methodology above are self-contained; reproduce results before relying on them for release.

- `/tmp/uam-task-switch-20260925/transition-fix.md`: transition benchmark and checks. The same directory holds the original captured snapshots and baseline assets.
- `/tmp/uam-history-paging-20260925/verification.md`: paging behavior, benchmarks, checks, and artifact inventory.
- `/tmp/uam-history-paging-20260925/browser_fixture_test.go` and `overlay.json`: fake-provider Go fixture serving the previous and current frontend bundles. The fixture regenerates task IDs on restart.
- `/tmp/uam-scroll-smoothness-20260925/findings.md`: continuous-scroll methodology and findings.
- `/tmp/uam-scroll-smoothness-20260925/scroll-bench.mjs`: trusted-wheel/CDP reproduction. Its saved browser WebSocket URL must be refreshed for a new browser session.

Do not publish captured conversation content, credentials, or process environments as part of a review. Prefer synthetic data for portable tests.

### Suggested review prompt

```text
Review docs/superpowers/plans/2026-09-25-web-lazy-data-and-cache.md against
the current UAM checkout. Start with the problem statement, not an assumption
that the proposed architecture is correct.

This is a read-only review. Do not change code, commit, install, restart, or
release anything. Preserve the dirty transition/history changes. First report
the current SHA and relevant worktree state; the draft was written against
606cb8e7c956292497e7d9d118a908ce19b39524 plus uncommitted work.

Check missing requirements, correctness races, cache invalidation and eviction,
active memory/DOM bounds, hidden-subagent traffic, API compatibility, request
counts, scrolling, and implementation complexity. Challenge the design and
propose alternatives where they solve the stated problem more simply. You may
broaden investigation and recommendations, but separate those from the minimum
implementation scope. Do not treat earlier benchmark numbers as a fresh run.

Return prioritized findings with a plan section or code pointer, a concrete
failure scenario, and a proposed correction. Identify which decisions block
implementation, which stages can proceed, and which measurements are missing.
Do not implement fixes. No provider/model calls are needed for this review.
```

## 14. Review findings (2026-09-25)

Read-only review against `main` at `606cb8e` plus the uncommitted transition/history work. No code was changed and no benchmark was rerun; byte figures below come from the retained capture in `/tmp/uam-task-switch-20260925/fixture-snapshots.json` (218 items, 428,590 bytes compact JSON). Compression is not a missing win: the deployed proxy already serves `/api/` JSON with `Content-Encoding: gzip` (probed 2026-09-25).

Verdict: the problem statement is correct and the proposed architecture is larger than the evidence supports. Two byte sources are missing from the analysis, and three subsystems can be removed without losing any requested behavior. Each item below states what was found, why it matters, and the smallest correction.

### 14.1 Missed

1. **Reasoning text is the second-largest deferrable body.**
   - Finding: sections 2 and 5 count only tool input/output. Capture breakdown:

     | Field | Bytes | Displayed while collapsed |
     |---|---:|---|
     | `tool.output` | 220,135 | no |
     | reasoning `text` (58 items) | ~62,000 | no; only "Thinking…" |
     | `tool.input` | 71,376 | main argument only |
     | assistant `text` | ~46,000 | yes |
     | skeleton fields (id, kind, time, ended_at, status, name) | ~22,000 | yes |

   - Reason: reasoning is about 45% of what remains after the plan's own input/output removal. Reasoning `delta` frames are also a large share of live stream volume while an agent thinks; that share is inferred from the retained ratio, not measured. Full reasoning text renders only behind two disclosures: `ActivityRun` mounts its rows on first open (`Transcript.tsx:455-470`), and `Thinking` (`Transcript.tsx:821`) keeps the text collapsed unless the user expanded that item, remembered in `sessionStorage`. `summarizeActivity` (`web/src/lib/transcript.ts:443`) and the empty-thought skip in `renderRows` (`Transcript.tsx:432`) only test that the text is non-empty.
   - Correction: treat reasoning text as a body. Skeleton carries `has_reasoning`; the body endpoint returns the text; skeleton subscribers receive no reasoning deltas. A `Thinking` row whose remembered state is expanded fetches its body on mount. Roughly 75 KB remains for the whole task, about 82% smaller.

2. **Every tool's output is sent at least twice today.**
   - Finding: `toolOutputSuffix` (`internal/web/items.go:154`) returns false on any status change, so completion re-publishes the full item, including the entire output (up to 256 KiB), to `tool_output=delta` subscribers as well. Image attachment republishes it again.
   - Reason: this doubles or triples wire bytes per tool and is the most likely source of the reported 3–4 MB per EventSource. The plan attributes the symptom to payload volume in general and does not name the mechanism, so the fix could be missed if skeleton mode is staged later or rolled back.
   - Correction: name it as the primary cause in section 1. Skeleton items fix it as a side effect; if stage 1 slips, an interim fix is to publish the completion item without `Output` to delta subscribers, since they already hold the streamed text.

3. **Consumers of `input`/`output` outside expansion are the actual replacement contract.**
   - Finding: section 5 says "compute display summaries on the server" without listing what the client reads today:
     - `mainArgument` (`transcript.ts:81`) JSON-parses `input` for the collapsed row label, "Running:" text, `subagentSummary`, and changed/read file counts (`toolCounts`, `summarizeTurn`, `changedFiles` via `input.path`/`file_path`/`filePath`).
     - `questionOf` (`transcript.ts:278`) reads `ask_user` output to render answers and declines.
     - Completed/idle subagent previews (`transcript.ts:540-541`) and the Subagents panel result (`Subagents.tsx:200,399`) come from the parent `task` tool's output, not the subagent's last message.
     - "Copy command"/"Copy output" (`Transcript.tsx:658-662`) work from the collapsed row menu.
   - Reason: stripping bodies without these exact replacements breaks labels, file counts, answered questions, subagent previews, and copy actions, all of which section 4 requires preserved. Section 6's "latest-message preview" would also silently change what completed subagents show. Server-side `arg` additionally removes per-tool JSON parsing on every client render, which is part of the insertion cost in section 7.
   - Correction: server sends `arg` and `path`; `ask_user` tools keep full bodies (they are small); the subagent preview source stays the parent output's first line; copy actions use the on-demand fetch section 4 already allows.

4. **Skeleton `item` frames will clobber loaded bodies.**
   - Finding: `upsert()` in `web/src/state.ts` replaces the item object wholesale and `appendToolOutput` writes into `item.tool.output`.
   - Reason: a running tool that is expanded and then completes would receive a skeleton `item` frame and lose its loaded body, showing an empty result as complete. That is the "corrupted result presented as complete" failure section 6 forbids.
   - Correction: loaded bodies live in a separate map keyed by `(agent_id, item_id)` and are never merged into `detail.items`. Invalidation then needs no server revision: drop the entry on any `item` or `history` frame for that id. Add a test for the expanded-running-tool completion case.

5. **A second stream needs its own sequence watermark.**
   - Finding: the `update` case in `state.ts` rejects any selected-task frame with `seq <= detailSeq` before routing.
   - Reason: if the detail stream in 14.2.2 is adopted, frames from two connections can arrive out of global `seq` order and the gate would drop valid agent frames. Agent frames already carry a per-agent `snapshotSeq`, so the per-agent path is already order-safe.
   - Correction: apply the top-level gate only to main-stream frames; route detail-stream frames through the per-agent watermark.

### 14.2 Cut

1. **Server-owned activity groups (section 5, "Stable activity identity").**
   - Reason: a skeleton item costs 100–200 bytes, so a worst-case 2,000-item task is about 400 KB and is still paged at 50 rows. Grouping is a pure function of item order already implemented and tested in `transcript.ts`; moving it server-side adds group identity, generation invalidation, cross-page continuation rules, and a golden-fixture parity gate, all to save a few hundred bytes per collapsed group.
   - Correction: the plan's own fallback ("lightweight item summaries") should be the design. State plainly that the tool list arrives before its group opens and let the owner accept that rather than labeling it intermediate.

2. **Stable stream, `PUT /api/event-streams/{id}/view`, stream IDs, view generations, per-resource barriers (section 6).**
   - Reason: the reconnect snapshot's global portion is about 4 KB (projects 386, settings 487, usage 447, sessions ~900 per task). Task switch already measures 49 ms median after the transition fix and 34 ms with recent history, both local, and a control `PUT` costs the same round trip as a reconnect. The protocol's cost is stream IDs, idempotent retry rules, 409 view conflicts, obsolete-frame purging under the manager lock, and a reducer that must merge per-resource barriers; none of that improves the measured switch.
   - Correction: keep the per-selection reconnect for the main stream. Add a second, short-lived detail stream keyed by query (`GET /api/events/detail?session=…&agent=…`, later `&items=…`) that carries its own snapshot frame. Subscribe and snapshot are atomic, which is the alternative section 6 asks the reviewer to compare; it is recreated on disclosure change and never touches the chat stream. Requires 14.1.5.

3. **Bounded cache machinery (section 8): 16 MiB accounting, LRU, TTL, prefetch eviction, ETag/304.**
   - Reason: after stage 1 a recent page is tens of kilobytes, and the only user-visible benefit of a cache is showing the last content during one round trip. The state already does this for one task via `previous`, rendered inert until the snapshot lands. Byte accounting, TTLs and validators add code paths with no measured payoff and their own failure modes (stale UI, references held past eviction).
   - Correction: generalize `previous` into a map of the last five `SessionDetail`s with the same inert-until-snapshot rule; drop entries on `history`, `session_removed`, and logout. Tool bodies use the per-task map from 14.1.4. Revisit only if a measurement shows body re-fetch cost.

4. **The five-token version table (section 5).**
   - Reason: existing `seq`, item-ID cursors, and "any item frame invalidates that body" already cover every row of the invalidation table in section 8. Epoch, history generation, resource revision and view generation are not derivable from anything the server tracks today and would need new plumbing to exist.
   - Correction: keep `seq` only.

5. **Capability handshake (section 9).**
   - Reason: the codebase already negotiates per-subscriber behavior with query flags (`tool_output=delta`, `history=recent`). An old server ignores an unknown flag and sends full items, which a skeleton-aware client can treat as bodies already loaded. A handshake frame adds a state the client must wait for before its first render.
   - Correction: one more query flag, detected by the presence of skeleton fields on items.

### 14.3 Keep, and order of work

- Keep: recent-history paging as implemented, 64 KiB/50-row budgets, one page in flight plus one read-ahead, the `ask_user`/approval/question preservation rules, the "profile before virtualizing" rule in section 7, and the acceptance matrix in section 11 minus the "one EventSource per tab" line.
- Existing paging notes (report-only): the cursor is a bare item ID and `OlderHistory` marshals up to 50 items under `m.mu`. Acceptable; skeleton items make both cheaper.
- Stage 1 (can start now): skeleton subscriber flag; `GET /api/sessions/{id}/items/{item_id}?agent_id=` returning one item with bodies; server-computed `arg`, `path`, `has_output`, `has_reasoning`; preview fields on `Subagent` replacing client-side `agentSteps`; frontend consumers in 14.1.3 moved to the new fields; body map with item-frame invalidation. Reason: it removes the bulk of the bytes, fixes 14.1.2, and depends on no protocol decision.
- Stage 2: detail stream for the open subagent and expanded running tools; drop closed-subagent `item`/`delta`/`tool_output` frames from the main stream for skeleton subscribers. Reason: this is the only interest the server cannot infer from the URL today, and it is isolated from chat ordering.
- Stage 3: re-profile page insertion on the smaller payload. First suspects: `prepend()` in `Task.tsx` reads layout for each anchor from the top of the transcript until the first visible one, then forces a full synchronous re-render with `flushSync`; `historyItemSeq` is a new object per page, so the `useArrivals` callback changes identity, and `ActivityRun`'s memo comparator (`Transcript.tsx:477`) and `ToolRun`'s `arrival` prop make every run re-render; `Transcript` rebuilds `linkInteractions`, `byParent` and groups on each render; `mainArgument` parses JSON per tool per render until 14.1.3 lands. Reason: the measured 50–119 ms tasks start immediately after page insertion, and these are the synchronous paths on that boundary.
- Stage 4: the five-entry previous-detail map, only if warm switching still needs it after stages 1–3. Reason: it is the cheapest cache that meets the requirement and should not be built before the need is measured.

Unverified: private VM behavior, physical touch devices, and any rerun of the earlier benchmarks.

## 15. Follow-up review comments (2026-09-25)

Author: coordinating agent. This responds to section 14 after checking the current frontend reducer, transcript consumers, tool-output publisher, and existing tool-output tests. This was a read-only code review; no benchmark or production test was rerun. Section 14's proxy probe is the earlier reviewer's observation, not a probe repeated here.

The smaller approach is a better starting proposal. Deferring tool and reasoning bodies, retaining client-side grouping, and profiling again should precede a new subscription-control protocol or a general cache subsystem. The original draft introduced more infrastructure than the measurements justify. The following corrections are necessary before adopting section 14 verbatim.

### 15.1 Completion output must remain authoritative

Section 14.1.2's interim suggestion to omit `Output` from completion items is unsafe under the current contract. A tool may produce its only output on completion, append a final chunk, or replace previously streamed text. Also, the reducer's `upsert` replaces the full item, so an omitted output can erase content already held by the browser.

Keep existing legacy completion frames unchanged. In the proposed lightweight representation, a summary update must never masquerade as a complete body. An opened detail receives an authoritative final body, or an explicitly defined replacement/update followed by reconciliation. Test completion-only output, a new final suffix, rewritten output, and completion with images.

The current code confirms that completion/status changes can resend accumulated output. It does not prove that every tool sends output twice, or that this is the primary cause on the private VM. Name it as a confirmed retransmission mechanism and measure its contribution before ranking it as the dominant cause. Keep raw UTF-8 field size, JSON-encoded size, and compressed wire size distinct in future reports.

### 15.2 Body invalidation must also invalidate pending reads

Section 14.1.4 correctly separates bodies from lightweight timeline items. Removing a cached body alone is insufficient: a fetch can start, a newer event can invalidate its result, and the older HTTP response can arrive afterward and restore stale content.

Each load needs task/agent/item identity, a request token, and a sequence barrier for that resource. Invalidation records the relevant sequence even if no body is currently cached. Reject an obsolete response or reconcile it with buffered later updates before displaying it as current. Handle retention trims and history replacement as well as `item` frames. Clear pending work and barriers on task/auth changes and coordinated reconnects.

A new public server revision system may be unnecessary for the first stage. Local request identity and resource-specific ordering still are necessary. A body map scoped to a task may use `(agent_id, item_id)` internally; a shared map must include the task ID too.

### 15.3 A second stream needs explicit ownership and ordering

Section 14.1.5 identifies the global sequence rejection correctly. The existing per-agent `snapshotSeq` only says which events its fetched snapshot already covers. It does not settle every conflict between a main stream and a detail stream, and it does not provide a route for expanded main-agent tool bodies.

Define which connection owns each state field. Main-stream summary/status updates must not overwrite detail bodies, and detail frames must not roll back main-stream status. A late detail response cannot bypass a newer body invalidation just because it arrived on another connection. Specify how snapshots cover deltas, how trims win, and how both streams reset together after failure or server restart. Sequence gaps are valid; arrival order across connections is not a global order.

### 15.4 Open reasoning must remain live

Section 14.1.1 suppresses reasoning deltas, but stage 2 names only subagents and running tools. An expanded reasoning block also needs an initial body, live updates, and final reconciliation. A one-time GET followed by suppressed deltas would freeze it while the agent continues thinking.

Include opened reasoning items in the detail-interest set. Follow existing UI disclosure semantics: if opening an activity group reveals several reasoning bodies, fetch the bounded visible set together or through the shared request limit. Do not require a new click solely to compensate for missing transport behavior. Activate body suppression only after all currently supported open views have a correct detail path.

### 15.5 A five-entry map still needs a size policy

Section 14.2.3 can dispense with a general TTL/ETag framework, but a `SessionDetail` can contain all older pages loaded during browsing. Five such objects and a separate body map can retain substantially more than five recent pages. Evicting a map entry also does not release data still referenced by a mounted component.

Cache only a bounded recent-page projection for task revisits, cap its total accounted bytes as well as task count, and clear closed body references unless they have a defined bounded reuse policy. Keep active DOM/history and in-flight buffers separately accountable. TTL is optional; a memory bound is not.

### 15.6 Reconcile the competing designs before coding

Section 14 proposes lightweight tool rows arriving before expansion, per-selection main-stream reconnects, and a second detail EventSource. Earlier sections propose fetching the list on expansion and one stable EventSource. These are alternatives, not interchangeable descriptions of one implementation.

The next section recommends a smaller first slice and names its scope tradeoffs. It also distinguishes preparation that can begin behind an inactive mode from behavior ready to enable. Appending a review does not by itself resolve the acceptance criteria or authorize changing the user's requested disclosure behavior.

## 16. Consolidated proposal for the next review

Status: recommended revision, awaiting review of the decisions below. This section replaces the conflicting implementation order and protocol/cache recommendations earlier in this document. Preserve the problem statement, existing verified behavior, source pointers, and benchmark methodology. No source change, installation, or release is authorized by this revision.

### 16.1 Scope choices and deferred work

| Topic | Recommended first slice | Acceptance implication |
|---|---|---|
| Timeline | Recent chat plus small tool/reasoning records; retain frontend grouping | Tool-list records arrive before expansion. This is a proposed relaxation of strict list-on-expansion, requiring an explicit scope decision. |
| Bodies | Fetch tool input/output and reasoning only when the existing UI reveals them | Closed details transfer no full body; opened content must remain complete and live. |
| Main connection | Retain the current selected-task EventSource lifecycle initially | Task switches still reconnect. Benchmark the smaller snapshots before adding stable-stream control. |
| Detail connection | At most one additional EventSource for the combined open detail set | At most two streams per tab, never one per tool. This changes the original proposal's one-stream acceptance target. |
| Subagents | Server sends a bounded preview and status when closed; recent lightweight transcript when open | Preserve completed result/error previews and add the requested latest-message preview for active subagents with a clear source. |
| Task revisit cache | Optional later stage: recent-page-only map, at most five tasks and a shared byte cap | Immediate cached presentation remains stale until a fresh snapshot confirms it. |
| Body reuse cache | Start with open-view state; refetch on reopening if freshness cannot be established cheaply | Do not promise zero repeat downloads without a tested validation contract. Add bounded reuse only if measured refetch cost warrants it. |

Defer server-owned activity groups, the `PUT` viewer-control API, persistent IndexedDB, broad resource revisions, and a general cache framework. Retain them as alternatives if the first slice misses the agreed user behavior or performance targets. If strict tool-list-on-expansion remains required, server-owned group reads cannot simply be removed and the lightweight-row slice remains intermediate work.

### 16.2 Small representation and endpoint contract

Negotiate lightweight items using an additive query option on the existing session/detail/history/SSE paths. Keep the default legacy representation intact. Declare the actual representation and detail-stream support in the existing snapshot/response envelope, including for an empty task. This avoids both a separate handshake round trip and ambiguous detection from whether an item happened to have output. An old server ignores the query option; the client uses its legacy path when the response lacks support.

Lightweight records include item/agent identity, kind, timestamps, status, bounded `arg`/`path` previews, and explicit content-availability fields. Preserve full semantic values where needed for file identity; a display-truncated path must not become a file lookup key. Include enough reasoning presence/timing state to preserve counts and working indicators without transferring the text.

Audit the specific consumers listed in section 14.1.3: labels, current step, file/change counts, question answers, parent results, and copy actions. Keep the content required for approvals and questions available under existing limits. Do not rely on the assertion that all `ask_user` bodies are inherently small; measure any exception to the page target.

For a closed active subagent, send a bounded latest-message preview, status, and preview source/time. Keep the earlier proposed 512-byte preview and 250 ms coalescing as tuning candidates; flush status/failure/final changes promptly. For completed or idle subagents, preserve the existing parent-result summary; expose it distinctly from the latest message. Fetch the full parent result only when its result view is opened. Provider output continues to be retained under existing server limits.

Add a read-only item endpoint such as `GET /api/sessions/{id}/items/{item_id}?agent_id=...` for completed body reads. Return identity, the complete retained body, and the snapshot `seq`; reuse current authentication, errors, cancellation, and response limits. No model invocation or conversation prompt is allowed. Defer a separate metadata endpoint unless substantial retained metadata actually needs independent loading.

Use a detail stream such as `GET /api/events/detail?session=...&agent=...&items=...` for live open views. An opened subagent receives a recent lightweight transcript; `items` identifies only bodies currently revealed, including reasoning. The selected detail set must be bounded and validated. Finalize the encoding and limit before implementation; do not place arbitrary full histories or unbounded ID lists in the URL. Older subagent history remains cursor-paged.

### 16.3 Ownership, races, and lifecycle

- The main stream owns selected-task chat, lightweight records, subagent summaries, interactions, and task/global status. It carries no closed-subagent transcript and no unopened tool/reasoning bodies in lightweight mode.
- The optional detail stream owns the opened subagent's transcript and explicitly opened bodies. Its initial snapshot and subscriber registration are atomic. It must not duplicate main-task status ownership. For a live body supplied by this snapshot, do not also issue a redundant body GET.
- Store bodies separately from timeline items. Route main-agent and subagent bodies by task + agent + item ID. Compare each resource against its snapshot/invalidation sequence, not the other stream's latest global sequence.
- Changing disclosures recreates only the single detail stream, with a new local connection token. Close the previous connection first. Reject queued callbacks belonging to the old connection. Preserve continuing visible content as refreshing until the new atomic snapshot covers the gap.
- While a completed-body GET is pending, capture its request token and relevant invalidation floor. A newer mutation, trim, or replacement invalidates that attempt or requires replay of buffered updates. An old response must not recreate a deleted/evicted body.
- A completion summary is never a complete body. If it arrives ahead of detail completion, keep the old body marked refreshing or wait for a covering body snapshot; do not label the old body final. Test both arrival orders.
- A task change aborts its pending reads and closes its detail stream. History replacement resets affected bodies/cursors. Failure or reconnect of either stream invalidates outstanding request tokens and initiates coordinated resynchronization before comparing sequences again. Do not compare old-server and new-server sequences or rely on a replay log that does not exist.
- Start with conservative invalidation and fresh snapshots rather than public revision/epoch machinery. If coordinated reset cannot be implemented and tested reliably, retain an explicit service-epoch marker; simplification must not remove the correctness check.
- Collapse removes detail interest and active body references. Keep pending approvals/questions visible through their existing path. Read errors and unavailable retained content stay distinguishable; no silent truncation or indefinite retry loop.

The transport contract must define bounded snapshot bytes, selected body count, aggregate event buffering, request timeout, and slow-client behavior before enabling the mode. Reuse existing limits where suitable, but do not multiply a per-resource allowance by an unlimited number of open resources.

### 16.4 Cache and rendering policy

First measure lightweight loading with no cross-task reuse. If warm switching still needs improvement, keep at most five recent-page projections under an initial total 16 MiB accounted-data cap. This is a small admission/eviction policy, not a new cache framework. Evict the least recently viewed task as needed, and skip admission of an oversized entry. Do not store the accumulated history array in this map. No TTL or ETag layer is required for this first task-presentation cache.

On revisit, render that task's cached recent page with its own draft, then replace/reconcile it from a fresh selected-task snapshot. Preserve the existing stale-task action guard. Document whether the composer remains inert until confirmation or allows drafting with send disabled; test focus and first-keystroke behavior for the chosen policy. Clear entries on history invalidation, deletion, logout/auth-context changes, and coordinated restart recovery.

Initially release tool/reasoning bodies on close. If later adding reuse, charge those bodies to the same reusable-data budget and validate them before treating them as current. Active transcript pages, DOM, image memory, and in-flight buffers still need bounds and cleanup; the five-entry map does not solve those separately retained references.

Retain automatic scroll loading, cursor stability, anchor compensation, and one older-page request per transcript. Single-page read-ahead remains a proposed experiment, not an existing feature. Profile the `flushSync` insertion, anchor scanning/layout reads, arrival callback identity, grouping work, and Markdown rendering using a CPU trace. Optimize measured contributors only. Increase follow-up page sizes or add a rendering window only when the corresponding request-count or retained-DOM measurements justify it.

### 16.5 Revised implementation stages

1. **Resolve the scope tradeoffs and wire contract.** Decide whether upfront lightweight tool records are an acceptable first slice and whether two bounded streams are preferable to stable-stream control. Specify ownership, resets, payload bounds, and empty/legacy-server negotiation. Stop dependent implementation if these contracts remain incompatible.
2. **Prepare lightweight projection and body reads behind an inactive mode.** Add compact fields, question/result preservation, response representation markers, and safe item reads. Test summary/body separation, authoritative completion, and pending-read invalidation. Preparation can proceed before the frontend switches modes; suppressing bodies in the active UI cannot.
3. **Implement open-view delivery and cleanup.** Include running tool output, opened reasoning, subagent transcript paging, and parent-result content. Test atomic snapshots, both cross-stream arrival orders, trims, restart/reconnect, rapid navigation, and collapse. Only enable lightweight mode when every existing opened-content path works.
4. **Measure and address scrolling.** Compare the current dirty paging build with lightweight mode under identical local and delayed-response workloads. Attribute insertion stalls before changing rendering. Measure requests, bytes, frame intervals, long tasks, boundary waits, and unused read-ahead separately.
5. **Add the bounded recent-task cache if measurements justify it.** Compare cached and uncached revisits. Validate byte/count limits, action guards, focus, invalidation, and released references. Treat completed-body reuse as a separate measured addition with a freshness contract.
6. **Run the agreed focused acceptance checks and document results.** Record source/build identity, exact check scope, request/stream counts, memory accounting, and remaining unknowns. Stop without committing, installing, or releasing unless the user separately requests those actions.

### 16.6 Acceptance changes and required regression cases

Carry forward section 11's correctness, performance, and evidence rules, with these explicit changes for the proposed first slice:

| Earlier criterion | Revised proposal |
|---|---|
| No tool-list download before group expansion | Small bounded tool records may arrive with recent history if that scope tradeoff is accepted. Full tool and reasoning bodies remain deferred. |
| One EventSource; no reconnect on task switch | At most one main and one detail EventSource. Main reconnects on selection; details reconnect only for disclosure changes or recovery. Zero orphaned streams. |
| Reopening unchanged completed details transfers no body | Deferred until bounded body reuse has a verified freshness rule; count reopening requests honestly. |
| Under-100-ms cached presentation | Applies if the cache stage is implemented. Keep cached presentation and confirmed read/write readiness as separate metrics. |
| General revision/ETag invalidation | Local request/connection identity, resource sequence barriers, explicit reset/trim handling, and conservative refetching must prove the same correctness. |

Required additional regression cases are final-only output, a completion suffix, output rewrite, completion plus images, a stale HTTP response after invalidation, completion arriving in either stream first, reasoning changing while expanded, a main-agent tool body alongside an open subagent, a trim during a body read, and either stream reconnecting with old callbacks still queued. Include an empty task and a legacy server in representation fallback tests.

For memory checks, use long conversations with many older pages and oversized chat/body records, not only the 218-item capture. Confirm that the revisit cache retains only recent pages, closed bodies release references, pending buffers have an aggregate bound, and retained heap/listeners plateau under repeated navigation. The cap measures accounted data, not exact browser heap.

Do not promote the new mode based solely on a smaller JSON response. It must preserve all exposed content and interactions, reduce hidden-detail traffic, and avoid new scrolling or freshness regressions. Report a remaining scope gap explicitly rather than treating an intermediate optimization as the full requested feature.

### 16.7 Updated review instruction

Use the prompt in section 13 with this addition:

```text
Sections 15 and 16 are the latest review response and consolidated proposal.
Treat conflicting designs in sections 5–11 and 14 as alternatives retained for
context. Check the corrections as critically as the earlier review. In
particular, assess authoritative completion content, pending-fetch invalidation,
two-stream ownership/reset behavior, live reasoning, and bounded retained data.

The smaller first slice proposes sending lightweight tool records before
expansion and allowing two bounded EventSources. These are explicit scope
tradeoffs, not recorded user approvals. State whether they should be accepted
or whether the original strict disclosure/stable-stream behavior is necessary.
Return a review only; do not implement changes or rerun provider/model tasks.
```

## 17. Reviewer response to sections 15 and 16

Author: the section 14 reviewer. Read-only; nothing rerun. This responds to the coordinating agent's corrections and states a recommended decision for the owner.

### 17.1 Corrections accepted, with adjustments

- **15.1, authoritative completion.** Accepted. The interim suggestion in 14.1.2 to omit `Output` from completion items is withdrawn: `upsert` replaces the whole item, and a provider may deliver its only output at completion. Keep legacy completion frames unchanged. The retransmission at `items.go:154` remains a confirmed mechanism whose share of the reported bytes is unmeasured; rank it after measurement.
- **15.2, pending-read invalidation.** Accepted. The pattern already exists in the reducer: `agent_loading`/`agent_loaded` capture a request, buffer frames, and replay only those past the response's `seq`. Reuse it for body reads rather than designing a new one. A task-scoped body map keyed by `(agent_id, item_id)` is sufficient because the map is dropped on task change; a shared map would need the task ID.
- **15.3, two-stream ownership.** Accepted as a requirement; the split is short. Main stream owns timeline records, status, interactions, subagent summaries. Detail stream owns opened bodies and the opened subagent's transcript. Bodies compare against their own snapshot/invalidation `seq`; status never comes from the detail stream. Both streams reset together on either one's failure. That is a paragraph of contract and a handful of reducer tests, not a protocol.
- **15.4, live reasoning.** Accepted; this was missed in 14.1.1. An expanded `Thinking` row during streaming needs the same detail path as an expanded running tool. Since `Thinking` state is keyed by item ID in `sessionStorage`, a streaming thought is only visible live after an explicit expand, so the interest set stays small.
- **15.5, cache size policy.** Accepted in substance: cache the recent-page projection, never the accumulated `items` array. One correction to 16.4: a recent page is already bounded to 64 KiB by the server budget, so five entries are at most about 320 KiB plus subagent lists. A byte cap is free to add, but the 16 MiB figure is not a decision anyone needs to make; the count cap does the work.
- **15.6 and 16, design of record.** Section 16 is the reduced design from section 14 with the ordering and lifecycle rigor it lacked. It should be treated as the current proposal. Sections 5 through 8 and 14.2 are superseded where they conflict.

### 17.2 The two decisions that block work

Section 16.1 names them correctly. Recommendation for each:

1. **Lightweight tool records arrive before their group is expanded.** Accept. A record is 100–200 bytes; a full 2,000-item retention is about 400 KB and is still paged at 50 rows. Strict list-on-expansion would require server-owned groups, which is the largest single item cut. The requested behavior, "details in proportion to what is opened", is met; the literal mechanism is not. This is the owner's call and should be recorded as an explicit relaxation, not inferred.
2. **Two bounded EventSources instead of one stable stream with view control.** Accept. The reconnect snapshot's global portion is about 4 KB and a control `PUT` costs the same round trip. The detail stream gives atomic subscribe-plus-snapshot without stream IDs or view generations. Prototype it in stage 3 before freezing; if cross-stream reset proves hard to test, the fallback is reconnecting the main stream with the detail interest in its query, at the cost of resetting loaded older pages.

### 17.3 Position on section 16.5 staging

Stages 1 and 2 can begin now; stage 2 is safe behind an inactive mode. Stage 3 is where the remaining risk lives and should be prototyped rather than specified further on paper. Stages 4 and 5 should not start until stage 3 is measured with the section 11 method. Section 16.6's regression list is the right acceptance bar for enabling the mode.

### 17.4 Still open

- Cause of the 50–119 ms insertion tasks. Every suspect in 14.3 is unprofiled. Profile with the lightweight payload before any rendering change.
- Whether `ask_user` bodies and parent `task` results fit the recent-page budget in real tasks. Measure on a tool-heavy capture, per 16.2.
- The private VM. All numbers are local headless Chromium; the reported symptom may have a cause there that none of this addresses.
