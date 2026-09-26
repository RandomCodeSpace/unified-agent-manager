# Terminal support retired

UAM's TUI, PTY host, attach protocol, and terminal provider adapters were
removed on 2026-09-26. Current UAM supports Copilot through the web/SDK only.
Use `uam web` to start it, and `uam help` for the remaining service commands.

Saved terminal records and profiles are preserved. The new binary cannot
attach to or stop old terminal hosts. Stop them with the older binary before
replacing it. Provider executables and their conversation data are not removed.

See the [web guide](web.md) for current operations. The
[previous terminal implementation](https://github.com/RandomCodeSpace/unified-agent-manager/tree/ce17de920971fef2d52d2d1d00731e26c3453e0f)
remains available in Git history.
