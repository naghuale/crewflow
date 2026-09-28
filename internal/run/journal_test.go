package run

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/proc"
)

// theWork is what an executor of a test writes: the words of the agent and a call of
// a tool, in the shape the profile of OpenCode reads.
const theWork = `{"type":"text","sessionID":"ses_7fKq2","part":{"type":"text","text":"I did the work."}}
{"type":"tool_use","sessionID":"ses_7fKq2","part":{"type":"tool","tool":"bash","state":{"status":"completed","input":{"command":"go test -race -count=1 ./..."}}}}
{"type":"text","sessionID":"ses_7fKq2","part":{"type":"text","text":"The change request is open."}}
`

// theRefusals is what a run of that agent says on the way out.
const theRefusals = "INFO  service=default starting opencode\n" +
	"! permission requested: external_directory (/tmp/*); auto-rejecting\n"

// configContent is the variable of the profile of OpenCode in which the rights of a
// run are named, and a test of a run finds the rights of the run in the environment
// the executor was started with (docs/DESIGN.md §7d).
const configContent = "OPENCODE_CONFIG_CONTENT"

// TestRunWritesTheJournalWhileTheExecutorWorks is the case the pilot of M1.4 showed
// the need for: the journal of a run holds what the executor wrote while it is still
// writing it, and the state of the task says the attempt is going. A run that
// crewflow is killed in the middle of then leaves both behind, and a watch in another
// terminal has something to show (docs/DESIGN.md §7).
func TestRunWritesTheJournalWhileTheExecutorWorks(t *testing.T) {
	m := newMachine(t)
	wrote, release := make(chan struct{}), make(chan struct{})
	m.answers["opencode"] = answer{stdout: theRun, wrote: wrote, wait: release}
	host := &host{task: taskOf(43), opened: true}
	cfg := projectOf(t, m.worktrees, "")

	over := make(chan outcome, 1)
	go func() {
		result, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
		over <- outcome{result: result, err: err}
	}()

	<-wrote
	journals := newJournals(m.home, "naghuale-crewflow")
	if got := whatWasWritten(t, journals.JournalPath(43, 1)); got != theRun {
		t.Errorf("the journal holds %q while the executor works, want what it wrote", got)
	}
	running := stateOf(t, m, 43)
	if len(running.Attempts) != 1 {
		t.Fatalf("the state holds %d attempts, want the one that is running", len(running.Attempts))
	}
	if got := running.Attempts[0].Outcome; got != Running {
		t.Errorf("the outcome of the attempt in the state = %q, want %q while the executor works", got, Running)
	}
	if got := running.Attempts[0].EndedAt; !got.IsZero() {
		t.Errorf("the attempt in the state ended at %s, want it to have no end yet", got)
	}

	close(release)
	finished := <-over
	if finished.err != nil {
		t.Fatalf("Run returned an error: %v", finished.err)
	}
	if finished.result.Outcome != ChangeRequestOpened {
		t.Errorf("the outcome = %q, want %q", finished.result.Outcome, ChangeRequestOpened)
	}
	after := stateOf(t, m, 43)
	if got := after.Attempts[0].Outcome; got != ChangeRequestOpened {
		t.Errorf("the outcome of the attempt in the state = %q after the run, want %q", got, ChangeRequestOpened)
	}
	if got := whatWasWritten(t, after.Attempts[0].Journal); got != theRun {
		t.Errorf("the journal holds %q after the run, want what the executor wrote", got)
	}
}

// TestRunStoppedByAPerson: crewflow that is stopped with a signal stops the executor
// with it and says the run was interrupted. An executor left to work on without
// anyone would go on writing into a worktree nobody watches, and the next run of the
// task would go on in its session with a journal that has a hole in it.
func TestRunStoppedByAPerson(t *testing.T) {
	m := newMachine(t)
	wrote := make(chan struct{})
	m.answers["opencode"] = answer{stdout: theRun, wrote: wrote, hangs: true}
	host := &host{task: taskOf(43), opened: true}
	cfg := projectOf(t, m.worktrees, "")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	over := make(chan outcome, 1)
	go func() {
		result, err := Run(ctx, m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
		over <- outcome{result: result, err: err}
	}()

	<-wrote
	cancel()
	finished := <-over
	if finished.err != nil {
		t.Fatalf("Run of a stopped run returned an error: %v", finished.err)
	}
	if finished.result.Outcome != Interrupted {
		t.Errorf("the outcome = %q, want %q", finished.result.Outcome, Interrupted)
	}
	state := stateOf(t, m, 43)
	if len(state.Attempts) != 1 {
		t.Fatalf("the state holds %d attempts, want the one that was stopped", len(state.Attempts))
	}
	attempt := state.Attempts[0]
	if attempt.Outcome != Interrupted {
		t.Errorf("the outcome of the attempt in the state = %q, want %q", attempt.Outcome, Interrupted)
	}
	if attempt.EndedAt.IsZero() {
		t.Error("the attempt in the state has no end, want the moment a person stopped the run")
	}
	// What the executor had written until it was stopped is where the continuation
	// of the run reads it from.
	if got := whatWasWritten(t, attempt.Journal); got != theRun {
		t.Errorf("the journal of the stopped run holds %q, want what the executor wrote", got)
	}
}

// TestRunThatRanOutOfTimeIsNotAPerson: the time limit of the project and a person who
// stopped a run are two different things, and the second one is not the first.
func TestRunThatRanOutOfTimeIsNotAPerson(t *testing.T) {
	m := newMachine(t)
	m.answers["opencode"] = answer{stdout: theRun, hangs: true}
	host := &host{task: taskOf(43), opened: true}
	cfg := projectOf(t, m.worktrees, "20ms")

	result, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	if result.Outcome != TimedOut {
		t.Errorf("the outcome = %q, want %q", result.Outcome, TimedOut)
	}
}

// TestRunKeepsTheTemporaryFilesOfTheExecutorInTheWorktree: the executor is given a
// folder of its own to keep what is temporary in, and the folder is inside the
// worktree. A temporary file in the temporary folder of the machine is a write
// outside the worktree, and crewflow refuses it (docs/DESIGN.md §7a).
func TestRunKeepsTheTemporaryFilesOfTheExecutorInTheWorktree(t *testing.T) {
	m := newMachine(t)
	m.answers["opencode"] = answer{stdout: theRun}
	host := &host{task: taskOf(43), opened: true}
	cfg := projectOf(t, m.worktrees, "")

	result, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	scratch := filepath.Join(result.Worktree, ".scratch", "tmp")
	if info, err := os.Stat(scratch); err != nil {
		t.Errorf("the folder %s of the temporary files of the executor is not there: %v", scratch, err)
	} else if !info.IsDir() {
		t.Errorf("%s is not a folder, want the folder the executor keeps its temporary files in", scratch)
	}
	// The rights of the run go to the executor as well: the environment of a run is
	// the policy of reading and the folder of the temporary files, and nothing else
	// changes because of this.
	handed := m.envOf()
	for _, want := range []string{"TMPDIR=" + scratch, "TMP=" + scratch, "TEMP=" + scratch} {
		if !slices.Contains(handed, want) {
			t.Errorf("the executor was started with the environment %q, want %q in it", handed, want)
		}
	}
	if len(handed) != 4 || !strings.HasPrefix(handed[0], configContent+"=") {
		t.Errorf("the executor was started with the environment %q,\nwant the rights of the run and the folder of the temporary files", handed)
	}
	// The assignment names the folder as a path, so that the agent is not left to
	// work out where it may write.
	if asked := askedOf(t, m); !strings.Contains(asked, scratch) {
		t.Errorf("what the executor was asked does not name %s:\n%s", scratch, asked)
	}
}

// TestRunKeepsTheScratchOutOfTheRepository: the folder the executor works in is
// ignored by the repository of the project, once, and in the local ignore of git
// rather than in the `.gitignore` of the project: a repository a person owns is not
// changed because a task was run in it.
func TestRunKeepsTheScratchOutOfTheRepository(t *testing.T) {
	m := newMachine(t)
	m.answers["opencode"] = answer{stdout: theRun}
	host := &host{task: taskOf(43), opened: true}
	cfg := projectOf(t, m.worktrees, "")
	ignore := filepath.Join(m.repo, ".gitignore")
	if err := os.WriteFile(ignore, []byte("dist/\n"), 0o600); err != nil {
		t.Fatalf("write the .gitignore of the project: %v", err)
	}

	for _, request := range []Request{
		{Number: 43, RepoDir: m.repo},
		{Number: 43, RepoDir: m.repo, Continue: "add a test of the timeout"},
	} {
		if _, err := Run(t.Context(), m.env(), cfg, host.set(), request); err != nil {
			t.Fatalf("Run returned an error: %v", err)
		}
	}

	exclude := filepath.Join(m.git, "info", "exclude")
	if got := strings.Count(read(t, exclude), scratchIgnore); got != 1 {
		t.Errorf("%s holds the scratch %d times after two runs, want it once", exclude, got)
	}
	if got := read(t, ignore); got != "dist/\n" {
		t.Errorf("the .gitignore of the project holds %q, want it as it was", got)
	}
}

// TestExcludeJoinsTheLineItAdds: the local ignore of git is a list of lines, and one
// that ends without a newline of its own would otherwise take the scratch onto the
// end of the last pattern in it. A line that mentions the scratch in other words is
// another line, and the pattern is added after it all the same.
func TestExcludeJoinsTheLineItAdds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("make %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("# the .scratch/ of another project"), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	want := "# the .scratch/ of another project\n" + scratchIgnore + "\n"

	for range 2 {
		if err := exclude(path); err != nil {
			t.Fatalf("exclude returned an error: %v", err)
		}
		if got := read(t, path); got != want {
			t.Errorf("the local ignore holds %q, want %q", got, want)
		}
	}
}

// TestExcludeMakesTheIgnoreOfARepositoryThatHasNone: git writes a local ignore into
// every repository it makes, and a run that finds none of it makes the file the
// scratch is to be written into.
func TestExcludeMakesTheIgnoreOfARepositoryThatHasNone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "info", "exclude")

	if err := exclude(path); err != nil {
		t.Fatalf("exclude returned an error: %v", err)
	}

	if got := read(t, path); got != scratchIgnore+"\n" {
		t.Errorf("the local ignore of a repository that had none holds %q, want the scratch alone", got)
	}
}

// TestRunOfAWorktreeGitNamesNoRepositoryIn: without a repository there is nowhere to
// keep the scratch of the run out of, and a run that guesses one would write into the
// folder crewflow was called from. It says what is wrong and starts nothing.
func TestRunOfAWorktreeGitNamesNoRepositoryIn(t *testing.T) {
	m := newMachine(t)
	m.answers["git rev-parse --git-common-dir"] = answer{stdout: "\n"}
	m.answers["opencode"] = answer{stdout: theRun}
	host := &host{task: taskOf(43), opened: true}

	_, err := Run(t.Context(), m.env(), projectOf(t, m.worktrees, ""), host.set(), Request{Number: 43, RepoDir: m.repo})

	if err == nil {
		t.Fatal("Run without a repository to work in returned no error, want one")
	}
	if !strings.Contains(err.Error(), "no repository") {
		t.Errorf("error %q does not say that git named no repository", err)
	}
	if m.commandOf("opencode") != nil {
		t.Error("the executor was started without a repository to keep its scratch out of")
	}
}

// TestRunOfAWorktreeGitSaysNoTo: the scratch of the run could not be kept out of the
// repository, so no executor was started, and the attempt in the state of the task
// ends: an attempt left running would be a run that goes on in every watch of the
// task, and there is none.
func TestRunOfAWorktreeGitSaysNoTo(t *testing.T) {
	m := newMachine(t)
	m.answers["git rev-parse --git-common-dir"] = answer{stderr: "fatal: not a git repository\n", code: 128}
	m.answers["opencode"] = answer{stdout: theRun}
	host := &host{task: taskOf(43), opened: true}

	_, err := Run(t.Context(), m.env(), projectOf(t, m.worktrees, ""), host.set(), Request{Number: 43, RepoDir: m.repo})

	if err == nil {
		t.Fatal("Run without a repository to work in returned no error, want one")
	}
	for _, want := range []string{"rev-parse", "not a git repository", "128"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if m.commandOf("opencode") != nil {
		t.Error("the executor was started without a repository to keep its scratch out of")
	}
	state := stateOf(t, m, 43)
	if got := state.Attempts[0].Outcome; got != ExecutorFailed {
		t.Errorf("the outcome of the attempt in the state = %q, want %q", got, ExecutorFailed)
	}
	if state.Attempts[0].EndedAt.IsZero() {
		t.Error("the attempt in the state has no end, want the moment the run gave up")
	}
}

// TestRunGivesTheStyleOfTheCommits: the pilot of M1.4 wrote a commit with a paragraph
// of text in it, because the assignment said nothing about how the project writes its
// commit messages. The rule is the style of the last commits of the project, and a
// project that names its own style says it instead.
func TestRunGivesTheStyleOfTheCommits(t *testing.T) {
	cases := []struct {
		name  string
		style string
		want  []string
	}{
		{
			name:  "a project that names the style of its commits",
			style: "conventional: type(scope): subject",
			want:  []string{"conventional: type(scope): subject"},
		},
		{
			name: "a project that says nothing",
			want: []string{"git log --oneline -20", "72 characters"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine(t)
			m.answers["opencode"] = answer{stdout: theRun}
			host := &host{task: taskOf(43), opened: true}
			cfg := projectOf(t, m.worktrees, "")
			cfg.Project.CommitStyle = tc.style

			if _, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo}); err != nil {
				t.Fatalf("Run returned an error: %v", err)
			}

			asked := askedOf(t, m)
			for _, want := range tc.want {
				if !strings.Contains(asked, want) {
					t.Errorf("what the executor was asked does not hold %q about the commits:\n%s", want, asked)
				}
			}
			if tc.style == "" && !strings.Contains(asked, defaultCommitStyle) {
				t.Errorf("what the executor was asked does not hold the rule of the project:\n%s", asked)
			}
		})
	}
}

// TestWatchShowsAFinishedAttempt: a watch of a run that is over is the journal of
// that run, read through the profile that wrote it, and it is over as soon as the
// journal has been shown.
func TestWatchShowsAFinishedAttempt(t *testing.T) {
	home := t.TempDir()
	journals := newJournals(home, "naghuale-crewflow")
	attemptOf(t, journals, 43, 1, theWork, theRefusals, ChangeRequestOpened)

	watch, err := Watch(home, "naghuale-crewflow", 43, 0)
	if err != nil {
		t.Fatalf("Watch returned an error: %v", err)
	}
	if watch.Running() {
		t.Error("the watch says the attempt is running, want the one that has ended")
	}
	if watch.Profile != "opencode" {
		t.Errorf("the watch reads with the profile %q, want the one the run was made with", watch.Profile)
	}

	var shown bytes.Buffer
	if err := watch.Follow(t.Context(), &shown, nil); err != nil {
		t.Fatalf("Follow returned an error: %v", err)
	}

	want := []string{
		"I did the work.",
		"bash: go test -race -count=1 ./... (completed)",
		"The change request is open.",
		"external_directory /tmp/*",
	}
	if got := shownLines(shown.String()); !slices.Equal(got, want) {
		t.Errorf("the watch showed %q,\nwant %q", got, want)
	}
}

// TestWatchShowsTheLastLineNobodyClosed: an executor that ends without a newline in
// its journal wrote a line all the same, and a run that has ended will not add
// anything to it.
func TestWatchShowsTheLastLineNobodyClosed(t *testing.T) {
	home := t.TempDir()
	journals := newJournals(home, "naghuale-crewflow")
	attemptOf(t, journals, 43, 1, `{"type":"text","part":{"text":"I stopped in the middle of a line"}}`, "", Blocked)

	watch, err := Watch(home, "naghuale-crewflow", 43, 0)
	if err != nil {
		t.Fatalf("Watch returned an error: %v", err)
	}

	var shown bytes.Buffer
	if err := watch.Follow(t.Context(), &shown, nil); err != nil {
		t.Fatalf("Follow returned an error: %v", err)
	}

	if got, want := shownLines(shown.String()), []string{"I stopped in the middle of a line"}; !slices.Equal(got, want) {
		t.Errorf("the watch showed %q, want %q", got, want)
	}
}

// TestWatchFollowsARunningAttempt: a watch of a run that is going shows what the
// executor writes as it writes it, and stops when the run is over. The tick is a
// channel the test fills itself, so that nothing waits for a clock.
func TestWatchFollowsARunningAttempt(t *testing.T) {
	home := t.TempDir()
	journals := newJournals(home, "naghuale-crewflow")
	running := attemptOf(t, journals, 43, 1, "", "", Running)

	watch, err := Watch(home, "naghuale-crewflow", 43, 0)
	if err != nil {
		t.Fatalf("Watch returned an error: %v", err)
	}
	if !watch.Running() {
		t.Fatal("the watch says the attempt is not running, want the one that is")
	}
	ticks := make(chan time.Time)
	shown := make(chan string, 16)
	out := &lineWriter{write: func(line string) { shown <- line }}
	stopped := make(chan error, 1)
	go func() { stopped <- watch.Follow(t.Context(), out, ticks) }()

	// The executor writes while the run is going, and the watch shows it.
	write(t, running.Attempts[0].Journal, theWork)
	ticks <- time.Now()
	if got := <-shown; !strings.Contains(got, "I did the work.") {
		t.Errorf("the watch showed %q, want what the executor wrote while the run was going", got)
	}
	if got := <-shown; !strings.Contains(got, "bash: go test") {
		t.Errorf("the watch showed %q, want the tool the executor called", got)
	}
	if got := <-shown; !strings.Contains(got, "The change request is open.") {
		t.Errorf("the watch showed %q, want the last line the executor wrote while the run was going", got)
	}

	// The run ends, and the watch ends with it.
	ended := attemptOf(t, journals, 43, 1,
		`{"type":"text","part":{"text":"BLOCKED: I could not go on"}}`+"\n", "", Blocked)
	if ended.Attempts[0].Journal != running.Attempts[0].Journal {
		t.Fatalf("the attempt of the state moved from %q to %q, want the same journal",
			running.Attempts[0].Journal, ended.Attempts[0].Journal)
	}
	ticks <- time.Now()
	if got := <-shown; !strings.Contains(got, "BLOCKED: I could not go on") {
		t.Errorf("the watch showed %q after the run ended, want the last thing the executor wrote", got)
	}
	if err := <-stopped; err != nil {
		t.Fatalf("Follow of a run that has ended returned an error: %v", err)
	}
	select {
	case line := <-shown:
		t.Errorf("the watch went on showing %q after the run ended, want it to stop", line)
	default:
	}
}

// TestWatchOfAnAttemptThatWasNeverMade: a person who asks for a watch of a run that
// was not made is told that, and is not shown a journal that is not there.
func TestWatchOfAnAttemptThatWasNeverMade(t *testing.T) {
	home := t.TempDir()
	journals := newJournals(home, "naghuale-crewflow")

	t.Run("a task that was never run", func(t *testing.T) {
		_, err := Watch(home, "naghuale-crewflow", 43, 0)
		if err == nil {
			t.Fatal("Watch of a task that was never run returned no error, want one")
		}
		if !strings.Contains(err.Error(), "never run") {
			t.Errorf("error %q does not say that the task was never run here", err)
		}
	})
	t.Run("an attempt that was never made", func(t *testing.T) {
		attemptOf(t, journals, 43, 1, theWork, "", ChangeRequestOpened)
		_, err := Watch(home, "naghuale-crewflow", 43, 2)
		if err == nil {
			t.Fatal("Watch of an attempt that was never made returned no error, want one")
		}
		if !strings.Contains(err.Error(), "no attempt 2") {
			t.Errorf("error %q does not name the attempt that is not there", err)
		}
	})
	t.Run("the second attempt of a task of two", func(t *testing.T) {
		attemptOf(t, journals, 43, 2, theWork, "", ChangeRequestOpened)
		watch, err := Watch(home, "naghuale-crewflow", 43, 2)
		if err != nil {
			t.Fatalf("Watch returned an error: %v", err)
		}
		if watch.Attempt != 2 {
			t.Errorf("the watch is of the attempt %d, want the second one", watch.Attempt)
		}
	})
}

// askedOf is what the executor was asked, which is what the tests of the assignment
// read.
func askedOf(t *testing.T, m *machine) string {
	t.Helper()
	executor := m.commandOf("opencode")
	if executor == nil {
		t.Fatalf("the executor was not run, only: %v", m.lines())
	}
	return strings.Join(executor.args, "\n")
}

// outcome is what a run that has been waited for came out as.
type outcome struct {
	result Result
	err    error
}

// attemptOf is the state of a task with one attempt in it, which is what a run leaves
// while it is going and what it leaves after it is over. The journal of the attempt
// and the way out of it are written where the state points, and the outcome is the
// one given: the outcome running is a run that has not ended yet.
func attemptOf(t *testing.T, journals Journals, number, attempt int, journal, errorJournal string, ended Kind) State {
	t.Helper()
	started := time.Date(2026, time.September, 28, 10, 0, 0, 0, time.UTC)
	state := State{
		Number:  number,
		Title:   "the run of a task",
		Branch:  "crewflow/43-the-run-of-a-task",
		Profile: "opencode",
	}
	// A task that was run more than once holds the attempts before this one, and
	// each of them has ended.
	for before := 1; before <= attempt; before++ {
		state = state.NextAttempt(started.Add(time.Duration(before)*time.Minute),
			journals.JournalPath(number, before),
			journals.errorJournalPath(number, before),
			false,
			proc.Process{Pid: 4242, StartedAt: started})
		if before < attempt {
			state = state.Ended(started.Add(time.Duration(before+1)*time.Minute), ChangeRequestOpened)
		}
	}
	if ended != Running {
		state = state.Ended(started.Add(42*time.Minute), ended)
	}
	last := state.Attempts[len(state.Attempts)-1]
	if journal != "" {
		write(t, last.Journal, journal)
	}
	if errorJournal != "" {
		write(t, last.ErrorJournal, errorJournal)
	}
	if err := SaveState(journals.StatePath(number), state); err != nil {
		t.Fatalf("write the state of the task: %v", err)
	}
	return state
}

// write puts a file on the machine of a test, in the folder of it.
func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("make %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// shownLines are the lines a watch showed, without the empty ones.
func shownLines(shown string) []string {
	var lines []string
	for raw := range strings.Lines(shown) {
		if line := strings.TrimSpace(raw); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// lineWriter is a writer that hands every line it is given to a function, so that a
// test may look at what a watch has shown while it is still showing it.
type lineWriter struct {
	write func(line string)
}

// Write splits what a watch wrote into lines and hands them over one by one.
func (w *lineWriter) Write(p []byte) (int, error) {
	for _, line := range shownLines(string(p)) {
		w.write(line)
	}
	return len(p), nil
}
