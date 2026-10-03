package run

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// reasonApproval is what a run that stood in front of the window of the keychain of
// macOS and was not answered in it is called in the answer of `crewflow task run`, in
// its journal and in a report of a person. It is a name of its own and not a sentence,
// because an orchestrator reads names first and the sentence is what a person reads when
// the name has told them where to look (docs/DESIGN.md §7i).
const reasonApproval = "keychain-approval"

// blockedOnApproval is what a run does when it stood in front of the window of the
// keychain of macOS and nobody answered it. The executor was never started and the work
// of the task is untouched, but the run happened: it waited, it is over, and a task
// that is waiting for a person is a thing an orchestrator has to see (§7i).
//
// It is the one attempt of §7 that is written down without an executor having run. Every
// other refusal of the identity of a run leaves nothing behind, because there is nothing
// to go on with; this one leaves an attempt in `blocked` with the reason of it, because
// the run stood in front of a window of the owner of the machine for two whole minutes
// and a list of runs that said nothing about that is a list of runs that hides why a
// task has not started.
//
// The attempt is the one the run wrote into the state of the task before it went to the
// keychain, because a run that stands in front of that window is a run that has to be
// visible while it stands there (F-039, §6). What it stood at — the key of the App of
// the host — is what the state of the task says it was standing at, and it is what a
// list of runs shows as the reason of the silence.
//
// The state of the task holds the point to go on from as well (§7i): a person who comes
// back to a run that stood in front of a window of the system does the one thing that is
// his to do in it and goes on with `crewflow task resume`, and a run that was made to be
// started again by hand because of a window of macOS is what F-091 is about.
func (r *runner) blockedOnApproval(ctx context.Context, files *AttemptFiles, err error) (Result, error) {
	at := r.env.Now()
	state, loadErr := LoadState(r.journals.StatePath(r.task.Number))
	if loadErr != nil {
		_ = files.takeAway()
		return Result{}, loadErr
	}
	attempt := r.own(state)
	// What the run is asking for, and the point to go on from, are said before the state
	// is written: both are what the journal of the attempt holds and what a continuation
	// is checked against, and a run that cannot say the head of its branch writes no
	// point — a point crewflow cannot check later is not a point to go on from.
	point, pointErr := r.pointFor(ctx, state)
	if pointErr != nil {
		r.note(files.ErrorJournal, pointErr)
	} else {
		r.pointedAt(point, WaitTimeout)
		point.sayEvent(files.Out, EventAuthorizationRequired)
		point.sayEvent(files.Out, EventAuthorizationTimeout)
	}
	// What the attempt ended with is written onto the state as it is there now, under the
	// lock of the task and into the attempt of this run by its number: what another command
	// wrote while this run stood in front of the window of the system is not this run's to
	// take away, and the end of this attempt is not to be written into the attempt of
	// another run (D-068 FINDING-4, D-068 RECHECK-FINDING-4).
	if _, keepErr := UpdateState(r.journals.StatePath(r.task.Number), func(current State) (State, error) {
		// The name of the run is the mode of the file of the project: a run that was
		// refused the key of the App is a run of the mode of the bot, and the state says
		// so wherever the wait ended (§7i).
		current = current.Identified(r.attempt, Identity{Mode: r.cfg.Identity.Mode}).Ended(r.attempt, at, Blocked)
		if r.point != nil {
			current.Checkpoint = r.point
		}
		return current, nil
	}); keepErr != nil {
		_ = files.takeAway()
		return Result{}, keepErr
	}
	result := r.resultOf(attempt, at, files)
	result.Reason = fmt.Sprintf("%s: %v", reasonApproval, err)
	// The reason is on the way out of the run in the same words as in the report of it: a
	// person who reads the journal of the attempt afterwards is reading it when the window
	// has closed and the owner has gone back to what they were doing (§7i).
	result.ErrorJournal = r.noteError(files.ErrorJournal, errors.New(result.Reason))
	if closeErr := files.Close(); closeErr != nil {
		return result, closeErr
	}
	return result.spent(Blocked), nil
}

// deniedOnAuthorization is what a run that went on from the point of the task does when
// the person refuses the window of the system: the work of the task stays in the worktree
// it was done in, the attempt ends here as `blocked` with the words of the machine, and
// the point keeps the refusal — so that the next continuation says that it was refused
// instead of asking the same person the same question again (§7i).
//
// crewflow does not decide what a refusal of the keychain means: what it knows for
// certain is that the answer came and was not "yes", and the words of macOS go into the
// state and the journal as they are. What a refusal is refused for is the *continuation*,
// not the task: a person who needs the task done signs this build or runs it in the mode
// of the owner, and both are decisions of the owner (R4, §7f).
func (r *runner) deniedOnAuthorization(files *AttemptFiles, err error) (Result, error) {
	at := r.env.Now()
	state, loadErr := LoadState(r.journals.StatePath(r.task.Number))
	if loadErr != nil {
		_ = files.takeAway()
		return Result{}, loadErr
	}
	attempt := r.own(state)
	r.pointedAt(r.point, WaitDenied)
	r.point.sayEvent(files.Out, EventAuthorizationDenied)
	// The outcome of the attempt and the refusal of the person are written onto the state
	// as it is there now, under the lock of the task and into the attempt of this run by
	// its number (D-068 FINDING-4, D-068 RECHECK-FINDING-4).
	if _, keepErr := UpdateState(r.journals.StatePath(r.task.Number), func(current State) (State, error) {
		current = current.Ended(r.attempt, at, Blocked)
		if r.point != nil {
			current.Checkpoint = r.point
		}
		return current, nil
	}); keepErr != nil {
		_ = files.takeAway()
		return Result{}, keepErr
	}
	result := r.resultOf(attempt, at, files)
	result.Reason = fmt.Sprintf("%s: %v", reasonApproval, err)
	result.ErrorJournal = r.noteError(files.ErrorJournal, errors.New(result.Reason))
	if closeErr := files.Close(); closeErr != nil {
		return result, closeErr
	}
	return result.spent(Blocked), nil
}

// pointFor is the point a run writes when it stopped in front of a decision of a person:
// the step to go on from, the head of the branch the run stood at, the task as crewflow
// read it, and the three facts of the request — where it is asked, of which resource of
// the project and for what action (§7i).
//
// A new point is written over the one before it and not kept next to it: a run of a task
// stops once at a decision of a person, and the point that matters is the one the last
// run of the task stopped at. The head and the task of it are read again, so a point that
// was written before the branch moved is refused by the continuation and not by a guess
// here (docs/DESIGN.md §7i).
func (r *runner) pointFor(ctx context.Context, state State) (*Checkpoint, error) {
	head, err := r.head(ctx, state.Worktree)
	if err != nil {
		return nil, err
	}
	return &Checkpoint{
		Task:       r.task.Number,
		Step:       stepReadKey,
		Head:       head,
		Assignment: fingerprint(r.task),
		At:         r.env.Now(),
		Channel:    ChannelKeychain,
		Resource:   SubjectExecutorKey,
		Action:     ActionHumanExecute,
	}, nil
}

// pointedAt is the point of the run with the answer of the request written into it, and it
// is kept on the run itself: the report of the run, the state of the task and the refusal
// of the next continuation all read the same fact, and three copies of it would be three
// things that can disagree (§7h).
func (r *runner) pointedAt(point *Checkpoint, outcome string) {
	if point == nil {
		return
	}
	if outcome == WaitTimeout {
		// A wait that came out again is a new request of the same person, and the
		// point is written afresh: a person who comes back tomorrow to a point of
		// yesterday is a person who comes back to a refusal (§7i).
		point.At = r.env.Now()
	}
	point.Outcome, point.DecidedAt = outcome, r.env.Now()
	r.point = point
}

// answered is what came of the request the run was stopped at written into the point of
// the run, and the event of it written into the journal of the attempt while it is open. A
// run that was not going on from a point has no request to answer, and its point stays as
// it was (§7i).
func (r *runner) answered(files *AttemptFiles, outcome, event string) {
	if r.point == nil {
		return
	}
	point := *r.point
	point.Outcome, point.DecidedAt = outcome, r.env.Now()
	r.point = &point
	point.sayEvent(files.Out, event)
}

// takeRunAway is what a run does with the worktree and the branch it made when it is cut
// off before the executor was started: the keychain of macOS refused to let this program
// read the key of the App, and nothing was done in the worktree of the task, nothing was
// pushed and nothing is written down that points at either of them. What is left is a
// folder and a branch that the next `task run` refuses ("the worktree … is already there:
// run it again with -continue") and that `-continue` cannot reach, because the state of
// the task has no attempt in it (F-048, docs/DESIGN.md §7i).
//
// A continuation is not touched: the folder it went on in holds the work of the attempt
// before it, and that work is the work of the task.
func (r *runner) takeRunAway(ctx context.Context) error {
	if r.req.Continue != "" {
		return nil
	}
	var problems []error
	if _, err := os.Stat(r.worktree); err == nil {
		// The folder of the worktree and the record of it in the repository go together:
		// `git worktree remove` takes both, and `--force` is here because a run that
		// does not start its executor has nothing in the folder to lose.
		if err := r.git(ctx, r.repoDir(), "worktree", "remove", "--force", r.worktree); err != nil {
			problems = append(problems, err)
		}
	} else if !os.IsNotExist(err) {
		problems = append(problems, fmt.Errorf("the worktree %s: %w", r.worktree, err))
	}
	// The branch of the task is of the clone of the person and nothing of it was pushed:
	// the executor was never started. `git branch -D` fails on a branch that is not
	// there, and a machine where somebody took the branch away by hand is a machine where
	// a run has to take what is left and say nothing about the rest.
	listed, err := r.output(ctx, r.repoDir(), "branch", "--list", r.branch)
	switch {
	case err != nil:
		problems = append(problems, err)
	case strings.TrimSpace(listed) != "":
		if err := r.git(ctx, r.repoDir(), "branch", "-D", r.branch); err != nil {
			problems = append(problems, err)
		}
	}
	return errors.Join(problems...)
}

// resultOf is the answer of a run for everything a report shows about it whatever
// stopped it: the task, the branch, the worktree, the attempt, whose name the executor
// worked under and the two files of the attempt. What the executor did and how the run
// came out of it is what the caller adds (§7, §7i).
func (r *runner) resultOf(attempt Attempt, ended time.Time, files *AttemptFiles) Result {
	return Result{
		Task:         r.task.Number,
		Title:        r.task.Title,
		Branch:       r.branch,
		Worktree:     r.worktree,
		Profile:      r.profile.Name(),
		Session:      r.session,
		Attempt:      attempt.Number,
		Continued:    r.req.Continue != "",
		Resumed:      r.req.Resume,
		Checkpoint:   r.point,
		AutoResumed:  r.auto,
		Identity:     Identity{Mode: r.identity.Mode, Description: r.identity.Description},
		StartedAt:    attempt.StartedAt,
		EndedAt:      ended,
		Journal:      files.Journal,
		ErrorJournal: files.ErrorJournal,
		// The record of the pair is what let this run start beside another one, and a
		// report of the run says it: a run that went beside another one is a run somebody
		// decided to let it go (docs.DESIGN.md §7c).
		Admission: r.admission,
	}
}
