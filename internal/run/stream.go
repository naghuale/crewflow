package run

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// stopGrace is how long the executor is given to stop after it has been asked to,
// before it is killed: an agent that is stopped may want to write down where it
// was, and the journal of that is the only thing left of the run (docs/DESIGN.md §7a).
const stopGrace = 10 * time.Second

// Stream starts a program in dir and writes what it writes to stdout and to stderr
// as it comes, instead of holding it all until the program is done: the journal of a
// run has to hold what the executor did while it is still doing it, because a run
// that is cut short has nothing else to show where it was (docs/DESIGN.md §7).
//
// The extra environment is added to the one of the process and only that: the
// temporary files of the executor are pointed at its own worktree, and nothing here
// hands a secret of the machine to a program that did not have it.
//
// The code is what the program exited with and an error is a program that could not
// be started at all, which is a different thing: a program that was stopped with the
// context is the stop of the run and not a machine that could not run it.
func Stream(ctx context.Context, name string, args []string, dir string, env []string, stdout, stderr io.Writer) (exitCode int, err error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// A context that is done asks the program to stop the way a person asks a
	// program to stop, and kills it only if it does not: the executor of a run that
	// was interrupted has to stop, but it may say how far it got before it does.
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = stopGrace
	var exitErr *exec.ExitError
	switch err = cmd.Run(); {
	case err == nil:
	case errors.As(err, &exitErr):
		exitCode = exitErr.ExitCode()
		err = nil
	case ctx.Err() != nil:
		err = nil
	}
	return exitCode, err
}
