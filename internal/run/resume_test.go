package run

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/naghuale/crewflow/internal/access"
	"github.com/naghuale/crewflow/internal/config"
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

// theCall is the event of a run that called a shell with a command that was refused:
// where a run reads the commands of a run from, and what tells it that a path of the
// machine was only written down in one of them (docs/DESIGN.md §7a).
func theCall(command string) string {
	quoted, err := json.Marshal(command)
	if err != nil {
		panic(err)
	}
	return `{"type":"tool_use","sessionID":"ses_7fKq2","part":{"tool":"bash","state":{"status":"error",` +
		`"input":{"command":` + string(quoted) + `}}}}` + "\n"
}

// TestRunDoesNotGoOnByItselfAfterASecret: a place of secrets is no habit of anybody,
// and a run that was refused one is not a run crewflow answers by itself — whatever the
// shape of the command it was refused in. Every place of the deny list of the machine
// is reached here in each of the three shapes a run is answered by itself on, and the
// run of every one of them stops for the orchestrator with the refusal said to be what
// it is (docs/DESIGN.md §7a, §7d).
func TestRunDoesNotGoOnByItselfAfterASecret(t *testing.T) {
	// The home of the person is one for the whole test, and the places of secrets are
	// the ones of it: a run reads its closed places against the home it was given, and
	// a test whose places came from another home would be testing a machine that is
	// not the one the run is on (docs/DESIGN.md §7d).
	home := t.TempDir()
	// The three shapes a run is answered by itself on, each of them reaching for the
	// place of secrets on the way: the temporary folder of the machine, a path that
	// climbs out of the worktree, and a heredoc that writes a file of the project with
	// the place of secrets in the text of it.
	shapes := []struct {
		name    string
		refusal func(t *testing.T, worktree, place string) string
		command func(t *testing.T, worktree, place string) string
		note    string
	}{
		{
			name:    "a write under /tmp that reads a secret",
			refusal: func(*testing.T, string, string) string { return "/tmp/*" },
			command: func(_ *testing.T, _, place string) string { return "cat '" + place + "' > /tmp/copy" },
			note:    secretCommand,
		},
		{
			name: "a path that climbs out of the worktree to a secret",
			refusal: func(t *testing.T, worktree, place string) string {
				return climbedTo(t, worktree, place)
			},
			command: func(t *testing.T, worktree, place string) string {
				return "cat '" + climbedTo(t, worktree, place) + "' > docs/notes.md"
			},
			note: secretPath,
		},
		{
			name:    "a heredoc that writes a secret into a file of the worktree",
			refusal: func(_ *testing.T, _, place string) string { return place },
			command: func(_ *testing.T, _, place string) string {
				return "cat > docs/DESIGN.md <<'EOF'\nthe key is in " + place + "\nEOF"
			},
			note: secretPath,
		},
	}
	for _, place := range placesOfSecrets(t, home) {
		for _, shape := range shapes {
			t.Run(place+" — "+shape.name, func(t *testing.T) {
				m := newMachine(t)
				m.userHome = home
				worktree := filepath.Join(m.worktrees, "naghuale-crewflow", "43")
				refused := shape.refusal(t, worktree, place)
				m.answers["opencode"] = answer{
					stdout: theCall(shape.command(t, worktree, place)) + theRun,
					stderr: "! permission requested: external_directory (" + refused + "); auto-rejecting\n",
				}
				host := &host{task: taskOf(43), opened: true}

				result, err := Run(t.Context(), m.env(), projectOf(t, m.worktrees, ""), host.set(),
					Request{Number: 43, RepoDir: m.repo})
				if err != nil {
					t.Fatalf("Run returned an error: %v", err)
				}

				if result.Outcome != BlockedPermission {
					t.Errorf("the outcome = %q, want %q: a run that reads a secret stops for the orchestrator",
						result.Outcome, BlockedPermission)
				}
				if result.Attempt != 1 || result.AutoResumed != "" {
					t.Errorf("the run is the attempt %d (resumed for %q), want the first and no resume",
						result.Attempt, result.AutoResumed)
				}
				if got := len(m.commandsOf("opencode")); got != 1 {
					t.Errorf("the executor was run %d times, want once", got)
				}
				// The report says what the refusal was about, so that a person reading
				// the run does not read a key among the habits of a run.
				if len(result.Rejections) != 1 {
					t.Fatalf("the result holds the refusals %q, want the one of the run", result.Rejections)
				}
				for _, want := range []string{refused, shape.note} {
					if !strings.Contains(result.Rejections[0], want) {
						t.Errorf("the refusal %q does not hold %q", result.Rejections[0], want)
					}
				}
			})
		}
	}
}

// placesOfSecrets is every place the deny list holds for the home given, as a path
// that can be refused: the folders of the machine in every spelling of it, and the
// patterns of every machine as a file of a worktree, which is where they are written at
// on every machine (docs/DESIGN.md §7d).
func placesOfSecrets(t *testing.T, home string) []string {
	t.Helper()
	policy, _ := access.Resolve(t.Context(), access.Env{Home: home}, config.Access{})
	if len(policy.Deny) == 0 {
		t.Fatal("the policy of a machine has no closed place, so there is nothing to test")
	}
	worktree := filepath.Join(t.TempDir(), "naghuale-crewflow", "43")
	var places []string
	for _, place := range policy.Deny {
		if _, tail, isPattern := strings.Cut(place, "**/"); isPattern {
			place = filepath.Join(worktree, strings.ReplaceAll(tail, "*", "local"))
		}
		places = append(places, place)
	}
	return places
}

// climbedTo is the place of secrets as a path out of the worktree, which is what a
// shell of a run writes when it climbs to a key: `..` and what is under it, however
// deep the worktree lies (docs/DESIGN.md §7a, §7d).
func climbedTo(t *testing.T, worktree, place string) string {
	t.Helper()
	climbed, err := filepath.Rel(worktree, place)
	if err != nil {
		t.Fatalf("the path of %s from %s: %v", place, worktree, err)
	}
	return climbed
}

// closedOf is the places that stay closed to the executor on the machine of a test,
// worked out the way a run works them out — from the access policy of the project and
// the home of the person — so that a test of the classifier reads the same list a run
// does and not a list made up for it (docs/DESIGN.md §7d).
func closedOf(t *testing.T, home string) closed {
	t.Helper()
	policy, _ := access.Resolve(t.Context(), access.Env{Home: home}, config.Access{})
	return newClosed(policy, home)
}

// TestHabitOf is the classifier of a refusal, which is the whole of what crewflow
// knows of the ways an executor stops a run (docs/DESIGN.md §7a).
func TestHabitOf(t *testing.T) {
	home := "/home/andrey"
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
			name:    "a path of the machine a script edits a file of the worktree for",
			refusal: "external_directory /Users/someone/Library/Preferences/crewflow",
			calls: []profile.Call{
				{Tool: "bash", Argument: "sed -i '' -e 's|/Users/someone/Library/Preferences/crewflow|~|' docs/DESIGN.md"},
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
			name:     "a refusal of a permission that names no path, beside a command with a secret in it",
			refusal:  "do something dangerous",
			calls:    []profile.Call{{Tool: "bash", Argument: "cat '" + home + "/.ssh/id_ed25519' > /tmp/copy"}},
			worktree: worktree,
			want:     reasonOther,
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
		// A place of secrets is no habit of anybody, whatever the shape of the command
		// it came in: these three are the shapes a run is answered by itself on, and
		// none of them makes a run that was reading a key a run that forgot where its
		// scratch is (docs/DESIGN.md §7d).
		{
			name:    "a place of secrets in the spelling the run wrote it in",
			refusal: "external_directory ~/.ssh/id_ed25519",
			want:    reasonOther,
		},
		{
			name:     "a place of secrets in the spelling of the machine",
			refusal:  "external_directory /home/andrey/.ssh/id_ed25519",
			worktree: worktree,
			want:     reasonOther,
		},
		{
			name:    "a place of secrets behind the other name of the machine",
			refusal: "external_directory /private/tmp/../../../home/andrey/.ssh/config",
			want:    reasonOther,
		},
		{
			name:     "a place of secrets a command climbed to out of the worktree",
			refusal:  "external_directory ../../../../.ssh/config",
			worktree: worktree,
			want:     reasonOther,
		},
		{
			name:     "a file of a pattern of secrets inside the worktree",
			refusal:  "external_directory " + filepath.Join(worktree, ".env"),
			worktree: worktree,
			want:     reasonOther,
		},
		{
			name:     "a place of secrets named in a command that writes a file of the worktree",
			refusal:  "external_directory /tmp/*",
			calls:    []profile.Call{{Tool: "bash", Argument: "cat > /tmp/copy <<'EOF'\n" + home + "/.ssh/id_ed25519\nEOF"}},
			worktree: worktree,
			want:     reasonOther,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &runner{worktree: tc.worktree, closed: closedOf(t, home)}
			if got := r.habitOf(tc.refusal, tc.calls); got != tc.want {
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
