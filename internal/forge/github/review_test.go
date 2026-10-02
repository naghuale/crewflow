package github

import (
	"fmt"
	"net/url"
	"reflect"
	"slices"
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

// TestTheAuthorOfARecordIsTheSubjectTheHostKeepsItUnder is F-081 (01.10.2026) with the
// answer the owner gave on 01.10: the record of a person is counted by the number of
// that account (`user.id`), the record of an App by the number of that App
// (`performed_via_github_app.id`), and the login — which GraphQL and REST write
// differently for one and the same account, and which changes when an App is renamed —
// is read and used for nothing but the report (docs/DESIGN.md §7h, §7i).
//
// Both records of comments-bot.json were written under the same login as GraphQL gives
// it, and they are two different accounts: the first is the App of the orchestrator
// (5140522), the second a person whose login is the slug of that App (339150227).
func TestTheAuthorOfARecordIsTheSubjectTheHostKeepsItUnder(t *testing.T) {
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
	want := []forge.Subject{
		{Kind: forge.KindApp, ID: 5140522, Login: "crewflow-orchestrator[bot]"},
		{Kind: forge.KindUser, ID: 339150227, Login: "crewflow-orchestrator"},
	}
	for i, author := range want {
		if !comments[i].Author.Same(author) {
			t.Errorf("the author of the record %d is %+v, want %+v", i, comments[i].Author, author)
		}
		if comments[i].Author.Login != author.Login {
			t.Errorf("the author of the record %d is written %q, want %q: a report names the account",
				i, comments[i].Author.Login, author.Login)
		}
	}
}

// TestARecordWhoseAuthorTheHostDoesNotNameIsARefusal: the account a record was written
// by is what makes it a record of somebody, and a record of an account the gate cannot
// name is not a record it may count. Only a person and an App are read: an answer with no
// kind in it, a kind crewflow does not know, and a person whose login is called an App are
// all refused, because reading any of them as «a person» hands a record to whoever shares
// the name (docs/DESIGN.md §7h, §7i).
func TestARecordWhoseAuthorTheHostDoesNotNameIsARefusal(t *testing.T) {
	cases := []struct {
		name   string
		answer string
	}{
		{
			name:   "the answer holds no account at all",
			answer: commentAuthors("", ""),
		},
		{
			name:   "the answer does not say what kind of account it is",
			answer: commentAuthors("crewflow-orchestrator", ""),
		},
		{
			name:   "the account is an organization and not a person",
			answer: commentAuthors("crewflow", "Organization"),
		},
		{
			name:   "a person whose login is called an App",
			answer: commentAuthors("crewflow-orchestrator[bot]", "User"),
		},
		{
			name:   "the answer does not hold that record at all",
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
				t.Fatal("Comments(2) read a record of an account crewflow cannot name")
			}
		})
	}
}

// commentAuthors is the answer of the REST API about the accounts the records of the
// change in comments-bot.json were written by, with the account of both of them given as
// asked: the shape of the answer is the recorded one of comment-authors.json, and each case
// of the refusal above is that answer with one field of the account changed. The app of
// the answer is the one the owner checked on 01.10 for the record of the orchestrator:
// `performed_via_github_app.id` is 5140522, which is `orchestrator.github_app.app_id` of
// the file of this project.
func commentAuthors(login, kind string) string {
	comment := func(id string, number int) string {
		return fmt.Sprintf(`  {
    "id": %d,
    "node_id": %q,
    "author_association": "NONE",
    "issue_url": "https://api.github.com/repos/naghuale/crewflow/issues/107",
    "user": {"id": 336252606, "login": %q, "type": %q, "site_admin": false},
    "performed_via_github_app": {"id": 5140522, "slug": "crewflow-orchestrator",
      "node_id": "A_kgDOBIeVZw"},
    "created_at": "2026-09-30T22:05:47Z",
    "updated_at": "2026-09-30T22:05:47Z"
  }`, number, id, login, kind)
	}
	return "[\n" +
		comment("IC_kwDOUv-WT88AAAABYORP4w", 5920542691) + ",\n" +
		comment("IC_kwDOUv-WT88AAAABYORP5y", 5920542702) + "\n]"
}

// TestSubjectOfALoginOfTheSettingsIsTheAccountBehindIt: the file of a project names its
// accounts by login, and the gate counts records by the number the host keeps each of them
// under — so the adapter is asked once of every login the file names, and what it answers
// is a person with the number of that account and an App with the number of that App
// (docs.DESIGN.md §7h, §7i).
func TestSubjectOfALoginOfTheSettingsIsTheAccountBehindIt(t *testing.T) {
	m, api := machineWithTwoApps(t)
	api.answer["/users/naghuale"] = map[string]any{
		"id": 93920024, "login": "naghuale", "type": "User",
	}
	m.prints("api users/crewflow-orchestrator%5Bbot%5D",
		`{"id": 1988, "login": "crewflow-orchestrator[bot]", "type": "Bot"}`)
	m.prints("api users/naghuale", `{"id": 93920024, "login": "naghuale", "type": "User"}`)
	a := New(repo, "", m.env(t)).WithOrchestrator(Orchestrator{AppID: 5107053, API: api.URL()})

	cases := []struct {
		name  string
		login string
		want  forge.Subject
	}{
		{
			name:  "the account of the app of the orchestrator",
			login: "crewflow-orchestrator[bot]",
			want:  forge.Subject{Kind: forge.KindApp, ID: 5107053, Login: "crewflow-orchestrator[bot]"},
		},
		{
			name:  "the owner of the project",
			login: "naghuale",
			want:  forge.Subject{Kind: forge.KindUser, ID: 93920024, Login: "naghuale"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := a.Subject(t.Context(), tc.login)
			if err != nil {
				t.Fatalf("Subject(%q) returned an error: %v", tc.login, err)
			}
			if !got.Same(tc.want) {
				t.Errorf("Subject(%q) = %+v, want %+v", tc.login, got, tc.want)
			}
			if got.Login != tc.want.Login {
				t.Errorf("Subject(%q) is written %q, want %q: a report names the account the file names",
					tc.login, got.Login, tc.want.Login)
			}
		})
	}
}

// TestAnAccountOfAnAppThisProjectNamesNoNumberForIsARefusal: the host answers
// `/users/<slug>[bot]` with the account of an App and with nothing about the App itself, and
// there is no place to ask: `GET /apps/<slug>` answers only for the public apps, so the number
// of an App is in the file of the project and nowhere else. A project of the shared login names
// no App at all, so a login of an App in `[merge] reviewers` is an account no record of which
// could ever be counted. It is a refusal and not an account of no number: a person who wrote it
// into the file is to be told, because the gate would count nothing and say that nobody had
// approved anything (docs.DESIGN.md §7h, §7i).
func TestAnAccountOfAnAppThisProjectNamesNoNumberForIsARefusal(t *testing.T) {
	m := newMachine().prints("api users/somebody-else%5Bbot%5D",
		`{"id": 4242, "login": "somebody-else[bot]", "type": "Bot"}`)
	a := New(repo, "", m.env(t)).ToLook()

	_, err := a.Subject(t.Context(), "somebody-else[bot]")
	if err == nil {
		t.Fatal("Subject read the account of an app whose number the file of the project names nowhere")
	}
	for _, want := range []string{"somebody-else[bot]", "app_id", "orchestrator"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not mention %q: a person has to know what to change", err, want)
		}
	}
}

// storeNobodyMayRead is the store of the secrets of a machine whose keychain of macOS asks
// the owner of the machine in a window of the system: reading a key out of it opens that
// window, and on a build nobody has trusted yet nobody is at it. The read is a fall and not
// a refusal, so that a test says which key was asked for and stops there (docs/DESIGN.md §7i).
type storeNobodyMayRead struct{}

func (storeNobodyMayRead) Get(service, account string) ([]byte, error) {
	panic(fmt.Sprintf("the key %s/%s was read: an adapter that only looks has no store of secrets at all",
		service, account))
}

func (storeNobodyMayRead) Set(string, string, []byte) error { return nil }

func (storeNobodyMayRead) Has(string, string) (bool, error) { return false, nil }

// TestTheAccountsOfTheListsAreCountedByTheirNumbersWithoutAKeyOfAnyApp: the rules of the
// model of rights, in the way a command that only shows the state of a project counts them.
// The number of an App is in the file of the project, and the number of a person is one
// question `users/<login>` answers, so the way needs no key of any App and the store of the
// machine is one that falls over on the read: the accounts of `[merge] owners` and
// `[merge] reviewers` come out of it exactly as they come out of the roles that hold the keys.
//
// The machine of the test answers nothing about the App behind a login on purpose: the number
// is taken from the settings, so even an answer of the host — a `404` of a private App among
// them — changes nothing here (F-109).
func TestTheAccountsOfTheListsAreCountedByTheirNumbersWithoutAKeyOfAnyApp(t *testing.T) {
	cases := []struct {
		name string
		// login is what the file of a project writes and answer what the host says about
		// that account: the whole of what the host knows and the file does not.
		login  string
		answer string
		want   forge.Subject
		// refused are the words a refusal has to hold: a person who reads it is to know
		// which list of the file to change.
		refused []string
	}{
		{
			name:   "ID-001 a person is the account of the number the host keeps it under",
			login:  "naghuale",
			answer: `{"id": 93920024, "login": "naghuale", "type": "User"}`,
			want:   forge.Subject{Kind: forge.KindUser, ID: 93920024, Login: "naghuale"},
		},
		{
			name:   "ID-002 the app of the orchestrator is the number the settings name",
			login:  "crewflow-orchestrator[bot]",
			answer: `{"id": 336252606, "login": "crewflow-orchestrator[bot]", "type": "Bot"}`,
			want:   forge.Subject{Kind: forge.KindApp, ID: 5107053, Login: "crewflow-orchestrator[bot]"},
		},
		{
			name:   "a login of any app of the project counts as the app the orchestrator works as",
			login:  "crewflow-executor[bot]",
			answer: `{"id": 1988, "login": "crewflow-executor[bot]", "type": "Bot"}`,
			want:   forge.Subject{Kind: forge.KindApp, ID: 5107053, Login: "crewflow-executor[bot]"},
		},
		{
			name:    "ID-004 an account of a kind crewflow does not read is a refusal",
			login:   "crewflow",
			answer:  `{"id": 1, "login": "crewflow", "type": "Organization"}`,
			refused: []string{"does not know"},
		},
		{
			name:    "ID-008 a person whose login is called an app is a refusal",
			login:   "crewflow-orchestrator[bot]",
			answer:  `{"id": 339150227, "login": "crewflow-orchestrator[bot]", "type": "User"}`,
			refused: []string{"called an app"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine().prints("api users/"+url.PathEscape(tc.login), tc.answer)
			m.secrets = storeNobodyMayRead{}
			a := New(repo, "", m.env(t)).
				WithBot(Bot{AppID: 5107052}).
				WithOrchestrator(Orchestrator{AppID: 5107053}).
				ToLook()

			got, err := a.Subject(t.Context(), tc.login)

			if len(tc.refused) > 0 {
				if err == nil {
					t.Fatalf("Subject(%q) = %+v, want a refusal", tc.login, got)
				}
				for _, want := range tc.refused {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("the refusal %q does not mention %q: a person has to know what to change", err, want)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("Subject(%q) returned an error: %v", tc.login, err)
			}
			if !got.Same(tc.want) {
				t.Errorf("Subject(%q) = %+v, want %+v", tc.login, got, tc.want)
			}
			if got.Login != tc.want.Login {
				t.Errorf("Subject(%q) is written %q, want %q: a report names the account the file names",
					tc.login, got.Login, tc.want.Login)
			}
			// Nothing was asked of the host about the App behind the login: its number is in
			// the file of the project, and a question that answers it wrongly for a private
			// app is a question crewflow does not make (F-109).
			for _, asked := range m.ran {
				if line := strings.Join(asked.args, " "); strings.HasPrefix(line, "api apps/") {
					t.Errorf("gh was asked for %q, want no question about the app behind a login", line)
				}
			}
		})
	}
}

// TestAnAdapterThatOnlyLooksAsksAsThePersonAndWritesNothing: a token of an App is minted to
// act in its name, and a command that shows the state of a project does not act — so gh is
// the login of the person in every question, and a write is refused before gh is started at
// all. A record written as the person where the App of the orchestrator was meant is a
// decision of the owner, and a gate counts none of it while saying that nobody had approved
// anything (docs.DESIGN.md §7h, §7i).
func TestAnAdapterThatOnlyLooksAsksAsThePersonAndWritesNothing(t *testing.T) {
	m := newMachine().prints("pr list", "[]")
	m.secrets = storeNobodyMayRead{}
	a := New(repo, "", m.env(t)).WithOrchestrator(Orchestrator{AppID: 5107053}).ToLook()

	if _, _, err := a.FindChangeRequest(t.Context(), "crewflow/43-task"); err != nil {
		t.Fatalf("FindChangeRequest of a project that only shows its state returned an error: %v", err)
	}
	for _, command := range m.ran {
		if slices.ContainsFunc(command.env, func(pair string) bool { return strings.HasPrefix(pair, "GH_TOKEN=") }) {
			t.Errorf("gh was asked for %q with %v, want the login of the person",
				strings.Join(command.args, " "), command.env)
		}
	}

	for _, write := range []struct {
		name string
		do   func() error
	}{
		{
			name: "the record of a review",
			do:   func() error { return a.WriteComment(t.Context(), 7, "REVIEW: APPROVED 9f1c0de") },
		},
		{
			name: "the closing of a task",
			do:   func() error { return a.CloseTask(t.Context(), 43, "merged #7") },
		},
		{
			name: "the notice under a task",
			do:   func() error { return a.CommentTask(t.Context(), 43, "the run stands") },
		},
		{
			name: "the change request of a run",
			do: func() error {
				_, err := a.OpenChangeRequest(t.Context(), "crewflow/43-task", "the run", "Closes #43")
				return err
			},
		},
	} {
		t.Run(write.name, func(t *testing.T) {
			err := write.do()
			if err == nil {
				t.Fatal("an adapter that only looks wrote a record of the project")
			}
			if !strings.Contains(err.Error(), "writes nothing") {
				t.Errorf("the refusal %q does not say that the adapter writes nothing: a person has to know why", err)
			}
		})
	}
	// The refusal comes before gh is started, so the one read above is the only command
	// the machine of the test saw: a write that stands in front of the keychain of macOS
	// before it refuses would be a defect of the other kind.
	if len(m.ran) != 1 {
		t.Errorf("gh was asked %d times, want the one read and no command of a write at all", len(m.ran))
	}
}

// TestSigningAsIsTheSubjectTheRecordIsWrittenIn: the record of a review is an approval
// only because of the account it is written in, and what the gate compares is the number
// of that account — so the mode of the orchestrator is answered as a subject, with the
// number of the App it works as (docs/DESIGN.md §7h, §7i).
func TestSigningAsIsTheSubjectTheRecordIsWrittenIn(t *testing.T) {
	m, api := machineWithTwoApps(t)
	m.prints("api users/octocat", `{"id": 93920024, "login": "octocat", "type": "User"}`)

	separate := New(repo, "", m.env(t)).WithOrchestrator(Orchestrator{AppID: 5107053, API: api.URL()})
	got, err := separate.SigningAs(t.Context())
	if err != nil {
		t.Fatalf("SigningAs of the separate mode returned an error: %v", err)
	}
	want := forge.Subject{Kind: forge.KindApp, ID: 5107053, Login: "crewflow-orchestrator[bot]"}
	if !got.Same(want) {
		t.Errorf("SigningAs = %+v, want the subject of the app of the orchestrator %+v", got, want)
	}

	shared := New(repo, "", m.env(t))
	got, err = shared.SigningAs(t.Context())
	if err != nil {
		t.Fatalf("SigningAs of the shared mode returned an error: %v", err)
	}
	if !got.Same(forge.Subject{Kind: forge.KindUser, ID: 93920024, Login: "octocat"}) {
		t.Errorf("SigningAs = %+v, want the account of the login gh is signed in as", got)
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
