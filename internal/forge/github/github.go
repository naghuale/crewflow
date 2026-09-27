// Package github reads the tasks, the change requests and the check runs of a
// project on GitHub through gh. It only reads: this task changes nothing on
// GitHub, and it never sees a token, because gh keeps it in the keyring and
// crewflow uses it without looking (docs/DESIGN.md §7e, §7g).
//
// gh is started with a closed stdin and the host of the project in the
// environment, so that the same adapter serves github.com and a server of the
// project.
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/naghuale/crewflow/internal/forge"
)

// program is the tool of this adapter: the official client of GitHub, which
// holds the login and the token.
const program = "gh"

// The fields the adapter asks gh for, and nothing more: a field crewflow does not
// read is a field whose shape may change without anyone noticing.
const (
	taskFields   = "number,title,body,labels,state,url"
	changeFields = "number,url,headRefName,headRefOid,baseRefName,state,body"
)

// completed is what GitHub calls a check run that has ended. GitHub writes its
// states in capitals, crewflow writes its own in lower case, and the state() of
// this package is where the two meet (docs/DESIGN.md §4).
const completed = "completed"

// failingConclusions are the conclusions of a check that did not pass. The rest —
// success, neutral, skipped — are ways of saying that the check is not against
// this commit, which is what GitHub itself counts as a passed required check: a
// workflow with a conditional job would be red for ever otherwise.
var failingConclusions = []string{"failure", "cancelled", "timed_out", "action_required", "stale"}

// checkRunsPerPage is how many checks crewflow asks for at a time: the API gives
// 30 unless it is asked for more, and 100 is the most it will give. It is a
// variable so that a test does not have to hold a hundred runs to see the page
// after the first one.
var checkRunsPerPage = 100

// Adapter is GitHub as the three roles of §7g at once, which is the default: the
// tasks are the issues of the host and the checks are the workflows of the host.
type Adapter struct {
	// repo is the repository as "owner/name", the way every gh command of a
	// project is told which repository it is about.
	repo string
	// host is the server of the project, empty for github.com.
	host string
	// env is the machine the adapter talks to.
	env forge.Env
}

// The adapter is all three roles of the core at once, and each of them may check
// the machine: without a compile error here, a GitHub that cannot be read would
// pass for a host.
var (
	_ forge.Tracker = (*Adapter)(nil)
	_ forge.Forge   = (*Adapter)(nil)
	_ forge.CI      = (*Adapter)(nil)
)

// New returns the adapter of the repository, which is all it needs to know: the
// host of the project is empty for github.com.
func New(repo, host string, env forge.Env) *Adapter {
	return &Adapter{repo: repo, host: host, env: env}
}

// Task returns the issue with the number, as a task of the project: the body a
// person wrote, the labels crewflow decides a task by, and the state, which says
// whether anyone has done it already.
func (a *Adapter) Task(ctx context.Context, number int) (forge.Task, error) {
	out, err := a.json(ctx, "issue", "view", strconv.Itoa(number), "-R", a.repo, "--json", taskFields)
	if err != nil {
		return forge.Task{}, err
	}
	var issue issueJSON
	if err := decode(out, &issue); err != nil {
		return forge.Task{}, err
	}
	return issue.task(), nil
}

// FindChangeRequest returns the request of the branch, and whether there is one:
// gh lists at most the first one, and a branch without a request is a run that
// has not pushed one yet, which is not a failure of anything.
func (a *Adapter) FindChangeRequest(ctx context.Context, branch string) (forge.ChangeRequest, bool, error) {
	out, err := a.json(ctx, "pr", "list", "-R", a.repo,
		"--head", branch, "--state", "all", "--json", changeFields, "--limit", "1")
	if err != nil {
		return forge.ChangeRequest{}, false, err
	}
	var listed []changeJSON
	if err := decode(out, &listed); err != nil {
		return forge.ChangeRequest{}, false, err
	}
	if len(listed) == 0 {
		return forge.ChangeRequest{}, false, nil
	}
	return listed[0].changeRequest(), true, nil
}

// ChangeRequest returns the request with the number: the head it stands on is
// what a review is about and what a merge may touch (docs/DESIGN.md §8).
func (a *Adapter) ChangeRequest(ctx context.Context, number int) (forge.ChangeRequest, error) {
	out, err := a.json(ctx, "pr", "view", strconv.Itoa(number), "-R", a.repo, "--json", changeFields)
	if err != nil {
		return forge.ChangeRequest{}, err
	}
	var change changeJSON
	if err := decode(out, &change); err != nil {
		return forge.ChangeRequest{}, err
	}
	return change.changeRequest(), nil
}

// Comments returns what was written under the request, in the order it was
// written: the review of a run is read as a conversation, and a decision comes
// after the reasons for it.
func (a *Adapter) Comments(ctx context.Context, number int) ([]forge.Comment, error) {
	out, err := a.json(ctx, "pr", "view", strconv.Itoa(number), "-R", a.repo, "--json", "comments")
	if err != nil {
		return nil, err
	}
	var answer struct {
		Comments []commentJSON `json:"comments"`
	}
	if err := decode(out, &answer); err != nil {
		return nil, err
	}
	comments := make([]forge.Comment, 0, len(answer.Comments))
	for _, comment := range answer.Comments {
		created, err := time.Parse(time.RFC3339, comment.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("comment of %s at %q: %w", comment.Author.Login, comment.CreatedAt, err)
		}
		comments = append(comments, forge.Comment{
			Author:    comment.Author.Login,
			Body:      comment.Body,
			CreatedAt: created.UTC(),
		})
	}
	return comments, nil
}

// Status returns how the check runs of the commit stand. A commit with a check
// that has not finished is a commit crewflow waits for, whatever the others say;
// a commit whose checks have all finished is green unless one of them ended in
// a conclusion that says the check did not pass.
func (a *Adapter) Status(ctx context.Context, sha string) (forge.CheckState, error) {
	runs, err := a.checkRuns(ctx, sha)
	if err != nil {
		return forge.CheckNone, err
	}
	return checkState(runs), nil
}

// checkRuns reads the checks of the commit, page by page, and puts them together:
// a commit with many checks is answered with 30 of them at a time, and a red check
// on the second page is as red as one on the first.
func (a *Adapter) checkRuns(ctx context.Context, sha string) ([]checkRun, error) {
	endpoint := "repos/" + a.repo + "/commits/" + sha + "/check-runs"
	var runs []checkRun
	for page := 1; ; page++ {
		got, err := a.checkRunsOfPage(ctx, endpoint, page)
		if err != nil {
			return nil, err
		}
		runs = append(runs, got.CheckRuns...)
		// The total is the number of checks of the commit, not of the page, and
		// a page that came back empty is the end of them whatever it says.
		if len(runs) >= got.TotalCount || len(got.CheckRuns) == 0 {
			return runs, nil
		}
	}
}

// checkRunsOfPage is one page of the answer of the API about the checks of a
// commit, asked for with the biggest page there is.
func (a *Adapter) checkRunsOfPage(ctx context.Context, endpoint string, page int) (checkRunsPage, error) {
	path := fmt.Sprintf("%s?per_page=%d&page=%d", endpoint, checkRunsPerPage, page)
	out, err := a.json(ctx, "api", path)
	if err != nil {
		return checkRunsPage{}, err
	}
	var runs checkRunsPage
	if err := decode(out, &runs); err != nil {
		return checkRunsPage{}, err
	}
	return runs, nil
}

// checkRunsPage is one answer of the API about the checks of a commit: how many
// there are and how many of them are on this page.
type checkRunsPage struct {
	TotalCount int        `json:"total_count"`
	CheckRuns  []checkRun `json:"check_runs"`
}

// checkRun is one check of a commit, as far as its state is concerned: a check of
// GitHub is either still running or ended with a conclusion.
type checkRun struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

// checkState is the state of a commit out of its check runs: none, pending while
// anything has not ended, and then red for a check that did not pass.
func checkState(runs []checkRun) forge.CheckState {
	if len(runs) == 0 {
		return forge.CheckNone
	}
	failed := false
	for _, run := range runs {
		if run.Status != completed {
			return forge.CheckPending
		}
		if slices.Contains(failingConclusions, run.Conclusion) {
			failed = true
		}
	}
	if failed {
		return forge.CheckFailure
	}
	return forge.CheckSuccess
}

// issueJSON is the answer of "gh issue view", as far as a task needs it: the
// labels are objects in the answer of gh and the words of them are what a task is
// marked by.
type issueJSON struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	State string `json:"state"`
	URL   string `json:"url"`
}

// task is the issue as a task of the project.
func (i issueJSON) task() forge.Task {
	labels := make([]string, 0, len(i.Labels))
	for _, label := range i.Labels {
		labels = append(labels, label.Name)
	}
	return forge.Task{
		Number: i.Number,
		Title:  i.Title,
		Body:   i.Body,
		Labels: labels,
		State:  state(i.State),
		URL:    i.URL,
	}
}

// changeJSON is the answer of "gh pr list" and of "gh pr view", which ask for the
// same fields of a request.
type changeJSON struct {
	Number     int    `json:"number"`
	URL        string `json:"url"`
	HeadBranch string `json:"headRefName"`
	HeadSHA    string `json:"headRefOid"`
	BaseBranch string `json:"baseRefName"`
	State      string `json:"state"`
	Body       string `json:"body"`
}

// changeRequest is the request as the core knows it.
func (c changeJSON) changeRequest() forge.ChangeRequest {
	return forge.ChangeRequest{
		Number:     c.Number,
		URL:        c.URL,
		HeadBranch: c.HeadBranch,
		HeadSHA:    c.HeadSHA,
		BaseBranch: c.BaseBranch,
		State:      state(c.State),
		Body:       c.Body,
	}
}

// commentJSON is one comment of the answer of "gh pr view --json comments".
type commentJSON struct {
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
}

// state is the state of a task or a request as the core writes it: in one case,
// whatever case the system of the project writes it in.
func state(from string) string {
	return strings.ToLower(from)
}

// json starts gh with the arguments, in the environment of the project, and
// returns what it wrote. Every way gh can say no is an error with the command in
// it, because a person who is told what to run by hand sees what crewflow saw.
func (a *Adapter) json(ctx context.Context, args ...string) ([]byte, error) {
	command := append([]string{}, args...)
	stdout, stderr, code, err := a.env.Run(ctx, program, command, "", a.environment())
	if err != nil {
		return nil, fmt.Errorf("gh %s: %w", strings.Join(command, " "), err)
	}
	if code != 0 {
		return nil, fmt.Errorf("gh %s: exited with %d: %s",
			strings.Join(command, " "), code, firstLine(stderr, stdout))
	}
	if len(strings.TrimSpace(string(stdout))) == 0 {
		return nil, fmt.Errorf("gh %s: no JSON in the answer, gh wrote nothing", strings.Join(command, " "))
	}
	return stdout, nil
}

// environment is what the project adds to the environment of gh: its own server,
// and nothing when it is on github.com.
func (a *Adapter) environment() []string {
	if a.host == "" {
		return nil
	}
	return []string{"GH_HOST=" + a.host}
}

// decode is json.Unmarshal with the command in the error, because a JSON of a
// shape nobody expected is a fact about gh, not about the task.
func decode(out []byte, into any) error {
	if err := json.Unmarshal(out, into); err != nil {
		return fmt.Errorf("gh wrote JSON of an unknown shape: %w", err)
	}
	return nil
}

// firstLine is the first line of what a program said, which is what a report
// shows: the whole of it may be long, and a report is read by people.
func firstLine(outputs ...[]byte) string {
	for _, output := range outputs {
		for line := range strings.Lines(string(output)) {
			if line = strings.TrimSpace(line); line != "" {
				return line
			}
		}
	}
	return ""
}

// accountPattern finds the account in the answer of "gh auth status". The whole
// answer is never shown: it holds the token, and a report is read by people and
// pasted into issues (docs/DESIGN.md §7e).
var accountPattern = regexp.MustCompile(`account (\S+)`)
