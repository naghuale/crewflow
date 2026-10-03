package run

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/forge/roles"
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

// TestTheErrorOfAGitOfARunCarriesNoCredentialOfItsRoute: the git of a run goes out through
// the route of the project like the executor does, and what git says about a failure goes
// into the error the command prints and into the way out of the run. A git that was given a
// proxy with a login in it names that proxy in its complaint — `fatal: unable to access
// 'https://…': Could not resolve proxy`, the address in it — and the error of a run is a
// line of a terminal and of a file a person is sent to (D-068 RECHECK-FINDING-5,
// docs/DESIGN.md §7e, §7i).
func TestTheErrorOfAGitOfARunCarriesNoCredentialOfItsRoute(t *testing.T) {
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
	// git says what a proxy said to it, and the way out of the run is the file the command
	// tells the person to open.
	path := filepath.Join(t.TempDir(), "43-1.err")
	write(t, path, "crewflow: the executor may write: nowhere\n")
	m.answers["git fetch"] = answer{
		stderr: "fatal: unable to access 'https://github.com/naghuale/crewflow': " +
			"Could not resolve proxy: " + address + "\n",
		code: 128,
	}

	err := r.git(t.Context(), m.repo, "fetch", "origin", "main")
	r.note(path, err)

	if err == nil {
		t.Fatal("the git of a run that failed returned no error, want one with what git said in it")
	}
	for _, want := range []string{"git fetch origin main", "128"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error %q does not mention %q", err, want)
		}
	}
	nothingOfTheCredentials(t, "the error of the git of the run", err.Error(), theFormsOfTheCredentials(t, address, theCredentialsOfTheRoute))
	nothingOfTheCredentials(t, "the way out of the run", read(t, path), theFormsOfTheCredentials(t, address, theCredentialsOfTheRoute))
}

// nothingOfTheCredentials is that no value of the list is in the text, and which one is:
// the list of a run is the values crewflow read and never wrote down, and every one of them
// has to be out of every file and every line (docs/DESIGN.md §7e).
func nothingOfTheCredentials(t *testing.T, where, said string, values []string) {
	t.Helper()
	for _, value := range values {
		if value != "" && strings.Contains(said, value) {
			t.Errorf("%s holds the value %q of a credential:\n%s", where, value, said)
		}
	}
}

// The two pairs a proxy really takes, and both of them are the password of a person: one of
// three signs, which is a password somebody may have chosen, and one the address of the
// profile has to write another way (percent-encoded), which is the spelling a program is
// given and the only one that can come back out of a file of a run.
var theCredentialsOfACase = []string{"u:abc", "u:pa ss/word"}

// TestTheJournalTheWayOutAndTheStateOfARunHoldNoCredentialOfItsRoute is the guard of the
// three files a run leaves behind: the journal of the attempt, the way out of it and the
// state of the task. An executor that prints its own environment and stops by itself is the
// ordinary way the credentials of a route get into them — the environment of the executor
// holds the address of the profile with the pair in it, and the reason of a run goes into
// the state of the task, which an orchestrator reads and pastes into issues
// (D-068 RECHECK-FINDING-5, docs/DESIGN.md §7e, §7i).
func TestTheJournalTheWayOutAndTheStateOfARunHoldNoCredentialOfItsRoute(t *testing.T) {
	for _, pair := range theCredentialsOfACase {
		t.Run(pair, func(t *testing.T) {
			m := newMachine(t)
			c := aRunOnARouteWithCredentials(t, m, "proxy", pair, saidOfGh{})
			// The executor prints the route it was given and stops by itself, the way an
			// agent that cannot reach its provider does.
			m.answers["opencode"] = answer{
				stdout: `{"type":"text","sessionID":"ses_7fKq2","part":{"type":"text","text":"BLOCKED: ` +
					`I cannot reach ` + c.address + `"}}` + "\n",
				stderr: "curl: (97) cannot connect to " + c.address + "\n",
			}
			m.answers["gh issue view"] = answer{stdout: theIssueOfTheTask(t)}
			m.answers["gh pr list"] = answer{stdout: "[]\n"}
			env := m.env()
			env.Secrets = storeOfTheCredentialsOfTheProfile{pair: pair}
			env.Notices = c.notices

			result, err := Run(t.Context(), env, c.cfg, c.set, Request{Number: 43, RepoDir: m.repo})

			if err != nil {
				t.Fatalf("Run returned an error: %v", err)
			}
			// The route was really given to the programs of the run: a test that proves the
			// hiding of a value nobody was given proves nothing (docs/DESIGN.md §7d).
			if want := "HTTPS_PROXY=" + c.address; !contains(m.envOf(), want) {
				t.Fatalf("the executor was started without %q: %q", want, m.envOf())
			}
			if !strings.Contains(result.Reason, secret.Redacted) {
				t.Errorf("the reason of the run is %q, want the credentials of the route taken out of it", result.Reason)
			}
			for _, where := range []struct{ name, said string }{
				{"the journal of the attempt", read(t, result.Journal)},
				{"the way out of the run", read(t, result.ErrorJournal)},
				{"the state of the task", read(t, c.statePath(m))},
				{"the reason of the run", result.Reason},
			} {
				nothingOfTheCredentials(t, where.name, where.said, c.forms)
			}
		})
	}
}

// TestTheErrorOfTheHostOfTheProjectHoldsNoCredentialOfItsRoute: gh is started with the route
// of the project in its environment like every other program of a run, and what it says about
// a refusal goes into the error the command prints to the terminal of the person who started
// it. A gh that reached the host through a proxy names that proxy when the host refuses it —
// and the words of a refusal are the one part of a program nobody wrote
// (D-068 RECHECK-FINDING-5, docs/DESIGN.md §7e).
func TestTheErrorOfTheHostOfTheProjectHoldsNoCredentialOfItsRoute(t *testing.T) {
	for _, pair := range theCredentialsOfACase {
		t.Run(pair, func(t *testing.T) {
			m := newMachine(t)
			c := aRunOnARouteWithCredentials(t, m, "proxy", pair, saidOfGh{
				through: "HTTP 403: Resource not accessible by integration",
			})
			m.answers["opencode"] = answer{stdout: theRun}
			m.answers["gh issue view"] = answer{stdout: theIssueOfTheTask(t)}
			env := m.env()
			env.Secrets = storeOfTheCredentialsOfTheProfile{pair: pair}
			env.Notices = c.notices

			_, err := Run(t.Context(), env, c.cfg, c.set, Request{Number: 43, RepoDir: m.repo})

			if err == nil {
				t.Fatal("a run whose host refused it returned no error, want one")
			}
			// The refusal of the host is still there: only what of it is a secret is gone.
			if !strings.Contains(err.Error(), "HTTP 403") {
				t.Errorf("the error %q does not say what gh said", err)
			}
			if !strings.Contains(err.Error(), secret.Redacted) {
				t.Errorf("the error %q holds no %q, want the credentials of the route taken out of it",
					err, secret.Redacted)
			}
			nothingOfTheCredentials(t, "the error the command prints", err.Error(), c.forms)
		})
	}
}

// TestTheEventsOfTheRouteOfARunHoldNoCredentialOfTheProfileItWentThrough: in the mode
// `fallback` the one request that could not reach the network is made again through the
// active profile, and what each of the two attempts said is put into an event of the route —
// which goes into the journal of the attempt and into the terminal of the person who started
// the command. The credentials of that profile are in the environment of the second attempt
// and nowhere else, so this is the one place where they can be in a file of a run in the
// spelling the address writes them and not in the spelling of the store
// (D-068 RECHECK-FINDING-5, docs/DESIGN.md §7d, §7e).
func TestTheEventsOfTheRouteOfARunHoldNoCredentialOfTheProfileItWentThrough(t *testing.T) {
	for _, pair := range theCredentialsOfACase {
		t.Run(pair, func(t *testing.T) {
			m := newMachine(t)
			// Both attempts of the one request fail: the host is not there through either
			// road, and what the second one said names the profile it went out by.
			c := aRunOnARouteWithCredentials(t, m, "fallback", pair, saidOfGh{
				straight: "error connecting to api.github.com: dial tcp: lookup api.github.com: no such host",
				through:  "error connecting to api.github.com: dial tcp: lookup api.github.com: no such host",
			})
			m.answers["opencode"] = answer{stdout: theRun}
			m.answers["gh issue view"] = answer{stdout: theIssueOfTheTask(t)}
			env := m.env()
			env.Secrets = storeOfTheCredentialsOfTheProfile{pair: pair}
			env.Notices = c.notices

			result, err := Run(t.Context(), env, c.cfg, c.set, Request{Number: 43, RepoDir: m.repo})

			if err != nil {
				t.Fatalf("Run returned an error: %v", err)
			}
			for _, want := range []string{
				network.EventRouteFailed, network.EventFallbackStarted, network.EventFallbackFailed,
			} {
				if !strings.Contains(c.events.String(), want) {
					t.Errorf("the events of the route hold no %s: %q", want, c.events.String())
				}
			}
			for _, where := range []struct{ name, said string }{
				{"the events of the route", c.events.String()},
				{"the journal of the attempt", read(t, result.Journal)},
				{"the way out of the run", read(t, result.ErrorJournal)},
				{"the state of the task", read(t, c.statePath(m))},
			} {
				nothingOfTheCredentials(t, where.name, where.said, c.forms)
			}
		})
	}
}

// A run of a test on a project whose traffic goes through a profile of the owner, built the
// way a command builds it: the roles of the project are real, so every program they start
// goes out through the route of the project, the credentials of the profile are read where a
// connection through it is really made, and the events of the route are said to the notices
// (docs/DESIGN.md §7d, §7e).
type aRunOnARoute struct {
	cfg     config.Config
	set     forge.Set
	notices *secret.Notices
	// events is where the events of the route are said, apart from the journal of the
	// attempt they are added to as well.
	events *bytes.Buffer
	// address is what the programs of the run are given, and forms is every spelling of the
	// credentials of the profile that may come back out of a program.
	address string
	forms   []string
}

// saidOfGh is what gh says about the change request of the branch of a run, and it is said
// apart for the two roads: straight is what a gh that went out without a proxy says, and
// through is what a gh that went through the profile of the project says. What crewflow does
// with the two is not the same — the host answering and refusing ends the run where it is, and
// a proven failure of the network is the one failure the mode `fallback` answers with a second
// attempt (docs.DESIGN.md §7d, §7e).
//
// onStdout is which stream gh writes it to. A program of a host writes what it has to say to
// either of the two, and the answer of a second attempt is the stream an adapter reads into the
// error of a command when the complaint of that attempt is empty — so a value of the route in
// the answer of the second attempt is a value in an error of a command, and the values of the
// first route do not take it out (D-068 RECHECK-FINDING-5, §7e).
type saidOfGh struct {
	straight, through string
	onStdout          bool
}

// aRunOnARouteWithCredentials is that run with the given pair of the given mode: the store of
// the machine of the test holds the credentials of the profile, and nothing of it is the
// keychain of the person who runs the tests (§7e, §7i).
//
// gh is the program the roles of the project start, and it is a program of the machine of the
// test like any other: it answers what it was told, and it tells the route it went out by —
// the credentials of a profile are in the environment of the attempt through it and in no
// other, so only that attempt may name the profile in what it says (§7d, §7e).
func aRunOnARouteWithCredentials(t *testing.T, m *machine, mode, pair string, said saidOfGh) aRunOnARoute {
	t.Helper()
	cfg := projectOf(t, m.worktrees, "")
	cfg.Forge.Kind, cfg.Tracker.Kind, cfg.CI.Kind = "github", "forge", "forge"
	cfg.Network = config.Network{
		Mode:        mode,
		ActiveProxy: "work",
		Proxies: map[string]config.Proxy{
			"work": {Type: "socks5", Host: "proxy.example.com", Port: 1080, Credentials: "secret-store"},
		},
	}
	through, err := network.Through(cfg, "work")
	if err != nil {
		t.Fatalf("the route through the profile of the project: %v", err)
	}
	address, err := through.Address(pair)
	if err != nil {
		t.Fatalf("the address of the profile: %v", err)
	}
	// The complaint of the attempt through the profile names the profile it went out by: a
	// proxy that gives its own login away in a complaint is not rare, and the journal of a
	// run keeps for ever what it says. The straight attempt cannot name it — it was never
	// given the credentials — and says nothing but the network of the machine.
	said.through += ", through " + address + " as " + pair
	run := func(ctx context.Context, name string, args []string, dir string, extraEnv []string) ([]byte, []byte, int, error) {
		if filepath.Base(name) != "gh" || len(args) == 0 || args[0] != "pr" {
			return m.exec(ctx, name, args, dir, extraEnv)
		}
		m.started("gh", args, dir)
		m.wasGiven(extraEnv)
		complaint := said.straight
		if said.through != "" && slices.ContainsFunc(extraEnv, func(name string) bool {
			return strings.HasPrefix(name, "ALL_PROXY=")
		}) {
			complaint = said.through
		}
		if said.onStdout {
			return []byte(complaint), nil, 1, nil
		}
		return nil, []byte(complaint), 1, nil
	}
	events := &bytes.Buffer{}
	notices := secret.NewNotices(events)
	set, err := roles.New(cfg, forge.Env{
		LookPath:   func(name string) (string, error) { return filepath.Join(m.userHome, name), nil },
		Run:        run,
		ConfigPath: filepath.Join(t.TempDir(), "crewflow.toml"),
		Secrets:    storeOfTheCredentialsOfTheProfile{pair: pair},
		Now:        m.now,
		Events:     notices,
	})
	if err != nil {
		t.Fatalf("the roles of the project: %v", err)
	}
	return aRunOnARoute{
		cfg:     cfg,
		set:     set,
		notices: notices,
		events:  events,
		address: address,
		forms:   theFormsOfTheCredentials(t, address, pair),
	}
}

// theFormsOfTheCredentials are every spelling of the credentials of a profile that may reach
// a file of a run: the pair as the store keeps it, the password on its own, and the pair and
// the password as the address of the profile writes them. A value that is taken out of a text
// in one spelling and stands in it in another is a secret in every file a program writes
// (docs/DESIGN.md §7e).
func theFormsOfTheCredentials(t *testing.T, address, pair string) []string {
	t.Helper()
	parsed, err := url.Parse(address)
	if err != nil {
		t.Fatalf("the address of the profile is not an address: %v", err)
	}
	_, password, _ := strings.Cut(pair, ":")
	written := parsed.User.String()
	_, encoded, _ := strings.Cut(written, ":")
	return []string{pair, password, written, encoded}
}

// statePath is the file the state of the task of a test is in, read as it was written: the
// whole of what a person and an orchestrator read after the run is over.
func (c aRunOnARoute) statePath(m *machine) string {
	return newJournals(m.home, "naghuale-crewflow").StatePath(43)
}

// storeOfTheCredentialsOfTheProfile is the store of a machine that holds the credentials of
// the profile a run goes out through, whatever they are in this case, and nothing else.
type storeOfTheCredentialsOfTheProfile struct{ pair string }

func (s storeOfTheCredentialsOfTheProfile) Get(service, account string) ([]byte, error) {
	if service == secret.Service && account == secret.ProxyKey("work") {
		return []byte(s.pair), nil
	}
	return nil, secret.ErrNotFound
}

func (storeOfTheCredentialsOfTheProfile) Set(string, string, []byte) error { return nil }

// theIssueOfTheTask is the answer of "gh issue view" for the task of a test, in the shape gh
// writes it: the task of a project is read through the route of the project like any other
// program is started, and a run that reads it has to get the whole of it (docs/DESIGN.md §7g).
func theIssueOfTheTask(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf(`{"number":43,"title":"the run of a task","body":%s,"labels":[],`+
		`"state":"open","url":"https://github.com/naghuale/crewflow/issues/43"}`, strconv.Quote(wholeTask))
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

// TestTheDiagnosticsOfTheSecondAttemptGoThroughTheCredentialsOfBothRoutes: the mode
// `fallback` makes one operation twice, once straight and once through the active profile of
// the project, and the answer of the second attempt is read after the credentials of the
// second route were read for it. What gh wrote about the refusal of the host is in that
// answer — gh writes what it has to say to either of its streams, and a program of the host
// that goes through a proxy names the proxy in it — and the error a command prints is made of
// what gh wrote. Cleaning it with the values of the first route alone is a password of a proxy
// in a line of the terminal of the person and in the state of a task (D-068
// RECHECK-FINDING-5, docs.DESIGN.md §7e, §7i).
func TestTheDiagnosticsOfTheSecondAttemptGoThroughTheCredentialsOfBothRoutes(t *testing.T) {
	for _, pair := range theCredentialsOfACase {
		t.Run(pair, func(t *testing.T) {
			m := newMachine(t)
			// The straight attempt could not reach the network at all, and the second attempt
			// reached the host and was refused there — with the name of the profile it went out
			// by in the words it wrote to its answer.
			c := aRunOnARouteWithCredentials(t, m, "fallback", pair, saidOfGh{
				straight: "error connecting to api.github.com: dial tcp: lookup api.github.com: no such host",
				through:  "HTTP 403: Resource not accessible by integration",
				onStdout: true,
			})
			m.answers["opencode"] = answer{stdout: theRun}
			m.answers["gh issue view"] = answer{stdout: theIssueOfTheTask(t)}
			env := m.env()
			env.Secrets = storeOfTheCredentialsOfTheProfile{pair: pair}
			env.Notices = c.notices

			_, err := Run(t.Context(), env, c.cfg, c.set, Request{Number: 43, RepoDir: m.repo})

			if err == nil {
				t.Fatal("a run whose host refused it returned no error, want one with what gh wrote in it")
			}
			// The refusal of the host is still there: only what of it is a secret is gone.
			if !strings.Contains(err.Error(), "HTTP 403") {
				t.Errorf("the error %q does not say what gh wrote", err)
			}
			if !strings.Contains(err.Error(), secret.Redacted) {
				t.Errorf("the error %q holds no %q, want the credentials of the route of the second "+
					"attempt taken out of it", err, secret.Redacted)
			}
			// The profile was really the road of the second attempt: a test that proves the
			// cleaning of a value nobody was given proves nothing (docs/DESIGN.md §7d).
			if !strings.Contains(c.address, "@proxy.example.com:1080") {
				t.Fatalf("the address of the profile is %q, want the credentials in it", c.address)
			}
			nothingOfTheCredentials(t, "the error the command prints", err.Error(), c.forms)
		})
	}
}

// TestTheCanaryOfTheRunIsNowhereInWhatTheRunPublishes: the guard of the whole of it. A run
// whose route goes through a profile with a canary in it — a short one and a long one, in every
// spelling the address of the profile writes them — is watched in every place a person or a
// program reads what came of it: the error the command prints, the journal of the attempt, the
// way out of it, the events of the route, the state of the task and the report of the run. The
// words of the run are all there; the canary is in none of them (D-068 RECHECK-FINDING-5,
// docs/DESIGN.md §7e, §7i).
func TestTheCanaryOfTheRunIsNowhereInWhatTheRunPublishes(t *testing.T) {
	for _, tc := range []struct{ name, pair string }{
		{name: "a password of three signs", pair: theCredentialsOfACase[0]},
		{name: "a password the address writes another way", pair: theCredentialsOfACase[1]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pair := tc.pair
			m := newMachine(t)
			// The host refuses the change request of the branch with the name of the profile
			// in what it wrote, and the executor has printed its own environment on the way:
			// every way a value of the run comes back into a text of a run.
			c := aRunOnARouteWithCredentials(t, m, "proxy", pair, saidOfGh{
				through: "HTTP 403: Resource not accessible by integration",
			})
			m.answers["opencode"] = answer{
				stdout: "HTTPS_PROXY=" + c.address + "\n" + theRun,
				stderr: "curl: (97) cannot connect to " + c.address + "\n",
			}
			m.answers["gh issue view"] = answer{stdout: theIssueOfTheTask(t)}
			env := m.env()
			env.Secrets = storeOfTheCredentialsOfTheProfile{pair: pair}
			env.Notices = c.notices

			result, err := Run(t.Context(), env, c.cfg, c.set, Request{Number: 43, RepoDir: m.repo})

			if err == nil {
				t.Fatal("a run whose host refused it returned no error, want one with what gh wrote in it")
			}
			published := []struct{ name, said string }{
				{"the error the command prints", err.Error()},
				{"the journal of the attempt", read(t, result.Journal)},
				{"the way out of the run", read(t, result.ErrorJournal)},
				{"the events of the route", c.events.String()},
				{"the state of the task", read(t, c.statePath(m))},
				{"the reason of the run", result.Reason},
			}
			for _, refusal := range result.Rejections {
				published = append(published, struct{ name, said string }{"a refusal of the run", refusal})
			}
			for _, where := range published {
				nothingOfTheCredentials(t, where.name, where.said, c.forms)
			}
			// And the words are: a journal that says only `[redacted]` says that something was
			// there and nothing of what the run did (docs/DESIGN.md §7e).
			journal := read(t, result.Journal)
			if !strings.Contains(journal, secret.Redacted) || !strings.Contains(journal, "proxy.example.com:1080") {
				t.Errorf("the journal of the attempt is %q, want the words of the run with only the "+
					"credentials of the route taken out of them", journal)
			}
		})
	}
}

// TestTheReportOfARefusalHoldsNoSecretOfTheRun: a run that reached for a secret is reported
// twice — the outcome of the run and the line of its journal — and the report names the
// command the refusal came in with, because a command of a shell is refused whole and a
// person has to see what it was. A command may hold a token of a run, and a report about a
// secret of a run that carries that secret is a report that puts it into an issue
// (docs/DESIGN.md §7a.1, §7e, §7i).
func TestTheReportOfARefusalHoldsNoSecretOfTheRun(t *testing.T) {
	pair := theCredentialsOfACase[1]
	m := newMachine(t)
	c := aRunOnARouteWithCredentials(t, m, "proxy", pair, saidOfGh{})
	// The run was refused the key of the person and named the credentials of the route in the
	// command it was refused in: an agent that reads its own environment into a command is
	// an agent that hands a proxy password to the machinery of the refusals.
	m.answers["opencode"] = answer{
		stdout: theRun,
		stderr: "! permission requested: read (" + c.address + "); auto-rejecting\n" +
			"curl --proxy socks5://" + pair + "@proxy.example.com:1080 https://api.github.com\n",
	}
	m.answers["gh issue view"] = answer{stdout: theIssueOfTheTask(t)}
	env := m.env()
	env.Secrets = storeOfTheCredentialsOfTheProfile{pair: pair}
	env.Notices = c.notices

	result, err := Run(t.Context(), env, c.cfg, c.set, Request{Number: 43, RepoDir: m.repo})

	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}
	if len(result.Rejections) == 0 {
		t.Fatal("the report of the run holds no refusal, want the one the way out of it says")
	}
	// The refusal is reported: what the run was refused is the first thing an orchestrator
	// reads about it, and a report that says nothing of it is a report about nothing (§7a.1).
	if !strings.Contains(strings.Join(result.Rejections, " "), "read") {
		t.Errorf("the refusals of the run are %q, want what the run was refused", result.Rejections)
	}
	for _, where := range []struct{ name, said string }{
		{"a refusal of the report of the run", strings.Join(result.Rejections, "\n")},
		{"the journal of the attempt", read(t, result.Journal)},
		{"the way out of the run", read(t, result.ErrorJournal)},
		{"the state of the task", read(t, c.statePath(m))},
		{"the events of the route", c.events.String()},
	} {
		nothingOfTheCredentials(t, where.name, where.said, c.forms)
	}
}
