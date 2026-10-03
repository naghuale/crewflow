package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/forge/roles"
	"github.com/naghuale/crewflow/internal/gate"
	"github.com/naghuale/crewflow/internal/merge"
	taskrun "github.com/naghuale/crewflow/internal/run"
	"github.com/naghuale/crewflow/internal/secret"
)

// The head of a change of a test, whole: a record of a review names the whole of it,
// and a review that wrote anything else approves nothing (docs/DESIGN.md §7h).
const reviewHead = "fb8c5057f225d20e06d3d609750fe36458fa9fd7"

// reviewConfig is the file of a project that is reviewed: it says who reviews it and
// that the CI of every commit is required, which is the default of a project of
// GitHub.
const reviewConfig = `
[project]
repo = "naghuale/crewflow"
language = "en"

[executor]
command = ["opencode", "run", "--dir", "{worktree}", "--format", "json", "{prompt}"]
timeout = "1h"

[merge]
reviewers = ["naghuale"]
`

// changeOfTest is a change request about which there is nothing to say: open, of the
// project, meant for the default branch, green on the check the rules of the branch
// demand, and inside the boundaries of its task.
func changeOfTest() forge.ChangeRequest {
	return forge.ChangeRequest{
		Number:     7,
		URL:        "https://github.com/naghuale/crewflow/pull/7",
		HeadBranch: "crewflow/7-review",
		HeadSHA:    reviewHead,
		BaseBranch: "main",
		State:      "open",
		Body:       "Closes #7\n\n## What changed\n\nThe gate in code.",
		Repository: "naghuale/crewflow",
	}
}

// TestRunReviewOfAChangeThatMayBeMerged is the summary an orchestrator reads before
// it decides: what the change is, which record of a review stands, every check of the
// head against the ones the rules of the branch demand, the files against the
// boundaries of the task, and the verdict (docs/DESIGN.md §6, §7h).
func TestRunReviewOfAChangeThatMayBeMerged(t *testing.T) {
	host := newReviewHost(t)
	host.comment(gate.ApproveOf(reviewHead, 7))
	project := writeConfig(t, reviewConfig)
	var stdout, stderr bytes.Buffer

	code := run([]string{"review", "7", "-config", project}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow review = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	for _, want := range []string{
		"change #7", reviewHead, "main", "naghuale/crewflow",
		"review: approved of " + reviewHead[:8] + " by naghuale",
		"check test: success from github-actions",
		"files: 1, all of them within the boundaries of the task",
		"verdict: ready to merge",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("crewflow review wrote:\n%s\nwant it to mention %q", stdout.String(), want)
		}
	}
	if stderr.Len() != 0 {
		t.Errorf("crewflow review wrote %q to stderr, want nothing", stderr.String())
	}
}

// TestRunReviewAsJSON is the shape the orchestrator reads: the change, the record that
// stands, the checks and the verdict, in fields and not in a report to be read.
func TestRunReviewAsJSON(t *testing.T) {
	host := newReviewHost(t)
	host.comment(gate.ApproveOf(reviewHead, 7))
	project := writeConfig(t, reviewConfig)
	var stdout, stderr bytes.Buffer

	code := run([]string{"review", "7", "-config", project, "-json"}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow review = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	var summary struct {
		Change int    `json:"change"`
		Head   string `json:"head"`
		Task   int    `json:"task"`
		Review *struct {
			Decision string `json:"decision"`
			Commit   string `json:"commit"`
			Author   string `json:"author"`
			Counted  bool   `json:"counted"`
		} `json:"review"`
		Checks []struct {
			Name string `json:"name"`
		} `json:"checks"`
		Verdict gate.Verdict `json:"verdict"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatalf("the answer of crewflow review is not the JSON of a summary: %v\n%s", err, stdout.String())
	}
	if summary.Change != 7 || summary.Head != reviewHead || summary.Task != 7 {
		t.Errorf("the summary is of #%d at %s of task %d, want #7 at %s of task 7",
			summary.Change, summary.Head, summary.Task, reviewHead)
	}
	if summary.Review == nil || summary.Review.Decision != "approved" || !summary.Review.Counted {
		t.Errorf("the summary holds the review %+v, want the approval of the owner to stand", summary.Review)
	}
	if len(summary.Checks) != 1 || summary.Checks[0].Name != "test" {
		t.Errorf("the summary holds the checks %+v, want the one the rules of the branch demand", summary.Checks)
	}
	if !summary.Verdict.Ready {
		t.Errorf("the verdict is %+v, want a change that may be merged", summary.Verdict)
	}
}

// TestRunReviewApproveWritesTheRecordOnTheHead: the record is what the gate counts,
// and it counts only the approval of the head — the whole of it, because a commit
// named by a part of it is a commit nobody approved (docs/DESIGN.md §7h).
func TestRunReviewApproveWritesTheRecordOnTheHead(t *testing.T) {
	host := newReviewHost(t)
	project := writeConfig(t, reviewConfig)
	var stdout, stderr bytes.Buffer

	code := run([]string{"review", "7", "-config", project, "-approve"}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow review -approve = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	if len(host.written) != 1 {
		t.Fatalf("%d records were written under the change, want one", len(host.written))
	}
	written := host.written[0]
	if !strings.HasPrefix(written.body, "REVIEW: APPROVED "+reviewHead) {
		t.Errorf("the record written is %q, want an approval of the whole head %s", written.body, reviewHead)
	}
	if written.number != 7 {
		t.Errorf("the record was written under #%d, want #7", written.number)
	}
	if !strings.Contains(stderr.String(), reviewHead) {
		t.Errorf("crewflow review said %q, want it to name the commit it approved", stderr.String())
	}
}

// TestRunReviewApproveRefusesARedCommit: an approval of a change whose CI is red is a
// name on a comment and not a gate, and the record is not written at all — the reason
// is the one of the gate, and not "there is no approval" (docs/DESIGN.md §7h).
func TestRunReviewApproveRefusesARedCommit(t *testing.T) {
	cases := []struct {
		name  string
		given func(*reviewHost)
		want  string
	}{
		{
			name: "the CI is red",
			given: func(h *reviewHost) {
				h.checks[0].State = forge.CheckFailure
			},
			want: "required-check-failed",
		},
		{
			name: "a file is outside the boundaries of the task",
			given: func(h *reviewHost) {
				h.files = append(h.files, "docs/DESIGN.md")
			},
			want: "out-of-scope",
		},
		{
			name: "the change conflicts with main",
			given: func(h *reviewHost) {
				h.change.Conflicted = true
				h.required, h.checks = nil, nil
			},
			want: "not-fast-forward",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host := newReviewHost(t)
			host.comment(gate.ApproveOf(reviewHead, 7))
			tc.given(host)
			project := writeConfig(t, reviewConfig)
			var stdout, stderr bytes.Buffer

			code := run([]string{"review", "7", "-config", project, "-approve"}, &stdout, &stderr)

			if code != exitFailure {
				t.Errorf("crewflow review -approve = %d, want %d", code, exitFailure)
			}
			if len(host.written) != 0 {
				t.Errorf("a record was written under the change, want nothing: the change may not be approved")
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Errorf("crewflow review said %q, want it to name %q", stderr.String(), tc.want)
			}
		})
	}
}

// TestRunReviewRequestChangesWritesTheFindings: the record asks for changes and holds
// what the person wrote about them, because a request for changes without a reason is
// a request the executor cannot act on. The change is then one that may not be merged,
// and the code of the command says so (§7h).
func TestRunReviewRequestChangesWritesTheFindings(t *testing.T) {
	host := newReviewHost(t)
	project := writeConfig(t, reviewConfig)
	findings := filepath.Join(t.TempDir(), "findings.md")
	if err := writeFileOfTest(findings, "1. the gate has no test for a refused check\n"); err != nil {
		t.Fatalf("write the findings: %v", err)
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"review", "7", "-config", project, "-request-changes", findings}, &stdout, &stderr)

	if code != exitFailure {
		t.Fatalf("crewflow review -request-changes = %d, want %d: the change was sent back (stderr: %q)", code, exitFailure, stderr.String())
	}
	if len(host.written) != 1 {
		t.Fatalf("%d records were written under the change, want one", len(host.written))
	}
	for _, want := range []string{"REVIEW: CHANGES REQUESTED", "the gate has no test for a refused check"} {
		if !strings.Contains(host.written[0].body, want) {
			t.Errorf("the record written is %q, want it to hold %q", host.written[0].body, want)
		}
	}
	if !strings.Contains(stdout.String(), "approval-missing") {
		t.Errorf("crewflow review wrote:\n%s\nwant it to name the reason a change asked for changes may not be merged", stdout.String())
	}
}

// TestRunReviewRefusesAnApprovalOfSomebodyElse is what the gate of §7h protects most
// of all: the executor of a run may write anything under its own change, and none of
// it is an approval. The summary of such a change says who wrote it and that it does
// not count, and the verdict names the reason (docs/DESIGN.md §7h).
func TestRunReviewRefusesAnApprovalOfSomebodyElse(t *testing.T) {
	host := newReviewHost(t)
	host.comment(gate.ApproveOf(reviewHead, 7))
	// A record of the App of the executor of a run: it is written under the change
	// whatever it says, and the gate counts none of it as the word of a person (docs.DESIGN.md §7h, §7i).
	host.comments[0].Author = executorOfTheProject
	project := writeConfig(t, reviewConfig)
	var stdout, stderr bytes.Buffer

	code := run([]string{"review", "7", "-config", project}, &stdout, &stderr)

	if code != exitFailure {
		t.Errorf("crewflow review = %d, want %d", code, exitFailure)
	}
	if !strings.Contains(stdout.String(), "approval-untrusted") {
		t.Errorf("crewflow review wrote:\n%s\nwant it to name approval-untrusted", stdout.String())
	}
	if !strings.Contains(stdout.String(), "crewflow-executor[bot]") {
		t.Errorf("crewflow review wrote:\n%s\nwant it to name who wrote the record", stdout.String())
	}
}

// reviewTask is a whole task of a test, with the boundaries a change of it is checked
// against: the change of the test touches the gate, and the task is about the gate.
const reviewTask = "## Why\n\nThe rules of a review are kept by hand.\n\n" +
	"## What changes\n\ncrewflow says whether a change may be merged.\n\n" +
	"## How to check it yourself\n\n1. Run `crewflow review 7`.\n\n" +
	"## Out of scope\n\nThe merge itself is another command.\n\n" +
	"## Risks and decisions\n\nA refusal names one reason.\n\n" +
	"<details>\n<summary>Technical part for the executor</summary>\n\n" +
	"### Acceptance criteria\n\n- [ ] the gate names the reason\n\n" +
	"### Boundaries\n\n```\ninternal/gate/**\nREADME.md\n```\n\n</details>\n"

// TestRunReviewOfATaskTheOwnerHasToAccept is the step before the merge of a task marked
// `owner-check`: the report says that the result of the task waits for its owner, the
// gate refuses the change until he has taken it in for exactly this head, and the head
// he accepted is the one in the record (docs/DESIGN.md §7f, §7h).
func TestRunReviewOfATaskTheOwnerHasToAccept(t *testing.T) {
	host := newReviewHost(t)
	host.task.Labels = []string{"owner-check"}
	host.comment(gate.ApproveOf(reviewHead, 7))
	project := writeConfig(t, reviewConfig)
	var stdout, stderr bytes.Buffer

	code := run([]string{"review", "7", "-config", project}, &stdout, &stderr)

	if code != exitFailure {
		t.Fatalf("crewflow review = %d, want %d: the result waits for its owner", code, exitFailure)
	}
	for _, want := range []string{
		"owner acceptance: the task is marked `owner-check`, and there is no record of it under the change",
		"verdict: may not be merged, owner-acceptance-missing",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("crewflow review wrote:\n%s\nwant it to mention %q", stdout.String(), want)
		}
	}

	host.comment(gate.AcceptedOf(reviewHead))
	stdout.Reset()

	if code := run([]string{"review", "7", "-config", project}, &stdout, &stderr); code != exitOK {
		t.Fatalf("crewflow review = %d, want %d (stdout: %q)", code, exitOK, stdout.String())
	}
	for _, want := range []string{
		"owner acceptance: ACCEPTED " + reviewHead[:8] + " by naghuale",
		"verdict: ready to merge",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("crewflow review wrote:\n%s\nwant it to mention %q", stdout.String(), want)
		}
	}
}

// TestRunReviewApproveOfATaskTheOwnerHasToAccept: the acceptance is the owner's own act
// and the orchestrator cannot write it, so a verdict that refused the approval for a
// missing acceptance would leave a task marked `owner-check` with no way to start at all
// (docs/DESIGN.md §7h).
//
// The record is written and the command still leaves a code that is not zero: what it
// answers afterwards is whether the change may be merged now, and it may not until the
// owner has accepted the head. What was written is said on stderr all the same, so that
// an orchestrator knows where the task stands.
func TestRunReviewApproveOfATaskTheOwnerHasToAccept(t *testing.T) {
	host := newReviewHost(t)
	host.task.Labels = []string{"owner-check"}
	project := writeConfig(t, reviewConfig)
	var stdout, stderr bytes.Buffer

	code := run([]string{"review", "7", "-config", project, "-approve"}, &stdout, &stderr)

	if len(host.written) != 1 {
		t.Fatalf("%d records were written under the change, want one (stderr: %q)", len(host.written), stderr.String())
	}
	if want := "REVIEW: APPROVED " + reviewHead; !strings.HasPrefix(host.written[0].body, want) {
		t.Errorf("the record written is %q, want it to start with %q", host.written[0].body, want)
	}
	if code != exitFailure {
		t.Errorf("crewflow review -approve = %d, want %d: the result waits for its owner", code, exitFailure)
	}
	if want := "verdict: may not be merged, owner-acceptance-missing"; !strings.Contains(stdout.String(), want) {
		t.Errorf("crewflow review wrote:\n%s\nwant it to name %q", stdout.String(), want)
	}
	if !strings.Contains(stderr.String(), "approved "+reviewHead) {
		t.Errorf("crewflow review said %q, want it to name the commit it approved", stderr.String())
	}
}

// TestRunReviewAsJSONOfATaskTheOwnerHasToAccept is the shape the orchestrator reads:
// whether the result waits for its owner and which record of an acceptance stands, in
// fields and not in a report to be read (docs/DESIGN.md §7h).
func TestRunReviewAsJSONOfATaskTheOwnerHasToAccept(t *testing.T) {
	host := newReviewHost(t)
	host.task.Labels = []string{"owner-check"}
	host.comment(gate.ApproveOf(reviewHead, 7))
	host.comment(gate.AcceptedOf("1111111111111111111111111111111111111111"))
	project := writeConfig(t, reviewConfig)
	var stdout, stderr bytes.Buffer

	code := run([]string{"review", "7", "-config", project, "-json"}, &stdout, &stderr)

	if code != exitFailure {
		t.Fatalf("crewflow review = %d, want %d", code, exitFailure)
	}
	var summary struct {
		AcceptanceRequired bool   `json:"acceptance_required"`
		AcceptanceLabel    string `json:"acceptance_label"`
		OwnerAccept        *struct {
			Commit  string `json:"commit"`
			Author  string `json:"author"`
			Counted bool   `json:"counted"`
		} `json:"owner_accept"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatalf("the answer of crewflow review is not the JSON of a summary: %v\n%s", err, stdout.String())
	}
	if !summary.AcceptanceRequired || summary.AcceptanceLabel != "owner-check" {
		t.Errorf("the summary holds %q as the label of the acceptance, want it to wait for its owner",
			summary.AcceptanceLabel)
	}
	if summary.OwnerAccept == nil || summary.OwnerAccept.Counted || summary.OwnerAccept.Author != "naghuale" {
		t.Errorf("the summary holds the acceptance %+v, want the record of an earlier head shown and not counted",
			summary.OwnerAccept)
	}
}

// TestRunReviewOfAChangeWithNoTask is a change that no run of this machine opened: its
// task is unknown, its boundaries are nobody's to say, and the gate refuses it as
// `out-of-scope` rather than let it through on the strength of a task it cannot find
// (docs/DESIGN.md §7h).
func TestRunReviewOfAChangeWithNoTask(t *testing.T) {
	host := newReviewHost(t)
	host.comment(gate.ApproveOf(reviewHead, 0))
	takeStateOfTask(t)
	project := writeConfig(t, reviewConfig)
	var stdout, stderr bytes.Buffer

	code := run([]string{"review", "7", "-config", project}, &stdout, &stderr)

	if code != exitFailure {
		t.Errorf("crewflow review = %d, want %d", code, exitFailure)
	}
	if !strings.Contains(stdout.String(), "out-of-scope") {
		t.Errorf("crewflow review wrote:\n%s\nwant it to name out-of-scope", stdout.String())
	}
}

// TestRunReviewOfAChangeThatConflicts is the case of PR #19 (29.09.2026): GitHub
// does not run the checks of a change it cannot merge, so the summary names the
// conflict and what to do about it instead of the checks that are never going to come
// (docs/DESIGN.md §7h).
func TestRunReviewOfAChangeThatConflicts(t *testing.T) {
	host := newReviewHost(t)
	host.comment(gate.ApproveOf(reviewHead, 7))
	host.change.Conflicted = true
	host.change.MergeState = "DIRTY"
	host.required, host.checks = nil, nil
	project := writeConfig(t, reviewConfig)
	var stdout, stderr bytes.Buffer

	code := run([]string{"review", "7", "-config", project}, &stdout, &stderr)

	if code != exitFailure {
		t.Errorf("crewflow review = %d, want %d", code, exitFailure)
	}
	for _, want := range []string{"conflicts with main", "rebase", "not-fast-forward"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("crewflow review wrote:\n%s\nwant it to mention %q", stdout.String(), want)
		}
	}
	if strings.Contains(stdout.String(), "required-check-missing") {
		t.Errorf("crewflow review wrote:\n%s\nwant it not to report the checks of a change that never runs them", stdout.String())
	}
}

// TestRunReviewOfARepositoryWithoutRulesOnItsPlan is `crewflow review 69` in
// naghuale/tele on 30.09.2026 (F-043): the repository is private and on the free plan,
// so GitHub has no rules for its branch and says which plan it is about. That answer
// used to be read as a silence and to refuse every change of such a repository with
// `forge-unavailable` — a gate no merge may pass through. The report says where the
// checks of the change come from instead (docs/DESIGN.md §7h, §7k).
func TestRunReviewOfARepositoryWithoutRulesOnItsPlan(t *testing.T) {
	host := newReviewHost(t)
	host.comment(gate.ApproveOf(reviewHead, 7))
	host.required, host.rules = nil, forge.RulesUnavailableOnPlan
	project := writeConfig(t, reviewConfig)
	var stdout, stderr bytes.Buffer

	code := run([]string{"review", "7", "-config", project}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow review = %d, want %d (stdout: %q, stderr: %q)", code, exitOK, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "forge-unavailable") {
		t.Errorf("crewflow review wrote:\n%s\nwant it not to refuse a repository that has no rules on its plan", stdout.String())
	}
	for _, want := range []string{"rules of the branch", "plan of this repository", "crewflow.toml"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("crewflow review wrote:\n%s\nwant it to mention %q", stdout.String(), want)
		}
	}
}

// TestRunReviewOfARepositoryWithoutRulesOnItsPlanAsJSON is the same answer in the
// shape an orchestrator reads: the state of the rules of the branch is a word it may
// compare, and not a line to be read (docs/DESIGN.md §7h, §7k).
func TestRunReviewOfARepositoryWithoutRulesOnItsPlanAsJSON(t *testing.T) {
	host := newReviewHost(t)
	host.comment(gate.ApproveOf(reviewHead, 7))
	host.required, host.rules = nil, forge.RulesUnavailableOnPlan
	project := writeConfig(t, reviewConfig)
	var stdout, stderr bytes.Buffer

	code := run([]string{"review", "7", "-config", project, "-json"}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("crewflow review -json = %d, want %d (stderr: %q)", code, exitOK, stderr.String())
	}
	var summary struct {
		Rules   forge.RuleState `json:"rules"`
		Verdict gate.Verdict    `json:"verdict"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatalf("the answer of crewflow review is not the JSON of a summary: %v\n%s", err, stdout.String())
	}
	if summary.Rules != forge.RulesUnavailableOnPlan {
		t.Errorf("the summary says the rules are %q, want %q", summary.Rules, forge.RulesUnavailableOnPlan)
	}
	if !summary.Verdict.Ready {
		t.Errorf("the verdict is %+v, want a green head of a repository without rules to be enough", summary.Verdict)
	}
}

// TestRunReviewApproveAsAnAccountThatDoesNotCount is the record crewflow must not
// write: the gate counts only the records of the reviewers of the project, and one
// written in the name of anybody else is a comment that says "approved" and approves
// nothing (docs/DESIGN.md §7h).
func TestRunReviewApproveAsAnAccountThatDoesNotCount(t *testing.T) {
	host := newReviewHost(t)
	host.speakAs("somebody-else")
	project := writeConfig(t, reviewConfig)
	var stdout, stderr bytes.Buffer

	code := run([]string{"review", "7", "-config", project, "-approve"}, &stdout, &stderr)

	if code != exitFailure {
		t.Errorf("crewflow review -approve = %d, want %d", code, exitFailure)
	}
	if len(host.written) != 0 {
		t.Error("a record was written in the name of an account the gate does not count")
	}
	for _, want := range []string{"somebody-else", "naghuale"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("crewflow review said %q, want it to name %q", stderr.String(), want)
		}
	}
}

// TestRunReviewApproveWithEmptyFindings: a request for changes with nothing in it is a
// request the executor cannot act on, and crewflow asks for the file with the
// findings rather than writing a record of it (§7h).
func TestRunReviewApproveWithEmptyFindings(t *testing.T) {
	host := newReviewHost(t)
	project := writeConfig(t, reviewConfig)
	findings := filepath.Join(t.TempDir(), "findings.md")
	if err := writeFileOfTest(findings, "   \n"); err != nil {
		t.Fatalf("write the findings: %v", err)
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"review", "7", "-config", project, "-request-changes", findings}, &stdout, &stderr)

	if code != exitFailure {
		t.Errorf("crewflow review = %d, want %d", code, exitFailure)
	}
	if len(host.written) != 0 {
		t.Error("a record was written with no findings in it")
	}
	if !strings.Contains(stderr.String(), "empty") {
		t.Errorf("crewflow review said %q, want it to say that the findings are empty", stderr.String())
	}
}

// TestRunReviewCalledWrong walks the ways a call of the command can be wrong: no change
// at all, a word that is not a number, two decisions at once and an argument that is
// not a flag. Every one of them is a wrong call and not a change request that is not
// there, and every one of them says what the right call is (docs/DESIGN.md §6).
func TestRunReviewCalledWrong(t *testing.T) {
	newReviewHost(t)
	project := writeConfig(t, reviewConfig)
	cases := [][]string{
		{"review"},
		{"review", "-config", project},
		{"review", "seven", "-config", project},
		{"review", "0", "-config", project},
		{"review", "7", "8", "-config", project},
		{"review", "7", "-config", project, "-approve", "-request-changes", "findings.md"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := run(args, &stdout, &stderr)

			if code != exitUsage {
				t.Errorf("run(%v) = %d, want %d (stderr: %q)", args, code, exitUsage, stderr.String())
			}
			if !strings.Contains(stderr.String(), "Usage:") {
				t.Errorf("run(%v) wrote %q to stderr, want the usage", args, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Errorf("run(%v) wrote %q to stdout, want nothing", args, stdout.String())
			}
		})
	}
}

// A record of a review a host of a test was asked to write.
type record struct {
	number int
	body   string
}

// reviewHost is the host of a project of a test as a review sees it: the change, what
// is written under it, the files it touches, the checks of its head and the rules of
// the branch that demand them — and every record of a review the command asked it to
// write. Nothing of it is a network and nothing of it is a repository: no test of the
// command reaches GitHub or a checkout of the person who runs it (docs/DESIGN.md §7h).
type reviewHost struct {
	task     forge.Task
	change   forge.ChangeRequest
	comments []forge.Comment
	files    []string
	checks   []forge.CheckRun
	required []forge.RequiredCheck
	// rules is what the host says about the rules of the branch themselves, and
	// the state of them is what a repository on the free plan is judged by (§7h, §7k).
	rules   forge.RuleState
	written []record
	// environment is what every command of git of a review or of a merge was started
	// with, kept so that a test can see the settings of the account the push was made
	// as (§7i).
	environment []string
	// signedIn is the account a record of a review is written in, and the owner of
	// the repository unless a case says otherwise.
	signedIn string
	// orchestrator is the account the orchestrator of a project of a test works as:
	// empty is the mode of the shared login, and a case that sets it is a project
	// whose orchestrator is an account of the host of its own (§7i).
	orchestrator string
	// orchestratorSubject is that account as the host keeps it — the App of the
	// orchestrator with the number of that App — and it is what the gate counts a record
	// of it by (docs.DESIGN.md §7h, §7i).
	orchestratorSubject forge.Subject
	// subjects are the accounts the host of the test holds by the login the file of the
	// test names them with: the owner of the repository and whoever else a case asks for,
	// because a list of the file is a list of logins and each of them is asked of the
	// host once (docs.DESIGN.md §5, §7i).
	subjects map[string]forge.Subject
}

// newReviewHost is a project of a test with a change that is green, inside the
// boundaries of its task and approved by the owner of the repository, with the git of
// a test in place: a review is asked of git whether the default branch is behind the
// head and whether the approved commit is in the history, and the answers of a test
// are yes to both.
func newReviewHost(t *testing.T) *reviewHost {
	t.Helper()
	h := &reviewHost{
		signedIn: "naghuale",
		subjects: map[string]forge.Subject{ownerOfTheProject.Login: ownerOfTheProject},
		task: forge.Task{
			Number: 7,
			Title:  "the gate in code",
			Body:   reviewTask,
			State:  "open",
		},
		change: changeOfTest(),
		files:  []string{"internal/gate/gate.go"},
		checks: []forge.CheckRun{{Name: "test", State: forge.CheckSuccess, App: "github-actions", SHA: reviewHead}},
		required: []forge.RequiredCheck{
			{Name: "test", App: "github-actions"},
		},
		rules: forge.RulesNamed,
	}
	h.use(t)
	keepStateOfTask(t)
	return h
}

// stateOfTask is what crewflow kept of the run of a task: the run opened the change
// request of its branch, and that is how a review finds which task a change is of on
// this machine, without asking the host anything (§7).
func stateOfTask(t *testing.T, task, change int) {
	t.Helper()
	started := time.Date(2026, time.September, 29, 8, 0, 0, 0, time.UTC)
	state := taskrun.State{
		Number:   task,
		Title:    "the gate in code",
		Branch:   "crewflow/7-review",
		Worktree: "/w/7",
		Profile:  "opencode",
		Change: &taskrun.Change{
			Number: change,
			URL:    "https://github.com/naghuale/crewflow/pull/7",
		},
		Attempts: []taskrun.Attempt{{
			Number:    1,
			Executor:  "opencode",
			StartedAt: started,
			EndedAt:   started.Add(42 * time.Minute),
			Journal:   "/w/7/journal.jsonl",
			Outcome:   taskrun.ChangeRequestOpened,
			Identity:  taskrun.Identity{Mode: "owner"},
		}},
	}
	keepsState(t, statePathOfTask(task), state)
}

// keepStateOfTask leaves the state of the task of a test behind, as a run of it would.
func keepStateOfTask(t *testing.T) { stateOfTask(t, 7, 7) }

// takeStateOfTask takes the state away, as a change nobody ran here never had one.
func takeStateOfTask(t *testing.T) {
	t.Helper()
	if err := os.Remove(statePathOfTask(7)); err != nil {
		t.Fatalf("take the state of the task away: %v", err)
	}
}

// statePathOfTask is where the state of a task is kept: under the root of crewflow,
// by the project and the task (§7).
func statePathOfTask(task int) string {
	home, err := config.Config{}.ExpandPath(crewflowHome)
	if err != nil {
		panic("the home of crewflow: " + err.Error())
	}
	return filepath.Join(home, "state", "naghuale-crewflow", strconv.Itoa(task)+".json")
}

// comment is a record of a review under the change, by the owner of the repository, as
// the host keeps that account: the gate counts the record by the number under the name and
// never by the name (docs.DESIGN.md §7h, §7i).
func (h *reviewHost) comment(body string) {
	h.comments = append(h.comments, forge.Comment{
		Author:    ownerOfTheProject,
		Body:      body,
		CreatedAt: time.Date(2026, time.September, 29, 9, 0, 0, 0, time.UTC),
	})
}

// use makes the review command run on this host and against this git, and puts the
// machine back when the test is over.
func (h *reviewHost) use(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		reviewRoles = roles.AsOrchestrator
		gitOf = gitIn
	})
	reviewRoles = func(config.Config, forge.Env) (forge.Set, error) {
		return forge.Set{Forge: h, Tracker: h, CI: h}, nil
	}
	gitOf = h.gitIn
}

// gitIn is the git of this host, and the environment every command of it is started
// with: the push of a merge and the fetch of a review are the commands whose account
// matters, and a test of the separate mode has to see what they were started with
// (docs/DESIGN.md §7i).
func (h *reviewHost) gitIn(_ *secret.Out, environment []string) merge.Runner {
	h.environment = environment
	return h.git
}

// git is the history of a change of a test: the head is where the host says it is, the
// default branch of the project is behind it, and the approved commit is in it. Every
// other question is not asked, and a question that is asked without an answer here is
// a question the test of the command is not about.
func (h *reviewHost) git(_ context.Context, name string, args []string, _ string) ([]byte, []byte, int, error) {
	switch {
	case len(args) > 0 && args[0] == "fetch":
		return nil, nil, 0, nil
	case len(args) > 0 && args[0] == "rev-parse":
		return []byte(h.change.HeadSHA + "\n"), nil, 0, nil
	case len(args) > 1 && args[0] == "merge-base" && args[1] == "--is-ancestor":
		return nil, nil, 0, nil
	default:
		return nil, nil, 1, fmt.Errorf("git %v: a review of a test asks git nothing else", args)
	}
}

func (h *reviewHost) Task(context.Context, int) (forge.Task, error) { return h.task, nil }

func (h *reviewHost) ChangeRequest(context.Context, int) (forge.ChangeRequest, error) {
	return h.change, nil
}

func (h *reviewHost) FindChangeRequest(context.Context, string) (forge.ChangeRequest, bool, error) {
	return h.change, true, nil
}

func (h *reviewHost) Comments(context.Context, int) ([]forge.Comment, error) { return h.comments, nil }

func (h *reviewHost) ChangedFiles(context.Context, int) ([]string, error) { return h.files, nil }

func (h *reviewHost) HeadRef(number int) string {
	return fmt.Sprintf("refs/pull/%d/head", number)
}

func (h *reviewHost) Checks(context.Context, string) ([]forge.CheckRun, error) { return h.checks, nil }

func (h *reviewHost) RequiredChecks(context.Context) ([]forge.RequiredCheck, error) {
	return h.required, nil
}

// Rules is what the rules of the branch demand and what the host said about the rules
// themselves: a repository on the free plan has none to demand anything with, and that
// is a fact about the repository rather than a silence of the host (§7h, §7k).
func (h *reviewHost) Rules(context.Context) (forge.BranchRules, error) {
	return forge.BranchRules{Required: h.required, State: h.rules}, nil
}

func (h *reviewHost) Status(context.Context, string) (forge.CheckState, error) {
	return forge.CheckSuccess, nil
}

func (h *reviewHost) Doctor(context.Context) []forge.Check { return nil }

// WriteComment writes the record of a review and keeps it, so that a test can read
// what was written under the change and where (docs/DESIGN.md §7h).
// SignedIn is the account a record of a review of a test is written in, which is the
// account gh is signed in as: the file of the project names it as its reviewer, and a
// record written in the name of anybody else would not be an approval (§7h).
func (h *reviewHost) SignedIn(context.Context) (string, error) {
	if h.orchestrator != "" {
		return h.orchestrator, nil
	}
	return h.signedIn, nil
}

// SigningAs is the subject the host speaks as: the App of the orchestrator in the mode of
// a separate login and the account gh is signed in as otherwise, each with the number the
// gate counts a record of it by (docs/DESIGN.md §7h, §7i).
func (h *reviewHost) SigningAs(ctx context.Context) (forge.Subject, error) {
	if h.orchestrator != "" {
		return h.orchestratorSubject, nil
	}
	return h.Subject(ctx, h.signedIn)
}

// speakAs makes the host speak as another account: the login it is signed in as, and the
// account of the host behind that login, because a login is what a host is asked about and
// a record of a review is counted by the number under it (docs.DESIGN.md §7i).
func (h *reviewHost) speakAs(login string) {
	h.signedIn = login
	h.subjects[login] = forge.Subject{Kind: forge.KindUser, ID: 4242, Login: login}
}

// Subject is the account of the host the login names: the owner of the repository and the
// account of the App of the orchestrator are the only accounts a host of a test has, and
// both are named by the login the file of the test writes them with.
func (h *reviewHost) Subject(_ context.Context, login string) (forge.Subject, error) {
	if h.orchestrator != "" && login == h.orchestrator {
		return h.orchestratorSubject, nil
	}
	if subject, known := h.subjects[login]; known {
		return subject, nil
	}
	return forge.Subject{}, fmt.Errorf("the account %q is not on this host", login)
}

func (h *reviewHost) WriteComment(_ context.Context, number int, body string) error {
	h.written = append(h.written, record{number: number, body: body})
	return nil
}

// A host of a test is everything a review asks of a host of a project: the change, the
// records under it, the files it touches, the checks of its head, the ref of the head
// and the place a record of a review is written to.
var (
	_ forge.Tracker       = (*reviewHost)(nil)
	_ forge.Forge         = (*reviewHost)(nil)
	_ forge.CI            = (*reviewHost)(nil)
	_ forge.CheckLister   = (*reviewHost)(nil)
	_ forge.RuleLister    = (*reviewHost)(nil)
	_ forge.FileLister    = (*reviewHost)(nil)
	_ forge.HeadRef       = (*reviewHost)(nil)
	_ forge.CommentWriter = (*reviewHost)(nil)
	_ forge.SignedIn      = (*reviewHost)(nil)
	_ forge.Naming        = (*reviewHost)(nil)
	_ forge.Signer        = (*reviewHost)(nil)
)

// writeFileOfTest writes a file of a test and says so.
func writeFileOfTest(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}
