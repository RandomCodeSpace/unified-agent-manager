# Complete issues 226 and 227

Starting source: `d0c11b0`, released in `v0.11.0-beta.2`.

The owner requested completion of both issues and parallel agents on 2026-09-26.
This plan records the remaining accepted work, rather than reopening rejected
proposals. The governing agreements are the final decision, secure-only
amendment, temp-file amendment and accepted post-cleanup corrections in
[#227](https://github.com/RandomCodeSpace/unified-agent-manager/issues/227).
Later amendments take precedence.

## Problem and completed work

Task switching and long transcripts put too much work on the browser. File
references also triggered individual authenticated probes, stale results and
unnecessary work for invisible content. The recent releases provide lazy
history and bodies, bounded caches, SSE/JSON/asset compression, batched visible
file discovery, deferred message menus and reduced parsing of settled Markdown.

Two performance findings still need attribution: retained JavaScript growth in
an instrumented soak and a historical native ScrollLayer stall during older-page
insertion. File discovery is implemented, but opening files still lacks the
agreed unified preview, explicit temporary-file grants and declaration cards.

The owner also reported that the Changes side panel remains stale until its
manual Refresh button is clicked. An isolated writer will reproduce and fix
that refresh/invalidation contract, preserving manual recovery and avoiding
requests for each streaming token. Runtime verification uses a separate owned
Git fixture on the same dev service, leaving benchmark content unchanged.
The reproduction confirmed no request after an external edit and stale selected
diffs after accepted list refreshes with unchanged path/counts. The accepted fix
refreshes an open, active, document-visible panel every five seconds and when
visibility returns, with one cycle in flight and no queued or closed-panel work.
Background refresh preserves the visible diff and uses the selected scope.

The owner then reported that model and thinking/effort controls are disabled
while a task is working, preventing different settings on queued or steered
messages. A separate writer owns this correction. The queue, steering and SDK
setting contracts must be traced before enabling controls; settings must travel
with the intended submission, not merely appear selectable while the backend
rejects them. Verification remains on the same dev service with Luna/Ollama.
The pinned Copilot SDK has no per-message model/effort override for immediate
steering. Draft controls will remain available during a turn, and each queued
message will retain its requested model, thinking effort and context selection.
Apply that selection before dispatching the next queued turn. Same-settings
steering remains immediate; differing settings require an explicit Queue action,
without silently changing submission mode or aborting the current response.

The owner also requested the missing Utility-model summary for subagent rows.
The accepted timing is completion only: retain the latest activity while a
subagent works, then generate and save one bounded line from its completed
result using the configured Utility model. Disabled or failed generation keeps
the existing report fallback. Repeated events, navigation and reconnects must
not generate duplicate jobs; follow-up work must invalidate an obsolete result.
No per-token calls, periodic summaries or unbounded historical backfill.

The subagent side panel must also honor the same Compact/Detailed preference
as the main conversation. Reuse the existing turn renderer and retain lazy
bodies, questions, history anchors and follow-up controls. Ordinary prose,
paths and tool rows must fit the panel without horizontal scrolling. Code
blocks and tables may keep local horizontal scrolling when their content
requires it; do not hide overflowing content to conceal a layout defect.

## Ownership and boundaries

One writer per worktree. Separate agents own frontend previews and backend file
serving. CPU and memory analysts consume captures. One runtime verifier owns all
browser and API interaction with the existing authenticated dev instance. The
coordinator owns shared contracts, integration, provider-tool work and issue
updates. No additional application, fixture or smoke server may be started.

Preserve user tasks, projects, drafts, credentials and unrelated hosts/browser
sessions. Runtime fixtures are owned synthetic data. Model-backed checks use
Luna or Ollama only. Copilot remains the sole provider through web/SDK. Sonar
findings remain owner-managed. No new release is included in this goal.

## Ordered implementation and acceptance

1. **Resolve the remaining profiling questions on the frozen build.** Use a
   fresh browser for matched heap snapshots, including a no-work control and
   two equal batches of task/subagent transitions. Record the same selected
   task, viewport, cache sizes, DOM/listeners and stream ownership. Attribute
   retained paths before calling growth a leak. Separately repeat the original
   trusted-wheel older-history action at normal and four-times-throttled CPU.
   Preserve outliers and record fixture differences. Fix only an attributed,
   reproduced application defect; report a bounded non-reproduction honestly.
2. **One preview owner.** Normal clicks open one active preview, with existing
   images and diagrams retaining their enlargement behavior. Reuse the existing
   responsive panel/dialog controls and retain external-open and download.
   Close, replacement, task switch and authentication loss cancel reads and
   discard content/frames immediately. No preview bodies or bearer URLs enter
   transcript caches or persistent browser storage.
   The owner also requested compact tags for resolved file references: show a
   format icon and only the filename, with a default file icon for unknown
   formats. Preserve full-path identity in the link and accessible label/native
   title, keyboard activation and modified-click behavior. Reuse existing icons
   and avoid new per-reference requests or tooltip machinery. Actual image
   thumbnails remain images; unresolved temporary-path actions still identify
   the exact path before the owner grants access.
3. **Bounded text and sandboxed documents.** On click, reuse authenticated HEAD
   metadata and request `Range: bytes=0-65535` for text. Enforce the decoded-byte
   cap even if Range is ignored. Cover valid 206 ranges, empty 200/416, short
   responses, truncation, malformed/cut UTF-8, cancellation and unexpected
   encoding. Plain text is the safe rendering fallback. Only file views gain
   own-origin framing; the application keeps DENY and files retain opaque
   sandboxing. The owner accepted interactive HTML with a visible Close button
   and Escape while focus is in the app. The native-dialog browser probe showed
   that an opaque iframe does not forward Escape; no script injection or parser
   dependency is added to work around it. PDF embedding is conditional on actual
   Chromium and Firefox display; external open always remains available.
4. **Explicit exact-file temporary grants.** An absolute path under the real
   configured temp root offers an action displaying the exact path. Only the
   owner's click creates a grant. No grant or content request occurs on render,
   hover or viewport entry. Exclude the temp root itself, directories, runtime
   files and aliases, other users, special files and escapes. Validate through
   retained roots and opened descriptors. Retain identity for the grant's
   lifetime so replacement or inode reuse cannot authorize a new object.
   Revalidate each read, allow in-place live-file changes within limits, and
   close handles on expiry, cancellation, invalidation and shutdown. No sibling
   assets, directory widening or claims that the task produced a file.
5. **Metadata-only file declarations after the provider probe.** Probe normal
   create/resume, recorded host-tool events, subagents, correction after errors,
   discovered-tool name collisions, external Copilot resume and round-trip cost
   using the allowed models. Choose persistence from that evidence. Register a
   bounded `uam_show_file` host tool on normal sessions, never title sessions.
   Return metadata identifiers or correctable errors, never bytes or grants.
   Replay restores cards on the declaring turn, including subagents, without
   restoring permissions. Cards and matching mentions use the same workdir or
   explicit temp opening rules.
6. **Integrate and close with evidence.** Independently review each slice,
   run focused regressions and verify it on the existing dev instance before
   the dependent slice proceeds. Compare final task-switch latency with the
   frozen baseline using identical owned content and viewport. Preserve the
   existing 10% median/p90 regression ceiling and test typing, scrolling,
   anchors, menus, file actions and cleanup. Update both issue acceptance
   matrices and close only when the accepted work and conditional decisions
   have explicit results.

## Additive grant contract

The same-repository browser is the caller. Existing cookie authentication,
cross-origin checks, JSON requirements, `{error: string}` responses and
`Cache-Control: no-store` remain. No list endpoint or new API version is needed.

- `POST /api/sessions/{id}/file-grants` accepts `{path}` and returns 201 with
  `{id, url, name, mime, size, expires_at}`. No automatic POST retry.
- GET/HEAD of the opaque grant URL supports Range and `?download=1`. The URL
  contains no host path. Its distinct signing scope binds Host, task, grant
  identity and expiry; workdir keys cannot authorize it.
- Cookie-authenticated `DELETE /api/sessions/{id}/file-grants/{id}` invalidates
  a grant and returns 204, including an already absent grant.
- `/api/meta.temp_root` supports syntax-only eligibility. The server remains
  authoritative; workdir paths take precedence when a project is under temp.
- Initial bounds: five-minute lifetime, eight grants per task, 32 overall,
  20 MiB per file, 8 KiB request body and 4,096-byte UTF-8 path. Malformed input
  returns 400, invalid credentials 401, unavailable/disallowed targets 404,
  oversized targets 413 and exhausted grant capacity 429.
  If retained root/runtime protection cannot initialize safely, metadata omits
  temp eligibility and creation returns 503 rather than weakening the checks.

## Explicit exclusions and separate tracking

Keep React, Tailwind, Base UI and the existing Markdown parser. Marked, lazy
tooltips and transport changes retain their measured conditional gates; no
framework rewrite, WebTransport, speculative WebSocket migration, IndexedDB or
cache expansion. Multi-file report folders remain deferred. No MCP server,
artifact directory creation or automatic task-provenance rule.

[Issue 233](https://github.com/RandomCodeSpace/unified-agent-manager/issues/233)
tracks the existing workdir-file bearer-key exposure and sign-out revocation
semantics. Preview and exact-file grants must not widen that authority or claim
to fix this separate finding.

The stop condition is verified accepted behavior, integration and accurate
issue closure. Unavailable checks and unsupported conditional features remain
explicit; they are not relabeled as passing.

## Verification decisions

The initial CPU investigation used three normal and five CPU4 trusted-wheel
insertions on owned content. Native ScrollLayer maxima were 0.042–0.479 ms;
the historical 776.112 ms event did not reproduce. Every insertion completed
one history request and preserved the retained row within 0.157 px after
accounting for the wheel. CPU4 still produced one 56.372–83.467 ms React commit,
with one style/layout pass for the existing history anchor. No redundant pass
or safely removable operation was demonstrated, so no speculative anchor patch
is included. This is a bounded nonreproduction on different owned content,
not an explanation or a claimed fix for the historical event.

Matched heap diagnosis used 15 warmup cycles, a no-work control, and two batches
of 50 task/subagent transitions with fresh CDP connections and matched garbage
collection. Post-GC heap grew 1,170,448 bytes across the 100 measured cycles.
Compiled code and V8 metadata class increases accounted for 1,163,360 bytes
(99.39% of that increase); strong paths reached live code and engine dependency
metadata. Native request records were retained by DevTools network inspection.
Application owner, DOM, listener, cache and live-stream counts stayed fixed;
there were no retained closed EventSources after GC. Total heap did not plateau,
and this is not a universal no-leak result. It does not justify an application
memory patch. Fresh CDP sessions reused object IDs, so attribution used matched
classes and retaining paths rather than an invalid survivor-ID diff.

Detailed CPU and heap reports, source hashes, capture limits and raw evidence
are under `/tmp/uam-226-227-completion-20260926T174500Z/`, in
`cpu-scroll-analysis.md` and `memory-analysis.md`. The final integrated preview
and grant changes still require their separate regression comparison.

The initial preview candidate is installed on the same authenticated dev
service as `0.11.0-beta.2-dev.d0c11b0-preview1`. Its binary SHA-256 is
`b0808af530ecf4c797f69a2abfc81104626e2cd995ff7dce86329a197c2bfc4d`.
Deployment preserved the five existing tasks, authentication token, Ollama
environment and unrelated provider hosts. This records artifact identity;
the owned-fixture runtime checks passed bounded text, empty-file handling,
one preview owner, cancellation on close/replacement/task switch, interactive
opaque HTML, visible Close, app-focused Escape and mobile focus restoration.
Image enlargement and external opening, modified-click, exact download bytes,
copy, composer typing, context-menu cleanup and auth-loss disposal also passed.
The test-owned draft was cleared without sending a prompt. UI checks used
Chrome 151.0.7922.34 at 1280×720/DPR1 and one labeled 390×844 mobile case.

PDF embedding is not enabled. Actual Chromium 153 pixels verified top-level
display and failure inside the required opaque iframe. Firefox 150.0.2 displayed
both with its test client's PDF.js enabled; its initial blank result came from
Playwright's default PDF.js disablement. Direct HTTP checks retained the full
server sandbox policy. External-open/download is the common fallback, without
relaxing policy or adding a PDF dependency. Safari remains unverified.

Temporary-file external-open and download actions use the current preview's
grant. Closing that preview revokes it, including subsequent requests from an
external tab. The UI must state the five-minute lifetime, close behavior and
lack of sibling-asset access. An external tab does not create another grant
or prolong its lifetime.

The exact-file grant/tag candidate passed its scoped runtime gate on the same
service as `0.11.0-beta.2-dev.d0c11b0-grants1` (binary SHA-256
`0b4eec6614d29d42f791644f6c873f6e254815e0a47166e24e649d0f44b58e89`).
Fifty recorded API cases passed, with actual five-minute expiry and retained
file-descriptor release before the expired read. Browser checks covered tags,
unknown icons, distinct same-basename paths, trusted keyboard activation,
bounded text, interactive opaque HTML and revocation on disposal. Global
capacity, races and shutdown remain focused-test evidence. No inference ran.

The reviewed queue-settings and Changes corrections were integrated and
installed as `0.11.0-beta.2-dev.d0c11b0-controls1` (binary SHA-256
`60550ae2945a3006650ef22fdb596fd3d30c110f46659280f2efbb06f48b91fd`).
Combined frontend checks passed 28 cases plus TypeScript; the queue writer
also passed 17 focused backend regressions. The mounted Changes check showed
an external same-length edit automatically after 4.63 seconds with one list
and one selected-file request, and no polling during 5.6 seconds closed.
The queued switch also passed on this candidate: same-settings steering
succeeded during an Ollama turn, while a differing-settings steer returned
409. A Luna low/long-context draft with an image survived task switching,
queued without changing the active Ollama selection, and applied before the
next turn. Usage-derived completion telemetry reported Luna; the queue and
draft cleared. A distinct thinking-state interval was not sampled.

The panel density/overflow patch passed focused tests, independent review and
installed-source verification on `0.11.0-beta.2-dev.d0c11b0-combined1`.
It reuses the main turn renderer with separate child disclosure IDs. At a
320 px panel width, the log's scroll width stayed at 320 px in both modes:
Compact showed two turn heads, while Detailed showed two activity rows.
The wide native table and code block each scrolled locally with trusted
ArrowRight input; the outer panel and page stayed horizontally still.

The combined build also contains the reviewed Utility-summary implementation.
It passed the focused integrated race and rendering checks. Repeated complete
events are suppressed; a distinct late result cancels and replaces stale work.
The provider has no authoritative final marker for follow-up replies, so a
previously started stale call can be replaced by another call. Live verification
on `0.11.0-beta.2-dev.d0c11b0-probe3` completed with a Luna parent, an explicitly
selected Ollama child and the configured Luna Utility model. The generated line
appeared in the main transcript, survived reload and task switching, and was
replaced after a follow-up to the same child. The old generated line cleared
while running. The server supplied fresh activity for a 143 ms interval that
the browser assertions did not sample. Focused reducer/row replay verified that
the fresh preview replaces the previous result, with no product change needed.
While an idle child awaits its new Utility line, the existing fallback can show
the original parent result. Utility model selection is established by
configuration and the reviewed helper; independent usage-reported model
telemetry is unavailable for that helper.

The declaration probe completed on the same service with Luna and Ollama.
Both invoked the tool, corrected a missing path, and invoked it after SDK
resume. An explicitly selected Ollama child inherited the host callback.
Recorded results retained their hashes after disconnect; external Copilot
resume without the host truthfully reported the tool unavailable and retained
old journal data. Journal metadata is therefore the selected persistence path.
The child delivered two callbacks for one logical call, so production caches
the bounded result by session and call ID. No separate artifact table is needed.
Observed startup collisions and later tool-list invalidation refuse tool use;
the SDK does not provide atomic name reservation. Replayed metadata remains
untrusted display intent, with no provenance claim, and every file open still
uses the existing workdir or explicit temp-grant route. Production backend and
frontend changes passed independent review and are integrated. The combined
candidate passed fresh Luna Create/Resume and an explicit Ollama child call.
Main and child cards each mounted once in both densities and after reload.
No content or grant request preceded a click; opening read a changed current
file, after which the owned fixture was restored.

A later report identified accepted steers that were invisible until provider
delivery, encouraging a second send. A focused adapter regression reproduces
that gap. The integrated correction shows an accepted message keyed by the
returned provider message ID and reconciles delivery in place. A message delivered
as a later turn moves to its provider-recorded position. Known upload metadata is
retained when a confirming event omits it. Same-text messages with distinct IDs
remain distinct; no automatic resend is introduced. Independent review and focused
backend race/history/reducer checks passed. A live Luna steer rendered its
accepted receipt and reconciled with the exact provider message ID. The API
and browser each retained one row with the pending status cleared.

The separate report that steering abandoned unfinished work did not reproduce
in the bounded live cases. With the exact switch-branch/pull steer, Ollama
continued a code review against the updated fixture and reported its introduced
defect. The original background command's completion may help continuation in
that case. No prompt-policy change or guarantee of model-level completion is
claimed.

## Final combined regression gate

The installed candidate is `0.11.0-beta.2-dev.d0c11b0-completion1`, binary
SHA-256 `0e5a49bdb694bce95255a60547fa954527f37da4e9359c6dab26f2c44ffb6ad4`.
Focused declaration/steer/timing Go race tests, 115 combined frontend
history/state/transcript/file-reference tests, TypeScript, scoped ESLint and
the direct Vite/Go build passed. Independent final review found no remaining
integration issue in the shared history code.

Each benchmark batch retained 30 cold/cached pairs and two excluded warmups
on the same browser, viewport, harness and frozen workload. Baseline browser
assets match beta.2; its binary reports `0.11.0-beta.1-dev.007729e-md1` and is
not the published beta.2 binary. Readiness means the matching task and mounted
history window followed by two animation frames, not a measured pixel-paint
timestamp. All samples and outliers were retained.

| Task switch | Baseline | Candidate | Change |
| --- | ---: | ---: | ---: |
| Cold median | 142.40 ms | 148.90 ms | +4.56% |
| Cold p90 | 156.00 ms | 165.40 ms | +6.03% |
| Cached median | 117.20 ms | 115.35 ms | -1.58% |
| Cached p90 | 134.50 ms | 132.70 ms | -1.34% |

All four ratios passed the agreed 1.10 ceiling, with zero page, console or
runtime errors and unchanged workload hashes. Cold timings increased within
that ceiling; this result does not claim a general speedup or universal absence
of regressions. Full receipts are in `runtime/final-baseline-switches.json`,
`runtime/final-candidate-switches.json` and `runtime/final-switch-comparison.json`
under the evidence directory above. Source promotion still requires the exact
commit's protected-branch checks; this goal does not publish a release.
