package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/naghuale/crewflow/internal/doctor"
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
	if stderr.Len() != 0 {
		t.Errorf("crewflow doctor wrote %q to stderr, want nothing", stderr.String())
	}
	for _, want := range []string{"✓", "config", "git", "gh", "executor", "this machine is ready for a task"} {
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
	want := []string{"config", "git", "gh", "gh login", "executor"}
	got := make([]string, 0, len(report.Checks))
	for _, check := range report.Checks {
		got = append(got, check.Name)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("checks = %v, want %v", got, want)
	}
	for _, check := range report.Checks {
		if check.Status != doctor.OK {
			t.Errorf("check %q = %q, want ok on a ready machine", check.Name, check.Status)
		}
		if check.Detail == "" {
			t.Errorf("check %q has no detail, want what was found", check.Name)
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
}

// use makes the doctor command check this machine, and puts the machine back
// when the test is over.
func (m *machine) use(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { systemEnv = doctor.System })
	systemEnv = func(configPath, tempDir string, probe bool) doctor.Env {
		m.seen = doctor.Env{
			LookPath:   m.lookPath,
			Run:        m.run,
			ConfigPath: configPath,
			TempDir:    tempDir,
			Probe:      probe,
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
func (m *machine) run(ctx context.Context, name string, args []string, dir string) ([]byte, []byte, int, error) {
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
