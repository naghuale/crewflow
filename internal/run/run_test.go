package run

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
	"github.com/naghuale/crewflow/internal/secret"
	"github.com/naghuale/crewflow/internal/task"
)

// wholeTask is a task as the template asks a person to write it: every section
// filled in, and the boundaries that say the only paths it may change.
const wholeTask = "## Why\n\nA person builds a long command by hand for every task.\n\n" +
	"## What changes\n\nOne command runs the task and says how the run ended.\n\n" +
	"## How to check it yourself\n\n1. Run it on a task that is not there.\n\n" +
	"## Out of scope\n\nReview and merge are other commands.\n\n" +
	"## Risks and decisions\n\nA run makes a branch; main is not touched.\n\n" +
	"<details>\n<summary>Technical part for the executor</summary>\n\n" +
	"### Acceptance criteria\n\n- [ ] the run opens a change request\n\n" +
	"### Boundaries\n\n```\ninternal/run/**\n```\n\n</details>\n"

// theRun is the answer of an executor that did the work of a task: events of a
// session, and nothing on the way out.
const theRun = `{"type":"text","sessionID":"ses_7fKq2","part":{"type":"text","text":"The change request is open."}}
`

// TestRunOutcomes walks the seven ways a run can end, and the one that says the
// run did what it is for. The code the executor exited with is in every case but
// one, because it is not what says how a run ended (docs/DESIGN.md §7a).
func TestRunOutcomes(t *testing.T) {
	cases := []struct {
		name string
		// stdout and stderr are what the executor wrote, code is what it exited
		// with, and hangs says that it never answers at all.
		stdout string
		stderr string
		code   int
		hangs  bool
		// opened is the change request the host knows about, and changed is what
		// git holds of the files the run touched.
		opened  bool
		changed string
		// timeout is the time limit of the run, short only where a run has to be
		// waited out.
		timeout string
		want    Kind
		// wantOK is whether the code of the command is zero, and the other fields
		// are what the result has to hold besides the outcome.
		wantOK      bool
		wantRejects []string
		wantReason  string
		wantOutside []string
		// wantJournal is what the journal of the attempt holds besides what the
		// executor wrote, and is empty where the run says nothing of its own.
		wantJournal string
	}{
		{
			name:    "the run opened the change request",
			stdout:  theRun,
			opened:  true,
			changed: "internal/run/run.go\n",
			want:    ChangeRequestOpened,
			wantOK:  true,
		},
		{
			// The refusal here is of a place of secrets, which is closed to the executor
			// whatever the project wrote and is an outcome of a run of its own: the run
			// stops, nothing goes on by itself, and the report says what it was refused
			// and what crewflow will not do about it (docs/DESIGN.md §7a.1, §7d).
			name:    "the run reached for a secret",
			stdout:  theRun,
			stderr:  "! permission requested: external_directory (~/.ssh/config); auto-rejecting\n",
			opened:  true,
			changed: "internal/run/run.go\n",
			want:    BlockedSecret,
			wantRejects: []string{
				"external_directory ~/.ssh/config — a secret, closed to the executor whatever the project " +
					"wrote: ~/.ssh/config (" + accessSilent + "); " + recoveryDisabled,
			},
			// The journal of the attempt holds the place, the kind of access and that no
			// run of it goes on by itself: the kind is unknown where the run wrote no
			// command of its own that crewflow can read.
			wantJournal: "crewflow: the executor reached for a secret: ~/.ssh/config (" + accessSilent + "); " +
				recoveryDisabled + "\n",
		},
		{
			// A refusal of anything else is the outcome it was before: a place of
			// secrets is the only refusal that is an outcome of its own.
			name:    "the run was refused a permission",
			stdout:  theRun,
			stderr:  "! permission requested: external_directory (/opt/homebrew/include); auto-rejecting\n",
			opened:  true,
			changed: "internal/run/run.go\n",
			want:    BlockedPermission,
			wantRejects: []string{
				"external_directory /opt/homebrew/include",
			},
		},
		{
			name:       "the executor stopped by itself",
			stdout:     `{"type":"text","sessionID":"ses_7fKq2","part":{"type":"text","text":"BLOCKED: needs libtdjson — the build cannot find it"}}` + "\n",
			opened:     true,
			changed:    "internal/run/run.go\n",
			want:       Blocked,
			wantReason: "needs libtdjson — the build cannot find it",
		},
		{
			name:    "the run ran out of time",
			hangs:   true,
			timeout: "20ms",
			opened:  true,
			changed: "internal/run/run.go\n",
			want:    TimedOut,
		},
		{
			name:    "the run ended without a change request",
			stdout:  theRun,
			changed: "internal/run/run.go\n",
			want:    NoChangeRequest,
		},
		{
			name:    "the executor failed",
			stderr:  "the model provider is out of funds\n",
			code:    1,
			changed: "internal/run/run.go\n",
			want:    ExecutorFailed,
		},
		{
			name:        "the run changed files the task was not to change",
			stdout:      theRun,
			opened:      true,
			changed:     "internal/run/run.go\ndocs/DESIGN.md\n",
			want:        OutOfScope,
			wantOutside: []string{"docs/DESIGN.md"},
		},
		{
			name:        "the run went outside the boundaries and opened nothing",
			stdout:      theRun,
			changed:     "docs/DESIGN.md\n",
			want:        OutOfScope,
			wantOutside: []string{"docs/DESIGN.md"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine(t)
			m.answers["opencode"] = answer{stdout: tc.stdout, stderr: tc.stderr, code: tc.code, hangs: tc.hangs}
			m.answers["git diff --name-only"] = answer{stdout: tc.changed}
			host := &host{task: taskOf(43), opened: tc.opened}
			cfg := projectOf(t, m.worktrees, tc.timeout)

			result, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
			if err != nil {
				t.Fatalf("Run returned an error: %v", err)
			}

			if result.Outcome != tc.want {
				t.Errorf("the outcome = %q, want %q (reason %q, rejections %v)",
					result.Outcome, tc.want, result.Reason, result.Rejections)
			}
			if got := result.OK(); got != tc.wantOK {
				t.Errorf("OK() = %t, want %t for the outcome %q", got, tc.wantOK, result.Outcome)
			}
			if result.Reason != tc.wantReason {
				t.Errorf("the reason = %q, want %q", result.Reason, tc.wantReason)
			}
			if !slices.Equal(result.Rejections, tc.wantRejects) {
				t.Errorf("the rejections = %v, want %v", result.Rejections, tc.wantRejects)
			}
			if !slices.Equal(result.Outside, tc.wantOutside) {
				t.Errorf("the files outside the boundaries = %v, want %v", result.Outside, tc.wantOutside)
			}
			// The host is asked about the change request only when nothing before it
			// has already said how the run ended: a run that was refused a
			// permission is what it is, and the request it may have opened is a
			// question for the review.
			judged := tc.want == ChangeRequestOpened || tc.want == NoChangeRequest || tc.want == OutOfScope
			if got := len(host.asked) > 0; got != judged {
				t.Errorf("the host was asked about the change request: %t, want %t for the outcome %q", got, judged, tc.want)
			}
			if judged && tc.opened {
				if result.ChangeRequest == nil {
					t.Fatal("the result holds no change request, want the one the host knows about")
				}
				if result.ChangeRequest.Number != 44 {
					t.Errorf("the change request = %d, want the one the host knows about", result.ChangeRequest.Number)
				}
			}
			if !judged && result.ChangeRequest != nil {
				t.Errorf("the result holds the change request %+v, want none for the outcome %q", result.ChangeRequest, tc.want)
			}

			// What the executor wrote is left where a person can read it, and the
			// state of the task says how the attempt ended.
			if got, want := whatWasWritten(t, result.Journal), tc.stdout+tc.wantJournal; got != want {
				t.Errorf("the journal holds %q, want %q", got, want)
			}
			if got := read(t, result.ErrorJournal); got != tc.stderr {
				t.Errorf("the journal of the way out holds %q, want what the executor said", got)
			}
			if result.ExitCode != tc.code && !(tc.hangs && result.Outcome == TimedOut) {
				t.Errorf("the code the executor exited with = %d, want %d", result.ExitCode, tc.code)
			}
			state := stateOf(t, m, 43)
			if len(state.Attempts) != 1 || state.Attempts[0].Outcome != tc.want {
				t.Errorf("the state holds %+v, want one attempt that ended as %q", state.Attempts, tc.want)
			}
			if state.Branch != result.Branch || state.Worktree != result.Worktree {
				t.Errorf("the state holds the branch %q and the worktree %q, want the ones of the run %q and %q",
					state.Branch, state.Worktree, result.Branch, result.Worktree)
			}
		})
	}
}

// TestRunMakesTheWorktree checks what a run does before it starts anything: it
// takes the fresh default branch and makes a worktree of the task out of it, in the
// repository of the project, and the executor works there and nowhere else
// (docs/DESIGN.md §7, §8).
func TestRunMakesTheWorktree(t *testing.T) {
	m := newMachine(t)
	m.answers["opencode"] = answer{stdout: theRun}
	host := &host{task: taskOf(43), opened: true}
	cfg := projectOf(t, m.worktrees, "")

	result, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	// The two commands that make the worktree, in that order, and then the question
	// of what the run changed: a worktree is made out of a fresh default branch,
	// and a worktree stands on nothing else.
	want := []string{
		"git fetch origin main",
		"git worktree add -b " + result.Branch + " " + result.Worktree + " origin/main",
		"git rev-parse --git-common-dir",
		"git diff --name-only origin/main...HEAD",
	}
	var got []string
	for _, line := range m.lines() {
		if strings.HasPrefix(line, "git ") {
			got = append(got, line)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("the commands of git = %v, want %v", got, want)
	}
	for _, command := range m.all() {
		if !strings.HasPrefix(command.program, "git") {
			continue
		}
		want := m.repo
		switch {
		// What the run changed is asked in the worktree, which is where the changes
		// are: git of the repository knows nothing of them yet. The repository the
		// scratch of a run is kept out of is asked in the worktree too, which is a
		// worktree of it.
		case slices.Contains(command.args, "diff"), slices.Contains(command.args, "rev-parse"):
			want = result.Worktree
		}
		if command.dir != want {
			t.Errorf("git %v ran in %q, want %q", command.args[:1], command.dir, want)
		}
	}
	executor := m.commandOf("opencode")
	if executor == nil {
		t.Fatalf("the executor was not run, only: %v", m.lines())
	}
	if executor.dir != result.Worktree {
		t.Errorf("the executor ran in %q, want the worktree of the task %q", executor.dir, result.Worktree)
	}
	if want := filepath.Join(m.worktrees, "naghuale-crewflow", "43"); result.Worktree != want {
		t.Errorf("the worktree = %q, want %q", result.Worktree, want)
	}
	if result.Branch != "crewflow/43-the-run-of-a-task" {
		t.Errorf("the branch = %q, want the number of the task and its title", result.Branch)
	}
}

// TestRunGivesTheAssignment checks what the executor is asked: the rules of the
// run, the gates of the project, the whole task, and nothing of the template.
func TestRunGivesTheAssignment(t *testing.T) {
	m := newMachine(t)
	m.answers["opencode"] = answer{stdout: theRun}
	host := &host{task: taskOf(43), opened: true}
	cfg := projectOf(t, m.worktrees, "")
	cfg.Gates = []config.Gate{{Name: "test", Run: []string{"go", "test", "./..."}}}

	result, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	executor := m.commandOf("opencode")
	if executor == nil {
		t.Fatal("the executor was not run")
	}
	asked := strings.Join(executor.args, "\n")
	for _, want := range []string{
		result.Worktree, result.Branch, wholeTask,
		"go test ./...", ".scratch/", "AGENTS.md", "BLOCKED:", "Closes #43",
	} {
		if !strings.Contains(asked, want) {
			t.Errorf("what the executor was asked does not hold %q:\n%s", want, asked)
		}
	}
}

// TestRunOfATaskTheTrackerDoesNotHave is the first case of the manual check: a
// number nothing knows is an error that says so, and nothing is created at all.
func TestRunOfATaskTheTrackerDoesNotHave(t *testing.T) {
	m := newMachine(t)
	host := &host{noTask: errors.New("could not find issue 999")}

	_, err := Run(t.Context(), m.env(), projectOf(t, m.worktrees, ""), host.set(), Request{Number: 999, RepoDir: m.repo})

	if err == nil {
		t.Fatal("Run of a task that is not there returned no error, want one")
	}
	for _, want := range []string{"999", "could not find issue 999"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if len(m.ran) != 0 {
		t.Errorf("the run started %v, want nothing to be started", m.lines())
	}
	if entries, _ := os.ReadDir(m.home); len(entries) != 0 {
		t.Errorf("the run left %v in the home of crewflow, want nothing", entries)
	}
}

// TestRunOfATaskThatIsNotReady: a task nobody filled in is refused before anything
// is created, and the refusal names what is missing (docs/DESIGN.md §7f).
func TestRunOfATaskThatIsNotReady(t *testing.T) {
	m := newMachine(t)
	notReady := taskOf(43)
	notReady.Body = strings.Replace(wholeTask, "## How to check it yourself\n\n1. Run it on a task that is not there.\n", "", 1)
	host := &host{task: notReady}

	_, err := Run(t.Context(), m.env(), projectOf(t, m.worktrees, ""), host.set(), Request{Number: 43, RepoDir: m.repo})

	var missing *task.ErrNotReady
	if !errors.As(err, &missing) {
		t.Fatalf("Run of a task that is not ready = %v, want an *task.ErrNotReady", err)
	}
	if len(missing.Missing) != 1 || missing.Missing[0] != "How to check it yourself" {
		t.Errorf("the missing = %v, want the section nobody wrote", missing.Missing)
	}
	if len(m.ran) != 0 {
		t.Errorf("the run started %v, want nothing to be started", m.lines())
	}
}

// TestRunOfARiskyTaskThatNobodyApproved: the mode of the settings decides whether a
// task waits for a person, and a run that starts anyway would be work nobody
// looked at (docs/DESIGN.md §7f).
func TestRunOfARiskyTaskThatNobodyApproved(t *testing.T) {
	m := newMachine(t)
	risky := taskOf(43)
	risky.Labels = []string{"risky"}
	host := &host{task: risky}

	_, err := Run(t.Context(), m.env(), projectOf(t, m.worktrees, ""), host.set(), Request{Number: 43, RepoDir: m.repo})

	var missing *task.ErrNotReady
	if !errors.As(err, &missing) {
		t.Fatalf("Run of a risky task nobody approved = %v, want an *task.ErrNotReady", err)
	}
	if len(m.ran) != 0 {
		t.Errorf("the run started %v, want nothing to be started", m.lines())
	}
}

// TestRunWithAWorktreeThatIsAlreadyThere: the worktree of a task is the state of
// that task, and a second run over it is a run that goes on and not a new branch.
func TestRunWithAWorktreeThatIsAlreadyThere(t *testing.T) {
	m := newMachine(t)
	m.has(filepath.Join(m.worktrees, "naghuale-crewflow", "43"))
	host := &host{task: taskOf(43)}

	_, err := Run(t.Context(), m.env(), projectOf(t, m.worktrees, ""), host.set(), Request{Number: 43, RepoDir: m.repo})

	if err == nil {
		t.Fatal("Run over a worktree that is already there returned no error, want one")
	}
	if !strings.Contains(err.Error(), "-continue") {
		t.Errorf("error %q does not say how to go on in the worktree that is there", err)
	}
	if m.commandOf("opencode") != nil {
		t.Error("the executor was run over a worktree that is already there")
	}
}

// TestRunKeepsEveryFileInItsOwnFolders is the net under the tests of a run: the
// journal, the state and the worktree of a task are the only files a run leaves, and
// all of them have to be under the home crewflow was given and the root of worktrees
// the project named. A run that resolved the home of the machine instead would write
// into the home of the person who runs the tests, and this is the test that says so.
func TestRunKeepsEveryFileInItsOwnFolders(t *testing.T) {
	m := newMachine(t)
	m.answers["opencode"] = answer{stdout: theRun}
	host := &host{task: taskOf(43), opened: true}
	cfg := projectOf(t, m.worktrees, "")

	result, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	for _, path := range []string{result.Journal, result.ErrorJournal} {
		if !strings.HasPrefix(path, m.home) {
			t.Errorf("the run left %q, want it under the home of crewflow %q", path, m.home)
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("the run left no %q: %v", path, err)
		}
	}
	if !strings.HasPrefix(result.Worktree, m.worktrees) {
		t.Errorf("the run made the worktree %q, want it under the root the project named %q", result.Worktree, m.worktrees)
	}
	state := stateOf(t, m, 43)
	for _, attempt := range state.Attempts {
		for _, path := range []string{attempt.Journal, attempt.ErrorJournal} {
			if !strings.HasPrefix(path, m.home) {
				t.Errorf("the state points at %q, want it under the home of crewflow %q", path, m.home)
			}
		}
	}
	// The state of the task is under the home of crewflow, and the worktree is not:
	// one of them is what crewflow keeps of its own, the other is the work.
	if path := newJournals(m.home, "naghuale-crewflow").StatePath(43); !strings.HasPrefix(path, m.home) {
		t.Errorf("the state of the task is at %q, want it under the home of crewflow %q", path, m.home)
	}
}

// TestRunContinuesTheSession: the run that goes on after a review asked for
// changes is the same session, in the same worktree, and it is the second attempt
// of the task (docs/DESIGN.md §7).
func TestRunContinuesTheSession(t *testing.T) {
	m := newMachine(t)
	m.answers["opencode"] = answer{stdout: theRun}
	host := &host{task: taskOf(43), opened: true}
	cfg := projectOf(t, m.worktrees, "")

	if _, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo}); err != nil {
		t.Fatalf("the first run returned an error: %v", err)
	}
	first := m.commandOf("opencode")
	afterFirst := len(m.ran)

	result, err := Run(t.Context(), m.env(), cfg, host.set(),
		Request{Number: 43, RepoDir: m.repo, Continue: "the review asked for a test of the timeout"})
	if err != nil {
		t.Fatalf("the second run returned an error: %v", err)
	}

	ran := m.commandsOf("opencode")
	if len(ran) != 2 {
		t.Fatalf("the executor was run %d times, want twice", len(ran))
	}
	second := ran[1]
	// The message of the orchestrator is what the agent is asked now: the session
	// holds the task and the last try, and the rest of it says the same.
	message := "the review asked for a test of the timeout"
	if !slices.Contains(second.args, message) {
		t.Errorf("the second run ran with %v, want the message of the orchestrator in it", second.args[:len(second.args)-2])
	}
	if first.args[len(first.args)-1] == second.args[len(second.args)-3] {
		t.Error("the second run was asked the whole assignment again, want the message alone")
	}
	if !slices.Equal(second.args[len(second.args)-2:], []string{"--session", "ses_7fKq2"}) {
		t.Errorf("the second run ran with %v, want it to go on in the session of the first one", second.args)
	}
	if second.dir != first.dir {
		t.Errorf("the second run worked in %q, want the worktree of the first one %q", second.dir, first.dir)
	}
	if result.Attempt != 2 || !result.Continued {
		t.Errorf("the second run is the attempt %d (continued %t), want the second and a continuation", result.Attempt, result.Continued)
	}
	// The worktree is not made again: it is the same one, with the work in it.
	for _, line := range m.lines()[afterFirst:] {
		if strings.HasPrefix(line, "git worktree add") {
			t.Errorf("the second run made a worktree again: %v", m.lines()[afterFirst:])
		}
	}
	state := stateOf(t, m, 43)
	if len(state.Attempts) != 2 {
		t.Fatalf("the state holds %d attempts, want two", len(state.Attempts))
	}
	if !state.Attempts[1].Continued || state.Attempts[0].Continued {
		t.Errorf("the attempts = %+v, want only the second one to be a continuation", state.Attempts)
	}
	if state.Session != "ses_7fKq2" {
		t.Errorf("the state holds the session %q, want the one the run went on in", state.Session)
	}
}

// TestRunContinuesAnAgentWithoutSessions: an agent that has no session to go on in
// is run again in the same worktree, with the message of the orchestrator and the
// whole task, or it would begin from nothing (docs/DESIGN.md §7a).
func TestRunContinuesAnAgentWithoutSessions(t *testing.T) {
	m := newMachine(t)
	m.answers["claude"] = answer{stdout: "I did the work\n"}
	host := &host{task: taskOf(43), opened: true}
	cfg := projectOf(t, m.worktrees, "")
	cfg.Executor.Command = []string{"claude", "-p", "{prompt}"}

	if _, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo}); err != nil {
		t.Fatalf("the first run returned an error: %v", err)
	}
	if _, err := Run(t.Context(), m.env(), cfg, host.set(),
		Request{Number: 43, RepoDir: m.repo, Continue: "add a test of the timeout"}); err != nil {
		t.Fatalf("the second run returned an error: %v", err)
	}

	ran := m.commandsOf("claude")
	if len(ran) != 2 {
		t.Fatalf("the executor was run %d times, want twice", len(ran))
	}
	asked := ran[1].args[len(ran[1].args)-1]
	if !strings.HasPrefix(asked, "add a test of the timeout") || !strings.Contains(asked, wholeTask) {
		t.Errorf("the second run was asked %q, want the message and the whole task again", asked)
	}
	if slices.Contains(ran[1].args, "--session") {
		t.Errorf("the second run ran with %v, want no session: this agent has none", ran[1].args)
	}
}

// TestRunContinuesATaskThatWasNeverRun: there is nothing to continue, and crewflow
// says so instead of starting a task that was never begun.
func TestRunContinuesATaskThatWasNeverRun(t *testing.T) {
	m := newMachine(t)
	host := &host{task: taskOf(43)}

	_, err := Run(t.Context(), m.env(), projectOf(t, m.worktrees, ""), host.set(),
		Request{Number: 43, RepoDir: m.repo, Continue: "the review asked for changes"})

	if err == nil {
		t.Fatal("Run -continue of a task that was never run returned no error, want one")
	}
	if !strings.Contains(err.Error(), "never run") {
		t.Errorf("error %q does not say that the task was never run here", err)
	}
	if len(m.ran) != 0 {
		t.Errorf("the run started %v, want nothing to be started", m.lines())
	}
}

// TestRunContinuesATaskWhoseWorktreeIsGone: the worktree of a task is where its
// work is, and without it there is nothing to go on in.
func TestRunContinuesATaskWhoseWorktreeIsGone(t *testing.T) {
	m := newMachine(t)
	m.answers["opencode"] = answer{stdout: theRun}
	host := &host{task: taskOf(43), opened: true}
	cfg := projectOf(t, m.worktrees, "")

	if _, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo}); err != nil {
		t.Fatalf("the first run returned an error: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(m.worktrees, "naghuale-crewflow", "43")); err != nil {
		t.Fatalf("take the worktree away: %v", err)
	}

	_, err := Run(t.Context(), m.env(), cfg, host.set(),
		Request{Number: 43, RepoDir: m.repo, Continue: "the review asked for changes"})

	if err == nil {
		t.Fatal("Run -continue without a worktree returned no error, want one")
	}
	if !strings.Contains(err.Error(), "not there") {
		t.Errorf("error %q does not say that the worktree of the task is gone", err)
	}
}

// TestRunOfAProjectWithoutATracker: a project that keeps its tasks in files crewflow
// cannot read yet has no task to run, and the key to change is named.
func TestRunOfAProjectWithoutATracker(t *testing.T) {
	m := newMachine(t)

	_, err := Run(t.Context(), m.env(), projectOf(t, m.worktrees, ""), forge.Set{}, Request{Number: 43, RepoDir: m.repo})

	if err == nil {
		t.Fatal("Run of a project without a tracker returned no error, want one")
	}
	if !strings.Contains(err.Error(), "tracker.kind") {
		t.Errorf("error %q does not name the key a person has to change", err)
	}
}

// TestRunWhenGitSaysNo: the worktree could not be made, so no run happened, and the
// command that failed is in the error, because a person is shown what to run by
// hand.
func TestRunWhenGitSaysNo(t *testing.T) {
	m := newMachine(t)
	m.answers["git fetch"] = answer{stderr: "fatal: no such remote\n", code: 128}
	host := &host{task: taskOf(43)}

	_, err := Run(t.Context(), m.env(), projectOf(t, m.worktrees, ""), host.set(), Request{Number: 43, RepoDir: m.repo})

	if err == nil {
		t.Fatal("Run without a remote returned no error, want one")
	}
	for _, want := range []string{"git fetch origin main", "no such remote", "128"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if m.commandOf("opencode") != nil {
		t.Error("the executor was run without a worktree to run it in")
	}
}

// TestRunKeepsWhatAListOfRunsNeeds: the state of a task is all that
// `crewflow task list` reads, so a run writes into it which process it was in and
// which change request it opened. A machine that cannot say which process it is runs
// the task as crewflow did before: the state names no process, and a list reads that
// the old way (docs/DESIGN.md §7).
func TestRunKeepsWhatAListOfRunsNeeds(t *testing.T) {
	cases := []struct {
		name string
		// blind is a machine that cannot say what process the run is in.
		blind bool
		want  bool
	}{
		{name: "the machine knows the process of the run", want: true},
		{name: "the machine cannot be asked", blind: true, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine(t)
			m.answers["opencode"] = answer{stdout: theRun}
			host := &host{task: taskOf(43), opened: true}
			cfg := projectOf(t, m.worktrees, "")
			env := m.env()
			if tc.blind {
				env.Process = nil
			}

			result, err := Run(t.Context(), env, cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
			if err != nil {
				t.Fatalf("Run returned an error: %v", err)
			}

			state := stateOf(t, m, 43)
			process, named := state.Attempts[0].Process()
			if named != tc.want {
				t.Fatalf("the state names a process %+v: %t, want it named: %t", process, named, tc.want)
			}
			if tc.want && (process.Pid != 4242 || process.StartedAt.IsZero()) {
				t.Errorf("the state holds the process %+v, want the one the run was in", process)
			}
			if state.Change == nil || state.Change.Number != result.ChangeRequest.Number ||
				state.Change.URL != result.ChangeRequest.URL {
				t.Errorf("the state holds the change request %+v, want the one the run opened", state.Change)
			}
		})
	}
}

// taskOf is a whole task of a test: the number, a title, and a body written the way
// the template asks.
func taskOf(number int) forge.Task {
	return forge.Task{
		Number: number,
		Title:  "the run of a task",
		Body:   wholeTask,
		State:  "open",
		URL:    "https://github.com/naghuale/crewflow/issues/43",
	}
}

// projectOf is the settings of the project a test runs a task on: worktrees in a
// folder of the test's own, and an executor that is a program of the test's own.
func projectOf(t *testing.T, worktrees, timeout string) config.Config {
	t.Helper()
	if timeout == "" {
		timeout = "1h"
	}
	return config.Config{
		Project: config.Project{Repo: "naghuale/crewflow", DefaultBranch: "main", Language: "en"},
		// The worktrees of a project live under a root of their own, named after
		// it, whatever the project wrote in it (docs/DESIGN.md §5).
		Worktrees: config.Worktrees{Root: filepath.Join(worktrees, "{repo}")},
		Executor: config.Executor{ExecutorSpec: config.ExecutorSpec{
			Command:    []string{"opencode", "run", "--dir", "{worktree}", "--format", "json", "{prompt}"},
			Timeout:    timeout,
			StallAfter: "10m",
		}},
		Tasks: config.Tasks{OwnerApproval: "risky"},
	}
}

// stateOf is what crewflow kept of the task on this machine.
func stateOf(t *testing.T, m *machine, number int) State {
	t.Helper()
	state, err := LoadState(newJournals(m.home, "naghuale-crewflow").StatePath(number))
	if err != nil {
		t.Fatalf("load the state of task %d: %v", number, err)
	}
	return state
}

// host is the tracker and the host of a test: the task it holds and the change
// request of the branch it is told about. Nothing of it goes anywhere, so no test
// of a run reaches a tracker or a host.
type host struct {
	// task is what the tracker holds.
	task forge.Task
	// noTask is the answer of a tracker for a task it does not have.
	noTask error
	// change is the change request of the branch, and opened says whether there is
	// one at all: a branch with no request is a run in progress, not a failure.
	change forge.ChangeRequest
	opened bool
	// asked is every branch the host was asked about.
	asked []string
	// identity is whose name the executor of a run of this host works under, and a
	// host with none works as the person who runs crewflow: that is the mode every
	// run has been in, and a test says otherwise on purpose (§7i). A host with no
	// identity to give at all says so in noIdentity, which is how a machine without a
	// key of an app in it looks to a run (§7i).
	identity   forge.Identity
	noIdentity error
	// store is the store of the secrets of the machine as the roles of this host see
	// it: a project whose executor works as an App asks it for the key of the App
	// before anything else, and a test of the keychain of macOS puts a store of its
	// own in here so that no run of a test ever opens the one of the person who runs
	// it (§7e, §7i).
	store secret.Store
	// openedByTheRun is the request crewflow opened for a run whose executor did
	// not, and the title and the body it opened it with.
	openedByTheRun *forge.ChangeRequest
	title, body    string
}

// set is the roles of a project of a test. A run asks the tracker for the task and
// the host for the change request of the branch, and nothing else of either — and
// the host of a project in the mode of the bot may open a request on behalf of a run
// as well, which is the one thing the mode of the owner has none of (§7i).
func (h *host) set() forge.Set {
	set := forge.Set{Tracker: h, Forge: h}
	if h.identity.Mode == forge.ModeBot {
		set.Opener = h
	}
	return set
}

// ExecutorIdentity is whose name the executor of a run of this host works under. A
// host that says nothing works as the person who runs crewflow, because that is what
// a run has always done and what a test of the mode of the owner has to see (§7i).
func (h *host) ExecutorIdentity(context.Context) (forge.Identity, error) {
	if h.noIdentity != nil {
		return forge.Identity{}, h.noIdentity
	}
	// The key of the App comes before the name of the account: a run that is given a
	// token of the app is a run that signs one, and the store of the machine is where
	// the key of the app is (§7i). It is asked of the store and not of a field of this
	// host, because the store is what a machine of a test replaces.
	if h.store != nil {
		if _, err := h.store.Get(secret.Service, secret.AppKey(5107052)); err != nil {
			return forge.Identity{}, err
		}
	}
	if h.identity.Description == "" {
		return forge.Identity{
			Mode:        forge.ModeOwner,
			Description: "owner — the person who runs crewflow (shared rights)",
		}, nil
	}
	return h.identity, nil
}

// OpenChangeRequest is the request crewflow opens for a run whose executor did not,
// and a host that cannot open one says so rather than pretending the branch is under
// a request (§7i).
func (h *host) OpenChangeRequest(_ context.Context, branch, title, body string) (forge.ChangeRequest, error) {
	h.title, h.body = title, body
	if h.openedByTheRun == nil {
		return forge.ChangeRequest{}, errors.New("this host opens no change request of its own")
	}
	opened := *h.openedByTheRun
	opened.HeadBranch, opened.BaseBranch = branch, "main"
	if opened.Body == "" {
		opened.Body = body
	}
	return opened, nil
}

// Task returns the task of the test, or the error it was given for another one.
func (h *host) Task(_ context.Context, number int) (forge.Task, error) {
	if h.noTask != nil {
		return forge.Task{}, h.noTask
	}
	if h.task.Number != number {
		return forge.Task{}, fmt.Errorf("could not find issue %d", number)
	}
	return h.task, nil
}

// FindChangeRequest returns the request of the branch, when the test has one. It
// stands on the branch it was asked about, because that is what the request of a
// run of a task is.
func (h *host) FindChangeRequest(_ context.Context, branch string) (forge.ChangeRequest, bool, error) {
	h.asked = append(h.asked, branch)
	if !h.opened {
		return forge.ChangeRequest{}, false, nil
	}
	h.change.HeadBranch = branch
	if h.change.Number == 0 {
		h.change.Number = 44
		h.change.URL = "https://github.com/naghuale/crewflow/pull/44"
	}
	return h.change, true, nil
}

// ChangeRequest returns a request by its number, which a run of a task never asks
// for.
func (h *host) ChangeRequest(_ context.Context, number int) (forge.ChangeRequest, error) {
	return h.change, nil
}

// Comments returns nothing, which a run of a task never asks for either.
func (h *host) Comments(context.Context, int) ([]forge.Comment, error) { return nil, nil }

// Doctor says nothing: a test of a run has a host of its own already.
func (h *host) Doctor(context.Context) []forge.Check { return nil }
