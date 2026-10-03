package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/secret"
)

// theAnswerOfEveryCommandIsTheDocumentItsPolicyNames: every kind of document the boundary
// publishes is published by a command that runs for real here — with its own file of a project,
// its own host and, where the command needs one, its own store of secrets. The answer of each of
// them is read as the document it claims to be, and three things are said about it: it is a
// document a program can read, every field of it is named by the policy of its own kind, and it
// holds no marker of a cut — nothing of the run stood in these answers, and the words and the
// names of the format are kept whole where they stand (D-082, R224-015, docs/DESIGN.md §7e).
func TestTheAnswerOfEveryCommandIsTheDocumentItsPolicyNames(t *testing.T) {
	for _, tc := range theCommandsThatPublishADocument() {
		t.Run(string(tc.doc), func(t *testing.T) {
			// The code of the command is not the check here: a doctor that found something
			// wrong with the machine and a journal with a problem in it answer with the
			// whole document and a code that is not zero. What is refused is a command that
			// published no document at all — the answer is then the refusal, not a document.
			document, _ := tc.run(t)
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
			if bytes.Contains(document, []byte(secret.Redacted)) {
				t.Errorf("the answer of the command is\n%s\nwant no marker of a cut in it: nothing of the run "+
					"stood in the answer, and the words of the format are not cut", document)
			}
		})
	}
}

// aCommandThatPublishes is a command of the project and the kind of the document it prints.
type aCommandThatPublishes struct {
	doc secret.Document
	run func(t *testing.T) ([]byte, int)
}

// theCommandsThatPublishADocument is every kind of the registry of the boundary and the command
// that publishes it, read as it is read by a person: `crewflow task list -json` and the rest of
// the queue of attention, the run and the continuation of it, the admission of a pair, the review
// and the merge and the check after it, the machine of the person and the route of the project,
// the journal of the project, the key of the App and the version of the build (D-082, §6a, §7e).
func theCommandsThatPublishADocument() []aCommandThatPublishes {
	return []aCommandThatPublishes{
		{doc: secret.DocumentTaskList, run: theAnswerOfTheList},
		{doc: secret.DocumentTaskAttention, run: theAnswerOfTheAttention},
		{doc: secret.DocumentTaskAttentionAll, run: theAnswerOfTheAttentionOfEveryProject},
		{doc: secret.DocumentTaskCheck, run: theAnswerOfTheCheckOfATask},
		{doc: secret.DocumentTaskRun, run: theAnswerOfTheRun},
		{doc: secret.DocumentTaskAdmit, run: theAnswerOfTheAdmission},
		{doc: secret.DocumentTaskCheckStalled, run: theAnswerOfTheStandings},
		{doc: secret.DocumentReview, run: theAnswerOfTheReview},
		{doc: secret.DocumentMerge, run: theAnswerOfTheMerge},
		{doc: secret.DocumentVerify, run: theAnswerOfTheVerification},
		{doc: secret.DocumentDoctor, run: theAnswerOfTheDoctor},
		{doc: secret.DocumentDoctorNetwork, run: theAnswerOfTheChecksOfTheRoute},
		{doc: secret.DocumentNetworkProxyList, run: theAnswerOfTheProfiles},
		{doc: secret.DocumentChangelogCheck, run: theAnswerOfTheJournal},
		{doc: secret.DocumentAuthAppCheck, run: theAnswerOfTheKeyOfAnApp},
	}
}

// theAnswerOf runs a command of the project as a person runs it and gives back its answer.
func theAnswerOf(t *testing.T, args ...string) ([]byte, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	if code != exitOK {
		t.Logf("the answer of %q: %s", strings.Join(args, " "), stderr.String())
	}
	return stdout.Bytes(), code
}

// theAnswerOfTheList is `crewflow task list -json` over a project with one run of one task.
func theAnswerOfTheList(t *testing.T) ([]byte, int) {
	host := &host{task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	said, code := theAnswerOf(t, "task", "list", "-config", project, "-json")
	if code != exitOK {
		return said, code
	}
	return theAnswerOf(t, "task", "list", "-all", "-config", project, "-json")
}

// theAnswerOfTheAttention is `crewflow task attention -json`.
func theAnswerOfTheAttention(t *testing.T) ([]byte, int) {
	host := &host{task: taskOf(43)}
	host.use(t)
	return theAnswerOf(t, "task", "attention", "-config", host.config(t), "-json")
}

// theAnswerOfTheAttentionOfEveryProject is `crewflow task attention -json -all`.
func theAnswerOfTheAttentionOfEveryProject(t *testing.T) ([]byte, int) {
	host := &host{task: taskOf(43)}
	host.use(t)
	return theAnswerOf(t, "task", "attention", "-all", "-config", host.config(t), "-json")
}

// theAnswerOfTheCheckOfATask is `crewflow task check <N> -json`.
func theAnswerOfTheCheckOfATask(t *testing.T) ([]byte, int) {
	host := &host{task: taskOf(43)}
	host.use(t)
	return theAnswerOf(t, "task", "check", "43", "-config", host.config(t), "-json")
}

// theAnswerOfTheRun is `crewflow task run <N> -json`: a run whose host refuses the route, so
// the answer is a whole one with the names of the format in it.
func theAnswerOfTheRun(t *testing.T) ([]byte, int) {
	host := &host{task: taskOf(43)}
	host.use(t)
	return theAnswerOf(t, "task", "run", "43", "-config", host.config(t), "-json")
}

// theAnswerOfTheAdmission is `crewflow task admit <N> <N> -json`: the record of a pair and the
// file it is written in.
func theAnswerOfTheAdmission(t *testing.T) ([]byte, int) {
	host := &host{task: taskWithGlobs(43, "docs/**"), tasks: map[int]forge.Task{41: taskWithGlobs(41, "internal/run/**")}}
	host.use(t)
	return theAnswerOf(t, "task", "admit", "41", "43",
		"-c", "K2=pass", "-c", "K3=pass", "-c", "K4=pass", "-c", "K5=pass", "-c", "K6=pass",
		"-c", "K7=pass", "-c", "K8=pass", "-config", host.config(t), "-json")
}

// theAnswerOfTheStandings is `crewflow task check-stalled -json`.
func theAnswerOfTheStandings(t *testing.T) ([]byte, int) {
	host := &host{task: taskOf(43)}
	host.use(t)
	return theAnswerOf(t, "task", "check-stalled", "-config", host.config(t), "-json")
}

// theAnswerOfTheReview is `crewflow review <PR> -json`: the facts of the change and the answer
// of the gate about it.
func theAnswerOfTheReview(t *testing.T) ([]byte, int) {
	host := newMergeHost(t)
	host.remote = reviewHead
	return theAnswerOf(t, "review", "7", "-config", writeConfig(t, mergeConfig), "-json")
}

// theAnswerOfTheMerge is `crewflow merge <PR> -json`: what came of the merge.
func theAnswerOfTheMerge(t *testing.T) ([]byte, int) {
	host := newMergeHost(t)
	host.remote = reviewHead
	return theAnswerOf(t, "merge", "7", "-config", writeConfig(t, mergeConfig), "-json")
}

// theAnswerOfTheVerification is `crewflow verify <PR> -json`: what is true about the change
// after the merge.
func theAnswerOfTheVerification(t *testing.T) ([]byte, int) {
	host := newMergeHost(t)
	host.remote = reviewHead
	return theAnswerOf(t, "verify", "7", "-config", writeConfig(t, mergeConfig), "-json")
}

// theAnswerOfTheDoctor is `crewflow doctor -json`: the report of the machine of the person.
func theAnswerOfTheDoctor(t *testing.T) ([]byte, int) {
	return theAnswerOf(t, "doctor", "-config", writeProject(t), "-json")
}

// theAnswerOfTheChecksOfTheRoute is `crewflow doctor network -json`: the checks of the route of
// the project.
func theAnswerOfTheChecksOfTheRoute(t *testing.T) ([]byte, int) {
	useNetworkOfTheTest(t, &storeOfTheRoute{}, nil)
	return theAnswerOf(t, "doctor", "network", "-config", writeConfig(t, plainProject), "-json")
}

// theAnswerOfTheProfiles is `crewflow network proxy list -json`: the profiles the project
// declares, with no credential of one in it.
func theAnswerOfTheProfiles(t *testing.T) ([]byte, int) {
	useNetworkOfTheTest(t, &storeOfTheRoute{}, nil)
	project := writeConfig(t, plainProject)
	if _, _, code := say(t, "network", "proxy", "add", "home",
		"--type", "http", "--host", "192.0.2.10", "--port", "1082", "-config", project); code != exitOK {
		t.Fatalf("add a profile of the proxy: %d, want %d", code, exitOK)
	}
	return theAnswerOf(t, "network", "proxy", "list", "-config", project, "-json")
}

// theAnswerOfTheJournal is `crewflow changelog check -json`: the problems of the journal of the
// project, where the host holds no task of the fragment.
func theAnswerOfTheJournal(t *testing.T) ([]byte, int) {
	repo := repositoryIn(t)
	theHostIs(t, theHost(124))
	repo.fragment(128, "строка 128")
	repo.commit("feat: 128")
	args := changelogOf(repo, "changelog", "check", "-json")
	return theAnswerOf(t, args...)
}

// theAnswerOfTheKeyOfAnApp is `crewflow auth app check -json`.
func theAnswerOfTheKeyOfAnApp(t *testing.T) ([]byte, int) {
	store := &storeOfTheTest{key: keyOfTheTest(t)}
	api := useMachineOfTheTest(t, store, nil)
	project := writeConfig(t, projectConfig+botConfig+"\n[forge]\nkind = \"github\"\nhost = \""+hostOfTest(api)+"\"\n")
	return theAnswerOf(t, "auth", "app", "check", "-config", project, "-json")
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
