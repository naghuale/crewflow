package github

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/forge"
)

// The commands the adapter is promised to run, written out here rather than
// taken from the package: the test says what is asked of GitHub, it does not
// agree with the package on it.
const (
	repo = "naghuale/crewflow"
	sha  = "9e37237496ea06aca445d2d6670171ec22d7d5bc"
)

// TestTask reads the answer of "gh issue view" of a real issue and checks every
// field of the task, the labels in particular: they are the words §7f decides a
// task by.
func TestTask(t *testing.T) {
	m := newMachine().prints("issue view", fixture(t, "issue.json"))
	a := New(repo, "", m.env(t))

	task, err := a.Task(t.Context(), 1)
	if err != nil {
		t.Fatalf("Task(1) returned an error: %v", err)
	}

	want := forge.Task{
		Number: 1,
		Title:  "feat: binary skeleton and crewflow.toml loading (M1.1)",
		Body:   "## Зачем Пока у crewflow есть только описание устройства. Ну …",
		Labels: []string{"risky", "approved"},
		State:  "closed",
		URL:    "https://github.com/naghuale/crewflow/issues/1",
	}
	if !reflect.DeepEqual(task, want) {
		t.Errorf("Task(1) = %+v,\nwant %+v", task, want)
	}
	wantCommand := "issue view 1 -R " + repo + " --json number,title,body,labels,state,url"
	if got := m.commandLine(0); got != wantCommand {
		t.Errorf("the adapter ran %q, want %q", got, wantCommand)
	}
}

// TestFindChangeRequest reads the answer of "gh pr list" of a branch with a
// request and checks that it is found by the branch, with the head the review
// and the merge are about.
func TestFindChangeRequest(t *testing.T) {
	m := newMachine().prints("pr list", fixture(t, "change-list.json"))
	a := New(repo, "", m.env(t))

	change, found, err := a.FindChangeRequest(t.Context(), "feat/m1-2-doctor")
	if err != nil {
		t.Fatalf("FindChangeRequest returned an error: %v", err)
	}
	if !found {
		t.Fatal("FindChangeRequest found nothing, want the request of the branch")
	}

	want := forge.ChangeRequest{
		Number:     4,
		URL:        "https://github.com/naghuale/crewflow/pull/4",
		HeadBranch: "feat/m1-2-doctor",
		HeadSHA:    "dbb3c3ba4c1e97f68cf7d8a36bc1839f8e934c02",
		BaseBranch: "main",
		State:      "merged",
		Body:       "Closes #3 ## What changed - `internal/doctor`: `Run(ctx, Env …",
	}
	if !reflect.DeepEqual(change, want) {
		t.Errorf("FindChangeRequest = %+v,\nwant %+v", change, want)
	}
	wantCommand := "pr list -R " + repo +
		" --head feat/m1-2-doctor --state all --json number,url,headRefName,headRefOid,baseRefName,state,body --limit 1"
	if got := m.commandLine(0); got != wantCommand {
		t.Errorf("the adapter ran %q, want %q", got, wantCommand)
	}
}

// TestFindChangeRequestWithoutOne is the state of a run that has pushed its
// branch and has not opened the request yet: it is a run in progress, not a
// failure, and crewflow waits rather than stops.
func TestFindChangeRequestWithoutOne(t *testing.T) {
	m := newMachine().prints("pr list", "[]\n")
	a := New(repo, "", m.env(t))

	change, found, err := a.FindChangeRequest(t.Context(), "feat/m1-3b-task-run")
	if err != nil {
		t.Fatalf("FindChangeRequest returned an error: %v", err)
	}
	if found {
		t.Errorf("FindChangeRequest = %+v, want no request at all", change)
	}
}

// TestChangeRequest reads the answer of "gh pr view" of a real request.
func TestChangeRequest(t *testing.T) {
	m := newMachine().prints("pr view", fixture(t, "change.json"))
	a := New(repo, "", m.env(t))

	change, err := a.ChangeRequest(t.Context(), 2)
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
	}
	if !reflect.DeepEqual(change, want) {
		t.Errorf("ChangeRequest(2) = %+v,\nwant %+v", change, want)
	}
	wantCommand := "pr view 2 -R " + repo + " --json number,url,headRefName,headRefOid,baseRefName,state,body"
	if got := m.commandLine(0); got != wantCommand {
		t.Errorf("the adapter ran %q, want %q", got, wantCommand)
	}
}

// TestComments reads the review of a run out of the comments of a request: who
// wrote it, what was written and when, because the order of the review is the
// order of the comments.
func TestComments(t *testing.T) {
	m := newMachine().prints("pr view", fixture(t, "comments.json"))
	a := New(repo, "", m.env(t))

	comments, err := a.Comments(t.Context(), 2)
	if err != nil {
		t.Fatalf("Comments(2) returned an error: %v", err)
	}

	want := []forge.Comment{
		{
			Author:    "naghuale",
			Body:      "REVIEW: CHANGES REQUESTED Ревью коммита 1d74cab. Код соответ …",
			CreatedAt: time.Date(2026, time.September, 27, 22, 10, 43, 0, time.UTC),
		},
		{
			Author:    "naghuale",
			Body:      "REVIEW: APPROVED 9e37237496ea06aca445d2d6670171ec22d7d5bc Th …",
			CreatedAt: time.Date(2026, time.September, 27, 22, 14, 28, 0, time.UTC),
		},
	}
	if !reflect.DeepEqual(comments, want) {
		t.Errorf("Comments(2) = %+v,\nwant %+v", comments, want)
	}
	if got := m.commandLine(0); got != "pr view 2 -R "+repo+" --json comments" {
		t.Errorf("the adapter ran %q, want the comments of the request", got)
	}
}

// TestStatus walks the four answers of a commit, from the check runs of a real
// workflow: all passed, one still running, one red, and nothing at all.
func TestStatus(t *testing.T) {
	cases := []struct {
		file string
		want forge.CheckState
	}{
		{"check-runs-success.json", forge.CheckSuccess},
		{"check-runs-pending.json", forge.CheckPending},
		{"check-runs-failure.json", forge.CheckFailure},
		{"check-runs-none.json", forge.CheckNone},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			m := newMachine().prints("api", fixture(t, tc.file))
			a := New(repo, "", m.env(t))

			got, err := a.Status(t.Context(), sha)
			if err != nil {
				t.Fatalf("Status returned an error: %v", err)
			}
			if got != tc.want {
				t.Errorf("Status = %q, want %q", got, tc.want)
			}
			wantCommand := "api repos/" + repo + "/commits/" + sha + "/check-runs?per_page=100&page=1"
			if command := m.commandLine(0); command != wantCommand {
				t.Errorf("the adapter ran %q, want %q", command, wantCommand)
			}
			if len(m.ran) != 1 {
				t.Errorf("the adapter asked %v, want once: every check of the commit is in that page", m.ran)
			}
		})
	}
}

// TestStatusConclusions walks every conclusion GitHub writes, one run at a time,
// because a commit is green or red by them and by nothing else. A check that was
// skipped or came out neutral is a check that is not against this commit, which
// is what GitHub itself counts as a passed required check: a workflow with a
// conditional job would otherwise be red for ever.
func TestStatusConclusions(t *testing.T) {
	cases := []struct {
		conclusion string
		want       forge.CheckState
	}{
		{"success", forge.CheckSuccess},
		{"neutral", forge.CheckSuccess},
		{"skipped", forge.CheckSuccess},
		{"failure", forge.CheckFailure},
		{"cancelled", forge.CheckFailure},
		{"timed_out", forge.CheckFailure},
		{"action_required", forge.CheckFailure},
		{"stale", forge.CheckFailure},
	}
	for _, tc := range cases {
		t.Run(tc.conclusion, func(t *testing.T) {
			m := newMachine().prints("api", checkRuns(
				checkRunAnswer{name: "lint", status: "completed", conclusion: tc.conclusion}))
			a := New(repo, "", m.env(t))

			got, err := a.Status(t.Context(), sha)
			if err != nil {
				t.Fatalf("Status returned an error: %v", err)
			}
			if got != tc.want {
				t.Errorf("Status of a check concluded %q = %q, want %q", tc.conclusion, got, tc.want)
			}
		})
	}
}

// TestStatusNotFinished walks the statuses of a check run that has not ended: a
// commit with one of them is a commit crewflow waits for, whatever the others
// say, and the conclusion of such a run is nothing at all.
func TestStatusNotFinished(t *testing.T) {
	for _, status := range []string{"queued", "in_progress", "waiting", "requested", "pending"} {
		t.Run(status, func(t *testing.T) {
			m := newMachine().prints("api", checkRuns(
				checkRunAnswer{name: "lint", status: status},
				checkRunAnswer{name: "test", status: "completed", conclusion: "failure"}))
			a := New(repo, "", m.env(t))

			got, err := a.Status(t.Context(), sha)
			if err != nil {
				t.Fatalf("Status returned an error: %v", err)
			}
			if got != forge.CheckPending {
				t.Errorf("Status of a check that is %q = %q, want %q", status, got, forge.CheckPending)
			}
		})
	}
}

// TestStatusReadsEveryPage walks the commit whose checks do not fit into one
// answer: the API gives 30 at a time unless it is asked for more, and a red
// check on the second page is as red as one on the first. The fixtures are the
// two real pages of one commit of this repository, three at a time, with the
// conclusion of one run of the second page red.
func TestStatusReadsEveryPage(t *testing.T) {
	perPage(t, 3)
	page := func(n int) string {
		return "api repos/" + repo + "/commits/" + sha + "/check-runs?per_page=3&page=" + strconv.Itoa(n)
	}
	m := newMachine().
		prints(page(1), fixture(t, "check-runs-page-1.json")).
		prints(page(2), fixture(t, "check-runs-page-2.json"))
	a := New(repo, "", m.env(t))

	got, err := a.Status(t.Context(), sha)
	if err != nil {
		t.Fatalf("Status returned an error: %v", err)
	}

	if got != forge.CheckFailure {
		t.Errorf("Status = %q, want %q: the red check is on the second page", got, forge.CheckFailure)
	}
	for i, want := range []string{page(1), page(2)} {
		if command := m.commandLine(i); command != want {
			t.Errorf("the adapter asked %q as its question %d, want %q", command, i+1, want)
		}
	}
	if len(m.ran) != 2 {
		t.Errorf("the adapter asked %d times, want twice for the six checks of the commit", len(m.ran))
	}
}

// TestStatusStopsAtTheLastPage checks that a commit of six checks does not make
// crewflow ask for a third page that is not there.
func TestStatusStopsAtTheLastPage(t *testing.T) {
	perPage(t, 3)
	page := func(n int) string {
		return "api repos/" + repo + "/commits/" + sha + "/check-runs?per_page=3&page=" + strconv.Itoa(n)
	}
	m := newMachine().prints(page(1), fixture(t, "check-runs-page-1.json")).
		prints(page(2), fixture(t, "check-runs-page-2.json"))
	a := New(repo, "", m.env(t))

	if _, err := a.Status(t.Context(), sha); err != nil {
		t.Fatalf("Status returned an error: %v", err)
	}

	if len(m.ran) != 2 {
		t.Errorf("the adapter asked %v, want the two pages of the commit and no more", m.ran)
	}
}

// TestStatusOnePageIsEnough checks a commit whose checks fit into one answer: the
// first page ends the questions, and no page is asked for that is not there.
func TestStatusOnePageIsEnough(t *testing.T) {
	perPage(t, 3)
	m := newMachine().prints("api", `{"total_count": 1, "check_runs": [
	  {"name": "test", "status": "completed", "conclusion": "success"}
	]}`)
	a := New(repo, "", m.env(t))

	got, err := a.Status(t.Context(), sha)
	if err != nil {
		t.Fatalf("Status returned an error: %v", err)
	}

	if got != forge.CheckSuccess {
		t.Errorf("Status = %q, want %q", got, forge.CheckSuccess)
	}
	if len(m.ran) != 1 {
		t.Errorf("the adapter asked %v, want the one page of one check", m.ran)
	}
}

// TestStatusFailsOnTheSecondPage walks the other way round: a red check on the
// first page and nothing after it is red without a second question.
func TestStatusFailsOnTheSecondPage(t *testing.T) {
	perPage(t, 3)
	page := func(n int) string {
		return "api repos/" + repo + "/commits/" + sha + "/check-runs?per_page=3&page=" + strconv.Itoa(n)
	}
	m := newMachine().prints(page(1), fixture(t, "check-runs-failure.json"))
	a := New(repo, "", m.env(t))

	got, err := a.Status(t.Context(), sha)
	if err != nil {
		t.Fatalf("Status returned an error: %v", err)
	}

	if got != forge.CheckFailure {
		t.Errorf("Status = %q, want %q", got, forge.CheckFailure)
	}
	if len(m.ran) != 1 {
		t.Errorf("the adapter asked %v, want the one page that holds every check", m.ran)
	}
}

// TestStatusPageFails walks a red check on a page that could not be read: the
// whole answer is an error, because a commit whose second page is unknown is a
// commit crewflow cannot call green.
func TestStatusPageFails(t *testing.T) {
	perPage(t, 3)
	page := func(n int) string {
		return "api repos/" + repo + "/commits/" + sha + "/check-runs?per_page=3&page=" + strconv.Itoa(n)
	}
	m := newMachine().
		prints(page(1), fixture(t, "check-runs-page-1.json")).
		fails(page(2), "HTTP 502: Bad gateway\n")
	a := New(repo, "", m.env(t))

	_, err := a.Status(t.Context(), sha)
	if err == nil {
		t.Fatal("Status returned no error, want one: half of the checks are unknown")
	}
	for _, want := range []string{"page=2", "HTTP 502"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// TestStatusWalksTheOrderOfTheQuestions checks the order of the two questions
// that can be asked at once: a commit with a red check and a check that has not
// finished is a commit crewflow is still waiting for, and the red one is said
// when the rest is done.
func TestStatusWalksTheOrderOfTheQuestions(t *testing.T) {
	m := newMachine().prints("api", `{"total_count": 2, "check_runs": [
	  {"name": "lint", "status": "completed", "conclusion": "failure"},
	  {"name": "test", "status": "in_progress", "conclusion": null}
	]}`)
	a := New(repo, "", m.env(t))

	got, err := a.Status(t.Context(), sha)
	if err != nil {
		t.Fatalf("Status returned an error: %v", err)
	}

	if got != forge.CheckPending {
		t.Errorf("Status = %q, want %q: one check has not finished yet", got, forge.CheckPending)
	}
}

// TestGHFails walks the two ways gh says no: it refused, or it said nothing at
// all. Both are an error with the command in it, so that a person can run the
// same thing and see it.
func TestGHFails(t *testing.T) {
	cases := []struct {
		name   string
		answer answer
		want   []string
	}{
		{
			name:   "gh refused",
			answer: answer{stderr: "GraphQL: Could not resolve to an Issue with the number of 99.\n", code: 1},
			want:   []string{"issue view 99", "Could not resolve to an Issue"},
		},
		{
			name:   "gh said nothing",
			answer: answer{stdout: " \n"},
			want:   []string{"issue view 99", "no JSON"},
		},
		{
			name:   "gh is not there",
			answer: answer{err: errors.New(`exec: "gh": executable file not found in $PATH`)},
			want:   []string{"issue view 99", "executable file not found"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine()
			m.answers["issue view"] = tc.answer
			a := New(repo, "", m.env(t))

			_, err := a.Task(t.Context(), 99)
			if err == nil {
				t.Fatal("Task returned no error, want one")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

// TestHostIsTheEnvironmentOfGH checks that a project on its own server is talked
// to through GH_HOST, and that a project on the public one is not: an adapter
// that always sets the host would send the public requests to a server that is
// not there.
func TestHostIsTheEnvironmentOfGH(t *testing.T) {
	cases := []struct {
		name string
		host string
		want []string
	}{
		{"the public host", "", nil},
		{"the host of the project", "github.company.com", []string{"GH_HOST=github.company.com"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine().prints("issue view", fixture(t, "issue.json"))
			a := New(repo, tc.host, m.env(t))

			if _, err := a.Task(t.Context(), 1); err != nil {
				t.Fatalf("Task(1) returned an error: %v", err)
			}
			if len(m.ran) == 0 {
				t.Fatal("the adapter ran no command")
			}
			if !reflect.DeepEqual(m.ran[0].env, tc.want) {
				t.Errorf("the command ran with the environment %q, want %q", m.ran[0].env, tc.want)
			}
		})
	}
}

// wantCheck is what a report must say about one line of the adapter.
type wantCheck struct {
	name   string
	status forge.Status
	// detail and hint are what the line must mention, a person to act on it.
	detail []string
	hint   []string
}

// TestDoctor walks what a person runs into before a task: no gh at all, a gh that
// does not answer, a gh nobody is signed in with, and a gh that is. Nothing of
// the answer of "gh auth status" may end up in a line of the report: it holds the
// token, and a report is read by people and pasted into issues (§7e).
func TestDoctor(t *testing.T) {
	cases := []struct {
		name    string
		machine func(*machine) *machine
		want    []wantCheck
	}{
		{
			name:    "ready",
			machine: func(m *machine) *machine { return m },
			want: []wantCheck{
				{name: "gh", status: forge.OK, detail: []string{"gh version 2.62.0"}},
				{name: "gh login", status: forge.OK, detail: []string{"signed in as octocat"}},
			},
		},
		{
			name:    "gh is not installed",
			machine: func(m *machine) *machine { return m.without("gh") },
			want: []wantCheck{
				{
					name:   "gh",
					status: forge.Fail,
					detail: []string{"gh is not in PATH"},
					hint:   []string{"gh auth login", "forge.kind"},
				},
			},
		},
		{
			name: "gh does not answer",
			machine: func(m *machine) *machine {
				m.answers["--version"] = answer{stderr: "gh: broken\n", code: 2}
				return m
			},
			want: []wantCheck{
				{
					name:   "gh",
					status: forge.Fail,
					detail: []string{"exited with 2", "gh: broken"},
					hint:   []string{"install gh"},
				},
			},
		},
		{
			name: "nobody is signed in",
			machine: func(m *machine) *machine {
				return m.fails("auth status", "You are not logged into any GitHub hosts. To log in, run: gh auth login\n")
			},
			want: []wantCheck{
				{name: "gh", status: forge.OK},
				{
					name:   "gh login",
					status: forge.Fail,
					detail: []string{"not logged into any GitHub hosts"},
					hint:   []string{"gh auth login"},
				},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.machine(newMachine().prints("--version", "gh version 2.62.0 (2024-11-14)\n").
				prints("auth status", "github.com\n  \u2713 Logged in to github.com account octocat (keyring)\n"+
					"  - Token: gho_secrettoken\n"))
			a := New(repo, "", m.env(t))

			checks := a.Doctor(t.Context())

			if len(checks) != len(tc.want) {
				t.Fatalf("Doctor made %d checks %v, want %d", len(checks), checkNames(checks), len(tc.want))
			}
			for _, want := range tc.want {
				check := checkNamed(t, checks, want.name)
				if check.Status != want.status {
					t.Errorf("check %q = %q (%s), want %q", want.name, check.Status, check.Detail, want.status)
				}
				for _, in := range want.detail {
					if !strings.Contains(check.Detail, in) {
						t.Errorf("check %q detail = %q, want it to mention %q", want.name, check.Detail, in)
					}
				}
				for _, in := range want.hint {
					if !strings.Contains(check.Hint, in) {
						t.Errorf("check %q hint = %q, want it to mention %q", want.name, check.Hint, in)
					}
				}
				if want.status == forge.Fail && check.Hint == "" {
					t.Errorf("the failed check %q has no hint, want what to do about it", want.name)
				}
				for _, secret := range []string{"gho_", "Token:"} {
					if strings.Contains(check.Detail, secret) || strings.Contains(check.Hint, secret) {
						t.Errorf("the check %q holds %q of the answer of gh auth status", want.name, secret)
					}
				}
			}
		})
	}
}

// TestTheAdapterIsAllThreeRoles checks that one GitHub serves the host, the tasks
// and the CI at once, which is the default of §7g: crewflow asks the same thing
// of the same program three times, and not of three programs.
func TestTheAdapterIsAllThreeRoles(t *testing.T) {
	m := newMachine()
	a := New(repo, "", m.env(t))
	set := forge.Set{Forge: a, Tracker: a, CI: a}

	checkers := set.Checkers()

	if len(checkers) != 1 {
		t.Errorf("the set checks %d adapters, want the one of GitHub", len(checkers))
	}
}

// fixture is what gh wrote, saved as it was: the shapes of the answers are not
// guessed here, they are the ones gh prints.
func fixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read the fixture %s: %v", name, err)
	}
	return string(data)
}

// checkNamed returns the line of the report with the name, so that a test may
// look at one line without depending on the position of the others.
func checkNamed(t *testing.T, checks []forge.Check, name string) forge.Check {
	t.Helper()
	for _, check := range checks {
		if check.Name == name {
			return check
		}
	}
	t.Fatalf("no check named %q in %v", name, checkNames(checks))
	return forge.Check{}
}

// checkRunAnswer is one check of a commit as the API writes it: a name, a status
// and a conclusion, which is there only when the check has ended.
type checkRunAnswer struct {
	name       string
	status     string
	conclusion string
}

// checkRuns is the answer of the API about the named checks, in the shape the
// real answer has: a total, and the runs themselves.
func checkRuns(runs ...checkRunAnswer) string {
	answer := struct {
		TotalCount int `json:"total_count"`
		CheckRuns  []struct {
			Name       string  `json:"name"`
			HeadSHA    string  `json:"head_sha"`
			Status     string  `json:"status"`
			Conclusion *string `json:"conclusion"`
		} `json:"check_runs"`
	}{TotalCount: len(runs)}
	for _, run := range runs {
		written := struct {
			Name       string  `json:"name"`
			HeadSHA    string  `json:"head_sha"`
			Status     string  `json:"status"`
			Conclusion *string `json:"conclusion"`
		}{Name: run.name, HeadSHA: sha, Status: run.status}
		if run.conclusion != "" {
			written.Conclusion = &run.conclusion
		}
		answer.CheckRuns = append(answer.CheckRuns, written)
	}
	out, err := json.Marshal(answer)
	if err != nil {
		panic("the answer of the test is not JSON: " + err.Error())
	}
	return string(out)
}

// perPage makes the adapter ask for fewer checks at a time, so that a test of the
// pages does not have to hold a hundred runs to see the second one.
func perPage(t *testing.T, n int) {
	t.Helper()
	was := checkRunsPerPage
	checkRunsPerPage = n
	t.Cleanup(func() { checkRunsPerPage = was })
}

func checkNames(checks []forge.Check) []string {
	names := make([]string, 0, len(checks))
	for _, check := range checks {
		names = append(names, check.Name)
	}
	return names
}
