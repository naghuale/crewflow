package changelog

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The order of the lines of the journal comes out of the history of the branch, and a
// history is not something a test may make up: the tests of the order and of the merge of
// two branches run the real git in a folder of their own, so that the answer comes from
// real commits and a real merge (CA-001, CL-003). Nothing here reaches the repository of
// the person who runs the tests.

// gitOfTheTest is a git of a test: a folder of its own, commits with a clock of its own
// and nothing of the machine in it.
type gitOfTheTest struct {
	t   *testing.T
	dir string
	// moment is the time of the next commit. Every commit of a test is a minute later
	// than the one before it, so that the order of the merges is the order of the
	// commits on every machine, and not the speed of one of them.
	moment time.Time
}

// gitIn is a fresh repository of a test, with nothing committed in it yet.
func gitIn(t *testing.T) *gitOfTheTest {
	t.Helper()
	repo := &gitOfTheTest{
		t:      t,
		dir:    t.TempDir(),
		moment: time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC),
	}
	repo.must("init", "-q", ".")
	repo.must("config", "user.name", "crewflow test")
	repo.must("config", "user.email", "test@crewflow.invalid")
	repo.must("config", "commit.gpgsign", "false")
	repo.must("checkout", "-q", "-b", "main")
	return repo
}

// Run is the git of the package, so that a project of a test reads its order out of this
// repository and not out of another one.
func (g *gitOfTheTest) Run(ctx context.Context, name string, args []string, dir string) ([]byte, []byte, int, error) {
	g.t.Helper()
	if dir == "" {
		dir = g.dir
	}
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = dir
	var stdout, stderr strings.Builder
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	code := 0
	if err != nil {
		var failed *exec.ExitError
		if !errors.As(err, &failed) {
			g.t.Fatalf("git %s: %v", strings.Join(args, " "), err)
		}
		code = failed.ExitCode()
	}
	return []byte(stdout.String()), []byte(stderr.String()), code, nil
}

// must runs git and fails the test when git said no: a command of a test that is refused
// means the repository of the test is not what the test thinks it is.
func (g *gitOfTheTest) must(args ...string) string {
	g.t.Helper()
	stdout, stderr, code, err := g.Run(context.Background(), "git", args, g.dir)
	if err != nil || code != 0 {
		g.t.Fatalf("git %s: exited with %d: %s", strings.Join(args, " "), code, stderr)
	}
	return strings.TrimSpace(string(stdout))
}

// commit is one commit of the test, a minute later than the one before it.
func (g *gitOfTheTest) commit(message string) {
	g.t.Helper()
	g.moment = g.moment.Add(time.Minute)
	g.must("add", "-A")
	command := exec.Command("git", "commit", "-q", "-m", message)
	command.Dir = g.dir
	command.Env = append(os.Environ(),
		"GIT_AUTHOR_DATE="+g.moment.Format(time.RFC3339),
		"GIT_COMMITTER_DATE="+g.moment.Format(time.RFC3339),
	)
	if out, err := command.CombinedOutput(); err != nil {
		g.t.Fatalf("git commit %q: %v: %s", message, err, out)
	}
}

// write puts a file of the project on the disk of the repository.
func (g *gitOfTheTest) write(name, content string) {
	g.t.Helper()
	full := filepath.Join(g.dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		g.t.Fatalf("make a folder for %s: %v", name, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		g.t.Fatalf("write %s: %v", name, err)
	}
}

// read is a file of the project as it stands, and fails the test when it is not there.
func (g *gitOfTheTest) read(name string) string {
	g.t.Helper()
	content, err := os.ReadFile(filepath.Join(g.dir, filepath.FromSlash(name)))
	if err != nil {
		g.t.Fatalf("read %s: %v", name, err)
	}
	return string(content)
}

// gone is whether the path is not on the disk of the repository any more.
func (g *gitOfTheTest) gone(name string) bool {
	g.t.Helper()
	_, err := os.Stat(filepath.Join(g.dir, filepath.FromSlash(name)))
	return os.IsNotExist(err)
}

// asExitError is [errors.As] for an exit of a program, said once for the tests of this
// file.
func asExitError(err error, into **exec.ExitError) bool {
	failed, ok := err.(*exec.ExitError)
	if ok {
		*into = failed
	}
	return ok
}
