package run

import (
	"bytes"
	"regexp"
	"slices"
	"strconv"
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

// TestAge is how a person reads the age of a run: as whole seconds, whole minutes and
// whole hours while a run is young, as "yesterday" while it is a day old, and as a date
// of the calendar after that. It is counted from the start of the last try of the task
// and not from its end, because a run of yesterday is a run of yesterday however long
// it went on, and the length of it is a number for a program and not for an eye.
func TestAge(t *testing.T) {
	// A fixed zone and a fixed hour, for a list that says "yesterday" and a date is
	// read in the calendar of the person reading it, and a test may not depend on
	// where the machine it runs on is.
	zone := time.FixedZone("MSK", 3*60*60)
	now := time.Date(2026, time.September, 29, 14, 30, 0, 0, zone)
	cases := []struct {
		name string
		ago  time.Duration
		// then is the moment the run began, and it is named where a number of hours
		// back from now would not land on the same day of the calendar.
		then time.Time
		want string
	}{
		{name: "a run of this moment", ago: 0, want: "0s"},
		{name: "a run of a moment ago", ago: 35 * time.Second, want: "35s"},
		{name: "the last second of a minute", ago: 59 * time.Second, want: "59s"},
		{name: "the first minute of a run", ago: time.Minute, want: "1m"},
		{name: "a run of some minutes", ago: 42 * time.Minute, want: "42m"},
		{name: "the last minute of an hour", ago: 59 * time.Minute, want: "59m"},
		{name: "the first hour of a run", ago: time.Hour, want: "1h"},
		{name: "a run of some hours", ago: 9 * time.Hour, want: "9h"},
		{name: "the last hour of a day", ago: 23 * time.Hour, want: "23h"},
		{
			// A run of last night is a run of some hours, whatever day of the calendar
			// it began on: a person counts the hours of a run they are waiting for, and
			// a run of five hours is not a run of yesterday however late it began.
			name: "a run of last night", ago: 20 * time.Hour, want: "20h",
		},
		{
			name: "a run of the evening of yesterday",
			ago:  26 * time.Hour, then: time.Date(2026, time.September, 28, 12, 30, 0, 0, zone),
			want: "yesterday",
		},
		{
			name: "a run of the morning of the day before yesterday",
			ago:  36 * time.Hour, then: time.Date(2026, time.September, 27, 2, 30, 0, 0, zone),
			want: "Sep 27",
		},
		{
			name: "a run of another year",
			ago:  365 * 24 * time.Hour, then: time.Date(2025, time.October, 1, 10, 0, 0, 0, zone),
			want: "Oct 1, 2025",
		},
		{
			// A machine whose clock is behind the one the state was written on knows
			// of a run that begins in the future, and a run of a moment ago is the
			// only thing it can say about it.
			name: "a run of a moment that has not come yet",
			ago:  -time.Hour, want: "0s",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			then := now.Add(-tc.ago)
			if !tc.then.IsZero() {
				then = tc.then
			}
			if got := age(now, then); got != tc.want {
				t.Errorf("age(%s, %s) = %q, want %q", now.Format(time.RFC3339), then.Format(time.RFC3339), got, tc.want)
			}
		})
	}
}

// TestAgeAcrossTheMidnightOfTheMachine: a run is as old as it has been going while it
// is young, and only past a whole day is it a day of the calendar — a run of the
// evening of yesterday read in the morning is a run of hours, and read at noon of the
// next day it is a run of yesterday. Both are read in the zone of the machine the list
// is written on, and not in the zone the state was written in.
func TestAgeAcrossTheMidnightOfTheMachine(t *testing.T) {
	zone := time.FixedZone("MSK", 3*60*60)
	evening := time.Date(2026, time.September, 28, 19, 0, 0, 0, zone)
	cases := []struct {
		name string
		at   time.Time
		want string
	}{
		{name: "in the morning after it", at: time.Date(2026, time.September, 29, 6, 0, 0, 0, zone), want: "11h"},
		{name: "in the evening of the next day", at: time.Date(2026, time.September, 30, 20, 0, 0, 0, zone), want: "Sep 28"},
		{name: "on the same evening", at: time.Date(2026, time.September, 28, 22, 0, 0, 0, zone), want: "3h"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := age(tc.at, evening); got != tc.want {
				t.Errorf("the age of a run of %s at %s = %q, want %q",
					evening.Format(time.RFC3339), tc.at.Format(time.RFC3339), got, tc.want)
			}
		})
	}
}

// TestWriteLaysTheColumnsOut: the columns of a list are as wide as the widest word in
// them, a title with a newline in it does not break the lines under it, and a colour
// takes no room of a terminal.
func TestWriteLaysTheColumnsOut(t *testing.T) {
	ended := monday.Add(42 * time.Minute)
	runs := Runs{Repo: "naghuale-crewflow", Branch: "main", Total: 2, Entries: []Entry{
		{Task: 9, Title: "a short one", Attempt: 1, Attempts: 1, Outcome: Blocked, Executor: "opencode",
			Identity:  Identity{Mode: "owner"},
			StartedAt: monday, EndedAt: &ended, Duration: 42 * time.Minute},
		{Task: 43, Title: "a much longer title\nwith a line in it", Attempt: 12, Attempts: 12,
			Outcome:  ChangeRequestOpened,
			Executor: "codex", Identity: Identity{Mode: "bot"},
			StartedAt: monday, EndedAt: &ended, Duration: 42 * time.Minute,
			Change: &Change{Number: 6, URL: "https://example.com/pull/6"}},
	}}

	var out bytes.Buffer
	if err := runs.Write(&out, Screen{At: monday.Add(time.Hour)}); err != nil {
		t.Fatalf("Write returned an error: %v", err)
	}

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("the list is\n%s\nwant a header, a line of names and two lines of runs", out.String())
	}
	if want := "naghuale/crewflow · main · 2 tasks"; lines[0] != want {
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
	// Every column of a line begins where it begins in the other lines, whatever the
	// width of the titles before it: that is the whole of a table. A column of words
	// is read from the left and a column of numbers from the right, so the words of a
	// column begin in one place and the last letter of a number ends in one place.
	for _, column := range [][]string{
		{"TASK", "a short one", "a much longer title with a line in it"},
		{"EXECUTOR", "opencode", "codex"},
		{"OUTCOME", "blocked", "pr-opened"},
		{"RUN", none, "43-12"},
	} {
		if at := columnsOf(lines[1:], column); !slices.Equal(at, repeat(at[0], len(at))) {
			t.Errorf("the words of the column of %q begin in the columns %v, want them all in %d", column[0], at, at[0])
		}
	}
	for _, column := range [][]string{
		{"#", "9", "43"},
		{"AGE", "1h", "1h"},
	} {
		if at := endsOf(lines[1:], column); !slices.Equal(at, repeat(at[0], len(at))) {
			t.Errorf("the numbers of the column of %q end in the columns %v, want them all in %d", column[0], at, at[0])
		}
	}
}

// columnsOf is the column every one of the lines begins the word of that column in.
func columnsOf(lines, column []string) []int {
	where := make([]int, 0, len(lines))
	for i, line := range lines {
		at := columnOf(line, column[i])
		if at < 0 {
			return append(where, -1)
		}
		where = append(where, at)
	}
	return where
}

// endsOf is the column the last letter of the number of every one of the lines is in:
// a column of numbers is read to the right, so its numbers all end in one place.
func endsOf(lines, column []string) []int {
	where := make([]int, 0, len(lines))
	for i, line := range lines {
		at := strings.Index(line, column[i])
		if at < 0 {
			return append(where, -1)
		}
		where = append(where, width(line[:at+len(column[i])]))
	}
	return where
}

// TestWriteSaysOneTaskAndNoTasks: a list says which branch its tasks are counted from
// and how many the project has, and "1 tasks" is a list nobody believes.
func TestWriteSaysOneTaskAndNoTasks(t *testing.T) {
	ended := monday.Add(time.Minute)
	cases := []struct {
		total int
		want  string
	}{
		{total: 1, want: "naghuale/crewflow · main · 1 task"},
		{total: 0, want: "naghuale/crewflow · main · 0 tasks"},
		{total: 5, want: "naghuale/crewflow · main · 5 tasks"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			runs := Runs{Repo: "naghuale-crewflow", Branch: "main", Total: tc.total, Entries: []Entry{
				{Task: 9, Title: "a task", Attempt: 1, Attempts: 1, Outcome: Blocked, StartedAt: monday,
					EndedAt: &ended, Duration: time.Minute},
			}}
			var out bytes.Buffer
			if err := runs.Write(&out, Screen{At: monday}); err != nil {
				t.Fatalf("Write returned an error: %v", err)
			}
			if got := strings.SplitN(out.String(), "\n", 2)[0]; got != tc.want {
				t.Errorf("the header of the list is %q, want %q", got, tc.want)
			}
		})
	}
}

// TestWriteSaysTheBranchOnlyWhereTheProjectNamesOne: the branch of a project comes of
// the file of the project, and a list that names no project names no branch. A list of
// no project at all is a list of the whole machine, and it has no heading to put a
// branch in.
func TestWriteSaysTheBranchOnlyWhereTheProjectNamesOne(t *testing.T) {
	runs := Runs{Repo: "naghuale-crewflow", Total: 1}
	var out bytes.Buffer
	if err := runs.Write(&out, Screen{At: monday}); err != nil {
		t.Fatalf("Write returned an error: %v", err)
	}
	if want := "naghuale/crewflow · 1 task\nno runs yet\n"; out.String() != want {
		t.Errorf("the list of a project with no branch of its own is %q, want %q", out.String(), want)
	}
}

// TestWriteHasAColumnOfTheProjectForTheWholeMachine: a list of one project says which
// project it is over the table, and a list of every project on the machine has it in a
// column of its own, because there is no one project over the table of it.
func TestWriteHasAColumnOfTheProjectForTheWholeMachine(t *testing.T) {
	runs := Runs{Entries: []Entry{
		{Repo: "naghuale-crewflow", Task: 43, Title: "here", Attempt: 1, Attempts: 1, Outcome: Blocked, StartedAt: monday},
		{Repo: "naghuale-tele", Task: 7, Title: "there", Attempt: 1, Attempts: 1, Outcome: Blocked, StartedAt: monday},
	}}

	var out bytes.Buffer
	if err := runs.Write(&out, Screen{At: monday}); err != nil {
		t.Fatalf("Write returned an error: %v", err)
	}

	header := strings.SplitN(out.String(), "\n", 2)[0]
	if !strings.HasPrefix(header, "REPO ") {
		t.Errorf("the header of a list of the whole machine is %q, want the project first in it", header)
	}
	if strings.Contains(out.String(), "also:") {
		t.Errorf("a list of the whole machine wrote %q, want nothing about what is beside it", out.String())
	}
}

// TestTheColumnOfTheProjectSaysWhoseItIs: a list of the whole machine is read by a
// person who works on more than one project, and the owner of a project is said in it
// where the owners of them differ, and nowhere else. A machine of one person is a list
// of names, and a machine of several is a list of "owner/name" — for the name of a
// project says nothing of whose work it is.
func TestTheColumnOfTheProjectSaysWhoseItIs(t *testing.T) {
	entry := Entry{Repo: "naghuale-crewflow", Task: 43, Title: "a task", Attempt: 1, Attempts: 1,
		Outcome: Blocked, StartedAt: monday}
	elsewhere := []Project{
		{Repo: "naghuale-crewflow", Outcomes: map[Kind]int{Blocked: 1}},
		{Repo: "naghuale-tele", Outcomes: map[Kind]int{Running: 1}},
		{Repo: "other-thing", Outcomes: map[Kind]int{Blocked: 1}},
	}
	cases := []struct {
		name      string
		entries   []Entry
		elsewhere []Project
		// want is the name of the project of every task of the list, by its number.
		want map[int]string
	}{
		{
			name: "every project of one owner", entries: []Entry{entry},
			elsewhere: elsewhere[:1],
			want:      map[int]string{43: "crewflow"},
		},
		{
			name: "projects of two owners",
			entries: []Entry{entry, {Repo: "other-thing", Task: 7, Title: "a task of another project",
				Attempt: 1, Attempts: 1, Outcome: Blocked, StartedAt: monday}},
			elsewhere: elsewhere,
			want:      map[int]string{43: "naghuale/crewflow", 7: "other/thing"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runs := Runs{Entries: tc.entries, Elsewhere: tc.elsewhere}
			var out bytes.Buffer
			if err := runs.Write(&out, Screen{At: monday}); err != nil {
				t.Fatalf("Write returned an error: %v", err)
			}
			for _, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n")[1:] {
				// A table lines its columns up with spaces, and a cell is a run of
				// them: whatever is between two runs is one cell of the line.
				cells := cellsOfAList.Split(strings.TrimSpace(line), -1)
				number, err := strconv.Atoi(cells[1])
				if err != nil {
					t.Fatalf("the line %q has no number of a task in it", line)
				}
				want, known := tc.want[number]
				if !known {
					t.Fatalf("the line %q has a task of no case of the test in it", line)
				}
				if repo := cells[0]; repo != want {
					t.Errorf("the column of the project says %q on the line of the task %d, want %q", repo, number, want)
				}
			}
		})
	}
}

// TestTheRunAndTheExecutorAreSaidInOneColumnEach: a run of the whole machine says which
// project it is of and who wrote it — the agent and the name it went under in one
// column, for they are one question about a run — and a task that was tried once says a
// dash for the run, for the first try is the number of the task itself and a list that
// repeats it in every line has a column that says nothing.
func TestTheRunAndTheExecutorAreSaidInOneColumnEach(t *testing.T) {
	cases := []struct {
		name     string
		entry    Entry
		executor string
		run      string
	}{
		{
			name:     "a run of the owner",
			entry:    Entry{Task: 43, Title: "a task", Attempt: 1, Attempts: 1, Executor: "opencode", Identity: Identity{Mode: "owner"}, Outcome: Blocked},
			executor: "opencode · owner", run: none,
		},
		{
			name:     "a run of the bot, on its second try",
			entry:    Entry{Task: 43, Title: "a task", Attempt: 3, Attempts: 3, Executor: "codex", Identity: Identity{Mode: "bot"}, Outcome: Blocked},
			executor: "codex · bot", run: "43-3",
		},
		{
			// A state of before crewflow kept the agent names none, and a run of
			// before went under the owner's name: there was no other (docs/DESIGN.md §7i).
			name:     "a run of before the agent and the mode were kept",
			entry:    Entry{Task: 43, Title: "a task", Attempt: 1, Attempts: 1, Outcome: Blocked},
			executor: none + " · owner", run: none,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := tc.entry
			entry.StartedAt, entry.Repo = monday, "naghuale-crewflow"
			runs := Runs{Repo: "naghuale-crewflow", Branch: "main", Total: 1, Entries: []Entry{entry}}
			var out bytes.Buffer
			if err := runs.Write(&out, Screen{At: monday}); err != nil {
				t.Fatalf("Write returned an error: %v", err)
			}
			for _, want := range []string{tc.executor, tc.run} {
				if !strings.Contains(out.String(), want) {
					t.Errorf("the list wrote\n%s\nwant %q in it", out.String(), want)
				}
			}
		})
	}
}

// TestTheColumnsOfTheListAreReadWhereTheEyeLooksForThem: the number of a task and the
// age of a run are read to the right, whatever the width of the numbers in them, and
// the words are read from the left. A column of numbers read at its left is a column a
// person counts the digits of.
func TestTheColumnsOfTheListAreReadWhereTheEyeLooksForThem(t *testing.T) {
	ended := monday.Add(time.Minute)
	at := time.Date(2026, time.September, 29, 14, 0, 0, 0, time.UTC)
	entry := func(number int, ago time.Duration) Entry {
		return Entry{Task: number, Title: "a task", Attempt: 1, Attempts: 1, Outcome: Blocked,
			StartedAt: at.Add(-ago), EndedAt: &ended}
	}
	cases := []struct {
		name    string
		entries []Entry
	}{
		{name: "one digit of a task", entries: []Entry{entry(7, time.Minute)}},
		{name: "two digits of a task", entries: []Entry{entry(7, time.Minute), entry(43, 9*time.Hour)}},
		{name: "three digits of a task", entries: []Entry{entry(7, time.Minute), entry(431, 9*time.Hour)}},
		{name: "ages of one letter and of many", entries: []Entry{entry(7, time.Minute), entry(43, 30*time.Hour)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runs := Runs{Repo: "naghuale-crewflow", Branch: "main", Total: len(tc.entries), Entries: tc.entries}
			var out bytes.Buffer
			if err := runs.Write(&out, Screen{At: at}); err != nil {
				t.Fatalf("Write returned an error: %v", err)
			}
			// The last letter of the number of a task and the last letter of the age of
			// its run stand in one place in every line of the table: the eye looks for
			// the end of a number, not for where it began. A number of one digit has a
			// space in front of it and a number of three has none, and both end where
			// the column of them ends.
			lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")[2:]
			said := make([][]string, 0, len(tc.entries))
			for _, entry := range tc.entries {
				said = append(said, []string{strconv.Itoa(entry.Task), age(at, entry.StartedAt)})
			}
			numbers := endsOf(lines, firstOf(said))
			if want := numbers[0]; !slices.Equal(numbers, repeat(want, len(numbers))) {
				t.Errorf("the numbers of the tasks end in the columns %v, want all of them in %d", numbers, want)
			}
			ages := endsOf(lines, secondOf(said))
			if want := ages[0]; !slices.Equal(ages, repeat(want, len(ages))) {
				t.Errorf("the ages of the runs end in the columns %v, want all of them in %d", ages, want)
			}
		})
	}
}

// firstOf and secondOf are the first and the second cell of every one of the lines of
// a table, for a test that looks at two columns of numbers at once.
func firstOf(lines [][]string) []string  { return nthOf(lines, 0) }
func secondOf(lines [][]string) []string { return nthOf(lines, 1) }

func nthOf(lines [][]string, at int) []string {
	cells := make([]string, 0, len(lines))
	for _, line := range lines {
		cells = append(cells, line[at])
	}
	return cells
}

// cellsOfAList are the cells of a line of a list of runs: a table lines its columns up
// with spaces, and a cell is a run of them. It is what a test that is about one column
// of the table and not about the whole of it splits a line with.
var cellsOfAList = regexp.MustCompile(`\s{2,}`)

// columnOf is the column of a terminal a word begins in, which is not the number of
// the bytes before it: a Cyrillic letter is a letter and takes one column, and an
// ellipsis is one letter of three bytes.
func columnOf(line, word string) int {
	at := strings.Index(line, word)
	if at < 0 {
		return -1
	}
	return width(line[:at])
}

// repeat is the same number as many times as a test has cases of it, for a test that
// wants every line of a table to end a column in one place.
func repeat(number, times int) []int {
	numbers := make([]int, times)
	for i := range numbers {
		numbers[i] = number
	}
	return numbers
}

// TestTheTitleOfATaskTakesWhatTheOtherColumnsLeave: the title of a task is a line of
// text off the host and can be as long as anybody likes, and the columns beside it are
// as long as they are. What is left of the width of the screen is the title, and a
// title is never cut to nothing, whatever the width of the screen is.
func TestTheTitleOfATaskTakesWhatTheOtherColumnsLeave(t *testing.T) {
	ended := monday.Add(time.Minute)
	title := "the run of a task, and everything the executor wrote in it, and more besides"
	// Every column but the title is as wide as the widest of its name and the words in
	// it, and the columns are as far apart as the gap between them. What is left of
	// the screen is the title — and never less than leastTitleColumns, for a title cut
	// to nothing is a title nobody can read.
	beside := max(width("#"), width("43")) + max(width("EXECUTOR"), width("opencode · bot")) +
		max(width("OUTCOME"), width("pr-opened")) + max(width("AGE"), width("0s")) +
		max(width("RUN"), width(none)) + max(width("CHANGE"), width("#6")) + 6*len(gap)
	// The title of a task stands after the number of it and the gap, and the executor
	// of a run stands after the title of the task and the gap: a line of the table is
	// as wide as the screen, and every part of it is where it belongs in that width.
	titleAt := max(width("#"), width("43")) + len(gap)
	executorAt := func(title int) int { return titleAt + title + len(gap) }
	cases := []struct {
		name    string
		columns int
		want    int
	}{
		{name: "a screen of the usual width", columns: 120, want: max(120-beside, leastTitleColumns)},
		{name: "a screen much wider than the title needs", columns: beside + width(title) + 20, want: width(title) + 20},
		{name: "a screen of exactly the title and the columns", columns: beside + width(title), want: width(title)},
		{name: "a narrow screen", columns: 90, want: 90 - beside},
		{name: "a screen narrower than the rest of the list", columns: 40, want: leastTitleColumns},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runs := Runs{Repo: "naghuale-crewflow", Branch: "main", Total: 1, Entries: []Entry{
				{Task: 43, Title: title, Attempt: 1, Attempts: 1, Outcome: ChangeRequestOpened,
					Executor: "opencode", Identity: Identity{Mode: "bot"},
					StartedAt: monday, EndedAt: &ended, Duration: time.Minute,
					Change: &Change{Number: 6}},
			}}
			var out bytes.Buffer
			screen := Screen{At: monday, Columns: tc.columns}
			if err := runs.Write(&out, screen); err != nil {
				t.Fatalf("Write returned an error: %v", err)
			}
			// Where the executor of a run stands is where the title of the task ends:
			// the title is the last of the room the other columns leave it. The header
			// and the run under it are laid out to the same width, and the title of a
			// run is cut to the room of the column whatever the words in it are.
			lines := strings.Split(out.String(), "\n")
			if got := columnOf(lines[1], "EXECUTOR"); got != executorAt(tc.want) {
				t.Errorf("the column of the executor starts in the column %d, want %d: the title of a task has %d of the screen",
					got, executorAt(tc.want), tc.want)
			}
			if got := columnOf(lines[2], "opencode · bot"); got != executorAt(tc.want) {
				t.Errorf("the line of the run puts the executor in the column %d, want %d:\n%q",
					got, executorAt(tc.want), lines[2])
			}
		})
	}
}

// TestTheEmptyLineBetweenTheGroupsIsOnlyInATerminal: the runs that want a person are
// told from the rest by an empty line where a person is looking at the list, and not in
// a file, which is read line by line and where an empty line is a line about nothing.
func TestTheEmptyLineBetweenTheGroupsIsOnlyInATerminal(t *testing.T) {
	ended := monday.Add(time.Minute)
	runs := Runs{Repo: "naghuale-crewflow", Branch: "main", Total: 2, Entries: []Entry{
		{Task: 43, Title: "a run that is going", Attempt: 1, Attempts: 1, Outcome: Running,
			StartedAt: monday, Executor: "opencode"},
		{Task: 7, Title: "a run that came out well", Attempt: 1, Attempts: 1, Outcome: ChangeRequestOpened,
			StartedAt: monday, EndedAt: &ended, Executor: "opencode", Change: &Change{Number: 6}},
	}}

	var terminal, file bytes.Buffer
	if err := runs.Write(&terminal, Screen{At: monday, Terminal: true, Columns: 120}); err != nil {
		t.Fatalf("Write returned an error: %v", err)
	}
	if err := runs.Write(&file, Screen{At: monday}); err != nil {
		t.Fatalf("Write returned an error: %v", err)
	}

	if !strings.Contains(terminal.String(), "\n\n") {
		t.Errorf("a list in a terminal is\n%s\nwant an empty line between the runs that want a person and the rest",
			terminal.String())
	}
	if strings.Contains(file.String(), "\n\n") {
		t.Errorf("a list in a file is\n%s\nwant no empty line in it", file.String())
	}
}

// TestAListInWhichEveryRunWantsAPersonIsOneGroup: the runs that want a person are
// separated from the rest by an empty line, and a list in which every run wants a person
// has no rest to separate them from — an empty line under the line of the names would
// be an empty line in the middle of a table for no reason at all.
func TestAListInWhichEveryRunWantsAPersonIsOneGroup(t *testing.T) {
	ended := monday.Add(time.Minute)
	runs := Runs{Repo: "naghuale-crewflow", Branch: "main", Total: 2, Entries: []Entry{
		{Task: 43, Title: "a run that is going", Attempt: 1, Attempts: 1, Outcome: Running,
			StartedAt: monday, Executor: "opencode"},
		{Task: 7, Title: "a run that was refused", Attempt: 1, Attempts: 1, Outcome: Blocked,
			StartedAt: monday, EndedAt: &ended, Executor: "opencode"},
	}}

	var out bytes.Buffer
	if err := runs.Write(&out, Screen{At: monday, Terminal: true, Columns: 120}); err != nil {
		t.Fatalf("Write returned an error: %v", err)
	}
	if strings.Contains(out.String(), "\n\n") {
		t.Errorf("a list in which every run wants a person is\n%s\nwant no empty line in it", out.String())
	}
}

// TestTheColoursOfAListAreTheSixteenOfATerminal: a list is read at a glance, and only
// three things in it are said in colour — the project over the table, the names of the
// columns and the outcome of a run. Everything else is said in the letters of itself,
// and a terminal that reads no colour is given none of them at all.
func TestTheColoursOfAListAreTheSixteenOfATerminal(t *testing.T) {
	ended := monday.Add(4 * time.Hour)
	at := monday.Add(8 * time.Hour)
	runs := Runs{Repo: "naghuale-crewflow", Branch: "main", Total: 2, Entries: []Entry{
		{Task: 43, Title: "a run that is going", Attempt: 2, Attempts: 2, Outcome: Running,
			StartedAt: monday, Executor: "opencode"},
		{Task: 7, Title: "a run that was refused", Attempt: 2, Attempts: 2, Outcome: BlockedPermission,
			StartedAt: monday, EndedAt: &ended, Executor: "opencode", Change: &Change{Number: 6}},
	}}
	painted := Screen{At: at, Terminal: true, Painted: true, Columns: 120}

	var withColour bytes.Buffer
	if err := runs.Write(&withColour, painted); err != nil {
		t.Fatalf("Write returned an error: %v", err)
	}
	var withoutColour bytes.Buffer
	if err := runs.Write(&withoutColour, Screen{At: at, Terminal: true, Columns: 120}); err != nil {
		t.Fatalf("Write returned an error: %v", err)
	}

	for _, want := range []string{
		bold + "naghuale/crewflow · main · 2 tasks" + noColour,
		faint + "EXECUTOR" + noColour,
		cyan + "running" + noColour,
		yellow + "blocked-permission" + noColour,
		faint + "8h" + noColour,
		faint + "43-2" + noColour,
	} {
		if !strings.Contains(withColour.String(), want) {
			t.Errorf("a list in a terminal is\n%q\nwant %q in it", withColour.String(), want)
		}
	}
	if strings.Contains(withoutColour.String(), "\x1b") {
		t.Errorf("a list in a terminal that reads no colour is\n%q\nwant no escape in it at all",
			withoutColour.String())
	}
	// The colour of a list is said around the words of it and takes no room of the
	// screen: the same list without colour is the same list.
	if strip := escape.ReplaceAllString(withColour.String(), ""); strip != withoutColour.String() {
		t.Errorf("a list in colour is\n%q\nwant the same list as\n%q", strip, withoutColour.String())
	}
}

// TestTheOutcomeOfARunIsReadInOneColour: a run that is going is blue, a run that opened
// its change request is green, a run that stopped on a permission is yellow, a run that
// died is red, and an outcome nobody has to do anything about is said in the letters of
// itself — a colour where nothing happened is a colour that cries wolf.
func TestTheOutcomeOfARunIsReadInOneColour(t *testing.T) {
	cases := []struct {
		outcome Kind
		want    string
	}{
		{outcome: Running, want: cyan},
		{outcome: ChangeRequestOpened, want: green},
		{outcome: Blocked, want: yellow},
		{outcome: BlockedPermission, want: yellow},
		{outcome: Interrupted, want: red},
		{outcome: TimedOut, want: red},
		{outcome: ExecutorFailed, want: red},
		{outcome: MaybeRunning, want: ""},
		{outcome: NoChangeRequest, want: ""},
		{outcome: OutOfScope, want: ""},
	}
	for _, tc := range cases {
		t.Run(string(tc.outcome), func(t *testing.T) {
			if got := colourOf(tc.outcome); got != tc.want {
				t.Errorf("the colour of %q is %q, want %q", tc.outcome, got, tc.want)
			}
		})
	}
}

// TestTheListSaysNothingButWordsWhereNothingIsToBeSaid: a list goes into a file and
// through a pipe as often as it goes onto a screen, and a file of a list that draws
// over the terminal of whoever reads it is a file nobody can read (docs/DESIGN.md §6).
func TestTheListSaysNothingButWordsWhereNothingIsToBeSaid(t *testing.T) {
	ended := monday.Add(time.Minute)
	runs := Runs{Repo: "naghuale-crewflow", Branch: "main", Total: 2, Entries: []Entry{
		{Task: 43, Title: "a title that paints\x1b[2Jover the screen\r", Attempt: 1, Attempts: 1,
			Outcome: Running, StartedAt: monday, Executor: "opencode"},
		{Task: 7, Title: "a run that came out well", Attempt: 2, Attempts: 2, Outcome: ChangeRequestOpened,
			StartedAt: monday, EndedAt: &ended, Executor: "opencode", Change: &Change{Number: 6}},
	}}
	// A title is cleaned before it is measured, so that a title off the host cannot
	// change the width of a column or paint over the list around it. A screen is
	// either a terminal of a person or a file, and a list on either of them is a
	// table of letters — a terminal that takes no colour is a list of the same
	// letters.
	for _, screen := range []Screen{
		{At: monday}, {At: monday, Terminal: true},
	} {
		var out bytes.Buffer
		if err := runs.Write(&out, screen); err != nil {
			t.Fatalf("Write returned an error: %v", err)
		}
		for _, letter := range out.String() {
			if letter < ' ' && letter != '\n' {
				t.Errorf("the list on a screen of %+v wrote the control character %q, want a table of letters and nothing else",
					screen, letter)
			}
			if letter == 0x7f {
				t.Errorf("the list on a screen of %+v wrote a delete, want a table of letters and nothing else", screen)
			}
		}
		if !strings.Contains(out.String(), "a title that paints") {
			t.Errorf("the list on a screen of %+v wrote %q, want the title of the task in it", screen, out.String())
		}
	}
}

// TestTheScreenOfAListIsWideEnoughToReadATitle: a list that goes into a file is written
// as wide as a person reads, for a terminal of a person is not there to be asked and a
// file has no width of its own.
func TestTheScreenOfAListIsWideEnoughToReadATitle(t *testing.T) {
	if got := (Screen{}).width(); got != plainColumns {
		t.Errorf("a list with no screen of a person is written %d columns wide, want %d", got, plainColumns)
	}
	if got := (Screen{Columns: 80}).width(); got != 80 {
		t.Errorf("a list on a screen of 80 columns is written %d columns wide, want 80", got)
	}
	if got := (Screen{Columns: -1}).width(); got != plainColumns {
		t.Errorf("a list on a screen that says nothing is written %d columns wide, want %d", got, plainColumns)
	}
}

// TestTheOtherProjectsAreSaidInOneLine: a person in the folder of a project cannot see
// what runs beside it, and the owner of a machine of one person is in the heading of
// every list of it, so the projects beside it are said by their names alone. A machine
// with no other project says nothing about it, for a line about nothing is worse than
// no line.
func TestTheOtherProjectsAreSaidInOneLine(t *testing.T) {
	here := Runs{Repo: "naghuale-crewflow", Branch: "main", Total: 1, Entries: []Entry{
		{Task: 43, Title: "a run of this project", Attempt: 1, Attempts: 1, Outcome: Running, StartedAt: monday},
	}, Elsewhere: []Project{
		{Repo: "naghuale-tele", Outcomes: map[Kind]int{Running: 2, ChangeRequestOpened: 5, OutOfScope: 2}},
		{Repo: "other-thing", Outcomes: map[Kind]int{Blocked: 1}},
	}}

	var out bytes.Buffer
	if err := here.Write(&out, Screen{At: monday, Columns: 120}); err != nil {
		t.Fatalf("Write returned an error: %v", err)
	}
	for _, want := range []string{
		"also: tele (2 running, 5 pr-opened, 2 out-of-scope) · other/thing (1 blocked)",
		"crewflow task list -all for everything",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the list wrote %q, want %q in it", out.String(), want)
		}
	}

	narrow := here
	var wrapped bytes.Buffer
	if err := narrow.Write(&wrapped, Screen{At: monday, Columns: 60}); err != nil {
		t.Fatalf("Write returned an error: %v", err)
	}
	lines := strings.Split(strings.TrimRight(wrapped.String(), "\n"), "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[len(lines)-2], "  other/thing") {
		t.Errorf("the list on a narrow screen is\n%s\nwant the second project on a line of its own", wrapped.String())
	}
}

// TestCut: a title of a task is a line of text off the host and can be as long as
// anybody likes, and a table that grows with it stops being a table. What does not fit
// in the column of it is cut off at the end of a whole word, and an ellipsis says that
// it was.
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
		{
			name: "a long title is cut at the end of a word", text: "feat(identity): the executor as a bot",
			columns: 30, want: "feat(identity): the executor…",
		},
		{
			name: "a title of one word longer than the column is cut in the middle of it",
			text: "feat(identity): the executor as a bot", columns: 10, want: "feat(iden…",
		},
		{
			name: "a title in letters of two columns is cut by the column", text: "日本語のタイトル",
			columns: 5, want: "日本…",
		},
		{name: "a column of no room cannot hold a letter", text: "the run of a task", columns: 1, want: "…"},
		{
			name: "an emoji is a picture of two columns and is not cut in two", text: "идёт 🚀 сейчас",
			columns: 8, want: "идёт 🚀…",
		},
		{
			name: "a title of one script is cut at the end of the last word that fits",
			text: "окно закрыли, и это название", columns: 20, want: "окно закрыли, и это…",
		},
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
