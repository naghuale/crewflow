package config

import (
	"fmt"
	"slices"
	"strings"
)

// Status is what crewflow does with the values of a key of the file of a
// project: it applies them, or the design describes them and the code is not
// written yet (docs/DESIGN.md §5).
type Status string

const (
	// Supported is the status of a key whose every value crewflow applies.
	Supported Status = "supported"
	// Specified is the status of a key with a value crewflow describes and does
	// not do: that value is refused, and the registry says which task writes it.
	Specified Status = "specified"
)

// Setting is one key of crewflow.toml: the values crewflow applies, the ones it
// refuses, and where the value of it is applied.
//
// The registry exists so that a setting cannot promise what the program does not
// do: a key is here with its status, a value that turns on a behaviour of the
// design that the code has not written is an error of the load with the task that
// writes it, and a key marked supported is a key the code outside this package
// really reads — the meta-test of the registry holds both halves to that.
type Setting struct {
	// Key is the key of the file as a person writes it, such as "isolation.mode".
	Key string `json:"key"`
	// Status is what crewflow does with the key.
	Status Status `json:"status"`
	// Supported are the values of the key crewflow applies, in the words of the
	// key itself. A key of no choice — a repository, a command line, a path — has
	// none: there is nothing to apply, and crewflow reads whatever it says.
	Supported []string `json:"supported,omitempty"`
	// Unwritten are the values of the key that turn on a behaviour of the design
	// the code does not have.
	Unwritten []Asked `json:"unwritten,omitempty"`
	// Applies is where outside this package the key is applied: the expression
	// that reads it, or, for a key crewflow does whatever it says, the function
	// that does what the value names. It is what the meta-test of the registry
	// looks for, and it is empty only for a key with an unwritten value.
	Applies string `json:"applies,omitempty"`
}

// Asked is one value of the file of a project that turns on a behaviour
// crewflow describes and does not do: the key, the value, the task that writes
// it and what to write instead (docs/DESIGN.md §5).
type Asked struct {
	// Key is the key of the file, as a person wrote it.
	Key string `json:"key"`
	// Value is the value that asks for the behaviour, in the words of the key: a
	// value of a key that is a choice, and a shape of a value for a key that is
	// not ("not empty", "more than 1").
	Value string `json:"value"`
	// Task is the issue that writes the behaviour, and 0 while no issue is open
	// for it.
	Task int `json:"task,omitempty"`
	// Instead is what to do instead of the value that is refused, in the words a
	// person reads under the line of the refusal.
	Instead string `json:"instead"`

	// asks reports whether the settings of a project ask for the behaviour, and
	// it is the whole of what the load knows about the value: a file that asks for
	// nothing is a file crewflow can work with.
	asks func(Config) bool
}

// String is one refusal in the words a person reads: what crewflow does not do,
// which task writes it, and what to write instead.
func (a Asked) String() string {
	refusal := fmt.Sprintf("%s = %q is described in docs/DESIGN.md and crewflow does not do it yet", a.Key, a.Value)
	if a.Task > 0 {
		refusal += fmt.Sprintf(" (crewflow#%d)", a.Task)
	} else {
		refusal += ", and no issue is open for it"
	}
	return fmt.Sprintf("%s; %s", refusal, a.Instead)
}

// settings is the registry of every key of crewflow.toml. A key is here with its
// status, the values crewflow applies and the ones it refuses; a key that is
// missing is a key nobody has promised anything about, and the meta-test of the
// registry fails until it is here (docs/DESIGN.md §5).
var settings = []Setting{
	// [project] — the project itself. Every command reads the repository out of
	// it, and a run and the gate read the branch, the language of the tasks and
	// the style of the commits.
	{Key: "project.repo", Status: Supported, Applies: "cfg.Project.Repo"},
	{Key: "project.default_branch", Status: Supported, Applies: "cfg.Project.DefaultBranch"},
	{Key: "project.language", Status: Supported, Applies: "cfg.Project.Language"},
	{Key: "project.commit_style", Status: Supported, Applies: "cfg.Project.CommitStyle"},

	// [forge] — the host of the code. GitHub is the one adapter crewflow has, so
	// every other kind is a kind DESIGN §7g describes and a task writes (#59 for
	// a project that is not hosted at all, #60 GitLab, #61 Bitbucket; Gitea and
	// Azure are described and no task is written for them yet).
	{
		Key: "forge.kind", Status: Specified, Supported: []string{"github"},
		Applies: "cfg.Forge.Kind",
		Unwritten: []Asked{
			{Key: "forge.kind", Value: "none", Task: 59, Instead: `put kind = "github" here, or take the key out`,
				asks: func(c Config) bool { return c.Forge.Kind == "none" }},
			{Key: "forge.kind", Value: "gitlab", Task: 60, Instead: `put kind = "github" here, or take the key out`,
				asks: func(c Config) bool { return c.Forge.Kind == "gitlab" }},
			{Key: "forge.kind", Value: "bitbucket", Task: 61, Instead: `put kind = "github" here, or take the key out`,
				asks: func(c Config) bool { return c.Forge.Kind == "bitbucket" }},
			{Key: "forge.kind", Value: "gitea", Instead: `put kind = "github" here, or take the key out`,
				asks: func(c Config) bool { return c.Forge.Kind == "gitea" }},
			{Key: "forge.kind", Value: "azure", Instead: `put kind = "github" here, or take the key out`,
				asks: func(c Config) bool { return c.Forge.Kind == "azure" }},
		},
	},
	{Key: "forge.host", Status: Supported, Applies: "cfg.Forge.Host"},

	// [tracker] — where the tasks come from. The tasks of the forge are the only
	// ones crewflow takes today; a tracker of its own is described in DESIGN §7g
	// (#59 files, #62 Jira; Linear is described and no task is written for it yet).
	// The key of the project in it is written down for a tracker that is not the
	// forge and nothing else, so a key crewflow has no adapter for is a key
	// nobody reads.
	{
		Key: "tracker.kind", Status: Specified, Supported: []string{"forge"},
		Applies: "cfg.Tracker.Kind",
		Unwritten: []Asked{
			{Key: "tracker.kind", Value: "files", Task: 59, Instead: `put kind = "forge" here, or take the key out`,
				asks: func(c Config) bool { return c.Tracker.Kind == "files" }},
			{Key: "tracker.kind", Value: "jira", Task: 62, Instead: `put kind = "forge" here, or take the key out`,
				asks: func(c Config) bool { return c.Tracker.Kind == "jira" }},
			{Key: "tracker.kind", Value: "linear", Instead: `put kind = "forge" here, or take the key out`,
				asks: func(c Config) bool { return c.Tracker.Kind == "linear" }},
		},
	},
	{
		Key: "tracker.project", Status: Specified,
		Unwritten: []Asked{{
			Key: "tracker.project", Value: "not empty",
			Instead: "take the key out: the tracker of a project is its host, and it knows project.repo",
			asks:    func(c Config) bool { return strings.TrimSpace(c.Tracker.Project) != "" },
		}},
	},

	// [identity] — whose name the executor of a run works under (§7i). Both modes
	// are what crewflow does: the login of the person, or an account of the host.
	{Key: "identity.mode", Status: Supported, Supported: []string{"owner", "bot"}, Applies: "cfg.Identity.Mode"},
	{Key: "identity.github_app.app_id", Status: Supported, Applies: "cfg.Identity.GitHubApp.AppID"},
	{
		Key: "identity.github_app.installation_id", Status: Supported,
		Applies: "cfg.Identity.GitHubApp.InstallationID",
	},

	// [executor] — the agent a run works with (§7b). The command line, the model
	// and the time limit are what a run and the probe of doctor use; a list of
	// executors to try in turn is what a run does not do yet (#51), so a file
	// with one is refused rather than left to look as if it were a plan.
	{
		Key: "executor.command", Status: Supported,
		Applies: "cfg.Executor.ExecutorSpec.Command",
	},
	{Key: "executor.model", Status: Supported, Applies: "spec.Model"},
	{Key: "executor.model_flag", Status: Supported, Applies: "spec.ModelFlag"},
	{Key: "executor.timeout", Status: Supported, Applies: "cfg.Executor.Timeout"},
	{
		Key: "executor.fallback", Status: Specified,
		Unwritten: []Asked{{
			Key: "executor.fallback", Value: "not empty", Task: 51, Instead: "put fallback = [] here, or take the key out",
			asks: func(c Config) bool { return len(c.Executor.Fallback) > 0 },
		}},
	},

	// [access] — what the executor may read outside the worktree of its task
	// (§7d). Crewflow runs the commands itself and reads the paths before the
	// agent starts, and it keeps the places that hold secrets closed whatever the
	// project wrote.
	{Key: "access.read_from", Status: Supported, Applies: "cfg.ReadFrom"},
	{Key: "access.read", Status: Supported, Applies: "cfg.Read"},

	// [worktrees] — where the worktree of a task is made, outside the folder of
	// the person (§8).
	{Key: "worktrees.root", Status: Supported, Applies: "cfg.Worktrees.Root"},

	// [[gates]] — the commands of the project that have to pass before a change
	// is reviewed (§6). The name and the command line are what a run hands to the
	// agent and what doctor looks the programs of; a gate that must not run
	// together with another task's is a gate of parallel tasks, which crewflow
	// does not do yet (#56).
	{Key: "gates[].name", Status: Supported, Applies: "gate.Name"},
	{Key: "gates[].run", Status: Supported, Applies: "gate.Run"},
	{
		Key: "gates[].exclusive", Status: Specified,
		Unwritten: []Asked{{
			Key: "gates[].exclusive", Value: "true", Task: 56,
			Instead: "take the key out until tasks run one at a time",
			asks: func(c Config) bool {
				return slices.ContainsFunc(c.Gates, func(g Gate) bool { return g.Exclusive })
			},
		}},
	},

	// [ci] — what crewflow expects of the checks on the commit (§7h). The kind
	// crewflow asks the host for and the "none" of a project without checks are
	// what the roles build; a command of its own and Jenkins are described in
	// DESIGN §7g (#59 for the command; Jenkins has no task yet).
	{
		Key: "ci.kind", Status: Specified, Supported: []string{"forge", "none"},
		Applies: "cfg.CI.Kind",
		Unwritten: []Asked{
			{Key: "ci.kind", Value: "command", Task: 59, Instead: `put kind = "forge" here, or take the key out`,
				asks: func(c Config) bool { return c.CI.Kind == "command" }},
			{Key: "ci.kind", Value: "jenkins", Instead: `put kind = "forge" here, or take the key out`,
				asks: func(c Config) bool { return c.CI.Kind == "jenkins" }},
		},
	},
	{Key: "ci.required", Status: Supported, Applies: "cfg.CI.Required"},
	{Key: "ci.timeout", Status: Supported, Applies: "cfg.CI.Timeout"},

	// [merge] — who merges an approved change and how (§7h). The orchestrator
	// runs the one merge crewflow has and the strategy is the fast-forward that
	// merge does; a key that names anybody else promises a merge of a mode crewflow
	// has not, and a merge through the API of the host is a task of its own (#58).
	{
		Key: "merge.by", Status: Specified, Supported: []string{"orchestrator"},
		Unwritten: []Asked{
			{Key: "merge.by", Value: "executor", Instead: `put by = "orchestrator" here, or take the key out`,
				asks: func(c Config) bool { return c.Merge.By == "executor" }},
			{Key: "merge.by", Value: "human", Instead: `put by = "orchestrator" here, or take the key out`,
				asks: func(c Config) bool { return c.Merge.By == "human" }},
		},
	},
	// The only strategy crewflow does is the fast-forward of exactly one commit,
	// and merge.Run is where that happens: the key names it, nothing reads it, and
	// every other value is refused by Validate.
	{Key: "merge.strategy", Status: Supported, Supported: []string{"ff-only"}, Applies: "merge.Run"},
	{
		Key: "merge.via", Status: Specified, Supported: []string{"git-push"},
		Unwritten: []Asked{{
			Key: "merge.via", Value: "forge", Task: 58, Instead: `put via = "git-push" here, or take the key out`,
			asks: func(c Config) bool { return c.Merge.Via == "forge" },
		}},
	},
	// Who counts as an approving reviewer is what the gate is given (§7h).
	{Key: "merge.reviewers", Status: Supported, Applies: "cfg.Merge.Reviewers"},

	// [parallel] — how many tasks run at once (§7c). Crewflow runs one at a time,
	// and a second one asks for tasks without crossing files (#56).
	{
		Key: "parallel.max_tasks", Status: Specified, Supported: []string{"1"},
		Unwritten: []Asked{{
			Key: "parallel.max_tasks", Value: "more than 1", Task: 56, Instead: "put max_tasks = 1 here, or take the key out",
			asks: func(c Config) bool { return c.Parallel.MaxTasks > 1 },
		}},
	},

	// [tasks] — whose approval a task waits for (§7f). Every value of it is what
	// task.CheckReady asks about before a run starts.
	{
		Key: "tasks.owner_approval", Status: Supported,
		Supported: []string{"all", "risky", "none"},
		Applies:   "cfg.Tasks.OwnerApproval",
	},

	// [[requirements]] — the tools of the project and how each of them is checked;
	// doctor asks every one of them and compares what it printed with the minimum
	// the project wrote (§7d).
	{Key: "requirements.tools[].name", Status: Supported, Applies: "tool.Name"},
	{Key: "requirements.tools[].check", Status: Supported, Applies: "tool.Check"},
	{Key: "requirements.tools[].min", Status: Supported, Applies: "tool.Min"},

	// [isolation] — how far the agent is kept from the rest of the machine (§7d).
	// A run happens on the machine of the person, which is what "host" says; a
	// sandbox (#55) and a container (#57) are described and not written.
	{
		Key: "isolation.mode", Status: Specified, Supported: []string{"host"},
		Unwritten: []Asked{
			{Key: "isolation.mode", Value: "sandbox", Task: 55, Instead: `put mode = "host" here, or take the key out`,
				asks: func(c Config) bool { return c.Isolation.Mode == "sandbox" }},
			{Key: "isolation.mode", Value: "container", Task: 57, Instead: `put mode = "host" here, or take the key out`,
				asks: func(c Config) bool { return c.Isolation.Mode == "container" }},
		},
	},

	// [[capabilities]] — access to secrets and outside systems a project may need
	// (§7e). A declaration grants nothing today: there is no way to give it and no
	// way to ask the human about it (#54), and a file that declares one is refused
	// rather than read as a promise crewflow keeps.
	{
		Key: "capabilities[].name", Status: Specified,
		Unwritten: []Asked{{
			Key: "capabilities[].name", Value: "a declared capability", Task: 54,
			Instead: "take the [[capabilities]] tables out of the file",
			asks:    func(c Config) bool { return len(c.Capabilities) > 0 },
		}},
	},
	{
		Key: "capabilities[].use", Status: Specified,
		Unwritten: []Asked{{
			Key: "capabilities[].use", Value: "a declared capability", Task: 54,
			Instead: "take the [[capabilities]] tables out of the file",
			asks:    func(c Config) bool { return len(c.Capabilities) > 0 },
		}},
	},
	{
		Key: "capabilities[].actions", Status: Specified,
		Unwritten: []Asked{{
			Key: "capabilities[].actions", Value: "a declared capability", Task: 54,
			Instead: "take the [[capabilities]] tables out of the file",
			asks:    func(c Config) bool { return len(c.Capabilities) > 0 },
		}},
	},
}

// NotImplemented is the refusal of a file that asks for a behaviour crewflow
// describes and does not do. It carries every setting that asks, so that a
// person fixes them in one pass and a report shows the whole list (docs/DESIGN.md §5).
type NotImplemented struct {
	// Settings are the values of the file that ask for it, in the order of the
	// registry.
	Settings []Asked
}

// Error is the refusal in one line: what is not written, which task writes it,
// and what to write instead.
func (e *NotImplemented) Error() string {
	refusals := make([]string, 0, len(e.Settings))
	for _, setting := range e.Settings {
		refusals = append(refusals, setting.String())
	}
	return strings.Join(refusals, "; ")
}

// asked returns the settings of the file that ask for a behaviour crewflow has not
// written, in the order of the registry.
func (c Config) asked() []Asked {
	var asked []Asked
	for _, setting := range settings {
		for _, unwritten := range setting.Unwritten {
			if unwritten.asks(c) {
				asked = append(asked, unwritten)
			}
		}
	}
	return asked
}
