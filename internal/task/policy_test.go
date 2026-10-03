package task

import (
	"slices"
	"testing"

	"github.com/naghuale/crewflow/internal/secret"
)

// TestTheBoundaryKnowsEveryResultAndDecisionOfARecordOfAPair: the result of a criterion and
// the decision the eight of them add up to are words of closed lists of §7c, and a program
// decides by them. The boundary holds a copy of both, and the copy is held here against the
// lists the package keeps (D-044, D-082, docs/DESIGN.md §7c).
func TestTheBoundaryKnowsEveryResultAndDecisionOfARecordOfAPair(t *testing.T) {
	_, results, known := secret.ClassOf(secret.DocumentTaskAdmit, "criteria.K1.result")
	if !known {
		t.Fatal("the policy of the admission does not name the result of a criterion")
	}
	if !sameWords(results, Results) {
		t.Errorf("the results of a criterion are %q, want the words of the code: %q", results, Results)
	}
	_, decisions, known := secret.ClassOf(secret.DocumentTaskAdmit, "decision")
	if !known {
		t.Fatal("the policy of the admission does not name the decision of a record")
	}
	if !sameWords(decisions, Decisions) {
		t.Errorf("the decisions of a record are %q, want the words of the code: %q", decisions, Decisions)
	}
	_, of, _ := secret.ClassOf(secret.DocumentTaskRun, "admission.decision")
	if !sameWords(of, Decisions) {
		t.Errorf("the decisions of the admission of a run are %q, want the words of the code: %q", of, Decisions)
	}
}

// TestTheBoundaryHoldsTheFormOfTheDigestOfARecord: the digest of a record of a pair is what
// says that the record is the one crewflow wrote, and the boundary refuses a document whose
// digest is not of the form the code writes it in.
func TestTheBoundaryHoldsTheFormOfTheDigestOfARecord(t *testing.T) {
	form, known := secret.FormOf(secret.DocumentTaskAdmit, "snapshot_id")
	if !known {
		t.Fatal("the policy of the admission does not name the digest of a record")
	}
	record := Admission{Tasks: NewPair(41, 43), Decision: DecisionAllowed}
	whole, err := record.Set()
	if err != nil {
		t.Fatalf("set the digest of a record: %v", err)
	}
	if !form(whole.SnapshotID) {
		t.Errorf("the form of the digest of a record refuses the digest the code writes: %q", whole.SnapshotID)
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
