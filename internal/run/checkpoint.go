package run

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/naghuale/crewflow/internal/forge"
)

// stepReadKey is where a run of a task in the mode of the bot goes on from when it was
// stopped in front of the window of the keychain of macOS: the step of crewflow that asks
// the machine for the key of the App of the executor and signs a token of the host with
// it. Nothing of the task was done before it — the executor is started only after it — so
// going on from there is the whole of the run of the task, in the same worktree.
//
// It is a name and not a sentence, because the checkpoint is read by crewflow itself and
// the reason of a run is what a person reads (docs/DESIGN.md §7i).
const stepReadKey = "read-executor-key"

// CheckpointValid is how long the point a run of a task stands at stays a point to go on
// from: a day, which is the length of a working day of a person and the same length the
// attention queue waits before it escalates a task of its own (§6a).
//
// A point older than this is refused rather than guessed at: the machine may have been
// rebuilt, the App of the host may have been given a new key, and a week of silence is a
// new situation whatever the state file says about the old one. A person who wants the
// task done runs it again, which is a decision of the owner and not a continuation
// (docs/DESIGN.md §7i).
//
// The term is a constant of the code and not a key of `crewflow.toml`: a project that
// wants another term names it in a setting of its own in the task that gives settings of
// the resume to every project, and nothing of the continuation depends on this number
// being configurable today.
const CheckpointValid = 24 * time.Hour

// Checkpoint is where a run of a task stands when it needs a decision of a person, and
// everything crewflow has to know to go on from there once that decision is made: the step
// of crewflow to go on from, the head of the branch the run stood at, the task as crewflow
// read it, the three facts of the request and what came of it.
//
// It is what makes the person do only the necessary action — press "Always Allow" in the
// window of the system — and nothing else: the work, the worktree and the context of the
// executor stay where the run left them (docs/DESIGN.md §7i, MODEL IV-028).
type Checkpoint struct {
	// Task is the number of the task the point belongs to. A state file is of one task
	// already, and the number is here because a point is read out of it and by other
	// programs than the one that wrote it.
	Task int `json:"task"`
	// Step is where the run goes on from, as the name of a step of crewflow: the
	// continuation starts there and not at the beginning of a new run.
	Step string `json:"step"`
	// Head is the commit the branch of the task stood at when the point was written. It
	// is the fact a continuation is refused on when the branch has moved: the session of
	// the executor and the context of the run are of that commit and not of a later one.
	Head string `json:"head"`
	// Assignment is what crewflow remembers of the task when it wrote the point: a
	// fingerprint of the title and the body, not the task itself. It is what says
	// whether the task the tracker holds is the one a run was given, and a state file
	// keeps a hash of an issue rather than a copy of it (§7h).
	Assignment string `json:"assignment"`
	// At is when the point was written, and the freshness of it is counted from here.
	At time.Time `json:"at"`
	// DecidedAt is when the answer came, and it is nothing while a person has not
	// answered: a point is asked about until the answer, and the answer is a fact with a
	// moment of its own (§7h).
	DecidedAt time.Time `json:"decided_at,omitempty"`
	// Channel is where the decision is asked for, Resource is which resource of the
	// project it is about and Action is what the person is to do in it: the diagnosis of
	// the request, and nothing of it a state of its own (HA-002, §7f).
	Channel  string `json:"channel,omitempty"`
	Resource string `json:"resource,omitempty"`
	Action   string `json:"action,omitempty"`
	// Outcome is what came of the request: nothing while a person has not answered it,
	// and one of the three words below once it has. It is why a point whose answer was
	// a refusal is not asked again (docs/DESIGN.md §7i).
	Outcome string `json:"outcome,omitempty"`
}

// What came of a request of a person, in the words of the state of the task, of a report
// and of an orchestrator: an answer that is not "yes" is not a reason to ask again, and
// an answer that is "yes" is not a reason to ask a second question about it.
const (
	// WaitTimeout is a request nobody answered in the time of the wait: the window of the
	// system was open and closed and the answer came after it. It is the one outcome a
	// continuation is asked about again, because nothing has been decided.
	WaitTimeout = "timeout"
	// WaitCompleted is a request the person allowed: the key of the App was read, the
	// run went on, and there is nothing left to ask about.
	WaitCompleted = "completed"
	// WaitDenied is a request the person refused. It is not asked again: a person who
	// said no to this build of crewflow reading the key of the App of the host has
	// answered, and a run that goes on anyway is a run that asked twice (RP-006, §7i).
	WaitDenied = "denied"
)

// The events of a request of a person, as a journal and an orchestrator read them: the
// name of what crewflow asked for, and then how it came out. The names are of the design
// of the task journal (§6a, §7f) and are written into the journal of the attempt today,
// which is the only journal there is (docs/DESIGN.md §7).
const (
	// EventAuthorizationRequired is the request itself: what is being asked, where, of
	// which resource and for what action, and the point the run goes on from.
	EventAuthorizationRequired = "HUMAN_AUTHORIZATION_REQUIRED"
	// EventAuthorizationCompleted is the person allowing it.
	EventAuthorizationCompleted = "HUMAN_AUTHORIZATION_COMPLETED"
	// EventAuthorizationDenied is the person refusing it.
	EventAuthorizationDenied = "HUMAN_AUTHORIZATION_DENIED"
	// EventAuthorizationTimeout is nobody answering the window of the system in time.
	EventAuthorizationTimeout = "HUMAN_AUTHORIZATION_TIMEOUT"
)

// sayEvent writes one event of a request of a person into the journal of an attempt: the
// name first, because that is what a program and a person look for, and then the facts of
// the request and the point to go on from. A journal is read by a person long after the
// window of the system has closed, and an event without them says nothing they can act on
// (docs/DESIGN.md §7i).
func (c Checkpoint) sayEvent(out io.Writer, event string) {
	if out == nil {
		return
	}
	fmt.Fprintf(out, "crewflow: event %s channel=%s resource=%s action=%s step=%s head=%s at=%s\n",
		event, c.Channel, c.Resource, c.Action, c.Step, c.Head, c.At.Format(time.RFC3339))
}

// Refused is why a run of a task cannot go on from the point it stands at: a name for a
// program and a sentence for a person, because a person who is told why is one step from
// fixing it and a refusal that says only "no" is a wall (docs/DESIGN.md §7i).
type Refused struct {
	// Name is the reason of the refusal as a program reads it, one of the words below.
	Name string
	// Said is the sentence a report and a terminal hold: what happened and what may be
	// done about it.
	Said string
}

// Error is the sentence of the refusal, because a refusal is an error of a command and a
// command prints what it has.
func (r Refused) Error() string { return r.Said }

// The reasons a continuation is refused, as an orchestrator reads them out of a report or
// out of `-json`.
const (
	// RefusedNoCheckpoint is a task with no point to go on from: it was never run here,
	// or the run of it has already gone past the step the point was written at.
	RefusedNoCheckpoint = "no-checkpoint"
	// RefusedUnknownStep is a point written by a step of crewflow this build cannot go
	// on from. It is refused and not guessed at: a continuation of a step nobody knows
	// is a new run with the words of a continuation.
	RefusedUnknownStep = "unknown-step"
	// RefusedAnswered is a point whose request came already: the run of the task went on
	// from it and there is nothing left to continue.
	RefusedAnswered = "authorization-answered"
	// RefusedDenied is a point whose request the person refused.
	RefusedDenied = "authorization-denied"
	// RefusedStale is a point older than [CheckpointValid].
	RefusedStale = "stale-checkpoint"
	// RefusedTaskChanged is a point written for a task the tracker no longer holds as it
	// was: the body of the task is what the run was given and what a continuation would
	// go on with.
	RefusedTaskChanged = "task-changed"
	// RefusedHeadMoved is a point whose branch stands at another commit: the work and
	// the session of the run are of that commit and not of this one.
	RefusedHeadMoved = "head-moved"
)

// checkpoint is the point the run of the task goes on from, and every refusal of a
// continuation — checked here, once, in the order that costs the machine least: what the
// state of the task holds first, then the task itself, and git last.
//
// Not one of these refusals asks the machine for the key of the App. A continuation is
// started by a person, and a person who is told that it cannot be started has to get that
// answer without a window of the system opening in front of them: every attempt to read a
// key without the access opens a window of its own, and a program that retries on its own
// is a program that opens windows by itself (docs/DESIGN.md §7i).
func (r *runner) checkpoint(ctx context.Context) error {
	path := r.journals.StatePath(r.task.Number)
	state, err := LoadState(path)
	switch {
	case os.IsNotExist(err):
		return Refused{Name: RefusedNoCheckpoint, Said: fmt.Sprintf(
			"task %d was never run on this machine, so there is no point to go on from: "+
				"run it with `crewflow task run %d`", r.task.Number, r.task.Number)}
	case err != nil:
		return err
	}
	point := state.Checkpoint
	if point == nil {
		return r.nothingToGoOnFrom(state)
	}
	if point.Step != stepReadKey {
		return Refused{Name: RefusedUnknownStep, Said: fmt.Sprintf(
			"the point of task %d stands at the step %q, and this build of crewflow goes on only "+
				"from %q: run the task anew with `crewflow task run %d`",
			r.task.Number, point.Step, stepReadKey, r.task.Number)}
	}
	switch point.Outcome {
	case WaitCompleted:
		return Refused{Name: RefusedAnswered, Said: fmt.Sprintf(
			"the decision of the point of task %d came on %s and the run went on from it: "+
				"there is nothing left to continue, and what came of that run is in its journal",
			r.task.Number, saidAt(point.DecidedAt))}
	case WaitDenied:
		return Refused{Name: RefusedDenied, Said: fmt.Sprintf(
			"the decision of the point of task %d was refused on %s, and a refusal is not asked again: "+
				"run the task anew with `crewflow task run %d` when this build may read the key of the app, "+
				"or go on in the worktree by hand",
			r.task.Number, saidAt(point.DecidedAt), r.task.Number)}
	}
	if r.env.Now().Sub(point.At) > CheckpointValid {
		return Refused{Name: RefusedStale, Said: fmt.Sprintf(
			"the point of task %d was written on %s and a point is good for %s: it is not what this "+
				"machine and this task are now. Run the task anew with `crewflow task run %d`",
			r.task.Number, saidAt(point.At), Idle(CheckpointValid), r.task.Number)}
	}
	if fingerprint(r.task) != point.Assignment {
		return Refused{Name: RefusedTaskChanged, Said: fmt.Sprintf(
			"the task %d is not the one the point was written for: it has been changed since %s, and "+
				"the run would go on with a task it was not given. Run it anew with `crewflow task run %d`",
			r.task.Number, saidAt(point.At), r.task.Number)}
	}
	head, err := r.head(ctx, state.Worktree)
	if err != nil {
		return err
	}
	if head != point.Head {
		return Refused{Name: RefusedHeadMoved, Said: fmt.Sprintf(
			"the branch of task %d stands at %s and the point of it was written at %s: the work of "+
				"that run is of the other commit. Run the task anew with `crewflow task run %d`, or go on "+
				"in the worktree by hand",
			r.task.Number, shortOf(head), shortOf(point.Head), r.task.Number)}
	}
	r.point = point
	return nil
}

// nothingToGoOnFrom is the refusal of a task that has a state and no point in it. The two
// cases are told apart because they lead to different places: a run that is still going is
// answered in the window it stands at, and a run that has gone past is a task whose next
// step is the orchestrator's (§6a, §7a).
func (r *runner) nothingToGoOnFrom(state State) error {
	last := state.Attempts[len(state.Attempts)-1]
	if last.Outcome == Running {
		standing := last.Reason
		if standing == "" {
			standing = "something crewflow does not name in the state of the task"
		}
		return Refused{Name: RefusedNoCheckpoint, Said: fmt.Sprintf(
			"the run of task %d is still going: it stands at %s. Answer the window of the system it "+
				"waits for — the run goes on by itself — and come back to a point only if the wait is over",
			r.task.Number, standing)}
	}
	if last.Outcome == Blocked {
		if _, in := windowOf(last.Reason); in {
			// A run that stopped at the window of the system and left no point could
			// not say what the branch of the task stood at: a point crewflow cannot
			// check is not a point to go on from, and the run that is to be made is a
			// new one (§7i).
			return Refused{Name: RefusedNoCheckpoint, Said: fmt.Sprintf(
				"the run of task %d stopped at the window of %s and left no point to go on from: "+
					"crewflow could not say what the branch of the task stood at. Run the task anew with "+
					"`crewflow task run %d`",
				r.task.Number, named(last.Reason), r.task.Number)}
		}
	}
	return Refused{Name: RefusedNoCheckpoint, Said: fmt.Sprintf(
		"task %d has no point to go on from: the last run of it came out as %q, which is not a run "+
			"that needs a decision of a person. Continue it with `crewflow task run %d -continue \"what is "+
			"left to do\"` if it is not over",
		r.task.Number, last.Outcome, r.task.Number)}
}

// head is the commit the worktree of the task stands at, as git says it: the head of the
// branch of the task is the fact a point is checked against, and git is what holds it
// (docs/DESIGN.md §7i).
func (r *runner) head(ctx context.Context, worktree string) (string, error) {
	head, err := r.output(ctx, worktree, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("the head of the branch of the task: %w", err)
	}
	return strings.TrimSpace(head), nil
}

// fingerprint is what crewflow remembers of a task at the moment it writes a point: a
// digest of the number, the title and the body of it, and nothing else. It is enough to
// say whether the task the tracker holds is the one the run was given, and it is short
// because a state file is read by a person who is looking for the branch, not for a hash
// of an issue (docs/DESIGN.md §7h).
func fingerprint(t forge.Task) string {
	sum := sha256.Sum256([]byte(strconv.Itoa(t.Number) + "\n" + t.Title + "\n" + t.Body))
	return hex.EncodeToString(sum[:6])
}

// shortOf is a commit as a report of it names it: the first seven letters are as much of a
// commit as a person reads and compares, and a full sha in a refusal is a wall of digits
// between the person and the reason.
func shortOf(sha string) string {
	if len(sha) <= 7 {
		return sha
	}
	return sha[:7]
}

// saidAt is when a thing happened, as a refusal of it names it: the date is enough, and a
// refusal is read by a person who wants to know whether the run is from today.
func saidAt(at time.Time) string {
	return at.Format("2006-01-02 15:04")
}
