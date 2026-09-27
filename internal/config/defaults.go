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
	defaultCIRequired      = true
	defaultCITimeout       = "30m"
	defaultMergeBy         = "orchestrator"
	defaultMergeStrategy   = "ff-only"
	defaultMaxTasks        = 1
	defaultIsolationMode   = "host"
)

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
	if c.Merge.By == "" {
		c.Merge.By = defaultMergeBy
	}
	if c.Merge.Strategy == "" {
		c.Merge.Strategy = defaultMergeStrategy
	}
	if !meta.IsDefined("parallel", "max_tasks") {
		c.Parallel.MaxTasks = defaultMaxTasks
	}
	if c.Isolation.Mode == "" {
		c.Isolation.Mode = defaultIsolationMode
	}
}
