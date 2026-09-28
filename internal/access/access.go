// Package access is the policy of reading of an executor: the folders of the machine
// it may read outside the worktree of its task, and the places that stay closed to it
// whatever the project wrote in its file (docs/DESIGN.md §7d).
//
// A project says how to find its dependencies and not where they are — the paths are
// different on every machine — so crewflow runs those commands itself before the
// executor is started. What comes out of it is reading and nothing else, and the
// places that hold a secret are closed whatever the project named: everything an
// executor opens goes into the context of a model (§7e).
package access

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/naghuale/crewflow/internal/config"
)

// commandTimeout is how long a command of the project may take to say where its
// dependencies are. It is long enough for a tool that has to start and short enough
// that a task does not stand still in front of a tool that has hung: a run has an
// end whatever the file of the project named (docs/DESIGN.md §7d).
const commandTimeout = 10 * time.Second

// secrets are the places of a machine that hold a key, a token or a password, and
// that are closed to an executor whatever the project wrote. An entry that names a
// folder is resolved against the home of the person; an entry with "**" in it is a
// pattern, and a pattern is the same on every machine.
var secrets = []string{
	"~/.ssh",
	"~/.gnupg",
	"~/Library/Keychains",
	"~/.config/gh",
	"~/.aws",
	"~/.netrc",
	"~/.docker/config.json",
	"~/.kube",
	"**/.env",
	"**/.env.*",
}

// Policy is what the executor of a project may read outside the worktree of a task,
// and the places crewflow keeps closed to it. The two lists are handed to the agent
// by its own profile, and the second one is stronger than the first: a place that is
// in both is closed (docs/DESIGN.md §7d).
type Policy struct {
	// Read are the folders that may be read, clean and as they are on this machine.
	Read []string
	// Deny are the places that may never be read: the secrets of the machine, as
	// paths and as patterns.
	Deny []string
}

// Problem is a path of the project that crewflow will not let the executor read, and
// why. It is what `crewflow doctor` shows a person and what the journal of a run
// says: a path that is refused is not a run that failed, it is a run that goes on
// without it.
type Problem struct {
	// Path is what the project named, as it named it.
	Path string `json:"path"`
	// Reason says why crewflow did not open it, in the words a person reads.
	Reason string `json:"reason"`
}

// Runner starts a program of the project and returns what it wrote and the code it
// exited with. It is the machine of doctor and the machine of a run, each of which
// has one of its own.
type Runner func(ctx context.Context, name string, args []string) (stdout, stderr []byte, exitCode int, err error)

// Env is what Resolve needs from the machine. It is passed in whole, so that a test
// hands the policy a machine of its own and no test of it resolves "~" to the home of
// the person the test runs on.
type Env struct {
	// Home is the folder a leading "~/" stands for, and where the secrets of the
	// machine are.
	Home string
	// Run starts the commands of `access.read_from`.
	Run Runner
	// Timeout is how long one such command may take. Zero is commandTimeout, and a
	// test asks for less so that it does not have to sit through a hang.
	Timeout time.Duration
}

// Resolve works out the reading policy of a run on this machine: it runs the commands
// of the project, keeps the folders that are there, and closes the places of secrets
// whatever they named. A path crewflow will not open comes back as a Problem, and the
// policy comes back without it: a run goes on with what the project was really after
// (docs/DESIGN.md §7d).
func Resolve(ctx context.Context, env Env, cfg config.Access) (Policy, []Problem) {
	var problems []Problem
	named := slices.Clone(cfg.Read)
	for _, command := range cfg.ReadFrom {
		paths, reason := ask(ctx, env, command)
		if reason != "" {
			problems = append(problems, Problem{Path: strings.Join(command, " "), Reason: reason})
			continue
		}
		named = append(named, paths...)
	}
	policy := Policy{Deny: denyList(env.Home)}
	for _, path := range named {
		folder, reason := readable(path, env.Home)
		switch {
		case reason != "":
			problems = append(problems, Problem{Path: strings.TrimSpace(path), Reason: reason})
		case folder != "" && !slices.Contains(policy.Read, folder):
			policy.Read = append(policy.Read, folder)
		}
	}
	return policy, problems
}

// ask runs one command of the project and returns the paths it printed, one per
// line, and the reason crewflow could not ask. A command that says nothing is no
// answer and not a problem: a tool of a project may have nothing to name.
func ask(ctx context.Context, env Env, command []string) ([]string, string) {
	timeout := env.Timeout
	if timeout <= 0 {
		timeout = commandTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stdout, stderr, code, err := env.Run(ctx, command[0], command[1:])
	switch {
	case err != nil:
		return nil, fmt.Sprintf("the command did not run: %v", err)
	case code != 0:
		return nil, fmt.Sprintf("the command exited with %d: %s", code, firstLine(stderr, stdout))
	}
	var paths []string
	for raw := range strings.Lines(string(stdout)) {
		if path := strings.TrimSpace(raw); path != "" {
			paths = append(paths, path)
		}
	}
	return paths, ""
}

// readable is what crewflow makes of one path a project named: the folder the
// executor may read, or the reason the folder is not opened. A path with neither is
// one that is not there, and a path that is not there is not opened either — an
// empty line and a tool that named a folder it does not have are the same thing.
func readable(path, home string) (string, string) {
	place := strings.TrimSpace(path)
	switch {
	case place == "":
		return "", ""
	case strings.HasPrefix(place, "~"):
		if home == "" {
			return "", "~ stands for the home of the person, and this machine says it has none"
		}
		place = expand(place, home)
	case !filepath.IsAbs(place):
		return "", "not an absolute path, and crewflow cannot tell what it is relative to"
	}
	folder := real(place)
	if reason := refusal(folder, home); reason != "" {
		return "", reason
	}
	if _, err := os.Stat(folder); err != nil {
		return "", ""
	}
	return folder, ""
}

// refusal is why crewflow will not let the executor read the folder, or an empty
// string when it will. A project may open a folder of a library; it may not open the
// root of a disk, the home of the person, or anything a secret sits in — and not
// through a link that leads there either.
func refusal(folder, home string) string {
	switch {
	case folder == root:
		return "the root of a disk is not a folder of dependencies"
	case home != "" && folder == real(home):
		return "the home of the person holds everything, keys among them"
	}
	closed, _ := places(home)
	for _, secret := range closed {
		if holds(folder, secret) {
			return fmt.Sprintf("it holds %s, which is where secrets are", secret)
		}
	}
	return ""
}

// places is where the secrets of this machine are, with the "~" of the file
// resolved: the folders as this machine holds them, and the patterns as they were
// written, because a pattern is the same everywhere.
func places(home string) (folders, patterns []string) {
	for _, secret := range secrets {
		if strings.Contains(secret, "**") {
			patterns = append(patterns, secret)
			continue
		}
		folders = append(folders, real(expand(secret, home)))
	}
	return folders, patterns
}

// holds is whether the folder and the place of secrets are one folder or one of them
// is inside the other: a folder that holds a place of secrets is refused as well as a
// folder that is one, because a permission to read it is a permission to read the key.
func holds(folder, place string) bool {
	if folder == place || strings.HasPrefix(folder, place+string(filepath.Separator)) {
		return true
	}
	return strings.HasPrefix(place, folder+string(filepath.Separator))
}

// denyList is the whole of the closed places of this machine, folders and patterns,
// as a caller of the policy is given them.
func denyList(home string) []string {
	folders, patterns := places(home)
	return append(folders, patterns...)
}

// root is the folder every path on the machine is under, and the one folder a
// project never names as its dependencies.
var root = real(string(filepath.Separator))

// expand is the path with a leading "~/" standing for the home of the person, and a
// path that is nothing but "~" as the home itself.
func expand(path, home string) string {
	rest, isHomeRelative := strings.CutPrefix(path, "~/")
	switch {
	case path == "~":
		return home
	case isHomeRelative:
		return filepath.Join(home, filepath.FromSlash(rest))
	}
	return path
}

// real is the path as this machine holds it: a link followed, so that two names of
// one folder are one folder in the policy, and the folders that are not there are
// the ones nobody can read.
func real(path string) string {
	followed, err := filepath.EvalSymlinks(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(followed)
}

// firstLine is the first line of the outputs that says something: a command that
// failed without a word in it has said as much as it can.
func firstLine(outputs ...[]byte) string {
	for _, output := range outputs {
		for line := range strings.Lines(string(output)) {
			if text := strings.TrimSpace(line); text != "" {
				return text
			}
		}
	}
	return ""
}
