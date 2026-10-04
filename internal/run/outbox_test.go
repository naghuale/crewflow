package run

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/secret"
)

// TestAMaterializedEventIsOneLineOfTheOutboxAndTheBacklogIsEmpty: the outbox is the projection of
// the backlog that is not written over, and one line of it is one event of the task: the news of
// the transition with the whole of the record it rests on, under the name the backlog holds it by
// and in the place it took in the file. The backlog of the task lets the event go in the same
// call — that is what lets the outbox be the record of what was told rather than a second copy of
// what was not — and the revision of the record of the task does not move: a confirmation of what
// was told is not a change of the task (docs/DESIGN.md §7h, #243, #247).
func TestAMaterializedEventIsOneLineOfTheOutboxAndTheBacklogIsEmpty(t *testing.T) {
	statePath, outboxPath := pathsOfATask(t)
	at := monday.Add(9 * time.Hour)
	keeps(t, statePath, State{Number: 43, Title: "the run of a task"})
	event := recorded(t, statePath, went(AttentionStands, AttentionAwaitsReview, at, headOfTheFirst))
	before := stateOfTheTask(t, statePath)

	done, err := MaterializePendingEvents(nil, statePath, outboxPath)

	if err != nil {
		t.Fatalf("MaterializePendingEvents returned an error: %v", err)
	}
	if len(done.Written) != 1 {
		t.Fatalf("the call wrote %v into the outbox, want one record of the event", done.Written)
	}
	wrote := done.Written[0]
	if wrote.EventID != event.ID || wrote.Seq != 1 {
		t.Errorf("the record is at the place %d under the name %q, want the first record under the name the "+
			"backlog holds the event by: %q", wrote.Seq, wrote.EventID, event.ID)
	}
	if !sameNews(wrote, event) {
		t.Errorf("the record is %+v, want the whole of the news of the transition %s→%s at %s on %s",
			wrote, event.From, event.To, event.At, event.Basis)
	}
	if len(done.Told) != 1 || done.Told[0] != event.ID {
		t.Errorf("the call confirmed %v, want the name of the event it wrote: %q", done.Told, event.ID)
	}
	if len(done.Duplicated) != 0 || len(done.Conflicted) != 0 {
		t.Errorf("the call reported %v duplicated and %v in conflict, want a file that held neither",
			done.Duplicated, done.Conflicted)
	}
	// One line per event, the line break being what says the record is in the file, and the line
	// is the record as the format of it writes it.
	lines := linesOfOutbox(t, outboxPath)
	if len(lines) != 1 {
		t.Fatalf("the outbox of the task holds %d lines, want one line per event: %q", len(lines), lines)
	}
	held, err := recordOfLine([]byte(lines[0]))
	if err != nil {
		t.Fatalf("the line of the outbox is not a record of it: %v", err)
	}
	if !sameRecord(held, wrote) {
		t.Errorf("the line of the outbox is %+v, want the record that was written: %+v", held, wrote)
	}
	after := stateOfTheTask(t, statePath)
	if len(after.PendingEvents) != 0 {
		t.Errorf("the backlog of the task on the disk holds %v, want nothing that was told", idsOf(after))
	}
	if after.Revision != before.Revision {
		t.Errorf("the record of the task stands at the revision %d after the materialization, want %d: what "+
			"was told is not a change of the task", after.Revision, before.Revision)
	}
}

// TestTheOutboxOfATaskIsOnlyAppendedTo: the record of a task is written whole and a person opens
// it looking for the branch, which is why the news of a transition is a projection of its own
// with a road of its own. A file that is rewritten on every event is a file that says what is in
// it now and nothing about what came before it, so the outbox is appended to and never rewritten:
// the lines that were there are the same bytes after the next event and the places go one after
// another (docs/DESIGN.md §7h, #247).
func TestTheOutboxOfATaskIsOnlyAppendedTo(t *testing.T) {
	statePath, outboxPath := pathsOfATask(t)
	at := monday.Add(9 * time.Hour)
	keeps(t, statePath, State{Number: 43, Title: "the run of a task"})
	first := recorded(t, statePath, went(AttentionStands, AttentionAwaitsReview, at, headOfTheFirst))

	one, err := MaterializePendingEvents(nil, statePath, outboxPath)
	if err != nil {
		t.Fatalf("MaterializePendingEvents returned an error: %v", err)
	}
	before := read(t, outboxPath)
	second := recorded(t, statePath, went(AttentionAwaitsReview, AttentionFinishedUnseen, at.Add(time.Hour),
		headOfTheSecond))

	two, err := MaterializePendingEvents(nil, statePath, outboxPath)

	if err != nil {
		t.Fatalf("the second MaterializePendingEvents returned an error: %v", err)
	}
	if len(two.Written) != 1 || two.Written[0].EventID != second.ID {
		t.Fatalf("the second call wrote %v, want one new record of the transition that was recorded", two.Written)
	}
	if two.Written[0].Seq != one.Written[0].Seq+1 {
		t.Errorf("the second record is at the place %d, want %d: the places of the outbox are the order the "+
			"events were materialized in", two.Written[0].Seq, one.Written[0].Seq+1)
	}
	after := read(t, outboxPath)
	if !strings.HasPrefix(after, before) {
		t.Errorf("the outbox after the second event is\n%s\nwant it to begin with what it held\n%s", after, before)
	}
	lines := linesOfOutbox(t, outboxPath)
	if len(lines) != 2 || lines[0] != before[:len(before)-1] {
		t.Errorf("the outbox holds %q, want the first line byte for byte as it was and one line after it", lines)
	}
	if !strings.Contains(lines[0], first.ID) {
		t.Errorf("the first line is %q, want the record of the event %q", lines[0], first.ID)
	}
}

// TestTwoMaterializersOfOneEventLeaveOneLine: the lock of the outbox file is of the file itself and
// serializes reading it, checking it, taking the next place in it and appending to it, so two
// materializers of one task at the same time do not write two lines of one event. Both of them
// finish without a refusal: the one that came second either sees the record already in the file and
// confirms the same news again, or finds the backlog of the task empty because the first one told
// it (R1, docs/DESIGN.md §7h).
func TestTwoMaterializersOfOneEventLeaveOneLine(t *testing.T) {
	const writers = 4
	statePath, outboxPath := pathsOfATask(t)
	keeps(t, statePath, State{Number: 43, Title: "the run of a task"})
	event := recorded(t, statePath, went(AttentionStands, AttentionAwaitsReview,
		monday.Add(9*time.Hour), headOfTheFirst))

	var group sync.WaitGroup
	results := make(chan Materialized, writers)
	failures := make(chan error, writers)
	for range writers {
		group.Add(1)
		go func() {
			defer group.Done()
			done, err := MaterializePendingEvents(nil, statePath, outboxPath)
			if err != nil {
				failures <- err
				return
			}
			results <- done
		}()
	}
	group.Wait()
	close(failures)
	close(results)
	for err := range failures {
		t.Errorf("MaterializePendingEvents of another materializer returned an error: %v", err)
	}

	lines := linesOfOutbox(t, outboxPath)
	if len(lines) != 1 {
		t.Fatalf("the outbox holds %d lines for one event, want one: %q", len(lines), lines)
	}
	held, err := recordOfLine([]byte(lines[0]))
	if err != nil {
		t.Fatalf("the line of the outbox is not a record of it: %v", err)
	}
	if held.EventID != event.ID || !sameNews(held, event) {
		t.Errorf("the line of the outbox is %+v, want the news of the event %q", held, event.ID)
	}
	if kept := stateOfTheTask(t, statePath); len(kept.PendingEvents) != 0 {
		t.Errorf("the backlog of the task holds %v after %d materializers, want nothing that was told",
			idsOf(kept), writers)
	}
	written, told := 0, 0
	for done := range results {
		written += len(done.Written)
		told += len(done.Told)
	}
	if written != 1 {
		t.Errorf("%d of the calls reported a record written, want exactly one: the line of the event is written once",
			written)
	}
	if told > 1 {
		t.Errorf("%d of the calls reported the event confirmed, want at most one: the backlog holds each event once",
			told)
	}
}

// TestTheSameNewsAlreadyInTheOutboxIsConfirmedAndNothingIsAppended: the crash this step is written
// against is a materializer that was killed between the append and the confirmation. Its record is
// in the file and its event is still in the backlog of the task, and the next materializer finds
// the news already there under its own name: it appends nothing — the same news told to the file
// twice is one line of it — and the name goes into the list of what is confirmed, so that the
// backlog of the task does not keep the event for ever (R2, docs/DESIGN.md §7h).
func TestTheSameNewsAlreadyInTheOutboxIsConfirmedAndNothingIsAppended(t *testing.T) {
	statePath, outboxPath := pathsOfATask(t)
	at := monday.Add(9 * time.Hour)
	keeps(t, statePath, State{Number: 43, Title: "the run of a task"})
	transition := went(AttentionStands, AttentionAwaitsReview, at, headOfTheFirst)
	event := recorded(t, statePath, transition)
	// What a materializer that was killed between the append and the confirmation leaves
	// behind: the record of the outbox is in the file and the event is still in the backlog of
	// the task, under the name it was told in and with the whole of the news beside it.
	if _, err := materializeInto(statePath, outboxPath); err != nil {
		t.Fatalf("the part of the road that appends and does not confirm: %v", err)
	}
	if kept := stateOfTheTask(t, statePath); len(kept.PendingEvents) != 1 {
		t.Fatalf("the backlog of the task holds %v after the append without the confirmation, want the "+
			"event still in it", idsOf(kept))
	}
	before := read(t, outboxPath)

	done, err := MaterializePendingEvents(nil, statePath, outboxPath)

	if err != nil {
		t.Fatalf("MaterializePendingEvents of the news already in the outbox returned an error: %v", err)
	}
	if len(done.Written) != 0 {
		t.Errorf("the call appended %v to the outbox, want nothing: the news is in the file already", done.Written)
	}
	if len(done.Duplicated) != 1 || done.Duplicated[0] != event.ID {
		t.Errorf("the call reported %v as the news the outbox held already, want the name of the event: %q",
			done.Duplicated, event.ID)
	}
	if len(done.Told) != 1 || done.Told[0] != event.ID {
		t.Errorf("the call confirmed %v, want the name of the news the outbox held already: %q",
			done.Told, event.ID)
	}
	if kept := read(t, outboxPath); kept != before {
		t.Errorf("the outbox after the second call is\n%s\nwant the one line that was there\n%s", kept, before)
	}
	if kept := stateOfTheTask(t, statePath); len(kept.PendingEvents) != 0 {
		t.Errorf("the backlog of the task holds %v, want the event that was in the outbox already to be gone",
			idsOf(kept))
	}
}

// TestAnotherNewsUnderTheNameOfAnEventIsAConflictAndNothingIsWritten: the name of an event is a
// digest kept to twelve signs and nothing proves that two different transitions of a task do not
// have one. A record that is there under the name of a transition of the backlog is not that
// transition, so it is not written over and its news is not confirmed: what is on the disk stays
// byte for byte, the transition stays in the backlog where a person is asked about it, and the
// refusal is a name a caller can tell apart (R5, docs/DESIGN.md §7h).
func TestAnotherNewsUnderTheNameOfAnEventIsAConflictAndNothingIsWritten(t *testing.T) {
	statePath, outboxPath := pathsOfATask(t)
	at := monday.Add(9 * time.Hour)
	keeps(t, statePath, State{Number: 43, Title: "the run of a task"})
	event := recorded(t, statePath, went(AttentionStands, AttentionAwaitsReview, at, headOfTheFirst))
	// The same name under another news: the same two states and the same moment of the task, on
	// another record of the host.
	line, err := lineOfOutboxRecord(OutboxRecord{
		Seq:     1,
		EventID: event.ID,
		From:    event.From,
		To:      event.To,
		At:      event.At,
		Basis:   headOfTheSecond,
	})
	if err != nil {
		t.Fatalf("the record of another news under the same name: %v", err)
	}
	if err := writeFile(outboxPath, line); err != nil {
		t.Fatalf("write the outbox of the task: %v", err)
	}
	before := read(t, outboxPath)

	done, err := MaterializePendingEvents(nil, statePath, outboxPath)

	if !errors.Is(err, ErrOutboxConflict) {
		t.Fatalf("the materialization of a transition another news is written under = %v, want %v", err, ErrOutboxConflict)
	}
	if !strings.Contains(err.Error(), event.ID) {
		t.Errorf("the refusal %q does not name the event it is about, want %q in it", err, event.ID)
	}
	if len(done.Written) != 0 || len(done.Told) != 0 {
		t.Errorf("the call wrote %v and confirmed %v, want nothing: a record that is there is not written over",
			done.Written, done.Told)
	}
	if len(done.Conflicted) != 1 || done.Conflicted[0] != event.ID {
		t.Errorf("the call reported %v in conflict, want the name of the event of the backlog: %q",
			done.Conflicted, event.ID)
	}
	if kept := read(t, outboxPath); kept != before {
		t.Errorf("the outbox after the refusal is\n%s\nwant the record that was there\n%s", kept, before)
	}
	if kept := stateOfTheTask(t, statePath); len(kept.PendingEvents) != 1 {
		t.Errorf("the backlog of the task holds %v after the refusal, want the transition that was not told",
			idsOf(kept))
	}
}

// TestAnOutboxWithAnUncommittedTailIsRefusedAndItsTailIsKept: the file is one line per record and
// the line break of a line is what says the record is in the file — nothing said those bytes
// reached the disk, and `Sync` is not called to say it. So a tail that reads as a whole record and
// is not one is refused whole: nothing is appended after it, nothing is taken out of the backlog
// of the task, and the tail stays byte for byte, because what is to be done with a file that was
// cut in the middle is a decision of a person and not of this step (R3, R6, docs/DESIGN.md §7h).
func TestAnOutboxWithAnUncommittedTailIsRefusedAndItsTailIsKept(t *testing.T) {
	whole := `{"seq":1,"event_id":"0123456789ab","from":"stands","to":"awaits-review",` +
		`"at":"2026-09-28T19:00:00Z","basis":"` + headOfTheFirst + `"}`
	for _, tail := range []struct {
		name string
		what string
	}{
		{name: "a whole record without the line break that commits it", what: whole},
		{name: "a record cut in the middle after a whole one", what: whole + "\n" + `{"seq":2,"event_id":"abc"`},
		{name: "a record cut in the middle", what: `{"seq":1,"event_id":"0123456789ab"`},
	} {
		t.Run(tail.name, func(t *testing.T) {
			statePath, outboxPath := pathsOfATask(t)
			keeps(t, statePath, State{Number: 43, Title: "the run of a task"})
			recorded(t, statePath, went(AttentionStands, AttentionAwaitsReview,
				monday.Add(9*time.Hour), headOfTheFirst))
			if err := writeFile(outboxPath, []byte(tail.what)); err != nil {
				t.Fatalf("write the outbox of the task: %v", err)
			}

			_, err := MaterializePendingEvents(nil, statePath, outboxPath)

			if !errors.Is(err, ErrOutboxCorrupt) {
				t.Fatalf("the materialization into an outbox with %s = %v, want %v",
					tail.name, err, ErrOutboxCorrupt)
			}
			if kept := read(t, outboxPath); kept != tail.what {
				t.Errorf("the outbox after the refusal is\n%q\nwant the tail byte for byte as it was\n%q",
					kept, tail.what)
			}
			if kept := stateOfTheTask(t, statePath); len(kept.PendingEvents) != 1 {
				t.Errorf("the backlog of the task holds %v after the refusal, want the transition that was not told",
					idsOf(kept))
			}
		})
	}
}

// TestALineOfTheOutboxThatIsNotOfTheClosedSchemaIsRefused: the file is held to the closed schema of
// the format. A field the schema does not name is a field nobody looked at, a value of a kind its
// own row does not name is a document that stopped being the document the format describes, and a
// value that is not of the form its field holds is a record nobody may go and read — the commit a
// transition rests on and the name it is kept under are what a program reads. Every one of them is
// refused with a short code that names neither the line nor the path of the file, and the file is
// left byte for byte as it was (docs/DESIGN.md §7e, §7h, #247).
func TestALineOfTheOutboxThatIsNotOfTheClosedSchemaIsRefused(t *testing.T) {
	for _, what := range []struct {
		name    string
		changes map[string]any
		taken   []string
		raw     string
	}{
		{name: "a field of tomorrow", changes: map[string]any{"payload": "the words of a run"}},
		{name: "a field nobody filled in", taken: []string{"basis"}},
		{name: "a field with nothing in it", changes: map[string]any{"basis": nil}},
		{name: "a state out of the list of §6a", changes: map[string]any{"from": "in-progress"}},
		{name: "a commit that is not of its form", changes: map[string]any{"basis": strings.Repeat("z", 40)}},
		{name: "a name that is not a digest", changes: map[string]any{"event_id": "the first event"}},
		{name: "a moment that is not of its form", changes: map[string]any{"at": "yesterday"}},
		{name: "a place that is a word", changes: map[string]any{"seq": "one"}},
		{name: "a place out of the file", changes: map[string]any{"seq": 0}},
		{name: "a line that is not a record", raw: `{"seq":1,"basis":`},
		{name: "a record that is not one event", raw: `[{"seq":1}]`},
	} {
		t.Run(what.name, func(t *testing.T) {
			statePath, outboxPath := pathsOfATask(t)
			keeps(t, statePath, State{Number: 43, Title: "the run of a task"})
			recorded(t, statePath, went(AttentionStands, AttentionAwaitsReview,
				monday.Add(9*time.Hour), headOfTheFirst))
			line := []byte(what.raw + "\n")
			if what.raw == "" {
				line = lineWithFields(t, what.changes, what.taken...)
			}
			if err := writeFile(outboxPath, line); err != nil {
				t.Fatalf("write the outbox of the task: %v", err)
			}

			_, err := MaterializePendingEvents(nil, statePath, outboxPath)

			if !errors.Is(err, ErrOutboxCorrupt) {
				t.Fatalf("the materialization into an outbox with %s = %v, want %v",
					what.name, err, ErrOutboxCorrupt)
			}
			for _, unwanted := range []string{headOfTheFirst, outboxPath} {
				if strings.Contains(err.Error(), unwanted) {
					t.Errorf("the refusal %q holds %q, want the line and the path out of it", err, unwanted)
				}
			}
			if kept := read(t, outboxPath); kept != string(line) {
				t.Errorf("the outbox after the refusal is\n%q\nwant the line that was there\n%q", kept, line)
			}
			if kept := stateOfTheTask(t, statePath); len(kept.PendingEvents) != 1 {
				t.Errorf("the backlog of the task holds %v after the refusal, want the transition that was not told",
					idsOf(kept))
			}
		})
	}
}

// TestARecordThatTheOutboxDoesNotHoldIsNotWritten: a record of the outbox is bounded in its size
// and held to the schema of the format before a byte of it is written, and a record that does not
// fit either is refused whole: the file is read through the same schema, and a line this call
// cannot read back is a line nobody may read afterwards — the outbox is appended to and never
// rewritten. The bound of the state of a task is the tighter of the two, and a transition whose
// record is not a commit of the host never gets here at all (docs/DESIGN.md §7e, §7h, #247).
func TestARecordThatTheOutboxDoesNotHoldIsNotWritten(t *testing.T) {
	record := OutboxRecord{
		Seq:     1,
		EventID: "0123456789ab",
		From:    AttentionStands,
		To:      AttentionAwaitsReview,
		At:      monday,
		Basis:   headOfTheFirst,
	}
	t.Run("the record of the format is one line with the line break that commits it", func(t *testing.T) {
		line, err := lineOfOutboxRecord(record)
		if err != nil {
			t.Fatalf("the record of the outbox as a line: %v", err)
		}
		written, err := json.Marshal(record)
		if err != nil {
			t.Fatalf("the record of the outbox as a document: %v", err)
		}
		if string(line) != string(written)+"\n" {
			t.Errorf("the line of the outbox is %q, want the record as the format writes it and the line break "+
				"that commits it: %q", line, written)
		}
		if len(line) > OutboxRecordLimit {
			t.Errorf("the line of the outbox is %d bytes, want at most the bound of %d", len(line), OutboxRecordLimit)
		}
	})
	for _, what := range []struct {
		name   string
		change func(OutboxRecord) OutboxRecord
		code   string
	}{
		{name: "a record that does not fit one line of the outbox",
			change: func(r OutboxRecord) OutboxRecord { r.Basis = strings.Repeat("a", OutboxRecordLimit); return r },
			code:   "bytes"},
		{name: "a record whose commit is not of the form of one",
			change: func(r OutboxRecord) OutboxRecord { r.Basis = strings.Repeat("z", 40); return r },
			code:   "outbox-value-not-of-its-form"},
		{name: "a record whose name is not of the form of one",
			change: func(r OutboxRecord) OutboxRecord { r.EventID = "the first event"; return r },
			code:   "outbox-value-not-of-its-form"},
		{name: "a record whose state is out of the list of §6a",
			change: func(r OutboxRecord) OutboxRecord { r.From = AttentionState("in-progress"); return r },
			code:   "outbox-state-word-unknown"},
		{name: "a record whose place is out of the file",
			change: func(r OutboxRecord) OutboxRecord { r.Seq = 0; return r },
			code:   "outbox-place-not-of-the-file"},
	} {
		t.Run(what.name, func(t *testing.T) {
			_, err := lineOfOutboxRecord(what.change(record))

			if !errors.Is(err, ErrOutboxRecordRefused) {
				t.Fatalf("the line of %s = %v, want %v", what.name, err, ErrOutboxRecordRefused)
			}
			if !strings.Contains(err.Error(), what.code) {
				t.Errorf("the refusal %q does not hold %q, want the code of what is wrong with the record",
					err, what.code)
			}
			if strings.Contains(err.Error(), headOfTheFirst) {
				t.Errorf("the refusal %q holds the record the transition rests on, want no payload in it", err)
			}
		})
	}
	t.Run("a transition whose record is not a commit is refused by the state of the task", func(t *testing.T) {
		statePath, outboxPath := pathsOfATask(t)
		keeps(t, statePath, State{Number: 43, Title: "the run of a task"})

		_, err := RecordPendingEvent(nil, statePath,
			went(AttentionStands, AttentionAwaitsReview, monday.Add(9*time.Hour), strings.Repeat("z", 40)),
			runWentOn(1))

		if !errors.Is(err, secret.ErrProtectedFieldInvalid) {
			t.Fatalf("the transition on a record that is not a commit of the host = %v, want the refusal of the "+
				"policy of the state: %v", err, secret.ErrProtectedFieldInvalid)
		}
		if _, err := os.Stat(outboxPath); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the outbox of the task is there: %v, want the bound of the state to have been the one that "+
				"refused it", err)
		}
	})
}

// TestTheConfirmationOfWhatWasToldIsOneListAndOnlyWhatIsLeftOfItIsTried: the confirmation of what
// was materialized is one call of the road of #243 with one list in it, and it is made against the
// backlog as it is there now — a name that is in the backlog and is not in the list is a refusal.
// So a refusal of that kind is not the end of the road: the backlog is read again under the lock
// of the task, the names of the list that are still in it are worked out of it, and only those are
// tried once more (docs/DESIGN.md §7h, #243, #247).
func TestTheConfirmationOfWhatWasToldIsOneListAndOnlyWhatIsLeftOfItIsTried(t *testing.T) {
	statePath, _ := pathsOfATask(t)
	keeps(t, statePath, State{Number: 43, Title: "the run of a task"})
	event := recorded(t, statePath, went(AttentionStands, AttentionAwaitsReview,
		monday.Add(9*time.Hour), headOfTheFirst))
	// A name of the list that the backlog of the task does not hold: the confirmation is made
	// against the backlog as it is there now, and it is a refusal of the whole list — which is
	// what makes the one list visible, because a road that confirmed the names one by one would
	// have taken the event out of the backlog before it ever met the name that is not there.
	names := []string{"0f0f0f0f0f0f", event.ID}

	told, err := confirmMaterialized(nil, statePath, names, ConfirmationRounds)

	if err != nil {
		t.Fatalf("the confirmation of what was materialized returned an error: %v", err)
	}
	if len(told) != 1 || told[0] != event.ID {
		t.Errorf("the confirmation took %v out of the backlog, want the name that was in it: %q", told, event.ID)
	}
	if kept := stateOfTheTask(t, statePath); len(kept.PendingEvents) != 0 {
		t.Errorf("the backlog of the task holds %v, want the event that was told gone out of it", idsOf(kept))
	}
}

// TestAConfirmationThatDoesNotComeToAnEndIsRefusedAsADivergence: the backlog of the task is the
// only record of what has not been told yet, and an event written into the outbox and left in the
// backlog is told twice or never. So a materialization that is still holding on to something after
// the bound of the retries is refused by name — the names that are left, and not the content of
// anything — and what it wrote is not thrown away, because the file is appended to and never
// written over (docs/DESIGN.md §7h, #247).
func TestAConfirmationThatDoesNotComeToAnEndIsRefusedAsADivergence(t *testing.T) {
	statePath, outboxPath := pathsOfATask(t)
	keeps(t, statePath, State{Number: 43, Title: "the run of a task"})
	event := recorded(t, statePath, went(AttentionStands, AttentionAwaitsReview,
		monday.Add(9*time.Hour), headOfTheFirst))
	// The event written into the outbox and not confirmed, as a materializer that was killed
	// between the two leaves it, and a name in the list that the backlog does not hold: the one
	// list is refused as a whole, and the only round that is left after the re-reading of the
	// backlog is spent on nothing, so the materialization is refused.
	if _, err := materializeInto(statePath, outboxPath); err != nil {
		t.Fatalf("the part of the road that appends and does not confirm: %v", err)
	}

	_, err := confirmMaterialized(nil, statePath, []string{"0f0f0f0f0f0f", event.ID}, 1)

	if !errors.Is(err, ErrPendingConfirmDivergence) {
		t.Fatalf("the confirmation that did not come to an end = %v, want %v", err, ErrPendingConfirmDivergence)
	}
	if !strings.Contains(err.Error(), event.ID) {
		t.Errorf("the refusal %q does not name the event that is left in the backlog, want %q in it",
			err, event.ID)
	}
	// One list, one call: the event is still in the backlog, because the whole list was refused
	// and nothing of it was taken out under a name nobody checked.
	if kept := stateOfTheTask(t, statePath); len(kept.PendingEvents) != 1 {
		t.Errorf("the backlog of the task holds %v after the refusal, want the event still in it: "+
			"the confirmation was made with one list", idsOf(kept))
	}
	if lines := linesOfOutbox(t, outboxPath); len(lines) != 1 {
		t.Errorf("the outbox holds %q, want the line that was written before the refusal: nothing is taken out "+
			"of a file that is only appended to", lines)
	}
}

// TestTheMaterializerWritesTheRecordOfTheTaskThroughTheRoadOfTheConfirmation: the state of a task
// is written by the road of §7h under its own lock, and the materializer of the events of the task
// writes no field of it with its own hands. Everything but the backlog comes out of the call the
// same as it went in, and the revision stands where it stood: a confirmation of what was told is
// not a change of the task (docs/DESIGN.md §7h, #243, #247).
func TestTheMaterializerWritesTheRecordOfTheTaskThroughTheRoadOfTheConfirmation(t *testing.T) {
	statePath, outboxPath := pathsOfATask(t)
	keeps(t, statePath, State{Number: 43, Title: "the run of a task"})
	recorded(t, statePath, went(AttentionStands, AttentionAwaitsReview,
		monday.Add(9*time.Hour), headOfTheFirst))
	before := stateOfTheTask(t, statePath)

	if _, err := MaterializePendingEvents(nil, statePath, outboxPath); err != nil {
		t.Fatalf("MaterializePendingEvents returned an error: %v", err)
	}

	after := stateOfTheTask(t, statePath)
	if len(after.PendingEvents) != 0 {
		t.Errorf("the backlog of the task holds %v, want nothing that was told", idsOf(after))
	}
	before.PendingEvents, after.PendingEvents = nil, nil
	if !reflect.DeepEqual(before, after) {
		t.Errorf("the record of the task after the materialization is\n%+v\nwant everything but the backlog "+
			"byte for byte as it was\n%+v", after, before)
	}
}

// TestTheOutboxIsNotMadeWithoutTheRecordOfTheTaskItIsAbout: the backlog of a task that was never
// run is nothing to tell anybody about, and the road that makes a state out of a file that is not
// there is the road of a state and not this one. So a record of the task that is not there is a
// refusal, the file of the outbox is not made on the way to it, and a task with nothing to tell
// anybody about gets no file either — an outbox made for nothing is a folder of empty files a
// person has to look at (docs.DESIGN.md §7h, #247).
func TestTheOutboxIsNotMadeWithoutTheRecordOfTheTaskItIsAbout(t *testing.T) {
	t.Run("a task that was never run", func(t *testing.T) {
		statePath, outboxPath := pathsOfATask(t)

		_, err := MaterializePendingEvents(nil, statePath, outboxPath)

		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("the materialization of a task whose record is not there = %v, want %v", err, os.ErrNotExist)
		}
		if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the record of the task is there after the refusal: %v, want none made", err)
		}
		if _, err := os.Stat(outboxPath); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the outbox of the task is there after the refusal: %v, want none made", err)
		}
	})
	t.Run("a task with nothing to tell about", func(t *testing.T) {
		statePath, outboxPath := pathsOfATask(t)
		keeps(t, statePath, State{Number: 43, Title: "the run of a task"})
		written := read(t, statePath)

		done, err := MaterializePendingEvents(nil, statePath, outboxPath)

		if err != nil {
			t.Fatalf("MaterializePendingEvents of an empty backlog returned an error: %v", err)
		}
		if len(done.Written)+len(done.Told)+len(done.Duplicated)+len(done.Conflicted) != 0 {
			t.Errorf("the call reported %+v, want nothing: the backlog of the task holds nothing", done)
		}
		if kept := read(t, statePath); kept != written {
			t.Errorf("the record of the task is\n%s\nwant it\n%s", kept, written)
		}
		if _, err := os.Stat(outboxPath); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the outbox of the task is there: %v, want no file made for nothing", err)
		}
	})
}

// TestTheSchemaOfTheOutboxIsTheSchemaOfTheBacklogAndTheFormat: five of the six fields of a record
// of the outbox are the fields of a record of the backlog of a task, under the same names the
// policy of the document of that state gives them, and the sixth is the place of the record in the
// file. The outbox spells none of the forms out: the names and the kinds are asked of the policy
// of the document, because two lists of them are two statements about the format and a copy of the
// list that went from the code is a schema nobody maintains (D-044, docs/DESIGN.md §7e, §7h).
func TestTheSchemaOfTheOutboxIsTheSchemaOfTheBacklogAndTheFormat(t *testing.T) {
	record, err := json.Marshal(PendingEvent{
		ID: "0123456789ab", At: monday, From: AttentionStands, To: AttentionAwaitsReview, Basis: headOfTheFirst,
	})
	if err != nil {
		t.Fatalf("the record of the backlog as a document: %v", err)
	}
	var held map[string]json.RawMessage
	if err := json.Unmarshal(record, &held); err != nil {
		t.Fatalf("the record of the backlog as a tree: %v", err)
	}
	ofTheBacklog := make([]string, 0, len(held))
	for name := range held {
		ofTheBacklog = append(ofTheBacklog, name)
	}
	slices.Sort(ofTheBacklog)
	// Every field of the outbox but the place of the record answers to a field of the backlog,
	// and the field it answers to is named by the path in the schema itself.
	answered := make([]string, 0, len(outboxSchema)-1)
	for name, path := range outboxSchema {
		if name == "seq" {
			continue
		}
		answered = append(answered, name)
		field, ofTheBacklogRow := strings.CutPrefix(path, "pending_events[].")
		if !ofTheBacklogRow {
			t.Errorf("the field %q of the outbox answers to %q, want a field of a record of the backlog", name, path)
			continue
		}
		answered[len(answered)-1] = field
	}
	slices.Sort(answered)
	if !slices.Equal(answered, ofTheBacklog) {
		t.Errorf("the record of the outbox answers to %v, want the fields of a record of the backlog %v and "+
			"the place of the record in the file", answered, ofTheBacklog)
	}

	for name, path := range outboxSchema {
		what, list, named := secret.ClassOf(secret.DocumentState, path)
		if !named {
			t.Errorf("the policy of the document of a task does not name %q for the field %q of the outbox",
				path, name)
			continue
		}
		switch name {
		case "seq":
			if what != "number" {
				t.Errorf("the place of a record of the outbox is %q, want a number", what)
			}
		case "from", "to":
			if what != "enum" || !sameWords(list, statesOfTheQueue()) {
				t.Errorf("the states of §6a of the field %q are %q, want the closed list of them %q",
					name, list, statesOfTheQueue())
			}
		default:
			form, ofThatForm := secret.FormOf(secret.DocumentState, path)
			if what != "identifier" || !ofThatForm || form == nil {
				t.Errorf("the field %q of the outbox is %q, want a name the policy holds a form of", name, what)
			}
		}
	}
}

// pathsOfATask is the record of a task and the outbox of that task, both in the folder of the test
// and nowhere else. Which folder of the machine holds the outbox of a task of a project is a
// decision this step does not make, and a test that took the folder of a live project would be a
// test writing into what a person keeps (docs/DESIGN.md §7h, #247).
func pathsOfATask(t *testing.T) (statePath, outboxPath string) {
	t.Helper()
	home := t.TempDir()
	return newJournals(home, "naghuale-crewflow").StatePath(43), filepath.Join(home, "outbox", "43.jsonl")
}

// linesOfOutbox is the lines the file of the outbox of a task holds, in the order it holds them,
// and the test fails where the file does not end in the line break that commits the last of them:
// a line per event, and nothing else in the file.
func linesOfOutbox(t *testing.T, path string) []string {
	t.Helper()
	data := []byte(read(t, path))
	if len(data) == 0 {
		return nil
	}
	if !bytes.HasSuffix(data, []byte("\n")) {
		t.Fatalf("the outbox of the task does not end in a line break: %q", data)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

// lineWithFields is one line of the file of the outbox of a task with the fields named changed or
// taken out of it. A line of the file is written by the marshaller of the record, and a test of the
// schema of the file writes it the same way and then says what is wrong with it.
func lineWithFields(t *testing.T, changes map[string]any, taken ...string) []byte {
	t.Helper()
	record, err := json.Marshal(OutboxRecord{
		Seq:     1,
		EventID: "0123456789ab",
		From:    AttentionStands,
		To:      AttentionAwaitsReview,
		At:      monday,
		Basis:   headOfTheFirst,
	})
	if err != nil {
		t.Fatalf("the record of the outbox as a line: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(record, &fields); err != nil {
		t.Fatalf("the record of the outbox as a tree: %v", err)
	}
	for name, value := range changes {
		fields[name] = value
	}
	for _, name := range taken {
		delete(fields, name)
	}
	line, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("the line of the outbox with %v and without %v: %v", changes, taken, err)
	}
	return append(line, '\n')
}
