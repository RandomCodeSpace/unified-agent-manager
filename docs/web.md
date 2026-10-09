# Web interface

`uam web` runs a small background service on the Linux host that lets a
browser start and follow GitHub Copilot conversations. The work
runs on Linux. Closing the browser, VS Code, or the SSH connection does not
stop it; when you reconnect, the page shows the current progress or the
finished result.

The web interface talks to Copilot through its structured API (the Copilot
SDK). Copilot is the only supported integration. The former UAM TUI and
terminal session harnesses have been removed; `uam` now prints help.
See [ADR 0004](adr/0004-web-interface.md) for the design.

## Requirements

On the Linux host:

- `uam` installed from a release or `make install`. The web assets are built
  into the binary; Node.js is not needed to serve them.
- `copilot` (GitHub Copilot CLI, which needs `node` on `PATH`), installed and
  signed in exactly as for the terminal, or signed in later from Settings (see
  [GitHub Copilot sign-in](#github-copilot-sign-in)). UAM reuses its existing
  configuration, credentials, model settings, and permission rules. It does
  not install or reconfigure it. It updates it only when asked in Settings,
  the way it was installed, and only to a release the Copilot SDK built into
  UAM accepts (see **Copilot CLI updates** under
  [GitHub Copilot sign-in](#github-copilot-sign-in)).
- Start `uam web` from a normal login shell, where `copilot` resolves on
  `PATH`. The service inherits that environment.

On Windows: the built-in OpenSSH client (PowerShell) and a browser. Nothing is
installed on Windows.

The release build is pinned to Go 1.26.6. Earlier browser and provider validation used
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
terminal, and `/dev/null` for its standard streams. Started from inside
another service of your systemd user manager (an editor or agent server, for
example), it also moves into its own transient scope, `uam-web-….scope`, so
stopping or restarting that service leaves it running. Started from a login
shell, it stays in that session. Running `uam web` again
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

This instance's identity and its connected instances live in
`~/.config/uam/web-connections.json` (mode 0600, beside the token). Like the
token and the directory, uam makes the file private again when a copy,
restore or sync widened its mode. It refuses to start only when the file is
a symbolic link or belongs to another user, and names the file: replace the
link with the file itself, or run uam as the file's owner. The file belongs
to one machine; keep it out of dotfile sync.

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

Protected API requests need a session cookie. The signed file-view route instead
checks a task-scoped file key. Over HTTPS the key also needs a companion cookie
that the view route sets in the same browser (HttpOnly, limited to that Task's
file-key path), so a file view works only in the browser that opened it; over
plain HTTP, where browsers refuse such a cookie, the key alone is checked. Keys
and companion cookies last 12 hours. Cookies and file keys are bound to the `Host`
they were issued for, so the service accepts any `Host`: a website whose name
resolves to this host holds no credential for that name and reaches only the
sign-in page and static assets. The service rejects cross-origin state changes
and sends no CORS headers.

Authentication is always required. Both `uam web --no-auth` and the internal
`uam __web --no-auth` entry point reject the removed flag. Token load, creation
or validation failures stop startup before the service listens.

If an older daemon is still running without authentication, `uam web` refuses
to reuse it. `uam web status` identifies it as an unsupported legacy service;
`status --json` reports `no_auth: true` and `restart_required: true`. Run
`uam web stop`, then `uam web` to restart with sign-in required. This never
happens automatically, and the existing token is not replaced.

For adding a Project, the API also lists and creates folders on the host, as
the service user. `GET /api/fs/dirs?path=<absolute path>` returns the
subdirectories of a directory (your home when `path` is empty), never files:
`{"path", "parent"?, "entries": [{"name", "path", "git", "hidden", "link"}],
"truncated"}`, without dot-folders unless `hidden=1` is added, at most 1,000
entries after that filter; `parent` is absent at `/`. Folders whose names
could not be shown as they are (control characters, escape sequences) are
left out, since a Project cannot be added there either. `POST /api/fs/dirs`
with `{"parent", "name"}` creates one folder (mode 0755) and returns 201
`{"path"}`, or 409 when the name exists. A path that is not a folder, too
long or a symbolic-link loop is 400, one you cannot read or write is 403,
a missing one 404, and a full disk or quota 507. Both need sign-in like
other protected API routes. Created folders are logged with their path; listings
are logged only at debug level (`UAM_DEBUG=1`).

## GitHub Copilot sign-in

Copilot has one sign-in per server: the one the `copilot` CLI uses for the
service user. Every Task on the server, and the `copilot` command run as the
same user, uses it. Settings → **GitHub Copilot** shows it, read again each
time Settings opens or **Check again** is chosen:

- **Signed in as** the account's login, and how: a sign-in Copilot stored on
  the server, the token in a named environment variable (never its value),
  or the GitHub CLI (`gh`) sign-in.
- **Signed out**, with Copilot's own reason. While Copilot is signed out a
  banner over the main pane says "GitHub Copilot is signed out. Sign in in
  Settings." with **Open Settings**; creating a Task or sending to one is
  refused with that sentence (code `provider_signed_out`, HTTP 409) instead of
  the runtime's error, and a turn that fails because the sign-in was lost says
  so in place of that error. `GET /api/meta` lists the provider as
  `available: false` with `signed_out: true`.

**Sign in with GitHub.** Offered while Copilot is signed out and no
environment token is set. Choose **Sign in with GitHub**: Settings shows a
short code, with **Copy code**, and **Open GitHub**, which opens GitHub's
device page in a new tab. Enter the code there and approve Copilot CLI; no
token passes through UAM.
Settings checks every 2 seconds and shows the sign-in once GitHub approves it;
**Cancel** stops it. A code not approved within about 15 minutes expires and
the sign-in fails with **Try again**. A sign-in started in another tab or
before a reload shows its code again when Settings opens. It signs in the
whole server: every Task uses the account. To store the sign-in, the server
needs a system keychain, or Copilot's `storeTokenPlaintext` setting in
`~/.copilot/settings.json`. Signing in restarts Copilot, so it is refused while
Copilot Tasks have open conversations; close them first. While signed in,
**Use another token** replaces the sign-in.

**Or sign in with a token.** Paste a fine-grained personal access token with the
**Copilot Requests** permission (create one on GitHub under Settings →
Developer settings → Personal access tokens → Fine-grained tokens); classic
`ghp_` tokens are not accepted. The field is a password field. The token goes
straight to Copilot's `account.login`, which checks it with GitHub and stores
it the way `copilot login` does; UAM never logs, stores or returns it, and the
field is cleared whatever the outcome. A refused token shows GitHub's reason.
Replacing an existing sign-in asks for confirmation first. Copilot's models
load at once: no service restart is needed. If Copilot could not store the
sign-in, Settings says it lasts until the service restarts.

**Sign out** is offered only for a sign-in Copilot stored, after a
confirmation; it also signs the `copilot` command out for that user. A `gh`
sign-in is changed with `gh auth logout` on the server.

**Copilot CLI updates.** Settings → **GitHub Copilot** also shows the
installed Copilot CLI version and the newest stable release, read when the
service starts, every 6 hours, and when Settings asks again; the Settings icon
carries an amber dot while an update is available. The newest release is read
from `@github/copilot` on npm when npm installed the CLI, and from the
published GitHub releases otherwise. UAM updates a CLI that npm installed
globally, with an npm whose global folder the service user can write, and a
standalone `copilot` binary (the install script, a Homebrew cask, or one
placed by hand) in a folder the service user can write, on Linux and macOS;
otherwise Settings says why, and the CLI is updated the way it was installed.
An update first stages the release in a temporary folder, with npm or from the
release archive for this platform checked against the release's
`SHA256SUMS.txt`, and starts the Copilot SDK built into UAM on it. A release
the SDK refuses is not installed: Settings says it needs a newer UAM and does
not offer it again until the service restarts. Otherwise UAM runs
`npm install -g @github/copilot@<version>` or replaces the binary in place
while Copilot keeps running: Tasks working or waiting are not interrupted,
and new conversations keep using the running version. Only starting Copilot
waits while the files are replaced. As soon as no Copilot Task is working or
waiting, UAM closes the idle Copilot conversations (their Tasks keep their
state and reopen when next viewed or sent to) and stops Copilot, and the next
use starts the new version; until then Settings shows the version still
running.

**Environment tokens win.** A token in `COPILOT_GITHUB_TOKEN`, `GH_TOKEN` or
`GITHUB_TOKEN` (in that order) in the service's environment takes precedence
over any stored sign-in, so while one is set Settings names the variable and
turns signing in and out off. Change or remove it where the service starts,
then restart the service.

**One linked account.** A server uses one Copilot account. The first sign-in
links the server to its account; a server already signed in when it is
upgraded links to that account the first time it reads it. Settings shows
"Linked account: <login>" and, while signed out, says which GitHub account to
use. A sign-in as another account, by token or with GitHub, is refused ("This
server is linked to Copilot account <login>. Sign in with that account.") and
removed again. If Copilot is signed in as another account some other way (an
environment token, `copilot login` or `gh` on the server), Copilot is
unavailable and every Task, send and routine run that needs it is refused
until that is fixed: the app shows "Copilot is signed in to another account"
with **Open Settings** in place of every view but Settings, and lifts
it once the account is fixed. **Unlink** in Settings, after a confirmation,
signs out a sign-in Copilot stored and clears the link; the next sign-in links
its account. An environment token or a `gh` sign-in cannot be signed out here,
so its account is linked again at once.

**One account across connected instances.** Every instance connected to this
one must use the same Copilot account. Pairing sends this instance's linked
account and the target answers with its own: two instances linked to
different accounts never pair (the target answers 409 with code
`account_mismatch`, and adding the connection reports "<label> is linked to
Copilot account <login>; this instance is linked to <login>. Both must use the
same account."); when only one is linked, the other adopts its link, and is
then unavailable if it is signed in as another account; when neither is, or
the other instance predates this rule and sends no account, they pair as
before. Each read of `GET /api/connections` reads every enabled connection's
`/api/providers/copilot/account` and marks one linked to another account than
this instance, or signed in as another account than its link, with `"status":
"account_mismatch"` and a `"reason"`. Through such a connection this instance
refuses creating, prompting or resuming Tasks, routine runs, drafts and
the terminal with 409 `account_not_linked` and the reason (reading the
account again at most every 15 seconds); its reads, Settings and account
stay open so the account can be fixed there.

The API: `GET /api/providers/copilot/account` returns `{"signed_in", "login"?,
"host"?, "source"? ("stored", "env", "gh-cli" or "other"), "env_var"?,
"message"?, "linked"? {"login", "host", "linked_at"}}`, `linked` being the
account the server is linked to, absent until one is;
`POST /api/providers/copilot/account/sign-in` with `{"token"}`
and `POST /api/providers/copilot/account/sign-out` return the same shape, or
400 with the reason for a refused token, 409 while an environment token takes
precedence, and 409 with code `account_not_linked` for a token of another
account than the linked one. `DELETE /api/providers/copilot/account/link`
clears the link, signs out a stored sign-in and returns the account. While
Copilot is signed in as another account, `GET /api/meta` lists it
`available: false` with `account_mismatch: true` and the reason, and requests
that need it answer 409 with code `account_not_linked`. `POST /api/providers/copilot/account/device` starts a device
sign-in, or returns the one in progress; `GET` on the same path reads it and
`DELETE` cancels it (204). Both `POST` and `GET` return `{"state"
("idle", "starting", "waiting", "signed_in", "failed" or "canceled"),
"verification_uri"?, "user_code"?, "error"?, "account"?}`: `waiting` carries
the page and the code, `signed_in` the account, `failed` the reason (a
sign-in as another account than the linked one fails with the same text as the
token refusal). `POST`
answers 409 while an environment token takes precedence and 400 while Copilot
Tasks have open conversations. The provider's `capabilities.device_sign_in` in
`GET /api/meta` says whether it is offered. `GET /api/providers/copilot/cli`
returns `{"installed"?, "running"?, "latest"?, "update_available",
"manual"?, "incompatible"?, "checked_at"?, "check_error"?, "state" ("idle",
"updating", "updated" or "failed"), "target"?, "error"?}`, reading the
releases first when none were read yet or with `?refresh=1`; `installed` is
the version on disk, and `running`, while set, the older one Copilot still runs
until it restarts. `POST
/api/providers/copilot/cli/update` starts the update to `latest` and returns
the same shape in state `updating`, or the update in progress; it answers 409
when no update is available and when `manual` says the CLI must be updated
another way. While an update
is available, `GET /api/meta` lists its release as the provider's
`cli_update`; `capabilities.cli_update` says whether updates are offered. All
need sign-in like other protected API routes. Sign-ins and sign-outs are logged without the token.

## Use it

- **Projects**: add a directory on the Linux host as a Project. Type its
  path, or press **Browse** next to the field to pick it: the picker opens in
  the dialog at the folder in the field (your home when the field is empty
  or not a folder) and lists the folders there, with git repositories and
  symbolic links marked. Click a segment of the path at the top or **Up** to
  go back, double-click a folder or its arrow to open it, and turn on **Show
  hidden** for dot-folders. **New folder** adds a row where you type a name;
  Enter creates the folder and selects it. **Use this folder** puts the
  selected folder, or the one being shown, into the field. With the
  keyboard: arrows move, Enter opens, Backspace goes up, typing jumps to a
  name, and Ctrl+Enter (Cmd+Enter on a Mac) uses the folder. A directory
  has one Project; adding it again points you to the existing one. "Edit
  project" (the gear beside the Project in the sidebar's filter list) renames
  it and offers "Previous sessions" and "Remove project". Editing a Project
  changes only its record in UAM. Web sessions from before Projects existed are placed in
  a Project for their directory, named after it, when the service starts.
  Each Project has a badge of two characters on a colour: the first letter or
  digit of its name and the last one (`CG` for config), then a random remaining
  letter if that pair is taken (`P` for a name without
  ASCII letters or digits), different from every other Project's, on a colour
  no other Project uses while one is left. UAM picks it when you add the
  Project and keeps it when you rename it; Projects from before badges
  existed get one when the service starts.
- **Tasks**: "New task" (the pen in the sidebar, or Alt+N) opens a list of
  your Projects to pick from: type to search by name or directory, use the
  arrow keys and Enter, click one, or press Alt+1 to Alt+9 for the first
  nine. The Project the sidebar is filtered to starts highlighted, otherwise
  the one you worked in last; with a single Project the pen skips the list.
  Picking one opens a new, empty Task pane on the defaults for new tasks
  from Settings and puts the cursor in the composer. The Task
  itself, and its Copilot conversation, is created only when you send the
  first message: until then nothing appears in the sidebar, and leaving the
  pane (another Task, Settings, a reload) discards it, keeping what you typed
  for the next New task in that Project (attachments are not kept). The
  pickers in the composer's toolbar change the model, effort, context size
  and mode the Task starts with; `@` lists the Project's files, while `/`
  commands and `$` skills are available after the first message. If the Task
  is created but the message cannot be sent, the Task opens with the message
  back in its composer and the reason above it. Until the defaults are set
  in Settings, a Task starts with `auto`, default effort, default context
  size and safe mode. A default model the provider no longer offers is
  replaced by `auto` (or the first model), with default effort and context
  size, so New task does not fail on a stale default. A provider that is not installed or
  not compatible is reported with the reason. The Task runs in the Project's
  directory.
- **Names and titles**: the name is optional. Without one, the Task shows the
  title the provider gives the conversation (Copilot uses the first prompt),
  or "New task" until there is one. A model then titles the Task from its
  first message a few seconds later (see **Utility model** below); a first
  message of only images or files is titled from its images, or once the
  agent replied.
  Clearing a name shows the title again.
  Rename from the Task's row menu (hover "…" or right-click), the header
  menu, F2 on a row, or a double-click on the name; the name is edited in
  place. Renaming a Task does not rename the conversation at the provider.
- **Models**: the model list shows the models your Copilot account can
  select; the default is the provider's own choice. You can switch the model
  between turns, not while a turn runs; the new model applies from the next
  turn. The list is refreshed at most every five minutes, so a changed
  subscription shows up without restarting the service.
- **Model visibility**: Settings → Models hides models from the composer,
  the defaults for new tasks and title-model choices. Existing Tasks and defaults keep
  their current model and show "hidden in Settings". New models are visible
  automatically. Hiding is a display preference, not an access rule.
- **Composer layout**: the toolbar groups Model, Effort/Context and Safe/Yolo.
  The branch and changed-file count are on the branch button in the Task
  header, which opens Changes. When the toolbar
  is narrow (side panels open, or a phone), its labels give way in order:
  the execution word, the credits, effort and context, then permissions
  become glyphs and move into the More menu, and the model's name goes
  last. Hover a glyph for its value.
  The toolbar wraps on phones, and elsewhere only when even that does not
  fit. Type `$`
  at the start of a message to pick a skill; `/` still lists commands and
  skills, and `@` references files.
- **Copilot configuration**: Copilot Tasks load what the terminal CLI loads
  for the directory: your and the project's skills, the project's custom
  agents, custom instructions, hooks in `.github/hooks/`, and your MCP
  servers; the built-in GitHub MCP server only while Settings → MCP servers
  turns it on (see MCP servers). Hooks run their commands without asking,
  as they do in the terminal. Every Task also gets uam's built-in `uam`
  skill, which tells the agent what uam is and how its replies and files
  show here, and the built-in `uam-design` skill, which gives design
  defaults for web and UI work such as prototypes. `uam-design` is a fallback:
  it tells the agent to follow your own instructions and design skills and the
  project's design system (a `DESIGN.md`, tokens, its component library) first.
  uam writes both to `skills/<name>/SKILL.md` beside `sessions.json` at each
  start. uam also appends a system instruction asking
  the agent to put results in its reply, since tool calls are folded, to
  load the `uam` skill before it shows files, diagrams or charts, starts
  another Task or starts subagents, to give a question's options as separate
  choices, with the recommended option first and marked "(Recommended)", and
  to write commit
  messages and pull or merge requests without a `Co-authored-by` trailer or
  any AI attribution unless you ask for one. Copilot's own co-author trailer
  is turned off for Tasks.
  Settings → Skills and Agents also show Copilot's native discovery metadata:
  runtime names, sources, descriptions and global skill enablement. Hooks show
  each discovered action's event, origin and source, including actions disabled
  by Copilot's disabled-hooks setting; Instructions show discovered sources by
  label and location. Hook commands, instruction contents and descriptions are
  not included. Project discovery includes the runtime's global sources.
  Existing file edits and Disable/Enable still use the selected scope's exact
  file and revision; a globally disabled skill or hook is labelled separately.
  Each discovered skill also has a separately labelled Copilot global setting:
  Turn off globally / Turn on globally asks for confirmation, then adds or
  removes that one name in Copilot's disabled skills list. It applies to every
  skill with that name in every project, needs Terminal on like the file
  toggle, and never renames or edits a file. Plugin, built-in, remote and
  other definitions outside the file editor show read-only metadata. Discovery
  failures or limits are reported while managed files and disabled-file recovery
  remain available. Older runtimes without discovery keep the existing file view.
- **Effort**: choose Default or one of the selected model's reported levels.
  Default leaves the choice to Copilot; it does not mean a known level such
  as medium. Effort requires an explicit model with listed levels, so it is
  unavailable for `auto` or a model without them. While a turn runs, changes
  are saved with your draft and apply when its queued turn starts.
  Switching models keeps a supported effort and otherwise resets it to
  Default.
- **Context size**: choose a size offered by the model, where available.
  The sizes are Copilot's prompt budgets. Long context may cost more. A
  change applies between turns; switching to a model without the selected
  size resets it to Default. Per-Task context size is one of the exceptions
  to the shared provider feature rules (usage and credits and import also use capabilities) and
  requires the provider's capability. Copilot is still the only registered
  provider.
- **Context usage**: the ring beside the composer model shows used tokens
  as a share of the active prompt budget. Click it for token counts and the
  reported cached share. A short mark on the ring, and a line in its
  popover ("Compacts at 80% (218K tokens)"), show where Copilot starts
  compacting the conversation: the threshold it opened with, reported as
  `compact_threshold` on the Task summary while it is open (a change in
  Settings reaches it when it reopens), else the setting. Before a report, the track is
  empty and its popover says usage is unavailable. Effort and context size share one menu.
  The meter is live only: reopening a conversation, restarting the service
  or changing its selection clears it until a fresh report. Compaction or
  truncation appears as a notice in the conversation; the next usage report
  updates the meter.
- **Compacting**: while Copilot compacts the conversation, whether you ran
  `/compact` or it compacts on its own as the context fills, the Task says
  so: the status line above the message box (and, during a turn, the line
  at the foot of the conversation) reads "Compacting the conversation…",
  the header chip and the Task's row read "Compacting…", and the Task sits
  under Working in the list (a request waiting for you still wins). When it
  ends a quiet notice stays in the conversation, "Compacted the conversation
  · freed 2,332 tokens." (the count only when Copilot reports one), or
  "Compacting the conversation failed: …" with Copilot's reason. Messages you
  send meanwhile follow the usual Send now / After this turn rules. The
  service reports it as `compacting: true` on the Task summary, absent
  otherwise; it is live only and never saved.
- **Status line**: while a Task works, the line above the message box says
  what the agent says it is doing (Copilot derives it from its todo list),
  else a calm verb ("Working…" for a turn without a message), and for how
  long. A model call Copilot retries adds
  "Retrying, attempt 2 · HTTP 429 · rate limited" until the call goes
  through; a subagent's retry shows under its row. With a todo list it also
  says how far the agents got ("Todo 2/7"), the row the work is at and how
  many rows are in each open stage, "1 in progress · 1 blocked · 3 to do",
  each in its own colour behind its own glyph (a phone shows the glyphs and
  numbers), or, while the turn has not touched the list yet, the same counts
  from the last turn; clicking it then opens the list, by sections In
  progress, Blocked, To do and Done, with the subagent that wrote each row
  when uam could tell. Without a list, clicking it jumps to the end of the
  conversation. After a stop it says why until the next turn
  starts: "Stopped" after your Stop, or "Stopped: time limit" (a routine's),
  "Stopped: credit limit" (autopilot's, with the credits used and turns),
  "Stopped: remote command" or "Stopped: MCP server". The header says the
  same, and the Task row's tip says "You stopped it" for a Stop uam
  recorded as yours, else plain "Stopped". The service
  reports the reason as `stop_reason` on the Task summary (`owner`,
  `time_limit`, `credit_limit`, `remote` or `mcp`, absent when there is
  none); it is saved with the stopped state
  and cleared when the next turn starts. The running turn's intent and
  retry come as `turn_activity`, in the Task snapshot and in `turn_activity`
  events; they are live only and never saved. So is the todo list
  (`turn_activity.todos`): uam asks the agents, subagents included, to keep
  one in Copilot's session `todos` table for work of more than two steps,
  reads it when it changes and when the Task's conversation reopens, and
  sends at most 100 rows with the counts of all: `done`, `total`,
  `in_progress`, `pending` and `blocked`, a stage with no rows left out, and
  `open`, `in_progress` plus `pending`.
- **Each turn's todo list**: a turn that changed the list keeps it as it
  left it. Its last reply's foot reads "Todo 4/7 · 1 in progress · 1 blocked
  · 1 to do" and stays on screen; clicking it opens that list by the same
  sections as the live one: the rows the turn changed and the rows still
  open, at most 50. The end of a turn waits up to a second for Copilot's last
  change to the list to be read. The counts are saved with the turn's timing
  (`turn_timings[].todo`, as above), the rows in `turn-todos.jsonl` beside the
  Task's attachments, which is deleted with the Task. Counts saved before uam
  split `in_progress` from `pending` carry `open` alone, and the foot then
  reads "1 open".
  `GET /api/sessions/{id}/turns/{timing_id}/todos` returns them, also for a
  settled or archived Task, or 404 when none were kept.
- **Copilot notices**: Copilot's warnings ("Warning: …", with what to do in
  UAM, such as signing in again in Settings, and a link only when it is
  https) and its authentication, model, MCP and notification messages show
  as notices in the conversation, a subagent's in its own transcript, and
  come back with the history. Its other messages (timing, context window,
  snapshots, configuration, terminal tips) are not shown.
- **Usage and credits**: for a provider that reports quota (Copilot has the
  `usage` capability), UAM reads the account's quotas when it starts, after
  each turn ends, and at most once a minute while a browser is open, and
  keeps the last result. `GET /api/usage` returns it as
  `{"quotas": [{"provider", "type", "used", "entitlement", "unlimited",
  "remaining_percent", "overage", "reset_at"}], "stale", "updated_at"}`; the
  event stream carries the same object in `snapshot` and sends it as a
  `usage` frame when it changes. A failed read keeps the last quotas and sets
  `stale`; `reset_at` appears only while it is in the future; an unlimited
  quota has `entitlement` 0. A Task's `usage: {"ai_units"}` is what its
  conversation used so far, subagents included, and is absent until Copilot
  reports it. Copilot does not keep per-call costs in its history but does
  record the session total, so the figure comes back after a service restart
  once its recorded transcript loads, including a settled or archived Task
  viewed read-only. Models in `/api/meta` carry `cost_tier` (`low`, `medium`,
  `high`, `very_high`), for `auto` `discount_percent`, and `prices`: Copilot's
  token prices in AI Credits (the unit of `ai_units`) per `batch_size`
  tokens, for `input`, `output`, `cache_read` and `cache_write`, with
  `max_prompt_tokens` and a `long_context` tier where Copilot has one. The
  Task's `context` adds `prompt`, the input tokens of the latest main-agent
  model call, and `cached`, how many of those came from the prompt cache, so
  an estimate can price the cached share at `cache_read`. The composer shows
  remaining credits in a popover chip, including stale state when a refresh
  fails. The input-cost estimate beside it excludes output; the model menu
  shows the same estimate for each model. No estimate appears without prices
  or reported context.
- **Conversation**: your messages sit on the right, the agent's on the left,
  both rendered as markdown (never as HTML) while they stream; in your
  messages a single line break (Shift+Enter) stays a line break. The agent's
  reasoning, when the provider reports it, shows inline in grey, three lines
  at a time with "Show more"; while it streams you see the latest lines, and
  "Thought for 12s" once done. Every tool call is its own line: what ran
  (the shell command, URL, file path, search pattern, query or skill), a
  mark for running, done or failed, and, in yolo mode, "auto" where UAM
  allowed it; a call you allowed or denied yourself says so on the same
  line. Click a line for the full input and output. A run of more than eight
  calls keeps the first two and the last three and folds the rest behind
  "Show N more". When the agent asks you a question, the conversation shows
  it in full while it waits. Once answered it shrinks to one small card: the
  question, then "You chose" and the option you picked or "You wrote" and
  your text ("Declined" if you declined, "Not answered" if the turn ended
  first), also after a reload or restart. Click the card to see the whole
  question, every option with yours marked, and your full answer. Several
  questions asked together show as one card ("3 questions") that opens the
  same way. Copilot's recorded thinking also returns after a restart.
  If you scroll up while text arrives, the view stays put
  and offers "Jump to bottom". While the agent works, a label above the
  composer shows that it is working and for how long, and stays in view as
  you scroll; what it is doing shows at the end of the conversation. With
  the Compact activity setting (the default), the step in progress stays
  open there: the thought being streamed, its newest lines in a short box,
  or the call that is running, as its line with the newest lines of its
  output. When the step finishes it leaves that spot and joins the counts on
  the turn's summary line, and the next step takes its place; once the turn
  ends only the summary line remains. Questions, permission requests and
  subagents appear as before.
- **Diagrams and code**: a fenced ` ```mermaid ` block in a reply renders as
  a diagram once its fence has closed, with a Diagram / Code toggle and Copy
  code in its header; clicking the diagram opens it as large as the window
  allows. A block Mermaid cannot parse shows the code with a note that says
  where and what is wrong. Code blocks with a language are
  highlighted (Go, TypeScript, JavaScript, shell, JSON, YAML, Python, Rust,
  SQL, HTML/XML, CSS, Markdown, Dockerfile, Makefile, INI/TOML, diff).
  Diagrams render in the browser inside a sandboxed frame that cannot read
  your session or call the service (ADR 0004); the first diagram of a page
  load fetches Mermaid (3.4 MB).
- **Images in replies**: an image the agent refers to by path, as
  `![Screenshot](/home/you/project/shot.png)` or `![](./shots/a.png)`, is
  served from the Task's folder by the service, so it shows on any machine
  the browser runs on. A link to such a file opens it the same way. Only png,
  jpeg, gif and webp files inside the Task's folder are served, up to 20 MiB;
  a file outside it, reached directly or through a symbolic link, is not.
  An image at a web address stays a link, as the page loads nothing from
  other origins.
- **File previews**: resolved file references appear as tags with a format
  icon and the filename. Unknown formats use a default file icon; hover for
  the full path, which is also part of the accessible link name. Click a tag
  to open one preview beside the conversation, or in a dialog on a narrow
  screen. Images and diagrams open
  larger. Text files show at most the first 64 KiB as plain text, with a notice
  when more content is available. Use **Open in new tab** or **Download** for
  the full file; PDFs and unsupported types offer those actions too. HTML
  reports stay interactive inside an isolated frame. The **Close preview**
  button remains available; Escape closes the preview when focus is in the
  app, but does not cross into the app from a focused HTML frame. Closing
  returns focus to the opening control. Opening another preview or switching
  Tasks discards the old preview and cancels its pending read.
- **Declared files**: `uam_show_file` records a file reference and optional
  title. Its tool call folds into the turn's activity in both layouts;
  the conversation's inline file link opens the preview without a separate
  card. Opening a file still checks current availability, and temporary
  files require an explicit grant. Declaration metadata does not grant access.
- **Tasks started by an agent**: the agent can call `uam_create_task` to
  start a new Task in an existing Project. It gives the Project's ID or
  exact name, the first message (up to 16 KiB, with no control characters
  other than newlines and tabs), and optionally a name (up to 120
  characters) and a model. The tool never creates a Project and takes no
  directory. A name several Projects share is refused with their IDs. A
  model is checked as for New task; without one, the Task gets the model
  New task would pick from the Task defaults in Settings, and effort and
  context size stay at their defaults. The new Task always starts in safe
  mode. The call waits up to 15 seconds for the Task to be created and
  sent the message, then returns the new Task's ID. If the create fails in
  that time, the call fails with the reason. If the conversation is still
  opening, the call says so, and the Task appears in the sidebar once it
  opens; a failure after that is only written to the service log. The Task
  runs on its own: nothing from it goes back to the Task that started it. Its conversation begins with "Started by another task", and
  the API lists that Task as `spawned_by`, which survives restarts. A Task
  started this way does not get the tool itself. One Task can start at
  most 5 Tasks, its subagents' calls included. Every Task it started counts
  while its record exists, archived ones too; deleting one frees its
  place. Only an Active Task can call the tool.
- **Tables**: Markdown tables fill the message width. Each completed table
  has header controls for sorting and a contains filter per column. Filters
  combine; **Clear filters** restores all rows. Sorting cycles through ascending,
  descending and original order. Plain numbers sort numerically; IDs, dates and
  values with units remain text. Tables over 100 rows are paged, with sorting and
  filtering applied to all rows. A table still streaming stays plain until complete.
  These controls only change your view; they make no request to the agent and do
  not change the stored message or its Markdown export.
- **Charts**: ask a Task a data question ("chart commits per day this
  month") and its agent can answer with a chart through the `uam_chart`
  tool: a line or bar chart of up to 500 rows and 4 series, drawn in your
  browser with Apache ECharts. The agent either passes the rows or,
  cheaper, gives a shell command that prints them as CSV or JSON. uam runs
  that command itself in the Task's folder (30 seconds at most, 1 MiB of
  output), so the rows never pass through the model; the agent gets only a
  summary. uam runs a chart's command only for a Task in Yolo mode: in Safe
  mode the agent must run the command with its own shell tool, which asks
  you first, and pass the rows. The chart shows as a card in the
  conversation with **Table** (the rows), **Copy CSV** and **Pin to
  project**, and a footer saying where the rows came from and when. The rows are
  kept with the Task, so the card survives a reload and a restart, and goes
  when the Task is deleted. A chart fills the width of the conversation and
  keeps its text at the same size at any width. Each series has a fixed
  colour: several series take blue, orange, green and purple in order, and a
  chart of one series takes the colour its name picks, so the same measure
  keeps its colour across charts and refreshes.
  **Table** uses the same full-width sorting and filtering controls. **Chart**
  returns to the drawing; table filters do not change the chart. Only the selected
  view is mounted. Heatmaps with a single series of Cartesian category/value
  tuples or calendar date/value pairs also offer Table. Advanced charts using one
  explicit, column-oriented dataset with scalar rows expose that source as a table.
  Other or ambiguous specifications retain **Data**, the saved JSON, and advanced
  charts keep **Copy JSON**. Switching views uses the already fetched chart data.
  The tool tells the agent to explain the result without repeating a Markdown
  table, but UAM does not remove model-written tables from the transcript.
  **Pin to project** shows the command that a refresh will run; pinning is
  your approval of it. A chart made from rows the agent passed pins as a
  snapshot and never refreshes. A Project keeps up to 12 pinned charts.
  Once it has any, the Task header shows **Charts** with their count (on a
  phone, in the task's actions menu); it opens a panel beside the
  conversation with each chart's latest value, a small chart (click it for
  a larger one), when it was refreshed, **Refresh** and unpin (×).
  Refresh runs the saved command again in the Project folder and redraws,
  with no agent and no model call; it works at most once a minute per
  chart. Opening the panel refreshes each chart whose rows are more than an
  hour old. A failed refresh keeps the last rows and shows why.
  Conversation charts have one **Chart tools** button in the header. Tap,
  click or hover to open Table/Data, Copy, Pin, and available zoom/reset controls.
  Range handles live in this menu too, with the selected category/date endpoints;
  saved chart sliders no longer add a control bar beneath the drawing.
  Full charts support zooming and panning. Hold Ctrl while scrolling to zoom,
  or use the zoom buttons in Chart tools. Plain scrolling moves the conversation;
  tables and chart data pass scrolling to it at their edges. Series can be toggled
  through the legend. Small pinned previews keep their compact view.
  For other chart families, `uam_chart` accepts `kind: "echarts"` with an
  `options` JSON object, or a command with `format: "json"` that prints that
  object. This supports pie/donut, scatter, radar, heatmap, candlestick,
  boxplot, tree, treemap, sunburst, Sankey, graph, chord, funnel, gauge,
  parallel, pictorial bar, theme river, effect scatter and lines, alongside
  line and bar. These charts offer **Data** to inspect the saved specification and
  **Copy JSON** to copy it. Pinning and refresh work as above.
  The same saved specification, data and viewport size produce the same
  initial drawing: animation and random layouts are disabled. Interactions
  change the view, not the saved data. Options are bounded JSON, with no
  executable callbacks, external assets, custom series or geographic maps.
  Repeated pictorial symbols need an explicit integer `symbolRepeat` from
  0 to 1000; automatic repetition is unavailable. `splitNumber` uses the
  same limit. Sankey `layoutIterations` defaults to 32 and accepts 0 to 128.
- **Routines**: recurring work in a Project, such as "every weekday at
  09:00, check the dependencies for updates" or "every 6 hours, run the
  flaky tests and report failures". **Routines** (the clock) in the
  sidebar header and on the collapsed rail opens every Project's routines,
  each Project's under its name with its own **New routine**, `#routines`
  in the URL (`#routines=all` works too); pressing it again closes the
  view. The header's Project filter narrows the list to one Project,
  `#routines=<project id>`; the clock beside a Project in the sidebar's
  Project filter and Edit project → Routines open that filtered view. The
  view replaces the Task pane like Settings, and a `#routines` link opened
  while the page is open shows it. With no routines yet it says what a
  routine is and offers New routine, which asks for the Project when there
  are several. `GET /api/routines` lists every Project's routines, oldest
  first. Each routine has a name, the first message its runs send, a
  model (New task's model unless you choose one), a schedule, a mode, and
  two limits. A schedule is every day at a time, every weekday
  (Monday to Friday) at a time, every 1 to 24 hours, or once a week on a
  day at a time, in the local time of the host the service runs on. Nobody
  watches a run, so a new routine is **Yolo with autopilot**: every
  permission request is allowed, and the run's Task turns autopilot on
  (as `/autopilot on` does) before its first message, so it keeps working
  until the agent calls `task_complete` or the time limit stops it.
  **Yolo** allows every request for one turn: the run ends when the agent
  first stops. **Safe** makes each permission request wait for you, and
  the Task shows as Needs you. The form explains the chosen mode and, for
  both Yolo modes, that the agent acts without asking while nobody is
  watching. A routine saved before this choice existed keeps its mode
  (Safe or Yolo, without autopilot), and each card shows its mode. The API
  takes `mode` (`safe` or `yolo`) and `autopilot`; a create that leaves
  them out is Yolo with autopilot, and one that sets only `mode: "safe"`
  gets no autopilot. **Daily run limit** (1 to 100, default 24) counts every run
  that started, or tried to start, a Task since local midnight, Run now
  included; a firing past it is skipped. **Stop a run after** (1 to 720
  minutes, default 30) cancels the turn still running then, a turn waiting
  for your answer included; Stop also turns autopilot off, so an autopilot
  run that never finishes ends there as Stopped at the time limit. Its Task
  then reads "Stopped: time limit" in its header and status line, and
  "Stopped at the routine's time limit (N min)" in the sidebar and as its
  state's detail (`state_detail`), not "You stopped it", which only a Stop
  of yours shows. Each run starts a normal Task in the Project,
  named "<routine name> · <date and time>", with the routine's prompt as
  its first message. It shows in the sidebar and Needs you like any Task,
  begins with "Started by a routine.", and the API lists the routine as
  `routine_id`. Like a Task started by an agent, it does not get
  `uam_create_task`. A firing is skipped, with the reason "still running",
  while the previous run's Task is still working, also on a follow-up you
  sent it. Each card shows the schedule, the next run, the last run's
  outcome with a link to its Task, and Run now, Pause or Resume, Edit and
  Delete (asked first; the run history goes, the Tasks stay). Run now fires
  at once under the same rules, paused or not, and leaves the next
  scheduled run where it was. History lists the newest 100 runs: when,
  what fired it (the schedule, Run now, or a firing missed while uam was
  stopped), the Task, and the outcome: Running (Needs you while its Task
  waits for you), Finished, Failed with the reason, Cancelled (stopped in
  the Task), Stopped at the time limit, or Skipped and why. Routines are
  kept in `sessions.json`; the next run survives a restart. Firings missed
  while the service was down run once when it starts, not once each, and
  a run the stop interrupted is recorded as failed. Pausing clears the
  next run; resuming, or changing the schedule, sets it from then. Removing
  the Project removes its routines.
- **Subagents**: when the agent delegates work to a subagent, the reply that
  started it gets a chip on its turn line ("3 subagents · 1.2M tokens · 2
  done · 1 running") that opens the reply's subagents as one-line rows: state,
  name, duration and tokens, a failed one with its error under it. A subagent
  that another subagent spawned sits indented under it. While subagents run,
  the same rows sit at the foot of the conversation with the total tokens.
  Tokens are what Copilot reports for the subagent, shown as K, M or B; while it runs the count grows, and when it ends Copilot's
  total replaces it. On a computer, resting the pointer on a row shows a peek:
  how long it ran, tokens, tool calls, model, its last steps while it runs, its
  result or error, who spawned it, and Stop, Full transcript and Copy agent ID.
  Clicking a row opens its transcript in a panel beside it over a dimmed page;
  on a phone a tap opens a sheet from the bottom, first the peek, then the
  full transcript. The transcript shows the subagent's own prompt, replies and
  tool calls, live while it runs, with a pill per run when it ran more than
  once and a pill per subagent it spawned. "Show where it was spawned" jumps
  to the tool call in the conversation. There is also a "Subagents" button in
  the header with the total and how many are running; it lists the subagents
  grouped by the message that started their reply, with filters by state. The
  list holds the newest 200 subagents; when Copilot's record has older ones,
  the count reads "200+" and the list ends with "Show older subagents", which
  loads the next 100 from the record. Nothing older is read until you click.
  The transcript uses the same Compact/Detailed preference as the main
  conversation. Prose and paths wrap to fit; wide tables and code blocks
  scroll within their own regions. Subagent output never
  appears in the Task's own conversation. Its model and effort are shown
  when Copilot reports them; they are not guessed from the parent Task.
  A finished subagent's line is the first line of its own result, as
  Copilot reported it; nothing rewrites it. A subagent is "Idle" when it has finished and the main
  agent may still resume it. The web interface sends no messages to a
  subagent.
- **Approvals and questions**: when the provider asks for permission or asks a
  question, a card appears in the conversation and the Task's row moves to
  the attention items at the top of the sidebar, labelled "Input"; open the
  Task to answer (see Sidebar). Nothing is approved
  automatically unless you turned on yolo for that Task, and questions always
  wait for you. If no browser is connected, the request waits; the first
  answer from any tab wins and later answers are refused. Once decided, the
  card goes away: the decision shows on the tool call it was for, and a
  request UAM could not tie to a call shows as one grey line where it
  happened. A question with one question is not a card: it appears at the
  top of the message box, with its options. Pick an option there (pick the
  same one again to clear it) or type your own answer, then press Enter or
  Answer. You can always type your own answer, even when the agent offered
  options. The answer is either the picked option(s) or the typed text,
  never both: typing clears the picked option, and picking an option clears
  the typed text. An option the agent marks "(Recommended)" is already picked
  when the question appears; it is sent only when you press Enter or Answer.
  Nothing else goes with an answer: Attach is unavailable while a question
  waits, and a dropped or pasted file is refused with "Answer the question
  first." **Decline** (an ×)
  sits between Stop and **Answer** (an up arrow). What you had typed before the question
  arrived is kept and comes back once the question is settled.
- **Native planning**: `/plan` in a Copilot Task uses Copilot's own plan mode.
  When the plan is ready, **Plan ready** appears above the composer and the
  Task moves to Needs you. **Read plan** opens that reviewed snapshot, with
  revision numbers and **Show changes** after a revision. Pick an offered
  implementation action or **Leave planning**; the runtime's recommendation
  is staged, and Enter, **Approve plan**, or a second click sends it. Typing
  feedback clears that choice and asks Copilot to revise the plan. Feedback
  is a review response, not a new Task message. **Stop** remains available.
  Plan decisions appear as two-line receipts; their reader uses the recorded
  review even after the Task closes. A supported runtime may offer autopilot,
  subagents, interactive implementation, or leaving without implementation.
  Plan approval does not change Safe/Yolo permissions. During implementation,
  the status line labels current, touched SQL Todo progress **Plan**; an old
  or unavailable list is never treated as progress on the new plan. Only the
  exact provider-reported scratch-plan file is excluded from Task changes;
  project files named `plan.md` still count. Native session changes exclude
  it only when its path matches exactly; a relative path whose workspace
  root is unknown or ambiguous stays listed.
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
  > on everything anyway. Sign-in is required to create or control a Task.
- **Commands**: type `/` at the start of a message to list supported provider
  commands and skills. Names and aliases are searchable. Known unsupported
  commands stay visible with their reason and cannot become ordinary prompts.
  Up and Down move, Enter or Tab picks, and Esc closes. Argument choices fill
  the input; press Enter to run. Commands use their own endpoint, never Queue
  or Steer; only commands marked available during a turn can run then.
  A failed catalogue load holds slash input and offers Retry commands.
  Results can display text, offer subcommands, or open the existing model,
  permissions, context, usage or rename control. Choosing a subcommand fills
  the composer for explicit submission. `/yolo` and `/allow-all` change the
  same Safe/Yolo permission policy as the toolbar; autopilot does not change it.
  See [Web commands and execution state](web-commands.md) for the exact supported
  command list, limits and retry behavior.
- **Execution mode**: supported providers report Interactive, Plan or Autopilot
  separately from permissions. Both share one toolbar menu, whose label reads
  them together ("Safe · Interactive"). Its icon and first line rate the pair:
  Safe · Interactive is the safest, Safe · Autopilot keeps working on its own
  but still asks, Yolo · Interactive is unsafe, and Yolo · Autopilot is highly
  risky, so switching into it (from the menu or by a typed command) asks you
  to confirm first. The composer shows the runtime's objective
  status and, on expansion, reported turns, credits, limits and pause or
  completion details. Missing or stale observations say Status unavailable.
  Stop remains available between autopilot turns, disables continuation and
  pauses queued follow-ups through the existing cancellation operation. Partial
  cancellation failures remain errors rather than a claimed stopped state.
- **Background tasks**: a chip in the composer's toolbar counts the Task's
  running provider shells; click it to see each shell's status and command
  and to stop one. Stopping one shell does not stop the foreground turn.
  Stop requested means the provider accepted cancellation; the list waits for
  a reported terminal state. Unknown or read-only tasks cannot be stopped.
  A running subagent's Stop sits in its peek and its transcript.
- **File references**: type `@` at the start of a message or after a space to search the project's
  files: what `git ls-files` sees, including untracked files that are not
  ignored, and their directories. Picking one inserts `@path` and adds a chip;
  removing the chip removes the token, and deleting the token drops the chip.
  A message references at most 20 paths, each a regular file or a directory
  inside the project (no symbolic links, nothing binary). Copilot receives the
  reference and reads the file with its tools, which asks for a read
  permission in safe mode. A directory that is not a Git working tree offers
  no list.
- **Attachments**: the paper-clip button, pasting, and dropping files onto the
  composer upload them at once. A chip shows the upload's progress, then its
  size and type, or why it was refused. Allowed: png, jpeg, gif and webp
  images up to 3 MiB, PDF up to 10 MiB, and UTF-8 text up to 256 KiB, at most
  5 per message; the type is taken from the file's bytes, not its name.
  Until a file has uploaded (in a new Task, until its first Send), its chip
  and the model's image and PDF checks go by the name. SVG, HEIC, audio, video,
  archives and everything else are refused. Images and
  PDFs need a model that accepts them: Copilot reports this per model, `auto`
  is not checked, and the button says so when the Task's model takes text
  only. Attachments go with the message, whether it is sent, queued or
  steered. A message can be only attachments (or, through the API, only
  file references), with no text; blank text beside them is not sent. A
  message with no text and nothing attached cannot be sent. In the
  conversation, images show as thumbnails that open larger on click and
  other files as chips that open the stored copy, also after a reload. UAM
  keeps the files outside the project (see Settle, archive, and delete).
- **Images from tools**: when a tool returns an image, such as a screenshot
  or Copilot's `view` of an image file, UAM keeps a copy with the Task and
  shows it as a thumbnail under that tool call in the turn's activity, for
  subagents too. In Compact the thumbnail also stands in the reply at the
  call's place, without the tool call's line. Click it to see it large.
  Kept: png, jpeg, gif and webp up to 5 MiB, at most 50 per Task, one copy
  of each distinct image; the tool call notes any it left out and why. They
  come back after a reload or a restart of UAM, because Copilot records the
  image's bytes in its session. They are deleted with the Task, like
  attachments.
- **Messages while a turn runs**: Send shows what it will do as an icon,
  **Send now** (steer, an up arrow) or **After this turn** (queue, the
  waiting list's icon), following the Settings view; its tooltip names the
  action and its key. The menu beside it (the chevron, "More send options")
  offers the other. The send buttons carry icons only; screen readers hear
  their names.
  - **After this turn** holds the message until the turn completes, then sends it as
    the next prompt. A Task queues up to 20 messages and sends them one turn
    at a time, oldest first. You can cancel a queued message until it is
    sent; to change one, cancel it and queue it again. Each queued message
    retains its selected model, effort and context size. Changing the next
    draft or the Task's settings does not change messages already queued.
    The waiting messages are listed above the message box ("2 waiting").
  - **Send now** adds the message to the turn that is running. The agent reads
    it before its next step. Once Copilot accepts it, the conversation shows
    the message in bold italic (accepted); once Copilot records delivery, the
    same message turns to normal text (delivered). If it arrives as a new
    turn after the current one ends, it moves to that turn's position as its
    prompt.
    A steer cannot be taken back. If the turn is stopped or fails before the
    agent took it in, the message says **Not delivered** and a notice explains
    why. With Copilot, a steer also moves a
    shell command that is running to the background.
    A steer uses the current turn's model settings. If your draft selects
    different settings, or the provider cannot steer a running turn, Send
    becomes **After this turn**, Enter queues, and Send now stays in the menu,
    dimmed, with the reason. UAM does not silently change a requested steer
    into a queued message or restart the running response.
  - When no turn is running there is one button, **Send**, which sends the
    message at once.
  - While a turn runs, Enter does what Send shows (Send now by default) and
    Ctrl+Enter (⌘+Enter on a Mac) the other; the tooltip and the menu name
    the keys. Files and attachments go with a steer as with any other
    message.
  - **History**: with the caret on the first line (or an empty message), Up
    recalls the previous prompt from this Task, newest first, as in a shell;
    Up again goes further back. Down from the last line comes forward, and
    one step past the newest brings your unsent draft back, as does Esc.
    Editing a recalled prompt makes it the new draft. Only the text changes;
    picked files and uploads stay. When a `/`, `$` or `@` list is open, Up
    and Down move through the list instead.
  Prompt requests accept optional `settings: {model, effort, context_size}`;
  omitted settings snapshot the current Task selection. The service validates
  them again before dispatch. Unsupported selections return 400; different
  settings on an active steer return 409. A failed settings change sends no
  prompt and pauses the remaining queue. Attachment uploads accept an optional
  `model` query parameter for the draft's offered model; submission and dispatch
  recheck its media support without changing the running turn.
- **Suggested reply**: when a turn completes with an answer, the message you
  would most likely send next shows as muted ghost text in the empty composer,
  where its placeholder would be (two lines, then an ellipsis; the composer
  keeps its size). Right Arrow or End in the empty composer puts it in the
  box with the cursor at its end, ready to edit or send; nothing is sent
  until you send it. On a touch screen an arrow button at the end of the
  ghost text does the same. Typing replaces it, and clearing the box brings
  it back. Screen readers hear it as the box's description. It comes from
  one Utility model call, made the first time a composer shows that turn,
  with your last message and the agent's final answer; the service keeps the
  result with the Task, so opening it again, in any browser or after a
  restart, asks for nothing more. None shows while the Task works, waits for
  you, has queued messages, or is read-only. Without a Utility model, or past
  today's Background AI limit, there is none. Settings → Composer →
  **Suggest replies** turns it off.
- **Outcome line**: when a turn completes, the Task gets a one-line summary
  such as "Fixed the flaky redraw test; 3 files changed; tests pass". The
  Task's row in the sidebar keeps it in its hover text ("Review" is the
  visible status until you open the Task, then "Finished"), and the Task header
  beside its state (not on a phone). Everything after the opening phrase
  is the finish evidence's own reading of the turn (the service reads it
  once, for both): the files the Task's edit tools changed in the turn (the main
  agent's and its subagents', the files Changes shows for "Last turn"),
  whether the main agent's last test run (a "Ran the tests" row in the
  evidence) passed ("tests pass") or failed ("tests fail"), saying nothing when
  that run's result is unclear, and how many of its commands failed or
  exited non-zero. The phrase arriving after the turn does not mark the
  Task unread again. The opening phrase comes from one
  Utility model call, told to describe only what the agent's final answer
  says it did and to leave tests, files and commands to the evidence; until
  it answers, or without a Utility model, the line holds the evidence only.
  A new turn clears the line, and a turn that is stopped or fails gets none.
- **Branch from here**: a finished reply's turn menu offers native recorded
  history branching when the provider supports it. Its anchored model picker
  copies the conversation through that reply into a new active Task, without
  sending another message. Choose the same model, another offered model or
  Default; a changed model starts with default effort and context size. The
  source remains intact, and the new Task links back to it. Both Tasks use the
  same Project and current files. This creates no Git branch or isolated
  workspace. If the native result is uncertain, UAM keeps the request and
  refuses to create another branch automatically. The picker's **Dismiss**
  clears that one request so you can branch again; a Copilot session may still
  exist for it. When the branch was created but could not be added as a Task
  (for example, its Project was removed), the picker offers **Add the existing
  branch**, which retries adding that saved session without branching again,
  and **Dismiss**, which clears the request without deleting the Copilot
  session. Deleting the source Task also clears its unresolved requests.
- **Rewind to before this prompt**: when the provider supports it, a finished
  reply's turn menu also offers a native rewind of the open conversation. It
  works only while the Task is idle, with nothing queued, waiting or running in
  the background, and never resumes a closed conversation. Its anchored
  preview names how many prompts go and, for **Conversation and files**, which
  captured files return as they were before (a deleted file reads
  `+458 restored`); **Conversation only** leaves files as they are. Files
  changed after Copilot's last write are never overwritten; the result lists
  them as kept. If the conversation or its files change before you confirm, the
  preview is read again. UAM records the request before Copilot runs it, sends
  it once, shows Copilot's outcome and per-file results, then reads the
  conversation again. Copilot restores files before it shortens the
  conversation, so a partial result can leave restored files with the
  conversation unchanged. If the result is lost, the Task says so and refuses
  new messages, branches and rewinds until you choose **Reread conversation**;
  UAM never repeats the rewind itself. If that reread cannot settle the
  outcome, **Release task** clears the hold without touching the history: the
  conversation may already be shortened and files partly restored, and UAM
  records that the outcome stayed unknown. Recorded usage stays counted.
- **Edit and resend**: the same menu puts that reply's prompt, read in full,
  into the composer with its stored attachments, under a flat strip that says
  what sending would rewind; the status line hides meanwhile. Any draft you had
  is set aside and comes back when you stop editing or the edit is sent.
  Nothing changes until you send. An edit the send would refuse (empty, too
  large, a missing attachment or file, signed out) is refused before anything
  is rewound. Sending rewinds to before the prompt, waits
  until the conversation is read again, then sends the edit once as an
  ordinary message. If the rewind is uncertain, partial or changes nothing, or
  the message is refused, the edit stays in the composer with the reason and
  nothing is sent or retried automatically. An attachment without a stored
  copy cannot be sent again; the strip says so.
- **Run again and Try with another model**: the Task's actions menu (the "…"
  button in its header, or its sidebar row's context menu) has **Run
  again**, which starts a new Task in the same Project with the same model,
  effort, context size and Safe/Yolo mode, and this Task's last message as
  its first, and **Try with another model…**, which does the same on a model
  you pick (effort and context size then start at their defaults). The new
  Task opens; its transcript begins with a note naming the Task it repeats,
  which opens that Task, so the two can be compared. Attachments of the
  message are not sent again.
- **Export as Markdown**: the Task's actions menu downloads the whole
  conversation as a `.md` file: every message in order, with older history
  the service no longer holds read from Copilot's record, each tool call
  folded into a `<details>` block whose summary names the command and its
  exit code (the call's input and output inside), and a subagent's call
  with its generated summary line. When the record cannot be read, the file
  holds what the service keeps and says what is missing.
- **Settings**: the gear at the bottom of the sidebar opens the Settings view
  in the main pane (`#settings` in the address bar). UAM keeps the web
  interface's settings in `sessions.json`, so they apply in every browser.
  The first is what Enter does while a turn runs: **Send now** (steer, the
  default) or **After this turn** (queue). A change is saved as you make
  it; if the service refuses it, the old value comes back with the reason. `GET /api/settings` returns the
  settings as `{"send_default": "steer", "terminal": false}` (see
  [Terminal](#terminal)), and `PATCH /api/settings` with
  `{"send_default": "queue"}` changes them. An unknown key or value is
  refused and changes nothing. **New tasks** holds what every new Task
  starts with, in every Project: model, effort, context size (where the
  provider allows it) and Safe or Yolo mode, kept as `task_defaults`, e.g.
  `{"task_defaults": {"provider": "copilot", "model": "gpt-5-mini",
  "effort": "high", "context_size": "default", "mode": "safe"}}`; the
  service checks them as it checks a Task's selection. Until they are set,
  a new Task starts with the provider's own defaults. Defaults that older
  versions kept per Project are adopted from the newest Project that had
  them the first time the service loads the store. **Compact the
  conversation when its context reaches** sets where Copilot starts
  compacting a Task's conversation: 50% to 90% in steps of 5, 80% (Copilot's
  own default) unless changed. Compacting earlier keeps answers faster and
  cheaper but drops older detail sooner. It is kept as `compact_threshold`
  (a whole percent; `null` or 80 puts the default back, which is not
  stored) and applies to new Tasks and to a Task already open the next time
  its conversation reopens. Where a turn waits for compaction to finish
  stays Copilot's default, 95%. `hidden_models`, e.g.
  `{"hidden_models": {"copilot": ["gpt-5-mini"]}}`, lists the models not
  offered in the pickers; a `PATCH` replaces the list of each provider it
  names, and an empty list shows all of that provider's models again. At
  least one model the provider lists must stay visible, and a provider can
  hide at most 200 IDs of up to 128 bytes each. An ID the provider no longer
  lists is kept. Hiding is only a display preference: a Task or Project
  already on a hidden model keeps it. **UAM** (General) shows the version
  the service runs and, once another version is installed at the binary it
  started from, **Restart**; see
  [Restart onto a new version](#restart-onto-a-new-version).
  The **Utility model** is the model UAM uses for its own small AI jobs,
  including Task titles, completed subagent result lines, suggested replies
  and the opening phrase of outcome lines. Pick the cheapest
  that does the job. It is kept per provider under `title_model` (the key keeps
  its first name). With no entry, UAM uses the provider's cheapest priced
  model, worked out whenever it is needed: among the models `/api/meta`
  lists with input and output prices, leaving out `auto` and models hidden
  in Settings, the lowest input plus output price per token wins, then the
  lower input price, then the lower ID. `/api/meta` gives it per provider
  as `cheapest_model`, omitted when no model is priced; then those jobs
  are not run. Titles are the exception: with no entry, a Task is titled by
  its own model at the lowest reasoning effort that model offers, which
  spends that model's credits, and by the cheapest model when that call
  fails or the Task is on `auto`; with neither, the provider keeps its own
  title, which for Copilot is the first prompt. A `PATCH`
  with a model ID, e.g. `{"title_model": {"copilot": "gpt-5-mini"}}`,
  chooses that model; `"none"` opts the provider out, so it keeps its own
  title and subagent report, and UAM makes no utility AI call; an empty ID removes the entry, back to
  the cheapest. Only a provider with the `titles` capability can have a
  model, and the model must be in its current list.
  In Settings the **Utility model** section shows "Cheapest (currently
  GPT-6 Luna)", None, then each visible model
  with its prices. UAM asks the model in a separate short Copilot session
  with no tools and no session store, and deletes that session afterwards.
  It then shows the title, 60 characters at most, and writes it into the
  Copilot session, so `copilot --resume` shows it too. Only the first
  message counts: later messages, attachment-only ones included, commands,
  reopened Tasks and a changed setting never retitle a Task. A first
  message with no text goes with its images (at most 3) to a title model
  that accepts images; otherwise, or when it carried only files, the title
  waits for the end of the first turn that has a reply and is made from that
  reply, over the provider's placeholder. A name you type wins, even while the title is on its way. If the
  model fails or takes more than 20 s, the provider's title stays, and the
  service log says why. Each title costs AI credits,
  about 0.002 with gpt-6-luna.
- **Background AI**: every call UAM makes on the Utility model (Task
  titles, also those made with a Task's own model, subagent result lines, suggested replies and outcome lines) counts
  against a daily limit and is logged. Settings → **Background AI** shows
  today's calls against the limit, the limit itself, and the log behind
  **Show log · N calls today** (collapsed until opened, and read only while
  open), newest first and grouped by day with each day's totals: calls, failures,
  skipped calls, tokens in and out, and AI credits. Each entry has the time,
  what it was for ("Task title", "Suggested replies", "Outcome line";
  older versions also logged "Subagent summary"), the Task (a click opens
  it) or Project, the model
  (marked "the task's model" for a title made with the Task's own model, `session_model` in the log),
  the characters sent and received, the tokens, how long it took and how it
  ended. Tokens and credits are what Copilot reported for the call; when it
  reports none, the tokens are estimated at 4 characters each and marked
  with ≈. **Show older calls** pages back through everything kept: the log
  keeps 30 days, by the server's local date, in `utility-log.jsonl` beside
  `sessions.json`.
  The limit is `utility_daily_limit` in the settings, 200 calls a day when
  unset, 0 to 1000; `PATCH /api/settings` with
  `{"utility_daily_limit": 50}` sets it, `0` turns Background AI off, and
  `null` puts the default back. The day starts at midnight on the server.
  Once today's calls reach the limit, further calls are not made: they are
  logged as skipped (daily limit, or off when the limit is 0), new Tasks
  keep their first message as the title, completed subagents keep their own
  report, no replies are suggested (and the next look asks again), and
  outcome lines keep the evidence alone. Settings then says Background AI is
  paused until tomorrow; raising the limit resumes it at once.
  `GET /api/utility` returns `{"today": {"day", "calls", "limit",
  "paused", "resets_at"}, "days": [totals per day, newest first], "calls":
  [newest first], "next"}`; pass `next` as `?before=` for the following
  page, and `?limit=` (1 to 200, default 200) to size it.
- **Custom models (bring your own model)**: Settings → Models → Custom
  models adds OpenAI-compatible endpoints to Copilot's model list, next to
  the account's own models, so a Task can switch between them mid-Task in
  the composer's model menu. Each entry has a provider name (letters,
  digits, `.`, `_`, `-`), a base URL (`http` or `https`, no credentials,
  query or fragment), a model ID, an optional display name, and the *name*
  of the environment variable that holds the API key. That name must start
  with `UAM_BYOM_` (e.g. `UAM_BYOM_OPENROUTER`), so a custom model can never
  send any other variable of the service's environment to its endpoint. The
  model's ID in UAM is `provider/model_id`, e.g.
  `openrouter/qwen/qwen3-coder`. Models that share a provider name share its
  base URL and key variable; at most 100 are kept. `PATCH /api/settings` with
  `{"custom_models": [...]}` replaces the whole list (fields `name`,
  `display_name`, `base_url`, `model_id`, `wire_api` (`completions`, the
  default, or `responses`) and `api_key_env`); `[]` removes them all.
  Removing a model also takes it out of `hidden_models` and clears a
  `title_model` set to it, so that provider uses its cheapest model again. In Settings a provider is added or edited as
  a whole: name, base URL and key variable, then **Load models** lists what
  the endpoint serves as a searchable checklist (select all or none), and a
  model ID the endpoint does not list can be typed in. Saving writes one
  entry per checked model, named by its ID. The composer's model menu lists
  custom models under their provider name. **Load models** is
  `POST /api/settings/custom-models/discover` with
  `{"base_url", "api_key_env", "wire_api"}` (validated like a custom model);
  the service sends one `GET {base_url}/models` with the key, follows no
  redirect, stops after 10 s or 1 MiB, and answers
  `{"models": [sorted IDs, at most 500], "truncated": true|omitted,
  "key_present": true}`, or an error with only the upstream status line.
  This is an authenticated request from the service to a URL the browser chose.
  A signed-in owner can make the service send this GET, with a `UAM_BYOM_` key,
  to any host it can reach, including loopback and private addresses.
  The key itself never passes through the browser,
  the API, `sessions.json` or the service log: the service reads the
  variable from its own environment when it opens a Copilot session, and
  settings only report `key_present`. Export the variable where the service
  starts, then restart the service. A service started from a clean shell,
  such as `env -i HOME=$HOME bash -ic 'uam web …'`, reads only the
  interactive profile, so put `export UAM_BYOM_<NAME>=…` in `~/.bashrc`. A
  model whose variable is unset or empty fails when chosen, with the
  variable's name in the error; Copilot's models are unaffected. A Task left
  failed that way opens again with its next message, once you switch it to
  another model or set the variable and restart the service. Custom models
  report no prices and cost no AI credits; the endpoint bills you directly.
  This uses the Copilot SDK's experimental multi-provider support.
- **Since you left**: when you open a Task that changed since you last had it
  on screen in this browser, a line under the header says how long ago that
  was and what happened since, counted by the service over everything the
  Task did in between (not only the part of the conversation on screen):
  whether the agent finished (or the turn failed or was stopped), whether it
  ran the tests and how many other commands, how many files it changed, and
  whether it asked you something, for example "Since you left (42 min): the
  agent finished, ran the tests, and changed 3 files." What happens while
  you watch is not counted. **Jump to where you stopped** scrolls to the
  first thing you have not seen (once the page has stopped moving, so a
  scroll on a phone is never fought) and closes the line; × closes it too.
  It does not come back until the Task changes again while you are away.
  The last look is kept per browser: the moment you last had the Task on
  screen, marked while it is open and again as you leave it, hide the page
  or close it.
- **Finish evidence**: once the last turn has completed, the Changes panel
  (the branch button in the Task header) starts with a section titled
  "Finished — check the evidence", above its files, that puts what the
  agent did before what it says it did. While there is evidence, the
  branch button's name adds "evidence available" and its dot turns amber.
  It never uses a model; fixed rules in the service read the whole turn,
  however long, and the outcome line reads the same result. The section
  appears once that reading arrives and shows only the parts below that
  have something; a turn that ran no check, made no claim and edited no
  file (a question answered in chat) has none. Its title folds it away; it
  takes at most about a third of the panel and scrolls inside:
  - **Checks**: each shell command of the main agent in the turn that runs
    tests, a build, a linter, `go vet` or a type check (`go test`,
    `npm test`, `pytest`, `cargo test`, `go build`, `npm run build`,
    `eslint`, `golangci-lint`, `tsc`, `mypy` and similar, recognised by how
    each command of the line starts, also after `cd …&&`, `VAR=value` or a
    line break), labelled with every check the line runs ("Ran the build
    and the tests"), with the command, its exit status, the counts its
    output reports (Go packages, Jest, Vitest, pytest, cargo and TAP
    summaries) and how long it took. The exit status counts for a check
    only when it is that check's: a check followed by `;`, `||`, `&` or
    another line (`go test ./... || true`) passes or fails by its output's
    counts alone, and one piped into another without `pipefail`
    (`go test ./... | tail`, whose output may be cut) only fails by them;
    otherwise its result is unclear. A check followed by `&&` passed when
    the line exited 0. A quoted `|` is no pipe. **Show output** opens the
    command's whole output beside the conversation.
  - **Claims**: each sentence of the turn's final message that says tests,
    the build, linting, vet or type checks pass, or that docs (a README,
    CHANGELOG, `.md` or `docs/` file) were updated, is listed with what
    backs it, or **Not verified** and why: no such check ran in the turn,
    the last one failed or is unclear, code changed after it began (by the
    main agent or a subagent), or no doc file (the one the sentence names,
    if it names one) was edited. A part of a sentence with a negation or a
    failure word is not read as a claim ("I did not run the linter, but the
    build passes" claims the build only); "no errors", "no failures" and
    the like are not negations. A chip at the top counts the claims not
    verified.
    When the turn made claims but ran no check, one line says no tests,
    builds or linters ran.
  - **Changed in this turn**: the files the turn's edit tools changed (the
    main agent's and its subagents'; edits that failed left out), the same
    files Changes lists for "Last turn", named from the repository's top
    as Changes names them, with their line counts. A long list scrolls
    inside the section.
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
- **Settle, archive, and delete**: when a Task is done, settle it from its
  row menu (hover "…" or right-click) or the header menu. A settled
  Task is read-only and its conversation is closed; reopen it to continue,
  and your next message picks up the same conversation. Archive a Task, settled
  or not, to put it away for good; an archived Task cannot be reopened. Only
  an archived Task can be deleted, and a Project can be removed only once
  every Task in it is archived. Deleting or removing never deletes the
  provider's copy of the conversation and never touches the directory. It does
  delete the files you attached to the Task's prompts and the images its
  tools returned, which UAM keeps in
  `~/.config/uam/web-attachments/` (or `$UAM_CONFIG_DIR/web-attachments/`).
  An attachment you upload but never send is deleted after 24 hours.

  | Action | Allowed on | Also needs | Result |
  |---|---|---|---|
  | Settle | an active Task | no turn running, nothing waiting for you, an empty queue, no subagent or background task still running | Read-only; the conversation is closed |
  | Reopen | a settled Task | – | Active again; the next message reopens the same conversation |
  | Archive | an active or settled Task | for an active Task, the same as Settle | Read-only for good; there is no unarchive |
  | Delete Task | an archived Task | – | The Task is removed from UAM |
  | Remove Project | any Project | every Task in it archived, or no Tasks | The Project, its archived Tasks, its routines and the prompts saved for it are removed from UAM |

  A settled or archived Task takes no messages, queue actions, model, effort,
  context-size or mode changes. You can still rename a settled Task. Stop a
  running turn, answer what waits for you, and send or clear the queue before
  you settle or archive.

  A Task whose conversation is not open, such as a settled, archived or
  closed Task, or any Task after the service restarts, still shows its whole
  conversation. UAM reads it from Copilot's record without opening it: it
  sends nothing and changes nothing, and Copilot records nothing. The
  conversation appears after a moment; when it cannot be read, the Task
  says why and stays as it was. UAM keeps a conversation read this way in
  memory for up to 10 idle minutes, with a total memory budget that can evict
  older views sooner. Large histories show their newest retained part and a
  truncation note.
- **Previous sessions**: each Project can list the Copilot sessions started
  in exactly its directory outside the web interface, for example with
  `copilot` in a terminal: newest first, at
  most 100, with their title and date, leaving out the ones that are already
  Tasks. A session appears once it has a first message. Importing one makes
  it a Task with its whole conversation; nothing is sent. The Task keeps the
  session's model when Copilot still offers it, otherwise it takes the
  defaults for new tasks from Settings, and it takes their mode. It starts closed: your
  next message opens the session. Import is offered when the installed
  Copilot CLI supports both history reading and in-use detection.
- **Another program using the session**: a session that another program has
  open, such as a terminal `copilot --resume`, is
  marked in use and cannot be imported. Before every message, command, steer,
  and queued message of an imported or terminal-linked
  Task, UAM checks that no other program has the session open, and refuses while one does; a queued
  message then waits and the queue pauses. The check is not a lock: a
  program that opens the session after it, for example during the turn, is
  not caught, and both then write to the same session without an error.
  While UAM has a session open, `copilot --resume` in a terminal warns that
  it is in use. Deleting a Task leaves Copilot's conversation and any saved
  legacy terminal record intact. UAM no longer probes old terminal hosts or
  shows a terminal-host badge.
- **Older history**: scrolling up in a Task, or in an open subagent, keeps
  loading until its first recorded message, even when the service keeps only
  the newest part in memory. Older pages are read from Copilot's record on
  demand. The first such read of a long Task can take a second or two; later
  pages come from the service's cache. On an iPhone or iPad a loaded page
  appears once scrolling stops, so the view never moves under your finger. These pages never change, so this
  browser also keeps the ones you have read in its own storage (IndexedDB),
  and reading them again, even after a reload or a service restart, needs no
  request. That cache holds at most 64 MiB (less when the browser gives this
  site little space) and 16 MiB per Task, drops the least recently read Tasks
  first, and drops a Task not read here for 14 days. Signing out, or losing
  the sign-in, clears it; deleting a Task removes that Task's pages. Newer
  messages and item details always come from the service. Long tool output
  and thinking shortened in the transcript show whole when expanded or
  copied. A message too long to keep whole shows its start and "Show full
  message", which reads the whole text and shows it as plain text; copying
  it copies the whole text either way. "Earlier history was truncated"
  appears only when the record itself is gone or the provider cannot page
  it.
- **Changes**: the "Changes" button in the Task header shows how many files
  this Task changed and opens them beside the conversation with the diff (a
  full-screen sheet on a narrow window). Three scopes sit at the top, each
  with its file count: **This task** (the default) lists the files this
  Task's agent or its subagents edited with an edit tool (create, write,
  edit or a patch; one that failed is left out), compared with `HEAD`;
  **Last turn** narrows that to the files edited since the latest prompt
  you sent (a steer joins the running turn and does not start a new one); **All changes** is every uncommitted change in the working tree,
  from any Task or source. A file this Task edited shows everything that
  differs from `HEAD`, including changes something else made to it; a file
  changed back or committed drops out. Files written by shell commands are
  not attributed to the Task. Until a Task's history has been read, edits
  from before it may be missing, and the panel says so. Each file shows its
  status and `+added −removed` lines. Files that deserve a closer look come
  first with a short reason: CI workflows, lockfiles, Dockerfiles,
  migrations, deploy files, and sign-in, security or secrets code. A note
  appears when the scope changes more than about 400 lines. Tick **Viewed**
  on a file (in the list or above its diff) to mark it reviewed; the mark
  clears and the row says "Changed since you viewed" when the file changes
  again. Select a diff line (or its line number) to add a comment; comments
  collect at the bottom of the panel, and **Send N comments to the task**
  sends them as one message, at once to an idle Task or after the running
  turn. Viewed marks and unsent comments are kept in this browser per Task
  and survive a reload; a settled or archived Task takes no comments. On a
  wide window this panel can be
  resized by dragging their inner edge (the handle also takes the arrow
  keys; double-click resets); the width is kept per browser.
  An open panel on the active, visible Task refreshes its selected scope
  every five seconds and when the browser tab becomes visible again. The
  previous diff remains visible while an update loads. Closing or hiding the
  panel cancels its reads; manual Refresh remains available.
- **Commit, Push and Pull**: under the file list, the Changes panel shows the
  branch, how many commits it is ahead of or behind its upstream (as last
  fetched), and Push and Pull. "Commit" opens a message box and a checkbox for
  every changed file. The boxes start on this Task's files only (those its
  edit tools touched, as the Task records them; a shell command's edits are
  not counted), never on another Task's or on a file nobody is known to have
  edited. After a restart, edits made before the Task's history was read
  again may be unknown: the panel says so and you check the files yourself.
  A note says how many changed files are left out. The message and the
  checked files are kept per Task while Changes is closed.
  Generate (then Regenerate) asks the Utility model for a message from the
  chosen files' diff and the repository's last 20 commit subjects, so it
  follows their style, Conventional Commits included. It never commits, and
  it counts toward Background AI's daily limit: while that pauses it, the
  panel says so and you write the message yourself. The
  message is yours to edit and is committed exactly as written: uam adds no
  co-author, sign-off or AI credit, and removes any the model wrote. A counter
  shows the subject's length against 72 characters. "Commit n files" stages
  exactly those files, deletions included, and commits them; files you
  staged yourself stay staged and out of the commit. The repository's hooks
  run, and a hook's refusal shows its output. A file that was staged as new
  and then deleted has nothing to commit and is left out of the commit, and
  out of the index. "Commit and push" also pushes.
  Push never forces: it pushes to the branch's upstream, or, without one, to
  `origin` under the branch's name and sets that as its upstream. A branch
  whose upstream is another local branch is not pushed: Push says there is
  no remote to go to and moves nothing. Pull only
  fast-forwards; when both sides have new commits it says so and changes
  nothing. The service runs git without a terminal, so a remote that needs a
  password, or an SSH key the service's environment cannot use, fails with
  git's message. While any Task in the same repository could still be
  writing there (mid-turn, starting, with prompts queued, or with subagents
  or background jobs still running), the panel names it and Commit, Push and
  Pull wait; while a commit, push or pull runs, a turn starting in that
  repository waits for it. So an agent's edits are never committed half
  done. After a commit or a pull, every Task's change count in that
  repository is recounted.
- **Files**: the "Files" button beside Changes opens the project directory as
  a read-only tree in the same place (a full-screen sheet on a narrow
  window). Git decides what is listed, so `.gitignore` applies; symbolic
  links and special files are left out, and a directory outside a Git working
  tree shows why instead. Each folder is read when you expand it, and nothing
  refreshes on its own: use Refresh. Choosing a file shows it below the tree:
  text highlighted and wrapped (at most its first 64 KiB), images inline, and
  anything else with Open in new tab and Download. Only one of Changes, Files,
  Subagents and a file preview is open at a time. On a wide window, a click in
  the conversation or the composer closes whichever of them is open. When the
  header runs short of room (side panels open beside the sidebar), its buttons
  drop their labels first, then Files, Charts and Terminal move into the
  header's "…" menu, so the Task's title keeps its room.
- **Terminal**: off by default. Turn on Settings → Terminal and the Task
  header shows a "Terminal" button after Files, also when the project has no
  Git and on settled and archived Tasks. It opens a shell in the project
  folder, running on the server as the user the service runs as, docked at
  the bottom of the window under the conversation (drag its top edge to
  resize). It stays open, with its shell, while you switch Tasks. Anyone signed in can
  then run commands on the machine without the agent's permission prompts;
  agents do not use it. The panel's header shows the folder and whether the
  shell is connecting, connected, exited (with its exit code) or
  disconnected; Restart starts a new shell and × closes the panel. The shell
  lives only as long as the panel: closing it, Restart, reloading the page or
  turning the setting off ends the shell and everything it runs, and there
  is no reattaching. Esc goes to the terminal, so only × (or the header's
  Terminal button) closes it; Changes, Files, Subagents and file previews
  open beside the conversation above it. While the terminal has focus, keys
  go to the shell, not to uam's shortcuts. The browser keeps a few keys for
  itself before the page sees them, such as Ctrl+W, Ctrl+T, Ctrl+N and
  Ctrl+Tab on Windows and Linux; those cannot reach the shell. The mouse
  works the clipboard as in Windows Terminal or PuTTY: releasing a mouse
  selection copies it ("Copied" shows briefly in the panel's header), and
  right-click or middle-click pastes the clipboard at the prompt instead of
  opening the browser's menu. Ctrl+Shift+C (Cmd+C on a Mac) also copies the
  selection and Ctrl+Shift+V (Cmd+V) pastes. When a program in the terminal
  uses the mouse, its clicks go to it; hold Shift to select, copy and paste.
  Click-to-paste needs the browser to let the page read the clipboard: the
  first time, it asks. Where it refuses (permission denied, a plain-HTTP
  address, or a managed browser's policy) the header says "Clipboard
  blocked. Use Ctrl+Shift+V to paste." (Cmd+V on a Mac), and keyboard paste
  still works. On a phone, touch is unchanged: a long-press never pastes. At
  most eight terminals run at once. When the service refuses one (too many,
  the folder is gone, or the setting is off) the panel says "Could not open a
  terminal." with Retry. The terminal draws with WebGL; in a browser with
  WebGL turned off the panel says so instead.
- **No Git**: when the project directory is not in a Git repository, or git
  is not installed on the server, the Task header shows a warning in place of
  Changes and Files, and turns leave out their "Changed n files" line. Once
  a turn has finish evidence, the Changes button comes back beside the
  warning to show it. Click the warning for the reason. It clears once the
  directory becomes a repository, the next time the Project is re-read.
  When git is installed, the warning offers "Set up git here", which runs
  `git init` in the project folder (only when it is in no repository) and
  brings Changes and Files back.
- **Loading indicators**: during a turn a quiet ring turns only at the Task's
  state and at the step in progress; running subagents and background shells
  show a still dot and say "Running". Request loading and busy buttons share
  the same ring. Labels and skeleton bars stay still.
  Brief request waits keep the existing 300ms delay. Motion → Match system
  makes the ring static when the OS requests reduced motion.
- **Sidebar**: all unsettled Tasks across every Project share one flat list
  without group headings. Each row's status line says whether it needs
  input, is ready for review, is running, or is idle. Tasks needing attention
  come first, followed by review, running, and idle Tasks. Collapsible
  "Settled" and "Archived" shelves remain at the foot of the list.

  "Opened since" is tracked per browser: a Task you never opened in this
  browser counts as unread once it changes after your first visit. The open
  Task is read only while the page is visible: what it does while the tab is
  in the background stays unread until you come back. Marks of deleted
  Tasks are dropped.
  A row first shows the Project badge, Project name, and a short status
  with an icon: "Input", "Starting", "Working", "Compacting", "Review",
  "Finished", "Error", "Interrupted", "Stopped", "Closed", or "Idle".
  Input and Interrupted are amber; Working/Starting blue; Compacting violet;
  Review teal; Finished green; Error red; inactive states gray.
  The second line shows the Task title, the Project branch in muted text,
  and how long ago the Task changed. Hovering the row shows the full request
  or outcome, quiet duration, branch, and `+N −M` line counts when known. Hovering an
  active row that can settle shows Settle.
  - **Alt+J / Alt+K** open the next / previous Task needing attention, wrapping
    round. They do nothing in the terminal, and in a text field where the
    keys type a character (Option+J on a Mac).
  - The tab title, the installed app's badge and the sidebar button on a
    narrow window carry the Needs you count. The tab title reads "UAM" on
    the home screen, "UAM - <Task name>" while a Task is open ("UAM - New task" for
    a new Task or one still waiting for a title), and "UAM - Settings"
    or "UAM - Routines" for those views; the count comes
    first, as in "(9) UAM - Fix redraw".

  A shelf row shows the Project badge and title, faded until hovered or
  selected; its tooltip adds the Project name and directory and when the Task
  was created, settled and archived.
  A settled or archived Task opens read-only. Search matches Task names and
  titles, Project names and branches within the chosen Project filter.
  Right-click a row, press Shift+F10 or the Menu key, or long-press on touch
  for Rename, Close conversation, Settle or Reopen, Archive and Delete.
  Arrow keys move between rows,
  Enter opens a Task and F2 renames it. There are no Project headings: the
  filter below shows one Project's Tasks.
  - **Filter**: the badge button beside Search shows the chosen Project's
    badge, or a stacked-layers icon for all Projects. It opens a list with a
    search box, "All projects" and every Project; choosing one shows only that
    Project's Tasks, shelves included, and New task starts on it. The choice
    is kept per browser. The gear at the right of each Project opens "Edit
    project", with its name, "Routines", "Previous sessions" and "Remove
    project". What new Tasks start with is in Settings → New tasks, for
    every Project.
  - **Collapse the sidebar**: the UAM mark and wordmark beside Search in the sidebar
    header, or Ctrl+B (⌘+B on a Mac), shrinks the sidebar to a narrow icon
    rail and the conversation takes the width. The rail keeps, top to bottom,
    the UAM mark alone (shows the sidebar again, with the Needs you count on it),
    New task, the Project filter, Add project, and at the foot Settings and
    the connection dot; each opens exactly what the sidebar's
    own button opens. Toggling it keeps the selected Task and its URL.
    The choice is kept per browser. On a narrow window the sidebar is a
    drawer that the button and the shortcut open and close; there is no rail.
  - With no Task open, the main pane shows the UAM mark, "What are you
    working on?", a **New task** button and, once there are Tasks,
    **Recent tasks**: up to six that are not archived, across all Projects,
    latest update first; click one to open it. Without a Project it says
    "Start with a project" and offers **Add project**. Settings → This browser →
    Theme picks Light, Dark or Match system (the default, which follows the
    OS as it changes); the choice is kept per browser.

States shown for each session:

| State | Meaning |
|---|---|
| working | The provider is running a turn |
| awaiting permission / awaiting answer | The provider is waiting for you |
| completed | The last turn finished |
| cancelled | The last turn was stopped: with Stop turn, at a routine's time limit, or by Copilot (autopilot's credit limit, a remote command or an MCP server); `stop_reason` says which |
| failed | The provider reported an error, or its process or event stream ended; the detail says which |
| interrupted | UAM stopped while a turn was running; the turn was not resumed or resent |
| closed | The conversation was closed from the web interface, or the Task was imported and has not been sent a message yet |

A prompt is sent at most once per click. Retrying after a network error reuses
the same request ID, and the service answers a repeated request ID with the
recorded result instead of sending again. If the provider may or may not have
received a prompt, the page says so and nothing is resent.

## Terminal

Settings → Terminal, off by default, adds a terminal panel that runs a login
shell in the Project's folder: your `$SHELL` if it is an absolute path to an
executable, otherwise `bash` or `sh`. The setting is kept in `sessions.json`
as `terminal` and can also be changed with `PATCH /api/settings`
`{"terminal": true}`. At most 8 terminals are open at once.

**Security warning.** While the setting is on, anyone who can sign in gets a
shell on this host as the user running `uam web`, with that user's files and
credentials. Anyone signed in could already have the agent run commands in
yolo mode, so this adds no new capability, but the shell bypasses the
agent's permission prompts and managed policy. Turn it on only if you would
hand every holder of the access token a shell. A connected instance acts with
the access key it was paired with, so anyone who can sign in to an instance
connected to this one can turn the setting on and open a shell here too; no
further confirmation is asked.

A terminal lives as long as its panel's connection. Closing the panel,
reloading the page or losing the connection hangs up the shell and the
command it runs; there is no reattach. Turning the setting off or stopping
the service closes every open terminal the same way.

## MCP servers

MCP servers give the agent extra tools: a command this host runs (stdio) or
a remote address (HTTP or SSE). Copilot keeps them in its own user-wide MCP
configuration, the same one the `copilot` command on this host uses, so a
server added in either place shows in both. uam edits that configuration
only through the Copilot runtime's API; it never writes the file itself.

**Settings → MCP servers** lists the configured servers: name, type, the
command line or address, the names of its environment variables or headers
(values show as `••••`), and a switch for whether new Tasks start it.
Servers from a plugin or built into Copilot are listed read-only, except
the built-in GitHub server, `github-mcp-server` (below). **Add
server** and **Edit** take a name, a type, and then either a command,
arguments (one per line, passed as typed with no shell) and an optional
absolute working folder, or an address. Environment variable and header
values are write-only: no response carries them, an edit shows a stored
value as `•••• set`, leaving it empty keeps it and typing replaces it. Put
keys in a header, not in the address: the address is shown to anyone signed
in, and one with a user name or password in it is refused.

**The built-in GitHub server** has its own switch on its row, and it is
uam's setting rather than Copilot's: the `copilot` command on this host is
not affected. It is off by default, also on an install that never saved it.
While it is off, every Copilot Task created or reopened leaves the server
off, and so do Utility calls. Starting it took over a second each time a
Task opened (measured on Copilot CLI 1.0.93), and a message sent meanwhile
waited for it. Turning the switch on or off also reaches open Tasks at once:
their next turn has the GitHub tools, or no longer has them. A connected
instance has its own setting, shown when that instance offers it.

**Security.** A stdio server is a command run on this host as the uam user.
Adding or editing one is therefore allowed only while Settings → Shell
access → Terminal is on (see Terminal), which already grants a shell to
anyone signed in; with it off the form offers HTTP and SSE only and the
service refuses a stdio add or edit (`403`). Turning a server on or off and
removing it need no Terminal.

**In a Task**, the actions menu (`…`) → **MCP servers…** shows each server
as that Task's conversation sees it: Connected (with its tools; tap the
count to list them), Failed (with the error and **Restart**), Needs
sign-in, Starting, or Off for this task. The switch turns a server off or on
for this Task only, until its configuration reloads, its conversation closes,
or the built-in GitHub server's setting changes. Otherwise a conversation keeps the servers it
started with: other changes in Settings reach new Tasks, and an open
Task picks them up with **Apply current settings**, which reloads its
configuration while keeping the conversation open when the runtime supports
it. Prompt and tool changes apply on the next turn. Older runtimes close
and reopen instead. The action is refused while a turn, queued prompt,
subagent or background task is running, or another client holds the
conversation. A failed reload can have applied some changes; uam reports
the failure without closing or retrying it. Opening the dialog opens the Task's
conversation if it was closed; nothing is sent to the agent.

**Signing in to a remote server.** A server that uses OAuth shows Needs
sign-in. **Sign in** asks Copilot for the provider's sign-in page and shows
a link to open it in a new tab. When the browser's host matches a configured
HTTPS `--public-origin` and the provider supports public callbacks, finish
in that tab and return to the Task. The provider SDK handles discovery,
PKCE, code exchange, credential storage and connecting the server. UAM
accepts its callback only for that Task's current conversation, for 10
minutes and once; closing or reopening the conversation invalidates it.
The dialog updates through live server status; use **Refresh** if needed.

Without a matching HTTPS public origin or provider callback support,
the existing loopback flow remains: the provider sends the browser to an
address on `127.0.0.1` or `localhost`, where Copilot waits on this host. On
the host's own desktop it finishes by itself. From another computer that
page does not load; copy the whole address from the address bar, paste it
into the dialog and choose **Finish sign-in**. UAM passes only the exact
port, path and `state` of that Task/server's pending sign-in to the waiting
listener, for 10 minutes and once, without following redirects. A supported
public-callback failure is reported and does not start another login.
Copilot keeps the sign-in for later Tasks and the `copilot` command. A
connected remote server offers **Sign in again**, which discards the kept
sign-in first. If the provider's legacy redirect is not a loopback address,
finish in that tab or run `copilot` on the host and use `/mcp`.

## Install as an app

The web interface is an installable web app: it opens in its own window with
the UAM icon, without the browser's address bar, and sits in the dock, taskbar
or home screen.

- **Desktop Chrome or Edge:** open the URL, then use the install icon at the
  right end of the address bar (or the browser menu → *Install UAM*).
- **Android (Chrome):** open the URL and choose *Install app* from the menu,
  or accept the prompt.
- **iOS and iPadOS (Safari):** open the URL, tap *Share*, then *Add to Home
  Screen*.

The URL must be a secure context: `http://127.0.0.1:<port>` and `https://`
qualify; a plain `http://` address on another host does not offer install.

Updates apply on their own. The installed app keeps no copy of the interface:
after a new `uam web` is deployed, an open window notices the new version when
its event stream reconnects (or when it comes back into view) and reloads
itself, unless a draft, an upload or an open dialog would be lost; then it
shows *UAM was updated* with a **Reload** button above the pane and waits.

There is no offline mode. The app is a live view of the service and needs it
reachable, exactly as a browser tab does.

## Notifications

Settings → This browser → **Notify me when a Task needs me or finishes**
turns notifications on for the browser you are using. The browser asks for
permission when you turn the switch on, and at no other time.

A notification is sent once each time a Task:

- asks you a question: "<Task name> needs you: <question>"
- needs your permission: "<Task name> needs you: <request>"
- fails: "<Task name> failed"
- finishes a turn, with no subagent or background shell still running:
  "<Task name> finished"

The Task you are looking at on a visible page is never announced, on any of
your devices: each tab tells the service which Task it shows while it is
visible (`POST /api/viewing`), and the service sends no push for that Task.
A Task you open while its notification waits for the push is not announced
either. With several tabs open you still get one notification. Clicking it brings UAM to
the front and opens that Task. A newer notification for the same Task
replaces the older one. A notification holds the Task's name and the
question or request title, and nothing else from the conversation.

**Delivery.** Where the browser supports Web Push (desktop Chrome, Edge and
Firefox, Android, and the iPhone and iPad Home Screen app), notifications
arrive even when no UAM tab is open. The service sends them through the
browser's push service (Google, Mozilla or Apple), so it needs outbound
HTTPS access to that service. It uses the usual `HTTPS_PROXY` variables. An
undelivered notification expires after 30 minutes. If push cannot be set up,
the switch's help says notifications show only while UAM is open in a tab.
An open tab also shows a notification itself when the push has not arrived
within a few seconds. A browser or organisation policy that blocks
notifications leaves the switch off, with a note on how to allow them.

**iPhone and iPad** (iOS and iPadOS 16.4 or later): notifications work only
in the Home Screen app, not in a Safari tab.

1. In Safari, open UAM, tap *Share*, then *Add to Home Screen*.
2. Open UAM from the Home Screen and sign in.
3. Settings → This browser → turn on **Notify me when a Task needs me or
   finishes**, then tap *Allow*.

iOS notifications have no action buttons and no reply field. Tap one to open
the Task. Browsers expect every push to show a notification (Safari revokes
the subscription otherwise, Chrome shows its own notice), so every push is
shown; the service simply sends none for the Task you have open. While no
UAM page is visible, each push also sets the app icon's badge to the Task
list's **Needs you** count. Failed or interrupted Tasks count until any
browser opens them, because the service does not know which browser has
read them; that mark survives a restart, and a Task whose turn a restart
interrupted counts too. A visible page keeps the badge at its own count,
with this browser's read marks.

**Storage.** The first browser to turn notifications on makes the service
generate its Web Push (VAPID) key pair. The keys and the subscribed
browsers are stored in `~/.config/uam/web-push.json` (mode 0600, or
`$UAM_CONFIG_DIR/web-push.json`). The service forgets a subscription when the push service
reports it gone, and keeps at most 20 browsers. Turning the switch off
removes this browser's subscription. If `web-push.json` is deleted, a new key
pair is made, and each browser subscribes again the next time UAM is opened
there.

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

## Restart onto a new version

UAM never downloads itself. Install a new version the usual way, while the
service runs: `go install` (directly or through a Go module proxy such as
Nexus or Artifactory), `make install`, or a package manager. The service
resolves the path of its binary once, when it starts (symlinks followed), and
looks at that file every 30 seconds and when Settings opens: when its size,
modification time or inode changed, it runs `<binary> version` (10 second
limit, only `PATH` in the environment). A version other than the running one
shows in Settings → General → **UAM** as "UAM <installed> is installed
(running <running>)" with **Restart**, and the Settings gear and the General
tab carry an amber dot. A binary that is missing or whose `version` fails
shows the reason instead, and offers no restart. Nothing restarts until
**Restart** is chosen.

Restart waits until no Task works or waits for anything (a turn, an answer,
queued prompts, subagents or background tasks); meanwhile Settings says
"Restarts when no task is working or waiting." Then the service stops the
way `uam web stop` does, with nothing running, and runs the installed binary
in the same process: the PID, the flags (`--listen`, `--public-origin`,
`--log-headers`), the environment and the access token stay, and signed-in
browsers stay signed in. An open page reconnects and reloads onto the new
version (or offers Reload while it holds a draft or a popup). If the new
binary cannot be started, the service logs why and exits; run `uam web`
again. `GET /api/service` returns `{"running": "v0.15.4", "installed":
"v0.15.5", "restart": "available"}` (`restart` is `none`, `available`,
`pending` or `restarting`, with `error` when the binary cannot be read), and
`POST /api/service/restart` asks for the restart; `/api/meta` carries the
same status as `service`. With connected instances, Settings restarts the
instance on screen.

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
  Warning: listening on 0.0.0.0:8260, so other machines can reach this service; sign-in is required
```

Other machines that can reach the port open `http://<host IP>:8260/` and sign
in with the access token. The URL printed for this host uses loopback on the
same port (`0.0.0.0` becomes `127.0.0.1`, `::` becomes `::1`); `uam web
status` shows the same URL, the listen address and the warning.

The connection is plain HTTP, so the token and the session cookie cross the
network unencrypted. Use it only on a network you trust; otherwise use SSH
forwarding or an HTTPS reverse proxy.

Any host name works, such as a LAN name, without
`--public-origin`. The cross-origin and JSON checks do not change.

## Log request headers

For debugging, `uam web --log-headers` logs one line per HTTP request: the
method, the path with its query, the remote address, the `Host`, every request
header, and whether the request was allowed or refused (`cross-origin request rejected`, the JSON content-type refusal, or
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

The service can sit behind a reverse proxy on the same host. It accepts the
proxy's host name as is. Optionally record the public origin in the service
settings and CLI output:

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
private and rotate it if it leaks. See [Sign in](#sign-in).

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
- **Retired UAM terminal sessions.** Saved terminal records and profiles stay
  on disk but have no controls in current UAM. Use the older binary to stop
  existing terminal hosts before replacing it. Copilot conversations can
  still be imported as Tasks after the in-use check.
- **Removed planner.** The planner was removed
  ([ADR 0007](adr/0007-remove-the-planner.md)). At startup uam deletes its
  `board.db` (with `board.db-wal` and `board.db-shm`) and the lane worktrees
  under `lanes/` beside `sessions.json`, and each lane's repository forgets
  its worktree. The `uam-plan-*` branches it created in a Project's
  repository stay: list them with `git branch --list 'uam-plan-*'` and, after
  checking that nothing on a branch is still needed, remove it with
  `git branch -D <branch>`.
- **Copilot sessions without a prompt.** Copilot saves a conversation only
  after its first message. A session created without a prompt cannot be
  reopened after the service restarts.
- **Diagrams.** Only fenced ` ```mermaid ` blocks render; other diagram
  languages stay code. Mermaid draws with the interface font (Figtree, embedded
  in each diagram), and a diagram wider than the pane is scaled down; open it
  for full size. The first diagram on a page fetches a 3.4 MB script, once.
- **Late steers (Copilot).** A steer that arrives while Copilot writes the
  last reply of a turn is answered right after that reply, in the same turn.
  Steered messages look like any other message. One that arrives
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
- **Detached shells (Copilot).** While a shell Copilot started detached
  is running, Copilot CLI 1.0.92 and 1.0.93 report it in a form the
  Copilot SDK cannot read (still so in SDK 1.0.17), so the Task's
  background tasks cannot be read either. The chip says Status unavailable
  and lists the shells reported before, or does not appear when none were;
  nothing can be stopped from it. The shell shows once it ends. Until
  then, a subagent's follow-up and an autopilot turn waiting on an
  attached shell may also stay running.
- **Copilot session diffs.** Copilot does not report per-conversation file
  changes, so UAM attributes files to a Task from its edit tool calls (see
  Changes); files written by shell commands show only under All changes.
- **Opening a web conversation elsewhere at the same time.** Do not
  continue a web session's conversation in Copilot's own terminal UI while
  the web interface has it open.
- **Commands started by a provider that crashes.** Copilot runs shell
  commands in separate process sessions. If the provider process is
  killed abruptly, a command it had already started may keep running until it
  finishes; UAM does not track or stop it.
- **Older uam binaries** do not know about web sessions and show them as
  ordinary stopped sessions.

## Token usage and estimated cost

The Usage button beside Settings opens a popover with Today,
7 days, 30 days, and Lifetime totals. Each model shows input, output, cache,
and estimated USD cost. Cache is read plus write tokens; each model's info
tip splits it into read and written. Input already includes cache. Totals
preserve the harness's reported accounting; reasoning already included in
output is counted once. The 7-day and 30-day windows include today and use
the service's local dates.

UAM records SDK-reported Copilot task, subagent, and Background AI usage.
The embedded `aiusage-core` library also reads local harness records,
including Claude Code, Codex, OpenCode, and external Copilot sessions: every
15 minutes, and when the Usage popover or token costs are read, at most once a
minute. The popover first shows the last reading and its age.
No aiusage CLI installation or provider billing API is needed. Rows identify
the harness, even when several harnesses use the same model.

SDK counts remain authoritative while UAM owns a Copilot session. Ownership
intervals are saved before inference and closed on disconnect, so replayed
OTEL does not count the same call twice and later terminal usage can count.
Existing daily totals remain in `web-token-usage.json`; local harness records
and collection checkpoints live in `web-token-usage.db` beside it. Both survive
task removal and have no retention cutoff. A model call's counts, in these
totals and on the turn's timing in `sessions.json`, are saved with the turn's
end or the Task's next change, at the latest 30 seconds after the call, so a
crash loses at most that much. Since older daily totals lack call
identities, external Copilot history is imported only after a saved one-time
cutover. Other harnesses can contribute older local history. After an unclean
shutdown, an ownership interval without a known end is excluded through
recovery and marked as incomplete coverage. Copilot telemetry without a
usable session identity is also omitted and marked incomplete.

If any `OTEL_*` or `COPILOT_OTEL_*` environment configuration exists, UAM leaves
it untouched, including explicit disable flags, exporter destinations, and
content capture settings. Otherwise UAM enables a private local file export
for its Copilot child process under `~/.copilot/otel/`, with message content
capture off. This does not change the user's shell or separately launched
Copilot. A remote-only exporter supplies no local history to aiusage-core;
UAM's own SDK usage remains available. The usage database omits raw payloads,
activity, turn-context, and code-change records even when the user's exporter
captures content. User-configured telemetry files retain their own content
and retention settings.

Lifetime means all recorded usage, not a provider account's complete history.
The popover distinguishes collection failures from zero usage. Prompt-length
estimates are excluded. The open popover refreshes every 15 seconds.

Settings → Models → Token costs lets you set prices for a model, including
models missing from the bundled catalog. Prices are USD per million tokens.
Input and Output are required; Cache read and Cache write are optional. An
absent cache price uses the Input rate; an explicit zero is free. Removing a
manual override restores the bundled price, or marks the model Unpriced if
none exists. Costs use current base rates for every period, excluding tier,
batch, priority, tool and other non-token charges. They are estimates, not
invoices or Copilot credit balances. When no token rate is available, a
source-reported cost is retained, including harnesses that report costs without
tokens. Totals identify unpriced models and show a partial cost when only some
usage has prices.

The build embeds a pinned, MIT-licensed LiteLLM token-price snapshot. It needs
no runtime network fetch or LiteLLM dependency. To refresh it before a build,
run `python3 scripts/update-model-prices.py <full BerriAI/litellm commit SHA>`
and review `internal/web/model-prices.json` and `model-prices.LICENSE`.
Pricing matches exact IDs, plus Copilot's dotted Claude version spelling.
Custom endpoint prefixes are retained so unrelated provider prices are not
silently assigned to them.
