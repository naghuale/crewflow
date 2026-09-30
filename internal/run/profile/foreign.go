package profile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// globalConfigs are the files of the machine in which OpenCode takes its own rights,
// in the order the agent looks them up: a person may have a config of their own under
// a directory of their own choosing, and the one of the home is what is left when
// they have not. The agent merges every config it finds and the one of the run into
// one set of rights (verified on OpenCode 1.18.32 — see the test that proves it), so a
// `permission` in one of these is a second source of the rights of a run whatever
// crewflow writes into the settings it hands over (docs/DESIGN.md §7d).
var globalConfigs = []string{"opencode.json", "opencode.jsonc"}

// configDirVar is the variable that says where the config of the person is, when it is
// not in the folder of the home. The agent reads it the way every program of a machine
// reads it, and crewflow reads the same name for the same reason: a person who moved
// their config is not a person whose rights crewflow must not look at.
const configDirVar = "XDG_CONFIG_HOME"

// Rights is a file of the machine in which the agent of a run takes rights of its own.
// The rights of a run are merged with them, so a file here with a `permission` in it
// is a second source of the rights of a run, and one that is not in the repository and
// not in the review of a task (docs/DESIGN.md §7d).
type Rights struct {
	// Path is the file as this machine spells it, which is what a person goes and
	// changes.
	Path string `json:"path"`
	// Tables are the permissions the file holds, by the names the agent calls them by
	// ("edit", "read", "external_directory", "bash"). They are the rights of a person
	// that the rights of a run are merged with.
	Tables []string `json:"tables,omitempty"`
	// Unreadable says why crewflow could not tell what the file holds, for a file that
	// is there and is not a JSON crewflow can read. A file crewflow cannot read is a
	// file crewflow cannot promise anything about, and a promise it cannot make is not
	// made.
	Unreadable string `json:"unreadable,omitempty"`
}

// HoldsRights is whether this file has rights of its own in it, whether they are
// written as tables or crewflow could not read them to find out.
func (r Rights) HoldsRights() bool { return len(r.Tables) > 0 || r.Unreadable != "" }

// Says is what this file holds, in the words a person acts upon: the tables of rights
// by the names the agent calls them by, or the reason crewflow could not read the file
// to find out what is in it. It is one line for one file, and a refusal of a run and a
// warning of a report read the same words about the same thing.
func (r Rights) Says() string {
	switch {
	case r.Unreadable != "":
		return "crewflow cannot read it: " + r.Unreadable
	default:
		return "it holds " + strings.Join(r.Tables, ", ")
	}
}

// ForeignRights are the files of the machine in which the agent of this profile takes
// rights of its own, with what each of them holds. A run of a project whose agent has
// none of them holds the rights crewflow wrote and nothing else, and a run of an agent
// that has one of them with rights in it is a run whose rights are not crewflow's
// alone (docs/DESIGN.md §7d).
func (opencode) ForeignRights(home string, environ []string) []Rights {
	var found []Rights
	for _, folder := range configFolders(home, environ) {
		for _, name := range globalConfigs {
			path := filepath.Join(folder, name)
			rights, isThere := rightsIn(path)
			if isThere {
				found = append(found, rights)
			}
		}
	}
	return found
}

// ForeignRights is nothing: crewflow does not guess where an agent it has not run
// takes its rights from. A wrapper around such an agent is handed the whole of the
// policy in the environment and is what holds the agent to it, and a file crewflow
// cannot name is not a file crewflow may report on (docs/DESIGN.md §7b, §7d).
func (generic) ForeignRights(string, []string) []Rights { return nil }

// configFolders is every folder the agent of a run may take a config of the person
// from: the one it was told to look in, and the one of the home of the person. Both
// are given, and a machine that has no home has only the first.
func configFolders(home string, environ []string) []string {
	var folders []string
	if told := valueOf(environ, configDirVar); told != "" {
		folders = append(folders, filepath.Join(told, "opencode"))
	}
	if home != "" {
		folders = append(folders, filepath.Join(home, ".config", "opencode"))
	}
	return folders
}

// rightsIn is what one config of the person holds, and whether the file is there at
// all: a machine without one has no rights of its own to merge, and that is not a
// problem of the run.
func rightsIn(path string) (Rights, bool) {
	raw, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		return Rights{}, false
	case err != nil:
		return Rights{Path: path, Unreadable: err.Error()}, true
	}
	var settings struct {
		Permission map[string]json.RawMessage `json:"permission"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		// A file that is there and that crewflow cannot read is a file the rights of
		// a run cannot be promised against, whether it holds a `permission` or not.
		return Rights{Path: path, Unreadable: err.Error()}, true
	}
	tables := make([]string, 0, len(settings.Permission))
	for table := range settings.Permission {
		tables = append(tables, table)
	}
	slices.Sort(tables)
	return Rights{Path: path, Tables: tables}, true
}
