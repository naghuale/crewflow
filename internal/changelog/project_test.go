package changelog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// projectIn is a repository of a test with a journal and the fragments of two tasks in
// it, committed in the order they stand in the journal.
func projectIn(t *testing.T) *gitOfTheTest {
	t.Helper()
	repo := gitIn(t)
	repo.write(File, journalOfTheTest)
	repo.write("changelog.d/77.md", fragmentFile(77, "строка 77"))
	repo.commit("feat: 77")
	repo.write("changelog.d/124.md", "### Изменено\n\n- **строка 124.** Строка задачи 124.\n"+
		"  (задача [#124](https://github.com/naghuale/crewflow/issues/124))\n")
	repo.commit("feat: 124")
	return repo
}

// theProject is the journal of a repository of a test, with the order of the fragments
// read out of the history of the branch.
func theProject(repo *gitOfTheTest) Project {
	return Project{Root: repo.dir, Language: Russian, Order: Merged(repo.Run, repo.dir)}
}

// theProjectAtTip is the journal of a repository of a test as a check reads it in a
// checkout of a project on git: the journal is the build of the fragments of the tip of
// the default branch, which such a checkout has as `origin/main` after a fetch or a push.
func theProjectAtTip(repo *gitOfTheTest) Project {
	project := theProject(repo)
	project.Git, project.Tip = repo.Run, "origin/main"
	return project
}

// atTip is the tip of the default branch of a repository of a test: a checkout of a
// project on git has the branch of the host where the last fetch or push left it, and a
// test puts it where the branch of the project stands.
func atTip(repo *gitOfTheTest) {
	repo.t.Helper()
	repo.must("update-ref", "refs/remotes/origin/main", "HEAD")
}

// TestBuildWritesEveryFragmentIntoItsOwnSection: the unreleased part of the journal is
// built out of every fragment, each line under the section its own fragment named
// (CA-002, CL-001).
func TestBuildWritesEveryFragmentIntoItsOwnSection(t *testing.T) {
	repo := projectIn(t)
	built, err := theProject(repo).Build(context.Background())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := `### Добавлено

- **строка 77.** Строка задачи 77.
  (задача [#77](https://github.com/naghuale/crewflow/issues/77))

### Изменено

- **строка 124.** Строка задачи 124.
  (задача [#124](https://github.com/naghuale/crewflow/issues/124))`
	if !strings.Contains(string(built), want) {
		t.Errorf("the journal does not hold the lines of both fragments under their sections:\n%s", built)
	}
	if !strings.Contains(string(built), "## [v0.1.0] - 2026-10-01") {
		t.Errorf("the built journal lost the version below:\n%s", built)
	}
}

// TestWriteIsIdempotent: a build over a journal that is already what the fragments build
// does not write it at all — the file of the project is not touched for nothing, and the
// next change does not see a difference that is not one (CL-005).
func TestWriteIsIdempotent(t *testing.T) {
	repo := projectIn(t)
	project := theProject(repo)
	if written, err := project.Write(context.Background()); err != nil || !written {
		t.Fatalf("Write wrote %v, %v, want true, nil", written, err)
	}
	after := repo.read(File)
	if written, err := project.Write(context.Background()); err != nil || written {
		t.Fatalf("the second Write wrote %v, %v, want false, nil", written, err)
	}
	if got := repo.read(File); got != after {
		t.Errorf("the second Write changed the journal:\n%q\nwant\n%q", got, after)
	}
}

// TestBuildIsTheSameFileOnEveryMachine: the same set of fragments builds the same bytes
// whatever order the files were created in, whatever the filesystem hands over and
// whatever moment the build happened in (CA-006).
func TestBuildIsTheSameFileOnEveryMachine(t *testing.T) {
	first := gitIn(t)
	first.write(File, journalOfTheTest)
	first.write("changelog.d/124.md", fragmentFile(124, "строка 124"))
	first.write("changelog.d/77.md", fragmentFile(77, "строка 77"))
	first.commit("feat: 124 and 77")

	second := gitIn(t)
	second.write(File, journalOfTheTest)
	second.write("changelog.d/77.md", fragmentFile(77, "строка 77"))
	second.write("changelog.d/124.md", fragmentFile(124, "строка 124"))
	second.moment = first.moment
	second.commit("feat: 77 and 124, in another order on the disk")

	one, err := theProject(first).Build(context.Background())
	if err != nil {
		t.Fatalf("Build of the first: %v", err)
	}
	other, err := theProject(second).Build(context.Background())
	if err != nil {
		t.Fatalf("Build of the second: %v", err)
	}
	if string(one) != string(other) {
		t.Errorf("two builds of one set of fragments wrote different bytes:\n%s\n---\n%s", one, other)
	}
}

// TestCheckOfAWholeJournal: a journal that is what its fragments build, whose fragments
// are the tasks of the project and that say one thing each, is not a problem at all.
func TestCheckOfAWholeJournal(t *testing.T) {
	repo := projectIn(t)
	project := theProject(repo)
	if _, err := project.Write(context.Background()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	tasks := everyTaskIsThere
	problems, err := project.Check(context.Background(), tasks)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(problems) != 0 {
		t.Errorf("Check found %v in a whole journal, want nothing", problems)
	}
}

// TestCheckFindsEveryWrongThing: every reason a check has is a refusal with the file and
// what is wrong in it — a gate needs to tell them apart, and a person needs to see which
// file to open.
func TestCheckFindsEveryWrongThing(t *testing.T) {
	cases := []struct {
		name    string
		broken  func(repo *gitOfTheTest)
		tasks   Tasks
		reason  error
		want    string
		wantAny []string
	}{
		{
			name:   "a fragment that is not a fragment",
			broken: func(repo *gitOfTheTest) { repo.write("changelog.d/124.md", "### Убрано\n\n- строка\n") },
			reason: ErrFormat,
			want:   `changelog.d/124.md:1: "Убрано" is not a section of the journal`,
		},
		{
			name: "a task the host does not have",
			broken: func(repo *gitOfTheTest) {
				repo.write("changelog.d/128.md", fragmentFile(128, "строка 128"))
			},
			tasks: func(_ context.Context, number int) error {
				if number == 128 {
					return errors.New("the issue #128 does not exist")
				}
				return nil
			},
			reason:  ErrNoSuchTask,
			wantAny: []string{"changelog.d/128.md: the host has no task #128: the issue #128 does not exist"},
		},
		{
			name: "one line in two fragments",
			broken: func(repo *gitOfTheTest) {
				repo.write("changelog.d/0077.md", fragmentFile(77, "строка 77"))
			},
			reason: ErrDuplicate,
			wantAny: []string{
				"changelog.d/77.md: the task #77 has two fragments: changelog.d/0077.md and this one",
				"changelog.d/77.md:3: the line is in changelog.d/0077.md too — one line of the journal is one task",
			},
		},
		{
			name: "one task with two fragments",
			broken: func(repo *gitOfTheTest) {
				repo.write("changelog.d/0077.md", fragmentFile(77, "строка 77, вторая"))
			},
			reason:  ErrDuplicate,
			wantAny: []string{"changelog.d/77.md: the task #77 has two fragments: changelog.d/0077.md and this one"},
		},
		{
			name: "a journal written by hand",
			broken: func(repo *gitOfTheTest) {
				if _, err := theProject(repo).Write(context.Background()); err != nil {
					t.Fatalf("Write: %v", err)
				}
				repo.write(File, strings.Replace(repo.read(File), "строка 77", "строка 77, дописанная руками", 1))
			},
			reason: ErrOutOfSync,
			wantAny: []string{
				"CHANGELOG.md: the journal is not what the fragments build — run `crewflow changelog build`",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo := projectIn(t)
			c.broken(repo)
			repo.commit("chore: a project that went wrong")
			tasks := c.tasks
			if tasks == nil {
				tasks = everyTaskIsThere
			}
			problems, err := theProject(repo).Check(context.Background(), tasks)
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if len(problems) == 0 {
				t.Fatalf("Check found nothing, want the reason %v", c.reason)
			}
			var found bool
			for _, problem := range problems {
				if !errors.Is(problem, c.reason) {
					continue
				}
				found = true
				if c.want != "" && problem.Error() != c.want {
					t.Errorf("Check said\n%q\nwant\n%q", problem, c.want)
				}
				if len(c.wantAny) == 0 {
					continue
				}
				var named bool
				for _, want := range c.wantAny {
					if problem.Error() == want {
						named = true
					}
				}
				if !named {
					t.Errorf("Check said\n%q\nwant one of\n%q", problem, c.wantAny)
				}
			}
			if !found {
				t.Errorf("Check found %v, want the reason %v", problems, c.reason)
			}
		})
	}
}

// TestCheckOnTheDefaultBranchAsksForTheJournalOfEveryFragmentItHas: the journal of the
// default branch is the build of every fragment that stands in it, so a fragment that was
// merged into it without a rebuild of the journal is what the check has to name (CL-007).
func TestCheckOnTheDefaultBranchAsksForTheJournalOfEveryFragmentItHas(t *testing.T) {
	repo := projectIn(t)
	project := theProjectAtTip(repo)
	if _, err := project.Write(context.Background()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	repo.commit("chore(changelog): the journal of the two tasks")
	atTip(repo)
	if problems, err := project.Check(context.Background(), everyTaskIsThere); err != nil || len(problems) != 0 {
		t.Fatalf("Check of the built journal found %v, %v, want nothing", problems, err)
	}

	repo.write("changelog.d/128.md", fragmentFile(128, "строка 128"))
	repo.commit("feat: 128")
	atTip(repo)

	problems, err := project.Check(context.Background(), everyTaskIsThere)
	if err != nil {
		t.Fatalf("Check after the merge: %v", err)
	}
	if len(problems) != 1 || !errors.Is(problems[0], ErrOutOfSync) {
		t.Fatalf("Check after the merge found %v, want the one reason %v — a fragment merged into the default branch without a rebuild of the journal", problems, ErrOutOfSync)
	}
	want := "CHANGELOG.md: the journal is not what the fragments at origin/main build — " +
		"the journal of the default branch is built on it, after the merge"
	if got := problems[0].Error(); got != want {
		t.Errorf("Check said\n%q\nwant\n%q", got, want)
	}

	if _, err := project.Write(context.Background()); err != nil {
		t.Fatalf("Write after the merge: %v", err)
	}
	if problems, err := project.Check(context.Background(), everyTaskIsThere); err != nil || len(problems) != 0 {
		t.Errorf("Check after the build found %v, %v, want nothing", problems, err)
	}
}

// TestCheckOnABranchDoesNotAskForTheLineTheBranchBrings: a task writes only its own
// fragment and never the journal, so the line of the change is not expected in the
// journal before the merge — and the branch must not write it, or two branches with their
// own fragments would not merge one after another (CL-007, CL-003).
func TestCheckOnABranchDoesNotAskForTheLineTheBranchBrings(t *testing.T) {
	repo := projectIn(t)
	project := theProjectAtTip(repo)
	if _, err := project.Write(context.Background()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	repo.commit("chore(changelog): the journal of the two tasks")
	atTip(repo)

	repo.must("checkout", "-q", "-b", "crewflow/128")
	repo.write("changelog.d/128.md", fragmentFile(128, "строка 128"))
	repo.commit("feat: 128")

	if ref, inTip := project.Merged(context.Background()); !inTip || ref != "origin/main" {
		t.Errorf("Merged said %q, %v, want origin/main, true", ref, inTip)
	}
	if problems, err := project.Check(context.Background(), everyTaskIsThere); err != nil || len(problems) != 0 {
		t.Fatalf("Check on the branch found %v, %v, want nothing — the line of the branch goes into the journal when the default branch is built", problems, err)
	}
	if journal := repo.read(File); strings.Contains(journal, "строка 128") {
		t.Errorf("the branch wrote the line of its own fragment into the journal:\n%s", journal)
	}
}

// TestCheckOnABranchAfterTheChangeBuiltTheJournal: a task that has the journal in its
// boundaries may build it, and the journal it builds is the build of the fragments of the
// tip with the fragments of the change added to them — the second of the two journals the
// check accepts in a branch (CL-007).
func TestCheckOnABranchAfterTheChangeBuiltTheJournal(t *testing.T) {
	repo := projectIn(t)
	project := theProjectAtTip(repo)
	if _, err := project.Write(context.Background()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	repo.commit("chore(changelog): the journal of the two tasks")
	atTip(repo)

	repo.must("checkout", "-q", "-b", "crewflow/128")
	repo.write("changelog.d/128.md", fragmentFile(128, "строка 128"))
	repo.commit("feat: 128")
	if _, err := project.Write(context.Background()); err != nil {
		t.Fatalf("Write on the branch: %v", err)
	}
	repo.commit("chore(changelog): the line of 128")

	if journal := repo.read(File); !strings.Contains(journal, "строка 128") {
		t.Fatalf("the build of the branch left the line of its own fragment out:\n%s", journal)
	}
	if problems, err := project.Check(context.Background(), everyTaskIsThere); err != nil || len(problems) != 0 {
		t.Fatalf("Check of the journal the branch built found %v, %v, want nothing", problems, err)
	}

	repo.write(File, strings.Replace(repo.read(File), "строка 128", "строка 128, дописанная руками", 1))
	repo.commit("fix(changelog): a line written by hand")
	problems, err := project.Check(context.Background(), everyTaskIsThere)
	if err != nil {
		t.Fatalf("Check of the journal written by hand: %v", err)
	}
	if len(problems) != 1 || !errors.Is(problems[0], ErrOutOfSync) {
		t.Fatalf("Check found %v, want the one reason %v — a journal that is neither of the two builds", problems, ErrOutOfSync)
	}
	want := "CHANGELOG.md: the journal is neither the build of the fragments at origin/main nor the build of " +
		"the fragments of this checkout — run `crewflow changelog build`"
	if got := problems[0].Error(); got != want {
		t.Errorf("Check said\n%q\nwant\n%q", got, want)
	}
}

// TestCheckOnABranchAfterATaskRewroteItsOwnFragment: the journal is the build of the
// fragments the default branch has, and a task that rewrites its own fragment after the
// merge has not broken anything yet — its line changes in the journal when the default
// branch is built, and not before (CL-007).
func TestCheckOnABranchAfterATaskRewroteItsOwnFragment(t *testing.T) {
	repo := projectIn(t)
	project := theProjectAtTip(repo)
	if _, err := project.Write(context.Background()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	repo.commit("chore(changelog): the journal of the two tasks")
	atTip(repo)

	repo.must("checkout", "-q", "-b", "crewflow/124")
	repo.write("changelog.d/124.md", "### Изменено\n\n- **строка 124, дописанная задачей.** Строка задачи 124.\n"+
		"  (задача [#124](https://github.com/naghuale/crewflow/issues/124))\n")
	repo.commit("fix(changelog): its own line once more")

	if problems, err := project.Check(context.Background(), everyTaskIsThere); err != nil || len(problems) != 0 {
		t.Errorf("Check found %v, %v, want nothing — the journal is the build of the fragments of the tip, and the fragment of the branch is not one of them yet", problems, err)
	}
}

// TestCheckWithoutATipAsksForEveryFragmentThatStands: a project that names no default
// branch, or a checkout that never fetched it, has no tip to read the fragments of — and
// then the journal is the build of every fragment that stands, which is the strictest
// reading of the rule and the one a checkout outside a project on git gets.
func TestCheckWithoutATipAsksForEveryFragmentThatStands(t *testing.T) {
	repo := projectIn(t)
	project := theProject(repo)
	if _, err := project.Write(context.Background()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	repo.write("changelog.d/128.md", fragmentFile(128, "строка 128"))
	repo.commit("feat: 128")

	if ref, inTip := project.Merged(context.Background()); inTip || ref != "" {
		t.Errorf("Merged said %q, %v, want nothing, false — a checkout with no tip of the default branch has none to name", ref, inTip)
	}
	problems, err := project.Check(context.Background(), everyTaskIsThere)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(problems) != 1 || !errors.Is(problems[0], ErrOutOfSync) {
		t.Fatalf("Check found %v, want the one reason %v", problems, ErrOutOfSync)
	}
	want := "CHANGELOG.md: the journal is not what the fragments build — run `crewflow changelog build`"
	if got := problems[0].Error(); got != want {
		t.Errorf("Check said\n%q\nwant\n%q", got, want)
	}
}

// TestCheckAsksTheHostOnceAboutEveryTask: the host is asked about a number once, in the
// order the fragments stand on the disk, so that a check of the same journal twice asks
// the same questions of the host.
func TestCheckAsksTheHostOnceAboutEveryTask(t *testing.T) {
	repo := projectIn(t)
	repo.write("changelog.d/128.md", fragmentFile(128, "строка 128"))
	repo.write("changelog.d/0124.md", fragmentFile(124, "строка 124 ещё раз"))
	repo.commit("chore: two fragments of one task")
	var asked []int
	tasks := func(_ context.Context, number int) error {
		asked = append(asked, number)
		return nil
	}
	if _, err := theProject(repo).Check(context.Background(), tasks); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if want := []int{124, 128, 77}; !equal(asked, want) {
		t.Errorf("Check asked the host about %v, want %v — one question about each task, in the order the fragments stand on the disk", asked, want)
	}
}

// TestCheckLeavesTheJournalAloneWhenAFragmentIsBroken: a fragment that is not a fragment
// has no journal it could be compared with, and a check that said it twice would name one
// problem twice.
func TestCheckLeavesTheJournalAloneWhenAFragmentIsBroken(t *testing.T) {
	repo := projectIn(t)
	repo.write("changelog.d/124.md", "### Убрано\n\n- строка\n")
	problems, err := theProject(repo).Check(context.Background(), everyTaskIsThere)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	for _, problem := range problems {
		if errors.Is(problem, ErrOutOfSync) {
			t.Errorf("Check said the journal is out of sync as well:\n%v", problem)
		}
	}
}

// TestCheckWithoutAHost: a project whose host is not there is checked as far as it goes,
// and the task of every fragment is not among the things it found — a check that refused
// to say anything would be a check that says nothing.
func TestCheckWithoutAHost(t *testing.T) {
	repo := projectIn(t)
	if _, err := theProject(repo).Write(context.Background()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	problems, err := theProject(repo).Check(context.Background(), nil)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(problems) != 0 {
		t.Errorf("Check found %v in a whole journal, want nothing", problems)
	}
}

// TestReleaseMovesTheFragmentsIntoTheSectionOfTheVersion: a release gives the unreleased
// part the number and the day of it, leaves an empty unreleased part above and takes the
// fragments away — one commit, and no line of the release written into the next one.
func TestReleaseMovesTheFragmentsIntoTheSectionOfTheVersion(t *testing.T) {
	repo := projectIn(t)
	cut, err := theProject(repo).Release(context.Background(), "v0.2.0", "2026-10-03")
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if cut.Version != "v0.2.0" || cut.Date != "2026-10-03" || cut.Journal != File {
		t.Errorf("Release cut %+v, want the version v0.2.0, the day 2026-10-03 and the journal", cut)
	}
	if want := []string{"changelog.d/124.md", "changelog.d/77.md"}; !samePaths(cut.Moved, want) {
		t.Errorf("Release moved %v, want %v in the order the journal read them", cut.Moved, want)
	}
	for _, fragment := range cut.Moved {
		if !repo.gone(fragment) {
			t.Errorf("the fragment %s stands after the release, want it gone", fragment)
		}
	}
	journal := repo.read(File)
	want := `## [Не выпущено]

## [v0.2.0] - 2026-10-03

### Добавлено

- **строка 77.** Строка задачи 77.
  (задача [#77](https://github.com/naghuale/crewflow/issues/77))

### Изменено

- **строка 124.** Строка задачи 124.
  (задача [#124](https://github.com/naghuale/crewflow/issues/124))

## [v0.1.0] - 2026-10-01`
	if !strings.Contains(journal, want) {
		t.Errorf("the journal after the release does not hold\n%q\nbut holds\n%q", want, journal)
	}
}

// TestTheJournalOfAReleaseIsWhatTheFragmentsBuild: a journal with nothing unreleased in
// it is a journal a build over it does not change — otherwise the first build after a
// release would put a section of nothing under the heading.
func TestTheJournalOfAReleaseIsWhatTheFragmentsBuild(t *testing.T) {
	repo := projectIn(t)
	if _, err := theProject(repo).Release(context.Background(), "v0.2.0", "2026-10-03"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	project := theProject(repo)
	if written, err := project.Write(context.Background()); err != nil || written {
		t.Fatalf("Write after a release wrote %v, %v, want false, nil", written, err)
	}
}

// TestReadOfAProjectWithoutFragments: a project that has released everything has no folder
// of the fragments, and that is not a refusal — there is simply nothing to build from.
func TestReadOfAProjectWithoutFragments(t *testing.T) {
	project := Project{Root: t.TempDir(), Language: Russian}
	fragments, err := project.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(fragments) != 0 {
		t.Errorf("Read found %v, want nothing", fragments)
	}
}

// TestAnOrderThatIsNotTheOrderOfTheFragmentsIsRefused: a journal that read half of the
// fragments, or read one of them twice, cannot be followed back to a task.
func TestAnOrderThatIsNotTheOrderOfTheFragmentsIsRefused(t *testing.T) {
	repo := projectIn(t)
	project := theProject(repo)
	project.Order = func(context.Context, []int) ([]int, error) { return []int{124}, nil }
	if _, err := project.Build(context.Background()); err == nil {
		t.Fatal("Build read an order that is not the order of the fragments, want a refusal")
	}
	project.Order = func(context.Context, []int) ([]int, error) {
		return nil, errors.New("git log: the repository is gone")
	}
	if _, err := project.Build(context.Background()); err == nil {
		t.Fatal("Build ignored what the order refused with, want the refusal")
	}
}

// everyTaskIsThere is a host that has every task of the project.
func everyTaskIsThere(context.Context, int) error { return nil }

// samePaths is whether one list of the paths of the fragments is the same list as the
// other, said here in the words the tests mean by it.
func samePaths(got, want []string) bool {
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

// TestWriteRefusesWhenTheJournalIsNotThere: a build has nowhere to write to in a folder
// that has no journal, and it says which file is missing.
func TestWriteRefusesWhenTheJournalIsNotThere(t *testing.T) {
	project := Project{Root: t.TempDir(), Language: Russian}
	_, err := project.Write(context.Background())
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Write refused with %v, want the reason that the file is not there", err)
	}
	if !strings.Contains(err.Error(), filepath.ToSlash(File)) {
		t.Errorf("Write refused with %q, want the journal in it", err)
	}
}
