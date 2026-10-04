package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
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
		// What the project needs of the program that runs it: the example writes three
		// mechanisms out, because the names of them are what `crewflow version` and
		// `crewflow doctor` show and what a command refuses without (F-178, §5).
		{
			"crewflow.requires",
			cfg.Crewflow.Requires,
			[]string{"parallel-admission", "task-run-going", "output-boundary"},
		},
		{"project.repo", cfg.Project.Repo, "naghuale/tele"},
		{"project.default_branch", cfg.Project.DefaultBranch, "main"},
		{"project.language", cfg.Project.Language, "ru"},
		{"forge.kind", cfg.Forge.Kind, "github"},
		{"forge.host", cfg.Forge.Host, ""},
		{"tracker.kind", cfg.Tracker.Kind, "forge"},
		{"tracker.project", cfg.Tracker.Project, ""},
		// The mode of the executor is the mode of the owner until a project says
		// otherwise: a project that sets up an app of its own has to write it down
		// (docs/DESIGN.md §7i).
		{"identity.mode", cfg.Identity.Mode, "owner"},
		{"identity.github_app.app_id", cfg.Identity.GitHubApp.AppID, int64(0)},
		// The orchestrator shares the login of the owner until a project sets up a
		// second app for it, and the example of §5 says so where a project reads it
		// (docs/DESIGN.md §7i).
		{"orchestrator.mode", cfg.Orchestrator.Mode, "shared"},
		{"orchestrator.github_app.app_id", cfg.Orchestrator.GitHubApp.AppID, int64(0)},
		{
			"executor.command",
			cfg.Executor.Command,
			[]string{"opencode", "run", "--dir", "{worktree}", "--format", "json", "{prompt}"},
		},
		{"executor.model", cfg.Executor.Model, ""},
		{"executor.model_flag", cfg.Executor.ModelFlag, []string{"--model", "{model}"}},
		{"executor.timeout", cfg.Executor.Timeout, "90m"},
		// A run of a project that says nothing about standing still is marked after
		// ten minutes of silence: an executor that works says something every few
		// minutes, and an hour of nothing is a run nobody is watching (docs/DESIGN.md §7a).
		{"executor.stall_after", cfg.Executor.StallAfter, "15m"},
		// The term of the checkpoint of a run that waits for a decision of a person is
		// a key of its own, and the example says the day it puts in a file that says
		// nothing (docs.DESIGN.md §7i).
		{"executor.resume_within", cfg.Executor.ResumeWithin, "24h"},
		// One try again after a refusal of the model provider, and one continuation of a
		// run that stands: both are policies a project sees and may change (AR-013, §6a, §7a).
		{"executor.provider_retries", cfg.Executor.ProviderRetries, 1},
		{"executor.resume_stands", cfg.Executor.ResumeStands, true},
		{"len(executor.fallback)", len(cfg.Executor.Fallback), 0},
		{
			"access.read_from",
			cfg.Access.ReadFrom,
			[][]string{{"go", "env", "GOMODCACHE"}, {"go", "env", "GOROOT"}},
		},
		{"access.read", cfg.Access.Read, []string{}},
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
		// The example comments [[capabilities]] out: a declaration of a capability
		// is a promise crewflow does not keep yet, and a file that makes it does
		// not load (docs/DESIGN.md §5, §7e).
		{"capabilities", cfg.Capabilities, []Capability(nil)},
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
		{"acceptance.label", cfg.Acceptance.Label, "owner-check"},
		{"merge.reviewers", cfg.Merge.Reviewers, []string{}},
		{"merge.owners", cfg.Merge.Owners, []string{}},
		{"parallel.max_tasks", cfg.Parallel.MaxTasks, 1},
		{"tasks.owner_approval", cfg.Tasks.OwnerApproval, "risky"},
		// The thresholds of the queue of attention are the ones §6a names, and the example
		// of §5 writes them out so that a project reads them where it reads the rest of
		// its settings and not in a task that needs them.
		{"attention.top_after", cfg.Attention.TopAfter, "30m"},
		{"attention.escalate_after", cfg.Attention.EscalateAfter, "24h"},
		{"attention.remind_after", cfg.Attention.RemindAfter, "24h"},
		{"attention.weekly_after", cfg.Attention.WeeklyAfter, "168h"},
		{"attention.deadline_after", cfg.Attention.DeadlineAfter, "30m"},
	}
	for _, c := range checks {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s = %#v, want %#v", c.name, c.got, c.want)
		}
	}
}

// TestLoadAttentionThresholds checks that the thresholds a project names are the ones the
// queue of attention is worked out on, and that a project which names one of them names
// it for itself: a queue of attention with the thresholds of another project in it is a
// queue that escalates too early or too late (docs/DESIGN.md §6a).
func TestLoadAttentionThresholds(t *testing.T) {
	cfg, err := loadFile(t, "attention_thresholds.toml")
	if err != nil {
		t.Fatalf("Load(attention_thresholds.toml) returned an error: %v", err)
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"attention.top_after", cfg.Attention.TopAfter, "15m"},
		{"attention.escalate_after", cfg.Attention.EscalateAfter, "12h"},
		{"attention.remind_after", cfg.Attention.RemindAfter, "6h"},
		{"attention.weekly_after", cfg.Attention.WeeklyAfter, "240h"},
		{"attention.deadline_after", cfg.Attention.DeadlineAfter, "20m"},
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
		{"executor.stall_after", cfg.Executor.StallAfter, "10m"},
		// A project that says nothing about how long a point of a run stays a point
		// gets a day: a person comes back to it within a day, and a point of a week ago
		// is a point about a machine and a task that are not the ones now (§7i).
		{"executor.resume_within", cfg.Executor.ResumeWithin, "24h"},
		// The two policies of a run that waits and of a run that stands: a project that
		// says nothing gets one try again after a refusal of the provider and one
		// continuation of a run that stands, and both are keys of its file (F-119, F-143,
		// §6a, §7a).
		{"executor.provider_retries", cfg.Executor.ProviderRetries, 1},
		{"executor.resume_stands", cfg.Executor.ResumeStands, true},
		// A project that says nothing about [access] gets no folder to read: the
		// executor of it works in its worktree and nowhere else, and a run does not
		// hand out a permission nobody asked for (docs/DESIGN.md §7d).
		{"access.read_from", cfg.Access.ReadFrom, [][]string(nil)},
		{"access.read", cfg.Access.Read, []string(nil)},
		{"worktrees.root", cfg.Worktrees.Root, "~/.crewflow/worktrees/{repo}"},
		{"ci.required", cfg.CI.Required, true},
		{"ci.timeout", cfg.CI.Timeout, "30m"},
		{"merge.by", cfg.Merge.By, "orchestrator"},
		{"merge.strategy", cfg.Merge.Strategy, "ff-only"},
		{"merge.via", cfg.Merge.Via, "git-push"},
		// The mode of the orchestrator is the shared login until a project says
		// otherwise, and the owners of a project are the owner of its repository
		// until it names them (docs/DESIGN.md §7i).
		{"orchestrator.mode", cfg.Orchestrator.Mode, "shared"},
		{"orchestrator.github_app.app_id", cfg.Orchestrator.GitHubApp.AppID, int64(0)},
		{"parallel.max_tasks", cfg.Parallel.MaxTasks, 1},

		{"isolation.mode", cfg.Isolation.Mode, "host"},
		{"tasks.owner_approval", cfg.Tasks.OwnerApproval, "risky"},
		// A project that says nothing about the acceptance of the owner gets the label
		// of §5, and the owners of its repository with it (docs/DESIGN.md §7h).
		{"acceptance.label", cfg.Acceptance.Label, "owner-check"},
	}
	for _, c := range checks {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s = %#v, want %#v", c.name, c.got, c.want)
		}
	}
}

// TestResumeWithin is the promise of `[executor] resume_within`: the term of the point a
// run of a task stands at while it waits for a decision of a person is a key of the file of
// the project. A project that names one gets it, a project that says nothing gets the day
// the load fills in, and a value that is not a term a person can wait out is refused with
// the name of the key (docs.DESIGN.md §5, §7i).
func TestResumeWithin(t *testing.T) {
	for _, tc := range []struct {
		name, file, want string
	}{
		{"the term of the example", "example.toml", "24h"},
		{"the default of a project that says nothing", "minimal.toml", "24h"},
		{"the term a project names", "resume_within_custom.toml", "2h"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := loadFile(t, tc.file)
			if err != nil {
				t.Fatalf("Load(%s) returned an error: %v", tc.file, err)
			}
			if got := cfg.Executor.ResumeWithin; got != tc.want {
				t.Errorf("executor.resume_within = %q, want %q", got, tc.want)
			}
			if DefaultResumeWithin != "24h" {
				t.Errorf("the default crewflow puts in a file that says nothing = %q, want %q",
					DefaultResumeWithin, "24h")
			}
		})
	}
}

// TestThePolicyOfARunThatWaitsAndOfARunThatStands is the promise of the two keys of §5:
// how many times crewflow goes on by itself after the model provider refused the run, and
// whether it goes on at all after a run that stands with a provider that has answered.
// Both are policies a project agrees to, and both are visible — a key of the file of a
// project, not a constant of the program (AR-013, F-119, F-143, docs.DESIGN.md §6a, §7a).
func TestThePolicyOfARunThatWaitsAndOfARunThatStands(t *testing.T) {
	t.Run("a project that says nothing", func(t *testing.T) {
		cfg, err := loadFile(t, "minimal.toml")
		if err != nil {
			t.Fatalf("Load(minimal.toml) returned an error: %v", err)
		}
		if cfg.Executor.ProviderRetries != 1 {
			t.Errorf("executor.provider_retries = %d, want 1: one try again, and the work is kept either way",
				cfg.Executor.ProviderRetries)
		}
		if !cfg.Executor.ResumeStands {
			t.Error("executor.resume_stands = false, want true: a run that stands is continued once")
		}
	})
	t.Run("a project that decides every refusal itself", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "crewflow.toml")
		writeFile(t, path, `
[project]
repo = "naghuale/crewflow"

[executor]
command = ["agent", "run", "{prompt}"]
provider_retries = 0
resume_stands = false
`)
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("Load returned an error: %v", err)
		}
		if cfg.Executor.ProviderRetries != 0 {
			t.Errorf("executor.provider_retries = %d, want 0: a key written as 0 is a decision and not a missing key",
				cfg.Executor.ProviderRetries)
		}
		if cfg.Executor.ResumeStands {
			t.Error("executor.resume_stands = true, want false: a key written as false is a decision and not a missing key")
		}
	})
	t.Run("a number of no sense", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "crewflow.toml")
		writeFile(t, path, `
[project]
repo = "naghuale/crewflow"

[executor]
command = ["agent", "run", "{prompt}"]
provider_retries = -1
`)
		_, err := Load(path)
		if err == nil {
			t.Fatal("Load returned no error, want the refusal of a negative number of tries")
		}
		for _, want := range []string{"executor.provider_retries", "-1"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %q", err, want)
			}
		}
	})
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

// TestLoadMergeLogins checks the two lists of the accounts whose records count: the
// reviewers of a change and the owners who accept the result of a task. A project may
// name one of them and leave the other out, and each one that is left out is the owner
// of the repository — reviewing a change and accepting what a person will see are two
// acts, and a project may give them to different people (docs/DESIGN.md §5, §7h).
func TestLoadMergeLogins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crewflow.toml")
	writeFile(t, path, `
[project]
repo = "naghuale/crewflow"

[executor]
command = ["agent", "run", "{prompt}"]

[merge]
reviewers = ["naghuale", "reviewer"]
owners = ["naghuale"]
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned an error: %v", err)
	}
	if want := []string{"naghuale", "reviewer"}; !reflect.DeepEqual(cfg.Merge.Reviewers, want) {
		t.Errorf("merge.reviewers = %#v, want %#v", cfg.Merge.Reviewers, want)
	}
	if want := []string{"naghuale"}; !reflect.DeepEqual(cfg.Merge.Owners, want) {
		t.Errorf("merge.owners = %#v, want %#v", cfg.Merge.Owners, want)
	}
}

// TestLoadCommitStyle checks the key that says how the messages of the commits of a
// project are written, and that a project which says nothing about it has none: the
// rule crewflow falls back on is the one that works in every repository
// (docs/DESIGN.md §5).
func TestLoadCommitStyle(t *testing.T) {
	t.Run("the style of the project", func(t *testing.T) {
		cfg, err := loadFile(t, "commit_style.toml")
		if err != nil {
			t.Fatalf("Load(commit_style.toml) returned an error: %v", err)
		}
		if got, want := cfg.Project.CommitStyle, "conventional: type(scope): subject"; got != want {
			t.Errorf("project.commit_style = %q, want %q", got, want)
		}
	})
	t.Run("a project that says nothing", func(t *testing.T) {
		cfg, err := loadFile(t, "minimal.toml")
		if err != nil {
			t.Fatalf("Load(minimal.toml) returned an error: %v", err)
		}
		if got := cfg.Project.CommitStyle; got != "" {
			t.Errorf("project.commit_style = %q, want none: the style of the last commits stands", got)
		}
	})
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

// TestTheContourOfTheFileIsWhatTheFileWrote: [environment] is optional, and the two cases
// are told apart in the config and not only in the answer of the resolver. A file that
// names a contour gives that word; a file that says nothing gives no word at all, and the
// contour of a legacy file is not dev — the runs of a project that never wrote it down are
// the runs it had before the key was there, and nothing reclassifies them.
func TestTheContourOfTheFileIsWhatTheFileWrote(t *testing.T) {
	t.Run("a project that names a contour", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "crewflow.toml")
		writeFile(t, path, `
[project]
repo = "naghuale/crewflow"

[executor]
command = ["agent", "run", "{prompt}"]

[environment]
contour = "test"
state_root = "~/spaces/test-state"
worktrees_root = "~/spaces/test-worktrees"
`)

		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("Load returned an error: %v", err)
		}

		want := Environment{
			Contour:       "test",
			StateRoot:     "~/spaces/test-state",
			WorktreesRoot: "~/spaces/test-worktrees",
		}
		if cfg.Environment != want {
			t.Errorf("environment = %#v, want %#v", cfg.Environment, want)
		}
		if cfg.Worktrees.Root != DefaultWorktreesRoot {
			t.Errorf("worktrees.root = %q, want %q: the contour keys do not move where the worktrees of a run are made",
				cfg.Worktrees.Root, DefaultWorktreesRoot)
		}
	})
	t.Run("a project that names no contour", func(t *testing.T) {
		cfg, err := loadFile(t, "minimal.toml")
		if err != nil {
			t.Fatalf("Load(minimal.toml) returned an error: %v", err)
		}

		if got := cfg.Environment.Contour; got != "" {
			t.Errorf("environment.contour = %q, want no word: the file named none and %q is not its contour",
				got, "dev")
		}
		if got := cfg.Environment; got != (Environment{}) {
			t.Errorf("environment = %#v, want the zero table of a file that says nothing", got)
		}
	})
}

// TestTheDefaultsDoNotInventAContour: defaults.go fills in what a file leaves out, and a
// contour is not what a file leaves out — it is a decision of a person about the machine
// the project is worked on. A default of dev would name the contour of every project of
// the world, and the classification of a legacy file would be a constant of the program.
func TestTheDefaultsDoNotInventAContour(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "minimal.toml"))
	if err != nil {
		t.Fatalf("read testdata/minimal.toml: %v", err)
	}
	var cfg Config
	meta, err := toml.Decode(string(data), &cfg)
	if err != nil {
		t.Fatalf("decode testdata/minimal.toml: %v", err)
	}

	cfg.applyDefaults(&meta)

	if got := cfg.Environment; got != (Environment{}) {
		t.Errorf("environment after the defaults = %#v, want the zero table: a contour is written, not filled in", got)
	}
}

// TestApplyDefaultsGivesAFallbackTheLimitsOfTheExecutor holds both rules of
// defaults.go about a fallback executor, which the load no longer reaches: a
// non-empty fallback is a promise of §7b that crewflow does not keep yet, so the
// file is refused (docs/DESIGN.md §5). The rules themselves stay with the task that
// writes the fallback, and this test is what holds them to what they say.
func TestApplyDefaultsGivesAFallbackTheLimitsOfTheExecutor(t *testing.T) {
	for _, tc := range []struct {
		file    string
		applied func(Config) (string, string)
		key     string
	}{
		{
			file: "fallback_defaults.toml", key: "executor.fallback[0].timeout",
			applied: func(c Config) (string, string) { return c.Executor.Fallback[0].Timeout, c.Executor.Timeout },
		},
		{
			// The silence a run is marked at is a limit of the run and not of the
			// agent in it (#67), so a fallback says nothing about it and gets the one
			// of the executor it stands in for.
			file: "stall_after_defaults.toml", key: "executor.fallback[0].stall_after",
			applied: func(c Config) (string, string) { return c.Executor.Fallback[0].StallAfter, c.Executor.StallAfter },
		},
	} {
		t.Run(tc.file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", tc.file))
			if err != nil {
				t.Fatalf("read testdata/%s: %v", tc.file, err)
			}
			var cfg Config
			meta, err := toml.Decode(string(data), &cfg)
			if err != nil {
				t.Fatalf("decode testdata/%s: %v", tc.file, err)
			}

			cfg.applyDefaults(&meta)

			if got, want := tc.applied(cfg); got != want {
				t.Errorf("%s = %q, want %q", tc.key, got, want)
			}
		})
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
