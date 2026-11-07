package panel

import (
	"os"
	"time"

	"github.com/tpm2-trust/go-tui-slim/tui"
)

// Source is what a panel controls.
type Source interface {
	// Header is the top row; loading is true until the first of Collect has come.
	Header(loading bool) string
	// Sections are the panel's parts with their steps. While loading, their titles
	// (the steps are not shown).
	Sections(loading bool) []Section
	// Checking says that more is still coming from Collect.
	Checking() bool
	// Collect starts finding out what the sections show, in the background: each
	// function sent is applied in the loop's goroutine, in order, and makes the
	// sections new; the channel is closed when all has come. Nothing else of the
	// Source runs while a function of it is being made, so a function carries what it
	// found and applies it, and the Source's state is the loop's alone.
	Collect() <-chan func()
	// Run runs the step at section, step of the last Sections(false), in the terminal:
	// the screen is given back for it and comes back after it.
	Run(section, step int)
}

// Config is how a panel is drawn.
type Config struct {
	In *os.File // the terminal's input
	// Out is drawn on. Nil: stdout, with what else writes there while the frame is up
	// held off it (tui.Hush) and written below the screen when a step runs or the
	// panel is left.
	Out     *os.File
	Help    string // the keys' help, on '?'
	Loading string // what the steps' pane says until the first of Collect has come
	Again   string // the status row while Refresh collects again
}

// Run is the panel on its full screen until it is left (q, Esc, Ctrl-C): the frame is
// up at once, the sections come from Collect, a step runs with the screen given back,
// and Collect runs again after it. Run returns once all Collect started has come, so
// nothing prints under what follows.
func Run(src Source, cfg Config) error {
	g := tui.GlyphsFor(tui.UTF8Locale())
	st := &State{}
	loading := cfg.Loading
	if loading == "" {
		loading = "..."
	}

	var hush *tui.Hush
	screenOut := func() *os.File {
		if cfg.Out != nil {
			return cfg.Out
		}
		h, err := tui.StartHush()
		if err != nil {
			return os.Stdout
		}
		hush = h
		return h.Screen
	}
	unhush := func() {
		if hush == nil {
			return
		}
		if out := hush.Stop(); len(out) > 0 {
			os.Stdout.Write(out)
		}
		hush.Close()
		hush = nil
	}

	var got <-chan func()
	collect := func() { got = src.Collect() }
	// waitAll applies what is still coming.
	waitAll := func() {
		for got != nil {
			f, ok := <-got
			if !ok {
				got = nil
				break
			}
			f()
		}
	}

	var term *tui.Term
	var scr *tui.Screen
	var keys *tui.Reader
	enter := func() error {
		out := screenOut()
		term = tui.Open(cfg.In, out)
		scr = tui.NewScreen(out, tui.Colour(), g)
		keys = tui.NewReader(term.FD())
		if err := term.Enter(); err != nil {
			unhush()
			return err
		}
		return nil
	}
	leave := func() {
		term.Leave()
		unhush()
	}

	isLoading := true
	if err := enter(); err != nil {
		return err
	}
	collect() // inside the hush: what it prints stays off the frame
	defer func() {
		if r := recover(); r != nil {
			leave()
			panic(r)
		}
	}()
	var m []Section
	draw := func() {
		cols, rows, ok := term.Size()
		if !ok {
			cols, rows = 80, 24
		}
		m = src.Sections(isLoading)
		v := View{Header: src.Header(isLoading), Help: cfg.Help}
		if isLoading {
			v.Loading = loading
		} else {
			st.Clamp(m)
			v.Checking = src.Checking()
		}
		scr.Draw(st.Frame(m, v, cols, rows, g), cols)
	}
	draw()
	for {
		select {
		case f, ok := <-got:
			if !ok {
				got = nil
			} else {
				f()
				isLoading = false
			}
			draw()
			continue
		case <-term.Resized():
			scr.Invalidate()
			draw()
			continue
		default:
		}
		k, ok, err := keys.Read(200 * time.Millisecond)
		if err != nil {
			leave()
			return err
		}
		if !ok {
			continue
		}
		_, rows, _ := term.Size()
		switch st.Key(k, m, max(rows/2, 5)) {
		case Quit:
			waitAll()
			leave()
			return nil
		case Refresh:
			if got == nil {
				st.Message = cfg.Again
				collect()
			}
		case Redraw:
			scr.Invalidate()
		case RunStep:
			if s := st.Selected(m); s != nil && !isLoading {
				waitAll() // a step works on all there is
				sec, step := st.Section, st.Step
				leave()
				src.Run(sec, step)
				if err := enter(); err != nil {
					return err
				}
				isLoading = true
				collect()
			}
		}
		draw()
	}
}
