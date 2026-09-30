package run

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/naghuale/crewflow/internal/forge"
)

// reasonApproval is what a run that stood in front of the window of the keychain of
// macOS and was not answered in it is called in the answer of `crewflow task run`, in
// its journal and in a report of a person. It is a name of its own and not a sentence,
// because an orchestrator reads names first and the sentence is what a person reads when
// the name has told them where to look (docs/DESIGN.md §7i).
const reasonApproval = "keychain-approval"

// nextAttempt is the number the attempt crewflow is about to make gets in the state of
// the task, which is what the names of the two files of the attempt are made of. The
// state is not written yet — whose name the run goes under has to be in it — so the
// number is worked out of the state that is on the machine, and the journal of the
// attempt is opened before crewflow goes to the store of secrets for the key of the App:
// that call can make macOS ask the owner in a window of the system, and a run that stands
// in front of that window has to be writing its journal while it waits (§7i).
func (r *runner) nextAttempt() (int, error) {
	state, err := LoadState(r.journals.StatePath(r.task.Number))
	if err != nil {
		if os.IsNotExist(err) {
			return 1, nil
		}
		return 0, err
	}
	return len(state.Attempts) + 1, nil
}

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
func (r *runner) blockedOnApproval(files *AttemptFiles, err error) (Result, error) {
	// Whose name the run was going to work under is what the state of the task says
	// even where the run never got there: a project in the mode of the bot that was
	// refused its key is a run of the mode of the bot (§7i).
	r.identity = forge.Identity{Mode: r.cfg.Identity.Mode}
	ended := r.env.Now()
	state := r.stateOf(ended)
	attempt := state.Attempts[len(state.Attempts)-1]
	if keepErr := SaveState(r.journals.StatePath(r.task.Number), state.Ended(ended, Blocked)); keepErr != nil {
		_ = files.takeAway()
		return Result{}, keepErr
	}
	result := r.resultOf(attempt, ended, files)
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
		AutoResumed:  r.auto,
		Identity:     Identity{Mode: r.identity.Mode, Description: r.identity.Description},
		StartedAt:    attempt.StartedAt,
		EndedAt:      ended,
		Journal:      files.Journal,
		ErrorJournal: files.ErrorJournal,
	}
}
