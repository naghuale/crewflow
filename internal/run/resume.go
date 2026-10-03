package run

import (
	"fmt"
	"os"
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
//
// The list grows by the facts of the journals: 01.10.2026 three runs of a day were
// stopped by the same three shapes of refusal, and each of them was continued by hand
// (F-084, F-093, F-095) — a shape a run is answered by itself on is a shape an
// orchestrator is not asked about twice. Nothing of the list is about the rights of a
// run: every text in it narrows what a run is to do and widens nothing.

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
	// reasonWorktree is a path of the machine whose end is a path of the worktree: a
	// path written out by hand whose beginning leads to a folder that is not this
	// worktree, a copy of the project or a worktree spelled with a letter out of
	// place (F-095, 01.10.2026).
	reasonWorktree reason = "worktree-path"
	// reasonService is a path of the service of the project — the state of the runs,
	// the journals, the hooks of the push, the folder git keeps its own files in —
	// and nothing of the work of the task is in it (F-093, 01.10.2026).
	reasonService reason = "service-path"
	// reasonProbe is a command of a shell that changed folder and asked git or the
	// filesystem about it: a copy probed by hand where a test of the run would answer
	// the same question (F-084, 01.10.2026).
	reasonProbe reason = "shell-probe"
	// reasonNone is a run that goes on with nothing: no attempt of it was refused a
	// habit crewflow knows, and the run of the task stops for the orchestrator.
	reasonNone reason = ""
)

// reasonRepeated is the reason a run carries that stopped on a habit crewflow has
// already answered in this very run. The refusal is honest about itself — the run is
// over, and it is over because the same habit came twice — and what is to be looked
// into is the habit: its mechanism or the rule of it, and not a run that has been told
// (docs/DESIGN.md §7a.1, §7j).
const reasonRepeated = "known-habit-repeated"

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
	reasonWorktree: {
		reason:   reasonWorktree,
		headline: "a path written out by hand was refused, and the file is a path from the root of the worktree",
		text: "The run was refused a permission for a path of the machine and stopped there. The file " +
			"it was after is " + theFile + " — a file of this worktree, named by a path whose " +
			"beginning leads somewhere else: a copy of the project, the worktree of another task, " +
			"another machine.\n" +
			"Address every file of the project by the path it has from the root of the worktree, from " +
			"the root and down. The map of the access of this run stands in the first lines of this " +
			"journal: the worktree is where the work of the task is, and the machine in front of a " +
			"path of the worktree is the machine of somebody else's copy.\n" +
			"Do the work of the task and open the change request of its branch.",
	},
	reasonService: {
		reason:   reasonService,
		headline: "a path of the service of the project is refused, and it is not what the task is about",
		text: "The run was refused a permission for a path of the machine and stopped there. What is " +
			"on that path belongs to the service of this project — the state of the runs, the " +
			"journals, the hooks of the push, the folder git keeps its own files in — and nothing of " +
			"the work of the task is in it. That path is outside the map of the access of this run.\n" +
			"A run does not learn the rules of the project by reading the files of the tool: the " +
			"rules are in the assignment of this run and in the files of the worktree, and the hooks " +
			"of the push say what they say without being read.\n" +
			"Go on with the work of the task and open the change request of its branch.",
	},
	reasonProbe: {
		reason:   reasonProbe,
		headline: "a probe of a copy in a shell is refused, check it in a test with t.TempDir()",
		text: "The run was refused a permission for one command of a shell and stopped there. The " +
			"command changed folder and asked git or the filesystem about it: a copy of the project " +
			"probed by hand, in a folder of the worktree or in the scratch of the run. A command of a " +
			"shell is refused whole, so the probe ends the run without a word of what git would have " +
			"said.\n" +
			"Check what a program does in a test of your own with a temporary folder — `t.TempDir()` " +
			"in Go, and not in a shell — and let the toolchain manage the files it needs for itself. " +
			"A test you want to watch while you work on it belongs in the package with the other " +
			"tests, as `zz_debug_test.go`, and you take it away before you commit.\n" +
			"Do the work of the task and open the change request of its branch.",
	},
}

// theFile is where the text of a habit takes the file of the refusal the run was
// stopped at. The answer to a path written out by hand is about that one file, and a
// text that named no file would tell a run how to address files without telling it
// which file it was after.
const theFile = "`the file of the worktree`"

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
// for, the path of the refusal the text of that habit answers about, and the text it
// goes on with. An empty one is a run that stops and waits for the orchestrator, and
// its reason is the empty one.
type resume struct {
	habit
	// place is what the text of the habit has to name and the refusal held: the file of
	// the worktree that a path written out by hand was after. It is empty for a habit
	// whose answer is the same for every refusal of it, and so is the hole in the text
	// of the one habit that has a place for the refusal.
	place string
}

// line is what the journal of the attempt holds about a run that goes on by itself,
// and what a watch shows of it: the attempt ended here, and crewflow went on in the
// same session because of this habit (docs/DESIGN.md §7a). Where the habit answers
// about one path, the line names it: a person reading the refusal above wants to see
// which file that refusal was really about.
func (n resume) line() string {
	line := "crewflow: resumed once — " + n.headline
	if n.place != "" {
		line += " (" + n.place + ")"
	}
	return line
}

// tells is what the next attempt of the task is asked: the text of the habit with the
// file of the refusal in the one place of it that is about this refusal and not about
// the habit (docs/DESIGN.md §7a).
func (n resume) tells() string {
	if n.place == "" {
		return n.text
	}
	return strings.Replace(n.text, theFile, n.place, 1)
}

// told is the one line of what a run was told when crewflow went on with it by itself,
// and the empty one for a habit this build of crewflow does not know: a state of
// yesterday may name a habit the catalog has dropped since, and a queue has no words for
// a habit it cannot name (docs/DESIGN.md §7a.1, §6a).
func told(habit string) string {
	name := reason(habit)
	if one, known := habits[name]; known {
		return " — " + one.headline
	}
	if head, known := heads[name]; known {
		return " — " + head
	}
	return ""
}

// heads are the one lines of the two shapes of a run that goes on by itself and are not a
// refusal of a permission: a refusal of the model provider and a run that hung. They are
// what the queue shows beside the mark of an attempt crewflow went on with, so that a
// project can see what it is being answered for without reading a journal (F-119, F-143,
// docs/DESIGN.md §6a, §7a).
var heads = map[reason]string{
	reason(ReasonProviderUnavailable): providerHeadline,
	reasonStanding:                    standingHead,
}

// goesOnByItself is what crewflow goes on with after an attempt that stopped on a
// habit it knows, an empty resume where a run has to stop, and what the attempt that has
// just ended is written with. Five things keep a run from going on by itself:
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
func (r *runner) goesOnByItself(result *Result, state State, calls []profile.Call) (resume, ended) {
	// A run that stopped for the model provider is a wait for a resource, and what crewflow
	// makes of it is worked out before anything about permissions: the provider refused the
	// run before a permission was ever asked, and the habit of a refusal is of no use to it
	// (F-119, docs/DESIGN.md §6a, §7a). The reason of the wait and the word of the provider
	// go into the attempt that has just ended, because that is where the queue of attention
	// reads them from and it reads no journal (§6a, §7h).
	if next, marked, ok := r.afterProvider(result, state); ok {
		return next, ended{reason: result.Reason, marked: marked}
	}
	// A run that could not reach the host through either route of the project has said so in
	// its reason, and the reason goes into the attempt that has just ended: the queue of
	// attention reads it from there and reads no journal (§6a, §7d). Crewflow does not go on
	// by itself — the one second attempt of the mode `fallback` is spent, and a third attempt
	// is a decision a person makes, not a program of crewflow (§7a.1, §7j).
	if named(result.Reason) == ReasonRouteUnavailable {
		return resume{}, ended{reason: result.Reason}
	}
	// A run crewflow stopped because it stood with a provider that has answered is a run
	// whose agent has hung, and it goes on in the same session once — the same habit of a
	// known shape, and the same one line in the journal (F-143, docs.DESIGN.md §7a).
	if result.Outcome == Stalled {
		return r.afterStanding()
	}
	if result.Outcome != BlockedPermission {
		return resume{}, ended{}
	}
	// A task that was stopped for reaching a secret is not continued by crewflow at all,
	// whatever the next attempt was refused: the attempt before this one is what says
	// that, and its outcome is what a person reads before the next run (docs/DESIGN.md
	// §7a.1, §8).
	if before, was := r.before(state); was && before.Outcome == BlockedSecret {
		return resume{}, ended{}
	}
	next, known := r.oneHabitOf(result.Rejections, calls)
	if !known {
		return resume{}, ended{}
	}
	// The attempt of the run that has just ended is the one before the resume that is
	// being worked out, and its own mark says whether crewflow is the one that went on
	// into it: a habit crewflow has already answered once in this run is a habit it does
	// not answer twice. The refusal says so in the report and in the state, with the
	// habit that was answered in it: what is to be looked into is the habit and not a
	// run that has been told (docs/DESIGN.md §7a.1, §7j).
	if before := r.own(state); before.AutoResumed == string(next.reason) {
		result.Reason = reasonRepeated + ": " + string(next.reason)
		return resume{}, ended{reason: result.Reason}
	}
	return next, ended{}
}

// ended is what the state of the task is written with when the attempt that has just ended
// comes to an end: the reason of the stop, which the queue of attention reads out of the
// state and not out of a journal, and what the model provider said about whether the
// refusal may be repeated (docs/DESIGN.md §6a, §7a.1).
//
// It is a change and not a state on purpose: a run says what it has to say with it, and
// the state of the task is that change made on the state as it is there now — beside the
// records of the commands that wrote it while this run worked (D-068 FINDING-4). Both
// fields are empty for a run that stopped for neither, and an empty reason is not written
// at all, so that the reason an earlier step of the run put into the attempt stays.
type ended struct {
	// reason is the reason of the stop of the attempt that has just ended.
	reason string
	// marked is what the model provider said about the refusal it stopped the run for:
	// `retryable` where it marked it itself, and `unknown` where it said nothing that
	// makes the failure temporary.
	marked string
}

// The reason of a run that crewflow stopped because it had shown nothing and was refused
// nothing by anybody: an executor that hung. The continuation is the habit of it, and the
// state of the task is written with the silence the attempt stood in (F-143,
// docs/DESIGN.md §7a).
const reasonStanding = "stalled"

// The line a person reads about a run that hung, and the whole of what the next attempt
// is told: where the work of the task is, and what it was doing when it last said anything
// (docs/DESIGN.md §7a, §7a.1).
const (
	standingHeadline = "the run showed nothing for %s and was stopped there; crewflow goes on in the same session"
	standingHead     = "the run had shown nothing for longer than the project agreed to put up with"
	standingTells    = "The run was stopped because it had shown nothing for %s. The last thing it " +
		"showed of itself was: %s\n" +
		"Nothing of the work is lost: the work of the task is in this worktree and in this session, where " +
		"the run left it. Do not start the task over and do not repeat what the run already did — look at " +
		"what you were doing and go on with it.\n" +
		"Do the work of the task and open the change request of its branch. crewflow goes on by itself once " +
		"and no more: a run that stands a second time waits for a person and says so in the queue of attention."
)

// afterStanding is what a run that stood with a provider that has answered goes on with:
// once, in the same session and the same worktree, with the silence and the last step of
// the run named in the text. Whether it goes on at all was settled before the executor was
// started — the project says no, or the attempt before it stopped for reaching a secret
// ([runner.mayGoOnAfterStanding]) — and the one promise of §7a is kept here: a run crewflow
// has already stopped once for a hang is not stopped again, so a run that hangs twice hangs
// for a person and stands in the queue of attention where they see it (§7a, §7j).
//
// The attempt that stood keeps no reason of its own: a run that stands a second time is a
// run nobody knows what it waits for, and that is what the queue says about it, with the
// last step of the run in the entry (§6a).
func (r *runner) afterStanding() (resume, ended) {
	silence, stood := r.hang.stoodOnce()
	if !stood {
		return resume{}, ended{}
	}
	return resume{habit: habit{
		reason:   reason(reasonStanding),
		headline: fmt.Sprintf(standingHeadline, Idle(silence.For)),
		text:     fmt.Sprintf(standingTells, Idle(silence.For), silence.LastStep),
	}}, ended{}
}

// oneHabitOf is the habit every refusal of an attempt is about, and whether crewflow
// knows all of them: a run that was refused two habits at once is a run crewflow
// cannot answer with one of the texts.
func (r *runner) oneHabitOf(rejections []string, calls []profile.Call) (resume, bool) {
	if len(rejections) == 0 {
		return resume{}, false
	}
	name, place := r.habitOf(rejections[0], calls)
	one, known := habits[name]
	if !known {
		return resume{}, false
	}
	for _, rejection := range rejections[1:] {
		if other, _ := r.habitOf(rejection, calls); other != one.reason {
			return resume{}, false
		}
	}
	return resume{habit: one, place: place}, true
}

// own is the attempt of this run as the state of the task holds it, read by its number: it
// is the attempt crewflow is about to go on from, and its own mark says whether this run is
// the one that answered it. It is read out of the state by the number the run was given and
// not as the last attempt in it: a state of a task is written by whoever holds the lock of it,
// and "the last attempt" is the attempt of another run as soon as one of the two goes on
// (docs.DESIGN.md §7).
func (r *runner) own(state State) Attempt {
	attempt, _ := state.Attempt(r.attempt)
	return attempt
}

// before is the attempt before the one of this run, and whether a task has one: a first
// attempt has nothing before it, and what ended the attempt before it is a fact crewflow
// kept — the state of a run that is over is what a next run is read from
// (docs.DESIGN.md §7).
func (r *runner) before(state State) (Attempt, bool) {
	if r.attempt < 2 {
		return Attempt{}, false
	}
	return state.Attempt(r.attempt - 1)
}

// habitOf is the habit one refusal of a run is about and the file of the worktree the
// text of that habit has to name — the classifier of a refusal, and the whole of what
// crewflow knows of the ways an executor stops (docs/DESIGN.md §7a). A refusal names the
// kind of the permission and what it was about, and only the second of the two says
// whether the run made a mistake crewflow knows of.
//
// A refusal of a place of secrets never reaches this: such a run is `blocked-secret`
// from the start and goes on by itself nothing (§7a.1, §7d, §8).
//
// The habits are told apart in the order of how much each of them says about the refusal:
// the most specific one wins, and the one that is about the machine alone is what is left.
// A folder beside the worktree of the task is the wrong `cd` of a run wherever the worktrees
// of a project live, a path named only in the text of a command is a refusal of a command and
// not of a file, and a probe of a copy in a shell is a habit of the worktree whatever the
// folder of it is. A path that was written out by hand comes next, and a path of the service
// of the project after it — and both of them stand **before** the temporary folder, and that
// is the one order this list cannot be wrong about: on a machine whose worktrees live in the
// temporary folder — every machine of Linux, where it holds every worktree of every project —
// the path of such a refusal is a path of `/tmp` as well, and a habit that is read as "it wrote
// to /tmp" is a habit crewflow answers with the wrong words (docs/DESIGN.md §7a.1, §7d). The
// temporary folder is what is left: it is the one habit that says where a folder of the machine
// is and not what the run wanted of the path (docs/DESIGN.md §7a.1).
func (r *runner) habitOf(refusal string, calls []profile.Call) (reason, string) {
	path := refusedPath(refusal)
	switch {
	case path == "":
		return reasonOther, ""
	case beside(path, r.worktree):
		return reasonOutside, ""
	case onlyNamed(path, calls, r.worktree):
		return reasonMention, ""
	case probed(path, calls, r.worktree):
		return reasonProbe, ""
	}
	if place := r.placeOf(path); place != "" {
		return reasonWorktree, place
	}
	if service(path, r.worktree, r.env.UserHome) {
		return reasonService, ""
	}
	if temporary(path) {
		return reasonTmp, ""
	}
	return reasonOther, ""
}

// placeOf is the path of the worktree a refused path is about, as a path from the root
// of the worktree: the file a run was after, named by a path of the machine that does not
// lead to this worktree. It is the empty one for a path that leads to no file of the
// worktree, and the answer of a habit that has no place of its own holds nothing
// (docs/DESIGN.md §7a.1).
//
// This is the one question the classifier asks the machine, and it asks it about the
// worktree of the task and nothing else: a path written out by hand is a habit of a run
// only where the file it was after is really there, and a run of a project that is
// checked out somewhere else has to be told the same thing.
func (r *runner) placeOf(path string) string {
	folder := plain(path)
	root := filepath.Clean(r.worktree)
	if folder == "" || root == "" || root == "." || !filepath.IsAbs(folder) {
		return ""
	}
	parts := strings.Split(folder, string(filepath.Separator))
	// The end of the path is looked through from the longest one down, and the longest
	// end that is a path of the worktree is the file the run was after: `internal/run/
	// resume.go` says more of it than `run/resume.go` would, and the part a run wrote by
	// hand is the beginning of the path. Two parts are the least there may be — a folder
	// and a file of the worktree — because a name that matches a file of the root of the
	// project is a coincidence of names and not a path a run meant.
	for at := 1; at <= len(parts)-2; at++ {
		rest := filepath.Join(parts[at:]...)
		if _, err := os.Stat(filepath.Join(root, rest)); err == nil {
			return filepath.ToSlash(rest)
		}
	}
	return ""
}

// probed says that the refusal is about a copy of the project that a command of a shell
// went looking at: the command changed folder into it and asked git or the filesystem
// about it. The folder is of the worktree — its scratch included — and the rule of the
// project is about a `cd` in a shell whatever the folder of it is: a command of a shell
// is refused whole, so a probe ends the run without a word of what it would have learned
// (docs/DESIGN.md §7a.1).
func probed(path string, calls []profile.Call, worktree string) bool {
	if !inside(path, worktree) {
		return false
	}
	folder := plain(path)
	for _, call := range calls {
		if !isShell(call.Tool) || !strings.Contains(call.Argument, folder) {
			continue
		}
		if folderChangedInto(call.Argument) != "" && asksAbout(call.Argument) {
			return true
		}
	}
	return false
}

// probes are the names of the programs a command of a shell asks git or the filesystem
// with: what a run of a task reaches for when it wants to know how a folder behaves. A
// name is not a shell word on its own and is read as it is written, in a mark of a shell
// and without the folder of the program in front of it.
var probes = map[string]bool{
	"git": true, "ls": true, "cat": true, "cp": true, "mv": true, "rm": true,
	"mkdir": true, "touch": true, "stat": true, "find": true, "pwd": true, "tree": true,
}

// asksAbout is whether a command of a shell asks git or the filesystem about something,
// which is what a command that changed folder and then said `ls` or `git status` does.
func asksAbout(command string) bool {
	for _, word := range strings.Fields(command) {
		if probes[filepath.Base(bareWord(word))] {
			return true
		}
	}
	return false
}

// service says that the path belongs to the service of the project rather than to the
// work of a task: the folder crewflow keeps the state, the journals and the hooks of the
// push in under the home of the person, and the folder git keeps its own files in, which
// is what `git rev-parse --git-dir` and `git rev-parse --git-path` report and what a
// run reads when it wants to know where the hooks of the project are (docs/DESIGN.md
// §7a.1, §7f).
//
// Everything under the folder the worktrees of the project are made in is out of it: a
// copy of the project is not the service of the tool, and a run that was refused a file
// of another worktree is a run the wrong `cd` and a path written out by hand are habits
// of — whether or not they are habits crewflow knows is not this question to answer. A
// path in the folder of crewflow whose end is a file of the worktree is a path written
// out by hand, and it is looked for before this one.
func service(path, worktree, home string) bool {
	folder := plain(path)
	root := filepath.Clean(worktree)
	if folder == "" || root == "" || root == "." {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(folder), "/") {
		if part == ".git" {
			return true
		}
	}
	place := resolved(folder, root, home)
	return home != "" && !under(filepath.Dir(root), place) &&
		under(filepath.Join(home, ".crewflow"), place)
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
