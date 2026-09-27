package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func loadFile(t *testing.T, name string) (Config, error) {
	t.Helper()
	return Load(filepath.Join("testdata", name))
}

// TestLoadExample loads the file DESIGN §5 tells a project to write and checks
// every field against the example, so that the loader and the design cannot
// drift apart.
func TestLoadExample(t *testing.T) {
	cfg, err := loadFile(t, "example.toml")
	if err != nil {
		t.Fatalf("Load(example.toml) returned an error: %v", err)
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"project.repo", cfg.Project.Repo, "naghuale/tele"},
		{"project.default_branch", cfg.Project.DefaultBranch, "main"},
		{"project.language", cfg.Project.Language, "ru"},
		{"forge.kind", cfg.Forge.Kind, "github"},
		{"forge.host", cfg.Forge.Host, ""},
		{"tracker.kind", cfg.Tracker.Kind, "forge"},
		{"tracker.project", cfg.Tracker.Project, ""},
		{
			"executor.command",
			cfg.Executor.Command,
			[]string{"opencode", "run", "--dir", "{worktree}", "--format", "json", "{prompt}"},
		},
		{"executor.model", cfg.Executor.Model, ""},
		{"executor.model_flag", cfg.Executor.ModelFlag, []string{"--model", "{model}"}},
		{"executor.timeout", cfg.Executor.Timeout, "90m"},
		{"len(executor.fallback)", len(cfg.Executor.Fallback), 0},
		{
			"requirements.tools",
			cfg.Requirements.Tools,
			[]Tool{
				{Name: "go", Check: []string{"go", "version"}, Min: "1.27"},
				{Name: "golangci-lint", Check: []string{"golangci-lint", "version"}},
				{Name: "libtdjson", Check: []string{"sh", "-c", `test -e "$TELECLI_TDLIB_LIBRARY"`}},
			},
		},
		{"isolation.mode", cfg.Isolation.Mode, "host"},
		{
			"capabilities",
			cfg.Capabilities,
			[]Capability{{
				Name:    "github-keys",
				Use:     []string{"gh"},
				Actions: []string{"gh ssh-key add", "gh gpg-key add"},
			}},
		},
		{"worktrees.root", cfg.Worktrees.Root, "~/.crewflow/worktrees/{repo}"},
		{
			"gates",
			cfg.Gates,
			[]Gate{
				{Name: "format", Run: []string{"sh", "-c", `test -z "$(gofmt -l .)"`}},
				{Name: "vet", Run: []string{"go", "vet", "./..."}},
				{Name: "test", Run: []string{"go", "test", "-race", "-count=1", "./..."}},
				{Name: "lint", Run: []string{"golangci-lint", "run", "./..."}},
			},
		},
		{"ci.kind", cfg.CI.Kind, "forge"},
		{"ci.required", cfg.CI.Required, true},
		{"ci.timeout", cfg.CI.Timeout, "30m"},
		{"merge.by", cfg.Merge.By, "orchestrator"},
		{"merge.strategy", cfg.Merge.Strategy, "ff-only"},
		{"merge.via", cfg.Merge.Via, "git-push"},
		{"parallel.max_tasks", cfg.Parallel.MaxTasks, 1},
		{"tasks.owner_approval", cfg.Tasks.OwnerApproval, "risky"},
	}
	for _, c := range checks {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s = %#v, want %#v", c.name, c.got, c.want)
		}
	}
}

// TestLoadMinimalAppliesDefaults checks the file with nothing but the two keys
// that have no default at all.
func TestLoadMinimalAppliesDefaults(t *testing.T) {
	cfg, err := loadFile(t, "minimal.toml")
	if err != nil {
		t.Fatalf("Load(minimal.toml) returned an error: %v", err)
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"project.default_branch", cfg.Project.DefaultBranch, "main"},
		{"project.language", cfg.Project.Language, "en"},
		{"executor.timeout", cfg.Executor.Timeout, "90m"},
		{"worktrees.root", cfg.Worktrees.Root, "~/.crewflow/worktrees/{repo}"},
		{"ci.required", cfg.CI.Required, true},
		{"ci.timeout", cfg.CI.Timeout, "30m"},
		{"merge.by", cfg.Merge.By, "orchestrator"},
		{"merge.strategy", cfg.Merge.Strategy, "ff-only"},
		{"parallel.max_tasks", cfg.Parallel.MaxTasks, 1},
		{"isolation.mode", cfg.Isolation.Mode, "host"},
		{"tasks.owner_approval", cfg.Tasks.OwnerApproval, "risky"},
	}
	for _, c := range checks {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s = %#v, want %#v", c.name, c.got, c.want)
		}
	}
}

// TestLoadOwnerApproval checks every value a project may put into
// tasks.owner_approval, so that the choice of §7f is spelled out in one place.
func TestLoadOwnerApproval(t *testing.T) {
	for _, want := range []string{"all", "risky", "none"} {
		t.Run(want, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "crewflow.toml")
			writeFile(t, path, `
[project]
repo = "naghuale/crewflow"

[executor]
command = ["agent", "run", "{prompt}"]

[tasks]
owner_approval = "`+want+`"
`)
			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("Load returned an error: %v", err)
			}
			if got := cfg.Tasks.OwnerApproval; got != want {
				t.Errorf("tasks.owner_approval = %q, want %q", got, want)
			}
		})
	}
}

// TestLoadCIRequiredFalse is the other way round: a key written as false must
// not be mistaken for a missing key and turned back into the default.
func TestLoadCIRequiredFalse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crewflow.toml")
	writeFile(t, path, `
[project]
repo = "naghuale/crewflow"

[executor]
command = ["agent", "run", "{prompt}"]

[ci]
required = false
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned an error: %v", err)
	}
	if cfg.CI.Required {
		t.Error("ci.required = true, want false")
	}
}

func TestLoadFallbackAppliesDefaultTimeout(t *testing.T) {
	cfg, err := loadFile(t, "fallback_defaults.toml")
	if err != nil {
		t.Fatalf("Load(fallback_defaults.toml) returned an error: %v", err)
	}
	if got, want := cfg.Executor.Fallback[0].Timeout, cfg.Executor.Timeout; got != want {
		t.Errorf("executor.fallback[0].timeout = %q, want %q", got, want)
	}
}

func TestLoadUnknownKey(t *testing.T) {
	_, err := loadFile(t, "unknown_key.toml")
	if err == nil {
		t.Fatal("Load(unknown_key.toml) returned no error, want an error naming the unknown keys")
	}
	for _, want := range []string{"unknown key", "project.reppo", "creatflow.enabled"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestLoadMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.toml")
	_, err := Load(path)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Load(missing.toml) error = %v, want one wrapping fs.ErrNotExist", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not name the file %q", err, path)
	}
}

func TestLoadMalformedTOML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crewflow.toml")
	writeFile(t, path, "[project\nrepo = 1")
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load of a file that is not TOML returned no error")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not name the file %q", err, path)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
