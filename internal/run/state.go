package run

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/naghuale/crewflow/internal/proc"
)

// State is what crewflow keeps of a task between its runs, so that after an
// interruption it is visible where the run stopped: the branch, the worktree, the
// profile of the executor, the session to go on in, and every attempt in its own
// right (docs/DESIGN.md §7). The truth about the work is still on the host; this
// is what crewflow needs to not start from the beginning next time.
//
// What is never kept here is whether a change was approved or whether it may be
// merged: those are worked out from the host and from git every time they are asked
// for, so that a state file cannot say what the host says otherwise (docs/DESIGN.md §7h).
type State struct {
	// Schema is the version of the format of the file. A state written before
	// crewflow kept one is read as the version before the first, with every attempt
	// of it read as it is, and it is written again in the current format the next
	// time a run of the task touches it.
	Schema int `json:"schema,omitempty"`
	// Number is the task the state is of.
	Number int `json:"task"`
	// Title is the one line a person wrote, so that a state file says what it is
	// of without the host being read.
	Title string `json:"title"`
	// Branch and Worktree are where the work of the task is.
	Branch   string `json:"branch"`
	Worktree string `json:"worktree"`
	// Profile is what crewflow knew about the executor, which is what a later run
	// goes on with.
	Profile string `json:"profile"`
	// Session is the id of the session the executor went on, and the way a run of
	// the same task is continued in the same one.
	Session string `json:"session,omitempty"`
	// Change is the change request a run of the task opened, so that a list of the
	// runs of a project points at the work without asking the host anything
	// (docs/DESIGN.md §7).
	Change *Change `json:"change,omitempty"`
	// MergedSHA is the commit that was fast-forwarded into the default branch of the
	// project, and VerifiedAt when the merge was checked after it. Both are facts of
	// the process and not a verdict: whether the change may be merged is worked out
	// from the host and from git every time it is asked for, and what the state says
	// here is what has already happened (docs/DESIGN.md §7h).
	MergedSHA  string     `json:"merged_sha,omitempty"`
	VerifiedAt *time.Time `json:"verified_at,omitempty"`
	// Notice is what crewflow left under the task on the host about the queue of
	// attention, and when: the key of the entry it was about and the moment of the
	// record. It is a fact of what has been said and not a verdict about the task —
	// whether the task is in the queue at all is worked out at every read — and it is
	// what keeps one record to a key and a day rather than one a minute (§6a, §7h).
	Notice *Notice `json:"notice,omitempty"`
	// Checkpoint is where the run of the task stands when it needs a decision of a
	// person, and what came of the request. It is the last point a run of this task
	// wrote and not a queue of points: one run of a task stops once, and the run that
	// goes on from the point either answers the request or refuses it (docs.DESIGN.md
	// §7i).
	Checkpoint *Checkpoint `json:"checkpoint,omitempty"`
	// Settled is what the host of the project said about a run whose work is over: its
	// change went in or was closed, or the task itself was closed on the tracker.
	//
	// It is a fact of the host and not a verdict about the task (§6a): то, что crewflow
	// прочитал однажды, помнится, иначе расписание, которое ходит в хостинг каждую
	// минуту, спрашивало бы про один и тот же слитый прогон снова и снова — а список
	// прогонов, который не читает хостинг вовсе, показывал бы слитое как результат,
	// который никто не смотрел (F-061, F-105, §6, §6a).
	//
	// У памяти есть срок: свежая значит «спросим ещё раз». Изменение или задачу можно
	// открыть заново, и очередь, помнящая «завершено» навсегда, держала бы reopened
	// задачу вне её до следующего чтения хостинга — поэтому срок задан не памятью, а
	// проверкой (§6a).
	Settled *Settled `json:"settled,omitempty"`
	// Attempts are the starts of the executor, oldest first.
	Attempts []Attempt `json:"attempts"`
}

// Settled is what the host said about a run whose work is over, and when it said it: the
// change request of the run went in or was closed, or the task itself was closed. It is
// what a queue remembers so that it does not ask the host about the same finished run on
// every turn of a schedule, and what a list of runs reads to leave it out (docs/DESIGN.md
// §6, §6a).
type Settled struct {
	// By is what the host said, in the words of §6a: `change-merged`, `change-closed`
	// or `task-closed`. It is kept because a person reading a state file has to be able
	// to tell «изменение влито» от «задача закрыта»: это два разных события жизни
	// проекта, и очередь обязана знать, какое из них произошло (§6a, §7g).
	By string `json:"by"`
	// At is when the host said it, and what the shelf life of the memory is counted
	// from.
	At time.Time `json:"at"`
}

// Notice is the record crewflow left under a task about the queue of attention, and the
// moment of it: the key is what the record was about — the task, the state, the priority
// and the reason of the entry — and a record of another key is a new record, whatever the
// one before it said (docs/DESIGN.md §6a).
type Notice struct {
	// Key is what the record was about, as `Attention.Key` writes it.
	Key string `json:"key"`
	// At is when the record was left.
	At time.Time `json:"at"`
}

// Change is the change request a run of a task opened: its number and where it is,
// which is all a list of runs says about it.
type Change struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
}

// Schema is the version of the format of a state file, and a file without one is
// the version before it: crewflow kept the branch, the worktree and the outcomes of
// the attempts, and every attempt of it is read as it was written. A run that goes
// on writes the file in this version, and a file of a version crewflow does not know
// is refused rather than read as something it is not.
const Schema = 1

// Identity is whose name the executor of an attempt worked under: the mode it worked
// in and the one line a report shows (docs/DESIGN.md §7i).
//
// From the schema of §7h on it is written as the mode itself, "owner" or "bot": the
// words of a report of a run are in the journal and in the result of a run, and what
// a state file has to say is which of the two modes a try went under. A state written
// before that held a table with a mode and a description; it is read as it is, the
// table with the two words, and the mode in it is what a report shows.
type Identity struct {
	// Mode is "owner" or "bot", the two words the core knows.
	Mode string
	// Description is the one line a report of the run shows.
	Description string
}

// MarshalJSON is the mode alone: a state file of §7h holds `identity: "owner"`, and
// the line a report shows is in the report and not in the state of the task.
func (i Identity) MarshalJSON() ([]byte, error) {
	return json.Marshal(i.Mode)
}

// UnmarshalJSON reads both the mode of §7h and the table of the format before it, so
// that a state of before is read as what it is and a state of now is read as the
// mode it is.
func (i *Identity) UnmarshalJSON(data []byte) error {
	var mode string
	if err := json.Unmarshal(data, &mode); err == nil {
		i.Mode = mode
		return nil
	}
	type identity Identity
	var before identity
	if err := json.Unmarshal(data, &before); err != nil {
		return fmt.Errorf("the identity of an attempt is neither %q nor a table of before: %w", "owner", err)
	}
	*i = Identity(before)
	return nil
}

// Attempt is one start of the executor on a task: who ran it, in which session, whose
// name it went under, when it was, where it wrote, and how it ended. A task has as
// many attempts as it took tries, and the journal of each of them stays where it was
// written.
type Attempt struct {
	// Number is the attempt in the task, starting at one.
	Number int `json:"number"`
	// Executor is the agent of the attempt — "opencode", "codex" — and Session the
	// session of it, which is what a continuation goes on in. Both are of the
	// attempt and not of the task: a task whose executor was replaced starts again
	// in a session of its own, and a continuation goes on in the one before
	// (docs/DESIGN.md §7, §7h).
	Executor string `json:"executor,omitempty"`
	Session  string `json:"session,omitempty"`
	// StartedAt and EndedAt are when the executor was started and when it stopped,
	// which is what says how long a run took.
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	// LastAt and LastStep are the sign of life of the run, as the last step of crewflow
	// left it: when crewflow last did something of its own for this run, and what that
	// was. Together with the journal of the attempt — where the last line the executor
	// wrote stands — they are what says whether a run that is going is working or
	// standing, and a state of before crewflow kept either names neither, so that a run
	// of before is a run that has shown nothing since it began (docs/DESIGN.md §6, §7a).
	LastAt   time.Time `json:"last_at,omitempty"`
	LastStep string    `json:"last_step,omitempty"`
	// Reason is what the run stands at where crewflow knows it before it stands there:
	// a run in the mode of the bot goes to the keychain of the machine for the key of
	// the App of the host, and a run that is not answered in the window of the system
	// is a run that needs a person (docs/DESIGN.md §7i). It is read where a run has
	// been quiet for longer than the silence of the project, and it is a name and not a
	// sentence, because that is what a report of it shows.
	Reason string `json:"reason,omitempty"`
	// Provider is what the model provider of this attempt said about the failure it
	// stopped the run for: `retryable` where the provider itself marked the refusal
	// repeatable, and `unknown` where it said nothing that makes the failure temporary.
	// It is a fact of the run kept beside the reason of it, because the queue of
	// attention has to name the repeatability of the wait without reading a journal, and
	// it is empty for every attempt that did not stop for a provider (F-119,
	// docs.DESIGN.md §6a, §7h).
	Provider string `json:"provider,omitempty"`
	// ReportedAt is when a record of the standing of this attempt was last left under
	// the task on the host by `crewflow task check-stalled`. It is a fact of what has
	// been said and not a verdict about the run: whether the run is standing is worked
	// out at every read, and this is what keeps one record to an episode of silence
	// rather than one a minute (docs/DESIGN.md §6, §7h).
	ReportedAt *time.Time `json:"reported_at,omitempty"`
	// Journal and ErrorJournal are the files of what the executor wrote and of
	// what it said on the way out.
	Journal      string `json:"journal"`
	ErrorJournal string `json:"error_journal"`
	// Identity is whose name the executor of this attempt worked under, so that a
	// person reading the state of a task afterwards sees it for every attempt and
	// not only for the last one (docs/DESIGN.md §7i).
	Identity Identity `json:"identity"`
	// Outcome is how the attempt ended.
	Outcome Kind `json:"outcome"`
	// Continued says that the attempt went on in the session of an earlier one.
	Continued bool `json:"continued"`
	// Resumed says that the attempt went on from the point of the task: a run stood at
	// a decision of a person, the person did the necessary action, and this attempt is
	// the continuation of that run. It is not `Continued`, which is a run that goes on
	// in a session that exists; nothing of a run stopped in front of a window of the
	// system had started an executor (docs/DESIGN.md §7i).
	Resumed bool `json:"resumed,omitempty"`
	// AutoResumed is the habit an attempt went on by itself for, and is empty for a
	// first run and for a continuation the orchestrator asked for. It is what keeps a
	// task from being resumed by itself twice for the same habit: the second time
	// round the run stops and the orchestrator decides (docs/DESIGN.md §7a, §7j).
	AutoResumed string `json:"auto_resumed,omitempty"`
	// PID and ProcessStartedAt are the process of the run of crewflow itself: its
	// number and when that process started. It is crewflow and not the executor, and
	// that is on purpose — crewflow lives exactly as long as the run and stops the
	// executor when it is stopped, so a run whose crewflow is gone is a run that is
	// over whatever its executor is doing. The state of a task says "running" until
	// something says otherwise, and after a reboot of the machine nothing does but
	// the process itself (docs/DESIGN.md §7).
	PID              int        `json:"pid,omitempty"`
	ProcessStartedAt *time.Time `json:"process_started_at,omitempty"`
}

// Process is the process of the run of crewflow the attempt was made in, and whether
// the state names one at all: a state written before crewflow kept the number of a
// process names none, and the machine cannot be asked about a number it does not have.
func (a Attempt) Process() (proc.Process, bool) {
	if a.PID <= 0 || a.ProcessStartedAt == nil {
		return proc.Process{}, false
	}
	return proc.Process{Pid: a.PID, StartedAt: *a.ProcessStartedAt}, true
}

// Attempt returns the attempt with the number, and whether there is one.
func (s State) Attempt(number int) (Attempt, bool) {
	for _, attempt := range s.Attempts {
		if attempt.Number == number {
			return attempt, true
		}
	}
	return Attempt{}, false
}

// StartOf is one start of the executor: when it was, which agent ran it, in which
// session, whose name it went under, where it writes, and whether it goes on in the
// session of an attempt before. It is what a run of a task knows about its own start
// before the executor is started, and the state of the task is told before the
// executor is (docs/DESIGN.md §7).
type StartOf struct {
	// Started is when the executor was started: the attempt is going on from the
	// moment it is added, because that is what the state of a task says while the
	// executor works.
	Started time.Time
	// Journal and ErrorJournal are the files of what it writes and of what it says
	// on the way out.
	Journal      string
	ErrorJournal string
	// Executor is the agent of the attempt and Session the session of it, and a
	// continuation goes on in the session of the attempt before.
	Executor string
	Session  string
	// Continued says that the attempt goes on in the session of an earlier one.
	Continued bool
	// Resumed says that the attempt goes on from the point of the task, after a person
	// did the only thing that was his to do (docs/DESIGN.md §7i).
	Resumed bool
	// AutoResumed is the habit crewflow went on by itself for, when nobody asked it
	// to: an attempt the orchestrator continued is not one, and says nothing here.
	AutoResumed string
	// Step is what the run of the task was doing when the attempt began, and Reason
	// what it stands at where crewflow knows it before it stands there. They are the
	// sign of life the attempt starts with, and a run that is cut off between this and
	// the executor writing anything is a run a list of runs has to be able to call
	// standing (docs/DESIGN.md §6, §7a, §7i).
	Step   string
	Reason string
	// Process is the process the run of crewflow is happening in, which is what a
	// later list asks the machine about, and the identity is whose name the
	// executor of the run is about to work under, so that the state says it from the
	// moment the run starts and not only when it is over (docs/DESIGN.md §7, §7i).
	Process  proc.Process
	Identity Identity
}

// NextAttempt is the number the next start of the executor of this task gets, and
// the state with that attempt added and not written anywhere yet. The attempt is
// running from the moment it is added: it is what the state of a task says while the
// executor works, and what a run that is cut short leaves behind.
func (s State) NextAttempt(start StartOf) State {
	next := len(s.Attempts) + 1
	attempt := Attempt{
		Number:       next,
		StartedAt:    start.Started,
		LastAt:       start.Started,
		LastStep:     start.Step,
		Reason:       start.Reason,
		Journal:      start.Journal,
		ErrorJournal: start.ErrorJournal,
		Executor:     start.Executor,
		Session:      start.Session,
		Identity:     start.Identity,
		Outcome:      Running,
		Continued:    start.Continued,
		Resumed:      start.Resumed,
		AutoResumed:  start.AutoResumed,
	}
	if start.Process.Pid > 0 {
		started := start.Process.StartedAt
		attempt.PID, attempt.ProcessStartedAt = start.Process.Pid, &started
	}
	s.Attempts = append(s.Attempts, attempt)
	return s
}

// Ended records how the last attempt of the task ended, and when.
func (s State) Ended(ended time.Time, outcome Kind) State {
	if len(s.Attempts) == 0 {
		return s
	}
	last := len(s.Attempts) - 1
	s.Attempts[last].EndedAt = ended
	s.Attempts[last].Outcome = outcome
	return s
}

// Reason is the reason the attempt that has just ended is to be read with, and it is
// written into that attempt while it is still the one that ended: the state of a task is
// where the queue of attention and a list of runs read why a run stopped, and a run that
// repeated a habit crewflow had already answered says so in the state and not only in
// the journal it is over (docs/DESIGN.md §7a.1, §6a).
func (s State) Reason(reason string) State {
	if len(s.Attempts) == 0 {
		return s
	}
	s.Attempts[len(s.Attempts)-1].Reason = reason
	return s
}

// Provider is the state of a task with what the model provider of the attempt said about
// the failure it stopped it for written into that attempt: `retryable` where the provider
// itself says the refusal may be repeated and `unknown` where it said nothing that makes
// the failure temporary. It is written into the attempt that has just ended, beside its
// reason, and it is what the queue of attention names the repeatability of the wait by
// (F-119, docs/DESIGN.md §6a).
func (s State) Provider(marked string) State {
	if len(s.Attempts) == 0 {
		return s
	}
	s.Attempts[len(s.Attempts)-1].Provider = marked
	return s
}

// Alive is a sign of life of the attempt that is going: when crewflow last did
// something of its own for the run, what that was, and what the run stands at where
// crewflow knows it. A run whose executor writes is alive whatever crewflow writes into
// the state, and the sign of life of such a run is the last line of its journal
// (docs/DESIGN.md §6, §7a).
func (s State) Alive(at time.Time, step, reason string) State {
	if len(s.Attempts) == 0 {
		return s
	}
	last := len(s.Attempts) - 1
	s.Attempts[last].LastAt, s.Attempts[last].LastStep, s.Attempts[last].Reason = at, step, reason
	return s
}

// Reported is the moment crewflow said under the task that the attempt that is going
// stands, and it is the sign of one episode of silence: a run that is not reported any
// more is a run whose silence is over — it is working again, or it is over — and the
// next silence of it is a new one to write about (docs/DESIGN.md §6).
func (s State) Reported(at time.Time) State {
	if len(s.Attempts) == 0 {
		return s
	}
	reported := at
	s.Attempts[len(s.Attempts)-1].ReportedAt = &reported
	return s
}

// NoLongerReported is an attempt nobody is holding as standing any more: the record of
// its silence has been closed, and a run that stands again is a new episode of it.
func (s State) NoLongerReported() State {
	if len(s.Attempts) == 0 {
		return s
	}
	s.Attempts[len(s.Attempts)-1].ReportedAt = nil
	return s
}

// Noticed is the state of a task with the record of the queue of attention that was left
// under it, and the moment of it. It is the sign of one key of the queue, in the way
// `Reported` is the sign of one episode of the silence of a run: the same key is not
// written again within the day, and another key is a new record whatever the one before it
// said (docs/DESIGN.md §6a).
func (s State) Noticed(at time.Time, key string) State {
	s.Notice = &Notice{Key: key, At: at}
	return s
}

// NoticedWithin is whether a record of this key of the queue was left under the task
// within `after` of `now`, and a key that was never left is a record that is due
// (docs.DESIGN.md §6a). A limit of no length says that a task nobody has been told about
// is told about whenever the queue is read.
func (s State) NoticedWithin(now time.Time, after time.Duration, key string) bool {
	if s.Notice == nil || s.Notice.Key != key {
		return false
	}
	return after <= 0 || now.Sub(s.Notice.At) < after
}

// SettledWithin is whether the host said, within `after` of `now`, that the work of the
// task is over, and whether crewflow may therefore go on without asking about it again
// (docs/DESIGN.md §6a).
//
// A memory older than the shelf life is not a memory: смена или задача могли быть открыты
// заново, и очередь узнаёт об этом только тем, что спрашивает (F-105, §6a).
func (s State) SettledWithin(now time.Time, after time.Duration) bool {
	if s.Settled == nil {
		return false
	}
	return after <= 0 || now.Sub(s.Settled.At) < after
}

// SettledBy is the state of the task with what the host said about its work remembered:
// the fact a queue and a list of runs both read instead of asking again (§6a).
func (s State) SettledBy(at time.Time, by string) State {
	s.Settled = &Settled{By: by, At: at}
	return s
}

// Journals is where the journal of every run and the state of every task are kept:
// under the root crewflow has of its own and by the name of the project, so that
// the work of two projects never mixes and a person has one place to look
// (docs/DESIGN.md §7).
type Journals struct {
	// home is the root of what crewflow keeps, "~/.crewflow" in a real run.
	home string
	// repo is the project as "owner-name", the same name its worktrees are under.
	repo string
}

// newJournals is the place of the files of one project.
func newJournals(home, repo string) Journals {
	return Journals{home: home, repo: repo}
}

// JournalsOf is where the files of one project are kept under the root crewflow has
// of its own: the state of every task, the journal of every run and the journal of
// every merge of it. A command outside this package that keeps what it knows of a
// task — the merge of a change, the check after it — writes it where a run of that
// task wrote its own, so that a person has one place to look (docs/DESIGN.md §7).
func JournalsOf(home, repo string) Journals {
	return newJournals(home, repo)
}

// StatePath is the file that says where the last run of a task stopped.
func (j Journals) StatePath(number int) string {
	return filepath.Join(j.stateFolder(), strconv.Itoa(number)+".json")
}

// stateFolder is the folder the states of the tasks of the project are kept in, which
// is what `crewflow task list` reads and no network is needed for.
func (j Journals) stateFolder() string {
	return filepath.Join(j.home, "state", j.repo)
}

// JournalPath is what an attempt wrote, and errorJournalPath what it said on the
// way out: the two are read apart because a person reads the reason of a stop in
// the second one and the work of the run in the first.
func (j Journals) JournalPath(number, attempt int) string {
	return filepath.Join(j.home, "runs", j.repo, j.file(number, attempt, ".jsonl"))
}

// errorJournalPath is the way out of an attempt, next to what it wrote.
func (j Journals) errorJournalPath(number, attempt int) string {
	return filepath.Join(j.home, "runs", j.repo, j.file(number, attempt, ".err"))
}

// MergeJournalPath is what the merge of the change of a task wrote, next to the
// journals of the runs of that task: a merge is not an attempt of the executor and
// has no attempt of its own, and what git said about it is kept where a person
// looks for what a task of theirs did (docs/DESIGN.md §6, §7h).
func (j Journals) MergeJournalPath(number int) string {
	return filepath.Join(j.home, "runs", j.repo, strconv.Itoa(number)+"-merge.jsonl")
}

// file is the name of the file of an attempt, whatever it holds.
func (j Journals) file(number, attempt int, suffix string) string {
	return strconv.Itoa(number) + "-" + strconv.Itoa(attempt) + suffix
}

// AttemptFiles are the two files of one attempt, open and ready to be written to
// while the executor is writing: the journal of what it wrote and the way out of it,
// next to each other.
type AttemptFiles struct {
	// Journal and ErrorJournal are where the files are, which is what a person is
	// sent to and what `crewflow task watch` reads.
	Journal      string
	ErrorJournal string
	// Out and ErrOut are the open files. Everything written to them goes to the
	// files above as well: a journal that is written only at the end of a run is a
	// journal of a run that was cut short with nothing in it.
	Out, ErrOut *os.File
}

// Begin opens the two files of an attempt, before the executor is started. What a
// run writes is in them from its first line, so that a run that is cut short leaves
// its journal behind (docs/DESIGN.md §7).
func (j Journals) Begin(number, attempt int) (*AttemptFiles, error) {
	files := &AttemptFiles{
		Journal:      j.JournalPath(number, attempt),
		ErrorJournal: j.errorJournalPath(number, attempt),
	}
	var err error
	if files.Out, err = openForWriting(files.Journal); err != nil {
		return nil, err
	}
	if files.ErrOut, err = openForWriting(files.ErrorJournal); err != nil {
		_ = files.Out.Close()
		return nil, err
	}
	return files, nil
}

// Close is what a run does with the files of its attempt when the executor is done:
// the journal of a run is closed before anything is read out of it.
func (f *AttemptFiles) Close() error {
	return errors.Join(f.Out.Close(), f.ErrOut.Close())
}

// takeAway is what a run does with the files of an attempt that was never made: the
// journal of a run that did not start is a file nobody will ever read, and an empty one
// among the journals of a task is a question a person has to answer on their own
// (docs/DESIGN.md §7i).
func (f *AttemptFiles) takeAway() error {
	return errors.Join(f.Close(), os.Remove(f.Journal), os.Remove(f.ErrorJournal))
}

// openForWriting makes the file of a journal and empties it: the file of an attempt
// is written by one run, and what a run of an earlier attempt wrote is in the file of
// that attempt.
func openForWriting(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("make %s: %w", filepath.Dir(path), err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	return file, nil
}

// LoadState reads what crewflow kept of a task. A task with no state is not an
// error about the machine: it is a task that was never run, and the caller decides
// what that means.
//
// A state written before the format was versioned is read as the version before the
// first one, and nothing of it is lost: a run of before is a run of before, and the
// file is written in the current format the next time a run of the task touches it
// (docs/DESIGN.md §7h). A state of a version crewflow does not know is refused: a
// file of a shape nobody has looked at is a file crewflow cannot say anything true
// about, and a report that says nothing is worth more than one that lies.
func LoadState(path string) (State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return State{}, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("the state of the task in %s is not readable: %w", path, err)
	}
	if state.Schema > Schema {
		return State{}, fmt.Errorf("the state of the task in %s is of the format %d, and crewflow knows %d: "+
			"a newer crewflow wrote it, and this one cannot read what it does not know", path, state.Schema, Schema)
	}
	return state, nil
}

// SaveState writes the state of a task, in the current format of it whatever the one
// it was read in: a state of before is a state of a task of before, and the run that
// goes on with it writes what it knows today. It is written whole or not at all: a
// file cut in half by an interruption is a file that says the wrong thing about a
// run, and a state that is wrong is worse than none.
func SaveState(path string, state State) error {
	state.Schema = Schema
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("the state of the task: %w", err)
	}
	return writeFileAtomic(path, append(data, '\n'))
}

// writeFileAtomic writes through a file of its own next to the one it writes and
// moves it over: a reader of the state either sees the last whole state or the one
// before it, and never half of either.
func writeFileAtomic(path string, data []byte) error {
	folder := filepath.Dir(path)
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return fmt.Errorf("make %s: %w", folder, err)
	}
	temporary, err := os.CreateTemp(folder, filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("make a file in %s: %w", folder, err)
	}
	name := temporary.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write %s: %w", name, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("put %s in place of %s: %w", name, path, err)
	}
	return nil
}

// writeFile writes one file whole, which a journal may be big and a state never is.
func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("make %s: %w", filepath.Dir(path), err)
	}
	return os.WriteFile(path, data, 0o600)
}

// firstLine is the first line that says something, of the outputs in order. A
// command that said nothing has no line, and a report shows that instead of a
// blank.
func firstLine(outputs ...[]byte) string {
	for _, output := range outputs {
		for line := range bytes.Lines(output) {
			if text := string(bytes.TrimSpace(line)); text != "" {
				return text
			}
		}
	}
	return ""
}
