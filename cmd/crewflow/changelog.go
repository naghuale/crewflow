package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/naghuale/crewflow/internal/changelog"
	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/secret"
)

// runChangelog is `crewflow changelog`: the journal of the project is written by its
// tasks as fragments, and these commands assemble it out of them and say whether the
// journal is what the fragments make (docs/DESIGN.md §6, журнал изменений).
func runChangelog(out *secret.Out, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintf(stderr, "crewflow changelog: nothing to do\n\n")
		usage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "build":
		return runChangelogBuild(args[1:], stdout, stderr)
	case "check":
		return runChangelogCheck(out, args[1:], stdout, stderr)
	case "release":
		return runChangelogRelease(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		usage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "crewflow changelog: unknown subcommand %q\n\n", args[0])
		usage(stderr)
		return exitUsage
	}
}

// runChangelogBuild is `crewflow changelog build`: it writes the unreleased part of the
// journal out of the fragments that stand, in the order the changes were merged, and says
// whether it wrote it. A journal that is already what the fragments build is left as it
// stands — a build that rewrote the same bytes would touch a file of the project for
// nothing, and the next change would see a difference that is not one (CL-005).
func runChangelogBuild(args []string, stdout, stderr io.Writer) int {
	flags := changelogFlags("build", stderr)
	configPath := flags.String("config", defaultConfigPath, "path to crewflow.toml")
	repoDir := flags.String("repo", "", "the checkout to build the journal of, the folder crewflow was called in when empty")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "crewflow changelog build: unexpected argument %q\n\n", flags.Arg(0))
		usage(stderr)
		return exitUsage
	}
	project, err := journalOf(stderr, *configPath, *repoDir)
	if err != nil {
		return changelogFailed(stderr, err)
	}
	written, err := project.Write(context.Background())
	if err != nil {
		return changelogFailed(stderr, err)
	}
	if written {
		fmt.Fprintf(stdout, "changelog: built %s out of every fragment in %s\n", changelog.File, changelog.Dir)
		return exitOK
	}
	fmt.Fprintf(stdout, "changelog: %s is already what the fragments build\n", changelog.File)
	return exitOK
}

// runChangelogCheck is `crewflow changelog check`: every fragment is a fragment, every
// fragment is a task of the project, no two of them say one thing, and the journal is
// what they build. One reason at a time in a line, with the file and the line in it — this
// is the gate of a change that touches the journal, and a gate needs to know what it
// found and where.
func runChangelogCheck(out *secret.Out, args []string, stdout, stderr io.Writer) int {
	flags := changelogFlags("check", stderr)
	configPath := flags.String("config", defaultConfigPath, "path to crewflow.toml")
	repoDir := flags.String("repo", "", "the checkout to check the journal of, the folder crewflow was called in when empty")
	asJSON := flags.Bool("json", false, "print the answer as JSON, for the orchestrator")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "crewflow changelog check: unexpected argument %q\n\n", flags.Arg(0))
		usage(stderr)
		return exitUsage
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return changelogFailed(stderr, err)
	}
	ctx := context.Background()
	set, err := taskRoles(cfg, roleEnv(*configPath, secret.NewNotices(stderr), out))
	if err != nil {
		return changelogFailed(stderr, err)
	}
	project := changelog.Project{
		Root:     checkoutOf(*repoDir),
		Language: changelog.LanguageOf(cfg.Project.Language),
		Order:    changelog.Merged(changelog.Run(gitOfTheJournal), checkoutOf(*repoDir)),
	}
	// The host is asked about the task of every fragment only where there is a host to ask:
	// a project that is not hosted anywhere has no task to confirm, and its journal is
	// checked as far as it goes (§7g).
	asked := 0
	var tasks changelog.Tasks
	if set.Tracker != nil {
		tasks = func(ctx context.Context, number int) error {
			asked++
			_, err := set.Tracker.Task(ctx, number)
			return err
		}
	}
	problems, err := project.Check(ctx, tasks)
	if err != nil {
		return changelogFailed(stderr, err)
	}
	if *asJSON {
		if err := printJSON(stdout, changelog.Answer{Problems: problems}); err != nil {
			return changelogFailed(stderr, err)
		}
	} else if err := printChangelogProblems(stdout, problems, project, asked); err != nil {
		return changelogFailed(stderr, err)
	}
	if len(problems) > 0 {
		return exitFailure
	}
	return exitOK
}

// runChangelogRelease is `crewflow changelog release <version>`: it gives the unreleased
// part of the journal the number and the day of a release, leaves an empty unreleased part
// above it and takes the fragments away. It is one step of scripts/release.sh and nothing
// else: it commits nothing and pushes nothing — a tag is the decision of a person, and a
// program cannot take it back.
func runChangelogRelease(args []string, stdout, stderr io.Writer) int {
	flags := changelogFlags("release", stderr)
	configPath := flags.String("config", defaultConfigPath, "path to crewflow.toml")
	repoDir := flags.String("repo", "", "the checkout to release, the folder crewflow was called in when empty")
	date := flags.String("date", "", "the day of the release as YYYY-MM-DD, today when empty")
	version, code := takeVersion("release", args, flags, stderr)
	if code != exitOK {
		return code
	}
	if _, ok := changelog.Version(version); !ok {
		fmt.Fprintf(stderr, "crewflow changelog release: %q is not a version of the form vMAJOR.MINOR.PATCH\n\n", version)
		usage(stderr)
		return exitUsage
	}
	project, err := journalOf(stderr, *configPath, *repoDir)
	if err != nil {
		return changelogFailed(stderr, err)
	}
	day := *date
	if day == "" {
		// The day of a release is the day it is cut, and the day of a release is written
		// in UTC as the moment of a build is: a machine in another zone cuts the same
		// version on the same day (scripts/release.sh).
		day = time.Now().UTC().Format("2006-01-02")
	}
	cut, err := project.Release(context.Background(), version, day)
	if err != nil {
		return changelogFailed(stderr, err)
	}
	fmt.Fprintf(stdout, "changelog: %s is now %s\n", changelog.File, headingOf(cut))
	if len(cut.Moved) == 0 {
		fmt.Fprintf(stdout, "changelog: no fragment was waiting in %s — nothing moved into the section\n", changelog.Dir)
	}
	for _, fragment := range cut.Moved {
		fmt.Fprintf(stdout, "changelog: moved %s into the section and removed it\n", fragment)
	}
	return exitOK
}

// headingOf is the section of the journal a release left its lines in, as it stands in the
// journal: the version with the day of the release, not as one word of it.
func headingOf(cut changelog.Release) string {
	return "## [" + cut.Version + "] - " + cut.Date
}

// journalOf is the journal of the project the file names and the checkout the journal
// stands in, with the order of the fragments read out of the history of the branch.
//
// The git of it is the git of the machine and no account of anybody: the order of the
// lines comes out of a local checkout and needs no credential, and a command that runs
// inside a run of a task has no right to the account of the orchestrator (§7i).
func journalOf(stderr io.Writer, configPath, repoDir string) (changelog.Project, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return changelog.Project{}, err
	}
	root := checkoutOf(repoDir)
	return changelog.Project{
		Root:     root,
		Language: changelog.LanguageOf(cfg.Project.Language),
		Order:    changelog.Merged(changelog.Run(gitOfTheJournal), root),
	}, nil
}

// gitOfTheJournal is the git a journal is read with: the git of the machine with nothing
// added to the environment of it. The order of the lines comes out of a local checkout and
// needs no credential, and a command that runs inside a run of a task has no right to the
// account of the orchestrator (docs/DESIGN.md §7i).
func gitOfTheJournal(ctx context.Context, name string, args []string, dir string) ([]byte, []byte, int, error) {
	return startProgram(ctx, name, args, dir, nil)
}

// printChangelogProblems is what a check found, for a person: one line for one problem,
// and a line saying that there is none when there is none — a gate that says nothing about
// the fragments of the project has told the person nothing about them.
//
// A line without problems names what the check looked at: the fragments of the tasks. The
// journal is not one of the things it reads — a release writes it, and a task does not
// (docs/DESIGN.md §6, D-066).
func printChangelogProblems(out io.Writer, problems []changelog.Problem, project changelog.Project, asked int) error {
	if len(problems) == 0 {
		count, err := project.Count()
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "changelog: %d fragments, %d tasks asked about, every fragment is a fragment of a task\n",
			count, asked)
		return nil
	}
	for _, problem := range problems {
		fmt.Fprintf(out, "changelog: %v\n", problem)
	}
	fmt.Fprintf(out, "changelog: %d problems; the fragments are the lines of a task, one task one file\n", len(problems))
	return nil
}

func changelogFlags(subcommand string, stderr io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet("changelog "+subcommand, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { usage(stderr) }
	return flags
}

// takeVersion takes the version of a release out of the arguments of the subcommand and
// parses the rest as its flags: a person writes the version first and the flag package of
// Go stops at the first word that is not a flag, so both orders have to mean the same
// thing (as in `crewflow task run`).
func takeVersion(subcommand string, args []string, flags *flag.FlagSet, stderr io.Writer) (string, int) {
	version, rest := "", args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		version, rest = args[0], args[1:]
	}
	if err := flags.Parse(rest); err != nil {
		return "", exitUsage
	}
	switch {
	case version == "" && flags.NArg() == 1:
		version = flags.Arg(0)
	case version == "":
		fmt.Fprintf(stderr, "crewflow changelog %s: which version? a number, as in `crewflow changelog release v0.2.0`\n\n", subcommand)
		usage(stderr)
		return "", exitUsage
	case flags.NArg() > 0:
		fmt.Fprintf(stderr, "crewflow changelog %s: unexpected argument %q\n\n", subcommand, flags.Arg(0))
		usage(stderr)
		return "", exitUsage
	}
	return version, exitOK
}

func changelogFailed(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "crewflow changelog: %v\n", err)
	return exitFailure
}
