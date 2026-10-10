# A2 fuse hook probes

Run 2026-10-10 with GitHub Copilot CLI 1.0.94, SDK v1.0.17 and gpt-6-luna. Isolated temporary SDK sessions only; no owner Tasks were prompted. Configuration discovery, skills, file hooks and session store were disabled.

Command: `UAM_FUSES_LIVE_PROBE=1 go test ./internal/adapter/copilot -run '^TestFusesLiveProbe$' -count=1 -v`

Result: PASS, package 36.352s (probe 36.34s).

- (a) `web_fetch` Pre deny: model repeated exact reason `fuse-deny-indigo-943`; no fetch failure hook fired for the Pre denial.
- (b) Safe shell request `ls /etc/ssl`, permission callback `PermissionDecisionUserNotAvailable`: native result `denied-no-approval-rule-and-could-not-request-from-user`; no failure hook. Gate failed. Use the planned `permissionCompletedLocked` fallback, exclude owner answers, deliver the warning on the next matching Pre.
- (c) Explicit gpt-6-luna subagent: Pre sequence `[task web_fetch]`; Failed received `Error: Failed to fetch https://httpbin.org/status/403 - status code 403`. Its AdditionalContext marker reached the parent reply. Both hooks cover subagents.
- (d) Read-only scan of the existing 36 failed fetch journal rows: 19 `Error: Failed to fetch <url> - status code 403`, 15 status 404, one timeout, one validation error. No historical 429. A supplemental isolated live fetch returned `Error: Failed to fetch https://httpbin.org/status/429 - status code 429`; Failed context reached the reply. Only 401/403/429/451 will be recognized; 404 remains out of scope.

The 401 and 451 variants were not observed live. Tests cover their fixed `status code N` form; no Retry-After parsing or general HTTP-error inference is added.
