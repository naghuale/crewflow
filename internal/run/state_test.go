package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/proc"
	"github.com/naghuale/crewflow/internal/secret"
)

// TestJournalsPaths checks where the files of a run are kept: under the root
// crewflow has of its own, by the project and the task, and the way out of the
// executor next to what it wrote (docs/DESIGN.md §7).
func TestJournalsPaths(t *testing.T) {
	home := t.TempDir()
	journals := newJournals(home, "naghuale-crewflow")

	cases := []struct {
		name string
		got  string
		want string
	}{
		{
			name: "the state of a task",
			got:  journals.StatePath(43),
			want: filepath.Join(home, "state", "naghuale-crewflow", "43.json"),
		},
		{
			name: "the journal of a first attempt",
			got:  journals.JournalPath(43, 1),
			want: filepath.Join(home, "runs", "naghuale-crewflow", "43-1.jsonl"),
		},
		{
			name: "the way out of the second attempt",
			got:  journals.errorJournalPath(43, 2),
			want: filepath.Join(home, "runs", "naghuale-crewflow", "43-2.err"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("the file = %q, want %q", tc.got, tc.want)
			}
			if !strings.HasPrefix(tc.got, home) {
				t.Errorf("the file %q is not under the root of crewflow, want it under %q", tc.got, home)
			}
		})
	}
}

// TestBeginOpensTheFilesOfAnAttempt checks that the two files of an attempt are
// there before the executor writes a line into them, and that what is written to
// them is in them right away: a run that crewflow is killed in the middle of leaves
// its journal behind, and a journal written at the end of a run is empty for exactly
// that run (docs/DESIGN.md §7).
func TestBeginOpensTheFilesOfAnAttempt(t *testing.T) {
	journals := newJournals(t.TempDir(), "naghuale-crewflow")

	files, err := journals.Begin(nil, 43, 1)
	if err != nil {
		t.Fatalf("Begin returned an error: %v", err)
	}
	if _, err := io.WriteString(files.Out, "I did the work.\n"); err != nil {
		t.Fatalf("write the journal: %v", err)
	}
	if _, err := io.WriteString(files.ErrOut, "a refusal\n"); err != nil {
		t.Fatalf("write the way out: %v", err)
	}

	if got := read(t, files.Journal); got != "I did the work.\n" {
		t.Errorf("the journal holds %q before the run is over, want what the executor wrote", got)
	}
	if got := read(t, files.ErrorJournal); got != "a refusal\n" {
		t.Errorf("the journal of the way out holds %q before the run is over, want what the executor said", got)
	}
	if err := files.Close(); err != nil {
		t.Fatalf("Close returned an error: %v", err)
	}
	if got := read(t, files.Journal); got != "I did the work.\n" {
		t.Errorf("the journal holds %q after the run, want what the executor wrote", got)
	}
}

// TestTheFilesOfAnAttemptAreWrittenWholeWhenTheyAreClosed: a writer of the boundary holds
// back the end of what was written while it may still be the beginning of a value, and a
// run writes into its journal after the last flush of it — the line of the run that goes
// on by itself, the event of a provider that refused it. The files of an attempt are
// closed on every path a run ends by, and what was held back when they are is written
// there: the tail of the last line of a journal is a line nobody closes, and the value a
// person chose may begin with the end of a line (R5-NEW-5, docs/DESIGN.md §7e).
func TestTheFilesOfAnAttemptAreWrittenWholeWhenTheyAreClosed(t *testing.T) {
	out := secret.NewOut(secret.Chosen("\ns3cret")...)
	journals := newJournals(t.TempDir(), "naghuale-crewflow")

	files, err := journals.Begin(out, 43, 1)
	if err != nil {
		t.Fatalf("Begin returned an error: %v", err)
	}
	if _, err := io.WriteString(files.Out, "I did the work.\n"); err != nil {
		t.Fatalf("write the journal: %v", err)
	}
	if err := files.Close(); err != nil {
		t.Fatalf("Close returned an error: %v", err)
	}

	if got := read(t, files.Journal); got != "I did the work.\n" {
		t.Errorf("the journal holds %q, want the whole of what the executor wrote", got)
	}
}

// TestBeginEmptiesTheFilesOfAnAttempt: the file of an attempt is written by one
// run, and what the run of another attempt wrote is in the file of that attempt.
func TestBeginEmptiesTheFilesOfAnAttempt(t *testing.T) {
	journals := newJournals(t.TempDir(), "naghuale-crewflow")
	first, err := journals.Begin(nil, 43, 1)
	if err != nil {
		t.Fatalf("Begin returned an error: %v", err)
	}
	if _, err := io.WriteString(first.Out, "the first attempt\n"); err != nil {
		t.Fatalf("write the journal: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close returned an error: %v", err)
	}

	again, err := journals.Begin(nil, 43, 1)
	if err != nil {
		t.Fatalf("Begin of the same attempt returned an error: %v", err)
	}
	if err := again.Close(); err != nil {
		t.Fatalf("Close returned an error: %v", err)
	}
	if got := read(t, again.Journal); got != "" {
		t.Errorf("the journal of the attempt holds %q, want nothing: a run starts with an empty file", got)
	}
}

// TestStateRoundTrip checks that what crewflow keeps of a task survives being
// written and read again, which is all the state is for: a run that was
// interrupted has to be continued where it stopped (docs/DESIGN.md §7).
func TestStateRoundTrip(t *testing.T) {
	started := time.Date(2026, time.September, 28, 10, 0, 0, 0, time.UTC)
	ended := started.Add(42 * time.Minute)
	journals := newJournals(t.TempDir(), "naghuale-crewflow")

	state := State{
		Number:   43,
		Title:    "crewflow task run",
		Branch:   "crewflow/43-crewflow-task-run",
		Worktree: "/w/43",
		Profile:  "opencode",
		Session:  "ses_7fKq2",
	}
	attempt := state.NextAttempt(state.NextNumber(), StartOf{
		Started:      started,
		Journal:      journals.JournalPath(43, 1),
		ErrorJournal: journals.errorJournalPath(43, 1),
		Process:      proc.Process{Pid: 4242, StartedAt: started},
		Identity:     Identity{Mode: "bot", Description: "bot — GitHub App crewflow-executor (installation 12345)"},
	})
	attempt = attempt.Ended(1, ended, TimedOut)
	path := journals.StatePath(43)
	keeps(t, path, attempt)

	read, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState returned an error: %v", err)
	}

	if read.Number != 43 || read.Branch != attempt.Branch || read.Worktree != attempt.Worktree {
		t.Errorf("the state read back = %+v, want the branch and the worktree of the task", read)
	}
	if read.Profile != "opencode" || read.Session != "ses_7fKq2" {
		t.Errorf("the state read back = %+v, want the profile and the session of the run", read)
	}
	if len(read.Attempts) != 1 {
		t.Fatalf("the state read back holds %d attempts, want the one that was written", len(read.Attempts))
	}
	got := read.Attempts[0]
	if got.Number != 1 || got.Outcome != TimedOut || got.Continued {
		t.Errorf("the attempt read back = %+v, want the first one, timed out and not a continuation", got)
	}
	if !got.StartedAt.Equal(started) || !got.EndedAt.Equal(ended) {
		t.Errorf("the attempt read back ran from %s to %s, want %s to %s", got.StartedAt, got.EndedAt, started, ended)
	}
	if got, ok := read.Attempt(1); !ok || got.Journal != attempt.Attempts[0].Journal {
		t.Errorf("the journal of the attempt = %q, want %q", got.Journal, attempt.Attempts[0].Journal)
	}
	if _, ok := read.Attempt(2); ok {
		t.Error("the state holds an attempt that was never made")
	}
}

// TestTheStateLeavesOneFile: the bytes of a state are written through a file of its own and
// moved over, so that nothing of it stays behind for a person to trip over and a reader sees
// either the whole state or the one before it. This is the one place where the whole of a state
// reaches the disk, and it is written under the lock of the task — which is why this test writes
// twice through it and no caller of the package can (docs/DESIGN.md §7).
func TestTheStateLeavesOneFile(t *testing.T) {
	journals := newJournals(t.TempDir(), "naghuale-crewflow")
	path := journals.StatePath(43)
	first := State{Number: 43}

	neverRun, err := readPersisted(nil, State{})
	if err != nil {
		t.Fatalf("the record of a task that was never run: %v", err)
	}
	if _, err := saveState(nil, path, neverRun, first); err != nil {
		t.Fatalf("saveState returned an error: %v", err)
	}
	written, err := readPersisted(nil, first)
	if err != nil {
		t.Fatalf("read the record that was written: %v", err)
	}
	second := State{Number: 43, Branch: "crewflow/43-task"}
	if _, err := saveState(nil, path, written, second); err != nil {
		t.Fatalf("saveState of a second run returned an error: %v", err)
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Dir(path), err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if !slices.Equal(names, []string{"43.json"}) {
		t.Errorf("the folder of the state holds %v, want only the state of the task", names)
	}
}

// TestLoadStateOfATaskThatWasNeverRun: there is no state, and a caller has to be
// able to tell that from a state that could not be read.
func TestLoadStateOfATaskThatWasNeverRun(t *testing.T) {
	_, err := LoadState(filepath.Join(t.TempDir(), "state", "naghuale-crewflow", "43.json"))

	if !os.IsNotExist(err) {
		t.Errorf("LoadState = %v, want that the task was never run", err)
	}
}

// TestLoadStateOfABrokenFile: a state crewflow cannot read is a fact about the
// machine, and the path is in the error, because a person has to look at that file.
func TestLoadStateOfABrokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "43.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}

	_, err := LoadState(path)

	if err == nil {
		t.Fatal("LoadState of a broken file returned no error, want one")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not name the file a person has to look at", err)
	}
}

// TestStateKeepsTheProcessOfTheRun: a state of a task says "running" until something
// says otherwise, and the only thing that can say it is the process of the run itself
// — the change request is there for the same reason, so that a list of runs needs
// nothing but the state (docs/DESIGN.md §7).
func TestStateKeepsTheProcessOfTheRun(t *testing.T) {
	started := time.Date(2026, time.September, 28, 10, 0, 0, 0, time.UTC)
	process := proc.Process{Pid: 4242, StartedAt: started.Add(-time.Second)}
	journals := newJournals(t.TempDir(), "naghuale-crewflow")

	state := State{Number: 43, Title: "the run of a task"}.
		NextAttempt(1, StartOf{
			Started:      started,
			Journal:      journals.JournalPath(43, 1),
			ErrorJournal: journals.errorJournalPath(43, 1),
			Process:      process,
			Identity:     Identity{Mode: "bot", Description: "bot — GitHub App crewflow-executor (installation 12345)"},
		}).
		Ended(1, started.Add(42*time.Minute), ChangeRequestOpened)
	state.Change = &Change{Number: 44, URL: "https://github.com/naghuale/crewflow/pull/44"}
	path := journals.StatePath(43)
	keeps(t, path, state)

	read, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState returned an error: %v", err)
	}

	got, named := read.Attempts[0].Process()
	if !named {
		t.Fatal("the state read back names no process, want the one the run was in")
	}
	if got != process {
		t.Errorf("the process read back = %+v, want %+v", got, process)
	}
	if read.Change == nil || read.Change.URL != state.Change.URL || read.Change.Number != 44 {
		t.Errorf("the change request read back = %+v, want the one the run opened", read.Change)
	}
}

// TestStateOfARunThatKeptNoProcess: crewflow kept the number of the process of a run
// after this state was written, and a state of before that has to be read as it is —
// a list of runs may not ask the machine about a number it does not have.
func TestStateOfARunThatKeptNoProcess(t *testing.T) {
	older := `{
  "task": 43,
  "title": "the run of a task",
  "branch": "crewflow/43-the-run-of-a-task",
  "worktree": "/w/43",
  "profile": "opencode",
  "attempts": [
    {"number": 1, "started_at": "2026-09-28T10:00:00Z", "ended_at": "0001-01-01T00:00:00Z",
     "journal": "/w/43-1.jsonl", "error_journal": "/w/43-1.err", "outcome": "running",
     "continued": false}
  ]
}
`
	path := filepath.Join(t.TempDir(), "43.json")
	if err := os.WriteFile(path, []byte(older), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}

	state, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState of a state of before the process was kept: %v", err)
	}

	if len(state.Attempts) != 1 || state.Attempts[0].Outcome != Running {
		t.Fatalf("the state read back = %+v, want the one attempt that was going", state.Attempts)
	}
	if _, named := state.Attempts[0].Process(); named {
		t.Error("a state of before the process was kept names a process, want none")
	}
	if state.Change != nil {
		t.Errorf("a state of before the change request was kept holds %+v, want none", state.Change)
	}
}

// keeps is the state a test has made for a task, written the way a command writes one: as a
// change, under the lock of the task. A fixture of a test writes a whole state of a task on
// purpose — it stands for a machine whose state is what the test says — and it goes through
// `UpdateState` all the same, because that is the one road into a state and a second road is
// how a record of one command gets lost by another (D-044: одна дорога).
func keeps(t *testing.T, path string, state State) {
	t.Helper()
	if _, err := UpdateState(path, func(State) (State, error) { return state, nil }); err != nil {
		t.Fatalf("write the state of the task into %s: %v", path, err)
	}
}

// read is what a file holds, for a test that checks what a run left behind.
func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// whatWasWritten is what the executor wrote: the journal of a run without the lines
// of crewflow itself, which are whose name the run went under and what it was given
// the right to read, and which are crewflow's words and not the agent's
// (docs/DESIGN.md §7d, §7i).
func whatWasWritten(t *testing.T, path string) string {
	t.Helper()
	lines := strings.SplitAfter(read(t, path), "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "crewflow: ") {
			return strings.Join(withoutTheWorkOutsideTheCommits(lines[i:]), "")
		}
	}
	t.Fatalf("the journal %s holds no line of the executor in it, want the lines of crewflow and then what it wrote", path)
	return ""
}

// withoutTheWorkOutsideTheCommits is the journal without the one line crewflow writes about
// the work the run left outside the commits of its branch. It is a line of crewflow and not
// of the executor wherever it stands, and it stands after what the executor wrote because
// that is when the work is counted (D-088, docs.DESIGN.md §7a).
func withoutTheWorkOutsideTheCommits(lines []string) []string {
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(line, "crewflow: the run left ") {
			continue
		}
		kept = append(kept, line)
	}
	return kept
}

// TestAStateOfBeforeIsReadAsItIs: a state written before the format was versioned
// holds a table where an attempt of today holds the mode of the run, and it is read
// as what it is — every attempt of it, its outcome and its process — because a run of
// before is a run that happened and a list of runs has to show it (docs/DESIGN.md §7h).
func TestAStateOfBeforeIsReadAsItIs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "43.json")
	if err := os.WriteFile(path, []byte(read(t, filepath.Join("testdata", "state-of-before.json"))), 0o600); err != nil {
		t.Fatalf("write the state of before: %v", err)
	}

	state, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState of a state of before returned an error: %v", err)
	}

	if state.Schema != 0 {
		t.Errorf("a state without a schema is of the format %d, want 0", state.Schema)
	}
	if len(state.Attempts) != 1 {
		t.Fatalf("the state holds %d attempts, want the one it was written with", len(state.Attempts))
	}
	attempt := state.Attempts[0]
	if attempt.Identity.Mode != "bot" {
		t.Errorf("the mode of the attempt is %q, want %q", attempt.Identity.Mode, "bot")
	}
	if attempt.Outcome != ChangeRequestOpened || attempt.Number != 1 {
		t.Errorf("the attempt is %+v, want the first one that opened a change request", attempt)
	}
	process, named := attempt.Process()
	if !named || process.Pid != 4242 {
		t.Errorf("the process of the run is %+v, want the one the state was written with", process)
	}
	if state.Change == nil || state.Change.Number != 44 {
		t.Errorf("the change request of the state is %+v, want #44", state.Change)
	}

	// The next run that goes on with the task writes the file in the format of
	// today: the attempts before it are kept as they were, and the new one has the
	// mode, the agent and the session of its own.
	if _, err := UpdateState(path, func(current State) (State, error) {
		return current.NextAttempt(current.NextNumber(), StartOf{
			Started:      attempt.EndedAt.Add(time.Hour),
			Journal:      "/home/p/.crewflow/runs/naghuale-crewflow/43-2.jsonl",
			ErrorJournal: "/home/p/.crewflow/runs/naghuale-crewflow/43-2.err",
			Executor:     "opencode",
			Session:      "ses_7fKq2",
			Continued:    true,
			Identity:     Identity{Mode: "bot"},
		}).Ended(2, attempt.EndedAt.Add(time.Hour).Add(20*time.Minute), ChangeRequestOpened), nil
	}); err != nil {
		t.Fatalf("UpdateState returned an error: %v", err)
	}

	written := read(t, path)
	for _, in := range []string{`"schema": 1`, `"identity": "bot"`, `"executor": "opencode"`, `"session": "ses_7fKq2"`} {
		if !strings.Contains(written, in) {
			t.Errorf("the state written in the format of today does not hold %s:\n%s", in, written)
		}
	}
	again, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState of the state of today returned an error: %v", err)
	}
	if len(again.Attempts) != 2 {
		t.Fatalf("the state of today holds %d attempts, want both", len(again.Attempts))
	}
	if again.Attempts[0].Identity.Description != "" {
		t.Errorf("the attempt of before keeps the line a report shows: %q", again.Attempts[0].Identity.Description)
	}
	if again.Attempts[1].Session != "ses_7fKq2" {
		t.Errorf("the second attempt is in the session %q, want the one it went on in", again.Attempts[1].Session)
	}
}

// TestTheQueueAndTheNextRunKeepBothRecordsOfOneState: two commands write one state of a
// task at the same time, and this is the race of D-068 FINDING-4 on the machine of a
// test. The run of the task opened its change request and stopped; the queue of attention
// read the state of the task and went to the host to ask what came of that change; while
// it was asking, the review asked for changes and the run of the task went on — and the
// answer of the host came after that.
//
// Both records have to be in the state at the end: the attempt of the second run, which
// exists only in the file, and the memory of what the host said about the change of the
// first one, which is what keeps the queue from walking the network about a finished run
// every minute (F-061, F-105, §6a). A write of the whole state a command holds in its
// hands keeps one of them and loses the other.
func TestTheQueueAndTheNextRunKeepBothRecordsOfOneState(t *testing.T) {
	m := newMachine(t)
	m.answers["opencode"] = answer{stdout: theRun}
	origin := &host{task: taskOf(43), opened: true}
	cfg := projectOf(t, m.worktrees, "")

	if _, err := Run(t.Context(), m.env(), cfg, origin.set(), Request{Number: 43, RepoDir: m.repo}); err != nil {
		t.Fatalf("the first run returned an error: %v", err)
	}

	// The queue is asked about the change of the run that has ended, and the host of
	// the test answers only when the test has let the next run write its attempt.
	asked, answered := make(chan struct{}), make(chan struct{})
	queue := make(chan Queue, 1)
	go func() {
		read, err := CheckAttention(t.Context(), m.home, cfg.RepoName(), attentionEnvOf(m.at().Add(time.Hour)),
			&hostAnsweredLate{asked: asked, answered: answered,
				facts: HostFacts{Asked: true, Change: &ChangeFacts{Number: 44, State: "merged", Head: theHead}}}, nil)
		if err != nil {
			t.Errorf("CheckAttention returned an error: %v", err)
		}
		queue <- read
	}()
	<-asked

	if _, err := Run(t.Context(), m.env(), cfg, origin.set(),
		Request{Number: 43, RepoDir: m.repo, Continue: "the review asked for a test of the timeout"}); err != nil {
		t.Fatalf("the second run returned an error: %v", err)
	}
	close(answered)
	if read := <-queue; len(read.Entries) != 0 {
		t.Errorf("the queue holds %v, want nothing: a merged change takes the run out of it", tasksOfQueue(read))
	}

	state := stateOf(t, m, 43)
	if len(state.Attempts) != 2 {
		t.Errorf("the state holds %d attempts, want both: a write of the state the queue read takes the "+
			"attempt of the run that went on away", len(state.Attempts))
	}
	if state.Settled == nil || state.Settled.By != ReasonChangeMerged {
		t.Errorf("the state remembers %+v, want what the host said about the change of the first run", state.Settled)
	}
}

// TestAStateOfATaskUnderRacingWriters: many writers at once, each of them with its own
// attempt to leave in the state of one task — the sign of life of a run, the record of a
// standing one, the memory of what the host said. Every attempt is in the file at the
// end: the state is written under the lock of the task and onto the state as it is there
// now, and a writer that read the file before the others wrote does not take their
// attempts away (D-068 FINDING-4).
func TestAStateOfATaskUnderRacingWriters(t *testing.T) {
	const writers = 8
	journals := newJournals(t.TempDir(), "naghuale-crewflow")
	path := journals.StatePath(43)
	started := monday.Add(8 * time.Hour)
	keeps(t, path, State{Number: 43, Title: "the run of a task"})

	var group sync.WaitGroup
	written := make(chan error, writers)
	for at := range writers {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := UpdateState(path, func(state State) (State, error) {
				return state.NextAttempt(state.NextNumber(), StartOf{
					Started:      started.Add(time.Duration(at) * time.Minute),
					Step:         stepExecutor,
					Journal:      fmt.Sprintf("journal-of-the-writer-%d.jsonl", at),
					ErrorJournal: fmt.Sprintf("way-out-of-the-writer-%d.err", at),
					Executor:     "opencode",
					Identity:     Identity{Mode: "owner"},
				}), nil
			})
			written <- err
		}()
	}
	group.Wait()
	close(written)
	for err := range written {
		if err != nil {
			t.Fatalf("UpdateState returned an error: %v", err)
		}
	}

	kept, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState returned an error: %v", err)
	}
	if len(kept.Attempts) != writers {
		t.Fatalf("the state holds %d attempts, want the %d that were written at once", len(kept.Attempts), writers)
	}
	for _, attempt := range kept.Attempts {
		if attempt.Number < 1 || attempt.Number > writers {
			t.Errorf("the state holds the attempt %d, want every number from 1 to %d exactly once: "+
				"two writers took the same number of an attempt", attempt.Number, writers)
		}
	}

	// The lock of the task is a file of its own next to the state, and it is not the state of
	// a task: a queue that counted it as one would tell a person it is reading more tasks than
	// it is, and a list would show a run of a task that never was (docs/DESIGN.md §6a).
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read the folder of the state: %v", err)
	}
	for _, entry := range entries {
		if isStateOfATask(entry.Name()) != (entry.Name() == "43.json") {
			t.Errorf("the folder of the state holds %q, and a reader counts it as a task: %t",
				entry.Name(), isStateOfATask(entry.Name()))
		}
	}
	if count := countOfStates(entries); count != 1 {
		t.Errorf("the queue would say it is reading %d states, want the one task", count)
	}
}

// hostAnsweredLate is the host of a project in a test that is asked about a task and
// answers only when the test lets it: what a test of two writers of one state needs is
// for the answer of the host to come after the run of the task has written what it had to
// write, because that is the order in which the two of them write the same file.
type hostAnsweredLate struct {
	// asked is closed when the host is asked the first time, and answered is released by
	// the test to let the answer through.
	asked    chan struct{}
	answered chan struct{}
	// facts is what the host says about every task it is asked about.
	facts HostFacts
}

// FactsOf is what the host of the test says about a task, once the test has let it: the
// queue asks the host in a goroutine of its own, and both the closing of `asked` and the
// closing of `answered` may be reached by more than one of those goroutines.
func (h *hostAnsweredLate) FactsOf(ctx context.Context, _, _ int) (HostFacts, error) {
	select {
	case <-h.asked:
	default:
		close(h.asked)
	}
	select {
	case <-h.answered:
	case <-ctx.Done():
		return HostFacts{}, ctx.Err()
	}
	return h.facts, nil
}

// TestAStateOfANewerCrewflowIsRefused: a file of a format nobody looked at is a file
// crewflow cannot say anything true about, and a list of runs that read it as
// something it is not would show a person a run that never was (docs/DESIGN.md §7h).
func TestAStateOfANewerCrewflowIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "43.json")
	written := strings.Replace(read(t, filepath.Join("testdata", "state-of-before.json")),
		`"task": 43`, `"schema": 99,`+"\n"+`  "task": 43`, 1)
	if err := os.WriteFile(path, []byte(written), 0o600); err != nil {
		t.Fatalf("write the state: %v", err)
	}

	if _, err := LoadState(path); err == nil {
		t.Fatal("LoadState read a state of a format crewflow does not know")
	} else if !strings.Contains(err.Error(), "99") {
		t.Errorf("the error %q does not say which format the file is of", err)
	}
}

// TestTheReasonOfAnAttemptIsPublishedWhenTheStateOfTheTaskIsWritten: the state of a task is
// a file kept for ever, read by programs and pasted into issues, and the reason of an attempt
// in it is a sentence of a run — what an executor said about itself, what a provider said
// about its refusal. It is published through the boundary of the run when the state is
// written, and a state a caller writes without a boundary keeps the words of crewflow, which
// are its own (D-068 RECHECK-FINDING-5, docs/DESIGN.md §7e).
func TestTheReasonOfAnAttemptIsPublishedWhenTheStateOfTheTaskIsWritten(t *testing.T) {
	const canary = "ghs_16C7e42F292c6912E7710c838347Ae178B4a"
	journals := newJournals(t.TempDir(), "naghuale-crewflow")
	withReason := func(current State) (State, error) {
		started := current
		started.Number, started.Title, started.Branch = 43, "the run of a task", "crewflow/43-task"
		return started.NextAttempt(started.NextNumber(), StartOf{
			Started: time.Date(2026, time.October, 3, 9, 0, 0, 0, time.UTC), Step: "the executor of the run",
			Journal: journals.JournalPath(43, 1),
		}).Reason(1, "the run stopped itself: it could not reach "+canary), nil
	}

	if _, err := UpdateStateThrough(secret.NewOut(secret.Generated(canary)...), journals.StatePath(43), withReason); err != nil {
		t.Fatalf("write the state of the task: %v", err)
	}
	written, err := LoadState(journals.StatePath(43))
	if err != nil {
		t.Fatalf("read the state of the task: %v", err)
	}
	reason := written.Attempts[0].Reason
	if strings.Contains(reason, canary) {
		t.Errorf("the reason in the state of the task is %q, want the value of the run taken out of it", reason)
	}
	if !strings.Contains(reason, secret.Redacted) {
		t.Errorf("the reason in the state of the task is %q, want the words of the run left in it", reason)
	}
	if !strings.Contains(reason, "it could not reach ") {
		t.Errorf("the reason in the state of the task is %q, want the words of the run left in it", reason)
	}
	// The words of crewflow in a state are its own: a caller that has no values of a run to
	// publish writes what it has, and a value of a person is not taken out of the mode of an
	// identity or out of the answer of a person (docs.DESIGN.md §7e, §7i).
	if _, err := UpdateState(journals.StatePath(43), withReason); err != nil {
		t.Fatalf("write the state of the task again: %v", err)
	}
	written, err = LoadState(journals.StatePath(43))
	if err != nil {
		t.Fatalf("read the state of the task again: %v", err)
	}
	if want := "the run stopped itself: it could not reach " + canary; written.Attempts[0].Reason != want {
		t.Errorf("the reason in the state of a caller with no boundary is %q, want %q", written.Attempts[0].Reason, want)
	}
}

// TestTheCodeOfTheReasonOfAnAttemptSurvivesTheStateOfTheTask: a reason of §6a is a word out of
// the closed list of the codes and then the words of what happened, and the state of a task is
// a file kept for ever that programs read a reason out of. The code was published through the
// boundary like the words, and a password that happened to be a part of it left a reason of
// nothing in the file — the queue names a reason by the word before the colon, and
// `network-[redacted]-unavailable` is not a reason of the format, so a run that waited for a
// resource read as a run that stopped by itself (R5-NEW-8, docs/DESIGN.md §6a, §7e).
func TestTheCodeOfTheReasonOfAnAttemptSurvivesTheStateOfTheTask(t *testing.T) {
	const password = "route"
	reason := ReasonRouteUnavailable + ": the route of the project is not reachable"
	journals := newJournals(t.TempDir(), "naghuale-crewflow")
	withReason := func(current State) (State, error) {
		started := current
		started.Number, started.Title, started.Branch = 43, "the run of a task", "crewflow/43-task"
		return started.NextAttempt(started.NextNumber(), StartOf{
			Started: time.Date(2026, time.October, 3, 9, 0, 0, 0, time.UTC), Step: "the executor of the run",
			Journal: journals.JournalPath(43, 1),
		}).Reason(1, reason), nil
	}

	if _, err := UpdateStateThrough(secret.NewOut(secret.Chosen(password)...), journals.StatePath(43), withReason); err != nil {
		t.Fatalf("write the state of the task: %v", err)
	}
	written, err := LoadState(journals.StatePath(43))
	if err != nil {
		t.Fatalf("read the state of the task: %v", err)
	}

	want := ReasonRouteUnavailable + ": the " + secret.Redacted + " of the project is not reachable"
	if got := written.Attempts[0].Reason; got != want {
		t.Errorf("the reason in the state of the task is %q, want %q: a code of §6a is not a value of a run", got, want)
	}
	if got := refusalOf(written.Attempts[0].Reason); got != ReasonRouteUnavailable {
		t.Errorf("the queue names the run %q by the reason %q, want %q",
			got, written.Attempts[0].Reason, ReasonRouteUnavailable)
	}
}

// TestTheCanaryOfAReasonOfItsOwnIsNotWrittenIntoTheStateOfATask: the
// reason of an attempt is a word of the canonical list of the codes and
// then the words of what happened, and a word that is not in the list is
// a sentence of a run however it is written: one word without a space is
// not a code of the format by its shape, and a canary in front of a
// colon is not a code behind it. The state of a task is a file kept for
// ever that programs read a reason out of, and a canary that stood in
// the reason of it as a code — a word, no space, no colon — would stand
// there for ever, and a canary in front of a colon would keep its head
// (R5-NEW-10, docs/DESIGN.md §6a, §7e, §7h).
func TestTheCanaryOfAReasonOfItsOwnIsNotWrittenIntoTheStateOfATask(t *testing.T) {
	const canary = "canaryR5X9"
	journals := newJournals(t.TempDir(), "naghuale-crewflow")
	for _, reason := range []string{
		canary,
		canary + ": diagnostic",
	} {
		withReason := func(current State) (State, error) {
			started := current
			started.Number, started.Title, started.Branch = 43, "the run of a task", "crewflow/43-task"
			return started.NextAttempt(started.NextNumber(), StartOf{
				Started: time.Date(2026, time.October, 3, 9, 0, 0, 0, time.UTC), Step: "the executor of the run",
				Journal: journals.JournalPath(43, 1),
			}).Reason(1, reason), nil
		}

		if _, err := UpdateStateThrough(secret.NewOut(secret.Chosen(canary)...),
			journals.StatePath(43), withReason); err != nil {
			t.Fatalf("write the state of the task: %v", err)
		}
		written, err := LoadState(journals.StatePath(43))
		if err != nil {
			t.Fatalf("read the state of the task: %v", err)
		}
		got := written.Attempts[0].Reason
		if strings.Contains(got, canary) {
			t.Errorf("the reason in the state of the task is %q, want the canary taken out of it", got)
		}
		want := secret.Redacted
		if reason != canary {
			want = secret.Redacted + ": diagnostic"
		}
		if got != want {
			t.Errorf("the reason in the state of the task is %q, want %q: a word that is not a code of the format is a sentence of a run",
				got, want)
		}
	}
}

// TestTheWordsOfTheFormatSurviveTheReportOfARun: a program reads the report of a run to decide
// what it did, and it decides by the words out of the closed lists of the format — the outcome
// of the run, the profile of the executor, the code of a reason. Cut out of them, they are not
// the words of the format any more: a password that happened to be a part of `no-change-request`
// made the report say of the run something else than the run was (R5-NEW-8, R5-NEW-7,
// docs/DESIGN.md §6a, §7e).
func TestTheWordsOfTheFormatSurviveTheReportOfARun(t *testing.T) {
	for _, tc := range []struct {
		name     string
		password string
		result   Result
		want     Result
	}{
		{
			name:     "the outcome of the run",
			password: "change",
			result:   Result{Task: 43, Outcome: NoChangeRequest, Profile: "opencode", Reason: "the run pushed and stopped"},
			want: Result{Task: 43, Outcome: NoChangeRequest, Profile: "opencode",
				Reason: "the run pushed and stopped"},
		},
		{
			name:     "the code of the reason and the words behind it",
			password: "route",
			result: Result{Task: 43, Outcome: Blocked, Profile: "opencode",
				Reason: ReasonRouteUnavailable + ": the route of the project is not reachable"},
			want: Result{Task: 43, Outcome: Blocked, Profile: "opencode",
				Reason: ReasonRouteUnavailable + ": the " + secret.Redacted + " of the project is not reachable"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := secret.NewOut(secret.Chosen(tc.password)...)

			document, fallen := theReport(t, out, secret.DocumentTaskRun, tc.result)
			if fallen != nil {
				t.Fatalf("the report of the run fell over: %v", fallen)
			}
			var read Result
			if err := json.Unmarshal(document, &read); err != nil {
				t.Fatalf("the report of the run is not a document: %v\n%s", err, document)
			}
			if read.Task != tc.want.Task || read.Outcome != tc.want.Outcome ||
				read.Profile != tc.want.Profile || read.Reason != tc.want.Reason {
				t.Errorf("the report of the run is %+v, want %+v", read, tc.want)
			}
		})
	}
}

// theReport is the answer of a command as the boundary publishes it, with a fall of the
// boundary given back to the test instead of taken the program that prints it with it: an
// answer crewflow cannot write is a refusal of a command and not the end of a command
// (R5-NEW-7, docs/DESIGN.md §7e).
func theReport(t *testing.T, out *secret.Out, doc secret.Document, answer any) (document []byte, fallen any) {
	t.Helper()
	defer func() { fallen = recover() }()
	document, err := out.Report(doc, answer)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	return document, nil
}

// TestTheReasonOfAnAttemptKeepsItsMarkerWhenTheStateIsWrittenAgain: every write of the state of
// a task publishes the reason of every attempt in it again — the reason in the file is the
// reason of the last write, already cleaned, and the next write cleans it a second time. A
// password of one sign a person really chose is inside the marker of a cut, and the second pass
// cut the marker: a state file kept for ever grew `[[redacted]edacted]` in the reason of a run,
// which is neither what the boundary said happened nor something a person can read
// (R224-9, docs.DESIGN.md §7e).
func TestTheReasonOfAnAttemptKeepsItsMarkerWhenTheStateIsWrittenAgain(t *testing.T) {
	const password = "d"
	journals := newJournals(t.TempDir(), "naghuale-crewflow")
	begun := func(current State) (State, error) {
		started := current
		started.Number, started.Title, started.Branch = 43, "the run of a task", "crewflow/43-task"
		return started.NextAttempt(started.NextNumber(), StartOf{
			Started: time.Date(2026, time.October, 3, 9, 0, 0, 0, time.UTC), Step: "the executor of the run",
			Journal: journals.JournalPath(43, 1),
		}).Reason(1, "the run stopped itself: it could not reach "+password), nil
	}
	touched := func(current State) (State, error) {
		current.Title = "the run of a task, once more"
		return current, nil
	}
	out := secret.NewOut(secret.Chosen(password)...)

	if _, err := UpdateStateThrough(out, journals.StatePath(43), begun); err != nil {
		t.Fatalf("write the state of the task: %v", err)
	}
	// The password is one sign long, so it is a part of every third word of the reason, and
	// each of those places is cut as well — what must not happen is a cut inside a cut: the
	// reason of the second write is the reason of the first one, whole.
	want := "the run stoppe" + secret.Redacted + " itself: it coul" + secret.Redacted + " not reach " + secret.Redacted
	for write := range 3 {
		if _, err := UpdateStateThrough(out, journals.StatePath(43), touched); err != nil {
			t.Fatalf("write %d of the state of the task again: %v", write, err)
		}
		written, err := LoadState(journals.StatePath(43))
		if err != nil {
			t.Fatalf("read the state of the task after write %d: %v", write, err)
		}
		if reason := written.Attempts[0].Reason; reason != want {
			t.Errorf("the reason in the state of the task after write %d is %q, want %q", write, reason, want)
		}
	}
}

// aStateWithAFieldNobodyClassified is the state of a task with a field the policy of the kind does
// not name: what the record of a task looks like when a field of the format arrives before the
// policy of it does.
type aStateWithAFieldNobodyClassified struct {
	State
	FieldOfTomorrow string `json:"field_of_tomorrow"`
}

// TestAFieldNobodyClassifiedKeepsTheStateThatWasWritten: the state of a task is the record of
// what crewflow did, and a field of it that the policy of the kind does not name is not cleaned
// as free text and written all the same — the new record is not written, the record on the disk
// stays the last one that was published byte for byte, and the refusal names the field and no
// value of a run (D-089, D-082, docs/DESIGN.md §7h).
func TestAFieldNobodyClassifiedKeepsTheStateThatWasWritten(t *testing.T) {
	folder := t.TempDir()
	journals := newJournals(folder, "naghuale-crewflow")
	state := State{Number: 43, Title: "the run of a task", Branch: "crewflow/43-task", Schema: Schema}
	state = state.NextAttempt(state.NextNumber(), StartOf{
		Started: time.Date(2026, time.October, 3, 9, 0, 0, 0, time.UTC), Step: "the executor of the run",
		Journal: journals.JournalPath(43, 1),
	})
	neverWritten, err := readPersisted(secret.NewOut(), State{})
	if err != nil {
		t.Fatalf("the record of a task that was never run: %v", err)
	}
	if _, err := saveState(secret.NewOut(), journals.StatePath(43), neverWritten, state); err != nil {
		t.Fatalf("write the state of the task: %v", err)
	}
	published, err := os.ReadFile(journals.StatePath(43))
	if err != nil {
		t.Fatalf("read the state of the task: %v", err)
	}
	written, err := LoadState(journals.StatePath(43))
	if err != nil {
		t.Fatalf("read the state of the task as a state: %v", err)
	}

	err = publishState(secret.NewOut(), journals.StatePath(43), aStateWithAFieldNobodyClassified{
		State:           written,
		FieldOfTomorrow: "the run asked for " + secret.Redacted + " and left",
	})

	if err == nil {
		t.Fatalf("the state of a task with a field nobody classified was written: %s", published)
	}
	if !errors.Is(err, secret.ErrRedactionPathUnknown) {
		t.Fatalf("the refusal of the state of a task is %v, want %v", err, secret.ErrRedactionPathUnknown)
	}
	if !strings.Contains(err.Error(), "field_of_tomorrow") {
		t.Errorf("the refusal of the state of a task is %q, want it to name the field nobody classified", err)
	}
	kept, err := os.ReadFile(journals.StatePath(43))
	if err != nil {
		t.Fatalf("read the state of the task after the refusal: %v", err)
	}
	if string(kept) != string(published) {
		t.Errorf("the state of the task is\n%s\nwant the record that was published before it\n%s",
			kept, published)
	}
}

// TestTheRevisionOfAStateOfATaskIsTheWordOfTheRoad: the revision of the record of a task is
// what the road that writes it says, taken from the record that is on the disk under the lock
// of the task. A caller names a change and not a version of the record: whatever revision the
// change came with, the record on the disk takes the one that was there plus one — so a change
// cannot move the record back and cannot skip a version of it — and a change that claims a
// revision above the record is refused, because a subscriber reads the revision as a change of
// the task (docs/DESIGN.md §7h, #243).
func TestTheRevisionOfAStateOfATaskIsTheWordOfTheRoad(t *testing.T) {
	journals := newJournals(t.TempDir(), "naghuale-crewflow")
	path := journals.StatePath(43)

	first, err := UpdateState(path, func(State) (State, error) {
		return State{Number: 43, Title: "the run of a task"}, nil
	})
	if err != nil {
		t.Fatalf("write the state of a task that was not there: %v", err)
	}
	if first.Revision != 1 {
		t.Errorf("the record of the task that was written first stands at the revision %d, want 1: the first "+
			"change of a record that was not there is the first version of it", first.Revision)
	}

	// A change that names a revision above the record claims progress that never happened.
	written := read(t, path)
	_, err = UpdateState(path, func(current State) (State, error) {
		changed := current.Noticed(monday, "43:stands:no-progress")
		changed.Revision = current.Revision + 1
		return changed, nil
	})
	if !errors.Is(err, ErrRevisionForged) {
		t.Fatalf("the change that named the revision %d = %v, want %v", first.Revision+1, err, ErrRevisionForged)
	}
	for _, want := range []string{"2", "1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal is %q, want it to name the revision of the change and the one on the disk", err)
		}
	}
	if kept := read(t, path); kept != written {
		t.Errorf("the record of the task after the refusal is\n%s\nwant the one that was there\n%s", kept, written)
	}

	// A change that names a revision of its own below the record cannot lower it: the road
	// answers with the version of the record that follows the one on the disk.
	second, err := UpdateState(path, func(current State) (State, error) {
		changed := current.Noticed(monday.Add(time.Hour), "43:stands:no-progress")
		changed.Revision = 0
		return changed, nil
	})
	if err != nil {
		t.Fatalf("the change that named no revision: %v", err)
	}
	if second.Revision != first.Revision+1 {
		t.Errorf("the record of the task stands at the revision %d, want %d: the revision is the road's word "+
			"and not the caller's", second.Revision, first.Revision+1)
	}
	kept := stateOfTheTask(t, path)
	if kept.Revision != second.Revision {
		t.Errorf("the record on the disk stands at the revision %d, want %d", kept.Revision, second.Revision)
	}
}

// TestAWriteThatChangesNothingKeepsTheRevisionOfAStateOfATask: the revision counts changes and
// not writes. The queue of attention of a schedule writes the state of a task again and again
// while a run of it goes on, and a revision that grew on every write would count the writes of a
// schedule and not the changes of a task — a subscriber would wake on a record that says the
// same thing it read the minute before (docs/DESIGN.md §7h, #243).
func TestAWriteThatChangesNothingKeepsTheRevisionOfAStateOfATask(t *testing.T) {
	journals := newJournals(t.TempDir(), "naghuale-crewflow")
	path := journals.StatePath(43)
	started := monday.Add(8 * time.Hour)
	first, err := UpdateState(path, func(State) (State, error) {
		return State{Number: 43, Title: "the run of a task"}.NextAttempt(1, StartOf{
			Started: started, Step: stepExecutor, Journal: journals.JournalPath(43, 1),
		}), nil
	})
	if err != nil {
		t.Fatalf("write the state of the task: %v", err)
	}
	kept := read(t, path)

	for _, same := range []struct {
		name   string
		change func(State) (State, error)
	}{
		{name: "the whole state again", change: func(current State) (State, error) { return current, nil }},
		{name: "the same sign of life", change: func(current State) (State, error) {
			return current.Alive(1, started, stepExecutor, ""), nil
		}},
		{name: "the same attempt taken out of a state without it", change: func(current State) (State, error) {
			return current.withoutAttempt(1, started.Add(time.Hour)), nil
		}},
	} {
		t.Run(same.name, func(t *testing.T) {
			written, err := UpdateState(path, same.change)
			if err != nil {
				t.Fatalf("write the state of the task again: %v", err)
			}
			if written.Revision != first.Revision {
				t.Errorf("the record of the task stands at the revision %d after %q, want %d: a write that "+
					"changed nothing is not a change", written.Revision, same.name, first.Revision)
			}
		})
	}
	if read(t, path) != kept {
		t.Errorf("the record of the task after the writes that changed nothing is\n%s\nwant\n%s",
			read(t, path), kept)
	}
}

// TestEveryChangeOfARecordOfATaskTakesTheNextRevision: one number for everything the record
// says about the task — the attempts of it, the sign of life of a run, the point a run goes on
// from, and the memory the queue of attention keeps about what the host said. They are all
// changes of what the record says, and a subscriber that watches the revision sees every one of
// them; the one thing that does not move it is the backlog of the events that are not told yet,
// which is not a change of the task but the queue of what has not been said about it
// (docs/DESIGN.md §7h, #243).
func TestEveryChangeOfARecordOfATaskTakesTheNextRevision(t *testing.T) {
	journals := newJournals(t.TempDir(), "naghuale-crewflow")
	path := journals.StatePath(43)
	started := monday.Add(8 * time.Hour)
	at := started.Add(time.Minute)
	keeps(t, path, State{Number: 43, Title: "the run of a task", Branch: "crewflow/43-the-run-of-a-task"})
	keeps(t, path, stateOfTheTask(t, path).NextAttempt(1, StartOf{
		Started: started, Step: stepExecutor, Journal: journals.JournalPath(43, 1),
	}))

	for _, change := range []struct {
		name   string
		change func(State) (State, error)
	}{
		{name: "the sign of life of a run", change: func(state State) (State, error) {
			return state.Alive(1, at, stepExecutor, ""), nil
		}},
		{name: "what the host said about the change of the run", change: func(state State) (State, error) {
			return state.SettledBy(at, ReasonChangeMerged), nil
		}},
		{name: "the record the queue left under the task", change: func(state State) (State, error) {
			return state.Noticed(at, "43:stands:no-progress"), nil
		}},
	} {
		t.Run(change.name, func(t *testing.T) {
			before := stateOfTheTask(t, path).Revision
			written, err := UpdateState(path, change.change)
			if err != nil {
				t.Fatalf("write the state of the task: %v", err)
			}
			if written.Revision != before+1 {
				t.Errorf("the record of the task stands at the revision %d, want %d: %q is what the record "+
					"says about the task", written.Revision, before+1, change.name)
			}
		})
	}
}

// TestAStateOfBeforeTheRevisionWasKeptIsTheRevisionZero: a record written before crewflow kept
// a revision is read as the revision zero, and the first change of it takes revision one — not
// the revision of having been written in the format of today. A subscriber that has never seen
// the task reads it as the first version of it, and a task whose record never changes stays at
// the revision zero (docs/DESIGN.md §7h, #243).
func TestAStateOfBeforeTheRevisionWasKeptIsTheRevisionZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "43.json")
	before := strings.Replace(read(t, filepath.Join("testdata", "state-of-before.json")),
		`"schema": 1,`, `"revision": 0,`+"\n"+`  "schema": 1,`, 1)
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatalf("write the state of before: %v", err)
	}

	state, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState of a state of before returned an error: %v", err)
	}
	if state.Revision != 0 {
		t.Errorf("a record written before the revision was kept stands at %d, want the revision zero", state.Revision)
	}

	written, err := UpdateState(path, func(current State) (State, error) {
		return current.Noticed(monday, "43:stands:no-progress"), nil
	})
	if err != nil {
		t.Fatalf("the first change of a record of before: %v", err)
	}
	if written.Revision != 1 {
		t.Errorf("the first change of a record of before stands at the revision %d, want 1", written.Revision)
	}
	if got := strings.Contains(read(t, path), `"revision": 1`); !got {
		t.Errorf("the record on the disk does not hold its revision:\n%s", read(t, path))
	}
}

// TestTheRevisionOfAStateOfATaskDoesNotPassTheHighestOne: the revision only ever grows, and a
// record that stands at the highest revision the format has cannot be written over — a number
// that went over the top of it is a revision nobody can read as the version of a record that
// follows another one. The refusal comes before the write, so the record on the disk is the one
// that was there (docs/DESIGN.md §7h, #243).
func TestTheRevisionOfAStateOfATaskDoesNotPassTheHighestOne(t *testing.T) {
	journals := newJournals(t.TempDir(), "naghuale-crewflow")
	path := journals.StatePath(43)
	// The record stands at the highest revision on a machine of its own: no writer may write
	// such a record, because the road refuses a revision above the one that is there — so the
	// record is the one thing a test has to make with its own hands.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("make the folder of the state: %v", err)
	}
	full := fmt.Sprintf("{\n  %q: 1,\n  %q: %d,\n  %q: 43,\n  %q: \"the run of a task\",\n  %q: []\n}\n",
		"schema", "revision", int64(math.MaxInt64), "task", "title", "attempts")
	if err := os.WriteFile(path, []byte(full), 0o600); err != nil {
		t.Fatalf("write the record at the highest revision: %v", err)
	}
	written := read(t, path)
	if state := stateOfTheTask(t, path); state.Revision != math.MaxInt64 {
		t.Fatalf("the record of the task stands at the revision %d, want the highest one", state.Revision)
	}

	_, err := UpdateState(path, func(state State) (State, error) {
		return state.Noticed(monday, "43:stands:no-progress"), nil
	})

	if !errors.Is(err, ErrRevisionOverflow) {
		t.Fatalf("the change of a record at the highest revision = %v, want %v", err, ErrRevisionOverflow)
	}
	if kept := read(t, path); kept != written {
		t.Errorf("the record of the task after the refusal is\n%s\nwant the one that was there\n%s", kept, written)
	}
}

// TestAWriteOfAStateOfATaskThatFailedLeavesTheRevisionOnTheDisk: a revision that a write could
// not write is not the revision of the record. A change that refuses itself, a record the policy
// of the kind refuses to publish, and a file the machine will not let crewflow write over are
// three ways of not writing, and the record on the disk stands where it stood in all of them:
// what a subscriber reads is the revision of what is really there (docs/DESIGN.md §7h, #243).
func TestAWriteOfAStateOfATaskThatFailedLeavesTheRevisionOnTheDisk(t *testing.T) {
	journals := newJournals(t.TempDir(), "naghuale-crewflow")
	path := journals.StatePath(43)
	keeps(t, path, State{Number: 43, Title: "the run of a task"})
	written := read(t, path)
	refused := errors.New("the host did not answer")

	for _, failure := range []struct {
		name   string
		change func(State) (State, error)
		want   error
	}{
		{name: "a change that refused itself", change: func(State) (State, error) {
			return State{}, refused
		}, want: refused},
		{name: "a record the policy of the kind does not admit", change: func(state State) (State, error) {
			return state.NextAttempt(state.NextNumber(), StartOf{
				Started: monday, Step: stepExecutor, Journal: journals.JournalPath(43, 1),
				Identity: Identity{Mode: "root"},
			}), nil
		}, want: secret.ErrProtectedFieldInvalid},
	} {
		t.Run(failure.name, func(t *testing.T) {
			_, err := UpdateState(path, failure.change)
			if !errors.Is(err, failure.want) {
				t.Fatalf("the write of the state of the task = %v, want %v", err, failure.want)
			}
			if kept := read(t, path); kept != written {
				t.Errorf("the record of the task after the refusal is\n%s\nwant the one that was there\n%s",
					kept, written)
			}
		})
	}
	if state := stateOfTheTask(t, path); state.Revision != 1 {
		t.Errorf("the record of the task stands at the revision %d after the refusals, want 1", state.Revision)
	}
}

// TestARenameThatFailedLeavesTheRecordThatWasThere: the bytes of a record of a task are
// written through a file of its own and moved over the old one, so a reader sees the last whole
// record or the one before it. A rename that the machine refused leaves the record that was
// there whole and takes the file of its own with it — a temporary file left behind in the folder
// of the states is a thing a person trips over and a reader counts as a task (D-068 FINDING-4,
// docs/DESIGN.md §7h).
func TestARenameThatFailedLeavesTheRecordThatWasThere(t *testing.T) {
	journals := newJournals(t.TempDir(), "naghuale-crewflow")
	path := journals.StatePath(43)
	keeps(t, path, State{Number: 43, Title: "the run of a task"})
	written := read(t, path)
	// The state of the task of another task is a folder: a rename cannot put a file in place
	// of it, which is what a refused move over the record of a task looks like.
	taken := filepath.Join(journals.stateFolder(), "44.json")
	if err := os.Mkdir(taken, 0o700); err != nil {
		t.Fatalf("make the folder in place of a state: %v", err)
	}

	err := writeFileAtomic(taken, []byte("{\n  \"task\": 44\n}\n"))

	if err == nil {
		t.Fatal("writeFileAtomic moved a file over a folder, want the machine to refuse the move")
	}
	if kept := read(t, path); kept != written {
		t.Errorf("the record of the task of 43 after the refused move is\n%s\nwant\n%s", kept, written)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read the folder of the state: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "44.json.") {
			t.Errorf("the folder of the states holds %q, want no file of a write that was refused", entry.Name())
		}
	}
}

// TestTheRecordOfATaskCarriesItsRevisionInIt: the revision is a word of the record itself, and
// not a thing a program of crewflow has to be asked for: a person reading the file of a task and
// a program that has only the file both see which version of the record they hold, and a record
// of a task that was written before the revision was kept reads as the version zero (docs/DESIGN.md §7h, #243).
func TestTheRecordOfATaskCarriesItsRevisionInIt(t *testing.T) {
	journals := newJournals(t.TempDir(), "naghuale-crewflow")
	path := journals.StatePath(43)

	if _, err := UpdateState(path, func(State) (State, error) {
		return State{Number: 43, Title: "the run of a task"}, nil
	}); err != nil {
		t.Fatalf("write the state of the task: %v", err)
	}
	first := read(t, path)
	if !strings.Contains(first, `"revision": 1`) {
		t.Errorf("the record of the task does not hold its revision:\n%s", first)
	}

	if _, err := UpdateState(path, func(state State) (State, error) {
		return state.Noticed(monday, "43:stands:no-progress"), nil
	}); err != nil {
		t.Fatalf("change the state of the task: %v", err)
	}
	if second := read(t, path); !strings.Contains(second, `"revision": 2`) {
		t.Errorf("the record of the task after a change does not hold the next revision:\n%s", second)
	}
}
