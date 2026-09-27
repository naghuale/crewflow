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
const baseConfig = `
[project]
repo = "naghuale/crewflow"

[executor]
command = ["agent", "run", "{prompt}"]
`

// TestLoadRoleKinds walks every kind of every role of DESIGN §7g, so that the
// list of what crewflow knows is spelled out in one place and not only in the
// file that checks it.
func TestLoadRoleKinds(t *testing.T) {
	cases := []struct {
		table string
		key   string
		kinds []string
		// besides is written next to the kind, for a kind that cannot stand on
		// its own: a project with no host takes its tasks in files and waits
		// only for its own gates.
		besides string
	}{
		{table: "forge", key: "kind", kinds: []string{"github", "gitlab", "bitbucket", "gitea", "azure"}, besides: ""},
		{table: "forge", key: "kind", kinds: []string{"none"}, besides: "\n[tracker]\nkind = \"files\"\n\n[ci]\nkind = \"none\"\n"},
		{table: "tracker", key: "kind", kinds: []string{"forge", "jira", "linear", "files"}},
		{table: "ci", key: "kind", kinds: []string{"forge", "jenkins", "command", "none"}},
		{table: "merge", key: "via", kinds: []string{"git-push", "forge"}},
	}
	for _, tc := range cases {
		t.Run(tc.table+"."+tc.key, func(t *testing.T) {
			for _, kind := range tc.kinds {
				t.Run(kind, func(t *testing.T) {
					path := filepath.Join(t.TempDir(), "crewflow.toml")
					writeFile(t, path, baseConfig+"\n["+tc.table+"]\n"+tc.key+" = \""+kind+"\""+tc.besides+"\n")
					if _, err := Load(path); err != nil {
						t.Errorf("Load with %s.%s = %q returned an error: %v", tc.table, tc.key, kind, err)
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
	}
	for _, c := range checks {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s = %#v, want %#v", c.name, c.got, c.want)
		}
	}
}

// TestLoadRoleHostAndProject checks the two free strings of the roles: the
// project's own server and the key of the tracker, which crewflow only carries
// and does not judge.
func TestLoadRoleHostAndProject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crewflow.toml")
	writeFile(t, path, baseConfig+`
[forge]
kind = "gitlab"
host = "gitlab.company.com"

[tracker]
kind = "jira"
project = "TELE"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned an error: %v", err)
	}
	if cfg.Forge.Host != "gitlab.company.com" {
		t.Errorf("forge.host = %q, want %q", cfg.Forge.Host, "gitlab.company.com")
	}
	if cfg.Tracker.Project != "TELE" {
		t.Errorf("tracker.project = %q, want %q", cfg.Tracker.Project, "TELE")
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

// TestLoadRolesWithoutAForge is the other side of the same two keys: a project
// without a host that takes its tasks in files and waits only for its own gates
// is a project crewflow can work with.
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
	if _, err := Load(path); err != nil {
		t.Errorf("Load of a project without a host returned an error: %v", err)
	}
}
