---
name: uam
description: This session is a uam Task. The owner reads your replies in a browser, often on a phone, that folds tool calls and their output away, so state results and errors in the reply itself. Load this skill before showing the owner a file, image, diagram or chart, before calling a board_*, uam_create_task or uam_chart tool, and when the owner mentions uam or the Planner.
---

# uam

This conversation is a **Task** in uam, a web service that runs Copilot
sessions on a Linux host and shows them in the **owner's** browser, often on
a phone.

## Vocabulary

- **Project**: a named working directory. A **Task** is one Copilot
  conversation running in its Project's directory. Several Tasks can work in
  the same directory at once, so the working tree can change under you: check
  `git status` before you treat a change as yours.
- **Steer**: a message the owner sends while you work. It reaches you before
  your next step. A **queued** message waits until the turn ends.
- **Safe / Yolo**: the permission policy. In Safe each permission request
  waits for the owner.
- **Questions**: an `ask_user` question shows above the owner's composer with
  its choices, and waits for an answer with no time limit. Put each option in
  `choices`, not in the question text; the owner often answers from a phone.
  List the one you recommend first, ending in "(Recommended)": the owner sees
  it pre-selected. When several options may apply together, end the question
  itself with "(Choose any that apply)": the owner then ticks any number of them
  and you receive the chosen labels joined by ", ".
- **Changes**: the owner's diff view. It opens on **This task**: the files
  you or your subagents edited with an edit tool, compared with `HEAD`.
  Files written by shell commands, or changed by another Task, show only
  under **All changes**, the whole working tree against `HEAD`.

## What the owner sees

- **Compact view is the default.** Each turn folds into one line such as
  "Took 1m 2s · 3 commands · 1 failed". Tool calls, their output and thoughts
  are hidden; a failure shows only as a count. Put what the owner must read
  in the reply: results, the errors that matter, decisions, next steps.
- **Markdown** renders with GitHub extensions (tables, task lists). Raw HTML
  and math do not render: write formulas as code.
- **Code blocks** never wrap; on a phone they scroll sideways, so keep lines
  short. Common language tags are highlighted.
- **Diagrams**: a fenced `mermaid` block renders as a diagram. Other diagram
  languages stay code, and a block Mermaid cannot parse shows as code.
- **Images**: `![alt](path)` shows a png, jpeg, gif or webp file up to
  20 MiB inside the Task's directory. An image at a web address stays a link.
- **File links**: a Markdown link to a file in the Task's directory, or a
  path in inline code that contains a `/` (`src/app.go`, `./Makefile`),
  becomes a chip that opens a preview. Write paths relative to the Task's
  directory, or absolute.

## Showing a file: uam_show_file

Call `uam_show_file` to record a file reference, then include its path as a
Markdown link or inline code in your reply so the owner can open its preview.
The file is a regular file inside the Task's directory, or one you created
under the system temp directory. Text previews show the first 64 KiB;
an HTML page stays interactive.

## Charts: uam_chart

When the owner asks a data question (counts over time, sizes per kind,
durations), answer with a chart: call `uam_chart`. The owner's browser draws
a line or bar chart from rows, with a table and Copy CSV, and can pin it to
the Project and refresh it later without you.

- **Prefer `command`, even when the rows need computing.** Give a shell
  command (a pipeline, or a short script such as a `python3` heredoc) that
  prints the rows as CSV with a header row (`format: "csv"`), or as JSON objects
  (`format: "json"`). uam runs it in the Task's directory and reads the
  output itself, so the rows cost you no tokens; you get back the row count
  and each series' min, max, total and last value. The command must stand
  on its own (no files you made earlier that may be gone), because Refresh
  runs it again later. Example: commits per day this month:
  `git log --since="$(date +%Y-%m-01)" --date=short --format=%ad | sort | uniq -c | awk 'BEGIN{print "day,commits"}{print $2","$1}'`.
- **`data`** takes the rows inline as objects. Use it for a few rows you
  already have. In Safe mode uam refuses to run a command: then run it with
  your shell tool (the owner approves it) and pass its rows.
- `x` names the field for the x axis (values must be unique; aggregate
  first), `y` one to 4 numeric fields, one series each. `line` for a trend
  over ordered x such as days, `bar` to compare categories. At most 500
  rows: aggregate, or keep the last ones.
- With `kind: "echarts"`, the chart's ⋯ menu holds zoom, the date range
  and series toggles, and uam hides any `dataZoom` slider you add, so do not
  write "drag the slider" in a title or reply. Options are JSON, so
  formatters are string templates (`"{b}: {c}"`); a `valueFormatter` may
  only be a `"{value}"` template such as `"${value}"`.
- A refused call says what to fix. After the chart, write the takeaway in
  your reply (the peak, the trend, anything odd), not the rows: the owner
  sees them in the chart.

## Starting another Task: uam_create_task

`uam_create_task` starts a new Task in an existing Project, with your prompt
as its first message. Use it when the owner asks for work to run as its own
Task. The new Task starts in Safe mode, runs on its own and shows in the
owner's sidebar; no reply comes back to you. A Task can start at most 5. A Task
started this way, or by one of the owner's routines (scheduled runs), does
not have the tool.

## Planner: the board_* tools

The Planner is the owner's agile board per Project, with Epic, Story and
Subtask cards. The `board_*` tools are present while the owner has the
Planner on and the Project is a git repository.

- **Scope**: a Task started from the Planner works only under the card its
  first message names; that message may carry a brief from the owner.
  "Launch" and "Do whole story" start it holding a subtask: finish that
  with a done request, then `board_claim` the next pending one. "Plan with
  agent" creates and edits under its card and holds nothing. The owner may also add a running Task to a story without a
  message: it then holds a subtask and works in that story's scope like a
  launched Task, and `board_list` shows the card as "held by you". Any other
  Task reads the board and may propose epics. Every Task's scope also covers
  the cards it created until they start, so you can build out the epics you
  propose: stories, subtasks and links under them, from one request. Cards
  the owner or another Task created stay out of reach.
- **Requests**: `board_request` files done, cancel or blocked. You never
  mark a card done yourself. The owner accepts or rejects every request
  that is not accepted automatically (see Done). A rejection reaches you as
  a steer with the reason, and you keep the subtask.
- **Done** needs every checklist item ticked with `board_checklist` and no
  open blocker. uam then gathers the evidence itself (diff, commits, files
  this Task touched) and runs the owner's acceptance command. A failing
  command refuses the request. It is accepted at once when the command
  passes and nothing holds it back: claim the next pending one. Otherwise
  it waits for the owner, and the reply says why.
- **Proposals**: cards you create stay unconfirmed until the owner confirms
  or launches them, or approves their epic; the owner's edits keep them proposals and restart their
  14 days, after which they expire. Work runs only on confirmed cards:
  `board_claim` refuses a proposal or a card under one. Caps per Task: 50
  live cards created (deleted and expired ones free their place), 200
  created in its lifetime (deleted and expired ones still count), 10
  unconfirmed children per card or 10 epics at the root, 20 comments per
  card.
- **Approval**: an epic runs on the owner's one approval of it in the
  Planner. When your plan for an epic is complete, ask the owner to approve
  it there and end your turn; nothing runs before that. Under an approved
  epic the approval owns the work: uam starts its subtasks, each in a lane,
  so leave claiming to it (`board_claim` answers `run_owned`), and a
  card you add, or one the owner restores, stays a proposal until the owner
  approves the epic again, which you ask for the same way. Moving a card
  into or out of an approved epic becomes a change request for the owner.
  Keep a confirmed subtask in every confirmed story and epic there: a write
  that leaves one with none is refused. To split a confirmed subtask right
  under the epic, edit it into the first part and create the others.
  `board_get` shows the approval and any pause; nothing at or under a
  paused card starts, and moving a card out from under one becomes a
  change request too.
- **Lanes**: under an approved epic each subtask runs in a Task of its own,
  in its own git worktree, on a branch made from the Project's integration
  branch (`uam-plan-…`); other subtasks run beside it in theirs. That Task
  has only `board_get`, `board_list`, `board_checklist`, `board_comment` and
  `board_request`, and works on its one subtask, in its directory, on its
  branch: uam alone pushes, pulls and switches branches there. Finish with
  `board_request` done: uam commits what you left, merges the integration
  tip into your lane, runs the acceptance command and lands your work as one
  commit. Commit any merge you start before filing done. When the tip's
  merge conflicts, the reply names the files: run `git merge uam-plan-…`,
  resolve them, commit, and file done again. Merge only `uam-plan-…` into
  your lane, never the base branch: a lane holding base commits that
  `uam-plan-…` cannot take is refused. Once the reply says the work
  landed, or that landing is queued, end your turn. A landed subtask the
  owner reverts, or reopens with its code kept, comes back To do and
  paused; its comment says which and why, so read it before you plan
  around that subtask.
- **Planning**: until a subtask starts (held, doing or done) you plan it
  directly in your scope, confirmed or not: `board_edit`, `board_checklist`,
  `board_link`, `board_unlink`, `board_split` and `board_delete`. A started
  subtask keeps its plan: you tick only the one you hold and comment; a
  change to it becomes a request the owner accepts after releasing it.
- **Started work keeps what it waits on**. A write that would make a started
  subtask wait on an open card again is refused `in_progress`, naming that
  subtask: for example, moving a confirmed subtask into a done story that
  started work waits on. A card you create under a done story is a proposal
  and leaves the story done. Once work under a story or epic is in progress
  or done, only the owner links or unlinks what it waits on; you may still
  link it as the blocker of another card.
- **Delete**: `board_delete` cancels a card and everything under it, and the
  owner can restore it. It is refused while anything in it has started,
  while started work waits on it, and when it would leave the story or epic
  above it done or cancelled; then edit the card, or file a cancel request.
  Deleting a blocker releases what waits on it, so link the replacement
  first. `board_split` is refused the same way when its parts, which are
  proposals, would leave the story or epic above done or cancelled; then
  edit the subtask into the first part and create the others.
- **Links** order cards at one level: epics with epics, stories with stories
  in one epic, subtasks with subtasks in one story. Either side may be a
  proposal. To order work across containers, link the containers; a card
  waits on its container's blockers too. A linked card can't move to another
  parent until its links are removed. Splitting a subtask under a story makes
  siblings that keep its links, both ways, except to a subtask that already
  started.

## Attachments

A PDF the owner attaches reaches you natively only when the model supports
PDFs. Otherwise you get its file path; read it with your own tools.

## Subagent models

The owner can limit the models subagents run on in Settings. When a limit is
set, a subagent you start runs on the model you asked for only if it is
allowed; otherwise uam moves it to this Task's model or the owner's fallback.
If no allowed model is usable, the launch is refused: do the work yourself.
