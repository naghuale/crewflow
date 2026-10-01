package gate

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/forge"
)

// The commits the cases of §7h are made of. They are the letters of the
// specification: an approval is of one of them, the head is another, and what git
// says about them is a fact of the case rather than of a repository.
const (
	first  = "1111111111111111111111111111111111111111"
	second = "2222222222222222222222222222222222222222"
	third  = "3333333333333333333333333333333333333333"
)

// The three accounts of a project in the mode of §7i as the host of the code keeps them:
// the owner by the number of the account, the App of the orchestrator and the App of the
// executor by the number of the App. The records of the first two count and the record of
// the third is a record of nobody, and it is the numbers — never the logins — that tell
// the three apart (docs/DESIGN.md §7h, §7i).
var (
	owner           = forge.Subject{Kind: forge.KindUser, ID: 93920024, Login: ownerLogin}
	orchestratorApp = forge.Subject{Kind: forge.KindApp, ID: 5140522, Login: orchestratorLogin}
	executorApp     = forge.Subject{Kind: forge.KindApp, ID: 5107052, Login: executorLogin}
)

// The names the host writes the three accounts under. They are what a report shows and
// what the file of the project names an account with, and they decide nothing: the cases
// that put a login in front of a decision are the ones that prove it (docs.DESIGN.md §7i).
const (
	ownerLogin        = "naghuale"
	orchestratorLogin = "crewflow-orchestrator[bot]"
	executorLogin     = "crewflow-executor[bot]"
)

// ownerCheckLabel is the label a task is marked with to have its result accepted by
// the owner before it may be merged, which is what §5 gives a project that says
// nothing about it.
const ownerCheckLabel = "owner-check"

// ready are the facts of a change about which there is nothing to say: it is open,
// of the project, meant for its default branch, approved by its owner on the head
// itself, green on every check the rules of the branch demand, inside the
// boundaries of its task, and the default branch is behind it. Every case of the
// table of §7h is this and one thing more, and a case that is not the whole of it
// says so by naming what it changed.
//
// The task carries no label, so nothing about it has to be accepted by the owner:
// the cases that are about the acceptance say so by marking it `owner-check`.
func ready() Facts {
	return Facts{
		Number:            7,
		URL:               "https://github.com/naghuale/crewflow/pull/7",
		Open:              true,
		Repository:        "naghuale/crewflow",
		WantRepository:    "naghuale/crewflow",
		TargetBranch:      "main",
		WantBranch:        "main",
		Head:              second,
		Task:              7,
		Reviewers:         []forge.Subject{owner},
		Reviews:           []Review{approvedBy(owner, second, false)},
		Owners:            []forge.Subject{owner},
		Executor:          executorApp,
		Boundaries:        []string{"internal/gate/**", "README.md"},
		Files:             []string{"internal/gate/gate.go", "README.md"},
		Required:          []forge.RequiredCheck{{Name: "test", App: "github-actions"}},
		Checks:            []forge.CheckRun{{Name: "test", State: forge.CheckSuccess, App: "github-actions", SHA: second}},
		AcceptanceLabel:   ownerCheckLabel,
		DefaultIsAncestor: true,
	}
}

// approvedBy is a record of a review by an account: a decision about a commit, and
// whether it has been edited since it was published.
func approvedBy(author forge.Subject, commit string, edited bool) Review {
	return Review{
		Author:    author,
		CreatedAt: time.Date(2026, time.September, 28, 12, 0, 0, 0, time.UTC),
		Edited:    edited,
		Body:      ApproveOf(commit, 7),
	}
}

// changesRequestedBy is a record that asks for changes, which stands over any
// approval written before it.
func changesRequestedBy(author forge.Subject, findings string) Review {
	return Review{
		Author:    author,
		CreatedAt: time.Date(2026, time.September, 28, 13, 0, 0, 0, time.UTC),
		Body:      ChangesOf(second, 7, findings),
	}
}

// acceptedBy is a record in which the owner of the project takes the result of the task
// as it stands: the head the record is written for.
func acceptedBy(author forge.Subject, commit string) OwnerAccept {
	return OwnerAccept{
		Author:    author,
		CreatedAt: time.Date(2026, time.September, 28, 15, 0, 0, 0, time.UTC),
		Commit:    commit,
	}
}

// A case of the table of §7h: a set of facts, the reason they are refused with, and
// the word the reason has to name for the cases whose whole point is what a person
// is told about it. The numbers of the cases are the numbers of the integration
// tests of §7h, so that a case that fails says which one of them it is.
type caseOfTable struct {
	it   string
	name string
	// given changes the facts of an otherwise ready change.
	given  func(*Facts)
	want   Reason
	ready  bool
	detail string
}

// tableOfTheSpecification is every case of §7h that is a question of the gate
// alone: a set of facts and the one reason they are refused with, and the last one
// is a change about which there is nothing to say.
func tableOfTheSpecification() []caseOfTable {
	return []caseOfTable{
		{
			it:   "IT-001",
			name: "approved A, head = B, and A is an ancestor of B",
			given: func(facts *Facts) {
				facts.Reviews = []Review{approvedBy(owner, first, false)}
				facts.ApprovedIsAncestor = true
			},
			want:   ApprovalStale,
			detail: first,
		},
		{
			it:   "IT-002",
			name: "approved A, the history was rewritten, head = C, and A is not in it",
			given: func(facts *Facts) {
				facts.Reviews = []Review{approvedBy(owner, first, false)}
				facts.ApprovedIsAncestor = false
			},
			want:   HistoryRewritten,
			detail: first,
		},
		{
			it:    "IT-003",
			name:  "no record of a review at all",
			given: func(facts *Facts) { facts.Reviews = nil },
			want:  ApprovalMissing,
		},
		{
			it:     "IT-004",
			name:   "REVIEW: APPROVED <head> written by the executor of the run",
			given:  func(facts *Facts) { facts.Reviews = []Review{approvedBy(executorApp, second, false)} },
			want:   ApprovalUntrusted,
			detail: executorLogin,
		},
		{
			it:     "IT-005",
			name:   "the approval of the owner was edited after it was published",
			given:  func(facts *Facts) { facts.Reviews = []Review{approvedBy(owner, second, true)} },
			want:   ApprovalEdited,
			detail: ownerLogin,
		},
		{
			it:   "IT-006",
			name: "approved the head, then changes requested: the last record is not an approval",
			given: func(facts *Facts) {
				facts.Reviews = []Review{approvedBy(owner, second, false), changesRequestedBy(owner, "R1: the reason")}
			},
			want: ApprovalMissing,
		},
		{
			it:   "IT-007",
			name: "approved the head, and the check is green for another commit",
			given: func(facts *Facts) {
				facts.Checks = []forge.CheckRun{{Name: "test", State: forge.CheckSuccess, App: "github-actions", SHA: first}}
			},
			want:   CISHAMismatch,
			detail: first,
		},
		{
			it:   "IT-008",
			name: "lint and test are required, and the head has only a green test",
			given: func(facts *Facts) {
				facts.Required = append(facts.Required, forge.RequiredCheck{Name: "lint", App: "github-actions"})
			},
			want:   RequiredCheckMissing,
			detail: "lint",
		},
		{
			it:   "IT-009",
			name: "the check has not finished",
			given: func(facts *Facts) {
				facts.Checks = []forge.CheckRun{{Name: "test", State: forge.CheckPending, App: "github-actions", SHA: second}}
			},
			want: RequiredCheckIncomplete,
		},
		{
			it:   "IT-010",
			name: "the check has failed",
			given: func(facts *Facts) {
				facts.Checks = []forge.CheckRun{{Name: "test", State: forge.CheckFailure, App: "github-actions", SHA: second}}
			},
			want: RequiredCheckFailed,
		},
		{
			it:   "IT-011",
			name: "the green mark was written through the API of statuses, not by Actions",
			given: func(facts *Facts) {
				facts.Checks = []forge.CheckRun{{Name: "test", State: forge.CheckSuccess, App: "", SHA: second}}
			},
			want:   RequiredCheckUntrusted,
			detail: "statuses",
		},
		{
			it:     "IT-012",
			name:   "the default branch has moved on since the branch was cut",
			given:  func(facts *Facts) { facts.DefaultIsAncestor = false },
			want:   NotFastForward,
			detail: "main",
		},
		{
			it:   "a change that conflicts with main: the conflict is named, not the checks",
			name: "the host says the change conflicts with its branch",
			given: func(facts *Facts) {
				facts.Conflicted = true
				facts.Required = nil
				facts.Checks = nil
			},
			want:   NotFastForward,
			detail: "conflicts",
		},
		{
			it:     "IT-014",
			name:   "a file outside the boundaries of the task is in the range base..head",
			given:  func(facts *Facts) { facts.Files = append(facts.Files, "docs/DESIGN.md") },
			want:   OutOfScope,
			detail: "docs/DESIGN.md",
		},
		{
			it:   "IT-015",
			name: "the same, with SCOPE: ACCEPTED <head> written by the owner",
			given: func(facts *Facts) {
				facts.Files = append(facts.Files, "docs/DESIGN.md")
				facts.Accepted = []Acceptance{{
					Author:    owner,
					CreatedAt: time.Date(2026, time.September, 28, 14, 0, 0, 0, time.UTC),
					Commit:    second,
					Reason:    "the design of the gate is this task as well",
				}}
			},
			ready: true,
		},
		{
			it:    "IT-016",
			name:  "the change request is closed",
			given: func(facts *Facts) { facts.Open = false },
			want:  PRNotOpen,
		},
		{
			it:    "IT-016",
			name:  "the change request is a draft",
			given: func(facts *Facts) { facts.Draft = true },
			want:  PRDraft,
		},
		{
			it:     "IT-017",
			name:   "the change request is meant for another branch",
			given:  func(facts *Facts) { facts.TargetBranch = "release" },
			want:   WrongTargetBranch,
			detail: "release",
		},
		{
			it:     "IT-017",
			name:   "the change request is of another repository",
			given:  func(facts *Facts) { facts.Repository = "someone/crewflow" },
			want:   WrongRepository,
			detail: "someone/crewflow",
		},
		{
			it:     "IT-018",
			name:   "the host did not give a certain answer",
			given:  func(facts *Facts) { facts.Unavailable = "read the checks of the commit: the API answered 502" },
			want:   ForgeUnavailable,
			detail: "502",
		},
		{
			it:     "the task of the change is not known, so nothing is known to be inside its boundaries",
			name:   "a change whose task crewflow could not find has no boundaries to check it against",
			given:  func(facts *Facts) { facts.Boundaries = nil },
			want:   OutOfScope,
			detail: "internal/gate/gate.go",
		},
		{
			it:    "everything is in order",
			name:  "an open change, approved on its head, green, inside the boundaries and behind nothing",
			given: func(*Facts) {},
			ready: true,
		},
		{
			it:   "IT-024",
			name: "the task is marked owner-check, and there is no record of an acceptance",
			given: func(facts *Facts) {
				facts.Labels = []string{ownerCheckLabel}
			},
			want:   OwnerAcceptanceMissing,
			detail: "ACCEPTED",
		},
		{
			it:   "IT-025",
			name: "the owner accepted another commit",
			given: func(facts *Facts) {
				facts.Labels = []string{ownerCheckLabel}
				facts.OwnerAccepts = []OwnerAccept{acceptedBy(owner, first)}
			},
			want:   OwnerAcceptanceMissing,
			detail: first,
		},
		{
			it:   "IT-026",
			name: "the owner accepted the head, and a commit was added after it",
			given: func(facts *Facts) {
				facts.Labels = []string{ownerCheckLabel}
				facts.OwnerAccepts = []OwnerAccept{acceptedBy(owner, second)}
				facts.Head = third
				facts.Reviews = []Review{approvedBy(owner, third, false)}
				facts.Checks = []forge.CheckRun{{Name: "test", State: forge.CheckSuccess, App: "github-actions", SHA: third}}
			},
			want:   OwnerAcceptanceMissing,
			detail: second,
		},
		{
			it:   "IT-027",
			name: "the acceptance was written by the executor of the run",
			given: func(facts *Facts) {
				facts.Labels = []string{ownerCheckLabel}
				facts.OwnerAccepts = []OwnerAccept{acceptedBy(executorApp, second)}
			},
			want:   OwnerAcceptanceUntrusted,
			detail: executorLogin,
		},
		{
			it:   "IT-028",
			name: "the acceptance of the owner was edited after it was published",
			given: func(facts *Facts) {
				facts.Labels = []string{ownerCheckLabel}
				facts.OwnerAccepts = []OwnerAccept{{
					Author: owner, CreatedAt: time.Now(), Edited: true, Commit: second,
				}}
			},
			want:   OwnerAcceptanceEdited,
			detail: ownerLogin,
		},
		{
			it:   "IT-029",
			name: "the owner accepted the head of the change himself",
			given: func(facts *Facts) {
				facts.Labels = []string{ownerCheckLabel}
				facts.OwnerAccepts = []OwnerAccept{acceptedBy(owner, second)}
			},
			ready: true,
		},
		{
			it:   "IT-029b",
			name: "a task without the label owner-check needs no acceptance at all",
			given: func(facts *Facts) {
				facts.Labels = []string{"risky"}
				facts.OwnerAccepts = nil
			},
			ready: true,
		},
	}
}

// TestEvaluate walks the table of docs/DESIGN.md §7h: a set of facts and the one
// reason they are refused with, and one case of a change about which there is
// nothing to say.
func TestEvaluate(t *testing.T) {
	for _, tc := range tableOfTheSpecification() {
		t.Run(tc.it+" "+tc.name, func(t *testing.T) {
			facts := ready()
			tc.given(&facts)

			verdict := Evaluate(facts)

			if verdict.Ready != tc.ready {
				t.Fatalf("Evaluate = %+v, want ready %v", verdict, tc.ready)
			}
			if tc.ready {
				if verdict.Reason != "" {
					t.Errorf("a ready verdict names the reason %q, want none", verdict.Reason)
				}
				return
			}
			if verdict.Reason != tc.want {
				t.Errorf("Evaluate = %q (%s), want %q", verdict.Reason, verdict.Detail, tc.want)
			}
			if verdict.Detail == "" {
				t.Errorf("the reason %q has no detail: a person has to be told what to do about it", verdict.Reason)
			}
			if tc.detail != "" && !strings.Contains(verdict.Detail, tc.detail) {
				t.Errorf("the detail %q does not mention %q", verdict.Detail, tc.detail)
			}
		})
	}
}

// TestEveryReasonIsJudged is the promise the table of §7h makes and the tests above
// keep: a reason the gate can give is a reason a test names. The two that the gate
// cannot give are the ones of the push of a merge: they are written by the merge of the
// cycle and not by [Evaluate], and the tests of package merge walk every reason of this
// table against a real git — a table case there and its meta-test that fails when a
// reason has none (docs/DESIGN.md §7h).
func TestEveryReasonIsJudged(t *testing.T) {
	judged := map[Reason]bool{}
	for _, reason := range Reasons {
		judged[reason] = false
	}
	for _, tc := range tableOfTheSpecification() {
		facts := ready()
		tc.given(&facts)
		if verdict := Evaluate(facts); !verdict.Ready {
			judged[verdict.Reason] = true
		}
	}
	for _, reason := range []Reason{PushRejected, VerifyMismatch} {
		delete(judged, reason)
	}
	for reason, covered := range judged {
		if !covered {
			t.Errorf("the reason %q is in the table of §7h and no case of Facts reaches it", reason)
		}
	}
}

// TestApartIsTheVerdictWithoutTheApproval is what an orchestrator is shown before it
// writes a real approval: a change whose CI is red, or whose files went out of the
// boundaries, cannot be approved by accident — and the reason it is refused with is
// that one, not "there is no approval" (docs/DESIGN.md §7h).
func TestApartIsTheVerdictWithoutTheApproval(t *testing.T) {
	t.Run("everything else is in order", func(t *testing.T) {
		facts := ready()
		facts.Reviews = nil

		verdict := Apart(facts)

		if !verdict.Ready {
			t.Errorf("Apart = %+v, want a change that may be approved", verdict)
		}
	})
	t.Run("the CI is red", func(t *testing.T) {
		facts := ready()
		facts.Reviews = nil
		facts.Checks = []forge.CheckRun{{Name: "test", State: forge.CheckFailure, App: "github-actions", SHA: second}}

		verdict := Apart(facts)

		if verdict.Reason != RequiredCheckFailed {
			t.Errorf("Apart = %q (%s), want %q", verdict.Reason, verdict.Detail, RequiredCheckFailed)
		}
	})
	t.Run("a file is outside the boundaries", func(t *testing.T) {
		facts := ready()
		facts.Reviews = nil
		facts.Files = append(facts.Files, "cmd/crewflow/main.go")

		verdict := Apart(facts)

		if verdict.Reason != OutOfScope {
			t.Errorf("Apart = %q (%s), want %q", verdict.Reason, verdict.Detail, OutOfScope)
		}
	})
	t.Run("the default branch has moved on", func(t *testing.T) {
		facts := ready()
		facts.Reviews = nil
		facts.DefaultIsAncestor = false

		verdict := Apart(facts)

		if verdict.Reason != NotFastForward {
			t.Errorf("Apart = %q (%s), want %q", verdict.Reason, verdict.Detail, NotFastForward)
		}
	})
}

// TestAnAcceptanceOnlyCountsForTheHead: a person takes a file outside the
// boundaries of a task into their own hands, and then the branch grows. What was
// accepted is what they read, and not what the change is now (docs/DESIGN.md §7h).
func TestAnAcceptanceOnlyCountsForTheHead(t *testing.T) {
	facts := ready()
	facts.Files = append(facts.Files, "docs/DESIGN.md")
	facts.Accepted = []Acceptance{
		{Author: owner, Commit: second, Reason: "the design is this task as well"},
		{Author: owner, Commit: first, Reason: "an earlier head"},
	}

	if verdict := Evaluate(facts); verdict.Reason != OutOfScope {
		t.Errorf("Evaluate = %q (%s), want %q: the acceptance is of another head", verdict.Reason, verdict.Detail, OutOfScope)
	}
}

// TestAnAcceptanceIsCountedLikeAnApproval: only of a reviewer, and only while it has
// not been edited, because an acceptance anybody can rewrite is not one (docs/DESIGN.md §7h).
func TestAnAcceptanceIsCountedLikeAnApproval(t *testing.T) {
	cases := []struct {
		name  string
		given Acceptance
		want  Reason
	}{
		{
			name: "the executor of the run may not accept anything",
			given: Acceptance{
				Author: executorApp, Commit: second,
			},
			want: OutOfScope,
		},
		{
			name: "an acceptance that was edited counts for nothing",
			given: Acceptance{
				Author: owner, Commit: second, Edited: true,
			},
			want: OutOfScope,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			facts := ready()
			facts.Files = append(facts.Files, "docs/DESIGN.md")
			facts.Accepted = []Acceptance{tc.given}

			if verdict := Evaluate(facts); verdict.Reason != tc.want {
				t.Errorf("Evaluate = %q (%s), want %q", verdict.Reason, verdict.Detail, tc.want)
			}
		})
	}
}

// TestTheOwnerAcceptsByList: the acceptance is told apart from the approval of §5, and
// both are counted from their own list of accounts — a record of an owner and a record of
// a reviewer are two different acts, and the account that writes one of them need not be
// among the other list. Every case here is about the numbers the host keeps the accounts
// under: the login in them is only what a report shows (docs/DESIGN.md §5, §7h, §7i).
func TestTheOwnerAcceptsByList(t *testing.T) {
	reviewer := forge.Subject{Kind: forge.KindUser, ID: 4242, Login: "reviewer"}
	// A person whose login is the owner of the project with one letter added: over a
	// host that names an App by its slug alone, the two are one string, and a record of
	// that person must not be a record of the owner (F-081, docs/DESIGN.md §7h).
	namedLikeTheOwner := forge.Subject{Kind: forge.KindUser, ID: 7, Login: "naghuale-e"}
	cases := []struct {
		name  string
		given func(*Facts)
		ready bool
		want  Reason
	}{
		{
			name: "the owner the file names takes the result in",
			given: func(facts *Facts) {
				facts.Owners = []forge.Subject{owner}
				facts.OwnerAccepts = []OwnerAccept{acceptedBy(owner, second)}
			},
			ready: true,
		},
		{
			name: "a project that names no owners has nobody who may accept",
			given: func(facts *Facts) {
				facts.Owners, facts.OwnerAccepts = nil, []OwnerAccept{acceptedBy(owner, second)}
			},
			want: OwnerAcceptanceUntrusted,
		},
		{
			name: "an account that is only named like the owner is not the owner",
			given: func(facts *Facts) {
				facts.Owners = []forge.Subject{owner}
				facts.OwnerAccepts = []OwnerAccept{acceptedBy(namedLikeTheOwner, second)}
			},
			want: OwnerAcceptanceUntrusted,
		},
		{
			name: "the owners the project names and nobody else",
			given: func(facts *Facts) {
				facts.Owners = []forge.Subject{owner, reviewer}
				facts.OwnerAccepts = []OwnerAccept{acceptedBy(reviewer, second)}
			},
			ready: true,
		},
		{
			name: "a reviewer who is no owner of the project accepts nothing",
			given: func(facts *Facts) {
				facts.Owners = []forge.Subject{owner}
				facts.OwnerAccepts = []OwnerAccept{acceptedBy(reviewer, second)}
			},
			want: OwnerAcceptanceUntrusted,
		},
		{
			name: "the app of the executor takes the result of nothing in",
			given: func(facts *Facts) {
				facts.Owners = []forge.Subject{owner, executorApp}
				facts.OwnerAccepts = []OwnerAccept{acceptedBy(executorApp, second)}
			},
			want: OwnerAcceptanceUntrusted,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			facts := ready()
			facts.Labels = []string{ownerCheckLabel}
			tc.given(&facts)

			verdict := Evaluate(facts)

			if verdict.Ready != tc.ready {
				t.Fatalf("Evaluate = %+v, want ready %v", verdict, tc.ready)
			}
			if !tc.ready && verdict.Reason != tc.want {
				t.Errorf("Evaluate = %q (%s), want %q", verdict.Reason, verdict.Detail, tc.want)
			}
		})
	}
}

// TestApartStandsTheAcceptanceIn is what the owner of the repository gets to see before
// a change is approved: the acceptance of a task marked `owner-check` is his own act
// and the orchestrator cannot write it, so a verdict that refused the approval for a
// missing acceptance would leave the task with no way to start at all
// (docs/DESIGN.md §7h).
func TestApartStandsTheAcceptanceIn(t *testing.T) {
	facts := ready()
	facts.Labels = []string{ownerCheckLabel}
	facts.Reviews, facts.OwnerAccepts = nil, nil

	if verdict := Apart(facts); !verdict.Ready {
		t.Errorf("Apart = %+v, want a change of a task that may be approved and then accepted", verdict)
	}
}

// TestTheReviewersAreTheAccountsWhoseRecordsCount is the other list of §5, and the same
// rule: a record counts when the account behind it is one the file of the project names,
// and an account nobody named is a record of nobody however its login reads (docs/DESIGN.md
// §5, §7h, §7i).
func TestTheReviewersAreTheAccountsWhoseRecordsCount(t *testing.T) {
	other := forge.Subject{Kind: forge.KindUser, ID: 4242, Login: "reviewer"}
	// The account of an App under the login of another App, and a person whose login is
	// the slug of an App: over GraphQL they are one string each, and neither of them is
	// the App of the orchestrator of this project (F-081, docs/DESIGN.md §7h).
	appOfTheExecutor := forge.Subject{Kind: forge.KindApp, ID: 5107052, Login: orchestratorLogin}
	personNamedLikeTheApp := forge.Subject{Kind: forge.KindUser, ID: 339150227, Login: "crewflow-orchestrator"}
	cases := []struct {
		name   string
		who    forge.Subject
		reason Reason
	}{
		{name: "the app of the orchestrator approves", who: orchestratorApp},
		{name: "the owner the file names approves", who: owner},
		{name: "another reviewer the file names approves", who: other},
		{name: "the app of the executor approves nothing", who: executorApp, reason: ApprovalUntrusted},
		{name: "an app under the login of another app approves nothing", who: appOfTheExecutor, reason: ApprovalUntrusted},
		{name: "a person named like the app of the orchestrator approves nothing", who: personNamedLikeTheApp, reason: ApprovalUntrusted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			facts := ready()
			facts.Reviewers = []forge.Subject{owner, orchestratorApp, other}
			facts.Reviews = []Review{approvedBy(tc.who, second, false)}

			verdict := Evaluate(facts)

			if tc.reason == "" {
				if !verdict.Ready {
					t.Fatalf("Evaluate = %+v, want the record of this account to count", verdict)
				}
				return
			}
			if verdict.Reason != tc.reason {
				t.Errorf("Evaluate = %q (%s), want %q", verdict.Reason, verdict.Detail, tc.reason)
			}
		})
	}
}

// TestTheRecordOfAnAppIsCountedByItsNumberAndNotByItsName is the case of F-081 turned
// around: the App of the orchestrator is renamed, its account with it, and the record it
// wrote under its new name is the very record the gate counted before the rename — while
// a record of an App of the same host under that very login counts for nothing
// (docs/DESIGN.md §7h, §7i).
func TestTheRecordOfAnAppIsCountedByItsNumberAndNotByItsName(t *testing.T) {
	renamed := forge.Subject{Kind: forge.KindApp, ID: orchestratorApp.ID, Login: "crewflow-orchestrator-renamed[bot]"}
	facts := ready()
	facts.Reviewers = []forge.Subject{orchestratorApp}
	facts.Reviews = []Review{approvedBy(renamed, second, false)}

	if verdict := Evaluate(facts); !verdict.Ready {
		t.Errorf("Evaluate = %+v, want the record of the app under its new name to count", verdict)
	}

	facts.Reviews = []Review{approvedBy(executorApp, second, false)}
	if verdict := Evaluate(facts); verdict.Reason != ApprovalUntrusted {
		t.Errorf("Evaluate = %q (%s), want %q: the record of another app is a record of that app",
			verdict.Reason, verdict.Detail, ApprovalUntrusted)
	}
}

// TestAConflictedChangeIsNamedAsAConflict is the case of a change request that
// cannot be merged into its branch: GitHub does not run its checks at all, and a
// gate that reported `required-check-missing` would be telling a person to wait for
// something that is never going to come (docs/DESIGN.md §7h, PR #19 of 29.09.2026).
func TestAConflictedChangeIsNamedAsAConflict(t *testing.T) {
	facts := ready()
	facts.Conflicted = true
	facts.Required = nil
	facts.Checks = nil

	verdict := Evaluate(facts)

	if verdict.Reason != NotFastForward {
		t.Errorf("Evaluate = %q (%s), want %q", verdict.Reason, verdict.Detail, NotFastForward)
	}
	for _, in := range []string{"conflicts", "rebase"} {
		if !strings.Contains(verdict.Detail, in) {
			t.Errorf("the detail %q does not mention %q", verdict.Detail, in)
		}
	}
}

// TestARefusalNeverLeaksTheNextReason: a verdict names one reason, and the one
// after it is not in it. A person who fixes what is named comes back to a verdict
// that names the next thing, which is the only way a gate can be worked with.
func TestARefusalNeverLeaksTheNextReason(t *testing.T) {
	facts := ready()
	facts.Draft = true
	facts.Reviews = nil
	facts.Checks = []forge.CheckRun{{Name: "test", State: forge.CheckFailure, App: "github-actions", SHA: second}}
	facts.Files = append(facts.Files, "docs/DESIGN.md")

	verdict := Evaluate(facts)

	if verdict.Reason != PRDraft {
		t.Errorf("Evaluate = %q (%s), want %q", verdict.Reason, verdict.Detail, PRDraft)
	}
}

// TestASummaryOfTheAcceptanceOfTheOwner is what the owner of the repository reads to
// know whether the change is waiting for him: the task is marked as one that waits, and
// either nobody has taken the result in or the record that does is there to be read. A
// task without the label says nothing about an acceptance, and its report is the report
// it always was (docs/DESIGN.md §7f, §7h).
func TestASummaryOfTheAcceptanceOfTheOwner(t *testing.T) {
	cases := []struct {
		name  string
		given func(*Facts)
		want  string
	}{
		{
			name:  "the task is marked and nobody has accepted the result",
			given: func(facts *Facts) { facts.Labels = []string{ownerCheckLabel} },
			want:  "  owner acceptance: the task is marked `owner-check`, and there is no record of it under the change\n",
		},
		{
			name: "the owner accepted the head",
			given: func(facts *Facts) {
				facts.Labels = []string{ownerCheckLabel}
				facts.OwnerAccepts = []OwnerAccept{acceptedBy(owner, second)}
			},
			want: "  owner acceptance: ACCEPTED " + second[:8] + " by " + ownerLogin + " at 2026-09-28T15:00:00Z\n",
		},
		{
			name: "the record is of an earlier head",
			given: func(facts *Facts) {
				facts.Labels = []string{ownerCheckLabel}
				facts.OwnerAccepts = []OwnerAccept{acceptedBy(owner, first)}
			},
			want: "and it does not count: of another commit than the head\n",
		},
		{
			name: "the record was edited after it was published",
			given: func(facts *Facts) {
				facts.Labels = []string{ownerCheckLabel}
				facts.OwnerAccepts = []OwnerAccept{{
					Author: owner, CreatedAt: time.Now(), Edited: true, Commit: second,
				}}
			},
			want: "and it does not count: edited after it was published\n",
		},
		{
			name: "the record is of the executor of the run",
			given: func(facts *Facts) {
				facts.Labels = []string{ownerCheckLabel}
				facts.OwnerAccepts = []OwnerAccept{acceptedBy(executorApp, second)}
			},
			want: "and it does not count: not one of the owners of the project\n",
		},
		{
			name:  "a task nobody has to accept says nothing about it",
			given: func(facts *Facts) { facts.Labels = []string{"risky"} },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			facts := ready()
			tc.given(&facts)

			var out bytes.Buffer
			Summarize(facts, Evaluate(facts)).Write(&out)

			line := ownerAcceptLine(out.String())
			switch {
			case tc.want == "" && line != "":
				t.Errorf("the report holds the line %q, want none: the task waits for no one", line)
			case tc.want != "" && !strings.Contains(line, tc.want):
				t.Errorf("the line %q does not hold %q", line, tc.want)
			}
		})
	}
}

// ownerAcceptLine is the line of a report about the acceptance of the result of the
// task, and an empty string where the report holds none.
func ownerAcceptLine(report string) string {
	for line := range strings.Lines(report) {
		if strings.HasPrefix(line, "  owner acceptance:") {
			return line
		}
	}
	return ""
}

// TestASummaryOfNothingGathered: when not even the change could be read, a report of
// empty lines above a refusal says less than the refusal itself, and a person has to
// see the reason rather than the shape of a report about nothing (docs/DESIGN.md §7h).
func TestASummaryOfNothingGathered(t *testing.T) {
	facts := Facts{
		WantRepository: "naghuale/crewflow",
		WantBranch:     "main",
		Unavailable:    "forge.kind: this project has no host of its code",
	}

	summary := Summarize(facts, Evaluate(facts))
	var out bytes.Buffer
	summary.Write(&out)

	if got := out.String(); got != "verdict: may not be merged, forge-unavailable\n  forge.kind: this project has no host of its code\n" {
		t.Errorf("the report is %q, want the reason and nothing else", got)
	}
}
