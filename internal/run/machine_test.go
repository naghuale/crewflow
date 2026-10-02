package run

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/proc"
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
	// userHome is the home of the person this run happens for: the paths of the file
	// of a project are resolved against it, and the places crewflow keeps closed are
	// the ones under it. It is a folder of the test, so that no run of a test ever
	// resolves a path against the home of the person who runs the tests.
	userHome string
	// repo is the repository of the project. It is a folder and nothing else: a
	// test of the flow of a run does not need a git at all, and the one that does
	// makes a repository of its own (docs/DESIGN.md §7).
	repo string
	// git is the folder of the repository of the project as git names it, and where
	// the local ignore of the scratch of a run is written.
	git string
	// answers is what a command writes and the code it exits with.
	answers map[string]answer
	// changed is what git says the working copy of a folder has changed, by that folder:
	// two worktrees are two questions of the same command line, and the question of the
	// admission of a pair is asked about the working copies of both tasks of it (§7c).
	changed map[string]string
	// then is what a program answers run after run, for a test that runs the same
	// program more than once and needs to tell the runs apart: the two runs of a task
	// that crewflow goes on with by itself are two runs of one executor, and what it
	// says in them is not the same. An entry is taken off as the program is run, and
	// the last one of them is what every run after it answers.
	then map[string][]answer
	// mu guards ran and handed, which the run of an executor writes from its own
	// goroutine while the test of a running executor looks at the machine.
	mu sync.Mutex
	// ran are the commands that were started, in order.
	ran []started
	// handed is the answer the executor of the last run was started with, which is
	// where the environment a run hands its executor is found.
	handed answer
	// given is the extra environment the machine handed to the programs it started,
	// which is where the route of the project is found.
	given []string
	// clock is the time of the machine, which moves on with every question asked of
	// it, so that a report says when a thing happened and a test does not wait for
	// it to happen.
	clock time.Time
	// looked is where a test asks the run of a task to look at its own silence: a run
	// that is quiet for a while is a run a test says so about with its own clock, and
	// not with a wait (docs/DESIGN.md §6).
	looked chan looking
}

// looking is one request of a test to look at the silence of a run, and the channel the
// run closes when it has looked: a test that asks a run what it would say about its
// silence has to see the line of it before it reads the file it is in.
type looking struct {
	done chan struct{}
}

// theHead is the commit the worktree of a task of a test stands at, which is what the
// point of a run that stopped at a decision of a person is checked against and what a
// test moves when it wants to say that the branch has gone on without it
// (docs/DESIGN.md §7i).
const theHead = "9f1c0de4a4a0b1f2c3d4e5f60718293a4b5c6d7"

// answer is what a command of the machine does when it runs.
type answer struct {
	stdout string
	stderr string
	code   int
	// hangs says that the program waits for the end of the run and is killed with
	// it, which is what an agent that never answers looks like from the outside.
	hangs bool
	// wrote is closed once the program has written what it writes, and wait is
	// released by the test to let it go on. A test that looks at a run while it is
	// going needs the program to still be there while it looks.
	wrote chan struct{}
	wait  <-chan struct{}
	// more is what an executor of a test says after it has started, as an agent that
	// works says: the journal of the run grows while it goes on, and every line of it
	// is a sign of life of the run (docs/DESIGN.md §6). The run ends when the test
	// closes the channel.
	more chan string
	// env is the extra environment the machine hands to a program, which is where
	// the temporary folder of the executor is found.
	env []string
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
		userHome:  t.TempDir(),
		worktrees: t.TempDir(),
		repo:      t.TempDir(),
		git:       t.TempDir(),
		answers: map[string]answer{
			"git fetch":            {},
			"git worktree add":     {},
			"git diff --name-only": {},
		},
		changed: map[string]string{},
		then:    map[string][]answer{},
		clock:   time.Date(2026, time.September, 28, 10, 0, 0, 0, time.UTC),
		looked:  make(chan looking),
	}
	// Git is asked where the repository of the project is, because the scratch of a
	// run is kept out of it, and what commit the worktree of a task stands at, because
	// the point a run stops at is checked against it. Both are folders and commits of
	// the test.
	m.answers["git rev-parse --git-common-dir"] = answer{stdout: m.git}
	m.answers["git rev-parse HEAD"] = answer{stdout: theHead}
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

// says is what a program of the machine answers, run after run, and a run of it after
// the last answer is given answers whatever was given before: a test that needs a
// second run of an executor to be different from the first says so, and a test that
// does not says nothing.
func (m *machine) says(program string, answers ...answer) *machine {
	m.then[program] = answers
	return m
}

// answerNext is the answer of a program that has more than one to give, taken off the
// list under the lock: the executor of a run is started by the goroutine of that run,
// and a test that looks at the machine looks at it from its own.
func (m *machine) answerNext(program string) (answer, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	answers, ok := m.then[program]
	if !ok || len(answers) == 0 {
		return answer{}, false
	}
	if len(answers) > 1 {
		m.then[program] = answers[1:]
	} else {
		delete(m.then, program)
	}
	return answers[0], true
}

// env is the machine a run of a test happens on.
func (m *machine) env() Env {
	return Env{
		Home:     m.home,
		UserHome: m.userHome,
		Environ:  []string{"HOME=" + m.userHome, "PATH=/usr/bin"},
		Command:  m.exec,
		Stream:   m.stream,
		Tick:     m.stall,
		Now:      m.now,
		Process:  m.process,
	}
}

// wasGiven is the extra environment the machine handed to a program, which is where the
// route of the project is found in the programs a run starts for itself.
func (m *machine) wasGiven(environment []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.given = append(m.given, environment...)
}

// givenToPrograms is everything the machine handed to the programs of the last run,
// which is how a test sees what a run tells its own git about the network.
func (m *machine) givenToPrograms() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.given...)
}

// process is the process a run of a test happens in: a number and the moment it was
// started, both of the machine of the test, so that no test of a run asks the machine
// it happens to run on.
func (m *machine) process() (proc.Process, bool) {
	return proc.Process{Pid: 4242, StartedAt: m.at()}, true
}

// now is the clock of the machine. Every question moves it a minute on, so that the
// start and the end of a run are two moments and not one.
func (m *machine) now() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clock = m.clock.Add(time.Minute)
	return m.clock
}

// at is the time of the machine as a test says it: a test that wants a run to have been
// quiet for a quarter of an hour moves the clock of the machine and asks the run to look
// at its silence, and a run that looks at itself asks the same question (docs/DESIGN.md
// §6).
func (m *machine) at() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.clock
}

// quiet is the machine at a moment `since` after the one it is at, which is how a test
// says that a run has shown nothing for a while.
func (m *machine) quiet(since time.Duration) *machine {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clock = m.clock.Add(since)
	return m
}

// begins is the machine at a moment of its own choosing: a test that wants the sign of
// life of a run to be the moment its executor wrote, and that moment is in a file the
// machine wrote for real, puts the clock of the machine there (§6).
func (m *machine) begins(at time.Time) *machine {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clock = at
	return m
}

// stall is the tick of a machine of a test: the run of a task looks at its own silence
// when the test says so and not before, so that a test of the mark of a standing run
// never waits for a clock of its own.
func (m *machine) stall(ctx context.Context, _ time.Duration, onTick func()) func() {
	watch, cancel := context.WithCancel(ctx)
	over := make(chan struct{})
	go func() {
		defer close(over)
		for {
			select {
			case <-watch.Done():
				return
			case ask := <-m.looked:
				onTick()
				close(ask.done)
			}
		}
	}()
	return func() {
		cancel()
		<-over
	}
}

// look is the moment a test makes the run of the task look at its own silence, and waits
// until it has looked: what the run says about it is in the files of the attempt, and a
// test that reads them has to know that the line is already in them.
func (m *machine) look() {
	ask := looking{done: make(chan struct{})}
	select {
	case m.looked <- ask:
		<-ask.done
	case <-time.After(30 * time.Second):
		panic("the run of the test does not look at its own silence")
	}
}

// exec runs a command the way the machine answers it, and leaves behind what the
// command did to the machine.
func (m *machine) exec(ctx context.Context, name string, args []string, dir string, extraEnv []string) ([]byte, []byte, int, error) {
	program := filepath.Base(name)
	m.started(program, args, dir)
	m.wasGiven(extraEnv)
	answer, ok := m.answered(dir, program, args)
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

// stream starts a program the way the machine answers it and writes what it writes to
// the writers it was given as it writes it, and it is where a fake executor of a test
// waits to be looked at while the run goes on.
func (m *machine) stream(ctx context.Context, name string, args []string, dir string, env []string, stdout, stderr io.Writer) (int, error) {
	program := filepath.Base(name)
	m.started(program, args, dir)
	answer, ok := m.answered(dir, program, args)
	if !ok {
		return 127, fmt.Errorf("stream: %q: no answer in the machine", program)
	}
	if _, err := io.WriteString(stdout, answer.stdout); err != nil {
		return 0, err
	}
	if _, err := io.WriteString(stderr, answer.stderr); err != nil {
		return 0, err
	}
	answer.env = env
	m.stored(answer)
	if answer.wrote != nil {
		close(answer.wrote)
	}
	switch {
	case answer.more != nil:
		// An executor that goes on writing is an executor that can be stopped: a program
		// crewflow asks to stop with the context of the run stops where it stands, and a
		// fake that kept writing after that would hang a test of the run that stops a
		// hanging run by itself (docs/DESIGN.md §7a).
	writing:
		for {
			select {
			case text, open := <-answer.more:
				if !open {
					break writing
				}
				if _, err := io.WriteString(stdout, text); err != nil {
					return 0, err
				}
			case <-ctx.Done():
				return -1, nil
			}
		}
	case answer.wait != nil:
		select {
		case <-answer.wait:
		case <-ctx.Done():
			return -1, nil
		}
	case answer.hangs:
		<-ctx.Done()
		return -1, nil
	}
	if answer.code == 0 {
		m.made(program, args)
	}
	return answer.code, nil
}

// started writes a command down, in order, under the lock: the executor of a run is
// started by the goroutine of that run, and a test that looks at a run while it is
// going looks at the machine from its own.
func (m *machine) started(program string, args []string, dir string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ran = append(m.ran, started{program: program, args: args, dir: dir})
}

// envOf is the extra environment the executor of the last run was started with, which
// is where a run points the temporary files of its executor.
func (m *machine) envOf() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.handed.env
}

// stored keeps what the executor of the last run was started with, under the lock.
func (m *machine) stored(answer answer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handed = answer
}

// answerOf is what the machine says to a command: the entry of the whole command
// line, or of the longest beginning of it that the machine knows, or the next of what
// the program says run after run, or the program alone. A test may therefore answer a
// command that takes a path of its own without writing the path out.
func (m *machine) answerOf(program string, args []string) (answer, bool) {
	line := program
	for _, arg := range args {
		line += " " + arg
		if answer, ok := m.answers[line]; ok {
			return answer, true
		}
	}
	if answer, ok := m.answerNext(program); ok {
		return answer, true
	}
	answer, ok := m.answers[program]
	return answer, ok
}

// answered is what a command answers in the folder it was run in, and the files the
// working copies of a test have changed are a question of the folder as much as of the
// command: two worktrees ask the same `git diff --name-only` and are two different
// questions (docs/DESIGN.md §7c).
func (m *machine) answered(dir, program string, args []string) (answer, bool) {
	changed, is := m.changed[dir]
	if !is || !strings.HasPrefix(program, "git") {
		return m.answerOf(program, args)
	}
	for _, arg := range args {
		if arg == "diff" || arg == "ls-files" {
			return answer{stdout: changed}, true
		}
	}
	return m.answerOf(program, args)
}

// made is what a command left on the machine. Only git makes worktrees here, and it
// is the one command a test of a second run of a task meets on the way: the folder
// of the worktree is there afterwards, or a continuation has nothing to go on in.
// And git takes a worktree away again, which is what a test of a run that was cut
// off before the executor started meets: the folder has to be gone for the next run
// of the task to make one of its own.
func (m *machine) made(program string, args []string) {
	if program != "git" {
		return
	}
	switch {
	case len(args) >= 5 && args[0] == "worktree" && args[1] == "add":
		m.has(args[4])
	case len(args) >= 4 && args[0] == "worktree" && args[1] == "remove":
		m.gone(args[len(args)-1])
	}
}

// gone takes a folder of the machine away, as `git worktree remove` does.
func (m *machine) gone(path string) {
	if err := os.RemoveAll(path); err != nil {
		panic(fmt.Sprintf("take %s away: %v", path, err))
	}
}

// all are the commands that were run, in order, under the lock.
func (m *machine) all() []started {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.ran)
}

// lines are the commands that were run, in order, as a person would be shown them.
func (m *machine) lines() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	lines := make([]string, 0, len(m.ran))
	for _, command := range m.ran {
		lines = append(lines, strings.TrimSpace(command.program+" "+strings.Join(command.args, " ")))
	}
	return lines
}

// commandsOf are the runs of the program, in order.
func (m *machine) commandsOf(program string) []started {
	m.mu.Lock()
	defer m.mu.Unlock()
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
