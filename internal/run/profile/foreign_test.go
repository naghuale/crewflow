package profile

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestOpencodeMergesTheConfigOfThePerson is the fact the whole of the check of the
// rights of a run stands on: OpenCode merges the config of the person with the config
// of the run, and does not replace it. A `permission` of the person therefore reaches
// the run beside the one crewflow wrote, whatever crewflow puts into
// `OPENCODE_CONFIG_CONTENT`, and a run whose rights must be crewflow's alone cannot be
// started while that file holds one (docs/DESIGN.md §7d).
//
// The agent is asked about its own resolved settings, in a home of the test with a
// config of the person in it, and the answer is the truth about the agent of this
// machine. There is no stub for it: the question is what OpenCode does, and a test
// that answered it from a test would prove what the test believes.
//
// The check is skipped where the agent is not installed, and it needs no network and
// no key: `debug config` reads the files of the settings and prints them.
func TestOpencodeMergesTheConfigOfThePerson(t *testing.T) {
	agent, err := exec.LookPath("opencode")
	if err != nil {
		t.Skipf("opencode is not installed: %v", err)
	}
	home := t.TempDir()
	// A file of the person with a table of rights in it, in both spellings of a
	// pattern: a `**` of the person that allows everything, and a path of the run that
	// closes one place of secrets. The resolved settings hold the second, so the
	// rights of the run are written, and they hold the first, so the rights of the
	// person are merged with them and are not replaced.
	writeGlobal(t, home, `{
  "permission": {
    "edit": { "**": "allow" }
  }
}`)
	const runRights = `{"permission":{"edit":{"/Users/someone/.ssh/**":"deny"}}}`

	merged := debugConfig(t, agent, home, runRights)

	tables := permissionTables(t, merged, "edit")
	if !slices.Contains(tables, "/Users/someone/.ssh/**") {
		t.Errorf("the resolved rights of edit = %v, want the rule of the run among them", tables)
	}
	if !slices.Contains(tables, "**") {
		t.Errorf("the resolved rights of edit = %v, want the rule of the person (%q) merged into them, "+
			"and not replaced by the rule of the run", tables, "**")
	}
}

// TestOpencodeTakesTheConfigOfThePersonFromXDG: the agent looks in the folder it was
// told to look in before the folder of the home, so a person who moved their config
// is a person whose rights crewflow has to look for where they are and not where they
// usually are. A run that looked in one place only would miss a file in the other,
// and a file with rights of a person in it is the one thing a run may not merge with.
func TestOpencodeTakesTheConfigOfThePersonFromXDG(t *testing.T) {
	agent, err := exec.LookPath("opencode")
	if err != nil {
		t.Skipf("opencode is not installed: %v", err)
	}
	home, told := t.TempDir(), t.TempDir()
	// The file of the home says one thing and the folder the agent was told to look in
	// says another, and the second is the one the agent takes.
	writeGlobalIn(t, home, `{"permission":{"bash":"allow"}}`)
	writeGlobalIn(t, told, `{"permission":{"edit":{"**":"allow"}}}`)

	merged := debugConfig(t, agent, home, `{"permission":{"read":{"**/.env":"deny"}}}`,
		"XDG_CONFIG_HOME="+told)

	if tables := permissionTables(t, merged, "edit"); !slices.Contains(tables, "**") {
		t.Errorf("the resolved rights of edit = %v, want the rule of the config of the told folder", tables)
	}
	if tables := permissionTables(t, merged, "bash"); slices.Contains(tables, "**") {
		t.Errorf("the resolved rights of bash = %v, want the config of the home to be left out", tables)
	}
}

// writeGlobal is the config of the person in the folder the agent looks in when it was
// told nothing: `.config/opencode` of the home it is given.
func writeGlobal(t *testing.T, home, settings string) {
	t.Helper()
	writeConfigIn(t, filepath.Join(home, ".config", "opencode"), settings)
}

// writeGlobalIn is the config of the person in the folder `XDG_CONFIG_HOME` names, with
// the `opencode` the agent looks in under it.
func writeGlobalIn(t *testing.T, told, settings string) {
	t.Helper()
	writeConfigIn(t, filepath.Join(told, "opencode"), settings)
}

// writeConfigIn is a file of the settings of the person in the folder the agent reads
// them from, which is where a person goes and changes them.
func writeConfigIn(t *testing.T, folder, settings string) {
	t.Helper()
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatalf("make %s: %v", folder, err)
	}
	path := filepath.Join(folder, "opencode.json")
	if err := os.WriteFile(path, []byte(settings), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// debugConfig is what the agent says its resolved settings are, in a home of the test
// and with the rights of the run in the environment. Nothing of the machine of the
// person is read: the home of the test is the home the agent is given, and the
// settings of the person it finds there are the ones of the test.
func debugConfig(t *testing.T, agent, home, runRights string, environ ...string) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, agent, "debug", "config")
	// The whole environment of the process is replaced and not added to: a machine of a
	// test may hold anything, and a setting of it that names a folder of the person is
	// the one thing a test of this must not reach.
	cmd.Env = append([]string{
		"HOME=" + home,
		"PATH=" + os.Getenv("PATH"),
		configContent + "=" + runRights,
	}, environ...)
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("opencode debug config: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatalf("the settings of the agent are not JSON: %v\n%s", err, raw)
	}
	return settings
}

// permissionTables is the patterns of one table of rights in the settings the agent
// resolved, and not the rule of each of them: what matters here is which patterns are
// in the table at all, because that is what says whose rights a run is merged with.
func permissionTables(t *testing.T, settings map[string]any, table string) []string {
	t.Helper()
	permission, ok := settings["permission"].(map[string]any)
	if !ok {
		t.Fatalf("the settings of the agent hold no permission: %v", settings)
	}
	rules, ok := permission[table]
	if !ok {
		return nil
	}
	patterns, ok := rules.(map[string]any)
	if !ok {
		// A table that is a single word ("edit": "deny") has no patterns, and the
		// absence of any is what a test of the merge reads.
		return nil
	}
	found := make([]string, 0, len(patterns))
	for pattern := range patterns {
		found = append(found, pattern)
	}
	slices.Sort(found)
	return found
}

// TestForeignRightsFindsWhatTheAgentMerges is crewflow reading the same files the
// agent reads, with a temporary home and no keychain: a file of the person with rights
// in it is reported with the tables of it, a file without them is reported without
// any, and a file crewflow cannot read is reported as unreadable rather than as a
// machine with nothing in it.
func TestForeignRightsFindsWhatTheAgentMerges(t *testing.T) {
	home := t.TempDir()
	told := t.TempDir()
	// The config of the told folder holds a table of rights, the config of the home
	// holds none, and a file of nothing at all is left out of the answer.
	writeGlobalIn(t, told, `{"permission":{"edit":{"**":"allow"},"bash":"ask"}}`)
	writeGlobal(t, home, `{"model":"someone/model"}`)

	found := (opencode{}).ForeignRights(home, []string{"XDG_CONFIG_HOME=" + told})

	if len(found) != 2 {
		t.Fatalf("the configs of the person = %+v, want the one of the told folder and the one of the home", found)
	}
	if !slices.Equal(found[0].Tables, []string{"bash", "edit"}) {
		t.Errorf("the tables of %s = %v, want bash and edit", found[0].Path, found[0].Tables)
	}
	if len(found[1].Tables) != 0 || found[1].HoldsRights() {
		t.Errorf("the config %s has no rights of its own, want it reported without any", found[1].Path)
	}
	if !found[0].HoldsRights() {
		t.Error("a config with a table of rights in it does not hold rights, want it to")
	}
}

// TestForeignRightsOfAConfigCrewflowCannotRead: a file that is there and that
// crewflow cannot read is a file the rights of a run cannot be promised against, and
// it is said so rather than passed over as a file with nothing in it.
func TestForeignRightsOfAConfigCrewflowCannotRead(t *testing.T) {
	home := t.TempDir()
	writeGlobal(t, home, "{ this is not the JSON the agent reads")

	found := (opencode{}).ForeignRights(home, nil)

	if len(found) != 1 {
		t.Fatalf("the configs of the person = %+v, want the one of the home", found)
	}
	if found[0].Unreadable == "" {
		t.Errorf("the config %s is reported as read, want it reported as unreadable", found[0].Path)
	}
	if !found[0].HoldsRights() {
		t.Error("a config crewflow cannot read does not hold rights, want it to")
	}
}

// TestForeignRightsOfAGenericAgent: crewflow does not guess where an agent it has not
// run takes its rights from, and says nothing rather than naming a folder it does not
// know (docs/DESIGN.md §7b, §7d).
func TestForeignRightsOfAGenericAgent(t *testing.T) {
	home := t.TempDir()
	writeGlobal(t, home, `{"permission":{"edit":{"**":"allow"}}}`)

	if found := (generic{}).ForeignRights(home, nil); len(found) != 0 {
		t.Errorf("the configs of the person = %+v, want none: crewflow does not guess", found)
	}
}

// TestRightsAreReportedAsAFileAPersonChanges: the answer of a check is read by a
// person who goes and changes something, so it names the file as it is on this
// machine and the tables of it by the names the agent calls them by.
func TestRightsAreReportedAsAFileAPersonChanges(t *testing.T) {
	home := t.TempDir()
	writeGlobal(t, home, `{"permission":{"bash":"allow"}}`)

	found := (opencode{}).ForeignRights(home, nil)

	if len(found) != 1 {
		t.Fatalf("the configs of the person = %+v, want one", found)
	}
	if !strings.HasSuffix(found[0].Path, filepath.Join(".config", "opencode", "opencode.json")) {
		t.Errorf("the path of the config = %q, want the file as this machine holds it", found[0].Path)
	}
}
