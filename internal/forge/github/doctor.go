package github

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/naghuale/crewflow/internal/forge"
)

// The names a report calls the checks of this adapter by: the program, and the
// login behind it.
const (
	ghCheck      = "gh"
	ghLoginCheck = "gh login"
)

// The names a report calls the checks of a project whose executor works as an App
// by: the key of the App, and the App itself on the repository of the project.
const (
	appKeyCheck  = "app key"
	appCheck     = "app"
	importHint   = "download the private key of the app and run `crewflow auth app import <file.pem>`, then delete the file"
	permissionsW = "the rights of the app are wider than a run of this project needs: "
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
	checks := []forge.Check{version, a.login(ctx)}
	if a.app == nil {
		return checks
	}
	return append(checks, a.bot(ctx)...)
}

// bot are the checks of a project whose executor works as the App: the key is in the
// store of the machine, and the App is installed on the repository with no more rights
// than a run of it asks for. The key is asked for without being read where the store
// can answer that alone, and a token is never asked for here: a report of a machine
// must not leave a token in a terminal (docs/DESIGN.md §7e, §7i).
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
	// Rights a run does not ask for are rights the executor of a run holds: a report
	// that says only what was asked for is a report of an intention and not of a
	// machine (docs/DESIGN.md §7i).
	if extra := beyondRun(granted); len(extra) > 0 {
		check.Status = forge.Fail
		check.Hint = permissionsW + listed(extra) +
			"\nchange the permissions of the app in the settings of GitHub: a run of this project only pushes its own branch"
	}
	return []forge.Check{key, check}
}

// beyondRun are the rights an installation holds that a token of a run never asks
// for: administration of the repository, workflows, and the rest of what an App can be
// given and a run of a project must not have (docs/DESIGN.md §7i). The four rights of
// §7i are the rest.
func beyondRun(granted []string) []string {
	allowed := []string{"contents", "pull_requests", "issues", "metadata"}
	var extra []string
	for _, right := range granted {
		if !slices.ContainsFunc(allowed, func(name string) bool { return strings.HasPrefix(right, name+" ") }) {
			extra = append(extra, right)
		}
	}
	return extra
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
		return forge.Check{
			Name:   ghCheck,
			Status: forge.Fail,
			Detail: program + " --version " + howItFailed(code, err, stdout, stderr),
			Hint:   "install " + program + ", or put the " + program + " of the project in PATH",
		}
	}
	return forge.Check{Name: ghCheck, Status: forge.OK, Detail: firstLine(stdout, stderr)}
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
		return forge.Check{
			Name:   ghLoginCheck,
			Status: forge.Fail,
			Detail: detail,
			Hint:   program + " auth login",
		}
	}
	detail := "signed in"
	if match := accountPattern.FindSubmatch(stdout); match != nil {
		detail = "signed in as " + string(match[1])
	}
	return forge.Check{Name: ghLoginCheck, Status: forge.OK, Detail: detail}
}

// howItFailed is how a report says that a program did not do its job: that it
// could not be started at all, or the code it exited with and what it said on
// the way out.
func howItFailed(code int, err error, outputs ...[]byte) string {
	if err != nil {
		return "could not be started: " + err.Error()
	}
	detail := fmt.Sprintf("exited with %d", code)
	if line := firstLine(outputs...); line != "" {
		return detail + ": " + line
	}
	return detail
}
