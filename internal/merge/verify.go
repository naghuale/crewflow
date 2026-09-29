package merge

import (
	"context"
	"fmt"
	"time"

	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/gate"
	taskrun "github.com/naghuale/crewflow/internal/run"
)

// ciPoll is how often the checks of the branch of the host are asked about while they
// are going on: CI of a merge takes as long as the CI of a commit takes, and a
// question every ten seconds is enough to see it end without asking in a hurry.
const ciPoll = 10 * time.Second

// Verification is what a check after a merge found out, one line for each of the
// three things it asks about: where the branch of the host is, whether the task of the
// change is closed, and how the CI of that branch stands (docs/DESIGN.md §6, §7h).
type Verification struct {
	// Task is the task the change is of, Change the change request itself and URL
	// where a person reads it.
	Task   int    `json:"task,omitempty"`
	Change int    `json:"change"`
	URL    string `json:"url,omitempty"`
	// Head is the head of the change, which is the commit a fast-forward merge put
	// into the branch, and Merged the commit crewflow recorded as merged — what the
	// branch of the host is compared with, and the head where nothing was recorded.
	Head   string `json:"head,omitempty"`
	Merged string `json:"merged,omitempty"`
	// Branch is the branch the project merges into and Main the commit it is at, as
	// `git ls-remote` says it.
	Branch string `json:"branch,omitempty"`
	Main   string `json:"main,omitempty"`
	// TaskState is what the tracker holds about the task, and CI how the checks of the
	// branch stand: `pending` while they are going on, which is a wait and not a
	// refusal (docs/DESIGN.md §7h).
	TaskState string           `json:"task_state,omitempty"`
	CI        forge.CheckState `json:"ci,omitempty"`
	// Verified says that all three are in order, At is when that was found out and
	// Missing is every one of them that is not — a check that says "not verified" and
	// not why is a person looking at all of it by hand (docs/DESIGN.md §6).
	Verified bool     `json:"verified"`
	At       string   `json:"verified_at,omitempty"`
	Missing  []string `json:"missing,omitempty"`
	// Left is what the check could not write down, which does not make the merge any
	// less merged: the moment of the check is a fact of the process, and the branch of
	// the host is where it is whatever the state file says (docs/DESIGN.md §7h).
	Left []string `json:"left,omitempty"`
	// Note is what the check has to say about an answer that is in order, such as a
	// branch that has moved on since the merge and still holds the commit it merged.
	Note string `json:"note,omitempty"`
}

// Verify is `crewflow verify <PR>`: the check of a merge that has already been made.
// It asks the three questions that decide whether the cycle of a task closed — is the
// branch of the host at the commit that was merged, is the task closed, and is the CI
// of that branch green — and it says every one of them (docs/DESIGN.md §6).
//
// The CI of the branch is waited for while it is going on, up to the time limit the
// project gives it: a merge and the check of it are two moments of one thing, and the
// second of them has to wait for the first (docs/DESIGN.md §7h).
func Verify(ctx context.Context, deps Deps, number int) (Verification, error) {
	d := deps.whole()
	facts := gate.Collect(ctx, d.Gate, number)
	task := d.task(facts.Task)
	result := Verification{
		Task:      task,
		Change:    number,
		URL:       facts.URL,
		Head:      facts.Head,
		Merged:    d.merged(task, facts.Head),
		Branch:    d.Checkout.Branch,
		TaskState: d.stateOfTask(ctx, task),
	}
	if result.Merged == "" {
		result.Missing = append(result.Missing,
			"crewflow does not know which commit was merged: the change names no head and this machine kept no state of the task")
		return result, nil
	}

	main, err := d.Checkout.Head(ctx, d.Checkout.Branch)
	if err != nil {
		return Verification{}, err
	}
	result.Main = main
	result.didTheMergeGoIn(ctx, d, main)
	result.CI = d.ciOf(ctx, &result)
	if result.TaskState != "closed" {
		result.Missing = append(result.Missing,
			fmt.Sprintf("the task %d is %s, and the host closes it behind the change that is merged",
				task, listed(result.TaskState)))
	}
	if len(result.Missing) > 0 {
		return result, nil
	}
	result.Verified = true
	at := d.Now().UTC()
	result.At = at.Format(time.RFC3339)
	if err := d.verified(task, at); err != nil {
		result.Left = append(result.Left, err.Error())
	}
	return result, nil
}

// didTheMergeGoIn is whether the branch of the host holds the commit that was merged:
// it is at it, or it has moved on since and still holds it. A branch that is somewhere
// else has neither, and that is what a check after a merge is for (docs/DESIGN.md §7h).
func (v *Verification) didTheMergeGoIn(ctx context.Context, d *deps, main string) {
	if sameCommit(main, v.Merged) {
		return
	}
	holds, err := d.Checkout.Contains(ctx, v.Merged)
	switch {
	case err != nil:
		v.Missing = append(v.Missing,
			fmt.Sprintf("the branch of the host is at %s, and whether it holds the merged commit %s could not be read: %v",
				short(main), short(v.Merged), err))
	case holds:
		// The branch has moved on since the merge — another change went in after it —
		// and it holds the commit that was merged, which is what the merge was for.
		v.Note = fmt.Sprintf("%s of the host has moved on to %s and holds the merged %s",
			v.Branch, short(main), short(v.Merged))
	default:
		v.Missing = append(v.Missing,
			fmt.Sprintf("the branch of the host is at %s, and the merged commit %s is not in it",
				short(main), short(v.Merged)))
	}
}

// ciOf is how the checks of the branch of the host stand, and the waiting for them
// while they are going on: a red check of the branch of the project is the one thing
// that says the merge was not good after all, and a check that is still going on is
// not a refusal (docs/DESIGN.md §7h).
//
// A project that does not ask for the CI of its commits does not get a check of it
// here either: there is nothing that could have been found out, and saying so as a
// failure of the merge would be a refusal of nothing (docs/DESIGN.md §5).
func (d *deps) ciOf(ctx context.Context, result *Verification) forge.CheckState {
	if !d.Gate.RequireChecks {
		return forge.CheckNone
	}
	if d.Gate.CI == nil {
		result.Missing = append(result.Missing,
			fmt.Sprintf("this project has no CI crewflow can ask about the checks of %s of the host", d.Checkout.Branch))
		return forge.CheckNone
	}
	for waited := time.Duration(0); ; waited += ciPoll {
		asked, err := d.Gate.CI.Status(ctx, result.Main)
		switch {
		case err != nil:
			result.Missing = append(result.Missing,
				fmt.Sprintf("read the checks of the commit %s: %v", short(result.Main), err))
			return forge.CheckNone
		case asked != forge.CheckPending && asked != forge.CheckNone:
			if asked != forge.CheckSuccess {
				result.Missing = append(result.Missing,
					fmt.Sprintf("the checks of %s of the host are %s", d.Checkout.Branch, asked))
			}
			return asked
		case waited >= d.Timeout:
			result.Missing = append(result.Missing,
				fmt.Sprintf("the checks of %s of the host are %s after %s, and a check that is not green on the branch "+
					"of the project is not a merge crewflow may call good", d.Checkout.Branch, asked, d.Timeout))
			return asked
		}
		// The checks are still going on: the check after a merge waits for them and
		// says what it is waiting for, and a wait that is stopped with the command is a
		// wait that is over.
		if err := d.Sleep(ctx, ciPoll); err != nil {
			result.Missing = append(result.Missing, err.Error())
			return asked
		}
	}
}

// merged is the commit that was merged: the one crewflow recorded in the state of the
// task, and the head of the change where nothing was recorded — a fast-forward merge
// puts the head of the change into the branch and nothing else, so the head is the
// commit even for a merge that happened on another machine (docs/DESIGN.md §7h).
func (d *deps) merged(task int, head string) string {
	if task <= 0 {
		return head
	}
	state, err := taskrun.LoadState(taskrun.JournalsOf(d.Home, d.Repo).StatePath(task))
	if err != nil {
		return head
	}
	if state.MergedSHA != "" {
		return state.MergedSHA
	}
	return head
}

// verified writes the moment of the check into the state of the task, so that a list of
// the runs of the project says when a task of theirs was checked after its merge. A
// state that cannot be written is not a merge that failed: what the branch of the host
// holds stands whatever the state file says, and what could not be written down is
// said as the one thing that is left.
func (d *deps) verified(task int, at time.Time) error {
	if task <= 0 {
		return nil
	}
	path := taskrun.JournalsOf(d.Home, d.Repo).StatePath(task)
	state, err := taskrun.LoadState(path)
	if isNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("write the moment of the check into the state of task %d: %w", task, err)
	}
	state.VerifiedAt = &at
	if err := taskrun.SaveState(path, state); err != nil {
		return fmt.Errorf("write the state of task %d: %w", task, err)
	}
	return nil
}

// stateOfTask is what the tracker holds about the task, and nothing where it cannot be
// asked or the task is not known: a check after a merge does not go on because the
// tracker is down, and it says what it could not find out.
func (d *deps) stateOfTask(ctx context.Context, task int) string {
	if d.Tracker == nil || task <= 0 {
		return ""
	}
	found, err := d.Tracker.Task(ctx, task)
	if err != nil {
		return ""
	}
	return found.State
}

// listed is a state of a task as a line of a report reads it, and "not known" where
// there is none to show.
func listed(state string) string {
	if state == "" {
		return "not known"
	}
	return state
}
