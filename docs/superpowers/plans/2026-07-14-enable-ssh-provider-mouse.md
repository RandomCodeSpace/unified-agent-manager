# Enable SSH Provider Mouse Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Preserve provider mouse reporting by default over SSH so OpenCode and OMP wheel/touch scrolling works through UAM.

**Architecture:** Keep the existing attach output filter and explicit `UAM_ATTACH_MOUSE` policy boundary. Change only policy resolution so every value except `off` preserves provider-requested mouse modes, then document keyboard paste and explicit `off` as the compatibility fallback.

**Tech Stack:** Go 1.25.12, Unix PTYs, DEC private terminal modes, Go `testing`, Markdown documentation.

## Global Constraints

- Provider mouse reporting is enabled by default for local and SSH viewers.
- `UAM_ATTACH_MOUSE=off` retains terminal-owned mouse gestures.
- UAM preserves mouse modes only when the provider requests them.
- Do not change the attach protocol, store schema, provider arguments, session metadata, input framing, alternate-screen ownership, or detach cleanup.
- Linux, Ubuntu, and macOS behavior must remain supported.
- Use `rtk` for every shell command as required by `/opt/codex/RTK.md`.

---

### Task 1: Make SSH Mouse Reporting the Default

**Files:**
- Modify: `internal/session/attach_filter_test.go:27-79`
- Modify: `internal/session/attach.go:27-44`

**Interfaces:**
- Consumes: `attachMouseEnabled(getenv func(string) string) bool` and `newAttachOutputFilter(dst io.Writer, mouse bool) *attachOutputFilter`.
- Produces: unchanged `attachMouseEnabled` signature with the new rule that only explicit `off` returns false.

- [ ] **Step 1: Write the failing policy and OpenCode regression tests**

Change the SSH expectations in `TestAttachMousePolicy` and add an integration-level policy/filter test:

```go
func TestAttachMousePolicy(t *testing.T) {
	tests := []struct {
		name, value, sshConnection, sshTTY string
		want                               bool
	}{
		{"unset local", "", "", "", true}, {"auto local", "auto", "", "", true},
		{"unset ssh connection", "", "client", "", true}, {"auto ssh tty", "auto", "", "/dev/pts/1", true},
		{"on ssh", "on", "client", "", true}, {"off local", "off", "", "", false},
		{"off ssh", "off", "client", "/dev/pts/1", false},
		{"invalid local", "maybe", "", "", true}, {"invalid ssh", "maybe", "client", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := map[string]string{AttachMouseEnv: tt.value, "SSH_CONNECTION": tt.sshConnection, "SSH_TTY": tt.sshTTY}
			if got := attachMouseEnabled(func(key string) string { return env[key] }); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAttachDefaultSSHPreservesOpenCodeMouseModes(t *testing.T) {
	env := map[string]string{
		AttachMouseEnv:  "",
		"SSH_CONNECTION": "client 123 server 22",
		"SSH_TTY":        "/dev/pts/1",
	}
	mouse := attachMouseEnabled(func(key string) string { return env[key] })
	input := []byte("\x1b[?1000;1002;1003;1006h")
	if got := filterHostOutput(t, mouse, input); !bytes.Equal(got, input) {
		t.Fatalf("default SSH attach changed OpenCode mouse modes: got %q, want %q", got, input)
	}
}
```

- [ ] **Step 2: Run the focused tests and verify RED**

Run:

```bash
rtk proxy env GOTOOLCHAIN=go1.25.12 go test ./internal/session -run 'Test(AttachMousePolicy|AttachDefaultSSHPreservesOpenCodeMouseModes)' -count=1 -v
```

Expected: FAIL for the SSH `auto`/unset/invalid cases and for the OpenCode grouped mouse-mode assertion because current SSH auto policy returns false.

- [ ] **Step 3: Implement the minimal policy change**

Replace `attachMouseEnabled` and its comment in `internal/session/attach.go` with:

```go
// attachMouseEnabled resolves the per-viewer mouse policy. Providers keep mouse
// support locally and over SSH by default so wheel and touch scrolling work.
// Explicit off leaves mouse gestures under terminal control for selection/paste.
func attachMouseEnabled(getenv func(string) string) bool {
	return getenv(AttachMouseEnv) != "off"
}
```

- [ ] **Step 4: Run focused and session-package tests and verify GREEN**

Run:

```bash
rtk proxy env GOTOOLCHAIN=go1.25.12 go test ./internal/session -run 'Test(AttachMousePolicy|AttachDefaultSSHPreservesOpenCodeMouseModes|AttachOutputFilterOwnedModes|MouseEventsDoNotDisarm|AttachOwnsTerminalStateOnTTY|ReattachReplaysAgentPrivateModes|AttachMouseOffFiltersReplayedProviderModes)' -count=1 -v
rtk proxy env GOTOOLCHAIN=go1.25.12 go test ./internal/session -count=1
```

Expected: PASS. The default SSH test preserves the complete OpenCode sequence, explicit `off` still strips mouse modes, stdin wheel bytes remain unchanged, and detach ownership tests remain green.

- [ ] **Step 5: Format and commit the behavioral change**

Run:

```bash
rtk proxy gofmt -w internal/session/attach.go internal/session/attach_filter_test.go
rtk proxy git diff --check
rtk proxy git add internal/session/attach.go internal/session/attach_filter_test.go
rtk proxy git commit -m "fix(attach): enable provider mouse over SSH"
```

Expected: commit contains only the policy and regression-test changes.

---

### Task 2: Align Documentation and Run the Full Quality Gate

**Files:**
- Modify: `README.md:157-173`
- Modify: `docs/responsive-tui.md:121-132`
- Modify: `docs/responsive-tui.md:152-159`
- Modify: `docs/adr/0002-terminal-ownership-over-ssh.md:1-115`

**Interfaces:**
- Consumes: the `UAM_ATTACH_MOUSE` behavior implemented in Task 1.
- Produces: one consistent user contract for `auto`, `on`, `off`, invalid values, SSH scrolling, and keyboard paste fallback.

- [ ] **Step 1: Update the README contract and PowerShell guidance**

Replace the mouse policy list in `README.md` with:

```markdown
`UAM_ATTACH_MOUSE` controls whether provider mouse reporting is preserved:

- `auto` (the default) preserves provider mouse reporting locally and over SSH
- `on` preserves provider mouse reporting everywhere
- `off` suppresses provider mouse modes so the terminal keeps selection and
  paste gestures
```

Replace the PowerShell guidance with:

```markdown
For PowerShell SSH, use Windows Terminal and configure a keyboard paste binding
such as `Ctrl+V`, `Ctrl+Shift+V`, or `Shift+Insert`. Provider mouse reporting is
enabled by default so OpenCode and other mouse-aware providers can scroll. Set
`UAM_ATTACH_MOUSE=off` on the remote host when terminal-owned selection or
right-click paste is more important. Native Windows remains unsupported;
Windows is the SSH client in this setup.
```

- [ ] **Step 2: Update responsive TUI operations guidance**

Replace the opening paragraph under `## SSH, mouse, and paste` with:

```markdown
Mouse reporting defaults on for local and SSH attachments so wheel and touch
gestures reach mouse-aware providers such as OpenCode and OMP. Override it with
`UAM_ATTACH_MOUSE=on|off|auto`. Set it to `off` when terminal-owned selection or
right-click paste is more important than provider scrolling.
```

Replace troubleshooting steps 2-4 with:

```markdown
2. Confirm Windows Terminal has a keyboard paste binding such as `Ctrl+V`,
   `Ctrl+Shift+V`, or `Shift+Insert`.
3. Keep the remote setting at `UAM_ATTACH_MOUSE=auto` for provider scrolling.
4. If terminal-owned selection or right-click paste is preferred, set the
   remote setting to `UAM_ATTACH_MOUSE=off` and reattach.
```

- [ ] **Step 3: Amend ADR 0002 to record the changed priority**

Add this metadata after the original date:

```markdown
- Amended: 2026-07-14 — provider scrolling now takes priority over terminal-owned mouse gestures by default.
```

Change the policy table so `auto` or unset preserves mouse reporting locally and over SSH. State that invalid values use this default; explicit `off` retains the former SSH behavior. Replace the rejected `Always enable provider mouse reporting` alternative with:

```markdown
### Disable provider mouse reporting automatically over SSH

Rejected because it prevents wheel and touch scrolling in providers such as
OpenCode and OMP. Users who prioritize terminal-owned selection or paste can
still opt out with `UAM_ATTACH_MOUSE=off`.
```

Update the consequence to state that SSH defaults favor provider mouse interaction and that terminal selection/paste may require keyboard bindings or explicit `off`.

- [ ] **Step 4: Verify documentation consistency**

Run:

```bash
rtk rg -n 'disables it when|defaults on for local attachment and off|SSH defaults favor terminal selection|keep the remote setting at `UAM_ATTACH_MOUSE=auto`, or set it to `off`' README.md docs
rtk rg -n 'UAM_ATTACH_MOUSE|provider mouse|OpenCode|OMP' README.md docs/responsive-tui.md docs/adr/0002-terminal-ownership-over-ssh.md
rtk proxy git diff --check
```

Expected: the stale-policy search returns no matches; the second search shows the same default-on/explicit-off contract in all three documents; `git diff --check` exits successfully.

- [ ] **Step 5: Run the complete quality gate**

Run:

```bash
rtk proxy env GOTOOLCHAIN=go1.25.12 go test -race -count=1 -covermode=atomic -coverpkg=./... ./...
rtk proxy env GOTOOLCHAIN=go1.25.12 go vet ./...
rtk proxy env GOTOOLCHAIN=go1.25.12 golangci-lint run ./...
rtk proxy env GOTOOLCHAIN=go1.25.12 staticcheck ./...
rtk proxy go mod verify
rtk proxy git diff --check
```

Expected: all tests and analyzers exit zero, module verification prints `all modules verified`, and the diff check is clean.

- [ ] **Step 6: Commit documentation after verification**

Run:

```bash
rtk proxy git add README.md docs/responsive-tui.md docs/adr/0002-terminal-ownership-over-ssh.md
rtk proxy git commit -m "docs: prioritize provider scrolling over SSH"
```

Expected: commit contains only the README, responsive TUI guide, and ADR changes.
