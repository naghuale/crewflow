package run

import (
	"bytes"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestWidth is how a terminal draws a word of a title: a Cyrillic letter is a letter and
// takes one column, an emoji is a picture and takes two, and what is joined to what
// stands beside it takes no room of its own. A list of titles is lined up by this, and
// a table that counts bytes instead lines up nothing (docs/DESIGN.md §7).
func TestWidth(t *testing.T) {
	cases := []struct {
		name    string
		word    string
		columns int
	}{
		{name: "latin letters", word: "crewflow", columns: 8},
		{name: "a Cyrillic title", word: "открыл изменение", columns: 16},
		{name: "a title with a figure", word: "1ч 05м", columns: 6},
		{name: "an emoji", word: "идёт сейчас 🚀", columns: 14},
		{name: "a person and a shade", word: "👍🏽", columns: 2},
		{name: "a family of three", word: "👨‍👩‍👧", columns: 2},
		{name: "a keycap", word: "1️⃣", columns: 2},
		{name: "a wide letter of CJK", word: "日本語", columns: 6},
		{name: "nothing at all", word: "", columns: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := width(tc.word); got != tc.columns {
				t.Errorf("width(%q) = %d, want %d", tc.word, got, tc.columns)
			}
		})
	}
}

// TestSince is how a person reads the length of a run: as whole seconds, whole minutes,
// and hours with the minutes behind them.
func TestSince(t *testing.T) {
	cases := []struct {
		length time.Duration
		want   string
	}{
		{length: 0, want: "0s"},
		{length: 35 * time.Second, want: "35s"},
		{length: 59 * time.Second, want: "59s"},
		{length: time.Minute, want: "1m"},
		{length: 42 * time.Minute, want: "42m"},
		{length: 65 * time.Second, want: "1m"},
		{length: time.Hour, want: "1h 00m"},
		{length: 65 * time.Minute, want: "1h 05m"},
		{length: 25 * time.Hour, want: "1d 01h"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			if got := since(tc.length); got != tc.want {
				t.Errorf("since(%s) = %q, want %q", tc.length, got, tc.want)
			}
		})
	}
}

// TestWriteLaysTheColumnsOut: the columns of a list are as wide as the widest word in
// them, and a title with a newline in it does not break the lines under it.
func TestWriteLaysTheColumnsOut(t *testing.T) {
	ended := monday.Add(42 * time.Minute)
	runs := Runs{Repo: "naghuale/crewflow", Total: 2, Entries: []Entry{
		{Task: 9, Title: "a short one", Attempt: 1, Outcome: Blocked, Executor: "opencode",
			Identity:  Identity{Mode: "owner"},
			StartedAt: monday, EndedAt: &ended, Duration: 42 * time.Minute},
		{Task: 43, Title: "a much longer title\nwith a line in it", Attempt: 12, Outcome: ChangeRequestOpened,
			Executor: "codex", Identity: Identity{Mode: "bot"},
			StartedAt: monday, EndedAt: &ended, Duration: 42 * time.Minute,
			Change: &Change{Number: 6, URL: "https://example.com/pull/6"}},
	}}

	var out bytes.Buffer
	if err := runs.Write(&out); err != nil {
		t.Fatalf("Write returned an error: %v", err)
	}

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("the list is\n%s\nwant a header, a line of names and two lines of runs", out.String())
	}
	if want := "naghuale/crewflow · 2 tasks"; lines[0] != want {
		t.Errorf("the first line of the list is %q, want %q", lines[0], want)
	}
	if !strings.Contains(lines[3], "a much longer title with a line in it") {
		t.Errorf("the line of the task 43 is %q, want the whole title in it", lines[3])
	}
	// The change request is a number a person looks for, not a link that is too wide
	// to read a table by.
	if want := "#6"; !strings.HasSuffix(lines[3], want) {
		t.Errorf("the line of the task 43 ends in %q, want the change request in it", want)
	}
	// Every column of a line starts where it starts in the other lines, whatever the
	// width of the titles before it: that is the whole of a table.
	for _, column := range [][]string{
		{"OUTCOME", "blocked", "pr-opened"},
		{"WHEN", "2026-09-28", "2026-09-28"},
		{"EXECUTOR", "opencode", "codex"},
		{"RUN", "9-1", "43-12"},
	} {
		starts := make([]int, 0, len(lines)-1)
		for i, line := range lines[1:] {
			found := strings.Index(line, column[i])
			if found < 0 {
				t.Fatalf("the line %q has no %q in it, want every line of the table to have one", line, column[i])
			}
			starts = append(starts, found)
		}
		if !slices.Equal(starts, []int{starts[0], starts[0], starts[0]}) {
			t.Errorf("the columns of %q start at %v, want them all in one place", column[0], starts)
		}
	}
}

// TestWriteSaysOneTaskAndNoTasks: a list says how many tasks the project has, and
// "1 tasks" is a list nobody believes.
func TestWriteSaysOneTaskAndNoTasks(t *testing.T) {
	ended := monday.Add(time.Minute)
	cases := []struct {
		total int
		want  string
	}{
		{total: 1, want: "naghuale/crewflow · 1 task"},
		{total: 0, want: "naghuale/crewflow · 0 tasks"},
		{total: 5, want: "naghuale/crewflow · 5 tasks"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			runs := Runs{Repo: "naghuale/crewflow", Total: tc.total, Entries: []Entry{
				{Task: 9, Title: "a task", Attempt: 1, Outcome: Blocked, StartedAt: monday,
					EndedAt: &ended, Duration: time.Minute},
			}}
			var out bytes.Buffer
			if err := runs.Write(&out); err != nil {
				t.Fatalf("Write returned an error: %v", err)
			}
			if got := strings.SplitN(out.String(), "\n", 2)[0]; got != tc.want {
				t.Errorf("the header of the list is %q, want %q", got, tc.want)
			}
		})
	}
}

// TestWriteHasAColumnOfTheProjectForTheWholeMachine: a list of one project says which
// project it is over the table, and a list of every project on the machine has it in a
// column of its own, because there is no one project over the table of it.
func TestWriteHasAColumnOfTheProjectForTheWholeMachine(t *testing.T) {
	runs := Runs{Entries: []Entry{
		{Repo: "naghuale/crewflow", Task: 43, Title: "here", Attempt: 1, Outcome: Blocked, StartedAt: monday},
		{Repo: "naghuale/tele", Task: 7, Title: "there", Attempt: 1, Outcome: Blocked, StartedAt: monday},
	}}

	var out bytes.Buffer
	if err := runs.Write(&out); err != nil {
		t.Fatalf("Write returned an error: %v", err)
	}

	header := strings.SplitN(out.String(), "\n", 2)[0]
	if !strings.HasPrefix(header, "REPO ") {
		t.Errorf("the header of a list of the whole machine is %q, want the project first in it", header)
	}
	if strings.Contains(out.String(), "also on this machine") {
		t.Errorf("a list of the whole machine wrote %q, want nothing about what is beside it", out.String())
	}
}

// TestCut: a title of a task is a line of text off the host and can be as long as
// anybody likes, and a table that grows with it stops being a table. What does not fit
// in the column of it is cut off, and an ellipsis says that it was.
func TestCut(t *testing.T) {
	cases := []struct {
		name    string
		text    string
		columns int
		want    string
	}{
		{name: "a title that fits is left alone", text: "the run of a task", columns: 20, want: "the run of a task"},
		{name: "a title that fits exactly is left alone", text: "0123456789", columns: 10, want: "0123456789"},
		{name: "a title of one column too many is cut", text: "0123456789", columns: 9, want: "01234567…"},
		{name: "a long title is cut at the column", text: "feat(identity): the executor as a bot", columns: 10, want: "feat(iden…"},
		{name: "a title in letters of two columns is cut by the column", text: "日本語のタイトル", columns: 5, want: "日本…"},
		{name: "a column of no room cannot hold a letter", text: "the run of a task", columns: 1, want: "…"},
		{name: "an emoji is a picture of two columns and is not cut in two", text: "идёт 🚀 сейчас", columns: 8, want: "идёт 🚀…"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cut(tc.text, tc.columns); got != tc.want {
				t.Errorf("cut(%q, %d) = %q, want %q", tc.text, tc.columns, got, tc.want)
			}
		})
	}
}

// TestLineTakesTheCommandsOfATerminalOutOfATitle: a title of a task is a line of text
// off the host, and a title that clears the screen a person is reading, moves the
// cursor of it or draws a box over it is not a title anybody can read.
func TestLineTakesTheCommandsOfATerminalOutOfATitle(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{name: "a title is a title", text: "feat(task): the list of the runs", want: "feat(task): the list of the runs"},
		{name: "a newline is a space", text: "one\ntwo", want: "one two"},
		{name: "a carriage return is a space", text: "one\rtwo", want: "one two"},
		{name: "a tab is a space", text: "one\ttwo", want: "one two"},
		{name: "a sequence that clears the screen is gone", text: "before\x1b[2Jafter", want: "beforeafter"},
		{name: "a colour is gone", text: "\x1b[31mred\x1b[0m", want: "red"},
		{name: "a bell is a space", text: "one\atwo", want: "one two"},
		{name: "a delete is a space", text: "one\x7ftwo", want: "one two"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := line(tc.text); got != tc.want {
				t.Errorf("line(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}
