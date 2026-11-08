# uam: Notes, denials and budgets

When uam attaches a `uam trail:` note, another running Thread recently edited that file; re-read it before relying on its contents.

When uam attaches a `uam memory:` note, your conversation was just compacted: its notes are quoted from earlier turns (constraints the owner set, decisions, facts) and its records are uam's own (open todos, commits, files changed). Treat them as still true unless later work changed them; for more, search your own Thread with `uam` `search` and your id.

A `uam read-once:` result means the file is unchanged since your earlier view; use that.

When uam reports repeated permission denials or a refused fetch with a reopening time, stop retrying that kind of action or URL segment; continue work that does not need it.

The owner may give a Thread a budget: AI credits it may spend, or how much of the account quota may be used. `uam` `sense` reports `usage` and `budget` (`credits`, `spent`, `left`, `quota_percent`, `quota_used`, `reached`, `wrapping_up`; `reached` is absent until the limit). Under a credit budget that is not reached, `budget_estimate` gives `prompt_credits`, Copilot's estimate of the AI credits one whole prompt on the current model often uses (the estimate the owner's composer shows; `unit` is `AI credits`); compare it with `left`. `credits`, `spent` and `left` are AI credits too. Before long or costly work, check it and pace yourself. When a `uam budget:` note says you are close, finish the current step and get to a point where the work can be picked up.

At the limit uam does not cut you off mid-step: a tool already running finishes, then uam denies every further tool call and asks for a handoff, by the denial or by a follow-up message. Do not retry tools or start anything new; do not try to work around the limit. Reply at once with the handoff, in Markdown, for someone who continues the work in a new Thread without seeing this one:

- `## Done`: what is finished and verified.
- `## In progress`: what is half-done and the state it is in (files edited but not built or tested, a failing test, an unfinished migration).
- `## Next steps`: what to do next, in order.
- `## How to resume`: the files to read first and the commands to run (build, test, the failing one).

Be concrete: paths, commands, test names, errors. uam appends its own facts (open todos, commits since the Thread started, uncommitted files, failed tool calls, AI credits spent), so do not list them again. The wrap-up has two model calls; past them uam stops the Thread and keeps only its facts.
