# uam: Starting another Thread: uam_create_task

`uam_create_task` starts a new Thread with your prompt as its first message.
Use it when the owner asks for work to run as its own Thread, and for any
change outside your own Thread's folder: uam refuses edit tools there
(`uam scope:`), because such a change is tracked by no Thread. Do not write
outside your folder with shell commands either; uam cannot see where they
write, but the rule is the same. Committing and pushing your own branch is
not such a write.

- `project` or `path` (one of them): `project` is the Project's ID or exact
  name; a name several Projects share is refused with their IDs. `path` is a
  folder: absolute, `~/...`, or relative to your folder. A path inside a
  Project, or below its folder, starts the Thread in that Project and names
  the subfolder in its first message. A folder in no Project must be inside
  the home folder: uam asks the owner to confirm adding it as a Project (and
  makes it when missing), and the call waits for the answer. If the owner
  declines, do not call again for it; say what you need in your reply.
- `prompt` (required): up to 16 KiB. The new Thread sees nothing of this
  conversation, so write a prompt that stands alone.
- `name`: up to 120 characters; without one, the Thread is titled from its
  prompt.
- `model` and `effort`: pick them for the work. `uam` op `models` lists the
  models the owner enabled, each with its efforts and cost tier, and the
  default. A hard or wide change: a stronger model or more effort; a small,
  well-defined one: a cheaper model or less effort. Only enabled models are
  accepted. Without `model`, the owner's default for new Threads; without
  `effort`, the model's default. When the owner names a model, pass it.
- `worktree`: by default the Thread starts on a new branch in its own git
  worktree of the Project, so its changes stay apart from the Project's
  folder until merged (a Project outside git runs in its folder). Pass
  `false` only when the owner asks for the Project's own folder. `base` is
  the branch or commit the new branch starts from, else the Copilot CLI's
  `worktreeBaseRef` setting decides (the Project's current branch unless it
  says `defaultBranch`).

The new Thread starts in Yolo mode when you are in Yolo, else in Safe mode.
It runs on its own and shows in the owner's
sidebar. The result names its Thread ID. Each time its turn ends (completed,
failed, stopped, at its spending limit), or it is settled or archived, you get
a `uam thread:` note with its name and ID, how it ended, the files it changed
when known and the start of its last reply (up to 600 bytes; `history {id}`
reads the rest): with your next tool call, or as your next turn starts if you
are idle. When it waits for the owner on a question or permission request,
and when the owner answers, you get a note with the question and the answer.
Wait for these notes instead of polling the Thread with `uam`. A Thread can start at most 5; deleting
one frees its place. A Thread started this way, or by one of the owner's
routines (scheduled runs), does not have the tool: tell the owner what to
start instead.
