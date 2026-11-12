---
name: uam
description: Guide to uam, the browser app this Thread runs in, and its uam and uam_* tools. Load it before you show the owner a file, image, PDF, HTML page, Mermaid diagram or chart (uam_show_file, uam_chart); read or change an owner's drawing (uam_diagram_read, uam_diagram_edit); read Thread or Project state, search Threads' conversations or read one (uam); start another Thread (uam_create_task); message another Thread or answer one (uam_send); ask the owner a question with ask_user; start subagents; suggest a slash command or autopilot; and when the owner mentions uam, a Thread, a Project, their phone or the browser.
---

# uam

This conversation is a **Thread** in uam, a web service that runs Copilot
sessions on a Linux host and shows them in the **owner's** browser, often on
a phone. Do not call uam's web API or read its credentials; use the tools
and conventions below instead.

## Reference files

This file covers the basics. Before the work below, read its file from this
skill's base directory with your file-view tool. Read only the files the work
needs, all in one step when it needs several:

- `references/owner-view.md`: what the owner sees of a turn, Markdown and the
  rich blocks uam draws from it, code blocks and the todo list.
- `references/files.md`: showing a file or image: paths, links, file chips,
  images and `uam_show_file`.
- `references/diagrams.md`: a Mermaid diagram, or an owner's drawing
  (`uam_diagram_read`, `uam_diagram_edit`).
- `references/charts.md`: a chart (`uam_chart`).
- `references/uam-tool.md`: reading Thread or Project state, searching
  Threads' conversations or reading one (`uam`).
- `references/threads.md`: starting another Thread (`uam_create_task`), or a
  change outside your Thread's folder.
- `references/messages.md`: messaging another Thread or answering one
  (`uam_send`).
- `references/git.md`: commits, pushes, branches and worktrees, and the git
  commands uam refuses.
- `references/notes.md`: `uam trail:`, `uam memory:` and `uam read-once:`
  notes, repeated denials, and a budget (`uam budget:` notes and the handoff
  at the limit).
- `references/gitlab.md`: `glab` in a GitLab Project.
- `references/commands.md`: the slash commands the owner can run, plan mode
  and autopilot.

## Vocabulary

- **Project**: a named working directory. A **Thread** is one Copilot
  conversation running in its Project's directory. Several Threads can work in
  the same directory at once, so the working tree can change under you: check
  `git status` before you treat a change as yours.
- **Steer**: a message the owner sends while you work. It reaches you before
  your next step. A **queued** message waits until the turn ends.
- **Safe / Assisted / Yolo**: the permission policy. In Safe each permission
  request waits for the owner. In Assisted a reviewer model checks each
  request: the ones it approves run without waiting, the rest wait for the
  owner.
- **Changes**: the owner's diff view of the files you or your subagents
  edited with an edit tool, compared with how they were when the Thread
  started, your commits included. Files written only by shell commands, or
  changed only by another Thread, do not show there.

## Questions: ask_user

- The question shows above the owner's composer with its `choices` and waits
  with no time limit.
- The option whose label ends in "(Recommended)" is pre-selected. On a
  question ending in "(Choose any that apply)" the owner ticks any number of
  options, and you get the chosen labels joined by ", ".
- The owner may type their own answer instead of an option, even when you
  pass `allowFreeform: false`.
- A declined question fails with "the user declined to answer". Continue on
  your best judgement, or end the turn saying what you need.

## Subagents

- The owner sees each subagent as a row in your turn, with its description;
  its work stays folded, so report what it found in your reply.
- Subagents receive neither uam's system instructions nor this skill. uam
  passes them the todo and git rules when they start; put any other rule
  a subagent needs in its prompt.
- The owner can limit the models subagents run on in Settings. uam then
  moves a subagent you start to an allowed model and tells you so: that is
  final, so do not start it again. If no allowed model is usable, the launch
  is refused: do the work yourself.
- A subagent that uam cancelled after a model stall can be started again once.

## Attachments

- A PDF the owner attaches reaches you natively only when the model supports
  PDFs. Otherwise you get its file path; read it with your own tools.
- Text over 8 KiB the owner pastes or attaches arrives as a file path with its
  line count; read the parts you need.

## When a uam tool is unavailable

A uam tool call can fail, for instance after an MCP server changed its tools
mid-conversation: its failed result says why, for instance that it is
unavailable, or only that tool execution failed. uam checks its tools again before the Thread's next message,
so they usually work again at the next turn. Until then, carry on without
them: a path in your reply still becomes a file chip, and a Markdown table
can stand in for a chart. Do not retry the call in the same turn.
