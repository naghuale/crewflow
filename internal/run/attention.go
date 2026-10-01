package run

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The attention queue is the answer to the one question an orchestrator and an owner ask
// first: what of the work of this project needs me right now. It is worked out every time
// it is read — out of the state of the runs of the project and, where the caller may ask,
// out of what the host of the project says about their changes — and it is never stored: a
// queue kept beside the state says what it said when it was written and not what is true
// now (docs/DESIGN.md §6, §7h).
//
// It is there for three failures of the practice journal (#37), all of them one failure
// seen from the outside: a run that ended with a change request and that nobody noticed
// for an hour and a half while everybody saw "work is going on" (F-061), a run that
// stands with a live process and no work in it (F-039, F-041, §7a), and a task that waits
// for a decision of a person that nothing shows anywhere (F-065).
//
// A run that goes quiet in front of a window of the system of the machine — the keychain
// of macOS above all — is *waiting* and not *standing*, whatever the length of the wait
// (AQ-001, AQ-008, ES-006): the reason of a wait with a known reason is something a person
// goes and answers, and a hang is a run that nobody can answer at all.

// AttentionState is what the process of a task is doing, in the one word a person reads
// first. The states are mutually exclusive (AQ-006): a run waits or it stands, and it is
// never both — a run that is `stands` is a run nobody can name the reason of.
type AttentionState string

// The states of the queue. Every one of them except `escalated` is a state of the process
// itself; `escalated` is what such a state becomes when it has lasted longer than the
// project agreed to wait, and it keeps the state it came from beside it (docs/DESIGN.md §6).
const (
	// AttentionStands means the run is going and has shown nothing for longer than the
	// silence of the project, and crewflow does not know what it waits for (AQ-002). It
	// is the one state that always demands attention: nothing is known to be broken and
	// nothing is known to be going on, and a run in that state is the "zombie RUNNING"
	// of §7a.
	AttentionStands AttentionState = "stands"
	// AttentionEscalated means that one of the states below has been waiting for longer
	// than `[attention] escalate_after` and has been left as a record under the task
	// once. It names no cause of its own: the state it came from and the reason of the
	// wait stay beside it, because an escalation that says only "escalated" is a task
	// nobody knows what to do about (AQ-008).
	AttentionEscalated AttentionState = "escalated"
	// AttentionFinishedUnseen means the run ended and nobody has reacted to it: no
	// record of a review, no continuation and no merge. It is the class of attention of
	// §6a — the work is over and waits for a reaction — and only the reaction itself
	// clears it.
	AttentionFinishedUnseen AttentionState = "finished-unseen"
	// AttentionAwaitsReview means the change of the run is open and no record of a
	// review of it is under it: not one at all, or none of the head it stands at now
	// (F-065, §7h). The next one is the orchestrator.
	AttentionAwaitsReview AttentionState = "awaits-review"
	// AttentionAwaitsOwner means the result of the task is with the owner: a change of a
	// task marked `[acceptance] label` is approved and the owner has not taken the
	// result in, or a task of a category R waits for his decision (ES-001, ES-002, ES-003,
	// §7f, §7h).
	AttentionAwaitsOwner AttentionState = "awaits-owner"
	// AttentionAwaitsResource means the run waits for a resource of the project: the
	// minutes of CI, a window of a secret store, a slot of the scheduler. What the run
	// waits for is said only where the source of it says: an entry with no subject says
	// `unknown` rather than guessing, and the model of resources is a task of its own
	// (#78).
	AttentionAwaitsResource AttentionState = "awaits-resource"
	// AttentionBlocked means the run was refused something: a permission, a secret, or a
	// decision of a person that only a person may give (§7a.1, §7e, ES-006).
	AttentionBlocked AttentionState = "blocked"
	// AttentionStopped means a person stopped the run: the work of the task is not over
	// and nobody is looking at it, and crewflow neither goes on by itself nor hides the
	// fact (§7a).
	AttentionStopped AttentionState = "stopped"
)

// Priority is how loudly an entry of the queue asks for a person. It is worked out of the
// state of the task and of how long that state has lasted, and it is what a schedule of an
// orchestrator sorts by when it decides whom to wake first (docs/DESIGN.md §6a).
type Priority string

// The three priorities of the queue: a critical entry is one that has been waiting for
// longer than the project agreed to, a high one is a state a person has to answer today,
// and a normal one is a fact about a task that nobody is in a hurry for.
const (
	Critical Priority = "critical"
	High     Priority = "high"
	Normal   Priority = "normal"
)

// Actable is the answer to the second question a person asks of a queue: may anything be
// done about this right now. It is what puts the entries that demand a person on top and
// the ones that objectively wait for a window of a resource below them, so that the top of
// the queue is where the work is and not where the waiting is (docs/DESIGN.md §6a).
type Actable string

// The four answers of the queue to "may I act now".
const (
	// ActNow means a person may act right now and the queue says what to do.
	ActNow Actable = "now"
	// ActWatch means nothing is asked of a person: the fact is reported and the queue
	// watches it.
	ActWatch Actable = "watch"
	// ActWait means the run waits for a resource of the project with a window that is
	// known, and nobody has to do anything until that window comes back (#78).
	ActWait Actable = "wait"
	// ActUnknown means crewflow cannot say whether a person may act: it names what it
	// does not know and guesses nothing (§7e).
	ActUnknown Actable = "unknown"
)

// The reasons of the queue: why a task is in it and what it waits for. A reason is a name
// and not a sentence, because a block of a list, a record under a task and a schedule of
// an orchestrator are read by a person and by a program, and both of them look for the
// name first (docs/DESIGN.md §6, §7i).
const (
	// ReasonRunCompleted is a run that ended with its change request open and that nobody
	// has looked at (docs/DESIGN.md §6a).
	ReasonRunCompleted = "run-completed"
	// ReasonRunEnded is a run that ended and left no change request at all: the work of
	// the task is nowhere, and someone has to decide what to do with the task.
	ReasonRunEnded = "run-ended"
	// ReasonRunTimeout is a run that took longer than any run of the project may take and
	// was stopped: a hang is an outcome and not a wait (§7a).
	ReasonRunTimeout = "run-timeout"
	// ReasonRunFailed is a run whose executor is gone or could not be started (§7a).
	ReasonRunFailed = "run-failed"
	// ReasonOutOfScope is a run that changed files the task was not to change, which is
	// found out after the work and not before it (§7c).
	ReasonOutOfScope = "out-of-scope"
	// ReasonNoProgress is a run that stands: the reason of the silence is unknown, and
	// that is what makes it a thing to look into (AQ-002).
	ReasonNoProgress = "no-progress"
	// ReasonHumanAuthorization is a run that waits for a decision of a person that cannot
	// be got out of facts: a window of the keychain of macOS, an authorization of the
	// host, a secret store. One reason for every channel, and the channel is the
	// diagnosis and not a state of its own (HA-001, HA-002, §7f).
	ReasonHumanAuthorization = "human-authorization-required"
	// ReasonReviewRequired is a change that waits for a record of a review (§7h).
	ReasonReviewRequired = "review-required"
	// ReasonOwnerAcceptance is a change of a task marked `[acceptance] label` that waits
	// for the owner to take the result in (§7h).
	ReasonOwnerAcceptance = "owner-acceptance-required"
	// ReasonResourceWait is a run that waits for a resource of the project, which the
	// model of resources will name (#78).
	ReasonResourceWait = "resource-wait"
	// ReasonBlockedPermission is a run that was refused a permission (§7a, §7d).
	ReasonBlockedPermission = "blocked-permission"
	// ReasonBlockedSecret is a run that reached for a place of the machine that stays
	// closed whatever the project wrote (§7a.1, §7e, §8).
	ReasonBlockedSecret = "blocked-secret"
	// ReasonBlocked is a run that stopped by itself and said why (§7b, §7d).
	ReasonBlocked = "blocked"
	// ReasonStoppedByHand is a run a person stopped with SIGINT or SIGTERM (§7a).
	ReasonStoppedByHand = "stopped-by-person"
	// ReasonTaskMissing is a run of a task the host of the project does not have: the
	// repository moved, or another one took its name, and the state of the run outlived
	// the task it is of. It is a fact about the project and not an expectation — nobody is
	// waited for, nothing is escalated and nothing is written under a task that is not
	// there — and the journal of the run stays where it was (§6a, §7g).
	ReasonTaskMissing = "task-missing"
)

// The channel of `human-authorization-required` in the window of the keychain of macOS,
// and the action a person has to take in it: the two are the diagnosis of the request and
// neither of them is a state (HA-002, §7f, §7i).
const (
	// ChannelKeychain is the window that asks whether this build of crewflow may read the
	// key of the App of the host.
	ChannelKeychain = "macos-keychain"
	// ActionHumanExecute is a person doing it himself: pressing the button in the window
	// of the system, entering a password of an administrator, trusting a certificate.
	ActionHumanExecute = "human-execute"
)

// What the queue waits for, as a person reads it: the subjects of a wait and the actors
// that answer it. They are the things of a project and not the states of a run, and a
// person reads them to know where to go next (docs/DESIGN.md §6a).
const (
	// SubjectExecutorKey is the key of the App of the executor in the keychain of the
	// machine: the thing a run of the mode of the bot stands in front of (§7i).
	SubjectExecutorKey = "executor-key"
	// SubjectReviewer is the reviewer a change waits for, which the file of the project
	// names in `[merge] reviewers` (§7h).
	SubjectReviewer = "the reviewer"
	// SubjectOwner is a decision of the owner, of a category R (§7f, §7k).
	SubjectOwner = "the decision of the owner"
	// SubjectResult is the result of a run that opened no change request.
	SubjectResult = "the result of the run"
)

// The actors the queue waits for: the orchestrator, the owner, either of them, and nobody
// at all where a run waits for a resource of the project and no person is asked for
// anything.
const (
	ActorOrchestrator = "orchestrator"
	ActorOwner        = "owner"
	ActorEither       = "owner or orchestrator"
	ActorNobody       = "nobody — we are waiting"
)

// Attention is one task of the queue: the state of its process, the reason of it, what it
// waits for, who acts next, how long it has been that way, and what may be done about it.
// Everything in it is a fact of the moment the queue was read (docs/DESIGN.md §6a).
type Attention struct {
	// Repo, Task, Title and Run are what the run is of, the way a person names a run.
	Repo  string `json:"repo"`
	Task  int    `json:"task"`
	Title string `json:"title"`
	Run   string `json:"run"`
	// State is what the process of the task is doing, one word of the eight above.
	State AttentionState `json:"attention_state"`
	// EscalatedFrom is the state the task was in before the waiting grew past
	// `[attention] escalate_after`, and is nothing for an entry that is not escalated.
	EscalatedFrom AttentionState `json:"escalated_from,omitempty"`
	// Reason is why the task is in the queue, one of the names above. It names the cause
	// and not the length of the wait, which is in Since and in WaitingSeconds.
	Reason string `json:"reason,omitempty"`
	// Subject is what the task waits for: a resource of the machine, a decision of a
	// person, a record of a review. It is nothing where crewflow cannot say, and the
	// entry says `unknown` then rather than guessing (AQ-004, §7e).
	Subject string `json:"subject,omitempty"`
	// Channel, Resource and Action are the diagnosis of a request of a person: where it
	// is asked for, which resource of the project it is about, and what the person is to
	// do. They are nothing for every other reason, and the platform is only ever a channel
	// and never a state of its own (HA-002, §7f).
	Channel  string `json:"channel,omitempty"`
	Resource string `json:"resource,omitempty"`
	Action   string `json:"action,omitempty"`
	// NextActor is who the queue waits for.
	NextActor string `json:"next_actor"`
	// Priority is how loudly the entry asks, and Actable is whether anything may be done
	// about it right now: the queue is read by the first and sorted by the second.
	Priority Priority `json:"priority"`
	Actable  Actable  `json:"actable"`
	// Since is the moment the state began — the end of the run, the last sign of life of
	// a run that stands, the record of the review the owner is waiting behind — and
	// WaitingSeconds is how long it has been that way, in seconds because a program
	// counts seconds and a person reads a time.
	Since          time.Time `json:"waiting_since"`
	WaitingSeconds float64   `json:"waiting_seconds"`
	// LastStep is what the run was doing when it last showed a sign of life. It is what a
	// person needs for `stands`, where nothing else is known, and it is nothing for every
	// other state (docs/DESIGN.md §6a, §7a).
	LastStep string `json:"last_step,omitempty"`
	// Next is what may be done about it: a command to run, or the words of the decision
	// that is missing. It is nothing where crewflow has nothing to name, and a guessed
	// command is worse than no command (§7e).
	Next string `json:"next,omitempty"`
	// Hint is what a person has to be told where the queue has no action to offer: a task
	// of a repository that moved, and where the journal of the run stayed (§6a). It is a
	// hint and not a `Next`, because there is nothing to run — the same word doctor uses
	// for what to do about a check (§7d).
	Hint string `json:"hint,omitempty"`
	// Outcome is how the last attempt of the task came out, and Change the change request
	// a run of it opened, as a list of runs says it (docs/DESIGN.md §6).
	Outcome Kind    `json:"outcome"`
	Change  *Change `json:"change,omitempty"`
	// LongWaiting says that the task has been waiting for longer than
	// `[attention] weekly_after`, and it is what the weekly slice of the practice journal
	// counts as a long wait (#37).
	LongWaiting bool `json:"long_waiting,omitempty"`
	// Promoted says that the entry goes to the top of a list of runs: either somebody may
	// act on it right now, or it has waited for longer than `[attention] top_after` and a
	// person has to know of it (docs/DESIGN.md §6a).
	Promoted bool `json:"promoted"`
	// Said says that this turn of the watch left a record of the entry under the task on
	// the host, and Unsaid is what stood in the way where it left nothing. They are about
	// the record and not about the task, and both are said rather than hidden (§7g, §6a).
	Said   bool   `json:"said,omitempty"`
	Unsaid string `json:"unsaid,omitempty"`
	// Problem is what crewflow could not learn about the entry — a host that could not
	// be read. A queue that says "nobody is waiting" because the host was out of reach
	// would be lying about the work of the project, and the problem is said in the entry
	// instead of being passed over (§7h, §6a).
	Problem string `json:"problem,omitempty"`
}

// Waited is how long the entry has been in its state, as a length of time rather than as
// the seconds a program counts: a person reads a time.
func (a Attention) Waited() time.Duration {
	return time.Duration(a.WaitingSeconds * float64(time.Second))
}

// Key is what a record under a task is written once for: the task, the state, the priority
// and the reason. A change of the state, of the reason or of the priority is a new key and
// a new record whatever was written before it, and the same key is not written twice a day
// (docs/DESIGN.md §6a).
func (a Attention) Key() string {
	return fmt.Sprintf("%d/%s/%s/%s", a.Task, a.State, a.Priority, a.Reason)
}

// AttentionEnv is what the queue needs from the machine and from the file of the project:
// the clock, the question whether a run is still going, and the thresholds at which a task
// comes to the top of the queue, is escalated, is reminded of, and goes into the weekly
// slice of the practice journal (docs/DESIGN.md §6a).
type AttentionEnv struct {
	ListEnv
	// TopAfter is how long a task may wait before it goes to the top of the queue even
	// where nobody can act on it yet: `[attention] top_after`, half an hour by default.
	TopAfter time.Duration
	// EscalateAfter is how long a task may wait before the waiting is escalated and a
	// record of it is left under the task: `[attention] escalate_after`, a day.
	EscalateAfter time.Duration
	// RemindAfter is how long the same key waits before the same record may be left under
	// the task once more: `[attention] remind_after`, a day. It is what keeps a schedule
	// that runs every minute from writing every minute (§6).
	RemindAfter time.Duration
	// WeeklyAfter is how long a task may wait before the wait is called a long one and
	// goes into the weekly slice: `[attention] weekly_after`, a week (#37).
	WeeklyAfter time.Duration
	// AcceptanceLabel is the word a task is marked with to have its result accepted by
	// the owner before the change may go in: `[acceptance] label` (§7h).
	AcceptanceLabel string
}

// Host is what the queue may ask the host of a project about a task: the words it is
// marked with, and the facts of the change request of the run of it. It is nil where
// nothing may be asked — `crewflow task list` reads the state of the tasks and nothing
// else (§6) — and the queue then says what the state of a task is enough for and calls the
// rest of it unknown, because a queue that guesses is a queue a person cannot act on.
type Host interface {
	// FactsOf returns what the host of the project knows about the task and about the
	// change request of the run of it, which is the one with that number — and nothing
	// about the change where the number is zero, because a run that opened none has no
	// change to ask about.
	FactsOf(ctx context.Context, task, change int) (HostFacts, error)
}

// SayAttention is what leaves the notice of an entry of the queue under its task on the
// host of the project, and what a project of no host answers. It is the callback of §6a the
// way `Say` is the callback of §6: a project whose host cannot write under a task is
// reported to the person who asked and written about nowhere, and the answer says so
// rather than letting a schedule believe that a notice is there (§6, §7g).
type SayAttention func(ctx context.Context, one Attention) (said bool, problem string)

// HostFacts is what the host of a project is known to say about a task: where the task
// stands, the words it is marked with, and the change request of the run of it with what
// is written under it. A host nobody asked is the zero of this type, and every field of it
// says so.
type HostFacts struct {
	// Asked says that the host answered. Where it did not, everything below is nothing and
	// the queue concludes nothing out of their absence.
	Asked bool `json:"-"`
	// Closed says that the task is closed on the tracker, which is a fact of the work and
	// not a verdict about it: a task the host closed is a task that is done, whoever closed
	// it and whatever stood in the way of its run (§7g).
	Closed bool `json:"closed,omitempty"`
	// Missing says that the host of the project does not have the task at all, and is not
	// a failure of the host: a repository that moved and another one that took its name
	// answer exactly that, and the run journal of the task is still where it was (§7g).
	// It is not `Problem`: nothing failed, and the queue has an answer.
	Missing bool `json:"missing,omitempty"`
	// Labels are the words the host marks the task with, the label of the acceptance of
	// the owner among them ([acceptance] label, §7f).
	Labels []string `json:"labels,omitempty"`
	// Change is what the host says about the change request of the run of the task, and is
	// nothing where the run opened none or the host was not asked.
	Change *ChangeFacts `json:"change,omitempty"`
	// Problem is why the host could not be read, where it could not: a queue that says
	// "nobody is waiting" because the host was out of reach would be lying about the work
	// of the project, and the problem is said instead (§7h).
	Problem string `json:"problem,omitempty"`
}

// ChangeFacts is what the host of a project says about a change request: whether it is
// still open, what is at its head, and what is written under it. It is that part of the
// facts of §7h which the queue asks about, and nothing else: the queue does not judge a
// change, it says who is waited for (§7h).
type ChangeFacts struct {
	// Number and State are what the host calls the change and where it stands: "open",
	// "merged" or "closed".
	Number int    `json:"number"`
	State  string `json:"state"`
	// Head is the commit at the head of the change.
	Head string `json:"head"`
	// Approved says that a record of a review of the current head of the change is under
	// it, written by an account that reviews it — not by the executor of a run, and not a
	// record that was edited afterwards (§7h, §7i).
	Approved bool `json:"approved"`
	// Accepted says that the owner took the result of the task in at the current head of
	// the change, with `ACCEPTED <sha>` (§7h).
	Accepted bool `json:"accepted"`
	// Last is when the last record of a review or of an acceptance was written, and is
	// what the waiting of the owner is counted from.
	Last time.Time `json:"last,omitempty"`
}

// open is whether the change is still open, as the host of it says: a change that is merged
// or closed is not waited for by anybody.
func (c *ChangeFacts) open() bool {
	return c != nil && c.State == "open"
}

// marked is whether the host marks the task with the given word, compared without regard
// to the case of the letters, because a host keeps a label in whatever case a person typed
// it (§7f).
func (f HostFacts) marked(label string) bool {
	if label == "" {
		return false
	}
	return slices.ContainsFunc(f.Labels, func(one string) bool {
		return strings.EqualFold(strings.TrimSpace(one), label)
	})
}

// Queue is the attention of one project: the tasks of it that want a person, in the order a
// person reads them in, and the state files of the tasks that say nothing about a run.
type Queue struct {
	// Repo is the project the queue is of, as a person writes it.
	Repo string `json:"repo,omitempty"`
	// Entries are the tasks that want a person, the most urgent first.
	Entries []Attention `json:"entries"`
	// Unreadable are the state files of the tasks crewflow could not read, which is what a
	// list of runs already says about them: a task crewflow cannot read is a task nobody
	// can watch, and the queue says so rather than leaving a hole in it.
	Unreadable []string `json:"unreadable,omitempty"`
}

// Wanted is the entry of the queue of a task, and whether the task is in it at all. A list
// of runs puts the attention of its entries on them, and this is how an entry finds the
// task it is about.
func (q Queue) Wanted(task int) (Attention, bool) {
	for _, one := range q.Entries {
		if one.Task == task {
			return one, true
		}
	}
	return Attention{}, false
}

// Notes is what the queue could not say about the tasks of the project: the state files
// crewflow could not read, one per line. It goes to the standard error of a command that
// answers in JSON, because the answer of a command is the thing an orchestrator reads, and
// a state file it could not read is not an answer (§6a).
func (q Queue) Notes(w io.Writer) error {
	for _, path := range q.Unreadable {
		if _, err := fmt.Fprintf(w, "not read: %s\n", path); err != nil {
			return fmt.Errorf("write the notes of the queue: %w", err)
		}
	}
	return nil
}

// AttentionQueue is the attention of the project at this moment: the tasks of it that want
// a person, worked out of the state of their runs and, where the caller may ask the host
// about them, out of the change requests and the labels the tracker holds. It reads the
// state of the tasks and nothing else where the host is nil, and it creates nothing
// anywhere: a queue is a question, and a question is not a run (§6).
func AttentionQueue(ctx context.Context, home, repo string, env AttentionEnv, host Host) (Queue, error) {
	return attentionOf(ctx, home, repo, env, host, nil)
}

// CheckAttention is the queue of attention of the project, and the record it leaves under
// the tasks of it. A record is left once for the key of an entry — the task, the state, the
// priority and the reason — and not again for a day while the key is the same, so that a
// schedule of an orchestrator that runs every minute leaves one line under a task and not
// one a minute, and so that a change of the reason of a wait leaves a new line whatever was
// written before it (§6a).
func CheckAttention(ctx context.Context, home, repo string, env AttentionEnv, host Host, say SayAttention) (Queue, error) {
	return attentionOf(ctx, home, repo, env, host, say)
}

// attentionOf is the queue of the project and the records under the tasks of it, and the
// two are one function because a record is due exactly where an entry of the queue is.
func attentionOf(ctx context.Context, home, repo string, env AttentionEnv, host Host, say SayAttention) (Queue, error) {
	queue := Queue{Repo: ownerAndRepo(repo)}
	folder := filepath.Join(home, "state", repo)
	names, err := os.ReadDir(folder)
	if err != nil {
		if os.IsNotExist(err) {
			// A machine crewflow has run nothing in has no state of that project, and
			// nothing of it wants anybody.
			return queue, nil
		}
		return Queue{}, fmt.Errorf("read the states of the tasks in %s: %w", folder, err)
	}
	for _, name := range names {
		if name.IsDir() || !isStateOfATask(name.Name()) {
			continue
		}
		path := filepath.Join(folder, name.Name())
		// One state crewflow cannot read does not take the tasks beside it down with it:
		// what wants a person is what a person came for, and the file that could not be
		// read is the one they have to open by hand.
		state, err := LoadState(path)
		if err != nil {
			queue.Unreadable = append(queue.Unreadable, path)
			continue
		}
		// The state of the task is read on its own first: a run that is working asks for
		// nobody, and a project of a hundred tasks is asked about the ones that wait and
		// not about the rest (§6).
		one, wanted := AttentionOf(env, repo, state, HostFacts{})
		if !wanted {
			continue
		}
		if endedRun(lastAttemptOf(state)) {
			// Only a run that has ended can be cleared by a reaction of the host — a
			// change merged, a task closed, a record of a review — and a queue that asked
			// about a run that is going would put a network in the middle of a list.
			one, wanted = AttentionOf(env, repo, state, askHost(ctx, host, state))
			if !wanted {
				continue
			}
		}
		if one.State == AttentionEscalated && one.Reason != ReasonTaskMissing {
			one.Said, one.Unsaid = leaveRecord(ctx, env, say, path, state, one)
		}
		queue.Entries = append(queue.Entries, one)
	}
	slices.SortFunc(queue.Entries, byUrgency)
	return queue, nil
}

// lastAttemptOf is the last attempt of a task, and nothing at all where the task has none.
func lastAttemptOf(state State) Attempt {
	if len(state.Attempts) == 0 {
		return Attempt{}
	}
	return state.Attempts[len(state.Attempts)-1]
}

// endedRun is whether the last attempt of a task has ended, which is what a reaction of the
// host may still be about: nobody reacts to a change while the run that opened it works.
func endedRun(last Attempt) bool {
	return last.Number > 0 && last.Outcome != Running
}

// askHost is what the host of the project says about a task: its change request where the
// run opened one, and the task itself in every case, because a task the host has closed is
// done whoever closed it (§7g).
func askHost(ctx context.Context, host Host, state State) HostFacts {
	if host == nil {
		return HostFacts{}
	}
	change := 0
	if state.Change != nil {
		change = state.Change.Number
	}
	facts, err := host.FactsOf(ctx, state.Number, change)
	if err != nil {
		// A host that cannot be read is a host that is not there, and the queue says so
		// in the entry instead of concluding that the task wants nobody (§7h).
		return HostFacts{Problem: err.Error()}
	}
	facts.Asked = true
	return facts
}

// leaveRecord is the notice crewflow leaves under the task of an entry that waits for
// longer than the project agreed, with what it wrote and what stood in the way where it
// wrote nothing, and the state of the task as it is to be kept. Nothing is left for an
// entry that is not escalated: a queue is a question, and a task that a person stopped is
// not a thing to remind of (§6a).
func leaveRecord(ctx context.Context, env AttentionEnv, say SayAttention, path string, state State, one Attention) (bool, string) {
	now := env.Now()
	if state.NoticedWithin(now, env.RemindAfter, one.Key()) {
		// The same key was left under this task within the day, and a record a day is all
		// a task is promised: a person who reads it needs to know that the waiting lasts,
		// not to read it again every minute.
		return false, ""
	}
	if say == nil {
		return false, NoRecord
	}
	said, problem := say(ctx, one)
	if said {
		_ = SaveState(path, state.Noticed(now, one.Key()))
	}
	return said, problem
}

// AttentionOf is what the queue says about one task at this moment, out of the state of its
// runs and out of what the host of the project says about the change of the last of them,
// and whether the task is in the queue at all. A task with no run in it has nothing
// running and nothing finished, and a task whose run is working is not asking for anybody
// today (§6a).
//
// The project is named the way its state is kept, with a dash where the host has a slash,
// because that is also the name of the folder the journals of the runs are in: an entry of a
// task the host does not have says where those journals stayed, and the path it gives is the
// one a person can open (§7).
func AttentionOf(env AttentionEnv, repo string, state State, facts HostFacts) (Attention, bool) {
	if len(state.Attempts) == 0 {
		return Attention{}, false
	}
	last := state.Attempts[len(state.Attempts)-1]
	now := env.Now()
	outcome := env.outcome(last)
	silence := silenceOf(last, state.Profile, now)
	one := Attention{
		Repo:    ownerAndRepo(repo),
		Task:    state.Number,
		Title:   line(state.Title),
		Run:     runOf(state),
		Outcome: outcome,
		Change:  state.Change,
		// What the host could not be asked is said in the entry itself: a person who
		// reads the queue has to know that the change request of the task was not read
		// and not that the queue is certain about it (§7h).
		Problem: facts.Problem,
	}
	switch outcome {
	case Running, Stalled:
		// A run that is going is in the queue in two cases only, and which of the two it is
		// does not depend on how long it has been: a run that stands because nobody knows
		// why, and a run that waits because crewflow does (AQ-001, AQ-008).
		switch {
		case silence.Reason != "":
			one.waits(silence)
		case silence.standing(env.StallAfter):
			one.stands(silence)
		default:
			return Attention{}, false
		}
	case Interrupted:
		one.stopped(last)
	case Blocked, BlockedPermission, BlockedSecret:
		one.refused(last, silence, state.Checkpoint)
	default:
		if !one.finished(env, repo, state, last, outcome, facts) {
			return Attention{}, false
		}
	}
	if facts.Problem != "" {
		// The host could not be read, and crewflow cannot say whether somebody has already
		// looked at the result of the run: an entry that said "act now" about a change that
		// went in a week ago is a zombie of its own, and the oldest kind there is (F-061).
		one.Actable = ActUnknown
	}
	one.measured(env, now)
	return one, true
}

// waits is a run that is going and waits for something crewflow knows the name of: the
// window of the keychain of the machine above all. It is a wait and not a hang whatever the
// length of it, and a timeout does not turn it into one either (AQ-001, AQ-008, ES-006).
func (a *Attention) waits(silence Stall) {
	a.LastStep = silence.LastStep
	window, in := windowOf(silence.Reason)
	switch {
	case in:
		// A window of the system of the machine: the channel it is asked in, the resource
		// it is about and the action a person has to take in it are told from the wait
		// itself, and what the person is to do afterwards is the command that goes on from
		// the point the run stood at (#117).
		a.State, a.Reason = AttentionBlocked, ReasonHumanAuthorization
		a.Channel, a.Resource, a.Action = window.Channel, SubjectExecutorKey, window.Action
		a.Subject, a.NextActor, a.Priority, a.Actable = SubjectExecutorKey, ActorOwner, High, ActNow
		a.Next = resumeCommand(a.Task)
	case named(silence.Reason) == ReasonHumanAuthorization:
		// A decision of a person whose channel and action the state of the task does not
		// name. The queue says that a person is needed and that it does not know what is
		// being asked of him, because a guessed channel is a window a person will not
		// find (HA-004, §7e).
		a.State, a.Reason = AttentionBlocked, ReasonHumanAuthorization
		a.Subject, a.NextActor, a.Priority, a.Actable = "", ActorOwner, High, ActUnknown
	case named(silence.Reason) == ReasonResourceWait:
		// A resource of the project with a window crewflow does not know yet: nobody is
		// asked for anything, and the queue says that it cannot say whether a person may
		// act, rather than inventing a window that is not there (#78).
		a.State, a.Reason = AttentionAwaitsResource, ReasonResourceWait
		a.Subject, a.NextActor, a.Priority, a.Actable = "", ActorNobody, Normal, ActUnknown
	default:
		// A reason the state of a run holds that is not a window of the system: a refusal
		// the run stopped in, and the orchestrator is the one who may answer it (§7d, §7e).
		a.State, a.Reason = AttentionBlocked, refusalOf(silence.Reason)
		a.Subject, a.NextActor, a.Priority, a.Actable = named(silence.Reason), ActorOrchestrator, High, ActNow
	}
	a.Since = silence.At
}

// stands is a run that is going and has shown nothing for longer than the silence of the
// project, and where crewflow does not know what it waits for: the one state of the queue
// that is always about something nobody can say (AQ-002).
func (a *Attention) stands(silence Stall) {
	a.State, a.Reason = AttentionStands, ReasonNoProgress
	a.Subject, a.NextActor, a.Priority, a.Actable = "", ActorOrchestrator, High, ActNow
	a.Since, a.LastStep = silence.At, silence.LastStep
	a.Next = "look into what the run is standing at"
}

// stopped is a run a person stopped: it is not over, nobody is looking at it, and crewflow
// neither goes on by itself nor hides the fact (§7a, #45).
func (a *Attention) stopped(last Attempt) {
	a.State, a.Reason = AttentionStopped, ReasonStoppedByHand
	a.Subject, a.NextActor, a.Priority, a.Actable = SubjectResult, ActorEither, Normal, ActWatch
	a.Since = endedAt(last)
	a.Next = continueCommand(a.Task)
}

// refused is a run that was refused something and ended: a permission, a secret, or a
// decision of a person that only a person may give. A refusal in front of a window of the
// system is a wait for that window and is said as one (ES-006, §7i).
func (a *Attention) refused(last Attempt, silence Stall, point *Checkpoint) {
	switch last.Outcome {
	case BlockedPermission:
		a.State, a.Reason, a.Priority = AttentionBlocked, ReasonBlockedPermission, High
	case BlockedSecret:
		a.State, a.Reason, a.Priority = AttentionBlocked, ReasonBlockedSecret, High
	default:
		a.State, a.Reason, a.Priority = AttentionBlocked, ReasonBlocked, High
	}
	a.NextActor, a.Actable, a.Since = ActorOrchestrator, ActNow, endedAt(last)
	a.LastStep, a.Subject = silence.LastStep, last.Reason
	window, in := windowOf(last.Reason)
	switch {
	case in:
		// The run stood in front of a window of the machine and the window was not
		// answered: what is missing is a click of a person, not a permission of a
		// project, and the queue says that a person is needed (§7i).
		a.State, a.Reason = AttentionBlocked, ReasonHumanAuthorization
		a.Channel, a.Resource, a.Action = window.Channel, SubjectExecutorKey, window.Action
		a.Subject, a.NextActor, a.Actable = SubjectExecutorKey, ActorOwner, ActNow
		a.answerOf(point)
	case named(last.Reason) == ReasonHumanAuthorization:
		a.State, a.Reason = AttentionBlocked, ReasonHumanAuthorization
		a.Subject, a.NextActor, a.Actable = "", ActorOwner, ActUnknown
	}
}

// answerOf is what may be done about a run that was refused a decision of a person: the
// command that goes on from the point the run stands at — or, where the person refused
// that request, the run of the task anew, because `crewflow task resume` refuses a
// refused point and a queue that keeps naming a command that always says no is a queue
// that cries wolf (docs/DESIGN.md §6a, §7i).
func (a *Attention) answerOf(point *Checkpoint) {
	if point != nil && point.Outcome == WaitDenied {
		a.Next = runCommand(a.Task)
		a.Hint = fmt.Sprintf("this build of crewflow was refused the key of the app on %s: "+
			"sign it, or run the task as the owner, and the run will ask the window again",
			saidAt(point.DecidedAt))
		return
	}
	a.Next = resumeCommand(a.Task)
}

// gone is a run of a task the host of the project does not have: the repository moved, or
// another one took its name, and the state of the run is a fact of yesterday. It is in the
// queue and it is not an expectation — nobody is waited for, nothing is escalated and
// nothing is ever written under a task that is not there — and the journal of the run is
// where it always was (§6a, §7g).
func (a *Attention) gone(repo string, last Attempt) {
	a.State, a.Reason, a.Priority = AttentionFinishedUnseen, ReasonTaskMissing, Normal
	a.Subject, a.NextActor, a.Actable = SubjectResult, ActorOwner, ActWatch
	a.Since = endedAt(last)
	a.Hint = fmt.Sprintf("the task is not on %s: the repository may have moved, the journal of the run stays in %s",
		a.Repo, journalFolder(repo))
	a.LastStep = last.LastStep
}

// journalFolder is where the journals of the runs of a project are kept, as a person is
// told the path: `~/.crewflow/runs/<project>`, the same words DESIGN §7 uses.
func journalFolder(repo string) string {
	return filepath.Join("~", ".crewflow", "runs", repo)
}

// finished is a run that ended and left a task that wants somebody: the work of the task
// is over and its result is not looked at, or the run ended with nothing at all, or it
// ended too late, or it changed what it was not to change. Whether the task is in the queue
// at all is the question of the change request: a change that is merged or closed has been
// dealt with by the host and needs nobody (§6a).
func (a *Attention) finished(env AttentionEnv, repo string, state State, last Attempt, outcome Kind, facts HostFacts) bool {
	if state.MergedSHA != "" {
		// crewflow merged the change of the task itself and wrote the commit of it in the
		// state of the task: that is the reaction the queue waits for, and it is in the
		// state and not in the host, so it is said with or without a network (§7h).
		return false
	}
	if facts.Missing {
		// The host of the project does not have the task: there is nothing of it to look
		// at, there is nothing to write under it and nobody is waited for (§7g).
		a.gone(repo, last)
		return true
	}
	if change := facts.Change; change != nil {
		if !change.open() {
			// The host says the change is merged or closed: the reaction the queue waits
			// for has happened, and the task is not waiting for anything (§7h).
			return false
		}
		if facts.Closed {
			// The task behind an open change is closed: whoever closed it has dealt with
			// it, and a queue that keeps asking for a review of a task nobody has open is
			// a queue that cries wolf until a person stops reading it (§7g).
			return false
		}
		return a.awaiting(env, state, last, facts, change)
	}
	if facts.Closed {
		// The host says the task is closed and there is no change request to ask about:
		// the work of the task is over, and nobody waits for anything in it (§7g).
		return false
	}
	if !wantsAttention(outcome) && state.Change == nil {
		// A run that came out of it well and opened no change request asks for nobody,
		// however long ago it was.
		return false
	}
	// A run that opened a change request asks for the orchestrator until the host says
	// that somebody has looked at it, and where nothing was asked the state of the task
	// is the whole of what crewflow knows: that is F-061, three runs that ended with an
	// open change while everybody saw work going on (§6a).
	a.State, a.Reason, a.Priority = AttentionFinishedUnseen, endedReason(state, outcome), High
	a.Subject, a.NextActor, a.Actable = subjectOf(state), ActorOrchestrator, ActNow
	a.Since, a.Next = endedAt(last), nextOf(state)
	return true
}

// awaiting is a task whose change request is open and which a person is waited for: the
// orchestrator while no record of a review of the head of the change is under it, and the
// owner where the result of the task is his to accept (§6a, §7h).
func (a *Attention) awaiting(env AttentionEnv, state State, last Attempt, facts HostFacts, change *ChangeFacts) bool {
	if env.AcceptanceLabel != "" && facts.marked(env.AcceptanceLabel) {
		switch {
		case change.Accepted:
			// The owner took the result in and it is not merged: nothing of this is waited
			// for any more, and what happens next is the merge of §7h.
			return false
		case change.Approved:
			// The change is approved and the owner has not looked at the result of the
			// task. That wait began when the change was approved and not when the run
			// ended, and it is the wait of ES-002 and ES-003.
			a.State, a.Reason, a.Priority = AttentionAwaitsOwner, ReasonOwnerAcceptance, High
			a.Subject, a.NextActor, a.Actable = SubjectOwner, ActorOwner, ActNow
			a.Since, a.Next = atOr(change.Last, endedAt(last)), ownerDecision(state)
			return true
		}
	}
	if change.Approved {
		// The change of the task is approved and the result of it is nobody's to accept:
		// the next step of the cycle is the merge, and a merge is the orchestrator's own
		// next step after his own review. The queue watches what is waited for and not
		// what a person has already answered (§6a, §7h).
		return false
	}
	// A change with no record of a review under it, or with a record that is not of the
	// head it stands at now: the head moved after the last record, and the review that was
	// written is a review of a commit that is not there any more (§7h, F-065).
	a.State, a.Reason, a.Priority = AttentionAwaitsReview, ReasonReviewRequired, Normal
	a.Subject, a.NextActor, a.Actable = SubjectReviewer, ActorOrchestrator, ActNow
	a.Since, a.Next = endedAt(last), nextOf(state)
	return true
}

// measured is what the queue says about the age of an entry and about how loudly it asks.
// It is worked out here for every state alike, because the thresholds of the project are
// one thing and the state of the task is another (§6a).
func (a *Attention) measured(env AttentionEnv, now time.Time) {
	a.Since = atOr(a.Since, now)
	waited := max(now.Sub(a.Since), 0)
	a.WaitingSeconds = waited.Seconds()
	if env.EscalateAfter > 0 && waited > env.EscalateAfter && a.Reason != ReasonTaskMissing {
		// The state keeps its place beside the escalation, because an escalation of a task
		// nobody knows the cause of is an escalation about nothing (AQ-008). A task the
		// host does not have is not escalated: it is a fact of a project that moved, and
		// a reminder about a task of another repository is a record of a lie (§7g).
		a.EscalatedFrom, a.State, a.Priority = a.State, AttentionEscalated, Critical
		a.Actable = ActNow
	}
	if env.WeeklyAfter > 0 && waited > env.WeeklyAfter {
		a.LongWaiting = true
	}
	a.Promoted = a.Actable == ActNow || (env.TopAfter > 0 && waited > env.TopAfter)
}

// byUrgency is the order a person reads the queue in (AQ-005): what has to be looked into
// first is first — a run that stands, because nobody knows what is happening to it — then
// what has been waiting for a person for longer than it should, and the rest of what wants
// one behind them. Inside one state the entry that has waited longest comes first, because
// the one that has waited longest is the one that has waited most.
func byUrgency(a, b Attention) int {
	if rank, other := rankOf(a.State), rankOf(b.State); rank != other {
		return rank - other
	}
	return -cmp.Compare(a.Waited(), b.Waited())
}

// ranks is the place of every state of the queue in it. The eight states have one order
// and it is written down here, so that a queue two machines answer about the same project
// is read in the same order.
var ranks = map[AttentionState]int{
	AttentionStands:         0,
	AttentionEscalated:      1,
	AttentionAwaitsOwner:    2,
	AttentionAwaitsReview:   3,
	AttentionAwaitsResource: 4,
	AttentionFinishedUnseen: 5,
	AttentionBlocked:        6,
	AttentionStopped:        7,
}

// rankOf is the place of a state in the queue, and the last of them for a state crewflow
// does not know: a state it has never heard of is not more urgent than a known one.
func rankOf(state AttentionState) int {
	if rank, known := ranks[state]; known {
		return rank
	}
	return len(ranks)
}

// Write is the queue of attention for a person, at the top of a list of runs: what of the
// project wants a person right now, why it wants one, what it waits for, who acts next, how
// long it has been that way and what may be done about it. Only the entries that are
// promoted are in the block — the ones somebody may act on and the ones that have waited
// for longer than `[attention] top_after` — because the top of a list is where the work is,
// and the rest of the queue is in the answer of `-json` and in `crewflow task attention`
// (docs/DESIGN.md §6a).
//
// An entry is two lines and not one, because a state of a process and a thing to do about
// it are two things: a person reads the first line to learn which task it is and what it
// waits for, and the second to learn how long and what to do.
func (q Queue) Write(w io.Writer, screen Screen) error {
	promoted := q.Promoted()
	if len(promoted) == 0 {
		return nil
	}
	lines := []attentionLine{{attentionHeading, bold}}
	for _, one := range promoted {
		lines = append(lines,
			attentionLine{saidOf(one, screen), colourOfPriority(one.Priority)},
			attentionLine{detailOf(one), faint})
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(w, painted(line.text, line.colour, screen)); err != nil {
			return fmt.Errorf("write the queue of attention: %w", err)
		}
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return fmt.Errorf("write the queue of attention: %w", err)
	}
	return nil
}

// Promoted is the queue of the entries that go to the top of a list of runs: the ones
// somebody may act on right now and the ones that have waited for longer than
// `[attention] top_after` whatever their class is (docs/DESIGN.md §6a).
func (q Queue) Promoted() []Attention {
	var promoted []Attention
	for _, one := range q.Entries {
		if one.Promoted {
			promoted = append(promoted, one)
		}
	}
	return promoted
}

// attentionHeading is the name of the block at the top of a list of runs: the class of §6a
// in the words of the machine, because a list of runs is read by a program as much as by a
// person.
const attentionHeading = "ATTENTION REQUIRED"

// attentionLine is one line of the block of the queue and the colour it is read in: the
// name of the block and the state of an entry are read in colour, and the rest of the entry
// is said quietly so that the eye goes on to the state and not into the line (docs/DESIGN.md
// §6a).
type attentionLine struct {
	text   string
	colour string
}

// saidOf is the first line of an entry: which task it is, what state the process of it is
// in, why, and the diagnosis of a request of a person where the reason of the entry is one.
// The state is the only part of it that is painted: the state is what a person decides
// about, and the rest of the line is what they read it with (docs/DESIGN.md §6a).
func saidOf(one Attention, screen Screen) string {
	said := []string{fmt.Sprintf("  #%d", one.Task), painted(string(one.State), colourOfPriority(one.Priority), screen)}
	if one.EscalatedFrom != "" {
		// An escalation keeps the state it came from in the line, because an escalation of
		// a task nobody knows the cause of is an escalation about nothing (AQ-008).
		said = append(said, "("+string(one.EscalatedFrom)+")")
	}
	if one.Reason != "" {
		said = append(said, one.Reason)
	}
	if one.Channel != "" {
		said = append(said, "channel "+one.Channel, "the resource "+one.Resource, "the action "+one.Action)
	}
	if one.Subject != "" {
		said = append(said, one.Subject)
	}
	if one.LongWaiting {
		said = append(said, "a long wait (#37)")
	}
	return strings.Join(said, mid)
}

// detailOf is the second line of an entry: how long the state has lasted, who acts next,
// the last step of a run that stands, and what may be done about it.
func detailOf(one Attention) string {
	said := []string{"    waiting " + Idle(one.Waited()), "next: " + one.NextActor}
	if one.LastStep != "" {
		said = append(said, "the last step: "+one.LastStep)
	}
	if one.Hint != "" {
		said = append(said, one.Hint)
	}
	if one.Problem != "" {
		said = append(said, "the host could not be read: "+one.Problem)
	}
	if one.Unsaid != "" {
		said = append(said, "not left under the task: "+one.Unsaid)
	}
	if one.Next != "" {
		said = append(said, one.Next)
	}
	return strings.Join(said, mid)
}

// colourOfPriority is the colour an entry of the queue is read in: a critical entry — one
// that has waited for longer than the project agreed — is red, an entry a person has to
// answer today is yellow, and a normal one is said in the letters of it, because a colour
// where nobody is in a hurry is a colour that cries wolf (docs/DESIGN.md §6a).
func colourOfPriority(priority Priority) string {
	switch priority {
	case Critical:
		return red
	case High:
		return yellow
	default:
		return ""
	}
}

// NoticeUnder is the line crewflow leaves under the task of an entry of the queue that has
// waited for longer than the project agreed, one line per key: a task that a schedule asks
// about every minute gets one line and not one a minute, and a change of the state or of
// the reason of the wait gets a new one (§6a). It is the notice that the state of the task
// remembers by its key, and a task of no notice is a task nobody has been told about.
func NoticeUnder(one Attention) string {
	state := string(one.State)
	if one.EscalatedFrom != "" {
		state = fmt.Sprintf("%s of %s", one.State, one.EscalatedFrom)
	}
	said := fmt.Sprintf("crewflow: the task %d (%s) is %s: %s", one.Task, one.Run, state, one.Reason)
	if one.Channel != "" {
		said += fmt.Sprintf(" (channel %s, the resource %s, the action %s)", one.Channel, one.Resource, one.Action)
	}
	said += fmt.Sprintf(" — it waits for %s, the next one is %s, it has been waiting for %s",
		orNothing(one.Subject), one.NextActor, Idle(one.Waited()))
	if one.LastStep != "" {
		said += ", the last step: " + one.LastStep
	}
	if one.Problem != "" {
		said += ". The host could not be read, and crewflow does not know whether the result of the run " +
			"was looked at: " + one.Problem
	}
	if one.Next != "" {
		said += ". What may be done about it: " + one.Next
	}
	return said + " (docs/DESIGN.md §6a)."
}

// windows are the windows of the systems a run of crewflow has stood in front of, by the
// name the state of its task holds. A name that is not in here is a wait whose channel and
// action the queue cannot say, and it says so rather than calling it the keychain.
var windows = map[string]window{
	reasonApproval: {ChannelKeychain, ActionHumanExecute},
}

// window is the channel of a request of a person and the action the person is to take in
// it. The state of a task holds the reason of the wait and nothing more, and the diagnosis
// of the request is told from the name of the window (ES-006, §7f, §7i).
type window struct {
	Channel string
	Action  string
}

// windowOf is the window of a system a run stands at, and whether the reason names one.
// A reason carries the words of the refusal after a colon — `keychain-approval: …` — and
// the name of the window is the part before them (§7i).
func windowOf(reason string) (window, bool) {
	said, is := windows[named(reason)]
	return said, is
}

// named is the name of a reason out of what a state of a task holds: a refusal is a name
// and then what the refusal said, and the queue names and does not repeat (§7i).
func named(reason string) string {
	name, _, _ := strings.Cut(reason, ":")
	return strings.TrimSpace(name)
}

// refusalOf is the reason of a refused run as the queue names it: the name the state of the
// task holds, and the refusal itself where the state holds none.
func refusalOf(reason string) string {
	if name := named(reason); name != "" {
		return name
	}
	return ReasonBlocked
}

// endedReason is why a run that ended and left a task nobody is looking at is in the queue.
func endedReason(state State, outcome Kind) string {
	switch outcome {
	case TimedOut:
		return ReasonRunTimeout
	case ExecutorFailed, MaybeRunning:
		return ReasonRunFailed
	case OutOfScope:
		return ReasonOutOfScope
	case ChangeRequestOpened:
		return ReasonRunCompleted
	}
	if state.Change != nil {
		return ReasonRunCompleted
	}
	return ReasonRunEnded
}

// atOr is the moment of a wait, and the one that is given where crewflow does not know the
// moment: a wait that began nobody knows when is counted from the moment the run was last
// seen, and a moment that was never is not a moment at all.
func atOr(when, or time.Time) time.Time {
	if when.IsZero() {
		return or
	}
	return when
}

// endedAt is when the last attempt of a task ended, and when it began where nothing kept
// the end of it: a run whose process is gone was not seen to stop, and the wait that began
// when it was last seen is the wait crewflow can prove (§7).
func endedAt(last Attempt) time.Time {
	if !last.EndedAt.IsZero() {
		return last.EndedAt
	}
	return last.StartedAt
}

// subjectOf is what a task whose run ended waits for: the change request of the run where
// it opened one, and the result of the run itself where it did not.
func subjectOf(state State) string {
	if state.Change == nil {
		return SubjectResult
	}
	return "the change #" + strconv.Itoa(state.Change.Number)
}

// nextOf is the command that goes on with a task whose run ended: a review of its change
// request where the run opened one, and a continuation of the run in its own worktree where
// it did not (§7, §7a).
func nextOf(state State) string {
	if state.Change != nil {
		return "crewflow review " + strconv.Itoa(state.Change.Number)
	}
	return continueCommand(state.Number)
}

// continueCommand is the command that goes on in the worktree and the session of a run that
// was stopped and is not over (§7a).
func continueCommand(task int) string {
	return fmt.Sprintf("crewflow task run %d -continue \"what is left to do\"", task)
}

// resumeCommand is the command that goes on from the point a run stood at, with the context
// of the run kept: the worktree of the task and the task in hand are the ones the run left,
// and a person has done the only thing that was his to do (docs/DESIGN.md §6a, §7i).
func resumeCommand(task int) string {
	return fmt.Sprintf("crewflow task resume %d", task)
}

// runCommand is the command that runs a task from the beginning, which is what is left of a
// task whose point is not one to go on from: its point was refused, or it is older than the
// term of a point (§7i).
func runCommand(task int) string {
	return fmt.Sprintf("crewflow task run %d", task)
}

// ownerDecision is what a task that waits for the owner is to be given: his decision under
// the change request of the run, in the words the gate counts (§7h).
func ownerDecision(state State) string {
	if state.Change == nil {
		return "the decision of the owner under the task"
	}
	return "the decision of the owner under #" + strconv.Itoa(state.Change.Number)
}

// orNothing is what stands where the queue has nothing to name: an entry says `unknown`
// rather than an empty column, because a column that is empty is a column nobody reads
// (§6a).
func orNothing(said string) string {
	if said == "" {
		return "unknown"
	}
	return said
}
