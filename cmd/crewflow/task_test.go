package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/forge/roles"
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
		name  string
		host  *host
		want  []string
		notOK bool
		// refused says that the executor was refused a permission, which ends the
		// run with the code of a success.
		refused bool
	}{
		{
			name:    "the run was refused a permission",
			host:    &host{task: taskOf(43)},
			want:    []string{"blocked-permission", "external_directory /tmp/*"},
			notOK:   true,
			refused: true,
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
			if tc.refused {
				tc.host.refuses(t)
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
	for _, want := range []string{"task", "run", "check", "watch", "doctor", "version"} {
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
	// not have.
	task   forge.Task
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
	// home is the root of what crewflow keeps on this machine, worktrees the root
	// the worktrees of its tasks are made under, and git the folder of the repository
	// of the project as git names it: the local ignore of the scratch of a run is
	// written into it.
	home      string
	worktrees string
	git       string
}

// use makes the task command run on this host, and puts the machine back when the
// test is over. Every test gets a home of its own under TestMain's home, so that
// the journals and the state of one test of a task are not the state of the next
// one, and none of it is ever the home of the person the tests run on.
func (h *host) use(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		taskRoles = roles.New
		taskRunEnv = taskrun.System
	})
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	h.home = filepath.Join(home, ".crewflow")
	h.worktrees = filepath.Join(t.TempDir(), "worktrees")
	h.git = filepath.Join(t.TempDir(), "git")
	taskRoles = func(config.Config, forge.Env) (forge.Set, error) {
		return forge.Set{Tracker: h, Forge: h}, nil
	}
	taskRunEnv = func(home string) taskrun.Env {
		return taskrun.Env{Home: home, Command: h.exec, Stream: h.stream, Now: time.Now}
	}
}

// testHome is the home of the machine the tests run on: a folder of TestMain, and
// never the home of the person who runs them.
func testHome() string {
	return os.Getenv("HOME")
}

// refuses is the way out of a run of a test: the executor exits zero, as it does
// when a permission is refused, and the refusal is on it.
func (h *host) refuses(_ *testing.T) {
	h.refusal = "! permission requested: external_directory (/tmp/*); auto-rejecting\n"
}

// config is the crewflow.toml of the project of this host: its worktrees are a
// folder of the test and its executor is a program the test stands in for.
func (h *host) config(t *testing.T) string {
	t.Helper()
	return writeConfig(t, strings.ReplaceAll(taskConfig, "WORKTREES", h.worktrees))
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

// ChangeRequest returns a request by its number, which a run of a task never asks
// for.
func (h *host) ChangeRequest(context.Context, int) (forge.ChangeRequest, error) {
	return forge.ChangeRequest{}, nil
}

// Comments returns nothing, which a run of a task never asks for either.
func (h *host) Comments(context.Context, int) ([]forge.Comment, error) { return nil, nil }

// Doctor says nothing: a test of the command has a host of its own already.
func (h *host) Doctor(context.Context) []forge.Check { return nil }
