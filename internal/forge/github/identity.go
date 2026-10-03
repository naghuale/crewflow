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

// Orchestrator is the App the orchestrator of a project on GitHub works as, which is a
// second App of a project whose executor already has one: the numbers that say which
// App it is, which is all the file of a project says (docs/DESIGN.md §7i).
//
// A zero AppID is the mode of the shared login, and not a mistake: the orchestrator is
// then the person who runs crewflow, and the owner is that person too.
type Orchestrator struct {
	// AppID and InstallationID are the numbers of the App and of its installation on
	// the repository of the project.
	AppID          int64
	InstallationID int64
	// API is the address of the API of the host, as in [Bot].
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

// WithOrchestrator returns the adapter as the account the orchestrator of the project
// works as: the App of the project, its own token, and every command of gh and git in
// the name of that App. Without it, or with a zero AppID, the orchestrator is the login
// of the person who runs crewflow, which is what a project that has not set the second
// App up gets — and what `crewflow doctor` says is the weaker of the two (§7i).
func (a *Adapter) WithOrchestrator(who Orchestrator) *Adapter {
	if who.AppID <= 0 {
		return a
	}
	address := who.API
	if address == "" {
		address = app.API(a.host)
	}
	a.orchestrator = &app.Source{
		AppID:          who.AppID,
		InstallationID: who.InstallationID,
		Role:           app.Orchestrator,
		Repo:           a.repo,
		BaseURL:        address,
		Store:          a.env.Secrets,
		HTTP:           a.env.HTTP,
		Now:            a.env.Now,
	}
	return a
}

// OrchestratorIdentity is whose name the orchestrator of this project works under: the
// App of the project in the mode of a separate login, and the login of the person in
// the mode of a shared one, named after the account gh is signed in as (docs/DESIGN.md §7i).
func (a *Adapter) OrchestratorIdentity(ctx context.Context) (forge.Identity, error) {
	if a.orchestrator == nil {
		return a.sharedOrchestrator(), nil
	}
	return a.orchestrator.OrchestratorIdentity(ctx)
}

// DescribeOrchestrator is the line a report shows about the orchestrator of this
// project, without the rights of that name: a report of a machine prints a line and
// mints no token of an hour for it (§7e, §7i).
func (a *Adapter) DescribeOrchestrator(ctx context.Context) (forge.Identity, error) {
	if a.orchestrator == nil {
		return a.sharedOrchestrator(), nil
	}
	return a.orchestrator.DescribeOrchestrator(ctx)
}

// sharedOrchestrator is the orchestrator of a project that has no account of the host
// of its own: the login of the person, and with it the owner — the one thing the mode
// of the shared login cannot do is to tell the two apart, which is what `doctor` says
// (docs/DESIGN.md §7i).
func (a *Adapter) sharedOrchestrator() forge.Identity {
	account := a.account()
	if account == "" {
		return forge.Identity{
			Mode:        forge.ModeShared,
			Description: "shared — the login of gh (one login with the owner)",
		}
	}
	return forge.Identity{
		Mode:        forge.ModeShared,
		Account:     account,
		Description: "shared — the login gh " + account + " (one login with the owner)",
	}
}

// orchestratorToken is the token of the App of the orchestrator, signed once for as
// long as the adapter lives: a review and a merge ask gh a handful of times each, and a
// token of an hour for every question would be a token an hour for every question. The
// push of a merge does not go through it at all — that is what the helper of the
// credentials of git is for, and it signs a token of its own for that one push (§7i).
func (a *Adapter) orchestratorToken(ctx context.Context) (string, error) {
	a.signing.Lock()
	defer a.signing.Unlock()
	if a.signed != "" {
		return a.signed, nil
	}
	token, err := a.orchestrator.Token(ctx)
	if err != nil {
		return "", err
	}
	a.signed = token.Value
	return a.signed, nil
}

// App is the App of the project as a report and a command about it see it: where its
// key is kept, what it may do on the repository, and never a token of it. It is nil
// for a project whose executor works as the person who runs crewflow.
func (a *Adapter) App() *app.Source { return a.app }

// Orchestrator is the App of the orchestrator of the project, as a report and a
// command about it see it, and nil for a project whose orchestrator shares the login of
// the person (docs/DESIGN.md §7i).
func (a *Adapter) Orchestrator() *app.Source { return a.orchestrator }

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
		Account:     account,
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

// SignedIn is the account the adapter speaks as, and an error when it cannot say: a
// record of a review is a comment, and it is an approval only because of the account it
// was written in, so an account nobody can name is a record crewflow cannot count and
// should not write in the name of (docs/DESIGN.md §7h).
//
// Where the orchestrator of the project has an account of its own, that account is the
// one — gh is given the token of the App in every command, so the login it happens to
// be signed in as is not what a record of a review is written in (§7i).
//
// An adapter that was built to look speaks as the login of the person in every command,
// and that is the account it names: a record nobody is going to write has no account to
// be written in (§7i).
func (a *Adapter) SignedIn(ctx context.Context) (string, error) {
	if a.orchestrator != nil && !a.looking {
		bot, err := a.orchestrator.Bot(ctx)
		if err != nil {
			return "", err
		}
		return bot.Login, nil
	}
	if account := a.account(); account != "" {
		return account, nil
	}
	return "", errors.New("gh does not say which account it is signed in as: run `gh auth status` and read the account out of it")
}

// SigningAs is the subject the adapter speaks as: the App of the orchestrator, with the
// number of that App, where the orchestrator works apart from the owner — and the account
// gh is signed in as, with the number of that account, where it does not.
//
// It is a subject and not a login because the record of a review is compared with the
// lists of the project by the number the host keeps each account under, and a name is
// what changes when an App is renamed (F-081, docs.DESIGN.md §7h, §7i). The login above
// is where the answer comes from; the number is one question more of the host, and it is
// asked only where something is decided by it.
//
// An adapter that was built to look has no name to give and does not go and ask the host
// for one: the number of the App of the orchestrator is all the file of the project says
// about it, the host publishes no name against a number, and a subject without a name is
// a subject a gate counts records by like any other (§7i).
func (a *Adapter) SigningAs(ctx context.Context) (forge.Subject, error) {
	if a.orchestrator != nil {
		if a.looking {
			return forge.Subject{Kind: forge.KindApp, ID: a.orchestrator.AppID}, nil
		}
		bot, err := a.orchestrator.Bot(ctx)
		if err != nil {
			return forge.Subject{}, err
		}
		return forge.Subject{Kind: forge.KindApp, ID: a.orchestrator.AppID, Login: bot.Login}, nil
	}
	account := a.account()
	if account == "" {
		return forge.Subject{}, errors.New("gh does not say which account it is signed in as: " +
			"run `gh auth status` and read the account out of it")
	}
	return a.Subject(ctx, account)
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
	if err := a.mayAct(); err != nil {
		return forge.ChangeRequest{}, err
	}
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
		return forge.ChangeRequest{}, a.env.Out.Err(fmt.Errorf("gh %s: %w", strings.Join(arguments, " "), err))
	case code != 0:
		return forge.ChangeRequest{}, a.env.Out.Err(fmt.Errorf("gh %s: exited with %d: %s",
			strings.Join(arguments, " "), code, firstLine(stderr)))
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
// It writes as the orchestrator of the project and never as the App of the executor:
// the record of a review is the word of the orchestrator, and a record the executor
// could write is a record the gate would have to refuse. Which account the
// orchestrator is depends on the mode of §7i — the App of the project where the
// orchestrator works apart from the owner, and the login of the person where it does
// not (docs/DESIGN.md §7h, §7i).
func (a *Adapter) WriteComment(ctx context.Context, number int, body string) error {
	if err := a.mayAct(); err != nil {
		return err
	}
	environment, err := a.speaking(ctx)
	if err != nil {
		return err
	}
	arguments := []string{"pr", "comment", strconv.Itoa(number), "-R", a.repo, "--body", body}
	_, stderr, code, err := a.env.Run(ctx, program, arguments, "", environment)
	switch {
	case err != nil:
		return a.env.Out.Err(fmt.Errorf("gh %s: %w", strings.Join(arguments[:5], " "), err))
	case code != 0:
		return a.env.Out.Err(fmt.Errorf("gh %s: exited with %d: %s",
			strings.Join(arguments[:5], " "), code, firstLine(stderr)))
	}
	return nil
}

// The adapter of GitHub is a role of the core that can also say whose name the
// executor of a run works under, can open a change request on behalf of a run, and
// can say everything a review of a change is made of. Without a project that asks
// for the mode of the bot it is neither of the first two, and a project with such an
// app is given both.
var (
	_ forge.Identified = (*Adapter)(nil)
	// The adapter can say what subject of GitHub a login of the file of a project is:
	// the settings name accounts by login, and the gate counts records by the number the
	// host keeps each of them under (§7h, §7i).
	_ forge.Naming                = (*Adapter)(nil)
	_ forge.Signer                = (*Adapter)(nil)
	_ forge.Described             = (*Adapter)(nil)
	_ forge.Orchestrated          = (*Adapter)(nil)
	_ forge.OrchestratorDescribed = (*Adapter)(nil)
	_ forge.RequestOpener         = (*Adapter)(nil)
	_ forge.SignedIn              = (*Adapter)(nil)
	// A review of a project on GitHub is gathered out of these four, and without
	// one of them the gate of §7h has nothing to judge.
	_ forge.AppChecker    = (*Adapter)(nil)
	_ forge.FileLister    = (*Adapter)(nil)
	_ forge.CheckLister   = (*Adapter)(nil)
	_ forge.RuleLister    = (*Adapter)(nil)
	_ forge.HeadRef       = (*Adapter)(nil)
	_ forge.CommentWriter = (*Adapter)(nil)
)
