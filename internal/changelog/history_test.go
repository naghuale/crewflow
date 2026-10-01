package changelog

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A fragment of a task as it stands on the disk of a test: one line, its own section, the
// link to its task.
func fragmentFile(task int, what string) string {
	return "### Добавлено\n\n- **" + what + ".** Строка задачи " + itoa(task) + ".\n" +
		"  (задача [#" + itoa(task) + "](https://github.com/naghuale/crewflow/issues/" + itoa(task) + "))\n"
}

func itoa(number int) string { return strconv.Itoa(number) }

// TestMergedIsTheOrderOfTheMerges: the lines of the journal stand in the order the
// changes were merged, and not in the order of the numbers of the tasks — a task with a
// low number merged after a task with a high one is read first.
func TestMergedIsTheOrderOfTheMerges(t *testing.T) {
	repo := gitIn(t)
	repo.write(File, journalOfTheTest)
	repo.write("changelog.d/124.md", fragmentFile(124, "первая строка"))
	repo.commit("feat: 124")
	repo.write("changelog.d/77.md", fragmentFile(77, "вторая строка"))
	repo.commit("feat: 77")

	order, err := Merged(repo.Run, repo.dir)(context.Background(), []int{77, 124})
	if err != nil {
		t.Fatalf("Merged: %v", err)
	}
	if want := []int{77, 124}; !equal(order, want) {
		t.Errorf("Merged read the order %v, want %v", order, want)
	}
}

// TestMergedOfAFragmentThatIsNotInTheHistoryYet: a task writes its fragment before it
// commits it, and the fragment of a task that is not in the history yet stands after every
// other one — by number, so that the journal is the same file on every machine.
func TestMergedOfAFragmentThatIsNotInTheHistoryYet(t *testing.T) {
	repo := gitIn(t)
	repo.write(File, journalOfTheTest)
	repo.write("changelog.d/124.md", fragmentFile(124, "первая строка"))
	repo.commit("feat: 124")
	repo.write("changelog.d/130.md", fragmentFile(130, "вторая строка"))

	order, err := Merged(repo.Run, repo.dir)(context.Background(), []int{124, 130})
	if err != nil {
		t.Fatalf("Merged: %v", err)
	}
	if want := []int{124, 130}; !equal(order, want) {
		t.Errorf("Merged read the order %v, want %v", order, want)
	}
}

// TestMergedOfTwoChangesOfOneSecond: two branches merged one after another carry the same
// second, and the journal of them must not depend on which of the two git walked first —
// the number of the task is in the fragment itself.
func TestMergedOfTwoChangesOfOneSecond(t *testing.T) {
	repo := gitIn(t)
	repo.write(File, journalOfTheTest)
	repo.write("changelog.d/124.md", fragmentFile(124, "строка 124"))
	repo.write("changelog.d/128.md", fragmentFile(128, "строка 128"))
	repo.must("add", "-A")
	repo.commit("feat: two tasks at once")

	order, err := Merged(repo.Run, repo.dir)(context.Background(), []int{124, 128})
	if err != nil {
		t.Fatalf("Merged: %v", err)
	}
	if want := []int{128, 124}; !equal(order, want) {
		t.Errorf("Merged read the order %v, want %v", order, want)
	}
}

// TestMergedTakesTheCommitThatAddedTheFragment: the work of a task going on after its
// merge is not a new entry in the journal — the line of the task keeps the place where it
// entered.
func TestMergedTakesTheCommitThatAddedTheFragment(t *testing.T) {
	repo := gitIn(t)
	repo.write(File, journalOfTheTest)
	repo.write("changelog.d/124.md", fragmentFile(124, "строка 124"))
	repo.commit("feat: 124")
	repo.write("changelog.d/77.md", fragmentFile(77, "строка 77"))
	repo.commit("feat: 77")
	repo.write("changelog.d/124.md", fragmentFile(124, "строка 124 и ещё"))
	repo.commit("fix: 124 wording")

	order, err := Merged(repo.Run, repo.dir)(context.Background(), []int{124, 77})
	if err != nil {
		t.Fatalf("Merged: %v", err)
	}
	if want := []int{77, 124}; !equal(order, want) {
		t.Errorf("Merged read the order %v, want %v", order, want)
	}
}

// TestMergedOutsideACheckout: git that said no is a refusal with what it said, and not a
// silent order by number — a journal read in the wrong order looks like a journal read in
// the right one.
func TestMergedOutsideACheckout(t *testing.T) {
	_, err := Merged(func(_ context.Context, _ string, _ []string, _ string) ([]byte, []byte, int, error) {
		return nil, []byte("fatal: not a git repository"), 128, nil
	}, t.TempDir())(context.Background(), []int{124})
	if err == nil {
		t.Fatal("Merged read an order out of a folder that is not a checkout, want a refusal")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("Merged refused with %q, want what git said in it", err)
	}
}

// TestTwoBranchesWithTheirOwnFragmentsMergeWithoutAConflict: two tasks that touch nothing
// of each other are merged one after another, and the second merge has nothing to resolve
// (CA-001, CL-003). Before the fragments of the journal every task wrote the same file, and
// the second of them was refused until the first was merged again — the whole of what
// `changelog.d/` is for.
func TestTwoBranchesWithTheirOwnFragmentsMergeWithoutAConflict(t *testing.T) {
	repo := gitIn(t)
	repo.write(File, journalOfTheTest)
	repo.write("changelog.d/77.md", fragmentFile(77, "строка 77"))
	repo.commit("feat: 77")

	repo.must("checkout", "-q", "-b", "crewflow/124")
	repo.write("changelog.d/124.md", fragmentFile(124, "строка 124"))
	repo.commit("feat: 124")

	repo.must("checkout", "-q", "main")
	repo.must("checkout", "-q", "-b", "crewflow/128")
	repo.write("changelog.d/128.md", fragmentFile(128, "строка 128"))
	repo.commit("feat: 128")

	repo.must("checkout", "-q", "main")
	repo.must("merge", "-q", "--no-ff", "-m", "merge 124", "crewflow/124")
	if left := repo.must("status", "--porcelain"); left != "" {
		t.Fatalf("after the first merge the tree holds %q, want nothing", left)
	}
	repo.must("merge", "-q", "--no-ff", "-m", "merge 128", "crewflow/128")
	if left := repo.must("status", "--porcelain"); left != "" {
		t.Fatalf("after the second merge the tree holds %q, want nothing: the fragments of two tasks went into one file", left)
	}
	if _, err := os.Stat(filepath.Join(repo.dir, "changelog.d", "124.md")); err != nil {
		t.Fatalf("after the merge the fragment of 124 is gone: %v", err)
	}
	project := Project{Root: repo.dir, Language: Russian, Order: Merged(repo.Run, repo.dir)}
	built, err := project.Build(context.Background())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, line := range []string{"строка 77", "строка 124", "строка 128"} {
		if !strings.Contains(string(built), line) {
			t.Errorf("the journal holds no line of %q:\n%s", line, built)
		}
	}
}

// TestEditingOneFragmentLeavesTheOthers: a task rewrites its own fragment as often as it
// needs, and no other fragment of the folder moves a byte — the lines of the journal are
// read out of every fragment, and every fragment is its own file (CA-003, CL-002).
func TestEditingOneFragmentLeavesTheOthers(t *testing.T) {
	repo := gitIn(t)
	repo.write(File, journalOfTheTest)
	repo.write("changelog.d/77.md", fragmentFile(77, "строка 77"))
	repo.write("changelog.d/124.md", fragmentFile(124, "строка 124"))
	repo.commit("feat: 77 and 124")

	untouched := repo.read("changelog.d/77.md")
	repo.write("changelog.d/124.md", fragmentFile(124, "строка 124, дописанная задачей"))

	if got := repo.read("changelog.d/77.md"); got != untouched {
		t.Errorf("the fragment of 77 changed with the edit of the fragment of 124:\n%q", got)
	}
	project := Project{Root: repo.dir, Language: Russian, Order: Merged(repo.Run, repo.dir)}
	built, err := project.Build(context.Background())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(string(built), "строка 124, дописанная задачей") {
		t.Errorf("the journal does not hold the edited line:\n%s", built)
	}
	if strings.Contains(string(built), "- **строка 124. **") {
		t.Errorf("the journal still holds the line before the edit:\n%s", built)
	}
}

// equal is whether two orders of tasks are the same order.
func equal(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
