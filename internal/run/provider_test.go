package run

import (
	"strings"
	"testing"

	"github.com/naghuale/crewflow/internal/config"
)

// The facts this task is here for (F-119, journal #37): on 02.10 two runs of the same
// night died with «Cannot connect to API (https://opencode.ai)» and `isRetryable: true`,
// and each of them came out of `crewflow task run` as a plain failure of the executor —
// a task that a person had to start again by hand, with the branch and the worktree of
// the run before it left behind and nothing saying why.
//
// A refusal of the model provider is a wait for a resource of the machine, not a defect of
// the task: the work of the task, the branch and the session stay where the run left them,
// a point says where to go on from, and crewflow goes on once by itself (F-119,
// docs/DESIGN.md §6a, §7a, §7i).

// theProviderRefused is what a run of OpenCode writes when the provider does not answer:
// an event of the run with the class of the failure, the words of it and the mark that
// says the refusal may be repeated (F-119).
func theProviderRefused(retryable bool) string {
	mark := "false"
	if retryable {
		mark = "true"
	}
	return `{"type":"error","sessionID":"ses_7fKq2","part":{"type":"error","error":{"name":"AI_APICallError",` +
		`"data":{"message":"Cannot connect to API (https://opencode.ai)","isRetryable":` + mark + `}}}}` + "\n"
}

// refusedProvider is what the machine answers when the model provider refused the run: the
// words of the provider with the mark that says the refusal may be repeated, and the code
// an agent of that exits with (F-119).
func refusedProvider() answer {
	return answer{stdout: theProviderRefused(true), code: 1}
}

// refusedProviderWithoutMark is what the machine answers when the provider refused the run
// and said nothing about whether the refusal may be repeated.
func refusedProviderWithoutMark(message string) answer {
	return answer{
		stdout: `{"type":"error","sessionID":"ses_7fKq2","part":{"type":"error","error":` +
			`{"name":"ProviderAuthError","data":{"message":"` + message + `"}}}}` + "\n",
		code: 1,
	}
}

// worked is what the machine answers when the run did its work and opened the change
// request of its branch (docs.DESIGN.md §7a).
func worked() answer { return answer{stdout: theRun} }

// refusedProject is a machine, a host and the settings of a project of a test whose task 43
// the model provider refused: the worktree of the task is a folder of the test, the executor
// is the program of the machine, and the host has the change request of the branch.
func refusedProject(t *testing.T, attempts ...answer) (*machine, *host, config.Config) {
	t.Helper()
	m := newMachine(t)
	m.says("opencode", attempts...)
	m.answers["git diff --name-only"] = answer{stdout: "internal/run/run.go\n"}
	host := &host{task: taskOf(43), opened: true}
	return m, host, projectOf(t, m.worktrees, "")
}

// TestPRV001ARefusalOfTheModelProviderIsAWaitWithTheWorkKept is the whole of the failure
// this is here for. The provider refused the run, crewflow went on once in the same
// session with the work where it was, and the run opened its change request: nobody has to
// start the task again by hand, and the reason of the first attempt is in the state of the
// task and in the journal of it (F-119, docs/DESIGN.md §6a, §7a).
func TestPRV001ARefusalOfTheModelProviderIsAWaitWithTheWorkKept(t *testing.T) {
	m, host, cfg := refusedProject(t, refusedProvider(), worked())

	result, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	if result.Outcome != ChangeRequestOpened {
		t.Fatalf("the run came out as %q, want %q: crewflow went on once by itself",
			result.Outcome, ChangeRequestOpened)
	}
	if result.Attempt != 2 {
		t.Errorf("the run is attempt %d, want 2: the continuation is the attempt after the refusal", result.Attempt)
	}
	state := stateOf(t, m, 43)
	if len(state.Attempts) != 2 {
		t.Fatalf("the state holds %d attempts, want 2: the refusal and the continuation", len(state.Attempts))
	}
	first, second := state.Attempts[0], state.Attempts[1]

	// The refusal is a wait with the name of the resource it waits for and the words of the
	// provider beside it — and not `executor-failed`, which is what it was on 02.10.
	if first.Outcome != Blocked {
		t.Errorf("the first attempt came out as %q, want %q", first.Outcome, Blocked)
	}
	if !strings.HasPrefix(first.Reason, ReasonProviderUnavailable+":") {
		t.Errorf("the reason of the first attempt = %q, want it to begin with %q",
			first.Reason, ReasonProviderUnavailable)
	}
	if !strings.Contains(first.Reason, "Cannot connect to API") {
		t.Errorf("the reason of the first attempt = %q, want the words of the provider in it", first.Reason)
	}
	if first.Provider != ProviderRetryable {
		t.Errorf("the state holds %q of the provider, want %q: the provider said it may be repeated",
			first.Provider, ProviderRetryable)
	}
	// The work of the task is where the run left it: the same worktree, the same branch
	// and the same session, and the point a continuation goes on from.
	if result.Worktree != state.Worktree || result.Branch != state.Branch {
		t.Errorf("the continuation worked in %q on %q, want %q on %q: the work of the task is there",
			result.Worktree, result.Branch, state.Worktree, state.Branch)
	}
	if second.AutoResumed != ReasonProviderUnavailable {
		t.Errorf("the continuation went on for %q, want it to be %q: what went on by itself is "+
			"what an orchestrator reads in the state of the task", second.AutoResumed, ReasonProviderUnavailable)
	}
	if second.Session != first.Session {
		t.Errorf("the continuation went on in the session %q, want the session of the refusal %q",
			second.Session, first.Session)
	}
	if runs := m.commandsOf("opencode"); len(runs) != 2 {
		t.Fatalf("the executor was started %d times, want 2: the refusal and the continuation", len(runs))
	} else if got := strings.Join(runs[1].args, " "); !strings.Contains(got, "--session "+theSession) {
		t.Errorf("the continuation was started with %q, want it to go on in the session %q", got, theSession)
	}
	point := state.Checkpoint
	if point == nil {
		t.Fatal("the state of the task holds no point, want the point the run stood at: " +
			"without it a person has to start the task again by hand")
	}
	if point.Step != stepModelProvider {
		t.Errorf("the point stands at the step %q, want %q", point.Step, stepModelProvider)
	}
	if point.Head != theHead {
		t.Errorf("the point holds the head %q, want %q: a continuation is checked against it", point.Head, theHead)
	}
	if point.Resource != SubjectModelProvider {
		t.Errorf("the point waits for %q, want %q", point.Resource, SubjectModelProvider)
	}
	if point.Outcome != WaitCompleted {
		t.Errorf("the point holds %q, want %q: the run that went on from it is over", point.Outcome, WaitCompleted)
	}
}

// TestPRV002TheEventsOfTheProviderAreInTheJournal is what a person reads about a run long
// after the provider answered or did not: the refusal with what it said and whether it may
// be repeated, the retry crewflow scheduled beside the line of the run that goes on, the
// answer of the provider to that run, and the fact that the attempt was crewflow's own
// decision and not a person's (F-119, docs/DESIGN.md §6a, §7a).
func TestPRV002TheEventsOfTheProviderAreInTheJournal(t *testing.T) {
	m, host, cfg := refusedProject(t, refusedProvider(), worked())

	if _, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo}); err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	journals := newJournals(m.home, "naghuale-crewflow")
	refused := read(t, journals.JournalPath(43, 1))
	for _, want := range []string{
		EventProviderUnavailable, "reason=" + ReasonProviderUnavailable, "repeatable=true",
		"resource=" + SubjectModelProvider, "Cannot connect to API", EventProviderRetryScheduled,
	} {
		if !strings.Contains(refused, want) {
			t.Errorf("the journal of the refusal holds\n%s\nwant it to hold %q", refused, want)
		}
	}
	gone := read(t, journals.JournalPath(43, 2))
	for _, want := range []string{EventRunResumed, "habit=" + ReasonProviderUnavailable, EventProviderAvailable} {
		if !strings.Contains(gone, want) {
			t.Errorf("the journal of the continuation holds\n%s\nwant it to hold %q", gone, want)
		}
	}
	// The provider refused the run again in the second attempt — and crewflow said nothing
	// of a retry, because the one promise of the policy is spent.
	if strings.Contains(gone, EventProviderRetryScheduled) {
		t.Errorf("the journal of the continuation holds a scheduled retry:\n%s\nwant none", gone)
	}
	// And the words of the executor are still there: nothing of the run overwrote what it
	// wrote.
	if !strings.Contains(refused, "Cannot connect to API") {
		t.Error("the journal of the refusal holds nothing of what the provider said")
	}
}

// TestPRV003TheSecondRefusalWaitsForAPersonAndIsNotRepeated: one try again is the whole of
// the promise of the policy. The second refusal is a wait with the reason of the resource
// it waits for — not a failure of the task and not a hang of the run — and nothing is tried
// a third time (F-119, AR-013, docs/DESIGN.md §6a, §7a).
func TestPRV003TheSecondRefusalWaitsForAPersonAndIsNotRepeated(t *testing.T) {
	m, host, cfg := refusedProject(t, refusedProvider(), refusedProvider())

	result, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	if result.Outcome != Blocked {
		t.Errorf("the run came out as %q, want %q: a refusal that repeated is a wait, not a failure", result.Outcome, Blocked)
	}
	if !strings.HasPrefix(result.Reason, ReasonProviderUnavailable+":") {
		t.Errorf("the reason of the run = %q, want it to begin with %q: the wait keeps the name of "+
			"the resource whatever came of the retries", result.Reason, ReasonProviderUnavailable)
	}
	if !strings.Contains(result.Reason, "already gone on by itself") {
		t.Errorf("the reason of the run = %q, want it to say that the one automatic repeat is spent", result.Reason)
	}
	if runs := m.commandsOf("opencode"); len(runs) != 2 {
		t.Errorf("the executor was started %d times, want 2: a third try is a blind restart", len(runs))
	}
	state := stateOf(t, m, 43)
	if len(state.Attempts) != 2 {
		t.Fatalf("the state holds %d attempts, want 2", len(state.Attempts))
	}
	last := state.Attempts[len(state.Attempts)-1]
	if last.AutoResumed != ReasonProviderUnavailable {
		t.Errorf("the last attempt went on for %q, want %q", last.AutoResumed, ReasonProviderUnavailable)
	}
	if last.Provider != ProviderRetryable {
		t.Errorf("the state holds %q of the provider, want %q", last.Provider, ProviderRetryable)
	}
}

// TestPRV004TheQueueOfAProviderWaitSaysFourThings is what a person finds in the queue of
// attention: which resource of the machine the task waits for, whether the provider said the
// refusal may be repeated, that the work of the task is kept, and the command that goes on
// from it (F-119, docs/DESIGN.md §6a, §7i).
func TestPRV004TheQueueOfAProviderWaitSaysFourThings(t *testing.T) {
	m, host, cfg := refusedProject(t, refusedProvider(), refusedProvider())

	if _, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo}); err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	entry := attentionOnly(t, m.home, "naghuale-crewflow", attentionEnvOf(m.at()))
	if entry.State != AttentionBlocked {
		t.Errorf("the state of the entry = %q, want %q: a refusal of the provider is a wait and not a hang",
			entry.State, AttentionBlocked)
	}
	if entry.Reason != ReasonProviderUnavailable {
		t.Errorf("the reason of the entry = %q, want %q", entry.Reason, ReasonProviderUnavailable)
	}
	if entry.Subject != SubjectModelProvider {
		t.Errorf("the entry waits for %q, want %q", entry.Subject, SubjectModelProvider)
	}
	if !strings.Contains(entry.Hint, "kept") {
		t.Errorf("the hint of the entry = %q, want it to say that the work of the task is kept", entry.Hint)
	}
	if !strings.Contains(entry.Hint, "may be repeated") {
		t.Errorf("the hint of the entry = %q, want it to name the repeatability of the refusal", entry.Hint)
	}
	if entry.Next != "crewflow task resume 43" {
		t.Errorf("the entry says to do %q, want %q: the continuation is from the point of the run",
			entry.Next, "crewflow task resume 43")
	}
	if entry.NextActor != ActorOrchestrator || entry.Actable != ActNow {
		t.Errorf("the entry waits for %q and says %q is actable, want the orchestrator and %q",
			entry.NextActor, entry.Actable, ActNow)
	}
	if entry.WaitingSeconds <= 0 {
		t.Error("the entry does not count the wait, want the time of the resource it waits for")
	}
}

// TestPRV005OnlyAnExplicitMarkOfTheProviderIsAWait walks the ladder of §6a: a provider that
// marked the refusal repeatable is a wait, every other failure of the provider is a decision
// of a person, and a run that failed for a reason of its own is a run that failed. Nothing
// of the shape of a failure is read here — the mark is the contract, and a failure nobody
// marked is not a temporary unavailability crewflow may act on (F-119, MODEL: UNKNOWN не
// успех, docs/DESIGN.md §6a, §7e).
func TestPRV005OnlyAnExplicitMarkOfTheProviderIsAWait(t *testing.T) {
	refused := func(name, message, mark string) string {
		return `{"type":"error","sessionID":"ses_7fKq2","part":{"type":"error","error":{"name":"AI_APICallError",` +
			`"data":{"message":"` + message + `"` + mark + `}}}}` + "\n"
	}
	cases := []struct {
		name      string
		stdout    string
		code      int
		want      Kind
		reason    string
		wantTries int
	}{
		{
			name:   "the provider refused it and said it may be repeated",
			stdout: refused("retryable", "Cannot connect to API (https://opencode.ai)", `,"isRetryable":true`),
			code:   1, want: Blocked, reason: ReasonProviderUnavailable, wantTries: 2,
		},
		{
			name:   "the provider refused it and said it may not be repeated",
			stdout: refused("plain", "Auth is not provided for this model", `,"isRetryable":false`),
			code:   1, want: Blocked, reason: ReasonProviderErrorUnknown, wantTries: 1,
		},
		{
			name:   "the provider refused it and said nothing about repeating it",
			stdout: refused("none", "insufficient credits", ""),
			code:   1, want: Blocked, reason: ReasonProviderErrorUnknown, wantTries: 1,
		},
		{
			name: "the executor failed for a reason of its own",
			// A run that died on a bug of its own and mentioned a provider in passing is not
			// a run the provider refused: the mark of the provider is the only mark.
			stdout: `{"type":"text","sessionID":"ses_7fKq2","part":{"type":"text",` +
				`"text":"go test says: cannot connect to the opencode.ai test fixture"}}` + "\n",
			code: 2, want: ExecutorFailed, wantTries: 1,
		},
		{
			name: "the agent stopped by itself",
			stdout: `{"type":"text","sessionID":"ses_7fKq2","part":{"type":"text",` +
				`"text":"BLOCKED: needs libtdjson"}}` + "\n",
			code: 0, want: Blocked, reason: "needs libtdjson", wantTries: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine(t)
			m.answers["opencode"] = answer{stdout: tc.stdout, code: tc.code}
			m.answers["git diff --name-only"] = answer{stdout: "internal/run/run.go\n"}
			host := &host{task: taskOf(43)}
			cfg := projectOf(t, m.worktrees, "")

			result, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
			if err != nil {
				t.Fatalf("Run returned an error: %v", err)
			}

			if result.Outcome != tc.want {
				t.Errorf("the outcome = %q, want %q (reason %q)", result.Outcome, tc.want, result.Reason)
			}
			if tc.reason != "" && !strings.HasPrefix(result.Reason, tc.reason) {
				t.Errorf("the reason = %q, want it to begin with %q", result.Reason, tc.reason)
			}
			if runs := m.commandsOf("opencode"); len(runs) != tc.wantTries {
				t.Errorf("the executor was started %d times, want %d: what the provider did not mark "+
					"repeatable is not tried again", len(runs), tc.wantTries)
			}
			state := stateOf(t, m, 43)
			last := state.Attempts[len(state.Attempts)-1]
			switch tc.reason {
			case ReasonProviderUnavailable:
				if last.Provider != ProviderRetryable {
					t.Errorf("the state holds %q of the provider, want %q", last.Provider, ProviderRetryable)
				}
			case ReasonProviderErrorUnknown:
				if last.Provider != ProviderUnknown {
					t.Errorf("the state holds %q of the provider, want %q", last.Provider, ProviderUnknown)
				}
			}
		})
	}
}

// TestPRV006WhatAPersonDecidesIsNeverRepeated is the closed list of F-119 written out: a
// refusal of the login, a lack of rights, a wrong request, a model that is not set up and
// money or a quota are all a decision of a person, and none of them is a temporary
// unavailability crewflow repeats on its own. Every one of them is `provider-error-unknown`
// — a failure of the provider crewflow cannot read as temporary — and the queue says that a
// person is the next one, not the orchestrator alone (F-119, docs/DESIGN.md §6a).
func TestPRV006WhatAPersonDecidesIsNeverRepeated(t *testing.T) {
	for _, said := range []string{
		"Auth is not provided for this model",
		"Permission denied to use this model",
		"Invalid request for the model",
		"Model some/model is not available",
		"You have exceeded your quota",
	} {
		t.Run(said, func(t *testing.T) {
			m := newMachine(t)
			m.answers["opencode"] = refusedProviderWithoutMark(said)
			host := &host{task: taskOf(43)}
			cfg := projectOf(t, m.worktrees, "")

			result, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
			if err != nil {
				t.Fatalf("Run returned an error: %v", err)
			}

			if result.Outcome != Blocked || !strings.HasPrefix(result.Reason, ReasonProviderErrorUnknown) {
				t.Errorf("the run came out as %q with the reason %q, want %q with %q: "+
					"nothing of this is a temporary unavailability",
					result.Outcome, result.Reason, Blocked, ReasonProviderErrorUnknown)
			}
			if runs := m.commandsOf("opencode"); len(runs) != 1 {
				t.Errorf("the executor was started %d times, want 1: a decision of a person is not "+
					"repeated by crewflow", len(runs))
			}
			entry := attentionOnly(t, m.home, "naghuale-crewflow", attentionEnvOf(m.at()))
			if entry.Reason != ReasonProviderErrorUnknown {
				t.Errorf("the reason of the entry = %q, want %q", entry.Reason, ReasonProviderErrorUnknown)
			}
			if entry.NextActor != ActorEither {
				t.Errorf("the entry waits for %q, want %q: money and a quota are the owner's, a wrong "+
					"request is the run's", entry.NextActor, ActorEither)
			}
			if entry.Next != "crewflow task resume 43" {
				t.Errorf("the entry says to do %q, want the continuation from the point of the run", entry.Next)
			}
		})
	}
}

// TestPRV007ThePointOfAProviderWaitIsAResume is what §7i is for, one more step of crewflow:
// a person comes back to a task whose provider was down, and goes on in the worktree, the
// branch and the session the run left — the point says where, and `crewflow task resume`
// checks that nothing of the three moved under it (F-119, docs/DESIGN.md §7i).
func TestPRV007ThePointOfAProviderWaitIsAResume(t *testing.T) {
	m, host, cfg := refusedProject(t, refusedProvider(), worked())
	// A project that asks for no retries by itself: the person comes back to the task
	// instead, which is what `crewflow task resume` is for.
	cfg.Executor.ProviderRetries = 0

	first, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
	if err != nil {
		t.Fatalf("the run that was refused returned an error: %v", err)
	}
	if first.Outcome != Blocked {
		t.Fatalf("the run came out as %q, want %q: nothing was to be repeated by itself",
			first.Outcome, Blocked)
	}

	// A person keeps going by hand: the provider was refused, the work of the task is
	// where it is, and the point says so.
	m.says("opencode", worked())
	result, err := Resume(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
	if err != nil {
		t.Fatalf("Resume returned an error: %v", err)
	}

	if !result.Resumed {
		t.Error("the report does not say that the run went on from the point of the task, want it said")
	}
	if result.Worktree != first.Worktree || result.Branch != first.Branch {
		t.Errorf("the continuation worked in %q on %q, want %q on %q: the work of the task is there",
			result.Worktree, result.Branch, first.Worktree, first.Branch)
	}
	if !result.Continued {
		t.Error("the attempt is not marked a continuation, want it marked: it goes on in a session")
	}
	state := stateOf(t, m, 43)
	if point := state.Checkpoint; point == nil || point.Outcome != WaitCompleted {
		t.Errorf("the state holds the point %+v, want it answered as %q: the provider answered and "+
			"there is nothing left to continue from that place", point, WaitCompleted)
	}
	// The journal of the continuation says what the run went on from, and that the
	// provider answered this time.
	said := read(t, newJournals(m.home, "naghuale-crewflow").JournalPath(43, 2))
	for _, want := range []string{EventRunResumed, EventProviderAvailable} {
		if !strings.Contains(said, want) {
			t.Errorf("the journal of the continuation holds\n%s\nwant it to hold %q", said, want)
		}
	}
	if strings.Contains(said, EventAuthorizationCompleted) {
		t.Errorf("the journal of the continuation holds %s:\n%s\nwant none: nobody was asked "+
			"anything about a model provider", EventAuthorizationCompleted, said)
	}
}

// TestPRV008ThePolicyOfAProjectStopsTheRetries: `[executor] provider_retries = 0` is a
// project that wants to decide every refusal of its provider itself, and a key written as 0
// is a decision and not a missing key (AR-013, docs/DESIGN.md §5, §6a).
func TestPRV008ThePolicyOfAProjectStopsTheRetries(t *testing.T) {
	m, host, cfg := refusedProject(t, refusedProvider(), refusedProvider())
	cfg.Executor.ProviderRetries = 0

	result, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	if result.Outcome != Blocked || !strings.HasPrefix(result.Reason, ReasonProviderUnavailable) {
		t.Errorf("the run came out as %q with the reason %q, want %q with %q",
			result.Outcome, result.Reason, Blocked, ReasonProviderUnavailable)
	}
	if runs := m.commandsOf("opencode"); len(runs) != 1 {
		t.Errorf("the executor was started %d times, want 1: the project named no retries", len(runs))
	}
	if len(stateOf(t, m, 43).Attempts) != 1 {
		t.Error("the state holds more than one attempt, want the one that was refused")
	}
}

// theSession is the session of a run of a test, as the agent of it named it: a
// continuation goes on in that session and nowhere else (docs/DESIGN.md §7a).
const theSession = "ses_7fKq2"
