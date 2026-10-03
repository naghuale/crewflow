package github

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/forge/github/app"
	"github.com/naghuale/crewflow/internal/secret"
)

// The names a report calls the checks of this adapter by: the program, and the
// login behind it.
const (
	ghCheck      = "gh"
	ghLoginCheck = "gh login"
)

// warn is a check that is worth saying and is not worth stopping a run over. The word
// is the one the report of `doctor` prints and counts, so that a line of a role is a
// line of that report and not a second dialect to translate (docs/DESIGN.md §7e, §7h).
const warn forge.Status = "warn"

// The names a report calls the checks of a project whose executor works as an App
// by: the key of the App, the App itself on the repository of the project, and the
// token the App hands out for it.
const (
	appKeyCheck   = "app key"
	appCheck      = "app"
	appTokenCheck = "token"
	importHint    = "download the private key of the app and run `crewflow auth app import <file.pem>`, then delete the file"
	permissionsW  = "the rights of the app are wider than a run of this project needs: "
	tokenHint     = "a run of this project cannot start without a token of the repository of the project: " +
		"install the app %d on it, or change project.repo and identity.github_app in %s"
)

// The names a report calls the same three checks of the App of the orchestrator by: it
// is a second App of the project, and a report that called both of them "app" would
// leave a person with two lines and no way to tell which key is missing.
const (
	orchestratorKeyCheck   = "orchestrator app key"
	orchestratorAppCheck   = "orchestrator app"
	orchestratorTokenCheck = "orchestrator token"
	// orchestratorProtectionCheck is the rules of the branch a change is merged into,
	// which are read as the orchestrator and are the one question of §7h that takes a
	// right of an administrator that the App does not have (§7i).
	orchestratorProtectionCheck = "orchestrator branch protection"
	// orchestratorImportHint and the rest name the key of §7i of the orchestrator and
	// not the key of the executor, and the hint says what to do about it.
	orchestratorImportHint = "download the private key of the app of the orchestrator and run " +
		"`crewflow auth app import <file.pem>`, then delete the file"
	orchestratorPermissionsW = "the rights of the app of the orchestrator are wider than the orchestrator of this " +
		"project needs: "
	orchestratorTokenHint = "the orchestrator of this project cannot review or merge without a token of the " +
		"repository of the project: install the app %d on it, or change project.repo and orchestrator.github_app in %s"
)

// Doctor checks that gh is installed and that somebody is signed in with it,
// which is the only way into GitHub crewflow has. It is a check of the role and
// not of crewflow: a project on another host is checked by the adapter of that
// host, and a project without a host is not checked at all (docs/DESIGN.md §7g).
//
// A project whose executor works as an App is checked for the App as well: the key
// is where crewflow has to sign a token, and the installation is what a run of it
// will be able to do. A run in the mode of the bot that is not set up is a run that
// cannot start, and `crewflow doctor` is where a person finds that out (docs/DESIGN.md §7i).
func (a *Adapter) Doctor(ctx context.Context) []forge.Check {
	if _, err := a.env.LookPath(program); err != nil {
		return []forge.Check{{
			Name:   ghCheck,
			Status: forge.Fail,
			Detail: program + " is not in PATH",
			Hint: fmt.Sprintf("install %s, then sign in: %s auth login, or change forge.kind in %s",
				program, program, a.env.ConfigPath),
		}}
	}
	// A gh that does not answer is not asked about the login: there is nothing
	// to sign in with, and a report full of noise is a report nobody reads.
	version := a.version(ctx)
	if version.Status == forge.Fail {
		return []forge.Check{version}
	}
	checks := []forge.Check{version}
	// gh is given the token of the App of the orchestrator in every command where the
	// orchestrator works apart from the owner, and a token needs no login of its own: a
	// report that failed on "gh login" would refuse a project that is set up the way
	// §7i tells it to be set up (§7i).
	if a.orchestrator == nil {
		checks = append(checks, a.login(ctx))
	}
	return append(checks, a.AppChecks(ctx)...)
}

// AppChecks are the checks of the accounts of the host this adapter works as: the App of
// the executor under the names a report has always called them, and the App of the
// orchestrator under its own. They are asked apart from the program of the host, because
// a project has two accounts and gh is one (§7i).
func (a *Adapter) AppChecks(ctx context.Context) []forge.Check {
	var checks []forge.Check
	if a.app != nil {
		checks = append(checks, a.bot(ctx)...)
	}
	if a.orchestrator != nil {
		checks = append(checks, a.orchestratorOf(ctx)...)
	}
	return checks
}

// orchestratorOf are the checks of the App of the orchestrator: the key is in the
// store of the machine, the App is installed on the repository with no more rights than
// the orchestrator asks for, and the App does hand out a token of that repository. They
// are the checks of the App of the executor under other names and against other rights,
// because a report of a machine that showed one App where there are two would be a
// report of a project crewflow is not working on (§7h, §7i).
func (a *Adapter) orchestratorOf(ctx context.Context) []forge.Check {
	has, err := a.orchestrator.HasKey()
	switch {
	case err != nil:
		return []forge.Check{{
			Name:   orchestratorKeyCheck,
			Status: forge.Fail,
			Detail: err.Error(),
			Hint:   orchestratorImportHint,
		}}
	case !has:
		return []forge.Check{{
			Name:   orchestratorKeyCheck,
			Status: forge.Fail,
			Detail: fmt.Sprintf("the store holds no key of the app %d of the orchestrator", a.orchestrator.AppID),
			Hint:   orchestratorImportHint,
		}}
	}
	key := forge.Check{
		Name:   orchestratorKeyCheck,
		Status: forge.OK,
		Detail: fmt.Sprintf("the store holds the key of the app %d of the orchestrator", a.orchestrator.AppID),
	}
	installation, err := a.orchestrator.Installation(ctx)
	if err != nil {
		return []forge.Check{key, {
			Name:   orchestratorAppCheck,
			Status: forge.Fail,
			Detail: err.Error(),
			Hint: fmt.Sprintf("install the app %d on %s, or change orchestrator.github_app in %s",
				a.orchestrator.AppID, a.repo, a.env.ConfigPath),
		}}
	}
	check := forge.Check{
		Name:   orchestratorAppCheck,
		Status: forge.OK,
		Detail: fmt.Sprintf("installation %d on %s, rights: %s",
			installation.ID, a.repo, listed(installation.Granted())),
	}
	if extra := installation.Beyond(app.OrchestratorRights()); len(extra) > 0 {
		check.Status = forge.Fail
		check.Hint = orchestratorPermissionsW + listed(extra) +
			"\nchange the permissions of the app in the settings of GitHub: the orchestrator reviews, merges and " +
			"closes the task of a change, and it is not the owner"
	}
	return append(append([]forge.Check{key, check}, a.tokenOfOrchestrator(ctx)), a.protectionOf(ctx))
}

// tokenOfOrchestrator is the check that the App of the orchestrator hands out a token of
// the repository of the project: a review and a merge in the mode of a separate login
// are written and pushed in the name of that App, and a refusal of the API here is a
// refusal a person sees before a review and not in the middle of one. The token is asked
// for and thrown away — nothing of it is shown, and nothing of it is used for anything
// (docs/DESIGN.md §7e, §7i).
func (a *Adapter) tokenOfOrchestrator(ctx context.Context) forge.Check {
	token, err := a.orchestrator.Token(ctx)
	if err != nil {
		return forge.Check{
			Name:   orchestratorTokenCheck,
			Status: forge.Fail,
			Detail: err.Error(),
			Hint:   fmt.Sprintf(orchestratorTokenHint, a.orchestrator.AppID, a.env.ConfigPath),
		}
	}
	return forge.Check{
		Name:   orchestratorTokenCheck,
		Status: forge.OK,
		Detail: fmt.Sprintf("the api gave a token of the installation for %s, good until %s; it is not used and not shown",
			a.repo, token.ExpiresAt.UTC().Format(time.RFC3339)),
	}
}

// protectionOf is the check that the App of the orchestrator may read the rules of the
// branch a change of the project is merged into. It is asked before the first merge and
// not in the middle of one: a gate that cannot read those rules refuses every change of
// the project with `forge-unavailable`, and a person is told that here rather than by a
// merge that will not happen (docs/DESIGN.md §7h).
//
// It is a warning and not a failure where it cannot be read: the rules of the branch are
// there and are not being touched, and a report that failed would fail a project whose
// rights of an App are exactly the four of §7i over a rule of the host that crewflow
// never asks for. What is to be done about it is the owner's decision and not
// crewflow's (§7i).
func (a *Adapter) protectionOf(ctx context.Context) forge.Check {
	enabled, err := a.classicProtection(ctx)
	switch {
	case err != nil:
		return forge.Check{
			Name:   orchestratorProtectionCheck,
			Status: forge.Fail,
			Detail: err.Error(),
			Hint: fmt.Sprintf("the gate asks the host for the rules of the branch %s before every merge, and this "+
				"answer is what it will get there: the rules of that branch are not being read", a.base()),
		}
	case !enabled:
		return forge.Check{
			Name:   orchestratorProtectionCheck,
			Status: forge.OK,
			Detail: fmt.Sprintf("the branch %s has no protection of a branch itself, so its rules are rulesets, "+
				"which the app of the orchestrator may read", a.base()),
		}
	}
	return forge.Check{
		Name:   orchestratorProtectionCheck,
		Status: warn,
		Detail: fmt.Sprintf("the branch %s has the protection of a branch itself on, and the rules of it are only "+
			"read by an administrator", a.base()),
		Hint: protectionHint,
	}
}

// bot are the checks of a project whose executor works as the App: the key is in the
// store of the machine, the App is installed on the repository with no more rights than
// a run of it asks for, and the App does hand out a token of that repository. The key
// is asked for without being read where the store can answer that alone, and the token
// of the last check is asked for and thrown away: a report of a machine shows that a
// token came and never what it is, because a report is pasted into issues
// (docs/DESIGN.md §7e, §7i).
func (a *Adapter) bot(ctx context.Context) []forge.Check {
	has, err := a.app.HasKey()
	switch {
	case err != nil:
		return []forge.Check{{
			Name:   appKeyCheck,
			Status: forge.Fail,
			Detail: err.Error(),
			Hint:   importHint,
		}}
	case !has:
		return []forge.Check{{
			Name:   appKeyCheck,
			Status: forge.Fail,
			Detail: fmt.Sprintf("the store holds no key of the app %d", a.app.AppID),
			Hint:   importHint,
		}}
	}
	key := forge.Check{
		Name:   appKeyCheck,
		Status: forge.OK,
		Detail: fmt.Sprintf("the store holds the key of the app %d", a.app.AppID),
	}
	installation, err := a.app.Installation(ctx)
	if err != nil {
		return []forge.Check{key, {
			Name:   appCheck,
			Status: forge.Fail,
			Detail: err.Error(),
			Hint: fmt.Sprintf("install the app %d on %s, or change identity.github_app in %s",
				a.app.AppID, a.repo, a.env.ConfigPath),
		}}
	}
	granted := installation.Granted()
	check := forge.Check{
		Name:   appCheck,
		Status: forge.OK,
		Detail: fmt.Sprintf("installation %d on %s, rights: %s",
			installation.ID, a.repo, listed(granted)),
	}
	// Rights a run does not ask for are rights the executor of a run holds, and a
	// right asked for with a wider level than §7i is the same thing said in another
	// way. A report that says only what was asked for is a report of an intention and
	// not of a machine (docs/DESIGN.md §7i).
	if extra := installation.Beyond(app.RunRights()); len(extra) > 0 {
		check.Status = forge.Fail
		check.Hint = permissionsW + listed(extra) +
			"\nchange the permissions of the app in the settings of GitHub: a run of this project only pushes its own branch"
	}
	return append([]forge.Check{key, check}, a.token(ctx))
}

// token is the check that the App does hand out a token of the repository of the
// project: a run in the mode of the bot cannot start without one, and a refusal of the
// API here is a refusal a person sees before a task and not in the middle of one. The
// token is asked for and thrown away — nothing of it is shown, and nothing of it is
// used for anything (docs/DESIGN.md §7e, §7i).
func (a *Adapter) token(ctx context.Context) forge.Check {
	token, err := a.app.Token(ctx)
	if err != nil {
		return forge.Check{
			Name:   appTokenCheck,
			Status: forge.Fail,
			Detail: err.Error(),
			Hint:   fmt.Sprintf(tokenHint, a.app.AppID, a.env.ConfigPath),
		}
	}
	return forge.Check{
		Name:   appTokenCheck,
		Status: forge.OK,
		Detail: fmt.Sprintf("the api gave a token of the installation for %s, good until %s; it is not used and not shown",
			a.repo, token.ExpiresAt.UTC().Format(time.RFC3339)),
	}
}

// listed is a list of rights as one line of a report reads: a person reads the line
// and not a list of them.
func listed(items []string) string {
	if len(items) == 0 {
		return "none"
	}
	return strings.Join(items, ", ")
}

// version is the check that gh is a program and not a name: an adapter that
// cannot be started says so here and not in the middle of a task.
func (a *Adapter) version(ctx context.Context) forge.Check {
	stdout, stderr, code, err := a.env.Run(ctx, program, []string{"--version"}, "", a.environment())
	if err != nil || code != 0 {
		return a.check(ghCheck, forge.Fail, program+" --version "+a.howItFailed(code, err, stdout, stderr),
			"install "+program+", or put the "+program+" of the project in PATH")
	}
	return a.check(ghCheck, forge.OK, firstLine(stdout, stderr), "")
}

// login is the check that somebody is signed in with gh, and it reports only who:
// the whole answer of "gh auth status" holds the token, and a report is read by
// people and pasted into issues (docs/DESIGN.md §7e).
func (a *Adapter) login(ctx context.Context) forge.Check {
	stdout, stderr, code, err := a.env.Run(ctx, program, []string{"auth", "status"}, "", a.environment())
	if err != nil || code != 0 {
		detail := "nobody is signed in"
		if line := firstLine(stderr, stdout); line != "" {
			detail = line
		}
		return a.check(ghLoginCheck, forge.Fail, detail, program+" auth login")
	}
	detail := "signed in"
	if match := accountPattern.FindSubmatch(stdout); match != nil {
		detail = "signed in as " + string(match[1])
	}
	return a.check(ghLoginCheck, forge.OK, detail, "")
}

// check is one line of a report of this adapter with what it says published through the
// boundary of the adapter: what a program of the machine wrote about a failure of it is
// in the detail of the check, and the address of the profile with the credentials in it
// was in the environment of that program (docs.DESIGN.md §7e).
func (a *Adapter) check(name string, status forge.Status, detail, hint string) forge.Check {
	said, err := a.env.Out.Publish(secret.ChannelReport, detail)
	if err != nil {
		said = "this check could not be published"
	}
	return forge.Check{Name: name, Status: status, Detail: said, Hint: hint}
}

// howItFailed is how a report says that a program did not do its job: that it
// could not be started at all, or the code it exited with and what it said on
// the way out. The words are as the program wrote them, and the boundary of the
// adapter takes the values of the route out of them where the check is published (§7e).
func (a *Adapter) howItFailed(code int, err error, outputs ...[]byte) string {
	if err != nil {
		return "could not be started: " + err.Error()
	}
	detail := fmt.Sprintf("exited with %d", code)
	if line := firstLine(outputs...); line != "" {
		return detail + ": " + line
	}
	return detail
}
