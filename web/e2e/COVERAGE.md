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
| NAV-02 | Settings → UAM and access → Log out ends the session on the server, also after a restart | settings.test.tsx, web-ui-fixes.test.tsx, revoked_sessions_test.go | spec: sign-in.spec.ts |
| NAV-03 | A session that ends while a view is open (token changed + restart) drops to the sign-in screen titled "UAM" | web-ui-fixes.test.tsx, federation.test.tsx | spec: sign-in.spec.ts |
| NAV-04 | Sign-in is rate limited: past 10 failed attempts per client in 10 min the page says "Too many sign-in attempts…" | login.test.tsx, sign_in_limit_test.go | spec: sign-in.spec.ts |
| NAV-05 | Add project: folder picker browses the real host (home first, git/symlink marks, Show hidden, New folder, typed… | projects.test.tsx, folders.test.mjs, dirs_test.go | spec: first-thread.spec.ts |
| NAV-06 | A folder with more than 1,000 subfolders lists 1,000; Filter folders asks the server and finds the rest | projects.test.tsx, dirs_test.go | spec: nav-projects.spec.ts |
| NAV-07 | Adding a directory that already has a Project points to the existing one; a relative path is refused with the reason | projects.test.tsx | spec: nav-projects.spec.ts |
| NAV-08 | Edit project (gear in the Project filter) renames the Project; the sidebar rows show the new name, kept after reload | projects.test.tsx | spec: nav-projects.spec.ts |
| NAV-09 | Remove project is refused while any of its Threads is not archived, and allowed once all are archived | projects.test.tsx | spec: nav-projects.spec.ts |
| NAV-10 | Previous sessions lists Copilot sessions started in the Project's directory outside uam; Import makes a closed… | projects.test.tsx, import_test.go | not feasible (Previous sessions and Import are offered only while the Copilot provider is available, which needs a real GitHub sign-in; the pack runs the CLI signed out) |
| NAV-11 | New thread (pen / Alt+N) opens the Project palette (single Project skips it), opens an empty draft; the Thread… | app.test.tsx, home.test.tsx | spec: first-thread.spec.ts |
| NAV-12 | A new Thread takes its first message as title at once, then the Utility model's title replaces it; "Waiting for a… | thread-title.test.tsx, brand-title.test.tsx | spec: first-thread.spec.ts |
| NAV-13 | Rename a Thread in place (row menu, F2, double-click, header menu); the name survives reload; focus returns to the row | app.test.tsx | spec: nav-sidebar.spec.ts |
| NAV-14 | Sidebar: one flat list of unsettled Threads across Projects, latest activity first whatever their state; the Project filter narrows it and is kept per browser | needs-you.test.tsx, projects.test.tsx | spec: nav-sidebar.spec.ts |
| NAV-15 | Each row's status follows the real turn: Working → Needs permission → Working → Finished (and Error, Stopped); the… | needs-you.test.tsx, sidebar-transition.test.tsx | spec: shell-stage.spec.ts |
| NAV-16 | A Thread that finishes while another Thread is open (or the tab is hidden) shows as unread (teal, eye) until opened;… | needs-you.test.tsx | spec: nav-sidebar.spec.ts (the hidden-tab half not feasible: headless Chromium keeps every page visible) |
| NAV-17 | Needs you count shows in the tab title "(N) UAM - <name>", on the sidebar toggle and on Home; Alt+J / Alt+K walk the… | brand-title.test.tsx, needs-you.test.tsx, home.test.tsx | spec: nav-home.spec.ts |
| NAV-18 | A row shows how many files its own Thread changed ("N files changed by this thread"), never the folder's git counts, at page load and when a turn ends | unsaved-work.test.tsx | spec: nav-sidebar.spec.ts |
| NAV-19 | Settle a clean, idle Thread from the row hover or header menu: it moves to the Settled shelf, opens read-only;… | app.test.tsx, sidebar-transition.test.tsx | spec: shell-stage.spec.ts |
| NAV-20 | Settle/Archive are refused while a turn runs, with one reason ("Stop the turn, answer what is waiting…") | app.test.tsx | spec: shell-stage.spec.ts |
| NAV-21 | Settle/Archive with the Thread's own unsaved files opens one anchored popover ("Settle with unsaved work?" /… | settle-guard.test.tsx, archive_test.go | spec: shell-stage.spec.ts |
| NAV-22 | Archive → read-only; Settings → Archived lists it newest first; Unarchive restores it as Finished without a turn;… | archived-settings.test.tsx, app.test.tsx | spec: shell-stage.spec.ts |
| NAV-23 | Settled shelf: collapsible, newest ten then Show all, scrolls within 40% of height; settling while closed flashes… | sidebar-transition.test.tsx, web-ui-fixes.test.tsx, app.test.tsx | spec: nav-sidebar.spec.ts |
| NAV-24 | A Thread quiet for 7 days settles itself (hourly check), unless it has unsaved work, a pending request or is open | auto_settle_test.go | planned (needs a clock or sweep trigger: the quiet-Thread sweep runs only hourly, never at start) |
| NAV-25 | A worktree Thread whose branch landed on origin's default branch settles itself and its worktree/branch are removed | Go worktree/auto-settle tests | planned (needs a clock or sweep trigger: the merged-work sweep runs only hourly, never at start) |
| NAV-26 | Search threads matches names, titles, Project names and branches, archived included | projects.test.tsx, archived-settings.test.tsx, app.test.tsx | spec: nav-sidebar.spec.ts |
| NAV-27 | From 3 characters, search adds "In conversations": Threads whose prompts/replies hold every word, read from the… | conversation-search.test.tsx, search_test.go | spec: nav-sidebar.spec.ts |
| NAV-28 | Reload or a pasted link restores the same place: `#task=<id>`, `#settings`, `#routines`; Back/Forward move between… | app.test.tsx | spec: navigation.spec.ts, shell-nav.spec.ts |
| NAV-29 | Settings opens over the Thread (clearing the sidebar's Thread highlight meanwhile); × or Esc brings the Thread back with its scroll position, open rows and side panels;… | app.test.tsx | spec: nav-shell.spec.ts, ui-minors.spec.ts |
| NAV-30 | Ctrl+B (or the UAM mark) collapses the sidebar to the rail; kept per browser; keeps the selected Thread and URL; the… | app.test.tsx, empty-sidebar.test.tsx | spec: nav-shell.spec.ts |
| NAV-31 | Narrow window: the sidebar is a drawer; its first rows are ready on first open; Esc closes it every time and gives… | app.test.tsx | spec: shell-nav.spec.ts |
| NAV-32 | Home with no Thread open: "What are you working on?", New thread, Needs you list (inline Markdown stripped), Budgets, "Nothing needs you.";… | home.test.tsx, tasks.test.mjs | spec: nav-home.spec.ts, ui-minors.spec.ts |
| NAV-33 | Theme: Settings → This browser → Theme Light / Dark / Match system (default, follows the OS live); kept per browser… | theme.test.mjs, settings.test.tsx | spec: shell-nav.spec.ts |
| NAV-34 | Connection indicator: after the service restarts the page shows "Connection lost, reconnecting…", then reconnects on… | event-stream.test.mjs, app.test.tsx, restart_test.go | spec: shell-restart.spec.ts |
| NAV-35 | A turn running when uam restarts shows Interrupted afterwards; it is not resumed or resent | needs-you.test.tsx, queue_restart_test.go | spec: shell-restart.spec.ts |
| NAV-36 | Several tabs on plain HTTP keep working (live updates over one WebSocket each, not 2–3 EventSources per tab) | event-stream.test.mjs, event_socket_test.go | spec: nav-live.spec.ts |
| NAV-37 | A stream that goes silent without closing (no heartbeat for 2.5 × 15 s) is shown as lost and reopened; live updates… | event-stream.test.mjs | spec: nav-live.spec.ts |
| NAV-38 | After a new `uam web` build is deployed, an open page reloads itself on reconnect, or shows "UAM was updated." +… | update.test.mjs, uam-service.test.tsx | planned (needs a second build: a uam binary with another version to restart the service onto) |
| NAV-39 | Notifications: the switch asks permission only when turned on; a Thread that needs you or finishes is announced once… | notify.test.tsx, notify.test.mjs, sw.test.mjs, notify_test.go | spec: nav-live.spec.ts |
| NAV-40 | Installable app: manifest served, service worker registers, app badge follows Needs you | sw.test.mjs, needs-you.test.tsx | spec: nav-live.spec.ts, nav-home.spec.ts |
| NAV-41 | Instance switcher scopes the list, Home and Project filter to one instance; remote deep links; Settle/Archive on… | federation.test.tsx, connected-instances-settings.test.tsx, instance-identity.test.mjs, connected-api.test.mjs | planned (needs a second instance served over HTTPS, with the home service trusting its certificate: pairing refuses plain HTTP) |
| NAV-42 | Close conversation (row or header menu, confirmed) closes the CLI session; the next message reopens it | app.test.tsx | spec: nav-shell.spec.ts |
| NAV-43 | A save the service refuses shows a strip over the pane until a save succeeds | resilience.test.tsx | spec: nav-live.spec.ts |
| NAV-44 | With browser storage refusing reads/writes the app still renders, opens Threads and shelves | storage-refused.test.tsx | spec: nav-live.spec.ts |
| NAV-45 | Sidebar keyboard: list is one tab stop, arrows move, Enter opens, End reaches Settled, Shift+F10 opens the row menu | app.test.tsx | spec: nav-sidebar.spec.ts |
| NAV-46 | Where this Thread runs (Project folder / New worktree) on the draft; a worktree Thread's row shows the worktree's branch, a row on its Project folder's branch shows none (the tip names it) | place-foot.test.tsx, needs-you.test.tsx | spec: nav-sidebar.spec.ts |
| NAV-47 | A `sessions.json` that cannot be read at start: the first view says it was set aside, names the backup and how to recover, and keeps this browser's drafts | drafts.test.mjs, store_write_failure_test.go | spec: shell-state-set-aside.spec.ts |
| NAV-48 | A Thread another Thread started shows its parent as the header's first crumb; in a narrow header the crumb shrinks and truncates instead of drawing over the title | css.test.mjs | spec: shell-nav.spec.ts |
| NAV-49 | Sidebar header names its buttons in words: New thread in the bar; the Project filter (All projects or the chosen Project), Routines and Add project in one row under it, also in the phone's drawer | needs-you.test.tsx | spec: nav-sidebar.spec.ts |
| CMP-01 | New thread: the first Send creates the Thread (sidebar row appears only then) and its reply streams in | app.test.tsx | spec: first-thread.spec.ts |
| CMP-02 | Enter sends on an idle Thread and clears the box; Shift+Enter adds a line; one Send button when idle | composer.test.tsx | spec: composer-send.spec.ts |
| CMP-03 | A long multi-line message grows the box up to a cap (max-h 40dvh), then scrolls inside it; Send and the toolbar stay… | none | spec: composer-box.spec.ts |
| CMP-04 | Typing while a reply streams keeps every character and the caret; deltas do not re-render the composer | composer.test.tsx | spec: composer-send.spec.ts |
| CMP-05 | Typing a printable key on the Thread view with focus elsewhere focuses the composer and the first character lands in it | none | spec: composer-box.spec.ts |
| CMP-06 | A Thread's unsent draft (text, file chips, finished uploads, picker settings) survives a Thread switch and a page… | composer.test.tsx, drafts.test.mjs | spec: composer-drafts.spec.ts |
| CMP-07 | A New thread pane's typed text is kept per Project after leaving the pane (reload, other Thread); attachments are… | drafts.test.mjs | spec: composer-box.spec.ts |
| CMP-08 | Stop turn ends the running turn ("Turn stopped."), pauses the queue, and gives focus back to the textbox | composer.test.tsx | spec: composer-send.spec.ts |
| CMP-09 | While a turn runs, After this turn (Ctrl+Enter or the "More send options" menu) queues; "N waiting · Sent after this… | composer.test.tsx | spec: composer-send.spec.ts |
| CMP-10 | A queue left by a service restart is Paused; Resume sends the next, Clear drops all (confirmed) | composer.test.tsx | spec: composer-send.spec.ts |
| CMP-11 | Send now (Enter during a turn) steers: the message shows bold italic (accepted), then normal (delivered); after Stop… | composer.test.tsx, messages.test.tsx | spec: composer-send.spec.ts |
| CMP-12 | Settings send default "After this turn" swaps what Enter and Ctrl+Enter do | composer.test.tsx | spec: composer-box.spec.ts |
| CMP-13 | A draft whose model differs from the running turn cannot steer: Send becomes After this turn, Send now is dimmed in… | composer.test.tsx | spec: composer-models.spec.ts |
| CMP-14 | Up on the first line recalls earlier prompts newest first; Down past the newest or Esc restores the draft | composer.test.tsx | spec: composer-box.spec.ts |
| CMP-15 | After a failed turn, "Resend last prompt" puts the prompt (and stored uploads) back in the box | composer.test.tsx | spec: composer-attachments.spec.ts |
| CMP-16 | The Model picker switches the Thread's model between turns; the next turn uses it; the choice is saved on the Thread | composer.test.tsx | spec: composer-models.spec.ts |
| CMP-17 | Effort and context size picker offers the model's levels/sizes | composer.test.tsx | not feasible: custom models offer no effort or context levels |
| CMP-18 | Permissions and execution menu: Safe/Yolo × Interactive/Autopilot; Yolo · Autopilot asks to confirm | composer.test.tsx, assisted-mode.test.tsx | spec: composer-models.spec.ts |
| CMP-19 | A narrow toolbar folds labels in priority order (execution word, credits, effort/context, permissions → glyphs in… | composer.test.tsx | spec: composer-layout.spec.ts |
| CMP-20 | Attach files (paper clip): chip shows progress then size/type; the file goes with the message; the transcript shows… | composer.test.tsx, app.test.tsx, attachments.test.mjs | spec: composer-drafts.spec.ts |
| CMP-21 | The service types an upload by its bytes; SVG/archives refused; image > 3 MiB, PDF > 10 MiB, text > 256 KiB, > 5 per… | attachments.test.mjs, composer.test.tsx | spec: composer-attachments.spec.ts (the PDF limit through the API: a custom model takes no PDF) |
| CMP-22 | Pasting an image uploads it; pasting text > 8 KiB becomes `pasted-text.txt` with "Keep pasted-text.txt as text";… | composer.test.tsx | spec: composer-attachments.spec.ts |
| CMP-23 | Dragging files over the composer shows a "Drop to attach" overlay with what the model takes (the limits line when it takes every kind); dropping uploads them | none | spec: composer-attachments.spec.ts |
| CMP-24 | A message of only attachments sends, steers and queues; blank text beside them is not sent | composer.test.tsx, app.test.tsx | spec: composer-attachment-only.spec.ts |
| CMP-25 | `@` lists the repository's files (untracked included) and directories, fuzzy matched; picking inserts the path | composer.test.tsx, file-references.test.mjs | spec: composer-commands.spec.ts |
| CMP-26 | `/` lists the real CLI catalogue; Up/Down/Enter/Tab/Esc; unsupported (`/share`, `/every`, `/cwd`) dimmed with… | composer.test.tsx | spec: composer-commands.spec.ts |
| CMP-27 | Control commands change Thread state: `/rename X`, `/model <id>`, `/yolo on` and `/yolo off`; the changes are saved | none | spec: composer-commands.spec.ts |
| CMP-28 | `/compact` shows Compacting, then a transcript notice | context.test.tsx | spec: composer-commands.spec.ts |
| CMP-29 | `$` lists skills only; Esc closes | composer.test.tsx | spec: composer-commands.spec.ts |
| CMP-30 | `/btw <q>`: tag "Aside · not sent to the agent"; answer in a reader over the composer; nothing added to the… | ask-aside.test.tsx | spec: composer-commands.spec.ts |
| CMP-31 | Suggested reply: after a completed turn, ghost text in the empty box; Right Arrow/End or "Use suggestion" fills it,… | assist.test.tsx, assist.test.mjs | spec: composer-commands.spec.ts |
| CMP-32 | Edit and resend: "Edit and resend…" in Turn actions fills the past prompt and stored uploads under "Editing a past… | edit-resend.test.tsx | spec: composer-attachments.spec.ts |
| CMP-33 | Branch from here: picker → "Branch thread" creates "<name> (branch)", Idle, with the transcript through that reply; nothing is sent and the source keeps its turns | fork.test.tsx | spec: composer-send.spec.ts |
| CMP-34 | Credit/quota budget: set and remove in Tools → Usage; shows spent | task-usage.test.tsx | spec: composer-tools.spec.ts |
| CMP-35 | At the budget: wraps up, Send disabled with the reason and "Change budget", draft kept; 409 `budget_reached`;… | handoff.test.tsx | not feasible: a custom model spends no AI credits and the signed-out CLI reads no account quota, so no budget is reached |
| CMP-36 | Tools button opens one panel with tabs (Context, Usage, Agent, Style); arrow keys switch, Esc closes; `/context`… | composer-tools.test.tsx | spec: composer-tools.spec.ts (`/context` in composer-commands.spec.ts) |
| CMP-37 | Context ring fills from live usage and marks the compaction threshold; Context tab lists counts | context-ring.test.tsx, context.test.tsx | spec: composer-tools.spec.ts |
| CMP-38 | Agent tab: pick a Project custom agent (quiet Thread only); lists loaded skills | composer-tools.test.tsx | spec: composer-tools.spec.ts |
| CMP-39 | Output styles: Settings library, Style tab on a new Thread, a started Thread shows its pinned style read only; the… | output-style-settings.test.tsx, composer-tools.test.tsx | spec: composer-tools.spec.ts |
| CMP-40 | Settings → New threads (model, Safe/Yolo) applies to the next New thread's pickers and its first turn | settings.test.tsx, defaults.test.mjs | spec: settings-saving.spec.ts |
| CMP-41 | Routines: create one, Run now starts a Thread with its first message (default Yolo with autopilot), card shows the… | routines.test.tsx, routines.test.mjs | spec: composer-routines.spec.ts |
| CMP-42 | "Scheduled in this Thread · N" chip lists native schedules read only | schedules.test.tsx | not feasible: web schedules are disabled |
| CMP-43 | Background tasks chip counts running shells; Output and Stop per shell | composer.test.tsx, background-output.test.tsx | spec: activity-steps.spec.ts |
| CMP-44 | Model or local line (ModelPhase) says what runs during a turn | model-phase.test.tsx | spec: activity-steps.spec.ts (status line and Usage and AI phases) |
| CMP-45 | Model brand icon in the picker | none | spec: composer-models.spec.ts |
| CMP-46 | A settled Thread's composer is one row with Reopen and no message box; Reopen brings the box back, focused | composer.test.tsx | spec: composer-box.spec.ts |
| CMP-47 | Phone on-screen keyboard does not hide the prompt | none | not feasible: iOS WebKit only |
| CMP-48 | A failed command catalogue load holds slash input and offers "Retry commands" | none | spec: composer-commands.spec.ts |
| CMP-49 | A message queued while the service cannot save stays in the box with the error, is not queued, and queues once on retry after saving works | prompt_reserve_test.go | spec: composer-send.spec.ts |
| CMP-50 | A Copilot Fusion level the plan or a policy turns off shows in the effort menu, off, with GitHub's reason | composer.test.tsx, fusion_test.go | not feasible (Fusion's levels come from GitHub's catalog; aimock serves one model) |
| CMP-51 | Agent tab shows a Sandbox row only while the Copilot sandbox is on or turned off for the Thread; hidden when never configured, off or unknown | composer-tools.test.tsx, sandbox_test.go | not feasible (the pack's CLI runs with no sandbox configured, which shows nothing; turning it on needs a CLI sandbox setting and a host that enforces it) |
| TRN-01 | While a reply streams in and the reader is at the bottom, the conversation stays pinned to the newest text,… | none | spec: transcript.spec.ts |
| TRN-02 | Scrolling up while text arrives leaves the view where the reader put it | none | spec: transcript-scroll.spec.ts |
| TRN-03 | Replies render as Markdown while streaming (headings, lists, emphasis, blockquote, inline code) | markdown.test.mjs | spec: transcript.spec.ts, transcript-markdown.spec.ts |
| TRN-04 | Fenced code blocks show a language header, syntax highlighting (a lazy chunk that is skipped over 100k chars) and a… | none | spec: transcript.spec.ts, transcript-markdown.spec.ts |
| TRN-05 | `Copy message` on a message or sign-off copies the Markdown as written and confirms with `Copied` | none | spec: transcript-messages.spec.ts |
| TRN-06 | Links open outside the app | none | spec: transcript-messages.spec.ts |
| TRN-07 | Unsafe model output is neutralised: raw HTML shows as text, HTML comments are hidden, and `javascript:` URLs are… | none | spec: transcript-markdown.spec.ts |
| TRN-08 | Markdown tables fill the width | table-visual.test.mjs | spec: transcript.spec.ts, transcript-markdown.spec.ts |
| TRN-09 | Rich replies: exact Markdown shapes (alerts/callouts, label grids, stat tiles, step lists, task lists, status lists,… | none | spec: transcript-messages.spec.ts |
| TRN-10 | A ```mermaid flowchart opens in the React Flow view (selecting a node lights its edges; a dragged node resets with Reset layout); React Flow / Mermaid / Code toggle; the Mermaid view is a `data:` SVG `img alt="Diagram"` from the sandboxed frame; a box-and-arrow diagram Pele cannot read opens in React Flow through one Utility-model conversion per source, and one the model cannot convert falls back to Mermaid with a note | diagram.test.mjs, flowGraph.test.mjs, diagram_graph_test.go | spec: transcript-visuals.spec.ts |
| TRN-14 | A `uam_chart` call shows a chart card (`Chart: <title>`) with Table, Copy CSV and Pin to project | chart.test.mjs, chart-table.test.mjs | spec: transcript-visuals.spec.ts (Pin to project not covered) |
| TRN-15 | A chart with `command` runs it in the Thread folder only in Yolo mode (30 s, 1 MiB) | unsure: none found in UI tests (Go tests | spec: transcript-visuals.spec.ts (the 30 s limit not covered: it needs a 30 s wait) |
| TRN-16 | Scrolling up loads older pages until the first recorded message | history-paging (7), history-window (14), | spec: transcript-history.spec.ts |
| TRN-17 | After reading old pages while new turns arrive, `Jump to bottom` drops the old window (`history_after`) and lands on… | none | spec: transcript-history.spec.ts (window never drops its newest rows: that needs more than 150 rows) |
| TRN-18 | History older than the service's in-memory part, or read after a service restart, comes from Copilot's record | none in UI (no test for the truncated no | spec: transcript-history.spec.ts (after a restart; history past the in-memory part needs 2,000 items or 16 MiB of record) |
| TRN-19 | Read pages are cached in IndexedDB (64 MiB, 16 MiB per Thread, 14 days), so re-reading them after a reload needs no… | historyCache.test.mjs | planned (only archive pages are cached: it needs history past the 16 MiB the service reads back from the record or past 2,000 retained items, too slow to build in the pack) |
| TRN-20 | Reloading the page restores the conversation: messages, the finished turn's sign-off, charts, tables, diagrams and… | none | spec: transcript.spec.ts, transcript-visuals.spec.ts |
| TRN-21 | Reloading while a reply streams shows the text so far, which then continues to the end with no duplicated or lost text | history-paging.test.mjs | spec: transcript-scroll.spec.ts |
| TRN-22 | A dropped or silent event stream reopens on its own ("reconnecting" state), and the streaming reply completes… | event-stream.test.mjs | spec: transcript-scroll.spec.ts (a dropped stream; a silent one needs 37.5 s of silence) |
| TRN-23 | The turn sign-off follows the reply's last words: `Took Ns · N command ›`, outcome chips, and copy plus the turn… | dom/transcript (≈10 tests), dom/turn-way | spec: transcript-turn.spec.ts |
| TRN-24 | Receipts: under the last reply of an ended turn, claims that the turn's tool calls contradict are stamped… | receipts.test.mjs | spec: transcript-turn.spec.ts |
| TRN-25 | Finish evidence: the Changes panel opens with `Finished — check the evidence` (Passed/Failed/Not verified), and the… | none | spec: transcript-turn.spec.ts |
| TRN-26 | The Since you left line under the header summarises what happened while away | none | spec: transcript-away.spec.ts |
| TRN-27 | File references become tags with an icon (path, `path:line`, `path:12-20`, `name.ext#L10`, route names like `app/[id]/page.tsx`, a bare name once a tool touched it, a unique partial path) | file-references.test.mjs, preview.test.mjs, markdown.test.mjs, dom/file-references | spec: transcript-turn.spec.ts |
| TRN-28 | The open preview reloads in place when a tool in the Thread edits the file (`Reload` covers other changes) | preview.test.mjs | spec: transcript-preview.spec.ts |
| TRN-29 | A Markdown file preview renders the Markdown, and `Show the file as` toggles to source | none | spec: transcript-preview.spec.ts |
| TRN-30 | Tool calls are steps in the turn's timeline (the sign-off's counts open it): one line per call with its result (`ok`, `exit 2`); a click opens its input and output, three lines until `Show all N lines` | none | spec: transcript-messages.spec.ts |
| TRN-31 | Long command output opens in a `Command output` side panel that jumps to the latest output and has `Close command… | none | spec: transcript-turn.spec.ts |
| TRN-32 | A visual that throws (chart option the library rejects, a bad diagram) shows its reason in place (`The chart could… | none | spec: transcript-visuals.spec.ts (charts; a bad diagram in TRN-10) |
| TRN-33 | A message too long to keep whole shows its start and `Show full message` (plain text) | none | spec: transcript-messages.spec.ts |
| TRN-34 | Reasoning is metadata: the turn line counts it (`2 thoughts`), each thought is a `Thought` step that opens onto its text with `Copy thinking`, and none of it stands in the reply | none | spec: transcript-messages.spec.ts |
| TRN-35 | Closing Settings (or Routines) brings the Thread back at the same scroll position with open rows | none | spec: transcript-away.spec.ts |
| TRN-36 | iOS WebKit paging and anchoring workarounds: a page lands only after the touch ends and 150 ms without scroll, and… | none | not feasible: iOS WebKit only |
| TRN-37 | A Copilot Fusion turn shows its route on the working head (a Standard turn, the model it picked and why), a row per phase at the conversation's foot, and "Critic reviewing" on the strip | fusion-route.test.tsx, fusion.test.mjs | not feasible (Fusion routes on GitHub's side; aimock serves one model) |
| TRN-38 | The strip's "Agents and commands" list holds Fusion's recorded phases and the background commands with the subagents, under their message, with kind chips; a phase opens its reader with the verdict and Show in conversation | fusion-list.test.tsx, subagents.test.mjs, fusion_test.go | not feasible (Fusion routes on GitHub's side; aimock serves one model) |
| TRN-39 | A whole reply at a working turn's foot shows its alerts and rich blocks at once; a reply still arriving keeps its last block as written | reply-callout.test.tsx, stream_text_test.go | not feasible (the reply that stays last while its turn works is Copilot Fusion's; aimock serves one model) |
| TRN-40 | The conversation is a log but not a live region (streamed text is not read out again); a running tool's progress is plain text; the status line is the one announcer | app.test.tsx, transcript.test.tsx, details.test.tsx | spec: transcript-scroll.spec.ts (the log) |
| TRN-41 | `Jump to bottom` floats on the float shadow (every Tactile shadow is one tailwind-merge group, so a caller's shadow replaces the button's) | css.test.mjs | spec: transcript-scroll.spec.ts |
| TRN-42 | A cited number's definition and a receipt stamp's reason are tips on hover and keyboard focus, not native titles; a stamp with nothing to open shows its reason on a press | references-ledger.test.tsx, receipts.test.tsx | spec: references.spec.ts, transcript-turn.spec.ts |
| TRN-43 | GitLab item state polls redraw only the turn heads that cite GitLab items | gitlab-live.test.tsx | not feasible (a render count; the pack has no GitLab) |
| TRN-44 | A ```csv or ```tsv fence, and a ```json array of two or more flat objects with the same keys, render as the sortable table with `Show CSV` / `Show JSON` for the fence as written; ragged, unparsable or oversized data stays a code block | fence-table.test.mjs, fence-table.test.tsx | spec: transcript-markdown.spec.ts |
| TRN-45 | An HTML page with its own style sheet and script runs in the preview, loads again when a tool edits any file in its folder, toggles to `Source` and to a `Phone` width; its folder named without a slash opens its `index.html` | file-references.test.tsx, files_test.go | spec: transcript-preview.spec.ts |
| TRN-46 | A `uam_publish` call shows a card (title, `Version N`, `Open`); the page opens in the preview from its snapshot, loading its own files and refusing other origins'; publishing again adds a version (`Open version N`, `Versions`), the header's `Pages` opens the Thread's published pages with each version, and a version can be deleted | artifacts.test.tsx, artifacts_test.go | spec: transcript-publish.spec.ts |
| TRN-47 | A folder reference (`src/`, a link to `src/lib/`) is a chip with the folder glyph that lists the folder in the preview (git decides what is listed): a file opens in the preview, a folder lists in place, `Up to` goes back within the Thread's folder; a link out of it, through a symbolic link or to a host folder stays text | dom/file-references, markdown.test.mjs, file_resolver_test.go | spec: transcript-preview.spec.ts |
| TRN-48 | A uam tool that waits (a `uam_chart` command, `uam_publish` copying files, `uam_create_task` starting a Thread) shows its progress under its running row, gone once it ends (Copilot CLI 1.0.116+) | hosttools_test.go, charts_test.go | spec: transcript-visuals.spec.ts (`uam_chart` command) |
| ACT-01 | A tool call appears as a running step with its live output tail, then becomes a finished row with its output; the… | activity.test.mjs, state.test.mjs | spec: activity-steps.spec.ts |
| ACT-02 | A failing command shows as failed on its row while the turn still ends Finished | activity.test.mjs | spec: activity-steps.spec.ts |
| ACT-03 | Long command output opens in the command output side panel | none | spec: composer-commands.spec.ts (a native command's output; a background shell's: activity-steps.spec.ts) |
| ACT-04 | A shell permission request shows a card; Allow once runs it, Deny returns a refusal to the model and the turn goes… | none | spec: activity.spec.ts |
| ACT-05 | A file-write request shows the proposed change as a diff with the path once | none | spec: act-permissions.spec.ts |
| ACT-06 | The agent's reason for a command shows in grey under the request (cut at ~300 chars) | none | spec: act-permissions.spec.ts |
| ACT-07 | "Allow for this session" appears only when Copilot offers it, and becomes the main button after two Allow once of… | none | spec: act-permissions.spec.ts |
| ACT-08 | Yolo allows each request without a card (no flash), marks its step "auto-approved"; switching to yolo allows the waiting… | none | spec: act-permissions.spec.ts |
| ACT-09 | Assisted: a reviewer model approves some requests; the rest wait, with the reason | none | spec: act-permissions.spec.ts (refused with the reason while the reviewer model is not offered); a review itself not feasible (the reviewer is a Copilot model; the pack runs signed out on the mock) |
| ACT-10 | Autopilot keeps working turn after turn until the agent completes; Stop between autopilot turns ends it; Yolo ·… | composer ("Yolo with Autopilot is confir | spec: activity-agents.spec.ts (completion, Stop) |
| ACT-11 | A single question sits on the composer with the recommended option picked; Enter/Answer or clicking it again sends;… | answer.test.mjs | spec: activity.spec.ts (typed answer), activity-attention.spec.ts (recommended on Enter, Decline) |
| ACT-12 | A "(Choose any that apply)" question ticks several options and sends them together | none | spec: activity-attention.spec.ts |
| ACT-13 | A draft typed before the question returns after it is settled; attaching while it waits says "Answer the question… | none | spec: activity-attention.spec.ts |
| ACT-14 | An MCP elicitation (fields or link) shows on the composer or as a card; cancel vs decline | none | planned (needs a mock MCP server) |
| ACT-15 | `uam_form` shows a card; a required empty field blocks the button; the pressed button and values reach the agent;… | form_test.go | spec: host-tools.spec.ts (submit) |
| ACT-16 | An invalid `uam_form` is refused and the agent is told why; nothing shows | form_test.go | spec: host-tools.spec.ts |
| ACT-17 | `uam_surface` draws a card and later `updateDataModel` calls update it in place | surface_test.go | spec: host-tools.spec.ts |
| ACT-18 | Pressing a card button queues `[uam_surface action] …` as a short chip; the agent then updates the card; a stale… | none | spec: host-tools.spec.ts |
| ACT-19 | A deleted card leaves a "Removed by the agent" line; cards survive a reload/restart | none | spec: host-tools.spec.ts (removed line, reload), act-restart.spec.ts (restart) |
| ACT-20 | `/plan` → "Plan ready" above composer, Thread Needs answer; Read plan; Approve plan sends the staged action; typed… | none | spec: activity-agents.spec.ts (revise, approve, leave planning) |
| ACT-21 | The live todo list on the status line: "Todo 2/7", counts per stage, the reader by In progress/Blocked/To do/Done | todos.test.mjs | spec: activity-agents.spec.ts |
| ACT-22 | A turn that changed the list keeps it on its outcome bar; "N turns" opens the todo history; both survive a reload | todos.test.mjs | spec: activity-agents.spec.ts (outcome bar; history and reload planned) |
| ACT-23 | Dependency rails and the graph/board view built from `todo_deps` | todo-graph.test.mjs | spec: act-thread-tools.spec.ts |
| ACT-24 | Subagents: chip on the turn line, live rows at the foot, a row opens the subagent's own transcript at… | subagents.test.mjs, subagent-transcript.test.mjs, state.test.mjs | spec: activity-agents.spec.ts |
| ACT-25 | A subagent waiting on permission reads "Needs permission: <tool>" and is answered like the Thread; yolo covers it | none | spec: act-subagents.spec.ts |
| ACT-26 | A subagent's stalled model request is cancelled after 3 min; a stopped subagent's tool time ends | Go adapter tests | spec: act-subagents.spec.ts (stopped subagent); the stalled request planned (needs a clock: the bound is 3 min) |
| ACT-27 | Background shells: toolbar chip counts running shells; list with status/command; Output panel (8 KB, Load more, Jump…; a running shell is a row at the conversation's foot that opens its output | background-output.test.tsx | spec: activity-steps.spec.ts |
| ACT-28 | The status line says the agent's intent and elapsed time, "Retrying, attempt 2 · HTTP 429 · rate limited" during a… | none | spec: activity-steps.spec.ts (retry); intent planned (no report_intent tool in this CLI) |
| ACT-29 | The strip's phase pill: "Model" + model name, "Local · no model" + tool, "Waiting on you" + what; sidebar foot… | model-phase (12), usage tests | spec: activity-steps.spec.ts (status line and Usage and AI phases) |
| ACT-30 | "Needs you": sidebar row reads Needs answer/Needs permission without moving, Needs you count beside the wordmark,… | none | spec: activity-attention.spec.ts (row order: fixme, row moves until reload) |
| ACT-31 | Stop: "Stopping…" at once, then Stopped with the reason, conversation stays open, focus back in the composer, queue… | composer (Stop… tests), interrupted-card | spec: activity.spec.ts |
| ACT-32 | A uam restart while a call waits on permission: the call reads as never run with "Ask again", which sends once | none | spec: act-restart.spec.ts |
| ACT-33 | A turn whose model calls fail (HTTP 500/429/401) ends Error with one plain sentence, raw detail behind Details, and the real fix (Sign in again for credentials) before Resend last prompt | failed-cause.test.tsx | spec: activity.spec.ts (429), failed-thread-recovery.spec.ts (401) |
| ACT-34 | A main-agent model request with no answer is cancelled and retried with the transcript note "No answer from the… | Go adapter tests only; no web test names | spec: act-stall.spec.ts (retried once; the second stall that ends the turn is Go only) |
| ACT-35 | Send now (steer) during a turn: bold italic when accepted, normal when delivered; Not delivered with a notice when… | none | spec: act-queue.spec.ts |
| ACT-36 | After this turn (queue) and the paused queue: Resume / Clear after a stop or failure | none | spec: act-queue.spec.ts |
| ACT-37 | A uam restart mid-turn leaves the Thread Interrupted; a call it cut off offers Ask again / Run again | none | spec: act-restart.spec.ts |
| ACT-38 | The same Thread in two tabs: transcript streams in both; a permission answered in one disappears in the other; first… | none | spec: activity-attention.spec.ts |
| ACT-39 | Live updates reconnect after a dropped connection ("Connection lost, reconnecting | event-stream.test.mjs | spec: activity-attention.spec.ts (dropped socket; silent stream planned) |
| ACT-40 | Compaction shows on the status line of a working Thread; background compaction is a quiet note, not a blocking state | none | spec: act-thread-tools.spec.ts |
| ACT-41 | Subagent sheet from the strip: grouped by reply, state filters, reader beside the list or replacing it when narrow,… | subagents.test.mjs | spec: act-subagents.spec.ts |
| ACT-42 | Thread MCP servers task view: server state, sign-in in progress | none | planned (needs a mock MCP server) |
| ACT-43 | Custom agent picker (Agent tab) lists the Project's agents and changes only between turns | none | spec: act-thread-tools.spec.ts |
| ACT-44 | Settings → Background AI logs uam's Utility calls; subagent labels come from one Utility call | none | spec: act-thread-tools.spec.ts |
| ACT-45 | A transcript row that throws renders as one muted line; the rest stays | none | not feasible (no input makes a row throw in the built app; the component test replaces the renderer) |
| ACT-46 | A subagent whose Utility name says what its own name says shows the name once | none | spec: activity-agents.spec.ts |
| ACT-47 | Choice rows of a question (and a uam_surface list picker bound to nothing) look disabled and stop reacting to the pointer while an answer is on its way | interactions-card.test.tsx, surface.test.tsx | not feasible (the send window is a local request too short to observe) |
| ACT-48 | A subagent named in a reply (bold name or inline-code id) is an inline text button: a long name wraps with the words around it, Enter or a click opens the subagent | subagent-view.test.tsx | spec: act-subagents.spec.ts |
| ACT-49 | A question whose CLI advertises image support takes attached or pasted images as its answer, alone or with text; other files are left out with a note, and the service refuses images the question's limits do not allow | questions.test.tsx, attachments_test.go, web_test.go | not feasible (the Copilot CLI advertises image support only to a signed-in Copilot model; the pack runs signed out on a custom model) |
| ACT-50 | A risky shell request (delete, permissions, root, git history rewrite, file redirect, outside the Project, the CLI's warning or sandbox escape) shows one line saying why and has no main button; a harmless one has none; Deny is neutral; Allow for this session says what it allows until when | shell-risk.test.mjs, web_test.go | spec: act-permissions.spec.ts |
| ACT-51 | A waiting Thread says its state once per place: while a request shows where it is answered (its card, or the composer for one question) the header has no state label, the card has no "Needs permission" chip and the live line reads "Waiting on you" with the clock; the header label returns once answered | thread-header.test.tsx, model-phase.test.tsx, transcript.test.tsx | spec: act-status-once.spec.ts |
| ACT-52 | A running command has one clock: the live line counts a read of a background shell from the shell's start, as the shell's row does, both in whole seconds | model-phase.test.tsx | planned (needs a detached shell and a `read_bash` on it through the real CLI) |
| WSP-01 | The Thread header's Changes button and the sidebar row show the number of files the Thread changed | changes.test.mjs | spec: changes.spec.ts |
| WSP-02 | While open on the active Thread, the panel rereads every 5 s and when the tab becomes visible again | changes.test.mjs | spec: workspace-changes-files.spec.ts |
| WSP-03 | Files only Copilot's session tracking lists (shell writes) show a **Session tracking** label and have Revert disabled | none | spec: workspace-changes.spec.ts |
| WSP-04 | Edits the Thread then committed stay in Changes | none | spec: workspace-changes.spec.ts |
| WSP-05 | Unfolding a file card loads its diff | changes.test.mjs | spec: workspace-changes-files.spec.ts |
| WSP-06 | A file renamed in the index (`git mv`) names its old path ("from old") and counts only the changed lines | none | spec: workspace-changes-files.spec.ts |
| WSP-07 | A binary file, untracked included, reads `binary` in the list and "Binary file: no textual diff is available." in… | changes.test.mjs | spec: workspace-changes-files.spec.ts |
| WSP-08 | A diff over 1 MiB (`maxDiffBytes`) shows "The diff exceeded its size limit and is unavailable." At most 1000 files… | changes.test.mjs | spec: workspace-changes-files.spec.ts |
| WSP-09 | A deleted file counts its removed lines (`+0 −N`), its Open button is disabled, and its diff shows the removed lines | none in tsx/mjs (Go test covers deletion | spec: workspace-changes-files.spec.ts |
| WSP-10 | The diff of a tracked file whose name contains glob characters (`*`, `[ab]`) shows only that file | none | spec: workspace-changes-files.spec.ts |
| WSP-11 | **Revert** on a hunk reverts that hunk on disk | changes.test.mjs | spec: workspace-changes-files.spec.ts |
| WSP-12 | **Viewed** marks and line comments are kept per Thread in this browser and survive a reload | changes.test.mjs | spec: workspace-changes-files.spec.ts |
| WSP-13 | Files that deserve a closer look (CI workflows, lockfiles, Dockerfiles, migrations, secrets code) come first with a… | none | spec: workspace-changes-files.spec.ts |
| WSP-14 | The panel's inner edge can be dragged to resize it (arrow keys work, double-click resets) and the width is kept per… | none | spec: workspace-changes-files.spec.ts |
| WSP-15 | A turn's **Changed N files** chip and the reply menu's "This turn's changes" open a read-only reader with that… | none | spec: workspace-changes.spec.ts |
| WSP-16 | Inline edit-tool diffs in the transcript (edit-diff-cache) render at most 400 rows until **Show all**, and a… | none | spec: workspace-changes-files.spec.ts |
| WSP-17 | The rewind preview (reply menu "Rewind to before this prompt…") names how many prompts go and, under **Conversation… | none | spec: workspace-rewind.spec.ts |
| WSP-18 | Confirming **Conversation and files** restores the files on disk, removes the prompt and later turns from the… | none | spec: workspace-rewind.spec.ts |
| WSP-19 | **Conversation only** shortens the conversation and leaves every file as it is | none | spec: workspace-rewind.spec.ts |
| WSP-20 | A file edited outside after the preview makes the preview stale: it is read again and needs a new confirmation | none | spec: workspace-rewind.spec.ts |
| WSP-21 | Rewind is offered only while the Thread is idle, with nothing queued, waiting or running in the background | none | spec: workspace-rewind.spec.ts |
| WSP-22 | A lost rewind result holds the Thread ("Reread conversation", then "Release thread" after a failed reread) and… | dom/rewind (4 tests) | planned (needs fault injection: a rewind whose result is lost between Copilot and uam) |
| WSP-23 | **Edit and resend** rewinds to before the prompt, then sends the edited text once | none | spec: workspace-rewind.spec.ts |
| WSP-24 | A new Thread placed in **New worktree** gets `git worktree add -b uam/<slug>` at `<repo>.worktrees/<branch slug>` | routines.test.mjs | spec: workspace-worktrees.spec.ts |
| WSP-25 | `worktreeBaseRef` (repository `.github/copilot/settings*.json`, then the user's) and `worktreePathTemplate` (user… | none | spec: workspace-worktrees.spec.ts |
| WSP-26 | **New branch** (`uam/<8 hex>`) and **Switch** in the Project folder | none | spec: workspace-git-place.spec.ts |
| WSP-27 | Settle or Archive of a Thread with its own unsaved git work asks first ("Settle…", counts, first 3 files, **Open… | none | spec: workspace-worktrees.spec.ts |
| WSP-28 | Using a settled worktree Thread again (Reopen + message, or Run again) brings the worktree back from its branch,… | none | spec: workspace-worktrees.spec.ts |
| WSP-29 | Branch from here on a worktree Thread makes a new worktree on a new branch at the source's commit, carrying its… | none | spec: workspace-worktrees.spec.ts |
| WSP-30 | A worktree Thread whose branch landed on origin's default branch settles itself (`settled_by: merged`) and its… | none | planned (needs a sweep trigger or a clock: the merged-work sweep runs hourly, not at start) |
| WSP-31 | Settings → Projects & worktrees lists worktrees biggest first with Thread, stage and unsaved state | none | spec: workspace-worktrees.spec.ts |
| WSP-32 | Below 10% free disk, the Worktrees rail row and the Settings gear carry a warning dot | none | not feasible (needs a filesystem under 10% free; the pack runs on the host disk without root) |
| WSP-33 | Settings: **Commit messages** and **Pull and merge requests** prompts (Save, Reset to default), and the… | dom/git-settings (1 test) | spec: workspace-git-place.spec.ts |
| WSP-34 | The commit rules reach Threads opened afterwards in their system message: no attribution by default, otherwise a… | none | spec: workspace-git-place.spec.ts |
| WSP-35 | For a project folder that is no git repository, the header shows "Not a Git repository" in place of Changes and Files | dom/panels ("a project without Git offer | spec: workspace-git-setup.spec.ts |
| WSP-36 | When git is not installed, the warning reads "Git not installed" with no Set up action | none | not feasible (uam finds git in fixed system folders, not on PATH; the pack cannot remove /usr/bin/git) |
| WSP-37 | Terminal access gate: Settings → Server → UAM and access → Access → **Terminal** is off by default and the header… | none | spec: workspace-terminal.spec.ts |
| WSP-38 | Opening the terminal slides up a dock reading "Connecting…" and "Starting a shell…", then "Connected" | none | spec: workspace-terminal.spec.ts |
| WSP-39 | Shell lifetime: × or Restart on a connected shell asks first ("Close the terminal?" / "Restart the terminal?") | terminal.test.mjs | spec: workspace-terminal.spec.ts |
| WSP-40 | A terminal opened from a worktree Thread runs in the worktree | none | spec: workspace-terminal.spec.ts |
| WSP-41 | Dragging the dock's top edge (or using the keyboard) resizes it, and the PTY receives the new cols and rows | none | spec: workspace-terminal.spec.ts |
| WSP-42 | Clipboard: releasing a selection copies it ("Copied"), right- or middle-click pastes, Ctrl+Shift+C/V work, and a… | dom/terminal-clipboard (8 tests) | spec: workspace-terminal.spec.ts |
| WSP-43 | With focus in the terminal, keys (Esc included) go to the shell and not to uam shortcuts or the composer | none | spec: workspace-terminal.spec.ts |
| WSP-44 | At most 8 terminals | none | spec: workspace-terminal.spec.ts |
| WSP-45 | Without WebGL the dock says "The terminal needs WebGL, which this browser has turned off." | none | spec: workspace-terminal.spec.ts |
| WSP-46 | Settings → Accounts → GitLab shows the glab instance, the signed-in account, the token scopes and expiry; **Check access** reads again | none | spec: workspace-gitlab.spec.ts |
| WSP-47 | A reply ending a turn that touched GitLab items carries one chip per item in its outcome bar (`!2` draft, `Pipeline… | gitlab-refs.test.mjs | spec: workspace-gitlab.spec.ts |
| WSP-59 | A Thread opened while a turn runs reads its GitLab project at once: replies link GitLab and earlier turns show their chips before the turn ends | gitlab-live.test.tsx | spec: workspace-gitlab.spec.ts |
| WSP-48 | Pressing a chip opens the GitLab side panel: MR (discussions as Markdown, head pipeline), pipeline (jobs by stage,… | none | spec: workspace-gitlab.spec.ts |
| WSP-49 | A failed job chip names the failure kind (**Tests failed · 2 tests**, **Runner failure · not your change**…) | none | spec: workspace-gitlab.spec.ts |
| WSP-50 | Running items are reread every 20 s and open MRs every minute | none | spec: workspace-gitlab.spec.ts |
| WSP-51 | MR review actions on the Thread's own MR: reply, resolve, draft reply, Investigate, Fix | none | planned (needs a fake GitLab that maps the Thread's own commit to its merge request and takes discussion writes) |
| WSP-52 | Shell rules for agent glab calls: `glab api` and `search` are refused with a replacement, lists get `-P 10`, and… | glab_hooks_test.go | spec: workspace-gitlab.spec.ts |
| WSP-53 | Before `git push`, committed CI changes get `glab ci lint` | none | spec: workspace-gitlab.spec.ts |
| WSP-54 | `glab mr create` without a description gets one from the Utility model through `--description-file` | none | spec: workspace-gitlab.spec.ts |
| WSP-55 | Settling keeps a worktree holding git-ignored files outside regenerable folders; Settings counts them as not committed and Remove asks first | worktree_cleanup_test.go | spec: workspace-worktrees.spec.ts |
| WSP-56 | The GitLab panel links the project (header), a merge request's branches and **Compare**, and a pipeline's ref and commit; the project's exact path in a reply links to the project | gitlab-items.test.tsx, gitlab-refs.test.mjs | spec: workspace-gitlab.spec.ts |
| WSP-57 | A reply's prose links `issue #N`, `job #N`, `group/proj!N`, full SHAs and `commit <short>`; a bare `#N` stays text; merge requests and pipelines named in prose get outcome-bar chips, one per item | gitlab-items.test.tsx, gitlab-refs.test.mjs | spec: workspace-gitlab.spec.ts |
| WSP-58 | An open MR chip says its readiness from the one MR read (auto-merge, conflict, threads open, needs approval/rebase, ready); MR and pipeline links in a reply end in a state dot with the words in their title, no extra glab call | gitlab-items.test.tsx, gitlab_items_test.go | spec: workspace-gitlab.spec.ts |
| SET-01 | The sidebar gear opens Settings over the main pane with `#settings` in the URL; × or Esc closes it and the Thread… | settings.test.tsx | spec: settings-view.spec.ts |
| SET-02 | The rail lists the pages by group; Find a setting filters pages by words and Enter opens the first; a page mounts on… | settings.test.tsx, settings-groups.test.tsx | spec: settings-nav.spec.ts |
| SET-03 | At phone width the rail is a list showing each page's current values; opening a page moves focus to its back button… | settings.test.tsx | spec: settings-nav.spec.ts |
| SET-04 | Every service setting changed in the UI is saved at once and is still there after a reload and after a service… | settings.test.tsx | spec: settings-saving.spec.ts (reload; after a service restart: planned) |
| SET-05 | A save the service refuses puts the old value back and shows "Could not save the setting: <reason>" | settings.test.tsx | spec: settings-models.spec.ts |
| SET-06 | New threads defaults (model, effort, context size, Safe/Yolo) are saved as `task_defaults` and a new Thread starts… | settings.test.tsx | spec: settings-saving.spec.ts |
| SET-07 | The compaction threshold (50-90 % in 5s; 80 stored as unset) is saved and applies to new Threads | settings.test.tsx | spec: settings-saving.spec.ts (stored; reaching Copilot not assertable) |
| SET-08 | Conversation: the Enter-while-running default (Send now / After this turn), Suggest replies and Threads-can-message… | settings.test.tsx, settings-help.test.tsx, federation.test.tsx | spec: settings-saving.spec.ts |
| SET-09 | Custom providers: Add provider with name, base URL and API key, Load models lists what the endpoint serves (`POST… | settings.test.tsx | spec: settings.spec.ts |
| SET-10 | A saved API key is never shown again; leaving the edit form's key blank keeps it | settings.test.tsx | spec: settings-saving.spec.ts |
| SET-11 | Environment-variable key source: the name must start `UAM_BYOM_`; an unset variable shows "<VAR> is not set in the… | settings.test.tsx | spec: settings-providers.spec.ts |
| SET-12 | Load models failure (endpoint down, 401) shows "Could not load the models: <upstream status line>"; more than 500… | none found (no test text "Could not load | spec: settings-saving.spec.ts (the 500-model cap: planned) |
| SET-13 | Edit keeps the saved models selected; unchecking models asks "Remove models from this provider?"; Remove provider /… | settings.test.tsx | spec: settings-providers.spec.ts |
| SET-14 | Vision toggle: "Enable vision for <id>" is enabled only for checked models and saves `vision: true`; an image sent… | settings.test.tsx | spec: settings-providers.spec.ts |
| SET-15 | A saved custom model appears in the composer's model menu under its provider name and a Thread can switch to it… | settings.test.tsx | spec: settings.spec.ts |
| SET-16 | The service refuses invalid custom models (provider name outside letters/digits/`.`/`_`/`-`, base URL with… | none (server-side; no DOM test of this t | spec: settings-saving.spec.ts (provider name), settings-providers.spec.ts (base URL, variable name) |
| SET-17 | Models: the "Show <model>" switch hides a model from the pickers; a Thread already on it keeps it and shows "hidden… | settings.test.tsx | spec: settings-models.spec.ts |
| SET-18 | Subagent models: add, Make fallback, Remove; a subagent the agent launches on a model outside the list is moved to… | settings.test.tsx | spec: settings-models.spec.ts |
| SET-19 | Utility model: Cheapest / None / a model; with no priced model a Thread's title runs on the Thread's own model; None… | background-ai.test.tsx, settings.test.tsx | spec: settings-background-ai.spec.ts |
| SET-20 | Background AI: today's calls against the daily limit, limit edit, No daily limit, 0 turns it off; Show log lists… | background-ai.test.tsx | spec: settings-background-ai.spec.ts |
| SET-21 | Skills: list by source with filter and count, View, Disable (renames to `.uam-disabled`) / Enable, Remove; changes… | configuration.test.tsx | spec: settings-toolkit.spec.ts |
| SET-22 | Agents, hooks and instructions: add/edit/view, Terminal gate for agents and hooks, conflict on a file edited elsewhere | configuration.test.tsx, configuration-ai.test.tsx | spec: settings-toolkit.spec.ts |
| SET-23 | Install skills with npx (`skills@1.7.0 add`) | configuration.test.tsx | not feasible: needs the network |
| SET-24 | MCP servers: cards grouped by source from Copilot's user-wide config; Add server (HTTP/SSE address, headers); header… | mcp.test.tsx | not feasible: the signed-out CLI reports no MCP management (`available: false`) |
| SET-25 | A stdio MCP server can be added or edited only while Terminal is on: with it off the form offers HTTP and SSE only… | mcp.test.tsx | spec: settings-mcp.spec.ts (the form, its list read stubbed; the service's 403 and editing a saved server: not feasible, the signed-out CLI reports no MCP management) |
| SET-26 | The built-in GitHub MCP switch is off by default, is uam's setting (`github_mcp`) and reaches open Threads | mcp.test.tsx | spec: settings-mcp.spec.ts (Settings' list read stubbed; the server's tools in a turn: not feasible, needs GitHub sign-in) |
| SET-27 | In a Thread, … → MCP servers… shows each server's state (Connected with Show tools / N tools, Failed with error and… | mcp.test.tsx | spec: settings-mcp.spec.ts (Needs sign-in: not feasible, needs an OAuth provider) |
| SET-28 | Signed-out Copilot: Settings → GitHub Copilot says Signed out with Copilot's reason and Check again; a banner over… | copilot-account.test.tsx | spec: settings-copilot.spec.ts |
| SET-29 | Sign in with GitHub (device code), token sign-in, sign out, Unlink, linked-account refusal, environment-token precedence | copilot-account.test.tsx | not feasible: needs a real GitHub account |
| SET-30 | Copilot CLI panel shows the installed CLI version and the newest release, or "Could not check for a newer release:… | copilot-cli.test.tsx | spec: settings-server.spec.ts (the release check answered by a stub) |
| SET-31 | Restart Copilot runtime: the confirmation names Threads working or waiting, they end Stopped with "Copilot runtime… | copilot-cli.test.tsx | spec: settings-copilot.spec.ts |
| SET-32 | Copilot CLI update (stage, SDK handshake, install, restart when idle) | copilot-cli.test.tsx | not feasible: needs a CLI release to install |
| SET-33 | UAM and access → UAM shows the running version; with another version installed at the started binary path it shows… | uam-service.test.tsx | spec: settings-server.spec.ts |
| SET-34 | Terminal access gate: off by default; on adds a Terminal button to the Thread header and lets stdio MCP, skills,… | settings.test.tsx, configuration.test.tsx | spec: settings-toolkit.spec.ts, settings-mcp.spec.ts, workspace-terminal.spec.ts |
| SET-35 | The Usage button beside Settings opens a popover with Today / 7 days / 30 days / Lifetime; each model shows input,… | usage.test.tsx, usage-ai.test.tsx, usage-preload.test.tsx | spec: settings-usage.spec.ts (after a service restart: planned) |
| SET-36 | The Usage popover fits the viewport without its own scrolling, on desktop and at phone width | none | spec: settings-usage.spec.ts |
| SET-37 | Models → Token costs: an unpriced model says so; saved input and output prices give it an estimated cost | token-pricing.test.tsx | spec: settings-models.spec.ts |
| SET-38 | Per-Thread usage (Tools → Usage tab): this Thread's tokens and model calls, subagent rows, Refresh; a closed Thread… | task-usage.test.tsx | spec: settings-usage.spec.ts (after a service restart: planned) |
| SET-39 | Account quota and AI credits (allowance in Usage and the composer chip, budget in AI credits or quota %) | usage.test.tsx, usage-ai.test.tsx, task-usage.test.tsx | not feasible: custom models use no quota |
| SET-40 | Session stats: scans every journal under `COPILOT_HOME/session-state`, shows counts grouped by prefix, writes… | session-stats-settings.test.tsx | spec: settings-server.spec.ts |
| SET-41 | Connected instances: Add instance (label, address, access key, private-address switch), a refused pairing clears the… | connected-instances-settings.test.tsx | spec: settings-server.spec.ts |
| SET-43 | This browser: Theme Light / Dark / Match system and Motion are kept per browser (`localStorage` `uam.theme`) and… | settings-groups.test.tsx, theme.test.mjs | spec: settings-nav.spec.ts |
| SET-44 | Notify me when a Thread needs me or finishes (push notifications) | settings.test.tsx | not feasible: needs OS push |
| SET-45 | Log out in UAM and access ends the session and returns to sign-in | settings.test.tsx | spec: settings-nav.spec.ts |
| SET-46 | Help tips beside labels open on hover, focus and tap; disabled reasons stay inline | settings-help.test.tsx | spec: settings-nav.spec.ts |
| SET-47 | A Copilot CLI older than the oldest release uam drives: the card says "Copilot CLI X is too old for this UAM. Update to Y or newer." and Update installs the newest | none | spec: settings-copilot.spec.ts |
| SET-48 | An MCP server's Tool loading (Default, On demand, Always loaded) is saved as `defer_tools` in Copilot's config and shown again on edit | mcp.test.tsx, mcp_test.go | spec: settings-mcp.spec.ts (list and save stubbed; the service's write to the CLI: not feasible, the signed-out CLI reports no MCP management) |
