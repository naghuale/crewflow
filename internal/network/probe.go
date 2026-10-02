package network

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/secret"
)

// The capabilities a route of a project is checked for. They are checked one by one and
// never together: a route that reaches the host of the code has not thereby been
// checked for the provider of the model, and a report that says one word for all three
// says less than nothing about the one that is broken (docs/DESIGN.md §7d).
const (
	// CapabilityGitHub is the API of the host of the project.
	CapabilityGitHub = "github-api"
	// CapabilityGit is git over https, which is how the code of a task is fetched and
	// pushed, and how a change of it is read.
	CapabilityGit = "git-https"
	// CapabilityModelProvider is the model of a run. Where it is lives in the command
	// line of the executor and in the account of the person, and crewflow knows no
	// address of it: the capability stays unknown until a run has really gone through
	// the route and said so.
	CapabilityModelProvider = "model-provider"
)

// Capabilities are the three names, in the order a report reads them.
var Capabilities = []string{CapabilityGitHub, CapabilityGit, CapabilityModelProvider}

// The four states a capability of a route may be in. A profile that is in the file of a
// project says nothing about either of the first two: it is a declaration, and what
// answers to it is a fact about a machine on a day (docs/DESIGN.md §7d).
const (
	// StateAvailable is a check that answered and could reach its target.
	StateAvailable = "available"
	// StateUnavailable is a check that answered and could not reach its target.
	StateUnavailable = "unavailable"
	// StateUnknown is a capability nobody has checked yet, or one crewflow cannot
	// check: an unchecked capability is not a working one.
	StateUnknown = "unknown"
	// StateStale is a check older than `[network] test_valid_for`: it was a fact and
	// is not one now.
	StateStale = "stale"
)

// State is what one check of one capability ended as, with the moment it was made and
// how long it took. It is what a report and `-json` print, and it carries no credential
// of the profile: the route is named, the value of it is not read for a check that only
// says whether the route works (docs/DESIGN.md §7e).
type State struct {
	// Capability is what was checked.
	Capability string `json:"capability"`
	// Result is one of the four states above.
	Result string `json:"result"`
	// Detail is one line about what came of it, without anything secret in it.
	Detail string `json:"detail,omitempty"`
	// CheckedAt is when the check was made, and it is not set for a capability nobody
	// checked: a report that puts a moment on an unchecked capability says it was
	// checked then.
	CheckedAt time.Time `json:"checked_at,omitzero"`
	// Duration is how long the check took, as a person reads a time.
	Duration string `json:"duration,omitempty"`
}

// A machine of a project as a check of a route sees it: what asks each capability, what
// answers, and where the credentials of the profile are. Every field is a plain value
// or a plain function, which is what makes a check of a route a test of its own and not
// a test of the network of the machine the tests run on (docs/DESIGN.md §7d).
type Machine struct {
	// Now is the clock of the check and Timeout bounds one capability.
	Now     func() time.Time
	Timeout time.Duration
	// ValidFor is `[network] test_valid_for`: how long a check is a fact about the
	// machine.
	ValidFor time.Duration
	// Secrets is where the credentials of the profile are. A nil store is a profile
	// with no credentials read, and a check says so instead of failing on a store that
	// is not there.
	Secrets secret.Store
	// APIURL and GitURL are what the two checks of the host ask for.
	APIURL string
	GitURL string
	// Git is the git of the machine, started with the environment of the route added
	// to the environment of the process.
	Git func(ctx context.Context, args []string, environment []string) (stderr string, code int, err error)
}

// Check asks every capability of the route, one by one, and says what came of it. A
// capability that could not be asked at all is `unknown` and not `unavailable`: nothing
// was learned, and a report that called a check it could not make a failure sends a
// person after a route that may be perfectly good.
//
// The credentials of the profile are read here and only here: this is the one place a
// route is really connected through, and every other command that shows the route knows
// only whether they are configured (docs/DESIGN.md §7d, §7e).
func Check(ctx context.Context, m Machine, route Route, noProxy []string) ([]State, error) {
	credentials, err := Credentials(m.Secrets, route)
	if err != nil {
		return nil, err
	}
	environment, err := route.Environment(noProxy, credentials)
	if err != nil {
		return nil, err
	}
	states := make([]State, 0, len(Capabilities))
	for _, capability := range Capabilities {
		states = append(states, m.ask(ctx, capability, route, environment, credentials))
	}
	return states, nil
}

// Credentials is the login and the password of the profile, and only when it says that
// it takes some and the store has them. A profile with `credentials = "none"` is asked
// nothing, and a store that could not be read is a refusal rather than an empty pair: a
// proxy that needs a login and gets an empty one answers 407, and a person would be
// sent to the proxy instead of to their own keychain.
func Credentials(store secret.Store, route Route) (string, error) {
	if route.Direct || route.Profile.Credentials != "secret-store" {
		return "", nil
	}
	if store == nil {
		return "", fmt.Errorf("the profile %q says credentials = %q and this machine has no store of secrets",
			route.Name, route.Profile.Credentials)
	}
	value, err := store.Get(secret.Service, secret.ProxyKey(route.Name))
	switch {
	case errors.Is(err, secret.ErrNotFound):
		return "", fmt.Errorf("the profile %q takes credentials and there are none in the store of this machine, "+
			"as %q: `crewflow network proxy credentials %s set <file>`", route.Name, secret.ProxyKey(route.Name), route.Name)
	case err != nil:
		return "", fmt.Errorf("the credentials of the profile %q: %w", route.Name, err)
	}
	return string(value), nil
}

// Configured reports whether the credentials of the profile are in the store, without
// reading them: a report that says they are there says all a person needs, and a report
// that read them to say it would have them in memory for nothing (docs/DESIGN.md §7e).
func Configured(store secret.Store, route Route) (bool, error) {
	if route.Direct || route.Profile.Credentials != "secret-store" {
		return false, nil
	}
	if presence, ok := store.(secret.Presence); ok {
		return presence.Has(secret.Service, secret.ProxyKey(route.Name))
	}
	_, err := store.Get(secret.Service, secret.ProxyKey(route.Name))
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, secret.ErrNotFound):
		return false, nil
	default:
		return false, err
	}
}

// ask is one capability of one route, with the clock around it.
func (m Machine) ask(ctx context.Context, capability string, route Route, environment []string, credentials string) State {
	started := m.Now()
	state := m.check(ctx, capability, route, environment, credentials)
	state.Capability = capability
	state.CheckedAt = started
	if state.Result == StateUnknown {
		// Nothing was asked, so there is nothing that could be stale either: the state
		// of an unchecked capability is that nobody knows.
		state.CheckedAt = time.Time{}
		return state
	}
	state.Duration = m.Now().Sub(started).Round(time.Millisecond).String()
	if credentials != "" {
		// Nothing of a credential of the profile is in what a person reads, whatever the
		// program that answered wrote: the value goes through the redactor of the
		// secrets of the machine, whole and by its password (docs/DESIGN.md §7e).
		state.Detail = secret.Redact(state.Detail, credentials)
		if _, password, found := strings.Cut(credentials, ":"); found {
			state.Detail = secret.Redact(state.Detail, password)
		}
	}
	if m.Now().Sub(started) > m.ValidFor {
		state.Result = StateStale
		state.Detail = fmt.Sprintf("%s, and the check is older than the %s of [network] test_valid_for",
			state.Detail, m.ValidFor)
	}
	return state
}

// check is the one capability, with the route known: the two checks of the host are made
// through the route itself, and the third one cannot be made by a program of crewflow at
// all.
func (m Machine) check(ctx context.Context, capability string, route Route, environment []string, credentials string) State {
	switch capability {
	case CapabilityGitHub:
		return m.api(ctx, route, credentials)
	case CapabilityGit:
		return m.git(ctx, route, environment)
	case CapabilityModelProvider:
		return State{
			Result: StateUnknown,
			Detail: "crewflow does not know where the model of a run is served from: " +
				"a run that went through this route is what proves it",
		}
	default:
		return State{Result: StateUnknown, Detail: "no check of a capability named " + capability}
	}
}

// api asks the API of the host of the project through the route. The TLS of it is the
// TLS of the machine and it is not weakened for a proxy: a route that crewflow cannot
// verify the certificate through is not a route, it is a way to read somebody else's
// traffic (docs/DESIGN.md §7e, §8).
func (m Machine) api(ctx context.Context, route Route, credentials string) State {
	if m.APIURL == "" {
		return State{Result: StateUnknown, Detail: "the file of the project names no host to ask"}
	}
	address, err := route.Address(credentials)
	if err != nil {
		return State{Result: StateUnavailable, Detail: err.Error()}
	}
	proxied, err := ProxyOf(route, address)
	if err != nil {
		return State{Result: StateUnavailable, Detail: err.Error()}
	}
	transport := transportOf(proxied)
	client := &http.Client{Timeout: m.Timeout, Transport: transport}
	ctx, cancel := context.WithTimeout(ctx, m.Timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, m.APIURL, nil)
	if err != nil {
		return State{Result: StateUnavailable, Detail: err.Error()}
	}
	response, err := client.Do(request)
	if err != nil {
		return State{Result: StateUnavailable, Detail: shortError(err)}
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<10))
	switch {
	case response.StatusCode < 400:
		return State{Result: StateAvailable, Detail: fmt.Sprintf("%s answered %d through the route", m.APIURL, response.StatusCode)}
	case response.StatusCode < 500:
		// The host answered and did not let the request in: the route works, and the
		// credentials are a question of its own.
		return State{Result: StateAvailable, Detail: fmt.Sprintf("the route works, and %s answered %d: %s",
			m.APIURL, response.StatusCode, http.StatusText(response.StatusCode))}
	default:
		return State{Result: StateUnavailable, Detail: fmt.Sprintf("%s answered %d through the route", m.APIURL, response.StatusCode)}
	}
}

// git asks the git of the machine for the heads of the repository of the project
// through the route. What it answers with is not the question: the question is whether
// git got to the host at all, and a host that answered "not without credentials" has
// been reached through the route all the same.
func (m Machine) git(ctx context.Context, route Route, environment []string) State {
	if m.Git == nil || m.GitURL == "" {
		return State{Result: StateUnknown, Detail: "the file of the project names no repository to ask"}
	}
	ctx, cancel := context.WithTimeout(ctx, m.Timeout)
	defer cancel()
	stderr, code, err := m.Git(ctx, []string{"ls-remote", "--exit-code", "--heads", m.GitURL, "HEAD"}, environment)
	switch {
	case err != nil:
		return State{Result: StateUnavailable, Detail: shortError(err)}
	case unreachable(stderr):
		return State{Result: StateUnavailable, Detail: shortError(errors.New(stderr))}
	case code == 0:
		return State{Result: StateAvailable, Detail: fmt.Sprintf("git read the heads of %s through the route", m.GitURL)}
	default:
		return State{Result: StateAvailable, Detail: fmt.Sprintf("the route works, and git did not get into %s: %s",
			m.GitURL, shortError(errors.New(stderr)))}
	}
}

// unreachableWords is what git writes when it never got to the host. It is a list of
// words and not a guarantee: a route that is not one of these may be mistaken for a
// route that works, which is why a check that cannot tell says what it read and lets the
// person look.
var unreachableWords = []string{
	"could not resolve host",
	"Failed to connect",
	"Connection refused",
	"Connection timed out",
	"i/o timeout",
	"no such host",
	"Empty reply from server",
	"407 ",
	"unable to access",
}

// unreachable is whether what git wrote says it never got to the host.
func unreachable(stderr string) bool {
	for _, phrase := range unreachableWords {
		if strings.Contains(strings.ToLower(stderr), strings.ToLower(phrase)) {
			return true
		}
	}
	return false
}

// transportOf is the transport of the HTTP of a check: the proxy of the route and the
// TLS of the machine. The TLS is not weakened for a proxy — the certificate of the host
// is verified as it is everywhere else in crewflow, because a route crewflow cannot
// verify a certificate through is a way to read somebody else's traffic rather than a
// route to their own network (docs/DESIGN.md §7e, §8).
func transportOf(proxy func(*http.Request) (*url.URL, error)) *http.Transport {
	return &http.Transport{
		Proxy:               proxy,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: 10 * time.Second,
	}
}

// ProxyOf is the proxy of a route as the HTTP of the machine is given it, and nothing
// for a straight route: a client with no proxy is a client that connects where it was
// told to.
func ProxyOf(route Route, address string) (func(*http.Request) (*url.URL, error), error) {
	if route.Direct || address == "" {
		return nil, nil
	}
	proxied, err := url.Parse(address)
	if err != nil {
		return nil, fmt.Errorf("the address of the profile %q: %w", route.Name, err)
	}
	return http.ProxyURL(proxied), nil
}

// System is the machine this process runs on: the HTTP with the TLS of the machine, the
// git of PATH, and the clock. It is what the commands and the doctor ask, and a test
// hands them a machine of its own instead (docs/DESIGN.md §7d).
func System(route Route, noProxy []string, secrets secret.Store, now func() time.Time, timeout, validFor time.Duration) Machine {
	return Machine{
		Now: now, Timeout: timeout, ValidFor: validFor, Secrets: secrets,
		Git: gitOfTheMachine(noProxy),
	}
}

// gitOfTheMachine is the git of PATH started with the environment of the route added to
// the environment of the process, which is the same way a run hands the route to its own
// git (docs/DESIGN.md §7d).
func gitOfTheMachine(noProxy []string) func(ctx context.Context, args, environment []string) (string, int, error) {
	return func(ctx context.Context, args, environment []string) (string, int, error) {
		command := exec.CommandContext(ctx, "git", args...)
		command.Env = append(os.Environ(), environment...)
		what, err := command.CombinedOutput()
		var exit *exec.ExitError
		switch {
		case err == nil:
		case errors.As(err, &exit):
		default:
			return string(what), -1, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return string(what), command.ProcessState.ExitCode(), nil
	}
}

// shortError is one line of a failure, for a report and not for a journal: the whole of
// what came of a check and nothing that could carry a credential of the profile.
func shortError(err error) string {
	if err == nil {
		return ""
	}
	first, _, _ := strings.Cut(err.Error(), "\n")
	return first
}

// Factory is what builds the machine of a check out of the file of a project: the host
// of the project and the repository of it are what the two checks of the host ask for.
func Factory(cfg config.Config) func(Machine) Machine {
	return func(m Machine) Machine {
		host := cfg.Forge.Host
		if host == "" {
			host = "github.com"
		}
		m.APIURL = "https://api." + host + "/rate_limit"
		m.GitURL = "https://" + host + "/" + cfg.Project.Repo + ".git"
		return m
	}
}
