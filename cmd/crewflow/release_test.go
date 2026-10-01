package main

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/changelog"
)

// The script that cuts a release of the project is checked on a copy of it: it commits,
// builds a binary and puts a tag, and none of that may happen in the checkout of the
// person who runs the tests. The copy is the module as it stands on the disk — without
// `.git`, without the scratch of a run — with a repository of its own, and the script is
// run there exactly as it is run in the root of the project.

// TestReleaseScriptCutsAVersionOnACopyOfTheProject: a release gathers the fragments into
// the section of the version with the day of the release, leaves the unreleased section
// empty above it, takes the fragments away and does all of it in one commit with the tag
// on it. What a person has to do by hand afterwards is pushing — a tag is their decision.
func TestReleaseScriptCutsAVersionOnACopyOfTheProject(t *testing.T) {
	if testing.Short() {
		t.Skip("the script builds a binary of the project, and a short run is not the place for it")
	}
	copyOfProject := copiedProjectIn(t)
	on := repositoryOf(t, copyOfProject)

	ctx, stop := context.WithTimeout(context.Background(), 10*time.Minute)
	defer stop()
	command := exec.CommandContext(ctx, "sh", filepath.Join("scripts", "release.sh"), "v9.9.9")
	command.Dir = copyOfProject
	command.Env = machineOfTheTest(t)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("scripts/release.sh v9.9.9: %v\n%s\n%s", err, stdout.String(), stderr.String())
	}

	journal := readOfTheTest(t, filepath.Join(copyOfProject, changelog.File))
	day := time.Now().UTC().Format("2006-01-02")
	if want := "## [v9.9.9] - " + day + "\n"; !strings.Contains(journal, want) {
		t.Errorf("the journal of the copy has no section %q:\n%s", want, journal)
	}
	if want := "## [Не выпущено]\n\n## [v9.9.9] - "; !strings.Contains(journal, want) {
		t.Errorf("the unreleased section of the copy does not stand empty above the version:\n%s", journal)
	}
	if fragments, err := filepath.Glob(filepath.Join(copyOfProject, changelog.Dir, "*.md")); err != nil || len(fragments) != 0 {
		t.Errorf("the copy stands %d fragments after the release, want none", len(fragments))
	}
	if got, want := strings.TrimSpace(git(t, on, "log", "-1", "--format=%s")), "chore(release): crewflow v9.9.9"; got != want {
		t.Errorf("the release commit of the copy is %q, want %q", got, want)
	}
	if got, want := strings.TrimSpace(git(t, on, "log", "-1", "--format=%an <%ae>")), "crewflow test <test@crewflow.invalid>"; got != want {
		t.Errorf("the release commit of the copy is by %q, want the identity of the test: a git of a test takes no identity from the machine", got)
	}
	if got, want := strings.TrimSpace(git(t, on, "tag", "--points-at", "HEAD")), "v9.9.9"; got != want {
		t.Errorf("the copy is tagged %q, want %q", got, want)
	}
	if left := git(t, on, "status", "--porcelain"); left != "" {
		t.Errorf("the copy holds %q after the release, want nothing", left)
	}
}

// TestReleaseScriptRefusesWhatItCannotRelease: a release with nothing to release, and one
// on a working tree that is not the head of the branch of the project, are refused before
// anything is written — a half-made release is a version with a fragment in it and a line
// in the next one.
func TestReleaseScriptRefusesWhatItCannotRelease(t *testing.T) {
	if testing.Short() {
		t.Skip("the script builds a binary of the project, and a short run is not the place for it")
	}
	cases := []struct {
		name    string
		version string
		before  func(t *testing.T, on gitOfTheTest)
		want    string
	}{
		{
			name:    "a word that is not a version",
			version: "9.9",
			want:    "9.9 is not vMAJOR.MINOR.PATCH",
		},
		{
			name:    "nothing to release",
			version: "v9.9.9",
			before: func(t *testing.T, on gitOfTheTest) {
				t.Helper()
				fragments, err := filepath.Glob(filepath.Join(on.repo, changelog.Dir, "*.md"))
				if err != nil || len(fragments) == 0 {
					t.Fatalf("the copy holds no fragment: %v", err)
				}
				for _, fragment := range fragments {
					if err := os.Remove(fragment); err != nil {
						t.Fatalf("remove %s: %v", fragment, err)
					}
				}
				git(t, on, "add", "-A")
				git(t, on, "commit", "-q", "-m", "everything released")
			},
			want: "changelog.d holds no fragment",
		},
		{
			name:    "a branch that is not the branch of the project",
			version: "v9.9.9",
			before: func(t *testing.T, on gitOfTheTest) {
				t.Helper()
				git(t, on, "checkout", "-q", "-b", "crewflow/9")
			},
			want: "the branch is crewflow/9",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			copyOfProject := copiedProjectIn(t)
			on := repositoryOf(t, copyOfProject)
			if c.before != nil {
				c.before(t, on)
			}
			ctx, stop := context.WithTimeout(context.Background(), 10*time.Minute)
			defer stop()
			command := exec.CommandContext(ctx, "sh", filepath.Join("scripts", "release.sh"), c.version)
			command.Dir = copyOfProject
			command.Env = machineOfTheTest(t)
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			if err := command.Run(); err == nil {
				t.Fatalf("scripts/release.sh %s did not refuse, and wrote:\n%s", c.version, stdout.String())
			}
			if !strings.Contains(stderr.String(), c.want) {
				t.Errorf("scripts/release.sh %s said %q, want it to hold %q", c.version, stderr.String(), c.want)
			}
			if left := git(t, on, "status", "--porcelain"); left != "" {
				t.Errorf("the copy holds %q after the refusal, want nothing", left)
			}
		})
	}
}

// copiedProjectIn is the module of this repository in a folder of the test: every file
// that is not `.git`, not the scratch of a run and not a built binary.
//
// The `.git` of the project is never copied, and that holds for a file as well as for a
// folder: the `.git` of a worktree of git is a file with the path of the repository in it,
// and a copy of that file would make git in the copy answer about the repository of the
// person who runs the tests — the script would commit into it and tag it. [repositoryOf]
// refuses a copy whose git is not its own, so a mistake here is a failed test and not a
// branch of somebody else's.
func copiedProjectIn(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("the root of the project: %v", err)
	}
	copyOf := t.TempDir()
	err = filepath.WalkDir(root, func(where string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if name == ".git" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if name == ".scratch" || name == "dist" {
				return filepath.SkipDir
			}
			return nil
		}
		content, err := os.ReadFile(where)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, where)
		if err != nil {
			return err
		}
		full := filepath.Join(copyOf, relative)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		return os.WriteFile(full, content, 0o644)
	})
	if err != nil {
		t.Fatalf("copy the project: %v", err)
	}
	return copyOf
}

// repositoryOf is the git of a copy of the project: its own, begun on the branch of the
// project, with one commit in it. A copy whose git turned out to be somebody else's stops
// the test here, before anything is written with it.
func repositoryOf(t *testing.T, copyOf string) gitOfTheTest {
	t.Helper()
	on := gitOfTheTest{
		repo: copyOf,
		home: t.TempDir(),
		settings: map[string]string{
			"user.name":          "crewflow test",
			"user.email":         "test@crewflow.invalid",
			"commit.gpgsign":     "false",
			"init.defaultBranch": "main",
		},
	}
	git(t, on, "init", "-q", ".")
	git(t, on, "add", "-A")
	git(t, on, "commit", "-q", "-m", "the project as it stands")
	// The branch of the project is begun by the commit itself: `git checkout -b main`
	// refuses while another worktree of the same repository stands on it, and the copy is
	// a repository of its own anyway.
	git(t, on, "branch", "-M", "main")
	where := git(t, on, "rev-parse", "--absolute-git-dir")
	if !strings.HasPrefix(where+string(filepath.Separator), copyOf+string(filepath.Separator)) {
		t.Fatalf("the git of the copy of the project is %s, want a git inside %s", where, copyOf)
	}
	return on
}

// machineOfTheTest is the environment a program of the machine runs in under a test of the
// release: a home of its own, so that no config of the machine is read — a git of a test
// that took the identity of the person from `~/.gitconfig` behaved differently on the
// machine of one person and on the runner of CI, and CI has no identity at all; the identity
// of the test in the environment instead, which is where git reads it when there is no
// config; and the caches of the go toolchain of the machine, so that `go run` and `go build`
// inside the script do not fetch what is already on the disk.
func machineOfTheTest(t *testing.T) []string {
	t.Helper()
	home := t.TempDir()
	environment := append(withoutGitOfTheMachine(),
		"HOME="+home, "USERPROFILE="+home, "XDG_CONFIG_HOME="+home,
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=crewflow test", "GIT_AUTHOR_EMAIL=test@crewflow.invalid",
		"GIT_COMMITTER_NAME=crewflow test", "GIT_COMMITTER_EMAIL=test@crewflow.invalid")
	for _, name := range []string{"GOMODCACHE", "GOCACHE"} {
		environment = append(environment, name+"="+cacheOfGoOfTheMachine(t, name))
	}
	return environment
}

// cacheOfGoOfTheMachine is where the go toolchain of the machine keeps its cache, asked
// with the home of the machine: every test of this package runs under a home of its own,
// and the caches of the toolchain are under the home it is asked for — a test of the
// release that asked here would fetch every module again into the home of the test, which
// the tests then clean up after themselves and cannot remove, because what the toolchain
// writes is read-only.
func cacheOfGoOfTheMachine(t *testing.T, name string) string {
	t.Helper()
	command := exec.Command("go", "env", name)
	command.Env = append(withoutGitOfTheMachine(), "HOME="+machineHome, "USERPROFILE="+machineHome)
	out, err := command.Output()
	if err != nil {
		t.Fatalf("go env %s: %v", name, err)
	}
	return strings.TrimSpace(string(out))
}

// readOfTheTest is a file of the test as it stands.
func readOfTheTest(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}
