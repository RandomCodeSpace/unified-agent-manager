# Testing uam

`make test` runs everything that needs no setup. Two further layers exist and
both are opt-in, because they build and run the binary or write artifacts.

## Layers

| Layer | Command | What it covers |
|---|---|---|
| Unit and integration | `make test` | Every package, including in-process hosts and real PTY fixtures that need no external binary. |
| End-to-end | `make test-e2e` | The shipped binary: a real `uam __host`, a real `uam __attach`, a real dashboard, each on its own PTY. |
| Evidence collectors | see below | Manual/CI artifact capture for the multiplexer contracts. |
| Coverage | `make cover` | Writes `coverage.out` and prints the total. |

## End-to-end tests

```
make test-e2e
```

They are skipped unless `UAM_E2E_BIN` points at an absolute path to a built
binary, which is why the target builds first. To run one group directly:

```
UAM_E2E_BIN=$(pwd)/bin/uam go test ./internal/session/ -run TestE2E -v
UAM_E2E_BIN=$(pwd)/bin/uam go test ./internal/app/ -run TestE2E -v
```

The agent under test is a shell script, never a provider, so no API calls or
credentials are involved. Each test gets its own runtime and config directory,
so a run never touches your real sessions.

What only this layer can catch: the wiring between `__host` and `__attach`,
terminal setup and teardown ordering, the control-prefix chord under the
keyboard encodings a provider switches on, mouse-mode replay, role handover
between two live clients, and the dashboard's modal key contract.

A pty master here does **not** honour read deadlines, so the harnesses read on a
background goroutine and poll the recording. A blocking read on the test
goroutine hangs the run instead of failing it.

## Evidence collectors

Several tests capture artifacts for the multiplexer contracts and skip unless
told where to write them. Each requires an **absolute** path, and two also
require an exact final path element:

| Environment variable | Required directory name |
|---|---|
| `UAM_TASK9_EVIDENCE_DIR` | must end in `task-9-ownership` |
| `UAM_TASK11_EVIDENCE_DIR` | must end in `task-11-compat` |
| `UAM_TASK10_EVIDENCE_DIR` | any absolute path |
| `UAM_TASK1_EVIDENCE_DIR` … `UAM_TASK8_EVIDENCE_DIR` | any absolute path |

```
out=$(mktemp -d)
UAM_TASK9_EVIDENCE_DIR=$out/task-9-ownership \
UAM_TASK11_EVIDENCE_DIR=$out/task-11-compat \
  go test ./internal/session/
```

A wrong name is a hard failure rather than a skip, on purpose: it means the
collector would otherwise scatter artifacts somewhere unintended.

## Environment sensitivities

- `t.TempDir()` honours the umask. Under `umask 0002` a fixture directory is
  group-writable, which matters to any test asserting on ancestry warnings.
- Tests that assert a read-only directory blocks writes skip when run as root.
- Liveness tests that read `/proc` skip on platforms without it.
