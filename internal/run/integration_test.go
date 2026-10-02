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
	"github.com/naghuale/crewflow/internal/run/profile"
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
			// The refusal is of a path of the machine that crewflow opened to nobody
			// and that is not a habit: a run crewflow could answer goes on by itself,
			// and this one does not (docs/DESIGN.md §7a).
			name:    "the run was refused a permission and stopped",
			program: "opencode",
			body:    "exit 0\n",
			stderr: "INFO  service=default starting\n" +
				"! permission requested: external_directory (/opt/homebrew/include); auto-rejecting\n",
			want: BlockedPermission,
		},
		{
			// A place of secrets is an outcome of a run of its own, whatever ended the
			// attempt: a real run that was refused a key is what the orchestrator reads
			// first (docs/DESIGN.md §7a.1, §7d).
			name:    "the run reached for a key and stopped",
			program: "opencode",
			body:    "exit 0\n",
			stderr: "INFO  service=default starting\n" +
				"! permission requested: external_directory (~/.ssh/id_ed25519); auto-rejecting\n",
			want: BlockedSecret,
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

// TestRunGoesOnByItselfAfterARealExecutorWroteToTmp: the manual check of §7a against
// a real repository — an executor that is refused /tmp in the first run and does the
// work in the second, and the run of the task goes on by itself between them, in the
// same worktree and the same session, and comes to a change request. The fake executor
// does not write into the temporary folder of the machine: it says the refusal the way
// OpenCode says it, which is what a run reads.
func TestRunGoesOnByItselfAfterARealExecutorWroteToTmp(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not installed: %v", err)
	}
	repo, _ := repository(t)
	// The fake executor writes down the arguments of every run in the scratch of the
	// worktree, and remembers that it has been refused once.
	executor := fakeExecutor(t, "opencode",
		"mkdir -p .scratch/tmp\n"+
			"printf '%s\\n' \"$*\" >> .scratch/tmp/args\n"+
			"printf '%s' '"+theEvents+"'\n"+
			"if [ -f .scratch/tmp/refused ]; then\n"+
			commit("internal/run/run.go", "package run\n")+
			"else\n"+
			"  touch .scratch/tmp/refused\n"+
			said("! permission requested: external_directory (/tmp/*); auto-rejecting\n")+
			"fi\n")
	host := &host{task: taskOf(43), opened: true}
	cfg := projectOf(t, t.TempDir(), "1h")
	cfg.Executor.Command = []string{executor, "{worktree}", "--prompt", "{prompt}"}
	home := t.TempDir()

	result, err := Run(t.Context(), System(home), cfg, host.set(), Request{Number: 43, RepoDir: repo})
	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	if result.Outcome != ChangeRequestOpened || result.Attempt != 2 {
		t.Fatalf("the run is the attempt %d and ended as %q, want the second and %q",
			result.Attempt, result.Outcome, ChangeRequestOpened)
	}
	if result.AutoResumed != string(reasonTmp) {
		t.Errorf("the run went on by itself for %q, want %q", result.AutoResumed, reasonTmp)
	}
	asked := read(t, filepath.Join(result.Worktree, ".scratch", "tmp", "args"))
	if got := strings.Count(asked, "--session ses_fake"); got != 1 {
		t.Errorf("the executor was run with the session of the first run %d times, want once:\n%s", got, asked)
	}
	if !strings.Contains(asked, ".scratch/tmp") {
		t.Errorf("the second run was asked no text about the scratch of the worktree:\n%s", asked)
	}
	// The line about the resume is in the journal of the attempt that ended, which is
	// what a watch of the run shows.
	state, err := LoadState(newJournals(home, "naghuale-crewflow").StatePath(43))
	if err != nil {
		t.Fatalf("load the state of the task: %v", err)
	}
	if len(state.Attempts) != 2 || state.Attempts[1].AutoResumed != string(reasonTmp) {
		t.Fatalf("the state holds %+v, want two attempts, the second of them a resume", state.Attempts)
	}
	first := read(t, state.Attempts[0].Journal)
	if want := "crewflow: resumed once — " + habits[reasonTmp].headline; !strings.Contains(first, want) {
		t.Errorf("the journal of the attempt that ended holds no line %q:\n%s", want, first)
	}
}

// TestRunGoesOnByItselfAfterAPathOfTheWorktreeWasWrittenOutByHand: the case of F-095 —
// the run wrote a path of the worktree out by hand, the project of the path is spelled
// with a letter out of place, and the path leads to a copy of the project and not to the
// worktree of the task. The file it was after is in the worktree, and the run is told
// which file and told to address it from the root of the worktree, in the same worktree
// and in the same session, and the work goes on (docs/DESIGN.md §7a.1).
func TestRunGoesOnByItselfAfterAPathOfTheWorktreeWasWrittenOutByHand(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not installed: %v", err)
	}
	repo, _ := repository(t)
	// The project of a task of a test has files in it, so that a path written out by
	// hand has a file of the worktree at the end of it: the habit of a path is a habit
	// only where the file is really there.
	write(t, filepath.Join(repo, "internal", "run", "resume.go"), "package run\n")
	gitOf(t, repo, "add", ".")
	gitOf(t, repo, "commit", "-m", "feat: the project of a test")
	gitOf(t, repo, "push", "origin", "main")
	cfg := projectOf(t, t.TempDir(), "1h")
	worktree, err := Worktree(cfg, 43)
	if err != nil {
		t.Fatalf("the worktree of the task: %v", err)
	}
	// The copy of the project the run addressed: the same worktree, the same task
	// number, and the name of the project with a letter out of place.
	typo := filepath.Join(filepath.Dir(filepath.Dir(worktree)), "owner-repz", "2", "internal", "run", "resume.go")
	// The fake executor writes the file of the worktree the run meant — the events of the
	// call say which file — and is refused for the path of the copy. In the second run it
	// does the work of the task and opens the change request.
	executor := fakeExecutor(t, "opencode",
		"mkdir -p .scratch/tmp\n"+
			"printf '%s\\n' \"$*\" >> .scratch/tmp/args\n"+
			"printf '%s' '"+theCall("edit "+typo)+theEvents+"'\n"+
			"if [ -f .scratch/tmp/refused ]; then\n"+
			commit("internal/run/resume.go", "package run // the work of the run\n")+
			"else\n"+
			"  touch .scratch/tmp/refused\n"+
			said("! permission requested: external_directory ("+typo+"); auto-rejecting\n")+
			"fi\n")
	cfg.Executor.Command = []string{executor, "{worktree}", "--prompt", "{prompt}"}
	home := t.TempDir()

	result, err := Run(t.Context(), System(home), cfg, (&host{task: taskOf(43), opened: true}).set(),
		Request{Number: 43, RepoDir: repo})
	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	if result.Outcome != ChangeRequestOpened || result.Attempt != 2 {
		t.Fatalf("the run is the attempt %d and ended as %q, want the second and %q",
			result.Attempt, result.Outcome, ChangeRequestOpened)
	}
	if result.AutoResumed != string(reasonWorktree) {
		t.Errorf("the run went on by itself for %q, want %q", result.AutoResumed, reasonWorktree)
	}
	// The next attempt is told which file the run was after, and told it as a path from
	// the root of the worktree: a rule about files and not only about the refusal.
	state, err := LoadState(newJournals(home, "naghuale-crewflow").StatePath(43))
	if err != nil {
		t.Fatalf("load the state of the task: %v", err)
	}
	asked := read(t, filepath.Join(worktree, ".scratch", "tmp", "args"))
	if want := "internal/run/resume.go"; !strings.Contains(asked, want) {
		t.Errorf("the second run was asked no path of the file it was after:\n%s", asked)
	}
	first := read(t, state.Attempts[0].Journal)
	one := resume{habit: habits[reasonWorktree], place: "internal/run/resume.go"}
	if want := one.line(); !strings.Contains(first, want) {
		t.Errorf("the journal of the attempt that ended holds no line %q:\n%s", want, first)
	}
}

// TestASecretIsNotAHabitWhateverTheShapeOfItIs: the places that stay closed are looked
// for before every habit of §7a.1, and each of the shapes those habits are of can be worn
// by a refusal of a secret — a probe of a copy where a `.env` of the worktree stands, a
// path written out by hand whose end is a `.env` that is really in the worktree, a
// worktree that is itself in a place of secrets. The classifier calls each of them the
// habit it is, and the run stops for the secret all the same: a run that reached a key is
// a run a person decides about, whatever the shape of the command was (§7a.1, §7d, §8).
func TestASecretIsNotAHabitWhateverTheShapeOfItIs(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not installed: %v", err)
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("the home of the person: %v", err)
	}
	cases := []struct {
		name string
		// command and refusal are what the run called a shell with and what it was
		// refused for, as functions of the worktree of the task: the habit of a path is
		// a habit only where the file the run was after is really there.
		command, refusal func(worktree string) string
		habit            reason
		kind             string
		inSecrets        bool
	}{
		{
			name:    "a probe of a copy that stands where a `.env` of the worktree does",
			command: func(string) string { return "cd .scratch/tmp/.env.production && git init" },
			refusal: func(string) string { return ".scratch/tmp/.env.production" },
			habit:   reasonProbe,
			kind:    accessCd,
		},
		{
			name: "a path written out by hand whose end is a `.env` of the worktree",
			command: func(worktree string) string {
				return "cat " + copied(worktree, "config", ".env.local")
			},
			refusal: func(worktree string) string {
				return copied(worktree, "config", ".env.local")
			},
			habit: reasonWorktree,
			kind:  accessRead,
		},
		{
			name:      "a probe of a copy in a worktree that stands in a place of secrets",
			command:   func(string) string { return "cd .scratch/tmp/probe && git init" },
			refusal:   func(string) string { return ".scratch/tmp/probe" },
			habit:     reasonProbe,
			kind:      accessCd,
			inSecrets: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A repository of its own for every case: the worktrees of a task are made
			// with the branch of the task, and one repository cannot hold that branch
			// twice.
			repo, _ := repository(t)
			write(t, filepath.Join(repo, "config", ".env.local"), "TOKEN=one\n")
			gitOf(t, repo, "add", ".")
			gitOf(t, repo, "commit", "-m", "feat: the project of a test")
			gitOf(t, repo, "push", "origin", "main")
			cfg := projectOf(t, t.TempDir(), "1h")
			if tc.inSecrets {
				// The worktrees of the project are made where a place of secrets is,
				// which no project would name and this test has to: it is the only way
				// the check of a secret and a habit of a folder of the worktree meet on
				// one refusal (docs/DESIGN.md §7d).
				cfg.Worktrees.Root = filepath.Join(userHome, ".ssh", "{repo}")
			}
			worktree, err := Worktree(cfg, 43)
			if err != nil {
				t.Fatalf("the worktree of the task: %v", err)
			}
			refused := tc.refusal(worktree)
			executor := fakeExecutor(t, "opencode",
				"printf '%s' '"+theCall(tc.command(worktree))+theEvents+"'\n"+
					said("! permission requested: external_directory ("+refused+"); auto-rejecting\n"))
			cfg.Executor.Command = []string{executor, "{worktree}", "--prompt", "{prompt}"}

			result, err := Run(t.Context(), System(t.TempDir()), cfg, (&host{task: taskOf(43), opened: true}).set(),
				Request{Number: 43, RepoDir: repo})
			if err != nil {
				t.Fatalf("Run returned an error: %v", err)
			}

			// The habit the refusal is about, worked out the way a run works it out and
			// after the worktree of the task is really there: a run that reached a secret
			// is stopped before the habits are looked at, and this is what the check of
			// a secret is there for.
			r := &runner{worktree: worktree, env: Env{UserHome: userHome}}
			if got, _ := r.habitOf("external_directory "+refused, []profile.Call{{
				Tool: "bash", Argument: tc.command(worktree),
			}}); got != tc.habit {
				t.Errorf("the habit of the refusal %q = %q, want %q: the test is about this habit", refused, got, tc.habit)
			}
			if result.Outcome != BlockedSecret {
				t.Fatalf("the outcome = %q, want %q: a habit does not make a secret one of its own",
					result.Outcome, BlockedSecret)
			}
			if result.Attempt != 1 || result.AutoResumed != "" {
				t.Errorf("the run is the attempt %d (resumed for %q), want the first and no resume",
					result.Attempt, result.AutoResumed)
			}
			if len(result.Rejections) != 1 {
				t.Fatalf("the result holds the refusals %q, want the one of the run", result.Rejections)
			}
			for _, want := range []string{tc.kind, recoveryDisabled} {
				if !strings.Contains(result.Rejections[0], want) {
					t.Errorf("the refusal %q does not hold %q", result.Rejections[0], want)
				}
			}
		})
	}
}

// copied is a file of the worktree named by the path of another copy of the project: the
// worktree of the task, the same worktree spelled with the name of the project with a
// letter out of place and the number of the task changed — what a run writes out by hand
// when it has the shape of the worktree and not the worktree itself (docs/DESIGN.md §7a.1).
func copied(worktree string, parts ...string) string {
	project := filepath.Dir(filepath.Dir(worktree))
	return filepath.Join(append([]string{project, "owner-repz", "2"}, parts...)...)
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
	// Every folder that is open is named with the hand that asked for it and the
	// reason: a path in a journal says nothing about who wanted it, and a person
	// looking at a run afterwards has to see that the folder came from the file of the
	// project and not from somewhere else (docs/DESIGN.md §7d).
	opened := "crewflow: the executor may read outside the worktree: " + through +
		" · [access] · the project asked for it with `"
	if want := opened + "sh -c"; !strings.Contains(journal, want) {
		t.Errorf("the journal holds %q, want the first folder of the run with the hand behind it", firstLineOf(journal))
	}
	if want := modules + " · [access] · the project asked for it with `"; !strings.Contains(journal, want) {
		t.Errorf("the journal holds %q, want the same folder in the spelling of the machine with the hand behind it", firstLineOf(journal))
	}
	// Where a run may write is said in the journal beside what it may read, because
	// both are the access of the run and one of them without the other leaves a person
	// guessing where the work was to be done.
	if want := "crewflow: the executor may write: " + result.Worktree + " · crewflow · the worktree of this task, " +
		Scratch(result.Worktree) + " · crewflow · the scratch of this run: TMPDIR, TMP and TEMP point at it"; !strings.Contains(journal, want) {
		t.Errorf("the journal holds %q, want where the run may write in it", firstLineOf(journal))
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
	out, err := rawGit(dir, args...)
	return strings.TrimSpace(out), err
}

// rawGit is the same git with nothing taken off what it wrote: the empty value of a reset
// in the settings of a worktree is a whole line of the answer of git and nothing else, and
// a test that reads that list has to be able to see it (§7i).
func rawGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	return string(out), err
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
	// The list of the helpers of the worktree is reset before the helper of crewflow is
	// added to it: an empty value and then the helper, and nothing else. The helper of the
	// machine — `osxkeychain` of the system file of macOS — is the one git asks first, it
	// has the token of the login of the person, and after a push of a run went through it
	// is handed the token of the App of that run (F-116, R6, #141). A real git is asked
	// about the whole list here, because the value git would use is the last one and the
	// one before it is the reset: `git config --get` alone shows a worktree that is right
	// and a worktree that is not (§7i).
	raw, err := rawGit(result.Worktree, "config", "--worktree", "--get-all", "credential.helper")
	if err != nil {
		t.Fatalf("git config --worktree --get-all credential.helper: %v\n%s", err, raw)
	}
	if helpers := strings.Split(strings.TrimRight(raw, "\n"), "\n"); len(helpers) != 2 ||
		helpers[0] != "" || helpers[1] != "crewflow auth git-credential" {
		t.Errorf("the worktree of the run holds the helpers %q, want an empty value and then the helper of crewflow",
			helpers)
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
