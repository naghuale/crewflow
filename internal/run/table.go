package run

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Screen is the screen a list of runs is written for: the moment it is written for,
// how wide the screen is, and what the screen is — a terminal of a person, which is
// looked at, or a file or a pipe, which is read. A list reads the state crewflow kept
// and asks the machine about a run, and nothing else, so what it needs to know about
// the screen is told to it from outside (docs/DESIGN.md §6, §7).
type Screen struct {
	// At is the moment the list is written for. The age of every run in it is counted
	// from that moment, so a run that is going gets older while the list stands still:
	// only the length of a run stops, and it stops at the moment it ended.
	At time.Time
	// Columns is how wide the screen is, and zero where nothing says: a list that
	// goes into a file or through a pipe is written as wide as a person reads.
	Columns int
	// Terminal says the list goes onto a screen of a person and not into a file. It
	// is what the runs that want a person are told apart from the rest by an empty
	// line on, because a terminal is looked at and a file is read line by line, where
	// an empty line is a line about nothing.
	Terminal bool
	// Painted says the terminal reads the sixteen colours of a terminal. A file, a
	// pipe, a person who set NO_COLOR and a terminal that says it takes no colour at
	// all (TERM=dumb) are all a list of plain letters, and a list of plain letters
	// reads the same in every terminal of the world and in every file. It is said of
	// no screen that is not a terminal of a person.
	Painted bool
}

// width is how many columns the list may take: the width of the screen, and
// plainColumns where nothing says, because a list that goes into a file is read in
// whatever the person who reads it reads in, and 120 is what a terminal is most of the
// time.
func (s Screen) width() int {
	if s.Columns > 0 {
		return s.Columns
	}
	return plainColumns
}

// plainColumns is how wide a list of runs is written where no screen of a person says:
// wide enough for the title of a task and the columns beside it, and no wider.
const plainColumns = 120

// leastTitleColumns is the narrowest the title of a task is ever cut to. A title cut to
// nothing is a title nobody can read, and a list wider than a screen is a list a person
// scrolls along to read.
const leastTitleColumns = 20

// gap is what is between two columns of a list, and two spaces are enough to tell one
// word from the next in any terminal.
const gap = "  "

// none is what stands where a list has nothing to say: no agent, no change request, a
// run that is on its first try and is therefore not a try anybody needs to hear about. It
// is one mark for one thing, so that the eye learns it once.
const none = "—"

// mid is what stands between the pieces of a line: the parts of a header, the projects
// of a machine, the agent of a run and the name it went under. It is a middot and not
// a dot, a comma or a bar, because it takes a column of a letter in any terminal and
// reads as nothing in particular, which is what it is.
const mid = " · "

// The colours a list of runs is read in: the sixteen a terminal has, and nothing else,
// because a colour every terminal has is a colour that reads the same in every theme of
// them. Nothing else is drawn — a border, a line under a name, an icon and a background
// are four ways of saying nothing louder.
const (
	bold     = "\x1b[1m" // the project a list is of, and the number of its tasks
	faint    = "\x1b[2m" // the names of the columns, and the numbers beside a run
	red      = "\x1b[31m"
	green    = "\x1b[32m"
	yellow   = "\x1b[33m"
	cyan     = "\x1b[36m"
	noColour = "\x1b[0m"
)

// Write is the list of the runs of a project for a person: which project it is, the
// branch its tasks are counted from and how many it has, the runs that want somebody
// today on top, the rest from the last to the first, and under the table what the
// other projects of the machine are doing. A list of the whole machine has the project
// in a column of its own and says nothing under the table, because all of it is in it
// already. A project where nothing was run yet gets one line instead of an empty table:
// a person who asked has to be told that there is nothing, not shown nothing.
func (r Runs) Write(w io.Writer, screen Screen) error {
	if r.Repo != "" {
		if _, err := fmt.Fprintln(w, painted(r.heading(), bold, screen)); err != nil {
			return fmt.Errorf("write the list: %w", err)
		}
	}
	if len(r.Entries) == 0 {
		if _, err := fmt.Fprintln(w, "no runs yet"); err != nil {
			return fmt.Errorf("write the list: %w", err)
		}
	} else if err := r.table(screen.At).write(w, screen); err != nil {
		return err
	}
	if err := r.Notes(w); err != nil {
		return err
	}
	return r.beside(w, screen)
}

// heading is the line over the table of a list of one project: which project it is, the
// branch its tasks are counted from and how many of them it has. The branch is said
// because a list of tasks is a list of a project at a moment of it, and a person who
// reads it wants to know which branch the work of it is on its way to.
func (r Runs) heading() string {
	parts := []string{ownerAndRepo(r.Repo), r.Branch, counted(r.Total, "task")}
	said := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			said = append(said, part)
		}
	}
	return strings.Join(said, mid)
}

// column is one column of the table of a list: what it is called over the table, and
// which way the cells of it stand in it.
type column struct {
	// name is what the column is called over the table.
	name string
	// right says the cells of the column are read to the right of it. A number of a
	// task and an age are, because a column of numbers is a column of digits and the
	// eye looks for the last of them; words are read from the left.
	right bool
}

// table is the list of runs as a person reads it: the columns of it, one line of each
// run in them, and where the runs that want a person end and the rest begin.
type table struct {
	columns []column
	// title is the column of the title of a task, the one column that is ever cut, and
	// split is the line where the runs that want a person end and the rest begin. A
	// list in which every run wants a person is one group and has no such line.
	title int
	split int
	rows  []row
}

// row is one line of the table: a cell for every column of it, and the colour every
// one of the cells is read in. A colour is one of the sixteen a terminal has, or the
// empty string where the cell is read as it is written.
type row struct {
	cells   []string
	colours []string
}

// table is the list as a person reads it: the runs of a project with the project over
// the table, and the runs of every project of the machine with the project in a column
// of its own, because there is no one project over the table of it to name. The runs
// are in the order a list of runs is in (byRecency): what wants a person on top, and
// the rest from the last try of a task to the first (docs/DESIGN.md §6, §7).
func (r Runs) table(at time.Time) table {
	built := table{columns: r.columns()}
	built.title = slices.IndexFunc(built.columns, func(c column) bool { return c.name == titleColumn })
	built.rows = append(built.rows, row{
		cells:   names(built.columns),
		colours: faintly(len(built.columns)),
	})
	oneOwner := r.oneOwner()
	wants := true
	for i, entry := range r.Entries {
		built.rows = append(built.rows, r.rowOf(entry, at, oneOwner))
		// The first run that wants nobody is where the two groups of a list meet. It
		// is the line of the run in the table, for the line of the names is the one
		// before the first run, and a run of its own is not a group of runs.
		if i > 0 && wants && !wantsAttention(entry.Outcome) {
			built.split = i + 1
		}
		wants = wantsAttention(entry.Outcome)
	}
	return built
}

// titleColumn is the name of the one cell of a table that is ever cut: a number, an age
// and a name are as long as they are, and the title of a task is a line of text off the
// host that can be as long as anybody likes.
const titleColumn = "TASK"

// columns of the table of a list, in the order a person reads them: which task it is
// and what it is about, who ran it and how the last try of it came out, and then the
// age of the run, the run itself and the change request of it. The project is in front
// of all of it for a list of the whole machine.
func (r Runs) columns() []column {
	list := []column{
		{name: "#", right: true},
		{name: titleColumn},
		{name: "EXECUTOR"},
		{name: "OUTCOME"},
		{name: "AGE", right: true},
		{name: "RUN"},
		{name: "CHANGE"},
	}
	if r.Repo == "" {
		list = append([]column{{name: "REPO"}}, list...)
	}
	return list
}

// rowOf is one run of a list as a line of the table of it. The title of the task is
// left whole here and cut when the table is written, where the width of the screen is
// known, and the age of the run is counted from the moment the list is written for, so
// that a run that is going gets older while the list stands still.
func (r Runs) rowOf(entry Entry, at time.Time, oneOwner bool) row {
	said := row{
		cells: []string{
			strconv.Itoa(entry.Task),
			line(entry.Title),
			executor(entry),
			outcomeCell(entry),
			age(at, entry.StartedAt),
			attempt(entry),
			change(entry),
		},
		// The number of a task and its title are read word by word and are said in
		// the letters of themselves; the outcome is the one thing a list draws
		// attention to; the age, the run and the change request are numbers beside the
		// run, and they are said quietly so that the eye goes past them.
		colours: []string{"", "", "", colourOf(entry.Outcome), faint, faint, faint},
	}
	if r.Repo == "" {
		said.cells = append([]string{project(entry.Repo, oneOwner)}, said.cells...)
		said.colours = append([]string{""}, said.colours...)
	}
	return said
}

// write is the table as a terminal draws it: the columns lined up by the columns of a
// terminal the words of them take, the title of the task cut to the room the other
// columns leave, and an empty line between the runs that want a person and the rest
// where there is a terminal to be looked at.
func (t table) write(w io.Writer, screen Screen) error {
	widths := t.widths(screen)
	for at := range t.rows {
		// The runs that want a person are separated from the rest by an empty line in
		// a terminal and not in a file: a terminal is looked at, and a file is read
		// line by line, where an empty line is a line about nothing. A list in which
		// every run wants a person is one group, and there is nothing to separate.
		if screen.Terminal && t.split > 0 && at == t.split {
			if _, err := fmt.Fprintln(w); err != nil {
				return fmt.Errorf("write the list: %w", err)
			}
		}
		if _, err := fmt.Fprintln(w, t.shown(at, widths, screen)); err != nil {
			return fmt.Errorf("write the list: %w", err)
		}
	}
	return nil
}

// shown is the line at that place in the table as the screen it goes to shows it: every
// cell in the column of it, a column of numbers read to the right and a column of words
// to the left, and the last cell of a line as long as it is — a terminal shows the space
// at the end of a line as nothing, and a file of it is harder to read.
func (t table) shown(at int, widths []int, screen Screen) string {
	row := t.rows[at]
	var out strings.Builder
	for i, cell := range row.cells {
		if i > 0 {
			out.WriteString(gap)
		}
		if i == t.title {
			cell = cut(cell, widths[i])
		}
		out.WriteString(t.shaped(row, i, cell, widths, screen))
	}
	return strings.TrimRight(out.String(), " ")
}

// shaped is one cell of a line in the column it stands in, in the colour it is read
// in. The spaces around it are put on outside the colour, for the room a cell takes in
// a column is the room of its letters and not the room of the colour around them: a
// colour takes no room of a terminal, or a list in colour would be a different table
// from the same list in letters.
func (t table) shaped(row row, at int, cell string, widths []int, screen Screen) string {
	before, after := 0, 0
	if at < len(row.cells)-1 {
		room := widths[at] - width(cell)
		if t.columns[at].right {
			before = room
		} else {
			after = room
		}
	}
	return strings.Repeat(" ", before) + painted(cell, row.colours[at], screen) +
		strings.Repeat(" ", after)
}

// widths is how wide every column of the table is: as wide as the widest word in it,
// and the title of a task as wide as what the other columns leave of the screen, and
// never narrower than leastTitleColumns — a title cut to nothing is a title nobody can
// read, and a list wider than a screen is a list a person scrolls along.
func (t table) widths(screen Screen) []int {
	widths := make([]int, len(t.columns))
	for i, c := range t.columns {
		widths[i] = width(c.name)
	}
	for _, said := range t.rows {
		for i, cell := range said.cells {
			widths[i] = max(widths[i], width(cell))
		}
	}
	rest := screen.width() - (len(widths)-1)*len(gap)
	for i, w := range widths {
		if i != t.title {
			rest -= w
		}
	}
	widths[t.title] = max(rest, leastTitleColumns)
	return widths
}

// painted is a cell of a table as the screen it goes to shows it: in colour where a
// terminal of a person reads colour, and in the letters of it everywhere else, so that
// a list which goes into a file is a file of words and nothing else. A screen that is
// not a terminal of a person is never painted, whatever it says of colour: a file is
// read by nobody's eye.
func painted(text, colour string, screen Screen) string {
	if colour == "" || !screen.Terminal || !screen.Painted {
		return text
	}
	return colour + text + noColour
}

// colourOf is the colour the outcome of a run is read in, and it is one colour for as
// long as the outcome asks a person for the same thing: a run that is going is blue, a
// run that opened its change request is green, a run that stopped on a permission or on
// its own says so in yellow, and a run that died — cut short, out of time, with its
// executor gone, or after reaching for a key — is red. A run that stands is yellow: it
// is not over and nothing is broken, and it is the one mark of a list that asks for
// somebody to look at it today. Every other outcome is said in the letters of it:
// nothing of a run that is out of scope or that changed nothing asks for anybody today,
// and a colour where nothing happened is a colour that cries wolf.
func colourOf(outcome Kind) string {
	switch outcome {
	case Running:
		return cyan
	case Stalled, Blocked, BlockedPermission:
		return yellow
	case ChangeRequestOpened:
		return green
	case BlockedSecret, Interrupted, TimedOut, ExecutorFailed:
		return red
	default:
		return ""
	}
}

// beside is what a list of one project says about the machine it is on: the other
// projects that have runs of it and what came of those runs, in one line, and how to
// see all of it at once. A machine with no other project has nothing to say here, and a
// line about nothing is worse than no line. The line of the other projects goes over
// the width of the screen where it does not fit, a project at a time, for a project
// broken in the middle of its outcomes is a project nobody can read.
func (r Runs) beside(w io.Writer, screen Screen) error {
	if r.Repo == "" || len(r.Elsewhere) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return fmt.Errorf("write the list: %w", err)
	}
	for i, line := range r.linesOfOthers(screen.width()) {
		if i == 0 {
			line = "  also: " + line
		} else {
			line = "  " + line
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return fmt.Errorf("write the list: %w", err)
		}
	}
	if _, err := fmt.Fprintln(w, "  crewflow task list -all for everything"); err != nil {
		return fmt.Errorf("write the list: %w", err)
	}
	return nil
}

// linesOfOthers is what the other projects of the machine are doing, one string per
// line of the screen: the name of a project and the outcomes of its runs, in one line
// while it fits, and a project to a line of its own where it does not.
func (r Runs) linesOfOthers(columns int) []string {
	said := make([]string, 0, len(r.Elsewhere))
	for _, other := range r.Elsewhere {
		said = append(said, fmt.Sprintf("%s (%s)", r.besideOf(other.Repo), other.said()))
	}
	var lines []string
	line := ""
	for _, one := range said {
		switch {
		case line == "":
			line = one
		case width(line)+len(mid)+width(one) <= columns:
			line += mid + one
		default:
			lines = append(lines, line)
			line = one
		}
	}
	return append(lines, line)
}

// said is what a project of the machine is doing, in the words of a person: "1 running,
// 2 pr-opened", in the order the outcomes are worked out in and only the ones there
// are. A project nothing was run in is not on the machine as far as a list of runs is
// concerned, and a line of zeros is not what a person reads.
func (p Project) said() string {
	var said []string
	for _, outcome := range kinds {
		if n := p.Outcomes[outcome]; n > 0 {
			said = append(said, fmt.Sprintf("%d %s", n, outcome))
		}
	}
	return strings.Join(said, ", ")
}

// project is the name of a project as a person reads it: the name of it alone where the
// owner of it is known already — it is over the table of a list of one project, and it
// is the owner of every other project of a machine of one person — and "owner/name"
// where it is not, for the name of a project says nothing of whose work it is and a
// person who cannot tell two projects apart cannot read a list of both.
func project(repo string, oneOwner bool) string {
	if !oneOwner {
		return ownerAndRepo(repo)
	}
	_, name, _ := strings.Cut(repo, "-")
	return name
}

// oneOwner is whether every project of a list is of the same owner. It is what a
// machine of one person looks like, and then the owner of them all is said once.
func (r Runs) oneOwner() bool {
	if len(r.Entries) == 0 {
		return false
	}
	owner, _, named := strings.Cut(r.Entries[0].Repo, "-")
	if !named {
		return false
	}
	for _, entry := range r.Entries[1:] {
		if other, _, _ := strings.Cut(entry.Repo, "-"); other != owner {
			return false
		}
	}
	return true
}

// besideOf is the name of a project of the machine as the line about the projects beside
// the project of the list says it: the name alone where the owner of it is the owner of
// this one — the owner of a machine of one person is in the heading of every list of it,
// and saying it again in every line under the table is noise — and "owner/name" where
// the owner of it is somebody else's.
func (r Runs) besideOf(other string) string {
	owner, _, named := strings.Cut(r.Repo, "-")
	theirOwner, _, theirNamed := strings.Cut(other, "-")
	return project(other, named && theirNamed && owner == theirOwner)
}

// executor is the column of who wrote the run: the agent that ran the last try of the
// task, and the name that try went under, in one column of it, because they are one
// question about a run and two columns are two things to read (docs/DESIGN.md §7b,
// §7i). A state of before crewflow kept the agent in names none, and the mark of
// nothing stands where it is; the name of a run is the owner's where the state does not
// say, for a run of before was the owner's.
func executor(entry Entry) string {
	agent := entry.Executor
	if agent == "" {
		agent = none
	}
	return agent + mid + identity(entry)
}

// identity is the column of whose name the executor of the last try worked under:
// "owner" or "bot", the two modes a run goes under (docs/DESIGN.md §7i).
func identity(entry Entry) string {
	if entry.Identity.Mode == "" {
		return "owner"
	}
	return entry.Identity.Mode
}

// outcomeCell is the column of how the last try of a task came out. A run that is standing
// is the one kind that carries a number with it — how long it has shown nothing — because
// that number is what a person decides about, and the step it was doing is in the answer
// of `-json` and in the record under the task rather than in a column of its own: a table
// of runs a person reads down the middle of (docs/DESIGN.md §6).
func outcomeCell(entry Entry) string {
	if entry.Stalled == nil {
		return string(entry.Outcome)
	}
	return string(Stalled) + " " + Idle(entry.Stalled.For)
}

// attempt is the column of the run: "57-2" where the task was tried more than once,
// and the mark of nothing where it was tried once, for the first try is the number of
// the task itself and a list that repeats it in every line is a list with a column that
// says nothing.
func attempt(entry Entry) string {
	if entry.Attempts < 2 {
		return none
	}
	return entry.Run()
}

// change is the change request of the run as a person says it — the number of it, the
// way the host and every review of it call it — and the mark of nothing where there is
// none: a link is too wide to read a table by, and the number is what a person goes and
// looks for.
func change(entry Entry) string {
	if entry.Change == nil {
		return none
	}
	return "#" + strconv.Itoa(entry.Change.Number)
}

// age is how long ago the last try of a task was started, in the words a person reads a
// time in: "35s", "42m", "9h", "yesterday", "Sep 27". A run that is going is as old as
// it has been going, and a run that ended is as old as the moment it began: what is read
// here is the age of a run and not its length, so that a list of a morning says how
// fresh a run of yesterday is even where nothing of it has changed since. A moment of
// the future — a machine whose clock is behind the one the state was written on — is a
// run that has just been started.
func age(at, since time.Time) string {
	gone := at.Sub(since)
	switch {
	case gone < time.Minute:
		return strconv.Itoa(int(max(gone, 0).Seconds())) + "s"
	case gone < time.Hour:
		return strconv.Itoa(int(gone.Minutes())) + "m"
	case gone < 24*time.Hour:
		return strconv.Itoa(int(gone.Hours())) + "h"
	}
	// A day is a day of a calendar and not a number of hours: "yesterday" is what a
	// person calls the day before, and past that a date says which day it was, with
	// the year in it whenever it is not this one. Both moments are read in the zone of
	// the machine the list is written on — the zone `at` is in, which is the zone of
	// the clock of the person reading it — and not in the zone the state was written
	// in, or a run of the evening would be dated to the morning of the next day.
	then := since.In(at.Location())
	switch {
	case sameDay(then, at.AddDate(0, 0, -1)):
		return "yesterday"
	case then.Year() != at.Year():
		return then.Format("Jan 2, 2006")
	default:
		return then.Format("Jan 2")
	}
}

// sameDay is whether two moments are on one day of the calendar of the person reading
// them, in the zone of the machine they are on: a run of the night before is a run of
// yesterday, whatever zone either end of it counts the hours in.
func sameDay(one, other time.Time) bool {
	year, month, day := one.Date()
	otherYear, otherMonth, otherDay := other.Date()
	return year == otherYear && month == otherMonth && day == otherDay
}

// counted is how many of something there are, in the words of a person: "1 task" and
// "3 tasks", because a list that says "1 tasks" is a list nobody believes.
func counted(number int, noun string) string {
	if number == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(number) + " " + noun + "s"
}

// Notes is what a list could not show in the table: the runs that were left out of a
// short one, and the state files that say nothing about a run. It is written to the
// standard error of a command that answers in JSON, because the answer of a command is
// the thing an orchestrator reads, and a broken file is not an answer.
func (r Runs) Notes(w io.Writer) error {
	if r.Left > 0 {
		if _, err := fmt.Fprintf(w, "and %d more, show them all with -all\n", r.Left); err != nil {
			return fmt.Errorf("write the list: %w", err)
		}
	}
	for _, path := range r.Unreadable {
		if _, err := fmt.Fprintf(w, "not read: %s\n", path); err != nil {
			return fmt.Errorf("write the list: %w", err)
		}
	}
	return nil
}

// names are the names of the columns of a table, as many of them as it has columns.
func names(columns []column) []string {
	said := make([]string, 0, len(columns))
	for _, c := range columns {
		said = append(said, c.name)
	}
	return said
}

// faintly is the colour of every name of a table, as many times as the table has
// columns: every name of a table is a name, and a list of names is not a list of
// headlines.
func faintly(colours int) []string {
	faded := make([]string, colours)
	for i := range faded {
		faded[i] = faint
	}
	return faded
}
