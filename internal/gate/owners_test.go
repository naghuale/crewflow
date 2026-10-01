package gate

import (
	"strings"
	"testing"

	"github.com/naghuale/crewflow/internal/forge"
)

// The three accounts of a project in the mode of §7i, and the fourth: a person whose
// login is the slug of the App of the orchestrator. Over GraphQL the two are one string,
// because an App is named there by its slug alone, and a person may have that slug as a
// login; the host keeps each of them under its own number, and the gate counts records by
// those numbers (F-081, 01.10.2026; docs/DESIGN.md §7h, §7i).
var (
	// namedLikeTheApp is a person whose login is the slug of the App of the
	// orchestrator. The file of a project may name that person among its owners, and a
	// record of that person is not a record of the App — the one case the whole of this
	// change is about, from both sides.
	namedLikeTheApp = forge.Subject{Kind: forge.KindUser, ID: 339150227, Login: "crewflow-orchestrator"}
)

// TestARecordIsCountedByTheAccountAndNotByTheLogin: the account of the orchestrator is
// the one whose record approves, and a person whose login is the slug of its App is not
// it. The two lists of the project are lists of accounts now, so what arrives with a
// record is the account the host keeps it under — and this is the pair that says so, in
// both lists: the record of the App approves, the record of the person with its slug does
// not, and no record of the App is ever a decision of the owner (docs/DESIGN.md §7h, §7i).
func TestARecordIsCountedByTheAccountAndNotByTheLogin(t *testing.T) {
	cases := []struct {
		name string
		who  forge.Subject
		// wants is whether the record of this account counts in each of the two lists:
		// as an approval of the reviewers, and as a decision of the owners.
		wants struct{ review, owner bool }
	}{
		{
			name:  "the account of the app of the orchestrator",
			who:   orchestratorApp,
			wants: struct{ review, owner bool }{review: true, owner: false},
		},
		{
			name:  "a person with the slug of that app as a login",
			who:   namedLikeTheApp,
			wants: struct{ review, owner bool }{review: false, owner: true},
		},
	}
	for _, tc := range cases {
		t.Run("the record of a review: "+tc.name, func(t *testing.T) {
			facts := ready()
			facts.Reviewers = []forge.Subject{orchestratorApp}
			facts.Reviews = []Review{approvedBy(tc.who, second, false)}

			verdict := Evaluate(facts)

			if tc.wants.review && !verdict.Ready {
				t.Errorf("Evaluate = %+v, want the record to count", verdict)
			}
			if !tc.wants.review && verdict.Reason != ApprovalUntrusted {
				t.Errorf("Evaluate = %q (%s), want %q: the reviewers of the project are %v",
					verdict.Reason, verdict.Detail, ApprovalUntrusted, facts.Reviewers)
			}
		})
		t.Run("the record of an owner: "+tc.name, func(t *testing.T) {
			facts := ready()
			facts.Reviewers = []forge.Subject{orchestratorApp}
			facts.Reviews = []Review{approvedBy(orchestratorApp, second, false)}
			// The owner of this project is a person whose login is the slug of the App:
			// an account the file of a project names, and not a judgement about what
			// stands behind that name.
			facts.Owners = []forge.Subject{namedLikeTheApp}
			facts.Labels = []string{ownerCheckLabel}
			facts.OwnerAccepts = []OwnerAccept{acceptedBy(tc.who, second)}

			verdict := Evaluate(facts)

			if tc.wants.owner && !verdict.Ready {
				t.Errorf("Evaluate = %+v, want the record of the owner to count", verdict)
			}
			if !tc.wants.owner && verdict.Reason != OwnerAcceptanceUntrusted {
				t.Errorf("Evaluate = %q (%s), want %q: the owners of the project are %v",
					verdict.Reason, verdict.Detail, OwnerAcceptanceUntrusted, facts.Owners)
			}
		})
	}
}

// TestARecordIsNotCountedWhenTheLoginIsTheSameAndTheAccountIsNot is the refusal in the
// other direction: the login is the name the host writes every account of an App under, so
// two Apps of one host carry one login in their records, and only the number of the App
// tells them apart. A record of the App of the executor under the login of the App of the
// orchestrator is a record of the executor and approves nothing (docs/DESIGN.md §7h, §7i).
func TestARecordIsNotCountedWhenTheLoginIsTheSameAndTheAccountIsNot(t *testing.T) {
	executorNamedLikeTheOrchestrator := forge.Subject{
		Kind: forge.KindApp, ID: executorApp.ID, Login: orchestratorLogin,
	}
	facts := ready()
	facts.Reviewers = []forge.Subject{orchestratorApp}
	facts.Reviews = []Review{approvedBy(executorNamedLikeTheOrchestrator, second, false)}

	if verdict := Evaluate(facts); verdict.Reason != ApprovalUntrusted {
		t.Errorf("Evaluate = %q (%s), want %q: the record of another app is a record of that app",
			verdict.Reason, verdict.Detail, ApprovalUntrusted)
	}
}

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
	facts.Reviewers = []forge.Subject{orchestratorApp}
	facts.Reviews = []Review{approvedBy(orchestratorApp, second, false)}
	facts.Owners = []forge.Subject{owner}
	facts.Accepted = []Acceptance{{
		Author: orchestratorApp,
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
// cases differ in one account and in nothing else (§7h, §7i).
func TestAnAcceptanceOfTheOwnerIsTheOwners(t *testing.T) {
	facts := ready()
	facts.Files = append(facts.Files, "docs/DESIGN.md")
	facts.Reviewers = []forge.Subject{orchestratorApp}
	facts.Reviews = []Review{approvedBy(orchestratorApp, second, false)}
	facts.Owners = []forge.Subject{owner}
	facts.Accepted = []Acceptance{{
		Author: owner,
		Commit: second,
		Reason: "the design of the gate is this task as well",
	}}

	if verdict := Evaluate(facts); !verdict.Ready {
		t.Errorf("Evaluate = %+v, want the acceptance of the owner to count", verdict)
	}
}

// TestTheApprovalOfTheOwnerCountsWhereTheProjectNamesIt: the record of a review counts
// from the reviewers of the project, whoever they are — a person is one of them where
// the file of the project says so, and the gate does not ask who is more powerful. The
// mode of §7i says which account the orchestrator is, and this list says whose word
// approves a change; a project that leaves the owner out of it has said that his
// approval is a comment (docs/DESIGN.md §7h, §7i).
func TestTheApprovalOfTheOwnerCountsWhereTheProjectNamesIt(t *testing.T) {
	cases := []struct {
		name      string
		reviewers []forge.Subject
		want      bool
	}{
		{
			name:      "the reviewers of the project are the account of the orchestrator",
			reviewers: []forge.Subject{orchestratorApp},
			want:      false,
		},
		{
			name:      "the project names the owner among the reviewers as well",
			reviewers: []forge.Subject{orchestratorApp, owner},
			want:      true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			facts := ready()
			facts.Reviewers = tc.reviewers
			facts.Owners = []forge.Subject{owner}
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

// TestTheReportNamesTheOwnersItHas: the report of a change shows the accounts of both
// lists as the host writes them, and only where they differ — a project that has set
// nobody apart has one list, and a report that said it twice would be a report about a
// distinction the project does not make (docs/DESIGN.md §7h, §7i).
func TestTheReportNamesTheOwnersItHas(t *testing.T) {
	facts := ready()
	facts.Reviewers = []forge.Subject{orchestratorApp}
	facts.Reviews = []Review{approvedBy(orchestratorApp, second, false)}
	facts.Owners = []forge.Subject{owner}
	facts.Files = []string{"README.md"}
	facts.Accepted = []Acceptance{{Author: owner, Commit: second, Reason: "this task is about it"}}

	summary := Summarize(facts, Verdict{Ready: true})
	if len(summary.Owners) != 1 || summary.Owners[0] != ownerLogin {
		t.Errorf("the report of the change names the owners %v, want %q", summary.Owners, ownerLogin)
	}
	if len(summary.Reviewers) != 1 || summary.Reviewers[0] != orchestratorLogin {
		t.Errorf("the report of the change names the reviewers %v, want %q",
			summary.Reviewers, orchestratorLogin)
	}
}

// TestTheRefusalOfTheBoundariesNamesTheOwnersAndNotTheReviewers: a person is to know
// whose record lifts the refusal, and the record that lifts it is the one of the owner
// (docs.DESIGN.md §7h, §7i).
func TestTheRefusalOfTheBoundariesNamesTheOwnersAndNotTheReviewers(t *testing.T) {
	facts := ready()
	facts.Reviewers = []forge.Subject{orchestratorApp}
	facts.Reviews = []Review{approvedBy(orchestratorApp, second, false)}
	facts.Owners = []forge.Subject{owner}
	facts.Files = append(facts.Files, "docs/DESIGN.md")

	verdict := Evaluate(facts)

	if verdict.Reason != OutOfScope {
		t.Fatalf("Evaluate = %q (%s), want %q", verdict.Reason, verdict.Detail, OutOfScope)
	}
	for _, want := range []string{"SCOPE: ACCEPTED", ownerLogin} {
		if !strings.Contains(verdict.Detail, want) {
			t.Errorf("the refusal is %q, want it to mention %q", verdict.Detail, want)
		}
	}
	if strings.Contains(verdict.Detail, orchestratorLogin) {
		t.Errorf("the refusal is %q, want the orchestrator not named in it: only the owner may take a file",
			verdict.Detail)
	}
}
