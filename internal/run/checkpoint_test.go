package run

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/secret"
)

// TestARunThatWaitedForTheKeychainWritesThePointItStandsAt is the whole of the failure
// this is here for (F-091, journal #37): on 01.10 a window of the keychain of macOS stood
// open for two minutes, nobody was at the machine, the run stopped as `blocked:
// keychain-approval` — and the owner had to start the run again by hand, repeating not
// only the decision but the run. A person has to do the one thing that is his; everything
// else is what crewflow keeps (docs/DESIGN.md §7i, MODEL IV-028).
//
// So a run that waited and was not answered writes the point it stands at into the state
// of the task — the step to go on from, the head of the branch, the task as crewflow read
// it — and the event of the request with the three facts a person needs to find the window.
func TestARunThatWaitedForTheKeychainWritesThePointItStandsAt(t *testing.T) {
	standing := waitedForTheKeychain(t)

	state := stateOf(t, standing.m, 43)
	point := state.Checkpoint
	if point == nil {
		t.Fatal("the state of task 43 holds no checkpoint, want the point the run stands at: " +
			"without it the owner has to start the run again by hand")
	}
	if point.Task != 43 {
		t.Errorf("the checkpoint is of task %d, want 43", point.Task)
	}
	if point.Step != stepReadKey {
		t.Errorf("the checkpoint stands at the step %q, want %q: it is the step the run was stopped at",
			point.Step, stepReadKey)
	}
	if point.Head != theHead {
		t.Errorf("the checkpoint holds the head %q, want %q: a continuation is checked against it",
			point.Head, theHead)
	}
	if point.Assignment != fingerprint(taskOf(43)) {
		t.Errorf("the checkpoint holds the assignment %q, want the fingerprint of the task %q",
			point.Assignment, fingerprint(taskOf(43)))
	}
	if point.At.IsZero() {
		t.Error("the checkpoint has no moment in it, want the one the run wrote it at")
	}
	for _, want := range []struct {
		name, got, said string
	}{
		{"the channel", point.Channel, ChannelKeychain},
		{"the resource", point.Resource, SubjectExecutorKey},
		{"the action", point.Action, ActionHumanExecute},
		{"what came of the request", point.Outcome, WaitTimeout},
	} {
		if want.got != want.said {
			t.Errorf("the checkpoint holds %q, want %q", want.name, want.said)
		}
	}
	// The event of the request is in the journal of the attempt while it is open, with
	// the three facts of it: a person who comes back to a run that stood in front of a
	// window of the system reads that journal after the window has closed (§7i).
	journal := read(t, standing.journals().JournalPath(43, 1))
	for _, want := range []string{
		EventAuthorizationRequired,
		"channel=" + ChannelKeychain,
		"resource=" + SubjectExecutorKey,
		"action=" + ActionHumanExecute,
		"step=" + stepReadKey,
		EventAuthorizationTimeout,
	} {
		if !strings.Contains(journal, want) {
			t.Errorf("the journal of the attempt holds no %q, want the request of a person in it:\n%s", want, journal)
		}
	}
}

// TestAResumeGoesOnFromThePointOfTheTask is what the point is for: the person answered
// the window of the system — "Always Allow" — and the run went on in the worktree and the
// branch the run before it left, with the task in hand and nothing else to say. No new
// worktree, no new branch, and no running of the task from the beginning (§7i).
func TestAResumeGoesOnFromThePointOfTheTask(t *testing.T) {
	standing := waitedForTheKeychain(t)
	standing.theKeychainAnswers(t)

	result := standing.resume(t)

	if result.Outcome != ChangeRequestOpened {
		t.Errorf("the continuation came out as %q, want %q: the run went on from where it stood",
			result.Outcome, ChangeRequestOpened)
	}
	if !result.Resumed {
		t.Error("the report does not say that the run went on from the point of the task, " +
			"want it said: a person who pressed \"Always Allow\" has to see that the work went on")
	}
	if result.Worktree != standing.first.Worktree {
		t.Errorf("the continuation worked in %q, want the worktree the run before it left %q: "+
			"the work of the task is in that folder", result.Worktree, standing.first.Worktree)
	}
	if result.Branch != standing.first.Branch {
		t.Errorf("the continuation is on the branch %q, want %q", result.Branch, standing.first.Branch)
	}
	if result.Attempt != 2 {
		t.Errorf("the continuation is attempt %d, want 2: a run of a task that goes on is the attempt after it",
			result.Attempt)
	}
	// The request came through, and the state of the task says so — which is what the
	// next continuation is refused with: there is nothing left to go on from (§7i).
	state := stateOf(t, standing.m, 43)
	if state.Checkpoint == nil || state.Checkpoint.Outcome != WaitCompleted {
		t.Fatalf("the state of task 43 holds the point %v, want the request answered as %q",
			state.Checkpoint, WaitCompleted)
	}
	if last := state.Attempts[len(state.Attempts)-1]; !last.Resumed {
		t.Error("the attempt in the state of the task does not say that it went on from the point, " +
			"want it said: an orchestrator reads the state of the task and not a terminal")
	}
	journal := read(t, standing.journals().JournalPath(43, 2))
	if !strings.Contains(journal, EventAuthorizationCompleted) {
		t.Errorf("the journal of the continuation holds no %s, want the answer of the person in it:\n%s",
			EventAuthorizationCompleted, journal)
	}
}

// TestAResumeIsRefusedAndNamesWhy walks every reason a continuation is refused. A point is
// a claim about the machine and about the task as they were when the run stopped, and every
// one of these is a claim that has stopped being true. Going on from a point that is not
// the one the run wrote is a new run with the words of a continuation, and a person who is
// told only "no" is a person who runs `crewflow task run` and loses the work again
// (docs/DESIGN.md §7i).
func TestAResumeIsRefusedAndNamesWhy(t *testing.T) {
	cases := []struct {
		name string
		// before is what the machine and the task are like when the person asks for the
		// continuation, after the run stopped in front of the window. It may answer the
		// request first, which is how a point comes to be spent or refused.
		before func(t *testing.T, standing *atTheWindow)
		// reason is the name of the refusal and said is what it has to hold: the name is
		// what a program reads and the sentence is what a person reads.
		reason, said string
	}{
		{
			name: "the branch of the task has moved",
			before: func(_ *testing.T, standing *atTheWindow) {
				standing.m.answers["git rev-parse HEAD"] = answer{stdout: "0f9e8d7c6b5a4938271605f4e3d2c1b0a9f8e7d6"}
			},
			reason: RefusedHeadMoved,
			said:   "stands at 0f9e8d7",
		},
		{
			name: "the task is not the one the run was given",
			before: func(_ *testing.T, standing *atTheWindow) {
				// The owner added a criterion while the run waited. The body of the
				// task is what the run was given and what a continuation goes on with.
				standing.host.task.Body += "\n### Risks and decisions\n\nAlso: do not touch the hooks.\n"
			},
			reason: RefusedTaskChanged,
			said:   "it has been changed",
		},
		{
			name: "the point is older than the term of a point",
			before: func(_ *testing.T, standing *atTheWindow) {
				standing.m.quiet(CheckpointValid + time.Hour)
			},
			reason: RefusedStale,
			said:   "a point is good for",
		},
		{
			name: "the person refused the request",
			before: func(t *testing.T, standing *atTheWindow) {
				standing.theOwnerRefuses()
				standing.resume(t)
			},
			reason: RefusedDenied,
			said:   "a refusal is not asked again",
		},
		{
			name: "the request came already",
			before: func(t *testing.T, standing *atTheWindow) {
				standing.theKeychainAnswers(t)
				standing.resume(t)
			},
			reason: RefusedAnswered,
			said:   "there is nothing left to continue",
		},
		{
			name: "the point stands at a step this build does not know",
			before: func(t *testing.T, standing *atTheWindow) {
				standing.spoilThePoint(t, func(point *Checkpoint) { point.Step = "read-orchestrator-key" })
			},
			reason: RefusedUnknownStep,
			said:   "goes on only",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			standing := waitedForTheKeychain(t)
			tc.before(t, standing)
			attempts := len(stateOf(t, standing.m, 43).Attempts)

			_, err := Resume(t.Context(), standing.env, standing.cfg, standing.host.set(),
				Request{Number: 43, RepoDir: standing.m.repo})

			if err == nil {
				t.Fatal("Resume returned no error, want the continuation refused")
			}
			var refusal Refused
			if !errors.As(err, &refusal) {
				t.Fatalf("Resume returned %v, want a refusal of the continuation, which names its reason", err)
			}
			if refusal.Name != tc.reason {
				t.Errorf("the refusal is named %q, want %q: a program reads the name", refusal.Name, tc.reason)
			}
			if !strings.Contains(refusal.Said, tc.said) {
				t.Errorf("the refusal says %q, want it to hold %q: a refusal that names nothing "+
					"leaves a person where it was", refusal.Said, tc.said)
			}
			// A refusal creates nothing of its own: no attempt of the task and no
			// journal of one, because a continuation that was refused is not a run
			// (§7). What it leaves alone is everything the run before it wrote down.
			if now := len(stateOf(t, standing.m, 43).Attempts); now != attempts {
				t.Errorf("the state of task 43 holds %d attempts, want the %d it held before the "+
					"refusal: a refused continuation leaves the state as it was", now, attempts)
			}
			if started := len(standing.m.commandsOf("opencode")); started > 1 {
				t.Errorf("the executor was started %d times, want the one of the run before the refusal",
					started)
			}
		})
	}
}

// TestATaskThatWasNeverRunHasNothingToResume: a point is what a run of the task wrote when
// it stopped, and a task that was never run on this machine has none. A refusal that says
// so, with the command that starts the task, is a refusal a person can act on (§7i).
func TestATaskThatWasNeverRunHasNothingToResume(t *testing.T) {
	m := newMachine(t)
	host := &host{task: taskOf(43)}
	cfg := projectOf(t, m.worktrees, "")

	_, err := Resume(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})

	if err == nil {
		t.Fatal("Resume returned no error, want the refusal of a task that was never run")
	}
	var refusal Refused
	if !errors.As(err, &refusal) || refusal.Name != RefusedNoCheckpoint {
		t.Fatalf("Resume returned %v, want the refusal %q", err, RefusedNoCheckpoint)
	}
	if !strings.Contains(refusal.Said, "crewflow task run 43") {
		t.Errorf("the refusal says %q, want it to name the command that starts the task", refusal.Said)
	}
	if m.askedFor("opencode") {
		t.Errorf("the machine was asked %v, want no executor started for a task with no point", m.lines())
	}
}

// TestNothingGoesToTheKeyOfTheAppWithoutAPersonAsking is the promise of §7i this is built
// on: every attempt to read the key of the app without the access opens a window of macOS
// of its own, so nothing crewflow does on its own may read it. A run that stood in front of
// that window asks once and stops; the list of runs, the queue of attention and the check of
// the runs that stand — everything a schedule of an orchestrator runs — read the state of
// the task and ask nothing; and a continuation that is refused asks nothing either, so a
// person who is told that it cannot go on is told it without a window opening for them.
func TestNothingGoesToTheKeyOfTheAppWithoutAPersonAsking(t *testing.T) {
	m := newMachine(t)
	store := &countingKeychain{}
	env := m.env()
	env.Notices = secret.NewNotices(nil)
	env.Secrets = secret.Waited(store, env.Notices, 10*time.Millisecond)
	host := &host{task: taskOf(43), identity: theBot(), store: env.Secrets}
	cfg := inTheModeOfTheBot(projectOf(t, m.worktrees, ""))

	if _, err := Run(t.Context(), env, cfg, host.set(), Request{Number: 43, RepoDir: m.repo}); err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}
	if reads := store.askedFor(); reads != 1 {
		t.Fatalf("the keychain of the machine was asked %d times by the run, want once: "+
			"every attempt to read it opens a window of the system", reads)
	}

	// Everything a schedule of an orchestrator runs, twice over: a queue that cries wolf
	// is a queue nobody reads (§6a).
	queue := attentionEnvOf(m.at())
	for range 2 {
		if _, err := List(m.home, "naghuale-crewflow", queue.ListEnv); err != nil {
			t.Fatalf("List returned an error: %v", err)
		}
		if _, err := AttentionQueue(t.Context(), m.home, "naghuale-crewflow", queue, nil); err != nil {
			t.Fatalf("AttentionQueue returned an error: %v", err)
		}
		if _, err := CheckStalled(t.Context(), m.home, "naghuale-crewflow", queue.ListEnv,
			func(context.Context, Standing) (bool, string) { return true, "" }); err != nil {
			t.Fatalf("CheckStalled returned an error: %v", err)
		}
	}

	// And a continuation that is refused: the point is checked before the key is asked
	// for, so a person who is told "no" is not answered by a window of macOS.
	m.quiet(CheckpointValid + time.Hour)
	if _, err := Resume(t.Context(), env, cfg, host.set(), Request{Number: 43, RepoDir: m.repo}); err == nil {
		t.Fatal("Resume returned no error, want the refusal of a point older than the term of a point")
	}

	if reads := store.askedFor(); reads != 1 {
		t.Errorf("the keychain of the machine was asked %d times, want the one of the run: nothing "+
			"crewflow does on its own may read the key, or it opens a window of the system", reads)
	}
}

// TestARefusedContinuationIsABlockedRunWithTheAnswerOfThePerson: a continuation that was
// refused by the owner of the machine is a run that happened — it stood in front of a
// window of the system and the window was answered — and a run that is waiting for a person
// is a thing an orchestrator has to see. The name of the run is the mode of the project
// wherever the wait ended, and the words of macOS go into the state as they are: crewflow
// does not decide what a refusal of the keychain means (§7i).
func TestARefusedContinuationIsABlockedRunWithTheAnswerOfThePerson(t *testing.T) {
	standing := waitedForTheKeychain(t)
	standing.theOwnerRefuses()
	if result := standing.resume(t); result.Outcome != Blocked {
		t.Errorf("the continuation came out as %q, want %q", result.Outcome, Blocked)
	}

	state := stateOf(t, standing.m, 43)
	last := state.Attempts[len(state.Attempts)-1]
	if last.Outcome != Blocked {
		t.Errorf("the attempt came out as %q, want %q: a run refused a decision of a person is blocked",
			last.Outcome, Blocked)
	}
	if last.Identity.Mode != forge.ModeBot {
		t.Errorf("the attempt holds the mode %q, want %q", last.Identity.Mode, forge.ModeBot)
	}
	if !strings.HasPrefix(last.Reason, reasonApproval) {
		t.Errorf("the attempt holds the reason %q, want it to begin with %q", last.Reason, reasonApproval)
	}
	if state.Checkpoint == nil || state.Checkpoint.Outcome != WaitDenied {
		t.Errorf("the state holds the point %v, want the request answered as %q", state.Checkpoint, WaitDenied)
	}
	journal := read(t, standing.journals().JournalPath(43, 2))
	if !strings.Contains(journal, EventAuthorizationDenied) {
		t.Errorf("the journal of the continuation holds no %s, want the refusal of the person in it:\n%s",
			EventAuthorizationDenied, journal)
	}
}

// TestTheQueueOfAWaitNamesTheContinuationAndOfARefusalTheRunAgain: the queue is where a
// person looks to find out what is wanted of them, so what it says to do has to be a
// command that works. A request nobody has answered is answered by going on from the point
// once the person has done the one thing that is his; a request a person refused is not one
// to go on from, and a queue that keeps naming a command that always answers "no" is a
// queue that cries wolf until a person stops reading it (§6a, §7i).
func TestTheQueueOfAWaitNamesTheContinuationAndOfARefusalTheRunAgain(t *testing.T) {
	waiting := waitedForTheKeychain(t)
	entry := attentionOnly(t, waiting.m.home, "naghuale-crewflow", attentionEnvOf(waiting.m.at()))
	if entry.Next != "crewflow task resume 43" {
		t.Errorf("the queue of a run that is waiting says %q, want %q", entry.Next, "crewflow task resume 43")
	}
	if entry.Reason != ReasonHumanAuthorization {
		t.Errorf("the reason of the entry is %q, want %q", entry.Reason, ReasonHumanAuthorization)
	}

	refused := waitedForTheKeychain(t)
	refused.theOwnerRefuses()
	refused.resume(t)
	one := attentionOnly(t, refused.m.home, "naghuale-crewflow", attentionEnvOf(refused.m.at()))
	if one.Next != "crewflow task run 43" {
		t.Errorf("the queue of a run that was refused says %q, want the run of the task again: "+
			"`crewflow task resume` refuses a point a person has refused", one.Next)
	}
	if !strings.Contains(one.Hint, "refused") {
		t.Errorf("the queue holds the hint %q, want it to say that this build was refused the key", one.Hint)
	}
}

// atTheWindow is a machine with a task of it stopped in front of the window of the keychain
// of macOS: the store of the machine, the host of the project, the settings of the project
// and the run that stopped there. Every test of a continuation starts from one, because a
// continuation is nothing without a run that was stopped.
type atTheWindow struct {
	m   *machine
	env Env
	// notes is where the run says that it waits, and the store of the machine is behind
	// it: one set of notices for the terminal and the journal of the attempt (§7i).
	notes *secret.Notices
	host  *host
	cfg   config.Config
	// first is the run that stopped at the window.
	first Result
}

// journals is where the state of the task and the journals of its attempts are kept.
func (a *atTheWindow) journals() Journals {
	return newJournals(a.m.home, a.cfg.RepoName())
}

// spoilThePoint changes the point of the task and writes the state back, which is how a
// test says that the machine or the task is not what the point was written for — a branch
// that moved, a task that was edited, a step this build knows nothing about. A test writes
// the fact into the state rather than pretending to be a run: what the state holds is what
// a continuation reads, and nothing else (§7h).
func (a *atTheWindow) spoilThePoint(t *testing.T, spoil func(point *Checkpoint)) {
	t.Helper()
	path := a.journals().StatePath(43)
	state, err := LoadState(path)
	if err != nil {
		t.Fatalf("the state of task 43: %v", err)
	}
	if state.Checkpoint == nil {
		t.Fatal("the state of task 43 holds no checkpoint, want the point the run stands at")
	}
	spoil(state.Checkpoint)
	if err := SaveState(path, state); err != nil {
		t.Fatalf("write the state of task 43 back: %v", err)
	}
}

// resume is the continuation the person asks for: the command, and what it came out as.
func (a *atTheWindow) resume(t *testing.T) Result {
	t.Helper()
	result, err := Resume(t.Context(), a.env, a.cfg, a.host.set(), Request{Number: 43, RepoDir: a.m.repo})
	if err != nil {
		t.Fatalf("Resume returned an error: %v", err)
	}
	return result
}

// theKeychainAnswers is what the owner of the machine does in the window of the system, and
// what a run behind it sees: the keychain lets this build of the program in and opens no
// window of its own.
func (a *atTheWindow) theKeychainAnswers(t *testing.T) {
	t.Helper()
	a.host.store = secret.Waited(&answering{key: keyOfTheRun(t)}, a.notes, time.Minute)
	a.host.opened = true
	a.m.answers["opencode"] = answer{stdout: theRun}
	a.m.answers["git config"] = answer{}
}

// theOwnerRefuses is what the owner of the machine does when he does not want this build of
// crewflow to read the key of the app: macOS answers −128 "User canceled" (§7i).
func (a *atTheWindow) theOwnerRefuses() {
	a.host.noIdentity = errors.New("the keychain of macOS refused to read crewflow/github-app-5107052 " +
		"(status -128): User canceled")
}

// waitedForTheKeychain is a machine and a project of a test whose run of task 43 stood in
// front of the window of the keychain of macOS and was not answered in it: the store of the
// machine is one that does not trust this build of the program, and the wait of it is short
// enough for a test. It fails the test where the run did not stop where it should.
func waitedForTheKeychain(t *testing.T) *atTheWindow {
	t.Helper()
	m := newMachine(t)
	notes := secret.NewNotices(nil)
	env := m.env()
	env.Notices = notes
	env.Secrets = secret.Waited(unanswered{}, notes, 10*time.Millisecond)
	standing := &atTheWindow{
		m:     m,
		env:   env,
		notes: notes,
		host:  &host{task: taskOf(43), identity: theBot(), store: env.Secrets},
		cfg:   inTheModeOfTheBot(projectOf(t, m.worktrees, "")),
	}
	first, err := Run(t.Context(), env, standing.cfg, standing.host.set(),
		Request{Number: 43, RepoDir: m.repo})
	if err != nil {
		t.Fatalf("the run that stood at the window of the keychain returned an error: %v", err)
	}
	if first.Outcome != Blocked {
		t.Fatalf("the first run came out as %q, want %q", first.Outcome, Blocked)
	}
	standing.first = first
	return standing
}

// TestARunThatCannotSayTheHeadOfItsBranchLeavesNoPoint: a point is checked against the commit
// the branch of the task stands at, and a point that crewflow cannot check is not a point to
// go on from — a continuation from it would be a guess about the work. So a run that could not
// ask git writes no point, says why in the way out of the run, stays blocked, and a person who
// asks to continue it is told to run the task anew (docs/DESIGN.md §7i).
func TestARunThatCannotSayTheHeadOfItsBranchLeavesNoPoint(t *testing.T) {
	m := newMachine(t)
	// A worktree git cannot tell the commit of: the run stands at the window, the wait
	// is over, and there is nothing to check a point against.
	m.answers["git rev-parse HEAD"] = answer{code: 1, stderr: "fatal: not a git repository"}
	notes := secret.NewNotices(nil)
	env := m.env()
	env.Notices = notes
	env.Secrets = secret.Waited(unanswered{}, notes, 10*time.Millisecond)
	host := &host{task: taskOf(43), identity: theBot(), store: env.Secrets}
	cfg := inTheModeOfTheBot(projectOf(t, m.worktrees, ""))

	result, err := Run(t.Context(), env, cfg, host.set(), Request{Number: 43, RepoDir: m.repo})

	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}
	if result.Outcome != Blocked {
		t.Errorf("the run came out as %q, want %q: the run is over either way", result.Outcome, Blocked)
	}
	if point := stateOf(t, m, 43).Checkpoint; point != nil {
		t.Errorf("the state of task 43 holds the point %+v, want none: a point crewflow cannot check "+
			"is not a point to go on from", point)
	}
	if out := read(t, result.ErrorJournal); !strings.Contains(out, "the head of the branch of the task") {
		t.Errorf("the way out of the run holds %q, want it to say why there is no point to go on from", out)
	}

	_, err = Resume(t.Context(), env, cfg, host.set(), Request{Number: 43, RepoDir: m.repo})

	var refusal Refused
	if !errors.As(err, &refusal) {
		t.Fatalf("Resume returned %v, want the refusal of a task with no point", err)
	}
	if !strings.Contains(refusal.Said, "crewflow task run 43") {
		t.Errorf("the refusal says %q, want it to name the run of the task anew", refusal.Said)
	}
}

// countingKeychain is the keychain of macOS of a machine of a test that counts how many times it was
// asked for the key of the app: every attempt to read it without the access opens a window
// of the system, and a window of the system that nobody asked for is the defect this whole
// mechanism is here for (docs/DESIGN.md §7i).
type countingKeychain struct {
	mu    sync.Mutex
	reads int
}

// Get counts the read and waits for a window nobody answers, which is what a keychain of
// macOS does with a build it has never been given the answer for.
func (c *countingKeychain) Get(string, string) ([]byte, error) {
	c.mu.Lock()
	c.reads++
	c.mu.Unlock()
	<-noWindow
	return nil, nil
}

func (*countingKeychain) Set(string, string, []byte) error { return nil }

func (*countingKeychain) Has(string, string) (bool, error) { return true, nil }

func (*countingKeychain) Allowed(string) (bool, error) { return false, nil }

// askedFor is how many times the machine was asked for the key of the app.
func (c *countingKeychain) askedFor() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reads
}
