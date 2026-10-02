package network

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
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

// The states a capability of a route may be in. A profile that is in the file of a
// project says nothing about either of the first two: it is a declaration, and what
// answers to it is a fact about a machine on a day (docs/DESIGN.md §7d).
const (
	// StateAvailable is a check that reached its target and was answered.
	StateAvailable = "available"
	// StateUnavailable is a check that could not reach its target, and the reason says
	// which of the closed words below it was.
	StateUnavailable = "unavailable"
	// StateUnknown is a capability nobody could read an answer of: unchecked, cut short
	// for a reason of its own, or answered in a way crewflow does not read. An unknown is
	// not a success and not a reason to change the route — it is a check that learned
	// nothing, and MODEL holds that UNKNOWN is not success (§6a, §7d).
	StateUnknown = "unknown"
	// StateInterrupted is a check that was stopped before it could answer: a person
	// pressed Ctrl+C, or the machine went away. It replaces nothing — whatever the
	// route answered before is still what it answered (§6a).
	StateInterrupted = "interrupted"
	// StateStale is a check older than `[network] test_valid_for`: it was a fact and
	// is not one now.
	StateStale = "stale"
)

// The closed reasons of a capability that could not reach its target. They are closed on
// purpose: a check that cannot tell which of them it is says `unknown` and its own
// reason, and never one of these — a route named by a guess is a route nobody chose
// (docs/DESIGN.md §7d).
const (
	// ReasonDNS is a host whose name did not resolve.
	ReasonDNS = "dns-failed"
	// ReasonConnectionRefused is a host that answered that it is not listening.
	ReasonConnectionRefused = "connection-refused"
	// ReasonConnectionTimeout is a host that did not answer in time.
	ReasonConnectionTimeout = "connection-timeout"
	// ReasonTLS is a certificate that could not be verified. crewflow does not weaken
	// the TLS of a check for a proxy, so this reason is sent to the owner of the machine
	// and not to the proxy (§7e, §8).
	ReasonTLS = "tls-failed"
	// ReasonProxyAuth is a proxy that wants a login and did not get one.
	ReasonProxyAuth = "proxy-authentication-required"
)

// The reasons of a check that learned nothing, one per what was asked.
const (
	// ReasonGitUnclassified is everything git said that is not one of the reasons above:
	// credentials git was refused, a repository or a ref that is not there, a code crewflow
	// does not know, and an answer that is not the answer git writes.
	ReasonGitUnclassified = "git-check-unclassified"
	// ReasonAPIUnclassified is the same for the API of the host: an answer crewflow does
	// not read as reachability.
	ReasonAPIUnclassified = "api-check-unclassified"
)

// State is what one check of one capability ended as, with the moment it was made and
// how long it took. It is what a report and `-json` print, and it carries no credential
// of the profile: the route is named, the value of it is not read for a check that only
// says whether the route works (docs/DESIGN.md §7e).
type State struct {
	// Capability is what was checked.
	Capability string `json:"capability"`
	// Result is one of the states above.
	Result string `json:"result"`
	// Reason is the closed reason of an `unavailable` and the reason of an `unknown`, so
	// that a report and a program read the same word for the same thing.
	Reason string `json:"reason,omitempty"`
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
	// APIURL, GitURL and GitRef are what the two checks of the host ask for: the address
	// of the API, the repository, and the ref in it that every project has.
	APIURL string
	GitURL string
	GitRef string
	// Git is the git of the machine, started with the environment of the route added
	// to the environment of the process. Its answer and its complaint are two streams and
	// not one: what git lists is one of them, and a check that could not tell which is
	// which would read the complaint as the answer.
	Git func(ctx context.Context, args []string, environment []string) (stdout, stderr string, exitCode int, err error)
}

// Check asks every capability of the route, one by one, and says what came of it. A
// capability that could not be asked at all is `unknown` and not `unavailable`: nothing
// was learned, and a report that called a check it could not make a failure sends a
// person after a route that may be perfectly good.
//
// What was answered before a check was stopped stays what it was: a capability answered
// `interrupted` says nothing about the ones before it, and what those answered is not
// thrown away (docs/DESIGN.md §6a, §7d).
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
		if ctx.Err() != nil {
			// The check was stopped and nothing more will be learned: every capability
			// left is `interrupted`, and what was answered before it is left as it was.
			states = append(states, State{
				Capability: capability,
				Result:     StateInterrupted,
				Detail:     "the check was stopped before it could answer",
			})
			continue
		}
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
	if state.Result == StateUnknown || state.Result == StateInterrupted {
		// Nothing was read, so there is nothing that could be stale either: the state of
		// an unchecked capability is that nobody knows, and one that was stopped has no
		// moment of its own.
		return state
	}
	state.CheckedAt = started
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
			Reason: ReasonAPIUnclassified,
			Detail: "crewflow does not know where the model of a run is served from: " +
				"a run that went through this route is what proves it",
		}
	default:
		return State{Result: StateUnknown, Reason: ReasonAPIUnclassified,
			Detail: "no check of a capability named " + capability}
	}
}

// api asks the API of the host of the project through the route. The TLS of it is the
// TLS of the machine and it is not weakened for a proxy: a route that crewflow cannot
// verify the certificate through is not a route, it is a way to read somebody else's
// traffic (docs/DESIGN.md §7e, §8).
//
// The answer of the host is read by the closed rule of §7d: 2xx and 3xx are the route
// working; a 407 is the proxy wanting a login, which is a route that does not work as it
// is; 401, 403 and 404 say that the host answered and did not let this request in, which
// proves nothing about the route and is therefore `unknown`; 5xx is the host failing.
func (m Machine) api(ctx context.Context, route Route, credentials string) State {
	if m.APIURL == "" {
		return State{Result: StateUnknown, Reason: ReasonAPIUnclassified,
			Detail: "the file of the project names no host to ask"}
	}
	address, err := route.Address(credentials)
	if err != nil {
		return State{Result: StateUnknown, Reason: ReasonAPIUnclassified, Detail: err.Error()}
	}
	proxied, err := ProxyOf(route, address)
	if err != nil {
		return State{Result: StateUnknown, Reason: ReasonAPIUnclassified, Detail: err.Error()}
	}
	transport := transportOf(proxied)
	client := &http.Client{Timeout: m.Timeout, Transport: transport}
	ctx, cancel := context.WithTimeout(ctx, m.Timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, m.APIURL, nil)
	if err != nil {
		return State{Result: StateUnknown, Reason: ReasonAPIUnclassified, Detail: err.Error()}
	}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return interrupted(err)
		}
		if reason := networkReason(err.Error()); reason != "" {
			return State{Result: StateUnavailable, Reason: reason, Detail: oneLine(err.Error())}
		}
		return State{Result: StateUnknown, Reason: ReasonAPIUnclassified, Detail: oneLine(err.Error())}
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<10))
	switch code := response.StatusCode; {
	case code < 400:
		return State{Result: StateAvailable,
			Detail: fmt.Sprintf("%s answered %d through the route", m.APIURL, code)}
	case code == http.StatusProxyAuthRequired:
		return State{Result: StateUnavailable, Reason: ReasonProxyAuth,
			Detail: fmt.Sprintf("the proxy wants a login and password: %s answered 407", m.APIURL)}
	case code == http.StatusUnauthorized, code == http.StatusForbidden, code == http.StatusNotFound:
		// The host answered and did not let this request in: that says nothing about the
		// route, and a report that called it a working one would be counting what it did
		// not learn.
		return State{Result: StateUnknown, Reason: ReasonAPIUnclassified,
			Detail: fmt.Sprintf("%s answered %d (%s): the host answered and let nobody in",
				m.APIURL, code, http.StatusText(code))}
	default:
		return State{Result: StateUnavailable, Reason: ReasonAPIUnclassified,
			Detail: fmt.Sprintf("%s answered %d through the route", m.APIURL, code)}
	}
}

// git asks the git of the machine for one ref of the repository of the project through
// the route, and reads what came of it by the closed rule: an exit of zero and an answer
// that is what git writes for `ls-remote` is the route working; a failure of the network
// that crewflow can name is the route not working, with that name; everything else is
// `unknown` — including credentials git was refused and a repository or ref that is not
// there, which say nothing about the route (docs/DESIGN.md §7d, MODEL: UNKNOWN не успех).
//
// A non-zero exit never becomes `available` on its own. That was the first version of this
// check and it called every refusal of credentials a working route, including the answer
// of a git that never got anywhere (F-119, ревью #156).
func (m Machine) git(ctx context.Context, route Route, environment []string) State {
	if m.Git == nil || m.GitURL == "" {
		return State{Result: StateUnknown, Reason: ReasonGitUnclassified,
			Detail: "the file of the project names no repository to ask"}
	}
	ref := m.GitRef
	if ref == "" {
		ref = "HEAD"
	}
	ctx, cancel := context.WithTimeout(ctx, m.Timeout)
	defer cancel()
	// The ref of the branch the project merges into: a ref every repository of it has,
	// and an answer of git that is either a list of commits or nothing a reader can use.
	answer, said, code, err := m.Git(ctx, []string{"ls-remote", m.GitURL, ref}, environment)
	switch {
	case ctx.Err() != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded):
		return interrupted(err)
	case err != nil:
		if reason := networkReason(err.Error()); reason != "" {
			return State{Result: StateUnavailable, Reason: reason, Detail: oneLine(err.Error())}
		}
		return unclassifiedGit(fmt.Sprintf("git could not be started: %s", oneLine(err.Error())))
	case networkReason(said) != "":
		return State{Result: StateUnavailable, Reason: networkReason(said),
			Detail: fmt.Sprintf("%s could not be reached through the route: %s", m.GitURL, oneLine(said))}
	case code != 0:
		return unclassifiedGit(fmt.Sprintf("git exited with %d and said %q", code, oneLine(said)))
	case refsAre(answer):
		return State{Result: StateAvailable,
			Detail: fmt.Sprintf("git read %s of %s through the route: %s", ref, m.GitURL, refsOf(answer))}
	default:
		// An exit of zero and nothing crewflow can read: no such ref, no such repository,
		// or a git of another vintage that lists something else. None of it is a route
		// that works.
		return unclassifiedGit(fmt.Sprintf("git listed nothing crewflow can read of %s, and said %q",
			m.GitURL, oneLine(answer+said)))
	}
}

// unclassifiedGit is everything the git of the machine said that is not one of the closed
// reasons: `unknown` with its own reason, the code and what was said, so that a person can
// read the failure and a program can tell it from a route that does not work.
//
// In it are the cases the closed list deliberately does not name, and a check that used to
// read some of them as a working route (F-119, ревью #156): a refusal of the credentials
// git went as ("Authentication failed", "Permission denied"), a repository or a ref that is
// not there, the code of `ls-remote --exit-code` for a ref that is not in the repository, a
// code crewflow has no word for, a sentence crewflow has no word for, nothing said at all,
// and an exit of zero with an answer that is not a list of refs. None of them is a failure
// of the network and none of them is proof that it works: the host answered and let nobody
// in, which says nothing about the route — and a refusal of credentials is not a reason to
// change it either (§7d, MODEL: UNKNOWN не успех).
func unclassifiedGit(detail string) State {
	return State{Result: StateUnknown, Reason: ReasonGitUnclassified, Detail: detail}
}

// interrupted is a check that was stopped before it could answer. It says nothing about
// the route and replaces nothing that came before it.
func interrupted(err error) State {
	detail := "the check was stopped before it could answer"
	if err != nil {
		detail += ": " + oneLine(err.Error())
	}
	return State{Result: StateInterrupted, Detail: detail}
}

// refLine is what git writes for one ref: forty signs of a commit, a tab, and the name of
// the ref. An answer crewflow does not read as this is not an answer, and a check that
// reads it as one would call any git that printed something a route that works.
var refLine = regexp.MustCompile(`^[0-9a-f]{40}\t\S+$`)

// refsAre whether what git wrote for `ls-remote` is a list of refs and nothing else.
func refsAre(answer string) bool {
	lines := strings.Split(strings.TrimRight(answer, "\n"), "\n")
	for _, line := range lines {
		if !refLine.MatchString(line) {
			return false
		}
	}
	return len(lines) > 0 && lines[0] != ""
}

// refsOf is the list of refs of an answer, for the line of a report: a check says what it
// read, not only that it read something.
func refsOf(answer string) string {
	lines := strings.Split(strings.TrimRight(answer, "\n"), "\n")
	names := make([]string, 0, len(lines))
	for _, line := range lines {
		if name, _, found := strings.Cut(line, "\t"); found {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	if len(names) > 3 {
		return fmt.Sprintf("%s and %d more", strings.Join(names[:3], ", "), len(names)-3)
	}
	return strings.Join(names, ", ")
}

// oneLine is one line of what a program wrote, for a report: a sentence of a failure is
// what a person reads, and nothing of a credential is in it (docs/DESIGN.md §7e).
func oneLine(said string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(said), "\n")
	return first
}

// The closed reasons of the network, and the words that name them. The order is the order
// in which they are read: a proxy that wants a login says so in a sentence that also
// mentions connecting, and a certificate that could not be verified is said with a
// timeout in the same sentence.
var reasonsOfTheNetwork = []struct {
	reason string
	words  []string
}{
	{ReasonProxyAuth, []string{
		"407", "proxy authentication required", "proxyconnect", "proxy connect",
	}},
	{ReasonTLS, []string{
		"x509", "certificate", "tls", "ssl",
	}},
	{ReasonDNS, []string{
		"could not resolve host", "no such host", "name or service not known",
		"temporary failure in name resolution", "nodename nor servname",
	}},
	{ReasonConnectionRefused, []string{
		"connection refused", "failed to connect", "no route to host", "network is unreachable",
	}},
	{ReasonConnectionTimeout, []string{
		"timed out", "i/o timeout", "timeout", "deadline exceeded",
	}},
}

// networkReason is the reason of the failure of the network in what a program wrote, and
// an empty string where none of the closed words is in it. An empty string is not a
// failure of the route: it is a check that cannot tell, and it ends as `unknown`.
func networkReason(said string) string {
	said = strings.ToLower(said)
	for _, named := range reasonsOfTheNetwork {
		for _, word := range named.words {
			if strings.Contains(said, word) {
				return named.reason
			}
		}
	}
	return ""
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
//
// The two streams stay apart: the list of refs is what the check reads as the answer, and
// the complaint is what it reads as the reason. A program that joins them answers a
// question nobody asked.
func gitOfTheMachine(noProxy []string) func(ctx context.Context, args, environment []string) (string, string, int, error) {
	return func(ctx context.Context, args, environment []string) (string, string, int, error) {
		command := exec.CommandContext(ctx, "git", args...)
		command.Env = append(os.Environ(), environment...)
		var listed, complained bytes.Buffer
		command.Stdout, command.Stderr = &listed, &complained
		err := command.Run()
		var exit *exec.ExitError
		switch {
		case err == nil:
		case errors.As(err, &exit):
		default:
			return listed.String(), complained.String(), -1,
				fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return listed.String(), complained.String(), command.ProcessState.ExitCode(), nil
	}
}

// Factory is what builds the machine of a check out of the file of a project: the host
// of the project and the repository of it are what the two checks of the host ask for,
// and the ref is the branch the project merges into — a ref every repository of it has, so
// that git has something real to answer with.
func Factory(cfg config.Config) func(Machine) Machine {
	return func(m Machine) Machine {
		host := cfg.Forge.Host
		if host == "" {
			host = "github.com"
		}
		m.APIURL = "https://api." + host + "/rate_limit"
		m.GitURL = "https://" + host + "/" + cfg.Project.Repo + ".git"
		if cfg.Project.DefaultBranch != "" {
			m.GitRef = "refs/heads/" + cfg.Project.DefaultBranch
		}
		return m
	}
}
