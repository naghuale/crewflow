package merge

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/doctor"
	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/gate"
	taskrun "github.com/naghuale/crewflow/internal/run"
)

// TestMain points the home of the machine at a folder of the run of the tests, for
// every test of the package: git reads the settings of a person out of it, and a merge
// writes its journal and the state of the task into it — never into the home of the
// person who runs the tests (docs/DESIGN.md §7h).
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "crewflow-merge-home-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "merge tests: make a home of their own: %v\n", err)
		os.Exit(1)
	}
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME"} {
		if err := os.Setenv(key, home); err != nil {
			fmt.Fprintf(os.Stderr, "merge tests: point %s at %s: %v\n", key, home, err)
			os.Exit(1)
		}
	}
	code := m.Run()
	if err := os.RemoveAll(home); err != nil {
		fmt.Fprintf(os.Stderr, "merge tests: take %s away: %v\n", home, err)
	}
	os.Exit(code)
}

// The names the parts of a case of §7h are known under: a commit of the repository of
// the test is asked for by its name, so that both ends of a question about a history
// are the same commit in every case and not a moment of a clock.
const (
	baseCommit   = "base"
	firstCommit  = "first"
	secondCommit = "second"
	otherCommit  = "other"
)

// The task, the change and the people of a project of a test: the change is of the
// task, and the owner of the repository is the reviewer of it by the default of §5.
const (
	changeNumber = 7
	repositoryOf = "naghuale/crewflow"
	repoName     = "naghuale-crewflow"
	owner        = "naghuale"
	executor     = "crewflow-executor[bot]"
)

// theTask is the whole task the change of a test is of, with the boundaries the change
// is checked against: it is about the merge, and the merge is what it was to write.
const theTask = "## Why\n\nA merge is done by hand.\n\n## What changes\n\ncrewflow merges a change.\n\n" +
	"## How to check it yourself\n\n1. Merge a change.\n\n## Out of scope\n\nAcceptance by the owner.\n\n" +
	"## Risks and decisions\n\nA merge moves a branch.\n\n<details>\n<summary>Technical part</summary>\n\n" +
	"### Acceptance criteria\n\n- [ ] the merge fast-forwards main\n\n" +
	"### Boundaries\n\n```\ninternal/merge/**\nREADME.md\n```\n\n</details>\n"

// TestRunWalksTheTableOfTheSpecification walks every case of docs/DESIGN.md §7h on a
// real git: a temporary bare repository is the host of the project, a checkout of it is
// what a merge pushes from, and the API of the host is a struct. Nothing of it reaches
// the network or a repository of the person who runs the tests, and every case names
// the IT number of the specification it is.
func TestRunWalksTheTableOfTheSpecification(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not installed: %v", err)
	}
	for _, tc := range tableOfTheSpecification() {
		t.Run(tc.it+" "+tc.name, func(t *testing.T) {
			s := newScenario(t, tc.given)

			result, err := Run(t.Context(), s.deps(), changeNumber)
			if err != nil {
				t.Fatalf("Run returned an error: %v", err)
			}

			if tc.want != "" {
				s.assertRefused(t, result, tc)
				return
			}
			s.assertMerged(t, result, tc)
		})
	}
}

// TestMT001EveryReasonOfTheGateIsCoveredByACase is the promise 7 of docs/DESIGN.md §7h:
// every reason a merge can be refused with is a reason a case of the table names. A
// reason that is in the code and in no case is a refusal nobody has to obey, and a
// refusal nobody obeys is a word in a report.
//
// The gate judges most of the reasons out of the facts of a change and its own tests
// walk them there; what is walked here is the whole set of them as a merge meets it,
// the two a merge writes and the gate cannot give among them.
func TestMT001EveryReasonOfTheGateIsCoveredByACase(t *testing.T) {
	covered := map[gate.Reason]string{}
	for _, tc := range tableOfTheSpecification() {
		if tc.want == "" {
			continue
		}
		covered[tc.want] = tc.it
	}
	for _, reason := range gate.Reasons {
		if it, ok := covered[reason]; !ok {
			t.Errorf("the reason %q is in the table of §7h and no case of the specification reaches it", reason)
		} else {
			t.Logf("%s: %s", reason, it)
		}
	}
}

// TestMT002EveryCaseNamesAReasonOrAnOutcomeThatExists is the other half of the promise:
// a case may not name a reason or an outcome the code does not have, or the table would
// read as a promise of a refusal nothing can give.
func TestMT002EveryCaseNamesAReasonOrAnOutcomeThatExists(t *testing.T) {
	reasons := map[gate.Reason]bool{}
	for _, reason := range gate.Reasons {
		reasons[reason] = true
	}
	outcomes := map[Outcome]bool{
		Merged: true, AlreadyMerged: true, MergedWithCleanupWarning: true, Refused: true,
	}
	for _, tc := range tableOfTheSpecification() {
		switch {
		case tc.want != "" && !reasons[tc.want]:
			t.Errorf("the case %s names the reason %q, which is not a reason of §7h", tc.it, tc.want)
		case tc.want == "" && tc.outcome == "":
			t.Errorf("the case %s (%s) names neither a reason nor an outcome", tc.it, tc.name)
		case tc.want == "" && !outcomes[tc.outcome]:
			t.Errorf("the case %s names the outcome %q, which no merge comes to", tc.it, tc.outcome)
		case tc.want != "" && tc.outcome != "":
			t.Errorf("the case %s names the reason %q and the outcome %q, want one of the two", tc.it, tc.want, tc.outcome)
		}
	}
}

// TestMT003TheRegistryOfThePromisesMatchesTheDesign is the promise the registry exists
// for: the promises and the numbers of their cases live in the code as well, and the two
// copies have to be the same list, in both directions. A promise of §7h that is in no
// registry of the code is a promise nobody is keeping; a promise of the code that §7h
// does not know about is a test nobody agreed to; and a promise whose cases are not
// written yet is in the list of the promises crewflow waits for a task for, and only
// there (docs/DESIGN.md §7h).
func TestMT003TheRegistryOfThePromisesMatchesTheDesign(t *testing.T) {
	for _, differs := range comparePromises(promisesInTheDesign(t), registry(), waitingFor()) {
		t.Error(differs)
	}
}

// TestMT003FailsWhenAPromiseLeavesTheRegistry shows the rule of the meta-test and not
// only that it holds today: a promise that is gone from the registry of the code and is
// still written in §7h is a promise nobody is keeping, and the test has to say so
// (docs/DESIGN.md §7h).
func TestMT003FailsWhenAPromiseLeavesTheRegistry(t *testing.T) {
	withoutTheChecks := make([]promise, 0, len(registry()))
	for _, kept := range registry() {
		if kept.number == promiseOfChecks {
			continue
		}
		withoutTheChecks = append(withoutTheChecks, kept)
	}

	differs := comparePromises(promisesInTheDesign(t), withoutTheChecks, waitingFor())

	if len(differs) == 0 {
		t.Fatalf("MT-003 holds with the promise %d left out of the registry of the code: "+
			"§7h writes it and nothing keeps it", promiseOfChecks)
	}
	if !names(differs, promiseOfChecks) {
		t.Errorf("the rule holds %v, want it to name the promise %d it has lost", differs, promiseOfChecks)
	}
}

// TestMT003FailsWhenTheDesignHasAPromiseTheRegistryDoesNotHave is the other direction: a
// promise written into §7h with cases of its own and kept by nobody is the same mistake
// as the other way round, and a promise whose cases are not written yet is the one case
// where §7h may be ahead of the code (docs/DESIGN.md §7h).
func TestMT003FailsWhenTheDesignHasAPromiseTheRegistryDoesNotHave(t *testing.T) {
	inTheDesign := promisesInTheDesign(t)
	inTheDesign[promiseAheadOfTheCode] = []string{"IT-030"}

	differs := comparePromises(inTheDesign, registry(), waitingFor())

	if len(differs) == 0 {
		t.Fatalf("MT-003 holds with the promise %d written in §7h and in no registry of the code",
			promiseAheadOfTheCode)
	}
	if !names(differs, promiseAheadOfTheCode) {
		t.Errorf("the rule holds %v, want it to name the promise %d the design has and the code does not",
			differs, promiseAheadOfTheCode)
	}
}

// TestMT003FailsWhenAPromiseIsStillWaitedFor is what keeps the list of the promises
// crewflow waits for a task for from going stale: a promise whose cases are written and
// registered is kept, and a list that still waits for it says that crewflow is waiting
// for what it already has (docs/DESIGN.md §7h).
func TestMT003FailsWhenAPromiseIsStillWaitedFor(t *testing.T) {
	stale := append(waitingFor(), waitedFor{number: promiseOfMerge, task: taskOfThisTask})

	differs := comparePromises(promisesInTheDesign(t), registry(), stale)

	if len(differs) == 0 {
		t.Fatalf("MT-003 holds with the promise %d waited for and kept at the same time", promiseOfMerge)
	}
	if !names(differs, promiseOfMerge) {
		t.Errorf("the rule holds %v, want it to name the promise %d the list waits for and the registry keeps",
			differs, promiseOfMerge)
	}
}

// The promises of §7h that the tests above name by their number, so that a test does not
// carry a number the code may disagree with the design about.
const (
	// promiseOfChecks is the promise that a merge is impossible with the CI of the head
	// red, and promiseOfTheMerge the one that the merge happened and exactly that.
	promiseOfChecks = 3
	promiseOfMerge  = 6
	// promiseOfTheOwner is the acceptance of the owner before a merge and taskOfTheOwner
	// the task that writes it; taskOfThisTask is the task that keeps the promise of the
	// merge, and promiseAheadOfTheCode the number a test gives a promise that §7h has and
	// the code has not, so that no test writes a promise into the design.
	promiseOfTheOwner     = 5
	taskOfTheOwner        = 9
	taskOfThisTask        = 8
	promiseAheadOfTheCode = 9
)

// comparePromises is the whole rule of MT-003, and what it does not accept. Every promise
// of the design is in the registry of the code with the same cases of it, or is a promise
// crewflow waits for a task to write the cases of — the design states a promise before its
// cases are written, and the list of what is waited for says which task is writing them.
// Every promise of the registry is in the design, and a promise the registry keeps is not
// waited for any more: the task wrote its cases and the list must not go on saying that
// crewflow waits for what it already has.
//
// It takes the promises as they are given and answers what does not match, so that a
// test can show the rule on a promise that is not there: a rule that cannot be shown
// failing is a rule nobody has to obey (docs/DESIGN.md §7h).
func comparePromises(inTheDesign map[int][]string, kept []promise, awaited []waitedFor) []string {
	registered := make(map[int][]string, len(kept))
	for _, p := range kept {
		registered[p.number] = p.its
	}
	waitingForTask := make(map[int]int, len(awaited))
	for _, w := range awaited {
		waitingForTask[w.number] = w.task
	}

	var differs []string
	for _, number := range promiseNumbers(inTheDesign) {
		named := inTheDesign[number]
		inRegistry, isRegistered := registered[number]
		_, isWaitedFor := waitingForTask[number]
		switch {
		case isRegistered && !slices.Equal(inRegistry, named):
			differs = append(differs, fmt.Sprintf("promise %d holds the cases %v in §7h and %v in the registry of the code",
				number, named, inRegistry))
		case isRegistered:
		case isWaitedFor:
		default:
			differs = append(differs, fmt.Sprintf("promise %d of §7h is in no registry of the code, and no task is waited on for it",
				number))
		}
	}
	for _, p := range kept {
		if _, is := inTheDesign[p.number]; !is {
			differs = append(differs, fmt.Sprintf("promise %d is in the registry of the code and not in §7h", p.number))
		}
	}
	// A promise the registry keeps is not waited for any more: the task wrote its cases
	// and they are there, and a list that goes on naming the task would say that
	// crewflow waits for a promise it already keeps. A promise nobody wrote about is
	// not waited for either: there is nothing to wait for.
	for _, w := range awaited {
		cases, isKept := registered[w.number]
		switch {
		case isKept:
			differs = append(differs, fmt.Sprintf("promise %d is waited for the task #%d and its cases %v are in the registry of the code: it is kept, and nothing is waited for any more",
				w.number, w.task, cases))
		default:
			if _, is := inTheDesign[w.number]; !is {
				differs = append(differs, fmt.Sprintf("the code waits for the promise %d of the task #%d, and §7h has no such promise",
					w.number, w.task))
			}
		}
	}
	return differs
}

// promiseNumbers are the promises of the design in the order it writes them in: a map of
// them has no order of its own, and what the rule holds has to read the same twice.
func promiseNumbers(inTheDesign map[int][]string) []int {
	numbers := make([]int, 0, len(inTheDesign))
	for number := range inTheDesign {
		numbers = append(numbers, number)
	}
	slices.Sort(numbers)
	return numbers
}

// names is whether what the rule holds says anything of that promise: a rule that fails
// for another reason than the one a test is about proves nothing about it.
func names(differs []string, number int) bool {
	return slices.ContainsFunc(differs, func(differs string) bool {
		return strings.Contains(differs, fmt.Sprintf("promise %d ", number))
	})
}

// promise is one promise of docs/DESIGN.md §7h with the cases that keep it: the number
// the design gives it and the numbers of the cases it names under it. The registry of
// the promises is in the code next to the cases themselves, and MT-003 compares the two
// copies, so that neither of them can say a promise the other does not (docs/DESIGN.md §7h).
type promise struct {
	// number is the number of the promise in §7h, and its the numbers of the cases
	// under it, in the order the design writes them in.
	number int
	its    []string
}

// waitedFor is a promise of §7h whose cases are not written yet and the task that writes
// them: the promise stands in the design and the code names the task instead of the
// cases, and nothing else may stand for it (docs/DESIGN.md §7h).
type waitedFor struct {
	// number is the promise of §7h and task the number of the task that keeps it.
	number int
	task   int
}

// registry is the promises of §7h that the code keeps, with the cases of each. The
// promise of the environment of a run names the tests of it by their own names and is
// not here; a task that keeps a promise adds it with its cases and takes it out of the
// list of what is waited for.
func registry() []promise {
	return []promise{
		{number: 1, its: []string{"IT-001", "IT-002", "IT-005"}},
		{number: 2, its: []string{"IT-003", "IT-004", "IT-006"}},
		{number: promiseOfChecks, its: []string{"IT-007", "IT-008", "IT-009", "IT-010", "IT-011"}},
		{number: 4, its: []string{
			"IT-012", "IT-013", "IT-014", "IT-015", "IT-016", "IT-017", "IT-018",
		}},
		{number: promiseOfMerge, its: []string{"IT-019", "IT-020", "IT-021", "IT-022", "IT-023", "IT-030"}},
		{number: 7, its: []string{"MT-001", "MT-002", "MT-003"}},
	}
}

// waitingFor is the promises of §7h whose cases are not written yet, each with the task
// that writes them. The list is short and it is not a place to leave a promise nobody is
// working on: the rule of MT-003 fails while a promise here has its cases in the registry
// of the code (docs/DESIGN.md §7h).
func waitingFor() []waitedFor {
	return []waitedFor{
		{number: promiseOfTheOwner, task: taskOfTheOwner},
	}
}

// promisesInTheDesign is the numbers of the cases §7h names under every promise of the
// section of the verifiable promises: the promises are read out of it by their numbers
// and every `IT-nnn` and `MT-nnn` under one of them is its case. Nothing outside that
// section is read, so a promise of another part of the design is not a promise of this
// one (docs/DESIGN.md §7h).
func promisesInTheDesign(t *testing.T) map[int][]string {
	t.Helper()
	design, err := os.ReadFile(filepath.Join(rootOfTheWorktree(t), "docs", "DESIGN.md"))
	if err != nil {
		t.Fatalf("read the design: %v", err)
	}
	marked := strings.Index(string(design), promisesMark)
	if marked < 0 {
		t.Fatalf("the design has no section %q: the promises of §7h are read out of it", promisesMark)
	}
	till := strings.Index(string(design)[marked:], endsPromisesMark)
	if till < 0 {
		t.Fatalf("the design has no section after %q: where the promises end is where they end", promisesMark)
	}
	found := map[int][]string{}
	within := 0
	for raw := range strings.Lines(string(design)[marked : marked+till]) {
		line := strings.TrimSpace(raw)
		if number, is := numberOfPromise(line); is {
			within = number
			continue
		}
		if within == 0 {
			continue
		}
		for _, field := range strings.Fields(line) {
			if number, is := numberOfCase(field); is {
				found[within] = append(found[within], number)
			}
		}
	}
	return found
}

// rootOfTheWorktree is the folder the tests of a package are run under and the folder
// the project is in: the design is a file of the project and the tests are run in the
// folder of the package, so it is looked for by walking up until the folder that holds
// both the design and go.mod is found — no path of the test reaches outside the worktree
// and none of them is written down (docs/DESIGN.md §7h).
func rootOfTheWorktree(t *testing.T) string {
	t.Helper()
	started, err := os.Getwd()
	if err != nil {
		t.Fatalf("the folder the tests are run in: %v", err)
	}
	for folder := started; ; {
		_, design := os.Stat(filepath.Join(folder, "docs", "DESIGN.md"))
		_, module := os.Stat(filepath.Join(folder, "go.mod"))
		if design == nil && module == nil {
			return folder
		}
		parent := filepath.Dir(folder)
		if parent == folder {
			t.Fatalf("no folder with docs/DESIGN.md and go.mod above %s: the design of the project is where the module is", started)
		}
		folder = parent
	}
}

// The marks of the section of §7h that holds the promises and the section after it: a
// number of a promise is read out of the design between them and nowhere else.
const (
	promisesMark     = "### Проверяемые обещания системы"
	endsPromisesMark = "### Обещание безопасности среды"
)

// numberOfPromise is the number a promise of §7h is written under — "**3. …" — and
// whether the line is one: a line that begins with two stars and a number is a promise,
// and the rest of the design is not.
func numberOfPromise(line string) (int, bool) {
	rest, is := strings.CutPrefix(line, "**")
	if !is {
		return 0, false
	}
	digits, _, is := strings.Cut(rest, ".")
	if !is || digits == "" {
		return 0, false
	}
	number, err := strconv.Atoi(digits)
	return number, err == nil
}

// numberOfCase is the number of a case of the specification — `IT-019`, `MT-001` — and
// whether the word is one. A case in a list of the design is written with the comma of
// the list after it, and the reasons and the names of the tests stand beside the numbers
// in their own words; only the numbers are counted.
func numberOfCase(word string) (string, bool) {
	for _, mark := range []string{"IT-", "MT-"} {
		rest, is := strings.CutPrefix(word, mark)
		if !is {
			continue
		}
		number := strings.TrimRight(rest, ",.;·")
		if number != "" && strings.Trim(number, "0123456789") == "" {
			return mark + number, true
		}
	}
	return "", false
}

// A case of the table of §7h: a repository and a host of a test changed into the case,
// the one reason the merge is refused with — or the outcome a merge that is not refused
// comes to — and whether a push was called for at all.
type caseOfTable struct {
	it   string
	name string
	// given changes the case into itself: the facts of the host, the history of the
	// repository, or what happens between two steps of the merge.
	given func(*scenario)
	// want is the reason of the table of §7h the merge is refused with, and outcome
	// what a merge that was not refused comes to. Exactly one of them is set.
	want    gate.Reason
	outcome Outcome
	// pushes says that the merge is to call `git push` at all: a merge refused by the
	// gate pushes nothing, a merge refused by the host or by a branch that has moved
	// on is the one case where a push is made and refused, and a merge that found the
	// change in the branch already makes none.
	pushes bool
	// closedBy is who closed the task of the change behind the merge: the host, which
	// closes an issue on its own, or crewflow, which closes a task the host left open
	// in the time the merge waited for it (docs/DESIGN.md §7h).
	closedBy ClosedBy
}

// tableOfTheSpecification is every case of §7h as a merge meets it: a set of facts of a
// host and a repository of a test, and the one reason the merge is refused with or the
// outcome it comes to.
func tableOfTheSpecification() []caseOfTable {
	approvesHead := func(s *scenario) { s.approved(s.head()) }
	return []caseOfTable{
		{
			it:   "IT-001",
			name: "approved A, head = B, and A is an ancestor of B",
			given: func(s *scenario) {
				s.comment(gate.ApproveOf(s.sha(s.t.Context(), "change~1"), s.task.Number), owner)
			},
			want: gate.ApprovalStale,
		},
		{
			it:   "IT-002",
			name: "approved A, the history was rewritten, head = C, and A is not in it",
			given: func(s *scenario) {
				approved := s.sha(s.t.Context(), "change~1")
				rewritten := s.rewrite(s.t.Context())
				s.comment(gate.ApproveOf(approved, s.task.Number), owner)
				s.change.HeadSHA = rewritten
			},
			want: gate.HistoryRewritten,
		},
		{
			it:   "IT-003",
			name: "no record of a review at all",
			given: func(s *scenario) {
				s.comments = nil
			},
			want: gate.ApprovalMissing,
		},
		{
			it:   "IT-004",
			name: "REVIEW: APPROVED <head> written by the executor of the run",
			given: func(s *scenario) {
				s.comments = nil
				s.comment(gate.ApproveOf(s.head(), s.task.Number), executor)
			},
			want: gate.ApprovalUntrusted,
		},
		{
			it:   "IT-005",
			name: "the approval of the owner was edited after it was published",
			given: func(s *scenario) {
				s.comments = nil
				s.comment(gate.ApproveOf(s.head(), s.task.Number), owner, true)
			},
			want: gate.ApprovalEdited,
		},
		{
			it:   "IT-006",
			name: "approved the head, then changes requested: the last record is not an approval",
			given: func(s *scenario) {
				approvesHead(s)
				s.comment(gate.ChangesOf(s.head(), s.task.Number, "R1: the reason"), owner)
			},
			want: gate.ApprovalMissing,
		},
		{
			it:   "IT-007",
			name: "approved the head, and the check is green for another commit",
			given: func(s *scenario) {
				approvesHead(s)
				s.checks[0].SHA = s.sha(s.t.Context(), "change~1")
			},
			want: gate.CISHAMismatch,
		},
		{
			it:   "IT-008",
			name: "lint and test are required, and the head has only a green test",
			given: func(s *scenario) {
				approvesHead(s)
				s.required = append(s.required, forge.RequiredCheck{Name: "lint", App: "github-actions"})
			},
			want: gate.RequiredCheckMissing,
		},
		{
			it:   "IT-009",
			name: "the check has not finished",
			given: func(s *scenario) {
				approvesHead(s)
				s.checks[0].State = forge.CheckPending
			},
			want: gate.RequiredCheckIncomplete,
		},
		{
			it:   "IT-010",
			name: "the check has failed",
			given: func(s *scenario) {
				approvesHead(s)
				s.checks[0].State = forge.CheckFailure
			},
			want: gate.RequiredCheckFailed,
		},
		{
			it:   "IT-011",
			name: "the green mark was written through the API of statuses, not by Actions",
			given: func(s *scenario) {
				approvesHead(s)
				s.checks[0].App = ""
			},
			want: gate.RequiredCheckUntrusted,
		},
		{
			it:   "IT-012",
			name: "the default branch has moved on since the branch was cut",
			given: func(s *scenario) {
				approvesHead(s)
				s.moveMain(s.t.Context())
			},
			want: gate.NotFastForward,
		},
		{
			it:   "IT-013",
			name: "the default branch moved on between the check and the push",
			given: func(s *scenario) {
				approvesHead(s)
				s.beforePush = func() { s.moveMain(context.Background()) }
			},
			want:   gate.NotFastForward,
			pushes: true,
		},
		{
			it:   "IT-014",
			name: "a file outside the boundaries of the task is in the range base..head",
			given: func(s *scenario) {
				approvesHead(s)
				s.files = append(s.files, "docs/DESIGN.md")
			},
			want: gate.OutOfScope,
		},
		{
			it:   "IT-015",
			name: "the same, with SCOPE: ACCEPTED <head> written by the owner",
			given: func(s *scenario) {
				approvesHead(s)
				s.files = append(s.files, "docs/DESIGN.md")
				s.comment(gate.AcceptOf(s.head(), "the design of the merge is this task as well"), owner)
			},
			outcome: Merged,
			pushes:  true,
		},
		{
			it:   "IT-016",
			name: "the change request is closed",
			given: func(s *scenario) {
				approvesHead(s)
				s.change.State = "closed"
			},
			want: gate.PRNotOpen,
		},
		{
			it:   "IT-016",
			name: "the change request is a draft",
			given: func(s *scenario) {
				approvesHead(s)
				s.change.Draft = true
			},
			want: gate.PRDraft,
		},
		{
			it:   "IT-017",
			name: "the change request is meant for another branch",
			given: func(s *scenario) {
				approvesHead(s)
				s.change.BaseBranch = "release"
			},
			want: gate.WrongTargetBranch,
		},
		{
			it:   "IT-017",
			name: "the change request is of another repository",
			given: func(s *scenario) {
				approvesHead(s)
				s.change.Repository = "someone/crewflow"
			},
			want: gate.WrongRepository,
		},
		{
			it:   "IT-018",
			name: "the host did not give a certain answer",
			given: func(s *scenario) {
				approvesHead(s)
				s.changeErr = fmt.Errorf("the API of the host answered 502")
			},
			want: gate.ForgeUnavailable,
		},
		{
			it:       "IT-019",
			name:     "everything is in order",
			given:    approvesHead,
			outcome:  Merged,
			pushes:   true,
			closedBy: ClosedByHost,
		},
		{
			it:   "IT-020",
			name: "the change is in the default branch already: nothing to push",
			// The case of a merge that is asked for a second time is a repository where
			// the change is in the branch already, and the merge before it is how the
			// case comes about.
			given: func(s *scenario) {
				if _, err := Run(context.Background(), s.deps(), changeNumber); err != nil {
					s.t.Fatalf("the first merge returned an error: %v", err)
				}
				s.commands, s.pushes = nil, 0
			},
			outcome: AlreadyMerged,
		},
		{
			it:   "IT-021",
			name: "the merge went through, and the worktree of the task could not be taken away",
			given: func(s *scenario) {
				approvesHead(s)
				s.leaveWork(s.worktree)
			},
			outcome: MergedWithCleanupWarning,
			pushes:  true,
		},
		{
			it:   "IT-022",
			name: "the host refuses the push of the branch",
			given: func(s *scenario) {
				approvesHead(s)
				s.forbidPush(s.t.Context())
			},
			want:   gate.PushRejected,
			pushes: true,
		},
		{
			it:   "IT-023",
			name: "the push was accepted, and the branch of the host is somewhere else",
			given: func(s *scenario) {
				approvesHead(s)
				s.afterPush = func() { s.moveMain(context.Background()) }
			},
			want:   gate.VerifyMismatch,
			pushes: true,
		},
		{
			it:   "IT-030",
			name: "the host did not close the task in the time the merge waited for it",
			// A host that closed the task behind a change it merged does not do it
			// every time: it closed neither the task of #48 nor of #47 on 30.09, and a
			// check after such a merge answers «not verified» about a merge that did go
			// in. The merge waits the whole time it was given and closes the task itself
			// then, with a record under it.
			given: func(s *scenario) {
				approvesHead(s)
				s.closedAfter = -1
			},
			outcome:  Merged,
			pushes:   true,
			closedBy: ClosedByCrewflow,
		},
	}
}

// scenario is one case of the table as a merge meets it: a repository of the test with
// a change of a task in it, the host that says what the case says, and the git both are
// asked through — every command of it is written down, so that a case can ask whether
// a push was made at all.
type scenario struct {
	*repository
	*host
	task     forge.Task
	home     string
	state    string
	worktree string
	// commands is every command of git the merge ran, and pushes the ones that were a
	// push of the branch of the project.
	commands []string
	pushes   int
	// beforePush is what the host does between the check of the gate and the push, and
	// afterPush the same between the push and the question of where the branch is. They
	// are how a branch that moves on while a merge is going on is a case of its own
	// (docs/DESIGN.md §7h).
	beforePush func()
	afterPush  func()
	// waits is how often the merge waited between two questions of the host, which is
	// how a test waits for nothing and says how often it was asked.
	waits int
	now   time.Time
}

// newScenario is a project of a test with a change that is green, approved by the owner
// of the repository on its own head and inside the boundaries of its task — a case of
// the table is this and one thing more.
func newScenario(t *testing.T, given func(*scenario)) *scenario {
	t.Helper()
	repo := newRepository(t)
	s := &scenario{
		repository: repo,
		home:       t.TempDir(),
		worktree:   filepath.Join(t.TempDir(), "worktree"),
		now:        time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC),
		host: &host{
			task: forge.Task{
				Number: changeNumber,
				Title:  "the merge of a change",
				Body:   theTask,
				State:  "open",
			},
			stands: map[string][]forge.CheckState{},
		},
	}
	s.task = s.host.task
	s.change = forge.ChangeRequest{
		Number:     changeNumber,
		URL:        "https://github.com/naghuale/crewflow/pull/7",
		HeadBranch: "change",
		HeadSHA:    repo.sha(t.Context(), "change"),
		BaseBranch: "main",
		State:      "open",
		Body:       "Closes #7\n\n## What changed\n\ncrewflow merges a change.",
		Repository: repositoryOf,
	}
	s.files = []string{"internal/merge/merge.go"}
	s.checks = []forge.CheckRun{{
		Name: "test", State: forge.CheckSuccess, App: "github-actions", SHA: s.change.HeadSHA,
	}}
	s.required = []forge.RequiredCheck{{Name: "test", App: "github-actions"}}
	s.approved(s.change.HeadSHA)
	s.addWorktree(t)
	s.keepState(t)
	if given != nil {
		given(s)
	}
	return s
}

// head is the commit at the head of the change, which is the commit a merge of it is
// about.
func (s *scenario) head() string { return s.change.HeadSHA }

// addWorktree is the checkout of the task of a test: a worktree of the repository of
// the test, detached at the head of the change, which is what a merge takes away when
// it has taken the change in.
func (s *scenario) addWorktree(t *testing.T) {
	t.Helper()
	s.git(t, s.checkout, "worktree", "add", "--detach", "--quiet", s.worktree, s.sha(s.t.Context(), "change"))
}

// leaveWork is what an executor that was stopped mid-work leaves in the worktree of the
// task: a file it changed and did not commit. git refuses to take such a worktree away
// without being told to, and the merge does not tell it to: a person looks at the work
// first and takes the checkout away by hand (docs/DESIGN.md §7h).
func (s *scenario) leaveWork(worktree string) {
	path := filepath.Join(worktree, "internal", secondCommit)
	if err := os.WriteFile(path, []byte("the work of a run that was stopped\n"), 0o600); err != nil {
		s.t.Fatalf("leave work in the worktree of the task: %v", err)
	}
}

// keepState is what crewflow kept of the run of the task: the branch, the worktree and
// the change request the run opened. It is where a merge writes the commit it merged
// (docs/DESIGN.md §7).
func (s *scenario) keepState(t *testing.T) {
	t.Helper()
	s.state = taskrun.JournalsOf(s.home, repoName).StatePath(s.task.Number)
	state := taskrun.State{
		Number:   s.task.Number,
		Title:    s.task.Title,
		Branch:   "change",
		Worktree: s.worktree,
		Profile:  "opencode",
		Change:   &taskrun.Change{Number: changeNumber, URL: s.change.URL},
		Attempts: []taskrun.Attempt{{Number: 1, Outcome: taskrun.ChangeRequestOpened}},
	}
	if err := taskrun.SaveState(s.state, state); err != nil {
		t.Fatalf("write the state of the task: %v", err)
	}
}

// deps is the merge of this case: the host of the test as the roles of the project, the
// checkout of the test as the checkout it pushes from, the task, and the clock and the
// waiting of the test instead of the machine's.
func (s *scenario) deps() Deps {
	return Deps{
		Gate: gate.Deps{
			Forge:         s.host,
			CI:            s.host,
			Repository:    repositoryOf,
			DefaultBranch: "main",
			Reviewers:     []string{owner},
			Task:          s.task.Number,
			Boundaries:    []string{"internal/merge/**", "README.md"},
			RequireChecks: true,
		},
		Checkout: Git{
			Dir:     s.checkout,
			Branch:  "main",
			HeadRef: headRef(changeNumber),
			Run:     s.run,
		},
		Tracker:  s.host,
		Task:     s.task.Number,
		Worktree: s.worktree,
		Home:     s.home,
		Repo:     repoName,
		Timeout:  time.Minute,
		Now:      func() time.Time { return s.now },
		Sleep: func(context.Context, time.Duration) error {
			s.waits++
			return nil
		},
	}
}

// run is the git of the merge of a test: the real one, with the identity of the test
// under it, and every command of it written down.
func (s *scenario) run(ctx context.Context, name string, args []string, dir string) ([]byte, []byte, int, error) {
	s.commands = append(s.commands, name+" "+strings.Join(args, " "))
	if len(args) > 0 && args[0] == "push" {
		s.pushes++
		if s.beforePush != nil {
			s.beforePush()
		}
	}
	stdout, stderr, code, err := s.repository.run(ctx, name, args, dir)
	if len(args) > 0 && args[0] == "push" && s.afterPush != nil {
		s.afterPush()
	}
	return stdout, stderr, code, err
}

// pushed tells whether a push of the branch of the project was called for at all: a
// merge refused by the gate pushes nothing, and every case of the table says which of
// the two it is.
func (s *scenario) pushed() bool { return s.pushes > 0 }

// assertRefused is what a merge that was not merged comes to: the one reason of the
// table of §7h, nothing pushed, and a journal that says which commands git was given.
func (s *scenario) assertRefused(t *testing.T, result Result, tc caseOfTable) {
	t.Helper()
	if result.Outcome != Refused {
		t.Fatalf("the outcome is %q, want %q (%+v)", result.Outcome, Refused, result.Verdict)
	}
	if result.Verdict.Reason != tc.want {
		t.Errorf("the merge is refused with %q (%s), want %q", result.Verdict.Reason, result.Verdict.Detail, tc.want)
	}
	if result.Verdict.Detail == "" {
		t.Errorf("the reason %q has no detail: a person has to be told what to do about it", result.Verdict.Reason)
	}
	if result.MergedSHA != "" {
		t.Errorf("the merge holds the commit %q, want none: nothing was merged", result.MergedSHA)
	}
	if len(s.closings) > 0 {
		t.Errorf("the merge that was refused closed %v, want no task closed: only a merge that went in closes one",
			s.closings)
	}
	s.assertPushes(t, tc)
}

// assertPushes is whether git was asked for a push of the branch of the project at all:
// a merge refused by the gate pushes nothing at all, and a push git refuses is the one
// case in which a merge made one (docs/DESIGN.md §7h).
func (s *scenario) assertPushes(t *testing.T, tc caseOfTable) {
	t.Helper()
	switch {
	case tc.pushes && !s.pushed():
		t.Errorf("git was asked for no push, want the merge to have tried: %v", s.commands)
	case !tc.pushes && s.pushed():
		t.Errorf("git was asked for %d pushes, want none", s.pushes)
	}
}

// assertMerged is what a merge that was not refused came to, and what the host holds
// afterwards: the outcome of the case, the commit of the head in the branch of the
// host, and the worktree of the task taken away — or named, where it could not be.
func (s *scenario) assertMerged(t *testing.T, result Result, tc caseOfTable) {
	t.Helper()
	if result.Outcome != tc.outcome {
		t.Fatalf("the outcome is %q, want %q (left %v)", result.Outcome, tc.outcome, result.Left)
	}
	if !result.OK() {
		t.Errorf("the outcome %q is not one crewflow calls a merge", result.Outcome)
	}
	if !result.Verdict.Ready {
		t.Errorf("the verdict is %+v, want the gate to have let the change in", result.Verdict)
	}
	s.assertPushes(t, tc)
	if !sameCommit(result.MergedSHA, s.change.HeadSHA) {
		t.Errorf("the merge says %s of the host is at %q, want the approved %s",
			s.change.BaseBranch, result.MergedSHA, s.change.HeadSHA)
	}
	if at := s.remoteMain(t); !sameCommit(at, s.change.HeadSHA) {
		t.Errorf("%s of the host is at %s, want the approved %s", s.change.BaseBranch, at, s.change.HeadSHA)
	}
	s.assertClosed(t, result, tc)
	if tc.outcome == MergedWithCleanupWarning {
		if len(result.Left) == 0 {
			t.Error("the merge says nothing is left to do by hand, want the worktree of the task named")
		}
		if _, err := os.Stat(s.worktree); err != nil {
			t.Errorf("the worktree of the task is not there any more although the merge could not take it away: %v", err)
		}
		return
	}
	if tc.outcome != Merged {
		return
	}
	if _, err := os.Stat(s.worktree); !os.IsNotExist(err) {
		t.Errorf("the worktree of the task is still there after the merge: %v", err)
	}
}

// assertClosed is what became of the task of the change behind a merge that went in:
// who closed it, whether the host was asked to close it at all, and whether the fact is
// written down in the outcome of the merge and in its journal — a report that does not
// say who closed the task leaves a person guessing (docs/DESIGN.md §6, §7h).
func (s *scenario) assertClosed(t *testing.T, result Result, tc caseOfTable) {
	t.Helper()
	if tc.closedBy == "" {
		return
	}
	if !result.TaskClosed || result.TaskClosedBy != tc.closedBy {
		t.Fatalf("the merge holds the task closed %v by %q, want it closed by %q",
			result.TaskClosed, result.TaskClosedBy, tc.closedBy)
	}
	if want := `"task_closed_by":"` + string(tc.closedBy) + `"`; !strings.Contains(read(t, result.Journal), want) {
		t.Errorf("the journal of the merge holds no %s:\n%s", want, read(t, result.Journal))
	}
	switch tc.closedBy {
	case ClosedByHost:
		if len(s.closings) > 0 {
			t.Errorf("the host closed the task behind the change and crewflow closed it a second time: %v", s.closings)
		}
	case ClosedByCrewflow:
		if s.reads < 2 {
			t.Errorf("the merge read the task %d times and closed it, want it to have waited for the host first", s.reads)
		}
		if len(s.closings) != 1 {
			t.Fatalf("crewflow closed %d tasks, want the one of the change: %v", len(s.closings), s.closings)
		}
		if got := s.closings[0].task; got != result.Task {
			t.Errorf("crewflow closed the task #%d, want #%d: the task closed is the one the change names",
				got, result.Task)
		}
		// The record under the task is what a person reads when they ask why the task is
		// closed: it has to name the change that went in and the commit it went in with.
		for _, want := range []string{"#" + strconv.Itoa(changeNumber), short(s.change.HeadSHA)} {
			if !strings.Contains(s.closings[0].comment, want) {
				t.Errorf("the record under the task is %q, want it to name %q", s.closings[0].comment, want)
			}
		}
	}
}

// TestAMergeOfWhatIsAlreadyInTheBranchPushesNothing is IT-020 on the same repository:
// the merge that went through, asked for a second time, finds the change in the branch
// and has nothing to do.
func TestAMergeOfWhatIsAlreadyInTheBranchPushesNothing(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not installed: %v", err)
	}
	s := newScenario(t, nil)

	first, err := Run(t.Context(), s.deps(), changeNumber)
	if err != nil {
		t.Fatalf("the first Run returned an error: %v", err)
	}
	if first.Outcome != Merged {
		t.Fatalf("the first merge came to %q, want %q", first.Outcome, Merged)
	}
	before := len(s.commands)

	again, err := Run(t.Context(), s.deps(), changeNumber)
	if err != nil {
		t.Fatalf("the second Run returned an error: %v", err)
	}

	if again.Outcome != AlreadyMerged {
		t.Fatalf("the second merge came to %q (%v), want %q", again.Outcome, again.Left, AlreadyMerged)
	}
	if !again.OK() {
		t.Errorf("a merge of what is already merged is not one crewflow calls a merge: %q", again.Outcome)
	}
	for _, command := range s.commands[before:] {
		if strings.Contains(command, " push ") {
			t.Errorf("the second merge ran %q, want no push at all", command)
		}
	}
	if at := s.remoteMain(t); !sameCommit(at, s.change.HeadSHA) {
		t.Errorf("main of the host is at %s, want the head of the change %s", at, s.change.HeadSHA)
	}
}

// TestTheJournalOfAMergeHoldsWhatGitSaid: the facts the gate judged, every command of
// git with everything it wrote, and how the merge came out. It is the file a person is
// sent to after a merge, and the code `git push` exited with is not what says where the
// branch ended up (docs/DESIGN.md §6, §7h).
func TestTheJournalOfAMergeHoldsWhatGitSaid(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not installed: %v", err)
	}
	s := newScenario(t, nil)

	result, err := Run(t.Context(), s.deps(), changeNumber)
	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	journal := read(t, result.Journal)
	for _, want := range []string{
		`"step":"gate"`,
		`"head":"` + s.change.HeadSHA + `"`,
		`"verdict":{"ready":true}`,
		`"command":"git push origin ` + s.change.HeadSHA + `:refs/heads/main"`,
		`"command":"git ls-remote origin refs/heads/main"`,
		`"outcome":"merged"`,
	} {
		if !strings.Contains(journal, want) {
			t.Errorf("the journal of the merge holds no %s:\n%s", want, journal)
		}
	}
	if !strings.Contains(journal, s.repository.remote) {
		t.Errorf("the journal of the merge holds no word of what git wrote:\n%s", journal)
	}
}

// TestTheMergeRecordsTheCommitInTheStateOfTheTask: what a check after the merge
// compares the branch of the host with, and what a list of the runs of the project says
// the task was merged at (docs/DESIGN.md §7h).
func TestTheMergeRecordsTheCommitInTheStateOfTheTask(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not installed: %v", err)
	}
	s := newScenario(t, nil)

	if _, err := Run(t.Context(), s.deps(), changeNumber); err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	state, err := taskrun.LoadState(s.state)
	if err != nil {
		t.Fatalf("read the state of the task: %v", err)
	}
	if state.MergedSHA != s.change.HeadSHA {
		t.Errorf("the state holds the merged commit %q, want %q", state.MergedSHA, s.change.HeadSHA)
	}
}

// pushedOnTopOfTheChange is a change that was merged and got a commit pushed to its
// branch afterwards: what a host says the head of a merged change is, and what the
// commit that went into the branch is, are then two commits (docs/DESIGN.md §7h).
func (s *scenario) pushedOnTopOfTheChange(t *testing.T) {
	t.Helper()
	s.git(t, s.checkout, "checkout", "--quiet", "change")
	s.commit(t, "after-the-merge")
	s.git(t, s.checkout, "push", "--quiet", "origin", "change:"+headRef(changeNumber))
	s.change.HeadSHA = s.sha(t.Context(), "change")
	s.git(t, s.checkout, "checkout", "--quiet", "main")
}

// repository is a bare repository — what a host is — and a checkout of it, which is what
// a merge pushes from. Both are made for one test and are gone with it, and neither of
// them is a repository of the person who runs the tests.
type repository struct {
	// remote is the bare repository, checkout the clone of it a merge runs in.
	remote   string
	checkout string
	// who is the name of the person the commits of the test are made by, which is the
	// only way to commit without the settings of the machine.
	who []string
	// t is the test, for the cases that need to make the repository say something
	// itself: git inside the table of §7h.
	t *testing.T
}

// newRepository is a bare repository with a default branch of two commits, a branch of
// a change with two commits of its own, a ref of a pull request standing at it and a
// branch on the side that is in the history of neither.
func newRepository(t *testing.T) *repository {
	t.Helper()
	root := t.TempDir()
	r := &repository{
		remote:   filepath.Join(root, "host.git"),
		checkout: filepath.Join(root, "checkout"),
		who: []string{
			"GIT_AUTHOR_NAME=crewflow tests", "GIT_AUTHOR_EMAIL=tests@crewflow.invalid",
			"GIT_COMMITTER_NAME=crewflow tests", "GIT_COMMITTER_EMAIL=tests@crewflow.invalid",
		},
		t: t,
	}
	r.git(t, "", "init", "--quiet", "--bare", "--initial-branch=main", r.remote)
	r.git(t, "", "init", "--quiet", "--initial-branch=main", r.checkout)
	r.git(t, r.checkout, "config", "commit.gpgsign", "false")
	r.git(t, r.checkout, "remote", "add", "origin", r.remote)
	r.commit(t, baseCommit)
	r.git(t, r.checkout, "push", "--quiet", "origin", "main")
	r.git(t, r.checkout, "checkout", "--quiet", "-b", "change")
	r.commit(t, firstCommit)
	r.commit(t, secondCommit)
	r.git(t, r.checkout, "push", "--quiet", "origin", "change:"+headRef(changeNumber))
	r.git(t, r.checkout, "checkout", "--quiet", "-b", otherCommit, "main")
	r.commit(t, otherCommit)
	r.git(t, r.checkout, "push", "--quiet", "origin", "main", "change", otherCommit)
	return r
}

// commit makes a commit on the branch that is checked out, under the name the cases
// know it by, and pushes nothing: a case pushes what it wants pushed.
func (r *repository) commit(t *testing.T, name string) {
	t.Helper()
	path := filepath.Join(r.checkout, "internal")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("make %s: %v", path, err)
	}
	if err := os.WriteFile(filepath.Join(path, name), []byte(name), 0o600); err != nil {
		t.Fatalf("write the file of the commit %s: %v", name, err)
	}
	r.git(t, r.checkout, "add", ".")
	r.git(t, r.checkout, "commit", "--quiet", "-m", "commit "+name)
}

// sha is the commit of the repository under that name, which is what a host would say
// the head of a change is.
func (r *repository) sha(ctx context.Context, name string) string {
	r.t.Helper()
	out, _, code, err := r.run(ctx, "git", []string{"rev-parse", name}, r.checkout)
	if err != nil || code != 0 {
		r.t.Fatalf("the commit of %s: %q, %d, %v", name, out, code, err)
	}
	return strings.TrimSpace(string(out))
}

// rewrite is a history rewritten under an approval: another commit where the branch of
// the change was, pushed over it, and the ref of the pull request moved with it. What
// the host says the head is follows (docs/DESIGN.md §7h).
func (r *repository) rewrite(ctx context.Context) string {
	r.t.Helper()
	r.git(r.t, r.checkout, "checkout", "--quiet", "change")
	r.git(r.t, r.checkout, "reset", "--quiet", "--hard", r.sha(ctx, "main"))
	r.commit(r.t, "rewritten")
	r.git(r.t, r.checkout, "push", "--quiet", "--force", "origin", "change:"+headRef(changeNumber))
	return r.sha(ctx, "change")
}

// moveMain is a default branch that goes on while a merge is going on: a second clone of
// the project commits and pushes into it, which is the way main moves on under anybody
// (docs/DESIGN.md §7h).
func (r *repository) moveMain(ctx context.Context) {
	r.t.Helper()
	other := filepath.Join(r.t.TempDir(), "other")
	r.git(r.t, "", "clone", "--quiet", r.remote, other)
	r.git(r.t, other, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(other, "moved-on.txt"), []byte("moved on\n"), 0o600); err != nil {
		r.t.Fatalf("write the file of the commit that moved main on: %v", err)
	}
	r.git(r.t, other, "add", ".")
	r.git(r.t, other, "commit", "--quiet", "-m", "chore: main moved on")
	r.git(r.t, other, "push", "--quiet", "origin", "main")
}

// rewindMain is a branch of the host that has been forced back to where it was before
// the change: a merge that a check afterwards finds out nothing of, and the one case a
// check after a merge is there for (docs/DESIGN.md §7h).
func (r *repository) rewindMain(ctx context.Context) {
	r.t.Helper()
	first, _, _, err := r.run(ctx, "git", []string{"rev-list", "--max-parents=0", "HEAD"}, r.checkout)
	if err != nil {
		r.t.Fatalf("the first commit of the repository: %v", err)
	}
	r.git(r.t, r.checkout, "push", "--quiet", "--force", "origin",
		strings.TrimSpace(string(first))+":refs/heads/main")
}

// forbidPush is a host that takes no push at all: a hook in the bare repository that
// refuses everything, which is what a branch rule of a host looks like from the git of
// a merge (docs/DESIGN.md §7g).
func (r *repository) forbidPush(ctx context.Context) {
	r.t.Helper()
	hooks := filepath.Join(r.remote, "hooks")
	if err := os.MkdirAll(hooks, 0o700); err != nil {
		r.t.Fatalf("make the folder of hooks of the host: %v", err)
	}
	hook := "#!/bin/sh\necho 'the rules of this branch let nothing through' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(hooks, "pre-receive"), []byte(hook), 0o700); err != nil {
		r.t.Fatalf("write the hook of the host: %v", err)
	}
}

// remoteMain is where the branch of the host is, as `git ls-remote` says it.
func (r *repository) remoteMain(t *testing.T) string {
	t.Helper()
	head, err := Git{Dir: r.checkout, Branch: "main", Run: r.run}.Head(t.Context(), "main")
	if err != nil {
		t.Fatalf("the branch of the host: %v", err)
	}
	return head
}

// git runs one git in dir and stops the test when it says no.
func (r *repository) git(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, stderr, code, err := r.run(t.Context(), "git", args, dir); err != nil || code != 0 {
		t.Fatalf("git %v: exited with %d: %s (%v)", args, code, stderr, err)
	}
}

// run is how git is started on the machine of the test: as a person would, in a folder,
// with the identity of the test under it and nothing of the machine in it.
func (r *repository) run(ctx context.Context, name string, args []string, dir string) ([]byte, []byte, int, error) {
	return doctor.Command(ctx, name, args, dir, r.who)
}

// headRef is the ref of the host that stands at the head of a change request: it is the
// name GitHub keeps the head of a pull request under, and a merge fetches it to have the
// commit of the head in a checkout (docs/DESIGN.md §7h).
func headRef(number int) string {
	return "refs/pull/" + strconv.Itoa(number) + "/head"
}

// read is what a file holds, for a test that checks what a merge left behind.
func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
