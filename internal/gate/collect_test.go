package gate

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/forge"
)

// A host of the test: a change request, what is written under it, and the answers
// the project of a test is made of. Every question a review asks has an answer here,
// and every way of not answering has one as well, so that a gathering that could not
// learn something is a gathering a test can watch fail (docs/DESIGN.md §7h).
type host struct {
	change  forge.ChangeRequest
	files   []string
	comment []forge.Comment
	checks  []forge.CheckRun
	needed  []forge.RequiredCheck
	// plan is what the host says about the rules of the branch themselves: the rules
	// are there, or the plan of the repository has none to demand anything with.
	plan forge.RuleState

	// noChange, noFiles, noComments, noChecks and noRules are the ways a host of the
	// test does not answer, one per question a review asks of it.
	noChange   error
	noFiles    error
	noComments error
	noChecks   error
	noRules    error
}

// ChangeRequest returns the change the host of the test holds, or the way it does
// not hold it.
func (h *host) ChangeRequest(context.Context, int) (forge.ChangeRequest, error) {
	if h.noChange != nil {
		return forge.ChangeRequest{}, h.noChange
	}
	return h.change, nil
}

// Comments returns what is written under the change, oldest first.
func (h *host) Comments(context.Context, int) ([]forge.Comment, error) {
	if h.noComments != nil {
		return nil, h.noComments
	}
	return h.comment, nil
}

// FindChangeRequest returns nothing: a review is always about a change a person
// named, and finding one by its branch is the business of a run.
func (h *host) FindChangeRequest(context.Context, string) (forge.ChangeRequest, bool, error) {
	if h.noChange != nil {
		return forge.ChangeRequest{}, false, h.noChange
	}
	return h.change, true, nil
}

// ChangedFiles returns the files the change touches.
func (h *host) ChangedFiles(context.Context, int) ([]string, error) {
	if h.noFiles != nil {
		return nil, h.noFiles
	}
	return h.files, nil
}

// HeadRef is the ref of the host that stands at the head of a change, the way GitHub
// keeps the head of a pull request.
func (h *host) HeadRef(number int) string { return headRef(number) }

// Checks returns the checks of the commit.
func (h *host) Checks(context.Context, string) ([]forge.CheckRun, error) {
	if h.noChecks != nil {
		return nil, h.noChecks
	}
	return h.checks, nil
}

// RequiredChecks returns what the rules of the branch demand of a change.
func (h *host) RequiredChecks(context.Context) ([]forge.RequiredCheck, error) {
	if h.noRules != nil {
		return nil, h.noRules
	}
	return h.needed, nil
}

// Rules returns what the rules of the branch demand and what the host said about the
// rules themselves: a repository of a free plan has no rules to demand anything with,
// and that is an answer about the repository and not a silence of the host
// (docs/DESIGN.md §7h, §7k).
func (h *host) Rules(context.Context) (forge.BranchRules, error) {
	if h.noRules != nil {
		return forge.BranchRules{}, h.noRules
	}
	return forge.BranchRules{Required: h.needed, State: h.plan}, nil
}

// Status and Doctor are of no use to a review and are here for the role of §7g this
// host plays as well.
func (*host) Status(context.Context, string) (forge.CheckState, error) {
	return forge.CheckNone, nil
}

func (*host) Doctor(context.Context) []forge.Check { return nil }

// headless is a host that can say what a change is but not which ref stands at its
// head: an adapter of a host that keeps no ref of a change, and a review of it cannot
// ask git about a commit it has not got (docs/DESIGN.md §7h).
type headless struct{ *host }

// HeadRef is a ref that is not there, and the gathering says so rather than fetching
// something of its own.
func (headless) HeadRef(int) string { return "" }

// theHost is a host of the test with a change about which there is nothing to say:
// open, of the project, meant for the default branch, green, inside the boundaries,
// and approved by the owner on the head itself.
func theHost() *host {
	return &host{
		change: forge.ChangeRequest{
			Number:     7,
			URL:        "https://github.com/naghuale/crewflow/pull/7",
			HeadSHA:    second,
			BaseBranch: "main",
			State:      "open",
			Body:       "Closes #7",
			Repository: "naghuale/crewflow",
		},
		files:   []string{"internal/gate/gate.go"},
		comment: []forge.Comment{{Author: owner, Body: ApproveOf(second, 7), CreatedAt: time.Now().Add(-time.Hour)}},
		checks:  []forge.CheckRun{{Name: "test", State: forge.CheckSuccess, App: actionsApp, SHA: second}},
		needed:  []forge.RequiredCheck{{Name: "test", App: actionsApp}},
		plan:    forge.RulesNamed,
	}
}

// theHead is the host of the test with every fact of it about the head of the change
// of the repository of the test: the change stands there, the approval is of that
// commit and the green check is of that commit, as they would be in a pull request
// that is ready.
func theHead(t *testing.T, repo *repository) *host {
	t.Helper()
	h := theHost()
	head := repo.sha(t, "change")
	h.change.HeadSHA = head
	h.comment = []forge.Comment{{
		Author:    owner,
		Body:      ApproveOf(head, 7),
		CreatedAt: time.Date(2026, time.September, 28, 12, 0, 0, 0, time.UTC),
	}}
	h.checks = []forge.CheckRun{{Name: "test", State: forge.CheckSuccess, App: actionsApp, SHA: head}}
	return h
}

// The app GitHub Actions runs under, the app a check of this repository is taken
// from (docs/DESIGN.md §7h).
const actionsApp = "github-actions"

// deps is the review of a change of the repository of the test: the host of the test
// as the host of the code and as the CI, the project, the reviewers, the task with
// its boundaries, and a checkout of the repository of the test to ask git in.
func deps(t *testing.T, repo *repository, h *host) Deps {
	t.Helper()
	return Deps{
		Forge:         h,
		CI:            h,
		Repository:    "naghuale/crewflow",
		DefaultBranch: "main",
		Reviewers:     []string{owner},
		Task:          7,
		Boundaries:    []string{"internal/gate/**"},
		RequireChecks: true,
		Git:           repo.history(),
	}
}

// TestCollect judges a change out of what a host says and what git says, with
// nothing but those two: the change of the repository of the test, its approval and
// its green check are the facts of one review, and the verdict is what the gate makes
// of them (docs/DESIGN.md §7h).
func TestCollect(t *testing.T) {
	repo := newRepository(t)
	host := theHead(t, repo)

	facts := Collect(t.Context(), deps(t, repo, host), 7)

	if facts.Unavailable != "" {
		t.Fatalf("Collect could not gather the facts: %s", facts.Unavailable)
	}
	verdict := Evaluate(facts)
	if !verdict.Ready {
		t.Errorf("Evaluate = %+v, want a change that may be merged", verdict)
	}
	if facts.Files[0] != "internal/gate/gate.go" {
		t.Errorf("the files of the change are %v, want what the host said", facts.Files)
	}
	if !facts.DefaultIsAncestor {
		t.Error("the default branch is not an ancestor of the head: the history of the repository of the test says it is")
	}
}

// TestCollectTurnsEverySilenceIntoAReason: whatever the host does not answer, the
// gathering hands the reason of §7h over and not an error — a review whose facts are
// half gathered is a review a person has to be told about, and `forge-unavailable` is
// what a host that did not answer is called (docs/DESIGN.md §7h).
func TestCollectTurnsEverySilenceIntoAReason(t *testing.T) {
	cases := []struct {
		name  string
		given func(*host)
		// asHost replaces the host of the test with a host of a lesser kind, for the
		// cases where what is missing is a capability and not an answer.
		asHost func(*host) forge.Forge
		want   Reason
	}{
		{
			name:  "the host does not have the change",
			given: func(h *host) { h.noChange = errors.New("gh: Not Found (HTTP 404)") },
			want:  ForgeUnavailable,
		},
		{
			name:  "the host does not say what is written under the change",
			given: func(h *host) { h.noComments = errors.New("gh: exited with 1") },
			want:  ForgeUnavailable,
		},
		{
			name:  "the host does not say which files the change touches",
			given: func(h *host) { h.noFiles = errors.New("gh: exited with 1") },
			want:  ForgeUnavailable,
		},
		{
			name:  "the host does not say what its rules of the branch demand",
			given: func(h *host) { h.noRules = errors.New("gh: exited with 1") },
			want:  ForgeUnavailable,
		},
		{
			name:  "the host does not say what the checks of the head are",
			given: func(h *host) { h.noChecks = errors.New("gh: exited with 1") },
			want:  ForgeUnavailable,
		},
		{
			name:   "the host has no ref for the head of the change",
			given:  func(h *host) {},
			asHost: func(h *host) forge.Forge { return headless{h} },
			want:   ForgeUnavailable,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newRepository(t)
			host := theHead(t, repo)
			tc.given(host)

			given := deps(t, repo, host)
			if tc.asHost != nil {
				given.Forge = tc.asHost(host)
			}
			facts := Collect(t.Context(), given, 7)

			if verdict := Evaluate(facts); verdict.Reason != tc.want {
				t.Errorf("Evaluate = %q (%s), want %q", verdict.Reason, verdict.Detail, tc.want)
			}
			if facts.Unavailable == "" {
				t.Error("the facts carry no reason why they could not be gathered")
			}
		})
	}
}

// TestCollectWithoutGit reads the host of the change and stops: a machine that
// cannot start git cannot say whether the branch of the project is behind the head of
// the change, and a review that did not ask is a review of a change nobody merged
// (docs/DESIGN.md §7h).
func TestCollectWithoutGit(t *testing.T) {
	host := theHost()
	host.change.HeadSHA = first
	given := Deps{Forge: host, CI: host, Repository: "naghuale/crewflow", DefaultBranch: "main", RequireChecks: true}

	facts := Collect(t.Context(), given, 7)

	if verdict := Evaluate(facts); verdict.Reason != ForgeUnavailable {
		t.Errorf("Evaluate = %q (%s), want %q", verdict.Reason, verdict.Detail, ForgeUnavailable)
	}
	if !strings.Contains(facts.Unavailable, "git") {
		t.Errorf("the reason %q does not say what could not be read", facts.Unavailable)
	}
}

// TestCollectOfAConflictedChange: GitHub does not run the checks of a change that
// cannot be merged into its branch, so asking about them would be asking about
// something the host is never going to run. The conflict is the fact, and it is the
// gate that names it (docs/DESIGN.md §7h, PR #19 of 29.09.2026).
func TestCollectOfAConflictedChange(t *testing.T) {
	repo := newRepository(t)
	host := theHead(t, repo)
	host.change.Conflicted = true
	host.change.MergeState = "DIRTY"
	asked := false
	ci := &watchingCI{host: host, asked: &asked}

	given := deps(t, repo, host)
	given.CI = ci
	facts := Collect(t.Context(), given, 7)

	if asked {
		t.Error("the checks of a change that conflicts with its branch were asked about")
	}
	if !facts.Conflicted {
		t.Error("the facts do not say that the change conflicts with its branch")
	}
	verdict := Evaluate(facts)
	if verdict.Reason != NotFastForward {
		t.Errorf("Evaluate = %q (%s), want %q", verdict.Reason, verdict.Detail, NotFastForward)
	}
	if !strings.Contains(verdict.Detail, "rebase") {
		t.Errorf("the detail %q does not say what to do about it", verdict.Detail)
	}
}

// TestCollectOfAProjectWithoutACIHoldsNothing: a project that says it has no CI has
// said what a commit of it is to satisfy, and that is nothing. It is an answer, and
// not a gap in what the host was asked.
func TestCollectOfAProjectWithoutACIHoldsNothing(t *testing.T) {
	repo := newRepository(t)
	host := theHead(t, repo)
	given := deps(t, repo, host)
	given.CI = noChecks{}

	facts := Collect(t.Context(), given, 7)

	if verdict := Evaluate(facts); !verdict.Ready {
		t.Errorf("Evaluate = %+v, want a project without CI to be judged on everything else", verdict)
	}
}

// TestCollectRequiresWhatTheHostNames: where the rules of a branch of the host name
// no check, every check of the head is required — and a commit with no checks at all
// is not a commit that passed them (docs/DESIGN.md §7h).
func TestCollectRequiresWhatTheHostNames(t *testing.T) {
	t.Run("the checks of the head stand in for the rules", func(t *testing.T) {
		repo := newRepository(t)
		host := theHead(t, repo)
		host.needed = nil

		facts := Collect(t.Context(), deps(t, repo, host), 7)

		if len(facts.Required) != 1 || facts.Required[0].Name != "test" {
			t.Errorf("the required checks are %v, want the check the head has", facts.Required)
		}
		if verdict := Evaluate(facts); !verdict.Ready {
			t.Errorf("Evaluate = %+v, want a green check of the head to be enough", verdict)
		}
	})
	t.Run("a commit with no checks and no rules is a refusal, not a pass", func(t *testing.T) {
		repo := newRepository(t)
		host := theHead(t, repo)
		host.needed, host.checks = nil, nil

		facts := Collect(t.Context(), deps(t, repo, host), 7)

		if verdict := Evaluate(facts); verdict.Reason != ForgeUnavailable {
			t.Errorf("Evaluate = %q (%s), want %q", verdict.Reason, verdict.Detail, ForgeUnavailable)
		}
	})
	t.Run("a project that asks for no CI asks for no checks", func(t *testing.T) {
		repo := newRepository(t)
		host := theHead(t, repo)
		host.checks = nil
		given := deps(t, repo, host)
		given.RequireChecks = false

		facts := Collect(t.Context(), given, 7)

		if verdict := Evaluate(facts); !verdict.Ready {
			t.Errorf("Evaluate = %+v, want a project that does not ask for CI to be judged on everything else", verdict)
		}
	})
}

// TestCollectOfARepositoryWithoutRulesOnItsPlan is F-043 of 30.09.2026: a private
// repository on the free plan has no rules for its branch, the host says so, and a
// gate that read the answer as a silence refused every change of it with
// `forge-unavailable` — a refusal nobody can merge a task through. The rules are
// absent there, and what stands is what the project says about its CI (docs/DESIGN.md
// §7h, §7k).
func TestCollectOfARepositoryWithoutRulesOnItsPlan(t *testing.T) {
	t.Run("the checks of the head are what the project asks for", func(t *testing.T) {
		repo := newRepository(t)
		host := theHead(t, repo)
		host.needed, host.plan = nil, forge.RulesUnavailableOnPlan

		facts := Collect(t.Context(), deps(t, repo, host), 7)

		if facts.Unavailable != "" {
			t.Fatalf("Collect could not gather the facts: %s", facts.Unavailable)
		}
		if facts.Rules != forge.RulesUnavailableOnPlan {
			t.Errorf("the state of the rules is %q, want %q", facts.Rules, forge.RulesUnavailableOnPlan)
		}
		if len(facts.Required) != 1 || facts.Required[0].Name != "test" {
			t.Errorf("the required checks are %v, want the check the head has", facts.Required)
		}
		if verdict := Evaluate(facts); !verdict.Ready {
			t.Errorf("Evaluate = %+v, want a green head of a repository without rules to be enough", verdict)
		}
	})
	t.Run("a project that asks for no CI asks for no checks", func(t *testing.T) {
		repo := newRepository(t)
		host := theHead(t, repo)
		host.needed, host.plan = nil, forge.RulesUnavailableOnPlan
		given := deps(t, repo, host)
		given.RequireChecks = false

		facts := Collect(t.Context(), given, 7)

		if len(facts.Required) != 0 {
			t.Errorf("the required checks are %v, want none: the project asks for the CI of nobody", facts.Required)
		}
		if verdict := Evaluate(facts); !verdict.Ready {
			t.Errorf("Evaluate = %+v, want a project that does not ask for CI to be judged on everything else", verdict)
		}
	})
	t.Run("a head with no checks is still a refusal, and it names the plan", func(t *testing.T) {
		repo := newRepository(t)
		host := theHead(t, repo)
		host.needed, host.checks, host.plan = nil, nil, forge.RulesUnavailableOnPlan

		facts := Collect(t.Context(), deps(t, repo, host), 7)

		verdict := Evaluate(facts)
		if verdict.Reason != ForgeUnavailable {
			t.Errorf("Evaluate = %q (%s), want %q", verdict.Reason, verdict.Detail, ForgeUnavailable)
		}
		if !strings.Contains(verdict.Detail, "plan") {
			t.Errorf("the detail %q does not say that the rules are not on the plan of the repository", verdict.Detail)
		}
	})
}

// TestCollectFindsTheTaskOfAChange is what gives a change its boundaries: the
// `Closes #N` its body opens with, or the record of a review that names the task when
// there is a record at all.
func TestCollectFindsTheTaskOfAChange(t *testing.T) {
	repo := newRepository(t)
	host := theHead(t, repo)
	given := deps(t, repo, host)
	given.Task = 0

	facts := Collect(t.Context(), given, 7)

	if facts.Task != 7 {
		t.Errorf("the task of the change is %d, want 7: nothing named it but the record of the review", facts.Task)
	}
}

// watchingCI is the CI of a host of the test that says whether anything was asked of
// it at all, which is how a test sees that a question was not asked.
type watchingCI struct {
	host  *host
	asked *bool
}

// Checks is a question about the checks of a commit, and it is watched.
func (c *watchingCI) Checks(context.Context, string) ([]forge.CheckRun, error) {
	*c.asked = true
	return c.host.Checks(context.Background(), "")
}

// RequiredChecks is a question about the rules of the branch, and it is watched.
func (c *watchingCI) RequiredChecks(context.Context) ([]forge.RequiredCheck, error) {
	*c.asked = true
	return c.host.RequiredChecks(context.Background())
}

func (c *watchingCI) Status(context.Context, string) (forge.CheckState, error) {
	return forge.CheckNone, nil
}

func (c *watchingCI) Doctor(context.Context) []forge.Check { return nil }

// noChecks is the CI of a project that has none: it is a role of §7g that is asked
// the same questions as any other and has nothing to say about checks (docs/DESIGN.md §7g).
type noChecks struct{}

func (noChecks) Status(context.Context, string) (forge.CheckState, error) {
	return forge.CheckNone, nil
}

func (noChecks) Doctor(context.Context) []forge.Check { return nil }
