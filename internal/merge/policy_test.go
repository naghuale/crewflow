package merge

import (
	"slices"
	"testing"

	"github.com/naghuale/crewflow/internal/secret"
)

// TestTheBoundaryKnowsEveryOutcomeAndWhoClosedTheTaskOfAMerge: the outcome of a merge and the
// word of who closed the task behind it are words of closed lists of §7h, and a program decides
// by them. The boundary holds a copy of both and the copy is held here against the constants
// that write them (D-044, D-082, docs/DESIGN.md §7h).
func TestTheBoundaryKnowsEveryOutcomeAndWhoClosedTheTaskOfAMerge(t *testing.T) {
	_, outcomes, known := secret.ClassOf(secret.DocumentMerge, "outcome")
	if !known {
		t.Fatal("the policy of the merge does not name the outcome of a merge")
	}
	want := []string{string(Merged), string(AlreadyMerged), string(MergedWithCleanupWarning), string(Refused)}
	if !sameWords(outcomes, want) {
		t.Errorf("the outcomes of a merge are %q, want the words of the code: %q", outcomes, want)
	}
	_, closed, known := secret.ClassOf(secret.DocumentMerge, "task_closed_by")
	if !known {
		t.Fatal("the policy of the merge does not name who closed the task of a change")
	}
	closedBy := []string{string(ClosedByHost), string(ClosedByCrewflow)}
	if !sameWords(closed, closedBy) {
		t.Errorf("the words of who closed the task are %q, want %q", closed, closedBy)
	}
}

// TestTheBoundaryHoldsTheStatesOfTheChecksOfAVerification: what came of the checks of the
// branch after the merge is a word of the closed list of the host as the gate keeps it.
func TestTheBoundaryHoldsTheStatesOfTheChecksOfAVerification(t *testing.T) {
	_, states, known := secret.ClassOf(secret.DocumentVerify, "ci")
	if !known {
		t.Fatal("the policy of the verification does not name the state of its checks")
	}
	if want := []string{"pending", "success", "failure", "none"}; !sameWords(states, want) {
		t.Errorf("the states of the checks of a verification are %q, want %q", states, want)
	}
}

// sameWords is whether two lists of words of the format hold the same words: the order a boundary
// reads a list in is its own, and the words of it are the words of the code.
func sameWords(one, other []string) bool {
	first, second := slices.Clone(one), slices.Clone(other)
	slices.Sort(first)
	slices.Sort(second)
	return slices.Equal(first, second)
}
