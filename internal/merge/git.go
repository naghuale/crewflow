package merge

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/naghuale/crewflow/internal/gate"
	taskrun "github.com/naghuale/crewflow/internal/run"
)

// Runner starts a program in a folder and returns what it wrote and the code it
// exited with. Every way git can say no is an error with the command in it, because a
// person who is told what to run by hand sees what crewflow saw (docs/DESIGN.md §10).
type Runner func(ctx context.Context, name string, args []string, dir string) (stdout, stderr []byte, exitCode int, err error)

// Git is the checkout a merge pushes from and asks the branch of the host about: the
// folder git is started in, the branch the project merges into, the remote it is on
// and the ref the host keeps the head of a change under.
//
// It is given whole, so that a test of a merge runs against a repository of its own
// and reaches no repository of the person who runs the tests, and it is the very
// checkout the gate is given: a review and a merge of one change ask the same history
// the same way (docs/DESIGN.md §7h).
type Git struct {
	// Dir is the checkout git is started in — the folder crewflow was called in, or
	// the one -repo named.
	Dir string
	// Branch is the default branch of the project, and Remote the name of the remote
	// it is on: a project may name another remote, and crewflow names "origin" when
	// it is not told, as everything else of a run does.
	Branch string
	Remote string
	// HeadRef is the ref the host keeps the head of a change under, as `git fetch` is
	// given it. A merge that has to know whether the commit of the head is in the
	// branch already fetches it: a commit the checkout has never heard of is not in
	// the history of anything, and answering "not merged" about a commit nobody
	// fetched would be answering a guess (docs/DESIGN.md §7h).
	HeadRef string
	// Run starts git.
	Run Runner
}

// History is the checkout as the gate is given it, so that the facts of a change are
// read out of the repository the merge pushes from and not out of another one
// (docs/DESIGN.md §7h).
func (g Git) History() gate.History {
	return gate.History{Dir: g.Dir, Branch: g.Branch, Remote: g.Remote, Run: g.Run}
}

// Contains is whether the default branch of the project holds the commit. The branch
// of the host and the ref of the head are fetched first, because the objects are what
// `merge-base` answers about and not what the remote is thought to have
// (docs/DESIGN.md §7h).
func (g Git) Contains(ctx context.Context, commit string) (bool, error) {
	if commit == "" {
		return false, nil
	}
	if err := g.git(ctx, "fetch", "--no-tags", "--quiet", g.remote(), g.Branch); err != nil {
		return false, err
	}
	if g.HeadRef != "" && !g.holds(ctx, commit) {
		if err := g.git(ctx, "fetch", "--no-tags", "--quiet", g.remote(), g.HeadRef); err != nil {
			return false, err
		}
	}
	return g.History().Ancestor(ctx, commit, g.remoteBranch())
}

// Head is the commit the branch of the host is at, as `git ls-remote` says it.
//
// It is the answer a merge is judged on and the only one: the code `git push` exited
// with says nothing about where the branch ended up, and a host that accepted a push
// and put the branch somewhere else is a merge that did not happen
// (docs/DESIGN.md §6, §7h).
func (g Git) Head(ctx context.Context, branch string) (string, error) {
	ref := "refs/heads/" + branch
	stdout, stderr, code, err := g.Run(ctx, "git", []string{"ls-remote", g.remote(), ref}, g.Dir)
	if err != nil {
		return "", fmt.Errorf("git ls-remote %s %s: %w", g.remote(), ref, err)
	}
	if code != 0 {
		return "", fmt.Errorf("git ls-remote %s %s: exited with %d: %s", g.remote(), ref, code, firstLine(stderr))
	}
	for raw := range strings.Lines(string(stdout)) {
		fields := strings.Fields(raw)
		if len(fields) != 2 || fields[1] != ref {
			continue
		}
		return fields[0], nil
	}
	// A branch the host does not have is a commit nobody can fast-forward into: there
	// is nothing to compare a head with, and a merge that went on would be a merge
	// into a branch that is not there.
	return "", fmt.Errorf("the remote %s holds no branch %s: %s", g.remote(), branch, firstLine(stdout, stderr))
}

// Push moves the branch of the project to exactly the given commit: one refspec, one
// argument per field of it, and no `--force` anywhere near it.
//
// The command is given to git as an array of arguments and never as a line of a shell,
// because in a shell `$SHA:refs/heads/main` is a modifier of a variable and not a
// refspec — that is how the pilot nearly pushed the wrong commit into main
// (docs/DESIGN.md §7h, §10).
func (g Git) Push(ctx context.Context, commit string) error {
	refspec := commit + ":refs/heads/" + g.Branch
	_, stderr, code, err := g.Run(ctx, "git", []string{"push", g.remote(), refspec}, g.Dir)
	switch {
	case err != nil:
		return fmt.Errorf("git push %s %s: %w", g.remote(), refspec, err)
	case code != 0:
		return fmt.Errorf("git push %s %s: exited with %d: %s", g.remote(), refspec, code, firstLine(stderr))
	}
	return nil
}

// RemoveWorktree takes the checkout of a task away and prunes what is left of it in
// the bookkeeping of git. A worktree that is not there is taken away as well: the
// state of the task may name a checkout a person has already deleted, and crewflow
// prunes the record of it either way (docs/DESIGN.md §7h).
//
// A worktree that holds work outside the commits of its branch is not taken away, and the
// refusal is the one refusal of every road that takes a worktree away: a merge that went
// through is a merge that went through, and a checkout with the work of a task in it is
// left to the person who reads the warning. The snapshot of the work is not what lets the
// folder go — a snapshot may be incomplete, a path that stays closed is left out of it
// (D-088, docs/DESIGN.md §7a, §7h).
// Output is one question about the checkout answered by git in a folder, and it is how the
// question whether a worktree holds work outside the commits of its branch is asked here as
// it is asked by a run: one question and one answer, whatever the worktree is about to go
// away for (docs/DESIGN.md §7a, §7h).
func (g Git) Output(ctx context.Context, dir string, args ...string) (string, error) {
	stdout, stderr, code, err := g.Run(ctx, "git", args, dir)
	switch {
	case err != nil:
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	case code != 0:
		return "", fmt.Errorf("git %s: exited with %d: %s", strings.Join(args, " "), code, firstLine(stderr))
	}
	return string(stdout), nil
}

func (g Git) RemoveWorktree(ctx context.Context, path string) error {
	if _, err := os.Stat(path); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		g.prune(ctx)
		return nil
	}
	work, err := taskrun.UncommittedIn(ctx, g, path)
	if err != nil {
		return err
	}
	if work.Any() {
		return fmt.Errorf("the worktree %s holds %d files outside its commits, "+
			"and crewflow does not take it away: %w", path, work.Count(), taskrun.ErrUncommittedWork)
	}
	_, stderr, code, err := g.Run(ctx, "git", []string{"worktree", "remove", path}, g.Dir)
	if err != nil {
		return fmt.Errorf("git worktree remove %s: %w", path, err)
	}
	if code != 0 {
		return fmt.Errorf("git worktree remove %s: exited with %d: %s", path, code, firstLine(stderr))
	}
	g.prune(ctx)
	return nil
}

// prune is the bookkeeping of git after a worktree is gone. Whatever it says is not
// worth a word in the outcome: what a person has to do by hand is the checkout that is
// still on the disk, and the record of it in git is not it (docs/DESIGN.md §7h).
func (g Git) prune(ctx context.Context) {
	_, _, _, _ = g.Run(ctx, "git", []string{"worktree", "prune"}, g.Dir)
}

// holds is whether the checkout has the commit at all: a question of its own, because
// an object git has never seen is not in the history of anything.
func (g Git) holds(ctx context.Context, commit string) bool {
	_, _, code, err := g.Run(ctx, "git", []string{"cat-file", "-e", commit + "^{commit}"}, g.Dir)
	return err == nil && code == 0
}

// remoteBranch is the branch of the project as it is on the remote, which is the form
// a fetch leaves it in.
func (g Git) remoteBranch() string {
	return g.remote() + "/" + g.Branch
}

// remote is the remote of the project, and "origin" when nobody named another.
func (g Git) remote() string {
	if g.Remote == "" {
		return "origin"
	}
	return g.Remote
}

// git runs one command of git and returns a refusal with the command in it when git
// said no.
func (g Git) git(ctx context.Context, args ...string) error {
	_, err := g.Output(ctx, g.Dir, args...)
	return err
}

// firstLine is the first line that says something of what a program wrote: a refusal
// is read by people and the whole of what git wrote may be long.
func firstLine(outputs ...[]byte) string {
	for _, output := range outputs {
		for line := range strings.Lines(string(output)) {
			if text := strings.TrimSpace(line); text != "" {
				return text
			}
		}
	}
	return ""
}

// isNotExist is whether a file is not there, said once for the ways the packages of
// crewflow ask it.
func isNotExist(err error) bool {
	return err != nil && os.IsNotExist(err)
}
