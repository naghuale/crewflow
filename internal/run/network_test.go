package run

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/network"
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
			"home": {Type: "http", Host: "192.0.2.10", Port: 1082, Credentials: "none"},
		},
	}

	if _, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo}); err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	for _, want := range []string{
		"HTTP_PROXY=http://192.0.2.10:1082",
		"HTTPS_PROXY=http://192.0.2.10:1082",
		"ALL_PROXY=http://192.0.2.10:1082",
		"NO_PROXY=.internal.example",
	} {
		if !contains(m.envOf(), want) {
			t.Errorf("the executor was started without %q: %q", want, m.envOf())
		}
	}
	// The git of the run goes out the same way: a run that fetches through the profile of
	// the project and starts its executor beside it is a run whose code came from a
	// different machine than the one it writes on.
	for _, want := range []string{"HTTP_PROXY=http://192.0.2.10:1082"} {
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
			"home": {Type: "http", Host: "192.0.2.10", Port: 1082, Credentials: "none"},
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

// theCredentialsOfTheRoute are what the store of a machine holds for the profile a run
// goes out through: a login and a password, as the store of a proxy profile keeps them.
// The password has signs in it that an address writes another way, because a value that is
// taken out of a text in one spelling and stands in it in another is a secret in every file
// a program writes (docs/DESIGN.md §7e).
const theCredentialsOfTheRoute = "relay:s3cret with/a space"

// thePasswordOfTheRoute is the password of that profile on its own, which is what an agent
// printing its own environment puts next to the name of the login.
const thePasswordOfTheRoute = "s3cret with/a space"

// aRouteThatTakesItsCredentials is the network of a project whose traffic goes through a
// proxy of the owner, whatever the run does: the credentials are in the environment of
// every program the run starts, and a project whose route takes none proves nothing about
// a run that hands them out (docs/DESIGN.md §7d, §7e).
func aRouteThatTakesItsCredentials() config.Network {
	return config.Network{
		Mode:        "proxy",
		ActiveProxy: "work",
		Proxies: map[string]config.Proxy{
			"work": {Type: "socks5", Host: "proxy.example.com", Port: 1080, Credentials: "secret-store"},
		},
	}
}

// storeWithTheRoute is the store of a machine that holds the credentials of the profile a
// run goes out through, and nothing else: a run of a test reads no keychain of a person
// and writes none (§7e, §7i).
type storeWithTheRoute struct{}

func (storeWithTheRoute) Get(service, account string) ([]byte, error) {
	if service == secret.Service && account == secret.ProxyKey("work") {
		return []byte(theCredentialsOfTheRoute), nil
	}
	return nil, secret.ErrNotFound
}

func (storeWithTheRoute) Set(string, string, []byte) error { return nil }

// theAddressOfTheRoute is what a run really hands its executor and its own git, with the
// credentials of the profile in it: a test says what an agent would print out of it, and
// what it prints has to be the value a machine writes, percent-encoding and all (§7e).
func theAddressOfTheRoute(t *testing.T, networkOf config.Network) string {
	t.Helper()
	route, err := network.Choose(config.Config{Network: networkOf}, "")
	if err != nil {
		t.Fatalf("the route of the project: %v", err)
	}
	address, err := route.Address(theCredentialsOfTheRoute)
	if err != nil {
		t.Fatalf("the address of the profile: %v", err)
	}
	return address
}

// TestARunKeepsTheCredentialsOfTheRouteOutOfItsFiles: the credentials of the profile the
// owner chose are in the environment of every program a run starts, and an executor that
// prints its own environment is one line of a journal — a file kept for ever and pasted
// into issues. The token of the identity was taken out of it since the mode of the bot
// began; the password of the proxy was not, so a project whose route takes credentials had
// it in the journal of every run and in the way out of it (D-068 FINDING-5, docs/DESIGN.md
// §7e, §7i).
func TestARunKeepsTheCredentialsOfTheRouteOutOfItsFiles(t *testing.T) {
	m := newMachine(t)
	cfg := projectOf(t, m.worktrees, "")
	cfg.Network = aRouteThatTakesItsCredentials()
	address := theAddressOfTheRoute(t, cfg.Network)
	// The executor prints its own environment, as an agent asked about its environment
	// does, and its way out names the route that refused it.
	m.answers["opencode"] = answer{
		stdout: "HTTPS_PROXY=" + address + "\n",
		stderr: "curl: the route " + address + " refused the connection\n",
	}
	host := &host{task: taskOf(43), opened: true}
	env := m.env()
	env.Secrets = storeWithTheRoute{}

	result, err := Run(t.Context(), env, cfg, host.set(), Request{Number: 43, RepoDir: m.repo})

	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}
	// The executor was really given the route with the credentials in it: a test that
	// proves the redaction of a value nobody was given proves nothing (§7d).
	if want := "HTTPS_PROXY=" + address; !contains(m.envOf(), want) {
		t.Fatalf("the executor was started without %q: %q", want, m.envOf())
	}
	for _, file := range []struct{ name, path string }{
		{"journal", result.Journal},
		{"way out of the run", result.ErrorJournal},
	} {
		said := read(t, file.path)
		for _, secretOfTheRun := range []string{thePasswordOfTheRoute, theCredentialsOfTheRoute} {
			if strings.Contains(said, secretOfTheRun) {
				t.Errorf("the %s holds %q:\n%s", file.name, secretOfTheRun, said)
			}
		}
		if !strings.Contains(said, secret.Redacted) {
			t.Errorf("the %s holds %q, want the credentials of the route taken out of it", file.name, said)
		}
	}
	// The address of the profile is left as it was: a person who reads the journal of a
	// run has to see where the traffic of the project went, and a redaction that took the
	// address with it would be a second secret where there is one address (§7d, §7e).
	if got := read(t, result.Journal); !strings.Contains(got, "proxy.example.com:1080") {
		t.Errorf("the journal holds %q, want the address of the profile of the route in it", got)
	}
}

// TestTheErrorsOfARunGoThroughTheSecretsOfItsRoute: a line crewflow writes about a run is
// a line of a file of the run, and an error of a run carries what a program of it printed
// — a program of a run is started with the credentials of the route in its environment.
// The errors of a run go through the values of the identity alone since the mode of the
// bot, and a project whose route takes credentials had a password of its proxy in the
// way out of a run that failed (D-068 FINDING-5, docs/DESIGN.md §7e).
func TestTheErrorsOfARunGoThroughTheSecretsOfItsRoute(t *testing.T) {
	m := newMachine(t)
	cfg := projectOf(t, m.worktrees, "")
	cfg.Network = aRouteThatTakesItsCredentials()
	address := theAddressOfTheRoute(t, cfg.Network)
	env := m.env()
	env.Secrets = storeWithTheRoute{}
	r := &runner{env: env, cfg: cfg}
	if err := r.throughRoute(); err != nil {
		t.Fatalf("work out the route of the run: %v", err)
	}
	path := filepath.Join(t.TempDir(), "43-1.err")
	write(t, path, "crewflow: the executor may write: nowhere\n")

	r.note(path, fmt.Errorf("the change request of the branch: the route %s is refused", address))

	said := read(t, path)
	for _, secretOfTheRun := range []string{thePasswordOfTheRoute, theCredentialsOfTheRoute} {
		if strings.Contains(said, secretOfTheRun) {
			t.Errorf("the way out of the run holds %q:\n%s", secretOfTheRun, said)
		}
	}
	if !strings.Contains(said, secret.Redacted) {
		t.Errorf("the way out of the run holds %q, want the credentials of the route taken out of it", said)
	}
}

// TestWhatTheRunSaidAboutItselfGoesThroughTheSecretsOfItsRoute: crewflow writes the words
// of the agent and of the provider into places that are not the output of the executor: the
// reason of a run is in the state of the task and in the report of the run, and the words
// of the provider are in the event of the journal of the attempt. An agent that stopped
// because it could not reach its provider and quoted the route it was given puts the
// password of the proxy into all of them — and both the state of a task and a report of a
// run are pasted into issues (D-068 FINDING-5, docs/DESIGN.md §7e, §7i).
func TestWhatTheRunSaidAboutItselfGoesThroughTheSecretsOfItsRoute(t *testing.T) {
	m := newMachine(t)
	cfg := projectOf(t, m.worktrees, "")
	cfg.Network = aRouteThatTakesItsCredentials()
	address := theAddressOfTheRoute(t, cfg.Network)
	// The provider refused the run and named the route it went out through, and crewflow
	// went on once by itself; the attempt after it stopped by itself and named the same
	// route again, this time in the words of the agent (F-119).
	m.says("opencode",
		answer{
			stdout: `{"type":"error","sessionID":"ses_7fKq2","part":{"type":"error","error":{"name":"AI_APICallError",` +
				`"data":{"message":"Cannot connect through ` + address + `","isRetryable":true}}}}` + "\n",
			code: 1,
		},
		answer{
			stdout: `{"type":"text","sessionID":"ses_7fKq2","part":{"text":"BLOCKED: I cannot reach ` +
				address + `"}}` + "\n",
		})
	host := &host{task: taskOf(43), opened: true}
	env := m.env()
	env.Secrets = storeWithTheRoute{}

	result, err := Run(t.Context(), env, cfg, host.set(), Request{Number: 43, RepoDir: m.repo})

	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}
	state := stateOf(t, m, 43)
	if len(state.Attempts) != 2 {
		t.Fatalf("the state holds %d attempts, want 2: the refusal of the provider and the attempt after it",
			len(state.Attempts))
	}
	refused := state.Attempts[0]
	for _, where := range []struct {
		name, said string
	}{
		{"the reason of the attempt the provider refused", refused.Reason},
		{"the journal of the attempt the provider refused", read(t, refused.Journal)},
		{"the reason of the run", result.Reason},
	} {
		if strings.Contains(where.said, thePasswordOfTheRoute) {
			t.Errorf("%s holds the password of the route:\n%s", where.name, where.said)
		}
		if !strings.Contains(where.said, secret.Redacted) {
			t.Errorf("%s holds %q, want the credentials of the route taken out of it", where.name, where.said)
		}
	}
	// The words are still there — only what of them is a secret is gone: a reason of
	// `[redacted]` alone would say that something was refused and nothing of what.
	if want := ReasonProviderUnavailable + ": Cannot connect through socks5://" + secret.Redacted +
		"@proxy.example.com:1080"; refused.Reason != want {
		t.Errorf("the reason of the attempt the provider refused = %q, want %q", refused.Reason, want)
	}
	if want := "I cannot reach socks5://" + secret.Redacted + "@proxy.example.com:1080"; result.Reason != want {
		t.Errorf("the reason of the run = %q, want the words of the agent with the credentials taken out: %q",
			result.Reason, want)
	}
}

// contains is whether the environment a program was started with holds the name.
func contains(environment []string, name string) bool {
	for _, one := range environment {
		if one == name {
			return true
		}
	}
	return false
}
