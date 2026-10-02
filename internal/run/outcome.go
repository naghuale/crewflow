package run

import (
	"context"
	"fmt"
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

// MaybeRunning is what a list of runs says of an attempt that says it is running and
// names no process to ask about: a state of before crewflow kept the number of the
// process of a run, of a run that has not been going for longer than any run may go
// on. It is not one of the outcomes of a run — nothing came of it — and it is not
// "running" either, because crewflow cannot tell that it is, and a report that says
// what it does not know is worth more than one that does not (docs/DESIGN.md §7).
const MaybeRunning Kind = "maybe-running"

// The seven outcomes of a run, in the order crewflow works them out, which is the
// order of what says the most: a run that ran out of time tells nothing about what
// it was refused in its last second, and a run that was refused tells nothing about
// what it would have opened.
const (
	// Stalled means a run that is going has shown nothing for longer than the silence
	// the project agreed to put up with, and is standing: a window of the keychain
	// nobody answered, an executor that hangs, a machine with no network. It is a
	// mark, and not an outcome of a run — the state of the task holds `running` all
	// the same and the attempt ends the way it would have ended — worked out every time
	// a list of runs is read, because a flag in a file that says nothing about the time
	// outlives the silence it was written for (docs/DESIGN.md §6, §7a, §7h).
	//
	// It is the outcome of one attempt only: the one crewflow stopped itself because
	// the run had stood and there was no reason of the wait to stand at, and that it is
	// going on in the same session. A run that stands with a provider that has answered
	// is a run whose agent has hung, and the three hangs of a day that cost a person a
	// night each were ended by hand (F-143, docs/DESIGN.md §7a).
	Stalled Kind = "stalled"
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
	// BlockedSecret means the executor reached for a place of the machine that stays
	// closed whatever the project wrote: a key, a token, a `.env`. It is an outcome of
	// its own and not a refusal among others, because it is the one refusal a run
	// never goes on from by itself and the one an orchestrator reads before anything
	// else in it (docs/DESIGN.md §7a.1, §7d, §8).
	BlockedSecret Kind = "blocked-secret"
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

// kinds is every outcome there is, in the order crewflow works them out, which is the
// order a list of runs counts and says them in: what says the most about a run first,
// and the ones that mean the same thing beside each other. A run that stands is a run
// that has not ended, and it is counted among the runs a machine is busy with all the
// same: a list of another project says "1 stalled" about a run that is going nowhere.
var kinds = []Kind{
	Running, Stalled, MaybeRunning, Interrupted, TimedOut, BlockedSecret, BlockedPermission, Blocked,
	ExecutorFailed, NoChangeRequest, ChangeRequestOpened, OutOfScope,
}

// outcome is how the run came out, in the order the kinds above are written in, and
// the change request of the branch is put into the result when there is one to put
// it into: a run that opened a request and went outside the boundaries of the task
// is told about both, or a person would see an outcome and no work.
//
// What the run reached for a secret is asked before anything else, and it is one answer
// with the refusals: the outcome of a run that reached one and the report of it are the
// same answer read twice (docs/DESIGN.md §7a.1, §7d, §8).
//
// The last question is what the run changed, and it is asked of git in the worktree
// and of the globs of the task itself (docs/DESIGN.md §7c).
func (r *runner) outcome(ctx context.Context, result Result, stdout, stderr []byte, code int, ended Kind,
	secrets findings) (Result, error) {
	// An attempt to reach a secret stops the run whatever ended the attempt and
	// whatever the habit of the command looks like: the refusals of such a run are
	// shown with the place, the kind of access and the fact that no run of it goes on
	// by itself (docs/DESIGN.md §7a.1, §7d, §8).
	result.Rejections = secrets.marked
	if len(secrets.reached) > 0 {
		return result.spent(BlockedSecret), nil
	}
	reason := r.profile.Blocked(stdout)
	// What the model provider said about the failure it stopped the run for is read
	// before anything the run said about itself: a provider that refused the run is a wait
	// for a resource and not a defect of the task, however the task of the run ended, and
	// an agent that writes BLOCKED: because its provider is down has not decided anything
	// about its task (F-119, docs/DESIGN.md §6a, §7a).
	provider, refused := refuseProvider(r.profile.ProviderFailure(stdout, stderr))
	switch {
	case ended == Interrupted:
		// What the executor was refused and why it stopped are still facts about the
		// run, and they are in the result: a person who stopped the run goes on in
		// its session, and the refusals are what a continuation runs into again.
		result.Reason = reason
		return result.spent(Interrupted), nil
	case ended == TimedOut:
		return result.spent(TimedOut), nil
	case ended == Stalled:
		// crewflow stopped the run itself because it had shown nothing for longer than
		// the silence of the project, and it is going on by itself in the same session.
		// The reason of the silence is in the journal of the attempt and in the state of
		// the task; there is nothing else to judge the run by (F-143, §7a).
		return result.spent(Stalled), nil
	case len(secrets.marked) > 0:
		return result.spent(BlockedPermission), nil
	case refused:
		// The words of the provider are in the reason of the run beside the name of the
		// wait: the name is the code of §6a that a program reads, and the words are what
		// a person reads when the queue says which resource of the machine the task
		// waits for (§6a, §7i).
		result.Reason = provider.reason + ": " + provider.said
		return result.spent(Blocked), nil
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
	// A run that pushed its branch and did not open a request is not over yet: the
	// work of the task is on the host and nobody asked about it, and a run in the mode
	// of the bot has an account that may open the request for it (docs/DESIGN.md §7i).
	opened, err := r.openRequest(ctx)
	if err != nil {
		// The request could not be opened. That is said on the way out of the run and
		// the run keeps the outcome it has: a request crewflow could not open is a
		// thing a person decides about, and the branch of the run is in the report.
		r.note(result.ErrorJournal, fmt.Errorf("the change request of the branch: %w", err))
		return result.spent(NoChangeRequest), nil
	}
	if opened != nil {
		result.ChangeRequest = opened
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
