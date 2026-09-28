package config

import (
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
		{"ci_timeout_invalid.toml", []string{"ci.timeout", "30"}},
		{"gate_name_empty.toml", []string{"gates[0].name"}},
		{"gate_name_duplicate.toml", []string{"gates[1].name", "test"}},
		{"gate_run_empty.toml", []string{"gates[0].run"}},
		{"merge_by_unknown.toml", []string{"merge.by", "robot", "orchestrator"}},
		{"merge_strategy_unknown.toml", []string{"merge.strategy", "squash", "ff-only"}},
		{"merge_via_unknown.toml", []string{"merge.via", "web", "git-push"}},
		{
			"forge_kind_unknown.toml",
			[]string{"forge.kind", "bogus", "github, gitlab, bitbucket, gitea, azure, none"},
		},
		{"tracker_kind_unknown.toml", []string{"tracker.kind", "backlog", "forge, jira, linear, files"}},
		{"ci_kind_unknown.toml", []string{"ci.kind", "teamcity", "forge, jenkins, command, none"}},
		{"parallel_max_tasks_zero.toml", []string{"parallel.max_tasks", "0"}},
		{"isolation_mode_unknown.toml", []string{"isolation.mode", "vm", "host"}},
		{"requirements_tool_duplicate.toml", []string{"requirements.tools[1].name", "go"}},
		{"capabilities_duplicate.toml", []string{"capabilities[1].name", "github-keys"}},
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
		"example.toml", "minimal.toml", "fallback_defaults.toml", "identity_bot.toml",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadFile(t, name); err != nil {
				t.Errorf("Load(%s) returned an error: %v", name, err)
			}
		})
	}
}
