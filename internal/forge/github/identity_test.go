package github

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/secret"
)

// TestExecutorIdentityOfTheOwnerNamesTheLogin: the mode of the owner is the login of
// the person, and a report that says the powers of a run are shared has to say whose
// they are — a person reading it must not have to go and look for the account.
func TestExecutorIdentityOfTheOwnerNamesTheLogin(t *testing.T) {
	m := newMachine().prints("auth status", "github.com\n  ✓ Logged in to github.com account naghuale (keyring)\n  - Token: gho_************\n")
	a := New(repo, "", m.env(t))

	identity, err := a.ExecutorIdentity(t.Context())
	if err != nil {
		t.Fatalf("ExecutorIdentity returned an error: %v", err)
	}

	if identity.Mode != forge.ModeOwner {
		t.Errorf("the mode = %q, want %q", identity.Mode, forge.ModeOwner)
	}
	if !strings.Contains(identity.Description, "naghuale") {
		t.Errorf("the description is %q, want it to name the account gh is signed in as", identity.Description)
	}
	if !strings.Contains(identity.Description, "shared rights") {
		t.Errorf("the description is %q, want it to say the powers of a run are the powers of the login", identity.Description)
	}
	// A run in the mode of the owner is handed nothing: the login of the person is
	// what the executor of it already has, and a token in its environment would be a
	// token nobody asked for (docs/DESIGN.md §7i).
	if len(identity.Env) > 0 || len(identity.GitConfig) > 0 || len(identity.Secrets) > 0 {
		t.Errorf("the identity of a run of the owner is %+v, want it empty of rights", identity)
	}
}

// TestExecutorIdentityOfTheOwnerWithoutAWorkingGhStillNamesSomebody: gh that cannot
// be asked is a check of doctor and not the end of a run: a run in the mode of the
// owner is what crewflow has always done, and an adapter that cannot say who is signed
// in does not stop a task over it (docs/DESIGN.md §7g).
func TestExecutorIdentityOfTheOwnerWithoutAWorkingGhStillNamesSomebody(t *testing.T) {
	m := newMachine()
	m.fails("auth status", "gh: command not found")
	a := New(repo, "", m.env(t))

	identity, err := a.ExecutorIdentity(t.Context())
	if err != nil {
		t.Fatalf("ExecutorIdentity returned an error: %v", err)
	}
	if identity.Mode != forge.ModeOwner {
		t.Errorf("the mode = %q, want %q", identity.Mode, forge.ModeOwner)
	}
	if identity.Description == "" {
		t.Error("the description is empty, want a line a report can show")
	}
}

// TestExecutorIdentityOfTheBotIsTheApp: a run in the mode of the bot is handed a token
// of the App, the name and the address its commits are made by, and the helper git
// takes a fresh token from. All of it comes from the App, and nothing of it from the
// login of the person.
func TestExecutorIdentityOfTheBotIsTheApp(t *testing.T) {
	m, api := machineWithAnApp(t)
	a := New(repo, "", m.env(t)).WithBot(Bot{AppID: 5107052, InstallationID: 12345, API: api.URL()})

	identity, err := a.ExecutorIdentity(t.Context())
	if err != nil {
		t.Fatalf("ExecutorIdentity returned an error: %v", err)
	}

	if identity.Mode != forge.ModeBot {
		t.Errorf("the mode = %q, want %q", identity.Mode, forge.ModeBot)
	}
	if want := "bot — GitHub App crewflow-executor (installation 12345)"; identity.Description != want {
		t.Errorf("the description = %q, want %q", identity.Description, want)
	}
	for _, want := range []string{
		"GH_TOKEN=ghs_token_of_the_run",
		"GIT_AUTHOR_NAME=crewflow-executor[bot]",
		"GIT_AUTHOR_EMAIL=1987+crewflow-executor[bot]@users.noreply.github.com",
		"GIT_COMMITTER_NAME=crewflow-executor[bot]",
		"GIT_COMMITTER_EMAIL=1987+crewflow-executor[bot]@users.noreply.github.com",
	} {
		if !slices.Contains(identity.Env, want) {
			t.Errorf("the environment of the executor is %v, want %q in it", identity.Env, want)
		}
	}
	assertHelperOfThisBuild(t, identity.GitConfig, "executor")
	// The token is in the environment of the executor and nowhere else, and its value
	// is among the values a journal is put through: an agent that prints its own
	// environment is one line of a journal that would carry a token of an hour into a
	// file kept for ever (docs/DESIGN.md §7e).
	if !slices.Contains(identity.Secrets, "ghs_token_of_the_run") {
		t.Errorf("the secrets of the run are %v, want the token of the app in them", identity.Secrets)
	}
}

// TestExecutorIdentityOfTheBotWithoutAKeySaysWhatToDo: a run in the mode of the bot
// that has no key cannot start, and the owner is told which command makes it possible.
func TestExecutorIdentityOfTheBotWithoutAKeySaysWhatToDo(t *testing.T) {
	m, api := machineWithAnApp(t)
	m.secrets = &storeOfTheTest{}
	a := New(repo, "", m.env(t)).WithBot(Bot{AppID: 5107052, API: api.URL()})

	_, err := a.ExecutorIdentity(t.Context())
	if err == nil {
		t.Fatal("ExecutorIdentity returned no error, want the key of the app to be missing")
	}
	if !strings.Contains(err.Error(), "crewflow auth app import") {
		t.Errorf("ExecutorIdentity = %v, want it to name the command that imports the key", err)
	}
}

// TestOpenChangeRequestSignsATokenOfItsOwn: a run asks for this because the token it
// had is over, and a request opened in the login of the person would be a request the
// gate of a review cannot tell from one of the owner (docs/DESIGN.md §7h, §7i).
func TestOpenChangeRequestSignsATokenOfItsOwn(t *testing.T) {
	m, api := machineWithAnApp(t)
	m.prints("pr create", "https://github.com/naghuale/crewflow/pull/44\n")
	m.prints("pr list", `[{"number":44,"url":"https://github.com/naghuale/crewflow/pull/44",`+
		`"headRefName":"crewflow/43-the-run","headRefOid":"9f1c0de","baseRefName":"main",`+
		`"state":"OPEN","body":"Closes #43"}]`)
	a := New(repo, "", m.env(t)).WithBot(Bot{AppID: 5107052, InstallationID: 12345, API: api.URL(), DefaultBranch: "main"})

	opened, err := a.OpenChangeRequest(t.Context(), "crewflow/43-the-run", "the title", "Closes #43")
	if err != nil {
		t.Fatalf("OpenChangeRequest returned an error: %v", err)
	}
	if opened.HeadBranch != "crewflow/43-the-run" || opened.Number != 44 {
		t.Errorf("the request that was opened is %+v, want the one of the branch of the run", opened)
	}
	create, ok := m.commandOf("pr create")
	if !ok {
		t.Fatalf("gh was asked %v, want it to open the request", m.ran)
	}
	for _, want := range []string{"--head crewflow/43-the-run", "--base main", "--title the title", "--body Closes #43"} {
		if !strings.Contains(create, want) {
			t.Errorf("gh was asked %q, want it to say %q", create, want)
		}
	}
	// The token of the request is a new one, and it is in the environment of the gh
	// that opens it and nowhere else.
	token := ""
	for _, name := range m.environment {
		for _, entry := range name {
			if value, ok := strings.CutPrefix(entry, "GH_TOKEN="); ok {
				token = value
			}
		}
	}
	if token != "ghs_token_of_the_run" {
		t.Errorf("gh was asked with the token %q, want the token of the app", token)
	}
}

// TestOpenChangeRequestOfAProjectWithoutAnAppRefuses: a project in the mode of the
// owner opens nothing for itself, and a run of it is a run whose outcome is
// no-change-request whatever the branch holds (§7i).
func TestOpenChangeRequestOfAProjectWithoutAnAppRefuses(t *testing.T) {
	m := newMachine()
	a := New(repo, "", m.env(t))

	_, err := a.OpenChangeRequest(t.Context(), "crewflow/43-the-run", "the title", "Closes #43")
	if err == nil {
		t.Fatal("OpenChangeRequest returned no error, want the refusal of a project without an app")
	}
	if len(m.ran) > 0 {
		t.Errorf("gh was asked %v, want nothing: this project has no account of its own", m.ran)
	}
}

// TestDoctorOfTheBotLooksForTheKeyAndTheInstallation: a run in the mode of the bot
// that is not set up cannot start, and `crewflow doctor` is where a person finds that
// out — before a task, and not in the middle of one (docs/DESIGN.md §7i).
func TestDoctorOfTheBotLooksForTheKeyAndTheInstallation(t *testing.T) {
	cases := []struct {
		name string
		// rights is what the API of the test answers as the rights of the installation
		// of the app, and key is whether the store of the machine holds its key.
		rights map[string]any
		key    bool
		want   []struct {
			name   string
			status forge.Status
		}
	}{
		{
			name:   "the app is installed with the rights of the design",
			rights: rightsOfTheDesign(),
			key:    true,
			want: []struct {
				name   string
				status forge.Status
			}{
				{"gh", forge.OK},
				{"gh login", forge.OK},
				{appKeyCheck, forge.OK},
				{appCheck, forge.OK},
				{appTokenCheck, forge.OK},
			},
		},
		{
			name: "the key was never imported",
			want: []struct {
				name   string
				status forge.Status
			}{
				{"gh", forge.OK},
				{"gh login", forge.OK},
				{appKeyCheck, forge.Fail},
			},
		},
		{
			name: "the app may do more than a run needs",
			rights: map[string]any{
				"contents": "write", "pull_requests": "write",
				"issues": "read", "metadata": "read", "workflows": "write",
			},
			key: true,
			want: []struct {
				name   string
				status forge.Status
			}{
				{"gh", forge.OK},
				{"gh login", forge.OK},
				{appKeyCheck, forge.OK},
				{appCheck, forge.Fail},
				{appTokenCheck, forge.OK},
			},
		},
		{
			name: "a right of the design is wider than a run needs it to be",
			rights: map[string]any{
				"contents": "write", "pull_requests": "write",
				"issues": "write", "metadata": "read",
			},
			key: true,
			want: []struct {
				name   string
				status forge.Status
			}{
				{"gh", forge.OK},
				{"gh login", forge.OK},
				{appKeyCheck, forge.OK},
				{appCheck, forge.Fail},
				{appTokenCheck, forge.OK},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, api := machineWithAnApp(t)
			m.secrets = &storeOfTheTest{}
			if tc.key {
				m.secrets.(*storeOfTheTest).key = keyOfTheTest(t)
			}
			if tc.rights != nil {
				api.installationOfTheTest(tc.rights)
			}
			a := New(repo, "", m.env(t)).WithBot(Bot{AppID: 5107052, InstallationID: 12345, API: api.URL()})

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
			// A report of a machine never leaves a token in a terminal: the key is
			// asked for as a fact and not read, and the token of the last check is
			// asked for and thrown away — no command of the report is started with it.
			for _, command := range m.ran {
				for _, entry := range command.env {
					if strings.Contains(entry, "ghs_token_of_the_run") {
						t.Errorf("the report started a command with a token in it: %q", entry)
					}
				}
			}
			for _, check := range checks {
				if strings.Contains(check.Detail, "ghs_token_of_the_run") || strings.Contains(check.Hint, "ghs_token_of_the_run") {
					t.Errorf("check %q shows the token of the trial: %q", check.Name, check.Detail)
				}
			}
		})
	}
}

// TestDoctorOfTheBotSaysWhatGitHubRefusedForTheToken: a run in the mode of the bot
// cannot start without a token, and GitHub refuses one with words of its own — 422
// when the repository of the project is not a repository of the installation. The
// report says those words and what to do about them, so that the refusal is found out
// before a task and not in the middle of one, and it shows nothing of the value of a
// token: a report is read by people and pasted into issues (docs/DESIGN.md §7e, §7i).
func TestDoctorOfTheBotSaysWhatGitHubRefusedForTheToken(t *testing.T) {
	const said = "There is at least one repository that does not exist " +
		"or is not accessible to the parent installation."
	m, api := machineWithAnApp(t)
	api.answer["/app/installations/12345/access_tokens"] = map[string]any{
		"message": said,
		"errors": []any{map[string]any{
			"resource": "InstallationAccessToken", "code": "custom", "message": said,
		}},
	}
	api.refusesAt("/app/installations/12345/access_tokens", http.StatusUnprocessableEntity)
	a := New(repo, "", m.env(t)).WithBot(Bot{AppID: 5107052, InstallationID: 12345, API: api.URL()})

	checks := a.Doctor(t.Context())

	var found bool
	for _, check := range checks {
		if check.Name != appTokenCheck {
			continue
		}
		found = true
		if check.Status != forge.Fail {
			t.Errorf("check %q = %q (%s), want fail: a run cannot start without a token",
				check.Name, check.Status, check.Detail)
		}
		for _, want := range []string{said, "422", "naghuale/crewflow"} {
			if !strings.Contains(check.Detail, want) {
				t.Errorf("check %q detail = %q, want it to say %q", check.Name, check.Detail, want)
			}
		}
		if check.Hint == "" {
			t.Errorf("check %q is %q and says nothing to do about it", check.Name, check.Status)
		}
	}
	if !found {
		t.Errorf("doctor made the checks %v, want a check of the token of the run", checkNames(checks))
	}
}

// serverOfTheTest is the API of GitHub as a test needs it: it answers for the App, for
// its installation and for the account of the App, and it writes down what it was
// asked, because a test of a token is a test of the question as much as of the answer.
type serverOfTheTest struct {
	// answer is what the server answers, by the path it was asked at.
	answer map[string]any
	// status is the code of the answer of a path, for an answer the API of GitHub
	// refuses with: the code is a part of what a report of a machine has to show.
	status map[string]int
	// asked are the paths of the requests, in order.
	asked []string
	// server is the HTTP the test talks through.
	server *httptest.Server
}

// refusesAt makes the server answer the path with the code, the way the API of GitHub
// refuses a request it does not want to grant.
func (a *serverOfTheTest) refusesAt(path string, code int) {
	a.status[path] = code
}

// URL is the address of the API of the test, which is what the adapter of the project
// is pointed at: a test of a token is a test of a server of the test.
func (a *serverOfTheTest) URL() string { return a.server.URL }

// installationOfTheTest answers for the installation of the App on the repository of
// the project, with the rights given. GitHub answers the question about the repository
// and the question about the number in the file of the project with the same thing, so
// both paths are answered with it — and it answers it in the form the documentation of
// the REST API of GitHub writes it (version 2022-11-28): the account of the owner is an
// object with a name in it and not a name, and the rights are a map of a name to a
// level. The answers in the whole of that form are in the testdata of the package of
// the app, which is the one that reads them.
func (a *serverOfTheTest) installationOfTheTest(rights map[string]any) {
	answer := map[string]any{
		"id": 12345, "app_id": 5107052,
		"account":              map[string]any{"login": "naghuale", "id": 1, "type": "User", "site_admin": false},
		"repository_selection": "selected",
		"permissions":          rights,
	}
	a.answer["/repos/naghuale/crewflow/installation"] = answer
	a.answer["/app/installations/12345"] = answer
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
// rights of §7i in it. Crewflow reads the repositories of the answer to be sure the
// token is for the one repository of a run, and the rights to be sure they are no
// wider than a run needs — so a server of a test that answered with a token and
// nothing else would be a server GitHub never was.
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

// machineWithAnApp is a machine of a test that has an App on it: the store holds a key
// the test generated, and the server of the test answers for the API of GitHub. No
// test of crewflow asks GitHub for a token, and none opens the keychain of a person
// (docs/DESIGN.md §7i).
func machineWithAnApp(t *testing.T) (*machine, *serverOfTheTest) {
	t.Helper()
	api := &serverOfTheTest{answer: map[string]any{}, status: map[string]int{}}
	api.answer["/app"] = map[string]any{"id": 5107052, "slug": "crewflow-executor"}
	api.answer["/users/crewflow-executor[bot]"] = map[string]any{"id": 1987, "login": "crewflow-executor[bot]", "type": "Bot"}
	api.answer["/app/installations/12345/access_tokens"] = tokenOfTheTest()
	api.installationOfTheTest(rightsOfTheDesign())
	api.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	m := newMachine()
	m.prints("pr list", `[]`)
	m.secrets = &storeOfTheTest{key: keyOfTheTest(t)}
	return m, api
}

// storeOfTheTest holds the key a test generated and nothing else: the keychain of
// macOS is never in a test (docs/DESIGN.md §7i).
//
// A project whose executor and whose orchestrator have an App each has two keys, and a
// test that leaves one of them out has to be able to say which: `keys` holds a key per
// app, and where it is there the one `key` holds is the key of every app — which is
// what a test of a project with one App needs.
type storeOfTheTest struct {
	key  []byte
	keys map[string][]byte
}

// Get returns the key of the app, and the error of a store that holds no key of it.
func (s *storeOfTheTest) Get(service, account string) ([]byte, error) {
	if s.keys != nil {
		key, found := s.keys[account]
		if !found {
			return nil, secret.ErrNotFound
		}
		return key, nil
	}
	if s.key == nil {
		return nil, secret.ErrNotFound
	}
	return s.key, nil
}

// Set keeps the key the test is given, under the app it belongs to.
func (s *storeOfTheTest) Set(service, account string, value []byte) error {
	if s.keys != nil {
		s.keys[account] = value
		return nil
	}
	s.key = value
	return nil
}

// Has says whether a key of that app is in the store, as the keychain of macOS can: a
// report asks for that and not for a value.
func (s *storeOfTheTest) Has(service, account string) (bool, error) {
	if s.keys != nil {
		_, found := s.keys[account]
		return found, nil
	}
	return s.key != nil, nil
}

// keyOfTheTest is a private key of a test, in the PEM a person downloads from GitHub:
// no test of crewflow signs a token with the key of a real App.
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
