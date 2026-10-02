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
	case "review":
		return runReview(args[1:], stdout, stderr)
	case "merge":
		return runMerge(args[1:], stdout, stderr)
	case "changelog":
		return runChangelog(args[1:], stdout, stderr)
	case "verify":
		return runVerify(args[1:], stdout, stderr)
	case "auth":
		return runAuth(args[1:], stdout, stderr)
	case "network":
		return runNetwork(args[1:], stdout, stderr)
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

// usage lists the subcommands: a check of the machine, a run of a task, a continuation
// of a run that was stopped at a decision of a person, what a check of a task is missing
// before it is run, the record of the admission of a pair of tasks to run beside each
// other, a watch of a run that is going on, a list of the runs of the project, a
// queue of what of them wants a person right now, a check of the runs that stand, a review
// of a change, the merge of a change the gate allows and the check of a merge that went
// through, the journal of the project out of the fragments its tasks leave, the key of the
// app a run works as, and the version.
// The rest of the cycle arrives with the next steps of docs/DESIGN.md §11.
func usage(w io.Writer) {
	fmt.Fprint(w, `crewflow develops with two roles: an orchestrator that plans, reviews and
merges, and an executor that writes the code of one task at a time.

Usage:
  crewflow <subcommand> [flags]

Subcommands:
  doctor         Check that this machine is ready for a task (docs/DESIGN.md §7d)
  task run <N>   Run one task in a worktree of its own, and say how it ended (§6)
  task resume <N> Go on from the point a run stood at, after a person did what only he can (§7i)
  task check <N> Say whether a task may be run, and what it is missing when it may not
  task admit <A> <B>  Write the record of the admission of a pair to run beside (§7c)
  task watch <N> Show what the executor of a task is doing, as it is doing it
  task list      List the runs of the project, and of the whole machine with -all
  task attention The tasks of the project that want a person right now, the whole
                  machine with -all (§6a)
  task check-stalled  The runs that stand, and one record under each task (§6)
  review <PR>    Say whether a change may be merged, and why it may not (§7h)
  merge <PR>     Merge a change the gate allows: fast-forward of the approved commit (§7h)
  verify <PR>    Check a merge: the branch of the host, the task and the CI of it (§7h)
  changelog build  Build the unreleased part of the journal out of the fragments (§6)
  changelog check  Check the fragments and the journal of a change (§6)
  auth app       The GitHub Apps a run and an orchestrator work as: import a key, check it (§7i)
  network mode   How the programs of crewflow go out to the network: direct, proxy or
                  fallback (§7d)
  network proxy  The named proxies of the project: list, add, edit, use, remove, test (§7d)
  help           Show this message
  version        Print the version, the commit and the build time

  crewflow task run <N> [-config path] [-repo path] [-continue "message"] [-json]
  crewflow task resume <N> [-config path] [-repo path] [-json]
  crewflow task check <N> [-config path] [-json]
  crewflow task admit <A> <B> [-c K2=pass[:reason]]… [-owner-exception "why"] [-config path] [-repo path] [-json]
  crewflow task watch <N> [-config path] [-attempt K]
  crewflow task list [-all] [-json] [-config path] [-repo path]
  crewflow task attention [-all] [-json] [-config path]
  crewflow task check-stalled [-json] [-config path]
  crewflow review <PR> [-config path] [-repo path] [-approve | -request-changes <file>] [-json]
  crewflow merge <PR> [-config path] [-repo path] [-json]
  crewflow verify <PR> [-config path] [-repo path] [-json]
  crewflow changelog build [-config path] [-repo path]
  crewflow changelog check [-config path] [-repo path] [-json]
  crewflow changelog release <version> [-config path] [-repo path] [-date YYYY-MM-DD]
  crewflow auth app import <file.pem> [-config path] [-as executor|orchestrator]
  crewflow auth app check [-config path] [-as executor|orchestrator] [-json]
  crewflow auth git-credential [get] [-config path] [-as executor|orchestrator]
  crewflow network proxy list [-json] [-config path]
  crewflow network proxy add <name> -type http|https|socks5 -host H -port P [-credentials none|secret-store]
  crewflow network proxy edit <name> [-type T] [-host H] [-port P] [-credentials none|secret-store]
  crewflow network proxy use <name> [-config path]
  crewflow network proxy remove <name> [-config path]
  crewflow network proxy credentials <name> set -file <path> | remove | status
  crewflow network proxy test [name] [-json] [-config path]
  crewflow network mode direct|proxy|fallback [-config path]
  crewflow doctor network [-json] [-config path]

Pages of the project, where the words of a refusal and the rest are written:
  docs/DESIGN.md §7i           how a run works as a GitHub App and what signs its push
  docs/help/old-app-tokens.md  tokens of an app left in the keychain of macOS by the builds
                               before the list of helpers of git is reset, and how the owner
                               removes them by hand
`)
}
