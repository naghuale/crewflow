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
	expanded := strings.ReplaceAll(path, "{repo}", c.RepoName())
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

// RepoName is the name of the repository as a path may hold it: "owner/name"
// becomes "owner-name", so that the worktrees, the journals and the state of two
// projects never share a folder.
func (c Config) RepoName() string {
	return strings.ReplaceAll(c.Project.Repo, "/", "-")
}
