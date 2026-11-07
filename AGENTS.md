# Notes for agents working on go-tui-slim

- **What it is:** the full screen, the questions and the secrets of a terminal
  program on Linux - `tui`, `panel`, `prompt`, `termguard`, `secret`. It knows nothing
  of its users: no name, path, text or notion of kira's or the vault's.
  They pass their own (a prompt's buffer, a screen's rows).
- **The standard library and `golang.org/x/sys` alone.** No other dependency:
  programs that run as root and take PINs link this.
- **A secret is never in a string.** It is a `[]byte` that its holder wipes
  (`secret.Wipe`); a function that takes or returns one says who wipes it.
  Nothing of a secret is echoed, logged or copied on the way.
- **The terminal is given back on every way out** the process has code for:
  its own return, a cancelled prompt, a signal (`termguard`).
- **Tests for everything.** Every exported function has a test; terminal
  behaviour is tested on a pty. Tests run with `-count=1`, wait for a state,
  never for a time; what needs a privilege says why it skips.
- **No backwards compatibility** (the workspace's rule): one API, the current.
- **Comments and docs say what it is**, not what it was.
