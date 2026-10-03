package doctor

import (
	"slices"
	"testing"

	"github.com/naghuale/crewflow/internal/secret"
)

// TestTheBoundaryKnowsEveryAnswerOfACheckOfTheMachine: a check of the machine of the person
// ends as one of three words, and only `fail` stops crewflow — a word outside the three is not
// an answer of the format, and a document with one is a document a program cannot read. The
// boundary holds the three and the test holds them here against the constants (D-044, D-082,
// docs/DESIGN.md §7d).
func TestTheBoundaryKnowsEveryAnswerOfACheckOfTheMachine(t *testing.T) {
	for _, path := range []string{"checks[].status"} {
		_, words, known := secret.ClassOf(secret.DocumentDoctor, path)
		if !known {
			t.Fatalf("the policy of the report of the machine does not name %q", path)
		}
		want := []string{string(OK), string(Warn), string(Fail)}
		if !sameWords(words, want) {
			t.Errorf("the answers of a check in %q are %q, want the words of the code: %q", path, words, want)
		}
	}
	_, words, known := secret.ClassOf(secret.DocumentDoctorNetwork, "[].status")
	if !known {
		t.Fatal("the policy of the checks of the route does not name the answer of a check")
	}
	want := []string{string(OK), string(Warn), string(Fail)}
	if !sameWords(words, want) {
		t.Errorf("the answers of a check of the route are %q, want %q", words, want)
	}
	// The two subjects of a project have four modes between them, and the mode of the
	// orchestrator is not the mode of the executor (docs/DESIGN.md §7i).
	_, ofExecutor, _ := secret.ClassOf(secret.DocumentDoctor, "identity.mode")
	_, ofOrchestrator, _ := secret.ClassOf(secret.DocumentDoctor, "orchestrator.mode")
	if sameWords(ofExecutor, ofOrchestrator) {
		t.Errorf("the mode of the executor is %q and of the orchestrator is %q, want two different sets of words",
			ofExecutor, ofOrchestrator)
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
