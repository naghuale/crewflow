package run

import (
	"strings"
	"testing"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/secret"
)

// TestCFNET002TheRouteOfTheProjectReachesTheExecutorAndTheGitOfTheRun: the four names of
// the profile the owner chose are added to the environment of the programs a run starts
// and to nothing else — the global environment of the machine, the settings of git and
// the settings of the person stay as they were, because a proxy of a project is a choice
// of that project (docs/DESIGN.md §7d).
func TestCFNET002TheRouteOfTheProjectReachesTheExecutorAndTheGitOfTheRun(t *testing.T) {
	m := newMachine(t)
	m.answers["opencode"] = answer{stdout: theRun}
	host := &host{task: taskOf(43), opened: true}
	cfg := projectOf(t, m.worktrees, "")
	cfg.Network = config.Network{
		Mode:        "proxy",
		ActiveProxy: "home",
		NoProxy:     []string{".internal.example"},
		Proxies: map[string]config.Proxy{
			"home": {Type: "http", Host: "192.168.0.4", Port: 1082, Credentials: "none"},
		},
	}

	if _, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo}); err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	for _, want := range []string{
		"HTTP_PROXY=http://192.168.0.4:1082",
		"HTTPS_PROXY=http://192.168.0.4:1082",
		"ALL_PROXY=http://192.168.0.4:1082",
		"NO_PROXY=.internal.example",
	} {
		if !contains(m.envOf(), want) {
			t.Errorf("the executor was started without %q: %q", want, m.envOf())
		}
	}
	// The git of the run goes out the same way: a run that fetches through the profile of
	// the project and starts its executor beside it is a run whose code came from a
	// different machine than the one it writes on.
	for _, want := range []string{"HTTP_PROXY=http://192.168.0.4:1082"} {
		if !contains(m.givenToPrograms(), want) {
			t.Errorf("the programs of the run were started without %q: %q", want, m.givenToPrograms())
		}
	}
}

// TestCFNET001AStraightRouteAddsNothingToTheExecutor: a variable of the shell of a person
// is a route nobody chose, and a project that says nothing about the network goes out as it
// is — a project that named a profile without making it the active one has prepared, not
// switched (docs/DESIGN.md §7d).
func TestCFNET001AStraightRouteAddsNothingToTheExecutor(t *testing.T) {
	m := newMachine(t)
	m.answers["opencode"] = answer{stdout: theRun}
	host := &host{task: taskOf(43), opened: true}
	cfg := projectOf(t, m.worktrees, "")
	cfg.Network = config.Network{
		Mode: "direct",
		Proxies: map[string]config.Proxy{
			"home": {Type: "http", Host: "192.168.0.4", Port: 1082, Credentials: "none"},
		},
	}

	if _, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo}); err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	for _, name := range m.envOf() {
		if strings.Contains(name, "_PROXY=") {
			t.Errorf("the executor of a run in the mode %q was given %q, want no route at all", cfg.Network.Mode, name)
		}
	}
}

// TestTheRunDoesNotStartOnAProfileWhoseCredentialsAreMissing: a run whose proxy answers
// 407 is a run that stopped in the middle of a task, and the file of the project can be
// checked before anything of the run is started (docs/DESIGN.md §7d, §7e).
func TestTheRunDoesNotStartOnAProfileWhoseCredentialsAreMissing(t *testing.T) {
	m := newMachine(t)
	m.answers["opencode"] = answer{stdout: theRun}
	host := &host{task: taskOf(43), opened: true}
	cfg := projectOf(t, m.worktrees, "")
	cfg.Network = config.Network{
		Mode:        "proxy",
		ActiveProxy: "work",
		Proxies: map[string]config.Proxy{
			"work": {Type: "socks5", Host: "proxy.example.com", Port: 1080, Credentials: "secret-store"},
		},
	}
	env := m.env()
	env.Secrets = &storeWithoutAnything{}

	if _, err := Run(t.Context(), env, cfg, host.set(), Request{Number: 43, RepoDir: m.repo}); err == nil {
		t.Fatal("a run of a profile whose credentials are in no store returned no error, want a refusal")
	} else if !strings.Contains(err.Error(), secret.ProxyKey("work")) {
		t.Errorf("the refusal %q does not name the item of the store", err)
	}
	if len(m.envOf()) != 0 {
		t.Errorf("the executor was started anyway with %q", m.envOf())
	}
}

// storeWithoutAnything is a store of a machine where nothing of this project was kept.
type storeWithoutAnything struct{}

func (storeWithoutAnything) Get(string, string) ([]byte, error) { return nil, secret.ErrNotFound }

func (storeWithoutAnything) Set(string, string, []byte) error { return nil }

// contains is whether the environment a program was started with holds the name.
func contains(environment []string, name string) bool {
	for _, one := range environment {
		if one == name {
			return true
		}
	}
	return false
}
