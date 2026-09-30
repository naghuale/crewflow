package gate

import (
	"context"
	"fmt"
)

// Change is what a check after a merge reads out of a change request: which change it
// is, what state the host holds it in, the commit at its head, and the task it is of.
//
// It is not [Facts] and is not judged: a change that has gone in is not a change anyone
// may merge, and the rules of the gate — the approval of it, the files it touches, the
// checks of its head, the history behind it — are questions about a change that may
// still go in. What a check after the merge asks is which commit the branch of the host
// is at, and a change that names no head is a fact nobody may act on (docs/DESIGN.md
// §6, §7h).
type Change struct {
	// Number is the change request and URL where a person reads it.
	Number int
	URL    string
	// State is what the host holds the change as: "open", "merged" or "closed".
	State string
	// Head is the commit at the head of the branch of the change. A fast-forward merge
	// puts it into the default branch of the project and nothing else, so it is the
	// commit that was merged even for a merge that happened on another machine.
	Head string
	// Task is the number of the task the change is of: the one this machine kept for the
	// change, and the one the `Closes #N` of the body names where this machine kept
	// none. A change merged on another machine has no state here, and the body is what
	// still says which task it was of (docs/DESIGN.md §7h).
	Task int
	// Unavailable is why the change could not be read at all, and it is a reason of its
	// own: a change nobody read is not a change that names no head, and a report that
	// says the first instead of the second sends a person to look for a fault of a
	// change that may not have one.
	Unavailable string
}

// ReadChange reads the change request of the number from the host, and nothing else of
// it: a check after a merge asks the host about the change, and one question it could
// not answer is told apart from another rather than swallowed (docs/DESIGN.md §7h).
func ReadChange(ctx context.Context, deps Deps, number int) Change {
	c := Change{Number: number}
	if deps.Forge == nil {
		c.Unavailable = "forge.kind: this project has no host of its code, so there is no change request to read"
		return c
	}
	change, err := deps.Forge.ChangeRequest(ctx, number)
	if err != nil {
		c.Unavailable = fmt.Sprintf("read the change request #%d: %v", number, err)
		return c
	}
	c.Number, c.URL = change.Number, change.URL
	c.State, c.Head = change.State, change.HeadSHA
	// The state this machine kept is the one the commit that was merged is recorded
	// under, so where there is one it is the number the check reads that state by; the
	// body of the change is what says which task it is of where this machine kept no
	// state of it, which is every change merged on another machine (§7).
	c.Task = deps.Task
	if c.Task <= 0 {
		c.Task, _ = TaskOf(change.Body)
	}
	return c
}
