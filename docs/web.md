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

The beta build is pinned to Go 1.25.14. Browser and provider validation used
Copilot CLI 1.0.88 and Go 1.26.5 on Linux 6.8.

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
`--listen 127.0.0.1:<port>` for another port. To let other machines connect
directly, see [Listen beyond loopback](#listen-beyond-loopback).

| Command | Effect |
|---|---|
| `uam web` | Start the service, or show how to reach the running one |
| `uam web status [--json]` | Show whether it is running, its PID, address, and version (not the token) |
| `uam web stop` | Stop the service and the provider processes it started |
| `uam web token set` | Replace the access token with one read from stdin (see [Sign in](#sign-in)) |

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

To choose the token yourself, pipe it in or type it at the prompt, which does
not echo it:

```sh
uam web token set < my-token-file
```

It reads stdin up to the first newline and trims surrounding whitespace. The
token must be 24 to 256 printable ASCII characters with no whitespace;
anything else is refused and the file is left alone. The command never
prints the token. There is no argument, flag or environment variable for it:
arguments show up in `ps` and shell history, and the service's environment is
inherited by the agents it starts. The file is replaced atomically (mode
0600). It refuses while the service runs, so the service never holds a token
the file no longer has: run `uam web stop` first, then `uam web` again.
Session cookies are derived from the token, so the restart signs out every
browser.

Every API request needs the cookie. The service also rejects requests whose
`Host` is not loopback, a configured public origin or, when it listens beyond
loopback, an IP address; it rejects cross-origin state changes and sends no
CORS headers.

`uam web --no-auth` turns sign-in off: every request is treated as signed in,
and `uam web` and `uam web status` print `Authentication: disabled` instead of
the token. The `Host`, cross-origin, and JSON checks still apply. It is off by
default. Behind a public reverse proxy, it lets anyone on the internet run
agents and shell commands on this host with your credentials; on an address
beyond loopback, anyone who can reach that address. To turn it back off, run
`uam web stop`, then `uam web` without the flag.

## Use it

- **Projects**: add a directory on the Linux host as a Project. A directory
  has one Project; adding it again points you to the existing one. Renaming a
  Project changes only its name in UAM. Web sessions from before Projects
  existed are placed in a Project for their directory, named after it, when
  the service starts.
- **Tasks**: start a Task (a web session) in a Project. Choose the provider
  (only Copilot for now), optionally a model, effort, context size, a name,
  and a first prompt. A provider that is not installed or not compatible is
  shown as unavailable with the reason. The Task runs in the Project's directory.
- **Names and titles**: the name is optional. Without one, the Task shows the
  title the provider gives the conversation (Copilot uses the first prompt),
  or "New task" until there is one. Clearing a name shows the title again.
  Renaming a Task does not rename the conversation at the provider.
- **Models**: the model list shows the models your Copilot account can
  select; the default is the provider's own choice. You can switch the model
  between turns, not while a turn runs; the new model applies from the next
  turn. The list is refreshed at most every five minutes, so a changed
  subscription shows up without restarting the service.
- **Effort**: choose Default or one of the selected model's reported levels.
  Default leaves the choice to Copilot; it does not mean a known level such
  as medium. Effort requires an explicit model with listed levels, so it is
  unavailable for `auto` or a model without them. Change it between turns.
  Switching models keeps a supported effort and otherwise resets it to
  Default.
- **Context size**: choose a size offered by the model, where available.
  The sizes are Copilot's prompt budgets. Long context may cost more. A
  change applies between turns; switching to a model without the selected
  size resets it to Default. Per-Task context size is the sole exception to
  the shared provider feature rules and requires the provider's capability.
  Copilot is still the only registered provider.
- **Context meter**: the Task header shows used tokens and the active prompt
  budget once Copilot reports them. Missing usage stays hidden, not zero.
  The meter is live only: reopening a conversation, restarting the service
  or changing its selection clears it until a fresh report. Compaction or
  truncation appears as a notice in the conversation; the next usage report
  updates the meter.
- **Conversation**: your messages sit on the right, the agent's on the left,
  both rendered as markdown (never as HTML) while they stream. The agent's
  reasoning, when the provider reports it, is a collapsed "Thinking…" row
  with a live one-line preview while it streams and "Thought for 12s" once
  done; expanding it shows the full text, and the choice is kept per block
  for the browser session. Runs of tool calls fold into one line that
  expands in place. If you scroll up while text arrives, the view stays put
  and offers "New output".
- **Subagents**: when the agent delegates work to a subagent, the Task shows
  one compact row under the tool call that started it (name, status, and
  the duration once it ended) and a "Subagents" button in the header with
  the total and how many are running. The button opens a panel beside the
  conversation that lists the subagents grouped by status (running, idle,
  failed, completed, cancelled) with their start time and duration; "Spawned
  by" jumps to the tool call in the conversation. Opening a row, or "Open" on
  its row in the conversation, shows that subagent's own prompt, replies,
  and tool calls in the panel, live while it runs. Subagent output never
  appears in the Task's own conversation. Its model and effort are shown
  when Copilot reports them; they are not guessed from the parent Task.
  A subagent is "Idle" when it has finished and Copilot still accepts a
  follow-up for it. While the Task is between turns, the panel then offers a
  composer that sends a message to that subagent only. The main agent does
  not see that conversation, and a follow-up whose delivery is uncertain is
  never resent.
- **Approvals and questions**: when the provider asks for permission or asks a
  question, a card appears in the conversation and a "Needs you" mark on the
  Task in the project list and on the home screen. Nothing is approved
  automatically unless you turned on yolo for that Task, and questions always
  wait for you. If no browser is connected, the request waits; the first
  answer from any tab wins and later answers are refused.
- **Yolo**: a Task in yolo mode does not ask for permission. As each
  permission request arrives, UAM allows it once, the same as clicking "Allow
  once". That includes requests from subagents. Shell commands, file writes,
  reads outside the project directory, and web fetches then run without a
  prompt. Questions from the agent still wait for you, and so does any request
  your organization's Copilot policy says a person must approve. Tasks start
  in safe mode. You can pick yolo when you create a Task or switch it at any
  time, even during a turn. Switching to yolo also allows the request the Task
  is waiting on; switching back to safe makes the next request ask again. Every
  request yolo allowed stays in the conversation, marked "allowed (yolo)".

  > **Warning:** in yolo mode the agent can run any command and change any
  > file your Linux account can reach, with your credentials, and nobody is
  > asked first. Turn it on only for a project where you would click "Allow"
  > on everything anyway. With `--no-auth` behind a public reverse proxy or
  > on an address beyond loopback, anyone who can reach the page can start a
  > yolo Task.
- **Messages while a turn runs**: you can queue a message or steer the turn
  with it.
  - **Queue** holds the message until the turn completes, then sends it as
    the next prompt. A Task queues up to 20 messages and sends them one turn
    at a time, oldest first. You can cancel a queued message until it is
    sent; to change one, cancel it and queue it again.
  - **Steer** adds the message to the turn that is running. The agent reads
    it before its next step, and it shows in the conversation, marked as a
    steer, at the point where the agent took it in. A steer cannot be taken
    back. If the turn is stopped or fails before the agent took it in, a
    notice says the steer was not delivered and quotes it. With Copilot, a steer also moves a
    shell command that is running to the background.
  - When no turn is running, both send the message at once.
- **Paused queue**: the queue pauses when a turn is stopped or fails, when
  the provider process ends, when you close the session, and when the
  provider refuses a message or may not have received it. A paused queue
  sends nothing until you act on it. Resume sends the next message, at once
  if no turn is running. Clear drops every queued message.
- **Stop turn** cancels the running turn. The conversation stays open. A
  queue pauses instead of moving on, and steers the agent had not taken in
  yet are dropped, each with a notice.
- **Close session** disconnects UAM from the provider conversation and keeps
  the record. Sending another prompt reopens the same conversation.
- **Settle, archive, and delete**: when a Task is done, settle it. A settled
  Task is read-only and its conversation is closed; reopen it to continue,
  and your next message picks up the same conversation. Archive a Task, settled
  or not, to put it away for good; an archived Task cannot be reopened. Only
  an archived Task can be deleted, and a Project can be removed only once
  every Task in it is archived. Deleting or removing never deletes the
  provider's copy of the conversation and never touches the directory.

  | Action | Allowed on | Also needs | Result |
  |---|---|---|---|
  | Settle | an active Task | no turn running, nothing waiting for you, an empty queue | Read-only; the conversation is closed |
  | Reopen | a settled Task | – | Active again; the next message reopens the same conversation |
  | Archive | an active or settled Task | for an active Task, the same as Settle | Read-only for good; there is no unarchive |
  | Delete Task | an archived Task | – | The Task is removed from UAM |
  | Remove Project | any Project | every Task in it archived, or no Tasks | The Project and its archived Tasks are removed from UAM |

  A settled or archived Task takes no messages, queue actions, model, effort,
  context-size or mode changes. You can still rename a settled Task. Stop a
  running turn, answer what waits for you, and send or clear the queue before
  you settle or archive. After the service restarts, a settled or archived Task shows no
  conversation, the same as a closed one.
- **Changes**: the Task's meta line shows how many files differ from `HEAD`
  in the project directory and opens them as a sheet with the diff. That
  includes edits made by anything else in the working tree, not only this
  session.
- **Home screen**: with no Task open, the page lists Tasks that need you or
  have new activity since you last looked, then every Project with its
  Tasks. On a narrow window the project list opens as a drawer. The theme
  follows the system and can be toggled from the project list.

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
and marks those sessions interrupted. Queued messages are dropped without
being sent; queues are kept in memory only. Session records and the exact
provider conversation IDs are kept. After `uam web` starts again, sending a prompt to a
session reopens the same provider conversation. The Task's selected model,
effort and context size are restored before the prompt is sent. If Copilot
cannot apply them, UAM reports the failure without sending the prompt.

## Listen beyond loopback

By default the service listens on `127.0.0.1` only. `--listen` takes any IP
address: `0.0.0.0` for every IPv4 interface, `::` for every IPv6 interface, or
one interface's address such as `192.168.1.20`. Host names other than
`localhost` are refused.

```sh
uam web --listen 0.0.0.0:8260
```

```text
uam web started (pid 258199)
  URL:           http://127.0.0.1:8260/
  Listen:        0.0.0.0:8260
  Access token:  <64 hex characters>
  Warning: listening on 0.0.0.0:8260, so other machines can reach this service; sign-in is required (--no-auth is not in use)
```

Other machines that can reach the port open `http://<host IP>:8260/` and sign
in with the access token. The URL printed for this host uses loopback on the
same port (`0.0.0.0` becomes `127.0.0.1`, `::` becomes `::1`); `uam web
status` shows the same URL, the listen address and the warning.

The connection is plain HTTP, so the token and the session cookie cross the
network unencrypted. Use it only on a network you trust; otherwise use SSH
forwarding or an HTTPS reverse proxy.

The `Host` check still blocks DNS rebinding. Beyond loopback the service also
accepts a `Host` that is an IP address, such as the LAN address. Any domain
name that is not a configured public origin gets 403, so a website whose name
resolves to this host cannot use the service. To reach it by name, add that
name with `--public-origin`. The cross-origin and JSON checks do not change.

With `--no-auth` as well, nothing stands between the network and your agents:

```text
  Authentication: disabled — anyone who can reach this service can use it
  Warning: listening on 0.0.0.0:8260, so other machines can reach this service
  Warning: --no-auth is in use: anyone who can reach 0.0.0.0:8260 can run agents on this host with your credentials
```

Anyone who can reach the port can then start Tasks in your directories with
your Copilot credentials, including yolo Tasks that run shell commands
without asking.

## Log request headers

For debugging, `uam web --log-headers` logs one line per HTTP request: the
method, the path with its query, the remote address, the `Host`, every request
header, and whether the request was allowed or refused (`host not allowed`,
`cross-origin request rejected`, the JSON content-type refusal, or
`authentication required`, with the status code). It is off by default, and
`uam web status` shows `Header logging: on` while it is on. Headers that can
carry a credential are logged as `[redacted]`: `Cookie`, `Authorization`,
`Proxy-Authorization`, and any whose name contains `auth`, `cookie`, `token`,
`key`, `secret`, `session`, `jwt`, `signature`, `password` or `credential`.
Other values are cut at 512 bytes, and bodies are never logged.
Other headers are logged as sent, so turn it off again (`uam web stop`, then
`uam web` without the flag) once you have what you need.

The lines go to the uam log, `~/.cache/uam/uam.log` (or
`$UAM_CACHE_DIR/uam.log`, or `$XDG_CACHE_HOME/uam/uam.log`), mode 0600,
rotated at 5 MiB with three backups. Each line is a JSON record:

```sh
grep '"msg":"web request"' ~/.cache/uam/uam.log
```

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
- **Late steers (Copilot).** A steer that arrives while Copilot writes the
  last reply of a turn is answered right after that reply, in the same turn,
  and shows as an ordinary message without the steer mark. One that arrives
  just after the turn ended starts a new turn.
- **Selection changes (Copilot).** UAM does not force compaction to make a
  smaller context size fit. If Copilot requires consent or cancels a switch,
  the change fails and the Task keeps its recorded selection. Returning
  effort to Default uses an experimental Copilot SDK operation, separate
  from the model switch. If that reset fails after the switch, or Copilot
  cannot confirm which settings applied, UAM marks the Task failed and closes
  the conversation. The provider may have partly changed; UAM does not claim
  the selection succeeded or try to roll it back. The next explicit send
  must first restore the recorded settings successfully.
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
