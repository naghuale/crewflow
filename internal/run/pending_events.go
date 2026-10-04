package run

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/naghuale/crewflow/internal/secret"
)

// The backlog of the events of a task: the transitions of its state that nobody has been told
// about yet, kept in the record of the task itself.
//
// A separate intent written before the state and a single pending field beside it both lose
// events — one of them has to survive a crewflow that was killed between the two writes, and the
// other says that something happened without saying what — and either of them can publish a
// transition the state never took. So the transition and the change of the record are one write
// under the lock of the task, the backlog has a bound, and the confirmation of a told event takes
// it out under the same lock (docs/DESIGN.md §7h, #243).
//
// Nothing in this file produces events: a producer is written when it can prove that the
// immutable record the event rests on is there and will stay there, and a digest of a record
// nobody may read again is not that proof. What is here is the storage and the bounds, and a
// caller of the two roads below (#243).

// PendingEvent is one transition of the state of a task that has not been told to anybody yet:
// the two states of §6a the task went from and to, when it went, and the immutable record the
// transition rests on — the commit at the head of its branch.
//
// There is nothing of a run in it. A subscriber is told what the task did by the record the
// transition rests on and by nothing else, and a field of words of a person or of a program in
// the backlog of a task is a field nobody looked at when the event was told (docs/DESIGN.md §7e, §7h).
type PendingEvent struct {
	// ID is what the backlog keeps the event under and what a confirmation of it names. It is
	// a digest of the revision the record took when the event was written, of the transition
	// and of the whole record it rests on, and no caller writes it.
	//
	// The revision is in it because it is what tells two changes of a task apart: the same
	// transition on the same commit, told once before a change of the title and once after it,
	// is two events of two versions of the record and not one event told twice. The name of an
	// event is therefore bound to the one version of the record it was told in, and it stays
	// that way for as long as the event is in the backlog.
	ID string `json:"id"`
	// At is when the task went from From to To.
	At time.Time `json:"at"`
	// From and To are the two states of §6a, and both of them are words of the closed list of
	// them: a word out of the list is a transition of nothing, and the state of a task is not
	// written with one.
	From AttentionState `json:"from"`
	To   AttentionState `json:"to"`
	// Basis is the record the transition rests on: the commit at the head of the branch of the
	// task when it went. It is the whole of the payload of the event, and it is kept because
	// it can be read again — a transition of nothing anyone may go and look at is a claim.
	Basis string `json:"basis"`
}

// The bounds of the backlog. A state of a task is written whole and kept for ever, and a person
// reads it looking for the branch of the task (docs/DESIGN.md §7h), so a backlog that nobody
// confirmed is bounded in the number of its records and in the size of each of them: at the limit
// a task has written eight records of at most 512 bytes each, and one transition more is refused
// whole.
const (
	// PendingLimit is the highest number of events the backlog of a task holds. A task whose
	// transitions nobody confirms is a task crewflow has to be told about, and the refusal
	// below says which task and which kind of backlog it is.
	PendingLimit = 8
	// PendingRecordLimit is the highest size of one record of the backlog as it is written, in
	// bytes. The forms of its fields are what keep a record short — a word of a closed list
	// and a commit of the host — and the limit is what says so out loud: a record that does
	// not fit is refused with the transition it belongs to, whatever a caller put in it.
	PendingRecordLimit = 512
)

// The refusals of the backlog. Every one of them is a name a caller can tell apart, and every
// one of them is told before a byte of the record of the task is written: a transition refused
// as a whole is a transition nobody may act on as if it had happened.
var (
	// ErrPendingLimitReached is the refusal of a transition of a task whose backlog is full:
	// the transition and the change it belongs to are both not written, and nothing is tried
	// afterwards to make room for one of them.
	ErrPendingLimitReached = errors.New("pending-event-limit-reached")
	// ErrPendingExists is the refusal of a transition that the backlog holds already: a record
	// of the backlog is written once and is not overwritten by the same news again, and the
	// one that is there stays whole.
	ErrPendingExists = errors.New("pending-event-exists")
	// ErrPendingTooLarge is the refusal of a record of the backlog that does not fit the bound
	// of one record: a state of a task is not a place for a payload.
	ErrPendingTooLarge = errors.New("pending-event-too-large")
	// ErrPendingStateUnknown is the refusal of a transition between two words that are not the
	// states of §6a: what the task went from and to are the states of the queue, and a word
	// out of the list is a transition nobody can read.
	ErrPendingStateUnknown = errors.New("pending-event-state-unknown")
	// ErrPendingWithoutChange is the refusal of a transition of a task that the change beside it
	// did not make: the record of the task says the same thing it said before, so nothing was
	// committed and there is no transition of it to tell anybody about. The refusal comes
	// before the write, and the record on the disk stays what it was — neither the event nor
	// the change of it reaches the disk (docs/DESIGN.md §7h, #243).
	ErrPendingWithoutChange = errors.New("pending-event-without-change")
	// ErrPendingNotFound is the refusal of a confirmation of an event the backlog does not
	// hold: either the event was told already or the caller named what was never written, and
	// a confirmation that took something out of the backlog on a name nobody checked would
	// take out whatever else holds that name.
	ErrPendingNotFound = errors.New("pending-event-not-found")
)

// Transition is what a producer of the events of a task knows about a change of its state: the
// two states of §6a it went from and to, the moment of it, and the immutable record it rests
// on. Everything else about the change is in the state of the task itself — the transition is
// the news about the change and not a copy of it.
type Transition struct {
	// From and To are the states of §6a the task went from and to.
	From, To AttentionState
	// At is when it went.
	At time.Time
	// Basis is the record the transition rests on: the commit at the head of the branch of
	// the task at that moment. A transition with nothing behind it is not written.
	Basis string
}

// RecordPendingEvent writes the transition into the backlog of the task in the same write as the
// change it belongs to, under the lock of the task: a subscriber that read the record between
// the change and the news about it would see a task that went from A to B and would never hear
// about it, and the two are one write or neither of them is (docs.DESIGN.md §7h, #243).
//
// A transition is news of a change, and a change it can be news of. So the change is made
// first and the record it made is compared with the record that was on the disk: if the change
// left the record saying the same thing, the transition is refused whole
// (`pending-event-without-change`) — nothing was committed, and an event of a task that did not
// happen is exactly the uncommitted transition the backlog exists not to publish. The comparison
// is of what was read under the lock and of what the change made, because the change is made on
// the arrays behind the record it was given and a record compared with itself afterwards says
// that nothing changed.
//
// Then the event is added to the record the change made, named by the revision that record
// takes: two changes of a task committed one after another are two events even when the
// transition and the record it rests on are the same words.
//
// A transition that does not fit the backlog refuses the change with it, and nothing of either
// reaches the disk: the record on it stays the one that was there, byte for byte, and crewflow
// does not go back to it afterwards to make room or to write the change without the news — a
// transition whose event was lost is not a transition anybody may act on.
//
// The caller names the transition and the change and nothing else: the name of the event is a
// digest of it and of the revision, and the revision of the record is the road's word (#243).
func RecordPendingEvent(out *secret.Out, path string, was Transition, change func(State) (State, error)) (State, error) {
	return updateStateThrough(out, path, func(state State, wasRead persisted) (State, error) {
		// The transition is read before the change is made: a word of §6a that the format
		// does not name is refused as what it is, and not as a change that changed nothing.
		if _, err := eventOf(was, 0); err != nil {
			return State{}, err
		}
		changed, err := change(state)
		if err != nil {
			return State{}, err
		}
		same, err := saysTheSame(out, wasRead, changed)
		if err != nil {
			return State{}, err
		}
		if same {
			return State{}, fmt.Errorf("the change of the task left the record of it saying the same thing, so "+
				"nothing was committed and the transition %s→%s on %s is no news of it: %w",
				was.From, was.To, shortOf(was.Basis), ErrPendingWithoutChange)
		}
		revision, err := revisionOf(wasRead)
		if err != nil {
			return State{}, err
		}
		return changed.withPendingEvent(was, revision)
	})
}

// ConfirmPendingEvents takes out of the backlog the events whose ids are confirmed: under the
// lock of the task, and against the backlog as it is there now. An event another writer added
// while this list of confirmed names was being made is not in the list and stays where it is,
// and a name that is in the list and is not in the backlog is a refusal — the backlog is the
// only record of what has not been told yet, and a confirmation of what is not in it says
// nothing about what is (docs/DESIGN.md §7h, #243).
//
// The revision of the record does not move: the task went from A to B once, and a confirmation
// says the news about it has been told — it is not a second change of the task, and a subscriber
// that watches the revision must not see a change of the task where there was none.
func ConfirmPendingEvents(out *secret.Out, path string, confirmed []string) (State, error) {
	return UpdateStateThrough(out, path, func(state State) (State, error) {
		return state.withoutPendingEvents(confirmed)
	})
}

// withPendingEvent is the state of a task with the transition written into its backlog at that
// revision, and a refusal where it does not fit. The event is appended and never written over: a
// backlog is a queue of what is not told yet, and the record it holds is a record of a transition
// of the task that a subscriber may come to look at after the task went on changing.
//
// The revision is the one the record of the task takes with this event in it, and it is part of
// the name of the event: a record of the backlog holds each event once, and the same event twice
// in one record is the same transition told twice about one version of the record
// (docs/DESIGN.md §7h, #243).
func (s State) withPendingEvent(was Transition, revision int64) (State, error) {
	event, err := eventOf(was, revision)
	if err != nil {
		return s, err
	}
	if _, held := s.pendingEvent(event.ID); held {
		return s, fmt.Errorf("the transition %s→%s of the task on %s is in its backlog already, and a record of "+
			"the backlog is not written over: %w", was.From, was.To, shortOf(was.Basis), ErrPendingExists)
	}
	if len(s.PendingEvents) >= PendingLimit {
		return s, fmt.Errorf("the backlog of the task holds %d transitions and cannot hold one more: %w",
			len(s.PendingEvents), ErrPendingLimitReached)
	}
	size, err := json.Marshal(event)
	if err != nil {
		return s, fmt.Errorf("the transition of the task as it is written: %w", err)
	}
	if len(size) > PendingRecordLimit {
		return s, fmt.Errorf("the transition of the task is %d bytes as it is written and the record of a task is "+
			"never this big: %w", len(size), ErrPendingTooLarge)
	}
	kept := make([]PendingEvent, len(s.PendingEvents), len(s.PendingEvents)+1)
	copy(kept, s.PendingEvents)
	s.PendingEvents = append(kept, event)
	return s, nil
}

// withoutPendingEvents is the state of a task with the confirmed events taken out of its backlog
// and every other event left in it. Nothing is written over and nothing is added: the backlog
// after a confirmation is the backlog that was there, less what the confirmation named.
func (s State) withoutPendingEvents(confirmed []string) (State, error) {
	for _, id := range confirmed {
		if _, held := s.pendingEvent(id); !held {
			return s, fmt.Errorf("the event %q is confirmed and the backlog of the task does not hold it: %w",
				id, ErrPendingNotFound)
		}
	}
	kept := make([]PendingEvent, 0, len(s.PendingEvents))
	for _, event := range s.PendingEvents {
		if slices.Contains(confirmed, event.ID) {
			continue
		}
		kept = append(kept, event)
	}
	s.PendingEvents = kept
	return s, nil
}

// pendingEvent is the record the backlog holds under that name, and whether it holds one.
func (s State) pendingEvent(id string) (PendingEvent, bool) {
	at := slices.IndexFunc(s.PendingEvents, func(event PendingEvent) bool { return event.ID == id })
	if at < 0 {
		return PendingEvent{}, false
	}
	return s.PendingEvents[at], true
}

// eventOf is the record the backlog keeps the transition under at that revision, and a refusal
// where the transition is not between the states of §6a. The name of the record is a digest of
// it, and no caller brings one: a name a caller wrote is a name nobody checked.
func eventOf(was Transition, revision int64) (PendingEvent, error) {
	if !KnownState(was.From) || !KnownState(was.To) {
		return PendingEvent{}, fmt.Errorf("the transition %q→%q of the task is not between the states of §6a, "+
			"and what the task went from and to are the states of the queue: %w",
			was.From, was.To, ErrPendingStateUnknown)
	}
	return PendingEvent{
		ID:    idOf(was, revision),
		At:    was.At,
		From:  was.From,
		To:    was.To,
		Basis: was.Basis,
	}, nil
}

// idOf is what the backlog keeps the transition under at that revision: a digest of the revision
// the record of the task takes with the event in it, of the two states of the transition and of
// the whole record it rests on. The revision and the basis are both of it in full — a transition
// on another commit, and the same transition told again after another change of the task, are two
// events and not one — and the moment of it is not: the moment is part of the record of the event
// and not part of the name of it. It is kept short because a state file is read by a person who is
// looking for the branch of the task, not for a hash (docs/DESIGN.md §7h, #243).
func idOf(was Transition, revision int64) string {
	sum := sha256.Sum256([]byte(strconv.FormatInt(revision, 10) + "\x00" + string(was.From) + "\x00" +
		string(was.To) + "\x00" + was.Basis))
	return hex.EncodeToString(sum[:6])
}
