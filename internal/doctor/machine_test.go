package doctor

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// machine is the machine a test checks: the programs it has, what a command
// prints and the code it exits with, and everything that was asked of it. It
// stands in for the host machine so that no test depends on what happens to be
// installed where it runs.
type machine struct {
	// programs are the programs in PATH, by the name a command line uses.
	programs map[string]string
	// answers is what a command writes, by its whole command line, or by its
	// first word when the arguments are not what a test cares about.
	answers map[string]answer
	// lookedUp are the programs Run asked PATH about, in order.
	lookedUp []string
	// ran are the commands Run started, in order.
	ran []command
	// cache is the folder a tool of a project names as the place of its cache, and
	// it is a folder of the test: a test of the report opens nothing of a person.
	cache string
	// run starts a command. It is a field so that a test may make the machine
	// answer, or hang, whatever it needs.
	run func(ctx context.Context, name string, args []string, dir string, extraEnv []string) (stdout, stderr []byte, exitCode int, err error)
}

// answer is what a command of the fake machine does when it runs.
type answer struct {
	stdout string
	stderr string
	code   int
}

// command is one command that was run on the fake machine.
type command struct {
	name string
	args []string
	dir  string
}

// newMachine returns a machine with nothing on it and nothing answering.
func newMachine() *machine {
	m := &machine{programs: map[string]string{}, answers: map[string]answer{}}
	m.run = m.exec
	return m
}

// has puts programs into PATH, as /usr/bin/<name>, which is a path of no
// consequence: a test looks at the name, not at where the program sits.
func (m *machine) has(programs ...string) *machine {
	for _, program := range programs {
		m.programs[program] = "/usr/bin/" + program
	}
	return m
}

// prints makes a command succeed and write output.
func (m *machine) prints(commandLine, output string) *machine {
	m.answers[commandLine] = answer{stdout: output}
	return m
}

// fails makes a command exit with the code 1 and write the line to stderr, the
// way a program that cannot do its job does.
func (m *machine) fails(commandLine, line string) *machine {
	m.answers[commandLine] = answer{stderr: line, code: 1}
	return m
}

// env is the environment that checks this machine. The home of the person is a
// folder of the test: a temporary folder of a test may be under a link, and the policy
// of reading follows every link before it opens anything, so the test works with the
// paths as this machine holds them.
func (m *machine) env(t *testing.T, configPath string) Env {
	t.Helper()
	return Env{
		LookPath:   m.lookPath,
		Run:        m.run,
		ConfigPath: configPath,
		Home:       onThisMachine(t, t.TempDir()),
		TempDir:    t.TempDir(),
	}
}

// onThisMachine is the path as the machine of a test holds it.
func onThisMachine(t *testing.T, path string) string {
	t.Helper()
	followed, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("follow %s: %v", path, err)
	}
	return followed
}

// lookPath is exec.LookPath on this machine.
func (m *machine) lookPath(name string) (string, error) {
	m.lookedUp = append(m.lookedUp, name)
	path, ok := m.programs[name]
	if !ok {
		return "", fmt.Errorf("exec: %q: not found in $PATH", name)
	}
	return path, nil
}

// exec runs a command the way the machine answers it.
func (m *machine) exec(ctx context.Context, name string, args []string, dir string, _ []string) ([]byte, []byte, int, error) {
	m.ran = append(m.ran, command{name: name, args: args, dir: dir})
	a, ok := m.answerOf(name, args)
	if !ok {
		return nil, nil, 127, fmt.Errorf("exec: %q: no answer in the machine", name)
	}
	return []byte(a.stdout), []byte(a.stderr), a.code, nil
}

// answerOf is what the machine says to a command: the entry of the whole
// command line if there is one, the entry of its first word otherwise.
func (m *machine) answerOf(name string, args []string) (answer, bool) {
	program := filepath.Base(name)
	if line := strings.TrimSpace(program + " " + strings.Join(args, " ")); line != program {
		if a, ok := m.answers[line]; ok {
			return a, true
		}
	}
	a, ok := m.answers[program]
	return a, ok
}

// answering returns a Run in which the one program answers as the test says, and
// every other program is the machine as it is.
func (m *machine) answering(program string, how func(ctx context.Context) ([]byte, []byte, int, error)) func(
	ctx context.Context, name string, args []string, dir string, extraEnv []string,
) ([]byte, []byte, int, error) {
	return func(ctx context.Context, name string, args []string, dir string, extraEnv []string) ([]byte, []byte, int, error) {
		if filepath.Base(name) != program {
			return m.exec(ctx, name, args, dir, extraEnv)
		}
		m.ran = append(m.ran, command{name: name, args: args, dir: dir})
		return how(ctx)
	}
}

// commandOf is the first command that was run with the program, or nil.
func (m *machine) commandOf(program string) *command {
	for i, ran := range m.ran {
		if filepath.Base(ran.name) == program {
			return &m.ran[i]
		}
	}
	return nil
}

// shortenProbeTimeout makes the probe end sooner, so that a test does not have
// to wait the two minutes a person would.
func shortenProbeTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	was := probeTimeout
	probeTimeout = d
	t.Cleanup(func() { probeTimeout = was })
}
