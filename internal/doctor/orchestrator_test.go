package doctor

import (
	"slices"
	"strings"
	"testing"
)

// orchestratorConfig is the settings of a project whose orchestrator works as an app
// of the host of its own: the mode, and the numbers that say which app it is. The key
// of the app is not in the file and never will be (docs/DESIGN.md §7e, §7i).
const orchestratorConfig = `
[orchestrator]
mode = "separate"

[orchestrator.github_app]
app_id = 5107053
installation_id = 12346
`

// TestReportSaysWhoseNameTheOrchestratorWorksUnder: the mode of the orchestrator is in
// every report as a field of its own, the same way the mode of the executor is, because
// a person who reads a report about a review or a merge has to see whose word it is
// written in without asking anything (docs/DESIGN.md §7i).
func TestReportSaysWhoseNameTheOrchestratorWorksUnder(t *testing.T) {
	cases := []struct {
		name string
		m    *machine
		want Identity
		// status is what the line of the mode is: the shared login is a warning about
		// what it costs, and an account of its own is what it says it should be.
		status Status
	}{
		{
			name:   "the mode of the shared login",
			status: Warn,
			m: newMachine().has("git", "gh", "agent").
				prints("git --version", "git version 2.47.1\n").
				prints("gh --version", "gh version 2.62.0\n").
				prints("gh auth status", "github.com\n  ✓ Logged in to github.com account octocat (keyring)\n"),
			want: Identity{
				Mode: "shared", Account: "octocat",
				Description: "shared — the login gh octocat (one login with the owner)",
			},
		},
		{
			name: "the mode of an account of its own",
			m:    machineWithTheOrchestrator(t),
			want: Identity{
				Mode: "separate", Account: "crewflow-orchestrator[bot]",
				Description: "separate — GitHub App crewflow-orchestrator (installation 12346)",
			},
			status: OK,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config := writeConfig(t, baseConfig)
			if tc.want.Mode == "separate" {
				config = writeConfig(t, tc.m.orchestratorProject())
			}

			report := runOn(t, tc.m, config)

			if report.Orchestrator != tc.want {
				t.Errorf("the report holds the orchestrator %+v, want %+v", report.Orchestrator, tc.want)
			}
			line := checkOf(t, report, "orchestrator identity")
			if line.Status != tc.status {
				t.Errorf("check \"orchestrator identity\" = %q (%s), want %q", line.Status, line.Detail, tc.status)
			}
			if line.Detail != tc.want.Description {
				t.Errorf("check \"orchestrator identity\" detail = %q, want %q", line.Detail, tc.want.Description)
			}
		})
	}
}

// TestTheSharedLoginIsAWarningAndSaysWhatItCosts: a project that has not set the second
// app up works, and everything a review needs works — so the line is a warning and not
// a failure, and it says plainly that the acceptance of the owner and the record of a
// review are one signature, which is the whole of what the mode costs (docs/DESIGN.md §7i).
func TestTheSharedLoginIsAWarningAndSaysWhatItCosts(t *testing.T) {
	m := newMachine().has("git", "gh", "agent").
		prints("git --version", "git version 2.47.1\n").
		prints("gh --version", "gh version 2.62.0\n").
		prints("gh auth status", "github.com\n  ✓ Logged in to github.com account octocat (keyring)\n")
	config := writeConfig(t, baseConfig)

	report := runOn(t, m, config)

	line := checkOf(t, report, "orchestrator identity")
	if line.Status != Warn {
		t.Errorf("check \"orchestrator identity\" = %q (%s), want %q: a review works under the login of the person",
			line.Status, line.Detail, Warn)
	}
	if !strings.Contains(line.Detail, "one login with the owner") {
		t.Errorf("check \"orchestrator identity\" detail = %q, want it to say that the login is one with the owner", line.Detail)
	}
	for _, want := range []string{`mode = "separate"`, "crewflow auth app import"} {
		if !strings.Contains(line.Hint, want) {
			t.Errorf("check \"orchestrator identity\" hint = %q, want it to mention %q", line.Hint, want)
		}
	}
	if !report.OK() {
		t.Error("report.OK() = false, want true: a project in the shared mode is a project crewflow works for")
	}
}

// TestTheOrchestratorApartFromTheOwnerChecksTheSecondApp: the key, the installation and
// the rights of the app of the orchestrator are three checks of their own, named apart
// from the three of the executor — a report of a project with two apps that said "app
// key" twice would leave a person with two lines and no way to tell which key is
// missing (docs/DESIGN.md §7i).
func TestTheOrchestratorApartFromTheOwnerChecksTheSecondApp(t *testing.T) {
	m := machineWithTheOrchestrator(t)
	config := writeConfig(t, m.orchestratorProject())

	report := runOn(t, m, config)

	if got := checkOf(t, report, "orchestrator app key"); got.Status != OK {
		t.Errorf("check \"orchestrator app key\" = %q (%s), want ok", got.Status, got.Detail)
	}
	app := checkOf(t, report, "orchestrator app")
	if app.Status != OK {
		t.Fatalf("check \"orchestrator app\" = %q (%s, %s), want ok", app.Status, app.Detail, app.Hint)
	}
	for _, want := range []string{"12346", "naghuale/crewflow", "contents write", "issues write"} {
		if !strings.Contains(app.Detail, want) {
			t.Errorf("check \"orchestrator app\" detail = %q, want it to say %q", app.Detail, want)
		}
	}
	token := checkOf(t, report, "orchestrator token")
	if token.Status != OK {
		t.Errorf("check \"orchestrator token\" = %q (%s), want ok", token.Status, token.Detail)
	}
	// A report of a machine never shows a token, and asks for one only to find out
	// that the API hands it out (docs/DESIGN.md §7e, §7i).
	if strings.Contains(printed(report), "ghs_token_of_the_orchestrator") {
		t.Errorf("the report holds the value of the token:\n%s", printed(report))
	}
	// The checks of the executor are named as they always are and the ones of the
	// orchestrator beside them: a report that called both of them "app key" would
	// leave a person with two lines and no way to tell which key is missing.
	for _, want := range []string{"gh", "gh login", "executor identity", "orchestrator app key"} {
		if !slices.Contains(checkNames(report), want) {
			t.Errorf("the report holds the checks %v, want %q among them", checkNames(report), want)
		}
	}
}

// TestTheOrchestratorWithoutItsKeySaysWhatToDo: an app of the orchestrator that nobody
// imported the key of is a project whose reviews cannot be written and whose merges
// cannot be pushed as the app, and the report names the command that makes it possible
// (docs/DESIGN.md §7i).
func TestTheOrchestratorWithoutItsKeySaysWhatToDo(t *testing.T) {
	m := machineWithTheOrchestrator(t)
	m.holdsTheKey = false
	config := writeConfig(t, m.orchestratorProject())

	report := runOn(t, m, config)

	key := checkOf(t, report, "orchestrator app key")
	if key.Status != Fail {
		t.Errorf("check \"orchestrator app key\" = %q (%s), want fail", key.Status, key.Detail)
	}
	if !strings.Contains(key.Hint, "crewflow auth app import") {
		t.Errorf("check \"orchestrator app key\" hint = %q, want it to name the command that imports the key", key.Hint)
	}
	line := checkOf(t, report, "orchestrator identity")
	if line.Status != Fail {
		t.Errorf("check \"orchestrator identity\" = %q, want fail: the mode cannot be worked out without the key", line.Status)
	}
	if !strings.Contains(line.Hint, "orchestrator.github_app") {
		t.Errorf("check \"orchestrator identity\" hint = %q, want it to name the key of the project to change", line.Hint)
	}
}

// TestTheRightsOfTheAppOfTheOrchestratorAreTheRightsOfTheOrchestrator: the report reads
// what the app of the orchestrator may do against what the orchestrator asks for, and
// not against what a run asks for — an app of the orchestrator has the issues in write
// and that is no news, while a right nobody asked for is (§7i).
func TestTheRightsOfTheAppOfTheOrchestratorAreTheRightsOfTheOrchestrator(t *testing.T) {
	m := machineWithTheOrchestrator(t)
	m.api.installationOfTheOrchestrator(map[string]any{
		"contents": "write", "pull_requests": "write",
		"issues": "write", "metadata": "read", "workflows": "write",
	})
	config := writeConfig(t, m.orchestratorProject())

	report := runOn(t, m, config)

	app := checkOf(t, report, "orchestrator app")
	if app.Status != Fail {
		t.Fatalf("check \"orchestrator app\" = %q (%s), want fail: the app may do more than the orchestrator needs", app.Status, app.Detail)
	}
	if !strings.Contains(app.Hint, "workflows write") {
		t.Errorf("check \"orchestrator app\" hint = %q, want it to name the right that is too wide", app.Hint)
	}
}

// TestTheDoctorSaysAheadOfTimeThatTheProtectionOfTheBranchIsUnreadable: the rules of the
// protection of a branch are read with the right of an administrator, and the App of the
// orchestrator has none — so a project whose default branch has that protection on cannot
// be merged through the gate at all, and `crewflow doctor` is where a person finds that
// out before the first merge and not in the middle of one (docs/DESIGN.md §7h, §7i).
func TestTheDoctorSaysAheadOfTimeThatTheProtectionOfTheBranchIsUnreadable(t *testing.T) {
	m := machineWithTheOrchestrator(t)
	m.prints("gh api repos/naghuale/crewflow/branches/main",
		`{"name":"main","protected":true,"protection":{"enabled":true}}`)
	config := writeConfig(t, m.orchestratorProject())

	report := runOn(t, m, config)

	check := checkOf(t, report, "orchestrator branch protection")
	if check.Status != Warn {
		t.Fatalf("check %q = %q (%s), want %q", check.Name, check.Status, check.Detail, Warn)
	}
	if !strings.Contains(check.Hint, "administration: read") {
		t.Errorf("check %q hint = %q, want it to name the right that is missing", check.Name, check.Hint)
	}
	if !report.OK() {
		t.Error("report.OK() = false, want true: a rule of the host crewflow cannot change is not a missing thing")
	}
}

// machineWithTheOrchestrator is a machine of a test that has the second app of the
// project on it: the server of the test answers for it and its installation, and the
// store of the machine holds the key the test generated. No report of crewflow asks
// GitHub for a token of a real app (docs/DESIGN.md §7i).
//
// The executor of such a project is left in the mode of the owner, so that the report
// under test holds the account of the orchestrator alone: the names of the two Apps of
// a project are both checked in the tests of the adapter, which asks one server for both
// (§7i).
func machineWithTheOrchestrator(t *testing.T) *machine {
	t.Helper()
	m := machineWithAnApp(t)
	m.holdsTheKey = true
	m.prints("gh api repos/naghuale/crewflow/branches/main", branchOfTheRulesets)
	m.api.answer["5107053 /api/v3/app"] = map[string]any{"id": 5107053, "slug": "crewflow-orchestrator"}
	m.api.answer["/api/v3/users/crewflow-orchestrator[bot]"] = map[string]any{
		"id": 1988, "login": "crewflow-orchestrator[bot]", "type": "Bot",
	}
	// The account of the App of the orchestrator is what the lists of the gate may name,
	// and a name in them is asked of the host for the number under it (§7h, §7i).
	m.prints("gh api users/crewflow-orchestrator%5Bbot%5D",
		`{"id": 1988, "login": "crewflow-orchestrator[bot]", "type": "Bot"}`)
	m.api.installationOfTheOrchestrator(rightsOfTheOrchestrator())
	return m
}

// branchOfTheRulesets is what gh writes about the default branch of a project whose
// rules are rulesets: the branch says of itself that it is protected, and the protection
// of a branch itself is off — which is what keeps crewflow from asking the App of the
// orchestrator for rules only an administrator may read (docs/DESIGN.md §7h, §7i).
const branchOfTheRulesets = `{
  "name": "main",
  "commit": {"sha": "fb8c5057f225d20e06d3d609750fe36458fa9fd7"},
  "protected": true,
  "protection": {"enabled": false}
}`

// rightsOfTheOrchestrator are the four rights of §7i with the issues in write: the
// orchestrator closes the task behind a change, and a run of the executor does not.
func rightsOfTheOrchestrator() map[string]any {
	return map[string]any{
		"contents": "write", "pull_requests": "write", "issues": "write", "metadata": "read",
	}
}

// installationOfTheOrchestrator answers for the installation of the app of the
// orchestrator on the repository of the project, with the rights given, and hands out a
// token of it — the answer of the documentation of the REST API of GitHub in its whole
// (docs/DESIGN.md §7i).
func (a *apiOfTheTest) installationOfTheOrchestrator(rights map[string]any) {
	answer := map[string]any{
		"id": 12346, "app_id": 5107053,
		"account":              map[string]any{"login": "naghuale", "id": 1, "type": "User", "site_admin": false},
		"repository_selection": "selected",
		"permissions":          rights,
	}
	a.answer["5107053 /api/v3/app/installations/12346"] = answer
	// The installation of an App on a repository is asked through the repository, and
	// the App of the request is what tells the server which installation to answer with.
	a.answer["5107053 /api/v3/repos/naghuale/crewflow/installation"] = answer
	a.answer["/api/v3/app/installations/12346/access_tokens"] = map[string]any{
		"token":                "ghs_token_of_the_orchestrator",
		"expires_at":           "2026-09-28T13:00:00Z",
		"permissions":          rights,
		"repository_selection": "selected",
		"repositories": []map[string]any{{
			"id": 1296269, "node_id": "MDEwOlJlcG9zaXRvcnkxMjk2MjY5",
			"name": "crewflow", "full_name": "naghuale/crewflow",
		}},
	}
}

// orchestratorProject is the crewflow.toml of a project whose orchestrator works as an
// app of the server of the test: the host of the file is the address of the server, and
// the second app of the project is named in it (docs/DESIGN.md §7i).
func (m *machine) orchestratorProject() string {
	return baseConfig + "\n[forge]\nkind = \"github\"\nhost = \"" + m.host + "\"\n" + orchestratorConfig
}
