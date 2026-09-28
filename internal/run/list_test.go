package run

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/proc"
)

// monday is the day every time in these tests is on: a list of runs says when a run
// was, and a test says it with a day of its own rather than with the day of the
// machine it runs on.
var monday = time.Date(2026, time.September, 28, 10, 0, 0, 0, time.UTC)

// TestListOrder is what a person asks a list of runs for: what is going on right
// now, and what went on before that, the last try of a task above the one before it
// (docs/DESIGN.md §7).
func TestListOrder(t *testing.T) {
	home := t.TempDir()
	repo := "naghuale-crewflow"
	writeState(t, home, repo, 43, "the run of a task", nil, try{endedAt: monday.Add(42 * time.Minute), outcome: ChangeRequestOpened})
	writeState(t, home, repo, 44, "the journal of a run", nil,
		try{endedAt: monday.Add(30 * time.Minute), outcome: TimedOut},
		try{startedAt: monday.Add(time.Hour), endedAt: monday.Add(90 * time.Minute), outcome: BlockedPermission})
	writeState(t, home, repo, 49, "a run that is going", nil, try{startedAt: monday.Add(2 * time.Hour), outcome: Running, pid: 100})
	writeState(t, home, repo, 50, "a run whose window was closed", nil, try{startedAt: monday.Add(3 * time.Hour), outcome: Running, pid: 200})

	runs, err := List(home, repo, true, listOf(monday.Add(4*time.Hour), 100))
	if err != nil {
		t.Fatalf("List returned an error: %v", err)
	}

	want := []int{49, 50, 44, 43}
	got := make([]int, 0, len(runs.Entries))
	for _, entry := range runs.Entries {
		got = append(got, entry.Task)
	}
	if !slices.Equal(got, want) {
		t.Errorf("the list is of the tasks %v, want %v", got, want)
	}
	if runs.Left != 0 {
		t.Errorf("a list of everything leaves %d entries out, want none", runs.Left)
	}
}

// TestListSaysHowEachRunCameOut: the outcome of a run is what a person reads first,
// and an attempt that says "running" is only running while the process of the run is
// still there — a window that was closed and a machine that was rebooted are both a
// run that is over (docs/DESIGN.md §7).
func TestListSaysHowEachRunCameOut(t *testing.T) {
	home := t.TempDir()
	repo := "naghuale-crewflow"
	writeState(t, home, repo, 43, "opened a change request", &Change{Number: 44, URL: "https://github.com/naghuale/crewflow/pull/44"},
		try{startedAt: monday, endedAt: monday.Add(42 * time.Minute), outcome: ChangeRequestOpened})
	writeState(t, home, repo, 44, "going right now", nil,
		try{startedAt: monday, endedAt: monday.Add(42 * time.Minute), outcome: Blocked},
		try{startedAt: monday.Add(time.Hour), outcome: Running, pid: 100})
	writeState(t, home, repo, 49, "the window was closed", nil, try{startedAt: monday.Add(time.Hour), outcome: Running, pid: 200})
	writeState(t, home, repo, 50, "two tries", nil,
		try{startedAt: monday, endedAt: monday.Add(42 * time.Minute), outcome: NoChangeRequest},
		try{startedAt: monday.Add(3 * time.Hour), endedAt: monday.Add(3*time.Hour + 35*time.Second), outcome: OutOfScope, pid: 300})

	cases := []struct {
		name     string
		task     int
		outcome  Kind
		attempts int
		length   time.Duration
		// known says whether the length of the run is known at all: a run whose
		// process is gone was not seen to stop, and how long it went on is in
		// nothing crewflow kept.
		known   bool
		change  string
		endedAt time.Time
	}{
		{
			name: "the run opened the change request of the branch", task: 43,
			outcome: ChangeRequestOpened, attempts: 1, known: true, length: 42 * time.Minute,
			change: "https://github.com/naghuale/crewflow/pull/44", endedAt: monday.Add(42 * time.Minute),
		},
		{
			name: "the last try of the task is going", task: 44,
			outcome: Running, attempts: 2, known: true, length: 3 * time.Hour,
		},
		{
			name: "the process of the run is gone", task: 49,
			outcome: Interrupted, attempts: 1, known: false,
		},
		{
			name: "what the last try cost", task: 50,
			outcome: OutOfScope, attempts: 2, known: true, length: 35 * time.Second,
			endedAt: monday.Add(3*time.Hour + 35*time.Second),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runs, err := List(home, repo, true, listOf(monday.Add(4*time.Hour), 100))
			if err != nil {
				t.Fatalf("List returned an error: %v", err)
			}
			entry, ok := runs.entry(tc.task)
			if !ok {
				t.Fatalf("the list holds %+v, want the task %d in it", runs.Entries, tc.task)
			}
			if entry.Outcome != tc.outcome {
				t.Errorf("the outcome = %q, want %q", entry.Outcome, tc.outcome)
			}
			if entry.Attempts != tc.attempts {
				t.Errorf("the number of attempts = %d, want %d", entry.Attempts, tc.attempts)
			}
			length, known := entry.Length()
			if known != tc.known {
				t.Errorf("the length of the run is %s and known %t, want it known %t", length, known, tc.known)
			}
			if known && length != tc.length {
				t.Errorf("the run took %s, want %s", length, tc.length)
			}
			if entry.ChangeURL != tc.change {
				t.Errorf("the change request = %q, want %q", entry.ChangeURL, tc.change)
			}
			if entry.Title == "" {
				t.Error("the entry has no title, want the one the task was run with")
			}
			switch {
			case tc.endedAt.IsZero() && entry.EndedAt != nil:
				t.Errorf("a run with no end of its own ended at %s, want it to have none", entry.EndedAt)
			case !tc.endedAt.IsZero() && (entry.EndedAt == nil || !entry.EndedAt.Equal(tc.endedAt)):
				t.Errorf("the run ended at %v, want %s", entry.EndedAt, tc.endedAt)
			}
		})
	}
}

// TestListOfStatesWrittenBeforeTheProcessWasKept reads a state of before crewflow kept
// the number of the process of a run the only way it can be read: the machine cannot
// be asked about a number it does not have, and a run that says "running" and has been
// going for longer than any run of the project may go on is over.
func TestListOfStatesWrittenBeforeTheProcessWasKept(t *testing.T) {
	home := t.TempDir()
	repo := "naghuale-crewflow"
	writeState(t, home, repo, 43, "just started, nobody knows", nil, try{startedAt: monday, outcome: Running})
	writeState(t, home, repo, 44, "started a long time ago", nil, try{startedAt: monday, outcome: Running})

	cases := []struct {
		name string
		now  time.Time
		task int
		want Kind
	}{
		{
			name: "a run of an hour ago may still be going",
			now:  monday.Add(59 * time.Minute),
			task: 43,
			want: MaybeRunning,
		},
		{
			name: "a run of the length of the whole timeout may be going",
			now:  monday.Add(time.Hour),
			task: 44,
			want: MaybeRunning,
		},
		{
			name: "a run of yesterday is not going",
			now:  monday.Add(25 * time.Hour),
			task: 43,
			want: Interrupted,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runs, err := List(home, repo, true, listOf(tc.now))
			if err != nil {
				t.Fatalf("List returned an error: %v", err)
			}
			entry, ok := runs.entry(tc.task)
			if !ok {
				t.Fatalf("the list holds %+v, want the task %d in it", runs.Entries, tc.task)
			}
			if entry.Outcome != tc.want {
				t.Errorf("the outcome = %q, want %q", entry.Outcome, tc.want)
			}
		})
	}
}

// TestListAsksTheMachineOnlyAboutRunsThatAreGoing: the machine is asked about the
// process of an attempt that says it is running, and about nothing else — a run that
// said how it ended needs no question to be read.
func TestListAsksTheMachineOnlyAboutRunsThatAreGoing(t *testing.T) {
	home := t.TempDir()
	repo := "naghuale-crewflow"
	writeState(t, home, repo, 43, "over", nil, try{endedAt: monday, outcome: TimedOut, pid: 100})
	writeState(t, home, repo, 44, "going", nil, try{startedAt: monday, outcome: Running, pid: 200})

	asked := []int{}
	env := listOf(monday.Add(time.Minute))
	env.Running = func(process proc.Process) bool {
		asked = append(asked, process.Pid)
		return process.Pid == 200
	}

	if _, err := List(home, repo, true, env); err != nil {
		t.Fatalf("List returned an error: %v", err)
	}

	if !slices.Equal(asked, []int{200}) {
		t.Errorf("the machine was asked about the processes %v, want only the one of the run that is going", asked)
	}
}

// TestListIsShortUnlessAllIsAskedFor: a person asks what is going on and what was
// going on, and twenty tasks is more of that than anybody reads; an orchestrator
// asks for all of them, and is told how many there are besides.
func TestListIsShortUnlessAllIsAskedFor(t *testing.T) {
	home := t.TempDir()
	repo := "naghuale-crewflow"
	for number := 1; number <= ListLimit+3; number++ {
		writeState(t, home, repo, number, "a task", nil,
			try{startedAt: monday.Add(time.Duration(number) * time.Minute), outcome: TimedOut})
	}

	runs, err := List(home, repo, false, listOf(monday.Add(2*time.Hour)))
	if err != nil {
		t.Fatalf("List returned an error: %v", err)
	}
	if len(runs.Entries) != ListLimit {
		t.Errorf("the list holds %d runs, want the last %d", len(runs.Entries), ListLimit)
	}
	if runs.Left != 3 {
		t.Errorf("the list left %d runs out, want 3", runs.Left)
	}
	if last, first := runs.Entries[len(runs.Entries)-1], runs.Entries[0]; last.Task != 4 || first.Task != ListLimit+3 {
		t.Errorf("the list goes from the task %d to the task %d, want the newest %d and then down to 4",
			first.Task, last.Task, ListLimit+3)
	}

	all, err := List(home, repo, true, listOf(monday.Add(2*time.Hour)))
	if err != nil {
		t.Fatalf("List of everything returned an error: %v", err)
	}
	if len(all.Entries) != ListLimit+3 || all.Left != 0 {
		t.Errorf("a list of everything holds %d runs and leaves %d out, want %d and none",
			len(all.Entries), all.Left, ListLimit+3)
	}
}

// TestListOfAProjectWhereNothingWasRunYet: there is no state folder at all, and a
// person who asks is told that instead of being shown nothing or an error.
func TestListOfAProjectWhereNothingWasRunYet(t *testing.T) {
	runs, err := List(t.TempDir(), "naghuale-crewflow", false, listOf(monday))
	if err != nil {
		t.Fatalf("List returned an error: %v", err)
	}

	if len(runs.Entries) != 0 || len(runs.Unreadable) != 0 {
		t.Errorf("the list = %+v, want nothing at all", runs)
	}
	var stdout bytes.Buffer
	if err := runs.Write(&stdout); err != nil {
		t.Fatalf("Write returned an error: %v", err)
	}
	if want := "no runs yet\n"; stdout.String() != want {
		t.Errorf("the list of a project with no runs wrote %q, want %q", stdout.String(), want)
	}
}

// TestListWithAStateItCannotRead: one file crewflow cannot read does not take the
// tasks of the other ones down with it, and the file is named, because a person has
// to look at it.
func TestListWithAStateItCannotRead(t *testing.T) {
	home := t.TempDir()
	repo := "naghuale-crewflow"
	writeState(t, home, repo, 43, "the run of a task", nil, try{endedAt: monday, outcome: TimedOut})
	broken := filepath.Join(home, "state", repo, "44.json")
	if err := os.WriteFile(broken, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write %s: %v", broken, err)
	}
	if err := os.WriteFile(filepath.Join(home, "state", repo, "45.json"),
		[]byte(`{"task":45,"title":"a task with nothing in it","attempts":[]}`), 0o600); err != nil {
		t.Fatalf("write a state with no attempt in it: %v", err)
	}
	// What an interrupted write leaves behind is not the state of a task, and it is
	// not shown as one.
	if err := os.WriteFile(filepath.Join(home, "state", repo, "46.json.2837"), []byte("{"), 0o600); err != nil {
		t.Fatalf("write the leftovers of a state: %v", err)
	}

	runs, err := List(home, repo, false, listOf(monday.Add(time.Hour)))
	if err != nil {
		t.Fatalf("List returned an error: %v", err)
	}

	if len(runs.Entries) != 1 || runs.Entries[0].Task != 43 {
		t.Errorf("the list = %+v, want the one task that was run", runs.Entries)
	}
	if want := []string{broken, filepath.Join(home, "state", repo, "45.json")}; !slices.Equal(runs.Unreadable, want) {
		t.Errorf("the files the list could not read = %v, want %v", runs.Unreadable, want)
	}
	var stdout bytes.Buffer
	if err := runs.Write(&stdout); err != nil {
		t.Fatalf("Write returned an error: %v", err)
	}
	for _, want := range []string{"the run of a task", "not read: " + broken} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("the list wrote %q, want it to mention %q", stdout.String(), want)
		}
	}
}

// TestListDoesNotTouchTheStateOfATask: a list is a question, and a question is not a
// run: the state of a task is what a later run of it goes on with, and a list that
// rewrote it would be a run that nobody asked for.
func TestListDoesNotTouchTheStateOfATask(t *testing.T) {
	home := t.TempDir()
	repo := "naghuale-crewflow"
	writeState(t, home, repo, 43, "the run of a task", nil, try{startedAt: monday, outcome: Running, pid: 100})
	path := filepath.Join(home, "state", repo, "43.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the state: %v", err)
	}

	if _, err := List(home, repo, true, listOf(monday.Add(time.Hour))); err != nil {
		t.Fatalf("List returned an error: %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the state after the list: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("the state of the task is\n%s\nwant it left as it was:\n%s", after, before)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read the folder of the state: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("the folder of the state holds %d files, want only the state of the task", len(entries))
	}
}

// TestListAsJSON is the shape the orchestrator reads: one object per task, with the
// length of a run in seconds, because a program counts seconds and does not read
// "1h 05m".
func TestListAsJSON(t *testing.T) {
	home := t.TempDir()
	repo := "naghuale-crewflow"
	writeState(t, home, repo, 43, "открыл изменение", &Change{Number: 44, URL: "https://github.com/naghuale/crewflow/pull/44"},
		try{startedAt: monday, endedAt: monday.Add(42 * time.Minute), outcome: ChangeRequestOpened})
	writeState(t, home, repo, 44, "идёт сейчас 🚀", nil, try{startedAt: monday.Add(4 * time.Hour), outcome: Running, pid: 100})
	writeState(t, home, repo, 49, "окно закрыли", nil, try{startedAt: monday.Add(3 * time.Hour), outcome: Running, pid: 200})

	runs, err := List(home, repo, true, listOf(monday.Add(4*time.Hour+65*time.Second), 100))
	if err != nil {
		t.Fatalf("List returned an error: %v", err)
	}

	got := jsonOf(t, runs.Entries)
	if want := read(t, filepath.Join("testdata", "list.json")); got != want {
		t.Errorf("the list as JSON is\n%s\nwant\n%s", got, want)
	}
}

// TestListForAPerson is what a person reads: the runs that are going on top, the
// others from the last to the first, and the columns lined up however wide the letters
// of the titles are.
func TestListForAPerson(t *testing.T) {
	home := t.TempDir()
	repo := "naghuale-crewflow"
	writeState(t, home, repo, 43, "открыл изменение", &Change{Number: 44, URL: "https://github.com/naghuale/crewflow/pull/44"},
		try{startedAt: monday, endedAt: monday.Add(42 * time.Minute), outcome: ChangeRequestOpened})
	writeState(t, home, repo, 44, "идёт сейчас 🚀", nil, try{startedAt: monday.Add(4 * time.Hour), outcome: Running, pid: 100})
	writeState(t, home, repo, 49, "окно закрыли", nil, try{startedAt: monday.Add(3 * time.Hour), outcome: Running, pid: 200})
	for number := 60; number < 60+ListLimit; number++ {
		writeState(t, home, repo, number, "задача, которую запускали много раз", nil,
			try{startedAt: monday.Add(time.Duration(number) * time.Second),
				endedAt: monday.Add(time.Duration(number)*time.Second + 35*time.Second),
				outcome: BlockedPermission})
	}
	// The times of a list are the times of the machine of the person reading it, and
	// a golden file may not depend on where the tests run.
	local := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = local })

	runs, err := List(home, repo, false, listOf(monday.Add(4*time.Hour+65*time.Second), 100))
	if err != nil {
		t.Fatalf("List returned an error: %v", err)
	}
	var stdout bytes.Buffer
	if err := runs.Write(&stdout); err != nil {
		t.Fatalf("Write returned an error: %v", err)
	}

	if want := read(t, filepath.Join("testdata", "list.txt")); stdout.String() != want {
		t.Errorf("the list for a person is\n%s\nwant\n%s", stdout.String(), want)
	}
	if runs.Left != 3 {
		t.Errorf("the list left %d runs out, want the 3 of the tasks 60 and 61 and 62", runs.Left)
	}
}

// TestListOfARunWhoseProcessIsReal is the whole of it on a real process of the machine:
// a run whose state says it is going is going while the process of it is there, and it
// is interrupted the moment that process is over — whatever the state says, and even
// when another process holds the number the run had, which is what a reboot of a
// machine does to every number it gave out (docs/DESIGN.md §7).
func TestListOfARunWhoseProcessIsReal(t *testing.T) {
	home, repo := t.TempDir(), "naghuale-crewflow"
	env := listOf(time.Now())
	env.Running = proc.System().Alive

	sleeper := startSleeper(t, "30")
	writeState(t, home, repo, 43, "a run of a real process", nil,
		try{startedAt: sleeper.began, outcome: Running, pid: sleeper.pid})
	if got := outcomeOf(t, home, repo, env); got != Running {
		t.Errorf("the outcome of a run whose process is going = %q, want %q", got, Running)
	}

	// The process of the run is over and the state of the task still says that it is
	// running: that is what a run crewflow was killed in the middle of leaves behind.
	sleeper.over(t)
	if got := outcomeOf(t, home, repo, env); got != Interrupted {
		t.Errorf("the outcome of a run whose process is over = %q, want %q", got, Interrupted)
	}

	// A process that is going, with a moment beside it that is not its own: the number
	// of a process says nothing on its own, and this is the number of a run of
	// yesterday after the machine has been rebooted.
	other := startSleeper(t, "30")
	writeState(t, home, repo, 43, "a run of a real process", nil,
		try{startedAt: other.began, outcome: Running, pid: other.pid,
			processStartedAt: other.began.Add(-time.Hour)})
	if got := outcomeOf(t, home, repo, env); got != Interrupted {
		t.Errorf("the outcome of a run whose number another process holds = %q, want %q", got, Interrupted)
	}
}

// sleeper is a program of the machine of a test that is going, and the moment it began:
// what a run of crewflow writes into the state of a task about itself.
type sleeper struct {
	cmd   *exec.Cmd
	pid   int
	began time.Time
}

// startSleeper starts a real program on the machine of the test and asks the machine
// when it began. Nothing of it is faked: a real process is the only way to ask a real
// question about a real one, and the program is stopped when the test is over.
func startSleeper(t *testing.T, seconds string) sleeper {
	t.Helper()
	cmd := exec.Command("sleep", seconds)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start a program of the machine: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	begin, there := proc.System().StartedAt(cmd.Process.Pid)
	if !there {
		_ = cmd.Process.Kill()
		t.Fatalf("the machine does not know of the process %d, which is going", cmd.Process.Pid)
	}
	return sleeper{cmd: cmd, pid: cmd.Process.Pid, began: begin}
}

// over ends the program of a test and waits for it, and stops it when the test is over
// before the program is. What it exited with is of no interest to anybody here: the
// test wants a process that is over and not one that is over well.
func (s sleeper) over(t *testing.T) {
	t.Helper()
	_ = s.cmd.Process.Kill()
	_ = s.cmd.Wait()
}

// outcomeOf is what a list says about the task 43 of a project of a test.
func outcomeOf(t *testing.T, home, repo string, env ListEnv) Kind {
	t.Helper()
	runs, err := List(home, repo, true, env)
	if err != nil {
		t.Fatalf("List returned an error: %v", err)
	}
	entry, ok := runs.entry(43)
	if !ok {
		t.Fatalf("the list holds %+v, want the task 43 in it", runs.Entries)
	}
	return entry.Outcome
}

// jsonOf is the answer of a command in JSON, in the shape `printJSON` writes it in, so
// that the file a test compares against is the file a person gets.
func jsonOf(t *testing.T, answer any) string {
	t.Helper()
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(answer); err != nil {
		t.Fatalf("print the answer as JSON: %v", err)
	}
	return out.String()
}

// listOf is the machine a list of runs of a test is asked: the clock of the test, the
// processes that are going, and the time limit of a run of the project.
func listOf(now time.Time, going ...int) ListEnv {
	return ListEnv{
		Now:     func() time.Time { return now },
		Running: func(process proc.Process) bool { return slices.Contains(going, process.Pid) },
		Timeout: time.Hour,
	}
}

// try is one attempt of a task as `crewflow task list` reads it. A pid of zero is a
// state of before crewflow kept the number of the process of a run, and a moment of a
// process of its own is a number that has been given to another process since.
type try struct {
	startedAt time.Time
	endedAt   time.Time
	outcome   Kind
	pid       int
	// processStartedAt is when the process of the run began; the moment the run was
	// started at is what a state without it holds.
	processStartedAt time.Time
}

// writeState puts the state of a task where crewflow keeps it, with the attempts it
// took and the change request the last of them opened.
func writeState(t *testing.T, home, repo string, number int, title string, change *Change, tries ...try) {
	t.Helper()
	state := State{
		Number:   number,
		Title:    title,
		Branch:   fmt.Sprintf("crewflow/%d-task", number),
		Worktree: filepath.Join("/w", strconv.Itoa(number)),
		Profile:  "opencode",
	}
	state.Change = change
	journals := newJournals(home, repo)
	for i, attempt := range tries {
		begin := attempt.processStartedAt
		if begin.IsZero() {
			begin = attempt.startedAt
		}
		state = state.NextAttempt(attempt.startedAt,
			journals.JournalPath(number, i+1),
			journals.errorJournalPath(number, i+1),
			i > 0, proc.Process{Pid: attempt.pid, StartedAt: begin},
			Identity{Mode: "owner", Description: "owner — the login gh naghuale (shared rights)"})
		if attempt.outcome != Running {
			state = state.Ended(attempt.endedAt, attempt.outcome)
		}
	}
	if len(tries) == 0 {
		t.Fatalf("the task %d has no attempt, and a list has nothing to show of it", number)
	}
	if err := SaveState(journals.StatePath(number), state); err != nil {
		t.Fatalf("write the state of the task %d: %v", number, err)
	}
}
