package doctor

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/naghuale/crewflow/internal/secret"
)

// System returns the environment of the machine this process runs on: the
// programs of PATH, the given project file, the home of the person and, for the
// probe, the given temporary folder.
func System(configPath, tempDir string, probe bool) Env {
	// A machine that cannot say where the home of the person is has no folder crewflow
	// may resolve a "~" of the file of a project to, and the report says so rather
	// than guessing one.
	home, _ := os.UserHomeDir()
	return Env{
		LookPath:   exec.LookPath,
		Run:        Command,
		ConfigPath: configPath,
		Home:       home,
		TempDir:    tempDir,
		Probe:      probe,
		// A project whose executor works as an app of its own is checked through the
		// store of the machine, over the HTTP of the machine, with the clock of it: a
		// report that asks anything else of the machine it runs on is a report of the
		// machine the tests of crewflow happen to run on (docs/DESIGN.md §7i).
		Secrets: secret.System(),
		HTTP:    &http.Client{Timeout: 30 * time.Second},
		Now:     time.Now,
	}
}

// Command runs a program in dir and returns what it wrote and the code it
// exited with. A program that could not be started at all is an error, and a
// program that failed is a code: doctor tells those apart, because "git is not
// installed" and "git failed" lead a person to different places.
//
// The stdin of the program is the empty device, on purpose. An agent that waits
// for an answer to a question would otherwise wait for it until the timeout
// (docs/DESIGN.md §7a).
//
// The extra environment is added to the one of the process and only that: a role
// of a project tells its system which host to talk to, and nothing here hands a
// secret of the machine to a program that did not have it.
func Command(ctx context.Context, name string, args []string, dir string, extraEnv []string) (stdout, stderr []byte, exitCode int, err error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	var exitErr *exec.ExitError
	switch err = cmd.Run(); {
	case err == nil:
	case errors.As(err, &exitErr):
		exitCode = exitErr.ExitCode()
		err = nil
	}
	return out.Bytes(), errOut.Bytes(), exitCode, err
}
