package task

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestAdmissionOverlapOfTheDeclaredBoundaries is K1 worked out of the globs the two tasks
// declare, before either of them has written a line: two boundaries that may both be true
// of one file are a common write path already, and a pair whose declarations do not meet
// may go as far as the files will tell (docs/DESIGN.md §7c, F-135).
func TestAdmissionOverlapOfTheDeclaredBoundaries(t *testing.T) {
	cases := []struct {
		name       string
		one, other Boundaries
		want       []string
	}{
		{
			name: "the same folder of the project",
			one:  Boundaries{"internal/run/**"},
			want: nil,
		},
		{
			name:  "one is under the other",
			one:   Boundaries{"docs/**"},
			other: Boundaries{"docs/DESIGN.md"},
			want:  []string{"docs/** and docs/DESIGN.md"},
		},
		{
			name:  "one is the folder the other covers",
			one:   Boundaries{"docs/DESIGN.md"},
			other: Boundaries{"docs/**"},
			want:  []string{"docs/DESIGN.md and docs/**"},
		},
		{
			name:  "one is under the middle of the other",
			one:   Boundaries{"internal/run/**"},
			other: Boundaries{"internal/**"},
			want:  []string{"internal/run/** and internal/**"},
		},
		{
			// A boundary names a path, and `internal` is the one entry of the repository
			// under that name and not everything under it — `internal/**` is that.
			name:  "a folder of the project and the file inside it, both named",
			one:   Boundaries{"internal"},
			other: Boundaries{"internal/run.go"},
			want:  nil,
		},
		{
			name:  "a glob of a name and the name itself",
			one:   Boundaries{"*.go"},
			other: Boundaries{"main.go"},
			want:  []string{"*.go and main.go"},
		},
		{
			name:  "two folders of the project that do not touch",
			one:   Boundaries{"internal/run/**"},
			other: Boundaries{"docs/DESIGN.md"},
			want:  nil,
		},
		{
			name:  "a file of the root and a folder of it",
			one:   Boundaries{"README.md"},
			other: Boundaries{"docs/**"},
			want:  nil,
		},
		{
			// Two patterns of one segment meet where a name of the files of both may be one
			// and the same name, and `*.go` and `*.md` are two such names that never are.
			name:  "two globs of a name that cannot be one name",
			one:   Boundaries{"*.go"},
			other: Boundaries{"*.md"},
			want:  nil,
		},
		{
			name:  "a glob of a name and one that covers every name",
			one:   Boundaries{"*_test.go"},
			other: Boundaries{"*"},
			want:  []string{"*_test.go and *"},
		},
		{
			name:  "one star and a question mark against a name",
			one:   Boundaries{"run?.go"},
			other: Boundaries{"run1.go"},
			want:  []string{"run?.go and run1.go"},
		},
		{
			// A `**` in the middle stands for any number of folders, and `internal/**` is
			// every folder of the package and not the folder of it alone.
			name:  "a star in the middle and the folder under it",
			one:   Boundaries{"internal/**"},
			other: Boundaries{"internal/run/state.go"},
			want:  []string{"internal/** and internal/run/state.go"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Overlap(tc.one, tc.other)
			if len(got) != len(tc.want) {
				t.Fatalf("the overlap of %v and %v = %v, want %v", tc.one, tc.other, got, tc.want)
			}
			for _, want := range tc.want {
				if !contains(got, want) {
					t.Errorf("the overlap of %v and %v = %v, want it to hold %q", tc.one, tc.other, got, want)
				}
			}
			// The overlap does not depend on the order the two boundaries were named in:
			// the record of a pair is one file and a refusal of it names it the same way
			// whoever names it.
			reversed := Overlap(tc.other, tc.one)
			if len(reversed) != len(got) {
				t.Errorf("the overlap the other way round = %v, want %v", reversed, got)
			}
		})
	}
}

// TestAdmissionStoppingIsAFailBeforeAnUnknown is what stops a pair: a criterion that says
// the two tasks have something in common is a fact about the pair and is named with the
// resource it is, and a criterion nobody worked out is not (MODEL, IV-031, §7c).
func TestAdmissionStoppingIsAFailBeforeAnUnknown(t *testing.T) {
	cases := []struct {
		name      string
		criteria  Criteria
		wantName  string
		wantFound bool
		verified  bool
	}{
		{
			name:     "every criterion is a pass",
			criteria: allPass(),
			verified: true,
		},
		{
			name:      "the first criterion that failed",
			criteria:  withCriteria(allPass(), Criteria{K3: Criterion{Result: ResultFail, Resource: "crewflow.toml"}}),
			wantName:  "K3",
			wantFound: true,
		},
		{
			name:      "a criterion nobody worked out",
			criteria:  withCriteria(allPass(), Criteria{K5: Criterion{Result: ResultUnknown}}),
			wantName:  "K5",
			wantFound: true,
		},
		{
			// A criterion that failed is named before a criterion nobody worked out, whichever
			// of the two comes first in the list: a `fail` is a fact about the pair and an
			// `unknown` is not one, and the refusal says which of the two it is.
			name: "a failed criterion before an unknown one",
			criteria: withCriteria(allPass(), Criteria{
				K2: Criterion{Result: ResultUnknown},
				K7: Criterion{Result: ResultFail, Resource: "internal/run/codes.go"},
			}),
			wantName:  "K7",
			wantFound: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.criteria.Verified(); got != tc.verified {
				t.Errorf("Verified() = %t, want %t", got, tc.verified)
			}
			name, one, found := tc.criteria.Stopping()
			if found != tc.wantFound {
				t.Fatalf("Stopping() found %t, want %t", found, tc.wantFound)
			}
			if !tc.wantFound {
				return
			}
			if name != tc.wantName {
				t.Errorf("the criterion that stops the pair = %q, want %q", name, tc.wantName)
			}
			if one.Result == ResultPass {
				t.Errorf("the criterion that stops the pair is %q, want a criterion that is not a pass", one.Result)
			}
		})
	}
}

// TestAdmissionRefusesARecordItCannotObey is what `task run` does with a record that says
// one thing and is another: a word outside of the format, a decision of `allowed` with a
// criterion that is not a `pass`, a criterion without a reason and a record a hand has
// been in. A rule applied from a record nobody checked is a rule nobody wrote
// (docs.DESIGN.md §7c, §7h).
func TestAdmissionRefusesARecordItCannotObey(t *testing.T) {
	edited, err := whole().Set()
	if err != nil {
		t.Fatalf("the record of a whole pair: %v", err)
	}
	edited.ValidWhile.Shared = "a hand was in this record"
	cases := []struct {
		name   string
		record Admission
		want   string
	}{
		{
			name:   "a record of a format of another crewflow",
			record: withDecision(settled(t, whole()), AdmissionVersion+1, DecisionAllowed),
			want:   "the format",
		},
		{
			name:   "a decision that is not one of the three",
			record: withDecision(settled(t, whole()), AdmissionVersion, "probably"),
			want:   "not one of",
		},
		{
			name:   "a criterion with a result nobody wrote",
			record: withCriteriaIn(settled(t, whole()), Criteria{K4: Criterion{Result: "yes"}}),
			want:   "not one of",
		},
		{
			name:   "a criterion that is not a pass with nothing to explain it",
			record: withCriteriaIn(settled(t, whole()), Criteria{K6: Criterion{Result: ResultUnknown}}),
			want:   "with no reason",
		},
		{
			name:   "a pair that is allowed while one of the criteria is not a pass",
			record: withCriteriaIn(settled(t, whole()), Criteria{K7: Criterion{Result: ResultUnknown, Reason: "nobody has looked"}}),
			want:   "says one thing and is another",
		},
		{
			name: "an exception of the owner with nothing in it",
			record: withOwner(withDecision(settled(t, whole()), AdmissionVersion, DecisionOwnerException),
				&OwnerException{}),
			want: "with nothing in it",
		},
		{
			name:   "a record a hand has been in",
			record: edited,
			want:   "was written by somebody",
		},
		{
			name:   "a pair that names one task twice",
			record: withTasks(settled(t, whole()), NewPair(43, 43)),
			want:   "not a pair of two tasks",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.record.Checked()
			if err == nil {
				t.Fatalf("a record crewflow cannot obey was accepted, want a refusal about %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal says %q, want it to be about %q", err, tc.want)
			}
		})
	}
}

// TestAdmissionOfAWholePairIsOneCrewflowWrote: the record as it is written, read back out
// of the file, still says what it said — the digest is of its own contents, and the
// criteria, the decision and the facts it was taken against have all come through.
func TestAdmissionOfAWholePairIsOneCrewflowWrote(t *testing.T) {
	record, err := whole().Set()
	if err != nil {
		t.Fatalf("the record of a whole pair: %v", err)
	}
	if err := record.Checked(); err != nil {
		t.Fatalf("the record of a whole pair is refused: %v", err)
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("the record of a whole pair as JSON: %v", err)
	}
	for _, want := range []string{
		`"version":1`, `"repository_id":null`, `"tasks"`, `"snapshot_id"`,
		`"K1"`, `"K2"`, `"K8"`, `"decision":"allowed"`, `"created_at"`, `"valid_while"`,
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("the record as JSON does not hold %q:\n%s", want, data)
		}
	}
	var read Admission
	if err := json.Unmarshal(data, &read); err != nil {
		t.Fatalf("the record of a whole pair read back: %v", err)
	}
	if err := read.Checked(); err != nil {
		t.Errorf("the record read back is refused: %v", err)
	}
	if !read.Criteria.Verified() || read.Decision != DecisionAllowed {
		t.Errorf("the record read back holds the criteria %+v and the decision %q, want all of them a pass and `allowed`",
			read.Criteria, read.Decision)
	}
}

// TestAdmissionStaleNamesWhatHasGoneOnWithoutTheRecord is what a run is told when the world
// the record was taken against is not the world the pair is about to start in: each fact
// of it by name, so that a person knows what to write the record again for (docs/DESIGN.md
// §7c).
func TestAdmissionStaleNamesWhatHasGoneOnWithoutTheRecord(t *testing.T) {
	was := ValidWhile{
		Specs:      map[string]string{"41": "aaaa", "43": "bbbb"},
		Boundaries: map[string][]string{"41": {"internal/run/**"}, "43": {"docs/**"}},
		Branches:   map[string]string{"41": "crewflow/41-one", "43": "crewflow/43-two"},
		Active:     []int{41},
		Shared:     "cccc",
	}
	cases := []struct {
		name string
		now  ValidWhile
		want string
	}{
		{name: "nothing has changed", now: was},
		{
			name: "the specification of a task",
			now:  withSpec(was, "43", "dddd"),
			want: "the specification of the task #43 has changed",
		},
		{
			name: "what a task declares it may change",
			now:  withBoundaries(was, "41", []string{"internal/task/**"}),
			want: "what the task #41 declares it may change has changed",
		},
		{
			name: "the branch of a task",
			now:  withBranch(was, "43", "crewflow/43-other"),
			want: "the branch of the task #43 has changed",
		},
		{
			name: "the set of the runs going on",
			now:  withActive(was, []int{41, 43}),
			want: "the set of the tasks going on in the project has changed",
		},
		{
			name: "the files both of the tasks may change",
			now:  withShared(was, "eeee"),
			want: "the files both of the tasks may change have changed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			moved := Stale(was, tc.now)
			if tc.want == "" {
				if len(moved) != 0 {
					t.Fatalf("Stale() = %v, want nothing: the record is still about this pair", moved)
				}
				return
			}
			if len(moved) != 1 || moved[0] != tc.want {
				t.Errorf("Stale() = %v, want one line about %q", moved, tc.want)
			}
		})
	}
}

// TestBoundariesCoverTheFilesOfTheRun is the question the admission of a pair asks of
// every file a working copy has changed: is it a file the other of the two tasks may
// change (docs/DESIGN.md §7c).
func TestBoundariesCoverTheFilesOfTheRun(t *testing.T) {
	boundaries := Boundaries{"internal/run/**", "changelog.d/*.md"}
	for _, want := range []string{"internal/run/run.go", "internal/run/profile/profile.go", "changelog.d/182.md"} {
		if !boundaries.Covers(want) {
			t.Errorf("the boundaries do not cover %q, want them to", want)
		}
	}
	for _, unwanted := range []string{"internal/task/task.go", "changelog.d/notes.txt", "docs/DESIGN.md"} {
		if boundaries.Covers(unwanted) {
			t.Errorf("the boundaries cover %q, want them not to", unwanted)
		}
	}
}

// TestTheCriteriaOfAPairAreTheEightOfTheModel: the names are a closed list in the order of
// MODEL, IV-031, and a report reads them in that order with what each of them is about. A
// ninth criterion is a criterion nobody wrote a test against, and a word outside of the list
// is not a criterion (docs/DESIGN.md §7e).
func TestTheCriteriaOfAPairAreTheEightOfTheModel(t *testing.T) {
	want := []string{"K1", "K2", "K3", "K4", "K5", "K6", "K7", "K8"}
	if got := CriteriaNames(); !slices.Equal(got, want) {
		t.Fatalf("the criteria of a pair = %v, want %v", got, want)
	}
	for _, name := range want {
		if about := Says(name); about == "" || about == name {
			t.Errorf("the criterion %s is about %q, want what it is about in the words of a report", name, about)
		}
	}
	if got := Says("K9"); got != "K9" {
		t.Errorf("Says of a name that is not a criterion = %q, want the name itself", got)
	}
	if !slices.Contains(Results, ResultUnknown) || !slices.Contains(Decisions, DecisionOwnerException) {
		t.Errorf("the results %v and the decisions %v are not the closed lists of the format", Results, Decisions)
	}
}

// whole is a record of a pair every criterion of which is a `pass`, with the facts it was
// taken against filled in: the record `crewflow task admit` writes when a pair holds.
func whole() Admission {
	record := Admission{
		Version: AdmissionVersion,
		Tasks:   NewPair(41, 43),
		Criteria: Criteria{
			K1: WritePaths{Criterion: Criterion{
				Result: ResultPass,
				Reason: "neither the declared boundaries nor the files of the working copies have anything in common",
			}},
		},
		Decision:  DecisionAllowed,
		CreatedAt: time.Date(2026, time.October, 3, 9, 0, 0, 0, time.UTC),
		ValidWhile: ValidWhile{
			Specs:      map[string]string{"41": "aaaa", "43": "bbbb"},
			Boundaries: map[string][]string{"41": {"internal/run/**"}, "43": {"docs/**"}},
			Branches:   map[string]string{"41": "crewflow/41-one", "43": "crewflow/43-two"},
			Active:     []int{41},
			Shared:     "cccc",
		},
	}
	record.Criteria = withCriteria(allPass(), record.Criteria)
	return record
}

// allPass is the eight criteria of a pair, every one of them a `pass`.
func allPass() Criteria {
	return Criteria{
		K1: WritePaths{Criterion: Criterion{Result: ResultPass}},
		K2: Criterion{Result: ResultPass},
		K3: Criterion{Result: ResultPass},
		K4: Criterion{Result: ResultPass},
		K5: Criterion{Result: ResultPass},
		K6: Criterion{Result: ResultPass},
		K7: Criterion{Result: ResultPass},
		K8: Criterion{Result: ResultPass},
	}
}

// withCriteria is these criteria with the ones given over them, a criterion of the two
// that says nothing being the one of the first: a test builds a pair of eight criteria out
// of a whole and moves one or two of them.
func withCriteria(base, over Criteria) Criteria {
	for _, name := range CriteriaNames() {
		if said, is := over.One(name); is && said.Result != "" {
			base.Set(name, said)
		}
	}
	return base
}

// settled is the record with the digest of its own contents, which is what
// `crewflow task admit` writes and what a test needs before it breaks one thing in a
// record on purpose.
func settled(t *testing.T, record Admission) Admission {
	t.Helper()
	written, err := record.Set()
	if err != nil {
		t.Fatalf("the record of the pair %s: %v", record.Tasks, err)
	}
	return written
}

// withCriteriaIn is the record with that one criterion in it, the rest left as they are.
func withCriteriaIn(record Admission, one Criteria) Admission {
	record.Criteria = withCriteria(record.Criteria, one)
	return record
}

// withDecision is the record with a version and a decision in it.
func withDecision(record Admission, version int, decision string) Admission {
	record.Version, record.Decision = version, decision
	return record
}

// withOwner is the record with the decision of the owner in it.
func withOwner(record Admission, owner *OwnerException) Admission {
	record.Owner = owner
	return record
}

// withTasks is the record of another pair.
func withTasks(record Admission, pair Pair) Admission {
	record.Tasks = pair
	return record
}

// withSpec, withBoundaries, withBranch, withActive and withShared are the facts of a pair
// as they stand now, one of them moved at a time: the maps are copied rather than written
// into, so that the world a record was taken against is not moved with them.
func withSpec(was ValidWhile, task, digest string) ValidWhile {
	was.Specs = maps(was.Specs)
	was.Specs[task] = digest
	return was
}

func withBoundaries(was ValidWhile, task string, globs []string) ValidWhile {
	was.Boundaries = slicesOf(was.Boundaries)
	was.Boundaries[task] = globs
	return was
}

func withBranch(was ValidWhile, task, branch string) ValidWhile {
	was.Branches = maps(was.Branches)
	was.Branches[task] = branch
	return was
}

func withActive(was ValidWhile, numbers []int) ValidWhile {
	was.Active = numbers
	return was
}

func withShared(was ValidWhile, digest string) ValidWhile {
	was.Shared = digest
	return was
}

func maps(one map[string]string) map[string]string {
	copied := map[string]string{}
	for key, value := range one {
		copied[key] = value
	}
	return copied
}

func slicesOf(one map[string][]string) map[string][]string {
	copied := map[string][]string{}
	for key, value := range one {
		copied[key] = value
	}
	return copied
}

// contains is whether a list holds a line, so that the order of the overlap of two
// boundaries does not matter to the test of it.
func contains(all []string, one string) bool {
	for _, line := range all {
		if line == one {
			return true
		}
	}
	return false
}
