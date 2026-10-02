package network

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/secret"
)

// baseFile is the file of a project with the two profiles every case of the network of
// this project has: one at home over http without credentials, one at work over socks5
// with them. A case adds its keys of `[network]` where they belong — right below `mode` —
// because a second table of the same name is not a file (docs/DESIGN.md §7d).
const baseFile = `
[project]
repo = "naghuale/crewflow"

[executor]
command = ["agent", "run", "{prompt}"]

[network]
%s
[network.proxies.home]
type = "http"
host = "192.0.2.10"
port = 1082
credentials = "none"

[network.proxies.work]
type = "socks5"
host = "proxy.example.com"
port = 1080
credentials = "secret-store"
`

// withFile is the file of a project with the keys of `[network]` a case names, in a
// folder of the test that is gone when the case is over, read by the same loader every
// command reads — a test that built a Config by hand would check a shape the file of a
// project does not have.
func withFile(t *testing.T, keys string) (string, config.Config) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "crewflow.toml")
	if err := os.WriteFile(path, []byte(fmt.Sprintf(baseFile, keys)), 0o600); err != nil {
		t.Fatalf("write the file of the project: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("the file of the project does not load: %v", err)
	}
	return path, cfg
}

// keysOf turns the pairs a case names into the lines of the table of `[network]`.
func keysOf(pairs ...string) string {
	var lines strings.Builder
	for i := 0; i+1 < len(pairs); i += 2 {
		fmt.Fprintf(&lines, "%s = %s\n", pairs[i], pairs[i+1])
	}
	return lines.String()
}

// TestCFNET001AStraightRouteHandsNothingToAChildProcess is the first rule of the order of
// the route (02.10.2026): the mode `direct` sends a request out as it is, and a variable
// left over from the shell of a person is a route nobody chose. Nothing is added to the
// environment of a child process in this mode — not an empty one, not a `NO_PROXY` and
// not a proxy that the shell of the machine happened to have (docs/DESIGN.md §7d).
func TestCFNET001AStraightRouteHandsNothingToAChildProcess(t *testing.T) {
	_, cfg := withFile(t, keysOf("mode", `"direct"`))

	environment, err := EnvironmentOf(cfg, "api.github.com", "")

	if err != nil {
		t.Fatalf("the route of a request: %v", err)
	}
	if len(environment) != 0 {
		t.Errorf("a child process was given %q in the mode %q, want nothing: "+
			"a variable of the shell of a person is a route nobody chose", environment, cfg.Network.Mode)
	}
}

// TestCFNET002AProxyReachesTheEnvironmentOfAChildProcess is the second: in the mode
// `proxy` the route is in the environment of the child process and nowhere else. Four
// names and no more — the global environment of the machine and the settings of git,
// global and system, are not touched, and a program of a person must not find a proxy
// there that the project never asked for (docs/DESIGN.md §7d).
func TestCFNET002AProxyReachesTheEnvironmentOfAChildProcess(t *testing.T) {
	_, cfg := withFile(t, keysOf("mode", `"proxy"`, "active_proxy", `"home"`))

	environment, err := EnvironmentOf(cfg, "api.github.com", "")

	if err != nil {
		t.Fatalf("the route of a request: %v", err)
	}
	want := []string{
		"HTTP_PROXY=http://192.0.2.10:1082",
		"HTTPS_PROXY=http://192.0.2.10:1082",
		"ALL_PROXY=http://192.0.2.10:1082",
	}
	for _, name := range want {
		if !slices.Contains(environment, name) {
			t.Errorf("the child process was started with %q, want %q in it", environment, name)
		}
	}
	for _, name := range environment {
		mark, _, _ := strings.Cut(name, "=")
		switch mark {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY":
		default:
			t.Errorf("the child process was given %q, want the four names of a route and nothing else", name)
		}
	}
}

// TestTheTypeOfAProfileIsTheSchemeOfTheAddress is what a program of the machine does
// with the type: a proxy over socks5 is not an http proxy, and a route that hands out an
// http address for it is a route to a program that will not understand it.
func TestTheTypeOfAProfileIsTheSchemeOfTheAddress(t *testing.T) {
	_, cfg := withFile(t, keysOf("mode", `"proxy"`, "active_proxy", `"work"`))

	environment, err := EnvironmentOf(cfg, "api.github.com", "ann:secret")

	if err != nil {
		t.Fatalf("the route of a request: %v", err)
	}
	for _, name := range environment {
		if strings.HasPrefix(name, "HTTP_PROXY=") && !strings.Contains(name, "socks5://ann:secret@") {
			t.Errorf("the route was handed over as %q, want the scheme the type of the profile says", name)
		}
	}
}

// TestCFNET011NoProxyIsCheckedBeforeTheRouteIsPicked is the order the owner decided on:
// a target of `no_proxy` goes straight out whatever the mode says, and the exceptions
// work in the mode `proxy` as well — the same way the standard NO_PROXY works, because
// that is what a person who has one on another machine writes without thinking
// (docs/DESIGN.md §7d).
func TestCFNET011NoProxyIsCheckedBeforeTheRouteIsPicked(t *testing.T) {
	cases := []struct {
		name   string
		rules  []string
		target string
		want   bool
	}{
		{"an exact name", []string{"api.github.com"}, "api.github.com", true},
		{"a name that is not in the rules", []string{"api.github.com"}, "github.com", false},
		{"a domain suffix", []string{".github.com"}, "api.github.com", true},
		{"a domain suffix without the dot", []string{"github.com"}, "api.github.com", true},
		{"a suffix that is only a part of a name", []string{"hub.com"}, "api.github.com", false},
		{"a name that ends in the suffix as a part of it", []string{"github.com"}, "notgithub.com", false},
		{"a star domain", []string{"*.github.com"}, "api.github.com", true},
		{"an address", []string{"192.0.2.9"}, "192.0.2.9", true},
		{"another address", []string{"192.0.2.9"}, "192.0.2.10", false},
		{"a network", []string{"192.0.2.0/24"}, "192.0.2.10", true},
		{"a network it is not in", []string{"10.0.0.0/8"}, "192.0.2.10", false},
		{"everything", []string{"*"}, "api.github.com", true},
		{"a port that is the one asked for", []string{"github.com:443"}, "github.com:443", true},
		{"a port that is not the one asked for", []string{"github.com:443"}, "github.com", false},
		{"the host of a whole address", []string{"api.github.com"}, "https://api.github.com/rate_limit", true},
		{"the port of a whole address", []string{"github.com:443"}, "https://github.com:443/naghuale/crewflow", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rules, err := tomlRules(tc.rules)
			if err != nil {
				t.Fatalf("the rules of the case as they are written in a file: %v", err)
			}
			_, cfg := withFile(t, keysOf("mode", `"proxy"`, "active_proxy", `"home"`,
				"no_proxy", rules))

			route, err := Choose(cfg, tc.target)

			if err != nil {
				t.Fatalf("the route of %q with the rules %v: %v", tc.target, tc.rules, err)
			}
			if route.Direct != tc.want {
				t.Errorf("the route of %q with the rules %v is direct = %t, want %t (%s)",
					tc.target, tc.rules, route.Direct, tc.want, route.Why)
			}
		})
	}
}

// tomlRules writes a list of rules the way the file of a project writes it, so that a
// case of the exceptions is a case of the file and not of a value built in a test.
func tomlRules(rules []string) (string, error) {
	quoted := make([]string, 0, len(rules))
	for _, rule := range rules {
		quoted = append(quoted, strconv.Quote(rule))
	}
	return "[" + strings.Join(quoted, ", ") + "]", nil
}

// TestAStraightTargetGoesOutBeforeTheModeIsEvenLookedAt: the exceptions are checked
// first, and a rule that names the host of the project takes it out of a proxy in the
// mode `proxy`.
func TestAStraightTargetGoesOutBeforeTheModeIsEvenLookedAt(t *testing.T) {
	_, cfg := withFile(t, keysOf("mode", `"proxy"`, "active_proxy", `"home"`,
		"no_proxy", `["api.github.com"]`))

	route, err := Choose(cfg, "api.github.com")

	if err != nil {
		t.Fatalf("the route of a request: %v", err)
	}
	if !route.Direct {
		t.Fatalf("the route of a target of [network] no_proxy is %q, want it straight out", route.Why)
	}
	environment, err := route.Environment(cfg.Network.NoProxy, "")
	if err != nil {
		t.Fatalf("the environment of the route: %v", err)
	}
	if len(environment) != 0 {
		t.Errorf("a child process was given %q for a target of no_proxy, want nothing", environment)
	}
}

// TestTheExceptionsGoToTheChildProcessWithTheProxy: they are in the environment of a
// child process as the standard NO_PROXY is written, so that a program of the machine —
// gh, git, an agent — sends them out straight without crewflow having to rewrite one call.
func TestTheExceptionsGoToTheChildProcessWithTheProxy(t *testing.T) {
	_, cfg := withFile(t, keysOf("mode", `"proxy"`, "active_proxy", `"home"`,
		"no_proxy", `[".internal.example", "192.168.0.0/16"]`))

	environment, err := EnvironmentOf(cfg, "api.github.com", "")

	if err != nil {
		t.Fatalf("the route of a request: %v", err)
	}
	if !slices.Contains(environment, "NO_PROXY=.internal.example,192.168.0.0/16") {
		t.Errorf("the child process was started with %q, want the exceptions of the file in NO_PROXY", environment)
	}
}

// TestARuleCrewflowCannotReadOneWayIsRefused: a rule with a scheme, a path, credentials
// or a star in the middle of a name means different things to different programs. Crewflow
// refuses it instead of reading it as it likes: a route picked by a guess is a route
// nobody chose (docs/DESIGN.md §7d).
func TestARuleCrewflowCannotReadOneWayIsRefused(t *testing.T) {
	cases := []struct{ rule, mention string }{
		{"", "empty"},
		{"https://github.com", "scheme"},
		{"github.com/naghuale", "path"},
		{"ann@github.com", "credentials"},
		{"git*.github.com", "star"},
		{"github.*.com", "star"},
		{"github.com:https", "port"},
		{"github.com: 443", "port"},
	}
	for _, tc := range cases {
		t.Run(tc.rule, func(t *testing.T) {
			_, err := NoProxy([]string{tc.rule}, "api.github.com")

			if !errors.Is(err, ErrPattern) {
				t.Errorf("NoProxy(%q) = %v, want the refusal of a rule crewflow cannot read", tc.rule, err)
			}
			if !strings.Contains(err.Error(), tc.mention) {
				t.Errorf("the refusal %q does not say %q", err, tc.mention)
			}
		})
	}
}

// TestARuleIsReadOneWayOrNotAtAll: every rule the standard NO_PROXY knows is read the way
// the standard reads it, and a name is compared without regard to its case.
func TestARuleIsReadOneWayOrNotAtAll(t *testing.T) {
	cases := []struct {
		rule, target string
		want         bool
	}{
		{"GitHub.com", "api.github.com", true},
		{".GITHUB.COM", "api.GitHub.com", true},
		{"github.com", "github.com.", true},
		{"192.0.2.10", "192.0.2.10", true},
		{"10.0.0.0/8", "git.internal", false},
	}
	for _, tc := range cases {
		t.Run(tc.rule+" "+tc.target, func(t *testing.T) {
			got, err := NoProxy([]string{tc.rule}, tc.target)
			if err != nil {
				t.Fatalf("NoProxy(%q, %q): %v", tc.rule, tc.target, err)
			}
			if got != tc.want {
				t.Errorf("NoProxy(%q, %q) = %t, want %t", tc.rule, tc.target, got, tc.want)
			}
		})
	}
}

// TestTheRouteNamesTheProfileAndWhyItWasChosen: a report that says "through home"
// without saying why is a report a person has to open the file to understand.
func TestTheRouteNamesTheProfileAndWhyItWasChosen(t *testing.T) {
	_, cfg := withFile(t, keysOf("mode", `"proxy"`, "active_proxy", `"home"`))

	route, err := Choose(cfg, "api.github.com")

	if err != nil {
		t.Fatalf("the route of a request: %v", err)
	}
	if route.Name != "home" || route.Direct {
		t.Errorf("the route came out as %q through %q, want the active profile", route.Why, route.Name)
	}
	if !strings.Contains(route.Why, "home") {
		t.Errorf("the route says %q, want the profile of the file that was used", route.Why)
	}
}

// TestAProfileOfTheProjectMayBePreparedWithoutBeingUsed: `test home` asks about a
// profile before anybody made it the active one, and a profile that is prepared in
// advance is prepared for that (docs/DESIGN.md §7d).
func TestAProfileOfTheProjectMayBePreparedWithoutBeingUsed(t *testing.T) {
	_, cfg := withFile(t, keysOf("mode", `"direct"`))

	route, err := Through(cfg, "work")

	if err != nil {
		t.Fatalf("the route through the profile work: %v", err)
	}
	if route.Name != "work" || route.Direct {
		t.Errorf("the route through work = %+v, want the profile of the file", route)
	}
	if _, err := Through(cfg, "office"); err == nil {
		t.Error("a route through a profile that is in no file returned no error, want a refusal naming the profiles")
	}
}

// TestTheModeOfTheProxyWithoutAProfileIsARefusal: a route that names nothing is not a
// route, and the refusal names the command that makes it one.
func TestTheModeOfTheProxyWithoutAProfileIsARefusal(t *testing.T) {
	_, cfg := withFile(t, keysOf("mode", `"proxy"`))

	_, err := Choose(cfg, "api.github.com")

	if err == nil {
		t.Fatal("the mode proxy without an active profile returned no error, want a refusal")
	}
	if !strings.Contains(err.Error(), "crewflow network proxy use") {
		t.Errorf("the refusal %q does not say what to do", err)
	}
}

// TestTheTermsOfTheRouteAreDurations: a check of a route waits as long as the file says
// and stops there, and a report is a fact for as long as the file says it is.
func TestTheTermsOfTheRouteAreDurations(t *testing.T) {
	path, cfg := withFile(t, keysOf("connect_timeout", `"3s"`, "test_valid_for", `"2h"`))

	connect, validFor, err := Terms(cfg)

	if err != nil {
		t.Fatalf("the terms of the route: %v", err)
	}
	if connect != 3*time.Second || validFor != 2*time.Hour {
		t.Errorf("the terms came out as %s and %s, want 3s and 2h of the file %s", connect, validFor, path)
	}
}

// TestTheDefaultsOfTheRouteAreTheOnesOfTheFile: a project that says nothing goes
// straight out, waits ten seconds for an answer and treats it as a fact for an hour
// (docs/DESIGN.md §7d).
func TestTheDefaultsOfTheRouteAreTheOnesOfTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crewflow.toml")
	if err := os.WriteFile(path, []byte(minimalFile), 0o600); err != nil {
		t.Fatalf("write the file of the project: %v", err)
	}

	cfg, err := config.Load(path)

	if err != nil {
		t.Fatalf("a project that says nothing about the network does not load: %v", err)
	}
	connect, validFor, err := Terms(cfg)
	if err != nil {
		t.Fatalf("the terms of the route: %v", err)
	}
	if cfg.Network.Mode != "direct" || connect != 10*time.Second || validFor != time.Hour {
		t.Errorf("the defaults came out as mode %q, %s and %s, want direct, 10s and 1h",
			cfg.Network.Mode, connect, validFor)
	}
}

// TestTheCredentialsOfAProfileAreReadOnlyForAConnectionThatIsMade: a report that says
// whether they are there reads nothing, and the value itself never reaches anything but
// the address of a proxy (docs/DESIGN.md §7e).
func TestTheCredentialsOfAProfileAreReadOnlyForAConnectionThatIsMade(t *testing.T) {
	store := &storeOfTheTest{values: map[string]string{
		secret.Service + "/" + secret.ProxyKey("work"): "ann:s3cret",
	}}
	_, cfg := withFile(t, keysOf("mode", `"proxy"`, "active_proxy", `"work"`))
	route, err := Choose(cfg, "api.github.com")
	if err != nil {
		t.Fatalf("the route of a request: %v", err)
	}

	there, err := Configured(store, route)
	if err != nil {
		t.Fatalf("whether the credentials are in the store: %v", err)
	}
	if !there {
		t.Error("the store has the credentials of the profile and the report says it has not")
	}
	if store.read != 0 {
		t.Errorf("the report read the store %d times, want the answer of a presence without reading the value", store.read)
	}
	value, err := Credentials(store, route)
	if err != nil {
		t.Fatalf("the credentials for a connection: %v", err)
	}
	if value != "ann:s3cret" {
		t.Errorf("the credentials came out as %q, want the value of the store", value)
	}
}

// TestAProfileWithoutCredentialsIsAskedNothing: a proxy that takes no login is not sent
// an empty pair, and a store that has something under that name is not read for a profile
// that says it has none.
func TestAProfileWithoutCredentialsIsAskedNothing(t *testing.T) {
	store := &storeOfTheTest{values: map[string]string{
		secret.Service + "/" + secret.ProxyKey("home"): "ann:s3cret",
	}}
	_, cfg := withFile(t, keysOf("mode", `"proxy"`, "active_proxy", `"home"`))
	route, err := Choose(cfg, "api.github.com")
	if err != nil {
		t.Fatalf("the route of a request: %v", err)
	}

	value, err := Credentials(store, route)

	if err != nil || value != "" {
		t.Errorf("the credentials of a profile that takes none = %q, %v, want none and no error", value, err)
	}
	if store.read != 0 {
		t.Errorf("the store was read %d times for a profile that takes no credentials, want none", store.read)
	}
}

// TestAProfileThatTakesCredentialsAndHasNoneIsARefusal: an empty pair in the address is an
// address that says the wrong thing, and a person would be sent to the proxy instead of to
// their own store (docs/DESIGN.md §7e).
func TestAProfileThatTakesCredentialsAndHasNoneIsARefusal(t *testing.T) {
	store := &storeOfTheTest{values: map[string]string{}}
	_, cfg := withFile(t, keysOf("mode", `"proxy"`, "active_proxy", `"work"`))
	route, err := Choose(cfg, "api.github.com")
	if err != nil {
		t.Fatalf("the route of a request: %v", err)
	}

	_, err = Credentials(store, route)

	if err == nil {
		t.Fatal("the credentials of a profile that takes some came out of an empty store, want a refusal")
	}
	if !strings.Contains(err.Error(), "crewflow network proxy credentials work set") {
		t.Errorf("the refusal %q does not say what to do", err)
	}
}

// storeOfTheTest is the store of secrets of a machine of a test: it keeps values by the
// name of the item and counts how many times it was read, so that a report that says a
// secret is there without reading it can be shown to have done so.
type storeOfTheTest struct {
	values map[string]string
	read   int
	put    int
}

func (s *storeOfTheTest) Get(service, account string) ([]byte, error) {
	s.read++
	value, known := s.values[service+"/"+account]
	if !known {
		return nil, secret.ErrNotFound
	}
	return []byte(value), nil
}

func (s *storeOfTheTest) Set(service, account string, value []byte) error {
	s.put++
	if s.values == nil {
		s.values = map[string]string{}
	}
	s.values[service+"/"+account] = string(value)
	return nil
}

func (s *storeOfTheTest) Has(service, account string) (bool, error) {
	_, known := s.values[service+"/"+account]
	return known, nil
}

// minimalFile is a project that says nothing about the network: the case of the defaults
// of the route.
const minimalFile = `
[project]
repo = "naghuale/crewflow"

[executor]
command = ["agent", "run", "{prompt}"]
`
