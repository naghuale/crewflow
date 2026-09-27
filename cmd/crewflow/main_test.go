package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain points the home directory of the machine at a folder of the run of the
// tests, for every test of the package. A command of crewflow resolves "~" to the
// home of the person it runs on, and a test of it must not write there: what a test
// leaves in the home of a person outlives the test, and a journal or a state of
// another project in it makes a later run believe what never happened. A test that
// needs a home of its own sets one; this is the net under all of them.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "crewflow-test-home-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "crewflow tests: make a home of their own: %v\n", err)
		os.Exit(1)
	}
	for _, key := range []string{"HOME", "USERPROFILE"} {
		if err := os.Setenv(key, home); err != nil {
			fmt.Fprintf(os.Stderr, "crewflow tests: point %s at %s: %v\n", key, home, err)
			os.Exit(1)
		}
	}
	code := m.Run()
	if err := os.RemoveAll(home); err != nil {
		fmt.Fprintf(os.Stderr, "crewflow tests: take %s away: %v\n", home, err)
	}
	os.Exit(code)
}

func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(version) = %d, want 0 (stderr: %q)", code, stderr.String())
	}
	want := "crewflow dev (commit unknown, built unknown)\n"
	if got := stdout.String(); got != want {
		t.Errorf("run(version) wrote %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Errorf("run(version) wrote %q to stderr, want nothing", stderr.String())
	}
}

func TestRunHelp(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		t.Run(arg, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run([]string{arg}, &stdout, &stderr); code != 0 {
				t.Fatalf("run(%s) = %d, want 0 (stderr: %q)", arg, code, stderr.String())
			}
			if !strings.Contains(stdout.String(), "Usage:") {
				t.Errorf("run(%s) wrote %q, want the usage", arg, stdout.String())
			}
			for _, sub := range []string{"doctor", "version", "help"} {
				if !strings.Contains(stdout.String(), sub) {
					t.Errorf("usage does not mention the %q subcommand", sub)
				}
			}
			if stderr.Len() != 0 {
				t.Errorf("run(%s) wrote %q to stderr, want nothing", arg, stderr.String())
			}
		})
	}
}

func TestRunUnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"bogus"}, &stdout, &stderr); code != exitUsage {
		t.Errorf("run(bogus) = %d, want %d", code, exitUsage)
	}
	if stdout.Len() != 0 {
		t.Errorf("run(bogus) wrote %q to stdout, want nothing", stdout.String())
	}
	for _, want := range []string{"bogus", "Usage:"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("run(bogus) wrote %q to stderr, want it to mention %q", stderr.String(), want)
		}
	}
}

func TestRunWithoutSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != exitUsage {
		t.Errorf("run() = %d, want %d", code, exitUsage)
	}
	if stdout.Len() != 0 {
		t.Errorf("run() wrote %q to stdout, want nothing", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Errorf("run() wrote %q to stderr, want the usage", stderr.String())
	}
}

func TestRunBadFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"version", "-nope"}, &stdout, &stderr); code != exitUsage {
		t.Errorf("run(version -nope) = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr.String(), "nope") {
		t.Errorf("run(version -nope) wrote %q to stderr, want it to mention the flag", stderr.String())
	}
}

// TestBinary runs the built binary, because that is the only way to see what
// -ldflags puts into it.
func TestBinary(t *testing.T) {
	stamped := build(t, strings.Join([]string{
		"-X github.com/naghuale/crewflow/internal/buildinfo.Version=1.2.3",
		"-X github.com/naghuale/crewflow/internal/buildinfo.Commit=deadbeef",
		"-X github.com/naghuale/crewflow/internal/buildinfo.Date=2026-09-28T09:00:00Z",
	}, " "))
	plain := build(t, "")

	t.Run("version without ldflags", func(t *testing.T) {
		stdout, stderr, code := execute(t, plain, "version")
		if code != 0 {
			t.Fatalf("crewflow version = %d, want 0 (stderr: %q)", code, stderr)
		}
		want := "crewflow dev (commit unknown, built unknown)\n"
		if stdout != want {
			t.Errorf("crewflow version wrote %q, want %q", stdout, want)
		}
	})

	t.Run("version with ldflags", func(t *testing.T) {
		stdout, stderr, code := execute(t, stamped, "version")
		if code != 0 {
			t.Fatalf("crewflow version = %d, want 0 (stderr: %q)", code, stderr)
		}
		want := "crewflow 1.2.3 (commit deadbeef, built 2026-09-28T09:00:00Z)\n"
		if stdout != want {
			t.Errorf("crewflow version wrote %q, want %q", stdout, want)
		}
	})

	t.Run("unknown subcommand", func(t *testing.T) {
		stdout, stderr, code := execute(t, plain, "bogus")
		if code != exitUsage {
			t.Errorf("crewflow bogus = %d, want %d", code, exitUsage)
		}
		if stdout != "" {
			t.Errorf("crewflow bogus wrote %q to stdout, want nothing", stdout)
		}
		if !strings.Contains(stderr, "Usage:") {
			t.Errorf("crewflow bogus wrote %q to stderr, want the usage", stderr)
		}
	})
}

// build compiles the binary into t.TempDir(), with ldflags if given.
func build(t *testing.T, ldflags string) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "crewflow")
	args := []string{"build", "-o", binary}
	if ldflags != "" {
		args = append(args, "-ldflags", ldflags)
	}
	args = append(args, ".")
	if out, err := exec.Command("go", args...).CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return binary
}

// execute runs the binary and returns its stdout, its stderr and its exit code.
func execute(t *testing.T, binary string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errOut bytes.Buffer
	cmd := exec.Command(binary, args...)
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	default:
		t.Fatalf("run %s: %v", binary, err)
	}
	return out.String(), errOut.String(), code
}
