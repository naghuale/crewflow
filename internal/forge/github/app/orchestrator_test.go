package app

import (
	"net/http"
	"strings"
	"testing"
)

// rightsOfTheOrchestrator are the four rights of §7i with the issues in write: the
// orchestrator closes the task of a change and leaves a record under it, and a run of
// the executor does not (docs/DESIGN.md §7h, §7i).
func rightsOfTheOrchestrator() map[string]any {
	return map[string]any{
		"contents": "write", "pull_requests": "write", "issues": "write", "metadata": "read",
	}
}

// orchestratorOfTheDocumentation is a Source of the App of the orchestrator, on the
// same server and the same store of the documentation: the answers of the API of GitHub
// are the examples of its documentation with the one right of the orchestrator changed,
// and the asked of it is every path the API was asked at (docs/DESIGN.md §7i).
//
// The examples are the same files the tests of the executor read, and the only thing
// changed in them is `issues: read` → `issues: write` — a test that built an answer of
// its own would keep agreeing with the code that reads it, and a test that carried a
// second copy of the answer would carry a second value of a token into the history.
func orchestratorOfTheDocumentation(t *testing.T) (*Source, *asked) {
	t.Helper()
	asked := &asked{}
	source := sourceOfTest(t, func(w http.ResponseWriter, r *http.Request) {
		asked.of(t, r)
		answer, known := answerOfTheOrchestrator[r.URL.Path]
		w.Header().Set("Content-Type", "application/json")
		if !known {
			w.WriteHeader(http.StatusNotFound)
			answerOfTest(t, w, map[string]any{"message": "Not Found"})
			return
		}
		if answer == withTheRightsOfTheOrchestrator {
			// The example of the documentation, with the one right of the orchestrator
			// in it: every other field of the answer is the one the documentation writes
			// and the one this package reads.
			example := objectOfTheTest(t, exampleAnswer)
			example["permissions"] = rightsOfTheOrchestrator()
			answerOfTest(t, w, example)
			return
		}
		writeAnswerOfTheTest(t, w, answer)
	})
	source.Role = Orchestrator
	return source, asked
}

// withTheRightsOfTheOrchestrator is the mark an answer of the documentation carries when
// the one right of the orchestrator is changed in it, and exampleAnswer is the file of
// that example: the paths of the App itself are the same for both Apps of a project, and
// the token of the request is what tells them apart (§7i).
const withTheRightsOfTheOrchestrator = "§7i"

// exampleAnswer is the answer of the documentation to a request for a token of the
// installation, as the tests of the executor read it.
const exampleAnswer = "access-token.json"

// theTokenOfTheExample is the value the example of the documentation holds, read out of
// that file and written nowhere else: a test that wrote the value of a token in its own
// text would put a second copy of it into the history, and the check for secrets of the
// project (`.gitleaksignore`, README) keeps a fingerprint of every copy of it. What a
// test compares against is the answer it handed to the server, and nothing else.
func theTokenOfTheExample(t *testing.T) string {
	t.Helper()
	token, is := objectOfTheTest(t, exampleAnswer)["token"].(string)
	if !is || token == "" {
		t.Fatalf("the example of the documentation %s holds no token", exampleAnswer)
	}
	return token
}

// answerOfTheOrchestrator is the example of the documentation that answers every
// question crewflow asks the App of the orchestrator, by the path it is asked at.
var answerOfTheOrchestrator = map[string]string{
	appPath:                        "app.json",
	"/app/installations/165781718": withTheRightsOfTheOrchestrator,
	"/app/installations/165781718/access_tokens": withTheRightsOfTheOrchestrator,
	"/repos/naghuale/crewflow/installation":      withTheRightsOfTheOrchestrator,
	"/users/crewflow-executor[bot]":              "bot.json",
}

// TestTheTokenOfTheOrchestratorIsAskedForWithTheRightsOfTheOrchestrator: the token of
// the App of the orchestrator is asked for with the rights of the orchestrator and no
// others — the four of §7i with the issues in write, because a merge closes the task
// behind the change that went in — and the answer is read in the form the documentation
// of the endpoint writes it, exactly as the answer of a run is (docs/DESIGN.md §7h, §7i).
func TestTheTokenOfTheOrchestratorIsAskedForWithTheRightsOfTheOrchestrator(t *testing.T) {
	var asked asked
	source := sourceOfTest(t, func(w http.ResponseWriter, r *http.Request) {
		asked.of(t, r)
		example := objectOfTheTest(t, "access-token.json")
		example["permissions"] = rightsOfTheOrchestrator()
		answerOfTest(t, w, example)
	})
	source.Role = Orchestrator

	token, err := source.Token(t.Context())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}

	// The body of the request is compared with the example of the documentation of the
	// endpoint, and not with the fields the code happens to build: the second of the
	// two only says that the code agrees with itself.
	want := objectOfTheTest(t, "access-token-request.json")
	want["permissions"] = rightsOfTheOrchestrator()
	if got := printedJSON(t, asked.body); got != printedJSON(t, want) {
		t.Errorf("the body of the request is %s, want the example of the documentation %s",
			got, printedJSON(t, want))
	}
	if want := theTokenOfTheExample(t); token.Value != want {
		t.Errorf("the token is %q, want the token of the answer of the API", token.Value)
	}
	if want := "2016-07-11 22:14:10 +0000 UTC"; token.ExpiresAt.String() != want {
		t.Errorf("the token is good until %s, want %s: the life of it is what the API says",
			token.ExpiresAt, want)
	}
}

// TestTheRightsOfTheTwoAppsAreNotTheSame: the App of the executor reads the issues of
// a task and the App of the orchestrator writes them, and that is the only difference
// between the two sets of rights — a report of a machine that showed them as one set
// would have an owner approving a task through an App that cannot.
func TestTheRightsOfTheTwoAppsAreNotTheSame(t *testing.T) {
	run, orchestrator := RunRights(), OrchestratorRights()

	if run["issues"] != "read" || orchestrator["issues"] != "write" {
		t.Errorf("the rights of a run are %v and of the orchestrator %v, want the issues of the second in write", run, orchestrator)
	}
	for _, name := range []string{"contents", "pull_requests", "metadata"} {
		if run[name] != orchestrator[name] {
			t.Errorf("the right %q is %q for a run and %q for the orchestrator, want one level for both",
				name, run[name], orchestrator[name])
		}
	}
	if len(run) != len(orchestrator) {
		t.Errorf("a run is asked for %d rights and the orchestrator for %d, want the same four", len(run), len(orchestrator))
	}
}

// TestTheRightsOfTheInstallationAreReadAgainstTheRoleOfTheApp: a right the App of the
// orchestrator is asked for is not news to it, and one the App of the executor is asked
// for is wider than a run needs — the same answer of the API read against the wrong set
// of rights, which is what a report of a machine would call a power nobody meant to
// give (docs/DESIGN.md §7i).
func TestTheRightsOfTheInstallationAreReadAgainstTheRoleOfTheApp(t *testing.T) {
	installation := Installation{Permissions: map[string]string{
		"contents": "write", "pull_requests": "write", "issues": "write", "metadata": "read",
	}}

	if wider := installation.Beyond(OrchestratorRights()); len(wider) > 0 {
		t.Errorf("the rights of the app of the orchestrator are %v, want the four it is asked for", wider)
	}
	wider := installation.Beyond(RunRights())
	if len(wider) != 1 || wider[0] != "issues write" {
		t.Errorf("the rights of the app of the executor beyond a run are %v, want %q", wider, "issues write")
	}
}

// TestTheTokenOfTheOrchestratorIsRefusedWhenTheAPIMadeItWider: the token of the App
// of the orchestrator is the power of whoever holds it to close tasks, to write the
// records of a review and to push the default branch of the project, and an answer of
// the API that is wider than the request is refused before anybody uses it — with the
// name of the wider right in the refusal, so that a person can go and change it in the
// settings of GitHub (docs/DESIGN.md §7i).
func TestTheTokenOfTheOrchestratorIsRefusedWhenTheAPIMadeItWider(t *testing.T) {
	cases := []struct {
		name   string
		change func(answer map[string]any)
		want   string
	}{
		{
			name: "the workflows of the repository",
			change: func(answer map[string]any) {
				answer["permissions"].(map[string]any)["workflows"] = "write"
			},
			want: "workflows write",
		},
		{
			name: "an administration of the repository",
			change: func(answer map[string]any) {
				answer["permissions"].(map[string]any)["contents"] = "admin"
			},
			want: "contents admin",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			answer := answerOfTheToken(t)
			answer["permissions"] = rightsOfTheOrchestrator()
			tc.change(answer)
			source := sourceOfTest(t, func(w http.ResponseWriter, r *http.Request) {
				answerOfTest(t, w, answer)
			})
			source.Role = Orchestrator

			token, err := source.Token(t.Context())
			if err == nil {
				t.Fatalf("Token = %+v, want an answer wider than the orchestrator to be refused", token)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Token = %v, want it to name %q", err, tc.want)
			}
			if !strings.Contains(err.Error(), "orchestrator") {
				t.Errorf("Token = %v, want it to name whose token it is", err)
			}
		})
	}
}

// TestTheOrchestratorIsGivenItsTokenAndTheHelperOfGit: the orchestrator of a project
// is handed the token gh speaks with and the helper git takes a fresh token from for
// the push of a merge, and the helper is told which App to sign for — a project whose
// two subjects have an App each has two of them, and a push of a merge that was signed
// as the executor would be the login of the run all over again (§7h, §7i).
func TestTheOrchestratorIsGivenItsTokenAndTheHelperOfGit(t *testing.T) {
	source, _ := orchestratorOfTheDocumentation(t)

	identity, err := source.OrchestratorIdentity(t.Context())
	if err != nil {
		t.Fatalf("OrchestratorIdentity: %v", err)
	}

	if identity.Mode != ModeSeparate {
		t.Errorf("the mode = %q, want %q", identity.Mode, ModeSeparate)
	}
	if want := "separate — GitHub App crewflow-executor (installation 165781718)"; identity.Description != want {
		t.Errorf("the description = %q, want %q", identity.Description, want)
	}
	if want := "GH_TOKEN=" + theTokenOfTheExample(t); !contains(identity.Env, want) {
		t.Errorf("the environment of the orchestrator is %v, want %q in it", identity.Env, want)
	}
	assertHelperOfThisBuild(t, identity.GitConfig, "orchestrator")
	// The name and the address of the account are not here: nobody commits as the
	// orchestrator, and a commit of the merge is the one the gate approved (§7h).
	for _, unwanted := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME", "GIT_AUTHOR_EMAIL"} {
		for _, entry := range identity.Env {
			if strings.HasPrefix(entry, unwanted) {
				t.Errorf("the environment of the orchestrator holds %q, want no name to commit under", entry)
			}
		}
	}
	// The token is a secret of the orchestrator as it is of a run: a journal that
	// carried it would carry a credential of an hour into a file kept for ever (§7e).
	if want := theTokenOfTheExample(t); !contains(identity.Secrets, want) {
		t.Errorf("the secrets of the orchestrator are %v, want the token of the app in them", identity.Secrets)
	}
}

// TestTheOrchestratorIsDescribedWithoutATokenOfIt: a report of a machine shows the
// line of the mode of the orchestrator and hands it no rights: the token is asked for
// where it is used and nowhere else, and the description is the line every report
// shows (§7e, §7i).
func TestTheOrchestratorIsDescribedWithoutATokenOfIt(t *testing.T) {
	source, asked := orchestratorOfTheDocumentation(t)

	identity, err := source.DescribeOrchestrator(t.Context())
	if err != nil {
		t.Fatalf("DescribeOrchestrator: %v", err)
	}

	if identity.Mode != ModeSeparate || !strings.HasPrefix(identity.Description, "separate — GitHub App ") {
		t.Errorf("the description of the orchestrator is %q in the mode %q, want the line of §7i", identity.Description, identity.Mode)
	}
	if len(identity.Env) > 0 || len(identity.Secrets) > 0 {
		t.Errorf("the described orchestrator is %+v, want no token of it in there", identity)
	}
	for _, path := range asked.paths {
		if strings.HasSuffix(path, "/access_tokens") {
			t.Errorf("a description asked for a token at %q, want the line and nothing else", path)
		}
	}
}
