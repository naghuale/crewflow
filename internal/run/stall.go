package run

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/naghuale/crewflow/internal/run/profile"
	"github.com/naghuale/crewflow/internal/secret"
)

// A run that is going and has not shown a sign of life for a while is standing, and a
// standing run is a run nobody is watching: four runs of the mode of the bot stood for
// an hour and a half with not a line in `~/.crewflow/runs` and not one line in
// `crewflow task list`, and the orchestrator noticed them by accident while taking the
// output of commands (F-039, F-041, journal #37, 30.09).
//
// So crewflow watches every run that is going, keeps the moment of the last sign of
// life of it — the last line the executor wrote, or the last step of crewflow itself —
// and marks a run that has shown nothing for longer than the silence of the project as
// `stalled` (docs/DESIGN.md §6, §7a).
//
// The mark is not an outcome of a run and it does not change one: the process goes on
// working and the attempt ends the way it would have ended anyway. Nothing stops a
// standing run but a person or an orchestrator that decides to, exactly as §7a says
// about a hang: a hang is a thing to be shown, not a thing to be killed.

// Stall is the silence of a run that is going: how long it has been standing, what it
// was doing when it last showed a sign of life, and what it stands at where crewflow
// knows it before it stands there.
type Stall struct {
	// For is how long the run has shown nothing.
	For time.Duration
	// LastStep is what the run showed the last time it showed anything: a line of the
	// executor read the way a watch reads a journal, or a step of crewflow itself.
	LastStep string
	// Reason is what the run stands at, where crewflow knows it: a run in the mode of
	// the bot goes to the keychain of the machine for the key of the App of the host,
	// and a run that is not answered in the window of the system is a run that needs a
	// person, not a run that is quiet (docs/DESIGN.md §7i).
	Reason string
}

// The steps of crewflow a run stands at, as a person reads them in a list of runs. They
// are short and they are in English whatever the language of the project, because a
// state file, a journal and a record under a task are read by an orchestrator and by
// the tools of this project (docs/DESIGN.md §7a).
const (
	// stepBegan is the run of a task before it has done anything at all.
	stepBegan = "the run of the task has begun"
	// stepIdentity is the run going to the host of the project for the name the
	// executor of it works under, which in the mode of the bot means a key of an App
	// out of the keychain of the machine.
	stepIdentity = "the account of the host the executor works as"
	// stepPreparing is the run getting the task ready for the executor: the scratch of
	// the run out of the repository, what the executor may read outside the worktree
	// (§7d), and the worktree of the task for an account of the host (§7i).
	stepPreparing = "the worktree and the rights of the run"
	// stepExecutor is the run starting the executor of the task, and the sign of life
	// it holds from then on until the executor says something itself.
	stepExecutor = "the executor of the run"
	// stepSilent is the run of a task whose executor has written nothing yet, which is
	// a run that has begun and shown nothing since.
	stepSilent = "the executor has written nothing yet"
)

// stepColumns is how much of a step a cell of a table, a line of a journal and a
// record under a task hold: a step is the last thing a run said, and a run said a
// whole line of a journal where a person reads one line.
const stepColumns = 60

// standing says whether the silence of a run is longer than the silence the project
// agreed to put up with. A project that says no limit has none: crewflow does not
// invent a moment at which a run of somebody else's project becomes a thing to worry
// about, and a list that marks every run of a project that named no threshold is a
// list nobody reads.
func (s Stall) standing(after time.Duration) bool {
	return after > 0 && s.For > after
}

// silenceOf is the silence of an attempt at the moment it is asked about, worked out of
// what the state of the task holds and of the journal of the attempt, with the executor
// of the run read by name because a journal is read the way the run that wrote it was
// read. The sign of life is the later of the two: the last step of crewflow that the
// state names, and the last line the executor wrote into the journal. A state of before
// crewflow kept any of this names nothing, and a run that began and has shown nothing
// since has been standing since it began.
//
// It is worked out every time it is asked about and never stored as a mark: a run that
// is marked is a run a person has to look at, and a flag in a file that says nothing
// about the time is a flag that outlives the silence it was written for
// (docs/DESIGN.md §6, §7h).
func silenceOf(attempt Attempt, executor string, now time.Time) Stall {
	at, step := attempt.aliveAt(), attempt.LastStep
	if written, line, ok := lastLineOf(attempt.Journal, executor); ok && written.After(at) {
		at, step = written, line
	}
	if step == "" {
		step = stepSilent
	}
	return Stall{
		For:      max(now.Sub(at), 0),
		LastStep: step,
		Reason:   attempt.Reason,
	}
}

// aliveAt is when the run of an attempt last showed a sign of life, as the state of the
// task knows it: the last step of crewflow that was written down, and the moment the
// run was started where nothing of it was.
func (a Attempt) aliveAt() time.Time {
	if a.LastAt.IsZero() {
		return a.StartedAt
	}
	return a.LastAt
}

// journalTail is how much of the end of a journal is read to find the last line the
// executor wrote. A line of an agent is a line of text, and the last of them is in the
// last few kilobytes whatever a run of an hour has written before it.
const journalTail = 4096

// lastLineOf is when the journal of an attempt was last written and what its last line
// says, and whether there is such a line at all: a run that is going has a journal, and
// a state of a task of before may name a file that has been taken away.
//
// The line is read through the profile of the run, because the words of an agent are
// not the events it writes them as, and a table that shows `{"type":"tool", …}` where a
// person reads `bash: go test ./...` is a table about a format (docs/DESIGN.md §7a).
func lastLineOf(path, executor string) (time.Time, string, bool) {
	if path == "" {
		return time.Time{}, "", false
	}
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, "", false
	}
	file, err := os.Open(path)
	if err != nil {
		return time.Time{}, "", false
	}
	defer func() { _ = file.Close() }()
	from := max(info.Size()-journalTail, 0)
	if _, err := file.Seek(from, io.SeekStart); err != nil {
		return time.Time{}, "", false
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return time.Time{}, "", false
	}
	// The beginning of what was read is the middle of a line wherever the file is
	// longer than the tail: half a line is not a line, and it is not what the run said
	// last.
	text := string(data)
	if from > 0 {
		_, rest, found := strings.Cut(text, "\n")
		if !found {
			text = ""
		} else {
			text = rest
		}
	}
	raw := lastOf(saidLines(text))
	if raw == "" {
		return info.ModTime(), "", false
	}
	return info.ModTime(), stepOf(raw, executor), true
}

// stepOf is the last thing a run said, in the words a person reads it in and as short
// as the places that show it. A line of an agent that a profile cannot read is shown as
// it is: the run said something, and what cannot be read is not better than nothing.
func stepOf(raw, executor string) string {
	read := profile.Named(executor).Read([]string{raw}, nil)
	said := raw
	if len(read) > 0 {
		said = read[len(read)-1]
	}
	return cut(line(said), stepColumns)
}

// stalledOf is the line the way out of an attempt holds when the run of it goes quiet:
// how long it has been quiet, what it was doing when it last said something, and what
// it stands at where crewflow knows it. It is said once for an episode of silence and
// not once a minute: a file that says the same thing every minute is a file nobody
// reads to the end (docs/DESIGN.md §6, §7a).
func stalledOf(silence Stall) string {
	said := fmt.Sprintf("crewflow: stalled — no activity for %s, the last step: %s",
		Idle(silence.For), silence.LastStep)
	if silence.Reason != "" {
		said += ", standing at: " + silence.Reason
	}
	return said
}

// resumedOf is the line the way out of an attempt holds when the run of it is working
// again, so that a reader of the file sees the end of an episode of silence in it and
// not only its beginning.
func resumedOf() string { return "crewflow: the run of the task is working again" }

// Idle is how long a silence is in the words a person reads a time in: "35s", "11m",
// "2h20m". A journal, a record under a task and a report of a command are read by a
// person, and "11m0s" is what a program prints.
func Idle(silence time.Duration) string {
	switch {
	case silence < time.Minute:
		return fmt.Sprintf("%ds", int(silence.Seconds()))
	case silence < time.Hour:
		return fmt.Sprintf("%dm", int(silence.Minutes()))
	case silence%time.Hour == 0:
		return fmt.Sprintf("%dh", int(silence.Hours()))
	default:
		return fmt.Sprintf("%dh%dm", int(silence.Hours()), int(silence.Minutes())%60)
	}
}

// saidLines are the lines of a text with the empty ones and the ends of the lines left out:
// a journal is read line by line, and an empty line says nothing.
func saidLines(text string) []string {
	var found []string
	for raw := range strings.Lines(text) {
		if line := strings.TrimRight(raw, "\r\n"); strings.TrimSpace(line) != "" {
			found = append(found, line)
		}
	}
	return found
}

// lastOf is the last of the lines of a journal, and nothing where there are none.
func lastOf(all []string) string {
	if len(all) == 0 {
		return ""
	}
	return all[len(all)-1]
}

// alive is the sign of life of a run: when it last showed one, what it was, and what it
// stands at. The state of the task is written from it at every step of crewflow, and
// the watcher of the run reads it while the executor writes — one thing of one run and
// two goroutines of it, so it is behind a lock and not a field of the run.
type alive struct {
	mu     sync.Mutex
	at     time.Time
	step   string
	reason string
}

// show is a sign of life of the run, from a step of crewflow or from the state of the
// task it was last written from.
func (a *alive) show(at time.Time, step, reason string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.at, a.step, a.reason = at, step, reason
}

// sign is what the run showed the last time it showed anything.
func (a *alive) sign() (time.Time, string, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.at, a.step, a.reason
}

// watcher looks at the silence of a run while its executor works, and says it in the
// way out of the attempt once for every episode of it.
//
// The lines go there and not into the journal of the attempt, because the journal is
// what the silence of a run is worked out of: a line crewflow writes about the run
// would be the sign of life of a run that is standing, and a run that stands would
// never be marked (docs/DESIGN.md §6, §7a).
type watcher struct {
	runner *runner
	// attempt is the attempt the run is in, and after the silence the project agreed
	// to put up with.
	attempt Attempt
	files   *AttemptFiles
	after   time.Duration
	// standing is whether the run is standing right now, and is what keeps an episode
	// of silence to one line. Only the goroutine of the watch touches it.
	standing bool
}

// watch is the watch of the silence of a run, and the function that stops it and waits
// for it: a run has to know that nobody is writing to the files of its attempt before
// it closes them. The function is safe to call twice, because every return of a run that
// watches stops the watch and not one of them is the one that got there first.
func (r *runner) watch(ctx context.Context, attempt Attempt, files *AttemptFiles, after time.Duration) func() {
	if r.env.Tick == nil || after <= 0 {
		return func() {}
	}
	w := &watcher{runner: r, attempt: attempt, files: files, after: after}
	return r.env.Tick(ctx, stallTick(after), w.look)
}

// look is one turn of the watch: the silence of the run as the run itself knows it, and
// a line for every change of what it is — into a silence and out of it. Nothing else is
// said, and nothing is said twice.
func (w *watcher) look() {
	silence := silenceOf(w.runner.attemptNow(w.attempt), w.runner.profile.Name(), w.runner.env.Now())
	switch {
	case silence.standing(w.after) && !w.standing:
		w.standing = true
		w.note(stalledOf(silence))
	case !silence.standing(w.after) && w.standing:
		w.standing = false
		w.note(resumedOf())
	}
}

// note adds a line of the watch to the way out of the attempt, through a handle of its
// own, and goes on when it cannot: what a person reads about the standing of a run is
// the state of the task and the record under the task, and a line of a journal that
// could not be written is not a reason to stop a run for. The line goes through the
// redactor of the secrets of the identity like every other line of a run, because the
// last step of a run is a line the executor wrote (docs/DESIGN.md §7e).
func (w *watcher) note(said string) {
	_ = appendLine(w.files.ErrorJournal, secret.Redact(said, w.runner.identity.Secrets...))
}

// attemptNow is the attempt of the run as the run itself knows it: the sign of life the
// run holds, which is further along than the state of the task on the disk and is what
// the watch of the run looks at.
func (r *runner) attemptNow(attempt Attempt) Attempt {
	at, step, reason := r.alive.sign()
	attempt.LastAt, attempt.LastStep, attempt.Reason = at, step, reason
	return attempt
}

// stallTick is how often a run looks at its own silence: a quarter of the silence the
// project agreed to, and never less than a second nor more than a quarter of a minute.
// A mark of a run that stands is worth minutes at most, and a look that is rarer than
// the silence itself would find the run long after it went quiet.
func stallTick(after time.Duration) time.Duration {
	return min(max(after/4, time.Second), 15*time.Second)
}

// appendLine puts one line at the end of a file of a run through a handle of its own,
// which the machine writes at the end of the file whatever the other handle stands at:
// the run of an attempt holds its own files open and writes into them, and a line the
// watch of the run adds while it does is a line of the same file and not a second one.
func appendLine(path, line string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open %s to add a line: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	if _, err := io.WriteString(file, line+"\n"); err != nil {
		return fmt.Errorf("add a line to %s: %w", path, err)
	}
	return nil
}

// Standing is a run that stands, or a run that stood and is not standing any more: what
// `crewflow task check-stalled` answers, and what it leaves a record under the task
// about. A command of the schedule of an orchestrator is asked about the runs of a
// project every few minutes, and this is the whole of what it says: what stands, for
// how long, what it was doing, and whether the record under the task is one this turn
// of the watch has just left (docs/DESIGN.md §6).
type Standing struct {
	// Repo, Task, Run and Title are what the run is of, the way a person names a run:
	// the project, the number of the task, the run of it, and the one line the task was
	// given.
	Repo  string `json:"repo"`
	Task  int    `json:"task"`
	Run   string `json:"run"`
	Title string `json:"title"`
	// Stalled says whether the run is standing now. A run that was standing and is not
	// is in the answer as well, with the record that says so: the end of an episode of
	// silence is a fact about a task and not less of one than its beginning.
	Stalled bool `json:"stalled"`
	// StalledFor is how long the run has been standing, in seconds because a program
	// counts seconds. It is nothing for a run that is not standing any more: how long
	// that one stood is in the record that opened the episode, and the moment the run
	// showed a sign of life is the only end of it crewflow keeps.
	StalledFor float64 `json:"stalled_for"`
	// LastStep is what the run showed the last time it showed anything, and Reason what
	// it stands at where crewflow knows it (docs/DESIGN.md §7i).
	LastStep string `json:"last_step"`
	Reason   string `json:"reason,omitempty"`
	// Outcome is how the last attempt of the task came out, and it is what the record
	// says where a run that stood is over rather than working again.
	Outcome Kind `json:"outcome"`
	// Said says whether this turn of the watch is the one that left the record under the
	// task, and Problem says what stood in the way where it did not: a project whose
	// host cannot write under a task has the journal of the attempt as its record, and
	// the answer says that instead of pretending otherwise.
	Said    bool   `json:"said"`
	Problem string `json:"problem,omitempty"`
}

// Say is what leaves the record of a run under its task: the host of the project, and
// nothing at all for a project that has no host that can write there. It answers
// whether the record was left and what stood in the way where it was not.
type Say func(ctx context.Context, standing Standing) (said bool, problem string)

// NoRecord is the answer of a host that cannot write under a task: crewflow does not
// stand in for the tracker of a project, and a run that stands in such a project is
// reported to the person who asked and written about in the journal of the attempt and
// nowhere else (docs/DESIGN.md §6, §7g).
const NoRecord = "this project has no host that can leave a record under a task"

// CheckStalled is the runs of a project that stand, and the runs that stood and have
// gone on or ended, worked out of the state of the tasks of the project at the moment it
// is asked. It reads the state and nothing else — no tracker, no host, no network — and
// says through the callback what is to be written under a task, and writes down in the
// state of the task that it was (docs/DESIGN.md §6).
//
// One episode of silence is one record: a run that is standing and has been reported is
// in the answer with `said` false, and a run that has been reported and is no longer
// standing is in the answer once more with the record that says so and with nothing
// after it. A command of a schedule that ran every minute is therefore not a record
// every minute, and a person reading the task sees two lines and not two hundred.
func CheckStalled(ctx context.Context, home, repo string, env ListEnv, say Say) ([]Standing, error) {
	folder := filepath.Join(home, "state", repo)
	names, err := os.ReadDir(folder)
	if err != nil {
		if os.IsNotExist(err) {
			// A machine crewflow has run nothing in has no state of that project, and
			// nothing of it stands.
			return nil, nil
		}
		return nil, fmt.Errorf("read the states of the tasks in %s: %w", folder, err)
	}
	journals := newJournals(home, repo)
	var standings []Standing
	for _, name := range names {
		if name.IsDir() || !isStateOfATask(name.Name()) {
			continue
		}
		state, err := LoadState(filepath.Join(folder, name.Name()))
		if err != nil {
			// One state crewflow cannot read does not take the tasks beside it down
			// with it: what a schedule is to act on is what it could read, and the
			// file it could not read is the one a person has to open by hand.
			continue
		}
		if one, _ := checkOne(ctx, env, say, journals, repo, state); one != nil {
			standings = append(standings, *one)
		}
	}
	slices.SortFunc(standings, func(a, b Standing) int { return cmp.Compare(a.Task, b.Task) })
	return standings, nil
}

// checkOne is the one task of a project that stands, what is to be written about it
// under its own task, and the state of the task as it is to be kept. A task with no
// attempt in it is a task that was never run, and nothing of it stands.
func checkOne(ctx context.Context, env ListEnv, say Say, journals Journals, repo string, state State) (*Standing, State) {
	if len(state.Attempts) == 0 {
		return nil, state
	}
	last := state.Attempts[len(state.Attempts)-1]
	now := env.Now()
	silence := silenceOf(last, state.Profile, now)
	switch standing, reported := env.outcome(last) == Running && silence.standing(env.StallAfter), last.ReportedAt != nil; {
	case standing && !reported:
		// The silence has begun and nobody has said so under the task.
		one := recordOf(ctx, env, say, repo, state, silence, true)
		if one.Said {
			state = state.Reported(now)
			_ = SaveState(journals.StatePath(state.Number), state)
		}
		return &one, state
	case standing:
		// The silence is an episode crewflow has already written about: the answer
		// still says that the run stands, and the record is not written a second time.
		return &Standing{
			Repo: ownerAndRepo(repo), Task: state.Number, Run: runOf(state), Title: state.Title,
			Stalled: true, StalledFor: silence.For.Seconds(), LastStep: silence.LastStep,
			Reason: silence.Reason, Outcome: last.Outcome,
		}, state
	case reported:
		// The silence is over — the run is working again, or it is over — and the task
		// has to be told so once, because the record that opened the episode is under
		// it and a person who reads it has to learn where it went.
		one := recordOf(ctx, env, say, repo, state, silence, false)
		if one.Said {
			state = state.NoLongerReported()
			_ = SaveState(journals.StatePath(state.Number), state)
		}
		return &one, state
	default:
		return nil, state
	}
}

// recordOf is the answer about a run whose silence has begun or ended, with the record
// crewflow leaves under the task about it. A run of a project whose host cannot write
// under a task is reported and not written about, and the answer says so rather than
// letting a schedule believe that a record is there.
func recordOf(ctx context.Context, env ListEnv, say Say, repo string, state State, silence Stall, stalled bool) Standing {
	last := state.Attempts[len(state.Attempts)-1]
	one := Standing{
		Repo: ownerAndRepo(repo), Task: state.Number, Run: runOf(state), Title: state.Title,
		Stalled: stalled, StalledFor: silence.For.Seconds(), LastStep: silence.LastStep,
		Reason: silence.Reason, Outcome: env.outcome(last),
	}
	if say == nil {
		one.Problem = NoRecord
		return one
	}
	one.Said, one.Problem = say(ctx, one)
	return one
}

// runOf is the run the state of a task is of, as a person names it.
func runOf(state State) string {
	if len(state.Attempts) == 0 {
		return ""
	}
	last := state.Attempts[len(state.Attempts)-1]
	return strconv.Itoa(state.Number) + "-" + strconv.Itoa(last.Number)
}

// Silence is how long the run of a standing entry has been standing, as a length of time
// rather than as the seconds a program reads: the answer of `-json` holds the seconds,
// because a program counts seconds, and a person reads a time.
func (one Standing) Silence() time.Duration {
	return time.Duration(one.StalledFor * float64(time.Second))
}

// Record is the line crewflow leaves under the task of a run that stands, and the line
// it leaves when the run goes on again or is over: one for the beginning of an episode
// of silence and one for its end, and never a third for a minute in between. A person
// who reads the task is reading it to find out whether anybody is looking after the run,
// and this is the whole of what crewflow knows about that (docs/DESIGN.md §6).
func Record(one Standing) string {
	if one.Stalled {
		said := fmt.Sprintf("crewflow: the run of the task %d (%s) has shown nothing for %s, "+
			"the last step: %s", one.Task, one.Run, one.Silence(), one.LastStep)
		if one.Reason != "" {
			said += ", standing at: " + one.Reason
		}
		return said + ". It needs attention, and crewflow does not stop a run: " +
			"a person or an orchestrator decides what happens to it (docs/DESIGN.md §6)."
	}
	if one.Outcome == Running {
		return fmt.Sprintf("crewflow: the run of the task %d (%s) is working again, and the silence is over",
			one.Task, one.Run)
	}
	return fmt.Sprintf("crewflow: the run of the task %d (%s) is over: %s, and the silence it stood in is over with it",
		one.Task, one.Run, one.Outcome)
}
