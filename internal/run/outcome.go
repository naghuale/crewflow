package run

import (
	"context"
	"strings"

	"github.com/naghuale/crewflow/internal/task"
)

// Kind is how a run ended, and one value for a run that has not ended yet. None of
// them is "the executor exited zero": that code is what a run of a refused permission
// and a run of a finished task both return (docs/DESIGN.md §7a).
type Kind string

// Running is the outcome of an attempt that has been started and has not ended. It
// is what the state of a task says while the executor works, so that a run crewflow
// was killed in the middle of leaves a record of where it was (docs/DESIGN.md §7).
const Running Kind = "running"

// The seven outcomes of a run, in the order crewflow works them out, which is the
// order of what says the most: a run that ran out of time tells nothing about what
// it was refused in its last second, and a run that was refused tells nothing about
// what it would have opened.
const (
	// Interrupted means a person stopped the run: crewflow was stopped with SIGINT
	// or SIGTERM, and it stopped the executor with it rather than leaving it to work
	// on without anyone (docs/DESIGN.md §7a). What the executor had done until then is
	// in its journal, and a run that goes on is a continuation of it.
	Interrupted Kind = "interrupted"
	// TimedOut means the run took longer than the project allows a run to take, and
	// was stopped: a hang is an outcome, not a wait (docs/DESIGN.md §7a).
	TimedOut Kind = "timeout"
	// BlockedPermission means the executor was refused a permission and stopped
	// there. Each refusal is a thing an orchestrator decides about, and the run is
	// not silently good news (docs/DESIGN.md §7a, §7d).
	BlockedPermission Kind = "blocked-permission"
	// Blocked means the executor stopped by itself and said why: it cannot read
	// the task, or it needs a secret or a package that is not there
	// (docs/DESIGN.md §7b, §7d).
	Blocked Kind = "blocked"
	// ExecutorFailed means the executor could not be started, or failed.
	ExecutorFailed Kind = "executor-failed"
	// NoChangeRequest means the run ended without opening the change request of its
	// branch, whatever it managed to do in the worktree.
	NoChangeRequest Kind = "no-change-request"
	// ChangeRequestOpened means the run opened the change request of its branch and
	// changed nothing it was not to change. It is the only outcome a run is for.
	ChangeRequestOpened Kind = "pr-opened"
	// OutOfScope means the run changed files the task was not to change. The work
	// is not reviewed before that is found out, and the change request is told
	// about anyway: it is where the work is (docs/DESIGN.md §7c).
	OutOfScope Kind = "out-of-scope"
)

// outcome is how the run came out, in the order the kinds above are written in, and
// the change request of the branch is put into the result when there is one to put
// it into: a run that opened a request and went outside the boundaries of the task
// is told about both, or a person would see an outcome and no work.
//
// The last question is what the run changed, and it is asked of git in the worktree
// and of the globs of the task itself (docs/DESIGN.md §7c).
func (r *runner) outcome(ctx context.Context, result Result, stdout, stderr []byte, code int, ended Kind) (Result, error) {
	rejections := r.profile.Rejections(stdout, stderr)
	reason := r.profile.Blocked(stdout)
	switch {
	case ended == Interrupted:
		// What the executor was refused and why it stopped are still facts about the
		// run, and they are in the result: a person who stopped the run goes on in
		// its session, and the refusals are what a continuation runs into again.
		result.Rejections, result.Reason = rejections, reason
		return result.spent(Interrupted), nil
	case ended == TimedOut:
		return result.spent(TimedOut), nil
	case len(rejections) > 0:
		result.Rejections = rejections
		return result.spent(BlockedPermission), nil
	case reason != "":
		result.Reason = reason
		return result.spent(Blocked), nil
	case code != 0:
		return result.spent(ExecutorFailed), nil
	}

	change, found, err := r.set.Forge.FindChangeRequest(ctx, r.branch)
	switch {
	case err != nil:
		return result, err
	case found:
		result.ChangeRequest = &change
	}

	outside, err := r.outside(ctx)
	if err != nil {
		return result, err
	}
	if len(outside) > 0 {
		result.Outside = outside
		return result.spent(OutOfScope), nil
	}
	if found {
		return result.spent(ChangeRequestOpened), nil
	}
	return result.spent(NoChangeRequest), nil
}

// outside are the files the run changed that the task was not to change. The files
// are the ones git holds in the worktree against the branch the task started from,
// not the ones the run said it wrote: a report is about the work, not the account
// of it (docs/DESIGN.md §7c).
func (r *runner) outside(ctx context.Context) ([]string, error) {
	changed, err := r.output(ctx, r.worktree, "diff", "--name-only", "origin/"+r.cfg.Project.DefaultBranch+"...HEAD")
	if err != nil {
		return nil, err
	}
	return task.Read(r.task.Body, r.cfg.Project.Language).Boundaries().Outside(lines(changed))
}

// lines are the paths of the answer of git, one per line, with the empty ones left
// out: git says nothing at all when nothing was changed.
func lines(out string) []string {
	var found []string
	for raw := range strings.Lines(out) {
		if line := strings.TrimSpace(raw); line != "" {
			found = append(found, line)
		}
	}
	return found
}

// spent is the result of a run that ended the way it says, with everything that was
// found out about it already in it.
func (r Result) spent(outcome Kind) Result {
	r.Outcome = outcome
	return r
}
