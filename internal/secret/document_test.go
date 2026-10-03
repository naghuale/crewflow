package secret

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestADocumentWithoutAPolicyIsNotPublishedAtAll: a program names the document it publishes,
// and a document this boundary has no policy for is a program that publishes somewhere nobody
// wrote a policy. The refusal carries the document and nothing else — the answer of the command
// is not in the refusal, because a refusal is a text a person reads (D-082, R224-012,
// docs/DESIGN.md §7e).
func TestADocumentWithoutAPolicyIsNotPublishedAtAll(t *testing.T) {
	out := boundaryOf(Chosen(canaryShort)...)

	document, err := out.Report(Document("task-list/v2"), map[string]any{"task": 43})

	if !errors.Is(err, ErrDocumentPolicyMissing) {
		t.Fatalf("the report of a document without a policy = %v, want %v", err, ErrDocumentPolicyMissing)
	}
	if document != nil {
		t.Errorf("the report of a document without a policy is %q, want nothing published at all", document)
	}
	if !strings.Contains(err.Error(), "task-list/v2") {
		t.Errorf("the refusal is %q, want it to name the document crewflow does not know", err)
	}
	if strings.Contains(err.Error(), canaryShort) {
		t.Errorf("the refusal is %q, want no value of the run in it", err)
	}
}

// TestAFieldWithoutAPolicyIsNotPublishedAtAll: a field the policy of the document does not name
// is a field nobody looked at, and publishing it silently is how a value of a run gets out of a
// document without anybody knowing which field of it it went through. The refusal carries the
// document and the path and nothing else (D-082, R224-008, R224-012, docs/DESIGN.md §7e).
func TestAFieldWithoutAPolicyIsNotPublishedAtAll(t *testing.T) {
	for _, tc := range []struct {
		name   string
		doc    Document
		answer map[string]any
		path   string
	}{
		{name: "a field of no name in the format", doc: DocumentMerge, path: "outcome_of_the_run",
			answer: map[string]any{"outcome_of_the_run": "the proxy asked for " + canaryShort}},
		{name: "a field of a record nobody named", doc: DocumentMerge, path: "verdict.words",
			answer: map[string]any{"verdict": map[string]any{"ready": false, "words": canaryShort}}},
		{name: "a field of an element of a list", doc: DocumentReview, path: "checks[].what",
			answer: map[string]any{"checks": []any{map[string]any{"name": "tests", "what": canaryShort}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := boundaryOf(Chosen(canaryShort)...)

			document, err := out.Report(tc.doc, tc.answer)

			if !errors.Is(err, ErrRedactionPathUnknown) {
				t.Fatalf("the report of a document with an unnamed field = %v, want %v", err, ErrRedactionPathUnknown)
			}
			if document != nil {
				t.Errorf("the report is %q, want nothing published at all", document)
			}
			if !strings.Contains(err.Error(), tc.path) || !strings.Contains(err.Error(), string(tc.doc)) {
				t.Errorf("the refusal is %q, want it to name the document and the field", err)
			}
			if strings.Contains(err.Error(), canaryShort) {
				t.Errorf("the refusal is %q, want no value of the run in it", err)
			}
		})
	}
}

// TestAProtectedValueTheFormatDoesNotAdmitStopsThePublication: a word out of a closed list that
// is not in it, and a name not of the form its own field holds, are values of the format that
// are not values of it. They are kept as they are and the document is not published: a document
// with a word of the format that nobody can enumerate is a document a program cannot act on
// (D-082, R224-003, R224-004, docs/DESIGN.md §6a, §7e).
func TestAProtectedValueTheFormatDoesNotAdmitStopsThePublication(t *testing.T) {
	for _, tc := range []struct {
		name   string
		doc    Document
		answer map[string]any
		path   string
	}{
		{name: "a word out of the list of the outcomes", doc: DocumentTaskRun, path: "outcome",
			answer: map[string]any{"outcome": "pr-opened-and-then-some"}},
		{name: "a name not of its form", doc: DocumentVerify, path: "head",
			answer: map[string]any{"head": "not a commit at all"}},
		{name: "a moment not in the words of RFC 3339", doc: DocumentVerify, path: "verified_at",
			answer: map[string]any{"verified": true, "verified_at": "yesterday"}},
		{name: "a number where the format says a word", doc: DocumentVerify, path: "branch",
			answer: map[string]any{"branch": 42}},
		{name: "a word where the format says a number", doc: DocumentVerify, path: "task",
			answer: map[string]any{"task": "forty-three"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := boundaryOf(Chosen(canaryShort)...)

			document, err := out.Report(tc.doc, tc.answer)

			switch {
			case errors.Is(err, ErrProtectedFieldInvalid), errors.Is(err, ErrJSONSchemaInvalid):
			default:
				t.Fatalf("the report of a document with a value the format does not admit = %v, want a refusal", err)
			}
			if document != nil {
				t.Errorf("the report is %q, want nothing published at all", document)
			}
			if !strings.Contains(err.Error(), tc.path) {
				t.Errorf("the refusal is %q, want it to name the field %q", err, tc.path)
			}
		})
	}
}

// TestTheValueBotOfTheFormatIsNotASecretAndNotALeak: the criteria of the owner of #224 name the
// value `bot` as the case that decides whether a boundary cuts words of the format or keeps
// them: the mode of an identity is a word of the closed list, a commit of the host is a name,
// and the words of a run are the words of a run. A password that happens to be `bot` cuts
// nothing of the first two and everything of the third (D-082, R224-001, R224-002,
// docs/DESIGN.md §7e).
func TestTheValueBotOfTheFormatIsNotASecretAndNotALeak(t *testing.T) {
	const password = "bot"
	const commit = "9f2b1c4a7d3e5f60718293a4b5c6d7e8f9012342"
	out := boundaryOf(Chosen(password)...)

	document, err := out.Report(DocumentReview, map[string]any{
		"review": map[string]any{
			"decision": "approved", "commit": "9f2b1c4a7d3e5f60718293a4b5c6d7e8f9012342"},
		"head":    commit,
		"outside": []any{"the agent ran as bot and printed its environment"},
	})
	if err != nil {
		t.Fatalf("Report: %v", err)
	}

	want := map[string]any{
		"review": map[string]any{
			"decision": "approved", "commit": "9f2b1c4a7d3e5f60718293a4b5c6d7e8f9012342"},
		"head":    commit,
		"outside": []any{"the agent ran as " + Redacted + " and printed its environment"},
	}
	var read map[string]any
	if err := json.Unmarshal(document, &read); err != nil {
		t.Fatalf("the document of the answer is not a document: %v\n%s", err, document)
	}
	if !sameJSON(read, want) {
		t.Errorf("the document of the answer is\n%s\nwant\n%s", document, asJSON(t, want))
	}
}

// TestTheStateOfATaskIsWrittenOverAGapInItsPolicy: the state of a task is a record of what
// crewflow did, written after the actions of a run, and a field of it that the policy of the
// document does not name must not cost the run its record: the field is cleaned as free text
// and the gap is given back to be said as an event (R224-16, D-082, docs/DESIGN.md §7h).
func TestTheStateOfATaskIsWrittenOverAGapInItsPolicy(t *testing.T) {
	out := boundaryOf(Chosen(canaryShort)...)

	document, gaps, err := out.StateDocument(map[string]any{
		"schema":            1,
		"task":              43,
		"branch":            "crewflow/43-the-run-of-a-task",
		"field_of_tomorrow": "the run asked for " + canaryShort + " and left",
	})
	if err != nil {
		t.Fatalf("the state of a task: %v", err)
	}

	if len(gaps) != 1 || gaps[0] != "field_of_tomorrow" {
		t.Errorf("the gaps of the policy of the state are %q, want the one field nobody classified", gaps)
	}
	var read map[string]any
	if err := json.Unmarshal(document, &read); err != nil {
		t.Fatalf("the state of a task is not a document: %v\n%s", err, document)
	}
	if read["task"] != float64(43) {
		t.Errorf("the task of the state is %v, want 43: the fields the policy names are written as they are",
			read["task"])
	}
	said, _ := read["field_of_tomorrow"].(string)
	if strings.Contains(said, canaryShort) || !strings.Contains(said, Redacted) {
		t.Errorf("the field nobody classified is %q, want %q in the place of the value: a gap is not a way out",
			said, Redacted)
	}
}

// TestTheBoundaryPublishesNoDocumentItHasNoWordOf: the zero boundary and a boundary of no
// values both publish the documents of the table as they are — a command with no run behind it
// has nothing to hold back — and both of them still hold every document of the registry to its
// policy (docs/DESIGN.md §7e, D-082).
func TestTheBoundaryPublishesNoDocumentItHasNoWordOf(t *testing.T) {
	var none *Out
	document, err := none.Report(DocumentTaskCheck, map[string]any{"task": 43, "ready": true})
	if err != nil {
		t.Fatalf("the report of no boundary: %v", err)
	}
	if !strings.Contains(string(document), `"ready": true`) {
		t.Errorf("the report of no boundary is %s, want the answer of the command as it is", document)
	}
	if _, err := none.Report(Document("nonsense/v1"), map[string]any{}); !errors.Is(err, ErrDocumentPolicyMissing) {
		t.Errorf("the report of a document without a policy from no boundary = %v, want %v", err, ErrDocumentPolicyMissing)
	}
}

// sameJSON is whether two values read out of two documents are the same document.
func sameJSON(one, other any) bool {
	first, err := json.Marshal(one)
	if err != nil {
		return false
	}
	second, err := json.Marshal(other)
	if err != nil {
		return false
	}
	return string(first) == string(second)
}
