# Semgrep remediation checklist

Status: completed review and verification. No production defect was confirmed; only focused security-contract tests and review artifacts changed. See `semgrep-results.md` for warnings and the unverified browser check. See `plan.md` for the baseline, evidence, constraints, and acceptance criteria. One writer owns each implementation worktree. Each confirmed fix is a separate small unit with its nearest tests.

## Baseline and coverage

- [x] T1: Preserve the raw reports and reconcile the dirty source baseline before implementation.
- [x] T1: Account for all 673 findings in a disposition ledger, retaining unresolved matches and evidence for grouped closures.
- [x] T2: Check the three partial-parsing locations with their native parsers; resolve supported issues or document scanner coverage exceptions.
- [x] Checkpoint: verify source identity, finding totals, and the explicit coverage gaps. Do not edit application code merely to clear a scanner warning.

Dependencies: T1 first; T2 uses its fixed baseline. Verification is artifact/source inspection and targeted parser/scanner checks. Expected scope is reports/configuration plus isolated parser fixtures if needed.

## Execution authority

- [x] T3a: Trace chart command authorization and pin/refresh behavior; repair only a demonstrated bypass.
- [x] T3b: Trace acceptance-command authorship through owner and agent routes; repair only a demonstrated bypass.
- [x] T3c: Verify terminal authentication, enablement, origin, and shell-selection contracts.
- [x] T3d: Review read-only Git arguments and option boundaries.
- [x] T3e: Review Git write arguments and intended hook execution.
- [x] T3f: Verify Copilot executable resolution and fixed version-check arguments.
- [x] T3g: Verify daemon executable and argument construction.
- [x] T3h: Classify all nine test command-execution matches and the local test WebSocket match.
- [x] Checkpoint: each execution match has a supported disposition; each actual repair has a passing focused regression and affected-rule scan.

Dependencies: T1; apply relevant T2 exceptions. Each production subtask touches only its owning boundary and nearest tests, normally two to five files. Reuse the named tests in `plan.md`; add only tests needed for a confirmed defect or unresolved contract.

## HTTP and diagram boundaries

- [x] T4a: Verify session-cookie behavior across supported HTTP, TLS, and proxy cases.
- [x] T4b: Verify file redirects remain on the current origin with hostile path inputs.
- [x] T4c: Review each SSE and download sink against its content type and serialization.
- [x] T4d: Review diagram message source/payload checks, sandbox isolation, and generated SVG/document boundaries; verify guards with focused tests and mutations. Real-browser execution remains unverified.
- [x] Checkpoint: focused tests establish the reviewed contracts. No sandbox behavior changed. The attempted browser smoke check could not connect; this is recorded as unverified.

Dependencies: T1; use T2 coverage results. Each subtask is independent and stays within one boundary. For T4d, separate bridge changes from build-document changes if their combined scope exceeds five files.

## Remaining findings and policy

- [x] T5a: Inspect all permission matches by object type; do not apply the directory-breaking `0600` autofix.
- [x] T5b: Resolve regex, fixed-host URL, cosmetic randomness, and test-SQL matches with local evidence, one helper at a time.
- [x] T5c: Evaluate Go loop semantics, path conventions, supported-runtime methods, and JSON keys against actual contracts.
- [x] T5d: Check each flagged React initializer's update/remount contract; preserve intentional snapshots.
- [x] T5e: Account for remaining INFO and style matches; defer unrelated internationalization and prop-spreading changes.
- [x] T6: Propose narrowly scoped scan policy and justified exceptions after the relevant findings have dispositions. Keep full raw results accessible.

Dependencies: T1; prioritize T3 and T4 before cosmetic/correctness review. Each demonstrated bug gets one owning-code patch and focused regression. T6 depends on the disposition evidence and does not authorize CI or dependency updates.

## Completion

- [x] T7: Review each repair and its evidence before any requested push.
- [x] T7: Run the final full raw scan with the baseline rule snapshot and compare all original findings; evaluate any agreed project configuration separately.
- [x] T7: Report confirmed fixes, justified exceptions, deferred recommendations, unresolved findings, and remaining parse/CE limitations.
- [x] Stop when the agreed defects are resolved and verification is complete. No commits or pushes without a request.

Dependencies: T2 through T6. Full scan is the final backlog comparison; other verification remains focused unless a concrete affected contract requires broader checks. Completion does not require zero raw findings.
