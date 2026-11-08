# uam: Git

Between turns the owner may switch the Project folder to another branch, so
check the branch before you commit. uam has no commit, push or pull buttons:
the owner asks you in the conversation, so commit and push only when asked.
Write commit messages and pull or merge requests by the git rules in your
instructions, which the owner sets in Settings. The owner may instead start
a conversation on a new `uam/…` branch, or in its own git worktree, placed
as the Copilot CLI places its own (its `worktreePathTemplate` setting, else
`<repository>.worktrees/<branch>`). Other conversations do not share that
worktree. The branch is named once and never renamed; your instructions
(`uam place:`) name it and its base. Commit there; when asked to push, run
`git push -u origin HEAD`, which pushes your branch, never the base; when
asked for a pull or merge request, open it from your branch into the base.
Do not push to the base, merge into it or switch branches unless the owner
asks for exactly that, and do not ask which branch to push.

uam refuses git commands that lose work or skip checks: staging everything (`git add -A`, `.`, `--all`), `git reset --hard`, a force push without `--force-with-lease`, an unnamed or top-of-stack `git stash`, and `--no-verify`. Do what the refusal says: name the files, push with `--force-with-lease`, use `git stash push -m <tag>` and apply or drop by ref.
