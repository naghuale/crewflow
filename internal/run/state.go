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
)

// State is what crewflow keeps of a task between its runs, so that after an
// interruption it is visible where the run stopped: the branch, the worktree, the
// profile of the executor, the session to go on in, and every attempt in its own
// right (docs/DESIGN.md §7). The truth about the work is still on the host; this
// is what crewflow needs to not start from the beginning next time.
type State struct {
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
	// Attempts are the starts of the executor, oldest first.
	Attempts []Attempt `json:"attempts"`
}

// Attempt is one start of the executor on a task: when it was, where it wrote, and
// how it ended. A task has as many attempts as it took tries, and the journal of
// each of them stays where it was written.
type Attempt struct {
	// Number is the attempt in the task, starting at one.
	Number int `json:"number"`
	// StartedAt and EndedAt are when the executor was started and when it stopped,
	// which is what says how long a run took.
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	// Journal and ErrorJournal are the files of what the executor wrote and of
	// what it said on the way out.
	Journal      string `json:"journal"`
	ErrorJournal string `json:"error_journal"`
	// Outcome is how the attempt ended.
	Outcome Kind `json:"outcome"`
	// Continued says that the attempt went on in the session of an earlier one.
	Continued bool `json:"continued"`
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

// NextAttempt is the number the next start of the executor of this task gets, and
// the state with that attempt added and not written anywhere yet. The attempt is
// running from the moment it is added: it is what the state of a task says while the
// executor works, and what a run that is cut short leaves behind.
func (s State) NextAttempt(started time.Time, journal, errorJournal string, continued bool) State {
	next := len(s.Attempts) + 1
	s.Attempts = append(s.Attempts, Attempt{
		Number:       next,
		StartedAt:    started,
		Journal:      journal,
		ErrorJournal: errorJournal,
		Outcome:      Running,
		Continued:    continued,
	})
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

// StatePath is the file that says where the last run of a task stopped.
func (j Journals) StatePath(number int) string {
	return filepath.Join(j.home, "state", j.repo, strconv.Itoa(number)+".json")
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
func LoadState(path string) (State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return State{}, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("the state of the task in %s is not readable: %w", path, err)
	}
	return state, nil
}

// SaveState writes the state of a task. It is written whole or not at all: a file
// cut in half by an interruption is a file that says the wrong thing about a run,
// and a state that is wrong is worse than none.
func SaveState(path string, state State) error {
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
