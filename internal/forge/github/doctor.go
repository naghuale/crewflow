package github

import (
	"context"
	"fmt"

	"github.com/naghuale/crewflow/internal/forge"
)

// The names a report calls the checks of this adapter by: the program, and the
// login behind it.
const (
	ghCheck      = "gh"
	ghLoginCheck = "gh login"
)

// Doctor checks that gh is installed and that somebody is signed in with it,
// which is the only way into GitHub crewflow has. It is a check of the role and
// not of crewflow: a project on another host is checked by the adapter of that
// host, and a project without a host is not checked at all (docs/DESIGN.md §7g).
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
	return []forge.Check{version, a.login(ctx)}
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
