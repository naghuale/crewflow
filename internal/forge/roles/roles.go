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
)

// New returns the roles the settings of the project ask for, or an error that
// names the kind crewflow has no adapter for. The settings are those of a file
// that has been through [config.Load]: the pairs that contradict each other are
// refused there.
func New(cfg config.Config, env forge.Env) (forge.Set, error) {
	return rolesOf(cfg, env, cfg.Identity.Mode)
}

// AsOwner returns the roles of a project as the person who runs crewflow, whoever
// the executor of the project works as.
//
// A review of a change is the business of the orchestrator, and the orchestrator is
// the person: the record of a review is written in their name and counted only
// because it is, and the rules of a branch are rights of a person and not of the App
// of an executor, which has none of them (docs/DESIGN.md §7h, §7i). A project in the
// mode of the owner gets the same roles either way, and `task run` asks for the ones
// of [New].
func AsOwner(cfg config.Config, env forge.Env) (forge.Set, error) {
	return rolesOf(cfg, env, forge.ModeOwner)
}

// rolesOf is the roles of a project under the mode of the executor, which is what a
// run works under and what a review deliberately does not.
func rolesOf(cfg config.Config, env forge.Env, mode string) (forge.Set, error) {
	hosting, err := hostOf(cfg, env, mode)
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
func hostOf(cfg config.Config, env forge.Env, mode string) (*github.Adapter, error) {
	switch cfg.Forge.Kind {
	case "github":
		adapter := github.New(cfg.Project.Repo, cfg.Forge.Host, env).
			WithDefaultBranch(cfg.Project.DefaultBranch)
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
