# Glab hook probe — CLI 1.0.94

Executed with `gpt-6-luna` in an isolated temporary SDK session. No GitLab
repository, owner Task, or shared project configuration was changed.

```
UAM_GLAB_LIVE_PROBE=1 go test ./internal/adapter/copilot -run '^TestGlabLiveProbe$' -v -count=1
```

Result: PASS, 13.432 seconds. The native bash input was a map containing
`command` and `description`. The hook cloned that map, replaced only a harmless
marker in `command`, and returned it through `ModifiedArgs`. The resulting file
contained the replacement marker, proving the changed command executed.

The same two-call probe checked the working directory contract:

```
call 1: cwd=$SESSION; command=cd $SESSION/scratch && pwd
call 2: cwd=$SESSION; command=printf glab-probe-before-572 > $SESSION/marker; pwd
```

The second hook still received the original session directory after the first
shell call changed directories. A governor gated by the hook's working
directory cannot be activated for a nested GitLab scratch repository by this
shell `cd` sequence. A live acceptance Task must have the scratch repository
as its actual session directory; changing a shared project's origin is not a
substitute.

The test is opt-in and skips during ordinary package checks. Its temporary
session is disconnected and deleted after the run.
