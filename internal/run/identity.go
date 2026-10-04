package run

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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
// time it pushes; and the folder of hooks of this task, because an executor that is
// talked into pushing into the default branch is refused by the machine as well and not
// only by the words of the task. Every one of those settings of git is written as an empty
// value and then as its own: the list of the helpers of the machine is taken out of it by
// the empty value, and what is left is the helper of crewflow and nobody else ([resets]).
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
	keys := make([]string, 0, len(r.identity.GitConfig))
	for key := range r.identity.GitConfig {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		for i, value := range valuesOf(key, r.identity.GitConfig[key]) {
			// The first value of a setting is put in place of what the worktree holds and
			// the rest are added to it: a worktree that is run a second time must not keep
			// the settings of the build before it, and git refuses to write one value over
			// several with a single one.
			args := []string{"config", "--worktree", "--replace-all"}
			if i > 0 {
				args = []string{"config", "--worktree", "--add"}
			}
			args = append(args, key, value)
			if err := r.git(ctx, r.worktree, args...); err != nil {
				return fmt.Errorf("the settings of git in the worktree: %w", err)
			}
		}
	}
	// The path of the hooks is the last thing written, so that a worktree is never left
	// pointing at a folder of hooks that is not there.
	hooks, err := r.hooks()
	if err != nil {
		return err
	}
	if err := r.git(ctx, r.worktree, "config", "--worktree", "core.hooksPath", hooks); err != nil {
		return fmt.Errorf("the path of the hooks of the worktree: %w", err)
	}
	return nil
}

// valuesOf are the values one setting of a worktree of a run holds, in the order git reads
// them: the empty value first for a setting of [resets], and the value the adapter of the host
// gave after it.
func valuesOf(key, value string) []string {
	if !slices.Contains(resets, key) {
		return []string{value}
	}
	return []string{"", value}
}

// resets are the settings of git whose value a worktree of a run takes to nothing before the
// settings the adapter of the host gave it.
//
// An empty `credential.helper` is what takes the list of helpers out of the files of the
// system and of the user before crewflow's own helper is added to it, and the helper of the
// machine is the one that answers first: `osxkeychain` of the file of the system of macOS has
// the token of the login of a person, and after a push went through it is handed the token of
// the App of the run — a secret of a run in the keychain of the owner, without their knowing
// (F-116, R6, #141). `credential.useHttpPath` is reset by the same rule and not out of need:
// one rule for every setting a run writes into a worktree of its own is a rule nobody has to
// remember setting by setting, and the value of the worktree is the one git reads either way.
//
// Only the settings of a worktree a run manages are reset: the file of the system, the file of
// the person and the settings of the repository of the project are left as they are, and
// crewflow writes none of them (docs/DESIGN.md §7a, §7i).
var resets = []string{"credential.helper", "credential.useHttpPath"}

// hooks writes the pre-push hook of the task into a folder of crewflow of its own and
// answers with that folder, which the worktree of the task is then pointed at.
//
// It is a folder of its own and not a file in the common hooks directory of the
// repository, because that directory is one file for every worktree of it: the review
// worktree of the orchestrator and the checkout of a person who works in the same clone
// would be refused the branch of the last task, and two tasks of one project would write
// over each other. The folder is outside the worktree, so that the tools of the executor
// do not see it as a part of the tree of the task (docs/DESIGN.md §7i).
func (r *runner) hooks() (string, error) {
	folder := r.hooksFolder()
	// What is under that name is the hook of the last run of this task, and a task
	// that is run again has a branch of its own: the hook is written afresh every run.
	if err := os.RemoveAll(folder); err != nil {
		return "", fmt.Errorf("take the hooks of the task away: %w", err)
	}
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return "", fmt.Errorf("make %s: %w", folder, err)
	}
	hook := filepath.Join(folder, "pre-push")
	if err := os.WriteFile(hook, []byte(prePush(r.branch)), 0o700); err != nil {
		return "", fmt.Errorf("write %s: %w", hook, err)
	}
	return folder, nil
}

// hooksFolder is where the hook of this task is kept: under what crewflow keeps of its
// own, by the project and by the number of the task, so that the worktrees of two tasks
// of one project have a hook each and the checkout of a person has none
// (docs/DESIGN.md §7).
func (r *runner) hooksFolder() string {
	return filepath.Join(r.journals.home, "hooks", r.cfg.RepoName(), strconv.Itoa(r.task.Number))
}

// prePush is the hook crewflow writes into the folder of hooks of a run. It reads the
// lines git writes to a pre-push hook — the local reference, its two shas and the
// reference on the host, four words to a line — and refuses every push of anything but
// the branch of the task, a deletion of it among them: a branch of a run is pushed and
// fetched, and deleting it is what a rewrite of the history of a task looks like from the
// host (§7h).
//
// The snapshot of the work a run left outside the commits is refused by its own name and
// before everything else: it is the one ref of this repository that is never to leave it
// under any push at all, and `git push --mirror` sends every ref of the repository — a
// person has to be able to tell from the refusal what a mirror was about to do. The work
// of a run is a protection from a lost folder and not work of the project, and the host
// has no branch for it, no review of it and no gate over it (D-088, §7a, §7i).
func prePush(branch string) string {
	allowed := "refs/heads/" + branch
	return `#!/bin/sh
# Written by crewflow before a run: the executor of a task pushes the branch of
# that task and nothing else (docs/DESIGN.md §7i). This hook is a guard and not
# the guard: the rules of the branch on the host are what an app cannot go around,
# and a hook is a file of a machine that anyone may delete. It is in a folder of
# this worktree alone, so no other worktree of this repository is touched by it.
branch='` + allowed + `'
while read -r local localSHA remote remoteSHA; do
	case "$remote" in
	` + checkpoints + `/*)
		echo "crewflow: the snapshot of the uncommitted work of a run stays on this machine, $remote is not pushed" >&2
		exit 1
		;;
	esac
	if [ "$local" = '(delete)' ] || [ "$remote" != "$branch" ]; then
		echo "crewflow: the executor of a task pushes only $branch, not $remote" >&2
		exit 1
	fi
done
exit 0
`
}

// takeHooksAway is the folder of hooks of this task gone. It is a folder of crewflow of
// its own, and a run that goes on in this worktree writes a new one where it belongs —
// which is why this is only said where nothing of the run is left to be read anyway
// (docs/DESIGN.md §7i).
func (r *runner) takeHooksAway() error {
	if err := os.RemoveAll(r.hooksFolder()); err != nil {
		return fmt.Errorf("take the hooks of the task away: %w", err)
	}
	return nil
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
