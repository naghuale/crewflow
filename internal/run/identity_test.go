package run

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/secret"
)

// theToken is the token of the App in the environment of a run of a test: it is
// longer than the shortest value a redactor looks for, and a test that says
// otherwise would not be a test of a run in the mode of the bot.
const theToken = "ghs_16C7e42F292c6912E7710c838347Ae178B4a"

// theBot is the identity of a run whose executor works as the GitHub App of the
// project: the token, the name and the address its commits are made by, the helper
// git takes a fresh token from, and the value that must not reach a journal
// (docs/DESIGN.md §7i).
func theBot() forge.Identity {
	return forge.Identity{
		Mode:        forge.ModeBot,
		Description: "bot — GitHub App crewflow-executor (installation 12345)",
		Env: []string{
			"GH_TOKEN=" + theToken,
			"GIT_AUTHOR_NAME=crewflow-executor[bot]",
			"GIT_AUTHOR_EMAIL=1987+crewflow-executor[bot]@users.noreply.github.com",
			"GIT_COMMITTER_NAME=crewflow-executor[bot]",
			"GIT_COMMITTER_EMAIL=1987+crewflow-executor[bot]@users.noreply.github.com",
		},
		GitConfig: map[string]string{
			"credential.helper":      "crewflow auth git-credential",
			"credential.useHttpPath": "true",
		},
		Secrets: []string{theToken},
	}
}

// theOwner is the identity of a run whose executor works as the person who runs
// crewflow: the login of the person is what the executor already has, and a run is
// handed nothing at all.
func theOwner() forge.Identity {
	return forge.Identity{
		Mode:        forge.ModeOwner,
		Description: "owner — the login gh naghuale (shared rights)",
	}
}

// TestRunInTheModeOfTheBotHandsTheExecutorTheTokenOfTheApp: a run in the mode of the
// bot is the whole of §7i in one run — the executor is handed a token of the app, its
// commits are made by the account of the app, and the journal says whose name the run
// went under before anything else, because a person reading the journal afterwards has
// to know it without reading the state file as well.
func TestRunInTheModeOfTheBotHandsTheExecutorTheTokenOfTheApp(t *testing.T) {
	m := newMachine(t)
	m.answers["opencode"] = answer{stdout: theRun}
	m.answers["git config"] = answer{}
	host := &host{task: taskOf(43), opened: true, identity: theBot()}
	cfg := projectOf(t, m.worktrees, "")

	result, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	for _, want := range theBot().Env {
		if !slices.Contains(m.envOf(), want) {
			t.Errorf("the executor was started with %v, want %q in it", m.envOf(), want)
		}
	}
	// The mode of a run is in its journal as its first line, in its state and in
	// what the orchestrator is told: a report that names the outcome of a run and
	// not whose name it went under is half a report (§7i).
	if got, want := firstLineOf(read(t, result.Journal)),
		"crewflow: executor: "+theBot().Description; got != want {
		t.Errorf("the journal starts with %q, want %q", got, want)
	}
	// A state of a task keeps the mode of every attempt of it and not the one line a
	// report of a run shows: that line is in the journal and in the result of the
	// run, and the state is a list of facts for a list of runs to read (§7h).
	state := stateOf(t, m, 43)
	if got, want := state.Attempts[0].Identity.Mode, "bot"; got != want {
		t.Errorf("the state holds the mode %q, want %q", got, want)
	}
	if got := state.Attempts[0].Executor; got != "opencode" {
		t.Errorf("the state holds the executor %q, want the agent of the attempt", got)
	}
	if state.Schema != Schema {
		t.Errorf("the state is of the format %d, want %d", state.Schema, Schema)
	}
	if want := (Identity{Mode: "bot", Description: theBot().Description}); result.Identity != want {
		t.Errorf("the result holds the identity %+v, want %+v", result.Identity, want)
	}
	// The settings of git are the ones of the worktree of the run and not the ones of
	// the repository: a push of the checkout of the person must not go through the
	// helper of a run (§7i).
	if !m.ranConfig("--worktree") {
		t.Errorf("git was asked %v, want the helper of the credentials in the worktree alone", m.lines())
	}
}

// TestTheWorktreeOfARunResetsTheHelpersOfTheMachineBeforeTheHelperOfCrewflow: the
// settings of git of a worktree are a list, and a run writes the empty value of each of
// them before the value of the adapter of the host. An empty `credential.helper` takes
// the list of helpers out of the files of the system and of the user, and the helper of
// the machine is the one that answers first: `osxkeychain` of the file of the system of
// macOS has the token of the login of a person, and after the push of a run went through
// it is handed the token of the App of that run — a secret in the keychain of the owner
// without their knowing, and an hour later a stale token in front of the helper of
// crewflow (F-116, R6, #141).
//
// The order is the whole of it: a value written after the reset is the only one git is
// left with, and `--replace-all` in front of it is what keeps a worktree that is run a
// second time from holding the settings of the build before it (§7i).
func TestTheWorktreeOfARunResetsTheHelpersOfTheMachineBeforeTheHelperOfCrewflow(t *testing.T) {
	m := newMachine(t)
	m.answers["opencode"] = answer{stdout: theRun}
	m.answers["git config"] = answer{}
	host := &host{task: taskOf(43), opened: true, identity: theBot()}
	cfg := projectOf(t, m.worktrees, "")

	if _, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo}); err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	want := [][]string{
		{"config", "--worktree", "--replace-all", "credential.helper", ""},
		{"config", "--worktree", "--add", "credential.helper", "crewflow auth git-credential"},
		{"config", "--worktree", "--replace-all", "credential.useHttpPath", ""},
		{"config", "--worktree", "--add", "credential.useHttpPath", "true"},
	}
	written := m.credentialsOfTheWorktree()
	if len(written) != len(want) {
		t.Fatalf("git was asked %v, want the settings of the credentials in the order %v", written, want)
	}
	for i, args := range want {
		if !slices.Equal(written[i], args) {
			t.Errorf("git was asked %v, want %v", written[i], args)
		}
	}
}

// TestRunInTheModeOfTheOwnerHandsTheExecutorNothing: the mode of the owner is what
// crewflow has always done, and the proof of that is that a run of it is handed
// nothing: no token, no name of a bot, and no settings of git in the worktree.
func TestRunInTheModeOfTheOwnerHandsTheExecutorNothing(t *testing.T) {
	m := newMachine(t)
	m.answers["opencode"] = answer{stdout: theRun}
	host := &host{task: taskOf(43), opened: true, identity: theOwner()}
	cfg := projectOf(t, m.worktrees, "")

	result, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	for _, entry := range m.envOf() {
		for _, unwanted := range []string{"GH_TOKEN", "GIT_AUTHOR_NAME", "GIT_COMMITTER_EMAIL"} {
			if strings.HasPrefix(entry, unwanted+"=") {
				t.Errorf("the executor was started with %q, want no %s in the mode of the owner", entry, unwanted)
			}
		}
	}
	if m.ranConfig("--worktree") {
		t.Errorf("git was asked %v, want no settings of the worktree in the mode of the owner", m.lines())
	}
	if got, want := firstLineOf(read(t, result.Journal)),
		"crewflow: executor: "+theOwner().Description; got != want {
		t.Errorf("the journal starts with %q, want %q", got, want)
	}
}

// TestRunInTheModeOfTheBotKeepsTheTokenOutOfTheJournal: the token of a run is in the
// environment of the executor, and an agent that prints its own environment is one
// line of a journal — a file kept for ever and pasted into issues, which is the one
// place a token of an hour must not be (docs/DESIGN.md §7e, §7i).
func TestRunInTheModeOfTheBotKeepsTheTokenOutOfTheJournal(t *testing.T) {
	m := newMachine(t)
	// The executor prints what it was given, as an agent that is asked about its own
	// environment would, and says on the way out that the token is what it holds.
	m.answers["opencode"] = answer{
		stdout: "GH_TOKEN=" + theToken + " GIT_AUTHOR_NAME=crewflow-executor[bot]\n",
		stderr: "gh: the token of the run is " + theToken + "\n",
	}
	m.answers["git config"] = answer{}
	host := &host{task: taskOf(43), opened: true, identity: theBot()}
	cfg := projectOf(t, m.worktrees, "")

	result, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	for _, file := range []struct{ name, path string }{
		{"journal", result.Journal},
		{"the way out of the run", result.ErrorJournal},
	} {
		got := read(t, file.path)
		if strings.Contains(got, theToken) {
			t.Errorf("the %s holds the token of the run:\n%s", file.name, got)
		}
		if !strings.Contains(got, secret.Redacted) {
			t.Errorf("the %s holds %q, want the token of the run taken out of it", file.name, got)
		}
	}
}

// TestRunOpensTheChangeRequestTheExecutorDidNot: a run of an agent is longer than the
// life of a token, and a run whose branch is on the host and whose request nobody
// opened is work that nobody asked about. The account of the app opens it, and the
// run says so with the outcome it is for (docs/DESIGN.md §7i).
func TestRunOpensTheChangeRequestTheExecutorDidNot(t *testing.T) {
	m := newMachine(t)
	m.answers["opencode"] = answer{stdout: theRun}
	m.answers["git config"] = answer{}
	// The branch of the run is on the host, and there is no request of it: that is
	// what `git ls-remote` is asked about, and what a run that ended without a
	// request is judged by (§7i).
	m.answers["git ls-remote"] = answer{stdout: "9f1c0de9f1c0de9f1c0de9f1c0de9f1c0de9f1c0\trefs/heads/crewflow/43-the-run-of-a-task"}
	opened := &forge.ChangeRequest{Number: 44, URL: "https://github.com/naghuale/crewflow/pull/44"}
	host := &host{task: taskOf(43), identity: theBot(), openedByTheRun: opened}
	cfg := projectOf(t, m.worktrees, "")

	result, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	if result.Outcome != ChangeRequestOpened {
		t.Fatalf("the outcome = %q, want %q: the work of the task is on the host", result.Outcome, ChangeRequestOpened)
	}
	if result.ChangeRequest == nil || result.ChangeRequest.Number != 44 {
		t.Fatalf("the result holds the request %+v, want the one crewflow opened", result.ChangeRequest)
	}
	if !strings.Contains(host.body, "Closes #43") {
		t.Errorf("the request was opened with %q, want the line the cycle of a task turns on", host.body)
	}
	if host.title != taskOf(43).Title {
		t.Errorf("the request was opened as %q, want the title of the task", host.title)
	}
	state := stateOf(t, m, 43)
	if state.Change == nil || state.Change.URL != opened.URL {
		t.Errorf("the state holds the request %+v, want the one crewflow opened", state.Change)
	}
}

// TestRunOpensNothingWhenTheBranchIsNotOnTheHost: there is no work to put under a
// request, and a run that pushed nothing is a run whose outcome is no-change-request
// whatever the account of the app could do about it (§7i).
func TestRunOpensNothingWhenTheBranchIsNotOnTheHost(t *testing.T) {
	m := newMachine(t)
	m.answers["opencode"] = answer{stdout: theRun}
	m.answers["git config"] = answer{}
	m.answers["git ls-remote"] = answer{}
	host := &host{task: taskOf(43), identity: theBot(), openedByTheRun: &forge.ChangeRequest{Number: 44}}
	cfg := projectOf(t, m.worktrees, "")

	result, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	if result.Outcome != NoChangeRequest {
		t.Errorf("the outcome = %q, want %q: nothing was pushed", result.Outcome, NoChangeRequest)
	}
	if host.title != "" {
		t.Errorf("the request was opened as %q, want nothing opened", host.title)
	}
}

// TestRunOfTheOwnerOpensNothingForItself: a run in the mode of the owner has no
// account of the host of its own, and a branch without a request stays a run whose
// outcome is no-change-request — a report of a run must not name work that a review
// would take for the owner's own (§7i).
func TestRunOfTheOwnerOpensNothingForItself(t *testing.T) {
	m := newMachine(t)
	m.answers["opencode"] = answer{stdout: theRun}
	host := &host{task: taskOf(43), identity: theOwner(), openedByTheRun: &forge.ChangeRequest{Number: 44}}
	cfg := projectOf(t, m.worktrees, "")

	result, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	if result.Outcome != NoChangeRequest {
		t.Errorf("the outcome = %q, want %q", result.Outcome, NoChangeRequest)
	}
	if host.title != "" {
		t.Errorf("the request was opened as %q, want nothing opened", host.title)
	}
	if m.askedFor("git ls-remote") {
		t.Errorf("git was asked %v, want no question about the branch in the mode of the owner", m.lines())
	}
}

// TestRunInTheModeOfTheBotThatCannotStartSaysWhy: a run in the mode of the bot whose
// host cannot hand out a token is a run that is not started, and nothing of it is
// left behind: an attempt of a task in a state that never went is a lie a person
// reads (docs/DESIGN.md §7i).
func TestRunInTheModeOfTheBotThatCannotStartSaysWhy(t *testing.T) {
	m := newMachine(t)
	// A host that cannot answer: the key of the app was never imported, or the store
	// of the machine could not be read.
	host := &host{task: taskOf(43), identity: theBot(), noIdentity: errors.New("the private key of the app is not in the store")}
	cfg := projectOf(t, m.worktrees, "")

	_, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
	if err == nil {
		t.Fatal("Run returned no error, want the refusal of the host")
	}
	if !strings.Contains(err.Error(), "the identity of the executor") {
		t.Errorf("Run = %v, want the error to name whose name the run was refused", err)
	}
	if m.askedFor("opencode") {
		t.Errorf("the executor was started %v, want nothing started", m.lines())
	}
}

// ranConfig is whether any command of git was run with the given argument, which is
// how a test sees the settings of the worktree of a run (§7i).
func (m *machine) ranConfig(argument string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, command := range m.ran {
		if command.program == "git" && slices.Contains(command.args, argument) {
			return true
		}
	}
	return false
}

// credentialsOfTheWorktree are the commands git was asked to write the settings of the
// credentials of a worktree of a run with, in the order it was asked them, which is the
// order git reads the values in (§7i, #141).
func (m *machine) credentialsOfTheWorktree() [][]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var written [][]string
	for _, command := range m.ran {
		if command.program != "git" || !slices.Contains(command.args, "--worktree") {
			continue
		}
		if slices.Contains(command.args, "credential.helper") ||
			slices.Contains(command.args, "credential.useHttpPath") {
			written = append(written, slices.Clone(command.args))
		}
	}
	return written
}

// askedFor is whether a program was run with the given name, which is how a test sees
// what a run asked the machine for.
func (m *machine) askedFor(program string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, command := range m.ran {
		if command.program == program {
			return true
		}
	}
	return false
}

// ranCommand is whether any command of git was run whose arguments begin with the given
// words, which is how a test sees what a run asked of the repository of the project
// besides the worktree of the task.
func (m *machine) ranCommand(words string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, command := range m.ran {
		if command.program == "git" && strings.HasPrefix(strings.Join(command.args, " "), words) {
			return true
		}
	}
	return false
}

// The pre-push hook of a run is a guard behind the rules of the branch on the host, and
// it is the only guard that lives in the worktree of the run itself: the rules of the
// host are the ones an app cannot go around, and a hook is a file of a machine that
// anyone may delete. What it has to do is refuse a push of anything but the branch of
// the task; that it says nothing about anybody else's worktree is what the folder of
// hooks of this task alone is for, and a test against a real git proves it
// (docs/DESIGN.md §7i).
func TestPrePushRefusesEverythingButTheBranchOfTheTask(t *testing.T) {
	branch := "crewflow/43-the-run-of-a-task"
	// The hook of a worktree of a run is a program of its own, and a test of it
	// starts it as git starts one: with the lines of a push on its standard input.
	lines := []string{
		"refs/heads/" + branch + " 9f1c0de refs/heads/" + branch + " 9e37237",
		"refs/heads/main 9f1c0de refs/heads/main 9e37237",
		"refs/heads/" + branch + " 9f1c0de refs/heads/tags/v1",
		"refs/heads/crewflow/44-other-task 9f1c0de refs/heads/crewflow/44-other-task 9e37237",
		"(delete) 0000000000000000000000000000000000000000 refs/heads/" + branch,
	}
	want := []bool{false, true, true, true, true}
	for i, line := range lines {
		refused, out, err := pushOfTheTest(t, prePush(branch), line)
		if err != nil {
			t.Fatalf("the hook on %q: %v", line, err)
		}
		if refused != want[i] {
			t.Errorf("the hook refused %q: %t, want %t (it said %q)", line, refused, want[i], out)
		}
	}
}

// pushOfTheTest runs a pre-push hook the way git runs one: with one line of a push on
// its standard input. It says whether the push was refused, what the hook printed and
// what went wrong — a hook of a test is a program of the test, and a test of it is a
// test of that program.
func pushOfTheTest(t *testing.T, hook, line string) (refused bool, said string, err error) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skipf("sh is not installed: %v", err)
	}
	path := filepath.Join(t.TempDir(), "pre-push")
	if err := os.WriteFile(path, []byte(hook), 0o700); err != nil {
		t.Fatalf("write the hook of the test: %v", err)
	}
	cmd := exec.Command(path)
	cmd.Stdin = strings.NewReader(line + "\n")
	out, err := cmd.CombinedOutput()
	refused = err != nil
	var exit *exec.ExitError
	if refused && !errors.As(err, &exit) {
		return false, string(out), err
	}
	return refused, strings.TrimSpace(string(out)), nil
}
