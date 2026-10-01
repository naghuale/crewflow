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
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/forge/github/app"
)

// program is the tool of this adapter: the official client of GitHub, which
// holds the login and the token.
const program = "gh"

// The fields the adapter asks gh for, and nothing more: a field crewflow does not
// read is a field whose shape may change without anyone noticing.
const (
	taskFields   = "number,title,body,labels,state,url"
	changeFields = "number,url,headRefName,headRefOid,baseRefName,state,body"
	// viewFields is what a review needs as well: whether the request is a draft,
	// whether it can be merged into the branch it is meant for, and which
	// repository the work is in. A `gh pr list` is not asked for any of it: that
	// command looks at many requests at a time and a review is about one (§7h).
	viewFields = changeFields + ",isDraft,mergeStateStatus,headRepository"
	// fileFields is a `gh pr view` about the files of a request alone, for a review
	// that has read the rest of it already.
	fileFields = "files"
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
	// defaultBranch is the branch the change requests of the project are meant for,
	// app is the App the executor of the project works as and orchestrator the App
	// the orchestrator of it works as. There is no App of either until a project
	// asks for the mode of the bot (§7i).
	defaultBranch string
	app           *app.Source
	orchestrator  *app.Source
	// signed is the token of the App of the orchestrator, once it has been signed for
	// this adapter, and signing the lock that keeps one gh and the next out of each
	// other's way.
	signed  string
	signing sync.Mutex
}

// The adapter is all three roles of the core at once, and each of them may check
// the machine: without a compile error here, a GitHub that cannot be read would
// pass for a host.
var (
	_ forge.Tracker       = (*Adapter)(nil)
	_ forge.Forge         = (*Adapter)(nil)
	_ forge.CI            = (*Adapter)(nil)
	_ forge.TaskCloser    = (*Adapter)(nil)
	_ forge.TaskCommenter = (*Adapter)(nil)
)

// New returns the adapter of the repository, which is all it needs to know: the
// host of the project is empty for github.com.
func New(repo, host string, env forge.Env) *Adapter {
	return &Adapter{repo: repo, host: host, env: env}
}

// Task returns the issue with the number, as a task of the project: the body a
// person wrote, the labels crewflow decides a task by, and the state, which says
// whether anyone has done it already.
//
// An issue the host of the project does not have is the answer of §7g and not a failure of
// the machine: gh says it in one line of its own words, and crewflow wraps that line in
// `forge.ErrNoSuchTask` so that a caller can tell a task of a repository that moved from a
// tracker that could not be read (docs.DESIGN.md §6a). Nothing else is that answer: a
// repository gh cannot resolve, a network that failed and a 5xx are all "crewflow does not
// know", and they stay it.
func (a *Adapter) Task(ctx context.Context, number int) (forge.Task, error) {
	out, err := a.json(ctx, "issue", "view", strconv.Itoa(number), "-R", a.repo, "--json", taskFields)
	if err != nil {
		if issueIsGone(err) {
			return forge.Task{}, fmt.Errorf("the task #%d is not on %s: %w", number, a.repo, goneTask{err})
		}
		return forge.Task{}, err
	}
	var issue issueJSON
	if err := decode(out, &issue); err != nil {
		return forge.Task{}, err
	}
	return issue.task(), nil
}

// CloseTask closes the issue with the number and leaves the comment under it in
// the same command: a task a merge closed has to say under itself why it is
// closed and by whom, because the person who reads the task afterwards is not
// the machine that closed it (docs/DESIGN.md §7h).
func (a *Adapter) CloseTask(ctx context.Context, number int, comment string) error {
	arguments := []string{"issue", "close", strconv.Itoa(number), "-R", a.repo, "--comment", comment}
	environment, err := a.speaking(ctx)
	if err != nil {
		return err
	}
	_, stderr, code, err := a.env.Run(ctx, program, arguments, "", environment)
	switch {
	case err != nil:
		return fmt.Errorf("gh %s: %w", strings.Join(arguments[:5], " "), err)
	case code != 0:
		return fmt.Errorf("gh %s: exited with %d: %s",
			strings.Join(arguments[:5], " "), code, firstLine(stderr))
	}
	return nil
}

// CommentTask leaves a line under the issue of the task and the issue stays open: what
// crewflow has to say about a run that stands is said where a person and an
// orchestrator look at the task, and a task whose run is standing is not a task that is
// done (docs/DESIGN.md §6).
func (a *Adapter) CommentTask(ctx context.Context, number int, body string) error {
	arguments := []string{"issue", "comment", strconv.Itoa(number), "-R", a.repo, "--body", body}
	_, stderr, code, err := a.env.Run(ctx, program, arguments, "", a.environment())
	switch {
	case err != nil:
		return fmt.Errorf("gh %s: %w", strings.Join(arguments[:5], " "), err)
	case code != 0:
		return fmt.Errorf("gh %s: exited with %d: %s",
			strings.Join(arguments[:5], " "), code, firstLine(stderr))
	}
	return nil
}

// FindChangeRequest returns the request of the branch, and whether there is one:
// gh lists at most the first one, and a branch without a request is a run that
// has not pushed one yet, which is not a failure of anything.
func (a *Adapter) FindChangeRequest(ctx context.Context, branch string) (forge.ChangeRequest, bool, error) {
	return a.changeRequestOf(ctx, branch, nil)
}

// changeRequestOf is the request of the branch as the identity of a run sees it: a
// run in the mode of the bot asks gh in its own environment, because gh without the
// token of the App answers as the person who runs crewflow and a report would then be
// a report of another person (docs/DESIGN.md §7i).
func (a *Adapter) changeRequestOf(ctx context.Context, branch string, env []string) (forge.ChangeRequest, bool, error) {
	out, err := a.jsonIn(env, ctx, "pr", "list", "-R", a.repo,
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
// what a review is about and what a merge may touch, and whether it is a draft or
// cannot be merged into its branch is what a review has to name (docs/DESIGN.md §8,
// §7h).
func (a *Adapter) ChangeRequest(ctx context.Context, number int) (forge.ChangeRequest, error) {
	out, err := a.json(ctx, "pr", "view", strconv.Itoa(number), "-R", a.repo, "--json", viewFields)
	if err != nil {
		return forge.ChangeRequest{}, err
	}
	var change changeJSON
	if err := decode(out, &change); err != nil {
		return forge.ChangeRequest{}, err
	}
	return change.changeRequest(), nil
}

// ChangedFiles returns the paths a request changes against the branch it is meant
// for, as the host holds them: the boundaries of a task are checked against the
// work, and not against what the executor of it said it wrote (docs/DESIGN.md §7c, §7h).
func (a *Adapter) ChangedFiles(ctx context.Context, number int) ([]string, error) {
	out, err := a.json(ctx, "pr", "view", strconv.Itoa(number), "-R", a.repo, "--json", fileFields)
	if err != nil {
		return nil, err
	}
	var answer struct {
		Files []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if err := decode(out, &answer); err != nil {
		return nil, err
	}
	files := make([]string, 0, len(answer.Files))
	for _, file := range answer.Files {
		if file.Path != "" {
			files = append(files, file.Path)
		}
	}
	return files, nil
}

// Comments returns what was written under the request, in the order it was
// written: the review of a run is read as a conversation, and a decision comes
// after the reasons for it.
//
// Whether a comment has been edited since it was published comes with it, because
// a record of a review that anybody can rewrite after the fact is not a record the
// gate may count (docs/DESIGN.md §7h).
//
// The account each of them was written by is the subject the REST API keeps it under —
// `user.type`, `user.id` and `performed_via_github_app.id` — and not the one above: over
// GraphQL an App is named by its slug alone, which a person may have as a login, so the
// two are one string there, and the login of an account changes when an App is renamed. A
// record is counted by the kind of the account and the number under it, and the login is
// read for the report and for nothing else (docs/DESIGN.md §7h, §7i).
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
	if len(answer.Comments) == 0 {
		return nil, nil
	}
	authors, err := a.authorsOf(ctx, number)
	if err != nil {
		return nil, err
	}
	comments := make([]forge.Comment, 0, len(answer.Comments))
	for _, comment := range answer.Comments {
		author, is := authors[comment.ID]
		if !is {
			return nil, fmt.Errorf("the host did not say who wrote the comment %s of the change request #%d",
				comment.ID, number)
		}
		created, err := time.Parse(time.RFC3339, comment.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("comment of %s at %q: %w", author, comment.CreatedAt, err)
		}
		edited, err := comment.editedAfter()
		if err != nil {
			return nil, fmt.Errorf("comment of %s: %w", author, err)
		}
		comments = append(comments, forge.Comment{
			Author:    author,
			Body:      comment.Body,
			CreatedAt: created.UTC(),
			Edited:    edited,
		})
	}
	return comments, nil
}

// commentsPerPage is how many comments are asked for at a time: the API gives 30 unless
// it is asked for more, and 100 is the most it will give. A page shorter than that is
// the end of the list, which is what the host promises about a page — so a change whose
// comments fit in one page is read with one question (docs/DESIGN.md §7h).
const commentsPerPage = 100

// authorsOf are the subjects the comments under the change were written by, by the id
// the host knows a comment under in both of its APIs: the kind of the account, the number
// of that account, and the number of the App the record was written through
// (`performed_via_github_app`), which is what tells the App of the orchestrator from the
// App of the executor and from every other App of the host (docs/DESIGN.md §7h, §7i).
//
// An account the answer does not name is not a record of nobody: it is a gap in what the
// host said, and a caller cannot tell it from an answer and must not treat it as one
// (§7h). A record written by a bot that did not act as an App is read as well and counts
// for nothing, which is a fact about the change and not a refusal (§7h).
func (a *Adapter) authorsOf(ctx context.Context, number int) (map[string]forge.Subject, error) {
	endpoint := fmt.Sprintf("repos/%s/issues/%d/comments", a.repo, number)
	authors := map[string]forge.Subject{}
	for page := 1; ; page++ {
		out, err := a.json(ctx, "api", fmt.Sprintf("%s?per_page=%d&page=%d", endpoint, commentsPerPage, page))
		if err != nil {
			return nil, err
		}
		var answer []struct {
			NodeID string `json:"node_id"`
			User   struct {
				Login string `json:"login"`
				ID    int64  `json:"id"`
				Type  string `json:"type"`
			} `json:"user"`
			PerformedViaGitHubApp *struct {
				ID int64 `json:"id"`
			} `json:"performed_via_github_app"`
		}
		if err := decode(out, &answer); err != nil {
			return nil, err
		}
		for _, comment := range answer {
			author, err := subjectOfComment(comment.User.Login, comment.User.Type, comment.User.ID,
				comment.PerformedViaGitHubApp)
			if err != nil {
				return nil, fmt.Errorf("the author of the comment %s: %w", comment.NodeID, err)
			}
			authors[comment.NodeID] = author
		}
		if len(answer) < commentsPerPage {
			return authors, nil
		}
	}
}

// subjectOfComment is the subject of the account one record of a change was written by, as
// the REST API writes it: the login of the account, the kind the host holds it as, the
// number of the account, and the number of the App the record was written through, which
// GitHub writes as `performed_via_github_app` and leaves out for a record that no App
// wrote (docs/DESIGN.md §7h, §7i).
func subjectOfComment(login, kind string, id int64, app *struct {
	ID int64 `json:"id"`
}) (forge.Subject, error) {
	var number int64
	if app != nil {
		number = app.ID
	}
	return forge.SubjectFrom(login, kind, id, number)
}

// Subject returns the subject of the account of the host the login names: a person with
// the number the host keeps that account under, or an App with the number of the App that
// login belongs to. It is asked once of every login the file of a project names in
// `[merge] owners` and `[merge] reviewers`, and the gate is given subjects instead of
// names from then on — a list of names cannot say what the gate compares, and an App may
// be renamed under any list of them (docs/DESIGN.md §7h, §7i).
//
// A login the host does not know, or holds as a kind crewflow does not read, is an error
// and not an empty subject: a file of a project naming an account crewflow cannot name is
// a mistake in that file, and a gate that went on without it would count no records at
// all while saying that nobody had approved anything (§7h).
func (a *Adapter) Subject(ctx context.Context, login string) (forge.Subject, error) {
	out, err := a.json(ctx, "api", "users/"+url.PathEscape(login))
	if err != nil {
		return forge.Subject{}, fmt.Errorf("the account %q: %w", login, err)
	}
	var answer struct {
		Login string `json:"login"`
		ID    int64  `json:"id"`
		Type  string `json:"type"`
	}
	if err := decode(out, &answer); err != nil {
		return forge.Subject{}, err
	}
	subject, err := forge.SubjectFrom(answer.Login, answer.Type, answer.ID, 0)
	if err != nil {
		return forge.Subject{}, fmt.Errorf("the account %q: %w", login, err)
	}
	if subject.Kind == forge.KindApp {
		return a.subjectOfOurApp(ctx, subject)
	}
	return subject, nil
}

// subjectOfOurApp is the subject of the account of the App of this project it belongs to,
// with the number of that App, and an error where it belongs to none. The host answers
// `/users/<slug>[bot]` with the account of an App and with nothing about the App itself,
// so which App it is crewflow knows of the two the file of a project names — the App of
// the executor and the App of the orchestrator — and of no other: a login of a third App
// is an account whose number cannot be learned, and a subject with no number counts for
// nothing, so the person who wrote it into `[merge] reviewers` is told to name the App of
// the orchestrator instead (docs.DESIGN.md §7h, §7i).
func (a *Adapter) subjectOfOurApp(ctx context.Context, subject forge.Subject) (forge.Subject, error) {
	for _, source := range []*app.Source{a.orchestrator, a.app} {
		if source == nil {
			continue
		}
		bot, err := source.Bot(ctx)
		if err != nil {
			return forge.Subject{}, err
		}
		if strings.EqualFold(bot.Login, subject.Login) {
			return forge.Subject{Kind: forge.KindApp, ID: source.AppID, Login: bot.Login}, nil
		}
	}
	return forge.Subject{}, fmt.Errorf("the account %q is the account of an app that is neither the app of the "+
		"executor nor the app of the orchestrator of this project: crewflow cannot tell which app it is, so no "+
		"record of it could be counted — name the app of the orchestrator by its number", subject.Login)
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

// checkState is the state of a commit out of its check runs: none, pending while
// anything has not ended, and then red for a check that did not pass.
func checkState(runs []checkRunJSON) forge.CheckState {
	if len(runs) == 0 {
		return forge.CheckNone
	}
	state := forge.CheckSuccess
	for _, run := range runs {
		switch run.state() {
		case forge.CheckPending:
			return forge.CheckPending
		case forge.CheckFailure:
			state = forge.CheckFailure
		}
	}
	return state
}

// checkRuns reads the checks of the commit, page by page, and puts them together:
// a commit with many checks is answered with 30 of them at a time, and a red check
// on the second page is as red as one on the first.
func (a *Adapter) checkRuns(ctx context.Context, sha string) ([]checkRunJSON, error) {
	endpoint := "repos/" + a.repo + "/commits/" + sha + "/check-runs"
	var runs []checkRunJSON
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
	TotalCount int            `json:"total_count"`
	CheckRuns  []checkRunJSON `json:"check_runs"`
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
// same fields of a request. The last three are asked for by "gh pr view" only: they
// are what a review needs to know beyond the head and the branch, and they are
// empty in a list, which is not a review.
type changeJSON struct {
	Number     int    `json:"number"`
	URL        string `json:"url"`
	HeadBranch string `json:"headRefName"`
	HeadSHA    string `json:"headRefOid"`
	BaseBranch string `json:"baseRefName"`
	State      string `json:"state"`
	Body       string `json:"body"`
	IsDraft    bool   `json:"isDraft"`
	// MergeState is what the host says about merging the request into its branch:
	// "CLEAN", "DIRTY" when it conflicts, "BLOCKED", "UNSTABLE" and "UNKNOWN"
	// while the host is still working it out. It is kept as it is, in the words
	// of the host, because which of them is an answer and which is not is a
	// question the gate asks and this adapter does not.
	MergeState string `json:"mergeStateStatus"`
	// HeadRepository is the repository the work is in. A request of a fork names
	// the fork there, and a gate has to be able to see that it is not the project.
	HeadRepository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"headRepository"`
	HeadOwner struct {
		Login string `json:"login"`
	} `json:"headRepositoryOwner"`
}

// dirty is what the host calls a request that conflicts with the branch it is meant
// for. GitHub runs no checks for such a request, which is why the gate has to name
// the conflict and not the checks that never came (docs/DESIGN.md §7h).
const dirty = "DIRTY"

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
		Draft:      c.IsDraft,
		Repository: c.repository(),
		Conflicted: strings.EqualFold(c.MergeState, dirty),
		MergeState: c.MergeState,
	}
}

// repository is the repository the work of the request is in, as the core names a
// repository. A request of a fork names the fork, and that is the whole of what a
// gate needs to know to refuse it by name.
func (c changeJSON) repository() string {
	if c.HeadRepository.NameWithOwner != "" {
		return c.HeadRepository.NameWithOwner
	}
	return c.HeadOwner.Login
}

// commentJSON is one comment of the answer of "gh pr view --json comments". The account
// it was written by is not read here: over GraphQL an App is named by its slug alone, and
// the name the gate counts is read of the answer of the REST API (docs/DESIGN.md §7h).
type commentJSON struct {
	// ID is what the host knows this comment under in both of its APIs, and it is how
	// the account of the author is found in the answer of the REST one: the same
	// comment has one id in both, and it is the only thing that joins them.
	ID        string `json:"id"`
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
	// LastEditedAt is when the comment was changed after it was published, and
	// gh leaves the field out altogether for a comment nobody has edited: what is
	// there is a moment, and what is not there is a record nobody has touched.
	LastEditedAt *string `json:"lastEditedAt"`
}

// editedAfter says that the comment was changed after it was published. A moment
// crewflow cannot read is an answer it does not have: the gate counts a record that
// was edited apart from one that was not, and guessing which one it is would let
// an approval be rewritten in silence (docs/DESIGN.md §7h).
func (c commentJSON) editedAfter() (bool, error) {
	if c.LastEditedAt == nil {
		return false, nil
	}
	if _, err := time.Parse(time.RFC3339, *c.LastEditedAt); err != nil {
		return false, fmt.Errorf("the answer says it was edited at %q, which is not a moment: %w", *c.LastEditedAt, err)
	}
	return true, nil
}

// state is the state of a task or a request as the core writes it: in one case,
// whatever case the system of the project writes it in.
func state(from string) string {
	return strings.ToLower(from)
}

// goneTask is the answer of a tracker about a task it does not have with the answer of the
// host inside it: a person reading a report sees what gh said and may run the command by hand,
// and a program asking `forge.NoSuchTask` sees which of the two answers it got (§7g, §6a).
type goneTask struct{ what error }

// Error is the answer of the host as it was, and nothing of its own: what gh said is what
// happened, and a sentence of crewflow over it would hide the command a person may re-run.
func (g goneTask) Error() string { return g.what.Error() }

// Unwrap is the answer as a caller reads it: `forge.ErrNoSuchTask` for the fact, and the
// answer of the host inside it for everything else that may be wrapped around it.
func (g goneTask) Unwrap() error { return errors.Join(forge.ErrNoSuchTask, g.what) }

// issueIsGone is whether an answer of gh about a task means that the host of the project
// does not have it. gh of GitHub says so in one line — "GraphQL: Could not resolve to an
// issue with the number of 53" — and says nothing else that way: a repository it cannot
// resolve, a network that failed and a 5xx are other lines, and a task a tracker could not
// be read about is not a task a tracker does not have (§7g, §6a).
func issueIsGone(err error) bool {
	said := strings.ToLower(err.Error())
	return strings.Contains(said, "could not resolve to an issue") ||
		strings.Contains(said, "could not resolve to an 'issue'")
}

// json starts gh with the arguments, in the environment of the project, and
// returns what it wrote. Every way gh can say no is an error with the command in
// it, because a person who is told what to run by hand sees what crewflow saw.
func (a *Adapter) json(ctx context.Context, args ...string) ([]byte, error) {
	return a.jsonIn(nil, ctx, args...)
}

// jsonIn is gh in the environment of the project plus the one of an identity of a
// run, and nothing else: a token of a run is added to what gh is started with and is
// not kept anywhere else (docs/DESIGN.md §7i).
func (a *Adapter) jsonIn(extra []string, ctx context.Context, args ...string) ([]byte, error) {
	environment, err := a.speaking(ctx)
	if err != nil {
		return nil, err
	}
	command := append([]string{}, args...)
	stdout, stderr, code, err := a.env.Run(ctx, program, command, "", append(environment, extra...))
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

// environment is what the project adds to the environment of gh: its own server, and
// nothing when it is on github.com. It is a slice of its own every time, because the
// environment of an identity of a run is added to it and a slice that another call
// grows is a slice two calls share (docs/DESIGN.md §7i).
func (a *Adapter) environment() []string {
	if a.host == "" {
		return nil
	}
	return []string{"GH_HOST=" + a.host}
}

// speaking is the environment of a gh that talks to the host for the orchestrator of
// the project: its own server, and — where the orchestrator has an account of the host
// of its own — the token that makes gh speak as that account and not as the person
// (docs/DESIGN.md §7i).
//
// A command that does not talk to the host is not given the token: `gh --version` and
// the report of a machine are not the business of the App, and a token in the
// environment of a command is a token in whatever that command writes (§7e).
func (a *Adapter) speaking(ctx context.Context) ([]string, error) {
	environment := a.environment()
	if a.orchestrator == nil {
		return environment, nil
	}
	token, err := a.orchestratorToken(ctx)
	if err != nil {
		return nil, err
	}
	return append(environment, "GH_TOKEN="+token), nil
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
