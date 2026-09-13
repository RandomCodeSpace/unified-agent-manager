# Kitty keyboard flag replay on re-attach

Date: 2026-09-12
Issue: #93
Scope: `internal/vterm/vterm.go` (`dispatchCSI`, `Redraw`, `writeReplayModes`),
`internal/session/attach.go` (`screenEnter`, `screenReset`, `screenExit`,
`attachOutputFilter`). Sources checked at the pinned commits below; line
numbers are from those commits.

- kitty keyboard protocol spec: <https://sw.kovidgoyal.net/kitty/keyboard-protocol/>
- kitty source `kovidgoyal/kitty` @ `9710eb4` (`kitty/screen.c`, `kitty/screen.h`, `kitty/vt-parser.c`)
- tmux source `tmux/tmux` @ `e880cf6` (`input.c`, `tty.c`, `tty-features.c`, `input-keys.c`, `options-table.c`, `tmux.1`)
- zellij source `zellij-org/zellij` @ `a162323` (`zellij-server/src/panes/grid.rs`, `zellij-server/src/panes/terminal_pane.rs`, `zellij-client/src/lib.rs`, `zellij-utils/assets/config/default.kdl`)

## Conclusion

The kitty keyboard protocol is a per-screen stack of flag bytes, not a mode
bit. `dispatchCSI` drops every `CSI > … u`, `CSI < … u` and `CSI = … u`
(`vterm.go:285-294`), so `Redraw` has nothing to re-push and a re-attached
client is left in legacy key encoding while the agent keeps parsing `CSI u`
chords. The fix is to track a bounded stack per screen in `Terminal`, replay
the active screen's stack as pushes at the top of `writeReplayModes`, and pop
exactly that many entries on detach.

tmux and zellij do not have this problem because they never let a pane's
keyboard state reach the outer terminal: both hold the outer terminal at one
fixed enhancement level for the life of the client and re-encode keys per
pane. uam is passthrough, so it has to replay.

Because `attachOutputFilter` contains the agent's own `?47/?1047/?1049`
toggles (`attach.go:653`, `attachAltModes`) and the client owns the physical
alternate screen (`screenEnter`, `attach.go:73`), the physical terminal runs
exactly one stack during an attachment: its alternate-screen stack. The
agent's main-screen and alt-screen pushes interleave on it. The vterm still
needs two stacks (that is the model the agent believes it is driving), but
replay and detach act on the active one only.

## Findings

### 1. Push / pop / set semantics; stacks are per screen

Spec, "Progressive enhancement":

- Set: `CSI = flags ; mode u`. "The second, `mode` parameter is optional
  (defaulting to 1) and specifies how the flags are applied. The value `1`
  means all set bits are set and all unset bits are reset. The value `2` means
  all set bits are set, unset bits are left unchanged. The value `3` means all
  set bits are reset, unset bits are left unchanged."
- Push / pop: "`CSI > flags u` # for push, if flags omitted default to zero"
  and "`CSI < number u` # to pop number entries, defaulting to 1 if
  unspecified".
- Per screen: "Terminals must maintain separate stacks for the main and
  alternate screens." The note under it: "a program that uses the alternate
  screen such as an editor, can change the keyboard mode in the alternate
  screen only, without affecting the mode in the main screen or even knowing
  what that mode is."
- Flag bits: 1 disambiguate escape codes, 2 report event types, 4 report
  alternate keys, 8 report all keys as escape codes, 16 report associated
  text.
- Quickstart tells applications to send `CSI > 1 u` "at application startup
  if using the main screen or when entering alternate screen mode" and
  `CSI < u` "at application exit if using the main screen or just before
  leaving alternate screen mode". That is the pattern uam's agents follow
  (`attach.go:1004-1007`, `decodeEnhancedCtrlKey` comment).

kitty's implementation (the reference) makes the edge cases concrete:

- Storage: `uint8_t main_key_encoding_flags[8], alt_key_encoding_flags[8],
  *key_encoding_flags;` (`screen.h:167`). Bit `0x80` marks a slot in use, the
  low seven bits hold the flags.
- Dispatch (`vt-parser.c:1363-1385`): `CSI ? u` reports; `CSI = … u` sets
  with mode defaulting to 1; `CSI > … u` pushes with flags defaulting to 0;
  `CSI < … u` pops with count defaulting to 1. Bare `CSI u` is SCORC (restore
  cursor), which is why `dispatchCSI` must branch on the prefix before the
  `'u'` case.
- Set (`screen.c:2087-2101`) rewrites the topmost in-use slot, or slot 0 when
  nothing is in use, and marks it in use. It never changes the depth.
- Push (`screen.c:2104-2118`) marks the current top in use, then writes the
  new value one slot above. On a fresh screen this turns slot 0 into an
  explicit base entry (value 0) and puts the pushed flags in slot 1.
- Current value (`screen.c:2071-2076`) is the topmost in-use slot, or 0 when
  none is in use.
- Screen switch (`screen_toggle_screen_buffer`, `screen.c:1917-1940`) only
  swaps the `key_encoding_flags` pointer between the two arrays. Neither
  stack is cleared on entering or leaving the alternate screen.
- Reset (`do_screen_reset`, `screen.c:209`, memsets at `225-226`) zeroes
  both stacks. It is called with `is_hard_reset=false` from
  `screen_soft_reset` (`screen.c:268-270`), and the memsets are not guarded by
  `is_hard_reset`, so in kitty DECSTR (`CSI ! p`) clears both screens'
  stacks. That is kitty-specific; the spec does not say it, and other
  terminals were not checked.

### 2. What `CSI ? u` returns

Spec: "The program running in the terminal can query the terminal for the
current values of the flags by sending: `CSI ? u`. The terminal will reply
with: `CSI ? flags u`". kitty formats it as `"?%uu"` from the current value
(`screen.c:2079-2084`), so a terminal with nothing pushed answers
`ESC [ ? 0 u`.

Detection, spec "Detection of support for this protocol": "An application
can query the terminal for support of this protocol by sending the escape code
querying for the current progressive enhancement status followed by request
for the primary device attributes. If an answer for the device attributes is
received without getting back an answer for the progressive enhancement the
terminal does not support this protocol."

uam forwards the agent's `CSI ? u` to the physical terminal unchanged
(`dispatchCSI` returns without recording it; the attach path is passthrough)
and forwards the reply back through `stdinFilter`, where `seqPoisons` treats
`CSI ? … u` as a terminal reply rather than a keystroke
(`attach.go:1085-1090`). With no client attached the query goes unanswered,
so an agent that probes at startup while detached concludes "unsupported".
zellij answers this query itself from the pane's state (finding 4); tmux
drops it. See Open questions.

### 3. Stack depth; pop past empty; push past full

Spec: "Terminals should limit the size of the stack as appropriate, to
prevent Denial-of-Service attacks." / "If a pop request is received that
empties the stack, all flags are reset." / "If a push request is received and
the stack is full, the oldest entry from the stack must be evicted."

kitty: 8 slots per screen (`screen.h:167`). Push when the top is already the
last slot `memmove`s everything down one, dropping slot 0 (`screen.c:2114`),
so at most 7 pushed values survive above the base. Pop (`screen.c:2121-2129`)
clears up to `num` in-use slots from the top and stops when none are left;
popping more than exist is harmless and the current value becomes 0. A large
`CSI < n u` is therefore a safe "empty the stack" on kitty and, by the spec
wording, on any conforming terminal.

### 4. How tmux and zellij replay flag state to a freshly attached client

Neither replays anything. Both pin the outer terminal to one level and
translate keys per pane.

tmux (`e880cf6`):

- tmux does not parse kitty sequences from panes at all. The CSI dispatch
  table (`input.c:318-348`) has a single `'u'` entry,
  `{ 'u', "", INPUT_CSI_RCP }`; bytes `0x3c-0x3f` (`<`, `=`, `>`, `?`) are
  collected as intermediates (`input.c:577`) and matched exactly by
  `bsearch` in `input_csi_dispatch`, so `CSI > 1 u`, `CSI < u`,
  `CSI = … u` and `CSI ? u` from a pane are all discarded and the query is
  never answered.
- tmux does parse xterm modifyOtherKeys: `CSI > 4 ; m m` (`INPUT_CSI_MODSET`,
  `input.c:336`, handler `1527-1545`) and `CSI > 4 n` (`INPUT_CSI_MODOFF`,
  `input.c:338`, handler `1546-1559`), stored as two bits in `screen->mode`:
  `MODE_KEYS_EXTENDED` (`0x8000`) and `MODE_KEYS_EXTENDED_2` (`0x40000`)
  (`tmux.h:692-703`). It is a single level, not a stack, and
  `screen_alternate_on/off` (`screen.c:692`, `728`) neither save nor restore
  `mode`, so it is not per screen either.
- Outer terminal: on attach `tty_update_features` (`tty.c:553-564`, called
  from `tty.c:319`) writes the `Eneks` capability, `\E[>4;2m`
  (`tty-features.c:239-242`), whenever the `extended-keys` option is on; on
  detach `tty_stop_tty` (`tty.c:454`, line `509`) writes `Dseks`, `\E[>4m`.
  `tmux.1`: "tmux will always request extended keys itself if the terminal
  supports them." The pane's state never reaches the outer terminal.
- Keys are decoded by tmux and re-encoded for the pane according to
  `s->mode & EXTENDED_KEY_MODES` (`input-keys.c:690-703`), in the format
  chosen by `extended-keys-format` (`csi-u` or `xterm`,
  `options-table.c:106-108`, `422-428`).

zellij (`a162323`):

- Per pane, the grid keeps one boolean,
  `supports_kitty_keyboard_protocol` (`grid.rs:870`), with the comment
  "Zellij only supports the first 'progressive enhancement' layer of the
  kitty keyboard protocol". `CSI > n u` sets it to `n > 0`
  (`grid.rs:5368-5378`); `CSI < … u` clears it regardless of the count
  (`5379-5384`); `CSI = n u` behaves like push, "kitty keyboard protocol
  without the stack, just setting" (`5396-5404`); `CSI ? u` is answered by
  the grid itself with `ESC [ ? 1 u` or `ESC [ ? 0 u` written back into the
  pane (`5385-5395`). Reset clears it (`grid.rs:2810`).
- It is per screen: entering the alternate screen stashes the boolean in
  `AlternateScreenState` (`grid.rs:5142-5162`) and leaving swaps it back
  (`apply_contents_to`, `grid.rs:5716-5725`).
- Outer terminal: the client writes `ESC [ > 1 u`
  (`ENTER_KITTY_KEYBOARD_MODE`, `lib.rs:58`) once, right after entering its
  alternate screen and clearing attributes (`lib.rs:972-981`, also `856`),
  unless `support_kitty_keyboard_protocol false` is configured
  (`default.kdl:476-480`, default true). On exit or detach the teardown
  string starts with `ESC [ < 1 u` (`EXIT_KITTY_KEYBOARD_MODE`, `lib.rs:59`)
  and then leaves the alternate screen (`terminal_teardown_message`,
  `lib.rs:1764-1780`; used at `1380-1386` and `1612-1618`).
- Keys are decoded centrally and re-encoded per pane:
  `adjust_input_to_terminal_with_kitty_keyboard_protocol` when the pane's
  boolean is set, the legacy encoder otherwise (`terminal_pane.rs:439-448`).

What this means for uam: the multiplexer answer is "own the outer terminal's
keyboard mode and translate". That requires a full key decoder and
re-encoder in the attach client, which uam does not have (it only decodes the
prefix chord, `decodeEnhancedCtrlKey`). Replaying the agent's stack is the
passthrough-compatible alternative and is what #93 asks for.

### 5. What a detaching client must emit

Spec basis: pops that empty the stack reset all flags; stacks are per
screen. uam runs the whole attachment inside its own alternate screen
(`screenEnter`, `attach.go:73`), and the agent's alt-screen toggles never
reach the physical terminal (`attachAltModes`, `attach.go:653`). So:

- Every push the agent made during the attachment sits on the physical
  terminal's alternate-screen stack.
- `?1049l` (end of `screenExit`, `attach.go:111`) returns the physical
  terminal to its main-screen stack, which uam never touched. The user's
  shell is in the right mode after detach even if nothing were popped.
- The alternate-screen stack is not cleared by the switch (kitty
  `screen.c:1917-1940`; the spec says nothing about clearing). Anything left
  on it is inherited by the next program that enters the alternate screen:
  vim, the next `uam` attach, anything. That is why the detach must actually
  empty it, before `?1049l`.

The current `screenReset` prefix `CSI < u` + `CSI = 0;1 u`
(`attach.go:98-99`) pops one entry and zeroes whatever is then current. With
two pushes (agent plus a child TUI it spawned), one entry survives beneath a
zeroed top; the next `CSI < u` from any program on that screen brings it
back. The zeroing set masks the single-push case but does not empty the
stack. On kitty specifically the later `CSI ! p` in the same `screenReset`
happens to wipe both stacks (finding 1), which is why the leak has not been
visible there; that is not portable.

## State the vterm Terminal must track

Per screen, alongside `main`/`alt` (either a field on `screen` or two fields
on `Terminal` selected by `onAlt` the way `active()` selects grids):

```go
// kittyFlags is one screen's progressive-enhancement state: the stack of
// pushed flag bytes plus the base value in force when the stack is empty
// (only CSI = … u can make base non-zero).
type kittyFlags struct {
	stack []uint8 // bottom first; len <= kittyStackMax
	base  uint8
}
```

`dispatchCSI` handling, placed before the early return at
`vterm.go:285-294` (the other prefixed sequences keep returning):

| Sequence | Effect on the active screen's `kittyFlags` |
| --- | --- |
| `CSI > f u` (`f` default 0) | `stack = append(stack, f & 0x1f)`; if `len > kittyStackMax`, drop `stack[0]` |
| `CSI < n u` (`n` default 1) | pop `min(n, len)`; if that empties the stack, `base = 0` |
| `CSI = f ; m u` (`m` default 1) | target = top of stack if non-empty else `base`; m=1 `target = f`, m=2 `target \|= f`, m=3 `target &^= f` |
| `CSI ? u` | no state change |
| `reset()` (`vterm.go:667-678`) | both screens back to `{nil, 0}` |

`current()` = top of stack if non-empty, else `base`. Mask to the five
defined bits (`0x1f`) so a garbage parameter is never replayed verbatim.

`kittyStackMax = 7`: kitty keeps 8 slots of which one is the base, so a
conforming agent can rely on at most 7 pushes surviving. Tracking more than
the reference terminal keeps would replay entries the physical terminal had
already evicted.

Per-screen: keep one `kittyFlags` for `main` and one for `alt`. `switchAlt`
(`vterm.go:479`) needs no change: per the spec and kitty, each screen keeps
its stack across switches; nothing is saved or cleared. Expose the active
screen's depth (for example `KittyStackDepth() int`) for the detach path.

Tests to extend: `TestPrivatePrefixedCSIDoesNotDraw`
(`internal/vterm/private_csi_test.go`) already feeds all four sequences and
asserts they do not draw; add assertions on `Redraw()` output for push, push
twice, pop past empty, set-with-empty-stack, and alt-screen isolation.

## Replay sequence `writeReplayModes` must emit

Emit at the top of `writeReplayModes` (`vterm.go:820`), before the DEC
private modes, so the keyboard state is in place before any mode that can
generate input (mouse, focus). `Redraw` calls `writeReplayModes` right after
`ESC [0m ESC [2J ESC [H` (`vterm.go:755-756`), which is after the client has
entered its alternate screen. That ordering is required: the pushes must land
on the alternate-screen stack the agent will later pop.

For the active screen's `kittyFlags` `k`:

```
if k.base != 0:              ESC [ = <base> ; 1 u      (CSI = flags ; 1 u)
for e in k.stack, bottom→top: ESC [ > <e> u             (CSI > flags u)
```

Examples:

- agent did `CSI > 1 u`, then spawned a TUI that did `CSI > 15 u`:
  replay `\x1b[>1u\x1b[>15u`
- agent did only `CSI = 1 ; 1 u`: replay `\x1b[=1;1u`
- nothing pushed, base 0: replay nothing (same "differs from power-on
  default" rule `replayModes` uses)

Why pushes rather than one `CSI = current ; 1 u`: the agent will pop later.
Had replay used set, that pop would empty the physical stack and reset flags
to 0 while the agent believes it is back on its own flags. Replaying the
entries one-for-one keeps every later pop landing on the value the agent
expects, and gives the detach path an exact count.

Terminals that do not implement the protocol ignore all three forms, which
matches the "terminals ignore sequences they don't implement" contract
`screenReset` already relies on (`attach.go:95-97`).

## Detach sequence

Replace the fixed `CSI < u` + `CSI = 0;1 u` prefix of `screenReset`
(`attach.go:98-99`) with a count the client knows:

```
ESC [ < <d> u          d = depth of the active screen's stack; omit when 0
ESC [ = 0 ; 1 u        zero the base (agent used set, or d is stale)
… existing mouseReset, ?1004l, ?2004l, ?2026l, CSI ! p, ESC >, ESC ( B, ?25h
ESC [ ? 1007 r
ESC [ ? 1049 l         back to the main screen and its untouched stack
```

The pops and the set must precede `?1049l`: they target the alternate-screen
stack. After `?1049l` they would hit the user's main-screen stack, and
`CSI = 0;1 u` there would clobber a shell that runs with enhancements on
(fish 4 pushes flags at its prompt).

Where `d` comes from: `attachOutputFilter` already parses every outgoing CSI
for the mouse and alt-screen rewrites (`attach.go:662`), so counting
`CSI > … u` / `CSI < n u` there is the smallest change and keeps working when
the host has died. A host-side `KittyStackDepth()` gives the same number
while the host is alive. When the count is unknown, `ESC [ < 7 u` (the
`kittyStackMax`) is the fallback: popping more entries than exist is defined
by the spec and by kitty, and leaves the stack empty.

## Open questions

1. Detached probes. An agent that starts with no client attached sends
   `CSI ? u` + `CSI c` into a PTY nobody answers; the DA1 reply never comes
   either, so behaviour depends on the agent's timeout. Should the host
   answer `CSI ? u` itself from `current()` the way zellij does
   (`grid.rs:5385-5395`)? That would also make an attached client's real
   reply redundant and possibly conflicting. Separate change from #93.
2. Two vterm stacks, one physical stack. Because the agent's alt-screen
   toggles never reach the physical terminal, a child TUI that dies on the
   agent's alt screen without popping leaves its entry on the physical
   stack, whereas on a real terminal the agent's main screen would be
   unaffected. If it bites, `switchAlt(false)` could hand the host a
   synthetic `CSI < len(alt.stack) u` for the client. Not needed for replay.
3. `kittyStackMax` across terminals. 7 is kitty's number; foot, ghostty,
   wezterm, alacritty and iTerm2 were not checked. A terminal with a smaller
   stack evicts entries uam still tracks; replay would then push more than
   the terminal keeps, which is self-correcting (it evicts again) but makes
   the detach pop count over-estimate, which is harmless.
4. Observer clients. `Redraw` goes to observers too (`host_attach.go:77`);
   their terminals receive the pushes and start sending `CSI u` chords the
   observer role discards (`stdinFilter.result`). Harmless, and `screenReset`
   already runs for both roles, so they pop too.
5. modifyOtherKeys. `CSI > 4 ; n m` / `CSI > 4 n` is the xterm equivalent
   and is dropped by the same early return. It is a single level, not a
   stack; it would replay as `CSI > 4 ; <n> m` and reset with `CSI > 4 m`.
   Out of scope for #93 unless an agent is found that uses it instead of
   `CSI u`.
6. DECSTR side effect on kitty. `screenReset`'s `CSI ! p` wipes both of
   kitty's stacks, including the user's main-screen stack. Pre-existing, not
   introduced by this change; noted because it hides the leak in finding 5
   on kitty only.
