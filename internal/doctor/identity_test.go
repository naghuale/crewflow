package doctor

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/naghuale/crewflow/internal/secret"
)

// botConfig is the settings of a project whose executor works as an app of its own:
// the mode, and the numbers that say which app it is. The key of the app is not in
// the file and never will be (docs/DESIGN.md §7e, §7i).
const botConfig = `
[identity]
mode = "bot"

[identity.github_app]
app_id = 5107052
installation_id = 12345
`

// TestRunInTheModeOfTheBotNamesTheApp: a report of a project whose executor works as
// an app says which app, says that the key is in the store and says what the app may
// do — and it says all of it with a token of an hour that it asked for and threw away,
// because a report is not a run (docs/DESIGN.md §7e, §7i).
func TestRunInTheModeOfTheBotNamesTheApp(t *testing.T) {
	m := machineWithAnApp(t)
	m.holdsTheKey = true
	config := writeConfig(t, m.botProject())

	report := runOn(t, m, config)

	if got := checkOf(t, report, "executor identity"); got.Status != OK {
		t.Errorf("check \"executor identity\" = %q (%s), want ok", got.Status, got.Detail)
	} else if !strings.Contains(got.Detail, "crewflow-executor") || !strings.Contains(got.Detail, "12345") {
		t.Errorf("check \"executor identity\" detail = %q, want it to name the app and its installation", got.Detail)
	}
	if got := checkOf(t, report, "app key"); got.Status != OK {
		t.Errorf("check \"app key\" = %q (%s), want ok", got.Status, got.Detail)
	}
	app := checkOf(t, report, "app")
	if app.Status != OK {
		t.Errorf("check \"app\" = %q (%s, %s), want ok", app.Status, app.Detail, app.Hint)
	}
	for _, want := range []string{"contents write", "pull_requests write", "issues read"} {
		if !strings.Contains(app.Detail, want) {
			t.Errorf("check \"app\" detail = %q, want it to say %q", app.Detail, want)
		}
	}
}

// TestRunInTheModeOfTheBotAsksForATokenAndShowsNoneOfIt: a run in the mode of the bot
// cannot start without a token, so a report asks for one and throws it away — a
// refusal of GitHub is a refusal a person sees before a task and not in the middle of
// one. The value of the token is nowhere in the report: a report is pasted into issues
// and read by people (docs/DESIGN.md §7e, §7i).
func TestRunInTheModeOfTheBotAsksForATokenAndShowsNoneOfIt(t *testing.T) {
	m := machineWithAnApp(t)
	m.holdsTheKey = true
	config := writeConfig(t, m.botProject())

	report := runOn(t, m, config)

	token := checkOf(t, report, "token")
	if token.Status != OK {
		t.Fatalf("check \"token\" = %q (%s), want ok", token.Status, token.Detail)
	}
	if !strings.Contains(token.Detail, "naghuale/crewflow") {
		t.Errorf("check \"token\" detail = %q, want it to name the repository the token is for", token.Detail)
	}
	if strings.Contains(printed(report), "ghs_token_of_the_run") {
		t.Errorf("the report holds the value of the token:\n%s", printed(report))
	}
	asked := false
	for _, path := range m.api.asked {
		if strings.HasSuffix(path, "/access_tokens") {
			asked = true
		}
	}
	if !asked {
		t.Errorf("the report asked %v, want it to ask for a token of the repository", m.api.asked)
	}
}

// TestRunInTheModeOfTheBotWithATokenGitHubRefuses: the words of GitHub are the line of
// the report and what to do about them is the hint under it — a person who reads a
// refusal has to change something in the settings of the app, and a report that said
// only "failed" would tell them nothing.
func TestRunInTheModeOfTheBotWithATokenGitHubRefuses(t *testing.T) {
	m := machineWithAnApp(t)
	m.holdsTheKey = true
	m.api.answer["/api/v3/app/installations/12345/access_tokens"] = map[string]any{
		"message": theRefusalOfGitHub,
		"errors": []any{map[string]any{
			"resource": "InstallationAccessToken", "code": "custom", "message": theRefusalOfGitHub,
		}},
	}
	m.api.refusesAt("/api/v3/app/installations/12345/access_tokens", http.StatusUnprocessableEntity)
	config := writeConfig(t, m.botProject())

	report := runOn(t, m, config)

	token := checkOf(t, report, "token")
	if token.Status != Fail {
		t.Fatalf("check \"token\" = %q (%s), want fail: a run cannot start without a token", token.Status, token.Detail)
	}
	if !strings.Contains(token.Detail, theRefusalOfGitHub) || !strings.Contains(token.Detail, "422") {
		t.Errorf("check \"token\" detail = %q, want the words of GitHub and the code it answered", token.Detail)
	}
	if !strings.Contains(token.Hint, "identity.github_app") {
		t.Errorf("check \"token\" hint = %q, want it to name what to change in the file of the project", token.Hint)
	}
	if report.OK() {
		t.Error("report.OK() = true, want false: the machine cannot start a run in the mode of the bot")
	}
}

// TestRunInTheModeOfTheBotWithoutAKeySaysWhatToDo: an app nobody imported the key of
// is a project whose runs cannot start, and the report names the command that makes
// it possible (docs/DESIGN.md §7i).
func TestRunInTheModeOfTheBotWithoutAKeySaysWhatToDo(t *testing.T) {
	m := machineWithAnApp(t)
	config := writeConfig(t, m.botProject())

	report := runOn(t, m, config)

	key := checkOf(t, report, "app key")
	if key.Status != Fail {
		t.Errorf("check \"app key\" = %q (%s), want fail", key.Status, key.Detail)
	}
	if !strings.Contains(key.Hint, "crewflow auth app import") {
		t.Errorf("check \"app key\" hint = %q, want it to name the command that imports the key", key.Hint)
	}
	if got := checkOf(t, report, "executor identity"); got.Status != Fail {
		t.Errorf("check \"executor identity\" = %q, want fail: the mode cannot be worked out without the key", got.Status)
	}
}

// TestRunInTheModeOfTheBotWithRightsWiderThanARunNeeds: what an app may do is worth
// nothing if the report only says what was asked for, and a run of this project is
// handed a token for four rights and no more (docs/DESIGN.md §7i).
func TestRunInTheModeOfTheBotWithRightsWiderThanARunNeeds(t *testing.T) {
	m := machineWithAnApp(t)
	m.holdsTheKey = true
	m.api.installationOfTheTest(map[string]any{
		"contents": "write", "pull_requests": "write",
		"issues": "read", "metadata": "read", "workflows": "write",
	})
	config := writeConfig(t, m.botProject())

	report := runOn(t, m, config)

	app := checkOf(t, report, "app")
	if app.Status != Fail {
		t.Fatalf("check \"app\" = %q (%s), want fail: the app may do more than a run needs", app.Status, app.Detail)
	}
	if !strings.Contains(app.Hint, "workflows write") {
		t.Errorf("check \"app\" hint = %q, want it to name the right that is too wide", app.Hint)
	}
}

// TestReportSaysWhoseNameTheExecutorWorksUnder: the mode is in every report and not
// only in the one about a bot, because an orchestrator reads the report on every run
// and has to know whose powers the run has (docs/DESIGN.md §7i).
func TestReportSaysWhoseNameTheExecutorWorksUnder(t *testing.T) {
	cases := []struct {
		name string
		m    *machine
		want Identity
	}{
		{
			name: "the mode of the owner",
			m: newMachine().has("git", "gh", "agent").
				prints("git --version", "git version 2.47.1\n").
				prints("gh --version", "gh version 2.62.0\n").
				prints("gh auth status", "github.com\n  ✓ Logged in to github.com account octocat (keyring)\n"),
			want: Identity{Mode: "owner", Description: "owner — the login gh octocat (shared rights)"},
		},
		{
			name: "the mode of the bot",
			m: func() *machine {
				m := machineWithAnApp(t)
				m.holdsTheKey = true
				return m
			}(),
			want: Identity{Mode: "bot", Description: "bot — GitHub App crewflow-executor (installation 12345)"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config := writeConfig(t, baseConfig)
			if tc.want.Mode == "bot" {
				config = writeConfig(t, tc.m.botProject())
			}

			report := runOn(t, tc.m, config)

			if report.Identity != tc.want {
				t.Errorf("the report holds the identity %+v, want %+v", report.Identity, tc.want)
			}
			// The report is read by an orchestrator in JSON, and the mode is a field of
			// its own: a script that reads the report on every run must not have to
			// parse a line of a check to know whose powers a run has.
			data, err := json.Marshal(report)
			if err != nil {
				t.Fatalf("marshal the report: %v", err)
			}
			var answer struct {
				Identity struct {
					Mode        string `json:"mode"`
					Description string `json:"description"`
				} `json:"identity"`
			}
			if err := json.Unmarshal(data, &answer); err != nil {
				t.Fatalf("unmarshal the report: %v", err)
			}
			if answer.Identity.Mode != tc.want.Mode || answer.Identity.Description != tc.want.Description {
				t.Errorf("the JSON of the report holds the identity %+v, want %+v", answer.Identity, tc.want)
			}
		})
	}
}

// TestReportWithoutTheFileOfTheProjectHasAMode: a report is read on every run, and a
// field that is missing in one of them is a field a script has to guard against. A
// report made without a file to read holds an empty mode, as it holds empty lists for
// the reading policy.
func TestReportWithoutTheFileOfTheProjectHasAMode(t *testing.T) {
	m := newMachine().has("git").prints("git --version", "git version 2.47.1\n")

	report := runOn(t, m, filepath.Join(t.TempDir(), "missing.toml"))

	if report.Identity != (Identity{}) {
		t.Errorf("the report of a run without a file holds the identity %+v, want an empty one", report.Identity)
	}
	if !slices.Contains(checkNames(report), "config") {
		t.Error("the report of a run without a file holds no check of the file, want the reason it could not be read")
	}
}

// machineWithAnApp is a machine of a test that has a server of GitHub on it: the API
// of a project on a host of its own is under https://<host>/api/v3, so the host of
// the test is the address of a server of the test and the client is the one that
// trusts its certificate. No test of crewflow asks GitHub for a token, and none opens
// the keychain of a person (docs/DESIGN.md §7i).
func machineWithAnApp(t *testing.T) *machine {
	t.Helper()
	api := &apiOfTheTest{answer: map[string]any{
		"/api/v3/app": map[string]any{"id": 5107052, "slug": "crewflow-executor"},
	}, status: map[string]int{}}
	api.installationOfTheTest(rightsOfTheDesign())
	api.answer["/api/v3/app/installations/12345/access_tokens"] = tokenOfTheTest()
	api.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.asked = append(api.asked, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		answer, ok := api.answer[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"message": "Not Found: " + r.URL.Path})
			return
		}
		if code, refused := api.status[r.URL.Path]; refused {
			w.WriteHeader(code)
		}
		_ = json.NewEncoder(w).Encode(answer)
	}))
	t.Cleanup(api.server.Close)
	m := newMachine().
		has("git", "gh", "agent").
		prints("git --version", "git version 2.47.1\n").
		prints("gh --version", "gh version 2.62.0\n").
		prints("gh auth status", "github.com\n  ✓ Logged in to github.com account octocat (keyring)\n")
	m.api = api
	m.host = strings.TrimPrefix(api.server.URL, "https://")
	return m
}

// apiOfTheTest is the API of GitHub as a report of a test needs it: it answers for the
// app and for its installation, and it writes down what it was asked at.
type apiOfTheTest struct {
	answer map[string]any
	// status is the code of the answer of a path, for an answer the API of GitHub
	// refuses with: the code and the words of a refusal are what a report has to show.
	status map[string]int
	asked  []string
	server *httptest.Server
}

// refusesAt makes the server answer the path with the code, the way the API of GitHub
// refuses a request it does not want to grant.
func (a *apiOfTheTest) refusesAt(path string, code int) {
	a.status[path] = code
}

// installationOfTheTest answers for the installation of the App on the repository of
// the project, with the rights given. GitHub answers the question about the repository
// and the question about the number in the file of the project with the same thing, so
// both paths are answered with it — and it answers it in the form the documentation of
// the REST API of GitHub writes it (version 2022-11-28): the account of the owner is an
// object with a name in it and not a name, and the rights are a map of a name to a
// level. The answers in the whole of that form are in the testdata of the package of
// the app, which is the one that reads them.
func (a *apiOfTheTest) installationOfTheTest(rights map[string]any) {
	answer := map[string]any{
		"id": 12345, "app_id": 5107052,
		"account":              map[string]any{"login": "naghuale", "id": 1, "type": "User", "site_admin": false},
		"repository_selection": "selected",
		"permissions":          rights,
	}
	a.answer["/api/v3/repos/naghuale/crewflow/installation"] = answer
	a.answer["/api/v3/app/installations/12345"] = answer
}

// rightsOfTheDesign are the four rights a token of a run is asked for and no more, as
// the API of GitHub writes them: a name and a level (docs/DESIGN.md §7i).
func rightsOfTheDesign() map[string]any {
	return map[string]any{
		"contents": "write", "pull_requests": "write", "issues": "read", "metadata": "read",
	}
}

// tokenOfTheTest is the answer of the documentation of the REST API of GitHub to a
// request for the token of the installation, with the repository of the project and the
// rights of §7i in it: crewflow reads the repositories of the answer to be sure the
// token is for the one repository of a run, and the rights to be sure they are no wider
// than a run needs.
func tokenOfTheTest() map[string]any {
	return map[string]any{
		"token":                "ghs_token_of_the_run",
		"expires_at":           "2026-09-28T13:00:00Z",
		"permissions":          rightsOfTheDesign(),
		"repository_selection": "selected",
		"repositories": []map[string]any{{
			"id": 1296269, "node_id": "MDEwOlJlcG9zaXRvcnkxMjk2MjY5",
			"name": "crewflow", "full_name": "naghuale/crewflow",
		}},
	}
}

// theRefusalOfGitHub is what the API of GitHub answers to a request for a token of a
// repository that is not a repository of the installation: it is the words of a person
// who reads the report and has to decide what to change.
const theRefusalOfGitHub = "There is at least one repository that does not exist " +
	"or is not accessible to the parent installation."

// botProject is the crewflow.toml of a project whose executor works as an app of the
// server of the test, which is what [machine.host] is: the API of a project on a host
// of its own is under https://<host>/api/v3, and a test of an app is a test of a
// server of a test (docs/DESIGN.md §7i).
func (m *machine) botProject() string {
	return baseConfig + "\n[forge]\nkind = \"github\"\nhost = \"" + m.host + "\"\n" + botConfig
}

// storeOfTheTest is the store of a test: it holds the key the test generated, or
// nothing at all when the test is about a project whose key was never imported. The
// keychain of macOS is never in a test (docs/DESIGN.md §7i).
type storeOfTheTest struct {
	key []byte
}

// Get returns the key of the test, and the error of an empty store when it has none.
func (s *storeOfTheTest) Get(service, account string) ([]byte, error) {
	if s.key == nil {
		return nil, secret.ErrNotFound
	}
	return s.key, nil
}

// Set keeps the key the test is given.
func (s *storeOfTheTest) Set(service, account string, value []byte) error {
	s.key = value
	return nil
}

// Has says whether a key is in the store, as the keychain of macOS can: a report asks
// for that and not for a value.
func (s *storeOfTheTest) Has(service, account string) (bool, error) { return s.key != nil, nil }

// keyOfTheTest is a private key of a test, in the PEM a person downloads from GitHub:
// no test of crewflow signs a token with the key of a real app.
func keyOfTheTest(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate a key of the test: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal the key of the test: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}
