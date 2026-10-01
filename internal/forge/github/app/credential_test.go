package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheHelperOfGitIsThisBuildAndSaysWhoseTokenItSigns is F-082 (01.10.2026): the helper
// git is given was the name of crewflow without a `!` before it, so git looked for
// `git-credential-crewflow`, found nothing, and refused a push the gate had already
// allowed. The line names the program by its own path — the build that judged the gate
// and not whatever PATH of whoever runs it holds — and says whose token it signs, for
// both subjects of a project (docs/DESIGN.md §7h, §7i).
func TestTheHelperOfGitIsThisBuildAndSaysWhoseTokenItSigns(t *testing.T) {
	program, err := os.Executable()
	if err != nil {
		t.Fatalf("the program of this build: %v", err)
	}
	if !filepath.IsAbs(program) {
		t.Fatalf("os.Executable() = %q, want a path a person could run", program)
	}
	cases := []struct {
		name string
		role Role
	}{
		{name: "the push of a run", role: Executor},
		{name: "the push of a merge", role: Orchestrator},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			helper, err := credentialHelper(tc.role)
			if err != nil {
				t.Fatalf("credentialHelper(%q) returned an error: %v", tc.role, err)
			}
			if !strings.HasPrefix(helper, "!") {
				t.Errorf("the helper is %q, want it to begin with %q: without it git looks for a program named after it",
					helper, "!")
			}
			if !strings.Contains(helper, program) {
				t.Errorf("the helper is %q, want it to name the program of this build %q", helper, program)
			}
			if want := "auth git-credential -as " + string(tc.role); !strings.Contains(helper, want) {
				t.Errorf("the helper is %q, want it to say %q", helper, want)
			}
		})
	}
}

// TestGitCallsTheHelperOfThisBuild runs a real git in a folder of the test and watches it
// start the helper the way it starts the one crewflow writes. The line handed to git is
// the line of the code above with the program of this build swapped for a helper the test
// wrote — the rest of it, the `!`, the path and the role, is what the code writes and what
// git is asked to run.
//
// Without this the shape above is only words: what is proved here is that a git reads
// this value as a command to start, and starts it (docs/DESIGN.md §7i).
func TestGitCallsTheHelperOfThisBuild(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not installed: %v", err)
	}
	helper, err := credentialHelper(Orchestrator)
	if err != nil {
		t.Fatalf("credentialHelper returned an error: %v", err)
	}
	program, err := os.Executable()
	if err != nil {
		t.Fatalf("the program of this build: %v", err)
	}
	// A helper the test can watch: it writes what it was asked and answers with the
	// credentials of the test, which are not the credentials of anybody.
	called := filepath.Join(t.TempDir(), "asked.txt")
	script := filepath.Join(t.TempDir(), "helper.sh")
	writeHelper(t, script, called)
	line := strings.Replace(helper, program, script, 1)
	if line == helper {
		t.Fatalf("the helper %q does not name the program of this build %q", helper, program)
	}

	repo := t.TempDir()
	git(t, repo, "init", "--quiet")
	filled := git(t, repo,
		// A list of helpers is a list: the settings of the machine are reset before
		// crewflow's own, the way the push of a merge resets them (§7i), and the answer
		// below is the one of crewflow's helper and of nothing else.
		"-c", credentialHelperKey+"=",
		"-c", credentialHelperKey+"="+line,
		"credential", "fill")

	if !strings.Contains(filled, "password=ghs_token_of_the_test") {
		t.Errorf("git credential fill wrote %q, want the password the helper of the test answers with", filled)
	}
	asked := read(t, called)
	for _, want := range []string{"-as orchestrator", "get", "host=github.com"} {
		if !strings.Contains(asked, want) {
			t.Errorf("the helper was asked with %q, want it to hold %q", asked, want)
		}
	}
}

// assertHelperOfThisBuild is what the settings of git of a worktree have to hold: the
// helper is this very build, named by its path, and it is told whose token it signs. The
// two words are F-082 (01.10.2026), and every subject of a project that pushes goes
// through a line like this one (docs/DESIGN.md §7h, §7i).
func assertHelperOfThisBuild(t *testing.T, settings map[string]string, role string) {
	t.Helper()
	program, err := os.Executable()
	if err != nil {
		t.Fatalf("the program of this build: %v", err)
	}
	helper := settings[credentialHelperKey]
	if !strings.HasPrefix(helper, "!") {
		t.Errorf("credential.helper = %q, want it to begin with %q: without it git looks for a program named after it",
			helper, "!")
	}
	if !strings.Contains(helper, program) {
		t.Errorf("credential.helper = %q, want it to name the program of this build %q", helper, program)
	}
	if want := "auth git-credential -as " + role; !strings.Contains(helper, want) {
		t.Errorf("credential.helper = %q, want it to say %q", helper, want)
	}
}

// writeHelper writes the helper of the test: it records what git asked it and answers
// with the credentials of the test, so that git has an answer and the test has a record.
func writeHelper(t *testing.T, path, record string) {
	t.Helper()
	script := "#!/bin/sh\n" +
		"asked=$(cat)\n" +
		"{ echo \"arguments: $*\"; echo \"asked: $asked\"; } > '" + record + "'\n" +
		"echo 'username=x-access-token'\n" +
		"echo 'password=ghs_token_of_the_test'\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write the helper of the test: %v", err)
	}
}

// git runs one git in the folder of the test, with the home of a folder of the test under
// it: nothing of the machine is in it, and a helper of the machine cannot answer instead
// of the one of the test.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	command.Stdin = strings.NewReader("protocol=https\nhost=github.com\n\n")
	command.Env = append(os.Environ(),
		"HOME="+filepath.Join(t.TempDir(), "home"),
		"XDG_CONFIG_HOME="+filepath.Join(t.TempDir(), "xdg"),
	)
	out, err := command.Output()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, firstLine(err))
	}
	return string(out)
}

// firstLine is what a program wrote about its failure, which is a test wants to read
// rather than the code of the exit alone.
func firstLine(err error) string {
	failed, is := err.(*exec.ExitError)
	if !is {
		return ""
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(failed.Stderr)), "\n")
	return line
}

// read is what a file holds, for a test that looks at what a helper was asked.
func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
