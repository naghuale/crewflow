package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/doctor"
	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/forge/roles"
	"github.com/naghuale/crewflow/internal/proc"
	taskrun "github.com/naghuale/crewflow/internal/run"
	"github.com/naghuale/crewflow/internal/secret"
	"github.com/naghuale/crewflow/internal/task"
)

// crewflowHome is the root of what crewflow keeps of its own: the journal of every
// run and the state of every task, outside the projects themselves
// (docs/DESIGN.md §7).
const crewflowHome = "~/.crewflow"

// watchTick is how often a watch looks at the journal of a run and at the state of
// the task: a run is a stream of lines, and a person is reading them, but a terminal
// is not a terminal of a thousand lines a second.
const watchTick = 500 * time.Millisecond

// taskRoles and taskRunEnv are how the task command gets the roles of a project and
// the machine to run a task on. They are variables so that a test of the command
// runs against a host and a machine of its own and reaches nothing.
var (
	taskRoles  = roles.New
	taskRunEnv = taskrun.System
	// taskMachine is how a list of runs asks the machine whether a run is still going,
	// and taskClock is the clock it says how long that run has been going. They are
	// variables so that a test of the command never looks at the process of a run it
	// did not start.
	taskMachine = proc.System()
	taskClock   = time.Now
)

// runTask runs one task, in a worktree of its own, and says how the run ended.
func runTask(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintf(stderr, "crewflow task: nothing to do\n\n")
		usage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "run":
		return runTaskRun(args[1:], stdout, stderr)
	case "check":
		return runTaskCheck(args[1:], stdout, stderr)
	case "watch":
		return runTaskWatch(args[1:], stdout, stderr)
	case "list":
		return runTaskList(args[1:], stdout, stderr)
	case "attention":
		return runTaskAttention(args[1:], stdout, stderr)
	case "check-stalled":
		return runTaskCheckStalled(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		usage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "crewflow task: unknown subcommand %q\n\n", args[0])
		usage(stderr)
		return exitUsage
	}
}

// runTaskRun is `crewflow task run <N>`: it takes the task from the tracker, checks
// that it is ready, makes the branch and the worktree of it, runs the executor
// there and reports what came of it. The code of the command is zero only when the
// run opened the change request of its branch and changed nothing it was not to
// change: every other outcome is something a person or an orchestrator has to
// decide about, and a code cannot decide it (docs/DESIGN.md §6).
func runTaskRun(args []string, stdout, stderr io.Writer) int {
	flags := taskFlags("run", stderr)
	configPath := flags.String("config", defaultConfigPath, "path to crewflow.toml")
	repoDir := flags.String("repo", "", "the repository to make the worktree out of, the folder crewflow was called in when empty")
	continueMessage := flags.String("continue", "", "go on with this message, in the same worktree and the same session")
	asJSON := flags.Bool("json", false, "print the outcome as JSON, for the orchestrator")
	number, code := takeNumber("run", args, flags, stderr)
	if code != exitOK {
		return code
	}
	wanted, named := taskNumber(stderr, "run", number)
	if !named {
		return exitUsage
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return failed(stderr, err)
	}
	home, err := cfg.ExpandPath(crewflowHome)
	if err != nil {
		return failed(stderr, err)
	}
	// Everything crewflow says about the machine of this run — the line about the window
	// of the keychain of macOS above all — is said to the terminal of the person and, as
	// soon as the run has a journal of its own, to that journal too. The store of the
	// secrets and the run are given the same notices, so that one line reaches both and
	// not one of them (docs/DESIGN.md §7i).
	notices := secret.NewNotices(stderr)
	set, err := taskRoles(cfg, roleEnv(*configPath, notices))
	if err != nil {
		return failed(stderr, err)
	}

	// A run that a person stops stops the executor with it and says the run was
	// interrupted: an executor left to work on without anyone would go on writing
	// into a worktree nobody watches (docs/DESIGN.md §7a).
	ctx, stop := stoppedBy()
	defer stop()
	env := taskRunEnv(home)
	env.ConfigPath = *configPath
	env.Notices = notices
	result, err := taskrun.Run(ctx, env, cfg, set, taskrun.Request{
		Number:   wanted,
		RepoDir:  *repoDir,
		Continue: *continueMessage,
	})
	if err != nil {
		failed(stderr, err)
	}
	if *asJSON {
		if printErr := printJSON(stdout, result); printErr != nil {
			return failed(stderr, printErr)
		}
	} else if result.Task != 0 {
		printResult(stdout, result)
	}
	if err != nil {
		return exitFailure
	}
	if !result.OK() {
		return exitFailure
	}
	return exitOK
}

// runTaskCheck is `crewflow task check <N>`: it takes the task from the tracker and
// says whether it may be run, and what it is missing when it may not. Nothing is
// created for a check: no branch, no worktree, no attempt, no state, because a check
// that made a worktree would already be a run of a task (docs/DESIGN.md §7f).
func runTaskCheck(args []string, stdout, stderr io.Writer) int {
	flags := taskFlags("check", stderr)
	configPath := flags.String("config", defaultConfigPath, "path to crewflow.toml")
	asJSON := flags.Bool("json", false, "print the answer as JSON, for the orchestrator")
	number, code := takeNumber("check", args, flags, stderr)
	if code != exitOK {
		return code
	}
	wanted, named := taskNumber(stderr, "check", number)
	if !named {
		return exitUsage
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return failed(stderr, err)
	}
	set, err := taskRoles(cfg, roleEnv(*configPath, secret.NewNotices(stderr)))
	if err != nil {
		return failed(stderr, err)
	}

	readiness, err := task.Check(context.Background(), set, cfg, wanted)
	if err != nil {
		return failed(stderr, err)
	}
	if *asJSON {
		if err := printJSON(stdout, readiness); err != nil {
			return failed(stderr, err)
		}
	} else {
		printReadiness(stdout, readiness)
	}
	if !readiness.Ready {
		return exitFailure
	}
	return exitOK
}

// runTaskWatch is `crewflow task watch <N>`: it shows what the executor of the last
// attempt of a task wrote, as the profile of the run reads it, and it goes on
// showing while the attempt is still running. It creates nothing and starts nobody: a
// watch of a run is a second terminal, not a second run (docs/DESIGN.md §7).
func runTaskWatch(args []string, stdout, stderr io.Writer) int {
	flags := taskFlags("watch", stderr)
	configPath := flags.String("config", defaultConfigPath, "path to crewflow.toml")
	attempt := flags.Int("attempt", 0, "the attempt to watch, the last one of the task when not named")
	number, code := takeNumber("watch", args, flags, stderr)
	if code != exitOK {
		return code
	}
	wanted, named := taskNumber(stderr, "watch", number)
	if !named {
		return exitUsage
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return failed(stderr, err)
	}
	home, err := cfg.ExpandPath(crewflowHome)
	if err != nil {
		return failed(stderr, err)
	}
	watch, err := taskrun.Watch(home, cfg.RepoName(), wanted, *attempt)
	if err != nil {
		return failed(stderr, err)
	}
	printWatch(stdout, watch)

	ctx, stop := stoppedBy()
	defer stop()
	ticker := time.NewTicker(watchTick)
	defer ticker.Stop()
	// A person who stops a watch stops watching: the run of the task goes on in its
	// own terminal and in the worktree of its own.
	if err := watch.Follow(ctx, stdout, ticker.C); err != nil && !errors.Is(err, context.Canceled) {
		return failed(stderr, err)
	}
	return exitOK
}

// runTaskList is `crewflow task list`: the tasks of the project that were run, the ones
// that want somebody today on top and the rest from the last to the first, and under
// the table what the other projects of the machine are doing. It reads the state
// crewflow kept and nothing else — no tracker, no host, no network — and creates
// nothing: a list of runs is a question, and a question is not a run
// (docs/DESIGN.md §6, §7).
//
// The project is the one the file of the project names, and -repo is the checkout that
// file is looked for in — the same flag as in `task run`, and the same way it is used:
// a person points it at the folder they work in. -all is another question: every run
// of every project of the machine, from any folder, with the project in a column of it.
func runTaskList(args []string, stdout, stderr io.Writer) int {
	flags := taskFlags("list", stderr)
	configPath := flags.String("config", "", "path to crewflow.toml, the one in -repo when not named")
	repo := flags.String("repo", "", "the checkout whose runs to show, the folder crewflow was called in when empty")
	asJSON := flags.Bool("json", false, "print the list as JSON, for the orchestrator")
	all := flags.Bool("all", false, "show the runs of every project on this machine, from any folder")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "crewflow task list: unexpected argument %q\n\n", flags.Arg(0))
		usage(stderr)
		return exitUsage
	}
	if *all && *repo != "" {
		fmt.Fprintf(stderr, "crewflow task list: -all is of every project on this machine, -repo is of one of them\n\n")
		usage(stderr)
		return exitUsage
	}
	if *repo != "" {
		if err := checkoutAt(*repo); err != nil {
			return failed(stderr, fmt.Errorf("-repo %s: %w", *repo, err))
		}
	}

	runs, err := listOfRuns(context.Background(), *configPath, *repo, *all, taskrun.ListEnv{
		Now:     taskClock,
		Running: taskMachine.Alive,
	})
	if err != nil {
		return failed(stderr, err)
	}

	if *asJSON {
		// The answer of a command is what an orchestrator reads, and a state file
		// that could not be read and a list that was left short are not part of it:
		// they go beside it, where the answer stays whole.
		entries := runs.Entries
		if entries == nil {
			entries = []taskrun.Entry{}
		}
		if err := printJSON(stdout, entries); err != nil {
			return failed(stderr, err)
		}
		if err := runs.Notes(stderr); err != nil {
			return failed(stderr, err)
		}
		return exitOK
	}
	if err := runs.Write(stdout, taskScreen(stdout, taskClock())); err != nil {
		return failed(stderr, err)
	}
	return exitOK
}

// runTaskAttention is `crewflow task attention`: the queue of attention of the project —
// only the tasks that want a person — with the state of the process of each of them, the
// reason of it, what it waits for, who acts next, how long it has been that way and what may
// be done about it (docs/DESIGN.md §6a).
//
// It is the same queue that `crewflow task list` shows over its table, with the host of the
// project in it: the labels of the tasks and the records under their change requests, so
// that a run that ended with an open change and a review of it is not in the queue at all.
// It is made for the schedule of an orchestrator, and it leaves a record under a task that
// has waited for longer than `[attention] escalate_after`: one for the key of the entry — the
// task, the state, the priority and the reason — and not again for a day while the key is
// the same, so that a schedule that runs every minute leaves one line under a task and not
// one a minute. The code is not zero while anything in the queue wants a person.
func runTaskAttention(args []string, stdout, stderr io.Writer) int {
	flags := taskFlags("attention", stderr)
	configPath := flags.String("config", defaultConfigPath, "path to crewflow.toml")
	asJSON := flags.Bool("json", false, "print the queue as JSON, for the orchestrator")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "crewflow task attention: unexpected argument %q\n\n", flags.Arg(0))
		usage(stderr)
		return exitUsage
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return failed(stderr, err)
	}
	env, err := attentionEnv(cfg)
	if err != nil {
		return failed(stderr, err)
	}
	home, err := whereCrewflowKeeps()
	if err != nil {
		return failed(stderr, err)
	}
	ctx, stop := stoppedBy()
	defer stop()
	// The state of the tasks is read first and the host is asked about the roles of the
	// project only where the queue is worked out of it: a project whose host is not there
	// is answered from the state alone, and says so in the entries it could not ask
	// about (docs/DESIGN.md §6a).
	queue, err := taskrun.CheckAttention(ctx, home, cfg.RepoName(), env,
		hostOfTheProject(ctx, cfg, *configPath), noticeUnderTheTask(cfg, *configPath))
	if err != nil {
		return failed(stderr, err)
	}
	if *asJSON {
		if err := printJSON(stdout, queue); err != nil {
			return failed(stderr, err)
		}
	} else if err := queue.Write(stdout, taskScreen(stdout, taskClock())); err != nil {
		return failed(stderr, err)
	}
	if err := queue.Notes(stderr); err != nil {
		return failed(stderr, err)
	}
	if len(queue.Entries) == 0 {
		if !*asJSON {
			fmt.Fprintln(stdout, "nothing wants a person: the runs of this project are working or are over")
		}
		return exitOK
	}
	// A task in the queue is a thing a person has to do something about, and the code of
	// the command says so, whatever crewflow managed to write under the task.
	return exitFailure
}

// hostOfTheProject is the host of the project as the queue of attention asks about it.
//
// A project whose roles cannot be built gets a host that answers nothing and says why: the
// queue is then worked out of the state of the tasks alone, which is a whole answer for
// everything but the review and the acceptance of a change, and every entry of it says that
// the host was not read. A queue that says "nobody is waiting" because the host was out of
// reach would be lying about the work of the project (docs/DESIGN.md §6a, §7h).
func hostOfTheProject(ctx context.Context, cfg config.Config, configPath string) taskrun.Host {
	set, err := taskRoles(cfg, roleEnv(configPath, secret.NewNotices(io.Discard)))
	if err != nil {
		return hostUnreachable{err}
	}
	host, err := attentionHost(ctx, set, cfg)
	if err != nil {
		return hostUnreachable{err}
	}
	return host
}

// hostUnreachable is a host of a project crewflow could not reach or could not read the
// accounts of: it says why for every task, and the queue puts that into the entries it could
// not work out of the host (docs.DESIGN.md §7h).
type hostUnreachable struct{ err error }

// FactsOf is the failure itself, and nothing else: there is no fact of a host that is not
// there, and a queue that guessed one would act on a guess.
func (h hostUnreachable) FactsOf(context.Context, int, int) (taskrun.HostFacts, error) {
	return taskrun.HostFacts{}, h.err
}

// runTaskCheckStalled is `crewflow task check-stalled`: the runs of the project that
// stand — that is, that are going and have shown no sign of life for longer than
// `[executor] stall_after` — with how long each of them has been standing, what it was
// doing when it last showed anything, and what it stands at where crewflow knows that.
// It also answers the runs that stood and are no longer standing, once each: the end of
// an episode of silence is a thing a person has to learn from the task as well.
//
// The command is made for the schedule of an orchestrator, and it says what it wrote
// under the tasks: one line for the beginning of a silence and one for its end, whatever
// the number of turns in between, because a person who reads a task is not to find two
// hundred lines about one run there. It reads the state of the tasks and writes nothing
// else, and it reaches the host only when there is a record to leave under a task
// (docs/DESIGN.md §6).
func runTaskCheckStalled(args []string, stdout, stderr io.Writer) int {
	flags := taskFlags("check-stalled", stderr)
	configPath := flags.String("config", defaultConfigPath, "path to crewflow.toml")
	asJSON := flags.Bool("json", false, "print the answer as JSON, for the orchestrator")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "crewflow task check-stalled: unexpected argument %q\n\n", flags.Arg(0))
		usage(stderr)
		return exitUsage
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return failed(stderr, err)
	}
	env, err := stallEnv(cfg)
	if err != nil {
		return failed(stderr, err)
	}
	home, err := whereCrewflowKeeps()
	if err != nil {
		return failed(stderr, err)
	}
	repo := cfg.RepoName()
	// The state of the tasks is read first, and the host is asked about the roles of the
	// project only where there is something to leave under a task: a project whose host
	// is not there, or a run that is working, is answered without a network at all
	// (docs/DESIGN.md §6).
	standings, err := taskrun.CheckStalled(context.Background(), home, repo, env, recordUnderTheTask(cfg, *configPath))
	if err != nil {
		return failed(stderr, err)
	}
	if *asJSON {
		answer := standings
		if answer == nil {
			answer = []taskrun.Standing{}
		}
		if err := printJSON(stdout, answer); err != nil {
			return failed(stderr, err)
		}
	} else {
		printStandings(stdout, standings)
	}
	// A run that stands is a thing a person has to do something about, and the code of
	// the command says so, whatever it managed to write under the task.
	for _, one := range standings {
		if one.Stalled {
			return exitFailure
		}
	}
	return exitOK
}

// stallEnv is the machine the runs of the project are asked about: the clock, the
// question whether a run is still going, the silence the project puts up with, and the
// time limit of a run of it.
func stallEnv(cfg config.Config) (taskrun.ListEnv, error) {
	env := taskrun.ListEnv{Now: taskClock, Running: taskMachine.Alive}
	silence, err := stallSilence(cfg)
	if err != nil {
		return env, err
	}
	env.StallAfter = silence
	if env.Timeout, err = runLimit(cfg); err != nil {
		return env, err
	}
	return env, nil
}

// recordUnderTheTask is what leaves the record of a run under its task on the host of
// the project, and what a project of no host answers: a run that stands is reported to
// the person who asked and written about in no tracker, and the answer says so rather
// than letting a schedule believe that a record is there (docs/DESIGN.md §6, §7g).
//
// The roles of the project are built the first time there is something to leave under a
// task, and not before: a project whose host is not reachable, or a run that is working,
// is answered without a network at all.
func recordUnderTheTask(cfg config.Config, configPath string) taskrun.Say {
	var roles *forge.Set
	return func(ctx context.Context, one taskrun.Standing) (bool, string) {
		if roles == nil {
			set, err := taskRoles(cfg, roleEnv(configPath, secret.NewNotices(io.Discard)))
			if err != nil {
				return false, err.Error()
			}
			roles = &set
		}
		commenter, is := roles.Tracker.(forge.TaskCommenter)
		if !is {
			return false, taskrun.NoRecord
		}
		if err := commenter.CommentTask(ctx, one.Task, taskrun.Record(one)); err != nil {
			return false, err.Error()
		}
		return true, ""
	}
}

// printStandings is the answer of a watch for a person: what stands, for how long, what
// it was doing when it last said something, and whether the record under the task was
// left this time — and where nothing could be written, what the host said.
func printStandings(w io.Writer, standings []taskrun.Standing) {
	for _, one := range standings {
		if one.Stalled {
			fmt.Fprintf(w, "task %d (%s): standing for %s, the last step: %s\n",
				one.Task, one.Run, taskrun.Idle(one.Silence()), one.LastStep)
		} else {
			fmt.Fprintf(w, "task %d (%s): no longer standing, the run is %s\n", one.Task, one.Run, one.Outcome)
		}
		if one.Reason != "" {
			fmt.Fprintf(w, "  standing at: %s\n", one.Reason)
		}
		switch {
		case one.Said:
			fmt.Fprintf(w, "  left under the task\n")
		case one.Problem != "":
			fmt.Fprintf(w, "  not left under the task: %s\n", one.Problem)
		}
	}
}

// listOfRuns is the list the call asked for: the runs of the project of the folder, or
// the runs of every project on the machine. The queue of attention comes with the list of
// one project, worked out of the state of its tasks and of nothing else: a list reads no
// tracker, no host and no network, and the entries of it are told the state of the process
// of their own task, which is what the state of the task is enough for (docs/DESIGN.md §6,
// §6a).
func listOfRuns(ctx context.Context, configPath, repo string, all bool, env taskrun.ListEnv) (taskrun.Runs, error) {
	home, err := whereCrewflowKeeps()
	if err != nil {
		return taskrun.Runs{}, err
	}
	if !all {
		cfg, err := config.Load(projectFile(configPath, repo))
		if err != nil {
			return taskrun.Runs{}, err
		}
		if env.Timeout, err = runLimit(cfg); err != nil {
			return taskrun.Runs{}, err
		}
		if env.StallAfter, err = stallSilence(cfg); err != nil {
			return taskrun.Runs{}, err
		}
		runs, err := taskrun.List(home, cfg.RepoName(), env)
		if err != nil {
			return taskrun.Runs{}, err
		}
		// The state of a run says nothing of the branch the tasks of the project are
		// counted from: the file of the project says that, and it is this that read
		// it, so the header of the list can say which branch the work is on its way
		// to.
		runs.Branch = cfg.Project.DefaultBranch
		attention, err := attentionEnv(cfg)
		if err != nil {
			return taskrun.Runs{}, err
		}
		attention.ListEnv = env
		// A list of the whole machine has no queue at the top of it: every project of
		// the machine has thresholds of its own in its own file, and a folder of no
		// project at all says nothing about any of them (§6).
		queue, err := taskrun.AttentionQueue(ctx, home, cfg.RepoName(), attention, nil)
		if err != nil {
			return taskrun.Runs{}, err
		}
		return runs.WithAttention(queue), nil
	}
	// The file of the project of the folder, where there is one, is read for two things
	// only — how long a run of it may go on and how long it may stand still — and a
	// folder of no project says nothing about either. A list of the whole machine is
	// therefore given no limit at all, and says a run it cannot put an end to for what it
	// is: a run that may be going, and a run of a project that named no silence is never
	// marked as standing whatever it does (docs/DESIGN.md §6, §7).
	if cfg, err := config.Load(projectFile(configPath, "")); err == nil {
		if env.Timeout, err = runLimit(cfg); err != nil {
			return taskrun.Runs{}, err
		}
		if env.StallAfter, err = stallSilence(cfg); err != nil {
			return taskrun.Runs{}, err
		}
	}
	return taskrun.EveryProject(home, env)
}

// runLimit is how long a run of this project may take, which is what says that a run
// crewflow kept no process of is over.
func runLimit(cfg config.Config) (time.Duration, error) {
	limit, err := time.ParseDuration(cfg.Executor.Timeout)
	if err != nil {
		return 0, fmt.Errorf("executor.timeout: %w", err)
	}
	return limit, nil
}

// stallSilence is how long a run of this project may show nothing before a list of runs
// calls it standing: the silence the project agreed to put up with, and the file of the
// project is where it is written (docs/DESIGN.md §6).
func stallSilence(cfg config.Config) (time.Duration, error) {
	silence, err := time.ParseDuration(cfg.Executor.StallAfter)
	if err != nil {
		return 0, fmt.Errorf("executor.stall_after: %w", err)
	}
	return silence, nil
}

// whereCrewflowKeeps is the root of what crewflow keeps of its own on this machine: the
// journal of every run and the state of every task, the same for every project whatever
// folder a command was called in.
func whereCrewflowKeeps() (string, error) {
	return config.Config{}.ExpandPath(crewflowHome)
}

// projectFile is the file of the project whose runs are to be shown: the one that was
// named, the one in the checkout that was named, or the one in the folder crewflow was
// called in. A checkout without a file of its own is a folder of a project crewflow
// knows nothing about, and saying so is better than an empty list that looks like a
// project nothing was ever run in.
func projectFile(configPath, repo string) string {
	switch {
	case configPath != "":
		return configPath
	case repo != "":
		return filepath.Join(repo, "crewflow.toml")
	default:
		return defaultConfigPath
	}
}

// checkoutAt is a clear answer when there is no checkout at the path a person named: a
// file where a folder was named, or nothing at all, is a wrong call of the caller and
// not a project without runs.
func checkoutAt(repo string) error {
	info, err := os.Stat(repo)
	switch {
	case err != nil:
		return err
	case !info.IsDir():
		return errors.New("that is a file, want the folder of a checkout of the project")
	default:
		return nil
	}
}

// taskFlags are the flags of a subcommand of the task, and the usage that goes with
// them: a subcommand called wrong is a wrong call, and the usage says what the right
// one is.
func taskFlags(subcommand string, stderr io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet("task "+subcommand, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { usage(stderr) }
	return flags
}

// takeNumber takes the number of the task out of the arguments of a subcommand and
// parses the rest as its flags: a person writes the number first and the flag package
// of Go stops at the first word that is not a flag, so both orders have to mean the
// same thing. The code it returns is the one of a call that could not be read.
func takeNumber(subcommand string, args []string, flags *flag.FlagSet, stderr io.Writer) (string, int) {
	number, rest := "", args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		number, rest = args[0], args[1:]
	}
	if err := flags.Parse(rest); err != nil {
		return "", exitUsage
	}
	switch {
	case number == "" && flags.NArg() == 1:
		number = flags.Arg(0)
	case number != "" && flags.NArg() > 0:
		fmt.Fprintf(stderr, "crewflow task %s: unexpected argument %q\n\n", subcommand, flags.Arg(0))
		usage(stderr)
		return "", exitUsage
	}
	return number, exitOK
}

// taskNumber is the number of the task a subcommand is about, which is what the
// tracker of the project numbers its tasks by, and whether the call named one at all.
// A word that is not a number is a mistake of the call and not a task that does not
// exist, and it is said here where the usage is.
func taskNumber(stderr io.Writer, subcommand, number string) (int, bool) {
	if number == "" {
		fmt.Fprintf(stderr, "crewflow task %s: which task? a number, as in `crewflow task %s 43`\n\n", subcommand, subcommand)
		usage(stderr)
		return 0, false
	}
	task, err := strconv.Atoi(number)
	if err != nil || task < 1 {
		fmt.Fprintf(stderr, "crewflow task %s: %q is not the number of a task\n\n", subcommand, number)
		usage(stderr)
		return 0, false
	}
	return task, true
}

// roleEnv is the machine the roles of a project are built on: the programs of PATH, the
// way they are started, the file a hint names — and, for a project whose executor works
// as an account of the host, the store of the machine where its key is, the HTTP to the
// host and the clock. The store of the secrets is behind the wait of the keychain of
// macOS and says what it is about to wait for to the notices of the command
// (docs/DESIGN.md §7i).
func roleEnv(configPath string, notices *secret.Notices) forge.Env {
	return forge.Env{
		LookPath:   exec.LookPath,
		Run:        runRole,
		ConfigPath: configPath,
		Secrets:    storeOfSecrets(notices),
		HTTP:       httpOfMachine,
		Now:        time.Now,
	}
}

// runRole starts a program for a role of the project: the tracker, the host or the
// CI, which is told which host to talk to in the environment of the process and
// finds its program in PATH as a person would.
func runRole(ctx context.Context, name string, args []string, dir string, extraEnv []string) ([]byte, []byte, int, error) {
	return doctor.Command(ctx, name, args, dir, extraEnv)
}

// stoppedBy is the context of a command that has to stop what it is doing when a
// person stops it. The run of a task stops the executor with it, and a watch of a run
// stops watching, and neither of them leaves a program working on without anyone
// (docs/DESIGN.md §7a).
func stoppedBy() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// printResult is the outcome of a run for a person: what came of it, where the
// change request is, what the run was refused or changed that it was not to change,
// and where the journal of the run is. It is in English whatever the language of the
// project: the languages of the reports are a task of their own, and a report in an
// unknown language is a report nobody reads.
func printResult(w io.Writer, result taskrun.Result) {
	// Whose name the run went under is the first line of the report: it is the first
	// question a person has of a run and the last thing anybody reads, because a
	// report that says the outcome and not the mode is half a report (docs/DESIGN.md §7i).
	if result.Identity.Description != "" {
		fmt.Fprintf(w, "executor: %s\n", result.Identity.Description)
	}
	fmt.Fprintf(w, "task %d: %s, attempt %d\n", result.Task, result.Outcome, result.Attempt)
	if result.Continued {
		fmt.Fprintf(w, "  went on in the session %s of the last run\n", result.Session)
	}
	// A run that went on by itself is said in the report, because the orchestrator
	// reads whether it has to continue the task by hand, and a run nobody asked for
	// has to be visible as one (docs/DESIGN.md §7a).
	if result.AutoResumed != "" {
		fmt.Fprintf(w, "  crewflow went on by itself after a refusal for %s\n", result.AutoResumed)
	}
	if result.Outcome == taskrun.Interrupted {
		fmt.Fprintf(w, "  the run was stopped and is not over: go on in it with "+
			"`crewflow task run %d -continue \"what is left to do\"`\n", result.Task)
	}
	if change := result.ChangeRequest; change != nil {
		fmt.Fprintf(w, "  #%d %s (head %s)\n", change.Number, change.URL, change.HeadSHA)
	}
	for _, refusal := range result.Rejections {
		fmt.Fprintf(w, "  refused: %s\n", refusal)
	}
	if result.Reason != "" {
		// The reason of a run is why the run stopped, whatever stopped it: the words of
		// the executor that stopped itself, or the keychain of macOS that was asked to
		// let this build of crewflow in and did not (docs/DESIGN.md §7i).
		fmt.Fprintf(w, "  the run stopped: %s\n", result.Reason)
	}
	for _, outside := range result.Outside {
		fmt.Fprintf(w, "  outside the boundaries of the task: %s\n", outside)
	}
	if result.ExitCode != 0 {
		fmt.Fprintf(w, "  the executor exited with %d\n", result.ExitCode)
	}
	fmt.Fprintf(w, "  branch %s\n", result.Branch)
	fmt.Fprintf(w, "  worktree %s\n", result.Worktree)
	fmt.Fprintf(w, "  journal %s\n", result.Journal)
}

// printReadiness is the answer of a check for a person: whether the task may be run,
// and, when it may not, every thing it is missing, one per line, because a person
// goes and writes them.
func printReadiness(w io.Writer, readiness task.Readiness) {
	if readiness.Ready {
		fmt.Fprintf(w, "task %d: ready, %s\n", readiness.Number, readiness.Title)
		return
	}
	fmt.Fprintf(w, "task %d: not ready, %s\n", readiness.Number, readiness.Title)
	for _, missing := range readiness.Missing {
		fmt.Fprintf(w, "  %s\n", missing)
	}
}

// printWatch is the line a watch opens with: which attempt of which task is being
// watched, how far it has come and where its journal is. It is printed once, and
// everything after it is the journal itself.
func printWatch(w io.Writer, watch *taskrun.Watcher) {
	// The mode of the run is said before the attempt, because a person who opens a
	// second terminal to watch a run has to see whose run they are watching before
	// they read a word of it (docs/DESIGN.md §7i).
	if watch.Identity.Description != "" {
		fmt.Fprintf(w, "executor: %s\n", watch.Identity.Description)
	}
	going := ""
	if watch.Running() {
		going = ", the run is going"
	}
	when := watch.StartedAt.Format(time.RFC3339)
	if !watch.EndedAt.IsZero() {
		when += " to " + watch.EndedAt.Format(time.RFC3339)
	}
	fmt.Fprintf(w, "task %d, attempt %d of %q (%s%s, %s), journal %s\n",
		watch.Task, watch.Attempt, watch.Title, watch.Outcome, going, when, watch.Journal)
}

// printJSON is the answer of a command for the orchestrator, in the shape it reads.
// The report is read by a person too, when something in it surprises them and they
// ask for it again with -json.
func printJSON(w io.Writer, answer any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(answer); err != nil {
		return fmt.Errorf("print the answer: %w", err)
	}
	return nil
}

// failed is what a command says when it could not do what it was told: the reason,
// and the code that says it did not.
func failed(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "crewflow task: %v\n", err)
	return exitFailure
}
