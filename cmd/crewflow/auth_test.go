package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/secret"
)

// botConfig is the settings of a project whose executor works as an app of its own:
// the mode, and the numbers that say which app it is. The key of the app is not in
// the file and never will be — it is in the store of the machine
// (docs/DESIGN.md §7e, §7i).
const botConfig = `
[identity]
mode = "bot"

[identity.github_app]
app_id = 5107052
installation_id = 12345
`

// TestAuthAppImportPutsTheKeyAwayAndSaysNothingOfIt: the key of an app is the one
// secret an owner hands over once, and nothing of it may be printed — not the key, not
// a part of it. What the command says afterwards is the number of the app and the file
// to delete, because a private key in a folder of downloads is a key anybody on the
// machine can read (docs/DESIGN.md §7e, §7i).
func TestAuthAppImportPutsTheKeyAwayAndSaysNothingOfIt(t *testing.T) {
	store := &storeOfTheTest{}
	useMachineOfTheTest(t, store, nil)
	key := keyOfTheTest(t)
	path := filepath.Join(t.TempDir(), "crewflow-app.pem")
	if err := os.WriteFile(path, key, 0o600); err != nil {
		t.Fatalf("write the key: %v", err)
	}
	project := writeConfig(t, projectConfig+botConfig)
	var stdout, stderr bytes.Buffer

	code := run([]string{"auth", "app", "import", path, "-config", project}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow auth app import = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	kept, err := store.Get(secret.Service, secret.AppKey(5107052))
	if err != nil {
		t.Fatalf("the key is not in the store of the test: %v", err)
	}
	if len(kept) == 0 {
		t.Fatal("the store of the test holds nothing for the app, want the key that was imported")
	}
	for _, said := range []string{stdout.String(), stderr.String()} {
		if strings.Contains(said, "PRIVATE KEY") {
			t.Errorf("a command printed the key of the app:\n%s", said)
		}
	}
	for _, want := range []string{"5107052", path, "delete"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("crewflow auth app import wrote %q, want it to mention %q", stdout.String(), want)
		}
	}
}

// TestAuthAppImportTwiceKeepsTheLastKey: GitHub gives a new key of an app whenever an
// owner asks for one, and the last one imported is the one a run has to sign with —
// a second item under the same name would be a key nothing uses.
func TestAuthAppImportTwiceKeepsTheLastKey(t *testing.T) {
	store := &storeOfTheTest{}
	useMachineOfTheTest(t, store, nil)
	project := writeConfig(t, projectConfig+botConfig)
	folder := t.TempDir()

	for _, name := range []string{"first.pem", "second.pem"} {
		path := filepath.Join(folder, name)
		if err := os.WriteFile(path, keyOfTheTest(t), 0o600); err != nil {
			t.Fatalf("write the key: %v", err)
		}
		var stdout, stderr bytes.Buffer
		if code := run([]string{"auth", "app", "import", path, "-config", project}, &stdout, &stderr); code != exitOK {
			t.Fatalf("crewflow auth app import %s = %d, want %d (stderr: %q)", name, code, exitOK, stderr.String())
		}
	}
	if store.writes != 2 {
		t.Errorf("the store was written %d times, want the second import to write over the first", store.writes)
	}
}

// TestAuthAppImportRefusesWhatIsNotAKey: a person points the import at the wrong file,
// and a store of an app must not hold whatever a path held.
func TestAuthAppImportRefusesWhatIsNotAKey(t *testing.T) {
	store := &storeOfTheTest{}
	useMachineOfTheTest(t, store, nil)
	path := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(path, []byte("the key of the app is somewhere else\n"), 0o600); err != nil {
		t.Fatalf("write the file: %v", err)
	}
	project := writeConfig(t, projectConfig+botConfig)
	var stdout, stderr bytes.Buffer

	code := run([]string{"auth", "app", "import", path, "-config", project}, &stdout, &stderr)

	if code != exitFailure {
		t.Errorf("crewflow auth app import = %d, want %d for a file that is not a key", code, exitFailure)
	}
	if !strings.Contains(stderr.String(), "not in PEM") {
		t.Errorf("crewflow auth app import wrote %q, want it to say what is wrong with the file", stderr.String())
	}
	if store.writes != 0 {
		t.Errorf("the store was written %d times, want nothing written for a file that is not a key", store.writes)
	}
}

// TestAuthAppImportWithoutAnAppInTheFileSaysWhatToDo: a project in the mode of the
// owner has no app, and a key imported for it would be a key of nothing.
func TestAuthAppImportWithoutAnAppInTheFileSaysWhatToDo(t *testing.T) {
	store := &storeOfTheTest{}
	useMachineOfTheTest(t, store, nil)
	path := filepath.Join(t.TempDir(), "crewflow-app.pem")
	if err := os.WriteFile(path, keyOfTheTest(t), 0o600); err != nil {
		t.Fatalf("write the key: %v", err)
	}
	project := writeConfig(t, projectConfig)
	var stdout, stderr bytes.Buffer

	code := run([]string{"auth", "app", "import", path, "-config", project}, &stdout, &stderr)

	if code != exitFailure {
		t.Fatalf("crewflow auth app import = %d, want %d for a project with no app", code, exitFailure)
	}
	if !strings.Contains(stderr.String(), "identity.github_app.app_id") {
		t.Errorf("crewflow auth app import wrote %q, want it to name the key to set", stderr.String())
	}
}

// TestAuthAppCheckSaysWhatIsSetUpAndHoldsNoToken: the check a person runs before a
// task says that the key is there, that the app is installed on the repository and
// what it may do — and it says none of it with a token of an hour in its hand, because
// a check has nothing to do with a credential it would throw away
// (docs/DESIGN.md §7e, §7i).
func TestAuthAppCheckSaysWhatIsSetUpAndHoldsNoToken(t *testing.T) {
	store := &storeOfTheTest{key: keyOfTheTest(t)}
	api := useMachineOfTheTest(t, store, nil)
	project := writeConfig(t, projectConfig+botConfig+"\n[forge]\nkind = \"github\"\nhost = \""+hostOfTest(api)+"\"\n")
	var stdout, stderr bytes.Buffer

	code := run([]string{"auth", "app", "check", "-config", project}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow auth app check = %d, want %d (stderr: %q)\n%s", code, exitOK, stderr.String(), stdout.String())
	}
	for _, want := range []string{
		"bot — GitHub App crewflow-executor (installation 12345)",
		"key: there",
		"installation: 12345 on naghuale/crewflow",
		"contents write", "pull_requests write", "issues read",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("crewflow auth app check wrote %q, want it to mention %q", stdout.String(), want)
		}
	}
	if strings.Contains(stdout.String(), "ghs_") {
		t.Errorf("crewflow auth app check printed a token:\n%s", stdout.String())
	}
	for _, path := range api.asked {
		if strings.HasSuffix(path, "/access_tokens") {
			t.Errorf("the check asked for a token at %q, want no token of a check", path)
		}
	}
}

// TestAuthAppCheckSaysWhatIsMissingAndFails: a project in the mode of the bot whose
// key was never imported cannot start a run, and the check says so with the code that
// a script reads.
func TestAuthAppCheckSaysWhatIsMissingAndFails(t *testing.T) {
	store := &storeOfTheTest{}
	api := useMachineOfTheTest(t, store, nil)
	project := writeConfig(t, projectConfig+botConfig+"\n[forge]\nkind = \"github\"\nhost = \""+hostOfTest(api)+"\"\n")
	var stdout, stderr bytes.Buffer

	code := run([]string{"auth", "app", "check", "-config", project}, &stdout, &stderr)

	if code != exitFailure {
		t.Errorf("crewflow auth app check = %d, want %d without a key", code, exitFailure)
	}
	if !strings.Contains(stderr.String(), "crewflow auth app import") {
		t.Errorf("crewflow auth app check wrote %q, want it to name the command that imports the key", stderr.String())
	}
	if !strings.Contains(stdout.String(), "key: not there") {
		t.Errorf("crewflow auth app check wrote %q, want it to say the key is not there", stdout.String())
	}
}

// TestAuthAppCheckRefusesRightsWiderThanARunNeeds: the powers of the executor of every
// run of the project are the rights of the app, and a check that says only what was
// asked for is a check of an intention. A right asked for with a wider level than §7i
// is the same thing said in another way, and it is refused as well.
func TestAuthAppCheckRefusesRightsWiderThanARunNeeds(t *testing.T) {
	cases := []struct {
		name   string
		rights map[string]any
		want   string
	}{
		{
			name: "a right nobody asked for",
			rights: map[string]any{
				"contents": "write", "pull_requests": "write",
				"issues": "read", "metadata": "read", "workflows": "write",
			},
			want: "workflows write",
		},
		{
			name: "a right of the design wider than a run needs it",
			rights: map[string]any{
				"contents": "write", "pull_requests": "write", "issues": "write", "metadata": "read",
			},
			want: "issues write",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &storeOfTheTest{key: keyOfTheTest(t)}
			api := useMachineOfTheTest(t, store, nil)
			api.installationOfTheTest(tc.rights)
			project := writeConfig(t, projectConfig+botConfig+"\n[forge]\nkind = \"github\"\nhost = \""+hostOfTest(api)+"\"\n")
			var stdout, stderr bytes.Buffer

			code := run([]string{"auth", "app", "check", "-config", project}, &stdout, &stderr)

			if code != exitFailure {
				t.Fatalf("crewflow auth app check = %d, want %d for rights wider than a run needs", code, exitFailure)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Errorf("crewflow auth app check wrote %q, want it to name the right that is too wide", stderr.String())
			}
		})
	}
}

// TestAuthAppCheckAsJSON: the answer of a command is what an orchestrator reads, and
// the mode of the executor and the rights of the app are fields of it.
func TestAuthAppCheckAsJSON(t *testing.T) {
	store := &storeOfTheTest{key: keyOfTheTest(t)}
	api := useMachineOfTheTest(t, store, nil)
	project := writeConfig(t, projectConfig+botConfig+"\n[forge]\nkind = \"github\"\nhost = \""+hostOfTest(api)+"\"\n")
	var stdout, stderr bytes.Buffer

	code := run([]string{"auth", "app", "check", "-config", project, "-json"}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow auth app check -json = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	var answer struct {
		Mode         string `json:"mode"`
		Description  string `json:"description"`
		AppID        int64  `json:"app_id"`
		Key          bool   `json:"key"`
		Installation struct {
			ID          int64             `json:"id"`
			Permissions map[string]string `json:"permissions"`
		} `json:"installation"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &answer); err != nil {
		t.Fatalf("crewflow auth app check -json wrote %q, which is not JSON: %v", stdout.String(), err)
	}
	if answer.Mode != "bot" || answer.AppID != 5107052 || !answer.Key {
		t.Errorf("the answer is %+v, want the app of the project and its key", answer)
	}
	if answer.Installation.ID != 12345 || answer.Installation.Permissions["contents"] != "write" {
		t.Errorf("the answer holds the installation %+v, want the one of the repository", answer.Installation)
	}
}

// TestGitCredentialAnswersOnlyThePushOfThisProject: the helper of a worktree is
// started by git for every credential it wants, and it answers for github.com and for
// the repository of this project and for nothing else: a helper that handed a token out
// for another repository would hand it to a program of another project
// (docs/DESIGN.md §7i).
func TestGitCredentialAnswersOnlyThePushOfThisProject(t *testing.T) {
	store := &storeOfTheTest{key: keyOfTheTest(t)}
	api := useMachineOfTheTest(t, store, nil)
	project := writeConfig(t, projectConfig+botConfig+"\n[forge]\nkind = \"github\"\nhost = \""+hostOfTest(api)+"\"\n")
	cases := []struct {
		name    string
		request string
		want    string
	}{
		{
			name:    "the push of this project to its own host",
			request: "protocol=https\nhost=" + hostOfTest(api) + "\npath=naghuale/crewflow\n\n",
			want:    "ghs_token_of_the_run",
		},
		{
			name:    "another repository of the same host",
			request: "protocol=https\nhost=" + hostOfTest(api) + "\npath=naghuale/telecli\n\n",
		},
		{
			name:    "another host",
			request: "protocol=https\nhost=github.com\npath=naghuale/crewflow\n\n",
		},
		{
			name:    "another protocol",
			request: "protocol=ssh\nhost=" + hostOfTest(api) + "\npath=naghuale/crewflow\n\n",
		},
		{
			name:    "an empty request",
			request: "\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			authStdin = strings.NewReader(tc.request)
			t.Cleanup(func() { authStdin = os.Stdin })
			var stdout, stderr bytes.Buffer

			code := run([]string{"auth", "git-credential", "get", "-config", project}, &stdout, &stderr)

			if code != exitOK {
				t.Fatalf("crewflow auth git-credential = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
			}
			if tc.want == "" {
				if stdout.Len() != 0 {
					t.Errorf("the helper wrote %q, want nothing for a request that is not the push of this project", stdout.String())
				}
				return
			}
			if !strings.Contains(stdout.String(), "username=x-access-token") ||
				!strings.Contains(stdout.String(), "password="+tc.want) {
				t.Errorf("the helper wrote %q, want the token of the app as the password", stdout.String())
			}
		})
	}
}

// TestGitCredentialKeepsNothingOfWhatGitSays: `store` and `erase` are notes of git
// about itself, and a helper that wrote them down would be a helper with a memory of
// the credentials of a person.
func TestGitCredentialKeepsNothingOfWhatGitSays(t *testing.T) {
	store := &storeOfTheTest{key: keyOfTheTest(t)}
	useMachineOfTheTest(t, store, nil)
	project := writeConfig(t, projectConfig+botConfig)
	authStdin = strings.NewReader("protocol=https\nhost=github.com\npath=naghuale/crewflow\npassword=the-token-of-a-person\n\n")
	t.Cleanup(func() { authStdin = os.Stdin })
	var stdout, stderr bytes.Buffer

	for _, operation := range []string{"store", "erase"} {
		stdout.Reset()
		stderr.Reset()
		if code := run([]string{"auth", "git-credential", operation, "-config", project}, &stdout, &stderr); code != exitOK {
			t.Errorf("crewflow auth git-credential %s = %d, want %d", operation, code, exitOK)
		}
		if stdout.Len() != 0 {
			t.Errorf("the helper wrote %q for %s, want nothing at all", stdout.String(), operation)
		}
	}
	if strings.Contains(store.string(), "the-token-of-a-person") {
		t.Error("the store of the test holds what git said, want it to keep nothing of it")
	}
}

// TestGitCredentialOfAProjectWithoutABotSaysNothing: a project in the mode of the
// owner pushes with the login of the person, and a helper of an app in the way of that
// login would be a helper crewflow put there for nothing (§7i).
func TestGitCredentialOfAProjectWithoutABotSaysNothing(t *testing.T) {
	store := &storeOfTheTest{key: keyOfTheTest(t)}
	useMachineOfTheTest(t, store, nil)
	project := writeConfig(t, projectConfig)
	authStdin = strings.NewReader("protocol=https\nhost=github.com\npath=naghuale/crewflow\n\n")
	t.Cleanup(func() { authStdin = os.Stdin })
	var stdout, stderr bytes.Buffer

	code := run([]string{"auth", "git-credential", "get", "-config", project}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow auth git-credential = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("the helper wrote %q, want nothing for a project in the mode of the owner", stdout.String())
	}
}

// TestAuthCalledWrong: a command called wrong is a wrong call, and the usage says what
// the right one is.
func TestAuthCalledWrong(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"nothing at all", []string{"auth"}, "nothing to do"},
		{"an unknown subcommand", []string{"auth", "tokens"}, "unknown subcommand"},
		{"nothing to do with the app", []string{"auth", "app"}, "what to do with the app"},
		{"an unknown thing with the app", []string{"auth", "app", "rotate"}, "unknown subcommand"},
		{"an import without a file", []string{"auth", "app", "import"}, "one path"},
		{"a check with an argument", []string{"auth", "app", "check", "43"}, "unexpected argument"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := run(tc.args, &stdout, &stderr)

			if code != exitUsage {
				t.Errorf("run(%v) = %d, want %d", tc.args, code, exitUsage)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Errorf("run(%v) wrote %q, want it to say %q", tc.args, stderr.String(), tc.want)
			}
			if !strings.Contains(stderr.String(), "Usage:") {
				t.Errorf("run(%v) wrote %q, want the usage with it", tc.args, stderr.String())
			}
		})
	}
}

// useMachineOfTheTest points the commands about the key of an app at a store, a server
// and a clock of the test, and puts the machine back when the test is over: no test of
// crewflow opens the keychain of the person who runs it and no test asks GitHub for a
// token of a real app (docs/DESIGN.md §7i).
func useMachineOfTheTest(t *testing.T, store secret.Store, api *apiOfTheTest) *apiOfTheTest {
	t.Helper()
	if api == nil {
		api = newAPIOfTheTest(t)
	}
	oldStore, oldClient, oldNow := secretsOfMachine, httpOfMachine, clockOfMachine
	t.Cleanup(func() { secretsOfMachine, httpOfMachine, clockOfMachine = oldStore, oldClient, oldNow })
	secretsOfMachine = func() secret.Store { return store }
	httpOfMachine = api.server.Client()
	clockOfMachine = func() time.Time { return time.Date(2026, time.September, 28, 12, 0, 0, 0, time.UTC) }
	return api
}

// newAPIOfTheTest is a server of GitHub as a test of a command needs it: the API of a
// project on a host of its own is under https://<host>/api/v3, and the test points the
// host of the project at the address of the server.
func newAPIOfTheTest(t *testing.T) *apiOfTheTest {
	t.Helper()
	api := &apiOfTheTest{answer: map[string]any{
		"/api/v3/app": map[string]any{"id": 5107052, "slug": "crewflow-executor"},
		"/api/v3/app/installations/12345/access_tokens": tokenOfTheTest(),
	}}
	api.installationOfTheTest(rightsOfTheDesign())
	api.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.asked = append(api.asked, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		answer, ok := api.answerFor(r)
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"message": "Not Found: " + r.URL.Path})
			return
		}
		_ = json.NewEncoder(w).Encode(answer)
	}))
	t.Cleanup(api.server.Close)
	return api
}

// apiOfTheTest is the API of GitHub as a test of a command sees it, and hostOfTest is
// the host of a project that points at it.
type apiOfTheTest struct {
	answer map[string]any
	asked  []string
	server *httptest.Server
}

// answerFor is the answer of the server to a request: the one of the app that asked, and
// the answer every app gets otherwise.
//
// The endpoints of an App are the ones every App asks and every App gets its own answer
// to, because the path of the request is the same for all of them and the token of it
// names the App in `iss`. A project with two Apps asks one server both of those
// questions, and a server of a test that answered them with one name would be a server
// with one App on the repository (docs/DESIGN.md §7i).
func (a *apiOfTheTest) answerFor(r *http.Request) (any, bool) {
	if app := appOfTheRequest(r); app != "" {
		if answer, found := a.answer[app+" "+r.URL.Path]; found {
			return answer, true
		}
	}
	answer, found := a.answer[r.URL.Path]
	return answer, found
}

// appOfTheRequest is the app that made a request, out of the token of it: `iss` is the
// number of the App, as the documentation of the endpoint writes it, and a request
// without such a token is a request of a gh and not of an App.
func appOfTheRequest(r *http.Request) string {
	_, token, found := strings.Cut(r.Header.Get("Authorization"), "Bearer ")
	if !found {
		return ""
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Issuer string `json:"iss"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	return claims.Issuer
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
// request for the token of the installation, with the repository of the project and
// the rights of §7i in it. Crewflow reads the repositories of the answer to be sure the
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

func hostOfTest(api *apiOfTheTest) string {
	return strings.TrimPrefix(api.server.URL, "https://")
}

// storeOfTheTest is the store of a test: what is in it, how many times it was written
// and what git said to the helper of the credentials. The keychain of macOS is never in
// a test (docs/DESIGN.md §7i).
type storeOfTheTest struct {
	key    []byte
	writes int
	said   []string
	// account is the name the last write went under, which is how a test of a project
	// with two Apps tells the two keys apart: a project with two apps has two keys, and
	// one item under one name is one of them (§7i).
	account string
}

// Get returns the key of the test, and the error of an empty store when it has none.
func (s *storeOfTheTest) Get(service, account string) ([]byte, error) {
	if s.key == nil {
		return nil, secret.ErrNotFound
	}
	return s.key, nil
}

// Set keeps the key the test is given, under the name it was given for.
func (s *storeOfTheTest) Set(service, account string, value []byte) error {
	s.writes++
	s.key, s.account = value, account
	return nil
}

// Has says whether a key is in the store, as the keychain of macOS can: a check asks
// for that and not for a value.
func (s *storeOfTheTest) Has(service, account string) (bool, error) { return s.key != nil, nil }

// string is everything that was kept, as a string, for a test that looks at it.
func (s *storeOfTheTest) string() string { return string(s.key) + strings.Join(s.said, "\n") }

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

// TestGitCredentialOfThePushOfAMergeSignsTheTokenOfTheOrchestrator: the push of a merge
// is made as the account of the app of the orchestrator, and the helper git takes the
// credentials of that push from signs the token of that app — the second one of the
// project, with the rights of the orchestrator in it, and not the one of the executor a
// run signs with (§7h, §7i).
func TestGitCredentialOfThePushOfAMergeSignsTheTokenOfTheOrchestrator(t *testing.T) {
	store := &storeOfTheTest{key: keyOfTheTest(t)}
	api := useMachineOfTheTest(t, store, nil)
	api.answer["/api/v3/app"] = map[string]any{"id": 5107053, "slug": "crewflow-orchestrator"}
	api.installationOfTheOrchestratorOfTheTest()
	project := writeConfig(t, projectConfig+botConfig+orchestratorConfig+
		"\n[forge]\nkind = \"github\"\nhost = \""+hostOfTest(api)+"\"\n")
	authStdin = strings.NewReader("protocol=https\nhost=" + hostOfTest(api) + "\npath=naghuale/crewflow\n\n")
	t.Cleanup(func() { authStdin = os.Stdin })
	var stdout, stderr bytes.Buffer

	code := run([]string{"auth", "git-credential", "get", "-as", "orchestrator", "-config", project}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow auth git-credential -as orchestrator = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	if !strings.Contains(stdout.String(), "password=ghs_token_of_the_orchestrator") {
		t.Errorf("the helper wrote %q, want the token of the app of the orchestrator", stdout.String())
	}
	if strings.Contains(stdout.String(), "ghs_token_of_the_run") {
		t.Errorf("the helper wrote %q, want the token of the second app and not the one of a run", stdout.String())
	}
}

// TestGitCredentialWithoutAnAccountToSignForSaysNothing: a project whose orchestrator
// shares the login of the person has no app for it, and a helper in the way of the push
// of a person would be a helper crewflow put there for nothing (§7i).
func TestGitCredentialWithoutAnAccountToSignForSaysNothing(t *testing.T) {
	store := &storeOfTheTest{key: keyOfTheTest(t)}
	api := useMachineOfTheTest(t, store, nil)
	project := writeConfig(t, projectConfig+botConfig+"\n[forge]\nkind = \"github\"\nhost = \""+hostOfTest(api)+"\"\n")
	authStdin = strings.NewReader("protocol=https\nhost=" + hostOfTest(api) + "\npath=naghuale/crewflow\n\n")
	t.Cleanup(func() { authStdin = os.Stdin })
	var stdout, stderr bytes.Buffer

	code := run([]string{"auth", "git-credential", "get", "-as", "orchestrator", "-config", project}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow auth git-credential -as orchestrator = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("the helper wrote %q, want nothing for a project whose orchestrator is the login of a person", stdout.String())
	}
}

// TestGitCredentialCalledWithAnAccountCrewflowDoesNotHave: the helper signs a token for
// the two subjects of a project and for no third, and a word it does not know is a wrong
// call and says what the right one is (§6, §7i).
func TestGitCredentialCalledWithAnAccountCrewflowDoesNotHave(t *testing.T) {
	useMachineOfTheTest(t, &storeOfTheTest{key: keyOfTheTest(t)}, nil)
	project := writeConfig(t, projectConfig+botConfig)
	authStdin = strings.NewReader("\n")
	t.Cleanup(func() { authStdin = os.Stdin })
	var stdout, stderr bytes.Buffer

	code := run([]string{"auth", "git-credential", "get", "-as", "owner", "-config", project}, &stdout, &stderr)

	if code != exitUsage {
		t.Errorf("crewflow auth git-credential -as owner = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr.String(), "executor or orchestrator") {
		t.Errorf("crewflow auth git-credential wrote %q, want it to say whose account it signs for", stderr.String())
	}
}

// installationOfTheOrchestratorOfTheTest answers for the installation of the app of the
// orchestrator on the repository of the project, with the rights of the orchestrator in
// it, and hands out the token of the documentation for it: the same answer as for the app
// of the executor with the issues in write (docs/DESIGN.md §7h, §7i).
func (a *apiOfTheTest) installationOfTheOrchestratorOfTheTest() {
	rights := map[string]any{
		"contents": "write", "pull_requests": "write", "issues": "write", "metadata": "read",
	}
	answer := map[string]any{
		"id": 12346, "app_id": 5107053,
		"account":              map[string]any{"login": "naghuale", "id": 1, "type": "User", "site_admin": false},
		"repository_selection": "selected",
		"permissions":          rights,
	}
	a.answer["5107053 /api/v3/repos/naghuale/crewflow/installation"] = answer
	a.answer["/api/v3/app/installations/12346"] = answer
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

// TestAuthAppImportOfTheSecondAppPutsItsOwnKeyAway: a project with two Apps has two
// keys, and the store of the machine keeps each of them under the name of its own app —
// `github-app-<app_id>`. One import of one key under one name is one of the two, and the
// person who imported it has to know which (docs/DESIGN.md §7i).
func TestAuthAppImportOfTheSecondAppPutsItsOwnKeyAway(t *testing.T) {
	store := &storeOfTheTest{}
	useMachineOfTheTest(t, store, nil)
	path := filepath.Join(t.TempDir(), "crewflow-orchestrator.pem")
	if err := os.WriteFile(path, keyOfTheTest(t), 0o600); err != nil {
		t.Fatalf("write the key: %v", err)
	}
	project := writeConfig(t, projectConfig+botConfig+orchestratorConfig)
	var stdout, stderr bytes.Buffer

	code := run([]string{"auth", "app", "import", path, "-as", "orchestrator", "-config", project}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow auth app import -as orchestrator = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	if want := secret.AppKey(5107053); store.account != want {
		t.Errorf("the key was kept under %q, want %q", store.account, want)
	}
	if !strings.Contains(stdout.String(), "5107053") {
		t.Errorf("crewflow auth app import wrote %q, want it to name the app the key is of", stdout.String())
	}
}

// TestAuthAppImportOfAnAppTheProjectHasNotNamedSaysWhatToDo: a project in the shared mode
// has no app of the orchestrator, and a key put away for one would be a key of nothing
// (docs/DESIGN.md §7i).
func TestAuthAppImportOfAnAppTheProjectHasNotNamedSaysWhatToDo(t *testing.T) {
	store := &storeOfTheTest{}
	useMachineOfTheTest(t, store, nil)
	path := filepath.Join(t.TempDir(), "crewflow-orchestrator.pem")
	if err := os.WriteFile(path, keyOfTheTest(t), 0o600); err != nil {
		t.Fatalf("write the key: %v", err)
	}
	project := writeConfig(t, projectConfig+botConfig)
	var stdout, stderr bytes.Buffer

	code := run([]string{"auth", "app", "import", path, "-as", "orchestrator", "-config", project}, &stdout, &stderr)

	if code != exitFailure {
		t.Errorf("crewflow auth app import -as orchestrator = %d, want %d for a project with no second app", code, exitFailure)
	}
	if !strings.Contains(stderr.String(), "orchestrator") {
		t.Errorf("crewflow auth app import wrote %q, want it to name the mode of the orchestrator", stderr.String())
	}
	if store.writes != 0 {
		t.Errorf("the store was written %d times, want nothing written for an app nobody named", store.writes)
	}
}

// TestAuthAppCheckOfTheSecondAppNamesTheAccountOfTheOrchestrator: a check of the app of
// the orchestrator says the orchestrator and not "executor", and it reads the rights of
// the installation against the rights of the orchestrator — `issues write` is what that
// account is asked for, and a report that called it too wide would send a person to
// change a setting that is right (docs/DESIGN.md §7h, §7i).
func TestAuthAppCheckOfTheSecondAppNamesTheAccountOfTheOrchestrator(t *testing.T) {
	store := &storeOfTheTest{key: keyOfTheTest(t)}
	api := useMachineOfTheTest(t, store, nil)
	api.answer["/api/v3/app"] = map[string]any{"id": 5107053, "slug": "crewflow-orchestrator"}
	api.installationOfTheOrchestratorOfTheTest()
	project := writeConfig(t, projectConfig+botConfig+orchestratorConfig+
		"\n[forge]\nkind = \"github\"\nhost = \""+hostOfTest(api)+"\"\n")
	var stdout, stderr bytes.Buffer

	code := run([]string{"auth", "app", "check", "-as", "orchestrator", "-config", project}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow auth app check -as orchestrator = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	for _, want := range []string{"orchestrator: separate — GitHub App crewflow-orchestrator", "12346", "issues write"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("crewflow auth app check wrote:\n%s\nwant it to mention %q", stdout.String(), want)
		}
	}
	// The token of the installation is asked for and thrown away, and nothing of it is
	// shown: a report is read by people and pasted into issues (docs/DESIGN.md §7e).
	if strings.Contains(stdout.String(), "ghs_token_of_the_orchestrator") {
		t.Errorf("crewflow auth app check wrote:\n%s\nwant no token of the app in it", stdout.String())
	}
}
