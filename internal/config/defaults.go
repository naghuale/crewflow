package config

import "github.com/BurntSushi/toml"

// Defaults of the keys a project may leave out. None of them names an agent, a
// model or a language of the host: a project says all of that itself, and
// crewflow only fills in the parts that mean the same everywhere.
const (
	defaultBranch          = "main"
	defaultLanguage        = "en"
	defaultWorktreesRoot   = "~/.crewflow/worktrees/{repo}"
	defaultExecutorTimeout = "90m"
	defaultForgeKind       = "github"
	defaultTrackerKind     = "forge"
	defaultIdentityMode    = "owner"
	defaultCIKind          = "forge"
	defaultCIRequired      = true
	defaultCITimeout       = "30m"
	defaultMergeBy         = "orchestrator"
	defaultMergeStrategy   = "ff-only"
	defaultMergeVia        = "git-push"
	defaultMaxTasks        = 1
	defaultIsolationMode   = "host"
	defaultOwnerApproval   = "risky"
	defaultAcceptanceLabel = "owner-check"
)

// DefaultWorktreesRoot is where the worktree of a task is made when the file of
// the project does not say: outside the folder of the person, whatever the
// worktrees of a run end up doing (docs/DESIGN.md §8). It is exported because a
// caller that builds the settings of a project by hand must land on the same
// place as one that reads them from a file.
const DefaultWorktreesRoot = defaultWorktreesRoot

// applyDefaults fills in what the file does not say. A key written as false or as
// 0 is a decision, not a missing key, so for those the file itself is asked
// whether the key is there at all.
func (c *Config) applyDefaults(meta *toml.MetaData) {
	if c.Project.DefaultBranch == "" {
		c.Project.DefaultBranch = defaultBranch
	}
	if c.Project.Language == "" {
		c.Project.Language = defaultLanguage
	}
	if c.Forge.Kind == "" {
		c.Forge.Kind = defaultForgeKind
	}
	if c.Tracker.Kind == "" {
		c.Tracker.Kind = defaultTrackerKind
	}
	// The executor works as the person who runs crewflow until a project says
	// otherwise: a mode of a bot is something a project sets up on purpose, with an
	// app of its own and a key of its own (docs/DESIGN.md §7i).
	if c.Identity.Mode == "" {
		c.Identity.Mode = defaultIdentityMode
	}
	if c.Executor.Timeout == "" {
		c.Executor.Timeout = defaultExecutorTimeout
	}
	// A fallback executor that says nothing about the time limit gets the one of
	// the executor it stands in for.
	for i := range c.Executor.Fallback {
		if c.Executor.Fallback[i].Timeout == "" {
			c.Executor.Fallback[i].Timeout = c.Executor.Timeout
		}
	}
	if c.Worktrees.Root == "" {
		c.Worktrees.Root = defaultWorktreesRoot
	}
	if !meta.IsDefined("ci", "required") {
		c.CI.Required = defaultCIRequired
	}
	if c.CI.Timeout == "" {
		c.CI.Timeout = defaultCITimeout
	}
	if c.CI.Kind == "" {
		c.CI.Kind = defaultCIKind
	}
	if c.Merge.By == "" {
		c.Merge.By = defaultMergeBy
	}
	if c.Merge.Strategy == "" {
		c.Merge.Strategy = defaultMergeStrategy
	}
	if c.Merge.Via == "" {
		c.Merge.Via = defaultMergeVia
	}
	if !meta.IsDefined("parallel", "max_tasks") {
		c.Parallel.MaxTasks = defaultMaxTasks
	}
	if c.Isolation.Mode == "" {
		c.Isolation.Mode = defaultIsolationMode
	}
	if c.Tasks.OwnerApproval == "" {
		c.Tasks.OwnerApproval = defaultOwnerApproval
	}
	// The label a task is marked with to have its result accepted by the owner is the
	// one crewflow reads of §5: a project that says nothing is a project whose tasks
	// are marked `owner-check`, and the gate refuses their merges until the owner has
	// looked at the head (docs/DESIGN.md §7h).
	if c.Acceptance.Label == "" {
		c.Acceptance.Label = defaultAcceptanceLabel
	}
}
