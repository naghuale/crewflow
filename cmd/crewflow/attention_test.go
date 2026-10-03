package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/forge/github"
	"github.com/naghuale/crewflow/internal/forge/roles"
	"github.com/naghuale/crewflow/internal/gate"
	"github.com/naghuale/crewflow/internal/proc"
	taskrun "github.com/naghuale/crewflow/internal/run"
	"github.com/naghuale/crewflow/internal/secret"
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

	// The answer is the canonical document of §6a: the version of the format and the
	// moment it was made are in the root of it, the records of what wants a person are
	// under `attention`, and the list of the runs is named and empty — очередь не
	// перечисляет прогоны, которые не требуют действия.
	answer := documentOf(t, stdout.Bytes())
	if answer.Version != taskrun.Format {
		t.Errorf("the version of the format = %d, want %d", answer.Version, taskrun.Format)
	}
	if !answer.GeneratedAt.Equal(ended.Add(2 * time.Hour)) {
		t.Errorf("the document was made at %s, want the moment of the clock of the command",
			answer.GeneratedAt)
	}
	if answer.Repo != "naghuale/crewflow" {
		t.Errorf("the document is of the project %q, want naghuale/crewflow", answer.Repo)
	}
	if answer.Runs == nil || len(answer.Runs) != 0 {
		t.Errorf("the document holds the runs %+v, want none and named", answer.Runs)
	}
	if len(answer.Attention) != 1 {
		t.Fatalf("the document holds %+v, want the one task that wants a person", answer.Attention)
	}
	one := answer.Attention[0]
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
	if err := json.Unmarshal(documentRuns(t, stdout.Bytes()), &answer); err != nil {
		t.Fatalf("crewflow task list -json wrote %q, which is not an answer: %v", stdout.String(), err)
	}
	if len(answer) != 1 {
		t.Fatalf("the answer holds %+v, want the one run of the project", answer)
	}
	held, is := answer[0]["attention"].(map[string]any)
	if !is {
		t.Fatalf("the answer holds %+v, want the record of the attention of the task in the record of the run",
			answer[0])
	}
	for key, want := range map[string]any{
		"attention_state": string(taskrun.AttentionStands),
		"reason":          taskrun.ReasonNoProgress,
		"next_actor":      taskrun.ActorOrchestrator,
		"priority":        string(taskrun.High),
		"actable":         string(taskrun.ActNow),
	} {
		if said := held[key]; said != want {
			t.Errorf("the answer holds attention.%s = %v, want %v", key, said, want)
		}
	}
	if since, is := held["waiting_since"].(string); !is || since == "" {
		t.Errorf("the answer holds attention.waiting_since = %v, want the moment the run went quiet",
			held["waiting_since"])
	}
	// The list of the attention of the document is the very queue the block over the table
	// was written from: одна очередь, а не две (docs/DESIGN.md §6a, F-105).
	document := documentOf(t, stdout.Bytes())
	if len(document.Attention) != 1 || document.Attention[0].Task != 43 {
		t.Errorf("the document holds the attention %+v, want the one record the block over the table was written from",
			document.Attention)
	}
}

// storeThatFallsOver is the store of the secrets of a machine whose keychain of macOS asks
// the owner of the machine in a window of the system: reading a key of an App out of it opens
// that window, and on a build nobody has trusted yet nobody is at it. The read is a fall and
// not a refusal, so that a test says which key was asked for and stops there (docs.DESIGN.md §7i).
type storeThatFallsOver struct{}

func (storeThatFallsOver) Get(service, account string) ([]byte, error) {
	panic(fmt.Sprintf("the key %s/%s was read: a command that only shows the state of a project has no business asking for it",
		service, account))
}

func (storeThatFallsOver) Set(string, string, []byte) error { return nil }

func (storeThatFallsOver) Has(string, string) (bool, error) { return false, nil }

// TestTheCommandsThatShowTheStateAskTheRolesThatHoldNoKeyOfAnApp: the failure this is here
// for, at the level of the commands that run on a schedule. The queue of what wants a person
// counts records by the numbers the host publishes and reads a foreign service as the person
// who runs crewflow, and it used to do that with the roles of a review — so every login of the
// lists of the gate was turned into a number by asking the App of the orchestrator for its own
// name, and on a build of the program nobody had trusted yet that read opened a window of the
// keychain of macOS and stood in front of it for two minutes at a time. The whole queue of
// #37 became `read-failed` that way (F-109, docs.DESIGN.md §6a, §7i).
//
// So the two commands that only show the state are made of the roles that carry the numbers of
// the Apps and no key of them, the machine of the test hands out a store of secrets that falls
// over on the read, and both of them answer the queue out of the host of the test as before.
func TestTheCommandsThatShowTheStateAskTheRolesThatHoldNoKeyOfAnApp(t *testing.T) {
	for _, command := range []string{"attention", "list"} {
		t.Run("task "+command, func(t *testing.T) {
			host := &host{opened: true, task: taskOf(43)}
			host.use(t)
			was := secretsOfMachine
			t.Cleanup(func() { secretsOfMachine = was })
			secretsOfMachine = func() secret.Store { return storeThatFallsOver{} }
			asked, withKeys := 0, 0
			stateRoles = func(cfg config.Config, env forge.Env) (forge.Set, error) {
				// The roles of a command that only shows the state are the ones crewflow
				// builds by number, and they are asked for here with the machine of the
				// command — the store of the keys is in that machine, and the roles are to
				// leave it out (docs.DESIGN.md §6a, §7i).
				built, err := roles.ToShow(cfg, env)
				if err != nil {
					return forge.Set{}, err
				}
				asked++
				if adapter, is := built.Forge.(*github.Adapter); is && adapter.Orchestrator().Store != nil {
					withKeys++
				}
				// The queue of a test is a queue of a host of a test.
				return forge.Set{Tracker: host, Forge: host}, nil
			}
			project := host.configAs(t, separateConfig)
			ended := putRunThatEnded(t, host, time.Now().Add(-time.Hour))
			taskClock = func() time.Time { return ended.Add(time.Hour) }
			var stdout, stderr bytes.Buffer

			run([]string{"task", command, "-config", project}, &stdout, &stderr)

			if asked == 0 {
				t.Errorf("crewflow task %s did not ask for the roles of the project at all", command)
			}
			if withKeys != 0 {
				t.Errorf("crewflow task %s was given the roles of the project %d times with the store of the keys "+
					"of the machine in them, want the roles that only show the state, which carry no store", command, withKeys)
			}
			said := stdout.String() + stderr.String()
			if strings.Contains(said, "keychain") {
				t.Errorf("crewflow task %s wrote %q, want nothing about the keychain of macOS: it did not ask it for anything", command, said)
			}
			// The queue is the answer these two commands are for: a command that could not
			// read the host would be saying nothing about the work of the project.
			if strings.Contains(said, "COULD NOT READ") || strings.Contains(said, "host could not be read") {
				t.Errorf("crewflow task %s wrote %q, want the queue of the project read out of the host of the test", command, said)
			}
		})
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
	stateRoles = reviewRoles
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
	keepsState(t, journals.StatePath(43), state)
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

// TestRunTaskAttentionAndTaskListGiveTheSameAttention: CL-007…CL-009. Две команды о
// проекте должны отвечать об одном и том же списке внимания — иначе список прогонов
// показывает то, чего уже нет, а очередь — то, чего никто не спрашивал (F-105).
//
// Четыре прогона: изменение слито, задача закрыта, изменение открыто и никто на него не
// смотрел, и прогон, который стоит. Первые двое — нигде: их работу хостинг объявил
// законченной, и состояние помнит это. Третий — в обоих ответах, и с одинаковыми
// состоянием, причиной и следующим участником. Четвёртый — в обоих, и его видно без
// хостинга вовсе.
func TestRunTaskAttentionAndTaskListGiveTheSameAttention(t *testing.T) {
	// Один хост на четыре задачи: слитое изменение, закрытая задача, открытое изменение
	// без записи ревью и прогон, который стоит.
	host := &host{opened: true, tasks: map[int]forge.Task{
		43: {Number: 43, Title: "изменение слито", State: "open"},
		44: {Number: 44, Title: "задача закрыта", State: "closed"},
		45: {Number: 45, Title: "открыто, не смотрели", State: "open"},
		46: {Number: 46, Title: "стоит", State: "open"},
	}, states: map[int]string{
		43: "merged",
		44: "open",
		45: "open",
	}}
	host.use(t)
	project := host.config(t)
	ended := time.Now().Add(-2 * time.Hour)
	putRunsOfFourTasks(t, host, ended)
	taskClock = func() time.Time { return ended.Add(2 * time.Hour) }
	var stdout, stderr bytes.Buffer

	if code := run([]string{"task", "attention", "-config", project, "-json"}, &stdout, &stderr); code != exitFailure {
		t.Fatalf("crewflow task attention -json = %d, want %d (stderr: %q)", code, exitFailure, stderr.String())
	}
	fromAttention := attentionOfAnswer(t, stdout.Bytes())

	// `task list` спрашивает тот же проект о том же, только без сети: то, что состояние
	// помнит, у него есть, а то, чего не помнит, — нет.
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"task", "list", "-config", project, "-json"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("crewflow task list -json = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	fromList := attentionOfAnswer(t, stdout.Bytes())

	want := map[int]attentionRecord{
		45: {task: 45, state: string(taskrun.AttentionAwaitsReview), reason: taskrun.ReasonReviewRequired, actor: taskrun.ActorOrchestrator},
		46: {task: 46, state: string(taskrun.AttentionStands), reason: taskrun.ReasonNoProgress, actor: taskrun.ActorOrchestrator},
	}
	if !reflect.DeepEqual(fromAttention, want) {
		t.Errorf("`task attention -json` = %+v,\nwant %+v: слитое и закрытое — нигде, открытое без реакции — здесь", fromAttention, want)
	}
	if !reflect.DeepEqual(fromList, want) {
		t.Errorf("`task list -json` = %+v,\nwant %+v: список прогонов отвечает о том же, о чём очередь", fromList, want)
	}
}

// TestRunTaskAttentionAllJSONIsOneDocumentOfTheMachine: `-all` — очередь внимания всех
// проектов машины в одном документе, с проектом в каждой записи. Задача говорит, что `-all`
// кладёт записи всех проектов машины в один документ, и очередь — второе представление
// состояния процесса, а не только список (docs/DESIGN.md §6a).
//
// Ответ считан из состояния прогонов и ничего не спрашивает: у каждого проекта свой трекер и
// свой хостинг, а папка, из которой спросили, может проектом вовсе не быть, поэтому запись
// под задачей здесь не оставляется ни в одном проекте — и человек об этом узнаёт из строки
// над блоком (docs/DESIGN.md §6, §7g).
func TestRunTaskAttentionAllJSONIsOneDocumentOfTheMachine(t *testing.T) {
	host := &host{opened: true, task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	_, other := host.otherProject(t)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"task", "run", "43", "-config", project}, &stdout, &stderr); code != exitOK {
		t.Fatalf("the run of this project = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	host.task = taskOf(50)
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"task", "run", "50", "-config", other}, &stdout, &stderr); code != exitOK {
		t.Fatalf("the run of the other project = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()

	// The folder crewflow was called in is of no project at all: -all is asked from any
	// folder, and the state of every project is in one place.
	t.Chdir(t.TempDir())

	if code := run([]string{"task", "attention", "-all", "-json"}, &stdout, &stderr); code != exitFailure {
		t.Fatalf("crewflow task attention -all -json = %d, want %d (stderr: %q)", code, exitFailure, stderr.String())
	}

	document := documentOf(t, stdout.Bytes())
	if document.Repo != "" {
		t.Errorf("the document is of the project %q, want no one: the attention of the machine is of no project",
			document.Repo)
	}
	if len(document.Runs) != 0 {
		t.Errorf("the document holds the runs %+v, want none and named: очередь не перечисляет прогоны", document.Runs)
	}
	// Every project of the machine is in one document, and every record says which project
	// it is of — иначе программа, читающая два проекта разом, не знала бы, чей это прогон.
	ofProject := map[string]int{}
	for _, one := range document.Attention {
		ofProject[one.Repo] = one.Task
		switch {
		case one.State != taskrun.AttentionFinishedUnseen:
			t.Errorf("the record of the task %d of %s is %q, want %q: прогон закончился, и его не смотрели",
				one.Task, one.Repo, one.State, taskrun.AttentionFinishedUnseen)
		case one.Reason != taskrun.ReasonRunCompleted:
			t.Errorf("the reason of the record of the task %d of %s = %q, want %q",
				one.Task, one.Repo, one.Reason, taskrun.ReasonRunCompleted)
		}
	}
	if want := map[string]int{"naghuale/crewflow": 43, "naghuale/telecli": 50}; !reflect.DeepEqual(ofProject, want) {
		t.Errorf("the document holds the records %+v,\nwant %+v: записи всех проектов машины в одном документе",
			ofProject, want)
	}

	// Человеку то же самое — с проектом над каждой очередью и со строкой о том, чего этот
	// ответ не читал: блок без неё читался бы как решение по каждой из задач (§6a).
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"task", "attention", "-all"}, &stdout, &stderr); code != exitFailure {
		t.Fatalf("crewflow task attention -all = %d, want %d (stderr: %q)", code, exitFailure, stderr.String())
	}
	said := stdout.String()
	for _, want := range []string{attentionOfTheMachine, "naghuale/crewflow", "naghuale/telecli",
		"ATTENTION REQUIRED", "#43", "#50", taskrun.ReasonRunCompleted} {
		if !strings.Contains(said, want) {
			t.Errorf("crewflow task attention -all wrote %q, want it to mention %q", said, want)
		}
	}
	if stderr.Len() != 0 {
		t.Errorf("crewflow task attention -all wrote %q to stderr, want nothing", stderr.String())
	}
}

// TestRunTaskAttentionAndTaskListNameTheConflictOfOneChangeAlike: CA-010 говорит, что
// `conflict-with-main` называют и очередь, и список прогонов, и `status`, а все три считают
// одну и ту же очередь. Одно и то же изменение, у которого `mergeable_state: DIRTY`, читается
// обоими одинаково — иначе один человек увидит в одном «нужно посмотреть», а в другом «не
// смотреть» про одну задачу (docs.DESIGN.md §6a, §6, D-044, F-110).
func TestRunTaskAttentionAndTaskListNameTheConflictOfOneChangeAlike(t *testing.T) {
	host := &host{opened: true, task: taskOf(43), conflicted: true, mergeState: "DIRTY",
		ci: ciOfTheTest{}}
	host.use(t)
	project := host.config(t)
	ended := putRunThatEnded(t, host, time.Now().Add(-time.Hour))
	taskClock = func() time.Time { return ended.Add(time.Hour) }
	var stdout, stderr bytes.Buffer

	if code := run([]string{"task", "attention", "-config", project, "-json"}, &stdout, &stderr); code != exitFailure {
		t.Fatalf("crewflow task attention -json = %d, want %d (stderr: %q)", code, exitFailure, stderr.String())
	}
	fromAttention := attentionOfAnswer(t, stdout.Bytes())

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"task", "list", "-config", project, "-json"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("crewflow task list -json = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	fromList := attentionOfAnswer(t, stdout.Bytes())

	want := map[int]attentionRecord{
		43: {task: 43, state: string(taskrun.AttentionBlocked),
			reason: taskrun.ReasonConflictWithMain, actor: taskrun.ActorOrchestrator},
	}
	if !reflect.DeepEqual(fromAttention, want) {
		t.Errorf("`task attention -json` = %+v,\nwant %+v: конфликт изменения назван сразу", fromAttention, want)
	}
	if !reflect.DeepEqual(fromList, want) {
		t.Errorf("`task list -json` = %+v,\nwant %+v: список отвечает о том же изменении теми же словами",
			fromList, want)
	}
}

// attentionRecord is one answer об attention, as a program reads it: task, attention_state,
// reason и next_actor — те четыре поля, по которым две команды обязаны совпадать
// (CL-007…CL-009, docs/DESIGN.md §6a).
type attentionRecord struct {
	task   int
	state  string
	reason string
	actor  string
}

// documentOf is the canonical document of §6a as a command wrote it, and it fails the test
// where the answer is not one: программа, читающая состояние процесса, читает один формат
// (docs/DESIGN.md §6a).
func documentOf(t *testing.T, answer []byte) taskrun.Document {
	t.Helper()
	var document taskrun.Document
	if err := json.Unmarshal(answer, &document); err != nil {
		t.Fatalf("the answer %s is not a document of the format: %v", answer, err)
	}
	if document.Version != taskrun.Format {
		t.Errorf("the answer %s holds the version %d, want %d", answer, document.Version, taskrun.Format)
	}
	if document.GeneratedAt.IsZero() {
		t.Errorf("the answer %s holds no moment of its own, want generated_at", answer)
	}
	return document
}

// documentRuns is the list of the records of the runs of a document without the root around
// it: a test that reads the fields of a record must not have to know where the record itself
// is (docs/DESIGN.md §6a).
func documentRuns(t *testing.T, answer []byte) []byte {
	t.Helper()
	var document struct {
		Runs []map[string]any `json:"runs"`
	}
	if err := json.Unmarshal(answer, &document); err != nil {
		t.Fatalf("the answer %s is not a document: %v", answer, err)
	}
	runs, err := json.Marshal(document.Runs)
	if err != nil {
		t.Fatalf("the records of the answer %s: %v", answer, err)
	}
	return runs
}

// attentionOfAnswer is what an answer of `-json` says об attention, по задачам и в
// порядке номеров: блок внимания и записи списка прогонов читаются одинаково, и человек
// проверяет их глазами именно так.
//
// The document is one for both commands, and the attention of it is told twice: by the list
// of what wants a person and by the records of the runs, and a program that reads either
// reads the same thing (docs/DESIGN.md §6a, CL-007…CL-009).
func attentionOfAnswer(t *testing.T, answer []byte) map[int]attentionRecord {
	t.Helper()
	records := map[int]attentionRecord{}
	for _, one := range entriesOfAnswer(t, answer) {
		if one.state == "" {
			continue
		}
		records[one.task] = one
	}
	for _, one := range documentOf(t, answer).Attention {
		records[one.Task] = attentionRecord{one.Task, string(one.State), one.Reason, one.NextActor}
	}
	return records
}

// entriesOfAnswer is the attention each record of a run says of its own task, read by the
// words of the format and not by the struct of the package: программа читает JSON, и тест
// должен проверять то, что читает она.
func entriesOfAnswer(t *testing.T, answer []byte) []attentionRecord {
	t.Helper()
	var document struct {
		Runs []struct {
			Task      int `json:"task"`
			Attention struct {
				AttentionState string `json:"attention_state"`
				Reason         string `json:"reason"`
				NextActor      string `json:"next_actor"`
			} `json:"attention"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(answer, &document); err != nil {
		t.Fatalf("the answer %s is not a document: %v", answer, err)
	}
	records := make([]attentionRecord, 0, len(document.Runs))
	for _, one := range document.Runs {
		records = append(records, attentionRecord{one.Task, one.Attention.AttentionState,
			one.Attention.Reason, one.Attention.NextActor})
	}
	return records
}

// putRunsOfFourTasks is the state of четырёх задач проекта: две работы кончены, одна
// ждёт ревью, одна стоит (docs.DESIGN.md §6a, F-061, F-105).
func putRunsOfFourTasks(t *testing.T, h *host, ended time.Time) {
	t.Helper()
	var startedAt time.Time
	journals := taskrun.JournalsOf(h.home, "naghuale-crewflow")
	for _, one := range []struct {
		number   int
		title    string
		change   *taskrun.Change
		standing bool
	}{
		{number: 43, title: "изменение слито", change: &taskrun.Change{Number: 43}},
		{number: 44, title: "задача закрыта", change: &taskrun.Change{Number: 44}},
		{number: 45, title: "открыто, не смотрели", change: &taskrun.Change{Number: 45}},
		{number: 46, title: "стоит", standing: true},
	} {
		state := taskrun.State{
			Number: one.number, Title: one.title, Branch: "crewflow/task", Profile: "opencode",
			Change: one.change,
		}
		started := ended.Add(-42 * time.Minute)
		state = state.NextAttempt(taskrun.StartOf{
			Started: started, Step: "the executor of the run",
			Journal: journals.JournalPath(one.number, 1), Executor: "opencode",
			Identity: taskrun.Identity{Mode: "owner", Description: "owner"},
		})
		switch {
		case one.standing:
			// Стоящий прогон: процесс есть, знака жизни одиннадцать минут назад.
			state = state.Alive(ended.Add(-11*time.Minute), "go test ./...", "")
			state.Attempts[0].PID, startedAt = 4242, ended.Add(-12*time.Minute)
			state.Attempts[0].ProcessStartedAt = &startedAt
		default:
			state = state.Ended(ended, taskrun.ChangeRequestOpened)
		}
		keepsState(t, journals.StatePath(one.number), state)
	}
	taskMachine = proc.Env{Ask: func(_ string, args []string) (string, error) {
		if !slices.Contains(args, "-p") {
			return "", errors.New("ps: no such file or directory")
		}
		return ended.Add(-12 * time.Minute).Format("Mon Jan _2 15:04:05 2006"), nil
	}}
}

// TestRunTaskAttentionSaysThatItIsReadingWhenTheHostIsSlow: F-098, строка молчания.
// Чтение ходит в чужой сервис, и минута тишины в терминале выглядит как зависшая команда:
// человек нажимает Ctrl+C и не получает ответа.
//
// Пока очередь читается дольше readSilence, команда говорит в stderr, что читает и сколько
// задач — и говорит это до ответа, а не после него. Здесь хостинг держит первый вопрос до
// той самой строки, и без неё тест бы завис (docs/DESIGN.md §6a).
func TestRunTaskAttentionSaysThatItIsReadingWhenTheHostIsSlow(t *testing.T) {
	host := &host{opened: true, task: taskOf(43)}
	host.use(t)
	project := host.config(t)
	ended := putRunThatEnded(t, host, time.Now().Add(-time.Hour))
	taskClock = func() time.Time { return ended.Add(time.Hour) }
	// Строка должна уйти раньше ответа хостинга: без этой подмены тест ждал бы readSilence
	// настоящего времени, а с ней — отвечает на ту же строку, что и человек в терминале.
	wrote := make(chan struct{})
	stderr := &notified{w: &bytes.Buffer{}, wrote: wrote}
	reviewRoles = func(config.Config, forge.Env) (forge.Set, error) {
		return forge.Set{
			Tracker: &waitingFor{Tracker: host, wrote: wrote},
			Forge:   host,
		}, nil
	}
	stateRoles = reviewRoles
	was := readSilence
	readSilence = time.Millisecond
	t.Cleanup(func() { readSilence = was })
	var stdout bytes.Buffer

	code := run([]string{"task", "attention", "-config", project}, &stdout, stderr)

	// Прогон открыл change request и никто его не одобрил: он ждёт человека, и команда
	// выходит с не-нулевым кодом — строка о чтении ничего не меняет в ответе (§6a).
	if code != exitFailure {
		t.Fatalf("crewflow task attention = %d, want %d (stderr: %q)", code, exitFailure, stderr.w.String())
	}
	if !strings.Contains(stderr.w.String(), "reading the state of 1 tasks") {
		t.Errorf("crewflow task attention написал %q в stderr, want строку о чтении", stderr.w.String())
	}
	if strings.Contains(stdout.String(), "reading the state") {
		t.Errorf("crewflow task attention написал %q в stdout, want строку о чтении в stderr: "+
			"ответ команды — это очередь, а не то, что она делает", stdout.String())
	}
}

// notified is the stderr of a test, which says when it was written to the first time: так
// тест узнаёт, что строка о чтении ушла, и отпускает хостинг (§6a).
type notified struct {
	mu    sync.Mutex
	w     *bytes.Buffer
	wrote chan struct{}
	once  sync.Once
}

func (n *notified) Write(p []byte) (int, error) {
	n.once.Do(func() { close(n.wrote) })
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.w.Write(p)
}

// waitingFor is the tracker of a test, which answers nothing until the command has said that
// it is reading: строка о чтении должна уйти раньше ответа хостинга, иначе она бесполезна
// (§6a).
type waitingFor struct {
	forge.Tracker
	wrote chan struct{}
}

// Task is the first question the queue asks the host of a project, and here it is the one
// that waits for the line.
func (w *waitingFor) Task(ctx context.Context, number int) (forge.Task, error) {
	select {
	case <-w.wrote:
	case <-ctx.Done():
	}
	return w.Tracker.Task(ctx, number)
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
	keepsState(t, journals.StatePath(43), state)
	return ended
}

// keepsState is the state a test has made for a task, written the way a command writes one:
// as a change, under the lock of the task. A fixture stands for a machine whose state is what
// the test says, and it goes through `UpdateState` all the same — that is the one road into a
// state, and a second one is how a record of one command gets lost by another (D-044: одна
// дорога).
func keepsState(t *testing.T, path string, state taskrun.State) {
	t.Helper()
	if _, err := taskrun.UpdateState(path, func(taskrun.State) (taskrun.State, error) { return state, nil }); err != nil {
		t.Fatalf("write the state of the task into %s: %v", path, err)
	}
}

// ciOfTheTest is the CI of a project of a test: the checks of the head of a change request
// and whether the host answers at all. Это третья роль §7g, и очередь внимания читает её
// отдельно от хостинга — по тем же словам `Checks`, `required-check-missing` и §6a.
type ciOfTheTest struct {
	checks  []forge.CheckRun
	refused error
}

// Checks are the checks of the head, as the host of them holds them.
func (c ciOfTheTest) Checks(context.Context, string) ([]forge.CheckRun, error) {
	if c.refused != nil {
		return nil, c.refused
	}
	return c.checks, nil
}

// RequiredChecks are none of the rules of the branch of a test: у теста нет ветки, и
// очередь внимания спрашивает о проверках головы, а не о правилах (§7h).
func (ciOfTheTest) RequiredChecks(context.Context) ([]forge.RequiredCheck, error) { return nil, nil }

// Status is the whole of this CI as a gate asks it, and the queue of attention спрашивает
// не его, а Checks (§7g).
func (ciOfTheTest) Status(context.Context, string) (forge.CheckState, error) {
	return forge.CheckNone, nil
}

// Doctor says nothing: у теста нет ни программы, ни входа (§7d).
func (ciOfTheTest) Doctor(context.Context) []forge.Check { return nil }

// TestRunTaskAttentionNamesTheConflictAndTheAbsenceOfChecksAndTheChecksThatAreGoing:
// CA-010 / CL-010 / CL-011 через настоящую команду. Изменение с конфликтом, изменение без
// проверок и изменение, чьи проверки идут, называются в очереди тремя разными причинами, и
// ни одна из них не «ждём CI». Каждый раз ждёт своего: конфликт — переноса ветки, отсутствие
// проверок — решения человека, ход проверок — минут CI до срока (docs.DESIGN.md §6a, F-110).
func TestRunTaskAttentionNamesTheConflictAndTheAbsenceOfChecksAndTheChecksThatAreGoing(t *testing.T) {
	const head = "9f1c0de"
	for _, tc := range []struct {
		name string
		ci   forge.CheckLister
		// conflicted and mergeState are what the host says about merging the change.
		conflicted bool
		mergeState string
		want       string
		notWant    string
	}{
		{
			name:       "изменение конфликтует с main",
			ci:         ciOfTheTest{checks: []forge.CheckRun{{Name: "test", State: forge.CheckSuccess, SHA: head}}},
			conflicted: true,
			mergeState: "DIRTY",
			want:       taskrun.ReasonConflictWithMain,
			notWant:    taskrun.ReasonChecksRunning,
		},
		{
			name:       "проверок нет вовсе",
			ci:         ciOfTheTest{},
			mergeState: "BLOCKED",
			want:       taskrun.ReasonNoChecks,
			notWant:    taskrun.ReasonChecksRunning,
		},
		{
			name:       "проверки идут",
			ci:         ciOfTheTest{checks: []forge.CheckRun{{Name: "test", State: forge.CheckPending, SHA: head}}},
			mergeState: "BLOCKED",
			want:       taskrun.ReasonChecksRunning,
			notWant:    taskrun.ReasonNoChecks,
		},
		{
			name:       "CI не ответил — очередь не выдумывает причину",
			ci:         ciOfTheTest{refused: errors.New("gh api repos/naghuale/crewflow/commits/9f1c0de/status: 503")},
			mergeState: "BLOCKED",
			want:       taskrun.ReasonReviewRequired,
			notWant:    taskrun.ReasonNoChecks,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := &host{opened: true, task: taskOf(43), ci: tc.ci,
				conflicted: tc.conflicted, mergeState: tc.mergeState}
			host.use(t)
			project := host.config(t)
			ended := putRunThatEnded(t, host, time.Now().Add(-time.Hour))
			taskClock = func() time.Time { return ended.Add(time.Hour) }
			var stdout, stderr bytes.Buffer

			code := run([]string{"task", "attention", "-config", project}, &stdout, &stderr)

			if code != exitFailure {
				t.Fatalf("crewflow task attention = %d, want %d (stdout: %q, stderr: %q)",
					code, exitFailure, stdout.String(), stderr.String())
			}
			if !strings.Contains(stdout.String(), tc.want) {
				t.Errorf("crewflow task attention wrote %q, want the reason %q", stdout.String(), tc.want)
			}
			if strings.Contains(stdout.String(), tc.notWant) {
				t.Errorf("crewflow task attention wrote %q, want it not to name %q", stdout.String(), tc.notWant)
			}
		})
	}
}

// TestRunTaskAttentionSaysWhenTheChecksCouldNotBeRead: нет данных — «неизвестно», а не
// догадка. Где CI проекта не ответил, очередь говорит, что о проверках не знает, и не
// называет их ни идущими, ни отсутствующими: отсутствие ответа не «ещё идёт» (F-098,
// docs.DESIGN.md §6a).
func TestRunTaskAttentionSaysWhenTheChecksCouldNotBeRead(t *testing.T) {
	host := &host{opened: true, task: taskOf(43), mergeState: "BLOCKED",
		ci: ciOfTheTest{refused: errors.New("the checks could not be read")}}
	host.use(t)
	project := host.config(t)
	ended := putRunThatEnded(t, host, time.Now().Add(-time.Hour))
	taskClock = func() time.Time { return ended.Add(time.Hour) }
	var stdout, stderr bytes.Buffer

	code := run([]string{"task", "attention", "-config", project}, &stdout, &stderr)

	if code != exitFailure {
		t.Fatalf("crewflow task attention = %d, want %d (stdout: %q, stderr: %q)",
			code, exitFailure, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "could not be read") {
		t.Errorf("crewflow task attention wrote %q, want it to say that the checks are unknown", stdout.String())
	}
	for _, unwanted := range []string{taskrun.ReasonNoChecks, taskrun.ReasonChecksRunning} {
		if strings.Contains(stdout.String(), unwanted) {
			t.Errorf("crewflow task attention wrote %q, want it not to name %q", stdout.String(), unwanted)
		}
	}
}
