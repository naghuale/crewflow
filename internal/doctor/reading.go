package doctor

import (
	"context"
	"fmt"
	"strings"

	"github.com/naghuale/crewflow/internal/access"
	"github.com/naghuale/crewflow/internal/config"
)

// The reading policy of a project is a part of the report and not a check of a
// program: what a person reads it for is what a run is about to be allowed to do, and
// the checks around it are about whether a program is there and works
// (docs/DESIGN.md §7d).
const (
	readName     = "access read"
	rejectedName = "access rejected"
)

// reading works out what the executor of the project may read outside the worktree of
// a task on this machine, and says it: the folders the commands of [access] named, and
// every path crewflow would not open with the reason why. A person sees here exactly
// what an agent of a run is about to be given, which is what a refusal of the next run
// is about.
func (c *checker) reading(ctx context.Context, cfg config.Config) {
	policy, problems := access.Resolve(ctx, c.readingEnv(), cfg.Access)
	// The lists of the report are written as they are, and an empty one is an empty
	// list and not a missing field: an orchestrator reads them and does not guess.
	c.access = Access{Read: orEmpty(policy.Read), Deny: orEmpty(policy.Deny)}
	if problems != nil {
		c.access.Rejected = problems
	} else {
		c.access.Rejected = []access.Problem{}
	}

	detail := "the project names no folder to read outside its worktree"
	if len(policy.Read) > 0 {
		detail = strings.Join(policy.Read, ", ")
	}
	c.add(Check{Name: readName, Status: OK, Detail: detail})
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

// orEmpty is a list of the report that is there and holds nothing, which is what a
// reader of `-json` needs to tell from a field that is not there at all.
func orEmpty(paths []string) []string {
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
