package gate

import (
	"fmt"
	"io"
	"slices"

	"github.com/naghuale/crewflow/internal/forge"
)

// Summary is what `crewflow review` shows the orchestrator: the change and where it
// stands, the record that counts, the checks the head has against the ones the
// rules of its branch demand, the files it touches against the boundaries of its
// task, and the verdict of the gate (docs/DESIGN.md §6, §7h).
//
// It is made of the facts and the verdict and nothing else, so that what a person
// reads and what a program reads are the same thing, and neither of them is a
// second opinion of the gate.
type Summary struct {
	// Change and URL are the change request as the host holds it, Task the task it
	// is of, and Head the commit everything below is about.
	Change int    `json:"change"`
	URL    string `json:"url,omitempty"`
	Task   int    `json:"task,omitempty"`
	Head   string `json:"head"`
	// Base is the branch the change is meant for, Repository the repository it is
	// in, and State whether it is open and whether it is a draft.
	Base       string `json:"base,omitempty"`
	Repository string `json:"repository,omitempty"`
	State      string `json:"state"`
	// Conflicted says that the change cannot be merged into its branch as it
	// stands, which is why no check of it will ever come, and MergeState is what the
	// host says about it in its own words: "DIRTY" is the conflict, and "UNKNOWN"
	// is a host that has not worked it out yet, which a person has to see rather
	// than be told about it by a check that has not run.
	Conflicted bool   `json:"conflicted,omitempty"`
	MergeState string `json:"merge_state,omitempty"`
	// Reviewers are the accounts whose record counts, and Review the last record
	// of a review among those: what was decided, about which commit, and by whom.
	Reviewers []string   `json:"reviewers,omitempty"`
	Review    *DecidedOn `json:"review,omitempty"`
	// Checks are the required checks of the head, one line each, whether the head
	// has them or not: a check that is not there is a line of a report too, and
	// the one that stops the merge is in the verdict.
	Checks []CheckLine `json:"checks,omitempty"`
	// Rules is what the host said about the rules of the branch of the project:
	// forge.RulesNamed when it named the checks they demand, and
	// forge.RulesUnavailableOnPlan when the plan of the repository has none — then
	// the checks of a change are the ones crewflow.toml asks for, and a person has
	// to see that rather than read "no checks" (docs/DESIGN.md §7h, §7k).
	Rules forge.RuleState `json:"rules,omitempty"`
	// Files is how many files the change touches, and Outside the ones the task
	// was not to touch, with whoever took them into their own hands.
	Files      int      `json:"files"`
	Outside    []string `json:"outside,omitempty"`
	AcceptedBy string   `json:"scope_accepted_by,omitempty"`
	// Verdict is the answer of the gate about all of it.
	Verdict Verdict `json:"verdict"`
}

// DecidedOn is one record of a review as a report shows it: what was decided, about
// which commit, by whom, when, and whether it has been edited since — an edited
// record does not count, and a person has to see that it was edited.
type DecidedOn struct {
	Decision  Decision `json:"decision"`
	Commit    string   `json:"commit,omitempty"`
	Author    string   `json:"author"`
	CreatedAt string   `json:"created_at"`
	Edited    bool     `json:"edited,omitempty"`
	Counted   bool     `json:"counted"`
}

// CheckLine is one required check of a head as a report shows it: the name it is
// known under, how it stands, the app that reported it, the commit it is about, and
// what the rules of the branch expect of it.
type CheckLine struct {
	Name  string           `json:"name"`
	State forge.CheckState `json:"state"`
	App   string           `json:"app,omitempty"`
	SHA   string           `json:"sha,omitempty"`
	// Missing says that the rules of the branch demand this check and the head
	// has none under that name.
	Missing bool `json:"missing,omitempty"`
	// Want is the app the gate takes the check from, empty when the host expects
	// none.
	Want string `json:"want,omitempty"`
}

// Summarize is the report of a change: the facts of it and the verdict of the gate,
// with nothing added on top. The checks are listed in the order the rules of the
// branch demand them and what the head does not have is a line of its own, so that
// the person reading the report sees the same list a merge would be refused on.
func Summarize(f Facts, v Verdict) Summary {
	s := Summary{
		Change:     f.Number,
		URL:        f.URL,
		Task:       f.Task,
		Head:       f.Head,
		Base:       f.TargetBranch,
		Repository: f.Repository,
		State:      stateOf(f),
		Conflicted: f.Conflicted,
		MergeState: f.MergeState,
		Reviewers:  f.reviewers(),
		Files:      len(f.Files),
		Rules:      f.Rules,
		Verdict:    v,
	}
	// The record that stands is the one the gate counts, and the last record of
	// anybody is shown when none of them counts: an executor that wrote "approved"
	// under its own change is exactly what a person has to see in a report of it
	// (docs/DESIGN.md §7h).
	if record, counted := f.Counted(); counted {
		s.Review = decidedOn(record, true)
	} else if last, ok := lastReview(f); ok {
		s.Review = decidedOn(last, false)
	}
	s.Checks = checkLines(f)
	// The files outside the boundaries are a fact of the report even where the
	// boundaries are unreadable: a summary that cannot say them says nothing, and the
	// verdict below says why the change is not in order.
	if outside, err := f.Outside(); err == nil && len(outside) > 0 {
		s.Outside = outside
	}
	if record, ok := f.accepted(); ok {
		s.AcceptedBy = record.Author
	}
	return s
}

// decidedOn is one record of a review as a report shows it: what was decided, about
// which commit, by whom and when — and whether the gate counts it.
func decidedOn(record Review, counted bool) *DecidedOn {
	decided, _ := Parse(record.Body)
	return &DecidedOn{
		Decision:  decided.Decision,
		Commit:    decided.Commit,
		Author:    record.Author,
		CreatedAt: record.CreatedAt.UTC().Format(timeLayout),
		Edited:    record.Edited,
		Counted:   counted,
	}
}

// timeLayout is how a report writes a moment: RFC 3339, the way the host writes it
// and the way a program reads it back.
const timeLayout = "2006-01-02T15:04:05Z07:00"

// stateOf is the state of the change as a report names it, and the two words a
// person needs before anything else: whether it is open at all, and whether it is
// still a draft.
func stateOf(f Facts) string {
	if !f.Open {
		return "closed"
	}
	if f.Draft {
		return "draft"
	}
	return "open"
}

// checkLines is every required check of the head, in the order the rules of the
// branch name them, with the check the head has under that name — or with nothing
// there at all, which is a line of a report like any other.
func checkLines(f Facts) []CheckLine {
	lines := make([]CheckLine, 0, len(f.Required))
	seen := make(map[string]struct{}, len(f.Required))
	for _, want := range f.Required {
		if _, duplicate := seen[want.Name]; duplicate {
			continue
		}
		seen[want.Name] = struct{}{}
		line := CheckLine{Name: want.Name, Want: want.App}
		if check, ok := f.checkOf(want.Name); ok {
			line.State, line.App, line.SHA = check.State, check.App, check.SHA
		} else {
			line.State, line.Missing = forge.CheckNone, true
		}
		lines = append(lines, line)
	}
	// The checks the head has that the rules of the branch say nothing about are
	// shown as well, in the order the host gave them: a report that hid them would
	// hide the very thing a person is to look at when a check is green for the
	// wrong reason.
	for _, check := range f.Checks {
		if !slices.ContainsFunc(lines, func(line CheckLine) bool { return line.Name == check.Name }) {
			lines = append(lines, CheckLine{Name: check.Name, State: check.State, App: check.App, SHA: check.SHA})
		}
	}
	return lines
}

// lastReview is the last record of a review of anybody, and whether there is one.
func lastReview(f Facts) (Review, bool) {
	if len(f.Reviews) == 0 {
		return Review{}, false
	}
	return f.Reviews[len(f.Reviews)-1], true
}

// Write is the report as a person reads it: what the change is, the record that
// counts, every required check on its own line, the files against the boundaries of
// the task, and the verdict last, because it is the one line everybody came for.
//
// What the gate did not read of a change that is not open is not in the report: a
// summary that said "no checks" about a change that was merged would be saying
// something crewflow never found out, and the verdict below already says why the
// change cannot be merged.
func (s Summary) Write(out io.Writer) {
	// Nothing at all was gathered — the host of the project could not even be asked
	// for the change — and a report of empty lines above a refusal says less than the
	// refusal itself.
	if s.Change == 0 {
		s.writeVerdict(out)
		return
	}
	fmt.Fprintf(out, "change #%d", s.Change)
	if s.URL != "" {
		fmt.Fprintf(out, " %s", s.URL)
	}
	fmt.Fprintf(out, "\n  task: %s\n", number(s.Task))
	fmt.Fprintf(out, "  head: %s\n", s.Head)
	fmt.Fprintf(out, "  base: %s of %s (%s)\n", s.Base, s.Repository, s.State)
	fmt.Fprintf(out, "  reviewers: %s\n", listed(s.Reviewers))
	s.writeReview(out)
	if s.State == "open" {
		s.writeChecks(out)
		s.writeFiles(out)
	}
	s.writeVerdict(out)
}

// writeReview is the last record of a review and whether the gate counts it. A record
// of somebody it does not count is said so on the line and not in the verdict only: a
// person reading a report has to know that the "approved" in it is not an approval.
func (s Summary) writeReview(out io.Writer) {
	switch {
	case s.Review == nil:
		fmt.Fprintf(out, "  review: no record of a review under the change\n")
	case s.Review.Counted:
		fmt.Fprintf(out, "  review: %s of %s by %s at %s\n",
			s.Review.Decision, shortCommit(s.Review.Commit), s.Review.Author, s.Review.CreatedAt)
	default:
		why := "not one of the reviewers of the project"
		if s.Review.Edited {
			why = "edited after it was published"
		}
		fmt.Fprintf(out, "  review: %s of %s by %s at %s, and it does not count: %s\n",
			s.Review.Decision, shortCommit(s.Review.Commit), s.Review.Author, s.Review.CreatedAt, why)
	}
}

// writeChecks is every required check of the head on a line of its own, and the reason
// the host is still working out whether the change can be merged: a check that is not
// there is a line of a report like any other, and the one that stops the merge is in
// the verdict.
func (s Summary) writeChecks(out io.Writer) {
	// The plan of the repository has no rules to demand checks with, and a report
	// that said "the project asks for none" where it asks for the checks of the head
	// would be saying something the host never said (docs/DESIGN.md §7h, §7k).
	if s.Rules == forge.RulesUnavailableOnPlan {
		fmt.Fprintf(out, "  rules of the branch: not on the plan of this repository, so the checks are the ones crewflow.toml asks for\n")
	}
	switch {
	case s.Conflicted:
		fmt.Fprintf(out, "  the change conflicts with %s: CI will not run for it, rebase the branch\n", s.Base)
	case s.MergeState == "UNKNOWN":
		fmt.Fprintf(out, "  the host has not said yet whether the change can be merged into %s: read the checks with care\n", s.Base)
	}
	if len(s.Checks) == 0 {
		fmt.Fprintf(out, "  checks: the project asks for none\n")
		return
	}
	for _, check := range s.Checks {
		fmt.Fprintf(out, "  check %s\n", check.String())
	}
}

// writeFiles is the work of the change against the boundaries of its task, and whoever
// took a file outside them into their own hands.
func (s Summary) writeFiles(out io.Writer) {
	switch {
	case s.AcceptedBy != "":
		fmt.Fprintf(out, "  files: %d, the ones outside the boundaries taken by %s\n", s.Files, s.AcceptedBy)
	case len(s.Outside) > 0:
		fmt.Fprintf(out, "  files: %d, outside the boundaries: %s\n", s.Files, listed(s.Outside))
	case s.Files > 0:
		fmt.Fprintf(out, "  files: %d, all of them within the boundaries of the task\n", s.Files)
	default:
		fmt.Fprintf(out, "  files: none\n")
	}
}

// writeVerdict is the answer of the gate, and what a person is to do about it.
func (s Summary) writeVerdict(out io.Writer) {
	if s.Verdict.Ready {
		fmt.Fprintf(out, "verdict: %s\n", s.Verdict)
		return
	}
	fmt.Fprintf(out, "verdict: may not be merged, %s\n  %s\n", s.Verdict.Reason, s.Verdict.Detail)
}

// String is one line of a report about a required check, and a check the head does
// not have is named as what it is.
func (c CheckLine) String() string {
	if c.Missing {
		return fmt.Sprintf("%s: not run at all, and the rules of the branch demand it", c.Name)
	}
	line := fmt.Sprintf("%s: %s", c.Name, c.State)
	if c.App != "" {
		line += " from " + c.App
	} else {
		line += " from the API of statuses"
	}
	if c.SHA != "" {
		line += " for " + shortCommit(c.SHA)
	}
	return line
}

// number is a number of a task as a report says it, and a task crewflow does not
// know is said so rather than shown as a zero.
func number(task int) string {
	if task <= 0 {
		return "crewflow does not know which task this change is of"
	}
	return fmt.Sprintf("#%d", task)
}

// shortCommit is a commit as a report names it: the first eight letters are what a
// person recognizes, and the whole of it is in the line of the head above.
func shortCommit(commit string) string {
	if len(commit) <= 8 {
		return commit
	}
	return commit[:8]
}
