package gate

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/naghuale/crewflow/internal/forge"
)

// Deps is everything a review is made of: the roles of the project and the
// repository its history is read from. It is passed in whole, so that a test of the
// gathering has a host, a CI and a git of its own and reaches neither the network
// nor a repository of the person who runs it (docs/DESIGN.md §7h).
type Deps struct {
	// Forge is the host of the code and the CI is where the checks of a commit
	// run, both of them the roles of §7g. A project that has no CI says so with
	// a CI that has no checks, and the gate asks for none.
	Forge forge.Forge
	CI    forge.CI
	// Repository is the project as "owner/name", and DefaultBranch the branch it
	// starts from and merges into: a change of anywhere else is not a change of
	// this project.
	Repository    string
	DefaultBranch string
	// Reviewers are the logins whose record of a review counts, and Owners those whose
	// record of an acceptance of the result of a task counts. Empty is the owner of the
	// repository in both, which is the default of §5.
	Reviewers []string
	Owners    []string
	// AcceptanceLabel is the label a task is marked with to say that its result has to
	// be accepted by the owner before the change of it may go in, and empty is a project
	// that asks for no acceptance at all (docs/DESIGN.md §5, §7f, §7h).
	AcceptanceLabel string
	// Task is the task the change is of, zero when crewflow does not know it, and
	// TaskOf where the boundaries and the labels of that task come from. A caller that
	// reads them out of the tracker of the project hands Boundaries and Labels over
	// instead: the gate asks for them once it knows which task the change is of, and a
	// task crewflow does not know leaves every file of the change outside its
	// boundaries.
	Task       int
	Boundaries []string
	Labels     []string
	TaskOf     func(ctx context.Context, task int) (TaskFacts, error)
	// RequireChecks is [ci] required: what the rules of the branch of the host do
	// not name is required as well, and nothing at all is when the project says
	// its checks are nobody's business.
	RequireChecks bool
	// Git is the checkout the history of the change is asked of.
	Git History
}

// TaskFacts is what the gate asks the tracker for about the task of a change: the paths
// the task was to change and the words it is marked with. Both come out of one reading
// of the task, because a task read twice is a task that may have been marked in between
// (docs/DESIGN.md §7h).
type TaskFacts struct {
	// Boundaries are the paths the task was to change.
	Boundaries []string
	// Labels are the words the tracker marks the task with, the label of the owner's
	// acceptance among them.
	Labels []string
}

// Collect gathers the facts of a change from the host, from the CI of the head and
// from git, and hands them over.
//
// It never fails: whatever it could not learn goes into [Facts.Unavailable] and the
// gate turns that into `forge-unavailable`. A review whose facts are half gathered
// is a review a person has to be told about, and the reason it cannot be judged has
// to be the reason of §7h and not an error of a program that went on somewhere else
// (docs/DESIGN.md §7h).
func Collect(ctx context.Context, deps Deps, number int) Facts {
	f := Facts{
		WantRepository:  deps.Repository,
		WantBranch:      deps.DefaultBranch,
		Reviewers:       deps.Reviewers,
		Owners:          deps.Owners,
		AcceptanceLabel: deps.AcceptanceLabel,
		Task:            deps.Task,
		Boundaries:      deps.Boundaries,
		Labels:          deps.Labels,
	}
	if deps.Forge == nil {
		return f.unavailable("forge.kind: this project has no host of its code, so there is no change request to review")
	}
	change, err := deps.Forge.ChangeRequest(ctx, number)
	if err != nil {
		return f.unavailable(fmt.Sprintf("read the change request #%d: %v", number, err))
	}
	f.Number = change.Number
	f.URL = change.URL
	f.Open = change.State == "open"
	f.Draft = change.Draft
	f.Conflicted = change.Conflicted
	f.MergeState = change.MergeState
	f.Repository = change.Repository
	f.TargetBranch = change.BaseBranch
	f.Head = change.HeadSHA
	// A change request of a host that writes no repository of its own is asked
	// about only when it says it is in another one: the common case is a change of
	// the project of the file, and a second question about it is a question whose
	// answer changes with the version of a host.
	if f.Repository == "" {
		f.Repository = f.WantRepository
	}
	// What is written under the change is read whatever the state of it is: a person
	// looking at a change that was merged asks who approved it, and a summary that
	// said "no record of a review" under a merged change would be lying about a fact
	// the host holds (docs/DESIGN.md §7h).
	f.reviews(ctx, deps, number)
	if change.State != "open" {
		return f
	}

	f.taskOf(ctx, deps)
	f.files(ctx, deps, number)
	// The checks of a change that conflicts with its branch are asked about only
	// when it does not: GitHub does not run them, and the gate names the conflict
	// itself instead of reporting checks that were never going to come.
	if !f.Conflicted {
		f.collectChecks(ctx, deps)
	}
	f.history(ctx, deps, number)
	return f
}

// reviews are the records under the change: the ones that approve or ask for
// changes, the ones in which a person takes a file outside the boundaries of the
// task into their own hands, and the ones in which the owner takes the result of
// the task as it stands in. All three are read as they are written and each is
// counted only of the accounts of the project that may write it (docs/DESIGN.md §7h).
func (f *Facts) reviews(ctx context.Context, deps Deps, number int) {
	comments, err := deps.Forge.Comments(ctx, number)
	if err != nil {
		*f = f.unavailable(fmt.Sprintf("read what is written under the change request #%d: %v", number, err))
		return
	}
	for _, comment := range comments {
		if record, is := Parse(comment.Body); is {
			f.Reviews = append(f.Reviews, Review{
				Author:    comment.Author,
				CreatedAt: comment.CreatedAt,
				Edited:    comment.Edited,
				Body:      comment.Body,
			})
			if record.Task > 0 && f.Task == 0 {
				f.Task = record.Task
			}
			continue
		}
		if commit, reason, is := ParseAcceptance(comment.Body); is {
			f.Accepted = append(f.Accepted, Acceptance{
				Author:    comment.Author,
				CreatedAt: comment.CreatedAt,
				Edited:    comment.Edited,
				Commit:    commit,
				Reason:    reason,
			})
			continue
		}
		if commit, is := ParseOwnerAccept(comment.Body); is {
			f.OwnerAccepts = append(f.OwnerAccepts, OwnerAccept{
				Author:    comment.Author,
				CreatedAt: comment.CreatedAt,
				Edited:    comment.Edited,
				Commit:    commit,
			})
		}
	}
}

// taskOf are the paths the task of the change was to change and the words it is marked
// with, out of the tracker of the project. A change whose task crewflow does not know
// has no boundaries, and then every file of it is outside them: a gate that let a
// change through because nobody could say what it was to touch would be a gate with a
// hole in it (docs/DESIGN.md §7c, §7h).
func (f *Facts) taskOf(ctx context.Context, deps Deps) {
	if len(f.Boundaries) > 0 || deps.TaskOf == nil {
		return
	}
	// The record of a review may name the task of the change where the run that
	// opened it is not known here, and the record is read before this: a change of a
	// project crewflow ran on another machine still has its boundaries and its labels.
	if f.Task <= 0 {
		return
	}
	found, err := deps.TaskOf(ctx, f.Task)
	if err != nil {
		*f = f.unavailable(fmt.Sprintf("read the task %d of the change: %v", f.Task, err))
		return
	}
	f.Boundaries, f.Labels = found.Boundaries, found.Labels
}

// files is what the change touches, as the host knows it. It is the work and not
// the account of it: the boundaries of a task are checked against the files that
// really changed (docs/DESIGN.md §7c, §7h).
func (f *Facts) files(ctx context.Context, deps Deps, number int) {
	lister, ok := deps.Forge.(forge.FileLister)
	if !ok {
		*f = f.unavailable(fmt.Sprintf("the host of the project cannot say which files the change request #%d touches, so the boundaries of the task cannot be checked", number))
		return
	}
	files, err := lister.ChangedFiles(ctx, number)
	if err != nil {
		*f = f.unavailable(fmt.Sprintf("read the files of the change request #%d: %v", number, err))
		return
	}
	f.Files = files
}

// checks are the checks of the head of the change and what the rules of its branch
// demand of them.
//
// The host is asked which checks it requires, and where its rules name none, the
// checks the head has are the ones that are required — or, when the head has none
// either and the project says its CI is required, nothing is known to have run and
// the gate is told so rather than let a commit through on the strength of a silence
// (docs/DESIGN.md §7h).
//
// The rules of a branch of the host may also be absent altogether: a repository on the
// free plan has none, and that is what the host says, so the checks of the head stand
// for them just as they do where the rules name none (§7h, §7k).
func (f *Facts) collectChecks(ctx context.Context, deps Deps) {
	if !deps.RequireChecks {
		return
	}
	host, ok := deps.CI.(forge.CheckLister)
	if !ok {
		// A project that says it has no CI has said what the gate is to require of
		// a commit: nothing, and that is an answer and not a gap.
		return
	}
	rules, err := rulesOf(ctx, host)
	if err != nil {
		*f = f.unavailable(fmt.Sprintf("read the rules of the branch %s: %v", f.WantBranch, err))
		return
	}
	checks, err := host.Checks(ctx, f.Head)
	if err != nil {
		*f = f.unavailable(fmt.Sprintf("read the checks of the commit %s: %v", f.Head, err))
		return
	}
	f.Checks = checks
	f.Required = rules.Required
	f.Rules = rules.State
	if len(rules.Required) > 0 {
		return
	}
	if len(checks) == 0 {
		*f = f.unavailable(nothingRan(rules.State, f.WantBranch, f.Head))
		return
	}
	for _, check := range checks {
		f.Required = append(f.Required, forge.RequiredCheck{Name: check.Name, App: check.App})
	}
}

// rulesOf are the rules of the branch of the project as the CI of it holds them.
//
// The CI that can say whether the host has any rules at all is asked for both at once,
// and a CI that names checks and says nothing about that is asked for the names alone:
// its answer is rules that demanded what it named, and a repository without rules is
// not a thing it can tell (docs/DESIGN.md §7g, §7h).
func rulesOf(ctx context.Context, host forge.CheckLister) (forge.BranchRules, error) {
	if lister, ok := host.(forge.RuleLister); ok {
		return lister.Rules(ctx)
	}
	required, err := host.RequiredChecks(ctx)
	if err != nil {
		return forge.BranchRules{}, err
	}
	return forge.BranchRules{Required: required, State: forge.RulesNamed}, nil
}

// nothingRan is why nothing is known to have run for a change, told as whichever of
// the two silences of the host it is: the rules of the branch are there and name no
// check, or the plan of the repository has no rules to name any. Both are refusals, and
// a person has to be told which of them they are looking at (docs/DESIGN.md §7h, §7k).
func nothingRan(state forge.RuleState, branch, head string) string {
	if state == forge.RulesUnavailableOnPlan {
		return fmt.Sprintf("the plan of this repository has no rules of a branch to demand a check, "+
			"and the commit %s has none either, while the project asks for the CI of every commit: "+
			"nothing is known to have run for this change", head)
	}
	return fmt.Sprintf("the rules of the branch %s name no check, and the commit %s has none either, "+
		"while the project asks for the CI of every commit: nothing is known to have run for this change",
		branch, head)
}

// history is what git says about the commits of the change: the objects first, and
// then whether the default branch of the project and the approved commit are
// ancestors of the head (docs/DESIGN.md §7h).
func (f *Facts) history(ctx context.Context, deps Deps, number int) {
	refs, ok := deps.Forge.(forge.HeadRef)
	if !ok {
		*f = f.unavailable(fmt.Sprintf("the host of the project names no ref for the head of the change request #%d, so its history cannot be read", number))
		return
	}
	if err := deps.Git.Prepare(ctx, refs.HeadRef(number), f.Head); err != nil {
		*f = f.unavailable(fmt.Sprintf("read the history of the change: %v", err))
		return
	}
	ancestor, err := deps.Git.DefaultIsAncestor(ctx, f.Head)
	if err != nil {
		*f = f.unavailable(fmt.Sprintf("ask git whether %s is an ancestor of the head: %v", f.WantBranch, err))
		return
	}
	f.DefaultIsAncestor = ancestor
	if approved, is := f.approvedCommit(); is {
		ancestor, err := deps.Git.Ancestor(ctx, approved, f.Head)
		if err != nil {
			*f = f.unavailable(fmt.Sprintf("ask git whether the approved commit %s is an ancestor of the head: %v", approved, err))
			return
		}
		f.ApprovedIsAncestor = ancestor
	}
}

// unavailable is the facts with the reason they cannot be judged in, and nothing
// else: a fact that could not be gathered is a refusal, and a refusal that is
// hidden behind a verdict about a commit nobody looked at is the one failure of a
// gate that cannot be seen.
func (f Facts) unavailable(why string) Facts {
	f.Unavailable = why
	return f
}

// TaskOf is the task a change is of, out of the text it was opened with: the
// `Closes #N` of §7 is what ties a change to a task, and a change whose task
// nobody can find out has no boundaries crewflow could check it against.
func TaskOf(body string) (int, bool) {
	for raw := range strings.Lines(body) {
		line := strings.TrimSpace(raw)
		for _, mark := range []string{"Closes", "Fixes", "Resolves"} {
			rest, found := strings.CutPrefix(line, mark)
			if !found {
				continue
			}
			number, found := strings.CutPrefix(strings.TrimSpace(rest), "#")
			if !found {
				continue
			}
			fields := strings.Fields(number)
			if len(fields) == 0 {
				continue
			}
			// "Closes #7, #8" is one line about two tasks and the first of them is
			// the one the change is of: the punctuation after the number is the
			// sentence's, not part of it.
			if task, err := strconv.Atoi(strings.TrimRight(fields[0], ".,;:")); err == nil && task > 0 {
				return task, true
			}
		}
	}
	return 0, false
}
