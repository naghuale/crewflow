package doctor

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
)

// System returns the environment of the machine this process runs on: the
// programs of PATH, the given project file and, for the probe, the given
// temporary folder.
func System(configPath, tempDir string, probe bool) Env {
	return Env{
		LookPath:   exec.LookPath,
		Run:        Command,
		ConfigPath: configPath,
		TempDir:    tempDir,
		Probe:      probe,
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
func Command(ctx context.Context, name string, args []string, dir string) (stdout, stderr []byte, exitCode int, err error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
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
