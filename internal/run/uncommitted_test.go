package run

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/access"
	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/secret"
)

// The work a run leaves outside the commits of its branch is D-088: F-193 was two hours of
// active work — thirteen changed and six new files — and a timeout that took all of it out
// of the reach of the next run, with nothing in the state of the task and nothing in the
// queue to say that the folder held it. These tests are against a real git and a real
// worktree, because what is at stake is the behaviour of git itself: an index of its own,
// a ref nobody is on, and a worktree that is the same afterwards byte for byte.

// TestASnapshotOfTheWorkLeavesTheWorktreeAsItWas: the snapshot is written through an index
// of its own, so that the head of the branch, the branch itself, the index a person staged
// their work in and every file of the worktree are what they were before it. A snapshot
// that moved the branch or staged a file would be a snapshot that changed the work it is
// there to protect (D-088, docs/DESIGN.md §7a).
func TestASnapshotOfTheWorkLeavesTheWorktreeAsItWas(t *testing.T) {
	repo := repositoryWith(t)
	r := runnerOf(t, repo, 43, 1)
	worktree := checkoutOf(t, repo, r)
	// A run that has done some of the work: a file committed and then changed, a file
	// staged, a file new, a file deleted and a file the repository ignores.
	write(t, filepath.Join(worktree, "changed.go"), "package run // the first version\n")
	gitOf(t, worktree, "add", "changed.go")
	gitOf(t, worktree, "commit", "-m", "feat: the first commit of the run")
	write(t, filepath.Join(worktree, "changed.go"), "package run // changed\n")
	write(t, filepath.Join(worktree, "staged.go"), "package run // staged\n")
	gitOf(t, worktree, "add", "staged.go")
	write(t, filepath.Join(worktree, "new.go"), "package run // new\n")
	if err := os.Remove(filepath.Join(worktree, "removed.go")); err != nil {
		t.Fatalf("take the file away: %v", err)
	}
	write(t, filepath.Join(worktree, "ignored.go"), "package run // ignored\n")

	before := marksOf(t, worktree)
	work := r.mustKeep(t)

	after := marksOf(t, worktree)
	if before != after {
		t.Errorf("the worktree of the task after the snapshot:\n%s\nwant:\n%s", after, before)
	}
	if work.Ref != "refs/crewflow/checkpoints/43/1" {
		t.Errorf("the snapshot is kept in %q, want the ref of the task and the attempt", work.Ref)
	}
	if want := "git -C " + worktree + " cherry-pick --no-commit refs/crewflow/checkpoints/43/1"; work.Restore != want {
		t.Errorf("the work is put back with %q, want %q", work.Restore, want)
	}
	// The counts are what the record of the attempt holds: the file that was deleted is
	// work of the run, and the file the repository ignores is not — it is a build of the
	// project, a cache of its tools or the scratch of the run.
	if work.Changed != 3 || work.New != 1 {
		t.Errorf("the counts are %d changed and %d new, want 3 and 1: a deletion is work and an ignored file is not",
			work.Changed, work.New)
	}
	// The snapshot holds the whole of the worktree: the file that was deleted is gone from
	// it, the new one is in it, and the ignored one is not there — it is not the work of
	// the task.
	in := split(gitOut(t, worktree, "ls-tree", "--name-only", "-r", work.Ref))
	want := []string{".gitignore", "README.md", "changed.go", "new.go", "staged.go"}
	if !slices.Equal(in, want) {
		t.Errorf("the snapshot holds %v, want the whole of the worktree as it stood: %v", in, want)
	}
	// The file that was deleted is not in the snapshot and the file the repository ignores
	// is not in it either: the first is gone from the folder, and the second is a build
	// of the project or the scratch of the run and not the work of the task.
	if slices.Contains(in, "removed.go") {
		t.Error("the snapshot holds the file the run deleted, want the worktree as the folder has it")
	}
	if slices.Contains(in, "ignored.go") {
		t.Error("the snapshot holds a file the repository ignores, want none")
	}
	if held := gitOut(t, worktree, "show", work.Ref+":changed.go"); held != "package run // changed" {
		t.Errorf("the snapshot holds %q for the changed file, want what the run wrote", held)
	}
	// Nothing of it is on the branch: the snapshot is a commit nobody is on, and its
	// parent is the commit the run made.
	head := gitOut(t, worktree, "rev-parse", "HEAD")
	if parent := gitOut(t, worktree, "rev-parse", work.Ref+"^"); parent != head {
		t.Errorf("the parent of the snapshot is %s, want the head of the branch %s: the snapshot is not a commit of the branch",
			shortCommit(parent), shortCommit(head))
	}
}

// TestTheWorkIsPutBackOutOfTheSnapshotAndTheNewerWorkIsNotTrampledOver: the one command of
// git that the record names has to bring the work back, and it has to refuse rather than
// win where the worktree has moved on since: a person who lost a folder to a cleanup and
// then went on in the same worktree must not lose the newer work to the older snapshot
// (D-088, docs/DESIGN.md §7a).
func TestTheWorkIsPutBackOutOfTheSnapshotAndTheNewerWorkIsNotTrampledOver(t *testing.T) {
	repo := repositoryWith(t)
	r := runnerOf(t, repo, 43, 1)
	worktree := checkoutOf(t, repo, r)
	write(t, filepath.Join(worktree, "gone.go"), "package run // the work of the run\n")
	work := r.mustKeep(t)

	// The folder is lost the way the cleanup of a worktree loses it.
	if err := os.RemoveAll(worktree); err != nil {
		t.Fatalf("take the worktree away: %v", err)
	}
	restored := checkoutOf(t, repo, runnerOf(t, repo, 43, 2))
	putBack(t, restored, work.Ref)

	if got := read(t, filepath.Join(restored, "gone.go")); got != "package run // the work of the run\n" {
		t.Errorf("the file came back as %q, want the work of the run", got)
	}
	// The work comes back staged and not committed: it is the person who puts it back who
	// decides that it goes into the branch.
	if staged := split(gitOut(t, restored, "diff", "--cached", "--name-only")); !slices.Equal(staged, []string{"gone.go"}) {
		t.Errorf("the work came back as %v, want it staged and not committed", staged)
	}

	// The same snapshot into a worktree that has gone on with the same file: the command
	// refuses and the newer work stays as it is.
	gitOf(t, restored, "reset", "--hard")
	gitOf(t, restored, "clean", "-fd")
	write(t, filepath.Join(restored, "gone.go"), "package run // the work of the next attempt\n")
	if _, err := runGit(restored, "cherry-pick", "--no-commit", work.Ref); err == nil {
		t.Error("the work of the next attempt was put over by the snapshot")
	}
	if got := read(t, filepath.Join(restored, "gone.go")); got != "package run // the work of the next attempt\n" {
		t.Errorf("the file holds %q, want the work of the next attempt and not the snapshot", got)
	}
}

// TestThePathsThatStayClosedAreLeftOutOfTheSnapshot: the boundary of a run cleans what a
// run publishes and it does not clean a blob, so a file that stays closed to the executor
// whatever the project wrote is not written into the repository of the project — and the
// snapshot says so, because a protection that is not whole has to be said to be not whole
// (D-088, docs/DESIGN.md §7d, §8).
func TestThePathsThatStayClosedAreLeftOutOfTheSnapshot(t *testing.T) {
	repo := repositoryWith(t)
	cfg := projectOf(t, t.TempDir(), "1h")
	r := runnerOf(t, repo, 43, 1)
	r.cfg = cfg
	// The places that stay closed on the machine of the run, as a run works them out
	// before it is started: the keys, the tokens and the file of the environment of the
	// project, whatever the project or the task wrote.
	policy, _ := access.Resolve(t.Context(), access.Env{Home: "/home/person"}, config.Access{}, nil)
	r.policy, r.closed = policy, newClosed(policy, "/home/person")
	worktree := checkoutOf(t, repo, r)
	write(t, filepath.Join(worktree, "the-work.go"), "package run // the work of the run\n")
	write(t, filepath.Join(worktree, ".env"), "TOKEN=the value of a person\n")
	write(t, filepath.Join(worktree, ".scratch", "tmp", "a-probe.go"), "package run // a probe\n")

	work := r.mustKeep(t)

	// The scratch of the run is left out by the same rule whatever git says about it: it
	// is the one thing a run writes outside of what the task is to change.
	if !slices.Equal(work.Left, []string{".env", ".scratch/tmp/a-probe.go"}) {
		t.Errorf("the snapshot left out %v, want .env and the probe of the run", work.Left)
	}
	in := split(gitOut(t, worktree, "ls-tree", "--name-only", "-r", work.Ref))
	if !slices.Equal(in, []string{".gitignore", "README.md", "removed.go", "the-work.go"}) {
		t.Errorf("the snapshot holds %v, want the worktree without the files that stay closed", in)
	}
	if _, err := runGit(worktree, "cat-file", "-e", work.Ref+":.env"); err == nil {
		t.Error("the snapshot holds a file that stays closed to the run, want it left out")
	}
	if !strings.Contains(work.said(), ".env") {
		t.Errorf("the record of the work says %q, want it to name what was left out of the snapshot", work.said())
	}
}

// TestASnapshotIsNeverPushedAndTheOldOnesAreTakenAway: two facts about the refs of
// `refs/crewflow/`. The first is that no push of any kind sends them — a mirror sends
// every ref of the repository, and the hook of the worktree refuses them by their name
// before it refuses anything else. The second is the term of them: a snapshot is a
// protection from a lost folder and not an archive of the project (D-088,
// docs/DESIGN.md §7a, §7i).
func TestASnapshotIsNeverPushedAndTheOldOnesAreTakenAway(t *testing.T) {
	repo := repositoryWith(t)
	r := runnerOf(t, repo, 43, 2)
	worktree := checkoutOf(t, repo, r)
	write(t, filepath.Join(worktree, "the-work.go"), "package run // the work of the run\n")
	ref := r.mustKeep(t).Ref

	hook := filepath.Join(t.TempDir(), "pre-push")
	if err := os.WriteFile(hook, []byte(prePush(r.branch)), 0o700); err != nil {
		t.Fatalf("write the hook: %v", err)
	}
	// What `git push --mirror` writes to a pre-push hook: every ref of the repository, four
	// words to a line, and the ref of the snapshot among them.
	said, code := runHook(t, hook, mirrorSays(t, repo, worktree))
	if code == 0 {
		t.Errorf("the hook let a push through that carried %s:\n%s", ref, said)
	}
	if !strings.Contains(said, ref) {
		t.Errorf("the hook said %q, want it to name the ref of the snapshot", said)
	}
	// The branch of the task goes through, and that is the one thing the hook is for.
	branch := "refs/heads/" + r.branch
	head := headOf(t, worktree)
	if refused, code := runHook(t, hook, branch+" "+head+" "+branch+" "+head+"\n"); code != 0 {
		t.Errorf("the hook refused the branch of the task, want it let through:\n%s", refused)
	}

	// The term of the snapshots of the task: an old one is taken away and a fresh one is
	// kept, and the ref of another task is not touched — it is the work of another task.
	old := checkpointRef(43, 1)
	at := time.Now().Add(-checkpointKept - time.Hour).Format(time.RFC3339)
	gitOf(t, repo, "update-ref", old, commitAt(t, repo, at))
	gitOf(t, repo, "update-ref", checkpointRef(44, 1), headOf(t, worktree))

	r.prunesCheckpoints(t.Context())

	if _, err := runGit(repo, "show-ref", "--verify", "--quiet", old); err == nil {
		t.Errorf("the snapshot %s is still there, want it taken away: the term of it is %s", old, checkpointKept)
	}
	for _, kept := range []string{ref, checkpointRef(44, 1)} {
		if _, err := runGit(repo, "show-ref", "--verify", "--quiet", kept); err != nil {
			t.Errorf("the ref %s is gone: %v", kept, err)
		}
	}
}

// TestTheRecordOfTheWorkHoldsNoContentsOfTheFiles: what a run writes and what the boundary
// of the run takes out of it are two things, and a snapshot is a blob in the repository of
// the project: the record of the attempt names the paths, the counts and one command, and
// never what is in a file (D-088, docs/DESIGN.md §7e, §7h).
func TestTheRecordOfTheWorkHoldsNoContentsOfTheFiles(t *testing.T) {
	const canary = "hunter-two-canary-9f1"
	work := Uncommitted{
		At:      monday,
		Changed: 1,
		New:     1,
		Head:    "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b",
		Ref:     checkpointRef(43, 1),
		// A file that stays closed is named by its path and never by what is in it, and a
		// path that holds a value of a run is a value of a run as much as a sentence is.
		Left:    []string{".env", "notes/" + canary},
		Restore: "git -C /w/43 cherry-pick --no-commit refs/crewflow/checkpoints/43/1",
	}
	path := newJournals(t.TempDir(), "naghuale-crewflow").StatePath(43)
	if _, err := UpdateStateThrough(secret.NewOut(secret.Chosen(canary)...), path, func(state State) (State, error) {
		begun := state.NextAttempt(1, StartOf{
			Started: monday, Journal: "j", ErrorJournal: "e",
			Identity: Identity{Mode: "owner", Description: "owner"},
		})
		return begun.Ended(1, monday.Add(time.Hour), TimedOut).UncommittedWork(1, work), nil
	}); err != nil {
		t.Fatalf("write the state of the task: %v", err)
	}
	if held := read(t, path); strings.Contains(held, canary) {
		t.Errorf("the state of the task holds %q, want no value of a run in it:\n%s", canary, held)
	}
	state, err := LoadState(path)
	if err != nil {
		t.Fatalf("read the state of the task: %v", err)
	}
	kept := state.Attempts[0].Uncommitted
	if kept == nil {
		t.Fatal("the attempt of the task holds no record of the work, want the record of it")
	}
	if kept.Count() != 2 || kept.Ref != checkpointRef(43, 1) {
		t.Errorf("the record holds %+v, want two files and the ref of the snapshot", kept)
	}
}

// TestTheRecordOfTheWorkIsAPartOfTheFormatsThatCarryIt: a field nobody classified is a
// field the boundary does not publish, and the state of a task with such a field is not
// written at all (D-089): the record of the work is registered in the state of the task
// and in the list of the runs, and the reason of it is in the closed list of §6a
// (D-088, docs/DESIGN.md §6a, §7e).
func TestTheRecordOfTheWorkIsAPartOfTheFormatsThatCarryIt(t *testing.T) {
	if !KnownReason(ReasonWorkUncommitted) {
		t.Errorf("the reason %q is not in the closed list of §6a, want it in it", ReasonWorkUncommitted)
	}
	for _, path := range []string{
		"attempts[].uncommitted_work",
		"attempts[].uncommitted_work.at",
		"attempts[].uncommitted_work.changed",
		"attempts[].uncommitted_work.new",
		"attempts[].uncommitted_work.head",
		"attempts[].uncommitted_work.ref",
		"attempts[].uncommitted_work.restore",
		"attempts[].uncommitted_work.left[]",
	} {
		if _, _, known := secret.ClassOf(secret.DocumentState, path); !known {
			t.Errorf("the policy of the state does not name %q", path)
		}
	}
	for _, path := range []string{"runs[].uncommitted_work", "runs[].uncommitted_work.head", "runs[].uncommitted_work.ref"} {
		if _, _, known := secret.ClassOf(secret.DocumentTaskList, path); !known {
			t.Errorf("the policy of the list of the runs does not name %q", path)
		}
	}
	if _, words, known := secret.ClassOf(secret.DocumentTaskAttention, "attention[].reason"); !known ||
		!slices.Contains(words, ReasonWorkUncommitted) {
		t.Errorf("the policy of the queue holds %q for the reason of an entry, want %q in it",
			words, ReasonWorkUncommitted)
	}
}

// TestAWorktreeThatHoldsWorkIsNotTakenAway: every road that takes a worktree away refuses
// where there is work outside the commits of the branch, and the refusal is one refusal —
// the run that never started its executor and the merge that has come through. A snapshot
// is not what lets the folder go: it may be incomplete, and the work of the task is not a
// folder of a run that never started (D-088, docs/DESIGN.md §7a, §7i).
func TestAWorktreeThatHoldsWorkIsNotTakenAway(t *testing.T) {
	t.Run("the run that never started its executor", func(t *testing.T) {
		repo := repositoryWith(t)
		r := runnerOf(t, repo, 43, 1)
		if err := r.prepare(t.Context()); err != nil {
			t.Fatalf("make the worktree of the task: %v", err)
		}
		write(t, filepath.Join(r.worktree, "the-work-of-a-run.go"), "package run\n")

		err := r.takeRunAway(t.Context())

		if !errors.Is(err, ErrUncommittedWork) {
			t.Fatalf("the run took the worktree away with %v, want the refusal %q", err, ErrUncommittedWork)
		}
		if _, err := os.Stat(r.worktree); err != nil {
			t.Errorf("the worktree of the task is gone: %v", err)
		}
		if branch := gitOut(t, repo, "branch", "--list", r.branch); strings.TrimSpace(branch) == "" {
			t.Error("the branch of the task was taken away, want it kept: the work of the run is on it")
		}
	})
	t.Run("a clean worktree is taken away", func(t *testing.T) {
		repo := repositoryWith(t)
		r := runnerOf(t, repo, 43, 1)
		if err := r.prepare(t.Context()); err != nil {
			t.Fatalf("make the worktree of the task: %v", err)
		}
		if err := r.takeRunAway(t.Context()); err != nil {
			t.Fatalf("take the worktree of a run that never started away: %v", err)
		}
		if _, err := os.Stat(r.worktree); !os.IsNotExist(err) {
			t.Errorf("the worktree of the task is still there: %v", err)
		}
	})
}

// TestARunThatWasCutOffWithItsWorkOutsideTheCommitsIsVisibleAndSaved: the whole of D-088
// through a real run, three ways of being cut off. A timeout, a person stopping the run
// and a run that stood: in every one of them the work of the run is outside the commits,
// and in every one of them the state of the task says so, the queue asks about it with
// `work-uncommitted`, and the work is in a ref of the repository that a person can put
// back (D-088, docs/DESIGN.md §6a, §7a).
func TestARunThatWasCutOffWithItsWorkOutsideTheCommitsIsVisibleAndSaved(t *testing.T) {
	const worked = "package run // the work of the run\n"
	for _, tc := range []struct {
		name string
		// timeout is the limit of the attempt and stop says whether a person stops the
		// run from the test. A run that stood is stopped by crewflow itself and it goes on
		// by itself, and the queue of the three ways of being cut off is held by
		// TestTheQueueAsksAboutTheWorkOfTheRunThatWasCutOff.
		timeout  string
		stop     bool
		stalled  string
		attempts int
		want     Kind
	}{
		// The limit of the attempt is short enough that the run is stopped and long enough
		// that the executor of a loaded machine writes its files inside it: a run that is
		// cut off before it wrote anything leaves no work to record, and the test would
		// be about the load of the machine rather than about D-088.
		{name: "the run took longer than the limit of an attempt", timeout: "3s", want: TimedOut, attempts: 1},
		{name: "a person stopped the run", timeout: "1h", stop: true, want: Interrupted, attempts: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := repositoryWith(t)
			home := t.TempDir()
			cfg := projectOf(t, t.TempDir(), tc.timeout)
			executor := fakeExecutor(t, "opencode",
				"printf '%s' '"+theEvents+"'\n"+
					"printf '%b' '"+worked+"' > the-work-of-the-run.go\n"+
					"printf '%b' '"+worked+"' > the-second-file-of-the-run.go\n"+
					"touch the-run-is-working\n"+
					"sleep 60\n")
			cfg.Executor.Command = []string{executor, "{worktree}", "--prompt", "{prompt}"}

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan Result, 1)
			failed := make(chan error, 1)
			go func() {
				result, err := Run(ctx, System(home), cfg, (&host{task: taskOf(43)}).set(),
					Request{Number: 43, RepoDir: repo})
				if err != nil {
					failed <- err
					return
				}
				done <- result
			}()
			// The run of a person is stopped while the executor works and after it has
			// written the work: a person stops a run that is doing something.
			if tc.stop {
				worktree, err := Worktree(cfg, 43)
				if err != nil {
					t.Fatalf("the worktree of the task: %v", err)
				}
				awaitFile(t, worktree, "the-run-is-working")
				cancel()
			}

			var result Result
			select {
			case result = <-done:
			case err := <-failed:
				t.Fatalf("Run returned an error: %v", err)
			case <-time.After(90 * time.Second):
				t.Fatal("the run did not end")
			}
			if result.Outcome != tc.want {
				t.Fatalf("the run came out as %q, want %q (reason %q)", result.Outcome, tc.want, result.Reason)
			}

			state, err := LoadState(newJournals(home, "naghuale-crewflow").StatePath(43))
			if err != nil {
				t.Fatalf("load the state of the task: %v", err)
			}
			if len(state.Attempts) != tc.attempts {
				t.Fatalf("the state holds %d attempts, want %d: %+v", len(state.Attempts), tc.attempts, state.Attempts)
			}
			last := state.Attempts[len(state.Attempts)-1]
			work := last.Uncommitted
			if work == nil {
				t.Fatalf("the attempt holds no record of the work: %+v", last)
			}
			// Three files: the two the run wrote and the file it touched to say that it
			// is working. The record holds a count of files and not a list of them: the
			// names of the files are in the worktree and in the snapshot.
			if work.Count() != 3 {
				t.Errorf("the record holds %d files outside the commits, want the three files of the run", work.Count())
			}
			if want := checkpointRef(43, last.Number); work.Ref != want {
				t.Errorf("the snapshot is kept in %q, want %q", work.Ref, want)
			}
			// The snapshot holds the work of the run, and the branch of the task does not
			// have it: nothing of it was committed and nothing of it was pushed.
			held := split(gitOut(t, result.Worktree, "ls-tree", "--name-only", "-r", work.Ref))
			if !slices.Equal(held, []string{".gitignore", "README.md", "removed.go",
				"the-run-is-working", "the-second-file-of-the-run.go", "the-work-of-the-run.go"}) {
				t.Errorf("the snapshot holds %v, want the worktree as the run left it", held)
			}
			if committed := split(gitOut(t, result.Worktree, "diff", "--name-only", "origin/main...HEAD")); len(committed) != 0 {
				t.Errorf("the branch of the task holds %v, want nothing committed by the run", committed)
			}
			// The queue asks about the work before whatever the run ended with, the next
			// one is the orchestrator, and the action is a continuation in that folder.
			one, wanted := queueOf(t, home, "naghuale-crewflow", attentionEnvOf(last.EndedAt.Add(time.Hour)), nil).Wanted(43)
			if !wanted {
				t.Fatalf("the queue holds no entry of the task, want the work of the run in it")
			}
			if one.Reason != ReasonWorkUncommitted {
				t.Errorf("the reason of the entry = %q, want %q", one.Reason, ReasonWorkUncommitted)
			}
			if one.NextActor != ActorOrchestrator || one.Next != continueCommand(43) {
				t.Errorf("the entry waits for %q and offers %q, want %q and the continuation of the task",
					one.NextActor, one.Next, continueCommand(43))
			}
			for _, want := range []string{"3 files outside the commits", work.Ref, "3 new"} {
				if !strings.Contains(one.Hint, want) {
					t.Errorf("the hint of the entry is %q, want it to say %q", one.Hint, want)
				}
			}
			// The journal of the attempt says it too, while it was open: a person watching
			// the run sees the work where the run left it.
			if said := read(t, last.Journal); !strings.Contains(said, work.Ref) {
				t.Errorf("the journal of the attempt holds no line about the snapshot:\n%s", said)
			}
		})
	}
}

// awaitFile waits for a file of the worktree of the task to be there: a person stops a run
// that is doing something, and a test that stops one that has not written anything yet
// stops nothing (docs.DESIGN.md §7a).
func awaitFile(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, name)
	for until := time.Now().Add(30 * time.Second); time.Now().Before(until); {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the file %s of the worktree of the task is not there", name)
}

// TestTheQueueAsksAboutTheWorkOfTheRunThatWasCutOff: the three ways a run is cut off are
// three reasons in the queue and one reason of the work: a run that ran out of time, a run
// a person stopped and a run that stood all leave the work of the task in one folder, and
// the queue asks about the work before it asks about whatever the run ended with. The next
// one is the orchestrator and the action is a continuation in that same folder — crewflow
// does not go on by itself, because the work of a run is a decision and not a habit
// (D-088, docs.DESIGN.md §6a, §7a).
func TestTheQueueAsksAboutTheWorkOfTheRunThatWasCutOff(t *testing.T) {
	const repo = "naghuale-crewflow"
	for _, tc := range []struct {
		name    string
		outcome Kind
		want    string
	}{
		{name: "the run ran out of time", outcome: TimedOut, want: ReasonWorkUncommitted},
		{name: "a person stopped the run", outcome: Interrupted, want: ReasonWorkUncommitted},
		{name: "the run stood", outcome: Stalled, want: ReasonWorkUncommitted},
		{name: "the run could not be started", outcome: ExecutorFailed, want: ReasonWorkUncommitted},
		// A refusal keeps its own reason — it is what an orchestrator reads first
		// (§7a.1, §7d) — and the entry says the work of the run besides it.
		{name: "the run was refused a permission", outcome: BlockedPermission, want: ReasonBlockedPermission},
		{name: "the run reached for a secret", outcome: BlockedSecret, want: ReasonBlockedSecret},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			ended := monday.Add(time.Hour)
			work := Uncommitted{
				At:      ended,
				Changed: 13,
				New:     6,
				Head:    "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b",
				Ref:     checkpointRef(43, 1),
				Restore: "git -C /w/43 cherry-pick --no-commit " + checkpointRef(43, 1),
			}
			writeState(t, home, repo, 43, "the work of a task outside the commits", nil,
				try{startedAt: monday, endedAt: ended, outcome: tc.outcome, uncommitted: &work})

			queue := queueOf(t, home, repo, attentionEnvOf(ended.Add(30*time.Minute)), nil)

			one, wanted := queue.Wanted(43)
			if !wanted {
				t.Fatalf("the queue holds %+v, want the task 43 in it", tasksOfQueue(queue))
			}
			if one.Reason != tc.want {
				t.Errorf("the reason of the entry = %q, want %q", one.Reason, tc.want)
			}
			if one.NextActor != ActorOrchestrator || one.Actable != ActNow {
				t.Errorf("the entry waits for %q and is %q, want %q and %q",
					one.NextActor, one.Actable, ActorOrchestrator, ActNow)
			}
			if one.Since != ended {
				t.Errorf("the entry has been waiting since %s, want the end of the run %s", one.Since, ended)
			}
			if tc.want != ReasonWorkUncommitted {
				// A refusal is what the orchestrator reads first and it is the reason of
				// the entry; the work of the run is said beside it and not instead of it.
				return
			}
			if one.State != AttentionFinishedUnseen {
				t.Errorf("the state of the entry = %q, want %q: the run is over and its work is not in git",
					one.State, AttentionFinishedUnseen)
			}
			if one.Next != continueCommand(43) {
				t.Errorf("what may be done = %q, want the continuation of the task", one.Next)
			}
			for _, want := range []string{
				"19 files outside the commits", "13 changed", "6 new", "1a2b3c4",
				work.Ref, work.Restore, "crewflow does not go on by itself",
			} {
				if !strings.Contains(one.Hint, want) {
					t.Errorf("the hint of the entry is %q,\nwant it to say %q", one.Hint, want)
				}
			}
		})
	}
}

// TestARunThatWentOnByItselfIsNotAskedAboutTheWorkOfTheAttemptBeforeIt: the record belongs
// to the attempt that did the work, and a continuation is its own attempt in the same
// worktree. The queue asks about the work of the run a person is looking at, which is the
// last one (D-088, docs/DESIGN.md §7h).
func TestARunThatWentOnByItselfIsNotAskedAboutTheWorkOfTheAttemptBeforeIt(t *testing.T) {
	const repo = "naghuale-crewflow"
	home := t.TempDir()
	ended := monday.Add(time.Hour)
	work := Uncommitted{At: ended, Changed: 1, New: 1, Head: "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b",
		Ref: checkpointRef(43, 1)}
	writeState(t, home, repo, 43, "the work of a task outside the commits", nil,
		try{startedAt: monday, endedAt: ended, outcome: Stalled, uncommitted: &work},
		try{startedAt: ended.Add(time.Minute), endedAt: ended.Add(time.Hour), outcome: TimedOut,
			uncommitted: &Uncommitted{At: ended.Add(time.Hour), Changed: 2, New: 0,
				Head: "2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c", Ref: checkpointRef(43, 2)}})

	queue := queueOf(t, home, repo, attentionEnvOf(ended.Add(3*time.Hour)), nil)

	one, wanted := queue.Wanted(43)
	if !wanted {
		t.Fatalf("the queue holds %+v, want the task 43 in it", tasksOfQueue(queue))
	}
	if one.Reason != ReasonWorkUncommitted {
		t.Fatalf("the reason of the entry = %q, want %q", one.Reason, ReasonWorkUncommitted)
	}
	if !strings.Contains(one.Hint, checkpointRef(43, 2)) {
		t.Errorf("the hint of the entry is %q,\nwant the work of the last attempt of the run", one.Hint)
	}
}

// marksOf is everything about a worktree that a snapshot must not change: the head of the
// branch, the branch itself, the index as git holds it and every file of the worktree with
// the length of it. It is compared as one string, so that a failure says which of them
// moved.
func marksOf(t *testing.T, worktree string) string {
	t.Helper()
	var out strings.Builder
	for _, args := range [][]string{
		{"rev-parse", "HEAD"},
		{"rev-parse", "--abbrev-ref", "HEAD"},
		{"ls-files", "--stage"},
		{"status", "--porcelain"},
	} {
		fmt.Fprintf(&out, "git %s: %s\n", strings.Join(args, " "), gitOut(t, worktree, args...))
	}
	err := filepath.Walk(worktree, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !info.Mode().IsRegular() {
			return err //nolint:nilerr
		}
		held, err := os.ReadFile(path) //nolint:gosec // a test reads the worktree it made
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(worktree, path)
		fmt.Fprintf(&out, "file %s %d\n", relative, len(held))
		return nil
	})
	if err != nil {
		t.Fatalf("walk the worktree of the task: %v", err)
	}
	return out.String()
}

// repositoryWith is a repository with one commit in it and an origin of its own, and a file
// of the project in it, so that a worktree of the task has files to change, to add and to
// take away.
func repositoryWith(t *testing.T) string {
	t.Helper()
	repo, _ := repository(t)
	write(t, filepath.Join(repo, "removed.go"), "package run // to be taken away\n")
	write(t, filepath.Join(repo, ".gitignore"), "ignored.go\n")
	gitOf(t, repo, "add", ".")
	gitOf(t, repo, "commit", "-m", "feat: the project of a test")
	gitOf(t, repo, "push", "origin", "main")
	return repo
}

// runnerOf is a run of the task of the number, wired to the machine of the test, against
// the repository of the test. It is what the code of a run is called through, and not a
// stand-in inside it: the snapshot is a question to git, and a test of it is worth nothing
// against a git of its own making (D-088, docs/DESIGN.md §7a).
func runnerOf(t *testing.T, repo string, number, attempt int) *runner {
	t.Helper()
	home := t.TempDir()
	cfg := projectOf(t, t.TempDir(), "1h")
	worktree, err := Worktree(cfg, number)
	if err != nil {
		t.Fatalf("the worktree of the task %d: %v", number, err)
	}
	r := &runner{
		env:      System(home),
		cfg:      cfg,
		task:     taskOf(number),
		branch:   fmt.Sprintf("crewflow/%d-task", number),
		worktree: worktree,
		attempt:  attempt,
		journals: newJournals(home, "naghuale-crewflow"),
		req:      Request{Number: number, RepoDir: repo},
	}
	r.out = secret.NewOut()
	r.closed = newClosed(r.policy, t.TempDir())
	return r
}

// mustKeep is what a run that was cut off does with the work it left in its worktree: it
// is counted, snapshotted and said in the journal of the attempt, and it is a record of
// counts and names and one command and never of what is in a file (D-088,
// docs.DESIGN.md §7a).
func (r *runner) mustKeep(t *testing.T) Uncommitted {
	t.Helper()
	files, err := r.journals.Begin(secret.NewOut(), r.task.Number, r.attempt)
	if err != nil {
		t.Fatalf("open the files of the attempt: %v", err)
	}
	work, err := r.keeps(t.Context(), files, TimedOut)
	if err != nil {
		t.Fatalf("keep the work outside the commits: %v", err)
	}
	if err := files.Close(); err != nil {
		t.Fatalf("close the files of the attempt: %v", err)
	}
	return work
}

// checkoutOf is the worktree of the task, made the way a run makes it out of the default
// branch of the repository, and taken away with the test. It is made again from the
// default branch where a test lost it on purpose — the bookkeeping of git is pruned
// first, because that is what a folder of a task that is gone leaves behind.
func checkoutOf(t *testing.T, repo string, r *runner) string {
	t.Helper()
	if _, err := runGit(repo, "worktree", "prune"); err != nil {
		t.Fatalf("prune the bookkeeping of git: %v", err)
	}
	if _, err := runGit(repo, "worktree", "add", "-B", r.branch, r.worktree, "origin/main"); err != nil {
		t.Fatalf("make the worktree of the task: %v", err)
	}
	t.Cleanup(func() {
		if _, err := runGit(repo, "worktree", "remove", "--force", r.worktree); err != nil {
			_, _ = runGit(repo, "worktree", "prune")
		}
	})
	return r.worktree
}

// headOf is the commit the branch of a checkout stands at.
func headOf(t *testing.T, dir string) string {
	t.Helper()
	return gitOut(t, dir, "rev-parse", "HEAD")
}

// commitAt is a commit of the repository with the date it was made on, which is how a
// snapshot of a run that happened a month ago looks to `git for-each-ref`: the term of a
// snapshot is counted from the commit of it and not from the day the ref was written
// (D-088, docs/DESIGN.md §7a).
func commitAt(t *testing.T, repo, at string) string {
	t.Helper()
	head := headOf(t, repo)
	tree := gitOut(t, repo, "rev-parse", head+"^{tree}")
	cmd := exec.Command("git", "commit-tree", tree, "-p", head, "-m", checkpointSaid(43, 1))
	cmd.Dir = repo
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_DATE="+at, "GIT_COMMITTER_DATE="+at,
		"GIT_AUTHOR_NAME="+checkpointAuthor, "GIT_AUTHOR_EMAIL="+checkpointAuthor+"@localhost",
		"GIT_COMMITTER_NAME="+checkpointAuthor, "GIT_COMMITTER_EMAIL="+checkpointAuthor+"@localhost")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the commit of %s: %v\n%s", at, err, out)
	}
	return strings.TrimSpace(string(out))
}

// mirrorSays is what `git push --mirror` writes to a pre-push hook of this repository:
// every ref of it, the commit it holds here, the ref on the host and the commit of it —
// four words to a line, which is what the hook of a worktree reads (docs/DESIGN.md §7i).
func mirrorSays(t *testing.T, repo, worktree string) string {
	t.Helper()
	listed, err := runGit(repo, "for-each-ref", "--format=%(refname) %(objectname)")
	if err != nil {
		t.Fatalf("the refs of the repository: %v", err)
	}
	var said strings.Builder
	for _, line := range split(listed) {
		ref, commit, _ := strings.Cut(line, " ")
		fmt.Fprintf(&said, "%s %s %s %s\n", ref, commit, ref, commit)
	}
	// The snapshot of a run is a ref of the repository as well, and a mirror sends it like
	// any other: it is the last line, and the hook has to stop on it.
	fmt.Fprintf(&said, "%s %s %s %s\n", checkpoints+"/43/1", headOf(t, worktree),
		checkpoints+"/43/1", headOf(t, worktree))
	return said.String()
}

// runHook is what a pre-push hook said and what it exited with, given the lines git writes
// to it.
func runHook(t *testing.T, hook, said string) (string, int) {
	t.Helper()
	cmd := exec.Command(hook)
	cmd.Stdin = strings.NewReader(said)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("run the hook: %v", err)
		}
		code = exit.ExitCode()
	}
	return string(out), code
}

// putBack is the one command of git that the record of the work names.
func putBack(t *testing.T, worktree, ref string) {
	t.Helper()
	if _, err := runGit(worktree, "cherry-pick", "--no-commit", ref); err != nil {
		t.Fatalf("put the work back out of %s: %v", ref, err)
	}
}
