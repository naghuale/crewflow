package network

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/secret"
)

// The mode `fallback` in one file: straight out first, and one more attempt through the
// active profile of the project when the first one could not reach the network — and
// nothing else. What is written here is the policy of that mode and not a route that
// outlives it: the second attempt belongs to the one request it was made for, and the
// requests after it go out the way the mode says (owner 02.10, docs/DESIGN.md §7d).
//
// Three things this file does not do, and each of them is a decision of a person:
//
//   - it does not change the route of the project. A project that goes through a proxy from
//     now on is a project whose file says `mode = "proxy"`, and `crewflow network proxy use`
//     is what picks the profile. A program that changed the file while a person reads it
//     changed a setting nobody asked it to change (docs/DESIGN.md §7d, §7e);
//   - it does not change the powers of the subject. The second attempt is made by the same
//     program, under the same account and with the same token as the first one: another road
//     is not another right, and a route that asked for more would widen the account of the run
//     on the way (SEC-NET-009, §7i);
//   - it does not repeat a request on a failure that is not a failure of the network. A
//     refusal of the login, a lack of rights, a wrong request, a failure of the settings or
//     of the task itself and an error crewflow cannot read are answers of the host or of the
//     task, and asking the same question twice through another road only hides it behind a
//     longer wait (F-119, docs/DESIGN.md §7d, §7e, MODEL: UNKNOWN не успех).

// The events of the route of a project, in the names the journal of a run reads them by
// (docs.DESIGN.md §6a, §7a). A run is read long after the network answered or did not, and
// these five lines are the whole of what a person learns about the route from it.
//
// None of them carries a credential of a profile: the value of the store is read for a
// connection that is really made and is never written down, and an event that held one would
// be a copy of a password in a file kept for ever (docs.DESIGN.md §7e, §7i).
const (
	// EventRouteSelected is the route an operation of a program of crewflow went out by.
	// It is said in the mode `fallback` alone: the other two modes are what the file of the
	// project says, and a journal that repeated the file line by line would be a journal
	// nobody reads (docs/DESIGN.md §7d).
	EventRouteSelected = "NETWORK_ROUTE_SELECTED"
	// EventRouteFailed is a proven failure of the network on the route an operation went
	// out by, with the reason of it out of the closed list of §7d.
	EventRouteFailed = "NETWORK_ROUTE_FAILED"
	// EventFallbackStarted is the one more attempt going through the active profile, and
	// which profile it is. There is one of them per operation and never more
	// (docs/DESIGN.md §7d).
	EventFallbackStarted = "NETWORK_FALLBACK_STARTED"
	// EventFallbackSucceeded is that second attempt having got to the host: the first
	// answer of the second route and not a promise about the next one (docs/DESIGN.md §7d).
	EventFallbackSucceeded = "NETWORK_FALLBACK_SUCCEEDED"
	// EventFallbackFailed is that second attempt not having got there either, or not having
	// been made at all because the credentials of the profile could not be read. Crewflow
	// says which of the two it is: the first is a fact about the route and the second only
	// about the machine (docs.DESIGN.md §7d, §7e).
	EventFallbackFailed = "NETWORK_FALLBACK_FAILED"
)

// ErrRouteUnavailable is the refusal of a program of crewflow that was made through both
// routes of the project and reached neither: straight out and through the active profile.
// It is an error of its own and not a sentence a caller has to read, because the outcome of
// a run is a different thing when both roads are closed than when one of them is: a wait for
// a resource of the machine with the work kept, and not a defect of the task
// (docs.DESIGN.md §6a, §7d).
var ErrRouteUnavailable = errors.New("no route to the network")

// Unavailable is both routes of a project and neither of them answering: the closed reason of
// the straight route and the closed reason of the route through the profile, with the name of
// the profile and nothing else. A person reads the line it makes in the queue of attention and
// knows what to look at without opening a journal (docs/DESIGN.md §6a, §7d).
type Unavailable struct {
	// Profile is the name of the profile the second attempt went through.
	Profile string
	// Direct is the reason the straight route did not get there.
	Direct string
	// Through is the reason the route through the profile did not get there either.
	Through string
}

// Error is one line for a person and for the state of a task: both routes of the project are
// named as unavailable, each with the reason that was proven for it.
func (u Unavailable) Error() string {
	return fmt.Sprintf("no route to the network: straight out — unavailable (%s); "+
		"through the profile %q — unavailable (%s)", u.Direct, u.Profile, u.Through)
}

// Unwrap is what makes [RouteUnavailable] answer for this error, so that a caller tells two
// closed roads from one failure of a task by asking about it and not by reading its words.
func (u Unavailable) Unwrap() error { return ErrRouteUnavailable }

// RouteUnavailable is whether an error of a program of crewflow is the refusal of both routes
// of the project, and what each of them answered. A caller that has to tell that from a failure
// of the task, of the host or of the settings of the project asks here
// (docs/DESIGN.md §6a, §7d).
func RouteUnavailable(err error) (Unavailable, bool) {
	var closed Unavailable
	if !errors.As(err, &closed) {
		return Unavailable{}, false
	}
	return closed, true
}

// Answer is what came of one operation of a program of crewflow: the two streams it wrote,
// apart, the code it exited with, and whether it could be started at all. These are the same
// fields the machine of a check of a route answers with, so that the two places where a
// program of crewflow talks to the network read one thing (docs/DESIGN.md §7d).
type Answer struct {
	// Stdout is what the program wrote to its standard output.
	Stdout string
	// Stderr is what it wrote about what went wrong, which is not its answer and never
	// becomes one.
	Stderr string
	// Code is what it exited with.
	Code int
	// Err is why it could not be started at all, or [Unavailable] where both routes of the
	// project were tried and neither answered.
	Err error
}

// said is everything the program wrote: a program of the machine may write the complaint of a
// failure to either of its streams, and a check that read one of them would know half of what
// happened.
func (a Answer) said() string { return a.Stdout + "\n" + a.Stderr }

// failed is whether the operation failed at all. A program that answered and mentioned a
// failure on the way did get where it was going, and its answer is not a failure of the
// network (docs.DESIGN.md §7d).
func (a Answer) failed() bool { return a.Err != nil || a.Code != 0 }

// Attempt is one operation of one program of crewflow under one route of the project: it is
// given that route and the environment of it, and it answers what came of the attempt. It is a
// function and not an interface of a host, because gh, git and the executor of any system are
// started the same way, and a rule of the route crewflow could not apply to one of them would
// not be a rule of the route (docs/DESIGN.md §7d).
type Attempt func(route Route, environment []string) (stdout, stderr string, code int, err error)

// Fallback is the machine one operation of a program of crewflow is made on: the settings of
// the project, the route the operation goes out by, the store the credentials of a profile are
// in, the clock, and where the events of the route are said.
//
// It is a value and not a row of arguments because every one of those is handed in, and an
// operation made with a store and a clock of its own is an operation of a machine of a test
// rather than of the machine the tests run on: no test of crewflow reads the keychain of the
// person who runs it (docs/DESIGN.md §7d, §7e, §7i).
type Fallback struct {
	// Config is the file of the project: the mode of the route, the profile to go through,
	// and the targets that go out straight whatever the mode says.
	Config config.Config
	// Route is the route the operation goes out by, as [Choose] worked it out. Its
	// `Fallback` is the profile a second attempt may go through, and it is empty in every
	// mode but `fallback`.
	Route Route
	// Secrets is where the credentials of a profile are. A nil store is a project with no
	// credentials read, and a route that needs them says so instead of being made with an
	// empty pair (docs.DESIGN.md §7e).
	Secrets secret.Store
	// Now is the clock the events of the route are stamped with.
	Now func() time.Time
	// Events is where those events are said: the terminal of the person who started the
	// command and, as soon as a run has a journal of its own, that journal (§7i).
	Events *secret.Notices
}

// Once is one operation of a program of crewflow under the route of the project: the route the
// file of the project names, and — only on a **proven repeatable** failure of the network, and
// only in the mode `fallback` — one more attempt through the active profile. What came of the
// attempt that was really made is what the caller gets, and where both failed the error it gets
// is [Unavailable] (docs/DESIGN.md §7d).
//
// The change of route does not outlive this call: the operations after it go out the way the
// mode says, and a permanent change of the route is a command of a person and not a decision of
// a program of crewflow (owner 02.10, §7d).
func (f Fallback) Once(ctx context.Context, do Attempt) Answer {
	if f.Route.Fallback != "" {
		f.say(EventRouteSelected, "route="+name(f.Route), "second="+f.Route.Fallback)
	}
	first, err := f.made(do, f.Route)
	if err != nil || ctx.Err() != nil || f.Route.Fallback == "" {
		// A route that could not be worked out is a route nothing was asked by; a project with
		// no second route, and a person who stopped the command, are both cases where there is
		// nothing to try and nothing to say about the network.
		return first.answer
	}
	if !first.answer.failed() {
		return first.answer
	}
	reason := Failure(first.answer.Err, first.answer.said())
	if !Repeatable(reason) {
		// The host answered and did not let the operation in, or crewflow cannot read what
		// went wrong. Neither is a failure of the network, and a second attempt would ask the
		// same question twice to learn the same answer a minute later (§7d, §7e).
		return first.answer
	}
	f.say(EventRouteFailed, "route="+name(f.Route), "reason="+reason, first.told())

	through, err := Through(f.Config, f.Route.Fallback)
	if err != nil {
		return first.answer
	}
	f.say(EventFallbackStarted, "profile="+through.Name, "attempts=1")
	second, err := f.made(do, through)
	if err != nil {
		// The credentials of the profile could not be read, so the attempt was not made at
		// all. That is a fact about this machine and not about the route, and the failure of
		// the straight route stands as it is: crewflow did not learn that the second route is
		// unavailable, only that it could not ask (§7e, MODEL: UNKNOWN не успех).
		f.say(EventFallbackFailed, "profile="+through.Name, "made=false",
			fmt.Sprintf("said=%q", oneLine(err.Error())))
		return first.answer
	}
	throughReason := ""
	if second.answer.failed() {
		throughReason = Failure(second.answer.Err, second.answer.said())
	}
	if !Repeatable(throughReason) {
		// The second attempt got to the host and the host answered: the route is not the
		// reason of the failure, and a failure of the task behind a proxy is the very thing
		// this mode must not hide (§7d, §7e).
		f.say(EventFallbackSucceeded, "profile="+through.Name, second.exited())
		return second.answer
	}
	f.say(EventFallbackFailed, "profile="+through.Name, "reason="+throughReason, second.told())
	second.answer.Err = Unavailable{Profile: through.Name, Direct: reason, Through: throughReason}
	return second.answer
}

// made is one attempt of one operation along a route: the credentials of that route are read
// for the attempt and go into the environment of the child process and nowhere else, as they
// do for every other connection crewflow makes through a route (docs/DESIGN.md §7d, §7e). The
// credentials of the profile of the second attempt are read here and not earlier, because this
// is where a connection through it is really made (§7e).
func (f Fallback) made(do Attempt, route Route) (tried, error) {
	credentials, err := Credentials(f.Secrets, route)
	if err != nil {
		return tried{}, err
	}
	environment, err := route.Environment(f.Config.Network.NoProxy, credentials)
	if err != nil {
		return tried{}, err
	}
	stdout, stderr, code, err := do(route, environment)
	return tried{
		answer:      Answer{Stdout: stdout, Stderr: stderr, Code: code, Err: err},
		credentials: credentials,
	}, nil
}

// say is one event of the route, stamped with the clock of the machine. An operation of a
// machine of a test has a clock of its own or none at all, and an event without a moment is
// still an event (docs/DESIGN.md §7i).
func (f Fallback) say(event string, facts ...string) {
	if f.Now != nil {
		facts = append(facts, "at="+f.Now().Format(time.RFC3339))
	}
	SayEvent(f.Events, event, facts...)
}

// tried is one attempt of one operation together with the credentials it was made with: the
// two belong to each other, because what the program wrote goes through the redactor with the
// value that was read for it — a proxy that answers with the password of its own profile must
// not have that password in a journal (docs/DESIGN.md §7e, §7i).
type tried struct {
	answer      Answer
	credentials string
}

// told is what the program wrote, as one fact of an event, with nothing of a credential in it:
// one line, because a failure spread over three lines is a failure nobody reads whole
// (docs.DESIGN.md §7a, §7e).
func (t tried) told() string {
	line := oneLine(t.answer.said())
	if line == "" {
		return ""
	}
	return fmt.Sprintf("said=%q", secret.Redact(line, t.secrets()...))
}

// exited is how the attempt ended, as one fact of an event: the code it exited with, or that
// it could not be started at all.
func (t tried) exited() string {
	if t.answer.Err != nil {
		return "started=false"
	}
	return "exit=" + strconv.Itoa(t.answer.Code)
}

// secrets are the values that must not reach a journal out of the credentials of the profile:
// the pair itself and its password, which a program may print on its own (§7e).
func (t tried) secrets() []string {
	if t.credentials == "" {
		return nil
	}
	values := []string{t.credentials}
	if _, password, found := strings.Cut(t.credentials, ":"); found {
		values = append(values, password)
	}
	return values
}

// Failure is the reason of a failure of the network in what one operation of a program of
// crewflow was told, by the ladder of §7d: what Go says the error is first, and the words the
// program wrote after that. An empty string proves nothing — the class of the failure is not
// known — and a caller that acted on an empty string would repeat a request for a reason nobody
// read (docs/DESIGN.md §7d, §7e).
//
// The words are the two streams of the program joined: the answer of gh and the complaint of git
// are two streams and not one.
func Failure(err error, said string) string {
	if err != nil {
		if reason := networkReasonOf(err); reason != "" {
			return reason
		}
	}
	return networkReason(said)
}

// Repeatable is whether a reason is one of the five proven failures of the network — the only
// ones a check may call `unavailable` for, and the only ones the mode `fallback` answers with a
// second attempt. `unknown` is not among them, and neither is any reason of a host that
// answered: the network got there and the account it went as did not, and that is a decision of
// a person rather than another road to try (docs/DESIGN.md §7d, §7e).
func Repeatable(reason string) bool {
	switch reason {
	case ReasonDNS, ReasonConnectionRefused, ReasonConnectionTimeout, ReasonTLS, ReasonProxyAuth:
		return true
	}
	return false
}

// name is how a route is named in an event: `direct` for a straight one and the name of the
// profile for a route through it, because "через прокси" in a journal is a word with no profile
// behind it (docs.DESIGN.md §7d, §7i).
func name(route Route) string {
	if route.Direct || route.Name == "" {
		return "direct"
	}
	return route.Name
}

// SayEvent writes one event of the route into the journal of the run that is going and into the
// terminal of the person who started it, in the words of the other events of a run: the name
// first, because that is what a program and a person look for, and then the facts it happened
// with (docs.DESIGN.md §6a, §7a, §7i).
//
// The zero Notices and a nil one say nothing: an operation that was given nowhere to say what it
// did is an operation of a machine of a test, and a program that cannot say where it would say it
// says nothing at all.
func SayEvent(events *secret.Notices, event string, facts ...string) {
	if events == nil {
		return
	}
	line := "crewflow: event " + event
	for _, fact := range facts {
		if fact != "" {
			line += " " + fact
		}
	}
	events.Say(line)
}
