package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/gate"
	"github.com/naghuale/crewflow/internal/merge"
)

// orchestratorConfig is the file of a project whose orchestrator works as an account of
// the host of its own: the mode, the numbers of the second app, and nothing else — the
// reviewers of the project are left out on purpose, because where the orchestrator has
// an account of its own that account is the one whose record of a review counts, and the
// owner is not among them without the file of the project saying so (§7i).
const orchestratorConfig = `
[orchestrator]
mode = "separate"

[orchestrator.github_app]
app_id = 5107053
installation_id = 12346
`

// reviewApartConfig is the file of a project of a review whose orchestrator works as an
// account of the host of its own and which names no reviewers: the account of the
// orchestrator is then the one whose record of a review counts, and the owner is not
// among them without the file of the project saying so (§5, §7i).
const reviewApartConfig = `
[project]
repo = "naghuale/crewflow"
language = "en"

[executor]
command = ["opencode", "run", "--dir", "{worktree}", "--format", "json", "{prompt}"]
timeout = "1h"
` + orchestratorConfig

// reviewSharedConfig is the file of a project of the shared mode: the orchestrator works
// under the login of the owner, which is what a project gets without saying anything
// about the second app, and what `crewflow doctor` calls a debt of trust (§7i, §7k).
const reviewSharedConfig = `
[project]
repo = "naghuale/crewflow"
language = "en"

[executor]
command = ["opencode", "run", "--dir", "{worktree}", "--format", "json", "{prompt}"]
timeout = "1h"
`

// mergeApartConfig is the same for a merge, which is judged by the same gate and reads
// the same two lists of accounts out of the file of the project (§7h, §7i).
const mergeApartConfig = `
[project]
repo = "naghuale/crewflow"
language = "en"

[executor]
command = ["opencode", "run", "--dir", "{worktree}", "--format", "json", "{prompt}"]
timeout = "1h"

[ci]
required = true
timeout = "5m"
` + orchestratorConfig

// useSeparate makes the host of a test a project of the separate mode: the orchestrator
// of it is the account of an app of the host, a record of a review is written in the name
// of that account, and what the host already holds under the change was written by it
// as well (docs/DESIGN.md §7i).
func (h *reviewHost) useSeparate(t *testing.T) {
	if t != nil {
		t.Helper()
	}
	h.orchestrator = "crewflow-orchestrator[bot]"
	h.orchestratorSubject = forge.Subject{
		Kind: forge.KindApp, ID: 5107053, Login: "crewflow-orchestrator[bot]",
	}
	for i := range h.comments {
		h.comments[i].Author = h.orchestratorSubject
	}
}

// useSeparate is the same for a merge, whose change is approved by the account of the
// orchestrator in a project of the separate mode (docs/DESIGN.md §7h, §7i).
func (h *mergeHost) useSeparate() {
	h.reviewHost.useSeparate(nil)
}

// OrchestratorIdentity is the mode of the orchestrator of a project of a test, with the
// rights of that mode: a host of a test stands for the account the adapter of a project
// would speak as, and the mode is what the record of a review is written in and what the
// push of a merge is signed with (docs/DESIGN.md §7h, §7i).
func (h *reviewHost) OrchestratorIdentity(context.Context) (forge.Identity, error) {
	identity, err := h.DescribeOrchestrator(context.Background())
	if err != nil {
		return forge.Identity{}, err
	}
	identity.Env = []string{"GH_TOKEN=ghs_token_of_the_orchestrator"}
	return identity, nil
}

// DescribeOrchestrator is the line of that mode and the settings git takes the
// credentials of a push from, asked without a token of it: a report and a description
// mint no token of an hour (§7e, §7i).
func (h *reviewHost) DescribeOrchestrator(context.Context) (forge.Identity, error) {
	if h.orchestrator == "" {
		return forge.Identity{
			Mode:        forge.ModeShared,
			Description: "shared — the login gh " + h.signedIn + " (one login with the owner)",
		}, nil
	}
	return forge.Identity{
		Mode:        forge.ModeSeparate,
		Description: "separate — GitHub App crewflow-orchestrator (installation 12346)",
		GitConfig:   map[string]string{"credential.helper": "crewflow auth git-credential -as orchestrator"},
	}, nil
}

// TestRunReviewInTheSeparateModeWritesTheRecordAsTheAccountOfTheApp: a record of a
// review is an approval only because of the account it is written in, and in the mode of
// a separate login that account is the App of the project — the line says so before the
// record goes out, and the record is written in the name of that account and of nobody
// else (docs/DESIGN.md §7h, §7i).
func TestRunReviewInTheSeparateModeWritesTheRecordAsTheAccountOfTheApp(t *testing.T) {
	host := newReviewHost(t)
	host.useSeparate(t)
	project := writeConfig(t, reviewApartConfig)
	var stdout, stderr bytes.Buffer

	code := run([]string{"review", "7", "-approve", "-config", project}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow review -approve = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	if len(host.written) != 1 {
		t.Fatalf("the host was asked to write %d records, want the one of the approval", len(host.written))
	}
	if !strings.Contains(host.written[0].body, gate.ApproveOf(reviewHead, 7)) {
		t.Errorf("the record is %q, want the approval of the head of the change", host.written[0].body)
	}
	// The line of the mode goes out before the record and names the account, so that a
	// person reading a terminal knows what the record under the change was written as.
	for _, want := range []string{"separate — GitHub App crewflow-orchestrator", "crewflow-orchestrator[bot] approved"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("crewflow review wrote %q, want it to mention %q", stderr.String(), want)
		}
	}
	// The report of the same command names the account whose record counts and, apart
	// from the reviewers, the owner whose records are the decision of a person.
	for _, want := range []string{"crewflow-orchestrator[bot]", "owners: naghuale"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("crewflow review wrote:\n%s\nwant it to mention %q", stdout.String(), want)
		}
	}
}

// TestTheApprovalOfTheOrchestratorIsCountedAndTheAcceptanceOfItIsNot: the same change,
// approved by the App of the orchestrator, with a file outside the boundaries of the task
// that the orchestrator accepted. The gate counts the approval and refuses the change
// for the file, because a record of an acceptance is a person taking it into their own
// hands and the account of the orchestrator is not one (docs/DESIGN.md §7h, §7i).
func TestTheApprovalOfTheOrchestratorIsCountedAndTheAcceptanceOfItIsNot(t *testing.T) {
	host := newReviewHost(t)
	host.useSeparate(t)
	host.comments = nil
	host.files = []string{"internal/gate/gate.go", "docs/DESIGN.md"}
	host.comment(gate.ApproveOf(reviewHead, 7))
	host.comments[0].Author = host.orchestratorSubject
	host.comment(gate.AcceptOf(reviewHead, "the design of the gate is this task as well"))
	host.comments[1].Author = host.orchestratorSubject
	project := writeConfig(t, reviewApartConfig)
	var stdout, stderr bytes.Buffer

	code := run([]string{"review", "7", "-config", project}, &stdout, &stderr)

	if code != exitFailure {
		t.Fatalf("crewflow review = %d, want %d (stdout: %q)", code, exitFailure, stdout.String())
	}
	for _, want := range []string{string(gate.OutOfScope), "SCOPE: ACCEPTED", "naghuale"} {
		if !strings.Contains(stderr.String()+stdout.String(), want) {
			t.Errorf("crewflow review wrote:\n%s\n%s\nwant it to mention %q", stdout.String(), stderr.String(), want)
		}
	}
	if strings.Contains(stderr.String()+stdout.String(), "may be merged") {
		t.Errorf("crewflow review wrote:\n%s\nwant it to refuse the change", stdout.String())
	}
}

// TestTheAcceptanceOfTheOwnerLiftsTheRefusalOfTheFile: the same change and the same
// file, and the record of the acceptance written by the owner of the project. The gate
// has nothing more to say about it, and the report says whose record it was (§7h, §7i).
func TestTheAcceptanceOfTheOwnerLiftsTheRefusalOfTheFile(t *testing.T) {
	host := newReviewHost(t)
	host.useSeparate(t)
	host.comments = nil
	host.files = []string{"internal/gate/gate.go", "docs/DESIGN.md"}
	host.comment(gate.ApproveOf(reviewHead, 7))
	host.comments[0].Author = host.orchestratorSubject
	host.comment(gate.AcceptOf(reviewHead, "the design of the gate is this task as well"))
	project := writeConfig(t, reviewApartConfig)
	var stdout, stderr bytes.Buffer

	code := run([]string{"review", "7", "-config", project}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow review = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	if !strings.Contains(stdout.String(), "taken by naghuale") {
		t.Errorf("crewflow review wrote:\n%s\nwant it to say whose acceptance took the file", stdout.String())
	}
}

// TestTheApprovalOfTheOwnerCountsWhereTheProjectNamesHim: the file of the project may
// name the owner among the reviewers, and then his approval is one. The mode says which
// account the orchestrator works as; this list says whose word approves a change
// (§7h, §7i).
//
// It is the shared mode and not the separate one that can name the owner among the
// reviewers: in the separate mode the login of the orchestrator is not the login of the
// owner, and a file that says the opposite is refused before a review is written — see
// TestOR005 (docs/DESIGN.md §7i, §7k).
func TestTheApprovalOfTheOwnerCountsWhereTheProjectNamesHim(t *testing.T) {
	host := newReviewHost(t)
	host.comments = nil
	host.comment(gate.ApproveOf(reviewHead, 7))
	project := writeConfig(t, reviewSharedConfig+"\n[merge]\nreviewers = [\"naghuale\"]\n")
	var stdout, stderr bytes.Buffer

	code := run([]string{"review", "7", "-config", project}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow review = %d, want %d (stdout: %q)\n%s", code, exitOK, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "approved") {
		t.Errorf("crewflow review wrote:\n%s\nwant the approval of the owner to count", stdout.String())
	}
}

// TestTheApprovalOfTheOwnerIsACommentWhereTheProjectNamesOnlyTheApp: the same
// approval, in a project whose reviewers are the account of the orchestrator alone. The
// gate names the author of the record and says the record does not count — an approval
// nobody counts is a comment, whatever it says (§7h, §7i).
func TestTheApprovalOfTheOwnerIsACommentWhereTheProjectNamesOnlyTheApp(t *testing.T) {
	host := newReviewHost(t)
	host.useSeparate(t)
	host.comments = nil
	host.comment(gate.ApproveOf(reviewHead, 7))
	project := writeConfig(t, reviewApartConfig+"\n[merge]\nreviewers = [\"crewflow-orchestrator[bot]\"]\n")
	var stdout, stderr bytes.Buffer

	code := run([]string{"review", "7", "-config", project}, &stdout, &stderr)

	if code != exitFailure {
		t.Fatalf("crewflow review = %d, want %d", code, exitFailure)
	}
	for _, want := range []string{string(gate.ApprovalUntrusted), "naghuale", "crewflow-orchestrator[bot]"} {
		if !strings.Contains(stderr.String()+stdout.String(), want) {
			t.Errorf("crewflow review wrote:\n%s\n%s\nwant it to mention %q", stdout.String(), stderr.String(), want)
		}
	}
}

// TestThePushOfAMergeIsMadeAsTheAccountOfTheApp: the push of a merge is the one command
// of git that moves the default branch of the project, and in the mode of a separate
// login it is made as the account of the App of the orchestrator. The settings that say
// so go into the environment of the command — the helper of the credentials of git signs
// a token of an hour for that one push — and the checkout of a person is not written to
// (docs/DESIGN.md §7a, §7h, §7i).
func TestThePushOfAMergeIsMadeAsTheAccountOfTheApp(t *testing.T) {
	host := newMergeHost(t)
	host.useSeparate()
	project := writeConfig(t, mergeApartConfig)
	var stdout, stderr bytes.Buffer

	code := run([]string{"merge", "7", "-config", project}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow merge = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	if !host.ran("git push origin " + reviewHead + ":refs/heads/main") {
		t.Errorf("crewflow merge ran:\n%s\nwant the push of the approved commit", strings.Join(host.commands, "\n"))
	}
	if !host.pushedWithTheHelperOfTheOrchestrator() {
		t.Errorf("the commands of git were started with %v, want the helper of the credentials of the orchestrator",
			host.environment)
	}
	// The report and the journal of the merge say whose name it was pushed as: a merge
	// pushed as the owner and a merge pushed as the orchestrator are two different
	// things to a person who reads them afterwards (docs/DESIGN.md §7h, §7i).
	if !strings.Contains(stdout.String(), "separate — GitHub App crewflow-orchestrator") {
		t.Errorf("crewflow merge wrote:\n%s\nwant the line of the mode of the orchestrator", stdout.String())
	}
	journal := read(t, filepath.Join(testHome(), ".crewflow", "runs", "naghuale-crewflow", "7-merge.jsonl"))
	if !strings.Contains(journal, `"step":"orchestrator"`) || !strings.Contains(journal, "crewflow-orchestrator") {
		t.Errorf("the journal of the merge holds no line of the orchestrator:\n%s", journal)
	}
}

// TestAMergeInTheSeparateModeSaysItsModeAsJSON: the shape an orchestrator reads, where
// the mode of §7i is a field of its own and not a line a script has to parse out of a
// report (docs/DESIGN.md §7i).
func TestAMergeInTheSeparateModeSaysItsModeAsJSON(t *testing.T) {
	host := newMergeHost(t)
	host.useSeparate()
	project := writeConfig(t, mergeApartConfig)
	var stdout, stderr bytes.Buffer

	code := run([]string{"merge", "7", "-config", project, "-json"}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow merge -json = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	var answer struct {
		Orchestrator merge.Orchestrator `json:"orchestrator"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &answer); err != nil {
		t.Fatalf("the answer of crewflow merge is not the JSON of an outcome: %v\n%s", err, stdout.String())
	}
	if answer.Orchestrator.Mode != forge.ModeSeparate ||
		!strings.Contains(answer.Orchestrator.Description, "crewflow-orchestrator") {
		t.Errorf("the answer holds the orchestrator %+v, want the account of the app of the orchestrator", answer.Orchestrator)
	}
}

// TestTheSharedModeSaysWhatItCosts: in the mode of a shared login a record of a review is
// written as the person who runs crewflow, the line says so, and the command still
// works: a project that has not set the second App up is a project crewflow has to work
// for (docs/DESIGN.md §7i).
func TestTheSharedModeSaysWhatItCosts(t *testing.T) {
	newReviewHost(t)
	project := writeConfig(t, reviewConfig)
	var stdout, stderr bytes.Buffer

	code := run([]string{"review", "7", "-approve", "-config", project}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow review -approve = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	if !strings.Contains(stderr.String(), "shared — the login gh naghuale (one login with the owner)") {
		t.Errorf("crewflow review wrote %q, want the line of the mode of the orchestrator", stderr.String())
	}
}

// TestGitIsStartedWithTheSettingsOfTheAccountAndOfNothingElse: the environment of the
// commands of git is what the adapter of the host named, in the words git reads them
// (`GIT_CONFIG_COUNT` and a pair for every value), and every setting of a list is reset
// before crewflow's own: the machine may have a helper that answers with the token of the
// login of a person, and a push signed with it would be that login all over again (§7a, §7i,
// F-116).
func TestGitIsStartedWithTheSettingsOfTheAccountAndOfNothingElse(t *testing.T) {
	environment := gitConfig(map[string]string{
		"credential.helper":      "crewflow auth git-credential -as orchestrator",
		"credential.useHttpPath": "true",
		"core.editor":            "true",
	})

	want := []string{
		"GIT_CONFIG_COUNT=5",
		"GIT_CONFIG_KEY_0=core.editor", "GIT_CONFIG_VALUE_0=true",
		"GIT_CONFIG_KEY_1=credential.helper", "GIT_CONFIG_VALUE_1=",
		"GIT_CONFIG_KEY_2=credential.helper",
		"GIT_CONFIG_VALUE_2=crewflow auth git-credential -as orchestrator",
		"GIT_CONFIG_KEY_3=credential.useHttpPath", "GIT_CONFIG_VALUE_3=",
		"GIT_CONFIG_KEY_4=credential.useHttpPath", "GIT_CONFIG_VALUE_4=true",
	}
	if strings.Join(environment, " ") != strings.Join(want, " ") {
		t.Errorf("the environment of the commands of git is %v, want %v", environment, want)
	}
	if got := gitConfig(nil); got != nil {
		t.Errorf("the environment of the commands of git is %v, want nothing for a project with no settings", got)
	}
}

// TestTheGitOfTheMachineIsTheOneOfTheCommands: the runner a command builds is the git of
// the machine, started with the environment of the project and nothing else — a test of
// a command replaces the way a program is started and sees what it was started with
// (docs/DESIGN.md §7a, §7i).
func TestTheGitOfTheMachineIsTheOneOfTheCommands(t *testing.T) {
	var started [][]string
	old := startProgram
	t.Cleanup(func() { startProgram = old })
	startProgram = func(_ context.Context, name string, args []string, dir string, environment []string) ([]byte, []byte, int, error) {
		started = append(started, environment)
		return []byte(name + " " + strings.Join(args, " ") + " in " + dir), nil, 0, nil
	}
	runner := gitIn([]string{"GH_TOKEN=ghs_token_of_the_orchestrator"})

	out, _, code, err := runner(t.Context(), "git", []string{"push", "origin", "x:refs/heads/main"}, "/w")

	if err != nil || code != 0 {
		t.Fatalf("the runner of git = (%q, %d, %v), want the command to be started", out, code, err)
	}
	if len(started) != 1 || started[0][0] != "GH_TOKEN=ghs_token_of_the_orchestrator" {
		t.Errorf("the command was started with %v, want the environment of the project", started)
	}
}

// TestTheReviewersOfTheProjectAreTheAccountOfTheOrchestratorWhereItSaysNothing: in the
// mode of a separate login and with no reviewers in the file, the account the
// orchestrator works as is the one whose record of a review counts — and a host that
// cannot name that account is a refusal, because a gate counting the approvals of the
// owner would count nothing and would say so as though nobody had approved anything
// (docs.DESIGN.md §5, §7h, §7i).
//
// Every list of the file is a list of logins and every answer of these two functions is a
// list of subjects: the gate counts records by the number the host keeps each account
// under, so the login of the file is asked of the host once and the answer is an account.
func TestTheReviewersOfTheProjectAreTheAccountOfTheOrchestratorWhereItSaysNothing(t *testing.T) {
	orchestratorOfTest := forge.Subject{Kind: forge.KindApp, ID: 5107053, Login: "crewflow-orchestrator[bot]"}
	cfg := config.Config{
		Project:      config.Project{Repo: "naghuale/crewflow"},
		Orchestrator: config.Orchestrator{Mode: config.ModeSeparate},
	}
	host := &reviewHost{
		orchestrator:        "crewflow-orchestrator[bot]",
		orchestratorSubject: orchestratorOfTest,
		signedIn:            "crewflow-orchestrator[bot]",
		subjects:            map[string]forge.Subject{ownerOfTheProject.Login: ownerOfTheProject},
	}
	set := forge.Set{Forge: host}

	reviewers, err := reviewersOf(t.Context(), cfg, set)

	if err != nil {
		t.Fatalf("reviewersOf returned an error: %v", err)
	}
	if len(reviewers) != 1 || !reviewers[0].Same(orchestratorOfTest) {
		t.Errorf("the reviewers are %v, want the account of the orchestrator", reviewers)
	}
	// The file of the project says otherwise, and what it says stands: the gate counts
	// the records of the reviewers, whoever they are (§5). A login of the separate mode
	// has to be an account of its own, though: naming the owner among the reviewers of a
	// separate project is refused before a review is written (OR-005).
	cfg.Merge.Reviewers = []string{"maintainer"}
	host.subjects["maintainer"] = forge.Subject{Kind: forge.KindUser, ID: 4242, Login: "maintainer"}
	reviewers, err = reviewersOf(t.Context(), cfg, set)
	if err != nil || len(reviewers) != 1 || !reviewers[0].Same(host.subjects["maintainer"]) {
		t.Errorf("the reviewers are %v (%v), want the ones the file of the project names", reviewers, err)
	}
	cfg.Merge.Reviewers = []string{"naghuale"}
	if _, err := reviewersOf(t.Context(), cfg, set); err == nil {
		t.Error("reviewersOf with the owner among the reviewers of a separate project returned no error, want the refusal of OR-005")
	}
	// The shared mode asks the host for the owner of the repository, which is the
	// reviewer until the file says otherwise — the default of §5 is an account like any
	// other, and the host is the only one who knows the number under that name (§5, §7i).
	cfg.Orchestrator.Mode, cfg.Merge.Reviewers = config.ModeShared, nil
	reviewers, err = reviewersOf(t.Context(), cfg, set)
	if err != nil || len(reviewers) != 1 || !reviewers[0].Same(ownerOfTheProject) {
		t.Errorf("the reviewers are %v (%v), want the owner of the repository under its number", reviewers, err)
	}
	// A host that names no account cannot be the reviewer of a project, and saying so is
	// better than counting the records of somebody else.
	silent := &reviewHost{subjects: map[string]forge.Subject{}}
	if _, err := reviewersOf(t.Context(), cfg, set); err != nil {
		t.Errorf("reviewersOf in the shared mode = %v, want the default of §5", err)
	}
	cfg.Orchestrator.Mode = config.ModeSeparate
	if _, err := reviewersOf(t.Context(), cfg, forge.Set{Forge: silent}); err == nil {
		t.Error("reviewersOf with a host that names no account returned no error, want one")
	}
}

// TestTheGitOfACommandCarriesTheSettingsOfTheAccount: the settings the adapter named
// reach the commands of git, and a host that cannot name the mode of the orchestrator
// leaves the commands of git as they are: an empty environment is the git of the person
// who runs crewflow, which is what a project in the shared mode is (§7a, §7i).
func TestTheGitOfACommandCarriesTheSettingsOfTheAccount(t *testing.T) {
	apart := &reviewHost{orchestrator: "crewflow-orchestrator[bot]", signedIn: "crewflow-orchestrator[bot]"}
	cfg := config.Config{Network: config.Network{Mode: "direct"}}
	environment, err := gitEnvironment(t.Context(), cfg, forge.Set{Forge: apart}, io.Discard)
	if err != nil {
		t.Fatalf("the environment of the commands of git: %v", err)
	}

	if !strings.Contains(strings.Join(environment, " "), "crewflow auth git-credential -as orchestrator") {
		t.Errorf("the environment of the commands of git is %v, want the helper of the orchestrator in it", environment)
	}
	shared := &reviewHost{signedIn: "naghuale"}
	got, err := gitEnvironment(t.Context(), cfg, forge.Set{Forge: shared}, io.Discard)
	if err != nil || got != nil {
		t.Errorf("the environment of the commands of git is %v (%v), want nothing for the shared mode", got, err)
	}
	got, err = gitEnvironment(t.Context(), cfg, forge.Set{}, io.Discard)
	if err != nil || got != nil {
		t.Errorf("the environment of the commands of git is %v (%v), want nothing for a project without a host", got, err)
	}
}

// TestCFNET002TheGitOfAReviewAndAMergeGoesThroughTheRoute: a fetch of the objects of the
// head of a change and a push of a merge are network like any other, and a project whose
// owner named a proxy expects both to go through it — in the environment of that one
// command, and in no setting of the machine or of git (docs/DESIGN.md §7d, §7h).
func TestCFNET002TheGitOfAReviewAndAMergeGoesThroughTheRoute(t *testing.T) {
	apart := &reviewHost{orchestrator: "crewflow-orchestrator[bot]", signedIn: "crewflow-orchestrator[bot]"}
	cfg := config.Config{Network: config.Network{
		Mode:        "proxy",
		ActiveProxy: "home",
		Proxies: map[string]config.Proxy{
			"home": {Type: "http", Host: "192.0.2.10", Port: 1082, Credentials: "none"},
		},
	}}

	environment, err := gitEnvironment(t.Context(), cfg, forge.Set{Forge: apart}, io.Discard)

	if err != nil {
		t.Fatalf("the environment of the commands of git: %v", err)
	}
	joined := strings.Join(environment, " ")
	for _, want := range []string{"HTTP_PROXY=http://192.0.2.10:1082", "git-credential -as orchestrator"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the environment of the commands of git is %v, want %q in it", environment, want)
		}
	}
}

// pushedWithTheHelperOfTheOrchestrator is whether the commands of git of a merge were
// started with the settings that point git at the helper of the credentials of the
// account of the orchestrator (docs/DESIGN.md §7i).
func (h *mergeHost) pushedWithTheHelperOfTheOrchestrator() bool {
	joined := strings.Join(h.environment, " ")
	return strings.Contains(joined, "GIT_CONFIG_VALUE_2=crewflow auth git-credential -as orchestrator") ||
		strings.Contains(joined, "GIT_CONFIG_VALUE_1=crewflow auth git-credential -as orchestrator")
}
