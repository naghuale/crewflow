package run

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/proc"
	"github.com/naghuale/crewflow/internal/task"
)

// theIndex is what git holds of the repository of a test: one entry per file, as
// `git ls-files -s` writes it. The files both of the tasks of a pair may change are read
// out of it, which is what says that the sections of DESIGN and the contracts a pair was
// admitted on are the ones there are now (docs.DESIGN.md §7c).
const theIndex = "100644 1f0a4e2c9b7d6e5f4a3b2c1d0e9f8a7b6c5d4e3f2 0\tdocs/DESIGN.md\n" +
	"100644 2a1b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4 0\tinternal/run/run.go\n" +
	"100644 3b2c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5 0\tinternal/task/task.go\n"

// TestAdmitWritesTheRecordOfAPair is the whole of `crewflow task admit` on a pair whose
// paths do not meet: the record lands in the state of the project under the name of the
// pair, K1 is worked out of the two sets of boundaries and of the files of the working
// copies, the seven criteria a person entered are in it, and the decision is the eight of
// them added up (docs/DESIGN.md §7c).
func TestAdmitWritesTheRecordOfAPair(t *testing.T) {
	m := newMachine(t)
	m.answers["git ls-files -s"] = answer{stdout: theIndex}
	m.has(worktreeOf(m, 41))
	going(t, m, 41, worktreeOf(m, 41))
	host := &host{task: taskWithGlobs(43, "docs/**"), tasks: map[int]forge.Task{41: taskWithGlobs(41, "internal/run/**")}}

	record, err := Admit(t.Context(), m.env(), projectOf(t, m.worktrees, ""), host.set(), AdmissionRequest{
		First: 41, Second: 43, RepoDir: m.repo, Entered: enteredAllPass(),
	})
	if err != nil {
		t.Fatalf("Admit returned an error: %v", err)
	}

	if record.Tasks != task.NewPair(41, 43) {
		t.Errorf("the record is of the pair %s, want the pair of the two tasks in one order", record.Tasks)
	}
	if record.Criteria.K1.Result != task.ResultPass {
		t.Errorf("K1 = %q (%s), want %q: neither the boundaries nor the files have anything in common",
			record.Criteria.K1.Result, record.Criteria.K1.Reason, task.ResultPass)
	}
	if record.Decision != task.DecisionAllowed {
		t.Errorf("the decision = %q, want %q: all eight criteria are a pass", record.Decision, task.DecisionAllowed)
	}
	if !slices.Equal(record.ValidWhile.Active, []int{41}) {
		t.Errorf("the record was taken against the runs %v of the project, want the one of task 41", record.ValidWhile.Active)
	}
	// The record is a file in the state of the project, beside the states of its tasks,
	// and what is written in it is a record crewflow wrote.
	path := newJournals(m.home, "naghuale-crewflow").AdmissionPath(record.Tasks)
	written := read(t, path)
	for _, want := range []string{
		`"version": 1`, `"snapshot_id"`, `"decision": "allowed"`, `"K1"`, `"K8"`, `"valid_while"`,
	} {
		if !strings.Contains(written, want) {
			t.Errorf("the record in %s does not hold %q:\n%s", path, want, written)
		}
	}
	readBack, err := LoadAdmission(path)
	if err != nil {
		t.Fatalf("the record written is not readable: %v", err)
	}
	if err := readBack.Checked(); err != nil {
		t.Errorf("the record written is one crewflow cannot obey: %v", err)
	}
}

// TestAdmitWorksOutK1FromTheDeclaredBoundaries: the boundaries of the two tasks are asked
// before either of them has written a line, because two globs that may both be true of one
// file are a common write path already. The pair of F-135 is this one — a run that went in
// at K1 = FAIL (docs/DESIGN.md §7c).
func TestAdmitWorksOutK1FromTheDeclaredBoundaries(t *testing.T) {
	m := newMachine(t)
	m.answers["git ls-files -s"] = answer{stdout: theIndex}
	host := &host{task: taskWithGlobs(43, "docs/DESIGN.md"), tasks: map[int]forge.Task{
		41: taskWithGlobs(41, "internal/run/**", "docs/DESIGN.md"),
	}}

	record, err := Admit(t.Context(), m.env(), projectOf(t, m.worktrees, ""), host.set(), AdmissionRequest{
		First: 41, Second: 43, RepoDir: m.repo, Entered: enteredAllPass(),
	})
	if err != nil {
		t.Fatalf("Admit returned an error: %v", err)
	}

	k1 := record.Criteria.K1
	if k1.Result != task.ResultFail {
		t.Errorf("K1 = %q, want %q: the two of them may both change %s", k1.Result, task.ResultFail, k1.Resource)
	}
	want := []string{"docs/DESIGN.md and docs/DESIGN.md"}
	if !slices.Equal(k1.DeclaredPatternsOverlap, want) {
		t.Errorf("the declared patterns that meet = %v, want %v", k1.DeclaredPatternsOverlap, want)
	}
	// A pair with a failed criterion is not admitted whatever the other seven say.
	if record.Decision != task.DecisionDenied {
		t.Errorf("the decision = %q, want %q", record.Decision, task.DecisionDenied)
	}
}

// TestAdmitWorksOutK1FromTheFilesTheWorkingCopiesChanged is the second half of K1: what
// the run that is going has really written into the territory of the task that is about to
// start, whatever its boundaries say about it.
func TestAdmitWorksOutK1FromTheFilesTheWorkingCopiesChanged(t *testing.T) {
	m := newMachine(t)
	m.answers["git ls-files -s"] = answer{stdout: theIndex}
	worktree := filepath.Join(m.worktrees, "naghuale-crewflow", "41")
	m.has(worktree)
	// The run of task 41 has changed a file of the repository, and one of them is inside
	// the boundaries of task 43: the common write path as it stands, and not as declared.
	m.changed[worktree] = "internal/task/task.go\nREADME.md\n"
	going(t, m, 41, worktree)
	host := &host{task: taskWithGlobs(43, "docs/**", "internal/task/**"), tasks: map[int]forge.Task{
		41: taskWithGlobs(41, "internal/task/**"),
	}}

	record, err := Admit(t.Context(), m.env(), projectOf(t, m.worktrees, ""), host.set(), AdmissionRequest{
		First: 41, Second: 43, RepoDir: m.repo, Entered: enteredAllPass(),
	})
	if err != nil {
		t.Fatalf("Admit returned an error: %v", err)
	}

	k1 := record.Criteria.K1
	if k1.Result != task.ResultFail {
		t.Errorf("K1 = %q (%s), want %q", k1.Result, k1.Reason, task.ResultFail)
	}
	if !slices.Equal(k1.ConcreteFilesOverlap, []string{"internal/task/task.go"}) {
		t.Errorf("the files the working copies have changed in common = %v, want the one inside the "+
			"boundaries of the other task", k1.ConcreteFilesOverlap)
	}
}

// TestAdmitLeavesWhatNobodyEnteredUnknown: a criterion a person did not enter is
// `unknown` and not `pass`, and the record says which command enters it. A record that
// left it out quietly would be a record that admits a pair nobody checked (MODEL, IV-031).
func TestAdmitLeavesWhatNobodyEnteredUnknown(t *testing.T) {
	m := newMachine(t)
	m.answers["git ls-files -s"] = answer{stdout: theIndex}
	host := &host{task: taskWithGlobs(43, "docs/**"), tasks: map[int]forge.Task{41: taskWithGlobs(41, "internal/run/**")}}

	entered := enteredAllPass()
	delete(entered, "K5")

	record, err := Admit(t.Context(), m.env(), projectOf(t, m.worktrees, ""), host.set(), AdmissionRequest{
		First: 41, Second: 43, RepoDir: m.repo, Entered: entered,
	})
	if err != nil {
		t.Fatalf("Admit returned an error: %v", err)
	}

	five, _ := record.Criteria.One("K5")
	if five.Result != task.ResultUnknown {
		t.Errorf("K5 = %q, want %q: nobody entered it", five.Result, task.ResultUnknown)
	}
	if !strings.Contains(five.Reason, "-c K5=") {
		t.Errorf("K5 says %q, want it to name the flag that enters it", five.Reason)
	}
	if record.Decision != task.DecisionDenied {
		t.Errorf("the decision = %q, want %q: a criterion nobody worked out is not a pass", record.Decision, task.DecisionDenied)
	}
}

// TestAdmitKeepsTheDecisionOfTheOwnerApart: an exception of the owner is a decision of a
// person on this experiment, and it is kept beside the criteria and not instead of them:
// the record still says what was checked and what it came out as, and the decision is a
// word of its own, which is what keeps it out of the pairs of two clean tasks (D-056).
func TestAdmitKeepsTheDecisionOfTheOwnerApart(t *testing.T) {
	m := newMachine(t)
	m.answers["git ls-files -s"] = answer{stdout: theIndex}
	host := &host{task: taskWithGlobs(43, "docs/DESIGN.md"), tasks: map[int]forge.Task{
		41: taskWithGlobs(41, "docs/DESIGN.md"),
	}}

	record, err := Admit(t.Context(), m.env(), projectOf(t, m.worktrees, ""), host.set(), AdmissionRequest{
		First: 41, Second: 43, RepoDir: m.repo,
		Entered:   enteredAllPass(),
		Exception: "the owner watches this pair himself",
	})
	if err != nil {
		t.Fatalf("Admit returned an error: %v", err)
	}

	if record.Decision != task.DecisionOwnerException {
		t.Errorf("the decision = %q, want %q", record.Decision, task.DecisionOwnerException)
	}
	if record.Owner == nil || record.Owner.Why != "the owner watches this pair himself" {
		t.Errorf("the record holds the owner %+v, want what he said", record.Owner)
	}
	if record.Criteria.K1.Result != task.ResultFail {
		t.Errorf("K1 = %q, want %q: the decision of the owner is not a criterion that passed", record.Criteria.K1.Result, task.ResultFail)
	}
	if err := record.Checked(); err != nil {
		t.Errorf("the record of an exception is one crewflow cannot obey: %v", err)
	}
}

// TestTaskRunRefusesToGoBesideARunNobodyAdmittedItTo is the failure this task is for: a
// second run of one project while the first is going, with no record of the admission of
// the pair. The run is refused with the command that writes the record, and nothing is
// created for it: no branch, no worktree, no attempt, no window of the system (F-135,
// F-146, docs/DESIGN.md §7c, §7i).
func TestTaskRunRefusesToGoBesideARunNobodyAdmittedItTo(t *testing.T) {
	m := newMachine(t)
	m.answers["opencode"] = answer{stdout: theRun}
	m.answers["git ls-files -s"] = answer{stdout: theIndex}
	m.has(worktreeOf(m, 41))
	going(t, m, 41, worktreeOf(m, 41))
	host := &host{task: taskWithGlobs(43, "docs/**"), tasks: map[int]forge.Task{41: taskWithGlobs(41, "internal/run/**")}}

	_, err := Run(t.Context(), m.env(), projectOf(t, m.worktrees, ""), host.set(), Request{Number: 43, RepoDir: m.repo})

	refusal := refusedBy(t, err, 43)
	if refusal.Word != AdmissionRequired {
		t.Errorf("the refusal is %q, want %q", refusal.Word, AdmissionRequired)
	}
	if refusal.Beside != 41 {
		t.Errorf("the refusal names the task %d it is about, want the run of task 41", refusal.Beside)
	}
	if !strings.Contains(refusal.Detail, "crewflow task admit 41 43") {
		t.Errorf("the refusal says %q, want it to name the command that writes the record", refusal.Detail)
	}
	if m.commandOf("opencode") != nil {
		t.Error("the executor was started beside a run nobody admitted the pair to")
	}
	if _, err := os.Stat(filepath.Join(m.worktrees, "naghuale-crewflow", "43")); !os.IsNotExist(err) {
		t.Error("the refused run made a worktree, want a refusal that leaves nothing behind")
	}
	if _, err := os.Stat(newJournals(m.home, "naghuale-crewflow").StatePath(43)); !os.IsNotExist(err) {
		t.Error("the refused run wrote a state of the task, want none")
	}
}

// TestTaskRunRefusesARecordThatIsOutOfDate walks the ways the world of a pair moves on
// under the record of it: the files both of the tasks may change, and the specification of
// a task. A record of yesterday's pair is not a record of this one, and applying it would
// be applying a decision about one pair to another (docs/DESIGN.md §7c).
func TestTaskRunRefusesARecordThatIsOutOfDate(t *testing.T) {
	cases := []struct {
		name string
		// moved is what happens between the record and the run.
		moved func(t *testing.T, m *machine, host *host)
		want  string
	}{
		{
			// A file both of the two tasks may change is a contract or a document of the
			// project, and what it holds a minute ago is not what the pair was admitted on
			// (D-055, §7c).
			name: "what a file both of the tasks may change holds",
			moved: func(_ *testing.T, m *machine, _ *host) {
				m.answers["git ls-files -s"] = answer{stdout: strings.Replace(theIndex,
					"100644 2a1b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4 0\tinternal/run/run.go",
					"100644 9f8e7d6c5b4a39281706f5e4d3c2b1a09988776 0\tinternal/run/run.go", 1)}
			},
			want: "the files both of the tasks may change have changed",
		},
		{
			name: "the specification of the task that is to start",
			moved: func(t *testing.T, _ *machine, host *host) {
				changed := taskWithGlobs(43, "docs/**")
				changed.Body += "\nOne more line the owner wrote after the record was written.\n"
				host.task = changed
			},
			want: "the specification of the task #43 has changed",
		},
		{
			name: "what a task declares it may change",
			moved: func(t *testing.T, _ *machine, host *host) {
				host.tasks[41] = taskWithGlobs(41, "internal/task/**")
			},
			want: "what the task #41 declares it may change has changed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine(t)
			m.answers["opencode"] = answer{stdout: theRun}
			m.answers["git ls-files -s"] = answer{stdout: theIndex}
			m.has(worktreeOf(m, 41))
			going(t, m, 41, worktreeOf(m, 41))
			// The pair of this case shares a folder of the project, so that the files both
			// of the tasks may change are files there are: the digest of them is what says
			// that the sections of DESIGN and the contracts a pair was admitted on are the
			// ones there are now (D-055, §7c).
			host := &host{task: taskWithGlobs(43, "internal/run/**"), tasks: map[int]forge.Task{41: taskWithGlobs(41, "internal/run/**")}}
			if _, err := Admit(t.Context(), m.env(), projectOf(t, m.worktrees, ""), host.set(), AdmissionRequest{
				First: 41, Second: 43, RepoDir: m.repo, Entered: enteredAllPass(),
			}); err != nil {
				t.Fatalf("Admit returned an error: %v", err)
			}
			tc.moved(t, m, host)

			_, err := Run(t.Context(), m.env(), projectOf(t, m.worktrees, ""), host.set(), Request{Number: 43, RepoDir: m.repo})

			refusal := refusedBy(t, err, 43)
			if refusal.Word != AdmissionRequired {
				t.Errorf("the refusal is %q, want %q: the record is out of date", refusal.Word, AdmissionRequired)
			}
			if !strings.Contains(refusal.Detail, tc.want) {
				t.Errorf("the refusal says %q, want it to name %q", refusal.Detail, tc.want)
			}
			if m.commandOf("opencode") != nil {
				t.Error("the executor was started on a record that is out of date")
			}
		})
	}
}

// TestTaskRunRefusesARecordWrittenBeforeAnyRunWasGoing: a record is written about a pair
// while the project is free, and a run of another task starts before the second of the pair
// does. The record is about a project with nothing going on in it, and it is not a record of
// the pair that is about to run beside one that is (F-146, docs/DESIGN.md §7c).
func TestTaskRunRefusesARecordWrittenBeforeAnyRunWasGoing(t *testing.T) {
	m := newMachine(t)
	m.answers["opencode"] = answer{stdout: theRun}
	m.answers["git ls-files -s"] = answer{stdout: theIndex}
	host := &host{task: taskWithGlobs(43, "docs/**"), tasks: map[int]forge.Task{41: taskWithGlobs(41, "internal/run/**")}}
	record, err := Admit(t.Context(), m.env(), projectOf(t, m.worktrees, ""), host.set(), AdmissionRequest{
		First: 41, Second: 43, RepoDir: m.repo, Entered: enteredAllPass(),
	})
	if err != nil {
		t.Fatalf("Admit returned an error: %v", err)
	}
	if len(record.ValidWhile.Active) != 0 {
		t.Fatalf("the record was taken against the runs %v of the project, want none of them", record.ValidWhile.Active)
	}
	m.has(worktreeOf(m, 41))
	going(t, m, 41, worktreeOf(m, 41))

	_, err = Run(t.Context(), m.env(), projectOf(t, m.worktrees, ""), host.set(), Request{Number: 43, RepoDir: m.repo})

	refusal := refusedBy(t, err, 43)
	if refusal.Word != AdmissionRequired {
		t.Errorf("the refusal is %q, want %q", refusal.Word, AdmissionRequired)
	}
	if !strings.Contains(refusal.Detail, "the set of the tasks going on in the project has changed") {
		t.Errorf("the refusal says %q, want it to name the set of the runs going on", refusal.Detail)
	}
	if m.commandOf("opencode") != nil {
		t.Error("the executor was started on a record written while nothing of the project was running")
	}
}

// TestTaskRunRefusesACriterionNobodyWorkedOut is the second word of a refusal: the pair
// has a record, the record is about this pair in this state of the world, and one of the
// eight criteria is `unknown`. An unverified criterion is not a pass, and a run that went
// in on one is the failure the whole record is for (MODEL, IV-031, docs/DESIGN.md §7c).
func TestTaskRunRefusesACriterionNobodyWorkedOut(t *testing.T) {
	m := newMachine(t)
	m.answers["opencode"] = answer{stdout: theRun}
	m.answers["git ls-files -s"] = answer{stdout: theIndex}
	m.has(worktreeOf(m, 41))
	going(t, m, 41, worktreeOf(m, 41))
	host := &host{task: taskWithGlobs(43, "docs/**"), tasks: map[int]forge.Task{41: taskWithGlobs(41, "internal/run/**")}}
	entered := enteredAllPass()
	delete(entered, "K7")
	if _, err := Admit(t.Context(), m.env(), projectOf(t, m.worktrees, ""), host.set(), AdmissionRequest{
		First: 41, Second: 43, RepoDir: m.repo, Entered: entered,
	}); err != nil {
		t.Fatalf("Admit returned an error: %v", err)
	}

	_, err := Run(t.Context(), m.env(), projectOf(t, m.worktrees, ""), host.set(), Request{Number: 43, RepoDir: m.repo})

	refusal := refusedBy(t, err, 43)
	if refusal.Word != AdmissionUnknown || refusal.Criterion != "K7" {
		t.Errorf("the refusal is %q criterion=%s, want %q criterion=K7",
			refusal.Word, refusal.Criterion, AdmissionUnknown)
	}
	if !strings.Contains(refusal.Detail, "-c K7=") {
		t.Errorf("the refusal says %q, want the reason the record itself holds", refusal.Detail)
	}
	if m.commandOf("opencode") != nil {
		t.Error("the executor was started on a criterion nobody worked out")
	}
}

// TestTaskRunRefusesACriterionThatFailed is the third word: a criterion with a `fail` in
// it, and the refusal names the criterion and the resource the two of them have in common,
// which is what a person acts upon (§7c, F-135).
func TestTaskRunRefusesACriterionThatFailed(t *testing.T) {
	m := newMachine(t)
	m.answers["opencode"] = answer{stdout: theRun}
	m.answers["git ls-files -s"] = answer{stdout: theIndex}
	m.has(worktreeOf(m, 41))
	going(t, m, 41, worktreeOf(m, 41))
	host := &host{task: taskWithGlobs(43, "docs/**"), tasks: map[int]forge.Task{41: taskWithGlobs(41, "internal/run/**")}}
	entered := enteredAllPass()
	entered["K3"] = task.Criterion{Result: task.ResultFail, Resource: "crewflow.toml", Reason: "the registry of the settings is one"}
	if _, err := Admit(t.Context(), m.env(), projectOf(t, m.worktrees, ""), host.set(), AdmissionRequest{
		First: 41, Second: 43, RepoDir: m.repo, Entered: entered,
	}); err != nil {
		t.Fatalf("Admit returned an error: %v", err)
	}

	_, err := Run(t.Context(), m.env(), projectOf(t, m.worktrees, ""), host.set(), Request{Number: 43, RepoDir: m.repo})

	refusal := refusedBy(t, err, 43)
	if refusal.Word != AdmissionDenied || refusal.Criterion != "K3" || refusal.Resource != "crewflow.toml" {
		t.Errorf("the refusal is %q criterion=%s resource=%s, want %q criterion=K3 resource=crewflow.toml",
			refusal.Word, refusal.Criterion, refusal.Resource, AdmissionDenied)
	}
	if !strings.Contains(refusal.Detail, "the registry of the settings is one") {
		t.Errorf("the refusal says %q, want the reason a person entered", refusal.Detail)
	}
	said := err.Error()
	for _, want := range []string{AdmissionDenied, "criterion=K3", "resource=crewflow.toml"} {
		if !strings.Contains(said, want) {
			t.Errorf("the refusal %q does not hold %q, want a line a program can read", said, want)
		}
	}
	if m.commandOf("opencode") != nil {
		t.Error("the executor was started on a criterion that failed")
	}
}

// TestTaskRunGoesOnWithTheExceptionOfTheOwner: a record of `owner-exception` is a decision
// of a person and the run goes on with it, and says so in the journal of the attempt and in
// the report — a run that went beside another one on his decision is not a pair of two
// clean tasks, whatever the criteria of its record look like (D-056, docs.DESIGN.md §7c).
func TestTaskRunGoesOnWithTheExceptionOfTheOwner(t *testing.T) {
	m := newMachine(t)
	m.answers["opencode"] = answer{stdout: theRun}
	m.answers["git ls-files -s"] = answer{stdout: theIndex}
	m.has(worktreeOf(m, 41))
	going(t, m, 41, worktreeOf(m, 41))
	host := &host{task: taskWithGlobs(43, "docs/DESIGN.md", "internal/run/**"), opened: true, tasks: map[int]forge.Task{
		41: taskWithGlobs(41, "internal/run/**"),
	}}
	record, err := Admit(t.Context(), m.env(), projectOf(t, m.worktrees, ""), host.set(), AdmissionRequest{
		First: 41, Second: 43, RepoDir: m.repo, Entered: enteredAllPass(),
		Exception: "the owner watches this pair himself",
	})
	if err != nil {
		t.Fatalf("Admit returned an error: %v", err)
	}
	if record.Criteria.K1.Result != task.ResultFail {
		t.Fatalf("K1 = %q, want %q: this pair shares a folder and the owner took it anyway", record.Criteria.K1.Result, task.ResultFail)
	}

	result, err := Run(t.Context(), m.env(), projectOf(t, m.worktrees, ""), host.set(), Request{Number: 43, RepoDir: m.repo})

	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}
	if result.Admission == nil || result.Admission.Decision != task.DecisionOwnerException {
		t.Fatalf("the result holds the admission %+v, want the exception of the owner", result.Admission)
	}
	if result.Admission.Owner == nil || !strings.Contains(result.Admission.Owner.Why, "watches this pair") {
		t.Errorf("the result holds the owner %+v, want what he said", result.Admission.Owner)
	}
	journal := read(t, result.Journal)
	for _, want := range []string{"goes beside the pair #41, #43", task.DecisionOwnerException, "watches this pair himself"} {
		if !strings.Contains(journal, want) {
			t.Errorf("the journal of the attempt does not hold %q:\n%s", want, journal)
		}
	}
}

// TestTaskRunAloneAsksNothingAboutAnyPair: a run that has the project to itself is the
// common case, and it is not one crewflow pays a pair of git questions for — the rule is
// about the second run, and a first run has no pair to admit (docs.DESIGN.md §7c).
func TestTaskRunAloneAsksNothingAboutAnyPair(t *testing.T) {
	m := newMachine(t)
	m.answers["opencode"] = answer{stdout: theRun}
	host := &host{task: taskOf(43), opened: true}

	result, err := Run(t.Context(), m.env(), projectOf(t, m.worktrees, ""), host.set(), Request{Number: 43, RepoDir: m.repo})

	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}
	if result.Admission != nil {
		t.Errorf("the result holds the admission %+v, want none: the project was free", result.Admission)
	}
	if m.ranCommand("git ls-files") {
		t.Errorf("git was asked for the files of the repository: %v", m.lines())
	}
}

// TestTaskRunRefusesAThirdRunOfTheProject: не больше двух задач одного проекта идут рядом
// (D-051), and a record of one pair admits no third run — the pair of the run that is going
// and the task that starts is one pair, and nothing in the record of it says anything about
// the other one.
func TestTaskRunRefusesAThirdRunOfTheProject(t *testing.T) {
	m := newMachine(t)
	m.answers["opencode"] = answer{stdout: theRun}
	m.answers["git ls-files -s"] = answer{stdout: theIndex}
	for _, number := range []int{39, 41} {
		m.has(worktreeOf(m, number))
		going(t, m, number, worktreeOf(m, number))
	}
	host := &host{task: taskOf(43), tasks: map[int]forge.Task{
		39: taskWithGlobs(39, "internal/run/**"), 41: taskWithGlobs(41, "docs/**"),
	}}

	_, err := Run(t.Context(), m.env(), projectOf(t, m.worktrees, ""), host.set(), Request{Number: 43, RepoDir: m.repo})

	refusal := refusedBy(t, err, 43)
	if refusal.Word != AdmissionRequired {
		t.Errorf("the refusal is %q, want %q", refusal.Word, AdmissionRequired)
	}
	for _, want := range []string{"#39", "#41", "third run"} {
		if !strings.Contains(refusal.Detail, want) {
			t.Errorf("the refusal says %q, want it to name %q", refusal.Detail, want)
		}
	}
	if m.commandOf("opencode") != nil {
		t.Error("the executor was started as a third run of the project")
	}
}

// TestTaskRunRefusesARecordThatIsNotOneCrewflowWrote: a record a hand has been in — a
// criterion turned into a `pass` after the fact — is refused, because a rule applied from a
// record nobody wrote is a rule nobody checked (docs/DESIGN.md §7c, §7h).
func TestTaskRunRefusesARecordThatIsNotOneCrewflowWrote(t *testing.T) {
	m := newMachine(t)
	m.answers["opencode"] = answer{stdout: theRun}
	m.answers["git ls-files -s"] = answer{stdout: theIndex}
	m.has(worktreeOf(m, 41))
	going(t, m, 41, worktreeOf(m, 41))
	host := &host{task: taskWithGlobs(43, "internal/run/**"), tasks: map[int]forge.Task{41: taskWithGlobs(41, "internal/run/**")}}
	record, err := Admit(t.Context(), m.env(), projectOf(t, m.worktrees, ""), host.set(), AdmissionRequest{
		First: 41, Second: 43, RepoDir: m.repo, Entered: enteredAllPass(),
	})
	if err != nil {
		t.Fatalf("Admit returned an error: %v", err)
	}
	if record.Decision != task.DecisionDenied {
		t.Fatalf("the decision of the record = %q, want %q: this pair shares a folder", record.Decision, task.DecisionDenied)
	}
	// A hand turns the criterion that failed into a pass and the decision into `allowed`,
	// and the digest of the record is left as `crewflow task admit` wrote it.
	record.Criteria.K1 = task.WritePaths{Criterion: task.Criterion{
		Result: task.ResultPass, Reason: "the two of them write nothing in common"}}
	record.Decision = task.DecisionAllowed
	if err := SaveAdmission(newJournals(m.home, "naghuale-crewflow").AdmissionPath(record.Tasks), record); err != nil {
		t.Fatalf("write the record: %v", err)
	}

	_, err = Run(t.Context(), m.env(), projectOf(t, m.worktrees, ""), host.set(), Request{Number: 43, RepoDir: m.repo})

	refusal := refusedBy(t, err, 43)
	if refusal.Word != AdmissionRequired || !strings.Contains(refusal.Detail, "was written by somebody") {
		t.Errorf("the refusal is %q and says %q, want it to be about a record a hand has been in",
			refusal.Word, refusal.Detail)
	}
}

// TestGoingTasksAsksTheMachineAboutTheProcess is what takes a run crewflow was killed in the
// middle of out of the set of the runs that are going: the state of the task says `running`
// and the process the run wrote down says whether anybody is behind it (§7). A machine that
// cannot be asked keeps the run in the set, because a second run that begins while the first
// is going is the failure the admission of a pair is here to prevent (§7c).
func TestGoingTasksAsksTheMachineAboutTheProcess(t *testing.T) {
	gone := proc.Process{Pid: 4242, StartedAt: time.Date(2026, time.October, 3, 9, 0, 0, 0, time.UTC)}
	cases := []struct {
		name  string
		alive Liveness
		want  []int
	}{
		{name: "a machine that cannot be asked", alive: nil, want: []int{41}},
		{name: "the process of the run is still there", alive: func(proc.Process) bool { return true }, want: []int{41}},
		{name: "the process is gone", alive: func(proc.Process) bool { return false }, want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine(t)
			m.has(worktreeOf(m, 41))
			going(t, m, 41, worktreeOf(m, 41))

			got, err := goingTasks(newJournals(m.home, "naghuale-crewflow").stateFolder(), tc.alive)

			if err != nil {
				t.Fatalf("goingTasks returned an error: %v", err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("the runs going on = %v, want %v (the state names the process %+v)", got, tc.want, gone)
			}
		})
	}
}

// TestAdmissionPathIsInTheStateOfTheProject: the record of a pair is one file beside the
// states of the tasks of the project, and nothing reads it as a state of a task: the name
// is not the name of one, and a list of runs has nothing to say about a decision (§6, §7c).
func TestAdmissionPathIsInTheStateOfTheProject(t *testing.T) {
	journals := newJournals("/home/someone/.crewflow", "naghuale-crewflow")

	want := filepath.Join("/home/someone/.crewflow", "state", "naghuale-crewflow", "admission-41-43.json")
	if got := journals.AdmissionPath(task.NewPair(43, 41)); got != want {
		t.Errorf("the record of the pair is at %q, want %q", got, want)
	}
	if base := filepath.Base(journals.AdmissionPath(task.NewPair(41, 43))); isStateOfATask(base) {
		t.Errorf("the record of a pair is named %q, and that is the name of a state of a task", base)
	}
}

// taskWithGlobs is a whole task of a test whose boundaries are the ones a case is about:
// K1 is worked out of them, so a test of it says which paths each of the two tasks may
// change (docs/DESIGN.md §7c).
func taskWithGlobs(number int, globs ...string) forge.Task {
	task := taskOf(number)
	task.Body = strings.Replace(wholeTask, "internal/run/**", strings.Join(globs, "\n"), 1)
	return task
}

// worktreeOf is the working copy of a task, where crewflow makes it.
func worktreeOf(m *machine, number int) string {
	return filepath.Join(m.worktrees, "naghuale-crewflow", fmt.Sprint(number))
}

// going is the state of a task whose run is going now: one attempt that says `running`,
// with the working copy it works in beside it (docs/DESIGN.md §7).
func going(t *testing.T, m *machine, number int, worktree string) {
	t.Helper()
	journals := newJournals(m.home, "naghuale-crewflow")
	started := m.at()
	process, _ := m.process()
	state := State{
		Schema:   Schema,
		Number:   number,
		Title:    "the run of a task",
		Branch:   Branch(taskOf(number)),
		Worktree: worktree,
		Profile:  "opencode",
		Attempts: []Attempt{{
			Number: 1, StartedAt: started, LastAt: started, Outcome: Running,
			Journal:          journals.JournalPath(number, 1),
			ErrorJournal:     journals.JournalPath(number, 1) + ".err",
			PID:              process.Pid,
			ProcessStartedAt: &process.StartedAt,
		}},
	}
	if err := SaveState(journals.StatePath(number), state); err != nil {
		t.Fatalf("write the state of the task %d: %v", number, err)
	}
}

// enteredAllPass is what a person enters for the seven criteria crewflow cannot work out
// when the pair holds: seven `pass` and a reason for none of them.
func enteredAllPass() map[string]task.Criterion {
	entered := map[string]task.Criterion{}
	for _, name := range task.CriteriaNames() {
		if name != "K1" {
			entered[name] = task.Criterion{Result: task.ResultPass}
		}
	}
	return entered
}

// refusedBy is the refusal of a run that was not started, and that the test reads: a run
// that has not begun has no result to say what came of it, and the only thing it has is why
// it did not (docs.DESIGN.md §6, §7c).
func refusedBy(t *testing.T, err error, number int) *ErrAdmission {
	t.Helper()
	var refusal *ErrAdmission
	if !errors.As(err, &refusal) {
		t.Fatalf("Run returned %v, want an *ErrAdmission", err)
	}
	if refusal.Task != number {
		t.Errorf("the refusal is about the task %d, want the task %d that was not started", refusal.Task, number)
	}
	return refusal
}
