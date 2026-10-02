package changelog

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
)

// Run starts a program in a folder and returns what it wrote and the code it exited
// with. A git of a test answers for itself, so that a test of the order of the journal
// never reaches the repository of the person who runs it, as a runner of a merge never
// does (docs/DESIGN.md §7h).
type Run func(ctx context.Context, name string, args []string, dir string) (stdout, stderr []byte, exitCode int, err error)

// Merger is where the fragments stand in the order their changes were merged into the
// branch of the project: the numbers of the tasks, the one that was merged last first.
// The order is when the changes went in and not the number of the task — two tasks that
// touch nothing of each other are read in the order they were merged, and the number of
// a task says nothing about when it was (CA-001, CL-003).
type Merger func(ctx context.Context, tasks []int) ([]int, error)

// Merged is the merger of a checkout: it asks git when the fragment of each task entered
// the history of the branch, and answers in the order the changes were merged, the
// newest merge first — so that the journal reads as the last thing that happened above
// the things before it.
//
// Two changes that entered in the same second, which is what two branches merged one
// after another look like, are read by the number of the task, the newer number first:
// the number is in the fragment itself and does not depend on the machine, and two
// builds over one set of fragments answer the same order on every one of them (CA-006).
// A fragment git has no commit for is not in the history yet — a task writes its fragment
// before it commits it — and stands after every other one, by number.
func Merged(run Run, root string) Merger {
	return MergedAt(run, root, "")
}

// MergedAt is the merger of a revision of the history rather than of the checkout: it
// asks when the fragment of each task entered the history of that revision. It is what a
// journal of the default branch is read in — the order of its lines is the order their
// changes were merged there, and not the order a branch of a task was rewritten in.
func MergedAt(run Run, root, rev string) Merger {
	return func(ctx context.Context, tasks []int) ([]int, error) {
		entered, err := entered(ctx, run, root, tasks, rev)
		if err != nil {
			return nil, err
		}
		order := append([]int(nil), tasks...)
		slices.SortStableFunc(order, func(one, other int) int {
			at, inOne := entered[one]
			moment, inOther := entered[other]
			switch {
			case inOne && inOther && at != moment:
				// The change that entered last is read first.
				return cmp(moment, at)
			case inOne != inOther:
				if inOne {
					return -1
				}
				return 1
			}
			return other - one
		})
		return order, nil
	}
}

// entered is the moment the fragment of every task entered the history of a revision, as
// `git log` tells it: the oldest commit that added the file, because that is the commit
// the change of the task went in with, and the commits after it are the work of the same
// task going on — they are not entries in the journal.
//
// One call asks about every fragment at once: a project with a hundred of them waits for
// one git and not for a hundred, and the log that names the files each commit added is
// the answer in the order it walked them.
func entered(ctx context.Context, run Run, root string, tasks []int, rev string) (map[int]int64, error) {
	args := []string{"log"}
	if rev != "" {
		args = append(args, rev)
	}
	args = append(args, "--reverse", "--format=%H %ct", "--diff-filter=A", "--name-only", "--", Dir)
	stdout, stderr, code, err := run(ctx, "git", args, root)
	switch {
	case err != nil:
		return nil, fmt.Errorf("git log %s: %w", Dir, err)
	case code != 0:
		return nil, fmt.Errorf("git log %s: exited with %d: %s", Dir, code, firstLine(stderr))
	}
	wanted := make(map[string]int, len(tasks))
	for _, task := range tasks {
		wanted[path.Join(Dir, strconv.Itoa(task)+".md")] = task
	}
	entered := make(map[int]int64, len(tasks))
	var at int64
	for line := range strings.Lines(string(stdout)) {
		text := strings.TrimSpace(line)
		if moment, is := momentOf(text); is {
			at = moment
			continue
		}
		task, is := wanted[text]
		if !is {
			continue
		}
		// The log is walked from the oldest commit to the newest, so the first commit
		// that names the fragment is the one that put it into the history.
		if _, already := entered[task]; already {
			continue
		}
		entered[task] = at
	}
	return entered, nil
}

// momentOf is the moment a line of `git log` that names a commit was committed at, and
// whether the line names one: the line of a commit is its hash and its time and nothing
// else, and every other line of the log names the files that commit added.
func momentOf(line string) (int64, bool) {
	commit, seconds, ok := strings.Cut(line, " ")
	if !ok || len(commit) != 40 || !isHex(commit) {
		return 0, false
	}
	at, err := strconv.ParseInt(seconds, 10, 64)
	if err != nil {
		return 0, false
	}
	return at, true
}

// cmp is the sign of a-b, for a comparison that is written out in the middle of another
// one and has no name of its own.
func cmp(a, b int64) int {
	switch {
	case a > b:
		return 1
	case a < b:
		return -1
	}
	return 0
}

// isHex is whether the string is written in the sixteen digits git names its objects
// with.
func isHex(text string) bool {
	for _, letter := range text {
		switch {
		case letter >= '0' && letter <= '9', letter >= 'a' && letter <= 'f':
		default:
			return false
		}
	}
	return true
}

// firstLine is the first line that says something of what a program wrote: a refusal is
// read by people, and all of what git wrote may be long.
func firstLine(outputs ...[]byte) string {
	for _, output := range outputs {
		for line := range strings.Lines(string(output)) {
			if text := strings.TrimSpace(line); text != "" {
				return text
			}
		}
	}
	return ""
}
