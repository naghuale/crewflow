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
	runs := Runs{Entries: []Entry{
		{Task: 9, Title: "a short one", Attempts: 1, Outcome: Blocked,
			StartedAt: monday, EndedAt: &ended, Duration: 42 * time.Minute},
		{Task: 43, Title: "a much longer title\nwith a line in it", Attempts: 12, Outcome: ChangeRequestOpened,
			StartedAt: monday, EndedAt: &ended, Duration: 42 * time.Minute, ChangeURL: "https://example.com/1"},
	}}

	var out bytes.Buffer
	if err := runs.Write(&out); err != nil {
		t.Fatalf("Write returned an error: %v", err)
	}

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("the list is\n%s\nwant a line of names and two lines of runs", out.String())
	}
	if !strings.Contains(lines[2], "a much longer title with a line in it") {
		t.Errorf("the line of the task 43 is %q, want the whole title in it", lines[2])
	}
	if want := "https://example.com/1"; !strings.HasSuffix(lines[2], want) {
		t.Errorf("the line of the task 43 ends in %q, want the change request in it", lines[2])
	}
	// Every column of a line starts where it starts in the other lines, whatever the
	// width of the titles before it: that is the whole of a table.
	for _, column := range [][]string{
		{"OUTCOME", "blocked", "pr-opened"},
		{"WHEN", "2026-09-28", "2026-09-28"},
	} {
		starts := make([]int, 0, len(lines))
		for i, line := range lines {
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
