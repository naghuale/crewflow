package profile

import (
	"slices"
	"strings"
	"testing"
)

// The answers below are written after the events of the pilot runs of
// docs/DESIGN.md §7a: a line of JSON per event, each with the session it belongs
// to and the part of it, and the refusals of a run on the way out. They are
// shortened, and nothing else about them is guessed: a profile that reads a field
// the agent does not send reads nothing, and that is a shape of a run that
// another version of the agent may change.

// opencodeRun is a run of OpenCode that did the work and said where it stood: the
// text of every part of the answer carries the session it belongs to.
const opencodeRun = `{"type":"step-start","sessionID":"ses_7fKq2"}
{"type":"text","sessionID":"ses_7fKq2","part":{"type":"text","text":"I read the task and started on it."}}
{"type":"tool-use","sessionID":"ses_7fKq2","part":{"type":"tool","tool":"bash","state":{"status":"completed"}}}
{"type":"text","sessionID":"ses_7fKq2","part":{"type":"text","text":"The change request is open."}}
`

// opencodeRefused is a run that stopped at the first question about a path outside
// the worktree, and the answer of the agent to the question went to stderr.
const opencodeRefused = `{"type":"text","sessionID":"ses_9Lm4x","part":{"type":"text","text":"Writing the note outside the worktree."}}
`

// refusalsOfRun is what stderr holds of that run: the question, and the fact that
// nobody was there to answer it.
const refusalsOfRun = "" +
	"INFO  service=default starting opencode\n" +
	"! permission requested: external_directory (/tmp/*); auto-rejecting\n" +
	"! permission requested: external_directory (/Users/someone/**); auto-rejecting\n"

// opencodeBlocked is a run of an agent that stopped by itself and said why, as
// crewflow asks it to: the last message of the answer begins with the word.
const opencodeBlocked = `{"type":"text","sessionID":"ses_3Kd8","part":{"type":"text","text":"I cannot install a package."}}
{"type":"text","sessionID":"ses_3Kd8","part":{"type":"text","text":"BLOCKED: needs libtdjson — the build cannot find it"}}
`

// TestSessionID checks that the session of a run is found in the stream of events
// of the agent: a run that knows its session can be continued in it, and one that
// does not can only be begun again.
func TestSessionID(t *testing.T) {
	cases := []struct {
		name   string
		events string
		want   string
	}{
		{name: "a run that did the work", events: opencodeRun, want: "ses_7fKq2"},
		{name: "a run that was refused", events: opencodeRefused, want: "ses_9Lm4x"},
		{name: "a stream with no session in it", events: `{"type":"text","part":{"text":"hello"}}`, want: ""},
		{name: "no events at all", events: "", want: ""},
		{name: "an event of a shape crewflow does not know", events: "{not json}\n" + opencodeRun, want: "ses_7fKq2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := (opencode{}).SessionID([]byte(tc.events)); got != tc.want {
				t.Errorf("SessionID = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRejections checks what a refused permission is read as: one entry per
// refusal, with the kind of it and the path of it, because that is what an
// orchestrator decides about (docs/DESIGN.md §7d).
func TestRejections(t *testing.T) {
	cases := []struct {
		name   string
		stderr string
		want   []string
	}{
		{
			name:   "a path outside the worktree",
			stderr: refusalsOfRun,
			want:   []string{"external_directory /tmp/*", "external_directory /Users/someone/**"},
		},
		{
			name:   "a command",
			stderr: "! permission requested: bash (rm -rf /); auto-rejecting\n",
			want:   []string{"bash rm -rf /"},
		},
		{
			name:   "a refusal with nothing in parentheses",
			stderr: "! permission requested: webfetch; auto-rejecting\n",
			want:   []string{"webfetch"},
		},
		{
			name:   "stderr with no refusal in it",
			stderr: "INFO  service=default starting opencode\n",
			want:   nil,
		},
		{name: "no stderr at all", stderr: "", want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := opencode{}.Rejections(nil, []byte(tc.stderr))

			if !slices.Equal(got, tc.want) {
				t.Errorf("Rejections = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestBlocked checks that a run that stopped by itself is understood, and that the
// reason it gave is what a person is told.
func TestBlocked(t *testing.T) {
	cases := []struct {
		name   string
		events string
		want   string
	}{
		{
			name:   "the agent stopped and said why",
			events: opencodeBlocked,
			want:   "needs libtdjson — the build cannot find it",
		},
		{
			name:   "a run that did not stop",
			events: opencodeRun,
			want:   "",
		},
		{
			name:   "the word in the middle of a message",
			events: `{"type":"text","part":{"text":"I was not BLOCKED: I did the work."}}`,
			want:   "",
		},
		{
			name: "the last of several stops is the one that counts",
			events: `{"type":"text","part":{"text":"BLOCKED: first"}}
{"type":"text","part":{"text":"BLOCKED: second"}}`,
			want: "second",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := (opencode{}).Blocked([]byte(tc.events)); got != tc.want {
				t.Errorf("Blocked = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestContinueArgs checks how a run goes on in the same session of OpenCode: the
// id of the session is the argument that says it (docs/DESIGN.md §7a).
func TestContinueArgs(t *testing.T) {
	got := opencode{}.ContinueArgs("ses_7fKq2")

	want := []string{"--session", "ses_7fKq2"}
	if !slices.Equal(got, want) {
		t.Errorf("ContinueArgs = %q, want %q", got, want)
	}
}

// TestGeneric walks what crewflow can say about an agent it has never run: no
// session to name, no refusal to read, and no session to continue in — but the
// word BLOCKED is understood all the same, because that is crewflow's own word for
// a stop, whatever the agent is.
func TestGeneric(t *testing.T) {
	answer := "I read the task and worked on it.\nBLOCKED: the task does not say which branch to use\n"

	if got := (generic{}).SessionID([]byte(answer)); got != "" {
		t.Errorf("SessionID = %q, want no session: this agent has none crewflow can see", got)
	}
	if got := (generic{}).Rejections([]byte(answer), nil); got != nil {
		t.Errorf("Rejections = %v, want none: this agent does not say so where crewflow can see it", got)
	}
	if got := (generic{}).ContinueArgs("ses_7fKq2"); got != nil {
		t.Errorf("ContinueArgs = %q, want none: a run of this agent cannot go on in a session", got)
	}
	want := "the task does not say which branch to use"
	if got := (generic{}).Blocked([]byte(answer)); got != want {
		t.Errorf("Blocked = %q, want %q", got, want)
	}
}

// TestFor walks which profile a command line of a project gets: the one of OpenCode
// when the program is OpenCode, and the generic one for anything else. crewflow
// knows no agent it has not run, and guessing is how a run is read wrong
// (docs/DESIGN.md §7b).
func TestFor(t *testing.T) {
	cases := []struct {
		name    string
		command []string
		want    string
	}{
		{
			name:    "opencode in the settings of a project",
			command: []string{"opencode", "run", "--dir", "{worktree}", "{prompt}"},
			want:    "opencode",
		},
		{
			name:    "opencode by its whole path",
			command: []string{"/opt/homebrew/bin/opencode", "run", "{prompt}"},
			want:    "opencode",
		},
		{
			name:    "another agent",
			command: []string{"claude", "-p", "{prompt}"},
			want:    "generic",
		},
		{
			name:    "a program named like it by accident",
			command: []string{"/tmp/opencode-2", "run", "{prompt}"},
			want:    "generic",
		},
		{
			name:    "no command at all",
			command: nil,
			want:    "generic",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			profile := For(tc.command)

			if profile.Name() != tc.want {
				t.Errorf("For(%q) = the profile %q, want %q", tc.command, profile.Name(), tc.want)
			}
		})
	}
}

// TestBlockedIsNotAWordOfAMessage checks the line a run of any agent is stopped by:
// the word has to stand at the start of the message, or every answer that mentions
// it would stop the run.
func TestBlockedIsNotAWordOfAMessage(t *testing.T) {
	answer := "I was never BLOCKED, the task was clear.\nI am done.\n"

	if got := (generic{}).Blocked([]byte(answer)); got != "" {
		t.Errorf("Blocked = %q, want none from an answer that only mentions the word", got)
	}
}

// TestNameOfEveryProfile: a report and a state file call a profile by its name, so
// the names must not be empty and must not repeat.
func TestNameOfEveryProfile(t *testing.T) {
	names := []string{For([]string{"opencode"}).Name(), For([]string{"claude"}).Name()}

	if slices.Contains(names, "") {
		t.Errorf("a profile is called %q, want a name", names)
	}
	if names[0] == names[1] {
		t.Errorf("both profiles are called %q, want the report to tell them apart", names[0])
	}
	for _, name := range names {
		if strings.TrimSpace(name) != name {
			t.Errorf("a profile is called %q, want it without spaces around it", name)
		}
	}
}
