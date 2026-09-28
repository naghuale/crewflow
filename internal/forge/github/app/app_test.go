package app

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// readBody is what a request of the server of a test carries.
func readBody(r *http.Request) (string, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// TestInstallationIsTheAnswerOfTheDocumentation: the answers of both endpoints of an
// installation are the same answer, and the fields of it are read as the documentation
// of the REST API of GitHub (version 2022-11-28) writes them — the account of the
// owner is an object with a name in it and not a name, the number is a number, and the
// rights are a map of a name to a level. An answer crewflow cannot read that way is an
// answer that stops `crewflow auth app check` and `crewflow doctor` for every run of
// the project (docs/DESIGN.md §7i).
func TestInstallationIsTheAnswerOfTheDocumentation(t *testing.T) {
	cases := []struct {
		name string
		// named is whether the file of the project names the installation: the number
		// in it is what a token of a run is asked of, and the whole of that
		// installation is asked for as well.
		named bool
		want  string
	}{
		{
			name:  "the installation the file of the project names",
			named: true,
			want:  "/app/installations/165781718",
		},
		{
			name: "the installation found through the repository",
			want: "/repos/naghuale/crewflow/installation",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source, asked := sourceOfTheDocumentation(t)
			if !tc.named {
				source.InstallationID = 0
			}

			installation, err := source.Installation(t.Context())
			if err != nil {
				t.Fatalf("Installation: %v", err)
			}

			if !contains(asked.paths, tc.want) {
				t.Errorf("the installation was asked for at %v, want %q among them", asked.paths, tc.want)
			}
			if installation.ID != 165781718 || installation.AppID != 5107052 {
				t.Errorf("the installation is %+v, want the installation %d of the app %d of the project",
					installation, 165781718, 5107052)
			}
			// The account of the owner is an object and not a name: this is the field
			// that stopped every check of a project whose App is installed, and the
			// value of it is the one the documentation puts there.
			if installation.Account.Login != "naghuale" || installation.Account.ID != 1 ||
				installation.Account.Type != "User" {
				t.Errorf("the account of the installation is %+v, want the owner of the project as an object",
					installation.Account)
			}
		})
	}
}

// TestInstallationHoldsTheRightsOfTheAnswerOfTheDocumentation: the rights of an
// installation are what a report shows about the powers of the executor, and they are
// read as the map of a name to a level the documentation writes them as.
func TestInstallationHoldsTheRightsOfTheAnswerOfTheDocumentation(t *testing.T) {
	source, _ := sourceOfTheDocumentation(t)

	installation, err := source.Installation(t.Context())
	if err != nil {
		t.Fatalf("Installation: %v", err)
	}

	want := []string{"contents write", "issues read", "metadata read", "pull_requests write"}
	granted := installation.Granted()
	if len(granted) != len(want) {
		t.Fatalf("the rights of the installation are %v, want %v", granted, want)
	}
	for i := range want {
		if granted[i] != want[i] {
			t.Errorf("the rights of the installation are %v, want %v", granted, want)
			break
		}
	}
	if beyond := installation.BeyondARun(); len(beyond) > 0 {
		t.Errorf("the rights beyond a run are %v, want none: these are the rights of §7i", beyond)
	}
}

// TestInstallationOfTheRepositoryAndOfTheFileAreOneInstallation: a run is handed a
// token of the number in the file of the project, and a check that showed the rights
// of another installation would be a report of a machine nobody runs on. When the two
// are not one, the check says which is which and what to do about it.
func TestInstallationOfTheRepositoryAndOfTheFileAreOneInstallation(t *testing.T) {
	source := sourceOfTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/naghuale/crewflow/installation" {
			answerOfTest(t, w, map[string]any{"id": 1, "app_id": 5107052})
			return
		}
		answerOfTest(t, w, map[string]any{"id": 165781718, "app_id": 5107052})
	})

	_, err := source.Installation(t.Context())
	if err == nil {
		t.Fatal("Installation returned no error, want the two installations to be told apart")
	}
	for _, want := range []string{"installation 165781718", "as 1:", "installation_id"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Installation = %v, want it to mention %q", err, want)
		}
	}
}

// TestInstallationOfAnAnswerOfAnotherShapeNamesTheFieldAndWhatToDo: an answer in which
// the account is a name and not an object is an answer of an API of another shape or a
// shape of an invention, and the person who reads the report has to be told which
// field it is and what is to be done about it — a run of the project does not start
// until somebody does.
func TestInstallationOfAnAnswerOfAnotherShapeNamesTheFieldAndWhatToDo(t *testing.T) {
	cases := []struct {
		name   string
		answer map[string]any
		want   string
	}{
		{
			name: "the account of the owner is a name and not an object",
			answer: map[string]any{
				"id": 165781718, "app_id": 5107052, "account": "naghuale",
			},
			want: "account",
		},
		{
			name: "the rights are names and not levels",
			answer: map[string]any{
				"id": 165781718, "app_id": 5107052,
				"permissions": map[string]any{"contents": true},
			},
			want: "permissions",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := sourceOfTest(t, func(w http.ResponseWriter, r *http.Request) {
				answerOfTest(t, w, tc.answer)
			})

			_, err := source.Installation(t.Context())
			if err == nil {
				t.Fatal("Installation returned no error, want the answer to be refused")
			}
			for _, want := range []string{tc.want, apiVersion, "owner of the project"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("Installation = %v, want it to mention %q", err, want)
				}
			}
		})
	}
}

// TestInstallationBeyondARunIsEveryRightThatIsNotTheOneOfARun: the powers of the
// executor of every run of the project are the rights of its installation, and §7i
// names them one by one. A right crewflow did not ask for and a right asked for with a
// wider level are both more than a run needs, and both are the same thing said in a
// report a person has to act on.
func TestInstallationBeyondARunIsEveryRightThatIsNotTheOneOfARun(t *testing.T) {
	cases := []struct {
		name   string
		rights map[string]string
		want   []string
	}{
		{
			name: "the rights of the design and nothing else",
			rights: map[string]string{
				"contents": "write", "pull_requests": "write", "issues": "read", "metadata": "read",
			},
		},
		{
			name: "a right of the design with a wider level",
			rights: map[string]string{
				"contents": "write", "pull_requests": "write", "issues": "write", "metadata": "read",
			},
			want: []string{"issues write"},
		},
		{
			name: "a right nobody asked for",
			rights: map[string]string{
				"contents": "write", "pull_requests": "write", "issues": "read",
				"metadata": "read", "workflows": "write",
			},
			want: []string{"workflows write"},
		},
		{
			name: "a level of an administration of the repository",
			rights: map[string]string{
				"contents": "admin", "pull_requests": "write", "issues": "read", "metadata": "read",
			},
			want: []string{"contents admin"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			installation := Installation{Permissions: tc.rights}

			beyond := installation.BeyondARun()

			if len(beyond) != len(tc.want) {
				t.Fatalf("the rights beyond a run are %v, want %v", beyond, tc.want)
			}
			for i := range tc.want {
				if beyond[i] != tc.want[i] {
					t.Errorf("the rights beyond a run are %v, want %v", beyond, tc.want)
					break
				}
			}
		})
	}
}

// TestTheAnswerOfTheDocumentationWithARightTooManyNamesIt: the answer of the
// documentation of an App somebody gave one right too many to is read whole, and the
// right is named — a report that said only what it asked for would be a report of an
// intention, and the executor of every run of the project would hold the right.
func TestTheAnswerOfTheDocumentationWithARightTooManyNamesIt(t *testing.T) {
	source := sourceOfTest(t, func(w http.ResponseWriter, r *http.Request) {
		writeAnswerOfTheTest(t, w, "installation-wider.json")
	})

	installation, err := source.Installation(t.Context())
	if err != nil {
		t.Fatalf("Installation: %v", err)
	}

	beyond := installation.BeyondARun()
	if len(beyond) != 1 || beyond[0] != "workflows write" {
		t.Errorf("the rights beyond a run are %v, want the one that is too wide", beyond)
	}
}

// TestSourceTokenIsTheAnswerOfTheDocumentation: the answer of a token holds the
// repository it is for and the rights it was given, and crewflow reads the token and
// the hour it is good until out of it and nothing else — the rest of the answer is
// there to be ignored and not to be guessed at.
func TestSourceTokenIsTheAnswerOfTheDocumentation(t *testing.T) {
	source, asked := sourceOfTheDocumentation(t)

	token, err := source.Token(t.Context())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}

	if want := "/app/installations/165781718/access_tokens"; asked.path != want {
		t.Errorf("the token was asked for at %q, want %q", asked.path, want)
	}
	if want := "ghs_16C7e42F292c6912E7710c838347Ae178B4a"; token.Value != want {
		t.Errorf("the token is %q, want the token of the answer of the API", token.Value)
	}
	if want := "2016-07-11 22:14:10 +0000 UTC"; token.ExpiresAt.String() != want {
		t.Errorf("the token is good until %s, want %s: the life of it is what the API says",
			token.ExpiresAt, want)
	}
}

// TestSourceTokenAsksForOneRepositoryAndTheRightsOfTheDesign: the token of an
// installation is the whole of the power the executor of a run has on the host, and
// §7i names its repository and its rights one by one. A token asked for with more of
// either is a token the executor may use on a day it is not wanted.
func TestSourceTokenAsksForOneRepositoryAndTheRightsOfTheDesign(t *testing.T) {
	var asked asked
	source := sourceOfTest(t, func(w http.ResponseWriter, r *http.Request) {
		asked.of(t, r)
		answerOfTest(t, w, map[string]any{
			"token":      "ghs_16C7e42F292c6912E7710c838347Ae178B4a",
			"expires_at": "2026-09-28T13:00:00Z",
		})
	})

	token, err := source.Token(t.Context())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}

	if asked.method != http.MethodPost {
		t.Errorf("the token was asked for with %s, want POST", asked.method)
	}
	if want := "/app/installations/165781718/access_tokens"; asked.path != want {
		t.Errorf("the token was asked for at %q, want %q", asked.path, want)
	}
	if !strings.HasPrefix(asked.authorization, "Bearer ") || asked.authorization == "Bearer " {
		t.Errorf("the request was made with %q, want a bearer token of the app", asked.authorization)
	}
	if !strings.Contains(asked.accept, "github+json") {
		t.Errorf("the request asked for %q, want the JSON of the API of GitHub", asked.accept)
	}
	repositories, _ := asked.body["repositories"].([]any)
	if len(repositories) != 1 || repositories[0] != "naghuale/crewflow" {
		t.Errorf("the token was asked for the repositories %v, want one: the repository of the project", repositories)
	}
	permissions, _ := asked.body["permissions"].(map[string]any)
	want := map[string]any{"contents": "write", "pull_requests": "write", "issues": "read", "metadata": "read"}
	for name, level := range want {
		if permissions[name] != level {
			t.Errorf("the token was asked for %s %v, want %q", name, permissions[name], level)
		}
	}
	if len(permissions) != len(want) {
		t.Errorf("the token was asked for the rights %v, want only %v", permissions, want)
	}
	if token.Value != "ghs_16C7e42F292c6912E7710c838347Ae178B4a" {
		t.Errorf("the token is %q, want what the answer of the API holds", token.Value)
	}
	if want := "2026-09-28 13:00:00 +0000 UTC"; token.ExpiresAt.String() != want {
		t.Errorf("the token is good until %s, want %s: the life of it is what the API says", token.ExpiresAt, want)
	}
}

// TestSourceTokenLooksTheInstallationUpWhenTheFileHasNone: the number of the
// installation is in the file of the project when the owner wrote it there, and it is
// found through the repository when he did not. The call is made with the key of the
// App, because there is no installation to ask with before one is found.
func TestSourceTokenLooksTheInstallationUpWhenTheFileHasNone(t *testing.T) {
	var asked asked
	source := sourceOfTest(t, func(w http.ResponseWriter, r *http.Request) {
		asked.of(t, r)
		answerOfTest(t, w, map[string]any{"id": 777, "app_id": 5107052})
	})
	source.InstallationID = 0

	installation, err := source.Installation(t.Context())
	if err != nil {
		t.Fatalf("Installation: %v", err)
	}
	if want := "/repos/naghuale/crewflow/installation"; asked.path != want {
		t.Errorf("the installation was asked for at %q, want %q", asked.path, want)
	}
	if installation.ID != 777 || installation.AppID != 5107052 {
		t.Errorf("the installation is %+v, want the one of the repository of the project", installation)
	}
}

// TestSourceTokenUsesTheInstallationItFound: a run that pushes with a token of an
// installation nobody named has to be a token of the installation of this
// repository, and not of the first one the App happens to have.
func TestSourceTokenUsesTheInstallationItFound(t *testing.T) {
	paths := []string{}
	source := sourceOfTest(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/repos/naghuale/crewflow/installation":
			answerOfTest(t, w, map[string]any{"id": 777})
		default:
			answerOfTest(t, w, map[string]any{"token": "ghs_key", "expires_at": "2026-09-28T13:00:00Z"})
		}
	})
	source.InstallationID = 0

	if _, err := source.Token(t.Context()); err != nil {
		t.Fatalf("Token: %v", err)
	}
	if want := "/app/installations/777/access_tokens"; paths[len(paths)-1] != want {
		t.Errorf("the token was asked for at %q, want %q", paths[len(paths)-1], want)
	}
}

// TestSourceTokenWithoutAKeySaysWhatToDoAboutIt: a run in the mode of the bot needs a
// token, and a token needs the key of the App. The error names the command that
// imports it, because a report of a run that was refused is read by a person who has
// to decide what to do, and the API was never asked.
func TestSourceTokenWithoutAKeySaysWhatToDoAboutIt(t *testing.T) {
	source := sourceOfTest(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the API was asked at %q, want nothing: there is no key to ask with", r.URL.Path)
	})
	source.Store = &storeOfTest{}

	_, err := source.Token(t.Context())
	if !errors.Is(err, ErrNoKey) {
		t.Errorf("Token = %v, want %v", err, ErrNoKey)
	}
	if !strings.Contains(err.Error(), "crewflow auth app import") {
		t.Errorf("Token = %v, want it to name the command that imports the key", err)
	}
}

// TestSourceTokenSaysWhatTheAPIRefused: GitHub refuses a token of an App that is not
// installed on the repository with a message of its own, and that message is the only
// thing a person can act on.
func TestSourceTokenSaysWhatTheAPIRefused(t *testing.T) {
	source := sourceOfTest(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		answerOfTest(t, w, map[string]any{"message": "Installation not found"})
	})

	_, err := source.Token(t.Context())
	if err == nil {
		t.Fatal("Token returned no error, want what the API refused")
	}
	for _, want := range []string{"Installation not found", "5107052", "naghuale/crewflow"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Token = %v, want it to mention %q", err, want)
		}
	}
}

// TestSourceTokenWithoutATokenInTheAnswerSaysSo: an answer of the API with a code of
// a success and no token in it is not a token, and a run that went on with it would
// have pushed with nothing.
func TestSourceTokenWithoutATokenInTheAnswerSaysSo(t *testing.T) {
	source := sourceOfTest(t, func(w http.ResponseWriter, r *http.Request) {
		answerOfTest(t, w, map[string]any{"expires_at": "2026-09-28T13:00:00Z"})
	})

	_, err := source.Token(t.Context())
	if err == nil {
		t.Fatal("Token returned no error, want the answer of the API to be refused")
	}
	if !strings.Contains(err.Error(), "no token") {
		t.Errorf("Token = %v, want it to say the answer holds no token", err)
	}
}

// TestSourceBotIsTheAccountCommitsAreMadeBy: the commits of a run are made by the
// account of the App, because that is who the review of a person sees, and the
// address is the one GitHub gives that account so that a commit of a bot is a commit
// of a bot and not a name somebody chose. The answer of `GET /users/{login}` is the
// account of GitHub as it writes every account, and it holds more than this needs.
func TestSourceBotIsTheAccountCommitsAreMadeBy(t *testing.T) {
	var paths []string
	source := sourceOfTest(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case appPath:
			writeAnswerOfTheTest(t, w, "app.json")
		case "/users/crewflow-executor[bot]":
			writeAnswerOfTheTest(t, w, "bot.json")
		default:
			answerOfTest(t, w, map[string]any{"token": "ghs_key", "expires_at": "2026-09-28T13:00:00Z"})
		}
	})

	bot, err := source.Bot(t.Context())
	if err != nil {
		t.Fatalf("Bot: %v", err)
	}
	if want := "crewflow-executor[bot]"; bot.Login != want {
		t.Errorf("the account of the app is %q, want %q", bot.Login, want)
	}
	if want := "1987+crewflow-executor[bot]@users.noreply.github.com"; bot.Email != want {
		t.Errorf("the address of the app is %q, want %q", bot.Email, want)
	}
	if bot.ID != 1987 {
		t.Errorf("the number of the app is %d, want 1987", bot.ID)
	}
	if bot.Type != "Bot" {
		t.Errorf("the answer of the API calls the account %q, want a bot", bot.Type)
	}
	// The account is asked for by its name, and the name comes from the App itself:
	// a run does not have an account of its own, it has the one of the App.
	if want := appPath; paths[0] != want {
		t.Errorf("the app was asked for at %q, want %q", paths[0], want)
	}
}

// TestSourceBotWithoutASlugInTheAnswerSaysSo: the account of an App is found by its
// name, and an answer without a name is not an answer crewflow can go on with.
func TestSourceBotWithoutASlugInTheAnswerSaysSo(t *testing.T) {
	source := sourceOfTest(t, func(w http.ResponseWriter, r *http.Request) {
		answerOfTest(t, w, map[string]any{"id": 1987})
	})

	_, err := source.Bot(t.Context())
	if err == nil {
		t.Fatal("Bot returned no error, want the answer of the API to be refused")
	}
	if !strings.Contains(err.Error(), "slug") {
		t.Errorf("Bot = %v, want it to say what was missing from the answer", err)
	}
}

// TestSourceIdentityIsWhatARunInTheModeOfTheBotIsGiven: the token in the environment
// of the executor, the name and the address its commits are made by, the helper git
// takes a fresh token from, and the values that must not reach a journal. A run is
// judged by the one line of the description, so that a person reading a run knows
// whose name it went under without asking anything. All of it comes out of the answers
// the documentation of the API of GitHub gives.
func TestSourceIdentityIsWhatARunInTheModeOfTheBotIsGiven(t *testing.T) {
	source, _ := sourceOfTheDocumentation(t)

	identity, err := source.Identity(t.Context())
	if err != nil {
		t.Fatalf("Identity: %v", err)
	}
	if identity.Mode != ModeBot {
		t.Errorf("the mode = %q, want %q", identity.Mode, ModeBot)
	}
	if want := "bot — GitHub App crewflow-executor (installation 165781718)"; identity.Description != want {
		t.Errorf("the description = %q, want %q", identity.Description, want)
	}
	for _, want := range []string{
		"GH_TOKEN=ghs_16C7e42F292c6912E7710c838347Ae178B4a",
		"GIT_AUTHOR_NAME=crewflow-executor[bot]",
		"GIT_AUTHOR_EMAIL=1987+crewflow-executor[bot]@users.noreply.github.com",
		"GIT_COMMITTER_NAME=crewflow-executor[bot]",
		"GIT_COMMITTER_EMAIL=1987+crewflow-executor[bot]@users.noreply.github.com",
	} {
		if !contains(identity.Env, want) {
			t.Errorf("the environment of the executor is %v, want %q in it", identity.Env, want)
		}
	}
	if want := "crewflow auth git-credential"; identity.GitConfig["credential.helper"] != want {
		t.Errorf("the settings of git in the worktree are %v, want credential.helper = %q", identity.GitConfig, want)
	}
	// The token is in the environment of the executor and nowhere else, and its value
	// is among the secrets a journal is put through: an agent that prints its own
	// environment is one line of a journal that would carry a token of an hour into a
	// file kept for ever (docs/DESIGN.md §7e).
	if !contains(identity.Secrets, "ghs_16C7e42F292c6912E7710c838347Ae178B4a") {
		t.Errorf("the secrets of the run are %v, want the token of the run in them", identity.Secrets)
	}
}

// TestSourceIdentityWithoutATokenSaysSo: a run in the mode of the bot that cannot be
// handed a token is a run that was not started, and crewflow says why before the
// executor of it is.
func TestSourceIdentityWithoutATokenSaysSo(t *testing.T) {
	source := sourceOfTest(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		answerOfTest(t, w, map[string]any{"message": "Bad credentials"})
	})

	_, err := source.Identity(t.Context())
	if err == nil {
		t.Fatal("Identity returned no error, want the refusal of the API")
	}
	if !strings.Contains(err.Error(), "Bad credentials") {
		t.Errorf("Identity = %v, want what the API said", err)
	}
}

// TestSourceHasKeyAsksTheStoreWithoutReadingIt: a report says that the key of an App
// is on this machine, and it does not read a key to say it: the store of macOS can
// answer that on its own, and a store that cannot is asked for the value.
func TestSourceHasKeyAsksTheStoreWithoutReadingIt(t *testing.T) {
	store := &storeOfTest{key: Key{key: keyOfTheTest(t)}.PEM()}
	source := sourceOfTest(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("the API was asked about something, want nothing: this is a question to the store")
	})
	source.Store = store

	has, err := source.HasKey()
	if err != nil {
		t.Fatalf("HasKey: %v", err)
	}
	if !has {
		t.Error("HasKey = false, want true: the key is in the store")
	}
	if store.reads != 0 {
		t.Errorf("the store was read %d times, want none: a report does not read a key", store.reads)
	}
}

// TestSourceHasKeyOfAnEmptyStoreSaysNo: the owner has to import the key before a run
// in the mode of the bot, and a report has to be able to say that it is not there.
func TestSourceHasKeyOfAnEmptyStoreSaysNo(t *testing.T) {
	source := sourceOfTest(t, nil)
	source.Store = &storeOfTest{}

	has, err := source.HasKey()
	if err != nil {
		t.Fatalf("HasKey: %v", err)
	}
	if has {
		t.Error("HasKey = true, want false: the store holds no key")
	}
}

// TestInstallationGrantedIsWhatAPersonReads: a report of the rights of an App says
// them the way the settings of the App say them, sorted, and it says a right crewflow
// did not ask for as well — what an App may do is worth nothing if the report only
// says what it was asked for.
func TestInstallationGrantedIsWhatAPersonReads(t *testing.T) {
	installation := Installation{Permissions: map[string]string{
		"pull_requests": "write",
		"contents":      "write",
		"issues":        "read",
		"workflows":     "write",
	}}
	want := []string{"contents write", "issues read", "pull_requests write", "workflows write"}
	got := installation.Granted()
	if len(got) != len(want) {
		t.Fatalf("the rights are %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("the rights are %v, want %v", got, want)
			break
		}
	}
	if granted := (Installation{}).Granted(); len(granted) > 0 {
		t.Errorf("the rights of an installation with none are %v, want none", granted)
	}
}

// asked is what a call to the API was made with, as a test reads it: the path, the
// method, the headers and the body.
type asked struct {
	path          string
	paths         []string
	method        string
	authorization string
	accept        string
	body          map[string]any
}

// of reads one request of the server of a test.
func (a *asked) of(t *testing.T, r *http.Request) {
	t.Helper()
	a.path, a.method = r.URL.Path, r.Method
	a.paths = append(a.paths, r.URL.Path)
	a.authorization, a.accept = r.Header.Get("Authorization"), r.Header.Get("Accept")
	if r.Body == nil {
		return
	}
	body, err := readBody(r)
	if err != nil {
		t.Fatalf("read the body of the request: %v", err)
	}
	if body == "" {
		return
	}
	a.body = objectOfTest(t, body, false)
}

// contains is whether a list of the words of a report holds one of them.
func contains(words []string, want string) bool {
	for _, word := range words {
		if word == want {
			return true
		}
	}
	return false
}

// TestSourceOnAMachineWithoutAStoreSaysSo: the keychain is the store crewflow has, and
// a machine that has none cannot sign a token of an app. A report of such a machine
// says what is missing instead of falling over on a store that was never there
// (docs/DESIGN.md §7i).
func TestSourceOnAMachineWithoutAStoreSaysSo(t *testing.T) {
	source := sourceOfTest(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("the API was asked about something, want nothing: there is no key to ask with")
	})
	source.Store = nil

	if _, err := source.Key(); err == nil {
		t.Error("Key returned no error, want a machine without a store of secrets to say so")
	} else if !strings.Contains(err.Error(), "no store of secrets") {
		t.Errorf("Key = %v, want it to say the machine has no store", err)
	}
	if _, err := source.HasKey(); err == nil {
		t.Error("HasKey returned no error, want a machine without a store of secrets to say so")
	}
}
