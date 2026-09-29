package run

import (
	"bytes"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/naghuale/crewflow/internal/run/profile"
)

// theRefusalToTmp is what a run of the pilot wrote on the way out when the executor
// wrote a file of the machine into its temporary folder: the refusal names the kind of
// the permission and what it was about, and the path is the pattern the agent was
// refused, not a single file.
const theRefusalToTmp = "INFO  service=default starting opencode\n" +
	"! permission requested: external_directory (/tmp/*); auto-rejecting\n"

// theRefusalToSsh is the same kind of refusal about a place of the machine that is
// closed for a reason of its own, and not a habit of a run.
const theRefusalToSsh = "! permission requested: external_directory (~/.ssh/config); auto-rejecting\n"

// TestRunGoesOnByItselfWhenTheExecutorWritesToTmp: the run that stopped on the one
// habit crewflow knows by heart goes on by itself, in the same worktree and the same
// session, with the text of that habit, and the outcome of the task is the one of the
// attempt that followed it (docs/DESIGN.md §7a).
func TestRunGoesOnByItselfWhenTheExecutorWritesToTmp(t *testing.T) {
	m := newMachine(t)
	// The executor of this test writes into /tmp in the first run, and OpenCode
	// refuses the whole command and ends the run there; in the second run it does the
	// work of the task.
	m.says("opencode",
		answer{stdout: theRun, stderr: theRefusalToTmp},
		answer{stdout: theRun},
	)
	host := &host{task: taskOf(43), opened: true}
	cfg := projectOf(t, m.worktrees, "")

	result, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	if result.Outcome != ChangeRequestOpened {
		t.Fatalf("the outcome = %q, want %q (rejections %v, reason %q)",
			result.Outcome, ChangeRequestOpened, result.Rejections, result.Reason)
	}
	if result.Attempt != 2 || !result.Continued {
		t.Errorf("the run is the attempt %d (continued %t), want the second and a continuation",
			result.Attempt, result.Continued)
	}
	if result.AutoResumed != string(reasonTmp) {
		t.Errorf("the run went on by itself for %q, want %q", result.AutoResumed, reasonTmp)
	}
	// The work is the work of the first run: the second run went on in the same
	// worktree and in the session the first one held.
	ran := m.commandsOf("opencode")
	if len(ran) != 2 {
		t.Fatalf("the executor was run %d times, want twice", len(ran))
	}
	if ran[1].dir != ran[0].dir {
		t.Errorf("the second run worked in %q, want the worktree of the first one %q", ran[1].dir, ran[0].dir)
	}
	if !slices.Equal(ran[1].args[len(ran[1].args)-2:], []string{"--session", "ses_7fKq2"}) {
		t.Errorf("the second run ran with %v, want it to go on in the session of the first one", ran[1].args)
	}
	// What the second run was asked is the text of the habit and nothing else: the
	// session holds the task and the context of the first try.
	asked := ran[1].args[len(ran[1].args)-3]
	for _, want := range []string{".scratch/tmp", "zz_debug_test.go", "before you commit"} {
		if !strings.Contains(asked, want) {
			t.Errorf("the second run was asked text without %q:\n%s", want, asked)
		}
	}
	// The journal of the attempt that ended holds the line about the resume, and the
	// state of the task holds the habit in the attempt that went on by itself.
	state := stateOf(t, m, 43)
	if len(state.Attempts) != 2 {
		t.Fatalf("the state holds %d attempts, want the two of the run", len(state.Attempts))
	}
	if state.Attempts[0].AutoResumed != "" || state.Attempts[1].AutoResumed != string(reasonTmp) {
		t.Errorf("the attempts are marked %q and %q, want the second one marked with the habit",
			state.Attempts[0].AutoResumed, state.Attempts[1].AutoResumed)
	}
	if state.Attempts[0].Outcome != BlockedPermission || state.Attempts[1].Outcome != ChangeRequestOpened {
		t.Errorf("the attempts ended as %q and %q, want %q and %q",
			state.Attempts[0].Outcome, state.Attempts[1].Outcome, BlockedPermission, ChangeRequestOpened)
	}
	journal := read(t, state.Attempts[0].Journal)
	resumed := resume{habit: habits[reasonTmp]}
	if want := "crewflow: resumed once — " + habits[reasonTmp].headline; !strings.Contains(journal, want) {
		t.Errorf("the journal of the attempt that ended holds no line %q:\n%s", want, journal)
	}
	// A watch of the attempt that ended shows the line: that is where the person
	// watching the run sees why the next attempt was given what it was given.
	watch, err := Watch(m.home, "naghuale-crewflow", 43, 1)
	if err != nil {
		t.Fatalf("Watch returned an error: %v", err)
	}
	var shown bytes.Buffer
	if err := watch.Follow(t.Context(), &shown, nil); err != nil {
		t.Fatalf("Follow returned an error: %v", err)
	}
	if want := resumed.line(); !strings.Contains(shown.String(), want) {
		t.Errorf("the watch showed %q, want the line %q", shown.String(), want)
	}
}

// TestRunGoesOnByItselfOnlyOnceForTheSameHabit: the executor that was told where its
// scratch is and wrote into /tmp again is not told a second time, and the task stops
// where it is for the orchestrator to decide (docs/DESIGN.md §7a, §7j).
func TestRunGoesOnByItselfOnlyOnceForTheSameHabit(t *testing.T) {
	m := newMachine(t)
	refused := answer{stdout: theRun, stderr: theRefusalToTmp}
	m.says("opencode", refused, refused)
	host := &host{task: taskOf(43), opened: true}
	cfg := projectOf(t, m.worktrees, "")

	result, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	if result.Outcome != BlockedPermission {
		t.Errorf("the outcome = %q, want %q after the second refusal of the same habit",
			result.Outcome, BlockedPermission)
	}
	if got := len(m.commandsOf("opencode")); got != 2 {
		t.Errorf("the executor was run %d times, want the first and one resume", got)
	}
	if result.Attempt != 2 {
		t.Errorf("the run is the attempt %d, want the second and no third after the same habit twice",
			result.Attempt)
	}
	// The attempt the run ended in is the one crewflow resumed, and the task stops
	// there: the refusals of it are what the state holds, one per attempt.
	state := stateOf(t, m, 43)
	if len(state.Attempts) != 2 {
		t.Fatalf("the state holds %d attempts, want the one that ended and its resume", len(state.Attempts))
	}
	if state.Attempts[1].AutoResumed != string(reasonTmp) {
		t.Errorf("the second attempt is marked %q, want %q: crewflow is the one that went on with it",
			state.Attempts[1].AutoResumed, reasonTmp)
	}
	if last := state.Attempts[1].Outcome; last != BlockedPermission {
		t.Errorf("the last attempt ended as %q, want %q", last, BlockedPermission)
	}
}

// TestRunDoesNotGoOnByItself: a refusal of a place of the machine that is not a habit
// of a run is a thing an orchestrator decides about, and a run that is refused two
// habits at once is not a run crewflow answers with the text of one of them.
func TestRunDoesNotGoOnByItself(t *testing.T) {
	cases := []struct {
		name   string
		stderr string
	}{
		{
			name:   "a place of the machine that is closed for a reason of its own",
			stderr: theRefusalToSsh,
		},
		{
			name: "a habit of the run and a refusal crewflow knows nothing of",
			stderr: "! permission requested: external_directory (/tmp/*); auto-rejecting\n" +
				"! permission requested: external_directory (~/.ssh/config); auto-rejecting\n",
		},
		{
			name:   "a refusal that names no path at all",
			stderr: "! permission requested: do something dangerous; auto-rejecting\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine(t)
			m.says("opencode", answer{stdout: theRun, stderr: tc.stderr})
			host := &host{task: taskOf(43), opened: true}
			cfg := projectOf(t, m.worktrees, "")

			result, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
			if err != nil {
				t.Fatalf("Run returned an error: %v", err)
			}

			if result.Outcome != BlockedPermission {
				t.Errorf("the outcome = %q, want %q", result.Outcome, BlockedPermission)
			}
			if result.Attempt != 1 || result.AutoResumed != "" {
				t.Errorf("the run is the attempt %d (resumed for %q), want the first and no resume",
					result.Attempt, result.AutoResumed)
			}
			if got := len(m.commandsOf("opencode")); got != 1 {
				t.Errorf("the executor was run %d times, want once", got)
			}
			if len(result.Rejections) == 0 {
				t.Error("the result holds no refusal, want what the executor was refused")
			}
		})
	}
}

// TestRunGoesOnByItselfAfterARefusalBesideTheWorktree: the other worktree of the
// project is as much a place of the machine as /tmp is, and the run that reached for
// it goes on by itself with the text of that habit (docs/DESIGN.md §7a, §8).
func TestRunGoesOnByItselfAfterARefusalBesideTheWorktree(t *testing.T) {
	m := newMachine(t)
	cfg := projectOf(t, m.worktrees, "")
	beside := filepath.Join(m.worktrees, "naghuale-crewflow", "44")
	m.says("opencode",
		answer{stdout: theRun, stderr: "! permission requested: external_directory (" + beside + "); auto-rejecting\n"},
		answer{stdout: theRun},
	)
	host := &host{task: taskOf(43), opened: true}

	result, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	if result.Outcome != ChangeRequestOpened || result.Attempt != 2 {
		t.Fatalf("the run is the attempt %d and ended as %q, want the second and %q",
			result.Attempt, result.Outcome, ChangeRequestOpened)
	}
	if result.AutoResumed != string(reasonOutside) {
		t.Errorf("the run went on by itself for %q, want %q", result.AutoResumed, reasonOutside)
	}
	asked := m.commandsOf("opencode")[1].args
	if !strings.Contains(strings.Join(asked, "\n"), "(cd dir && command)") {
		t.Errorf("the second run was asked no rule about a subshell:\n%v", asked)
	}
}

// TestRunGoesOnByItselfAfterAPathOfTheMachineWasOnlyWrittenDown: the case of
// telecli#59 — the run writes a file of the project with a heredoc, the path of the
// machine rides along in the text of it, and the whole command is refused. crewflow
// goes on by itself and tells the run to edit such files with the tools of edit and
// write (docs/DESIGN.md §7a).
func TestRunGoesOnByItselfAfterAPathOfTheMachineWasOnlyWrittenDown(t *testing.T) {
	m := newMachine(t)
	cfg := projectOf(t, m.worktrees, "")
	const application = "~/Library/Application Support/crewflow"
	// The events of a run that is refused a command: the call of the shell with the
	// heredoc is what a run reads to see that the path is only written down.
	events := `{"type":"tool_use","sessionID":"ses_7fKq2","part":{"tool":"bash","state":{"status":"error",` +
		`"input":{"command":"cat > docs/DESIGN.md <<'EOF'\nthe application is ` + application + `\nEOF"}}}}` + "\n" +
		theRun
	m.says("opencode",
		answer{stdout: events, stderr: "! permission requested: external_directory (" + application + "); auto-rejecting\n"},
		answer{stdout: theRun},
	)
	host := &host{task: taskOf(43), opened: true}

	result, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	if result.Outcome != ChangeRequestOpened || result.Attempt != 2 {
		t.Fatalf("the run is the attempt %d and ended as %q, want the second and %q",
			result.Attempt, result.Outcome, ChangeRequestOpened)
	}
	if result.AutoResumed != string(reasonMention) {
		t.Errorf("the run went on by itself for %q, want %q", result.AutoResumed, reasonMention)
	}
	asked := strings.Join(m.commandsOf("opencode")[1].args, "\n")
	if !strings.Contains(asked, "Edit such files with the tools of edit and write") {
		t.Errorf("the second run was asked no rule about editing such files:\n%s", asked)
	}
}

// TestHabitOf is the classifier of a refusal, which is the whole of what crewflow
// knows of the ways an executor stops a run (docs/DESIGN.md §7a).
func TestHabitOf(t *testing.T) {
	worktree := "/home/andrey/.crewflow/worktrees/naghuale-crewflow/43"
	application := "~/Library/Application Support/crewflow"
	// A heredoc of a file of the project that holds a path of the machine in its
	// text: the case of the run of telecli#59, where the whole command was refused
	// for a path the run was only writing down.
	called := []profile.Call{{
		Tool: "bash",
		Argument: "cat > docs/DESIGN.md <<'EOF'\nthe application support of the user is " +
			application + "\nEOF",
	}}
	cases := []struct {
		name     string
		refusal  string
		calls    []profile.Call
		worktree string
		want     reason
	}{
		{
			name:    "a file of the temporary folder, as a pattern",
			refusal: "external_directory /tmp/*",
			want:    reasonTmp,
		},
		{
			name:    "a file of the temporary folder behind its other name",
			refusal: "external_directory /private/tmp/plan_test.go",
			want:    reasonTmp,
		},
		{
			name:     "the worktree of another task of the same project",
			refusal:  "external_directory /home/andrey/.crewflow/worktrees/naghuale-crewflow/44",
			worktree: worktree,
			want:     reasonOutside,
		},
		{
			name:     "the worktree of another task named as a path above the worktree",
			refusal:  "external_directory ../44/run.go",
			worktree: worktree,
			want:     reasonOutside,
		},
		{
			name:     "a path of the machine named in a command that writes a file of the worktree",
			refusal:  "external_directory " + application,
			calls:    called,
			worktree: worktree,
			want:     reasonMention,
		},
		{
			name:     "the same path in a command that writes nothing of the worktree",
			refusal:  "external_directory " + application,
			calls:    []profile.Call{{Tool: "bash", Argument: "ls -l " + application}},
			worktree: worktree,
			want:     reasonOther,
		},
		{
			name:     "the same path read by a tool that is not a shell",
			refusal:  "external_directory " + application,
			calls:    []profile.Call{{Tool: "read", Argument: application}},
			worktree: worktree,
			want:     reasonOther,
		},
		{
			name:     "a place of the machine that is closed for a reason of its own",
			refusal:  "external_directory ~/.ssh/config",
			worktree: worktree,
			want:     reasonOther,
		},
		{
			name:    "the same place where a run of it cannot tell where it stands",
			refusal: "external_directory ~/.ssh/config",
			want:    reasonOther,
		},
		{
			name:     "a place of the machine edited in place by a command of a shell",
			refusal:  "external_directory /Users/someone/.ssh/config",
			calls:    []profile.Call{{Tool: "bash", Argument: "sed -i '' /Users/someone/.ssh/config"}},
			worktree: worktree,
			want:     reasonOther,
		},
		{
			name:    "a path of the machine that a script edits a file of the worktree for",
			refusal: "external_directory /Users/someone/.ssh/config",
			calls: []profile.Call{
				{Tool: "bash", Argument: "sed -i '' -e 's|/Users/someone/.ssh/config|~/.ssh/config|' docs/DESIGN.md"},
			},
			worktree: worktree,
			want:     reasonMention,
		},
		{
			name:    "a refusal of a permission that names no path",
			refusal: "do something dangerous",
			want:    reasonOther,
		},
		{
			name:     "a file of the worktree itself",
			refusal:  "external_directory " + filepath.Join(worktree, "internal/run/run.go"),
			worktree: worktree,
			want:     reasonOther,
		},
		{
			name:     "a folder of the machine that is nowhere near the worktree",
			refusal:  "external_directory /etc/hosts",
			worktree: worktree,
			want:     reasonOther,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := habitOf(tc.refusal, tc.calls, tc.worktree); got != tc.want {
				t.Errorf("the habit of %q = %q, want %q", tc.refusal, got, tc.want)
			}
		})
	}
}

// TestTheAssignmentLeadsWithTheTemporaryFiles: the rule of the temporary files is
// the first of the rules of a run, because it is the one an executor breaks most and
// the one that ends a run at once: a rule in the middle of a list is a rule that is
// read after the third one (docs/DESIGN.md §7a).
func TestTheAssignmentLeadsWithTheTemporaryFiles(t *testing.T) {
	m := newMachine(t)
	m.answers["opencode"] = answer{stdout: theRun}
	host := &host{task: taskOf(43), opened: true}
	cfg := projectOf(t, m.worktrees, "")

	result, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	asked := askedOf(t, m)
	_, rules, found := strings.Cut(asked, "The rules of this run:")
	if !found {
		t.Fatalf("the assignment has no rules in it:\n%s", asked)
	}
	first, _, _ := strings.Cut(strings.TrimLeft(rules, "\n"), "\n- ")
	for _, want := range []string{"`" + Scratch(result.Worktree) + "`", ".scratch/tmp/", "zz_debug_test.go", "/tmp"} {
		if !strings.Contains(first, want) {
			t.Errorf("the first rule of the run does not hold %q:\n%s", want, first)
		}
	}
	if !strings.Contains(first, "ends there") {
		t.Errorf("the first rule of the run does not say what a refusal ends:\n%s", first)
	}
}

// TestTheHabitsAreTextCrewflowCanGive: a habit without a text is a habit crewflow
// cannot answer, and a text that names no place to write is an answer of no use to
// the executor that stopped (docs/DESIGN.md §7a).
func TestTheHabitsAreTextCrewflowCanGive(t *testing.T) {
	for _, want := range []reason{reasonTmp, reasonOutside, reasonMention} {
		one, known := habits[want]
		if !known {
			t.Errorf("crewflow knows no habit %q", want)
			continue
		}
		if one.reason != want {
			t.Errorf("the habit %q is stored as %q", want, one.reason)
		}
		if one.headline == "" || one.text == "" {
			t.Errorf("the habit %q has no text to go on with: %+v", want, one)
		}
	}
	for _, want := range []string{".scratch/tmp", "zz_debug_test.go"} {
		if !strings.Contains(habits[reasonTmp].text, want) {
			t.Errorf("the text of the habit %q does not hold %q", reasonTmp, want)
		}
	}
	if !strings.Contains(habits[reasonOutside].text, "(cd dir && command)") {
		t.Errorf("the text of the habit %q does not hold a subshell", reasonOutside)
	}
}
