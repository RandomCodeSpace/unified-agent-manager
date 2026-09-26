# UAM lazy transcript implementation results

Started: 2026-09-25. Resumed: 2026-09-26. Status: local implementation, independent review and scoped verification complete, including backend compression. This is not a release or installation record.

The [implementation plan](2026-09-25-web-lazy-data-and-cache.md) remains the acceptance contract. Source is based on `main` at `606cb8e7c956292497e7d9d118a908ce19b39524` plus the preserved, uncommitted transition/history work. Root integrated isolated backend, frontend and cache worktrees. A separate agent reviewed contracts and another ran browser verification.

## Implemented behavior

- Initial task data contains recent chat, compact tool/reasoning records and bounded subagent summaries. Full tool input/output and reasoning arrive when disclosed. Question tools retain their semantic content.
- One selected-task stream owns chat and status. One optional detail stream serves up to eight revealed bodies and one opened subagent. Closing a parent suspends child interests. One-off copy reads fetch the complete retained body without adding it to live state.
- Continuing open bodies supply their covered sequence on a detail reconnect. The server acknowledges unchanged bodies instead of resending them. Mutation, epoch and request guards preserve corrections and reject abandoned responses. Reconnecting an opened subagent refreshes its held reading window and recent tail without fetching the gap between them.
- Main and subagent history load automatically in both directions as the reader approaches the held window's boundary. Main history commits can yield while rendering, then restore the reading anchor at commit. Parent-locate keeps its required synchronous path.
- Five recent compact task pages share a 16 MiB accounted-data cache. Cached presentation remains visible during refresh. Capability, epoch, auth, history, trim and deletion changes invalidate entries.
- The unread-state callback now captures only selected task ID, visit times and mount time. Heap snapshots identified and verified removal of a chain retaining prior App renders and transcripts.

Legacy HTTP/SSE representations and provider execution remain unchanged. No new dependency or provider call has been used. There is no IndexedDB, offline editing, or retained cache of collapsed bodies.

## Bounds and known limits

| Resource | Implemented bound |
|---|---|
| Recent/older compact page | 50 complete items, 64 KiB soft target; one oversized item remains intact |
| Closed subagent preview/result prefix | 512 UTF-8 bytes each; preview text coalesces for 250 ms |
| Streams | One main plus at most one detail stream per tab |
| Detail body interests | Eight, admitted by viewport relevance; other revealed bodies become eligible on scroll |
| Foreground HTTP reads | Two shared slots; one older-page read per transcript; 10-second deadline |
| Hydration buffers | 256 frames per owner; 2 MiB for main history and 2 MiB for detail hydration |
| Recent-task cache | Five pages, 16 MiB accounted data; excludes older pages and revealed bodies |
| Active reading window | 150 items, 4 MiB accounted data per transcript; one oversized item remains intact |
| Separate live tail | 50 items under the same 4 MiB allowance; one oversized item remains intact |
| Historical metadata | Up to 2,000 retained item identities and required semantic fields; no chat/body text, images or display strings |
| Main history lookahead | Upward demand only, three viewport heights capped at 1,600 CSS pixels; one request in flight |

These byte limits count specified data, not the entire browser heap. Large retained chat/questions can exceed the page target. A result whose meaningful prose lies beyond its 512-byte prefix may have an incomplete preview until opened.

Cached content is presentation only. Typing, sending and state-dependent actions wait for the fresh main snapshot. Drafts remain in their existing storage and survive cache eviction. This is the plan's explicit safe fallback; immediate drafting during a slow cached refresh is not implemented.

Active history/DOM windowing failed its first traversal check and has since been implemented. The first checkpoint retained older loaded pages within the task. The cache cap and navigation cleanup measurements did not bound that active view.

The reviewed forward-paging and bounded subagent-range backend is integrated. Its four source files match `backend-window-manifest.json`. Backend range admission uses the same UTF-16 string and record accounting as the browser; CJK, astral text and HTML escaping have regression coverage. Frontend data-window integration is complete. Its reducer tests found and fixed stale tail-cache data, a newer live value being overwritten by a page, an evicted correction being appended out of chronological order, and a terminal HTTP page racing newer SSE items.

The checkpoint traversed all 1,800 retained rows in both directions, with at most 150 active items and a 50-item live tail. Transcript DOM peaked at 1,512 elements and diagnostic heap stayed around 20–21 MB. These are checkpoint measurements, not final quiet-run benchmarks. Final integrated variable-height anchoring now passes: four older/newer insertion shifts were 0.25, 0.78125, 0.484375 and 0.453125 CSS pixels. The final quiet measurements and subsequent paging-race verification are complete below. Compression is a separately authorized followup.

## Verification completed

- Main integration passed 103 focused frontend tests covering history, state, transcript, motion, drafts, detail ordering, read scheduling and cache behavior. Ten task/unread tests passed after the callback retention fix.
- The bounded-window checkpoint passed 130 focused frontend tests, TypeScript and scoped ESLint. The final turn-identity followup passed 14 helper tests, TypeScript and scoped ESLint. Root's anchor/helper lint and tracked diff check passed after integration. The main checkout initially failed its normal TypeScript command because the handoff omitted `allowImportingTsExtensions`, which was present in the author's isolated tree. Adding that single compiler option resolved all six TS5097 diagnostics; `tsc --noEmit` then passed without command-line overrides.
- Race-enabled Go checks passed for compact representation/detail transport plus affected history, retention, output and event-stream contracts. The later result-summary line-preservation change passed its focused Go test.
- Production Vite builds passed. Existing Mermaid IIFE `import.meta` and large-chunk warnings reproduced. The quiet benchmark entry asset is `index-CCLBIMOY.js`, SHA-256 `a5ae568f9e39b05954d6a00bda57f67cd05891eddcc0be9c62d913d66e063f16`, preserved under `/tmp/uam-lazy-20260925/verified-candidate-dist`. The asset manifest is `verified-candidate-assets.json`.
- Independent static reviews covered ordering, completion corrections, loaded subagent range refresh, bounded interests, cache invalidation, epoch checks, callback retention and active windows. Final browser acceptance remains separate from those static reviews.
- An isolated React/browser fixture passed 71 anchor checks against the integrated anchor source, with maximum visible drift of 0.3125 CSS pixels, all 450 rows reachable in both directions, and trusted wheel/composer input during a 1.2-second pending load. The integrated captured-content case subsequently exposed the separate anchor failure described above. Keeping a stable group header fixed can still move the message being read. Preferring visible concrete rows that survive the page resolved isolated 3,370-pixel and 5,205-pixel failures to zero drift. A further answered-question test exposed an 8,485-pixel shift because its interaction ID was outside the history item IDs; the narrowed guard resolved it to zero. The followups passed 23 affected checks plus three final focused cases. The final anchor source is SHA-256 `1880fabe306b229d4594f81eba284125193dafd309462ea573dfc9b13187b71a`.
- Restoring 12 reasoning disclosures respected the eight-body and two-stream limits with zero settled churn. Telemetry observed two detail reconfigurations within one animation interval during three early frames, so this implementation does not claim at most one reconfiguration per animation frame.
- The browser reproduced a compact turn collapsing when an older page revealed its original user prompt. The stable-identity fix passed that same case across four older pages. Detailed group extension, independent split tool-group state and focus restoration after opener eviction also passed without browser errors.
- Browser fixtures exercised first-key composer focus, task drafts, rapid selection, live tool/reasoning updates, final-only/suffix/rewrite output, nested disclosure cleanup, copy of unloaded bodies, completed subagent result loading, and more than eight remembered open reasoning disclosures. No provider conversation was opened.
- A React development StrictMode fixture reproduced a controller registration failure, then verified loading, unmount cleanup, remount and one-connection retry after the fix.
- Browser checks held a cached task and its selected draft on screen through a 1,200 ms snapshot delay. Controls and typing stayed inert until confirmation, then the first key inserted correctly. A simulated service restart with a new epoch and reset sequence recovered the authoritative changed open body. Simulated authentication loss removed the transcript and closed all EventSources. These fixture paths produced no uncaught errors.

Raw fixtures, scripts, samples and heap snapshots are under `/tmp/uam-lazy-20260925/verification`. They may not exist on another machine. Baseline dirty files and bundles are preserved beside them. Captured content is not included in this document.

## Transport and retention evidence

The first detail-stream prototype resent large unchanged bodies on each disclosure change. Keeping a 256 KiB tool output and 1 MiB reasoning body open while toggling another tool ten times produced 21 snapshots and 27,907,281 raw bytes. The conditional continuation implementation reduced that sequence to 1,343,877 bytes, including 15,100 bytes across the 20 reconfigurations after initial delivery. The corresponding gzip-equivalent totals were 14,109,252 and 677,124 bytes. Compression was calculated for comparison; these are not claims about deployed wire compression.

This is a 95.18% reduction from the failed prototype, not a claimed 95% improvement over the previous application. A legacy full snapshot was about 1.33 MB; the legacy recent snapshot in this synthetic fixture excluded the large old bodies. Unrelated subagent events did not force body resend, while a real tool mutation did.

After the callback fix, application object, closure and context counts and sizes had zero growth between cycles 25 and 100. String storage decreased by 244 bytes. DOM nodes stayed at 653, listeners at 297 and active server subscriptions at one, with at most two concurrent streams. The earlier long chain through prior App render contexts disappeared from heap retainers.

Total used heap rose from 9,694,448 to 10,519,064 bytes across those checkpoints. Heap snapshots attributed the increase to compiled code and browser/DevTools records, rather than growing retained application objects. Reporting that total as a flat heap would be incorrect. Final fixture/browser shutdown and zero-subscriber confirmation remain part of cleanup.

The separate long-history traversal failed at this checkpoint. Importing 2,400 synthetic Markdown chat items exercised the server's trim to 1,800 retained items. The browser then accumulated every fetched row:

| Loaded items | Transcript DOM elements | JavaScript heap after GC |
|---:|---:|---:|
| 50 | 507 | 9.94 MB |
| 250 | 2,507 | 26.91 MB |
| 500 | 5,007 | 47.16 MB |
| 1,000 | 10,007 | 86.79 MB |
| 1,800 | 18,006 | 149.89 MB |

There were 35 older-page requests and no browser errors. A fresh browser retained one document throughout, so this was active transcript growth rather than navigation/BFCache retention. The smaller captured fixture grew from 121 to 1,753 transcript elements and 6.52 to 15.48 MB while loading all 218 items in four requests. These findings require the planned bounded active window; the server retention cap alone does not pass that gate.

## Performance comparison

The additional release baseline is the actual annotated `v0.10.8` tag, commit `36e17de46cd3f47a0aabb557d4d1180d92e5cb7c`, tag object `c44f0422009124bd0db6009c49f4fd13d43a47cb`. The isolated fixture uses its backend source and committed `index-CqzX7xX4.js`, SHA-256 `a1d4554efbd2680480352088696cf190f8e9f3d3eb57d2d6cfec688217ee9f45`. The preserved dirty checkout is a separate comparison and must not be labeled v0.10.8.

Final paired runs use the same captured data, viewport and browser. Six task clones force cache misses for cold switching; repeated visits to the original tasks measure cached paint separately from confirmed readiness. Each switch comparison includes four warmups and at least 30 measured selections. Local, 200 ms and 1,200 ms response delays are reported separately.

### Cold switching and cache presentation

Corrected quiet runs used Chromium 151.0.7922.34 at 1280×720. Each cold run cycles six task clones to exceed the five-entry cache, takes four warmups, then measures 30 selections. Runs were serialized without concurrent builds or other test browsers. Confirmed readiness requires the matching selected-task snapshot and a non-inert pane. Paint is the following animation-frame proxy, not a hardware display measurement.

| Comparison | Baseline median / p95 | New median / p95 | Median change |
|---|---:|---:|---:|
| v0.10.8 readiness, pair 1 | 290.3 / 318.8 ms | 35.5 / 46.0 ms | 87.77% faster |
| v0.10.8 readiness, pair 2 | 287.2 / 313.6 ms | 35.9 / 42.4 ms | 87.50% faster |
| v0.10.8 paint, pair 1 | 310.9 / 347.9 ms | 49.8 / 52.8 ms | 83.98% faster |
| v0.10.8 paint, pair 2 | 312.6 / 336.5 ms | 50.0 / 51.0 ms | 84.01% faster |
| Dirty recent-history readiness, pair 1 | 33.0 / 40.9 ms | 34.3 / 40.8 ms | 3.94% slower |
| Dirty recent-history readiness, pair 2 | 31.6 / 46.2 ms | 33.3 / 45.1 ms | 5.38% slower |
| Dirty recent-history paint, pair 1 | 38.7 / 53.3 ms | 50.1 / 50.9 ms | 11.4 ms slower |
| Dirty recent-history paint, pair 2 | 36.2 / 51.7 ms | 47.8 / 52.2 ms | 11.6 ms slower |

The plan's cold confirmed-readiness gate passes in both dirty-baseline pairs, below its 10% regression allowance. The median paint regression against that already optimized, unreleased checkout is real in these measurements, approximately one display frame. Its p95 changed by -2.4 ms and +0.5 ms. This is not a claim that every latency metric improved. Compared with v0.10.8, both readiness and paint improved in both pairs.

Across the release pairs, readiness ranges were 262.2–320.0 and 258.0–344.6 ms, versus 21.9–47.2 and 23.6–45.4 ms for the new build. Dirty baseline ranges were 22.7–57.2 and 21.3–46.5 ms, versus 23.2–47.1 and 22.4–45.3 ms for the new build.

| Injected snapshot delay | Cached presentation median / p95 | Confirmed readiness median / p95 |
|---|---:|---:|
| Local | 25.4 / 30.2 ms | 42.6 / 59.0 ms |
| 200 ms | 33.6 / 36.8 ms | 239.6 / 250.1 ms |
| 1,200 ms | 33.8 / 37.4 ms | 1,242.0 / 1,253.1 ms |

Cached presentation meets the 100 ms p95 target independently of network delay. Fresh confirmation still gates typing and actions. Archived tasks have no editable composer, so their drafting readiness is reported as unavailable rather than confused with task readiness. All these runs reported zero browser errors.

The first probe accidentally waited for the presentation-frame promise before measuring the confirmed paint frame, adding an extra frame in some cases. Its readiness timings remained valid, but all final pairs were rerun after separating those clocks. Superseded output is preserved under `verification/superseded-extra-frame`. Tables above use only corrected runs: `v0108-cold-pair*-switch.json`, `final-release-pair*-switch.json`, `dirty-cold-pair*-switch.json`, `final-dirty-pair*-switch.json`, and `final-cached-*-switch.json`.

Earlier diagnostic runs established that synchronous history commits caused insertion stalls. A yielding commit with a pre-commit anchor eliminated the observed insertion long tasks in one local run and kept a delayed-scroll anchor within 0.25 CSS pixels. Those diagnostic results are not the final paired performance gate.

### Scrolling, actual traffic and retention

The new build met the fixed scroll target at each delay. Trusted wheel runs recorded 96.65%, 96.54% and 97.00% of animation intervals within 18.7 ms for local, 200 ms and 1,200 ms page responses. Each run had a 16.8 ms p95, no observed insertion-associated PerformanceObserver task over 50 ms, three page requests, 85,823 history bytes and zero unused prefetched pages. At 1,200 ms delay the reader spent 1,017 ms at the boundary; local and 200 ms runs recorded zero boundary wait. Isolated RAF gaps still reached 116.7 ms, so this does not mean every frame met the budget.

The v0.10.8 wheel runs are not equivalent pagination workloads: it sends all 218 items upfront and requires a Show earlier messages control. Wheel-only input makes no history requests there, so injected history delays have no effect. Its raw frame results and a 4.07-second gap remain in the evidence, without attributing that gap to application insertion.

| Uncompressed response-body workload | v0.10.8 | New compact build |
|---|---:|---:|
| Initial closed task | 440,730 bytes | 32,155 bytes |
| Initial plus all 218 retained items | 440,730 bytes | 123,468 bytes |
| Closed child updates | 670,865 bytes | 252 bytes |

These exclude headers and chunk framing. Actual requests with `Accept-Encoding: gzip` returned no `Content-Encoding` on either version. The initial transfer falls 92.70%; reading all retained items falls 71.99%. Compact history needs four additional pages versus seven for the dirty recent-history protocol. Returning through evicted history costs refetches: the 1,800-row up/down traversal made 68 page requests totaling 804,682 bytes. The window deliberately trades those reads for bounded retained data.

| 100-cycle comparison | v0.10.8 | New compact build |
|---|---:|---:|
| Heap after GC, cycle 25 | 28.47 MB | 9.94 MB |
| Heap after GC, cycle 100 | 82.40 MB | 10.69 MB |
| Application object count change | +38,047 | -245 |
| Closure count change | +1,189 | -26 |
| Context count change | +566 | -8 |
| DOM / listener counts at both checkpoints | 901 / 360 | 665 / 297 |

The new build's aggregate heap increase is chiefly compiled code and browser tracking, while application object, closure and context counts decline. Maximum concurrent streams were two for the new build and one for the release. All error arrays were empty. This supplies direct release evidence for the callback retention fix, not merely a comparison with an intermediate prototype.

The final 1,800-row traversal passed both directions with a 150-item reading window, 50-item recent tail, 1,512 transcript elements, and roughly 20–21.3 MB post-GC heap. The 601-item single-turn fixture then exposed a request ownership race: passive cleanup triggered by a changed history cursor aborted the next forward request and left loading active. The fix removes the cursor from the lifetime identity, performs task/reset cleanup during layout, and cancels only the controller owned by a request. The exact instrumented failure now passes all 601 rows in both directions, 22 successful pages, and one pending read at most.

That followup is a Task-only change on asset `index-CilEpfIk.js`, SHA-256 `d280205c50f0eceef34b006f0a73070c3028d4e228a883f282f6ff9a64b73867`, under `/tmp/uam-lazy-20260925/paging-owner-dist`. Earlier quiet timing/traffic/memory tables were measured on `CCLBIMOY`; they must not be relabeled as timings of `CilEpfIk`. An analogous subagent reconnect probe passed without changes. Independent review cleared the Task fix.

The full uncompressed evidence report is `/tmp/uam-lazy-20260925/verification/uncompressed-checkpoint-report.md`, with raw CPU profiles, heap snapshots, request records, browser contracts and reproduction commands beside it.

## Completion and release state

The uncompressed phase is complete. The paging fix passed the exact huge-turn case, pending-request navigation cancellation and return/retry, keyboard anchoring at 0.25 px, and parent lookup through four pages. Its final 100-cycle run on `CilEpfIk` measured 10.19 to 11.09 MB post-GC heap from cycles 25 to 100, with zero object, closure, context and array count growth. DOM stayed at 733 elements and listeners at 305; streams remained at most two; no browser errors occurred. Source review, scoped TypeScript/lint and the production build passed.

`uncompressed-checkpoint-manifest.json` freezes 70 evidence artifacts. `final-owned-cleanup.json` records zero subscribers, stopped owned fixture processes and closed ports 8317–8319. The real UAM service and unrelated browser were preserved.

The user then authorized backend compression. Its goal is lower actual wire transfer for API JSON, both SSE streams and embedded text assets, with identical decoded content and prompt incremental delivery. The implementation uses standard-library gzip at the existing routing boundary, with no dependency. File/download and range behavior remains outside this followup. Required checks cover negotiation/headers, HEAD, streaming flush/deadline/cancellation, actual encoded bytes and scoped browser timing. Compression implementation and independent review are complete. Root integrated the exact reviewed three-file patch `525526df0d208fba617a21d92b199caa57fc1aa071dbe1f87517104b95ef3d0a`. It adds `compression.go`, `compression_test.go`, and one routing call in `server.go`. The before/after manifest is `/tmp/uam-lazy-20260925/compression/manifest.json`.

Focused compression, auth, CSP and stream regression tests passed. An independent `TestCompression` run passed too. Real HTTP clients manually decoded main/detail snapshots, tiny live updates and heartbeats before the stream handlers returned. HEAD metadata, range/download identity, negotiation/Vary, deadline forwarding, errors and subscription cleanup passed. The same `CilEpfIk` frontend is used for both new-build identity and gzip comparisons. The actual encoded-wire probe passed 55 checks. Response bytes were counted before decompression, with exact decoded hash/event equality:

| Same final build | Identity body bytes | Gzip body bytes | Reduction |
|---|---:|---:|---:|
| Main JavaScript | 942,185 | 345,080 | 63.37% |
| Main CSS | 64,671 | 15,455 | 76.10% |
| Compact main snapshot | 32,163 | 7,725 | 75.98% |
| Task JSON plus four history pages | 113,489 | 33,660 | 70.34% |
| Initial detail with three bodies | 1,811 | 958 | 47.10% |

Ten tiny main/detail updates arrived independently, with roughly 1.6–3.4 ms measured local wire latency. Both compressed streams delivered the actual 15-second heartbeat while remaining open; subscriptions returned to zero. PNG, refused gzip, HEAD and Range checks passed. These are measured compressed response bodies, excluding HTTP/chunk/TLS overhead. The JSON/history workload differs from the earlier SSE-initial-plus-history workload, so their totals must not be mixed.

Native EventSource delivery passed too. Ten main updates arrived at a median 3.7 ms and maximum 5.2 ms; ten detail updates had a median 3.7 ms and maximum 4.4 ms. Both responses carried `Content-Encoding: gzip`, decoded the actual 15-second heartbeat, and remained live. Closing a body released the detail connection. No browser errors occurred and active sources stayed at most two.

Both identity and gzip variants use the same new backend and `CilEpfIk` assets; the identity override belongs only to the test adapter. Six fresh-browser runs each used four warmups and 30 cold selections across six clones. The balanced order was release, identity, gzip, then gzip, identity, release. The final source/build is directly measured here:

| Variant | Pair 1 readiness median / p95 | Pair 2 readiness median / p95 | Pair 1 / pair 2 paint median |
|---|---:|---:|---:|
| Actual v0.10.8 | 283.9 / 347.0 ms | 287.2 / 348.2 ms | 302.2 / 312.8 ms |
| Final build, identity | 34.6 / 43.2 ms | 35.8 / 48.6 ms | 50.1 / 50.4 ms |
| Final build, gzip | 37.1 / 46.0 ms | 35.8 / 47.2 ms | 50.4 / 50.4 ms |

Gzip's confirmed-readiness overhead is 7.23% in pair 1 and 0% in pair 2, within the 10% scoped limit. Paint changes by 0.3 ms and 0 ms. The final compressed build improves confirmed readiness by 86.93–87.53% versus v0.10.8. All 180 selections passed with zero browser errors and verified response-encoding headers. No timing rerun was used to remove outliers.

The same final wire probe measured the release's initial full-task stream at 440,766 body bytes, versus 7,725 bytes for the new compressed recent snapshot, a 98.25% reduction. Reading all retained history costs 35,152 compressed body bytes including the main snapshot, a 92.02% reduction versus the release's upfront full history. Opening deferred bodies adds their traffic; these figures describe closed disclosures.

Compression cleanup confirms zero subscribers on all three fixture ports, closed named browser, stopped owned fixture processes and closed ports 8317–8319. The real UAM PID 1936490 on port 8260 and unrelated browser were preserved. Raw evidence is in `compression-comparison-summary.json`, `compression-wire-results.json`, `compression-native-browser.json`, and `compression-owned-cleanup.json`. The final code manifest is `/tmp/uam-lazy-20260925/compression/integrated-code-manifest.json`, SHA-256 `c2bd088172d8c46295e9e436c38f128f0478889d49f6d821b636fc0ed845832a`.

The completed compression report is `/tmp/uam-lazy-20260925/verification/compression-verification-report.md`; its frozen artifact and binary hashes are in `compression-verification-manifest.json`.

The 100-cycle heap comparisons above were measured before enabling gzip. Compression-specific checks cover encoded bytes, decoded equality, incremental delivery, timing, writer cleanup and subscriber cleanup. No full frontend memory/traversal matrix was repeated for a backend-only compression change.

No correctness failure remains in the exercised paths. The secondary median-paint tradeoff against the earlier dirty checkout remains documented above; these results do not claim that every metric improved. The private VM, physical touch devices, hosted CI and deployed artifact are unverified. Source remains local and uncommitted. Nothing has been pushed, merged, installed, restarted or released by this implementation run.

## Initial full-page UI load, 2026-09-26

This followup measures initial document navigation and normal page reload against actual v0.10.8. It is separate from the task-switch benchmarks above. The final compressed build reaches task/composer readiness faster in every measured profile, but cold startup adds main-thread long tasks and local first-content paint is slightly slower. This is not a clean no-regression result across all metrics.

The comparison used the same captured, editable CONFIG task, Chromium 151, a 1280 by 720 viewport and unthrottled CPU. Each of eight version/cache/network cells has two warmups and 15 measured loads, for 120 measured loads total. Versions alternate within paired rounds. Cold means a fresh browser with an empty HTTP cache; warm means a normal full-document reload with cached assets and fresh React state. Browser process launch time is excluded. The simulated connection uses CDP latency of 100 ms, 10 Mbps download and 2 Mbps upload. No provider calls were made.

Readiness starts at navigation and ends after the matching authoritative snapshot arrives and the selected task's connected, visible, enabled composer remains writable through the next event-loop turn. Delaying the snapshot by 1,200 ms proved that the probe does not report readiness early. Trusted typing passed for both versions and all eight warmup cells. This is DOM/composer readiness, not proof of displayed pixels when paint measurements are missing.

| Profile | v0.10.8 readiness median / p95 | Final compressed readiness median / p95 | Median improvement |
| --- | ---: | ---: | ---: |
| Cold, local | 351.9 / 1,201.6 ms | 261.6 / 304.9 ms | 25.7% |
| Cached reload, local | 193.7 / 217.3 ms | 129.5 / 176.9 ms | 33.1% |
| Cold, simulated connection | 1,878.2 / 2,171.0 ms | 958.5 / 988.5 ms | 49.0% |
| Cached reload, simulated connection | 857.5 / 898.2 ms | 430.8 / 480.1 ms | 49.8% |

All 120 readiness samples are retained, including outliers. No measured navigation was repeated or replaced. p95 uses nearest rank; with 15 samples per cell it equals the observed maximum, so it is a coarse tail estimate.

| Profile | First content median, release / new | Last observed LCP median, release / new | Valid paint samples, release / new |
| --- | ---: | ---: | ---: |
| Cold, local | 120 / 126 ms | 660 / 294 ms | 13/15 / 14/15 |
| Cached reload, local | 72 / 72 ms | 484 / 156 ms | 15/15 / 15/15 |
| Cold, simulated connection | 1,092 / 604 ms | 2,216 / 988 ms | 13/15 / 14/15 |
| Cached reload, simulated connection | 172 / 172 ms | 1,076 / 456 ms | 15/15 / 15/15 |

First content may be a loading shell. LCP is the last observed entry at a requested readiness-plus-1,000-ms cutoff, with actual cutoff saved per sample; it is not a finalized Web Vital. Six cold loads lacked both paint measurements, four on the release and two on the new build. Recorded frame diagnostics show visible, focused pages with valid geometry but roughly one-second animation-frame gaps and empty raw paint entries. The scheduling cause is unresolved. Missing paint remains null, and the associated readiness outliers remain in the table. Setup failures and the two probe revisions are preserved in the methodology notes.

Cold local finite-response transfer completed before readiness falls from 994,989 to 433,421 bytes, a 56.4% reduction. Under the simulated connection it falls to 392,462 bytes; one font finishes after readiness there. These totals use DevTools encoded transfer accounting and exclude the open EventSource. They are not total page traffic. DevTools supplied no usable encoded-byte count for open SSE despite delivering its snapshot, so that field is unavailable. The separate raw-wire compression evidence above remains the source for actual SSE compression. Every cached measured reload had real cache hits for JavaScript, CSS and both fonts. Its finite-response transfer was 2,594 bytes on the release and 2,290 bytes on the new build, still excluding SSE.

The main-thread tradeoff needs followup. The release had no observed task over 50 ms before readiness. The new cold local loads had one such task each, totaling a median 84 ms and maximum 104 ms. New cold simulated loads had a median 96 ms of long-task time and maximum 147 ms across one or two tasks. These tasks occur just after the initial snapshot arrives; their function-level cause has not been profiled. Warm loads have zero median long-task time but isolated new-build tasks reach 55 to 58 ms. Local document load also changes from 93.6 to 101.4 ms cold and 48.4 to 51.2 ms warm. Faster task readiness does not erase these regressions.

The startup waterfall still includes auth before the main stream. On the new cached simulated profile, median auth finishes at 266.6 ms, the main stream starts at 275.3 ms, the selected snapshot arrives at 388.5 ms and readiness follows at 430.8 ms. These are independent medians, not an additive CPU breakdown. This benchmark does not isolate compression from the other frontend changes.

There were zero JavaScript/runtime or EventSource errors through the observation cutoff. Both fixture versions lack the original two attachments and HTML preview referenced by CONFIG, producing documented 404s and preview loading failures. Those requests start after first content in samples with paint data, but missing media can still affect later layout and LCP. The private VM, production routing/TLS, complete media rendering and slower physical devices remain unverified. No new production edits were made for this benchmark.

Evidence is under `/tmp/uam-lazy-20260925/verification/ui-load`: `ui-load-report.md`, `summary.json`, all raw sample JSON, `methodology-notes.md`, reproduction scripts and commands, and `owned-cleanup.json`. The detailed report includes ranges, paired results, resource categories, cache headers, waterfall timings and error counts. It measures release asset `index-CqzX7xX4.js` against final `index-CilEpfIk.js`, using the exact previously verified backend binaries. Root checked all 42 code/test/config files against the integrated build manifest after the run; none changed.

Cleanup confirmed zero fixture subscribers, closed benchmark browsers, stopped owned fixture PIDs 3364703 and 3365057, and closed ports 8317 through 8319. Real UAM PID 1936490 on port 8260 and the unrelated browser were preserved. This followup adds benchmark documentation only; source remains uncommitted and uninstalled.
