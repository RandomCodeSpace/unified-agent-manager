# Testing UAM

UAM now supports the Copilot web/SDK interface. Terminal host, attach, dashboard,
and other-provider suites were removed with those implementations.

## Focused checks

Build the embedded frontend first with `make web`. Then run tests for the
changed packages, for example:

```sh
go test -race ./internal/cli ./internal/daemonruntime ./internal/web ./internal/adapter/copilot ./internal/store
```

The web and Copilot adapter tests use deterministic provider doubles. They do
not send prompts to real models. Each lifecycle test uses disposable config,
runtime directories, and tokens.

`make test` runs the repository tests. `make cover` records coverage, and
`make lint` runs the Go linter. Frontend commands live in `web/package.json`.

## Built-binary lifecycle

```sh
make test-e2e
```

This verifies authenticated startup, survival after the launcher's terminal
closes, sign-in, status, reuse, stop, and rejection of `--no-auth`. To test a
specific binary on Linux:

```sh
UAM_WEB_TEST_BIN=/absolute/path/to/uam go test ./internal/cli -run '^TestWebServiceOutlivesLauncherTerminal$' -count=1
```

The PTY fixture here tests daemon detachment and the token prompt; it does not
implement a UAM terminal harness. Linux distro CI runs the same static binary
and CLI tests. macOS CI checks the runtime directory, process identity, web
startup failure paths, and CLI behavior. The `/proc` launcher-detachment test
is Linux-only.

## Environment sensitivities

- `t.TempDir()` honors the umask; runtime-directory fixtures explicitly set 0700.
- Tests for an unwritable directory skip when running as root.
- Tests that inspect `/proc` skip on platforms without it.
