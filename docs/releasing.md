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

Versions are `vMAJOR.MINOR.PATCH` or `vMAJOR.MINOR.PATCH-beta.N`. Stable preparation
fetches `origin/main` and requires the source parent to be an ancestor of it.
A beta may use an approved unmerged source commit. Both release kinds keep the
same quality, coverage, security, release-configuration and protected release
environment gates. Preparation is a packaging check, not evidence that those
gates passed. Review the source, child commit and local tag before publication.

The release workflow repeats the source-parent/child checks, rebuilds the UI
with the pinned Node version, and refuses a bundle that differs from the tag,
including extra ignored files. `make check-web` checks existing build output
without rebuilding it.
GoReleaser also builds the UI before compiling Go for snapshots and releases.
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

Run `scripts/test-release-packaging` on a committed source checkout to check the
contract in a disposable repository. It exercises source-only behavior,
preparation, source isolation, stable ancestry, duplicate-tag refusal, a real
versioned Go install from a local module proxy, and rejection of source edits
in a generated child. It does not publish tags or contact a provider.
