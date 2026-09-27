package config

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Placeholders crewflow substitutes into the command line of an executor.
const (
	worktreePlaceholder = "{worktree}"
	promptPlaceholder   = "{prompt}"
	modelPlaceholder    = "{model}"
)

// commandPlaceholders is what a command line may use, and nothing else: a
// misspelled placeholder would reach the agent as it is.
var commandPlaceholders = []string{worktreePlaceholder, promptPlaceholder, modelPlaceholder}

// placeholderPattern finds every "{...}" in an argument, so that each one can be
// checked by name.
var placeholderPattern = regexp.MustCompile(`\{[^{}]*\}`)

// The values crewflow knows for the keys that are a choice, not free text.
var (
	mergeBys        = []string{"orchestrator", "executor", "human"}
	mergeStrategies = []string{"ff-only"}
	isolationModes  = []string{"host", "sandbox", "container"}
)

// Validate reports the first thing that is wrong with the config, naming the key
// and the value that is wrong, so that a person can fix the file without
// guessing.
func (c Config) Validate() error {
	if err := validateRepo(c.Project.Repo); err != nil {
		return err
	}
	if err := validateExecutor("executor", c.Executor.ExecutorSpec); err != nil {
		return err
	}
	for i, fallback := range c.Executor.Fallback {
		if err := validateExecutor(fmt.Sprintf("executor.fallback[%d]", i), fallback); err != nil {
			return err
		}
	}
	if err := validateGates(c.Gates); err != nil {
		return err
	}
	if err := validateTimeout("ci.timeout", c.CI.Timeout); err != nil {
		return err
	}
	if err := oneOf("merge.by", c.Merge.By, mergeBys); err != nil {
		return err
	}
	if !slices.Contains(mergeStrategies, c.Merge.Strategy) {
		return fmt.Errorf("merge.strategy: unknown value %q, the only strategy crewflow does is %q",
			c.Merge.Strategy, mergeStrategies[0])
	}
	if c.Parallel.MaxTasks < 1 {
		return fmt.Errorf("parallel.max_tasks: must be at least 1, got %d", c.Parallel.MaxTasks)
	}
	if err := oneOf("isolation.mode", c.Isolation.Mode, isolationModes); err != nil {
		return err
	}
	if err := uniqueNames("requirements.tools", c.Requirements.Tools, func(t Tool) string { return t.Name }); err != nil {
		return err
	}
	return uniqueNames("capabilities", c.Capabilities, func(c Capability) string { return c.Name })
}

// validateRepo checks that the repository is one repository: "owner/name".
func validateRepo(repo string) error {
	owner, name, found := strings.Cut(repo, "/")
	if !found || owner == "" || name == "" || strings.Contains(name, "/") {
		return fmt.Errorf("project.repo: must be the repository as %q, got %q", "owner/name", repo)
	}
	return nil
}

// validateExecutor checks one executor, under the key that names it.
func validateExecutor(key string, executor ExecutorSpec) error {
	if len(executor.Command) == 0 {
		return fmt.Errorf("%s.command: must not be empty: crewflow knows no agent, the file says which one to run",
			key)
	}
	if !hasPlaceholder(executor.Command, promptPlaceholder) {
		return fmt.Errorf("%s.command: must contain the %s placeholder, the text of the task goes there",
			key, promptPlaceholder)
	}
	for _, arg := range executor.Command {
		for _, placeholder := range placeholderPattern.FindAllString(arg, -1) {
			if !slices.Contains(commandPlaceholders, placeholder) {
				return fmt.Errorf("%s.command: unknown placeholder %s, allowed placeholders: %s",
					key, placeholder, strings.Join(commandPlaceholders, ", "))
			}
		}
	}
	if len(executor.ModelFlag) > 0 && !hasPlaceholder(executor.ModelFlag, modelPlaceholder) {
		return fmt.Errorf("%s.model_flag: must contain the %s placeholder, the flag is added only for the model that is asked for",
			key, modelPlaceholder)
	}
	return validateTimeout(key+".timeout", executor.Timeout)
}

// validateGates checks that every gate can be named in a report and can fail.
func validateGates(gates []Gate) error {
	names := make([]string, 0, len(gates))
	for i, gate := range gates {
		key := fmt.Sprintf("gates[%d]", i)
		switch {
		case gate.Name == "":
			return fmt.Errorf("%s.name: must not be empty: a report names every gate it ran", key)
		case slices.Contains(names, gate.Name):
			return fmt.Errorf("%s.name: duplicate gate name %q", key, gate.Name)
		case len(gate.Run) == 0:
			return fmt.Errorf("%s.run: must not be empty: a gate without a command cannot fail", key)
		}
		names = append(names, gate.Name)
	}
	return nil
}

// validateTimeout checks that a timeout is a duration a person can wait out, and
// not a number without a unit or a run that is killed at once.
func validateTimeout(key, value string) error {
	if value == "" {
		return fmt.Errorf("%s: must not be empty", key)
	}
	timeout, err := time.ParseDuration(value)
	if err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	if timeout <= 0 {
		return fmt.Errorf("%s: must be positive, got %q", key, value)
	}
	return nil
}

// oneOf checks that a key holds one of the values crewflow knows.
func oneOf(key, value string, allowed []string) error {
	if slices.Contains(allowed, value) {
		return nil
	}
	return fmt.Errorf("%s: unknown value %q, allowed: %s", key, value, strings.Join(allowed, ", "))
}

// uniqueNames checks that no two items share a name, because a report and a
// grant both speak of them by name.
func uniqueNames[T any](key string, items []T, nameOf func(T) string) error {
	seen := make(map[string]struct{}, len(items))
	for i, item := range items {
		name := nameOf(item)
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("%s[%d].name: duplicate name %q", key, i, name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

// hasPlaceholder reports whether any argument of a command line carries the
// placeholder.
func hasPlaceholder(command []string, placeholder string) bool {
	return slices.ContainsFunc(command, func(arg string) bool {
		return strings.Contains(arg, placeholder)
	})
}
