package doctor

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// The five checks of the separation of the subjects, as the owner of the project named
// them on 01.10 (docs/DESIGN.md §7i, §7k):
//
//	DR-001 the mode of the shared login is never hidden: it is a warning in the words of
//	       the mode, and the debts it carries are in the JSON of the report;
//	DR-002 the mode of a separate login requires an owner who is not the orchestrator;
//	DR-003 the mode of a separate login requires the lists of accounts not to meet;
//	DR-004 the accounts of each bot of the project are checked one by one — the key, the
//	       installation and the rights of its app;
//	DR-005 every debt of trust of the project is visible, and a project of a separate
//	       login with both apps set up has none;
//	DR-008 both lists are shown as the correspondence they are — the login of the file and
//	       the number the gate counts that account by;
//	DR-009 a login of either list the host cannot name is a failure of the file, and the
//	       report says which list to change.
//
// The records the gate counts are the cases OR-001…OR-008: four of them in
// `internal/gate` and three in `internal/config`, and the debt of OR-007 is what this
// file is about — the same accounts in the shared mode, which is not an error and is
// shown as what it is.

// TestDR001TheSharedLoginIsNeverHidden: a project in the mode of the shared login says
// so in the line of the mode, and the debts of trust it carries are named in the report
// itself, where a script reads them: the owner and the orchestrator are one subject, and
// the same account is in both lists of the gate (docs/DESIGN.md §7i, §7k).
func TestDR001TheSharedLoginIsNeverHidden(t *testing.T) {
	m := newMachine().has("git", "gh", "agent").
		prints("git --version", "git version 2.47.1\n").
		prints("gh --version", "gh version 2.62.0\n").
		prints("gh auth status", "github.com\n  ✓ Logged in to github.com account octocat (keyring)\n")
	config := writeConfig(t, baseConfig)

	report := runOn(t, m, config)

	line := checkOf(t, report, separationCheck)
	if line.Status != Warn {
		t.Fatalf("check %q = %q (%s), want %q: the mode of the shared login is a debt, not a pass",
			separationCheck, line.Status, line.Detail, Warn)
	}
	for _, want := range []string{DebtOwnerOrchestrator, DebtOwnersReviewers} {
		if !strings.Contains(line.Detail, want) {
			t.Errorf("check %q detail = %q, want it to name the debt %q", separationCheck, line.Detail, want)
		}
	}
	if !slices.Equal(report.TrustDebt, []string{DebtOwnerOrchestrator, DebtOwnersReviewers}) {
		t.Errorf("the report holds the debts %v, want both of them", report.TrustDebt)
	}
	// The debts are in the JSON under their own name, and the section of the separation
	// says whether the owner and the orchestrator are one account: a person — or a
	// script — must not have to read a line of a check to learn it.
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal the report: %v", err)
	}
	var answer struct {
		Authority struct {
			Owner               string    `json:"owner"`
			Orchestrator        string    `json:"orchestrator"`
			Executor            string    `json:"executor"`
			OwnerIsOrchestrator bool      `json:"owner_is_orchestrator"`
			Overlap             []Subject `json:"overlap"`
			Reviewers           []Subject `json:"reviewers"`
		} `json:"authority"`
		TrustDebt []string `json:"trust_debt"`
	}
	if err := json.Unmarshal(data, &answer); err != nil {
		t.Fatalf("unmarshal the report: %v", err)
	}
	if !answer.Authority.OwnerIsOrchestrator {
		t.Error("the JSON says the owner and the orchestrator are different accounts, want one subject in the shared mode")
	}
	if !slices.Equal(answer.TrustDebt, report.TrustDebt) {
		t.Errorf("the JSON holds the debts %v, want %v", answer.TrustDebt, report.TrustDebt)
	}
	if answer.Authority.Owner != "naghuale" || answer.Authority.Orchestrator != "octocat" {
		t.Errorf("the JSON holds the accounts %+v, want the owner of the repository and the login of gh",
			answer.Authority)
	}
}

// TestDR007TheAccountsOfTheSharedModeAreAnOverlapAndNotAnError: the same accounts in the
// shared mode — the owners and the reviewers naming one account, which in the separate
// mode is refused by the load of the file (OR-008) — are not an error here: one login is
// both subjects in this mode by definition, and the report says `owner == orchestrator:
// YES` and names the overlap (docs/DESIGN.md §7i, §7k).
func TestDR007TheAccountsOfTheSharedModeAreAnOverlapAndNotAnError(t *testing.T) {
	m := newMachine().has("git", "gh", "agent").
		prints("git --version", "git version 2.47.1\n").
		prints("gh --version", "gh version 2.62.0\n").
		prints("gh auth status", "github.com\n  ✓ Logged in to github.com account octocat (keyring)\n").
		prints("gh api users/octocat", `{"id": 1, "login": "octocat", "type": "User"}`)
	config := writeConfig(t, baseConfig+
		"\n[merge]\nreviewers = [\"naghuale\"]\nowners = [\"naghuale\", \"octocat\"]\n")

	report := runOn(t, m, config)

	if line := checkOf(t, report, separationCheck); line.Status != Warn {
		t.Fatalf("check %q = %q (%s), want %q: an overlap in the shared mode is a debt and not a failure",
			separationCheck, line.Status, line.Detail, Warn)
	}
	if !report.OK() {
		t.Error("report.OK() = false, want true: nothing is missing on this machine")
	}
	authority := report.Authority
	if !authority.OwnerIsOrchestrator {
		t.Error("the report says the owner and the orchestrator are two accounts, want one in the shared mode")
	}
	if got := loginsOfSubjects(authority.Overlap); !slices.Equal(got, []string{"naghuale"}) {
		t.Errorf("the report holds the overlap %v, want the one account in both lists", authority.Overlap)
	}
	if !slices.Contains(report.TrustDebt, DebtOwnersReviewers) {
		t.Errorf("the report holds the debts %v, want the overlap of the two lists among them", report.TrustDebt)
	}
}

// TestDR002TheSeparateModeNeedsAnOwnerWhoIsNotTheOrchestrator: a file whose
// orchestrator works under the login of the owner does not load (OR-005), and the report
// of such a machine says so where a person reads it — the check of the file fails, and it
// names the mode that is not kept. The run does not continue on a machine whose file
// promises a separation the accounts do not make (docs/DESIGN.md §7i, §7k).
func TestDR002TheSeparateModeNeedsAnOwnerWhoIsNotTheOrchestrator(t *testing.T) {
	m := machineWithTheOrchestrator(t)
	config := writeConfig(t, m.orchestratorProject()+"\n[merge]\nreviewers = [\"naghuale\"]\n")

	report := runOn(t, m, config)

	line := checkOf(t, report, "config")
	if line.Status != Fail {
		t.Fatalf("check \"config\" = %q (%s), want fail: the file names the owner as the account of the orchestrator", line.Status, line.Detail)
	}
	for _, want := range []string{"separate mode requires an account of its own", "naghuale"} {
		if !strings.Contains(line.Detail, want) {
			t.Errorf("check \"config\" detail = %q, want it to mention %q", line.Detail, want)
		}
	}
	if report.OK() {
		t.Error("report.OK() = true, want false: a project whose orchestrator is the owner is not a project to start a task on")
	}
}

// TestDR003TheSeparateModeNeedsTheListsNotToMeet: two shapes of the same rule, one in
// each of the places it can be caught. A file that names the same account in both lists
// is refused by the load (OR-008) and the report of that machine fails on the file. A
// file that names the account of the app of the orchestrator among the owners and
// leaves the reviewers out loads — the account of an app is in no file — and the section
// of the report catches it, because the gate counts that account's record of a review as
// a decision of the owner (docs/DESIGN.md §7i, §7k).
func TestDR003TheSeparateModeNeedsTheListsNotToMeet(t *testing.T) {
	t.Run("the file names both", func(t *testing.T) {
		m := machineWithTheOrchestrator(t)
		config := writeConfig(t, m.orchestratorProject()+
			"\n[merge]\nreviewers = [\"crewflow-orchestrator[bot]\"]\nowners = [\"crewflow-orchestrator[bot]\"]\n")

		report := runOn(t, m, config)

		line := checkOf(t, report, "config")
		if line.Status != Fail {
			t.Fatalf("check \"config\" = %q (%s), want fail: the lists of the file meet", line.Status, line.Detail)
		}
		if !strings.Contains(line.Detail, "remove overlapping identities or switch to shared mode") {
			t.Errorf("check \"config\" detail = %q, want it to say what to do about it", line.Detail)
		}
	})

	t.Run("the account of the app is named among the owners", func(t *testing.T) {
		m := machineWithTheOrchestrator(t)
		config := writeConfig(t, m.orchestratorProject()+
			"\n[merge]\nowners = [\"crewflow-orchestrator[bot]\"]\n")

		report := runOn(t, m, config)

		line := checkOf(t, report, separationCheck)
		if line.Status != Fail {
			t.Fatalf("check %q = %q (%s), want fail: the account of the orchestrator is among the owners",
				separationCheck, line.Status, line.Detail)
		}
		if !strings.Contains(line.Detail, "crewflow-orchestrator[bot]") {
			t.Errorf("check %q detail = %q, want it to name the account that is in both lists", separationCheck, line.Detail)
		}
		if got := loginsOfSubjects(report.Authority.Overlap); !slices.Equal(got, []string{"crewflow-orchestrator[bot]"}) {
			t.Errorf("the report holds the overlap %v, want the account of the app", report.Authority.Overlap)
		}
		if !strings.Contains(checkOf(t, report, separationCheck).Hint, `mode = "shared"`) {
			t.Errorf("check %q hint does not say what to do about it", separationCheck)
		}
	})
}

// TestDR004EveryBotOfTheProjectIsChecked: a project with two apps has six checks of its
// own — the key, the installation and the rights of each — and a report that named them
// all "app key" would leave a person with two lines and no way to tell which key is
// missing (docs.DESIGN §7i).
func TestDR004EveryBotOfTheProjectIsChecked(t *testing.T) {
	m := machineWithTheOrchestrator(t)
	// A project with two apps: the executor under the first one and the orchestrator
	// under the second, which is what a project of the separate mode looks like when the
	// executor is a bot too (docs/DESIGN.md §7i).
	config := writeConfig(t, m.botProject()+"\n"+orchestratorConfig)

	report := runOn(t, m, config)

	for _, want := range []string{
		"app key", "token",
		"orchestrator app key", "orchestrator app", "orchestrator token",
	} {
		if line := checkOf(t, report, want); line.Status != OK {
			t.Errorf("check %q = %q (%s), want ok: the account of that app is checked like any other", want, line.Status, line.Detail)
		}
	}
	if report.Authority.OwnerIsOrchestrator {
		t.Error("the report says the owner and the orchestrator are one account, want the three subjects apart")
	}
	if len(report.TrustDebt) != 0 {
		t.Errorf("the report holds the debts %v, want none: three accounts, nothing on trust", report.TrustDebt)
	}
	// The section names each of the three subjects by the account the host gives it: the
	// App of the executor, the App of the orchestrator and the owner of the repository.
	authority := report.Authority
	if authority.Owner != "naghuale" || authority.Orchestrator != "crewflow-orchestrator[bot]" ||
		authority.Executor != "crewflow-executor[bot]" {
		t.Errorf("the report holds the accounts %+v, want the owner, the app of the orchestrator and the app of the executor",
			authority)
	}
}

// TestDR005EveryDebtIsVisibleAndASeparateProjectHasNone: the debts of a project are
// exactly the places of it where a decision of a person is held on trust, and a project
// whose orchestrator works under an account of its own carries none: the list is empty
// and stays a list, so that a script asking for it finds nothing instead of a hole
// (docs.DESIGN §7i, §7k).
func TestDR005EveryDebtIsVisibleAndASeparateProjectHasNone(t *testing.T) {
	m := machineWithTheOrchestrator(t)
	config := writeConfig(t, m.orchestratorProject())

	report := runOn(t, m, config)

	if len(report.TrustDebt) != 0 {
		t.Errorf("the report holds the debts %v, want an empty list: the owner and the orchestrator are two accounts", report.TrustDebt)
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal the report: %v", err)
	}
	var answer struct {
		TrustDebt []string `json:"trust_debt"`
	}
	if err := json.Unmarshal(data, &answer); err != nil {
		t.Fatalf("unmarshal the report: %v", err)
	}
	if answer.TrustDebt == nil {
		t.Error("the JSON of the report holds no trust_debt, want an empty list: a field a script reads is there whether it is empty or not")
	}
	if line := checkOf(t, report, separationCheck); line.Status != OK {
		t.Errorf("check %q = %q (%s), want ok", separationCheck, line.Status, line.Detail)
	}
	if authority := report.Authority; authority.OwnerIsOrchestrator || len(authority.Overlap) != 0 {
		t.Errorf("the report holds the separation %+v, want the three subjects apart and the lists not meeting", authority)
	}
}

// TestDR008TheListsAreShownAsTheCorrespondenceTheyAre: the two lists of the gate are
// written as logins and are counted by the number the host keeps each of those accounts
// under, and a report that shows only the logins cannot say what the gate will compare —
// so every account of both lists is shown with the name the file writes it under and the
// kind and the number under it (docs.DESIGN.md §7h, §7i, §7k).
func TestDR008TheListsAreShownAsTheCorrespondenceTheyAre(t *testing.T) {
	m := machineWithTheOrchestrator(t)
	config := writeConfig(t, m.orchestratorProject()+
		"\n[merge]\nreviewers = [\"crewflow-orchestrator[bot]\"]\nowners = [\"naghuale\"]\n")

	report := runOn(t, m, config)

	authority := report.Authority
	if len(authority.Reviewers) != 1 {
		t.Fatalf("the report holds the reviewers %v, want the one account the file names", authority.Reviewers)
	}
	for _, want := range []string{"crewflow-orchestrator[bot]", "Bot", "5107053"} {
		if got := authority.Reviewers[0].String(); !strings.Contains(got, want) {
			t.Errorf("the report shows the reviewer as %q, want it to name %q", got, want)
		}
	}
	if len(authority.Owners) != 1 || authority.Owners[0].Kind != "User" || authority.Owners[0].ID != 93920024 {
		t.Errorf("the report holds the owners %v, want the owner of the repository with the number of that account",
			authority.Owners)
	}
}

// TestDR009AnAccountOfTheListsTheHostCannotNameIsAFailureOfTheFile: a login of
// `[merge] owners` or `[merge] reviewers` that the host does not know, or holds as a kind
// crewflow does not read, is a mistake in that file — and the report says so instead of
// showing a section with a hole in it, because a gate that went on without that account
// would count no records at all (docs.DESIGN.md §7h, §7i, §7k).
func TestDR009AnAccountOfTheListsTheHostCannotNameIsAFailureOfTheFile(t *testing.T) {
	cases := []struct {
		name   string
		answer string
		// key is where the login stands in the file: the refusal has to name it, for a
		// person has to know which list to change.
		key string
	}{
		{
			name:   "the host knows no such account",
			answer: "gh: Not Found (HTTP 404)",
			key:    "merge.owners[0]",
		},
		{
			name:   "the account is an organization and not a person",
			answer: `{"id": 1, "login": "naghuale", "type": "Organization"}`,
			key:    "merge.owners[0]",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := machineWithTheOrchestrator(t)
			if strings.HasPrefix(tc.answer, "gh:") {
				m.fails("gh api users/naghuale", tc.answer)
			} else {
				m.prints("gh api users/naghuale", tc.answer)
			}
			config := writeConfig(t, m.orchestratorProject()+"\n[merge]\nowners = [\"naghuale\"]\n")

			report := runOn(t, m, config)

			line := checkOf(t, report, separationCheck)
			if line.Status != Fail {
				t.Fatalf("check %q = %q (%s), want fail: the file names an account of nobody",
					line.Name, line.Status, line.Detail)
			}
			for _, want := range []string{tc.key, "naghuale"} {
				if !strings.Contains(line.Detail, want) {
					t.Errorf("check %q detail = %q, want it to mention %q", line.Name, line.Detail, want)
				}
			}
			if !strings.Contains(line.Hint, "merge.owners") {
				t.Errorf("check %q hint = %q, want it to name the lists to change", line.Name, line.Hint)
			}
			if report.OK() {
				t.Error("report.OK() = true, want false: a gate that cannot name the owners counts no records")
			}
		})
	}
}
