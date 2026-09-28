// Command crewflow develops with two roles: an orchestrator that plans, reviews
// and merges, and an executor that writes the code of one task at a time.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/naghuale/crewflow/internal/buildinfo"
)

// Exit codes. Two says that crewflow was called wrong, which a script can tell
// from a run that did what it was told; one says that the machine is not ready
// for a task.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches the subcommand in args and returns the exit code, so that the
// whole program can be tested in process.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "doctor":
		return runDoctor(args[1:], stdout, stderr)
	case "task":
		return runTask(args[1:], stdout, stderr)
	case "version":
		return runVersion(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		usage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "crewflow: unknown subcommand %q\n\n", args[0])
		usage(stderr)
		return exitUsage
	}
}

// runVersion prints the version, the commit and the build time that were stamped
// into the binary at link time.
func runVersion(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("version", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { usage(stderr) }
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "crewflow version: unexpected argument %q\n\n", flags.Arg(0))
		usage(stderr)
		return exitUsage
	}
	fmt.Fprintln(stdout, buildinfo.String())
	return exitOK
}

// usage lists the subcommands: a check of the machine, a run of a task, what a check
// of a task is missing before it is run, a watch of a run that is going on, a list of
// the runs of the project, and the version. The rest of the cycle arrives with the
// next steps of docs/DESIGN.md §11.
func usage(w io.Writer) {
	fmt.Fprint(w, `crewflow develops with two roles: an orchestrator that plans, reviews and
merges, and an executor that writes the code of one task at a time.

Usage:
  crewflow <subcommand> [flags]

Subcommands:
  doctor         Check that this machine is ready for a task (docs/DESIGN.md §7d)
  task run <N>   Run one task in a worktree of its own, and say how it ended (§6)
  task check <N> Say whether a task may be run, and what it is missing when it may not
  task watch <N> Show what the executor of a task is doing, as it is doing it
  task list      List the runs of the project, the ones that are going on top
  help           Show this message
  version        Print the version, the commit and the build time

  crewflow task run <N> [-config path] [-repo path] [-continue "message"] [-json]
  crewflow task check <N> [-config path] [-json]
  crewflow task watch <N> [-config path] [-attempt K]
  crewflow task list [-config path] [-repo owner/name] [-json] [-all]
`)
}
