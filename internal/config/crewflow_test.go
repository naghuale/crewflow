package config

import (
	"slices"
	"strings"
	"testing"

	"github.com/naghuale/crewflow/internal/buildinfo"
)

// TestLoadAcceptsWhatTheProjectRequires is the half of §5 a project sees first: the file
// says what it needs of the program, it loads, and the names reach the commands that act.
// Nothing of the machine is asked here — whether the program on it has those mechanisms is
// the question of the build, asked where a command acts and in `crewflow doctor`
// (docs.DESIGN.md §5, F-178).
func TestLoadAcceptsWhatTheProjectRequires(t *testing.T) {
	cfg, err := loadFile(t, "crewflow_requires.toml")
	if err != nil {
		t.Fatalf("Load(crewflow_requires.toml) returned an error: %v", err)
	}

	want := []string{"parallel-admission", "output-boundary"}
	if got := cfg.Crewflow.Requires; !slices.Equal(got, want) {
		t.Errorf("crewflow.requires = %v, want %v", got, want)
	}
}

// TestAProjectThatRequiresNothingLoads is what the defaults of the file are for: a project
// that says nothing about the program is a project crewflow can work with, as it was
// before the key was there (docs.DESIGN.md §5).
func TestAProjectThatRequiresNothingLoads(t *testing.T) {
	cfg, err := loadFile(t, "minimal.toml")
	if err != nil {
		t.Fatalf("Load(minimal.toml) returned an error: %v", err)
	}

	if got := cfg.Crewflow.Requires; len(got) != 0 {
		t.Errorf("crewflow.requires = %v of a project that says nothing, want none", got)
	}
}

// TestTheFileOfThisRepositoryDeclaresWhatItNeeds holds crewflow itself to the rule it
// writes for other projects: the project depends on mechanisms of the program that runs
// it — the admission of a pair, the refusal of a second run, the boundary of §7e, the
// separation of §7i — and it says so. A name in the file that no build has would make
// every command of this project refuse, and a mechanism of this repository that the file
// does not name is a dependency nobody is told about (F-178, docs.DESIGN.md §5).
func TestTheFileOfThisRepositoryDeclaresWhatItNeeds(t *testing.T) {
	path := strings.Join([]string{rootOfTheModule(t), "crewflow.toml"}, "/")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%s) returned an error: %v", path, err)
	}
	if len(cfg.Crewflow.Requires) == 0 {
		t.Fatalf("%s requires nothing, want the mechanisms the process of this project relies on", path)
	}
	for _, name := range cfg.Crewflow.Requires {
		if !buildinfo.Has(name) {
			t.Errorf("%s requires the capability %q, which no build of crewflow has: %v",
				path, name, buildinfo.Capabilities())
		}
	}
	for _, name := range []string{
		buildinfo.CapabilityParallelAdmission,
		buildinfo.CapabilityTaskRunGoing,
		buildinfo.CapabilityOutputBoundary,
		buildinfo.CapabilityIdentitySeparation,
		buildinfo.CapabilityCapabilityRequires,
	} {
		if !slices.Contains(cfg.Crewflow.Requires, name) {
			t.Errorf("%s does not require %q, want it: the process of this project relies on it",
				path, name)
		}
	}
}
