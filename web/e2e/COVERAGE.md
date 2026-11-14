# Browser regression coverage

One row per user-visible behaviour of `uam web`. Component tests are the
happy-dom suites in `web/packages/ui/tests` (and Go tests where named); status
is `spec: <file>` for a case in this directory, `planned`, or `not feasible`
with the reason. A fix for a `uam web` bug adds or extends a spec here and
updates its row (docs/testing.md, "Browser regression pack").

IDs: NAV shell and navigation, CMP composer, TRN transcript, ACT activity and
approvals, WSP workspace (changes, git, terminal, GitLab), SET settings.

| ID | Behaviour | Component tests | Status |
|---|---|---|---|
| NAV-01 | Signing in with the printed token opens the app; a wrong token says "That token was not accepted."; the session… | login.test.tsx, web-ui-fixes.test.tsx, sign_in_limit_test.go | spec: sign-in.spec.ts |
| NAV-02 | Settings → UAM and access → Log out ends the session on the server, also after a restart | settings.test.tsx, web-ui-fixes.test.tsx, revoked_sessions_test.go | planned |
| NAV-03 | A session that ends while a view is open (token changed + restart) drops to the sign-in screen titled "UAM" | web-ui-fixes.test.tsx, federation.test.tsx | planned |
| NAV-04 | Sign-in is rate limited: past 10 failed attempts per client in 10 min the page says "Too many sign-in attempts…" | login.test.tsx, sign_in_limit_test.go | planned |
| NAV-05 | Add project: folder picker browses the real host (home first, git/symlink marks, Show hidden, New folder, typed… | projects.test.tsx, folders.test.mjs, dirs_test.go | spec: first-thread.spec.ts |
| NAV-06 | A folder with more than 1,000 subfolders lists 1,000; Filter folders asks the server and finds the rest | projects.test.tsx, dirs_test.go | planned |
| NAV-07 | Adding a directory that already has a Project points to the existing one; a relative path is refused with the reason | projects.test.tsx | planned |
| NAV-08 | Edit project (gear in the Project filter) renames the Project; the sidebar rows show the new name, kept after reload | projects.test.tsx | planned |
| NAV-09 | Remove project is refused while any of its Threads is not archived, and allowed once all are archived | projects.test.tsx | planned |
| NAV-10 | Previous sessions lists Copilot sessions started in the Project's directory outside uam; Import makes a closed… | projects.test.tsx, import_test.go | planned (needs a CLI session made outside uam) |
| NAV-11 | New thread (pen / Alt+N) opens the Project palette (single Project skips it), opens an empty draft; the Thread… | app.test.tsx, home.test.tsx | spec: first-thread.spec.ts |
| NAV-12 | A new Thread takes its first message as title at once, then the Utility model's title replaces it; "Waiting for a… | thread-title.test.tsx, brand-title.test.tsx | spec: first-thread.spec.ts |
| NAV-13 | Rename a Thread in place (row menu, F2, double-click, header menu); the name survives reload; focus returns to the row | app.test.tsx | planned |
| NAV-14 | Sidebar: one flat list of unsettled Threads across Projects, newest created first whatever their state; Project… | needs-you.test.tsx, projects.test.tsx | planned |
| NAV-15 | Each row's status follows the real turn: Working → Needs permission → Working → Finished (and Error, Stopped); the… | needs-you.test.tsx, sidebar-transition.test.tsx | spec: shell-stage.spec.ts |
| NAV-16 | A Thread that finishes while another Thread is open (or the tab is hidden) shows as unread (teal, eye) until opened;… | needs-you.test.tsx | planned |
| NAV-17 | Needs you count shows in the tab title "(N) UAM - <name>", on the sidebar toggle and on Home; Alt+J / Alt+K walk the… | brand-title.test.tsx, needs-you.test.tsx, home.test.tsx | planned |
| NAV-18 | The row shows the folder's unsaved git work ("✎N ↑M") read from the real repo at page load and when a turn ends | unsaved-work.test.tsx | planned |
| NAV-19 | Settle a clean, idle Thread from the row hover or header menu: it moves to the Settled shelf, opens read-only;… | app.test.tsx, sidebar-transition.test.tsx | spec: shell-stage.spec.ts |
| NAV-20 | Settle/Archive are refused while a turn runs, with one reason ("Stop the turn, answer what is waiting…") | app.test.tsx | spec: shell-stage.spec.ts |
| NAV-21 | Settle/Archive with the Thread's own unsaved files opens one anchored popover ("Settle with unsaved work?" /… | settle-guard.test.tsx, archive_test.go | spec: shell-stage.spec.ts |
| NAV-22 | Archive → read-only; Settings → Archived lists it newest first; Unarchive restores it as Finished without a turn;… | archived-settings.test.tsx, app.test.tsx | spec: shell-stage.spec.ts |
| NAV-23 | Settled shelf: collapsible, newest ten then Show all, scrolls within 40% of height; settling while closed flashes… | sidebar-transition.test.tsx, web-ui-fixes.test.tsx, app.test.tsx | planned |
| NAV-24 | A Thread quiet for 7 days settles itself (hourly check), unless it has unsaved work, a pending request or is open | auto_settle_test.go | planned (needs a clock or sweep trigger) |
| NAV-25 | A worktree Thread whose branch landed on origin's default branch settles itself and its worktree/branch are removed | Go worktree/auto-settle tests | planned (needs a clock or sweep trigger) |
| NAV-26 | Search threads matches names, titles, Project names and branches, archived included | projects.test.tsx, archived-settings.test.tsx, app.test.tsx | planned |
| NAV-27 | From 3 characters, search adds "In conversations": Threads whose prompts/replies hold every word, read from the… | conversation-search.test.tsx, search_test.go | planned |
| NAV-28 | Reload or a pasted link restores the same place: `#task=<id>`, `#settings`, `#routines`; Back/Forward move between… | app.test.tsx | spec: navigation.spec.ts, shell-nav.spec.ts |
| NAV-29 | Settings opens over the Thread; × or Esc brings the Thread back with its scroll position, open rows and side panels;… | app.test.tsx | planned |
| NAV-30 | Ctrl+B (or the UAM mark) collapses the sidebar to the rail; kept per browser; keeps the selected Thread and URL; the… | app.test.tsx, empty-sidebar.test.tsx | planned |
| NAV-31 | Narrow window: the sidebar is a drawer; its first rows are ready on first open; Esc closes it every time and gives… | app.test.tsx | spec: shell-nav.spec.ts |
| NAV-32 | Home with no Thread open: "What are you working on?", New thread, Needs you list, Budgets, "Nothing needs you.";… | home.test.tsx | planned |
| NAV-33 | Theme: Settings → This browser → Theme Light / Dark / Match system (default, follows the OS live); kept per browser… | theme.test.mjs, settings.test.tsx | spec: shell-nav.spec.ts |
| NAV-34 | Connection indicator: after the service restarts the page shows "Connection lost, reconnecting…", then reconnects on… | event-stream.test.mjs, app.test.tsx, restart_test.go | spec: shell-restart.spec.ts |
| NAV-35 | A turn running when uam restarts shows Interrupted afterwards; it is not resumed or resent | needs-you.test.tsx, queue_restart_test.go | spec: shell-restart.spec.ts |
| NAV-36 | Several tabs on plain HTTP keep working (live updates over one WebSocket each, not 2–3 EventSources per tab) | event-stream.test.mjs, event_socket_test.go | planned |
| NAV-37 | A stream that goes silent without closing (no heartbeat for 2.5 × 15 s) is shown as lost and reopened; live updates… | event-stream.test.mjs | planned |
| NAV-38 | After a new `uam web` build is deployed, an open page reloads itself on reconnect, or shows "UAM was updated." +… | update.test.mjs, uam-service.test.tsx | planned (needs a second build) |
| NAV-39 | Notifications: the switch asks permission only when turned on; a Thread that needs you or finishes is announced once… | notify.test.tsx, notify.test.mjs, sw.test.mjs, notify_test.go | planned |
| NAV-40 | Installable app: manifest served, service worker registers, app badge follows Needs you | sw.test.mjs, needs-you.test.tsx | planned (manifest and service worker only) |
| NAV-41 | Instance switcher scopes the list, Home and Project filter to one instance; remote deep links; Settle/Archive on… | federation.test.tsx, connected-instances-settings.test.tsx, instance-identity.test.mjs, connected-api.test.mjs | planned (needs a second instance) |
| NAV-42 | Close conversation (row or header menu, confirmed) closes the CLI session; the next message reopens it | app.test.tsx | planned |
| NAV-43 | A save the service refuses shows a strip over the pane until a save succeeds | resilience.test.tsx | planned |
| NAV-44 | With browser storage refusing reads/writes the app still renders, opens Threads and shelves | storage-refused.test.tsx | planned |
| NAV-45 | Sidebar keyboard: list is one tab stop, arrows move, Enter opens, End reaches Settled, Shift+F10 opens the row menu | app.test.tsx | planned |
| NAV-46 | Where this Thread runs (PlacePicker: Current branch / Worktree) on the draft; a worktree Thread's row shows the… | place-foot.test.tsx, needs-you.test.tsx | planned |
| CMP-01 | New thread: the first Send creates the Thread (sidebar row appears only then) and its reply streams in | app.test.tsx | spec: first-thread.spec.ts |
| CMP-02 | Enter sends on an idle Thread and clears the box; Shift+Enter adds a line; one Send button when idle | composer.test.tsx | spec: composer-send.spec.ts |
| CMP-03 | A long multi-line message grows the box up to a cap (max-h 40dvh), then scrolls inside it; Send and the toolbar stay… | none | planned |
| CMP-04 | Typing while a reply streams keeps every character and the caret; deltas do not re-render the composer | composer.test.tsx | spec: composer-send.spec.ts |
| CMP-05 | Typing a printable key on the Thread view with focus elsewhere focuses the composer and the first character lands in it | none | planned |
| CMP-06 | A Thread's unsent draft (text, file chips, finished uploads, picker settings) survives a Thread switch and a page… | composer.test.tsx, drafts.test.mjs | spec: composer-drafts.spec.ts |
| CMP-07 | A New thread pane's typed text is kept per Project after leaving the pane (reload, other Thread); attachments are… | drafts.test.mjs | planned |
| CMP-08 | Stop turn ends the running turn ("Turn stopped."), pauses the queue, and gives focus back to the textbox | composer.test.tsx | spec: composer-send.spec.ts |
| CMP-09 | While a turn runs, After this turn (Ctrl+Enter or the "More send options" menu) queues; "N waiting · Sent after this… | composer.test.tsx | spec: composer-send.spec.ts |
| CMP-10 | A queue left by a service restart is Paused; Resume sends the next, Clear drops all (confirmed) | composer.test.tsx | planned (needs a service restart) |
| CMP-11 | Send now (Enter during a turn) steers: the message shows bold italic (accepted), then normal (delivered); after Stop… | composer.test.tsx, messages.test.tsx | spec: composer-send.spec.ts |
| CMP-12 | Settings send default "After this turn" swaps what Enter and Ctrl+Enter do | composer.test.tsx | planned |
| CMP-13 | A draft whose model differs from the running turn cannot steer: Send becomes After this turn, Send now is dimmed in… | composer.test.tsx | planned |
| CMP-14 | Up on the first line recalls earlier prompts newest first; Down past the newest or Esc restores the draft | composer.test.tsx | planned |
| CMP-15 | After a failed turn, "Resend last prompt" puts the prompt (and stored uploads) back in the box | composer.test.tsx | planned |
| CMP-16 | The Model picker switches the Thread's model between turns; the next turn uses it; the choice is saved on the Thread | composer.test.tsx | planned |
| CMP-17 | Effort and context size picker offers the model's levels/sizes | composer.test.tsx | not feasible: custom models offer no effort or context levels |
| CMP-18 | Permissions and execution menu: Safe/Yolo × Interactive/Autopilot; Yolo · Autopilot asks to confirm | composer.test.tsx, assisted-mode.test.tsx | planned |
| CMP-19 | A narrow toolbar folds labels in priority order (execution word, credits, effort/context, permissions → glyphs in… | composer.test.tsx | spec: composer-layout.spec.ts |
| CMP-20 | Attach files (paper clip): chip shows progress then size/type; the file goes with the message; the transcript shows… | composer.test.tsx, app.test.tsx, attachments.test.mjs | spec: composer-drafts.spec.ts |
| CMP-21 | The service types an upload by its bytes; SVG/archives refused; image > 3 MiB, PDF > 10 MiB, text > 256 KiB, > 5 per… | attachments.test.mjs, composer.test.tsx | planned |
| CMP-22 | Pasting an image uploads it; pasting text > 8 KiB becomes `pasted-text.txt` with "Keep pasted-text.txt as text";… | composer.test.tsx | planned |
| CMP-23 | Dropping files on the composer shows an overlay with the limits line and uploads them | none | planned |
| CMP-24 | A message of only attachments sends, steers and queues; blank text beside them is not sent | composer.test.tsx, app.test.tsx | planned |
| CMP-25 | `@` lists the repository's files (untracked included) and directories, fuzzy matched; picking inserts the path | composer.test.tsx, file-references.test.mjs | planned |
| CMP-26 | `/` lists the real CLI catalogue; Up/Down/Enter/Tab/Esc; unsupported (`/share`, `/every`, `/cwd`) dimmed with… | composer.test.tsx | spec: composer-commands.spec.ts |
| CMP-27 | Control commands change Thread state: `/rename X`, `/model <id>`, `/yolo on | none | planned |
| CMP-28 | `/compact` shows Compacting, then a transcript notice | context.test.tsx | planned |
| CMP-29 | `$` lists skills only; Esc closes | composer.test.tsx | planned |
| CMP-30 | `/btw <q>`: tag "Aside · not sent to the agent"; answer in a reader over the composer; nothing added to the… | ask-aside.test.tsx | planned |
| CMP-31 | Suggested reply: after a completed turn, ghost text in the empty box; Right Arrow/End or "Use suggestion" fills it,… | assist.test.tsx, assist.test.mjs | spec: composer-commands.spec.ts |
| CMP-32 | Edit and resend: "Edit and resend…" in Turn actions fills the past prompt and stored uploads under "Editing a past… | edit-resend.test.tsx | planned |
| CMP-33 | Branch from here: picker → "Branch thread" creates "<name> (branch)", Not started, with the transcript through that… | fork.test.tsx | planned |
| CMP-34 | Credit/quota budget: set and remove in Tools → Usage; shows spent | task-usage.test.tsx | planned |
| CMP-35 | At the budget: wraps up, Send disabled with the reason and "Change budget", draft kept; 409 `budget_reached`;… | handoff.test.tsx | planned |
| CMP-36 | Tools button opens one panel with tabs (Context, Usage, Agent, Style); arrow keys switch, Esc closes; `/context`… | composer-tools.test.tsx | planned |
| CMP-37 | Context ring fills from live usage and marks the compaction threshold; Context tab lists counts | context-ring.test.tsx, context.test.tsx | planned |
| CMP-38 | Agent tab: pick a Project custom agent (quiet Thread only); lists loaded skills | composer-tools.test.tsx | planned |
| CMP-39 | Output styles: Settings library, Style tab on a new Thread, a started Thread shows its pinned style read only; the… | output-style-settings.test.tsx, composer-tools.test.tsx | planned |
| CMP-40 | Settings → New threads (model, Safe/Yolo) applies to the next New thread's pickers and its first turn | settings.test.tsx, defaults.test.mjs | planned |
| CMP-41 | Routines: create one, Run now starts a Thread with its first message (default Yolo with autopilot), card shows the… | routines.test.tsx, routines.test.mjs | spec: composer-routines.spec.ts |
| CMP-42 | "Scheduled in this Thread · N" chip lists native schedules read only | schedules.test.tsx | not feasible: web schedules are disabled |
| CMP-43 | Background tasks chip counts running shells; Output and Stop per shell | composer.test.tsx, background-output.test.tsx | planned |
| CMP-44 | Model or local line (ModelPhase) says what runs during a turn | model-phase.test.tsx | planned |
| CMP-45 | Model brand icon in the picker | none | planned |
| CMP-46 | A settled Thread's composer is one row with Reopen; Reopen focuses the box | composer.test.tsx | planned |
| CMP-47 | Phone on-screen keyboard does not hide the prompt | none | not feasible: iOS WebKit only |
| CMP-48 | A failed command catalogue load holds slash input and offers "Retry commands" | none | planned |
| TRN-01 | While a reply streams in and the reader is at the bottom, the conversation stays pinned to the newest text,… | none | spec: transcript.spec.ts |
| TRN-02 | Scrolling up while text arrives leaves the view where the reader put it | none | planned |
| TRN-03 | Replies render as Markdown while streaming (headings, lists, emphasis, blockquote, inline code) | markdown.test.mjs | spec: transcript.spec.ts (tables and code only) |
| TRN-04 | Fenced code blocks show a language header, syntax highlighting (a lazy chunk that is skipped over 100k chars) and a… | none | spec: transcript.spec.ts (no highlighting check) |
| TRN-05 | `Copy message` on a message or sign-off copies the Markdown as written and confirms with `Copied` | none | planned |
| TRN-06 | Links open outside the app | none | planned |
| TRN-07 | Unsafe model output is neutralised: raw HTML shows as text, HTML comments are hidden, and `javascript:` URLs are… | none | planned |
| TRN-08 | Markdown tables fill the width | table-visual.test.mjs | spec: transcript.spec.ts (rendering only) |
| TRN-09 | Rich replies: exact Markdown shapes (alerts/callouts, label grids, stat tiles, step lists, task lists, status lists,… | none | planned |
| TRN-10 | A ```mermaid fence renders as a diagram (a `data:` SVG `img alt="Diagram"` drawn in a hidden `iframe… | diagram.test.mjs | planned |
| TRN-11 | `Edit as drawing` on a flowchart asks `Create an editable drawing?`: `Create drawing` writes `docs/*.excalidraw` and… | none | planned |
| TRN-12 | `Use selection in message` sends the selected shapes with the next message | none | planned |
| TRN-13 | The draw.io editor (Settings → Conversation → Diagram editor) at a named origin | none | planned (needs a local draw.io stand-in) |
| TRN-14 | A `uam_chart` call shows a chart card (`Chart: <title>`) with Table, Copy CSV and Pin to project | chart.test.mjs, chart-table.test.mjs | planned |
| TRN-15 | A chart with `command` runs it in the Thread folder only in Yolo mode (30 s, 1 MiB) | unsure: none found in UI tests (Go tests | planned |
| TRN-16 | Scrolling up loads older pages until the first recorded message | history-paging (7), history-window (14), | planned |
| TRN-17 | After reading old pages while new turns arrive, `Jump to bottom` drops the old window (`history_after`) and lands on… | none | planned |
| TRN-18 | History older than the service's in-memory part, or read after a service restart, comes from Copilot's record | none in UI (no test for the truncated no | planned (needs a service restart) |
| TRN-19 | Read pages are cached in IndexedDB (64 MiB, 16 MiB per Thread, 14 days), so re-reading them after a reload needs no… | historyCache.test.mjs | planned |
| TRN-20 | Reloading the page restores the conversation: messages, the finished turn's sign-off, charts, tables, diagrams and… | none | spec: transcript.spec.ts (messages only) |
| TRN-21 | Reloading while a reply streams shows the text so far, which then continues to the end with no duplicated or lost text | history-paging.test.mjs | planned |
| TRN-22 | A dropped or silent event stream reopens on its own ("reconnecting" state), and the streaming reply completes… | event-stream.test.mjs | planned |
| TRN-23 | The turn sign-off follows the reply's last words: `Took Ns · N command ›`, outcome chips, and copy plus the turn… | dom/transcript (≈10 tests), dom/turn-way | planned |
| TRN-24 | Receipts: under the last reply of an ended turn, claims that the turn's tool calls contradict are stamped… | receipts.test.mjs | planned |
| TRN-25 | Finish evidence: the Changes panel opens with `Finished — check the evidence` (Passed/Failed/Not verified), and the… | none | planned |
| TRN-26 | The Since you left line under the header summarises what happened while away | none | planned |
| TRN-27 | File references become tags with an icon (path, `path:line`, a bare name once a tool touched it, a unique partial path) | file-references.test.mjs, preview.test.mjs, markdown.test.mjs | planned |
| TRN-28 | The open preview reloads in place when a tool in the Thread edits the file (`Reload` covers other changes) | preview.test.mjs | planned |
| TRN-29 | A Markdown file preview renders the Markdown, and `Show the file as` toggles to source | none | planned |
| TRN-30 | Tool call lines: each call is one line with a status mark, a click opens full input and output (details load as you… | none | planned |
| TRN-31 | Long command output opens in a `Command output` side panel that jumps to the latest output and has `Close command… | none | planned |
| TRN-32 | A visual that throws (chart option the library rejects, a bad diagram) shows its reason in place (`The chart could… | none | planned |
| TRN-33 | A message too long to keep whole shows its start and `Show full message` (plain text) | none | planned |
| TRN-34 | Reasoning shows inline in grey, three lines at a time with `Show more`, and `Thought for Ns` once done | none | planned |
| TRN-35 | Closing Settings (or Routines) brings the Thread back at the same scroll position with open rows | none | planned |
| TRN-36 | iOS WebKit paging and anchoring workarounds: a page lands only after the touch ends and 150 ms without scroll, and… | none | not feasible: iOS WebKit only |
| ACT-01 | A tool call appears as a running step with its live output tail, then becomes a finished row with its output; the… | activity.test.mjs, state.test.mjs | planned |
| ACT-02 | A failing command shows as failed on its row while the turn still ends Finished | activity.test.mjs | planned |
| ACT-03 | Long command output opens in the command output side panel | none | planned |
| ACT-04 | A shell permission request shows a card; Allow once runs it, Deny returns a refusal to the model and the turn goes… | none | spec: activity.spec.ts |
| ACT-05 | A file-write request shows the proposed change as a diff with the path once | none | planned |
| ACT-06 | The agent's reason for a command shows in grey under the request (cut at ~300 chars) | none | planned |
| ACT-07 | "Allow for this session" appears only when Copilot offers it, and becomes the main button after two Allow once of… | none | planned |
| ACT-08 | Yolo allows each request without a card (no flash), marks it "allowed (yolo)"; switching to yolo allows the waiting… | none | planned |
| ACT-09 | Assisted: a reviewer model approves some requests; the rest wait, with the reason | none | planned (refusal path only: the reviewer is a Copilot model) |
| ACT-10 | Autopilot keeps working turn after turn until the agent completes; Stop between autopilot turns ends it; Yolo ·… | composer ("Yolo with Autopilot is confir | planned |
| ACT-11 | A single question sits on the composer with the recommended option picked; Enter/Answer or clicking it again sends;… | answer.test.mjs | spec: activity.spec.ts (typed answer) |
| ACT-12 | A "(Choose any that apply)" question ticks several options and sends them together | none | planned |
| ACT-13 | A draft typed before the question returns after it is settled; attaching while it waits says "Answer the question… | none | planned |
| ACT-14 | An MCP elicitation (fields or link) shows on the composer or as a card; cancel vs decline | none | planned (needs a mock MCP server) |
| ACT-15 | `uam_form` shows a card; a required empty field blocks the button; the pressed button and values reach the agent;… | form_test.go | spec: host-tools.spec.ts (submit) |
| ACT-16 | An invalid `uam_form` is refused and the agent is told why; nothing shows | form_test.go | planned |
| ACT-17 | `uam_surface` draws a card and later `updateDataModel` calls update it in place | surface_test.go | spec: host-tools.spec.ts |
| ACT-18 | Pressing a card button queues `[uam_surface action] …` as a short chip; the agent then updates the card; a stale… | none | spec: host-tools.spec.ts |
| ACT-19 | A deleted card leaves a "removed" tombstone; cards survive a reload/restart | none | planned |
| ACT-20 | `/plan` → "Plan ready" above composer, Thread Needs answer; Read plan; Approve plan sends the staged action; typed… | none | planned |
| ACT-21 | The live todo list on the status line: "Todo 2/7", counts per stage, the reader by In progress/Blocked/To do/Done | todos.test.mjs | planned |
| ACT-22 | A turn that changed the list keeps it on its outcome bar; "N turns" opens the todo history; both survive a reload | todos.test.mjs | planned |
| ACT-23 | Dependency rails and the graph/board view built from `todo_deps` | todo-graph.test.mjs | planned |
| ACT-24 | Subagents: chip on the turn line, live rows at the foot, a row opens the subagent's own transcript at… | subagents.test.mjs, subagent-transcript.test.mjs, state.test.mjs | planned |
| ACT-25 | A subagent waiting on permission reads "Needs permission: <tool>" and is answered like the Thread; yolo covers it | none | planned |
| ACT-26 | A subagent's stalled model request is cancelled after 3 min; a stopped subagent's tool time ends | Go adapter tests | planned |
| ACT-27 | Background shells: toolbar chip counts running shells; list with status/command; Output panel (8 KB, Load more, Jump… | none | planned |
| ACT-28 | The status line says the agent's intent and elapsed time, "Retrying, attempt 2 · HTTP 429 · rate limited" during a… | none | planned |
| ACT-29 | The strip's phase pill: "Model" + model name, "Local · no model" + tool, "Waiting on you" + what; sidebar foot… | model-phase (12), usage tests | planned |
| ACT-30 | "Needs you": sidebar row reads Needs answer/Needs permission without moving, Needs you count beside the wordmark,… | none | planned |
| ACT-31 | Stop: "Stopping…" at once, then Stopped with the reason, conversation stays open, focus back in the composer, queue… | composer (Stop… tests), interrupted-card | spec: activity.spec.ts |
| ACT-32 | Stop while a call waits on permission: the call reads as never run with "Ask again", which sends once | none | planned |
| ACT-33 | A turn whose model calls fail (HTTP 500/429 exhausted) ends Failed with the cause first; the last prompt can be put… | none | spec: activity.spec.ts (429) |
| ACT-34 | A main-agent model request with no answer is cancelled and retried with the transcript note "No answer from the… | Go adapter tests only; no web test names | planned |
| ACT-35 | Send now (steer) during a turn: bold italic when accepted, normal when delivered; Not delivered with a notice when… | none | planned |
| ACT-36 | After this turn (queue) and the paused queue: Resume / Clear after a stop or failure | none | planned |
| ACT-37 | A uam restart mid-turn leaves the Thread Interrupted; a call it cut off offers Ask again / Run again | none | planned (needs a service restart) |
| ACT-38 | The same Thread in two tabs: transcript streams in both; a permission answered in one disappears in the other; first… | none | planned |
| ACT-39 | Live updates reconnect after a dropped connection ("Connection lost, reconnecting | event-stream.test.mjs | planned |
| ACT-40 | Compaction shows on the live line; background compaction is a quiet note, not a blocking state | none | planned |
| ACT-41 | Subagent sheet from the strip: grouped by reply, state filters, reader beside the list or replacing it when narrow,… | subagents.test.mjs | planned |
| ACT-42 | Thread MCP servers task view: server state, sign-in in progress | none | planned (needs a mock MCP server) |
| ACT-43 | Custom agent picker (Agent tab) lists the Project's agents and changes only between turns | none | planned |
| ACT-44 | Settings → Background AI logs uam's Utility calls; subagent labels come from one Utility call | none | planned |
| ACT-45 | A transcript row that throws renders as one muted line; the rest stays | none | planned |
| WSP-01 | The Thread header's Changes button and the sidebar row show the number of files the Thread changed | changes.test.mjs | spec: changes.spec.ts |
| WSP-02 | While open on the active Thread, the panel rereads every 5 s and when the tab becomes visible again | changes.test.mjs | planned |
| WSP-03 | Files only Copilot's session tracking lists (shell writes) show a **Session tracking** label and have Revert disabled | none | planned |
| WSP-04 | Edits the Thread then committed stay in Changes | none | planned |
| WSP-05 | Unfolding a file card loads its diff | changes.test.mjs | planned |
| WSP-06 | A file renamed in the index (`git mv`) names its old path ("from old") and counts only the changed lines | none | planned |
| WSP-07 | A binary file, untracked included, reads `binary` in the list and "Binary file: no textual diff is available." in… | changes.test.mjs | planned |
| WSP-08 | A diff over 1 MiB (`maxDiffBytes`) shows "The diff exceeded its size limit and is unavailable." At most 1000 files… | changes.test.mjs | planned |
| WSP-09 | A deleted file shows the deleted status, its Open button is disabled, and its diff shows the removed lines | none in tsx/mjs (Go test covers deletion | planned |
| WSP-10 | The diff of a tracked file whose name contains glob characters (`*`, `[ab]`) shows only that file | none | planned |
| WSP-11 | **Revert** on a hunk reverts that hunk on disk | changes.test.mjs | planned |
| WSP-12 | **Viewed** marks and line comments are kept per Thread in this browser and survive a reload | changes.test.mjs | planned |
| WSP-13 | Files that deserve a closer look (CI workflows, lockfiles, Dockerfiles, migrations, secrets code) come first with a… | none | planned |
| WSP-14 | The panel's inner edge can be dragged to resize it (arrow keys work, double-click resets) and the width is kept per… | none | planned |
| WSP-15 | A turn's **Changed N files** chip and the reply menu's "This turn's changes" open a read-only reader with that… | none | planned |
| WSP-16 | Inline edit-tool diffs in the transcript (edit-diff-cache) render at most 400 rows until **Show all**, and a… | none | planned |
| WSP-17 | The rewind preview (reply menu "Rewind to before this prompt…") names how many prompts go and, under **Conversation… | none | planned |
| WSP-18 | Confirming **Conversation and files** restores the files on disk, removes the prompt and later turns from the… | none | planned |
| WSP-19 | **Conversation only** shortens the conversation and leaves every file as it is | none | planned |
| WSP-20 | A file edited outside after the preview makes the preview stale: it is read again and needs a new confirmation | none | planned |
| WSP-21 | Rewind is offered only while the Thread is idle, with nothing queued, waiting or running in the background | none | planned |
| WSP-22 | A lost rewind result holds the Thread ("Reread conversation", then "Release thread" after a failed reread) and… | dom/rewind (4 tests) | planned (needs fault injection) |
| WSP-23 | **Edit and resend** rewinds to before the prompt, then sends the edited text once | none | planned |
| WSP-24 | A new Thread placed in **New worktree** gets `git worktree add -b uam/<slug>` at `<repo>.worktrees/<branch slug>` | routines.test.mjs | planned |
| WSP-25 | `worktreeBaseRef` (repository `.github/copilot/settings*.json`, then the user's) and `worktreePathTemplate` (user… | none | planned |
| WSP-26 | **New branch** (`uam/<8 hex>`) and **Switch** in the Project folder | none | planned |
| WSP-27 | Settle or Archive of a Thread with its own unsaved git work asks first ("Settle…", counts, first 3 files, **Open… | none | planned |
| WSP-28 | Using a settled worktree Thread again (Reopen + message, or Run again) brings the worktree back from its branch,… | none | planned |
| WSP-29 | Branch from here on a worktree Thread makes a new worktree on a new branch at the source's commit, carrying its… | none | planned |
| WSP-30 | A worktree Thread whose branch landed on origin's default branch settles itself (`settled_by: merged`) and its… | none | planned (needs a clock or sweep trigger) |
| WSP-31 | Settings → Projects & worktrees lists worktrees biggest first with Thread, stage and unsaved state | none | planned |
| WSP-32 | Below 10% free disk, the Worktrees rail row and the Settings gear carry a warning dot | none | planned (needs a full disk) |
| WSP-33 | Settings: **Commit messages** and **Pull and merge requests** prompts (Save, Reset to default), and the… | dom/git-settings (1 test) | planned |
| WSP-34 | The commit rules reach Threads opened afterwards in their system message: no attribution by default, otherwise a… | none | planned |
| WSP-35 | For a project folder that is no git repository, the header shows "Not a Git repository" in place of Changes and Files | dom/panels ("a project without Git offer | planned |
| WSP-36 | When git is not installed, the warning reads "Git not installed" with no Set up action | none | planned (needs PATH without git) |
| WSP-37 | Terminal access gate: Settings → Server → UAM and access → Access → **Terminal** is off by default and the header… | none | planned |
| WSP-38 | Opening the terminal slides up a dock reading "Connecting…" and "Starting a shell…", then "Connected" | none | planned |
| WSP-39 | Shell lifetime: × or Restart on a connected shell asks first ("Close the terminal?" / "Restart the terminal?") | terminal.test.mjs | planned |
| WSP-40 | A terminal opened from a worktree Thread runs in the worktree | none | planned |
| WSP-41 | Dragging the dock's top edge (or using the keyboard) resizes it, and the PTY receives the new cols and rows | none | planned |
| WSP-42 | Clipboard: releasing a selection copies it ("Copied"), right- or middle-click pastes, Ctrl+Shift+C/V work, and a… | dom/terminal-clipboard (8 tests) | planned |
| WSP-43 | With focus in the terminal, keys (Esc included) go to the shell and not to uam shortcuts or the composer | none | planned |
| WSP-44 | At most 8 terminals | none | planned |
| WSP-45 | Without WebGL the dock says "The terminal needs WebGL, which this browser has turned off." | none | planned |
| WSP-46 | Settings → Accounts → GitLab shows the glab account, the instance, and the selected Project's origin, role and token… | none | planned (needs a fake glab) |
| WSP-47 | A reply ending a turn that touched GitLab items carries one chip per item in its outcome bar (`!2` draft, `Pipeline… | gitlab-refs.test.mjs | planned (needs a fake glab) |
| WSP-48 | Pressing a chip opens the GitLab side panel: MR (discussions as Markdown, head pipeline), pipeline (jobs by stage,… | none | planned (needs a fake glab) |
| WSP-49 | A failed job chip names the failure kind (**Tests failed · 2 tests**, **Runner failure · not your change**…) | none | planned (needs a fake glab) |
| WSP-50 | Running items are reread every 20 s and open MRs every minute | none | planned (needs a fake glab) |
| WSP-51 | MR review actions on the Thread's own MR: reply, resolve, draft reply, Investigate, Fix | none | planned (needs a fake glab) |
| WSP-52 | Shell rules for agent glab calls: `glab api` and `search` are refused with a replacement, lists get `-P 10`, and… | glab_hooks_test.go | planned (needs a fake glab) |
| WSP-53 | Before `git push`, committed CI changes get `glab ci lint` | none | planned (needs a fake glab) |
| WSP-54 | `glab mr create` without a description gets one from the Utility model through `--description-file` | none | planned (needs a fake glab) |
| SET-01 | The sidebar gear opens Settings over the main pane with `#settings` in the URL; × or Esc closes it and the Thread… | settings.test.tsx | planned |
| SET-02 | The rail lists the pages by group; Find a setting filters pages by words and Enter opens the first; a page mounts on… | settings.test.tsx, settings-groups.test.tsx | planned |
| SET-03 | At phone width the rail is a list showing each page's current values; opening a page moves focus to its back button… | settings.test.tsx | planned |
| SET-04 | Every service setting changed in the UI is saved at once and is still there after a reload and after a service… | settings.test.tsx | planned |
| SET-05 | A save the service refuses puts the old value back and shows "Could not save the setting: <reason>" | settings.test.tsx | planned |
| SET-06 | New threads defaults (model, effort, context size, Safe/Yolo) are saved as `task_defaults` and a new Thread starts… | settings.test.tsx | planned |
| SET-07 | The compaction threshold (50-90 % in 5s; 80 stored as unset) is saved and applies to new Threads | settings.test.tsx | planned |
| SET-08 | Conversation: the Enter-while-running default (Send now / After this turn), Suggest replies and Threads-can-message… | settings.test.tsx, settings-help.test.tsx, federation.test.tsx | planned |
| SET-09 | Custom providers: Add provider with name, base URL and API key, Load models lists what the endpoint serves (`POST… | settings.test.tsx | spec: settings.spec.ts |
| SET-10 | A saved API key is never shown again; leaving the edit form's key blank keeps it | settings.test.tsx | planned |
| SET-11 | Environment-variable key source: the name must start `UAM_BYOM_`; an unset variable shows "<VAR> is not set in the… | settings.test.tsx | planned |
| SET-12 | Load models failure (endpoint down, 401) shows "Could not load the models: <upstream status line>"; more than 500… | none found (no test text "Could not load | planned |
| SET-13 | Edit keeps the saved models selected; unchecking models asks "Remove models from this provider?"; Remove provider /… | settings.test.tsx | planned |
| SET-14 | Vision toggle: "Enable vision for <id>" is enabled only for checked models and saves `vision: true`; an image sent… | settings.test.tsx | planned |
| SET-15 | A saved custom model appears in the composer's model menu under its provider name and a Thread can switch to it… | settings.test.tsx | spec: settings.spec.ts |
| SET-16 | The service refuses invalid custom models (provider name outside letters/digits/`.`/`_`/`-`, base URL with… | none (server-side; no DOM test of this t | planned |
| SET-17 | Models: the "Show <model>" switch hides a model from the pickers; a Thread already on it keeps it and shows "hidden… | settings.test.tsx | planned |
| SET-18 | Subagent models: add, Make fallback, Remove; a subagent the agent launches on a model outside the list is moved to… | settings.test.tsx | planned |
| SET-19 | Utility model: Cheapest / None / a model; with no priced model a Thread's title runs on the Thread's own model; None… | background-ai.test.tsx, settings.test.tsx | planned |
| SET-20 | Background AI: today's calls against the daily limit, limit edit, No daily limit, 0 turns it off; Show log lists… | background-ai.test.tsx | planned |
| SET-21 | Skills: list by source with filter and count, View, Disable (renames to `.uam-disabled`) / Enable, Remove; changes… | configuration.test.tsx | planned |
| SET-22 | Agents, hooks and instructions: add/edit/view, Terminal gate for agents and hooks, conflict on a file edited elsewhere | configuration.test.tsx, configuration-ai.test.tsx | planned |
| SET-23 | Install skills with npx (`skills@1.7.0 add`) | configuration.test.tsx | not feasible: needs the network |
| SET-24 | MCP servers: cards grouped by source from Copilot's user-wide config; Add server (HTTP/SSE address, headers); header… | mcp.test.tsx | planned |
| SET-25 | A stdio MCP server can be added or edited only while Terminal is on: with it off the form offers HTTP and SSE only… | mcp.test.tsx | planned |
| SET-26 | The built-in GitHub MCP switch is off by default, is uam's setting (`github_mcp`) and reaches open Threads | mcp.test.tsx | planned (off state only: on needs GitHub sign-in) |
| SET-27 | In a Thread, … → MCP servers… shows each server's state (Connected with Show tools / N tools, Failed with error and… | mcp.test.tsx | planned (needs a mock MCP server) |
| SET-28 | Signed-out Copilot: Settings → GitHub Copilot says Signed out with Copilot's reason and Check again; a banner over… | copilot-account.test.tsx | planned |
| SET-29 | Sign in with GitHub (device code), token sign-in, sign out, Unlink, linked-account refusal, environment-token precedence | copilot-account.test.tsx | not feasible: needs a real GitHub account |
| SET-30 | Copilot CLI panel shows the installed CLI version and the newest release, or "Could not check for a newer release:… | copilot-cli.test.tsx | planned |
| SET-31 | Restart Copilot runtime: the confirmation names Threads working or waiting, they end Stopped with "Copilot runtime… | copilot-cli.test.tsx | planned |
| SET-32 | Copilot CLI update (stage, SDK handshake, install, restart when idle) | copilot-cli.test.tsx | not feasible: needs a CLI release to install |
| SET-33 | UAM and access → UAM shows the running version; with another version installed at the started binary path it shows… | uam-service.test.tsx | planned (restart needs a second build) |
| SET-34 | Terminal access gate: off by default; on adds a Terminal button to the Thread header and lets stdio MCP, skills,… | settings.test.tsx, configuration.test.tsx | planned |
| SET-35 | The Usage button beside Settings opens a popover with Today / 7 days / 30 days / Lifetime; each model shows input,… | usage.test.tsx, usage-ai.test.tsx, usage-preload.test.tsx | planned |
| SET-36 | The Usage popover fits the viewport without its own scrolling, on desktop and at phone width | none | planned |
| SET-37 | Models → Token costs: an unpriced model says so; saved input and output prices give it an estimated cost | token-pricing.test.tsx | planned |
| SET-38 | Per-Thread usage (Tools → Usage tab): this Thread's tokens and model calls, subagent rows, Refresh; a closed Thread… | task-usage.test.tsx | planned |
| SET-39 | Account quota and AI credits (allowance in Usage and the composer chip, budget in AI credits or quota %) | usage.test.tsx, usage-ai.test.tsx, task-usage.test.tsx | not feasible: custom models use no quota |
| SET-40 | Session stats: scans every journal under `COPILOT_HOME/session-state`, shows counts grouped by prefix, writes… | session-stats-settings.test.tsx | planned |
| SET-41 | Connected instances: Add instance (label, address, access key, private-address switch), a refused pairing clears the… | connected-instances-settings.test.tsx | planned (pairing needs a second instance) |
| SET-42 | draw.io: Enable waits for a server check of the origin, a failed check shows the error and keeps Enable off; enabled… | drawio-settings.test.tsx | planned (failure path only) |
| SET-43 | This browser: Theme Light / Dark / Match system and Motion are kept per browser (`localStorage` `uam.theme`) and… | settings-groups.test.tsx, theme.test.mjs | planned |
| SET-44 | Notify me when a Thread needs me or finishes (push notifications) | settings.test.tsx | not feasible: needs OS push |
| SET-45 | Log out in UAM and access ends the session and returns to sign-in | settings.test.tsx | planned |
| SET-46 | Help tips beside labels open on hover, focus and tap; disabled reasons stay inline | settings-help.test.tsx | planned |
