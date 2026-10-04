package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadRejects walks one file per rule of Validate and checks that the error
// names the key that is wrong, not just that something is wrong.
func TestLoadRejects(t *testing.T) {
	cases := []struct {
		file string
		want []string
	}{
		{"repo_no_slash.toml", []string{"project.repo", "owner/name"}},
		{"repo_empty.toml", []string{"project.repo", "owner/name"}},
		{"command_empty.toml", []string{"executor.command"}},
		{"command_no_prompt.toml", []string{"executor.command", "{prompt}"}},
		{"command_unknown_placeholder.toml", []string{"executor.command", "{repo}"}},
		{"model_flag_without_model.toml", []string{"executor.model_flag", "{model}"}},
		{"executor_timeout_invalid.toml", []string{"executor.timeout", "soon"}},
		{"executor_timeout_zero.toml", []string{"executor.timeout", "0s"}},
		{"executor_stall_after_invalid.toml", []string{"executor.stall_after", "ten minutes"}},
		{"executor_stall_after_zero.toml", []string{"executor.stall_after", "0s"}},
		{"executor_resume_within_invalid.toml", []string{"executor.resume_within", "a couple of days"}},
		{"executor_resume_within_zero.toml", []string{"executor.resume_within", "0s", "must be positive"}},
		{"ci_timeout_invalid.toml", []string{"ci.timeout", "30"}},
		{"gate_name_empty.toml", []string{"gates[0].name"}},
		{"gate_name_duplicate.toml", []string{"gates[1].name", "test"}},
		{"gate_run_empty.toml", []string{"gates[0].run"}},
		{"merge_by_unknown.toml", []string{"merge.by", "robot", "orchestrator"}},
		{"merge_strategy_unknown.toml", []string{"merge.strategy", "squash", "ff-only"}},
		{"merge_via_unknown.toml", []string{"merge.via", "web", "git-push"}},
		{"merge_reviewer_empty.toml", []string{"merge.reviewers[0]", "naghuale"}},
		{"merge_reviewer_duplicate.toml", []string{"merge.reviewers[1]", "naghuale"}},
		{"merge_reviewer_mention.toml", []string{"merge.reviewers[0]", "@"}},
		{"merge_owner_empty.toml", []string{"merge.owners[0]", "naghuale"}},
		{"merge_owner_duplicate.toml", []string{"merge.owners[1]", "naghuale"}},
		{"merge_owner_mention.toml", []string{"merge.owners[0]", "@"}},
		{
			"forge_kind_unknown.toml",
			[]string{"forge.kind", "bogus", "github, gitlab, bitbucket, gitea, azure, none"},
		},
		{"tracker_kind_unknown.toml", []string{"tracker.kind", "backlog", "forge, jira, linear, files"}},
		{"ci_kind_unknown.toml", []string{"ci.kind", "teamcity", "forge, jenkins, command, none"}},
		{"parallel_max_tasks_zero.toml", []string{"parallel.max_tasks", "0"}},
		{"isolation_mode_unknown.toml", []string{"isolation.mode", "vm", "host"}},
		{"network_proxy_type_unknown.toml", []string{"network.proxies.home.type", "socks4", "http, https, socks5"}},
		{"network_proxy_port_zero.toml", []string{"network.proxies.home.port", "must be a port from 1 to 65535"}},
		{
			"network_proxy_host_with_credentials.toml",
			[]string{"network.proxies.home.host", "scheme", "store of secrets"},
		},
		{"network_active_proxy_unknown.toml", []string{"network.active_proxy", "office"}},
		{"requirements_tool_duplicate.toml", []string{"requirements.tools[1].name", "go"}},
		{"capabilities_duplicate.toml", []string{"capabilities[1].name", "github-keys"}},
		{"crewflow_requires_empty_name.toml", []string{"crewflow.requires[1]", "empty name"}},
		{"crewflow_requires_spaces.toml", []string{"crewflow.requires[0]", "parallel-admission", "one word"}},
		{"crewflow_requires_duplicate.toml", []string{"crewflow.requires[2]", "duplicate capability", "parallel-admission"}},
		{"fallback_command_no_prompt.toml", []string{"executor.fallback[0].command", "{prompt}"}},
		{"fallback_timeout_invalid.toml", []string{"executor.fallback[0].timeout", "half an hour"}},
		{"tasks_owner_approval_unknown.toml", []string{"tasks.owner_approval", "sometimes", "risky"}},
		{"access_read_from_empty.toml", []string{"access.read_from[0]", "prints"}},
		{"access_read_empty.toml", []string{"access.read[0]", "a path on the machine"}},
		{"identity_mode_unknown.toml", []string{"identity.mode", "root", "owner, bot"}},
		{"identity_bot_without_app.toml", []string{"identity.github_app.app_id", "settings of GitHub"}},
		{"identity_bot_app_id_zero.toml", []string{"identity.github_app.app_id"}},
		{
			"identity_bot_without_a_forge.toml",
			[]string{"identity.mode", "GitHub App", `"none"`},
		},
		{"orchestrator_mode_unknown.toml", []string{"orchestrator.mode", "owner", "shared, separate"}},
		{
			"orchestrator_separate_without_app.toml",
			[]string{"orchestrator.github_app.app_id", "settings of GitHub"},
		},
		{"orchestrator_separate_app_id_zero.toml", []string{"orchestrator.github_app.app_id"}},
		{
			"orchestrator_separate_without_a_forge.toml",
			[]string{"orchestrator.mode", "GitHub App", `"none"`},
		},
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

// TestLoadAccepts checks that the two valid files pass Load, which validates.
func TestLoadAccepts(t *testing.T) {
	for _, name := range []string{
		"example.toml", "minimal.toml", "identity_bot.toml", "orchestrator_separate.toml",
		"attention_thresholds.toml", "resume_within_custom.toml", "network_proxy.toml",
		"crewflow_requires.toml",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadFile(t, name); err != nil {
				t.Errorf("Load(%s) returned an error: %v", name, err)
			}
		})
	}
}

// headOfAProject is the part of a file every case below is a file of a project with: the
// two keys that have no default at all. The cases of [environment] are written here and
// not as files of testdata, because testdata is not among the files this task may touch
// and a refusal is about the words a person wrote, not about a name of a fixture.
const headOfAProject = `
[project]
repo = "naghuale/crewflow"

[executor]
command = ["agent", "run", "{prompt}"]
`

// TestLoadRefusesTheEnvironmentOfAProject walks the rules of the contour keys: the closed
// list of the words, and the rules of a root a project names for itself. A file with a
// mistake in any of them is refused here, where the person who wrote it reads what to
// write instead, and not in the middle of a task.
func TestLoadRefusesTheEnvironmentOfAProject(t *testing.T) {
	cases := []struct {
		name   string
		table  string
		want   []string
		absent []string
	}{
		{
			name:  "a word of another vocabulary",
			table: "[environment]\ncontour = \"staging\"\n",
			want:  []string{"environment.contour", `"staging"`, "dev, test, prod"},
		},
		{
			name:  "a word with a space around it",
			table: "[environment]\ncontour = \" dev \"\n",
			want:  []string{"environment.contour", `"dev"`, "dev, test, prod"},
			// The refusal carries the cleaned word and nothing else: the words of a file
			// are not a place for a value as it was typed.
			absent: []string{`" dev "`},
		},
		{
			name:  "a state root that climbs out",
			table: "[environment]\ncontour = \"dev\"\nstate_root = \"~/spaces/../etc\"\n",
			want:  []string{"environment.state_root", "climbs out"},
		},
		{
			name:  "a worktrees root that climbs out",
			table: "[environment]\ncontour = \"test\"\nworktrees_root = \"~/../etc\"\n",
			want:  []string{"environment.worktrees_root", "climbs out"},
		},
		{
			name:  "a state root of the working directory of a command",
			table: "[environment]\ncontour = \"dev\"\nstate_root = \"spaces/state\"\n",
			want:  []string{"environment.state_root", "absolute", `"~/"`},
		},
		{
			name:  "a worktrees root of the working directory of a command",
			table: "[environment]\ncontour = \"prod\"\nworktrees_root = \"worktrees\"\n",
			want:  []string{"environment.worktrees_root", "absolute", `"~/"`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "crewflow.toml")
			writeFile(t, path, headOfAProject+"\n"+tc.table)

			_, err := Load(path)

			if err == nil {
				t.Fatalf("Load of a file with %s returned no error, want the refusal of the key", tc.name)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(err.Error(), absent) {
					t.Errorf("error %q carries %q, want it out", err, absent)
				}
			}
		})
	}
}

// TestLoadAcceptsTheContourOfAProject: every word of the closed list is a file crewflow can
// work with, with the roots of the project and without them. Naming prod is a word a file
// may hold and not an admission to it: what the machine and the program do with that word
// is a task of its own, and this one only reads it and checks the folders around it.
func TestLoadAcceptsTheContourOfAProject(t *testing.T) {
	for _, contour := range []string{"dev", "test", "prod"} {
		t.Run(contour, func(t *testing.T) {
			t.Run("with the roots of the project", func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "crewflow.toml")
				writeFile(t, path, headOfAProject+"\n[environment]\ncontour = \""+contour+
					"\"\nstate_root = \"~/spaces/state\"\nworktrees_root = \"/var/crewflow/worktrees\"\n")

				cfg, err := Load(path)
				if err != nil {
					t.Fatalf("Load returned an error: %v", err)
				}

				if cfg.Environment.Contour != contour {
					t.Errorf("environment.contour = %q, want %q", cfg.Environment.Contour, contour)
				}
				if cfg.Worktrees.Root != DefaultWorktreesRoot {
					t.Errorf("worktrees.root = %q, want %q: [environment] does not route a run",
						cfg.Worktrees.Root, DefaultWorktreesRoot)
				}
			})
			t.Run("without a word of roots", func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "crewflow.toml")
				writeFile(t, path, headOfAProject+"\n[environment]\ncontour = \""+contour+"\"\n")

				cfg, err := Load(path)
				if err != nil {
					t.Fatalf("Load returned an error: %v", err)
				}

				if cfg.Environment.StateRoot != "" || cfg.Environment.WorktreesRoot != "" {
					t.Errorf("the roots = %#v, want none: crewflow proposes them and writes no word into the file",
						cfg.Environment)
				}
			})
			t.Run("with empty roots", func(t *testing.T) {
				// A key of a string that a file leaves out and a key a file writes empty
				// are the same word in a Config, and every key of a string in this file
				// reads that word as "the project names none". An empty root is
				// therefore the root crewflow proposes, and the resolver says of it that
				// the file named it not.
				path := filepath.Join(t.TempDir(), "crewflow.toml")
				writeFile(t, path, headOfAProject+"\n[environment]\ncontour = \""+contour+
					"\"\nstate_root = \"\"\nworktrees_root = \"  \"\n")

				cfg, err := Load(path)
				if err != nil {
					t.Fatalf("Load returned an error: %v", err)
				}

				if cfg.Environment.StateRoot != "" || cfg.Environment.WorktreesRoot != "  " {
					t.Errorf("the roots = %#v, want the words of the file as they were written", cfg.Environment)
				}
				layout, err := cfg.ResolveContour(t.TempDir())
				if err != nil {
					t.Fatalf("ResolveContour returned an error: %v", err)
				}
				if layout.StateNamed || layout.WorktreesNamed {
					t.Errorf("a root is named, want none: %#v", layout)
				}
				if want := filepath.Join(layout.Home, ".crewflow", contour, "state", "naghuale-crewflow"); layout.State != want {
					t.Errorf("the state root = %q, want the one crewflow proposes (%q)", layout.State, want)
				}
			})
		})
	}
}
