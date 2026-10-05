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
  it pre-selected.
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
  or launches them; the owner's edits keep them proposals and restart their
  14 days, after which they expire. Work runs only on confirmed cards:
  `board_claim` refuses a proposal or a card under one. Caps per Task: 20
  created cards, 10 unconfirmed children per card or 10 epics at the root,
  20 comments per card.
- **Planning**: until a subtask starts (held, doing or done) you plan it
  directly in your scope, confirmed or not: `board_edit`, `board_checklist`,
  `board_link`, `board_unlink`, `board_split` and `board_dismiss` for
  proposals. A started subtask keeps its plan: you tick only the one you
  hold and comment; a change to it becomes a request the owner accepts after
  releasing it.
- **Links** order cards at one level: epics with epics, stories with stories
  in one epic, subtasks with subtasks in one story. Either side may be a
  proposal. To order work across containers, link the containers; a card
  waits on its container's blockers too. A linked card can't move to another
  parent until its links are removed.

## Attachments

A PDF the owner attaches reaches you natively only when the model supports
PDFs. Otherwise you get its file path; read it with your own tools.
