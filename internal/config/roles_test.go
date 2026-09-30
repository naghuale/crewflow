package config

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// baseConfig is what a project says at least: a repository and an executor. A
// case adds the keys of the roles it is about to this table and never writes it
// twice, because a file with two [executor] tables is not a file.
// noTask is what a kind of a role is expected to be refused with when the design
// describes it and no issue is written for it yet: the refusal names the key and
// the value and says that nobody waits for it.
const noTask = "-"

const baseConfig = `
[project]
repo = "naghuale/crewflow"

[executor]
command = ["agent", "run", "{prompt}"]
`

// TestLoadRoleKinds walks every kind of every role of DESIGN §7g, so that the
// list of what crewflow knows is spelled out in one place and not only in the
// file that checks it. A kind of a role crewflow has no adapter for is described
// in §7g and is not written, so a file that asks for it does not load: the load
// says so, with the task that writes it (docs/DESIGN.md §5).
func TestLoadRoleKinds(t *testing.T) {
	cases := []struct {
		table string
		key   string
		kinds map[string]string
		// besides is written next to the kind, for a kind that cannot stand on
		// its own: a project with no host would take its tasks in files and wait
		// only for its own gates.
		besides string
	}{
		{table: "forge", key: "kind", kinds: map[string]string{"github": ""}},
		{table: "forge", key: "kind", kinds: map[string]string{
			"gitlab": "#60", "bitbucket": "#61", "gitea": noTask, "azure": noTask, "none": "#59",
		}, besides: "\n[tracker]\nkind = \"files\"\n\n[ci]\nkind = \"none\"\n"},
		{table: "tracker", key: "kind", kinds: map[string]string{
			"forge": "", "jira": "#62", "linear": noTask, "files": "#59",
		}},
		{table: "ci", key: "kind", kinds: map[string]string{
			"forge": "", "jenkins": noTask, "command": "#59", "none": "",
		}},
		{table: "merge", key: "via", kinds: map[string]string{"git-push": "", "forge": "#58"}},
		// The mode of each of the two subjects is a choice crewflow does in every
		// one of its values: the login of the owner or an account of the host for
		// the executor, one login for both or an account of its own for the
		// orchestrator (docs/DESIGN.md §7i).
		{table: "identity", key: "mode", kinds: map[string]string{"owner": ""}},
		{table: "identity", key: "mode", kinds: map[string]string{"bot": ""},
			besides: "\n[identity.github_app]\napp_id = 5107052\n"},
		{table: "orchestrator", key: "mode", kinds: map[string]string{"shared": ""}},
		{table: "orchestrator", key: "mode", kinds: map[string]string{"separate": ""},
			besides: "\n[orchestrator.github_app]\napp_id = 5107053\n"},
	}
	for _, tc := range cases {
		t.Run(tc.table+"."+tc.key, func(t *testing.T) {
			for kind, task := range tc.kinds {
				t.Run(kind, func(t *testing.T) {
					path := filepath.Join(t.TempDir(), "crewflow.toml")
					writeFile(t, path, baseConfig+"\n["+tc.table+"]\n"+tc.key+" = \""+kind+"\""+tc.besides+"\n")
					_, err := Load(path)
					switch task {
					case "":
						if err != nil {
							t.Errorf("Load with %s.%s = %q returned an error: %v", tc.table, tc.key, kind, err)
						}
					case noTask:
						if err == nil {
							t.Fatalf("Load with %s.%s = %q returned no error, want the refusal of the registry",
								tc.table, tc.key, kind)
						}
						for _, want := range []string{tc.table + "." + tc.key, kind, "no issue is open"} {
							if !strings.Contains(err.Error(), want) {
								t.Errorf("error %q does not mention %q", err, want)
							}
						}
					default:
						if err == nil {
							t.Fatalf("Load with %s.%s = %q returned no error, want the refusal that names %s",
								tc.table, tc.key, kind, task)
						}
						for _, want := range []string{tc.table + "." + tc.key, kind, task} {
							if !strings.Contains(err.Error(), want) {
								t.Errorf("error %q does not mention %q", err, want)
							}
						}
					}
				})
			}
		})
	}
}

// TestLoadRoleDefaults checks the roles a project says nothing about: by default
// everything is on GitHub, as it was before the roles became three (§7g).
func TestLoadRoleDefaults(t *testing.T) {
	cfg, err := loadFile(t, "minimal.toml")
	if err != nil {
		t.Fatalf("Load(minimal.toml) returned an error: %v", err)
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"forge.kind", cfg.Forge.Kind, "github"},
		{"forge.host", cfg.Forge.Host, ""},
		{"tracker.kind", cfg.Tracker.Kind, "forge"},
		{"tracker.project", cfg.Tracker.Project, ""},
		{"ci.kind", cfg.CI.Kind, "forge"},
		{"merge.via", cfg.Merge.Via, "git-push"},
		// Both subjects of a project work under the login of the person until it
		// says otherwise (§7i).
		{"identity.mode", cfg.Identity.Mode, "owner"},
		{"orchestrator.mode", cfg.Orchestrator.Mode, "shared"},
	}
	for _, c := range checks {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s = %#v, want %#v", c.name, c.got, c.want)
		}
	}
}

// TestLoadRoleHostAndProject checks the two free strings of the roles: the
// project's own server, which the adapter of the host is given, and the key of
// the tracker, which only a tracker of its own would read. A key of the tracker
// in a file whose tracker is the forge promises something crewflow does not do,
// and the load refuses it (docs/DESIGN.md §5).
func TestLoadRoleHostAndProject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crewflow.toml")
	writeFile(t, path, baseConfig+`
[forge]
host = "github.company.com"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned an error: %v", err)
	}
	if cfg.Forge.Host != "github.company.com" {
		t.Errorf("forge.host = %q, want %q", cfg.Forge.Host, "github.company.com")
	}

	refused := filepath.Join(t.TempDir(), "crewflow.toml")
	writeFile(t, refused, baseConfig+`
[tracker]
project = "TELE"
`)
	_, err = Load(refused)
	if err == nil {
		t.Fatal("Load of a file with a key of the tracker returned no error, want the refusal of the registry")
	}
	if !strings.Contains(err.Error(), "tracker.project") {
		t.Errorf("error %q does not name the key of the tracker", err)
	}
}

// TestLoadRolesOfAProjectWithoutAForge walks the two pairs of keys that
// contradict each other: a project with no host has neither tasks nor checks to
// take from one.
func TestLoadRolesOfAProjectWithoutAForge(t *testing.T) {
	cases := []struct {
		file string
		want []string
	}{
		{"forge_none_tracker_forge.toml", []string{"tracker.kind", "forge.kind", "none"}},
		{"forge_none_ci_forge.toml", []string{"ci.kind", "forge.kind", "none"}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			_, err := loadFile(t, tc.file)
			if err == nil {
				t.Fatalf("Load(%s) returned no error, want one", tc.file)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

// TestLoadRolesWithoutAForge is the offline project of DESIGN §7g: no host at
// all, the tasks in files and only its own gates. It is described and crewflow
// has no adapter for it (#59), so the load says so instead of taking a file that
// promises a cycle the program cannot run.
func TestLoadRolesWithoutAForge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crewflow.toml")
	writeFile(t, path, baseConfig+`
[forge]
kind = "none"

[tracker]
kind = "files"

[ci]
kind = "none"
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load of a project without a host returned no error, want the refusal that names #59")
	}
	for _, want := range []string{"forge.kind", "tracker.kind", "crewflow#59"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}
