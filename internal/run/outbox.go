package run

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/naghuale/crewflow/internal/secret"
)

// The outbox of a task: what was told about the transitions of the state of the task, in a file
// of its own, one line per event.
//
// The backlog of §7h lives in the record of the task, and the record is written whole — a state
// of a task is a file a person opens looking for the branch — so the news about a transition is
// written over on every change of the task and is gone the moment a confirmation takes it out of
// there. The outbox is the projection of that backlog which is not written over: a line is
// appended once and stays, and what a subscriber is to be told is read from here rather than
// from a record that is going to be written over again.
//
// It is a projection and not the source of the news: the backlog is the only record of what has
// not been told yet, and a line here is what was confirmed out of the backlog. Nothing is
// delivered out of it in this step, nothing is claimed, nobody is leased and nothing is
// acknowledged — the file is what a delivery will read, and writing it is what this step does
// (docs/DESIGN.md §7h, #247).
//
// Three properties hold it together, and each of them is refused rather than worked around:
//
//   - the file is appended to and never rewritten, and one line is one event;
//   - the lock of the file itself serializes reading it, checking it, taking the next place in it
//     and appending to it, so that two materializers of one task do not write two lines of one
//     event. The lock of the outbox is taken before the lock of the record of the task and never
//     the other way round, and it is given back before the confirmation — which takes the lock of
//     the task itself (R1);
//   - what was appended is read back whole and compared field by field, and that readback is the
//     only acknowledgement: a write that says it went through is not what the file holds. `Sync`
//     is not called and the loss of power is not claimed against (R6).

// OutboxRecord is one line of the outbox of a task: the news of one transition of the state of
// that task, under the name the backlog of the task holds it by, in the place it took in the file.
type OutboxRecord struct {
	// Seq is the place of the record in the outbox of the task, counted from one, and it is
	// the order the events were materialized in rather than the order they happened in: the
	// backlog keeps the moments the task went through, and an event of a moment that is older
	// than the record beside it is written after it, so a place may not be read as a time.
	Seq int64 `json:"seq"`
	// EventID is what the backlog of the task holds the transition under and what a
	// confirmation of it names. Nobody brings one: the name of an event is a digest of the
	// version of the record of the task the event was told in, of the transition and of the
	// whole record it rests on, kept to twelve signs because a state file is read by a person
	// and not by a hash (docs/DESIGN.md §7h, #243).
	EventID string `json:"event_id"`
	// From and To are the two states of §6a the task went from and to, and both of them are
	// words of the closed list of them: a word out of the list is a transition of nothing.
	From AttentionState `json:"from"`
	To   AttentionState `json:"to"`
	// At is when the task went from From to To.
	At time.Time `json:"at"`
	// Basis is the record the transition rests on: the commit at the head of the branch of the
	// task when it went. It is the whole of the payload of the line, and nothing else is in it:
	// a transition is told by the record it rests on and by no words of the run that went
	// through it (docs/DESIGN.md §7e, §7h).
	Basis string `json:"basis"`
}

// The bounds of the outbox. A record of a task is bounded because a person reads it whole, and a
// file of the outbox is not trimmed in this step at all: a file nobody trims grows by lines, and
// a line nobody bounded is a line nobody knows the size of (docs/DESIGN.md §7h, #247).
const (
	// OutboxRecordLimit is the highest size of one line of the outbox of a task, in bytes, the
	// line break of the line included. The fields of a record are bounded by the schema of the
	// format — a name of twelve signs, a commit of the host, two words of §6a and a moment —
	// and the bound of the state of a task is tighter than this one; the limit is what says it
	// out loud, because a file nobody trims grows by lines and a line nobody bounded is a line
	// nobody knows the size of (docs/DESIGN.md §7h, #247).
	OutboxRecordLimit = 512
	// ConfirmationRounds is how many times the confirmation of what was materialized is tried
	// before the materialization is refused as a divergence.
	//
	// The backlog is read and the confirmation is made under two locks, and another writer of
	// the task may take the events out of the backlog in between: the list to confirm is worked
	// out of the backlog again under the lock of the task, and only what is left of it is tried
	// once more. A materialization that keeps meeting a backlog somebody else emptied is not a
	// materialization that will ever come to an end, and it is refused rather than retried for
	// ever (docs/DESIGN.md §7h).
	ConfirmationRounds = 3
)

// The refusals of the outbox. Every one of them is a name a caller can tell apart, and none of
// them is worked around: an event refused here is an event nobody may act on as if it had been
// told.
var (
	// ErrOutboxLockAlias is the refusal of a pair of paths whose locks are one lock. The lock of
	// a file of crewflow is a file of its own next to it, and two names the machine resolves to
	// one file are one lock whatever they are called: the lock of the outbox is held while the
	// lock of the record of the task is taken, and a pair that is one lock is a road that waits
	// for itself for ever. So it is refused before either lock is taken and before a byte of
	// either file is read — nothing is created, nothing is appended and nothing is confirmed
	// (R1, F251-1, docs/DESIGN.md §7h).
	ErrOutboxLockAlias = errors.New("outbox-lock-alias")
	// ErrOutboxLockUnresolved is the refusal of a pair of names the machine cannot resolve, as
	// distinct from a pair that is one lock: the nearest folder above them that is there says what
	// they are, and where there is no such folder to say — a name that points at nothing, a folder
	// that points at itself, a folder this run may not go into — nothing says that the two names
	// are different, and a road that went on would make one of them and take its lock on a
	// comparison nobody made. It is named apart because a caller has a different thing to do about
	// it (F251-2, F251-3, docs/DESIGN.md §7h).
	ErrOutboxLockUnresolved = errors.New("outbox-lock-unresolved")
	// ErrOutboxCorrupt is the refusal of a file of an outbox that the format of it does not
	// admit: a line that is not a record of it, a field the schema does not name, a value of a
	// kind or of a form its own field does not hold, a tail of an append that was never
	// committed. Nothing is appended to such a file and nothing is taken out of it — not the
	// tail that is wrong, and not a byte of what was read before it. What is to be done with a
	// file cut in the middle is not decided here, and a person has to look at it
	// (docs/DESIGN.md §7h, #247).
	ErrOutboxCorrupt = errors.New("outbox-corrupt")
	// ErrOutboxRecordRefused is the refusal of a record this call would write and does not: one
	// that does not fit the bound of a record of an outbox, or one holding a value the format
	// does not admit. The bound of the state of a task is the tighter of the two — a
	// transition of the backlog rests on a commit of the host and is written through the policy
	// of the document of that state before it is ever here — and this road does not take the
	// bound of another road for its own: a line written that the reader of the file would
	// refuse is a line nobody may read afterwards, and the file is appended to and never
	// rewritten (docs/DESIGN.md §7h, #247).
	ErrOutboxRecordRefused = errors.New("outbox-record-refused")
	// ErrOutboxReadback is the refusal of a line that was appended and did not come back as it
	// was written: what the file holds at the place the record was written at is not that
	// record. Nothing is confirmed on a record nobody may read, and the line that is there is
	// left whole as it is (docs/DESIGN.md §7h, #247).
	ErrOutboxReadback = errors.New("outbox-readback-mismatch")
	// ErrOutboxConflict is the refusal of a transition whose name the outbox of the task holds
	// another news under. The name of an event is a digest of twelve signs and nothing proves
	// that two different transitions of a task do not have one: the record that is there is not
	// changed, the transition is not confirmed and stays in the backlog, so the two of them are
	// both still where a person can see them (R4, R5, docs.DESIGN.md §7h).
	ErrOutboxConflict = errors.New("outbox-conflict")
	// ErrPendingConfirmDivergence is the refusal of a materialization whose events are written
	// and are not out of the backlog of the task within the bound of the retries. The backlog is
	// the only record of what has not been told yet, and an event written into the outbox and
	// left in the backlog is told twice or never: the refusal names the events that are left
	// (docs/DESIGN.md §7h, #247).
	ErrPendingConfirmDivergence = errors.New("pending-confirm-divergence")
)

// Materialized is what one call of [MaterializePendingEvents] did to the outbox of a task and to
// the backlog of that task.
type Materialized struct {
	// Written are the records the call appended to the outbox of the task, in the order it
	// appended them: each of them is in the file, whole, and was read back field by field.
	Written []OutboxRecord
	// Duplicated are the names of the events the outbox held already under their own names and
	// with the same whole content. Nothing was appended for them and their names are in Told:
	// the same news told to the file twice is one line of it.
	Duplicated []string
	// Conflicted are the names of the events the outbox of the task holds another news under.
	// Nothing was written for them, their names are not in Told, and the records that are there
	// were not touched.
	Conflicted []string
	// Told are the names taken out of the backlog of the task by this call: what it wrote and
	// what the outbox held already. It is one list, and it is the one the confirmation below
	// was given.
	Told []string
}

// MaterializePendingEvents is the whole road of a materializer of the events of a task: what the
// backlog of the task holds is read under the lock of the record of the task, appended to the
// outbox of the task under the lock of that file, and taken out of the backlog by one call of
// [ConfirmPendingEvents] after the lock of the outbox is given back.
//
// The two paths are named by the caller and are not worked out here: which folder of the machine
// holds the outbox of a task of a project is a decision this step does not make, and a name of a
// file built from a project and a task number is a name made out of words nobody checked. A
// record of the task that is not there is not made here either — the backlog of a task that was
// never run is nothing to tell anybody about, and a materializer that made one would write a
// record of a task that does not exist (docs/DESIGN.md §7h, #247).
//
// What it refuses is named and is not worked around: two names whose locks are one lock, an outbox
// that does not say what the format of it says, a record that does not fit, a name another news is
// written under, a write that did not come back as it was written, and a backlog that did not let
// go of what was written. There is no producer here and no adapter of a live project: what a
// subscriber is told is a later step (docs/DESIGN.md §7h, #247).
func MaterializePendingEvents(out *secret.Out, statePath, outboxPath string) (Materialized, error) {
	// Before either lock and before a byte of either file: two names the machine resolves to one
	// file are one lock whatever they are called, and a road that takes one lock twice waits for
	// itself for ever (R1, F251-1).
	one, err := oneLockFor(statePath, outboxPath)
	if err != nil {
		return Materialized{}, err
	}
	if one {
		return Materialized{}, fmt.Errorf("the outbox of the task and the record of the task are one file under "+
			"two names, and the lock of a file is one file whatever the two of them are called, so taking it "+
			"for both of them is a road that waits for itself for ever: %w", ErrOutboxLockAlias)
	}
	done, err := materializeInto(statePath, outboxPath)
	if err != nil {
		return done, err
	}
	// The lock of the outbox was given back above and before this: the confirmation is made
	// under the lock of the record of the task, and the two locks are never held at once in the
	// other order (R1).
	told, err := confirmMaterialized(out, statePath, namesToTell(done), ConfirmationRounds)
	done.Told = told
	refused := error(nil)
	if len(done.Conflicted) > 0 {
		refused = fmt.Errorf("the outbox of the task holds another news under the name of %s of the backlog of "+
			"the task, and a record that is there is not written over: %w",
			strings.Join(done.Conflicted, ", "), ErrOutboxConflict)
	}
	return done, errors.Join(refused, err)
}

// oneLockFor is whether the lock of the record of a task and the lock of the outbox of that task
// are one lock. The lock of a file in this project is a file of its own next to it, so two names
// the machine resolves to one file are one lock whatever the two of them are called — and the two
// locks are taken one after another by this road, the second one while the first one is held, so a
// pair of them that is one lock is a road that waits for itself (R1, F251-1, docs/DESIGN.md §7h).
//
// A pair whose names cannot be resolved is not a pair of different names and is refused apart from
// one that is one lock (F251-2): two names nobody could compare are not a pair a lock may be taken
// for.
func oneLockFor(statePath, outboxPath string) (bool, error) {
	state, err := asTheMachineNames(statePath)
	if err != nil {
		return false, err
	}
	outbox, err := asTheMachineNames(outboxPath)
	if err != nil {
		return false, err
	}
	return state == outbox, nil
}

// asTheMachineNames is the name of a file as the machine resolves it: the nearest folder above it
// that is there is resolved, and the names below that folder are kept as they are written.
//
// The nearest folder that is there and not the folder of the file alone, because the file may well
// not be there yet: the outbox of a task is a file of a folder crewflow has not made, and a walk
// that reads a name that is not there as a name to be kept is the only walk that may go on.
//
// And it is `lstat`, not the resolution, that says whether a name is there. A symlink that points
// at a target nobody made is there and says nothing, and reading its refused resolution as "not
// there yet" keeps the name as it was written: the pair then looks like two different names and the
// road goes on. What it met there depended on which of the two names was the one that pointed at
// nothing — named through it, the outbox failed in `MkdirAll` before a lock was opened; named
// through it, the record of the task, with the outbox named by the target itself, that `MkdirAll`
// made the folder of the target and took the lock of the outbox there, and the lock of the record
// was that same lock. A name that is there and that the machine will not resolve, and a name this
// run may not look into, therefore stop the walk and are refused (F251-3).
//
// What is not resolved is a hard link of a file under two names of it, and a symlink of the file
// itself rather than of the folder above it: nothing in the name of a path says either, and opening
// the files to see whether they are one is not what this check is (F251-1).
func asTheMachineNames(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("the name of a file of crewflow made absolute: %w", err)
	}
	folder, below := filepath.Dir(absolute), []string{filepath.Base(absolute)}
	for {
		_, seen := os.Lstat(folder)
		switch {
		case errors.Is(seen, fs.ErrNotExist):
			// The name is not there at all: it is a part of the file that has not been made
			// yet, and it is kept as it was written.
			parent := filepath.Dir(folder)
			if parent == folder {
				return "", fmt.Errorf("the folder of a file of crewflow is not there and the machine has "+
					"no folder above it, so the name cannot be resolved: %w", ErrOutboxLockUnresolved)
			}
			below, folder = append(below, filepath.Base(folder)), parent
			continue
		case seen != nil:
			return "", fmt.Errorf("the nearest folder above a file of crewflow that is there is one this "+
				"run may not look into, and two names it cannot compare are not two names it can: %q: %w",
				filepath.Base(folder), ErrOutboxLockUnresolved)
		}
		resolved, err := filepath.EvalSymlinks(folder)
		if err != nil {
			return "", fmt.Errorf("the nearest folder above a file of crewflow is there and the machine "+
				"will not resolve it — a name that points at nothing, a name that points at itself — and "+
				"two such names cannot be told apart: %q: %w", filepath.Base(folder), ErrOutboxLockUnresolved)
		}
		slices.Reverse(below)
		return filepath.Join(append([]string{resolved}, below...)...), nil
	}
}

// materializeInto is everything a materializer does under the lock of the outbox: the backlog of
// the task is read there, the file is read and checked, the next place in it is taken and the
// record is appended and read back. The lock of the record of the task is taken inside it and
// given back before a byte of the outbox is touched, and it is taken inside and never outside:
// the two locks are taken in this order by every writer of a task, and a writer that took them
// the other way round would be a writer two of them wait for each other over (R1,
// docs/DESIGN.md §7h).
func materializeInto(statePath, outboxPath string) (Materialized, error) {
	// `lockState` is the road a file of its own is locked on in this package and the same one
	// the record of a task is locked on: a lock file next to the file it protects, never taken
	// away — a lock taken away is a second lock made of another file while the first one is
	// still held (docs/DESIGN.md §7h, §7i).
	release, err := lockState(outboxPath)
	if err != nil {
		return Materialized{}, err
	}
	defer release()
	backlog, err := pendingEventsOfTask(statePath)
	if err != nil {
		return Materialized{}, err
	}
	if len(backlog) == 0 {
		// Nothing is to be told about this task, and no file is made for it: an outbox that
		// was made for nothing is a folder of empty files a person has to look at.
		return Materialized{}, nil
	}
	records, size, err := readOutbox(outboxPath)
	if err != nil {
		return Materialized{}, err
	}
	done := Materialized{}
	for _, event := range backlog {
		held, found := recordHeldUnder(records, event.ID)
		switch {
		case found && sameNews(held, event):
			// The file holds this news already under its own name. This is what a
			// materializer that was killed between the append and the confirmation leaves
			// behind, and it is told and appended nothing: the record that is there is the
			// record of this transition, and the name goes into the list of what is
			// confirmed so that the backlog of the task lets it go (R2).
			done.Duplicated = append(done.Duplicated, event.ID)
		case found:
			// Another news under this name. It is refused and nothing is written: the
			// record that is there is not this transition and is not written over, and the
			// transition stays in the backlog where a person is asked about it (R5).
			done.Conflicted = append(done.Conflicted, event.ID)
		default:
			written, at, err := appendOutboxRecord(outboxPath, size, nextPlace(records), event)
			if err != nil {
				return done, err
			}
			records, size = append(records, written), at
			done.Written = append(done.Written, written)
		}
	}
	return done, nil
}

// pendingEventsOfTask is the backlog of the task as it is there now: read under the lock of the
// record of the task and copied out of it, so that what the confirmation names is worked out of
// the backlog and not out of what a caller held in its hands an age ago (docs/DESIGN.md §7h, #243).
//
// A record of the task that is not there is a refusal and not an empty backlog: a task that was
// never run has nothing to tell anybody about, and the road that wrote a state out of a missing
// file is the road of a state — not this one, which writes an outbox and confirms what is in a
// record that was there (docs.DESIGN.md §7h, #247).
func pendingEventsOfTask(statePath string) ([]PendingEvent, error) {
	release, err := lockState(statePath)
	if err != nil {
		return nil, err
	}
	defer release()
	state, err := LoadState(statePath)
	if err != nil {
		return nil, fmt.Errorf("the record of the task that keeps the backlog of the events of it: %w", err)
	}
	return slices.Clone(state.PendingEvents), nil
}

// readOutbox is what the file of the outbox of a task holds, in the order and in the size it
// holds it: the size is the place the next record begins at, and it is what the readback after
// the append starts from.
func readOutbox(path string) ([]OutboxRecord, int64, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		// A task whose events nobody materialized has no outbox yet, and an outbox that is
		// made here is made with its first record and not before it.
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("read the outbox of the task: %w", err)
	}
	records, err := recordsOutOf(data)
	if err != nil {
		return nil, 0, err
	}
	return records, int64(len(data)), nil
}

// recordsOutOf is what the records of an outbox are in the bytes of its file, in the order they
// stand in it, and a refusal where a line of it is not a record of the file.
//
// The file is one line per record and the line break of a line is what says the record is there:
// a process that was killed in the middle of an append leaves a tail that reads as a whole
// record and is not one — nothing said those bytes reached the disk, and `Sync` is not called
// here to say it either (R6). So a file that does not end in a line break is refused whole, and
// nothing is appended after such a tail: the tail stays byte for byte as it is, and whether it is
// to be dropped or cut is not decided in this step (docs.DESIGN.md §7h, #247).
func recordsOutOf(data []byte) ([]OutboxRecord, error) {
	if len(data) == 0 {
		return nil, nil
	}
	if !bytes.HasSuffix(data, []byte("\n")) {
		return nil, fmt.Errorf("the last line of the outbox of the task has no line break of its own, so what "+
			"it says was never committed and nothing is appended after it: %w", ErrOutboxCorrupt)
	}
	var records []OutboxRecord
	for at := 1; len(data) > 0; at++ {
		line, rest, _ := bytes.Cut(data, []byte("\n"))
		record, err := recordOfLine(line)
		if err != nil {
			return nil, corrupt(at, err)
		}
		records, data = append(records, record), rest
	}
	return records, nil
}

// appendOutboxRecord writes one record at the end of the file of the outbox of a task and answers
// the record as it is in the file together with the size the file is of after it.
//
// The write is one `O_APPEND` write of the whole line, and a write that went in part is a
// refusal and not a tail to be finished: what is left behind is what the next reader of the file
// finds, and it is not trimmed and not taken away here (docs/DESIGN.md §7h, #247).
func appendOutboxRecord(path string, at, place int64, event PendingEvent) (OutboxRecord, int64, error) {
	record := OutboxRecord{
		Seq:     place,
		EventID: event.ID,
		From:    event.From,
		To:      event.To,
		At:      event.At,
		Basis:   event.Basis,
	}
	line, err := lineOfOutboxRecord(record)
	if err != nil {
		return OutboxRecord{}, at, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return OutboxRecord{}, at, fmt.Errorf("open the outbox of the task to append to it: %w", err)
	}
	written, err := file.Write(line)
	if err == nil && written != len(line) {
		err = io.ErrShortWrite
	}
	if closed := file.Close(); err == nil {
		err = closed
	}
	if err != nil {
		return OutboxRecord{}, at, fmt.Errorf("append to the outbox of the task: %w", err)
	}
	if err := readBackRecord(path, at, record); err != nil {
		return OutboxRecord{}, at, err
	}
	return record, at + int64(written), nil
}

// lineOfOutboxRecord is one record of an outbox as a line of the file holds it, the line break of
// the line included, because the line break is what says the record is in the file.
//
// The bound of a record is checked before the schema of it and the schema before either of them
// writes a byte: the file is read through the same schema, and a line this call cannot read back
// is a line nobody may read afterwards — the file is appended to and never written over again
// (docs/DESIGN.md §7h, #247).
func lineOfOutboxRecord(record OutboxRecord) ([]byte, error) {
	line, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("the record of the outbox as a line: %w", err)
	}
	if len(line)+1 > OutboxRecordLimit {
		return nil, fmt.Errorf("the record of the outbox is %d bytes as it is written and one record of an outbox "+
			"is never this big: %w", len(line)+1, ErrOutboxRecordRefused)
	}
	if err := heldOfTheFormat(record); err != nil {
		return nil, fmt.Errorf("the record of the outbox is not what the format of it has (%s): %w",
			err, ErrOutboxRecordRefused)
	}
	return append(line, '\n'), nil
}

// readBackRecord is what the file of the outbox holds at the place the record was written at: the
// whole of the line and nothing else, read through the same door every other line of the file is
// read through, and compared field by field with the record that was written.
//
// It is the only acknowledgement of an append — a write that says it went through is not what the
// file holds — and it is a read of what is on the disk now and not of the words of this call: what
// is claimed is that a process which is not this one reads that record, and neither `Sync` nor
// the loss of power is (R6, docs.DESIGN.md §7h, #247).
func readBackRecord(path string, at int64, wrote OutboxRecord) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open the outbox of the task to read the record back: %w", err)
	}
	defer func() { _ = file.Close() }()
	size, err := file.Stat()
	if err != nil {
		return fmt.Errorf("the outbox of the task after the record was appended: %w", err)
	}
	if size.Size() < at {
		return fmt.Errorf("the outbox of the task is %d bytes after the record was written at the place %d and "+
			"it is shorter than it was before the write: %w", size.Size(), at, ErrOutboxReadback)
	}
	tail, err := io.ReadAll(io.NewSectionReader(file, at, size.Size()-at))
	if err != nil {
		return fmt.Errorf("read the record of the outbox of the task back: %w", err)
	}
	held, err := recordOfLine(tail)
	if err != nil {
		return fmt.Errorf("what the outbox of the task holds at the place the record was written at is not one "+
			"line of it (%s): %w", err, ErrOutboxReadback)
	}
	if !sameRecord(held, wrote) {
		return fmt.Errorf("what the outbox of the task holds at the place the record was written at is a record "+
			"other than the one written, in its place %d under the name %q and not that news: %w",
			held.Seq, held.EventID, ErrOutboxReadback)
	}
	return nil
}

// recordOfLine is the record of the outbox that one line of the file is, and a refusal where the
// line is not one.
//
// The file is held to the closed schema of the format: a field the schema does not name is a field
// nobody looked at, a value of a kind its own row does not name is a document that stopped being
// the document the format describes, and a value that is not of the form its field holds is a
// record nobody may go and read — the commit a transition rests on and the name it is kept under
// are read by a program (docs/DESIGN.md §7e, §7h, #247).
func recordOfLine(line []byte) (OutboxRecord, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(line, &fields); err != nil {
		return OutboxRecord{}, notARecord("outbox-line-unreadable")
	}
	for name := range outboxSchema {
		if _, held := fields[name]; !held {
			return OutboxRecord{}, fmt.Errorf("outbox-field-%s-absent", name)
		}
	}
	for name := range fields {
		if _, named := outboxSchema[name]; !named {
			return OutboxRecord{}, notARecord("outbox-field-of-tomorrow")
		}
	}
	// Every field of the schema is named above, and what is left is what the line says of each
	// of them: a value of another kind than the field holds is refused by the reading itself.
	var record OutboxRecord
	if err := json.Unmarshal(line, &record); err != nil {
		return OutboxRecord{}, notARecord("outbox-field-of-another-kind")
	}
	if err := heldOfTheFormat(record); err != nil {
		return OutboxRecord{}, err
	}
	return record, nil
}

// outboxSchema is the closed schema of one record of an outbox: the name of every field the format
// has, and the place of the document of a task that names it. Five of the six are the fields of a
// record of the backlog of a task under the same names — the same words, the same forms and the
// same closed list of the states of §6a — and one is the outbox's own, the place of the record
// in the file.
//
// The names and the forms are asked of the policy of that document and are not spelled here: two
// lists of them are two statements about the format, and a copy of the list that went from the
// code is a schema nobody maintains (D-044, docs/DESIGN.md §7e, §7h).
var outboxSchema = map[string]string{
	"seq":      "revision",
	"event_id": "pending_events[].id",
	"from":     "pending_events[].from",
	"to":       "pending_events[].to",
	"at":       "pending_events[].at",
	"basis":    "pending_events[].basis",
}

// heldOfTheFormat is whether every field of the record is what the policy of the document of a
// task says of the field it answers to: a number of the file, a word of the closed list of §6a, or
// a name of the form that field holds there. The place of a record is counted from one, because a
// record of an outbox of which the place is not known is a record that cannot be placed.
func heldOfTheFormat(record OutboxRecord) error {
	if record.Seq <= 0 {
		return notARecord("outbox-place-not-of-the-file")
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{"event_id", record.EventID},
		{"from", string(record.From)},
		{"to", string(record.To)},
		{"at", record.At.Format(time.RFC3339Nano)},
		{"basis", record.Basis},
	} {
		if err := heldByFormat(field.name, field.value); err != nil {
			return err
		}
	}
	return nil
}

// heldByFormat is whether the value of a field of a record of the outbox is of the kind the policy
// of the document of a task names for that field and of the form that kind holds there. A field
// the policy stopped naming is a refusal and not a record written out of what is left of it: the
// code and the statement about the format disagree, and which of them is right is not decided by
// a file of an outbox (docs/DESIGN.md §7e).
func heldByFormat(name, value string) error {
	what, words, named := secret.ClassOf(secret.DocumentState, outboxSchema[name])
	switch {
	case !named:
		return notARecord("outbox-field-out-of-the-schema")
	case what == "enum":
		if !slices.Contains(words, value) {
			return notARecord("outbox-state-word-unknown")
		}
	case what == "identifier":
		form, ofThatForm := secret.FormOf(secret.DocumentState, outboxSchema[name])
		if value == "" || !ofThatForm || !form(value) {
			return notARecord("outbox-value-not-of-its-form")
		}
	default:
		return notARecord("outbox-field-of-another-class")
	}
	return nil
}

// notARecord is why one line of the file of an outbox is not a record of it. It is a short code
// and not a sentence about the line: a refusal is a text a person reads and a terminal pastes,
// and a line holds the payload of an event — the record the transition rests on — and nothing
// else that is worth repeating (docs.DESIGN.md §7e, §7h).
func notARecord(code string) error { return errors.New(code) }

// corrupt is the refusal of a line of the file of an outbox that is not a record of it. It names
// the line and what about it is wrong, and neither the line nor the path of the file: what is to
// be done with a file that says something else is a decision of a person and not of this step
// (docs/DESIGN.md §7h, #247).
func corrupt(at int, why error) error {
	return fmt.Errorf("line %d of the outbox of the task is not a record of it (%s), and nothing is appended to "+
		"an outbox that says something else and nothing is taken out of it: %w", at, why, ErrOutboxCorrupt)
}

// nextPlace is the place the next record of the outbox of a task takes: one more than the highest
// place the file holds, and one when it holds nothing. It is worked out of the places in the file
// and not of the number of its lines, so that a file with a hole in it is not given a place that
// is already in it.
func nextPlace(records []OutboxRecord) int64 {
	place := int64(1)
	for _, record := range records {
		if record.Seq >= place {
			place = record.Seq + 1
		}
	}
	return place
}

// recordHeldUnder is the record the outbox of a task holds under a name of an event, and whether
// it holds one. The file may hold the same name twice — nothing in it forbids two lines of one
// name — and the reader of the file reaches the first of them first.
func recordHeldUnder(records []OutboxRecord, id string) (OutboxRecord, bool) {
	at := slices.IndexFunc(records, func(record OutboxRecord) bool { return record.EventID == id })
	if at < 0 {
		return OutboxRecord{}, false
	}
	return records[at], true
}

// sameNews is whether the record in the outbox of a task is the news of the transition the backlog
// of the task holds: the two states it went from and to, the moment of it, and the whole of the
// record it rests on.
//
// The name of the event is in neither comparison and is not in it on purpose: a name is a digest
// of twelve signs and nothing proves that two different transitions of a task do not have one, so
// what tells one record of the outbox from another is the whole of the news and not the word it
// is filed under (R4, docs.DESIGN.md §7h).
func sameNews(record OutboxRecord, event PendingEvent) bool {
	return record.From == event.From && record.To == event.To &&
		record.At.Equal(event.At) && record.Basis == event.Basis
}

// sameRecord is whether the record the file holds is the record that was written, in every field
// of it: the place, the name, the two states, the moment and the whole of the record the
// transition rests on. A record that came back under the right name and is another record beside
// it is not the record that was written, and what a subscriber reads is the record and not the
// name (docs.DESIGN.md §7h, #247).
func sameRecord(held, wrote OutboxRecord) bool {
	return held.Seq == wrote.Seq && held.EventID == wrote.EventID &&
		held.From == wrote.From && held.To == wrote.To &&
		held.At.Equal(wrote.At) && held.Basis == wrote.Basis
}

// namesToTell is the one list of names [ConfirmPendingEvents] is given: what was written into the
// outbox of the task and what the outbox held already. A name that is in the outbox and is not
// in this list is not confirmed — the record of a conflict is not news anybody may act on, and
// the transition under that name stays in the backlog (docs/DESIGN.md §7h, #243, #247).
func namesToTell(done Materialized) []string {
	names := make([]string, 0, len(done.Written)+len(done.Duplicated))
	for _, record := range done.Written {
		names = append(names, record.EventID)
	}
	return append(names, done.Duplicated...)
}

// confirmMaterialized is the confirmation of what was materialized: one list and one call of the
// road of #243, and the names in it are the names the outbox holds — what this call wrote and
// what it held already.
//
// The confirmation is made against the backlog as it is there now, and a name that is in the
// backlog and is not in the list is a refusal (docs.DESIGN.md §7h, #243). So a refusal of that
// kind is not an end of the road: the backlog is read again under the lock of the task, the names
// of this list that are still in it are worked out of it, and only those are tried once more —
// what another writer took out of the backlog in between was confirmed by it and is not
// confirmed twice. A materialization that is still holding on to something after the bound is a
// divergence and is refused by name (docs.DESIGN.md §7h, #247).
func confirmMaterialized(out *secret.Out, statePath string, names []string, rounds int) ([]string, error) {
	remaining := slices.Clone(names)
	told := make([]string, 0, len(names))
	for range rounds {
		if len(remaining) == 0 {
			return told, nil
		}
		if _, err := ConfirmPendingEvents(out, statePath, remaining); err != nil {
			if !errors.Is(err, ErrPendingNotFound) {
				return told, err
			}
			kept, err := pendingEventsOfTask(statePath)
			if err != nil {
				return told, err
			}
			remaining = namesLeftIn(kept, remaining)
			continue
		}
		told = append(told, remaining...)
		remaining = nil
	}
	if len(remaining) > 0 {
		return told, fmt.Errorf("the events %s of the task are written into its outbox and are still in the "+
			"backlog of the record of the task, which nobody took out of it: %w",
			strings.Join(remaining, ", "), ErrPendingConfirmDivergence)
	}
	return told, nil
}

// namesLeftIn is the names of a list that the backlog of a task still holds, in the order of the
// list and not of the backlog: what is to be confirmed again is what was named and not what
// happens to stand next to it.
func namesLeftIn(backlog []PendingEvent, names []string) []string {
	left := make([]string, 0, len(names))
	for _, id := range names {
		if held := slices.ContainsFunc(backlog, func(event PendingEvent) bool {
			return event.ID == id
		}); held {
			left = append(left, id)
		}
	}
	return left
}
