package network

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
)

// What a real git writes, one run per branch of the closed rule of §7d. Every case of
// the classifier below is one of these answers verbatim, so that a test of the classifier
// is a test of what it has to read and not of what it would like to read
// (docs/DESIGN.md §7d, ревью #156).
var (
	// GIT-NET-001: the repository answered and git listed a ref of it.
	gitListedMain = recordedGit{listed: aHeadOfMain, code: 0}
	// GIT-NET-002: the name of the host did not resolve.
	gitNoSuchHost = recordedGit{
		said: "fatal: unable to access 'https://github.com/naghuale/crewflow.git/': " +
			"Could not resolve host: github.com",
		code: 128,
	}
	// GIT-NET-003: the host answered that it is not listening.
	gitConnectionRefused = recordedGit{
		said: "fatal: unable to access 'https://github.com/naghuale/crewflow.git/': " +
			"Failed to connect to github.com port 443: Connection refused",
		code: 128,
	}
	// GIT-NET-004: the host did not answer in time.
	gitConnectionTimeout = recordedGit{
		said: "fatal: unable to access 'https://github.com/naghuale/crewflow.git/': " +
			"Operation timed out after 10001 milliseconds with 0 bytes received",
		code: 128,
	}
	// GIT-NET-005: the certificate could not be verified. crewflow does not weaken the
	// TLS of a check for a proxy, so this one is the owner's to read (§7e, §8).
	gitTLSFailed = recordedGit{
		said: "fatal: unable to access 'https://github.com/naghuale/crewflow.git/': " +
			"SSL certificate problem: unable to get local issuer certificate",
		code: 128,
	}
	// GIT-NET-006: the proxy wants a login and password.
	gitProxyWantsLogin = recordedGit{
		said: "fatal: unable to access 'https://github.com/naghuale/crewflow.git/': " +
			"Received HTTP code 407 from proxy after CONNECT",
		code: 128,
	}
	// GIT-NET-007: the host answered and refused the account git went as.
	gitAuthenticationFailed = recordedGit{
		said: "remote: Invalid username or password.\n" +
			"fatal: Authentication failed for 'https://github.com/naghuale/crewflow.git/'",
		code: 128,
	}
	// GIT-NET-007 again, in the words of another host.
	gitPermissionDenied = recordedGit{
		said: "remote: Permission to naghuale/crewflow.git denied to naghuale.\n" +
			"fatal: unable to access 'https://github.com/naghuale/crewflow.git/': " +
			"The requested URL returned error: 403",
		code: 128,
	}
	// GIT-NET-008: the host answered and there is no such repository.
	gitRepositoryNotFound = recordedGit{
		said: "remote: Repository not found.\n" +
			"fatal: repository 'https://github.com/naghuale/crewflow.git/' not found",
		code: 128,
	}
	// GIT-NET-009: git said something crewflow has no word for.
	gitSaidSomethingElse = recordedGit{
		said: "fatal: the remote end hung up unexpectedly",
		code: 128,
	}
	// GIT-NET-009 again: a code nobody has a word for, with nothing said at all.
	gitSilentWithACode = recordedGit{code: 42}
	// GIT-NET-011: git exited zero and listed something that is not a list of refs.
	gitListedSomethingElse = recordedGit{listed: "warning: redirecting to https://github.com/\n", code: 0}
	// GIT-NET-008 again: git exited zero and listed nothing at all.
	gitListedNothing = recordedGit{listed: "", code: 0}
)

// TestTheGitOfACheckIsReadByTheClosedRule walks every branch of it with the answer a real
// git writes for that branch. The rule has no "not a known network failure, therefore the
// route works": a non-zero exit never becomes `available` on its own, and everything the
// closed list does not name is `unknown` with `git-check-unclassified` (docs/DESIGN.md §7d,
// MODEL: UNKNOWN не успех).
func TestTheGitOfACheckIsReadByTheClosedRule(t *testing.T) {
	cases := []struct {
		id     string
		name   string
		answer recordedGit
		want   string
		reason string
	}{
		{"GIT-NET-001", "a ref of the repository", gitListedMain, StateAvailable, ReasonRequestSucceeded},
		{"GIT-NET-002", "a host that does not resolve", gitNoSuchHost, StateUnavailable, ReasonDNS},
		{"GIT-NET-003", "a host that refuses the connection", gitConnectionRefused, StateUnavailable, ReasonConnectionRefused},
		{"GIT-NET-004", "a host that does not answer", gitConnectionTimeout, StateUnavailable, ReasonConnectionTimeout},
		{"GIT-NET-005", "a certificate that does not verify", gitTLSFailed, StateUnavailable, ReasonTLS},
		{"GIT-NET-006", "a proxy that wants a login", gitProxyWantsLogin, StateUnavailable, ReasonProxyAuth},
		{"GIT-NET-007", "an account the host refused", gitAuthenticationFailed, StateUnknown, ReasonAuthenticationFailed},
		{"GIT-NET-007", "rights the account has not", gitPermissionDenied, StateUnknown, ReasonPermissionDenied},
		{"GIT-NET-008", "a repository that is not there", gitRepositoryNotFound, StateUnknown, ReasonCheckUnclassified},
		{"GIT-NET-008", "a ref that is not there", gitListedNothing, StateUnknown, ReasonReferenceNotFound},
		{"GIT-NET-008", "the code of --exit-code for a ref that is not there",
			recordedGit{code: 2}, StateUnknown, ReasonReferenceNotFound},
		{"GIT-NET-009", "a failure crewflow has no word for", gitSaidSomethingElse, StateUnknown, ReasonCheckUnclassified},
		{"GIT-NET-009", "a code crewflow has no word for, and nothing said", gitSilentWithACode, StateUnknown, ReasonCheckUnclassified},
		{"GIT-NET-011", "an exit of zero and something that is not a ref", gitListedSomethingElse, StateUnknown, ReasonCheckUnclassified},
	}
	for _, tc := range cases {
		t.Run(tc.id+" "+tc.name, func(t *testing.T) {
			fake := &machineOfTheTest{gitAnswers: []recordedGit{tc.answer}}
			checked, closeServer := machine(t, fake)
			defer closeServer()
			state := gitOfTheTest(t, checked)

			if state.Result != tc.want {
				t.Errorf("git came out as %q (%s), want %q", state.Result, state.Detail, tc.want)
			}
			if state.Reason != tc.reason {
				t.Errorf("git came out with the reason %q (%s), want %q", state.Reason, state.Detail, tc.reason)
			}
			if tc.want != StateAvailable && strings.Contains(state.Detail, "the route works") {
				t.Errorf("the detail %q calls the route working, want a check that learned nothing", state.Detail)
			}
			if tc.reason == ReasonCheckUnclassified && !strings.Contains(state.Detail, "git exited with") &&
				!strings.Contains(state.Detail, "nothing crewflow can read") &&
				!strings.Contains(state.Detail, "could not be started") {
				t.Errorf("the detail %q of an unclassified failure wants the code and what was said in it", state.Detail)
			}
		})
	}
}

// TestTheReasonOfAFailureIsReadFromTheWordsThatNameIt: Go writes the prefix `proxyconnect`
// on every failure of dialling a proxy, so a reason that took it for the reason of a proxy
// that wants a login would call a dead proxy something it is not. The live run of c4a8fcf
// said exactly that: a route through `127.0.0.1:9` came out as
// `proxy-authentication-required` for `proxyconnect tcp: dial tcp 127.0.0.1:9: connect:
// connection refused` (F-119, ревью #156). Proxy authentication is the status 407 and the
// words that name it, TLS is said by `x509` / `certificate` / `tls handshake` / `ssl`, and
// a sentence with no word of any of them is not a failure of the network at all.
func TestTheReasonOfAFailureIsReadFromTheWordsThatNameIt(t *testing.T) {
	cases := []struct {
		name   string
		said   string
		want   string
		reason string
	}{
		{
			"the error Go writes for a proxy nobody listens on",
			`Get "https://api.github.com/rate_limit": proxyconnect tcp: dial tcp 127.0.0.1:9: ` +
				`connect: connection refused`,
			StateUnavailable, ReasonConnectionRefused,
		},
		{
			"a proxy that answers 407",
			"fatal: unable to access 'https://github.com/naghuale/crewflow.git/': " +
				"Received HTTP code 407 from proxy after CONNECT",
			StateUnavailable, ReasonProxyAuth,
		},
		{
			"the words that name a proxy that wants a login",
			"fatal: unable to access 'https://github.com/naghuale/crewflow.git/': " +
				"Proxy Authentication Required",
			StateUnavailable, ReasonProxyAuth,
		},
		{
			"the prefix of Go and nothing else",
			"fatal: unable to access 'https://github.com/naghuale/crewflow.git/': " +
				"proxyconnect tcp: a sentence nobody has seen before",
			StateUnknown, ReasonCheckUnclassified,
		},
		{
			"a certificate through the same proxy",
			"fatal: unable to access 'https://github.com/naghuale/crewflow.git/': " +
				"proxyconnect tcp: tls: failed to verify certificate: x509: certificate signed " +
				"by unknown authority",
			StateUnavailable, ReasonTLS,
		},
		{
			"a handshake that ran out of time",
			"fatal: unable to access 'https://github.com/naghuale/crewflow.git/': " +
				"proxyconnect tcp: tls handshake timeout",
			StateUnavailable, ReasonTLS,
		},
		{
			"a name the proxy could not resolve",
			"proxyconnect tcp: dial tcp: lookup github.com on 192.0.2.10:53: " +
				"no such host",
			StateUnavailable, ReasonDNS,
		},
		{
			"text with a bare tls in it and no failure of a certificate",
			"fatal: unable to access 'https://github.com/naghuale/crewflow.git/': " +
				"the tls cache of this machine is locked by another process",
			StateUnknown, ReasonCheckUnclassified,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &machineOfTheTest{gitAnswers: []recordedGit{{said: tc.said, code: 128}}}
			checked, closeServer := machine(t, fake)
			defer closeServer()
			state := gitOfTheTest(t, checked)

			if state.Result != tc.want || state.Reason != tc.reason {
				t.Errorf("git came out as %q/%q (%s), want %q/%q",
					state.Result, state.Reason, state.Detail, tc.want, tc.reason)
			}
		})
	}
}

// TestTheReasonOfTheProviderOfTheModelIsItsOwn: nothing was asked about it and nothing
// could be, so it is `unknown` with the reason of that and not with the reason of a
// failure of the host of the code — a check that learned nothing is not a check that went
// wrong (docs/DESIGN.md §7d, §7j).
func TestTheReasonOfTheProviderOfTheModelIsItsOwn(t *testing.T) {
	fake := &machineOfTheTest{gitListed: aHeadOfMain}
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
	provider := states[2]
	if provider.Capability != CapabilityModelProvider {
		t.Fatalf("the third capability came out as %q, want %q", provider.Capability, CapabilityModelProvider)
	}
	if provider.Result != StateUnknown {
		t.Errorf("the provider of the model came out as %q (%s), want %q", provider.Result, provider.Detail, StateUnknown)
	}
	if provider.Reason != ReasonUsageNotProven {
		t.Errorf("the provider of the model came out with the reason %q, want %q: nothing was checked",
			provider.Reason, ReasonUsageNotProven)
	}
	// And the reason of the other two is the reason of their own checks.
	for _, state := range states[:2] {
		if state.Reason == ReasonUsageNotProven {
			t.Errorf("the capability %q carries the reason of the provider of the model", state.Capability)
		}
	}
}

// TestTheDeadProxyOfTheLiveRunIsARefusedConnection: the live run of c4a8fcf with the route
// through `127.0.0.1:9` — a port nothing listens on — called it
// `proxy-authentication-required` for `proxyconnect tcp: dial tcp 127.0.0.1:9: connect:
// connection refused`, and the same sentence came out of the check of the API of the host.
// Go writes `proxyconnect` on every failure of dialling a proxy, so the word names nothing
// and the reason is the one the rest of the sentence proves (F-119, ревью #156).
func TestTheDeadProxyOfTheLiveRunIsARefusedConnection(t *testing.T) {
	const dead = `Get "https://api.github.com/rate_limit": proxyconnect tcp: dial tcp 127.0.0.1:9: ` +
		`connect: connection refused`

	if got := networkReason(dead); got != ReasonConnectionRefused {
		t.Errorf("the reason of %q came out as %q, want %q", dead, got, ReasonConnectionRefused)
	}
	fake := &machineOfTheTest{gitAnswers: []recordedGit{{said: dead, code: 128}}}
	checked, closeServer := machine(t, fake)
	defer closeServer()

	state := gitOfTheTest(t, checked)

	if state.Result != StateUnavailable || state.Reason != ReasonConnectionRefused {
		t.Errorf("git came out as %q/%q (%s), want %q/%q",
			state.Result, state.Reason, state.Detail, StateUnavailable, ReasonConnectionRefused)
	}
	// And the same reason out of the typed ladder of the HTTP client — `*net.OpError` with
	// the error of the system inside it, which is what Go hands the check when the dial
	// itself was refused. No dial happens here: this is the ladder, not the network.
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
	if got := networkReasonOf(refused); got != ReasonConnectionRefused {
		t.Errorf("the reason of a typed refused dial came out as %q, want %q", got, ReasonConnectionRefused)
	}
	if got := networkReasonOf(&net.DNSError{Err: "no such host", Name: "github.com"}); got != ReasonDNS {
		t.Errorf("the reason of a typed failure of DNS came out as %q, want %q", got, ReasonDNS)
	}
	certificate := &tls.CertificateVerificationError{Err: errors.New("x509: certificate signed by unknown authority")}
	if got := networkReasonOf(certificate); got != ReasonTLS {
		t.Errorf("the reason of a typed certificate came out as %q, want %q", got, ReasonTLS)
	}
}

// gitOfTheTest is the capability of the git of one machine of a test, without the other
// two: a classifier is a test of one capability at a time.
func gitOfTheTest(t *testing.T, checked Machine) State {
	t.Helper()
	_, cfg := withFile(t, keysOf("mode", `"direct"`))
	route, err := Choose(cfg, "api.github.com")
	if err != nil {
		t.Fatalf("the route of a request: %v", err)
	}
	return checked.check(t.Context(), CapabilityGit, route, nil, "")
}

// TestTheCheckOfGitAsksForARefThatIsInTheRepository: `HEAD` of the branch the project
// merges into, without `--exit-code` — that flag made git answer with a code for a ref
// that is not in the repository, and a code alone was read as a route that works
// (F-119, ревью #156).
func TestTheCheckOfGitAsksForARefThatIsInTheRepository(t *testing.T) {
	fake := &machineOfTheTest{gitAnswers: []recordedGit{gitListedMain}}
	checked, closeServer := machine(t, fake)
	defer closeServer()

	state := gitOfTheTest(t, checked)

	if state.Result != StateAvailable {
		t.Fatalf("git came out as %q (%s), want %q", state.Result, state.Detail, StateAvailable)
	}
	if !strings.Contains(state.Detail, "refs/heads/main") {
		t.Errorf("the answer %q does not name the ref it read", state.Detail)
	}
	if len(fake.gitRan) != 1 {
		t.Fatalf("git was started %d times, want once", len(fake.gitRan))
	}
	asked := strings.Join(fake.gitRan[0], " ")
	for _, want := range []string{"ls-remote", "https://github.com/naghuale/crewflow.git", "refs/heads/main"} {
		if !strings.Contains(asked, want) {
			t.Errorf("git was asked %q, want %q in it", asked, want)
		}
	}
	if strings.Contains(asked, "--exit-code") {
		t.Errorf("git was asked %q, want no --exit-code: a code instead of an answer is not an answer", asked)
	}
}

// TestGITNET010ACheckThatWasStoppedIsInterruptedAndReplacesNothing: a person pressed
// Ctrl+C while the check was going on. Every capability of the route says `interrupted`
// with no moment and no time of its own, and the answer the straight route gave a moment
// before is what the report still holds — a check that was stopped replaces nothing
// (docs/DESIGN.md §6a, §7d).
func TestGITNET010ACheckThatWasStoppedIsInterruptedAndReplacesNothing(t *testing.T) {
	fake := &machineOfTheTest{gitAnswers: []recordedGit{gitListedMain, gitListedMain}}
	checked, closeServer := machine(t, fake)
	defer closeServer()
	_, cfg := withFile(t, keysOf("mode", `"direct"`))
	route, err := Choose(cfg, "api.github.com")
	if err != nil {
		t.Fatalf("the route of a request: %v", err)
	}

	answered, err := Check(t.Context(), checked, route, cfg.Network.NoProxy)
	if err != nil {
		t.Fatalf("the check of the straight route: %v", err)
	}
	if answered[1].Result != StateAvailable {
		t.Fatalf("the straight route came out as %q (%s), want %q before anything is stopped",
			answered[1].Result, answered[1].Detail, StateAvailable)
	}
	ctx, stop := context.WithCancel(t.Context())
	stop()
	capped, err := Check(ctx, checked, route, cfg.Network.NoProxy)

	if err != nil {
		t.Fatalf("the check of a route that was stopped: %v", err)
	}
	for _, state := range capped {
		if state.Result != StateInterrupted {
			t.Errorf("the capability %q came out as %q (%s), want %q",
				state.Capability, state.Result, state.Detail, StateInterrupted)
		}
		if !state.CheckedAt.IsZero() || state.Duration != "" {
			t.Errorf("the capability %q carries the moment %s and the time %q of a check that never happened",
				state.Capability, state.CheckedAt, state.Duration)
		}
	}
	if answered[1].Result != StateAvailable {
		t.Errorf("the answer of the straight route came out as %q, want the answer it gave: "+
			"a check that was stopped replaces nothing", answered[1].Result)
	}
}

// TestGITNET012TheResultsOfTwoRoutesAreTwoResults: the same git answering the same
// question is one answer, and the report of a route says about the route it went through.
// A result of one route never becomes the result of the other (docs/DESIGN.md §7d).
func TestGITNET012TheResultsOfTwoRoutesAreTwoResults(t *testing.T) {
	fake := &machineOfTheTest{gitAnswers: []recordedGit{
		gitListedMain,        // the straight route: the repository answered
		gitConnectionRefused, // the profile of the owner: nothing answered
		gitListedMain,        // the straight route again, as a doctor of a machine checks both
		gitConnectionRefused,
	}}
	checked, closeServer := machine(t, fake)
	defer closeServer()
	// The profile of the proxied route is a server of the test on 127.0.0.1: a check of a
	// route dials the profile it goes through, and the only profile a test may dial is one
	// of its own (guard_test.go).
	cfg := aProxyOfTheTest(t, "here", "none", &machineOfTheTest{})
	straight := Route{Direct: true, Why: `network.mode = "direct"`}
	proxied, err := Through(cfg, "here")
	if err != nil {
		t.Fatalf("the route through the profile here: %v", err)
	}

	direct, err := Check(t.Context(), checked, straight, cfg.Network.NoProxy)
	if err != nil {
		t.Fatalf("the check of the straight route: %v", err)
	}
	through, err := Check(t.Context(), checked, proxied, cfg.Network.NoProxy)
	if err != nil {
		t.Fatalf("the check of the profile: %v", err)
	}

	if direct[1].Result != StateAvailable {
		t.Errorf("the straight route came out as %q (%s), want %q", direct[1].Result, direct[1].Detail, StateAvailable)
	}
	if through[1].Result != StateUnavailable || through[1].Reason != ReasonConnectionRefused {
		t.Errorf("the route through the profile came out as %q/%q (%s), want %q/%q",
			through[1].Result, through[1].Reason, through[1].Detail, StateUnavailable, ReasonConnectionRefused)
	}
	if direct[1].Reason != ReasonRequestSucceeded {
		t.Errorf("the straight route carries the reason %q, want %q: it worked",
			direct[1].Reason, ReasonRequestSucceeded)
	}
	// And the git of each route was started with the environment of that route alone.
	if len(fake.gitWasGiven) != 2 {
		t.Fatalf("git was started %d times, want once per route", len(fake.gitWasGiven))
	}
	if len(fake.gitWasGiven[0]) != 0 {
		t.Errorf("the straight route started git with %q, want no proxy in it", fake.gitWasGiven[0])
	}
	address, err := proxied.Address("")
	if err != nil {
		t.Fatalf("the address of the profile of the test: %v", err)
	}
	if !contains(fake.gitWasGiven[1], "HTTP_PROXY="+address) {
		t.Errorf("the route through the profile started git with %q, want the profile of the route in it",
			fake.gitWasGiven[1])
	}
}

// TestAGitThatCouldNotBeStartedIsNotARouteThatDoesNotWork: a machine where git is not
// installed is a machine where the check could not be made, and crewflow says so instead
// of blaming the route of the project (docs/DESIGN.md §7d).
func TestAGitThatCouldNotBeStartedIsNotARouteThatDoesNotWork(t *testing.T) {
	fake := &machineOfTheTest{gitFails: errors.New(`exec: "git": executable file not found in $PATH`)}
	checked, closeServer := machine(t, fake)
	defer closeServer()

	state := gitOfTheTest(t, checked)

	if state.Result != StateUnknown || state.Reason != ReasonCheckUnclassified {
		t.Errorf("a git that could not be started came out as %q/%q (%s), want %q/%q: "+
			"nothing was learned about the route",
			state.Result, state.Reason, state.Detail, StateUnknown, ReasonCheckUnclassified)
	}
}
