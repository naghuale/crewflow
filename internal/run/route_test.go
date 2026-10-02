package run

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/network"
)

// The facts this file is here for (F-119, journal #37): on 02.10 the host of a project was
// not reachable from the machine of the owner, and a run that had already worked died with
// the words of a name that did not resolve — while the profile of the project was there and
// nobody had asked for it. The mode `fallback` of §7d is the answer to that, and this file
// holds what it promises: one more attempt through the profile for the one request that
// could not reach the network, the work of the task kept either way, and a queue that says
// both routes are unavailable rather than a bare failure nobody can read (F-119,
// docs/DESIGN.md §6a, §7d).

// closedRoutes is the refusal of a host that was asked through both routes of a project and
// reached neither: what [network.Fallback] hands back to the roles, and what the roles hand
// to the run.
func closedRoutes() error {
	return network.Unavailable{
		Profile: "home",
		Direct:  network.ReasonDNS,
		Through: network.ReasonConnectionRefused,
	}
}

// unrouteableProject is a machine, a host and the settings of a project whose host cannot
// be reached: the worktree of the task is a folder of the test, the executor is the program
// of the machine, and the file of the project asks for the mode `fallback` with one profile.
func unrouteableProject(t *testing.T) (*machine, *host, config.Config) {
	t.Helper()
	m := newMachine(t)
	m.says("opencode", worked())
	m.answers["git diff --name-only"] = answer{stdout: "internal/run/run.go\n"}
	host := &host{task: taskOf(43), opened: true, noChange: closedRoutes()}
	cfg := projectOf(t, m.worktrees, "")
	cfg.Network = config.Network{
		Mode:        "fallback",
		ActiveProxy: "home",
		NoProxy:     []string{".internal.example"},
		Proxies: map[string]config.Proxy{
			"home": {Type: "http", Host: "192.0.2.10", Port: 1082, Credentials: "none"},
		},
	}
	return m, host, cfg
}

// TestCFNET007ARunWhoseHostIsNotReachableAnywhereWaitsWithItsWorkKept is the whole of the
// failure this mode is here for: the host was asked straight out and through the profile,
// neither answered, and the run ends as a wait with the reason of §6a in it — the worktree,
// the branch and the session of the task are where the run left them, and a person goes on
// from there instead of starting the task again (F-119, docs/DESIGN.md §6a, §7d).
func TestCFNET007ARunWhoseHostIsNotReachableAnywhereWaitsWithItsWorkKept(t *testing.T) {
	m, host, cfg := unrouteableProject(t)

	result, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	if result.Outcome != Blocked {
		t.Fatalf("the run came out as %q, want %q: a host nobody can reach is a wait and not a defect of the task",
			result.Outcome, Blocked)
	}
	if !strings.HasPrefix(result.Reason, ReasonRouteUnavailable+":") {
		t.Errorf("the reason of the run = %q, want it to begin with %q", result.Reason, ReasonRouteUnavailable)
	}
	for _, want := range []string{"straight out", network.ReasonDNS, "home", network.ReasonConnectionRefused} {
		if !strings.Contains(result.Reason, want) {
			t.Errorf("the reason of the run = %q, want %q in it: both routes and each reason of its own", result.Reason, want)
		}
	}
	state := stateOf(t, m, 43)
	if len(state.Attempts) != 1 {
		t.Fatalf("the state holds %d attempts, want one: crewflow does not go on by itself here", len(state.Attempts))
	}
	last := state.Attempts[0]
	if !strings.HasPrefix(last.Reason, ReasonRouteUnavailable+":") {
		t.Errorf("the state of the attempt says %q, want it to begin with %q: the queue reads this and no journal",
			last.Reason, ReasonRouteUnavailable)
	}
	if last.AutoResumed != "" {
		t.Errorf("the attempt went on for %q, want nothing: the one second attempt is spent", last.AutoResumed)
	}
}

// TestCFNET009TheWorkOfTheTaskIsKeptWhenNoRouteAnswers is what a person comes back to in
// the morning: the worktree of the task is there, with what the executor wrote in it, and
// the branch stands where the run pushed it. A run that took its worktree away because the
// network went is a run that threw the night away (docs/DESIGN.md §7a, §7d).
func TestCFNET009TheWorkOfTheTaskIsKeptWhenNoRouteAnswers(t *testing.T) {
	m, host, cfg := unrouteableProject(t)

	result, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	if result.Worktree == "" {
		t.Fatal("the run left no worktree, want the work of the task where the run left it")
	}
	if _, err := os.Stat(result.Worktree); err != nil {
		t.Errorf("the worktree of the task is not there: %v", err)
	}
	if result.Session == "" {
		t.Error("the run left no session, want the session the continuation goes on in")
	}
	if result.Branch != Branch(host.task) {
		t.Errorf("the branch of the run is %q, want %q", result.Branch, Branch(host.task))
	}
	journal := journalOf(t, result)
	if !strings.Contains(journal, theRun) {
		t.Error("the journal of the attempt holds nothing of what the executor wrote")
	}
}

// TestTheQueueOfAClosedRouteSaysBothRoutesAndTheCommandToGoOn is what a person finds in the
// queue of attention: that the network got to neither road, with the reasons of §7d, that the
// work of the task is kept, and the command that goes on from it (docs.DESIGN.md §6a, §7d).
func TestTheQueueOfAClosedRouteSaysBothRoutesAndTheCommandToGoOn(t *testing.T) {
	m, host, cfg := unrouteableProject(t)

	if _, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo}); err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	entry := attentionOnly(t, m.home, "naghuale-crewflow", attentionEnvOf(m.at()))
	if entry.State != AttentionBlocked {
		t.Errorf("the state of the entry = %q, want %q", entry.State, AttentionBlocked)
	}
	if entry.Reason != ReasonRouteUnavailable {
		t.Errorf("the reason of the entry = %q, want %q", entry.Reason, ReasonRouteUnavailable)
	}
	if entry.Subject != SubjectNetworkRoute {
		t.Errorf("the entry waits for %q, want %q", entry.Subject, SubjectNetworkRoute)
	}
	for _, want := range []string{"straight out", network.ReasonDNS, "home", network.ReasonConnectionRefused} {
		if !strings.Contains(entry.Hint, want) {
			t.Errorf("the hint of the entry = %q, want %q in it: both routes and each reason of its own",
				entry.Hint, want)
		}
	}
	if !strings.Contains(entry.Hint, "kept") {
		t.Errorf("the hint of the entry = %q, want it to say that the work of the task is kept", entry.Hint)
	}
	want := fmt.Sprintf("crewflow task run 43 -continue %q", "what is left to do")
	if entry.Next != want {
		t.Errorf("the entry says to do %q, want %q: there is no point of a run to resume from here", entry.Next, want)
	}
	if entry.NextActor != ActorOrchestrator || entry.Actable != ActNow {
		t.Errorf("the entry waits for %q and says %q is actable, want the orchestrator and %q",
			entry.NextActor, entry.Actable, ActNow)
	}
}

// TestARunWhoseHostAnsweredIsNotAWaitOfThisKind: the closed list of §6a is held to the code
// of it. A run that failed for a reason of its own, or that could not be judged, is not a
// wait for the network of the machine — a queue that called it that would send a person
// after the network for a defect of the task (docs/DESIGN.md §6a, §7e).
func TestARunWhoseHostAnsweredIsNotAWaitOfThisKind(t *testing.T) {
	m := newMachine(t)
	m.says("opencode", worked())
	m.answers["git diff --name-only"] = answer{stdout: "internal/run/run.go\n"}
	host := &host{task: taskOf(43), opened: true}

	result, err := Run(t.Context(), m.env(), projectOf(t, m.worktrees, ""), host.set(),
		Request{Number: 43, RepoDir: m.repo})

	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}
	if result.Outcome != ChangeRequestOpened {
		t.Errorf("the run came out as %q, want %q", result.Outcome, ChangeRequestOpened)
	}
	if _, both := network.RouteUnavailable(errorOf(result)); both {
		t.Error("the run of a host that answered was read as a run that reached no route")
	}
	state := stateOf(t, m, 43)
	if reason := state.Attempts[0].Reason; strings.HasPrefix(reason, ReasonRouteUnavailable) {
		t.Errorf("the state of the attempt says %q, want nothing of the route of the network", reason)
	}
}

// errorOf is the error of a run, and there is none where the run came out as it should: this
// file asks about the refusal of both routes and not about the outcome of the run.
func errorOf(Result) error { return nil }
