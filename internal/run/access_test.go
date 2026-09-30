package run

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/naghuale/crewflow/internal/forge"
)

// TestTheRunIsGivenWhatTheTaskAsks: the field of a task that says what it may read
// outside its work folder opens that folder to the executor and to nothing else, and
// only for the run of that task. The folder is opened for reading and closed for
// writing like every other folder of the policy, the assignment of the run carries the
// path with the hand that asked for it and the reason a person wrote, and the journal
// says the same (docs/DESIGN.md §7d, §7f).
func TestTheRunIsGivenWhatTheTaskAsks(t *testing.T) {
	m := newMachine(t)
	m.answers["opencode"] = answer{stdout: theRun}
	folders := folderOfTheTest(t, m, "sdk")
	host := &host{task: taskAskingFor(t, folders, "the task builds against the headers of C"), opened: true}
	cfg := projectOf(t, m.worktrees, "")

	result, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	rights := strings.Join(m.envOf(), "\n")
	for _, want := range []string{
		// Open for reading and closed for writing, as every folder of the policy is.
		`"` + folders + `/**":"allow"`,
		`"` + folders + `/**":"deny"`,
	} {
		if !strings.Contains(rights, want) {
			t.Errorf("the rights of the executor do not hold %q:\n%s", want, rights)
		}
	}
	// The assignment of the run shows the map of the access with the hand behind each
	// folder: a path on its own says nothing about who wanted it.
	asked := askedOf(t, m)
	for _, want := range []string{
		"read: " + folders + " — the task builds against the headers of C",
		"· task · the task builds against the headers of C",
	} {
		if !strings.Contains(asked, want) {
			t.Errorf("the assignment of the run does not hold %q:\n%s", want, asked)
		}
	}
	// And so does the journal, which is what a person reads afterwards.
	journal := journalOf(t, result)
	if want := folders + " · task · the task builds against the headers of C"; !strings.Contains(journal, want) {
		t.Errorf("the journal of the run does not hold %q:\n%s", want, journal)
	}
}

// TestTheFolderOfATaskIsNotOpenToTheNextOne: a folder one task needs is open for the
// run of that task and is not a folder of the project. The next task of the same
// project names its own dependencies in the file of the project, and a run of it that
// found the folder of another task open would be a run with more access than anybody
// asked for (docs/DESIGN.md §7d, §7f).
func TestTheFolderOfATaskIsNotOpenToTheNextOne(t *testing.T) {
	m := newMachine(t)
	m.answers["opencode"] = answer{stdout: theRun}
	folders := folderOfTheTest(t, m, "sdk")
	host := &host{task: taskOf(43), opened: true}
	cfg := projectOf(t, m.worktrees, "")

	// The task that asked for the folder is not the one that is run: its name is in
	// the field, the tracker holds a task without it.
	if _, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo}); err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	rights := strings.Join(m.envOf(), "\n")
	if strings.Contains(rights, folders) {
		t.Errorf("the rights of a run of a task that asked for nothing hold %q:\n%s", folders, rights)
	}
}

// TestARunOfATaskThatAsksForASecretDoesNotStart: the places of secrets are closed to
// the executor whatever the file of the project and whatever the task said, and a task
// that asks for one of them cannot be run: the run would be refused a permission in the
// middle of the work, with nothing committed. It is refused before the worktree of the
// task is made, so that nothing of the run is left behind and the next run of the task
// begins from nothing (docs/DESIGN.md §7d, §7f).
func TestARunOfATaskThatAsksForASecretDoesNotStart(t *testing.T) {
	m := newMachine(t)
	m.answers["opencode"] = answer{stdout: theRun}
	keys := filepath.Join(m.userHome, ".ssh")
	if err := os.MkdirAll(keys, 0o700); err != nil {
		t.Fatalf("make %s: %v", keys, err)
	}
	host := &host{task: taskAskingFor(t, keys, "the task wants to look at the keys"), opened: true}
	cfg := projectOf(t, m.worktrees, "")

	_, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})

	if err == nil {
		t.Fatal("Run returned no error, want the run of a task that asks for a secret to be refused")
	}
	for _, want := range []string{keys, "where secrets are", "take the path out of the task"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not hold %q", err.Error(), want)
		}
	}
	// Nothing of the run was started, and nothing of it was left behind: no worktree
	// was made, because the ask of the task is worked out before it is.
	if ran := m.commandsOf("opencode"); len(ran) != 0 {
		t.Errorf("the executor was run %d times, want no run at all", len(ran))
	}
	if made := m.commandsOf("git"); slices.ContainsFunc(made, func(command started) bool {
		return len(command.args) > 1 && command.args[0] == "worktree"
	}) {
		t.Errorf("git made a worktree of a run that never started: %v", m.lines())
	}
}

// TestARunOfAProjectWithRightsOfThePersonDoesNotStart: the rights of a run come from
// crewflow and from nothing else. OpenCode merges the config of the person with the
// settings of the run — a fact the test of the profile proves against the agent
// itself — so a config of the person with a table `permission` in it gives a run rights
// that were never written here, in a file that is not in the repository and not in the
// review of the task. Such a run is not started, and the file is named so that a
// person knows what to change (docs/DESIGN.md §7d).
func TestARunOfAProjectWithRightsOfThePersonDoesNotStart(t *testing.T) {
	cases := map[string]string{
		"a table of rights":           `{"permission":{"edit":{"**":"allow"}}}`,
		"a file crewflow cannot read": "{ this is not the JSON the agent reads",
	}
	for name, settings := range cases {
		t.Run(name, func(t *testing.T) {
			m := newMachine(t)
			m.answers["opencode"] = answer{stdout: theRun}
			globalConfigOfTheTest(t, m, settings)
			host := &host{task: taskOf(43), opened: true}
			cfg := projectOf(t, m.worktrees, "")

			_, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})

			if err == nil {
				t.Fatal("Run returned no error, want a run whose rights are not crewflow's alone to be refused")
			}
			for _, want := range []string{filepath.Join(m.userHome, ".config", "opencode", "opencode.json"), "rights of a run are written by crewflow alone"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal %q does not hold %q", err.Error(), want)
				}
			}
			if ran := m.commandsOf("opencode"); len(ran) != 0 {
				t.Errorf("the executor was run %d times, want no run at all", len(ran))
			}
		})
	}
}

// TestARunOfAMachineWithNoRightsOfThePersonGoesOn: a person with no config of the agent
// of their own has no rights to merge into the run, and the run of such a machine is
// not stopped for it. The check is about the file and not about the person, and a
// check that stops every run of a machine with nothing in it stops every run.
func TestARunOfAMachineWithNoRightsOfThePersonGoesOn(t *testing.T) {
	m := newMachine(t)
	m.answers["opencode"] = answer{stdout: theRun}
	// A config of the person that holds no table of rights: a model of their own is
	// not a permission, and a run is not stopped for it.
	globalConfigOfTheTest(t, m, `{"model":"someone/model"}`)
	host := &host{task: taskOf(43), opened: true}
	cfg := projectOf(t, m.worktrees, "")

	if _, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo}); err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	if ran := m.commandsOf("opencode"); len(ran) != 1 {
		t.Errorf("the executor was run %d times, want the one run of the task", len(ran))
	}
}

// TestAFolderOfTheProjectThatCannotBeOpenedIsSaidAndTheRunGoesOn: a path of the file
// of the project that crewflow will not open is not a run that failed. The run goes on
// without it and the way out of the run says what was named and why it was not opened,
// because a person who looks at a run that was refused a permission for a folder of a
// dependency has to see it (docs/DESIGN.md §7d).
func TestAFolderOfTheProjectThatCannotBeOpenedIsSaidAndTheRunGoesOn(t *testing.T) {
	m := newMachine(t)
	m.answers["opencode"] = answer{stdout: theRun}
	keys := filepath.Join(m.userHome, ".ssh")
	if err := os.MkdirAll(keys, 0o700); err != nil {
		t.Fatalf("make %s: %v", keys, err)
	}
	host := &host{task: taskOf(43), opened: true}
	cfg := projectOf(t, m.worktrees, "")
	cfg.Access.Read = []string{keys, filepath.Join(m.userHome, "go", "pkg", "mod")}
	if err := os.MkdirAll(cfg.Access.Read[1], 0o700); err != nil {
		t.Fatalf("make %s: %v", cfg.Access.Read[1], err)
	}

	result, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	said := read(t, result.ErrorJournal)
	if !strings.Contains(said, keys) || !strings.Contains(said, "where secrets are") {
		t.Errorf("the way out of the run = %q, want it to name the folder that was refused and why", said)
	}
	// The folder that could be opened is open, and the one that could not is not: the
	// run goes on with what the file of the project was really after.
	rights := strings.Join(m.envOf(), "\n")
	if !strings.Contains(rights, `"`+cfg.Access.Read[1]+`/**":"allow"`) {
		t.Errorf("the rights of the executor do not hold the folder that could be opened:\n%s", rights)
	}
}

// taskAskingFor is a whole task of a test with one line in the field that says what it
// may read outside its work folder: the folder and the reason a person wrote.
func taskAskingFor(t *testing.T, path, reason string) forge.Task {
	t.Helper()
	asked := taskOf(43)
	at := strings.Index(asked.Body, "nothing")
	if at < 0 {
		t.Fatalf("the body of a task has no reading field in it:\n%s", asked.Body)
	}
	asked.Body = asked.Body[:at] + "read: " + path + " — " + reason + "\n\n" + asked.Body[at+len("nothing"):]
	return asked
}

// folderOfTheTest is a folder of the machine of a test that a task may ask to read, and
// it is the one this machine holds it under: the policy of a run names a folder in both
// of the spellings of it, and a test looks for the one a person would write.
func folderOfTheTest(t *testing.T, m *machine, name string) string {
	t.Helper()
	path := filepath.Join(m.userHome, name)
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("make %s: %v", path, err)
	}
	return path
}

// globalConfigOfTheTest is the config of the person in the home of the test, which is
// where an agent of a run looks for it and where crewflow looks for the rights of a
// person that were never written by crewflow.
func globalConfigOfTheTest(t *testing.T, m *machine, settings string) {
	t.Helper()
	folder := filepath.Join(m.userHome, ".config", "opencode")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatalf("make %s: %v", folder, err)
	}
	path := filepath.Join(folder, "opencode.json")
	if err := os.WriteFile(path, []byte(settings), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// journalOf is what the executor of a run wrote and what crewflow wrote beside it.
func journalOf(t *testing.T, result Result) string {
	t.Helper()
	return read(t, result.Journal)
}
