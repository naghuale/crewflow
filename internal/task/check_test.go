package task

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/forge"
)

// TestCheckSaysWhatATaskIsMissing: the answer of a check is the readiness of the task
// and nothing else, and what is missing is named one thing at a time, because it is
// what the owner goes and writes.
func TestCheckSaysWhatATaskIsMissing(t *testing.T) {
	cases := []struct {
		name        string
		without     []string
		labels      []string
		approval    string
		wantReady   bool
		wantMissing []string
	}{
		{
			name:      "a task that is ready",
			wantReady: true,
		},
		{
			name:        "a section nobody wrote",
			without:     []string{"check"},
			wantMissing: []string{headings["en"]["check"]},
		},
		{
			name:        "the technical part nobody wrote",
			without:     []string{"details"},
			wantMissing: []string{"the technical part of the task, folded away in <details>"},
		},
		{
			name:        "a risky task nobody approved",
			labels:      []string{"risky"},
			approval:    "risky",
			wantMissing: []string{"the owner approved it: the label `approved`"},
		},
		{
			name:        "a task of a project that waits for every task",
			approval:    "all",
			wantMissing: []string{"the owner approved it: the label `approved`"},
		},
		{
			name:      "a risky task the owner approved",
			labels:    []string{"risky", "approved"},
			approval:  "risky",
			wantReady: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tracker := &trackerOf{task: labelled(taskOf(43, "en", tc.without...), tc.labels)}
			approval := tc.approval
			if approval == "" {
				approval = "risky"
			}

			readiness, err := Check(t.Context(), forge.Set{Tracker: tracker}, projectOf("en", approval), 43)

			if err != nil {
				t.Fatalf("Check returned an error: %v", err)
			}
			if readiness.Ready != tc.wantReady {
				t.Errorf("the task is ready = %t, want %t (missing %q)", readiness.Ready, tc.wantReady, readiness.Missing)
			}
			if !slices.Equal(readiness.Missing, tc.wantMissing) {
				t.Errorf("what is missing = %q, want %q", readiness.Missing, tc.wantMissing)
			}
			if readiness.Number != tracker.task.Number || readiness.Title != tracker.task.Title {
				t.Errorf("the readiness = %+v, want the task the tracker holds", readiness)
			}
		})
	}
}

// TestCheckCreatesNothing: a check is a question about a task, and a question that
// left a branch, a worktree or a state behind would already be a run of it. The home
// of crewflow and the root of the worktrees of the project are the two places a run
// writes to, and both of them have to be as they were.
func TestCheckCreatesNothing(t *testing.T) {
	home, worktrees := t.TempDir(), filepath.Join(t.TempDir(), "worktrees")
	tracker := &trackerOf{task: taskOf(43, "en")}
	cfg := projectOf("en", "risky")
	cfg.Worktrees = config.Worktrees{Root: worktrees + "/{repo}"}

	readiness, err := Check(t.Context(), forge.Set{Tracker: tracker}, cfg, 43)

	if err != nil {
		t.Fatalf("Check returned an error: %v", err)
	}
	if !readiness.Ready {
		t.Fatalf("the task is not ready: %q, want a task that is ready", readiness.Missing)
	}
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
		t.Errorf("the check left %v in the home of crewflow, want nothing", entries)
	}
	if _, err := os.Stat(worktrees); !os.IsNotExist(err) {
		t.Errorf("the check made the root of the worktrees: %v", err)
	}
}

// TestCheckOfATaskTheTrackerDoesNotHave: a number nothing knows is an error that says
// so, and not a task that is not ready: the two lead a person to different places.
func TestCheckOfATaskTheTrackerDoesNotHave(t *testing.T) {
	tracker := &trackerOf{noTask: errors.New("could not find issue 999")}

	_, err := Check(t.Context(), forge.Set{Tracker: tracker}, projectOf("en", "risky"), 999)

	if err == nil {
		t.Fatal("Check of a task that is not there returned no error, want one")
	}
	for _, want := range []string{"999", "could not find issue 999"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// TestCheckOfAProjectWithoutATracker: a project that keeps its tasks in files crewflow
// cannot read yet has no task to check, and the key to change is named.
func TestCheckOfAProjectWithoutATracker(t *testing.T) {
	_, err := Check(t.Context(), forge.Set{}, projectOf("en", "risky"), 43)

	if err == nil {
		t.Fatal("Check of a project without a tracker returned no error, want one")
	}
	if !strings.Contains(err.Error(), "tracker.kind") {
		t.Errorf("error %q does not name the key a person has to change", err)
	}
}

// trackerOf is the tracker of a test: the task it holds and the error it says for a
// task it does not have. Nothing of it goes anywhere, so no test of a check reaches a
// tracker of a person.
type trackerOf struct {
	task   forge.Task
	noTask error
}

// Task returns the task of the test, or the error it was given for another one.
func (h *trackerOf) Task(_ context.Context, number int) (forge.Task, error) {
	if h.noTask != nil {
		return forge.Task{}, h.noTask
	}
	if h.task.Number != number {
		return forge.Task{}, fmt.Errorf("could not find issue %d", number)
	}
	return h.task, nil
}

// Doctor says nothing, which a check of a task never asks a tracker about.
func (h *trackerOf) Doctor(context.Context) []forge.Check { return nil }

// labelled is the task with the labels the test gives it, which is what decides
// whether the owner has to approve it (docs/DESIGN.md §7f).
func labelled(task forge.Task, labels []string) forge.Task {
	task.Labels = labels
	return task
}
