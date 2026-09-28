package run

import (
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// when is how a moment of a run is written in a list: the date of the machine of the
// person reading it, because a run of last week and a run of an hour ago are the same
// date far more often than not, and a person reads their own clock.
const when = "2006-01-02 15:04"

// gap is what is between two columns of a list, and two spaces are enough to tell one
// word from the next in any terminal.
const gap = "  "

// titleColumns is how wide the column of the title of a task is at most. A title is a
// line of text off the host and can be as long as anybody likes, and a table that
// grows with every title in it is not a table anybody reads.
const titleColumns = 40

// ellipsis is what stands where the part of a title that did not fit was cut off.
const ellipsis = "…"

// escape is a sequence of characters a terminal reads as a command and not as letters:
// the colours, the moving of the cursor and the wiping of the screen. A title of a
// task is a line of text off the host, and one that carries a command in it is one
// that paints over whatever the person who reads the list is looking at.
var escape = regexp.MustCompile("\x1b(?:[@-Z\\\\-_]|\\[[0-?]*[ -/]*[@-~])")

// Write is the list of the runs of a project for a person: which project it is and how
// many tasks it has, the runs that want somebody today on top, the rest from the last
// to the first, and under the table what the other projects of the machine are doing.
// A list of the whole machine has the project in a column of its own and says nothing
// under the table, because all of it is in it already. A project where nothing was run
// yet gets one line instead of an empty table: a person who asked has to be told that
// there is nothing, not shown nothing.
func (r Runs) Write(w io.Writer) error {
	if r.Repo != "" {
		if _, err := fmt.Fprintf(w, "%s · %s\n", ownerAndRepo(r.Repo), counted(r.Total, "task")); err != nil {
			return fmt.Errorf("write the list: %w", err)
		}
	}
	if len(r.Entries) == 0 {
		if _, err := fmt.Fprintln(w, "no runs yet"); err != nil {
			return fmt.Errorf("write the list: %w", err)
		}
	} else if err := writeRows(w, r.rows()); err != nil {
		return err
	}
	if err := r.Notes(w); err != nil {
		return err
	}
	return r.beside(w)
}

// rows is the table of the list: a line of names and a line of a run, with the project
// in front of them when the list is of every project of the machine and there is no one
// project to name over the whole of it.
func (r Runs) rows() [][]string {
	names := []string{"#", "TASK", "EXECUTOR", "AS", "OUTCOME", "WHEN", "FOR", "RUN", "CHANGE"}
	if r.Repo == "" {
		names = append([]string{"REPO"}, names...)
	}
	rows := [][]string{names}
	for _, entry := range r.Entries {
		row := []string{
			strconv.Itoa(entry.Task),
			cut(line(entry.Title), titleColumns),
			agent(entry),
			as(entry),
			string(entry.Outcome),
			entry.StartedAt.Local().Format(when),
			length(entry),
			entry.Run(),
			change(entry),
		}
		if r.Repo == "" {
			row = append([]string{ownerAndRepo(entry.Repo)}, row...)
		}
		rows = append(rows, row)
	}
	return rows
}

// beside is what a list of one project says about the machine it is on: the other
// projects that have runs of it and what came of those runs, and how to see all of it
// at once. A machine with no other project has nothing to say here, and a line about
// nothing is worse than no line.
func (r Runs) beside(w io.Writer) error {
	if r.Repo == "" || len(r.Elsewhere) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return fmt.Errorf("write the list: %w", err)
	}
	for _, project := range r.Elsewhere {
		_, err := fmt.Fprintf(w, "  also on this machine: %s — %s\n", ownerAndRepo(project.Repo), project.said())
		if err != nil {
			return fmt.Errorf("write the list: %w", err)
		}
	}
	if _, err := fmt.Fprintln(w, "  show everything: crewflow task list -all"); err != nil {
		return fmt.Errorf("write the list: %w", err)
	}
	return nil
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

// agent is the column of the agent that ran the last try of a task, and a dash where
// the state of the task names none: a state of before crewflow kept the profile of a
// run says nothing about who ran it, and a list that invented a name would be lying.
func agent(entry Entry) string {
	if entry.Executor == "" {
		return "-"
	}
	return entry.Executor
}

// as is the column of whose name the executor of the last try worked under: "owner" or
// "bot", the two modes a run goes under (docs/DESIGN.md §7i). A state of before
// crewflow kept the mode says nothing, and a run of before was the owner's — there was
// no other mode to work in then.
func as(entry Entry) string {
	if entry.Identity.Mode == "" {
		return "owner"
	}
	return entry.Identity.Mode
}

// change is the change request of the run as a person says it — the number of it, the
// way the host and every review of it call it — and an em dash where there is none: a
// link is too wide to read a table by, and the number is what a person goes and looks
// for.
func change(entry Entry) string {
	if entry.Change == nil {
		return "—"
	}
	return "#" + strconv.Itoa(entry.Change.Number)
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

// writeRows writes a table with its columns lined up, the way a terminal draws words:
// by how many columns they take, not by how many bytes or letters they are made of.
func writeRows(w io.Writer, rows [][]string) error {
	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for column, cell := range row {
			widths[column] = max(widths[column], width(cell))
		}
	}
	for _, row := range rows {
		var out strings.Builder
		for column, cell := range row {
			if column > 0 {
				out.WriteString(gap)
			}
			// The last column of a line is not padded: a terminal shows the space at
			// the end of a line as nothing, and a file of it is harder to read.
			if column < len(row)-1 {
				out.WriteString(cell + strings.Repeat(" ", widths[column]-width(cell)))
				continue
			}
			out.WriteString(cell)
		}
		if _, err := fmt.Fprintln(w, strings.TrimRight(out.String(), " ")); err != nil {
			return fmt.Errorf("write the list: %w", err)
		}
	}
	return nil
}

// width is how many columns of a terminal a word takes. A Cyrillic letter is a letter
// and takes one, an emoji is a picture and takes two, and what is joined to what stands
// beside it takes no room of its own: a table of titles in any language is a table of
// words, and the columns of it have to line up.
func width(word string) int {
	var m measure
	for _, letter := range word {
		m.of(letter)
	}
	return m.columns
}

// measure is the width of a word in the columns of a terminal, taken letter by letter,
// and the state of it that a letter is read in: what is joined to what stands beside it
// is one picture and not two, a tone is a shade of the letter before it and not a
// letter of its own, and a keycap is a sign in a box as wide as an emoji is.
type measure struct {
	columns int
	joined  bool
}

// of is one letter more of the word and the room it takes of it.
func (m *measure) of(letter rune) int {
	switch {
	case letter == joiner:
		m.joined = true
		return 0
	case m.joined:
		m.joined = false
		return 0
	case isTone(letter):
		return 0
	case letter == keycap && m.columns > 0:
		m.columns++
		return 1
	}
	room := letterWidth(letter)
	m.columns += room
	return room
}

// joiner is what glues the emoji of one picture together, a tone is what shades the
// emoji it follows, and a keycap is the box around a sign.
const (
	joiner   = '\u200d'
	toneFrom = '\U0001f3fb'
	toneTo   = '\U0001f3ff'
	keycap   = '\u20e3'
)

// isTone is whether the rune is one of the shades a person may have, which stand on the
// emoji before them and are not a picture of their own.
func isTone(letter rune) bool {
	return letter >= toneFrom && letter <= toneTo
}

// letterWidth is how many columns one rune takes in a terminal.
func letterWidth(letter rune) int {
	switch {
	case letter < 0x20, letter == 0x7f:
		// A letter that is a command to the terminal is no letter at all.
		return 0
	case unicode.In(letter, unicode.Mn, unicode.Me, unicode.Cf):
		// A mark on a letter, a selector of how a letter looks and an instruction
		// between letters are all drawn on top of the letters around them.
		return 0
	case isWide(letter):
		return 2
	default:
		return 1
	}
}

// wide is the letters a terminal draws in two columns: the scripts of the wide and full
// width classes of Unicode and the emoji. It is not every letter of the wide classes —
// a rare one is drawn in one and only mislines a table that has it in a title.
var wide = []rune{
	0x1100, 0x115f, // Hangul Jamo
	0x2e80, 0x303e, // CJK radicals, Kangxi, CJK symbols and punctuation
	0x3041, 0x33ff, // Kana, Bopomofo, Hangul compatibility, CJK compatibility
	0x3400, 0x4dbf, // CJK unified ideographs, extension A
	0x4e00, 0x9fff, // CJK unified ideographs
	0xa000, 0xa4cf, // Yi
	0xa960, 0xa97f, // Hangul Jamo, extended A
	0xac00, 0xd7a3, // Hangul syllables
	0xf900, 0xfaff, // CJK compatibility ideographs
	0xfe10, 0xfe19, // vertical forms
	0xfe30, 0xfe6f, // CJK compatibility forms, small form variants
	0xff00, 0xff60, // fullwidth forms
	0xffe0, 0xffe6, // fullwidth signs
	0x1f000, 0x1faff, // the emoji, and the symbols that are drawn like them
	0x20000, 0x3fffd, // CJK unified ideographs, extensions B to F
}

// isWide is whether a terminal draws the letter in two columns.
func isWide(letter rune) bool {
	if letter > utf8.MaxRune {
		return false
	}
	for pair := 0; pair < len(wide); pair += 2 {
		if letter > wide[pair+1] {
			continue
		}
		return letter >= wide[pair]
	}
	return false
}

// cut is a cell that is not wider than the column it stands in: what does not fit is
// cut off and an ellipsis stands where it was, because a title that stops in the
// middle of a word is a title nobody can read, and one that stops without saying so is
// a title a bug has shortened.
func cut(text string, columns int) string {
	if columns <= 0 || width(text) <= columns {
		return text
	}
	room := width(ellipsis)
	var out strings.Builder
	soFar := measure{}
	for _, letter := range text {
		next := soFar
		next.of(letter)
		if next.columns+room > columns {
			break
		}
		soFar = next
		out.WriteRune(letter)
	}
	return strings.TrimRight(out.String(), " ") + ellipsis
}

// length is how long a run took, in the words a person reads a time in, and a dash
// where nothing kept it: a run whose process is gone was not seen to stop, and a time
// of zero would be a run that took no time at all.
func length(entry Entry) string {
	took, known := entry.Length()
	if !known {
		return "-"
	}
	return since(took)
}

// since is how long a run took, in the words a person reads a time in: "35s", "42m",
// "1h 05m". A run of less than a second, or of less than nothing — a machine whose clock
// is behind the one the state was written on — is a run that has just been started.
func since(took time.Duration) string {
	if took < 0 {
		took = 0
	}
	switch {
	case took < time.Minute:
		return strconv.Itoa(int(took.Seconds())) + "s"
	case took < time.Hour:
		return strconv.Itoa(int(took.Minutes())) + "m"
	case took < 24*time.Hour:
		return fmt.Sprintf("%dh %02dm", int(took.Hours()), int(took.Minutes())%60)
	default:
		return fmt.Sprintf("%dd %02dh", int(took.Hours())/24, int(took.Hours())%24)
	}
}

// line is a cell of a table that holds a title: a word of it is a word, and whatever
// else a title holds — a newline, a tab, a command to the terminal — is put in its
// place or taken out of it, because a title that breaks the line breaks every column
// under it, and a title that paints over them is a title a person never reads.
func line(text string) string {
	return strings.Map(func(letter rune) rune {
		if unicode.IsControl(letter) {
			return ' '
		}
		return letter
	}, escape.ReplaceAllString(text, ""))
}
