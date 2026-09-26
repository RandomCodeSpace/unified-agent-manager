# Markdown renderer experiment: implementation and acceptance results

Date: 2026-09-26. Tracking discussion: [#226](https://github.com/RandomCodeSpace/unified-agent-manager/issues/226).

## Result and disposition

The Marked implementation is preserved in an isolated worktree and is **not accepted for integration**. Both candidates failed to establish the required reduction in cold Markdown work. The first candidate materially improved streaming parsing, but that does not satisfy the cold-contributor gate agreed in #226. Candidate 2 removed unnecessary React Fragments without resolving the cold cost.

Keep the current renderer in main. No Marked source, dependency or built asset has been copied into main. This report is the only main-worktree addition from this disposition. No commit, push, release, installation or real-service restart was performed. The existing lazy-history/cache/compression implementation remains intact: all 42 files match its frozen integrated manifest.

The larger 40-navigation matrix and 20-cycle retention check were not started. Repeating them after the prerequisite failed would not establish acceptance. A future attempt needs an evidence-backed cold-path change or an explicit revision of the agreed acceptance criteria; this report does not waive them.

## Builds and ownership

Root was the sole production writer in `/tmp/uam-ui-perf-226-20260926`. Browser verification and source review had separate owners; browser timing runs used a quiet window. All work used deterministic fixtures, with no provider calls.

The main checkout is based on `606cb8e7c956292497e7d9d118a908ce19b39524` plus the uncommitted lazy-history/cache/compression changes. The comparison baseline is that frozen build, **not release 0.10.8**. The earlier release comparison remains in `2026-09-25-web-lazy-data-and-cache-results.md`.

| Build | Main JavaScript | Bytes | SHA-256 |
|---|---|---:|---|
| Frozen baseline | `index-CilEpfIk.js` | 942,185 | `d280205c50f0eceef34b006f0a73070c3028d4e228a883f282f6ff9a64b73867` |
| Candidate 1 | `index-d-8SKI4T.js` | 844,099 | `e9713ff3867052e7167ba6e3ca9bf70e9fe208b1077251f8397dbce105dfb5ea` |
| Candidate 2 | `index-BlXFyQt9.js` | 844,113 | `1f6e322af6503aaf71892d1579d358743711dddbc616ff701f336f663cd09d10` |

Candidate 2 reduces the emitted main asset by 10.4%. These are uncompressed emitted bytes. Existing DevTools finished-transfer measurements for the gzipped main JavaScript are 346,872 bytes baseline and 312,303 bytes candidate 2, about 10.0% lower; those include response overhead and are not body-only measurements. Mermaid's emitted artifact is byte-identical. The only emitted CSS difference is the generated `.table { display: table }` utility; the matched older-history capture had zero elements matching it.

Candidate 2 renderer SHA-256: `092a6c237bb7e3159793e7777194a3c01b39087ffd554228f4ce4193316eaa0a`. Test SHA-256: `354d5ac0b3a72a9c86a24cb0f8fa071993fcdfdd3a8d9016a0d7b01c40b39e57`.

## Implemented candidate

Five incremental source/test files differ from the frozen baseline: `web/package.json`, `web/package-lock.json`, `web/src/components/common.tsx`, new `web/src/lib/markdownReact.ts`, and new `web/tests/markdown-react.test.mjs`.

The implementation uses exact Marked 18.0.14 and marked-footnote 1.4.0 to produce React nodes. Raw HTML remains text; it never enters an HTML sink. Existing link, image, code-copy, highlighting, Mermaid, completed-block memoization and task-file components remain in use. The old react-markdown/remark-gfm renderer stays as a development-only golden reference. Three existing micromark utilities preserve character, URL and HTML-block compatibility. The scoped advisory query returned no advisories for the 12 packages in the new direct dependency closures; the permissive licenses and single-maintainer extension/utilities concentration were recorded separately. No unrelated dependency audit or upgrade was performed.

Independent review found and verified fixes for double-decoding of escaped attributes, parser-instance retention, invalid whitespace footnotes losing text, and a potential sibling-key collision during Fragment removal. Candidate 2 uses positional element keys without wrapping every node in another Fragment.

## Verification completed

- 36 focused renderer tests pass on the final candidate, including code-copy bytes, URL handling, footnotes, a forced-GC parser-lifetime regression and the sibling-key regression.
- Captured-message parity covered 88 assistant/reasoning/notice messages: whole messages and 25 prefix cuts, both whole and split-block rendering, for 4,336 equal comparisons. This run preceded the final key-only correction; the final focused suite covers that correction. No intentional rendering difference was approved.
- TypeScript, changed-file ESLint and a production Vite build passed during candidate verification. The final key correction has focused-test and production-build coverage. Existing focused Markdown/diagram checks also passed. No repository-wide test suite was run for this experiment.
- Real sign-in, protected SSE, missing/invalid/expired-cookie denial and 16 focused browser interactions passed. Candidate 2 repeated all 16 interactions. Coverage includes copy bytes, link/image policy, authenticated attachment display, typing, streamed Mermaid fence readiness and lightbox Escape.
- All 22 source-mapped diagnostic outputs match the corresponding candidate 2 served files byte for byte.

## Candidate 2: fresh authenticated cold profiles

Each variant used a fresh browser with a valid server-issued cookie installed before its first app navigation. Sign-in correctness was tested separately through the real form. JavaScript and messages were not warmed through sign-in. Both variants used the same gzip and authenticated runtime path, with matching selected-task and task-list hashes.

These are one paired diagnostic capture at each CPU setting, with CPU/trace profiling overhead. They are not a repeated, uninstrumented navigation benchmark.

| Work / milestone | CPU | Baseline | Candidate 2 |
|---|---|---:|---:|
| Markdown parser + bridge, initial blocking task | 1x | 18.890 ms | 19.843 ms |
| Markdown parser + bridge, all work before readiness | 1x | 18.890 ms | 25.254 ms |
| Markdown parser + bridge, initial blocking task | 4x slowdown | 85.095 ms | 99.708 ms |
| Markdown parser + bridge, all work before readiness | 4x slowdown | 86.053 ms | 130.795 ms |
| Initial blocking task | 1x | 137 ms | 121 ms |
| Initial blocking task | 4x slowdown | 500 ms | 580 ms |
| Confirmed readiness | 1x | 411 ms | 315 ms |
| Confirmed readiness | 4x slowdown | 1,853.2 ms | 1,298.5 ms |

The apparent readiness improvement does not establish reduced application work. At 4x slowdown, native/program samples before readiness changed from 999.168 to 336.922 ms. Meanwhile, initial React work increased from 128.345 to 159.759 ms and transcript/component work from 93.374 to 121.227 ms.

Candidate 2's complete pre-ready Markdown cost at 4x is 123.387 ms in parsing plus 7.408 ms in the bridge. Marked `getRegex` has 27.323 ms sampled self time; other hot functions include `emStrong`, `blockTokens`, HTML and paragraph tokenization. Even removing the entire measured `getRegex` cost would leave 103.472 ms, above the baseline's 86.053 ms. Source review found no demonstrated small adapter fix sufficient to close that gap. A native-tokenizer fallback can redundantly repeat a failed match in the candidate, but its measured impact is unknown; it is a recorded follow-up, not an assumed solution or a shipped change.

Candidate 2 CPU1 FCP was missing and remains missing. Its CPU4 trace also contains a 1,173 ms post-ready task, almost entirely Chromium's `ThrottlingURLLoader::OnReceiveResponse` for a fixture preview returning 404, with no application Markdown/layout/scroll stack. These findings remain in the evidence; readiness is not substituted for paint, and the native event is not silently discarded.

## Earlier candidate 1 results: useful, not candidate 2 acceptance

Candidate 1 completed 120 measured cold task switches in two balanced pairs with reversed order. Both passed the agreed 10% median ceiling: 36.3 to 36.5 ms in the first pair and 35.4 to 33.8 ms in the reversed pair. These do not establish candidate 2's repeated-switch gate.

The earlier authenticated streamed-reply profiles delivered all 80 deltas and rendered markers:

| Metric | CPU | Baseline | Candidate 1 |
|---|---|---:|---:|
| Markdown parser + bridge | 1x | 195.683 ms | 41.195 ms |
| Markdown parser + bridge | 4x slowdown | 826.034 ms | 190.419 ms |
| Delta-to-DOM p95 | 1x | 25.7 ms | 24.0 ms |
| Delta-to-DOM p95 | 4x slowdown | 131.7 ms | 94.0 ms |
| Long-task time during streaming | 4x slowdown | 1,078 ms | 416 ms |
| Maximum frame interval during the stream work window | 4x slowdown | 150.0 ms | 166.6 ms |

Parsing improved 77–79%, but generic React work and native layout increased. The first candidate also failed to establish a cold parsing win. Its authenticated cold profiles followed UI sign-in and therefore have a warm-JavaScript/messages caveat, unlike candidate 2's fresh-cookie cold profiles above.

The later candidate 2 streaming pair failed the stricter fixture-equivalence assertion: synthetic sessions changed temporary projects and timestamps. Those captures are retained as mismatched evidence and do not support a matched candidate 2 result. The fixture-only correction was prepared, but the failed cold gate stopped further timing runs. Candidate 2's full matched streaming acceptance remains unverified.

## Older history and retained anomalies

One candidate 1 older-history capture recorded an 846 ms task with a 776 ms native `ScrollLayer` operation and 607 ms thread CPU. It remains an unresolved finding; unchanged HistoryAnchor source alone does not disprove a regression.

The fresh matched candidate 2/baseline older-history pair has identical selected data, task lists, 50-item history response hashes, DOM counts, and scroll geometry. DOM counts changed from 423 to 591 in both, Conversation nodes from 125 to 293, scrollTop from 33 to 1,008 and scrollHeight from 630 to 1,568. Maximum `ScrollLayer` time was 0.049 ms baseline and 0.042 ms candidate 2; work long tasks were 65 and 64 ms. The earlier stall did not recur in this one pair. That is a bounded observation, not proof the earlier problem cannot recur.

No completed candidate 2 40-load, repeated-switch, comprehensive anchor or 20-cycle browser-retention result is claimed. The earlier baseline retention results and the focused parser-GC test do not substitute for those gates.

## Secure-only decisions and evidence

[#227's secure-only amendment](https://github.com/RandomCodeSpace/unified-agent-manager/issues/227#issuecomment-5844214675) requires removal of the public/private `--no-auth` flags and runtime bypasses, fail-closed token bootstrap, and authenticated fixtures. It is an agreed future contract; no #227 production code is included in this experiment. Historical anonymous traces remain labeled as historical attribution, not secure-only acceptance.

The [subsequent temp-file amendment](https://github.com/RandomCodeSpace/unified-agent-manager/issues/227#issuecomment-5844308670) agrees exact-file grants on the authenticated owner's existing Open click, no automatic directory widening or prefetch, and a later metadata-only declaration-tool probe restricted to Luna/Ollama. It does not authorize service changes or report-folder grants.

Local evidence root: `/tmp/uam-ui-perf-226-20260926-evidence/`. Candidate 2 diagnostic data are under `candidate-verification/candidate2/`; key files include `cold-function-comparison.json`, `cold-cpu4-native-breakdown.json`, and `older4-pair-checkpoint.json`. The final `verification-stop-report.md`, `verification-stop-summary.json`, and 586-file `verification-stop-manifest.json` record acceptance state and cleanup. Source manifests and incremental baseline diffs are at the evidence root. These local files are not published or committed test fixtures.

The verifier corrected a harness redaction gap for disposable fixture file-grant URL path segments and audited 156 JSON/CPU-profile files, finding zero remaining known derived grant paths or additional unredacted cookie/authorization headers. Timing data were preserved and artifact hashes updated. All owned fixture processes and browsers were stopped, their secret socket removed, ports 8317/8318/8319 closed, and the real port 8260 and unrelated browser remained untouched. Fixture binaries and evidence remain available. No live token was inspected.
