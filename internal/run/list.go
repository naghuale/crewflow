package run

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/naghuale/crewflow/internal/proc"
)

// ListLimit is how many runs `crewflow task list` shows unless -all was asked for: a
// person asks what is going on and what was going on, and twenty tasks is more of that
// than anybody reads. An orchestrator asks for all of them and says what it wants with
// them.
const ListLimit = 20

// Liveness is the question a list asks about a run: is the process the run was in
// still that process. It is the machine, asked.
type Liveness func(process proc.Process) bool

// ListEnv is what a list of runs needs from the machine: the clock, to say how long a
// run that is going has been going; the question whether it is going at all; and the
// time limit of a run of the project, which is what says that a run crewflow kept no
// process of is over.
type ListEnv struct {
	// Now is the clock of the machine.
	Now func() time.Time
	// Running asks whether the process of a run is still there.
	Running Liveness
	// Timeout is how long a run of the project may take.
	Timeout time.Duration
}

// Runs is what crewflow knows of the runs of one project: one entry per task that was
// run, and what it could not read.
type Runs struct {
	// Entries are the tasks that were run, the ones that are going right now first.
	Entries []Entry
	// Unreadable are the state files that say nothing about a run, and Left is how
	// many entries were left out of a list that was not asked to be a long one.
	Unreadable []string
	Left       int
}

// Entry is one task in the list: what it is, how many tries it took, how the last of
// them came out — or that it is going on right now — and where its change request is.
type Entry struct {
	// Task and Title are the task the run was of, as the state of it says: a list
	// reads no tracker, so that it is the same answer with the network down.
	Task  int    `json:"task"`
	Title string `json:"title"`
	// Attempts is how many times the executor was started on the task.
	Attempts int `json:"attempts"`
	// Outcome is how the last attempt came out. It is running while the process of
	// the run is still there, interrupted once it is not, and the outcome of the run
	// itself otherwise (docs/DESIGN.md §7).
	Outcome Kind `json:"outcome"`
	// StartedAt is when the last attempt was started, and EndedAt is when it stopped,
	// which there is none of while it is going.
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	// Duration is how long the last attempt took, or has been going for. It is zero
	// when nothing kept it, and Length says when that is.
	Duration time.Duration `json:"-"`
	// ChangeURL is the change request the run of the task opened, when it opened one.
	ChangeURL string `json:"change_url,omitempty"`
}

// Length is how long the last attempt of the task took, and whether that is known at
// all. A run whose process is gone was not seen to stop: crewflow was not there to see
// it end, and how long it went on afterwards is in nothing it kept.
func (e Entry) Length() (time.Duration, bool) {
	if e.Outcome != Running && e.EndedAt == nil {
		return 0, false
	}
	return e.Duration, true
}

// MarshalJSON is the shape the orchestrator reads a list in. The length of a run is in
// seconds, because a program counts seconds and does not read "1h 05m", and it is
// nothing at all when nothing kept it.
func (e Entry) MarshalJSON() ([]byte, error) {
	length, known := e.Length()
	var seconds *float64
	if known {
		whole := math.Round(length.Seconds())
		seconds = &whole
	}
	answer := struct {
		Task            int        `json:"task"`
		Title           string     `json:"title"`
		Attempts        int        `json:"attempts"`
		Outcome         Kind       `json:"outcome"`
		StartedAt       time.Time  `json:"started_at"`
		EndedAt         *time.Time `json:"ended_at,omitempty"`
		DurationSeconds *float64   `json:"duration_seconds"`
		ChangeURL       string     `json:"change_url,omitempty"`
	}{
		Task:            e.Task,
		Title:           e.Title,
		Attempts:        e.Attempts,
		Outcome:         e.Outcome,
		StartedAt:       e.StartedAt,
		EndedAt:         e.EndedAt,
		DurationSeconds: seconds,
		ChangeURL:       e.ChangeURL,
	}
	data, err := json.Marshal(answer)
	if err != nil {
		return nil, fmt.Errorf("the run of the task %d: %w", e.Task, err)
	}
	return data, nil
}

// List reads the state of every task of a project and makes the list of its runs: the
// ones that are going right now on top, and the rest from the last try of a task to the
// first. It reads the state of the tasks and nothing else — no tracker, no host, no
// network — because the runs of a project are what happened on the machine it was run
// on (docs/DESIGN.md §7).
func List(home, repo string, all bool, env ListEnv) (Runs, error) {
	folder := newJournals(home, repo).stateFolder()
	names, err := os.ReadDir(folder)
	if err != nil {
		// A project that was never run has no folder of states yet, and that is
		// nothing wrong with the machine: there is no run to show, and the person who
		// asked is told so.
		if os.IsNotExist(err) {
			return Runs{}, nil
		}
		return Runs{}, fmt.Errorf("read the state of the tasks in %s: %w", folder, err)
	}
	runs := Runs{}
	for _, name := range names {
		if !isStateOfATask(name.Name()) {
			continue
		}
		path := filepath.Join(folder, name.Name())
		// One file crewflow cannot read does not take the tasks of the other ones
		// down with it: what did happen is what a person came for, and the file that
		// could not be read is named so that they can look at it.
		state, err := LoadState(path)
		if err == nil {
			if entry, ok := env.entryOf(state); ok {
				runs.Entries = append(runs.Entries, entry)
				continue
			}
		}
		runs.Unreadable = append(runs.Unreadable, path)
	}
	slices.SortStableFunc(runs.Entries, byRecency)
	return runs.shown(all), nil
}

// isStateOfATask is whether the name is the state of a task. The folder of the states
// holds nothing else, and what an interrupted write left in it is not a task.
func isStateOfATask(name string) bool {
	number, ok := strings.CutSuffix(name, ".json")
	if !ok {
		return false
	}
	task, err := strconv.Atoi(number)
	return err == nil && task > 0
}

// entryOf is what crewflow makes of the state of one task: the tries it took, and the
// last of them with how long it has been going. A state with no attempt in it is a
// state of no run, and there is nothing to show of the task.
func (e ListEnv) entryOf(state State) (Entry, bool) {
	if len(state.Attempts) == 0 {
		return Entry{}, false
	}
	last := state.Attempts[len(state.Attempts)-1]
	entry := Entry{
		Task:      state.Number,
		Title:     state.Title,
		Attempts:  len(state.Attempts),
		Outcome:   e.outcome(last),
		StartedAt: last.StartedAt,
	}
	if !last.EndedAt.IsZero() {
		ended := last.EndedAt
		entry.EndedAt = &ended
	}
	switch {
	case entry.Outcome == Running:
		// A run that is going is as long as it has been going so far, and its end is
		// a moment that has not come yet.
		entry.Duration = e.Now().Sub(last.StartedAt)
	case !last.EndedAt.IsZero():
		entry.Duration = last.EndedAt.Sub(last.StartedAt)
	}
	if state.Change != nil {
		entry.ChangeURL = state.Change.URL
	}
	return entry, true
}

// outcome is how the last try of a task came out, as a list of runs says it. An attempt
// that says "running" is only running while the process of the run is still there: a
// window that was closed, a machine that was rebooted and a run killed outright are one
// and the same thing to the person who comes back to it, and the state of the task
// cannot tell them apart (docs/DESIGN.md §7).
func (e ListEnv) outcome(last Attempt) Kind {
	if last.Outcome != Running {
		return last.Outcome
	}
	process, named := last.Process()
	switch {
	case named:
		if e.Running(process) {
			return Running
		}
		// The process of the run is gone, and no other process has its number: a run
		// that is over and was not over when it was written down.
		return Interrupted
	case e.Now().Sub(last.StartedAt) > e.Timeout:
		// A state of before crewflow kept the number of the process of a run: there
		// is nothing to ask, and a run that has been going for longer than any run of
		// this project may go on is not going on.
		return Interrupted
	default:
		// A run of a moment ago that names no process. It may be going and crewflow
		// has no way of telling, so it says that instead of saying that it is.
		return MaybeRunning
	}
}

// byRecency is the order a person reads the runs of a project in: what is going on
// right now, and then what was started last. A run that is going is on top whatever it
// was started with, because it is the one that wants a decision today.
func byRecency(a, b Entry) int {
	switch {
	case a.Outcome == Running && b.Outcome != Running:
		return -1
	case b.Outcome == Running && a.Outcome != Running:
		return 1
	}
	return b.StartedAt.Compare(a.StartedAt)
}

// shown is the list the caller asked for: the last runs of the project, and the number
// of the ones that did not fit into it.
func (r Runs) shown(all bool) Runs {
	if all || len(r.Entries) <= ListLimit {
		return r
	}
	r.Left = len(r.Entries) - ListLimit
	r.Entries = r.Entries[:ListLimit]
	return r
}

// entry is the run of the task with that number in the list, and whether the list holds
// one.
func (r Runs) entry(number int) (Entry, bool) {
	for _, entry := range r.Entries {
		if entry.Task == number {
			return entry, true
		}
	}
	return Entry{}, false
}
