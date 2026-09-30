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
	forgeKinds      = []string{"github", "gitlab", "bitbucket", "gitea", "azure", "none"}
	trackerKinds    = []string{"forge", "jira", "linear", "files"}
	ciKinds         = []string{"forge", "jenkins", "command", "none"}
	identityModes   = []string{"owner", "bot"}
	mergeBys        = []string{"orchestrator", "executor", "human"}
	mergeStrategies = []string{"ff-only"}
	mergeVias       = []string{"git-push", "forge"}
	isolationModes  = []string{"host", "sandbox", "container"}
	ownerApprovals  = []string{"all", "risky", "none"}
)

// Validate reports the first thing that is wrong with the config, naming the key
// and the value that is wrong, so that a person can fix the file without
// guessing.
func (c Config) Validate() error {
	if err := validateRepo(c.Project.Repo); err != nil {
		return err
	}
	if err := oneOf("forge.kind", c.Forge.Kind, forgeKinds); err != nil {
		return err
	}
	if err := oneOf("tracker.kind", c.Tracker.Kind, trackerKinds); err != nil {
		return err
	}
	if err := oneOf("ci.kind", c.CI.Kind, ciKinds); err != nil {
		return err
	}
	if err := validateIdentity(c); err != nil {
		return err
	}
	if err := validateRoles(c); err != nil {
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
	if err := validateAccess(c.Access); err != nil {
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
	if err := oneOf("merge.via", c.Merge.Via, mergeVias); err != nil {
		return err
	}
	if err := validateLogins("merge.reviewers", c.Merge.Reviewers); err != nil {
		return err
	}
	if err := validateLogins("merge.owners", c.Merge.Owners); err != nil {
		return err
	}
	if c.Parallel.MaxTasks < 1 {
		return fmt.Errorf("parallel.max_tasks: must be at least 1, got %d", c.Parallel.MaxTasks)
	}
	if err := oneOf("isolation.mode", c.Isolation.Mode, isolationModes); err != nil {
		return err
	}
	if err := oneOf("tasks.owner_approval", c.Tasks.OwnerApproval, ownerApprovals); err != nil {
		return err
	}
	if err := uniqueNames("requirements.tools", c.Requirements.Tools, func(t Tool) string { return t.Name }); err != nil {
		return err
	}
	return uniqueNames("capabilities", c.Capabilities, func(c Capability) string { return c.Name })
}

// validateIdentity checks the mode of the executor against the host of the project
// and against the bot it names. The mode is the same word everywhere, and what a bot
// of a host is called is the business of the adapter of that host: a file that asks
// for a mode no host of it has a bot for is refused here, where the owner reads the
// reason, and not in the middle of a task (docs/DESIGN.md §7g, §7i).
func validateIdentity(c Config) error {
	if err := oneOf("identity.mode", c.Identity.Mode, identityModes); err != nil {
		return err
	}
	if c.Identity.Mode != "bot" {
		return nil
	}
	if c.Forge.Kind != "github" {
		return fmt.Errorf("identity.mode: the bot of a run is an account of the host of the code, "+
			"and the only one crewflow has is the GitHub App; forge.kind is %q", c.Forge.Kind)
	}
	if c.Identity.GitHubApp.AppID <= 0 {
		return fmt.Errorf("identity.github_app.app_id: must be the number of the app as it stands in the settings of GitHub, got %d",
			c.Identity.GitHubApp.AppID)
	}
	if c.Identity.GitHubApp.InstallationID < 0 {
		return fmt.Errorf("identity.github_app.installation_id: must be a number of an installation or 0 to find it, got %d",
			c.Identity.GitHubApp.InstallationID)
	}
	return nil
}

// validateLogins checks the accounts whose record counts, under the key that names
// them: each of them has to be a name, because a gate that looks for a record in the
// name of nobody counts no record at all, and none of them twice, because a list that
// says the same name twice is a file a person meant something else by (docs/DESIGN.md
// §7h). The reviewers of a change and the owners who accept a result are two such
// lists, and both are told apart in the reason a file with one of them wrong is
// refused for.
//
// An empty list is not a mistake: the owner of the repository reviews the project and
// accepts its results until it says otherwise, which is what a project that says
// nothing gets.
func validateLogins(key string, logins []string) error {
	seen := make(map[string]struct{}, len(logins))
	for i, login := range logins {
		switch {
		case strings.TrimSpace(login) == "":
			return fmt.Errorf("%s[%d]: must be a login of an account, as %q, got %q",
				key, i, "naghuale", login)
		case strings.HasPrefix(login, "@"):
			return fmt.Errorf("%s[%d]: a login is written without the @ of a mention: %q", key, i, login)
		}
		if _, duplicate := seen[login]; duplicate {
			return fmt.Errorf("%s[%d]: duplicate account %q", key, i, login)
		}
		seen[login] = struct{}{}
	}
	return nil
}

// validateRoles checks the roles of DESIGN §7g against each other: a project
// with no host of its own has no tasks and no checks to take from that host, so
// the two keys that ask the forge for them cannot stand next to a forge of "none".
func validateRoles(c Config) error {
	if c.Forge.Kind != "none" {
		return nil
	}
	for _, role := range []struct{ key, kind string }{
		{"tracker.kind", c.Tracker.Kind},
		{"ci.kind", c.CI.Kind},
	} {
		if role.kind == "forge" {
			return fmt.Errorf("%s: asks the forge for what it is, and forge.kind is %q: there is no forge to ask",
				role.key, c.Forge.Kind)
		}
	}
	return nil
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

// validateAccess checks the commands that name the folders of a project: a command
// line with no program in it cannot print a path, and a path the file names has to
// say something about itself, because an empty one is not a place on the machine.
func validateAccess(access Access) error {
	for i, command := range access.ReadFrom {
		if len(command) == 0 {
			return fmt.Errorf("access.read_from[%d]: must not be empty: "+
				"it is a command that prints the folders the project may read", i)
		}
	}
	for i, path := range access.Read {
		if strings.TrimSpace(path) == "" {
			return fmt.Errorf("access.read[%d]: must not be empty: it is a path on the machine", i)
		}
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
