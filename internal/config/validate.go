package config

import (
	"errors"
	"fmt"
	"maps"
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
	forgeKinds        = []string{"github", "gitlab", "bitbucket", "gitea", "azure", "none"}
	trackerKinds      = []string{"forge", "jira", "linear", "files"}
	ciKinds           = []string{"forge", "jenkins", "command", "none"}
	identityModes     = []string{"owner", "bot"}
	orchestratorModes = []string{"shared", "separate"}
	mergeBys          = []string{"orchestrator", "executor", "human"}
	mergeStrategies   = []string{"ff-only"}
	mergeVias         = []string{"git-push", "forge"}
	isolationModes    = []string{"host", "sandbox", "container"}
	ownerApprovals    = []string{"all", "risky", "none"}
	networkModes      = []string{"direct", "proxy", "fallback"}
	proxyTypes        = []string{"http", "https", "socks5"}
	credentialPlaces  = []string{"none", "secret-store"}
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
	if err := validateOrchestrator(c); err != nil {
		return err
	}
	if err := validateRoles(c); err != nil {
		return err
	}
	if err := validateExecutor("executor", c.Executor.ExecutorSpec); err != nil {
		return err
	}
	// The term of the checkpoint of a task is a length of time the project agreed on,
	// in the way the time limit of a run and the silence after which it is marked
	// standing are: a point with no term is a point that never goes stale, and a
	// continuation a week later would be a guess about the work (docs/DESIGN.md §7i).
	if err := validateDuration("executor.resume_within", c.Executor.ResumeWithin); err != nil {
		return err
	}
	// How many times crewflow goes on by itself after the model provider refused the run
	// is a promise a project makes to a person: zero is a project that wants to decide
	// every refusal itself, and a number of one or more is a number of tries it agreed
	// to. What crewflow will not take is a number of no sense — a negative number of
	// tries is not a policy, it is a mistake in the file (F-119, §6a, §7a).
	if c.Executor.ProviderRetries < 0 {
		return fmt.Errorf("executor.provider_retries: must not be negative, got %d", c.Executor.ProviderRetries)
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
	if err := validateDuration("ci.timeout", c.CI.Timeout); err != nil {
		return err
	}
	if err := validateAttention(c.Attention); err != nil {
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
	if err := validateNetwork(c.Network); err != nil {
		return err
	}
	if err := Separation(c.Orchestrator.Mode, c.Merge.Owners, c.Merge.Reviewers, c.Project.Repo); err != nil {
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
	return validateGitHubApp("identity", c.Forge.Kind, c.Identity.GitHubApp,
		"the bot of a run is an account of the host of the code")
}

// validateOrchestrator checks the mode of the orchestrator against the host of the
// project and against the account it names, the way the mode of the executor is
// checked: the two modes of §7i are the same for every host, and a file that asks
// for a mode no host of the project has an account for is refused here, where the
// owner reads the reason (docs/DESIGN.md §7i).
func validateOrchestrator(c Config) error {
	if err := oneOf("orchestrator.mode", c.Orchestrator.Mode, orchestratorModes); err != nil {
		return err
	}
	if c.Orchestrator.Mode != ModeSeparate {
		return nil
	}
	return validateGitHubApp("orchestrator", c.Forge.Kind, c.Orchestrator.GitHubApp,
		"an orchestrator apart from the owner is an account of the host of the code")
}

// validateGitHubApp is what an account of a host of its own needs in the file of the
// project: a host that has such an account of its own, and the number of it as the
// settings of the host hold it. The key of the account is not there and never is — it
// is in the keychain of the machine (docs/DESIGN.md §7e, §7i).
func validateGitHubApp(table, kind string, app GitHubApp, what string) error {
	if kind != "github" {
		return fmt.Errorf("%s.mode: %s, and the only one crewflow has is the GitHub App; forge.kind is %q",
			table, what, kind)
	}
	if app.AppID <= 0 {
		return fmt.Errorf("%s.github_app.app_id: must be the number of the app as it stands in the settings of GitHub, got %d",
			table, app.AppID)
	}
	if app.InstallationID < 0 {
		return fmt.Errorf("%s.github_app.installation_id: must be a number of an installation or 0 to find it, got %d",
			table, app.InstallationID)
	}
	return nil
}

// OwnersOf are the logins whose records are the decision of a person and not the work
// of the orchestrator: the list the file of the project names, and the owner of its
// repository when it names none. The record of an acceptance of a result and the record
// of a scope acceptance are counted from this one list and from no other
// (docs/DESIGN.md §5, §7h).
func OwnersOf(list []string, repository string) []string {
	return loginsOf(list, repository)
}

// ReviewersOf are the logins whose record of a review is an approval: the list the
// file of the project names, and the owner of its repository when it names none
// (docs/DESIGN.md §5, §7h).
func ReviewersOf(list []string, repository string) []string {
	return loginsOf(list, repository)
}

// loginsOf is the default of §5 behind both lists: a project that names no account
// has the owner of its repository, and nothing else.
func loginsOf(list []string, repository string) []string {
	if len(list) > 0 {
		return list
	}
	if owner, _, of := strings.Cut(repository, "/"); of {
		return []string{owner}
	}
	return nil
}

// Separation is what the mode of an orchestrator apart from the owner demands of the
// accounts of the project, checked against the accounts it is given: the logins whose
// records are decisions of the owner, the logins whose records of a review are
// approvals, and the repository the project belongs to. An empty mode, or any mode but
// the separate one, is a nil answer: one login is both subjects in the mode of a shared
// login by definition, and the overlap there is a debt of trust that `crewflow doctor`
// shows instead of hiding (docs/DESIGN.md §7i, §7k).
//
// In the mode of a separate login two things are refused:
//
//   - an account of the reviewers is an account of the owners (OR-008): the same login
//     would approve a change and accept its result, which is the one subject doing both
//     parts of the work the mode exists to divide;
//   - the owner of the repository is an account of the reviewers (OR-005): a record of
//     a review would be written under the login of the owner, and the mode would promise
//     a separation the file makes impossible.
//
// The accounts are the ones the caller resolved, and the two callers see different
// ones: the load of the file sees the lists as they are written, because the account of
// an App of the orchestrator is not in the file, and a command of a review or of a merge
// sees the account the App answers with. The rule is the same for both; what the load
// cannot see, the command and a report of `doctor` say.
func Separation(mode string, owners, reviewers []string, repository string) error {
	if mode != ModeSeparate {
		return nil
	}
	for _, login := range reviewers {
		if contains(owners, login) {
			return fmt.Errorf("separate mode requires role separation: %q is among the accounts of the reviewers "+
				"and among the accounts of the owners, and the gate would count the record of the orchestrator as a "+
				"decision of the owner; overlap: %s; fix: remove overlapping identities or switch to shared mode",
				login, login)
		}
	}
	if owner, _, of := strings.Cut(repository, "/"); of && contains(reviewers, owner) {
		return fmt.Errorf("separate mode requires an account of its own: the owner of the repository %q is among "+
			"the accounts whose record of a review counts, and a review would be written under the login of the "+
			"owner; fix: name the account of the app of the orchestrator among merge.reviewers, "+
			"or switch to shared mode", owner)
	}
	return nil
}

// contains is whether the login is in the list, as the host writes a login and as a
// person writes it: the letters of a login are its letters whatever their case.
func contains(logins []string, login string) bool {
	return slices.ContainsFunc(logins, func(one string) bool {
		return strings.EqualFold(one, login)
	})
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
//
// The list of the owners is one list and one check: the account whose record of an
// acceptance of a result counts is the account whose record of a scope acceptance
// counts, and an orchestrator that works apart from the owner is in neither of them
// (docs/DESIGN.md §7h, §7i).
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
	return errors.Join(
		validateDuration(key+".timeout", executor.Timeout),
		validateDuration(key+".stall_after", executor.StallAfter),
	)
}

// validateAttention checks that every threshold of the queue of attention is a length of
// time a person can be waited for, and that the thresholds of the project make a queue out
// of them: a task is escalated before it is called a long wait, and a record under a task
// is left once before it is repeated (docs/DESIGN.md §6a).
func validateAttention(attention Attention) error {
	err := errors.Join(
		validateDuration("attention.top_after", attention.TopAfter),
		validateDuration("attention.escalate_after", attention.EscalateAfter),
		validateDuration("attention.remind_after", attention.RemindAfter),
		validateDuration("attention.weekly_after", attention.WeeklyAfter),
		validateDuration("attention.deadline_after", attention.DeadlineAfter),
	)
	if err != nil {
		return err
	}
	top, _ := time.ParseDuration(attention.TopAfter)
	escalate, _ := time.ParseDuration(attention.EscalateAfter)
	weekly, _ := time.ParseDuration(attention.WeeklyAfter)
	if escalate < top {
		return fmt.Errorf("attention.escalate_after: must be at least attention.top_after (%q), "+
			"or a task is escalated before it has waited long enough to be in the queue", attention.TopAfter)
	}
	if weekly < escalate {
		return fmt.Errorf("attention.weekly_after: must be at least attention.escalate_after (%q), "+
			"or a wait is called a long one before it is escalated", attention.EscalateAfter)
	}
	deadline, _ := time.ParseDuration(attention.DeadlineAfter)
	if deadline < 0 {
		return fmt.Errorf("attention.deadline_after: must not be a negative length (%q), "+
			"or every wait of the project is over its deadline the moment it begins", attention.DeadlineAfter)
	}
	return nil
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

// validateNetwork checks the route of a project against the profiles it names: a
// mode crewflow does not do, an active profile that is not in the table, and a
// profile crewflow could not talk to anyway — a protocol outside the list, a
// port that is not a port, and an address with a login in it, which is a secret
// in the file of a project and is kept in the store of secrets instead
// (docs/DESIGN.md §7d, §7e).
func validateNetwork(network Network) error {
	if err := oneOf("network.mode", network.Mode, networkModes); err != nil {
		return err
	}
	if err := errors.Join(
		validateDuration("network.connect_timeout", network.ConnectTimeout),
		validateDuration("network.test_valid_for", network.TestValidFor),
	); err != nil {
		return err
	}
	if network.ActiveProxy != "" {
		if _, known := network.Proxies[network.ActiveProxy]; !known {
			return fmt.Errorf("network.active_proxy: no profile %q in [network.proxies], "+
				"the profiles of the project are %s", network.ActiveProxy, knownProfiles(network))
		}
	}
	for _, name := range slices.Sorted(maps.Keys(network.Proxies)) {
		if err := validateProxy(name, network.Proxies[name]); err != nil {
			return err
		}
	}
	return nil
}

// validateProxy checks one named profile: the name a person gives it, the
// protocol crewflow speaks to it over, where it listens and whether it takes
// credentials.
func validateProxy(name string, proxy Proxy) error {
	table := fmt.Sprintf("network.proxies.%s", name)
	if err := proxyName(name); err != nil {
		return fmt.Errorf("%s: %w", table, err)
	}
	if err := oneOf(table+".type", proxy.Type, proxyTypes); err != nil {
		return err
	}
	if strings.TrimSpace(proxy.Host) == "" {
		return fmt.Errorf("%s.host: must be the host or the address of the proxy, got %q", table, proxy.Host)
	}
	for _, secret := range []struct{ what, mark string }{
		{"a scheme", "://"},
		{"a login", "@"},
		{"a path", "/"},
	} {
		if strings.Contains(proxy.Host, secret.mark) {
			return fmt.Errorf("%s.host: must be the host or the address of the proxy without %s, got %q: "+
				"a scheme is what the type says, and credentials are in the store of secrets, "+
				"not in the file of the project", table, secret.what, proxy.Host)
		}
	}
	if proxy.Port < 1 || proxy.Port > 65535 {
		return fmt.Errorf("%s.port: must be a port from 1 to 65535, got %d", table, proxy.Port)
	}
	if err := oneOf(table+".credentials", proxy.Credentials, credentialPlaces); err != nil {
		return err
	}
	return nil
}

// proxyName checks the name a person gives a profile. A name goes into the key
// of the profile in the store of secrets and into a line of a report, so it is a
// name and not a path, a URL or a piece of TOML.
func proxyName(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("must not be empty: it is the name of the profile")
	}
	for _, mark := range []string{" ", "\"", "'", "[", "]", ".", "/", "=", "#", "@"} {
		if strings.Contains(name, mark) {
			return fmt.Errorf("must be a name without %q, got %q", mark, name)
		}
	}
	return nil
}

// knownProfiles are the names of the profiles of a project, for the one refusal
// that has to tell a person what to write instead.
func knownProfiles(network Network) string {
	names := slices.Sorted(maps.Keys(network.Proxies))
	if len(names) == 0 {
		return "none yet"
	}
	return strings.Join(names, ", ")
}

// validateDuration checks that a key holds a duration a person can wait out, and not a
// number without a unit or a wait that is over at once. The time limit of a run, the
// limit of the checks of a commit and the silence after which a run is marked as
// standing are all the same kind of value: a length of time the project agreed on.
func validateDuration(key, value string) error {
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
