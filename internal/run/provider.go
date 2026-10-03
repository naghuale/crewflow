package run

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/naghuale/crewflow/internal/run/profile"
	"github.com/naghuale/crewflow/internal/secret"
)

// A model provider that does not answer is a resource of the machine that a run waits
// for, and nothing else: the work of the task, the branch of it and the session of it are
// where the run left them, and a person who comes back in the morning has a task to
// continue rather than a defect to look at (F-119, journal #37, docs/DESIGN.md §6a, §7a).
//
// What makes it a wait and not a failure of the task is a mark of the provider itself and
// nothing else — the field `isRetryable: true` in what the run wrote, or a class of the
// failure the adapter of the agent reads unambiguously. A run that failed for a reason of
// its own and mentioned a provider in passing is not a run the provider refused, and a
// failure of the provider that carries no mark is not a temporary one: a refusal of the
// login, a lack of rights, a wrong request, a model that is not set up, money or a quota
// are all a decision of a person, and crewflow that reads them as a network blip repeats
// a task that cannot succeed and hides the thing a person has to decide (AR-013,
// docs/DESIGN.md §6a, §7e).
//
// How many times crewflow goes on by itself is the policy of the project —
// `[executor] provider_retries`, one by default — and how many times it may is a promise
// a person is owed: the work of the task is kept either way, and a task that waits for a
// provider that is down all night is a task a person reads once and not every minute.

// The two words the state of a task holds about the model provider of an attempt: the
// failure the provider refused the run for may be repeated, or nothing the provider said
// makes it a temporary one. It is a fact of the run kept beside the reason of it, so that
// the queue of attention can name the repeatability of a wait without reading a journal
// (docs/DESIGN.md §6a, §7h).
const (
	// ProviderRetryable is a refusal the provider itself marked repeatable.
	ProviderRetryable = "retryable"
	// ProviderUnknown is a failure of the provider that carries no mark of
	// repeatability: crewflow does not read it as a temporary unavailability and does not
	// repeat it.
	ProviderUnknown = "unknown"
)

// The events of a wait for the model provider, in the names the journal of a task reads
// them by (docs/DESIGN.md §6a, §7a). A run is read long after the provider answered or did
// not, and the journal is the whole of what a person has then.
const (
	// EventProviderUnavailable is the provider having refused the run.
	EventProviderUnavailable = "PROVIDER_UNAVAILABLE"
	// EventProviderRetryScheduled is crewflow having said that it will go on by itself
	// once, in the same session, with the work where it is.
	EventProviderRetryScheduled = "PROVIDER_RETRY_SCHEDULED"
	// EventProviderAvailable is the provider having answered the run that went on from the
	// refusal: the first answer, and not a promise about the next one.
	EventProviderAvailable = "PROVIDER_AVAILABLE"
	// EventRunResumed is an attempt that crewflow went on with by itself, whatever it went
	// on from: a refusal of the provider or a run that stood.
	EventRunResumed = "RUN_RESUMED"
)

// refusal is what crewflow makes of what a run wrote about the model provider: the reason
// of §6a the run is to be read with, the word the state of the task holds about it, and
// whether there was a failure of the provider at all. The zero of it with `false` is a run
// the provider refused nothing, which is every run of an agent whose failures crewflow
// cannot read (docs/DESIGN.md §7e).
type refusal struct {
	reason string
	marked string
	said   string
}

// refuseProvider is the ladder of the failure of a model provider: a mark of repeatability
// is a wait for a resource, and no mark is a decision of a person. There is no third
// answer here on purpose — «неизвестно» is not «ещё идёт», and a run that is unknown to
// have been refused by anybody is a run that failed (MODEL: UNKNOWN не успех,
// docs/DESIGN.md §6a).
func refuseProvider(failure profile.Failure) (refusal, bool) {
	switch {
	case !failure.Of:
		return refusal{}, false
	case failure.Retryable:
		return refusal{ReasonProviderUnavailable, ProviderRetryable, failure.Said}, true
	default:
		return refusal{ReasonProviderErrorUnknown, ProviderUnknown, failure.Said}, true
	}
}

// The line a run of a task waits with and the words the next attempt of it is given, for a
// provider that refused the run and said the refusal may be repeated (docs/DESIGN.md §7a,
// §7a.1). The texts are in English whatever the language of the project, because the tools
// crewflow runs read English and the assignment of a run is in English for the same reason.
const (
	providerHeadline = "the model provider refused the run and said the refusal may be repeated"
	providerTells    = "The run was refused by the model provider of this project, and the provider said " +
		"the refusal may be repeated: %s\n" +
		"Nothing of the work is lost: the work of the task is in this worktree and in this session, where the " +
		"run left it. Do not start the task over and do not repeat what the run already did.\n" +
		"The provider has answered no more since — go on with the work of the task and open the change request " +
		"of its branch. crewflow goes on by itself once and no more: if the provider refuses the run again, the " +
		"task waits for a person and says why in the queue of attention."
)

// providerResume is the run that goes on by itself after the model provider refused the
// previous attempt and said it may be repeated.
func providerResume(said string) resume {
	text := providerTells
	if said != "" {
		text = fmt.Sprintf(providerTells, said)
	}
	return resume{habit: habit{reason: reason(ReasonProviderUnavailable), headline: providerHeadline, text: text}}
}

// afterProvider is what a run goes on with after the model provider refused it, and what
// the state of the task is told about that refusal. Three things keep a run from going on
// by itself here, and the first of them is not one of them:
//
//   - a failure the provider did not mark repeatable is not repeated at all: a refusal of
//     the login, a lack of rights, a wrong request, a model that is not set up and money or
//     a quota are decisions of a person, and crewflow does not take them for a network blip
//     (F-119, §6a, §7e);
//   - the policy of the project says how many tries a provider is given at all
//     (`[executor] provider_retries`, one by default), and a task whose provider was
//     already tried that many times waits;
//
// The refusal that comes after the automatic repeat keeps the reason of the wait —
// `provider-unavailable` — and says in it that the repeat is spent, so that the queue says
// «ждёт» and not «провал» and a person sees a wait for a resource rather than a defect. An
// executor that was told the provider refused it and got the same refusal is not to be told a
// second time, and what is to be looked into is the provider, not a run that has been told
// (F-119, §7a.1, §7j).
func (r *runner) afterProvider(result *Result, state State) (resume, string, bool) {
	said := named(result.Reason)
	if result.Outcome != Blocked ||
		(said != ReasonProviderUnavailable && said != ReasonProviderErrorUnknown) {
		return resume{}, "", false
	}
	// The words of the provider are the part of the reason after the name of it: the name
	// is the code of §6a, and the words are what a person reads (docs/DESIGN.md §7i).
	_, words, _ := strings.Cut(result.Reason, ":")
	words = strings.TrimSpace(words)
	if said == ReasonProviderErrorUnknown {
		// Nothing the provider said makes this failure temporary, so nothing is repeated:
		// a refusal of the login, a lack of rights, a wrong request, a model that is not
		// set up and money or a quota are all a decision of a person, and crewflow that
		// tried them again would be trying a task that cannot succeed (F-119, §6a).
		return resume{}, ProviderUnknown, true
	}
	if r.providerTriesSpent(state) >= r.cfg.Executor.ProviderRetries {
		result.Reason = fmt.Sprintf("%s: crewflow has already gone on by itself once, and the provider "+
			"refused the run again: %s", ReasonProviderUnavailable, words)
		return resume{}, ProviderRetryable, true
	}
	return providerResume(words), ProviderRetryable, true
}

// providerTriesSpent is how many times crewflow has already gone on by itself after the
// provider refused this task. It is counted over the whole state of the task and not over
// the one run of it, because the promise of the policy is about the task: a person who
// goes on by hand and meets the same refusal must not find a run that repeats itself
// behind their back (AR-013, docs/DESIGN.md §6a, §7a).
func (r *runner) providerTriesSpent(state State) int {
	spent := 0
	for _, attempt := range state.Attempts {
		if attempt.AutoResumed == ReasonProviderUnavailable {
			spent++
		}
	}
	return spent
}

// providerPoint is the point a run writes when it stops for the model provider: where the
// work of the task is, what the branch of it stands at and what the task was — the same
// point §7i writes when a run stands at the window of the keychain, with the resource the
// run waits for in place of the channel and the action of a person. Nobody is asked
// anything here: the provider is not a window, and a queue that asked a person to click
// something that is not there is a queue that cries wolf (docs/DESIGN.md §6a, §7i).
//
// A point crewflow cannot check is no point at all, and the run says why in the way out of
// it: a continuation from a point crewflow cannot check would be a guess about the work.
func (r *runner) providerPoint(ctx context.Context, worktree string) (*Checkpoint, error) {
	head, err := r.head(ctx, worktree)
	if err != nil {
		return nil, err
	}
	return &Checkpoint{
		Task:       r.task.Number,
		Step:       stepModelProvider,
		Head:       head,
		Assignment: fingerprint(r.task),
		At:         r.env.Now(),
		Resource:   SubjectModelProvider,
	}, nil
}

// saidEvent writes one event of a run into the journal of its attempt: the name first,
// because that is what a program and a person look for, and then the facts it happened
// with (docs/DESIGN.md §6a, §7a).
func sayEvent(out io.Writer, event string, facts ...string) {
	if out == nil {
		return
	}
	said := strings.Join(keptFacts(facts), " ")
	if said != "" {
		said = " " + said
	}
	fmt.Fprintf(out, "crewflow: event %s%s\n", event, said)
}

// keptFacts are the facts of an event that the run has, in the order it said them: a fact
// crewflow has nothing to say about is left out rather than written as an empty name.
func keptFacts(facts []string) []string {
	var kept []string
	for _, fact := range facts {
		if fact != "" {
			kept = append(kept, fact)
		}
	}
	return kept
}

// failureOf is the words of the failure of the provider as a line of a journal holds them: one
// line, whatever the provider wrote, because a journal is read as it grows and a failure
// spread over three lines is a failure nobody reads whole (docs/DESIGN.md §7a).
//
// The words of the provider are the words a program of the run printed, and they go
// through the secrets of the run like every other line of it: an agent that prints its own
// environment on the way out to a refusal of its provider puts the password of the route in
// there with it, and this line is written to the journal of the attempt and not through the
// redactor of the output of the executor (docs/DESIGN.md §7e, §7i).
func (r *runner) failureOf(failure profile.Failure) string {
	words := strings.Join(strings.Fields(secret.Redact(failure.Said, r.secrets()...)), " ")
	if words == "" {
		return ""
	}
	return fmt.Sprintf("said=%q", words)
}

// saidAtEvent is when an event happened, in the words of the other events of a run.
func saidAtEvent(when time.Time) string {
	return "at=" + when.Format(time.RFC3339)
}

// pointedAtProvider is the point of a run that stopped for the model provider, kept on the
// run itself and written into the state of the task with everything else the run ends
// with, and what the journal of the attempt holds of it. A point crewflow cannot write is
// not a point at all — a continuation from a guess about the work is worse than no
// continuation — and the run says why in the way out of it and goes on to hold the work
// where it is (§7i).
func (r *runner) pointedAtProvider(ctx context.Context, worktree string, files *AttemptFiles, refused refusal) {
	point, err := r.providerPoint(ctx, worktree)
	if err != nil {
		r.note(files.ErrorJournal, fmt.Errorf("the point of task %d: %w", r.task.Number, err))
		return
	}
	r.point = point
}

// saysWhatCameOfProvider is what the journal of the attempt holds about the model provider
// of the run, and it is said here, where crewflow has just learnt it, because a person
// reads the journal of a run long after the provider answered or did not:
//
//   - the provider refused the run — `PROVIDER_UNAVAILABLE`, with what it said and whether
//     it said the refusal may be repeated;
//   - the provider answered the run that went on from that refusal — `PROVIDER_AVAILABLE`,
//     and only where the run wrote something of its own and was refused no more: this is
//     the answer of the first call and not a promise about the next one.
//
// The retry crewflow scheduled is said where the run that goes on is written down
// (`PROVIDER_RETRY_SCHEDULED`, beside the line of the run), so that the two facts a reader
// of the journal needs — what happened and what crewflow did about it — stand next to one
// another (§6a, §7a).
func (r *runner) saysWhatCameOfProvider(files *AttemptFiles, attempt int, failure profile.Failure, wrote bool) {
	refused, ok := refuseProvider(failure)
	switch {
	case ok:
		sayEvent(files.Out, EventProviderUnavailable,
			"reason="+refused.reason, "resource="+SubjectModelProvider, "attempt="+strconv.Itoa(attempt),
			"repeatable="+strconv.FormatBool(failure.Retryable), r.failureOf(failure), saidAtEvent(r.env.Now()))
	case r.wentOnFromProvider() && wrote:
		sayEvent(files.Out, EventProviderAvailable,
			"resource="+SubjectModelProvider, "attempt="+strconv.Itoa(attempt), saidAtEvent(r.env.Now()))
		r.spentPoint()
	}
}

// wentOnFromProvider is whether this attempt is a run that goes on from a refusal of the
// model provider: crewflow went on by itself, or a person asked to continue from the point
// the refusal wrote. Both are the run whose coming out says whether the provider answered
// (§7a, §7i).
func (r *runner) wentOnFromProvider() bool {
	return r.auto == ReasonProviderUnavailable ||
		(r.point != nil && r.point.Step == stepModelProvider)
}

// spentPoint is the point of the provider marked as spent: the run went on from that
// place, the provider answered, and a continuation from there is not offered any more — the
// same thing §7i does with a point whose request a person answered, and for the same
// reason: a point that stands where nothing stands is a question nobody asked. It is kept
// on the run and written into the state of the task with everything else the run ends
// with.
func (r *runner) spentPoint() {
	if !r.wentOnFromProvider() {
		return
	}
	point := *r.point
	point.Outcome, point.DecidedAt = WaitCompleted, r.env.Now()
	r.point = &point
}
