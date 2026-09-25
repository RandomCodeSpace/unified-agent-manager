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

The release build is pinned to Go 1.25.14. Earlier browser and provider validation used
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

Every API request needs the cookie. The cookie is bound to the `Host` it was
issued for, so the service accepts any `Host`: a website whose name resolves
to this host holds no cookie for that name and reaches only the sign-in page.
The service rejects cross-origin state changes and sends no CORS headers.

`uam web --no-auth` turns sign-in off: every request is treated as signed in,
and `uam web` and `uam web status` print `Authentication: disabled` instead of
the token. The cross-origin and JSON checks still apply, and the service
then rejects requests whose `Host` is not loopback, a configured public origin
or, when it listens beyond loopback, an IP address. It is off by
default. Behind a public reverse proxy, it lets anyone on the internet run
agents and shell commands on this host with your credentials; on an address
beyond loopback, anyone who can reach that address. To turn it back off, run
`uam web stop`, then `uam web` without the flag.

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
every other API route. With
`--no-auth` on an address others can reach, anyone who can reach the service
can browse your directory tree and create folders in it, the same exposure as
the rest of the service. Created folders are logged with their path; listings
are logged only at debug level (`UAM_DEBUG=1`).

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
  or "New task" until there is one. With a title model set on the Settings
  page, a model titles the Task from its first message a few seconds later.
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
  The strip underneath shows the Project, branch and changed-file count;
  click the count to open Changes. The toolbar wraps on phones. Type `$`
  at the start of a message to pick a skill; `/` still lists commands and
  skills, and `@` references files.
- **Copilot configuration**: Copilot Tasks load what the terminal CLI loads
  for the directory: your and the project's skills, the project's custom
  agents, custom instructions, hooks in `.github/hooks/`, and the built-in
  GitHub MCP server. Hooks run their commands without asking, as they do in
  the terminal.
- **Effort**: choose Default or one of the selected model's reported levels.
  Default leaves the choice to Copilot; it does not mean a known level such
  as medium. Effort requires an explicit model with listed levels, so it is
  unavailable for `auto` or a model without them. Change it between turns.
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
  reported cached share. Before a report, the track is empty and its popover
  says usage is unavailable. Effort and context size share one menu.
  The meter is live only: reopening a conversation, restarting the service
  or changing its selection clears it until a fresh report. Compaction or
  truncation appears as a notice in the conversation; the next usage report
  updates the meter.
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
  both rendered as markdown (never as HTML) while they stream. The agent's
  reasoning, when the provider reports it, shows inline in grey, three lines
  at a time with "Show more"; while it streams you see the latest lines, and
  "Thought for 12s" once done. Every tool call is its own line: what ran
  (the shell command, URL, file path, search pattern, query or skill), a
  mark for running, done or failed, and, in yolo mode, "auto" where UAM
  allowed it; a call you allowed or denied yourself says so on the same
  line. Click a line for the full input and output. A run of more than eight
  calls keeps the first two and the last three and folds the rest behind
  "Show N more". When the agent asks you a question, the conversation shows
  the question, its choices and your answer under "You answered", also after
  a reload or restart. An interrupted question without a recorded answer
  shows "No answer." Copilot's recorded thinking also returns after a restart.
  If you scroll up while text arrives, the view stays put
  and offers "New output".
- **Diagrams and code**: a fenced ` ```mermaid ` block in a reply renders as
  a diagram once its fence has closed, with a Diagram / Code toggle and Copy
  code in its header; clicking the diagram opens it larger. A block Mermaid
  cannot parse shows the code with a note. Code blocks with a language are
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
  question, a card appears in the conversation and the Task's row in the
  sidebar says Approval or Input. Nothing is approved
  automatically unless you turned on yolo for that Task, and questions always
  wait for you. If no browser is connected, the request waits; the first
  answer from any tab wins and later answers are refused. Once decided, the
  card goes away: the decision shows on the tool call it was for, and a
  request UAM could not tie to a call shows as one grey line where it
  happened.
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
  separately from permissions. The composer shows the runtime's objective
  status and, on expansion, reported turns, credits, limits and pause or
  completion details. Missing or stale observations say Status unavailable.
  Stop remains available between autopilot turns, disables continuation and
  pauses queued follow-ups through the existing cancellation operation. Partial
  cancellation failures remain errors rather than a claimed stopped state.
- **Background tasks**: running provider shells appear above the composer with
  their own Stop action. Stopping one shell does not stop the foreground turn.
  Stop requested means the provider accepted cancellation; the list waits for
  a reported terminal state. Unknown or read-only tasks cannot be stopped.
  Subagents retain their Stop action in the subagent panel and context menu.
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
  5 per message; the type is taken from the file's bytes, not its name. SVG,
  HEIC, audio, video, archives and everything else are refused. Images and
  PDFs need a model that accepts them: Copilot reports this per model, `auto`
  is not checked, and the button says so when the Task's model takes text
  only. Attachments go with the message, whether it is sent, queued or
  steered. In the conversation, images show as thumbnails that open
  larger on click and other files as chips that open the stored copy, also
  after a reload. UAM keeps the files outside the project (see Settle,
  archive, and delete).
- **Images from tools**: when a tool returns an image, such as a screenshot
  or Copilot's `view` of an image file, UAM keeps a copy with the Task and
  shows it as a thumbnail under that tool call, for subagents too; click it
  to see it large. Kept: png, jpeg, gif and
  webp up to 5 MiB, at most 50 per Task, one copy of each distinct image;
  the tool call notes any it left out and why. They come back after a reload
  or a restart of UAM, because Copilot records the image's bytes in its
  session. They are deleted with the Task, like attachments.
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
  - While a turn runs, Enter does what the Settings view says (steer by
    default) and Ctrl+Enter (⌘+Enter on a Mac) the other; the two buttons
    and their tooltips follow the setting. Files and attachments go with a
    steer as with any other message.
  - **History**: with the caret on the first line (or an empty message), Up
    recalls the previous prompt from this Task, newest first, as in a shell;
    Up again goes further back. Down from the last line comes forward, and
    one step past the newest brings your unsent draft back, as does Esc.
    Editing a recalled prompt makes it the new draft. Only the text changes;
    picked files and uploads stay. When a `/`, `$` or `@` list is open, Up
    and Down move through the list instead.
- **Settings**: the gear at the bottom of the sidebar opens the Settings view
  in the main pane (`#settings` in the address bar). UAM keeps the web
  interface's settings in `sessions.json`, so they apply in every browser.
  The first is what Enter does while a turn runs: steer (the default) or
  queue. A change is saved as you make it; if the service refuses it, the
  old value comes back with the reason. `GET /api/settings` returns the
  settings as `{"send_default": "steer"}`, and `PATCH /api/settings` with
  `{"send_default": "queue"}` changes them. An unknown key or value is
  refused and changes nothing. **New tasks** holds what every new Task
  starts with, in every Project: model, effort, context size (where the
  provider allows it) and Safe or Yolo mode, kept as `task_defaults`, e.g.
  `{"task_defaults": {"provider": "copilot", "model": "gpt-5-mini",
  "effort": "high", "context_size": "default", "mode": "safe"}}`; the
  service checks them as it checks a Task's selection. Until they are set,
  a new Task starts with the provider's own defaults. Defaults that older
  versions kept per Project are adopted from the newest Project that had
  them the first time the service loads the store. `hidden_models`, e.g.
  `{"hidden_models": {"copilot": ["gpt-5-mini"]}}`, lists the models not
  offered in the pickers; a `PATCH` replaces the list of each provider it
  names, and an empty list shows all of that provider's models again. At
  least one model the provider lists must stay visible, and a provider can
  hide at most 200 IDs of up to 128 bytes each. An ID the provider no longer
  lists is kept. Hiding is only a display preference: a Task or Project
  already on a hidden model keeps it.
  The **Utility model** is the model UAM uses for its own small AI jobs,
  such as titling a new Task from its first message; pick the cheapest that
  does the job. It is kept per provider under `title_model` (the key keeps
  its first name). With no entry, UAM uses the provider's cheapest priced
  model, worked out whenever it is needed: among the models `/api/meta`
  lists with input and output prices, leaving out `auto` and models hidden
  in Settings, the lowest input plus output price per token wins, then the
  lower input price, then the lower ID. `/api/meta` gives it per provider
  as `cheapest_model`, omitted when no model is priced; then the provider
  keeps its own title, which for Copilot is the first prompt. A `PATCH`
  with a model ID, e.g. `{"title_model": {"copilot": "gpt-5-mini"}}`,
  chooses that model; `"none"` opts the provider out, so it keeps its own
  title and UAM makes no AI call; an empty ID removes the entry, back to
  the cheapest. Only a provider with the `titles` capability can have a
  model (Copilot, not OpenCode), and the model must be in its current list.
  In Settings the **Utility model** section shows "Cheapest (currently
  GPT-6 Luna)", the provider's own title (no AI), then each visible model
  with its prices. UAM asks the model in a separate short Copilot session
  with no tools and no session store, and deletes that session afterwards.
  It then shows the title, 60 characters at most, and writes it into the
  Copilot session, so `copilot --resume` shows it too. Only the first
  message counts: later messages, commands, reopened Tasks and a changed
  setting never retitle a Task. A name you type wins, even while the title
  is on its way. If the model fails or takes more than 20 s, the provider's
  title stays, and the service log says why. Each title costs AI credits,
  about 0.002 with gpt-6-luna.
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
  This is a request from the service to a URL the browser chose: on an
  instance without sign-in (`--no-auth`) anyone who reaches it can make the
  service send such a GET, with a `UAM_BYOM_` key, to any host it can reach,
  loopback and private addresses included. The key itself never passes through the browser,
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
  | Settle | an active Task | no turn running, nothing waiting for you, an empty queue | Read-only; the conversation is closed |
  | Reopen | a settled Task | – | Active again; the next message reopens the same conversation |
  | Archive | an active or settled Task | for an active Task, the same as Settle | Read-only for good; there is no unarchive |
  | Delete Task | an archived Task | – | The Task is removed from UAM |
  | Remove Project | any Project | every Task in it archived, or no Tasks | The Project and its archived Tasks are removed from UAM |

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
  `copilot` in a terminal or in a uam terminal session: newest first, at
  most 100, with their title and date, leaving out the ones that are already
  Tasks. A session appears once it has a first message. Importing one makes
  it a Task with its whole conversation; nothing is sent. The Task keeps the
  session's model when Copilot still offers it, otherwise it takes the
  defaults for new tasks from Settings, and it takes their mode. It starts closed: your
  next message opens the session. Import is offered when the installed
  Copilot CLI supports both history reading and in-use detection;
  OpenCode cannot tell when another program uses a session, so it offers no
  import.
- **Another program using the session**: a session that another program has
  open, such as a terminal `copilot --resume` or a uam terminal session, is
  marked in use and cannot be imported. Before every message, command, steer,
  queued message and subagent follow-up of an imported or terminal-linked
  Task, UAM checks that no other program has the session open, and refuses while one does; a queued
  message then waits and the queue pauses. The check is not a lock: a
  program that opens the session after it, for example during the turn, is
  not caught, and both then write to the same session without an error.
  While UAM has a session open, `copilot --resume` in a terminal warns that
  it is in use. A Task imported from a uam terminal session shows that it is
  also open in the terminal while that session runs. Deleting the Task never
  deletes the terminal session or Copilot's session.
- **Changes**: the "Changes" button in the Task header shows how many files
  differ from `HEAD` in the project directory and opens them beside the
  conversation with the diff (a full-screen sheet on a narrow window). That
  includes edits made by anything else in the working tree, not only this
  session. On a wide window this panel and the Subagents panel can be
  resized by dragging their inner edge (the handle also takes the arrow
  keys; double-click resets); the width is kept per browser.
- **Sidebar**: Tasks form one flat list with a compact card for each Task,
  followed by collapsible "Settled" and "Archived" shelves. A card shows its
  Project, state or last activity, title, provider icon and branch when known.
  A settled or archived Task opens read-only. Search matches Task names and
  titles, Project names and branches within the chosen Project filter.
  Right-click a card, press Shift+F10 or the Menu key, or long-press on touch
  for Rename, Close conversation, Settle or Reopen, Archive and Delete.
  Arrow keys move between cards,
  Enter opens a Task and F2 renames it. There are no collapsible Project
  headings.
  - **Filter**: the badge button beside Search shows the chosen Project's
    badge, or a stacked-layers icon for all Projects. It opens a list with a
    search box, "All projects" and every Project; choosing one shows only that
    Project's Tasks, shelves included, and New task starts on it. The choice
    is kept per browser. The gear at the right of each Project opens "Edit
    project", where its name and Task defaults live together with "Previous
    sessions" and "Remove project".
  - **Hide the sidebar**: the UAM icon beside Search in the sidebar header, or
    Ctrl+B (⌘+B on a Mac), hides the sidebar and the conversation takes the
    width; the same icon then sits at the start of the main pane's header.
    Toggling it keeps the selected Task and its URL.
    The choice is kept per browser. On a narrow window the sidebar is a
    drawer that the button and the shortcut open and close.
  - With no Task open, the main pane shows only the uam mark and one line;
    New task and Add project are in the sidebar. There is one theme; it does
    not follow the system.

States shown for each session:

| State | Meaning |
|---|---|
| working | The provider is running a turn |
| awaiting permission / awaiting answer | The provider is waiting for you |
| completed | The last turn finished |
| cancelled | The last turn was stopped with Stop turn |
| failed | The provider reported an error, or its process or event stream ended; the detail says which |
| interrupted | UAM stopped while a turn was running; the turn was not resumed or resent |
| closed | The conversation was closed from the web interface, or the Task was imported and has not been sent a message yet |

A prompt is sent at most once per click. Retrying after a network error reuses
the same request ID, and the service answers a repeated request ID with the
recorded result instead of sending again. If the provider may or may not have
received a prompt, the page says so and nothing is resent.

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

With sign-in on, any host name works, such as a LAN name, without
`--public-origin`. The cross-origin and JSON checks do not change.

With `--no-auth`, the `Host` check still blocks DNS rebinding: the service
accepts loopback, a configured public origin and an IP address such as the LAN
address, and any other name gets 403. To reach it by name, add that name with
`--public-origin`. Beyond that, nothing stands between the network and your
agents:

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

The service can sit behind a reverse proxy on the same host. With sign-in on,
it accepts the proxy's host name as is. With `--no-auth`, the `Host` check
refuses names that are not loopback or a configured public origin, so tell it
the public origin:

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
  Neither side takes over the other's records. The web interface can import
  the Copilot conversation of a terminal session as a Task (see Previous
  sessions); the terminal session stays the terminal's.
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
