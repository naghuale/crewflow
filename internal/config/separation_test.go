package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// The two rules of the mode of an orchestrator apart from the owner, in the three cases
// the owner of the project named on 01.10 (docs/DESIGN.md §7i, §7k):
//
//	OR-005 the separate mode with the login of the owner among the accounts whose record
//	       of a review counts is a file that promises a separation it does not have;
//	OR-007 the shared mode with the same accounts among the owners and the reviewers is
//	       not an error — one login is both subjects in that mode by definition — and the
//	       debt of trust it is is shown by `crewflow doctor` (DR-001, DR-007);
//	OR-008 the separate mode with an account among the owners and among the reviewers is
//	       a file where one account approves a change and accepts its result.
//
// The records themselves are counted by the gate and are the cases OR-001…OR-004 and
// OR-006 of `internal/gate`; what is here is what a file may say before any record is
// written.

// apartConfig is the file of a project whose orchestrator works as an account of the
// host of its own: the mode, the numbers of the second app, and nothing else. The two
// lists of accounts are left out on purpose — an empty list is the owner of the
// repository for the owners, and the account of the app for the reviewers (docs/DESIGN.md
// §5, §7i).
const apartConfig = `
[orchestrator]
mode = "separate"

[orchestrator.github_app]
app_id = 5107053
`

// TestOR005TheOwnerMayNotBeTheOrchestratorOfTheSeparateMode: in the mode of an account
// of its own the login of the owner is not the login the orchestrator writes a review in,
// and a file that names the owner among the accounts whose record counts is refused
// before a review is written: the mode would promise a division the accounts do not make
// (docs/DESIGN.md §7i, §7k).
func TestOR005TheOwnerMayNotBeTheOrchestratorOfTheSeparateMode(t *testing.T) {
	cases := []struct {
		name string
		file string
		want string
	}{
		{
			name: "the owner of the repository among the reviewers",
			file: apartConfig + "\n[merge]\nreviewers = [\"crewflow-orchestrator[bot]\", \"naghuale\"]\n",
			want: "separate mode requires an account of its own",
		},
		{
			// The host writes a login in one case and a person writes it in another, and
			// the file is read in both: the account of the owner in other letters is the
			// account of the owner.
			name: "the same account in other letters",
			file: apartConfig + "\n[merge]\nreviewers = [\"crewflow-orchestrator[bot]\", \"Naghuale\"]\n",
			want: "separate mode requires an account of its own",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadString(t, baseConfig+tc.file)

			if err == nil {
				t.Fatalf("Load returned no error, want the refusal of a file where the owner is the orchestrator")
			}
			for _, want := range []string{tc.want, "shared mode"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal %q does not mention %q", err, want)
				}
			}
		})
	}
}

// TestOR007TheSharedModeWithTheOwnersAmongTheReviewersIsNotAnError: in the mode of a
// shared login the same account is both subjects, and a file that says so is a file
// crewflow works for — the load accepts it, and the debt it carries is what
// `crewflow doctor` shows and never hides (docs/DESIGN.md §7i, §7k).
func TestOR007TheSharedModeWithTheOwnersAmongTheReviewersIsNotAnError(t *testing.T) {
	for _, file := range []string{
		"",
		"\n[merge]\nowners = [\"naghuale\"]\n",
		"\n[merge]\nreviewers = [\"naghuale\"]\nowners = [\"naghuale\"]\n",
		"\n[merge]\nreviewers = [\"naghuale\", \"crewflow-executor[bot]\"]\nowners = [\"crewflow-executor[bot]\"]\n",
	} {
		cfg, err := loadString(t, baseConfig+file)
		if err != nil {
			t.Errorf("Load of a shared project with the accounts named %q returned an error: %v", file, err)
			continue
		}
		if cfg.Orchestrator.Mode != ModeShared {
			t.Errorf("Load of a project without a mode gave the mode %q, want the shared one", cfg.Orchestrator.Mode)
		}
		if err := Separation(cfg.Orchestrator.Mode, OwnersOf(cfg.Merge.Owners, cfg.Project.Repo),
			ReviewersOf(cfg.Merge.Reviewers, cfg.Project.Repo), cfg.Project.Repo); err != nil {
			t.Errorf("Separation in the shared mode = %v, want no refusal: one login is both subjects in that mode", err)
		}
	}
}

// TestOR008TheOwnersAndTheReviewersMayNotMeetInTheSeparateMode: an account in both lists
// of a project whose orchestrator works apart from the owner is an account that approves
// a change and accepts its result, and the load refuses the file before a review is
// written: the rule of the mode is that the subjects are separate (docs/DESIGN.md §7i, §7k).
func TestOR008TheOwnersAndTheReviewersMayNotMeetInTheSeparateMode(t *testing.T) {
	// The account is in other letters than the reviewers name it in, and it is the same
	// account: a gate that compared the letters would count the record of the
	// orchestrator as the decision of the owner (docs/DESIGN.md §7h).
	file := apartConfig + "\n[merge]\nreviewers = [\"crewflow-orchestrator[bot]\"]\nowners = [\"naghuale\", \"Crewflow-Orchestrator[BOT]\"]\n"

	_, err := loadString(t, baseConfig+file)

	if err == nil {
		t.Fatal("Load returned no error, want the refusal of a file whose lists meet")
	}
	for _, want := range []string{
		"separate mode requires role separation",
		"crewflow-orchestrator[bot]",
		"remove overlapping identities or switch to shared mode",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not mention %q", err, want)
		}
	}
}

// TestTheSeparateModeWithThreeAccountsLoads: the file the mode is for — the orchestrator
// under an account of the host of its own, the owner of the repository among the owners
// and not among the reviewers, the App of the executor in neither list — is a file that
// loads, and it is the only shape of the mode that does (docs/DESIGN.md §5, §7i).
func TestTheSeparateModeWithThreeAccountsLoads(t *testing.T) {
	cfg, err := loadString(t, baseConfig+apartConfig+
		"\n[merge]\nreviewers = [\"crewflow-orchestrator[bot]\"]\nowners = [\"naghuale\"]\n")

	if err != nil {
		t.Fatalf("Load returned an error: %v", err)
	}
	if got := OwnersOf(cfg.Merge.Owners, cfg.Project.Repo); len(got) != 1 || got[0] != "naghuale" {
		t.Errorf("the owners are %v, want the owner of the repository", got)
	}
	if err := Separation(cfg.Orchestrator.Mode, OwnersOf(cfg.Merge.Owners, cfg.Project.Repo),
		ReviewersOf(cfg.Merge.Reviewers, cfg.Project.Repo), cfg.Project.Repo); err != nil {
		t.Errorf("Separation = %v, want no refusal: the three subjects are three accounts", err)
	}
}

// loadString is the load of a file of a project written in the test, from the folder of
// the test and with the same two lines every case of the file needs.
func loadString(t *testing.T, file string) (Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "crewflow.toml")
	writeFile(t, path, file)
	return Load(path)
}
