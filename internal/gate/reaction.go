package gate

import (
	"time"
)

// Reaction is what the host of a change says about it to a caller that is not the
// gate: whether a record of a review of the head stands, whether the owner took the
// result in, and — where neither stands — the refusal of the gate with its reason and
// its words.
//
// It is the answer of [Evaluate] to the two of its checks that read records, and it is
// worked out of the same functions: a queue of attention (§6a) that counted the records
// under a change its own way would be the second opinion of §7h about the same change,
// and two opinions about one record is one too many (docs.DESIGN.md §6a, §7h).
type Reaction struct {
	// Approved says that a record of a review of the head of the change stands under
	// it, written by an account that may approve and not edited since it was
	// published — the same three ways a merge counts it (§7h).
	Approved bool
	// Accepted says that the owner of the project took the result of the task in at
	// the head of the change. It is true for a task the project does not ask the owner
	// to accept: nobody waits for an acceptance there, and a queue that put such a
	// task off for an acceptance nobody owes it would be inventing a wait (§6a).
	Accepted bool
	// Refusal is what the gate says where the records do not stand: an approval that
	// was edited after it was published approves nothing, and the queue names the
	// reason of §7h rather than a sentence of its own (§7h).
	Refusal Verdict
	// Last is when the last record that counts was written, and it is what a wait of
	// a person is counted from. It is nothing where no record counts, because a wait
	// nobody began is not counted from the moment the queue looked (§6a).
	Last time.Time
}

// React is what the records under a change say about it, as the gate counts them and
// nobody counts them a second way. It is what the queue of attention of §6a asks about
// a task whose run opened a change request: whether somebody has looked at the result
// of the run, and whether the owner has taken it in.
//
// It reads nothing but the records — no git and no checks, which are questions of a
// merge and not of a wait — and it says so where a record cannot be counted: the same
// refusal [Evaluate] would give, with its reason and its words.
func React(f Facts) Reaction {
	r := Reaction{Accepted: true}
	if verdict, no := f.approval(); no {
		// The gate tells a stale approval from a rewritten one by asking git whether
		// the approved commit is in the history of the head. A queue asks no git, and
		// naming a rewritten history where nobody looked for one is a claim about a
		// repository crewflow never read — so the reason of the refusal is the one
		// both agree on: commits were added after the approval, and nobody approved
		// them (§6a, §7h).
		if verdict.Reason == HistoryRewritten {
			verdict = refused(ApprovalStale, verdict.Detail)
		}
		r.Refusal = verdict
	} else {
		r.Approved = true
	}
	if verdict, no := f.ownerAcceptance(); no {
		r.Accepted, r.Refusal = false, verdict
	}
	r.Last = f.lastCounted()
	return r
}

// lastCounted is when the last record under the change that counts was written: the
// last review of an account that may approve, or the last acceptance of an owner,
// whichever is later. Both are counted exactly as a merge counts them, and a record
// that was edited after it was published counts for nothing here as it does there
// (docs/DESIGN.md §7h).
func (f Facts) lastCounted() time.Time {
	var last time.Time
	if review, is := f.Counted(); is {
		last = review.CreatedAt
	}
	for _, record := range f.ownerAcceptsOfOwners() {
		if !record.Edited && record.CreatedAt.After(last) {
			last = record.CreatedAt
		}
	}
	return last
}