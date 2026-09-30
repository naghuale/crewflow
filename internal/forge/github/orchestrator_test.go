package github

import (
	"slices"
	"strings"
	"testing"

	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/secret"
)

// machineWithTwoApps is a machine of a test with the two Apps of a project on it: the
// App of the executor and the App of the orchestrator, each with a key of its own in
// the store of the test and each answered by the server of the test in the form of the
// documentation of the API of GitHub (docs/DESIGN.md §7i).
func machineWithTwoApps(t *testing.T) (*machine, *serverOfTheTest) {
	t.Helper()
	m, api := machineWithAnApp(t)
	api.answer["/app"] = map[string]any{"id": 5107052, "slug": "crewflow-orchestrator"}
	api.answer["/users/crewflow-orchestrator[bot]"] = map[string]any{
		"id": 1988, "login": "crewflow-orchestrator[bot]", "type": "Bot",
	}
	api.answer["/app/installations/12345/access_tokens"] = tokenOfTheOrchestrator()
	api.installationOfTheOrchestrator(rightsOfTheOrchestrator())
	m.secrets = &storeOfTheTest{keys: map[string][]byte{
		secret.AppKey(5107052): keyOfTheTest(t),
		secret.AppKey(5107053): keyOfTheTest(t),
	}}
	return m, api
}

// rightsOfTheOrchestrator are the four rights of §7i with the issues in write: the
// orchestrator closes the task behind the change that went in, and a run of the
// executor does not (docs/DESIGN.md §7h, §7i).
func rightsOfTheOrchestrator() map[string]any {
	return map[string]any{
		"contents": "write", "pull_requests": "write", "issues": "write", "metadata": "read",
	}
}

// tokenOfTheOrchestrator is the answer of the documentation of the REST API of GitHub
// to a request for the token of the installation of the orchestrator: the same answer,
// with the rights of the orchestrator in it.
func tokenOfTheOrchestrator() map[string]any {
	return map[string]any{
		"token":                "ghs_token_of_the_orchestrator",
		"expires_at":           "2026-09-28T13:00:00Z",
		"permissions":          rightsOfTheOrchestrator(),
		"repository_selection": "selected",
		"repositories": []map[string]any{{
			"id": 1296269, "node_id": "MDEwOlJlcG9zaXRvcnkxMjk2MjY5",
			"name": "crewflow", "full_name": "naghuale/crewflow",
		}},
	}
}

// installationOfTheOrchestrator answers for the installation of the App of the
// orchestrator on the repository of the project, with the rights given.
func (a *serverOfTheTest) installationOfTheOrchestrator(rights map[string]any) {
	answer := map[string]any{
		"id": 12346, "app_id": 5107053,
		"account":              map[string]any{"login": "naghuale", "id": 1, "type": "User", "site_admin": false},
		"repository_selection": "selected",
		"permissions":          rights,
	}
	a.answer["/repos/naghuale/crewflow/installation"] = answer
	a.answer["/app/installations/12346"] = answer
	a.answer["/app/installations/12346/access_tokens"] = tokenOfTheOrchestrator()
}

// TestTheOrchestratorWithoutAnAccountOfItsOwnIsTheLoginOfThePerson: a project that has
// not set up a second App has one login for the orchestrator and the owner, and the
// line a report shows says so in the words of §7i — the whole of what that mode costs
// is in that line (docs/DESIGN.md §7i).
func TestTheOrchestratorWithoutAnAccountOfItsOwnIsTheLoginOfThePerson(t *testing.T) {
	m := newMachine().prints("auth status", "github.com\n  ✓ Logged in to github.com account naghuale (keyring)\n")
	a := New(repo, "", m.env(t)).WithOrchestrator(Orchestrator{})

	identity, err := a.OrchestratorIdentity(t.Context())
	if err != nil {
		t.Fatalf("OrchestratorIdentity returned an error: %v", err)
	}

	if identity.Mode != forge.ModeShared {
		t.Errorf("the mode = %q, want %q", identity.Mode, forge.ModeShared)
	}
	if want := "shared — the login gh naghuale (one login with the owner)"; identity.Description != want {
		t.Errorf("the description = %q, want %q", identity.Description, want)
	}
	// Nothing is handed to it: the login of the person is what it already has, and a
	// token in an environment it does not need is a token somebody has to redact.
	if len(identity.Env) > 0 || len(identity.GitConfig) > 0 || len(identity.Secrets) > 0 {
		t.Errorf("the identity of an orchestrator in the shared mode is %+v, want it empty of rights", identity)
	}
}

// TestTheOrchestratorApartFromTheOwnerIsTheSecondApp: the account the orchestrator of
// a project works as when the project has set one up, the token gh speaks with, and the
// helper git takes the credentials of a push from — with the App named in it, because a
// project has two Apps and a push of a merge is not the one of the executor (§7i).
func TestTheOrchestratorApartFromTheOwnerIsTheSecondApp(t *testing.T) {
	m, api := machineWithTwoApps(t)
	a := New(repo, "", m.env(t)).
		WithOrchestrator(Orchestrator{AppID: 5107053, InstallationID: 12346, API: api.URL()})

	identity, err := a.OrchestratorIdentity(t.Context())
	if err != nil {
		t.Fatalf("OrchestratorIdentity returned an error: %v", err)
	}

	if identity.Mode != forge.ModeSeparate {
		t.Errorf("the mode = %q, want %q", identity.Mode, forge.ModeSeparate)
	}
	if want := "separate — GitHub App crewflow-orchestrator (installation 12346)"; identity.Description != want {
		t.Errorf("the description = %q, want %q", identity.Description, want)
	}
	if !slices.Contains(identity.Env, "GH_TOKEN=ghs_token_of_the_orchestrator") {
		t.Errorf("the environment of the orchestrator is %v, want the token of its app in it", identity.Env)
	}
	if want := "crewflow auth git-credential -as orchestrator"; identity.GitConfig["credential.helper"] != want {
		t.Errorf("the settings of git are %v, want credential.helper = %q", identity.GitConfig, want)
	}
	if !slices.Contains(identity.Secrets, "ghs_token_of_the_orchestrator") {
		t.Errorf("the secrets of the orchestrator are %v, want the token of its app in them", identity.Secrets)
	}
}

// TestEveryCommandOfGhSpeaksAsTheOrchestrator: the record of a review and the closing
// of a task are the business of the orchestrator, and a gh that answered as the person
// would write both in the name of the owner — which is the very thing the second App
// is for, and what the gate then cannot tell from a decision of the owner (§7h, §7i).
func TestEveryCommandOfGhSpeaksAsTheOrchestrator(t *testing.T) {
	m, api := machineWithTwoApps(t)
	m.prints("pr comment", "")
	m.prints("issue close", "")
	m.prints("pr list", `[{"number":7,"url":"https://github.com/naghuale/crewflow/pull/7",`+
		`"headRefName":"crewflow/31-the-task","headRefOid":"9f1c0de","baseRefName":"main",`+
		`"state":"OPEN","body":"Closes #31"}]`)
	a := New(repo, "", m.env(t)).
		WithOrchestrator(Orchestrator{AppID: 5107053, InstallationID: 12346, API: api.URL()})

	if err := a.WriteComment(t.Context(), 7, "REVIEW: APPROVED 9f1c0de"); err != nil {
		t.Fatalf("WriteComment returned an error: %v", err)
	}
	if err := a.CloseTask(t.Context(), 31, "merged #7"); err != nil {
		t.Fatalf("CloseTask returned an error: %v", err)
	}
	if _, _, err := a.FindChangeRequest(t.Context(), "crewflow/31-the-task"); err != nil {
		t.Fatalf("FindChangeRequest returned an error: %v", err)
	}

	if len(m.ran) == 0 {
		t.Fatal("gh was asked nothing, want the commands of a review to have been run")
	}
	for _, command := range m.ran {
		if !slices.Contains(command.env, "GH_TOKEN=ghs_token_of_the_orchestrator") {
			t.Errorf("gh was asked for %q with the environment %v, want the token of the app of the orchestrator",
				strings.Join(command.args, " "), command.env)
		}
	}
	// One token for the whole of one review: a review asks gh a few times, and a
	// token of an hour for every question would be a token an hour for every question.
	tokens := 0
	for _, path := range api.asked {
		if strings.HasSuffix(path, "/access_tokens") {
			tokens++
		}
	}
	if tokens != 1 {
		t.Errorf("the API handed out %d tokens, want one for the whole review", tokens)
	}
}

// TestTheRecordOfAReviewIsWrittenAsTheAccountOfTheApp: a record of a review is an
// approval only because of the account it is written in, and the gate counts the
// records of the reviewers of the project — so the account the adapter writes as has to
// be the one of the App, and not the login gh happens to be signed in as (§7h, §7i).
func TestTheRecordOfAReviewIsWrittenAsTheAccountOfTheApp(t *testing.T) {
	m, api := machineWithTwoApps(t)
	a := New(repo, "", m.env(t)).
		WithOrchestrator(Orchestrator{AppID: 5107053, InstallationID: 12346, API: api.URL()})

	account, err := a.SignedIn(t.Context())
	if err != nil {
		t.Fatalf("SignedIn returned an error: %v", err)
	}

	if account != "crewflow-orchestrator[bot]" {
		t.Errorf("the account of the record is %q, want the account of the app of the orchestrator", account)
	}
}

// TestTheAccountOfTheSharedLoginIsStillTheLoginOfGh: a project that has not set up a
// second App writes its records under the login of the person, and that is the whole of
// what the shared mode is: the gate cannot tell it from the owner, and `doctor` says so
// (§7i).
func TestTheAccountOfTheSharedLoginIsStillTheLoginOfGh(t *testing.T) {
	m := newMachine().prints("auth status", "github.com\n  ✓ Logged in to github.com account naghuale (keyring)\n")
	a := New(repo, "", m.env(t)).WithOrchestrator(Orchestrator{})

	account, err := a.SignedIn(t.Context())
	if err != nil {
		t.Fatalf("SignedIn returned an error: %v", err)
	}

	if account != "naghuale" {
		t.Errorf("the account of the record is %q, want the login gh is signed in as", account)
	}
}

// TestDoctorOfTheOrchestratorLooksForTheKeyAndTheInstallation: an orchestrator apart
// from the owner that is not set up cannot write a record of a review or push a merge,
// and `crewflow doctor` is where a person finds that out — and what it is told there is
// about the rights of the orchestrator and not about those of a run (docs/DESIGN.md §7i).
func TestDoctorOfTheOrchestratorLooksForTheKeyAndTheInstallation(t *testing.T) {
	cases := []struct {
		name string
		// rights is what the API of the test answers as the rights of the installation
		// of the app of the orchestrator, and key is whether the store of the machine
		// holds that key.
		rights map[string]any
		key    bool
		want   []struct {
			name   string
			status forge.Status
		}
	}{
		{
			name:   "the app is installed with the rights of the orchestrator",
			rights: rightsOfTheOrchestrator(),
			key:    true,
			want: []struct {
				name   string
				status forge.Status
			}{
				{"gh", forge.OK},
				{orchestratorKeyCheck, forge.OK},
				{orchestratorAppCheck, forge.OK},
				{orchestratorTokenCheck, forge.OK},
			},
		},
		{
			name:   "the key of the second app was never imported",
			rights: rightsOfTheOrchestrator(),
			want: []struct {
				name   string
				status forge.Status
			}{
				{"gh", forge.OK},
				{orchestratorKeyCheck, forge.Fail},
			},
		},
		{
			name: "the app may do more than the orchestrator needs",
			rights: map[string]any{
				"contents": "write", "pull_requests": "write",
				"issues": "write", "metadata": "read", "workflows": "write",
			},
			key: true,
			want: []struct {
				name   string
				status forge.Status
			}{
				{"gh", forge.OK},
				{orchestratorKeyCheck, forge.OK},
				{orchestratorAppCheck, forge.Fail},
				{orchestratorTokenCheck, forge.OK},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, api := machineWithTwoApps(t)
			if !tc.key {
				m.secrets = &storeOfTheTest{keys: map[string][]byte{secret.AppKey(5107052): keyOfTheTest(t)}}
			}
			if tc.rights != nil {
				api.installationOfTheOrchestrator(tc.rights)
			}
			a := New(repo, "", m.env(t)).
				WithOrchestrator(Orchestrator{AppID: 5107053, InstallationID: 12346, API: api.URL()})

			checks := a.Doctor(t.Context())

			if len(checks) != len(tc.want) {
				t.Fatalf("doctor made the checks %v, want %d of them", checkNames(checks), len(tc.want))
			}
			for i, want := range tc.want {
				if checks[i].Name != want.name {
					t.Errorf("check %d is %q, want %q", i, checks[i].Name, want.name)
					continue
				}
				if checks[i].Status != want.status {
					t.Errorf("check %q = %q (%s), want %q", checks[i].Name, checks[i].Status, checks[i].Hint, want.status)
				}
				if want.status != forge.OK && checks[i].Hint == "" {
					t.Errorf("check %q is %q and says nothing to do about it", checks[i].Name, checks[i].Status)
				}
			}
			// A report of a machine never leaves a token in a terminal, whoever the App
			// of it is (§7e).
			for _, command := range m.ran {
				for _, entry := range command.env {
					if strings.Contains(entry, "ghs_token_of_the_") {
						t.Errorf("the report started a command with a token in it: %q", entry)
					}
				}
			}
		})
	}
}

// TestTheDoctorOfTheSharedModeDoesNotAskAboutASecondApp: a project that has not set up
// an account of its own for the orchestrator has nothing to check about one, and a
// report that asked would be a report of a setup nobody chose (docs/DESIGN.md §7i).
func TestTheDoctorOfTheSharedModeDoesNotAskAboutASecondApp(t *testing.T) {
	m := newMachine()
	a := New(repo, "", m.env(t)).WithOrchestrator(Orchestrator{})

	checks := a.Doctor(t.Context())

	if len(checks) != 2 || checks[0].Name != ghCheck || checks[1].Name != ghLoginCheck {
		t.Errorf("doctor made the checks %v, want the gh of a project in the shared mode", checkNames(checks))
	}
}
