package changelog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"slices"
	"strings"
)

// Tasks is the host a check asks whether the number of a fragment is a task of the
// project: one read of a task and nothing else. It is a function so that a test of a
// check answers for itself and never asks the host of the person who runs it, as every
// other seam of a command of crewflow does (docs/DESIGN.md §7h).
//
// Nil is a project whose check is not asked about tasks at all — a journal of a project
// that is not hosted anywhere. It is named, not guessed: a check with a host says how
// many tasks it asked about.
type Tasks func(ctx context.Context, number int) error

// A Project is the journal of one project: the root of the checkout it stands in, the
// language it is written in, the order the fragments of its tasks are read in.
//
// Everything it does is a file operation over `changelog.d/` and `CHANGELOG.md` — the
// order comes from git, and the host is asked about a task only by [Project.Check], and
// only whether the number of a fragment is a task at all.
type Project struct {
	// Root is the root of the checkout, where the folder of the fragments and the
	// journal stand.
	Root string
	// Language is the language the journal is written in.
	Language Language
	// Order is where the fragments stand in the order their changes were merged; nil
	// reads them as they stand on the disk, which is what a journal outside a checkout
	// of git is read by.
	Order Merger
}

// The reasons a check of a journal finds something wrong, and what a caller tells apart
// by [errors.Is]. A journal is checked by a gate, and a gate needs to know what it found
// and not only that it found something.
var (
	// ErrNoUnreleased is a journal with no part for the changes no version has taken.
	ErrNoUnreleased = errors.New("the journal has no unreleased part")
	// ErrDuplicate is one line of the journal or one task with two fragments.
	ErrDuplicate = errors.New("two fragments say one thing")
	// ErrNoSuchTask is a fragment of a task the host does not have.
	ErrNoSuchTask = errors.New("the host has no such task")
	// ErrAbsorbed is a line of the journal that a version below already holds.
	ErrAbsorbed = errors.New("a version below already holds this line")
)

// Build is the unreleased part of the journal built out of every fragment that stands:
// the sections in the order of the journal, the lines inside a section in the order the
// changes were merged. It writes nothing and answers the whole file, so that a caller
// writes it or does not, and so that a check compares it with the file on the disk.
func (p Project) Build(ctx context.Context) ([]byte, error) {
	fragments, err := p.body(ctx)
	if err != nil {
		return nil, err
	}
	return p.assemble(Body(fragments, p.Language))
}

// body is the fragments of the project in the order the journal reads them.
func (p Project) body(ctx context.Context) ([]Fragment, error) {
	fragments, err := p.Read()
	if err != nil {
		return nil, err
	}
	return p.order(ctx, fragments)
}

// order is the fragments in the order the journal reads them. A merger that answered
// something other than every fragment exactly once is refused: a journal that read half
// of the fragments, or read one twice, cannot be followed back to a task.
func (p Project) order(ctx context.Context, fragments []Fragment) ([]Fragment, error) {
	if p.Order == nil || len(fragments) == 0 {
		return fragments, nil
	}
	tasks := make([]int, 0, len(fragments))
	byTask := make(map[int]Fragment, len(fragments))
	for _, fragment := range fragments {
		tasks = append(tasks, fragment.Task)
		byTask[fragment.Task] = fragment
	}
	order, err := p.Order(ctx, tasks)
	if err != nil {
		return nil, err
	}
	read := append([]int(nil), order...)
	slices.Sort(read)
	wanted := append([]int(nil), tasks...)
	slices.Sort(wanted)
	if !slices.Equal(read, wanted) {
		return nil, fmt.Errorf("the order of the fragments is not the order of the fragments: %v, not %v", order, wanted)
	}
	ordered := make([]Fragment, 0, len(fragments))
	for _, task := range order {
		ordered = append(ordered, byTask[task])
	}
	return ordered, nil
}

// Answer is what a check found, for a program that asks: the problems and nothing else, so
// that a gate of a change can read the reason of a refusal instead of the text of it.
type Answer struct {
	Problems []Problem `json:"problems"`
}

// Count is how many fragments stand in the folder of the fragments — how many tasks have a
// line of the journal that no version has taken yet. A folder that is not there is a
// project that has released everything, and not a refusal.
func (p Project) Count() (int, error) {
	names, err := os.ReadDir(path.Join(p.Root, Dir))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read the fragments: %w", err)
	}
	return len(names), nil
}

// Read is every fragment of the project, as it stands on the disk, sorted by the name of
// the file. A fragment that is not a fragment is a refusal with the file and the line in
// it: the person who wrote it is the one who has to see it.
func (p Project) Read() ([]Fragment, error) {
	return Read(p.Root, p.Language)
}

// assemble is the whole journal with the built part of it under the heading of the
// unreleased part.
func (p Project) assemble(body string) ([]byte, error) {
	content, err := os.ReadFile(path.Join(p.Root, File))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", File, err)
	}
	assembled, err := Assemble(content, body, p.Language)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoUnreleased, err)
	}
	return assembled, nil
}

// Write is the built journal on the disk, and whether it wrote it. A journal that already
// is what the fragments build is not written at all: a build that rewrote the same bytes
// would touch a file of the project for nothing, and the next change would show a
// difference that is not one (CL-005).
func (p Project) Write(ctx context.Context) (written bool, err error) {
	assembled, err := p.Build(ctx)
	if err != nil {
		return false, err
	}
	journal := path.Join(p.Root, File)
	current, err := os.ReadFile(journal)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", File, err)
	}
	if string(current) == string(assembled) {
		return false, nil
	}
	if err := p.put(assembled); err != nil {
		return false, err
	}
	return true, nil
}

// put writes the journal whole or not at all: the content goes into a file of its own next
// to the journal and the rename that follows is the moment the journal changes. A build
// that was interrupted, a machine without room for the file and a folder that cannot hold
// a new one leave the journal of the last release as it was — half a section of a version
// is a journal a reader cannot follow back to a task (REL-030, CHG-REL-012).
func (p Project) put(content []byte) error {
	beside, err := os.CreateTemp(p.Root, "."+File+".*")
	if err != nil {
		return fmt.Errorf("write %s: %w", File, err)
	}
	// A rename has taken the file away already, and nothing else is to be done about it;
	// a write that failed has to take its own file back with it, or the next build would
	// find a file of the journal that is not a journal.
	defer func() { _ = os.Remove(beside.Name()) }()
	if _, err := beside.Write(content); err != nil {
		return p.failedToWrite(beside, err)
	}
	// The bytes are on the disk before the name of the journal points at them: a machine
	// that lost the power in between would otherwise leave the journal holding a name and
	// nothing under it.
	if err := beside.Sync(); err != nil {
		return p.failedToWrite(beside, err)
	}
	if err := beside.Close(); err != nil {
		return fmt.Errorf("write %s: %w", File, err)
	}
	// A file of a temporary one is private to the person who made it, and the journal of a
	// project is read by everybody who has the checkout.
	if err := os.Chmod(beside.Name(), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", File, err)
	}
	if err := os.Rename(beside.Name(), path.Join(p.Root, File)); err != nil {
		return fmt.Errorf("write %s: %w", File, err)
	}
	return nil
}

// failedToWrite is a write of the journal that stopped before the rename, with the file of
// it closed on the way out — a file that is still open when a program ends is a file the
// system has not finished with.
func (p Project) failedToWrite(beside *os.File, err error) error {
	if closed := beside.Close(); closed != nil && !errors.Is(closed, os.ErrClosed) {
		return fmt.Errorf("write %s: %w", File, errors.Join(err, closed))
	}
	return fmt.Errorf("write %s: %w", File, err)
}

// Problem is one thing a check found wrong: where it is, what is wrong and the reason,
// which is one a caller tells apart by [errors.Is] and a person reads in one line.
type Problem struct {
	// Path is the file of the project the problem is in, from the root of the checkout.
	Path string
	// Line is the line in it, or 0 when the problem is in the file as a whole.
	Line int
	// What says what is wrong, for a person.
	What string
	// Err is the reason: [ErrFormat], [ErrNoSuchTask], [ErrDuplicate] or [ErrAbsorbed].
	Err error
}

func (p Problem) Error() string {
	if p.Line > 0 {
		return fmt.Sprintf("%s:%d: %s", p.Path, p.Line, p.What)
	}
	return fmt.Sprintf("%s: %s", p.Path, p.What)
}

func (p Problem) Unwrap() error { return p.Err }

// MarshalJSON is the problem as a program reads it: where it is, what is wrong and the
// name of the reason. An error of Go holds its text in itself and marshals as an empty
// object of the fields it has, and a gate has to be able to tell the reasons apart by
// name.
func (p Problem) MarshalJSON() ([]byte, error) {
	type named struct {
		Path   string `json:"path"`
		Line   int    `json:"line,omitempty"`
		What   string `json:"what"`
		Reason string `json:"reason"`
	}
	return json.Marshal(named{Path: p.Path, Line: p.Line, What: p.What, Reason: reasonOf(p.Err)})
}

// reasonOf is the name of the reason a problem was found for, as this package names it.
func reasonOf(err error) string {
	for _, reason := range []error{ErrFormat, ErrNoSuchTask, ErrDuplicate, ErrAbsorbed} {
		if errors.Is(err, reason) {
			return reason.Error()
		}
	}
	return err.Error()
}

// Check is everything wrong with the fragments of the project, and it asks the host about
// every number a fragment is named after: a fragment of a task that is not a task is a
// line of the journal a reader cannot follow to anything.
//
// What is checked: the form of every fragment, the existence of every task, and the absence
// of two fragments that say one thing — one line of the journal in two of them, one task
// with two files.
//
// The journal itself is not compared with a build of the fragments: the journal is written
// by a release and only by it, and a task neither writes it nor knows where its line stands
// in it until it is merged (docs/DESIGN.md §6, D-066). A check that asked a task to bring the
// journal to the fragments would ask it to write a file every task writes, which is the
// resource `changelog.d/` exists to end.
func (p Project) Check(ctx context.Context, tasks Tasks) ([]Problem, error) {
	return p.readings(ctx, tasks)
}

// readings is everything a check knows: the form of every fragment, the task every
// fragment is named after, and the fragments that say one thing twice.
func (p Project) readings(ctx context.Context, tasks Tasks) ([]Problem, error) {
	names, err := os.ReadDir(path.Join(p.Root, Dir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read the fragments: %w", err)
	}
	var problems []Problem
	fragments := make([]Fragment, 0, len(names))
	for _, entry := range names {
		if entry.IsDir() {
			continue
		}
		where := path.Join(Dir, entry.Name())
		content, err := os.ReadFile(path.Join(p.Root, where))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", where, err)
		}
		fragment, err := Parse(entry.Name(), content, p.Language)
		if err != nil {
			var bad Bad
			if errors.As(err, &bad) {
				problems = append(problems, Problem{Path: bad.Path, Line: bad.Line, What: bad.What, Err: bad})
				continue
			}
			return nil, err
		}
		fragments = append(fragments, fragment)
	}
	problems = append(problems, p.taskProblems(ctx, fragments, tasks)...)
	return append(problems, duplicatesOf(fragments)...), nil
}

// taskProblems is a fragment of a task the host does not have, one problem for one
// number. Every number is asked about once and in the order the fragments stand on the
// disk, so that a check of the same journal twice asks the host the same questions.
func (p Project) taskProblems(ctx context.Context, fragments []Fragment, tasks Tasks) []Problem {
	if tasks == nil {
		return nil
	}
	var problems []Problem
	asked := make(map[int]bool, len(fragments))
	for _, fragment := range fragments {
		if asked[fragment.Task] {
			continue
		}
		asked[fragment.Task] = true
		if err := tasks(ctx, fragment.Task); err != nil {
			problems = append(problems, Problem{
				Path: fragment.Path,
				What: fmt.Sprintf("the host has no task #%d: %v", fragment.Task, err),
				Err:  ErrNoSuchTask,
			})
		}
	}
	return problems
}

// duplicatesOf is every line of the journal that stands in two fragments, and every task
// with two fragments. Two tasks that say the same thing about the same change write the
// journal twice, and a reader of it cannot tell which of them did it.
func duplicatesOf(fragments []Fragment) []Problem {
	var problems []Problem
	lines := make(map[string]Fragment, len(fragments))
	tasks := make(map[int]Fragment, len(fragments))
	for _, fragment := range fragments {
		if other, already := tasks[fragment.Task]; already {
			problems = append(problems, Problem{
				Path: fragment.Path,
				What: fmt.Sprintf("the task #%d has two fragments: %s and this one", fragment.Task, other.Path),
				Err:  ErrDuplicate,
			})
		} else {
			tasks[fragment.Task] = fragment
		}
		for _, entry := range fragment.Entries {
			line := oneLine(entry.Text)
			other, already := lines[line]
			if !already {
				lines[line] = Fragment{Path: fragment.Path, Task: fragment.Task}
				continue
			}
			problems = append(problems, Problem{
				Path: fragment.Path,
				Line: entry.Line,
				What: fmt.Sprintf("the line is in %s too — one line of the journal is one task", other.Path),
				Err:  ErrDuplicate,
			})
		}
	}
	return problems
}

// Release is what a release of a version did to the journal of a project: the version it
// cut with the day of it, the journal it wrote and the fragments it moved into the section
// of that version. It wrote them itself and says what it wrote, so that a person commits
// the result and does not have to guess what changed in it.
type Release struct {
	// Version is the version that was cut.
	Version string
	// Date is the day of the release.
	Date string
	// Journal is the path of the journal it wrote.
	Journal string
	// Moved are the fragments that went into the section of the version, in the order
	// the journal read them.
	Moved []string
}

// Release cuts a version of the project: the unreleased part of the journal, built out of
// every fragment that stands, becomes the section of that version with the day of the
// release, a fresh empty unreleased part stands above it, and the fragments are gone — a
// release is one commit, and a fragment that stood in it would write its line into the
// next version a second time.
//
// What a release puts into the journal of a version is not looked at twice: two fragments
// that say one thing and a line a version below already holds are refused before the
// journal is touched, and the fragments stand where they were. A refused release has moved
// nothing (REL-006, REL-026, CHG-REL-003, CHG-REL-004).
//
// The host is not asked here — only a check asks it, and a version is not held up by a
// task nobody can find (docs/DESIGN.md §6).
//
// The link at the end of the journal is left as it is: it names the commits of the branch
// of the project, and the unreleased part of the journal is them.
func (p Project) Release(ctx context.Context, version, date string) (Release, error) {
	fragments, err := p.body(ctx)
	if err != nil {
		return Release{}, err
	}
	body := Body(fragments, p.Language)
	content, err := os.ReadFile(path.Join(p.Root, File))
	if err != nil {
		return Release{}, fmt.Errorf("read %s: %w", File, err)
	}
	above, released, err := split(content, p.Language)
	if err != nil {
		return Release{}, err
	}
	if problems := duplicatesOf(fragments); len(problems) > 0 {
		return Release{}, fmt.Errorf("%w: %s", ErrDuplicate, saidBy(problems))
	}
	if problems := absorbedBy(released, fragments); len(problems) > 0 {
		return Release{}, fmt.Errorf("%w: %s", ErrAbsorbed, saidBy(problems))
	}
	out := above + "\n## [" + version + "] - " + date + "\n\n"
	if body != "" {
		out += body + "\n\n"
	}
	out += released
	if err := p.put([]byte(out)); err != nil {
		return Release{}, err
	}
	cut := Release{Version: version, Date: date, Journal: File}
	for _, fragment := range fragments {
		if err := os.Remove(path.Join(p.Root, fragment.Path)); err != nil {
			return Release{}, fmt.Errorf("remove %s: %w", fragment.Path, err)
		}
		cut.Moved = append(cut.Moved, fragment.Path)
	}
	return cut, nil
}

// absorbedBy is every line of the fragments that a version below already holds. A fragment
// that came back after the version that took it would put its line into the next version a
// second time, and the journal would hold one change in two sections without saying which
// of them is the release of it (REL-026, CHG-REL-004).
func absorbedBy(released string, fragments []Fragment) []Problem {
	var problems []Problem
	for _, fragment := range fragments {
		for _, entry := range fragment.Entries {
			if !strings.Contains(released, entry.Text) {
				continue
			}
			problems = append(problems, Problem{
				Path: fragment.Path,
				Line: entry.Line,
				What: "a version below holds this line already — one line of the journal is in one version",
				Err:  ErrAbsorbed,
			})
		}
	}
	return problems
}

// saidBy is what a release found wrong, in one line: the file, the line in it and what is
// wrong there, one problem after another. A release that has written nothing can say all of
// it at once, and a person has one place to look.
func saidBy(problems []Problem) string {
	what := make([]string, 0, len(problems))
	for _, problem := range problems {
		what = append(what, problem.Error())
	}
	return strings.Join(what, "; ")
}

// Version is the number of a release out of the word that names it: `v0.2.0` is
// `0.2.0`, and a word that is not a version is refused — a release is a tag, and a tag
// is the owner's decision (scripts/release.sh).
func Version(word string) (string, bool) {
	digits, ok := strings.CutPrefix(word, "v")
	if !ok {
		return "", false
	}
	parts := strings.Split(digits, ".")
	if len(parts) != 3 {
		return "", false
	}
	for _, part := range parts {
		if part == "" {
			return "", false
		}
		for _, digit := range part {
			if digit < '0' || digit > '9' {
				return "", false
			}
		}
	}
	return digits, true
}
