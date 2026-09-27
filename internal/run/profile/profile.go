// Package profile is what crewflow knows about one agent: where the session of a
// run is named, what a refused permission looks like in what the agent writes, and
// how a run goes on in the same session (docs/DESIGN.md §7a).
//
// Everything a run asks about its executor is asked of a profile, and the run
// itself knows no agent: the next one is one more profile, not a change in the
// core (docs/DESIGN.md §7b).
package profile

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
)

// blockedPrefix is the word an executor finishes with when it cannot go on, and
// what it says why. It is crewflow's own word, so every profile understands it,
// whatever the agent is.
const blockedPrefix = "BLOCKED:"

// Profile is what a run has to know about the agent it starts. The methods ask
// questions about what the agent wrote and are never given more than that: a
// profile reads a run, it does not judge it.
type Profile interface {
	// Name is how a report and a state file call the profile, "opencode" or
	// "generic".
	Name() string
	// SessionID returns the id of the session the run went on, and an empty string
	// for an agent whose runs have no session to name.
	SessionID(stdout []byte) string
	// Rejections returns what the agent was refused during the run, one entry per
	// refusal with its kind and its path or command: a refused permission ends a
	// run, and it is an outcome of its own that a person has to decide about
	// (docs/DESIGN.md §7a, §7d).
	Rejections(stdout, stderr []byte) []string
	// Blocked returns the reason of the last message that began with "BLOCKED:",
	// and an empty string when the run was not stopped by the agent itself.
	Blocked(stdout []byte) string
	// ContinueArgs returns the arguments that go on in the session, and nothing at
	// all for an agent that has no sessions to continue: a run of such an agent
	// begins again, in the same worktree and with the task in hand.
	ContinueArgs(session string) []string
}

// opencodeProgram is the program of the first agent the pilot runs were made with.
// The profile is chosen by the program of the command of the project, because that
// is the only thing about an agent crewflow can see before it runs it
// (docs/DESIGN.md §7a).
const opencodeProgram = "opencode"

// For is the profile of the agent of a command line: the one of OpenCode when the
// program is OpenCode, and the generic one for anything else.
func For(command []string) Profile {
	if len(command) > 0 && filepath.Base(command[0]) == opencodeProgram {
		return opencode{}
	}
	return generic{}
}

// opencode is OpenCode, read through the stream of events it writes for a run
// (docs/DESIGN.md §7a). What is parsed here is what a run of the pilot was made
// of, and nothing more of the shape of the agent: a field crewflow does not read
// is a field that may change without anyone noticing.
type opencode struct{}

// Name is how a report calls this profile.
func (opencode) Name() string { return opencodeProgram }

// SessionID is the session every event of a run belongs to. The last one that
// names a session is taken, because a run that has a session at all has the same
// one from its first event to its last.
func (opencode) SessionID(stdout []byte) string {
	session := ""
	for _, event := range events(stdout) {
		if event.SessionID != "" {
			session = event.SessionID
		}
	}
	return session
}

// Rejections are the lines of the way out that say a question about a permission
// was asked and answered without a person. Each of them ended the run, and each of
// them names what was refused (docs/DESIGN.md §7a).
func (o opencode) Rejections(_, stderr []byte) []string {
	var rejections []string
	for _, match := range rejectionPattern.FindAllStringSubmatch(string(stderr), -1) {
		rejections = append(rejections, refusal(match[1]))
	}
	return rejections
}

// Blocked is the reason the agent itself gave, in the last text of the answer that
// began with the word.
func (opencode) Blocked(stdout []byte) string {
	reason := ""
	for _, event := range events(stdout) {
		if event.Type != "text" {
			continue
		}
		if said, ok := blockedReason(event.Part.Text); ok {
			reason = said
		}
	}
	return reason
}

// ContinueArgs is the session to go on in: a run that stopped may be continued in
// it, with the worktree and the context it already has (docs/DESIGN.md §7a).
func (opencode) ContinueArgs(session string) []string {
	return []string{"--session", session}
}

// generic is the profile of an agent crewflow has not run yet. It sees no session,
// no refusal and cannot be continued: crewflow does not guess the shape of an
// agent it does not know, and a run it cannot read is a run whose outcome is
// "the executor was silent", not a run that went well (docs/DESIGN.md §7b).
type generic struct{}

// Name is how a report calls this profile.
func (generic) Name() string { return "generic" }

// SessionID is nothing: an agent of this kind is not known to have sessions.
func (generic) SessionID([]byte) string { return "" }

// Rejections is nothing: an agent of this kind is not known to say so where
// crewflow reads it.
func (generic) Rejections(_, _ []byte) []string { return nil }

// Blocked is the reason in the last line of the answer that began with the word:
// the word is crewflow's own, whatever the agent is.
func (generic) Blocked(stdout []byte) string {
	reason := ""
	for raw := range strings.Lines(string(stdout)) {
		if said, ok := blockedReason(raw); ok {
			reason = said
		}
	}
	return reason
}

// ContinueArgs is nothing: a run of an agent of this kind begins again, in the
// same worktree and with the task in hand.
func (generic) ContinueArgs(string) []string { return nil }

// event is one line of the stream of an agent, as far as a run is concerned: which
// session it belongs to, and the part of the answer it carries.
type event struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionID"`
	Part      struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"part"`
}

// events are the events of a stream crewflow can read, and nothing else: a line
// that is not an event of a shape it knows is not an error, it is a line of an
// agent that said something else.
func events(stdout []byte) []event {
	var read []event
	for raw := range strings.Lines(string(stdout)) {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var one event
		if err := json.Unmarshal([]byte(line), &one); err != nil {
			continue
		}
		read = append(read, one)
	}
	return read
}

// rejectionPattern finds a line that says a question about a permission was asked
// and answered without a person. The answer of a refusal is the whole reason: what
// kind of permission it was and what it was about (docs/DESIGN.md §7a).
var rejectionPattern = regexp.MustCompile(`(?m)^.*permission requested:\s*(.+?)\s*;\s*auto-rejecting.*$`)

// refusal is the reason of a refusal without the parentheses around what it was
// about, which is how an orchestrator reads it: the kind of the permission, then
// the path or the command.
func refusal(reason string) string {
	kind, about, inParentheses := strings.Cut(strings.TrimSpace(reason), "(")
	if !inParentheses || !strings.HasSuffix(strings.TrimSpace(about), ")") {
		return strings.TrimSpace(reason)
	}
	return strings.TrimSpace(kind) + " " + strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(about), ")"))
}

// blockedReason is what a message that begins with the word says after it, and
// whether it begins with the word at all: the word in the middle of a sentence is
// a word, not a stop.
func blockedReason(message string) (string, bool) {
	line := strings.TrimSpace(message)
	reason, isStop := strings.CutPrefix(line, blockedPrefix)
	return strings.TrimSpace(reason), isStop
}
