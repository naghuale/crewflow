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
	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/forge/roles"
	"github.com/naghuale/crewflow/internal/gate"
	taskrun "github.com/naghuale/crewflow/internal/run"
	"github.com/naghuale/crewflow/internal/secret"
	"github.com/naghuale/crewflow/internal/task"
)

// The machine a review works on: the roles of a project as its orchestrator and the way
// git is started. They are variables so that a test of the command runs against a host
// and a repository of its own and reaches neither the network nor a repository of the
// person who runs it (docs/DESIGN.md §7h).

// runReview is `crewflow review <PR>`: it gathers the facts of a change from the host
// and from git, hands them to the gate, and says whether the change may be merged —
// and, when it may not, the one reason why (docs/DESIGN.md §6, §7h).
//
// Nothing about a change is stored and nothing about it is decided for ever: the
// answer is worked out in the moment it is asked for, out of the host and out of git,
// so that it cannot go out of date and cannot disagree with GitHub. With -approve or
// -request-changes the command writes the record of a review under the change, as the
// orchestrator of the project: a record an executor could write is a record the gate
// would have to refuse (§7h, §7i).
func runReview(out *secret.Out, args []string, stdout, stderr *secret.Writer) int {
	flags := reviewFlags(stderr)
	configPath := flags.String("config", defaultConfigPath, "path to crewflow.toml")
	repoDir := flags.String("repo", "", "the checkout to read the history of the change in, the folder crewflow was called in when empty")
	approve := flags.Bool("approve", false, "write a record of an approval of the head of the change")
	requestChanges := flags.String("request-changes", "", "write a record asking for changes, with the findings in this file")
	asJSON := flags.Bool("json", false, "print the answer as JSON, for the orchestrator")
	change, code := takeChange("review", args, flags, stderr)
	if code != exitOK {
		return code
	}
	if *approve && *requestChanges != "" {
		fmt.Fprintf(stderr, "crewflow review: -approve and -request-changes are two decisions, not one\n\n")
		usage(stderr)
		return exitUsage
	}

	cfg, err := loadFor(*configPath)
	if err != nil {
		return reviewFailed(stderr, err)
	}
	home, err := cfg.ExpandPath(crewflowHome)
	if err != nil {
		return reviewFailed(stderr, err)
	}
	// A review is the business of the orchestrator, and the orchestrator is the account
	// the project gives it: the record of a review counts only because a reviewer of the
	// project wrote it, and the rules of a branch are rights a person has — and, in the
	// mode of a separate login, rights the App of the orchestrator has (§7h, §7i).
	set, err := reviewRoles(cfg, roleEnv(*configPath, secret.NewNotices(stderr), out))
	if err != nil {
		return reviewFailed(stderr, err)
	}

	ctx, stop := stoppedBy()
	defer stop()
	facts, err := gatherReview(ctx, out, set, cfg, home, *repoDir, change, stderr)
	if err != nil {
		return reviewFailed(stderr, err)
	}
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
//
// The two lists of accounts come from the file of the project: the reviewers, whose
// record of a review is an approval, and the owners, whose records are the decision of a
// person — where the orchestrator works apart from the owner, the first of the two is
// its account and the second is not it (§5, §7h, §7i).
func gatherReview(ctx context.Context, out *secret.Out, set forge.Set, cfg config.Config, home, repoDir string, change int, stderr io.Writer) (gate.Facts, error) {
	reviewers, err := reviewersOf(ctx, cfg, set)
	if err != nil {
		return gate.Facts{}, err
	}
	owners, err := ownersOf(ctx, cfg, set)
	if err != nil {
		return gate.Facts{}, err
	}
	number := taskOfChange(home, cfg, change)
	history, err := historyOf(ctx, out, cfg, set, repoDir, stderr)
	if err != nil {
		return gate.Facts{}, err
	}
	return gate.Collect(ctx, gate.Deps{
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
		Git:             history,
	}, change), nil
}

// historyOf is the checkout the history of a change is asked in: the folder crewflow
// was called in, or the one -repo named. A review is asked in a checkout of the
// project and creates nothing in it: a fetch of the objects of the head is a fetch
// like any other, and nothing else of the run writes there (§7h).
func historyOf(ctx context.Context, out *secret.Out, cfg config.Config, set forge.Set, repoDir string, stderr io.Writer) (gate.History, error) {
	environment, err := gitEnvironment(ctx, cfg, set, stderr)
	if err != nil {
		return gate.History{}, err
	}
	dir := repoDir
	if dir == "" {
		dir = "."
	}
	return gate.History{Dir: dir, Branch: cfg.Project.DefaultBranch, Run: gitOf(out, environment)}, nil
}

// taskFactsOf is what the gate asks the tracker about the task of a change: the paths
// it was to change and the words it is marked with, out of one reading of the task and
// its technical part (§7c, §7f).
func taskFactsOf(set forge.Set, cfg config.Config) func(context.Context, int) (gate.TaskFacts, error) {
	return func(ctx context.Context, number int) (gate.TaskFacts, error) {
		if set.Tracker == nil {
			return gate.TaskFacts{}, fmt.Errorf("tracker.kind: this project has no tracker of tasks, so the boundaries of task %d cannot be read", number)
		}
		found, err := set.Tracker.Task(ctx, number)
		if err != nil {
			return gate.TaskFacts{}, fmt.Errorf("read task %d: %w", number, err)
		}
		return gate.TaskFacts{
			Boundaries: task.Read(found.Body, cfg.Project.Language).Boundaries(),
			Labels:     found.Labels,
		}, nil
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
	account, err := accountOfReview(ctx, set, cfg)
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
	// The line of the mode goes out before the record is written and not after: a person
	// reading a terminal has to see which of the two accounts of the project the record
	// under the change was written in (docs/DESIGN.md §7h, §7i).
	if identity, err := orchestratorOf(ctx, set); err == nil {
		fmt.Fprint(stderr, saidOrchestrator(identity))
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

// accountOfReview is the subject a record of a review is written as, and whether it is
// one the gate counts: the record of a review is a comment, and it is an approval only
// because an account the project reviews with wrote it (docs.DESIGN.md §7h).
//
// It is a subject and not a login because that is what the gate will compare: the record
// is written as the account of the App of the orchestrator, and whether it counts is
// decided by the number of that App and not by the name of its account, which changes
// whenever the App is renamed (docs/DESIGN.md §7h, §7i).
func accountOfReview(ctx context.Context, set forge.Set, cfg config.Config) (forge.Subject, error) {
	account, err := forge.SigningAs(ctx, set.Forge)
	if err != nil {
		return forge.Subject{}, err
	}
	reviewers, err := reviewersOf(ctx, cfg, set)
	if err != nil {
		return forge.Subject{}, err
	}
	if !slices.ContainsFunc(reviewers, func(reviewer forge.Subject) bool { return account.Same(reviewer) }) {
		return forge.Subject{}, fmt.Errorf("a record of a review would be written as %s, which is not one of the reviewers of the project (%s): "+
			"the gate would not count it, and a record nobody counts is a comment",
			account.Named(), strings.Join(loginsOf(reviewers), ", "))
	}
	return account, nil
}

// withReview is the facts of a change with the record that was just written under it,
// as the account it was written in: the summary of the same command is then the
// answer about the change as it stands afterwards and not as it stood before
// (docs/DESIGN.md §7h).
func withReview(facts gate.Facts, account forge.Subject, body string) gate.Facts {
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

// takeChange takes the number of the change request out of the arguments of the command
// and parses the rest as its flags: a person writes the number first and the flag
// package of Go stops at the first word that is not a flag, so both orders have to mean
// the same thing. The command is named in every refusal, because a wrong call says which
// command it was a wrong call of.
func takeChange(command string, args []string, flags *flag.FlagSet, stderr io.Writer) (int, int) {
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
		fmt.Fprintf(stderr, "crewflow %s: unexpected argument %q\n\n", command, flags.Arg(0))
		usage(stderr)
		return 0, exitUsage
	}
	change, err := strconv.Atoi(number)
	if err != nil || change < 1 {
		fmt.Fprintf(stderr, "crewflow %s: which change? a number, as in `crewflow %s 7`\n\n", command, command)
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
