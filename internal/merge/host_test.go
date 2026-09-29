package merge

import (
	"context"
	"fmt"
	"time"

	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/gate"
)

// host is the tracker, the host of the code and the CI of a project of a test: what
// the tracker holds, what the host knows about a change, what is written under it, the
// files it touches, the checks of its head and the rules of the branch that demand them
// — and how the checks of a commit stand when they are asked about, and when the task
// of a change is closed behind it.
//
// Nothing of it is a network and nothing of it is a repository: no test of a merge
// reaches GitHub or a checkout of the person who runs it (docs/DESIGN.md §7h).
type host struct {
	task      forge.Task
	taskErr   error
	change    forge.ChangeRequest
	changeErr error
	comments  []forge.Comment
	files     []string
	checks    []forge.CheckRun
	required  []forge.RequiredCheck
	// stands is how the checks of a commit stand when they are asked about, oldest
	// answer first: a check that is going on answers `pending` and then `success`, and
	// that is what a check after a merge waits for (docs/DESIGN.md §7h).
	stands map[string][]forge.CheckState
	asked  map[string]int
	// closedAfter is after how many readings of the task it says it is closed. The
	// host closes an issue behind a change that has gone in, and a test of a merge
	// waiting for that must not wait for it (docs/DESIGN.md §7h).
	closedAfter int
	reads       int
}

// comment is a record under the change of a test: what is written, by whom, and whether
// it has been edited since it was published — the two facts about a record that say
// whether the gate counts it (docs/DESIGN.md §7h).
func (h *host) comment(body, author string, edited ...bool) {
	wasEdited := len(edited) > 0 && edited[0]
	h.comments = append(h.comments, forge.Comment{
		Author:    author,
		Body:      body,
		CreatedAt: time.Date(2026, time.October, 1, 11, 0, 0, 0, time.UTC),
		Edited:    wasEdited,
	})
}

// approved is a record of the owner of the repository approving the commit, which is
// what the gate counts and what nothing else stands over (docs/DESIGN.md §7h).
func (h *host) approved(commit string) {
	h.comment(gate.ApproveOf(commit, h.task.Number), owner)
}

// Task is what the tracker holds: the task of the change, open until the host has closed
// it as many times as the case says it should.
func (h *host) Task(_ context.Context, number int) (forge.Task, error) {
	if h.taskErr != nil {
		return forge.Task{}, h.taskErr
	}
	if h.task.Number != number {
		return forge.Task{}, fmt.Errorf("could not find issue %d", number)
	}
	h.reads++
	found := h.task
	if h.closedAfter >= 0 && h.reads > h.closedAfter {
		found.State = "closed"
	}
	return found, nil
}

// ChangeRequest is the change as the host holds it, or the answer it did not give.
func (h *host) ChangeRequest(_ context.Context, number int) (forge.ChangeRequest, error) {
	if h.changeErr != nil {
		return forge.ChangeRequest{}, h.changeErr
	}
	if h.change.Number != number {
		return forge.ChangeRequest{}, fmt.Errorf("could not find change request %d", number)
	}
	return h.change, nil
}

// FindChangeRequest is the change of the branch, which a merge never asks for: it is
// named by its number.
func (h *host) FindChangeRequest(context.Context, string) (forge.ChangeRequest, bool, error) {
	return h.change, true, nil
}

// Comments is what is written under the change, oldest first.
func (h *host) Comments(context.Context, int) ([]forge.Comment, error) { return h.comments, nil }

// ChangedFiles is what the change touches, as the host knows it.
func (h *host) ChangedFiles(context.Context, int) ([]string, error) { return h.files, nil }

// Checks is what the head of the change has, and what the rules of the branch demand of
// it (docs/DESIGN.md §7h).
func (h *host) Checks(context.Context, string) ([]forge.CheckRun, error) { return h.checks, nil }

func (h *host) RequiredChecks(context.Context) ([]forge.RequiredCheck, error) {
	return h.required, nil
}

// Status is how the checks of the commit stand. A commit nothing was scripted for is
// green: a check after a merge asks about the branch of the host, and the case of a
// check that is going on is the one that says what it will be.
func (h *host) Status(_ context.Context, sha string) (forge.CheckState, error) {
	if h.asked == nil {
		h.asked = map[string]int{}
	}
	answer, is := h.stands[sha]
	if !is {
		return forge.CheckSuccess, nil
	}
	at := h.asked[sha]
	h.asked[sha] = at + 1
	if at >= len(answer) {
		return answer[len(answer)-1], nil
	}
	return answer[at], nil
}

// HeadRef is the ref of the host that stands at the head of a change: the name GitHub
// keeps the head of a pull request under (docs/DESIGN.md §7h).
func (h *host) HeadRef(number int) string { return headRef(number) }

// SignedIn is the account the host speaks as, which is the owner of the repository in a
// test: a record of a review counts only because a reviewer of the project wrote it.
func (h *host) SignedIn(context.Context) (string, error) { return owner, nil }

// Doctor says nothing: a test of a merge has a host of its own already.
func (h *host) Doctor(context.Context) []forge.Check { return nil }

// The host of a test is everything a merge asks of a host of a project: the task, the
// change, the records under it, the files it touches, the checks of its head, the rules
// of the branch, the ref of the head and how the checks of a commit stand.
var (
	_ forge.Tracker     = (*host)(nil)
	_ forge.Forge       = (*host)(nil)
	_ forge.CI          = (*host)(nil)
	_ forge.CheckLister = (*host)(nil)
	_ forge.FileLister  = (*host)(nil)
	_ forge.HeadRef     = (*host)(nil)
	_ forge.SignedIn    = (*host)(nil)
)
