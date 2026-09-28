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
