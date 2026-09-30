package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/naghuale/crewflow/internal/doctor"
	"github.com/naghuale/crewflow/internal/forge"
)

// projectConfig is a crewflow.toml of a project that needs nothing but its
// executor, so that a test of the command sees only the checks of the machine
// itself.
const projectConfig = `
[project]
repo = "naghuale/crewflow"

[executor]
command = ["agent", "run", "--dir", "{worktree}", "{prompt}"]
`

// TestRunDoctorReady checks the report a person gets on a machine where nothing
// is missing, and the code a script reads.
func TestRunDoctorReady(t *testing.T) {
	(&machine{}).use(t)
	var stdout, stderr bytes.Buffer

	code := run([]string{"doctor", "-config", writeProject(t)}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow doctor = %d, want %d (stderr: %q)\n%s", code, exitOK, stderr.String(), stdout.String())
	}
	// The report is the answer and is printed to stdout, and the steps of the report are
	// said on the way out to stderr: a machine that takes its time is a machine whose
	// steps a person watches, and nothing of them is part of the answer a script reads
	// with `-json` (docs/DESIGN.md §7d, §7i).
	for _, want := range []string{"the file of the project", "the roles of the project", "the requirements"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("crewflow doctor wrote %q to stderr, want it to say the step %q", stderr.String(), want)
		}
	}
	if strings.Contains(stdout.String(), "crewflow doctor:") {
		t.Errorf("crewflow doctor wrote %q to stdout, want the report and nothing else", stdout.String())
	}
	for _, want := range []string{
		"✓", "config", "git", "gh", "executor", "nothing is missing",
		// The one thing a machine of the pilot is told about itself: the powers of its
		// executor are the powers of the login, and the mode of the bot is the answer
		// (docs/DESIGN.md §7i).
		"executor identity", "shared rights", `mode = "bot"`,
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("crewflow doctor wrote %q, want it to mention %q", stdout.String(), want)
		}
	}
	if strings.Contains(stdout.String(), "✗") {
		t.Errorf("crewflow doctor wrote %q, want no failed check on a ready machine", stdout.String())
	}
}

// TestRunDoctorMissingGit checks that what is missing is named, with the hint
// under the line of the check, and that the exit code says that crewflow cannot
// work here.
func TestRunDoctorMissingGit(t *testing.T) {
	machine := &machine{missing: map[string]bool{"git": true}}
	machine.use(t)
	var stdout, stderr bytes.Buffer

	code := run([]string{"doctor", "-config", writeProject(t)}, &stdout, &stderr)

	if code != exitFailure {
		t.Fatalf("crewflow doctor = %d, want %d", code, exitFailure)
	}
	lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	found := false
	for i, line := range lines {
		if !strings.HasPrefix(line, "✗") || !strings.Contains(line, "git") {
			continue
		}
		found = true
		if i+1 >= len(lines) || !strings.Contains(lines[i+1], "install git") {
			t.Errorf("the hint of the failed check is not on the next line: %q", stdout.String())
		}
	}
	if !found {
		t.Errorf("crewflow doctor wrote %q, want a failed check of git", stdout.String())
	}
	if !strings.Contains(stdout.String(), "checks failed") ||
		!strings.Contains(stdout.String(), "fix them before a task starts") {
		t.Errorf("crewflow doctor wrote %q, want it to say what is wrong", stdout.String())
	}
}

// TestRunDoctorSpecifiedSetting is the manual check of §5: a file that asks for a
// mode crewflow has not written is refused by name, with the task that writes it
// and what to write instead, and the code is not zero.
func TestRunDoctorSpecifiedSetting(t *testing.T) {
	(&machine{}).use(t)
	project := writeConfig(t, projectConfig+`
[isolation]
mode = "sandbox"
`)
	var stdout, stderr bytes.Buffer

	code := run([]string{"doctor", "-config", project}, &stdout, &stderr)

	if code != exitFailure {
		t.Fatalf("crewflow doctor = %d, want %d for a setting crewflow does not do", code, exitFailure)
	}
	for _, want := range []string{"isolation.mode", "sandbox", "crewflow#55", `mode = "host"`} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("crewflow doctor wrote %q, want it to mention %q", stdout.String(), want)
		}
	}
}

// TestRunDoctorNoConfig is the first case of the manual check: a folder that is
// not a project yet says so, and the code is not zero.
func TestRunDoctorNoConfig(t *testing.T) {
	(&machine{}).use(t)
	missing := filepath.Join(t.TempDir(), "crewflow.toml")
	var stdout, stderr bytes.Buffer

	code := run([]string{"doctor", "-config", missing}, &stdout, &stderr)

	if code != exitFailure {
		t.Fatalf("crewflow doctor = %d, want %d", code, exitFailure)
	}
	for _, want := range []string{"crewflow.toml", "✗", "-config"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("crewflow doctor wrote %q, want it to mention %q", stdout.String(), want)
		}
	}
}

// TestRunDoctorJSON checks the report for the orchestrator: the same checks as
// the text, and JSON that can be read.
func TestRunDoctorJSON(t *testing.T) {
	(&machine{}).use(t)
	var stdout, stderr bytes.Buffer

	code := run([]string{"doctor", "-config", writeProject(t), "-json"}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow doctor -json = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	var report doctor.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("crewflow doctor -json wrote %q, which is not JSON: %v", stdout.String(), err)
	}
	want := []string{"config", "git", "gh", "gh login", "executor identity", "orchestrator identity",
		"authority separation", "executor", "access read", "executor rights"}
	got := make([]string, 0, len(report.Checks))
	for _, check := range report.Checks {
		got = append(got, check.Name)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("checks = %v, want %v", got, want)
	}
	for _, check := range report.Checks {
		// The mode of the owner is the one line of a ready machine that is worth a
		// look and not a failure: nothing a run needs is missing, and the powers of
		// the executor are the powers of the login (docs/DESIGN.md §7i).
		if check.Status == doctor.Fail {
			t.Errorf("check %q = %q, want no failure on a ready machine", check.Name, check.Status)
		}
		if check.Detail == "" {
			t.Errorf("check %q has no detail, want what was found", check.Name)
		}
	}
	// The mode of the executor is in the report itself and not only in a line of a
	// check: an orchestrator reads the report on every run and has to know whose
	// powers a run has without parsing a line (§7i).
	if report.Identity.Mode != forge.ModeOwner || !strings.Contains(report.Identity.Description, "shared rights") {
		t.Errorf("the report holds the identity %+v, want the mode of the owner with the rights it shares", report.Identity)
	}
	// The reading policy is a part of every report: what the executor may read outside
	// the worktree, what it may never read, and the paths crewflow would not open. The
	// lists are there even when they are empty, because a script reads them on every
	// report and a field that is missing in one of them is a field to guard against.
	var whole map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &whole); err != nil {
		t.Fatalf("crewflow doctor -json wrote %q, which is not JSON: %v", stdout.String(), err)
	}
	// The settings of the project that ask for what crewflow has not written are a
	// part of every report for the same reason: a report of a machine with nothing
	// in them says so with an empty list, and a field that is missing is a field a
	// script has to guard against (docs/DESIGN.md §5).
	asked, isList := whole["specified"].([]any)
	if !isList || asked == nil {
		t.Errorf("specified = %v, want a list, empty when every key of the file says what crewflow does", whole["specified"])
	}
	policy, isTable := whole["access"].(map[string]any)
	if !isTable {
		t.Fatalf("the report holds %v, want an access section", whole)
	}
	for _, field := range []string{"read", "deny", "rejected"} {
		list, isList := policy[field].([]any)
		if !isList || list == nil {
			t.Errorf("access.%s = %v, want a list, empty when there is nothing in it", field, policy[field])
		}
	}
}

// TestRunDoctorJSONFailure checks that the code says it as well, so that a
// script needs to read only one thing.
func TestRunDoctorJSONFailure(t *testing.T) {
	(&machine{missing: map[string]bool{"gh": true}}).use(t)
	var stdout, stderr bytes.Buffer

	code := run([]string{"doctor", "-config", writeProject(t), "-json"}, &stdout, &stderr)

	if code != exitFailure {
		t.Fatalf("crewflow doctor -json = %d, want %d", code, exitFailure)
	}
	var report doctor.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("crewflow doctor -json wrote %q, which is not JSON: %v", stdout.String(), err)
	}
	failed := 0
	for _, check := range report.Checks {
		if check.Status == doctor.Fail {
			failed++
			if check.Hint == "" {
				t.Errorf("the failed check %q has no hint, want what to do about it", check.Name)
			}
		}
	}
	if failed == 0 {
		t.Errorf("checks = %v, want a failed check when gh is not installed", report.Checks)
	}
}

// TestRunDoctorProbe checks that -probe makes the executor answer in an empty
// folder of its own, and that the folder is gone afterwards.
func TestRunDoctorProbe(t *testing.T) {
	machine := &machine{}
	machine.use(t)
	var stdout, stderr bytes.Buffer

	run([]string{"doctor", "-config", writeProject(t), "-probe"}, &stdout, &stderr)

	if !machine.seen.Probe {
		t.Error("-probe was not passed on, want the executor to be asked")
	}
	if len(machine.folders) != 1 {
		t.Fatalf("the executor ran in %q, want it to run in one empty folder", machine.folders)
	}
	folder := machine.folders[0]
	if !strings.HasPrefix(folder, os.TempDir()) {
		t.Errorf("the probe ran in %q, want a folder of its own outside the project", folder)
	}
	if _, err := os.Stat(folder); !os.IsNotExist(err) {
		t.Errorf("the folder of the probe %q is still there, want it removed", folder)
	}
}

// TestRunDoctorWithoutProbe checks that nothing is created when the probe was
// not asked for.
func TestRunDoctorWithoutProbe(t *testing.T) {
	machine := &machine{}
	machine.use(t)
	var stdout, stderr bytes.Buffer

	run([]string{"doctor", "-config", writeProject(t)}, &stdout, &stderr)

	if machine.seen.Probe {
		t.Error("the executor was asked to answer without -probe")
	}
	if machine.seen.TempDir != "" {
		t.Errorf("the doctor was given the folder %q, want none without a probe", machine.seen.TempDir)
	}
}

// TestRunDoctorDefaultConfigPath checks where a person looks first: a project
// keeps its crewflow.toml in the folder crewflow was called in.
func TestRunDoctorDefaultConfigPath(t *testing.T) {
	machine := &machine{}
	machine.use(t)
	var stdout, stderr bytes.Buffer

	run([]string{"doctor"}, &stdout, &stderr)

	if machine.seen.ConfigPath != "./crewflow.toml" {
		t.Errorf("-config defaulted to %q, want %q", machine.seen.ConfigPath, "./crewflow.toml")
	}
}

func TestRunDoctorCalledWrong(t *testing.T) {
	cases := [][]string{
		{"doctor", "-nope"},
		{"doctor", "extra"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			(&machine{}).use(t)
			var stdout, stderr bytes.Buffer

			if code := run(args, &stdout, &stderr); code != exitUsage {
				t.Errorf("run(%v) = %d, want %d", args, code, exitUsage)
			}
			if stdout.Len() != 0 {
				t.Errorf("run(%v) wrote %q to stdout, want nothing", args, stdout.String())
			}
			if !strings.Contains(stderr.String(), "Usage:") {
				t.Errorf("run(%v) wrote %q to stderr, want the usage", args, stderr.String())
			}
		})
	}
}

// TestRunDoctorWarning checks that a check that is worth a look but is not
// broken does not stop crewflow: the code is zero and the line says so.
func TestRunDoctorWarning(t *testing.T) {
	(&machine{}).use(t)
	project := writeConfig(t, `
[project]
repo = "naghuale/crewflow"

[executor]
command = ["agent", "run", "--dir", "{worktree}", "{prompt}"]

[requirements]
tools = [{ name = "go", check = ["go", "version"], min = "1.27" }]
`)
	var stdout, stderr bytes.Buffer

	code := run([]string{"doctor", "-config", project}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow doctor = %d, want %d for a warning (stdout: %q)", code, exitOK, stdout.String())
	}
	for _, want := range []string{"!", "no version in the output", "worth a look"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("crewflow doctor wrote %q, want it to mention %q", stdout.String(), want)
		}
	}
	if strings.Contains(stdout.String(), "✗") {
		t.Errorf("crewflow doctor wrote %q, want no failed check", stdout.String())
	}
}

// machine is the machine a test makes the doctor command check, so that a test
// of the command never depends on what is installed where it runs.
type machine struct {
	// missing are the programs that are not installed.
	missing map[string]bool
	// seen is the environment the command was built with.
	seen doctor.Env
	// folders are the folders a command was run in.
	folders []string
	// home is the home of the person the doctor command is run for: a folder of the
	// test, so that no test of the command resolves a path against the home of the
	// person who runs it.
	home string
}

// use makes the doctor command check this machine, and puts the machine back
// when the test is over.
func (m *machine) use(t *testing.T) {
	t.Helper()
	m.home = t.TempDir()
	t.Cleanup(func() { systemEnv = doctor.System })
	// The steps of the report and whatever the store of the secrets is waiting for are
	// said to the writer the command was given, and the test looks at that writer: the
	// steps of a report are not part of its answer and never go to stdout (§7i).
	systemEnv = func(configPath, tempDir string, probe bool, out io.Writer) doctor.Env {
		m.seen = doctor.Env{
			LookPath:   m.lookPath,
			Run:        m.run,
			ConfigPath: configPath,
			Home:       m.home,
			TempDir:    tempDir,
			Probe:      probe,
			Out:        out,
		}
		return m.seen
	}
}

// lookPath is exec.LookPath on this machine.
func (m *machine) lookPath(name string) (string, error) {
	if m.missing[name] {
		return "", fmt.Errorf("exec: %q: not found in $PATH", name)
	}
	return "/usr/bin/" + name, nil
}

// run starts a command that answers with its own command line and exits zero,
// which is enough for the checks of a project that needs nothing else.
func (m *machine) run(_ context.Context, name string, args []string, dir string, _ []string) ([]byte, []byte, int, error) {
	if dir != "" {
		m.folders = append(m.folders, dir)
	}
	return []byte(filepath.Base(name) + " " + strings.Join(args, " ")), nil, 0, nil
}

// writeProject writes a crewflow.toml of a project that needs nothing but its
// executor into a folder of its own, so that a test never reads the file of the
// project it runs in.
func writeProject(t *testing.T) string {
	t.Helper()
	return writeConfig(t, projectConfig)
}

// writeConfig writes the given crewflow.toml into a folder of its own and
// returns its path.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "crewflow.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}
