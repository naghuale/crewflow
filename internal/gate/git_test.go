package gate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/naghuale/crewflow/internal/doctor"
)

// TestMain points the home of the machine at a folder of the run of the tests, for
// every test of the package: git reads the settings of a person out of it, and a test
// that reads or writes there is a test that depends on the machine it runs on. The
// history of a change is asked of a real git, and git is asked of a repository the
// test made for itself — never of a repository of the person who runs the tests
// (docs/DESIGN.md §7h).
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "crewflow-gate-home-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "gate tests: make a home of their own: %v\n", err)
		os.Exit(1)
	}
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME"} {
		if err := os.Setenv(key, home); err != nil {
			fmt.Fprintf(os.Stderr, "gate tests: point %s at %s: %v\n", key, home, err)
			os.Exit(1)
		}
	}
	code := m.Run()
	if err := os.RemoveAll(home); err != nil {
		fmt.Fprintf(os.Stderr, "gate tests: take %s away: %v\n", home, err)
	}
	os.Exit(code)
}

// The names the commits of a repository of the test are known under. A test asks for
// a commit by its name, so that the two ends of a question about a history are the
// same commit in every test and not a moment of a clock.
const (
	baseCommit   = "base"
	firstCommit  = "first"
	secondCommit = "second"
	otherCommit  = "other"
)

// changeNumber is the number of the change request of the repository of the test:
// the ref of a pull request is what a review fetches the head of a change by.
const changeNumber = 7

// repository is a bare repository — what a host is — and a checkout of it, which is
// what a review asks its questions in. Both are made for one test and are gone with
// it, and neither of them is a repository of the person who runs the tests.
type repository struct {
	// remote is the bare repository, checkout the clone of it a review runs in.
	remote   string
	checkout string
	// who is the name of the person the commits of the test are made by, which is
	// the only way to commit without the settings of the machine.
	who []string
}

// newRepository is a bare repository with a history a gate can be asked about: a
// default branch with two commits, a branch of a change with a commit of its own and
// a ref of a pull request standing at it, and a branch on the side that is not in
// the history of either.
func newRepository(t *testing.T) *repository {
	t.Helper()
	root := t.TempDir()
	r := &repository{
		remote:   filepath.Join(root, "host.git"),
		checkout: filepath.Join(root, "checkout"),
		who: []string{
			"GIT_AUTHOR_NAME=crewflow tests", "GIT_AUTHOR_EMAIL=tests@crewflow.invalid",
			"GIT_COMMITTER_NAME=crewflow tests", "GIT_COMMITTER_EMAIL=tests@crewflow.invalid",
		},
	}
	r.git(t, "", "init", "--quiet", "--bare", "--initial-branch=main", r.remote)
	r.git(t, "", "init", "--quiet", "--initial-branch=main", r.checkout)
	r.git(t, r.checkout, "config", "commit.gpgsign", "false")
	r.git(t, r.checkout, "remote", "add", "origin", r.remote)
	r.commit(t, baseCommit)
	r.git(t, r.checkout, "branch", "change")
	r.git(t, r.checkout, "checkout", "--quiet", "change")
	r.commit(t, firstCommit)
	r.commit(t, secondCommit)
	// The ref of a pull request is what a review fetches: GitHub keeps the head of a
	// change request under a ref of its own, and that is the name of it (docs/DESIGN.md §7h).
	r.git(t, r.checkout, "push", "--quiet", "origin", "change:"+headRef(changeNumber))
	r.git(t, r.checkout, "checkout", "--quiet", "-b", otherCommit, "main")
	r.commit(t, otherCommit)
	r.git(t, r.checkout, "push", "--quiet", "origin", "main", "change", otherCommit)
	return r
}

// History is the checkout of the repository of the test, as a review is given it.
func (r *repository) history() History {
	return History{Dir: r.checkout, Branch: "main", Run: r.run}
}

// commit makes a commit on the branch that is checked out, under the name the tests
// know it by, and pushes nothing: the test pushes what it wants pushed.
func (r *repository) commit(t *testing.T, name string) {
	t.Helper()
	path := filepath.Join(r.checkout, "internal")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("make %s: %v", path, err)
	}
	if err := os.WriteFile(filepath.Join(path, name), []byte(name), 0o600); err != nil {
		t.Fatalf("write the file of the commit %s: %v", name, err)
	}
	r.git(t, r.checkout, "add", ".")
	r.git(t, r.checkout, "commit", "--quiet", "-m", "commit "+name)
}

// sha is the commit the branch of the test is at under that name, which is what a
// host would say the head of a change is.
func (r *repository) sha(t *testing.T, branch string) string {
	t.Helper()
	out, _, code, err := r.run(t.Context(), "git", []string{"rev-parse", branch}, r.checkout)
	if err != nil || code != 0 {
		t.Fatalf("the commit of %s: %q, %d, %v", branch, out, code, err)
	}
	return strings.TrimSpace(string(out))
}

// force pushes another commit over the branch of a change, which is how a history is
// rewritten under an approval (docs/DESIGN.md §7h).
func (r *repository) force(t *testing.T) string {
	t.Helper()
	r.git(t, r.checkout, "checkout", "--quiet", "change")
	r.git(t, r.checkout, "reset", "--quiet", "--hard", r.sha(t, "main"))
	r.commit(t, "rewritten")
	r.git(t, r.checkout, "push", "--quiet", "--force", "origin", "change:"+headRef(changeNumber))
	return r.sha(t, "change")
}

// git runs one git in dir and stops the test when it says no.
func (r *repository) git(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, stderr, code, err := r.run(t.Context(), "git", args, dir); err != nil || code != 0 {
		t.Fatalf("git %v: exited with %d: %s (%v)", args, code, stderr, err)
	}
}

// run is how git is started on the machine of the test: as a person would, in a
// folder, with the identity of the test under it.
func (r *repository) run(ctx context.Context, name string, args []string, dir string) ([]byte, []byte, int, error) {
	return doctor.Command(ctx, name, args, dir, r.who)
}

// TestPrepareFetchesTheHeadOfAChange is the first thing a review asks of git: the
// commit the head of the change is, has to be in the checkout, and it has to be the
// commit the host named. A ref that points somewhere else is a head that moved
// while it was being read, and a review of it would be a review of a commit nobody
// approved.
func TestPrepareFetchesTheHeadOfAChange(t *testing.T) {
	repo := newRepository(t)
	head := repo.sha(t, "change")

	if err := repo.history().Prepare(t.Context(), headRef(changeNumber), head); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	t.Run("the head moved while it was being read", func(t *testing.T) {
		err := repo.history().Prepare(t.Context(), headRef(changeNumber), repo.sha(t, "main"))
		if err == nil {
			t.Fatal("Prepare took the head of the change for the head of another branch")
		}
	})
	t.Run("the host names no ref for the head", func(t *testing.T) {
		if err := repo.history().Prepare(t.Context(), "", head); err == nil {
			t.Fatal("Prepare read a history without knowing what to fetch")
		}
	})
}

// TestAncestorOfTheDefaultBranch is the question a merge is refused on: the default
// branch of the project is an ancestor of the head of the change, so a merge of it is
// a fast-forward. When the default branch has moved on, it is not, and that is
// `not-fast-forward` (docs/DESIGN.md §7h).
func TestAncestorOfTheDefaultBranch(t *testing.T) {
	repo := newRepository(t)
	head := repo.sha(t, "change")

	ancestor, err := repo.history().DefaultIsAncestor(t.Context(), head)
	if err != nil {
		t.Fatalf("DefaultIsAncestor: %v", err)
	}
	if !ancestor {
		t.Errorf("DefaultIsAncestor = false, want main to be an ancestor of the head of the change")
	}

	repo.git(t, repo.checkout, "checkout", "--quiet", "main")
	repo.commit(t, "moved-on")
	repo.git(t, repo.checkout, "push", "--quiet", "origin", "main")

	ancestor, err = repo.history().DefaultIsAncestor(t.Context(), head)
	if err != nil {
		t.Fatalf("DefaultIsAncestor after the branch moved on: %v", err)
	}
	if ancestor {
		t.Errorf("DefaultIsAncestor = true, want false: main has moved on since the branch was cut")
	}
}

// TestAncestorOfAnApprovedCommit is what tells a stale approval from a rewritten
// history: an approved commit that is an ancestor of the head is work that has grown
// since the approval, and an approved commit that is not in the history at all is a
// history somebody rewrote (docs/DESIGN.md §7h).
func TestAncestorOfAnApprovedCommit(t *testing.T) {
	repo := newRepository(t)
	approved := repo.sha(t, "change~1")
	head := repo.sha(t, "change")

	ancestor, err := repo.history().Ancestor(t.Context(), approved, head)
	if err != nil {
		t.Fatalf("Ancestor: %v", err)
	}
	if !ancestor {
		t.Errorf("Ancestor = false, want the approved commit to be an ancestor of the head: a commit was added after the approval")
	}

	rewritten := repo.force(t)
	ancestor, err = repo.history().Ancestor(t.Context(), approved, rewritten)
	if err != nil {
		t.Fatalf("Ancestor after the force push: %v", err)
	}
	if ancestor {
		t.Errorf("Ancestor = true, want false: the history of the branch was rewritten under the approval")
	}
}

// TestAncestorOfACommitNobodyHas: an approved commit that this machine has never
// heard of is not an ancestor of anything. That is an answer — the commit is not in
// the history of the head — and not a question crewflow cannot answer, so the gate
// gets a fact and not a refusal.
func TestAncestorOfACommitNobodyHas(t *testing.T) {
	repo := newRepository(t)

	ancestor, err := repo.history().Ancestor(t.Context(),
		"0123456789abcdef0123456789abcdef01234567", repo.sha(t, "change"))
	if err != nil {
		t.Fatalf("Ancestor of a commit the checkout has never seen: %v, want a fact and not a refusal", err)
	}
	if ancestor {
		t.Error("Ancestor = true, want false: the checkout has never seen that commit")
	}
}

// TestAncestorWithNoGitAtAll: a machine that cannot start git cannot answer about a
// history, and the gate has to hear that as a refusal and not as a fast-forward.
func TestAncestorWithNoGitAtAll(t *testing.T) {
	history := History{Dir: t.TempDir(), Branch: "main"}

	if _, err := history.Ancestor(t.Context(), "a", "b"); err == nil {
		t.Error("Ancestor answered without a git to ask")
	}
	if err := history.Prepare(t.Context(), "refs/pull/7/head", "abc"); err == nil {
		t.Error("Prepare read a history without a git to ask")
	}
}

// headRef is the ref of the host that stands at the head of a change request: it is
// the name GitHub keeps the head of a pull request under, and a review fetches it to
// have the commit of the head in a checkout (docs/DESIGN.md §7h).
func headRef(number int) string {
	return "refs/pull/" + strconv.Itoa(number) + "/head"
}
