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
// do — and it says all of it without a token of an hour, because a report is not a
// run and a token it minted for a line of text would be a token in the memory of a
// command that only prints (§7e, §7i).
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
	// The report of a machine is not a run: it holds no token, and it never asks for
	// one. A test that asked would be a test of somebody else's account.
	for _, path := range m.api.asked {
		if strings.HasSuffix(path, "/access_tokens") {
			t.Errorf("the report asked for a token at %q, want no token of a report", path)
		}
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
	m.api.answer["/api/v3/repos/naghuale/crewflow/installation"] = map[string]any{
		"id": 12345, "app_id": 5107052,
		"permissions": map[string]any{
			"contents": "write", "pull_requests": "write",
			"issues": "read", "metadata": "read", "workflows": "write",
		},
	}
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
		"/api/v3/repos/naghuale/crewflow/installation": map[string]any{
			"id": 12345, "app_id": 5107052,
			"permissions": map[string]any{
				"contents": "write", "pull_requests": "write",
				"issues": "read", "metadata": "read",
			},
		},
	}}
	api.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.asked = append(api.asked, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		answer, ok := api.answer[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"message": "Not Found: " + r.URL.Path})
			return
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
	asked  []string
	server *httptest.Server
}

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
