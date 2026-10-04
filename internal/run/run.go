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
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/naghuale/crewflow/internal/access"
	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/doctor"
	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/network"
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
	// Command starts a program in dir with the extra environment added to the one of
	// the process, and returns what it wrote and the code it exited with. Git and the
	// tools of a project go through it, the way they do in doctor (§7d).
	Command func(ctx context.Context, name string, args []string, dir string, extraEnv []string) (stdout, stderr []byte, exitCode int, err error)
	// Stream starts a program in dir and writes what it writes to stdout and stderr
	// as it comes, with the given environment added to the one of the process. The
	// executor goes through it and nothing else does: the journal of a run is written
	// while the run goes on, and nothing of a run is only in the memory of the
	// process that ran it (§7).
	Stream func(ctx context.Context, name string, args []string, dir string, env []string, stdout, stderr io.Writer) (exitCode int, err error)
	// Tick calls onTick every `every` until the context of the run is over, on a
	// goroutine of its own, and answers the function that stops the tick and waits for
	// it: a run of a task has to know that nobody writes to the files of its attempt
	// before it closes them. A run whose machine cannot look at itself has it at nil and
	// looks at nothing, and the silence of a run is then worked out of the state of the
	// task wherever it is read (docs/DESIGN.md §6).
	Tick func(ctx context.Context, every time.Duration, onTick func()) (stop func())
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
	// Alive asks the machine whether the process of another run of the project is still
	// there, and it is what takes a run that crewflow was killed in the middle of out of
	// the set of the runs that are going (docs.DESIGN.md §7). A machine that is not asked
	// is a machine whose answer is the one the state of the task gives, so a run whose
	// process crewflow cannot ask about goes on counting as a run that is going
	// (docs.DESIGN.md §7c).
	Alive Liveness
	// ConfigPath is the crewflow.toml this run was asked for, which a hint of a
	// refusal names: a person who is told "add app_id to <path>" has to know which
	// file (docs/DESIGN.md §5).
	ConfigPath string
	// Secrets is where the key of the App of the project is kept, and a run in the
	// mode of the bot signs a token with it. A run in the mode of the owner never
	// asks the store for anything, and no run of any mode ever shows what is in it
	// (docs/DESIGN.md §7e, §7i).
	Secrets secret.Store
	// Notices is where the run says that it is waiting for the owner of the machine,
	// and the journal of the attempt is put there for as long as the attempt is open.
	// It is the same notices the store of the machine is behind the wait of, so that
	// a line about the keychain of macOS reaches the person who started the run and
	// the journal of the attempt both, and not one of them alone (§7i).
	Notices *secret.Notices
	// Out is the boundary of everything this run publishes: the terminal of the person
	// who started the command, the journals of the attempts, the events, the state of
	// the task and the report of the run. The values a run must not write down are
	// learned into it — the credentials of the route it goes out by, and the token of
	// the App it works as — and whatever a program of the run prints is cleaned there
	// and not at the place that prints it, because a place is a place a program can
	// go around and a boundary is not (docs/DESIGN.md §7e).
	//
	// A run that was given no boundary makes one of its own: nothing is published
	// outside a command of crewflow, and a run of a machine of a test has no terminal
	// to clean (§7e).
	Out *secret.Out
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
		Tick:     Tick,
		Now:      time.Now,
		Process:  machine.Self,
		Alive:    machine.Alive,
		// The key of the App of the project is in the store of the machine, and a
		// run in the mode of the bot signs a token with it; a run in the mode of the
		// owner never touches the store (docs/DESIGN.md §7i).
		Secrets: secret.System(),
	}
}

// Tick calls onTick every `every` until the context is over, and answers the function
// that stops the tick and waits for it. A run of a task looks at its own silence on it
// while its executor works, and the mark of a standing run is written to the files of
// the attempt while they are open — so a run has to be able to make the tick stop before
// it closes them (docs/DESIGN.md §6, §7a).
func Tick(ctx context.Context, every time.Duration, onTick func()) func() {
	watch, cancel := context.WithCancel(ctx)
	over := make(chan struct{})
	go func() {
		defer close(over)
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-watch.Done():
				return
			case <-ticker.C:
				onTick()
			}
		}
	}()
	return func() {
		cancel()
		<-over
	}
}

// Start starts a program in dir, with the extra environment added to the one of the
// process, and returns what it wrote and the code it exited with. It is the runner of
// doctor, and the reason it is that one is written down there: the stdin of a program is
// the empty device, because an agent that waits for an answer waits for it until the
// timeout of the run is out (docs/DESIGN.md §7a).
func Start(ctx context.Context, name string, args []string, dir string, extraEnv []string) (stdout, stderr []byte, exitCode int, err error) {
	return doctor.Command(ctx, name, args, dir, extraEnv)
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
	// Resume says that this run goes on from the point of the task and not from a
	// message of the orchestrator: a run of the task stood at a decision of a person,
	// the person did the only thing that was his to do, and the run goes on from the
	// same worktree with the task in hand. [Resume] is how a caller asks for it, and a
	// caller that sets it here gets exactly what that function does (docs/DESIGN.md §7i).
	Resume bool
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
	// Resumed says that this attempt went on from the point of the task and not from a
	// message of the orchestrator, and Checkpoint is that point with what came of the
	// request for which it was written. A person reads both: the report of a run has to
	// say whether the work went on from where it stood or from the beginning again
	// (docs/DESIGN.md §7i).
	Resumed    bool        `json:"resumed,omitempty"`
	Checkpoint *Checkpoint `json:"checkpoint,omitempty"`
	// AutoResumed is the habit crewflow went on by itself for in this attempt, and is
	// empty for a first run and for a continuation the orchestrator asked for. A
	// report says it because a run that was answered by itself is not a run two
	// people had (docs/DESIGN.md §7a).
	AutoResumed string `json:"auto_resumed,omitempty"`
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
	// Admission is the record of the pair that let this run start while another run of the
	// project was going, and it is nothing for a run that had the project to itself. A
	// report says it where the run went beside another one: a run that went on by an
	// exception of the owner is a run two people decided on, and it is not a pair of two
	// clean tasks however clean the criteria of its record look (docs.DESIGN.md §7c).
	Admission *task.Admission `json:"admission,omitempty"`
}

// OK is whether the run did what a run is for: it opened the change request of its
// branch and stayed inside the boundaries of the task. Every other outcome is
// something a person or an orchestrator has to decide about, and none of them is a
// failure of the machine (docs/DESIGN.md §6, §8).
func (r Result) OK() bool {
	return r.Outcome == ChangeRequestOpened
}

// The two words of who told a run to go on, as the journal of the attempt holds them: the
// run was answered by crewflow itself or by an orchestrator of the project, and a run that
// was answered by itself is not a run two people had (docs/DESIGN.md §7a).
const (
	byCrewflow = "crewflow"
	byPerson   = "an orchestrator"
)

// Read takes the task from the tracker, checks that it is ready, makes the branch and
// the worktree of it, runs the executor there and works out what came of it.
//
// A run that stopped on a habit crewflow knows goes on by itself, once, in the same
// worktree and the same session: the work of a task is worth more than the minutes an
// orchestrator spends telling the same executor a second time where its scratch is
// (docs/DESIGN.md §7a).
//
// Nothing is created for a task that is not ready or that the tracker does not
// have, and a run that ends in any other way than a change request is still a run
// that is recorded: the state and the journal of it are what the next step of the
// cycle and the next attempt of a person are read from (docs/DESIGN.md §7).
func Run(ctx context.Context, env Env, cfg config.Config, set forge.Set, req Request) (Result, error) {
	r := &runner{env: env, cfg: cfg, set: set, req: req, hang: &hang{}, by: byPerson}
	r.out = env.Out
	if err := r.readTask(ctx); err != nil {
		return Result{}, err
	}
	// One task is run by one run at a time, and the question is asked before anything of
	// the run is made: a second run of a task whose run is going would take a number of an
	// attempt of its own beside the one that is going, and the two runs would write what
	// came of one of them into the state of the other (D-068 RECHECK-FINDING-4, §7).
	if err := r.alone(); err != nil {
		return Result{}, err
	}
	// A second run of one project does not begin without a record of the admission of the
	// pair, and the check is here rather than in the memory of an orchestrator: the rule was
	// written down twice and did not hold the action twice (F-135, F-146). It is applied
	// before the worktree of the task is made and before the key of the App is asked for,
	// so that a refusal of it leaves nothing behind (§7c, §7i).
	if err := r.admit(ctx); err != nil {
		return Result{}, err
	}
	// The point of the task is read and checked before the worktree of it is looked at
	// and long before the key of the app is asked for: a continuation that cannot go on
	// has to say why without a window of the system opening for an answer crewflow
	// already knows (§7i).
	if req.Resume {
		if err := r.checkpoint(ctx); err != nil {
			return Result{}, err
		}
		// What the continuation is told before the task depends on what the run stood at:
		// a decision of a person came through, or the model provider answered again, and
		// the two are not the same sentence (§7i).
		if r.point != nil && r.point.Step == stepModelProvider {
			req.Continue = providerResumedMessage
		}
	}
	if err := r.prepare(ctx); err != nil {
		return Result{}, err
	}
	for {
		result, err := r.start(ctx)
		if err != nil || r.resume.reason == reasonNone {
			return result, err
		}
		// The run goes on by itself in the session of the attempt that has just ended,
		// with the text of the habit it was refused for. The worktree and the branch
		// are the ones of that attempt, the attempt after it is a continuation like
		// any other with the number of it one higher, and the outcome of the task is
		// the one of the last attempt (docs/DESIGN.md §7a, §7h).
		r.auto, r.req.Continue, r.session = string(r.resume.reason), r.resume.tells(), result.Session
		r.by = byCrewflow
		r.resume = resume{}
	}
}

// Resume goes on from the point of the task: the worktree and the branch of the run that
// stopped there, the task in hand and nothing else to say. It is what a person runs after
// doing the one thing that was his to do in the window of the system — "Always Allow" — so
// that a run of the mode of the bot is not started again by hand from the beginning
// (docs/DESIGN.md §7i, MODEL IV-028).
//
// The point is checked before anything of the run happens, and every refusal of it names
// its reason: the head of the branch moved, the task changed, the point is older than
// `[executor] resume_within`, or the person refused the request and a refusal is not
// asked again.
// A refusal creates nothing — no attempt, no journal, no worktree — and asks the machine
// for nothing but the commit the worktree stands at: every attempt to read the key of the
// app without the access opens a window of the system, and crewflow never opens one on its
// own (docs/DESIGN.md §7i).
func Resume(ctx context.Context, env Env, cfg config.Config, set forge.Set, req Request) (Result, error) {
	req.Resume = true
	req.Continue = resumedMessage
	return Run(ctx, env, cfg, set, req)
}

// resumedMessage is what the executor of a continuation is told before the task itself:
// the decision the run was stopped at came through, the work of the task is where the run
// left it, and nothing of that run is to be started over (docs/DESIGN.md §7i).
const resumedMessage = "The decision of the person this run was stopped at came through, so the run goes on " +
	"from that point. The work of the task is in this worktree as the run left it: do not start the task over " +
	"and do not repeat what the run already did."

// providerResumedMessage is what the executor of a continuation from the point of a model
// provider is told before the task itself: the provider is asked again, and the work of the
// task is where the run that was refused left it (F-119, docs/DESIGN.md §7a, §7i).
const providerResumedMessage = "The model provider refused the run before, and this run goes on from that " +
	"point: the provider is being asked again. The work of the task is in this worktree and in this session " +
	"as the run left it: do not start the task over and do not repeat what the run already did."

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
	// closed is what stays closed to the executor of this run on this machine, worked
	// out before it was started and read back when it was refused something: a refusal
	// of a secret is one crewflow never answers by itself (docs/DESIGN.md §7d).
	closed closed
	// policy is the access of the run — the folders that are open with the ask behind
	// each of them, and the places of secrets that are closed — worked out before the
	// worktree of the task was made. It is written into the rights of the agent and
	// shown to the agent in the map of the same run, and the two come out of this one
	// calculation: a run told one thing and given another is a run nobody can read
	// afterwards (docs/DESIGN.md §7d).
	policy access.Policy
	// problems are the paths that were asked for and were not opened. A path of the
	// project among them is a line of the journal of the run; a path of the task stops
	// the run before it starts, and there is none of it left to write.
	problems []access.Problem
	// auto is the habit of the attempt that is going on, when crewflow is the one
	// that goes on with it: an attempt the orchestrator continued has none, and a
	// habit crewflow knows is the one thing a run answers by itself (docs.DESIGN.md
	// §7a).
	auto string
	// attempt is the number this run's attempt got in the state of the task, and
	// every change that attempt goes on to be written under is addressed by it: the
	// state of a task is written by whoever holds the lock of it, and the attempt that
	// happens to be the last one at the moment of a write is not necessarily the
	// attempt of this run (docs.DESIGN.md §7).
	attempt int
	// by is who told the attempt that is going on to go on: crewflow by itself, or an
	// orchestrator of the project. It is said in the journal of the attempt, because a
	// run that was answered by itself is not a run two people had (docs.DESIGN.md §7a).
	by string
	// resume is what the attempt that has just ended is followed by, and is empty
	// unless the run goes on by itself: it is worked out while the journal of the
	// attempt is still open, so that the journal holds the line about it.
	resume resume
	// point is where this run stands if it goes on from the point of the task: the
	// step to go on from, the head of the branch and the request that was made, and
	// what came of it. It is read and checked before the run does anything, and it is
	// empty for every run that is not a continuation from it (docs/DESIGN.md §7i).
	point *Checkpoint
	// alive is the sign of life of the run: when it last showed one, what it was and
	// what it stands at. The state of the task is written from it at every step of
	// crewflow, and the watch of the run reads it while the executor works
	// (docs/DESIGN.md §6, §7a).
	alive *alive
	// hang is what ends a run of the task that stood with nothing to wait for, once, and
	// the silence it hung in. It belongs to the run and not to an attempt: the attempt
	// crewflow goes on with is not stopped a second time (F-143, §7a).
	hang *hang
	// route is what the programs of this run are told about the network: the four names
	// of the profile the owner chose, and nothing else. It is empty in the mode
	// `direct`, because a run that hands its executor a proxy nobody chose is a run that
	// sends a task of a person to a machine of somebody else (docs/DESIGN.md §7d).
	route []string
	// out is the boundary of everything this run publishes: the terminal of the person
	// who started the command, the journals of the attempts, the events, the state of the
	// task and the report of the run. The credentials of the route and the secrets of the
	// identity are learned into it, and whatever a program of this run prints — an
	// executor that prints its own environment is one line of a journal that is kept for
	// ever and pasted into issues — is cleaned there (docs/DESIGN.md §7d, §7e).
	out *secret.Out
	// admission is the record of the pair that let this run start beside another run of
	// the project. It is worked out before the worktree of the task is made, and it is said
	// in the journal of the attempt and in the report of the run: a run that went beside
	// another one is a run somebody decided to let it go, and a person reading it
	// afterwards has to see that (docs.DESIGN.md §7c, §7i).
	admission *task.Admission
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
	// The route of the run is worked out here, before anything of the run is started:
	// a profile whose credentials are missing stops the run here rather than in the
	// middle of a task, and a route nobody can work out is not a route to start a task
	// on (docs/DESIGN.md §7d).
	if err := r.throughRoute(); err != nil {
		return err
	}
	// The access of the run is worked out here, before the worktree of the task is
	// made: a run that may not be given what the task asks for must leave nothing
	// behind, and neither must a run whose agent takes rights of its own somewhere
	// crewflow does not write (docs/DESIGN.md §7d, §7f).
	return r.readings(ctx)
}

// throughRoute is what the programs of this run are told about the network. It is the
// route of the mode of the project — straight out, or through the active profile — and
// the exceptions of the file beside it. The credentials of the profile are read here and
// nowhere else: they go into the environment of the child processes and into nothing that
// a journal or a state of a task holds (docs/DESIGN.md §7d, §7e).
func (r *runner) throughRoute() error {
	route, err := network.Choose(r.cfg, "")
	if err != nil {
		return err
	}
	credentials, err := network.Credentials(r.env.Secrets, route)
	if err != nil {
		return err
	}
	environment, err := route.Environment(r.cfg.Network.NoProxy, credentials)
	if err != nil {
		return err
	}
	r.route = environment
	r.boundary().Learn(secret.Chosen(route.Secrets(credentials)...)...)
	return nil
}

// boundary is the boundary of everything this run publishes: the terminal of the person who
// started the command, the journals of the attempts, the events, the state of the task and
// the report of the run.
//
// A run that was given no boundary of the command makes one of its own, and it is made
// here: a run of a machine of a test has no terminal to clean, and a run that has a route
// with credentials has something to hold back from the moment it is worked out
// (docs.DESIGN.md §7e).
func (r *runner) boundary() *secret.Out {
	if r.out == nil {
		r.out = secret.NewOut()
	}
	return r.out
}

// learns is what a run adds to the boundary of what it publishes when it has found
// another value it must not write down: the secrets of the identity it works under join
// the credentials of the route it goes out through, because a program of the run is
// started with both of them in its environment, and an agent that prints its own
// environment prints them both (docs.DESIGN.md §7e, §7i).
//
// Each value carries the rule of its own kind: a token of an hour is long and is looked
// for from the eighth sign on, and a password of a proxy is as long as its owner made it
// and is looked for whatever it is (docs.DESIGN.md §7e).
func (r *runner) learns() {
	r.boundary().Learn(secret.Generated(r.identity.Secrets...)...)
}

// readings is the policy of the run: the folders the file of the project named, the
// folders the task asked to read beside them with the reason a person wrote, and the
// places of secrets that are closed whatever either of them said. It is worked out
// with the commands of the project and not with what a previous run worked out on a
// machine that may have changed since.
//
// A path of the project that crewflow will not open is not a run that failed: the run
// goes on without it and says so in its journal, because a person who looks at a run
// that was refused a permission for a folder of a dependency has to see that the
// folder was named and was not opened. A path of the task is a different thing: the
// task is what the run was given to do, and a run that cannot read what its task
// needs is a run that would be refused a permission in the middle of the work, so it
// does not start at all (docs/DESIGN.md §7d, §7f).
func (r *runner) readings(ctx context.Context) error {
	if err := r.ownRights(); err != nil {
		return err
	}
	// A field of the task crewflow could not read has already stopped the run in
	// `task.CheckReady`, and a check that could not be made is not a check that
	// passed: the asks of a task are the lines that were read, and nothing else.
	read, _ := task.Read(r.task.Body, r.cfg.Project.Language).Readings()
	policy, problems := access.Resolve(ctx, r.access(), r.cfg.Access, asks(read))
	r.policy, r.problems = policy, problems
	for _, problem := range problems {
		if problem.Source == access.SourceTask {
			return fmt.Errorf("the task asks to read %s, and crewflow will not open it: %s: "+
				"take the path out of the task, or ask a person what the executor may read",
				problem.Path, problem.Reason)
		}
	}
	return nil
}

// asks is what the task asked to read in the words the policy of a run is worked out
// in. The two are one entry and not two — a path of a task without a reason is not an
// ask — and the translation is here, where the two packages meet, and not in either of
// them (docs/DESIGN.md §7d, §7f).
func asks(read []task.Reading) []access.Asked {
	asked := make([]access.Asked, 0, len(read))
	for _, one := range read {
		asked = append(asked, access.Asked{Path: one.Path, Reason: one.Reason})
	}
	return asked
}

// ownRights is that the rights of a run come from crewflow and from nothing else. An
// agent that merges a file of the person into the settings of a run has rights that
// were never written here, in a file that is not in the repository and not in the
// review of the task, and no run of crewflow may be started on them (docs/DESIGN.md
// §7d). The check is made before the worktree of the task: a refused run leaves
// nothing behind.
func (r *runner) ownRights() error {
	var held []string
	for _, rights := range r.profile.ForeignRights(r.env.UserHome, r.env.Environ) {
		if !rights.HoldsRights() {
			continue
		}
		held = append(held, fmt.Sprintf("%s (%s)", rights.Path, rights.Says()))
	}
	if len(held) == 0 {
		return nil
	}
	return fmt.Errorf("the rights of the executor come from somewhere crewflow does not write: %s: "+
		"take the table `permission` out of that file, or out of the folder it is in, "+
		"and run the task again — the rights of a run are written by crewflow alone",
		strings.Join(held, ", "))
}

// prepare makes the worktree of the task, or finds the one a continuation goes on
// in. A first run starts from a fresh default branch: a task that is worked on
// without it would stand on whatever the folder of the person happened to hold.
//
// A continuation from the point of the task goes on in the worktree the run left as well
// as a `-continue` does, and is refused by the point before it gets here (docs/DESIGN.md §7i).
func (r *runner) prepare(ctx context.Context) error {
	if r.req.Continue != "" || r.req.Resume {
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
//
// The git of a run goes out through the route of the project the way the executor does:
// the environment of the route is added to the environment of the process and to nothing
// else, so a git that fetches goes through the profile the owner named, and a run in the
// mode `direct` leaves git as it was (docs/DESIGN.md §7d).
//
// What git said about the failure goes through [runner.quiet]: git was given the address of
// the profile with the credentials in it, and a git that could not reach the host names that
// profile — and the error of a run is a line of the terminal of the person who started the
// command and of the way out of the run (D-068 RECHECK-FINDING-5, §7e, §7i).
func (r *runner) git(ctx context.Context, dir string, args ...string) error {
	_, stderr, code, err := r.started(ctx, "git", args, dir)
	switch {
	case err != nil:
		return r.boundary().Err(fmt.Errorf("git %s: %w", strings.Join(args, " "), err))
	case code != 0:
		return r.boundary().Err(fmt.Errorf("git %s: exited with %d: %s",
			strings.Join(args, " "), code, firstLine(stderr)))
	}
	return nil
}

// output runs one command of git in dir and returns what it wrote, for the one
// command whose answer crewflow reads: the files the run changed. What git said about a
// failure goes through [runner.quiet] here as well: the words of a program of the machine
// are read as they are in every case (docs/DESIGN.md §7e, §7i).
func (r *runner) output(ctx context.Context, dir string, args ...string) (string, error) {
	stdout, stderr, code, err := r.started(ctx, "git", args, dir)
	switch {
	case err != nil:
		return "", r.boundary().Err(fmt.Errorf("git %s: %w", strings.Join(args, " "), err))
	case code != 0:
		return "", r.boundary().Err(fmt.Errorf("git %s: exited with %d: %s",
			strings.Join(args, " "), code, firstLine(stderr)))
	}
	return string(stdout), nil
}

// started is the program of the machine of this run with the route of the project added
// to the environment of it, and nothing else: no setting of the person, no setting of
// git, and no file of the machine anywhere (docs/DESIGN.md §7d).
func (r *runner) started(ctx context.Context, name string, args []string, dir string) (stdout, stderr []byte, exitCode int, err error) {
	return r.env.Command(ctx, name, args, dir, r.route)
}

// reserve is the attempt of this run: the number it gets, the two files it writes into and
// the record that it is going are all one change of the state of the task, made under the
// lock of that state and against the state as it is there now. It is the one place where a
// run of a task takes a number of an attempt, and therefore the one place where a task whose
// run is going is refused: a task does not run beside itself, and no second attempt of a task
// may be begun while the attempt before it is going (docs/DESIGN.md §7, §7c).
//
// The number is worked out here and not before the lock was taken: two runs of one task that
// read the state before either of them wrote were given the same number, and the second of
// them opened the journal of the attempt that was going over it (D-068 RECHECK-FINDING-4).
//
// What the reservation holds is [reservation.was]: a run that turns out never to have started
// puts the state of the task back as it was, and the state of a task that had none is taken
// away instead of left behind empty (F-048, docs/DESIGN.md §7i).
func (r *runner) reserve(started time.Time, step, reason string) (reservation, error) {
	var mine reservation
	state, err := UpdateStateThrough(r.boundary(), r.journals.StatePath(r.task.Number), func(current State) (State, error) {
		// The question of one task and one run at a time is asked under the lock and not
		// only before it: a run that has passed the question outside may arrive here while
		// the run it is about to double is still writing, and this is the answer that
		// holds (§7, D-068 RECHECK-FINDING-4).
		if attempt, is := goingAttempt(current, r.env.Alive); is {
			return State{}, goingRefusal(r.task.Number, attempt)
		}
		reserved, number := r.stateOf(current, started, step, reason)
		mine = reservation{state: reserved, attempt: number, was: len(current.Attempts) > 0}
		return reserved, nil
	})
	if err != nil {
		return reservation{}, err
	}
	mine.state = state
	return mine, nil
}

// reservation is what a run has taken in the state of its task before it started anything:
// the state as it was written with that attempt in it, the number of the attempt, and whether
// the task had a state before this run wrote one (docs.DESIGN.md §7, §7i).
type reservation struct {
	state   State
	attempt int
	was     bool
}

// stateOf is what crewflow keeps of the task with one attempt more in it, out of the state
// the task was in before this run began: a first run of a task starts a state of its own, and
// every run after it is the next attempt of the same task. The attempt is told which process
// the run is happening in, before the executor is started, because that is all that is left
// of a run that crewflow is killed in the middle of (docs.DESIGN.md §7).
//
// The attempt is the one with the number the state says comes next, and the two files of it
// are named after that number: a state that holds one attempt and points at the journal of
// another is a state a person reads and cannot follow, and two attempts under one number are
// two runs writing into one journal (docs/DESIGN.md §7).
//
// The sign of life of the run is what it is at this moment: crewflow is either beginning the
// run of the task or standing in front of the window of the keychain of the machine, and a
// run that is waiting for a person says so where a list of runs and a schedule of an
// orchestrator read it (§6, §7a, §7i).
func (r *runner) stateOf(before State, started time.Time, step, reason string) (State, int) {
	number := before.NextNumber()
	state := before
	state.Number, state.Title = r.task.Number, r.task.Title
	state.Branch, state.Worktree, state.Profile = r.branch, r.worktree, r.profile.Name()
	state.Session = r.session
	process, _ := r.process()
	// The executor and the session of the attempt are of that attempt and not of the
	// task: a task whose executor was replaced starts again in a session of its own,
	// and a continuation goes on in the one before. The session of a first run is
	// not known before the executor is started, and it is written into the attempt
	// when the run is over (§7h).
	return state.NextAttempt(number, StartOf{
		Started:      started,
		Step:         step,
		Reason:       reason,
		Journal:      r.journals.JournalPath(r.task.Number, number),
		ErrorJournal: r.journals.errorJournalPath(r.task.Number, number),
		Executor:     r.profile.Name(),
		Session:      r.session,
		Continued:    r.req.Continue != "",
		Resumed:      r.req.Resume,
		AutoResumed:  r.auto,
		Process:      process,
		Identity:     Identity{Mode: r.cfg.Identity.Mode, Description: r.identity.Description},
	}), number
}

// putStateBack is the state of the task without the attempt of this run, and nothing at
// all where there was no state: an attempt that never was may not be left in the state of
// a task, and a state file that did not exist may not be left behind empty
// (docs/DESIGN.md §7i).
//
// The state is put back under the lock of the task and by taking that one attempt out of
// the state as it is there now: what another command wrote while this run was standing in
// front of the window of the keychain is not this run's to take away (D-068 FINDING-4).
func (r *runner) putStateBack(attempt int, started time.Time, was bool) error {
	path := r.journals.StatePath(r.task.Number)
	release, err := lockState(path)
	if err != nil {
		return err
	}
	defer release()
	current, err := LoadState(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	// What the record on the disk says is taken while it is whole, before the attempt is
	// taken out of it: the removal is made on the attempts behind the record that was read.
	persisted, err := readPersisted(r.boundary(), current)
	if err != nil {
		return err
	}
	kept := current.withoutAttempt(attempt, started)
	if !was && len(kept.Attempts) == 0 {
		// A task that was never run has no state to put back, and an empty one is a file
		// that every reader of the folder of the states walks over for nothing (§7).
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("take the state of the task away: %w", err)
		}
		return nil
	}
	// The bytes are written by `saveState` under the lock held above, over the record as it is
	// there now: a state of a task that is there is changed, and this run changes it by taking
	// one attempt out of it — and the revision of the record is the road's word, taken from the
	// one that was read, so that removal of an attempt is a change of the record like any other
	// and not a road around the revision (D-044, docs.DESIGN.md §7h).
	_, err = saveState(r.boundary(), path, persisted, kept)
	return err
}

// aliveStep is a sign of life of the run at a step of crewflow together with whose name
// the executor of it works under: the state of the task is written from them, because
// that file is what a list of runs, a record under a task and the schedule of an
// orchestrator are worked out of, and a run whose step is not written down is a run whose
// step is a guess (docs/DESIGN.md §6, §7i).
//
// Both are written under the lock of the task and onto the state as it is there now: the
// queue of attention of a schedule and the check of the runs that stand write that state
// too, while the executor of this run works, and a write of the state this run holds in
// its hands would take their records away (D-068 FINDING-4). They are written into the
// attempt of this run by its number and not into the last attempt of the state: what is
// written here belongs to the run that is writing it (D-068 RECHECK-FINDING-4).
//
// A state that cannot be written is said on the way out of the run and the run goes on:
// the first state of a run that cannot be written stops the run before its executor is
// started, and by this point the run is going with the state of it already on disk. A
// run that stopped because it could not write a sign of life is a run nobody is
// watching, which is the failure the sign of life is here for.
func (r *runner) aliveStep(state State, step, reason string, identity Identity) State {
	now := r.env.Now()
	r.alive.show(now, step, reason)
	written, err := UpdateStateThrough(r.boundary(), r.journals.StatePath(r.task.Number), func(current State) (State, error) {
		return current.Identified(r.attempt, identity).Alive(r.attempt, now, step, reason), nil
	})
	if err != nil {
		r.note(r.own(state).ErrorJournal, err)
		return state
	}
	return written
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
// The state of the task is written before crewflow goes to the machine for anything, and
// again when the run is done: a run that crewflow is killed in the middle of has to
// leave behind the attempt, the worktree and the journal of what it was doing, and a run
// that stands in front of the window of the keychain has to be in the state of the task
// while it stands there — four runs of the mode of the bot stood for an hour and a half
// with not a line written down, and nobody could see them (F-039, F-041, §7, §7i).
func (r *runner) start(ctx context.Context) (Result, error) {
	timeout, err := time.ParseDuration(r.cfg.Executor.Timeout)
	if err != nil {
		return Result{}, fmt.Errorf("executor.timeout: %w", err)
	}
	stallAfter, err := time.ParseDuration(r.cfg.Executor.StallAfter)
	if err != nil {
		return Result{}, fmt.Errorf("executor.stall_after: %w", err)
	}
	command, err := r.command()
	if err != nil {
		return Result{}, err
	}
	// The attempt of this run is taken in the state of the task before anything else of
	// it, under the lock of that state: the number it gets and the two files it writes into
	// are one change, and a task whose run is going is refused there (§7, D-068
	// RECHECK-FINDING-4).
	//
	// The state of the task is written before the identity of the run is worked out,
	// because the run is already a run at that moment and the identity is the one thing
	// about it that can make macOS ask the owner in a window of the system. What the run
	// stands at is written with it: a run in the mode of the bot goes to the keychain of
	// the machine, and a run that is not answered there is a run that needs a person and
	// not a run that is quiet (F-039, §7i).
	//
	// The mode of the run is what the file of the project says, and the state of the
	// task holds the mode and not the rest of the identity, so the state is complete
	// before crewflow has asked anybody for anything (§7h, §7i).
	r.alive = &alive{}
	started := r.env.Now()
	step, reason := stepBegan, ""
	if r.cfg.Identity.Mode == forge.ModeBot {
		step, reason = stepIdentity, reasonApproval
	}
	r.alive.show(started, step, reason)
	taken, err := r.reserve(started, step, reason)
	if err != nil {
		return Result{}, err
	}
	state, number := taken.state, taken.attempt
	r.attempt = number
	// The journal of the attempt is opened after the reservation and not before it: the two
	// files are named after the number the attempt got, and a journal opened before that
	// number is known is the journal of an attempt nobody holds (§7).
	//
	// A journal that could not be opened is a run that did not begin, and it leaves
	// nothing behind: the attempt is taken out of the state of the task again, and what
	// another command wrote in the state of that task while this run was trying stays
	// (F-048, §7i).
	files, err := r.journals.Begin(r.boundary(), r.task.Number, number)
	if err != nil {
		return Result{}, errors.Join(err, r.putStateBack(number, started, taken.was))
	}
	listening := r.env.Notices.Listening(files.Out)
	defer listening()

	// The watch of the silence of the run is started here and not when the executor is:
	// everything from here to the executor is crewflow waiting for something, and that is
	// exactly what a run that nobody is watching is (§6, §7a).
	mine, _ := state.Attempt(number)
	stopWatch := r.watch(ctx, mine, files, stallAfter, r.mayGoOnAfterStanding(state))
	defer stopWatch()

	// Whose name the executor of this run works under is worked out before the executor
	// is started, because a run in the mode of the bot that cannot be given a token of
	// its own is a run that is not started at all (§7i).
	r.identity = forge.Identity{Mode: r.cfg.Identity.Mode}
	if r.identity, err = r.identityOf(ctx); err != nil {
		if errors.Is(err, secret.ErrApproval) {
			// The wait is over and nobody answered the window: the attempt ends here,
			// and the state of the task keeps the attempt with it — it is the one
			// attempt of §7 that is written down without an executor having run
			// (§7i).
			stopWatch()
			return r.blockedOnApproval(ctx, files, err)
		}
		if r.point != nil {
			// The run went on from the point of the task and the person refused the
			// window of the system this time: the work of the task stays where it is,
			// the attempt ends here, and the point keeps the refusal, so that the next
			// continuation says that it was refused instead of asking again (§7i).
			stopWatch()
			return r.deniedOnAuthorization(files, err)
		}
		// A run that never started its executor leaves nothing behind: neither the
		// journal of an attempt that was not made, nor the worktree and the branch it
		// made, which nothing written down points at and the next run of the task cannot
		// come back to (docs/DESIGN.md §7i). The state of the task goes back to what it
		// was before this run wrote its attempt into it: a run that was not a run may
		// not leave an attempt behind (F-039 put the state there first, and F-048 keeps
		// the truth of it).
		stopWatch()
		return Result{}, errors.Join(err, files.takeAway(), r.putStateBack(number, started, taken.was), r.takeRunAway(ctx))
	}
	// The token of the identity is learned into the boundary of the run as soon as it is
	// signed: it is in the environment of every program this run starts from here on, and
	// an executor that prints its own environment is one line of a journal kept for ever
	// and pasted into issues (docs.DESIGN.md §7e, §7i).
	r.learns()
	// The key of the app of the host was read and a token is signed with it, so a
	// request the run was stopped at came through. The state of the task says so and
	// the journal of the attempt holds the event of it, in the same place the run said
	// what it waited for: a person who reads the journal of a continuation afterwards
	// sees what was decided and when (§7i). A continuation from the point of a model
	// provider has no request of a person to have come through and says nothing of one.
	if r.point == nil || r.point.Step == stepReadKey {
		r.answered(files, WaitCompleted, EventAuthorizationCompleted)
	}
	// The run is going to get the task ready for its executor, and that is the sign of
	// life the state of the task holds from here on: whatever the run stood at before
	// this — the window of the keychain, the time of the machine to answer — is behind it.
	//
	// Whose name the executor works under goes into the state with it, because the state
	// says it: the mode of the file of the project is what the state holds while crewflow
	// is still working it out, and the mode the host answered with is what it holds from
	// here on (§7i).
	identity := Identity{Mode: r.identity.Mode, Description: r.identity.Description}
	state = r.aliveStep(state, stepPreparing, "", identity)
	attempt, _ := state.Attempt(number)
	if err := r.scratch(ctx); err != nil {
		stopWatch()
		_ = files.Close()
		return r.stopBeforeStart(err)
	}
	// Whose name the executor of the run worked under, and what it went on with, are the
	// first lines of the journal, before what the executor did and before the rights it
	// was given: a person reading a journal of a run afterwards has to see whose name it
	// went under without reading the state file as well, and a run that went beside
	// another one has to see there that it did (docs.DESIGN.md §7i, §7c).
	fmt.Fprintf(files.Out, "crewflow: executor: %s\n", r.identity.Description)
	if r.admission != nil {
		fmt.Fprintln(files.Out, markOf(*r.admission))
	}
	// Every attempt that goes on in the session of the attempt before it says so in its own
	// journal, with what it went on for and the session it goes on in: a person who opens
	// the journal of the second attempt of a task has to see there whether crewflow went on
	// by itself or an orchestrator asked for it (docs.DESIGN.md §7a).
	if r.req.Continue != "" {
		facts := []string{"by=" + r.by, "session=" + r.session, "worktree=" + r.worktree}
		if r.auto != "" {
			facts = append(facts, "habit="+r.auto)
		}
		sayEvent(files.Out, EventRunResumed, append(facts, saidAtEvent(r.env.Now()))...)
	}
	// What the executor may read outside its worktree is worked out before it is
	// started and is said in the journal right after that: a run that was given the
	// right to read a folder of the machine has to say which, and a person who reads
	// the journal of the run afterwards sees it there (docs/DESIGN.md §7d).
	rights, err := r.rights(files)
	if err != nil {
		stopWatch()
		_ = files.Close()
		return r.stopBeforeStart(err)
	}
	// A run in the mode of the bot sets its worktree up before the executor is
	// started: the helper git takes a fresh token from, and the hook that refuses a
	// push anywhere but the branch of the task (§7i).
	if err := r.bot(ctx); err != nil {
		stopWatch()
		_ = files.Close()
		return r.stopBeforeStart(err)
	}
	state = r.aliveStep(state, stepExecutor, "", identity)

	// The time limit of the run is on the context, and a program that is still
	// going when it is out is asked to stop with it: the run has an end whatever the
	// executor thinks of it. The same context ends the run when it has stood with a
	// provider that has answered — once, and the watch of the run is what decides it
	// (F-143, §7a).
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	r.hang.running(cancel)
	defer r.hang.over()

	// What the executor wrote is in the journal from its first line and is also kept
	// in memory, because a run is judged out of what the agent said, and the file is
	// the only one of the two a person and a watch can read (docs/DESIGN.md §7a).
	//
	// The files of the run are the boundary of the run, made when they were opened: the
	// token of a run is in the environment of the executor, and so is the password of the
	// profile the owner chose the route through, and an agent that prints its own
	// environment is one line of a journal that would carry both of them into a file kept
	// for ever and pasted into an issue (§7e, §7i).
	var out, errOut bytes.Buffer
	environment := slices.Concat(rights, r.identity.Env, tempEnv(r.worktree), r.route)
	code, err := r.env.Stream(runCtx, command[0], command[1:], r.worktree, environment,
		io.MultiWriter(&out, files.Out), io.MultiWriter(&errOut, files.ErrOut))
	// What is held back is a beginning of a line and may be the beginning of a
	// secret, and the run is over: it is written before the files are closed.
	ended := r.endOf(ctx, runCtx)
	flushed := files.Flush()
	result := r.resultOf(attempt, r.env.Now(), files)
	result.ExitCode = code
	if err != nil {
		// The executor could not be started at all. That is said where a person
		// reads the way out of a run, and the outcome is a run that failed.
		result.ErrorJournal = r.noteError(files.ErrorJournal, err)
	}
	// The session of the run is what a continuation goes on in, and it is found in
	// what the executor wrote even when the run ended badly.
	result.Session = r.profile.SessionID(out.Bytes())

	// What the run reached for a secret is worked out of the refusals of the run and
	// the calls of the executor before anything is judged by it: the outcome of the
	// run and the line the journal of it holds are one answer read twice
	// (docs/DESIGN.md §7a.1, §7d, §8).
	calls := r.profile.Calls(out.Bytes())
	secrets := r.closed.found(r.profile.Rejections(out.Bytes(), errOut.Bytes()), calls, r.worktree)
	provider := r.profile.ProviderFailure(out.Bytes(), errOut.Bytes())
	result, judgeErr := r.outcome(runCtx, result, out.Bytes(), errOut.Bytes(), code, ended, secrets)
	// What the run reached for a secret is said while the journal of the attempt is
	// still open, next to the line of a run that goes on by itself, so that a watch of
	// the attempt shows the place, the kind of access and that it is not continued
	// (docs/DESIGN.md §7a.1, §7d).
	if line := secrets.line(); line != "" {
		fmt.Fprint(files.Out, line)
	}
	// What came of the model provider is said while the journal of the attempt is still
	// open, and the run that stopped for it writes the point to go on from: the work of
	// the task, the branch of it and the session of it are where the run left them, and a
	// person who comes back in the morning continues it with `crewflow task resume` rather
	// than starting the task over (F-119, §6a, §7i).
	r.saysWhatCameOfProvider(files, result.Attempt, provider, len(out.Bytes()) > 0)
	if refused, ok := refuseProvider(provider); ok && result.Outcome == Blocked {
		r.pointedAtProvider(ctx, state.Worktree, files, refused)
	}
	// What the run is going on with is worked out and said in the same place: the line
	// belongs to the attempt that ended here, and a watch of it shows why the next one
	// was given what it was (docs.DESIGN.md §7a).
	next, ending := r.goesOnByItself(&result, state, calls)
	if next.reason != reasonNone {
		fmt.Fprintf(files.Out, "%s\n", next.line())
		// The retry of a refused provider is said as an event of its own, beside the line
		// of the run that goes on: the two facts a person reads a journal for are what
		// happened and what crewflow did about it (§6a, §7a).
		if string(next.reason) == ReasonProviderUnavailable {
			sayEvent(files.Out, EventProviderRetryScheduled,
				"resource="+SubjectModelProvider, "attempt="+strconv.Itoa(result.Attempt+1),
				saidAtEvent(r.env.Now()))
		}
		r.resume = next
	}
	if closeErr := errors.Join(files.Close(), flushed); closeErr != nil {
		return result, closeErr
	}
	return result, r.keep(ending, result, judgeErr)
}

// stopBeforeStart is what a run does when the executor could not be started at all:
// the attempt ends here, and the state of the task says so. An attempt left running
// would be a run that goes on in every watch of the task, and there is none.
//
// The outcome is written onto the state as it is there now, under the lock of the task and
// into the attempt of this run by its number: what another command wrote beside it is kept,
// and the attempt of another run is not where the end of this one belongs
// (D-068 FINDING-4, D-068 RECHECK-FINDING-4).
func (r *runner) stopBeforeStart(err error) (Result, error) {
	ended := r.env.Now()
	if _, keepErr := UpdateStateThrough(r.boundary(), r.journals.StatePath(r.task.Number), func(state State) (State, error) {
		return state.Ended(r.attempt, ended, ExecutorFailed), nil
	}); keepErr != nil {
		return Result{}, errors.Join(err, keepErr)
	}
	return Result{}, err
}

// rights is the environment of the executor with the access of the run in it: the
// folders that are open are opened to the executor for reading, the places of secrets
// are closed, and nothing of either may be written. The policy is the one worked out
// before the worktree of the task was made, and the first line of the journal says
// every folder of it with the ask behind it: a run that was given the right to read a
// folder of the machine has to say which, which hand asked for it and why, and a
// person who reads the journal of the run afterwards sees it there (docs/DESIGN.md
// §7d).
//
// A policy that cannot be named to the agent at all stops the run before the executor
// is started: an agent that was told nothing is an agent whose rights crewflow does
// not know.
func (r *runner) rights(files *AttemptFiles) ([]string, error) {
	for _, problem := range r.problems {
		r.note(files.ErrorJournal, fmt.Errorf("access: %s: %s", problem.Path, problem.Reason))
	}
	env, err := r.profile.AccessEnv(r.policy, r.env.Environ)
	if err != nil {
		return nil, err
	}
	// The agent is told what is closed to it, and the run keeps the same list to read a
	// refusal against: a run of a machine that was refused a key is a run that stopped
	// for a reason of its own, whatever the command around it looked like, and the
	// orchestrator is the one who decides about that (docs/DESIGN.md §7a, §7d).
	r.closed = newClosed(r.policy, r.env.UserHome)
	fmt.Fprintf(files.Out, "crewflow: the executor may write: %s\n", listed(writable(r.worktree, Scratch(r.worktree))))
	fmt.Fprintf(files.Out, "crewflow: the executor may read outside the worktree: %s\n", listed(r.policy.Read))
	fmt.Fprintf(files.Out, "crewflow: it may never read: %s\n", listedPaths(r.policy.Deny))
	return env, nil
}

// access is the machine as the policy of reading needs it: the home of the person and
// the way a command of the project is started, the two of which a run already has for
// everything else it runs (§7d).
func (r *runner) access() access.Env {
	return access.Env{
		Home: r.env.UserHome,
		Run: func(ctx context.Context, name string, args []string) ([]byte, []byte, int, error) {
			return r.started(ctx, name, args, "")
		},
	}
}

// listed is a list of the access of a run as one line of a journal reads it: a person
// reads the line and not a list of them, and an empty policy is said so rather than
// left blank. A folder that is open is named with the ask behind it, because a path in
// a journal says nothing about who wanted it and why it was allowed.
func listed(grants []access.Grant) string {
	if len(grants) == 0 {
		return "nothing"
	}
	parts := make([]string, 0, len(grants))
	for _, grant := range grants {
		parts = append(parts, grant.Line())
	}
	return strings.Join(parts, ", ")
}

// listedPaths is a list of places as one line of a journal reads it: the places that
// are closed are the same wherever they stand, and a line that repeated the same
// sentence after every one of them would say nothing.
func listedPaths(paths []string) string {
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
//
// A third end is crewflow's own: a run that showed nothing for longer than the silence of
// the project, with nothing standing in the way that it knows of, is stopped here and goes
// on in the same session — and the mark of that silence is the outcome of the attempt it
// ended, because the run did not fail at anything (F-143, §7a).
func (r *runner) endOf(caller, run context.Context) Kind {
	switch {
	case errors.Is(caller.Err(), context.Canceled):
		return Interrupted
	case errors.Is(run.Err(), context.DeadlineExceeded):
		return TimedOut
	case r.hang.stoodHere():
		return Stalled
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
//
// Every one of these is a change of the state of the task and not a state of it: it is
// made under the lock of the task and onto the state as it is there now, so that what the
// queue of attention or the check of the runs that stand wrote while the executor was
// working is in the file beside what this run has to say (D-068 FINDING-4). Every one of
// them is made in the attempt of this run by its number and not in the last attempt
// whatever it is now: this run ending while the attempt of another run was added before it
// is what put the outcome of one of them into the state of the other (D-068
// RECHECK-FINDING-4).
func (r *runner) keep(ending ended, result Result, judgeErr error) error {
	if _, err := UpdateStateThrough(r.boundary(), r.journals.StatePath(r.task.Number), func(state State) (State, error) {
		state = state.Ended(r.attempt, result.EndedAt, result.Outcome)
		if ending.reason != "" {
			state = state.Reason(r.attempt, ending.reason)
		}
		if ending.marked != "" {
			state = state.Provider(r.attempt, ending.marked)
		}
		if r.point != nil {
			// The point of the task is kept on the run itself, and the state holds what
			// the run holds: the report, the state and the refusal of the next
			// continuation all read one fact, and three copies of it would be three
			// things that can disagree (docs.DESIGN.md §7h, §7i).
			state.Checkpoint = r.point
		}
		if result.Session != "" {
			state.Session = result.Session
			// The session of a run belongs to the attempt it went in, and not only to
			// the task: every attempt of a task has its own session, and a continuation
			// goes on in the one of the attempt it follows (docs.DESIGN.md §7h).
			state = state.InSession(r.attempt, result.Session)
		}
		if change := result.ChangeRequest; change != nil {
			state.Change = &Change{Number: change.Number, URL: change.URL}
		}
		return state, nil
	}); err != nil {
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
// about it is in the return of the caller. What is written goes through the boundary
// of the run: an error of a run may carry what a program of it printed, and a file of a
// run is read by people and pasted into issues (§7e).
func (r *runner) note(path string, err error) {
	previous, readErr := os.ReadFile(path)
	if readErr == nil {
		said, refuse := r.boundary().Publish(secret.ChannelJournal, err.Error())
		if refuse != nil {
			said = secret.Redacted
		}
		line := fmt.Sprintf("crewflow: %v\n", said)
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
		return Prompt(r.task, r.cfg, r.branch, r.worktree, r.policy)
	}
	if r.session != "" {
		return r.req.Continue, nil
	}
	return Continuation(r.req.Continue, r.task, r.cfg, r.branch, r.worktree, r.policy)
}
