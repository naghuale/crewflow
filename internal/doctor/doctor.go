// Package doctor checks that the machine can run crewflow at all, before a task
// starts: the project file is readable, the programs are installed, the tools
// the project requires answer and, when asked for, the executor answers too.
// Doctor installs nothing, it says what is missing and what to do about it
// (docs/DESIGN.md §7d).
package doctor

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/naghuale/crewflow/internal/access"
	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/forge/roles"
)

// Status is how one check ended.
type Status string

// The three answers a check can give. Only Fail stops crewflow: a Warn is
// something a person should look at, and a task may still run.
const (
	// OK is a check that passed.
	OK Status = "ok"
	// Warn is a check that passed, but that a person should look at.
	Warn Status = "warn"
	// Fail is a check that did not pass: crewflow cannot work this way.
	Fail Status = "fail"
)

// Check is one line of the report.
type Check struct {
	// Name is what the report calls the check: the program it is about, or the
	// name a gate or a tool has in the project file.
	Name string `json:"name"`
	// Status is how the check ended.
	Status Status `json:"status"`
	// Detail is one line of what was found, without anything secret in it.
	Detail string `json:"detail"`
	// Hint is what to do about it, and is there only when something is wrong.
	Hint string `json:"hint,omitempty"`
}

// Report is every check of one run of doctor, in the order they were made.
type Report struct {
	Checks []Check `json:"checks"`
	// Access is what the executor of the project may read outside the worktree of a
	// task on this machine, and the paths crewflow would not open, because a refusal
	// of the next run is about a permission that was given here (docs/DESIGN.md §7d).
	Access Access `json:"access"`
}

// Access is the reading policy of a run as this machine works it out: the folders the
// commands of the project named, the places that are closed whatever the project
// wrote, and every path that was refused with the reason why.
type Access struct {
	// Read are the folders the executor of a run may read outside its worktree.
	Read []string `json:"read"`
	// Deny are the places crewflow keeps closed to it, whatever the project asked for.
	Deny []string `json:"deny"`
	// Rejected are the paths of the project that crewflow would not open, and why.
	Rejected []access.Problem `json:"rejected"`
}

// OK reports whether nothing failed, which is what decides the exit code.
func (r Report) OK() bool {
	return !slices.ContainsFunc(r.Checks, func(check Check) bool { return check.Status == Fail })
}

// Env is everything Run needs from the machine. It is passed in whole, so that
// a test can hand it a machine of its own and nothing here depends on what
// happens to be installed where the test runs.
type Env struct {
	// LookPath finds a program the way the shell does, in PATH.
	LookPath func(name string) (string, error)
	// Run starts a program in dir with a closed stdin and returns what it wrote
	// and the code it exited with. The extra environment is added to the one of
	// the process, which is how a role of a project talks to a host of its own
	// (GH_HOST and the like) without spelling out the whole environment.
	Run func(ctx context.Context, name string, args []string, dir string, extraEnv []string) (stdout, stderr []byte, exitCode int, err error)
	// ConfigPath is the crewflow.toml to read.
	ConfigPath string
	// Home is the home of the person crewflow runs for: what a leading "~/" in the
	// file of the project stands for, and where the places that hold secrets are. It
	// is given and not read from the process, so that a test of doctor resolves no
	// path against the home of the person who runs the tests.
	Home string
	// TempDir is the empty folder the probe runs the executor in, so that a
	// probe touches nothing of the project.
	TempDir string
	// Probe asks the executor one tiny question. It is off unless it was asked
	// for, because a run of an agent spends its money and its limits.
	Probe bool
}

// Run checks the machine and reports what it found. The checks are made in the
// order of what they need: the file, then git, then the roles the file speaks
// of, then everything else the file says the project needs. A check that needs
// the file is skipped when the file is not readable, because a check that could
// not be made is not a check that failed.
func Run(ctx context.Context, env Env) Report {
	c := &checker{env: env}
	cfg, readable := c.config()
	c.git(ctx)
	if !readable {
		return c.report()
	}
	c.roles(ctx, cfg)
	c.executors(cfg)
	c.reading(ctx, cfg)
	if env.Probe {
		c.probe(ctx, cfg)
	}
	c.gates(cfg)
	c.requirements(ctx, cfg)
	return c.report()
}

// checker makes the checks of one Run and keeps them in order, together with the
// reading policy of the project, which is not a check but a part of what a run is
// about to be given.
type checker struct {
	env    Env
	checks []Check
	access Access
}

// add puts a check into the report.
func (c *checker) add(check Check) {
	c.checks = append(c.checks, check)
}

// report is what Run returns. The reading policy is there even when the file of the
// project could not be read, as three empty lists: an orchestrator reads them on every
// report and a field that is missing in one of the two is a field to guard against in
// a script.
func (c *checker) report() Report {
	if c.access.Read == nil {
		c.access = Access{Read: []string{}, Deny: []string{}, Rejected: []access.Problem{}}
	}
	return Report{Checks: c.checks, Access: c.access}
}

// config reads the project file, which every check below needs: which executor
// to run, which gates the project has and which tools it requires.
func (c *checker) config() (config.Config, bool) {
	cfg, err := config.Load(c.env.ConfigPath)
	if err != nil {
		c.add(Check{
			Name:   "config",
			Status: Fail,
			Detail: err.Error(),
			Hint:   "fix the file, or point -config at the crewflow.toml of the project",
		})
		return config.Config{}, false
	}
	c.add(Check{Name: "config", Status: OK, Detail: c.env.ConfigPath})
	return cfg, true
}

// git checks that git is installed: a task happens in a worktree, so without git
// crewflow cannot do anything at all.
func (c *checker) git(ctx context.Context) {
	git, ok := c.lookUp("git", "git", "install git")
	if !ok {
		return
	}
	stdout, stderr, code, err := c.run(ctx, git, "--version")
	if code != 0 || err != nil {
		c.add(Check{
			Name:   "git",
			Status: Fail,
			Detail: "git --version " + exited(code, stdout, stderr),
			Hint:   "install git, or put the git of the project in PATH",
		})
		return
	}
	c.add(Check{Name: "git", Status: OK, Detail: firstLine(stdout, stderr)})
}

// roles asks the three roles of the project what they found on the machine
// (docs/DESIGN.md §7g): each has a program of its own and a login of its own, so
// each is checked where it is, and the adapter of the system is the one that
// knows how to be checked.
func (c *checker) roles(ctx context.Context, cfg config.Config) {
	set, err := roles.New(cfg, c.rolesEnv())
	if err != nil {
		c.add(c.noRole(err))
		return
	}
	for _, role := range set.Checkers() {
		for _, check := range role.Doctor(ctx) {
			c.add(asLine(check))
		}
	}
}

// rolesEnv is the environment of the machine as a role needs it: the same
// programs, the same way of starting them, and the file a hint names.
func (c *checker) rolesEnv() forge.Env {
	return forge.Env{
		LookPath:   c.env.LookPath,
		Run:        c.env.Run,
		ConfigPath: c.env.ConfigPath,
	}
}

// noRole is the check for a role of a kind crewflow has no adapter for. It is a
// failed check of the setting that asked for it: a person is to change that
// setting, and not to install anything (docs/DESIGN.md §7g).
func (c *checker) noRole(err error) Check {
	var notImplemented *forge.ErrNotImplemented
	if !errors.As(err, &notImplemented) {
		return Check{
			Name:   "config",
			Status: Fail,
			Detail: err.Error(),
			Hint:   fmt.Sprintf("the keys of the roles say different things, fix them in %s", c.env.ConfigPath),
		}
	}
	role, _, _ := strings.Cut(notImplemented.Key, ".")
	return Check{
		Name:   role,
		Status: Fail,
		Detail: err.Error(),
		Hint: fmt.Sprintf("crewflow has no %s adapter yet, or change %s in %s",
			notImplemented.Kind, notImplemented.Key, c.env.ConfigPath),
	}
}

// asLine is a line of a role as a line of the report: the same words and the
// same detail, and a failure of a role is a failure like any other.
func asLine(check forge.Check) Check {
	return Check{
		Name:   check.Name,
		Status: Status(check.Status),
		Detail: check.Detail,
		Hint:   check.Hint,
	}
}

// executors checks that the executor and every executor it may fall back to are
// installed, in the order crewflow would try them (docs/DESIGN.md §7b).
func (c *checker) executors(cfg config.Config) {
	c.executor("executor", "executor.command", cfg.Executor.ExecutorSpec)
	for i, fallback := range cfg.Executor.Fallback {
		key := fmt.Sprintf("executor.fallback[%d].command", i)
		c.executor(fmt.Sprintf("executor fallback %d", i+1), key, fallback)
	}
}

// executor checks one executor, under the name a report calls it and the key a
// person fixes in the file.
func (c *checker) executor(name, key string, spec config.ExecutorSpec) {
	program, _ := commandProgram(spec.Command)
	path, ok := c.lookUp(name, program, c.changeHint(program, key))
	if !ok {
		return
	}
	c.add(Check{Name: name, Status: OK, Detail: path})
}

// gates checks that the program of every gate is installed, and stops at the
// shell: what a "sh -c" script calls is the job of [requirements] to say, not
// something a gate may hide from the report.
func (c *checker) gates(cfg config.Config) {
	for i, gate := range cfg.Gates {
		program, viaShell := commandProgram(gate.Run)
		name := "gate " + gate.Name
		path, ok := c.lookUp(name, program, c.changeHint(program, fmt.Sprintf("gates[%d].run", i)))
		if !ok {
			continue
		}
		detail := path
		if viaShell {
			detail = path + ", the script of the gate is not run here"
		}
		c.add(Check{Name: name, Status: OK, Detail: detail})
	}
}

// requirements checks every tool of [requirements]: that it is installed, that
// its check command passes and, when the project wrote a min, that the version
// it printed is not older (docs/DESIGN.md §7d).
func (c *checker) requirements(ctx context.Context, cfg config.Config) {
	for i, tool := range cfg.Requirements.Tools {
		c.requirement(ctx, i, tool)
	}
}

// requirement checks one tool, under the name a report calls it and the key a
// person fixes in the file.
func (c *checker) requirement(ctx context.Context, i int, tool config.Tool) {
	name := "tool " + tool.Name
	key := fmt.Sprintf("requirements.tools[%d].check", i)
	if len(tool.Check) == 0 {
		c.add(Check{
			Name:   name,
			Status: Fail,
			Detail: "no check command in the file",
			Hint:   fmt.Sprintf("add check to requirements.tools[%d] in %s", i, c.env.ConfigPath),
		})
		return
	}
	path, ok := c.lookUp(name, tool.Check[0], c.changeHint(tool.Check[0], key))
	if !ok {
		return
	}
	stdout, stderr, code, err := c.env.Run(ctx, path, tool.Check[1:], "", nil)
	switch {
	case code != 0 || err != nil:
		c.add(Check{
			Name:   name,
			Status: Fail,
			Detail: "the check " + exited(code, stdout, stderr),
			Hint:   fmt.Sprintf("make the check work by hand: %s", strings.Join(tool.Check, " ")),
		})
	case tool.Min != "":
		c.add(c.minimum(name, tool, stdout, stderr))
	default:
		// A check may say nothing at all and still pass: "test -e <file>"
		// is one of those.
		detail := firstLine(stdout, stderr)
		if detail == "" {
			detail = "the check passed and said nothing"
		}
		c.add(Check{Name: name, Status: OK, Detail: detail})
	}
}

// minimum compares the version a check printed with the min of the tool. A check
// that prints no version at all is a warning and not a failure: crewflow cannot
// see that the project is served, but a person is told instead of being left
// with a check that passed for no reason.
func (c *checker) minimum(name string, tool config.Tool, stdout, stderr []byte) Check {
	found := firstVersion(stdout)
	if found == "" {
		found = firstVersion(stderr)
	}
	if found == "" {
		return Check{
			Name:   name,
			Status: Warn,
			Detail: fmt.Sprintf("no version in the output, so the min %s was not compared", tool.Min),
			Hint:   c.minHint(tool, "let the check print the version"),
		}
	}
	enough, err := versionAtLeast(found, tool.Min)
	switch {
	case err != nil:
		return Check{
			Name:   name,
			Status: Warn,
			Detail: err.Error(),
			Hint:   c.minHint(tool, "write the min as numbers, for example 1.27"),
		}
	case !enough:
		return Check{
			Name:   name,
			Status: Fail,
			Detail: fmt.Sprintf("needs %s, found %s", tool.Min, found),
			Hint:   c.minHint(tool, "put a newer one in PATH, or lower the min"),
		}
	default:
		return Check{Name: name, Status: OK, Detail: firstLine(stdout, stderr)}
	}
}

// minHint is what to do about the min of a tool, whatever is wrong with it: what
// a person may do, and then the min, the tool and the file to do it in.
func (c *checker) minHint(tool config.Tool, what string) string {
	return fmt.Sprintf("%s, or fix the min %s of the tool %q in %s", what, tool.Min, tool.Name, c.env.ConfigPath)
}

// lookUp asks PATH for a program. When it is not there, the failed check is
// added and false is returned, so that a caller goes on with its next check.
func (c *checker) lookUp(name, program, hint string) (string, bool) {
	path, err := c.env.LookPath(program)
	if err != nil {
		c.add(Check{
			Name:   name,
			Status: Fail,
			Detail: fmt.Sprintf("%s is not in PATH", program),
			Hint:   hint,
		})
		return "", false
	}
	return path, true
}

// changeHint says how a program that is missing may be dealt with: installed,
// or the file changed so that crewflow looks for another one.
func (c *checker) changeHint(program, key string) string {
	return fmt.Sprintf("install %s, or change %s in %s", program, key, c.env.ConfigPath)
}

// run starts a command in the folder crewflow was called in: the checks of the
// machine itself do not depend on the project, only its file does. They also
// need nothing of the environment of the project: that is what a role is for.
func (c *checker) run(ctx context.Context, path string, args ...string) (stdout, stderr []byte, exitCode int, err error) {
	return c.env.Run(ctx, path, args, "", nil)
}

// exited is how a report says that a command did not do its job: the code it
// exited with and, if it said anything on the way out, what it said.
func exited(code int, outputs ...[]byte) string {
	detail := fmt.Sprintf("exited with %d", code)
	if line := firstLine(outputs...); line != "" {
		return detail + ": " + line
	}
	return detail
}

// shells run a script of the project instead of a program of the project, so a
// command of the shape ["sh", "-c", "…"] is checked by the shell alone.
var shells = []string{"sh", "bash", "zsh"}

// commandProgram is the program a command line runs, and whether that program
// is a shell that will be handed a script. What such a script calls is said by
// [requirements], not by the command that runs it.
func commandProgram(command []string) (program string, viaShell bool) {
	if len(command) == 0 {
		return "", false
	}
	program = command[0]
	viaShell = len(command) > 1 && command[1] == "-c" && slices.Contains(shells, filepath.Base(program))
	return program, viaShell
}

// firstLine is the first line that says something, of the outputs in order. A
// command that printed nothing has no line, and the report says that instead of
// showing a blank.
func firstLine(outputs ...[]byte) string {
	for _, output := range outputs {
		for line := range strings.Lines(string(output)) {
			if line = strings.TrimSpace(line); line != "" {
				return line
			}
		}
	}
	return ""
}
