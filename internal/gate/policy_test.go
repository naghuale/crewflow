package gate

import (
	"slices"
	"testing"

	"github.com/naghuale/crewflow/internal/secret"
)

// TestTheBoundaryKnowsEveryReasonTheGateNames: a reason of a refusal of the gate is a word of
// the table of §7h, and a program switches on it. The boundary holds a copy of that table to
// refuse a document that says a word the format does not name, and the copy is held here word
// by word against the constants that write it (D-044, D-082, docs/DESIGN.md §7h).
func TestTheBoundaryKnowsEveryReasonTheGateNames(t *testing.T) {
	for _, path := range []string{"verdict.reason"} {
		for _, doc := range []secret.Document{secret.DocumentReview, secret.DocumentMerge} {
			_, words, known := secret.ClassOf(doc, path)
			if !known {
				t.Fatalf("the policy of %q does not name %q", doc, path)
			}
			if !sameWords(words, reasonsOfTheGate()) {
				t.Errorf("the reasons of %q in %q are %q, want the words of the gate: %q",
					path, doc, words, reasonsOfTheGate())
			}
		}
	}
}

// TestTheBoundaryKnowsBothDecisionsOfARecordOfAReview: a record of a review is one of two
// words and no third one — a record crewflow cannot read is not an approval (§7h).
func TestTheBoundaryKnowsBothDecisionsOfARecordOfAReview(t *testing.T) {
	_, words, known := secret.ClassOf(secret.DocumentReview, "review.decision")
	if !known {
		t.Fatal("the policy of the review does not name the decision of a record")
	}
	if want := []string{string(Approved), string(ChangesRequested)}; !sameWords(words, want) {
		t.Errorf("the decisions of a record of a review are %q, want %q", words, want)
	}
}

// TestTheBoundaryHoldsTheStatesOfTheChecksAndTheRules: how the checks of a commit stand and
// what the host says about the rules of the branch are words of closed lists of §7h, and the
// boundary refuses a document that says another one.
func TestTheBoundaryHoldsTheStatesOfTheChecksAndTheRules(t *testing.T) {
	_, states, known := secret.ClassOf(secret.DocumentReview, "checks[].state")
	if !known {
		t.Fatal("the policy of the review does not name the state of a check")
	}
	if want := []string{"pending", "success", "failure", "none"}; !sameWords(states, want) {
		t.Errorf("the states of a check are %q, want %q", states, want)
	}
	_, rules, known := secret.ClassOf(secret.DocumentReview, "rules")
	if !known {
		t.Fatal("the policy of the review does not name the state of the rules of the branch")
	}
	if want := []string{"named", "unavailable-on-plan"}; !sameWords(rules, want) {
		t.Errorf("the states of the rules of a branch are %q, want %q", rules, want)
	}
}

// reasonsOfTheGate is every reason of a refusal of the gate in the words of its constants.
func reasonsOfTheGate() []string {
	reasons := make([]string, 0, len(Reasons))
	for _, reason := range Reasons {
		reasons = append(reasons, string(reason))
	}
	return reasons
}

// sameWords is whether two lists of words of the format hold the same words: the order a boundary
// reads a list in is its own, and the words of it are the words of the code.
func sameWords(one, other []string) bool {
	first, second := slices.Clone(one), slices.Clone(other)
	slices.Sort(first)
	slices.Sort(second)
	return slices.Equal(first, second)
}
