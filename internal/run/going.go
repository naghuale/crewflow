package run

import (
	"fmt"
	"os"
	"time"
)

// RunGoing is the word a run of a task is refused with while the run of that task is going.
// It is a word of §7 and not one of the three words of §7c: a task does not run beside itself,
// no record of a pair admits it and none of the three refusals of a pair can speak for it
// (docs.DESIGN.md §7c, §7e).
const RunGoing = "task-run-going"

// ErrGoing is the refusal of a run of a task whose run is going: one task is run by one run at
// a time, because the state of the task and the two files of an attempt belong to that attempt
// and to nobody else.
//
// A second run of the same task is a run that divides what belongs to one run: it takes a number
// of an attempt of its own beside the one that is going, and when the first run ends it writes
// its outcome, its session and its reason into "the last attempt", which by then is the attempt
// of the second run — while the attempt of the first one stands in every list of runs as one
// that is going (docs.DESIGN.md §7, D-068 RECHECK-FINDING-4).
type ErrGoing struct {
	// Task is the task that is not to be started again, Attempt is the attempt whose run is
	// going now, Started is when that attempt was started and Journal where what it writes
	// goes: a person who was told only that a run was refused has to find the run that is
	// going, and this is the line that tells them where.
	Task    int
	Attempt int
	Started time.Time
	Journal string
}

// Error names the task, the word of the refusal, the attempt that is going and the file it
// writes: a refusal a person cannot act on is a refusal they will ask about again
// (docs.DESIGN.md §7e, §7i).
func (e *ErrGoing) Error() string {
	return fmt.Sprintf("task %d is not run again: %s — the attempt %d of it is going since %s and writes to %s: "+
		"одна задача идёт одним прогоном; подождите, пока он кончится, и прочтите его журнал (docs.DESIGN.md §7)",
		e.Task, RunGoing, e.Attempt, e.Started.Format("2006-01-02 15:04"), e.Journal)
}

// goingAttempt is the attempt of a task whose run is going now, and whether there is one.
// It is the last attempt of the task that is looked at: an attempt is `running` from the
// moment it is added until the run that began it writes how it ended, and a task whose last
// attempt has ended has no run going whatever the attempts before it hold
// (docs/DESIGN.md §7).
func goingAttempt(state State, alive Liveness) (Attempt, bool) {
	if len(state.Attempts) == 0 {
		return Attempt{}, false
	}
	last := state.Attempts[len(state.Attempts)-1]
	if !goingRun(last, alive) {
		return Attempt{}, false
	}
	return last, true
}

// goingRun says whether the run of this attempt is going: the state of the task says the
// attempt is running, and where it names the process of a run of crewflow, the machine has
// the last word about it — crewflow lives exactly as long as its run and stops the executor
// with itself, so a run whose crewflow is gone is a run that is over whatever its executor is
// doing (docs.DESIGN.md §6, §7).
//
// An attempt of a state written before crewflow kept numbers of processes names none, and such
// a run goes on counting as a run that is going: a machine that is not asked cannot tell that a
// process is gone, and a run that cannot be asked about is not a run to start another one over
// (docs.DESIGN.md §7, §7c).
func goingRun(attempt Attempt, alive Liveness) bool {
	if attempt.Outcome != Running {
		return false
	}
	if process, named := attempt.Process(); named && alive != nil {
		return alive(process)
	}
	return true
}

// goingRefusal is the refusal of a run of a task whose run is going, and it is the same
// refusal whoever asked for it and whichever of the two places of it came to the question:
// before anything of the run is created, and under the lock of the task where the number of
// the attempt is taken (docs.DESIGN.md §7).
func goingRefusal(number int, attempt Attempt) *ErrGoing {
	return &ErrGoing{
		Task:    number,
		Attempt: attempt.Number,
		Started: attempt.StartedAt,
		Journal: attempt.Journal,
	}
}

// alone is the question a run of a task asks before anything of it is created: is the run of
// this task going now? It is asked before the worktree of the task and before the key of the
// App, so that a task whose run is going is told so without anything being made for it and
// without a window of the system opening (§7i).
//
// The answer here is a question and not the gate: the state of the task is read outside the
// lock of the task, and another run of the same task may begin the moment after this reading.
// The gate is the reservation of the attempt, which takes the number under that lock and
// refuses there (§7, D-068 RECHECK-FINDING-4).
func (r *runner) alone() error {
	state, err := LoadState(r.journals.StatePath(r.task.Number))
	switch {
	case os.IsNotExist(err):
		// A task that was never run here has no run going.
		return nil
	case err != nil:
		return err
	}
	if attempt, is := goingAttempt(state, r.env.Alive); is {
		return goingRefusal(r.task.Number, attempt)
	}
	return nil
}
