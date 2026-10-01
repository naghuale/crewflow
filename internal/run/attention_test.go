package run

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// The queue of attention is the answer to the question an orchestrator and an owner ask
// first, and the tests of it are the failures of the practice journal (#37) it is there
// for: a run that ended with a change request and that nobody noticed for an hour and a
// half (F-061), a run that stands with a live process and no work in it (F-041, §7a), and
// a task that waits for a decision of a person that nothing shows (F-065).

// TestARunThatEndedWithAChangeAndNobodyHasLookedAtItIsFinishedUnseen: the run of the
// first failure. A run that ended with its change request open and that nobody has
// reacted to is in the queue as `finished-unseen`, the next one is the orchestrator, and
// the state of the process is what a person reads first (docs/DESIGN.md §6, §6a).
func TestARunThatEndedWithAChangeAndNobodyHasLookedAtItIsFinishedUnseen(t *testing.T) {
	home, repo := t.TempDir(), "naghuale-crewflow"
	ended := monday.Add(8*time.Hour + 42*time.Minute)
	writeState(t, home, repo, 43, "the queue of attention", &Change{Number: 113, URL: "https://example.com/113"},
		try{startedAt: monday.Add(8 * time.Hour), endedAt: ended, outcome: ChangeRequestOpened})
	at := ended.Add(time.Hour)

	queue := queueOf(t, home, repo, attentionEnvOf(at), nil)

	one, wanted := queue.Wanted(43)
	if !wanted {
		t.Fatalf("the queue holds %+v, want the task 43 in it", tasksOfQueue(queue))
	}
	if one.State != AttentionFinishedUnseen {
		t.Errorf("the state = %q, want %q: a run nobody has looked at is not seen", one.State, AttentionFinishedUnseen)
	}
	if one.Reason != ReasonRunCompleted {
		t.Errorf("the reason = %q, want %q", one.Reason, ReasonRunCompleted)
	}
	if one.NextActor != ActorOrchestrator {
		t.Errorf("the next one = %q, want %q: a change nobody has looked at is the work of the orchestrator",
			one.NextActor, ActorOrchestrator)
	}
	if one.Priority != High {
		t.Errorf("the priority = %q, want %q: F-061 cost an hour and a half of nothing", one.Priority, High)
	}
	if one.Waited() != time.Hour {
		t.Errorf("the task has been waiting for %s, want 1h: the queue counts from the end of the run", Idle(one.Waited()))
	}
	if one.Next != "crewflow review 113" {
		t.Errorf("what may be done = %q, want the review of #113", one.Next)
	}
	if one.Said || one.Unsaid != "" {
		t.Errorf("the queue says it left %q under the task, want nothing: a queue is a question and writes nowhere", one.Unsaid)
	}
}

// TestAReviewOfTheChangeIsAResponseAndLeavesTheQueue: what clears `finished-unseen` is
// the reaction of a person and nothing else — a record of a review of the head of the
// change as it stands, a continuation of the run, or a merge. A review of an earlier head
// is not a reaction to what the change says now, and F-065 is a run that hung with the
// record of the review somewhere in it (docs/DESIGN.md §6a, §7h).
func TestAReviewOfTheChangeIsAResponseAndLeavesTheQueue(t *testing.T) {
	const repo = "naghuale-crewflow"
	ended := monday.Add(8*time.Hour + 42*time.Minute)
	change := &Change{Number: 113, URL: "https://example.com/113"}
	head := "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b"
	old := "0000000000000000000000000000000000000000"
	for _, tc := range []struct {
		name  string
		facts HostFacts
		want  bool
	}{
		{
			name:  "no record of a review at all",
			facts: HostFacts{Change: &ChangeFacts{Number: 113, State: "open", Head: head}},
			want:  true,
		},
		{
			name:  "a review of the head as it stands",
			facts: HostFacts{Change: &ChangeFacts{Number: 113, State: "open", Head: head, Approved: true, Last: ended}},
			want:  false,
		},
		{
			name:  "a review of a head that has grown since",
			facts: HostFacts{Change: &ChangeFacts{Number: 113, State: "open", Head: head, Last: ended}},
			want:  true,
		},
		{
			name:  "a change the host has merged",
			facts: HostFacts{Change: &ChangeFacts{Number: 113, State: "merged", Head: head}},
			want:  false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			writeState(t, home, repo, 43, "the queue of attention", change,
				try{startedAt: monday.Add(8 * time.Hour), endedAt: ended, outcome: ChangeRequestOpened})

			queue := queueOf(t, home, repo, attentionEnvOf(ended.Add(time.Hour)), &hostOfTest{facts: map[int]HostFacts{43: tc.facts}})

			_, inQueue := queue.Wanted(43)
			if inQueue != tc.want {
				t.Errorf("the task is in the queue: %v, want %v", inQueue, tc.want)
			}
		})
	}
	_ = old
}

// TestTheReactionOfTheHostTakesTheRunOutOfTheQueue: the review of PR #118 on a live run.
// A change request that is merged or closed is the reaction, and a task the host closed is
// done: a queue that keeps a merged change in it for two days is the zombie this task is
// named after, and it is worse than the one of a standing run, because the run is over and
// the queue cannot see it (F-061, §6a).
func TestTheReactionOfTheHostTakesTheRunOutOfTheQueue(t *testing.T) {
	const repo = "naghuale-crewflow"
	ended := monday.Add(8*time.Hour + 42*time.Minute)
	head := "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b"
	change := &Change{Number: 113, URL: "https://example.com/113"}
	reviewer := &hostOfTest{}
	for _, tc := range []struct {
		name  string
		facts HostFacts
		want  bool
	}{
		{
			name:  "an open change and no record of a review after the run",
			facts: HostFacts{Change: &ChangeFacts{Number: 113, State: "open", Head: head}},
			want:  true,
		},
		{
			name:  "a change the host merged",
			facts: HostFacts{Change: &ChangeFacts{Number: 113, State: "merged", Head: head}},
			want:  false,
		},
		{
			name:  "a change the host closed",
			facts: HostFacts{Change: &ChangeFacts{Number: 113, State: "closed", Head: head}},
			want:  false,
		},
		{
			name:  "a task the host closed behind an open change",
			facts: HostFacts{Closed: true, Change: &ChangeFacts{Number: 113, State: "open", Head: head}},
			want:  false,
		},
		{
			name:  "a task the host closed, where the run opened no change",
			facts: HostFacts{Closed: true},
			want:  false,
		},
		{
			name:  "a task the host has open, where the run opened no change",
			facts: HostFacts{Change: &ChangeFacts{Number: 0, State: "open", Head: head}},
			want:  true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			writeState(t, home, repo, 43, "the run of a task", change,
				try{startedAt: monday.Add(8 * time.Hour), endedAt: ended, outcome: ChangeRequestOpened})
			reviewer.facts = map[int]HostFacts{43: tc.facts}

			queue := queueOf(t, home, repo, attentionEnvOf(ended.Add(66*time.Hour)), reviewer)

			_, inQueue := queue.Wanted(43)
			if inQueue != tc.want {
				t.Errorf("the task is in the queue: %v, want %v (%+v)", inQueue, tc.want, queue.Entries)
			}
		})
	}
}

// TestARunThatWasClearedLeavesNoRecordUnderTheTask: a run whose change went in is not in
// the queue, and crewflow writes nothing under its task about a wait that is over — the
// records of the queue of attention are about what is still waited for (docs/DESIGN.md §6a).
func TestARunThatWasClearedLeavesNoRecordUnderTheTask(t *testing.T) {
	home, repo := t.TempDir(), "naghuale-crewflow"
	ended := monday.Add(8*time.Hour + 42*time.Minute)
	writeState(t, home, repo, 43, "the run of a task", &Change{Number: 113, URL: "https://example.com/113"},
		try{startedAt: monday.Add(8 * time.Hour), endedAt: ended, outcome: ChangeRequestOpened})
	said := &saidUnderTheTask{}
	host := &hostOfTest{facts: map[int]HostFacts{43: {
		Change: &ChangeFacts{Number: 113, State: "merged", Head: "abc"},
	}}}

	queue := queueOf(t, home, repo, attentionEnvOf(ended.Add(66*time.Hour)), host, said.sayAttention)

	if len(queue.Entries) != 0 {
		t.Errorf("the queue holds %+v, want nothing: the change of the task went in", queue.Entries)
	}
	if len(said.lines) != 0 {
		t.Errorf("crewflow left %q under a task whose change is merged, want nothing", said.lines)
	}
}

// TestAHostThatCouldNotBeReadLeavesNoEntryAndNamesTheHole: where the host could not be
// read, crewflow cannot say whether somebody has already looked at the result of the run —
// и раньше это выражалось записью `unknown` в очереди. Такой записи больше нет: запись,
// о которой ничего не известно, занимает место настоящей и эскалируется вместе с ней
// (F-098, docs.DESIGN.md §6a, §7h).
//
// Задача не попадает в очередь вовсе, а причина остаётся названной — в блоке
// «не удалось прочитать», где у неё есть прогон и задача, к которым она относится.
func TestAHostThatCouldNotBeReadLeavesNoEntryAndNamesTheHole(t *testing.T) {
	home, repo := t.TempDir(), "naghuale-crewflow"
	ended := monday.Add(8*time.Hour + 42*time.Minute)
	writeState(t, home, repo, 43, "the run of a task", &Change{Number: 113, URL: "https://example.com/113"},
		try{startedAt: monday.Add(8 * time.Hour), endedAt: ended, outcome: ChangeRequestOpened})

	queue := queueOf(t, home, repo, attentionEnvOf(ended.Add(66*time.Hour)), &hostOfTest{})

	if _, inQueue := queue.Wanted(43); inQueue {
		t.Errorf("в очереди %v, want без задачи 43: неизвестное не является требованием внимания",
			tasksOfQueue(queue))
	}
	if len(queue.Unread) != 1 || queue.Unread[0].Task != 43 {
		t.Fatalf("в блоке «не прочитано» %+v, want задача 43 с названной проблемой", queue.Unread)
	}
	if queue.Unread[0].Problem == "" {
		t.Error("в блоке «не прочитано» нет проблемы, want она названа")
	}
}

// TestATaskTheHostDoesNotHaveIsAFactAndNotAWait: the third defect of the review of PR #118,
// found on telecli (01.10). The repository moved, another one took the name `naghuale/tele`
// and the issues of it renumbered; the runs of tasks of the old repository came out of the
// queue as `escalated · finished-unseen` with a comment written under a task that does not
// exist. A task the host says it does not have is its own reason: nobody is waited for,
// nothing is escalated, nothing is written under it, and where the journal of the run stayed
// is said in the row (§6a, §7g).
func TestATaskTheHostDoesNotHaveIsAFactAndNotAWait(t *testing.T) {
	home, repo := t.TempDir(), "naghuale-tele"
	ended := monday.Add(8*time.Hour + 42*time.Minute)
	writeState(t, home, repo, 53, "проверки навыков агента", &Change{Number: 61, URL: "https://example.com/61"},
		try{startedAt: monday.Add(8 * time.Hour), endedAt: ended, outcome: ChangeRequestOpened})
	host := &hostOfTest{facts: map[int]HostFacts{53: {Missing: true}}}
	at := ended.Add(66 * time.Hour)
	said := &saidUnderTheTask{}

	queue := queueOf(t, home, repo, attentionEnvOf(at), host, said.sayAttention)

	one, wanted := queue.Wanted(53)
	if !wanted {
		t.Fatalf("the queue holds %v, want the task 53 in it: the run is a fact a person has to see", tasksOfQueue(queue))
	}
	if one.Reason != ReasonTaskMissing {
		t.Errorf("the reason = %q, want %q", one.Reason, ReasonTaskMissing)
	}
	if one.State == AttentionEscalated || one.EscalatedFrom != "" {
		t.Errorf("the state = %q of %q, want %q: a fact of a project that moved is not escalated",
			one.State, one.EscalatedFrom, AttentionFinishedUnseen)
	}
	if one.Priority != Normal || one.Actable != ActWatch {
		t.Errorf("the entry is %q and %q, want %q and %q: nobody is waited for and nothing is asked",
			one.Priority, one.Actable, Normal, ActWatch)
	}
	if one.Problem != "" {
		t.Errorf("the entry says %q, want nothing: the host answered, it did not fail", one.Problem)
	}
	for _, want := range []string{"naghuale/tele", "may have moved", "~/.crewflow/runs/naghuale-tele"} {
		if !strings.Contains(one.Hint, want) {
			t.Errorf("the hint is %q, want it to mention %q", one.Hint, want)
		}
	}
	if len(said.lines) != 0 {
		t.Errorf("crewflow left %q under a task the host does not have, want nothing", said.lines)
	}
	// A run that went on after the repository moved is the same fact and is said as one: a
	// missing task says nothing about whether the process of the run is alive (F-042).
	kept, err := LoadState(newJournals(home, repo).StatePath(53))
	if err != nil {
		t.Fatalf("load the state of the task: %v", err)
	}
	if kept.Notice != nil {
		t.Errorf("the state of the task holds the notice %+v, want none: nothing was written under it", kept.Notice)
	}
}

// TestATrackerThatCouldNotBeReadIsNotAMissingTask: the other half of the rule. A network that
// failed, a 5xx and a repository gh cannot resolve are all "crewflow does not know" — и это
// не то же самое, что «задачи нет»: репозиторий переехал это одна причина, а сеть легла
// это другая, и различает их адаптер (§6a, §7g, §7h).
//
// Отказ сети — причина блока «не удалось прочитать», а не `task-missing` и не запись
// очереди: под задачей, о которой ничего не известно, crewflow ничего не пишет, потому
// что напоминание о собственном сбое — не напоминание о работе проекта (F-098, §6a).
func TestATrackerThatCouldNotBeReadIsNotAMissingTask(t *testing.T) {
	home, repo := t.TempDir(), "naghuale-tele"
	ended := monday.Add(8*time.Hour + 42*time.Minute)
	writeState(t, home, repo, 53, "the run of a task", &Change{Number: 61, URL: "https://example.com/61"},
		try{startedAt: monday.Add(8 * time.Hour), endedAt: ended, outcome: ChangeRequestOpened})
	said := &saidUnderTheTask{}

	queue := queueOf(t, home, repo, attentionEnvOf(ended.Add(66*time.Hour)), &hostOfTest{}, said.sayAttention)

	if _, inQueue := queue.Wanted(53); inQueue {
		t.Errorf("в очереди %v, want без задачи 53: сетевой отказ не повод требовать внимания",
			tasksOfQueue(queue))
	}
	if len(queue.Unread) != 1 {
		t.Fatalf("в блоке «не прочитано» %d строк, want одна: %+v", len(queue.Unread), queue.Unread)
	}
	unread := queue.Unread[0]
	if unread.Reason != ReasonReadFailed {
		t.Errorf("причина = %q, want %q: сеть легла, и это не «задачи нет»", unread.Reason, ReasonReadFailed)
	}
	if unread.Problem == "" {
		t.Error("в блоке «не прочитано» нет проблемы, want она названа")
	}
	if len(said.lines) != 0 {
		t.Errorf("под задачей оставлено %q, want ничего: о непрочитанном не напоминают", said.lines)
	}
}

// TestAnApprovedTaskOfTheLabelOfTheOwnerWaitsForTheOwner: the wait of ES-002 and ES-003. A
// change of a task marked `owner-check` is approved and the owner has not taken the result
// in, so the next one is the owner — and the wait began when the change was approved and
// not when the run ended (docs/DESIGN.md §6a, §7f, §7h).
func TestAnApprovedTaskOfTheLabelOfTheOwnerWaitsForTheOwner(t *testing.T) {
	home, repo := t.TempDir(), "naghuale-crewflow"
	ended := monday.Add(8*time.Hour + 42*time.Minute)
	approved := monday.Add(9 * time.Hour)
	writeState(t, home, repo, 43, "текст, который читает владелец",
		&Change{Number: 113, URL: "https://example.com/113"},
		try{startedAt: monday.Add(8 * time.Hour), endedAt: ended, outcome: ChangeRequestOpened})
	change := &ChangeFacts{Number: 113, State: "open", Head: "abc", Approved: true, Last: approved}
	host := &hostOfTest{facts: map[int]HostFacts{43: {
		Labels: []string{"owner-check", "risky"},
		Change: change,
	}}}
	at := approved.Add(3 * time.Hour)

	queue := queueOf(t, home, repo, attentionEnvOf(at), host)

	one, wanted := queue.Wanted(43)
	if !wanted {
		t.Fatalf("the queue holds %+v, want the task 43 in it", tasksOfQueue(queue))
	}
	if one.State != AttentionAwaitsOwner {
		t.Errorf("the state = %q, want %q: the result of the task is with the owner", one.State, AttentionAwaitsOwner)
	}
	if one.Reason != ReasonOwnerAcceptance {
		t.Errorf("the reason = %q, want %q", one.Reason, ReasonOwnerAcceptance)
	}
	if one.NextActor != ActorOwner {
		t.Errorf("the next one = %q, want %q", one.NextActor, ActorOwner)
	}
	if one.Waited() != 3*time.Hour {
		t.Errorf("the owner has been waiting for %s, want 3h: the wait began at the approval", Idle(one.Waited()))
	}
	// The owner took the result in, and the change is not merged: nothing of this is
	// waited for any more, and the next step of the cycle is the merge (§7h).
	change.Accepted = true
	queue = queueOf(t, home, repo, attentionEnvOf(at.Add(time.Minute)), host)
	if _, inQueue := queue.Wanted(43); inQueue {
		t.Errorf("the queue holds %+v after the acceptance, want the task out of it", tasksOfQueue(queue))
	}
}

// TestARunThatShowsNothingStands: a run whose executor has written nothing for longer than
// the silence of the project is not `running` and not a wait: nobody knows why it is quiet,
// and a run nobody knows about is a thing to look into (AQ-002, docs/DESIGN.md §6a, §7a).
func TestARunThatShowsNothingStands(t *testing.T) {
	home, repo := t.TempDir(), "naghuale-crewflow"
	started := monday.Add(8 * time.Hour)
	writeState(t, home, repo, 43, "a run that goes quiet", nil,
		try{startedAt: started, outcome: Running, pid: 100, lastAt: started, lastStep: "go test ./..."})
	at := started.Add(47 * time.Minute)

	queue := queueOf(t, home, repo, attentionEnvOf(at, 100), nil)

	one, wanted := queue.Wanted(43)
	if !wanted {
		t.Fatalf("the queue holds %+v, want the task 43 in it", tasksOfQueue(queue))
	}
	if one.State != AttentionStands {
		t.Errorf("the state = %q, want %q: nothing is known about the run, and it is not working", one.State, AttentionStands)
	}
	if one.Reason != ReasonNoProgress {
		t.Errorf("the reason = %q, want %q: the reason of the silence is unknown", one.Reason, ReasonNoProgress)
	}
	if one.LastStep != "go test ./..." {
		t.Errorf("the last step = %q, want the step the state of the task holds: it is all a person has", one.LastStep)
	}
	if one.Waited() != 47*time.Minute {
		t.Errorf("the run has been standing for %s, want 47m", Idle(one.Waited()))
	}
	if !one.Promoted {
		t.Error("the entry is not promoted, want it at the top: a run that stands always wants a person")
	}
}

// TestAQ001AKnownReasonIsNeverAStand: the rule the owner of 01.10 wrote out. A run that
// goes quiet in front of a window of the machine is waiting and not standing, whatever the
// length of the wait, and a timeout does not change the class of the wait either (AQ-001,
// AQ-008, ES-006, docs/DESIGN.md §6a, §7i).
func TestAQ001AKnownReasonIsNeverAStand(t *testing.T) {
	home, repo := t.TempDir(), "naghuale-crewflow"
	started := monday.Add(8 * time.Hour)
	// The run of the mode of the bot goes to the keychain for the key of the App of the
	// host, and the state of the task says what it stands at while it waits (§7i).
	writeState(t, home, repo, 41, "прогон у окна связки ключей", nil,
		try{startedAt: started, outcome: Running, pid: 100, lastAt: started,
			lastStep: stepIdentity, reason: reasonApproval})
	at := started.Add(2 * time.Minute)

	queue := queueOf(t, home, repo, attentionEnvOf(at, 100), nil)

	one, wanted := queue.Wanted(41)
	if !wanted {
		t.Fatalf("the queue holds %+v, want the task 41 in it: a run at a window needs a person", tasksOfQueue(queue))
	}
	if one.State == AttentionStands {
		t.Fatalf("the state = %q with the reason %q known, want a wait: AQ-001", one.State, one.Reason)
	}
	if one.Reason != ReasonHumanAuthorization {
		t.Errorf("the reason = %q, want %q: one reason for every channel of §7f", one.Reason, ReasonHumanAuthorization)
	}
	if one.Channel != ChannelKeychain {
		t.Errorf("the channel = %q, want %q", one.Channel, ChannelKeychain)
	}
	if one.Resource != SubjectExecutorKey || one.Action != ActionHumanExecute {
		t.Errorf("the request is about %q and asks %q, want the key of the App of the executor and human-execute",
			one.Resource, one.Action)
	}
	if one.NextActor != ActorOwner {
		t.Errorf("the next one = %q, want %q: only a person may answer the window of the system", one.NextActor, ActorOwner)
	}
	if one.Next != "crewflow task resume 41" {
		t.Errorf("what may be done = %q, want the command that goes on from the point the run stood at", one.Next)
	}
	// Two minutes later, with the window closed and nobody there: the wait is the same
	// wait, and the class of it has not become a stand (AQ-008, ES-006: ночной #41).
	after := at.Add(3 * time.Hour)
	one = attentionOnly(t, home, repo, attentionEnvOf(after, 100))
	if one.State == AttentionStands {
		t.Errorf("the state = %q after three hours at the window, want a wait: the reason is still known", one.State)
	}
}

// TestAWaitOfADecisionWithoutAChannelIsUnknown: a reason that says that a person is needed
// and does not say where does not become a window of the keychain. The queue says that a
// person is needed and that it does not know what is being asked of him, because a guessed
// channel is a window nobody will find (HA-004, docs/DESIGN.md §6a, §7e).
func TestAWaitOfADecisionWithoutAChannelIsUnknown(t *testing.T) {
	home, repo := t.TempDir(), "naghuale-crewflow"
	started := monday.Add(8 * time.Hour)
	writeState(t, home, repo, 42, "a run that waits for a person", nil,
		try{startedAt: started, outcome: Running, pid: 100, lastAt: started,
			lastStep: stepExecutor, reason: ReasonHumanAuthorization})
	at := started.Add(4 * time.Hour)

	one := attentionOnly(t, home, repo, attentionEnvOf(at, 100))

	if one.State != AttentionBlocked {
		t.Errorf("the state = %q, want %q", one.State, AttentionBlocked)
	}
	if one.Channel != "" || one.Resource != "" || one.Action != "" {
		t.Errorf("the request says channel %q, resource %q, action %q, want nothing: the state of the task does not name them",
			one.Channel, one.Resource, one.Action)
	}
	if one.Actable != ActUnknown {
		t.Errorf("the entry says %q, want %q: crewflow cannot say whether a person may act", one.Actable, ActUnknown)
	}
}

// TestAQ005TheOrderOfTheQueueIsTheOrderTheOwnerWrote: what has to be looked into first is
// first — a run that stands, because nobody knows what is happening to it — then what has
// been waiting for a person for longer than it should, and inside one state the entry that
// has waited longest comes first (AQ-005, docs/DESIGN.md §6a).
func TestAQ005TheOrderOfTheQueueIsTheOrderTheOwnerWrote(t *testing.T) {
	home, repo := t.TempDir(), "naghuale-crewflow"
	now := monday.Add(30 * time.Hour)
	quiet := now.Add(-90 * time.Minute)
	// A run that stands, a run that waits for a person, a change that waits for a review
	// and a run that ended with nobody looking at it.
	writeState(t, home, repo, 1, "стоит", nil,
		try{startedAt: quiet, outcome: Running, pid: 100, lastAt: quiet, lastStep: "go test ./..."})
	writeState(t, home, repo, 2, "ждёт владельца", &Change{Number: 12},
		try{startedAt: now.Add(-2 * time.Hour), endedAt: now.Add(-2 * time.Hour), outcome: ChangeRequestOpened})
	writeState(t, home, repo, 3, "ждёт ревью", &Change{Number: 13},
		try{startedAt: now.Add(-time.Hour), endedAt: now.Add(-time.Hour), outcome: ChangeRequestOpened})
	writeState(t, home, repo, 4, "завершён, не просмотрен", nil,
		try{startedAt: now.Add(-3 * time.Hour), endedAt: now.Add(-3 * time.Hour), outcome: TimedOut})
	head := "1111111111111111111111111111111111111111"
	host := &hostOfTest{facts: map[int]HostFacts{
		2: {Labels: []string{"owner-check"},
			Change: &ChangeFacts{Number: 12, State: "open", Head: head, Approved: true, Last: now.Add(-2 * time.Hour)}},
		3: {Change: &ChangeFacts{Number: 13, State: "open", Head: head}},
		4: {},
	}}

	queue := queueOf(t, home, repo, attentionEnvOf(now, 100), host)

	want := []int{1, 2, 3, 4}
	got := tasksOfQueue(queue)
	if len(got) != len(want) {
		t.Fatalf("the queue holds %v, want %v", got, want)
	}
	for i, number := range want {
		if got[i] != number {
			t.Errorf("the queue holds %v, want %v: what stands comes before what is waited for, and what is waited for before what is read",
				got, want)
			break
		}
	}
}

// TestTheWaitIsEscalatedOnceAndTheRecordIsNotRepeatedForADay: the promise of §6a to a
// person. A task that waits for longer than `[attention] escalate_after` is escalated and a
// record of it is left under the task once; a schedule that runs every minute leaves one
// line and not one a minute, and a change of the reason of the wait is a new line whatever
// the one before it said (F-061, docs/DESIGN.md §6a).
func TestTheWaitIsEscalatedOnceAndTheRecordIsNotRepeatedForADay(t *testing.T) {
	home, repo := t.TempDir(), "naghuale-crewflow"
	ended := monday.Add(8*time.Hour + 42*time.Minute)
	writeState(t, home, repo, 43, "очередь внимания", &Change{Number: 113, URL: "https://example.com/113"},
		try{startedAt: monday.Add(8 * time.Hour), endedAt: ended, outcome: ChangeRequestOpened})
	late := ended.Add(25 * time.Hour)
	said := &saidUnderTheTask{}
	env := attentionEnvOf(late)

	queue := queueOf(t, home, repo, env, nil, said.sayAttention)

	one, wanted := queue.Wanted(43)
	if !wanted {
		t.Fatalf("the queue holds %+v, want the task 43 in it", tasksOfQueue(queue))
	}
	if one.State != AttentionEscalated {
		t.Errorf("the state = %q after 25h, want %q: the waiting has outlasted what the project agreed to", one.State, AttentionEscalated)
	}
	if one.EscalatedFrom != AttentionFinishedUnseen {
		t.Errorf("the state it came from = %q, want %q: an escalation of a task nobody knows the cause of is about nothing",
			one.EscalatedFrom, AttentionFinishedUnseen)
	}
	if one.Priority != Critical {
		t.Errorf("the priority = %q, want %q", one.Priority, Critical)
	}
	if one.Reason != ReasonRunCompleted {
		t.Errorf("the reason = %q, want %q: the escalation keeps the cause of the wait", one.Reason, ReasonRunCompleted)
	}
	if !one.Said {
		t.Fatalf("the queue says it left nothing under the task: %q", one.Problem)
	}
	if len(said.lines) != 1 {
		t.Fatalf("the queue left %d records under the task, want one: %q", len(said.lines), said.lines)
	}
	if !strings.Contains(said.lines[0], "escalated of finished-unseen") ||
		!strings.Contains(said.lines[0], ReasonRunCompleted) {
		t.Errorf("the record under the task is\n%s\nwant the state, the reason and where the task is going", said.lines[0])
	}
	// The second turn of a schedule that ran a minute later writes nothing, and the state
	// of the task is where crewflow remembers that it wrote the first one (§6a).
	later := attentionEnvOf(late.Add(time.Minute))
	queue = queueOf(t, home, repo, later, nil, said.sayAttention)
	if one, _ := queue.Wanted(43); one.Said {
		t.Error("the second turn of the watch left a record under the task, want nothing: the key is the same")
	}
	if len(said.lines) != 1 {
		t.Fatalf("the queue left %d records under the task, want the one of the first turn: %q", len(said.lines), said.lines)
	}
	// A day later the same record may be left once more, and a change of the reason of
	// the wait is a new record whenever it happens.
	queue = queueOf(t, home, repo, attentionEnvOf(late.Add(25*time.Hour)), nil, said.sayAttention)
	if one, _ := queue.Wanted(43); !one.Said {
		t.Errorf("a day after the first record nothing was left, want the reminder: %q", one.Problem)
	}
	if len(said.lines) != 2 {
		t.Errorf("the queue left %d records after a day, want two: the first and the reminder", len(said.lines))
	}
}

// TestAChangeOfTheReasonIsANewRecord: the key of a record is the task, the state, the
// priority and the reason, and a change of any of them is a new key — a schedule that asks
// about a task every minute leaves one line for the wait it found and another for the one
// that replaced it (docs/DESIGN.md §6a).
func TestAChangeOfTheReasonIsANewRecord(t *testing.T) {
	home, repo := t.TempDir(), "naghuale-crewflow"
	ended := monday.Add(8*time.Hour + 42*time.Minute)
	writeState(t, home, repo, 43, "очередь внимания", nil,
		try{startedAt: monday.Add(8 * time.Hour), endedAt: ended, outcome: TimedOut})
	late := ended.Add(25 * time.Hour)
	said := &saidUnderTheTask{}

	queueOf(t, home, repo, attentionEnvOf(late), nil, said.sayAttention)
	if len(said.lines) != 1 {
		t.Fatalf("the queue left %d records, want one", len(said.lines))
	}
	// The run is continued, and the continuation is a refusal of the executor: the state
	// of the queue is another one, and it has its own line under the task.
	state, err := LoadState(newJournals(home, repo).StatePath(43))
	if err != nil {
		t.Fatalf("load the state of the task: %v", err)
	}
	state = state.NextAttempt(StartOf{Started: late, Step: stepBegan})
	state = state.Ended(late.Add(time.Hour), BlockedPermission)
	if err := SaveState(newJournals(home, repo).StatePath(43), state); err != nil {
		t.Fatalf("write the state of the task: %v", err)
	}

	queue := queueOf(t, home, repo, attentionEnvOf(late.Add(26*time.Hour)), nil, said.sayAttention)

	one, wanted := queue.Wanted(43)
	if !wanted {
		t.Fatalf("the queue holds %+v, want the task 43 in it", tasksOfQueue(queue))
	}
	if one.Reason != ReasonBlockedPermission {
		t.Errorf("the reason = %q, want %q: the refusal of the new try is a new reason", one.Reason, ReasonBlockedPermission)
	}
	if !one.Said {
		t.Errorf("nothing was left under the task, want a new record: %q", one.Problem)
	}
	if len(said.lines) != 2 {
		t.Errorf("the queue left %d records, want two: one for each reason of the wait", len(said.lines))
	}
}

// TestARunTheHostCouldNotBeReadIsNotAttention: F-098, 01.10. Прерванное чтение — не
// утверждение. Раньше запись, чей хостинг не прочитан, попадала в очередь как
// `escalated · finished-unseen` с причиной `context canceled`: восемь строк о давних
// слитых PR, поднятых после Ctrl+C, и очередь говорила, что восемь задач ждут человека
// именно потому, что его спросить не удалось.
//
// Теперь такая запись не запись очереди, а строка блока «не удалось прочитать»: то, что
// crewflow не знает, не выдаётся за то, что требует внимания (docs.DESIGN.md §6a, §7h).
func TestARunTheHostCouldNotBeReadIsNotAttention(t *testing.T) {
	home, repo := t.TempDir(), "naghuale-crewflow"
	ended := monday.Add(8 * time.Hour)
	writeState(t, home, repo, 43, "the run of a task", &Change{Number: 113},
		try{startedAt: monday, endedAt: ended, outcome: ChangeRequestOpened})
	said := &saidUnderTheTask{}

	queue := queueOf(t, home, repo, attentionEnvOf(ended.Add(66*time.Hour)), &hostOfTest{}, said.sayAttention)

	if len(queue.Entries) != 0 {
		t.Errorf("очередь держит %+v, want ничего: хостинг не прочитан", tasksOfQueue(queue))
	}
	if len(queue.Unread) != 1 {
		t.Fatalf("в блоке «не прочитано» %d строк, want одна: %+v", len(queue.Unread), queue.Unread)
	}
	unread := queue.Unread[0]
	if unread.Task != 43 || unread.Run != "43-1" {
		t.Errorf("в блоке %+v, want задачу 43 и прогон 43-1", unread)
	}
	if unread.Reason != ReasonReadFailed {
		t.Errorf("причина = %q, want %q", unread.Reason, ReasonReadFailed)
	}
	if unread.Problem == "" {
		t.Error("в строке нет проблемы, want она названа: человек должен знать, куда смотреть")
	}
	if len(said.lines) != 0 {
		t.Errorf("под задачей оставлено %q, want ничего: то, что не прочитано, не напоминают",
			said.lines)
	}
}

// TestACancelledReadIsNotAttentionAndIsSaidAsInterrupted: тот же случай по Ctrl+C. Отмена
// контекста — не отказ хостинга, и очередь говорит об этом отдельно: прочитанное не
// выдаётся за список требующего внимания, а прерванное чтение говорит, что оно прервано
// (docs.DESIGN.md §6a).
func TestACancelledReadIsNotAttentionAndIsSaidAsInterrupted(t *testing.T) {
	home, repo := t.TempDir(), "naghuale-crewflow"
	ended := monday.Add(8 * time.Hour)
	writeState(t, home, repo, 43, "the run of a task", &Change{Number: 113},
		try{startedAt: monday, endedAt: ended, outcome: ChangeRequestOpened})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	queue, err := CheckAttention(ctx, home, repo, attentionEnvOf(ended.Add(time.Hour)), &hostOfTest{}, nil)

	if err != nil {
		t.Fatalf("CheckAttention вернула ошибку: %v", err)
	}
	if len(queue.Entries) != 0 {
		t.Errorf("очередь держит %+v, want ничего: чтение прервано", tasksOfQueue(queue))
	}
	if !queue.Interrupted {
		t.Error("очередь не сказала, что чтение прервано, want она это говорит")
	}
	if len(queue.Unread) != 1 || queue.Unread[0].Reason != ReasonReadInterrupted {
		t.Errorf("блок «не прочитано» = %+v, want одна строка с причиной %q",
			queue.Unread, ReasonReadInterrupted)
	}
}

// TestAnEntryWithoutAHostIsWhatTheStateOfTheTaskIsEnoughFor: `crewflow task list` reads no
// tracker and no host (§6), and the queue it shows is worked out of the state of the tasks
// alone. An entry of it says what it knows and nothing more, and the change request of a
// run is what ties a task to a change (docs/DESIGN.md §6, §6a).
func TestAnEntryWithoutAHostIsWhatTheStateOfTheTaskIsEnoughFor(t *testing.T) {
	home, repo := t.TempDir(), "naghuale-crewflow"
	ended := monday.Add(8*time.Hour + 42*time.Minute)
	writeState(t, home, repo, 43, "the run of a task", &Change{Number: 113, URL: "https://example.com/113"},
		try{startedAt: monday.Add(8 * time.Hour), endedAt: ended, outcome: ChangeRequestOpened})
	at := ended.Add(time.Hour)

	one := attentionOnly(t, home, repo, attentionEnvOf(at))

	if one.State != AttentionFinishedUnseen {
		t.Errorf("the state = %q, want %q", one.State, AttentionFinishedUnseen)
	}
	if one.Channel != "" || one.Subject == "" {
		t.Errorf("the entry says channel %q and subject %q, want no channel and the change it waits for", one.Channel, one.Subject)
	}
	if !one.Promoted {
		t.Error("the entry is not promoted, want it at the top: the work is over and nobody has looked at it")
	}
}

// TestTheQueueIsWorkedOutAndNotKept: the state of a task says nothing of the queue, and the
// queue of a project is the same after it has been read as before. A queue kept beside the
// state says what it said when it was written and not what is true now (§6a, §7h).
func TestTheQueueIsWorkedOutAndNotKept(t *testing.T) {
	home, repo := t.TempDir(), "naghuale-crewflow"
	ended := monday.Add(8*time.Hour + 42*time.Minute)
	writeState(t, home, repo, 43, "the run of a task", &Change{Number: 113, URL: "https://example.com/113"},
		try{startedAt: monday.Add(8 * time.Hour), endedAt: ended, outcome: ChangeRequestOpened})
	before, err := LoadState(newJournals(home, repo).StatePath(43))
	if err != nil {
		t.Fatalf("load the state of the task: %v", err)
	}

	if _, err := AttentionQueue(t.Context(), home, repo, attentionEnvOf(ended.Add(25*time.Hour)), nil); err != nil {
		t.Fatalf("AttentionQueue returned an error: %v", err)
	}

	after, err := LoadState(newJournals(home, repo).StatePath(43))
	if err != nil {
		t.Fatalf("load the state of the task: %v", err)
	}
	if after.Notice != before.Notice {
		t.Errorf("the state of the task holds the notice %+v after the queue was read, want it as it was", after.Notice)
	}
	if len(after.Attempts) != len(before.Attempts) {
		t.Errorf("the queue changed the attempts of the task, want none of them touched")
	}
}

// TestTheBlockOfTheQueueIsAtTheTopOfAListOfRuns: a person opens a list of runs to see what
// wants them, and the block of the queue is the first thing in it — the state, the reason,
// what it waits for, how long, who acts next and what may be done about it (§6a).
func TestTheBlockOfTheQueueIsAtTheTopOfAListOfRuns(t *testing.T) {
	home, repo := t.TempDir(), "naghuale-crewflow"
	ended := monday.Add(8*time.Hour + 42*time.Minute)
	quiet := monday.Add(30*time.Hour - 47*time.Minute)
	writeState(t, home, repo, 41, "прогон, который молчит", nil,
		try{startedAt: quiet, outcome: Running, pid: 100, lastAt: quiet, lastStep: "bash: go test ./..."})
	writeState(t, home, repo, 43, "очередь внимания", &Change{Number: 113, URL: "https://example.com/113"},
		try{startedAt: monday.Add(8 * time.Hour), endedAt: ended, outcome: ChangeRequestOpened})
	at := monday.Add(30 * time.Hour)

	runs, err := List(home, repo, attentionEnvOf(at, 100).ListEnv)
	if err != nil {
		t.Fatalf("List returned an error: %v", err)
	}
	queue := queueOf(t, home, repo, attentionEnvOf(at, 100), nil)
	var out bytes.Buffer
	if err := runs.WithAttention(queue).Write(&out, screenAt(at)); err != nil {
		t.Fatalf("Write returned an error: %v", err)
	}
	said := out.String()
	for _, want := range []string{
		"ATTENTION REQUIRED",
		"#41", string(AttentionStands), ReasonNoProgress, "waiting 47m", "the last step: bash: go test ./...",
		"#43", string(AttentionFinishedUnseen), ReasonRunCompleted, "next: orchestrator", "crewflow review 113",
	} {
		if !strings.Contains(said, want) {
			t.Errorf("the list wrote\n%s\nwant it to hold %q", said, want)
		}
	}
	if strings.Index(said, "ATTENTION REQUIRED") > strings.Index(said, "TASK") {
		t.Errorf("the list wrote\n%s\nwant the block of the queue over the table of the runs", said)
	}
}

// TestTheQueueAsksTheHostAboutManyTasksAtATimeAndNotOneAfterAnother: скорость чтения.
// Восемь прогонов, каждому из которых нужна реакция хостинга, читались восемь раз
// подряд — по одному вопросу на задачу, и на живой машине это были минуты молчания
// (F-061, 01.10).
//
// Теперь вопросы идут пачками, и очередь не ждёт окончания одного, чтобы начать
// следующий: счётчик в тесте показывает, что одновременно в работе было больше одного
// вопроса, и что их было не больше предела. Предел существует потому, что хостинг —
// это чужой сервис, и вопрос к нему без счётчика одновременности есть способ уронить
// чужой проект (F-061, §6a).
func TestTheQueueAsksTheHostAboutManyTasksAtATimeAndNotOneAfterAnother(t *testing.T) {
	home, repo := t.TempDir(), "naghuale-crewflow"
	ended := monday.Add(8 * time.Hour)
	for task := 1; task <= 8; task++ {
		writeState(t, home, repo, task, "ждёт", &Change{Number: 100 + task},
			try{startedAt: monday, endedAt: ended, outcome: ChangeRequestOpened})
	}
	host := (&hostThatCounts{states: map[int]string{}}).gather(hostAtOnce)

	queue := queueOf(t, home, repo, attentionEnvOf(ended.Add(time.Hour)), host)

	if len(host.asked) != 8 {
		t.Errorf("the host was asked about %v, want all eight runs", host.asked)
	}
	if host.widest < 2 {
		t.Errorf("the host was asked %d at a time at the most, want more than one: "+
			"очередь не должна ждать окончания одного вопроса, чтобы задать следующий", host.widest)
	}
	if host.widest > hostAtOnce {
		t.Errorf("the host was asked %d at a time, want not more than %d: чужой хостинг "+
			"не терпит вопросов без предела", host.widest, hostAtOnce)
	}
	if len(queue.Entries) != 8 {
		t.Errorf("очередь держит %v, want все восемь записей: чтение пачками не теряет ни одной",
			tasksOfQueue(queue))
	}
}

// TestTheQueueRemembersThatTheWorkIsOverAndAsksNoMoreAboutIt: скорость и правда в одном
// правиле. Слитьё изменения — это факт хостинга, и очередь, спросившая о нём однажды,
// помнит его: расписание оркестратора ходит каждую минуту и не должна спрашивать про один
// и тот же слитый прогон снова (F-061, §6a).
//
// Память имеет срок: смена или задача могут быть открыты заново, и очередь узнаёт об этом
// только тем, что спрашивает. Через сутки она спрашивает и, если изменение открыто снова,
// показывает задачу в очереди — память о «завершено» не переживает правду хостинга.
func TestTheQueueRemembersThatTheWorkIsOverAndAsksNoMoreAboutIt(t *testing.T) {
	home, repo := t.TempDir(), "naghuale-crewflow"
	ended := monday.Add(8 * time.Hour)
	writeState(t, home, repo, 43, "the run of a task", &Change{Number: 113},
		try{startedAt: monday, endedAt: ended, outcome: ChangeRequestOpened})
	journals := newJournals(home, repo)
	head := "1111111111111111111111111111111111111111"
	// Первое чтение: хостинг говорит, что изменение слито, и очередь запоминает это.
	merged := &hostOfTest{facts: map[int]HostFacts{43: {Change: &ChangeFacts{Number: 113, State: "merged", Head: head}}}}
	queueOf(t, home, repo, attentionEnvOf(ended.Add(time.Hour)), merged)

	state, err := LoadState(journals.StatePath(43))
	if err != nil {
		t.Fatalf("load the state of the task: %v", err)
	}
	if state.Settled == nil || state.Settled.By != ReasonChangeMerged {
		t.Fatalf("the state holds %+v, want %q remembered: правда хостинга помнится, а не перечитывается",
			state.Settled, ReasonChangeMerged)
	}
	if !state.Settled.At.Equal(monday.Add(9 * time.Hour)) {
		t.Errorf("the state remembers it at %s, want 9h: when the host said it", state.Settled.At)
	}

	// Второе чтение, через минуту: хостинг молчит о слитых, потому что о нём не
	// спрашивают — и очередь всё равно не показывает прогон.
	silent := &hostThatCounts{}
	queue := queueOf(t, home, repo, attentionEnvOf(monday.Add(9*time.Hour+time.Minute)), silent)

	if len(silent.asked) != 0 {
		t.Errorf("the host was asked about %v, want nobody: слитое помнится", silent.asked)
	}
	if _, inQueue := queue.Wanted(43); inQueue {
		t.Errorf("the queue holds %v, want nothing: the change went in", tasksOfQueue(queue))
	}

	// Через сутки память протухает, и очередь спрашивает хостинг снова: изменение могли
	// открыть заново.
	reopened := &hostOfTest{facts: map[int]HostFacts{43: {Change: &ChangeFacts{Number: 113, State: "open", Head: head}}}}
	queue = queueOf(t, home, repo, attentionEnvOf(monday.Add(9*time.Hour+25*time.Hour)), reopened)

	if len(reopened.asked) != 1 {
		t.Errorf("the host was asked about %v, want once: память о завершённом имеет срок", reopened.asked)
	}
	one, wanted := queue.Wanted(43)
	if !wanted {
		t.Fatalf("the queue holds %v, want the task 43: изменение открыто снова, и это правда хостинга",
			tasksOfQueue(queue))
	}
	if one.EscalatedFrom != AttentionAwaitsReview {
		t.Errorf("the state = %q of %q, want the reopened change waiting for a review like any other",
			one.State, one.EscalatedFrom)
	}
}

// TestTheQueueRemembersThatTheHostClosedTheTaskToo: закрытая хостингом задача — тот же
// факт и та же память. «Задача закрыта» и «изменение слито» — два разных события, и
// очередь помнит, какое именно (docs.DESIGN.md §6a, §7g).
func TestTheQueueRemembersThatTheHostClosedTheTaskToo(t *testing.T) {
	home, repo := t.TempDir(), "naghuale-crewflow"
	ended := monday.Add(8 * time.Hour)
	writeState(t, home, repo, 43, "the run of a task", &Change{Number: 113},
		try{startedAt: monday, endedAt: ended, outcome: ChangeRequestOpened})
	journals := newJournals(home, repo)

	queueOf(t, home, repo, attentionEnvOf(ended.Add(time.Hour)),
		&hostOfTest{facts: map[int]HostFacts{43: {Closed: true}}})

	state, err := LoadState(journals.StatePath(43))
	if err != nil {
		t.Fatalf("load the state of the task: %v", err)
	}
	if state.Settled == nil || state.Settled.By != ReasonTaskClosed {
		t.Errorf("the state holds %+v, want %q remembered", state.Settled, ReasonTaskClosed)
	}
}

// TestTheStateOfATaskRemembersTheRecordThatWasLeft: what crewflow has already said under a
// task is a fact of the process, and it is what keeps one record to a key and a day. A
// state of before crewflow kept the notice names none, and a task nobody has been told
// about is a task the record is due for (§6a, §7h).
func TestTheStateOfATaskRemembersTheRecordThatWasLeft(t *testing.T) {
	state, err := LoadState(newJournals(t.TempDir(), "naghuale-crewflow").StatePath(1))
	if err == nil {
		t.Fatalf("the state of a task that was never run was read: %+v", state)
	}
	var before State
	if before.NoticedWithin(monday, time.Hour, "43/awaits-review/normal/review-required") {
		t.Error("a task nobody has been told about holds a record, want none")
	}
	after := before.Noticed(monday, "43/awaits-review/normal/review-required")
	if !after.NoticedWithin(monday.Add(time.Minute), 24*time.Hour, "43/awaits-review/normal/review-required") {
		t.Error("a record left a minute ago is not in the state of the task, want it there")
	}
	if after.NoticedWithin(monday.Add(25*time.Hour), 24*time.Hour, "43/awaits-review/normal/review-required") {
		t.Error("a record left a day and an hour ago still counts, want the reminder to be due")
	}
	if after.NoticedWithin(monday.Add(time.Minute), 24*time.Hour, "43/escalated/critical/review-required") {
		t.Error("a record of another key counts for this one, want a new record to be due")
	}
	// A state of a task survives the write of the queue: the notice is a part of the
	// state and is read back as one.
	home, repo := t.TempDir(), "naghuale-crewflow"
	writeState(t, home, repo, 43, "the run of a task", nil,
		try{startedAt: monday, endedAt: monday.Add(time.Hour), outcome: TimedOut})
	key := (&Attention{Task: 43, State: AttentionEscalated, Priority: Critical, Reason: ReasonRunTimeout}).Key()
	if err := SaveState(newJournals(home, repo).StatePath(43), after2(monday, key)); err != nil {
		t.Fatalf("write the state of the task: %v", err)
	}
	kept, err := LoadState(newJournals(home, repo).StatePath(43))
	if err != nil {
		t.Fatalf("load the state of the task: %v", err)
	}
	if kept.Notice == nil || kept.Notice.Key != key || !kept.Notice.At.Equal(monday) {
		t.Errorf("the state of the task holds the notice %+v, want the key and the moment of it", kept.Notice)
	}
}

// TestTheEntryOfAListCarriesTheStateOfTheAttention: a program asks a list of runs for the
// same queue a person reads at the top of it, and the fields it needs are the state of the
// process, the reason of it, who acts next, the priority and how long it has been that way
// (docs/DESIGN.md §6a).
func TestTheEntryOfAListCarriesTheStateOfTheAttention(t *testing.T) {
	ended := monday.Add(8*time.Hour + 42*time.Minute)
	one := Attention{
		Task: 43, Title: "the run of a task", Run: "43-1",
		State: AttentionAwaitsOwner, Reason: ReasonOwnerAcceptance, Subject: SubjectOwner,
		NextActor: ActorOwner, Priority: High, Actable: ActNow, Since: ended,
		WaitingSeconds: 3600, Next: "the decision of the owner under #113",
		Outcome: ChangeRequestOpened, Change: &Change{Number: 113},
	}
	entry := Entry{
		Repo: "naghuale-crewflow", Task: 43, Title: "the run of a task", Attempts: 1, Attempt: 1,
		Outcome: ChangeRequestOpened, StartedAt: ended, EndedAt: &ended, Change: one.Change,
		Attention: &one,
	}

	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal the entry: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("read the answer of the list: %v", err)
	}
	for key, want := range map[string]any{
		"attention_state": string(AttentionAwaitsOwner),
		"reason":          ReasonOwnerAcceptance,
		"next_actor":      ActorOwner,
		"priority":        string(High),
		"actable":         string(ActNow),
		"subject":         SubjectOwner,
		"next":            one.Next,
		"waiting_seconds": 3600.0,
	} {
		if said := got[key]; said != want {
			t.Errorf("the answer holds %s = %v, want %v", key, said, want)
		}
	}
	since, is := got["waiting_since"].(string)
	if !is || !strings.HasPrefix(since, "2026-09-") {
		t.Errorf("the answer holds waiting_since = %v, want the moment the state began", got["waiting_since"])
	}
}

// TestTheHostIsAskedOnlyWhereAWaitMayBeCleared: a queue that asked the host about every
// task of the project would put a network in the middle of a list of runs, and a task that
// wants nobody has nothing to ask about. A run that is going is not asked about either:
// nobody reacts to a change while the run that opened it works (§6, §6a).
//
// The questions now go in batches and their order is whatever the host answers first, so the
// test compares the set: порядок вопросов ничего не значит для того, о чём их спрашивали.
func TestTheHostIsAskedOnlyWhereAWaitMayBeCleared(t *testing.T) {
	home, repo := t.TempDir(), "naghuale-crewflow"
	ended := monday.Add(8 * time.Hour)
	// A run that ended with its change request, a run that ended without one, a run that is
	// going, and a run that worked out.
	writeState(t, home, repo, 43, "opened a change", &Change{Number: 113},
		try{startedAt: monday, endedAt: ended, outcome: ChangeRequestOpened})
	writeState(t, home, repo, 44, "opened nothing", nil,
		try{startedAt: monday, endedAt: ended, outcome: TimedOut})
	writeState(t, home, repo, 45, "is going", nil,
		try{startedAt: monday, outcome: Running, pid: 100, lastAt: ended, lastStep: stepExecutor})
	writeState(t, home, repo, 46, "worked out", nil,
		try{startedAt: monday, endedAt: ended, outcome: ChangeRequestOpened})
	host := &hostThatCounts{}

	queueOf(t, home, repo, attentionEnvOf(ended.Add(time.Minute), 100), host)

	if !slices.Equal(slices.Sorted(slices.Values(host.asked)), []int{43, 44}) {
		t.Errorf("the host was asked about %v, want the tasks 43 and 44: a reaction may clear what has ended, "+
			"and a run that is going or a task that wants nobody is not read from the host", host.asked)
	}
	changes := slices.Sorted(slices.Values(host.changes))
	if !slices.Equal(changes, []int{0, 113}) {
		t.Errorf("the host was asked about the changes %v, want 113 and nothing: a run that opened no change "+
			"has no change to ask about, only a task", changes)
	}
}

// TestAHostThatCannotBeReadSaysSo: a queue that says "nobody is waiting" because the host
// was out of reach would be lying about the work of the project, and a queue that said
// "someone is waiting" would lie about it in the other direction. Neither: the run goes
// into the "could not read" block with what is known about it and what stood in the way
// (§7h, docs.DESIGN.md §6a, F-098).
func TestAHostThatCannotBeReadSaysSo(t *testing.T) {
	home, repo := t.TempDir(), "naghuale-crewflow"
	ended := monday.Add(8 * time.Hour)
	writeState(t, home, repo, 43, "the run of a task", &Change{Number: 113},
		try{startedAt: monday, endedAt: ended, outcome: ChangeRequestOpened})

	queue := queueOf(t, home, repo, attentionEnvOf(ended.Add(time.Hour)), &hostOfTest{})

	if len(queue.Entries) != 0 {
		t.Errorf("the queue holds %v, want nothing: the host was not read", tasksOfQueue(queue))
	}
	if len(queue.Unread) != 1 {
		t.Fatalf("the block holds %+v, want one line", queue.Unread)
	}
	unread := queue.Unread[0]
	if !strings.Contains(unread.Problem, "knows no task 43") {
		t.Errorf("the block says %q, want the problem of the host: a queue that hides a host it could not "+
			"read is a queue a person acts on with half the truth", unread.Problem)
	}
	if unread.Run != "43-1" {
		t.Errorf("the block names the run %q, want 43-1: a line has to say what it is about", unread.Run)
	}
}

// TestAProjectThatHasRunNothingHasNoQueue: a machine crewflow has run nothing in has no
// state of that project, and nothing of it wants anybody — which is an answer and not an
// error about the machine (§6, §6a).
func TestAProjectThatHasRunNothingHasNoQueue(t *testing.T) {
	queue, err := AttentionQueue(t.Context(), t.TempDir(), "naghuale-crewflow", attentionEnvOf(monday), nil)
	if err != nil {
		t.Fatalf("AttentionQueue returned an error: %v", err)
	}
	if len(queue.Entries) != 0 {
		t.Errorf("the queue holds %+v, want nothing at all", queue.Entries)
	}
}

// TestAStateCrewflowCannotReadIsNamed: one state file of a task that cannot be read does
// not take the tasks beside it down with it, and the file that could not be read is named
// so that a person can open it by hand (§6, §6a).
func TestAStateCrewflowCannotReadIsNamed(t *testing.T) {
	home, repo := t.TempDir(), "naghuale-crewflow"
	ended := monday.Add(8 * time.Hour)
	writeState(t, home, repo, 43, "the run of a task", nil,
		try{startedAt: monday, endedAt: ended, outcome: TimedOut})
	broken := newJournals(home, repo).StatePath(44)
	if err := writeFile(broken, []byte("{not a state of a task")); err != nil {
		t.Fatalf("write the state of the task 44: %v", err)
	}

	queue := queueOf(t, home, repo, attentionEnvOf(ended.Add(time.Hour)), nil)

	if _, wanted := queue.Wanted(43); !wanted {
		t.Errorf("the queue holds %v, want the task 43: one state that cannot be read takes nothing down with it", tasksOfQueue(queue))
	}
	if len(queue.Unreadable) != 1 || !strings.HasSuffix(queue.Unreadable[0], "44.json") {
		t.Errorf("the queue names %v as unreadable, want the state of the task 44", queue.Unreadable)
	}
}

// TestTheQueueOfAProjectThatIsOverHasNoEntryInIt: a run that came out of it well asks for
// nobody, however long ago it was, and a change that crewflow merged itself is the
// reaction the queue waits for (§6a, §7h).
func TestTheQueueOfAProjectThatIsOverHasNoEntryInIt(t *testing.T) {
	home, repo := t.TempDir(), "naghuale-crewflow"
	ended := monday.Add(8 * time.Hour)
	writeState(t, home, repo, 43, "merged", &Change{Number: 113},
		try{startedAt: monday, endedAt: ended, outcome: ChangeRequestOpened})
	merged, err := LoadState(newJournals(home, repo).StatePath(43))
	if err != nil {
		t.Fatalf("load the state of the task: %v", err)
	}
	if err := SaveState(newJournals(home, repo).StatePath(43),
		merged.Ended(ended.Add(time.Minute), ChangeRequestOpened)); err != nil {
		t.Fatalf("write the state of the task: %v", err)
	}
	state, err := LoadState(newJournals(home, repo).StatePath(43))
	if err != nil {
		t.Fatalf("load the state of the task: %v", err)
	}
	state.MergedSHA = "abc123"
	if err := SaveState(newJournals(home, repo).StatePath(43), state); err != nil {
		t.Fatalf("write the state of the task: %v", err)
	}

	queue := queueOf(t, home, repo, attentionEnvOf(ended.Add(25*time.Hour)), nil)

	if _, wanted := queue.Wanted(43); wanted {
		t.Errorf("the queue holds %+v, want nothing: crewflow merged the change itself", tasksOfQueue(queue))
	}
}

// TestTheBlockOfTheQueueSaysNothingWhereNobodyWaits: a list of runs of a project where
// everything is working has no block over the table, and a block about nothing is worse
// than no block (§6a).
func TestTheBlockOfTheQueueSaysNothingWhereNobodyWaits(t *testing.T) {
	home, repo := t.TempDir(), "naghuale-crewflow"
	started := monday.Add(8 * time.Hour)
	writeState(t, home, repo, 43, "a run that works", nil,
		try{startedAt: started, outcome: Running, pid: 100, lastAt: started, lastStep: stepExecutor})
	at := started.Add(time.Minute)

	runs, err := List(home, repo, attentionEnvOf(at, 100).ListEnv)
	if err != nil {
		t.Fatalf("List returned an error: %v", err)
	}
	queue := queueOf(t, home, repo, attentionEnvOf(at, 100), nil)
	var out bytes.Buffer
	if err := runs.WithAttention(queue).Write(&out, screenAt(at)); err != nil {
		t.Fatalf("Write returned an error: %v", err)
	}
	if strings.Contains(out.String(), "ATTENTION REQUIRED") {
		t.Errorf("the list wrote\n%s\nwant no block of a queue where nobody waits", out.String())
	}
}

// after2 is the state of a task with the notice of a record written into it, for the test
// of what the state holds and what it does not.
func after2(at time.Time, key string) State {
	var state State
	state.Number = 43
	state.Title = "the run of a task"
	state = state.NextAttempt(StartOf{Started: at, Step: stepBegan})
	state = state.Ended(at.Add(time.Hour), TimedOut)
	return state.Noticed(at, key)
}

// attentionOf is the machine and the file of a project for the queue of attention: the clock,
// the question whether a run is going, and the thresholds of §6a — the silence of the
// executor (§7a) and the three of `[attention]`.
func attentionEnvOf(now time.Time, going ...int) AttentionEnv {
	env := AttentionEnv{ListEnv: listOf(now, going...)}
	env.StallAfter = 10 * time.Minute
	env.TopAfter = 30 * time.Minute
	env.EscalateAfter = 24 * time.Hour
	env.RemindAfter = 24 * time.Hour
	env.WeeklyAfter = 7 * 24 * time.Hour
	env.AcceptanceLabel = "owner-check"
	return env
}

// queueOf is the queue of attention of a project as a command reads it, with the records it
// leaves under the tasks of it, and it fails the test where the queue could not be worked
// out at all.
func queueOf(t *testing.T, home, repo string, env AttentionEnv, host Host, said ...SayAttention) Queue {
	t.Helper()
	var under SayAttention
	if len(said) > 0 {
		under = said[0]
	}
	queue, err := CheckAttention(t.Context(), home, repo, env, host, under)
	if err != nil {
		t.Fatalf("CheckAttention returned an error: %v", err)
	}
	return queue
}

// TestTheQueueSaysWhatItCouldNotReadAndDoesNotPutItInTheBlockOfAttention: F-098 на живом
// прогоне. Восемь строк `escalated · finished-unseen` с причиной `context canceled` после
// Ctrl+C читались как «восемь задач требуют внимания», и повторный запуск через три минуты
// говорил «nothing wants a person» — очередь противоречила сама себе.
//
// Теперь блок внимания не содержит ничего о непрочитанном, а рядом стоит отдельный блок с
// числом таких прогонов и их причинами. Человек, читающий очередь, видит и то, что ждёт
// его, и то, что crewflow не смог прочитать (docs.DESIGN.md §6a).
func TestTheQueueSaysWhatItCouldNotReadAndDoesNotPutItInTheBlockOfAttention(t *testing.T) {
	home, repo := t.TempDir(), "naghuale-crewflow"
	ended := monday.Add(8 * time.Hour)
	// Два прогона, которых никто не смотрел, и один, который стоит: стоящий виден и без
	// хостинга, а остальные два — нет.
	writeState(t, home, repo, 1, "стоит", nil,
		try{startedAt: ended.Add(-47 * time.Minute), outcome: Running, pid: 100,
			lastAt: ended.Add(-47 * time.Minute), lastStep: "go test ./..."})
	writeState(t, home, repo, 2, "слит давно", &Change{Number: 21},
		try{startedAt: monday, endedAt: ended, outcome: ChangeRequestOpened})
	writeState(t, home, repo, 3, "слит позже", &Change{Number: 22},
		try{startedAt: monday, endedAt: ended, outcome: ChangeRequestOpened})

	queue := queueOf(t, home, repo, attentionEnvOf(ended.Add(time.Hour), 100), &hostOfTest{})

	var out bytes.Buffer
	if err := queue.Write(&out, screenAt(ended.Add(time.Hour))); err != nil {
		t.Fatalf("Write вернул ошибку: %v", err)
	}
	said := out.String()
	if !strings.Contains(said, string(AttentionStands)) || !strings.Contains(said, "#1") {
		t.Errorf("очередь написала %q, want #1 стоит: то, что видно без хостинга, остаётся видно", said)
	}
	for _, unwanted := range []string{"#2", "#3", string(AttentionEscalated)} {
		if strings.Contains(said, unwanted) {
			t.Errorf("блок внимания написал %q, want без %q: непрочитанное не является вниманием", said, unwanted)
		}
	}
	var block bytes.Buffer
	if err := queue.Unread.Write(&block, screenAt(ended.Add(time.Hour))); err != nil {
		t.Fatalf("Write of the unread block returned an error: %v", err)
	}
	for _, want := range []string{unreadHeading, "#2 (2-1)", "#3 (3-1)", ReasonReadFailed} {
		if !strings.Contains(block.String(), want) {
			t.Errorf("блок «не прочитано» = %q, want %q", block.String(), want)
		}
	}
}

// attentionOnly is the one entry of the queue of a project, and it fails the test where the
// queue holds nothing.
func attentionOnly(t *testing.T, home, repo string, env AttentionEnv, host ...Host) Attention {
	t.Helper()
	var asked Host
	if len(host) > 0 {
		asked = host[0]
	}
	queue := queueOf(t, home, repo, env, asked)
	if len(queue.Entries) != 1 {
		t.Fatalf("the queue holds %v, want one task in it", tasksOfQueue(queue))
	}
	return queue.Entries[0]
}

// tasksOfQueue is the numbers of the tasks of a queue, in the order of it, which is the
// order a person reads them in (AQ-005).
func tasksOfQueue(queue Queue) []int {
	tasks := make([]int, 0, len(queue.Entries))
	for _, one := range queue.Entries {
		tasks = append(tasks, one.Task)
	}
	return tasks
}

// hostOfTest is the host of a project in a test: what it says about the task with that
// number, and nothing about any other. It remembers which tasks it was asked about, because
// «спросили или нет» is a part of what the queue promises: помнит ли она, что хостинг уже
// сказал (F-061, F-105, §6a).
type hostOfTest struct {
	mu    sync.Mutex
	facts map[int]HostFacts
	asked []int
}

// FactsOf is what the host of the test says about a task. A task it knows nothing about is
// a task the host could not be read for, and the queue is told so rather than concluding
// that the task wants nobody.
//
// Очередь спрашивает хостинг пачками и сразу несколькими вопросами, и замок здесь —
// потому, что список заданных вопросов пишут из нескольких горутин сразу (§6a).
func (h *hostOfTest) FactsOf(_ context.Context, task, _ int) (HostFacts, error) {
	h.mu.Lock()
	h.asked = append(h.asked, task)
	h.mu.Unlock()
	facts, known := h.facts[task]
	if !known {
		return HostFacts{}, fmt.Errorf("the host of the test knows no task %d", task)
	}
	return facts, nil
}

// hostThatCounts is the host of a project that remembers which tasks it was asked about
// and which change request it was asked about with each of them.
type hostThatCounts struct {
	asked   []int
	changes []int
	// states is what the change of each task stands as, and everything is «open» where it
	// is not named: тест о чтении пачками считает вопросы, а не состояния (§6a).
	states map[int]string
	// going и widest — сколько вопросов было в работе одновременно и сколько их было
	// больше всего: по ним видно, читала очередь по одному или пачками.
	mu     sync.Mutex
	going  int
	widest int
	// waitFor сколько вопросов должно висеть одновременно, чтобы тест поверил, что
	// очередь читает пачками: каждый вопрос ждёт, пока столько их соберётся, иначе
	// одновременность была бы случайной и тест ничего бы не проверял (§6a).
	waitFor int
}

// gather is how many questions a test wants to see hanging at once, and how long a
// question of it may wait for the others before it is answered anyway: a queue that reads
// one by one will answer every question of it alone, and the test sees widest == 1.
func (h *hostThatCounts) gather(want int) *hostThatCounts {
	h.waitFor = want
	return h
}

// FactsOf is one task asked of the host: the change request of the run where there is one,
// and nothing about the change where the run opened none. It counts what it was asked and
// how many вопросов висело на нём одновременно (docs.DESIGN.md §6a).
func (h *hostThatCounts) FactsOf(_ context.Context, task, change int) (HostFacts, error) {
	h.enter()
	defer h.leave()
	h.mu.Lock()
	h.asked, h.changes = append(h.asked, task), append(h.changes, change)
	h.mu.Unlock()
	if change == 0 {
		return HostFacts{}, nil
	}
	state, is := h.states[change]
	if !is {
		state = "open"
	}
	return HostFacts{Change: &ChangeFacts{Number: change, State: state, Head: "abc"}}, nil
}

// enter и leave — счётчик вопросов в работе: widest remembers the most that were asked at
// once, which is what tells a queue that reads by batches from one that reads one by one.
func (h *hostThatCounts) enter() {
	waited := 0
	h.mu.Lock()
	h.going++
	h.widest = max(h.widest, h.going)
	// Ждать остальных, пока не собралось столько, сколько тест хочет видеть, — но не
	// вечно: очередь, которая читает по одному, ответит каждый вопрос сам по себе, и
	// тест увидитwidest == 1 вместо того, чтобы провиснуть.
	for h.going < h.waitFor {
		waited++
		if waited > 500 {
			break
		}
		h.mu.Unlock()
		time.Sleep(time.Millisecond)
		h.mu.Lock()
	}
	h.mu.Unlock()
}

func (h *hostThatCounts) leave() {
	h.mu.Lock()
	h.going--
	h.mu.Unlock()
}

// saidUnderTheTask is the records a queue left under the tasks of a project in a test: the
// lines themselves, in the order they were written, and the notice of a host that left them
// all. It is a value and not a function, so that a test can read what was written after the
// queue was worked out.
type saidUnderTheTask struct {
	lines []string
}

// sayAttention is what the queue asks of the host about a notice: the line is kept, and the
// notice is said to have been left.
func (s *saidUnderTheTask) sayAttention(_ context.Context, one Attention) (bool, string) {
	s.lines = append(s.lines, NoticeUnder(one))
	return true, ""
}
