package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/doctor"
	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/forge/roles"
	taskrun "github.com/naghuale/crewflow/internal/run"
)

// crewflowHome is the root of what crewflow keeps of its own: the journal of every
// run and the state of every task, outside the projects themselves
// (docs/DESIGN.md §7).
const crewflowHome = "~/.crewflow"

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
	flags := flag.NewFlagSet("task run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { usage(stderr) }
	configPath := flags.String("config", defaultConfigPath, "path to crewflow.toml")
	repoDir := flags.String("repo", "", "the repository to make the worktree out of, the folder crewflow was called in when empty")
	continueMessage := flags.String("continue", "", "go on with this message, in the same worktree and the same session")
	asJSON := flags.Bool("json", false, "print the outcome as JSON, for the orchestrator")
	// The number of the task is taken out of the arguments before the flags are
	// parsed, because a person writes it first and Go's flag package stops at the
	// first word that is not a flag: both orders then mean the same thing.
	number, rest := "", args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		number, rest = args[0], args[1:]
	}
	if err := flags.Parse(rest); err != nil {
		return exitUsage
	}
	switch {
	case number == "" && flags.NArg() == 1:
		number = flags.Arg(0)
	case number != "" && flags.NArg() > 0:
		fmt.Fprintf(stderr, "crewflow task run: unexpected argument %q\n\n", flags.Arg(0))
		usage(stderr)
		return exitUsage
	}
	task, named := taskNumber(stderr, number)
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

	result, err := taskrun.Run(context.Background(), taskRunEnv(home), cfg, set, taskrun.Request{
		Number:   task,
		RepoDir:  *repoDir,
		Continue: *continueMessage,
	})
	if err != nil {
		failed(stderr, err)
	}
	if *asJSON {
		if printErr := printResultJSON(stdout, result); printErr != nil {
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

// taskNumber is the number of the task to run, which is what the tracker of the
// project numbers its tasks by, and whether the call named one at all. A word that
// is not a number is a mistake of the call and not a task that does not exist, and
// it is said here where the usage is.
func taskNumber(stderr io.Writer, number string) (int, bool) {
	if number == "" {
		fmt.Fprintf(stderr, "crewflow task run: which task? a number, as in `crewflow task run 43`\n\n")
		usage(stderr)
		return 0, false
	}
	task, err := strconv.Atoi(number)
	if err != nil || task < 1 {
		fmt.Fprintf(stderr, "crewflow task run: %q is not the number of a task\n\n", number)
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

// printResult is the outcome of a run for a person: what came of it, where the
// change request is, what the run was refused or changed that it was not to change,
// and where the journal of the run is. It is in English whatever the language of
// the project: the languages of the reports are a task of their own, and a report
// in an unknown language is a report nobody reads.
func printResult(w io.Writer, result taskrun.Result) {
	fmt.Fprintf(w, "task %d: %s, attempt %d\n", result.Task, result.Outcome, result.Attempt)
	if result.Continued {
		fmt.Fprintf(w, "  went on in the session %s of the last run\n", result.Session)
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

// printResultJSON is the outcome for the orchestrator, in the shape it reads. The
// report is read by a person too, when something in it surprises them and they ask
// for it again with -json.
func printResultJSON(w io.Writer, result taskrun.Result) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return fmt.Errorf("print the outcome: %w", err)
	}
	return nil
}

// failed is what a command says when it could not do what it was told: the reason,
// and the code that says it did not.
func failed(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "crewflow task run: %v\n", err)
	return exitFailure
}
