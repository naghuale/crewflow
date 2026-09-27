package github

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/naghuale/crewflow/internal/forge"
)

// machine is the machine the adapter finds: the programs in PATH, what gh writes
// and the code it exits with, and everything that was asked of it. It stands in
// for the host machine, so that no test of the adapter ever runs the real gh.
type machine struct {
	// programs are the programs in PATH, by the name a command line uses.
	programs map[string]string
	// answers is what gh writes, by as many first words of its command line as
	// a test cares about.
	answers map[string]answer
	// ran are the commands that were started, in order.
	ran []command
}

// answer is what gh does when it runs.
type answer struct {
	stdout string
	stderr string
	code   int
	err    error
}

// command is one gh that was run on the fake machine.
type command struct {
	args []string
	env  []string
}

// newMachine returns a machine with gh on it and nothing answering.
func newMachine() *machine {
	m := &machine{programs: map[string]string{"gh": "/usr/bin/gh"}, answers: map[string]answer{}}
	m.answers["--version"] = answer{stdout: "gh version 2.62.0 (2024-11-14)\n"}
	m.answers["auth status"] = answer{stdout: "  ✓ Logged in to github.com account octocat (keyring)\n"}
	return m
}

// prints makes gh succeed and write output.
func (m *machine) prints(commandLine, output string) *machine {
	m.answers[commandLine] = answer{stdout: output}
	return m
}

// fails makes gh exit with the code 1 and write the line to stderr, the way a
// program that cannot do its job does.
func (m *machine) fails(commandLine, line string) *machine {
	m.answers[commandLine] = answer{stderr: line, code: 1}
	return m
}

// without takes programs out of PATH.
func (m *machine) without(programs ...string) *machine {
	for _, program := range programs {
		delete(m.programs, program)
	}
	return m
}

// commandLine is what gh was asked, as a person would write it in a shell.
func (m *machine) commandLine(i int) string {
	if i >= len(m.ran) {
		return ""
	}
	return strings.Join(m.ran[i].args, " ")
}

// env is the environment the adapter is given, so that no test touches the
// environment of the machine it runs on.
func (m *machine) env(t *testing.T) forge.Env {
	t.Helper()
	return forge.Env{
		LookPath:   m.lookPath,
		Run:        m.run,
		ConfigPath: filepath.Join(t.TempDir(), "crewflow.toml"),
	}
}

// lookPath is exec.LookPath on this machine.
func (m *machine) lookPath(name string) (string, error) {
	path, ok := m.programs[name]
	if !ok {
		return "", fmt.Errorf("exec: %q: not found in $PATH", name)
	}
	return path, nil
}

// run starts gh the way the machine answers it.
func (m *machine) run(_ context.Context, name string, args []string, _ string, env []string) ([]byte, []byte, int, error) {
	m.ran = append(m.ran, command{args: args, env: env})
	if filepath.Base(name) != "gh" {
		return nil, nil, 127, fmt.Errorf("exec: %q: not gh", name)
	}
	a, ok := m.answerOf(args)
	if !ok {
		return nil, nil, 127, fmt.Errorf("exec: gh %s: no answer in the machine", strings.Join(args, " "))
	}
	if a.err != nil {
		return nil, nil, -1, a.err
	}
	return []byte(a.stdout), []byte(a.stderr), a.code, nil
}

// answerOf is what the machine says to gh: the answer of the whole command line
// if there is one, the answer of its first two words otherwise, and of its first
// word last. That way a test may name the whole command or only the part it is
// about.
func (m *machine) answerOf(args []string) (answer, bool) {
	for _, key := range []string{strings.Join(args, " "), strings.Join(args[:min(2, len(args))], " "), args[0]} {
		if a, ok := m.answers[key]; ok {
			return a, true
		}
	}
	return answer{}, false
}
