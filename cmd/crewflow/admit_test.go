package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/forge"
	taskrun "github.com/naghuale/crewflow/internal/run"
)

// TestRunTaskAdmitWritesTheRecordOfAPair is what a person gets from `crewflow task admit`:
// the eight criteria with what each of them is, the decision they add up to, and where the
// record is written — in the state of the project, beside the states of its tasks
// (docs.DESIGN.md §7c).
func TestRunTaskAdmitWritesTheRecordOfAPair(t *testing.T) {
	host := &host{task: taskWithGlobs(43, "docs/**"), tasks: map[int]forge.Task{41: taskWithGlobs(41, "internal/run/**")}}
	host.use(t)
	project := host.config(t)
	var stdout, stderr bytes.Buffer

	code := run([]string{"task", "admit", "41", "43", "-c", "K2=pass", "-c", "K3=pass", "-c", "K4=pass",
		"-c", "K5=pass", "-c", "K6=pass", "-c", "K7=pass", "-c", "K8=pass", "-config", project}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow task admit = %d, want %d (stderr: %q)\n%s", code, exitOK, stderr.String(), stdout.String())
	}
	for _, want := range []string{
		"admission of the pair #41, #43: allowed",
		"K1 write paths", "pass", "K2 documents", "K8 CI",
		"record " + filepath.Join(host.home, "state", "naghuale-crewflow", "admission-41-43.json"),
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("crewflow task admit wrote %q, want it to mention %q", stdout.String(), want)
		}
	}
	if stderr.Len() != 0 {
		t.Errorf("crewflow task admit wrote %q to stderr, want nothing", stderr.String())
	}
	record, err := taskrun.LoadAdmission(filepath.Join(host.home, "state", "naghuale-crewflow", "admission-41-43.json"))
	if err != nil {
		t.Fatalf("the record of the pair is not readable: %v", err)
	}
	if err := record.Checked(); err != nil {
		t.Errorf("the record written is one crewflow cannot obey: %v", err)
	}
}

// TestRunTaskAdmitOfAPairThatDoesNotHold: the record is written whatever the eight criteria
// say, and the code of the command is not zero, because a person who asked about a pair that
// does not hold is told so by the code as well as by the eight lines (§7c).
func TestRunTaskAdmitOfAPairThatDoesNotHold(t *testing.T) {
	host := &host{task: taskWithGlobs(43, "docs/DESIGN.md"), tasks: map[int]forge.Task{
		41: taskWithGlobs(41, "docs/DESIGN.md"),
	}}
	host.use(t)
	project := host.config(t)
	var stdout, stderr bytes.Buffer

	code := run([]string{"task", "admit", "41", "43", "-c", "K2=pass", "-c", "K3=pass", "-c", "K4=pass",
		"-c", "K5=pass", "-c", "K6=pass", "-c", "K7=pass", "-c", "K8=pass", "-config", project}, &stdout, &stderr)

	if code == exitOK {
		t.Errorf("crewflow task admit = 0, want not 0:\n%s", stdout.String())
	}
	for _, want := range []string{"denied", "K1 write paths", "fail", "docs/DESIGN.md and docs/DESIGN.md"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("crewflow task admit wrote %q, want it to mention %q", stdout.String(), want)
		}
	}
}

// TestRunTaskAdmitKeepsTheDecisionOfTheOwnerApart: an exception of the owner is a decision
// of a person on this experiment, and the record keeps the criteria as they came out as well
// as the decision — a pair of two clean tasks it is not, however the decision reads (D-056).
func TestRunTaskAdmitKeepsTheDecisionOfTheOwnerApart(t *testing.T) {
	host := &host{task: taskWithGlobs(43, "docs/DESIGN.md"), tasks: map[int]forge.Task{
		41: taskWithGlobs(41, "docs/DESIGN.md"),
	}}
	host.use(t)
	project := host.config(t)
	var stdout, stderr bytes.Buffer

	code := run([]string{"task", "admit", "41", "43", "-c", "K2=pass", "-c", "K3=pass", "-c", "K4=pass",
		"-c", "K5=pass", "-c", "K6=pass", "-c", "K7=pass", "-c", "K8=pass",
		"-owner-exception", "the owner watches this pair himself", "-config", project}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow task admit = %d, want %d (stderr: %q)\n%s", code, exitOK, stderr.String(), stdout.String())
	}
	for _, want := range []string{"owner-exception", "decided this pair on his own: the owner watches this pair himself"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("crewflow task admit wrote %q, want it to mention %q", stdout.String(), want)
		}
	}
}

// TestRunTaskAdmitRefusesWhatItCannotRead walks the calls a person makes wrong: a criterion
// of another pair, a result nobody wrote, a criterion with nothing to explain it, a criterion
// crewflow works out itself, and a call that names no pair. Every one of them is refused
// where a person reads it, and nothing is written for any of them (docs/DESIGN.md §7f).
func TestRunTaskAdmitRefusesWhatItCannotRead(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "a criterion that is not one of the eight",
			args: []string{"task", "admit", "41", "43", "-c", "K9=pass"},
			want: "not one of the criteria",
		},
		{
			name: "a result nobody wrote",
			args: []string{"task", "admit", "41", "43", "-c", "K2=maybe"},
			want: "not a result of the format",
		},
		{
			name: "a criterion that is not a pass with nothing to explain it",
			args: []string{"task", "admit", "41", "43", "-c", "K2=fail"},
			want: "with no reason",
		},
		{
			name: "a line that is not a criterion at all",
			args: []string{"task", "admit", "41", "43", "-c", "documents"},
			want: "is not a criterion of a pair",
		},
		{
			name: "the criterion crewflow works out itself",
			args: []string{"task", "admit", "41", "43", "-c", "K1=pass"},
			want: "worked out by crewflow",
		},
		{
			name: "one number where a pair belongs",
			args: []string{"task", "admit", "41"},
			want: "which pair?",
		},
		{
			name: "a word where a number belongs",
			args: []string{"task", "admit", "41", "forty-three"},
			want: "is not the number of a task",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host := &host{task: taskOf(43), tasks: map[int]forge.Task{41: taskOf(41)}}
			host.use(t)
			project := host.config(t)
			var stdout, stderr bytes.Buffer

			code := run(append(tc.args, "-config", project), &stdout, &stderr)

			if code != exitUsage {
				t.Errorf("crewflow task admit = %d, want %d (stdout: %q)", code, exitUsage, stdout.String())
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Errorf("crewflow task admit wrote %q to stderr, want it to say %q", stderr.String(), tc.want)
			}
			if entries, _ := readDirOf(filepath.Join(host.home, "state")); len(entries) != 0 {
				t.Errorf("the refused call left %v in the state of the project, want nothing", entries)
			}
		})
	}
}

// TestRunTaskRunRefusesToGoBesideARunNobodyAdmittedItTo is the whole of this task seen from
// the command a person types: the run of task 43 is going while the run of task 41 is going,
// no record of the pair has been written, and the run is refused with the command that writes
// it. Nothing of the refused run is left behind (F-135, F-146, docs/DESIGN.md §7c).
func TestRunTaskRunRefusesToGoBesideARunNobodyAdmittedItTo(t *testing.T) {
	host := &host{task: taskWithGlobs(43, "docs/**"), tasks: map[int]forge.Task{41: taskWithGlobs(41, "internal/run/**")}}
	host.use(t)
	project := host.config(t)
	goingOfTest(t, host, 41)
	var stdout, stderr bytes.Buffer

	code := run([]string{"task", "run", "43", "-config", project}, &stdout, &stderr)

	if code == exitOK {
		t.Errorf("crewflow task run = 0, want not 0:\n%s", stdout.String())
	}
	for _, want := range []string{
		taskrun.AdmissionRequired, "task 43", "task 41", "crewflow task admit 41 43",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("crewflow task run wrote %q to stderr, want it to mention %q", stderr.String(), want)
		}
	}
	if len(host.started) != 0 {
		t.Errorf("the executor was started %v, want nothing: the run was refused before its worktree was made", host.started)
	}
}

// TestRunTaskRunGoesBesideARunOnARecord: the check of the issue itself — a second run of a
// project beside a going one, with a record of the pair written before it, and the run goes
// on and says in its report that it did (docs/DESIGN.md §7c).
func TestRunTaskRunGoesBesideARunOnARecord(t *testing.T) {
	host := &host{task: taskWithGlobs(43, "docs/**"), opened: true, tasks: map[int]forge.Task{
		41: taskWithGlobs(41, "internal/run/**"),
	}}
	host.use(t)
	project := host.config(t)
	goingOfTest(t, host, 41)
	var stdout, stderr bytes.Buffer
	admitted(t, host, project, "41", "43")

	code := run([]string{"task", "run", "43", "-config", project}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow task run = %d, want %d (stderr: %q)\n%s", code, exitOK, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "it went beside the run of the task #41, #43 on the admission allowed") {
		t.Errorf("crewflow task run wrote %q, want it to say the run went beside another one on a record", stdout.String())
	}
	journal := readFileOfTest(t, filepath.Join(host.home, "runs", "naghuale-crewflow", "43-1.jsonl"))
	if !strings.Contains(journal, "goes beside the pair #41, #43") {
		t.Errorf("the journal of the attempt holds %q, want it to name the pair the run went beside", journal)
	}
}

// taskWithGlobs is a whole task of a test whose boundaries are the ones a case is about: K1
// is worked out of them, so a test says which paths each of the two tasks may change
// (docs.DESIGN.md §7c).
func taskWithGlobs(number int, globs ...string) forge.Task {
	task := taskOf(number)
	task.Body = strings.Replace(wholeTask, "internal/run/**", strings.Join(globs, "\n"), 1)
	return task
}

// goingOfTest is the state of a task whose run is going now, beside the worktree it works
// in: a second run of the project is refused until the pair is admitted (docs/DESIGN.md §7c).
func goingOfTest(t *testing.T, h *host, number int) {
	t.Helper()
	journals := taskrun.JournalsOf(h.home, "naghuale-crewflow")
	started := time.Now()
	state := taskrun.State{
		Schema: taskrun.Schema, Number: number, Title: "the run of a task",
		Branch:   "crewflow/" + itoaOfTest(number) + "-the-run-of-a-task",
		Worktree: filepath.Join(h.worktrees, "naghuale-crewflow", itoaOfTest(number)),
		Profile:  "opencode",
		Attempts: []taskrun.Attempt{{
			Number: 1, StartedAt: started, LastAt: started, Outcome: taskrun.Running,
			Journal: journals.JournalPath(number, 1), ErrorJournal: journals.JournalPath(number, 1) + ".err",
		}},
	}
	if err := os.MkdirAll(state.Worktree, 0o700); err != nil {
		t.Fatalf("make the worktree of the task %d: %v", number, err)
	}
	keepsState(t, journals.StatePath(number), state)
}

// admitted is `crewflow task admit` with every criterion a person may enter a `pass`, for a
// test that goes on to check what the record of a pair lets a run do.
func admitted(t *testing.T, h *host, project, one, other string) {
	t.Helper()
	args := []string{"task", "admit", one, other, "-config", project}
	for _, name := range []string{"K2", "K3", "K4", "K5", "K6", "K7", "K8"} {
		args = append(args, "-c", name+"=pass")
	}
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr); code != exitOK {
		t.Fatalf("crewflow task admit = %d, want %d (stderr: %q)\n%s", code, exitOK, stderr.String(), stdout.String())
	}
}

// readDirOf is what a folder of a test holds, for a test that a refused call left nothing in.
func readDirOf(path string) ([]os.DirEntry, error) { return os.ReadDir(path) }

// readFileOfTest is what a file of a test holds.
func readFileOfTest(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// itoaOfTest is a number of a test as a path holds it.
func itoaOfTest(number int) string { return strconv.Itoa(number) }
