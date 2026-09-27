package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// homeDir reports the home directory a leading "~/" stands for. It is a variable
// so that a test can point it at a temporary directory and never read the home
// directory of the machine it runs on.
var homeDir = os.UserHomeDir

// ExpandPath expands a leading "~/" and substitutes "{repo}" with the name of
// the repository, "owner/name" becoming "owner-name", so that the worktrees of
// two projects do not share a folder.
func (c Config) ExpandPath(path string) (string, error) {
	expanded := strings.ReplaceAll(path, "{repo}", c.repoName())
	rest, isHomeRelative := strings.CutPrefix(expanded, "~/")
	if !isHomeRelative {
		return expanded, nil
	}
	home, err := homeDir()
	if err != nil {
		return "", fmt.Errorf("expand %q: home directory: %w", path, err)
	}
	return filepath.Join(home, filepath.FromSlash(rest)), nil
}

// repoName is the name of the repository that is safe to put in a path.
func (c Config) repoName() string {
	return strings.ReplaceAll(c.Project.Repo, "/", "-")
}
