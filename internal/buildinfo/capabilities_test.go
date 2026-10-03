package buildinfo

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// everyCapability is the whole closed list of what a build of crewflow may have, written
// out in a test and not read out of the code: a list read out of the code holds itself
// whatever it holds, and the day a mechanism is added and left out of the list there is
// nothing to notice it.
var everyCapability = []string{
	CapabilityParallelAdmission,
	CapabilityTaskRunGoing,
	CapabilityOutputBoundary,
	CapabilityIdentitySeparation,
	CapabilityCapabilityRequires,
}

// TestThisBuildHasEveryCapabilityItNames is the promise the file of a project relies on:
// a build says which mechanisms it has, and every name it says is one this build really
// has. A name in the list that no build has is a promise nobody keeps, and a mechanism in
// the code that the list does not name is a mechanism a project cannot require
// (docs.DESIGN.md §5).
func TestThisBuildHasEveryCapabilityItNames(t *testing.T) {
	have := Capabilities()
	if !slices.IsSorted(have) {
		t.Errorf("Capabilities() = %v, want them in one order so that a report does not shuffle between two runs", have)
	}
	for _, name := range everyCapability {
		if !slices.Contains(have, name) {
			t.Errorf("the capability %q is not in the list of this build: %v", name, have)
		}
	}
	if len(have) != len(everyCapability) {
		t.Errorf("Capabilities() = %v, want exactly the %d capabilities of the build, and no other", have, len(everyCapability))
	}
}

// TestCapabilitiesIsACopy keeps a caller from taking a name out of the statement of the
// build for everybody who reads it after, the way the lists of codes of §6a are held.
func TestCapabilitiesIsACopy(t *testing.T) {
	shown := Capabilities()
	if len(shown) == 0 {
		t.Fatal("Capabilities() is empty, want the capabilities of this build")
	}
	shown[0] = "mutated-by-a-caller"

	if again := Capabilities(); again[0] == "mutated-by-a-caller" {
		t.Errorf("Capabilities() = %v after a caller changed its copy, want the list of the build itself", again)
	}
}

// TestRequireRefusesWhatThisBuildHasNot is the refusal of §5: a program whose safety
// depends on a mechanism it does not have says so with its own word, before it acts. The
// build of a test is one without the capability, which is what an installed build of an
// older version is (F-178).
func TestRequireRefusesWhatThisBuildHasNot(t *testing.T) {
	withoutOutputBoundary(t)

	err := Require([]string{CapabilityOutputBoundary, CapabilityParallelAdmission})

	var refusal *ErrCapabilityMissing
	if !errors.As(err, &refusal) {
		t.Fatalf("Require = %v, want the refusal of the capabilities of the build", err)
	}
	if !slices.Equal(refusal.Required, []string{CapabilityOutputBoundary}) {
		t.Errorf("the refusal names %v, want only what this build has not: %q", refusal.Required, CapabilityOutputBoundary)
	}
	for _, want := range []string{ReasonCapabilityMissing, CapabilityOutputBoundary, CapabilityParallelAdmission} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal says %q, want it to mention %q", err, want)
		}
	}
}

// TestRequirePassesForWhatTheBuildHas is the other half: a project that asks for nothing
// this build lacks runs, and a build that has everything the project names does not stop a
// command over a list of words.
func TestRequirePassesForWhatTheBuildHas(t *testing.T) {
	for _, requires := range [][]string{nil, {}, {CapabilityTaskRunGoing}, everyCapability} {
		if err := Require(requires); err != nil {
			t.Errorf("Require(%v) = %v, want no refusal: this build has every one of them", requires, err)
		}
	}
}

// TestRequireNamesEveryMissingCapability says them all at once: a person who fixes the
// program they run has to be able to see the whole list, not one name per refusal.
func TestRequireNamesEveryMissingCapability(t *testing.T) {
	withoutOutputBoundary(t)
	withoutTaskRunGoing(t)

	var refusal *ErrCapabilityMissing
	if !errors.As(Require([]string{CapabilityOutputBoundary, CapabilityTaskRunGoing, CapabilityParallelAdmission}), &refusal) {
		t.Fatal("Require of two missing capabilities returned no refusal, want both named")
	}
	if !slices.Equal(refusal.Required, []string{CapabilityOutputBoundary, CapabilityTaskRunGoing}) {
		t.Errorf("the refusal names %v, want both names, in the order the project wrote them", refusal.Required)
	}
}

// TestTheRefusalCarriesTheReasonAsItsOwnWord is what a program and a person read the same
// way: the word of the refusal is a constant of the code, and every refusal of it says it
// first (docs.DESIGN.md §5, §7e).
func TestTheRefusalCarriesTheReasonAsItsOwnWord(t *testing.T) {
	withoutOutputBoundary(t)

	err := Require([]string{CapabilityOutputBoundary})

	if head, _, _ := strings.Cut(err.Error(), ":"); head != ReasonCapabilityMissing {
		t.Errorf("the refusal begins with %q, want the word %q first", head, ReasonCapabilityMissing)
	}
}

// withoutOutputBoundary makes the build of a test a build of an older version: the
// mechanism is not in it, and the rest of the list stays as it was, the way the list of a
// build is not edited by a test of it (F-178, docs.DESIGN.md §5).
func withoutOutputBoundary(t *testing.T) {
	t.Helper()
	without(t, CapabilityOutputBoundary)
}

// withoutTaskRunGoing is the other refusal of a task run, taken out of the build of a test
// for the same reason (#208, docs.DESIGN.md §7).
func withoutTaskRunGoing(t *testing.T) {
	t.Helper()
	without(t, CapabilityTaskRunGoing)
}

// without takes one capability out of the build this test runs as.
func without(t *testing.T, name string) {
	t.Helper()
	kept := slices.Clone(capabilities)
	t.Cleanup(func() { capabilities = kept })
	capabilities = slices.DeleteFunc(slices.Clone(capabilities), func(have string) bool { return have == name })
	if slices.Contains(capabilities, name) {
		t.Fatalf("the build of the test still has the capability %q, want a build without it", name)
	}
}
