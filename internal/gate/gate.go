// Package gate decides whether a change may be merged, and names the one reason why
// it may not (docs/DESIGN.md §7h).
//
// It is the heart of crewflow: everything else exists so that this package can be
// handed the facts of a change and give the same answer every time. Two rules hold
// it up.
//
// [Evaluate] is pure. The facts go in, the verdict comes out, and nothing inside it
// touches a network, a repository or a clock. The facts are gathered by [Collect],
// which is the only part of the package that asks anybody anything — and a fact that
// could not be gathered is a refusal with a reason, never a guess (§7h).
//
// A verdict is never written down. "Approved" and "ready to merge" are worked out
// from the host and from git in the moment they are asked for, so that they cannot
// go out of date and cannot disagree with the host (§7h).
package gate

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/task"
)

// Reason is why a change may not be merged, and one of the words of the table of
// docs/DESIGN.md §7h and nothing else. A gate that could name a refusal nobody
// wrote a test against is a gate whose refusals nobody has to obey, so the set of
// words is closed here and every one of them has a case in the tests of this
// package.
type Reason string

// The reasons of a refusal, in the order the gate finds them. The first two of the
// merge are what happens to the push of it, and this package never pushes: they are
// here because the table of §7h names them, and the merge that comes next writes
// them (docs/DESIGN.md §7h).
const (
	// ApprovalMissing says that there is no record of a review to count, or that
	// the last record to count is not an approval.
	ApprovalMissing Reason = "approval-missing"
	// ApprovalUntrusted says that the only approvals came from accounts that are
	// not among the reviewers of the project.
	ApprovalUntrusted Reason = "approval-untrusted"
	// ApprovalEdited says that an approval was edited after it was published,
	// which is how an old record would be rewritten in silence.
	ApprovalEdited Reason = "approval-edited"
	// ApprovalStale says that the approved commit is an ancestor of the head:
	// commits were added after the approval, and nobody approved them.
	ApprovalStale Reason = "approval-stale"
	// HistoryRewritten says that the approved commit is not in the history of the
	// head at all: the branch was rewritten under the approval.
	HistoryRewritten Reason = "history-rewritten"
	// OwnerAcceptanceMissing says that the task is one the owner has to accept, and
	// that there is no record of it for the head of the change: either nobody wrote
	// one, or the one that is there is of a commit that is not the head any more.
	OwnerAcceptanceMissing Reason = "owner-acceptance-missing"
	// OwnerAcceptanceUntrusted says that the only records of an acceptance came from
	// accounts that are not among the owners of the project.
	OwnerAcceptanceUntrusted Reason = "owner-acceptance-untrusted"
	// OwnerAcceptanceEdited says that a record of an acceptance was edited after it was
	// published, which is how an acceptance of another commit could be written in
	// silence.
	OwnerAcceptanceEdited Reason = "owner-acceptance-edited"
	// PRNotOpen says that the change request is closed or merged.
	PRNotOpen Reason = "pr-not-open"
	// PRDraft says that the change request is a draft and asks for nothing yet.
	PRDraft Reason = "pr-draft"
	// WrongRepository says that the change request is not of the project of the
	// file that is being judged.
	WrongRepository Reason = "wrong-repository"
	// WrongTargetBranch says that the change request is meant for another branch
	// than the one the project starts from and merges into.
	WrongTargetBranch Reason = "wrong-target-branch"
	// OutOfScope says that the change touches files the task was not to change.
	OutOfScope Reason = "out-of-scope"
	// RequiredCheckMissing says that a check the branch rules of the host demand
	// is not on the head.
	RequiredCheckMissing Reason = "required-check-missing"
	// RequiredCheckIncomplete says that a required check has not finished yet.
	RequiredCheckIncomplete Reason = "required-check-incomplete"
	// RequiredCheckFailed says that a required check has finished and did not
	// pass.
	RequiredCheckFailed Reason = "required-check-failed"
	// RequiredCheckUntrusted says that a check was reported by something other
	// than the app the gate takes it from.
	RequiredCheckUntrusted Reason = "required-check-untrusted"
	// CISHAMismatch says that a green check is about another commit.
	CISHAMismatch Reason = "ci-sha-mismatch"
	// NotFastForward says that the default branch of the project is not an
	// ancestor of the head, so a merge of it would not be a fast-forward.
	NotFastForward Reason = "not-fast-forward"
	// PushRejected says that the host refused the push of a merge.
	PushRejected Reason = "push-rejected"
	// VerifyMismatch says that after the push of a merge the branch of the host
	// points somewhere other than the commit that was approved.
	VerifyMismatch Reason = "verify-mismatch"
	// ForgeUnavailable says that the host or the network did not give a certain
	// answer. It is the refusal of everything unknown: a gate that guessed would
	// be a gate that let a change through on a guess (docs/DESIGN.md §7h).
	ForgeUnavailable Reason = "forge-unavailable"
)

// Reasons is every reason of the table of §7h, in the order they are written in
// there. The merge reasons are in it as well: they are the words of a refusal of
// crewflow, and a caller that counts them must be able to name them all.
var Reasons = []Reason{
	ApprovalMissing, ApprovalUntrusted, ApprovalEdited, ApprovalStale, HistoryRewritten,
	OwnerAcceptanceMissing, OwnerAcceptanceUntrusted, OwnerAcceptanceEdited,
	PRNotOpen, PRDraft, WrongRepository, WrongTargetBranch, OutOfScope,
	RequiredCheckMissing, RequiredCheckIncomplete, RequiredCheckFailed, RequiredCheckUntrusted,
	CISHAMismatch, NotFastForward, PushRejected, VerifyMismatch, ForgeUnavailable,
}

// Verdict is the answer of the gate about one change: whether it may be merged, and
// the one reason why it may not. The reason is of the table above and never a
// phrase of its own, so that a program and a person read the same word for the
// same refusal (docs/DESIGN.md §7h).
type Verdict struct {
	// Ready says that everything the gate checks is in order. There is no verdict
	// without a reason and a ready one, and a ready one names no reason.
	Ready bool `json:"ready"`
	// Reason is the one thing that is not in order, and Detail is what a person
	// has to do about it: the names of the files, the name of the check, the two
	// commits, whatever the refusal is about.
	Reason Reason `json:"reason,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// String is the verdict as one line reads in a report: "ready to merge" or the
// reason and what to do about it.
func (v Verdict) String() string {
	if v.Ready {
		return "ready to merge"
	}
	return string(v.Reason) + ": " + v.Detail
}

// refused is a verdict that says no, with the reason of the table of §7h and what a
// person is to do about it.
func refused(reason Reason, detail string) Verdict {
	return Verdict{Reason: reason, Detail: detail}
}

// Facts are the things a verdict is made of, and nothing else: what the host says
// about the change, what its review records say, what the CI of the head says, which
// files the change touches against the boundaries of its task, and what git says
// about the history. A verdict is a pure function of the facts, so the same facts
// always give the same answer, and a fact that is not here is a fact the gate
// refuses for (docs/DESIGN.md §7h).
type Facts struct {
	// Number and URL are what the change is and where a person reads it, so that
	// a report names it without the host being read again.
	Number int
	URL    string
	// Open, Draft, Conflicted, MergeState, Repository, TargetBranch and Head are
	// the change as the host holds it. Conflicted says that the change cannot be
	// merged into the branch it is meant for as it stands: GitHub does not even run
	// the checks of such a change, so the gate names the conflict instead of
	// reporting missing checks. MergeState is the word of the host about it, which
	// a report shows as it is.
	Open         bool
	Draft        bool
	Conflicted   bool
	MergeState   string
	Repository   string
	TargetBranch string
	Head         string
	// WantRepository and WantBranch are the repository of the project and the
	// branch it starts from and merges into: a change of anywhere else is not a
	// change of this project, whatever its number says.
	WantRepository string
	WantBranch     string
	// Reviews are the records of a review under the change, oldest first, and
	// Reviewers the logins whose records count. A record of anybody else is read
	// and shown, and does not approve anything: an executor may write "approved"
	// under its own change, and the gate is what does not take it (docs/DESIGN.md §7h).
	Reviews   []Review
	Reviewers []string
	// Accepted are the records in which a person takes the files outside the
	// boundaries of the task into their own hands, under the same rules of the
	// author and of the editing as an approval.
	Accepted []Acceptance
	// Required is what the rules of the branch of the project demand of a change,
	// and Checks is what the head of this change has. A check that is required and
	// not there is not in Checks, which is what `required-check-missing` is about.
	Required []forge.RequiredCheck
	Checks   []forge.CheckRun
	// Rules is what the host said about the rules of the branch themselves: they are
	// there, or the plan of the repository has none to demand anything with, and then
	// what the project says about its CI is what stands (docs/DESIGN.md §7h, §7k).
	Rules forge.RuleState
	// Files is what the change touches, against the branch it is meant for, and
	// Boundaries is what the task of the change was to touch. A change whose task
	// crewflow does not know has no boundaries, and no file is inside them.
	Files      []string
	Boundaries []string
	// Labels are the words the tracker marks the task of the change with, and
	// AcceptanceLabel the one of them that says the owner has to accept the result
	// himself before it may go in. A project that names none has `owner-check`, and a
	// task without that label is merged as it always was (docs/DESIGN.md §5, §7f, §7h).
	Labels          []string
	AcceptanceLabel string
	// Owners are the logins whose record of an acceptance counts, with the default of
	// §5 filled in, and OwnerAccepts the records in which the owner of the project says
	// that they have taken the result of the task as it stands: under the same rules of
	// the author and of the editing as an approval, because an acceptance anybody can
	// rewrite is not one (docs/DESIGN.md §7h).
	Owners       []string
	OwnerAccepts []OwnerAccept
	// Task is the number of the task the change is of, which a report names.
	Task int
	// DefaultIsAncestor says that the default branch of the project is an ancestor
	// of the head, and ApprovedIsAncestor that the approved commit is: the second
	// one is not a condition of an approval, it is what tells a stale approval
	// from a rewritten history (docs/DESIGN.md §7h).
	DefaultIsAncestor  bool
	ApprovedIsAncestor bool
	// Unavailable is why the facts could not be gathered, and it is a reason of its
	// own: an answer the host did not give is not an answer that says yes.
	Unavailable string
}

// Review is one record of a review under a change as a comment: who wrote it, when,
// whether anybody has edited it since it was published, and what it says. A record
// that has been edited does not count, because an approval a person can rewrite
// after the fact is not an approval of anything (docs/DESIGN.md §7h).
type Review struct {
	// Author is who wrote it, by the name the host knows them under.
	Author string
	// CreatedAt orders the records: the last one that counts is the one that
	// stands.
	CreatedAt time.Time
	// Edited says that the record was changed after it was published.
	Edited bool
	// Body is what was written, which is where the record itself is in.
	Body string
}

// Acceptance is one record in which a person takes the files of a change outside
// the boundaries of its task into their own hands: for which commit, and why
// (docs/DESIGN.md §7h).
type Acceptance struct {
	// Author is who wrote it, by the name the host knows them under.
	Author string
	// CreatedAt orders the records against each other.
	CreatedAt time.Time
	// Edited says that the record was changed after it was published, which
	// takes it out of the count as it takes an approval out of it.
	Edited bool
	// Commit is the head the record was written for: a record for an earlier head
	// says nothing about the files that were added since.
	Commit string
	// Reason is what the person wrote about it, which a report shows.
	Reason string
}

// OwnerAccept is one record in which the owner of the project takes the result of a
// task as it stands: `ACCEPTED <full sha>` under the change (docs/DESIGN.md §7h).
//
// It is a record and not a word in a report: the owner writes it under the change
// himself, in his own account, and the gate reads it there — which is the whole of
// what makes it his own look at the result and not somebody else's word that he did.
type OwnerAccept struct {
	// Author is who wrote it, by the name the host knows them under.
	Author string
	// CreatedAt orders the records against each other.
	CreatedAt time.Time
	// Edited says that the record was changed after it was published, which takes it
	// out of the count as it takes an approval out of it.
	Edited bool
	// Commit is the head the record was written for: a commit added after it is a
	// commit nobody has accepted, and the acceptance of it has to be written anew.
	Commit string
}

// Evaluate is the whole of the gate: the facts of a change go in, and the verdict
// comes out. The order of the checks is the order of `merge_ready` in docs/DESIGN.md
// §7h, and the first one that does not hold is the reason: a person who is told
// one reason can go and fix it, and a gate that named all of them at once would
// name the one that is not in the way.
//
// Nothing here reaches the network, a repository or a clock, which is what lets the
// same facts be judged the same way by a person reading a report and by a merge
// that is about to happen.
func Evaluate(f Facts) Verdict {
	if f.Unavailable != "" {
		return refused(ForgeUnavailable, f.Unavailable)
	}
	if !f.Open {
		return refused(PRNotOpen, "the change request is not open: a closed change is a change of nothing")
	}
	if f.Draft {
		return refused(PRDraft, "the change request is a draft: mark it ready for review first")
	}
	if f.Repository != f.WantRepository {
		return refused(WrongRepository, fmt.Sprintf("the change request is of %q, the project is %q", f.Repository, f.WantRepository))
	}
	if f.TargetBranch != f.WantBranch {
		return refused(WrongTargetBranch, fmt.Sprintf("the change request is meant for %q, the project merges into %q", f.TargetBranch, f.WantBranch))
	}
	if verdict, no := f.approval(); no {
		return verdict
	}
	if verdict, no := f.ownerAcceptance(); no {
		return verdict
	}
	if verdict, no := f.boundaries(); no {
		return verdict
	}
	// A change that conflicts with its branch is asked about before the checks,
	// because there are none: GitHub does not run the checks of a change it
	// cannot merge, and reporting the checks as missing would be a fact about a
	// commit that was never made.
	if f.Conflicted {
		return refused(NotFastForward, fmt.Sprintf("the change conflicts with %s, so nothing may be merged into it and CI will not run: rebase the branch onto %s", f.WantBranch, f.WantBranch))
	}
	if verdict, no := f.checks(); no {
		return verdict
	}
	if !f.DefaultIsAncestor {
		return refused(NotFastForward, fmt.Sprintf("%s is not an ancestor of the head: it has moved on since the branch was cut, rebase the branch onto %s", f.WantBranch, f.WantBranch))
	}
	return Verdict{Ready: true}
}

// Apart is the verdict on everything but the approval and the acceptance: the same
// facts with the records of the reviews taken out, a record of an approval of the
// current head in their place written by the first of the trusted reviewers, and a
// record of an acceptance of that head in the place of the ones there are.
//
// It is what the orchestrator is shown before it writes a real approval, so that
// an approval of a change with a red CI or a file outside the boundaries of its
// task is never written by mistake — a comment that says "approved" is not a gate,
// and a gate that approves such a change has stopped being one (docs/DESIGN.md §7h).
// The acceptance of the owner is stood in for as well: it is his own act and the
// orchestrator cannot write it, and a gate that refused an approval for a missing
// acceptance would leave a task marked `owner-check` without a way to start at all.
func Apart(f Facts) Verdict {
	f.Reviews = []Review{{
		Author: firstOf(f.reviewers()),
		Body:   ApprovedOf(f.Head),
	}}
	f.OwnerAccepts = []OwnerAccept{{
		Author: firstOf(f.owners()),
		Commit: f.Head,
	}}
	return Evaluate(f)
}

// approval is the whole of `approval_valid` of docs/DESIGN.md §7h: a record of a
// review counts when it is of a reviewer of the project and has not been edited,
// the last record that counts is the one that stands, and it stands only when it
// approves the head of the change.
//
// Each way of it failing is a reason of its own, because each is fixed by
// something else: a record of an executor is a record of nobody, a record that
// was edited is a record that can be rewritten, a record of an earlier head is a
// record of work that has grown since, and a record of a commit that is not in the
// history any more is a record of a history somebody rewrote.
func (f Facts) approval() (Verdict, bool) {
	ofReviewers := f.reviewsOfReviewers()
	if len(ofReviewers) == 0 {
		if authors := authorsOf(f.Reviews, func(review Review) string { return review.Author }); len(authors) > 0 {
			return refused(ApprovalUntrusted, fmt.Sprintf("the only records of a review are of %s, and the reviewers of the project are %s",
				listed(authors), listed(f.reviewers()))), true
		}
		return refused(ApprovalMissing, "there is no record of a review under the change"), true
	}
	// A record that was edited after it was published is a record anybody can
	// rewrite in silence. An approval like that is named as what it is, and any
	// other edited record is not a record at all: the one before it stands.
	if last := ofReviewers[len(ofReviewers)-1]; last.Edited {
		if record, is := Parse(last.Body); is && record.Decision == Approved {
			return refused(ApprovalEdited, fmt.Sprintf("the approval of %s was edited after it was published: an approval that can be rewritten approves nothing",
				last.Author)), true
		}
	}
	counted := f.trustedReviews()
	if len(counted) == 0 {
		return refused(ApprovalMissing, fmt.Sprintf("the last record of a review of %s was edited after it was published, and an edited record counts for nothing",
			ofReviewers[len(ofReviewers)-1].Author)), true
	}
	last := counted[len(counted)-1]
	record, is := Parse(last.Body)
	if !is {
		return refused(ApprovalMissing, "the last record of a review is not a record crewflow reads"), true
	}
	if record.Decision != Approved {
		return refused(ApprovalMissing, fmt.Sprintf("the last record of a review asks for changes (%s), so there is no approval of the head",
			last.Author)), true
	}
	if sameCommit(record.Commit, f.Head) {
		return Verdict{}, false
	}
	if f.ApprovedIsAncestor {
		return refused(ApprovalStale, fmt.Sprintf("the approval is of %s and the head is %s: commits were added after the approval, and nobody approved them",
			record.Commit, f.Head)), true
	}
	return refused(HistoryRewritten, fmt.Sprintf("the approved commit %s is not in the history of the head %s: the history was rewritten under the approval",
		record.Commit, f.Head)), true
}

// ownerAcceptance is the whole of `owner_accepted(pr)` of docs/DESIGN.md §7h: the
// result of a task marked `owner-check` is taken in by a record of one of the owners
// of the project, written for exactly the head of the change and not edited since it
// was published.
//
// Each way of it failing is a reason of its own, because each is fixed by something
// else: a record of an executor is a record of nobody, a record that was edited is a
// record anybody can rewrite in silence, and a record of an earlier head is a record
// of work that has grown since.
func (f Facts) ownerAcceptance() (Verdict, bool) {
	if !f.requiresOwnerAcceptance() {
		return Verdict{}, false
	}
	records := f.ownerAcceptsOfOwners()
	if len(records) == 0 {
		if authors := authorsOf(f.OwnerAccepts, func(record OwnerAccept) string { return record.Author }); len(authors) > 0 {
			return refused(OwnerAcceptanceUntrusted, fmt.Sprintf("the only records of an acceptance are of %s, and the owners of the project are %s",
				listed(authors), listed(f.owners()))), true
		}
		return refused(OwnerAcceptanceMissing, fmt.Sprintf("task %d is marked `%s`, and there is no record of an acceptance under the change: "+
			"the owner takes the result in himself, with a record `ACCEPTED %s` under the change", f.Task, f.AcceptanceLabel, f.Head)), true
	}
	last := records[len(records)-1]
	if last.Edited {
		return refused(OwnerAcceptanceEdited, fmt.Sprintf("the acceptance of %s was edited after it was published: an acceptance that can be rewritten accepts nothing",
			last.Author)), true
	}
	if sameCommit(last.Commit, f.Head) {
		return Verdict{}, false
	}
	return refused(OwnerAcceptanceMissing, fmt.Sprintf("the acceptance is of %s and the head is %s: commits were added after the owner accepted the result, and nobody has accepted them",
		last.Commit, f.Head)), true
}

// requiresOwnerAcceptance is whether the task of the change is one the owner has to accept
// before it may go in, which is what the label of the project says: a task without
// that label is merged as it always was, and a project that names no label at all has
// nothing to require of anybody (docs/DESIGN.md §5, §7f, §7h).
func (f Facts) requiresOwnerAcceptance() bool {
	return f.AcceptanceLabel != "" && slices.Contains(f.Labels, f.AcceptanceLabel)
}

// ownerAcceptsOfOwners are the records of an acceptance of somebody who may accept,
// edited or not, in the order they were written.
func (f Facts) ownerAcceptsOfOwners() []OwnerAccept {
	var records []OwnerAccept
	for _, record := range f.OwnerAccepts {
		if f.isOwner(record.Author) {
			records = append(records, record)
		}
	}
	return records
}

// boundaries are the paths the task of the change was to touch, and the files it
// touches. Only a person may take a file outside them into their own hands, and
// only for the head as it stands: a record of acceptance for an earlier head says
// nothing about what was added since.
func (f Facts) boundaries() (Verdict, bool) {
	outside, err := f.Outside()
	if err != nil {
		return refused(OutOfScope, fmt.Sprintf("the boundaries of task %d are not paths crewflow reads: %v", f.Task, err)), true
	}
	if len(outside) == 0 {
		return Verdict{}, false
	}
	if _, ok := f.accepted(); ok {
		return Verdict{}, false
	}
	return refused(OutOfScope, fmt.Sprintf("the change touches %s, which task %d was not to change; "+
		"only %s may take a file into their own hands, with a record `SCOPE: ACCEPTED %s <why>` under the change",
		listed(outside), f.Task, listed(f.reviewers()), f.Head)), true
}

// Outside are the files the change touches that the task of it was not to change,
// and the error of reading its boundaries. A change whose task crewflow does not
// know has no boundaries at all, and then every file of it is outside them: a gate
// that let a change through because nobody could say what it was to touch would be
// a gate with a hole in it (docs/DESIGN.md §7c, §7h).
func (f Facts) Outside() ([]string, error) {
	return task.Boundaries(f.Boundaries).Outside(f.Files)
}

// accepted is the last record of acceptance that counts: of a reviewer, not edited,
// and written for the head as it stands.
func (f Facts) accepted() (Acceptance, bool) {
	records := make([]Acceptance, 0, len(f.Accepted))
	for _, record := range f.Accepted {
		if !f.isReviewer(record.Author) || record.Edited {
			continue
		}
		records = append(records, record)
	}
	if len(records) == 0 {
		return Acceptance{}, false
	}
	last := records[len(records)-1]
	return last, sameCommit(last.Commit, f.Head)
}

// ownerAccepted is the last record in which the owner of the project takes the result
// of the task in, and whether it counts: only a record of an owner, only while it has
// not been edited, and only for the head as it stands — the same three ways an
// approval is counted, because an acceptance anybody can rewrite, and an acceptance of
// a commit that has grown since, are not a look at the result (docs/DESIGN.md §7h).
func (f Facts) ownerAccepted() (OwnerAccept, bool) {
	records := f.ownerAcceptsOfOwners()
	if len(records) == 0 {
		return OwnerAccept{}, false
	}
	last := records[len(records)-1]
	return last, !last.Edited && sameCommit(last.Commit, f.Head)
}

// checks is what the head of the change has of what the rules of its branch demand,
// and it is asked in the order of §7h: a check that is not there at all comes
// before a check that has not ended, a check that has ended red comes before a
// check of the wrong app, and a check of another commit comes last of all, because
// it is the one that says the change has not been looked at since.
func (f Facts) checks() (Verdict, bool) {
	found, missing := f.demanded()
	if len(missing) > 0 {
		return refused(RequiredCheckMissing, fmt.Sprintf("the head has no check named %s, which the rules of the branch require",
			listed(missing))), true
	}
	for _, pair := range found {
		if pair.got.State == forge.CheckPending {
			return refused(RequiredCheckIncomplete, fmt.Sprintf("the check %q of the head has not finished yet", pair.got.Name)), true
		}
	}
	for _, pair := range found {
		if pair.got.State != forge.CheckSuccess {
			return refused(RequiredCheckFailed, fmt.Sprintf("the check %q of the head did not pass", pair.got.Name)), true
		}
	}
	for _, pair := range found {
		if !strings.EqualFold(pair.got.App, pair.want.App) {
			return refused(RequiredCheckUntrusted, fmt.Sprintf("the check %q of the head was reported by %q, and only %q is taken for it",
				pair.got.Name, nameOf(pair.got.App), pair.want.App)), true
		}
	}
	for _, pair := range found {
		if !sameCommit(pair.got.SHA, f.Head) {
			return refused(CISHAMismatch, fmt.Sprintf("the check %q is green for %s, and the head is %s: CI has not run for this change",
				pair.got.Name, pair.got.SHA, f.Head)), true
		}
	}
	return Verdict{}, false
}

// demanded is every check the rules of the branch demand, with the one the head has
// under that name — or without one, which is what a missing check is.
type demanded struct {
	want forge.RequiredCheck
	got  forge.CheckRun
}

// demanded is the required checks against the ones the head has, and the names of
// those it has none of. A change request names a check twice or a head reports it
// under two apps: the first one of each name stands, because a refusal that names
// the same check twice is a refusal nobody reads.
func (f Facts) demanded() (found []demanded, missing []string) {
	seen := make(map[string]struct{}, len(f.Required))
	for _, want := range f.Required {
		if _, duplicate := seen[want.Name]; duplicate {
			continue
		}
		seen[want.Name] = struct{}{}
		check, ok := f.checkOf(want.Name)
		if !ok {
			missing = append(missing, want.Name)
			continue
		}
		found = append(found, demanded{want: want, got: check})
	}
	return found, missing
}

// checkOf is the first check the head has under the name, and whether it has one.
func (f Facts) checkOf(name string) (forge.CheckRun, bool) {
	for _, check := range f.Checks {
		if check.Name == name {
			return check, true
		}
	}
	return forge.CheckRun{}, false
}

// trustedReviews are the records of a review that count: of a reviewer of the
// project and not edited since they were published, in the order they were written.
func (f Facts) trustedReviews() []Review {
	counted := make([]Review, 0, len(f.Reviews))
	for _, review := range f.Reviews {
		if f.isReviewer(review.Author) && !review.Edited {
			counted = append(counted, review)
		}
	}
	return counted
}

// reviewsOfReviewers are the records of a review of somebody who may approve, edited
// or not, in the order they were written. A record of anybody else is not one of
// them: an executor may write whatever it likes under its own change, and none of
// it is the word of a reviewer.
func (f Facts) reviewsOfReviewers() []Review {
	var records []Review
	for _, review := range f.Reviews {
		if f.isReviewer(review.Author) {
			records = append(records, review)
		}
	}
	return records
}

// Counted is the last record of a review the gate counts, and whether there is one:
// a summary of a change says which decision stands and who wrote it, and a person
// who reads "approved" in a report has to see whose approval it is.
func (f Facts) Counted() (Review, bool) {
	records := f.trustedReviews()
	if len(records) == 0 {
		return Review{}, false
	}
	return records[len(records)-1], true
}

// approvedCommit is the commit the last approval on record is about, and whether
// there is one. It is what the history of the change is asked about when the
// approval is not of the head: whether that commit is still in the history is what
// tells a stale approval from a rewritten one, and asking git is left to [Collect]
// so that this package has no repository inside it (docs/DESIGN.md §7h).
func (f Facts) approvedCommit() (string, bool) {
	records := f.trustedReviews()
	if len(records) == 0 {
		return "", false
	}
	record, is := Parse(records[len(records)-1].Body)
	if !is || record.Decision != Approved {
		return "", false
	}
	return record.Commit, true
}

// isReviewer is whether the account is one of the reviewers of the project: the
// ones the file of the project names, or the owner of the repository when it names
// none (docs/DESIGN.md §5, §7h).
func (f Facts) isReviewer(author string) bool {
	return slices.ContainsFunc(f.reviewers(), func(reviewer string) bool {
		return strings.EqualFold(reviewer, author)
	})
}

// isOwner is whether the account may accept the result of a task: the ones the file of
// the project names as its owners, or the owner of the repository when it names none
// (docs/DESIGN.md §5, §7h).
func (f Facts) isOwner(author string) bool {
	return slices.ContainsFunc(f.owners(), func(owner string) bool {
		return strings.EqualFold(owner, author)
	})
}

// reviewers are the accounts whose records count, with the default of §5 filled in:
// a project that names none has the owner of its repository, and nothing else.
func (f Facts) reviewers() []string {
	if len(f.Reviewers) > 0 {
		return f.Reviewers
	}
	if owner, _, of := strings.Cut(f.WantRepository, "/"); of {
		return []string{owner}
	}
	return nil
}

// owners are the accounts who may accept the result of a task, with the default of §5
// filled in: a project that names none has the owner of its repository, and nothing
// else. A project whose repository names no owner has nobody who may accept, and a task
// of it marked `owner-check` is then refused for a missing acceptance — a record of one
// is a record nobody could write.
func (f Facts) owners() []string {
	if len(f.Owners) > 0 {
		return f.Owners
	}
	if owner, _, of := strings.Cut(f.WantRepository, "/"); of {
		return []string{owner}
	}
	return nil
}

// sameCommit is whether two names of a commit are the same commit: the host and
// git write a SHA in the same letters, and a name of a commit that is written with
// other letters is the same commit.
func sameCommit(one, other string) bool {
	return one != "" && strings.EqualFold(one, other)
}

// authorsOf are the accounts that wrote the given records, each of them once, in
// the order they wrote. The records of a review and the records of an acceptance are
// told apart by the field their author is in, which is the only thing the refusal
// about them asks for.
func authorsOf[T any](records []T, author func(T) string) []string {
	var authors []string
	for _, record := range records {
		if name := author(record); !slices.Contains(authors, name) {
			authors = append(authors, name)
		}
	}
	return authors
}

// listed is a list of names as one line of a report reads them, and a list of
// nothing is said as nothing rather than left blank.
func listed(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// nameOf is what a report calls a source of a check, and a check with no app at
// all is a mark written through the API of statuses: it is said so rather than
// shown as a blank.
func nameOf(app string) string {
	if app == "" {
		return "the API of statuses"
	}
	return app
}

// firstOf is the first of a list, and an empty string when the list is empty.
func firstOf(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return names[0]
}
