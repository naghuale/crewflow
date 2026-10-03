package access

import (
	"slices"
	"testing"

	"github.com/naghuale/crewflow/internal/secret"
)

// TestTheBoundaryKnowsEveryOneWhoMayAskForAFolder: a folder is open because the file of the
// project, the task of a run or crewflow itself asked for it, and there is no fourth asker. The
// boundary holds the three and refuses a document that names another one (D-044, D-082,
// docs/DESIGN.md §7d).
func TestTheBoundaryKnowsEveryOneWhoMayAskForAFolder(t *testing.T) {
	_, words, known := secret.ClassOf(secret.DocumentDoctor, "access.read[].source")
	if !known {
		t.Fatal("the policy of the report of the machine does not name who asked for a folder")
	}
	want := []string{string(SourceAccess), string(SourceTask), string(SourceCrewflow)}
	if !sameWords(words, want) {
		t.Errorf("the ones who may ask for a folder are %q, want the words of the code: %q", words, want)
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
