# Unified web UI for multiple UAM instances

Research date: 2026-10-04. Implementation baseline: `29ab70093cba185edec7dfa2e1b3dd85cc3ea3f9`.

The user authorized autonomous phase-one implementation after research. This report records the resolved design. Implementation and acceptance evidence are tracked in [Unified hosted UAM instances with a private shared UI](https://github.com/RandomCodeSpace/unified-agent-manager/issues/360). Source inspection establishes feasibility; it does not establish production readiness or zero regressions.

## Feasibility and starting point

**Feasible now using one hosted UAM as the home UI.** Each connected UAM keeps its own tasks, files, settings and provider runtime. The home UI combines summaries and routes actions to their original owner. No shared task database or task migration is needed.

Start with A hosting the UI and explicitly attaching B, C and further instances in Settings. A saves their connection details and delegated access credentials for reuse after signing into A from another device. A forwards supported requests through a restricted, single-hop proxy. This keeps browser traffic same-origin and avoids exposing saved keys to browser storage.

Both ends need the connected-instance protocol. Existing UAM versions cannot be assumed compatible simply because they expose a web UI. Compatibility is determined by protocol and capability fields, not by comparing release strings.

This is a substantial state and routing change. The Settings form is a small part of the feature: live events, native resource URLs, terminal sockets, persisted drafts, account selection and notification clicks must all retain their owner.

## Settled scope

| Area | Phase-one behavior |
| --- | --- |
| Connected instances | Add by label, HTTPS URL and access key; enable, disable, renew access or remove. Support a collection rather than a fixed pair. |
| Persistence | A stores credentials separately from ordinary Settings and returns only redacted connection metadata. |
| Combined workspace | Combine tasks using the existing task-card presentation, with a compact instance name and no added filters. Each selected workspace uses its owner's settings, models, account and actions; the instance selector is in Settings. |
| Files and terminal | REST, uploads, downloads, ranges, previews, live events and terminal sockets retain their owner through A. |
| Multiple Copilot accounts | Different connected instances can use different accounts. Choosing the instance chooses its current provider account. |
| Usage | Keep per-instance results when unique totals and matching time windows cannot be proved. Unknown or unavailable data is not zero. |
| Notifications | A forwards source-local notices into its own owner notification channel and existing push subscription. |
| Reusable UI | One private local npm workspace package, imported by the hosted app in development and production. No registry publication. |
| Static page | Deferred to phase two, including its login and credential storage model. |

The protected `uam` branch is the integration base for this feature; repository default `main` is unchanged. After research, `git pull --ff-only` confirmed the latest main at the baseline above. Implementation uses isolated `work/uam-*` branches and one writer per worktree. No deployment or live account experiments are part of this work.

## Connection and credential boundary

The chosen path is browser → A → B. A must remain available and able to reach B for controls through A; B's standalone UI and accepted tasks remain independent.

Pairing exchanges the supplied B access key for a dedicated revocable workload grant. A retains that grant and discards the supplied master key. B stores the grant verifier and revocation metadata. Master-key rotation on B invalidates issued grants. A's saved connection does not grant access to B's connection registry, owner login, saved credentials or forwarding routes.

The new registry uses a private, bounded, atomic server-side file store with directory/file integrity checks. This follows UAM's OS-protected credential model; it is not an encrypted-vault claim. Secrets must not appear in ordinary Settings responses, snapshots, logs, exports, URLs or browser persistent storage. Full task and terminal access runs under the target's OS account, so API scopes do not promise isolation from a hostile operator who can read that account's files.

Connections require verified HTTPS. Private-network addresses require explicit opt-in and checked address pinning. Redirects, DNS rebinding, cloud metadata addresses and arbitrary destinations cannot turn the proxy into a general network relay. Existing standalone loopback HTTP behavior is unchanged.

The proxy allows registered workload routes only. Normal task creation (`POST /api/sessions`) remains a workload operation; owner login sessions and connection administration do not. REST uses existing owner authentication on A, while A supplies the workload grant and expected instance identity to B. Remote authorization failures become a connection-scoped error; they must not sign the user out of A.

Browser resource handling needs more than JSON routing. A issues its own task- and generation-scoped preview credentials and preserves the existing file confinement, companion-cookie and sandbox rules. B's cookies and active content cannot become trusted A-origin application content. Terminal origin checks run before opening the remote shell. Native EventSource and WebSocket APIs do not provide arbitrary header authentication, which is one reason to prefer the same-origin proxy in this phase. [EventSource interface](https://html.spec.whatwg.org/multipage/server-sent-events.html#the-eventsource-interface), [WebSocket interface](https://websockets.spec.whatwg.org/).

## Identity, lifecycle and cycles

Each server has a persistent instance identity, separate from the existing per-process event epoch. A connection has its own local registration ID and generation. Pin the expected remote identity before writes; reject self-attachment and duplicate aliases. A replacement server at the same URL requires explicit removal and pairing. Cloned state must not silently become a second independent identity.

All remote entity references, caches, drafts and links include their owner. Local legacy task IDs, storage keys and links remain unchanged. Each source has independent snapshots, sequence tracking, reconnect status and cancellation. Delayed replies from a disabled, removed or superseded generation cannot restore stale state.

| Saved registrations | A's view |
| --- | --- |
| A → B; B → A | A and B once each |
| A → B; B → C; C → A | A and B only |
| A → B and C; B → C | A, B and C once each |
| A → alias of A, or a second alias of B | Reject the duplicate identity |

This rule is structural: remote APIs and notification exports return only locally owned data. The UI's combined view and imported notices are never re-exported as local workload. A never traverses B's registry, so reciprocal registrations cannot recurse.

Disabling B on A rejects new B commands and closes that connection's streams and terminal shell. It does not cancel already accepted agent tasks or stop B's daemon. Re-enabling starts from fresh state. Removing forgets A's registration and access without deleting B's task data. Disabling/removing an active terminal must explain that its shell closes. An accepted write may already have completed; disconnecting is not rollback.

## Multiple GitHub Copilot accounts

Phase one supports multiple accounts through separately configured UAM instances. A workstation can use a personal account, a work server a work account, and another server either account. The number of accounts need not equal the number of instances.

Within one current UAM runtime, changing Copilot sign-in affects that instance's tasks according to its existing account contract. This feature does not switch global CLI credentials per task and does not add simultaneous account profiles inside one runtime. Such profiles require persistent task-to-account binding and account-aware discovery, quotas, utility calls, history and resume handling. The SDK has documented per-session authentication, so that remains a feasible separate extension rather than a prerequisite. [GitHub SDK per-session authentication](https://docs.github.com/en/copilot/how-tos/copilot-sdk/setup/multi-tenancy#per-session-githubtoken).

On one machine, different ports alone do not isolate accounts or state. Instances need separate UAM state and intentional Copilot credentials/state; shared filesystem paths remain shared. Copilot documents `COPILOT_HOME` for its configuration directory. [Copilot configuration directory](https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-config-dir-reference#changing-the-location-of-the-configuration-directory).

Keep three identities distinct: login to the home dashboard, authorization to a connected UAM, and the provider account that executes its tasks. Quota percentages or entitlements must not be added across hosts using the same account. Deduplication requires a confirmed account/host/scope/unit/period identity; otherwise show the observations separately. Current usage periods and imported provider histories do not establish unique fleet-wide consumption.

## Private UI package

The implemented extraction uses one public application import:

```tsx
import { UamApp } from '@uam/ui';
```

```text
web/
  package.json            # private host and workspace root
  package-lock.json       # one lockfile
  src/main.tsx            # thin mount and development-only mock loader
  vite.config.ts          # existing host assembly
  packages/ui/
    package.json          # private source export, React peer dependencies
    src/index.ts          # UamApp public entry
    src/                  # app, state, clients, components, styles
    public/               # worker, manifest and public assets
    tests/                # pure and component tests
```

The package owns providers, styles, fonts and lifecycle cleanup. The host imports the same source entry in development and builds; there is no separate library build or npm publication. Existing commands and the Go asset destination `internal/web/dist` remain intact. One React runtime, lazy chunks, the isolated diagram document and its exact CSP hash must survive extraction. npm links local workspaces, and Vite processes linked ESM source. [npm workspaces](https://docs.npmjs.com/cli/v11/using-npm/workspaces/), [Vite linked dependencies](https://vite.dev/guide/dep-pre-bundling.html#monorepos-and-linked-dependencies).

A future static host can reuse this application boundary. A single import does not supply login, a credential backend, deployment headers, network reachability or an alternate transport. Those are explicit phase-two host concerns. If publication is later requested, add a distribution contract for JavaScript, declarations, compiled styles and package-owned assets; do not add it preemptively.

## Notification ownership

B exports bounded, replayable source-local notices before its own standalone viewer suppression. A maintains one daemon reader per enabled source, independent of open browser pages. Imported notices use a separate presentation channel and never enter A's exported local source stream.

A uses its existing root worker and VAPID subscription for local and imported notices. Qualified event keys, attention counts and click targets retain home/source/connection/generation identity. B's independent standalone push remains intact. Logout preserves existing push opt-in semantics, while opening a task still requires login.

Reconnects, gaps, revocation and generation changes need explicit handling. Accepted push deliveries cannot be recalled, and exactly-once operating-system banners or sounds are not guaranteed. The bounded push queue uses at-most-once cursor consumption: queue overflow or a crash after cursor persistence can lose an alert; an open presentation stream may still receive it. If A is offline it cannot forward new remote push notifications. Real page-closed push behavior requires browser/platform evidence; unit tests alone cannot prove it.

## Regression gates and evidence

The acceptance order is package extraction, secure connection boundary, isolated source state, complete owner workflows/resources, notification integration, then combined verification. Reuse the existing tests and add only behavior tests for affected contracts.

- Preserve standalone login, cookie/CSRF/origin checks, task history, drafts, diagrams, files, terminal lifecycle, provider sign-in and notifications with no connections configured.
- Verify saved registrations across restarts, redacted responses, expected identity, self/alias rejection, grant revocation, master rotation, private-target policy and denied registry/nested-proxy routes.
- Use real A/B/C TLS fixtures with colliding task/project IDs and reciprocal registrations. Count owner actions; do not infer correctness from different fixture IDs.
- Disable one source while work is accepted on several sources. Its channels close, accepted work continues, and another source remains usable.
- Verify that remote authorization loss leaves home login and other sources intact. Exercise resources and uploads as well as the JSON helper.
- Validate source-qualified navigation and stale links, isolated drafts/history, capability differences, offline states and remote versions without triggering a home reload.
- Prove emitted production assets, Go embedding and release packaging after relocation. Exercise native browser resources and page-closed notifications where the environment supports them.

Completed extraction evidence: locked install, one React runtime, 422 pure tests, 462 DOM tests, lint/typecheck/production build, eight scoped Go asset tests and the release-packaging fixture passed. Development source resolution and HMR event delivery were observed. Native preview navigation/snapshots initially failed with generic automation errors. The later user-authorized Chromium run below supplies native interaction and in-page Fast Refresh evidence. These extraction results alone do not certify the later federation changes.

The final local implementation combines the server, proxy, notification and UI
slices on `work/uam-phase1`. The integrated production build passed lint, all 443
pure tests, TypeScript and both Vite outputs. The identical frozen frontend passed
482 DOM tests in 42 files. Eight Go asset checks then passed against the final
generated bundle, including the diagram document's exact security policy.

Focused Go race checks cover the connection boundary and existing auth, CSRF,
event, task, file-key and terminal contracts. Three real TLS A/B/C integration
tests verify colliding IDs and cyclic registrations, disabling one source while
accepted tasks continue, and remote revocation without losing home or another
source. An introduced header-log redaction regression was fixed; the affected
logging and connected-key race checks passed afterward. No unrelated Go suite
was run or claimed.

Regression work also removed a frontend state-publication loop, preserved
healthy clients during another connection's edits, and rejected obsolete events
and qualified links. A remaining New Task test failure was traced to Happy DOM
emitting `hashchange` for `replaceState`; a standalone probe confirmed the
standards mismatch. The test setup suppresses only those synthetic events, with
separate checks preserving genuine fragment navigation. [HTML URL and history
update steps](https://html.spec.whatwg.org/dev/browsing-the-web.html#url-and-history-update-steps).

The user-authorized Chromium 148.0.7778.96 run through Playwright 1.60.0 passed
25 native browser assertion groups using the embedded production UI and real
A/B/C HTTPS routes. Coverage includes standalone login/prompt/settle/reopen/New
Task; pairing and saved connections in a separate empty browser context; cyclic
registrations; colliding-ID owner routing and isolated drafts; remote HTML and
sibling JS/CSS/images in an opaque sandbox; companion-cookie enforcement;
file upload/download and byte ranges; a real remote shell over WebSocket;
disabling unrelated and selected connections; scoped access revocation and repair;
stale links and generation rejection; removal; and a 390px connection form.
The same run proved private-package Vite Fast Refresh preserved an unsent draft
without reloading the document. The HTTPS fixture used test certificates and fake
provider conversations, not live Copilot accounts. A second browser context is
cross-session evidence, not a separate physical-device test.

After screenshot feedback, the added task-list filters and connection-status
prose were removed. Each task shows the configured instance name in a compact
label, or Local for the hosting instance. Long labels truncate with the full
name on hover. Connected rows reuse the existing project,
status, title, branch and time presentation; their actions retain owner binding.
The instance selector appears only in Settings. Instance identity remains available
to assistive technology through an accessible description. The affected 46 DOM
cases passed across focused runs, with lint and TypeScript checks and regenerated
production assets. Seven additional native Chromium checks verified the clean
presentation, Settings selector, selected task, B-only prompt/completion and
390px mobile task/sidebar layout. These focused checks cover the final refinement;
the preceding full frontend suite results belong to the pre-refinement tree.
The subsequent compact instance-name correction passed its focused task-row DOM
test, scoped lint, production asset generation and four native Chromium checks
for labels, retained ownership, Settings-only selection and mobile layout.

No uncaught application JavaScript errors were observed in these browser runs.
Expected negative checks produced disabled/revoked/removed-connection errors and
terminated streams. Native terminals also emitted existing xterm inline-style CSP
warnings; the dependency, policy and external scrollbar fallback match baseline
`29ab700`. No CSP relaxation was introduced. Build warnings remain for chunk size,
`inlineDynamicImports` and diagram-IIFE `import.meta`.

Actual page-closed OS push remains unverified. At completion of local acceptance,
the implementation was uncommitted and had not been deployed or submitted to
hosted CI. The user subsequently authorized a protected merge to `uam`, deployment
and an alpha release; the tracker records that release process and its actual
results. Platform push evidence remains outstanding. Passing automated and
browser checks are not a zero-regression guarantee.

The tracker contains the current implementation and combined verification results. Live Copilot identity/billing attribution, external deployment, supported maximum instance count, and platform push behavior are not established by this research. The subsequent release authorization covers deployment and an alpha release from `uam`. npm publication and connecting additional live accounts remain separate work.

## Canonical decisions

- [Hosted transport and saved-key authentication](https://github.com/RandomCodeSpace/unified-agent-manager/issues/361#issuecomment-5982572377)
- [Instance identity, merged views and account totals](https://github.com/RandomCodeSpace/unified-agent-manager/issues/362#issuecomment-5982572711)
- [Notification ownership](https://github.com/RandomCodeSpace/unified-agent-manager/issues/363#issuecomment-5982589550)
- [Compatibility and regression acceptance](https://github.com/RandomCodeSpace/unified-agent-manager/issues/364#issuecomment-5982633346)
