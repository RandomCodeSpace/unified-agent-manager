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

## Real Copilot CLI against a mock model

```sh
npm --prefix web ci
make test-aimock
```

This runs the Copilot provider against the installed `copilot` CLI with the
model replaced by aimock (`@copilotkit/aimock`, pinned in `web/package.json`),
a local mock of the OpenAI API. Each test gets an empty `HOME` and no GitHub
tokens, so it needs no sign-in and spends no AI credits. Without
`UAM_AIMOCK_E2E=1` the tests skip; with it, a missing `copilot`, `node` or
aimock fails them. `UAM_AIMOCK_CLI` overrides the path of aimock's `cli.js`.

## Browser regression pack

`web/e2e` is a Playwright pack that drives the real app in Chromium: the built
`uam` serving its web interface, the real `copilot` CLI with no GitHub login,
and aimock as the model, through a custom model named `mock`. It spends no AI
credits, needs no secrets and uses only loopback. CI runs it as `test-ui` in
the merge queue and before a release; it is one of the jobs the required `ci`
check waits for.

**The rule:** a fix for a `uam web` bug lands with the spec that reproduces it,
in the same pull request. When a fix cannot be covered by a spec, the pull
request says why. `web/e2e/COVERAGE.md` lists what is covered and what is planned.

```sh
npx --prefix web playwright install chromium   # once
make test-ui                                    # builds bin/uam, runs the pack
make test-ui ARGS='e2e/activity.spec.ts -g Deny'  # one case
```

With a binary already built, `cd web && UAM_E2E_BIN=$PWD/../bin/uam npx
playwright test -c e2e` runs the pack without rebuilding; add `--workers=1`,
`--repeat-each=10` or `--headed` as needed. A missing or relative
`UAM_E2E_BIN`, or no `copilot` on `PATH`, fails at once. A failed case keeps
its trace and attaches `aimock-journal.json` (every model request the case's
scripts answered, in full) and the part of `uam.log` it wrote; open the report
with `npx --prefix web playwright show-report e2e/playwright-report`.

Each Playwright worker starts its own aimock (in process) and its own service
(`uam __web`, the process `uam web` starts, so the harness owns it) on free
loopback ports with a fresh `HOME`, config, session and cache directory, signs
in and registers the mock model. Each test gets a fresh Project directory (a
git repository with one commit of `README.md`), so cases are independent and
run in parallel and in any order. There are no retries: a flaky case fails.

### Writing a case

Specs are `web/e2e/*.spec.ts`. Import `test`, `expect` and the helpers from
`./harness`; the fixtures are `page` (signed in), `project` (a registered
Project; `workdir` is its directory), `model` (the script) and `uam` (the
service: `url`, `token`, `mock`, `api`). Locate by role, accessible name, label
and text: the UI has no test IDs, and the pack adds none. No fixed waits: use
Playwright's waiting assertions.

```ts
test('the agent says hello', async ({ page, project, model }) => {
  model.reply('say hello', 'Hello from the mock.');
  await startThread(page, project, 'say hello');
  await expect(conversation(page).getByText('Hello from the mock.')).toBeVisible();
});
```

- `model.reply(prompt, text, { latency, chunkSize })` answers a prompt
  containing `prompt`; `latency` (ms per chunk) and `chunkSize` (characters)
  make it stream slowly.
- `model.toolRound(prompt, calls, final)` answers with tool calls, then the
  result of the last call with `final`. It registers the result's script first:
  the prompt is still the last user message when the results come back.
- `model.fail(prompt, status, message)` answers with an upstream error. The CLI
  retries five times: a 429 fails the turn in 5 to 9 s, a 500 in 16 to 20 s.
- `model.title(prompt, title)` answers uam's background title request.
- `model.on(match, response, opts)` takes any aimock match and response.
- `model.served()` and `toolResult(model, callId)` return what the model received.

A script answers only the main agent of a Thread in the test's own directory,
so it cannot answer another test or a background call. Everything else gets
the catch-all reply `aimock background reply` (titles, suggested replies, the
outcome line): never assert it. A main-agent call that no script answered
fails the test, naming its prompt.

To learn a CLI tool's argument shape, read the `tools` of a request in the
attached journal (or `model.served()[0].request.tools` in a draft case). The
CLI's tools include `bash {command, description}`, `create {path, file_text}`,
`edit {path, old_str, new_str}` and `ask_user {question, choices}`; uam's host
tools (`uam_form`, `uam_surface`, ...) are declared in `internal/web`.

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
