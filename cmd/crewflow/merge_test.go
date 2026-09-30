package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/forge/roles"
	"github.com/naghuale/crewflow/internal/gate"
	"github.com/naghuale/crewflow/internal/merge"
	taskrun "github.com/naghuale/crewflow/internal/run"
)

// mergeConfig is the file of a project a change of it is merged in: it names who
// reviews it and how long the CI of the branch may be waited for.
const mergeConfig = `
[project]
repo = "naghuale/crewflow"
language = "en"

[executor]
command = ["opencode", "run", "--dir", "{worktree}", "--format", "json", "{prompt}"]
timeout = "1h"

[merge]
reviewers = ["naghuale"]

[ci]
required = true
timeout = "5m"
`

// TestRunMergeOfAChangeTheGateAllows is what the orchestrator asks for after an
// approval: the default branch of the host is fast-forwarded to exactly the approved
// commit, the branch is asked afterwards where it points, and the task of the change is
// waited for (docs/DESIGN.md §6, §7h).
func TestRunMergeOfAChangeTheGateAllows(t *testing.T) {
	host := newMergeHost(t)
	project := writeConfig(t, mergeConfig)
	var stdout, stderr bytes.Buffer

	code := run([]string{"merge", "7", "-config", project}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow merge = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	for _, want := range []string{
		"change #7", "merged: main of the host is at " + reviewHead, "task #7: closed by host",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("crewflow merge wrote:\n%s\nwant it to mention %q", stdout.String(), want)
		}
	}
	// The push is one refspec of one commit, given to git as arguments and never as a
	// line of a shell: in a shell `$SHA:refs/heads/main` is a modifier of a variable
	// and not a refspec (docs/DESIGN.md §7h, §10).
	if !host.ran("git push origin " + reviewHead + ":refs/heads/main") {
		t.Errorf("crewflow merge ran:\n%s\nwant the push of the approved commit", strings.Join(host.commands, "\n"))
	}
	if !host.ran("git ls-remote origin refs/heads/main") {
		t.Errorf("crewflow merge ran:\n%s\nwant the branch of the host asked where it points", strings.Join(host.commands, "\n"))
	}
	if host.remote != reviewHead {
		t.Errorf("the branch of the host is at %s, want the approved %s", host.remote, reviewHead)
	}
	// What the merge did is in its journal, next to what a run of the task wrote.
	journal := filepath.Join(testHome(), ".crewflow", "runs", "naghuale-crewflow", "7-merge.jsonl")
	if !strings.Contains(read(t, journal), `"command":"git push origin `+reviewHead+`:refs/heads/main"`) {
		t.Errorf("the journal of the merge holds no push:\n%s", read(t, journal))
	}
	state, err := taskrun.LoadState(statePathOfTask(7))
	if err != nil {
		t.Fatalf("read the state of the task: %v", err)
	}
	if state.MergedSHA != reviewHead {
		t.Errorf("the state of the task holds the merged commit %q, want %q", state.MergedSHA, reviewHead)
	}
}

// TestRunMergeAsJSON is the shape an orchestrator reads: the change, the commit, the
// outcome and the verdict, in fields and not in a report to be read.
func TestRunMergeAsJSON(t *testing.T) {
	host := newMergeHost(t)
	project := writeConfig(t, mergeConfig)
	var stdout, stderr bytes.Buffer

	code := run([]string{"merge", "7", "-config", project, "-json"}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow merge -json = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	var answer struct {
		Task      int            `json:"task"`
		Change    int            `json:"change"`
		Head      string         `json:"head"`
		Branch    string         `json:"branch"`
		Outcome   merge.Outcome  `json:"outcome"`
		MergedSHA string         `json:"merged_sha"`
		TaskClose bool           `json:"task_closed"`
		ClosedBy  merge.ClosedBy `json:"task_closed_by"`
		Journal   string         `json:"journal"`
		Verdict   gate.Verdict   `json:"verdict"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &answer); err != nil {
		t.Fatalf("the answer of crewflow merge is not the JSON of an outcome: %v\n%s", err, stdout.String())
	}
	if answer.Outcome != merge.Merged || answer.MergedSHA != reviewHead || answer.Head != reviewHead {
		t.Errorf("the answer holds %+v, want the change merged at the approved commit", answer)
	}
	if answer.ClosedBy != merge.ClosedByHost {
		t.Errorf("the answer says the task was closed by %q, want %q: a host that closes an issue behind a change says so",
			answer.ClosedBy, merge.ClosedByHost)
	}
	if answer.Change != 7 || answer.Task != 7 || answer.Branch != "main" || !answer.TaskClose {
		t.Errorf("the answer is of #%d of task #%d into %q (task closed %v), want #7 of task 7 into main",
			answer.Change, answer.Task, answer.Branch, answer.TaskClose)
	}
	if !answer.Verdict.Ready || answer.Journal == "" {
		t.Errorf("the answer holds the verdict %+v and the journal %q, want a verdict that is ready and the journal",
			answer.Verdict, answer.Journal)
	}
	_ = host
}

// TestTheReportOfAMergeNamesWhoClosedTheTask: a change that has gone in leaves a task
// behind it, the host closes that task as a rule, and where it does not the merge closes
// it itself. A report that does not say which hand closed the task leaves a person
// guessing about a task that is closed for good (docs/DESIGN.md §6, §7h).
func TestTheReportOfAMergeNamesWhoClosedTheTask(t *testing.T) {
	for _, tc := range []struct {
		name   string
		closed merge.ClosedBy
		want   string
	}{
		{"the host closed it", merge.ClosedByHost, "task #7: closed by host"},
		{"crewflow closed it", merge.ClosedByCrewflow, "task #7: closed by crewflow"},
		{"nobody closed it yet", "", "task #7: not closed yet"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer

			printMerge(&out, merge.Result{
				Change:       7,
				Outcome:      merge.Merged,
				MergedSHA:    reviewHead,
				Task:         7,
				TaskClosed:   tc.closed != "",
				TaskClosedBy: tc.closed,
			})

			if !strings.Contains(out.String(), tc.want) {
				t.Errorf("the report of the merge wrote:\n%s\nwant it to mention %q", out.String(), tc.want)
			}
		})
	}
}

// TestRunMergeRefusesAChangeTheGateRefuses: a change nobody approved is not pushed at
// all, and the answer is the one reason of the table of §7h and what to do about it
// (docs/DESIGN.md §7h).
func TestRunMergeRefusesAChangeTheGateRefuses(t *testing.T) {
	host := newMergeHost(t)
	host.reviewHost.comments = nil
	project := writeConfig(t, mergeConfig)
	var stdout, stderr bytes.Buffer

	code := run([]string{"merge", "7", "-config", project}, &stdout, &stderr)

	if code != exitFailure {
		t.Fatalf("crewflow merge = %d, want %d", code, exitFailure)
	}
	if !strings.Contains(stdout.String(), "approval-missing") || !strings.Contains(stdout.String(), "nothing was pushed") {
		t.Errorf("crewflow merge wrote:\n%s\nwant the reason and that nothing was pushed", stdout.String())
	}
	if host.pushed() {
		t.Errorf("crewflow merge ran:\n%s\nwant no push at all", strings.Join(host.commands, "\n"))
	}
}

// TestRunMergeOfAChangeTheHostRefuses: a host with a rule on its branch refuses the push,
// the branch is where it was, and that is `push-rejected` — not a merge that went
// through and not a branch that moved on (docs/DESIGN.md §7h).
func TestRunMergeOfAChangeTheHostRefuses(t *testing.T) {
	host := newMergeHost(t)
	host.refuses = true
	project := writeConfig(t, mergeConfig)
	var stdout, stderr bytes.Buffer

	code := run([]string{"merge", "7", "-config", project}, &stdout, &stderr)

	if code != exitFailure {
		t.Fatalf("crewflow merge = %d, want %d", code, exitFailure)
	}
	if !strings.Contains(stdout.String(), "push-rejected") {
		t.Errorf("crewflow merge wrote:\n%s\nwant it to name push-rejected", stdout.String())
	}
	if host.remote == reviewHead {
		t.Error("the branch of the host is at the approved commit, want it where the host left it")
	}
}

// TestRunMergeOfAChangeThatIsAlreadyMerged: a merge asked for a second time finds the
// change in the branch of the host, pushes nothing and says so (docs/DESIGN.md §7h).
func TestRunMergeOfAChangeThatIsAlreadyMerged(t *testing.T) {
	host := newMergeHost(t)
	host.remote = reviewHead
	project := writeConfig(t, mergeConfig)
	var stdout, stderr bytes.Buffer

	code := run([]string{"merge", "7", "-config", project}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow merge = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	if !strings.Contains(stdout.String(), "already merged") {
		t.Errorf("crewflow merge wrote:\n%s\nwant it to say that the change is merged already", stdout.String())
	}
	if host.pushed() {
		t.Errorf("crewflow merge ran:\n%s\nwant no push of what is merged already", strings.Join(host.commands, "\n"))
	}
}

// TestRunVerifyOfAMergedChange is the check of a merge in the case it is for: the branch
// of the host is at the commit that was merged, the task is closed and the CI of the
// branch is green (docs/DESIGN.md §6, §7h).
func TestRunVerifyOfAMergedChange(t *testing.T) {
	host := newMergeHost(t)
	host.remote = reviewHead
	project := writeConfig(t, mergeConfig)
	var stdout, stderr bytes.Buffer

	code := run([]string{"verify", "7", "-config", project}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow verify = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	for _, want := range []string{
		"change #7", "verified: main of the host is at " + reviewHead,
		"task #7: closed", "checks of main: success",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("crewflow verify wrote:\n%s\nwant it to mention %q", stdout.String(), want)
		}
	}
	state, err := taskrun.LoadState(statePathOfTask(7))
	if err != nil {
		t.Fatalf("read the state of the task: %v", err)
	}
	if state.VerifiedAt == nil {
		t.Error("the state of the task holds no moment of the check, want the one the check reports")
	}
}

// TestRunVerifyOfARedBranch: the CI of the branch of the project is red, the check says
// so with its own word and the code of the command says it did not verify
// (docs/DESIGN.md §6, §7h).
func TestRunVerifyOfARedBranch(t *testing.T) {
	host := newMergeHost(t)
	host.remote = reviewHead
	host.checks = forge.CheckFailure
	project := writeConfig(t, mergeConfig)
	var stdout, stderr bytes.Buffer

	code := run([]string{"verify", "7", "-config", project}, &stdout, &stderr)

	if code != exitFailure {
		t.Fatalf("crewflow verify = %d, want %d", code, exitFailure)
	}
	if !strings.Contains(stdout.String(), "not verified") || !strings.Contains(stdout.String(), string(forge.CheckFailure)) {
		t.Errorf("crewflow verify wrote:\n%s\nwant it to name the red checks of the branch", stdout.String())
	}
}

// TestRunMergeCalledWrong walks the ways a call of the commands of the merge can be
// wrong: no change at all, a word that is not a number, two changes, and an argument
// that is not a flag. Every one of them is a wrong call and says what the right one is
// (docs/DESIGN.md §6).
func TestRunMergeCalledWrong(t *testing.T) {
	newMergeHost(t)
	project := writeConfig(t, mergeConfig)
	for _, command := range []string{"merge", "verify"} {
		for _, args := range [][]string{
			{command},
			{command, "-config", project},
			{command, "seven", "-config", project},
			{command, "0", "-config", project},
			{command, "7", "8", "-config", project},
		} {
			t.Run(strings.Join(args, " "), func(t *testing.T) {
				var stdout, stderr bytes.Buffer

				code := run(args, &stdout, &stderr)

				if code != exitUsage {
					t.Errorf("run(%v) = %d, want %d (stderr: %q)", args, code, exitUsage, stderr.String())
				}
				if !strings.Contains(stderr.String(), "Usage:") || !strings.Contains(stderr.String(), "crewflow "+command) {
					t.Errorf("run(%v) wrote %q to stderr, want the usage of the command", args, stderr.String())
				}
				if stdout.Len() != 0 {
					t.Errorf("run(%v) wrote %q to stdout, want nothing", args, stdout.String())
				}
			})
		}
	}
}

// TestUsageMentionsTheCommandsOfTheMerge: the two commands of the merge are in the usage
// with their flags, or an orchestrator does not know they exist (docs/DESIGN.md §6).
func TestUsageMentionsTheCommandsOfTheMerge(t *testing.T) {
	var stdout, stderr bytes.Buffer

	if code := run([]string{"help"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("run(help) = %d, want 0", code)
	}
	for _, want := range []string{
		"merge <PR>", "verify <PR>", "crewflow merge <PR> [-config path] [-repo path] [-json]",
		"crewflow verify <PR> [-config path] [-repo path] [-json]",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("the usage does not mention %q:\n%s", want, stdout.String())
		}
	}
}

// mergeHost is the host of a project of a test with the git of the merge behind it: a
// merge asks the branch of the host where it points and pushes into it, and both
// answers come from a struct here — no test of the command reaches GitHub or a checkout
// of the person who runs it (docs/DESIGN.md §7h).
type mergeHost struct {
	*reviewHost
	// remote is the commit the branch of the host is at, refuses says that it takes no
	// push at all, and checks is how the CI of the branch stands when it is asked.
	remote   string
	refuses  bool
	checks   forge.CheckState
	commands []string
}

// newMergeHost is a project of a test with a change that is green and approved by the
// owner of the repository, and the git of the test in place.
func newMergeHost(t *testing.T) *mergeHost {
	t.Helper()
	h := &mergeHost{reviewHost: newReviewHost(t), remote: reviewBase, checks: forge.CheckSuccess}
	h.reviewHost.comment(gate.ApproveOf(reviewHead, 7))
	h.use(t)
	return h
}

// use makes the commands of the merge run on this host and against this git, and puts
// the machine back when the test is over.
func (h *mergeHost) use(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		mergeRoles = roles.AsOwner
		mergeGit = runGit
	})
	mergeRoles = func(config.Config, forge.Env) (forge.Set, error) {
		return forge.Set{Forge: h, Tracker: h, CI: h}, nil
	}
	mergeGit = h.git
}

// Task is the task of the change, closed behind the merge: a host closes an issue behind
// a change that has gone in, and a merge that waits for it waits for that
// (docs/DESIGN.md §7h).
func (h *mergeHost) Task(ctx context.Context, number int) (forge.Task, error) {
	found, err := h.reviewHost.Task(ctx, number)
	if err == nil {
		found.State = "closed"
	}
	return found, err
}

// Status is how the checks of the commit of the branch stand.
func (h *mergeHost) Status(context.Context, string) (forge.CheckState, error) { return h.checks, nil }

// git is the git of a merge of a test: every command is written down, the branch of the
// host is at `remote`, and a push moves it to the commit it was given — unless the host
// refuses pushes, which is what a rule on the branch of a host looks like from here.
func (h *mergeHost) git(_ context.Context, name string, args []string, _ string) ([]byte, []byte, int, error) {
	h.commands = append(h.commands, name+" "+strings.Join(args, " "))
	switch {
	case len(args) > 0 && args[0] == "fetch":
		return nil, nil, 0, nil
	case len(args) > 1 && args[0] == "rev-parse":
		return []byte(h.reviewHost.change.HeadSHA + "\n"), nil, 0, nil
	case len(args) > 3 && args[0] == "merge-base" && args[1] == "--is-ancestor":
		// "is the default branch an ancestor of the head" is the fast-forward a merge
		// needs, and it holds as long as the branch is behind the head; "is the head an
		// ancestor of the default branch" is whether the change is in the branch
		// already, which is what a second merge finds out.
		if strings.HasPrefix(args[2], "origin/") {
			return nil, nil, 0, nil
		}
		if args[2] == h.reviewHost.change.HeadSHA && h.remote == args[2] {
			return nil, nil, 0, nil
		}
		return nil, nil, 1, nil
	case len(args) > 0 && args[0] == "ls-remote":
		return []byte(h.remote + "\trefs/heads/main\n"), nil, 0, nil
	case len(args) > 0 && args[0] == "push":
		if h.refuses {
			return nil, []byte("the rules of this branch let nothing through\n"), 1, nil
		}
		h.remote, _, _ = strings.Cut(args[len(args)-1], ":")
		return []byte("To the host\n"), nil, 0, nil
	default:
		return nil, nil, 0, nil
	}
}

// ran is whether git was given that command, as a person would write it by hand.
func (h *mergeHost) ran(command string) bool {
	return slices.Contains(h.commands, command)
}

// pushed is whether git was asked for a push of the branch of the project at all.
func (h *mergeHost) pushed() bool {
	for _, command := range h.commands {
		if strings.Contains(command, " push ") {
			return true
		}
	}
	return false
}

// reviewBase is the commit the default branch of the project is at before a change of it
// is merged: the state of the repository a change is merged into is a commit of its own,
// and not the head of the change.
const reviewBase = "0a1b2c3d4e5f60718293a4b5c6d7e8f901234567"

// read is what a file holds, for a test that checks what a command left behind.
func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// TestTheCycleOfATaskWithoutAPushByHand is the criterion of a merge being a command and
// not a habit: a task, a run in a worktree of its own, a change request, an approval of
// the head, a merge and a check after it — all of it on a real git, with a bare
// repository as the host of the project and a struct in place of its API, and without a
// push and a comparison of a commit anywhere in the test (docs/DESIGN.md §6, §7h).
func TestTheCycleOfATaskWithoutAPushByHand(t *testing.T) {
	for _, program := range []string{"git", "sh"} {
		if _, err := exec.LookPath(program); err != nil {
			t.Skipf("%s is not installed: %v", program, err)
		}
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	repo, origin := repositoryOfCycle(t)
	host := &cycleHost{repo: repo, origin: origin, worktreeOfTask: filepath.Join(t.TempDir(), "7")}
	host.use(t)
	executor := fakeExecutor(t, "opencode",
		"printf '%s' '"+theEvents+"'\n"+
			commit("internal/merge/merge.go", "package merge\n")+
			"git push --quiet origin HEAD\n")
	project := writeConfig(t, strings.NewReplacer(
		"WORKTREES", filepath.Join(t.TempDir(), "worktrees"),
		"EXECUTOR", executor,
	).Replace(cycleConfig))

	var stdout, stderr bytes.Buffer
	for _, step := range []struct {
		name string
		args []string
		want int
	}{
		{"task run", []string{"task", "run", "7", "-config", project, "-repo", repo}, exitOK},
		{"review", []string{"review", "7", "-config", project, "-repo", repo}, exitFailure},
		{"approve", []string{"review", "7", "-config", project, "-repo", repo, "-approve"}, exitOK},
		{"merge", []string{"merge", "7", "-config", project, "-repo", repo}, exitOK},
		{"verify", []string{"verify", "7", "-config", project, "-repo", repo}, exitOK},
	} {
		stdout.Reset()
		stderr.Reset()
		code := run(step.args, &stdout, &stderr)
		if code != step.want {
			t.Fatalf("crewflow %s = %d, want %d\nstdout:\n%s\nstderr:\n%s",
				step.name, code, step.want, stdout.String(), stderr.String())
		}
		// The check after the merge is the step that has to read a change the host
		// holds as merged: a change that has gone in is not one anyone may merge, and
		// the check has to confirm the merge out of the head of the change all the same
		// (docs/DESIGN.md §6, §7h).
		if step.name == "verify" && !strings.Contains(stdout.String(), "verified:") {
			t.Errorf("crewflow verify wrote:\n%s\nwant it to confirm the merge", stdout.String())
		}
	}

	// What a person would look at by hand is what the commands said: the branch of the
	// host is at the commit the approval named, and the state of the task says what was
	// merged and when it was checked.
	head := strings.TrimSpace(gitOfCycleCmd(repo, "rev-parse", "refs/remotes/origin/"+host.branch))
	if at := remoteOfCycle(t, origin, "main"); at != head {
		t.Errorf("main of the host is at %s, want the head of the change %s", at, head)
	}
	if !strings.Contains(host.approvedBody, "REVIEW: APPROVED "+head) {
		t.Errorf("the record under the change holds %q, want an approval of the head %s",
			host.approvedBody, head)
	}
	state, err := taskrun.LoadState(taskrun.JournalsOf(filepath.Join(home, ".crewflow"), "naghuale-crewflow").StatePath(7))
	if err != nil {
		t.Fatalf("read the state of the task: %v", err)
	}
	if state.MergedSHA != head || state.VerifiedAt == nil {
		t.Errorf("the state of the task holds the merged %q and the check %v, want %q and a moment",
			state.MergedSHA, state.VerifiedAt, head)
	}
	if _, err := os.Stat(host.worktreeOfTask); !os.IsNotExist(err) {
		t.Errorf("the worktree of the task is still there after the merge: %v", err)
	}
	// The task of the change is closed behind the merge, which is what the host does
	// and what the check after the merge asks about (docs/DESIGN.md §7h).
	found, err := host.Task(t.Context(), 7)
	if err != nil {
		t.Fatalf("read the task of the change: %v", err)
	}
	if found.State != "closed" {
		t.Errorf("the task of the change is %q, want it closed behind the merge", found.State)
	}
}

// cycleConfig is the file of the project of the cycle: the executor is a program of the
// test, the worktrees of its tasks are in a folder of the test, and the task of the
// change may change what the merge is about.
const cycleConfig = `
[project]
repo = "naghuale/crewflow"
language = "en"

[executor]
command = ["EXECUTOR", "{worktree}", "--prompt", "{prompt}"]
timeout = "1h"

[worktrees]
root = "WORKTREES/{repo}"

[merge]
reviewers = ["naghuale"]

[ci]
required = true
timeout = "1m"
`

// theEvents is what an agent of a real run writes: one line of JSON per event, each
// with the session it belongs to, and the last of them saying the change request is open.
const theEvents = `{"type":"text","sessionID":"ses_cycle","part":{"type":"text","text":"the change request is open"}}
`

// cycleTask is the whole task of the cycle, with the boundaries the change of it is
// checked against: it is about the merge, and the merge is what it was to write.
const cycleTask = "## Why\n\nA merge is done by hand.\n\n## What changes\n\ncrewflow merges a change.\n\n" +
	"## How to check it yourself\n\n1. Merge a change.\n\n## Out of scope\n\nAcceptance by the owner.\n\n" +
	"## Risks and decisions\n\nA merge moves a branch.\n\n<details>\n<summary>Technical part</summary>\n\n" +
	"### Acceptance criteria\n\n- [ ] the merge fast-forwards main\n\n" +
	"### Boundaries\n\n```\ninternal/merge/**\n```\n\n</details>\n"

// cycleHost is the whole cycle of a task on a real git: the tracker holds the task, the
// host holds the change request the run opened and what is written under it, and the
// task is closed when the branch of the host is at the head of the change — which is
// what a host does behind a change that has been merged (docs/DESIGN.md §7h).
type cycleHost struct {
	// repo is the checkout of the project and origin the bare repository that stands
	// for the host of it. Every answer this host gives about the change is read out of
	// them with real git, which is what makes the cycle a real one.
	repo   string
	origin string
	// branch is the branch of the change the run pushed, approvedBody what the run of
	// the review wrote under it, and worktreeOfTask the checkout of the task, which a
	// merge takes away.
	branch         string
	approvedBody   string
	worktreeOfTask string
	comments       []forge.Comment
}

// use makes every command of crewflow run on this host and this machine, and puts them
// back the way it found them.
func (h *cycleHost) use(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		taskRoles = roles.New
		taskRunEnv = taskrun.System
		reviewRoles = rolesAsOwner
		reviewGit = runGit
		mergeRoles = roles.AsOwner
		mergeGit = runGit
	})
	set := func(config.Config, forge.Env) (forge.Set, error) {
		return forge.Set{Forge: h, Tracker: h, CI: h}, nil
	}
	taskRoles, reviewRoles, mergeRoles = set, set, set
	reviewGit, mergeGit = runGit, runGit
	// The run of the test happens with real git and a real program where the agent is,
	// and the process of the run is the process of the test (docs/DESIGN.md §7).
	taskRunEnv = func(home string) taskrun.Env {
		env := taskrun.System(home)
		env.UserHome = os.Getenv("HOME")
		return env
	}
}

// Task is the task of the change, and it is closed when the branch of the host is at
// the head of the change: that is what a host does behind a change that has gone in.
func (h *cycleHost) Task(_ context.Context, number int) (forge.Task, error) {
	if number != 7 {
		return forge.Task{}, fmt.Errorf("could not find issue %d", number)
	}
	found := forge.Task{Number: 7, Title: "the merge of a change", Body: cycleTask, State: "open"}
	if h.merged() {
		found.State = "closed"
	}
	return found, nil
}

// ChangeRequest is the change request the run opened, with the head read out of the
// repository of the test: what the host says the head is, is what git holds. It is
// `merged` as soon as the branch of the host is at that head, which is what a host does
// behind a change that has gone in — and it is the shape `verify` is written against,
// since a check after a merge is made of a change nobody may merge any more
// (docs/DESIGN.md §7h).
func (h *cycleHost) ChangeRequest(_ context.Context, number int) (forge.ChangeRequest, error) {
	if number != 7 {
		return forge.ChangeRequest{}, fmt.Errorf("could not find change request %d", number)
	}
	if h.branch == "" {
		h.branch = branchOfCycle(h.repo)
	}
	state := "open"
	if h.merged() {
		state = "merged"
	}
	return forge.ChangeRequest{
		Number:     7,
		URL:        "https://github.com/naghuale/crewflow/pull/7",
		HeadBranch: h.branch,
		HeadSHA:    h.head(),
		BaseBranch: "main",
		State:      state,
		Body:       "Closes #7\n\n## What changed\n\ncrewflow merges a change.",
		Repository: "naghuale/crewflow",
	}, nil
}

// merged is whether the branch of the host has taken in the change, which is what a
// host says with the state of a change request and with the state of its task.
func (h *cycleHost) merged() bool {
	head := h.head()
	return head != "" && remoteOfCycleCmd(h.origin, "main") == head
}

// head is the commit at the head of the change, as the branch of it stands on the host.
func (h *cycleHost) head() string {
	if h.branch == "" {
		return ""
	}
	return strings.TrimSpace(gitOfCycleCmd(h.repo, "rev-parse", "refs/remotes/origin/"+h.branch))
}

// FindChangeRequest is the change request of the branch a run pushed.
func (h *cycleHost) FindChangeRequest(context.Context, string) (forge.ChangeRequest, bool, error) {
	change, err := h.ChangeRequest(context.Background(), 7)
	return change, err == nil, err
}

// Comments is what is written under the change, oldest first.
func (h *cycleHost) Comments(context.Context, int) ([]forge.Comment, error) {
	var records []forge.Comment
	if h.approvedBody != "" {
		records = append(records, forge.Comment{
			Author:    "naghuale",
			Body:      h.approvedBody,
			CreatedAt: time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC),
		})
	}
	return append(records, h.comments...), nil
}

// ChangedFiles is what the change touches, as git holds it against the default branch.
func (h *cycleHost) ChangedFiles(context.Context, int) ([]string, error) {
	out := strings.TrimSpace(gitOfCycleCmd(h.repo, "diff", "--name-only", "origin/main...refs/remotes/origin/"+h.branch))
	var files []string
	for raw := range strings.Lines(out) {
		if line := strings.TrimSpace(raw); line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// Checks is the check of the head of the change, green, from the app of the workflows of
// the host.
func (h *cycleHost) Checks(_ context.Context, sha string) ([]forge.CheckRun, error) {
	return []forge.CheckRun{{Name: "test", State: forge.CheckSuccess, App: "github-actions", SHA: sha}}, nil
}

// RequiredChecks is what the rules of the branch of the project demand of a change.
func (h *cycleHost) RequiredChecks(context.Context) ([]forge.RequiredCheck, error) {
	return []forge.RequiredCheck{{Name: "test", App: "github-actions"}}, nil
}

// Status is how the checks of a commit stand: the CI of this host is green.
func (h *cycleHost) Status(_ context.Context, sha string) (forge.CheckState, error) {
	return forge.CheckSuccess, nil
}

// HeadRef is the ref of the host that stands at the head of a change: the repository of
// the test keeps no pull requests, so the branch of the change stands for it.
func (h *cycleHost) HeadRef(int) string { return "refs/heads/" + h.branchOfHead() }

// SignedIn is the account the host speaks as, which is the owner of the repository: a
// record of a review counts only because a reviewer of the project wrote it.
func (h *cycleHost) SignedIn(context.Context) (string, error) { return "naghuale", nil }

// WriteComment is what an approval under the change is: the host keeps it, and a
// comment written by anybody else is not an approval the gate counts.
func (h *cycleHost) WriteComment(_ context.Context, number int, body string) error {
	if strings.HasPrefix(body, "REVIEW:") {
		h.approvedBody = body
		return nil
	}
	h.comments = append(h.comments, forge.Comment{
		Author:    "naghuale",
		Body:      body,
		CreatedAt: time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC),
	})
	return nil
}

// branchOfHead is the branch of the change, which the host names after the run pushed it.
func (h *cycleHost) branchOfHead() string {
	if h.branch == "" {
		h.branch = branchOfCycle(h.repo)
	}
	return h.branch
}

// Doctor says nothing: a test of the commands has a host of its own already.
func (h *cycleHost) Doctor(context.Context) []forge.Check { return nil }

// The host of the cycle is everything the commands ask of a host of a project.
var (
	_ forge.Tracker       = (*cycleHost)(nil)
	_ forge.Forge         = (*cycleHost)(nil)
	_ forge.CI            = (*cycleHost)(nil)
	_ forge.CheckLister   = (*cycleHost)(nil)
	_ forge.FileLister    = (*cycleHost)(nil)
	_ forge.HeadRef       = (*cycleHost)(nil)
	_ forge.CommentWriter = (*cycleHost)(nil)
	_ forge.SignedIn      = (*cycleHost)(nil)
)

// repositoryOfCycle is a checkout of a project with an origin of its own, so that a
// worktree of a task, a branch pushed and a fast-forward of main all happen against a
// real remote. Both are in folders of the test.
func repositoryOfCycle(t *testing.T) (repo, origin string) {
	t.Helper()
	repo, origin = t.TempDir(), t.TempDir()
	gitOfCycle(t, "", "init", "--quiet", "--bare", "--initial-branch=main", origin)
	gitOfCycle(t, "", "init", "--quiet", "--initial-branch=main", repo)
	gitOfCycle(t, repo, "config", "commit.gpgsign", "false")
	gitOfCycle(t, repo, "config", "user.email", "tests@crewflow.invalid")
	gitOfCycle(t, repo, "config", "user.name", "crewflow tests")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("crewflow\n"), 0o600); err != nil {
		t.Fatalf("write the first commit of the repository: %v", err)
	}
	gitOfCycle(t, repo, "add", "README.md")
	gitOfCycle(t, repo, "commit", "--quiet", "-m", "chore: the repository of a test")
	gitOfCycle(t, repo, "remote", "add", "origin", origin)
	gitOfCycle(t, repo, "push", "--quiet", "origin", "main")
	return repo, origin
}

// branchOfCycle is the branch of the change a run pushed, which is the only one the
// repository of the test has besides main.
func branchOfCycle(repo string) string {
	for _, line := range strings.Split(strings.TrimSpace(gitOfCycleCmd(repo, "branch", "--list", "crewflow/*", "--format=%(refname:short)")), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			return name
		}
	}
	return ""
}

// remoteOfCycle is where the branch of the host is, as `git ls-remote` says it.
func remoteOfCycle(t *testing.T, origin, branch string) string {
	t.Helper()
	out := gitOfCycleCmd("", "ls-remote", origin, "refs/heads/"+branch)
	fields := strings.Fields(out)
	if len(fields) < 2 {
		t.Fatalf("the branch %s of the host is nowhere: %q", branch, out)
	}
	return fields[0]
}

// gitOfCycle runs one git in dir and stops the test when it says no.
func gitOfCycle(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out := gitOfCycleCmd(dir, args...); out != "" && strings.Contains(out, "fatal") {
		t.Fatalf("git %v: %s", args, out)
	}
}

// gitOfCycleCmd is what a command of git wrote, with nothing of the settings of the
// machine in it: a test of the cycle must hold on a machine where nothing is configured
// either.
func gitOfCycleCmd(dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=crewflow tests", "GIT_AUTHOR_EMAIL=tests@crewflow.invalid",
		"GIT_COMMITTER_NAME=crewflow tests", "GIT_COMMITTER_EMAIL=tests@crewflow.invalid",
	)
	out, _ := cmd.CombinedOutput()
	return strings.TrimSpace(string(out))
}

// remoteOfCycleCmd is where the branch of the host is, without stopping a test: the host
// of the cycle asks it while it answers for the task.
func remoteOfCycleCmd(origin, branch string) string {
	return strings.Fields(gitOfCycleCmd("", "ls-remote", origin, "refs/heads/"+branch))[0:1][0]
}

// fakeExecutor is a program that stands in for an agent: a script of the test, so that a
// run is seen working with a real program, a closed stdin and a real exit code. Its name
// is the name of the program, because that is what the profile of a run is chosen by.
func fakeExecutor(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	script := "#!/bin/sh\nset -e\ncd \"$1\"\n" + body
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write the fake executor: %v", err)
	}
	return path
}

// commit is what an executor leaves in the worktree of a task: a file with a path, added
// and committed, so that the work of the run is on the branch of the task.
func commit(path, content string) string {
	return "mkdir -p '" + filepath.Dir(path) + "'\n" +
		"printf '%b' '" + content + "' > '" + path + "'\n" +
		"git add '" + path + "'\n" +
		"git commit --quiet -m 'feat: the work of the run'\n"
}
