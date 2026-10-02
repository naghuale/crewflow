package run

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/task"
)

// The admission of a pair of tasks to run beside one another is a record in the state of
// the project, beside the states of its tasks: `admission-<a>-<b>.json`, with the two
// numbers in one order whatever order the pair was named in. Nothing reads it as a state
// of a task — its name is not the name of one, and a list of runs and a queue of
// attention have nothing to say about a decision (docs/DESIGN.md §6, §7c).

// The words a run beside another one is refused with. They are the words of §7c and
// nothing else, so that a program reads one refusal for one reason and a word nobody
// wrote is a word nobody reads (docs/DESIGN.md §7e).
const (
	// AdmissionRequired says that there is no record of the admission of the pair, or
	// that the record there is not about this pair in this state of the world.
	AdmissionRequired = "parallel-admission-required"
	// AdmissionUnknown says that a criterion of the pair was not worked out, or that
	// crewflow could not work out what it needs to work it out. An unverified criterion
	// is not a pass (C6, MODEL IV-031).
	AdmissionUnknown = "parallel-admission-unknown"
	// AdmissionDenied says that a criterion of the pair failed, and the refusal names the
	// criterion and the resource both of the tasks have.
	AdmissionDenied = "parallel-admission-denied"
)

// ErrAdmission is the refusal of a run that may not start beside the run that is going: a
// type and not a line of text, because a caller has to tell the three refusals apart —
// there is no record, a criterion was not worked out, a criterion failed — and each of
// them is acted upon by somebody different (§7c).
type ErrAdmission struct {
	// Task is the task that is not to be started, and Beside the task whose run is going
	// now.
	Task   int
	Beside int
	// Word is one of the three words above: the refusal a program switches on.
	Word string
	// Criterion is the criterion of the pair that stopped it, "K1"…"K8", and Resource is
	// what the two tasks have in common where the criterion failed. Both are empty where
	// no criterion of the pair is what stopped it.
	Criterion string
	Resource  string
	// Detail is what a person has to do about it, in a sentence: the command that writes
	// the record, or the criterion nobody worked out and why.
	Detail string
}

// Error names the pair, the word of the refusal, the criterion and the resource where
// there are such, and what to do about it: a refusal a person cannot read is a refusal
// they will ask about again (docs/DESIGN.md §7e).
func (e *ErrAdmission) Error() string {
	said := fmt.Sprintf("task %d is not admitted to run beside the run of task %d: %s",
		e.Task, e.Beside, e.Word)
	if e.Criterion != "" {
		said += " criterion=" + e.Criterion
	}
	if e.Resource != "" {
		said += " resource=" + e.Resource
	}
	if e.Detail != "" {
		said += " — " + e.Detail
	}
	return said
}

// AdmissionRequest is one request to write the record of a pair.
type AdmissionRequest struct {
	// First and Second are the two tasks of the pair, in the order they were named.
	First, Second int
	// RepoDir is the repository the working copies of the pair are of: the folder
	// crewflow was called in when it is empty.
	RepoDir string
	// Entered is what a person wrote for the criteria crewflow cannot work out, by the
	// name of the criterion — K2…K8, K1 is worked out and entered by nobody. A criterion
	// nobody entered is `unknown`, and a criterion entered without a reason where its
	// result is not a `pass` is a call crewflow refuses rather than a record it writes.
	Entered map[string]task.Criterion
	// Exception is the decision of the owner of the project to let this pair run beside
	// the other one whatever the criteria of it say, and why he gave it: записанное решение
	// владельца на текущий эксперимент (D-056).
	Exception string
}

// Admit is `crewflow task admit <A> <B>`: it takes both of the tasks from the tracker,
// works K1 out of the paths they declare and out of the files their working copies have
// really changed, adds what a person entered for the other seven criteria, and writes the
// record of the decision of the pair (docs/DESIGN.md §7c).
//
// Nothing is refused here but a pair crewflow cannot read: a record is written for every
// pair anybody asks about, whatever it says. The refusal of a pair that is not admitted
// belongs to the run that is not to start — `crewflow task run` reads the record and
// refuses with the criterion and the resource, because a record of what the eight criteria
// say is not a verdict about a run and a person has to see the eight lines before he
// decides what to do with them.
func Admit(ctx context.Context, env Env, cfg config.Config, set forge.Set, req AdmissionRequest) (task.Admission, error) {
	if set.Tracker == nil {
		return task.Admission{}, fmt.Errorf("tracker.kind: this project has no tracker of tasks crewflow can read, " +
			"so there is no pair of them to admit")
	}
	first, err := set.Tracker.Task(ctx, req.First)
	if err != nil {
		return task.Admission{}, fmt.Errorf("read task %d: %w", req.First, err)
	}
	second, err := set.Tracker.Task(ctx, req.Second)
	if err != nil {
		return task.Admission{}, fmt.Errorf("read task %d: %w", req.Second, err)
	}
	journals := newJournals(env.Home, cfg.RepoName())
	repoDir := req.RepoDir
	if repoDir == "" {
		repoDir = "."
	}
	world, err := snapshotOf(ctx, env, cfg, journals, repoDir, env.Alive, first, second)
	if err != nil {
		return task.Admission{}, err
	}
	pair := task.NewPair(req.First, req.Second)
	record := task.Admission{
		Version:   task.AdmissionVersion,
		Tasks:     pair,
		Decision:  task.DecisionDenied,
		CreatedAt: env.Now(),
		// The stable number of the repository is nothing until #120 fills it: a record
		// says which project it is of with the pair and the state of the project it is
		// written in, and a number nobody keeps is not made up here (§6a, §7g).
		ValidWhile: world,
	}
	record.Criteria, err = criteriaOf(ctx, env, cfg, journals, repoDir, first, second, req.Entered)
	if err != nil {
		return task.Admission{}, err
	}
	record.Decision = decisionOf(record.Criteria)
	if req.Exception != "" {
		record.Decision, record.Owner = task.DecisionOwnerException, &task.OwnerException{
			Why: req.Exception,
			At:  record.CreatedAt,
		}
	}
	record, err = record.Set()
	if err != nil {
		return task.Admission{}, err
	}
	if err := SaveAdmission(journals.AdmissionPath(pair), record); err != nil {
		return task.Admission{}, err
	}
	return record, nil
}

// criteriaOf is the eight criteria of a pair as a record holds them: K1 worked out by
// crewflow, K2…K8 as a person entered them, and every criterion nobody entered `unknown`
// with the reason it says. A criterion nobody checked is not a pass (MODEL, IV-031), and
// a record that leaves one out is a record that says so rather than one that hides it.
func criteriaOf(ctx context.Context, env Env, cfg config.Config, journals Journals, repoDir string,
	first, second forge.Task, entered map[string]task.Criterion) (task.Criteria, error) {
	for _, name := range slices.Sorted(maps.Keys(entered)) {
		if name == "K1" {
			return task.Criteria{}, fmt.Errorf("K1 is worked out by `crewflow task admit` out of the boundaries of the " +
				"two tasks and the files their working copies have changed, and it is entered by nobody: " +
				"a criterion a person enters is a criterion nobody checked")
		}
		if !slices.Contains(task.CriteriaNames(), name) {
			return task.Criteria{}, fmt.Errorf("%q is not one of the criteria of a pair: %s",
				name, strings.Join(task.CriteriaNames(), ", "))
		}
	}
	paths, err := writePaths(ctx, env, cfg, journals, repoDir, first, second)
	if err != nil {
		return task.Criteria{}, err
	}
	criteria := task.Criteria{K1: paths}
	for _, name := range task.CriteriaNames() {
		if name == "K1" {
			// K1 is worked out above and entered by nobody.
			continue
		}
		one, is := entered[name]
		if !is {
			one = task.Criterion{
				Result: task.ResultUnknown,
				Reason: "nobody has checked it: `crewflow task admit` is told what it is with -c " + name + "=…",
			}
		}
		criteria.Set(name, one)
	}
	return criteria, nil
}

// decisionOf is what the eight criteria of a pair add up to: `allowed` where all of them
// are a `pass`, and `denied` wherever one of them is not. The record keeps the decision in
// the criteria and beside them at once, and a record whose decision is `allowed` while a
// criterion is not a `pass` is refused as the record it is (§7c, MODEL IV-031).
func decisionOf(criteria task.Criteria) string {
	if criteria.Verified() {
		return task.DecisionAllowed
	}
	return task.DecisionDenied
}

// writePaths is K1 of a pair: the common write paths of the two tasks, out of the paths
// they declare and out of the files their working copies have really changed.
//
// The declared boundaries are asked first, because a boundary is what the task is going to
// be held to and it is written before the run: two globs that may both be true of one file
// are a common write path already. The files of the working copies are asked after, and
// they are what the first run of the pair has really written into the territory of the
// second — the pair that went in at K1 = FAIL is what this is for (F-135).
//
// A task whose run has not started has no working copy and has changed nothing, and there
// is nothing to compare: what it writes outside its boundaries is caught by its own run as
// `out-of-scope` (§7c). A working copy the state of a task names and that is not there any
// more is an unknown, and not an empty set: what it changed, nobody knows.
func writePaths(ctx context.Context, env Env, cfg config.Config, journals Journals, repoDir string,
	first, second forge.Task) (task.WritePaths, error) {
	language := cfg.Project.Language
	mine := task.Read(first.Body, language).Boundaries()
	theirs := task.Read(second.Body, language).Boundaries()
	paths := task.WritePaths{Criterion: task.Criterion{Result: task.ResultUnknown}}
	switch {
	case len(mine) == 0:
		paths.Reason = fmt.Sprintf("the task #%d declares no boundaries, and there is nothing to check K1 against", first.Number)
		return paths, nil
	case len(theirs) == 0:
		paths.Reason = fmt.Sprintf("the task #%d declares no boundaries, and there is nothing to check K1 against", second.Number)
		return paths, nil
	}
	paths.DeclaredPatternsOverlap = task.Overlap(mine, theirs)
	common, problem, err := commonFiles(ctx, env, cfg, journals, first, second, mine, theirs)
	if err != nil {
		return task.WritePaths{}, err
	}
	paths.ConcreteFilesOverlap = common
	switch {
	case len(paths.DeclaredPatternsOverlap) > 0:
		paths.Result = task.ResultFail
		paths.Resource = paths.DeclaredPatternsOverlap[0]
		paths.Reason = "the boundaries of the two tasks may both be true of one file"
		return paths, nil
	case len(common) > 0:
		paths.Result = task.ResultFail
		paths.Resource = common[0]
		paths.Reason = "one of the working copies has changed a file the other of the two may change"
		return paths, nil
	case problem != "":
		paths.Reason = problem
		return paths, nil
	}
	paths.Result = task.ResultPass
	paths.Reason = "neither the declared boundaries nor the files of the working copies have anything in common"
	return paths, nil
}

// commonFiles are the files the working copies of the two tasks have really changed that
// the other of the two may change as well, in one order, and the problem that stopped the
// question where there is one. Both directions are asked: a run of the first task that
// changed the second one's file is a common write path, whichever of the two started
// first.
func commonFiles(ctx context.Context, env Env, cfg config.Config, journals Journals,
	first, second forge.Task, mine, theirs task.Boundaries) ([]string, string, error) {
	var common []string
	for _, side := range []struct {
		task   forge.Task
		others task.Boundaries
	}{{first, theirs}, {second, mine}} {
		changed, problem, err := changedFiles(ctx, env, cfg, journals, side.task)
		if err != nil {
			return nil, "", err
		}
		if problem != "" {
			return nil, problem, nil
		}
		for _, file := range changed {
			if side.others.Covers(file) {
				common = append(common, file)
			}
		}
	}
	slices.Sort(common)
	return slices.Compact(common), "", nil
}

// changedFiles are the files the working copy of a task has changed against the default
// branch of the project, and the problem that stopped the question where there is one.
func changedFiles(ctx context.Context, env Env, cfg config.Config, journals Journals, of forge.Task) ([]string, string, error) {
	state, err := LoadState(journals.StatePath(of.Number))
	if err != nil {
		if os.IsNotExist(err) {
			// The task has not run on this machine: it has no working copy and has
			// changed nothing here, and there is nothing to compare it with.
			return nil, "", nil
		}
		return nil, "", err
	}
	if state.Worktree == "" {
		return nil, "", nil
	}
	if _, err := os.Stat(state.Worktree); err != nil {
		return nil, fmt.Sprintf("the working copy %s of the task #%d is named by its state and is not there: "+
			"what it changed, crewflow cannot tell", state.Worktree, of.Number), nil
	}
	stdout, stderr, code, err := env.Command(ctx, "git",
		[]string{"diff", "--name-only", "origin/" + cfg.Project.DefaultBranch + "...HEAD"}, state.Worktree, nil)
	if err != nil {
		return nil, fmt.Sprintf("git could not tell what the task #%d has changed: %v", of.Number, err), nil
	}
	if code != 0 {
		return nil, fmt.Sprintf("git could not tell what the task #%d has changed: %s",
			of.Number, firstLine(stderr)), nil
	}
	var changed []string
	for line := range bytes.Lines(stdout) {
		if file := strings.TrimSpace(string(line)); file != "" {
			changed = append(changed, file)
		}
	}
	slices.Sort(changed)
	return changed, "", nil
}

// snapshotOf is what the decision of a pair is taken against: the two of the tasks as the
// tracker holds them, the branch each of them works in, the set of the runs going on in
// the project, and a digest of the files both of them may change with what each of them
// holds. `task admit` writes it into the record, and `task run` builds it again and asks
// whether the two are one thing (docs.DESIGN.md §7c).
//
// Git is asked only what is on this machine — the index of the repository and the working
// copies of the tasks — so that a record is written and read without the network, and a
// project whose route is not there is a project whose record of a pair is still a record.
func snapshotOf(ctx context.Context, env Env, cfg config.Config, journals Journals, repoDir string,
	alive Liveness, first, second forge.Task) (task.ValidWhile, error) {
	language := cfg.Project.Language
	world := task.ValidWhile{
		Specs:      map[string]string{},
		Boundaries: map[string][]string{},
		Branches:   map[string]string{},
	}
	for _, one := range []forge.Task{first, second} {
		key := keyOf(one)
		world.Specs[key] = task.DigestOf(one.Body)
		world.Boundaries[key] = task.Read(one.Body, language).Boundaries()
		world.Branches[key] = Branch(one)
	}
	going, err := goingTasks(journals.stateFolder(), alive)
	if err != nil {
		return task.ValidWhile{}, err
	}
	world.Active = going
	shared, err := sharedDigest(ctx, env, repoDir, world.Boundaries[keyOf(first)], world.Boundaries[keyOf(second)])
	if err != nil {
		return task.ValidWhile{}, err
	}
	world.Shared = shared
	return world, nil
}

// keyOf is the number of a task as the maps of a record are keyed by it: a JSON object has
// string keys, and a pair is about tasks and not about strings.
func keyOf(one forge.Task) string { return strconv.Itoa(one.Number) }

// sharedDigest is a digest of the files both of the two tasks may change, with what each
// of them holds in the index of the repository: the common resources of a pair — the
// documents of the project, the sections of DESIGN among them and the contracts of the
// code of it. It is what says that the sections and the contracts a pair was admitted on
// are the ones there are now, and a change in any of them makes the record out of date
// (D-055, docs/DESIGN.md §7c).
//
// The index is read and not the files themselves, because git has already digested them:
// a record of a pair stays one line long however many files the two tasks have in common.
func sharedDigest(ctx context.Context, env Env, repoDir string, one, other task.Boundaries) (string, error) {
	stdout, stderr, code, err := env.Command(ctx, "git", []string{"ls-files", "-s"}, repoDir, nil)
	if err != nil {
		return "", fmt.Errorf("ask git for the files of the repository in %s: %w", repoDir, err)
	}
	if code != 0 {
		return "", fmt.Errorf("git ls-files -s: exited with %d: %s", code, firstLine(stderr))
	}
	var common []string
	for line := range bytes.Lines(stdout) {
		entry := strings.TrimRight(string(line), "\r\n")
		_, path, cut := strings.Cut(entry, "\t")
		if !cut {
			continue
		}
		if one.Covers(path) && other.Covers(path) {
			common = append(common, entry)
		}
	}
	slices.Sort(common)
	return task.DigestOf(strings.Join(common, "\n")), nil
}

// goingTasks are the tasks of the project whose run is going now, as the state of them
// says, in one order.
//
// The state of a task says `running` until something says otherwise, and what says
// otherwise is the process the run wrote down: a run crewflow was killed in the middle of
// leaves the state of its task saying `running` with nobody behind it, and a person who
// comes back to it has to see that the project is free again (docs/DESIGN.md §7). A
// machine that cannot be asked is a machine whose answer is the one the state gives: a
// second run of a project that begins while the first is going is the failure the admission
// of a pair is here to prevent, and a machine that cannot tell must not let it through
// (F-135, F-146, §7c).
//
// One state crewflow cannot read does not decide for the others: what a person is to act
// upon is what could be read, and the file that could not be read is one he has to open by
// hand.
func goingTasks(folder string, alive Liveness) ([]int, error) {
	names, err := os.ReadDir(folder)
	if err != nil {
		if os.IsNotExist(err) {
			// A project crewflow has run nothing in has no state at all, and nothing of it
			// is going on.
			return nil, nil
		}
		return nil, fmt.Errorf("read the states of the tasks in %s: %w", folder, err)
	}
	var going []int
	for _, name := range names {
		if name.IsDir() || !isStateOfATask(name.Name()) {
			continue
		}
		state, err := LoadState(filepath.Join(folder, name.Name()))
		if err != nil || len(state.Attempts) == 0 {
			continue
		}
		last := state.Attempts[len(state.Attempts)-1]
		if last.Outcome != Running {
			continue
		}
		if process, named := last.Process(); named && alive != nil && !alive(process) {
			continue
		}
		going = append(going, state.Number)
	}
	slices.Sort(going)
	return going, nil
}

// AdmissionPath is the file the record of a pair is kept in, beside the states of the
// tasks of the project and under the root crewflow keeps of its own (docs/DESIGN.md §7).
func (j Journals) AdmissionPath(pair task.Pair) string {
	return filepath.Join(j.stateFolder(),
		fmt.Sprintf("admission-%d-%d.json", pair.First, pair.Second))
}

// LoadAdmission reads the record of a pair. A pair with no record is not a machine that
// could not answer: it is a pair nobody admitted, and the caller decides what that means
// for the run it was about to start (§7c). A record of a version crewflow does not know
// is refused rather than read as something it is not, like the state of a task (§7h).
func LoadAdmission(path string) (task.Admission, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return task.Admission{}, err
	}
	var record task.Admission
	if err := json.Unmarshal(data, &record); err != nil {
		return task.Admission{}, fmt.Errorf("the admission of a pair in %s is not readable: %w", path, err)
	}
	if record.Version > task.AdmissionVersion {
		return task.Admission{}, fmt.Errorf("the admission of a pair in %s is of the format %d, and crewflow knows %d: "+
			"a newer crewflow wrote it, and this one cannot read what it does not know",
			path, record.Version, task.AdmissionVersion)
	}
	return record, nil
}

// SaveAdmission writes the record of a pair, whole or not at all: a file cut in half by an
// interruption is a file that says the wrong thing about a pair, and the run that reads it
// afterwards would take it for what it is not (§7c, §7h).
func SaveAdmission(path string, record task.Admission) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("the admission of the pair %s: %w", record.Tasks, err)
	}
	return writeFileAtomic(path, append(data, '\n'))
}

// admit is what stands between the second run of a project and a pair nobody wrote down:
// while another run of the project is going, this run may not start without a record of
// the admission of the pair, and the record has to be about this pair in this state of the
// world. The rule was in DESIGN §7c and in the template of the orchestrator, and twice in
// a day it did not hold the action (F-135, F-146); here it is the code and not the memory
// of anybody.
//
// It is applied before the worktree of the task is made and before the key of the App is
// asked for, so that a refusal of it leaves nothing behind and opens no window of the
// system (docs.DESIGN.md §7i).
func (r *runner) admit(ctx context.Context) error {
	beside, err := r.goingBeside()
	if err != nil {
		return err
	}
	switch {
	case len(beside) == 0:
		// The first run of the project is the first run of the project: there is no pair,
		// and a record is written when and if there is one.
		return nil
	case len(beside) > 1:
		return &ErrAdmission{
			Task:   r.task.Number,
			Beside: beside[0],
			Word:   AdmissionRequired,
			Detail: fmt.Sprintf("the runs of the tasks %s are going, and a record of one pair admits no third run: "+
				"не больше двух задач одного проекта идут рядом (D-051)", numbersOf(beside)),
		}
	}
	return r.admittedTo(ctx, beside[0])
}

// goingBeside are the runs of this project that are going now, apart from the run of this
// task: a task does not run beside itself, and the state of the task that is about to
// start has not been written yet.
func (r *runner) goingBeside() ([]int, error) {
	going, err := goingTasks(r.journals.stateFolder(), r.env.Alive)
	if err != nil {
		return nil, err
	}
	var beside []int
	for _, number := range going {
		if number != r.task.Number {
			beside = append(beside, number)
		}
	}
	return beside, nil
}

// admittedTo is the record of the pair of this task and that one, checked in the state of
// the world as it stands: no record, a record about a world that has gone on, a criterion
// that was not worked out and a criterion that failed all refuse the run, and only eight
// `pass` and a decision of `allowed` let it go (docs.DESIGN.md §7c).
//
// A record of `owner-exception` is a decision of the owner of the project and the run goes
// on with it: it is said in the journal of the attempt and in the report, and it is not a
// pair of two clean tasks however clean the criteria of the record look (D-056).
func (r *runner) admittedTo(ctx context.Context, other int) error {
	pair := task.NewPair(r.task.Number, other)
	record, found, err := admissionOf(r.journals, pair)
	switch {
	case err != nil:
		return &ErrAdmission{Task: r.task.Number, Beside: other, Word: AdmissionRequired,
			Detail: fmt.Sprintf("the record of the pair is not one crewflow can read: %v", err)}
	case !found:
		return &ErrAdmission{Task: r.task.Number, Beside: other, Word: AdmissionRequired,
			Detail: fmt.Sprintf("the pair has no record of its admission: write it before the run with `%s` "+
				"(docs/DESIGN.md §7c)", pair.Command())}
	}
	if err := record.Checked(); err != nil {
		return &ErrAdmission{Task: r.task.Number, Beside: other, Word: AdmissionRequired,
			Detail: err.Error()}
	}
	their, err := r.set.Tracker.Task(ctx, other)
	if err != nil {
		return &ErrAdmission{Task: r.task.Number, Beside: other, Word: AdmissionUnknown,
			Detail: fmt.Sprintf("crewflow could not read the task #%d of the pair, and a pair it cannot read "+
				"is a pair it cannot admit: %v", other, err)}
	}
	now, err := snapshotOf(ctx, r.env, r.cfg, r.journals, r.repoDir(), r.env.Alive, r.task, their)
	if err != nil {
		return &ErrAdmission{Task: r.task.Number, Beside: other, Word: AdmissionUnknown,
			Detail: fmt.Sprintf("crewflow could not work out what the record of the pair was taken against: %v", err)}
	}
	if moved := task.Stale(record.ValidWhile, now); len(moved) > 0 {
		return &ErrAdmission{Task: r.task.Number, Beside: other, Word: AdmissionRequired,
			Detail: fmt.Sprintf("the record of the pair is out of date — %s; write it again with `%s` "+
				"(docs/DESIGN.md §7c)", strings.Join(moved, ", "), pair.Command())}
	}
	// The decision of the owner stands above the eight criteria: he took this pair into this
	// experiment whatever they say, and the run goes on with it, saying so. A record of
	// `allowed` is the only other way through, and the eight `pass` of it have just been
	// read.
	if record.Decision != task.DecisionOwnerException {
		if name, one, stops := record.Criteria.Stopping(); stops {
			word := AdmissionUnknown
			if one.Result == task.ResultFail {
				word = AdmissionDenied
			}
			return &ErrAdmission{Task: r.task.Number, Beside: other, Word: word,
				Criterion: name, Resource: one.Resource, Detail: one.Reason}
		}
	}
	r.admission = &record
	return nil
}

// admissionOf is the record of a pair out of the state of the project, and whether there
// is one. A pair with no record is a pair nobody admitted and not a machine that could
// not answer, and the caller refuses the run for that (docs.DESIGN.md §7c).
func admissionOf(journals Journals, pair task.Pair) (task.Admission, bool, error) {
	path := journals.AdmissionPath(pair)
	record, err := LoadAdmission(path)
	switch {
	case err == nil:
		return record, true, nil
	case errors.Is(err, os.ErrNotExist):
		return task.Admission{}, false, nil
	default:
		return task.Admission{}, false, err
	}
}

// numbersOf are the tasks of a project as a refusal names them.
func numbersOf(numbers []int) string {
	named := make([]string, 0, len(numbers))
	for _, number := range numbers {
		named = append(named, "#"+strconv.Itoa(number))
	}
	return strings.Join(named, ", ")
}

// markOf is the line the journal of an attempt holds where a run went beside another one of
// the project: the pair, the decision and what the owner said where he made the decision
// himself. A person reading the journal of the run afterwards has to see that the pair was
// admitted and by whom, and not only that it started (docs.DESIGN.md §7c, §7i).
func markOf(record task.Admission) string {
	said := fmt.Sprintf("crewflow: the run of the task goes beside the pair %s on the admission %s",
		record.Tasks, record.Decision)
	if record.Owner != nil && record.Owner.Why != "" {
		said += " of the owner: " + record.Owner.Why
	}
	return said + " (docs/DESIGN.md §7c)"
}
