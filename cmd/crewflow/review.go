package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/doctor"
	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/forge/roles"
	"github.com/naghuale/crewflow/internal/gate"
	taskrun "github.com/naghuale/crewflow/internal/run"
	"github.com/naghuale/crewflow/internal/task"
)

// The machine a review works on: the roles of a project and the way git is started.
// They are variables so that a test of the command runs against a host and a
// repository of its own and reaches neither the network nor a repository of the
// person who runs it (docs/DESIGN.md §7h).
var (
	reviewRoles = roles.AsOwner
	reviewGit   = runGit
)

// rolesAsOwner is how a review command gets the roles of a project, named so that a
// test can put the machine back the way it found it.
var rolesAsOwner = roles.AsOwner

// runReview is `crewflow review <PR>`: it gathers the facts of a change from the host
// and from git, hands them to the gate, and says whether the change may be merged —
// and, when it may not, the one reason why (docs/DESIGN.md §6, §7h).
//
// Nothing about a change is stored and nothing about it is decided for ever: the
// answer is worked out in the moment it is asked for, out of the host and out of git,
// so that it cannot go out of date and cannot disagree with GitHub. With -approve or
// -request-changes the command writes the record of a review under the change, as the
// person who runs crewflow: a record an executor could write is a record the gate
// would have to refuse (§7h, §7i).
func runReview(args []string, stdout, stderr io.Writer) int {
	flags := reviewFlags(stderr)
	configPath := flags.String("config", defaultConfigPath, "path to crewflow.toml")
	repoDir := flags.String("repo", "", "the checkout to read the history of the change in, the folder crewflow was called in when empty")
	approve := flags.Bool("approve", false, "write a record of an approval of the head of the change")
	requestChanges := flags.String("request-changes", "", "write a record asking for changes, with the findings in this file")
	asJSON := flags.Bool("json", false, "print the answer as JSON, for the orchestrator")
	change, code := takeChange(args, flags, stderr)
	if code != exitOK {
		return code
	}
	if *approve && *requestChanges != "" {
		fmt.Fprintf(stderr, "crewflow review: -approve and -request-changes are two decisions, not one\n\n")
		usage(stderr)
		return exitUsage
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return reviewFailed(stderr, err)
	}
	home, err := cfg.ExpandPath(crewflowHome)
	if err != nil {
		return reviewFailed(stderr, err)
	}
	// A review is the business of the orchestrator, and the orchestrator is the
	// person: the record of a review counts only because a reviewer of the project
	// wrote it, and the rules of a branch are rights a person has and the App of an
	// executor does not (§7h, §7i).
	set, err := reviewRoles(cfg, roleEnv(*configPath))
	if err != nil {
		return reviewFailed(stderr, err)
	}

	ctx, stop := stoppedBy()
	defer stop()
	facts := gatherReview(ctx, set, cfg, home, *repoDir, change)
	if *approve || *requestChanges != "" {
		if code := writeReviewRecord(ctx, set, cfg, &facts, change, *approve, *requestChanges, stderr); code != exitOK {
			return code
		}
	}
	summary := gate.Summarize(facts, gate.Evaluate(facts))
	if *asJSON {
		if err := printJSON(stdout, summary); err != nil {
			return reviewFailed(stderr, err)
		}
	} else {
		summary.Write(stdout)
	}
	if summary.Verdict.Ready {
		return exitOK
	}
	return exitFailure
}

// gatherReview is the facts of a change, gathered by the gate out of the roles of the
// project and out of a checkout of the repository. The task of the change and the
// paths it was to change come from what crewflow kept of a run of it and from the
// tracker, and everything else from the host and from git (docs/DESIGN.md §7h).
func gatherReview(ctx context.Context, set forge.Set, cfg config.Config, home, repoDir string, change int) gate.Facts {
	number := taskOfChange(home, cfg, change)
	return gate.Collect(ctx, gate.Deps{
		Forge:         set.Forge,
		CI:            set.CI,
		Repository:    cfg.Project.Repo,
		DefaultBranch: cfg.Project.DefaultBranch,
		Reviewers:     cfg.Merge.Reviewers,
		Task:          number,
		BoundariesOf:  boundariesOf(set, cfg),
		RequireChecks: cfg.CI.Required,
		Git:           historyOf(cfg, repoDir),
	}, change)
}

// historyOf is the checkout the history of a change is asked in: the folder crewflow
// was called in, or the one -repo named. A review is asked in a checkout of the
// project and creates nothing in it: a fetch of the objects of the head is a fetch
// like any other, and nothing else of the run writes there (§7h).
func historyOf(cfg config.Config, repoDir string) gate.History {
	dir := repoDir
	if dir == "" {
		dir = "."
	}
	return gate.History{Dir: dir, Branch: cfg.Project.DefaultBranch, Run: reviewGit}
}

// runGit starts git the way doctor starts every program of a machine: with a closed
// stdin and the environment of the process, in the folder it is told (§7a).
func runGit(ctx context.Context, name string, args []string, dir string) ([]byte, []byte, int, error) {
	return doctor.Command(ctx, name, args, dir, nil)
}

// boundariesOf is where the paths a change was to change come from: the task of the
// change, as the tracker holds it, and the technical part of it (§7c, §7f).
func boundariesOf(set forge.Set, cfg config.Config) func(context.Context, int) ([]string, error) {
	return func(ctx context.Context, number int) ([]string, error) {
		if set.Tracker == nil {
			return nil, fmt.Errorf("tracker.kind: this project has no tracker of tasks, so the boundaries of task %d cannot be read", number)
		}
		found, err := set.Tracker.Task(ctx, number)
		if err != nil {
			return nil, fmt.Errorf("read task %d: %w", number, err)
		}
		return task.Read(found.Body, cfg.Project.Language).Boundaries(), nil
	}
}

// taskOfChange is the task a change request is of, out of what crewflow kept of the
// runs of this project: a run that opened a request recorded both, and the state is
// read without a network and without the host (§7).
//
// A change that no run of this machine opened has no task crewflow knows, and its
// boundaries are then nobody's to say: the gate refuses it as `out-of-scope`, because
// a change nobody can place is a change nobody may merge (§7h).
func taskOfChange(home string, cfg config.Config, change int) int {
	limit, err := runLimit(cfg)
	if err != nil {
		return 0
	}
	runs, err := taskrun.List(home, cfg.RepoName(), taskrun.ListEnv{
		Now:     taskClock,
		Running: taskMachine.Alive,
		Timeout: limit,
	})
	if err != nil {
		return 0
	}
	for _, entry := range runs.Entries {
		if entry.Change != nil && entry.Change.Number == change {
			return entry.Task
		}
	}
	return 0
}

// writeReviewRecord writes the record of a review under the change: an approval, or a
// request for changes with the findings a person wrote about them. The facts of the
// change are told what was written, so that the summary of the same command is the
// answer about the change as it is afterwards (docs/DESIGN.md §7h).
//
// The record is written only when everything but the approval is in order. An
// approval of a change whose CI is red, or whose files went out of the boundaries of
// its task, is a name on a comment and not a gate — and a gate that approves such a
// change has stopped being one.
func writeReviewRecord(ctx context.Context, set forge.Set, cfg config.Config, facts *gate.Facts, change int,
	approve bool, findingsFile string, stderr io.Writer) int {
	apart := gate.Apart(*facts)
	if !apart.Ready {
		fmt.Fprintf(stderr, "crewflow review: may not write a record of a review of #%d: %s\n  %s\n\n",
			change, apart.Reason, apart.Detail)
		return exitFailure
	}
	account, err := accountOfReview(set, cfg)
	if err != nil {
		return reviewFailed(stderr, err)
	}
	body, err := reviewRecord(approve, findingsFile, *facts)
	if err != nil {
		return reviewFailed(stderr, err)
	}
	writer, ok := set.Forge.(forge.CommentWriter)
	if !ok {
		return reviewFailed(stderr, fmt.Errorf("the host of the project has nowhere to write a record of a review to"))
	}
	if err := writer.WriteComment(ctx, change, body); err != nil {
		return reviewFailed(stderr, fmt.Errorf("write the record of the review under #%d: %w", change, err))
	}
	*facts = withReview(*facts, account, body)
	if approve {
		fmt.Fprintf(stderr, "crewflow review: %s approved %s under #%d\n", account, facts.Head, change)
	} else {
		fmt.Fprintf(stderr, "crewflow review: %s asked for changes under #%d, and the run goes on with `crewflow task run %d -continue …`\n",
			account, change, facts.Task)
	}
	return exitOK
}

// accountOfReview is the account a record of a review is written in, and whether it is
// one the gate counts: the record of a review is a comment, and it is an approval
// only because a reviewer of the project wrote it (docs/DESIGN.md §7h).
func accountOfReview(set forge.Set, cfg config.Config) (string, error) {
	signed, ok := set.Forge.(forge.SignedIn)
	if !ok {
		return "", fmt.Errorf("the host of the project does not say which account it speaks as, " +
			"so a record of a review would be written in the name of nobody")
	}
	account, err := signed.SignedIn(context.Background())
	if err != nil {
		return "", err
	}
	reviewers := cfg.Merge.Reviewers
	if len(reviewers) == 0 {
		// The default of §5: a project that names no reviewers has the owner of its
		// repository, and nobody else.
		if owner, _, of := strings.Cut(cfg.Project.Repo, "/"); of {
			reviewers = []string{owner}
		}
	}
	if !slices.Contains(reviewers, account) {
		return "", fmt.Errorf("a record of a review would be written as %s, which is not one of the reviewers of the project (%s): "+
			"the gate would not count it, and a record nobody counts is a comment", account, strings.Join(reviewers, ", "))
	}
	return account, nil
}

// withReview is the facts of a change with the record that was just written under it,
// as the account it was written in: the summary of the same command is then the
// answer about the change as it stands afterwards and not as it stood before
// (docs/DESIGN.md §7h).
func withReview(facts gate.Facts, account, body string) gate.Facts {
	facts.Reviews = append(facts.Reviews, gate.Review{
		Author:    account,
		CreatedAt: taskClock(),
		Body:      body,
	})
	return facts
}

// reviewRecord is the record of a review in the format of §7h: the decision as a
// person reads it, whatever the person wrote under it, and the block the programs read.
func reviewRecord(approve bool, findingsFile string, facts gate.Facts) (string, error) {
	if approve {
		return gate.ApproveOf(facts.Head, facts.Task), nil
	}
	findings, err := os.ReadFile(findingsFile)
	if err != nil {
		return "", fmt.Errorf("read the findings: %w", err)
	}
	if len(strings.TrimSpace(string(findings))) == 0 {
		return "", fmt.Errorf("the findings in %s are empty: a request for changes says what to change", findingsFile)
	}
	return gate.ChangesOf(facts.Head, facts.Task, string(findings)), nil
}

// reviewFlags are the flags of `crewflow review`, and the usage that goes with them:
// a command called wrong is a wrong call, and the usage says what the right one is.
func reviewFlags(stderr io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet("review", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { usage(stderr) }
	return flags
}

// takeChange takes the number of the change request out of the arguments and parses
// the rest as its flags: a person writes the number first and the flag package of Go
// stops at the first word that is not a flag, so both orders have to mean the same
// thing.
func takeChange(args []string, flags *flag.FlagSet, stderr io.Writer) (int, int) {
	number, rest := "", args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		number, rest = args[0], args[1:]
	}
	if err := flags.Parse(rest); err != nil {
		return 0, exitUsage
	}
	switch {
	case number == "" && flags.NArg() == 1:
		number = flags.Arg(0)
	case number != "" && flags.NArg() > 0:
		fmt.Fprintf(stderr, "crewflow review: unexpected argument %q\n\n", flags.Arg(0))
		usage(stderr)
		return 0, exitUsage
	}
	change, err := strconv.Atoi(number)
	if err != nil || change < 1 {
		fmt.Fprintf(stderr, "crewflow review: which change? a number, as in `crewflow review 7`\n\n")
		usage(stderr)
		return 0, exitUsage
	}
	return change, exitOK
}

// reviewFailed is what a review says when it could not do what it was told: the
// reason, and the code that says it did not.
func reviewFailed(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "crewflow review: %v\n", err)
	return exitFailure
}
