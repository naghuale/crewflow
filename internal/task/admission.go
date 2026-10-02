package task

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// The admission of a pair of tasks to run beside one another is a record and not a
// verdict worked out on the spot: `crewflow task admit <A> <B>` writes it before the
// second run starts, and `crewflow task run` applies it without asking anybody. The rule
// itself is written in docs/DESIGN.md §7c and in the template of the orchestrator, and
// twice in a day it did not hold the action (F-135, F-146) — the code holds it now, and
// the record is what it holds it by.
//
// The criteria are K1…K8 of MODEL, IV-031, and the words of their results are three:
// `pass`, `fail` and `unknown`. A criterion nobody checked is `unknown` and not `pass`
// (C6, IV-031): непроверенный критерий допуском не считается, и пара, у которой половина
// критериев не проверена, не идёт.

const (
	// ResultPass says that the criterion was checked and the two tasks do not have it in
	// common.
	ResultPass = "pass"
	// ResultFail says that the criterion was checked and the two tasks do have it in
	// common. A record holds what they have in common, so that a refusal can name the
	// resource and not only the criterion.
	ResultFail = "fail"
	// ResultUnknown says that the criterion could not be worked out: a machine has not
	// been asked, or a person has not said. It is the safe side of a mistake, because
	// `task run` refuses a pair with one (docs/DESIGN.md §7c).
	ResultUnknown = "unknown"
)

// Results is every result a criterion may hold, in the order a report reads them: the
// one that admits the pair, the one that forbids it, and the one that says nothing was
// worked out.
var Results = []string{ResultPass, ResultFail, ResultUnknown}

// The decisions of a pair, which are what the whole record is for. `allowed` is the only
// one that lets a second run start on its own; `owner-exception` lets it start and says
// in the journal and in the report that the owner decided it, and is never counted as a
// pair of two clean tasks — the criteria of an experiment are not evidence about the
// criteria of the next pair (F-135, D-056).
const (
	// DecisionAllowed says that all eight criteria are a `pass`: пара допустима.
	DecisionAllowed = "allowed"
	// DecisionDenied says that the pair is not admitted by this record. Which criterion
	// stopped it, and what the two tasks have in common, the record says in the criteria.
	DecisionDenied = "denied"
	// DecisionOwnerException says that the owner of the project took this pair into this
	// experiment on his own decision, whatever the criteria say.
	DecisionOwnerException = "owner-exception"
)

// Decisions is every decision a record may hold, in one order.
var Decisions = []string{DecisionAllowed, DecisionDenied, DecisionOwnerException}

// AdmissionVersion is the version of the format of a record of a pair. A record of a
// version crewflow does not know is refused and not read as something it is not, like the
// state of a task (§7h): a record whose shape nobody has looked at is a record crewflow
// cannot say anything true about.
const AdmissionVersion = 1

// The criteria of the independence of a pair in the order of MODEL, IV-031, with what
// each of them is about in the words a report of crewflow reads in. The order is the
// order a person reads the eight lines in, and it is the order they are written to a
// report and checked in.
var criteriaOfAPair = []struct{ name, about string }{
	{"K1", "write paths"},
	{"K2", "documents"},
	{"K3", "configuration"},
	{"K4", "model"},
	{"K5", "release"},
	{"K6", "test data"},
	{"K7", "contracts"},
	{"K8", "CI"},
}

// CriteriaNames is every criterion of a pair, in the order of MODEL, IV-031. The list is
// closed: a program switches on these words and nothing else, and a name outside of them
// is not a criterion (docs/DESIGN.md §7e).
func CriteriaNames() []string {
	names := make([]string, 0, len(criteriaOfAPair))
	for _, criterion := range criteriaOfAPair {
		names = append(names, criterion.name)
	}
	return names
}

// Says is what a criterion is about, as a report of crewflow reads it, and the name of
// the criterion itself when the name is not one of the eight.
func Says(name string) string {
	for _, criterion := range criteriaOfAPair {
		if criterion.name == name {
			return criterion.about
		}
	}
	return name
}

// Admission is the record of the decision that two tasks of the project may run beside
// one another: the pair itself, what each of the eight criteria of MODEL, IV-031 says,
// the decision, and the facts of the world the decision was taken against. It is written
// by `crewflow task admit` and read by `crewflow task run`; nothing of it is a truth
// about the host, and it goes out of date the moment the tasks, their branches, the set of
// the runs going on or the files both of them may change (docs/DESIGN.md §7c).
type Admission struct {
	// Version is the version of the format of the record.
	Version int `json:"version"`
	// RepositoryID is the stable number the host keeps the repository under, and is null
	// until #120 fills it: a record of a pair is about a pair of a project, and which
	// project it is has to be answerable without the name of a folder (§6a, §7g).
	RepositoryID *string `json:"repository_id"`
	// Tasks is the pair, in one order whatever order it was named in.
	Tasks Pair `json:"tasks"`
	// SnapshotID is the digest of the whole record, and it is what says that this is the
	// record `crewflow task admit` wrote: a record edited by hand afterwards has a digest
	// that is not of its own contents, and is refused rather than obeyed ([Admission.ID]).
	SnapshotID string `json:"snapshot_id"`
	// Criteria are the eight of MODEL, IV-031 as they were worked out or entered.
	Criteria Criteria `json:"criteria"`
	// Decision is what the criteria add up to, and it is in them and not beside them: a
	// decision of `allowed` while a criterion is not a `pass` is a record that says one
	// thing and is another, and it is refused.
	Decision string `json:"decision"`
	// CreatedAt is when the record was written, and it is a fact of the record and not a
	// term: a record does not grow stale with time, it goes stale with what it was taken
	// against (`ValidWhile`).
	CreatedAt time.Time `json:"created_at"`
	// ValidWhile is what the decision was taken against, and what has to be as it was for
	// the record to hold.
	ValidWhile ValidWhile `json:"valid_while"`
	// Owner is the decision of the owner where he took the pair into an experiment
	// against a criterion, and why he gave it. It is here and not in the decision word
	// alone, because a reason a person gave has to be readable a month later.
	Owner *OwnerException `json:"owner_exception,omitempty"`
}

// Pair is the two tasks of an admission, in one order whatever order they were named in:
// the record of a pair is one file, and «#182 и #145» and «#145 и #182» are one pair and
// not two.
type Pair struct {
	// First is the lower of the two numbers, and Second the higher one.
	First  int `json:"first"`
	Second int `json:"second"`
}

// NewPair is the pair of two tasks in one order, so that a record of it is one file and
// a refusal of it names it the same way whoever names it.
func NewPair(one, other int) Pair {
	if one > other {
		one, other = other, one
	}
	return Pair{First: one, Second: other}
}

// Beside is whether the pair is of that task, whichever of the two it is: a run of a task
// beside itself is not a pair, and no record of one is written for it.
func (p Pair) Beside(number int) bool {
	return p.First == number || p.Second == number
}

// TheOther is the task of the pair that is not that one.
func (p Pair) TheOther(number int) int {
	if p.First == number {
		return p.Second
	}
	return p.First
}

// Command is the command that writes the record of the pair, as a refusal of a run names
// it: a person told that a pair is not admitted is told what to run to admit it.
func (p Pair) Command() string {
	return fmt.Sprintf("crewflow task admit %d %d", p.First, p.Second)
}

// String is the pair as a line of a report reads it.
func (p Pair) String() string {
	return fmt.Sprintf("#%d, #%d", p.First, p.Second)
}

// Criterion is one criterion of the pair as a record holds it: the result and the reason
// a person entered it with. The reason is required of everything that is not a `pass`,
// because a criterion without one is a word a person has to guess about, and a refusal
// nobody can read is a refusal they will ask about again (§7e, §7f).
type Criterion struct {
	// Result is one of the three words of [Results].
	Result string `json:"result"`
	// Reason is what a person wrote for the result, and what a refusal shows beside it.
	Reason string `json:"reason,omitempty"`
	// Resource is what the two tasks have in common where the criterion failed — the file
	// both of them write, the contract both of them change — so that a refusal of the pair
	// names the thing and not only the criterion (§7c).
	Resource string `json:"resource,omitempty"`
}

// WritePaths is K1 — the common write paths of the pair — which is the one criterion
// crewflow works out itself, out of the paths the two tasks declare and out of the files
// their working copies have really changed. A criterion a person enters is a criterion
// nobody checked, and the paths of the tasks are the one thing a machine can check
// without asking anybody (docs/DESIGN.md §7c, F-135).
type WritePaths struct {
	Criterion
	// DeclaredPatternsOverlap are the pairs of globs of the two boundaries that can both
	// be true of one file, one entry per pair.
	DeclaredPatternsOverlap []string `json:"declared_patterns_overlap,omitempty"`
	// ConcreteFilesOverlap are the files the working copies of the two tasks have really
	// changed and the other of the two may change as well: the common write path as it
	// stands, and not as it is declared.
	ConcreteFilesOverlap []string `json:"concrete_files_overlap,omitempty"`
}

// Criteria are the eight of MODEL, IV-031 as a record holds them, one field per
// criterion and no other place a criterion may be: a program reads the eight of them by
// their names, and a ninth criterion is a criterion nobody wrote a test against (§7e).
type Criteria struct {
	// K1 is worked out by crewflow and entered by nobody.
	K1 WritePaths `json:"K1"`
	// K2 is about the documents of the project, and it is checked by the sections of
	// DESIGN and not by the file (D-055, 02.10).
	K2 Criterion `json:"K2"`
	// K3 is about the configuration of the project: `crewflow.toml` and the registry of
	// its settings.
	K3 Criterion `json:"K3"`
	// K4 is about the model: одна модель или документ не меняют.
	K4 Criterion `json:"K4"`
	// K5 is about the release: теги и миграции, always exclusive.
	K5 Criterion `json:"K5"`
	// K6 is about the test data of the project: `testdata/…`.
	K6 Criterion `json:"K6"`
	// K7 is about the contracts of the project — a verifiable one both of them change,
	// «состояние внимания» among them.
	K7 Criterion `json:"K7"`
	// K8 is about the configuration of CI: одна конфигурация CI не меняют.
	K8 Criterion `json:"K8"`
}

// One is the criterion with that name, and whether the pair has one by that name.
func (c Criteria) One(name string) (Criterion, bool) {
	switch name {
	case "K1":
		return c.K1.Criterion, true
	case "K2":
		return c.K2, true
	case "K3":
		return c.K3, true
	case "K4":
		return c.K4, true
	case "K5":
		return c.K5, true
	case "K6":
		return c.K6, true
	case "K7":
		return c.K7, true
	case "K8":
		return c.K8, true
	}
	return Criterion{}, false
}

// Set is the criteria of the pair with that one criterion in it, and whether the pair has
// a criterion by that name at all: the eight are a closed list, and a name outside of it
// is not a criterion a record may hold (docs.DESIGN.md §7e).
func (c *Criteria) Set(name string, one Criterion) bool {
	switch name {
	case "K1":
		c.K1.Criterion = one
	case "K2":
		c.K2 = one
	case "K3":
		c.K3 = one
	case "K4":
		c.K4 = one
	case "K5":
		c.K5 = one
	case "K6":
		c.K6 = one
	case "K7":
		c.K7 = one
	case "K8":
		c.K8 = one
	default:
		return false
	}
	return true
}

// Admits is whether the record lets a run of one of the two tasks start beside the run of
// the other: either all eight criteria are a `pass` and the decision is `allowed`, or the
// decision is the one the owner of the project took on his own for this experiment. Every
// other decision is a pair that goes by one (docs/DESIGN.md §7c).
func (a Admission) Admits() bool {
	return a.Decision == DecisionAllowed || a.Decision == DecisionOwnerException
}

// Verified is whether every criterion of the pair is a `pass`: пара допустима, только
// когда все восемь `PASS`, и это единственное условие, при котором решение —
// `allowed` (MODEL, IV-031).
func (c Criteria) Verified() bool {
	for _, name := range CriteriaNames() {
		one, _ := c.One(name)
		if one.Result != ResultPass {
			return false
		}
	}
	return true
}

// Stopping is the criterion that stops the pair and whether anything stops it: a `fail`
// before a `unknown`, because a criterion that says the two tasks have something in
// common is a fact about the pair, and a criterion that says nothing was worked out is
// not (docs/DESIGN.md §7c).
func (c Criteria) Stopping() (string, Criterion, bool) {
	for _, result := range []string{ResultFail, ResultUnknown} {
		for _, name := range CriteriaNames() {
			one, _ := c.One(name)
			if one.Result == result {
				return name, one, true
			}
		}
	}
	return "", Criterion{}, false
}

// ValidWhile is what the decision of a pair was taken against: the facts that have to be
// as they were for the record to hold. A record whose world has gone on without it is not
// a record about this pair any more, and `task run` asks for a new one rather than obey an
// old one (F-135, F-146, docs/DESIGN.md §7c).
type ValidWhile struct {
	// Specs is the digest of the specification of each of the two tasks as it was read,
	// by the number of the task. The head of a task is its body: a task whose text
	// changed is a different task for the question of the paths it declares.
	Specs map[string]string `json:"specs"`
	// Boundaries is what each of the two tasks declared it may change, by the number of
	// the task. A pair whose declarations changed is a pair nobody looked at.
	Boundaries map[string][]string `json:"boundaries"`
	// Branches is the branch each of the two tasks works in, by the number of the task:
	// the work of a pair goes on in two branches, and a branch that is not the one of the
	// task is work of somebody else.
	Branches map[string]string `json:"branches"`
	// Active is the set of the tasks of the project that were going on when the record was
	// written. A third run starting is a pair of its own, and a record of two does not
	// admit it.
	Active []int `json:"active"`
	// Shared is a digest of the files both of the two tasks may change, with what each of
	// them held: the common resources of the pair — the documents of the project, the
	// sections of DESIGN and the contracts of the code of it among them. It is what says
	// that the sections and the contracts a pair was admitted on are the ones there are
	// now (D-055, docs/DESIGN.md §7c).
	Shared string `json:"shared"`
}

// OwnerException is the decision of the owner of the project to let a pair run beside
// another one whatever the criteria of it say, and why he gave it: записанное решение
// владельца на текущий эксперимент. It is kept beside the criteria and not instead of
// them, so that the record still says what was checked and what it came out as.
type OwnerException struct {
	// Why is what the owner said, in his words.
	Why string `json:"why"`
	// At is when he said it, which is the moment the experiment was decided on and the
	// moment the pair stopped being a pair of two clean tasks.
	At time.Time `json:"at"`
}

// ID is the digest of the record: the pair, the eight criteria, the decision, what the
// decision was taken against and when it was made, with the digest itself left out of it.
// A record whose digest is not of its own contents is a record a hand has been in, and a
// hand may write a `pass` where there was a `fail` — so the digest is what says that this
// is the record `crewflow task admit` wrote and not one somebody edited afterwards
// (docs.DESIGN.md §7c, §7h).
//
// The maps are marshalled with their keys in order, so two records of one pair differ only
// where the pair differs and never where the world was read in a different order.
func (a Admission) ID() (string, error) {
	a.SnapshotID = ""
	data, err := json.Marshal(a)
	if err != nil {
		return "", fmt.Errorf("the admission of the pair %s: %w", a.Tasks, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:16], nil
}

// DigestOf is the digest of anything, as the facts of a record are held: the specification
// of a task, the files both of a pair may change, and nothing else crewflow writes down as
// a digest of its own.
func DigestOf(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])[:16]
}

// Set is the record of a pair with the digest of itself in it, which is what makes it a
// record crewflow wrote.
func (a Admission) Set() (Admission, error) {
	id, err := a.ID()
	if err != nil {
		return Admission{}, err
	}
	a.SnapshotID = id
	return a, nil
}

// Checked is whether the record is one crewflow may act on: the version of the format,
// the pair, the decision, and every one of the eight criteria with a result that is a
// word of the format and a reason where the result is not a `pass`. A record that says one
// thing and is another — a decision of `allowed` with a criterion that is not a `pass`, a
// digest that is not of its own contents — is refused, because a rule applied from a
// record nobody checked is a rule nobody wrote (docs/DESIGN.md §7h).
func (a Admission) Checked() error {
	switch {
	case a.Version > AdmissionVersion:
		return fmt.Errorf("the admission of the pair %s is of the format %d, and crewflow knows %d: "+
			"a newer crewflow wrote it, and this one cannot read what it does not know",
			a.Tasks, a.Version, AdmissionVersion)
	case a.Tasks.First < 1 || a.Tasks.Second < 1 || a.Tasks.First >= a.Tasks.Second:
		return fmt.Errorf("the admission names the pair %s, which is not a pair of two tasks: "+
			"a record of a pair names the two of them in one order", a.Tasks)
	case !slices.Contains(Decisions, a.Decision):
		return fmt.Errorf("the admission of the pair %s has the decision %q, which is not one of %s",
			a.Tasks, a.Decision, strings.Join(Decisions, ", "))
	}
	for _, name := range CriteriaNames() {
		one, _ := a.Criteria.One(name)
		if !slices.Contains(Results, one.Result) {
			return fmt.Errorf("the criterion %s of the pair %s is %q, which is not one of %s",
				name, a.Tasks, one.Result, strings.Join(Results, ", "))
		}
		if one.Result != ResultPass && strings.TrimSpace(one.Reason) == "" {
			return fmt.Errorf("the criterion %s of the pair %s is %q with no reason: "+
				"a criterion nobody may explain is a word a person has to guess about",
				name, a.Tasks, one.Result)
		}
	}
	if a.Decision == DecisionAllowed && !a.Criteria.Verified() {
		return fmt.Errorf("the admission of the pair %s allows it while one of the eight criteria is not a `pass`: "+
			"a record that says one thing and is another is not a record crewflow obeys", a.Tasks)
	}
	if a.Decision == DecisionOwnerException && (a.Owner == nil || strings.TrimSpace(a.Owner.Why) == "") {
		return fmt.Errorf("the admission of the pair %s is an exception of the owner with nothing in it: "+
			"the decision of a person is kept with what he said", a.Tasks)
	}
	id, err := a.ID()
	if err != nil {
		return err
	}
	if a.SnapshotID != id {
		return fmt.Errorf("the admission of the pair %s holds the digest %s and not the digest of its own contents %s: "+
			"the record was written by somebody and not by `crewflow task admit`", a.Tasks, a.SnapshotID, id)
	}
	return nil
}

// Stale is what the world says about a record it was not taken against, one phrase per
// fact that has gone on without it, in the words a refusal names them in. A record of a
// pair whose specification, boundaries, branch, set of the runs going on or common files
// have changed is a record about a pair that is not the one that is going to start, and
// applying it would be applying a decision of yesterday to today's work (F-135, §7c).
//
// An empty answer is what a record that still holds says, and a fact crewflow cannot
// work out counts as a fact that has changed: a record that cannot be checked is not a
// record that may be obeyed.
func Stale(was, now ValidWhile) []string {
	var moved []string
	for _, one := range sortedKeys(was.Specs, now.Specs) {
		switch {
		case was.Specs[one] == "":
			moved = append(moved, "the task #"+one+" is not in the record at all")
		case now.Specs[one] == "":
			moved = append(moved, "the task #"+one+" could not be read")
		case was.Specs[one] != now.Specs[one]:
			moved = append(moved, "the specification of the task #"+one+" has changed")
		}
	}
	for _, one := range sortedKeys(was.Boundaries, now.Boundaries) {
		if !slices.Equal(was.Boundaries[one], now.Boundaries[one]) {
			moved = append(moved, "what the task #"+one+" declares it may change has changed")
		}
	}
	for _, one := range sortedKeys(was.Branches, now.Branches) {
		if was.Branches[one] != now.Branches[one] {
			moved = append(moved, "the branch of the task #"+one+" has changed")
		}
	}
	if !slices.Equal(was.Active, now.Active) {
		moved = append(moved, "the set of the tasks going on in the project has changed")
	}
	if was.Shared != now.Shared {
		moved = append(moved, "the files both of the tasks may change have changed")
	}
	return moved
}

// sortedKeys is every key of two maps of the facts of a pair, in the order a person reads
// the tasks of a project in. A refusal that named them in the order of a map would name
// the same facts in a different order on every run.
func sortedKeys[V any](one, other map[string]V) []string {
	seen := map[string]bool{}
	var keys []string
	for key := range one {
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	for key := range other {
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys
}

// Overlap is what the declared boundaries of two tasks have in common, one entry per pair
// of globs that can both be true of one file, written as the two of them. It is what K1
// is worked out of first, and it is worked out of the globs and not of the files: a glob
// of a task names a whole set of paths, and whether two such sets meet is something a
// machine can say before a line of either task is written (docs/DESIGN.md §7c).
func Overlap(one, other Boundaries) []string {
	var common []string
	for _, mine := range one {
		for _, theirs := range other {
			if patternsMeet(mine, theirs) {
				common = append(common, mine+" and "+theirs)
			}
		}
	}
	return common
}

// patternsMeet is whether one glob of a boundary and another can both be true of one
// path. It is worked out segment by segment, because a glob of a task is a path written
// in the syntax of a shell where `**` crosses folders and `*` and `?` do not, and two
// paths meet exactly where their segments can be one and the same segment.
//
// A `**` in the middle stands for any number of folders, and one at the end for
// everything under what came before it. Two segments meet where they are the same
// segment, or where one of them is a pattern that matches the other, or — where both are
// patterns — where their fixed parts do not disagree: `*.go` and `*.md` do not meet, and
// `*.go` and `*` do, and a machine that cannot tell says that they may meet. The safe
// side of that mistake is a refusal of a pair that was never going to conflict, and it
// costs a person one record of eight lines; the other side is a pair let go that does
// conflict, and it is the failure this record is for (F-135).
func patternsMeet(one, other string) bool {
	return segmentsMeet(strings.Split(one, "/"), strings.Split(other, "/"))
}

func segmentsMeet(one, other []string) bool {
	switch {
	case len(one) == 0 && len(other) == 0:
		return true
	case len(one) == 0 || len(other) == 0:
		return false
	case one[0] == "**" && len(one) == 1:
		// Everything under what came before it, which is what a `**` at the end of a
		// boundary covers — and only once the folders before it are one and the same:
		// `docs/**` and `internal/**` meet in no file of the project at all.
		return true
	case other[0] == "**" && len(other) == 1:
		return true
	case one[0] == "**":
		// A `**` in the middle stands for any number of folders, and for none of them.
		return segmentsMeet(one[1:], other) || segmentsMeet(one, other[1:])
	case other[0] == "**":
		return segmentsMeet(one, other[1:]) || segmentsMeet(one[1:], other)
	}
	return segmentMeets(one[0], other[0]) && segmentsMeet(one[1:], other[1:])
}

// segmentMeets is whether one segment of a path and another can be one and the same
// name of a file or of a folder.
func segmentMeets(one, other string) bool {
	switch {
	case one == other:
		return true
	case !isPattern(one):
		return matchesSegment(other, one)
	case !isPattern(other):
		return matchesSegment(one, other)
	default:
		return bothPatternsMeet(one, other)
	}
}

// matchesSegment is whether a pattern of one segment of a boundary matches a name.
func matchesSegment(pattern, name string) bool {
	glob, err := regexp.Compile("^" + segmentPattern(pattern) + "$")
	if err != nil {
		return false
	}
	return glob.MatchString(name)
}

// bothPatternsMeet is whether two patterns of one segment may be one and the same name:
// where both of them hold a fixed part, these parts have to agree, and where one of them
// holds none it says nothing against the other.
func bothPatternsMeet(one, other string) bool {
	return fixedPartsAgree(beforeThePattern(one), beforeThePattern(other)) &&
		fixedPartsAgree(afterThePattern(one), afterThePattern(other))
}

// fixedPartsAgree is whether two fixed parts of a pattern may be parts of one name: a
// part of one of them that is empty says nothing, and two parts are one when the shorter
// of them stands at the edge of the longer.
func fixedPartsAgree(one, other string) bool {
	if one == "" || other == "" {
		return true
	}
	return strings.HasPrefix(other, one) || strings.HasPrefix(one, other)
}

// beforeThePattern is what a segment holds before its first star, and afterThePattern
// what stands after its last one: the two ends of it that every name it matches begins
// and ends with.
func beforeThePattern(segment string) string {
	before, _, _ := strings.Cut(segment, "*")
	before, _, _ = strings.Cut(before, "?")
	return before
}

func afterThePattern(segment string) string {
	last := ""
	for _, char := range segment {
		if char == '*' || char == '?' {
			last = ""
			continue
		}
		last += string(char)
	}
	return last
}

// isPattern is whether a segment of a boundary holds a star or a question mark, and so
// stands for names rather than for one name.
func isPattern(segment string) bool {
	return strings.ContainsAny(segment, "*?")
}
