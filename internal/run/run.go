// Package run starts an executor on one task, in a worktree of its own, and says
// how the run ended (docs/DESIGN.md §6, §7, §7a).
//
// A run is not a success because the executor exited zero: the first run of the
// pilot was refused a permission and exited zero with no change request at all. The
// outcome is what the run did — a change request, a refusal, a stop of its own, a
// timeout, nothing at all, a failure, or work outside the boundaries of the task —
// and it is that outcome and not a code that a person reads.
package run

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/naghuale/crewflow/internal/access"
	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/doctor"
	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/proc"
	"github.com/naghuale/crewflow/internal/run/profile"
	"github.com/naghuale/crewflow/internal/secret"
	"github.com/naghuale/crewflow/internal/task"
)

// Env is everything a run needs from the machine. It is passed in whole, so that a
// test hands a run a machine of its own and no test depends on what happens to be
// installed, or to be found, where the test runs.
type Env struct {
	// Home is the root of what crewflow keeps of its own: the journal of every run
	// and the state of every task (§7).
	Home string
	// UserHome is the home of the person crewflow runs for: what a leading "~/" in
	// the file of the project stands for, and where the places crewflow keeps closed
	// are. It is given and not read from the process, so that a test of a run never
	// resolves a path against the home of the person it runs on (docs/DESIGN.md §7d).
	UserHome string
	// Environ is the environment crewflow itself was started with, which is where a
	// person may have put settings of their own for the agent. A run writes the rights
	// of the policy of the project over them and hands both on (§7d).
	Environ []string
	// Command starts a program in dir and returns what it wrote and the code it
	// exited with. Git and the tools of a project go through it, the way they do in
	// doctor (§7d).
	Command func(ctx context.Context, name string, args []string, dir string) (stdout, stderr []byte, exitCode int, err error)
	// Stream starts a program in dir and writes what it writes to stdout and stderr
	// as it comes, with the given environment added to the one of the process. The
	// executor goes through it and nothing else does: the journal of a run is written
	// while the run goes on, and nothing of a run is only in the memory of the
	// process that ran it (§7).
	Stream func(ctx context.Context, name string, args []string, dir string, env []string, stdout, stderr io.Writer) (exitCode int, err error)
	// Now is the clock of a run, so that a report says when a thing happened and a
	// test does not have to wait for it to happen.
	Now func() time.Time
	// Process is the process this run of crewflow happens in — its number and when it
	// started — which is what the state of the task is told, so that `crewflow task
	// list` can ask the machine later whether this run is still going
	// (docs/DESIGN.md §7). It is crewflow and not the executor: crewflow lives
	// exactly as long as the run and stops the executor with itself. A machine that
	// cannot say leaves the state of the task as it was written before: an attempt
	// that names no process, which a list reads the old way.
	Process func() (proc.Process, bool)
	// ConfigPath is the crewflow.toml this run was asked for, which a hint of a
	// refusal names: a person who is told "add app_id to <path>" has to know which
	// file (docs/DESIGN.md §5).
	ConfigPath string
	// Secrets is where the key of the App of the project is kept, and a run in the
	// mode of the bot signs a token with it. A run in the mode of the owner never
	// asks the store for anything, and no run of any mode ever shows what is in it
	// (docs/DESIGN.md §7e, §7i).
	Secrets secret.Store
}

// System is the machine this process runs on, with the given root of what crewflow
// keeps of its own.
func System(home string) Env {
	machine := proc.System()
	// A machine that cannot say where the home of the person is has no folder crewflow
	// may resolve a "~" of the file of a project to, and the policy of a run says so
	// rather than guessing one.
	userHome, _ := os.UserHomeDir()
	return Env{
		Home:     home,
		UserHome: userHome,
		Environ:  os.Environ(),
		Command:  Start,
		Stream:   Stream,
		Now:      time.Now,
		Process:  machine.Self,
		// The key of the App of the project is in the store of the machine, and a
		// run in the mode of the bot signs a token with it; a run in the mode of the
		// owner never touches the store (docs/DESIGN.md §7i).
		Secrets: secret.System(),
	}
}

// Start starts a program in dir and returns what it wrote and the code it exited
// with. It is the runner of doctor, and the reason it is that one is written down
// there: the stdin of a program is the empty device, because an agent that waits
// for an answer waits for it until the timeout of the run is out (docs/DESIGN.md §7a).
func Start(ctx context.Context, name string, args []string, dir string) (stdout, stderr []byte, exitCode int, err error) {
	return doctor.Command(ctx, name, args, dir, nil)
}

// Request is one run of one task.
type Request struct {
	// Number is the task to run.
	Number int
	// RepoDir is the repository the worktree is made from: the folder crewflow was
	// called in when it is empty.
	RepoDir string
	// Continue is what the orchestrator says to go on with. An empty message is the
	// first run of a task; a message is a run that goes on in the same worktree,
	// whether the last one was interrupted or the review asked for changes
	// (docs/DESIGN.md §7).
	Continue string
}

// Result is everything a person and an orchestrator are told about a run.
type Result struct {
	// Task and Title are what was run, so that a report says which task it is of
	// without the tracker being read again.
	Task  int    `json:"task"`
	Title string `json:"title"`
	// Outcome is how the run ended, and everything below is what is known about it.
	Outcome  Kind   `json:"outcome"`
	Branch   string `json:"branch"`
	Worktree string `json:"worktree"`
	Profile  string `json:"profile"`
	Session  string `json:"session,omitempty"`
	Attempt  int    `json:"attempt"`
	// Identity is whose name the executor of the run worked under, and the one line
	// a report of a run shows before anything else: a person reading the outcome of a
	// run has to know whose name it went under (docs/DESIGN.md §7i).
	Identity Identity `json:"identity"`
	// Continued says that this attempt went on in the session of an earlier one.
	Continued bool `json:"continued"`
	// StartedAt and EndedAt are when the executor was started and stopped.
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	// Journal and ErrorJournal are the files of what the executor wrote and said.
	Journal      string `json:"journal"`
	ErrorJournal string `json:"error_journal"`
	// ExitCode is what the executor exited with, which is a fact of a run and not
	// its outcome.
	ExitCode int `json:"exit_code"`
	// Rejections are the permissions the executor was refused, Reason is why it
	// stopped by itself, and Outside are the files it changed that the task was not
	// to change. Which of them holds anything depends on the outcome.
	Rejections []string `json:"rejections,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	Outside    []string `json:"outside,omitempty"`
	// ChangeRequest is the request the run opened, when it opened one: a run that
	// went outside the boundaries of the task is told about the request as well, or
	// a person would not see the work to look at.
	ChangeRequest *forge.ChangeRequest `json:"change_request,omitempty"`
}

// OK is whether the run did what a run is for: it opened the change request of its
// branch and stayed inside the boundaries of the task. Every other outcome is
// something a person or an orchestrator has to decide about, and none of them is a
// failure of the machine (docs/DESIGN.md §6, §8).
func (r Result) OK() bool {
	return r.Outcome == ChangeRequestOpened
}

// Run takes the task from the tracker, checks that it is ready, makes the branch
// and the worktree of it, runs the executor there and works out what came of it.
//
// Nothing is created for a task that is not ready or that the tracker does not
// have, and a run that ends in any other way than a change request is still a run
// that is recorded: the state and the journal of it are what the next step of the
// cycle and the next attempt of a person are read from (docs/DESIGN.md §7).
func Run(ctx context.Context, env Env, cfg config.Config, set forge.Set, req Request) (Result, error) {
	r := &runner{env: env, cfg: cfg, set: set, req: req}
	if err := r.readTask(ctx); err != nil {
		return Result{}, err
	}
	if err := r.prepare(ctx); err != nil {
		return Result{}, err
	}
	return r.start(ctx)
}

// runner is one run of one task, and what it has found out about it so far.
type runner struct {
	env Env
	cfg config.Config
	set forge.Set
	req Request
	// task is what the tracker holds, and branch and worktree are where its work
	// is to be done.
	task     forge.Task
	branch   string
	worktree string
	// profile is what crewflow knows about the executor of the project.
	profile profile.Profile
	// identity is whose name the executor of this run works under, and it is worked
	// out before the executor is started: a run that cannot be given an account of its
	// own is a run that is not started at all (docs/DESIGN.md §7i).
	identity forge.Identity
	// journals is where the state and the journal of the task are kept.
	journals Journals
	// session is the session a continuation goes on in, if there is one to go on in.
	session string
}

// readTask takes the task from the tracker and checks that it may be run at all.
func (r *runner) readTask(ctx context.Context) error {
	if r.set.Tracker == nil {
		return fmt.Errorf("tracker.kind: this project has no tracker of tasks crewflow can read, so there is no task to run")
	}
	if r.set.Forge == nil {
		return fmt.Errorf("forge.kind: this project has no host of its code, so a run has no change request to open")
	}
	found, err := r.set.Tracker.Task(ctx, r.req.Number)
	if err != nil {
		return fmt.Errorf("read task %d: %w", r.req.Number, err)
	}
	if err := task.CheckReady(found, r.cfg); err != nil {
		return err
	}
	r.task = found
	r.branch = Branch(found)
	r.worktree, err = Worktree(r.cfg, found.Number)
	if err != nil {
		return err
	}
	r.profile = profile.For(r.cfg.Executor.ExecutorSpec.Command)
	r.journals = newJournals(r.env.Home, r.cfg.RepoName())
	return nil
}

// prepare makes the worktree of the task, or finds the one a continuation goes on
// in. A first run starts from a fresh default branch: a task that is worked on
// without it would stand on whatever the folder of the person happened to hold.
func (r *runner) prepare(ctx context.Context) error {
	if r.req.Continue != "" {
		return r.findWorktree()
	}
	if _, err := os.Stat(r.worktree); err == nil {
		return fmt.Errorf("the worktree %s of task %d is already there: "+
			"run it again with -continue to go on in the same worktree, or take the worktree away yourself",
			r.worktree, r.task.Number)
	}
	if err := r.git(ctx, r.repoDir(), "fetch", "origin", r.cfg.Project.DefaultBranch); err != nil {
		return err
	}
	return r.git(ctx, r.repoDir(),
		"worktree", "add", "-b", r.branch, r.worktree, "origin/"+r.cfg.Project.DefaultBranch)
}

// findWorktree is the worktree a continuation goes on in. There has to be one: a
// run that was interrupted left it, and a task whose worktree is gone has to be run
// from the beginning, which is a new branch and a new run.
func (r *runner) findWorktree() error {
	state, err := LoadState(r.journals.StatePath(r.task.Number))
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("task %d was never run here, so there is no run to go on in: run it without -continue first",
				r.task.Number)
		}
		return err
	}
	if _, err := os.Stat(state.Worktree); err != nil {
		// The worktree of the task is gone, and the hook that went with it goes with
		// it: a folder of hooks that nothing points at any more is what is left of a
		// task that was cleaned up by hand, and a run of this task writes a new one
		// where it belongs (docs/DESIGN.md §7i).
		return errors.Join(
			fmt.Errorf("the worktree %s of task %d is not there any more, so its run cannot be continued: %w",
				state.Worktree, state.Number, err),
			r.takeHooksAway())
	}
	r.worktree = state.Worktree
	r.branch = state.Branch
	r.session = state.Session
	return nil
}

// repoDir is the repository the worktree is made from: the one the caller named, or
// the folder crewflow was called in.
func (r *runner) repoDir() string {
	if r.req.RepoDir != "" {
		return r.req.RepoDir
	}
	return "."
}

// git runs one command of git in dir and returns what it wrote. Every way git can
// say no is an error with the command in it, because a person who is told what to
// run by hand sees what crewflow saw.
func (r *runner) git(ctx context.Context, dir string, args ...string) error {
	_, stderr, code, err := r.env.Command(ctx, "git", args, dir)
	switch {
	case err != nil:
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	case code != 0:
		return fmt.Errorf("git %s: exited with %d: %s", strings.Join(args, " "), code, firstLine(stderr))
	}
	return nil
}

// output runs one command of git in dir and returns what it wrote, for the one
// command whose answer crewflow reads: the files the run changed.
func (r *runner) output(ctx context.Context, dir string, args ...string) (string, error) {
	stdout, stderr, code, err := r.env.Command(ctx, "git", args, dir)
	switch {
	case err != nil:
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	case code != 0:
		return "", fmt.Errorf("git %s: exited with %d: %s", strings.Join(args, " "), code, firstLine(stderr))
	}
	return string(stdout), nil
}

// stateOf is what crewflow keeps of the task with one attempt more in it: a first
// run of a task starts a state of its own, and every run after it is the next
// attempt of the same task. The attempt is told which process the run is happening
// in, before the executor is started, because that is all that is left of a run that
// crewflow is killed in the middle of (docs/DESIGN.md §7).
func (r *runner) stateOf(started time.Time) State {
	state, err := LoadState(r.journals.StatePath(r.task.Number))
	if err != nil {
		state = State{}
	}
	attempt := len(state.Attempts) + 1
	state.Number, state.Title = r.task.Number, r.task.Title
	state.Branch, state.Worktree, state.Profile = r.branch, r.worktree, r.profile.Name()
	state.Session = r.session
	process, _ := r.process()
	// The executor and the session of the attempt are of that attempt and not of the
	// task: a task whose executor was replaced starts again in a session of its own,
	// and a continuation goes on in the one before. The session of a first run is
	// not known before the executor is started, and it is written into the attempt
	// when the run is over (§7h).
	return state.NextAttempt(StartOf{
		Started:      started,
		Journal:      r.journals.JournalPath(r.task.Number, attempt),
		ErrorJournal: r.journals.errorJournalPath(r.task.Number, attempt),
		Executor:     r.profile.Name(),
		Session:      r.session,
		Continued:    r.req.Continue != "",
		Process:      process,
		Identity:     Identity{Mode: r.identity.Mode, Description: r.identity.Description},
	})
}

// process is the process of crewflow this run is happening in, and whether the machine
// could say so at all. It is a question, not a fact: a machine that cannot be asked
// is a machine whose state of the task is written without a process in it, which is
// how every state of crewflow was written before.
func (r *runner) process() (proc.Process, bool) {
	if r.env.Process == nil {
		return proc.Process{}, false
	}
	return r.env.Process()
}

// start runs the executor in the worktree of the task and says what came of it. The
// time limit of the run is the one of the project: a hang is an outcome of a run and
// not a reason to wait for ever (docs/DESIGN.md §7a).
//
// The state of the task is written before the executor is started and again when it
// is done: a run that crewflow is killed in the middle of has to leave behind the
// attempt, the worktree and the journal of what it was doing (§7).
func (r *runner) start(ctx context.Context) (Result, error) {
	timeout, err := time.ParseDuration(r.cfg.Executor.Timeout)
	if err != nil {
		return Result{}, fmt.Errorf("executor.timeout: %w", err)
	}
	command, err := r.command()
	if err != nil {
		return Result{}, err
	}
	// Whose name the executor of this run works under is worked out before the state
	// of the task is written, because the state says it: a run in the mode of the bot
	// that cannot be given a token of its own is a run that is not started at all, and
	// a state of a task with an attempt that never was is a state that lies (§7i).
	if r.identity, err = r.identityOf(ctx); err != nil {
		return Result{}, err
	}
	state := r.stateOf(r.env.Now())
	attempt := state.Attempts[len(state.Attempts)-1]
	if err := SaveState(r.journals.StatePath(r.task.Number), state); err != nil {
		return Result{}, err
	}
	files, err := r.journals.Begin(r.task.Number, attempt.Number)
	if err != nil {
		return r.stopBeforeStart(state, err)
	}
	if err := r.scratch(ctx); err != nil {
		_ = files.Close()
		return r.stopBeforeStart(state, err)
	}
	// Whose name the run went under is the first line of the journal, before what the
	// executor did and before the rights it was given: a person reading a journal of a
	// run afterwards has to see whose name it went under without reading the state file
	// as well (docs/DESIGN.md §7i).
	fmt.Fprintf(files.Out, "crewflow: executor: %s\n", r.identity.Description)
	// What the executor may read outside its worktree is worked out before it is
	// started and is said in the journal right after that: a run that was given the
	// right to read a folder of the machine has to say which, and a person who reads
	// the journal of the run afterwards sees it there (docs/DESIGN.md §7d).
	rights, err := r.rights(ctx, files)
	if err != nil {
		_ = files.Close()
		return r.stopBeforeStart(state, err)
	}
	// A run in the mode of the bot sets its worktree up before the executor is
	// started: the helper git takes a fresh token from, and the hook that refuses a
	// push anywhere but the branch of the task (§7i).
	if err := r.bot(ctx); err != nil {
		_ = files.Close()
		return r.stopBeforeStart(state, err)
	}

	// The time limit of the run is on the context, and a program that is still
	// going when it is out is asked to stop with it: the run has an end whatever the
	// executor thinks of it.
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// What the executor wrote is in the journal from its first line and is also kept
	// in memory, because a run is judged out of what the agent said, and the file is
	// the only one of the two a person and a watch can read (docs/DESIGN.md §7a).
	//
	// The files of the run go through the redactor of the secrets of the identity: the
	// token of a run is in the environment of the executor, and an agent that prints
	// its own environment is one line of a journal that would carry a token of an hour
	// into a file kept for ever and pasted into an issue (§7e, §7i).
	var out, errOut bytes.Buffer
	journal, wayOut := secret.NewRedactor(files.Out, r.identity.Secrets...), secret.NewRedactor(files.ErrOut, r.identity.Secrets...)
	environment := append(append(rights, r.identity.Env...), tempEnv(r.worktree)...)
	code, err := r.env.Stream(runCtx, command[0], command[1:], r.worktree, environment,
		io.MultiWriter(&out, journal), io.MultiWriter(&errOut, wayOut))
	// What is held back is a beginning of a line and may be the beginning of a
	// secret, and the run is over: it is written before the files are closed.
	ended := r.endOf(ctx, runCtx)
	flushed := errors.Join(journal.Flush(), wayOut.Flush())
	closeErr := errors.Join(files.Close(), flushed)
	result := Result{
		Task:         r.task.Number,
		Title:        r.task.Title,
		Branch:       r.branch,
		Worktree:     r.worktree,
		Profile:      r.profile.Name(),
		Session:      r.session,
		Attempt:      attempt.Number,
		Continued:    r.req.Continue != "",
		Identity:     Identity{Mode: r.identity.Mode, Description: r.identity.Description},
		StartedAt:    attempt.StartedAt,
		EndedAt:      r.env.Now(),
		Journal:      files.Journal,
		ErrorJournal: files.ErrorJournal,
		ExitCode:     code,
	}
	if err != nil {
		// The executor could not be started at all. That is said where a person
		// reads the way out of a run, and the outcome is a run that failed.
		result.ErrorJournal = r.noteError(files.ErrorJournal, err)
	}
	if closeErr != nil {
		return result, closeErr
	}
	// The session of the run is what a continuation goes on in, and it is found in
	// what the executor wrote even when the run ended badly.
	result.Session = r.profile.SessionID(out.Bytes())

	result, err = r.outcome(runCtx, result, out.Bytes(), errOut.Bytes(), code, ended)
	return result, r.keep(state, result, err)
}

// stopBeforeStart is what a run does when the executor could not be started at all:
// the attempt ends here, and the state of the task says so. An attempt left running
// would be a run that goes on in every watch of the task, and there is none.
func (r *runner) stopBeforeStart(state State, err error) (Result, error) {
	ended := state.Ended(r.env.Now(), ExecutorFailed)
	if keepErr := SaveState(r.journals.StatePath(r.task.Number), ended); keepErr != nil {
		return Result{}, errors.Join(err, keepErr)
	}
	return Result{}, err
}

// rights is the environment of the executor with the reading policy of the project in
// it: the folders the commands of [access] named are opened to the executor, the
// places of secrets are closed, and nothing of either may be written. It is worked
// out with the commands of the project and not with what a previous run worked out on
// a machine that may have changed since (docs/DESIGN.md §7d).
//
// A path crewflow will not open is not a run that failed: the run goes on without it
// and says so on the way out, because a person who looks at a run that was refused a
// permission for a folder of a dependency has to see that the folder was named and
// was not opened. A policy that cannot be named to the agent at all stops the run
// before the executor is started: an agent that was told nothing is an agent whose
// rights crewflow does not know.
func (r *runner) rights(ctx context.Context, files *AttemptFiles) ([]string, error) {
	policy, problems := access.Resolve(ctx, r.access(), r.cfg.Access)
	for _, problem := range problems {
		r.note(files.ErrorJournal, fmt.Errorf("access: %s: %s", problem.Path, problem.Reason))
	}
	env, err := r.profile.AccessEnv(policy, r.env.Environ)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(files.Out, "crewflow: the executor may read outside the worktree: %s; it may never read: %s\n",
		listed(policy.Read), listed(policy.Deny))
	return env, nil
}

// access is the machine as the policy of reading needs it: the home of the person and
// the way a command of the project is started, the two of which a run already has for
// everything else it runs (§7d).
func (r *runner) access() access.Env {
	return access.Env{
		Home: r.env.UserHome,
		Run: func(ctx context.Context, name string, args []string) ([]byte, []byte, int, error) {
			return r.env.Command(ctx, name, args, "")
		},
	}
}

// listed is a list of paths as one line of a journal reads: a person reads the line
// and not a list of them, and an empty policy is said so rather than left blank.
func listed(paths []string) string {
	if len(paths) == 0 {
		return "nothing"
	}
	return strings.Join(paths, ", ")
}

// endOf is how a run that has just stopped ended, asked of the two contexts of the
// run: the one of the caller, which a signal of a person cancels, and the one with
// the time limit of the project on it, which says that the run ran out of time. A
// person who stopped a run is told so, and the run of a project that ran out of
// time is told that (docs/DESIGN.md §7a).
func (r *runner) endOf(caller, run context.Context) Kind {
	switch {
	case errors.Is(caller.Err(), context.Canceled):
		return Interrupted
	case errors.Is(run.Err(), context.DeadlineExceeded):
		return TimedOut
	default:
		return ""
	}
}

// keep writes what happened down, so that after an interruption it is visible where
// the run stopped (docs/DESIGN.md §7). The state is written even when a run could
// not be judged: what a person then reads is the attempt, and the reason comes back
// as the error of the run. The change request of the run is written into it as well,
// so that a list of the runs of a project points at the work and not only at the
// outcome of the run.
func (r *runner) keep(state State, result Result, judgeErr error) error {
	state = state.Ended(result.EndedAt, result.Outcome)
	if result.Session != "" {
		state.Session = result.Session
		// The session of a run belongs to the attempt it went in, and not only to
		// the task: every attempt of a task has its own session, and a continuation
		// goes on in the one of the attempt it follows (docs/DESIGN.md §7h).
		if last := len(state.Attempts) - 1; last >= 0 {
			state.Attempts[last].Session = result.Session
		}
	}
	if change := result.ChangeRequest; change != nil {
		state.Change = &Change{Number: change.Number, URL: change.URL}
	}
	if err := SaveState(r.journals.StatePath(r.task.Number), state); err != nil {
		return err
	}
	return judgeErr
}

// noteError adds what went wrong to the way out of the run, so that the file a
// person is sent to holds the reason and not only what the agent said.
func (r *runner) noteError(errorJournal string, err error) string {
	r.note(errorJournal, err)
	return errorJournal
}

// note adds what went wrong to a file of the run, and goes on when it cannot: the
// file of a journal is not worth stopping a run for, and what crewflow has to say
// about it is in the return of the caller. What is written goes through the redactor
// of the secrets of the identity: an error of a run may carry what a program of it
// printed, and a file of a run is read by people and pasted into issues (§7e).
func (r *runner) note(path string, err error) {
	previous, readErr := os.ReadFile(path)
	if readErr == nil {
		line := fmt.Sprintf("crewflow: %v\n", secret.Redact(err.Error(), r.identity.Secrets...))
		_ = writeFile(path, append(previous, []byte(line)...))
	}
}

// command is the command line of the run: the command of the project with the text
// of the run in it, and, for a run that goes on in a session, the arguments that
// name the session.
func (r *runner) command() ([]string, error) {
	prompt, err := r.prompt()
	if err != nil {
		return nil, err
	}
	command, err := Command(r.cfg.Executor.ExecutorSpec, r.worktree, prompt)
	if err != nil {
		return nil, err
	}
	if r.session == "" {
		return command, nil
	}
	// A profile that has no session to go on in returns nothing here, and the run
	// begins again in the same worktree with the task in hand (docs/DESIGN.md §7a).
	continuation := r.profile.ContinueArgs(r.session)
	if len(continuation) == 0 {
		return command, nil
	}
	return append(command, continuation...), nil
}

// prompt is what the executor is told. A first run gets the whole assignment; a
// run that goes on in a session gets the message of the orchestrator alone, because
// the session holds the task and the context of the last try.
func (r *runner) prompt() (string, error) {
	if r.req.Continue == "" {
		return Prompt(r.task, r.cfg, r.branch, r.worktree)
	}
	if r.session != "" {
		return r.req.Continue, nil
	}
	return Continuation(r.req.Continue, r.task, r.cfg, r.branch, r.worktree)
}
