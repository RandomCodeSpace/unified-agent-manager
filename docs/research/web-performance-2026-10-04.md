# UAM web performance audit, 2026-10-04

Source baseline: `5591b0848863936ef4aa8d25586d5c564d3acad8`.

Scope: initial loading, recent Usage UI, event streams and transcript rendering. Source analysis by a separate reviewer, live resource measurements through the authenticated T3 browser, and a local production web build. Source findings describe **potential impact**, not measured regressions or release-gate failures.

## Scorecard

| Metric | Value | Source | Target | Status |
| --- | --- | --- | --- | --- |
| LCP | Not measured | None in this source audit | ≤2.5s | — |
| INP | Not measured | None in this source audit | ≤200ms | — |
| CLS | Not measured | None in this source audit | ≤0.1 | — |
| Lighthouse Performance | Not measured | None in this source audit | ≥90 | — |

Framework: React 19.3 client-rendered SPA, Vite 8.3, Tailwind CSS; Go HTTP backend. No third-party analytics/scripts found in the inspected entry path. Browser resource byte counts are separate observations and cannot establish Core Web Vitals. No Lighthouse report, performance trace, field data, or usable paint/interaction samples were available.

## Measurements and limits

Live target: `https://uam.randomcodespace.dev`, 2026-10-04 at 01:20 UTC.
The deployed asset was `index-DXHxSZWx.js`; the local simplification below was not deployed.
Raw sanitized measurements: [web-performance-2026-10-04.json](web-performance-2026-10-04.json).
No conversation content, API response bodies or credentials are included.

| Live asset | Decoded bytes | Gzip body bytes |
| --- | ---: | ---: |
| Initial app JavaScript | 1,368,758 | 495,640 |
| Shared runtime JavaScript | 716 | 438 |
| Initial CSS | 85,310 | 20,070 |

All three returned HTTP 200, HTTP/2 and gzip, with `Cache-Control: public, max-age=31536000, immutable`.
The initial navigation reused cached assets. Explicit `fetch` calls with `cache: 'no-cache'` verified actual response bodies and nonzero transfers separately. These fetches were audit probes, not app-request duplication.

Three sequential read-only Usage requests returned HTTP 200 in **96.8, 87.1 and 91.1 ms**, including response-body consumption. Bodies were about 10.6 kB decoded. This small current-history sample does not establish p95 latency, server CPU time or lock contention at scale. The observed navigation TTFB was 105.1 ms, also a single browser observation, not a cold-load benchmark.

The local production web build produced these rounded sizes:

| Local asset | Minified size, decimal kB | Build gzip estimate, decimal kB | Loaded on initial page |
| --- | ---: | ---: | --- |
| Entry JavaScript | 1,368.66 | 416.37 | Yes |
| ECharts | 951.84 | 314.65 | No |
| Terminal | 452.21 | 116.32 | No |
| Syntax highlighting | 73.64 | 24.45 | No |
| File browser | 7.56 | 2.85 | No |
| Mermaid sandbox document | 3,419.85 | 945.33 | No |

Build gzip estimates and live gzip bytes use different compression settings and are not interchangeable. Deferred assets are listed for feature-specific cost and are not summed into initial page load. The formatting change is not a performance optimization, and no speed improvement is claimed.

## Ranked findings

### High: secondary views are part of the initial app import graph

- **Area:** Loading / JavaScript.
- **Location:** `web/src/App.tsx:12` (SettingsView), `:13` (RoutinesView), `:21` (PlannerView plus controller); `web/src/components/Settings.tsx:5` through `:14`; `web/src/components/planner/Planner.tsx:18` through `:24`.
- **Evidence:** App imports these views synchronously before authentication/route selection. Settings pulls configuration, account, MCP and pricing UI; Planner imports Board, Card, Map, Inbox and Tree view code. Only terminal content is explicitly lazy at `App.tsx:39`. Hiding a route does not remove its imported code from the entry graph.
- **Potential impact:** cold navigation pays transfer, parse and compile cost for views the user has not opened. The entry size is measured below; this inspection cannot attribute all entry bytes to these views.
- **Small recommendation:** begin with an independent secondary view such as Settings or Routines and use the existing `lazy`/`Suspense` convention. Verify the affected direct hash navigation and loading state, then compare entry bytes. Planner requires keeping its controller/context available to other screens; defer that split until justified rather than splitting the whole shell at once.

### Medium: Usage aggregation holds the shared manager lock for lifetime-scaled work

- **Area:** API / interaction latency.
- **Location:** `internal/web/token_usage.go:151` through `:190`; caller `web/src/components/Usage.tsx:51` through `:67`.
- **Evidence:** every report locks `m.mu`, scans `m.tokens.Days` separately for all four periods, merges cached harness buckets, prices models, and sorts every result before unlocking. The ledger explicitly has no lifetime retention cutoff (`token_usage.go:64`). An open popup requests this report every 15 seconds.
- **Potential impact:** as daily/model entries and concurrent viewers grow, aggregation can delay other manager operations that share the lock. No lock contention or slow response was measured; do not label current usage as slow on this evidence alone.
- **Small recommendation:** first measure report duration and mutex hold time using a realistic long-history fixture. If material, aggregate the four periods in one ledger pass, or snapshot the necessary data under lock and compute outside it while preserving a coherent price/usage snapshot. Avoid adding a cache until repeated computation is measured as a cost.
- **Boundary:** this is SDK daily-aggregate processing, not repeated harness file collection. The harness collector already runs in the background once per minute and caches summaries (`internal/web/harness_usage.go:180` through `:228`).

### Low: an open Usage popup keeps polling when its tab is hidden

- **Area:** Network / background work.
- **Location:** `web/src/components/Usage.tsx:51` through `:67`, `:142`.
- **Evidence:** polling is correctly mounted only while open, but `setInterval` has no document-visibility check. Cleanup prevents stale UI writes but does not abort an already-started request.
- **Potential impact:** redundant API/network work when the open popup is in a background tab; a quick close/reopen can leave the prior read finishing in parallel with a new read. Browser timer throttling reduces but does not eliminate this behavior.
- **Small recommendation:** skip interval reads while `document.hidden`, refresh on return if freshness requires it, and pass an AbortSignal on close only if this request is observed to remain expensive. Retain the existing pending/stale-result guards. This is lower priority than initial bundle cost.

## Positive observations

- **Explicit stream ownership:** `web/src/App.tsx:354` through `:504` creates one primary EventSource for the selected task and closes it on effect cleanup. Transcript deltas are batched into a requestAnimationFrame dispatch (`:403` through `:414`, `:473` through `:476`). Repeated task switches do not by themselves demonstrate a stream leak.
- **Demand-scoped detail stream:** `web/src/components/Details.tsx:62` through `:89` admits at most eight body interests, prioritizing visible content; `:120` through `:182` opens a separate detail stream only when needed and closes it on cleanup. Expecting only one total SSE connection would be incorrect for an expanded task.
- **Bounded transcript/body work:** `web/src/components/Task.tsx:162` through `:171` computes a visible transcript window. Whole oversized message bodies are displayed as plain text (`web/src/components/Transcript.tsx:826`), avoiding megabyte Markdown parsing.
- **Incremental Markdown rendering:** `web/src/components/common.tsx:808` through `:829` memoizes finished blocks while streaming. Highlighting is loaded dynamically (`:434` through `:455`) and capped at 100,000 characters (`:459` through `:479`). Large active fenced blocks may still merit a trace if typing/streaming becomes sluggish; source inspection alone does not justify a rewrite.
- **Heavy dependencies deferred:** terminal (`App.tsx:39`), files (`Task.tsx:40`), syntax highlighting (`common.tsx:444`), and ECharts (`web/src/lib/echarts.ts:6`, loaded by its component) are deferred. Mermaid lives in an on-demand sandbox document rather than the entry bundle (`web/vite.config.ts:41` through `:50`). Its large file size must be attributed to diagram use, not added unconditionally to initial load.
- **Asset delivery:** hashed `/assets/` receive one-year immutable cache headers (`internal/web/server.go:1291` through `:1293`); HTML and the diagram frame revalidate (`:1270` through `:1288`). HTTP gzip exists, including flushing for streaming responses (`internal/web/compression.go:17`, `:154` through `:171`). Live checks confirmed HTTP/2, gzip and immutable asset caching.
- **Usage is local and bounded in the UI:** closed popup does not mount its reader (`Usage.tsx:142`); open polling has an overlap guard (`:55`) and cleanup (`:66`); switching period selects already-fetched aggregates instead of requesting again (`:68`). No charting package is imported by Usage.
- **Fonts:** entry imports two self-hosted variable families (`web/src/main.tsx:4` through `:5`); there is no font-CDN handshake. Font timing/CLS was not measured.

## Recommended next evidence

Use a reproducible cold-load and a representative long transcript to measure bundle transfer, scripting and interactions. Record live protocol/cache headers separately. Do not fabricate Lighthouse scores or call warm-cache navigation timing real-user LCP/INP. The user declined view lazy loading after a separate measurement found only about 8% fewer initial JavaScript bytes. The selected follow-up is eager vendor splitting, recorded below. Usage aggregation and background polling remain findings only.

## Completed simplification and verification

The local change centralizes partial-cost labels in `Usage.tsx` and reuses formatted cache counts between the tooltip and accessible label. Unpriced, zero, sub-cent, cent and ordinary dollar values retain the same rendering. Polling, collection, prices and accounting are unchanged.

- Original 10 Usage DOM cases passed before changes.
- Five cost-boundary characterization cases also passed on the original implementation.
- All 15 cases passed after the simplification, including first-open positioning, periods, info tooltips, retry and stale-report behavior.
- ESLint passed for `src/components/Usage.tsx` and `tests/dom/usage.test.tsx`.
- The web TypeScript check and production Vite build passed. The build emitted large-chunk and Mermaid IIFE/import.meta warnings; these were not suppressed or repaired in this scope.
- An independent reviewer approved the scoped diff with no findings.

Commands, from `web/`: `./node_modules/.bin/vitest run tests/dom/usage.test.tsx`, `./node_modules/.bin/eslint src/components/Usage.tsx tests/dom/usage.test.tsx`, `./node_modules/.bin/tsc --noEmit`, and `./node_modules/.bin/vite build`, each through `rtk proxy`.

The audit and measurements were completed on `refactor/usage-simplify` before release publication. No production restart was performed during this audit.

## Eager vendor splitting

The user chose build-time splitting while keeping Settings, Planner and Routines eager. The only application change in this follow-up is `web/vite.config.ts`: three groups for React, UI controls and Markdown, each limited to initial dependency roots. Default recursive dependency capture is preserved, with React first. Vite emits modulepreload links for these groups automatically. The separate diagram IIFE configuration is untouched.

| Startup JavaScript | Baseline | Three vendor groups |
| --- | ---: | ---: |
| Largest file | 1,368,661 bytes | 792,904 bytes |
| Total decoded | 1,369,377 bytes | 1,368,870 bytes |
| Total gzip, Go BestSpeed | 496,078 bytes | 493,164 bytes |
| Requests, including entry and runtime | 2 | 5 |

The largest file is 42.1% smaller. Total transfer is about 0.6% smaller. Four modulepreloads allow startup chunks to be fetched together; these measurements do not establish a page-load latency improvement. No component lazy loading or dependency version changes were added.

A preliminary size-based vendor group was rejected: it produced 19 startup requests, including 42-byte and 209-byte files, and increased Go-gzipped transfer to 499,949 bytes. Explicit library groups avoid that fragmentation.

Validation:

- Production build, web TypeScript check and ESLint for `vite.config.ts` passed.
- The final built startup files match the measured candidate byte-for-byte.
- The static chunk graph is acyclic, vendor chunks have no app-entry back edges, and every supporting startup chunk is preloaded.
- Existing Terminal, Files, ECharts, highlighting and history-cache entries remain dynamic.
- CSS and the diagram frame are byte-for-byte unchanged. The baseline and final builds emit the large-chunk and diagram IIFE warnings.
- Independent config review approved the change with no blocking findings.
- Runtime UI behavior is **unverified**. The native preview could not navigate to the temporary local server. A temporary compiled-entry Vitest probe was stopped after three file-resolution failures; those failures do not establish an application defect or a passing runtime check. No browser startup-time improvement is claimed.

The implementation uses the installed [Rolldown code-splitting API](https://rolldown.rs/reference/OutputOptions.codeSplitting) and Vite's generated preload links. The detailed comparison is stored in the adjacent JSON artifact under `bundleSplitting`.
