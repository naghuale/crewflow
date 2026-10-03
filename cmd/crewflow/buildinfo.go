package main

import (
	"strings"

	"github.com/naghuale/crewflow/internal/buildinfo"
	"github.com/naghuale/crewflow/internal/config"
)

// requiredCapabilities is the question a command asks of the build it runs in: has this
// program the mechanisms the file of the project requires of it? It is a variable so that
// a test of a command can put an older build in its place and see the refusal of §5 — an
// installed program of a version before a mechanism was written is exactly that, and the
// test must not have to be one (F-178).
var requiredCapabilities = buildinfo.Require

// loadFor is the file of the project as a command that acts reads it: the file itself, and
// then the question of the capabilities of the build. The refusal comes out of here,
// before the roles of the project are built, before a key of an App is read and before a
// branch, a worktree or an attempt of anything is made: a run that opens a window of the
// keychain of macOS and is then refused leaves a person in front of a window they did not
// come for (docs.DESIGN.md §5, §7i, F-178).
//
// The commands that read the file without it are the ones that create nothing and whose
// answer cannot be mistaken for a promise: `task check`, `task watch`, `task list`,
// `task attention`, `task check-stalled`, `verify`, `changelog`, `network`, `auth`. They
// show what is there, and a machine whose build is older is told so by `doctor` and by the
// commands that would have acted.
func loadFor(configPath string) (config.Config, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return config.Config{}, err
	}
	if err := requiredCapabilities(cfg.Crewflow.Requires); err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}

// capabilities is the line of what this build can do, as `crewflow version` prints it: the
// list out of the code of the build, in one order, and never a sentence that says the
// program is up to date when nobody compared it with anything (docs.DESIGN.md §5).
func capabilities() string {
	return "capabilities: " + strings.Join(buildinfo.Capabilities(), ", ")
}
