package run

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/naghuale/crewflow/internal/run/profile"
)

// Watcher is a look at one attempt of a task: what it is, how far it has come, and
// what has been read of its journal already. It is what `crewflow task watch` shows
// in another terminal while a run goes on, and the file of the attempt is the only
// thing it reads: a run that is killed in the middle of has left its journal behind
// (docs/DESIGN.md §7).
type Watcher struct {
	// Task, Attempt and Title are what is being watched.
	Task    int
	Attempt int
	Title   string
	// Profile is what crewflow knew about the executor of the attempt, and the
	// journal is read through it: a journal is read the way the run that wrote it
	// was read, whatever the project uses as its executor today.
	Profile string
	// Identity is whose name the executor of the attempt worked under, so that a
	// watch says it before the journal and a person reading it in another terminal
	// knows whose run they are watching (docs/DESIGN.md §7i).
	Identity Identity
	// Outcome, StartedAt and EndedAt are how far the attempt has come. The outcome
	// of an attempt that has not ended yet is the outcome running.
	Outcome   Kind
	StartedAt time.Time
	EndedAt   time.Time
	// Journal and ErrorJournal are the files of the attempt: what the executor wrote
	// and what it said on the way out.
	Journal      string
	ErrorJournal string

	// reader is the profile of the attempt, statePath where the state of the task is
	// read again from while the run goes on, and the two tails how much of the files
	// has been read.
	reader       profile.Profile
	statePath    string
	journal      tail
	errorJournal tail
}

// Watch opens the journal of the last attempt of a task, or of the attempt with the
// number given, ready to be read. A task that was never run here and an attempt that
// was never made are both said as they are: either of them is the answer a person who
// asked for a watch has to have.
func Watch(home, repo string, number, attempt int) (*Watcher, error) {
	journals := newJournals(home, repo)
	path := journals.StatePath(number)
	state, err := LoadState(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("task %d was never run here, so there is no journal of it to watch", number)
		}
		return nil, err
	}
	if attempt <= 0 {
		attempt = len(state.Attempts)
	}
	found, ok := state.Attempt(attempt)
	if !ok {
		if len(state.Attempts) == 0 {
			return nil, fmt.Errorf("task %d has no attempts, so there is no journal of it to watch", number)
		}
		return nil, fmt.Errorf("task %d has no attempt %d, it has %d", number, attempt, len(state.Attempts))
	}
	return &Watcher{
		Task:         state.Number,
		Attempt:      found.Number,
		Title:        state.Title,
		Profile:      state.Profile,
		Identity:     found.Identity,
		Outcome:      found.Outcome,
		StartedAt:    found.StartedAt,
		EndedAt:      found.EndedAt,
		Journal:      found.Journal,
		ErrorJournal: found.ErrorJournal,
		reader:       profile.Named(state.Profile),
		statePath:    path,
		journal:      tail{path: found.Journal},
		errorJournal: tail{path: found.ErrorJournal},
	}, nil
}

// Running says whether the attempt is still going, which is what a watch waits for
// and what it stops on.
func (w *Watcher) Running() bool {
	return w.Outcome == Running
}

// Print writes what the journal of the attempt has grown into since it was printed
// last, through the profile of the run: the words of the agent, the tools it called
// and the permissions it was refused. A line the agent has not finished writing is
// held back until it is a whole one, and the last line of a run that has ended is
// shown even if nothing closed it.
func (w *Watcher) Print(out io.Writer) error {
	// The run of the attempt is over, so a line the executor did not close is a line
	// it will not close: it is shown as it is rather than lost.
	ended := !w.Running()
	journal, err := w.journal.lines(ended)
	if err != nil {
		return fmt.Errorf("read %s: %w", w.journal.path, err)
	}
	errorJournal, err := w.errorJournal.lines(ended)
	if err != nil {
		return fmt.Errorf("read %s: %w", w.errorJournal.path, err)
	}
	for _, line := range w.reader.Read(journal, errorJournal) {
		if _, err := fmt.Fprintln(out, line); err != nil {
			return fmt.Errorf("write the journal of the run: %w", err)
		}
	}
	return nil
}

// Follow prints the lines of the journal of the attempt as they come and returns
// when the attempt is not running any more: the state of the task is what says so,
// and it is read again on every turn. The tick is when a look happens, so that a test
// hands it a channel of its own and does not wait for a clock.
func (w *Watcher) Follow(ctx context.Context, out io.Writer, tick <-chan time.Time) error {
	for {
		if err := w.reload(); err != nil {
			return err
		}
		if err := w.Print(out); err != nil {
			return err
		}
		if !w.Running() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, more := <-tick:
			if !more {
				return nil
			}
		}
	}
}

// reload reads the state of the task again: a run writes how its attempt ended into
// it when the run is over, and that is the only thing that says the journal of the
// attempt is not growing any more.
func (w *Watcher) reload() error {
	state, err := LoadState(w.statePath)
	if err != nil {
		return fmt.Errorf("read the state of the task: %w", err)
	}
	attempt, ok := state.Attempt(w.Attempt)
	if !ok {
		return fmt.Errorf("the state of the task holds no attempt %d, want the one that is being watched", w.Attempt)
	}
	w.Outcome, w.EndedAt = attempt.Outcome, attempt.EndedAt
	return nil
}

// tail reads what one journal has grown into since it was last read, and holds back
// a line that is not finished: the executor writes a line of a journal in parts, and
// half a line is not a line.
type tail struct {
	path    string
	read    int64
	pending string
}

// lines are the lines the file has grown into, without the ends of the lines and
// without the empty ones: a journal of a run is a list of what the agent said. The
// last line is one even when nothing closed it, as long as the run of the attempt is
// over and the file will hold nothing more.
func (t *tail) lines(ended bool) ([]string, error) {
	data, err := t.since()
	if err != nil {
		return nil, err
	}
	whole := t.pending + data
	var lines []string
	for {
		line, rest, found := strings.Cut(whole, "\n")
		if !found {
			if ended && strings.TrimRight(whole, "\r") != "" {
				lines = append(lines, strings.TrimRight(whole, "\r"))
				whole = ""
			}
			break
		}
		if line = strings.TrimRight(line, "\r"); line != "" {
			lines = append(lines, line)
		}
		whole = rest
	}
	t.pending = whole
	return lines, nil
}

// since is what has been added to the file since the last time it was read, and how
// much of it has been read: the file is only ever added to while a run goes on, and
// a file that became shorter was begun again.
func (t *tail) since() (string, error) {
	file, err := os.Open(t.path)
	if err != nil {
		// A run that has written nothing yet has no journal to read. That is not an
		// error: the state of the task is what says the attempt is running.
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if info.Size() < t.read {
		t.read, t.pending = 0, ""
	}
	if _, err := file.Seek(t.read, io.SeekStart); err != nil {
		return "", err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return "", err
	}
	t.read += int64(len(data))
	return string(data), nil
}
