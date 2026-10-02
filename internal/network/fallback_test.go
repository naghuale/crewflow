package network

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/secret"
)

// The words a program of the machine writes when the network is not there, and when it is
// there and answered no. They are the words of gh on a machine with no route and on a
// machine whose account does not fit, and the mode `fallback` reads them the way it reads
// every failure of the network: by the closed list of §7d, and by nothing else.
const (
	noSuchHost    = "error connecting to api.github.com: dial tcp: lookup api.github.com: no such host"
	refusedDial   = `Get "https://api.github.com/rate_limit": dial tcp 192.0.2.10:1082: connect: connection refused`
	badCredential = "HTTP 401: Bad credentials (https://api.github.com/rate_limit)"
	noRights      = "HTTP 403: Resource not accessible by integration (https://api.github.com/rate_limit)"
	wrongRequest  = "HTTP 404: Not Found (https://api.github.com/rate_limit)"
	unreadable    = "the flag --json is not a flag of gh"
)

// recorder is the machine one operation of a program of crewflow is made on in the cases
// of this file: what each attempt was given and what it answered, the events of the route
// as they were said, a clock that stands still, and a store of the secrets of the machine
// of the test and never the keychain of a person (docs/DESIGN.md §7d, §7i).
type recorder struct {
	// attempts is every start of the program, in the order they were made: how many
	// attempts were made is half of what this mode promises, and a case that cannot count
	// them cannot hold it.
	attempts []attempt
	// said is where the events of the route are said.
	said bytes.Buffer
	// clock is the moment the events are stamped with.
	clock time.Time
	// secrets is where the credentials of a profile are in this case.
	secrets secret.Store
}

// attempt is one start of a program: the route it went out by, the environment it was
// given and what it answered.
type attempt struct {
	route       Route
	environment []string
}

// fallback is the machine of the operations of this file: the file of the project, the
// route of it, the store of the secrets of the machine and where the events are said.
func (r *recorder) fallback(cfg config.Config, route Route) Fallback {
	return Fallback{
		Config:  cfg,
		Route:   route,
		Secrets: r.secrets,
		Now:     func() time.Time { return r.clock },
		Events:  secret.NewNotices(&r.said),
	}
}

// do is the program of these cases: it answers what the case told it to answer for that
// route, and it counts every start of itself.
func (r *recorder) do(answers map[string]answer) Attempt {
	return func(route Route, environment []string) (string, string, int, error) {
		answered, known := answers[name(route)]
		if !known {
			answered = answers[""]
		}
		r.attempts = append(r.attempts, attempt{route: route, environment: environment})
		return answered.stdout, answered.stderr, answered.code, answered.err
	}
}

// answer is what the program of a case said when it was started along a route.
type answer struct {
	stdout string
	stderr string
	code   int
	err    error
}

// saidEvents are the names of the events of the route, in the order they were said.
func (r *recorder) saidEvents() []string {
	var names []string
	for line := range strings.Lines(r.said.String()) {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "crewflow:" && fields[1] == "event" {
			names = append(names, fields[2])
		}
	}
	return names
}

// has is whether one of the lines of the machine names the event and says the fact: a
// check of a fact of an event reads the line and not the name of the event alone.
func (r *recorder) has(event, fact string) bool {
	for line := range strings.Lines(r.said.String()) {
		if strings.Contains(line, "event "+event) && strings.Contains(line, fact) {
			return true
		}
	}
	return false
}

// TestCFNET003TheStraightRouteIsTheOnlyOneThatIsTriedWhenItAnswers is the mode `fallback`
// at its ordinary work: the request goes out straight and the profile is not touched at
// all. A proxy that is asked for nothing is a proxy nobody asked for, and a report that
// named the profile of a run that worked would send a person after a route that was never
// in the way (docs/DESIGN.md §7d).
func TestCFNET003TheStraightRouteIsTheOnlyOneThatIsTriedWhenItAnswers(t *testing.T) {
	_, cfg := withFile(t, keysOf("mode", `"fallback"`, "active_proxy", `"home"`))
	route, err := Choose(cfg, "")
	if err != nil {
		t.Fatalf("the route of the project: %v", err)
	}
	r := &recorder{clock: time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)}

	answered := r.fallback(cfg, route).Once(t.Context(),
		r.do(map[string]answer{"direct": {stdout: `{"rate": {}}`}}))

	if answered.Err != nil || answered.Code != 0 {
		t.Errorf("the operation answered with %d and %v, want the answer of the host", answered.Code, answered.Err)
	}
	if len(r.attempts) != 1 {
		t.Fatalf("the program was started %d times, want once: a route that answers is not retried", len(r.attempts))
	}
	if len(r.attempts[0].environment) != 0 {
		t.Errorf("the straight attempt was given %q, want nothing: it goes out as it is", r.attempts[0].environment)
	}
	if r.has(EventFallbackStarted, "profile=") {
		t.Errorf("the profile was named although the straight route answered: %q", r.said.String())
	}
	if !slices.Contains(r.saidEvents(), EventRouteSelected) {
		t.Errorf("the route the operation went out by is not in the journal: %q", r.said.String())
	}
}

// TestCFNET004AProvenFailureOfTheNetworkIsMadeOneMoreTimeThroughTheProfile is the promise
// of the mode: a straight route that could not reach the network and named one of the five
// proven failures gets one more attempt, through the active profile of the project, and
// what that attempt is answered goes to the caller (docs/DESIGN.md §7d, F-119).
func TestCFNET004AProvenFailureOfTheNetworkIsMadeOneMoreTimeThroughTheProfile(t *testing.T) {
	for _, said := range []string{noSuchHost, refusedDial} {
		t.Run(said, func(t *testing.T) {
			_, cfg := withFile(t, keysOf("mode", `"fallback"`, "active_proxy", `"home"`))
			route, err := Choose(cfg, "")
			if err != nil {
				t.Fatalf("the route of the project: %v", err)
			}
			r := &recorder{clock: time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)}

			answered := r.fallback(cfg, route).Once(t.Context(), r.do(map[string]answer{
				"direct": {stderr: said, code: 1},
				"home":   {stdout: `{"rate": {}}`},
			}))

			if answered.Err != nil || answered.Code != 0 {
				t.Errorf("the operation answered with %d and %v, want the answer of the host through the profile",
					answered.Code, answered.Err)
			}
			if len(r.attempts) != 2 {
				t.Fatalf("the program was started %d times, want the straight one and one through the profile", len(r.attempts))
			}
			second := r.attempts[1]
			if second.route.Name != "home" || second.route.Direct {
				t.Errorf("the second attempt went out through %q, want the active profile of the project", name(second.route))
			}
			for _, want := range []string{"HTTP_PROXY=http://192.0.2.10:1082", "HTTPS_PROXY=http://192.0.2.10:1082"} {
				if !slices.Contains(second.environment, want) {
					t.Errorf("the second attempt was given %q, want %q in it", second.environment, want)
				}
			}
			for _, event := range []string{EventRouteSelected, EventRouteFailed, EventFallbackStarted, EventFallbackSucceeded} {
				if !slices.Contains(r.saidEvents(), event) {
					t.Errorf("the journal holds no %s: %q", event, r.said.String())
				}
			}
			if r.has(EventFallbackFailed, "profile=") {
				t.Errorf("the journal holds a failure of a second attempt that answered: %q", r.said.String())
			}
		})
	}
}

// TestCFNET005NothingButAProvenFailureOfTheNetworkIsRetried is the other half of the
// promise: a host that answered and did not let the operation in is not a network that is
// not there, and asking it again through another road repeats a refusal of the login, a
// lack of rights or a wrong request and hides the answer behind a longer wait
// (F-119, docs/DESIGN.md §7d, §7e).
func TestCFNET005NothingButAProvenFailureOfTheNetworkIsRetried(t *testing.T) {
	for _, said := range []string{badCredential, noRights, wrongRequest, unreadable, ""} {
		t.Run(said, func(t *testing.T) {
			_, cfg := withFile(t, keysOf("mode", `"fallback"`, "active_proxy", `"home"`))
			route, err := Choose(cfg, "")
			if err != nil {
				t.Fatalf("the route of the project: %v", err)
			}
			r := &recorder{clock: time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)}

			answered := r.fallback(cfg, route).Once(t.Context(), r.do(map[string]answer{
				"direct": {stderr: said, code: 1},
				"home":   {stdout: `{"rate": {}}`},
			}))

			if len(r.attempts) != 1 {
				t.Fatalf("the program was started %d times, want once: %q is not a failure of the network",
					len(r.attempts), said)
			}
			if _, both := RouteUnavailable(answered.Err); both {
				t.Errorf("the answer of the host was read as two closed roads: %v", answered.Err)
			}
			if r.has(EventFallbackStarted, "profile=") {
				t.Errorf("the journal holds a second attempt: %q", r.said.String())
			}
		})
	}
}

// TestCFNET006AReasonNobodyCanReadIsNoReasonToTryAgain is the ladder of §7d read the way
// the queue reads it: a failure that proves nothing is `unknown`, and `unknown` is no
// reason to change the route. A retry on it is a request made twice for a cause nobody read,
// and the second answer is as unreadable as the first (MODEL: UNKNOWN не успех, §7d).
func TestCFNET006AReasonNobodyCanReadIsNoReasonToTryAgain(t *testing.T) {
	_, cfg := withFile(t, keysOf("mode", `"fallback"`, "active_proxy", `"home"`))
	route, err := Choose(cfg, "")
	if err != nil {
		t.Fatalf("the route of the project: %v", err)
	}
	r := &recorder{clock: time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)}

	answered := r.fallback(cfg, route).Once(t.Context(), r.do(map[string]answer{
		"direct": {stderr: "something happened and crewflow cannot read it", code: 2},
		"home":   {stdout: `{"rate": {}}`},
	}))

	if len(r.attempts) != 1 {
		t.Fatalf("the program was started %d times, want once: nothing proved a failure of the network", len(r.attempts))
	}
	if answered.Code != 2 || answered.Err != nil {
		t.Errorf("the operation answered with %d and %v, want the answer of the host as it is", answered.Code, answered.Err)
	}
	if Failure(nil, "something happened and crewflow cannot read it") != "" {
		t.Error("a failure with no closed word in it was read as a proven one")
	}
}

// TestCFNET007BothRoutesUnansweredIsTheRefusalOfBothRoutes is the end of the mode: both
// roads are closed, and the error says so in the words of §6a — straight out unavailable
// with its own reason, the profile unavailable with its own, and nothing to guess. A caller
// tells that from a failure of a task by asking about it (docs/DESIGN.md §6a, §7d).
func TestCFNET007BothRoutesUnansweredIsTheRefusalOfBothRoutes(t *testing.T) {
	_, cfg := withFile(t, keysOf("mode", `"fallback"`, "active_proxy", `"home"`))
	route, err := Choose(cfg, "")
	if err != nil {
		t.Fatalf("the route of the project: %v", err)
	}
	r := &recorder{clock: time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)}

	answered := r.fallback(cfg, route).Once(t.Context(), r.do(map[string]answer{
		"direct": {stderr: noSuchHost, code: 1},
		"home":   {stderr: refusedDial, code: 1},
	}))

	closed, both := RouteUnavailable(answered.Err)
	if !both {
		t.Fatalf("the error of the operation is %v, want the refusal of both routes", answered.Err)
	}
	if closed.Profile != "home" || closed.Direct != ReasonDNS || closed.Through != ReasonConnectionRefused {
		t.Errorf("the refusal says %+v, want the profile %q with %q and %q", closed, "home", ReasonDNS, ReasonConnectionRefused)
	}
	for _, want := range []string{"straight out", "unavailable", "home", ReasonConnectionRefused} {
		if !strings.Contains(closed.Error(), want) {
			t.Errorf("the line %q does not say %q", closed.Error(), want)
		}
	}
	if !r.has(EventFallbackFailed, "reason="+ReasonConnectionRefused) {
		t.Errorf("the journal does not hold the failure of the second attempt: %q", r.said.String())
	}
	if !errors.Is(answered.Err, ErrRouteUnavailable) {
		t.Error("the error of both routes is not the sentinel of it")
	}
}

// TestCFNET008ThereIsNoThirdAttempt is the promise about the number: one straight attempt
// and one through the profile, and never a third one. A route that kept trying hides how
// long an operation really took behind attempts nobody asked for (docs/DESIGN.md §7d).
func TestCFNET008ThereIsNoThirdAttempt(t *testing.T) {
	_, cfg := withFile(t, keysOf("mode", `"fallback"`, "active_proxy", `"home"`))
	route, err := Choose(cfg, "")
	if err != nil {
		t.Fatalf("the route of the project: %v", err)
	}
	r := &recorder{clock: time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)}

	r.fallback(cfg, route).Once(t.Context(), r.do(map[string]answer{
		"direct": {stderr: noSuchHost, code: 1},
		"home":   {stderr: refusedDial, code: 1},
	}))

	if len(r.attempts) != 2 {
		t.Errorf("the program was started %d times, want exactly two: straight out and one through the profile", len(r.attempts))
	}
	if started := strings.Count(r.said.String(), "event "+EventFallbackStarted); started != 1 {
		t.Errorf("the second attempt is announced %d times, want once", started)
	}
}

// TestTheModeWithoutAProfileIsARefusalAndNotAStraightRoute: a project that asks for a
// second road and names no profile is a file no program can work with, and crewflow says so
// instead of sending the request out straight and telling nobody that the second route was
// never there (docs/DESIGN.md §7d, §7e).
func TestTheModeWithoutAProfileIsARefusalAndNotAStraightRoute(t *testing.T) {
	_, cfg := withFile(t, keysOf("mode", `"fallback"`))

	if _, err := Choose(cfg, ""); err == nil {
		t.Error("the route of a project in the mode fallback and with no profile was worked out, " +
			"want a refusal that names the command that picks one")
	}

	// A file that names a profile nobody defined is refused by the loader of the file itself,
	// and a settings value that says so anyway — a program of a person building a Config by
	// hand, a test — is refused by the same route choice, because a route through a profile
	// that is not there is not a route (docs/DESIGN.md §7d, §7e).
	cfg.Network.ActiveProxy = "office"

	if _, err := Choose(cfg, ""); err == nil {
		t.Error("the route of a project in the mode fallback was worked out through a profile nobody defined")
	}
}

// TestTheSecondRouteIsNotKeptForTheNextRequest is the decision of the owner of 02.10: a
// change of the route lives in the one request it was made for. The next operation of the
// same process goes out straight, and the file of the project is as it was (§7d).
func TestTheSecondRouteIsNotKeptForTheNextRequest(t *testing.T) {
	_, cfg := withFile(t, keysOf("mode", `"fallback"`, "active_proxy", `"home"`))
	route, err := Choose(cfg, "")
	if err != nil {
		t.Fatalf("the route of the project: %v", err)
	}
	r := &recorder{clock: time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)}
	straight := 0
	program := func(route Route, environment []string) (string, string, int, error) {
		r.attempts = append(r.attempts, attempt{route: route, environment: environment})
		if route.Direct && straight == 0 {
			straight++
			return "", noSuchHost, 1, nil
		}
		return `{"rate": {}}`, "", 0, nil
	}
	machine := r.fallback(cfg, route)

	machine.Once(t.Context(), program)
	machine.Once(t.Context(), program)

	if len(r.attempts) != 3 {
		t.Fatalf("the program was started %d times, want three: two straight and one through the profile", len(r.attempts))
	}
	if third := r.attempts[2]; !third.route.Direct || third.route.Name != "" {
		t.Errorf("the operation after the second attempt went out through %q, want straight out", name(third.route))
	}
	if _, again := Choose(cfg, ""); again != nil {
		t.Error("the route of the project was changed by an operation of it")
	}
}

// TestTheEventsOfTheRouteCarryNoCredentialOfAProfile: the value of the store is read for a
// connection and is never written down, whatever the program on the other end of the profile
// prints — including the credentials of that very profile (docs.DESIGN.md §7e).
func TestTheEventsOfTheRouteCarryNoCredentialOfAProfile(t *testing.T) {
	_, cfg := withFile(t, keysOf("mode", `"fallback"`, "active_proxy", `"work"`))
	route, err := Choose(cfg, "")
	if err != nil {
		t.Fatalf("the route of the project: %v", err)
	}
	pair := "ann:s3cret-of-the-proxy"
	// The words of a failure of the network that carry the credentials of the profile in the
	// middle of them: a proxy that gives its own login away in its complaint is not rare, and
	// the journal of a run keeps for ever what it says.
	refused := "dial tcp 127.0.0.1:9: connect: connection refused, login " + pair
	store := &storeOfTheTest{values: map[string]string{
		secret.Service + "/" + secret.ProxyKey("work"): pair,
	}}
	r := &recorder{clock: time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC), secrets: store}

	r.fallback(cfg, route).Once(t.Context(), r.do(map[string]answer{
		"direct": {stderr: noSuchHost, code: 1},
		"work":   {stderr: refused, code: 1},
	}))

	for _, value := range []string{pair, "s3cret-of-the-proxy"} {
		if strings.Contains(r.said.String(), value) {
			t.Errorf("the journal of the route holds the value %q of a credential: %q", value, r.said.String())
		}
	}
	if !r.has(EventFallbackFailed, secret.Redacted) {
		t.Errorf("the failure of the second attempt says nothing of what the proxy wrote: %q", r.said.String())
	}
}

// TestTheCredentialsOfTheProfileOfTheSecondAttemptAreReadWhenItIsMade: the value is read
// where a connection through the profile is really made and nowhere else, and a profile
// whose credentials cannot be read is not tried with an empty pair — a proxy that wants a
// login and gets nothing answers 407, and a person would be sent to the proxy instead of to
// their own store (docs/DESIGN.md §7d, §7e).
func TestTheCredentialsOfTheProfileOfTheSecondAttemptAreReadWhenItIsMade(t *testing.T) {
	_, cfg := withFile(t, keysOf("mode", `"fallback"`, "active_proxy", `"work"`))
	route, err := Choose(cfg, "")
	if err != nil {
		t.Fatalf("the route of the project: %v", err)
	}
	r := &recorder{clock: time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC), secrets: &storeWithoutAnything{}}

	answered := r.fallback(cfg, route).Once(t.Context(), r.do(map[string]answer{
		"direct": {stderr: noSuchHost, code: 1},
		"work":   {stdout: `{"rate": {}}`},
	}))

	if len(r.attempts) != 1 {
		t.Errorf("the program was started %d times, want once: the second attempt was not made at all", len(r.attempts))
	}
	if _, both := RouteUnavailable(answered.Err); both {
		t.Error("an attempt that was not made was read as a second route that is unavailable")
	}
	if !r.has(EventFallbackFailed, "made=false") {
		t.Errorf("the journal does not say that the second attempt was not made: %q", r.said.String())
	}
	if answered.Stderr != noSuchHost {
		t.Errorf("the answer of the operation is %q, want the words of the straight route as they are", answered.Stderr)
	}
}

// storeWithoutAnything is a store of a machine where nothing of this project was kept.
type storeWithoutAnything struct{}

func (storeWithoutAnything) Get(string, string) ([]byte, error) { return nil, secret.ErrNotFound }

func (storeWithoutAnything) Set(string, string, []byte) error { return nil }
