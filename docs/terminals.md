# Terminal and OS support

`uam` usually runs on a remote host while the terminal drawing it is whatever
client you happen to SSH from — Windows Terminal today, Termius on a phone
tomorrow, a VS Code or JetBrains pane the day after. The binary is a constant;
the client terminal is the variable, and it can change on every attach. This
page states the support promise and how `uam` adapts to the client instead of
assuming one.

## Host operating systems

| Tier | Systems | Promise |
|---|---|---|
| Full support | Ubuntu, Debian, Arch Linux, UBI8, RHEL (AMD64/ARM64) | Same behavior everywhere, verified in CI |
| Supported | macOS (Intel and Apple silicon) | Same behavior, developer-tested |
| Best effort | Any other Linux | Expected to work; not gated in CI |
| Not supported | Native Windows | No Unix PTYs, sockets or process groups |

The binary is built with `CGO_ENABLED=0`: it is fully static, links no distro
libraries (no glibc/musl split, no libtinfo, no terminfo database), and talks
to the kernel directly. Distro independence therefore holds by construction —
the `Distros` CI workflow keeps the construction honest by running the
real-PTY end-to-end suites inside `ubuntu:24.04`, `debian:12`,
`archlinux:latest`, `redhat/ubi8` and `rockylinux:9` (the RHEL 9 stand-in)
containers on every change.

Windows is supported **as a client only**: Windows Terminal or PowerShell
SSHing to a Linux/macOS host running `uam`.

## Client terminals

First-class, exercised regularly:

- **Windows Terminal / PowerShell** over SSH
- **Termius** (mobile — the dashboard has a dedicated ≤78-column layout for a
  phone with the keyboard up)
- **VS Code** integrated terminal (xterm.js)
- **JetBrains IDEs** integrated terminal (JediTerm)

Anything speaking xterm-class escape sequences is expected to work.

## How adaptation works

Every one of those clients advertises the same `TERM=xterm-256color` while
rendering differently, so `uam` never sniffs `TERM`. It measures, once per
startup, before the first frame:

1. **Ambiguous-width probe.** The dashboard's marks (`●`, `○`, `◆`, the status
   shades) are East-Asian-Ambiguous characters: some fonts and terminals draw
   them one cell wide, others two — the classic symptom is a board whose
   columns shear diagonally. `uam` prints one probe glyph, asks the terminal
   where the cursor landed (`ESC[6n`), and erases it. A two-cell answer
   switches the **entire** vocabulary to a plain-ASCII set (a mixed vocabulary
   is harder to read than a degraded one). A terminal that never answers is
   left on the full Unicode set.
2. **Locale check.** A non-UTF-8 locale (`LC_ALL`/`LC_CTYPE`/`LANG`) selects
   the ASCII set outright — no probe needed to know UTF-8 bytes will mojibake.
3. **Color negotiation.** Handled separately by the rendering stack: truecolor,
   256-color, 8-color and monochrome all work. No datum is ever carried by hue
   alone — every mark has a distinct glyph and the status column has distinct
   words, so `NO_COLOR` or a monochrome terminal loses color and nothing else.

### Overrides

| Variable | Effect |
|---|---|
| `UAM_ASCII=1` | Force the ASCII glyph set (beats everything) |
| `UAM_WIDE=0` | Trust the terminal to draw ambiguous glyphs narrow; skip the probe |
| `UAM_NO_MOUSE=1` | Disable mouse reporting; restores native drag-to-copy |
| `NO_COLOR=1` | Standard color kill switch |

### Diagnosing a client

Run `uam doctor` **from the client in question** — the terminal section is
measured by whichever terminal runs the command:

```
terminal  glyphs=unicode  ambiguous_width=1  utf8=yes  locale=LANG=C.UTF-8  term=xterm-256color  colorterm=truecolor
```

Every input to the glyph-set decision is on that line, plus the two usual
suspects for "the colors look flat":

- `colorterm=unset` over SSH means the client's truecolor capability was not
  forwarded. Fix it client-side in `~/.ssh/config` and host-side in
  `sshd_config`:

  ```
  # client ~/.ssh/config
  Host your-vps
      SetEnv COLORTERM=truecolor

  # host /etc/ssh/sshd_config
  AcceptEnv COLORTERM
  ```

- A `locale=` entry without `UTF-8` means the SSH client forwarded a locale
  the host has not generated (Debian/UBI minimal images ship almost none).
  Either generate it on the host (`locale-gen`, or `dnf install glibc-langpack-en`
  on UBI/RHEL) or stop forwarding `LANG` from the client. Until then `uam`
  renders in its ASCII set rather than mojibake.

## Known client quirks

| Client | Quirk | Handling |
|---|---|---|
| Windows Terminal | Does not forward `COLORTERM` over SSH | `SetEnv`/`AcceptEnv` above |
| Termius (mobile) | Half the screen is keyboard while operating | Compact board fits 40×12; tap a row to select, then tap its explicit `Attach` or `Resume` action |
| VS Code terminal | Fonts often draw ambiguous glyphs wide | Caught by the startup probe |
| JetBrains (JediTerm) | Renders its own font fallback; occasional wide ambiguous glyphs | Caught by the startup probe |
| Any client | Terminal left odd after a crash | `uam` writes a reset sequence on TUI exit; `reset(1)` as a last resort |
