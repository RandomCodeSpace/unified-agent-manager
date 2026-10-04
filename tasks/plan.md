# Semgrep remediation plan

Resolve confirmed defects from the full local Semgrep scan while preserving intended product behavior. The 673 matches are an audit backlog, not 673 confirmed bugs. Initial source inspection found several contextual or inapplicable warnings; it has not established an exploitable vulnerability.

Execution status: the user authorized this plan and comprehensive testing. Review and testing are complete; no production defect was confirmed. See `semgrep-results.md` for dispositions, the two test-file changes, full check results, build warnings, and the unverified browser check. The original ordered plan below remains as the rationale for the work.

## Scope and baseline

- The subsequent user instruction authorized remediation and comprehensive testing. No production patch was justified by the review. Dependency upgrades, commits, pushes, and CI changes remain out of scope.
- Baseline: HEAD `cfaaddd4a9d1a1d26085bd7ffa484e07f34103ce` plus the existing modified and untracked files. Source hashes still matched the scan when this plan was prepared.
- Scan: Semgrep CE 1.163.0, local rule revision `a84ff9cc2453ca91d581380de4b8b3f272f6f4be`; 816 rules loaded, 675 reported run, 555 files scanned.
- Results: 17 ERROR, 119 WARNING, 537 INFO. Three partial-parsing warnings. No reported timeouts or skipped-rule errors.
- Evidence: `/tmp/uam-semgrep-lv4h2yfm/{metadata.json,results.json,results.sarif,findings.csv,SUMMARY.md}`. These temporary artifacts must be retained before cleanup if implementation is deferred.
- Owner: one implementing engineer or agent per worktree. Preserve the existing dirty work; reconcile ownership and file hashes before starting each change. An isolated checkout must include the relevant uncommitted baseline, not silently scan or fix HEAD alone.
- Smallest change: fix the owning behavior only when evidence establishes a defect. Keep each implementation unit to one boundary and its nearest tests, normally two to five files.

## What inspection already tells us

| Matches | Evidence and proposed disposition |
| --- | --- |
| 16 command-execution ERRORs | Seven production call sites and nine test call sites. Production includes resolved executables, structured Git arguments, owner-authored acceptance commands, chart commands, and the terminal. Audit authorization and argument provenance before deciding whether a fix is needed. Existing `#nosec` comments explain intent but are not sufficient evidence. |
| One WebSocket ERROR | `internal/web/terminal_test.go:60` connects to a local `httptest.Server`. This match does not establish insecure production transport. |
| 40 permission warnings | The rule treats permission values above `0600` as incorrect even for directories. `internal/web/configuration.go:152` creates a directory with `0700`. Do not apply the supplied `0600` autofix to directories. Inspect each remaining site for file-versus-directory context. |
| Cookie and redirect warnings | `auth.go:269` already uses `HttpOnly`, `SameSiteStrict`, and a conditional Secure flag. `server.go:957` builds a redirect under a fixed `/api/sessions/` prefix. Verify the intended HTTPS/proxy and same-origin contracts; do not break supported local HTTP access to satisfy a literal-pattern rule. |
| Diagram messaging warnings | The frame uses `sandbox="allow-scripts"`; the parent checks the reply's source and opaque origin, and the child checks the sender against its parent. Preserve this isolation. Verify the complete message and rendering boundary before changing origins or escaping. |
| Regex warnings | Composer tokens already escape dynamic text; the diagram attribute names are internal constants; hook validation compiles a user-authored regex; `looksLikePath` rejects inputs over 300 characters before its flagged regex. These require context-specific decisions, not blanket replacement. |
| 474 untranslated JSX and seven translation-key warnings | These are proposed product/style requirements. The translation-key matches occur in transcript tests. Do not introduce internationalization as security remediation. |
| 39 prop-spreading INFOs and six props-in-state warnings | Several state initializers intentionally preserve arrival history, disclosure state, or animation lifetime. Only change an initializer if a supported state transition demonstrably fails. |

## Work order

### 1. Record dispositions and preserve a reproducible baseline

Create a compact finding ledger keyed by rule, path, and source location, with the source hash or snippet identity where lines move. Classify each result as confirmed defect, intentional behavior, false positive, out-of-scope recommendation, or unresolved. Record evidence and the verification that supports closure. Group repetitive findings only when every member shares the same justification.

Acceptance: all 673 original matches are accounted for; raw reports remain intact; unresolved results are visible. Do not classify an entire directory of tests as harmless without checking the flagged operations.

### 2. Resolve or document the three parsing gaps

Inspect `internal/web/manager.go:1647`, `web/src/components/McpTask.tsx:58`, and `web/tests/dom/planner-renders.test.tsx:12`. The reported constructs are a Go generic union, JSX attribute text containing an ampersand, and a TypeScript generic invocation. Use the language's own parser on these files to distinguish invalid source from a Semgrep parser limitation.

For a scanner limitation, retain a precise coverage exception and inspect the skipped region. Test any scanner-version candidate separately only if an update is approved; keep the original rule snapshot for comparison. Avoid rewriting valid application code solely to appease the scanner. A behavior-preserving workaround is justified only when closing the gap is an agreed requirement.

Acceptance: each gap is either rescanned successfully or explicitly documented as incomplete coverage. No unexplained partial parse may be presented as a clean scan.

### 3. Audit execution authority first

Work through these independently, preserving the feature's current authority model:

1. `charts.go:424` and its callers/pins: distinguish unapproved agent commands from commands permitted by the current mode or owner approval.
2. `board_evidence.go:852`: establish who can author and change the acceptance command, including agent-facing routes.
3. `terminal.go:150`: verify authentication, Terminal setting, origin checks, and executable selection.
4. `changes.go:520` and `git_actions.go:693`, as separate units: trace executable resolution, path/revision arguments, option boundaries, and intended hook behavior.
5. `internal/adapter/copilot/web.go:404` and `daemon.go:269`, separately: verify the executable and arguments originate from the documented local configuration.

For each unit, reuse its focused tests and add one regression per confirmed defect. Shell execution is intentional in some of these features; deleting it is not an acceptable remediation. Acceptance means unauthorized inputs cannot cross the existing execution boundary, and authorized workflows still work. Record an evidence-backed disposition when no defect is found.

### 4. Review HTTP and browser boundaries

Treat each boundary below as its own small unit:

- Cookies in `auth.go`: TLS/proxy Secure behavior, HttpOnly, SameSite, and supported local access.
- File redirect in `server.go`: constructed Location stays on the current origin for hostile path values and supported download requests.
- SSE writers in `server.go` and `detail_events.go`: event-stream content type and serialized payloads. Markdown downloads in `export.go`: attachment and content-type behavior. Verify each sink's context before treating raw writes as HTML injection.
- Diagram bridge and generated document in `web/src/diagram-frame/main.ts`, `web/src/lib/diagram.ts`, and `web/vite.config.ts`: reject messages from unrelated windows, validate payloads, preserve opaque-origin isolation, and inspect SVG display and script-document construction.

Acceptance: confirmed boundary failures have targeted fixes and regressions. Existing isolation and non-HTML responses remain intact. Use a focused real-browser check if changed sandbox/origin behavior cannot be established by the existing DOM tests; mocked Mermaid tests alone do not prove browser isolation.

### 5. Resolve remaining correctness and input-handling matches

Use one helper or component per unit. Inspect the regex matches, fixed-host model-price download URLs, cosmetic badge randomness, test SQL, filesystem path conventions, loop-pointer rules against the declared Go language version, `replaceAll` against supported runtimes, array-based JSON keys, and the six React state warnings.

Acceptance: change only demonstrated failures of an existing contract. Preserve intentional state snapshots and updates; distinguish URLs and slash-separated API paths from OS-native paths. Existing tests or a small focused reproducer must establish a bug before an implementation patch. Otherwise record the applicable constraint and close or defer the match.

### 6. Propose a useful ongoing scan policy

After dispositions are supported, propose the smallest local scan configuration that separates security/correctness checks from optional product/style advice. Keep the full-bundle report for reference. Do not add internationalization, rewrite prop spreading, weaken directory permissions, or broadly exclude tests to reduce the number.

If implementation is later authorized, any suppression must identify the exact rule and justified scope. Preserve useful cases of a rule; narrow exceptions instead of disabling security rules globally. New custom rules are justified only by a specific coverage gap and require positive and negative fixtures. CI integration is a separate follow-up, not part of this plan by default.

Acceptance: confirmed defects cannot be hidden by the proposed configuration, and every exception has a recorded reason. Raw versus configured findings remain distinguishable.

### 7. Verify the completed repair set

Each fix gets its focused behavioral check and affected-rule scan. Reuse passing evidence until a relevant change invalidates it. Once all planned repairs are ready, run one full scan using the same raw configuration as the baseline to compare results, and separately evaluate any agreed project configuration. This final project-wide scan verifies disposition of the original full-project backlog.

Acceptance: no unresolved confirmed defects in the agreed scope, no introduced security regression, every original match accounted for, and all scanner gaps explicit. A remaining raw finding is acceptable when its disposition is supported; zero raw matches is not the target. Stop after verification and review. Commit or push only when requested.

## Verification scope

These are future commands, not checks run during planning. Select the named test needed for each unit rather than running every example:

- Backend: `rtk go test ./internal/web -run '^TestChartInSafeModeTakesRowsButNoCommand$' -count=1`; analogous focused names include `TestTerminalRouteRefusals`, `TestTerminalOrigins`, `TestBoardToolSchemasAreStrictAndOwnerFree`, `TestWorkspaceDiffTreatsGitReportedNamesAsPaths`, `TestGitCommitRefusals`, and `TestViewFileRedirectTargetIsBuiltFromTheRoute`.
- Frontend helpers, from `web/`: `rtk proxy node --experimental-strip-types --test tests/diagram.test.mjs`; choose `tests/markdown.test.mjs` or `tests/composer.test.mjs` for their respective changes.
- Frontend DOM, from `web/`: `rtk proxy ./node_modules/.bin/vitest run tests/dom/diagram-frame.test.tsx`; choose the relevant existing DOM file and test-name filter for other components.
- Semgrep: retain `--oss-only --metrics=off --disable-version-check`, the installed local rule snapshot, and baseline inclusion settings. Scope intermediate scans to affected rules and files. Save reports outside the checkout.
- No repository-wide tests, builds, formatting, or dependency audits by default. Name the affected contract and evidence gap before expanding a check. Mark unavailable verification explicitly.

## Remaining uncertainty

The initial checks are source inspection, not completed call-chain audits or exploitability tests. The repair count and implementation effort remain unknown until steps 1 through 4 classify the important matches. The ordered checklist is in `tasks/todo.md`.
