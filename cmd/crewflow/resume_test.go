package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	taskrun "github.com/naghuale/crewflow/internal/run"
	"github.com/naghuale/crewflow/internal/secret"
)

// theWindowOfTheKeychain is the answer of a machine whose owner was not at it: the store of
// macOS waited in front of a window of the system for two whole minutes and nobody answered
// it, which is what a run of the mode of the bot stands at (§7i).
func theWindowOfTheKeychain() error {
	return fmt.Errorf("the keychain of macOS asked the owner to allow this program to read "+
		"crewflow/github-app-5107052, and nobody answered in 2m0s: %w", secret.ErrApproval)
}

// TestRunTaskResumeGoesOnFromThePointOfTheTask is the whole of what `crewflow task resume`
// is for: a run stood at the window of the keychain of macOS, nobody answered it, and the
// owner was told that the run has to be started again by hand. He does not start it again —
// he presses "Always Allow" in the window of the system and runs `crewflow task resume`, and
// the run goes on in the worktree and the branch the run before it left, with the task in
// hand (F-091, journal #37, docs/DESIGN.md §7i).
func TestRunTaskResumeGoesOnFromThePointOfTheTask(t *testing.T) {
	host := &host{task: taskOf(43), opened: true}
	host.use(t)
	project := host.config(t)
	host.noIdentity = theWindowOfTheKeychain()
	var stopped, said bytes.Buffer

	code := run([]string{"task", "run", "43", "-config", project}, &stopped, &said)

	if code != exitFailure {
		t.Fatalf("crewflow task run = %d, want %d: the run of the test has to stand at the window first",
			code, exitFailure)
	}
	if point := pointOfTheTest(t, host); point == nil {
		t.Fatal("the state of task 43 holds no point, want the point the run stood at")
	}
	// The report of a run that stands at a decision of a person says what to do about it:
	// the decision is his, and the command is crewflow's.
	if !strings.Contains(stopped.String(), "crewflow task resume 43") {
		t.Errorf("crewflow task run wrote %q, want it to name the command that goes on from the point",
			stopped.String())
	}

	// The owner answered the window of the system: "Always Allow".
	host.noIdentity = nil
	var stdout, stderr bytes.Buffer

	code = run([]string{"task", "resume", "43", "-config", project}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow task resume = %d, want %d (stderr: %q)\n%s", code, exitOK, stderr.String(), stdout.String())
	}
	for _, want := range []string{
		"task 43: pr-opened, attempt 2",
		"went on from the checkpoint read-executor-key",
		"the request came out as completed",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("crewflow task resume wrote %q, want it to mention %q", stdout.String(), want)
		}
	}
	if stderr.Len() != 0 {
		t.Errorf("crewflow task resume wrote %q to stderr, want nothing", stderr.String())
	}
}

// TestRunTaskResumeRefusesAndNamesWhy: a point is a claim about the machine and about the
// task as they were when the run stopped, and a continuation that cannot be made says so in
// a sentence a person can act on. The code of the command is not zero, because a script has
// to be able to tell a continuation that went on from one that did not (docs.DESIGN.md §6).
func TestRunTaskResumeRefusesAndNamesWhy(t *testing.T) {
	cases := []struct {
		name string
		// spoil is what the machine or the task is like when the person asks for the
		// continuation.
		spoil func(host *host)
		want  string
	}{
		{
			name:  "the branch of the task has moved",
			spoil: func(host *host) { host.head = "0f9e8d7c6b5a4938271605f4e3d2c1b0a9f8e7d6" },
			want:  "stands at 0f9e8d7",
		},
		{
			name: "the task is not the one the run was given",
			spoil: func(host *host) {
				host.task.Body += "\n### Risks and decisions\n\nAlso: do not touch the hooks.\n"
			},
			want: "it has been changed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host := &host{task: taskOf(43), opened: true}
			host.use(t)
			project := host.config(t)
			host.noIdentity = theWindowOfTheKeychain()
			var stopped, said bytes.Buffer
			if code := run([]string{"task", "run", "43", "-config", project}, &stopped, &said); code == exitOK {
				t.Fatal("crewflow task run = 0, want the run to stand at the window of the keychain first")
			}
			host.noIdentity = nil
			tc.spoil(host)

			var stdout, stderr bytes.Buffer

			code := run([]string{"task", "resume", "43", "-config", project}, &stdout, &stderr)

			if code != exitFailure {
				t.Fatalf("crewflow task resume = %d, want %d", code, exitFailure)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Errorf("crewflow task resume wrote %q, want it to hold %q: a refusal a person cannot "+
					"act on is a refusal they will ask about again", stderr.String(), tc.want)
			}
			if stdout.Len() != 0 {
				t.Errorf("crewflow task resume wrote %q, want a refusal to print nothing but its reason",
					stdout.String())
			}
		})
	}
}

// TestRunTaskResumeOfATaskThatWasNeverRun: a person who reads the queue of attention and
// runs the command it names for a task whose run went away has to be told what to do, and
// not shown an empty report (docs.DESIGN.md §6a).
func TestRunTaskResumeOfATaskThatWasNeverRun(t *testing.T) {
	host := &host{task: taskOf(43)}
	host.use(t)
	var stdout, stderr bytes.Buffer

	code := run([]string{"task", "resume", "43", "-config", host.config(t)}, &stdout, &stderr)

	if code != exitFailure {
		t.Fatalf("crewflow task resume = %d, want %d", code, exitFailure)
	}
	if !strings.Contains(stderr.String(), "crewflow task run 43") {
		t.Errorf("crewflow task resume wrote %q, want it to name the command that starts the task", stderr.String())
	}
}

// TestUsageMentionsTaskResume: the queue of attention names `crewflow task resume`, so a
// person who asks for help has to see it there.
func TestUsageMentionsTaskResume(t *testing.T) {
	var stdout, stderr bytes.Buffer

	if code := run([]string{"help"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("run(help) = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "task resume <N>") {
		t.Errorf("the usage does not mention `task resume <N>`:\n%s", stdout.String())
	}
}

// pointOfTheTest is the point the run of the task of a test stopped at, as the state of the
// task holds it.
func pointOfTheTest(t *testing.T, h *host) *taskrun.Checkpoint {
	t.Helper()
	journals := taskrun.JournalsOf(h.home, "naghuale-crewflow")
	state, err := taskrun.LoadState(journals.StatePath(43))
	if err != nil {
		t.Fatalf("the state of task 43: %v", err)
	}
	return state.Checkpoint
}
