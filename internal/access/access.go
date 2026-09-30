// Package access is the policy of reading of an executor: the folders of the machine
// it may read outside the worktree of its task, and the places that stay closed to it
// whatever the project wrote in its file (docs/DESIGN.md §7d).
//
// A project says how to find its dependencies and not where they are — the paths are
// different on every machine — so crewflow runs those commands itself before the
// executor is started. A task of that project may ask for one folder more, with the
// reason a person wrote for it, and only for the run of that task. What comes out of
// either is reading and nothing else, and the places that hold a secret are closed
// whatever was asked: everything an executor opens goes into the context of a model
// (§7e).
//
// Every folder that is open comes out of here with where it came from and why, because
// the one calculation of this package has two readers: the rights of the agent, which
// are written from the paths, and the map of the access that a run is given and
// `crewflow doctor` reports, which is nothing without the source and the reason.
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

// Source is who asked for a folder of the reading policy of a run, as a report, a
// journal and the assignment of a run name it. A path alone does not say it: a folder
// the project needs on every machine of it and a folder one task needs for its own
// reason are both a path, and a person deciding about a run has to tell them apart
// (docs/DESIGN.md §7d).
type Source string

// The three who may ask. There is no fourth: a path that nobody asked for is not
// opened, and a path of a place of secrets is closed whatever the ask was.
const (
	// SourceAccess is the file of the project, the table `[access]` of it.
	SourceAccess Source = "[access]"
	// SourceTask is the specification of the task, for the run of that task only.
	SourceTask Source = "task"
	// SourceCrewflow is what crewflow itself opens or closes, whatever the project
	// and the task wrote.
	SourceCrewflow Source = "crewflow"
)

// Grant is one folder the executor of a run may read, with where it came from and why
// it is open. The three are one answer read three ways: the rights of the agent are
// written from the path, the map of the run and the report of `crewflow doctor` from
// all of it, and a review of a task from the reason (docs/DESIGN.md §7d).
type Grant struct {
	// Path is the folder as this machine spells it — a folder under a link is in both
	// spellings, because an agent asks about a path in the one it wrote.
	Path string `json:"path"`
	// Source is who asked for it.
	Source Source `json:"source"`
	// Reason is why it is open, in the words of whoever asked: the command of the
	// project, the key of the file of the project, or the sentence of a person.
	Reason string `json:"reason"`
}

// Policy is what the executor of a project may read outside the worktree of a task,
// and the places crewflow keeps closed to it. The two lists are handed to the agent
// by its own profile, and the second one is stronger than the first: a place that is
// in both is closed (docs/DESIGN.md §7d).
type Policy struct {
	// Read are the folders that may be read, each with where it came from and why.
	Read []Grant
	// Deny are the places that may never be read: the secrets of the machine, in the
	// same two spellings, and the patterns that are the same on every machine.
	Deny []string
}

// Paths is every folder of the list as plain paths, for the caller that needs the
// paths and nothing else: the rights of a profile are written from them, and a
// folder without a source is a folder of the machine and not an answer to a question.
func Paths(grants []Grant) []string {
	paths := make([]string, 0, len(grants))
	for _, grant := range grants {
		paths = append(paths, grant.Path)
	}
	return paths
}

// Line is one folder of the access of a run as a person reads it: the path, the hand
// that asked for it and the reason. The line is the same in the assignment of a run,
// in the first lines of its journal and in the report of `crewflow doctor`, because a
// person who reads one of them and then another has to find the same words, and
// because the three are one answer read three ways.
func (g Grant) Line() string {
	return g.Path + " · " + string(g.Source) + " · " + g.Reason
}

// Asked is one folder a task of a run asks the executor to read outside the worktree,
// with the reason a person wrote for it. Its source is always the task: a path of the
// project belongs in the file of the project, where every run of it is held to it,
// and a folder of one run is not a folder of the next (docs/DESIGN.md §7d, §7f).
type Asked struct {
	// Path is what the task named, as the task named it.
	Path string
	// Reason is why the task needs it, in the words of a person. A path without one is
	// not an ask: `crewflow task check` refuses the task before a run of it starts.
	Reason string
}

// Problem is a path that crewflow will not let the executor read, and why. It is what
// `crewflow doctor` shows a person and what the journal of a run says. A path of the
// project that was refused is not a run that failed, it is a run that goes on without
// it; a path of the task that was refused is a run that does not start at all, since
// the task is what the run was given to do (docs/DESIGN.md §7d, §7f).
type Problem struct {
	// Path is what was named, as it was named.
	Path string `json:"path"`
	// Source is who asked for it, so that a caller tells a folder the project could
	// not have from a folder the task cannot do without.
	Source Source `json:"source"`
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
// of the project, keeps the folders that are there, adds what the task asks to read
// beside them, and closes the places of secrets whatever they asked for. A folder is
// named in both the spelling the asker wrote and the one every link above it makes,
// because `/var` and `/private/var` are one folder in two names and an agent asks in
// the one it wrote. A path crewflow will not open comes back as a Problem, and the
// policy comes back without it: a run of the project goes on with what the file of
// the project was really after, and a run of a task does not start at all
// (docs/DESIGN.md §7d, §7f).
func Resolve(ctx context.Context, env Env, cfg config.Access, asked []Asked) (Policy, []Problem) {
	var problems []Problem
	// The asks of the project come first and the ask of the task after them, because
	// the project is what every run of it is held to and the task is what this one is
	// held to: the order of the policy is the order of what it answers to.
	var named []request
	for _, path := range cfg.Read {
		named = append(named, request{path: path, source: SourceAccess, reason: namedInFile})
	}
	for _, command := range cfg.ReadFrom {
		paths, reason := ask(ctx, env, command)
		if reason != "" {
			problems = append(problems, Problem{
				Path:   strings.Join(command, " "),
				Source: SourceAccess,
				Reason: reason,
			})
			continue
		}
		for _, path := range paths {
			named = append(named, request{
				path:   path,
				source: SourceAccess,
				reason: namedByCommand(command),
			})
		}
	}
	for _, one := range asked {
		named = append(named, request{path: one.Path, source: SourceTask, reason: one.Reason})
	}

	policy := Policy{Deny: denyList(env.Home)}
	for _, one := range named {
		policy.open(env.Home, &problems, one)
	}
	return policy, problems
}

// request is one path on its way into the policy, with the hand it came from: the two
// are kept together because a path of the project that was refused is a line of a
// report and a path of the task that was refused is a run that does not start.
type request struct {
	// path is what was named, as it was named.
	path string
	// source is who asked, and reason is why, in the words of whoever asked.
	source Source
	reason string
}

// open adds the folders of one ask to the policy, in every spelling of this machine,
// or says why crewflow will not open them. A folder that is already in the policy is
// left as it is: the first ask is the one that is shown, and a project is answered
// before a task of it.
func (p *Policy) open(home string, problems *[]Problem, one request) {
	folders, reason := readable(one.path, home)
	switch {
	case reason != "":
		*problems = append(*problems, Problem{Path: strings.TrimSpace(one.path), Source: one.source, Reason: reason})
	default:
		for _, folder := range folders {
			if !slices.ContainsFunc(p.Read, func(open Grant) bool { return open.Path == folder }) {
				p.Read = append(p.Read, Grant{Path: folder, Source: one.source, Reason: one.reason})
			}
		}
	}
}

// Why a folder that is open is open, in the words a person reads them in a report and
// a person of the task reads them in a review: what the project wrote is a key of its
// file or a command of it, and a command is named the way a person may run it by hand.
const (
	namedInFile = "the project named it in [access] of its file"
)

// namedByCommand is the reason of a folder a tool of the project named: the command
// itself, because a person who wants to see where the path came from runs that command
// and reads the same answer. The whole of it is on one line — a command of a project
// may hold a script with newlines in it, and a reason with a newline in it would break
// the line of a journal and the section of an assignment it stands in.
func namedByCommand(command []string) string {
	return "the project asked for it with `" + oneLine(strings.Join(command, " ")) + "`"
}

// oneLine is a text as one line of a report and a journal reads it.
func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
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
// executor may read in every way this machine spells it, or the reason the folder is
// not opened. A path with neither is one that is not there, and a path that is not
// there is not opened either — an empty line and a tool that named a folder it does
// not have are the same thing.
func readable(path, home string) ([]string, string) {
	place := strings.TrimSpace(path)
	switch {
	case place == "":
		return nil, ""
	case strings.HasPrefix(place, "~"):
		if home == "" {
			return nil, "~ stands for the home of the person, and this machine says it has none"
		}
		place = expand(place, home)
	case !filepath.IsAbs(place):
		return nil, "not an absolute path, and crewflow cannot tell what it is relative to"
	}
	folder := real(place)
	if reason := refusal(folder, home); reason != "" {
		return nil, reason
	}
	if _, err := os.Stat(folder); err != nil {
		return nil, ""
	}
	return spellings(place, folder), ""
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
// resolved: the folders as this machine holds them in both the spellings of it, and the
// patterns as they were written, because a pattern is the same everywhere.
func places(home string) (folders, patterns []string) {
	for _, secret := range secrets {
		if strings.Contains(secret, "**") {
			patterns = append(patterns, secret)
			continue
		}
		// A machine with no home of the person has no place to resolve "~" against, and
		// a run cannot start on one anyway. The places are left as they are written,
		// which names no folder of the machine.
		if home == "" {
			folders = append(folders, secret)
			continue
		}
		written := expand(secret, home)
		folders = append(folders, spellings(written, real(written))...)
	}
	return folders, patterns
}

// spellings is one place in every way this machine writes it: as the project named it
// and with every link above it followed, when the two are not the same path. On macOS
// `/tmp` and `/var` are links into `/private`, and an agent asks about a path in the
// spelling it wrote, so a rule in one of them does not cover the other and both are
// named (docs/DESIGN.md §7d).
func spellings(written, followed string) []string {
	if written == followed {
		return []string{followed}
	}
	return []string{written, followed}
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

// real is the path as this machine holds it: every link in the part of it that is
// there followed, and the rest joined as it was written. A folder that is not there
// yet still belongs to the tree the links above it make, and without that the same
// folder would be in two spellings on one machine: `/private/var/…/.ssh` for a folder
// that exists and `/var/…/.ssh` for one that does not.
func real(path string) string {
	clean := filepath.Clean(path)
	followed, err := filepath.EvalSymlinks(clean)
	if err == nil {
		return filepath.Clean(followed)
	}
	parent := filepath.Dir(clean)
	if parent == clean {
		return clean
	}
	return filepath.Join(real(parent), filepath.Base(clean))
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
