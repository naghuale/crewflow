package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/forge/roles"
	"github.com/naghuale/crewflow/internal/gate"
	taskrun "github.com/naghuale/crewflow/internal/run"
	"github.com/naghuale/crewflow/internal/secret"
)

// The queue of attention of a project is the answer to the question an orchestrator and an
// owner ask first — what of the work of this project needs me right now — and it is worked
// out of the state of the runs of the project and of what the host of the project says
// about their changes (docs/DESIGN.md §6a).
//
// `crewflow task list` shows the queue over the table and reads nothing but the state of
// the tasks, because a list is a question and a question reaches no network (§6).
// `crewflow task attention` is the same queue with the host of the project in it — the
// labels of the tasks and the records under their change requests — and it is the command
// of a schedule of an orchestrator: it leaves a record under a task that has waited for
// longer than the project agreed, once for a key and not once a minute, and its code says
// that something wants a person.

// attentionEnv is the machine and the file of the project the queue is worked out on: the
// clock, the question whether a run is still going, the time limit of a run, and the
// thresholds of §6a — the silence after which a run stands, which is the one of the
// executor (§7a), and the three of `[attention]`.
func attentionEnv(cfg config.Config) (taskrun.AttentionEnv, error) {
	env := taskrun.AttentionEnv{ListEnv: taskrun.ListEnv{
		Now:     taskClock,
		Running: taskMachine.Alive,
	}}
	var err error
	if env.ListEnv.StallAfter, err = stallSilence(cfg); err != nil {
		return env, err
	}
	if env.ListEnv.Timeout, err = runLimit(cfg); err != nil {
		return env, err
	}
	for _, threshold := range []struct {
		key   string
		value string
		into  *time.Duration
	}{
		{"attention.top_after", cfg.Attention.TopAfter, &env.TopAfter},
		{"attention.escalate_after", cfg.Attention.EscalateAfter, &env.EscalateAfter},
		{"attention.remind_after", cfg.Attention.RemindAfter, &env.RemindAfter},
		{"attention.weekly_after", cfg.Attention.WeeklyAfter, &env.WeeklyAfter},
	} {
		limit, err := time.ParseDuration(threshold.value)
		if err != nil {
			return env, fmt.Errorf("%s: %w", threshold.key, err)
		}
		*threshold.into = limit
	}
	env.AcceptanceLabel = cfg.Acceptance.Label
	return env, nil
}

// attentionHost is the host of a project as the queue asks about it: the words a task is
// marked with, and the facts of the change request of the run of it — whether it is still
// open, what is at its head, and whether a record of a review or of an acceptance of that
// head is under it. It counts the records the way the gate of §7h counts them, by the
// account that wrote them and not by what they say, and it asks the host about a task only
// where the run of it opened a change request: a run that opened none has nothing on the
// host to say about it.
func attentionHost(ctx context.Context, set forge.Set, cfg config.Config) (taskrun.Host, error) {
	if set.Tracker == nil || set.Forge == nil {
		return nil, nil
	}
	// The accounts of the project are worked out by the same two functions a review is
	// worked out by, and over the same roles: the queue counts the records a review counts,
	// and in the mode of an account of its own the host has to know the app of the
	// orchestrator to say which account that is. A queue that built the roles another way
	// refuses the login of the orchestrator and then answers about every run of the project
	// "the host could not be read" — the review of PR #118 saw exactly that on a live run
	// (docs.DESIGN.md §7h, §7i).
	reviewers, err := reviewersOf(ctx, cfg, set)
	if err != nil {
		return nil, err
	}
	owners, err := ownersOf(ctx, cfg, set)
	if err != nil {
		return nil, err
	}
	// The account the executor of a run works under is not a reviewer and not an owner,
	// whatever the lists of the file of the project name: a run that approves its own
	// change is not a review (docs/DESIGN.md §7h, §7i).
	return hostOfAttention{set: set, reviewers: reviewers, owners: owners, executor: roles.ExecutorOf(cfg)}, nil
}

// hostOfAttention is the host of a project as the queue reads it. The accounts of the
// project are worked out once, where the roles of it are built, and not once per task of
// the queue: a queue of twenty tasks asks the host about the tasks and not about the file of
// the project twenty times over.
type hostOfAttention struct {
	set       forge.Set
	reviewers []forge.Subject
	owners    []forge.Subject
	executor  forge.Subject
}

// FactsOf is what the host of the project says about a task and about the change request of
// the run of it. What could not be read is said rather than passed over: a queue that says
// "nobody is waiting" because the host was out of reach would be lying about the work of
// the project (docs.DESIGN.md §7h).
//
// A task the host of the project does not have is not a failure to read: it is the answer
// of §7g, and the queue is told in one word. That is what a repository that moved looks
// like — the live run of PR #118 on telecli (01.10: the name `naghuale/tele` was taken by a
// new repository and the issues of it renumbered) held five runs of tasks of the old one as
// `escalated · finished-unseen`, each with a comment written under a task that is not there.
func (h hostOfAttention) FactsOf(ctx context.Context, task, change int) (taskrun.HostFacts, error) {
	found, err := h.set.Tracker.Task(ctx, task)
	switch {
	case forge.NoSuchTask(err):
		return taskrun.HostFacts{Missing: true}, nil
	case err != nil:
		return taskrun.HostFacts{}, fmt.Errorf("read task %d: %w", task, err)
	}
	facts := taskrun.HostFacts{Labels: found.Labels, Closed: isClosed(found.State)}
	changeFacts, err := h.changeFacts(ctx, change)
	if err != nil {
		// The labels of the task were read and the change request was not: the queue is
		// told what it knows and what it does not, because an entry that hides a host it
		// could not read is an entry a person acts on with half the truth.
		facts.Problem = err.Error()
		return facts, nil
	}
	facts.Change = changeFacts
	return facts, nil
}

// changeFacts is what the host of the project says about a change request: where it stands,
// what is at its head, and whether a record of a review or of an acceptance of that head is
// under it. The records are counted as the gate of §7h counts them, and the last one of a
// kind stands: a request for changes written after an approval takes it back, and an
// approval of a commit that has grown since is an approval of a commit that is not there.
func (h hostOfAttention) changeFacts(ctx context.Context, number int) (*taskrun.ChangeFacts, error) {
	change, err := h.set.Forge.ChangeRequest(ctx, number)
	if err != nil {
		return nil, fmt.Errorf("read the change request #%d: %w", number, err)
	}
	comments, err := h.set.Forge.Comments(ctx, number)
	if err != nil {
		return nil, fmt.Errorf("read what is written under the change request #%d: %w", number, err)
	}
	approved, reviewedAt := approvedAt(comments, change.HeadSHA, h.reviewers, h.executor)
	accepted, acceptedAt := acceptedOf(comments, change.HeadSHA, h.owners, h.executor)
	// The waiting of a person began when the last record under the change was written, and
	// the two kinds of record are ordered by the same clock: an owner who accepted the
	// result after the last review has been waiting since the acceptance was asked for,
	// and a person reading the queue is told how long that is.
	last := reviewedAt
	if acceptedAt.After(last) {
		last = acceptedAt
	}
	return &taskrun.ChangeFacts{
		Number: change.Number, State: change.State, Head: change.HeadSHA,
		Approved: approved, Accepted: accepted, Last: last,
	}, nil
}

// noticeUnderTheTask is what leaves the notice of an entry of the queue under its task on
// the host of the project, and what a project of no host answers: a task that waits for
// longer than the project agreed is reported to the person who asked and written about in
// no tracker, and the answer says so rather than letting a schedule believe that a notice
// is there (docs/DESIGN.md §6a, §7g).
//
// The roles of the project are the roles of its orchestrator — the same ones a review and a
// merge are made of — and they are built the first time there is something to leave under a
// task, and not before: a project whose host is not reachable, or a queue with nothing
// escalated in it, is answered without a network at all. A record of the queue is a word to
// the orchestrator and is written as him: in the mode of an account of its own the App of the
// executor is refused by the host of an issue, and a notice that cannot be written is no
// notice at all (docs.DESIGN.md §6a, §7g, §7i).
func noticeUnderTheTask(cfg config.Config, configPath string) taskrun.SayAttention {
	var built *forge.Set
	return func(ctx context.Context, one taskrun.Attention) (bool, string) {
		if built == nil {
			set, err := reviewRoles(cfg, roleEnv(configPath, secret.NewNotices(io.Discard)))
			if err != nil {
				return false, err.Error()
			}
			built = &set
		}
		commenter, is := built.Tracker.(forge.TaskCommenter)
		if !is {
			return false, taskrun.NoRecord
		}
		if err := commenter.CommentTask(ctx, one.Task, taskrun.NoticeUnder(one)); err != nil {
			return false, err.Error()
		}
		return true, ""
	}
}

// approvedAt is whether the last record of a review under a change is an approval of the
// head as it stands, and when that record was written. A record of anybody else, and a
// record that was edited after it was published, is a comment and not an approval, whatever
// it says (docs/DESIGN.md §7h, §7i).
func approvedAt(comments []forge.Comment, head string, of []forge.Subject, executor forge.Subject) (bool, time.Time) {
	approved, when := false, time.Time{}
	for _, comment := range comments {
		record, is := gate.Parse(comment.Body)
		if !is || comment.Edited || comment.Author.Same(executor) || !among(comment.Author, of) {
			continue
		}
		approved = record.Decision == gate.Approved && sameCommit(record.Commit, head)
		when = comment.CreatedAt
	}
	return approved, when
}

// acceptedOf is whether the owner of the project took the result of the task in at the head
// of the change as it stands, and when that record was written. The record is `ACCEPTED
// <sha>` of one of `[merge] owners` and of nobody else: an orchestrator that works apart
// from the owner is a reviewer of the project and not an owner of it (docs/DESIGN.md §7h,
// §7i).
func acceptedOf(comments []forge.Comment, head string, of []forge.Subject, executor forge.Subject) (bool, time.Time) {
	accepted, when := false, time.Time{}
	for _, comment := range comments {
		commit, is := gate.ParseOwnerAccept(comment.Body)
		if !is || comment.Edited || comment.Author.Same(executor) || !among(comment.Author, of) {
			continue
		}
		accepted = sameCommit(commit, head)
		when = comment.CreatedAt
	}
	return accepted, when
}

// isClosed is whether a tracker says of a task that it is closed, whatever the word of the
// host stands for: a task that is closed is a task that is done, and a queue of what wants a
// person has no business asking for anything under it (docs.DESIGN.md §7g).
func isClosed(state string) bool {
	return strings.EqualFold(strings.TrimSpace(state), "closed")
}

// among is whether an account is one of the list, by the kind of the account and the number
// the host keeps it under: a host writes one account in a different line in every API, an
// App is renamed together with its account, and a person may have the login an App goes by
// (docs/DESIGN.md §7h, §7i).
func among(account forge.Subject, accounts []forge.Subject) bool {
	for _, one := range accounts {
		if account.Same(one) {
			return true
		}
	}
	return false
}

// sameCommit is whether a record of a review is about the head of a change: the shas of a
// host are one commit whatever their case, and a record about no commit at all is about
// none (docs.DESIGN.md §7h).
func sameCommit(one, other string) bool {
	return one != "" && other != "" && strings.EqualFold(one, other)
}
