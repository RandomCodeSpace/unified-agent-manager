<div align="center">

# Unified Agent Manager

**A browser workspace for GitHub Copilot.**

Run tasks, follow tool calls and subagents, and return after closing the browser.

<p>
  <a href="https://github.com/RandomCodeSpace/unified-agent-manager/actions/workflows/ci.yml"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/RandomCodeSpace/unified-agent-manager/ci.yml?branch=main&label=ci&style=for-the-badge&logo=githubactions&logoColor=white"></a>
  <a href="https://github.com/RandomCodeSpace/unified-agent-manager/actions/workflows/security.yml"><img alt="Security" src="https://img.shields.io/github/actions/workflow/status/RandomCodeSpace/unified-agent-manager/security.yml?branch=main&label=security&style=for-the-badge&logo=githubactions&logoColor=white"></a>
  <a href="https://github.com/RandomCodeSpace/unified-agent-manager/releases"><img alt="Latest release" src="https://img.shields.io/github/v/release/RandomCodeSpace/unified-agent-manager?style=for-the-badge&logo=github"></a>
  <a href="https://github.com/RandomCodeSpace/unified-agent-manager/releases"><img alt="Downloads" src="https://img.shields.io/github/downloads/RandomCodeSpace/unified-agent-manager/total?style=for-the-badge&logo=github&label=downloads"></a>
  <a href="https://go.dev/"><img alt="Go version" src="https://img.shields.io/github/go-mod/go-version/RandomCodeSpace/unified-agent-manager?style=for-the-badge&logo=go"></a>
  <img alt="Linux and macOS" src="https://img.shields.io/badge/Linux%20%7C%20macOS-supported-0ea5e9?style=for-the-badge&logo=terminal&logoColor=white">
</p>

</div>

UAM runs Copilot through its SDK and serves an authenticated web interface.
Projects group tasks by working directory. Tasks keep their conversations,
model choices, history, and lifecycle state across browser reconnects.
Custom models, including Ollama endpoints, use the same Copilot SDK integration.

## Install

UAM runs on Linux and macOS. Download an archive from
[GitHub Releases](https://github.com/RandomCodeSpace/unified-agent-manager/releases),
or install a published version with Go:

```sh
go install github.com/RandomCodeSpace/unified-agent-manager/cmd/uam@latest
uam version
```

This README describes the current source. Older published versions may still
include terminal features that have since been retired.

## Start the web workspace

1. Install GitHub Copilot CLI and Node.js, and sign in to Copilot.
2. Start the service:

   ```sh
   uam web
   ```

3. Open the printed URL and sign in with the access token. Add a Project,
   create a Task, choose a model, and send a message.

The service keeps working after its launching terminal or browser closes.
For a remote host, use the printed SSH forwarding command or configure an
authenticated deployment as described in the [web guide](docs/web.md).
Authentication is required; there is no `--no-auth` mode.

## Commands

| Command | What it does |
|---|---|
| `uam`, `uam help`, `uam --help` | Show help |
| `uam web` | Start the web service or show the running service |
| `uam web status [--json]` | Show service status |
| `uam web stop` | Stop the service and its Copilot runtimes |
| `uam web token set` | Set the access token from stdin |
| `uam version`, `uam --version`, `uam -v` | Print the version |

`uam web` accepts `--listen`, repeatable `--public-origin`, and `--log-headers`.
See the [web guide](docs/web.md) for their behavior and deployment details.

## Retired terminal support

UAM supports Copilot through the web/SDK only. Its dashboard, terminal host,
attach protocol, and terminal provider adapters have been removed, including
the terminal adapter for Copilot. Claude Code, Codex, Hermes, Oh My Pi, and
OpenCode are no longer UAM integrations. The Copilot executable is still
required by the SDK.

The former `new`, `dispatch`, `attach`, `last`, `ls`, `stop`, `restart`, `rm`,
`kill-all`, `profile`, and `doctor` commands return a retirement error. Use the
web interface for task controls and `uam web status` for service status.

Existing terminal records and profiles remain on disk and are not converted
into web tasks or deleted. Previous Copilot conversations can still be imported
through the web interface after its in-use check. Other providers' history is
not imported. This source change does not stop existing processes or uninstall
provider binaries. Stop any old UAM terminal sessions with the older binary
before replacing it; the new binary cannot attach to or stop those hosts.

## Task permissions

Tasks use the permission mode selected in the web interface. This is not an
operating-system sandbox. Multiple tasks can edit the same directory, so use
separate worktrees or checkouts when tasks need filesystem isolation. UAM does
not create that isolation automatically.

## Build and test

```sh
make build
make test-e2e
```

Source builds require Go 1.25.14 or newer and Node.js with npm. The tested Node
version is in `web/.node-version`. `make build` and `make install` use the locked
frontend dependencies, build the UI, and embed it in the Go binary. Generated
bundles are ignored on source branches. Without `make web`, a plain Go build
can show help/version, but `uam web` reports that its UI has not been built.

Release tags contain the generated UI, so installing a published tag does not
require Node.js to build UAM. See [release preparation](docs/releasing.md),
[testing](docs/testing.md), and the [web command reference](docs/web-commands.md).

### Verify a release

`SHA256SUMS` is signed with [cosign](https://github.com/sigstore/cosign) keyless
signing from the release workflow. Download the archive, `SHA256SUMS`,
`SHA256SUMS.sig`, and `SHA256SUMS.pem` from the release, then check the
signature against the workflow identity before trusting the checksums:

```sh
cosign verify-blob --certificate SHA256SUMS.pem --signature SHA256SUMS.sig \
  --certificate-identity-regexp '^https://github.com/RandomCodeSpace/unified-agent-manager/.github/workflows/release.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com SHA256SUMS
sha256sum -c SHA256SUMS --ignore-missing
```
