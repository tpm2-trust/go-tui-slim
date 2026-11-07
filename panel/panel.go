// Package panel is a control panel on a full screen: sections on the left, the selected
// section's steps and the selected step's detail on the right - what it does, how it
// stands, the command it equals, what it destroys - and the keys at the bottom. A step
// runs in the terminal with the screen given back, and the screen comes back after it.
// What the panel shows comes in the background: the frame is up at once.
//
// It knows nothing of what it controls: its user gives the sections and steps (Source),
// their texts, the help, and runs the steps.
package panel

import (
	"fmt"
	"strings"

	"github.com/tpm2-trust/go-tui-slim/tui"
)

// Danger is how much a step can break.
type Danger int

const (
	Changes  Danger = iota // it changes what it controls
	Reads                  // it reads, nothing else: no mark of its own
	Destroys               // it destroys what is there: marked, and its Destroys said
)

// Step is one line of a section.
type Step struct {
	Title    string
	Done     string // how it stands when in place ("" when not)
	Blocked  string // why it cannot run here ("" when it can)
	Dirty    string // what needs attention ("" when nothing does)
	Explain  string // what it does
	Command  string // the command it equals, if any
	Destroys string // what a step that Destroys removes
	Danger   Danger
	Check    bool   // a check: when Done, Enter says so instead of running it
	Jump     string // a section's Key: Enter opens that section instead of running
	Runnable bool   // Enter runs it (Source.Run)
}

// Section is one part of the panel. Intro is shown when the section, not a step, is
// selected; its user puts there what the section has to say (facts included).
type Section struct {
	Key, Title, Intro string
	Steps             []Step
}

// Focus is where the keys move.
type Focus int

const (
	Sections Focus = iota
	Steps
)

// State is what the screen shows apart from the sections: pure, so that keys and
// frames are tested without a terminal.
type State struct {
	Section, Step int // the selected section and its selected step
	Focus         Focus
	Help          bool   // the keys' help in place of the detail
	Message       string // the status row's last word

	scroll    int // the detail pane's first row
	searching bool
	query     string
	hit       int // the selected search result
}

// Cmd is what a key asks of the loop.
type Cmd int

const (
	None Cmd = iota
	RunStep
	Quit
	Refresh
	Redraw
)

// View is what a frame is drawn with besides the sections.
type View struct {
	Header   string
	Loading  string // non-empty while nothing has come yet: shown in place of the steps
	Checking bool   // more is still coming: the status row says so
	Help     string // the keys' help, on '?'
}

func dim(s string) string     { return "\033[2m" + s + "\033[0m" }
func bold(s string) string    { return "\033[1m" + s + "\033[0m" }
func reverse(s string) string { return "\033[7m" + s + "\033[0m" }
func good(s string) string    { return "\033[0;32m" + s + "\033[0m" }
func bad(s string) string     { return "\033[0;31m" + s + "\033[0m" }
func amber(s string) string   { return "\033[0;33m" + s + "\033[0m" }

type hit struct{ section, step int }

// search is every step whose title holds q first, then those whose state, command or
// explanation does.
func search(m []Section, q string) []hit {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return nil
	}
	var titles, rest []hit
	for i, sec := range m {
		for j, s := range sec.Steps {
			text := strings.ToLower(tui.StripSGR(strings.Join([]string{s.Done, s.Blocked, s.Dirty, s.Command, s.Explain}, " ")))
			switch {
			case strings.Contains(strings.ToLower(tui.StripSGR(s.Title)), q):
				titles = append(titles, hit{i, j})
			case strings.Contains(text, q):
				rest = append(rest, hit{i, j})
			}
		}
	}
	return append(titles, rest...)
}

// Selected is the step under the cursor, or nil.
func (st *State) Selected(m []Section) *Step {
	if st.Section < 0 || st.Section >= len(m) || st.Step < 0 || st.Step >= len(m[st.Section].Steps) {
		return nil
	}
	return &m[st.Section].Steps[st.Step]
}

// Clamp keeps the selection inside m, which may change after every refresh.
func (st *State) Clamp(m []Section) {
	if len(m) == 0 {
		st.Section, st.Step, st.Focus = 0, 0, Sections
		return
	}
	st.Section = min(max(st.Section, 0), len(m)-1)
	n := len(m[st.Section].Steps)
	if n == 0 {
		st.Step = 0
		if st.Focus == Steps {
			st.Focus = Sections
		}
		return
	}
	st.Step = min(max(st.Step, 0), n-1)
}

func (st *State) steps(m []Section) int {
	if st.Section < len(m) {
		return len(m[st.Section].Steps)
	}
	return 0
}

// Key takes one key; page is how far PgUp and PgDn scroll. What it asks of the loop
// comes back.
func (st *State) Key(k tui.Key, m []Section, page int) Cmd {
	if st.searching {
		return st.searchKey(k, m)
	}
	st.Message = ""
	switch k.Name {
	case tui.KeyCtrlC, tui.KeyCtrlD:
		return Quit
	case tui.KeyCtrlL:
		return Redraw
	case tui.KeyUp:
		st.move(-1, m)
	case tui.KeyDown:
		st.move(1, m)
	case tui.KeyPgUp:
		st.scroll = max(st.scroll-page, 0)
	case tui.KeyPgDn:
		st.scroll += page
	case tui.KeyHome:
		st.move(-1000, m)
	case tui.KeyEnd:
		st.move(1000, m)
	case tui.KeyRight, tui.KeyTab:
		if st.Focus == Sections && st.steps(m) > 0 {
			st.Focus, st.scroll = Steps, 0
		}
	case tui.KeyLeft, tui.KeyBackTab:
		st.Focus, st.scroll = Sections, 0
	case tui.KeyEsc:
		switch {
		case st.Help:
			st.Help = false
		case st.Focus == Steps:
			st.Focus, st.scroll = Sections, 0
		default:
			return Quit
		}
	case tui.KeyEnter:
		return st.enter(m)
	case tui.KeyRune:
		switch k.Rune {
		case 'q':
			return Quit
		case 'k':
			st.move(-1, m)
		case 'j':
			st.move(1, m)
		case 'h':
			st.Focus, st.scroll = Sections, 0
		case 'l':
			if st.steps(m) > 0 {
				st.Focus, st.scroll = Steps, 0
			}
		case 'r':
			return Refresh
		case '?':
			st.Help = !st.Help
		case '/':
			st.searching, st.query, st.hit = true, "", 0
		}
	}
	return None
}

func (st *State) move(by int, m []Section) {
	st.scroll = 0
	if st.Focus == Sections {
		st.Section = min(max(st.Section+by, 0), max(len(m)-1, 0))
		st.Step = 0
		return
	}
	if n := st.steps(m); n > 0 {
		st.Step = min(max(st.Step+by, 0), n-1)
	}
}

// enter acts on the selection: a section opens, a step that jumps opens its section,
// one that needs attention runs, a check in place and a blocked step say so, any other
// runnable one runs.
func (st *State) enter(m []Section) Cmd {
	if st.Focus == Sections {
		if st.steps(m) > 0 {
			st.Focus, st.Step, st.scroll = Steps, 0, 0
		}
		return None
	}
	s := st.Selected(m)
	if s == nil {
		return None
	}
	switch {
	case s.Jump != "":
		for i, sec := range m {
			if sec.Key == s.Jump {
				st.Section, st.Step, st.scroll = i, 0, 0
			}
		}
		return None
	case s.Dirty != "":
		return RunStep
	case s.Done != "" && s.Check:
		st.Message = "In place: " + s.Done
		return None
	case s.Done == "" && s.Blocked != "":
		st.Message = "Not possible here: " + s.Blocked
		return None
	case !s.Runnable:
		return None
	}
	return RunStep
}

func (st *State) searchKey(k tui.Key, m []Section) Cmd {
	hits := search(m, st.query)
	switch k.Name {
	case tui.KeyEsc, tui.KeyCtrlC:
		st.searching = false
	case tui.KeyEnter:
		st.searching = false
		if st.hit < len(hits) {
			h := hits[st.hit]
			st.Section, st.Step, st.Focus, st.scroll = h.section, h.step, Steps, 0
		}
	case tui.KeyBackspace:
		if r := []rune(st.query); len(r) > 0 {
			st.query, st.hit = string(r[:len(r)-1]), 0
		}
	case tui.KeyUp:
		st.hit = max(st.hit-1, 0)
	case tui.KeyDown:
		st.hit = min(st.hit+1, max(len(hits)-1, 0))
	case tui.KeyRune:
		st.query, st.hit = st.query+string(k.Rune), 0
	}
	return None
}

// Frame draws the screen: cols by rows, every row as wide as cols at most, with the
// glyphs g. At 72 columns or more the sections and the steps are side by side, below
// that one at a time.
func (st *State) Frame(m []Section, v View, cols, rows int, g tui.Glyphs) []string {
	out := make([]string, 0, rows)
	out = append(out, tui.Fit(v.Header, cols, g.Ellipsis), dim(strings.Repeat(g.Horiz, cols)))
	body := max(rows-4, 0)
	var main []string
	if cols >= 72 {
		side := 22
		width := cols - side - 3
		left := st.sidebar(m, g, side)
		right := st.mainPane(m, v, width, body, g)
		main = tui.Columns(" "+dim(g.Vert)+" ", []int{side, width}, pad(left, body), pad(right, body))
	} else if st.Focus == Sections && !st.searching {
		main = pad(st.sidebar(m, g, cols), body)
	} else {
		main = pad(st.mainPane(m, v, cols, body, g), body)
	}
	out = append(out, main[:body]...)
	out = append(out, dim(strings.Repeat(g.Horiz, cols)), st.statusRow(v))
	out = out[:min(len(out), rows)]
	for i, r := range out {
		out[i] = tui.Fit(r, cols, g.Ellipsis)
	}
	return out
}

func pad(rows []string, n int) []string {
	for len(rows) < n {
		rows = append(rows, "")
	}
	return rows[:n]
}

// sidebar is the sections, each marked when a step of it needs attention.
func (st *State) sidebar(m []Section, g tui.Glyphs, width int) []string {
	var out []string
	for i, sec := range m {
		mark := " "
		for _, s := range sec.Steps {
			if s.Dirty != "" {
				mark = bad(g.Bad)
				break
			}
		}
		label := mark + " " + sec.Title
		if i == st.Section {
			if st.Focus == Sections {
				label = reverse(tui.Fit(label, width, g.Ellipsis))
			} else {
				label = bold(label)
			}
		}
		out = append(out, label)
	}
	return out
}

// Row is a step's row in a list: its mark, its title, how it stands.
func Row(s Step, g tui.Glyphs) string {
	var mark, state string
	switch {
	case s.Dirty != "":
		mark, state = bad(g.Bad), bad(s.Dirty)
	case s.Done != "":
		mark, state = good(g.Good), s.Done
	case s.Blocked != "":
		mark, state = dim(g.Blocked), dim(s.Blocked)
	case s.Danger == Reads:
		mark = " "
	default:
		mark = amber(g.Open)
	}
	title := s.Title
	if s.Blocked != "" && s.Done == "" && s.Dirty == "" {
		title = dim(title)
	}
	if s.Danger == Destroys {
		title += " " + bad(g.Warn)
	}
	if s.Jump != "" {
		title += " " + g.More
	}
	if state != "" {
		return mark + " " + title + "  " + dim("-") + " " + state
	}
	return mark + " " + title
}

// mainPane is the section's title and steps, and below them the selection's detail.
func (st *State) mainPane(m []Section, v View, width, height int, g tui.Glyphs) []string {
	if st.searching {
		return st.searchPane(m, width, height, g)
	}
	if st.Section >= len(m) {
		return nil
	}
	sec := m[st.Section]
	out := []string{bold(sec.Title)}
	if v.Loading != "" {
		return append(out, "", dim(v.Loading))
	}
	steps := sec.Steps
	listRoom := min(len(steps), max(height/2-2, 3))
	first := 0
	if st.Step >= listRoom {
		first = st.Step - listRoom + 1
	}
	for i := first; i < len(steps) && i < first+listRoom; i++ {
		row := Row(steps[i], g)
		if i == st.Step && st.Focus == Steps {
			row = reverse(tui.Fit(tui.StripSGR(row), width, g.Ellipsis))
		}
		out = append(out, row)
	}
	if len(steps) > listRoom {
		out = append(out, dim(fmt.Sprintf("  (%d of %d)", st.Step+1, len(steps))))
	}
	out = append(out, dim(strings.Repeat(g.Horiz, width)))
	detail := st.detail(m, v, width, g)
	if st.scroll > max(len(detail)-1, 0) {
		st.scroll = max(len(detail)-1, 0)
	}
	return append(out, detail[st.scroll:]...)
}

// detail is the selected step's: what it does and how it stands - or, on the sections,
// the section's intro.
func (st *State) detail(m []Section, v View, width int, g tui.Glyphs) []string {
	text := ""
	switch {
	case st.Help:
		text = v.Help
	case st.Focus == Sections:
		text = m[st.Section].Intro
	default:
		s := st.Selected(m)
		if s == nil {
			break
		}
		var b strings.Builder
		b.WriteString(bold(s.Title) + "\n")
		switch {
		case s.Dirty != "":
			b.WriteString(bad("Needs attention: "+s.Dirty) + "\n")
		case s.Done != "":
			b.WriteString(good("Done: ") + s.Done + "\n")
		case s.Blocked != "":
			b.WriteString(dim("Not possible here: "+s.Blocked) + "\n")
		}
		if s.Explain != "" {
			b.WriteString("\n" + s.Explain + "\n")
		}
		if s.Danger == Destroys && s.Destroys != "" {
			b.WriteString("\n" + bad(g.Warn+" Destroys: ") + s.Destroys + "\n")
		}
		if s.Command != "" {
			b.WriteString("\n" + dim("The command:") + " " + s.Command + "\n")
		}
		text = b.String()
	}
	return tui.Layout(strings.TrimRight(text, "\n"), width, 0)
}

func (st *State) searchPane(m []Section, width, height int, g tui.Glyphs) []string {
	out := []string{bold("Search:") + " " + st.query + "_", ""}
	hits := search(m, st.query)
	for i, h := range hits {
		if len(out) >= height {
			break
		}
		row := m[h.section].Title + " " + g.More + " " + tui.StripSGR(m[h.section].Steps[h.step].Title)
		if i == st.hit {
			row = reverse(tui.Fit(row, width, g.Ellipsis))
		}
		out = append(out, row)
	}
	if st.query != "" && len(hits) == 0 {
		out = append(out, dim("Nothing found."))
	}
	return out
}

// statusRow is the keys, or the last message.
func (st *State) statusRow(v View) string {
	switch {
	case st.searching:
		return dim("type to search  ↑↓ pick  Enter go there  Esc back")
	case st.Message != "":
		return amber(st.Message)
	}
	keys := "↑↓ move  ←→ in/out  Enter run  / search  ? keys  r again  q quit"
	if v.Checking {
		keys += "  · checking ..."
	}
	return dim(keys)
}
