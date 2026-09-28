package gate

import (
	"fmt"
	"strings"
)

// The two records a person writes under a change, one line each, and nothing else
// of a comment is read by the gate: the rest of it is for the people who read it
// (docs/DESIGN.md §7h).
const (
	// reviewMark opens a record of a review, and what follows it is the decision:
	// `REVIEW: APPROVED <full sha>` or `REVIEW: CHANGES REQUESTED`.
	reviewMark = "REVIEW"
	// scopeMark opens a record in which a person takes the files of a change
	// outside the boundaries of its task into their own hands:
	// `SCOPE: ACCEPTED <full sha> <why>`.
	scopeMark = "SCOPE"
)

// Decision is what a record of a review says about a change. There are two words,
// and no third one: a record crewflow cannot read is not an approval.
type Decision string

const (
	// Approved is a record that says the head of the change may be merged.
	Approved Decision = "approved"
	// ChangesRequested is a record that asks for changes, and stands over any
	// approval written before it.
	ChangesRequested Decision = "changes_requested"
)

// blockMark opens the machine-readable part of a record: the decisions above are
// for a person, and this is for whatever has to read the record without a person.
// Everything between this line and the end of the comment is the block.
const blockMark = "<!-- crewflow-review -->"

// Record is a record of a review as the gate reads it: what was decided, about
// which commit, and for which task. It is the first line of a comment and the
// machine-readable block under it, and the first line is what counts: a block that
// says something else is a change of the format, not a decision (docs/DESIGN.md §7h).
type Record struct {
	// Decision is what the record says about the change.
	Decision Decision
	// Commit is the head the record is about, the whole SHA of it. A record that
	// changes nothing says nothing about a commit, and the gate does not guess
	// one: an approval without a commit approves nothing.
	Commit string
	// Task is the task the change is of, zero when the record names none.
	Task int
}

// Parse reads a record of a review out of a comment, and whether there is one at
// all: a comment that is not a record is a comment, and a person talking under a
// change says many things that are not decisions.
func Parse(body string) (Record, bool) {
	fields := strings.Fields(firstLineOf(body))
	if len(fields) < 2 || !isMark(fields[0], reviewMark) {
		return Record{}, false
	}
	switch {
	case len(fields) >= 3 && fields[1] == "APPROVED":
		return Record{Decision: Approved, Commit: fields[2], Task: taskIn(body)}, true
	case len(fields) == 3 && fields[1] == "CHANGES" && fields[2] == "REQUESTED":
		return Record{Decision: ChangesRequested, Task: taskIn(body)}, true
	default:
		return Record{}, false
	}
}

// ParseAcceptance reads a record in which a person takes the files outside the
// boundaries of a task into their own hands: the commit it is about and the reason
// the person gave, and whether there is such a record at all. Who wrote it and
// whether it was edited are the facts of the comment it is in, not of its text.
func ParseAcceptance(body string) (commit, reason string, ok bool) {
	fields := strings.Fields(firstLineOf(body))
	if len(fields) < 3 || !isMark(fields[0], scopeMark) || fields[1] != "ACCEPTED" {
		return "", "", false
	}
	return fields[2], strings.Join(fields[3:], " "), true
}

// ApproveOf is the record that approves the commit: the first line the gate reads
// is the whole of the decision, and the block under it is for whatever has to read
// the record without a person.
func ApproveOf(commit string, number int) string {
	return record(reviewMark+": APPROVED "+commit, Approved, commit, number, "")
}

// ChangesOf is the record that asks for changes, with the findings a person wrote
// about them: the text goes under the decision, so that the record reads as the
// conversation it is and the block under it stays for the programs.
func ChangesOf(commit string, number int, findings string) string {
	return record(reviewMark+": CHANGES REQUESTED", ChangesRequested, commit, number, findings)
}

// AcceptOf is the record in which a person takes the files of a change outside the
// boundaries of its task into their own hands, and says why: the reason is part of
// the record, because an acceptance nobody can account for is an acceptance of
// anything.
func AcceptOf(commit, reason string) string {
	return fmt.Sprintf("%s: ACCEPTED %s %s\n", scopeMark, commit, strings.TrimSpace(reason))
}

// ApprovedOf is the first line of a record that approves the commit and nothing
// else, which is all [Apart] needs to put a record in place of the ones there are.
func ApprovedOf(commit string) string {
	return reviewMark + ": APPROVED " + commit
}

// record is one record of a review: the decision as a person reads it, whatever
// the person wrote under it, and the block for the programs. The block holds the
// same decision in the words of §7h, so that a change of the first line and a
// change of the design go together.
func record(head string, decision Decision, commit string, number int, findings string) string {
	var out strings.Builder
	out.WriteString(head)
	if body := strings.TrimSpace(findings); body != "" {
		out.WriteString("\n\n")
		out.WriteString(body)
	}
	out.WriteString("\n\n")
	out.WriteString(blockMark)
	fmt.Fprintf(&out, "\ndecision: %s", decision)
	fmt.Fprintf(&out, "\ncommit: %s", commit)
	if number > 0 {
		fmt.Fprintf(&out, "\ntask: %d", number)
	}
	out.WriteString("\n")
	return out.String()
}

// taskIn is the task the record names in its block, and zero when it names none: a
// record of before the block was written says nothing, and the task of a change is
// known from the change itself.
func taskIn(body string) int {
	_, block, found := strings.Cut(body, blockMark)
	if !found {
		return 0
	}
	for line := range strings.Lines(block) {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if ok && key == "task" {
			var number int
			if _, err := fmt.Sscanf(strings.TrimSpace(value), "%d", &number); err == nil {
				return number
			}
		}
	}
	return 0
}

// isMark is whether the first word of a line is the mark of a record, with or
// without the colon after it: a person writes `REVIEW: APPROVED …` and a program
// splits it into words, and the colon is not part of the mark.
func isMark(word, mark string) bool {
	return strings.TrimSuffix(word, ":") == mark
}

// firstLineOf is the first line of a comment, with the line endings of a host that
// writes them the other way as well: what a record is decided by is its first line,
// and a comment that begins with a blank line is a comment and not a record.
func firstLineOf(body string) string {
	line, _, _ := strings.Cut(strings.TrimLeft(body, " \t\r\n"), "\n")
	return line
}
