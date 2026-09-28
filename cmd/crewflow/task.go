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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/doctor"
	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/forge/roles"
	taskrun "github.com/naghuale/crewflow/internal/run"
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
	set, err := taskRoles(cfg, forge.Env{LookPath: exec.LookPath, Run: runRole, ConfigPath: *configPath})
	if err != nil {
		return failed(stderr, err)
	}

	// A run that a person stops stops the executor with it and says the run was
	// interrupted: an executor left to work on without anyone would go on writing
	// into a worktree nobody watches (docs/DESIGN.md §7a).
	ctx, stop := stoppedBy()
	defer stop()
	result, err := taskrun.Run(ctx, taskRunEnv(home), cfg, set, taskrun.Request{
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
	set, err := taskRoles(cfg, forge.Env{LookPath: exec.LookPath, Run: runRole, ConfigPath: *configPath})
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
	fmt.Fprintf(w, "task %d: %s, attempt %d\n", result.Task, result.Outcome, result.Attempt)
	if result.Continued {
		fmt.Fprintf(w, "  went on in the session %s of the last run\n", result.Session)
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
		fmt.Fprintf(w, "  the executor stopped: %s\n", result.Reason)
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
