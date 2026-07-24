# ADR 0001: Managed Session vs. Provider Conversation

- Status: Accepted
- Date: 2026-07-12

## Context

UAM controls a detached terminal host while each provider controls its own
conversation database. These lifecycles overlap but are not identical. Attaching
to a live terminal is deterministic; starting a stopped provider may be exact or
may rely on a provider's "continue latest" behavior.

Treating the two objects as one caused confusing outcomes when multiple retained
sessions used the same provider and working directory. In particular, an old
provider without an addressable conversation ID could resume a different recent
conversation even though the selected UAM row was unambiguous.

## Decision

UAM distinguishes a **Managed Session** from a **Provider Conversation**:

- A Managed Session is UAM's stable record and detached host identity. Its UAM
  ID, session name, provider, workspace, display metadata, and persisted order
  remain stable across a resume.
- A Provider Conversation is provider-owned state. UAM records a provider ID or
  creates isolated provider state when the provider supports it.
- **Attach** reconnects to a running host and never invokes provider resume.
- **Resume** launches a provider for a stopped Managed Session. The launch is
  classified as exact or heuristic before it occurs.

Multiple Managed Sessions may use one Workspace. This is useful for parallel
tasks, but every provider process sees and can edit the same filesystem. UAM
shows a warning when more than one running session shares a Workspace.

### Exact and heuristic resume

The current provider behavior is:

| Provider | Exact when | Heuristic fallback |
|---|---|---|
| Claude Code | UAM seeded and retained a provider session ID | `--continue` for older records or provider versions that could not seed an ID |
| GitHub Copilot CLI | Always for UAM-created records; the UAM ID is used as the provider name | None |
| OpenCode | The UAM identity integration learned a `ses_…` ID | `-c` continues OpenCode's latest conversation |
| Oh My Pi | The Managed Session has its dedicated provider state directory | Legacy records use `-c` without isolated state |
| OpenAI Codex | Not currently available | `resume --last` |
| Hermes Agent | Not currently available | Resume is unsupported; create a new Managed Session |

Other providers may declare exact, heuristic, or unsupported resume through the
same contract.

### Ambiguity guard

Before a heuristic resume, UAM counts retained sessions for the same provider
and normalized Workspace. If more than one exists, UAM stops before launching
the provider:

- CLI callers must retry `attach` or `restart` with `--allow-latest`.
- The TUI identifies the selected provider, session, and action and asks for
  confirmation. Confirming permits the provider's latest-conversation behavior;
  cancelling has no side effects.

This guard is intentionally conservative. A unique retained session can use the
provider fallback without another confirmation, but UAM still cannot prove
provider state that it does not own.

### OpenCode `/new`

OpenCode's `/new` command starts another Provider Conversation inside the same
running Managed Session. It does not create a second UAM row or a second
detached host. UAM's OpenCode identity integration follows the current root
conversation so a later exact resume targets the conversation last selected in
that Managed Session.

Use `uam new` or dispatch another session when two independently attachable UAM
sessions are required. Using the same Workspace is allowed, with the shared-file
risk described above.

### Workspace isolation

UAM does not automatically create Git worktrees, branches, stashes, or commits.
Automatic isolation would change repository topology and provider-visible paths,
and could silently alter established workflows. Users who need filesystem
isolation create a worktree or separate checkout first and dispatch each Managed
Session in the desired directory.

## Alternatives considered

### Always continue the provider's latest conversation

Rejected because selecting one UAM row could resume another conversation when
several records share a Workspace.

### Refuse all heuristic resume

Rejected because legacy records and providers without addressable IDs would
become unusable after a reboot or stop. Explicit confirmation preserves that
compatibility without pretending the target is exact.

### Create a worktree for every Managed Session

Rejected as a default because it changes filesystem and Git behavior, requires
branch-management policy, and does not suit non-Git workspaces. It remains a
user-controlled option.

### Treat provider `/new` as a new Managed Session

Rejected because the command occurs inside a provider-owned terminal protocol.
It does not create a new host and cannot provide an independently attachable UAM
session.

## Consequences

- Session selection is safe by default when resume would otherwise be
  ambiguous.
- Exact resume improves as providers expose stable identifiers; legacy records
  remain compatible.
- A Managed Session can outlive several provider-side conversation switches,
  which is visible in the terminology but does not multiply dashboard rows.
- Users retain control over worktrees and other repository isolation.
- Schema v4 adds profile selection and overrides. Older records migrate with an
  adjacent backup before the replacement write; unknown fields still round-trip.
- Runtime attachment state is deliberately absent from the durable record. See
  [terminal client/session ownership and protocol v2](0003-terminal-client-session-ownership-and-protocol-v2.md)
  for the live ownership contract.
