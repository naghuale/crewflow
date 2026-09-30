package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// baseConfig is what the project of a test has: a repository and an executor
// that is already installed. A case that says more about the executor adds its
// keys to this table or a sub-table of it, and never writes it twice: a file
// with two [executor] tables is not a file.
const baseConfig = `
[project]
repo = "naghuale/crewflow"

[executor]
command = ["agent", "run", "--dir", "{worktree}", "{prompt}"]
`

// TestRunEverythingThere checks that a machine with everything the project needs
// gets a report in which every check passed, and that the checks come in the
// order the report promises.
func TestRunEverythingThere(t *testing.T) {
	m := newMachine().
		has("git", "gh", "agent", "codex", "go", "golangci-lint", "sh").
		prints("git --version", "git version 2.47.1\n").
		prints("gh --version", "gh version 2.62.0 (2024-11-14)\n").
		prints("gh auth status", "github.com\n  ✓ Logged in to github.com account octocat (keyring)\n  - Token: gho_************\n").
		prints("go version", "go version go1.27.1 darwin/arm64\n").
		prints("golangci-lint version", "golangci-lint has version 2.14.0 built with go1.27.1\n").
		// A "test -e" check says nothing and passes: the machine is set up so
		// that the file is there.
		prints("sh -c test -e /usr/local/lib/libtdjson", "")
	config := writeConfig(t, baseConfig+`
[requirements]
tools = [
  { name = "go", check = ["go", "version"], min = "1.27" },
  { name = "golangci-lint", check = ["golangci-lint", "version"] },
  { name = "libtdjson", check = ["sh", "-c", "test -e /usr/local/lib/libtdjson"] },
]

[[gates]]
name = "format"
run = ["sh", "-c", "test -z \"$(gofmt -l .)\""]

[[gates]]
name = "lint"
run = ["golangci-lint", "run", "./..."]
`)

	report := runOn(t, m, config)

	want := []string{
		"config", "git", "gh", "gh login", "executor identity", "executor",
		"access read", "gate format", "gate lint", "tool go", "tool golangci-lint", "tool libtdjson",
	}
	if got := checkNames(report); !slices.Equal(got, want) {
		t.Errorf("checks = %v, want %v", got, want)
	}
	for _, check := range report.Checks {
		if check.Status == Fail {
			t.Errorf("check %q = %q (%s), want it to pass", check.Name, check.Status, check.Detail)
		}
	}
	if !report.OK() {
		t.Error("report.OK() = false, want true when nothing failed")
	}
	// The one thing a machine of the pilot has to be told about itself is that the
	// powers of its executor are the powers of its login: everything a run needs is
	// there, and a personal project is a project crewflow has to work for. The mode of
	// the bot is the answer, and a report says so (docs/DESIGN.md §7i).
	identity := checkOf(t, report, "executor identity")
	if identity.Status != Warn {
		t.Errorf("check \"executor identity\" = %q, want %q: a run in the mode of the owner has the rights of the login", identity.Status, Warn)
	}
	if !strings.Contains(identity.Detail, "shared rights") {
		t.Errorf("check \"executor identity\" detail = %q, want it to say the rights are shared", identity.Detail)
	}
	if !strings.Contains(identity.Hint, `mode = "bot"`) {
		t.Errorf("check \"executor identity\" hint = %q, want it to name the mode that separates the powers", identity.Hint)
	}
}

// TestRunReportKeepsTheToken checks that the answer of "gh auth status" is never
// shown as it is: it holds the token, and the report is read by people and
// pasted into issues.
func TestRunReportKeepsTheToken(t *testing.T) {
	m := newMachine().has("git", "gh", "agent").
		prints("git --version", "git version 2.47.1\n").
		prints("gh --version", "gh version 2.62.0\n").
		prints("gh auth status", "github.com\n  ✓ Logged in to github.com account octocat (keyring)\n"+
			"  - Active account: true\n  - Token: gho_secrettoken\n  - Token scopes: 'gist'\n")
	config := writeConfig(t, baseConfig)

	report := runOn(t, m, config)

	login := checkOf(t, report, "gh login")
	if login.Detail != "signed in as octocat" {
		t.Errorf("check \"gh login\" detail = %q, want it to name the account and nothing else", login.Detail)
	}
	for _, want := range []string{"gho_", "Token scopes"} {
		if strings.Contains(printed(report), want) {
			t.Errorf("the report holds %q of the answer of gh auth status", want)
		}
	}
}

// TestRunMissingPrograms walks what is missing from the machine and checks that
// the report names it and says what to do about it.
func TestRunMissingPrograms(t *testing.T) {
	cases := []struct {
		name     string
		missing  string
		config   string
		check    string
		wantHint []string
	}{
		{
			name:     "git",
			missing:  "git",
			check:    "git",
			wantHint: []string{"git"},
		},
		{
			name:     "gh",
			missing:  "gh",
			check:    "gh",
			wantHint: []string{"gh", "gh auth login"},
		},
		{
			name:     "executor",
			missing:  "agent",
			check:    "executor",
			wantHint: []string{"agent", "executor.command"},
		},
		{
			name:     "program of a gate",
			missing:  "golangci-lint",
			config:   "\n[[gates]]\nname = \"lint\"\nrun = [\"golangci-lint\", \"run\", \"./...\"]\n",
			check:    "gate lint",
			wantHint: []string{"golangci-lint", "gates[0].run"},
		},
		{
			name:     "required tool",
			missing:  "nosuchtool",
			config:   "\n[requirements]\ntools = [{ name = \"nosuchtool\", check = [\"nosuchtool\", \"--version\"] }]\n",
			check:    "tool nosuchtool",
			wantHint: []string{"nosuchtool", "requirements.tools[0].check"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine().
				prints("git --version", "git version 2.47.1\n").
				prints("gh --version", "gh version 2.62.0\n").
				prints("gh auth status", "  ✓ Logged in to github.com account octocat\n").
				prints("go version", "go version go1.27.1\n")
			for _, program := range []string{"git", "gh", "agent", "codex", "go", "golangci-lint", "sh"} {
				if program != tc.missing {
					m.has(program)
				}
			}
			config := writeConfig(t, baseConfig+tc.config)

			report := runOn(t, m, config)

			got := checkOf(t, report, tc.check)
			if got.Status != Fail {
				t.Errorf("check %q = %q (%s), want fail", tc.check, got.Status, got.Detail)
			}
			if !strings.Contains(got.Detail, tc.missing) {
				t.Errorf("check %q detail = %q, want it to name %q", tc.check, got.Detail, tc.missing)
			}
			for _, want := range tc.wantHint {
				if !strings.Contains(got.Hint, want) {
					t.Errorf("check %q hint = %q, want it to mention %q", tc.check, got.Hint, want)
				}
			}
			if report.OK() {
				t.Error("report.OK() = true, want false when a check failed")
			}
		})
	}
}

// TestRunGHNotLoggedIn is the case a person runs into right after installing
// gh: the program is there, but nobody is signed in.
func TestRunGHNotLoggedIn(t *testing.T) {
	m := newMachine().has("git", "gh", "agent").
		prints("git --version", "git version 2.47.1\n").
		prints("gh --version", "gh version 2.62.0\n").
		fails("gh auth status", "You are not logged into any GitHub hosts. To log in, run: gh auth login\n")
	config := writeConfig(t, baseConfig)

	report := runOn(t, m, config)

	login := checkOf(t, report, "gh login")
	if login.Status != Fail {
		t.Errorf("check \"gh login\" = %q (%s), want fail", login.Status, login.Detail)
	}
	if !strings.Contains(login.Hint, "gh auth login") {
		t.Errorf("check \"gh login\" hint = %q, want it to say how to sign in", login.Hint)
	}
	if got := checkOf(t, report, "gh"); got.Status != OK {
		t.Errorf("check \"gh\" = %q (%s), want ok: the program is installed", got.Status, got.Detail)
	}
}

// TestRunGateThroughShell checks that a gate of the shape ["sh", "-c", "…"] is
// checked by the shell alone: what the script calls is the job of
// [requirements] to say, not something a gate may hide from the report.
func TestRunGateThroughShell(t *testing.T) {
	m := newMachine().has("git", "gh", "agent", "sh")
	config := writeConfig(t, baseConfig+`
[[gates]]
name = "format"
run = ["sh", "-c", "test -z \"$(nosuchtool --list .)\""]
`)

	report := runOn(t, m, config)

	if got := checkOf(t, report, "gate format"); got.Status != OK {
		t.Errorf("check \"gate format\" = %q (%s), want ok: sh is installed", got.Status, got.Detail)
	}
	if slices.Contains(m.lookedUp, "nosuchtool") {
		t.Errorf("the machine was asked about %q, want only the shell to be looked up: %v", "nosuchtool", m.lookedUp)
	}
}

// TestRunRequirementVersions walks what a check command prints against the min
// of the tool, which is the only place where a version is compared.
func TestRunRequirementVersions(t *testing.T) {
	cases := []struct {
		name     string
		program  string
		tool     string
		tools    string
		output   string
		code     int
		want     Status
		detail   []string
		wantHint []string
	}{
		{
			name:    "newer than the minimum",
			program: "go",
			tool:    "go",
			tools:   `tools = [{ name = "go", check = ["go", "version"], min = "1.27" }]`,
			output:  "go version go1.27.1 darwin/arm64\n",
			want:    OK,
			detail:  []string{"go1.27.1"},
		},
		{
			name:     "older than the minimum",
			program:  "go",
			tool:     "go",
			tools:    `tools = [{ name = "go", check = ["go", "version"], min = "1.27" }]`,
			output:   "go version go1.26.3 darwin/arm64\n",
			want:     Fail,
			detail:   []string{"1.26.3", "1.27"},
			wantHint: []string{"go", "1.27"},
		},
		{
			name:    "no version in the output",
			program: "go",
			tool:    "go",
			tools:   `tools = [{ name = "go", check = ["go", "version"], min = "1.27" }]`,
			output:  "built from source\n",
			want:    Warn,
			detail:  []string{"1.27"},
		},
		{
			name:    "no minimum to compare with",
			program: "go",
			tool:    "go",
			tools:   `tools = [{ name = "go", check = ["go", "version"] }]`,
			output:  "go version go1.26.3 darwin/arm64\n",
			want:    OK,
			detail:  []string{"go version go1.26.3 darwin/arm64"},
		},
		{
			name:     "the check itself fails",
			program:  "sh",
			tool:     "libtdjson",
			tools:    `tools = [{ name = "libtdjson", check = ["sh", "-c", "test -e \"$TELECLI_TDLIB_LIBRARY\""] }]`,
			output:   "libtdjson is not where the build expects it\n",
			code:     1,
			want:     Fail,
			detail:   []string{"libtdjson is not where the build expects it"},
			wantHint: []string{"sh -c"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine().has("git", "gh", "agent", tc.program).
				prints("git --version", "git version 2.47.1\n").
				prints("gh --version", "gh version 2.62.0\n").
				prints("gh auth status", "  ✓ Logged in to github.com account octocat\n")
			m.answers[tc.program] = answer{stdout: tc.output, code: tc.code}
			config := writeConfig(t, baseConfig+"\n[requirements]\n"+tc.tools+"\n")

			report := runOn(t, m, config)

			got := checkOf(t, report, "tool "+tc.tool)
			if got.Status != tc.want {
				t.Errorf("check %q = %q (%s), want %q", got.Name, got.Status, got.Detail, tc.want)
			}
			for _, want := range tc.detail {
				if !strings.Contains(got.Detail, want) {
					t.Errorf("check %q detail = %q, want it to mention %q", got.Name, got.Detail, want)
				}
			}
			for _, want := range tc.wantHint {
				if !strings.Contains(got.Hint, want) {
					t.Errorf("check %q hint = %q, want it to mention %q", got.Name, got.Hint, want)
				}
			}
			if wantOK := tc.want != Fail; report.OK() != wantOK {
				t.Errorf("report.OK() = %t, want %t: only a failed check stops crewflow", report.OK(), wantOK)
			}
		})
	}
}

// TestRunToolWithoutCheckCommand: a tool with nothing to check is a mistake in
// the file, and doctor says so instead of passing it.
func TestRunToolWithoutCheckCommand(t *testing.T) {
	m := newMachine().has("git", "gh", "agent")
	config := writeConfig(t, baseConfig+"\n[requirements]\ntools = [{ name = \"go\" }]\n")

	report := runOn(t, m, config)

	got := checkOf(t, report, "tool go")
	if got.Status != Fail {
		t.Errorf("check \"tool go\" = %q (%s), want fail", got.Status, got.Detail)
	}
	if !strings.Contains(got.Hint, "check") {
		t.Errorf("check \"tool go\" hint = %q, want it to say what is missing in the file", got.Hint)
	}
}

// TestRunUnreadableConfig checks the case of a folder that is not a project
// yet: git is still worth telling about, and nothing that needs the file is
// checked. The roles are among those, because which host, tracker and CI a
// project has is what the file says (docs/DESIGN.md §7g).
func TestRunUnreadableConfig(t *testing.T) {
	cases := []struct {
		name   string
		config string
		want   string
	}{
		{
			name:   "no file at all",
			config: filepath.Join(t.TempDir(), "missing.toml"),
			want:   "missing.toml",
		},
		{
			name:   "a file crewflow cannot use",
			config: writeConfig(t, baseConfig+"\n[taks]\nowner_approval = \"risky\"\n"),
			want:   "taks",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine().has("git", "gh", "agent").
				prints("git --version", "git version 2.47.1\n").
				prints("gh --version", "gh version 2.62.0\n")

			report := runOn(t, m, tc.config)

			got := checkOf(t, report, "config")
			if got.Status != Fail {
				t.Errorf("check \"config\" = %q (%s), want fail", got.Status, got.Detail)
			}
			if !strings.Contains(got.Detail, tc.want) {
				t.Errorf("check \"config\" detail = %q, want it to name %q", got.Detail, tc.want)
			}
			if got.Hint == "" {
				t.Error("check \"config\" has no hint, want one that says what to do")
			}
			if git := checkOf(t, report, "git"); git.Status != OK {
				t.Errorf("check \"git\" is not ok, want it made even without the file")
			}
			for _, check := range report.Checks {
				if check.Name == "gh" || check.Name == "gh login" {
					t.Errorf("check %q was made, want the roles to wait for the file that names them", check.Name)
				}
				if strings.HasPrefix(check.Name, "executor") ||
					strings.HasPrefix(check.Name, "gate ") ||
					strings.HasPrefix(check.Name, "tool ") {
					t.Errorf("check %q was made without a readable file", check.Name)
				}
			}
			if report.OK() {
				t.Error("report.OK() = true, want false when the file is not readable")
			}
		})
	}
}

// TestRunSpecifiedSettings is the case a person runs into after writing down a
// host, a tracker or a CI crewflow has no adapter for: the file does not load, the
// report says which key asked, which task writes it and what to write instead, and
// the code is not zero (docs/DESIGN.md §5).
func TestRunSpecifiedSettings(t *testing.T) {
	cases := []struct {
		name   string
		config string
		want   []string
	}{
		{
			name:   "the host of the code",
			config: "\n[forge]\nkind = \"gitlab\"\n",
			want:   []string{"forge.kind", "gitlab", "crewflow#60", `kind = "github"`},
		},
		{
			name:   "the tracker of the tasks",
			config: "\n[tracker]\nkind = \"jira\"\n",
			want:   []string{"tracker.kind", "jira", "crewflow#62", `kind = "forge"`},
		},
		{
			name:   "the CI of a commit",
			config: "\n[ci]\nkind = \"jenkins\"\n",
			want:   []string{"ci.kind", "jenkins", "no issue is open", `kind = "forge"`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine().has("git", "gh", "agent").
				prints("git --version", "git version 2.47.1\n").
				prints("gh --version", "gh version 2.62.0\n").
				prints("gh auth status", "  ✓ Logged in to github.com account octocat\n")
			config := writeConfig(t, baseConfig+tc.config)

			report := runOn(t, m, config)

			file := checkOf(t, report, "config")
			if file.Status != Fail {
				t.Errorf("check %q = %q (%s), want fail: the file asks for what crewflow does not do", "config", file.Status, file.Detail)
			}
			if !strings.Contains(file.Detail, "crewflow.toml") {
				t.Errorf("check %q detail = %q, want it to name the file", "config", file.Detail)
			}
			asked := checkOf(t, report, specifiedCheck)
			if asked.Status != Fail {
				t.Errorf("check %q = %q (%s), want fail", specifiedCheck, asked.Status, asked.Detail)
			}
			for _, want := range tc.want {
				if !strings.Contains(asked.Detail+asked.Hint+file.Detail, want) {
					t.Errorf("the report does not mention %q, want the key that asked, the task and what to write", want)
				}
			}
			// The list of what asked is a part of the report and not only a line of a
			// check: an orchestrator reads the report on every run, and the keys are
			// what it has to tell a person about (docs/DESIGN.md §5).
			if len(report.Specified) != 1 {
				t.Fatalf("the report holds %d settings, want the one the file asked for", len(report.Specified))
			}
			if got := report.Specified[0].Key; got != strings.SplitN(tc.want[0], " = ", 2)[0] {
				t.Errorf("the report names the key %q, want %q", got, tc.want[0])
			}
			if report.OK() {
				t.Error("report.OK() = true, want false when the file asks for what is not written")
			}
			if slices.ContainsFunc(report.Checks, func(check Check) bool { return check.Name == "gh" }) {
				t.Error("the report asked GitHub about a project whose file does not load, want nothing asked")
			}
		})
	}
}

// wantPrompt is the question the probe must ask, written out here rather than
// taken from the package: the test says what an executor is asked, it does not
// agree with the package on it.
const wantPrompt = "Reply with exactly: crewflow-probe-ok"

// TestRunProbe checks that the probe asks the executor one tiny question in an
// empty folder, with the placeholders a task run would substitute, and that it
// believes the answer only when the marker is in it.
func TestRunProbe(t *testing.T) {
	cases := []struct {
		name string
		// config holds the keys of the [executor] table that differ from the
		// one every test has.
		config string
		answer string
		want   Status
		// wantArgs is what the executor must be run with, where {worktree} is
		// the empty folder of this test.
		wantArgs []string
	}{
		{
			name:     "the executor answers",
			answer:   "crewflow-probe-ok\n",
			want:     OK,
			wantArgs: []string{"run", "--dir", "{worktree}", wantPrompt},
		},
		{
			name:     "the executor answers something else",
			answer:   "I am not sure what you mean\n",
			want:     Fail,
			wantArgs: []string{"run", "--dir", "{worktree}", wantPrompt},
		},
		{
			name: "the model is asked for",
			config: `
model = "some/model"
model_flag = ["--model", "{model}"]
`,
			answer:   "crewflow-probe-ok",
			want:     OK,
			wantArgs: []string{"run", "--dir", "{worktree}", wantPrompt, "--model", "some/model"},
		},
		{
			name: "the model is left to the agent",
			config: `
model_flag = ["--model", "{model}"]
`,
			answer:   "crewflow-probe-ok",
			want:     OK,
			wantArgs: []string{"run", "--dir", "{worktree}", wantPrompt},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine().has("git", "gh", "agent")
			m.answers["agent"] = answer{stdout: tc.answer}
			config := writeConfig(t, baseConfig+tc.config)
			env := m.env(t, config)
			env.Probe = true

			report := Run(t.Context(), env)

			got := checkOf(t, report, "executor probe")
			if got.Status != tc.want {
				t.Errorf("check \"executor probe\" = %q (%s), want %q", got.Status, got.Detail, tc.want)
			}
			if tc.want == Fail && got.Hint == "" {
				t.Error("check \"executor probe\" has no hint, want the command to run by hand")
			}
			probe := m.commandOf("agent")
			if probe == nil {
				t.Fatalf("the executor was not run, only: %v", m.ran)
			}
			want := slices.Clone(tc.wantArgs)
			for i, arg := range want {
				want[i] = strings.ReplaceAll(arg, "{worktree}", env.TempDir)
			}
			if !reflect.DeepEqual(probe.args, want) {
				t.Errorf("the probe ran with %q, want the arguments %q", probe.args, want)
			}
			if probe.dir != env.TempDir {
				t.Errorf("the probe ran in %q, want the empty folder %q", probe.dir, env.TempDir)
			}
		})
	}
}

// TestRunProbeWithoutProbe: the probe spends the limits of the executor, so it
// happens only when it was asked for.
func TestRunProbeWithoutProbe(t *testing.T) {
	m := newMachine().has("git", "gh", "agent")
	config := writeConfig(t, baseConfig)

	report := runOn(t, m, config)

	if slices.Contains(checkNames(report), "executor probe") {
		t.Errorf("the executor was run without the probe: %v", m.ran)
	}
}

// TestRunProbeTimeout checks that an executor that never answers ends the probe
// instead of hanging crewflow forever (docs/DESIGN.md §7a).
func TestRunProbeTimeout(t *testing.T) {
	shortenProbeTimeout(t, 10*time.Millisecond)
	m := newMachine().has("git", "gh", "agent")
	// The executor is the one program that never answers; everything else is
	// the machine as it is.
	m.run = m.answering("agent", func(ctx context.Context) ([]byte, []byte, int, error) {
		<-ctx.Done()
		return nil, nil, -1, ctx.Err()
	})
	config := writeConfig(t, baseConfig)
	env := m.env(t, config)
	env.Probe = true

	report := Run(t.Context(), env)

	got := checkOf(t, report, "executor probe")
	if got.Status != Fail {
		t.Errorf("check \"executor probe\" = %q (%s), want fail", got.Status, got.Detail)
	}
	if !strings.Contains(got.Detail, "no answer in") {
		t.Errorf("check \"executor probe\" detail = %q, want it to name the time it waited for", got.Detail)
	}
}

// TestRunProbeTimeoutIsTwoMinutes is the number the probe is promised to have,
// checked without waiting for it: the run of the executor must end in it.
func TestRunProbeTimeoutIsTwoMinutes(t *testing.T) {
	m := newMachine().has("git", "gh", "agent")
	var deadline time.Time
	var hasDeadline bool
	m.run = m.answering("agent", func(ctx context.Context) ([]byte, []byte, int, error) {
		deadline, hasDeadline = ctx.Deadline()
		return []byte("crewflow-probe-ok"), nil, 0, nil
	})
	config := writeConfig(t, baseConfig)
	env := m.env(t, config)
	env.Probe = true

	Run(t.Context(), env)

	if !hasDeadline {
		t.Fatal("the probe was run without a timeout, want the run to end in two minutes")
	}
	if left := time.Until(deadline); left < 119*time.Second || left > 120*time.Second {
		t.Errorf("the probe had %s left, want about 2m", left)
	}
}

// TestRunProbeWithoutTemporaryFolder: a probe needs an empty folder of its own
// to run in, and the caller is the one who knows where that is.
func TestRunProbeWithoutTemporaryFolder(t *testing.T) {
	m := newMachine().has("git", "gh", "agent")
	config := writeConfig(t, baseConfig)
	env := m.env(t, config)
	env.Probe = true
	env.TempDir = ""

	report := Run(t.Context(), env)

	got := checkOf(t, report, "executor probe")
	if got.Status != Fail {
		t.Errorf("check \"executor probe\" = %q (%s), want fail", got.Status, got.Detail)
	}
	if !strings.Contains(got.Hint, "TempDir") {
		t.Errorf("check \"executor probe\" hint = %q, want it to name what the caller left out", got.Hint)
	}
}

// TestRunShowsWhatTheAgentMayRead is the section of the report a person reads before a
// task starts: the folders the commands of the project named, and every path crewflow
// would not open with the reason why. A refusal of a run is about one of these two
// lists, and there is nowhere else to look for it (docs/DESIGN.md §7d).
func TestRunShowsWhatTheAgentMayRead(t *testing.T) {
	cases := []struct {
		name string
		// access is the [access] table of the project.
		access string
		// wantsCache says that the folder the tool named is the whole of what the
		// executor may read, wantRejected are the words the check of the refused paths
		// has to hold, and wantRefused how many of them there are. No words at all
		// means that there is no such check.
		wantsCache   bool
		wantRejected []string
		wantRefused  int
	}{
		{
			name:   "a project that says nothing",
			access: "\n[access]\n",
		},
		{
			name:       "a project that says where its dependencies are",
			access:     "\n[access]\nread_from = [[\"go\", \"env\", \"GOMODCACHE\"]]\n",
			wantsCache: true,
		},
		{
			name:         "a project that named a place of secrets and the root of a disk",
			access:       "\n[access]\nread = [\"~/.ssh\", \"/\"]\n",
			wantRejected: []string{"~/.ssh", "where secrets are", "/", "the root of a disk"},
			wantRefused:  2,
		},
		{
			name:         "a tool that cannot say where they are",
			access:       "\n[access]\nread_from = [[\"npm\", \"config\", \"get\", \"cache\"]]\n",
			wantRejected: []string{"npm config get cache", "did not run"},
			wantRefused:  1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine().has("git", "gh", "agent").
				prints("git --version", "git version 2.47.1\n").
				prints("gh --version", "gh version 2.62.0\n").
				prints("gh auth status", "  ✓ Logged in to github.com account octocat\n")
			// The tool of the project names the folder it keeps its cache in, and the
			// folder is one of the test: a test of the report opens nothing of a
			// person.
			m.run = m.answering("go", func(context.Context) ([]byte, []byte, int, error) {
				return []byte(m.cache + "\n"), nil, 0, nil
			})
			env := m.env(t, writeConfig(t, baseConfig+tc.access))
			m.cache = filepath.Join(env.Home, "go", "pkg", "mod")
			if err := os.MkdirAll(m.cache, 0o700); err != nil {
				t.Fatalf("make %s: %v", m.cache, err)
			}
			m.cache = onThisMachine(t, m.cache)

			report := Run(t.Context(), env)

			read := checkOf(t, report, "access read")
			if read.Status != OK {
				t.Errorf("check \"access read\" = %q (%s), want ok", read.Status, read.Detail)
			}
			switch {
			case tc.wantsCache && !slices.Equal(report.Access.Read, []string{m.cache}):
				t.Errorf("the report allows reading %v, want the folder of the cache %q", report.Access.Read, m.cache)
			case tc.wantsCache && read.Detail != m.cache:
				t.Errorf("check \"access read\" detail = %q, want the folder of the cache %q", read.Detail, m.cache)
			case !tc.wantsCache && len(report.Access.Read) != 0:
				t.Errorf("the report allows reading %v, want nothing: the project named no folder", report.Access.Read)
			case !tc.wantsCache && !strings.Contains(read.Detail, "names no folder"):
				t.Errorf("check \"access read\" detail = %q, want it to say that the project named nothing", read.Detail)
			}
			// The keys of the person are closed to the executor whatever the project
			// wrote, and the report says where they are.
			if !slices.Contains(report.Access.Deny, filepath.Join(env.Home, ".ssh")) {
				t.Errorf("the closed places are %v, want the keys of the person among them", report.Access.Deny)
			}
			rejected, isCheck := checkByName(report, "access rejected")
			switch {
			case len(tc.wantRejected) == 0 && isCheck:
				t.Errorf("check \"access rejected\" = %q (%s), want no such check", rejected.Status, rejected.Detail)
			case len(tc.wantRejected) == 0 && len(report.Access.Rejected) != 0:
				t.Errorf("the refused paths are %+v, want none", report.Access.Rejected)
			case len(tc.wantRejected) > 0:
				// A refused path is worth a look and not a broken machine: the run goes
				// on without it, and a task may still run.
				if rejected.Status != Warn || !report.OK() {
					t.Errorf("the refused paths are %q and the report is ok %t, want a warning that does not stop crewflow",
						rejected.Status, report.OK())
				}
				if !strings.Contains(rejected.Hint, "crewflow.toml") {
					t.Errorf("check \"access rejected\" hint = %q, want it to name the file to change", rejected.Hint)
				}
				for _, want := range tc.wantRejected {
					if !strings.Contains(rejected.Detail, want) {
						t.Errorf("check \"access rejected\" detail = %q, want it to mention %q", rejected.Detail, want)
					}
				}
				if len(report.Access.Rejected) != tc.wantRefused {
					t.Errorf("the refused paths are %+v, want %d of them", report.Access.Rejected, tc.wantRefused)
				}
			}
		})
	}
}

// TestRunOfAReportWithoutTheFileOfTheProject: the reading policy is a part of every
// report, and a report made without a file to read holds empty lists rather than no
// lists at all, because an orchestrator reads them on every report.
func TestRunOfAReportWithoutTheFileOfTheProject(t *testing.T) {
	m := newMachine().has("git").prints("git --version", "git version 2.47.1\n")

	report := runOn(t, m, filepath.Join(t.TempDir(), "missing.toml"))

	if report.Access.Read == nil || report.Access.Deny == nil || report.Access.Rejected == nil {
		t.Errorf("the report of a run without a file holds %+v, want three empty lists", report.Access)
	}
}

// runOn checks the fake machine and returns what the report says.
func runOn(t *testing.T, m *machine, configPath string) Report {
	t.Helper()
	return Run(t.Context(), m.env(t, configPath))
}

// checkOf returns the check with the name, so that a test may look at one line
// of the report without depending on the position of the others.
func checkOf(t *testing.T, report Report, name string) Check {
	t.Helper()
	for _, check := range report.Checks {
		if check.Name == name {
			return check
		}
	}
	t.Fatalf("no check named %q in %v", name, checkNames(report))
	return Check{}
}

// checkByName returns the check with the name and whether there is one, for a test
// that is about a check that may be absent.
func checkByName(report Report, name string) (Check, bool) {
	for _, check := range report.Checks {
		if check.Name == name {
			return check, true
		}
	}
	return Check{}, false
}

// checkNames are the names of the checks, in the order they were made.
func checkNames(report Report) []string {
	names := make([]string, 0, len(report.Checks))
	for _, check := range report.Checks {
		names = append(names, check.Name)
	}
	return names
}

// printed is the report as a person would read it, for the checks that must not
// hold what a command wrote.
func printed(report Report) string {
	var out strings.Builder
	for _, check := range report.Checks {
		fmt.Fprintf(&out, "%s %s %s %s\n", check.Name, check.Status, check.Detail, check.Hint)
	}
	return out.String()
}

// writeConfig writes a crewflow.toml into a folder of its own and returns its
// path, so that a test never reads the file of the project it runs in.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "crewflow.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}
