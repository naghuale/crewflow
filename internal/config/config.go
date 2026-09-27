// Package config reads and checks crewflow.toml, the one file in which a project
// tells crewflow how to work with it (docs/DESIGN.md §5).
package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config is a whole crewflow.toml: which project it is, which agent runs a task
// in which worktree, and which commands must pass before a commit is reviewed.
type Config struct {
	Project      Project      `toml:"project"`
	Executor     Executor     `toml:"executor"`
	Worktrees    Worktrees    `toml:"worktrees"`
	Gates        []Gate       `toml:"gates"`
	CI           CI           `toml:"ci"`
	Merge        Merge        `toml:"merge"`
	Parallel     Parallel     `toml:"parallel"`
	Tasks        Tasks        `toml:"tasks"`
	Requirements Requirements `toml:"requirements"`
	Isolation    Isolation    `toml:"isolation"`
	Capabilities []Capability `toml:"capabilities"`
}

// Project is the repository crewflow works on.
type Project struct {
	// Repo is the GitHub repository as "owner/name".
	Repo string `toml:"repo"`
	// DefaultBranch is the branch a task starts from and merges into.
	DefaultBranch string `toml:"default_branch"`
	// Language is the language of tasks, reviews and messages to the human.
	Language string `toml:"language"`
}

// Executor is the agent crewflow runs a task with, plus the ones to try in turn
// when it turns out to be unavailable (docs/DESIGN.md §7b).
type Executor struct {
	ExecutorSpec
	// Fallback lists the executors to try, in order, after the primary one.
	Fallback []ExecutorSpec `toml:"fallback"`
}

// ExecutorSpec is one agent: the command line to run, the model to ask for and
// the time limit of a run.
type ExecutorSpec struct {
	// Command is the agent's command line, where crewflow substitutes the
	// {worktree}, {prompt} and {model} placeholders. It has no default:
	// crewflow knows no agent, the project says which one it uses.
	Command []string `toml:"command"`
	// Model is the model to ask for. Empty leaves the choice to the agent.
	Model string `toml:"model"`
	// ModelFlag is how the agent takes the model, for example
	// ["--model", "{model}"]. It is added only when Model is set.
	ModelFlag []string `toml:"model_flag"`
	// Timeout is how long a run may take, as a duration such as "90m".
	Timeout string `toml:"timeout"`
}

// Worktrees says where the worktree of a task is created.
type Worktrees struct {
	// Root is the directory that holds one worktree per task. It may start with
	// "~/" and contain "{repo}".
	Root string `toml:"root"`
}

// Gate is a command of the project that has to pass on the commit.
type Gate struct {
	// Name is how a report refers to the gate.
	Name string `toml:"name"`
	// Run is the command line, which passes when it exits zero.
	Run []string `toml:"run"`
	// Exclusive marks a gate that must not run together with another task's.
	Exclusive bool `toml:"exclusive"`
}

// CI is what crewflow expects from GitHub Actions on the commit.
type CI struct {
	// Required makes crewflow wait for CI and refuse a red commit.
	Required bool `toml:"required"`
	// Timeout is how long crewflow waits for CI, as a duration such as "30m".
	Timeout string `toml:"timeout"`
}

// Merge says who merges an approved pull request and how.
type Merge struct {
	// By is who merges: the orchestrator, the executor or the human.
	By string `toml:"by"`
	// Strategy is how it merges. Fast-forward is the only one crewflow does.
	Strategy string `toml:"strategy"`
}

// Parallel limits how many tasks run at the same time.
type Parallel struct {
	// MaxTasks is the number of runs at once; 1 means strictly one after
	// another.
	MaxTasks int `toml:"max_tasks"`
}

// Tasks is how crewflow treats a task before it starts (docs/DESIGN.md §7f).
type Tasks struct {
	// OwnerApproval is whose approval a task waits for: "all" makes every task
	// wait, "risky" only the ones a person must look at, "none" none.
	OwnerApproval string `toml:"owner_approval"`
}

// Requirements are the tools the project needs, with the command that checks
// each of them.
type Requirements struct {
	Tools []Tool `toml:"tools"`
}

// Tool is one required tool and how to check that it is there.
type Tool struct {
	// Name is what a report calls the tool.
	Name string `toml:"name"`
	// Check is the command line that proves the tool is usable.
	Check []string `toml:"check"`
	// Min is the lowest version the project works with, empty if it does not
	// care.
	Min string `toml:"min"`
}

// Isolation is how the agent is kept away from the rest of the machine.
type Isolation struct {
	// Mode is "host", "sandbox" or "container" (docs/DESIGN.md §7d).
	Mode string `toml:"mode"`
}

// Capability is access to secrets and outside systems that the project may need.
// Declaring it grants nothing: the human decides (docs/DESIGN.md §7e).
type Capability struct {
	// Name is what a report calls the capability.
	Name string `toml:"name"`
	// Use are the programs to use the capability through, without seeing the
	// secret.
	Use []string `toml:"use"`
	// Actions are the commands with consequences outside the machine, which are
	// always run only after a confirmation.
	Actions []string `toml:"actions"`
}

// Load reads the crewflow.toml at path, fills in the defaults and checks what
// came out, so that no caller ever works with a file it cannot use.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	meta, err := toml.Decode(string(data), &cfg)
	if err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if unknown := meta.Undecoded(); len(unknown) > 0 {
		keys := make([]string, 0, len(unknown))
		for _, key := range unknown {
			keys = append(keys, key.String())
		}
		return Config{}, fmt.Errorf("parse %s: unknown keys: %s", path, strings.Join(keys, ", "))
	}
	cfg.applyDefaults(&meta)
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}
