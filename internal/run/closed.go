package run

import (
	"path/filepath"
	"strings"

	"github.com/naghuale/crewflow/internal/access"
	"github.com/naghuale/crewflow/internal/run/profile"
)

// The notes a report adds to a refusal of a place of secrets, and nothing else: the
// refusal is already there, and what is new is what crewflow will not do about it. A
// run that was refused a key is not a run that forgot where its scratch is, and a run
// of crewflow that answered such a refusal by itself would be crewflow looking the
// other way (docs/DESIGN.md §7a, §7d, §7e).
const (
	// secretPath is said of a refusal whose path is a place of secrets, or lies in
	// one: the place is closed to the executor whatever the project wrote, and the
	// report says so where a person reads the run.
	secretPath = " — a secret, closed to the executor whatever the project wrote"
	// secretCommand is said of a refusal of a command that reaches for a secret. The
	// refusal names something else — the scratch folder of the machine, most often —
	// and the reason the run is not continued is the command around it.
	secretCommand = " — the command refused names a secret, and a run that reads one is not continued"
)

// closed is the answer of one question a run asks about what it was refused: is it
// about a place of the machine that stays closed to the executor whatever the project
// wrote (docs/DESIGN.md §7d).
//
// The list is the one the access policy of the project resolved, and it is read back
// here for one reason: the shapes of the habits a run may be answered by itself on are
// the shapes of a command, and a command that reads a key has the shape of a habit as
// easily as a heredoc that edits a file of the project has.
type closed struct {
	// folders are the places of secrets as this machine holds them, in every spelling
	// of the machine: a folder under a link is one place in two names, and a run is
	// refused for the one it wrote.
	folders []string
	// patterns are the places that are the same on every machine — `**/.env` and what
	// it stands for — and a path is one of them by its last folder.
	patterns []string
	// home is the home of the person: what a leading "~" of a path of a run stands
	// for, and where the secrets of the machine are.
	home string
}

// newClosed is the places that stay closed on this machine, as the policy of the
// project of a run resolved them and as the run will read them: the folders of the
// machine and the patterns of every machine, with the home of the person to read a
// path of a run in the spelling a run wrote it in.
func newClosed(policy access.Policy, home string) closed {
	places := closed{home: home}
	for _, place := range policy.Deny {
		if _, tail, isPattern := strings.Cut(place, "**/"); isPattern {
			places.patterns = append(places.patterns, tail)
			continue
		}
		places.folders = append(places.folders, place)
	}
	return places
}

// mark is the refusals of a run with a note on the ones that are about a secret: a
// report that shows a refusal of a key among refusals of a scratch folder reads as a
// habit, and the orchestrator decides on what it reads (docs/DESIGN.md §7a, §7d).
func (c closed) mark(refusals []string, calls []profile.Call, worktree string) []string {
	marked := make([]string, 0, len(refusals))
	for _, refusal := range refusals {
		if note := c.reaches(refusedPath(refusal), calls, worktree); note != "" {
			refusal += note
		}
		marked = append(marked, refusal)
	}
	return marked
}

// reaches is what a refusal about a secret adds to a report, and an empty string where
// the refusal is about nothing of the sort. A refused path is looked at as the run
// wrote it and as the machine holds it, and the commands of the run that name it are
// looked at too: a command of a shell is refused whole, so a run that was refused its
// scratch folder for a command that reads a key has been refused the key with it
// (docs/DESIGN.md §7a, §7d).
//
// The commands are looked for by the path without the glob an agent was refused — a
// refusal of `/tmp/*` is a refusal of every file under it, and the command that named
// one of them is the command the refusal came in with.
func (c closed) reaches(path string, calls []profile.Call, worktree string) string {
	if c.secret(path, worktree) {
		return secretPath
	}
	// A refusal that names no path has no command it came in with: a name of nothing is
	// in every command, and a refusal of a permission of any other kind is not a run
	// that was reading a key.
	named := plain(path)
	if named == "" {
		return ""
	}
	for _, call := range calls {
		if strings.Contains(call.Argument, named) && c.named(call.Argument) {
			return secretCommand
		}
	}
	return ""
}

// secret is whether a refused path is a place of secrets or lies in one. A `~` in the
// path is the home of the person and a relative path is the path it has from the
// worktree, because a shell of a run stands there and a path of `..` that climbs to a
// key is a key however it was written (docs/DESIGN.md §7d, §8).
func (c closed) secret(path, worktree string) bool {
	written := []string{plain(path), resolved(path, worktree, c.home)}
	if written[0] == "" {
		return false
	}
	if written[0] == written[1] {
		written = written[:1]
	}
	for _, place := range c.folders {
		if c.beneath(written, place) {
			return true
		}
	}
	for _, place := range written {
		if c.pattern(place) {
			return true
		}
	}
	return false
}

// named is whether a text of a run names a place of secrets: the place itself or
// anything under it, in every way a run may write it, and the last part of a pattern
// with a folder in front of it. A command that reaches for a key names it the way it
// was told to name it, and a command of a shell is refused whole (docs/DESIGN.md §7d).
func (c closed) named(text string) bool {
	for _, place := range c.folders {
		for _, name := range c.spelled(place) {
			if strings.Contains(text, name) {
				return true
			}
		}
	}
	for _, pattern := range c.patterns {
		if strings.Contains(text, "/"+pattern) {
			return true
		}
	}
	return false
}

// beneath says that one of the paths a run was refused is the place of secrets or
// something under it, in the spelling of either: a folder of the machine and the same
// folder written from the home of the person are one place in two words.
func (c closed) beneath(paths []string, place string) bool {
	for _, name := range c.spelled(place) {
		for _, path := range paths {
			if under(name, path) {
				return true
			}
		}
	}
	return false
}

// spelled is a place of secrets in every way a run may write it: as this machine holds
// it, and from the home of the person with a "~" in front of it. A machine that cannot
// say where the home of the person is has no spelling to add, and the place is left as
// it is written.
func (c closed) spelled(place string) []string {
	home := strings.TrimSuffix(c.home, string(filepath.Separator))
	switch {
	case home == "":
		return []string{place}
	case place == home:
		return []string{place, "~"}
	case strings.HasPrefix(place, home+string(filepath.Separator)):
		return []string{place, "~/" + filepath.ToSlash(strings.TrimPrefix(place, home+string(filepath.Separator)))}
	default:
		return []string{place}
	}
}

// pattern is whether a path is one of the patterns of the closed places: its last
// folder against the last part of the pattern, because "**" is a pattern of the same
// name on every machine and a `.env` is a `.env` wherever it is written.
func (c closed) pattern(path string) bool {
	if path == "" {
		return false
	}
	last := filepath.Base(filepath.FromSlash(path))
	for _, pattern := range c.patterns {
		if matched, err := filepath.Match(pattern, last); err == nil && matched {
			return true
		}
	}
	return false
}

// resolved is a path of a run as the machine holds it: a "~" in front of it is the home
// of the person, and a relative path is the path it has from the worktree. What a glob
// adds to it is left as it is: a place of secrets is a place whatever a pattern of it
// covers.
func resolved(path, worktree, home string) string {
	place := plain(path)
	switch {
	case place == "~":
		return home
	case strings.HasPrefix(place, "~/"):
		if home == "" {
			return place
		}
		return filepath.Join(home, filepath.FromSlash(strings.TrimPrefix(place, "~/")))
	case !filepath.IsAbs(place):
		return filepath.Join(worktree, place)
	}
	return place
}
