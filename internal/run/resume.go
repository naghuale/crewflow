package run

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/naghuale/crewflow/internal/run/profile"
)

// A run that stopped on a habit crewflow knows goes on by itself, once, in the same
// session and with the text of that habit: the work of a task is worth more than the
// minutes an orchestrator spends telling the same executor where its scratch is
// (docs/DESIGN.md §7a).
//
// The right of a run is not widened by a run having been interrupted. The habits are
// a constant of this file, `/tmp` stays closed — it holds the temporary files and the
// sockets of other programs — and a refusal crewflow has no habit for is a refusal an
// orchestrator decides about. That is what the list is: the line between the two.

// reason is the habit of an executor that a refusal is about. The names are the same
// in the state of a task, in a journal and in the answer of a run, and they are short
// because they are read in a table of runs.
type reason string

const (
	// reasonOther is a refusal crewflow has no habit for. The run of the task stops
	// there and a person decides, because crewflow does not know what the run wanted
	// of the path it was refused.
	reasonOther reason = "other"
	// reasonTmp is a file of the temporary folder of the machine, in the spelling
	// the run wrote it in.
	reasonTmp reason = "tmp"
	// reasonOutside is a folder beside the worktree of the task: the worktree of
	// another task of the same project, or anything else of the root the project
	// named its worktrees under.
	reasonOutside reason = "outside-cwd"
	// reasonMention is a path of the machine that a command of a shell names in its
	// text and never opens: a heredoc or a script that writes a file of the project,
	// where the run is refused the whole command for what it writes down.
	reasonMention reason = "mention"
	// reasonNone is a run that goes on with nothing: no attempt of it was refused a
	// habit crewflow knows, and the run of the task stops for the orchestrator.
	reasonNone reason = ""
)

// habits is what crewflow knows of the ways an executor stops a run, and the one text
// each of them goes on with. The texts are in English whatever the language of the
// project, because the tools crewflow runs read English and the assignment of a run is
// in English for the same reason (docs/DESIGN.md §7a).
var habits = map[reason]habit{
	reasonTmp: {
		reason:   reasonTmp,
		headline: "writing to /tmp is refused, scratch goes to .scratch/tmp",
		text: "The run was refused a permission for /tmp and stopped there. /tmp is closed to the " +
			"executor and stays closed: it holds the temporary files and the sockets of other " +
			"programs, and none of it is yours to read.\n" +
			"Keep what is temporary in `.scratch/tmp` inside the worktree — crewflow has made that " +
			"folder for this run and has pointed TMPDIR, TMP and TEMP at it, so a program that " +
			"keeps its temporary files where it was told needs no permission for them. A test you " +
			"want to watch while you work on it belongs in the package with the other tests, as " +
			"`zz_debug_test.go`, and you take it away before you commit.\n" +
			"Do the work of the task and open the change request of its branch.",
	},
	reasonOutside: {
		reason:   reasonOutside,
		headline: "a path beside the worktree is refused, the work of the task is inside it",
		text: "The run was refused a permission for a path beside the worktree of the task and " +
			"stopped there. Everything the task is about is inside the worktree, and the worktrees " +
			"of the other tasks of the project are not yours to read.\n" +
			"To run a command in another folder, put it in a subshell — `(cd dir && command)` — so " +
			"that the folder of the shell goes back to the worktree with it, and address a file of " +
			"another folder by the path it has from the worktree.\n" +
			"Do the work of the task and open the change request of its branch.",
	},
	reasonMention: {
		reason:   reasonMention,
		headline: "a path of the machine named in a command is refused, edit such files with the tools",
		text: "The run was refused a permission for one command of a shell and stopped there. The " +
			"command names a path of the machine in the text of what it writes — a heredoc or a " +
			"script that edits a file of the project — and the whole command is refused for a path " +
			"that is only written down, never opened.\n" +
			"Edit such files with the tools of edit and write, one file at a time, and leave the " +
			"path of the machine in the text you write rather than in a command that opens it.\n" +
			"Do the work of the task and open the change request of its branch.",
	},
}

// habit is what crewflow knows of one way of stopping a run: the name of it, the one
// line a journal and a report show of it, and the text the next attempt of the task is
// given.
type habit struct {
	// reason is the name of the habit, and it is what the state of a task, a journal
	// and the answer of a run call it.
	reason reason
	// headline is the one line a person reads about a run that went on by itself.
	headline string
	// text is what the next attempt is asked, the whole of what it needs to know to
	// stop stopping there.
	text string
}

// resume is a run that goes on by itself: the habit the attempt before it was refused
// for, and the text it goes on with. An empty one is a run that stops and waits for
// the orchestrator, and its reason is the empty one.
type resume struct {
	habit
}

// line is what the journal of the attempt holds about a run that goes on by itself,
// and what a watch shows of it: the attempt ended here, and crewflow went on in the
// same session because of this habit (docs/DESIGN.md §7a).
func (n resume) line() string {
	return "crewflow: resumed once — " + n.headline
}

// goesOnByItself is what crewflow goes on with after an attempt that stopped on a
// habit it knows, and an empty resume where a run has to stop. Five things keep a run
// from going on by itself:
//
//   - it did not stop on a refusal at all, or it was stopped by a person or by the
//     time of the project: those are decided by a person, and the orchestrator knows
//     what a person meant;
//   - it was refused a place of secrets, or a command that reaches for one: a run that
//     was reading a key is not a run that forgot where its scratch is, and a refusal
//     of secrets reaches the orchestrator whatever the shape of the command was
//     (docs/DESIGN.md §7a, §7d);
//   - one of its refusals is a habit of nobody's: the text of a habit would answer
//     for another;
//   - the refusals of the attempt are of more than one habit, for the same reason;
//   - the attempt that ended stopped for a secret: a run that reached for a key is not
//     one crewflow goes on from, and what a person has to say about it comes before
//     anything crewflow would do (docs/DESIGN.md §7a.1, §8);
//   - the attempt that ended went on by itself for this very habit: an executor that
//     was told where its scratch is and wrote into /tmp again is not to be told a
//     second time, and the orchestrator decides (docs/DESIGN.md §7a, §7j).
func (r *runner) goesOnByItself(result Result, state State, calls []profile.Call) resume {
	if result.Outcome != BlockedPermission {
		return resume{}
	}
	// A task that was stopped for reaching a secret is not continued by crewflow at all,
	// whatever the next attempt was refused: the attempt before this one is what says
	// that, and its outcome is what a person reads before the next run (docs/DESIGN.md
	// §7a.1, §8).
	if before, was := r.before(state); was && before.Outcome == BlockedSecret {
		return resume{}
	}
	next, known := r.oneHabitOf(result.Rejections, calls)
	if !known {
		return resume{}
	}
	// The attempt of the run that has just ended is the one before the resume that is
	// being worked out, and its own mark says whether crewflow is the one that went on
	// into it: a habit crewflow has already answered once in this run is a habit it does
	// not answer twice.
	if before := r.ended(state); before.AutoResumed == string(next.reason) {
		return resume{}
	}
	return next
}

// oneHabitOf is the habit every refusal of an attempt is about, and whether crewflow
// knows all of them: a run that was refused two habits at once is a run crewflow
// cannot answer with one of the texts.
func (r *runner) oneHabitOf(rejections []string, calls []profile.Call) (resume, bool) {
	if len(rejections) == 0 {
		return resume{}, false
	}
	one, known := habits[r.habitOf(rejections[0], calls)]
	if !known {
		return resume{}, false
	}
	for _, rejection := range rejections[1:] {
		if r.habitOf(rejection, calls) != one.reason {
			return resume{}, false
		}
	}
	return resume{habit: one}, true
}

// ended is the attempt of the run that has just ended, which is the last one in the
// state: it is the attempt crewflow is about to go on from, and its own mark says
// whether this run is the one that answered it.
func (r *runner) ended(state State) Attempt {
	if len(state.Attempts) == 0 {
		return Attempt{}
	}
	return state.Attempts[len(state.Attempts)-1]
}

// before is the attempt before the one that has just ended, and whether a task has one:
// a first attempt has nothing before it, and what ended the attempt before it is a fact
// crewflow kept — the state of a run that is over is what a next run is read from
// (docs/DESIGN.md §7).
func (r *runner) before(state State) (Attempt, bool) {
	if len(state.Attempts) < 2 {
		return Attempt{}, false
	}
	return state.Attempts[len(state.Attempts)-2], true
}

// habitOf is the habit one refusal of a run is about — the classifier of a refusal,
// and the whole of what crewflow knows of the ways an executor stops (docs/DESIGN.md
// §7a). A refusal names the kind of the permission and what it was about, and only
// the second of the two says whether the run made a mistake crewflow knows of.
//
// A refusal of a place of secrets never reaches this: such a run is `blocked-secret`
// from the start and goes on by itself nothing (§7a.1, §7d, §8).
func (r *runner) habitOf(refusal string, calls []profile.Call) reason {
	path := refusedPath(refusal)
	switch {
	case path == "":
		return reasonOther
	case temporary(path):
		return reasonTmp
	case beside(path, r.worktree):
		return reasonOutside
	case onlyNamed(path, calls, r.worktree):
		return reasonMention
	default:
		return reasonOther
	}
}

// refusedPath is the path or the command a refusal is about, and an empty string for
// a refusal that names none: a run that was refused something crewflow cannot read is
// not a habit of anybody.
func refusedPath(refusal string) string {
	_, about, found := strings.Cut(strings.TrimSpace(refusal), " ")
	if !found {
		return ""
	}
	return strings.TrimSpace(about)
}

// temporary says that the path is in the temporary folder of the machine, in both of
// its spellings: on a machine of macOS /tmp leads to /private/tmp, and a path of
// either of them is a file of another program.
func temporary(path string) bool {
	path = plain(path)
	return under("/tmp", path) || under("/private/tmp", path)
}

// beside says that the path is not in the worktree of the task and leaves it: another
// worktree of the same project, anything else that lies under the root they are made
// in, or a path of `..` that leaves wherever it is resolved from the worktree, which
// is what `cd ..` and a relative path come to. The worktree of the task is not beside
// it: everything inside it is allowed (docs/DESIGN.md §7a, §8).
func beside(path, worktree string) bool {
	folder, root := plain(path), filepath.Clean(worktree)
	if folder == "" || root == "." || root == "" {
		return false
	}
	if !filepath.IsAbs(folder) {
		// A relative path is resolved by a shell that stands in the worktree, and a
		// path of `..` leaves the worktree wherever that shell is.
		joined := filepath.Join(root, folder)
		return joined != root && !strings.HasPrefix(joined, root+string(filepath.Separator))
	}
	if inside(folder, root) {
		return false
	}
	return filepath.Dir(folder) == filepath.Dir(root)
}

// onlyNamed says that the path is named in what the run did and never opened by it:
// every call that holds it is a command of a shell that writes a file of the worktree,
// and there is at least one of them. That is a heredoc of a file of the project and a
// script that edits one, where the run is refused the whole command for a path that
// is part of what it writes down (docs/DESIGN.md §7a).
func onlyNamed(path string, calls []profile.Call, worktree string) bool {
	named := false
	for _, call := range calls {
		if !strings.Contains(call.Argument, path) {
			continue
		}
		if !isShell(call.Tool) || !writesInside(call.Argument, worktree) {
			return false
		}
		named = true
	}
	return named
}

// shells are the names an agent calls a shell by, in the marks of the names of the
// agents left out: a call of a shell is a call of a shell whatever it is called.
var shells = map[string]bool{"bash": true, "sh": true, "shell": true, "zsh": true}

// isShell says whether a call is of a shell, which is the only program whose argument
// is a command rather than a path.
func isShell(tool string) bool {
	return shells[strings.ToLower(strings.TrimSpace(tool))]
}

// writesInside says that a command of a shell writes a file of the worktree: what it
// redirects into, what it hands to a tee, and a file of the worktree that it edits
// where it stands. A command that only reads a file of the worktree writes nothing,
// and a command that writes a file of the machine is not one crewflow goes on for.
func writesInside(command, worktree string) bool {
	for _, target := range targets(command) {
		if inside(target, worktree) {
			return true
		}
	}
	return editsInPlace.MatchString(command) && namesFileOf(command, worktree)
}

// redirectedTo is what a command of a shell writes into: the target of every `>` and
// `>>` of it, with the marks of a shell around the target left out. A redirect of a
// descriptor — `2>&1` — is not a file, and the marks left out are the ones that tell
// the two apart.
var redirectedTo = regexp.MustCompile(`(?:^|[^0-9>&])>>?\s*(?:"([^"]*)"|'([^']*)'|(\S+))`)

// teedTo is what a command of a shell hands to a tee, which is what it writes into
// under another name, with or without the mark that says it appends.
var teedTo = regexp.MustCompile(`(?:^|\s)tee(?:\s+-a)?\s+(?:"([^"]*)"|'([^']*)'|(\S+))`)

// editsInPlace is whether a command edits a file where it stands rather than writing
// a new one: `sed -i`, `perl -i`, `patch` and `ed` are the ones a run of a task uses to
// change a file of the project in place.
var editsInPlace = regexp.MustCompile(`\b(?:sed|perl)\b[^\n]*?\s--?[a-z-]*i[a-z-]*|\b(?:patch|ed)\b`)

// targets is the paths a command of a shell writes into, as the marks of a shell
// around them left out.
func targets(command string) []string {
	var written []string
	for _, pattern := range []*regexp.Regexp{redirectedTo, teedTo} {
		for _, match := range pattern.FindAllStringSubmatch(command, -1) {
			for _, group := range match[1:] {
				if group != "" {
					written = append(written, group)
				}
			}
		}
	}
	return written
}

// namesFileOf says that one of the words of a command is a file of the worktree,
// resolved against it where the word is relative, because a shell in the worktree
// writes relative paths there.
func namesFileOf(command, worktree string) bool {
	for _, word := range strings.Fields(command) {
		if inside(word, worktree) {
			return true
		}
	}
	return false
}

// inside says that a path of a command is a file of the worktree, with a relative one
// resolved against it: a shell that stands in the worktree means the worktree by every
// path of its own tree.
func inside(path, worktree string) bool {
	root := filepath.Clean(worktree)
	if root == "" || root == "." {
		return false
	}
	if !filepath.IsAbs(path) {
		if !isPath(path) {
			return false
		}
		path = filepath.Join(root, path)
	}
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

// isPath says that a word of a command is a path of the worktree and not something
// else: a relative path names a folder of its own, while an option of a program, a
// place of the machine written with a `~` in front of it and a value out of a variable
// are words a shell resolves somewhere else or not at all. A name of a file in the
// folder the shell stands in — `Makefile` — is not one either: it is the one file
// whose place a run is told about least.
func isPath(word string) bool {
	return strings.Contains(word, "/") &&
		!strings.HasPrefix(word, "-") && !strings.HasPrefix(word, "~") && !strings.HasPrefix(word, "$")
}

// plain is a path as a shell and an agent wrote it, with what a glob and a link add
// to it left out: `/tmp/*` is `/tmp` and a path of a worktree is the folder itself.
// The path is not resolved, because the classifier reads what a run was told and not
// what the machine would answer.
func plain(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimRight(path, " \t*")
	if path == "" {
		return ""
	}
	return filepath.Clean(filepath.FromSlash(path))
}

// under says that a path is a folder or lies in it. It is not filepath.Rel: a folder
// that only shares a beginning with another is not inside it, and `/tmpfoo` is not in
// `/tmp`.
func under(folder, path string) bool {
	return path == folder || strings.HasPrefix(path, folder+string(filepath.Separator))
}
