package gate

import (
	"context"
	"fmt"
	"strings"
)

// History is the checkout a review asks about the history of a change: a working
// copy of the repository and the way git is started in it. It is given whole, so
// that a test of a review runs against a repository of its own and reaches no
// repository of the person who runs the tests (docs/DESIGN.md §7h).
type History struct {
	// Dir is the checkout git is started in — the folder crewflow was called in,
	// or the one -repo named.
	Dir string
	// Branch is the default branch of the project, and Remote the name of the
	// remote it is on. A project may name another remote; crewflow names
	// "origin" when it is not told, as everything else of a run does.
	Branch string
	Remote string
	// Run starts git and returns what it wrote and the code it exited with. Every
	// way git can say no is an error with the command in it, because a person who
	// is told what to run by hand sees what crewflow saw (docs/DESIGN.md §10).
	Run func(ctx context.Context, name string, args []string, dir string) (stdout, stderr []byte, exitCode int, err error)
}

// errNoGit is what a machine that cannot start git answers: a machine without a git
// cannot say whether a commit is an ancestor of another one, and a gate that took
// the silence for a yes would merge a change on a guess (docs/DESIGN.md §7h).
var errNoGit = fmt.Errorf("git: this machine has no way to start git, so the history of the change cannot be read")

// The words git says, and what they mean here. They are the only two answers
// `git merge-base --is-ancestor` has: yes, no, and a question it could not answer
// at all.
const (
	// ancestorYes is what git exits with when the first commit is an ancestor of
	// the second one.
	ancestorYes = 0
	// ancestorNo is what it exits with when it is not.
	ancestorNo = 1
)

// Prepare brings the objects a review asks about into the checkout: the head of the
// change, under the ref the host names it by, and the branch it is meant for. It
// asks for nothing to be written into the history of the person — a fetch updates
// what a fetch always updates — and it is done before anything is judged, because
// `merge-base` answers about the objects there are and not about the ones the
// remote has (docs/DESIGN.md §7h).
func (h History) Prepare(ctx context.Context, ref, head string) error {
	if h.Run == nil {
		return errNoGit
	}
	if h.Branch == "" {
		return fmt.Errorf("git: the project names no default branch, so the history of the change cannot be read")
	}
	if err := h.git(ctx, "fetch", "--no-tags", "--quiet", h.remote(), h.Branch); err != nil {
		return err
	}
	if ref == "" {
		return fmt.Errorf("the host names no ref for the head of the change, so its commit cannot be fetched")
	}
	if err := h.git(ctx, "fetch", "--no-tags", "--quiet", h.remote(), ref); err != nil {
		return err
	}
	got, err := h.output(ctx, "rev-parse", "FETCH_HEAD^{commit}")
	if err != nil {
		return err
	}
	if !sameCommit(strings.TrimSpace(got), head) {
		return fmt.Errorf("the head of the change moved while it was being read: the host says %s, and %s is what its ref points at",
			head, strings.TrimSpace(got))
	}
	return nil
}

// Ancestor is whether the commit of the first name is an ancestor of the one of the
// second, as the objects of the checkout hold it.
//
// A commit the checkout has never heard of is not an ancestor of anything: a commit
// that was force-pushed away is not in the history of the head, and that is what
// `history-rewritten` is about. Anything else git could not answer is not an
// answer, and an unanswerable question is a refusal (docs/DESIGN.md §7h).
func (h History) Ancestor(ctx context.Context, of, by string) (bool, error) {
	if h.Run == nil {
		return false, errNoGit
	}
	_, stderr, code, err := h.Run(ctx, "git", []string{"merge-base", "--is-ancestor", of, by}, h.Dir)
	switch {
	case err != nil:
		return false, fmt.Errorf("git merge-base --is-ancestor %s %s: %w", of, by, err)
	case code == ancestorYes:
		return true, nil
	case code == ancestorNo, !h.has(ctx, of):
		return false, nil
	default:
		return false, fmt.Errorf("git merge-base --is-ancestor %s %s: exited with %d: %s",
			of, by, code, firstLine(stderr))
	}
}

// has is whether the checkout holds the commit at all, which is a question of its
// own: an object git has never seen is a commit the host has written down and this
// machine has not fetched, and it is not an ancestor of the head whatever else it
// is.
func (h History) has(ctx context.Context, commit string) bool {
	if h.Run == nil {
		return false
	}
	_, _, code, err := h.Run(ctx, "git", []string{"cat-file", "-e", commit + "^{commit}"}, h.Dir)
	return err == nil && code == 0
}

// DefaultIsAncestor is whether the default branch of the project is an ancestor of
// the head of the change: a merge that is not a fast-forward is not a merge
// crewflow does (docs/DESIGN.md §7h).
func (h History) DefaultIsAncestor(ctx context.Context, head string) (bool, error) {
	return h.Ancestor(ctx, h.remoteBranch(), head)
}

// remoteBranch is the branch of the project as it is on the remote, which is the
// form a fetch leaves it in: what a reviewer judges against is the branch of the
// host and not the copy of it in a checkout, which may be a week old.
func (h History) remoteBranch() string {
	return h.remote() + "/" + h.Branch
}

// remote is the remote of the project, and "origin" when nobody named another.
func (h History) remote() string {
	if h.Remote == "" {
		return "origin"
	}
	return h.Remote
}

// git runs one command of git and returns what it wrote, and a refusal with the
// command in it when git said no.
func (h History) git(ctx context.Context, args ...string) error {
	_, err := h.output(ctx, args...)
	return err
}

// output is one command of git and the first line of what it wrote on the way out,
// for the commands whose answer crewflow reads.
func (h History) output(ctx context.Context, args ...string) (string, error) {
	stdout, stderr, code, err := h.Run(ctx, "git", args, h.Dir)
	command := "git " + strings.Join(args, " ")
	switch {
	case err != nil:
		return "", fmt.Errorf("%s: %w", command, err)
	case code != 0:
		return "", fmt.Errorf("%s: exited with %d: %s", command, code, firstLine(stderr))
	}
	return string(stdout), nil
}

// firstLine is the first line that says something, which is what a refusal shows:
// the whole of what a program wrote may be long, and a refusal is read by people.
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
