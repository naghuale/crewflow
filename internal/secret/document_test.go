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

// TestTheStateOfATaskWithAFieldNobodyClassifiedIsNotWrittenAtAll: the state of a task is the
// record of what crewflow did, kept for ever and read by programs that decide by the words of it.
// A field of it that the policy of the kind does not name is not cleaned as free text and written
// all the same: nobody looked at that field, and the state is not written at all. The refusal
// names the document and the field and no value of a run (D-089, D-082, docs/DESIGN.md §7h).
func TestTheStateOfATaskWithAFieldNobodyClassifiedIsNotWrittenAtAll(t *testing.T) {
	out := boundaryOf(Chosen(canaryShort)...)

	document, err := out.StateDocument(map[string]any{
		"schema":            1,
		"task":              43,
		"branch":            "crewflow/43-the-run-of-a-task",
		"field_of_tomorrow": "the run asked for " + canaryShort + " and left",
	})

	if err == nil {
		t.Fatalf("the state of a task with a field nobody classified is %s, want a refusal", document)
	}
	if document != nil {
		t.Errorf("the state of a task that was not written is %s, want nothing", document)
	}
	if !errors.Is(err, ErrRedactionPathUnknown) {
		t.Fatalf("the refusal of the state of a task is %v, want %v", err, ErrRedactionPathUnknown)
	}
	for _, want := range []string{string(DocumentState), "field_of_tomorrow"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal of the state of a task is %q, want it to name %q", err, want)
		}
	}
	if strings.Contains(err.Error(), canaryShort) {
		t.Errorf("the refusal of the state of a task is %q, want no value of a run in it", err)
	}
	// A state every field of which the policy names is written as it is, so that the refusal
	// above says something about the field and not about the kind of the document.
	document, err = out.StateDocument(map[string]any{"schema": 1, "task": 43})
	if err != nil {
		t.Fatalf("the state of a task every field of which is named: %v", err)
	}
	var read map[string]any
	if err := json.Unmarshal(document, &read); err != nil {
		t.Fatalf("the state of a task is not a document: %v\n%s", err, document)
	}
	if read["task"] != float64(43) {
		t.Errorf("the task of the state is %v, want 43: the fields the policy names are written as they are",
			read["task"])
	}
}

// TestAFieldNobodyClassifiedWithNothingInItIsNotPublished: a field the policy of
// the document does not name is a field nobody looked at whatever is in it, and
// a value that is not there is a value that cannot be cut but is still a field
// nobody looked at — `null` in a field of tomorrow is the same bypass as the
// words of a run in it, only quieter: the words cannot be told apart from the
// nothing that stands in their place (R5-NEW-9, D-082, D-089,
// docs/DESIGN.md §7e, §7h).
func TestAFieldNobodyClassifiedWithNothingInItIsNotPublished(t *testing.T) {
	for _, tc := range []struct {
		name   string
		doc    Document
		answer any
		path   string
	}{
		{name: "a field of the root with nothing in it", doc: DocumentState,
			answer: map[string]any{"schema": 1, "task": 43, "field_of_tomorrow": nil},
			path:   "field_of_tomorrow"},
		{name: "a field of a record of the state with nothing in it", doc: DocumentState,
			answer: map[string]any{"schema": 1, "task": 43,
				"change": map[string]any{"field_of_tomorrow": nil}},
			path: "change.field_of_tomorrow"},
		{name: "a field of an element of a list with nothing in it", doc: DocumentState,
			answer: map[string]any{"schema": 1, "task": 43,
				"attempts": []any{map[string]any{"field_of_tomorrow": nil}}},
			path: "attempts[].field_of_tomorrow"},
		{name: "a field of a report of a run with nothing in it", doc: DocumentTaskRun,
			answer: map[string]any{"task": 43,
				"checkpoint": map[string]any{"field_of_tomorrow": nil}},
			path: "checkpoint.field_of_tomorrow"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := boundaryOf(Chosen(canaryShort)...)

			document, err := out.Report(tc.doc, tc.answer)

			if !errors.Is(err, ErrRedactionPathUnknown) {
				t.Fatalf("the report of a document with an unnamed field of nothing = %v, want %v",
					err, ErrRedactionPathUnknown)
			}
			if document != nil {
				t.Errorf("the report is %q, want nothing published at all", document)
			}
			for _, want := range []string{string(tc.doc), tc.path} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal is %q, want it to name the document and the field", err)
				}
			}
			if strings.Contains(err.Error(), canaryShort) {
				t.Errorf("the refusal is %q, want no value of the run in it", err)
			}
		})
	}
}

// aDocumentOfItsOwn is a document with a shape of its own: a program of a run
// may say what the document it publishes is, and the boundary reads the document
// the answer marshals into, not the types of the answer (R5-NEW-6, R5-NEW-7).
type aDocumentOfItsOwn struct{}

// MarshalJSON writes the document as the program of the run wrote it: a field
// the policy of the kind does not name, and nothing in it.
func (aDocumentOfItsOwn) MarshalJSON() ([]byte, error) {
	return []byte(`{"schema": 1, "task": 43, "field_of_tomorrow": null}`), nil
}

// TestADocumentOfItsOwnWithAFieldNobodyClassifiedAndNothingInIt: the boundary
// reads the document, and a document a program of the run wrote with a field
// nobody classified and nothing in it is a document with a field nobody looked
// at: `null` is the words of a run that are not there yet, and the document is
// not published at all (R5-NEW-9, D-082, D-089, docs/DESIGN.md §7e, §7h).
func TestADocumentOfItsOwnWithAFieldNobodyClassifiedAndNothingInIt(t *testing.T) {
	out := boundaryOf(Chosen(canaryShort)...)

	document, err := out.StateDocument(aDocumentOfItsOwn{})

	if !errors.Is(err, ErrRedactionPathUnknown) {
		t.Fatalf("the state of a document of its own = %v, want %v", err, ErrRedactionPathUnknown)
	}
	if document != nil {
		t.Errorf("the state of a document of its own is %s, want nothing published at all", document)
	}
	for _, want := range []string{string(DocumentState), "field_of_tomorrow"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal is %q, want it to name the document and the field", err)
		}
	}
	if strings.Contains(err.Error(), canaryShort) {
		t.Errorf("the refusal is %q, want no value of the run in it", err)
	}
}

// TestAFieldThePolicyNamesWithNothingInItIsPublished: a field the policy of the
// document names is a field somebody looked at, and nothing in it is not a
// bypass of anything: the refusal of the tests above is about the field, not
// about the value, and a named field with `null` in it is published as the
// nothing it is (R5-NEW-9, docs/DESIGN.md §7e).
func TestAFieldThePolicyNamesWithNothingInItIsPublished(t *testing.T) {
	out := boundaryOf(Chosen(canaryShort)...)

	document, err := out.StateDocument(map[string]any{"schema": 1, "task": nil})
	if err != nil {
		t.Fatalf("the state of a task with a named field of nothing: %v", err)
	}
	if !strings.Contains(string(document), `"task": null`) {
		t.Errorf("the state of a task is %s, want the field the policy names with nothing in it",
			document)
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

// TestAPendingEventOfAStateOnSomethingThatIsNotARecordIsNotWritten: the backlog of a task keeps
// the transitions nobody has been told about yet, and every field of it is either a word of a
// closed list of §6a or a name with a shape the format has: a transition is told by the commit
// at the head of the branch of the task, and a payload of any other kind in the backlog of a
// task is a field nobody looked at when the event was told. So a record of a task whose basis is
// not a commit, or whose transition is between words that are not the states of the queue, is
// not written at all — and the refusal names the field and not the value of it (D-082, D-089,
// docs/DESIGN.md §7e, §7h, #243).
func TestAPendingEventOfAStateOnSomethingThatIsNotARecordIsNotWritten(t *testing.T) {
	for _, tc := range []struct {
		name   string
		event  map[string]any
		path   string
		reason error
	}{
		{name: "a payload in place of the record a transition rests on",
			event: map[string]any{"id": "7490550696ff", "at": "2026-10-04T09:00:00Z",
				"from": "stands", "to": "awaits-review", "basis": `{"the": "run said"}`},
			path: "pending_events[].basis", reason: ErrProtectedFieldInvalid},
		{name: "a value of a run in place of the record a transition rests on",
			event: map[string]any{"id": "7490550696ff", "at": "2026-10-04T09:00:00Z",
				"from": "stands", "to": "awaits-review", "basis": canaryShort},
			path: "pending_events[].basis", reason: ErrProtectedFieldInvalid},
		{name: "a word out of the list of the states in place of a state",
			event: map[string]any{"id": "7490550696ff", "at": "2026-10-04T09:00:00Z",
				"from": "in-progress", "to": "awaits-review", "basis": "9f1c0de4a4a0b1f2c3d4e5f60718293a4b5c6d7"},
			path: "pending_events[].from", reason: ErrProtectedFieldInvalid},
		{name: "a moment that is not a moment in place of the moment of a transition",
			event: map[string]any{"id": "7490550696ff", "at": "yesterday",
				"from": "stands", "to": "awaits-review", "basis": "9f1c0de4a4a0b1f2c3d4e5f60718293a4b5c6d7"},
			path: "pending_events[].at", reason: ErrProtectedFieldInvalid},
		{name: "a name a program cannot read in place of the name of an event",
			event: map[string]any{"id": "not-a-digest", "at": "2026-10-04T09:00:00Z",
				"from": "stands", "to": "awaits-review", "basis": "9f1c0de4a4a0b1f2c3d4e5f60718293a4b5c6d7"},
			path: "pending_events[].id", reason: ErrProtectedFieldInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := boundaryOf(Chosen(canaryShort)...)

			document, err := out.StateDocument(map[string]any{
				"schema": 1, "revision": 1, "task": 43,
				"pending_events": []any{tc.event},
			})

			if !errors.Is(err, tc.reason) {
				t.Fatalf("the state of a task with a transition that is not one = %v, want %v", err, tc.reason)
			}
			if document != nil {
				t.Errorf("the state of a task that was not written is %s, want nothing", document)
			}
			for _, want := range []string{string(DocumentState), tc.path} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal is %q, want it to name the document and the field", err)
				}
			}
			if strings.Contains(err.Error(), canaryShort) {
				t.Errorf("the refusal is %q, want no value of a run in it", err)
			}
		})
	}
}

// TestAPendingEventOfAStateIsWrittenAsItIs: the fields the policy of the backlog names are fields
// somebody looked at, and a transition of a task that rests on the commit at the head of its
// branch is a document the boundary publishes whole — the words of the format with a cut in them
// are words nobody can read, and the commit of the host is what a subscriber goes and reads the
// event against (D-082, R224-001, docs/DESIGN.md §7e, §7h, #243).
func TestAPendingEventOfAStateIsWrittenAsItIs(t *testing.T) {
	const commit = "9f1c0de4a4a0b1f2c3d4e5f60718293a4b5c6d7"
	out := boundaryOf(Chosen("commit")...)

	document, err := out.StateDocument(map[string]any{
		"schema": 1, "revision": 4, "task": 43,
		"pending_events": []any{map[string]any{
			"id": "7490550696ff", "at": "2026-10-04T09:00:00Z",
			"from": "stands", "to": "awaits-review", "basis": commit,
		}},
	})

	if err != nil {
		t.Fatalf("the state of a task with a transition of its own: %v", err)
	}
	var read map[string]any
	if err := json.Unmarshal(document, &read); err != nil {
		t.Fatalf("the state of a task is not a document: %v\n%s", err, document)
	}
	events, isList := read["pending_events"].([]any)
	if !isList || len(events) != 1 {
		t.Fatalf("the state of a task holds %v, want the one transition that was written", read["pending_events"])
	}
	want := map[string]any{
		"id": "7490550696ff", "at": "2026-10-04T09:00:00Z",
		"from": "stands", "to": "awaits-review", "basis": commit,
	}
	if event := events[0].(map[string]any); !sameJSON(event, want) {
		t.Errorf("the transition in the backlog is\n%s\nwant\n%s", asJSON(t, event), asJSON(t, want))
	}
	if revision, _ := read["revision"].(float64); revision != 4 {
		t.Errorf("the revision of the record is %v, want 4: the record keeps the number it was written with",
			read["revision"])
	}
}
