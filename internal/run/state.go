package run

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/naghuale/crewflow/internal/proc"
)

// State is what crewflow keeps of a task between its runs, so that after an
// interruption it is visible where the run stopped: the branch, the worktree, the
// profile of the executor, the session to go on in, and every attempt in its own
// right (docs/DESIGN.md §7). The truth about the work is still on the host; this
// is what crewflow needs to not start from the beginning next time.
//
// What is never kept here is whether a change was approved or whether it may be
// merged: those are worked out from the host and from git every time they are asked
// for, so that a state file cannot say what the host says otherwise (docs/DESIGN.md §7h).
type State struct {
	// Schema is the version of the format of the file. A state written before
	// crewflow kept one is read as the version before the first, with every attempt
	// of it read as it is, and it is written again in the current format the next
	// time a run of the task touches it.
	Schema int `json:"schema,omitempty"`
	// Number is the task the state is of.
	Number int `json:"task"`
	// Title is the one line a person wrote, so that a state file says what it is
	// of without the host being read.
	Title string `json:"title"`
	// Branch and Worktree are where the work of the task is.
	Branch   string `json:"branch"`
	Worktree string `json:"worktree"`
	// Profile is what crewflow knew about the executor, which is what a later run
	// goes on with.
	Profile string `json:"profile"`
	// Session is the id of the session the executor went on, and the way a run of
	// the same task is continued in the same one.
	Session string `json:"session,omitempty"`
	// Change is the change request a run of the task opened, so that a list of the
	// runs of a project points at the work without asking the host anything
	// (docs/DESIGN.md §7).
	Change *Change `json:"change,omitempty"`
	// MergedSHA is the commit that was fast-forwarded into the default branch of the
	// project, and VerifiedAt when the merge was checked after it. Both are facts of
	// the process and not a verdict: whether the change may be merged is worked out
	// from the host and from git every time it is asked for, and what the state says
	// here is what has already happened (docs/DESIGN.md §7h).
	MergedSHA  string     `json:"merged_sha,omitempty"`
	VerifiedAt *time.Time `json:"verified_at,omitempty"`
	// Attempts are the starts of the executor, oldest first.
	Attempts []Attempt `json:"attempts"`
}

// Change is the change request a run of a task opened: its number and where it is,
// which is all a list of runs says about it.
type Change struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
}

// Schema is the version of the format of a state file, and a file without one is
// the version before it: crewflow kept the branch, the worktree and the outcomes of
// the attempts, and every attempt of it is read as it was written. A run that goes
// on writes the file in this version, and a file of a version crewflow does not know
// is refused rather than read as something it is not.
const Schema = 1

// Identity is whose name the executor of an attempt worked under: the mode it worked
// in and the one line a report shows (docs/DESIGN.md §7i).
//
// From the schema of §7h on it is written as the mode itself, "owner" or "bot": the
// words of a report of a run are in the journal and in the result of a run, and what
// a state file has to say is which of the two modes a try went under. A state written
// before that held a table with a mode and a description; it is read as it is, the
// table with the two words, and the mode in it is what a report shows.
type Identity struct {
	// Mode is "owner" or "bot", the two words the core knows.
	Mode string
	// Description is the one line a report of the run shows.
	Description string
}

// MarshalJSON is the mode alone: a state file of §7h holds `identity: "owner"`, and
// the line a report shows is in the report and not in the state of the task.
func (i Identity) MarshalJSON() ([]byte, error) {
	return json.Marshal(i.Mode)
}

// UnmarshalJSON reads both the mode of §7h and the table of the format before it, so
// that a state of before is read as what it is and a state of now is read as the
// mode it is.
func (i *Identity) UnmarshalJSON(data []byte) error {
	var mode string
	if err := json.Unmarshal(data, &mode); err == nil {
		i.Mode = mode
		return nil
	}
	type identity Identity
	var before identity
	if err := json.Unmarshal(data, &before); err != nil {
		return fmt.Errorf("the identity of an attempt is neither %q nor a table of before: %w", "owner", err)
	}
	*i = Identity(before)
	return nil
}

// Attempt is one start of the executor on a task: who ran it, in which session, whose
// name it went under, when it was, where it wrote, and how it ended. A task has as
// many attempts as it took tries, and the journal of each of them stays where it was
// written.
type Attempt struct {
	// Number is the attempt in the task, starting at one.
	Number int `json:"number"`
	// Executor is the agent of the attempt — "opencode", "codex" — and Session the
	// session of it, which is what a continuation goes on in. Both are of the
	// attempt and not of the task: a task whose executor was replaced starts again
	// in a session of its own, and a continuation goes on in the one before
	// (docs/DESIGN.md §7, §7h).
	Executor string `json:"executor,omitempty"`
	Session  string `json:"session,omitempty"`
	// StartedAt and EndedAt are when the executor was started and when it stopped,
	// which is what says how long a run took.
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	// Journal and ErrorJournal are the files of what the executor wrote and of
	// what it said on the way out.
	Journal      string `json:"journal"`
	ErrorJournal string `json:"error_journal"`
	// Identity is whose name the executor of this attempt worked under, so that a
	// person reading the state of a task afterwards sees it for every attempt and
	// not only for the last one (docs/DESIGN.md §7i).
	Identity Identity `json:"identity"`
	// Outcome is how the attempt ended.
	Outcome Kind `json:"outcome"`
	// Continued says that the attempt went on in the session of an earlier one.
	Continued bool `json:"continued"`
	// AutoResumed is the habit an attempt went on by itself for, and is empty for a
	// first run and for a continuation the orchestrator asked for. It is what keeps a
	// task from being resumed by itself twice for the same habit: the second time
	// round the run stops and the orchestrator decides (docs/DESIGN.md §7a, §7j).
	AutoResumed string `json:"auto_resumed,omitempty"`
	// PID and ProcessStartedAt are the process of the run of crewflow itself: its
	// number and when that process started. It is crewflow and not the executor, and
	// that is on purpose — crewflow lives exactly as long as the run and stops the
	// executor when it is stopped, so a run whose crewflow is gone is a run that is
	// over whatever its executor is doing. The state of a task says "running" until
	// something says otherwise, and after a reboot of the machine nothing does but
	// the process itself (docs/DESIGN.md §7).
	PID              int        `json:"pid,omitempty"`
	ProcessStartedAt *time.Time `json:"process_started_at,omitempty"`
}

// Process is the process of the run of crewflow the attempt was made in, and whether
// the state names one at all: a state written before crewflow kept the number of a
// process names none, and the machine cannot be asked about a number it does not have.
func (a Attempt) Process() (proc.Process, bool) {
	if a.PID <= 0 || a.ProcessStartedAt == nil {
		return proc.Process{}, false
	}
	return proc.Process{Pid: a.PID, StartedAt: *a.ProcessStartedAt}, true
}

// Attempt returns the attempt with the number, and whether there is one.
func (s State) Attempt(number int) (Attempt, bool) {
	for _, attempt := range s.Attempts {
		if attempt.Number == number {
			return attempt, true
		}
	}
	return Attempt{}, false
}

// StartOf is one start of the executor: when it was, which agent ran it, in which
// session, whose name it went under, where it writes, and whether it goes on in the
// session of an attempt before. It is what a run of a task knows about its own start
// before the executor is started, and the state of the task is told before the
// executor is (docs/DESIGN.md §7).
type StartOf struct {
	// Started is when the executor was started: the attempt is going on from the
	// moment it is added, because that is what the state of a task says while the
	// executor works.
	Started time.Time
	// Journal and ErrorJournal are the files of what it writes and of what it says
	// on the way out.
	Journal      string
	ErrorJournal string
	// Executor is the agent of the attempt and Session the session of it, and a
	// continuation goes on in the session of the attempt before.
	Executor string
	Session  string
	// Continued says that the attempt goes on in the session of an earlier one.
	Continued bool
	// AutoResumed is the habit crewflow went on by itself for, when nobody asked it
	// to: an attempt the orchestrator continued is not one, and says nothing here.
	AutoResumed string
	// Process is the process the run of crewflow is happening in, which is what a
	// later list asks the machine about, and the identity is whose name the
	// executor of the run is about to work under, so that the state says it from
	// the moment the run starts and not only when it is over (docs/DESIGN.md §7, §7i).
	Process  proc.Process
	Identity Identity
}

// NextAttempt is the number the next start of the executor of this task gets, and
// the state with that attempt added and not written anywhere yet. The attempt is
// running from the moment it is added: it is what the state of a task says while the
// executor works, and what a run that is cut short leaves behind.
func (s State) NextAttempt(start StartOf) State {
	next := len(s.Attempts) + 1
	attempt := Attempt{
		Number:       next,
		StartedAt:    start.Started,
		Journal:      start.Journal,
		ErrorJournal: start.ErrorJournal,
		Executor:     start.Executor,
		Session:      start.Session,
		Identity:     start.Identity,
		Outcome:      Running,
		Continued:    start.Continued,
		AutoResumed:  start.AutoResumed,
	}
	if start.Process.Pid > 0 {
		started := start.Process.StartedAt
		attempt.PID, attempt.ProcessStartedAt = start.Process.Pid, &started
	}
	s.Attempts = append(s.Attempts, attempt)
	return s
}

// Ended records how the last attempt of the task ended, and when.
func (s State) Ended(ended time.Time, outcome Kind) State {
	if len(s.Attempts) == 0 {
		return s
	}
	last := len(s.Attempts) - 1
	s.Attempts[last].EndedAt = ended
	s.Attempts[last].Outcome = outcome
	return s
}

// Journals is where the journal of every run and the state of every task are kept:
// under the root crewflow has of its own and by the name of the project, so that
// the work of two projects never mixes and a person has one place to look
// (docs/DESIGN.md §7).
type Journals struct {
	// home is the root of what crewflow keeps, "~/.crewflow" in a real run.
	home string
	// repo is the project as "owner-name", the same name its worktrees are under.
	repo string
}

// newJournals is the place of the files of one project.
func newJournals(home, repo string) Journals {
	return Journals{home: home, repo: repo}
}

// JournalsOf is where the files of one project are kept under the root crewflow has
// of its own: the state of every task, the journal of every run and the journal of
// every merge of it. A command outside this package that keeps what it knows of a
// task — the merge of a change, the check after it — writes it where a run of that
// task wrote its own, so that a person has one place to look (docs/DESIGN.md §7).
func JournalsOf(home, repo string) Journals {
	return newJournals(home, repo)
}

// StatePath is the file that says where the last run of a task stopped.
func (j Journals) StatePath(number int) string {
	return filepath.Join(j.stateFolder(), strconv.Itoa(number)+".json")
}

// stateFolder is the folder the states of the tasks of the project are kept in, which
// is what `crewflow task list` reads and no network is needed for.
func (j Journals) stateFolder() string {
	return filepath.Join(j.home, "state", j.repo)
}

// JournalPath is what an attempt wrote, and errorJournalPath what it said on the
// way out: the two are read apart because a person reads the reason of a stop in
// the second one and the work of the run in the first.
func (j Journals) JournalPath(number, attempt int) string {
	return filepath.Join(j.home, "runs", j.repo, j.file(number, attempt, ".jsonl"))
}

// errorJournalPath is the way out of an attempt, next to what it wrote.
func (j Journals) errorJournalPath(number, attempt int) string {
	return filepath.Join(j.home, "runs", j.repo, j.file(number, attempt, ".err"))
}

// MergeJournalPath is what the merge of the change of a task wrote, next to the
// journals of the runs of that task: a merge is not an attempt of the executor and
// has no attempt of its own, and what git said about it is kept where a person
// looks for what a task of theirs did (docs/DESIGN.md §6, §7h).
func (j Journals) MergeJournalPath(number int) string {
	return filepath.Join(j.home, "runs", j.repo, strconv.Itoa(number)+"-merge.jsonl")
}

// file is the name of the file of an attempt, whatever it holds.
func (j Journals) file(number, attempt int, suffix string) string {
	return strconv.Itoa(number) + "-" + strconv.Itoa(attempt) + suffix
}

// AttemptFiles are the two files of one attempt, open and ready to be written to
// while the executor is writing: the journal of what it wrote and the way out of it,
// next to each other.
type AttemptFiles struct {
	// Journal and ErrorJournal are where the files are, which is what a person is
	// sent to and what `crewflow task watch` reads.
	Journal      string
	ErrorJournal string
	// Out and ErrOut are the open files. Everything written to them goes to the
	// files above as well: a journal that is written only at the end of a run is a
	// journal of a run that was cut short with nothing in it.
	Out, ErrOut *os.File
}

// Begin opens the two files of an attempt, before the executor is started. What a
// run writes is in them from its first line, so that a run that is cut short leaves
// its journal behind (docs/DESIGN.md §7).
func (j Journals) Begin(number, attempt int) (*AttemptFiles, error) {
	files := &AttemptFiles{
		Journal:      j.JournalPath(number, attempt),
		ErrorJournal: j.errorJournalPath(number, attempt),
	}
	var err error
	if files.Out, err = openForWriting(files.Journal); err != nil {
		return nil, err
	}
	if files.ErrOut, err = openForWriting(files.ErrorJournal); err != nil {
		_ = files.Out.Close()
		return nil, err
	}
	return files, nil
}

// Close is what a run does with the files of its attempt when the executor is done:
// the journal of a run is closed before anything is read out of it.
func (f *AttemptFiles) Close() error {
	return errors.Join(f.Out.Close(), f.ErrOut.Close())
}

// openForWriting makes the file of a journal and empties it: the file of an attempt
// is written by one run, and what a run of an earlier attempt wrote is in the file of
// that attempt.
func openForWriting(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("make %s: %w", filepath.Dir(path), err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	return file, nil
}

// LoadState reads what crewflow kept of a task. A task with no state is not an
// error about the machine: it is a task that was never run, and the caller decides
// what that means.
//
// A state written before the format was versioned is read as the version before the
// first one, and nothing of it is lost: a run of before is a run of before, and the
// file is written in the current format the next time a run of the task touches it
// (docs/DESIGN.md §7h). A state of a version crewflow does not know is refused: a
// file of a shape nobody has looked at is a file crewflow cannot say anything true
// about, and a report that says nothing is worth more than one that lies.
func LoadState(path string) (State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return State{}, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("the state of the task in %s is not readable: %w", path, err)
	}
	if state.Schema > Schema {
		return State{}, fmt.Errorf("the state of the task in %s is of the format %d, and crewflow knows %d: "+
			"a newer crewflow wrote it, and this one cannot read what it does not know", path, state.Schema, Schema)
	}
	return state, nil
}

// SaveState writes the state of a task, in the current format of it whatever the one
// it was read in: a state of before is a state of a task of before, and the run that
// goes on with it writes what it knows today. It is written whole or not at all: a
// file cut in half by an interruption is a file that says the wrong thing about a
// run, and a state that is wrong is worse than none.
func SaveState(path string, state State) error {
	state.Schema = Schema
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("the state of the task: %w", err)
	}
	return writeFileAtomic(path, append(data, '\n'))
}

// writeFileAtomic writes through a file of its own next to the one it writes and
// moves it over: a reader of the state either sees the last whole state or the one
// before it, and never half of either.
func writeFileAtomic(path string, data []byte) error {
	folder := filepath.Dir(path)
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return fmt.Errorf("make %s: %w", folder, err)
	}
	temporary, err := os.CreateTemp(folder, filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("make a file in %s: %w", folder, err)
	}
	name := temporary.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write %s: %w", name, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("put %s in place of %s: %w", name, path, err)
	}
	return nil
}

// writeFile writes one file whole, which a journal may be big and a state never is.
func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("make %s: %w", filepath.Dir(path), err)
	}
	return os.WriteFile(path, data, 0o600)
}

// firstLine is the first line that says something, of the outputs in order. A
// command that said nothing has no line, and a report shows that instead of a
// blank.
func firstLine(outputs ...[]byte) string {
	for _, output := range outputs {
		for line := range bytes.Lines(output) {
			if text := string(bytes.TrimSpace(line)); text != "" {
				return text
			}
		}
	}
	return ""
}
