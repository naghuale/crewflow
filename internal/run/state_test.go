package run

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/proc"
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

	files, err := journals.Begin(43, 1)
	if err != nil {
		t.Fatalf("Begin returned an error: %v", err)
	}
	if _, err := files.Out.WriteString("I did the work.\n"); err != nil {
		t.Fatalf("write the journal: %v", err)
	}
	if _, err := files.ErrOut.WriteString("a refusal\n"); err != nil {
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

// TestBeginEmptiesTheFilesOfAnAttempt: the file of an attempt is written by one
// run, and what the run of another attempt wrote is in the file of that attempt.
func TestBeginEmptiesTheFilesOfAnAttempt(t *testing.T) {
	journals := newJournals(t.TempDir(), "naghuale-crewflow")
	first, err := journals.Begin(43, 1)
	if err != nil {
		t.Fatalf("Begin returned an error: %v", err)
	}
	if _, err := first.Out.WriteString("the first attempt\n"); err != nil {
		t.Fatalf("write the journal: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close returned an error: %v", err)
	}

	again, err := journals.Begin(43, 1)
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
	attempt := state.NextAttempt(started, journals.JournalPath(43, 1), journals.errorJournalPath(43, 1), false,
		proc.Process{Pid: 4242, StartedAt: started}, Identity{Mode: "bot", Description: "bot — GitHub App crewflow-executor (installation 12345)"})
	attempt = attempt.Ended(ended, TimedOut)
	path := journals.StatePath(43)
	if err := SaveState(path, attempt); err != nil {
		t.Fatalf("SaveState returned an error: %v", err)
	}

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

// TestSaveStateLeavesOneFile: the state is written through a file of its own and
// moved over, so that nothing of it stays behind for a person to trip over.
func TestSaveStateLeavesOneFile(t *testing.T) {
	journals := newJournals(t.TempDir(), "naghuale-crewflow")
	path := journals.StatePath(43)

	if err := SaveState(path, State{Number: 43}); err != nil {
		t.Fatalf("SaveState returned an error: %v", err)
	}
	if err := SaveState(path, State{Number: 43, Branch: "crewflow/43-task"}); err != nil {
		t.Fatalf("SaveState of a second run returned an error: %v", err)
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
		NextAttempt(started, journals.JournalPath(43, 1), journals.errorJournalPath(43, 1), false, process,
			Identity{Mode: "bot", Description: "bot — GitHub App crewflow-executor (installation 12345)"}).
		Ended(started.Add(42*time.Minute), ChangeRequestOpened)
	state.Change = &Change{Number: 44, URL: "https://github.com/naghuale/crewflow/pull/44"}
	path := journals.StatePath(43)
	if err := SaveState(path, state); err != nil {
		t.Fatalf("SaveState returned an error: %v", err)
	}

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
			return strings.Join(lines[i:], "")
		}
	}
	t.Fatalf("the journal %s holds no line of the executor in it, want the lines of crewflow and then what it wrote", path)
	return ""
}
