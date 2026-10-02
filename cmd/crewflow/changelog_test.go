package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/changelog"
	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/forge"
)

// The journal of a project is built out of the fragments of its tasks, and the order of
// the lines comes out of the history of the branch: these tests run the real git in a
// checkout of their own, so that the journal of a test is built out of real commits and
// the commands reach neither the checkout nor the host of the person who runs them.

// A journal as a project has it before its fragments: the head, the unreleased part with a
// line a person wrote in it, the version below and the link at the end.
const journalOfTheTest = `# Changelog

Здесь — что изменилось в каждой версии.

## [Не выпущено]

### Добавлено

- строка, написанная руками

## [v0.1.0] - 2026-10-01

### Добавлено

- строка выпуска
  ([#1](https://github.com/naghuale/crewflow/pull/1), задача [#1](https://github.com/naghuale/crewflow/issues/1))

[Не выпущено]: https://github.com/naghuale/crewflow/commits/main
`

// The config of the project of a test: the language of the journal is the only thing these
// commands read of the file.
const changelogConfig = `
[project]
repo = "naghuale/crewflow"
default_branch = "main"
language = "ru"

[forge]
kind = "github"

[executor]
command = ["agent", "run", "--dir", "{worktree}", "{prompt}"]
`

// repository is a checkout of a test with a journal and the fragments of its tasks in it,
// committed at moments of their own.
type repository struct {
	gitOfTheTest
	t    *testing.T
	date string
}

// repositoryIn is a checkout of a test with a journal and the fragments of two tasks in it:
// 77 was merged first, 124 second.
func repositoryIn(t *testing.T) *repository {
	t.Helper()
	repo := &repository{
		gitOfTheTest: gitOfTheTest{
			repo: t.TempDir(),
			home: t.TempDir(),
			environment: settingsOf(map[string]string{
				"user.name":          "crewflow test",
				"user.email":         "test@crewflow.invalid",
				"commit.gpgsign":     "false",
				"init.defaultBranch": "main",
			}),
		},
		t:    t,
		date: "2026-10-02T09:00:00Z",
	}
	repo.git("init", "-q", ".")
	repo.write(changelog.File, journalOfTheTest)
	repo.fragment(77, "строка 77")
	repo.commit("feat: 77")
	repo.fragment(124, "строка 124")
	repo.commit("feat: 124")
	return repo
}

func (r *repository) git(args ...string) string {
	r.t.Helper()
	return git(r.t, r.gitOfTheTest, args...)
}

// commit is one commit of the test, an hour later than the one before it: the order of the
// merges is the order of the commits and not the speed of the machine.
func (r *repository) commit(message string) {
	r.t.Helper()
	moment, err := time.Parse(time.RFC3339, r.date)
	if err != nil {
		r.t.Fatalf("the moment of the test %q: %v", r.date, err)
	}
	r.date = moment.Add(time.Hour).Format(time.RFC3339)
	on := r.gitOfTheTest
	on.machine = map[string]string{"GIT_AUTHOR_DATE": r.date, "GIT_COMMITTER_DATE": r.date}
	git(r.t, on, "add", "-A")
	git(r.t, on, "commit", "-q", "-m", message)
}

func (r *repository) write(name, content string) {
	r.t.Helper()
	full := filepath.Join(r.repo, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.t.Fatalf("make a folder for %s: %v", name, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		r.t.Fatalf("write %s: %v", name, err)
	}
}

func (r *repository) read(name string) string {
	r.t.Helper()
	content, err := os.ReadFile(filepath.Join(r.repo, filepath.FromSlash(name)))
	if err != nil {
		r.t.Fatalf("read %s: %v", name, err)
	}
	return string(content)
}

// fragment is the file of a task with one line of the journal in it.
func (r *repository) fragment(task int, what string) {
	r.t.Helper()
	number := strconv.Itoa(task)
	r.write(changelog.Dir+"/"+number+".md", "### Добавлено\n\n- **"+what+".** Строка задачи "+number+".\n"+
		"  (задача [#"+number+"](https://github.com/naghuale/crewflow/issues/"+number+"))\n")
}

// changelogOf is a command of the journal against the checkout of a test.
func changelogOf(repo *repository, args ...string) []string {
	return append(args, "-config", writeConfig(repo.t, changelogConfig), "-repo", repo.repo)
}

// theHostIs makes the host of a test answer for the tasks of the project, and puts the
// roles back when the test is over. Nil is a project that is not hosted anywhere.
func theHostIs(t *testing.T, tracker forge.Tracker) {
	t.Helper()
	was := taskRoles
	t.Cleanup(func() { taskRoles = was })
	taskRoles = func(config.Config, forge.Env) (forge.Set, error) {
		return forge.Set{Tracker: tracker}, nil
	}
}

// theHost is a host that has the tasks of the project and nothing else.
func theHost(number int, tasks ...int) *host {
	h := &host{task: forge.Task{Number: number}, tasks: map[int]forge.Task{}}
	for _, task := range tasks {
		h.tasks[task] = forge.Task{Number: task}
	}
	return h
}

// TestChangelogBuild: the unreleased part of the journal is written out of the fragments,
// and a build over a journal that is already what they build writes nothing and says so.
func TestChangelogBuild(t *testing.T) {
	repo := repositoryIn(t)
	var stdout, stderr bytes.Buffer
	if code := run(changelogOf(repo, "changelog", "build"), &stdout, &stderr); code != exitOK {
		t.Fatalf("changelog build = %d, want 0 (stderr: %q)", code, stderr.String())
	}
	if want := "changelog: built CHANGELOG.md out of every fragment in changelog.d\n"; stdout.String() != want {
		t.Errorf("changelog build wrote %q, want %q", stdout.String(), want)
	}
	journal := repo.read(changelog.File)
	for _, line := range []string{"строка 124", "строка 77"} {
		if !strings.Contains(journal, line) {
			t.Errorf("the journal holds no line %q:\n%s", line, journal)
		}
	}
	if strings.Contains(journal, "строка, написанная руками") {
		t.Errorf("the journal kept the line that was written by hand:\n%s", journal)
	}

	stdout.Reset()
	stderr.Reset()
	if code := run(changelogOf(repo, "changelog", "build"), &stdout, &stderr); code != exitOK {
		t.Fatalf("the second changelog build = %d, want 0 (stderr: %q)", code, stderr.String())
	}
	if want := "changelog: CHANGELOG.md is already what the fragments build\n"; stdout.String() != want {
		t.Errorf("the second changelog build wrote %q, want %q", stdout.String(), want)
	}
}

// TestChangelogBuildInACheckoutWithoutAJournal: a build has nowhere to write to in a
// folder that has no journal, and it says which file is missing.
func TestChangelogBuildInACheckoutWithoutAJournal(t *testing.T) {
	empty := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run([]string{"changelog", "build",
		"-config", writeConfig(t, changelogConfig), "-repo", empty}, &stdout, &stderr)
	if code != exitFailure {
		t.Fatalf("changelog build = %d, want 1 (stderr: %q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), changelog.File) {
		t.Errorf("changelog build said %q, want the journal in it", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("changelog build wrote %q to stdout, want nothing", stdout.String())
	}
}

// TestChangelogCheckOfAWholeJournal: a journal that is what its fragments build, whose
// fragments are the tasks of the project and that say one thing each, is passed, and the
// answer says how much of it was looked at.
func TestChangelogCheckOfAWholeJournal(t *testing.T) {
	repo := repositoryIn(t)
	theHostIs(t, theHost(124, 77))
	var stdout, stderr bytes.Buffer
	run(changelogOf(repo, "changelog", "build"), &stdout, &stderr)
	stdout.Reset()
	stderr.Reset()
	if code := run(changelogOf(repo, "changelog", "check"), &stdout, &stderr); code != exitOK {
		t.Fatalf("changelog check = %d, want 0 (stdout: %q, stderr: %q)", code, stdout.String(), stderr.String())
	}
	want := "changelog: 2 fragments, 2 tasks asked about, CHANGELOG.md is what they build\n"
	if stdout.String() != want {
		t.Errorf("changelog check wrote %q, want %q", stdout.String(), want)
	}
}

// TestChangelogCheckFindsAJournalWrittenByHand: the journal is built and not written, and
// a line of it that no fragment builds is what the check says.
func TestChangelogCheckFindsAJournalWrittenByHand(t *testing.T) {
	repo := repositoryIn(t)
	theHostIs(t, theHost(124, 77))
	var stdout, stderr bytes.Buffer
	run(changelogOf(repo, "changelog", "build"), &stdout, &stderr)
	repo.write(changelog.File, strings.Replace(repo.read(changelog.File),
		"строка 77", "строка 77, дописанная руками", 1))
	stdout.Reset()
	stderr.Reset()
	if code := run(changelogOf(repo, "changelog", "check"), &stdout, &stderr); code != exitFailure {
		t.Fatalf("changelog check = %d, want 1 (stdout: %q)", code, stdout.String())
	}
	if want := "changelog: CHANGELOG.md: the journal is not what the fragments build"; !strings.Contains(stdout.String(), want) {
		t.Errorf("changelog check wrote %q, want it to hold %q", stdout.String(), want)
	}
}

// TestChangelogCheckFindsAFragmentOfATaskTheHostHasNot: a line of the journal that names a
// task nobody can go and read is a problem, and the check names the file and the refusal of
// the host.
func TestChangelogCheckFindsAFragmentOfATaskTheHostHasNot(t *testing.T) {
	repo := repositoryIn(t)
	theHostIs(t, theHost(124))
	var stdout, stderr bytes.Buffer
	run(changelogOf(repo, "changelog", "build"), &stdout, &stderr)
	repo.fragment(128, "строка 128")
	repo.commit("feat: 128")
	stdout.Reset()
	stderr.Reset()
	if code := run(changelogOf(repo, "changelog", "check"), &stdout, &stderr); code != exitFailure {
		t.Fatalf("changelog check = %d, want 1 (stdout: %q)", code, stdout.String())
	}
	for _, want := range []string{"changelog.d/128.md", "the host has no task #128"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("changelog check wrote %q, want it to hold %q", stdout.String(), want)
		}
	}
}

// TestChangelogCheckFindsAFragmentThatIsNotAFragment: the person who wrote the fragment is
// the one who has to see it, and the check says the file and the line.
func TestChangelogCheckFindsAFragmentThatIsNotAFragment(t *testing.T) {
	repo := repositoryIn(t)
	theHostIs(t, theHost(124, 77))
	repo.write(changelog.Dir+"/124.md", "### Убрано\n\n- строка\n")
	repo.commit("chore: a fragment that is not a fragment")
	var stdout, stderr bytes.Buffer
	if code := run(changelogOf(repo, "changelog", "check"), &stdout, &stderr); code != exitFailure {
		t.Fatalf("changelog check = %d, want 1 (stdout: %q)", code, stdout.String())
	}
	if want := `changelog: changelog.d/124.md:1: "Убрано" is not a section of the journal`; !strings.Contains(stdout.String(), want) {
		t.Errorf("changelog check wrote %q, want it to hold %q", stdout.String(), want)
	}
}

// TestChangelogCheckOfAProjectWithoutAHost: a project that is not hosted anywhere has no
// task to confirm, and its journal is checked as far as it goes — a check that refused to
// say anything would be a check that says nothing.
func TestChangelogCheckOfAProjectWithoutAHost(t *testing.T) {
	repo := repositoryIn(t)
	theHostIs(t, nil)
	var stdout, stderr bytes.Buffer
	run(changelogOf(repo, "changelog", "build"), &stdout, &stderr)
	stdout.Reset()
	stderr.Reset()
	if code := run(changelogOf(repo, "changelog", "check"), &stdout, &stderr); code != exitOK {
		t.Fatalf("changelog check = %d, want 0 (stdout: %q, stderr: %q)", code, stdout.String(), stderr.String())
	}
	want := "changelog: 2 fragments, 0 tasks asked about, CHANGELOG.md is what they build\n"
	if stdout.String() != want {
		t.Errorf("changelog check wrote %q, want %q", stdout.String(), want)
	}
}

// TestChangelogCheckAsJSON: a gate of a change reads the reason of a refusal, and not the
// words of it.
func TestChangelogCheckAsJSON(t *testing.T) {
	repo := repositoryIn(t)
	theHostIs(t, theHost(124))
	repo.fragment(128, "строка 128")
	repo.commit("feat: 128")
	var stdout, stderr bytes.Buffer
	run(changelogOf(repo, "changelog", "build"), &stdout, &stderr)
	stdout.Reset()
	if code := run(changelogOf(repo, "changelog", "check", "-json"), &stdout, &stderr); code != exitFailure {
		t.Fatalf("changelog check -json = %d, want 1 (stdout: %q)", code, stdout.String())
	}
	for _, want := range []string{`"problems"`, "the host has no task #128", `"reason": "the host has no such task"`} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("changelog check -json wrote %q, want it to hold %q", stdout.String(), want)
		}
	}
}

// TestChangelogRelease: the unreleased part becomes the section of the version with the day
// of the release, an empty unreleased part stands above it, and the fragments are gone.
func TestChangelogRelease(t *testing.T) {
	repo := repositoryIn(t)
	var stdout, stderr bytes.Buffer
	run(changelogOf(repo, "changelog", "build"), &stdout, &stderr)
	stdout.Reset()
	stderr.Reset()
	if code := run(changelogOf(repo, "changelog", "release", "v0.2.0", "-date", "2026-10-03"), &stdout, &stderr); code != exitOK {
		t.Fatalf("changelog release = %d, want 0 (stderr: %q)", code, stderr.String())
	}
	if want := "changelog: CHANGELOG.md is now ## [v0.2.0] - 2026-10-03\n"; !strings.HasPrefix(stdout.String(), want) {
		t.Errorf("changelog release wrote %q, want it to start with %q", stdout.String(), want)
	}
	journal := repo.read(changelog.File)
	if !strings.Contains(journal, "## [Не выпущено]\n\n## [v0.2.0] - 2026-10-03\n") {
		t.Errorf("the journal after the release does not stand the version under an empty unreleased part:\n%s", journal)
	}
	for _, fragment := range []string{"changelog.d/77.md", "changelog.d/124.md"} {
		if _, err := os.Stat(filepath.Join(repo.repo, fragment)); !os.IsNotExist(err) {
			t.Errorf("the fragment %s stands after the release, want it gone", fragment)
		}
	}
}

// TestChangelogReleaseOfAWordThatIsNotAVersion: a tag is the decision of a person, and a
// release does not guess at one.
func TestChangelogReleaseOfAWordThatIsNotAVersion(t *testing.T) {
	repo := repositoryIn(t)
	var stdout, stderr bytes.Buffer
	code := run(changelogOf(repo, "changelog", "release", "0.2"), &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("changelog release 0.2 = %d, want 2 (stderr: %q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "vMAJOR.MINOR.PATCH") {
		t.Errorf("changelog release said %q, want the form of a version in it", stderr.String())
	}
}

// TestChangelogCalledWrong: a call crewflow cannot read is a code 2 with the usage on
// stderr and nothing on stdout — a script tells it from a run that did what it was told.
func TestChangelogCalledWrong(t *testing.T) {
	cases := [][]string{
		{"changelog"},
		{"changelog", "write"},
		{"changelog", "build", "extra"},
		{"changelog", "check", "extra"},
		{"changelog", "build", "-nosuchflag"},
		{"changelog", "release"},
		{"changelog", "release", "v0.2.0", "v0.3.0"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(args, &stdout, &stderr); code != exitUsage {
				t.Errorf("run(%q) = %d, want %d (stdout: %q, stderr: %q)",
					args, code, exitUsage, stdout.String(), stderr.String())
			}
			if stdout.Len() != 0 {
				t.Errorf("run(%q) wrote %q to stdout, want nothing", args, stdout.String())
			}
			if !strings.Contains(stderr.String(), "Usage:") {
				t.Errorf("run(%q) wrote %q to stderr, want the usage", args, stderr.String())
			}
		})
	}
}

// TestChangelogHelp: `changelog help` is the usage of the program and nothing else.
func TestChangelogHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"changelog", "help"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("run(changelog help) = %d, want 0", code)
	}
	for _, want := range []string{"changelog build", "changelog check", "changelog release"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("the usage does not hold %q:\n%s", want, stdout.String())
		}
	}
}
