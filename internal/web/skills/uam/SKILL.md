---
name: uam
description: Guide to uam, the web app this Copilot session runs inside. Use when the owner mentions uam, a Task, a Project or the Planner; when a reply should show the owner a file, image or diagram; and before calling a board_* tool.
---

# uam

This conversation is a **Task** in uam, a web service that runs Copilot
sessions on a Linux host and shows them in a browser, often on a phone. The
**owner**, the one person who uses this uam, reads your replies there. Work
keeps running after the browser closes.

## Vocabulary

- **Project**: a named working directory. A **Task** is one Copilot
  conversation running in its Project's directory. Several Tasks can work in
  the same directory at once, so the working tree can change under you: check
  `git status` before you treat a change as yours.
- **Steer**: a message the owner sends while you work. It reaches you before
  your next step. A **queued** message waits until the turn ends.
- **Safe / Yolo**: the permission policy. In Safe each permission request
  waits for the owner; in Yolo uam allows each one once.
- **Interactive / Autopilot**: the execution mode. Plan mode is unavailable:
  a request to exit plan mode is always refused.
- **Questions**: an `ask_user` question shows above the owner's composer with
  its choices, and waits for an answer with no time limit. Offer choices; the
  owner often answers from a phone.
- **Changes**: the owner's view of the whole working tree diffed against
  `HEAD`, other Tasks' edits included.

## What the owner sees

- **Compact view is the default.** Each turn folds into one line such as
  "Took 1m 2s · 3 commands · 2 files read", which hides tool calls, their
  output, thoughts and failures. Put everything the owner must read in the
  reply text: results, the errors that matter, decisions, next steps.
- **Markdown** renders with GitHub extensions (tables, task lists). Raw HTML
  and math do not render: write formulas as code.
- **Code blocks** with a language tag are highlighted. They never wrap and
  scroll sideways, and the screen may be 390px wide.
- **Diagrams**: a fenced `mermaid` block renders as a diagram. Other diagram
  languages stay code, and a block Mermaid cannot parse shows as code.
- **Images**: `![alt](path)` shows a png, jpeg, gif or webp file up to
  20 MiB inside the Task's directory. An image at a web address stays a link:
  the page loads nothing from other origins.
- **File links**: a Markdown link to a file in the Task's directory, or a
  path in inline code such as `src/app.go`, becomes a chip that opens a
  preview. Write the path relative to the Task's directory, or absolute. A
  bare file name without `/` links only after a tool call in this Task named
  it.

## Showing a file: uam_show_file

Call `uam_show_file` to put a card for a file in the conversation: a report,
a screenshot, a generated PDF or HTML page. It shows the file without reading
it or granting access.

- The file is a regular file inside the Task's directory (a relative path
  resolves against it) or one you created under the system temp directory. A
  temp file opens only when the owner clicks it.
- Text previews show the first 64 KiB. HTML stays interactive in an isolated
  frame. PDFs and other types offer Open in new tab and Download.
- A session gets 128 cards.

## Planner: the board_* tools

The Planner is the owner's agile board per Project, with Epic, Story and
Subtask cards. The `board_*` tools are present only while the owner has the
Planner on and the Project is a git repository; without them, this Task has
no Planner.

- **The owner closes work.** Finish with `board_request` (`done`, `cancel` or
  `blocked`); the owner accepts or rejects it. A rejection reaches you as a
  steer with the reason, and you keep the subtask.
- **Proposals**: cards you create with `board_create` stay proposals until
  the owner confirms them, and expire after 14 days. You create stories and
  subtasks; epics are the owner's. An edit to a confirmed card becomes a
  change request.
- **One hold**: `board_claim` holds one subtask at a time and records `HEAD`
  as the baseline for the evidence.
- **Done**: a done request needs every checklist item ticked with
  `board_checklist` and no open blocker. uam then gathers the evidence itself
  (diff, commits, files this Task touched) and runs the owner's acceptance
  command; a failing command refuses the request.
- **Caps per Task**: 20 created cards, 10 unconfirmed children per epic or
  story, 20 comments per card.

The flow: read with `board_list` or `board_get`, `board_claim` the subtask,
do the work, tick its checklist, file `board_request` done, then claim the
next one. A Task launched from the Planner starts with a preamble that names
its card and its rules.

## Attachments

A PDF the owner attaches reaches you natively only when the model supports
PDFs. Otherwise you get its file path; read it with your own tools.
