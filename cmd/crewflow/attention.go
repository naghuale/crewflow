package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
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
// `crewflow task list` is the same queue over the table, and it is asked about the host of
// the project exactly where `task attention` is: список и очередь отвечают о делах одними
// и теми же словами, иначе человек увидит в одном «нужно посмотреть», а в другом «не
// смотреть» про одну и ту же задачу (§6a).
// `crewflow task attention` is that queue with the host of the project in it — the
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

// readSilence is how long a command may read the queue of a project and say nothing before
// it says that it is reading: две секунды — это дольше, чем моргание, и короче, чем время,
// за которое человек успевает решить, что команда зависла (F-098, §6a).
//
// It is a variable and not a constant so that a test can shorten it: тест не должен ждать
// две настоящие секунды ради одной строки (§6a).
var readSilence = 2 * time.Second

// reader is what a command says while it reads and has said nothing for a while. It is a
// timer armed by the queue and stopped by the command, and the write happens under a lock:
// очередь читает пачками и зовёт Reading из своей горутины, а stderr команды один (§6a).
type reader struct {
	mu    sync.Mutex
	w     io.Writer
	after time.Duration
	timer *time.Timer
	done  bool
}

// saysItReads returns the reading of a command that writes into w when the queue has been
// reading for longer than after. The caller must stop it when the queue is answered: строка
// о чтении не должна появиться после ответа на вопрос, который она описывает (§6a).
func saysItReads(w io.Writer, after time.Duration) *reader {
	return &reader{w: w, after: after}
}

// begin is told by the queue how many tasks of the project it is about to read. It arms the
// timer and does nothing else: очередь не должна ждать этой строки и не должна знать,
// напечатана она или нет (§6).
func (r *reader) begin(tasks int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done {
		return
	}
	r.timer = time.AfterFunc(r.after, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.done {
			return
		}
		fmt.Fprintf(r.w, "crewflow task attention: reading the state of %d tasks…\n", tasks)
	})
}

// stop ends the reading: the queue is answered, и строка о чтении больше не нужна.
func (r *reader) stop() {
	r.mu.Lock()
	r.done = true
	if r.timer != nil {
		r.timer.Stop()
	}
	r.mu.Unlock()
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
	return hostOfAttention{
		set: set, reviewers: reviewers, owners: owners, executor: roles.ExecutorOf(cfg),
		repository: cfg.Project.Repo, branch: cfg.Project.DefaultBranch,
		acceptance: cfg.Acceptance.Label,
	}, nil
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
	// repository, branch and acceptance are what the gate of §7h asks for beside the
	// records: the project a change has to be of, the branch it merges into, and the
	// label a task is marked with to have its result taken in by the owner (§5, §7f).
	repository string
	branch     string
	acceptance string
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
	if change <= 0 {
		// Прогон, который не открыл change request, не имеет ничего, кроме задачи, и
		// спрашивать хостинг про change request номера ноль — это вопрос, на который
		// «no pull requests found» и есть честный ответ: такого изменения нет
		// (F-099, docs.DESIGN.md §6a, §7h).
		return facts, nil
	}
	changeFacts, err := h.changeFacts(ctx, change, task, found.Labels)
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
// what is at its head, and what stands under it — an approval of that head, an acceptance
// of the result of the task, or the refusal of the gate about the records that are there.
//
// The task and its labels come with it because the acceptance of §7h is asked of a task
// marked with the label of the owner and of no other: a change of a task nobody marks
// waits for nobody's `ACCEPTED` (§5, §7f, §7h).
func (h hostOfAttention) changeFacts(ctx context.Context, number, task int, labels []string) (*taskrun.ChangeFacts, error) {
	change, err := h.set.Forge.ChangeRequest(ctx, number)
	if err != nil {
		return nil, fmt.Errorf("read the change request #%d: %w", number, err)
	}
	comments, err := h.set.Forge.Comments(ctx, number)
	if err != nil {
		return nil, fmt.Errorf("read what is written under the change request #%d: %w", number, err)
	}
	// The records under the change are counted by the gate of §7h and not by the queue:
	// a record of a review is one thing, and two opinions about it are one too many
	// (F-106, §6a). The gate is handed the change as the host holds it, the comments
	// under it and the accounts whose records count — the same accounts a review and a
	// merge are made of, because the queue reads the host as the orchestrator (§7i).
	reaction := gate.React(gate.Under(change, comments, gate.Deps{
		Repository:      h.repository,
		DefaultBranch:   h.branch,
		Executor:        h.executor,
		Reviewers:       h.reviewers,
		Owners:          h.owners,
		AcceptanceLabel: h.acceptance,
		Task:            task,
		Labels:          labels,
	}))
	refusal := ""
	if !reaction.Refusal.Ready {
		refusal = reaction.Refusal.String()
	}
	return &taskrun.ChangeFacts{
		Number: change.Number, State: change.State, Head: change.HeadSHA,
		Approved: reaction.Approved, Accepted: reaction.Accepted,
		Refusal: refusal, Last: reaction.Last,
	}, nil
}

// noticeUnderTheTask is what leaves the notice of an entry of the queue under its task on
// the host of the project, and what a project of no host answers: a task that waits for
// longer than the project agreed is reported to the person who asked and written about in
// no tracker, and the answer says so rather than letting a schedule believe that a notice
// is there (docs/DESIGN.md §6a, §7g).
//
// The roles of the project are the roles of its orchestrator with its key — the same ones
// a review and a merge are made of, and not the ones a command that only shows the state
// works with. A record under a task is a write, and a write is an act in the name of the
// App of the orchestrator: it is built the first time there is something to leave under a
// task, and not before, so a project whose host is not reachable, or a queue with nothing
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

// isClosed is whether a tracker says of a task that it is closed, whatever the word of the
// host stands for: a task that is closed is a task that is done, and a queue of what wants a
// person has no business asking for anything under it (docs.DESIGN.md §7g).
func isClosed(state string) bool {
	return strings.EqualFold(strings.TrimSpace(state), "closed")
}
