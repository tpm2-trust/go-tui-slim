package panel

import (
	"bytes"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tpm2-trust/go-tui-slim/tui"
	"golang.org/x/sys/unix"
)

// sample is a panel of three sections: one whose steps are of every kind, one a step
// jumps to, and one with no steps.
func sample() []Section {
	return []Section{
		{Key: "first", Title: "First", Intro: "The first section's intro.", Steps: []Step{
			{Title: "Read it", Danger: Reads, Runnable: true, Command: "tool show"},
			{Title: "Set it", Explain: "Sets the thing.", Runnable: true, Command: "tool set"},
			{Title: "Remove it", Done: "in place", Danger: Destroys, Destroys: "the thing, whole", Runnable: true, Command: "tool remove"},
			{Title: "Blocked one", Blocked: "no adapter", Runnable: true},
			{Title: "Checked", Done: "fine", Check: true, Runnable: true},
			{Title: "Broken", Dirty: "out of date", Runnable: true},
			{Title: "Go to second", Jump: "second"},
		}},
		{Key: "second", Title: "Second", Intro: "Second's intro.", Steps: []Step{{Title: "Approve", Explain: "Approves the state.", Runnable: true}}},
		{Key: "empty", Title: "Empty", Intro: "Nothing here."},
	}
}

func text(rows []string) string { return tui.StripSGR(strings.Join(rows, "\n")) }

// The frame at the sizes a person meets: as many rows as the terminal has, none wider;
// the sections beside the detail when there is room, one at a time when not; a step's
// detail with its state, what it destroys and the command; ASCII without UTF-8; the
// loading text before anything came.
func TestFrame(t *testing.T) {
	m := sample()
	g := tui.GlyphsFor(true)
	v := View{Header: "[ X ] panel · host"}
	for _, size := range [][2]int{{80, 25}, {132, 43}, {40, 24}, {30, 12}} {
		rows := (&State{}).Frame(m, v, size[0], size[1], g)
		if len(rows) != size[1] {
			t.Errorf("%dx%d: %d rows", size[0], size[1], len(rows))
		}
		for i, r := range rows {
			if tui.Cells(tui.StripSGR(r)) > size[0] {
				t.Errorf("%dx%d: row %d is %d wide: %q", size[0], size[1], i, tui.Cells(tui.StripSGR(r)), r)
			}
		}
		s := text(rows)
		if !strings.Contains(s, "First") || !strings.Contains(s, "[ X ] panel") {
			t.Errorf("%dx%d:\n%s", size[0], size[1], s)
		}
		if wide := size[0] >= 72; wide != strings.Contains(s, "first section's intro") {
			t.Errorf("%dx%d: the intro beside the sections: %v\n%s", size[0], size[1], wide, s)
		}
	}
	st := &State{Focus: Steps, Step: 2}
	s := text(st.Frame(m, v, 100, 40, g))
	for _, want := range []string{"Remove it ⚠", "Done: in place", "⚠ Destroys: the thing, whole", "The command: tool remove", "✓ Remove it"} {
		if !strings.Contains(s, want) {
			t.Errorf("the step's detail lacks %q:\n%s", want, s)
		}
	}
	if s := text(st.Frame(m, v, 100, 40, tui.GlyphsFor(false))); strings.ContainsAny(s, "✓⚠│─›") {
		t.Errorf("Unicode in an ASCII frame:\n%s", s)
	}
	s = text((&State{}).Frame(m, View{Header: "h", Loading: "Looking ..."}, 80, 25, g))
	if !strings.Contains(s, "Looking ...") || strings.Contains(s, "Read it") {
		t.Errorf("loading:\n%s", s)
	}
	if s := text((&State{}).Frame(m, View{Header: "h", Checking: true}, 100, 25, g)); !strings.Contains(s, "· checking ...") {
		t.Errorf("checking:\n%s", s)
	}
	if s := text((&State{Help: true}).Frame(m, View{Header: "h", Help: "THE KEYS"}, 100, 25, g)); !strings.Contains(s, "THE KEYS") {
		t.Errorf("help:\n%s", s)
	}
	if rows := (&State{}).Frame(nil, v, 80, 3, g); len(rows) != 3 {
		t.Errorf("3 rows and no sections: %d", len(rows))
	}
}

// Row marks each kind of step: needs attention, done, blocked, a view, open, destroying,
// jumping.
func TestRow(t *testing.T) {
	g := tui.GlyphsFor(true)
	for _, c := range []struct {
		s    Step
		want string
	}{
		{Step{Title: "a", Dirty: "old"}, "✗ a  - old"},
		{Step{Title: "a", Done: "yes"}, "✓ a  - yes"},
		{Step{Title: "a", Blocked: "no"}, "- a  - no"},
		{Step{Title: "a", Danger: Reads}, "  a"},
		{Step{Title: "a"}, "• a"},
		{Step{Title: "a", Danger: Destroys}, "• a ⚠"},
		{Step{Title: "a", Jump: "x"}, "• a ›"},
	} {
		if got := tui.StripSGR(Row(c.s, g)); got != c.want {
			t.Errorf("%+v: %q, want %q", c.s, got, c.want)
		}
	}
}

// The keys: moving, into and out of a section, each kind of step on Enter, a jump,
// search, Esc back and then out, help, refresh, redraw, and the selection kept inside
// sections that changed.
func TestKeys(t *testing.T) {
	m := sample()
	st := &State{}
	key := func(names ...string) Cmd {
		var cmd Cmd
		for _, n := range names {
			k := tui.Key{Name: n}
			if len(n) == 1 {
				k = tui.Key{Name: tui.KeyRune, Rune: rune(n[0])}
			}
			cmd = st.Key(k, m, 10)
		}
		return cmd
	}
	if key(tui.KeyDown, tui.KeyDown, tui.KeyDown); st.Section != 2 || st.Focus != Sections {
		t.Fatalf("down past the end: %+v", st)
	}
	if key(tui.KeyEnter, tui.KeyRight, "l"); st.Focus != Sections {
		t.Fatalf("into a section with no steps: %+v", st)
	}
	st = &State{}
	if key(tui.KeyEnter); st.Focus != Steps || st.Step != 0 {
		t.Fatalf("Enter on a section: %+v", st)
	}
	for _, c := range []struct {
		step    int
		cmd     Cmd
		message string
	}{
		{0, RunStep, ""},
		{1, RunStep, ""},
		{2, RunStep, ""}, // done, not a check: runs again (its user asks)
		{3, None, "Not possible here: no adapter"},
		{4, None, "In place: fine"},
		{5, RunStep, ""},
	} {
		st.Step = c.step
		if cmd := key(tui.KeyEnter); cmd != c.cmd || st.Message != c.message {
			t.Errorf("Enter on %q: %v %q", m[0].Steps[c.step].Title, cmd, st.Message)
		}
	}
	st.Step = 6
	if key(tui.KeyEnter); st.Section != 1 || st.Step != 0 {
		t.Fatalf("a jump opens its section: %+v", st)
	}
	st = &State{}
	key("/", "a", "p", "p", "r", tui.KeyEnter)
	if s := st.Selected(m); s == nil || s.Title != "Approve" || st.Focus != Steps {
		t.Fatalf("search by title: %+v", st)
	}
	st = &State{}
	key("/", "t", "o", "o", "l", tui.KeyDown, tui.KeyDown, tui.KeyEnter) // the commands: the third hit
	if s := st.Selected(m); s == nil || s.Title != "Remove it" {
		t.Fatalf("search by command, the third hit: %+v %v", st, s)
	}
	st = &State{}
	if key("/", "z", "z", tui.KeyBackspace, tui.KeyEsc); st.Focus != Sections || st.Section != 0 {
		t.Fatalf("a search left: %+v", st)
	}
	if s := text(st.Frame(m, View{}, 100, 30, tui.GlyphsFor(true))); strings.Contains(s, "Search:") {
		t.Fatalf("the search pane after Esc:\n%s", s)
	}
	st = &State{}
	key("/", "q", "q")
	if s := text(st.Frame(m, View{}, 100, 30, tui.GlyphsFor(true))); !strings.Contains(s, "Nothing found.") {
		t.Fatalf("no hit:\n%s", s)
	}
	st = &State{Focus: Steps}
	if key(tui.KeyEsc); st.Focus != Sections {
		t.Fatalf("Esc goes back first: %+v", st)
	}
	if key(tui.KeyEsc) != Quit || key("q") != Quit || key(tui.KeyCtrlC) != Quit {
		t.Fatal("Esc, q and Ctrl-C leave")
	}
	if key("?"); !st.Help {
		t.Fatal("? shows the keys")
	}
	if key(tui.KeyEsc); st.Help {
		t.Fatal("Esc closes the help first")
	}
	if key("r") != Refresh || key(tui.KeyCtrlL) != Redraw {
		t.Fatal("r and Ctrl-L")
	}
	st = &State{Section: 99, Step: 99, Focus: Steps}
	st.Clamp(m)
	if st.Section != 2 || st.Step != 0 || st.Focus != Sections {
		t.Fatalf("clamp into a section with no steps: %+v", st)
	}
	st = &State{Section: 0, Step: 99, Focus: Steps}
	if st.Clamp(m); st.Step != 6 {
		t.Fatalf("clamp: %+v", st)
	}
	if st.Clamp(nil); st.Section != 0 || st.Selected(nil) != nil {
		t.Fatalf("clamp to nothing: %+v", st)
	}
}

func openPty(t *testing.T, cols, rows int) (ptm, pts *os.File) {
	t.Helper()
	ptm, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	n, err := unix.IoctlGetInt(int(ptm.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.IoctlSetPointerInt(int(ptm.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	pts, err = os.OpenFile("/dev/pts/"+strconv.Itoa(n), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	unix.IoctlSetWinsize(int(ptm.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Col: uint16(cols), Row: uint16(rows)})
	t.Cleanup(func() { ptm.Close(); pts.Close() })
	return ptm, pts
}

// fake is a Source whose collection comes in two parts, the second when the test says.
type fake struct {
	t        *testing.T
	second   chan struct{}
	parts    int // what has come: the loop's alone
	ran      chan [2]int
	collects int
}

func (f *fake) Header(loading bool) string {
	if loading {
		return "[ X ] loading"
	}
	return "[ X ] ready"
}

func (f *fake) Sections(loading bool) []Section {
	m := sample()
	if loading {
		for i := range m {
			m[i].Steps = nil
		}
	}
	return m
}

func (f *fake) Checking() bool { return f.parts < 2 }

func (f *fake) Collect() <-chan func() {
	f.collects++
	ch := make(chan func())
	go func() {
		defer close(ch)
		ch <- func() { f.parts = 1 }
		<-f.second
		ch <- func() { f.parts = 2 }
	}()
	return ch
}

func (f *fake) Run(section, step int) { f.ran <- [2]int{section, step} }

// The panel on a pseudo-terminal: up at once with the loading text, the sections when
// the first part came, "checking" until the last; a step runs with the screen given
// back and the collection starts again after it; q waits for what is still coming and
// leaves the terminal as it was.
func TestRunOnAPty(t *testing.T) {
	ptm, pts := openPty(t, 100, 30)
	before, _ := unix.IoctlGetTermios(int(pts.Fd()), unix.TCGETS)
	var mu sync.Mutex
	var seen bytes.Buffer
	go func() {
		b := make([]byte, 4096)
		for {
			n, err := ptm.Read(b)
			if err != nil {
				return
			}
			mu.Lock()
			seen.Write(b[:n])
			mu.Unlock()
		}
	}()
	raw := func() string {
		mu.Lock()
		defer mu.Unlock()
		return seen.String()
	}
	shown := func() string { return tui.StripSGR(raw()) }
	waitFor := func(want string) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); !strings.Contains(shown(), want) && !strings.Contains(raw(), want); time.Sleep(10 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("the screen never showed %q:\n%q", want, shown())
			}
		}
	}
	f := &fake{t: t, second: make(chan struct{}), ran: make(chan [2]int, 1)}
	done := make(chan error, 1)
	go func() {
		done <- Run(f, Config{In: pts, Out: pts, Help: "KEYS", Loading: "Looking ...", Again: "Again ..."})
	}()
	waitFor("[ X ] ")
	waitFor("checking ...")
	waitFor("Read it")
	f.second <- struct{}{}
	ptm.Write([]byte("\r\r")) // into First, run "Read it"
	select {
	case got := <-f.ran:
		if got != [2]int{0, 0} {
			t.Fatalf("ran %v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("the step did not run:\n%q", shown())
	}
	waitFor("\033[?1049l") // given back for the step
	f.second <- struct{}{} // the collection after the step
	ptm.Write([]byte("q"))
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("q did not leave:\n%q", shown())
	}
	if f.collects != 2 || f.parts != 2 {
		t.Fatalf("collected %d times, %d parts", f.collects, f.parts)
	}
	after, _ := unix.IoctlGetTermios(int(pts.Fd()), unix.TCGETS)
	if out := raw(); after.Lflag != before.Lflag || strings.Count(out, "\033[?1049l") != 2 || !strings.Contains(out, "\033[?25h") {
		t.Fatalf("the terminal is not as it was: %x/%x", after.Lflag, before.Lflag)
	}
}
