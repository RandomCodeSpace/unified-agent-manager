# Release preparation

Source branches track the frontend source and lockfile, not `internal/web/dist`.
Release tags point to a child commit that adds only that generated directory.
This lets a versioned Go install embed the UI without running npm or a generator.
Do not merge the generated child back into a source branch.

Use the Node version in `web/.node-version` and the Go toolchain in `go.mod`.
Commit and review the source first, then prepare a local tag:

```sh
scripts/prepare-release v0.8.0-beta.1 <approved-source-sha>
git show --stat v0.8.0-beta.1
git diff v0.8.0-beta.1^ v0.8.0-beta.1
```

The script builds the committed `SOURCE_REF` (or committed `HEAD` when omitted),
not uncommitted edits. Any dirty files in the caller's worktree stay untouched.
It creates a disposable detached worktree, installs locked frontend
dependencies, typechecks/builds the UI, checks the embedded index and assets,
builds Go, and creates an annotated tag. It preserves the source branch and
refuses to replace an existing tag. It never pushes. The child commit's only
changes must be regular files added under `internal/web/dist`.

Versions are `vMAJOR.MINOR.PATCH`, `vMAJOR.MINOR.PATCH-alpha.N` or
`vMAJOR.MINOR.PATCH-beta.N`. Stable preparation
fetches `origin/main` and requires the source parent to be an ancestor of it.
An alpha or beta may use an approved unmerged source commit. All release kinds keep the
same quality, coverage, security, release-configuration and protected release
environment gates. Preparation is a packaging check, not evidence that those
gates passed. Review the source, child commit and local tag before publication.

The release workflow repeats the source-parent/child checks, rebuilds the UI
with the pinned Node version, and refuses a bundle that differs from the tag,
including extra ignored files. `make check-web` checks existing build output
without rebuilding it.
GoReleaser builds the UI for local snapshots; CI snapshots embed the bundle the
CI web job built. For releases it checks and embeds the tagged bundle already
rebuilt and verified by the release verify job; the publishing job does not need
Node.js or another UI build.
Only the tag is pushed for the generated child; source PRs carry no bundle.

After publication, install a specific release without Node.js:

```sh
go install github.com/RandomCodeSpace/unified-agent-manager/cmd/uam@v0.8.0-beta.1
```

The version above is an example until published. `@beta` is not an alias created
by this workflow. Use the exact beta tag. For a source checkout use `make build`
or `make install`; `make web` followed by Go commands also works. A source-only
binary's help and version commands work, while `uam web` explains that the UI must be
built. `internal/web/dist.placeholder` makes that source-only compilation
intentional without tracking a fake UI.

The version a source build embeds comes from `scripts/version`: the newest
release tag whose source parent is an ancestor of the checkout, with the
commits since that parent and the abbreviated hash, as
`v0.19.10-16-gfbcd6384` (plus `-dirty` for uncommitted changes). Plain
`git describe` cannot see those tags from `main` because release commits are
never merged back. A checkout of a tagged release commit prints the tag
alone; `VERSION=... make build` overrides the script. `scripts/test-version`
checks it in a disposable repository.

Run `scripts/test-release-packaging` on a committed source checkout to check the
contract in a disposable repository. It exercises source-only behavior,
alpha preparation, beta verification, version syntax, source isolation, stable ancestry, duplicate-tag refusal, a real
versioned Go install from a local module proxy, and rejection of source edits
in a generated child. It does not publish tags or contact a provider.

CI (`.github/workflows/ci.yml`) is one workflow for pull requests, pushes to
`main`, manual runs and the weekly schedule: tests, lint, security, the distro
matrix, the macOS smoke and the GoReleaser snapshot. A newer push to the same
pull request cancels the older run; main, manual and release runs keep separate
concurrency groups.

The web job typechecks and builds the bundle once; every Go job, the macOS smoke
and the snapshot embed that artifact instead of rebuilding it. Component tests run
with coverage in two parallel shards; the first shard also lints and runs the web
unit tests with coverage. Go tests run twice in parallel: `test-race` with the race
detector, and `coverage` without it, which produces the coverage report and checks
the 85% floor. Sonar imports the coverage reports without rerunning tests and fails
when either input job failed. Missing coverage reports or Sonar credentials fail the
check. Fork PRs cannot access the Sonar secret: a maintainer must review the change
and run it on a branch in this repository before merging.

The snapshot job always cross-builds and checks the four release archives. It
rehearses the release scripts (`scripts/test-release-packaging`,
`scripts/test-version`, the Cosign contract and `goreleaser check`) only when
release tooling changes (`scripts/`, `Makefile`, `.goreleaser.yml`, `go.mod`,
`go.sum`, workflows, the web package manifests and Node version,
`internal/version/`), and on the weekly schedule and manual runs.

A release tag does not rerun CI when its source commit already has a passing CI
run: the release verify job checks the tag and ancestry, validates the GoReleaser
configuration, rebuilds and compares the tagged bundle, and runs govulncheck. A
source commit without a passing CI run (one still running, or an unmerged alpha or
beta source) runs the whole CI first. Sonar does not scan release tags.

Require `web`, both `web-tests` shards, `test-race`, `coverage`, `lint`,
`SonarCloud quality gate`, `build static binaries`, the five distro checks,
`GoReleaser snapshot`, `Darwin-native lifecycle and version smoke`, `govulncheck`,
`gosec` and `dependency-review` alongside CodeQL in branch protection, so a failed
dependency cannot hide behind a skipped downstream job. Coverage thresholds are
floors, not a guarantee that the percentage can never decrease.
