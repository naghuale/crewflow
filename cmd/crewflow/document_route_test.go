package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/forge"
	taskrun "github.com/naghuale/crewflow/internal/run"
	"github.com/naghuale/crewflow/internal/secret"
)

// theAnswerOfEveryCommandIsTheDocumentItsPolicyNames: every kind of document the boundary
// publishes is published by a command that runs for real here — with its own file of a project,
// its own host and, where the command needs one, its own store of secrets. The answer of each of
// them is read as the document it claims to be, and four things are said about it:
//
//   - it is a document a program can read, and every field of it is named by the policy of its
//     own kind — a field nobody classified is a refusal of the whole document (D-082, R224-008);
//   - the value of a run the case planted is nowhere in it — that is the cutting, and a test that
//     only looked for the absence of a marker would not see it;
//   - the marker stands where the value stood — the words around it are left;
//   - every word of a closed list and every name of the format is in it byte for byte — the
//     values the format is read by, whole and as they were.
//
// A document that no value of a run can reach says so in the case, with the reason: a test, which
// plants a value and sees nothing happen, must say why nothing was expected to happen (R224-015,
// docs/DESIGN.md §7e).
func TestTheAnswerOfEveryCommandIsTheDocumentItsPolicyNames(t *testing.T) {
	for _, tc := range theCommandsThatPublishADocument() {
		t.Run(string(tc.doc), func(t *testing.T) {
			document, planted := tc.run(t)

			var tree any
			if err := json.Unmarshal(document, &tree); err != nil {
				t.Fatalf("the answer of the command is not a document: %v\n%s", err, document)
			}
			for _, path := range pathsOfADocument(tc.doc, tree) {
				if _, _, known := secret.ClassOf(tc.doc, path); !known {
					t.Errorf("the answer of the command holds the field %q, and the policy of %q does not name it",
						path, tc.doc)
				}
			}
			if planted.canary == "" {
				if planted.note == "" {
					t.Fatalf("the case planted no value and says nothing about why no value of a run " +
						"reaches this document")
				}
				if bytes.Contains(document, []byte(secret.Redacted)) {
					t.Errorf("the answer of the command is\n%s\nwant no marker of a cut in it: %s",
						document, planted.note)
				}
				return
			}
			if len(planted.cut) == 0 && planted.note == "" {
				t.Errorf("the case planted the value %q and says neither a field where it has to stand cut "+
					"nor a reason why no value of a run reaches this document", planted.canary)
			}
			for _, path := range planted.cut {
				said, there := theFieldOfTheDocument(tree, path)
				if !there {
					t.Errorf("the field %q of the answer is not there: the document is\n%s", path, document)
					continue
				}
				if !strings.Contains(said, secret.Redacted) {
					t.Errorf("the field %q of the answer is %q, want %q where the value of the run stood",
						path, said, secret.Redacted)
				}
			}
			for path, want := range planted.whole {
				said, there := theFieldOfTheDocument(tree, path)
				if !there {
					t.Errorf("the field %q of the answer is not there: the document is\n%s", path, document)
					continue
				}
				if said != want {
					t.Errorf("the field %q of the answer is %q, want %q: a word of a closed list and a name "+
						"of the format are kept whole, and a value of a run that fell into one of them stays "+
						"in it (R224-001, R224-002)", path, said, want)
				}
			}
			// The words of a document are the only place a value of a run may not stand in:
			// in the fields of words it is not there, in the fields of the format it is (D-082).
			for _, field := range theFieldsOfTheDocument(tc.doc, tree) {
				kind, _, known := secret.ClassOf(tc.doc, field.path)
				if !known || (kind != "text" && kind != "reason") {
					continue
				}
				if strings.Contains(field.said, planted.canary) {
					t.Errorf("the field %q of the answer is %q, want the value of the run %q taken out of it",
						field.path, field.said, planted.canary)
				}
			}
		})
	}
}

// aCommandThatPublishes is a command of the project, the kind of the document it prints, and what
// the case put into it: the value of a run, the fields where it has to stand cut, and the fields
// that have to be left whole.
type aCommandThatPublishes struct {
	doc secret.Document
	run func(t *testing.T) (document []byte, planted planted)
}

// planted is what a case put into the answer of a command: the value of a run it taught the
// boundary of the command, the fields where that value has to stand cut, the words and the names
// of the format that have to be in the document byte for byte — and, when no value of a run
// reaches the document at all, why.
type planted struct {
	canary string
	cut    []string
	whole  map[string]string
	note   string
}

// theCommandsThatPublishADocument is every kind of the registry of the boundary and the command
// that publishes it, read as a person reads it: `crewflow task list -json` and the rest of the
// queue of attention, the run and the continuation of it, the admission of a pair, the review and
// the merge and the check after it, the machine of the person and the route of the project, the
// journal of the project, the key of the App, and the state of a task, which is written by the run
// rather than printed (D-082, §6a, §7e, §7h).
func theCommandsThatPublishADocument() []aCommandThatPublishes {
	return []aCommandThatPublishes{
		{doc: secret.DocumentTaskList, run: theAnswerOfTheList},
		{doc: secret.DocumentTaskAttention, run: theAnswerOfTheAttention},
		{doc: secret.DocumentTaskAttentionAll, run: theAnswerOfTheAttentionOfEveryProject},
		{doc: secret.DocumentTaskCheck, run: theAnswerOfTheCheckOfATask},
		{doc: secret.DocumentTaskRun, run: theAnswerOfTheRun},
		{doc: secret.DocumentTaskResume, run: theAnswerOfTheResume},
		{doc: secret.DocumentTaskAdmit, run: theAnswerOfTheAdmission},
		{doc: secret.DocumentTaskCheckStalled, run: theAnswerOfTheStandings},
		{doc: secret.DocumentReview, run: theAnswerOfTheReview},
		{doc: secret.DocumentMerge, run: theAnswerOfTheMerge},
		{doc: secret.DocumentVerify, run: theAnswerOfTheVerification},
		{doc: secret.DocumentDoctor, run: theAnswerOfTheDoctor},
		{doc: secret.DocumentDoctorNetwork, run: theAnswerOfTheChecksOfTheRoute},
		{doc: secret.DocumentNetworkProxyList, run: theAnswerOfTheProfiles},
		{doc: secret.DocumentNetworkProxyTest, run: theAnswerOfTheTestOfARoute},
		{doc: secret.DocumentChangelogCheck, run: theAnswerOfTheJournal},
		{doc: secret.DocumentAuthAppCheck, run: theAnswerOfTheKeyOfAnApp},
		{doc: secret.DocumentState, run: theAnswerOfTheStateOfTheTask},
	}
}

// aFieldOfTheDocument is a field of a document with the path it stands at, for a test that reads
// every word of a document (D-082, docs/DESIGN.md §7e).
type aFieldOfTheDocument struct {
	path string
	said string
}

// theFieldsOfTheDocument is every leaf of a document with the path it stands at.
func theFieldsOfTheDocument(doc secret.Document, node any) []aFieldOfTheDocument {
	var fields []aFieldOfTheDocument
	walkLeaves(doc, node, "", &fields)
	return fields
}

// walkLeaves is the tree of a document leaf by leaf, under the path each leaf stands at.
func walkLeaves(doc secret.Document, node any, path string, fields *[]aFieldOfTheDocument) {
	switch held := node.(type) {
	case map[string]any:
		for name, value := range held {
			under := ofPath(path, name)
			if _, _, known := secret.ClassOf(doc, under); !known {
				under = ofPath(path, "*")
			}
			walkLeaves(doc, value, under, fields)
		}
	case []any:
		for _, item := range held {
			walkLeaves(doc, item, path+"[]", fields)
		}
	case string:
		*fields = append(*fields, aFieldOfTheDocument{path: path, said: held})
	}
}

// putRunWithWordsIntoTheState writes the state of a task whose title holds the words of a run, and
// whose last attempt stands: the list, the queue and the check of the standing read the state, not
// the tracker (docs/DESIGN.md §6, §6a).
func putRunWithWordsIntoTheState(t *testing.T, host *host, title string, ended time.Time) {
	t.Helper()
	journals := taskrun.JournalsOf(host.home, "naghuale-crewflow")
	state := taskrun.State{Number: 43, Title: title, Branch: "crewflow/43-task", Profile: "opencode"}
	state = state.NextAttempt(state.NextNumber(), taskrun.StartOf{
		Started: ended.Add(-42 * time.Minute), Step: "the executor of the run",
		Journal: journals.JournalPath(43, 1), Executor: "opencode",
		Identity: taskrun.Identity{Mode: "owner", Description: "owner — the login gh naghuale (shared rights)"}})
	state = state.Ended(1, ended, taskrun.TimedOut)
	keepsStateThroughTheBoundary(t, journals.StatePath(43), state, title)
}

// keepsStateThroughTheBoundary is a state as a run writes it: the words of a run that reached the
// boundary are out of the file on disk, and the commands that read the state — the list, the queue,
// the check of what stands — never learn the value themselves (D-082, docs/DESIGN.md §7e, §7h).
func keepsStateThroughTheBoundary(t *testing.T, path string, state taskrun.State, value string) {
	t.Helper()
	said, _, err := secret.NewOut(secret.Chosen(value)...).StateDocument(state)
	if err != nil {
		t.Fatalf("the state of the task through the boundary: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("make the folder of the state of the task: %v", err)
	}
	if err := os.WriteFile(path, append(said, '\n'), 0o600); err != nil {
		t.Fatalf("write the state of the task into %s: %v", path, err)
	}
}

// putRunThatStandsWithWords is the run that stands, with the words of a run in the title of its
// task, and the clock and the machine of the fixture of the package are given back to the run that
// comes after: a case of the whole route must not answer with the machine of another one (D-088).
func putRunThatStandsWithWords(t *testing.T, host *host, title string) {
	t.Helper()
	clock, machine := taskClock, taskMachine
	t.Cleanup(func() { taskClock, taskMachine = clock, machine })
	putRunThatStands(t, host)
	withWordsInTheStateOfTheRunThatStands(t, host, title)
}

// withWordsInTheStateOfTheRunThatStands is the run that stands with the words of a run in the title
// of its task, written through the boundary: the check of what stands reads the state, and the
// command knows no value of a run itself (D-082, docs/DESIGN.md §7e).
func withWordsInTheStateOfTheRunThatStands(t *testing.T, host *host, title string) {
	t.Helper()
	journals := taskrun.JournalsOf(host.home, "naghuale-crewflow")
	path := journals.StatePath(43)
	state, err := taskrun.LoadState(path)
	if err != nil {
		t.Fatalf("read the state of the task from %s: %v", path, err)
	}
	state.Title = title
	keepsStateThroughTheBoundary(t, path, state, title)
}

// theAnswerOf runs a command of the project as a person runs it and gives back its answer.
func theAnswerOf(t *testing.T, args ...string) ([]byte, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	if code != exitOK {
		// The code is what a program reads to decide what a command did, and it is not
		// what the document is: `task run` prints the answer of a run and then says it
		// merged nothing (D-082, docs/DESIGN.md §6a). The case reads the document.
		t.Logf("the command %q ended with %d and said: %s", strings.Join(args, " "), code, stderr.String())
	}
	return stdout.Bytes(), code
}

// theRunKnows teaches the boundary of a command the value of a run, the way a run teaches its
// own boundary the credentials of the route it went out by: a program of a test hands the value to
// the boundary through the roles of the project, and the boundary takes it out of everything the
// command publishes afterwards (docs.DESIGN.md §7e).
func theRunKnows(t *testing.T, canary string, seam *func(config.Config, forge.Env) (forge.Set, error)) {
	t.Helper()
	was := *seam
	t.Cleanup(func() { *seam = was })
	*seam = func(cfg config.Config, env forge.Env) (forge.Set, error) {
		env.Out.Learn(secret.Chosen(canary)...)
		return was(cfg, env)
	}
}

// theAnswerOfTheList is `crewflow task list -json` over a project with a run of one task, and the
// title of the task is the value of the run: a title is the words of a person, and a title of a
// task is everywhere the state of it is read.
func theAnswerOfTheList(t *testing.T) ([]byte, planted) {
	const canary = "s3cr3t-of-the-run"
	host := &host{task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	putRunWithWordsIntoTheState(t, host, "the run of a task "+canary, time.Now().Add(-time.Hour))
	theRunKnows(t, canary, &stateRoles)

	document, _ := theAnswerOf(t, "task", "list", "-config", project, "-json")

	return document, planted{
		canary: canary,
		cut:    []string{"runs[].title"},
		whole: map[string]string{
			"runs[].outcome":  "timeout",
			"runs[].identity": "owner",
			"runs[].run":      "43-1",
		},
	}
}

// theAnswerOfTheAttention is `crewflow task attention -json` over the same project: the queue is
// where the words of a run are read first — why it stands, what to do about it — and they are cut
// where they are read.
func theAnswerOfTheAttention(t *testing.T) ([]byte, planted) {
	const canary = "s3cr3t-of-the-run"
	host := &host{opened: true, task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	putRunWithWordsIntoTheState(t, host, "the run of a task "+canary, time.Now().Add(-time.Hour))
	theRunKnows(t, canary, &stateRoles)

	document, _ := theAnswerOf(t, "task", "attention", "-config", project, "-json")

	return document, planted{
		canary: canary,
		cut:    []string{"attention[].title"},
		whole:  map[string]string{"attention[].reason": "run-timeout", "attention[].run": "43-1"},
	}
}

// theAnswerOfTheAttentionOfEveryProject is `crewflow task attention -json -all`: the same
// document over every project of the machine.
func theAnswerOfTheAttentionOfEveryProject(t *testing.T) ([]byte, planted) {
	const canary = "s3cr3t-of-the-run"
	host := &host{opened: true, task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	putRunWithWordsIntoTheState(t, host, "the run of a task "+canary, time.Now().Add(-time.Hour))
	theRunKnows(t, canary, &stateRoles)

	document, _ := theAnswerOf(t, "task", "attention", "-all", "-config", project, "-json")

	return document, planted{
		canary: canary,
		cut:    []string{"attention[].title"},
		whole:  map[string]string{"attention[].reason": "run-timeout", "attention[].run": "43-1"},
	}
}

// theAnswerOfTheCheckOfATask is `crewflow task check <N> -json`: what the task is missing before
// it may be run, and the words of the task beside them.
func theAnswerOfTheCheckOfATask(t *testing.T) ([]byte, planted) {
	const canary = "s3cr3t-of-the-run"
	host := &host{task: taskOf(43)}
	host.task.Title = "the run of a task " + canary
	host.use(t)
	theRunKnows(t, canary, &taskRoles)

	document, _ := theAnswerOf(t, "task", "check", "43", "-config", host.config(t), "-json")

	return document, planted{
		canary: canary,
		cut:    []string{"title"},
		whole:  map[string]string{"task": "43", "ready": "true"},
	}
}

// theAnswerOfTheRun is `crewflow task run <N> -json`: the answer a program reads to decide what a
// run did, with the words of the task and the reason of the run cut out of it and the names of the
// run — the branch, the working folder, the journal, the mode, the outcome — whole.
func theAnswerOfTheRun(t *testing.T) ([]byte, planted) {
	const canary = "s3cr3t-of-the-run"
	host := &host{task: taskOf(43)}
	host.task.Title = "the run of a task " + canary
	host.use(t)
	theRunKnows(t, canary, &taskRoles)

	document, _ := theAnswerOf(t, "task", "run", "43", "-config", host.config(t), "-json")

	return document, planted{
		canary: canary,
		cut:    []string{"title"},
		whole: map[string]string{
			"task":      "43",
			"identity":  "owner",
			"profile":   "opencode",
			"continued": "false",
			"branch":    "crewflow/43-the-run-of-a-task-s3cr3t-of-the-run",
		},
	}
}

// theAnswerOfTheResume is `crewflow task resume <N> -json`: the same document of the same run,
// published by the command that goes on from the point where it stood.
func theAnswerOfTheResume(t *testing.T) ([]byte, planted) {
	const canary = "s3cr3t-of-the-run"
	host := &host{task: taskOf(43), opened: true}
	host.task.Title = "the run of a task " + canary
	host.use(t)
	project := host.config(t)
	host.noIdentity = theWindowOfTheKeychain()
	if code := theCodeOf(t, "task", "run", "43", "-config", project); code != exitFailure {
		t.Fatalf("crewflow task run = %d, want %d: the run of the case has to stand at the window first",
			code, exitFailure)
	}
	host.noIdentity = nil
	theRunKnows(t, canary, &taskRoles)

	document, _ := theAnswerOf(t, "task", "resume", "43", "-config", project, "-json")

	return document, planted{
		canary: canary,
		cut:    []string{"title"},
		whole:  map[string]string{"task": "43", "identity": "owner", "continued": "true"},
	}
}

// theAnswerOfTheAdmission is `crewflow task admit <N> <N> -json`: the record of a pair, the words
// a person entered for its criteria and the file the record is written in.
func theAnswerOfTheAdmission(t *testing.T) ([]byte, planted) {
	const canary = "s3cr3t-of-the-run"
	host := &host{task: taskWithGlobs(43, "docs/**"),
		tasks: map[int]forge.Task{41: taskWithGlobs(41, "internal/run/**")}}
	host.use(t)
	theRunKnows(t, canary, &taskRoles)

	document, _ := theAnswerOf(t, "task", "admit", "41", "43",
		"-c", "K2=fail: the agent asked for "+canary, "-c", "K3=pass", "-c", "K4=pass",
		"-c", "K5=pass", "-c", "K6=pass", "-c", "K7=pass", "-c", "K8=pass",
		"-config", host.config(t), "-json")

	return document, planted{
		canary: canary,
		cut:    []string{"criteria.K2.reason"},
		whole: map[string]string{
			"decision":           "denied",
			"criteria.K2.result": "fail",
			"criteria.K3.result": "pass",
			"valid_while.shared": "e3b0c44298fc1c14",
		},
	}
}

// theAnswerOfTheStandings is `crewflow task check-stalled -json`: the runs that are standing, how
// long they have stood and the title of the task each of them is of.
func theAnswerOfTheStandings(t *testing.T) ([]byte, planted) {
	const canary = "s3cr3t-of-the-run"
	host := &host{opened: true, task: taskOf(43)}
	host.use(t)
	putRunThatStandsWithWords(t, host, "the run of a task "+canary)
	theRunKnows(t, canary, &taskRoles)

	document, _ := theAnswerOf(t, "task", "check-stalled", "-config", host.config(t), "-json")

	return document, planted{
		canary: canary,
		cut:    []string{"[].title"},
		whole:  map[string]string{"[].task": "43", "[].run": "43-1", "[].stalled": "true"},
	}
}

// theAnswerOfTheReview is `crewflow review <PR> -json`: the facts of a change and the answer of the
// gate about it. The value of a run is planted in the name of a file the change touches — the
// files of a change are the words of the task beside the words of the host.
func theAnswerOfTheReview(t *testing.T) ([]byte, planted) {
	const canary = "s3cr3t-of-the-run"
	host := newMergeHost(t)
	host.reviewHost.files = []string{"internal/secret/" + canary + ".go"}
	host.remote = reviewHead
	theRunKnows(t, canary, &reviewRoles)

	document, _ := theAnswerOf(t, "review", "7", "-config", writeConfig(t, mergeConfig), "-json")

	return document, planted{
		canary: canary,
		cut:    []string{"outside[]"},
		whole: map[string]string{
			"change":        "7",
			"files":         "1",
			"head":          reviewHead,
			"repository":    "naghuale/crewflow",
			"verdict.ready": "false",
		},
	}
}

// theAnswerOfTheMerge is `crewflow merge <PR> -json`: what came of the merge — whose name it was
// pushed under, which commit is at the head of the default branch, and what is left to do by hand.
func theAnswerOfTheMerge(t *testing.T) ([]byte, planted) {
	const canary = "s3cr3t-of-the-run"
	host := newMergeHost(t)
	host.remote = reviewHead
	theRunKnows(t, canary, &mergeRoles)

	document, _ := theAnswerOf(t, "merge", "7", "-config", writeConfig(t, mergeConfig), "-json")

	return document, planted{canary: canary, note: "the answer is the words of crewflow and of the host about " +
		"one change: the run that merged it left no words of its own here, and the canary of the case is taught " +
		"to the boundary so that a value of the run, had one stood in this document, would be cut. The cutting " +
		"itself is covered by TestTheAnswerOfTheReview: the words of a change stand under the same boundary"}
}

// theAnswerOfTheVerification is `crewflow verify <PR> -json`: what is true about the change after
// the merge.
func theAnswerOfTheVerification(t *testing.T) ([]byte, planted) {
	const canary = "s3cr3t-of-the-run"
	host := newMergeHost(t)
	host.remote = reviewHead
	theRunKnows(t, canary, &mergeRoles)

	document, _ := theAnswerOf(t, "verify", "7", "-config", writeConfig(t, mergeConfig), "-json")

	return document, planted{canary: canary, note: "the answer is the words of crewflow and of the host about one " +
		"change after its merge: no value of a run stands in them, and the canary of the case is taught to the " +
		"boundary all the same. The cutting of a document of a change is covered by TestTheAnswerOfTheReview"}
}

// theAnswerOfTheDoctor is `crewflow doctor -json`: the report of the machine of the person — the
// checks, the folders a run may read and why they are open, and the debt of trust between the
// three subjects of the project.
func theAnswerOfTheDoctor(t *testing.T) ([]byte, planted) {
	const canary = "s3cr3t-of-the-run"
	theRunKnows(t, canary, &taskRoles)

	document, _ := theAnswerOf(t, "doctor", "-config", writeProject(t), "-json")

	return document, planted{canary: canary, note: "the report of the machine says what the checks, git and the " +
		"keychain of macOS answered: no value of a run stands in it, and the canary of the case is taught to the " +
		"boundary all the same. The cutting of a text field is covered by TestTheAnswerOfTheCheckOfATask"}
}

// theAnswerOfTheChecksOfTheRoute is `crewflow doctor network -json`: the checks of the route of
// the project, one per ability.
func theAnswerOfTheChecksOfTheRoute(t *testing.T) ([]byte, planted) {
	const canary = "s3cr3t-of-the-run"
	useNetworkOfTheTest(t, &storeOfTheRoute{}, nil)
	theRunKnows(t, canary, &taskRoles)

	document, _ := theAnswerOf(t, "doctor", "network", "-config", writeConfig(t, plainProject), "-json")

	return document, planted{canary: canary, note: "the checks of the route say what the route answered about " +
		"every ability of it: no value of a run stands in them, and the canary of the case is taught to the " +
		"boundary. The cutting of a text field is covered by TestTheAnswerOfTheAdmission"}
}

// theAnswerOfTheProfiles is `crewflow network proxy list -json`: the profiles the project declares.
// There is no text field in this document at all — a name, a kind, an address, a number, a flag
// and two words of closed lists — so no value of a run reaches it and there is nothing to cut.
func theAnswerOfTheProfiles(t *testing.T) ([]byte, planted) {
	useNetworkOfTheTest(t, &storeOfTheRoute{}, nil)
	project := writeConfig(t, plainProject)
	if _, _, code := say(t, "network", "proxy", "add", "home",
		"--type", "http", "--host", "192.0.2.10", "--port", "1082", "-config", project); code != exitOK {
		t.Fatalf("add a profile of the proxy: %d, want %d", code, exitOK)
	}

	document, _ := theAnswerOf(t, "network", "proxy", "list", "-config", project, "-json")

	return document, planted{note: "this document has no field of free words at all: the name of the profile, " +
		"its kind, its address, its port, two flags and a word of a closed list are all a name of the format, a " +
		"number or a flag. A value of a run cannot reach it, and there is nothing to cut"}
}

// theAnswerOfTheTestOfARoute is `crewflow proxy test <name> -json`: the route a request goes out
// by and what came of checking every ability of it. The abilities and their states are words of
// closed lists of §7d, and the details are the words of the checks themselves.
func theAnswerOfTheTestOfARoute(t *testing.T) ([]byte, planted) {
	const canary = "s3cr3t-of-the-run"
	useNetworkOfTheTest(t, &storeOfTheRoute{}, nil)
	project := writeConfig(t, plainProject)
	if _, _, code := say(t, "network", "proxy", "add", "home",
		"--type", "http", "--host", "192.0.2.10", "--port", "1082", "-config", project); code != exitOK {
		t.Fatalf("add a profile of the proxy: %d, want %d", code, exitOK)
	}

	document, _ := theAnswerOf(t, "network", "proxy", "test", "home", "-config", project, "-json")

	return document, planted{canary: canary, note: "the checks of the route in a machine without a network answer " +
		"\"unknown\" and say so in their details: no value of a run stands in them, and the canary of the case " +
		"is taught to the boundary. The cutting of a text field is covered by TestTheAnswerOfTheAdmission"}
}

// theAnswerOfTheJournal is `crewflow changelog check -json`: the problems of the journal of the
// project, one by one — where it is, what is wrong with it and the words of the host about it.
func theAnswerOfTheJournal(t *testing.T) ([]byte, planted) {
	repo := repositoryIn(t)
	theHostIs(t, theHost(124))
	repo.fragment(128, "строка 128")
	repo.commit("feat: 128")

	document, _ := theAnswerOf(t, changelogOf(repo, "changelog", "check", "-json")...)

	return document, planted{note: "the check of the journal reads files and asks the hosting: the boundary of " +
		"this command knows no value of a run, so there is nothing to cut here. Words of a run, when they are " +
		"in a document, are cut by the same boundary — see TestTheAnswerOfTheRun"}
}

// theAnswerOfTheKeyOfAnApp is `crewflow auth app check -json`: whether the key of the App is in the
// store of the machine and where the App is installed.
func theAnswerOfTheKeyOfAnApp(t *testing.T) ([]byte, planted) {
	store := &storeOfTheTest{key: keyOfTheTest(t)}
	api := useMachineOfTheTest(t, store, nil)
	project := writeConfig(t, projectConfig+botConfig+"\n[forge]\nkind = \"github\"\nhost = \""+hostOfTest(api)+"\"\n")

	document, _ := theAnswerOf(t, "auth", "app", "check", "-config", project, "-json")

	return document, planted{note: "the answer says whether the key of the App is in the store of the machine " +
		"and where the App is installed: no value of a run stands in it, and the boundary of this command knows " +
		"no value at all — the key of the App never belongs in an answer (docs/DESIGN.md §7e). The cutting of a " +
		"text field is covered by TestTheAnswerOfTheCheckOfATask"}
}

// theAnswerOfTheStateOfTheTask is the state of a task after a run really went: the file is written
// by a run and not printed by a command, and the whole route of this document is the run plus the
// file it left (R224-015, R224-016, docs/DESIGN.md §7h).
func theAnswerOfTheStateOfTheTask(t *testing.T) ([]byte, planted) {
	const canary = "s3cr3t-of-the-run"
	host := &host{task: taskOf(43)}
	host.task.Title = "the run of a task " + canary
	host.use(t)
	theRunKnows(t, canary, &taskRoles)
	// The run of this case goes out and comes back: what it leaves is the file of the state,
	// and the code of the command is what the run did about the change, not whether the file
	// is there (D-082, docs/DESIGN.md §6a).
	_ = theCodeOf(t, "task", "run", "43", "-config", host.config(t))
	path := taskrun.JournalsOf(host.home, "naghuale-crewflow").StatePath(43)
	said, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the state of the task: %v", err)
	}
	var document any
	if err := json.Unmarshal(said, &document); err != nil {
		t.Fatalf("the state of the task is not a document: %v\n%s", err, said)
	}
	published, _, err := secret.NewOut(secret.Chosen(canary)...).
		StateDocument(document)
	if err != nil {
		t.Fatalf("the state of the task through the boundary: %v", err)
	}
	return published, planted{
		canary: canary,
		cut:    []string{"title"},
		whole:  map[string]string{"task": "43", "profile": "opencode"},
	}
}

// theCodeOf is the code a command answered with, and its answer, for a case that needs both.
func theCodeOf(t *testing.T, args ...string) int {
	t.Helper()
	var stdout, stderr bytes.Buffer
	return run(args, &stdout, &stderr)
}

// theFieldOfTheDocument is the value at a path of a document and whether the path is there at
// all: a path of a list stands for every element of it, and a test reads the first (D-082, §7e).
func theFieldOfTheDocument(node any, path string) (string, bool) {
	said := theFieldsAt(node, strings.Split(path, "."))
	if len(said) == 0 {
		return "", false
	}
	return said[0], true
}

// theFieldsAt are the values at a path of a document: a segment `name` is the field of that name,
// a segment `name[]` is every element of the list at that name, and a segment `[]` is every
// element of the list the document is.
func theFieldsAt(node any, segments []string) []string {
	if len(segments) == 0 {
		return theLeafOf(node)
	}
	head, rest := segments[0], segments[1:]
	name, _, every := strings.Cut(head, "[]")
	switch held := node.(type) {
	case map[string]any:
		value, there := held[name]
		if !there {
			return nil
		}
		if !every {
			return theFieldsAt(value, rest)
		}
		return theElementsAt(value, rest)
	case []any:
		if name != "" {
			return nil
		}
		return theElementsAt(held, rest)
	}
	return nil
}

// theElementsAt is what a list says under the rest of a path, one element at a time.
func theElementsAt(node any, rest []string) []string {
	list, is := node.([]any)
	if !is {
		return nil
	}
	var values []string
	for _, item := range list {
		values = append(values, theFieldsAt(item, rest)...)
	}
	return values
}

// theLeafOf is a leaf of a document as a test reads it: a string as it is, a number as it is
// written, a flag as `true` or `false` (D-082, §7e).
func theLeafOf(node any) []string {
	switch leaf := node.(type) {
	case string:
		return []string{leaf}
	case bool:
		return []string{strconv.FormatBool(leaf)}
	case float64:
		return []string{strconv.FormatFloat(leaf, 'f', -1, 64)}
	}
	return nil
}

// pathsOfADocument are the paths of the fields of a document, the way the policy of the
// document names them: the name of every field, the elements of a list under `[]`, and the
// elements of a map the schema declares as dynamic under `.*` — a field of a map is named by the
// row of the map when the map has one, and by its own name when it does not, exactly as the
// boundary reads it (D-082, docs/DESIGN.md §7e).
func pathsOfADocument(doc secret.Document, node any) []string {
	var paths []string
	walkDocument(doc, node, "", &paths)
	return paths
}

// walkDocument is the tree of a document under the path it stands at.
func walkDocument(doc secret.Document, node any, path string, paths *[]string) {
	switch held := node.(type) {
	case map[string]any:
		if path != "" {
			*paths = append(*paths, path)
		}
		for name, value := range held {
			under := ofPath(path, name)
			if _, _, known := secret.ClassOf(doc, under); !known {
				under = ofPath(path, "*")
			}
			walkDocument(doc, value, under, paths)
		}
	case []any:
		if path != "" {
			*paths = append(*paths, path)
		}
		for _, item := range held {
			walkDocument(doc, item, path+"[]", paths)
		}
	}
}
