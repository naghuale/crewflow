package task

import (
	"context"
	"errors"
	"fmt"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/forge"
)

// Readiness is the answer to the one question a person asks before a run: may this
// task be run at all, and if not, what is it missing. It is what `crewflow task
// check` shows, and every entry of it is a thing a person can go and write
// (docs/DESIGN.md §7f).
type Readiness struct {
	// Number and Title are the task as the tracker holds it, so that a report says
	// which task it is of without the tracker being read again.
	Number int    `json:"task"`
	Title  string `json:"title"`
	// Ready says whether the task may be run, and Missing is what it lacks when it
	// may not: one entry per thing, none of them a file or a branch.
	Ready   bool     `json:"ready"`
	Missing []string `json:"missing,omitempty"`
}

// Check is the readiness of a task as the tracker holds it, and nothing else: no
// branch, no worktree, no attempt, no state, nothing written anywhere. It answers
// whether a task may be run without starting one, which is what a check of a task is
// for, and a check that made a worktree would already be a run (docs/DESIGN.md §7f).
func Check(ctx context.Context, set forge.Set, cfg config.Config, number int) (Readiness, error) {
	if set.Tracker == nil {
		return Readiness{}, errors.New("tracker.kind: this project has no tracker of tasks crewflow can read, so there is no task to check")
	}
	found, err := set.Tracker.Task(ctx, number)
	if err != nil {
		return Readiness{}, fmt.Errorf("read task %d: %w", number, err)
	}
	readiness := Readiness{Number: found.Number, Title: found.Title, Ready: true}
	// What the task is missing is a fact about the task and not a failure of the
	// machine: it is the answer a check is there to give.
	var notReady *ErrNotReady
	if errors.As(CheckReady(found, cfg), &notReady) {
		readiness.Ready, readiness.Missing = false, notReady.Missing
	}
	return readiness, nil
}
