package secret

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// The two kinds of a value a run must not write down, as the cases of this file use them: a
// token of an hour a machine wrote, and a password of a proxy a person chose — one long, one
// of three signs (docs/DESIGN.md §7e).
const (
	canaryLong  = "ghs_16C7e42F292c6912E7710c838347Ae178B4a"
	canaryShort = "abc"
)

// boundaryOf is the boundary of the cases of this file with the values they name in it. They
// are one list and not one list per kind: a program of a run is started with all of them in
// its environment, so anything it prints may hold any of them (docs.DESIGN.md §7e).
func boundaryOf(values ...Value) *Out { return NewOut(values...) }

// theCanaries are the two kinds of a value a run must not write down, in one list: a token of
// an hour a machine wrote, and a password of a proxy a person chose — one of three signs,
// which is a password a proxy really takes (docs/DESIGN.md §7e).
func theCanaries() []Value {
	return append(Generated(canaryLong), Chosen(canaryShort)...)
}

// TestTheBoundaryTakesTheValuesOfARunOutOfEverythingItPublishes: the values of a run are
// taken out of the texts of all the channels and out of nothing else — the words of a
// failure stay, and only what of them is a secret is gone (docs/DESIGN.md §7e).
func TestTheBoundaryTakesTheValuesOfARunOutOfEverythingItPublishes(t *testing.T) {
	out := boundaryOf(theCanaries()...)

	for _, channel := range []Channel{ChannelTerminal, ChannelReport, ChannelEvent, ChannelState, ChannelJournal} {
		said, err := out.Publish(channel, "the proxy refused "+canaryShort+" for "+canaryLong)
		if err != nil {
			t.Fatalf("Publish(%q): %v", channel, err)
		}
		if strings.Contains(said, canaryShort) || strings.Contains(said, canaryLong) {
			t.Errorf("the text published to %s is %q, want the values of the run taken out of it", channel, said)
		}
		if !strings.Contains(said, "the proxy refused ") || !strings.Contains(said, " for ") {
			t.Errorf("the text published to %s is %q, want the words of the failure left in it", channel, said)
		}
	}
}

// TestTheBoundaryLearnsWhatTheRunFindsWhileItIsGoing: the credentials of the route of a run
// are read after the run began, and what a program printed before they were read is still
// published after them — a value learned halfway through a run holds back the next line of
// its journal, and the boundary is asked what it knows at the moment of the write
// (D-068 RECHECK-FINDING-5, docs/DESIGN.md §7e).
func TestTheBoundaryLearnsWhatTheRunFindsWhileItIsGoing(t *testing.T) {
	out := boundaryOf()
	var journal bytes.Buffer
	said := out.Writer(&journal)

	if _, err := said.Write([]byte("the route is straight out\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	out.Learn(Chosen(canaryShort)...)
	if _, err := said.Write([]byte("the second attempt went through " + canaryShort + "@proxy.example.com\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := said.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if strings.Contains(journal.String(), canaryShort) {
		t.Errorf("the journal holds %q, want the value learned after it was opened taken out of it", canaryShort)
	}
	if !strings.Contains(journal.String(), "the route is straight out\n") {
		t.Errorf("the journal is %q, want what was written before the value was learned", journal.String())
	}
}

// TestAValueWhoseTailIsItsOwnBeginningIsCutWhole: a password a person chose may overlap
// itself — "abab" is its own second half, "aaaa" is one sign repeated — and the writer of
// a journal worked out the place of the cut over the text as the program wrote it, before
// cutting the values out of it. The cut landed in the middle of such a value: the head of
// it went into the journal as it was written, the tail was held back as the beginning of
// a value that was never finished, and the two halves stood next to each other in the
// file with the whole password between them (R5-NEW-2, docs/DESIGN.md §7e).
func TestAValueWhoseTailIsItsOwnBeginningIsCutWhole(t *testing.T) {
	for _, value := range []string{"abab", "aaaa", "abcab"} {
		t.Run(value, func(t *testing.T) {
			out := boundaryOf(Chosen(value)...)
			var journal bytes.Buffer
			said := out.Writer(&journal)

			// The value arrives in pieces, as a program writes what it writes.
			for _, piece := range []string{"the password is ", value, " and the line goes on\n"} {
				if _, err := said.Write([]byte(piece)); err != nil {
					t.Fatalf("Write(%q): %v", piece, err)
				}
			}
			if err := said.Flush(); err != nil {
				t.Fatalf("Flush: %v", err)
			}

			if strings.Contains(journal.String(), value) {
				t.Errorf("the journal holds %q, want the value taken out of it:\n%s", value, journal.String())
			}
			if !strings.Contains(journal.String(), "the password is "+Redacted+" and the line goes on") {
				t.Errorf("the journal is %q, want the words of the line with %q in the place of the value",
					journal.String(), Redacted)
			}
		})
	}
}

// TestHoweverAStreamIsSplitNoValueIsEverWholeInIt: a journal of a run is written in pieces
// and where the pieces fall is not crewflow's business, so one text is written again and
// again, cut in a different place every time — a value split between two of the pieces, a
// value at the very end of the last one, and the words of a line that end in the beginning
// of one. What comes out is the whole text with the values taken out of it and not one of
// them in it in any of its forms, and it is the same text every time (R5-NEW-2,
// docs/DESIGN.md §7e).
func TestHoweverAStreamIsSplitNoValueIsEverWholeInIt(t *testing.T) {
	values := []string{"abab", "aaaa", "aab", canaryShort, canaryLong, `p"ss`, "a b"}
	said := strings.Join([]string{
		"the password of the proxy is abab and it is aaaa twice:",
		"the token of the run is " + canaryLong + ", the login is a b,",
		"the agent printed " + `p"ss` + " and " + canaryShort + " and " + canaryShort + " again,",
		"and the password is aab at the very end",
	}, "\n")
	whole := Redact(said, Chosen(values...)...)
	forms := spellings(Chosen(values...))

	random := rand.New(rand.NewSource(20261003))
	for range 400 {
		out := boundaryOf(Chosen(values...)...)
		var journal bytes.Buffer
		writer := out.Writer(&journal)

		for rest := said; rest != ""; {
			piece := rest
			if cut := random.Intn(len(rest)) + 1; cut < len(rest) {
				piece, rest = rest[:cut], rest[cut:]
			} else {
				rest = ""
			}
			if _, err := writer.Write([]byte(piece)); err != nil {
				t.Fatalf("Write(%q): %v", piece, err)
			}
		}
		if err := writer.Flush(); err != nil {
			t.Fatalf("Flush: %v", err)
		}

		for _, form := range forms {
			if strings.Contains(journal.String(), form) {
				t.Fatalf("the journal holds %q, want no value of the run in it:\n%s", form, journal.String())
			}
		}
		if got := journal.String(); got != whole {
			t.Fatalf("the journal is %q, want %q: a text cut in another place is not another text", got, whole)
		}
	}
}

// TestTheBoundaryKnowsAValueInEveryFormAMachineWritesItIn: the value a run must not write
// down is written by a machine, and a machine writes it in more than one text — a proxy
// that says what it was asked for is quoted into an event with `%q`, a report writes its
// strings with the escapes of JSON, and a password of a person with a quote or a line
// break in it is not in either of those as it is written down. A boundary that knew only
// the value itself did not find it there, and the password stood in the event whole
// (R5-NEW-3, docs/DESIGN.md §7e).
func TestTheBoundaryKnowsAValueInEveryFormAMachineWritesItIn(t *testing.T) {
	for _, tc := range []struct {
		name     string
		value    string
		quoted   string
		document string
	}{
		{name: "a quote in it", value: `pa"ss`, quoted: `pa\"ss`, document: `pa\"ss`},
		{name: "a line break in it", value: "pa\nss", quoted: `pa\nss`, document: `pa\nss`},
		{name: "a mark JSON escapes", value: "pa&ss", quoted: "pa&ss", document: `pa&ss`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := boundaryOf(Chosen(tc.value)...)
			for _, channel := range []Channel{ChannelTerminal, ChannelReport, ChannelEvent, ChannelState, ChannelJournal} {
				for _, what := range []string{
					"the proxy asked for " + tc.value + " and left",
					"the proxy asked for " + tc.quoted + " and left",
					`{"detail":"the proxy asked for ` + tc.document + `"}`,
				} {
					published, err := out.Publish(channel, what)
					if err != nil {
						t.Fatalf("Publish(%q): %v", channel, err)
					}
					for _, form := range []string{tc.value, tc.quoted, tc.document} {
						if strings.Contains(published, form) {
							t.Errorf("the text published to %s is %q, want %q taken out of it",
								channel, published, form)
						}
					}
					if !strings.Contains(published, "the proxy asked for") {
						t.Errorf("the text published to %s is %q, want the words of the complaint left in it",
							channel, published)
					}
				}
			}
		})
	}
}

// TestTheReportOfACommandIsCleanedByItsStringsAndNotByItsBytes: the answer of a command is
// read by a program, and the values of a run are cut out of it where it is put together —
// by the strings of it and not by the bytes of the document that came out of it. A number
// of a report that happens to be the password of a proxy is a number of the report: the
// password of three signs is a number a person really chooses, and a document that says
// `"change": 42` where the password is 42 is a document a program cannot read. A commit of
// the host that happens to hold the password is a commit, and a document with a cut in a
// commit is a document no program can look the change up by (R5-NEW-4, R224-002,
// docs.DESIGN.md §7e).
func TestTheReportOfACommandIsCleanedByItsStringsAndNotByItsBytes(t *testing.T) {
	const password = "42"
	const commit = "9f2b1c4a7d3e5f60718293a4b5c6d7e8f9012342"
	type check struct {
		Name  string `json:"name"`
		SHA   string `json:"sha"`
		State string `json:"state"`
	}
	type answerOfACommand struct {
		Change  int `json:"change"`
		Task    int `json:"task"`
		Files   int `json:"files"`
		Verdict struct {
			Ready  bool   `json:"ready"`
			Reason string `json:"reason"`
			Detail string `json:"detail"`
		} `json:"verdict"`
		Checks  []check  `json:"checks"`
		Outside []string `json:"outside"`
	}
	out := boundaryOf(Chosen(password)...)
	var document bytes.Buffer
	said := out.Writer(&document)
	answer := answerOfACommand{Change: 42, Task: 42, Files: 42}
	answer.Verdict.Ready = true
	answer.Verdict.Reason = "required-check-failed"
	answer.Verdict.Detail = "the check asked for 42 and left"
	answer.Checks = []check{{Name: "tests", SHA: commit, State: "failure"}}
	answer.Outside = []string{"internal/run/state.go:42"}

	if err := said.Report(DocumentReview, answer); err != nil {
		t.Fatalf("Report: %v", err)
	}

	var read answerOfACommand
	if err := json.Unmarshal(document.Bytes(), &read); err != nil {
		t.Fatalf("the document of the answer is not a document: %v\n%s", err, document.String())
	}
	for _, number := range []struct {
		name  string
		got   int
		which int
	}{{name: "the change", got: read.Change, which: 42},
		{name: "the task", got: read.Task, which: 42},
		{name: "the files", got: read.Files, which: 42}} {
		if number.got != number.which {
			t.Errorf("%s of the answer is %d, want the number %d: a number of a report is not a value of a run",
				number.name, number.got, number.which)
		}
	}
	if read.Checks[0].SHA != commit {
		t.Errorf("the commit of the check is %q, want %q: an identifier of the format is not a value of a run",
			read.Checks[0].SHA, commit)
	}
	if read.Verdict.Reason != "required-check-failed" {
		t.Errorf("the reason of the verdict is %q, want the word of the gate: a word of a closed list is not a value of a run",
			read.Verdict.Reason)
	}
	for _, where := range []struct {
		name string
		said string
	}{{"the detail", read.Verdict.Detail}, {"the file out of the boundaries", strings.Join(read.Outside, " ")}} {
		if strings.Contains(where.said, password) {
			t.Errorf("%s of the answer is %q, want the value of the run taken out of it", where.name, where.said)
		}
		if !strings.Contains(where.said, Redacted) {
			t.Errorf("%s of the answer is %q, want %q in the place of the value", where.name, where.said, Redacted)
		}
	}
}

// told is a type of its own, as the outcome of a run and the reason of a merge are: a field of
// such a type takes a value of that type and not any other, and the boundary used to answer
// with a cleaned `string` and set it into a field of another type (R5-NEW-7,
// docs.DESIGN.md §7e).
type told string

// TestTheReportOfACommandIsWrittenThroughAPointerAndKeepsItsShape: the answer of a command is
// put together out of the types its caller made it of, and among them are pointers — a change
// request a run opened, a point it stands at, a field a program fills in later. The boundary
// walked those types and built the answer again field by field, and for a pointer it built a
// new value of the pointer type, which is a nil pointer, and put a string into it: the report
// of a command whose answer holds a value of a run behind a pointer fell over instead of
// answering (R5-NEW-6, docs.DESIGN.md §7e).
//
// The answer here is the document of a merge with its string fields behind pointers, as a
// caller may build one out of what it has: the boundary reads a document and not the types of
// the answer, so what the answer holds behind a pointer does not matter to it — a pointer is a
// string in the document, and a document is always valid.
func TestTheReportOfACommandIsWrittenThroughAPointerAndKeepsItsShape(t *testing.T) {
	type verdict struct {
		Ready  bool    `json:"ready"`
		Detail *string `json:"detail"`
	}
	type answerOfACommand struct {
		Task    int       `json:"task"`
		Branch  *string   `json:"branch"`
		Head    **string  `json:"head"`
		Journal *string   `json:"journal"`
		Left    *[]string `json:"left"`
		Verdict *verdict  `json:"verdict"`
		None    *verdict  `json:"orchestrator"`
	}
	branch := "crewflow/224-fix-secret-json-recheck-216"
	head := "9f2b1c4a7d3e5f60718293a4b5c6d7e8f9012342"
	journal := "/tmp/crewflow/state/43/1.jsonl"
	detail := "the proxy asked for " + canaryShort + " and left"
	deeper := &head
	out := boundaryOf(theCanaries()...)

	document, fallen := theDocument(t, out, DocumentMerge, answerOfACommand{
		Task:    43,
		Branch:  &branch,
		Head:    &deeper,
		Journal: &journal,
		Left:    &[]string{canaryLong, canaryShort + "@proxy.example.com"},
		Verdict: &verdict{Ready: false, Detail: &detail},
		None:    nil,
	})
	if fallen != nil {
		t.Fatalf("the report of an answer behind a pointer fell over: %v", fallen)
	}

	var read struct {
		Task    int      `json:"task"`
		Branch  string   `json:"branch"`
		Head    string   `json:"head"`
		Journal string   `json:"journal"`
		Left    []string `json:"left"`
		Verdict struct {
			Ready  bool   `json:"ready"`
			Detail string `json:"detail"`
		} `json:"verdict"`
		Orchestrator *struct {
			Mode        string `json:"mode"`
			Description string `json:"description"`
		} `json:"orchestrator"`
	}
	if err := json.Unmarshal(document, &read); err != nil {
		t.Fatalf("the document of the answer is not a document: %v\n%s", err, document)
	}
	for _, where := range []struct {
		name string
		said string
	}{{"the branch", read.Branch}, {"the head", read.Head}, {"the journal", read.Journal},
		{"the deep pointer", read.Head}, {"the list behind a pointer", strings.Join(read.Left, " ")},
		{"the field behind two pointers", read.Verdict.Detail}} {
		if strings.Contains(where.said, canaryShort) || strings.Contains(where.said, canaryLong) {
			t.Errorf("%s of the answer is %q, want the values of the run taken out of it", where.name, where.said)
		}
	}
	if !strings.Contains(read.Verdict.Detail, Redacted) {
		t.Errorf("the detail of the verdict is %q, want %q in the place of the value",
			read.Verdict.Detail, Redacted)
	}
	if read.Orchestrator != nil {
		t.Errorf("the answer holds an orchestrator where there was none: %+v", *read.Orchestrator)
	}
}

// TestTheReportOfACommandKeepsTheTypesOfTheAnswerInTheDocument: a field of a type of its own —
// the outcome of a run, the mode of an identity, the reason of a gate — takes a value of that
// type, and the boundary used to answer with a cleaned `string` and to set it into a field of
// another type: the report of a command whose answer holds a value of a run in such a field
// fell over instead of answering (R5-NEW-7, docs.DESIGN.md §7e).
func TestTheReportOfACommandKeepsTheTypesOfTheAnswerInTheDocument(t *testing.T) {
	type answerOfACommand struct {
		Task   int     `json:"task"`
		Branch told    `json:"branch"`
		Left   *[]told `json:"left"`
		Mode   struct {
			Mode        told `json:"mode"`
			Description told `json:"description"`
		} `json:"orchestrator"`
	}
	branch := told("crewflow/224-fix-secret-json-recheck-216")
	words := told("the token of the run is " + canaryLong)
	answer := answerOfACommand{Task: 43, Branch: branch,
		Left: &[]told{words, told("the login is " + canaryShort)}}
	answer.Mode.Mode = told("shared")
	answer.Mode.Description = told("the login is " + canaryShort)
	out := boundaryOf(theCanaries()...)

	document, fallen := theDocument(t, out, DocumentMerge, answer)
	if fallen != nil {
		t.Fatalf("the report of an answer with a named type fell over: %v", fallen)
	}

	var read struct {
		Branch       string   `json:"branch"`
		Left         []string `json:"left"`
		Orchestrator struct {
			Mode        string `json:"mode"`
			Description string `json:"description"`
		} `json:"orchestrator"`
	}
	if err := json.Unmarshal(document, &read); err != nil {
		t.Fatalf("the document of the answer is not a document: %v\n%s", err, document)
	}
	for _, where := range []struct {
		name string
		said string
	}{{"the list of a named type behind a pointer", strings.Join(read.Left, " ")},
		{"the description of the orchestrator", read.Orchestrator.Description}} {
		if strings.Contains(where.said, canaryShort) || strings.Contains(where.said, canaryLong) {
			t.Errorf("%s of the answer is %q, want the values of the run taken out of it", where.name, where.said)
		}
		if !strings.Contains(where.said, Redacted) {
			t.Errorf("%s of the answer is %q, want %q in the place of the value", where.name, where.said, Redacted)
		}
	}
}

// TestTheReportOfACommandKeepsTheExactNumberAndLeavesTheAnswerAsItWas: the identifiers of
// the host are numbers, and a number that happens to be bigger than 2^53 loses its last signs
// on the way through a float64 — a change number that reads back as another change number is a
// document a program acts on wrongly. And the answer of a command is the value of the caller:
// the boundary writes a document and does not change what it was given (R224-010, R224-013,
// docs.DESIGN.md §7e).
func TestTheReportOfACommandKeepsTheExactNumberAndLeavesTheAnswerAsItWas(t *testing.T) {
	type answerOfACommand struct {
		Change int    `json:"change"`
		Task   int64  `json:"task"`
		Note   string `json:"note"`
	}
	answer := answerOfACommand{
		Change: 42,
		Task:   9007199254740993, // 2^53+1: the last sign is lost in a float64
		Note:   "the proxy asked for " + canaryShort + " and left",
	}
	whole := answer
	out := boundaryOf(theCanaries()...)

	document, fallen := theDocument(t, out, DocumentVerify, answer)
	if fallen != nil {
		t.Fatalf("the report of an answer with a big number fell over: %v", fallen)
	}

	var read struct {
		Change int    `json:"change"`
		Task   int64  `json:"task"`
		Note   string `json:"note"`
	}
	if err := json.Unmarshal(document, &read); err != nil {
		t.Fatalf("the document of the answer is not a document: %v\n%s", err, document)
	}
	if read.Task != whole.Task {
		t.Errorf("the task of the answer is %d, want %d: a number of a report keeps its exact value",
			read.Task, whole.Task)
	}
	if read.Change != whole.Change {
		t.Errorf("the change of the answer is %d, want %d: a number of a report is not a value of a run",
			read.Change, whole.Change)
	}
	if answer != whole {
		t.Errorf("the answer of the caller is %+v, want it as it was: %+v", answer, whole)
	}
}

// TestAWordOfTheFormatSurvivesTheValueOfTheRunThatStandsInsideIt: a program reads a report to
// decide what a run did, and it decides by the words out of the closed lists of the format and
// by the names the format has a shape for. A password that happens to be a part of one of them —
// `pr` of `pr-opened`, `bot` of the mode of an identity, `awaits` of `awaits-owner`, `route` of
// `network-route-unavailable`, a commit that holds the password — is not a secret there and is
// not a leak either: the word of the format stays whole, and the words of the run around it are
// cut (R224-001, R224-002, R224-003, docs/DESIGN.md §6a, §7e).
func TestAWordOfTheFormatSurvivesTheValueOfTheRunThatStandsInsideIt(t *testing.T) {
	for _, tc := range []struct {
		name     string
		doc      Document
		password string
		answer   map[string]any
		want     map[string]any
	}{
		{
			name:     "the outcome of a run",
			doc:      DocumentTaskRun,
			password: "pr",
			answer: map[string]any{"outcome": "pr-opened", "profile": "opencode",
				"title": "the run asked for pr and left"},
			want: map[string]any{"outcome": "pr-opened", "profile": "opencode",
				"title": "the run asked for " + Redacted + " and left"},
		},
		{
			name:     "the mode of an identity",
			doc:      DocumentTaskRun,
			password: "bot",
			answer:   map[string]any{"identity": "bot", "title": "the run went out as bot"},
			want:     map[string]any{"identity": "bot", "title": "the run went out as " + Redacted},
		},
		{
			name:     "the code of a reason and the words behind it",
			doc:      DocumentTaskRun,
			password: "route",
			answer:   map[string]any{"reason": "network-route-unavailable: the route is not reachable"},
			want: map[string]any{"reason": "network-route-unavailable: the " + Redacted +
				" is not reachable"},
		},
		{
			name:     "the state of the queue",
			doc:      DocumentTaskAttention,
			password: "awaits",
			answer: map[string]any{"attention": []any{map[string]any{
				"attention_state": "awaits-owner", "reason": "owner-acceptance-required",
				"hint": "the owner has to answer, and it awaits"}}},
			want: map[string]any{"attention": []any{map[string]any{
				"attention_state": "awaits-owner", "reason": "owner-acceptance-required",
				"hint": "the owner has to answer, and it " + Redacted}}},
		},
		{
			name:     "the commit of the head of a change",
			doc:      DocumentReview,
			password: "2342",
			answer: map[string]any{"head": "9f2b1c4a7d3e5f60718293a4b5c6d7e8f9012342",
				"outside": []any{"internal/secret/policy.go:2342"}},
			want: map[string]any{"head": "9f2b1c4a7d3e5f60718293a4b5c6d7e8f9012342",
				"outside": []any{"internal/secret/policy.go:" + Redacted}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := boundaryOf(Chosen(tc.password)...)

			document, err := out.Report(tc.doc, tc.answer)
			if err != nil {
				t.Fatalf("Report(%s): %v", tc.doc, err)
			}
			var read map[string]any
			if err := json.Unmarshal(document, &read); err != nil {
				t.Fatalf("the document of the answer is not a document: %v\n%s", err, document)
			}
			if !reflect.DeepEqual(read, tc.want) {
				t.Errorf("the document of the answer is\n%s\nwant\n%s", document, asJSON(t, tc.want))
			}
		})
	}
}

// TestAReasonOfItsOwnIsNotACodeOfTheFormat: a reason of a run is a word of the
// canonical list of the codes and then the words of what happened, and a word that
// is not in the list is a sentence of a run however it is written: one word
// without a space is not a code of the format by its shape, and a canary in front
// of a colon is not a code behind it. Without the list a canary of one word would
// stand in the reason as a code — no space, no colon — and a canary in front of a
// colon would keep its head: that is the bypass the list closes (R5-NEW-10,
// docs/DESIGN.md §6a, §7e).
func TestAReasonOfItsOwnIsNotACodeOfTheFormat(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reason string
	}{
		{name: "a canary of one word", reason: canaryShort},
		{name: "a canary of one word and the words behind it",
			reason: canaryShort + ": diagnostic"},
		{name: "a canary of the token of an hour", reason: canaryLong},
		{name: "a canary of the token of an hour and the words behind it",
			reason: canaryLong + ": the provider refused the run"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := boundaryOf(theCanaries()...)
			answers := map[Document]any{
				DocumentTaskRun:          map[string]any{"task": 43, "reason": tc.reason},
				DocumentTaskResume:       map[string]any{"task": 43, "reason": tc.reason},
				DocumentTaskCheckStalled: []any{map[string]any{"task": 43, "reason": tc.reason}},
				DocumentState: map[string]any{"schema": 1, "task": 43, "attempts": []any{
					map[string]any{"number": 1, "reason": tc.reason}}},
			}
			for _, doc := range Documents() {
				answer, known := answers[doc]
				if !known {
					continue
				}
				document, err := out.Report(doc, answer)
				if err != nil {
					t.Fatalf("Report(%s): %v", doc, err)
				}
				if strings.Contains(string(document), canaryShort) ||
					strings.Contains(string(document), canaryLong) {
					t.Errorf("the report of %s is %s, want the canary out of the reason",
						doc, document)
				}
			}
			// A state of a task is written the same way: the reason of an attempt
			// is published through the boundary of the run before it is written
			// (docs/DESIGN.md §7e, §7h).
			document, err := out.StateDocument(answers[DocumentState])
			if err != nil {
				t.Fatalf("StateDocument: %v", err)
			}
			if strings.Contains(string(document), canaryShort) ||
				strings.Contains(string(document), canaryLong) {
				t.Errorf("the state of a task is %s, want the canary out of the reason",
					document)
			}
			for _, channel := range []Channel{ChannelTerminal, ChannelReport,
				ChannelEvent, ChannelState, ChannelJournal} {
				said, err := out.Reason(channel, tc.reason)
				if err != nil {
					t.Fatalf("Reason(%q): %v", channel, err)
				}
				if strings.Contains(said, canaryShort) || strings.Contains(said, canaryLong) {
					t.Errorf("the reason published to %s is %q, want the canary out of it",
						channel, said)
				}
			}
		})
	}
}

// TestTheCodeOfAReasonOfTheFormatSurvivesTheReasonOfItsOwn: the list of the codes
// is what keeps the words of a run out of a reason, and it is a list and not a
// shape: a word of the list is a word of the format wherever a password of a
// person stands inside it, and the words behind the colon are the words of a run
// and are cut. A test that cut everything would pass the one above and cut the
// code out of the reason with it — so the code is here, kept whole with the
// password inside it, and the words behind it cut (the owner's decision of
// 03.10: a registered code that matches a short password is kept;
// R5-NEW-8, R5-NEW-10, docs/DESIGN.md §6a, §7e).
func TestTheCodeOfAReasonOfTheFormatSurvivesTheReasonOfItsOwn(t *testing.T) {
	const password = "route"
	out := boundaryOf(Chosen(password)...)
	reason := "network-route-unavailable: the route of the project is not reachable"

	document, err := out.Report(DocumentTaskRun, map[string]any{"task": 43, "reason": reason})
	if err != nil {
		t.Fatalf("Report(%s): %v", DocumentTaskRun, err)
	}
	var read struct {
		Task   int    `json:"task"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(document, &read); err != nil {
		t.Fatalf("the report of a run is not a document: %v\n%s", err, document)
	}
	if want := "network-route-unavailable: the " + Redacted +
		" of the project is not reachable"; read.Reason != want {
		t.Errorf("the reason of the report of a run is %q, want %q", read.Reason, want)
	}
	said, err := out.Reason(ChannelState, reason)
	if err != nil {
		t.Fatalf("Reason(%q): %v", ChannelState, err)
	}
	if want := "network-route-unavailable: the " + Redacted +
		" of the project is not reachable"; said != want {
		t.Errorf("the reason published to the state is %q, want %q", said, want)
	}
}

// TestWhateverTypesAnAnswerIsMadeOfTheDocumentIsTheSame: the fields of a document are what the
// schema of the format says, and a caller may hold a field of it in whatever Go type it has —
// a string, a pointer, a type of its own — and the boundary reads the document and not the types
// of the answer. So one document is built again and again out of answers of different types, and
// the document is the same every time: no shape of an answer makes the boundary fall over, and
// the values of a run are out of the free text of it (R5-NEW-6, R5-NEW-7, R224-013,
// docs.DESIGN.md §7e).
func TestWhateverTypesAnAnswerIsMadeOfTheDocumentIsTheSame(t *testing.T) {
	type verdictOfAChange struct {
		Ready  bool    `json:"ready"`
		Reason string  `json:"reason"`
		Detail *string `json:"detail"`
	}
	type plain struct {
		Task    int               `json:"task"`
		Branch  string            `json:"branch"`
		Head    string            `json:"head"`
		Journal string            `json:"journal"`
		Left    []string          `json:"left"`
		Outcome string            `json:"outcome"`
		Verdict any               `json:"verdict"`
		Nothing *verdictOfAChange `json:"orchestrator"`
	}
	type named struct {
		Task    int               `json:"task"`
		Branch  *told             `json:"branch"`
		Head    **told            `json:"head"`
		Journal told              `json:"journal"`
		Left    *[]told           `json:"left"`
		Outcome told              `json:"outcome"`
		Verdict any               `json:"verdict"`
		Nothing *verdictOfAChange `json:"orchestrator"`
	}
	detail := "the proxy asked for " + canaryShort + " and left"
	branch := told("crewflow/224-fix-secret-json-recheck-216")
	head := told("9f2b1c4a7d3e5f60718293a4b5c6d7e8f9012342")
	journal := told("/tmp/crewflow/state/43/1.jsonl")
	headPointer := &head
	out := boundaryOf(theCanaries()...)
	// What the document says, whatever the answer was made of: the words of the run are cut
	// and the words and the names of the format are whole.
	want := map[string]any{
		"task":         float64(43),
		"branch":       "crewflow/224-fix-secret-json-recheck-216",
		"head":         "9f2b1c4a7d3e5f60718293a4b5c6d7e8f9012342",
		"journal":      "/tmp/crewflow/state/43/1.jsonl",
		"left":         []any{Redacted, "the proxy asked for " + Redacted},
		"outcome":      "merged",
		"orchestrator": nil,
		"verdict": map[string]any{"ready": false, "reason": "approval-missing",
			"detail": "the proxy asked for " + Redacted + " and left"},
	}

	for attempt := range 400 {
		answer := any(named{Task: 43, Branch: &branch, Head: &headPointer, Journal: journal,
			Left:    &[]told{told(canaryLong), told("the proxy asked for " + canaryShort)},
			Outcome: told("merged"),
			Verdict: verdictOfAChange{Reason: "approval-missing", Detail: &detail}})
		if attempt%2 == 0 {
			answer = plain{Task: 43, Branch: string(branch), Head: string(head), Journal: string(journal),
				Left:    []string{canaryLong, "the proxy asked for " + canaryShort},
				Outcome: "merged",
				Verdict: map[string]any{"ready": false, "reason": "approval-missing", "detail": detail}}
		}

		document, fallen := theDocument(t, out, DocumentMerge, answer)
		if fallen != nil {
			t.Fatalf("the report of answer %d (%#v) fell over: %v", attempt, answer, fallen)
		}
		var got map[string]any
		if err := json.Unmarshal(document, &got); err != nil {
			t.Fatalf("the document of answer %d is not a document: %v\n%s", attempt, err, document)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("the document of answer %d (%#v) is\n%s\nwant\n%s",
				attempt, answer, document, asJSON(t, want))
		}
	}
}

// theDocument is the answer of a command as the boundary publishes it as the document the
// caller names, with a fall of the boundary given back to the test instead of taken the program
// that prints it with it: an answer crewflow cannot write is a refusal of a command and not the
// end of a command (R5-NEW-6, R5-NEW-7, docs/DESIGN.md §7e).
func theDocument(t *testing.T, out *Out, doc Document, answer any) (document []byte, fallen any) {
	t.Helper()
	defer func() { fallen = recover() }()
	var published bytes.Buffer
	said := out.Writer(&published)
	if err := said.Report(doc, answer); err != nil {
		t.Fatalf("Report(%s): %v", doc, err)
	}
	return published.Bytes(), nil
}

// asJSON is a value as a document, for what a test says about it.
func asJSON(t *testing.T, value any) []byte {
	t.Helper()
	document, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("the document of a value: %v", err)
	}
	return document
}

// TestABoundaryRefusesToPublishToAChannelItDoesNotKnow: a way out that nobody named is a way
// out nobody cleared. The text is not returned at all — not in the clear and not cleaned —
// because a value nobody can publish has nowhere to be shown (docs.DESIGN.md §7e).
func TestABoundaryRefusesToPublishToAChannelItDoesNotKnow(t *testing.T) {
	out := boundaryOf(Chosen(canaryShort)...)

	said, err := out.Publish("webhook", "the password of the proxy is "+canaryShort)

	if !errors.Is(err, ErrNoSuchChannel) {
		t.Fatalf("Publish to a channel crewflow does not know = %v, want %v", err, ErrNoSuchChannel)
	}
	if said != "" {
		t.Errorf("Publish to a channel crewflow does not know returned %q, want no text at all", said)
	}
	if !strings.Contains(err.Error(), "webhook") {
		t.Errorf("the refusal %q does not name the channel, want a person to know which one", err)
	}
}

// TestCleaningATextTwiceIsCleaningItOnce: a text of a run goes through the boundary more than
// once — the report of a command and the terminal it is printed on, an event that is said to
// the journal and to the terminal — and a second cleaning must not cut it twice and leave two
// markers where there was one (docs.DESIGN.md §7e).
func TestCleaningATextTwiceIsCleaningItOnce(t *testing.T) {
	out := boundaryOf(theCanaries()...)
	said := "the proxy asked for " + canaryShort + " and the token is " + canaryLong

	once := out.Text(said)
	twice := out.Text(once)

	if twice != once {
		t.Errorf("cleaning a cleaned text = %q, want %q: a boundary a value got out of twice is not idempotent", twice, once)
	}
	if got, want := strings.Count(once, Redacted), 2; got != want {
		t.Errorf("the cleaned text holds %d %q, want one for each of the %d values", got, Redacted, want)
	}
}

// TestTheErrorOfTheBoundaryIsTheErrorTheProgramDecidesBy: what a person reads is the words
// with the values taken out of them, and what a program decides by is the error as it was
// given — `errors.Is` walks into it as it always did, because the logic of a decision reads
// the error and never these words (docs.DESIGN.md §7e, §7i).
func TestTheErrorOfTheBoundaryIsTheErrorTheProgramDecidesBy(t *testing.T) {
	out := boundaryOf(Chosen(canaryShort)...)
	refused := fmt.Errorf("the proxy %s@proxy.example.com:1080 asked for a login", canaryShort)

	said := out.Err(fmt.Errorf("gh pr list: %w", refused))

	if strings.Contains(said.Error(), canaryShort) {
		t.Errorf("the error of the boundary is %q, want the values of the run taken out of it", said)
	}
	if !strings.Contains(said.Error(), "gh pr list: ") {
		t.Errorf("the error of the boundary is %q, want the words of the command left in it", said)
	}
	if !errors.Is(said, refused) {
		t.Error("the error the boundary published cannot be told from the error it was given")
	}
	if out.Err(nil) != nil {
		t.Error("the boundary made an error out of no error")
	}
}

// TestTheZeroBoundaryPublishesWhatItIsGiven: a command with no run behind it has nothing to
// hold back, and a program that was given no boundary at all — a run of a machine of a test,
// an adapter of a caller that handed none — publishes what it was given instead of refusing
// to work (docs/DESIGN.md §7e).
func TestTheZeroBoundaryPublishesWhatItIsGiven(t *testing.T) {
	var out *Out

	if got := out.Text("the words of a program"); got != "the words of a program" {
		t.Errorf("the text of no boundary is %q, want what the program wrote", got)
	}
	if _, err := out.Publish(ChannelJournal, "the words of a program"); err != nil {
		t.Errorf("Publish of no boundary: %v, want the words of a program", err)
	}
	var journal bytes.Buffer
	said := out.Writer(&journal)
	if _, err := said.Write([]byte("the words of a program\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := said.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if journal.String() != "the words of a program\n" {
		t.Errorf("the journal of no boundary is %q, want what the program wrote", journal.String())
	}
	// A channel crewflow does not know is refused by every boundary, nil or not: what is
	// refused is the publishing and not the cleaning.
	if _, err := out.Publish("webhook", "the words of a program"); !errors.Is(err, ErrNoSuchChannel) {
		t.Errorf("Publish to a channel crewflow does not know = %v, want %v", err, ErrNoSuchChannel)
	}
}

// TestTheWriterOfAJournalIsSafeWhileTheRunWritesBesideIt: the journal of an attempt is
// written by the goroutine that reads the stream of the executor and by the run itself at the
// same time — an event of the route, the line about whose name the run works under — and one
// writer with a held-back tail behind two of them is a race and a torn value. The check is
// what `go test -race` reads: two goroutines into one writer of one boundary.
func TestTheWriterOfAJournalIsSafeWhileTheRunWritesBesideIt(t *testing.T) {
	out := boundaryOf(Chosen(canaryShort)...)
	var journal syncBuffer
	said := out.Writer(&journal)
	var waiting sync.WaitGroup
	for _, of := range []func(){
		func() {
			defer waiting.Done()
			for range 40 {
				_, _ = said.Write([]byte("the password is " + canaryShort + " and the line goes on\n"))
			}
			_ = said.Flush()
		},
		func() {
			defer waiting.Done()
			for range 40 {
				_, _ = said.Write([]byte("crewflow: event NETWORK_ROUTE_FAILED reason=dns-failed\n"))
			}
		},
	} {
		waiting.Add(1)
		go of()
	}
	waiting.Wait()

	if strings.Contains(journal.String(), canaryShort) {
		t.Errorf("the journal holds %q, want it taken out of it", canaryShort)
	}
	if !strings.Contains(journal.String(), "NETWORK_ROUTE_FAILED") {
		t.Errorf("the journal is %q, want the events of the run in it", journal.String())
	}
}

// syncBuffer is a buffer a test writes into from more than one goroutine.
type syncBuffer struct {
	mu   sync.Mutex
	held strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.held.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.held.String()
}
