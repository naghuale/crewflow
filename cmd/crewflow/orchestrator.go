package main

import (
	"context"
	"fmt"
	"slices"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/doctor"
	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/forge/roles"
	"github.com/naghuale/crewflow/internal/merge"
)

// resets are the settings of git whose value the environment of the commands of git takes to
// nothing before the settings the adapter of the host named: an empty `credential.helper` takes
// the list of helpers out of the files of the system and of the user, and the helper of the
// machine is the one that answers first — `osxkeychain` of the file of the system of macOS has
// the token of the login of a person, and a push of a merge signed with it would be that login
// all over again (F-116, R6, #141). `credential.useHttpPath` is reset by the same rule and not
// out of need: one rule for every setting crewflow writes into the environment of a push is a
// rule nobody has to remember setting by setting.
//
// Nothing outside that environment is reset: the settings of the machine and the ones of
// the repository are the settings of the person, and crewflow writes neither of them
// (docs/DESIGN.md §7a, §7i).
var resets = []string{"credential.helper", "credential.useHttpPath"}

// The machine a review and a merge are made of: the roles of the project as its
// orchestrator, and the way git is started. They are variables so that a test of a
// command runs against a host and a repository of its own and reaches neither the
// network nor a checkout of the person who runs it (docs/DESIGN.md §7h).
var (
	reviewRoles = roles.AsOrchestrator
	mergeRoles  = roles.AsOrchestrator
	// gitOf is the git of a review and of a merge: the git of the machine, every
	// command of it started with the environment the project works under. It is a
	// function of that environment and a variable, because the environment depends on
	// the mode of §7i and because a test of a command puts a git of its own in its
	// place — and then sees the environment every command was started with (§7a, §7i).
	gitOf = gitIn
)

// startProgram starts a program of the machine in a folder, with a closed stdin and the
// environment added to the one of the process — the way doctor starts every program of a
// machine (§7a). It is a variable so that a test of a command never starts a program of
// the person who runs it.
var startProgram = doctor.Command

// gitIn is the git of the machine with an environment added to every command of it.
// The environment is empty for a project whose orchestrator shares the login of the
// person, and for a project whose orchestrator works apart from the owner it holds the
// settings of git that point it at the helper of the credentials of crewflow: the push
// of a merge is made as the account of the app, and a checkout of a person is never
// written to make it so (docs/DESIGN.md §7a, §7h, §7i).
func gitIn(environment []string) merge.Runner {
	return func(ctx context.Context, name string, args []string, dir string) ([]byte, []byte, int, error) {
		return startProgram(ctx, name, args, dir, environment)
	}
}

// orchestratorOf is whose name the review and the merge of this project work under, and
// the one line a report of either of them shows: the same line `crewflow doctor` shows
// for the same project, so that a person does not have to remember which of the two
// commands told them the truth (docs/DESIGN.md §7i).
func orchestratorOf(ctx context.Context, set forge.Set) (forge.Identity, error) {
	if set.Forge == nil {
		return forge.Identity{}, nil
	}
	return forge.DescribeOrchestrator(ctx, set.Forge)
}

// reviewersOf are the subjects whose record of a review counts: the accounts the file of the
// project names, and — where it names none and the orchestrator works as an account of the
// host of its own — the account it works as, because a record of a review in that mode is
// written by it and by nobody else (docs/DESIGN.md §5, §7h, §7i).
//
// An account the host would not name is an error and not the owner of the repository:
// a gate that counted the approvals of the owner while the orchestrator wrote records
// under its own account would count nothing, and would say so as though the owner had
// not approved anything.
func reviewersOf(ctx context.Context, cfg config.Config, set forge.Set) ([]forge.Subject, error) {
	reviewers, err := roles.ReviewersOf(ctx, cfg, set)
	if err != nil {
		return nil, err
	}
	// The accounts of the owner and the accounts of the reviewers are told apart by the
	// mode of the orchestrator, and the mode of a separate login refuses a file that
	// makes them one: a record of a review that the gate would count as a decision of
	// the owner is not a review, it is the owner accepting his own work (docs/DESIGN.md
	// §7i, §7k). The load of the file sees the lists as they are written; here they are
	// the ones a review is written and counted under, and the account of an App is in no
	// file — so the rule is asked again with what the host answered.
	// The rule of §7i has nothing to say in the shared mode — one login is both subjects
	// in it by definition — so the owners are asked for only where the mode promises a
	// division the accounts of the file do not make.
	if cfg.Orchestrator.Mode != config.ModeSeparate {
		return reviewers, nil
	}
	owners, err := roles.OwnersOf(ctx, cfg, set)
	if err != nil {
		return nil, err
	}
	if refused := config.Separation(cfg.Orchestrator.Mode, loginsOf(owners), loginsOf(reviewers), cfg.Project.Repo); refused != nil {
		return nil, refused
	}
	return reviewers, nil
}

// ownersOf are the subjects whose records are the decisions of a person, with the default of
// §5 filled in the same way as the reviewers.
func ownersOf(ctx context.Context, cfg config.Config, set forge.Set) ([]forge.Subject, error) {
	return roles.OwnersOf(ctx, cfg, set)
}

// loginsOf are the names the host writes the accounts under, as the rule of §7i is checked
// against the file of the project as it is written; the subject behind each name is in the
// section of `crewflow doctor` and in every refusal of the gate.
func loginsOf(accounts []forge.Subject) []string {
	names := make([]string, 0, len(accounts))
	for _, account := range accounts {
		names = append(names, account.Login)
	}
	return names
}

// gitEnvironment is what the commands of git of a review and of a merge are started
// with: the settings the adapter of the host named for the account the orchestrator
// works as, and nothing else (docs/DESIGN.md §7a, §7i).
func gitEnvironment(ctx context.Context, set forge.Set) []string {
	if set.Forge == nil {
		return nil
	}
	described, ok := set.Forge.(forge.OrchestratorDescribed)
	if !ok {
		return nil
	}
	// The line of the mode and the settings of git are asked without a token of it: a
	// push is signed by the helper of the credentials, which signs its own token for
	// that one push, and a token in the environment of every command of git would be a
	// token in the journal of a merge of a test as well (§7e, §7i).
	identity, err := described.DescribeOrchestrator(ctx)
	if err != nil {
		return nil
	}
	return gitConfig(identity.GitConfig)
}

// gitConfig is a table of settings of git as the environment of a command carries it:
// `GIT_CONFIG_COUNT` and the pairs `GIT_CONFIG_KEY_n` and `GIT_CONFIG_VALUE_n`, which git
// reads as if they were given with `-c` and which therefore hold for that one command
// and for nothing else (docs/DESIGN.md §7a).
//
// A list is reset before crewflow's own: the settings of the repository or of the
// machine may name a helper that answers with the token of the login of a person, and
// a push of a merge that was signed with it would be the login of the person all over
// again — which is the very thing the mode of §7i is for (§7i, F-116).
func gitConfig(settings map[string]string) []string {
	if len(settings) == 0 {
		return nil
	}
	keys := make([]string, 0, len(settings))
	count := 0
	for key := range settings {
		keys = append(keys, key)
		count += len(valuesOf(key, settings[key]))
	}
	slices.Sort(keys)
	environment := []string{fmt.Sprintf("GIT_CONFIG_COUNT=%d", count)}
	n := 0
	for _, key := range keys {
		for _, value := range valuesOf(key, settings[key]) {
			environment = append(environment,
				fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", n, key),
				fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", n, value))
			n++
		}
	}
	return environment
}

// valuesOf are the values one setting of a push is given, in the order git reads them: the
// empty value first for a setting of [resets], and the value the adapter of the host named
// after it.
func valuesOf(key, value string) []string {
	if !slices.Contains(resets, key) {
		return []string{value}
	}
	return []string{"", value}
}

// saidOrchestrator is the line of the mode of the orchestrator as a person reads it
// before a record of a review is written under a change: which of the two modes the
// project works under, and what that means for the record (docs/DESIGN.md §7h, §7i).
func saidOrchestrator(identity forge.Identity) string {
	if identity.Description == "" {
		return ""
	}
	return "crewflow review: " + identity.Description + "\n"
}
