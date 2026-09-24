# Web interface

`uam web` runs a small background service on the Linux host that lets a
browser start and follow GitHub Copilot conversations. The work
runs on Linux. Closing the browser, VS Code, or the SSH connection does not
stop it; when you reconnect, the page shows the current progress or the
finished result.

The web interface talks to Copilot through its structured API (the Copilot
SDK). It is not a terminal in a browser, and it does not use the terminal
sessions that `uam` and `uam attach` manage.
See [ADR 0004](adr/0004-web-interface.md) for the design.

## Requirements

On the Linux host:

- `uam` installed from a release or `make install`. The web assets are built
  into the binary; Node.js is not needed to serve them.
- `copilot` (GitHub Copilot CLI, which needs `node` on `PATH`), installed and
  signed in exactly as for the terminal. UAM reuses its existing
  configuration, credentials, model settings, and permission rules. It does
  not install, update, or reconfigure it.
- Start `uam web` from a normal login shell, where `copilot` resolves on
  `PATH`. The service inherits that environment.

On Windows: the built-in OpenSSH client (PowerShell) and a browser. Nothing is
installed on Windows.

Tested with Copilot CLI 1.0.88 and Go 1.26.5 on Linux 6.8.

## Start the service

```sh
uam web
```

```text
uam web started (pid 258199)
  URL:           http://127.0.0.1:8260/
  Listen:        127.0.0.1:8260
  Access token:  <64 hex characters>

From another computer, forward the port over SSH (for example in PowerShell):
  ssh -N -L 127.0.0.1:8260:127.0.0.1:8260 <user>@<host>
then open http://127.0.0.1:8260/ and sign in with the access token.
```

The service detaches from the shell: it has its own session, no controlling
terminal, and `/dev/null` for its standard streams. Running `uam web` again
while it is running prints the same details, including the token. Use
`--listen 127.0.0.1:<port>` for another port. Only loopback addresses are
accepted.

| Command | Effect |
|---|---|
| `uam web` | Start the service, or show how to reach the running one |
| `uam web status [--json]` | Show whether it is running, its PID, address, and version (not the token) |
| `uam web stop` | Stop the service and the provider processes it started |

## Connect from Windows

In PowerShell:

```powershell
ssh -N -L 127.0.0.1:8260:127.0.0.1:8260 you@linux-host
```

Leave that window open and browse to `http://127.0.0.1:8260/`. The local
side is bound to `127.0.0.1`, so other machines on your network cannot use the
tunnel. If local port 8260 is taken, change only the first number, for example
`-L 127.0.0.1:18260:127.0.0.1:8260`, and browse to port 18260.

## Sign in

Enter the access token printed by `uam web`. The browser keeps an HttpOnly
session cookie for 30 days; the token itself is not stored in the browser.

The token lives in `~/.config/uam/web-token` (mode 0600, or
`$UAM_CONFIG_DIR/web-token`). To revoke every browser session, stop the
service, delete that file, and run `uam web` again; it creates a new token.

Every API request needs the cookie. The service also rejects requests whose
`Host` is not loopback (or a configured public origin), rejects cross-origin
state changes, and sends no CORS headers.

`uam web --no-auth` turns sign-in off: every request is treated as signed in,
and `uam web` and `uam web status` print `Authentication: disabled` instead of
the token. The `Host`, cross-origin, and JSON checks still apply. It is off by
default. Behind a public reverse proxy, it lets anyone on the internet run
agents and shell commands on this host with your credentials. To turn it back
off, run `uam web stop`, then `uam web` without the flag.

## Use it

- **New session**: choose the provider (only Copilot for now), a project
  directory on the Linux host, a name, and optionally a first prompt. A
  provider that is not installed or not compatible is shown as unavailable
  with the reason.
- **Conversation**: responses stream in; tool calls appear as expandable rows
  that update in place.
- **Approvals and questions**: when the provider asks for permission or asks a
  question, a card appears in the conversation and a badge in the session
  list. Nothing is approved automatically. If no browser is connected, the
  request waits; the first answer from any tab wins and later answers are
  refused.
- **Stop turn** cancels the running turn. The conversation stays open.
- **Close session** disconnects UAM from the provider conversation and keeps
  the record. Sending another prompt reopens the same conversation.
- **Changes**: *Workspace* shows `git` changes in the project versus `HEAD`.
  That includes edits made by anything else in the working tree, not only
  this session.

States shown for each session:

| State | Meaning |
|---|---|
| working | The provider is running a turn |
| awaiting permission / awaiting answer | The provider is waiting for you |
| completed | The last turn finished |
| cancelled | The last turn was stopped with Stop turn |
| failed | The provider reported an error, or its process or event stream ended; the detail says which |
| interrupted | UAM stopped while a turn was running; the turn was not resumed or resent |
| closed | The conversation was closed from the web interface |

A prompt is sent at most once per click. Retrying after a network error reuses
the same request ID, and the service answers a repeated request ID with the
recorded result instead of sending again. If the provider may or may not have
received a prompt, the page says so and nothing is resent.

## Disconnect and reconnect

Close the browser, VS Code, or the SSH window whenever you like. The turn
keeps running on Linux, including when no browser is connected. To come back,
run the same `ssh -N -L …` command and open the URL; the page loads the
current state and continues streaming. Reconnecting never sends a prompt.

## Stop

```sh
uam web stop
```

This ends running turns, stops the provider processes the service started,
and marks those sessions interrupted. Session records and the exact provider
conversation IDs are kept. After `uam web` starts again, sending a prompt to a
session reopens the same provider conversation.

## Same-host HTTPS reverse proxy

The service can sit behind a reverse proxy on the same host. Tell it the
public origin so the `Host` check accepts it:

```sh
uam web --public-origin https://uam.example.com
```

Caddy example:

```caddyfile
uam.example.com {
	reverse_proxy 127.0.0.1:8260 {
		flush_interval -1
		transport http {
			read_timeout 24h
		}
	}
}
```

`flush_interval -1` keeps the event stream unbuffered. The proxy must pass the
original `Host` header (Caddy does by default). This makes the service
reachable from the internet, protected by the access token; keep the token
private and rotate it if it leaks. With `--no-auth` it is not protected at all
(see [Sign in](#sign-in)).

## Limitations

- **Logout policy.** The service survives the end of an SSH session only when
  the host does not kill user processes at logout. systemd-logind does that
  when `KillUserProcesses=yes` (the compiled-in default on some
  distributions). Check the effective setting, including drop-ins, with
  `systemd-analyze cat-config systemd/logind.conf | grep KillUserProcesses`;
  a commented line means the distribution default applies. UAM does not
  change that policy.
- **Reboot.** A reboot ends the service and every running turn. Records keep
  the exact conversation IDs, so conversations can be reopened afterwards, but
  the interrupted turn is not continued. UAM does not install a boot service.
- **Separate from terminal sessions.** The dashboard, `uam ls`, and
  `uam attach` do not show web sessions, and the web interface does not show
  terminal sessions. `uam attach <id>` on a web session explains this.
  Neither side takes over the other's conversations.
- **Copilot sessions without a prompt.** Copilot saves a conversation only
  after its first message. A session created without a prompt cannot be
  reopened after the service restarts.
- **Copilot session diffs.** Copilot does not report per-conversation file
  changes; use the Workspace view.
- **Opening a web conversation elsewhere at the same time.** Do not
  continue a web session's conversation in Copilot's own terminal UI while
  the web interface has it open.
- **Commands started by a provider that crashes.** Copilot runs shell
  commands in separate process sessions. If the provider process is
  killed abruptly, a command it had already started may keep running until it
  finishes; UAM does not track or stop it.
- **Older uam binaries** do not know about web sessions and show them as
  ordinary stopped sessions.
