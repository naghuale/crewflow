// Package roles builds the three roles of docs/DESIGN.md §7g — the host of the
// code, the tracker of the tasks and the CI — out of the settings of a project,
// and says which kind of a role crewflow has no adapter for yet.
//
// The adapters live next to the systems they speak to, and the core knows only
// the words of package forge. This package is where the settings of a project
// meet those words, and the only place that knows both: which is why it is a
// package of its own and not a part of either.
package roles

import (
	"context"
	"fmt"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/forge/github"
	"github.com/naghuale/crewflow/internal/forge/github/app"
)

// New returns the roles the settings of the project ask for, or an error that
// names the kind crewflow has no adapter for. The settings are those of a file
// that has been through [config.Load]: the pairs that contradict each other are
// refused there.
//
// A run is the business of the executor alone, and it is given no account of the
// orchestrator: a run that could write a record of a review in the name of the
// orchestrator would be an executor with a pen in its hand (§7i).
func New(cfg config.Config, env forge.Env) (forge.Set, error) {
	return rolesOf(cfg, env, cfg.Identity.Mode, forge.ModeShared)
}

// AsOwner returns the roles of a project as the person who runs crewflow, whoever
// the executor of the project works as.
//
// A review of a change is the business of the orchestrator, and the owner is the
// person: the rules of a branch are rights of a person and not of the App of an
// executor, which has none of them (docs/DESIGN.md §7h, §7i). A project in the
// mode of the owner gets the same roles either way, and `task run` asks for the ones
// of [New].
func AsOwner(cfg config.Config, env forge.Env) (forge.Set, error) {
	return rolesOf(cfg, env, forge.ModeOwner, forge.ModeShared)
}

// AsOrchestrator returns the roles of a project as its orchestrator: the account the
// record of a review is written in and the merge is pushed as. In the mode of a shared
// login that is the person who runs crewflow, and in the mode of a separate one it is
// the App of the project — which is what lets the gate tell a decision of the owner
// from a record of the orchestrator (docs/DESIGN.md §7h, §7i).
//
// It is given no App of the executor and no opener: a review neither commits as the
// executor nor opens a change request of its own, and an adapter that carried the App
// of a run into a review is one command away from an executor writing a record (§7i).
func AsOrchestrator(cfg config.Config, env forge.Env) (forge.Set, error) {
	return rolesOf(cfg, env, forge.ModeOwner, cfg.Orchestrator.Mode)
}

// rolesOf is the roles of a project under the mode of the executor, which is what a
// run works under, and under the mode of the orchestrator, which is what a review and
// a merge work under. A run never has the second one and a review never the first:
// that each of them has an account of its own, or the login of the person, is the
// whole of §7i.
func rolesOf(cfg config.Config, env forge.Env, mode, orchestration string) (forge.Set, error) {
	hosting, err := hostOf(cfg, env, mode, orchestration)
	if err != nil {
		return forge.Set{}, err
	}
	if err := asksTheForge(cfg, hosting); err != nil {
		return forge.Set{}, err
	}
	set := forge.Set{}
	if hosting != nil {
		// One adapter plays all three parts by default, and it is the same one
		// for all of them: the tasks are the issues of the host and the checks
		// are the workflows of the host (§7g).
		set.Forge, set.Tracker, set.CI = hosting, hosting, hosting
		// The mode of the bot is the only one with an account of the host of its
		// own, and an account of the host is what may open the change request of a
		// run whose executor did not. A project in the mode of the owner opens it
		// as the person who runs crewflow, which is what a run has always done and
		// what a report of a run in that mode is expected to say (§7i).
		if mode == forge.ModeBot {
			set.Opener = hosting
		}
	}
	if cfg.Tracker.Kind != "forge" {
		return forge.Set{}, &forge.ErrNotImplemented{Key: "tracker.kind", Kind: cfg.Tracker.Kind}
	}
	switch {
	case cfg.CI.Kind == "forge":
	case cfg.CI.Kind == "none":
		set.CI = noChecks{}
	default:
		return forge.Set{}, &forge.ErrNotImplemented{Key: "ci.kind", Kind: cfg.CI.Kind}
	}
	return set, nil
}

// hostOf is the adapter of the host of the code, and nothing for a project that
// is not hosted anywhere: its change requests are local branches, and reading
// those is no adapter's work yet (§7g).
//
// The two modes of §7i are two accounts of the host and not one: the App of the
// executor, which a run works as, and the App of the orchestrator, which a review and
// a merge work as. A caller asks for the roles of one subject and gets the adapter
// built for that one, so that a review can never end up writing its record in the name
// of the executor and a run can never write anything at all in the name of the
// orchestrator.
func hostOf(cfg config.Config, env forge.Env, mode, orchestration string) (*github.Adapter, error) {
	switch cfg.Forge.Kind {
	case "github":
		adapter := github.New(cfg.Project.Repo, cfg.Forge.Host, env).
			WithDefaultBranch(cfg.Project.DefaultBranch).
			WithOrchestrator(orchestratorOf(cfg, env, orchestration))
		if mode == forge.ModeBot {
			// The numbers of the App are all the file of the project says about it:
			// the key of the App is in the store of the machine and never in a file
			// of a project (docs/DESIGN.md §7e, §7i).
			adapter = adapter.WithBot(github.Bot{
				AppID:          cfg.Identity.GitHubApp.AppID,
				InstallationID: cfg.Identity.GitHubApp.InstallationID,
				DefaultBranch:  cfg.Project.DefaultBranch,
			})
		}
		return adapter, nil
	case "none":
		return nil, nil
	default:
		return nil, &forge.ErrNotImplemented{Key: "forge.kind", Kind: cfg.Forge.Kind}
	}
}

// orchestratorOf is the App of the orchestrator of the project, and nothing where the
// orchestrator shares the login of the person: the zero AppID of [github.Orchestrator]
// is the shared login, and that is what a project gets without saying anything
// (docs/DESIGN.md §7i).
func orchestratorOf(cfg config.Config, env forge.Env, orchestration string) github.Orchestrator {
	if orchestration != forge.ModeSeparate {
		return github.Orchestrator{}
	}
	return github.Orchestrator{
		AppID:          cfg.Orchestrator.GitHubApp.AppID,
		InstallationID: cfg.Orchestrator.GitHubApp.InstallationID,
		API:            app.API(cfg.Forge.Host),
	}
}

// asksTheForge is the check for a project that takes its tasks or its checks
// from a host it does not have. [config.Load] refuses such a file already; this
// is the same refusal for settings that were built by hand, and it is a mistake
// in the file rather than an adapter crewflow has yet to write.
func asksTheForge(cfg config.Config, hosting *github.Adapter) error {
	if hosting != nil {
		return nil
	}
	for _, role := range []struct{ key, kind string }{
		{"tracker.kind", cfg.Tracker.Kind},
		{"ci.kind", cfg.CI.Kind},
	} {
		if role.kind == "forge" {
			return fmt.Errorf("%s: asks the forge for what it is, and forge.kind is %q: there is no forge to ask",
				role.key, cfg.Forge.Kind)
		}
	}
	return nil
}

// noChecks is the CI of a project that has none: the gates of the project are the
// only checks there are, and a commit of such a project is never red (§7g).
type noChecks struct{}

// Status says that there were no checks, whatever commit it is asked about.
func (noChecks) Status(context.Context, string) (forge.CheckState, error) {
	return forge.CheckNone, nil
}

// Doctor says nothing, because a program and a login of its own there are not.
func (noChecks) Doctor(context.Context) []forge.Check { return nil }

// The CI of a project without one is still a CI: the core asks all three roles
// the same questions, and one that is not there cannot answer.
var _ forge.CI = noChecks{}
