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
	"fmt"
	"slices"
	"time"
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

// Tracker is where the tasks of a project come from. Crewflow reads a task and
// never writes to the tracker: a run that opens a task is a change outside the
// cycle, and those are not crewflow's (§7e).
type Tracker interface {
	RoleChecker
	// Task returns the task with the number, and an error when there is no
	// such task or the tracker could not be read.
	Task(ctx context.Context, number int) (Task, error)
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
}

// Comment is one comment under a change request, by a person or by an agent.
type Comment struct {
	// Author is who wrote it, by the name the host knows them under.
	Author string
	// Body is what was written.
	Body string
	// CreatedAt is when it was written, which orders the review of a run.
	CreatedAt time.Time
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
type Env struct {
	// LookPath finds a program the way the shell does, in PATH.
	LookPath func(name string) (string, error)
	// Run starts a program in dir with the extra environment added to the one
	// of the process, and returns what it wrote and the code it exited with.
	Run func(ctx context.Context, name string, args []string, dir string, extraEnv []string) (stdout, stderr []byte, exitCode int, err error)
	// ConfigPath is the crewflow.toml a hint names, so that a person is told
	// which file to change.
	ConfigPath string
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
