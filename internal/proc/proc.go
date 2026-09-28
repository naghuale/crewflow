// Package proc asks the machine about a process: whether it is there, and since
// when. A run of a task writes the process it happens in into the state of the task,
// and `crewflow task list` asks about that process later, because a state says
// "running" until something says otherwise, and after a reboot of the machine
// nothing does (docs/DESIGN.md §7).
package proc

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// leeway is how far the moment a process started may be from the one crewflow wrote
// down and still be the same process. ps answers to the second, and that is all the
// doubt there is: a whole run of somebody else is a whole run of somebody else.
const leeway = time.Second

// lstart is what ps writes for the start time of a process, in the C locale: the day
// of the week, the month, the day, the time and the year.
const lstart = "Mon Jan _2 15:04:05 2006"

// Process is a process of the machine: its number and when it started. Both are
// written into the state of a task, and the second is what makes the first mean
// anything: the numbers of processes start over at every boot, so the number of a
// run of yesterday belongs today to something else entirely.
type Process struct {
	// Pid is the number of the process.
	Pid int `json:"pid"`
	// StartedAt is when the process started, as the machine says it.
	StartedAt time.Time `json:"process_started_at"`
}

// Env is what the questions are asked of. It is passed in whole, so that a test asks
// a machine of its own and no test depends on what is installed where the test runs.
type Env struct {
	// Ask runs a program of PATH and returns what it wrote.
	Ask func(name string, args []string) (stdout string, err error)
}

// System is the machine this process runs on, and it is ps that answers: it is on
// every machine a project is worked on, and it is the one that says when a process
// started.
func System() Env {
	return Env{Ask: ask}
}

// Self is the process this code runs in: the one a run of a task happens in, and the
// one whose window a person closes when they stop a run. Whether the machine could say
// when it started is returned with it, because a run that cannot be asked is a run
// whose state is written as it was written before.
func (e Env) Self() (Process, bool) {
	pid := os.Getpid()
	started, there := e.StartedAt(pid)
	if !there {
		return Process{}, false
	}
	return Process{Pid: pid, StartedAt: started}, true
}

// StartedAt asks the machine when the process with that number started, and whether
// it is there at all. A process that is not there has no start time, and a caller
// has to be able to tell those apart: "the run is over" and "the machine would not
// say" are both a run that is not going on, and only the second one is a question
// about the machine.
func (e Env) StartedAt(pid int) (time.Time, bool) {
	answer, err := e.Ask("ps", []string{"-o", "lstart=", "-p", strconv.Itoa(pid)})
	if err != nil {
		return time.Time{}, false
	}
	started, err := time.ParseInLocation(lstart, strings.TrimSpace(answer), time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return started, true
}

// Alive says whether the process is still there and is the one that started at the
// moment crewflow wrote down. A number on its own is not a process: a machine that
// was rebooted gives that number to something else, and a list of runs that called
// that a run would show work that stopped a week ago as work in progress
// (docs/DESIGN.md §7).
func (e Env) Alive(process Process) bool {
	if process.Pid <= 0 {
		return false
	}
	started, there := e.StartedAt(process.Pid)
	if !there {
		return false
	}
	return started.Sub(process.StartedAt) <= leeway && process.StartedAt.Sub(started) <= leeway
}

// ask runs a program and returns what it wrote to its standard output. The C locale
// is asked for: the day and the month in the answer of ps are read as the English
// words they are written in, and a machine in another language would write its own.
func ask(name string, args []string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return stdout.String(), nil
}
