package doctor

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/naghuale/crewflow/internal/buildinfo"
	"github.com/naghuale/crewflow/internal/config"
)

// The two lines of a report about the program on the machine: what this build is, and how
// far it is behind the `main` of the repository it was made from.
//
// They are asked early — after the file of the project and git, and before anything of the
// roles of the project is read — because they answer the question §5 asks of a machine: is
// the program that is going to act the one the project relies on (F-178).
const (
	// buildCheck is what this build says of itself: the version, the commit and the
	// capabilities of its code, and what of them the project requires.
	buildCheck = "installed build"
	// mainCheck is how far this build is behind the `main` of the repository it was made
	// from — the question nobody asked the machine on 03.10, when the build standing on it
	// was older than the repository (F-178).
	mainCheck = "build and main"
	// unknownCommit is what a build says when it was made without the link-time stamp, and
	// it names no commit of anything: there is nothing to compare it with.
	unknownCommit = "unknown"
)

// build is the line of a report about the program on the machine: which build of crewflow
// this is, and whether it has what the file of the project requires of it.
//
// A project that requires a mechanism the installed build has not got is a machine crewflow
// cannot work on, and the report fails on it with the same word a command refuses with, out
// of the same place: one answer for a person in `doctor` and in the command that would have
// acted (docs.DESIGN.md §5, F-178).
func (c *checker) build(ctx context.Context, cfg config.Config) {
	if refused := buildinfo.Require(cfg.Crewflow.Requires); refused != nil {
		c.add(Check{
			Name:   buildCheck,
			Status: Fail,
			Detail: refused.Error(),
			Hint: fmt.Sprintf("the capabilities of [crewflow] requires in %s are what this project needs of the "+
				"program that runs it; install a build of crewflow that has them and check again",
				c.env.ConfigPath),
		})
		return
	}
	detail := buildinfo.String() + "; capabilities: " + strings.Join(buildinfo.Capabilities(), ", ")
	if required := cfg.Crewflow.Requires; len(required) > 0 {
		detail += "; the project requires " + strings.Join(required, ", ")
	} else {
		detail += "; the project requires nothing of it"
	}
	fromMain, about := c.mainOfTheBuild(ctx)
	c.add(Check{Name: buildCheck, Status: OK, Detail: detail + "; " + fromMain})
	if about != nil {
		about.Name = mainCheck
		c.add(*about)
	}
}

// mainOfTheBuild is how far the installed build is behind the `main` of the repository it
// was made from — one line for the report whatever comes of it, and the check of its own
// only where the question has an answer at all.
//
// It is asked of the repository in the folder crewflow was called in and of nothing else: no
// fetch, no host, and a person who wants it fetched runs `git fetch` himself. It is not asked
// where the repository does not hold the commit the build names, because that is what makes
// a repository the one a build was made from: everywhere else the line says `unknown` and no
// check is made at all, since a check in a project of another repository would answer about
// a machine nobody asked (F-178, MODEL: UNKNOWN is not success).
func (c *checker) mainOfTheBuild(ctx context.Context) (string, *Check) {
	const unknown = "behind main: unknown"
	commit := buildinfo.Commit
	git, err := c.env.LookPath("git")
	if commit == unknownCommit || err != nil || !c.said(ctx, git, "cat-file", "-e", commit+"^{commit}") {
		return unknown, nil
	}
	main, read := c.tip(ctx, git, "refs/remotes/origin/main")
	switch {
	case !read:
		return unknown, &Check{
			Status: Warn,
			Detail: "unknown: this repository holds no refs/remotes/origin/main, and without the network " +
				"crewflow cannot tell how far the build is behind main",
			Hint: "fetch it in this repository (`git fetch`) and check again: nothing here says the build is up to date",
		}
	case main == commit:
		return "up to date with main (" + commit + ")", &Check{
			Status: OK,
			Detail: "up to date: the build is the tip of main this repository holds (" + commit + ")",
		}
	case !c.said(ctx, git, "merge-base", "--is-ancestor", commit, main):
		return unknown, &Check{
			Status: Warn,
			Detail: fmt.Sprintf("unknown: the build %s is no ancestor of the main %s this repository holds, "+
				"so it is neither behind it nor up to date", commit, main),
			Hint: "the history of main was rewritten, or the build was made of a branch: " +
				"crewflow will not guess which of the two it is",
		}
	default:
		behind := fmt.Sprintf("behind main by %s commits (the build is %s, main is %s)",
			c.commitsBetween(ctx, git, commit, main), commit, main)
		return behind, &Check{
			Status: Warn,
			Detail: behind,
			Hint: "install a build of the main of this repository: what is merged into it is not on the " +
				"machine, and the mechanisms of the project are merged",
		}
	}
}

// tip is the commit a ref of this repository stands at, and whether the ref is there at all:
// a repository that was never fetched holds no `origin/main`, and a ref nobody fetched is a
// fact about the machine and not a failure of it.
func (c *checker) tip(ctx context.Context, git, ref string) (string, bool) {
	stdout, _, code, err := c.run(ctx, git, "rev-parse", "--verify", "-q", ref)
	if code != 0 || err != nil {
		return "", false
	}
	tip := strings.TrimSpace(string(stdout))
	return tip, tip != ""
}

// commitsBetween is how many commits stand between the build and the tip of main, as git
// counts them, and a refusal in words where it could not: the sentence is about how far
// behind the build is, and a number made up there is a number a person acts on.
func (c *checker) commitsBetween(ctx context.Context, git, commit, main string) string {
	stdout, _, code, err := c.run(ctx, git, "rev-list", "--count", commit+".."+main)
	if code != 0 || err != nil {
		return "an unknown number of"
	}
	if commits := strings.TrimSpace(string(stdout)); commits != "" {
		if _, convErr := strconv.Atoi(commits); convErr == nil {
			return commits
		}
	}
	return "an unknown number of"
}

// said is whether a command of git answered that it was done. `--is-ancestor` writes nothing
// and says everything in its code, which is why it is asked and not read (docs.DESIGN.md §7h).
func (c *checker) said(ctx context.Context, git string, args ...string) bool {
	_, _, code, err := c.run(ctx, git, args...)
	return code == 0 && err == nil
}
