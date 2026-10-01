// Package forge is what crewflow knows about the systems a task comes from and
// goes back to: the host of the code and its review, the tracker of the tasks
// and the CI of a commit. They are three roles and not one, because the tasks
// may live in Jira, the code in GitLab and the checks in Jenkins, and that
// combination does not assemble out of one adapter (docs/DESIGN.md §7g).
//
// The core speaks only the words of this package — task, change request, head,
// comment, check state — and each system is an adapter of it. There is no "gh"
// and no "PR" here, so that GitLab and Bitbucket are not a rewrite of the core
// but one more adapter.
package forge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/naghuale/crewflow/internal/secret"
)

// Task is a task as a person wrote it: what is to be done and how one checks
// that it is done (docs/DESIGN.md §7f).
type Task struct {
	// Number is what the tracker of the project numbers its tasks by.
	Number int
	// Title is the one line a person wrote.
	Title string
	// Body is the task itself.
	Body string
	// Labels are the words the tracker marks a task with, "risky" and
	// "approved" among them (docs/DESIGN.md §7f).
	Labels []string
	// State is "open" or "closed": a closed task is already done.
	State string
	// URL is where a person reads the task.
	URL string
}

// ErrNoSuchTask is the answer of a tracker about a task it does not have: a fact about
// the project and not a failure of the machine. A repository that moved and took the name
// of another one answers it too — a task of yesterday is not a task of today, and the run
// journal of it is still in ~/.crewflow/runs — and a caller has to tell the two apart
// (docs/DESIGN.md §6a, §7g).
//
// A tracker that could not be reached, that failed or that was refused is not this error:
// there crewflow does not know, and does not say (§7h).
var ErrNoSuchTask = errors.New("no such task")

// NoSuchTask is whether an error of a tracker is its answer about a task it does not have,
// and a caller that has to act on the difference asks it here instead of reading the words
// of an error (docs.DESIGN.md §6a).
func NoSuchTask(err error) bool { return errors.Is(err, ErrNoSuchTask) }

// Tracker is where the tasks of a project come from. Crewflow reads a task and
// never opens one: a run that opens a task is a change outside the cycle, and
// those are not crewflow's (§7e). Closing is the other matter: a tracker is
// where a task is closed, and §7g says so of the role.
type Tracker interface {
	RoleChecker
	// Task returns the task with the number, and an error when there is no
	// such task or the tracker could not be read.
	Task(ctx context.Context, number int) (Task, error)
}

// TaskCloser is the tracker of a project that can close a task of it, which is
// the third thing §7g asks of the role. A host closes an issue behind a change
// that has gone in, and it does not always: a change that went in with its task
// still open is a cycle nobody finished, and a check after the merge answers
// «not verified» about a merge that did go in — which is how tasks of a project
// come to be closed by hand, one at a time, beside crewflow (docs/DESIGN.md
// §6, §7g, §7h).
type TaskCloser interface {
	// CloseTask closes the task with the number and leaves the comment under
	// it: a task crewflow closed is one a person will not look at again, and
	// the reason it is closed has to be where they read it (docs/DESIGN.md
	// §7h).
	CloseTask(ctx context.Context, number int, comment string) error
}

// TaskCommenter is the tracker of a project a line can be left under a task of it
// without closing the task. It is what crewflow writes under a task about a run that
// stands: the run goes on and a person has to learn that it is going nowhere, and the
// task is the one place an orchestrator and a person both look at (docs/DESIGN.md §6).
//
// It is not [TaskCloser] and it is not [CommentWriter]: closing a task is the end of a
// cycle, and a record of a review is a record of a decision, while this is a line about
// the state of a run that is still going.
type TaskCommenter interface {
	// CommentTask leaves the body under the task with the number, and the task stays
	// open: a task nobody is working on yet is not a task that is done.
	CommentTask(ctx context.Context, number int, body string) error
}

// ChangeRequest is a request to change the code of a project: what it wants,
// what it stands on and where it stands. A pull request on one host and a merge
// request on another are the same thing here (docs/DESIGN.md §4).
type ChangeRequest struct {
	// Number is what the host numbers its requests by.
	Number int
	// URL is where a person reads it.
	URL string
	// HeadBranch is the branch with the work.
	HeadBranch string
	// HeadSHA is the commit at the head of it. Everything crewflow approves,
	// reviews and merges is about this one commit (§8).
	HeadSHA string
	// BaseBranch is the branch the work is meant for.
	BaseBranch string
	// State is "open", "merged" or "closed".
	State string
	// Body is the text the request was opened with, "Closes #N" among it.
	Body string
	// Draft says that the request is still a draft and asks for nothing yet.
	Draft bool
	// Repository is the repository the work is in, as "owner/name". A host that
	// does not write it says nothing: a change of the repository of the project is
	// read as a change in the project, and a change of a fork is not
	// (docs/DESIGN.md §7h).
	Repository string
	// Conflicted says that the request cannot be merged into its base branch as it
	// stands. A host that runs checks only on a mergeable request will not run any
	// for such a change, and a gate that reported the checks as missing would be
	// reporting the absence of something the host was never going to do
	// (docs/DESIGN.md §7h).
	Conflicted bool
	// MergeState is what the host says about merging the request into its base
	// branch, in the words of the host: "CLEAN", "DIRTY" when it conflicts,
	// "BLOCKED", "UNSTABLE", and "UNKNOWN" while the host has not worked it out.
	// It is there because which of those is an answer and which is not is a
	// question of the gate and not of an adapter (docs/DESIGN.md §7h).
	MergeState string
}

// Comment is one comment under a change request, by a person or by an agent.
type Comment struct {
	// Author is who wrote it, as the host keeps that account: its kind and the number
	// under it. The gate counts a record by that subject and never by a login, because
	// a host writes one account in a different line in every API and renames it with
	// the App (docs/DESIGN.md §7h, §7i).
	Author Subject
	// Body is what was written.
	Body string
	// CreatedAt is when it was written, which orders the review of a run.
	CreatedAt time.Time
	// Edited says that it was changed after it was published. A record of a review
	// that has been edited is a record anybody can rewrite after the fact, and the
	// gate does not count it (docs/DESIGN.md §7h).
	Edited bool
}

// Forge is where the code of a project is hosted and reviewed: the requests to
// change it, what is at the head of one, and what was said under it.
type Forge interface {
	RoleChecker
	// FindChangeRequest returns the request of the branch, and whether there is
	// one at all: a run that has pushed a branch but no request yet is a run in
	// progress, not a failure.
	FindChangeRequest(ctx context.Context, branch string) (ChangeRequest, bool, error)
	// ChangeRequest returns the request with the number, and an error when
	// there is no such request or the host could not be read.
	ChangeRequest(ctx context.Context, number int) (ChangeRequest, error)
	// Comments returns what was written under the request, oldest first: the
	// review of a run is read in the order it was written.
	Comments(ctx context.Context, number int) ([]Comment, error)
}

// CheckState is how the checks of a commit stand.
type CheckState string

// The four answers about the checks of a commit. None is not a red: it says
// there was nothing to run.
const (
	// CheckPending means a check has not finished yet.
	CheckPending CheckState = "pending"
	// CheckSuccess means every check finished and passed.
	CheckSuccess CheckState = "success"
	// CheckFailure means a check finished and did not pass.
	CheckFailure CheckState = "failure"
	// CheckNone means there were no checks on the commit at all.
	CheckNone CheckState = "none"
)

// CI is where the checks of a commit run. Crewflow reads their result and does
// not run them: it is not a replacement for CI (docs/DESIGN.md §3).
type CI interface {
	RoleChecker
	// Status returns how the checks of the commit stand.
	Status(ctx context.Context, sha string) (CheckState, error)
}

// CheckRun is one check of a commit, as a gate needs it and not as a report of a
// machine needs it: the name it is known under, how it stands, the app that
// reported it, and the commit it is about. The last two are why a green mark of
// somebody else about another commit is not a check of this change
// (docs/DESIGN.md §7h).
type CheckRun struct {
	// Name is what the rules of the branch call the check.
	Name string
	// State is how the check stands.
	State CheckState
	// App is the app that reported it — "github-actions" for a workflow of
	// GitHub — and empty for a mark written through the API of statuses, which is
	// the mark of whoever wrote it.
	App string
	// SHA is the commit the check is about.
	SHA string
}

// RequiredCheck is a check the rules of the branch of a project demand of a change,
// and the app its result is to be taken from. The app is the rule of the host made
// a fact: for GitHub it is GitHub Actions, and a mark written through the API of
// statuses is the mark of whoever wrote it (docs/DESIGN.md §7h).
type RequiredCheck struct {
	// Name is what the rules of the branch call the check.
	Name string
	// App is the slug of the app the check is to come from, and empty where a host
	// expects none.
	App string
}

// CheckLister is the CI of a project that can say what checks a commit has, where
// each of them came from and which of them the rules of the branch demand. A role
// that is not one of these has no checks to name: a project with no CI at all is
// answered for, and a project whose CI cannot say has not answered (docs/DESIGN.md §7g, §7h).
type CheckLister interface {
	CI
	// Checks returns the checks of the commit: the runs of a host and the marks
	// written through its API of statuses, both of which have a name, a state, a
	// source and a commit.
	Checks(ctx context.Context, sha string) ([]CheckRun, error)
	// RequiredChecks returns what the rules of the branch of the project demand of
	// a change, and the app each of them is to be taken from. An answer with nothing
	// in it means the host names no check, and what the project says about its CI is
	// then what stands (docs/DESIGN.md §7h).
	RequiredChecks(ctx context.Context) ([]RequiredCheck, error)
}

// RuleState is what a host says about the rules of the branch of a project: it named
// the checks they demand, or the plan of the repository has no rules to demand
// anything with. Both are answers, and neither of them is a host that could not be
// read (docs/DESIGN.md §7h, §7k).
type RuleState string

const (
	// RulesNamed says that the host answered about the rules of the branch, whether
	// or not it named a check in them.
	RulesNamed RuleState = "named"
	// RulesUnavailableOnPlan says that the plan of the repository has no rules of a
	// branch at all: GitHub has rulesets only in public repositories on the free
	// plan, and it says so with a refusal that names the plan. That refusal is a
	// fact about the repository and not a silence of the host, and a gate that read
	// it as a silence would refuse every change of such a project for ever
	// (docs/DESIGN.md §7h, §7k).
	RulesUnavailableOnPlan RuleState = "unavailable-on-plan"
)

// BranchRules are the rules of the branch of a project as the host holds them: what
// they demand of a change, and whether the host has any rules to demand it with.
type BranchRules struct {
	// Required is what the rules of the branch demand of a change. It is empty where
	// the host names no check, and it is empty for a repository whose plan has no
	// rules as well — which is a fact of the plan and not of the rules.
	Required []RequiredCheck
	// State is what the host said about the rules themselves.
	State RuleState
}

// RuleLister is the CI of a project that can say whether the rules of the branch of
// the project are there at all. A CI that names checks and says nothing about that is
// a [CheckLister], and its answer is read as rules that demanded what it named
// (docs/DESIGN.md §7g, §7h).
type RuleLister interface {
	CI
	// Rules returns what the rules of the branch demand of a change and what the host
	// said about the rules themselves. A state of [RulesUnavailableOnPlan] is an
	// answer and not a gap in what was asked: the host has said that the repository
	// has no rules, and what the project says about its CI is then what stands
	// (docs/DESIGN.md §7h).
	Rules(ctx context.Context) (BranchRules, error)
}

// FileLister is the host that can say which files a change request touches. The
// boundaries of a task are checked against the work and not against what the
// executor said it wrote, both after a run and before a merge (docs/DESIGN.md §7c, §7h).
type FileLister interface {
	// ChangedFiles returns the paths a change request changes against the branch
	// it is meant for, as the host knows them.
	ChangedFiles(ctx context.Context, number int) ([]string, error)
}

// HeadRef is the host's own name of the ref that stands at the head of a change.
// A review needs the commit of the head in a checkout to ask git about the history,
// and a host may name that ref differently on every host (§7h).
type HeadRef interface {
	// HeadRef is the ref of the host that is the head of the change request, as
	// `git fetch` is given it.
	HeadRef(number int) string
}

// CommentWriter is the host a record of a review is written to: a comment under the
// change, in the name of the account whose approval the gate counts. It is the
// orchestrator's business and never the executor's: a record an executor writes is a
// record the gate does not count (docs/DESIGN.md §7h, §7i).
type CommentWriter interface {
	// WriteComment writes the body under the change request, as the account the
	// project reviews with.
	WriteComment(ctx context.Context, number int, body string) error
}

// SignedIn is the host that can say which account it speaks as, without handing out
// the rights of that account: a record of a review is a comment, and it is an
// approval only because of the account it is written in (docs/DESIGN.md §7h).
type SignedIn interface {
	// SignedIn is the login of the account the role is authenticated as, and an
	// error when it could not be read at all — which is not the same thing as an
	// account of no name.
	SignedIn(ctx context.Context) (string, error)
}

// The two modes of a run, the words the core knows and nothing else: the executor
// works as the person who runs crewflow, or as an account of the host of the project
// of its own. How a bot of a host is written down is the business of the adapter of
// that host, and a project that wants a mode a host has no adapter for is told so
// (docs/DESIGN.md §7i).
const (
	// ModeOwner is the login of the person: what crewflow has always done, what needs
	// nothing to be set up, and what the powers of the executor are the powers of.
	ModeOwner = "owner"
	// ModeBot is an account of the host of its own, with rights of its own and a
	// token of its own, which is what separates the powers of a run from the powers
	// of the person (§7i).
	ModeBot = "bot"
)

// The two modes of the orchestrator, the words the core knows and nothing else: the
// orchestrator works under the login of the person, together with the owner and
// therefore not to be told from it, or as an account of the host of the project of
// its own, which is what separates the two (§7i).
const (
	// ModeShared is the login of the person on both sides: what crewflow has always
	// done, what needs nothing to be set up, and what makes the acceptance of the
	// owner and the record of a review one signature.
	ModeShared = "shared"
	// ModeSeparate is an account of the host of its own, with rights of its own and a
	// token of its own, which is what lets a gate tell a decision of the owner from
	// a record of the orchestrator (§7i).
	ModeSeparate = "separate"
)

// Identity is whose name the executor of a run works under, and everything the run
// needs to work under it (docs/DESIGN.md §7i).
//
// The core asks the adapter of the host of the project for it and takes it as it is:
// a GitHub App answers with a token, an installation and an account, and a bot of
// another host answers with whatever it has (§7g, §7i). The values of a run are the
// values of the account, and the run puts them through the redactor of package secret
// with what it wrote (§7e).
type Identity struct {
	// Mode is the mode of the subject the identity is of: ModeOwner or ModeBot for the
	// executor of a run, ModeShared or ModeSeparate for the orchestrator of a project.
	// The four words are what a report and a state file hold, and which two of them
	// can stand together is the business of whoever asks (docs/DESIGN.md §7i).
	Mode string
	// Description is the one line a report shows and a journal keeps, as
	// "bot — GitHub App crewflow-executor (installation 12345)" or
	// "owner — the login gh naghuale (shared rights)": a person reading a run has to
	// see whose name it went under without asking anything. For the orchestrator it is
	// "separate — GitHub App crewflow-orchestrator (installation 12346)" or
	// "shared — the login gh naghuale (one login with the owner)".
	Description string
	// Account is the login of the host the subject works as, as the host writes it:
	// "crewflow-executor[bot]" for the App of a run, "crewflow-orchestrator[bot]" for
	// the App of the orchestrator, and the login of gh for either of them in a mode
	// where the work happens under the login of the person. It is empty where nobody
	// could be asked, and the accounts of a project are compared with it by the gate
	// and by `crewflow doctor` — a report that cannot name them cannot say whether the
	// owner and the orchestrator are one subject (docs/DESIGN.md §7i, §7k).
	Account string
	// Env are the variables the executor of the run is started with on top of the
	// ones of the person: a token of the host, and the name and the address its
	// commits are made by. It is empty for the mode of the owner, where the login of
	// the person is what the executor already has.
	Env []string
	// GitConfig are the settings git is given in the worktree of the run, by name
	// and value. It is empty for the mode of the owner.
	GitConfig map[string]string
	// Secrets are the values among the above that must never reach a journal, a
	// report or an error: the token of a run, and a key that has been in it. A run
	// puts everything the executor writes through the redactor of package secret
	// with them in it, because an agent that prints its own environment is one line
	// of a journal that would otherwise carry a token of an hour into a file kept
	// for ever (docs/DESIGN.md §7e).
	Secrets []string
}

// Identified is a role of the host of the code that knows whose name the executor of
// a run of this project works under: GitHub answers with a GitHub App, GitLab with a
// token of a project, Bitbucket with a user and an access token of its own
// (docs/DESIGN.md §7i). A host that has no such account yet is a host that has not
// answered, and the core does not guess one for it.
type Identified interface {
	// ExecutorIdentity returns what a run of this project is given to work under
	// the name of an account: the mode, the one line a report shows, the
	// environment of the executor, the settings of git in its worktree and the
	// values that must not reach a journal (§7e).
	ExecutorIdentity(ctx context.Context) (Identity, error)
}

// Described is a role that can say whose name the executor of a run works under
// without handing out the rights of that name: a report of a machine prints the line
// of the mode and needs no token of an hour to print it, and a token it minted for
// that would be a token of an hour in the memory of a command that shows a report
// (docs/DESIGN.md §7e, §7i).
type Described interface {
	// DescribeIdentity returns the mode of a run and the one line a report shows,
	// whatever a run of it would be given.
	DescribeIdentity(ctx context.Context) (Identity, error)
}

// DescribeIdentity is the line of a report about whose name the executor of a run
// works under. A role that can answer without a token of its own is asked that way,
// and a role that cannot is asked the whole identity: a report of a machine is worth
// a token of an hour that it throws away, but it is not worth a line it cannot print
// (docs/DESIGN.md §7e, §7i).
func DescribeIdentity(ctx context.Context, role Forge) (Identity, error) {
	if described, ok := role.(Described); ok {
		return described.DescribeIdentity(ctx)
	}
	return IdentityOf(ctx, role)
}

// IdentityOf is whose name the executor of a run of this project works under, asked
// of the role of the host of the code — which is the only one that knows what
// accounts of that host there are (§7g, §7i).
//
// A project that is not hosted anywhere works as the person who runs crewflow: there
// is no account of a host to be, and a run of such a project opens nothing anywhere.
// A host that cannot answer — no App installed, no key imported, no network — is an
// error and not a fallback: a run that went on as somebody else is a run whose
// report says the wrong thing about who worked under whose name.
func IdentityOf(ctx context.Context, role Forge) (Identity, error) {
	identified, ok := role.(Identified)
	if !ok {
		return Identity{
			Mode:        ModeOwner,
			Description: "owner — the person who runs crewflow (shared rights)",
		}, nil
	}
	return identified.ExecutorIdentity(ctx)
}

// AppChecker is a role of the host of the code that can check the accounts of that host
// it works as on this machine: the key of an App in the store of the machine, the
// installation of it on the repository of the project, the rights it was given, and
// whether it hands out a token at all.
//
// It is a question of its own because a project has as many accounts as it has
// subjects: the App of the executor and the App of the orchestrator are two accounts,
// and a report that answered for one of them under the name of the other would leave a
// person with two lines and no way to tell which key is missing (docs/DESIGN.md §7i).
type AppChecker interface {
	// AppChecks returns the lines about the accounts of the host this role works as,
	// and nothing about the program the host is read with: a report of a machine checks
	// gh once and the accounts of the project once each.
	AppChecks(ctx context.Context) []Check
}

// AppChecksOf are the lines about the accounts of the host a role works as, and nothing
// where it has none to tell: a project that is not hosted anywhere, and a host crewflow
// has no account of, are roles with no check of their own here (§7g, §7i).
func AppChecksOf(ctx context.Context, role Forge) []Check {
	checker, ok := role.(AppChecker)
	if !ok {
		return nil
	}
	return checker.AppChecks(ctx)
}

// Orchestrated is the role of the host of the code that knows whose name the
// orchestrator of a project works under: an account of the host of its own with a
// token of its own, which is what lets a gate tell its records from the records of
// the owner (docs/DESIGN.md §7i).
type Orchestrated interface {
	// OrchestratorIdentity returns what the orchestrator of a project is given to
	// work under the name of an account: the mode, the one line a report shows, the
	// environment gh and git are started with, the settings git takes its
	// credentials from, and the values that must not reach a journal (§7e, §7i).
	OrchestratorIdentity(ctx context.Context) (Identity, error)
}

// OrchestratorDescribed is the same role asked without the rights of that name: a
// report of a machine prints the line of the mode and needs no token of an hour to
// print it, and a token it minted for that would be a token of an hour in the memory
// of a command whose work is to print a report (docs/DESIGN.md §7e, §7i).
type OrchestratorDescribed interface {
	// DescribeOrchestrator returns the mode of the orchestrator, the one line a report
	// shows and the settings git takes its credentials from — and no token anywhere
	// in it.
	DescribeOrchestrator(ctx context.Context) (Identity, error)
}

// OrchestratorOf is whose name the orchestrator of this project works under, asked of
// the role of the host of the code.
//
// A project that is not hosted anywhere has no account of a host to be an
// orchestrator apart from the owner in, and it is the person who runs crewflow: there
// is nothing to write a record of a review in but that login. A host that cannot
// answer — no App installed, no key imported, no network — is an error and not a
// fallback, because a review written in the name of somebody else is a record the
// gate will not count (docs/DESIGN.md §7h, §7i).
func OrchestratorOf(ctx context.Context, role Forge) (Identity, error) {
	if orchestrated, ok := role.(Orchestrated); ok {
		return orchestrated.OrchestratorIdentity(ctx)
	}
	if role == nil {
		return sharedIdentity(""), nil
	}
	return sharedIdentity(loginOf(role)), nil
}

// DescribeOrchestrator is the line a report shows about the orchestrator, asked the
// way a role that can answer without a token of its own is asked, and the whole
// identity of it where it cannot: a report of a machine is worth a token of an hour
// that it throws away, but it is not worth a line it cannot print (§7e, §7i).
func DescribeOrchestrator(ctx context.Context, role Forge) (Identity, error) {
	if described, ok := role.(OrchestratorDescribed); ok {
		return described.DescribeOrchestrator(ctx)
	}
	return OrchestratorOf(ctx, role)
}

// sharedIdentity is the orchestrator of a project that has no account of a host of
// its own: the login of the person, named after the account gh is signed in as, and
// the same login the owner works under (docs/DESIGN.md §7i).
func sharedIdentity(account string) Identity {
	if account == "" {
		return Identity{
			Mode:        ModeShared,
			Description: "shared — the login of gh (one login with the owner)",
		}
	}
	return Identity{
		Mode:        ModeShared,
		Description: "shared — the login gh " + account + " (one login with the owner)",
	}
}

// loginOf is who the role speaks as, and an empty string where it cannot say: a line
// of a report that names nobody is said as such rather than left blank, and `gh auth
// status` is a check of doctor of its own (docs/DESIGN.md §7g).
func loginOf(role Forge) string {
	signed, ok := role.(SignedIn)
	if !ok {
		return ""
	}
	account, err := signed.SignedIn(context.Background())
	if err != nil {
		return ""
	}
	return account
}

// RequestOpener opens the change request of a branch on behalf of a run whose
// executor did not: an agent runs out of time, or the token of gh it was given is
// over, and the work of a run is on a branch that nobody asked about (§7i). Only the
// mode of the bot has one — the App may open a request of its own branch — and a run
// without one is a run whose outcome is no-change-request, whatever the branch holds.
type RequestOpener interface {
	// OpenChangeRequest opens the request of the branch with the given title and
	// body. It asks the host for credentials of its own rather than using the ones
	// it is given, because the ones of a run are out of date by the time a run asks:
	// that is why a run gets here at all.
	OpenChangeRequest(ctx context.Context, branch, title, body string) (ChangeRequest, error)
}

// Status is how one check of a role ended. The words are the ones the report of
// doctor uses, so that a line of a role is a line of the report and not a
// second dialect to translate.
type Status string

// A role is either usable or not: there is nothing in between that a task may
// start with.
const (
	// OK is a check that passed: the program is there and the login is done.
	OK Status = "ok"
	// Fail is a check that did not pass: the task cannot go this way.
	Fail Status = "fail"
)

// Check is one line about the machine, made by a role before a task starts: is
// the program of its system installed and is somebody signed in with it
// (docs/DESIGN.md §7d).
type Check struct {
	// Name is what the report calls the check: the program, the login behind it.
	Name string
	// Status is how it ended.
	Status Status
	// Detail is one line of what was found, without anything secret in it.
	Detail string
	// Hint is what to do about it, and is there only when something is wrong.
	Hint string
}

// RoleChecker is the check a role makes of the machine. Every role has one, and
// doctor asks all three of them what they found, because each has a program of
// its own and a login of its own (docs/DESIGN.md §7d, §7g).
type RoleChecker interface {
	// Doctor returns the lines of what the role found on the machine, in the
	// order a person should read them.
	Doctor(ctx context.Context) []Check
}

// Every role can be asked what it found, and nothing else about the machine:
// a caller asks the role, not the machine, what to check.
var (
	_ func(Tracker) RoleChecker = func(role Tracker) RoleChecker { return role }
	_ func(Forge) RoleChecker   = func(role Forge) RoleChecker { return role }
	_ func(CI) RoleChecker      = func(role CI) RoleChecker { return role }
)

// Env is everything an adapter needs from the machine. doctor hands its own
// environment over as it is: an adapter must be able to look a program up in
// PATH, start it with a closed stdin, and add to the environment of the process
// what its own system needs (GH_HOST and the like).
//
// The three fields below are how an adapter that works as a bot of its host reaches
// that host: the key of the App is in a store of secrets, the API is an HTTP client
// and the token of a run is a promise about a moment of a clock. They are nil in a
// test that has no bot to run, and a test of an adapter with a bot of its own hands
// all three over, so that no test of crewflow reaches the keychain of a person and no
// test of a token exchange reaches GitHub (docs/DESIGN.md §7i).
type Env struct {
	// LookPath finds a program the way the shell does, in PATH.
	LookPath func(name string) (string, error)
	// Run starts a program in dir with the extra environment added to the one of
	// the process, and returns what it wrote and the code it exited with.
	Run func(ctx context.Context, name string, args []string, dir string, extraEnv []string) (stdout, stderr []byte, exitCode int, err error)
	// ConfigPath is the crewflow.toml a hint names, so that a person is told
	// which file to change.
	ConfigPath string
	// Secrets is where a role keeps what it must not print: the key of the App of a
	// project, and whatever else a system of a project needs a secret for. Only
	// crewflow reads it, in the moment it signs a token, and never hands it to an
	// executor (docs/DESIGN.md §7e, §7i).
	Secrets secret.Store
	// HTTP is the client an adapter asks the API of a host over. A role that talks
	// through a program of the system — gh, glab — has no use for it and leaves it
	// nil.
	HTTP *http.Client
	// Now is the clock of the machine. A token of a run is signed with it and lives
	// an hour, so a role that mints tokens needs to know what time it is.
	Now func() time.Time
}

// Set is what a project has for the three roles: the implementations its
// settings ask for, each of which may be nil where a project has no such role.
type Set struct {
	// Forge is where the code is hosted and reviewed, nil for a project
	// without a host.
	Forge Forge
	// Tracker is where the tasks come from.
	Tracker Tracker
	// CI is where the checks of a commit run.
	CI CI
	// Opener opens the change request of a branch on behalf of a run whose executor
	// did not, and is nil where the project has asked for none: a project in the
	// mode of the bot has the App for it, and a project in the mode of the owner has
	// the login of the person, which is what a run has always had (§7i).
	Opener RequestOpener
}

// Checkers returns the adapters of the set that can check the machine, in the
// order of the roles and with the same adapter only once: by default all three
// roles are GitHub, and the machine is checked once and not three times.
func (s Set) Checkers() []RoleChecker {
	checkers := make([]RoleChecker, 0, 3)
	for _, role := range []RoleChecker{s.Forge, s.Tracker, s.CI} {
		if role == nil || slices.Contains(checkers, role) {
			continue
		}
		checkers = append(checkers, role)
	}
	return checkers
}

// ErrNotImplemented is the answer for a kind of a role crewflow has no adapter
// for: the adapters of the other systems come later, and a report that says so
// is worth more than a run that fails on the way in (§7g).
type ErrNotImplemented struct {
	// Key is the setting that asked for the adapter, "forge.kind".
	Key string
	// Kind is the kind that was asked for, "gitlab".
	Kind string
}

// Error says which key asks for which kind, so that a person is not left to
// search the file for the reason.
func (e *ErrNotImplemented) Error() string {
	return fmt.Sprintf("%s: adapter %s is not implemented yet", e.Key, e.Kind)
}
