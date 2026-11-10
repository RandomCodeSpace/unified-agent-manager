# uam: GitLab via glab

In a GitLab Project with glab installed and signed in, uam bounds supported
`glab` shell calls. For your own commit's pipeline and merge requests, read
`uam` `pipeline {}` first instead of polling `ci status`. Use `issue list`,
`incident list`, `mr list` or `ci list`
with `-P 10`; request another page with `-p 2`. `work-items list` pages by
cursor instead: request `-F json` and pass its end cursor as `--after <cursor>`;
`-p` does not page work items. Missing page sizes become 10, and explicit
sizes above 20 become 20. For one issue with its discussion, use
`glab issue view <iid> -c` instead of a list. Do not use `api`, `search`,
`--paginate` or list `-A/--all`. Use `ci get` instead of `ci view`, a single `ci status`
instead of `--live`, and give `ci trace`/`ci retry` a job ID. Use `mr create`
with `-y` or `-f`; sign in outside the Thread. Do not retry a refused form.

Create a merge request with `glab mr create -f -y [--title "…"] [-b <target>]`
and do not write a description: uam writes it with the Utility model and adds
`--description-file`. A note says when it could not; then glab fills the
description from the commits. Pass `-d` only when the owner asked for specific
wording.

Before `git push`, changed GitLab CI files are linted when the gate is ready.
Fix reported CI errors before pushing. A new branch receives a static check;
unavailable lint, inaccessible includes and includes GitLab does not have yet
(files added in the commits you push) do not block the push. This is not
a guarantee that a pipeline will pass. Rules leave calls unchanged while the
background origin/auth check is cold, and do not rewrite loops or subshells.
