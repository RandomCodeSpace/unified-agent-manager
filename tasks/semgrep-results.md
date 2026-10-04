# Semgrep review and verification results

Historical audit snapshot from 2026-10-04. This report and its ledger describe the dirty checkout identified below, not the current main branch or the commit publishing these documents. Review and test results for the publishing commit belong to its pull request. Local evidence paths refer to the auditing host.

The review found no confirmed production defect among the scanned matches. Production behavior was left unchanged. Two existing test files now directly cover rejected diagram messages; three isolated guard mutations demonstrated that the tests catch removal of those protections.

## Findings and limits

- The production frontend build passes with warnings about chunks over 500 kB, an ignored `inlineDynamicImports` option, and `import.meta` in IIFE output. These warnings were observed on the starting source. They remain report-only; this review did not establish a runtime failure from them.
- Real-browser execution remains unverified. The collaborative browser opened, but both environment-port and direct-loopback navigation to the temporary fixture failed; a fetch from the preview also failed. The fixture server was stopped. Do not treat DOM tests as proof of browser sandbox execution.
- Semgrep partially parses three files: `internal/web/manager.go:1647`, `web/src/components/McpTask.tsx:58`, and `web/tests/dom/planner-renders.test.tsx:12`. Native Go and TypeScript parsers accept these constructs with no diagnostics. The scanner gaps remain documented; valid source was not rewritten to work around them.
- Semgrep CE has no cross-file dataflow analysis. No new online dependency audit, hosted CI run, production deployment, or platform matrix was performed. The model-price refresh script's fixed-host URL validation was inspected; no remote refresh was executed.

## Dispositions

The [finding ledger](semgrep-findings.csv) records rule, severity, precise baseline location, file hash, disposition, rationale, and verification evidence for each match.

| Disposition | Original scan | Final scan |
| --- | ---: | ---: |
| False positive | 106 | 107 |
| Intentional behavior | 42 | 42 |
| Out-of-scope recommendation | 525 | 525 |
| Confirmed defect | 0 | 0 |
| Total | 673 | 674 |

The additional final match is `no-stringify-keys` at `web/src/mock/data.ts:1236`. The source hash is unchanged. The expression is `changes[t.project_id]`, with project IDs defined by the mock fixtures, not serialized objects. An isolated scan reproduces the match. It is recorded as SG0674; no application change is attributed to the count difference.

The final raw severities are 17 ERROR, 120 WARNING, and 537 INFO. All 673 original matches remain present. No source-level suppressions or broad exclusions were added to make the raw count smaller.

The 525 deferred recommendations comprise 474 untranslated JSX suggestions, 39 prop-spreading suggestions, 11 legacy-runtime `replaceAll` suggestions, and one native-confirm dialog suggestion. They do not establish violations of this project's existing requirements. Seven translation-key warnings are separately classified as false positives because the matched test helper is not a translation API.

The highest-severity production matches are intended execution features. Review covered the Safe-mode chart gate, authenticated pin/refresh routes, owner-only acceptance-command mutation, terminal enablement and origins, structured Git arguments and path boundaries, executable resolution, and daemon argv construction. Existing tests cover these contracts and passed under the race detector. This is evidence for these reviewed paths, not a general claim that the project is vulnerability-free.

## Changes

- `web/tests/diagram.test.mjs`: rejects replies from discarded frames, incorrect origins, unknown request IDs, and malformed payloads before accepting a valid reply.
- `web/tests/dom/diagram-frame.test.tsx`: verifies that an unrelated sender and a malformed parent request cannot invoke Mermaid or cause a reply.
- `tasks/semgrep-findings.csv`: one row per original finding plus the additional final-scan finding.
- This report and the existing plan/checklist: record completion, evidence, policy, and verification limits.

No production source, dependency, CI configuration, or existing user changes were altered. No commit or push was made.

## Verification

| Check | Result |
| --- | --- |
| `GOMAXPROCS=4 go test -race -count=1 -p 2 ./...` | Passed; all packages, 174 seconds |
| `go build ./...` | Passed |
| Frontend `npm test` after test changes | 396 passed; zero failures or skips |
| Frontend `npm run test:dom -- --maxWorkers=2` after test changes | 417 passed across 37 files; zero failures |
| Frontend `npm run lint` on baseline | Passed |
| ESLint on both changed test files | Passed |
| Frontend `tsc --noEmit` | Passed |
| Frontend production Vite build, output outside the repository | Passed with the warnings above |
| Native parsing of the three Semgrep partial-parse sites | Passed with no diagnostics |
| Focused diagram helper and DOM tests | Seven helper tests and four DOM tests passed |
| Three isolated mutations | Removing the parent's source guard, parent's origin guard, or child's source guard each failed on the intended test assertion |
| Final full local Semgrep scan | Completed, exit 0; 675 rules run on 558 files; 674 raw matches; same three partial parses; no reported timeouts |
| Browser smoke check | Unverified due to preview connectivity failure |

Go, type, and build checks were performed on the unchanged production source. They were not repeated after test-only changes. Both complete frontend suites were repeated to check the new assertions and mock isolation. No ordinary test failed; mutation-test failures were deliberate.

## Evidence and source identity

HEAD was `cfaaddd4a9d1a1d26085bd7ffa484e07f34103ce`, including the existing dirty files. The baseline is the actual checkout, not that commit alone. Source snapshots, original and final raw reports, command metadata, test logs, parser results, and mutation results are preserved at:

`/home/dev/.local/state/uam-semgrep-20261004/`

Key files are `baseline/results.json`, `final/results.json`, `comparison.json`, `native-parsers.json`, `mutation-results.json`, `go-race.log`, `web-unit-final.log`, `web-dom-final.log`, and `web-build.log`. The source snapshot is `source-before/`. Retain the raw reports when moving this ledger to another checkout.

## Ongoing scan policy

Keep full raw scans as the audit record. Review changed or new matches against the ledger and source hashes; a historical disposition is not an automatic exception for modified code. Recheck authorization guards whenever their callers or configuration change. Keep tests in the scan and keep ERROR/WARNING security rules enabled.

For a separate security/correctness review view, the four explicitly deferred recommendation rules may be omitted. This is a documented command, not a change to default Semgrep behavior or CI. With this exact installed rule bundle and final results, that view contains 149 matches; those still need their ledger context, and the raw report remains authoritative.

Run from the project root, choosing an output location outside the checkout:

```bash
rtk proxy /usr/local/bin/semgrep scan \
  --oss-only --metrics=off --disable-version-check \
  --config /opt/semgrep-rules \
  --x-ignore-semgrepignore-files --max-target-bytes=0 \
  --exclude-rule opt.semgrep-rules.typescript.react.portability.i18next.jsx-not-internationalized \
  --exclude-rule opt.semgrep-rules.typescript.react.best-practice.react-props-spreading \
  --exclude-rule opt.semgrep-rules.javascript.lang.correctness.no-replaceall \
  --exclude-rule opt.semgrep-rules.javascript.lang.best-practice.javascript-confirm \
  --json-output /tmp/uam-semgrep-review.json .
```

For the raw scan, omit the four `--exclude-rule` arguments. The inclusion flag beginning `--x-` is internal to Semgrep 1.163.0 and must be rechecked on an approved scanner update. No directory-permission autofix, blanket test exclusion, or blanket command-execution suppression is approved by this policy. Scanner/rule updates and CI integration remain separate work.
