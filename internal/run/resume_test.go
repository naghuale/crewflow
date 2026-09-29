package run

import (
	"bytes"
	"encoding/json"
	"os"
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

// theRefusalToASecret is the same kind of refusal about a place of secrets, which is an
// outcome of a run of its own and not a habit of a run (docs/DESIGN.md §7a.1, §7d).
const theRefusalToASecret = "INFO  service=default starting opencode\n" +
	"! permission requested: external_directory (~/.ssh/id_ed25519); auto-rejecting\n"

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
			name:   "a place of the machine that crewflow opens nobody",
			stderr: "! permission requested: external_directory (/opt/homebrew/include); auto-rejecting\n",
		},
		{
			name: "a habit of the run and a refusal crewflow knows nothing of",
			stderr: "! permission requested: external_directory (/tmp/*); auto-rejecting\n" +
				"! permission requested: external_directory (/etc/hosts); auto-rejecting\n",
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

// TestASecretBesideTheWorktreeIsStillASecret: the habits are told apart before the
// temporary folder, and the check of a place of secrets is ahead of every habit of them
// — a worktree of a project that a project made under a place of secrets, and a refusal
// of the folder beside it, is a refusal of that place and not a wrong `cd`. The habits
// are about where a run keeps its temporary files and how it addresses a folder; a key
// is a key in whatever folder of the machine it is named in (docs/DESIGN.md §7a.1, §7d,
// §8).
func TestASecretBesideTheWorktreeIsStillASecret(t *testing.T) {
	m := newMachine(t)
	// The worktrees of the project are made where a place of secrets is, which no
	// project would name and this test has to: it is the only way the two checks of a
	// run meet on one refusal.
	m.worktrees = filepath.Join(m.userHome, ".ssh")
	beside := filepath.Join(m.worktrees, "naghuale-crewflow", "44")
	m.answers["opencode"] = answer{
		stdout: theCall("cd "+beside+" && ls") + theRun,
		stderr: "! permission requested: external_directory (" + beside + "); auto-rejecting\n",
	}
	host := &host{task: taskOf(43), opened: true}

	result, err := Run(t.Context(), m.env(), projectOf(t, m.worktrees, ""), host.set(),
		Request{Number: 43, RepoDir: m.repo})
	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	if result.Outcome != BlockedSecret {
		t.Fatalf("the outcome = %q, want %q: a place of secrets is a secret in whatever folder it is in",
			result.Outcome, BlockedSecret)
	}
	if result.Attempt != 1 || result.AutoResumed != "" {
		t.Errorf("the run is the attempt %d (resumed for %q), want the first and no resume",
			result.Attempt, result.AutoResumed)
	}
	if got := len(m.commandsOf("opencode")); got != 1 {
		t.Errorf("the executor was run %d times, want once", got)
	}
	if len(result.Rejections) == 0 {
		t.Fatalf("the result holds no refusal, want the one of the run")
	}
	for _, want := range []string{beside, recoveryDisabled} {
		if !strings.Contains(result.Rejections[0], want) {
			t.Errorf("the refusal %q does not hold %q", result.Rejections[0], want)
		}
	}
	// The habit of that refusal is the wrong `cd` — a folder beside the worktree — and
	// the run is stopped before the habits are looked at. Saying so here is what keeps
	// the two checks in the order they are in: a run that reached a key is a run that
	// reached a key, whatever the folder of it looks like.
	refused, _, _ := strings.Cut(result.Rejections[0], " — ")
	worktree := filepath.Join(m.worktrees, "naghuale-crewflow", "43")
	if got := (&runner{worktree: worktree}).habitOf(refused, nil); got != reasonOutside {
		t.Errorf("the habit of %q is %q, want %q beside a worktree", refused, got, reasonOutside)
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
	// The home of the person is one for the whole test, with a link above it, and the
	// places of secrets are the ones of it: a run reads its closed places against the
	// home it was given, a test whose places came from another home would be testing a
	// machine that is not the one the run is on, and a test whose home has no link above
	// it would never see the two names a machine of macOS gives one folder (§7d).
	home := homeUnderALink(t)
	// The three shapes a run is answered by itself on, each of them reaching for the
	// place of secrets on the way: the temporary folder of the machine, a path that
	// climbs out of the worktree, and a heredoc that writes a file of the project with
	// the place of secrets in the text of it.
	shapes := []struct {
		name    string
		refusal func(t *testing.T, worktree, place string) string
		command func(t *testing.T, worktree, place string) string
		// named is what the report has to name as the place of secrets: the place
		// itself where the refusal is of something else, and the refused path where the
		// refusal is of the place as the run wrote it.
		named func(place, refused string) string
		kind  string
	}{
		{
			name:    "a write under /tmp that reads a secret",
			refusal: func(*testing.T, string, string) string { return "/tmp/*" },
			command: func(_ *testing.T, _, place string) string { return "cat '" + place + "' > /tmp/copy" },
			named:   func(place, _ string) string { return place },
			kind:    accessRead,
		},
		{
			name: "a path that climbs out of the worktree to a secret",
			refusal: func(t *testing.T, worktree, place string) string {
				return climbedTo(t, worktree, place)
			},
			command: func(t *testing.T, worktree, place string) string {
				return "cat '" + climbedTo(t, worktree, place) + "' > docs/notes.md"
			},
			named: func(_, refused string) string { return refused },
			kind:  accessRead,
		},
		{
			name:    "a heredoc that writes a secret into a file of the worktree",
			refusal: func(_ *testing.T, _, place string) string { return place },
			command: func(_ *testing.T, _, place string) string {
				return "cat > docs/DESIGN.md <<'EOF'\nthe key is in " + place + "\nEOF"
			},
			named: func(_, refused string) string { return refused },
			kind:  accessText,
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

				if result.Outcome != BlockedSecret {
					t.Errorf("the outcome = %q, want %q: a run that reaches a secret stops for the orchestrator",
						result.Outcome, BlockedSecret)
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
				named := shape.named(place, refused)
				// The report and the journal name the place as the run wrote it and the
				// other name of the same folder where the machine gives it one: a person
				// greps for the path the refusal named and reads the other beside it.
				wantPlaceNamed(t, result.Rejections[0], home, named)
				for _, want := range []string{shape.kind, recoveryDisabled} {
					if !strings.Contains(result.Rejections[0], want) {
						t.Errorf("the refusal %q does not hold %q", result.Rejections[0], want)
					}
				}
				journal := read(t, result.Journal)
				wantPlaceNamed(t, journal, home, named)
				for _, want := range []string{shape.kind, recoveryDisabled} {
					if !strings.Contains(journal, want) {
						t.Errorf("the journal of the attempt holds no %q:\n%s", want, journal)
					}
				}
			})
		}
	}
}

// TestRunReachesASecretAndStops is the manual check of §7a.1: each of the ways a run
// reaches for a place of secrets — reading a key, changing folder into one, searching a
// token, the other spelling of the path on the machine, and naming one in the text of a
// command — is an outcome of a run of its own, the report says the place and the kind of
// access, and nothing goes on by itself (docs/DESIGN.md §7a.1, §7d).
func TestRunReachesASecretAndStops(t *testing.T) {
	// The home of the person is under a link, so that the place of secrets of the case
	// of the other spelling of the machine is really a second name of the same folder
	// and not a path of its own (docs/DESIGN.md §7d).
	home := homeUnderALink(t)
	// The other spelling of a place of secrets on this machine: on a machine of macOS
	// /var leads to /private/var, and a run is refused for a path in the spelling it
	// wrote (docs/DESIGN.md §7d).
	followed, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatalf("follow %s: %v", home, err)
	}
	cases := []struct {
		name    string
		refused string
		command string
		kind    string
	}{
		{
			name:    "reading a key",
			refused: "~/.ssh/config",
			command: "cat ~/.ssh/config",
			kind:    accessRead,
		},
		{
			name:    "changing folder into a place of secrets",
			refused: "~/.ssh",
			command: "cd ~/.ssh && ls",
			kind:    accessCd,
		},
		{
			name:    "searching a token",
			refused: "~/.netrc",
			command: "grep x ~/.netrc",
			kind:    accessRead,
		},
		{
			name:    "a place of secrets in the other spelling of the machine",
			refused: filepath.Join(followed, ".ssh", "config"),
			command: "cat " + filepath.Join(followed, ".ssh", "config"),
			kind:    accessRead,
		},
		{
			name:    "naming a place of secrets in the text of a command",
			refused: "~/.aws/credentials",
			command: "cat > docs/DESIGN.md <<'EOF'\nthe credentials are in ~/.aws/credentials\nEOF",
			kind:    accessText,
		},
		{
			name:    "writing into a place of secrets",
			refused: "~/.aws/credentials",
			command: "echo token > ~/.aws/credentials",
			kind:    accessWrite,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine(t)
			m.userHome = home
			m.answers["opencode"] = answer{
				stdout: theCall(tc.command) + theRun,
				stderr: "! permission requested: external_directory (" + tc.refused + "); auto-rejecting\n",
			}
			host := &host{task: taskOf(43), opened: true}

			result, err := Run(t.Context(), m.env(), projectOf(t, m.worktrees, ""), host.set(),
				Request{Number: 43, RepoDir: m.repo})
			if err != nil {
				t.Fatalf("Run returned an error: %v", err)
			}

			if result.Outcome != BlockedSecret {
				t.Fatalf("the outcome = %q, want %q for %q", result.Outcome, BlockedSecret, tc.command)
			}
			if result.Attempt != 1 || result.AutoResumed != "" {
				t.Errorf("the run is the attempt %d (resumed for %q), want the first and no resume",
					result.Attempt, result.AutoResumed)
			}
			if got := len(m.commandsOf("opencode")); got != 1 {
				t.Errorf("the executor was run %d times, want once", got)
			}
			if len(result.Rejections) != 1 {
				t.Fatalf("the result holds the refusals %q, want the one of the run", result.Rejections)
			}
			// The place is named as the run wrote it, and the report and the journal
			// say the same of it: two names of one folder are one place (§7d).
			wantPlaceNamed(t, result.Rejections[0], home, tc.refused)
			wantPlaceNamed(t, read(t, result.Journal), home, tc.refused)
			for _, want := range []string{tc.kind, recoveryDisabled} {
				if !strings.Contains(result.Rejections[0], want) {
					t.Errorf("the refusal %q does not hold %q", result.Rejections[0], want)
				}
			}
		})
	}
}

// TestRunAfterASecretIsNeverResumedByItself: a task that was stopped for reaching a
// secret is not taken up by crewflow again by itself, whatever the next attempt is
// refused for — a run that reaches a key is a thing a person decides about, and a habit
// answered on the way to that decision is crewflow deciding it instead (§7a.1, §8).
func TestRunAfterASecretIsNeverResumedByItself(t *testing.T) {
	m := newMachine(t)
	m.says("opencode",
		answer{stdout: theRun, stderr: theRefusalToASecret},
		answer{stdout: theRun, stderr: theRefusalToTmp},
		answer{stdout: theRun},
	)
	host := &host{task: taskOf(43), opened: true}
	cfg := projectOf(t, m.worktrees, "")

	stopped, err := Run(t.Context(), m.env(), cfg, host.set(), Request{Number: 43, RepoDir: m.repo})
	if err != nil {
		t.Fatalf("the first run returned an error: %v", err)
	}
	if stopped.Outcome != BlockedSecret {
		t.Fatalf("the first run ended as %q, want %q", stopped.Outcome, BlockedSecret)
	}

	// The orchestrator goes on in the same session, and the run is refused its scratch
	// folder: a habit crewflow answers by itself everywhere else, and nowhere here.
	result, err := Run(t.Context(), m.env(), cfg, host.set(),
		Request{Number: 43, RepoDir: m.repo, Continue: "go on with the task"})
	if err != nil {
		t.Fatalf("the second run returned an error: %v", err)
	}

	if result.Outcome != BlockedPermission {
		t.Errorf("the second run ended as %q, want %q: nothing follows a run that reached a secret",
			result.Outcome, BlockedPermission)
	}
	if result.Attempt != 2 || result.AutoResumed != "" {
		t.Errorf("the second run is the attempt %d (resumed for %q), want the second and no resume",
			result.Attempt, result.AutoResumed)
	}
	if got := len(m.commandsOf("opencode")); got != 2 {
		t.Errorf("the executor was run %d times, want the run that reached a secret and the one after it", got)
	}
}

// TestRunThatReachedASecretIsBlockedSecretWhateverEndedIt: an attempt to reach a secret
// stops the run as `blocked-secret` whatever ended the attempt — a person who stopped it,
// the time of the project that ran out, the executor that failed — and a habit in the
// same attempt does not make it a habit (docs/DESIGN.md §7a.1, §8).
func TestRunThatReachedASecretIsBlockedSecretWhateverEndedIt(t *testing.T) {
	cases := []struct {
		name string
		// hangs is a run that ran out of time, and code is the code an executor that
		// failed exits with. Neither of them hides the secret.
		hangs bool
		code  int
	}{
		{name: "a run that ran out of time", hangs: true},
		{name: "an executor that failed", code: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine(t)
			timeout := ""
			if tc.hangs {
				timeout = "20ms"
			}
			m.answers["opencode"] = answer{stdout: theRun, stderr: theRefusalToASecret, hangs: tc.hangs, code: tc.code}
			host := &host{task: taskOf(43), opened: true}

			result, err := Run(t.Context(), m.env(), projectOf(t, m.worktrees, timeout), host.set(),
				Request{Number: 43, RepoDir: m.repo})
			if err != nil {
				t.Fatalf("Run returned an error: %v", err)
			}

			if result.Outcome != BlockedSecret {
				t.Errorf("the outcome = %q, want %q: the secret is what a person has to see", result.Outcome, BlockedSecret)
			}
			if got := len(m.commandsOf("opencode")); got != 1 {
				t.Errorf("the executor was run %d times, want once", got)
			}
			if len(result.Rejections) == 0 || !strings.Contains(result.Rejections[0], recoveryDisabled) {
				t.Errorf("the result holds the refusals %q, want the one of the run with the place in it", result.Rejections)
			}
		})
	}
}

// homeUnderALink is a home of the person for a test of the places of secrets, with a
// link above it: on a machine of macOS `/var` leads to `/private/var`, the deny list
// holds a place in both names, and a test whose home has no link above it sees only one
// of them — a test of the two names that has never seen two (docs/DESIGN.md §7d).
func homeUnderALink(t *testing.T) string {
	t.Helper()
	link := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatalf("link %s: %v", link, err)
	}
	return link
}

// closedOf is the places that stay closed to the executor on the machine of a test,
// worked out the way a run works them out — from the access policy of the project and
// the home of the person — so that a test of a report reads the same list a run does
// and not a list made up for it (docs/DESIGN.md §7d).
func closedOf(t *testing.T, home string) closed {
	t.Helper()
	policy, _ := access.Resolve(t.Context(), access.Env{Home: home}, config.Access{})
	return newClosed(policy, home)
}

// TestAReportNamesThePlaceAsTheRunWroteIt: on a machine where one folder has two names —
// `/var/folders/…/.ssh` and `/private/var/folders/…/.ssh` — a report names the one the
// refusal or the command said, because that is the path a person greps for in a journal,
// and the other beside it (docs/DESIGN.md §7d).
func TestAReportNamesThePlaceAsTheRunWroteIt(t *testing.T) {
	home := homeUnderALink(t)
	written := filepath.Join(home, ".ssh", "id_ed25519")
	followed := filepath.Join(onThisMachine(t, home), ".ssh", "id_ed25519")
	if written == followed {
		t.Skipf("the machine gives %s one name only, and there is nothing to tell apart", home)
	}
	places := closedOf(t, home)
	cases := []struct {
		name    string
		refusal string
		calls   []profile.Call
		// note is what the report and the journal say about the refusal.
		note string
	}{
		{
			name:    "the refusal names the place",
			refusal: "external_directory " + written,
			note:    "a secret, closed to the executor whatever the project wrote",
		},
		{
			name:    "a command of the run names the place",
			refusal: "external_directory /tmp/*",
			calls:   []profile.Call{{Tool: "bash", Argument: "cat " + followed + " > /tmp/copy"}},
			note:    "the command refused names a secret",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			answer := places.found([]string{tc.refusal}, tc.calls, filepath.Join(home, "worktree"))
			if len(answer.reached) != 1 {
				t.Fatalf("the run reached %+v, want one place", answer.reached)
			}
			// The place the run wrote, and the other name of the folder where this
			// machine has one for it: a path that is already the resolved one has no
			// other name, and there is nothing to add to it.
			wrote := written
			if len(tc.calls) > 0 {
				wrote = followed
			}
			said, other := wrote, other(t, home, wrote)
			if other != wrote {
				said += " (also " + other + ")"
			}
			if answer.reached[0].said != said {
				t.Errorf("the report names the place %q, want %q", answer.reached[0].said, said)
			}
			if !strings.Contains(answer.marked[0], tc.note) {
				t.Errorf("the refusal %q says nothing about a secret", answer.marked[0])
			}
			line := answer.line()
			if !strings.Contains(line, wrote) || (other != wrote && !strings.Contains(line, other)) {
				t.Errorf("the journal holds %q, want the place the run wrote and the other name of it", line)
			}
		})
	}
}

// other is the name of a place of secrets that is not the one it was written in: the
// path with the links above it followed, which on a machine of macOS is
// `/private/var/…` where the run wrote `/var/…` (docs/DESIGN.md §7d).
func other(t *testing.T, home, place string) string {
	t.Helper()
	rest, isUnder := strings.CutPrefix(place, filepath.Clean(home))
	if !isUnder {
		return place
	}
	return filepath.Join(onThisMachine(t, home), rest)
}

// wantPlaceNamed checks the place of secrets a report or a journal names: the path as
// the run wrote it, which may be either of the two names this machine gives the folder,
// and the other name beside it where the two are not the same. The paths are compared as
// the machine holds them and not as the letters of them, because two names of one folder
// are one place (docs/DESIGN.md §7d).
func wantPlaceNamed(t *testing.T, said, home, want string) {
	t.Helper()
	note := noteOn(t, said)
	named := placeNamed(t, note)
	if !samePlace(t, home, named, want) {
		t.Errorf("%q names the place %q, want the place %q", note, named, want)
	}
	if other := underHome(t, home, named); other != named && !strings.Contains(note, other) {
		t.Errorf("%q names the place %q and no other name of it, want %q as well", note, named, other)
	}
}

// noteOn is the line of a report or of a journal that tells what the run reached for,
// which is the line the note of the place is in — a journal holds the whole run and the
// answer of the run is one line of it.
func noteOn(t *testing.T, said string) string {
	t.Helper()
	for line := range strings.Lines(said) {
		if strings.Contains(line, recoveryDisabled) {
			return line
		}
	}
	t.Fatalf("nothing of %q tells what the run reached for", said)
	return ""
}

// placeNamed is the place of secrets the note names, and it is what stands between the
// colon the note ends its words with and the kind of access in it.
func placeNamed(t *testing.T, note string) string {
	t.Helper()
	head, _, hasKind := strings.Cut(note, " (")
	if !hasKind {
		t.Fatalf("the line %q names no place of secrets and no kind of access", note)
	}
	at := strings.LastIndex(head, ": ")
	if at < 0 {
		t.Fatalf("the line %q has no note about a place of secrets", note)
	}
	return strings.TrimSpace(head[at+len(": "):])
}

// samePlace says whether two paths name one folder of the machine: the parts of them
// under the home of the person, with the home as the machine holds it (docs/DESIGN.md
// §7d).
func samePlace(t *testing.T, home, got, want string) bool {
	t.Helper()
	return underHome(t, home, got) == underHome(t, home, want)
}

// underHome is a path of a test as the machine spells it: the part of it under the home
// as it was given, under the home with the links above it followed. A path that is not
// under the home is returned as it is — the patterns of the closed places are files of
// the worktree and have no home above them.
func underHome(t *testing.T, home, path string) string {
	t.Helper()
	clean := filepath.Clean(path)
	rest, isUnder := strings.CutPrefix(clean, filepath.Clean(home))
	if !isUnder {
		return clean
	}
	return filepath.Join(onThisMachine(t, home), rest)
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
			// The worktrees of a project may live in the temporary folder of the machine
			// — a machine whose TMPDIR says so has every worktree of every project under
			// `/tmp` — and a folder beside the worktree of a task is the wrong `cd` of a
			// run there as everywhere else. The habits are told apart before the
			// temporary folder is looked at, or a run that walked out of its worktree
			// would be told to keep its scratch (docs/DESIGN.md §7a).
			name:     "the worktree of another task where the worktrees live in the temporary folder",
			refusal:  "external_directory /tmp/crewflow-test/worktrees/owner-repo/telemetry",
			worktree: "/tmp/crewflow-test/worktrees/owner-repo/7",
			want:     reasonOutside,
		},
		{
			// The same machine and a path named only in the text of a command that writes
			// a file of the worktree: the other habit that a folder under the temporary
			// folder used to be read as.
			name:     "a path named in a command where the worktrees live in the temporary folder",
			refusal:  "external_directory /var/lib/telemetry",
			calls:    []profile.Call{{Tool: "bash", Argument: "cat > docs/DESIGN.md <<'EOF'\nthe telemetry is in /var/lib/telemetry\nEOF"}},
			worktree: "/tmp/crewflow-test/worktrees/owner-repo/7",
			want:     reasonMention,
		},
		{
			// The same worktree and a path of the temporary folder of the machine that is
			// not beside it: this one is the temporary folder, wherever the worktrees are.
			name:     "a file of the temporary folder beside no worktree of a task",
			refusal:  "external_directory /tmp/scratch/plan_test.go",
			worktree: "/tmp/crewflow-test/worktrees/owner-repo/7",
			want:     reasonTmp,
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &runner{worktree: tc.worktree}
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
