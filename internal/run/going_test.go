package run

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/proc"
)

// TestASecondRunOfATaskIsRefusedWhileItsFirstRunIsGoing is the failure the recheck of #195
// found: nothing forbade a second `task run N` — or a `task run N -continue`, or a `task resume
// N` — of a task whose run was going, and two attempts of one task divided what belongs to one
// run. The second attempt took a number of its own beside the number that was going, and when the
// first run ended it wrote its outcome, its session and its reason into "the last attempt", which
// by then was the attempt of the second run — while its own attempt stood in every list of runs
// as one that is going (D-068 RECHECK-FINDING-4).
//
// The test is written about what a person and a list of runs see — the words of the refusal, the
// state of the task and the files of the attempt, and nothing else — and not about the type the
// refusal has, so that it is about the failure and not about one way of saying it in Go
// (docs/DESIGN.md §7, §7e).
func TestASecondRunOfATaskIsRefusedWhileItsFirstRunIsGoing(t *testing.T) {
	m := newMachine(t)
	m.answers["git ls-files -s"] = answer{stdout: theIndex}
	m.answers["opencode"] = answer{stdout: theRun}
	// The executor of the first run writes and waits: a run that is going is a run that has
	// not said how it ended, and the second run of the task is called while it is there.
	wrote, release := make(chan struct{}), make(chan struct{})
	m.says("opencode", answer{stdout: theRun, wrote: wrote, wait: release})
	host := &host{task: taskOf(43), opened: true}
	cfg := projectOf(t, m.worktrees, "")
	journals := newJournals(m.home, "naghuale-crewflow")

	type answerOfRun struct {
		result Result
		err    error
	}
	over := make(chan answerOfRun, 1)
	go func() {
		result, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
		over <- answerOfRun{result, err}
	}()
	<-wrote

	_, again := Run(t.Context(), m.env(), cfg, host.set(),
		Request{Number: 43, RepoDir: m.repo, Continue: "the review asked for a test of the timeout"})

	if again == nil {
		t.Fatal("the second run of the task went on while the first one was going: одна задача идёт одним прогоном")
	}
	if !strings.Contains(again.Error(), RunGoing) {
		t.Errorf("the second run was refused with %q, want the word %q: the run of the task is going", again, RunGoing)
	}
	// A refusal spends nothing: the task stands where it was, with the attempt that is going
	// and without one more, and no journal of a second attempt was opened beside the journal
	// of the first (docs.DESIGN.md §7, §7c).
	standing := stateOf(t, m, 43)
	if len(standing.Attempts) != 1 {
		t.Fatalf("the state of the task holds %d attempts while its run is going, want the one: %+v",
			len(standing.Attempts), standing.Attempts)
	}
	if _, err := os.Stat(journals.JournalPath(43, 2)); !os.IsNotExist(err) {
		t.Errorf("there is a journal of a second attempt of the task (%v), want none: a refused run opens no journal", err)
	}
	// What the executor of the run that is going wrote, and what crewflow wrote beside it,
	// are whole: the journal of an attempt belongs to that attempt and to nobody else (§7).
	journal := read(t, journals.JournalPath(43, 1))
	for _, in := range []string{"crewflow: executor:", "The change request is open."} {
		if !strings.Contains(journal, in) {
			t.Errorf("the journal of the attempt that is going holds %q, want it whole with %q", journal, in)
		}
	}

	close(release)
	first := <-over
	if first.err != nil {
		t.Fatalf("the first run returned an error: %v", first.err)
	}
	// What came of the first run is in the attempt it happened in and nowhere else: the end of
	// a run is not written into an attempt that was added before it, and its own attempt does
	// not stand in a list of runs as one that is going for ever (D-068 RECHECK-FINDING-4).
	ended := stateOf(t, m, 43)
	if len(ended.Attempts) != 1 {
		t.Fatalf("the state of the task holds %d attempts after its run ended, want the one it began with: %+v",
			len(ended.Attempts), ended.Attempts)
	}
	mine := ended.Attempts[0]
	if mine.Outcome != first.result.Outcome || mine.Session != first.result.Session {
		t.Errorf("the attempt of the state ended as %q in the session %q, want %q in %q: what came of a run "+
			"belongs to the attempt it happened in", mine.Outcome, mine.Session,
			first.result.Outcome, first.result.Session)
	}
}

// TestTheRefusalOfARunThatIsGoingNamesTheAttempt is the same refusal read the way a program
// reads it: a type of its own, and the task, the attempt and the journal of the run that is
// going in it — so that an orchestrator tells this refusal from the refusal of a pair of §7c
// and can show a person where the run that is going writes (docs/DESIGN.md §7e, §7i).
func TestTheRefusalOfARunThatIsGoingNamesTheAttempt(t *testing.T) {
	m := newMachine(t)
	m.answers["git ls-files -s"] = answer{stdout: theIndex}
	m.answers["opencode"] = answer{stdout: theRun}
	wrote, release := make(chan struct{}), make(chan struct{})
	m.says("opencode", answer{stdout: theRun, wrote: wrote, wait: release})
	host := &host{task: taskOf(43), opened: true}
	cfg := projectOf(t, m.worktrees, "")
	journals := newJournals(m.home, "naghuale-crewflow")
	over := make(chan error, 1)
	go func() {
		_, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
		over <- err
	}()
	<-wrote

	_, err := Run(t.Context(), m.env(), cfg, host.set(),
		Request{Number: 43, RepoDir: m.repo, Continue: "the review asked for a test of the timeout"})

	var refusal *ErrGoing
	if !errors.As(err, &refusal) {
		t.Fatalf("the second run of the task returned %v, want an *ErrGoing", err)
	}
	if refusal.Task != 43 || refusal.Attempt != 1 {
		t.Errorf("the refusal is about the task %d and its attempt %d, want the task 43 and its attempt 1",
			refusal.Task, refusal.Attempt)
	}
	if refusal.Journal != journals.JournalPath(43, 1) {
		t.Errorf("the refusal names the journal %q, want the journal of the attempt that is going %q",
			refusal.Journal, journals.JournalPath(43, 1))
	}
	// The word of the refusal is the one a program switches on, and it is not one of the
	// three of a pair: a task does not run beside itself and no record admits it (§7c, §7e).
	var ofAPair *ErrAdmission
	if errors.As(err, &ofAPair) {
		t.Errorf("the refusal is the refusal of a pair %v, want the refusal of a task whose own run is going", ofAPair)
	}
	close(release)
	if err := <-over; err != nil {
		t.Fatalf("the first run returned an error: %v", err)
	}
}

// TestTheRefusalStopsWhereTheRunIsGone is the other side of the same measure: a state of a task
// that says `running` while the machine says the process of that run is gone is a state left by
// a run crewflow was killed in the middle of, and a task nobody may start is a task nobody can
// work on. The refusal asks the machine, and a machine that cannot be asked goes on counting
// such a run as a run that is going (docs/DESIGN.md §7).
func TestTheRefusalStopsWhereTheRunIsGone(t *testing.T) {
	m := newMachine(t)
	m.answers["git ls-files -s"] = answer{stdout: theIndex}
	m.answers["opencode"] = answer{stdout: theRun}
	// The state of the task says the attempt 1 is running and names the process of that run,
	// and the working copy it worked in is there: the continuation of the task goes on in it.
	going(t, m, 43, worktreeOf(m, 43))
	m.has(worktreeOf(m, 43))
	env := m.env()
	// The machine says that no process is there: the run that was going is over, whatever
	// its executor is doing, and the number of a process may be somebody else's after a
	// reboot — which is why the moment it started is written beside it (§7).
	env.Alive = func(proc.Process) bool { return false }
	host := &host{task: taskOf(43), opened: true}

	result, err := Run(t.Context(), env, projectOf(t, m.worktrees, ""), host.set(),
		Request{Number: 43, RepoDir: m.repo, Continue: "the run was killed, go on from where it stopped"})

	if err != nil {
		t.Fatalf("the continuation of a task whose run is gone returned an error: %v", err)
	}
	if result.Attempt != 2 {
		t.Errorf("the run came out as the attempt %d, want the second: the attempt that was left running is over",
			result.Attempt)
	}
	if state := stateOf(t, m, 43); len(state.Attempts) != 2 {
		t.Errorf("the state of the task holds %d attempts, want the one that was left and the one that went on",
			len(state.Attempts))
	}
}

// TestEveryChangeOfAnAttemptIsMadeInThatAttempt is the other half of the measure: a run of a
// task is answered in its own attempt and not in the last attempt of the state of the task.
// The state of a task is written by whoever holds the lock of it, and while one run works the
// queue of attention and the check of the runs that stand write the same file — the attempt
// that is the last one at the moment of a write is not necessarily the attempt of the writer,
// and what came of one run used to land in the state of another (D-068 RECHECK-FINDING-4).
func TestEveryChangeOfAnAttemptIsMadeInThatAttempt(t *testing.T) {
	started := time.Date(2026, time.September, 28, 10, 0, 0, 0, time.UTC)
	ended := started.Add(42 * time.Minute)
	state := State{Number: 43, Title: "the run of a task"}
	for number := range 2 {
		state = state.NextAttempt(state.NextNumber(), StartOf{
			Started:  started.Add(time.Duration(number) * time.Hour),
			Journal:  "journal-of-the-attempt.jsonl",
			Executor: "opencode",
		})
	}
	// The order is the order of a run: the sign of life goes in while the executor works,
	// and the reason of the stop is written into the same attempt after it (§7a).
	changed := state.
		Alive(1, ended, "the executor of the run", "the timeout of the test").
		Ended(1, ended, ChangeRequestOpened).
		Reason(1, "the review asked for a change").
		Provider(1, "retryable").
		InSession(1, "ses_first").
		Identified(1, Identity{Mode: "owner"})

	mine, found := changed.Attempt(1)
	if !found {
		t.Fatalf("the state holds no attempt 1: %+v", changed.Attempts)
	}
	if mine.Outcome != ChangeRequestOpened || !mine.EndedAt.Equal(ended) {
		t.Errorf("the attempt 1 ended as %q at %s, want %q at %s", mine.Outcome, mine.EndedAt, ChangeRequestOpened, ended)
	}
	if mine.Reason != "the review asked for a change" || mine.Provider != "retryable" || mine.Session != "ses_first" {
		t.Errorf("the attempt 1 holds the reason %q, the mark %q and the session %q, want what was written into it",
			mine.Reason, mine.Provider, mine.Session)
	}
	if mine.Identity.Mode != "owner" || mine.LastStep != "the executor of the run" {
		t.Errorf("the attempt 1 holds the identity %+v and the last step %q, want what was written into it",
			mine.Identity, mine.LastStep)
	}
	other, _ := changed.Attempt(2)
	if other.Outcome != Running || other.EndedAt != (time.Time{}) {
		t.Errorf("the attempt 2 is %q since %s, want it running and untouched: what came of the attempt 1 "+
			"is the attempt 1's own", other.Outcome, other.EndedAt)
	}
	// A change addressed to an attempt that is not in the state is not made at all: an
	// attempt this run took out of the state is not written into, and neither is the attempt
	// of another run that is not there yet (docs.DESIGN.md §7).
	untouched := changed.
		Ended(7, ended, Blocked).
		Reason(7, "a reason of a run that is not here").
		Provider(7, "unknown").
		InSession(7, "ses_seventh").
		Identified(7, Identity{Mode: "bot"}).
		Alive(7, ended, "a step of a run that is not here", "")
	if !slices.EqualFunc(changed.Attempts, untouched.Attempts, func(one, other Attempt) bool { return one == other }) {
		t.Errorf("a change addressed to an attempt that is not in the state changed it: %+v", untouched.Attempts)
	}
}
