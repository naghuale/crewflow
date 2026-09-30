package gate

import (
	"strings"
	"testing"
)

// TestApproveOf is the record a person writes by hand and what crewflow writes for
// them: the first line is the whole of the decision, and the block under it holds
// the same in the words of §7h. A record that reads back as anything else is a
// record the gate does not count, and an approval nobody can count approves nothing.
func TestApproveOf(t *testing.T) {
	body := ApproveOf(second, 7)

	want := "REVIEW: APPROVED " + second + "\n\n" +
		"<!-- crewflow-review -->\n" +
		"decision: approved\n" +
		"commit: " + second + "\n" +
		"task: 7\n"
	if body != want {
		t.Errorf("ApproveOf wrote:\n%q\nwant:\n%q", body, want)
	}

	record, is := Parse(body)
	if !is {
		t.Fatalf("Parse did not read the record crewflow writes itself")
	}
	if record.Decision != Approved || record.Commit != second || record.Task != 7 {
		t.Errorf("Parse = %+v, want an approval of %s for task 7", record, second)
	}
}

// TestChangesOf: what a person wrote about the changes they ask for is part of the
// record and goes under the decision, where a reader of the change reads it, and not
// into the block, which is for the programs.
func TestChangesOf(t *testing.T) {
	body := ChangesOf(second, 7, "1. the first point\n2. the second point\n")

	record, is := Parse(body)
	if !is {
		t.Fatal("Parse did not read the record of a request for changes")
	}
	if record.Decision != ChangesRequested {
		t.Errorf("Parse = %+v, want a request for changes", record)
	}
	for _, in := range []string{"1. the first point", "2. the second point"} {
		if !strings.Contains(body, in) {
			t.Errorf("the record does not hold the finding %q:\n%s", in, body)
		}
	}
}

// TestParseOfWhatACommentIsNot: a change is a conversation, and everything else in
// it is a comment — a question to the executor, a note about a test, the words of a
// person. Only a line that opens a record is one, and a record of a decision crewflow
// does not know is not a decision.
func TestParseOfWhatACommentIsNot(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{name: "an empty comment", body: ""},
		{name: "a question about the work", body: "Why is this here? The previous version read better."},
		{name: "the word approved in a sentence", body: "I would have approved this, but the gate refuses it."},
		{name: "an approval without a commit", body: "REVIEW: APPROVED"},
		{name: "a decision crewflow does not know", body: "REVIEW: LGTM 1234567890123456789012345678901234567890"},
		{name: "a record of a scope, which is not a review", body: AcceptOf(second, "the design is this task as well")},
		{name: "an acceptance of the result, which is not a review", body: AcceptedOf(second)},
		{name: "the record as a person writes it by hand", body: "REVIEW: APPROVED " + second + "\n\nAll five points are fixed.", want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, is := Parse(tc.body); is != tc.want {
				t.Errorf("Parse(%q) is = %v, want %v", tc.body, is, tc.want)
			}
		})
	}
}

// TestParseAcceptance is the other record a person writes under a change: a file
// outside the boundaries of the task taken into their own hands, for a commit and
// with a reason (docs/DESIGN.md §7h).
func TestParseAcceptance(t *testing.T) {
	body := AcceptOf(second, "the design of §7h is this task as well")

	commit, reason, is := ParseAcceptance(body)
	if !is {
		t.Fatalf("ParseAcceptance did not read %q", body)
	}
	if commit != second {
		t.Errorf("the acceptance is of %s, want %s", commit, second)
	}
	if reason != "the design of §7h is this task as well" {
		t.Errorf("the reason of the acceptance is %q", reason)
	}
	if _, _, is := ParseAcceptance("REVIEW: APPROVED " + second); is {
		t.Error("an approval of a head was read as an acceptance of a file")
	}
}

// TestParseOwnerAccept is the record the owner of the project writes under a change to
// take the result of the task in, and nothing else of a comment: the head the record is
// about is the first line, and a commit added after it is a commit nobody has accepted
// (docs/DESIGN.md §7h).
func TestParseOwnerAccept(t *testing.T) {
	commit, is := ParseOwnerAccept(AcceptedOf(second) + "\n\nI have looked at it.")
	if !is {
		t.Fatalf("ParseOwnerAccept did not read %q", AcceptedOf(second))
	}
	if commit != second {
		t.Errorf("the acceptance is of %s, want %s", commit, second)
	}
	cases := []struct {
		name string
		body string
		want bool
	}{
		{name: "the record as a person writes it", body: "ACCEPTED " + second, want: true},
		{name: "with what the person wrote about it", body: "ACCEPTED " + second + " — I have read it twice", want: true},
		{name: "the word in a sentence", body: "I would have ACCEPTED this, but the checks are red."},
		{name: "an acceptance without a commit", body: "ACCEPTED"},
		{name: "an acceptance of a file, which is a record of its own", body: AcceptOf(second, "the design is this task as well")},
		{name: "an approval of the same head", body: ApproveOf(second, 7)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, is := ParseOwnerAccept(tc.body); is != tc.want {
				t.Errorf("ParseOwnerAccept(%q) is = %v, want %v", tc.body, is, tc.want)
			}
		})
	}
}

// TestTaskOf is what ties a change to the task it is for: the `Closes #N` of §7. A
// change whose task nobody can find out has no boundaries, and a gate that let it
// through on that would be a gate with a hole in it.
func TestTaskOf(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
		ok   bool
	}{
		{name: "the line of every change crewflow opens", body: "Closes #7\n\n## What changed", want: 7, ok: true},
		{name: "with what is in the same line", body: "Closes #7, closes #8", want: 7, ok: true},
		{name: "the other word a person writes", body: "Fixes #42", want: 42, ok: true},
		{name: "a body that says nothing of it", body: "## What changed\n\nSomething."},
		{name: "a mention in the middle of a sentence", body: "This closes #7 somewhere in the middle."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			number, ok := TaskOf(tc.body)
			if number != tc.want || ok != tc.ok {
				t.Errorf("TaskOf = (%d, %v), want (%d, %v)", number, ok, tc.want, tc.ok)
			}
		})
	}
}
