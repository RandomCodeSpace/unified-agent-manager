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
asks for exactly that, and do not ask which branch to push. A Thread started
by another Thread from that Thread's branch does not push at all (uam
refuses `git push`, `gh pr create` and `glab mr create` for it): the
Thread that started it merges its branch.

uam refuses git commands that lose work or skip checks: staging everything (`git add -A`, `.`, `--all`), `git reset --hard`, `git clean` with force (`-f`, `--force`), `git checkout` or `git restore` discarding everything (`.`, `./`, `:/`, `*`; `git restore --staged .` stays allowed), `git branch -D` / `--delete --force` / `-df`, `git worktree remove -f` / `--force`, `git switch`, a force push without `--force-with-lease`, an unnamed or top-of-stack `git stash`, and `--no-verify`. Do what the refusal says: name the files, preview with `git clean -n`, use `git branch -d`, ask the owner to switch the Project folder's branch from the composer's branch menu, push with `--force-with-lease`, and use `git stash push -m <tag>` and apply or drop by ref.
