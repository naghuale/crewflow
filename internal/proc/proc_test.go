package proc

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestStartedAtOfALiveProcess: the number of a process says nothing about it — the
// numbers of processes start over at every boot — and a run of a task is only told
// apart from another one by when its process started (docs/DESIGN.md §7).
func TestStartedAtOfALiveProcess(t *testing.T) {
	sleeper := startSleeper(t)

	started, there := System().StartedAt(sleeper.pid)

	if !there {
		t.Fatalf("the machine does not know of the process %d, which is going", sleeper.pid)
	}
	if delay := time.Since(started); delay < 0 || delay > time.Minute {
		t.Errorf("the process %d started at %s, which is %s ago, want a moment ago", sleeper.pid, started, delay)
	}
}

// TestStartedAtOfAProcessThatIsOver: a run crewflow was killed in the middle of
// leaves the number of a process that is not there any more, and a list of runs has
// to be able to tell that from a process it cannot read.
func TestStartedAtOfAProcessThatIsOver(t *testing.T) {
	sleeper := sleeperOf(t, "0.05")
	if err := sleeper.cmd.Wait(); err != nil {
		t.Fatalf("wait for the process: %v", err)
	}

	started, there := System().StartedAt(sleeper.pid)

	if there {
		t.Errorf("the machine knows of the process %d as started at %s, want none: it is over", sleeper.pid, started)
	}
}

// TestAlive walks what a number of a process means on a machine: a run that is going,
// a run whose window was closed, and a number that has been given to another process
// since. A number on its own is not a run (docs/DESIGN.md §7).
func TestAlive(t *testing.T) {
	sleeper := startSleeper(t)
	started, there := System().StartedAt(sleeper.pid)
	if !there {
		t.Fatalf("the machine does not know of the process %d, which is going", sleeper.pid)
	}
	over := sleeperOf(t, "0.05")
	if err := over.cmd.Wait(); err != nil {
		t.Fatalf("wait for the process: %v", err)
	}

	cases := []struct {
		name    string
		process Process
		want    bool
	}{
		{
			name:    "the process of the run is going",
			process: Process{Pid: sleeper.pid, StartedAt: started},
			want:    true,
		},
		{
			name:    "the process of the run is over",
			process: Process{Pid: over.pid, StartedAt: started},
			want:    false,
		},
		{
			name:    "another process holds the number the run had",
			process: Process{Pid: sleeper.pid, StartedAt: started.Add(-time.Minute)},
			want:    false,
		},
		{
			name:    "a second of the machine is not a second of doubt",
			process: Process{Pid: sleeper.pid, StartedAt: started.Add(-time.Second)},
			want:    true,
		},
		{
			name:    "a whole run of somebody else is somebody else",
			process: Process{Pid: sleeper.pid, StartedAt: started.Add(time.Hour)},
			want:    false,
		},
		{
			name:    "no process at all",
			process: Process{},
			want:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := System().Alive(tc.process); got != tc.want {
				t.Errorf("Alive(%+v) = %t, want %t", tc.process, got, tc.want)
			}
		})
	}
}

// TestStartedAtReadsWhatTheMachineSays walks the answers of ps, because a state of a
// task is only as good as the reading of the machine behind it: a number with no
// answer behind it is a run that is over, and an answer in another language is a
// process crewflow cannot find.
func TestStartedAtReadsWhatTheMachineSays(t *testing.T) {
	cases := []struct {
		name   string
		answer string
		err    error
		want   time.Time
	}{
		{
			name:   "what ps writes on macOS and on Linux",
			answer: "Mon Sep 28 10:00:00 2026   \n",
			want:   time.Date(2026, time.September, 28, 10, 0, 0, 0, time.Local),
		},
		{
			name:   "a day of one digit is padded by ps",
			answer: "Fri Mar  6 09:05:03 2026   \n",
			want:   time.Date(2026, time.March, 6, 9, 5, 3, 0, time.Local),
		},
		{
			name:   "the machine said nothing",
			answer: "  \n",
		},
		{
			name:   "the machine said it in another language",
			answer: "пн сент 28 10:00:00 2026\n",
		},
		{
			name: "ps is not there",
			err:  errors.New("ps: executable file not found in $PATH"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := Env{Ask: func(name string, args []string) (string, error) {
				if name != "ps" {
					t.Errorf("asked %q, want ps: it is what says when a process started", name)
				}
				if want := "1234"; args[len(args)-1] != want {
					t.Errorf("asked ps %v, want the number of the process last", args)
				}
				return tc.answer, tc.err
			}}

			started, there := env.StartedAt(1234)

			if there != !tc.want.IsZero() {
				t.Fatalf("StartedAt = %s, %t, want %t", started, there, !tc.want.IsZero())
			}
			if there && !started.Equal(tc.want) {
				t.Errorf("the process started at %s, want %s", started, tc.want)
			}
			if there != env.Alive(Process{Pid: 1234, StartedAt: started}) {
				t.Errorf("Alive of the process just read = %t, want %t", !there, there)
			}
		})
	}
}

// TestAliveOfAMachineThatCannotAnswer: a machine without ps cannot tell a run that is
// going from one that is over, and says so instead of guessing.
func TestAliveOfAMachineThatCannotAnswer(t *testing.T) {
	env := Env{Ask: func(string, []string) (string, error) {
		return "", errors.New("ps: no such file or directory")
	}}

	if env.Alive(Process{Pid: 4242, StartedAt: time.Now()}) {
		t.Error("Alive of a process on a machine that cannot answer is true, want false")
	}
}

// sleeper is a program of a test that is going on while the test looks at it.
type sleeper struct {
	cmd *exec.Cmd
	pid int
}

// startSleeper starts a program that is going on for a minute, and stops it when the
// test is over: a real process of the machine is the only way to ask a real question
// about a real one.
func startSleeper(t *testing.T) sleeper {
	t.Helper()
	return sleeperOf(t, "60")
}

// sleeperOf starts a program that is going on for the given seconds, whether the test
// is over long before that or not.
func sleeperOf(t *testing.T, seconds string) sleeper {
	t.Helper()
	cmd := exec.Command("sleep", seconds)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start a program to look at: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return sleeper{cmd: cmd, pid: cmd.Process.Pid}
}

// TestSelf is the process a run of a task happens in: this one, with the moment it was
// started. It is what goes into the state of the task, so that a list of runs can ask
// about it later (docs/DESIGN.md §7).
func TestSelf(t *testing.T) {
	process, there := System().Self()

	if !there {
		t.Fatal("the machine does not know of the process this test runs in, want it")
	}
	if process.Pid != os.Getpid() {
		t.Errorf("the process = %d, want the process of this test, %d", process.Pid, os.Getpid())
	}
	if !System().Alive(process) {
		t.Errorf("the machine does not say that the process %+v of this test is going", process)
	}
	if _, there := (Env{Ask: func(string, []string) (string, error) {
		return "", errors.New("ps: no such file or directory")
	}}).Self(); there {
		t.Error("a machine without ps knows of a process, want none")
	}
}

// TestAskIsAboutThisProcess: what crewflow writes into the state of a task is the
// number of the process it runs in, and the machine is asked about that number and
// nothing else.
func TestAskIsAboutThisProcess(t *testing.T) {
	asked := 0
	env := Env{Ask: func(name string, args []string) (string, error) {
		asked++
		if want := "-p " + strconv.Itoa(os.Getpid()); !strings.Contains(strings.Join(args, " "), want) {
			t.Errorf("asked %s %v, want it asked about the process of this run", name, args)
		}
		return "Mon Sep 28 10:00:00 2026\n", nil
	}}

	if _, there := env.StartedAt(os.Getpid()); !there {
		t.Error("the machine did not answer about this very process, want the moment it started")
	}
	if asked != 1 {
		t.Errorf("the machine was asked %d times, want once", asked)
	}
}
