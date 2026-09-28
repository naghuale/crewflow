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

// TestListOrder is what a person asks a list of runs for: the runs that want somebody
// today on top — going, blocked, cut short — and the rest from the last try of a task
// to the first. A run that wants a person is above a run that does not whatever it
// was started with, because it is the one that is waited for (docs/DESIGN.md §7).
func TestListOrder(t *testing.T) {
	home := t.TempDir()
	repo := "naghuale-crewflow"
	writeState(t, home, repo, 43, "the run of a task", nil,
		try{startedAt: monday.Add(8 * time.Hour), endedAt: monday.Add(8*time.Hour + 42*time.Minute), outcome: ChangeRequestOpened})
	writeState(t, home, repo, 44, "the journal of a run", nil,
		try{endedAt: monday.Add(30 * time.Minute), outcome: TimedOut},
		try{startedAt: monday.Add(time.Hour), endedAt: monday.Add(90 * time.Minute), outcome: BlockedPermission})
	writeState(t, home, repo, 49, "a run that is going", nil, try{startedAt: monday.Add(2 * time.Hour), outcome: Running, pid: 100})
	writeState(t, home, repo, 50, "a run whose window was closed", nil, try{startedAt: monday.Add(3 * time.Hour), outcome: Running, pid: 200})
	writeState(t, home, repo, 51, "a run that was cut short", nil,
		try{startedAt: monday.Add(4 * time.Hour), endedAt: monday.Add(4*time.Hour + time.Minute), outcome: Interrupted})
	writeState(t, home, repo, 52, "a run the executor was lost in", nil,
		try{startedAt: monday.Add(5 * time.Hour), endedAt: monday.Add(5 * time.Hour), outcome: ExecutorFailed})
	writeState(t, home, repo, 53, "a run crewflow kept no process of", nil,
		try{startedAt: monday.Add(6 * time.Hour), outcome: Running})

	runs, err := List(home, repo, listOf(monday.Add(9*time.Hour), 100))
	if err != nil {
		t.Fatalf("List returned an error: %v", err)
	}

	// The task 43 was started last of all and it is the last in the list: what came of
	// a run is a thing to read, and what a run wants is a thing to do.
	want := []int{53, 52, 51, 50, 49, 44, 43}
	if got := tasksOf(runs); !slices.Equal(got, want) {
		t.Errorf("the list is of the tasks %v, want %v", got, want)
	}
	if runs.Left != 0 {
		t.Errorf("a list of a project with %d tasks leaves %d of them out, want none", len(want), runs.Left)
	}
	if runs.Repo != repo || runs.Total != len(want) {
		t.Errorf("the list is of %q with %d tasks, want %q with %d", runs.Repo, runs.Total, repo, len(want))
	}
}

// TestListSaysHowEachRunCameOut: the outcome of a run is what a person reads first,
// and an attempt that says "running" is only running while the process of the run is
// still there — a window that was closed and a machine that was rebooted are both a
// run that is over (docs/DESIGN.md §7). A list says as well who ran the last try and
// whose name it went under, and where its journal is (docs/DESIGN.md §7i).
func TestListSaysHowEachRunCameOut(t *testing.T) {
	home := t.TempDir()
	repo := "naghuale-crewflow"
	writeState(t, home, repo, 43, "opened a change request", &Change{Number: 44, URL: "https://github.com/naghuale/crewflow/pull/44"},
		try{startedAt: monday, endedAt: monday.Add(42 * time.Minute), outcome: ChangeRequestOpened, mode: "bot"})
	writeState(t, home, repo, 44, "going right now", nil,
		try{startedAt: monday, endedAt: monday.Add(42 * time.Minute), outcome: Blocked},
		try{startedAt: monday.Add(time.Hour), outcome: Running, pid: 100, mode: "bot"})
	writeState(t, home, repo, 49, "the window was closed", nil,
		try{startedAt: monday.Add(time.Hour), outcome: Running, pid: 200})
	writeState(t, home, repo, 50, "two tries", nil,
		try{startedAt: monday, endedAt: monday.Add(42 * time.Minute), outcome: NoChangeRequest},
		try{startedAt: monday.Add(3 * time.Hour), endedAt: monday.Add(3*time.Hour + 35*time.Second), outcome: OutOfScope, pid: 300})
	writeStateOfBefore(t, home, repo, 51, "a run of before the mode and the agent were kept",
		try{startedAt: monday.Add(3 * time.Hour), endedAt: monday.Add(3*time.Hour + 35*time.Second), outcome: OutOfScope})

	cases := []struct {
		name     string
		task     int
		outcome  Kind
		attempts int
		length   time.Duration
		// known says whether the length of the run is known at all: a run whose
		// process is gone was not seen to stop, and how long it went on is in
		// nothing crewflow kept.
		known    bool
		change   string
		executor string
		mode     string
		run      string
		endedAt  time.Time
	}{
		{
			name: "the run opened the change request of the branch", task: 43,
			outcome: ChangeRequestOpened, attempts: 1, known: true, length: 42 * time.Minute,
			change: "https://github.com/naghuale/crewflow/pull/44", executor: "opencode", mode: "bot",
			run: "43-1", endedAt: monday.Add(42 * time.Minute),
		},
		{
			name: "the last try of the task is going", task: 44,
			outcome: Running, attempts: 2, known: true, length: 3 * time.Hour,
			executor: "opencode", mode: "bot", run: "44-2",
		},
		{
			name: "the process of the run is gone", task: 49,
			outcome: Interrupted, attempts: 1, known: false,
			executor: "opencode", mode: "owner", run: "49-1",
		},
		{
			name: "what the last try cost", task: 50,
			outcome: OutOfScope, attempts: 2, known: true, length: 35 * time.Second,
			executor: "opencode", mode: "owner", run: "50-2",
			endedAt: monday.Add(3*time.Hour + 35*time.Second),
		},
		{
			// A state of before crewflow kept the mode of a run names no agent and
			// no name, and a list of runs says a dash where there is no agent and
			// the owner where the mode is not there: a run of before was the
			// owner's, for there was no other (docs/DESIGN.md §7i).
			name: "a state of before the mode and the agent were kept", task: 51,
			outcome: OutOfScope, attempts: 1, known: true, length: 35 * time.Second,
			executor: "", mode: "", run: "51-1",
			endedAt: monday.Add(3*time.Hour + 35*time.Second),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runs, err := List(home, repo, listOf(monday.Add(4*time.Hour), 100))
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
			if entry.Repo != repo {
				t.Errorf("the entry is of the project %q, want %q", entry.Repo, repo)
			}
			if entry.Executor != tc.executor {
				t.Errorf("the executor = %q, want %q", entry.Executor, tc.executor)
			}
			if entry.Identity.Mode != tc.mode {
				t.Errorf("the mode of the run = %q, want %q", entry.Identity.Mode, tc.mode)
			}
			if got := entry.Run(); got != tc.run {
				t.Errorf("the run = %q, want %q", got, tc.run)
			}
			length, known := entry.Length()
			if known != tc.known {
				t.Errorf("the length of the run is %s and known %t, want it known %t", length, known, tc.known)
			}
			if known && length != tc.length {
				t.Errorf("the run took %s, want %s", length, tc.length)
			}
			if got, want := changeOf(entry), tc.change; got != want {
				t.Errorf("the change request = %q, want %q", got, want)
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
		// timeout is how long a run of the project may take, and zero is the time
		// limit nobody said: a list of every project on the machine is asked from
		// any folder, and what it does not know it does not end a run with.
		timeout time.Duration
		task    int
		want    Kind
	}{
		{
			name:    "a run of an hour ago may still be going",
			now:     monday.Add(59 * time.Minute),
			timeout: time.Hour,
			task:    43,
			want:    MaybeRunning,
		},
		{
			name:    "a run of the length of the whole timeout may be going",
			now:     monday.Add(time.Hour),
			timeout: time.Hour,
			task:    44,
			want:    MaybeRunning,
		},
		{
			name:    "a run of yesterday is not going",
			now:     monday.Add(25 * time.Hour),
			timeout: time.Hour,
			task:    43,
			want:    Interrupted,
		},
		{
			name:    "a run of last week is not going either, when a project says how long a run may take",
			now:     monday.Add(8 * 24 * time.Hour),
			timeout: time.Hour,
			task:    43,
			want:    Interrupted,
		},
		{
			name: "a run nobody knows the time limit of is what the state says it is",
			now:  monday.Add(25 * time.Hour),
			task: 44,
			want: MaybeRunning,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := listOf(tc.now)
			env.Timeout = tc.timeout
			runs, err := List(home, repo, env)
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

	if _, err := List(home, repo, env); err != nil {
		t.Fatalf("List returned an error: %v", err)
	}

	if !slices.Equal(asked, []int{200}) {
		t.Errorf("the machine was asked about the processes %v, want only the one of the run that is going", asked)
	}
}

// TestListIsShort: a person asks what is going on and what was going on, and twenty
// tasks is more of that than anybody reads; a list of every project on the machine is
// what a person asks for when they mean all of it, and it is not short.
func TestListIsShort(t *testing.T) {
	home := t.TempDir()
	repo := "naghuale-crewflow"
	for number := 1; number <= ListLimit+3; number++ {
		writeState(t, home, repo, number, "a task", nil,
			try{startedAt: monday.Add(time.Duration(number) * time.Minute), outcome: TimedOut})
	}

	runs, err := List(home, repo, listOf(monday.Add(2*time.Hour)))
	if err != nil {
		t.Fatalf("List returned an error: %v", err)
	}
	if len(runs.Entries) != ListLimit {
		t.Errorf("the list holds %d runs, want the last %d", len(runs.Entries), ListLimit)
	}
	if runs.Left != 3 {
		t.Errorf("the list left %d runs out, want 3", runs.Left)
	}
	if runs.Total != ListLimit+3 {
		t.Errorf("the list says the project has %d tasks, want %d of them however many fit into it", runs.Total, ListLimit+3)
	}
	if last, first := runs.Entries[len(runs.Entries)-1], runs.Entries[0]; last.Task != 4 || first.Task != ListLimit+3 {
		t.Errorf("the list goes from the task %d to the task %d, want the newest %d and then down to 4",
			first.Task, last.Task, ListLimit+3)
	}

	all, err := EveryProject(home, listOf(monday.Add(2*time.Hour)))
	if err != nil {
		t.Fatalf("EveryProject returned an error: %v", err)
	}
	if len(all.Entries) != ListLimit+3 || all.Left != 0 {
		t.Errorf("a list of the whole machine holds %d runs and leaves %d out, want %d and none",
			len(all.Entries), all.Left, ListLimit+3)
	}
	if all.Repo != "" {
		t.Errorf("a list of the whole machine is of the project %q, want it of none of them", all.Repo)
	}
}

// TestEveryProjectIsEveryProjectOfTheMachine: `-all` is asked from any folder, and it
// is every run of every project that has one, with the project in it — the runs of
// the two projects of a machine are ordered together, and a project of the machine
// is named the way a person writes it.
func TestEveryProjectIsEveryProjectOfTheMachine(t *testing.T) {
	home := t.TempDir()
	writeState(t, home, "naghuale-crewflow", 43, "a task of crewflow", nil,
		try{startedAt: monday.Add(time.Hour), endedAt: monday.Add(time.Hour + time.Minute), outcome: ChangeRequestOpened})
	writeState(t, home, "naghuale-tele", 7, "a task of tele", nil,
		try{startedAt: monday.Add(2 * time.Hour), outcome: Running, pid: 100})

	runs, err := EveryProject(home, listOf(monday.Add(3*time.Hour), 100))
	if err != nil {
		t.Fatalf("EveryProject returned an error: %v", err)
	}

	want := []string{"naghuale-tele", "naghuale-crewflow"}
	got := make([]string, 0, len(runs.Entries))
	for _, entry := range runs.Entries {
		got = append(got, entry.Repo)
	}
	if !slices.Equal(got, want) {
		t.Errorf("the list is of %v, want %v", got, want)
	}
	if len(runs.Elsewhere) != 0 {
		t.Errorf("a list of the whole machine says %+v is elsewhere, want nothing besides it", runs.Elsewhere)
	}
}

// TestEveryProjectAsksTheMachineAboutEveryProject: a run of another project on the
// machine is as much of this machine as the one of the project of the folder, and a
// run that is going is asked about in either.
func TestEveryProjectAsksTheMachineAboutEveryProject(t *testing.T) {
	home := t.TempDir()
	writeState(t, home, "naghuale-crewflow", 43, "going here", nil,
		try{startedAt: monday, outcome: Running, pid: 100})
	writeState(t, home, "naghuale-tele", 7, "going there", nil,
		try{startedAt: monday, outcome: Running, pid: 200})

	asked := []int{}
	env := listOf(monday.Add(time.Minute))
	env.Running = func(process proc.Process) bool {
		asked = append(asked, process.Pid)
		return true
	}

	if _, err := EveryProject(home, env); err != nil {
		t.Fatalf("EveryProject returned an error: %v", err)
	}

	slices.Sort(asked)
	if !slices.Equal(asked, []int{100, 200}) {
		t.Errorf("the machine was asked about the processes %v, want the runs of both projects", asked)
	}
}

// TestListSaysWhatTheOtherProjectsOfTheMachineAreDoing: a person in the folder of a
// project cannot see what runs beside it, and the owner of this machine runs one
// project at a time and gets no news of the other (docs/DESIGN.md §6).
func TestListSaysWhatTheOtherProjectsOfTheMachineAreDoing(t *testing.T) {
	home := t.TempDir()
	here, other := "naghuale-crewflow", "naghuale-tele"
	writeState(t, home, here, 43, "a task of this project", nil,
		try{startedAt: monday, endedAt: monday.Add(time.Minute), outcome: ChangeRequestOpened})
	writeState(t, home, other, 7, "a task of the other project", nil,
		try{startedAt: monday, outcome: Running, pid: 100})
	writeState(t, home, other, 8, "another task of the other project", nil,
		try{startedAt: monday, endedAt: monday.Add(time.Minute), outcome: ChangeRequestOpened})
	writeState(t, home, other, 9, "and a third one", nil,
		try{startedAt: monday, endedAt: monday.Add(time.Minute), outcome: ChangeRequestOpened})
	// A project nothing was ever run in is not on the machine as far as a list of
	// runs is concerned, and a line of nothing but zeros is not what a person reads.
	if err := os.MkdirAll(filepath.Join(home, "state", "naghuale-nothing"), 0o700); err != nil {
		t.Fatalf("make the state folder of a project with no runs: %v", err)
	}

	runs, err := List(home, here, listOf(monday.Add(time.Hour), 100))
	if err != nil {
		t.Fatalf("List returned an error: %v", err)
	}

	var stdout bytes.Buffer
	if err := runs.Write(&stdout); err != nil {
		t.Fatalf("Write returned an error: %v", err)
	}
	want := "\n  also on this machine: naghuale/tele — 1 running, 2 pr-opened\n" +
		"  show everything: crewflow task list -all\n"
	if !strings.HasSuffix(stdout.String(), want) {
		t.Errorf("the list is\n%swant it to end with\n%s", stdout.String(), want)
	}
}

// TestListOfAMachineWithOneProjectOnly: there is nothing to say about the other
// projects of a machine that has none, and a line about nothing is worse than no line.
func TestListOfAMachineWithOneProjectOnly(t *testing.T) {
	home := t.TempDir()
	repo := "naghuale-crewflow"
	writeState(t, home, repo, 43, "a task of this project", nil,
		try{startedAt: monday, endedAt: monday.Add(time.Minute), outcome: ChangeRequestOpened})

	runs, err := List(home, repo, listOf(monday.Add(time.Hour)))
	if err != nil {
		t.Fatalf("List returned an error: %v", err)
	}

	var stdout bytes.Buffer
	if err := runs.Write(&stdout); err != nil {
		t.Fatalf("Write returned an error: %v", err)
	}
	for _, unwanted := range []string{"also on this machine", "show everything"} {
		if strings.Contains(stdout.String(), unwanted) {
			t.Errorf("the list wrote %q, want it to say nothing about %q", stdout.String(), unwanted)
		}
	}
}

// TestListOfAProjectWhereNothingWasRunYet: there is no state folder at all, and a
// person who asks is told that instead of being shown nothing or an error — with the
// project over the table, because a list of nothing is still a list of a project.
func TestListOfAProjectWhereNothingWasRunYet(t *testing.T) {
	runs, err := List(t.TempDir(), "naghuale-crewflow", listOf(monday))
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
	if want := "naghuale/crewflow · 0 tasks\nno runs yet\n"; stdout.String() != want {
		t.Errorf("the list of a project with no runs wrote %q, want %q", stdout.String(), want)
	}
}

// TestListWithAStateItCannotRead: one file crewflow cannot read does not take the
// tasks of the other ones down with it, and the file is named, because a person has
// to look at it. A broken state of another project is not a thing of this project.
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
	elsewhere := filepath.Join(home, "state", "naghuale-tele", "43.json")
	if err := os.MkdirAll(filepath.Dir(elsewhere), 0o700); err != nil {
		t.Fatalf("make the state folder of another project: %v", err)
	}
	if err := os.WriteFile(elsewhere, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write a state of another project crewflow cannot read: %v", err)
	}

	runs, err := List(home, repo, listOf(monday.Add(time.Hour)))
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
	if strings.Contains(stdout.String(), elsewhere) {
		t.Errorf("the list wrote %q, want nothing about the state of another project", stdout.String())
	}
}

// TestListDoesNotTouchTheStateOfATask: a list is a question, and a question is not a
// run: the state of a task is what a later run of it goes on with, and a list that
// rewrote it would be a run that nobody asked for.
func TestListDoesNotTouchTheStateOfATask(t *testing.T) {
	home := t.TempDir()
	repo := "naghuale-crewflow"
	writeState(t, home, repo, 43, "a run of a task", nil, try{startedAt: monday, outcome: Running, pid: 100})
	path := filepath.Join(home, "state", repo, "43.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the state: %v", err)
	}

	if _, err := List(home, repo, listOf(monday.Add(time.Hour))); err != nil {
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
// project, the agent of the last try, which run that was and where the change request
// is, and the length of a run in seconds, because a program counts seconds and does
// not read "1h 05m".
func TestListAsJSON(t *testing.T) {
	home := t.TempDir()
	repo := "naghuale-crewflow"
	writeState(t, home, repo, 43, "открыл изменение", &Change{Number: 44, URL: "https://github.com/naghuale/crewflow/pull/44"},
		try{startedAt: monday, endedAt: monday.Add(42 * time.Minute), outcome: ChangeRequestOpened, mode: "bot"})
	writeState(t, home, repo, 44, "идёт сейчас 🚀", nil, try{startedAt: monday.Add(4 * time.Hour), outcome: Running, pid: 100})
	writeState(t, home, repo, 49, "окно закрыли", nil, try{startedAt: monday.Add(3 * time.Hour), outcome: Running, pid: 200})

	runs, err := List(home, repo, listOf(monday.Add(4*time.Hour+65*time.Second), 100))
	if err != nil {
		t.Fatalf("List returned an error: %v", err)
	}

	got := jsonOf(t, runs.Entries)
	if want := read(t, filepath.Join("testdata", "list.json")); got != want {
		t.Errorf("the list as JSON is\n%s\nwant\n%s", got, want)
	}
}

// TestListForAPerson is what a person reads: which project it is and how many tasks
// it has, the runs that want somebody today on top, the rest from the last to the
// first, the columns lined up however wide the letters of the titles are, and under
// the table what the other projects of the machine are doing.
func TestListForAPerson(t *testing.T) {
	home, repo := t.TempDir(), "naghuale-crewflow"
	machineOfTest(t, home)
	utc(t)

	runs, err := List(home, repo, listOf(monday.Add(4*time.Hour+65*time.Second), 100))
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

// TestEveryProjectForAPerson is the same list of every project of the machine: the
// project is the first column of it, and nothing is said under the table, because all
// of it is already there.
func TestEveryProjectForAPerson(t *testing.T) {
	home := t.TempDir()
	machineOfTest(t, home)
	utc(t)

	all, err := EveryProject(home, listOf(monday.Add(4*time.Hour+65*time.Second), 100))
	if err != nil {
		t.Fatalf("EveryProject returned an error: %v", err)
	}
	var stdout bytes.Buffer
	if err := all.Write(&stdout); err != nil {
		t.Fatalf("Write returned an error: %v", err)
	}

	if want := read(t, filepath.Join("testdata", "list-all.txt")); stdout.String() != want {
		t.Errorf("the list of the whole machine is\n%s\nwant\n%s", stdout.String(), want)
	}
	if all.Left != 0 {
		t.Errorf("a list of the whole machine left %d runs out, want none", all.Left)
	}
}

// TestListWithoutATerminal: a list goes into a file and through a pipe as often as it
// goes onto a screen, and a table that draws over the terminal of whoever asked for
// it, or repaints itself in a loop, is not a table anybody reads (docs/DESIGN.md §6).
func TestListWithoutATerminal(t *testing.T) {
	home, repo := t.TempDir(), "naghuale-crewflow"
	machineOfTest(t, home)
	utc(t)

	runs, err := List(home, repo, listOf(monday.Add(4*time.Hour+65*time.Second), 100))
	if err != nil {
		t.Fatalf("List returned an error: %v", err)
	}
	var file bytes.Buffer
	if err := runs.Write(&file); err != nil {
		t.Fatalf("Write returned an error: %v", err)
	}

	for _, letter := range file.String() {
		if letter < ' ' && letter != '\n' {
			t.Errorf("the list wrote the control character %q, want a table of letters and nothing else", letter)
		}
		if letter == 0x7f {
			t.Error("the list wrote a delete, want a table of letters and nothing else")
		}
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
	runs, err := List(home, repo, env)
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

// utc is the time zone a list of runs is written in for the tests that compare it
// with a file: the times of a list are the times of the machine of the person reading
// it, and a golden file may not depend on where the tests run.
func utc(t *testing.T) {
	t.Helper()
	local := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = local })
}

// tasksOf is the numbers of the tasks of a list, in the order of it.
func tasksOf(runs Runs) []int {
	tasks := make([]int, 0, len(runs.Entries))
	for _, entry := range runs.Entries {
		tasks = append(tasks, entry.Task)
	}
	return tasks
}

// changeOf is the change request of an entry, and nothing where there is none.
func changeOf(entry Entry) string {
	if entry.Change == nil {
		return ""
	}
	return entry.Change.URL
}

// try is one attempt of a task as `crewflow task list` reads it. A pid of zero is a
// state of before crewflow kept the number of the process of a run, and a moment of a
// process of its own is a number that has been given to another process since. The
// mode is whose name the run went under.
type try struct {
	startedAt time.Time
	endedAt   time.Time
	outcome   Kind
	pid       int
	// processStartedAt is when the process of the run began; the moment the run was
	// started at is what a state without it holds.
	processStartedAt time.Time
	mode             string
}

// machineOfTest fills the home of a test with a machine of two projects: the runs of
// the project the test is about, and the runs of another one that a list of it says
// nothing less than the name of. It is what the tables of the tests are made of.
func machineOfTest(t *testing.T, home string) {
	t.Helper()
	writeState(t, home, "naghuale-crewflow", 43, "открыл изменение",
		&Change{Number: 44, URL: "https://github.com/naghuale/crewflow/pull/44"},
		try{startedAt: monday, endedAt: monday.Add(42 * time.Minute), outcome: ChangeRequestOpened, mode: "bot"})
	writeState(t, home, "naghuale-crewflow", 44, "идёт сейчас 🚀", nil,
		try{startedAt: monday.Add(4 * time.Hour), outcome: Running, pid: 100, mode: "bot"})
	writeState(t, home, "naghuale-crewflow", 49, "окно закрыли, и это название не влезает в колонку", nil,
		try{startedAt: monday.Add(3 * time.Hour), outcome: Running, pid: 200})
	for number := 60; number < 60+ListLimit; number++ {
		writeState(t, home, "naghuale-crewflow", number, "задача, которую запускали много раз", nil,
			try{startedAt: monday.Add(time.Duration(number) * time.Second),
				endedAt: monday.Add(time.Duration(number)*time.Second + 35*time.Second),
				outcome: BlockedPermission})
	}
	writeState(t, home, "naghuale-tele", 7, "починить то, что сломано", nil,
		try{startedAt: monday.Add(2 * time.Hour), outcome: Running, pid: 100})
	writeState(t, home, "naghuale-tele", 8, "и починить то, что сломается", nil,
		try{startedAt: monday.Add(time.Hour), endedAt: monday.Add(time.Hour + time.Minute), outcome: ChangeRequestOpened})
	writeState(t, home, "naghuale-tele", 9, "и ещё одно", nil,
		try{startedAt: monday.Add(30 * time.Minute), endedAt: monday.Add(31 * time.Minute), outcome: ChangeRequestOpened})
}

// writeState puts the state of a task where crewflow keeps it: the profile of the
// executor of the project and the mode of every attempt of it, and the change request
// the last of them opened.
func writeState(t *testing.T, home, repo string, number int, title string, change *Change, tries ...try) {
	t.Helper()
	put(t, home, repo, number, title, "opencode", "owner", change, tries...)
}

// writeStateOfBefore is the state of a task of before crewflow kept the profile of
// the executor and the mode of a run: a run of before names no agent and says nothing
// about whose name it went under, and a list of runs reads such a state as it is
// (docs/DESIGN.md §7i).
func writeStateOfBefore(t *testing.T, home, repo string, number int, title string, tries ...try) {
	t.Helper()
	put(t, home, repo, number, title, "", "", nil, tries...)
}

// put writes the state of a task: the agent of the runs, whose name they went under,
// the change request and every attempt of it, in the folder of the project they
// belong to.
func put(t *testing.T, home, repo string, number int, title, executor, mode string, change *Change, tries ...try) {
	t.Helper()
	if len(tries) == 0 {
		t.Fatalf("the task %d has no attempt, and a list has nothing to show of it", number)
	}
	state := State{
		Number:   number,
		Title:    title,
		Branch:   fmt.Sprintf("crewflow/%d-task", number),
		Worktree: filepath.Join("/w", strconv.Itoa(number)),
		Profile:  executor,
		Change:   change,
	}
	journals := newJournals(home, repo)
	for i, attempt := range tries {
		begin := attempt.processStartedAt
		if begin.IsZero() {
			begin = attempt.startedAt
		}
		under := attempt.mode
		if under == "" {
			under = mode
		}
		state = state.NextAttempt(StartOf{
			Started:      attempt.startedAt,
			Journal:      journals.JournalPath(number, i+1),
			ErrorJournal: journals.errorJournalPath(number, i+1),
			Continued:    i > 0,
			Process:      proc.Process{Pid: attempt.pid, StartedAt: begin},
			Identity:     Identity{Mode: under, Description: descriptionOf(under)},
		})
		if attempt.outcome != Running {
			state = state.Ended(attempt.endedAt, attempt.outcome)
		}
	}
	if err := SaveState(journals.StatePath(number), state); err != nil {
		t.Fatalf("write the state of the task %d: %v", number, err)
	}
}

// descriptionOf is the one line a report of a run under that name shows, and nothing
// at all where the state of before crewflow kept the mode says no name.
func descriptionOf(mode string) string {
	if mode == "" {
		return ""
	}
	return mode + " — the account the run of a test went under"
}
