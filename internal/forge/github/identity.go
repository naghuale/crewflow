package github

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/forge/github/app"
)

// Bot is the account the executor of a project on GitHub works as, and the branch its
// change requests are meant for: the numbers that say which App it is, which is all
// the file of a project says, and the branch a run of the project starts from and is
// meant for (docs/DESIGN.md §7i).
type Bot struct {
	// AppID and InstallationID are the numbers of the App and of its installation on
	// the repository of the project. A zero installation is not a mistake: the
	// installation is found through the repository the first time it is asked for.
	AppID          int64
	InstallationID int64
	// DefaultBranch is the branch a change request of the project is opened for.
	DefaultBranch string
	// API is the address of the API of the host. It is empty for the address the host
	// of the project stands for, and a test of a token exchange points it at a server
	// of its own: no test of crewflow asks GitHub for a token, and one that did would
	// be a test of somebody else's account.
	API string
}

// WithBot returns the adapter with the App of the project as the account the executor
// of it works as, and with the default branch of the project as the one a change
// request of it is meant for. Without it the adapter is the login of the owner, which
// is what a project that says nothing about it gets (docs/DESIGN.md §7i).
func (a *Adapter) WithBot(bot Bot) *Adapter {
	a.defaultBranch = bot.DefaultBranch
	if bot.AppID > 0 {
		address := bot.API
		if address == "" {
			address = app.API(a.host)
		}
		a.app = &app.Source{
			AppID:          bot.AppID,
			InstallationID: bot.InstallationID,
			Repo:           a.repo,
			BaseURL:        address,
			Store:          a.env.Secrets,
			HTTP:           a.env.HTTP,
			Now:            a.env.Now,
		}
	}
	return a
}

// App is the App of the project as a report and a command about it see it: where its
// key is kept, what it may do on the repository, and never a token of it. It is nil
// for a project whose executor works as the person who runs crewflow.
func (a *Adapter) App() *app.Source { return a.app }

// ExecutorIdentity is whose name the executor of a run of this project works under
// (docs/DESIGN.md §7i).
//
// The mode of the owner is the login of the person, named after the account gh is
// signed in as: a report that says the powers of a run are shared and does not say
// whose they are is a report a person has to go and look for. The mode of the bot is
// the App, and crewflow signs a token for it: one repository, the rights of §7i, and
// an hour of life (docs/DESIGN.md §7i).
func (a *Adapter) ExecutorIdentity(ctx context.Context) (forge.Identity, error) {
	if a.app == nil {
		return a.ownerIdentity(), nil
	}
	return a.app.Identity(ctx)
}

// ownerIdentity is the identity of a run that works as the person who runs crewflow,
// named after the account gh is signed in as. A gh that could not be asked is named
// as the login of gh and not as nobody: `crewflow doctor` reports the login as a check
// of its own, and a report that says nothing here is a report with a hole in it
// (docs/DESIGN.md §7g).
func (a *Adapter) ownerIdentity() forge.Identity {
	account := a.account()
	if account == "" {
		return forge.Identity{
			Mode:        forge.ModeOwner,
			Description: "owner — the login of gh (shared rights)",
		}
	}
	return forge.Identity{
		Mode:        forge.ModeOwner,
		Description: "owner — the login gh " + account + " (shared rights)",
	}
}

// DescribeIdentity is the line a report shows about whose name the executor of a run
// of this project works under, without the rights of that name: a report of a
// machine does not mint a token of an hour to print a mode (docs/DESIGN.md §7i).
func (a *Adapter) DescribeIdentity(ctx context.Context) (forge.Identity, error) {
	if a.app == nil {
		return a.ownerIdentity(), nil
	}
	return a.app.Describe(ctx)
}

// account is who gh is signed in as, and an empty string when gh could not be asked
// or said nothing: the answer of "gh auth status" holds the token, and only the
// account is read out of it (docs/DESIGN.md §7e).
func (a *Adapter) account() string {
	stdout, _, code, err := a.env.Run(context.Background(), program, []string{"auth", "status"}, "", a.environment())
	if err != nil || code != 0 {
		return ""
	}
	if match := accountPattern.FindSubmatch(stdout); match != nil {
		return string(match[1])
	}
	return ""
}

// SignedIn is the account gh speaks as, and an error when gh does not say: a record
// of a review is a comment, and it is an approval only because of the account it was
// written in, so an account nobody can name is a record crewflow cannot count and
// should not write in the name of (docs/DESIGN.md §7h).
func (a *Adapter) SignedIn(context.Context) (string, error) {
	if account := a.account(); account != "" {
		return account, nil
	}
	return "", errors.New("gh does not say which account it is signed in as: run `gh auth status` and read the account out of it")
}

// OpenChangeRequest opens the change request of a branch on behalf of a run whose
// executor did not: the agent ran out of time, or the token of gh it was given is
// over, and the work of a run is on a branch that nobody asked about
// (docs/DESIGN.md §7i).
//
// It signs a token of its own instead of using the one of the run, because the one of
// the run is what has gone over — and a request opened in the login of the person
// would be a request the gate of a review cannot tell from one of the owner, which is
// the very thing the mode of the bot is for (§7h).
func (a *Adapter) OpenChangeRequest(ctx context.Context, branch, title, body string) (forge.ChangeRequest, error) {
	if a.app == nil {
		return forge.ChangeRequest{}, errors.New("this project has no app of its own, so its runs open no change request for themselves")
	}
	token, err := a.app.Token(ctx)
	if err != nil {
		return forge.ChangeRequest{}, err
	}
	env := append(a.environment(), "GH_TOKEN="+token.Value)
	arguments := []string{"pr", "create", "-R", a.repo,
		"--head", branch, "--base", a.base(), "--title", title, "--body", body}
	_, stderr, code, err := a.env.Run(ctx, program, arguments, "", env)
	switch {
	case err != nil:
		return forge.ChangeRequest{}, fmt.Errorf("gh %s: %w", strings.Join(arguments, " "), err)
	case code != 0:
		return forge.ChangeRequest{}, fmt.Errorf("gh %s: exited with %d: %s",
			strings.Join(arguments, " "), code, firstLine(stderr))
	}
	// The request is read back instead of being assembled out of the URL gh printed:
	// a report shows the head of it, and the head is what a review is about (§8).
	opened, found, err := a.changeRequestOf(ctx, branch, env)
	switch {
	case err != nil:
		return forge.ChangeRequest{}, err
	case !found:
		return forge.ChangeRequest{}, fmt.Errorf("gh pr create: opened nothing for the branch %s, and it is not there either", branch)
	}
	return opened, nil
}

// base is the branch a change request of this project is meant for, and main when the
// project named none: it is the branch every work of a project starts from, and a
// request meant for another one is a request a person did not ask for.
func (a *Adapter) base() string {
	if a.defaultBranch == "" {
		return "main"
	}
	return a.defaultBranch
}

// WithDefaultBranch returns the adapter with the branch the change requests of the
// project are meant for: the rules of a branch are read by their name, and a project
// whose branch is not main needs crewflow to know which one it is (§7h).
func (a *Adapter) WithDefaultBranch(branch string) *Adapter {
	a.defaultBranch = branch
	return a
}

// HeadRef is the ref of GitHub that stands at the head of a change request, and it
// is what a review fetches to have the commit of the head in a checkout: the rules
// of §7h are asked of git, and git is asked about a commit it has to hold
// (docs/DESIGN.md §7h).
func (a *Adapter) HeadRef(number int) string {
	return fmt.Sprintf("refs/pull/%d/head", number)
}

// WriteComment writes a record of a review under the change request.
//
// It writes as the person who runs crewflow and never as the App of the executor:
// the record of a review is the word of the orchestrator, and a record the
// executor could write is a record the gate would have to refuse (docs/DESIGN.md §7h, §7i).
func (a *Adapter) WriteComment(ctx context.Context, number int, body string) error {
	arguments := []string{"pr", "comment", strconv.Itoa(number), "-R", a.repo, "--body", body}
	_, stderr, code, err := a.env.Run(ctx, program, arguments, "", a.environment())
	switch {
	case err != nil:
		return fmt.Errorf("gh %s: %w", strings.Join(arguments[:5], " "), err)
	case code != 0:
		return fmt.Errorf("gh %s: exited with %d: %s",
			strings.Join(arguments[:5], " "), code, firstLine(stderr))
	}
	return nil
}

// The adapter of GitHub is a role of the core that can also say whose name the
// executor of a run works under, can open a change request on behalf of a run, and
// can say everything a review of a change is made of. Without a project that asks
// for the mode of the bot it is neither of the first two, and a project with such an
// app is given both.
var (
	_ forge.Identified    = (*Adapter)(nil)
	_ forge.Described     = (*Adapter)(nil)
	_ forge.RequestOpener = (*Adapter)(nil)
	_ forge.SignedIn      = (*Adapter)(nil)
	// A review of a project on GitHub is gathered out of these four, and without
	// one of them the gate of §7h has nothing to judge.
	_ forge.FileLister    = (*Adapter)(nil)
	_ forge.CheckLister   = (*Adapter)(nil)
	_ forge.HeadRef       = (*Adapter)(nil)
	_ forge.CommentWriter = (*Adapter)(nil)
)
