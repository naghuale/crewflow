package doctor

import (
	"context"
	"fmt"
	"strings"

	"github.com/naghuale/crewflow/internal/access"
	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/run/profile"
)

// The reading policy of a project is a part of the report and not a check of a
// program: what a person reads it for is what a run is about to be allowed to do, and
// the checks around it are about whether a program is there and works
// (docs/DESIGN.md §7d).
const (
	readName     = "access read"
	rejectedName = "access rejected"
	rightsName   = "executor rights"
)

// reading works out what the executor of the project may read outside the worktree of
// a task on this machine, and says it: every folder with the ask behind it — the hand
// it came from and the reason it was made — and every path crewflow would not open with
// the reason why. A person sees here exactly what an agent of a run is about to be
// given, which is what a refusal of the next run is about, and where each folder of it
// came from, which is what a run that was given more than the project asked for is
// about (docs/DESIGN.md §7d).
func (c *checker) reading(ctx context.Context, cfg config.Config) {
	policy, problems := access.Resolve(ctx, c.readingEnv(), cfg.Access, nil)
	// The lists of the report are written as they are, and an empty one is an empty
	// list and not a missing field: an orchestrator reads them and does not guess.
	c.access = Access{Read: orEmpty(policy.Read), Deny: orEmptyPaths(policy.Deny)}
	if problems != nil {
		c.access.Rejected = problems
	} else {
		c.access.Rejected = []access.Problem{}
	}

	detail := "the project names no folder to read outside its worktree"
	if len(policy.Read) > 0 {
		detail = strings.Join(described(policy.Read), ", ")
	}
	c.add(Check{Name: readName, Status: OK, Detail: detail})
	c.ownRights(cfg)
	if len(problems) == 0 {
		return
	}
	refused := make([]string, 0, len(problems))
	for _, problem := range problems {
		refused = append(refused, fmt.Sprintf("%s: %s", problem.Path, problem.Reason))
	}
	// A path that was refused is not a machine that cannot work: the run goes on
	// without it, and an agent of the run is refused it if it asks. It is worth a look
	// all the same, because the project asked for something and did not get it.
	c.add(Check{
		Name:   rejectedName,
		Status: Warn,
		Detail: strings.Join(refused, "; "),
		Hint:   fmt.Sprintf("change the paths of [access] in %s, or ask a person what the agent may read", c.env.ConfigPath),
	})
}

// ownRights is that the rights of a run come from crewflow and from nothing else. An
// agent that merges a file of the person into the settings of a run has rights that
// were never written by crewflow, in a file that is not in the repository and not in
// the review of a task, and `crewflow task run` refuses such a run before it starts
// (docs/DESIGN.md §7d). The report says so where a person will see it before a task
// is run and not in the middle of it.
func (c *checker) ownRights(cfg config.Config) {
	agent := profile.For(cfg.Executor.ExecutorSpec.Command)
	name := fmt.Sprintf("the executor %q takes its rights from the files of the person only", agent.Name())
	var held []string
	for _, rights := range agent.ForeignRights(c.env.Home, c.env.Environ) {
		if !rights.HoldsRights() {
			continue
		}
		held = append(held, fmt.Sprintf("%s (%s)", rights.Path, rights.Says()))
	}
	if len(held) == 0 {
		c.add(Check{Name: rightsName, Status: OK, Detail: name})
		return
	}
	c.add(Check{
		Name:   rightsName,
		Status: Warn,
		Detail: name + ": " + strings.Join(held, ", "),
		Hint: "take the table `permission` out of that file, or out of the folder it is in: " +
			"crewflow writes the rights of a run itself, and crewflow task run refuses to start one while they are there",
	})
}

// described is every folder of the access of a run as one line of a report reads it:
// the path, the hand that asked for it and the reason, because a path in a report
// says nothing about who wanted it and a person deciding about a run has to tell a
// folder the project needs from a folder one task asked for.
func described(grants []access.Grant) []string {
	lines := make([]string, 0, len(grants))
	for _, grant := range grants {
		lines = append(lines, grant.Line())
	}
	return lines
}

// orEmpty is a list of the report that is there and holds nothing, which is what a
// reader of `-json` needs to tell from a field that is not there at all.
func orEmpty(grants []access.Grant) []access.Grant {
	if grants == nil {
		return []access.Grant{}
	}
	return grants
}

// orEmptyPaths is a list of places of the report that is there and holds nothing.
func orEmptyPaths(paths []string) []string {
	if paths == nil {
		return []string{}
	}
	return paths
}

// readingEnv is the machine as the policy of reading needs it: the home of the person
// and the way a command of the project is started, the two of which doctor already
// has for every other check it makes.
func (c *checker) readingEnv() access.Env {
	return access.Env{
		Home: c.env.Home,
		Run: func(ctx context.Context, name string, args []string) ([]byte, []byte, int, error) {
			return c.env.Run(ctx, name, args, "", nil)
		},
	}
}
