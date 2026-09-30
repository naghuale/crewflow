package github

import (
	"reflect"
	"strings"
	"testing"

	"github.com/naghuale/crewflow/internal/forge"
)

// The head of a change of a test, and the ref of a change request of it, which is
// the name GitHub keeps the head of a pull request under.
const (
	head   = "fb8c5057f225d20e06d3d609750fe36458fa9fd7"
	change = 19
)

// TestChangeRequestOfAReview reads the answer of "gh pr view" of a request that is
// ready and checks the three facts beyond the head that a review needs: whether it
// is a draft, which repository the work is in, and whether the host can merge it
// into the branch it is meant for (docs/DESIGN.md §7h).
func TestChangeRequestOfAReview(t *testing.T) {
	m := newMachine().prints("pr view", fixture(t, "change.json"))
	a := New(repo, "", m.env(t))

	got, err := a.ChangeRequest(t.Context(), 2)
	if err != nil {
		t.Fatalf("ChangeRequest(2) returned an error: %v", err)
	}

	want := forge.ChangeRequest{
		Number:     2,
		URL:        "https://github.com/naghuale/crewflow/pull/2",
		HeadBranch: "feat/m1-1-skeleton",
		HeadSHA:    sha,
		BaseBranch: "main",
		State:      "merged",
		Body:       "Closes #1 ## What changed - `cmd/crewflow`: the binary, disp …",
		Repository: "naghuale/crewflow",
		// A merged request has no merge state any more, and the host says so
		// rather than saying it is clean: the word is kept as it is written, and
		// what the gate makes of it is the gate's business (docs/DESIGN.md §7h).
		MergeState: "UNKNOWN",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ChangeRequest(2) = %+v,\nwant %+v", got, want)
	}
	wantCommand := "pr view 2 -R " + repo + " --json number,url,headRefName,headRefOid,baseRefName,state,body,isDraft,mergeStateStatus,headRepository"
	if got := m.commandLine(0); got != wantCommand {
		t.Errorf("the adapter ran %q, want %q", got, wantCommand)
	}
}

// TestChangeRequestThatConflicts is the change of PR #19 (29.09.2026): GitHub says
// the change cannot be merged into main, and the word of the host is "DIRTY". A
// gate that reported the checks of such a change as missing would be reporting the
// absence of something GitHub is never going to run (docs/DESIGN.md §7h).
func TestChangeRequestThatConflicts(t *testing.T) {
	m := newMachine().prints("pr view", fixture(t, "change-conflicting.json"))
	a := New(repo, "", m.env(t))

	got, err := a.ChangeRequest(t.Context(), change)
	if err != nil {
		t.Fatalf("ChangeRequest(%d) returned an error: %v", change, err)
	}

	if !got.Conflicted {
		t.Errorf("ChangeRequest(%d) = %+v, want the change to be said to conflict with its branch", change, got)
	}
	if got.MergeState != "DIRTY" {
		t.Errorf("the merge state is %q, want the word of the host", got.MergeState)
	}
	if got.Repository != "naghuale/crewflow" {
		t.Errorf("the repository of the change is %q, want %q", got.Repository, "naghuale/crewflow")
	}
}

// TestChangedFiles is what the boundaries of a task are checked against: the work
// itself and not what the executor of it said it wrote (docs/DESIGN.md §7c, §7h).
func TestChangedFiles(t *testing.T) {
	m := newMachine().prints("pr view", fixture(t, "files.json"))
	a := New(repo, "", m.env(t))

	got, err := a.ChangedFiles(t.Context(), 7)
	if err != nil {
		t.Fatalf("ChangedFiles(7) returned an error: %v", err)
	}

	want := []string{"internal/gate/gate.go", "internal/gate/gate_test.go", "docs/DESIGN.md"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ChangedFiles(7) = %v, want %v", got, want)
	}
	if want := "pr view 7 -R " + repo + " --json files"; m.commandLine(0) != want {
		t.Errorf("the adapter ran %q, want %q", m.commandLine(0), want)
	}
}

// TestTheAuthorOfARecordIsNamedWithTheKindOfTheAccount is F-081 (01.10.2026): GraphQL
// names an App by its slug alone — `crewflow-orchestrator` — and a person may have that
// very slug as a login, so the two are one string there and the gate counts records by
// the name of an account. The kind of the account is read with the name, and the suffix
// the host gives accounts of that kind is put there by that kind and not read out of the
// login: a record of the App and a record of a person with the App's slug are then two
// different accounts (docs/DESIGN.md §7h, §7i).
func TestTheAuthorOfARecordIsNamedWithTheKindOfTheAccount(t *testing.T) {
	m := newMachine().
		prints("pr view", fixture(t, "comments-bot.json")).
		prints("api repos/"+repo+"/issues/2/comments?per_page=100&page=1", fixture(t, "comment-authors.json"))
	a := New(repo, "", m.env(t))

	comments, err := a.Comments(t.Context(), 2)
	if err != nil {
		t.Fatalf("Comments(2) returned an error: %v", err)
	}

	if len(comments) != 2 {
		t.Fatalf("Comments(2) = %d records, want 2", len(comments))
	}
	// Both records were written under the same login as GraphQL gives it; what tells
	// them apart is the kind of the account the REST API holds.
	want := []string{"crewflow-orchestrator[bot]", "crewflow-orchestrator"}
	for i, author := range want {
		if comments[i].Author != author {
			t.Errorf("the author of the record %d is %q, want %q", i, comments[i].Author, author)
		}
	}
}

// TestARecordWhoseAuthorTheHostDoesNotNameIsARefusal: the account a record was written
// by is what makes it a record of somebody, and a record nobody may be named for is not
// a record the gate may count. An answer of the host that holds no name is not that, and
// it is not an answer either — it is `forge-unavailable` (§7h).
func TestARecordWhoseAuthorTheHostDoesNotNameIsARefusal(t *testing.T) {
	cases := []struct {
		name   string
		answer string
	}{
		{
			name:   "the answer holds no account at all",
			answer: fixture(t, "comment-authors-unnamed.json"),
		},
		{
			name:   "the answer does not hold that record",
			answer: "[]",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine().
				prints("pr view", fixture(t, "comments-bot.json")).
				prints("api repos/"+repo+"/issues/2/comments?per_page=100&page=1", tc.answer)
			a := New(repo, "", m.env(t))

			if _, err := a.Comments(t.Context(), 2); err == nil {
				t.Fatal("Comments(2) read a record whose author nobody may be named for")
			}
		})
	}
}

// TestCommentsThatWereEdited is the fact a gate cannot do without: a record of a
// review that was changed after it was published is a record anybody can rewrite in
// silence, and the gate counts the ones that were not edited (docs/DESIGN.md §7h).
func TestCommentsThatWereEdited(t *testing.T) {
	m := newMachine().
		prints("pr view", fixture(t, "comments-edited.json")).
		prints("api repos/"+repo+"/issues/2/comments?per_page=100&page=1", fixture(t, "comment-authors-owner.json"))
	a := New(repo, "", m.env(t))

	comments, err := a.Comments(t.Context(), 2)
	if err != nil {
		t.Fatalf("Comments(2) returned an error: %v", err)
	}

	if len(comments) != 2 {
		t.Fatalf("Comments(2) = %d records, want 2", len(comments))
	}
	if comments[0].Edited {
		t.Error("the first record was said to be edited, want a record nobody touched")
	}
	if !comments[1].Edited {
		t.Errorf("the second record of %s was not said to be edited, want it said to be", comments[1].Author)
	}
}

// TestCommentsWithAMomentNobodyCanRead: the answer says a comment was edited and
// does not say when in any way a program can read. That is not a record that may be
// counted and not a record that may be refused silently either: the review is told
// that the host did not give a certain answer (docs/DESIGN.md §7h).
func TestCommentsWithAMomentNobodyCanRead(t *testing.T) {
	m := newMachine().
		prints("pr view", fixture(t, "comments-broken.json")).
		prints("api repos/"+repo+"/issues/2/comments?per_page=100&page=1", fixture(t, "comment-authors-owner.json"))
	a := New(repo, "", m.env(t))

	if _, err := a.Comments(t.Context(), 2); err == nil {
		t.Fatal("Comments(2) read a record whose edit cannot be told from its publication")
	} else if !strings.Contains(err.Error(), "not a moment") {
		t.Errorf("the error %q does not say what was wrong with the answer", err)
	}
}

// TestChecksOfACommit reads the checks of a commit out of both places GitHub keeps
// them: the runs of the apps that started on it and the marks written through the
// API of statuses. A gate has to see the second kind as well, because a green mark
// of the API of statuses is the mark of whoever wrote it (docs/DESIGN.md §7h).
func TestChecksOfACommit(t *testing.T) {
	m := newMachine().
		prints("api repos/"+repo+"/commits/"+head+"/check-runs?per_page=100&page=1", fixture(t, "check-runs-app.json")).
		prints("api repos/"+repo+"/commits/"+head+"/status", fixture(t, "status.json"))
	a := New(repo, "", m.env(t))

	checks, err := a.Checks(t.Context(), head)
	if err != nil {
		t.Fatalf("Checks returned an error: %v", err)
	}

	want := []forge.CheckRun{
		{Name: "test (ubuntu-latest)", State: forge.CheckSuccess, App: "github-actions", SHA: head},
		{Name: "lint", State: forge.CheckFailure, App: "github-actions", SHA: head},
		// The mark of the API of statuses carries no app at all, and that is what
		// the gate refuses it for.
		{Name: "lint", State: forge.CheckFailure, App: "", SHA: head},
	}
	if !reflect.DeepEqual(checks, want) {
		t.Errorf("Checks = %+v,\nwant %+v", checks, want)
	}
}

// TestChecksOfACommitWithNoMarks is a project that runs its checks as workflows and
// writes no marks: the answer of the API of statuses is an empty list, and it is read
// as one rather than as a refusal (docs/DESIGN.md §7h).
func TestChecksOfACommitWithNoMarks(t *testing.T) {
	m := newMachine().
		prints("api repos/"+repo+"/commits/"+head+"/check-runs?per_page=100&page=1", fixture(t, "check-runs-none.json")).
		prints("api repos/"+repo+"/commits/"+head+"/status", fixture(t, "status-none.json"))
	a := New(repo, "", m.env(t))

	checks, err := a.Checks(t.Context(), head)
	if err != nil {
		t.Fatalf("Checks returned an error: %v", err)
	}

	if len(checks) != 0 {
		t.Errorf("Checks = %+v, want no checks at all", checks)
	}
}

// TestTheRulesOfABranchThatIsProtectedByRulesetsAlone is the case F-079 (01.10.2026):
// a branch whose rules are rulesets says of itself that it is protected, and the rules
// of the protection of the branch itself are a right of an administrator that the App of
// the orchestrator has not and is not to be given (§7i). Reading the rules of such a
// branch is asking the host which rules apply to that branch, and nothing else — so the
// question that needs administration is not asked at all (docs/DESIGN.md §7h).
func TestTheRulesOfABranchThatIsProtectedByRulesetsAlone(t *testing.T) {
	m := newMachine().
		prints("api repos/"+repo+"/branches/main", fixture(t, "branch-rulesets.json")).
		prints("api --include repos/"+repo+"/rules/branches/main", fixture(t, "rules-of-branch.raw")).
		prints("api --include repos/"+repo+"/branches/main/protection", fixture(t, "protection-refused.raw"))
	a := New(repo, "", m.env(t))

	got, err := a.RequiredChecks(t.Context())

	if err != nil {
		t.Fatalf("RequiredChecks returned an error: %v", err)
	}
	want := []forge.RequiredCheck{
		{Name: "test (ubuntu-latest)", App: actionsApp},
		{Name: "lint", App: actionsApp},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("RequiredChecks = %+v,\nwant %+v", got, want)
	}
	// The refusal is registered in the machine on purpose: were the rules of the
	// protection of the branch asked for, this would be the answer they would get, and
	// the whole point of the change is that it never is.
	if _, asked := m.commandOf("api --include repos/" + repo + "/branches/main/protection"); asked {
		t.Error("the adapter asked for the rules of the protection of the branch: " +
			"only an administrator may read them, and this branch has no protection of its own")
	}
	for i := range m.ran {
		if line := m.commandLine(i); strings.HasSuffix(line, "/rulesets?includes_parents=true") {
			t.Errorf("the adapter asked for the rulesets of the repository: %q, "+
				"which is a question about Administration, while the rules of the branch are not", line)
		}
	}
}

// TestTheRulesOfABranchNobodySaysIsProtected: the summary of the branch may come back
// without saying what protects it — a token that is not shown it, a changed answer of the
// host, a body of an error decoded as JSON. Reading that as "the branch is not protected"
// is a change let through on a silence, and the gate is not allowed to do it: the rules of
// the branch are then unknown and `forge-unavailable` is the answer (docs/DESIGN.md §7h).
func TestTheRulesOfABranchNobodySaysIsProtected(t *testing.T) {
	cases := []struct {
		name   string
		answer string
	}{
		{
			name:   "the branch says it is protected and does not say by what",
			answer: "branch-unnamed.json",
		},
		{
			name:   "the answer is not about a branch at all",
			answer: "branch-silent.json",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine().
				prints("api repos/"+repo+"/branches/main", fixture(t, tc.answer)).
				prints("api --include repos/"+repo+"/rules/branches/main", fixture(t, "rules-of-branch.raw"))
			a := New(repo, "", m.env(t))

			got, err := a.Rules(t.Context())

			if err == nil {
				t.Fatalf("Rules = %+v, want an error: the host did not say what protects the branch", got)
			}
			if !strings.Contains(err.Error(), "did not say what protects the branch main") {
				t.Errorf("the error %q does not say what the answer of the host was missing", err)
			}
			// The question about the rules of the branch is not asked either: a gate
			// that cannot tell the rules of a branch from their absence must not read
			// them as a fact about the branch.
			if _, asked := m.commandOf("api --include repos/" + repo + "/rules/branches/main"); asked {
				t.Error("the adapter asked for the rules of a branch whose protection it does not know")
			}
		})
	}
}

// TestRequiredChecksOfTheRulesOfTheBranch: the rules of a branch of GitHub are of
// two kinds and both are asked about, because a project may have either or both.
// Here the branch is protected with two contexts and a ruleset demands the same two,
// and one check of the pair is named in both (docs/DESIGN.md §7h).
func TestRequiredChecksOfTheRulesOfTheBranch(t *testing.T) {
	m := newMachine().
		prints("api repos/"+repo+"/branches/main", fixture(t, "branch-protected.json")).
		prints("api --include repos/"+repo+"/branches/main/protection", fixture(t, "protection.raw")).
		prints("api --include repos/"+repo+"/rules/branches/main", fixture(t, "rules-of-branch.raw"))
	a := New(repo, "", m.env(t))

	got, err := a.RequiredChecks(t.Context())
	if err != nil {
		t.Fatalf("RequiredChecks returned an error: %v", err)
	}

	want := []forge.RequiredCheck{
		{Name: "test (ubuntu-latest)", App: actionsApp},
		{Name: "lint", App: actionsApp},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("RequiredChecks = %+v,\nwant %+v", got, want)
	}
}

// TestTheChecksTheRulesOfTheBranchDemandAreTakenFromTheRulesOfThatBranch is what a
// gate has to be able to read where the rules of a branch are rulesets: the rule that
// demands checks says which checks, and the host gives it for the branch it applies to
// without anybody naming the ruleset it is in (docs/DESIGN.md §7h).
func TestTheChecksTheRulesOfTheBranchDemandAreTakenFromTheRulesOfThatBranch(t *testing.T) {
	m := newMachine().
		prints("api repos/"+repo+"/branches/main", fixture(t, "branch-rulesets.json")).
		prints("api --include repos/"+repo+"/rules/branches/main", fixture(t, "rules-of-branch.raw"))
	a := New(repo, "", m.env(t))

	rules, err := a.Rules(t.Context())
	if err != nil {
		t.Fatalf("Rules returned an error: %v", err)
	}

	if rules.State != forge.RulesNamed {
		t.Errorf("Rules = %+v, want the rules of the branch to be said to be there", rules)
	}
	want := []forge.RequiredCheck{
		{Name: "test (ubuntu-latest)", App: actionsApp},
		{Name: "lint", App: actionsApp},
	}
	if !reflect.DeepEqual(rules.Required, want) {
		t.Errorf("Rules.Required = %+v,\nwant %+v", rules.Required, want)
	}
}

// TestRulesOfTheBranchOfAPublicRepository: a repository whose plan has rules has them
// named, and what it named is what stands — the answer of a repository with rules and
// the answer of one without them are told apart by the state of the rules (docs/DESIGN.md §7h, §7k).
func TestRulesOfTheBranchOfAPublicRepository(t *testing.T) {
	m := newMachine().
		prints("api repos/"+repo+"/branches/main", fixture(t, "branch-rulesets.json")).
		prints("api --include repos/"+repo+"/rules/branches/main", fixture(t, "rules-of-branch.raw"))
	a := New(repo, "", m.env(t))

	got, err := a.Rules(t.Context())
	if err != nil {
		t.Fatalf("Rules returned an error: %v", err)
	}

	if got.State != forge.RulesNamed {
		t.Errorf("Rules = %+v, want the rules of the branch to be said to be there", got)
	}
	if len(got.Required) != 2 {
		t.Errorf("Rules = %+v, want the two checks the rules of the branch demand", got)
	}
}

// TestRulesOfABranchNobodyProtected: a branch of a project that protects nothing has
// no rules to ask about, and the host says so with an absence rather than a refusal —
// which is an answer, and not a gap in what crewflow knows about the branch
// (docs/DESIGN.md §7h).
func TestRulesOfABranchNobodyProtected(t *testing.T) {
	m := newMachine().
		prints("api repos/"+repo+"/branches/main", fixture(t, "branch-unprotected.json")).
		prints("api --include repos/"+repo+"/rules/branches/main", fixture(t, "rules-of-branch-none.raw"))
	a := New(repo, "", m.env(t))

	got, err := a.Rules(t.Context())
	if err != nil {
		t.Fatalf("Rules returned an error: %v", err)
	}

	if got.State != forge.RulesNamed {
		t.Errorf("Rules = %+v, want the rules of the branch to be said to be there and name no check", got)
	}
	if len(got.Required) != 0 {
		t.Errorf("Rules = %+v, want no checks: nobody demands any", got)
	}
}

// TestRulesOfABranchWhoseProtectionTheHostDoesNotFind: the summary of the branch says
// the protection of a branch is on and the rules of that protection are not there to be
// found. That is a fact about the branch and not a gap — the host names no protection of
// the branch itself — so the rules of the branch are the ones of its rulesets, and
// nobody is refused over a protection that is not there (docs/DESIGN.md §7h).
func TestRulesOfABranchWhoseProtectionTheHostDoesNotFind(t *testing.T) {
	m := newMachine().
		prints("api repos/"+repo+"/branches/main", fixture(t, "branch-protected.json")).
		prints("api --include repos/"+repo+"/branches/main/protection", fixture(t, "protection-none.raw")).
		prints("api --include repos/"+repo+"/rules/branches/main", fixture(t, "rules-of-branch.raw"))
	a := New(repo, "", m.env(t))

	got, err := a.RequiredChecks(t.Context())

	if err != nil {
		t.Fatalf("RequiredChecks returned an error: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("RequiredChecks = %+v, want the two checks the rules of the branch demand", got)
	}
}

// TestRulesOfARepositoryWithoutRulesOnItsPlan is the answer GitHub gives the rules of
// a private repository on the free plan (F-043 of 30.09.2026): it has none, it says
// which plan it is about, and that is a fact about the repository rather than a
// silence of the host. Read as a silence it made a review refuse every change of such
// a repository with `forge-unavailable`, and a gate nobody may merge through
// (docs/DESIGN.md §7h, §7k).
func TestRulesOfARepositoryWithoutRulesOnItsPlan(t *testing.T) {
	m := newMachine().
		prints("api repos/"+repo+"/branches/main", fixture(t, "branch-unprotected.json")).
		prints("api --include repos/"+repo+"/rules/branches/main", fixture(t, "rules-of-branch-on-plan.raw"))
	a := New(repo, "", m.env(t))

	got, err := a.Rules(t.Context())

	if err != nil {
		t.Fatalf("Rules returned an error: %v", err)
	}
	if got.State != forge.RulesUnavailableOnPlan {
		t.Errorf("Rules = %+v, want the rules of the branch to be said to be out of the plan of the repository", got)
	}
	if len(got.Required) != 0 {
		t.Errorf("Rules = %+v, want no checks at all: there are no rules to demand any", got)
	}
	// The same answer without the plan in it is what the CI of §7g is asked for, and
	// an error there is what a review of a repository of the free plan used to get.
	required, err := a.RequiredChecks(t.Context())
	if err != nil {
		t.Errorf("RequiredChecks returned an error: %v", err)
	}
	if len(required) != 0 {
		t.Errorf("RequiredChecks = %v, want no checks", required)
	}
}

// TestRulesOfARepositoryNobodyMayRead walks the two answers that are not a fact about
// the plan: a refusal with nothing of the plan in it and a host that did not answer at
// all. Both are a host that could not be read, and a gate that read either of them as
// "there are no rules" would take a refusal of access for an answer about the
// repository (docs/DESIGN.md §7h).
func TestRulesOfARepositoryNobodyMayRead(t *testing.T) {
	cases := []struct {
		name   string
		answer string
		want   string
	}{
		{
			name:   "the host refuses and says nothing of a plan",
			answer: fixture(t, "rules-of-branch-refused.raw"),
			want:   "403",
		},
		{
			name:   "the host did not answer",
			answer: fixture(t, "rules-of-branch-silent.raw"),
			want:   "500",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine().
				prints("api repos/"+repo+"/branches/main", fixture(t, "branch-unprotected.json")).
				prints("api --include repos/"+repo+"/rules/branches/main", tc.answer)
			a := New(repo, "", m.env(t))

			got, err := a.Rules(t.Context())

			if err == nil {
				t.Fatalf("Rules = %+v, want an error: the host did not say what the rules of the branch are", got)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the error %q does not say what the host answered", err)
			}
		})
	}
}

// TestRequiredChecksOfABranchNobodyMayRead is the case a gate has to tell apart from
// a branch without rules: the branch says its protection of a branch is on and the API
// will not tell which checks it demands. That is not an answer, and an answer that is
// not there is `forge-unavailable` and not "every check of the head has to be green"
// (docs/DESIGN.md §7h).
func TestRequiredChecksOfABranchNobodyMayRead(t *testing.T) {
	m := newMachine().
		prints("api repos/"+repo+"/branches/main", fixture(t, "branch-protected.json")).
		prints("api --include repos/"+repo+"/branches/main/protection", fixture(t, "protection-refused.raw"))
	a := New(repo, "", m.env(t))

	got, err := a.RequiredChecks(t.Context())

	if err == nil {
		t.Fatalf("RequiredChecks = %+v, want a refusal: the rules of the branch are not there to be read", got)
	}
	// The refusal is right and stays: an App of the orchestrator has no right of an
	// administrator and must not get one, and what is to be done about it is said in
	// the same words (docs/DESIGN.md §7h, §7i).
	for _, want := range []string{"403", "administration", "ruleset"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error %q does not say %q: a person has to be told what to do about it", err, want)
		}
	}
}

// TestHeadRef is the name of the ref of a change request on GitHub, and it is the
// one thing a review has to ask git for by name: the commit of the head of a change
// has to be in a checkout before anything may be said about its history
// (docs/DESIGN.md §7h).
func TestHeadRef(t *testing.T) {
	a := New(repo, "", newMachine().env(t))

	if got, want := a.HeadRef(7), "refs/pull/7/head"; got != want {
		t.Errorf("HeadRef(7) = %q, want %q", got, want)
	}
}

// TestWriteComment writes the record of a review as the person who runs crewflow and
// never as the App of the executor: a record the executor could write is a record the
// gate would have to refuse (docs/DESIGN.md §7h, §7i).
func TestWriteComment(t *testing.T) {
	m := newMachine().prints("pr comment", "")
	a := New(repo, "", m.env(t))

	err := a.WriteComment(t.Context(), 7, "REVIEW: APPROVED "+head)
	if err != nil {
		t.Fatalf("WriteComment returned an error: %v", err)
	}

	if got, want := m.commandLine(0), "pr comment 7 -R "+repo+" --body REVIEW: APPROVED "+head; got != want {
		t.Errorf("the adapter ran %q, want %q", got, want)
	}
	if environments := m.environment; len(environments) > 0 {
		for _, key := range environments[0] {
			if strings.HasPrefix(key, "GH_TOKEN=") {
				t.Error("the record of a review was written with a token of a run in its environment")
			}
		}
	}
}

// TestWriteCommentThatFailed says what to do about a comment nobody wrote.
func TestWriteCommentThatFailed(t *testing.T) {
	m := newMachine().fails("pr comment", "pull request not found\n")
	a := New(repo, "", m.env(t))

	err := a.WriteComment(t.Context(), 7, "REVIEW: APPROVED "+head)
	if err == nil {
		t.Fatal("WriteComment said nothing about a comment it could not write")
	}
	if !strings.Contains(err.Error(), "pull request not found") {
		t.Errorf("the error %q does not say what the host answered", err)
	}
}
