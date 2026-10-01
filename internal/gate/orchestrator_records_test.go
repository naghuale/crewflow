package gate

import (
	"strings"
	"testing"

	"github.com/naghuale/crewflow/internal/forge"
)

// The powers of the three subjects of a project, as the host names the accounts of
// them, in the eight cases the owner of the project named on 01.10 as the proof of this
// task (docs/DESIGN.md §7i, §7k):
//
//	OR-001 an approval of the executor is not an approval
//	OR-002 an acceptance of the executor is not an acceptance
//	OR-003 a record of a review of the orchestrator is an approval in the separate mode
//	OR-004 an acceptance of the owner is an acceptance
//	OR-005 the separate mode with the login of the owner as the login of the orchestrator
//	       is refused by the load of the file (`internal/config`, OR-005 there)
//	OR-006 neither `ACCEPTED` nor `SCOPE: ACCEPTED` of the orchestrator is the decision
//	       of the owner
//	OR-007 the shared mode with the owners among the reviewers is not an error, and
//	       `doctor` shows the debt of trust it is (`internal/doctor`)
//	OR-008 the separate mode with the owners among the reviewers is refused by the load
//	       of the file (`internal/config`, OR-008 there)
//
// The two cases of the file are here and not in this package because a rule about a file
// of a project is the business of the package that reads files, and because a gate that
// counted the record of the owner from a file that says the orchestrator is the owner
// would be counting it exactly as the owner wrote it — the mistake those two refusals
// exist to stop.

// apart is the facts of a change of a project whose orchestrator works under an account
// of the host of its own: the account of the orchestrator is the one whose record of a
// review counts, the owner of the repository is the one whose records of an acceptance
// count, and the App of the executor is in neither list. A change with nothing else to
// say about it is approved by the orchestrator, which is what every case below changes
// one record of (docs/DESIGN.md §7h, §7i).
func apart() Facts {
	facts := ready()
	facts.Reviewers = []forge.Subject{orchestratorApp}
	facts.Reviews = []Review{approvedBy(orchestratorApp, second, false)}
	facts.Owners = []forge.Subject{owner}
	return facts
}

// TestOR001TheApprovalOfTheExecutorIsNotAnApproval: a record of a review written by the
// App of the executor is not an approval, whatever it says — the App of a run has rights
// to write under a change, and the gate counts no record of it as the word of a person
// who looked at the result (docs/DESIGN.md §7h, §7i).
func TestOR001TheApprovalOfTheExecutorIsNotAnApproval(t *testing.T) {
	facts := apart()
	facts.Reviews = []Review{approvedBy(executorApp, second, false)}

	verdict := Evaluate(facts)

	if verdict.Reason != ApprovalUntrusted {
		t.Errorf("Evaluate = %q (%s), want %q: the App of the executor writes a record and the gate counts no record of it as an approval",
			verdict.Reason, verdict.Detail, ApprovalUntrusted)
	}
	if !strings.Contains(verdict.Detail, executorLogin) {
		t.Errorf("Evaluate = %q, want the refusal to name the author of the record it does not count", verdict.Detail)
	}
}

// TestOR002TheAcceptanceOfTheExecutorIsNotAnAcceptance: the same App, and the record in
// which the owner of the project takes the result of a task in. A task marked
// `owner-check` waits for that record, and the record of the App of a run is not it: it
// is a comment under the change, and the gate names the fact that nobody has accepted
// the result (docs/DESIGN.md §7f, §7i).
func TestOR002TheAcceptanceOfTheExecutorIsNotAnAcceptance(t *testing.T) {
	facts := apart()
	facts.Labels = []string{ownerCheckLabel}
	facts.OwnerAccepts = []OwnerAccept{{Author: executorApp, Commit: second}}

	verdict := Evaluate(facts)

	if verdict.Reason != OwnerAcceptanceUntrusted {
		t.Errorf("Evaluate = %q (%s), want %q: the only record of an acceptance is of the App of the executor",
			verdict.Reason, verdict.Detail, OwnerAcceptanceUntrusted)
	}
	if !strings.Contains(verdict.Detail, executorLogin) {
		t.Errorf("Evaluate = %q, want the refusal to name the author of the record it does not count", verdict.Detail)
	}
}

// TestOR003TheRecordOfTheOrchestratorIsAnApprovalInTheSeparateMode: in the mode of an
// account of its own the record of a review of the orchestrator is the approval of the
// change — it is the one whose record counts, the file of the project leaves the list
// empty on purpose, and the gate does not ask how powerful that account is (§7h, §7i).
func TestOR003TheRecordOfTheOrchestratorIsAnApprovalInTheSeparateMode(t *testing.T) {
	facts := apart()
	facts.Files = []string{"internal/gate/gate.go"}

	if verdict := Evaluate(facts); !verdict.Ready {
		t.Errorf("Evaluate = %+v, want the record of the orchestrator to be the approval of the change", verdict)
	}
}

// TestOR004TheAcceptanceOfTheOwnerIsAnAcceptance: the same change and the same task
// marked `owner-check`, and the record written by the owner of the project under his own
// account — the gate has nothing more to say about it, and says whose record it was
// (§7f, §7h, §7i).
func TestOR004TheAcceptanceOfTheOwnerIsAnAcceptance(t *testing.T) {
	facts := apart()
	facts.Labels = []string{ownerCheckLabel}
	facts.OwnerAccepts = []OwnerAccept{{Author: owner, Commit: second}}

	verdict := Evaluate(facts)
	if !verdict.Ready {
		t.Errorf("Evaluate = %+v, want the acceptance of the owner to be the acceptance of the result", verdict)
	}
	summary := Summarize(facts, verdict)
	if summary.OwnerAccept == nil || summary.OwnerAccept.Author != ownerLogin {
		t.Errorf("the report of the change holds the acceptance %+v, want it to say whose record took the task in", summary.OwnerAccept)
	}
	if !summary.AcceptanceRequired || summary.AcceptanceLabel != ownerCheckLabel {
		t.Errorf("the report holds the requirement %v under the label %q, want the task to wait for the owner",
			summary.AcceptanceRequired, summary.AcceptanceLabel)
	}
}

// TestOR006NeitherRecordOfTheOrchestratorIsTheDecisionOfTheOwner: the orchestrator has
// the rights to write both records under a change — it closes the task behind the change
// that went in — and neither of them is the decision of the owner: an acceptance the
// orchestrator writes is a comment whatever it says, and the file outside the boundaries
// of the task stays refused until a person takes it into their own hands (§7h, §7i).
func TestOR006NeitherRecordOfTheOrchestratorIsTheDecisionOfTheOwner(t *testing.T) {
	t.Run("SCOPE: ACCEPTED", func(t *testing.T) {
		facts := apart()
		facts.Files = append(facts.Files, "docs/DESIGN.md")
		facts.Accepted = []Acceptance{{
			Author: orchestratorApp,
			Commit: second,
			Reason: "the design of the gate is this task as well",
		}}

		verdict := Evaluate(facts)
		if verdict.Reason != OutOfScope {
			t.Errorf("Evaluate = %q (%s), want %q: the file stays outside the boundaries until the owner says so",
				verdict.Reason, verdict.Detail, OutOfScope)
		}
		if !strings.Contains(verdict.Detail, ownerLogin) {
			t.Errorf("Evaluate = %q, want the refusal to name the owner whose record lifts it", verdict.Detail)
		}
	})
	t.Run("ACCEPTED", func(t *testing.T) {
		facts := apart()
		facts.Labels = []string{ownerCheckLabel}
		facts.OwnerAccepts = []OwnerAccept{{Author: orchestratorApp, Commit: second}}

		if verdict := Evaluate(facts); verdict.Reason != OwnerAcceptanceUntrusted {
			t.Errorf("Evaluate = %q (%s), want %q: the acceptance of the orchestrator is not the acceptance of the owner",
				verdict.Reason, verdict.Detail, OwnerAcceptanceUntrusted)
		}
	})
}
