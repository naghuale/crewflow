package run

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/secret"
)

// The head of the branch of the task of the tests of the backlog: the immutable record a
// transition rests on, and the only payload the backlog keeps (docs/DESIGN.md §7h).
const (
	headOfTheFirst  = "9f1c0de4a4a0b1f2c3d4e5f60718293a4b5c6d7"
	headOfTheSecond = "1c0de4a4a0b1f2c3d4e5f60718293a4b5c6d79f"
)

// went is a transition of the state of a task from one state of §6a to another, on the record
// the branch of the task stood at when it went.
func went(from, to AttentionState, at time.Time, head string) Transition {
	return Transition{From: from, To: to, At: at, Basis: head}
}

// TestATransitionIsKeptWithTheChangeThatCarriesIt: the news about a change of a task and the
// change itself are one write under the lock of the task. A subscriber that read the record
// between them would see a task that went from A to B and would never hear about it, and a
// backlog that came out of a separate file is a backlog a crewflow that was killed between the
// two writes does not have. Both are in the file, and they are in the file the road wrote once
// (docs/DESIGN.md §7h, #243).
func TestATransitionIsKeptWithTheChangeThatCarriesIt(t *testing.T) {
	journals := newJournals(t.TempDir(), "naghuale-crewflow")
	path := journals.StatePath(43)
	at := monday.Add(9 * time.Hour)

	written, err := RecordPendingEvent(nil, path,
		went(AttentionStands, AttentionAwaitsReview, at, headOfTheFirst),
		func(state State) (State, error) {
			return state.NextAttempt(state.NextNumber(), StartOf{
				Started: at, Step: stepExecutor, Journal: journals.JournalPath(43, 1),
			}), nil
		})
	if err != nil {
		t.Fatalf("RecordPendingEvent returned an error: %v", err)
	}

	if len(written.Attempts) != 1 || len(written.PendingEvents) != 1 {
		t.Fatalf("the state written holds %d attempts and %d transitions, want the change and the news about it",
			len(written.Attempts), len(written.PendingEvents))
	}
	kept, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState returned an error: %v", err)
	}
	if len(kept.Attempts) != 1 {
		t.Errorf("the state on the disk holds %d attempts, want the change of the task in it", len(kept.Attempts))
	}
	event, held := kept.pendingEvent(written.PendingEvents[0].ID)
	if !held {
		t.Fatalf("the backlog of the task on the disk holds %v, want the transition %s→%s",
			kept.PendingEvents, AttentionStands, AttentionAwaitsReview)
	}
	if event.From != AttentionStands || event.To != AttentionAwaitsReview {
		t.Errorf("the transition in the backlog is %s→%s, want %s→%s",
			event.From, event.To, AttentionStands, AttentionAwaitsReview)
	}
	if event.Basis != headOfTheFirst {
		t.Errorf("the transition rests on %q, want the record the branch of the task stood at: %q",
			event.Basis, headOfTheFirst)
	}
	if !event.At.Equal(at) {
		t.Errorf("the transition is at %s, want %s", event.At, at)
	}
	if kept.Revision != written.Revision || written.Revision != 1 {
		t.Errorf("the record of the task stands at the revision %d (written as %d), want the first change of "+
			"the record of a task that was not there", kept.Revision, written.Revision)
	}
}

// TestTheBacklogOfATaskIsThereAfterTheProcessThatWroteItIsGone: the record of a task is
// written whole or not at all through a file of its own and moved over the old one, so the
// backlog of a task survives the crewflow that wrote it being killed — a separate file of the
// events, and an intent written before the state, are what do not survive that (D-068 FINDING-4,
// docs/DESIGN.md §7h, #243).
func TestTheBacklogOfATaskIsThereAfterTheProcessThatWroteItIsGone(t *testing.T) {
	journals := newJournals(t.TempDir(), "naghuale-crewflow")
	path := journals.StatePath(43)
	at := monday.Add(9 * time.Hour)
	keeps(t, path, State{Number: 43, Title: "the run of a task", Branch: "crewflow/43-the-run-of-a-task"})

	written, err := RecordPendingEvent(nil, path, went(AttentionStands, AttentionAwaitsReview, at, headOfTheFirst),
		func(state State) (State, error) {
			return state.SettledBy(at, ReasonChangeMerged), nil
		})
	if err != nil {
		t.Fatalf("RecordPendingEvent returned an error: %v", err)
	}

	// Nothing of the process that wrote it is left: the record moved over the old one through
	// a file of its own and took the file with it.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read the folder of the state: %v", err)
	}
	for _, entry := range entries {
		if entry.Name() != "43.json" && entry.Name() != "43.json.lock" {
			t.Errorf("the folder of the state holds %q, want only the record of the task and its lock",
				entry.Name())
		}
	}

	// The process that comes next reads the record from the disk and sees the whole of it.
	after, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState returned an error: %v", err)
	}
	if len(after.PendingEvents) != 1 || after.PendingEvents[0].ID != written.PendingEvents[0].ID {
		t.Errorf("the backlog read back is %v, want the transition that was written", after.PendingEvents)
	}
	if after.Revision != written.Revision {
		t.Errorf("the record read back stands at the revision %d, want %d", after.Revision, written.Revision)
	}
	if after.Settled == nil {
		t.Error("the change of the task was lost on the way through the atomic write, want it in the record")
	}
}

// TestABacklogThatIsFullRefusesTheTransitionAndKeepsTheRecord: the backlog is bounded in the
// number of its records, because a state of a task is written whole and read by a person
// looking for the branch. A transition that does not fit is refused whole — with the change it
// belongs to, and with nothing compensating for it afterwards: the record on the disk stays the
// one that was there, byte for byte, and crewflow does not go back to it to make room or to
// write the change without the news (docs/DESIGN.md §7h, #243).
func TestABacklogThatIsFullRefusesTheTransitionAndKeepsTheRecord(t *testing.T) {
	journals := newJournals(t.TempDir(), "naghuale-crewflow")
	path := journals.StatePath(43)
	at := monday.Add(9 * time.Hour)
	keeps(t, path, State{Number: 43, Title: "the run of a task"})

	for one := range PendingLimit {
		if _, err := RecordPendingEvent(nil, path,
			went(AttentionStands, AttentionAwaitsReview, at.Add(time.Duration(one)*time.Hour),
				headOfTheAt(one)), unchanged); err != nil {
			t.Fatalf("RecordPendingEvent %d of %d returned an error: %v", one+1, PendingLimit, err)
		}
	}
	full := read(t, path)
	state := stateOfTheTask(t, path)
	if len(state.PendingEvents) != PendingLimit {
		t.Fatalf("the backlog holds %d transitions, want the %d it holds", len(state.PendingEvents), PendingLimit)
	}

	_, err := RecordPendingEvent(nil, path,
		went(AttentionAwaitsReview, AttentionFinishedUnseen, at.Add(9*time.Hour), headOfTheAt(PendingLimit)),
		func(current State) (State, error) {
			return current.NextAttempt(current.NextNumber(), StartOf{
				Started: at.Add(9 * time.Hour), Step: stepExecutor, Journal: journals.JournalPath(43, 1),
			}), nil
		})

	if !errors.Is(err, ErrPendingLimitReached) {
		t.Fatalf("the transition into a full backlog = %v, want %v", err, ErrPendingLimitReached)
	}
	if !strings.Contains(err.Error(), "8") {
		t.Errorf("the refusal is %q, want it to say how full the backlog is", err)
	}
	if kept := read(t, path); kept != full {
		t.Errorf("the record of the task after the refusal is\n%s\nwant the one that was there\n%s", kept, full)
	}
	state = stateOfTheTask(t, path)
	if len(state.Attempts) != 0 {
		t.Errorf("the change the refused transition belonged to was written all the same: %d attempts in the record",
			len(state.Attempts))
	}
	if len(state.PendingEvents) != PendingLimit {
		t.Errorf("the backlog holds %d transitions after the refusal, want the %d that were there",
			len(state.PendingEvents), PendingLimit)
	}
}

// TestATransitionThatIsInTheBacklogAlreadyIsNotWrittenOverIt: the name of a record of the
// backlog is a digest of the transition and of the record it rests on, and the same transition
// on the same record is one event however many times it is written. The record that is there is
// kept whole — a subscriber that was told nothing about it yet must be able to read the same
// event again (docs/DESIGN.md §7h, #243).
func TestATransitionThatIsInTheBacklogAlreadyIsNotWrittenOverIt(t *testing.T) {
	journals := newJournals(t.TempDir(), "naghuale-crewflow")
	path := journals.StatePath(43)
	at := monday.Add(9 * time.Hour)
	keeps(t, path, State{Number: 43, Title: "the run of a task"})
	transition := went(AttentionStands, AttentionAwaitsReview, at, headOfTheFirst)
	if _, err := RecordPendingEvent(nil, path, transition, unchanged); err != nil {
		t.Fatalf("RecordPendingEvent returned an error: %v", err)
	}
	first := read(t, path)

	_, err := RecordPendingEvent(nil, path, transition, unchanged)

	if !errors.Is(err, ErrPendingExists) {
		t.Fatalf("the transition that is in the backlog already = %v, want %v", err, ErrPendingExists)
	}
	if kept := read(t, path); kept != first {
		t.Errorf("the record of the task after the refusal is\n%s\nwant the one that was there\n%s", kept, first)
	}
	if state := stateOfTheTask(t, path); len(state.PendingEvents) != 1 {
		t.Errorf("the backlog holds %d transitions, want the one that was there", len(state.PendingEvents))
	}
}

// TestARecordOfTheBacklogThatDoesNotFitIsRefused: a state of a task is not a place for a
// payload, and a record of the backlog is bounded in its size as well as in the number of
// records. A transition whose record does not fit is refused with the change it belongs to,
// whatever a caller put into it (docs/DESIGN.md §7h, #243).
func TestARecordOfTheBacklogThatDoesNotFitIsRefused(t *testing.T) {
	journals := newJournals(t.TempDir(), "naghuale-crewflow")
	path := journals.StatePath(43)
	keeps(t, path, State{Number: 43, Title: "the run of a task"})
	written := read(t, path)

	_, err := RecordPendingEvent(nil, path,
		went(AttentionStands, AttentionAwaitsReview, monday.Add(9*time.Hour),
			strings.Repeat("a", PendingRecordLimit*2)),
		unchanged)

	if !errors.Is(err, ErrPendingTooLarge) {
		t.Fatalf("the transition with a record that does not fit = %v, want %v", err, ErrPendingTooLarge)
	}
	if kept := read(t, path); kept != written {
		t.Errorf("the record of the task after the refusal is\n%s\nwant the one that was there\n%s", kept, written)
	}
}

// TestATransitionBetweenWordsThatAreNotTheStatesIsRefused: what the task went from and to are
// the states of the queue of §6a, and a word out of the closed list of them is a transition
// nobody can read — the state of a task is not written with one, and the refusal names the two
// words and not the record they were made up with (docs/DESIGN.md §6a, §7h, #243).
func TestATransitionBetweenWordsThatAreNotTheStatesIsRefused(t *testing.T) {
	journals := newJournals(t.TempDir(), "naghuale-crewflow")
	path := journals.StatePath(43)
	keeps(t, path, State{Number: 43, Title: "the run of a task"})
	written := read(t, path)

	for _, transition := range []Transition{
		went(AttentionState("in-progress"), AttentionAwaitsReview, monday.Add(9*time.Hour), headOfTheFirst),
		went(AttentionStands, AttentionState("merged"), monday.Add(9*time.Hour), headOfTheFirst),
	} {
		_, err := RecordPendingEvent(nil, path, transition, unchanged)
		if !errors.Is(err, ErrPendingStateUnknown) {
			t.Fatalf("the transition %q→%q = %v, want %v", transition.From, transition.To, err, ErrPendingStateUnknown)
		}
		for _, want := range []string{"§6a", string(transition.From), string(transition.To)} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal %q does not hold %q", err, want)
			}
		}
		if strings.Contains(err.Error(), headOfTheFirst) {
			t.Errorf("the refusal %q holds the record the transition rests on, want no payload in it", err)
		}
	}
	if kept := read(t, path); kept != written {
		t.Errorf("the record of the task after the refusals is\n%s\nwant the one that was there\n%s", kept, written)
	}
}

// TestOnlyTheConfirmedEventsAreTakenOutOfTheBacklog: the confirmation of what was told is made
// against the backlog as it is there now, under the lock of the task. An event another writer
// added while the list of confirmed names was being made is not in that list and stays where
// it is — and the task that went from A to B went once: the confirmation does not move the
// revision of the record and writes no event of its own (docs/DESIGN.md §7h, #243).
func TestOnlyTheConfirmedEventsAreTakenOutOfTheBacklog(t *testing.T) {
	journals := newJournals(t.TempDir(), "naghuale-crewflow")
	path := journals.StatePath(43)
	at := monday.Add(9 * time.Hour)
	keeps(t, path, State{Number: 43, Title: "the run of a task"})
	first := recorded(t, path, went(AttentionStands, AttentionAwaitsReview, at, headOfTheFirst))
	second := recorded(t, path, went(AttentionAwaitsReview, AttentionFinishedUnseen, at, headOfTheSecond))

	// The subscriber read the backlog and decided that these two were told. While it was
	// deciding, another writer recorded the third one.
	third := recorded(t, path, went(AttentionFinishedUnseen, AttentionStopped, at.Add(time.Hour), headOfTheAt(3)))
	before := stateOfTheTask(t, path)

	kept, err := ConfirmPendingEvents(nil, path, []string{first.ID, second.ID})
	if err != nil {
		t.Fatalf("ConfirmPendingEvents returned an error: %v", err)
	}
	if kept.Revision != before.Revision {
		t.Errorf("the record of the task stands at the revision %d after the confirmation, want %d: "+
			"a confirmation of what was told is not a change of the task", kept.Revision, before.Revision)
	}
	if len(kept.PendingEvents) != 1 || kept.PendingEvents[0].ID != third.ID {
		t.Fatalf("the backlog after the confirmation holds %v, want only the event that was added while it was made",
			idsOf(kept))
	}
	if event, held := kept.pendingEvent(third.ID); !held || event.Basis != headOfTheAt(3) {
		t.Errorf("the event that was added while the confirmation was made is %+v, want it whole in the backlog", event)
	}
	// The confirmation wrote no event of its own: the backlog is what is left of it, and a
	// task that went from A to B has one event and not two.
	after := stateOfTheTask(t, path)
	if len(after.PendingEvents) != 1 || after.Revision != before.Revision {
		t.Errorf("the backlog on the disk is %v at the revision %d, want one event at the revision %d",
			idsOf(after), after.Revision, before.Revision)
	}
}

// TestTheConfirmationOfAnEventThatIsNotInTheBacklogIsRefused: the backlog is the only record of
// what has not been told about a task yet, so a confirmation of a name that is not in it says
// nothing about what is — and a confirmation that took something out on a name nobody checked
// would take out whatever else holds that name. The refusal is written before the record on the
// disk is touched (docs/DESIGN.md §7h, #243).
func TestTheConfirmationOfAnEventThatIsNotInTheBacklogIsRefused(t *testing.T) {
	journals := newJournals(t.TempDir(), "naghuale-crewflow")
	path := journals.StatePath(43)
	keeps(t, path, State{Number: 43, Title: "the run of a task"})
	event := recorded(t, path, went(AttentionStands, AttentionAwaitsReview, monday.Add(9*time.Hour), headOfTheFirst))
	written := read(t, path)

	for _, name := range []string{"000000000000", "", event.ID + "0"} {
		if _, err := ConfirmPendingEvents(nil, path, []string{name}); !errors.Is(err, ErrPendingNotFound) {
			t.Errorf("the confirmation of %q = %v, want %v", name, err, ErrPendingNotFound)
		}
		if kept := read(t, path); kept != written {
			t.Errorf("the record of the task after the refusal of %q is\n%s\nwant the one that was there\n%s",
				name, kept, written)
		}
	}
}

// TestTheBacklogOfATaskSurvivesTheQueueAroundIt: the backlog lives in the record of the task
// together with everything else about it, so a writer of the queue of attention — a change that
// writes only the memory of what the host said — leaves the transitions that were not told yet
// where they are. A backlog kept beside the record and written by a road of its own would be
// taken away by the writer that wrote the whole record last (D-068 FINDING-4, docs/DESIGN.md §7h).
func TestTheBacklogOfATaskSurvivesTheQueueAroundIt(t *testing.T) {
	journals := newJournals(t.TempDir(), "naghuale-crewflow")
	path := journals.StatePath(43)
	at := monday.Add(9 * time.Hour)
	keeps(t, path, State{Number: 43, Title: "the run of a task"})
	event := recorded(t, path, went(AttentionStands, AttentionAwaitsReview, at, headOfTheFirst))

	// The queue leaves a record under the task and remembers what the host said about it.
	if _, err := UpdateState(path, func(state State) (State, error) {
		return state.Noticed(at, "43:stands:no-progress").SettledBy(at, ReasonChangeMerged), nil
	}); err != nil {
		t.Fatalf("UpdateState of the queue returned an error: %v", err)
	}

	state := stateOfTheTask(t, path)
	if event, held := state.pendingEvent(event.ID); !held || event.Basis != headOfTheFirst {
		t.Errorf("the backlog of the task holds %v, want the transition that was not told yet", idsOf(state))
	}
	if state.Settled == nil || state.Notice == nil {
		t.Error("the memory of the queue is not in the record, want it beside the backlog")
	}
}

// TestTheBacklogOfATaskIsNotWrittenWithTheValuesOfARun: a transition of a task is told by the
// record it rests on, and the backlog has no field of the words of a run in it: a boundary that
// has a password to cut has nothing to cut in the payload of an event. The record of the task
// is published through the boundary of the run all the same, and the transition that rests on
// what the branch of the task stands at goes in whole (docs/DESIGN.md §7e, §7h, #243).
func TestTheBacklogOfATaskIsNotWrittenWithTheValuesOfARun(t *testing.T) {
	journals := newJournals(t.TempDir(), "naghuale-crewflow")
	path := journals.StatePath(43)
	const password = "s3cret-of-the-run"
	out := secret.NewOut(secret.Chosen(password)...)
	at := monday.Add(9 * time.Hour)
	keeps(t, path, State{Number: 43, Title: "the run of a task"})

	if _, err := RecordPendingEvent(out, path,
		went(AttentionStands, AttentionAwaitsReview, at, headOfTheFirst),
		func(state State) (State, error) {
			return state.Reason(1, password+": the run asked for "+password), nil
		}); err != nil {
		t.Fatalf("RecordPendingEvent returned an error: %v", err)
	}

	written := read(t, path)
	if strings.Contains(written, password) {
		t.Fatalf("the record of the task holds a value of the run:\n%s", written)
	}
	state := stateOfTheTask(t, path)
	if len(state.PendingEvents) != 1 || state.PendingEvents[0].Basis != headOfTheFirst {
		t.Errorf("the backlog holds %v, want the transition on the record the branch of the task stood at",
			idsOf(state))
	}
}

// recorded is the record the backlog of the task holds after one transition written into it, and
// it fails the test where the transition was not written.
func recorded(t *testing.T, path string, transition Transition) PendingEvent {
	t.Helper()
	written, err := RecordPendingEvent(nil, path, transition, unchanged)
	if err != nil {
		t.Fatalf("RecordPendingEvent %s→%s returned an error: %v", transition.From, transition.To, err)
	}
	if len(written.PendingEvents) == 0 {
		t.Fatalf("the backlog of the task holds nothing, want the transition %s→%s",
			transition.From, transition.To)
	}
	return written.PendingEvents[len(written.PendingEvents)-1]
}

// unchanged is a change of a state of a task that changes nothing in it: a test that writes only
// a transition into the backlog of a task and does not touch the task itself says so.
func unchanged(state State) (State, error) { return state, nil }

// stateOfTheTask is the record of the task as it is on the disk, read the way a process that is
// not the one that wrote it reads it.
func stateOfTheTask(t *testing.T, path string) State {
	t.Helper()
	state, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState returned an error: %v", err)
	}
	return state
}

// idsOf is what the backlog of a record holds, by its names: a test of the backlog says what is
// in it and not what is not.
func idsOf(state State) []string {
	ids := make([]string, 0, len(state.PendingEvents))
	for _, event := range state.PendingEvents {
		ids = append(ids, event.ID)
	}
	return ids
}

// headOfTheAt is the commit at the head of the branch of the task of a test, one for every
// transition it records: two transitions on one record are one event, and a test that wants two
// events in the backlog needs two records to rest them on.
func headOfTheAt(at int) string {
	return fmt.Sprintf("%040x", at+1)
}
