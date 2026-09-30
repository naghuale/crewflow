package profile

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/naghuale/crewflow/internal/access"
)

// Where the rights of a run are named for an agent, which is the one thing about a
// profile crewflow has to know besides how to read what the agent writes: an agent
// that is not told may read a folder refuses a permission for it, and a run that
// stops there has done nothing (docs/DESIGN.md §7a, §7d).
const (
	// configContent is where OpenCode takes a part of its settings out of the
	// environment, as JSON. It is read after the files of the project, so what
	// crewflow writes there is what the agent goes by, whatever an opencode.json of
	// the project or of the person says (verified on OpenCode 1.18.32).
	configContent = "OPENCODE_CONFIG_CONTENT"
	// readVar and denyVar are where the rights of a run are named for an agent
	// crewflow has not run: a wrapper around another agent reads the two lists out of
	// the environment and holds its agent to them. The paths of a list are written
	// the way PATH is written, because that is the one list an agent reads.
	readVar = "CREWFLOW_READ"
	denyVar = "CREWFLOW_DENY"
)

// The three permissions of OpenCode that the rights of a run are written into, by the
// names of the agent itself.
const (
	// outside is a path outside the worktree, and the only permission that can open
	// one: everything under it needs an answer of its own (docs/DESIGN.md §7a).
	outside = "external_directory"
	// reading is the tool that reads a file, wherever it is. The places of secrets
	// are closed to it as well, because the `.env` of the worktree itself is inside
	// it and no rule about paths outside the worktree reaches there.
	reading = "read"
	// writing is the tool that changes a file. Every folder a run opened is closed to
	// it, and so is every place of secrets: a run writes in its worktree and nowhere
	// else.
	writing = "edit"
)

// AccessEnv is the reading policy of a run as OpenCode reads it: a part of its
// settings that opens the folders the project named outside the worktree and closes
// the places of secrets to reading, to writing and to going outside the worktree
// (docs/DESIGN.md §7d).
//
// The three tables are written key by key over the ones the environment already held:
// what a run may read is what the policy of the run says, and a rule of a person for
// another path or another tool is not crewflow's to throw away. A value that is not a
// table of patterns — a permission that is only "allow" or "deny" — has no keys to
// keep and is replaced. A value that is not JSON is an error before the executor is
// started, because guessing the rights of a run out of a value nobody can read is not
// a decision crewflow may make.
func (opencode) AccessEnv(policy access.Policy, environ []string) ([]string, error) {
	settings := map[string]any{}
	if given := valueOf(environ, configContent); given != "" {
		if err := json.Unmarshal([]byte(given), &settings); err != nil {
			return nil, fmt.Errorf("%s of the environment is not JSON, and crewflow will not "+
				"start a run whose rights it cannot read: %w", configContent, err)
		}
		if settings == nil {
			settings = map[string]any{}
		}
	}

	external, read, edit := map[string]string{}, map[string]string{}, map[string]string{}
	for _, grant := range policy.Read {
		external[under(grant.Path)] = "allow"
		edit[under(grant.Path)] = "deny"
	}
	// The places that are closed are written last, so that a place that is in both
	// lists is closed: the secrets of the machine are stronger than what a project
	// asked for, and a project cannot open them.
	for _, path := range policy.Deny {
		for _, pattern := range patternsOf(path) {
			external[pattern], read[pattern], edit[pattern] = "deny", "deny", "deny"
		}
	}

	permission, isTable := settings["permission"].(map[string]any)
	if !isTable {
		permission = map[string]any{}
	}
	permission[outside] = rulesOf(permission[outside], external)
	permission[reading] = rulesOf(permission[reading], read)
	permission[writing] = rulesOf(permission[writing], edit)
	settings["permission"] = permission

	content, err := json.Marshal(settings)
	if err != nil {
		return nil, fmt.Errorf("the rights of the run: %w", err)
	}
	return []string{configContent + "=" + string(content)}, nil
}

// AccessEnv is the reading policy of a run as a wrapper of another agent reads it: the
// two lists of the policy in the environment, under the names crewflow gives them. The
// agent of this profile is not OpenCode, and crewflow does not guess how an agent it
// has not run takes a permission: the policy is named whole, and holding the agent to
// it is the job of the wrapper around it (docs/DESIGN.md §7b, §7d).
func (generic) AccessEnv(policy access.Policy, _ []string) ([]string, error) {
	separator := string(os.PathListSeparator)
	return []string{
		readVar + "=" + strings.Join(access.Paths(policy.Read), separator),
		denyVar + "=" + strings.Join(policy.Deny, separator),
	}, nil
}

// rulesOf is the rules of one permission of a run written into the rules that were
// there: key by key, where a rule of a run for a pattern wins and a rule of a person
// for any other pattern stays. The rules of a person are kept as they are, of whatever
// shape they are: only the keys crewflow writes are its own.
func rulesOf(given any, rules map[string]string) any {
	table, isTable := given.(map[string]any)
	if !isTable {
		return rules
	}
	merged := make(map[string]any, len(table)+len(rules))
	for pattern, answer := range table {
		merged[pattern] = answer
	}
	for pattern, answer := range rules {
		merged[pattern] = answer
	}
	return merged
}

// under is the pattern of everything in a folder: a folder of a run is opened for what
// is in it, and not for itself.
func under(path string) string {
	return strings.TrimSuffix(path, "/") + "/**"
}

// patternsOf is a place in the patterns that cover it: the place itself and everything
// under it. A secret is a file as often as it is a folder — `~/.netrc`,
// `~/.docker/config.json` — and a pattern of everything under a place does not match
// the place. A pattern crewflow was given as a pattern is left as it was written,
// because a pattern is not a folder.
func patternsOf(path string) []string {
	if strings.Contains(path, "**") {
		return []string{path}
	}
	place := strings.TrimSuffix(path, "/")
	return []string{place, place + "/**"}
}

// valueOf is what the environment says under the name, or an empty string when it
// says nothing: an executor inherits the environment of crewflow, and one variable of
// it is about the rights of the run.
func valueOf(environ []string, name string) string {
	prefix := name + "="
	for _, entry := range environ {
		if value, found := strings.CutPrefix(entry, prefix); found {
			return value
		}
	}
	return ""
}
