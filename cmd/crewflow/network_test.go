package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/doctor"
	"github.com/naghuale/crewflow/internal/network"
	"github.com/naghuale/crewflow/internal/secret"
)

// The route of the project every case of the network starts from: straight out, with no
// profile named at all (docs/DESIGN.md §7d).
const plainProject = projectConfig

// useNetworkOfTheTest is the machine the commands about the network work on: a store of
// secrets, a clock that does not move, and a check of a route that answers what the case
// says it answers. No test of a command opens the keychain of the person who runs it and
// no test asks the network of the machine the tests run on.
func useNetworkOfTheTest(t *testing.T, store secret.Store, states []network.State) {
	t.Helper()
	oldStore, oldNow, oldCheck := secretsOfTheNetwork, clockOfTheNetwork, checksOfARoute
	t.Cleanup(func() {
		secretsOfTheNetwork, clockOfTheNetwork, checksOfARoute = oldStore, oldNow, oldCheck
	})
	secretsOfTheNetwork = func() secret.Store { return store }
	clockOfTheNetwork = func() time.Time { return time.Date(2026, time.October, 2, 9, 0, 0, 0, time.UTC) }
	checksOfARoute = func(context.Context, config.Config, network.Route, secret.Store) ([]network.State, error) {
		return states, nil
	}
}

// storeOfTheRoute is the store of secrets of a machine of a test: it keeps values by the
// name of the item, so that the credentials of a profile and the key of an App are two
// different things.
type storeOfTheRoute struct {
	values map[string]string
	reads  int
	writes int
	takes  []string
}

func (s *storeOfTheRoute) Get(service, account string) ([]byte, error) {
	s.reads++
	value, known := s.values[service+"/"+account]
	if !known {
		return nil, secret.ErrNotFound
	}
	return []byte(value), nil
}

func (s *storeOfTheRoute) Set(service, account string, value []byte) error {
	s.writes++
	if s.values == nil {
		s.values = map[string]string{}
	}
	s.values[service+"/"+account] = string(value)
	return nil
}

func (s *storeOfTheRoute) Has(service, account string) (bool, error) {
	_, known := s.values[service+"/"+account]
	return known, nil
}

func (s *storeOfTheRoute) Delete(service, account string) error {
	s.takes = append(s.takes, service+"/"+account)
	delete(s.values, service+"/"+account)
	return nil
}

// say runs a command and answers what it wrote and the code it ended with.
func say(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return stdout.String(), stderr.String(), code
}

// TestTheRouteOfAProjectIsChangedWithCommands: the whole of what a person does on 02.10
// when the proxy moved — add a profile, look at it, make it the active one, switch the
// route to it — and the file of the project says what the person said and nothing else.
func TestTheRouteOfAProjectIsChangedWithCommands(t *testing.T) {
	useNetworkOfTheTest(t, &storeOfTheRoute{}, nil)
	project := writeConfig(t, plainProject)

	stdout, stderr, code := say(t, "network", "proxy", "add", "home",
		"--type", "http", "--host", "192.168.0.4", "--port", "1082", "-config", project)
	if code != exitOK {
		t.Fatalf("add a profile = %d, want %d (stderr: %q)", code, exitOK, stderr)
	}
	if !strings.Contains(stdout, "192.168.0.4") {
		t.Errorf("the answer of the command is %q, want the address of the profile in it", stdout)
	}
	// The event of the route is written where a program and a person read it, and it
	// holds the profile and no credential of it.
	if !strings.Contains(stderr, network.EventProfileChanged) || !strings.Contains(stderr, "profile=home") {
		t.Errorf("stderr of the change is %q, want the event of the profile in it", stderr)
	}

	if _, _, code = say(t, "network", "proxy", "list", "-config", project); code != exitOK {
		t.Fatalf("list the profiles = %d, want %d", code, exitOK)
	}
	if _, _, code = say(t, "network", "proxy", "use", "home", "-config", project); code != exitOK {
		t.Fatalf("use the profile home = %d, want %d", code, exitOK)
	}
	if _, _, code = say(t, "network", "mode", "proxy", "-config", project); code != exitOK {
		t.Fatalf("switch the mode = %d, want %d", code, exitOK)
	}

	cfg, err := config.Load(project)
	if err != nil {
		t.Fatalf("the file of the project does not load after the commands: %v", err)
	}
	if cfg.Network.Mode != "proxy" || cfg.Network.ActiveProxy != "home" {
		t.Errorf("the route came out as mode %q through %q, want proxy through home", cfg.Network.Mode, cfg.Network.ActiveProxy)
	}
	route, err := network.Choose(cfg, "api.github.com")
	if err != nil {
		t.Fatalf("the route of a request: %v", err)
	}
	if route.Name != "home" || route.Profile.Port != 1082 {
		t.Errorf("the route came out through %q on the port %d, want home on 1082", route.Name, route.Profile.Port)
	}
}

// TestTheActiveProfileIsNotRemovedAndTheOtherOneIs: a route that names a profile nobody
// defined is a file that does not load, and the refusal names the way out.
func TestTheActiveProfileIsNotRemovedAndTheOtherOneIs(t *testing.T) {
	useNetworkOfTheTest(t, &storeOfTheRoute{}, nil)
	project := writeConfig(t, plainProject+`
[network]
mode = "proxy"
active_proxy = "home"

[network.proxies.home]
type = "http"
host = "192.168.0.4"
port = 1082
credentials = "none"

[network.proxies.work]
type = "socks5"
host = "proxy.example.com"
port = 1080
credentials = "none"
`)

	_, stderr, code := say(t, "network", "proxy", "remove", "home", "-config", project)

	if code != exitFailure {
		t.Fatalf("removing the active profile = %d, want %d", code, exitFailure)
	}
	if !strings.Contains(stderr, "crewflow network mode") {
		t.Errorf("the refusal %q does not say what to do first", stderr)
	}
	if _, stderr, code = say(t, "network", "proxy", "remove", "work", "-config", project); code != exitOK {
		t.Fatalf("removing another profile = %d, want %d (stderr: %q)", code, exitOK, stderr)
	}
	cfg, err := config.Load(project)
	if err != nil {
		t.Fatalf("the file of the project does not load: %v", err)
	}
	if _, still := cfg.Network.Proxies["work"]; still {
		t.Errorf("the profiles of the project are %s, want work out of them", network.Names(cfg))
	}
}

// TestAProfileIsPreparedWithoutBeingActive: a proxy can be written down before anybody
// made it the route, and `use` is what says so — the file does not switch the mode by
// itself, because a person who named a profile has not asked for the route to change.
func TestAProfileIsPreparedWithoutBeingActive(t *testing.T) {
	useNetworkOfTheTest(t, &storeOfTheRoute{}, nil)
	project := writeConfig(t, plainProject)

	if _, stderr, code := say(t, "network", "proxy", "add", "office",
		"--type", "https", "--host", "proxy.example.org", "--port", "3128", "-config", project); code != exitOK {
		t.Fatalf("add a profile = %d, want %d (stderr: %q)", code, exitOK, stderr)
	}
	stdout, _, code := say(t, "network", "proxy", "use", "office", "-config", project)
	if code != exitOK {
		t.Fatalf("use the profile = %d, want %d", code, exitOK)
	}
	if !strings.Contains(stdout, `the mode is still "direct"`) {
		t.Errorf("the answer of the command is %q, want it to say that the mode is still direct", stdout)
	}
	cfg, err := config.Load(project)
	if err != nil {
		t.Fatalf("the file of the project does not load: %v", err)
	}
	if cfg.Network.Mode != "direct" {
		t.Errorf("the mode came out as %q, want the mode the file had", cfg.Network.Mode)
	}
}

// TestAPortAndATypeAndAnAddressThatCrewflowCannotUseAreRefusedAndNothingIsWritten: the
// file of a project is the file every command of it reads, and a change that leaves a file
// nobody can work with is not a change.
func TestAPortAndATypeAndAnAddressThatCrewflowCannotUseAreRefusedAndNothingIsWritten(t *testing.T) {
	cases := []struct {
		name    string
		flags   []string
		mention string
	}{
		{"a port of zero", []string{"--type", "http", "--host", "h", "--port", "0"}, "needs --type, --host and --port"},
		{"a protocol of nobody", []string{"--type", "socks4", "--host", "h", "--port", "1080"}, "socks4"},
		{"an address with a login", []string{"--type", "http", "--host", "ann:s3cret@h", "--port", "1080"}, "store of secrets"},
		{"a host that is an address", []string{"--type", "http", "--host", "http://h", "--port", "1080"}, "scheme"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useNetworkOfTheTest(t, &storeOfTheRoute{}, nil)
			project := writeConfig(t, plainProject)
			before, err := os.ReadFile(project)
			if err != nil {
				t.Fatalf("read the file of the project: %v", err)
			}

			args := append([]string{"network", "proxy", "add", "office"}, tc.flags...)
			_, stderr, code := say(t, append(args, "-config", project)...)

			if code != exitFailure {
				t.Fatalf("adding %s = %d, want %d", tc.name, code, exitFailure)
			}
			if !strings.Contains(stderr, tc.mention) {
				t.Errorf("the refusal %q does not mention %q", stderr, tc.mention)
			}
			after, err := os.ReadFile(project)
			if err != nil {
				t.Fatalf("read the file of the project: %v", err)
			}
			if string(after) != string(before) {
				t.Errorf("the file of the project changed although the change was refused:\n%s", after)
			}
			if strings.Contains(string(after), "s3cret") {
				t.Errorf("a password is in the file of the project:\n%s", after)
			}
		})
	}
}

// TestNET010NoCommandOfTheRoutePrintsACredentialOfAProfile: the value lives in the store
// of the machine and is read for a connection that is really made. Everything a person and
// a program read — the list, the state of the credentials, the check of a route, the report
// of the machine — says whether there is one and not what it is (docs/DESIGN.md §7e).
func TestNET010NoCommandOfTheRoutePrintsACredentialOfAProfile(t *testing.T) {
	store := &storeOfTheRoute{values: map[string]string{
		secret.Service + "/" + secret.ProxyKey("work"): "ann:s3cret",
	}}
	useNetworkOfTheTest(t, store, []network.State{
		{Capability: network.CapabilityGitHub, Result: network.StateAvailable, Detail: "the API answered 200", Duration: "12ms"},
		{Capability: network.CapabilityGit, Result: network.StateAvailable, Detail: "git read the heads", Duration: "40ms"},
		{Capability: network.CapabilityModelProvider, Result: network.StateUnknown, Detail: "crewflow does not know where the model is served from"},
	})
	// `doctor network` asks the machine of the report: a server of the test stands in for
	// the host of the project, so that no check of a command reaches the network of the
	// machine the tests run on.
	answer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer answer.Close()
	(&networkHost{api: answer.URL, secrets: store}).use(t)
	project := writeConfig(t, plainProject+`
[network]
mode = "proxy"
active_proxy = "work"
no_proxy = [".internal.example"]

[network.proxies.work]
type = "socks5"
host = "proxy.example.com"
port = 1080
credentials = "secret-store"
`)
	password := filepath.Join(t.TempDir(), "proxy-password")
	if err := os.WriteFile(password, []byte("ann:s3cret\n"), 0o600); err != nil {
		t.Fatalf("write the file of the credentials: %v", err)
	}

	for _, args := range [][]string{
		{"network", "proxy", "list", "-config", project},
		{"network", "proxy", "list", "-json", "-config", project},
		{"network", "proxy", "credentials", "work", "status", "-config", project},
		{"network", "proxy", "test", "-config", project},
		{"network", "proxy", "test", "work", "-json", "-config", project},
	} {
		stdout, stderr, code := say(t, args...)
		for _, answer := range []string{stdout, stderr} {
			for _, value := range []string{"s3cret", "ann:s3cret"} {
				if strings.Contains(answer, value) {
					t.Errorf("`%s` wrote %q, want no credential of the profile in it", strings.Join(args, " "), answer)
				}
			}
		}
		if code != exitOK {
			t.Errorf("`%s` = %d, want %d (stderr: %q)", strings.Join(args, " "), code, exitOK, stderr)
		}
	}
	// And the list says that the credentials are configured without reading them: a report
	// that read them to say it would have had them in memory for nothing.
	if store.reads != 0 {
		t.Errorf("the commands read the store %d times, want a presence without reading the value", store.reads)
	}
	stdout, _, _ := say(t, "network", "proxy", "list", "-config", project)
	if !strings.Contains(stdout, "secret-store (configured)") {
		t.Errorf("the list says %q, want it to say that the credentials are configured", stdout)
	}

	// And the same for the report of the machine: it shows what each capability of the
	// route can do and says nothing of the value that route goes out with. The route of
	// this case is a proxy nobody answers, so the command fails — and the failure is a
	// failure of the route, not of the report.
	report, reportErr, _ := say(t, "doctor", "network", "-config", project)
	for _, want := range []string{"github-api", "git-https", "model-provider", "active_proxy"} {
		if !strings.Contains(report, want) {
			t.Errorf("the report of the route is %q, want %q in it", report, want)
		}
	}
	for _, answer := range []string{report, reportErr} {
		for _, value := range []string{"s3cret", "ann:s3cret"} {
			if strings.Contains(answer, value) {
				t.Errorf("`doctor network` wrote %q, want no credential of the profile in it", answer)
			}
		}
	}
}

// networkHost is the machine of a report about the network in a test of a command: the
// server of the test stands in for the host of the project, and the git of the machine is
// the git of the test — nothing of this reaches the network of the machine the tests run
// on (docs/DESIGN.md §7d).
type networkHost struct {
	api     string
	secrets secret.Store
}

func (n *networkHost) use(t *testing.T) {
	t.Helper()
	old := systemEnv
	t.Cleanup(func() { systemEnv = old })
	systemEnv = func(configPath, tempDir string, probe bool, out io.Writer) doctor.Env {
		return doctor.Env{
			ConfigPath: configPath, TempDir: tempDir, Probe: probe, Out: out,
			Secrets: n.secrets, Now: time.Now, NetworkAPI: n.api + "/rate_limit",
			Git: func(context.Context, []string, []string) (string, int, error) { return "", 0, nil },
		}
	}
}

// TestTheCredentialsOfAProfileArePutAwayAndTakenAwayAndNeitherIsItsRemoval: three
// operations that a person does apart from one another — the profile out of the file, the
// value out of the store — and the one that says the value is there never shows it
// (docs/DESIGN.md §7d, §7e).
func TestTheCredentialsOfAProfileArePutAwayAndTakenAwayAndNeitherIsItsRemoval(t *testing.T) {
	store := &storeOfTheRoute{}
	useNetworkOfTheTest(t, store, nil)
	project := writeConfig(t, plainProject+`
[network.proxies.work]
type = "socks5"
host = "proxy.example.com"
port = 1080
credentials = "secret-store"
`)
	password := filepath.Join(t.TempDir(), "proxy-password")
	if err := os.WriteFile(password, []byte("ann:s3cret"), 0o600); err != nil {
		t.Fatalf("write the file of the credentials: %v", err)
	}

	stdout, stderr, code := say(t, "network", "proxy", "credentials", "work", "set",
		"-file", password, "-config", project)
	if code != exitOK {
		t.Fatalf("put the credentials away = %d, want %d (stderr: %q)", code, exitOK, stderr)
	}
	if strings.Contains(stdout+stderr, "s3cret") {
		t.Errorf("the command said %q, want no password in it", stdout+stderr)
	}
	if kept, err := store.Get(secret.Service, secret.ProxyKey("work")); err != nil || string(kept) != "ann:s3cret" {
		t.Errorf("the credentials in the store are %q (%v), want the pair that was in the file", kept, err)
	}
	// A value that is not a pair is not credentials of a proxy, and an address with half
	// of one is an address that says the wrong thing to whoever reads it.
	lonely := filepath.Join(t.TempDir(), "proxy-password")
	if err := os.WriteFile(lonely, []byte("ann"), 0o600); err != nil {
		t.Fatalf("write the file of the credentials: %v", err)
	}
	if _, stderr, code = say(t, "network", "proxy", "credentials", "work", "set",
		"-file", lonely, "-config", project); code == exitOK {
		t.Errorf("putting away a value that is not a pair = %d, want %d", code, exitFailure)
	} else if !strings.Contains(stderr, "colon") {
		t.Errorf("the refusal %q does not say what the value has to look like", stderr)
	}

	// The profile goes out of the file and the value stays in the store: a profile added
	// back in an hour wants the same proxy to answer.
	said, _, code := say(t, "network", "proxy", "remove", "work", "-config", project)
	if code != exitOK {
		t.Fatalf("remove the profile = %d, want %d (stderr: %q)", code, exitOK, said)
	}
	if !strings.Contains(said, secret.ProxyKey("work")) {
		t.Errorf("the answer %q does not say that the credentials are still in the store", said)
	}
	if _, err := store.Get(secret.Service, secret.ProxyKey("work")); err != nil {
		t.Errorf("the credentials are out of the store after the profile was removed: %v", err)
	}

	// And now the other operation: the value goes away on its own.
	if _, stderr, code = say(t, "network", "proxy", "credentials", "work", "remove",
		"-config", project); code == exitOK {
		t.Errorf("removing the credentials of a profile that is gone = %d, want %d: "+
			"a person who takes a profile out has not asked to lose the value", code, exitFailure)
	} else if !strings.Contains(stderr, "in no [network.proxies]") {
		t.Errorf("the refusal %q does not name the profile that is not there", stderr)
	}
}

// TestTheCredentialsOfAProfileThatTakesNoneAreRefused: a proxy that takes no login has no
// credentials, and a file of a login in the store would be a file nobody knows the use of.
func TestTheCredentialsOfAProfileThatTakesNoneAreRefused(t *testing.T) {
	store := &storeOfTheRoute{}
	useNetworkOfTheTest(t, store, nil)
	project := writeConfig(t, plainProject+`
[network.proxies.home]
type = "http"
host = "192.168.0.4"
port = 1082
credentials = "none"
`)
	password := filepath.Join(t.TempDir(), "proxy-password")
	if err := os.WriteFile(password, []byte("ann:s3cret"), 0o600); err != nil {
		t.Fatalf("write the file of the credentials: %v", err)
	}

	_, stderr, code := say(t, "network", "proxy", "credentials", "home", "set",
		"-file", password, "-config", project)

	if code != exitFailure {
		t.Fatalf("putting credentials away for a profile that takes none = %d, want %d", code, exitFailure)
	}
	if !strings.Contains(stderr, "--credentials secret-store") {
		t.Errorf("the refusal %q does not say what to change first", stderr)
	}
	if store.writes != 0 {
		t.Errorf("the store of the machine was written %d times, want none", store.writes)
	}
}

// TestTheCapabilitiesOfARouteArePrintedApart: a report that said one word for all three
// would say nothing about the one that is broken, and the success of the host of the code
// says nothing about the provider of the model (docs/DESIGN.md §7d).
func TestTheCapabilitiesOfARouteArePrintedApart(t *testing.T) {
	useNetworkOfTheTest(t, &storeOfTheRoute{}, []network.State{
		{Capability: network.CapabilityGitHub, Result: network.StateAvailable, Detail: "the API answered 200", Duration: "12ms"},
		{Capability: network.CapabilityGit, Result: network.StateUnavailable, Detail: "could not resolve host: github.com"},
		{Capability: network.CapabilityModelProvider, Result: network.StateUnknown, Detail: "crewflow does not know where the model is served from"},
	})
	project := writeConfig(t, plainProject+`
[network]
mode = "proxy"
active_proxy = "home"

[network.proxies.home]
type = "http"
host = "192.168.0.4"
port = 1082
credentials = "none"
`)

	stdout, _, code := say(t, "network", "proxy", "test", "-config", project)

	if code != exitFailure {
		t.Errorf("`network proxy test` = %d, want %d: a capability that could not reach its target is a failure", code, exitFailure)
	}
	for _, want := range []string{
		"route: network.active_proxy = \"home\"",
		network.CapabilityGitHub, network.StateAvailable,
		network.CapabilityGit, network.StateUnavailable,
		network.CapabilityModelProvider, network.StateUnknown,
		"(12ms)",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the answer of the command is %q, want %q in it", stdout, want)
		}
	}
}

// TestTheModeOfTheRouteIsChangedByACommandAndTheOneCrewflowDoesNotDoIsRefused: the mode
// `fallback` is described and not written yet, and the refusal names the two that are
// (docs/DESIGN.md §7d, #145).
func TestTheModeOfTheRouteIsChangedByACommandAndTheOneCrewflowDoesNotDoIsRefused(t *testing.T) {
	useNetworkOfTheTest(t, &storeOfTheRoute{}, nil)
	project := writeConfig(t, plainProject+`
[network.proxies.home]
type = "http"
host = "192.168.0.4"
port = 1082
credentials = "none"
`)

	if _, _, code := say(t, "network", "proxy", "use", "home", "-config", project); code != exitOK {
		t.Fatalf("use the profile = %d, want %d", code, exitOK)
	}
	if _, stderr, code := say(t, "network", "mode", "proxy", "-config", project); code != exitOK {
		t.Fatalf("switch the mode = %d, want %d (stderr: %q)", code, exitOK, stderr)
	}
	if _, stderr, code := say(t, "network", "mode", "fallback", "-config", project); code != exitFailure {
		t.Fatalf("the mode fallback = %d, want %d", code, exitFailure)
	} else if !strings.Contains(stderr, "direct or proxy") {
		t.Errorf("the refusal %q does not name the modes crewflow does", stderr)
	}
	if _, stderr, code := say(t, "network", "mode", "direct", "-config", project); code != exitOK {
		t.Fatalf("switch the mode back = %d, want %d (stderr: %q)", code, exitOK, stderr)
	}
	stdout, _, _ := say(t, "network", "mode", "direct", "-config", project)
	if !strings.Contains(stdout, "the route of the project is direct") {
		t.Errorf("the answer of the command is %q, want it to say what the route is now", stdout)
	}
}
