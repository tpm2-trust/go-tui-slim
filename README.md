# go-tui-slim

The terminal side of a Go program on Linux that asks a person for things,
secrets among them, with the standard library and `golang.org/x/sys` alone:
a full screen that redraws only what changed, questions on a terminal or
from a pipe, the terminal given back on every way out, and secrets kept out
of swap, core files and strings.

Used by kira (`kira control`), kira-vault (its control
panel) and aSSH (the process lock). Part of the tpm2-trust workspace; owned by neither project.

## tui: the screen

```go
t := tui.Open(os.Stdin, os.Stdout)     // the terminal
if err := t.Enter(); err != nil { … }  // alternate screen, raw keys; given back on a signal too
defer t.Leave()
cols, rows, ok := t.Size()
s := tui.NewScreen(os.Stdout, tui.Colour(), tui.GlyphsFor(tui.UTF8Locale()))
s.Draw(rows, cols)                     // only the rows that changed
k, ok, err := tui.NewReader(t.FD()).Read(50 * time.Millisecond)
```

Text is measured in the cells it takes (`Cells`, `Layout`, `Fit`, `Sanitize`,
`StripSGR`); `Columns` puts panes side by side; `StartHush` keeps the
process's own output off a frame while it is drawn, and hands it over after.

## panel: a control panel

```go
err := panel.Run(src, panel.Config{In: os.Stdin, Help: keysHelp, Loading: "Looking ...", Again: "Looking again ..."})
```

Sections on the left, the selected section's steps and the selected step's
detail on the right (what it does, how it stands, the command it equals, what
it destroys), the keys at the bottom; side by side from 72 columns, one at a
time below. The program gives the `Source`: the header, the sections and their
steps, their collection in the background (`Collect`: functions the loop
applies, so the state is the loop's alone), and `Run`, which runs a step with
the screen given back. The frame is up before anything came. `State`'s `Key`
and `Frame` are pure: a program's own model is tested on them without a
terminal.

## prompt: questions

```go
p := prompt.New(os.Stdin, os.Stdout)
name, err := p.Input("Name", "What the machine is called", "", nil)
ok, err := p.Confirm("Go on?", "", false)
v, err := prompt.Select(p, "Mode", "", []prompt.Option[string]{…}, "auto")
pin, err := p.Password("PIN", "", nil)  // a []byte; the caller wipes it
defer secret.Wipe(pin)
```

On a terminal a prompt reads key by key (Ctrl-C, Ctrl-D, Esc cancel:
`ErrCancelled`; a line too long is refused whole: `ErrTooLong`); from a pipe
it reads one line and nothing past it. A menu takes the arrow keys on a
terminal that says its width, numbers anywhere else.

`Password` reads into `Prompter.SecretBuffer()` - a program that keeps its
secrets in locked memory sets it (`secret.LockedBuffer`) - or, unset, a
buffer of `MaxSecret` bytes. The answer is that buffer; no copy is made.

## termguard: the terminal given back

```go
restore, err := termguard.Hold{Change: func(t *unix.Termios) { … }, Flush: true}.Take(fd)
defer restore()
```

Given back on the caller's return and before a signal ends the process.

## secret: secrets in memory

```go
if err := secret.LockProcess(); err != nil { … }  // first thing: mlockall, not dumpable
buf, err := secret.LockedBuffer(512)              // one buffer locked; err: it is not
secret.Wipe(buf)                                  // to its full capacity
```

`LockProcess` needs `CAP_IPC_LOCK` or an unlimited `RLIMIT_MEMLOCK`
(`LimitMEMLOCK=infinity`, `--ulimit memlock=-1`); under a finite limit
without the capability it refuses rather than let the Go runtime die at its
next mapping. The process is not dumpable either way.

## Tests

```
GOWORK=off go test -count=1 ./...
```

The lock of the whole process is tested where it can be taken: as root, or
with an unlimited `RLIMIT_MEMLOCK`; elsewhere that test says why it skips.
