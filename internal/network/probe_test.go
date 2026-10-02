package network

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/secret"
)

// clockOfTheTest is the clock of a machine of a test: it stands still until a check
// moves it, so that how long a check took and how fresh its answer is are facts of the
// test and not of the machine the tests run on.
type clockOfTheTest struct {
	at time.Time
}

func (c *clockOfTheTest) now() time.Time { return c.at }

func (c *clockOfTheTest) tick(d time.Duration) { c.at = c.at.Add(d) }

// machineOfTheTest is the machine a check of a route is made on: an API of a server of
// the test, a git that answers what the case says it answers, and no git at all unless the
// case names one. No test of crewflow asks the network of the machine it runs on.
type machineOfTheTest struct {
	clock       *clockOfTheTest
	answered    string
	gitListed   string
	gitSaid     string
	gitCode     int
	gitFails    error
	gitRan      [][]string
	gitWasGiven [][]string
	withoutGit  bool
	// gitAnswers are the answers of one run after another, so that a case can give the
	// straight route and the proxied route of the same check different answers: the results
	// of two routes are two results and are never mixed (§7d).
	gitAnswers []recordedGit
}

// recordedGit is one answer of the git of a machine of a test: the two streams as a real
// git writes them, and the code it exited with.
type recordedGit struct {
	listed string
	said   string
	code   int
}

// aHeadOfMain is what a real git writes for `ls-remote <url> refs/heads/main` on a
// repository it may read: forty signs of the commit, a tab, and the name of the ref.
const aHeadOfMain = "119aae76c4a2f1e4bd1e1e1c2b8f2c0e9a7d4b31\trefs/heads/main\n"

// api answers the check of the host with whatever the case wrote, and nothing else.
func (m *machineOfTheTest) api(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(m.code())
}

func (m *machineOfTheTest) code() int {
	if m.answered == "" {
		return http.StatusOK
	}
	if code, found := strings.CutPrefix(m.answered, "code "); found {
		var answer int
		for _, r := range code {
			if r < '0' || r > '9' {
				return http.StatusOK
			}
			answer = answer*10 + int(r-'0')
		}
		return answer
	}
	return http.StatusOK
}

// git is the git of the machine of the test: it records what it was asked and answers with
// what the case wrote, as two streams, the way a real git writes them.
func (m *machineOfTheTest) git(_ context.Context, args, environment []string) (string, string, int, error) {
	m.gitRan = append(m.gitRan, args)
	m.gitWasGiven = append(m.gitWasGiven, environment)
	if len(m.gitAnswers) > 0 {
		answer := m.gitAnswers[0]
		m.gitAnswers = m.gitAnswers[1:]
		return answer.listed, answer.said, answer.code, nil
	}
	if m.gitFails != nil {
		return "", "", -1, m.gitFails
	}
	return m.gitListed, m.gitSaid, m.gitCode, nil
}

// machine is the machine of the check with the server of the API of it and the clock
// standing still.
func machine(t *testing.T, m *machineOfTheTest) (Machine, func()) {
	t.Helper()
	m.clock = &clockOfTheTest{at: time.Date(2026, time.October, 2, 9, 0, 0, 0, time.UTC)}
	answer := httptest.NewServer(http.HandlerFunc(m.api))
	checked := Machine{
		Now: m.clock.now, Timeout: 2 * time.Second, ValidFor: time.Hour,
		APIURL: answer.URL + "/rate_limit",
		GitURL: "https://github.com/naghuale/crewflow.git",
		GitRef: "refs/heads/main",
	}
	if !m.withoutGit {
		checked.Git = m.git
	}
	return checked, answer.Close
}

// TestCFNET010TheCapabilitiesOfARouteAreCheckedOneByOne: a route that reaches the host of
// the code has not thereby been checked for the model of a run, and a report that says
// one word for all three says less than nothing about the one that is broken. The two
// checks of the host are made here — the API answers, git cannot resolve anything — and
// the provider of the model stays unknown because crewflow does not know where it is
// (docs/DESIGN.md §7d).
func TestCFNET010TheCapabilitiesOfARouteAreCheckedOneByOne(t *testing.T) {
	fake := &machineOfTheTest{gitSaid: "fatal: unable to access 'https://github.com/': Could not resolve host: github.com"}
	checked, closeServer := machine(t, fake)
	defer closeServer()
	_, cfg := withFile(t, keysOf("mode", `"direct"`))
	route, err := Choose(cfg, "api.github.com")
	if err != nil {
		t.Fatalf("the route of a request: %v", err)
	}

	states, err := Check(t.Context(), checked, route, cfg.Network.NoProxy)

	if err != nil {
		t.Fatalf("the check of the route: %v", err)
	}
	if len(states) != 3 {
		t.Fatalf("the check came out with %d capabilities, want %d: %v", len(states), len(Capabilities), states)
	}
	byName := map[string]State{}
	for _, state := range states {
		byName[state.Capability] = state
	}
	if got := byName[CapabilityGitHub].Result; got != StateAvailable {
		t.Errorf("the API of the host came out as %q (%s), want %q: the server of the test answered",
			got, byName[CapabilityGitHub].Detail, StateAvailable)
	}
	if got := byName[CapabilityGit].Result; got != StateUnavailable {
		t.Errorf("git came out as %q (%s), want %q: the case says it never got to the host",
			got, byName[CapabilityGit].Detail, StateUnavailable)
	}
	if got := byName[CapabilityModelProvider].Result; got != StateUnknown {
		t.Errorf("the provider of the model came out as %q, want %q: nobody checked it", got, StateUnknown)
	}
	if !byName[CapabilityModelProvider].CheckedAt.IsZero() {
		t.Error("the provider of the model carries the moment of a check, want none: it was not checked")
	}
	for _, state := range states {
		if state.Result != StateUnknown && state.Duration == "" {
			t.Errorf("the capability %q came out without the time it took", state.Capability)
		}
	}
}

// TestTheGitOfTheCheckGoesThroughTheRoute: the check of git is the git of the machine
// with the environment of the route added to the environment of the process — the same
// way a run hands the route to its own git — and nothing else (docs/DESIGN.md §7d).
func TestTheGitOfTheCheckGoesThroughTheRoute(t *testing.T) {
	fake := &machineOfTheTest{}
	checked, closeServer := machine(t, fake)
	defer closeServer()
	_, cfg := withFile(t, keysOf("mode", `"proxy"`, "active_proxy", `"home"`,
		"no_proxy", `[".internal.example"]`))
	route, err := Choose(cfg, "api.github.com")
	if err != nil {
		t.Fatalf("the route of a request: %v", err)
	}

	if _, err := Check(t.Context(), checked, route, cfg.Network.NoProxy); err != nil {
		t.Fatalf("the check of the route: %v", err)
	}

	if len(fake.gitWasGiven) != 1 {
		t.Fatalf("git was started %d times, want once", len(fake.gitWasGiven))
	}
	for _, want := range []string{
		"HTTP_PROXY=http://192.168.0.4:1082",
		"HTTPS_PROXY=http://192.168.0.4:1082",
		"NO_PROXY=.internal.example",
	} {
		if !contains(fake.gitWasGiven[0], want) {
			t.Errorf("git was started with %q, want %q in it", fake.gitWasGiven[0], want)
		}
	}
	if len(fake.gitRan[0]) < 3 || fake.gitRan[0][0] != "ls-remote" {
		t.Errorf("git was asked %q, want it to be asked for the heads of the repository", fake.gitRan[0])
	}
}

// TestACheckThatCouldNotBeMadeIsUnknownAndNotAFailure: nothing was learned, and a report
// that called a check it could not make a failure sends a person after a route that may
// be perfectly good (docs/DESIGN.md §7d).
func TestACheckThatCouldNotBeMadeIsUnknownAndNotAFailure(t *testing.T) {
	fake := &machineOfTheTest{gitSaid: "fatal: unable to access: Could not resolve host: github.com"}
	checked, closeServer := machine(t, fake)
	defer closeServer()
	checked.APIURL = ""
	_, cfg := withFile(t, keysOf("mode", `"direct"`))
	route, err := Choose(cfg, "api.github.com")
	if err != nil {
		t.Fatalf("the route of a request: %v", err)
	}

	states, err := Check(t.Context(), checked, route, cfg.Network.NoProxy)

	if err != nil {
		t.Fatalf("the check of the route: %v", err)
	}
	api, git := states[0], states[1]
	if api.Result != StateUnknown || git.Result != StateUnavailable {
		t.Errorf("the capabilities came out as %q and %q, want %q and %q: the API could not be asked and git could not reach the host",
			api.Result, git.Result, StateUnknown, StateUnavailable)
	}
}

// TestGITNET014TheAnswerOfTheHostIsReadByTheClosedTable: an answer of the API of the host
// says about the route what it says and nothing more. A 2xx is the route working; a 407 is
// the proxy wanting a login; 401, 403 and 404 are the host answering and letting nobody in,
// which says nothing about the network and is therefore `unknown` with the reason of its
// own; anything else proves nothing and is `check-unclassified` (docs/DESIGN.md §7d).
func TestGITNET014TheAnswerOfTheHostIsReadByTheClosedTable(t *testing.T) {
	for _, tc := range []struct {
		code   string
		want   string
		reason string
	}{
		{"code 200", StateAvailable, ReasonRequestSucceeded},
		{"code 204", StateAvailable, ReasonRequestSucceeded},
		{"code 301", StateUnknown, ReasonCheckUnclassified},
		{"code 401", StateUnknown, ReasonAuthenticationFailed},
		{"code 403", StateUnknown, ReasonPermissionDenied},
		{"code 404", StateUnknown, ReasonEndpointNotFound},
		{"code 407", StateUnavailable, ReasonProxyAuth},
		{"code 502", StateUnknown, ReasonCheckUnclassified},
	} {
		t.Run(tc.code, func(t *testing.T) {
			fake := &machineOfTheTest{answered: tc.code, gitListed: aHeadOfMain}
			checked, closeServer := machine(t, fake)
			defer closeServer()
			_, cfg := withFile(t, keysOf("mode", `"direct"`))
			route, err := Choose(cfg, "api.github.com")
			if err != nil {
				t.Fatalf("the route of a request: %v", err)
			}

			states, err := Check(t.Context(), checked, route, cfg.Network.NoProxy)

			if err != nil {
				t.Fatalf("the check of the route: %v", err)
			}
			api := states[0]
			if api.Result != tc.want {
				t.Errorf("the API came out as %q (%s), want %q", api.Result, api.Detail, tc.want)
			}
			if api.Reason != tc.reason {
				t.Errorf("the API came out with the reason %q (%s), want %q", api.Reason, api.Detail, tc.reason)
			}
		})
	}
}

// TestACheckOlderThanTheTermOfTheProjectIsStale: an answer that was a fact and is not one
// now is neither available nor unavailable, and a route that was checked yesterday has
// not been checked today (docs/DESIGN.md §7d).
func TestACheckOlderThanTheTermOfTheProjectIsStale(t *testing.T) {
	fake := &machineOfTheTest{}
	checked, closeServer := machine(t, fake)
	defer closeServer()
	checked.ValidFor = time.Minute
	checked.Git = func(context.Context, []string, []string) (string, string, int, error) {
		// The check takes longer than the project says a fact of it is worth.
		fake.clock.tick(2 * time.Minute)
		return aHeadOfMain, "", 0, nil
	}
	_, cfg := withFile(t, keysOf("mode", `"direct"`))
	route, err := Choose(cfg, "api.github.com")
	if err != nil {
		t.Fatalf("the route of a request: %v", err)
	}

	states, err := Check(t.Context(), checked, route, cfg.Network.NoProxy)

	if err != nil {
		t.Fatalf("the check of the route: %v", err)
	}
	if got := states[1].Result; got != StateStale {
		t.Errorf("the capability came out as %q (%s), want %q", got, states[1].Detail, StateStale)
	}
}

// TestTheTLSOfACheckIsNotWeakened: a proxy is a way out of a machine, and a route that
// crewflow cannot verify a certificate through is a way to read somebody else's traffic
// (docs/DESIGN.md §7e, §8).
func TestTheTLSOfACheckIsNotWeakened(t *testing.T) {
	route, err := ProxyOf(Route{Name: "home", Profile: profileOf("http", "192.168.0.4", 1082)},
		"http://192.168.0.4:1082")
	if err != nil {
		t.Fatalf("the proxy of the route: %v", err)
	}

	transport := transportOf(route)

	if transport.TLSClientConfig.InsecureSkipVerify {
		t.Error("the check of a route does not verify the certificate of the host, want it verified")
	}
	if transport.TLSClientConfig.MinVersion < tls.VersionTLS12 {
		t.Errorf("the check of a route starts at TLS %x, want at least 1.2", transport.TLSClientConfig.MinVersion)
	}
	if transport.Proxy == nil {
		t.Error("the check of a route has no proxy, want the profile of the route")
	}
}

// TestAStraightRouteHasNoProxyAtAll: a client with no proxy is a client that connects
// where it was told to, and a proxy left over from the shell of a person is a route
// nobody chose (docs/DESIGN.md §7d).
func TestAStraightRouteHasNoProxyAtAll(t *testing.T) {
	proxy, err := ProxyOf(Route{Direct: true}, "")

	if err != nil || proxy != nil {
		t.Errorf("the proxy of a straight route is %t and the error is %v, want none and no error", proxy != nil, err)
	}
}

// TestNET010WhatACheckSaysCarriesNoCredentialOfTheProfile: the value of the store is read
// for the connection and goes nowhere else — not into the state of a capability, not into
// its detail, and not into what `crewflow network proxy test` prints. The one place it
// does go is the address of the proxy in the environment of the child process, because a
// proxy that takes a login cannot be reached without it (docs/DESIGN.md §7e).
func TestNET010WhatACheckSaysCarriesNoCredentialOfTheProfile(t *testing.T) {
	store := &storeOfTheTest{values: map[string]string{
		secret.Service + "/" + secret.ProxyKey("work"): "ann:s3cret",
	}}
	fake := &machineOfTheTest{gitSaid: "fatal: unable to access 'https://proxy.example.com/': Connection refused"}
	checked, closeServer := machine(t, fake)
	defer closeServer()
	checked.Secrets = store
	_, cfg := withFile(t, keysOf("mode", `"proxy"`, "active_proxy", `"work"`))
	route, err := Choose(cfg, "api.github.com")
	if err != nil {
		t.Fatalf("the route of a request: %v", err)
	}

	states, err := Check(t.Context(), checked, route, cfg.Network.NoProxy)

	if err != nil {
		t.Fatalf("the check of the route: %v", err)
	}
	for _, state := range states {
		for _, value := range []string{"s3cret", "ann:s3cret"} {
			if strings.Contains(state.Detail, value) {
				t.Errorf("the capability %q says %q, want no credential of the profile in it", state.Capability, state.Detail)
			}
		}
	}
	if len(fake.gitWasGiven) != 1 {
		t.Fatalf("git was started %d times, want once", len(fake.gitWasGiven))
	}
	for _, name := range fake.gitWasGiven[0] {
		if !strings.Contains(name, "s3cret") {
			continue
		}
		mark, _, _ := strings.Cut(name, "=")
		switch mark {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY":
		default:
			t.Errorf("the credential of the profile is in %q of the child process, "+
				"want it in the address of the proxy and nowhere else", name)
		}
	}
}

// TestTheCheckOfTheHostGoesThroughTheProfileOfTheRoute: the API of the host is asked
// through the proxy of the route and not beside it, which is the whole difference between
// a check of a profile and a check of the machine (docs/DESIGN.md §7d).
func TestTheCheckOfTheHostGoesThroughTheProfileOfTheRoute(t *testing.T) {
	proxied := &machineOfTheTest{}
	proxy := httptest.NewServer(http.HandlerFunc(proxied.api))
	defer proxy.Close()
	host, port := hostAndPort(t, proxy.URL)
	fake := &machineOfTheTest{}
	checked, closeServer := machine(t, fake)
	defer closeServer()
	path, _ := withFile(t, keysOf("mode", `"proxy"`, "active_proxy", `"home"`))
	if err := Add(path, "here", config.Proxy{
		Type: "http", Host: host, Port: port, Credentials: "none",
	}); err != nil {
		t.Fatalf("add the profile of the proxy of the test: %v", err)
	}
	if err := Use(path, "here"); err != nil {
		t.Fatalf("use the profile of the proxy of the test: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("the file of the project does not load: %v", err)
	}
	route, err := Choose(cfg, "api.github.com")
	if err != nil {
		t.Fatalf("the route of a request: %v", err)
	}

	states, err := Check(t.Context(), checked, route, cfg.Network.NoProxy)

	if err != nil {
		t.Fatalf("the check of the route: %v", err)
	}
	if states[0].Result != StateAvailable {
		t.Errorf("the API of the host came out as %q (%s), want %q: the proxy of the test answered",
			states[0].Result, states[0].Detail, StateAvailable)
	}
}

// TestACheckOfAProfileThatTakesCredentialsItHasNotGotIsARefusal: a check that goes on
// with an empty login answers 407 and leaves a person to wonder why their proxy is down.
func TestACheckOfAProfileThatTakesCredentialsItHasNotGotIsARefusal(t *testing.T) {
	store := &storeOfTheTest{values: map[string]string{}}
	fake := &machineOfTheTest{}
	checked, closeServer := machine(t, fake)
	defer closeServer()
	checked.Secrets = store
	_, cfg := withFile(t, keysOf("mode", `"proxy"`, "active_proxy", `"work"`))
	route, err := Choose(cfg, "api.github.com")
	if err != nil {
		t.Fatalf("the route of a request: %v", err)
	}

	if _, err := Check(t.Context(), checked, route, cfg.Network.NoProxy); err == nil {
		t.Fatal("the check of a profile whose credentials are in no store returned no error, want a refusal")
	}
	if len(fake.gitRan) != 0 {
		t.Error("git was started for a profile with no credentials in the store, want the refusal before any connection")
	}
}

// TestAStoreThatCouldNotBeReadIsARefusalAndNotAnEmptyLogin: the two lead a person to
// different places — one to their own keychain, the other to the proxy.
func TestAStoreThatCouldNotBeReadIsARefusalAndNotAnEmptyLogin(t *testing.T) {
	broken := &brokenStore{}
	_, cfg := withFile(t, keysOf("mode", `"proxy"`, "active_proxy", `"work"`))
	route, err := Choose(cfg, "api.github.com")
	if err != nil {
		t.Fatalf("the route of a request: %v", err)
	}

	if _, err := Credentials(broken, route); err == nil {
		t.Fatal("a store that could not be read returned no error, want a refusal")
	}
	if _, err := Configured(broken, route); err == nil {
		t.Fatal("a report that could not ask the store returned no error, want a refusal")
	}
}

// brokenStore is a store of a machine whose keychain refused to be read.
type brokenStore struct{}

func (brokenStore) Get(string, string) ([]byte, error) {
	return nil, errors.New("the keychain of macOS refused to look for it (status -25293)")
}

func (brokenStore) Set(string, string, []byte) error { return nil }

// profileOf is a profile of a project as the checks of a route see it.
func profileOf(kind, host string, port int) config.Proxy {
	return config.Proxy{Type: kind, Host: host, Port: port, Credentials: "none"}
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

// hostAndPort are the host and the port of an address of the test, which is the only way
// to name a proxy of a route in a case of this file.
func hostAndPort(t *testing.T, address string) (string, int) {
	t.Helper()
	parsed, err := url.Parse(address)
	if err != nil {
		t.Fatalf("the address %q: %v", address, err)
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil {
		t.Fatalf("the port of %q: %v", address, err)
	}
	return parsed.Hostname(), port
}
