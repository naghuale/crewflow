package merge

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/forge"
	taskrun "github.com/naghuale/crewflow/internal/run"
)

// TestVerifyOfAMergeThatWentThrough is the check of a merge in the case it is for: the
// branch of the host is at the commit that was merged, the task of the change is closed
// and the checks of that branch are green — and the moment of the check is written into
// the state of the task (docs/DESIGN.md §6, §7h).
func TestVerifyOfAMergeThatWentThrough(t *testing.T) {
	s := newScenario(t, nil)
	s.merged(t)

	result, err := Verify(t.Context(), s.deps(), changeNumber)
	if err != nil {
		t.Fatalf("Verify returned an error: %v", err)
	}

	if !result.Verified {
		t.Fatalf("the check says %+v, want a merge that went through to be verified", result)
	}
	if result.Main != s.change.HeadSHA || result.Merged != s.change.HeadSHA {
		t.Errorf("the check compares %q of the host with the merged %q, want the head of the change %s",
			result.Main, result.Merged, s.change.HeadSHA)
	}
	if result.CI != forge.CheckSuccess || result.TaskState != "closed" {
		t.Errorf("the check holds the CI %q and the task %q, want %q and closed",
			result.CI, result.TaskState, forge.CheckSuccess)
	}
	if result.At == "" {
		t.Error("the check says nothing about when it was made")
	}
	state, err := taskrun.LoadState(s.state)
	if err != nil {
		t.Fatalf("read the state of the task: %v", err)
	}
	if state.VerifiedAt == nil {
		t.Fatal("the state of the task holds no moment of the check, want the one the check reports")
	}
	if result.Note != "" {
		t.Errorf("the check says %q, want nothing to say about a merge that went through", result.Note)
	}
}

// TestVerifyWaitsForTheChecksOfTheBranch is what a check after a merge is for: the CI of
// the branch of the project runs after the merge, and a check that is still going on is
// a wait and not a refusal (docs/DESIGN.md §7h).
func TestVerifyWaitsForTheChecksOfTheBranch(t *testing.T) {
	s := newScenario(t, nil)
	s.merged(t)
	s.stands[s.change.HeadSHA] = []forge.CheckState{
		forge.CheckPending, forge.CheckPending, forge.CheckSuccess,
	}

	result, err := Verify(t.Context(), s.deps(), changeNumber)
	if err != nil {
		t.Fatalf("Verify returned an error: %v", err)
	}

	if !result.Verified {
		t.Fatalf("the check says %+v, want the checks that ended green to be waited for", result)
	}
	if s.waits != 2 {
		t.Errorf("the check waited %d times, want 2: two answers of `pending` and then a green one", s.waits)
	}
}

// TestVerifyOfAChecksThatFailed is a red check of the branch of the project: the one
// thing that says the merge was not good after all, and the check of it names it instead
// of saying "not verified" (docs/DESIGN.md §6, §7h).
func TestVerifyOfAChecksThatFailed(t *testing.T) {
	s := newScenario(t, nil)
	s.merged(t)
	s.stands[s.change.HeadSHA] = []forge.CheckState{forge.CheckFailure}

	result, err := Verify(t.Context(), s.deps(), changeNumber)
	if err != nil {
		t.Fatalf("Verify returned an error: %v", err)
	}

	if result.Verified {
		t.Fatalf("the check says %+v, want the red checks of the branch to be refused", result)
	}
	if !strings.Contains(strings.Join(result.Missing, "; "), string(forge.CheckFailure)) {
		t.Errorf("the check holds %v, want it to name the red checks of the branch", result.Missing)
	}
	if s.waits != 0 {
		t.Errorf("the check waited %d times for a red check, want none", s.waits)
	}
}

// TestVerifyOfChecksThatNeverEnd is a branch whose checks are still going on when the
// waiting is over: the check says so and does not call the merge good
// (docs/DESIGN.md §7h).
func TestVerifyOfChecksThatNeverEnd(t *testing.T) {
	s := newScenario(t, nil)
	s.merged(t)
	s.stands[s.change.HeadSHA] = []forge.CheckState{forge.CheckPending}

	result, err := Verify(t.Context(), s.deps(), changeNumber)
	if err != nil {
		t.Fatalf("Verify returned an error: %v", err)
	}

	if result.Verified {
		t.Fatalf("the check says %+v, want a branch whose checks never end not to be verified", result)
	}
	if !strings.Contains(strings.Join(result.Missing, "; "), string(forge.CheckPending)) {
		t.Errorf("the check holds %v, want it to name the checks that are still going on", result.Missing)
	}
}

// TestVerifyOfATaskThatIsStillOpen: a host that did not close the task behind the change
// that is merged has not done what a merge of it does, and the check says which of the
// three things is not in order (docs/DESIGN.md §7h).
func TestVerifyOfATaskThatIsStillOpen(t *testing.T) {
	s := newScenario(t, nil)
	s.merged(t)
	s.closedAfter = -1

	result, err := Verify(t.Context(), s.deps(), changeNumber)
	if err != nil {
		t.Fatalf("Verify returned an error: %v", err)
	}

	if result.Verified {
		t.Fatalf("the check says %+v, want an open task to be named", result)
	}
	if !strings.Contains(strings.Join(result.Missing, "; "), "open") {
		t.Errorf("the check holds %v, want it to say that the task is still open", result.Missing)
	}
}

// TestVerifyOfABranchThatMovedOn: the branch of the host has gone on since the merge and
// still holds the commit that was merged, which is what the merge was for — and the
// check says so instead of refusing a merge that did go in (docs/DESIGN.md §7h).
func TestVerifyOfABranchThatMovedOn(t *testing.T) {
	s := newScenario(t, nil)
	s.merged(t)
	s.moveMain(t.Context())

	result, err := Verify(t.Context(), s.deps(), changeNumber)
	if err != nil {
		t.Fatalf("Verify returned an error: %v", err)
	}

	if !result.Verified {
		t.Fatalf("the check says %+v, want a branch that moved on and holds the merged commit to be verified", result)
	}
	if !strings.Contains(result.Note, "moved on") {
		t.Errorf("the check says %q, want it to say that the branch has moved on", result.Note)
	}
}

// TestVerifyOfABranchThatDoesNotHoldTheMerge is the case a check after a merge is worth
// having: the branch of the host is somewhere the merged commit is not, and the check
// says so whatever a report of the merge said at the time (docs/DESIGN.md §7h).
func TestVerifyOfABranchThatDoesNotHoldTheMerge(t *testing.T) {
	s := newScenario(t, nil)
	s.merged(t)
	s.rewindMain(t.Context())

	result, err := Verify(t.Context(), s.deps(), changeNumber)
	if err != nil {
		t.Fatalf("Verify returned an error: %v", err)
	}

	if result.Verified {
		t.Fatalf("the check says %+v, want a branch that holds no merged commit not to be verified", result)
	}
	if !strings.Contains(strings.Join(result.Missing, "; "), "not in it") {
		t.Errorf("the check holds %v, want it to name the commit the branch is not at", result.Missing)
	}
}

// TestVerifyBeforeAMerge: nothing has been merged and the branch of the host is behind
// the change, so the check says what is not in order instead of passing a merge that
// did not happen (docs/DESIGN.md §7h).
func TestVerifyBeforeAMerge(t *testing.T) {
	s := newScenario(t, nil)

	result, err := Verify(t.Context(), s.deps(), changeNumber)
	if err != nil {
		t.Fatalf("Verify returned an error: %v", err)
	}

	if result.Verified {
		t.Fatalf("the check says %+v, want nothing before a merge", result)
	}
	if len(result.Missing) == 0 {
		t.Error("the check says what is not in order is nothing, want the branch of the host named")
	}
}

// TestVerifyOfAMergedChangeOfATaskThisMachineKeptNoStateOf is a check of a merge that
// did happen, on the shape a host gives a change that is merged: `merged`, the head of
// the change still named, `Closes #N` in the body — and nothing under it, because the
// records of the review were written by hand or not at all.
//
// The check reads the head and the task of the change off the host, whatever the state
// of this machine is: `Closes #7` is what a change says it is of, and a check that goes
// looking for it in a state file of this machine sends a person to do it by hand
// (docs/DESIGN.md §6, §7h).
func TestVerifyOfAMergedChangeOfATaskThisMachineKeptNoStateOf(t *testing.T) {
	s := newScenario(t, nil)
	s.merged(t)
	s.change.State = "merged"
	s.comments = nil
	deps := s.deps()
	deps.Task, deps.Gate.Task, deps.Worktree = 0, 0, ""

	result, err := Verify(t.Context(), deps, changeNumber)
	if err != nil {
		t.Fatalf("Verify returned an error: %v", err)
	}

	if !result.Verified {
		t.Fatalf("the check says %+v, want a merged change of a task with no state here to be verified", result)
	}
	if result.Task != changeNumber {
		t.Errorf("the check is of task %d, want %d: `Closes #%d` is what the change is of",
			result.Task, changeNumber, changeNumber)
	}
	if result.Merged != s.change.HeadSHA {
		t.Errorf("the check compares the branch with %q, want the head of the change %s", result.Merged, s.change.HeadSHA)
	}
}

// TestVerifyOfAChangeTheHostCouldNotBeRead: a check after a merge on a host that did not
// answer about the change says what the host could not be asked, and not that the
// change names no head — the change was never read, and a report that says so is a
// person looking at all of it by hand (docs/DESIGN.md §6, §7h).
func TestVerifyOfAChangeTheHostCouldNotBeRead(t *testing.T) {
	s := newScenario(t, nil)
	s.merged(t)
	s.change.State = "merged"
	s.changeErr = errors.New("gh pr view 7 -R naghuale/crewflow: exited with 1: could not reach the host")

	result, err := Verify(t.Context(), s.deps(), changeNumber)
	if err != nil {
		t.Fatalf("Verify returned an error: %v", err)
	}

	if result.Verified {
		t.Fatalf("the check says %+v, want a change the host did not answer about not to be verified", result)
	}
	missing := strings.Join(result.Missing, "; ")
	if !strings.Contains(missing, "could not reach the host") {
		t.Errorf("the check holds %v, want it to say what the host could not be asked", result.Missing)
	}
	if strings.Contains(missing, "names no head") {
		t.Errorf("the check holds %v, want it not to speak of a head and a state nobody read", result.Missing)
	}
}

// TestVerifyOfAMergedChangeTheHostCannotBeAskedAboutAndThisMachineKeptNoStateOf is what
// F-023 of the journal of the practice met on 30.09.2026 on PR #42 of this project, an
// hour after its own `crewflow merge` said `merged` and wrote `merged_sha` into the state
// of task #8: the host was asked about the change and gave no answer, and this machine
// kept no state to answer out of either — the worktree of the task was left behind by a
// cleanup that was refused, and the state went with the run of a machine that is not
// this one.
//
// The check said «crewflow does not know which commit was merged: the change names no
// head and this machine kept no state of the task» — a statement about a change nobody
// read, which sent the person reading it to look for a head the change had and a state
// the machine had. What it had to say is that the host could not be asked, which is a
// thing a person can go and do something about (docs/DESIGN.md §6, §7h).
func TestVerifyOfAMergedChangeTheHostCannotBeAskedAboutAndThisMachineKeptNoStateOf(t *testing.T) {
	s := newScenario(t, nil)
	s.merged(t)
	s.change.State = "merged"
	s.changeErr = errors.New("gh pr view 7 -R naghuale/crewflow: exited with 1: could not reach the host")
	deps := s.deps()
	deps.Task, deps.Gate.Task, deps.Worktree = 0, 0, ""
	deps.Home = t.TempDir()

	result, err := Verify(t.Context(), deps, changeNumber)
	if err != nil {
		t.Fatalf("Verify returned an error: %v", err)
	}

	if result.Verified {
		t.Fatalf("the check says %+v, want nothing to be verified on a host that gave no answer", result)
	}
	missing := strings.Join(result.Missing, "; ")
	if !strings.Contains(missing, "could not reach the host") {
		t.Errorf("the check holds %q, want it to name the host that could not be asked", missing)
	}
	if strings.Contains(missing, "names no head") {
		t.Errorf("the check holds %q, want it not to speak of a head and a state nobody read", missing)
	}
}

// TestVerifyOfAMergedChangeWhoseHeadHasMovedOnSince: a change that was merged and got
// commits pushed to its branch afterwards. The commit that was merged is the one the
// state of the task recorded and the one the branch of the host is compared with, and
// the check says that the head of the change is somewhere else rather than calling it
// the commit that was merged — a person reading the other would be told something
// false (docs/DESIGN.md §7h).
func TestVerifyOfAMergedChangeWhoseHeadHasMovedOnSince(t *testing.T) {
	s := newScenario(t, nil)
	s.merged(t)
	s.change.State = "merged"
	s.pushedOnTopOfTheChange(t)

	result, err := Verify(t.Context(), s.deps(), changeNumber)
	if err != nil {
		t.Fatalf("Verify returned an error: %v", err)
	}

	if !result.Verified {
		t.Fatalf("the check says %+v, want the merge of the commit that was merged to be verified", result)
	}
	if result.Merged != s.sha(t.Context(), "change~1") {
		t.Errorf("the check compares the branch with %q, want the commit that was merged", result.Merged)
	}
	if result.Head != s.change.HeadSHA {
		t.Errorf("the check holds the head %q, want the head the host names now", result.Head)
	}
	if !strings.Contains(result.Note, "stands at") {
		t.Errorf("the check says %q, want it to say that the head of the change has moved on", result.Note)
	}
}

// TestVerifyOfAMergedChangeWhoseHeadMovedOnAndWhoseBranchMovedOn: both ends of the
// merge went on after it happened — a change went in after this one, and this change
// got commits pushed to its branch. Each is a fact a person cannot work out from the
// other, and a check that wrote the second over the first has lost one of them
// (docs/DESIGN.md §6, §7h).
func TestVerifyOfAMergedChangeWhoseHeadMovedOnAndWhoseBranchMovedOn(t *testing.T) {
	s := newScenario(t, nil)
	s.merged(t)
	s.change.State = "merged"
	s.pushedOnTopOfTheChange(t)
	s.moveMain(t.Context())

	result, err := Verify(t.Context(), s.deps(), changeNumber)
	if err != nil {
		t.Fatalf("Verify returned an error: %v", err)
	}

	if !result.Verified {
		t.Fatalf("the check says %+v, want the merge of the commit that was merged to be verified", result)
	}
	for _, want := range []string{"the change stands at", "has moved on"} {
		if !strings.Contains(result.Note, want) {
			t.Errorf("the check says %q, want it to mention %q", result.Note, want)
		}
	}
}

// TestVerifyOfATaskCrewflowClosedBehindTheMerge: a host that does not close the task
// behind a change it merged, and a merge that closes it itself — the check after such a
// merge asks the tracker and finds the task closed, which is what the whole of it is
// for: a merge that went through is a merge a check may confirm (docs/DESIGN.md §6, §7h).
func TestVerifyOfATaskCrewflowClosedBehindTheMerge(t *testing.T) {
	s := newScenario(t, nil)
	s.closedAfter = -1

	merged, err := Run(t.Context(), s.deps(), changeNumber)
	if err != nil {
		t.Fatalf("the merge returned an error: %v", err)
	}
	if merged.TaskClosedBy != ClosedByCrewflow {
		t.Fatalf("the merge came to %q with the task closed by %q, want the task closed by crewflow",
			merged.Outcome, merged.TaskClosedBy)
	}

	result, err := Verify(t.Context(), s.deps(), changeNumber)
	if err != nil {
		t.Fatalf("Verify returned an error: %v", err)
	}

	if !result.Verified {
		t.Fatalf("the check says %+v, want the merge of a task crewflow closed to be verified", result)
	}
	if result.TaskState != "closed" {
		t.Errorf("the check reads the task as %q, want it closed: the merge closed it", result.TaskState)
	}
}

// merged is a change that has been merged: the merge is run first, the way a person runs
// it, and the case is about what the check after it finds (docs/DESIGN.md §6).
func (s *scenario) merged(t *testing.T) {
	t.Helper()
	result, err := Run(t.Context(), s.deps(), changeNumber)
	if err != nil {
		t.Fatalf("the merge returned an error: %v", err)
	}
	if result.Outcome != Merged {
		t.Fatalf("the merge came to %q (%v), want %q", result.Outcome, result.Left, Merged)
	}
}

// TestAMergeWithNoGitAtAll: a machine that cannot start git cannot say whether the
// change may go in, and it pushes nothing on a guess — the merge is refused with the
// reason of the table of §7h and the journal of it is still written (docs/DESIGN.md §7h).
func TestAMergeWithNoGitAtAll(t *testing.T) {
	s := newScenario(t, nil)
	deps := s.deps()
	deps.Checkout.Run = nil

	result, err := Run(t.Context(), deps, changeNumber)
	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	if result.Outcome != Refused || result.Verdict.Reason != "forge-unavailable" {
		t.Errorf("the merge came to %q (%q), want a refusal for forge-unavailable",
			result.Outcome, result.Verdict.Reason)
	}
	if s.pushed() {
		t.Error("a merge that cannot ask git pushed anyway")
	}
	if result.Journal == "" {
		t.Error("the merge holds no journal, want the file it writes even when it refuses")
	}
}

// TestAMergeOfAChangeItKnowsNoTaskOf: a change of a task this machine ran no task for
// has no state and no worktree to take away, and a merge of it is a merge anyway — the
// commit goes into the branch of the host and nothing else is invented for it
// (docs/DESIGN.md §7h).
func TestAMergeOfAChangeItKnowsNoTaskOf(t *testing.T) {
	s := newScenario(t, nil)
	deps := s.deps()
	deps.Task, deps.Worktree = 0, ""

	result, err := Run(t.Context(), deps, changeNumber)
	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	if result.Outcome != Merged {
		t.Fatalf("the merge came to %q (%v), want %q", result.Outcome, result.Left, Merged)
	}
	if result.MergedSHA != s.change.HeadSHA {
		t.Errorf("the merge says the branch of the host is at %q, want %q", result.MergedSHA, s.change.HeadSHA)
	}
}

// TestAJournalOfAMergeThatCouldNotBeWritten stops the merge before anything is pushed:
// the journal is where the whole of what git said is kept, and a merge whose evidence
// is thrown away is a merge nobody can look at afterwards (docs/DESIGN.md §6).
func TestAJournalOfAMergeThatCouldNotBeWritten(t *testing.T) {
	s := newScenario(t, nil)
	deps := s.deps()
	deps.Home = s.state + "/not-a-folder"

	if _, err := Run(t.Context(), deps, changeNumber); err == nil {
		t.Fatal("Run wrote a merge whose journal it cannot keep, want a refusal")
	}
	if s.pushed() {
		t.Error("a merge whose journal could not be written pushed anyway")
	}
}

// TestTheClockOfAMerge is the clock the journal of a merge and the state of the task are
// written with: a merge that happened at a moment crewflow can name, and not at the
// moment the test happens to run (docs/DESIGN.md §7).
func TestTheClockOfAMerge(t *testing.T) {
	s := newScenario(t, nil)
	s.now = time.Date(2026, time.October, 2, 9, 30, 0, 0, time.UTC)

	result, err := Run(t.Context(), s.deps(), changeNumber)
	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	if want := `"at":"` + s.now.Format(time.RFC3339) + `"`; !strings.Contains(read(t, result.Journal), want) {
		t.Errorf("the journal of the merge holds no %s", want)
	}
}
