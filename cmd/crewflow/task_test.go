package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/forge/roles"
	"github.com/naghuale/crewflow/internal/proc"
	taskrun "github.com/naghuale/crewflow/internal/run"
	"github.com/naghuale/crewflow/internal/task"
)

// wholeTask is a task as the template of the project asks a person to write it.
const wholeTask = "## Why\n\nA person builds a long command by hand for every task.\n\n" +
	"## What changes\n\nOne command runs the task and says how the run ended.\n\n" +
	"## How to check it yourself\n\n1. Run it on a task that is not there.\n\n" +
	"## Out of scope\n\nReview and merge are other commands.\n\n" +
	"## Risks and decisions\n\nA run makes a branch; main is not touched.\n\n" +
	"<details>\n<summary>Technical part for the executor</summary>\n\n" +
	"### Acceptance criteria\n\n- [ ] the run opens a change request\n\n" +
	"### Boundaries\n\n```\ninternal/run/**\n```\n\n</details>\n"

// The refusals a run of a test is stopped by, in the words of the agent that is
// refused: the temporary folder of the machine, which is a habit crewflow answers by
// itself, and a place of secrets, which is closed whatever the project wrote and ends
// the run as an outcome of its own (docs/DESIGN.md §7a.1, §7d).
const (
	theRefusalToTmp     = "! permission requested: external_directory (/tmp/*); auto-rejecting\n"
	theRefusalToASecret = "! permission requested: external_directory (~/.ssh/config); auto-rejecting\n"
)

// recoveryOff is what the report of a run that reached for a secret says on the way out:
// no run of it goes on by itself, whatever the habit of the command was (docs/DESIGN.md
// §7a.1, §7d).
const recoveryOff = "recovery: disabled"

// theHeadOfTheTest is the commit the worktree of a task of a test stands at, and the head
// the point of a run that was stopped at a decision of a person holds: a continuation is
// refused when the worktree stands anywhere else (docs.DESIGN.md §7i).
const theHeadOfTheTest = "9f1c0de4a4a0b1f2c3d4e5f60718293a4b5c6d7"

// TestRunTaskOpenedTheChangeRequest is the case a person waits for: the run opened
// the change request of its branch, and the report says where it is, what a person
// has to look at, and where the journal of the run is.
func TestRunTaskOpenedTheChangeRequest(t *testing.T) {
	host := &host{opened: true, task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	var stdout, stderr bytes.Buffer

	code := run([]string{"task", "run", "43", "-config", project}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow task run = %d, want %d (stderr: %q)\n%s", code, exitOK, stderr.String(), stdout.String())
	}
	for _, want := range []string{
		"#44", "https://github.com/naghuale/crewflow/pull/44",
		"crewflow/43-the-run-of-a-task",
		"journal", filepath.Join(host.home, "runs", "naghuale-crewflow", "43-1.jsonl"),
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("crewflow task run wrote %q, want it to mention %q", stdout.String(), want)
		}
	}
	if stderr.Len() != 0 {
		t.Errorf("crewflow task run wrote %q to stderr, want nothing", stderr.String())
	}
	if host.repoDir != "." {
		t.Errorf("the worktree was made out of %q, want the folder crewflow was called in", host.repoDir)
	}
}

// TestRunTaskExitCode walks the outcomes that are not what a run is for: each of
// them is reported, the journal is named, and the code of the command is not zero,
// because a script has to be able to tell them from a run that went well.
func TestRunTaskExitCode(t *testing.T) {
	cases := []struct {
		name string
		host *host
		want []string
		// notOK says that the run was not what a run is for, and the code of the
		// command is not zero.
		notOK bool
		// refusal is what the executor says on the way out: a run that is refused a
		// permission ends with the code of a success, and what it was refused is the
		// whole difference between the cases.
		refusal string
	}{
		{
			name:    "the run was refused a permission",
			host:    &host{task: taskOf(43)},
			want:    []string{"blocked-permission", "external_directory /tmp/*"},
			notOK:   true,
			refusal: theRefusalToTmp,
		},
		{
			// A place of secrets is an outcome of a run of its own, and the report says
			// what the run reached for, what for, and that no run of it goes on by
			// itself (docs/DESIGN.md §7a.1, §7d).
			name:    "the run reached for a place of secrets",
			host:    &host{task: taskOf(43)},
			want:    []string{"blocked-secret", "external_directory ~/.ssh/config", "a secret", recoveryOff},
			notOK:   true,
			refusal: theRefusalToASecret,
		},

		{
			name:  "the run ended without a change request",
			host:  &host{task: taskOf(43)},
			want:  []string{"no-change-request"},
			notOK: true,
		},
		{
			name:  "the run changed a file the task was not to change",
			host:  &host{task: taskOf(43), changed: "docs/DESIGN.md\n"},
			want:  []string{"out-of-scope", "docs/DESIGN.md"},
			notOK: true,
		},
		{
			name:  "the run opened the change request and went out of the boundaries",
			host:  &host{task: taskOf(43), opened: true, changed: "docs/DESIGN.md\n"},
			want:  []string{"out-of-scope", "#44"},
			notOK: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.refusal != "" {
				tc.host.refusal = tc.refusal
			}
			tc.host.use(t)
			project := tc.host.config(t)
			var stdout, stderr bytes.Buffer

			code := run([]string{"task", "run", "43", "-config", project}, &stdout, &stderr)

			if tc.notOK && code == exitOK {
				t.Errorf("crewflow task run = 0, want not 0:\n%s", stdout.String())
			}
			for _, want := range tc.want {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("crewflow task run wrote %q, want it to mention %q", stdout.String(), want)
				}
			}
		})
	}
}

// TestRunTaskRefusedATaskThatIsNotReady is the second and third case of the manual
// check: a task nobody filled in and a risky task nobody approved are both refused
// before anything is created, and the refusal names what is missing.
func TestRunTaskRefusedATaskThatIsNotReady(t *testing.T) {
	cases := []struct {
		name string
		task forge.Task
		want string
	}{
		{
			name: "a section nobody wrote",
			task: func() forge.Task {
				t := taskOf(43)
				t.Body = strings.Replace(wholeTask, "## How to check it yourself\n\n1. Run it on a task that is not there.\n", "", 1)
				return t
			}(),
			want: "How to check it yourself",
		},
		{
			name: "a risky task nobody approved",
			task: func() forge.Task {
				t := taskOf(43)
				t.Labels = []string{"risky"}
				return t
			}(),
			want: "approved",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host := &host{task: tc.task}
			host.use(t)
			project := host.config(t)
			var stdout, stderr bytes.Buffer

			code := run([]string{"task", "run", "43", "-config", project}, &stdout, &stderr)

			if code != exitFailure {
				t.Fatalf("crewflow task run = %d, want %d", code, exitFailure)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Errorf("crewflow task run wrote %q to stderr, want it to name %q", stderr.String(), tc.want)
			}
			if len(host.started) != 0 {
				t.Errorf("the executor was started %d times, want nothing to be started", len(host.started))
			}
		})
	}
}

// TestRunTaskThatIsNotThere is the first case of the manual check: a number the
// tracker does not know is an error that says so, and nothing is created.
func TestRunTaskThatIsNotThere(t *testing.T) {
	host := &host{noTask: fmt.Errorf("could not find issue 999")}
	host.use(t)
	project := host.config(t)
	var stdout, stderr bytes.Buffer

	code := run([]string{"task", "run", "999", "-config", project}, &stdout, &stderr)

	if code != exitFailure {
		t.Fatalf("crewflow task run = %d, want %d", code, exitFailure)
	}
	if !strings.Contains(stderr.String(), "could not find issue 999") {
		t.Errorf("crewflow task run wrote %q to stderr, want what the tracker said", stderr.String())
	}
	if len(host.started) != 0 {
		t.Errorf("the executor was started %d times, want nothing to be started", len(host.started))
	}
	if entries, _ := os.ReadDir(host.home); len(entries) != 0 {
		t.Errorf("the run left %v in the home of crewflow, want nothing", entries)
	}
}

// TestRunTaskJSON checks the outcome for the orchestrator: the same run as the text
// shows, in a shape a program can read.
func TestRunTaskJSON(t *testing.T) {
	host := &host{opened: true, task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	var stdout, stderr bytes.Buffer

	code := run([]string{"task", "run", "43", "-config", project, "-json"}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow task run -json = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	var result taskrun.Result
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("crewflow task run -json wrote %q, which is not JSON: %v", stdout.String(), err)
	}
	if result.Task != 43 || result.Outcome != taskrun.ChangeRequestOpened {
		t.Errorf("the result = %+v, want task 43 that opened its change request", result)
	}
	if result.Branch != "crewflow/43-the-run-of-a-task" {
		t.Errorf("the branch of the result = %q, want the one of the task", result.Branch)
	}
	if result.ChangeRequest == nil || result.ChangeRequest.Number != 44 {
		t.Errorf("the change request of the result = %+v, want the one the host knows about", result.ChangeRequest)
	}
	if result.Journal == "" || result.EndedAt.Before(result.StartedAt) {
		t.Errorf("the result = %+v, want the journal of the run and its times", result)
	}
}

// TestRunTaskContinue walks what a continuation of a run is: the same session in
// the same worktree, the second attempt of the task, and the message the
// orchestrator sent.
func TestRunTaskContinue(t *testing.T) {
	host := &host{opened: true, task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	var stdout, stderr bytes.Buffer

	if code := run([]string{"task", "run", "43", "-config", project}, &stdout, &stderr); code != exitOK {
		t.Fatalf("the first run = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()

	code := run([]string{"task", "run", "43", "-config", project, "-continue", "add a test of the timeout"}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("the second run = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	if !host.continuedIn("ses_7fKq2") {
		t.Errorf("the executor was run with %v, want it to go on in the session of the first run", host.started)
	}
	if !strings.Contains(stdout.String(), "attempt 2") {
		t.Errorf("crewflow task run -continue wrote %q, want it to say that this is the second attempt", stdout.String())
	}
}

// TestRunTaskSaysItWentOnByItself: the run that crewflow answered by itself is said in
// the report of the task, because the orchestrator reads whether it has to continue
// the task by hand, and a run nobody asked for has to be visible as one
// (docs/DESIGN.md §7a).
func TestRunTaskSaysItWentOnByItself(t *testing.T) {
	host := &host{task: taskOf(43), refusal: theRefusalToTmp}
	host.use(t)
	project := host.config(t)
	var stdout, stderr bytes.Buffer

	code := run([]string{"task", "run", "43", "-config", project}, &stdout, &stderr)

	if code == exitOK {
		t.Errorf("crewflow task run = 0, want not 0 after two refusals of the same habit:\n%s", stdout.String())
	}
	if !host.continuedIn("ses_7fKq2") {
		t.Errorf("the executor was run with %v, want it to go on by itself in the session of the first run", host.started)
	}
	for _, want := range []string{"attempt 2", "went on by itself", "tmp"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("crewflow task run wrote %q, want it to mention %q", stdout.String(), want)
		}
	}
}

// TestRunTaskKeepsEveryFileInItsOwnFolders is the net under the tests: a run leaves
// a journal, a state and a worktree behind, and every one of them has to be under
// the home crewflow was given and the root of worktrees the project named. A command
// that resolved the home of the machine instead would write into the home of the
// person who runs the tests, and this is the test that says so.
func TestRunTaskKeepsEveryFileInItsOwnFolders(t *testing.T) {
	host := &host{opened: true, task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	home := testHome()
	var stdout, stderr bytes.Buffer

	if code := run([]string{"task", "run", "43", "-config", project, "-json"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("crewflow task run -json = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	var result taskrun.Result
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("crewflow task run -json wrote %q, which is not JSON: %v", stdout.String(), err)
	}

	under := filepath.Join(home, ".crewflow")
	for _, path := range []string{result.Journal, result.ErrorJournal} {
		if !strings.HasPrefix(path, under) {
			t.Errorf("the run left %q, want it under the home of the test %q", path, under)
		}
	}
	if !strings.HasPrefix(result.Worktree, host.worktrees) {
		t.Errorf("the run made the worktree %q, want it under the root the project named %q", result.Worktree, host.worktrees)
	}
	state := filepath.Join(under, "state", "naghuale-crewflow", "43.json")
	if _, err := os.Stat(state); err != nil {
		t.Errorf("the state of the task is not at %s: %v", state, err)
	}
}

// TestRunTaskCheck walks what a check of a task answers: a task that is ready, a task
// nobody filled in, and a risky task nobody approved. Nothing is created for either
// answer, which is the whole point of a check.
func TestRunTaskCheck(t *testing.T) {
	cases := []struct {
		name string
		task forge.Task
		// wantCode is the code of the command: zero for a task that may be run and
		// one for one that may not.
		wantCode int
		want     string
	}{
		{
			name:     "a task that is ready",
			task:     taskOf(43),
			wantCode: exitOK,
			want:     "ready",
		},
		{
			name: "a section nobody wrote",
			task: func() forge.Task {
				t := taskOf(43)
				t.Body = strings.Replace(wholeTask, "## How to check it yourself\n\n1. Run it on a task that is not there.\n", "", 1)
				return t
			}(),
			wantCode: exitFailure,
			want:     "How to check it yourself",
		},
		{
			name: "a risky task nobody approved",
			task: func() forge.Task {
				t := taskOf(43)
				t.Labels = []string{"risky"}
				return t
			}(),
			wantCode: exitFailure,
			want:     "approved",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host := &host{task: tc.task}
			host.use(t)
			project := host.config(t)
			var stdout, stderr bytes.Buffer

			code := run([]string{"task", "check", "43", "-config", project}, &stdout, &stderr)

			if code != tc.wantCode {
				t.Errorf("crewflow task check = %d, want %d (stdout: %q, stderr: %q)",
					code, tc.wantCode, stdout.String(), stderr.String())
			}
			if !strings.Contains(stdout.String(), tc.want) {
				t.Errorf("crewflow task check wrote %q, want it to mention %q", stdout.String(), tc.want)
			}
			if len(host.started) != 0 {
				t.Errorf("the executor was started %d times by a check, want nothing to be started", len(host.started))
			}
			// A check is a question, and a question that left anything behind would
			// already be a run of the task.
			if entries, _ := os.ReadDir(host.home); len(entries) != 0 {
				t.Errorf("the check left %v in the home of crewflow, want nothing", entries)
			}
			if _, err := os.Stat(host.worktrees); !os.IsNotExist(err) {
				t.Errorf("the check made the root of the worktrees: %v", err)
			}
		})
	}
}

// TestRunTaskCheckJSON is the answer of a check for the orchestrator: the same as the
// text shows, in a shape a program can read.
func TestRunTaskCheckJSON(t *testing.T) {
	host := &host{task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	var stdout, stderr bytes.Buffer

	code := run([]string{"task", "check", "43", "-config", project, "-json"}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow task check -json = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	var readiness task.Readiness
	if err := json.Unmarshal(stdout.Bytes(), &readiness); err != nil {
		t.Fatalf("crewflow task check -json wrote %q, which is not JSON: %v", stdout.String(), err)
	}
	if readiness.Number != 43 || !readiness.Ready || len(readiness.Missing) != 0 {
		t.Errorf("the answer = %+v, want task 43 that is ready", readiness)
	}
}

// TestRunTaskCheckOfATaskThatIsNotThere: a number the tracker does not know is an
// error that says so, and nothing is created.
func TestRunTaskCheckOfATaskThatIsNotThere(t *testing.T) {
	host := &host{noTask: fmt.Errorf("could not find issue 999")}
	host.use(t)
	project := host.config(t)
	var stdout, stderr bytes.Buffer

	code := run([]string{"task", "check", "999", "-config", project}, &stdout, &stderr)

	if code != exitFailure {
		t.Fatalf("crewflow task check = %d, want %d", code, exitFailure)
	}
	if !strings.Contains(stderr.String(), "could not find issue 999") {
		t.Errorf("crewflow task check wrote %q to stderr, want what the tracker said", stderr.String())
	}
	if entries, _ := os.ReadDir(host.home); len(entries) != 0 {
		t.Errorf("the check left %v in the home of crewflow, want nothing", entries)
	}
}

// TestRunTaskSaysWhoseNameTheExecutorWorkedUnder: whose powers a run had is the first
// question about it, and every place a person looks for the answer says it — the
// report of a run, the watch of a run in another terminal and the list of the runs of
// the project in a column of its own (docs/DESIGN.md §7i).
func TestRunTaskSaysWhoseNameTheExecutorWorkedUnder(t *testing.T) {
	host := &host{opened: true, task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	var stdout, stderr bytes.Buffer

	if code := run([]string{"task", "run", "43", "-config", project}, &stdout, &stderr); code != exitOK {
		t.Fatalf("the run = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	report := stdout.String()
	stdout.Reset()
	stderr.Reset()

	if !strings.HasPrefix(report, "executor: owner") {
		t.Errorf("the report of the run is %q, want it to start with whose name it went under", firstLineOf(report))
	}
	// A watch in another terminal and a list of the runs of the project say it too: a
	// person who did not watch the run itself has nothing else to learn the mode of it
	// from, and a list has a column for it.
	steps := []struct {
		args   []string
		what   string
		column string
	}{
		{[]string{"task", "run", "43", "-config", project, "-continue", "and a test of the timeout"},
			"the report of the second run", "executor: owner"},
		{[]string{"task", "watch", "43", "-config", project}, "the watch", "executor: owner"},
		{[]string{"task", "list", "-config", project}, "the list", "EXECUTOR"},
	}
	for _, step := range steps {
		stdout.Reset()
		stderr.Reset()
		if code := run(step.args, &stdout, &stderr); code != exitOK {
			t.Fatalf("%s = %d, want %d (stderr: %q)", step.what, code, exitOK, stderr.String())
		}
		if !strings.Contains(stdout.String(), step.column) {
			t.Errorf("%s wrote %q, want it to mention %q", step.what, stdout.String(), step.column)
		}
	}
	// The agent that ran the task and the account it went under are one question about
	// a run and one column of the list: a person has to know which agent wrote a run
	// before they know whose run it was, and the list says both in one place
	// (docs/DESIGN.md §7i).
	if got := listColumn(t, stdout.String(), "EXECUTOR"); got != "opencode · owner" {
		t.Errorf("the list of the runs has %q under the column of the executor, want the agent and the name it went under", got)
	}
}

// columnsOfAList are the cells of a line of a list of the runs: a table lines its
// columns up with spaces, and a title is a title whatever spaces are in it.
var columnsOfAList = regexp.MustCompile(`\s{2,}`)

// listColumn is the cell of the list under the column with that name, for a test that
// is about the column of the mode of the run and not about the whole table. The table of
// a list is under the block of the queue of attention wherever that block is (§6a), so the
// line of the names is what says where the table begins.
func listColumn(t *testing.T, list, column string) string {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(list), "\n")
	for at, line := range lines {
		cells := columnsOfAList.Split(strings.TrimSpace(line), -1)
		if at+1 >= len(lines) || !slices.Contains(cells, column) {
			continue
		}
		under := columnsOfAList.Split(strings.TrimSpace(lines[at+1]), -1)
		if at := slices.Index(cells, column); at < len(under) {
			return under[at]
		}
	}
	t.Fatalf("the list has no column %q:\n%s", column, list)
	return ""
}

// firstLineOf is the first line of a text, for a test that is about where a report
// starts and not about all of it.
func firstLineOf(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line
}

// TestRunTaskWatchShowsWhatTheRunWrote: a watch in another terminal is the journal of
// the last attempt of a task, read through the profile of the run, and it is over as
// soon as the run is.
func TestRunTaskWatchShowsWhatTheRunWrote(t *testing.T) {
	host := &host{opened: true, task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	var stdout, stderr bytes.Buffer

	if code := run([]string{"task", "run", "43", "-config", project}, &stdout, &stderr); code != exitOK {
		t.Fatalf("the run = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()

	code := run([]string{"task", "watch", "43", "-config", project}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow task watch = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	for _, want := range []string{
		"task 43, attempt 1", "pr-opened", "done",
		filepath.Join(host.home, "runs", "naghuale-crewflow", "43-1.jsonl"),
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("crewflow task watch wrote %q, want it to mention %q", stdout.String(), want)
		}
	}
	if stderr.Len() != 0 {
		t.Errorf("crewflow task watch wrote %q to stderr, want nothing", stderr.String())
	}
}

// TestRunTaskWatchOfATaskThatWasNeverRun: there is no journal to show, and a person
// is told that instead of being shown nothing.
func TestRunTaskWatchOfATaskThatWasNeverRun(t *testing.T) {
	host := &host{task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	var stdout, stderr bytes.Buffer

	code := run([]string{"task", "watch", "43", "-config", project}, &stdout, &stderr)

	if code != exitFailure {
		t.Fatalf("crewflow task watch = %d, want %d", code, exitFailure)
	}
	if !strings.Contains(stderr.String(), "never run") {
		t.Errorf("crewflow task watch wrote %q to stderr, want it to say that the task was never run", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("crewflow task watch wrote %q to stdout, want nothing", stdout.String())
	}
}

// TestRunTaskListShowsTheRunsOfTheProject: one command is what a person asks to see
// what crewflow has been running on this project, and the answer says which project it
// is, one line per task with the outcome of its last try, who ran it and the change
// request it opened.
func TestRunTaskListShowsTheRunsOfTheProject(t *testing.T) {
	host := &host{opened: true, task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"task", "run", "43", "-config", project}, &stdout, &stderr); code != exitOK {
		t.Fatalf("the run = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()

	code := run([]string{"task", "list", "-config", project}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow task list = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	for _, want := range []string{
		"naghuale/crewflow · main · 1 task", "43", taskOf(43).Title, "pr-opened", "#44", "opencode · owner",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("crewflow task list wrote %q, want it to mention %q", stdout.String(), want)
		}
	}
	if strings.Contains(stdout.String(), "https://github.com/naghuale/crewflow/pull/44") {
		t.Errorf("crewflow task list wrote %q, want the number of the change request and not its link", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("crewflow task list wrote %q to stderr, want nothing", stderr.String())
	}
}

// TestRunTaskListJSON is the same list for the orchestrator: an array of the tasks that
// were run, with the project, the agent, the run and the change request of each of them
// and the length of the last try in seconds.
func TestRunTaskListJSON(t *testing.T) {
	host := &host{opened: true, task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"task", "run", "43", "-config", project}, &stdout, &stderr); code != exitOK {
		t.Fatalf("the run = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()

	code := run([]string{"task", "list", "-config", project, "-json"}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow task list -json = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	// The answer is the canonical document of §6a: the version of the format and the
	// moment of it are in the root, and the records of the runs are under `runs`.
	document := documentOf(t, stdout.Bytes())
	if document.Repo != "naghuale/crewflow" || document.Branch != "main" || document.Total != 1 {
		t.Errorf("the document is of %q on %q with %d tasks, want naghuale/crewflow on main with one",
			document.Repo, document.Branch, document.Total)
	}
	var entries []struct {
		Task            int     `json:"task"`
		Title           string  `json:"title"`
		Attempts        int     `json:"attempts"`
		Outcome         string  `json:"outcome"`
		DurationSeconds float64 `json:"duration_seconds"`
		Repo            string  `json:"repo"`
		Executor        string  `json:"executor"`
		Run             string  `json:"run"`
		Change          *struct {
			Number int    `json:"number"`
			URL    string `json:"url"`
		} `json:"change"`
	}
	if err := json.Unmarshal(documentRuns(t, stdout.Bytes()), &entries); err != nil {
		t.Fatalf("crewflow task list -json wrote %q, which is not a list: %v", stdout.String(), err)
	}
	if len(entries) != 1 {
		t.Fatalf("the list holds %+v, want the one task that was run", entries)
	}
	entry := entries[0]
	if entry.Task != 43 || entry.Title != taskOf(43).Title || entry.Outcome != "pr-opened" {
		t.Errorf("the entry = %+v, want the task 43 that opened its change request", entry)
	}
	if entry.Attempts != 1 || entry.DurationSeconds < 0 {
		t.Errorf("the entry = %+v, want one try and a length of it", entry)
	}
	if entry.Repo != "naghuale/crewflow" || entry.Executor != "opencode" || entry.Run != "43-1" {
		t.Errorf("the entry = %+v, want the project, the agent and the run of the task", entry)
	}
	if entry.Change == nil || entry.Change.Number != 44 ||
		entry.Change.URL != "https://github.com/naghuale/crewflow/pull/44" {
		t.Errorf("the change request of the entry = %+v, want the one the run opened", entry.Change)
	}
}

// TestRunTaskListOfAProjectThatWasNeverRun: a project where crewflow has run nothing
// has no state at all, and a person who asks is told that instead of being shown an
// error — under the name of the project, because a list of nothing is a list of a
// project.
func TestRunTaskListOfAProjectThatWasNeverRun(t *testing.T) {
	host := &host{task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	var stdout, stderr bytes.Buffer

	code := run([]string{"task", "list", "-config", project}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow task list = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	if want := "naghuale/crewflow · main · 0 tasks\nno runs yet\n"; stdout.String() != want {
		t.Errorf("crewflow task list wrote %q, want %q", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Errorf("crewflow task list wrote %q to stderr, want nothing", stderr.String())
	}
}

// TestRunTaskListSaysWhatElseRunsOnTheMachine: the owner of a machine runs one project
// at a time and gets no news of the other, and the runs of the other projects are what
// a person asks `task list` about most often.
func TestRunTaskListSaysWhatElseRunsOnTheMachine(t *testing.T) {
	host := &host{opened: true, task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	_, other := host.otherProject(t)
	var stdout, stderr bytes.Buffer

	if code := run([]string{"task", "run", "43", "-config", project}, &stdout, &stderr); code != exitOK {
		t.Fatalf("the run of this project = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	host.task = taskOf(50)
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"task", "run", "50", "-config", other}, &stdout, &stderr); code != exitOK {
		t.Fatalf("the run of the other project = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()

	if code := run([]string{"task", "list", "-config", project}, &stdout, &stderr); code != exitOK {
		t.Fatalf("crewflow task list = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}

	for _, want := range []string{
		"naghuale/crewflow · main · 1 task",
		// The other project of the machine is said by its name alone: the owner of
		// this one is in the heading of every list of it, and saying it again in the
		// line about the projects beside it is noise.
		"also: telecli (1 pr-opened)",
		"crewflow task list -all for everything",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("crewflow task list wrote %q, want it to mention %q", stdout.String(), want)
		}
	}
	// The list itself is of the project of the folder, and what the other project is
	// doing is said under the table and not in it.
	if table, _, _ := strings.Cut(stdout.String(), "\n\n"); strings.Contains(table, "telecli") {
		t.Errorf("crewflow task list wrote %q, want the runs of the other project under the table and not in it", table)
	}
}

// TestRunTaskListOfEveryProjectOnTheMachine: -all is what a person asks from anywhere
// and it is every run of every project, with the project as the first column of it.
func TestRunTaskListOfEveryProjectOnTheMachine(t *testing.T) {
	host := &host{opened: true, task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	_, other := host.otherProject(t)
	var stdout, stderr bytes.Buffer

	if code := run([]string{"task", "run", "43", "-config", project}, &stdout, &stderr); code != exitOK {
		t.Fatalf("the run of this project = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	host.task = taskOf(50)
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"task", "run", "50", "-config", other}, &stdout, &stderr); code != exitOK {
		t.Fatalf("the run of the other project = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()

	// The folder crewflow was called in is of no project at all: -all is asked from
	// any folder, and the state of every project is in one place.
	t.Chdir(t.TempDir())

	code := run([]string{"task", "list", "-all"}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow task list -all = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("crewflow task list -all wrote\n%s\nwant a line of names and a line of a run of each project",
			stdout.String())
	}
	if !strings.HasPrefix(lines[0], "REPO") {
		t.Errorf("the first column of a list of the whole machine is %q, want the project first", lines[0])
	}
	// Both the projects of the machine are of one owner, and the owner is said nowhere
	// but in the name of the column: a person who works on both of them reads a list of
	// them twice a day.
	for _, want := range []string{"crewflow", "telecli", "43", "50"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("crewflow task list -all wrote %q, want it to mention %q", stdout.String(), want)
		}
	}
	if unwanted := "also:"; strings.Contains(stdout.String(), unwanted) {
		t.Errorf("crewflow task list -all wrote %q, want nothing about %q: all of it is there", stdout.String(), unwanted)
	}
}

// TestRunTaskListAllJSONIsOneDocumentOfTheMachine: `-all` спрашивается из любой папки и
// показывает прогоны всех проектов машины. В `-json` это один документ формата §6a с
// одним списком записей, и у каждой записи назван её проект: программа, читающая два
// проекта разом, читает один формат и не гадает, из какого пришла запись. Проекта у
// документа нет — он о всей машине, и очередь в нём нет: у каждого проекта свой хостинг
// (docs/DESIGN.md §6, §6a).
func TestRunTaskListAllJSONIsOneDocumentOfTheMachine(t *testing.T) {
	host := &host{opened: true, task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	_, other := host.otherProject(t)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"task", "run", "43", "-config", project}, &stdout, &stderr); code != exitOK {
		t.Fatalf("the run of this project = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	host.task = taskOf(50)
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"task", "run", "50", "-config", other}, &stdout, &stderr); code != exitOK {
		t.Fatalf("the run of the other project = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()

	// The folder crewflow was called in is of no project at all: -all is asked from
	// any folder, and the state of every project is in one place.
	t.Chdir(t.TempDir())

	if code := run([]string{"task", "list", "-all", "-json"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("crewflow task list -all -json = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}

	document := documentOf(t, stdout.Bytes())
	if document.Repo != "" {
		t.Errorf("the document is of the project %q, want no one: a list of the whole machine is of no project", document.Repo)
	}
	if len(document.Attention) != 0 {
		t.Errorf("the document holds the attention %+v, want none: a queue is the answer of one project about one host",
			document.Attention)
	}
	// Both projects of the machine are in one list of records, and each record says
	// which project it is of.
	said := stdout.String()
	for _, want := range []string{`"repo": "naghuale/crewflow"`, `"repo": "naghuale/telecli"`,
		`"task": 43`, `"task": 50`} {
		if !strings.Contains(said, want) {
			t.Errorf("crewflow task list -all -json wrote a document without %s, want the runs of every project in one answer",
				want)
		}
	}
	if len(document.Runs) != 2 {
		t.Errorf("the document holds %d records, want the one run of each of the two projects", len(document.Runs))
	}
	for _, one := range entriesOfAnswer(t, stdout.Bytes()) {
		if one.state != "" {
			t.Errorf("the record of the task %d holds the attention %q, want none: a list of the whole machine has no queue",
				one.task, one.state)
		}
	}
}

// TestRunTaskListAllWithRepoIsAWrongCall: -all is of every project on the machine and
// -repo is of the one of a checkout, and a call that names both says two things at
// once that are not the same thing.
func TestRunTaskListAllWithRepoIsAWrongCall(t *testing.T) {
	host := &host{task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	repo, _ := host.otherProject(t)
	var stdout, stderr bytes.Buffer

	code := run([]string{"task", "list", "-all", "-repo", repo, "-config", project}, &stdout, &stderr)

	if code != exitUsage {
		t.Fatalf("crewflow task list -all -repo = %d, want %d", code, exitUsage)
	}
	if stdout.Len() != 0 {
		t.Errorf("crewflow task list -all -repo wrote %q, want no list at all", stdout.String())
	}
	if !strings.Contains(stderr.String(), "-all") || !strings.Contains(stderr.String(), "-repo") {
		t.Errorf("crewflow task list -all -repo wrote %q to stderr, want it to say which of the two is meant", stderr.String())
	}
}

// TestRunTaskListStartsNoProgram: a list is a question about the state crewflow kept,
// and it asks it of the machine and of nobody else — no tracker, no host, no network,
// no program of a person (docs/DESIGN.md §6, §7). A machine that fails on any call is
// what proves it.
func TestRunTaskListStartsNoProgram(t *testing.T) {
	host := &host{opened: true, task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"task", "run", "43", "-config", project}, &stdout, &stderr); code != exitOK {
		t.Fatalf("the run = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	// Everything that could start a program of a person now fails when it is touched.
	// The machine of the question whether a run is still going is the one thing a list
	// does ask, and it was given a machine of its own by the host of the test.
	taskRoles = func(config.Config, forge.Env) (forge.Set, error) {
		t.Error("crewflow task list asked for the roles of a project, want the state and nothing else")
		return forge.Set{}, nil
	}
	taskRunEnv = func(string) taskrun.Env {
		t.Error("crewflow task list asked for the machine a task is run on, want the state and nothing else")
		return taskrun.Env{}
	}
	t.Chdir(t.TempDir())

	for _, args := range [][]string{
		{"task", "list", "-config", project},
		{"task", "list", "-config", project, "-json"},
		{"task", "list", "-all"},
		{"task", "list", "-all", "-json"},
	} {
		stdout.Reset()
		stderr.Reset()

		if code := run(args, &stdout, &stderr); code != exitOK {
			t.Errorf("run(%v) = %d, want %d (stderr: %q)", args, code, exitOK, stderr.String())
			continue
		}
		if !strings.Contains(stdout.String(), "pr-opened") {
			t.Errorf("run(%v) wrote %q, want the list of the runs of the machine", args, stdout.String())
		}
	}
	if len(host.started) != 1 {
		t.Errorf("the lists started %d programs, want the one run of the test and nothing else", len(host.started))
	}
}

// TestRunTaskListWithoutATerminal: a list goes into a file and through a pipe as often
// as it goes onto a screen, and a table of a file that draws over the terminal of
// whoever reads it is not a table anybody reads.
func TestRunTaskListWithoutATerminal(t *testing.T) {
	host := &host{opened: true, task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"task", "run", "43", "-config", project}, &stdout, &stderr); code != exitOK {
		t.Fatalf("the run = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	host.task = taskOf(44)
	host.task.Title = "a title that paints\x1b[2Jover the screen\r"
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"task", "run", "44", "-config", project}, &stdout, &stderr); code != exitOK {
		t.Fatalf("the second run = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()

	// A pipe is not a terminal: a list written into one is a file, and this is the
	// same table with no colour and no command to the terminal in it.
	if code := run([]string{"task", "list", "-config", project}, &stdout, &stderr); code != exitOK {
		t.Fatalf("crewflow task list = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}

	list := stdout.String()
	for _, letter := range list {
		if letter < ' ' && letter != '\n' {
			t.Fatalf("crewflow task list wrote the control character %q, want a table of letters:\n%s", letter, list)
		}
	}
	if strings.Contains(list, "\x1b") {
		t.Fatalf("crewflow task list wrote a terminal command of its own:\n%q", list)
	}
	if !strings.Contains(list, "a title that paints") {
		t.Errorf("crewflow task list wrote %q, want the title of the task in it", list)
	}
}

// TestRunTaskListWithNoColourIsAFileOfWords: a person who set NO_COLOR, and a terminal
// that says it takes no colour with TERM=dumb, are answered in the plain letters of the
// list. The list is the same list either way, and the only thing taken out of it is the
// colour.
func TestRunTaskListWithNoColourIsAFileOfWords(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
	}{
		{name: "a person who asked for no colour", env: map[string]string{"NO_COLOR": "1"}},
		{name: "a terminal that takes no colour", env: map[string]string{"TERM": "dumb"}},
		{name: "both at once", env: map[string]string{"NO_COLOR": "1", "TERM": "dumb"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host := &host{opened: true, task: taskOf(43)}
			host.use(t)
			for name, value := range tc.env {
				t.Setenv(name, value)
			}
			project := host.config(t)
			var stdout, stderr bytes.Buffer
			if code := run([]string{"task", "run", "43", "-config", project}, &stdout, &stderr); code != exitOK {
				t.Fatalf("the run = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
			}
			stdout.Reset()
			stderr.Reset()

			// The screen of a test is the buffer the test writes into, which is a file
			// and not a terminal of a person: the colours of a list are asked of the
			// terminal it goes to, and there is none here whatever the environment says.
			taskScreen = func(w io.Writer, at time.Time) taskrun.Screen {
				return taskrun.Screen{At: at, Terminal: true, Columns: 120, Painted: wantsColour(os.Getenv)}
			}
			stdout.Reset()
			stderr.Reset()
			if code := run([]string{"task", "list", "-config", project}, &stdout, &stderr); code != exitOK {
				t.Fatalf("crewflow task list = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
			}
			if strings.Contains(stdout.String(), "\x1b") {
				t.Errorf("crewflow task list with %v wrote a colour:\n%q", tc.env, stdout.String())
			}
			if !strings.Contains(stdout.String(), "pr-opened") {
				t.Errorf("crewflow task list with %v wrote %q, want the list itself", tc.env, stdout.String())
			}
		})
	}
}

// TestRunTaskListOfAnotherCheckout: -repo is a checkout, the way it is in `task run`, and
// the project whose runs are shown is the one the file of the project in it names. Two
// projects on one machine keep their runs apart, and a person who points crewflow at
// the other folder sees the runs of that project and not the ones of this one.
func TestRunTaskListOfAnotherCheckout(t *testing.T) {
	host := &host{opened: true, task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	checkout, other := host.otherProject(t)
	var stdout, stderr bytes.Buffer

	// A run in each of the two projects: the state of a task is kept by the name the
	// file of the project has, and the number of the task is what tells them apart.
	if code := run([]string{"task", "run", "43", "-config", other}, &stdout, &stderr); code != exitOK {
		t.Fatalf("the run of the other project = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	host.task = taskOf(50)
	if code := run([]string{"task", "run", "50", "-config", project}, &stdout, &stderr); code != exitOK {
		t.Fatalf("the run of this project = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}

	cases := []struct {
		name string
		args []string
		// task is the task the list is to show, and apart the task of the other
		// project, which is not to be in the list at all.
		task  string
		apart string
	}{
		{
			name: "the checkout that was named", args: []string{"-repo", checkout},
			task: "43", apart: "50",
		},
		{
			name: "the file that was named", args: []string{"-config", other},
			task: "43", apart: "50",
		},
		{
			// The file that was named is the one that says which project this is: -repo
			// says where that file is looked for, and a file that was named is not
			// looked for anywhere.
			name: "the file that was named over the checkout",
			args: []string{"-config", project, "-repo", checkout},
			task: "50", apart: "43",
		},
		{
			name: "this project", args: []string{"-config", project},
			task: "50", apart: "43",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout.Reset()
			stderr.Reset()

			code := run(append([]string{"task", "list"}, tc.args...), &stdout, &stderr)

			if code != exitOK {
				t.Fatalf("crewflow task list %v = %d, want %d (stderr: %q)", tc.args, code, exitOK, stderr.String())
			}
			if !strings.Contains(stdout.String(), "\n"+tc.task+"  ") {
				t.Errorf("crewflow task list %v wrote %q, want the runs of the task %s", tc.args, stdout.String(), tc.task)
			}
			if strings.Contains(stdout.String(), "\n"+tc.apart+"  ") {
				t.Errorf("crewflow task list %v wrote %q, want the runs of one project and not of the other",
					tc.args, stdout.String())
			}
		})
	}
}

// TestRunTaskListOfSomethingThatIsNotACheckout: a path that is not a folder of a
// checkout is a wrong call of the caller, and it is said so. A list with nothing in it
// would be a lie here: the runs of that project are there, and crewflow was pointed at
// the wrong place.
func TestRunTaskListOfSomethingThatIsNotACheckout(t *testing.T) {
	host := &host{task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	file := writeConfig(t, taskConfig)
	cases := []struct {
		name string
		repo string
	}{
		{name: "a file where a checkout was named", repo: file},
		{name: "nothing at all", repo: filepath.Join(t.TempDir(), "nowhere")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := run([]string{"task", "list", "-config", project, "-repo", tc.repo}, &stdout, &stderr)

			if code != exitFailure {
				t.Fatalf("crewflow task list -repo %s = %d, want %d", tc.repo, code, exitFailure)
			}
			if stdout.Len() != 0 {
				t.Errorf("crewflow task list -repo %s wrote %q, want no list at all", tc.repo, stdout.String())
			}
			if !strings.Contains(stderr.String(), tc.repo) {
				t.Errorf("crewflow task list -repo %s wrote %q to stderr, want it to name the path", tc.repo, stderr.String())
			}
		})
	}
}

// TestRunTaskListOfAStateItCannotRead: one file crewflow cannot read does not take the
// list down, and the file is named in the answer.
func TestRunTaskListOfAStateItCannotRead(t *testing.T) {
	host := &host{opened: true, task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"task", "run", "43", "-config", project}, &stdout, &stderr); code != exitOK {
		t.Fatalf("the run = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	broken := filepath.Join(host.home, "state", "naghuale-crewflow", "44.json")
	if err := os.MkdirAll(filepath.Dir(broken), 0o700); err != nil {
		t.Fatalf("make the folder of the state: %v", err)
	}
	if err := os.WriteFile(broken, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write a state crewflow cannot read: %v", err)
	}
	stdout.Reset()
	stderr.Reset()

	code := run([]string{"task", "list", "-config", project, "-json"}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow task list -json = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	// The state crewflow could not read is not part of the answer: it is named in the
	// root of the document and beside it in stderr, and the records of the runs it did
	// read are whole (docs/DESIGN.md §6a).
	document := documentOf(t, stdout.Bytes())
	if len(document.Unreadable) != 1 || document.Unreadable[0] != broken {
		t.Errorf("the document names the states it could not read as %+v, want only %q", document.Unreadable, broken)
	}
	if len(document.Runs) != 1 {
		t.Errorf("the document holds %d records, want the one run it did read", len(document.Runs))
	}
	if want := "not read: " + broken + "\n"; stderr.String() != want {
		t.Errorf("crewflow task list -json wrote %q to stderr, want %q", stderr.String(), want)
	}
}

// TestRunTaskCheckStalledSaysWhatStandsAndWritesItUnderTheTask is the command of the
// schedule of an orchestrator: a run that has shown nothing for longer than
// `[executor] stall_after` is answered with how long it has been standing, what it was
// doing, and one record under the task — and the next turn of the schedule, a minute
// later, says the same thing and writes nothing more (docs/DESIGN.md §6).
func TestRunTaskCheckStalledSaysWhatStandsAndWritesItUnderTheTask(t *testing.T) {
	host := &host{opened: true, task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	standing := putRunThatStands(t, host)
	var stdout, stderr bytes.Buffer

	code := run([]string{"task", "check-stalled", "-config", project}, &stdout, &stderr)

	// A run that stands is a thing a person has to do something about, and the code of
	// the command says so whatever it managed to write under the task.
	if code != exitFailure {
		t.Fatalf("crewflow task check-stalled = %d, want %d (stderr: %q)", code, exitFailure, stderr.String())
	}
	for _, want := range []string{"task 43 (43-1)", "standing for 11m", "the executor of the run", "left under the task"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("crewflow task check-stalled wrote %q, want it to mention %q", stdout.String(), want)
		}
	}
	if stderr.Len() != 0 {
		t.Errorf("crewflow task check-stalled wrote %q to stderr, want nothing", stderr.String())
	}
	if len(host.records) != 1 {
		t.Fatalf("the host was asked %d times to write under the task, want once: %q", len(host.records), host.records)
	}
	for _, want := range []string{"43", "has shown nothing for 11m", "the executor of the run"} {
		if !strings.Contains(host.records[0], want) {
			t.Errorf("the record under the task is %q, want it to hold %q", host.records[0], want)
		}
	}

	// The next turn of the schedule: the same answer and no second record. A person who
	// reads a task is not to find a line under it every minute (§6).
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"task", "check-stalled", "-config", project}, &stdout, &stderr); code != exitFailure {
		t.Fatalf("the second crewflow task check-stalled = %d, want %d", code, exitFailure)
	}
	if !strings.Contains(stdout.String(), "standing for 11m") || strings.Contains(stdout.String(), "left under the task") {
		t.Errorf("the second turn wrote %q, want the same run standing and no record left", stdout.String())
	}
	if len(host.records) != 1 {
		t.Errorf("the host was asked %d times to write under the task, want once: %q", len(host.records), host.records)
	}

	// The executor writes again, the silence is over, and the task is told so once: the
	// end of an episode of silence is a fact about the task as much as its beginning.
	taskClock = func() time.Time { return standing.Add(13 * time.Minute) }
	putJournalAt(t, filepath.Join(host.home, "runs", "naghuale-crewflow", "43-1.jsonl"),
		`{"type":"text","sessionID":"ses_7fKq2","part":{"type":"text","text":"I did the work."}}`+"\n",
		standing.Add(13*time.Minute))
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"task", "check-stalled", "-config", project}, &stdout, &stderr); code != exitOK {
		t.Fatalf("crewflow task check-stalled after the run wrote again = %d, want %d (stderr: %q)",
			code, exitOK, stderr.String())
	}
	for _, want := range []string{"no longer standing", "left under the task"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("crewflow task check-stalled wrote %q, want it to mention %q", stdout.String(), want)
		}
	}
	if len(host.records) != 2 || !strings.Contains(host.records[1], "is working again") {
		t.Errorf("the records under the task are %q, want a second one about the run working again", host.records)
	}
}

// TestRunTaskCheckStalledJSON is the same answer for the schedule of a program: the
// length of the silence in seconds, the step of the run and whether the record under the
// task was left this time (docs/DESIGN.md §6).
func TestRunTaskCheckStalledJSON(t *testing.T) {
	host := &host{opened: true, task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	putRunThatStands(t, host)
	var stdout, stderr bytes.Buffer

	run([]string{"task", "check-stalled", "-config", project, "-json"}, &stdout, &stderr)

	var answer []struct {
		Task       int     `json:"task"`
		Run        string  `json:"run"`
		Stalled    bool    `json:"stalled"`
		StalledFor float64 `json:"stalled_for"`
		LastStep   string  `json:"last_step"`
		Said       bool    `json:"said"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &answer); err != nil {
		t.Fatalf("crewflow task check-stalled -json wrote %q, which is not an answer: %v", stdout.String(), err)
	}
	if len(answer) != 1 {
		t.Fatalf("the answer holds %+v, want the one run that stands", answer)
	}
	one := answer[0]
	if one.Task != 43 || one.Run != "43-1" || !one.Stalled || !one.Said {
		t.Errorf("the answer is %+v, want the run 43-1 standing with the record left", one)
	}
	if one.StalledFor != 660 || one.LastStep != "the executor of the run" {
		t.Errorf("the answer is %+v, want 660 seconds of silence and the step of the run", one)
	}
}

// TestRunTaskCheckStalledOfAProjectWhereNothingStands: a schedule that runs every minute
// has to be able to say that there is nothing to do, and its code says so (§6).
func TestRunTaskCheckStalledOfAProjectWhereNothingStands(t *testing.T) {
	host := &host{opened: true, task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"task", "run", "43", "-config", project}, &stdout, &stderr); code != exitOK {
		t.Fatalf("the run = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()

	code := run([]string{"task", "check-stalled", "-config", project}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow task check-stalled = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("crewflow task check-stalled wrote %q, want nothing at all: no run of this project stands", stdout.String())
	}
	if len(host.records) != 0 {
		t.Errorf("the host was asked %q, want nothing: there is nothing to write under a task", host.records)
	}
	// And `-json` of nothing is an empty list and not nothing at all: a program that
	// reads the answer has to be able to read it without asking whether it is there.
	stdout.Reset()
	if code := run([]string{"task", "check-stalled", "-config", project, "-json"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("crewflow task check-stalled -json = %d, want %d", code, exitOK)
	}
	if want := "[]\n"; stdout.String() != want {
		t.Errorf("crewflow task check-stalled -json wrote %q, want %q", stdout.String(), want)
	}
}

// TestRunTaskListSaysARunThatStands is the list a person looks at when nobody has
// noticed anything: a run that has shown nothing for longer than the silence of the
// project is on top with the length of the silence in the column of the outcome, and
// `-json` holds the step of the run beside it (docs/DESIGN.md §6).
func TestRunTaskListSaysARunThatStands(t *testing.T) {
	host := &host{opened: true, task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	putRunThatStands(t, host)
	var stdout, stderr bytes.Buffer

	code := run([]string{"task", "list", "-config", project}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow task list = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	for _, want := range []string{"43", "stalled 11m"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("crewflow task list wrote %q, want it to mention %q", stdout.String(), want)
		}
	}
	stdout.Reset()
	run([]string{"task", "list", "-config", project, "-json"}, &stdout, &stderr)
	for _, want := range []string{`"outcome": "stalled"`, `"stalled_for": 660`, `"last_step": "the executor of the run"`} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("crewflow task list -json wrote %q, want it to hold %q", stdout.String(), want)
		}
	}
}

// putRunThatStands is the state of a task whose run is going and has shown nothing for
// eleven minutes, with the clock of the machine eleven minutes on: what a list of runs
// and a check of the runs that stand are asked about when a run is standing (docs/DESIGN
// .md §6).
func putRunThatStands(t *testing.T, h *host) time.Time {
	t.Helper()
	started := time.Now().Add(-11 * time.Minute)
	taskClock = func() time.Time { return started.Add(11 * time.Minute) }
	taskMachine = proc.Env{Ask: func(_ string, args []string) (string, error) {
		// The machine says that the process of the run of the test is still there: a
		// list of runs asks about the process of a run and not about the file of it
		// (docs/DESIGN.md §7).
		if !slices.Contains(args, "-p") {
			return "", errors.New("ps: no such file or directory")
		}
		return started.Format("Mon Jan _2 15:04:05 2006"), nil
	}}
	journals := taskrun.JournalsOf(h.home, "naghuale-crewflow")
	state := taskrun.State{Number: 43, Title: "the run of a task", Branch: "crewflow/43-task", Profile: "opencode"}
	state = state.NextAttempt(taskrun.StartOf{
		Started:      started,
		Step:         "the executor of the run",
		Journal:      journals.JournalPath(43, 1),
		ErrorJournal: filepath.Join(h.home, "runs", "naghuale-crewflow", "43-1.err"),
		Executor:     "opencode",
		Process:      proc.Process{Pid: 4242, StartedAt: started},
		Identity:     taskrun.Identity{Mode: "owner", Description: "owner — the login gh naghuale (shared rights)"},
	})
	if err := taskrun.SaveState(journals.StatePath(43), state); err != nil {
		t.Fatalf("write the state of the task: %v", err)
	}
	return started
}

// putJournalAt is what an executor wrote into the journal of an attempt and the moment it
// wrote it: the sign of life of a run is the time of the last line of its journal
// (docs/DESIGN.md §6).
func putJournalAt(t *testing.T, path, text string, written time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("make the folder of the journal: %v", err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatalf("write the journal of the attempt: %v", err)
	}
	if err := os.Chtimes(path, written, written); err != nil {
		t.Fatalf("set the moment the journal was written: %v", err)
	}
}

// TestRunTaskCalledWrong walks what a script gets when crewflow is called wrong:
// the code of a wrong call, the usage, and no work.
func TestRunTaskCalledWrong(t *testing.T) {
	cases := [][]string{
		{"task", "run"},
		{"task", "run", "forty-three"},
		{"task", "run", "43", "-nope"},
		{"task", "run", "43", "extra"},
		{"task", "check"},
		{"task", "check", "forty-three"},
		{"task", "check", "43", "-nope"},
		{"task", "check", "43", "extra"},
		{"task", "watch"},
		{"task", "watch", "forty-three"},
		{"task", "watch", "43", "-nope"},
		{"task", "watch", "43", "extra"},
		{"task", "list", "-nope"},
		{"task", "list", "extra"},
		{"task", "fly", "43"},
		{"task"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			host := &host{task: taskOf(43)}
			host.use(t)
			project := host.config(t)
			var stdout, stderr bytes.Buffer

			code := run(append(args, "-config", project), &stdout, &stderr)

			if code != exitUsage {
				t.Errorf("run(%v) = %d, want %d", args, code, exitUsage)
			}
			if stdout.Len() != 0 {
				t.Errorf("run(%v) wrote %q to stdout, want nothing", args, stdout.String())
			}
			if !strings.Contains(stderr.String(), "Usage:") {
				t.Errorf("run(%v) wrote %q to stderr, want the usage", args, stderr.String())
			}
		})
	}
}

// TestRunTaskRepoFlag checks the folder the worktree is made out of: a person works
// in a folder that is not the repository, and a task is made out of the repository
// anyway.
func TestRunTaskRepoFlag(t *testing.T) {
	host := &host{opened: true, task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	repository := t.TempDir()
	var stdout, stderr bytes.Buffer

	if code := run([]string{"task", "run", "43", "-config", project, "-repo", repository}, &stdout, &stderr); code != exitOK {
		t.Fatalf("crewflow task run -repo = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}

	if host.repoDir != repository {
		t.Errorf("the worktree was made out of %q, want %q", host.repoDir, repository)
	}
}

// TestRunTaskWithoutConfig is the first thing a person sees in a folder that is not
// a project.
func TestRunTaskWithoutConfig(t *testing.T) {
	host := &host{task: taskOf(43)}
	host.use(t)
	var stdout, stderr bytes.Buffer

	code := run([]string{"task", "run", "43", "-config", filepath.Join(t.TempDir(), "crewflow.toml")}, &stdout, &stderr)

	if code != exitFailure {
		t.Fatalf("crewflow task run = %d, want %d", code, exitFailure)
	}
	if !strings.Contains(stderr.String(), "crewflow.toml") {
		t.Errorf("crewflow task run wrote %q to stderr, want it to name the file that is not there", stderr.String())
	}
}

// TestUsageMentionsTaskRun: a person who asks for help has to see what crewflow can
// do, and the usage is what they see.
func TestUsageMentionsTaskRun(t *testing.T) {
	var stdout, stderr bytes.Buffer

	if code := run([]string{"help"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("run(help) = %d, want 0", code)
	}
	for _, want := range []string{"task", "run", "check", "watch", "list", "doctor", "version"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("the usage does not mention %q:\n%s", want, stdout.String())
		}
	}
}

// taskConfig is a crewflow.toml of a project whose worktrees and executor are of the
// test, so that a test of the command never touches the folder of the person and
// never starts an agent.
const taskConfig = `
[project]
repo = "naghuale/crewflow"
language = "en"

[executor]
command = ["opencode", "run", "--dir", "{worktree}", "--format", "json", "{prompt}"]
timeout = "1h"

[worktrees]
root = "WORKTREES/{repo}"
`

// separateConfig is the crewflow.toml of a project whose orchestrator works under an account
// of the host of its own: the App of it is named by its number, and the login of that App is
// among the reviewers of the project. It is the mode this repository works in, and the one a
// second construction of the roles of a project gets wrong (§7i).
const separateConfig = `
[project]
repo = "naghuale/crewflow"
language = "en"

[executor]
command = ["opencode", "run", "{prompt}"]

[orchestrator]
mode = "separate"

[orchestrator.github_app]
app_id = 5140522

[merge]
reviewers = ["crewflow-orchestrator[bot]"]
owners = ["naghuale"]

[worktrees]
root = "WORKTREES/{repo}"
`

// taskOf is a whole task of a test.
func taskOf(number int) forge.Task {
	return forge.Task{
		Number: number,
		Title:  "the run of a task",
		Body:   wholeTask,
		State:  "open",
	}
}

// host is the tracker and the host of a test of the command: what task it holds,
// what the host knows about the branch, and the machine the run happens on.
type host struct {
	// task is what the tracker holds, and noTask what it says for a task it does
	// not have. tasks is the same for a project of several tasks, where every one of
	// them has its own answer (CL-007…CL-009, §6a).
	task   forge.Task
	tasks  map[int]forge.Task
	noTask error
	// opened is the change request of the branch, and changed is what git holds of
	// the files the run touched.
	opened  bool
	changed string
	// repoDir is the folder the worktree was made out of, which is what the
	// command was told to make it from.
	repoDir string
	// started is the runs of the executor, one line per run.
	started []string
	// refusal is what the executor says on the way out, which is how a run that was
	// refused a permission looks from the outside.
	refusal string
	// noIdentity is the refusal of a host that cannot hand out a token of an account of
	// its own: no key imported, no store to read it from, or a keychain of macOS whose
	// window of the system nobody answered (docs/DESIGN.md §7i).
	noIdentity error
	// home is the root of what crewflow keeps on this machine, worktrees the root
	// the worktrees of its tasks are made under, and git the folder of the repository
	// of the project as git names it: the local ignore of the scratch of a run is
	// written into it. head is what the worktree of a task stands at, which the point
	// a run was stopped at is checked against (§7i).
	home      string
	worktrees string
	git       string
	head      string
	// records are the lines crewflow left under the tasks of the project, in order: a
	// run that stands is written about under its task, and what a person reads there is
	// what a schedule of an orchestrator is for (docs.DESIGN.md §6).
	records []string
	// under is what the host holds written under the change request of the run, and
	// noSubject says that the host cannot name an account of its own — a host of a
	// project whose file names nobody it knows (docs.DESIGN.md §6a, §7i).
	under     []forge.Comment
	noSubject bool
	// merged says that the change request of the run is in the state of a change that went
	// in, and closed that the task of the test is closed: the two reactions of the host that
	// take a run out of the queue of attention (F-061, §6a, §7g).
	merged bool
	// changes are the change requests the host was asked about: очередь спрашивает
	// хостинг несколькими вопросами сразу, и замок нужен, чтобы их список не потерялся
	// (F-099, §6a).
	mu      sync.Mutex
	changes []int
	// states is where each change request of the project stands, by its number, for a
	// project of several changes — слитая, закрытая и открытая рядом (F-061, §6a).
	states map[int]string
	// app is the App the host of the test knows, and the login of every other App is
	// refused the way the host of GitHub refuses an App it was not given (§7i).
	app int64
}

// use makes the task command run on this host, and puts the machine back when the
// test is over. Every test gets a home of its own under TestMain's home, so that
// the journals and the state of one test of a task are not the state of the next
// one, and none of it is ever the home of the person the tests run on.
func (h *host) use(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		taskRoles = roles.New
		reviewRoles = roles.AsOrchestrator
		taskRunEnv = taskrun.System
		taskMachine = proc.System()
		taskClock = time.Now
	})
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	h.home = filepath.Join(home, ".crewflow")
	h.worktrees = filepath.Join(t.TempDir(), "worktrees")
	h.git = filepath.Join(t.TempDir(), "git")
	h.head = theHeadOfTheTest
	taskRoles = func(config.Config, forge.Env) (forge.Set, error) {
		return forge.Set{Tracker: h, Forge: h}, nil
	}
	// The queue of attention asks the host as the orchestrator of the project and not as
	// the executor of a run, so the roles of the orchestrator are a host of the test as
	// well: a command that reached the host of a person would read the tracker of a project
	// that is not the one of the test (docs/DESIGN.md §6a, §7i).
	reviewRoles = func(config.Config, forge.Env) (forge.Set, error) {
		return forge.Set{Tracker: h, Forge: h}, nil
	}
	taskRunEnv = func(home string) taskrun.Env {
		return taskrun.Env{
			Home:    home,
			Command: h.exec,
			Stream:  h.stream,
			Now:     time.Now,
			// A run of a test happens in the process of the test, and a list of runs
			// of a test never asks the machine the test runs on.
			Process: func() (proc.Process, bool) {
				return proc.Process{Pid: 4242, StartedAt: time.Now()}, true
			},
		}
	}
	taskMachine = proc.Env{Ask: func(string, []string) (string, error) {
		return "", errors.New("ps: no such file or directory")
	}}
	taskClock = time.Now
	taskScreen = screenOf
}

// testHome is the home of the machine the tests run on: a folder of TestMain, and
// never the home of the person who runs them.
func testHome() string {
	return os.Getenv("HOME")
}

// config is the crewflow.toml of the project of this host: its worktrees are a
// folder of the test and its executor is a program the test stands in for.
func (h *host) config(t *testing.T) string {
	t.Helper()
	return writeConfig(t, strings.ReplaceAll(taskConfig, "WORKTREES", h.worktrees))
}

// configAs is the file of a project of the test written out of one of the settings of it,
// with its worktrees in a folder of the test.
func (h *host) configAs(t *testing.T, settings string) string {
	t.Helper()
	return writeConfig(t, strings.ReplaceAll(settings, "WORKTREES", h.worktrees))
}

// otherProject is a checkout of another project in a folder of the test, and the file
// in it: a person with two clones of two projects on one machine points -repo at the
// folder of the one they mean, and the runs of the two never mix.
func (h *host) otherProject(t *testing.T) (checkout, configPath string) {
	t.Helper()
	checkout = t.TempDir()
	other := strings.ReplaceAll(taskConfig, `repo = "naghuale/crewflow"`, `repo = "naghuale/telecli"`)
	configPath = filepath.Join(checkout, "crewflow.toml")
	text := strings.ReplaceAll(other, "WORKTREES", h.worktrees)
	if err := os.WriteFile(configPath, []byte(text), 0o600); err != nil {
		t.Fatalf("write %s: %v", configPath, err)
	}
	return checkout, configPath
}

// exec is the machine of a test: git says nothing, the executor is the events of a
// run, and every start of it is written down.
func (h *host) exec(_ context.Context, name string, args []string, dir string) ([]byte, []byte, int, error) {
	if filepath.Base(name) == "git" {
		switch {
		case len(args) > 1 && args[0] == "worktree" && args[1] == "add":
			h.repoDir = dir
			if err := os.MkdirAll(args[4], 0o700); err != nil {
				return nil, nil, 1, err
			}
			return nil, nil, 0, nil
		case len(args) > 2 && args[0] == "diff":
			return []byte(h.changed), nil, 0, nil
		case len(args) > 1 && args[0] == "rev-parse":
			// What commit the worktree of the task stands at, which the point a run
			// was stopped at is checked against, and which a test moves when it wants
			// to say that the branch has gone on without the run (§7i).
			if len(args) > 1 && args[1] == "HEAD" {
				return []byte(h.head + "\n"), nil, 0, nil
			}
			// Where the repository of the project is, which a run asks for to keep
			// the scratch of the task out of it.
			return []byte(h.git + "\n"), nil, 0, nil
		}
		return nil, nil, 0, nil
	}
	h.started = append(h.started, strings.Join(args, " "))
	return []byte(`{"type":"text","sessionID":"ses_7fKq2","part":{"type":"text","text":"done"}}`),
		[]byte(h.refusal), 0, nil
}

// stream starts the executor of a test: it writes what it says to the journal of the
// run as it says it, which is how a run of a test leaves a journal a watch can show.
func (h *host) stream(_ context.Context, name string, args []string, _ string, _ []string, stdout, stderr io.Writer) (int, error) {
	out, errOut, code, err := h.exec(context.Background(), name, args, "")
	if err != nil {
		return code, err
	}
	if _, err := stdout.Write(out); err != nil {
		return code, err
	}
	if _, err := stderr.Write(errOut); err != nil {
		return code, err
	}
	return code, nil
}

// continuedIn says whether the executor was run with the session among its
// arguments.
func (h *host) continuedIn(session string) bool {
	for _, started := range h.started {
		if strings.Contains(started, "--session "+session) {
			return true
		}
	}
	return false
}

// Task returns the task of the test.
func (h *host) Task(_ context.Context, number int) (forge.Task, error) {
	if h.noTask != nil {
		return forge.Task{}, h.noTask
	}
	if task, is := h.tasks[number]; is {
		return task, nil
	}
	if h.task.Number != number {
		return forge.Task{}, fmt.Errorf("could not find issue %d", number)
	}
	return h.task, nil
}

// FindChangeRequest returns the change request of the branch, when the test has one.
func (h *host) FindChangeRequest(_ context.Context, branch string) (forge.ChangeRequest, bool, error) {
	if !h.opened {
		return forge.ChangeRequest{}, false, nil
	}
	return forge.ChangeRequest{
		Number:     44,
		URL:        "https://github.com/naghuale/crewflow/pull/44",
		HeadBranch: branch,
		HeadSHA:    "9f1c0de",
		BaseBranch: "main",
		State:      "open",
	}, true, nil
}

// CommentTask leaves a line under a task, and keeps what was left: a run that stands is
// written about under its task, and a person who reads the task afterwards reads what
// the schedule of the orchestrator said there (docs/DESIGN.md §6).
func (h *host) CommentTask(_ context.Context, number int, body string) error {
	h.records = append(h.records, fmt.Sprintf("#%d: %s", number, body))
	return nil
}

// ChangeRequest returns a request by its number: the change request of the run of the
// test where it opened one, and nothing where it did not. A host of a test whose change
// went in says so, and that is what takes a run out of the queue of attention (§6a).
func (h *host) ChangeRequest(_ context.Context, number int) (forge.ChangeRequest, error) {
	h.mu.Lock()
	h.changes = append(h.changes, number)
	h.mu.Unlock()
	if state, is := h.states[number]; is {
		return forge.ChangeRequest{
			Number: number, HeadBranch: "crewflow/43-task", HeadSHA: "9f1c0de",
			BaseBranch: "main", State: state,
		}, nil
	}
	if !h.opened {
		return forge.ChangeRequest{}, fmt.Errorf("could not find change request #%d", number)
	}
	state := "open"
	if h.merged {
		state = "merged"
	}
	return forge.ChangeRequest{
		Number:     44,
		URL:        "https://github.com/naghuale/crewflow/pull/44",
		HeadBranch: "crewflow/43-task",
		HeadSHA:    "9f1c0de",
		BaseBranch: "main",
		State:      state,
	}, nil
}

// Comments is what is written under the change request of the run of the test, which is
// what the queue of attention reads to know whether somebody has already looked at it
// (docs.DESIGN.md §6a).
func (h *host) Comments(context.Context, int) ([]forge.Comment, error) { return h.under, nil }

// Subject is what account of the host of the test a login of the file of the project is,
// so that the records of a review and of an acceptance are counted by the number the host
// keeps them under and not by a login (docs.DESIGN.md §7h, §7i).
//
// A host that cannot name an account is a host of a project whose file names nobody the
// host knows, and the queue of attention is then worked out of the state of the tasks
// alone and says what it knows.
func (h *host) Subject(_ context.Context, login string) (forge.Subject, error) {
	if h.noSubject {
		return forge.Subject{}, errors.New("the host of the test does not say what an account of it is")
	}
	// A host knows the App it was given and refuses the login of every other one, in the
	// words of §7i: an App that is neither the one of the executor nor the one of the
	// orchestrator is an App of somebody else's project, and counting records of it would
	// be counting records of a stranger.
	if number, isApp := appOfTest[login]; isApp {
		if h.app != 0 && h.app != number {
			return forge.Subject{}, fmt.Errorf("the account %q is the account of an app that is neither the app "+
				"of the executor nor the app of the orchestrator of this project", login)
		}
		return forge.Subject{Kind: forge.KindApp, Login: login, ID: number}, nil
	}
	return forge.Subject{Kind: forge.KindUser, Login: login, ID: int64(len(login))}, nil
}

// appOfTest are the Apps a host of a test knows by their login, as the two of this project
// are: the one of the executor of a run and the one of the orchestrator (§7i).
var appOfTest = map[string]int64{
	"crewflow-executor[bot]":     5107052,
	"crewflow-orchestrator[bot]": 5140522,
}

// as is the host of a test as the project gave it one App: the two roles of a project are
// two constructions of the roles, and a command that builds them another way is talking
// about a host that does not know the account it is asking about (§7i).
//
// The host is копируется by value, and the lock of the questions goes with it in a fresh
// one: a second host of the same test speaks for itself and keeps its own list (§7i).
func (h *host) as(app int64) *host {
	other := host{
		task: h.task, tasks: h.tasks, noTask: h.noTask, opened: h.opened, changed: h.changed,
		repoDir: h.repoDir, started: h.started, refusal: h.refusal, noIdentity: h.noIdentity,
		home: h.home, worktrees: h.worktrees, git: h.git, records: h.records, under: h.under,
		noSubject: h.noSubject, merged: h.merged, states: h.states, app: app,
	}
	return &other
}

// Doctor says nothing: a test of the command has a host of its own already.
func (h *host) Doctor(context.Context) []forge.Check { return nil }

// ExecutorIdentity is whose name the executor of a run of this host works under, and it
// is the host that answers it because a host is what knows what accounts of its own an
// executor can work as (docs/DESIGN.md §7i).
//
// A host that cannot answer at all is the machine of a project whose key of an App is
// nowhere, and a host with nothing to refuse works as the person who runs crewflow: that
// is what every run of a project that is not in the mode of the bot has been.
func (h *host) ExecutorIdentity(context.Context) (forge.Identity, error) {
	if h.noIdentity != nil {
		return forge.Identity{}, h.noIdentity
	}
	return forge.Identity{
		Mode:        forge.ModeOwner,
		Description: "owner — the person who runs crewflow (shared rights)",
	}, nil
}
