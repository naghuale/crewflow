package roles

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/forge/github"
	"github.com/naghuale/crewflow/internal/secret"
)

// baseConfig is what a project says at least; a case adds the keys of the roles
// it is about and never writes the base twice.
const baseConfig = `
[project]
repo = "naghuale/crewflow"

[executor]
command = ["agent", "run", "{prompt}"]
`

// TestNewOnGitHub is the default: the tasks are the issues of the host, the checks
// are the workflows of the host, and all three roles are one adapter of one
// program (docs/DESIGN.md §7g).
func TestNewOnGitHub(t *testing.T) {
	cfg := load(t, baseConfig)

	set, err := New(cfg, newMachine().env(t))
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}

	roles := []struct {
		name string
		role any
	}{
		{"forge", set.Forge},
		{"tracker", set.Tracker},
		{"ci", set.CI},
	}
	for _, role := range roles {
		if _, ok := role.role.(*github.Adapter); !ok {
			t.Errorf("the %s role is %T, want the adapter of GitHub", role.name, role.role)
		}
	}
	if len(set.Checkers()) != 1 {
		t.Errorf("the set checks %d adapters, want GitHub alone", len(set.Checkers()))
	}
}

// TestNewPassesTheProjectToTheAdapter checks that the settings reach the adapter:
// the repository is in every command, and the server of the project is in the
// environment of gh.
func TestNewPassesTheProjectToTheAdapter(t *testing.T) {
	m := newMachine().prints("issue view", `{"number": 5, "title": "a task", "state": "OPEN", "url": "u", "labels": []}`)
	cfg := load(t, baseConfig+"\n[forge]\nkind = \"github\"\nhost = \"github.company.com\"\n")

	set, err := New(cfg, m.env(t))
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}
	if _, err := set.Tracker.Task(t.Context(), 5); err != nil {
		t.Fatalf("Task(5) returned an error: %v", err)
	}

	if got := m.commandLine(0); !strings.Contains(got, "-R naghuale/crewflow") {
		t.Errorf("the adapter ran %q, want the repository of the project in it", got)
	}
	if got := m.environment[0]; len(got) != 1 || got[0] != "GH_HOST=github.company.com" {
		t.Errorf("the command ran with the environment %q, want the host of the project", got)
	}
}

// TestAsOwnerOfAProjectInTheModeOfTheBot is the review of a change: it is the
// business of the person who runs crewflow even where the executor of the project
// works as an App of the host, because the record of a review is counted only when
// it is written by a reviewer of the project, and the rules of a branch are rights
// a person has and an App of a run does not (docs/DESIGN.md §7h, §7i).
func TestAsOwnerOfAProjectInTheModeOfTheBot(t *testing.T) {
	m := newMachine()
	cfg := load(t, baseConfig+`
[identity]
mode = "bot"

[identity.github_app]
app_id = 5107052
`)

	asRun, err := New(cfg, m.env(t))
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}
	asOwner, err := AsOwner(cfg, m.env(t))
	if err != nil {
		t.Fatalf("AsOwner returned an error: %v", err)
	}

	if asRun.Opener == nil {
		t.Error("a run of a project in the mode of the bot has nothing to open a change request with")
	}
	if asOwner.Opener != nil {
		t.Error("a review has an account of the host of its own: the record of a review is the word of a person")
	}
	described, err := forge.DescribeIdentity(t.Context(), asOwner.Forge)
	if err != nil {
		t.Fatalf("DescribeIdentity returned an error: %v", err)
	}
	if described.Mode != forge.ModeOwner {
		t.Errorf("the identity of a review is %q, want %q: it is the person who reviews", described.Mode, forge.ModeOwner)
	}
}

// TestNewWithoutChecks walks a project that has no CI at all: its gates are the
// only checks there are, and there is nothing to ask.
func TestNewWithoutChecks(t *testing.T) {
	m := newMachine()
	cfg := load(t, baseConfig+"\n[ci]\nkind = \"none\"\n")

	set, err := New(cfg, m.env(t))
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}

	state, err := set.CI.Status(t.Context(), "9e37237")
	if err != nil {
		t.Fatalf("Status returned an error: %v", err)
	}
	if state != forge.CheckNone {
		t.Errorf("Status = %q, want %q: there are no checks to ask about", state, forge.CheckNone)
	}
	if len(m.ran) != 0 {
		t.Errorf("the machine was asked %v, want nothing: no CI is no question", m.ran)
	}
}

// TestHostOfWithoutAHost walks a project that is not hosted anywhere: there is no
// adapter of a host, and the adapter of the tasks is the adapter of the host
// whenever the tasks are there, which is not the case any more.
func TestHostOfWithoutAHost(t *testing.T) {
	cfg := config.Config{
		Project: config.Project{Repo: "naghuale/crewflow"},
		Forge:   config.Forge{Kind: "none"},
		Tracker: config.Tracker{Kind: "files"},
		CI:      config.CI{Kind: "none"},
	}

	hosting, err := hostOf(cfg, newMachine().env(t), cfg.Identity.Mode, cfg.Orchestrator.Mode, WithKeys)
	if err != nil {
		t.Fatalf("hostOf returned an error: %v", err)
	}

	if hosting != nil {
		t.Errorf("hostOf = %v, want nothing: the project has no host", hosting)
	}
	_, err = New(cfg, newMachine().env(t))
	var notImplemented *forge.ErrNotImplemented
	if !errors.As(err, &notImplemented) {
		t.Errorf("New returned %v, want the error of the one role of such a project that has no adapter yet", err)
	}
}

// TestNewNotImplemented walks the kinds crewflow has no adapter for yet, which
// are all of them but GitHub: the error names the setting and the kind, so that
// a report can tell a person which key to change.
//
// The settings are made by hand, because a file that asks for a role crewflow has
// no adapter for is refused by config.Load now, naming the task that writes it
// (docs/DESIGN.md §5). What is left for this package to answer is what it does
// with the settings it is given, which is the refusal a caller that builds them
// itself still meets.
func TestNewNotImplemented(t *testing.T) {
	cases := []struct {
		name string
		key  string
		kind string
	}{
		{"gitlab", "forge.kind", "gitlab"},
		{"bitbucket", "forge.kind", "bitbucket"},
		{"jira", "tracker.kind", "jira"},
		{"linear", "tracker.kind", "linear"},
		{"files", "tracker.kind", "files"},
		{"jenkins", "ci.kind", "jenkins"},
		{"command", "ci.kind", "command"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(settingsWithKind(tc.key, tc.kind), newMachine().env(t))
			if err == nil {
				t.Fatalf("New with the %s adapter returned no error, want one", tc.name)
			}
			var notImplemented *forge.ErrNotImplemented
			if !errors.As(err, &notImplemented) {
				t.Fatalf("New returned %v, want an error a caller may tell as *forge.ErrNotImplemented", err)
			}
			if notImplemented.Key != tc.key || notImplemented.Kind != tc.name {
				t.Errorf("the error holds %+v, want the key %s and the kind %s", notImplemented, tc.key, tc.name)
			}
			want := fmt.Sprintf("adapter %s is not implemented yet", tc.name)
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %q", err, want)
			}
		})
	}
}

// TestNewWithRolesThatContradictThemselves is the pair config.Load refuses, and
// this package refuses as well: a project that takes its tasks from a host it
// does not have is a mistake in the file, not a missing adapter.
func TestNewWithRolesThatContradictThemselves(t *testing.T) {
	cases := []struct {
		name string
		key  string
	}{
		{"the tasks of a project without a host", "tracker.kind"},
		{"the checks of a project without a host", "ci.kind"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The settings are made by hand and without the validation of
			// config.Load, which is where a project like this is turned away.
			cfg := config.Config{
				Project: config.Project{Repo: "naghuale/crewflow"},
				Forge:   config.Forge{Kind: "none"},
				Tracker: config.Tracker{Kind: "files"},
				CI:      config.CI{Kind: "none"},
			}
			if tc.key == "tracker.kind" {
				cfg.Tracker.Kind = "forge"
			} else {
				cfg.CI.Kind = "forge"
			}

			_, err := New(cfg, newMachine().env(t))
			if err == nil {
				t.Fatal("New returned no error, want one")
			}
			var notImplemented *forge.ErrNotImplemented
			if errors.As(err, &notImplemented) {
				t.Fatalf("New returned %v, want a mistake in the settings and not a missing adapter", err)
			}
			for _, want := range []string{tc.key, `"none"`} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

// TestTheLoginsOfTheListsBecomeSubjectsOfTheAccountsBehindThem: the file of a project
// names its accounts by login, and the gate counts records by the number the host keeps
// each of them under — so every login of both lists is asked of the host once here, and
// what the gate is given from then on is the account behind the name (docs/DESIGN.md §7h, §7i).
func TestTheLoginsOfTheListsBecomeSubjectsOfTheAccountsBehindThem(t *testing.T) {
	m := newMachine().
		prints("api users/naghuale", `{"id": 93920024, "login": "naghuale", "type": "User"}`)
	cfg := load(t, baseConfig+"\n[merge]\nreviewers = [\"naghuale\"]\nowners = [\"naghuale\"]\n")
	set, err := AsOrchestrator(cfg, m.env(t))
	if err != nil {
		t.Fatalf("AsOrchestrator returned an error: %v", err)
	}

	reviewers, err := ReviewersOf(t.Context(), cfg, set)
	if err != nil {
		t.Fatalf("ReviewersOf returned an error: %v", err)
	}
	owners, err := OwnersOf(t.Context(), cfg, set)
	if err != nil {
		t.Fatalf("OwnersOf returned an error: %v", err)
	}

	want := forge.Subject{Kind: forge.KindUser, ID: 93920024, Login: "naghuale"}
	for _, list := range [][]forge.Subject{reviewers, owners} {
		if len(list) != 1 || !list[0].Same(want) {
			t.Errorf("the accounts of a list are %+v, want the owner %+v", list, want)
		}
	}
}

// TestTheOwnerOfTheRepositoryIsTheDefaultOfBothLists: a project that names nobody has the
// owner of its repository for both lists, which is what it has always had — and the owner
// of a repository is a login the file does not even write, so it is asked of the host like
// every other account (docs/DESIGN.md §5, §7i).
func TestTheOwnerOfTheRepositoryIsTheDefaultOfBothLists(t *testing.T) {
	m := newMachine().
		prints("api users/naghuale", `{"id": 93920024, "login": "naghuale", "type": "User"}`)
	cfg := load(t, baseConfig)
	set, err := AsOrchestrator(cfg, m.env(t))
	if err != nil {
		t.Fatalf("AsOrchestrator returned an error: %v", err)
	}

	reviewers, err := ReviewersOf(t.Context(), cfg, set)
	if err != nil {
		t.Fatalf("ReviewersOf returned an error: %v", err)
	}
	owners, err := OwnersOf(t.Context(), cfg, set)
	if err != nil {
		t.Fatalf("OwnersOf returned an error: %v", err)
	}

	want := forge.Subject{Kind: forge.KindUser, ID: 93920024, Login: "naghuale"}
	for _, list := range [][]forge.Subject{reviewers, owners} {
		if len(list) != 1 || !list[0].Same(want) {
			t.Errorf("the accounts of a list are %+v, want the owner of the repository %+v", list, want)
		}
	}
}

// TestALoginTheHostDoesNotNameIsAnErrorOfTheFile: a file of a project naming an account
// crewflow cannot number is a mistake in that file — a gate that went on without it would
// count no records at all and say that nobody had approved anything (docs/DESIGN.md §7h, §7i).
func TestALoginTheHostDoesNotNameIsAnErrorOfTheFile(t *testing.T) {
	cases := []struct {
		name    string
		refused string
		answer  string
		// key is where the login stands in the file, and what the refusal has to name:
		// a person who reads it is to know which list to change.
		key string
		// of is the list the question is asked of.
		of func(context.Context, config.Config, forge.Set) ([]forge.Subject, error)
	}{
		{
			name:    "the host knows no such account",
			refused: "gh: Not Found (HTTP 404)",
			key:     "merge.owners[0]",
			of:      OwnersOf,
		},
		{
			name:   "the account is one crewflow may not read",
			answer: `{"id": 1, "login": "naghuale", "type": "Organization"}`,
			key:    "merge.reviewers[0]",
			of:     ReviewersOf,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine()
			if tc.refused != "" {
				m.fails("api users/naghuale", tc.refused)
			} else {
				m.prints("api users/naghuale", tc.answer)
			}
			cfg := load(t, baseConfig+"\n[merge]\nreviewers = [\"naghuale\"]\nowners = [\"naghuale\"]\n")
			set, err := AsOrchestrator(cfg, m.env(t))
			if err != nil {
				t.Fatalf("AsOrchestrator returned an error: %v", err)
			}

			asked, err := tc.of(t.Context(), cfg, set)
			if err == nil {
				t.Fatalf("the accounts of %s are %+v, want an error naming the key of the list", tc.key, asked)
			}
			if !strings.Contains(err.Error(), tc.key) {
				t.Errorf("the error %q does not name the key %q of the list", err, tc.key)
			}
		})
	}
}

// TestTheExecutorOfTheProjectIsTheAppItRunsAs: a record of the App of the executor is not
// an approval and not a decision of the owner whatever the lists of the file name, and the
// number of that App is what the gate tells it by — a run of another project has another
// App and another number (docs.DESIGN.md §7h, §7i).
func TestTheExecutorOfTheProjectIsTheAppItRunsAs(t *testing.T) {
	asBot := load(t, baseConfig+botConfig)
	got := ExecutorOf(asBot)
	if !got.Same(forge.Subject{Kind: forge.KindApp, ID: 5107052}) {
		t.Errorf("the executor of a project in the mode of the bot is %+v, want the app of the file", got)
	}
	if asOwner := ExecutorOf(load(t, baseConfig)); asOwner.ID != 0 || asOwner.Kind != "" {
		t.Errorf("the executor of a project in the mode of the owner is %+v, want nobody in particular", asOwner)
	}
}

// storeNobodyMayRead is the store of the secrets of a machine whose keychain of macOS asks
// the owner of the machine in a window of the system: reading a key out of it opens that
// window, and on a build nobody has trusted yet nobody is at it — a command that only
// shows the state of a project stands in front of that window for two minutes a read
// (F-109, docs/DESIGN.md §6a, §7i).
//
// The read is a fall and not a refusal, so that a test says which key was asked for and
// stops there: a store that returns an error is a store a caller may swallow.
type storeNobodyMayRead struct{}

func (storeNobodyMayRead) Get(service, account string) ([]byte, error) {
	panic(fmt.Sprintf("the key %s/%s was read: a command that only shows the state of a project has no business asking for it",
		service, account))
}

func (storeNobodyMayRead) Set(string, string, []byte) error { return nil }

func (storeNobodyMayRead) Has(string, string) (bool, error) { return false, nil }

// TestTheRolesThatShowTheStateCountTheAccountsByTheirNumbers: the whole of the second way
// of the roles of a project. A queue of what wants a person only shows the state of the
// work, and the accounts it counts records by are numbers the host publishes and numbers
// the file of the project holds — so the queue is worked out with the store of the secrets
// of the machine standing in the machine: nothing in it can read a key, and a command that
// only looks must work exactly as it is (docs/DESIGN.md §6a, §7h, §7i).
func TestTheRolesThatShowTheStateCountTheAccountsByTheirNumbers(t *testing.T) {
	m := machineOfTheNumbers()
	cfg := load(t, baseConfig+botConfig+orchestratorConfig+
		"\n[merge]\nreviewers = [\"crewflow-orchestrator[bot]\"]\nowners = [\"naghuale\"]\n")
	m.secrets = storeNobodyMayRead{}

	set, err := ToShow(cfg, m.env(t))
	if err != nil {
		t.Fatalf("ToShow returned an error: %v", err)
	}

	reviewers, err := ReviewersOf(t.Context(), cfg, set)
	if err != nil {
		t.Fatalf("ReviewersOf returned an error: %v", err)
	}
	owners, err := OwnersOf(t.Context(), cfg, set)
	if err != nil {
		t.Fatalf("OwnersOf returned an error: %v", err)
	}
	if want := (forge.Subject{Kind: forge.KindApp, ID: 5107053}); len(reviewers) != 1 || !reviewers[0].Same(want) {
		t.Errorf("the reviewers are %+v, want the app of the orchestrator %+v", reviewers, want)
	}
	if want := (forge.Subject{Kind: forge.KindUser, ID: 93920024}); len(owners) != 1 || !owners[0].Same(want) {
		t.Errorf("the owners are %+v, want the account of the owner %+v", owners, want)
	}
	if want := (forge.Subject{Kind: forge.KindApp, ID: 5107052}); !ExecutorOf(cfg).Same(want) {
		t.Errorf("the executor is %+v, want the app of the file %+v", ExecutorOf(cfg), want)
	}
	// The number of the App of the orchestrator is not asked of the host at all: it is in
	// the settings of the project, and the host publishes no number of an App against the
	// name of its account — `GET /apps/<slug>` answers only for the public apps, and the App
	// of a project on its own is not one. The machine of the test refuses that question the
	// way the host refused it in the live run of #130, and the queue does not notice (F-109).
	if asked := slices.ContainsFunc(m.ran, func(line string) bool {
		return strings.HasPrefix(line, "api apps/")
	}); asked {
		t.Errorf("gh was asked %v, want no question about the app behind a login", m.ran)
	}
	// The host is asked as the person: a token of an App is minted to act in its name, and
	// a queue is not an act (docs/DESIGN.md §7e, §7i).
	for i, environment := range m.environment {
		if slices.ContainsFunc(environment, func(pair string) bool { return strings.HasPrefix(pair, "GH_TOKEN=") }) {
			t.Errorf("gh was asked for %q with %v, want the login of the person and no token of an app",
				m.commandLine(i), environment)
		}
	}
}

// TestTheRolesThatShowTheStateHoldNoStoreAtAll: the numbers of the Apps are in the file of
// the project and stay in the adapter, and the key of an App is not in the file of anything
// — so an adapter built to show the state carries no store of secrets, and a mistake in it
// has nothing to read the key with (docs/DESIGN.md §7e, §7i).
func TestTheRolesThatShowTheStateHoldNoStoreAtAll(t *testing.T) {
	cfg := load(t, baseConfig+botConfig+orchestratorConfig)

	set, err := ToShow(cfg, newMachine().env(t))
	if err != nil {
		t.Fatalf("ToShow returned an error: %v", err)
	}
	adapter, ok := set.Forge.(*github.Adapter)
	if !ok {
		t.Fatalf("the host of the project is %T, want the adapter of GitHub", set.Forge)
	}
	if orchestrator := adapter.Orchestrator(); orchestrator == nil || orchestrator.Store != nil {
		t.Errorf("the app of the orchestrator of the project is %+v, want its number and no store of secrets", orchestrator)
	}
	if app := adapter.App(); app != nil {
		t.Errorf("the app of the executor is %+v, want none: a command that shows the state neither commits as a run nor opens its change request", app)
	}
	// The gate is made of the roles that hold the keys: a review writes its record in the
	// name of the App of the orchestrator, and that is a write (§7h, §7i).
	gate, err := AsOrchestrator(cfg, newMachine().env(t))
	if err != nil {
		t.Fatalf("AsOrchestrator returned an error: %v", err)
	}
	withKeys, ok := gate.Forge.(*github.Adapter)
	if !ok || withKeys.Orchestrator() == nil || withKeys.Orchestrator().Store == nil {
		t.Errorf("the app of the orchestrator of a review is %+v, want the store where its key is kept", gate.Forge)
	}
}

// machineOfTheNumbers is the machine of a project in the mode of a second App: what gh
// answers about the two accounts the lists of the file name, and a refusal of the question
// about the App behind a login, the way the host of a private App refuses it (F-109).
func machineOfTheNumbers() *machine {
	return newMachine().
		prints("api users/crewflow-orchestrator%5Bbot%5D",
			`{"id": 336252606, "login": "crewflow-orchestrator[bot]", "type": "Bot"}`).
		prints("api users/naghuale", `{"id": 93920024, "login": "naghuale", "type": "User"}`).
		fails("api apps/crewflow-orchestrator", "gh: Not Found (HTTP 404)")
}

// load reads a crewflow.toml of the test into the settings, and it writes the
// file into a folder of its own so that a test never reads the file of the
// project it runs in.
// settingsWithKind is the settings of a project whose role under the key is the
// kind, with the other two roles on the ones crewflow has an adapter for: a case
// of this file is about one role, and a role the file does not name is not the
// one under test.
func settingsWithKind(key, kind string) config.Config {
	cfg := config.Config{
		Project: config.Project{Repo: "naghuale/crewflow"},
		Forge:   config.Forge{Kind: "github"},
		Tracker: config.Tracker{Kind: "forge"},
		CI:      config.CI{Kind: "forge"},
	}
	switch key {
	case "forge.kind":
		cfg.Forge.Kind = kind
	case "tracker.kind":
		cfg.Tracker.Kind = kind
	case "ci.kind":
		cfg.CI.Kind = kind
	}
	return cfg
}

func load(t *testing.T, content string) config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "crewflow.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load of the settings of the test returned an error: %v", err)
	}
	return cfg
}

// machine is the machine the roles are built on: the programs in PATH and what
// gh writes, with everything it was asked kept.
type machine struct {
	// ran is what gh was asked, in order.
	ran []string
	// environment is the environment each of those commands was started with.
	environment [][]string
	// answers is what gh writes, by the first words of its command line, and refusals
	// is what it says when the host says no.
	answers  map[string]string
	refusals map[string]string
	// secrets is where the key of an App of a project would be kept. It is a store
	// of the test and never the keychain of a person: no test of crewflow opens the
	// secrets of the machine it runs on (docs/DESIGN.md §7i).
	secrets secret.Store
}

// newMachine returns a machine with every program installed and nothing
// answering: what a test needs of gh, it says with prints.
func newMachine() *machine {
	return &machine{answers: map[string]string{}, refusals: map[string]string{}, secrets: &storeOfTheTest{}}
}

// prints makes gh succeed and write output.
func (m *machine) prints(commandLine, output string) *machine {
	m.answers[commandLine] = output
	return m
}

// fails makes gh refuse the way it does when the host says no: the answer is on the error
// and the code is not zero, and the caller has to tell that from an answer.
func (m *machine) fails(commandLine, said string) *machine {
	m.refusals[commandLine] = said
	return m
}

// env is the environment the roles are built on, so that no test of the roles
// runs a real gh.
func (m *machine) env(t *testing.T) forge.Env {
	t.Helper()
	return forge.Env{
		LookPath: func(name string) (string, error) {
			return "/usr/bin/" + name, nil
		},
		Run: func(_ context.Context, _ string, args []string, _ string, env []string) ([]byte, []byte, int, error) {
			line := strings.Join(args, " ")
			m.ran = append(m.ran, line)
			m.environment = append(m.environment, env)
			words := strings.Join(args[:min(2, len(args))], " ")
			if said, refused := m.refusals[words]; refused {
				return nil, []byte(said + "\n"), 1, nil
			}
			return []byte(m.answers[words]), nil, 0, nil
		},
		ConfigPath: filepath.Join(t.TempDir(), "crewflow.toml"),
		Secrets:    m.secrets,
	}
}

// commandLine is what gh was asked, as a person would write it in a shell.
func (m *machine) commandLine(i int) string {
	if i >= len(m.ran) {
		return ""
	}
	return m.ran[i]
}
