# A1 trail hook probe

Run on 2026-10-10 with Copilot CLI 1.0.94, SDK v1.0.17 and gpt-6-luna.
The opt-in `TestTrailsLiveProbe` uses temporary files and separate SDK sessions.

- Native `apply_patch` hook arguments are a raw string containing `*** Begin Patch`, `*** Update File: probe.txt`, the hunk, and `*** End Patch`.
- The hook returned only `AdditionalContext`, asking for a nonce absent from the prompt. The final reply included that nonce and the temporary file changed from `before` to `after`. No `ModifiedArgs` was returned.
- Plain `edit` is unavailable in this model's tool set. The model reported "no `edit` tool is available in this session" and made no tool call. AdditionalContext on that exact tool remains unverified. The implementation accepts documented `path` arguments but does not depend on plain edit for the live check.

Observed apply_patch probe output:

```text
tool=apply_patch args_type=string
AdditionalContext alone reached final reply; edit succeeded
--- PASS: TestTrailsLiveProbe/apply_patch (8.26s)
```

Run explicitly with `UAM_TRAILS_LIVE_PROBE=1 go test ./internal/adapter/copilot -run '^TestTrailsLiveProbe$' -v -count=1`.
