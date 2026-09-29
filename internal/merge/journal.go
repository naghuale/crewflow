package merge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/naghuale/crewflow/internal/gate"
)

// Journal is what a merge wrote: the facts the gate judged, every command of git the
// merge ran, everything git wrote about it and how the merge came out — one JSON
// object a line, in the file of the merge of the task.
//
// It is kept next to the journals of the runs of that task, because the person who
// asks "did this go in?" is asked in the same place, and because the code `git push`
// exited with is not what says where the branch ended up: the whole of what git said
// is (docs/DESIGN.md §6, §7h).
type Journal struct {
	path string
	file *os.File
	now  func() time.Time
}

// Entry is one line of the journal of a merge: when it was written, what crewflow was
// doing, and what came of it.
type Entry struct {
	At   time.Time `json:"at"`
	Step string    `json:"step"`
	// Command is the command of git as a person would run it by hand, with Code what
	// it exited with and Stdout and Stderr everything it wrote: the whole of it, and
	// not the first line, because what a merge came to is worked out of all of it
	// (docs/DESIGN.md §6).
	Command string `json:"command,omitempty"`
	Code    int    `json:"code,omitempty"`
	Stdout  string `json:"stdout,omitempty"`
	Stderr  string `json:"stderr,omitempty"`
	Error   string `json:"error,omitempty"`
	// Gate is the facts of the change and the verdict the gate gave them, in the
	// shape a review shows them in: a merge is judged by the same gate and a person
	// reads the same words of it in either place (docs/DESIGN.md §7h).
	Gate *gate.Summary `json:"gate,omitempty"`
	// Outcome is how the merge came out, and Left what is left of what was to be done
	// around it.
	Outcome Outcome  `json:"outcome,omitempty"`
	Left    []string `json:"left,omitempty"`
}

// OpenJournal opens the journal of a merge, and empties it: the journal of a merge is
// written by that merge, and what a merge of before wrote is in the journal of the
// merge it was in.
//
// A journal that cannot be opened stops the merge before anything is pushed: it is
// where the whole of what git says is kept, and a merge whose evidence is thrown away
// is a merge nobody can look at afterwards.
func OpenJournal(path string, now func() time.Time) (*Journal, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("make the folder of the journal of the merge: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("write the journal of the merge: %w", err)
	}
	return &Journal{path: path, file: file, now: now}, nil
}

// Path is where the merge of a change writes what it did, which a report of a merge
// and a person both name.
func (j *Journal) Path() string { return j.path }

// Close is what a merge does with its journal when it is over.
func (j *Journal) Close() error {
	if j == nil || j.file == nil {
		return nil
	}
	return j.file.Close()
}

// Gate is the facts of the change and the verdict the gate gave them.
func (j *Journal) Gate(summary gate.Summary) error {
	return j.write(Entry{Step: "gate", Gate: &summary})
}

// Outcome is how the merge came out, with what is left to do by hand.
func (j *Journal) Outcome(result Result) error {
	return j.write(Entry{Step: "outcome", Outcome: result.Outcome, Left: result.Left})
}

// Note is something that happened around the merge and is not the outcome of it: a
// task that is not closed yet, a state that could not be written.
func (j *Journal) Note(err error) error {
	return j.write(Entry{Step: "note", Error: err.Error()})
}

// logging is the runner of git with every command and everything git wrote about it in
// the journal — the ones the gate runs while it gathers the facts included, because a
// merge is judged on the branch of the host and the whole of the way there is what a
// person reads (docs/DESIGN.md §6, §7h).
func (j *Journal) logging(run Runner) Runner {
	return func(ctx context.Context, name string, args []string, dir string) ([]byte, []byte, int, error) {
		stdout, stderr, code, err := run(ctx, name, args, dir)
		entry := Entry{
			Step:    "git",
			Command: name + " " + strings.Join(args, " "),
			Code:    code,
			Stdout:  string(stdout),
			Stderr:  string(stderr),
		}
		if err != nil {
			entry.Error = err.Error()
		}
		_ = j.write(entry)
		return stdout, stderr, code, err
	}
}

// write is one line of the journal. A journal that cannot be written is not what
// stops a merge — the merge is what the person is told about, and the journal is what
// they read afterwards — so the error comes back to the caller and the outcome is
// still what the branch of the host holds.
func (j *Journal) write(entry Entry) error {
	if j == nil || j.file == nil {
		return nil
	}
	if j.now != nil {
		entry.At = j.now()
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("the journal of the merge: %w", err)
	}
	if _, err := j.file.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write the journal of the merge: %w", err)
	}
	return nil
}
