package config

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// stubHome points "~/" expansion at dir for the duration of the test, so that no
// test ever reads the home directory of the machine it runs on.
func stubHome(t *testing.T, dir string) {
	t.Helper()
	previous := homeDir
	homeDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { homeDir = previous })
}

func TestExpandPath(t *testing.T) {
	home := t.TempDir()
	stubHome(t, home)
	cfg := Config{Project: Project{Repo: "naghuale/crewflow"}}

	cases := []struct {
		name string
		path string
		want string
	}{
		{
			"home and repo placeholder",
			"~/.crewflow/worktrees/{repo}",
			filepath.Join(home, ".crewflow", "worktrees", "naghuale-crewflow"),
		},
		{"home only", "~/projects", filepath.Join(home, "projects")},
		{"repo placeholder only", "/var/tmp/{repo}", "/var/tmp/naghuale-crewflow"},
		{"relative path", "worktrees/{repo}", "worktrees/naghuale-crewflow"},
		{"nothing to expand", "/var/tmp", "/var/tmp"},
		{"another user's home is not ours", "~someone/else", "~someone/else"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := cfg.ExpandPath(tc.path)
			if err != nil {
				t.Fatalf("ExpandPath(%q) returned an error: %v", tc.path, err)
			}
			if got != tc.want {
				t.Errorf("ExpandPath(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestExpandPathWorktreesRoot(t *testing.T) {
	home := t.TempDir()
	stubHome(t, home)

	cfg, err := loadFile(t, "minimal.toml")
	if err != nil {
		t.Fatalf("Load(minimal.toml) returned an error: %v", err)
	}
	got, err := cfg.ExpandPath(cfg.Worktrees.Root)
	if err != nil {
		t.Fatalf("ExpandPath returned an error: %v", err)
	}
	want := filepath.Join(home, ".crewflow", "worktrees", "naghuale-crewflow")
	if got != want {
		t.Errorf("worktrees root = %q, want %q", got, want)
	}
}

func TestExpandPathHomeUnavailable(t *testing.T) {
	previous := homeDir
	homeDir = func() (string, error) { return "", errors.New("no home here") }
	t.Cleanup(func() { homeDir = previous })

	cfg := Config{Project: Project{Repo: "naghuale/crewflow"}}
	_, err := cfg.ExpandPath("~/.crewflow")
	if err == nil {
		t.Fatal("ExpandPath returned no error, want one")
	}
	if !strings.Contains(err.Error(), "no home here") {
		t.Errorf("error %q does not explain why the path could not be expanded", err)
	}
}
