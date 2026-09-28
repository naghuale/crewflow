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

// TestCommentsThatWereEdited is the fact a gate cannot do without: a record of a
// review that was changed after it was published is a record anybody can rewrite in
// silence, and the gate counts the ones that were not edited (docs/DESIGN.md §7h).
func TestCommentsThatWereEdited(t *testing.T) {
	m := newMachine().prints("pr view", fixture(t, "comments-edited.json"))
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
	m := newMachine().prints("pr view", fixture(t, "comments-broken.json"))
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

// TestRequiredChecksOfTheRulesOfTheBranch: the rules of a branch of GitHub are of
// two kinds and both are asked about, because a project may have either or both.
// Here the branch is protected with two contexts and a ruleset demands the same two,
// and one check of the pair is named in both (docs/DESIGN.md §7h).
func TestRequiredChecksOfTheRulesOfTheBranch(t *testing.T) {
	m := newMachine().
		prints("api repos/"+repo+"/branches/main", fixture(t, "branch-protected.json")).
		prints("api --include repos/"+repo+"/branches/main/protection", fixture(t, "protection.raw")).
		prints("api repos/"+repo+"/rulesets?includes_parents=true", fixture(t, "rulesets.json")).
		prints("api repos/"+repo+"/rulesets/24112591", fixture(t, "ruleset.json")).
		prints("api repos/"+repo+"/rulesets/24115315", fixture(t, "ruleset-work-branches.json"))
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

// TestRequiredChecksOfARulesetOnly is a branch that is protected by rulesets rather
// than by the protection of the branch: the protection of it is not there (404) and
// the ruleset of the default branch is what names the checks.
func TestRequiredChecksOfARulesetOnly(t *testing.T) {
	m := newMachine().
		prints("api repos/"+repo+"/branches/main", fixture(t, "branch-protected.json")).
		prints("api --include repos/"+repo+"/branches/main/protection", fixture(t, "protection-none.raw")).
		prints("api repos/"+repo+"/rulesets?includes_parents=true", fixture(t, "rulesets.json")).
		prints("api repos/"+repo+"/rulesets/24112591", fixture(t, "ruleset.json")).
		prints("api repos/"+repo+"/rulesets/24115315", fixture(t, "ruleset-work-branches.json"))
	a := New(repo, "", m.env(t))

	got, err := a.RequiredChecks(t.Context())
	if err != nil {
		t.Fatalf("RequiredChecks returned an error: %v", err)
	}

	if len(got) != 2 {
		t.Errorf("RequiredChecks = %+v, want the two checks of the ruleset of the branch", got)
	}
}

// TestRequiredChecksOfARulesetAboutOtherBranches: a ruleset of the branches of the
// work says nothing about the checks a change of the default branch has to pass, and
// crewflow does not read it as if it did (docs/DESIGN.md §7h).
func TestRequiredChecksOfARulesetAboutOtherBranches(t *testing.T) {
	m := newMachine().
		prints("api repos/"+repo+"/branches/main", fixture(t, "branch-unprotected.json")).
		prints("api repos/"+repo+"/rulesets?includes_parents=true", fixture(t, "rulesets-work.json")).
		prints("api repos/"+repo+"/rulesets/24115315", fixture(t, "ruleset-work-branches.json"))
	a := New(repo, "", m.env(t))

	got, err := a.RequiredChecks(t.Context())
	if err != nil {
		t.Fatalf("RequiredChecks returned an error: %v", err)
	}

	if len(got) != 0 {
		t.Errorf("RequiredChecks = %+v, want none: the only ruleset is about the branches of the work", got)
	}
}

// TestRequiredChecksOfABranchNobodyMayRead is the case a gate has to tell apart from
// a branch without rules: the branch says it is protected and the API will not tell
// which checks it demands. That is not an answer, and an answer that is not there is
// `forge-unavailable` and not "every check of the head has to be green"
// (docs/DESIGN.md §7h).
func TestRequiredChecksOfABranchNobodyMayRead(t *testing.T) {
	m := newMachine().
		prints("api repos/"+repo+"/branches/main", fixture(t, "branch-protected.json")).
		prints("api --include repos/"+repo+"/branches/main/protection", fixture(t, "protection-refused.raw"))
	a := New(repo, "", m.env(t))

	if _, err := a.RequiredChecks(t.Context()); err == nil {
		t.Fatal("RequiredChecks read the rules of a branch the host would not show")
	} else if !strings.Contains(err.Error(), "403") {
		t.Errorf("the error %q does not say that the host refused to answer", err)
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
