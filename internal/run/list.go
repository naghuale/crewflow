package run

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/naghuale/crewflow/internal/proc"
)

// ListLimit is how many runs `crewflow task list` shows: a person asks what is going
// on and what was going on, and twenty tasks is more of that than anybody reads.
// `crewflow task list -all` shows every run of every project of the machine, and is
// not short.
const ListLimit = 20

// Liveness is the question a list asks about a run: is the process the run was in
// still that process. It is the machine, asked.
type Liveness func(process proc.Process) bool

// ListEnv is what a list of runs needs from the machine: the clock, to say how long a
// run that is going has been going; the question whether it is going at all; and the
// time limit of a run of the project, which is what says that a run crewflow kept no
// process of is over.
type ListEnv struct {
	// Now is the clock of the machine.
	Now func() time.Time
	// Running asks whether the process of a run is still there.
	Running Liveness
	// Timeout is how long a run of the project may take, and zero when nothing says:
	// a list of every project on the machine is asked from any folder, and a folder
	// of no project says nothing about the time limit of the runs beside it. What a
	// list does not know, it does not end a run with.
	Timeout time.Duration
}

// Runs is what crewflow knows of the runs of one project: one entry per task that was
// run, and what it could not read. A list of the whole machine is a Runs of no one
// project, and every entry of it says which project it is of.
type Runs struct {
	// Entries are the tasks that were run, the ones that want a person on top.
	Entries []Entry
	// Unreadable are the state files that say nothing about a run, and Left is how
	// many entries were left out of a list that was not asked to be a long one.
	Unreadable []string
	Left       int
	// Repo is the project this list is of, as the name its state is kept under, and
	// Total how many of its tasks were run whether all of them fit into the list or
	// not: the header of a list says how many tasks the project has, and the list
	// shows the last of them. Both are of no one project in a list of the whole
	// machine.
	Repo  string
	Total int
	// Branch is the branch the tasks of the project are counted from, which the
	// header of a list says: a list of tasks is a list of a project at a moment of
	// it, and a person who reads one wants to know which branch the work of it is on
	// its way to. It is of no one project in a list of the whole machine.
	Branch string
	// Elsewhere is what the other projects of the machine are doing, said under the
	// table of a list of one project: a person in the folder of a project cannot see
	// what runs beside it, and a run of another project of the machine is as much of
	// this machine as the one of their own (docs/DESIGN.md §6).
	Elsewhere []Project
}

// Entry is one task in the list: what it is, how many tries it took, how the last of
// them came out — or that it is going on right now — who ran it, whose name it went
// under, and where its change request is.
type Entry struct {
	// Repo is the project the run was of, as the name its state is kept under, and a
	// list shows it as the host names the project, "owner/name" (ownerAndRepo). Every
	// entry says it, so that a list of the whole machine needs no column of its own to
	// be read by a program, and a list of one project repeats it in every row.
	Repo string `json:"repo"`
	// Task and Title are the task the run was of, as the state of it says: a list
	// reads no tracker, so that it is the same answer with the network down.
	Task  int    `json:"task"`
	Title string `json:"title"`
	// Attempts is how many times the executor was started on the task, and Attempt is
	// the number of the last of them, which with the number of the task is the run a
	// person goes and looks at: the name of its journal and what `task watch <N>
	// -attempt K` watches.
	Attempts int `json:"attempts"`
	Attempt  int `json:"-"`
	// Outcome is how the last attempt came out. It is running while the process of
	// the run is still there, interrupted once it is not, and the outcome of the run
	// itself otherwise (docs/DESIGN.md §7).
	Outcome Kind `json:"outcome"`
	// StartedAt is when the last attempt was started, and EndedAt is when it stopped,
	// which there is none of while it is going.
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	// Duration is how long the last attempt took, or has been going for. It is zero
	// when nothing kept it, and Length says when that is.
	Duration time.Duration `json:"-"`
	// Executor is the agent that ran the last try — "opencode", "codex" — and not
	// the account it went under: that is what Identity says, and a list of runs
	// tells both apart because a person has to know which agent wrote a run before
	// they know whose run it was (docs/DESIGN.md §7b, §7i).
	Executor string `json:"executor"`
	// Change is the change request a run of the task opened, when it opened one, so
	// that a list of the runs of a project points at the work without asking the
	// host anything (docs/DESIGN.md §7).
	Change *Change `json:"change,omitempty"`
	// Identity is whose name the executor of the last attempt worked under, so that
	// a list of the runs of a project says the mode of each of them and a person does
	// not have to open a state file to learn that a run was in the mode of the owner
	// (docs/DESIGN.md §7i).
	Identity Identity `json:"identity"`
}

// Project is what a project of the machine is doing: its runs by what the last try of
// each of them came out as, which is what the line under a list of one project says
// about the projects beside it.
type Project struct {
	// Repo is the project as the name its state is kept under, and a list shows it as
	// the host names the project, "owner/name".
	Repo string
	// Outcomes is how many of the tasks of the project came out as what.
	Outcomes map[Kind]int
}

// Run is the run an entry is of, as a person names it: the number of the task and of
// the try beside each other, the way the journal of the attempt is named and the way
// `task watch <N> -attempt K` is called.
func (e Entry) Run() string {
	return strconv.Itoa(e.Task) + "-" + strconv.Itoa(e.Attempt)
}

// Length is how long the last attempt of the task took, and whether that is known at
// all. A run whose process is gone was not seen to stop: crewflow was not there to see
// it end, and how long it went on afterwards is in nothing it kept.
func (e Entry) Length() (time.Duration, bool) {
	if e.Outcome != Running && e.EndedAt == nil {
		return 0, false
	}
	return e.Duration, true
}

// MarshalJSON is the shape the orchestrator reads a list in. The length of a run is in
// seconds, because a program counts seconds and does not read "1h 05m", and it is
// nothing at all when nothing kept it.
func (e Entry) MarshalJSON() ([]byte, error) {
	length, known := e.Length()
	var seconds *float64
	if known {
		whole := math.Round(length.Seconds())
		seconds = &whole
	}
	answer := struct {
		Repo            string     `json:"repo"`
		Task            int        `json:"task"`
		Title           string     `json:"title"`
		Attempts        int        `json:"attempts"`
		Run             string     `json:"run"`
		Outcome         Kind       `json:"outcome"`
		StartedAt       time.Time  `json:"started_at"`
		EndedAt         *time.Time `json:"ended_at,omitempty"`
		DurationSeconds *float64   `json:"duration_seconds"`
		Executor        string     `json:"executor"`
		Change          *Change    `json:"change,omitempty"`
		Identity        Identity   `json:"identity"`
	}{
		Repo:            ownerAndRepo(e.Repo),
		Task:            e.Task,
		Title:           e.Title,
		Attempts:        e.Attempts,
		Run:             e.Run(),
		Outcome:         e.Outcome,
		StartedAt:       e.StartedAt,
		EndedAt:         e.EndedAt,
		DurationSeconds: seconds,
		Executor:        e.Executor,
		Change:          e.Change,
		Identity:        e.Identity,
	}
	data, err := json.Marshal(answer)
	if err != nil {
		return nil, fmt.Errorf("the run of the task %d: %w", e.Task, err)
	}
	return data, nil
}

// List is the runs of one project: the last twenty of them unless -all was asked for,
// the ones that want a person on top, and under the table what the other projects of
// the machine are doing. It reads the state crewflow kept and nothing else — no
// tracker, no host, no network — and creates nothing: a list of runs is a question,
// and a question is not a run (docs/DESIGN.md §6, §7).
//
// The project is named the way its state is kept, the name of the project with the slash
// of "owner/name" a dash, because that is how a list finds the folder of it. A list shows
// it as "owner/name" (ownerAndRepo).
func List(home, repo string, env ListEnv) (Runs, error) {
	machine, err := EveryProject(home, env)
	if err != nil {
		return Runs{}, err
	}
	return machine.Of(repo), nil
}

// EveryProject is the runs of every project crewflow has run anything of on this
// machine: all of them, in one order, with the project of each of them said in it. It
// is what `crewflow task list -all` shows, and it is asked from any folder: the state
// of the runs of every project is under one root whatever project the person stands
// in, and a folder of no project at all is a folder a list of the whole machine is
// asked in as well as any other.
func EveryProject(home string, env ListEnv) (Runs, error) {
	folder := filepath.Join(home, "state")
	projects, err := os.ReadDir(folder)
	if err != nil {
		// A machine crewflow has run nothing on has no folder of states yet, and
		// that is nothing wrong with the machine: there is no run to show, and the
		// person who asked is told so.
		if os.IsNotExist(err) {
			return Runs{}, nil
		}
		return Runs{}, fmt.Errorf("read the states of the tasks in %s: %w", folder, err)
	}
	runs := Runs{}
	for _, project := range projects {
		if !project.IsDir() {
			continue
		}
		if err := runs.read(env, project.Name(), filepath.Join(folder, project.Name())); err != nil {
			return Runs{}, err
		}
	}
	slices.SortStableFunc(runs.Entries, byRecency)
	return runs, nil
}

// read is every run of one project of the machine: one entry per task of it that was
// run, and the state files that say nothing about a run.
func (r *Runs) read(env ListEnv, repo, folder string) error {
	names, err := os.ReadDir(folder)
	if err != nil {
		return fmt.Errorf("read the state of the tasks in %s: %w", folder, err)
	}
	for _, name := range names {
		if !isStateOfATask(name.Name()) {
			continue
		}
		path := filepath.Join(folder, name.Name())
		// One file crewflow cannot read does not take the tasks of the other ones
		// down with it: what did happen is what a person came for, and the file that
		// could not be read is named so that they can look at it.
		state, err := LoadState(path)
		if err == nil {
			if entry, ok := env.entryOf(state, repo); ok {
				r.Entries = append(r.Entries, entry)
				continue
			}
		}
		r.Unreadable = append(r.Unreadable, path)
	}
	return nil
}

// Of is the list of one project out of the runs of the whole machine: the runs of the
// other projects are left out of it, the last twenty of it are kept, and what the
// others are doing is said under the table. A state crewflow cannot read belongs to
// the project it is the state of and not to the one of the folder.
func (r Runs) Of(repo string) Runs {
	of := Runs{Repo: repo}
	for _, entry := range r.Entries {
		if entry.Repo == repo {
			of.Entries = append(of.Entries, entry)
		}
	}
	of.Total = len(of.Entries)
	of.shown()
	for _, other := range r.projects() {
		if other.Repo != repo {
			of.Elsewhere = append(of.Elsewhere, other)
		}
	}
	for _, path := range r.Unreadable {
		if filepath.Base(filepath.Dir(path)) == repo {
			of.Unreadable = append(of.Unreadable, path)
		}
	}
	return of
}

// projects is every project of the machine, with what the last try of each of its
// tasks came out as, by the name its state is kept under and in the order of those
// names: a person reads the line of a project beside the order of the projects.
func (r Runs) projects() []Project {
	counts := map[string]map[Kind]int{}
	for _, entry := range r.Entries {
		if counts[entry.Repo] == nil {
			counts[entry.Repo] = map[Kind]int{}
		}
		counts[entry.Repo][entry.Outcome]++
	}
	projects := make([]Project, 0, len(counts))
	for repo, outcomes := range counts {
		projects = append(projects, Project{Repo: repo, Outcomes: outcomes})
	}
	slices.SortFunc(projects, func(a, b Project) int { return strings.Compare(a.Repo, b.Repo) })
	return projects
}

// shown is the list a person asked for: the last twenty runs of the project, and the
// number of the ones that did not fit into it.
func (r *Runs) shown() {
	if len(r.Entries) <= ListLimit {
		return
	}
	r.Left = len(r.Entries) - ListLimit
	r.Entries = r.Entries[:ListLimit]
}

// entry is the run of the task with that number in the list, and whether the list holds
// one.
func (r Runs) entry(number int) (Entry, bool) {
	for _, entry := range r.Entries {
		if entry.Task == number {
			return entry, true
		}
	}
	return Entry{}, false
}

// isStateOfATask is whether the name is the state of a task. The folder of the states
// holds nothing else, and what an interrupted write left in it is not a task.
func isStateOfATask(name string) bool {
	number, ok := strings.CutSuffix(name, ".json")
	if !ok {
		return false
	}
	task, err := strconv.Atoi(number)
	return err == nil && task > 0
}

// entryOf is what crewflow makes of the state of one task: the tries it took, and the
// last of them with how long it has been going, the agent that ran it and whose name
// it went under. A state with no attempt in it is a state of no run, and there is
// nothing to show of the task.
func (e ListEnv) entryOf(state State, repo string) (Entry, bool) {
	if len(state.Attempts) == 0 {
		return Entry{}, false
	}
	last := state.Attempts[len(state.Attempts)-1]
	entry := Entry{
		Repo:     repo,
		Task:     state.Number,
		Title:    state.Title,
		Attempts: len(state.Attempts),
		Attempt:  last.Number,
		Outcome:  e.outcome(last),
		// The state of a task keeps the profile of the executor of the run, and an
		// attempt of a state of before crewflow kept it names no agent at all. A
		// list shows a dash where there is nothing rather than a name that was
		// never written down.
		Executor:  state.Profile,
		StartedAt: last.StartedAt,
		Identity:  last.Identity,
		Change:    state.Change,
	}
	if !last.EndedAt.IsZero() {
		ended := last.EndedAt
		entry.EndedAt = &ended
	}
	switch {
	case entry.Outcome == Running:
		// A run that is going is as long as it has been going so far, and its end is
		// a moment that has not come yet.
		entry.Duration = e.Now().Sub(last.StartedAt)
	case !last.EndedAt.IsZero():
		entry.Duration = last.EndedAt.Sub(last.StartedAt)
	}
	return entry, true
}

// outcome is how the last try of a task came out, as a list of runs says it. An attempt
// that says "running" is only running while the process of the run is still there: a
// window that was closed, a machine that was rebooted and a run killed outright are one
// and the same thing to the person who comes back to it, and the state of the task
// cannot tell them apart (docs/DESIGN.md §7).
func (e ListEnv) outcome(last Attempt) Kind {
	if last.Outcome != Running {
		return last.Outcome
	}
	process, named := last.Process()
	switch {
	case named:
		if e.Running(process) {
			return Running
		}
		// The process of the run is gone, and no other process has its number: a run
		// that is over and was not over when it was written down.
		return Interrupted
	case e.Timeout > 0 && e.Now().Sub(last.StartedAt) > e.Timeout:
		// A state of before crewflow kept the number of the process of a run: there
		// is nothing to ask, and a run that has been going for longer than any run of
		// this project may go on is not going on.
		return Interrupted
	default:
		// A run of a moment ago that names no process. It may be going and crewflow
		// has no way of telling, so it says that instead of saying that it is.
		return MaybeRunning
	}
}

// byRecency is the order a person reads the runs of a project in: what wants a person
// today on top — a run that is going, a run that was refused, a run that was cut short
// — and the rest from the last try of a task to the first. A run of a week ago that
// wants somebody is above a run of an hour ago that came out well, because the first
// one is waited for and the second one is read (docs/DESIGN.md §6, §7).
func byRecency(a, b Entry) int {
	if aWants, bWants := wantsAttention(a.Outcome), wantsAttention(b.Outcome); aWants != bWants {
		if aWants {
			return -1
		}
		return 1
	}
	return b.StartedAt.Compare(a.StartedAt)
}

// wantsAttention is whether the outcome of a run is one a person has to do something
// about: it is going, it reached for a secret, it was refused a permission, it stopped by
// itself, it ran out of time, the executor of it is gone, or nobody knows whether it is
// going at all. A run that came out of it is not one, however long ago it was.
func wantsAttention(outcome Kind) bool {
	switch outcome {
	case Running, MaybeRunning, Interrupted, TimedOut, BlockedSecret, BlockedPermission, Blocked,
		ExecutorFailed:
		return true
	default:
		return false
	}
}

// ownerAndRepo is the name of a project as a person writes it, "owner/name", from the
// name its states are kept under. crewflow replaces the slash of a name with a dash,
// because a path may hold no slash, and the first dash of what is left is where the slash
// was: a name of a project on a host is an owner and a name, and the owner comes first.
// An owner whose own name has a dash in it is read wrong here, and a list of it says a
// name it does not know rather than none: nothing else could be shown, and the state of
// the task beside it is the truth about the run.
func ownerAndRepo(name string) string {
	owner, repo, of := strings.Cut(name, "-")
	if !of {
		return name
	}
	return owner + "/" + repo
}
