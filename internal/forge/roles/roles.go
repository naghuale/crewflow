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
	"slices"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/forge/github"
	"github.com/naghuale/crewflow/internal/forge/github/app"
	"github.com/naghuale/crewflow/internal/network"
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
	return rolesOf(cfg, env, cfg.Identity.Mode, forge.ModeShared, WithKeys)
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
	return rolesOf(cfg, env, forge.ModeOwner, forge.ModeShared, WithKeys)
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
	return rolesOf(cfg, env, forge.ModeOwner, cfg.Orchestrator.Mode, WithKeys)
}

// ToShow returns the roles of the project as a command that only shows its state sees
// them: the roles of the orchestrator, and nothing of the secrets of the machine in them.
// A command that shows a person what is going on has no business asking the keychain of
// macOS about the key of an App — on a new build of the program that window opens for
// every read and nobody is at it, and a queue of what wants a person stands in front of
// it for two minutes at a time (docs/DESIGN.md §6a, §7i).
//
// What such a command needs of the host it gets from the login of the person, which reads
// everything a command that only looks reads: the numbers of the two Apps of the project
// are in the file of the project, and the numbers a gate counts records by are in the
// answers of the host whatever account asks. So the ways are the same and the only thing
// that changes is who is asked — the same subjects, from the same host, without a token
// in a single environment (§7h, §7i).
func ToShow(cfg config.Config, env forge.Env) (forge.Set, error) {
	return rolesOf(cfg, env, forge.ModeOwner, cfg.Orchestrator.Mode, ByNumber)
}

// A Way is what the roles of a project are built out of: the keys of the Apps of the
// project, which the machine keeps in the store of its secrets, or nothing but the numbers
// of those Apps, which the file of the project holds. It is the way a caller builds them
// in and not a mode of the project: one project is read both ways on the same morning
// (docs.DESIGN.md §7i).
type Way int

const (
	// WithKeys builds the roles with the store of the secrets of the machine in them, and
	// the store is where a run finds the key of its App and a review the key of the App of
	// the orchestrator: both act in the name of an account of the host, and an act of that
	// kind is signed with a token of an hour (§7i).
	WithKeys Way = iota
	// ByNumber builds the roles with the numbers of the Apps of the file of the project and
	// with no store at all: every account of the lists of the gate is counted by a number
	// the host publishes, and a number is not a secret. The adapter of a project built this
	// way asks the host as the person and refuses to write (§7i).
	ByNumber
)

// rolesOf is the roles of a project under the mode of the executor, which is what a
// run works under, and under the mode of the orchestrator, which is what a review and
// a merge work under. A run never has the second one and a review never the first:
// that each of them has an account of its own, or the login of the person, is the
// whole of §7i.
func rolesOf(cfg config.Config, env forge.Env, mode, orchestration string, way Way) (forge.Set, error) {
	// Every program the roles of the project start goes out through the route of the
	// project: gh is a program of the machine like any other, and a project whose owner
	// named a proxy expects the tasks, the reviews and the checks to be read through it
	// as well as the code. Nothing else changes — the route is added to the environment
	// of the child process and to no setting of this one (docs/DESIGN.md §7d).
	env = throughRoute(cfg, env)
	if env.RouteError != nil {
		return forge.Set{}, env.RouteError
	}
	hosting, err := hostOf(cfg, env, mode, orchestration, way)
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
//
// The way is what the adapter is built out of, and the numbers of the Apps of the
// project are in it either way: they are what the file of the project says, and a gate
// counts records by them. What the way [ByNumber] takes away is the store of the secrets
// of the machine, so there is no key in the adapter to read and no token to mint (§7i).
func hostOf(cfg config.Config, env forge.Env, mode, orchestration string, way Way) (*github.Adapter, error) {
	if way == ByNumber {
		env = withoutSecrets(env)
	}
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
		if way == ByNumber {
			adapter = adapter.ToLook()
		}
		return adapter, nil
	case "none":
		return nil, nil
	default:
		return nil, &forge.ErrNotImplemented{Key: "forge.kind", Kind: cfg.Forge.Kind}
	}
}

// withoutSecrets is the machine of a caller that must not read the keys of the Apps of
// the project: the same programs, the same way of starting them and the same clock, and
// no store of secrets at all. An adapter built on it holds no store, so nothing in it can
// read a key even by mistake — and a read of a key is a question to the keychain of macOS,
// and that question opens a window of the system on a build nobody has trusted yet
// (docs.DESIGN.md §7i).
func withoutSecrets(env forge.Env) forge.Env {
	env.Secrets = nil
	return env
}

// throughRoute is the machine of the roles of a project with the route of the project
// around every program it starts: the environment of that route is added to each of them,
// and in the mode `fallback` the one operation that could not reach the network is made
// once more through the active profile of the project (docs/DESIGN.md §7d).
//
// The credentials of a profile are read where a connection through it is really made and
// nowhere else: for the route the project goes out by it is read here, before the first
// program is started, because a profile whose credentials are missing is a refusal a
// person has to be told about before a task starts; for the profile of the second attempt
// it is read when that attempt is being made, because until then nobody has asked whether
// there is a second attempt at all (docs/DESIGN.md §7d, §7e).
//
// A route that cannot be worked out is not a route the roles can be built on: gh would
// then go out the way the shell of the machine happened to leave it, and a report would
// say the project cannot be read while the answer would be a proxy of somebody else.
func throughRoute(cfg config.Config, env forge.Env) forge.Env {
	route, err := firstRoute(cfg, env)
	if err != nil {
		env.RouteError = err
		return env
	}
	machine := network.Fallback{
		Config:  cfg,
		Route:   route,
		Secrets: env.Secrets,
		Now:     env.Now,
		Events:  env.Events,
	}
	run := env.Run
	env.Run = func(ctx context.Context, name string, args []string, dir string, extra []string) ([]byte, []byte, int, error) {
		// The route is put behind what the adapter of the host asked for and never into it:
		// the token of the App of the project is the account the second attempt works under
		// as well, because another road is not another right (SEC-NET-009, §7i).
		answer := machine.Once(ctx, func(_ network.Route, ofRoute []string) (string, string, int, error) {
			stdout, stderr, code, err := run(ctx, name, args, dir, slices.Concat(extra, ofRoute))
			return string(stdout), string(stderr), code, err
		})
		return []byte(answer.Stdout), []byte(answer.Stderr), answer.Code, answer.Err
	}
	return env
}

// firstRoute is the route the programs of the roles go out by, and every refusal of a route
// nobody can work out: no profile named, a profile that is in no table of the project, and
// credentials of a profile that are not in the store of the machine or are not a login and a
// password in it. It is worked out before the first program of the roles is started, because a
// refusal a person has to act on belongs before a task starts and not in the middle of one
// (docs/DESIGN.md §7d, §7e).
func firstRoute(cfg config.Config, env forge.Env) (network.Route, error) {
	route, err := network.Choose(cfg, "")
	if err != nil {
		return network.Route{}, err
	}
	credentials, err := network.Credentials(env.Secrets, route)
	if err != nil {
		return network.Route{}, err
	}
	if _, err := route.Environment(cfg.Network.NoProxy, credentials); err != nil {
		return network.Route{}, err
	}
	return route, nil
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

// ReviewersOf are the subjects whose record of a review of a change of this project is
// an approval, in the order the rules of §5 and §7h give them: the accounts the file
// names, the account the App of the orchestrator works as where the file names none and
// the orchestrator has an account of its own, and the owner of the repository where
// neither of the two says anything.
//
// The second of the three is why this question is asked of the host and not read out of
// the file: the account of an App is not in the file of the project, and a gate that was
// told a different one would count nothing (docs/DESIGN.md §7h, §7i).
func ReviewersOf(ctx context.Context, cfg config.Config, set forge.Set) ([]forge.Subject, error) {
	if len(cfg.Merge.Reviewers) > 0 {
		return subjectsOf(ctx, "merge.reviewers", cfg.Merge.Reviewers, set)
	}
	if cfg.Orchestrator.Mode != config.ModeSeparate {
		return ownersOf(ctx, "merge.reviewers", nil, cfg.Project.Repo, set)
	}
	if set.Forge == nil {
		return nil, fmt.Errorf("orchestrator.mode: this project has no host of its own, so there is no account " +
			"to be the orchestrator apart from the owner in")
	}
	subject, err := forge.SigningAs(ctx, set.Forge)
	if err != nil {
		return nil, err
	}
	if subject.Kind == forge.KindUser {
		return nil, fmt.Errorf("the host of the project named %q as the account the orchestrator works as, and that "+
			"is a person: in the mode of a separate login the orchestrator works as an app of its own, and the "+
			"record of a review is written in the name of that app", subject.Login)
	}
	return []forge.Subject{subject}, nil
}

// OwnersOf are the subjects whose records are the decisions of a person and not the
// work of the orchestrator: the accounts the file names, and the owner of the repository
// where it names none. The record of an acceptance of a result and the record of a scope
// acceptance are counted from this one list (docs/DESIGN.md §5, §7h).
func OwnersOf(ctx context.Context, cfg config.Config, set forge.Set) ([]forge.Subject, error) {
	return ownersOf(ctx, "merge.owners", cfg.Merge.Owners, cfg.Project.Repo, set)
}

// ownersOf are the subjects of the accounts the file names under the key, with the
// default of §5 filled in first: a list the file leaves empty has the owner of its
// repository instead. That owner is named in the file of nothing — a repository names it
// by a login — and the number of that account is the only thing a record may be counted
// by, so every name is asked of the host here once, and the gate is given subjects from
// then on (docs.DESIGN.md §5, §7i).
func ownersOf(ctx context.Context, key string, list []string, repository string, set forge.Set) ([]forge.Subject, error) {
	return subjectsOf(ctx, key, config.OwnersOf(list, repository), set)
}

// subjectsOf are the subjects of the accounts the file of the project names under the
// key, in the order it names them in. A login the host does not know, or holds as a kind
// crewflow does not read, is an error naming the key and the login: a file that names an
// account nobody may write for is a mistake in that file, and a gate that went on without
// it would count no records at all while saying that nobody had approved anything
// (docs/DESIGN.md §7h, §7i).
func subjectsOf(ctx context.Context, key string, logins []string, set forge.Set) ([]forge.Subject, error) {
	if len(logins) == 0 {
		return nil, nil
	}
	if set.Forge == nil {
		return nil, fmt.Errorf("%s: this project has no host of its own, so it cannot say which account %q is",
			key, logins[0])
	}
	subjects := make([]forge.Subject, 0, len(logins))
	for i, login := range logins {
		subject, err := forge.SubjectOf(ctx, set.Forge, login)
		if err != nil {
			return nil, fmt.Errorf("%s[%d]: %w", key, i, err)
		}
		subjects = append(subjects, subject)
	}
	return subjects, nil
}

// ExecutorOf is the App of the executor of a run of this project, and nothing where the
// executor works as the person who runs crewflow: a record of the executor is not an
// approval and not a decision of the owner whatever the lists of the file name, and the
// number of that App is what tells it apart from every other account of the host
// (docs/DESIGN.md §7h, §7i).
func ExecutorOf(cfg config.Config) forge.Subject {
	if cfg.Identity.Mode != forge.ModeBot {
		return forge.Subject{}
	}
	return forge.Subject{Kind: forge.KindApp, ID: cfg.Identity.GitHubApp.AppID}
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
