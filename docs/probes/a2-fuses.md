# A2 fuse hook probes

Run 2026-10-10 with GitHub Copilot CLI 1.0.94, SDK v1.0.17 and gpt-6-luna. Isolated temporary SDK sessions only; no owner Tasks were prompted. Configuration discovery, skills, file hooks and session store were disabled.

Command: `UAM_FUSES_LIVE_PROBE=1 go test ./internal/adapter/copilot -run '^TestFusesLiveProbe$' -count=1 -v`

Result: PASS, package 36.352s (probe 36.34s).

- (a) `web_fetch` Pre deny: model repeated exact reason `fuse-deny-indigo-943`; no fetch failure hook fired for the Pre denial.
- (b) Safe shell request `ls /etc/ssl`, permission callback `PermissionDecisionUserNotAvailable`: native result `denied-no-approval-rule-and-could-not-request-from-user`; no failure hook. Gate failed. Use the planned `permissionCompletedLocked` fallback, exclude owner answers, deliver the warning on the next matching Pre.
- (c) Explicit gpt-6-luna subagent: Pre sequence `[task web_fetch]`; Failed received `Error: Failed to fetch https://httpbin.org/status/403 - status code 403`. Its AdditionalContext marker reached the parent reply. Both hooks cover subagents.
- (d) Read-only scan of the existing 36 failed fetch journal rows: 19 `Error: Failed to fetch <url> - status code 403`, 15 status 404, one timeout, one validation error. No historical 429. A supplemental isolated live fetch returned `Error: Failed to fetch https://httpbin.org/status/429 - status code 429`; Failed context reached the reply. Only 401/403/429/451 will be recognized; 404 remains out of scope.

The 401 and 451 variants were not observed live. Tests cover their fixed `status code N` form; no Retry-After parsing or general HTTP-error inference is added.

Supplemental acceptance-path probe: `TestFusesLiveProbe/rule-denial` used native session-local `Permissions.Configure` with a deny rule for `ls /etc/ssl`. The RPC succeeded and the command was refused, but neither permission events nor Failed fired (`pre=[bash]`, `failures=[]`, `permission_results=[]`). Package PASS 8.214s, probe 8.20s. Native rule refusals that emit no permission event cannot feed the planned permission-completion fallback. A dedicated Safe Playground Task also confirmed that an ordinary shell request waits for owner permission; no owner answer was supplied.

## Definitive permission-request hook gate

A second isolated probe used the [documented PermissionRequest output](https://docs.github.com/en/copilot/reference/hooks-reference#permissionrequest-decision-control): top-level `behavior: deny` and `message`. The temporary hook matched both the exact isolated session ID and an exact marked `ls /etc/ssl` command. Other requests were untouched.

CLI 1.0.94 and gpt-6-luna emitted exactly one each of `tool.execution_start`, `permission.requested` with `resolvedByHook:true`, `permission.completed` with `result.kind:denied-by-permission-request-hook`, and failed `tool.execution_complete`. Owner permission callback: zero. Failed callback: zero. The turn completed normally and the temporary session was deleted. Three earlier guessed output shapes were ignored and left permission pending; investigation stopped until the documented schema supplied new evidence. No shared hook configuration was changed during these probes.

The adapter therefore retains hidden hook-resolved request metadata only until native completion, counts that automatic result, and never publishes it as an owner interaction. Its own Pre refusals do not produce permission events, as probe (a) demonstrated. The deployed named check will use the same exact session-and-command filter and three bounded turns; it remains pending until deployment.

MCP permission kinds come from the existing verified tool catalog, retained in the existing per-session tool gate and invalidated with it. An internal `ToolUse.PermissionKind` field preserves actual tool names and arguments while grouping different proven MCP tools. No new metadata RPC or provider-wide lookup is added. Unknown tools are not guessed to be MCP.
