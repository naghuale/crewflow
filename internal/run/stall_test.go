package run

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/secret"
)

// TestARunThatHasJustStartedIsRunning: a run that has shown a sign of life a moment ago
// is a run that is going, and a list of runs says so — a mark of a run that stands is
// for a run nobody is watching, and a run of a minute ago is watched (docs/DESIGN.md §6).
func TestARunThatHasJustStartedIsRunning(t *testing.T) {
	home := t.TempDir()
	repo := "naghuale-crewflow"
	started := monday.Add(8 * time.Hour)
	writeState(t, home, repo, 43, "the run of a task", nil,
		try{startedAt: started, outcome: Running, pid: 100, lastAt: started, lastStep: stepExecutor})

	runs, err := List(home, repo, standingListAt(started.Add(30*time.Second), 100))
	if err != nil {
		t.Fatalf("List returned an error: %v", err)
	}
	entry, ok := runs.entry(43)
	if !ok {
		t.Fatalf("the list holds %+v, want the task 43 in it", tasksOf(runs))
	}
	if entry.Outcome != Running {
		t.Errorf("the outcome = %q, want %q: a run that has shown a sign of life is going", entry.Outcome, Running)
	}
	if entry.Stalled != nil {
		t.Errorf("the list says the run stands: %+v, want nothing said about a run that is working", entry.Stalled)
	}
}

// TestARunThatHasShownNothingStands: the whole of this task in one list. A run whose
// executor has written nothing for longer than `[executor] stall_after` is a run that
// stands, the list says how long it has been standing and what it was doing when it last
// showed a sign of life, and the state of the task still says `running` — the mark is
// worked out where it is read and is not a flag in a file that outlives the silence
// (docs/DESIGN.md §6, §7a, §7h).
func TestARunThatHasShownNothingStands(t *testing.T) {
	home := t.TempDir()
	repo := "naghuale-crewflow"
	started := monday.Add(8 * time.Hour)
	writeState(t, home, repo, 43, "the run of a task", nil,
		try{startedAt: started, outcome: Running, pid: 100, lastAt: started, lastStep: "go test ./..."})
	at := started.Add(11 * time.Minute)

	runs, err := List(home, repo, standingListAt(at, 100))
	if err != nil {
		t.Fatalf("List returned an error: %v", err)
	}
	entry, ok := runs.entry(43)
	if !ok {
		t.Fatalf("the list holds %+v, want the task 43 in it", tasksOf(runs))
	}
	if entry.Outcome != Stalled {
		t.Errorf("the outcome = %q, want %q: a run that has shown nothing for 11m stands", entry.Outcome, Stalled)
	}
	if entry.Stalled == nil {
		t.Fatal("the list says nothing about the silence of the run, want the length of it")
	}
	if entry.Stalled.For != 11*time.Minute {
		t.Errorf("the run has been standing for %s, want 11m", Idle(entry.Stalled.For))
	}
	if entry.Stalled.LastStep != "go test ./..." {
		t.Errorf("the last step of the run = %q, want the step the state of the task holds", entry.Stalled.LastStep)
	}
	// The state of the task is a fact of the process and the mark is not one of them: a
	// run that is going says `running` in the file whatever it is doing, and a list that
	// took the mark from there would be a list that has to be rewritten to be right.
	kept, err := LoadState(newJournals(home, repo).StatePath(43))
	if err != nil {
		t.Fatalf("load the state of the task: %v", err)
	}
	if got := kept.Attempts[0].Outcome; got != Running {
		t.Errorf("the state of the task holds %q, want %q: a mark is worked out, not kept", got, Running)
	}

	// A person reads the mark in the column of the outcome, with the length of the
	// silence in it, and the table of the runs says so (docs/DESIGN.md §6).
	var out bytes.Buffer
	if err := runs.Write(&out, screenAt(at)); err != nil {
		t.Fatalf("Write returned an error: %v", err)
	}
	for _, want := range []string{"stalled 11m", "43"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the list wrote\n%s\nwant it to hold %q", out.String(), want)
		}
	}
}

// TestTheLastLineOfTheJournalIsTheStepOfARun: the sign of life of a run is the last
// line the executor wrote, read the way a watch reads a journal, and the journal is what
// the state of the task names — not the moment the run began (docs/DESIGN.md §6, §7a).
func TestTheLastLineOfTheJournalIsTheStepOfARun(t *testing.T) {
	home := t.TempDir()
	repo := "naghuale-crewflow"
	started := monday.Add(8 * time.Hour)
	writeState(t, home, repo, 43, "the run of a task", nil,
		try{startedAt: started, outcome: Running, pid: 100, lastAt: started, lastStep: stepExecutor})
	journal := newJournals(home, repo).JournalPath(43, 1)
	lastLine := `{"type":"text","sessionID":"ses_fake","part":{"type":"text","text":"I did the work."}}` + "\n"
	putJournal(t, journal, lastLine, started.Add(9*time.Minute))
	// The silence of the project of the test is a minute, so that a line written nine
	// minutes into a run is a sign of life two minutes old and nothing more.
	patient := func(at time.Time, going ...int) ListEnv {
		env := listOf(at, going...)
		env.StallAfter = time.Minute
		return env
	}

	runs, err := List(home, repo, patient(started.Add(11*time.Minute), 100))
	if err != nil {
		t.Fatalf("List returned an error: %v", err)
	}
	entry, ok := runs.entry(43)
	if !ok || entry.Stalled == nil {
		t.Fatalf("the list holds %+v, want the task 43 standing", runs.Entries)
	}
	if entry.Stalled.For != 2*time.Minute {
		t.Errorf("the run has been standing for %s, want 2m: the last line of the journal is a sign of life",
			Idle(entry.Stalled.For))
	}
	// The line of the executor is read through its profile: a person reads the words of
	// the agent and not the events of the program it is.
	if got, want := entry.Stalled.LastStep, "I did the work."; got != want {
		t.Errorf("the last step of the run = %q, want %q", got, want)
	}
}

// TestARunThatWaitsForTheKeychainStandsWithItsReason: four runs of the mode of the bot
// stood for an hour and a half in front of the window of the keychain of the machine and
// nothing in the state of the task said so (F-039, F-041, §7i). A run of the mode of the
// bot goes to the keychain for the key of the App, a run that is not answered there is a
// run that stands, and the state of the task says what it stands at (docs/DESIGN.md §6).
func TestARunThatWaitsForTheKeychainStandsWithItsReason(t *testing.T) {
	home := t.TempDir()
	repo := "naghuale-crewflow"
	started := monday.Add(8 * time.Hour)
	writeState(t, home, repo, 43, "the run of a task", nil,
		try{startedAt: started, outcome: Running, pid: 100, mode: "bot",
			lastAt: started, lastStep: stepIdentity, reason: reasonApproval})
	// The wait of a keychain is two minutes long (§7i), so a project that wants to see a
	// run standing in front of its window says so: the threshold of the project is a
	// number a person chose, and this one is a minute.
	patient := func(at time.Time, going ...int) ListEnv {
		env := listOf(at, going...)
		env.StallAfter = time.Minute
		return env
	}

	runs, err := List(home, repo, patient(started.Add(2*time.Minute), 100))
	if err != nil {
		t.Fatalf("List returned an error: %v", err)
	}
	entry, ok := runs.entry(43)
	if !ok || entry.Stalled == nil {
		t.Fatalf("the list holds %+v, want the task 43 standing in front of the keychain", runs.Entries)
	}
	if entry.Outcome != Stalled {
		t.Errorf("the outcome = %q, want %q", entry.Outcome, Stalled)
	}
	if entry.Stalled.Reason != reasonApproval {
		t.Errorf("the reason of the silence = %q, want %q: a run that is not answered in the window needs a person",
			entry.Stalled.Reason, reasonApproval)
	}
	// The answer of `-json` is what an orchestrator reads, and the reason of the silence
	// is a field of it beside the length of the silence and the step of the run.
	said := jsonOf(t, entry)
	for _, want := range []string{
		`"outcome": "stalled"`, `"stalled_for": 120`, `"last_step": "` + stepIdentity + `"`,
		`"reason": "` + reasonApproval + `"`,
	} {
		if !strings.Contains(said, want) {
			t.Errorf("the answer of -json is\n%s\nwant it to hold %q", said, want)
		}
	}
}

// TestAProjectThatNamesNoSilenceMarksNothing: crewflow does not invent a moment at which
// a run of somebody else's project becomes a thing to worry about, and a list that marks
// every run of a project that named no silence is a list nobody reads (§6, §5).
func TestAProjectThatNamesNoSilenceMarksNothing(t *testing.T) {
	home := t.TempDir()
	repo := "naghuale-crewflow"
	started := monday.Add(8 * time.Hour)
	writeState(t, home, repo, 43, "the run of a task", nil,
		try{startedAt: started, outcome: Running, pid: 100, lastAt: started, lastStep: stepExecutor})

	if got := outcomeOf(t, home, repo, listOf(started.Add(3*time.Hour), 100)); got != Running {
		t.Errorf("the outcome = %q, want %q: a project that says no silence has no standing runs", got, Running)
	}
}

// TestARunThatIsOverNeverStands: a run that was cut short an hour ago is not a run that
// stands, however long ago it showed a sign of life: what has to be looked at is a run
// that is still going (docs/DESIGN.md §6).
func TestARunThatIsOverNeverStands(t *testing.T) {
	home := t.TempDir()
	repo := "naghuale-crewflow"
	started := monday.Add(8 * time.Hour)
	writeState(t, home, repo, 43, "the run of a task", nil,
		try{startedAt: started, endedAt: started.Add(time.Minute), outcome: Interrupted, pid: 100,
			lastAt: started, lastStep: stepExecutor})

	runs, err := List(home, repo, standingListAt(started.Add(3*time.Hour), 100))
	if err != nil {
		t.Fatalf("List returned an error: %v", err)
	}
	entry, ok := runs.entry(43)
	if !ok {
		t.Fatalf("the list holds %+v, want the task 43 in it", tasksOf(runs))
	}
	if entry.Outcome != Interrupted || entry.Stalled != nil {
		t.Errorf("the list says %q and %+v, want a run that was cut short and nothing said about a silence",
			entry.Outcome, entry.Stalled)
	}
}

// TestARunIsWrittenDownBeforeTheExecutorStarts: the failure this task is here for. Four
// runs of the mode of the bot stood for an hour and a half with not a line in
// `~/.crewflow/runs` — the state of the task was written after the run had been to the
// keychain of the machine, and a run that stands in front of that window is not there to
// be seen (F-039, F-041, docs/DESIGN.md §6, §7i).
//
// So the state of the task is written before crewflow goes to the machine for anything,
// with the sign of life of the run and what it stands at, and the run that goes on is
// marked as one that was refused nothing.
func TestARunIsWrittenDownBeforeTheExecutorStarts(t *testing.T) {
	m := newMachine(t)
	m.answers["git config"] = answer{}
	m.answers["opencode"] = answer{stdout: theRun}
	// The keychain of the machine: it has a secret of the app, it does not trust this
	// build of the program, and the owner of the machine is at the window and has not
	// answered yet.
	window := make(chan struct{})
	notices := secret.NewNotices(nil)
	env := m.env()
	env.Notices = notices
	env.Secrets = secret.Waited(&waiting{on: window}, notices, time.Minute)
	host := &host{task: taskOf(43), opened: true, identity: theBot(), store: env.Secrets}
	cfg := inTheModeOfTheBot(projectOf(t, m.worktrees, ""))

	over := make(chan outcome, 1)
	go func() {
		result, err := Run(t.Context(), env, cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
		over <- outcome{result: result, err: err}
	}()

	state := stateWhileItWaits(t, m)
	if len(state.Attempts) != 1 {
		t.Fatalf("the state of the task holds %d attempts while the run waits, want the one that is going", len(state.Attempts))
	}
	attempt := state.Attempts[0]
	if attempt.Outcome != Running {
		t.Errorf("the outcome of the attempt = %q, want %q while the run waits", attempt.Outcome, Running)
	}
	if attempt.Reason != reasonApproval {
		t.Errorf("what the run stands at = %q, want %q", attempt.Reason, reasonApproval)
	}
	if attempt.LastStep != stepIdentity {
		t.Errorf("the last step of the run = %q, want %q", attempt.LastStep, stepIdentity)
	}
	if attempt.Identity.Mode != "bot" {
		t.Errorf("the state holds the mode %q, want %q: the mode of the file of the project is what the state holds "+
			"while crewflow is still working the identity of the run out", attempt.Identity.Mode, "bot")
	}
	if m.askedFor("opencode") {
		t.Errorf("the executor was started %v, want the run still standing in front of the window", m.lines())
	}

	// A list of runs, a while later, calls the run standing and says what it stands at:
	// this is the whole of the mark, and a list of runs is where a person looks for it
	// (docs/DESIGN.md §6).
	waited := listOf(time.Now().Add(2*time.Minute), 4242)
	waited.StallAfter = time.Minute
	runs, err := List(m.home, "naghuale-crewflow", waited)
	if err != nil {
		t.Fatalf("List returned an error: %v", err)
	}
	entry, ok := runs.entry(43)
	if !ok || entry.Stalled == nil {
		t.Fatalf("the list holds %+v, want the run of the task 43 standing", runs.Entries)
	}
	if entry.Outcome != Stalled || entry.Stalled.Reason != reasonApproval {
		t.Errorf("the list says %q (%+v), want a run standing at %q", entry.Outcome, entry.Stalled, reasonApproval)
	}

	// The owner of the machine answers the window, and the run goes on to the executor:
	// the state that was there while it waited is the state of the run that went on.
	close(window)
	finished := <-over
	if finished.err != nil {
		t.Fatalf("Run returned an error: %v", finished.err)
	}
	if finished.result.Outcome != ChangeRequestOpened {
		t.Errorf("the outcome = %q, want %q: a run that was answered in the window goes on", finished.result.Outcome, ChangeRequestOpened)
	}
	after := stateOf(t, m, 43)
	if got := after.Attempts[0].Outcome; got != ChangeRequestOpened {
		t.Errorf("the state of the task holds %q after the run, want %q", got, ChangeRequestOpened)
	}
}

// TestTheRunThatIsGoingSaysWhenItStands: the journal of the attempt is what a person
// opens to see what a run is doing, and a run that stands has to be in it — once for the
// silence and once for its end, and not once a minute for as long as it stands
// (docs/DESIGN.md §6, §7a).
func TestTheRunThatIsGoingSaysWhenItStands(t *testing.T) {
	m := newMachine(t)
	more := make(chan string)
	wrote := make(chan struct{})
	m.answers["opencode"] = answer{stdout: theRun, wrote: wrote, more: more}
	host := &host{task: taskOf(43), opened: true}
	cfg := projectOf(t, m.worktrees, "")
	cfg.Executor.StallAfter = "1m"

	over := make(chan outcome, 1)
	go func() {
		result, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
		over <- outcome{result: result, err: err}
	}()
	<-wrote
	journals := newJournals(m.home, "naghuale-crewflow")
	journal := journals.JournalPath(43, 1)
	wayOut := read(t, journals.errorJournalPath(43, 1))
	// The sign of life of the run is the moment its executor wrote, and that is a moment
	// of a file the machine wrote for real: the clock of the machine is put there, so
	// that the silence a test sets is the silence it says it is (§6).
	m.begins(writtenAt(t, journal))

	// A run that is working says nothing: eleven minutes of silence are a mark, and a
	// mark that were given for every tick of the watch would be a file of noise. The
	// turn of the watch asks the machine for the time, and the machine is a minute on
	// every question.
	m.quiet(11 * time.Minute)
	m.look()
	standing := read(t, journals.errorJournalPath(43, 1))
	if said := standing[len(wayOut):]; !strings.Contains(said, "crewflow: stalled") {
		t.Errorf("the way out of the run holds %q, want it to say that the run stands", said)
	}
	for _, want := range []string{"12m", "The change request is open."} {
		if !strings.Contains(standing, want) {
			t.Errorf("the way out of the run holds %q, want it to hold %q", standing, want)
		}
	}
	m.quiet(11 * time.Minute)
	m.look()
	if again := read(t, journals.errorJournalPath(43, 1)); again != standing {
		t.Errorf("the way out of the run holds %q after another tick, want the same one line: a mark is not a line a minute",
			again[len(standing):])
	}

	// The executor writes again, and the run is working: the mark of the silence is over
	// and the end of it is said once (docs/DESIGN.md §6).
	more <- `{"type":"text","sessionID":"ses_fake","part":{"type":"text","text":"And the gate is green."}}` + "\n"
	m.begins(writtenAt(t, journal))
	m.look()
	working := read(t, journals.errorJournalPath(43, 1))
	if !strings.Contains(working, resumedOf()) {
		t.Errorf("the way out of the run holds %q, want it to say that the run is working again", working[len(standing):])
	}
	// And the run stands a second time: two episodes of silence, two pairs of lines.
	m.quiet(11 * time.Minute)
	m.look()
	close(more)
	finished := <-over
	if finished.err != nil {
		t.Fatalf("Run returned an error: %v", finished.err)
	}
	if finished.result.Outcome != ChangeRequestOpened {
		t.Errorf("the outcome = %q, want %q: a mark does not change what a run is", finished.result.Outcome, ChangeRequestOpened)
	}
	said := read(t, journals.errorJournalPath(43, 1))
	if got := strings.Count(said, "crewflow: stalled"); got != 2 {
		t.Errorf("the way out of the run says %d times that the run stands, want 2: one for each episode of silence", got)
	}
	if got := strings.Count(said, resumedOf()); got != 1 {
		t.Errorf("the way out of the run says %d times that the run is working again, want 1", got)
	}
	// A mark of a run changes nothing of the run: the attempt ends the way it would have
	// ended, and the state of the task holds the outcome and not the mark (§6, §7h).
	if got := stateOf(t, m, 43).Attempts[0].Outcome; got != ChangeRequestOpened {
		t.Errorf("the state of the task holds %q, want %q", got, ChangeRequestOpened)
	}
}

// TestCheckStalledSaysARunOnceAnEpisode: a command of the schedule of an orchestrator is
// asked about the runs of a project every few minutes, and every turn of it is answered.
// What it writes under a task is one line for the beginning of a silence and one for its
// end, whatever the number of turns in between: a person who reads the task sees two
// lines and not two hundred (docs/DESIGN.md §6).
func TestCheckStalledSaysARunOnceAnEpisode(t *testing.T) {
	home := t.TempDir()
	repo := "naghuale-crewflow"
	started := monday.Add(8 * time.Hour)
	writeState(t, home, repo, 43, "the run of a task", nil,
		try{startedAt: started, outcome: Running, pid: 100, lastAt: started, lastStep: "go test ./..."})
	journal := newJournals(home, repo).JournalPath(43, 1)
	said := `{"type":"text","sessionID":"ses_fake","part":{"type":"text","text":"I did the work."}}` + "\n"
	// The executor wrote half a minute into the run, and the watch of the orchestrator
	// looks eleven minutes after the run began: the run has shown nothing for ten and a
	// half minutes, which is longer than the silence of the project (§6).
	putJournal(t, journal, said, started.Add(30*time.Second))
	saidAt := started.Add(11 * time.Minute)
	var records []string
	record := func(_ context.Context, one Standing) (bool, string) {
		records = append(records, Record(one))
		return true, ""
	}

	// The first turn of the watch: the run has stood for eleven minutes and nobody has
	// written it under the task.
	turns := checkOf(t, home, repo, saidAt, 100, record)
	if len(turns) != 1 || !turns[0].Stalled || !turns[0].Said {
		t.Fatalf("the answer = %+v, want the one run standing with the record left", turns)
	}
	if got := turns[0].StalledFor; got != 630 {
		t.Errorf("the run has been standing for %v seconds, want 630", got)
	}
	if turns[0].LastStep != "I did the work." {
		t.Errorf("the last step of the run = %q, want the last line of its journal", turns[0].LastStep)
	}
	// The next turn of the watch says the same thing and writes nothing: a schedule that
	// ran every minute is not a record under the task every minute (§6).
	again := checkOf(t, home, repo, saidAt.Add(time.Minute), 100, record)
	if len(again) != 1 || !again[0].Stalled || again[0].Said {
		t.Errorf("the answer = %+v, want the run standing and no record left", again)
	}
	if len(records) != 1 {
		t.Errorf("%d records were left under the task, want 1", len(records))
	}
	for _, want := range []string{"43", "43-1", "10m", "I did the work."} {
		if !strings.Contains(records[0], want) {
			t.Errorf("the record under the task is %q, want it to hold %q", records[0], want)
		}
	}

	// The executor writes again: the silence is over and the task is told so once, and
	// the run that is working is in the answer of this turn and not only of the last one.
	putJournal(t, journal, said, saidAt.Add(2*time.Minute))
	working := checkOf(t, home, repo, saidAt.Add(2*time.Minute), 100, record)
	if len(working) != 1 || working[0].Stalled || !working[0].Said {
		t.Fatalf("the answer = %+v, want the run no longer standing with the record of it", working)
	}
	if !strings.Contains(records[1], "is working again") {
		t.Errorf("the record under the task is %q, want it to say that the run is working again", records[1])
	}
	if nothing := checkOf(t, home, repo, saidAt.Add(3*time.Minute), 100, record); len(nothing) != 0 {
		t.Errorf("the answer = %+v, want nothing: a run that is working is not a thing to write about", nothing)
	}

	// The run stands a second time, and the task is written about a second time: two
	// episodes of silence, two pairs of records (§6).
	quiet := saidAt.Add(15 * time.Minute)
	putJournal(t, journal, said, quiet.Add(-11*time.Minute))
	second := checkOf(t, home, repo, quiet, 100, record)
	if len(second) != 1 || !second[0].Stalled || !second[0].Said {
		t.Fatalf("the answer = %+v, want the run standing again with the record left", second)
	}
	if got := len(records); got != 3 {
		t.Errorf("%d records were left under the task, want 3: two beginnings and one end", got)
	}
	// And the run of that second episode is over rather than working again: the end of a
	// silence of a run that has finished says how it came out (§6).
	ended := quiet.Add(time.Minute)
	kept, err := LoadState(newJournals(home, repo).StatePath(43))
	if err != nil {
		t.Fatalf("load the state of the task: %v", err)
	}
	if err := SaveState(newJournals(home, repo).StatePath(43), kept.Ended(ended, ChangeRequestOpened)); err != nil {
		t.Fatalf("write the state of the task: %v", err)
	}
	last := checkOf(t, home, repo, ended.Add(time.Minute), 4242, record)
	if len(last) != 1 || last[0].Stalled || !last[0].Said || last[0].Outcome != ChangeRequestOpened {
		t.Fatalf("the answer = %+v, want the end of the silence of a run that is over", last)
	}
	if !strings.Contains(records[3], "pr-opened") {
		t.Errorf("the record under the task is %q, want it to say how the run came out", records[3])
	}
}

// TestCheckStalledOfAProjectWithNoHostSaysSoAndWritesNothing: a run that stands in a
// project whose host cannot write under a task is reported to the person who asked and
// not written about anywhere, and the next turn of the watch tries again: the journal of
// the attempt is the record of such a run, and crewflow does not pretend otherwise
// (docs/DESIGN.md §6, §7g).
func TestCheckStalledOfAProjectWithNoHostSaysSoAndWritesNothing(t *testing.T) {
	home := t.TempDir()
	repo := "naghuale-crewflow"
	started := monday.Add(8 * time.Hour)
	writeState(t, home, repo, 43, "the run of a task", nil,
		try{startedAt: started, outcome: Running, pid: 100, lastAt: started, lastStep: stepExecutor})

	turns, err := CheckStalled(t.Context(), home, repo, standingListAt(started.Add(11*time.Minute), 100), nil)
	if err != nil {
		t.Fatalf("CheckStalled returned an error: %v", err)
	}
	if len(turns) != 1 || !turns[0].Stalled || turns[0].Said {
		t.Fatalf("the answer = %+v, want the run standing and nothing written", turns)
	}
	if turns[0].Problem != NoRecord {
		t.Errorf("what stood in the way = %q, want %q", turns[0].Problem, NoRecord)
	}
	// The state of the task holds no record of a silence nobody was told about, so that
	// the next turn of the watch is the first one again.
	kept, err := LoadState(newJournals(home, repo).StatePath(43))
	if err != nil {
		t.Fatalf("load the state of the task: %v", err)
	}
	if kept.Attempts[0].ReportedAt != nil {
		t.Errorf("the state of the task holds the moment of a record at %v, want none: nothing was written under the task",
			kept.Attempts[0].ReportedAt)
	}
}

// TestCheckStalledOfAProjectThatWasNeverRun: a machine crewflow has run nothing in has
// nothing standing in it, and a schedule that asks finds that out (docs/DESIGN.md §6).
func TestCheckStalledOfAProjectThatWasNeverRun(t *testing.T) {
	turns, err := CheckStalled(t.Context(), t.TempDir(), "naghuale-crewflow", standingListAt(monday, 100), nil)
	if err != nil {
		t.Fatalf("CheckStalled returned an error: %v", err)
	}
	if len(turns) != 0 {
		t.Errorf("the answer = %+v, want nothing at all", turns)
	}
}

// TestIdleIsWhatAPersonReads: the length of a silence goes into a journal, into a record
// under a task and into a cell of a table, and a program does not read "11m0s".
func TestIdleIsWhatAPersonReads(t *testing.T) {
	cases := []struct {
		silence time.Duration
		want    string
	}{
		{silence: 35 * time.Second, want: "35s"},
		{silence: 11 * time.Minute, want: "11m"},
		{silence: 2*time.Hour + 20*time.Minute, want: "2h20m"},
		{silence: 9 * time.Hour, want: "9h"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			if got := Idle(tc.silence); got != tc.want {
				t.Errorf("Idle(%s) = %q, want %q", tc.silence, got, tc.want)
			}
		})
	}
}

// standingListAt is the machine a list of runs is asked with the silence of a project that
// waits ten minutes before it calls a run standing, which is what `[executor]
// stall_after` says for a project that says nothing.
func standingListAt(at time.Time, going ...int) ListEnv {
	env := listOf(at, going...)
	env.StallAfter = 10 * time.Minute
	return env
}

// checkOf is one turn of the watch of an orchestrator over the runs of a project, and the
// records it left under the tasks of it.
func checkOf(t *testing.T, home, repo string, at time.Time, pid int, say Say) []Standing {
	t.Helper()
	turns, err := CheckStalled(t.Context(), home, repo, standingListAt(at, pid), say)
	if err != nil {
		t.Fatalf("CheckStalled returned an error: %v", err)
	}
	return turns
}

// putJournal is what an executor wrote into the journal of an attempt, and the moment it
// wrote it: a sign of life of a run is the time of the last line of its journal, and a
// test has to be able to say when that line was written (docs/DESIGN.md §6).
func putJournal(t *testing.T, path, text string, written time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("make the folder of the journal: %v", err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatalf("write the journal of the attempt: %v", err)
	}
	if err := os.Chtimes(path, written, written); err != nil {
		t.Fatalf("set the moment the journal was written: %v", err)
	}
}

// writtenAt is the moment a file of a run was written, which is what the sign of life of
// the run is: the time of the last line of the journal of the attempt (§6).
func writtenAt(t *testing.T, path string) time.Time {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("read the moment %s was written: %v", path, err)
	}
	return info.ModTime()
}

// stateWhileItWaits is the state of the task of a run that is standing in front of the
// window of the keychain of the machine, read while it waits: a run that is not written
// down is a run nobody can see (F-039, docs/DESIGN.md §6).
func stateWhileItWaits(t *testing.T, m *machine) State {
	t.Helper()
	var state State
	// The run of the test is on its own goroutine, and the state of the task is written
	// before it goes to the keychain: it is there as soon as the run has begun.
	for range 1000 {
		loaded, err := LoadState(newJournals(m.home, "naghuale-crewflow").StatePath(43))
		if err == nil && len(loaded.Attempts) > 0 {
			state = loaded
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(state.Attempts) == 0 {
		t.Fatal("the state of the task holds no attempt while the run stands in front of the window of the keychain")
	}
	return state
}

// waiting is the keychain of a machine whose owner is at the window of the system and has
// not answered it: the read of the key of the App stands still until the test answers,
// and the store announces the window before it waits, because the machine does not trust
// this build of the program (docs/DESIGN.md §7i).
type waiting struct {
	on chan struct{}
}

func (w *waiting) Get(string, string) ([]byte, error) {
	<-w.on
	return []byte("the key of the app of a test"), nil
}

func (*waiting) Set(string, string, []byte) error { return nil }

func (*waiting) Has(string, string) (bool, error) { return true, nil }

func (*waiting) Allowed(string) (bool, error) { return false, nil }
