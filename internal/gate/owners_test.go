package gate

import (
	"strings"
	"testing"
)

// The two subjects of a project apart from each other, as the host of the project names
// them: the orchestrator works as an account of the host of its own, and the owner of
// the project is a person with a login of their own. A record of a review is the word
// of the first and a decision of the owner is the word of the second, and the gate has
// to tell them apart by the account and not by what is written (docs/DESIGN.md §7h, §7i).
const orchestrator = "crewflow-orchestrator[bot]"

// TestAnAcceptanceOfTheOrchestratorIsNotTheOwners: a file outside the boundaries of the
// task is taken into their own hands by a person, and the record of it counts only when
// a person wrote it. The orchestrator of a project that works apart from the owner has
// rights to write under a change — it writes the record of a review there every time —
// and the gate still counts nothing it writes: an acceptance is the owner's own
// (docs/DESIGN.md §7h, §7i).
func TestAnAcceptanceOfTheOrchestratorIsNotTheOwners(t *testing.T) {
	facts := ready()
	facts.Files = append(facts.Files, "docs/DESIGN.md")
	// The orchestrator is the reviewer of the project in this mode — it is the one that
	// writes `REVIEW: APPROVED` — and it is not among the owners.
	facts.Reviewers = []string{orchestrator}
	facts.Reviews = []Review{approvedBy(orchestrator, second, false)}
	facts.Owners = []string{owner}
	facts.Accepted = []Acceptance{{
		Author: orchestrator,
		Commit: second,
		Reason: "the design of the gate is this task as well",
	}}

	if verdict := Evaluate(facts); verdict.Reason != OutOfScope {
		t.Errorf("Evaluate = %q (%s), want %q: an acceptance of the orchestrator is not the acceptance of the owner",
			verdict.Reason, verdict.Detail, OutOfScope)
	}
}

// TestAnAcceptanceOfTheOwnerIsTheOwners: the same change, the same record, written by
// the owner of the project — and the gate has nothing more to say about it. The two
// cases differ in one login and in nothing else (§7h, §7i).
func TestAnAcceptanceOfTheOwnerIsTheOwners(t *testing.T) {
	facts := ready()
	facts.Files = append(facts.Files, "docs/DESIGN.md")
	facts.Reviewers = []string{orchestrator}
	facts.Reviews = []Review{approvedBy(orchestrator, second, false)}
	facts.Owners = []string{owner}
	facts.Accepted = []Acceptance{{
		Author: owner,
		Commit: second,
		Reason: "the design of the gate is this task as well",
	}}

	if verdict := Evaluate(facts); !verdict.Ready {
		t.Errorf("Evaluate = %+v, want the acceptance of the owner to count", verdict)
	}
}

// TestTheApprovalOfTheOwnerCountsWhereTheProjectNamesHim: the record of a review counts
// from the reviewers of the project, whoever they are — a person is one of them where
// the file of the project says so, and the gate does not ask who is more powerful. The
// mode of §7i says which account the orchestrator is, and this list says whose word
// approves a change; a project that leaves the owner out of it has said that his
// approval is a comment (docs/DESIGN.md §7h, §7i).
func TestTheApprovalOfTheOwnerCountsWhereTheProjectNamesHim(t *testing.T) {
	cases := []struct {
		name      string
		reviewers []string
		want      bool
	}{
		{
			name:      "the reviewers of the project are the account of the orchestrator",
			reviewers: []string{orchestrator},
			want:      false,
		},
		{
			name:      "the project names the owner among the reviewers as well",
			reviewers: []string{orchestrator, owner},
			want:      true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			facts := ready()
			facts.Reviewers = tc.reviewers
			facts.Owners = []string{owner}
			facts.Reviews = []Review{approvedBy(owner, second, false)}

			verdict := Evaluate(facts)

			if tc.want && !verdict.Ready {
				t.Errorf("Evaluate = %+v, want the approval of the owner to count", verdict)
			}
			if !tc.want && verdict.Reason != ApprovalUntrusted {
				t.Errorf("Evaluate = %q (%s), want %q: the reviewers of the project are %v",
					verdict.Reason, verdict.Detail, ApprovalUntrusted, tc.reviewers)
			}
		})
	}
}

// TestTheOwnerIsTheDefaultOfBothLists: a project that names neither the reviewers nor
// the owners of it has the owner of its repository for both, which is what it has always
// had — and a record of anybody else, in either list, is a record of nobody (§5, §7h).
func TestTheOwnerIsTheDefaultOfBothLists(t *testing.T) {
	facts := ready()
	facts.Reviewers, facts.Owners = nil, nil
	facts.WantRepository = "naghuale/crewflow"
	facts.Reviews = []Review{approvedBy(owner, second, false)}
	facts.Files = []string{"README.md"}
	facts.Accepted = []Acceptance{{Author: owner, Commit: second, Reason: "this task is about it"}}

	if verdict := Evaluate(facts); !verdict.Ready {
		t.Errorf("Evaluate = %+v, want the owner of the repository to be the reviewer and the owner", verdict)
	}
	summary := Summarize(facts, Verdict{Ready: true})
	if len(summary.Owners) != 1 || summary.Owners[0] != owner {
		t.Errorf("the report of the change names the owners %v, want %q", summary.Owners, owner)
	}
}

// TestTheRefusalOfTheBoundariesNamesTheOwnersAndNotTheReviewers: a person is to know
// whose record lifts the refusal, and the record that lifts it is the one of the owner
// (docs/DESIGN.md §7h, §7i).
func TestTheRefusalOfTheBoundariesNamesTheOwnersAndNotTheReviewers(t *testing.T) {
	facts := ready()
	facts.Reviewers = []string{orchestrator}
	facts.Reviews = []Review{approvedBy(orchestrator, second, false)}
	facts.Owners = []string{owner}
	facts.Files = append(facts.Files, "docs/DESIGN.md")

	verdict := Evaluate(facts)

	if verdict.Reason != OutOfScope {
		t.Fatalf("Evaluate = %q (%s), want %q", verdict.Reason, verdict.Detail, OutOfScope)
	}
	for _, want := range []string{"SCOPE: ACCEPTED", owner} {
		if !strings.Contains(verdict.Detail, want) {
			t.Errorf("the refusal is %q, want it to mention %q", verdict.Detail, want)
		}
	}
	if strings.Contains(verdict.Detail, orchestrator) {
		t.Errorf("the refusal is %q, want the orchestrator not named in it: only the owner may take a file", verdict.Detail)
	}
}
