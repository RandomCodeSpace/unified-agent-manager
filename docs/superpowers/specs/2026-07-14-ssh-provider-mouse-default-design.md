# SSH Provider Mouse Default

## Context

UAM's attach client currently treats `UAM_ATTACH_MOUSE=auto` or an unset value
as provider mouse enabled for local terminals and disabled for SSH terminals.
When disabled, the attach output filter removes DEC mouse modes requested by a
provider. OpenCode and OMP rely on those modes for wheel and touch scrolling, so
their internal views cannot scroll through UAM over SSH.

The existing SSH default prioritized terminal-owned selection and right-click
paste. The product priority is now provider scrolling. Clipboard behavior is
best effort and can use terminal keyboard bindings such as `Ctrl+V`,
`Ctrl+Shift+V`, or `Shift+Insert`.

## Decision

Provider mouse reporting is enabled by default for every attach viewer,
including SSH viewers. The environment variable remains the compatibility and
escape hatch:

| `UAM_ATTACH_MOUSE` value | Behavior |
|---|---|
| unset or `auto` | Preserve provider mouse modes locally and over SSH. |
| `on` | Preserve provider mouse modes locally and over SSH. |
| `off` | Remove supported provider mouse modes so the terminal owns mouse gestures. |
| invalid | Use the `auto` behavior and preserve provider mouse modes. |

This policy remains provider-neutral. UAM preserves mouse modes only when the
provider requests them; it does not synthesize mouse events or force mouse mode
for providers that do not use it.

## Implementation

`attachMouseEnabled` will resolve every value except explicit `off` to true.
The attach output filter, stdin filter, socket framing, terminal cleanup, and
alternate-screen ownership remain unchanged.

The output path remains:

1. The provider emits a DEC mouse-mode enable sequence.
2. The session host records and replays the sequence.
3. The attach output filter preserves it unless the viewer selected `off`.
4. The terminal converts wheel or touch gestures into mouse reports.
5. The stdin filter and framed attach connection forward those bytes unchanged.

On detach, UAM continues disabling mouse modes before restoring the caller's
terminal. This prevents provider state from leaking into the dashboard or
shell.

## Compatibility and Trade-offs

No CLI flags, protocols, session metadata, provider arguments, store schema, or
terminal cleanup sequences change. Existing `on` and `off` overrides remain
valid.

With provider mouse enabled, some terminals route clicks, drag selection, and
right-click gestures to the provider instead of handling them locally. Users
who prioritize terminal-owned selection or paste can set
`UAM_ATTACH_MOUSE=off`. Keyboard paste remains terminal-dependent because UAM
can forward only bytes supplied by the SSH client.

## Testing

Regression coverage will verify:

- unset and `auto` preserve provider mouse modes over SSH;
- `on` preserves provider mouse modes over SSH;
- only explicit `off` suppresses provider mouse modes;
- invalid values follow `auto` and preserve provider mouse modes;
- OpenCode's grouped `1000/1002/1003/1006` enable sequence survives the default
  SSH attach path;
- SGR wheel input remains byte-exact through the stdin filter;
- detach cleanup still disables mouse modes and restores the terminal;
- the complete race-enabled test suite remains green.

## Documentation and Rollout

The terminal ownership ADR and user-facing documentation will describe
scrolling as the default priority over SSH and `off` as the selection/paste
opt-out. This is a default-policy change with no migration or irreversible
state, so it can ship as a patch release and be reverted independently.

## Acceptance Criteria

- OpenCode and OMP wheel/touch scrolling works through a default SSH attach.
- Local attach behavior is unchanged.
- `UAM_ATTACH_MOUSE=off` retains the previous SSH mouse behavior.
- Provider output, input bytes, attach lifecycle, and terminal cleanup remain
  otherwise unchanged.
