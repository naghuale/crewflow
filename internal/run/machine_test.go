package run

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain points the home directory of the machine at a folder of the run of the
// tests, for every test of the package. A run resolves "~" to the home of the person
// it runs on, and a test of it must not write there: what a test leaves in the home
// of a person outlives the test, and a worktree or a journal of another project in
// it is work a later run of a task would walk into. A test that wants a home of its
// own takes one; this is the net under all of them.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "crewflow-test-home-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "run tests: make a home of their own: %v\n", err)
		os.Exit(1)
	}
	for _, key := range []string{"HOME", "USERPROFILE"} {
		if err := os.Setenv(key, home); err != nil {
			fmt.Fprintf(os.Stderr, "run tests: point %s at %s: %v\n", key, home, err)
			os.Exit(1)
		}
	}
	code := m.Run()
	if err := os.RemoveAll(home); err != nil {
		fmt.Fprintf(os.Stderr, "run tests: take %s away: %v\n", home, err)
	}
	os.Exit(code)
}

// machine is the machine a run happens on: the folders that are on it, what every
// command answers, and everything that was started. It stands in for the machine of
// a person, so that no test of a run makes a worktree in the folder of the project
// it runs in, and none of them depends on what is installed where the test runs.
type machine struct {
	// home is the root of what crewflow keeps of its own, and worktrees is the root
	// the worktrees of the project are made under. Both are folders of the test.
	home      string
	worktrees string
	// repo is the repository of the project. It is a folder and nothing else: a
	// test of the flow of a run does not need a git at all, and the one that does
	// makes a repository of its own (docs/DESIGN.md §7).
	repo string
	// answers is what a command writes and the code it exits with.
	answers map[string]answer
	// ran are the commands that were started, in order.
	ran []started
	// clock is the time of the machine, which moves on with every question asked of
	// it, so that a report says when a thing happened and a test does not wait for
	// it to happen.
	clock time.Time
}

// answer is what a command of the machine does when it runs.
type answer struct {
	stdout string
	stderr string
	code   int
	// hangs says that the program waits for the end of the run and is killed with
	// it, which is what an agent that never answers looks like from the outside.
	hangs bool
}

// started is one command that was run: the program, its arguments and the folder it
// was run in.
type started struct {
	program string
	args    []string
	dir     string
}

// newMachine returns a machine with a repository, a root of worktrees and a home of
// its own, and with git saying nothing at all unless a test tells it to.
func newMachine(t *testing.T) *machine {
	t.Helper()
	m := &machine{
		home:      t.TempDir(),
		worktrees: t.TempDir(),
		repo:      t.TempDir(),
		answers: map[string]answer{
			"git fetch":            {},
			"git worktree add":     {},
			"git diff --name-only": {},
		},
		clock: time.Date(2026, time.September, 28, 10, 0, 0, 0, time.UTC),
	}
	m.has(m.repo)
	return m
}

// has puts folders on the machine, as a test of a run needs: a worktree that is
// already there, or nothing at all where a run is to make one. The folders are
// real, because a run asks the machine whether a worktree is there and not this
// test.
func (m *machine) has(paths ...string) *machine {
	for _, path := range paths {
		if err := os.MkdirAll(path, 0o700); err != nil {
			panic(fmt.Sprintf("make %s: %v", path, err))
		}
	}
	return m
}

// env is the machine a run of a test happens on.
func (m *machine) env() Env {
	return Env{Home: m.home, Command: m.exec, Now: m.now}
}

// now is the clock of the machine. Every question moves it a minute on, so that the
// start and the end of a run are two moments and not one.
func (m *machine) now() time.Time {
	m.clock = m.clock.Add(time.Minute)
	return m.clock
}

// exec runs a command the way the machine answers it, and leaves behind what the
// command did to the machine.
func (m *machine) exec(ctx context.Context, name string, args []string, dir string) ([]byte, []byte, int, error) {
	program := filepath.Base(name)
	m.ran = append(m.ran, started{program: program, args: args, dir: dir})
	answer, ok := m.answerOf(program, args)
	if !ok {
		return nil, nil, 127, fmt.Errorf("exec: %q: no answer in the machine", program)
	}
	if answer.hangs {
		// A program that waits and is killed by the end of the run leaves a code
		// that is not a number a program exits with, which is what a killed one
		// leaves as well.
		<-ctx.Done()
		return nil, nil, -1, nil
	}
	if answer.code == 0 {
		m.made(program, args)
	}
	return []byte(answer.stdout), []byte(answer.stderr), answer.code, nil
}

// answerOf is what the machine says to a command: the entry of the whole command
// line, or of the longest beginning of it that the machine knows, or of the program
// alone. A test may therefore answer a command that takes a path of its own without
// writing the path out.
func (m *machine) answerOf(program string, args []string) (answer, bool) {
	line := program
	for _, arg := range args {
		line += " " + arg
		if answer, ok := m.answers[line]; ok {
			return answer, true
		}
	}
	answer, ok := m.answers[program]
	return answer, ok
}

// made is what a command left on the machine. Only git makes worktrees here, and it
// is the one command a test of a second run of a task meets on the way: the folder
// of the worktree is there afterwards, or a continuation has nothing to go on in.
func (m *machine) made(program string, args []string) {
	if program != "git" || len(args) < 5 || args[0] != "worktree" || args[1] != "add" {
		return
	}
	m.has(args[4])
}

// lines are the commands that were run, in order, as a person would be shown them.
func (m *machine) lines() []string {
	lines := make([]string, 0, len(m.ran))
	for _, command := range m.ran {
		lines = append(lines, strings.TrimSpace(command.program+" "+strings.Join(command.args, " ")))
	}
	return lines
}

// commandsOf are the runs of the program, in order.
func (m *machine) commandsOf(program string) []started {
	var commands []started
	for _, command := range m.ran {
		if command.program == program {
			commands = append(commands, command)
		}
	}
	return commands
}

// commandOf is the first run of the program, or nil.
func (m *machine) commandOf(program string) *started {
	commands := m.commandsOf(program)
	if len(commands) == 0 {
		return nil
	}
	return &commands[0]
}
