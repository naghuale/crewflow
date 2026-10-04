package merge

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	taskrun "github.com/naghuale/crewflow/internal/run"
)

// A checkout with the work of a task in it is not a folder of a run that is over: the
// cleanup after a merge is the last road by which work that was never committed can be
// lost without anybody deciding to lose it, and D-088 is the decision that it is not lost
// (D-088, docs/DESIGN.md §7a, §7h).

// TestACheckoutWithWorkOutsideTheCommitsIsNotTakenAway: the refusal is one refusal on every
// road that takes a checkout away, and it is told apart from the other refusals of git by
// `errors.Is` — a merge that went through and a folder that still holds the work of a task
// are two different facts, and a caller has to be able to say which is which.
func TestACheckoutWithWorkOutsideTheCommitsIsNotTakenAway(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not installed: %v", err)
	}
	repo := newRepository(t)
	checkout := filepath.Join(t.TempDir(), "worktree")
	repo.git(t, repo.checkout, "worktree", "add", "--quiet", "-b", "crewflow/43-task", checkout, "main")
	worktree := Git{Dir: repo.checkout, Branch: "main", Run: repo.run}
	// The work of a run of a task: one file changed and one file that is in the folder and
	// in no tree at all.
	if err := os.WriteFile(filepath.Join(checkout, "the-work-of-a-run.go"),
		[]byte("package merge\n"), 0o600); err != nil {
		t.Fatalf("write the work of the run: %v", err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "second.go"),
		[]byte("package merge\n"), 0o600); err != nil {
		t.Fatalf("write the work of the run: %v", err)
	}

	err := worktree.RemoveWorktree(t.Context(), checkout)

	if !errors.Is(err, taskrun.ErrUncommittedWork) {
		t.Fatalf("the worktree of the task was taken away with %v, want the refusal %q",
			err, taskrun.ErrUncommittedWork)
	}
	if _, err := os.Stat(checkout); err != nil {
		t.Errorf("the worktree of the task is gone: %v", err)
	}
	// The record of the work is nowhere in git after the refusal: a snapshot is not what
	// lets the folder go, it may be incomplete, and this path never writes one.
	if held, _, _, err := repo.run(t.Context(), "git",
		[]string{"for-each-ref", "--format=%(refname)", "refs/crewflow/"}, repo.checkout); err != nil || len(held) > 0 {
		t.Errorf("the merge wrote a snapshot of the work: %q (%v), want none", held, err)
	}
}

// TestACheckoutWithNothingOutsideTheCommitsIsTakenAway: the refusal is about the work and
// not about the folder — a merge that came through leaves nothing behind, and a rule that
// kept every worktree of every merged task would be a rule a person turns off.
func TestACheckoutWithNothingOutsideTheCommitsIsTakenAway(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not installed: %v", err)
	}
	repo := newRepository(t)
	checkout := filepath.Join(t.TempDir(), "worktree")
	repo.git(t, repo.checkout, "worktree", "add", "--quiet", "-b", "crewflow/43-task", checkout, "main")
	worktree := Git{Dir: repo.checkout, Branch: "main", Run: repo.run}

	if err := worktree.RemoveWorktree(t.Context(), checkout); err != nil {
		t.Fatalf("take the worktree of a task away: %v", err)
	}

	if _, err := os.Stat(checkout); !os.IsNotExist(err) {
		t.Errorf("the worktree of the task is still there: %v", err)
	}
}
