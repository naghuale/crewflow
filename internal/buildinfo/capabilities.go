package buildinfo

import (
	"fmt"
	"slices"
	"strings"
)

// ReasonCapabilityMissing is the word a command of a project is refused with when the
// build that runs it has not a mechanism the project requires. It is a word of §5 and not
// a word of §6a: nothing has gone wrong yet, nothing is waiting, and the refusal comes
// before anything is made — no branch, no worktree, no attempt and no window of the
// keychain (F-178, docs.DESIGN.md §5, §7i).
const ReasonCapabilityMissing = "crewflow-capability-missing"

// The capabilities of a build: the mechanisms whose absence it refuses to go on with.
// A name is here because the code of this build has that mechanism, and not because the
// design describes it — a capability the code has not written is a promise a project
// would rely on and nobody keeps (F-178, docs.DESIGN.md §5).
//
// Every exported identifier of this file is a name a project writes in `[crewflow]
// requires`, so each one is spelled once here and nowhere else.
const (
	// CapabilityParallelAdmission is the written admission of a pair of tasks to run
	// beside each other and the refusal of a second run of a project without it
	// (docs.DESIGN.md §7c, #182, `crewflow task admit`).
	CapabilityParallelAdmission = "parallel-admission"
	// CapabilityTaskRunGoing is the refusal of a second run of a task while the run of
	// that task is going: the state of the task and the two files of an attempt belong
	// to that attempt and to nobody else (docs.DESIGN.md §7, #208).
	CapabilityTaskRunGoing = "task-run-going"
	// CapabilityOutputBoundary is the one boundary every text of a command goes out
	// through: the terminal of the person, the journal of an attempt, the events of a
	// route, the state of a task and every `-json` (docs.DESIGN.md §7e).
	CapabilityOutputBoundary = "output-boundary"
	// CapabilityIdentitySeparation is the separation of the subjects of §7i: a run and a
	// review of the same project work as accounts of their own, and a report of the
	// machine says which one each of them is (docs.DESIGN.md §7i, §7k).
	CapabilityIdentitySeparation = "identity-separation"
	// CapabilityCapabilityRequires is this list itself: the build says which mechanisms
	// it has, the project names the ones it needs, and a command whose safety depends on
	// one of them refuses with [ReasonCapabilityMissing] before it does anything
	// (docs.DESIGN.md §5, #218).
	CapabilityCapabilityRequires = "capability-requires"
)

// capabilities is what this build has, in the order a report reads them in. It is a
// variable and not the list of the constants above so that a build made from a tree in
// which one of the mechanisms is not written carries a list without it — which is what an
// installed program of an older version is, and the whole of what this file is for
// (F-178).
var capabilities = []string{
	CapabilityCapabilityRequires,
	CapabilityIdentitySeparation,
	CapabilityOutputBoundary,
	CapabilityParallelAdmission,
	CapabilityTaskRunGoing,
}

// Capabilities is the list of what this build has. It is given out as a copy: the list is
// a statement about the build, and no caller may edit that statement for everybody who
// reads it after (docs.DESIGN.md §5, §6a).
func Capabilities() []string {
	return slices.Clone(capabilities)
}

// Has is whether this build has the named capability. It is also what answers whether the
// name is one crewflow knows of at all: a project may require a mechanism of a build that
// is not this one, and such a name is refused here as well — what a project cannot have is
// what this build has not got (docs.DESIGN.md §5).
func Has(name string) bool {
	return slices.Contains(capabilities, name)
}

// Missing are the capabilities of requires that this build does not have, in the order the
// project wrote them: a person who is told which mechanism the program they run has not
// can do something about it, and one who is told "no" cannot.
func Missing(requires []string) []string {
	var missing []string
	for _, name := range requires {
		if !Has(name) {
			missing = append(missing, name)
		}
	}
	return missing
}

// Require refuses when requires names a capability this build does not have, and says
// nothing at all when it names none it has not: a project that asks for what the program
// on the machine can do is not stopped over a list of words (docs.DESIGN.md §5).
func Require(requires []string) error {
	missing := Missing(requires)
	if len(missing) == 0 {
		return nil
	}
	return &ErrCapabilityMissing{Required: missing, Has: Capabilities()}
}

// ErrCapabilityMissing is the refusal of a command whose safety depends on a mechanism the
// build running it does not have. It carries the word of the refusal, what the project
// requires and what this build has, so that the program and a person read the same answer
// out of it (docs.DESIGN.md §5, §7e).
type ErrCapabilityMissing struct {
	// Required are the capabilities of `[crewflow] requires` that this build has not, in
	// the order the file of the project wrote them.
	Required []string
	// Has are the capabilities this build has, the whole list: a person has to be able
	// to see what is there to work out which one of them to go without.
	Has []string
}

// Error is the refusal in the words of §5: the word of it, what the project needs, what
// this build has, and what to do about it. The word comes first, because it is what an
// orchestrator switches on and a person greps for (docs.DESIGN.md §5).
func (e *ErrCapabilityMissing) Error() string {
	return fmt.Sprintf("%s: this build of crewflow does not have [%s], which the file of the project "+
		"requires; this build has [%s] — установите сборку, в которой этот механизм есть "+
		"(install a build of crewflow that has it; docs/DESIGN.md §5)",
		ReasonCapabilityMissing, listed(e.Required), listed(e.Has))
}

// listed is a list of the capabilities as one line of a refusal reads it.
func listed(names []string) string {
	return strings.Join(names, ", ")
}
