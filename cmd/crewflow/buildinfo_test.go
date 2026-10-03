package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/naghuale/crewflow/internal/buildinfo"
	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/forge"
)

// commandsThatRefuse are the commands of §5, each with the words its refusal begins with.
// The list is closed: a command that creates nothing and shows what is there is not in it,
// and a command added to it has to come with a test here (docs.DESIGN.md §5, §7i, F-178).
var commandsThatRefuse = []struct {
	name string
	args []string
	says string
}{
	{"task run", []string{"task", "run", "43", "-config"}, "crewflow task:"},
	{"task run -continue", []string{"task", "run", "43", "-continue", "go on", "-config"}, "crewflow task:"},
	{"task resume", []string{"task", "resume", "43", "-config"}, "crewflow task:"},
	{"review", []string{"review", "43", "-config"}, "crewflow review:"},
	{"merge", []string{"merge", "43", "-config"}, "crewflow merge:"},
}

// TestACommandRefusesWhatThisBuildHasNot is the whole of §5 seen from the command: the file
// of the project names a mechanism, the build on the machine has not got it, and every
// command that would act refuses with the word of the refusal — and does nothing at all
// before it says so.
//
// The name required here is one no build of crewflow has. That is not a guess of a test: it
// is exactly what an installed program of a version before a mechanism was written sees in
// the file of a project that requires it, and what a project of tomorrow sees in the file
// of a build of today (F-178).
func TestACommandRefusesWhatThisBuildHasNot(t *testing.T) {
	const required = "mechanism-of-a-build-that-has-not-been-written"
	for _, command := range commandsThatRefuse {
		t.Run(command.name, func(t *testing.T) {
			h := &host{task: taskOf(43)}
			h.use(t)
			project := h.configAs(t, taskConfig+"\n[crewflow]\nrequires = [\""+required+"\"]\n")
			args := append(append([]string{}, command.args...), project)
			var stdout, stderr bytes.Buffer

			code := run(args, &stdout, &stderr)

			if code != exitFailure {
				t.Errorf("`%s` = %d, want %d: the build has not the mechanism the project requires",
					strings.Join(command.args, " "), code, exitFailure)
			}
			for _, want := range []string{command.says, buildinfo.ReasonCapabilityMissing, required} {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("`%s` wrote %q to stderr, want it to mention %q",
						strings.Join(command.args, " "), stderr.String(), want)
				}
			}
			if stdout.Len() != 0 {
				t.Errorf("`%s` wrote %q to stdout, want a refusal and nothing else",
					strings.Join(command.args, " "), stdout.String())
			}
			// Nothing of the machine was touched. The roles of the project are what read a
			// key of an App, and a refusal that comes after that stands a person in front of
			// a window of the keychain of macOS nobody came to answer (docs.DESIGN.md §7i).
			if len(h.started) != 0 {
				t.Errorf("the executor was started %d times, want nothing to be started", len(h.started))
			}
			if entries := leftBehind(t, h.home); len(entries) != 0 {
				t.Errorf("the refusal left %v in the home of crewflow, want nothing: a refusal makes nothing",
					entries)
			}
		})
	}
}

// TestTheRolesAreNotBuiltForARefusedCommand watches the one step that cannot be seen from
// the outside: the refusal comes before the roles of the project are built, so no key of an
// App is read and no window of the system opens for a run that was never going to happen
// (docs.DESIGN.md §7i).
func TestTheRolesAreNotBuiltForARefusedCommand(t *testing.T) {
	h := &host{task: taskOf(43)}
	h.use(t)
	project := h.configAs(t, taskConfig+"\n[crewflow]\nrequires = [\"no-build-has-this\"]\n")
	built := 0
	taskRoles, reviewRoles, mergeRoles = counted(&built), counted(&built), counted(&built)
	var stdout, stderr bytes.Buffer

	code := run([]string{"task", "run", "43", "-config", project}, &stdout, &stderr)

	if code != exitFailure {
		t.Fatalf("`task run` = %d, want %d", code, exitFailure)
	}
	if built != 0 {
		t.Errorf("the roles of the project were built %d times, want none of them before the refusal", built)
	}
}

// TestTheRefusalIsTheOneOfTheBuild is what a person and an orchestrator read: the words come
// out of the package of the build and are not written out again by the command, so the word a
// project switches on is the one the code holds (docs.DESIGN.md §5).
func TestTheRefusalIsTheOneOfTheBuild(t *testing.T) {
	h := &host{task: taskOf(43)}
	h.use(t)
	project := h.configAs(t, taskConfig+"\n[crewflow]\nrequires = [\n  \"one-of-another-version\",\n  \"another-one\",\n]\n")
	var stdout, stderr bytes.Buffer

	code := run([]string{"task", "run", "43", "-config", project}, &stdout, &stderr)

	if code != exitFailure {
		t.Fatalf("`task run` = %d, want %d", code, exitFailure)
	}
	for _, want := range []string{"one-of-another-version, another-one", buildinfo.CapabilityParallelAdmission} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("`task run` wrote %q to stderr, want it to mention %q", stderr.String(), want)
		}
	}
}

// TestACommandThatOnlyShowsIsNotRefused is the other side of the closed list: a command that
// creates nothing answers what is there, and its answer cannot be mistaken for a promise. A
// person whose machine is older than the project has to be able to look at the readiness of
// a task and at the queue of attention all the same (docs.DESIGN.md §5, §6a).
func TestACommandThatOnlyShowsIsNotRefused(t *testing.T) {
	h := &host{task: taskOf(43)}
	h.use(t)
	project := h.configAs(t, taskConfig+"\n[crewflow]\nrequires = [\"no-build-has-this\"]\n")
	var stdout, stderr bytes.Buffer

	code := run([]string{"task", "check", "43", "-config", project}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("`task check` = %d, want %d (stdout: %q, stderr: %q)", code, exitOK, stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), buildinfo.ReasonCapabilityMissing) {
		t.Errorf("`task check` wrote %q to stderr, want the answer of the check and no refusal of the build", stderr.String())
	}
	if !strings.Contains(stdout.String(), "ready") {
		t.Errorf("`task check` wrote %q to stdout, want the answer about the task", stdout.String())
	}
}

// TestAProjectThatRequiresWhatTheBuildHasIsWorkedOnAsBefore keeps the mechanism from becoming
// a refusal of everything: a project whose file names what the program on the machine can do
// is worked on as it was before the key was there (docs.DESIGN.md §5).
func TestAProjectThatRequiresWhatTheBuildHasIsWorkedOnAsBefore(t *testing.T) {
	h := &host{task: taskOf(43)}
	h.use(t)
	project := h.configAs(t, taskConfig+"\n[crewflow]\nrequires = [\""+
		buildinfo.CapabilityParallelAdmission+"\"]\n")
	var stdout, stderr bytes.Buffer

	code := run([]string{"task", "check", "43", "-config", project}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("`task check` = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
}

// counted is the roles of a project that count how often a command asked for them: a test
// that watches the step before the refusal does not care what the roles are.
func counted(asked *int) func(config.Config, forge.Env) (forge.Set, error) {
	return func(config.Config, forge.Env) (forge.Set, error) {
		*asked++
		return forge.Set{}, nil
	}
}

// leftBehind is what a home of crewflow holds after a command that was refused before it did
// anything: nothing, and a refusal that leaves a state of a task or a journal behind is not
// the refusal §5 promises.
func leftBehind(t *testing.T, home string) []string {
	t.Helper()
	var names []string
	for _, folder := range []string{"runs", "state", "grants"} {
		entries, err := os.ReadDir(filepath.Join(home, folder))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("read %s of the home of the test: %v", folder, err)
		}
		for _, entry := range entries {
			names = append(names, folder+"/"+entry.Name())
		}
	}
	return names
}
