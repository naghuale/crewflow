// Package merge merges one change into the default branch of its project, and proves
// what came of it (docs/DESIGN.md §6, §7h).
//
// A merge here is a fast-forward of exactly one commit: the head of the change the
// gate has just judged, and nothing else. The gate is asked again in the moment of
// the merge, and nothing is pushed unless it says the change may go in, so that a
// verdict cannot go out of date between the approval and the push (docs/DESIGN.md §7h).
//
// What a merge came to is not read out of the code `git push` exited with: the branch
// of the host is asked afterwards where it points, and only that answer is what the
// outcome is worked out from. Every command of git the merge ran and everything git
// wrote about it is kept in the journal of the merge, because a person who is told
// that a merge was refused has to be able to see what git said (docs/DESIGN.md §6).
//
// Nothing after the merge can undo it, and nothing after it is a refusal: the task
// of the change is waited for and closed where the host left it open, the commit that
// was merged is recorded in the state of the task, and the worktree of the task is
// taken away. What could not be done there is said as what is left to do by hand, and
// the merge itself still stands (docs/DESIGN.md §7h).
package merge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/gate"
	taskrun "github.com/naghuale/crewflow/internal/run"
)

// The words a merge comes to, and the code each of them leaves the command with.
// Every word but `refused` says that the approved commit is in the default branch of
// the host: what happened around the merge is in the outcome beside it, because a
// merge that went through cannot be called off by anything that came after it
// (docs/DESIGN.md §7h).
const (
	// Merged says that the default branch of the host is the commit the gate
	// approved, that the task of the change is closed and that the worktree of the
	// task is taken away.
	Merged Outcome = "merged"
	// AlreadyMerged says that the default branch of the host holds the head of the
	// change already and that there was nothing to push: the same merge asked for a
	// second time is not a refusal of anything.
	AlreadyMerged Outcome = "already-merged"
	// MergedWithCleanupWarning says that the merge went through and that what came
	// after it did not: the state of the task or the worktree of it is left for a
	// person to tidy by hand.
	MergedWithCleanupWarning Outcome = "merged-with-cleanup-warning"
	// Refused says that the change may not be merged, and Reason of the table of
	// docs/DESIGN.md §7h is the one reason why. Nothing was pushed.
	Refused Outcome = "refused"
)

// Outcome is how a merge ended, and one of the four words above and nothing else.
type Outcome string

// ClosedBy is who closed the task of a change, and one of the two words below: a
// change that has gone in leaves a task behind it, and the task is either closed by
// the host behind the change or by the merge itself. A merge that names neither has
// left a task open, and a person has to close it by hand (docs/DESIGN.md §6, §7h).
type ClosedBy string

const (
	// ClosedByHost says that the host closed the task behind the change that went in,
	// as it usually does, and that crewflow did not write to the tracker at all.
	ClosedByHost ClosedBy = "host"
	// ClosedByCrewflow says that the host did not close the task in the time the merge
	// waited for it, and that the merge closed it with a record under it.
	ClosedByCrewflow ClosedBy = "crewflow"
)

// TaskClosureTimeout is how long a merge waits for the task of a change to be closed
// on the host: the host closes an issue behind a change that has been merged, and a
// merge that does not wait for it cannot say whether it did — and closes the task
// itself where the host did not, because a task left open behind a change that went
// in is a cycle nobody finished (docs/DESIGN.md §7h).
const TaskClosureTimeout = time.Minute

// taskClosedPoll is how often the merge asks the tracker about the task of the change
// while it waits: a host closes an issue within seconds of a merge, and a question
// every two of them is enough to see it happen and not to read the host to pieces.
const taskClosedPoll = 2 * time.Second

// Deps is everything a merge is made of: the roles of the project, the checkout it
// pushes from, the task the change is of and the machine it waits on. It is passed in
// whole, so that a test of a merge runs against a host, a git and a clock of its own
// and reaches neither the network nor a repository of the person who runs it.
type Deps struct {
	// Gate gathers and judges the facts of the change: the roles of the project, the
	// boundaries of its task and the checkout its history is read in. [Run] fills the
	// checkout and the task of it in, so that the gate and the merge always ask the
	// same git about the same history.
	Gate gate.Deps
	// Checkout is the repository the merge pushes from and asks the branch of the
	// host about.
	Checkout Git
	// Tracker is where the task of the change is read from, and — where the host did
	// not close it behind the change that went in — closed through: a merge that ends
	// with the task open is a cycle a person has to finish by hand, and a check after
	// the merge cannot confirm that merge because of it (docs/DESIGN.md §7h).
	Tracker forge.Tracker
	// Task is the task the change is of: its state is where the commit that was
	// merged is recorded and its worktree is what is taken away afterwards. Zero is a
	// change of a task this machine kept no state of, and there is then nothing to
	// record and nothing to tidy up.
	Task int
	// Worktree is the checkout of the task, empty when there is none to take away.
	Worktree string
	// Home and Repo are the root crewflow keeps its own files under and the project
	// they are kept for (docs/DESIGN.md §7).
	Home string
	Repo string
	// Timeout is how long the merge waits for the task of the change to be closed,
	// Sleep how it waits between the questions about it, and Now the clock the
	// journal and the state of the task are written with.
	Timeout time.Duration
	Sleep   func(ctx context.Context, d time.Duration) error
	Now     func() time.Time
}

// Result is everything a person and an orchestrator are told about a merge: which
// change it was of, which commit it was about, how it came out, and what is left of
// what was to be done around it.
type Result struct {
	// Task is the task the change is of, Change the change request itself and URL
	// where a person reads it.
	Task   int    `json:"task,omitempty"`
	Change int    `json:"change"`
	URL    string `json:"url,omitempty"`
	// Head is the commit at the head of the change, which is the commit a merge of it
	// is about, and Branch the branch of the project it goes into.
	Head   string `json:"head,omitempty"`
	Branch string `json:"branch,omitempty"`
	// Outcome is how the merge came out and Verdict the answer of the gate about the
	// change, which is what a refusal names.
	Outcome Outcome      `json:"outcome"`
	Verdict gate.Verdict `json:"verdict"`
	// MergedSHA is the commit the default branch of the host is at, once the merge is
	// over: the answer of `git ls-remote`, and not the code `git push` exited with.
	MergedSHA string `json:"merged_sha,omitempty"`
	// TaskClosed says that the task of the change is closed, TaskClosedBy who closed
	// it — the host behind the change that went in, or crewflow where the host left
	// the task open — and Left is what is left to do by hand when the merge went
	// through and what came after it did not.
	TaskClosed   bool     `json:"task_closed,omitempty"`
	TaskClosedBy ClosedBy `json:"task_closed_by,omitempty"`
	Left         []string `json:"left,omitempty"`
	// Journal is where the facts, every command of git and what it wrote are.
	Journal string `json:"journal"`
}

// OK is whether the approved commit is in the default branch of the host, whatever
// became of everything around the merge: a merge that went through is a merge that
// went through, and only a change that may not be merged at all leaves the command
// with a code that is not zero (docs/DESIGN.md §6).
func (r Result) OK() bool {
	switch r.Outcome {
	case Merged, AlreadyMerged, MergedWithCleanupWarning:
		return true
	default:
		return false
	}
}

// Run is `crewflow merge <PR>`: it gathers the facts of the change from the host and
// from git, hands them to the gate, and merges the change only if the gate says it
// may go in — a fast-forward of the default branch to exactly the head it approved.
//
// What the merge came out as is worked out from the branch of the host, not from the
// code `git push` exited with: a push git accepted and a push it refused both end in
// a code that means nothing on its own, and only `git ls-remote` says where the
// branch points (docs/DESIGN.md §7h).
func Run(ctx context.Context, deps Deps, number int) (Result, error) {
	d := deps.whole()
	journal, err := OpenJournal(d.mergeJournal(number), d.Now)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = journal.Close() }()
	// Every command of git a merge runs is in its journal, the ones of the gate
	// included: what a person reads afterwards is what git said and not the account
	// crewflow keeps of it (docs/DESIGN.md §6).
	d.Checkout.Run = journal.logging(d.Checkout.Run)
	d.Gate.Git = d.Checkout.History()

	facts := gate.Collect(ctx, d.Gate, number)
	verdict := gate.Evaluate(facts)
	if err := journal.Gate(gate.Summarize(facts, verdict)); err != nil {
		return Result{}, err
	}
	result := Result{
		Task:    d.task(facts.Task),
		Change:  number,
		URL:     facts.URL,
		Head:    facts.Head,
		Branch:  d.Checkout.Branch,
		Outcome: Refused,
		Verdict: verdict,
		Journal: journal.Path(),
	}

	// A change that is in the default branch already is not refused, whatever the host
	// says about it now: the host has closed a change it merged, and the gate calls
	// that `pr-not-open`. The git is asked before the verdict is acted on, because
	// "already merged" and "may not be merged" are both answers of a second merge and
	// only one of them is a refusal (docs/DESIGN.md §7h).
	if facts.Head != "" {
		merged, err := d.Checkout.Contains(ctx, facts.Head)
		if err != nil {
			return d.unreadable(journal, result, err)
		}
		if merged {
			result.Outcome = AlreadyMerged
			result.MergedSHA = facts.Head
			return result, journal.Outcome(result)
		}
	}
	if !verdict.Ready {
		return result, journal.Outcome(result)
	}

	// What the branch of the host is at before the push: a push git refuses is told
	// apart from one that arrived too late by asking the branch again, and not by
	// reading the words of git (docs/DESIGN.md §7h).
	before, err := d.Checkout.Head(ctx, d.Checkout.Branch)
	if err != nil {
		return d.unreadable(journal, result, err)
	}
	if err := d.Checkout.Push(ctx, facts.Head); err != nil {
		return d.refused(journal, result, d.rejection(ctx, facts.Head, before, err))
	}
	// The outcome of a merge is what the branch of the host points at afterwards, and
	// not what the push said: a host that accepted the push and put the branch
	// somewhere else is a merge that did not happen (docs/DESIGN.md §7h).
	after, err := d.Checkout.Head(ctx, d.Checkout.Branch)
	if err != nil {
		return d.unreadable(journal, result, err)
	}
	result.MergedSHA = after
	if !sameCommit(after, facts.Head) {
		return d.refused(journal, result, gate.Verdict{
			Reason: gate.VerifyMismatch,
			Detail: fmt.Sprintf("the push was accepted, and %s of the host is at %s and not at the approved %s: "+
				"read the journal of the merge, take the branch where it has to be by hand",
				d.Checkout.Branch, short(after), short(facts.Head)),
		})
	}

	// The task of the change is closed only now: the branch of the host is at the
	// approved commit, and a task closed before that would be a task closed for a merge
	// that did not happen (docs/DESIGN.md §7h).
	left := d.closedTask(ctx, journal, &result)
	result = d.after(ctx, result, journal, left)
	return result, journal.Outcome(result)
}

// closedTask is the task of the change after the commit is in the branch of the host:
// the merge waits for the host to close it, and closes it itself where the host did not
// do it in the time it was given. A host does not close an issue behind every change it
// merged — it did not for #48 of this project on 30.09 — and a task left open behind a
// change that went in is a cycle a person has to finish by hand, and a check after the
// merge answers «not verified» about a merge that did go in (docs/DESIGN.md §6, §7h).
//
// Only the task the change itself names in its `Closes #N` is closed, and only where
// the tracker of the project can close a task at all: a task crewflow closes is one a
// person will not look at again, and the two facts that make it safe are the line of the
// change and the fact that the branch of the host is at the approved commit.
//
// What could not be closed is what is left to do by hand, and it is what the caller is
// given.
func (d *deps) closedTask(ctx context.Context, journal *Journal, result *Result) []string {
	if d.taskClosed(ctx, journal, result.Task) {
		result.TaskClosed, result.TaskClosedBy = true, ClosedByHost
		return nil
	}
	closer, is := d.Tracker.(forge.TaskCloser)
	if !is {
		return nil
	}
	number, is := d.taskOfTheChange(ctx, journal, result.Change)
	if !is {
		return nil
	}
	// The change is what the host closes tasks by, and it is what a person reads on
	// the host: where it names another task than the one this machine kept state of,
	// the change wins and the difference is said out loud.
	if result.Task > 0 && result.Task != number {
		_ = journal.Note(fmt.Errorf("the change request #%d names the task #%d, and this machine kept the state of the task #%d",
			result.Change, number, result.Task))
	}
	result.Task = number
	if err := closer.CloseTask(ctx, number, closingOf(*result)); err != nil {
		left := fmt.Sprintf("close the task #%d by hand: %v", number, err)
		_ = journal.Note(errors.New(left))
		return []string{left}
	}
	result.TaskClosed, result.TaskClosedBy = true, ClosedByCrewflow
	_ = journal.Note(fmt.Errorf("the host did not close the task %d in %s, and crewflow closed it",
		number, d.Timeout))
	return nil
}

// taskOfTheChange is the task the change names in its own `Closes #N`, and nothing else
// crewflow kept: a change merged on another machine has no state here, and the line in
// its body is what still says which task it was of (docs/DESIGN.md §7h).
func (d *deps) taskOfTheChange(ctx context.Context, journal *Journal, number int) (int, bool) {
	if d.Gate.Forge == nil {
		return 0, false
	}
	change, err := d.Gate.Forge.ChangeRequest(ctx, number)
	if err != nil {
		_ = journal.Note(fmt.Errorf("read the change request #%d to find the task to close: %w", number, err))
		return 0, false
	}
	task, is := gate.TaskOf(change.Body)
	if !is {
		_ = journal.Note(fmt.Errorf("the change request #%d names no task in `Closes #N`, so crewflow closes none", number))
		return 0, false
	}
	return task, true
}

// closingOf is the record crewflow leaves under a task it closed: the change that went
// in, the commit it went in with, and the hand that closed the task. A person who reads
// the task afterwards is not looking at a report of a merge, and has to be able to see
// why the task is closed without asking anybody (docs/DESIGN.md §7h).
func closingOf(result Result) string {
	return fmt.Sprintf("влита #%d, %s, закрыта crewflow", result.Change, short(result.MergedSHA))
}

// rejection is the reason a push git refused is refused with: the branch of the host
// has moved on since it was last read, and the merge arrived too late — that is
// `not-fast-forward` — or the branch is where it was and the host said no, which is
// `push-rejected`.
//
// A branch that cannot be read at all leaves nothing to tell the two apart, and that
// is `forge-unavailable` rather than one of the two guesses (docs/DESIGN.md §7h).
func (d *deps) rejection(ctx context.Context, commit, before string, pushErr error) gate.Verdict {
	now, err := d.Checkout.Head(ctx, d.Checkout.Branch)
	switch {
	case err != nil:
		return gate.Verdict{
			Reason: gate.ForgeUnavailable,
			Detail: fmt.Sprintf("git refused the push and the branch of the host cannot be read to tell why: %v", pushErr),
		}
	case !sameCommit(now, before):
		return gate.Verdict{
			Reason: gate.NotFastForward,
			Detail: fmt.Sprintf("%s of the host moved on to %s while the merge was going, so it is not a fast-forward: "+
				"rebase the branch of the change onto %s and approve it again", d.Checkout.Branch, short(now), d.Checkout.Branch),
		}
	default:
		return gate.Verdict{
			Reason: gate.PushRejected,
			Detail: fmt.Sprintf("the host refused the push of %s to %s: %v", short(commit), d.Checkout.Branch, pushErr),
		}
	}
}

// after is everything that comes once the commit is in the branch of the host: the
// commit is written into the state of the task, so that a check after the merge has
// something to compare the branch with, and the worktree of the task is taken away.
//
// Neither can call the merge off, and both are said in the outcome: a merge that went
// through with a worktree nobody took away is a merge that went through
// (docs/DESIGN.md §7h). What is left of the task of the change is what came before
// this, and it is said here as well.
func (d *deps) after(ctx context.Context, result Result, journal *Journal, left []string) Result {
	if result.Task > 0 {
		if err := d.remember(result); err != nil {
			left = append(left, err.Error())
		}
	}
	if d.Worktree != "" {
		if err := d.Checkout.RemoveWorktree(ctx, d.Worktree); err != nil {
			_ = journal.Note(err)
			left = append(left, fmt.Sprintf("take the worktree %s of the task away by hand: %v", d.Worktree, err))
		}
	}
	result.Outcome, result.Left = Merged, left
	if len(left) == 0 {
		return result
	}
	_ = journal.Note(errors.New(strings.Join(left, "; ")))
	result.Outcome = MergedWithCleanupWarning
	return result
}

// remember writes the commit that was merged into the state of the task: the fact a
// check after the merge compares the branch of the host with, and the fact a list of
// the runs of the project says the task is merged at (docs/DESIGN.md §7h).
func (d *deps) remember(result Result) error {
	path := taskrun.JournalsOf(d.Home, d.Repo).StatePath(result.Task)
	state, err := taskrun.LoadState(path)
	if isNotExist(err) {
		// A change merged on another machine has no state here, and a merge does not
		// invent one: there is nothing of a run of this machine to add to.
		return nil
	}
	if err != nil {
		return fmt.Errorf("write the commit %s into the state of task %d: %w", short(result.MergedSHA), result.Task, err)
	}
	state.MergedSHA = result.MergedSHA
	if err := taskrun.SaveState(path, state); err != nil {
		return fmt.Errorf("write the state of task %d: %w", result.Task, err)
	}
	return nil
}

// unreadable is the refusal of a machine whose git could not answer: the branch of the
// host is not read, and a merge that cannot read it pushes nothing and calls itself
// nothing else. It is `forge-unavailable`, which is the refusal of everything unknown —
// a machine that cannot start git included (docs/DESIGN.md §7h).
func (d *deps) unreadable(journal *Journal, result Result, err error) (Result, error) {
	return d.refused(journal, result, gate.Verdict{
		Reason: gate.ForgeUnavailable,
		Detail: fmt.Sprintf("%v: the branch of the host could not be read, so nothing may be merged", err),
	})
}

// refused is the result of a merge that was not merged, with the one reason of the
// table of docs/DESIGN.md §7h in it.
func (d *deps) refused(journal *Journal, result Result, verdict gate.Verdict) (Result, error) {
	result.Outcome, result.Verdict, result.MergedSHA = Refused, verdict, ""
	return result, journal.Outcome(result)
}

// taskIsClosed waits for the host to close the task of the change, up to the time
// limit of the merge: a host closes an issue behind a change that has gone in, and
// the merge is what waits for it rather than a person going to look.
//
// A tracker that cannot be asked, or an issue that is still open when the waiting is
// over, is not a refused merge: the commit is in the branch of the host either way,
// and both are said in the journal of the merge (docs/DESIGN.md §7h).
func (d *deps) taskClosed(ctx context.Context, journal *Journal, number int) bool {
	if d.Tracker == nil || number <= 0 {
		return false
	}
	for waited := time.Duration(0); ; waited += taskClosedPoll {
		found, err := d.Tracker.Task(ctx, number)
		switch {
		case err != nil:
			_ = journal.Note(fmt.Errorf("read task %d: %w", number, err))
			return false
		case found.State == "closed":
			return true
		case waited >= d.Timeout:
			_ = journal.Note(fmt.Errorf("task %d is %s and not closed after %s", number, found.State, d.Timeout))
			return false
		}
		if err := d.Sleep(ctx, taskClosedPoll); err != nil {
			_ = journal.Note(err)
			return false
		}
	}
}

// task is the task a merge is of: the one it was told about, and the one the change
// names when the caller knows none — a change a run of another machine opened still
// says which task it is of, and its state is where the merge records the commit.
func (d *deps) task(fromChange int) int {
	if d.Task > 0 {
		return d.Task
	}
	return fromChange
}

// whole is the merge with everything the machine gives it by default, so that a
// caller that has no clock of its own has a merge that does not need one.
func (d Deps) whole() *deps {
	whole := deps{d}
	if whole.Now == nil {
		whole.Now = time.Now
	}
	if whole.Sleep == nil {
		whole.Sleep = sleep
	}
	if whole.Timeout <= 0 {
		whole.Timeout = TaskClosureTimeout
	}
	if whole.Checkout.Run == nil {
		whole.Checkout.Run = refusedRun
	}
	if whole.Checkout.Branch == "" {
		whole.Checkout.Branch = whole.Gate.DefaultBranch
	}
	return &whole
}

// mergeJournal is the file the merge writes: the one of the task the change is of, or
// the one of the change itself where the task is not known — a change of a task this
// machine never ran leaves a journal as well, and it is named after what a person can
// ask for (docs/DESIGN.md §7h).
func (d *deps) mergeJournal(number int) string {
	task := d.task(d.Gate.Task)
	if task <= 0 {
		task = number
	}
	return taskrun.JournalsOf(d.Home, d.Repo).MergeJournalPath(task)
}

// deps is a merge with the machine of it filled in, so that the steps of a merge are
// methods of one thing and the shape of the answers of the package does not grow with
// the number of them.
type deps struct{ Deps }

// sleep is how a merge waits between the questions it asks the host, and a wait that
// is stopped with the command is a wait that is over.
func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// refusedRun is what a machine that cannot start git answers: a merge that cannot ask
// git cannot say whether the change may go in, and it would push nothing on a guess
// (docs/DESIGN.md §7h).
func refusedRun(_ context.Context, name string, args []string, _ string) ([]byte, []byte, int, error) {
	return nil, nil, 0, fmt.Errorf("%s %s: this machine has no way to start %s, so the history of the change cannot be read",
		name, strings.Join(args, " "), name)
}

// sameCommit is whether two names of a commit are the same commit: git writes a SHA
// in one way and a host in another, and letters are what tells them apart.
func sameCommit(one, other string) bool {
	return one != "" && strings.EqualFold(one, other)
}

// short is a commit as a report names it: the first eight letters are what a person
// recognises, and the whole of it is in the head of the report above.
func short(commit string) string {
	if len(commit) <= 8 {
		return commit
	}
	return commit[:8]
}
