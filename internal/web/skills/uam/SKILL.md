---
name: uam
description: This session is a uam Task. The owner reads your replies in a browser, often on a phone, that folds tool calls and their output away, so state results and errors in the reply itself. Load this skill before showing the owner a file, image or diagram, before calling a board_* tool, and when the owner mentions uam or the Planner.
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
  its choices, and waits for an answer with no time limit. Offer choices; the
  owner often answers from a phone.
- **Changes**: the owner's view of the whole working tree diffed against
  `HEAD`, other Tasks' edits included.

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

Call `uam_show_file` to put a card for a file in the conversation: a report,
a screenshot, a generated PDF or HTML page. The file is a regular file inside
the Task's directory, or one you created under the system temp directory.
Text previews show the first 64 KiB; an HTML page stays interactive.

## Starting another Task: uam_create_task

`uam_create_task` starts a new Task in an existing Project, with your prompt
as its first message. Use it when the owner asks for work to run as its own
Task. The new Task runs on its own in Safe mode and shows in the owner's
sidebar; no reply comes back to you. A Task can start at most 5, and a Task
started this way cannot start more.

## Planner: the board_* tools

The Planner is the owner's agile board per Project, with Epic, Story and
Subtask cards. The `board_*` tools are present while the owner has the
Planner on and the Project is a git repository.

- **Scope**: a Task started from the Planner works only under the card its
  first message names. "Launch" and "Do whole story" start it holding a
  subtask: finish that with a done request, then `board_claim` the next
  pending one. "Plan with agent" creates and edits under its card and holds
  nothing. Any other Task reads the board and may propose epics.
- **The owner closes work.** `board_request` files done, cancel or blocked,
  and the owner accepts or rejects it. A rejection reaches you as a steer
  with the reason, and you keep the subtask.
- **Done** needs every checklist item ticked with `board_checklist` and no
  open blocker. uam then gathers the evidence itself (diff, commits, files
  this Task touched) and runs the owner's acceptance command; a failing
  command refuses the request.
- **Proposals**: cards you create stay unconfirmed until the owner confirms
  them, and expire after 14 days. Caps per Task: 20 created cards, 10
  unconfirmed children per card or 10 epics at the root, 20 comments per
  card.

## Attachments

A PDF the owner attaches reaches you natively only when the model supports
PDFs. Otherwise you get its file path; read it with your own tools.
