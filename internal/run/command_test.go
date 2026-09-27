package run

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/forge"
)

// TestBranch walks what a branch of a task is called: the number of the task says
// which one it is, and the slug of the title says what it is about, so that a
// person looking at a list of branches needs nothing else to tell them apart.
func TestBranch(t *testing.T) {
	cases := []struct {
		name   string
		number int
		title  string
		want   string
	}{
		{
			name:   "a title in latin letters",
			number: 43,
			title:  "feat: crewflow task run (M1.3b)",
			want:   "crewflow/43-feat-crewflow-task-run-m1-3b",
		},
		{
			name:   "a title in a language of the project",
			number: 7,
			title:  "Риски и решения",
			want:   "crewflow/7-task",
		},
		{
			name:   "a title with nothing a branch may hold",
			number: 7,
			title:  "???",
			want:   "crewflow/7-task",
		},
		{
			name:   "a title that is already short",
			number: 7,
			title:  "status",
			want:   "crewflow/7-status",
		},
		{
			name:   "a title of many words",
			number: 43,
			title:  "run the executor without a window and parse what it answered",
			want:   "crewflow/43-run-the-executor-without-a-window-and",
		},
		{
			name:   "a title of one long word",
			number: 43,
			title:  strings.Repeat("a", 60),
			want:   "crewflow/43-" + strings.Repeat("a", 40),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			branch := Branch(forge.Task{Number: tc.number, Title: tc.title})

			if branch != tc.want {
				t.Errorf("Branch(%q) = %q, want %q", tc.title, branch, tc.want)
			}
			if got := strings.TrimPrefix(branch, "crewflow/"); got != strings.ToLower(got) {
				t.Errorf("the branch %q is not in lower case, want a name a shell and a host take as it is", branch)
			}
		})
	}
}

// TestWorktree checks where the worktree of a task is made: under the root of the
// project, in a folder of its own named after the task, and never in the folder of
// the person (docs/DESIGN.md §8).
func TestWorktree(t *testing.T) {
	home := homeOfTest(t)
	cases := []struct {
		name string
		root string
		want string
	}{
		{
			name: "the root of the project",
			root: "~/worktrees/{repo}",
			want: filepath.Join(home, "worktrees", "naghuale-crewflow", "43"),
		},
		{
			name: "an absolute root",
			root: "/var/tmp/crewflow/{repo}",
			want: filepath.Join("/var/tmp/crewflow/naghuale-crewflow", "43"),
		},
		{
			name: "the root the settings leave out",
			want: filepath.Join(home, ".crewflow", "worktrees", "naghuale-crewflow", "43"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Config{Project: config.Project{Repo: "naghuale/crewflow"}, Worktrees: config.Worktrees{Root: tc.root}}

			got, err := Worktree(cfg, 43)
			if err != nil {
				t.Fatalf("Worktree returned an error: %v", err)
			}
			if got != tc.want {
				t.Errorf("Worktree = %q, want %q", got, tc.want)
			}
		})
	}
}

// homeOfTest is the home directory of a test: TestMain points "~/" at a folder of
// the run of the tests for all of them, and a test that wants a home of its own
// takes one here.
func homeOfTest(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

// TestCommand walks the command line a run starts the executor with: the
// placeholders of the settings are substituted, and the flag of the model is added
// only when the project named a model (docs/DESIGN.md §5).
func TestCommand(t *testing.T) {
	cases := []struct {
		name string
		spec string
		want []string
	}{
		{
			name: "the model is left to the agent",
			spec: `command = ["opencode", "run", "--dir", "{worktree}", "--format", "json", "{prompt}"]`,
			want: []string{"opencode", "run", "--dir", "/w/43", "--format", "json", "do the task"},
		},
		{
			name: "the model is asked for",
			spec: `
command = ["opencode", "run", "--dir", "{worktree}", "--format", "json", "{prompt}"]
model = "some/model"
model_flag = ["--model", "{model}"]
`,
			want: []string{"opencode", "run", "--dir", "/w/43", "--format", "json", "do the task", "--model", "some/model"},
		},
		{
			name: "a model flag of a project that names no model",
			spec: `
command = ["opencode", "run", "{prompt}"]
model_flag = ["--model", "{model}"]
`,
			want: []string{"opencode", "run", "do the task"},
		},
		{
			name: "the text of the task and the worktree in one argument",
			spec: `command = ["agent", "--cwd={worktree}", "run: {prompt}"]`,
			want: []string{"agent", "--cwd=/w/43", "run: do the task"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := executorOf(t, tc.spec)

			got, err := Command(spec, "/w/43", "do the task")
			if err != nil {
				t.Fatalf("Command returned an error: %v", err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("Command = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCommandWithoutACommand: a project with no command for its executor cannot
// be run at all, and crewflow says so instead of starting nothing.
func TestCommandWithoutACommand(t *testing.T) {
	_, err := Command(config.ExecutorSpec{}, "/w/43", "do the task")

	if err == nil {
		t.Fatal("Command of an executor with no command returned no error, want one")
	}
	if !strings.Contains(err.Error(), "executor.command") {
		t.Errorf("error %q does not name the key a person has to fill in", err)
	}
}

// TestPrompt reads what the executor is given the way a person reads it: where it
// works, what it must not do, what has to pass, what to open, and the whole task
// after all of it.
func TestPrompt(t *testing.T) {
	cfg := config.Config{
		Project: config.Project{Repo: "naghuale/crewflow", Language: "en"},
		Gates: []config.Gate{
			{Name: "test", Run: []string{"go", "test", "-race", "-count=1", "./..."}},
			{Name: "lint", Run: []string{"golangci-lint", "run", "./..."}},
		},
	}
	task := forge.Task{Number: 43, Title: "crewflow task run", Body: "## Why\n\na person runs a command by hand"}

	prompt, err := Prompt(task, cfg, "crewflow/43-crewflow-task-run", "/w/43")
	if err != nil {
		t.Fatalf("Prompt returned an error: %v", err)
	}

	for _, want := range []string{
		"/w/43", "crewflow/43-crewflow-task-run",
		".scratch/", "AGENTS.md", "BLOCKED:",
		"go test -race -count=1 ./...", "golangci-lint run ./...",
		"Closes #43",
		task.Title, task.Body,
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt does not mention %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "{{") {
		t.Errorf("the prompt holds a placeholder of the template, want it filled in:\n%s", prompt)
	}
	// The body goes last, so that the rules of the run are read first and the
	// task is the last thing an agent has in front of it.
	if strings.Index(prompt, task.Body) < strings.Index(prompt, "Closes #43") {
		t.Errorf("the task is not the last part of the prompt:\n%s", prompt)
	}
}

// TestPromptWithoutGates: a project with no gates has nothing to run before the
// change request, and the prompt says so instead of listing nothing under a
// heading that promises something.
func TestPromptWithoutGates(t *testing.T) {
	prompt, err := Prompt(forge.Task{Number: 43, Title: "t", Body: "b"}, config.Config{}, "crewflow/43-t", "/w/43")
	if err != nil {
		t.Fatalf("Prompt returned an error: %v", err)
	}

	if !strings.Contains(prompt, "no gates") {
		t.Errorf("the prompt of a project without gates does not say so:\n%s", prompt)
	}
}

// TestContinuation checks what an executor is given when a run goes on without a
// session to continue in: the message of the orchestrator, and the whole task, or
// the agent would begin again from nothing (docs/DESIGN.md §7a).
func TestContinuation(t *testing.T) {
	task := forge.Task{Number: 43, Title: "crewflow task run", Body: "## Why\n\na person runs a command by hand"}

	prompt, err := Continuation("the review asked for tests", task, config.Config{}, "crewflow/43-crewflow-task-run", "/w/43")
	if err != nil {
		t.Fatalf("Continuation returned an error: %v", err)
	}

	if !strings.HasPrefix(prompt, "the review asked for tests") {
		t.Errorf("the continuation does not begin with the message of the orchestrator:\n%s", prompt)
	}
	for _, want := range []string{task.Title, task.Body, "Closes #43"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the continuation does not hold %q, want the whole task again:\n%s", want, prompt)
		}
	}
}

// executorOf reads the [executor] table a case writes, so that a case of the
// command line does not repeat the whole file of a project.
func executorOf(t *testing.T, table string) config.ExecutorSpec {
	t.Helper()
	path := filepath.Join(t.TempDir(), "crewflow.toml")
	content := "[project]\nrepo = \"naghuale/crewflow\"\n\n[executor]\n" + table + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write the config of the test: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load returned an error: %v", err)
	}
	return cfg.Executor.ExecutorSpec
}
