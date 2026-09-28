package run

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/naghuale/crewflow/internal/proc"
)

// theEvents is what an agent of a real run writes: one line of JSON per event, each
// with the session it belongs to. A run that did the work says so in the last of
// them.
const theEvents = `{"type":"text","sessionID":"ses_fake","part":{"type":"text","text":"I did the work."}}
{"type":"text","sessionID":"ses_fake","part":{"type":"text","text":"The change request is open."}}
`

// TestRunAgainstRealGit runs a task the way a person runs it: a real git, a real
// worktree made out of a real repository that has an origin of its own, and a real
// program where the agent is. Nothing of it reaches a tracker, a host or the home of
// the person: the host of this test is a struct, and the home of crewflow is a
// folder of the test.
func TestRunAgainstRealGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not installed: %v", err)
	}
	cases := []struct {
		name string
		// program is the name of the fake executor, and the name of the program is
		// what the profile of a run is chosen by: "opencode" is read as a stream of
		// events and a program of any other name is not.
		program string
		// body is what the fake executor does in the worktree of the task, after it
		// has said what it has to say.
		body   string
		stderr string
		want   Kind
		// wantOutside are the files the run changed that the task was not to change,
		// and wantFiles every file it changed.
		wantOutside []string
		wantFiles   []string
		wantReason  string
	}{
		{
			name:      "the run did the work and opened nothing",
			program:   "opencode",
			body:      commit("internal/run/run.go", "package run\n"),
			want:      NoChangeRequest,
			wantFiles: []string{"internal/run/run.go"},
		},
		{
			name:        "the run changed a file the task was not to change",
			program:     "opencode",
			body:        commit("internal/run/run.go", "package run\n") + commit("docs/DESIGN.md", "changed\n"),
			want:        OutOfScope,
			wantOutside: []string{"docs/DESIGN.md"},
			// git holds its files in the order of their paths, whatever order they
			// were changed in.
			wantFiles: []string{"docs/DESIGN.md", "internal/run/run.go"},
		},
		{
			name:    "the run was refused a permission and stopped",
			program: "opencode",
			body:    "exit 0\n",
			stderr:  "INFO  service=default starting\n! permission requested: external_directory (/tmp/*); auto-rejecting\n",
			want:    BlockedPermission,
		},
		{
			name:       "an agent crewflow knows nothing about stopped by itself",
			program:    "agent",
			body:       "printf 'BLOCKED: I could not go on\\n'\n",
			want:       Blocked,
			wantReason: "I could not go on",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, origin := repository(t)
			home := t.TempDir()
			executor := fakeExecutor(t, tc.program, said(tc.stderr)+"printf '%s' '"+theEvents+"'\n"+tc.body)
			host := &host{task: taskOf(43)}
			cfg := projectOf(t, t.TempDir(), "1h")
			cfg.Executor.Command = []string{executor, "{worktree}", "--prompt", "{prompt}"}

			result, err := Run(t.Context(), System(home), cfg, host.set(), Request{Number: 43, RepoDir: repo})
			if err != nil {
				t.Fatalf("Run returned an error: %v", err)
			}

			if result.Outcome != tc.want {
				t.Errorf("the outcome = %q, want %q (reason %q)", result.Outcome, tc.want, result.Reason)
			}
			if result.Reason != tc.wantReason {
				t.Errorf("the reason = %q, want %q", result.Reason, tc.wantReason)
			}
			if !slices.Equal(result.Outside, tc.wantOutside) {
				t.Errorf("the files outside the boundaries = %v, want %v", result.Outside, tc.wantOutside)
			}
			// What the program wrote is in the journal, and the state of the task is
			// where a continuation of the run is read from.
			if got := read(t, result.Journal); !strings.Contains(got, `"sessionID":"ses_fake"`) {
				t.Errorf("the journal holds %q, want the events of the run", got)
			}
			if got := read(t, result.ErrorJournal); tc.stderr != "" && !strings.Contains(got, tc.stderr) {
				t.Errorf("the journal of the way out holds %q, want what the program said", got)
			}
			state, err := LoadState(newJournals(home, "naghuale-crewflow").StatePath(43))
			if err != nil {
				t.Fatalf("load the state of the task: %v", err)
			}
			if len(state.Attempts) != 1 || state.Attempts[0].Outcome != tc.want {
				t.Errorf("the state holds %+v, want one attempt that ended as %q", state.Attempts, tc.want)
			}
			// The state names the process the run was in, and the machine agrees: it
			// is the process that is running this test, and a list of runs reads that
			// process to tell a run that is going from one that is over.
			process, named := state.Attempts[0].Process()
			if !named {
				t.Error("the state of the task names no process, want the one the run was in")
			} else if !proc.System().Alive(process) {
				t.Errorf("the machine does not know of the process %+v the run was in", process)
			}

			// The worktree is a worktree of a real repository: it is on the branch of
			// the task and holds the work of the run, and the origin of the project
			// is where the branch would be pushed from.
			if branch := gitOut(t, result.Worktree, "rev-parse", "--abbrev-ref", "HEAD"); branch != result.Branch {
				t.Errorf("the worktree is on %q, want the branch of the task %q", branch, result.Branch)
			}
			if tc.wantFiles != nil {
				changed := gitOut(t, result.Worktree, "diff", "--name-only", "origin/main...HEAD")
				if !slices.Equal(split(changed), tc.wantFiles) {
					t.Errorf("the worktree holds the changes %q, want %v", changed, tc.wantFiles)
				}
			}
			if _, err := os.Stat(filepath.Join(origin, "refs")); err != nil {
				t.Errorf("the origin of the repository is not a repository: %v", err)
			}
		})
	}
}

// TestRunAgainstRealGitContinuesTheSession: the run that goes on after a review is
// the same worktree and the same session, and it is the second attempt of the task
// in the state of it.
func TestRunAgainstRealGitContinuesTheSession(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not installed: %v", err)
	}
	repo, _ := repository(t)
	// The fake executor writes down the arguments it was run with, so that a test
	// can see which session it was asked to go on in.
	executor := fakeExecutor(t, "opencode",
		"printf '%s\\n' \"$*\" >> args\n"+
			"printf '%s' '"+theEvents+"'\n"+
			commit("internal/run/run.go", "package run\n"))
	host := &host{task: taskOf(43)}
	cfg := projectOf(t, t.TempDir(), "1h")
	cfg.Executor.Command = []string{executor, "{worktree}", "--prompt", "{prompt}"}
	home := t.TempDir()

	if _, err := Run(t.Context(), System(home), cfg, host.set(), Request{Number: 43, RepoDir: repo}); err != nil {
		t.Fatalf("the first run returned an error: %v", err)
	}
	result, err := Run(t.Context(), System(home), cfg, host.set(),
		Request{Number: 43, RepoDir: repo, Continue: "the review asked for a test of the timeout"})
	if err != nil {
		t.Fatalf("the second run returned an error: %v", err)
	}

	asked := read(t, filepath.Join(result.Worktree, "args"))
	if !strings.Contains(asked, "--session ses_fake") {
		t.Errorf("the worktree holds the arguments of both runs:\n%s, want the second one to name the session", asked)
	}
	if strings.Count(asked, "You are the executor of crewflow") != 1 {
		t.Errorf("the worktree holds the arguments of both runs:\n%s, want the whole assignment only in the first", asked)
	}
	if result.Attempt != 2 || !result.Continued {
		t.Errorf("the second run is the attempt %d (continued %t), want the second and a continuation", result.Attempt, result.Continued)
	}
	if result.Session != "ses_fake" {
		t.Errorf("the session of the run = %q, want the one the events named", result.Session)
	}
	if branch := gitOut(t, result.Worktree, "rev-parse", "--abbrev-ref", "HEAD"); branch != result.Branch {
		t.Errorf("the second run worked in a worktree on %q, want the branch of the task %q", branch, result.Branch)
	}
}

// TestRunKeepsTheScratchOfTheExecutorOutOfTheProject runs a task against a real
// repository, twice, and looks at what a run leaves in it: the folder the executor
// keeps its temporary files in, the environment it was pointed at, and the local
// ignore of git that keeps that folder out of the project. The `.gitignore` of the
// repository is not touched: a repository a person owns is not changed because a task
// was run in it.
func TestRunKeepsTheScratchOfTheExecutorOutOfTheProject(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not installed: %v", err)
	}
	repo, _ := repository(t)
	ignore := filepath.Join(repo, ".gitignore")
	if err := os.WriteFile(ignore, []byte("dist/\n"), 0o600); err != nil {
		t.Fatalf("write the .gitignore of the project: %v", err)
	}
	gitOf(t, repo, "add", ".gitignore")
	gitOf(t, repo, "commit", "-m", "chore: the ignore of the project")
	// The fake executor writes down the temporary folder it was given and the
	// arguments it was run with, as a program of a real run would find them.
	executor := fakeExecutor(t, "agent",
		"printf '%s\\n' \"$*\" > args\n"+
			"printf 'TMPDIR=%s\\nTMP=%s\\nTEMP=%s\\n' \"$TMPDIR\" \"$TMP\" \"$TEMP\" > tempdir\n"+
			"printf '%s' '"+theEvents+"'\n")
	host := &host{task: taskOf(43)}
	cfg := projectOf(t, t.TempDir(), "1h")
	cfg.Executor.Command = []string{executor, "{worktree}", "--prompt", "{prompt}"}
	home := t.TempDir()

	var result Result
	for _, request := range []Request{
		{Number: 43, RepoDir: repo},
		{Number: 43, RepoDir: repo, Continue: "the review asked for a test of the scratch"},
	} {
		run, err := Run(t.Context(), System(home), cfg, host.set(), request)
		if err != nil {
			t.Fatalf("Run returned an error: %v", err)
		}
		result = run
	}

	scratch := filepath.Join(result.Worktree, ".scratch", "tmp")
	if info, err := os.Stat(scratch); err != nil {
		t.Errorf("the folder %s of the temporary files of the executor is not there: %v", scratch, err)
	} else if !info.IsDir() {
		t.Errorf("%s is not a folder, want the folder the executor keeps its temporary files in", scratch)
	}
	want := fmt.Sprintf("TMPDIR=%s\nTMP=%s\nTEMP=%s\n", scratch, scratch, scratch)
	if got := read(t, filepath.Join(result.Worktree, "tempdir")); got != want {
		t.Errorf("the executor was started with the temporary folder %q, want %q", got, want)
	}
	// The assignment names the folder as a path, so that the agent is not left to
	// work out where it may write.
	if asked := read(t, filepath.Join(result.Worktree, "args")); !strings.Contains(asked, scratch) {
		t.Errorf("what the executor was asked does not name %s:\n%s", scratch, asked)
	}
	// The scratch of every worktree of the repository is ignored once, in the local
	// ignore of git, and the .gitignore of the project is as the person left it.
	exclude := filepath.Join(repo, ".git", "info", "exclude")
	if got := strings.Count(read(t, exclude), scratchIgnore); got != 1 {
		t.Errorf("%s holds the scratch %d times after two runs, want it once", exclude, got)
	}
	if got := read(t, ignore); got != "dist/\n" {
		t.Errorf("the .gitignore of the project holds %q, want it as it was", got)
	}
	if status := gitOut(t, result.Worktree, "status", "--porcelain"); strings.Contains(status, scratchIgnore) {
		t.Errorf("git in the worktree holds the scratch of the run:\n%s, want it out of the way", status)
	}
}

// TestRunGivesTheExecutorTheRightsToReadTheDependenciesOfTheProject runs a task
// against a real repository with a fake executor that writes down the environment it
// was started with. The folders the project named in [access] are opened to it for
// reading and closed for writing, the places of secrets are closed, and the journal of
// the run says what it was started with (docs/DESIGN.md §7d).
//
// The home of the person is reached through a link, as it is on macOS where `/var` and
// `/tmp` lead into `/private`: the rules of a run name a place in both the spelling
// the project wrote and the one the machine holds, because an agent asks about a path
// in the spelling it wrote. The link is made here, so that the case is the same
// everywhere and does not wait for a runner to have links of its own.
func TestRunGivesTheExecutorTheRightsToReadTheDependenciesOfTheProject(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not installed: %v", err)
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skipf("sh is not installed: %v", err)
	}
	repo, _ := repository(t)
	executor := fakeExecutor(t, "opencode",
		"printf '%s' \"$OPENCODE_CONFIG_CONTENT\" > rights\n"+
			"printf '%s' '"+theEvents+"'\n")
	host := &host{task: taskOf(43)}
	// The folder of the home is taken as this machine holds it, or the two spellings
	// the test is about would be three: the one of the test runner, the one of the link
	// and the one the link leads to.
	held := onThisMachine(t, t.TempDir())
	userHome := filepath.Join(held, "link")
	if err := os.Symlink(held, userHome); err != nil {
		t.Fatalf("make the link of the home: %v", err)
	}
	// The cache of the modules is named through the link, the way the tool of the
	// project would name it, and both the paths of the test and the ones this machine
	// holds are the ones no secret of the person who runs the tests is in.
	through := filepath.Join(userHome, "go", "pkg", "mod")
	modules := filepath.Join(held, "go", "pkg", "mod")
	if err := os.MkdirAll(modules, 0o700); err != nil {
		t.Fatalf("make %s: %v", modules, err)
	}
	cfg := projectOf(t, t.TempDir(), "1h")
	cfg.Executor.Command = []string{executor, "{worktree}", "--prompt", "{prompt}"}
	// The project says how to find its dependencies, because the paths are different
	// on every machine: here a command that prints the folder of the test.
	cfg.Access.ReadFrom = [][]string{{"sh", "-c", "printf '%s\\n' '" + through + "'"}}
	env := System(t.TempDir())
	env.UserHome, env.Environ = userHome, nil

	result, err := Run(t.Context(), env, cfg, host.set(), Request{Number: 43, RepoDir: repo})
	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	// What the executor was given, as a program of a real run would find it: the folder
	// of the dependencies in both of the spellings of it, the same folder closed for
	// writing, a place of secrets as the place itself and as everything under it in both
	// spellings, a secret that is a file and is not there, and the `.env` of the worktree
	// itself closed to reading as well.
	rights := read(t, filepath.Join(result.Worktree, "rights"))
	for _, want := range []string{
		`"` + through + `/**":"allow"`,                        // the dependencies, through the link
		`"` + modules + `/**":"allow"`,                        // the same folder, as this machine holds it
		`"` + modules + `/**":"deny"`,                         // and closed for writing, in both spellings
		`"` + through + `/**":"deny"`,                         //
		`"` + filepath.Join(held, ".ssh") + `":"deny"`,        // the keys of the person
		`"` + filepath.Join(held, ".ssh") + `/**":"deny"`,     // and everything in them
		`"` + filepath.Join(userHome, ".ssh") + `/**":"deny"`, // through the link as well
		`"` + filepath.Join(held, ".netrc") + `":"deny"`,      // a secret that is a file,
		`"` + filepath.Join(held, ".netrc") + `/**":"deny"`,   // and there may be none of it
		`"**/.env":"deny"`,                                    // the environment of the project
		`"read":{"**/.env":"deny"`,                            // closed to the tool that reads
	} {
		if !strings.Contains(rights, want) {
			t.Errorf("the rights of the executor are %s,\nwant %q in them", rights, want)
		}
	}
	// The journal of the run opens with the lines of crewflow itself: whose name the
	// run went under, and the policy it was started with, so that a person reading it
	// afterwards sees both without asking anything (docs/DESIGN.md §7d, §7i).
	journal := read(t, result.Journal)
	if want := "crewflow: executor: owner — "; !strings.HasPrefix(journal, want) {
		t.Errorf("the journal starts with %q, want the mode of the run in it", firstLineOf(journal))
	}
	if !strings.Contains(journal, "crewflow: the executor may read outside the worktree: "+through+", "+modules) {
		t.Errorf("the journal holds %q, want the rights of the run in it", firstLineOf(journal))
	}
	// A run that named what it may read and got it has nothing to say on the way out.
	if said := read(t, result.ErrorJournal); said != "" {
		t.Errorf("the way out of the run holds %q, want nothing said", said)
	}
}

// onThisMachine is the path as the machine of a test holds it: a folder of a temporary
// folder of a test may be under a link, and the rights of a run name a place in both
// the spelling it was written in and the one every link above it makes.
func onThisMachine(t *testing.T, path string) string {
	t.Helper()
	followed, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("follow %s: %v", path, err)
	}
	return followed
}

// firstLineOf is the first line of a text, for a test that is about where a file
// starts and not about all of it.
func firstLineOf(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line
}

// repository is a git repository with a commit in it and an origin of its own, so
// that a worktree can be made out of the default branch of it as a real one. It is
// made in a folder of the test and thrown away with it.
func repository(t *testing.T) (repo, origin string) {
	t.Helper()
	repo, origin = t.TempDir(), t.TempDir()
	gitOf(t, repo, "init", "--initial-branch=main")
	gitOf(t, repo, "config", "user.email", "executor@crewflow.test")
	gitOf(t, repo, "config", "user.name", "crewflow test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("crewflow\n"), 0o600); err != nil {
		t.Fatalf("write the first commit of the repository: %v", err)
	}
	gitOf(t, repo, "add", "README.md")
	gitOf(t, repo, "commit", "-m", "chore: the repository of a test")
	gitOf(t, origin, "init", "--bare", "--initial-branch=main")
	gitOf(t, repo, "remote", "add", "origin", origin)
	gitOf(t, repo, "push", "origin", "main")
	return repo, origin
}

// fakeExecutor is a program that stands in for an agent: it is a script of the test
// and not a stand-in inside the code of a run, so that a run is seen working with a
// real program, a closed stdin and a real exit code. Its name is the name of the
// program, because that is what the profile of a run is chosen by, and its first
// argument is the worktree of the task, which it changes nothing outside of.
func fakeExecutor(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	script := "#!/bin/sh\nset -e\ncd \"$1\"\n" + body
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write the fake executor: %v", err)
	}
	return path
}

// said is what a fake executor says on the way out: a program writes a line of what
// it says on its error output, and a test of a run reads it from there.
func said(stderr string) string {
	var out strings.Builder
	for raw := range strings.Lines(strings.TrimSuffix(stderr, "\n")) {
		fmt.Fprintf(&out, "echo '%s' >&2\n", strings.TrimSuffix(raw, "\n"))
	}
	return out.String()
}

// commit is what a fake executor leaves in the worktree: a file with a path, added
// and committed, so that the work of the run is on the branch of the task and
// `git diff` has something to say about it.
func commit(path, content string) string {
	return "mkdir -p '" + filepath.Dir(path) + "'\n" +
		"printf '%b' '" + content + "' > '" + path + "'\n" +
		"git add '" + path + "'\n" +
		"git commit -m 'feat: the work of the run'\n"
}

// gitOf runs a command of git and fails the test when it says no: a test of a run
// against a real git is worth nothing if the repository is not a real one.
func gitOf(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := runGit(dir, args...); err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
}

// gitOut is what a command of git wrote.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := runGit(dir, args...)
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return out
}

// runGit starts git the way a person does, in the folder it is told to work in and
// without the settings of the machine: a test of a run must hold on a machine where
// nothing is configured either.
func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// split are the paths of the answer of git, one per line.
func split(out string) []string {
	var lines []string
	for raw := range strings.Lines(strings.TrimSpace(out)) {
		if path := strings.TrimSpace(raw); path != "" {
			lines = append(lines, path)
		}
	}
	return lines
}

// TestRunInTheModeOfTheBotGuardsTheWorktreeOfTheTask runs a task in the mode of the
// bot against a real repository with a real origin, and looks at what the run left in
// the worktree: the helper git takes its credentials from, and the folder of hooks that
// lets the branch of the task through and refuses everything else.
//
// The hook is a guard behind the rules of the branch on the host, and it is the only one
// that lives in the worktree of a run — the rules of the host are what an app cannot go
// around. The folder of hooks belongs to the worktree of that task alone: the checkout
// of the person, a worktree of the orchestrator and a second task of the same project
// push whatever they like, and the hook of the person is not touched (docs/DESIGN.md §7i).
func TestRunInTheModeOfTheBotGuardsTheWorktreeOfTheTask(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not installed: %v", err)
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skipf("sh is not installed: %v", err)
	}
	repo, _ := repository(t)
	// The hook of the person is in the common folder of hooks of the repository, and a
	// run of a task writes its hook into a folder of crewflow of its own: a repository
	// a person works in is not a part of a run (§7a, §7i).
	hookOfThePerson := filepath.Join(repo, ".git", "hooks", "pre-push")
	if err := os.MkdirAll(filepath.Dir(hookOfThePerson), 0o700); err != nil {
		t.Fatalf("make the folder of hooks of the repository: %v", err)
	}
	own := "#!/bin/sh\nexit 0\n"
	if err := os.WriteFile(hookOfThePerson, []byte(own), 0o700); err != nil {
		t.Fatalf("write the hook of the person: %v", err)
	}
	// The fake executor writes down the environment it was given and leaves work on
	// the branch of the task, as an agent that pushes would.
	executor := fakeExecutor(t, "agent",
		"printf 'GH_TOKEN=%s\\nGIT_AUTHOR_NAME=%s\\n' \"$GH_TOKEN\" \"$GIT_AUTHOR_NAME\" > identity\n"+
			"printf '%s' '"+theEvents+"'\n"+
			commit("internal/run/run.go", "package run\n"))
	host := &host{task: taskOf(43), opened: true, identity: theBot()}
	cfg := projectOf(t, t.TempDir(), "1h")
	cfg.Executor.Command = []string{executor, "{worktree}", "--prompt", "{prompt}"}
	home := t.TempDir()

	result, err := Run(t.Context(), System(home), cfg, host.set(), Request{Number: 43, RepoDir: repo})
	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	// The helper of the credentials and the path of the hooks are settings of the
	// worktree of the run and not of the repository: the checkout of the person pushes
	// with the login of the person, and the one of a run pushes with a token of the app.
	if got := gitOut(t, result.Worktree, "config", "--get", "credential.helper"); got != "crewflow auth git-credential" {
		t.Errorf("the worktree of the run has the helper %q, want the one of crewflow", got)
	}
	hooks := gitOut(t, result.Worktree, "config", "--get", "core.hooksPath")
	if !strings.Contains(hooks, filepath.Join("hooks", "naghuale-crewflow", "43")) {
		t.Errorf("the folder of hooks of the run is %q, want one of crewflow for the task 43", hooks)
	}
	// Nothing a run sets is in the settings of the checkout of the person, whatever
	// else the machine holds there. A setting that is not there is not an error of a
	// test: git says so with a code of an exit, and this machine may well have a
	// helper of its own.
	if got := gitSetting(repo, "core.hooksPath"); got != "" {
		t.Errorf("the checkout of the person has core.hooksPath = %q, want none of it", got)
	}
	if got := gitSetting(repo, "credential.helper"); strings.Contains(got, "crewflow") {
		t.Errorf("the checkout of the person has the helper %q, want the one it had", got)
	}
	for _, value := range []string{
		"crewflow auth git-credential",
		filepath.Join("hooks", "naghuale-crewflow"),
		"crewflow-executor[bot]",
		"users.noreply.github.com",
	} {
		if listed := gitConfigOf(repo); strings.Contains(listed, value) {
			t.Errorf("the settings of the checkout of the person hold %q:\n%s", value, listed)
		}
	}
	if got := read(t, hookOfThePerson); got != own {
		t.Errorf("the hook of the person holds %q, want the one they had", got)
	}
	// The executor is handed the token of the app and signs its commits with the
	// account of the app, and the journal holds neither: a journal is a file kept for
	// ever and pasted into issues (docs/DESIGN.md §7e, §7i).
	if got, want := read(t, filepath.Join(result.Worktree, "identity")),
		"GH_TOKEN="+theToken+"\nGIT_AUTHOR_NAME=crewflow-executor[bot]\n"; got != want {
		t.Errorf("the executor was given %q, want %q", got, want)
	}
	if got := read(t, result.Journal); strings.Contains(got, theToken) {
		t.Errorf("the journal of the run holds the token of the app:\n%s", got)
	}

	// The hook refuses a push of the branch of the project and lets the branch of the
	// task through, and it says why: an executor that was talked into pushing into main
	// is refused by the machine as well and not only by the words of the task.
	pushed, err := runGit(result.Worktree, "push", "origin", "HEAD:refs/heads/main")
	if err == nil {
		t.Errorf("the hook let a push into main through:\n%s", pushed)
	}
	if !strings.Contains(pushed, "pushes only") {
		t.Errorf("the hook refused a push into main with %q, want it to say what it allows", pushed)
	}
	if out, err := runGit(result.Worktree, "push", "origin", "HEAD:refs/heads/"+result.Branch); err != nil {
		t.Errorf("the hook refused the push of the branch of the task:\n%s\n%s", result.Branch, out)
	}

	// A worktree of the same repository that is not a task — the review of the
	// orchestrator, the work of a person in another clone of it — pushes whatever it
	// likes: the folder of hooks of a task belongs to the worktree of that task.
	review := filepath.Join(t.TempDir(), "review")
	gitOf(t, repo, "worktree", "add", "-b", "review/44-the-review", review, "main")
	if out, err := runGit(review, "push", "origin", "HEAD:refs/heads/review/44-the-review"); err != nil {
		t.Errorf("the worktree of a review could not push its own branch:\n%s\n%s", "review/44-the-review", out)
	}
	// And the checkout of the person is not a worktree of a run at all.
	gitOf(t, repo, "checkout", "main")
	gitOf(t, repo, "merge", "--ff-only", "origin/"+result.Branch)
	gitOf(t, repo, "push", "origin", "main")

	// Two tasks of one project have a folder of hooks each, and neither of them may
	// push the branch of the other.
	secondCfg := cfg
	secondCfg.Executor.Command = []string{
		fakeExecutor(t, "agent", "printf '%s' '"+theEvents+"'\n"+
			commit("internal/run/run.go", "package run // the work of the second task\n")),
		"{worktree}", "--prompt", "{prompt}"}
	another := *host
	another.task = taskOf(44)
	second, err := Run(t.Context(), System(home), secondCfg, another.set(), Request{Number: 44, RepoDir: repo})
	if err != nil {
		t.Fatalf("the run of the second task returned an error: %v", err)
	}
	if out, err := runGit(second.Worktree, "push", "origin", "HEAD:refs/heads/"+second.Branch); err != nil {
		t.Errorf("the worktree of the second task could not push its own branch %q:\n%s", second.Branch, out)
	}
	if out, err := runGit(second.Worktree, "push", "origin", "HEAD:refs/heads/"+result.Branch); err == nil {
		t.Errorf("the worktree of the second task could push the branch of the first:\n%s", out)
	} else if !strings.Contains(out, "pushes only") {
		t.Errorf("the second task was refused with %q, want the hook of its own worktree to refuse it", out)
	}
	// The hook of the first task is still the one of the first task.
	if got := read(t, filepath.Join(hooks, "pre-push")); !strings.Contains(got, result.Branch) {
		t.Errorf("the hook of the first task holds %q, want the branch of the first task in it", got)
	}
}

// TestRunTakesTheHooksOfTheTaskAwayWithItsWorktree: the folder of hooks of a task
// goes with the worktree of that task, because a folder that nothing points at any more
// is what is left of a task that was cleaned up by hand (docs/DESIGN.md §7i).
func TestRunTakesTheHooksOfTheTaskAwayWithItsWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not installed: %v", err)
	}
	repo, _ := repository(t)
	executor := fakeExecutor(t, "agent", "printf '%s' '"+theEvents+"'\n")
	host := &host{task: taskOf(43), opened: true, identity: theBot()}
	cfg := projectOf(t, t.TempDir(), "1h")
	cfg.Executor.Command = []string{executor, "{worktree}", "--prompt", "{prompt}"}
	home := t.TempDir()

	result, err := Run(t.Context(), System(home), cfg, host.set(), Request{Number: 43, RepoDir: repo})
	if err != nil {
		t.Fatalf("the first run returned an error: %v", err)
	}
	folder := filepath.Join(home, "hooks", "naghuale-crewflow", "43")
	if _, err := os.Stat(folder); err != nil {
		t.Fatalf("the folder of hooks of the task is not there: %v", err)
	}
	// The worktree of the task is taken away by hand, and the run of the task is asked
	// to go on in it: there is nothing to go on in, and the folder of hooks goes too.
	if err := os.RemoveAll(result.Worktree); err != nil {
		t.Fatalf("take the worktree away: %v", err)
	}
	_, err = Run(t.Context(), System(home), cfg, host.set(),
		Request{Number: 43, RepoDir: repo, Continue: "the review asked for a test"})
	if err == nil {
		t.Fatal("the run of a task with no worktree returned no error, want the refusal")
	}
	if !strings.Contains(err.Error(), "is not there any more") {
		t.Errorf("the run = %v, want it to say the worktree of the task is gone", err)
	}
	if _, err := os.Stat(folder); !os.IsNotExist(err) {
		t.Errorf("the folder of hooks of the task is still there after its worktree is gone: %v", err)
	}
}

// gitSetting is what a setting of a repository is on this machine, and an empty string
// where there is none: git says "not there" with a code of an exit, and a test that
// waited for words of a setting that may be absent would fail on a machine that never
// had it.
func gitSetting(dir, key string) string {
	out, err := runGit(dir, "config", "--get", key)
	if err != nil {
		return ""
	}
	return out
}

// gitConfigOf is every setting of a repository, as a person would read it with
// `git config --list`: a test that looks for one setting of a run in it reads them
// all, and there is no way to ask git for the keys alone.
func gitConfigOf(dir string) string {
	out, err := runGit(dir, "config", "--list")
	if err != nil {
		return ""
	}
	return out
}
