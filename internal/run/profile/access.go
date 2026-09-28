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

// AccessEnv is the reading policy of a run as OpenCode reads it: a part of its
// settings that opens the folders the project named outside the worktree, closes the
// places of secrets, and — for every folder that is opened — closes the same folder
// for writing, which a run never allows (docs/DESIGN.md §7d).
//
// What the environment already held is read first and the rules of the run are written
// over it: what a run may read is what the policy of the run says, whatever else was
// configured. Settings that are not a permission are left as they are. A value that is
// not JSON is an error before the executor is started, because guessing the rights of
// a run out of a value nobody can read is not a decision crewflow may make.
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
	external, edit := map[string]string{}, map[string]string{}
	for _, path := range policy.Read {
		external[patternOf(path)], edit[patternOf(path)] = "allow", "deny"
	}
	// The places that are closed are written last, so that a place that is in both
	// lists is closed: the secrets of the machine are stronger than what a project
	// asked for, and a project cannot open them.
	for _, path := range policy.Deny {
		external[patternOf(path)], edit[patternOf(path)] = "deny", "deny"
	}
	// Only the two permissions a run knows are written: a rule of a person for another
	// tool is not crewflow's to throw away, and a run that has no opinion about a tool
	// is not one that has a different opinion.
	permission, isTable := settings["permission"].(map[string]any)
	if !isTable {
		permission = map[string]any{}
	}
	permission["external_directory"], permission["edit"] = external, edit
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
		readVar + "=" + strings.Join(policy.Read, separator),
		denyVar + "=" + strings.Join(policy.Deny, separator),
	}, nil
}

// patternOf is the pattern of one place: everything under a folder is named with
// "/**", and a pattern crewflow was given as a pattern — `**/.env` — is left as it
// was written, because it is a pattern and not a folder.
func patternOf(path string) string {
	if strings.Contains(path, "**") {
		return path
	}
	return strings.TrimSuffix(path, "/") + "/**"
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
