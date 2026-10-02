package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/forge/roles"
	"github.com/naghuale/crewflow/internal/gate"
	"github.com/naghuale/crewflow/internal/merge"
	taskrun "github.com/naghuale/crewflow/internal/run"
	"github.com/naghuale/crewflow/internal/secret"
)

// The machine a merge works on: the roles of a project as its orchestrator and the way
// git is started. They are variables in orchestrator.go, together with the ones of a
// review, because a review and a merge are made of the same machine (docs/DESIGN.md §7h).

// runMerge is `crewflow merge <PR>`: it asks the gate again, in the moment of the merge,
// whether the change may go in, and — only if it may — fast-forwards the default branch
// of the project to exactly the head the gate approved and reads the branch of the host
// afterwards to prove that it is there (docs/DESIGN.md §6, §7h).
//
// A change the gate refuses is not pushed at all and the reason of the refusal is the
// answer of the command: one reason of the table of §7h and what a person is to do
// about it. What the merge came out as is worked out from the branch of the host and
// not from the code `git push` exited with, and the whole of what git said is in the
// journal of the merge.
//
// The push is made as the orchestrator of the project: the login of the person where the
// orchestrator shares it with the owner, and the App of the project where it does not —
// and in the second case git takes the credentials of that push from the helper of
// crewflow, which signs a token of an hour for that one push and for nothing else
// (§7h, §7i).
func runMerge(args []string, stdout, stderr io.Writer) int {
	flags := mergeFlags("merge", stderr)
	configPath := flags.String("config", defaultConfigPath, "path to crewflow.toml")
	repoDir := flags.String("repo", "", "the checkout to push from, the folder crewflow was called in when empty")
	asJSON := flags.Bool("json", false, "print the outcome as JSON, for the orchestrator")
	change, code := takeChange("merge", args, flags, stderr)
	if code != exitOK {
		return code
	}

	ctx, stop := stoppedBy()
	defer stop()
	deps, _, err := depsOfChange(ctx, *configPath, *repoDir, change, stderr)
	if err != nil {
		return mergeFailed(stderr, err)
	}
	result, err := merge.Run(ctx, deps, change)
	if err != nil {
		return mergeFailed(stderr, err)
	}
	if *asJSON {
		if err := printJSON(stdout, result); err != nil {
			return mergeFailed(stderr, err)
		}
	} else {
		printMerge(stdout, result)
	}
	if !result.OK() {
		return exitFailure
	}
	return exitOK
}

// runVerify is `crewflow verify <PR>`: the check of a merge that has already been made.
// It asks the three questions that close the cycle of a task — is the branch of the host
// at the commit that was merged, is the task closed, and is the CI of that branch green
// — waits for the checks while they are going on, and writes the moment of the check
// into the state of the task (docs/DESIGN.md §6, §7h).
func runVerify(args []string, stdout, stderr io.Writer) int {
	flags := mergeFlags("verify", stderr)
	configPath := flags.String("config", defaultConfigPath, "path to crewflow.toml")
	repoDir := flags.String("repo", "", "the checkout the branch of the host is read in, the folder crewflow was called in when empty")
	asJSON := flags.Bool("json", false, "print the answer as JSON, for the orchestrator")
	change, code := takeChange("verify", args, flags, stderr)
	if code != exitOK {
		return code
	}

	ctx, stop := stoppedBy()
	defer stop()
	deps, cfg, err := depsOfChange(ctx, *configPath, *repoDir, change, stderr)
	if err != nil {
		return mergeFailed(stderr, err)
	}
	// How long the check waits for the checks of the branch is what the project says
	// about its CI: a merge and the check of it are two moments of one thing, and the
	// limit of CI is the limit the project gave it (docs/DESIGN.md §5, §7h).
	limit, err := time.ParseDuration(cfg.CI.Timeout)
	if err != nil {
		return mergeFailed(stderr, fmt.Errorf("ci.timeout: %w", err))
	}
	deps.Timeout = limit

	result, err := merge.Verify(ctx, deps, change)
	if err != nil {
		return mergeFailed(stderr, err)
	}
	if *asJSON {
		if err := printJSON(stdout, result); err != nil {
			return mergeFailed(stderr, err)
		}
	} else {
		printVerify(stdout, result)
	}
	if !result.Verified {
		return exitFailure
	}
	return exitOK
}

// depsOfChange is what a merge or a check of a change is made of: the roles of the
// project as its orchestrator, the checkout the branch of the host is read in and pushed
// from, the task the change is of and the state of that task. The task comes out of what
// crewflow kept of the runs of this project, and a change no run of it opened still says
// which task it is of (docs/DESIGN.md §7, §7h).
func depsOfChange(ctx context.Context, configPath, repoDir string, change int, stderr io.Writer) (merge.Deps, config.Config, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return merge.Deps{}, config.Config{}, err
	}
	home, err := cfg.ExpandPath(crewflowHome)
	if err != nil {
		return merge.Deps{}, config.Config{}, err
	}
	set, err := mergeRoles(cfg, roleEnv(configPath, secret.NewNotices(stderr)))
	if err != nil {
		return merge.Deps{}, config.Config{}, err
	}
	// Whose name the merge writes and pushes as is asked of the roles of the orchestrator
	// and goes into the report and the journal of the merge: a merge that was pushed as
	// the owner is a merge the person who reads the journal afterwards cannot tell from
	// one that was pushed as the orchestrator (docs/DESIGN.md §7h, §7i).
	orchestrator, err := orchestratorOf(ctx, set)
	if err != nil {
		return merge.Deps{}, config.Config{}, err
	}
	reviewers, err := reviewersOf(ctx, cfg, set)
	if err != nil {
		return merge.Deps{}, config.Config{}, err
	}
	owners, err := ownersOf(ctx, cfg, set)
	if err != nil {
		return merge.Deps{}, config.Config{}, err
	}
	number := taskOfChange(home, cfg, change)
	state := stateOfTaskAt(home, cfg, number)
	// The route of the project is worked out before anything is asked of the host: a
	// proxy that cannot be read is a refusal here and not an answer of somebody else's
	// machine dressed as the answer of this one (docs/DESIGN.md §7d).
	checkoutEnvironment, err := gitEnvironment(ctx, cfg, set, stderr)
	if err != nil {
		return merge.Deps{}, config.Config{}, err
	}
	return merge.Deps{
		Gate: gate.Deps{
			Forge:           set.Forge,
			CI:              set.CI,
			Repository:      cfg.Project.Repo,
			DefaultBranch:   cfg.Project.DefaultBranch,
			Executor:        roles.ExecutorOf(cfg),
			Reviewers:       reviewers,
			Owners:          owners,
			AcceptanceLabel: cfg.Acceptance.Label,
			Task:            number,
			TaskOf:          taskFactsOf(set, cfg),
			RequireChecks:   cfg.CI.Required,
		},
		Checkout: merge.Git{
			Dir:     checkoutOf(repoDir),
			Branch:  cfg.Project.DefaultBranch,
			HeadRef: headRefOf(set.Forge, change),
			Run:     gitOf(checkoutEnvironment),
		},
		Tracker:      set.Tracker,
		Task:         number,
		Worktree:     state.Worktree,
		Home:         home,
		Repo:         cfg.RepoName(),
		Orchestrator: merge.Orchestrator{Mode: orchestrator.Mode, Description: orchestrator.Description},
	}, cfg, nil
}

// headRefOf is the ref the host keeps the head of the change under, and an empty string
// where the host names none: a merge that has to know whether the commit of the head is
// in the branch already fetches that ref, and the gate refuses a change whose head
// cannot be fetched at all (docs/DESIGN.md §7h).
func headRefOf(host forge.Forge, change int) string {
	names, ok := host.(forge.HeadRef)
	if !ok {
		return ""
	}
	return names.HeadRef(change)
}

// stateOfTaskAt is what crewflow kept of the task of a change, and an empty state where
// there is none: a change of a task this machine never ran has no worktree to take away
// and no state to write the merged commit into, and a merge invents neither
// (docs/DESIGN.md §7).
func stateOfTaskAt(home string, cfg config.Config, task int) taskrun.State {
	if task <= 0 {
		return taskrun.State{}
	}
	state, err := taskrun.LoadState(taskrun.JournalsOf(home, cfg.RepoName()).StatePath(task))
	if err != nil {
		return taskrun.State{}
	}
	return state
}

// checkoutOf is the checkout a merge pushes from: the folder crewflow was called in, or
// the one -repo named. Nothing of a merge is written into it but what a fetch and a
// push always write (docs/DESIGN.md §7a).
func checkoutOf(repoDir string) string {
	if repoDir == "" {
		return "."
	}
	return repoDir
}

// printMerge is the outcome of a merge for a person: what the change is, where the branch
// of the host ended up, what became of the task behind it and what is left to do by
// hand — or, for a merge that did not happen, the one reason why and what to do about
// it. It is in English whatever the language of the project: the languages of the
// reports are a task of their own (docs/DESIGN.md §6).
func printMerge(w io.Writer, result merge.Result) {
	fmt.Fprintf(w, "change #%d", result.Change)
	if result.URL != "" {
		fmt.Fprintf(w, " %s", result.URL)
	}
	fmt.Fprintf(w, "\n")
	if result.Orchestrator.Description != "" {
		fmt.Fprintf(w, "  orchestrator: %s\n", result.Orchestrator.Description)
	}
	switch result.Outcome {
	case merge.Merged:
		fmt.Fprintf(w, "  merged: %s of the host is at %s\n", result.Branch, result.MergedSHA)
	case merge.AlreadyMerged:
		fmt.Fprintf(w, "  already merged: %s of the host holds %s, nothing was pushed\n",
			result.Branch, result.MergedSHA)
	case merge.MergedWithCleanupWarning:
		fmt.Fprintf(w, "  merged: %s of the host is at %s\n", result.Branch, result.MergedSHA)
		fmt.Fprintf(w, "  what came after the merge did not go through:\n")
		for _, left := range result.Left {
			fmt.Fprintf(w, "    %s\n", left)
		}
	default:
		fmt.Fprintf(w, "  may not be merged: %s\n  %s\n", result.Verdict.Reason, result.Verdict.Detail)
		fmt.Fprintf(w, "  nothing was pushed\n")
	}
	if result.Task > 0 {
		fmt.Fprintf(w, "  task #%d: %s\n", result.Task, closedBy(result))
	}
	fmt.Fprintf(w, "  journal %s\n", result.Journal)
}

// printVerify is the check of a merge for a person: the change, where the branch of the
// host is, the task of the change, the checks of that branch — and every one of the
// three that is not in order, because a check that says "not verified" and not why is a
// person looking at all of it by hand (docs/DESIGN.md §6).
func printVerify(w io.Writer, result merge.Verification) {
	fmt.Fprintf(w, "change #%d", result.Change)
	if result.URL != "" {
		fmt.Fprintf(w, " %s", result.URL)
	}
	fmt.Fprintf(w, "\n")
	if result.Verified {
		fmt.Fprintf(w, "  verified: %s of the host is at %s, the commit that was merged\n", result.Branch, result.Merged)
	} else {
		fmt.Fprintf(w, "  not verified:\n")
		for _, missing := range result.Missing {
			fmt.Fprintf(w, "    %s\n", missing)
		}
	}
	if result.Note != "" {
		fmt.Fprintf(w, "  %s\n", result.Note)
	}
	if result.Task > 0 {
		fmt.Fprintf(w, "  task #%d: %s\n", result.Task, closedOrOpen(result.TaskState == "closed"))
	}
	if result.CI != "" {
		fmt.Fprintf(w, "  checks of %s: %s\n", result.Branch, result.CI)
	}
	for _, left := range result.Left {
		fmt.Fprintf(w, "  not written down: %s\n", left)
	}
}

// closedBy is what a report of a merge says about the task of the change: the host
// closed it behind the change that went in, or crewflow closed it where the host left it
// open. A task nobody closed is said as not closed, and it is the one thing of a merge
// that is still to be done by hand (docs/DESIGN.md §6, §7h).
func closedBy(result merge.Result) string {
	switch result.TaskClosedBy {
	case merge.ClosedByHost:
		return "closed by host"
	case merge.ClosedByCrewflow:
		return "closed by crewflow"
	default:
		return "not closed yet"
	}
}

// closedOrOpen is how the check of a merge says that a task is closed, and how it says
// that it is not: the check reads the tracker and does not close anything, and a task it
// finds open is a task a person is to close.
func closedOrOpen(closed bool) string {
	if closed {
		return "closed"
	}
	return "not closed yet"
}

// mergeFlags are the flags of `crewflow merge` and of `crewflow verify`, and the usage
// that goes with them: a command called wrong is a wrong call, and the usage says what
// the right one is.
func mergeFlags(command string, stderr io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { usage(stderr) }
	return flags
}

// mergeFailed is what a merge says when it could not do what it was told: the reason,
// and the code that says it did not.
func mergeFailed(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "crewflow merge: %v\n", err)
	return exitFailure
}
