package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/gate"
	taskrun "github.com/naghuale/crewflow/internal/run"
)

// The queue of attention of a project as a person and a schedule of an orchestrator read
// it: the tasks that want a person, the state of the process of each of them, the reason,
// what it waits for, who acts next, how long it has been that way and what may be done
// about it (docs/DESIGN.md §6a).

// TestRunTaskAttentionSaysWhatWantsAPerson: the command of the schedule. A run that has
// shown nothing for longer than the silence of the project is in the queue as `stands` with
// its last step, and the code of the command is not zero while anything in the queue wants a
// person — that is how a schedule knows there is somebody to call (docs/DESIGN.md §6a, §7a).
func TestRunTaskAttentionSaysWhatWantsAPerson(t *testing.T) {
	host := &host{opened: true, task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	standing := putRunThatStands(t, host)
	var stdout, stderr bytes.Buffer

	code := run([]string{"task", "attention", "-config", project}, &stdout, &stderr)

	if code != exitFailure {
		t.Fatalf("crewflow task attention = %d, want %d (stderr: %q)", code, exitFailure, stderr.String())
	}
	for _, want := range []string{
		"ATTENTION REQUIRED", "#43", string(taskrun.AttentionStands), taskrun.ReasonNoProgress,
		"waiting 11m", "the last step: the executor of the run", "next: orchestrator",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("crewflow task attention wrote %q, want it to mention %q", stdout.String(), want)
		}
	}
	if stderr.Len() != 0 {
		t.Errorf("crewflow task attention wrote %q to stderr, want nothing", stderr.String())
	}

	// A run that works asks for nobody, and a queue with nothing in it says so rather
	// than showing a block about nothing.
	putJournalAt(t, host.home+"/runs/naghuale-crewflow/43-1.jsonl",
		`{"type":"text","sessionID":"ses_7fKq2","part":{"type":"text","text":"I did the work."}}`+"\n",
		standing.Add(12*time.Minute))
	taskClock = func() time.Time { return standing.Add(12 * time.Minute) }
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"task", "attention", "-config", project}, &stdout, &stderr); code != exitOK {
		t.Fatalf("crewflow task attention = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	if strings.Contains(stdout.String(), "ATTENTION REQUIRED") {
		t.Errorf("crewflow task attention wrote %q, want no block where nobody waits", stdout.String())
	}
	if !strings.Contains(stdout.String(), "nothing wants a person") {
		t.Errorf("crewflow task attention wrote %q, want it to say that nothing waits", stdout.String())
	}
}

// TestRunTaskAttentionLeavesOneRecordUnderAnEscalatedTask: the promise of §6a to a person
// who reads a task. A task that has waited for longer than `[attention] escalate_after` is
// written about under its own task once, the next turn of the schedule a minute later says
// the same thing and writes nothing more, and a change of the reason of the wait is a new
// line whatever the one before it said (F-061, docs/DESIGN.md §6a).
func TestRunTaskAttentionLeavesOneRecordUnderAnEscalatedTask(t *testing.T) {
	host := &host{opened: true, task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	ended := putRunThatEnded(t, host, time.Now().Add(-25*time.Hour))
	taskClock = func() time.Time { return ended.Add(25 * time.Hour) }
	var stdout, stderr bytes.Buffer

	code := run([]string{"task", "attention", "-config", project}, &stdout, &stderr)

	if code != exitFailure {
		t.Fatalf("crewflow task attention = %d, want %d (stderr: %q)", code, exitFailure, stderr.String())
	}
	if !strings.Contains(stdout.String(), string(taskrun.AttentionEscalated)) {
		t.Errorf("crewflow task attention wrote %q, want the task escalated", stdout.String())
	}
	if len(host.records) != 1 {
		t.Fatalf("the host was asked %d times to write under the task, want once: %q", len(host.records), host.records)
	}
	for _, want := range []string{"#43", "escalated", taskrun.ReasonReviewRequired, "crewflow review"} {
		if !strings.Contains(host.records[0], want) {
			t.Errorf("the record under the task is %q, want it to hold %q", host.records[0], want)
		}
	}

	// The next turn of the same schedule: the same answer and no second line under the
	// task. A person who reads a task is not to find a line under it every minute (§6a).
	taskClock = func() time.Time { return ended.Add(25*time.Hour + time.Minute) }
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"task", "attention", "-config", project}, &stdout, &stderr); code != exitFailure {
		t.Fatalf("the second crewflow task attention = %d, want %d", code, exitFailure)
	}
	if len(host.records) != 1 {
		t.Errorf("the host was asked %d times to write under the task, want once: %q", len(host.records), host.records)
	}
}

// TestRunTaskAttentionJSON is the same answer for a program: the state of the process, the
// reason of it, who acts next, the priority and how long the task has been that way
// (docs/DESIGN.md §6a).
func TestRunTaskAttentionJSON(t *testing.T) {
	host := &host{opened: true, task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	ended := putRunThatEnded(t, host, time.Now().Add(-2*time.Hour))
	taskClock = func() time.Time { return ended.Add(2 * time.Hour) }
	var stdout, stderr bytes.Buffer

	run([]string{"task", "attention", "-config", project, "-json"}, &stdout, &stderr)

	var answer taskrun.Queue
	if err := json.Unmarshal(stdout.Bytes(), &answer); err != nil {
		t.Fatalf("crewflow task attention -json wrote %q, which is not an answer: %v", stdout.String(), err)
	}
	if len(answer.Entries) != 1 {
		t.Fatalf("the answer holds %+v, want the one task that wants a person", answer.Entries)
	}
	one := answer.Entries[0]
	switch {
	case one.Task != 43:
		t.Errorf("the answer is about the task %d, want 43", one.Task)
	case one.State != taskrun.AttentionAwaitsReview:
		t.Errorf("the state = %q, want %q: the change of the run is open and nobody has reviewed it",
			one.State, taskrun.AttentionAwaitsReview)
	case one.Reason != taskrun.ReasonReviewRequired:
		t.Errorf("the reason = %q, want %q", one.Reason, taskrun.ReasonReviewRequired)
	case one.NextActor != taskrun.ActorOrchestrator:
		t.Errorf("the next one = %q, want %q", one.NextActor, taskrun.ActorOrchestrator)
	case one.Priority != taskrun.Normal:
		// A change that waits for a review is a usual piece of the cycle and not a run
		// that went wrong: the table of §6a gives it an ordinary priority, and the
		// escalation of §6a is what makes it loud (§6a).
		t.Errorf("the priority = %q, want %q", one.Priority, taskrun.Normal)
	case one.Waited() != 2*time.Hour:
		t.Errorf("the task has been waiting for %s, want 2h", taskrun.Idle(one.Waited()))
	case one.Next != "crewflow review 44":
		t.Errorf("what may be done = %q, want the review of #44", one.Next)
	}
}

// TestRunTaskAttentionLeavesTheQueueWhenTheChangeWasReviewed: the reaction of a person is
// what clears the wait, and the queue is worked out of the records the host holds: a
// reviewed change of a task that asks for no acceptance is not waited for by anybody, and a
// reviewed change of a task marked `owner-check` waits for the owner (docs/DESIGN.md §6a,
// §7h).
func TestRunTaskAttentionLeavesTheQueueWhenTheChangeWasReviewed(t *testing.T) {
	owner := forge.Subject{Kind: forge.KindUser, Login: "naghuale", ID: int64(len("naghuale"))}
	for _, tc := range []struct {
		name   string
		labels []string
		want   string
	}{
		{
			name: "a change nobody looks at any more",
			want: "",
		},
		{
			name:   "a reviewed change of a task the owner has to accept",
			labels: []string{"owner-check"},
			want:   string(taskrun.AttentionAwaitsOwner),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := &host{opened: true, task: taskOf(43)}
			host.task.Labels = tc.labels
			host.under = []forge.Comment{{
				Author:    owner,
				CreatedAt: time.Now().Add(-time.Hour),
				Body:      gate.ApprovedOf("9f1c0de"),
			}}
			if tc.want == string(taskrun.AttentionAwaitsOwner) {
				// The owner has not taken the result in, and the wait is his: the
				// `ACCEPTED <sha>` of an owner is the reaction that clears it (§7h).
				host.under = append(host.under, forge.Comment{
					Author:    forge.Subject{Kind: forge.KindApp, ID: 42},
					CreatedAt: time.Now().Add(-time.Hour),
					Body:      "crewflow is looking at the text",
				})
			}
			host.use(t)
			project := host.config(t)
			ended := putRunThatEnded(t, host, time.Now().Add(-2*time.Hour))
			taskClock = func() time.Time { return ended.Add(2 * time.Hour) }
			var stdout, stderr bytes.Buffer

			code := run([]string{"task", "attention", "-config", project}, &stdout, &stderr)

			switch {
			case tc.want == "" && code != exitOK:
				t.Fatalf("crewflow task attention = %d, want %d: a reviewed change waits for nobody (stdout: %q, stderr: %q)",
					code, exitOK, stdout.String(), stderr.String())
			case tc.want != "" && code != exitFailure:
				t.Fatalf("crewflow task attention = %d, want %d (stdout: %q, stderr: %q)", code, exitFailure, stdout.String(), stderr.String())
			}
			if tc.want != "" && !strings.Contains(stdout.String(), tc.want) {
				t.Errorf("crewflow task attention wrote %q, want the task in the state %q", stdout.String(), tc.want)
			}
			if tc.want == "" && strings.Contains(stdout.String(), "ATTENTION REQUIRED") {
				t.Errorf("crewflow task attention wrote %q, want no queue at all", stdout.String())
			}
		})
	}
}

// TestRunTaskListShowsTheQueueOverTheTable: a person opens a list of runs to see what wants
// them, and the block of the queue is the first thing in it. The list reads the state of
// the tasks and nothing else, so the queue in it is worked out of the state alone and says
// what it knows (docs/DESIGN.md §6, §6a).
func TestRunTaskListShowsTheQueueOverTheTable(t *testing.T) {
	host := &host{opened: true, task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	putRunThatStands(t, host)
	var stdout, stderr bytes.Buffer

	if code := run([]string{"task", "list", "-config", project}, &stdout, &stderr); code != exitOK {
		t.Fatalf("crewflow task list = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	said := stdout.String()
	for _, want := range []string{"ATTENTION REQUIRED", "#43", string(taskrun.AttentionStands), "waiting 11m"} {
		if !strings.Contains(said, want) {
			t.Errorf("crewflow task list wrote %q, want it to mention %q", said, want)
		}
	}
	if strings.Index(said, "ATTENTION REQUIRED") > strings.Index(said, "TASK") {
		t.Errorf("crewflow task list wrote %q, want the block of the queue over the table", said)
	}

	// The answer of `-json` is the same queue for a program, with the state of the
	// attention of the task in the entry of the run (docs/DESIGN.md §6a).
	stdout.Reset()
	if code := run([]string{"task", "list", "-config", project, "-json"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("crewflow task list -json = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	var answer []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &answer); err != nil {
		t.Fatalf("crewflow task list -json wrote %q, which is not an answer: %v", stdout.String(), err)
	}
	if len(answer) != 1 {
		t.Fatalf("the answer holds %+v, want the one run of the project", answer)
	}
	entry := answer[0]
	for key, want := range map[string]any{
		"attention_state": string(taskrun.AttentionStands),
		"reason":          taskrun.ReasonNoProgress,
		"next_actor":      taskrun.ActorOrchestrator,
		"priority":        string(taskrun.High),
		"actable":         string(taskrun.ActNow),
	} {
		if said := entry[key]; said != want {
			t.Errorf("the answer holds %s = %v, want %v", key, said, want)
		}
	}
	if since, is := entry["waiting_since"].(string); !is || since == "" {
		t.Errorf("the answer holds waiting_since = %v, want the moment the run went quiet", entry["waiting_since"])
	}
}

// TestRunTaskAttentionOfAProjectWithNoHostSaysWhatTheStateHolds: a project whose host of
// its own cannot be read is answered out of the state of its tasks, and it says so rather
// than reaching a host that is not there.
//
// Раньше такая задача попадала в очередь как `finished-unseen` со строкой «the host could
// not be read»: очередь утверждала и то, что задача ждёт человека, и то, что человек об
// этом не знает. Теперь она говорит, что прочитать не удалось, и ни о чём не утверждает
// (docs.DESIGN.md §6a, §7h, F-098).
func TestRunTaskAttentionOfAProjectWithNoHostSaysWhatTheStateHolds(t *testing.T) {
	host := &host{opened: true, task: taskOf(43), noSubject: true}
	host.use(t)
	project := host.config(t)
	ended := putRunThatEnded(t, host, time.Now().Add(-time.Hour))
	taskClock = func() time.Time { return ended.Add(time.Hour) }
	var stdout, stderr bytes.Buffer

	if code := run([]string{"task", "attention", "-config", project}, &stdout, &stderr); code != exitFailure {
		t.Fatalf("crewflow task attention = %d, want %d (stderr: %q)", code, exitFailure, stderr.String())
	}
	said := stdout.String()
	if !strings.Contains(said, "COULD NOT READ") {
		t.Errorf("crewflow task attention wrote %q, want the block of what was not read", said)
	}
	if !strings.Contains(said, "#43 (43-1)") {
		t.Errorf("crewflow task attention wrote %q, want the run it is about", said)
	}
	if strings.Contains(said, string(taskrun.AttentionFinishedUnseen)) {
		t.Errorf("crewflow task attention wrote %q, want no %q: a run nobody read does not wait for a person",
			said, taskrun.AttentionFinishedUnseen)
	}
	if stderr.Len() != 0 {
		t.Errorf("crewflow task attention wrote %q to stderr, want nothing", stderr.String())
	}
}

// TestRunTaskAttentionReadsTheHostAsTheOrchestratorOfTheProject: the second defect of the
// review of PR #118. A project in the mode of an account of its own names the App of its
// orchestrator in `[orchestrator] github_app app_id` and its login in `[merge] reviewers`,
// and the host of the project only knows that App where the roles of the project were built
// as its orchestrator: the queue answered "the account is the account of an app that is
// neither the app of the executor nor the app of the orchestrator of this project" for
// every task of the repository, and every run of it looked like a task nobody had looked at
// (docs/DESIGN.md §7h, §7i).
func TestRunTaskAttentionReadsTheHostAsTheOrchestratorOfTheProject(t *testing.T) {
	host := &host{opened: true, task: taskOf(43)}
	host.use(t)
	// The host of the test knows one App, the one of the orchestrator, and refuses the login
	// of every other App the way the host of GitHub refuses an App it was not given: the two
	// roles of the project of the test hand out two different hosts, exactly as the two
	// constructions of crewflow do.
	taskRoles = func(config.Config, forge.Env) (forge.Set, error) {
		return forge.Set{Tracker: host, Forge: host.as(5107052)}, nil
	}
	reviewRoles = func(config.Config, forge.Env) (forge.Set, error) {
		return forge.Set{Tracker: host, Forge: host.as(5140522)}, nil
	}
	project := host.configAs(t, separateConfig)
	ended := putRunThatEnded(t, host, time.Now().Add(-time.Hour))
	taskClock = func() time.Time { return ended.Add(time.Hour) }
	var stdout, stderr bytes.Buffer

	code := run([]string{"task", "attention", "-config", project}, &stdout, &stderr)

	if code != exitFailure {
		t.Fatalf("crewflow task attention = %d, want %d (stdout: %q, stderr: %q)", code, exitFailure, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "the host could not be read") {
		t.Errorf("crewflow task attention wrote %q, want the queue to have read the host: "+
			"the roles of the project are the roles of its orchestrator, as a review is made of", stdout.String())
	}
	if !strings.Contains(stdout.String(), string(taskrun.AttentionAwaitsReview)) {
		t.Errorf("crewflow task attention wrote %q, want the change of the run waiting for a review", stdout.String())
	}
}

// TestRunTaskAttentionTakesAMergedChangeOutOfTheQueue: a live run of the build of this branch
// left nine merged changes of this repository in the queue as `escalated · finished-unseen`
// with a wait of a day and a half — the queue must read the reaction of the host (F-061).
func TestRunTaskAttentionTakesAMergedChangeOutOfTheQueue(t *testing.T) {
	host := &host{opened: true, task: taskOf(43), merged: true}
	host.use(t)
	project := host.config(t)
	ended := putRunThatEnded(t, host, time.Now().Add(-66*time.Hour))
	taskClock = func() time.Time { return ended.Add(66 * time.Hour) }
	var stdout, stderr bytes.Buffer

	code := run([]string{"task", "attention", "-config", project}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow task attention = %d, want %d: a merged change is the reaction (stdout: %q, stderr: %q)",
			code, exitOK, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), string(taskrun.AttentionEscalated)) {
		t.Errorf("crewflow task attention wrote %q, want no queue at all", stdout.String())
	}
	if len(host.records) != 0 {
		t.Errorf("crewflow left %q under a task whose change is merged, want nothing", host.records)
	}
}

// TestRunTaskAttentionTakesAClosedTaskOutOfTheQueue: a task the host has closed is done,
// whoever closed it and whatever stood in the way of its run (§7g).
func TestRunTaskAttentionTakesAClosedTaskOutOfTheQueue(t *testing.T) {
	host := &host{opened: true, task: taskOf(43)}
	host.task.State = "closed"
	host.use(t)
	project := host.config(t)
	ended := putRunThatEnded(t, host, time.Now().Add(-3*time.Hour))
	taskClock = func() time.Time { return ended.Add(3 * time.Hour) }
	var stdout, stderr bytes.Buffer

	if code := run([]string{"task", "attention", "-config", project}, &stdout, &stderr); code != exitOK {
		t.Fatalf("crewflow task attention = %d, want %d (stdout: %q, stderr: %q)", code, exitOK, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "ATTENTION REQUIRED") {
		t.Errorf("crewflow task attention wrote %q, want no queue: the host has closed the task", stdout.String())
	}
}

// TestRunTaskAttentionOfARunWithoutAPrDoesNotReadOne: F-099, живой прогон на этом
// репозитории. У прогона, который не открыл change request, очередь спрашивала у
// хостинга про «change request #0» и получала «no pull requests found» — в строке
// очереди это читалось как ошибка чтения хостинга у задачи, у которой change request
// и не бывает.
//
// Change request номера ноль не бывает: у хостинга спрашивают про те, что у прогона
// есть, а их нет — ни одного вопроса (docs.DESIGN.md §6a, §7h).
func TestRunTaskAttentionOfARunWithoutAPrDoesNotReadOne(t *testing.T) {
	host := &host{opened: true, task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	ended := putRunThatEndedWithoutAChange(t, host, time.Now().Add(-time.Hour))
	taskClock = func() time.Time { return ended.Add(time.Hour) }
	var stdout, stderr bytes.Buffer

	code := run([]string{"task", "attention", "-config", project}, &stdout, &stderr)

	if code != exitFailure {
		t.Fatalf("crewflow task attention = %d, want %d (stdout: %q, stderr: %q)",
			code, exitFailure, stdout.String(), stderr.String())
	}
	for _, unwanted := range []string{"no pull requests found", "change request #0", "the host could not be read"} {
		if strings.Contains(stdout.String(), unwanted) {
			t.Errorf("crewflow task attention wrote %q, want no %q: change request номера ноль не бывает",
				stdout.String(), unwanted)
		}
	}
	if !strings.Contains(stdout.String(), string(taskrun.AttentionFinishedUnseen)) {
		t.Errorf("crewflow task attention wrote %q, want %q: прогон без PR — это результат, а не ошибка чтения",
			stdout.String(), taskrun.AttentionFinishedUnseen)
	}
	if len(host.changes) != 0 {
		t.Errorf("хостинг спросили про change request %v, want ни одного: у прогона их нет", host.changes)
	}
}

// putRunThatEndedWithoutAChange is the state of a task whose run ended and opened no
// change request: результат есть, а PR нет, и очередь не должна искать его (F-099).
func putRunThatEndedWithoutAChange(t *testing.T, h *host, ended time.Time) time.Time {
	t.Helper()
	journals := taskrun.JournalsOf(h.home, "naghuale-crewflow")
	state := taskrun.State{
		Number: 43, Title: "the run of a task", Branch: "crewflow/43-task", Profile: "opencode",
	}
	state = state.NextAttempt(taskrun.StartOf{
		Started:  ended.Add(-42 * time.Minute),
		Step:     "the executor of the run",
		Journal:  journals.JournalPath(43, 1),
		Executor: "opencode",
		Identity: taskrun.Identity{Mode: "owner", Description: "owner — the person who runs crewflow"},
	})
	state = state.Ended(ended, taskrun.TimedOut)
	if err := taskrun.SaveState(journals.StatePath(43), state); err != nil {
		t.Fatalf("write the state of the task: %v", err)
	}
	return ended
}

// TestRunTaskAttentionSaysThatItCouldNotReadInsteadOfClaimingAttention: F-098 на этом
// репозитории, 01.10: после Ctrl+C команда печатала восемь строк `escalated ·
// finished-unseen` с причиной `context canceled`, а через три минуты — «nothing wants a
// person». Очередь говорила и то, что восемь задач ждут человека, и то, что не ждёт никто.
//
// Теперь блок `ATTENTION REQUIRED` не содержит ничего о непрочитанном, рядом стоит блок
// `COULD NOT READ` с числом прогонов и их причинами, а код не ноль: расписание узнаёт, что
// ответ неполон (docs.DESIGN.md §6a).
func TestRunTaskAttentionSaysThatItCouldNotReadInsteadOfClaimingAttention(t *testing.T) {
	host := &host{opened: true, task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	ended := putRunThatEnded(t, host, time.Now().Add(-66*time.Hour))
	// Хостинг не читается ни для одной задачи: сеть легла, и это не «задачи нет».
	host.noTask = errors.New("read task 43: dial tcp: i/o timeout")
	taskClock = func() time.Time { return ended.Add(66 * time.Hour) }
	var stdout, stderr bytes.Buffer

	code := run([]string{"task", "attention", "-config", project}, &stdout, &stderr)

	if code == exitOK {
		t.Fatalf("crewflow task attention = 0, want не ноль: прочитано не всё, а расписание должно знать")
	}
	said := stdout.String()
	if !strings.Contains(said, "COULD NOT READ") || !strings.Contains(said, "#43 (43-1)") {
		t.Errorf("crewflow task attention написал %q, want блок «COULD NOT READ» с прогоном", said)
	}
	for _, unwanted := range []string{"ATTENTION REQUIRED", string(taskrun.AttentionEscalated), "nothing wants a person"} {
		if strings.Contains(said, unwanted) {
			t.Errorf("crewflow task attention написал %q, want без %q: непрочитанное — не внимание", said, unwanted)
		}
	}
	if len(host.records) != 0 {
		t.Errorf("под задачей оставлено %q, want ничего: о непрочитанном не напоминают", host.records)
	}
}

// TestRunTaskAttentionInterruptedSaysItWasInterruptedWithoutAList: тот же случай по Ctrl+C.
// Человек остановил команду на середине: сказать «прервано» и выйти с ненулевым кодом, не
// печатая список того, что crewflow успел прочитать и не понял (docs.DESIGN.md §6a).
func TestRunTaskAttentionInterruptedSaysItWasInterruptedWithoutAList(t *testing.T) {
	host := &host{opened: true, task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	ended := putRunThatEnded(t, host, time.Now().Add(-time.Hour))
	host.noTask = errors.New("read task 43: " + context.Canceled.Error())
	taskClock = func() time.Time { return ended.Add(time.Hour) }
	var stdout, stderr bytes.Buffer

	code := run([]string{"task", "attention", "-config", project}, &stdout, &stderr)

	if code == exitOK {
		t.Fatalf("crewflow task attention = 0, want не ноль: чтение прервано, а это не ответ")
	}
	said := stdout.String()
	if !strings.Contains(said, "interrupted") {
		t.Errorf("crewflow task attention написал %q, want слово «interrupted»: прерванное чтение говорит, что прервано", said)
	}
	for _, unwanted := range []string{"ATTENTION REQUIRED", string(taskrun.AttentionEscalated), "nothing wants a person"} {
		if strings.Contains(said, unwanted) {
			t.Errorf("crewflow task attention написал %q, want без %q: прерванное чтение не даёт утверждений", said, unwanted)
		}
	}
}

// TestRunTaskAttentionTakesTheVerdictOfTheGateForTheRecordsUnderTheChange: одобрение и
// приёмку считает gate, и очередь показывает то же, что показали бы ворота. До правки
// очередь считала записи под изменением своими двумя правилами (F-106): одобрение,
// отредактированное после публикации, уходило из очереди как «принято», и задача, чей
// PR ждёт ревью, из неё выпадала.
//
// Три случая: обычное одобрение выводит задачу из очереди, отредактированное — нет и
// называет причину ворот, а приёмка помеченной задачи ждёт владельца.
func TestRunTaskAttentionTakesTheVerdictOfTheGateForTheRecordsUnderTheChange(t *testing.T) {
	owner := forge.Subject{Kind: forge.KindUser, Login: "naghuale", ID: int64(len("naghuale"))}
	for _, tc := range []struct {
		name    string
		labels  []string
		under   []forge.Comment
		wantOut bool
		want    string
	}{
		{
			name: "одобрение головы выводит задачу из очереди",
			under: []forge.Comment{{
				Author: owner, CreatedAt: time.Now().Add(-time.Hour), Body: gate.ApprovedOf("9f1c0de"),
			}},
			wantOut: true,
		},
		{
			name: "отредактированное после публикации одобрение не считается",
			under: []forge.Comment{{
				Author: owner, CreatedAt: time.Now().Add(-time.Hour),
				Body: gate.ApprovedOf("9f1c0de"), Edited: true,
			}},
			want: string(gate.ApprovalEdited),
		},
		{
			name: "запись исполнителя не одобрение",
			under: []forge.Comment{{
				Author:    forge.Subject{Kind: forge.KindApp, ID: 5107052},
				CreatedAt: time.Now().Add(-time.Hour),
				Body:      gate.ApprovedOf("9f1c0de"),
			}},
			want: string(gate.ApprovalUntrusted),
		},
		{
			name:   "принятый результат помеченной задачи выводит её из очереди",
			labels: []string{"owner-check"},
			under: []forge.Comment{
				{Author: owner, CreatedAt: time.Now().Add(-time.Hour), Body: gate.ApprovedOf("9f1c0de")},
				{Author: owner, CreatedAt: time.Now().Add(-time.Minute), Body: gate.AcceptedOf("9f1c0de")},
			},
			wantOut: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := &host{opened: true, task: taskOf(43)}
			host.task.Labels = tc.labels
			host.under = tc.under
			host.use(t)
			project := host.config(t)
			ended := putRunThatEnded(t, host, time.Now().Add(-time.Hour))
			taskClock = func() time.Time { return ended.Add(time.Hour) }
			var stdout, stderr bytes.Buffer

			code := run([]string{"task", "attention", "-config", project}, &stdout, &stderr)

			switch {
			case tc.wantOut && code != exitOK:
				t.Fatalf("crewflow task attention = %d, want %d (stdout: %q, stderr: %q)",
					code, exitOK, stdout.String(), stderr.String())
			case !tc.wantOut && code != exitFailure:
				t.Fatalf("crewflow task attention = %d, want %d (stdout: %q, stderr: %q)",
					code, exitFailure, stdout.String(), stderr.String())
			}
			if tc.wantOut {
				if strings.Contains(stdout.String(), "ATTENTION REQUIRED") {
					t.Errorf("crewflow task attention wrote %q, want no queue", stdout.String())
				}
				return
			}
			if !strings.Contains(stdout.String(), string(taskrun.AttentionAwaitsReview)) {
				t.Errorf("crewflow task attention wrote %q, want %q: запись не считается",
					stdout.String(), taskrun.AttentionAwaitsReview)
			}
			if !strings.Contains(stdout.String(), tc.want) {
				t.Errorf("crewflow task attention wrote %q, want причину ворот %q", stdout.String(), tc.want)
			}
		})
	}
}

// TestRunTaskAttentionOfATaskTheHostDoesNotHave: the third defect of the review of PR #118,
// found on telecli (01.10). The repository moved, another one took the name, the issues were
// renumbered — and the runs of tasks of the old repository stood in the queue as
// `escalated · finished-unseen`, each row carrying "Could not resolve to an issue" and each
// one trying to write a comment under a task that is not there. The answer of the host is a
// fact of the project, and the queue treats it as one (docs.DESIGN.md §6a, §7g).
func TestRunTaskAttentionOfATaskTheHostDoesNotHave(t *testing.T) {
	host := &host{opened: true, task: taskOf(53),
		noTask: fmt.Errorf("the task #53 is not on naghuale/tele: %w", forge.ErrNoSuchTask)}
	host.use(t)
	project := host.config(t)
	ended := putRunThatEnded(t, host, time.Now().Add(-66*time.Hour))
	taskClock = func() time.Time { return ended.Add(66 * time.Hour) }
	var stdout, stderr bytes.Buffer

	code := run([]string{"task", "attention", "-config", project}, &stdout, &stderr)

	if code != exitFailure {
		t.Fatalf("crewflow task attention = %d, want %d: the run is a fact a person has to see (stdout: %q, stderr: %q)",
			code, exitFailure, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), taskrun.ReasonTaskMissing) {
		t.Errorf("crewflow task attention wrote %q, want the reason %q", stdout.String(), taskrun.ReasonTaskMissing)
	}
	// The hint names the project of the file, which in the test of a command is the one of
	// the fixture; on telecli it is `naghuale/tele`.
	for _, want := range []string{"naghuale/crewflow", "may have moved", "~/.crewflow/runs/"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("crewflow task attention wrote %q, want the hint to mention %q", stdout.String(), want)
		}
	}
	for _, unwanted := range []string{"escalated", "the host could not be read", "not left under the task"} {
		if strings.Contains(stdout.String(), unwanted) {
			t.Errorf("crewflow task attention wrote %q, want no %q: a task the host does not have is not escalated, "+
				"nothing failed and nothing is written under it", stdout.String(), unwanted)
		}
	}
	if len(host.records) != 0 {
		t.Errorf("crewflow wrote %q under a task the host does not have, want nothing", host.records)
	}
	if stderr.Len() != 0 {
		t.Errorf("crewflow task attention wrote %q to stderr, want nothing", stderr.String())
	}
}

// TestRunTaskAttentionRefusesAnArgumentItCannotRead: a call with a word in it is a call
// that cannot be understood, and the usage says what the right call is (docs/DESIGN.md §6).
func TestRunTaskAttentionRefusesAnArgumentItCannotRead(t *testing.T) {
	host := &host{task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	var stdout, stderr bytes.Buffer

	code := run([]string{"task", "attention", "43", "-config", project}, &stdout, &stderr)

	if code != exitUsage {
		t.Fatalf("crewflow task attention 43 = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr.String(), `unexpected argument "43"`) {
		t.Errorf("crewflow task attention wrote %q, want it to say what it cannot read", stderr.String())
	}
}

// putRunThatEnded is the state of a task whose run ended with the change request of the
// host open, and the moment it ended: a run that nobody has looked at since is the case of
// F-061, three runs of a night that ended while everybody saw work going on (§6a).
func putRunThatEnded(t *testing.T, h *host, ended time.Time) time.Time {
	t.Helper()
	journals := taskrun.JournalsOf(h.home, "naghuale-crewflow")
	state := taskrun.State{
		Number: 43, Title: "the run of a task", Branch: "crewflow/43-task", Profile: "opencode",
		Change: &taskrun.Change{Number: 44, URL: "https://github.com/naghuale/crewflow/pull/44"},
	}
	state = state.NextAttempt(taskrun.StartOf{
		Started:  ended.Add(-42 * time.Minute),
		Step:     "the executor of the run",
		Journal:  journals.JournalPath(43, 1),
		Executor: "opencode",
		Identity: taskrun.Identity{Mode: "owner", Description: "owner — the person who runs crewflow"},
	})
	state = state.Ended(ended, taskrun.ChangeRequestOpened)
	if err := taskrun.SaveState(journals.StatePath(43), state); err != nil {
		t.Fatalf("write the state of the task: %v", err)
	}
	return ended
}
