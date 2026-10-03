package network

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
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

// The closed list of reasons. A reason is a **proven** explanation of a state and not the
// most likely one: the ladder of a check is a structured sign first (a status code, an
// exit code), then a typed error, then the class of the failure — and where nothing is
// proven the state is `unknown` with `check-unclassified`. A check never names a reason
// more specific than the evidence in its hands (docs/DESIGN.md §7d, MODEL: UNKNOWN не
// успех).
const (
	// ReasonRequestSucceeded is the one reason of an `available`: the check was made and
	// the target answered.
	ReasonRequestSucceeded = "request-succeeded"

	// The five reasons of a proven failure of the network. They are the only ones a check
	// may call `unavailable` for, and `fallback` on them is a decision of #145.

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
	// ReasonProxyAuth is a proxy that wants a login and did not get one. Only the status
	// 407 and the words that name it prove it: Go writes `proxyconnect` on every failure
	// of dialling a proxy, so that word names nothing on its own (§7d, F-119).
	ReasonProxyAuth = "proxy-authentication-required"

	// The reasons of a host that answered and did not let the check in. Nothing is proven
	// about the network on them: it got there, and the account it went as did not — which
	// is why they are not a reason for `fallback` either.

	// ReasonAuthenticationFailed is an account the host did not take.
	ReasonAuthenticationFailed = "authentication-failed"
	// ReasonPermissionDenied is an account that is there and may not.
	ReasonPermissionDenied = "permission-denied"
	// ReasonEndpointNotFound is an address of the host that is not there.
	ReasonEndpointNotFound = "endpoint-not-found"
	// ReasonReferenceNotFound is a repository that has no such ref or branch.
	ReasonReferenceNotFound = "reference-not-found"

	// ReasonCheckUnclassified is everything else: nothing proven, so nothing named beyond
	// the fact that the check could not read its answer.
	ReasonCheckUnclassified = "check-unclassified"
	// ReasonUsageNotProven is the provider of the model of a run. Nothing was asked of it
	// and nothing could be: crewflow does not know where the model is served from, so a
	// successful run is what proves this route (§7d, §7j).
	ReasonUsageNotProven = "proxy-usage-not-proven"
)

// The five reasons of a proven failure of the network, and the words that name them.
//
// The order is the order in which they are read: the specific ones first, so that a
// sentence about a refused connection that also says `proxyconnect` is a refusal and not a
// proxy that wants a login, and a handshake that ran out of time is a certificate and not a
// slow one. Go writes that prefix on **every** failure of dialling a proxy — refused,
// unresolved, certificate that does not verify — so it names nothing on its own, and proxy
// authentication is only the status 407 and the words that name it
// (docs/DESIGN.md §7d, F-119, ревью #156).
//
// The TLS words are narrow for the same reason: a bare `tls` is in text that is not a
// failure of a certificate, and TLS is read before the timeout because a handshake that ran
// out of time says `tls handshake timeout` — a certificate question and not a slow one.
var reasonsOfTheNetwork = []struct {
	reason string
	words  []string
}{
	{ReasonConnectionRefused, []string{
		"connection refused", "failed to connect", "no route to host", "network is unreachable",
	}},
	{ReasonDNS, []string{
		"could not resolve host", "no such host", "name or service not known",
		"temporary failure in name resolution", "nodename nor servname",
	}},
	{ReasonTLS, []string{
		"x509", "certificate", "tls handshake", "ssl",
	}},
	{ReasonConnectionTimeout, []string{
		"timed out", "i/o timeout", "timeout", "deadline exceeded",
	}},
	{ReasonProxyAuth, []string{
		"proxy authentication required", "407",
	}},
}

// The reasons a host answers with, in the words git and an adapter write them. Neither of
// them is a failure of the network and neither of them is proof that the network works.
var refusalsOfTheHost = []struct {
	reason string
	words  []string
}{
	{ReasonAuthenticationFailed, []string{
		"authentication failed", "invalid username or password", "could not read username",
		"could not read password", "http basic: access denied", "401",
	}},
	{ReasonPermissionDenied, []string{
		"permission denied", "403",
	}},
}

// networkReason is the reason of the failure of the network in what a program wrote, and
// an empty string where none of the closed words is in it. An empty string proves nothing:
// the class of the failure is not known, so the check ends as `unknown` (§7d).
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

// refusalReason is the reason a host answered with, in the words it wrote: `unknown` and a
// reason of its own, because the network is not what failed.
func refusalReason(said string) string {
	said = strings.ToLower(said)
	for _, named := range refusalsOfTheHost {
		for _, word := range named.words {
			if strings.Contains(said, word) {
				return named.reason
			}
		}
	}
	return ""
}

// networkReasonOf is the reason of a failure of the network in a typed error, by the second
// rung of the ladder: what Go itself says it is, not what a sentence about it contains. An
// error that is none of these falls back to its words, and an error that is none of those
// either proves nothing.
func networkReasonOf(err error) string {
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return ReasonDNS
	}
	var certificate *tls.CertificateVerificationError
	if errors.As(err, &certificate) {
		return ReasonTLS
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return ReasonConnectionRefused
	}
	var timed net.Error
	if errors.As(err, &timed) && timed.Timeout() {
		return ReasonConnectionTimeout
	}
	return networkReason(err.Error())
}

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
	// Dial is how the check of the host opens a connection: the dialer of the machine
	// where it is nil, and the dialer of a machine of a test where a test handed one in.
	// The check of a route is a test of the route and not a test of the network of the
	// machine the tests run on, and that is true of every connection it makes: a check that
	// answered from a proxy of the machine of a person would be a check of that proxy
	// (docs/DESIGN.md §7d).
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
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
		// program that answered wrote: the values of the route go through the redactor of
		// the secrets of the machine (docs.DESIGN.md §7e).
		state.Detail = secret.Redact(state.Detail, route.Secrets(credentials)...)
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
			Reason: ReasonUsageNotProven,
			Detail: "crewflow does not know where the model of a run is served from: " +
				"a run that went through this route is what proves it",
		}
	default:
		return unclassified("no check of a capability named " + capability)
	}
}

// api asks the API of the host of the project through the route, by the closed table of
// §7d: a status code first, a typed failure of the network second, and nothing named beyond
// the evidence in either. The TLS is the TLS of the machine and it is not weakened for a
// proxy: a route crewflow cannot verify a certificate through is not a route, it is a way
// to read somebody else's traffic (docs/DESIGN.md §7e, §8).
func (m Machine) api(ctx context.Context, route Route, credentials string) State {
	if m.APIURL == "" {
		return unclassified("the file of the project names no host to ask")
	}
	address, err := route.Address(credentials)
	if err != nil {
		return unclassified(err.Error())
	}
	proxied, err := ProxyOf(route, address)
	if err != nil {
		return unclassified(err.Error())
	}
	transport := transportOf(proxied, m.Dial)
	client := &http.Client{Timeout: m.Timeout, Transport: transport}
	ctx, cancel := context.WithTimeout(ctx, m.Timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, m.APIURL, nil)
	if err != nil {
		return unclassified(err.Error())
	}
	response, err := client.Do(request)
	if err != nil {
		if stopped(ctx) {
			return interrupted(err)
		}
		if reason := networkReasonOf(err); reason != "" {
			return State{Result: StateUnavailable, Reason: reason, Detail: oneLine(err.Error())}
		}
		return unclassified(oneLine(err.Error()))
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<10))
	switch code := response.StatusCode; {
	case code < 300:
		return State{Result: StateAvailable, Reason: ReasonRequestSucceeded,
			Detail: fmt.Sprintf("%s answered %d through the route", m.APIURL, code)}
	case code == http.StatusProxyAuthRequired:
		// The status itself is the proof: Go writes `proxyconnect` on every failure of
		// dialling a proxy, so nothing but the status says that this one wanted a login.
		return State{Result: StateUnavailable, Reason: ReasonProxyAuth,
			Detail: fmt.Sprintf("the proxy answered 407 through %s: it wants a login and password", m.APIURL)}
	case code == http.StatusUnauthorized:
		return State{Result: StateUnknown, Reason: ReasonAuthenticationFailed,
			Detail: fmt.Sprintf("%s answered 401: the network is there, the account is not", m.APIURL)}
	case code == http.StatusForbidden:
		return State{Result: StateUnknown, Reason: ReasonPermissionDenied,
			Detail: fmt.Sprintf("%s answered 403: the network is there, the rights are not", m.APIURL)}
	case code == http.StatusNotFound:
		return State{Result: StateUnknown, Reason: ReasonEndpointNotFound,
			Detail: fmt.Sprintf("%s answered 404: the network is there, the endpoint is not", m.APIURL)}
	default:
		return unclassified(fmt.Sprintf("%s answered %d", m.APIURL, code))
	}
}

// git asks the git of the machine for one ref of the repository of the project through the
// route, by the closed table of §7d: what git wrote is the only evidence there is, and a
// sentence it did not prove anything of names nothing. A non-zero exit never becomes
// `available` on its own — that was the first version of this check, and it called every
// refusal of credentials a working route, including the answer of a git that never got
// anywhere (F-119, ревью #156).
func (m Machine) git(ctx context.Context, route Route, environment []string) State {
	if m.Git == nil || m.GitURL == "" {
		return unclassified("the file of the project names no repository to ask")
	}
	ref := m.GitRef
	if ref == "" {
		ref = "HEAD"
	}
	ctx, cancel := context.WithTimeout(ctx, m.Timeout)
	defer cancel()
	// The ref of the branch the project merges into: a ref every repository of it has, and
	// an answer of git that is either a list of commits or nothing a reader can use.
	answer, said, code, err := m.Git(ctx, []string{"ls-remote", m.GitURL, ref}, environment)
	switch {
	case stopped(ctx):
		return interrupted(err)
	case err != nil:
		if reason := networkReasonOf(err); reason != "" {
			return State{Result: StateUnavailable, Reason: reason, Detail: oneLine(err.Error())}
		}
		return unclassified(fmt.Sprintf("git could not be started: %s", oneLine(err.Error())))
	case networkReason(said) != "":
		return State{Result: StateUnavailable, Reason: networkReason(said),
			Detail: fmt.Sprintf("%s could not be reached through the route: %s", m.GitURL, oneLine(said))}
	case code == 0 && refsAre(answer):
		return State{Result: StateAvailable, Reason: ReasonRequestSucceeded,
			Detail: fmt.Sprintf("git read %s of %s through the route: %s", ref, m.GitURL, refsOf(answer))}
	case code == 0 && strings.TrimSpace(answer+said) == "":
		// git reached the host, listed nothing and complained of nothing: the repository is
		// there and the ref is not in it.
		return State{Result: StateUnknown, Reason: ReasonReferenceNotFound,
			Detail: fmt.Sprintf("%s has no %s in it", m.GitURL, ref)}
	case code == noRefsToList:
		// The code `ls-remote --exit-code` exits with when the host answered and the ref is
		// not in it. The check does not ask for that flag — an answer is read, not a code —
		// and a git that answers with it anyway says the same thing (docs/DESIGN.md §7d).
		return State{Result: StateUnknown, Reason: ReasonReferenceNotFound,
			Detail: fmt.Sprintf("%s has no %s in it: git exited with %d", m.GitURL, ref, code)}
	case refusalReason(said) != "":
		return State{Result: StateUnknown, Reason: refusalReason(said),
			Detail: fmt.Sprintf("%s answered and did not let git in: %s", m.GitURL, oneLine(said))}
	default:
		return unclassified(fmt.Sprintf("git exited with %d and said %q", code, oneLine(answer+said)))
	}
}

// noRefsToList is the code `git ls-remote --exit-code` exits with when the host answered
// and there is no ref of the pattern in it. It is git's own code, and it names a repository
// that has no such ref — not a route that does not work.
const noRefsToList = 2

// stopped is whether a check was stopped before it could answer: a person pressed Ctrl+C
// or the machine went away. A timeout is not that — it is the time the project gave the
// check, and it is the one of the five proven failures of the network.
func stopped(ctx context.Context) bool {
	return ctx.Err() != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded)
}

// unclassified is a check that could not read its answer: `unknown` and the reason that
// says so. Nothing is named beyond the fact that nothing was proven.
func unclassified(detail string) State {
	return State{Result: StateUnknown, Reason: ReasonCheckUnclassified, Detail: detail}
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

// transportOf is the transport of the HTTP of a check: the proxy of the route and the
// TLS of the machine. The TLS is not weakened for a proxy — the certificate of the host
// is verified as it is everywhere else in crewflow, because a route crewflow cannot
// verify a certificate through is a way to read somebody else's traffic rather than a
// route to their own network (docs/DESIGN.md §7e, §8).
func transportOf(proxy func(*http.Request) (*url.URL, error), dial func(context.Context, string, string) (net.Conn, error)) *http.Transport {
	return &http.Transport{
		Proxy:               proxy,
		DialContext:         dial,
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
