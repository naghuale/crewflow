package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/secret"
)

// The words of the mode of a run: the executor works as the App, or as the owner of
// the project. The core knows these two and nothing else, and how a bot of a host is
// written down is the business of the adapter of that host (docs/DESIGN.md §7i).
const (
	// ModeOwner is the executor of the login of the person who runs crewflow.
	ModeOwner = "owner"
	// ModeBot is the executor of an account of the host of its own.
	ModeBot = "bot"
)

// The rights crewflow asks the token of an installation for, one by one, and no more:
// contents and pull requests to write the work of a task, issues to read the task,
// metadata to be read at all (docs/DESIGN.md §7i). There is no administration and no
// workflow in it, and a token asked for with more of either is a token the executor
// of a run may use on a day nobody wanted it.
var rights = map[string]string{
	"contents":      "write",
	"pull_requests": "write",
	"issues":        "read",
	"metadata":      "read",
}

// Source is the App of one project: what it is, where its key is kept, and the server
// it is asked about. Every part of it is a plain value, so that a test of a token
// exchange runs against a server of its own and a store of its own, and the key of an
// App is nowhere in a test but in a key the test generated.
type Source struct {
	// AppID is the number of the App, as it is in the settings of its owner.
	AppID int64
	// InstallationID is the installation of the App on the repository of the
	// project. Zero is not a mistake: the App is asked which installation the
	// repository has, once per run.
	InstallationID int64
	// Repo is the repository as "owner/name", the one repository the token of the
	// installation is asked for.
	Repo string
	// BaseURL is the address of the API, and Now is the clock of the machine: both
	// are given so that a test of a token exchange happens at a moment of its own
	// against a server of its own.
	BaseURL string
	Store   secret.Store
	HTTP    *http.Client
	Now     func() time.Time
}

// Token is a token of an installation: the value itself and the moment it stops
// working. GitHub gives an hour, and crewflow asks for one repository and the rights
// above, and a run keeps the value in the environment of the executor and nowhere
// else.
type Token struct {
	// Value is the token itself. It is a secret to a report: a journal of a run
	// goes through the redactor of package secret with this value in it
	// (docs/DESIGN.md §7e).
	Value string
	// ExpiresAt is when GitHub says the token stops working, as the answer holds it.
	ExpiresAt time.Time
}

// Account is an account of the host as the API of GitHub writes one: a person, a bot
// or an organization. It is an object with a name in it and not a name — the account of
// an installation is written by GitHub as `{"login": …, "id": …, "type": …}`, and an
// answer in which it is a string is an answer no installation of GitHub ever gives
// (docs/DESIGN.md §7i).
type Account struct {
	// Login is the name the account is known under, and ID is the number the host
	// knows it by: the two are what an address of a commit of a run is made of.
	Login string `json:"login"`
	ID    int64  `json:"id"`
	// Type is what the API says the account is — "User", "Organization", "Bot" — and
	// a report shows it, so that a person can tell a bot from a person at a glance.
	Type string `json:"type"`
}

// Installation is the App as it is installed, and what it may do there: what a report
// shows about the powers of the executor, and what `crewflow auth app check` prints
// without printing a token.
//
// The fields are those of the answer of `GET /repos/{owner}/{repo}/installation` and
// of `GET /app/installations/{installation_id}` as the documentation of the REST API
// of GitHub describes them, of the version crewflow asks for (apiVersion): both
// endpoints answer the same thing, and one struct reads both. What the answer holds
// besides them — which repositories, when it was made, who suspended it — is not what
// a run of a project needs, and a field of an invention is a field nothing checks
// (docs/DESIGN.md §7i).
type Installation struct {
	// ID is the number of the installation, which is what a token is asked of, and
	// AppID is the number of the App that is installed.
	ID    int64 `json:"id"`
	AppID int64 `json:"app_id"`
	// Account is who the installation is of: the person or the organization that let
	// the App in.
	Account Account `json:"account"`
	// Permissions are the rights the installation was given, as GitHub writes them:
	// "contents": "write" is a map of a string to a string and not a flag, because
	// that is how the answer of the API is shaped and a permission crewflow invents
	// would be a permission nothing checks.
	Permissions map[string]string `json:"permissions"`
}

// Granted are the rights of the installation written as a person reads them, sorted
// by name: "contents write, issues read, pull_requests write". An installation with
// no rights at all is said so rather than left blank, and a right crewflow did not
// ask for is in the list as well — a report of what an App may do is worth nothing if
// it only says what it was asked for.
func (i Installation) Granted() []string {
	names := make([]string, 0, len(i.Permissions))
	for name := range i.Permissions {
		names = append(names, name)
	}
	slices.Sort(names)
	granted := make([]string, 0, len(names))
	for _, name := range names {
		granted = append(granted, name+" "+i.Permissions[name])
	}
	return granted
}

// BeyondARun are the rights an installation holds that a token of a run never asks
// for: a right crewflow did not ask for at all — workflows, administration and the
// rest of what an App can be given — and a right asked for with a level wider than
// the one of §7i, as `issues write` where a run reads the issues of a task and
// `contents admin` where a run writes them. Both are the powers the executor of every
// run of this project has, and a report that lists only the rights of the design is a
// report of an intention and not of a machine (docs/DESIGN.md §7i).
func (i Installation) BeyondARun() []string {
	var beyond []string
	for _, right := range i.Granted() {
		name, level, _ := strings.Cut(right, " ")
		if asked, known := rights[name]; !known || asked != level {
			beyond = append(beyond, right)
		}
	}
	return beyond
}

// API is the address of the API of a host: the public one, and the one of a server of
// a project on its own path of three, which is where a server of GitHub keeps it.
//
// It is here and not in the adapter of GitHub alone, because a command about the key
// of an app asks the same questions a run does and has to be pointed at the same
// place, and a test of either of them points both at a server of its own instead of
// at GitHub (docs/DESIGN.md §7i).
func API(host string) string {
	if host == "" {
		return "https://api.github.com"
	}
	return "https://" + host + "/api/v3"
}

// Bot is the account the commits of a run are made by: the account of the App, as
// GitHub writes it, and the address it gives that account so that a commit of a bot
// is a commit of a bot and not a name somebody chose.
type Bot struct {
	// Account is what the answer of the API holds: the name, the number and what
	// GitHub says the account is. An account of an App is an account like any other,
	// and it is read as one.
	Account
	// Email is the address of the account. GitHub sends no address for an account of
	// an App, and the one of a person is no address at all, so it is composed of the
	// number and the name — the form GitHub itself writes in the commits it makes.
	Email string
}

// appIDPath is the path of the App itself, which crewflow asks to be let in as.
const appPath = "/app"

// apiVersion is the version of the REST API of GitHub that every call of crewflow
// asks for, and the version whose documentation describes every field it reads: an
// answer of another version is an answer whose shape nobody looked at
// (docs/DESIGN.md §7i).
const apiVersion = "2022-11-28"

// Key is the private key of the App, read from the store of the machine. An owner
// imports it once with `crewflow auth app import`, and from then on crewflow reads it
// in the moment it signs a token and at no other time: the executor of a run never
// sees it, and no journal and no error holds it (docs/DESIGN.md §7i).
func (s *Source) Key() (Key, error) {
	store, err := s.store()
	if err != nil {
		return Key{}, err
	}
	value, err := store.Get(secret.Service, secret.AppKey(s.AppID))
	if err != nil {
		if errors.Is(err, secret.ErrNotFound) {
			return Key{}, fmt.Errorf("the private key of the app %d is not in the store: %w; "+
				"download it in the settings of the app and run `crewflow auth app import <file.pem>`",
				s.AppID, ErrNoKey)
		}
		return Key{}, fmt.Errorf("the private key of the app %d: %w", s.AppID, err)
	}
	key, err := ReadKey(value)
	if err != nil {
		return Key{}, fmt.Errorf("the private key of the app %d in the store is not one: %w; "+
			"import it again with `crewflow auth app import <file.pem>`", s.AppID, err)
	}
	return key, nil
}

// HasKey reports whether the key of the App is in the store, without reading it when
// the store can answer that alone: a report of a machine says that the key is there,
// and a report does not print a key to say it (docs/DESIGN.md §7e).
func (s *Source) HasKey() (bool, error) {
	store, err := s.store()
	if err != nil {
		return false, err
	}
	if presence, ok := store.(secret.Presence); ok {
		return presence.Has(secret.Service, secret.AppKey(s.AppID))
	}
	_, err = store.Get(secret.Service, secret.AppKey(s.AppID))
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, secret.ErrNotFound):
		return false, nil
	default:
		return false, err
	}
}

// Installation is the App as it is installed on the repository of the project, and
// what it may do there. The repository is asked about, because that is the question
// the check exists for: is the App installed here. When the file of the project names
// an installation, that one is asked for as well and the two numbers have to be the
// same — a run is handed a token of the number in the file, and the rights of one
// installation said of another are the rights of a machine nobody runs on
// (docs/DESIGN.md §7i).
func (s *Source) Installation(ctx context.Context) (Installation, error) {
	// The key is read for every request and not kept: it is the one secret of a run
	// that never outlives the call it signed.
	key, err := s.Key()
	if err != nil {
		return Installation{}, err
	}
	repository, err := s.installationAt(ctx, key, "/repos/"+s.Repo+"/installation")
	if err != nil {
		return Installation{}, err
	}
	if s.InstallationID == 0 {
		return repository, nil
	}
	named, err := s.installationAt(ctx, key, fmt.Sprintf("/app/installations/%d", s.InstallationID))
	if err != nil {
		return Installation{}, err
	}
	if named.ID != repository.ID {
		return Installation{}, fmt.Errorf("the file of the project names the installation %d of the app %d, "+
			"and the app is installed on %s as %d: a run of this project is given a token of the number in the "+
			"file, so change installation_id in the file of the project", s.InstallationID, s.AppID, s.Repo, repository.ID)
	}
	return named, nil
}

// installationAt is one installation as one of the two endpoints of the API answers
// it: the one of the repository and the one of the number are the same answer, and
// an answer without an installation in it is an answer crewflow cannot go on with.
func (s *Source) installationAt(ctx context.Context, key Key, path string) (Installation, error) {
	var installation Installation
	if err := s.ask(ctx, key, http.MethodGet, path, nil, &installation); err != nil {
		return Installation{}, fmt.Errorf("the installation of the app %d on %s: %w", s.AppID, s.Repo, err)
	}
	if installation.ID == 0 {
		return Installation{}, fmt.Errorf("the installation of the app %d on %s: the answer of the API names no installation", s.AppID, s.Repo)
	}
	return installation, nil
}

// granted is the answer of `POST /app/installations/{installation_id}/access_tokens`
// as crewflow reads it: the token and the moment it stops working. The answer holds the
// repository the token is for and the rights it was given as well, and crewflow asked
// for both — a field of an invention is a field nothing checks (docs/DESIGN.md §7i).
type granted struct {
	// Token is the value itself, and ExpiresAt is the hour it is good until, as the
	// documentation of the API of GitHub writes both of them (apiVersion).
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Token is a token of the installation, for the repository of the project and for
// nothing else. It is short: GitHub gives an hour, and a run of an agent is longer
// than that, which is why the credential helper of a worktree signs a new one for
// every push and a run keeps none of them (docs/DESIGN.md §7i).
func (s *Source) Token(ctx context.Context) (Token, error) {
	token, _, err := s.token(ctx)
	return token, err
}

// token is a token of the installation and the number of the installation it was
// asked of: a description of a run says which installation its work went under, and
// the number is not always in the file of the project.
func (s *Source) token(ctx context.Context) (Token, int64, error) {
	installation := s.InstallationID
	if installation == 0 {
		found, err := s.Installation(ctx)
		if err != nil {
			return Token{}, 0, err
		}
		installation = found.ID
	}
	key, err := s.Key()
	if err != nil {
		return Token{}, 0, err
	}
	body := map[string]any{
		"repositories": []string{s.Repo},
		"permissions":  rights,
	}
	var answer granted
	path := fmt.Sprintf("/app/installations/%d/access_tokens", installation)
	if err := s.ask(ctx, key, http.MethodPost, path, body, &answer); err != nil {
		return Token{}, 0, fmt.Errorf("the token of the app %d on %s: %w", s.AppID, s.Repo, err)
	}
	if answer.Token == "" {
		return Token{}, 0, fmt.Errorf("the token of the app %d on %s: the answer of the API holds no token", s.AppID, s.Repo)
	}
	return Token{Value: answer.Token, ExpiresAt: answer.ExpiresAt}, installation, nil
}

// store is where the key of the App is, and an error where there is none: a machine
// that has no store of secrets cannot sign a token, and a report of it says so in the
// words of the machine rather than falling over on a store that was never there
// (docs/DESIGN.md §7i).
func (s *Source) store() (secret.Store, error) {
	if s.Store == nil {
		return nil, fmt.Errorf("the private key of the app %d: this machine has no store of secrets, "+
			"and the keychain is the store crewflow has", s.AppID)
	}
	return s.Store, nil
}

// App is the App itself as the API of GitHub describes it: its number and the name its
// accounts come under. The endpoint is one of the App and is asked for with the key of
// it, because there is no installation to ask with before one is found.
//
// The answer holds more — the name of the App, the number it signs its calls with, its
// owner, the rights it may be given and the events it subscribes to — and a run needs
// the name alone: the name is the account a commit of a run is made by.
type App struct {
	// ID is the number of the App, and Slug is the name an account of it is
	// "slug[bot]" under: the name is where a report says who the executor is.
	ID   int64  `json:"id"`
	Slug string `json:"slug"`
}

// App is the App of the project as the API of GitHub describes it.
func (s *Source) App(ctx context.Context) (App, error) {
	key, err := s.Key()
	if err != nil {
		return App{}, err
	}
	var app App
	if err := s.ask(ctx, key, http.MethodGet, appPath, nil, &app); err != nil {
		return App{}, fmt.Errorf("the app %d: %w", s.AppID, err)
	}
	if app.Slug == "" {
		return App{}, fmt.Errorf("the app %d: the answer of the API holds no slug, so its accounts have no name", s.AppID)
	}
	return app, nil
}

// Bot is the account the commits of a run are made by. It is asked for by its name,
// and the number of the account is asked for and not guessed: the number is part of
// the address of the account, and an address with a guessed number is an address of
// nobody. The answer of `GET /users/{login}` is the account of GitHub as it writes
// every account — a person, a bot or an organization — and it holds more than crewflow
// shows; the name, the number and the type are the whole of what a run needs.
func (s *Source) Bot(ctx context.Context) (Bot, error) {
	app, err := s.App(ctx)
	if err != nil {
		return Bot{}, err
	}
	var bot Bot
	if err := s.askAsInstallation(ctx, http.MethodGet, "/users/"+url.PathEscape(app.Slug+"[bot]"), nil, &bot); err != nil {
		return Bot{}, fmt.Errorf("the account of the app %d: %w", s.AppID, err)
	}
	if bot.Login == "" {
		return Bot{}, fmt.Errorf("the account of the app %d: the answer of the API holds no login", s.AppID)
	}
	bot.Email = strconv.FormatInt(bot.ID, 10) + "+" + bot.Login + "@users.noreply.github.com"
	return bot, nil
}

// Describe is whose name the executor of a run of this project works under, in one
// line and without the rights of that name: a report of a machine shows the line and
// needs no token of an hour to show it, and a token it minted for that would be a
// token of an hour in the memory of a command whose work is to print a report
// (docs/DESIGN.md §7e, §7i).
//
// It does ask the key of the App for the name of the App: there is no way to learn
// what an account of GitHub is called without being let in as the App, and a report
// that cannot be asked says so.
func (s *Source) Describe(ctx context.Context) (forge.Identity, error) {
	app, err := s.App(ctx)
	if err != nil {
		return forge.Identity{}, err
	}
	installation := s.InstallationID
	if installation == 0 {
		found, err := s.Installation(ctx)
		if err != nil {
			return forge.Identity{}, err
		}
		installation = found.ID
	}
	return forge.Identity{
		Mode:        ModeBot,
		Description: fmt.Sprintf("bot — GitHub App %s (installation %d)", app.Slug, installation),
	}, nil
}

// Identity is what a run in the mode of the bot is given: the token in the
// environment of the executor, the name and the address its commits are made by, the
// helper git takes its credentials from, and the values that must not reach a
// journal. The description is the one line every report of a run shows, so that a
// person reading a run knows whose name it went under without asking.
//
// The token is in the identity and nowhere else: crewflow signs it here, hands it to
// the executor and puts its value through the redactor of the journal, and the
// helper signs a new one for every push of the worktree.
func (s *Source) Identity(ctx context.Context) (forge.Identity, error) {
	token, installation, err := s.token(ctx)
	if err != nil {
		return forge.Identity{}, err
	}
	bot, err := s.Bot(ctx)
	if err != nil {
		return forge.Identity{}, err
	}
	return forge.Identity{
		Mode:        ModeBot,
		Description: fmt.Sprintf("bot — GitHub App %s (installation %d)", strings.TrimSuffix(bot.Login, "[bot]"), installation),
		Env: []string{
			// gh is the tool of the adapter of GitHub, and the token is what lets it
			// speak as the App: without it gh would speak as the person, and the
			// powers of a run would be the powers of the person (§7i).
			"GH_TOKEN=" + token.Value,
			// The commits of a run are made by the account of the App, and not by
			// whoever happens to have the machine: a person reading a diff has to see
			// the bot that wrote it.
			"GIT_AUTHOR_NAME=" + bot.Login,
			"GIT_AUTHOR_EMAIL=" + bot.Email,
			"GIT_COMMITTER_NAME=" + bot.Login,
			"GIT_COMMITTER_EMAIL=" + bot.Email,
		},
		GitConfig: map[string]string{
			// A run of an agent is longer than the life of a token, and git asks for
			// a password every time it pushes. The helper signs a new token for the
			// push, so that the executor never has to be handed a token twice
			// (docs/DESIGN.md §7i).
			"credential.helper": "crewflow auth git-credential",
		},
		Secrets: []string{token.Value},
	}, nil
}

// ask makes one call to the API as the App itself: the bearer token of the call is the
// JWT that the key of the App signed, and it is the only way into the endpoints of
// the App and its installations.
func (s *Source) ask(ctx context.Context, key Key, method, path string, body, out any) error {
	token, err := key.JWT(s.AppID, s.now())
	if err != nil {
		return err
	}
	return s.call(ctx, method, path, token, body, out)
}

// askAsInstallation makes one call to the API as the installation, which is what the
// questions about a repository are asked with: the endpoints of the App and its
// installations are for the App, and everything else is for the installation.
func (s *Source) askAsInstallation(ctx context.Context, method, path string, body, out any) error {
	token, err := s.Token(ctx)
	if err != nil {
		return err
	}
	return s.call(ctx, method, path, token.Value, body, out)
}

// call is one request to the API and what it answered, whatever the request is: the
// address of the API and the client are the ones the source was given, so that a
// test of the package is a test of a server of its own and not of GitHub.
func (s *Source) call(ctx context.Context, method, path, token string, body, out any) error {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("the body of the request: %w", err)
		}
		payload = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(s.BaseURL, "/")+path, payload)
	if err != nil {
		return fmt.Errorf("the request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", apiVersion)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := s.client().Do(request)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() { _ = response.Body.Close() }()
	answer, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("%s %s: read the answer: %w", method, path, err)
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		return fmt.Errorf("%s %s: the API answered %s: %s", method, path, response.Status, message(answer))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(answer, out); err != nil {
		// The decoder names the field it could not read, and a person who reads this
		// has to be told what it means: a field of a type of its own is a change of
		// the API and not a mistake of theirs, so the owner of the project is the one
		// to tell — crewflow reads the fields the documentation of apiVersion
		// describes, and an answer of a shape of its own is one of them to look at
		// (docs/DESIGN.md §7i).
		return fmt.Errorf("%s %s: the answer of the API is not a JSON crewflow knows: %w; "+
			"the fields crewflow reads are the fields of the documentation of the API of GitHub of the version %s, "+
			"so a field of another type is a change of the API: tell the owner of the project",
			method, path, err, apiVersion)
	}
	return nil
}

// client is the HTTP the API is asked with, and the clock the token is signed with:
// both are given, and a machine that is asked without one of them says so rather than
// reaching for a package that would be untested.
func (s *Source) client() *http.Client {
	if s.HTTP == nil {
		return http.DefaultClient
	}
	return s.HTTP
}

// now is the clock of the machine, and a Source without one is a Source that cannot
// sign: a token is a promise about a moment, and a moment nobody named is not one.
func (s *Source) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}
	return s.Now()
}

// message is what the API said about a refusal, in its own words: the text of a
// message of GitHub is what a person can act on, and "exited with 1" is not.
func message(answer []byte) string {
	var said struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(answer, &said); err == nil && said.Message != "" {
		return said.Message
	}
	if text := strings.TrimSpace(string(answer)); text != "" {
		return text
	}
	return "the API said nothing about it"
}
