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

## Durable notice follow-up

The first deployed collision exposed a persistence assumption: SDK hook
AdditionalContext is delivered to the model, but CLI 1.0.94 does not put
that callback output in the journal's file-hook `hook.end` events. The
original hook-event notice mapper therefore produced no notice.

The adapter now calls `Session.Log` with `Ephemeral: false` for a trail.
Its `session.info` notification uses the existing live/history notice
mapper and the existing provider journal. Logging has a five-second
bound; a logging failure is recorded and still returns model context.

The isolated native-patch probe was repeated on CLI 1.0.94 with
gpt-6-luna and the race detector. It called Log from inside PreToolUse,
then disconnected, resumed, and read GetEvents. Results:

```text
AdditionalContext alone reached final reply; edit succeeded
Nested Log completed and notification survived disconnect/resume/GetEvents
--- PASS: TestTrailsLiveProbe (11.34s)
--- PASS: TestTrailsLiveProbe/apply_patch (10.74s)
ok internal/adapter/copilot 12.368s
```

The generated context is also covered by a Manager regression using the
recorded JSON-string native patch shape from the collision fixture.
