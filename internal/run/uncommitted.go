package run

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// checkpoints is where crewflow keeps the work a run left outside the commits of its
// branch: a ref of the repository of the project, under the number of the task and the
// number of the attempt, so that the snapshot of a worktree names the task and the run it
// is of and is not mistaken for a branch (D-088, docs.DESIGN.md §7a).
//
// The namespace is `refs/crewflow/` and nothing else, and it is local: a ref of it is
// never pushed under any push at all — the hook of a worktree refuses it by its name, and
// the push of a merge is one refspec of the branch of the project and not a mirror
// (§7h, §7i).
const checkpoints = "refs/crewflow/checkpoints"

// checkpointKept is how long a snapshot of work outside the commits is kept, and after
// that it is taken away: a snapshot is a protection from a lost folder and not an archive
// of the project, and the objects of a run of a month ago are the work of the project by
// then — in the branch, in the change request or in the main branch (D-088).
//
// The term is one number and not a setting of the project: `internal/config` is the
// boundary of another task, and a number of a project that is written down in one place is
// a number a person can find (D-088, docs/DESIGN.md §5).
const checkpointKept = 30 * 24 * time.Hour

// snapshotFor is how long the saving of the work outside the commits may take. Git is
// asked a handful of questions about a worktree and writes a tree into a ref of the
// repository; a minute is more than any of it needs, and a saving of the work that hangs
// is a run that does not end where the person who stopped it said it would (D-088,
// docs.DESIGN.md §7a).
const snapshotFor = time.Minute

// snapshotBatch is how many paths go into the snapshot in one command of git: a run that
// touched a thousand files would give git a command line that fits nowhere, and every
// batch is written into the same index of its own.
const snapshotBatch = 64

// ErrUncommittedWork is the refusal to take a worktree away that holds work outside the
// commits of its branch. A snapshot is not what lets the work go: a snapshot may be
// incomplete — a path that stays closed is left out of it — and a worktree that holds work
// is not a folder of a run that never started. Every road that takes a worktree away
// refuses here, and the refusal is one refusal, so that a caller tells the two apart with
// `errors.Is` and not by the words of it (D-088, docs/DESIGN.md §7a, §7h, §7i).
var ErrUncommittedWork = errors.New("worktree-has-uncommitted-work")

// Uncommitted is the work an attempt left in its worktree outside the commits of its
// branch: how much of it there is, the commit the branch stood at, the snapshot of it in
// the ref of the repository and the one command of git that puts the work back.
//
// It is a record of counts, names and one command, and never of what is in the files: the
// files of a run may hold what the boundary of the run would have taken out of them, and a
// state file is read by people and pasted into issues (D-088, docs.DESIGN.md §7e, §7h).
type Uncommitted struct {
	// At is when crewflow counted the work of the attempt, which is when the attempt
	// ended: a queue counts how long a task has been waiting from the end of its run
	// (§6a).
	At time.Time `json:"at"`
	// Changed is how many files of the worktree hold something the tree of the branch
	// does not, and New how many files are in the worktree and in no tree at all. A file
	// the repository ignores is in neither count: it is a build of the project or the
	// scratch of a run, and not the work of the task (§7a).
	Changed int `json:"changed"`
	New     int `json:"new"`
	// Head is the commit the branch stood at when the work was counted, which is what a
	// person compares the work against.
	Head string `json:"head"`
	// Ref is the snapshot of the work — `refs/crewflow/checkpoints/<task>/<attempt>` —
	// and Restore the one command of git that puts the work back into the worktree. Both
	// are nothing where every path of the work was left out of the snapshot: there is
	// nothing to bring back and a command that finds nothing is a command a person
	// trusts in vain (§7a).
	Ref     string `json:"ref,omitempty"`
	Restore string `json:"restore,omitempty"`
	// Left are the paths of the work that are not in the snapshot: the scratch of the run
	// and the places that stay closed to the run whatever the project wrote. An
	// incomplete snapshot says so in the record of the attempt, because it is a
	// protection and not a copy (§7d, §8).
	Left []string `json:"left,omitempty"`
}

// Any is whether there is work outside the commits at all: nothing was changed and
// nothing is new, and there is nothing of a task to record or to refuse a worktree over
// (docs.DESIGN.md §7a).
func (u Uncommitted) Any() bool {
	return u.Changed > 0 || u.New > 0
}

// Count is how many files of the work there are in all, as a person and a program read
// the number: a report says "13 files outside the commits" and not "13 and 6" (D-088,
// docs/DESIGN.md §6a).
func (u Uncommitted) Count() int {
	return u.Changed + u.New
}

// said is what a report of a run and a queue of attention say about the work a run left
// outside the commits: how much of it there is, where the snapshot of it is, what was left
// out of it and the one command that puts it back (D-088, docs.DESIGN.md §6a, §7a).
func (u Uncommitted) said() string {
	var out strings.Builder
	fmt.Fprintf(&out, "the run left %d files outside the commits of its branch (%d changed, %d new), and the branch stood at %s",
		u.Count(), u.Changed, u.New, shortCommit(u.Head))
	if u.Ref != "" {
		fmt.Fprintf(&out, "; a snapshot of it is kept in %s and is not on the host", u.Ref)
	}
	if len(u.Left) > 0 {
		fmt.Fprintf(&out, "; %d paths were left out of the snapshot: %s", len(u.Left), strings.Join(u.Left, ", "))
	}
	if u.Restore != "" {
		fmt.Fprintf(&out, "; the work is put back with `%s`", u.Restore)
	}
	return out.String()
}

// shortCommit is the commit as a report names it: the first seven signs of it, where there
// is a full one to shorten, and as it is where there is not — a report says what it has and
// does not make a name of it up (docs/DESIGN.md §7h).
func shortCommit(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}

// checkpointRef is the ref the snapshot of the work of that attempt is kept in: the
// repository of the project, the number of the task and the number of the attempt
// (D-088, docs/DESIGN.md §7a).
func checkpointRef(task, attempt int) string {
	return fmt.Sprintf("%s/%d/%d", checkpoints, task, attempt)
}

// GitOutput is a question about a checkout, asked of git in a folder. It is one
// interface for two roads that ask the same question — the road a run goes on with and the
// road a merge comes in by — so that "the work outside the commits" is one question and one
// answer whatever the worktree is about to happen to (docs.DESIGN.md §7a, §7h).
type GitOutput interface {
	Output(ctx context.Context, dir string, args ...string) (string, error)
}

// UncommittedIn is what a checkout holds outside the commits of its branch: the files that
// were changed and the files that are new, and the commit its branch stood at.
//
// A file the repository ignores is not work: it is a build of the project, a cache of its
// tools or the scratch of the run, and the work of the task is not in it. A file that is
// gone from the worktree is work — the tree of the branch holds it and the folder does
// not, and a snapshot of the worktree has to carry the deletion with it (D-088,
// docs.DESIGN.md §7a).
func UncommittedIn(ctx context.Context, g GitOutput, dir string) (Uncommitted, error) {
	work, _, err := uncommittedOf(ctx, g, dir)
	return work, err
}

// uncommittedOf is the work outside the commits with the paths of it: what a run keeps a
// snapshot of and what the record of the attempt counts. One question and one answer for
// every caller, and the paths with it because the snapshot is of the files and not of the
// number of them (D-088, docs.DESIGN.md §7a).
func uncommittedOf(ctx context.Context, g GitOutput, dir string) (Uncommitted, []string, error) {
	head, err := g.Output(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return Uncommitted{}, nil, err
	}
	work := Uncommitted{Head: strings.TrimSpace(head)}
	// Everything git holds in the index and in the worktree against the head of the
	// branch, whether it is staged or not: a change that was staged and a change that was
	// not are the same work, and one of them going into the snapshot and the other not
	// would make the snapshot a lie.
	changed, err := pathsOf(ctx, g, dir, "diff", "--name-only", "-z", work.Head)
	if err != nil {
		return Uncommitted{}, nil, err
	}
	// The files that are in the worktree and in no tree at all, without the ones the
	// repository ignores.
	created, err := pathsOf(ctx, g, dir, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return Uncommitted{}, nil, err
	}
	work.Changed, work.New = len(changed), len(created)
	return work, slices.Concat(changed, created), nil
}

// pathsOf are the paths of one answer of git, which is read with a zero between them: a
// name of a file may hold a line break of its own, and a path in a record of a run is the
// name and not a line of a list (docs/DESIGN.md §7a).
func pathsOf(ctx context.Context, g GitOutput, dir string, args ...string) ([]string, error) {
	out, err := g.Output(ctx, dir, args...)
	if err != nil {
		return nil, err
	}
	var paths []string
	for path := range strings.SplitSeq(out, "\x00") {
		if path != "" {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

// Output is [runner.output] under the name every question about a checkout is asked by:
// whether a worktree holds work outside its commits is one question however the worktree
// is about to go away (docs.DESIGN.md §7a, §7h).
func (r *runner) Output(ctx context.Context, dir string, args ...string) (string, error) {
	return r.output(ctx, dir, args...)
}

// keeps is what a run does with the work it left in its worktree outside the commits of
// its branch: the files are counted, a snapshot of them is kept in a ref of crewflow's
// own, and both go into the attempt of the state of the task — so that an interruption of
// a run costs at most one step of work, and a person has one command that puts the work
// back (D-088, docs.DESIGN.md §6a, §7a, §7h).
//
// It is said while the journal of the attempt is open, before it is closed, so that a
// watch of the attempt shows the work where the run left it.
//
// Nothing of it happens for a run that opened its change request: the work of such a run is
// in the commits of the branch and on the host, and there is nothing outside the commits
// that has to be saved. What it answers is the record of the attempt, and nothing where
// there was no work: the caller writes it into the state under the lock of the task,
// beside the end of the attempt (D-088, docs/DESIGN.md §7h).
func (r *runner) keeps(ctx context.Context, files *AttemptFiles, ended Kind) (Uncommitted, error) {
	if ended == ChangeRequestOpened {
		return Uncommitted{}, nil
	}
	// The context of the run is not the context of this work: a run that a person stopped
	// has been stopped by cancelling that context, and a run that ran out of time has had
	// it run out — and the work of that run is exactly the work that is worth saving. So
	// the snapshot is taken of a context that is still alive, with a limit of its own: the
	// saving of the work must not keep crewflow standing there where the run was asked to
	// stop (D-088, docs.DESIGN.md §7a, §7i).
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), snapshotFor)
	defer cancel()
	work, paths, err := uncommittedOf(ctx, r, r.worktree)
	if err != nil {
		return Uncommitted{}, err
	}
	if !work.Any() {
		return Uncommitted{}, nil
	}
	work.At = r.env.Now()
	paths, work.Left = r.allowed(paths)
	// A run whose every path was left out of the snapshot has a record and no snapshot:
	// the work is in the folder and nowhere else, and a ref that holds nothing would be a
	// ref a person reads as a copy (D-088).
	if len(paths) > 0 {
		if work, err = r.snapshotted(ctx, work, paths); err != nil {
			return Uncommitted{}, err
		}
	}
	fmt.Fprintf(files.Out, "crewflow: %s\n", work.said())
	return work, nil
}

// allowed are the paths of the work outside the commits that go into the snapshot, and the
// ones left out of it: the scratch of the run and the places that stay closed to the run
// whatever the project wrote.
//
// A path left out makes the snapshot incomplete and an incomplete snapshot says so in the
// record of the attempt: `secret.Out` cleans what a run publishes, and it does not clean
// the blob of a file — a snapshot that took a file a run wrote with a key in it would keep
// that key in the repository of the project, in a ref nobody pushes and everybody who
// reads the folder can reach (D-088, docs/DESIGN.md §7d, §8).
func (r *runner) allowed(paths []string) (kept, left []string) {
	for _, path := range paths {
		if r.scratchOf(path) || r.closed.secret(filepath.Join(r.worktree, path), r.worktree) {
			left = append(left, path)
			continue
		}
		kept = append(kept, path)
	}
	return kept, left
}

// scratchOf is whether a path of the worktree is the scratch of the run or what is in it:
// the scratch is the one thing a run writes outside of what the task is to change, and it
// belongs to the run and not to the work of the task (docs.DESIGN.md §7a).
func (r *runner) scratchOf(path string) bool {
	return path == scratchFolder || strings.HasPrefix(path, scratchIgnore)
}

// snapshotted is the work outside the commits kept in a ref of crewflow's own: the whole
// of the worktree as it stood, the files that were changed and the files that are new, with
// the paths that stay closed left out of it.
//
// It is written through an index of its own — `GIT_INDEX_FILE` — so that the head of the
// branch, the branch itself, the index a person has staged their work in and the files of
// the worktree are the same after the snapshot as before it, byte for byte. Nothing is
// pushed anywhere and no branch is moved: the snapshot is a commit nobody is on
// (D-088, docs.DESIGN.md §7a).
//
// The executor of the run is not alive here: the run waits for it to stop before it is
// judged, and a snapshot of a folder that is still being written is a snapshot of
// something nobody has finished.
func (r *runner) snapshotted(ctx context.Context, work Uncommitted, paths []string) (Uncommitted, error) {
	if err := os.MkdirAll(Scratch(r.worktree), 0o700); err != nil {
		return work, fmt.Errorf("make %s: %w", Scratch(r.worktree), err)
	}
	folder, err := os.MkdirTemp(Scratch(r.worktree), "checkpoint-")
	if err != nil {
		return work, fmt.Errorf("make a folder for the index of the snapshot: %w", err)
	}
	defer func() { _ = os.RemoveAll(folder) }()
	index := filepath.Join(folder, "index")
	// The snapshot is the whole of the worktree and not a difference of it: a person who
	// brings it back wants the files as they were, and a tree that held only the changed
	// ones would have to be read against the head of the branch to mean anything.
	if _, err := r.indexed(ctx, index, r.worktree, "read-tree", work.Head); err != nil {
		return work, err
	}
	for at := 0; at < len(paths); at += snapshotBatch {
		end := min(at+snapshotBatch, len(paths))
		args := append([]string{"add", "-A", "--"}, paths[at:end]...)
		if _, err := r.indexed(ctx, index, r.worktree, args...); err != nil {
			return work, err
		}
	}
	tree, err := r.indexed(ctx, index, r.worktree, "write-tree")
	if err != nil {
		return work, err
	}
	commit, err := r.indexed(ctx, index, r.repoDir(), "commit-tree", strings.TrimSpace(tree),
		"-p", work.Head, "-m", checkpointSaid(r.task.Number, r.attempt))
	if err != nil {
		return work, err
	}
	work.Ref = checkpointRef(r.task.Number, r.attempt)
	work.Restore = fmt.Sprintf("git -C %s cherry-pick --no-commit %s", r.worktree, work.Ref)
	if err := r.git(ctx, r.repoDir(), "update-ref", work.Ref, strings.TrimSpace(commit)); err != nil {
		return work, err
	}
	r.prunesCheckpoints(ctx)
	return work, nil
}

// checkpointAuthorEnv is whose name the commit of a snapshot goes under, said to git as the
// environment of the one command that writes it: a snapshot is not work of a person, and a
// repository that names the owner of the machine in every commit of every task of it is a
// repository that says something untrue about half of them. It is the environment and not
// `-c user.name=…` on the command line because the settings of a repository belong to the
// project and not to the run of a task (D-088, docs.DESIGN.md §7a).
var checkpointAuthorEnv = []string{
	"GIT_AUTHOR_NAME=" + checkpointAuthor, "GIT_AUTHOR_EMAIL=" + checkpointAuthor + "@localhost",
	"GIT_COMMITTER_NAME=" + checkpointAuthor, "GIT_COMMITTER_EMAIL=" + checkpointAuthor + "@localhost",
}

// checkpointAuthor is whose name the commit of a snapshot goes under, and checkpointSaid
// is what it says: the task and the attempt it is the work of, and the rule the snapshot
// is there by. Neither of them holds anything the run wrote, and both are words of
// crewflow and not of a person (D-088, docs/DESIGN.md §7e).
const checkpointAuthor = "crewflow"

// checkpointSaid is the message of the commit of a snapshot: what the work is of and the
// rule the snapshot is there by. It is a line of crewflow and not of the run, and it holds
// no word that the run wrote (D-088, docs/DESIGN.md §7e).
func checkpointSaid(task, attempt int) string {
	return fmt.Sprintf("crewflow: the work attempt %d of the task %d left outside the commits of its branch (D-088)",
		attempt, task)
}

// indexed is git with an index of its own: the snapshot of the work outside the commits is
// written through it, and the index of the worktree — the one a person staged their work
// in — is not read and not written by it (D-088, docs.DESIGN.md §7a).
func (r *runner) indexed(ctx context.Context, index, dir string, args ...string) (string, error) {
	env := append(slices.Clone(r.route), "GIT_INDEX_FILE="+index)
	env = append(env, checkpointAuthorEnv...)
	stdout, stderr, code, err := r.env.Command(ctx, "git", args, dir, env)
	switch {
	case err != nil:
		return "", r.boundary().Err(fmt.Errorf("git %s: %w", strings.Join(args, " "), err))
	case code != 0:
		return "", r.boundary().Err(fmt.Errorf("git %s: exited with %d: %s",
			strings.Join(args, " "), code, firstLine(stderr)))
	}
	return string(stdout), nil
}

// prunesCheckpoints takes the snapshots of the task that are older than the term away. It
// is said and not asked for: a snapshot is a protection from a lost folder and not an
// archive, and the objects of it are the work of the project by then (D-088,
// docs.DESIGN.md §7a).
func (r *runner) prunesCheckpoints(ctx context.Context) {
	listed, err := r.output(ctx, r.repoDir(), "for-each-ref", "--format=%(refname) %(committerdate:unix)",
		checkpoints+"/"+fmt.Sprint(r.task.Number)+"/")
	if err != nil {
		return
	}
	now := r.env.Now()
	for _, line := range lines(strings.TrimSpace(listed)) {
		ref, stamp, found := strings.Cut(line, " ")
		if !found || ref == "" {
			continue
		}
		unix, err := strconv.ParseInt(strings.TrimSpace(stamp), 10, 64)
		if err != nil {
			continue
		}
		if now.Sub(time.Unix(unix, 0)) <= checkpointKept {
			continue
		}
		if err := r.git(ctx, r.repoDir(), "update-ref", "-d", ref); err != nil {
			return
		}
	}
}
