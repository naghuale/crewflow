package run

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/naghuale/crewflow/internal/forge"
)

// identityOf is whose name the executor of this run works under (docs/DESIGN.md §7i).
//
// It is the host of the code of the project that answers, because a host is what
// knows what accounts of its own an executor can work as: the executor of a run in the
// mode of the bot is handed a token of a GitHub App, and the executor of a run in the
// mode of the owner is handed nothing because the login of the person is what it
// already has. A host that cannot answer is a run that is not started: a run that went
// on as somebody else is a run whose journal, whose state and whose report say the
// wrong thing about who worked under whose name.
func (r *runner) identityOf(ctx context.Context) (forge.Identity, error) {
	if r.set.Forge == nil {
		// A run without a host has no account of a host to work as, and the words of
		// that are the same for every project: it works as the person who runs
		// crewflow, and nothing of a bot of a host is asked of anybody (§7g).
		return forge.Identity{
			Mode:        forge.ModeOwner,
			Description: "owner — the person who runs crewflow (shared rights)",
		}, nil
	}
	identity, err := forge.IdentityOf(ctx, r.set.Forge)
	if err != nil {
		return forge.Identity{}, fmt.Errorf("the identity of the executor: %w", err)
	}
	if identity.Description == "" {
		// Every report of a run shows this line, and a line that says nothing is a
		// line a person reads twice without learning anything.
		identity.Description = identity.Mode
	}
	return identity, nil
}

// bot sets the worktree of the task up for an executor that works as an account of
// the host of its own, and does nothing for the mode of the owner: a run of the owner
// pushes with the login of the person, which is what crewflow has always done
// (docs/DESIGN.md §7i).
//
// Two things go into the worktree: the helper git takes its credentials from, because a
// run of an agent is longer than the life of a token and git asks for a password every
// time it pushes; and the hook that lets nothing but the branch of the task through, as
// a second line of defence behind the rules of the branch of the host.
func (r *runner) bot(ctx context.Context) error {
	if r.identity.Mode != forge.ModeBot {
		return nil
	}
	// The settings of git in a worktree are the ones of that worktree alone, and git
	// keeps them in a file of its own only where the repository says it does. The
	// setting is in the shared configuration of the repository — which is where
	// crewflow already writes the local ignore of the scratch of a run (§7a) — and it
	// is written once, whatever a run of this project is asked for later.
	if err := r.git(ctx, r.repoDir(), "config", "extensions.worktreeConfig", "true"); err != nil {
		return err
	}
	for key, value := range r.identity.GitConfig {
		if err := r.git(ctx, r.worktree, "config", "--worktree", key, value); err != nil {
			return fmt.Errorf("the settings of git in the worktree: %w", err)
		}
	}
	return r.hook(ctx)
}

// hook is the pre-push hook of the worktree of the task: it lets the branch of the
// task through and refuses everything else, so that an executor that was talked into
// pushing into the default branch is refused by the machine as well and not only by
// the words of the task (docs/DESIGN.md §7i).
//
// It is a guard and not the guard: the rules of the branch of the host are what an App
// cannot go around, and a hook is a script of the machine of a person that a person
// may delete. It refuses a push only from a worktree of a run — the common directory of
// a linked worktree holds its name — because the same hooks folder is the one of the
// checkout of the person, and their own pushes are not a run of crewflow.
func (r *runner) hook(ctx context.Context) error {
	common, err := r.commonDir(ctx)
	if err != nil {
		return err
	}
	folder := filepath.Join(common, "hooks")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return fmt.Errorf("make %s: %w", folder, err)
	}
	return os.WriteFile(filepath.Join(folder, "pre-push"), []byte(prePush(r.branch)), 0o700)
}

// commonDir is the directory git holds the objects, the references and the hooks of
// every worktree of the repository in, as this machine spells it: a relative answer is
// resolved against the folder it was asked in, because a path built out of nothing
// would be written where crewflow happens to be called from (§7a).
func (r *runner) commonDir(ctx context.Context) (string, error) {
	asked, err := r.output(ctx, r.worktree, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	common := strings.TrimSpace(asked)
	switch {
	case common == "":
		return "", fmt.Errorf("git rev-parse --git-common-dir: named no repository, so there is nowhere to keep the hooks of the run")
	case filepath.IsAbs(common):
		return common, nil
	default:
		return filepath.Join(r.worktree, common), nil
	}
}

// prePush is the hook crewflow writes into the worktree of a run. It reads the lines
// git writes to a pre-push hook — the local reference, its two shas and the reference
// on the host, four words to a line — and refuses every push of anything but the
// branch of the task, a deletion of it among them: a branch of a run is pushed and
// fetched, and deleting it is what a rewrite of a task's history looks like from the
// host (§7h).
func prePush(branch string) string {
	allowed := "refs/heads/" + branch
	return `#!/bin/sh
# Written by crewflow before a run: the executor of a task pushes the branch of
# that task and nothing else (docs/DESIGN.md §7i). The rules of the branch on the
# host are what an app cannot go around; this hook refuses a push from a worktree
# of a run, and says nothing about the checkout of the person.
branch='` + allowed + `'
case "$GIT_DIR" in
*/worktrees/*) ;;
*) exit 0 ;;
esac
while read -r local localSHA remote remoteSHA; do
	if [ "$local" = '(delete)' ] || [ "$remote" != "$branch" ]; then
		echo "crewflow: the executor of a task pushes only $branch, not $remote" >&2
		exit 1
	fi
done
exit 0
`
}

// openRequest is the change request of a run whose executor did not open one, and
// nothing for every other run (docs/DESIGN.md §7i).
//
// A run in the mode of the bot whose branch is on the host and whose token is over —
// gh had an hour, and a run of an agent is longer — has work that nobody asked about,
// and the account of the app may open the request for it. A run in the mode of the
// owner has no such account, and a branch without a request stays a run whose outcome
// is no-change-request, whatever it holds.
func (r *runner) openRequest(ctx context.Context) (*forge.ChangeRequest, error) {
	if r.set.Opener == nil || r.identity.Mode != forge.ModeBot {
		return nil, nil
	}
	pushed, err := r.pushed(ctx)
	if err != nil {
		return nil, err
	}
	if !pushed {
		return nil, nil
	}
	change, err := r.set.Opener.OpenChangeRequest(ctx, r.branch, r.changeTitle(), r.changeBody())
	if err != nil {
		return nil, err
	}
	return &change, nil
}

// pushed is whether the branch of the task is on the host. It is the one question a
// run asks git about a branch it pushed: an executor that pushed nothing has no work
// to put under a request, and a request for a branch that is not there is a request a
// person cannot open.
func (r *runner) pushed(ctx context.Context) (bool, error) {
	remote, err := r.output(ctx, r.worktree, "ls-remote", "--heads", "origin", r.branch)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(remote) != "", nil
}

// changeTitle is the title of the request crewflow opens for a run whose executor did
// not: the title of the task, which is the one line a person wrote and the one a
// person reads in a list of the requests of the project (§7f).
func (r *runner) changeTitle() string { return r.task.Title }

// changeBody is the body of that request: the line the whole cycle of a task turns on,
// and a note of who opened the request and why, because a request opened by crewflow
// with the words of a task and no words of its own is a request a reviewer cannot
// read as what it is (§6, §7h).
func (r *runner) changeBody() string {
	return fmt.Sprintf("Closes #%d\n\n"+
		"crewflow opened this request: the executor of the task pushed the branch of it "+
		"and the token of its app was out of date, so the run opened the request itself "+
		"(docs/DESIGN.md §7i).\n", r.task.Number)
}
