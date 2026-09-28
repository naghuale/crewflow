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

// The owners of the project and of the App that works under it, and the executor
// of a run: the records of the first two count, the record of the third is a record
// of nobody (docs/DESIGN.md §7h).
const (
	owner    = "naghuale"
	executor = "crewflow-executor[bot]"
)

// ready are the facts of a change about which there is nothing to say: it is open,
// of the project, meant for its default branch, approved by its owner on the head
// itself, green on every check the rules of the branch demand, inside the
// boundaries of its task, and the default branch is behind it. Every case of the
// table of §7h is this and one thing more, and a case that is not the whole of it
// says so by naming what it changed.
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
		Reviewers:         []string{owner},
		Reviews:           []Review{approvedBy(owner, second, false)},
		Boundaries:        []string{"internal/gate/**", "README.md"},
		Files:             []string{"internal/gate/gate.go", "README.md"},
		Required:          []forge.RequiredCheck{{Name: "test", App: "github-actions"}},
		Checks:            []forge.CheckRun{{Name: "test", State: forge.CheckSuccess, App: "github-actions", SHA: second}},
		DefaultIsAncestor: true,
	}
}

// approvedBy is a record of a review by an account: a decision about a commit, and
// whether it has been edited since it was published.
func approvedBy(author, commit string, edited bool) Review {
	return Review{
		Author:    author,
		CreatedAt: time.Date(2026, time.September, 28, 12, 0, 0, 0, time.UTC),
		Edited:    edited,
		Body:      ApproveOf(commit, 7),
	}
}

// changesRequestedBy is a record that asks for changes, which stands over any
// approval written before it.
func changesRequestedBy(author, findings string) Review {
	return Review{
		Author:    author,
		CreatedAt: time.Date(2026, time.September, 28, 13, 0, 0, 0, time.UTC),
		Body:      ChangesOf(second, 7, findings),
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
			given:  func(facts *Facts) { facts.Reviews = []Review{approvedBy(executor, second, false)} },
			want:   ApprovalUntrusted,
			detail: executor,
		},
		{
			it:     "IT-005",
			name:   "the approval of the owner was edited after it was published",
			given:  func(facts *Facts) { facts.Reviews = []Review{approvedBy(owner, second, true)} },
			want:   ApprovalEdited,
			detail: owner,
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
// cannot give are the ones of the push of a merge, which is the next step of the
// cycle and is written by the merge and not by [Evaluate] (docs/DESIGN.md §7h).
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
				Author: executor, Commit: second,
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

// TestTheOwnerReviewsByDefault is the default of §5: a project that names no
// reviewers has the owner of its repository, and nothing else — an account that
// happens to be named like it is not the same account as one that is not named at
// all.
func TestTheOwnerReviewsByDefault(t *testing.T) {
	facts := ready()
	facts.Reviewers = nil
	facts.WantRepository = "naghuale/crewflow"
	facts.Reviews = []Review{approvedBy("naghuale", second, false)}

	if verdict := Evaluate(facts); !verdict.Ready {
		t.Errorf("Evaluate = %+v, want the owner of the repository to count", verdict)
	}
	facts.Reviews = []Review{approvedBy("naghuale-e", second, false)}
	if verdict := Evaluate(facts); verdict.Reason != ApprovalUntrusted {
		t.Errorf("Evaluate = %q (%s), want %q", verdict.Reason, verdict.Detail, ApprovalUntrusted)
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
