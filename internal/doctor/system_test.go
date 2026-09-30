package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCommandClosesStdin: an agent that waits for an answer to a question must
// not wait for it until the timeout (docs/DESIGN.md §7a). The stdin of the test
// is a pipe that never ends, so a program that inherits it would wait for ever,
// while a program whose stdin is the empty device is over at once.
func TestCommandClosesStdin(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("make a pipe: %v", err)
	}
	defer reader.Close()
	defer writer.Close()
	was := os.Stdin
	os.Stdin = reader
	defer func() { os.Stdin = was }()

	done := make(chan error, 1)
	go func() {
		_, _, _, err := Command(t.Context(), "cat", nil, t.TempDir(), nil)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("cat with a closed stdin returned an error: %v", err)
		}
	case <-time.After(time.Minute):
		t.Fatal("cat with a closed stdin did not end: it read the stdin of the process")
	}
}

// TestCommandTellsTheCodeFromTheError: a program that failed and a program that
// could not be started lead a person to different places, so they are told
// apart.
func TestCommandTellsTheCodeFromTheError(t *testing.T) {
	t.Run("a program that failed", func(t *testing.T) {
		dir := t.TempDir()
		stdout, stderr, code, err := Command(t.Context(), "sh",
			[]string{"-c", "pwd; echo out; echo err >&2; exit 3"}, dir, nil)
		if err != nil {
			t.Errorf("Command returned an error for a program that failed: %v", err)
		}
		if code != 3 {
			t.Errorf("exit code = %d, want 3", code)
		}
		lines := strings.Split(strings.TrimSpace(string(stdout)), "\n")
		// The folder a program sees is the one it was given, spelled the way
		// the host spells it: macOS keeps the symbolic links of /var.
		if len(lines) != 2 || (lines[0] != dir && lines[0] != resolved(t, dir)) || lines[1] != "out" {
			t.Errorf("stdout = %q, want the folder of the run and what the program printed", stdout)
		}
		if got := strings.TrimSpace(string(stderr)); got != "err" {
			t.Errorf("stderr = %q, want what the program printed there", got)
		}
	})

	t.Run("a program that is not there", func(t *testing.T) {
		_, _, _, err := Command(t.Context(), "nosuchprogram-4f2b", nil, t.TempDir(), nil)
		if err == nil {
			t.Error("Command returned no error for a program that could not be started")
		}
	})
}

// TestSystem is the environment a person gets: the programs of PATH and the
// programs of the host.
func TestSystem(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	env := System("crewflow.toml", dir, true, &out)

	if env.ConfigPath != "crewflow.toml" || env.TempDir != dir || !env.Probe {
		t.Errorf("System = %+v, want the file, the folder and the probe it was given", env)
	}
	// The steps of the report and whatever the store of the secrets is waiting for are
	// said to the writer it was given, and the program of this build is the one whose
	// signature the keychain of macOS knows about (docs/DESIGN.md §7i).
	if env.Out != &out {
		t.Errorf("System writes its steps to %v, want the writer it was given", env.Out)
	}
	if env.Executable == "" {
		t.Error("System does not say which program this is, want the one whose signature the keychain remembers")
	}
	path, err := env.LookPath("sh")
	if err != nil {
		t.Fatalf("System did not look into PATH: %v", err)
	}
	if path == "" {
		t.Error("LookPath returned no path for a program every machine has")
	}
	if _, _, code, err := env.Run(t.Context(), path, []string{"-c", "exit 0"}, dir, nil); code != 0 || err != nil {
		t.Errorf("Run of a program that is there = %d, %v, want 0 and no error", code, err)
	}
}

// resolved is the path a program sees, with the symbolic links of the way to it
// resolved: macOS puts /var/folders on the way to the folder of a test.
func resolved(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve %s: %v", path, err)
	}
	return resolved
}
