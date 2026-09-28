package roles

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	cfg := load(t, baseConfig+"\n[forge]\nkind = \"none\"\n\n[tracker]\nkind = \"files\"\n\n[ci]\nkind = \"none\"\n")

	hosting, err := hostOf(cfg, newMachine().env(t), cfg.Identity.Mode)
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
func TestNewNotImplemented(t *testing.T) {
	cases := []struct {
		name   string
		config string
		key    string
		kind   string
	}{
		{"gitlab", baseConfig + "\n[forge]\nkind = \"gitlab\"\n", "forge.kind", "gitlab"},
		{"bitbucket", baseConfig + "\n[forge]\nkind = \"bitbucket\"\n", "forge.kind", "bitbucket"},
		{"jira", baseConfig + "\n[tracker]\nkind = \"jira\"\n", "tracker.kind", "jira"},
		{"linear", baseConfig + "\n[tracker]\nkind = \"linear\"\n", "tracker.kind", "linear"},
		{"files", baseConfig + "\n[tracker]\nkind = \"files\"\n", "tracker.kind", "files"},
		{"jenkins", baseConfig + "\n[ci]\nkind = \"jenkins\"\n", "ci.kind", "jenkins"},
		{"command", baseConfig + "\n[ci]\nkind = \"command\"\n", "ci.kind", "command"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(load(t, tc.config), newMachine().env(t))
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

// load reads a crewflow.toml of the test into the settings, and it writes the
// file into a folder of its own so that a test never reads the file of the
// project it runs in.
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
	// answers is what gh writes, by the first words of its command line.
	answers map[string]string
	// secrets is where the key of an App of a project would be kept. It is a store
	// of the test and never the keychain of a person: no test of crewflow opens the
	// secrets of the machine it runs on (docs/DESIGN.md §7i).
	secrets secret.Store
}

// newMachine returns a machine with every program installed and nothing
// answering: what a test needs of gh, it says with prints.
func newMachine() *machine {
	return &machine{answers: map[string]string{}, secrets: &storeOfTheTest{}}
}

// prints makes gh succeed and write output.
func (m *machine) prints(commandLine, output string) *machine {
	m.answers[commandLine] = output
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
			m.ran = append(m.ran, strings.Join(args, " "))
			m.environment = append(m.environment, env)
			return []byte(m.answers[strings.Join(args[:min(2, len(args))], " ")]), nil, 0, nil
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
