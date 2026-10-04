package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// projectWithAnEnvironment is a config of a project that wrote [environment] down, built
// in the hands of a test and not read from a file: the resolver is a function of the
// struct and of the home it is given, and a test says so without a disk.
func projectWithAnEnvironment(contour, stateRoot, worktreesRoot string) Config {
	return Config{
		Project: Project{Repo: "naghuale/crewflow"},
		Environment: Environment{
			Contour:       contour,
			StateRoot:     stateRoot,
			WorktreesRoot: worktreesRoot,
		},
	}
}

// TestResolveContourOfAFileThatNamesNoContour is the classification of the contour in one
// test: a file without [environment] is legacy-unclassified and not a dev contour.
// Calling it dev would reclassify the runs a project had before the key was there by
// nobody's word, and the paths of the machine would move under them.
func TestResolveContourOfAFileThatNamesNoContour(t *testing.T) {
	home := t.TempDir()
	cfg := projectWithAnEnvironment("", "", "")

	layout, err := cfg.ResolveContour(home)
	if err != nil {
		t.Fatalf("ResolveContour returned an error: %v", err)
	}

	if layout.Contour != ContourLegacy {
		t.Errorf("contour = %q, want %q: a file that names none is unclassified, and dev is not the same word",
			layout.Contour, ContourLegacy)
	}
	if layout.Declared {
		t.Error("Declared = true, want false: nobody wrote a contour down")
	}
	if layout.Home != home {
		t.Errorf("Home = %q, want the home the resolver was given (%q)", layout.Home, home)
	}
	// A project that says nothing keeps the paths the machine has today: the answer of a
	// legacy file is not a proposal to move anything.
	want := filepath.Join(home, ".crewflow", "state", "naghuale-crewflow")
	if layout.State != want {
		t.Errorf("the proposed state root = %q, want the path of the machine today (%q)", layout.State, want)
	}
	want = filepath.Join(home, ".crewflow", "worktrees", "naghuale-crewflow")
	if layout.Worktrees != want {
		t.Errorf("the proposed worktrees root = %q, want the path of the machine today (%q)", layout.Worktrees, want)
	}
	if layout.StateNamed || layout.WorktreesNamed {
		t.Error("a root is named, want none: a file that says nothing named no root")
	}
}

// TestResolveContourOfEveryContourAProjectNames walks the three words of the closed list:
// each of them is a contour crewflow knows, each of them is proposed under a folder of its
// own, and each of them is declared — the difference from legacy that a person has to be
// able to see in an answer.
func TestResolveContourOfEveryContourAProjectNames(t *testing.T) {
	home := t.TempDir()
	for _, contour := range []string{ContourDev, ContourTest, ContourProd} {
		t.Run(contour, func(t *testing.T) {
			cfg := projectWithAnEnvironment(contour, "", "")

			layout, err := cfg.ResolveContour(home)
			if err != nil {
				t.Fatalf("ResolveContour returned an error: %v", err)
			}

			if layout.Contour != contour {
				t.Errorf("contour = %q, want %q", layout.Contour, contour)
			}
			if !layout.Declared {
				t.Error("Declared = false, want true: the file wrote this word down")
			}
			state := filepath.Join(home, ".crewflow", contour, "state", "naghuale-crewflow")
			if layout.State != state {
				t.Errorf("the proposed state root = %q, want %q", layout.State, state)
			}
			worktrees := filepath.Join(home, ".crewflow", contour, "worktrees", "naghuale-crewflow")
			if layout.Worktrees != worktrees {
				t.Errorf("the proposed worktrees root = %q, want %q", layout.Worktrees, worktrees)
			}
		})
	}
}

// TestResolveContourRefusesAWordNobodyDefined is the closed list at the resolver as well
// as at the load: a Config built by hand is refused the same words a file is refused, and
// the refusal is the same one every time — the key, the cleaned word and the three words
// that are there.
func TestResolveContourRefusesAWordNobodyDefined(t *testing.T) {
	home := t.TempDir()
	for _, tc := range []struct {
		name, contour string
		want          []string
	}{
		{
			name:    "a word of another vocabulary",
			contour: "staging",
			want:    []string{"environment.contour", `"staging"`, "dev, test, prod"},
		},
		{
			name:    "a word with a space around it",
			contour: " dev ",
			want:    []string{"environment.contour", `"dev"`, "dev, test, prod"},
		},
		{
			name:    "a word of no kind",
			contour: "PROD",
			want:    []string{"environment.contour", `"PROD"`, "dev, test, prod"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := projectWithAnEnvironment(tc.contour, "", "")

			_, err := cfg.ResolveContour(home)

			if err == nil {
				t.Fatalf("ResolveContour of the contour %q returned no error, want the refusal of the closed list",
					tc.contour)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
			if trimmed := strings.TrimSpace(tc.contour); trimmed != tc.contour &&
				strings.Contains(err.Error(), tc.contour) {
				t.Errorf("error %q carries the raw word, want the cleaned one only", err)
			}
		})
	}
}

// TestResolveContourTakesTheRootsAProjectNames: a root a project wrote for itself wins
// over the root crewflow would propose, one kind at a time, and the answer says which of
// the two roots came from the file. An absolute root of the project is a choice and not a
// mistake, and {repo} keeps the two projects apart there as it does by default.
func TestResolveContourTakesTheRootsAProjectNames(t *testing.T) {
	home := t.TempDir()
	proposed := func(contour, kind string) string {
		return filepath.Join(home, ".crewflow", contour, kind, "naghuale-crewflow")
	}
	for _, tc := range []struct {
		name, stateRoot, worktreesRoot string
		wantState, wantWorktrees       string
		wantStateNamed, wantNamed      bool
	}{
		{
			name:           "one root of the two",
			stateRoot:      "~/spaces/tele-state",
			wantState:      filepath.Join(home, "spaces", "tele-state"),
			wantWorktrees:  proposed(ContourDev, "worktrees"),
			wantStateNamed: true,
		},
		{
			name:           "both roots, absolute",
			stateRoot:      "/var/crewflow/tele-state",
			worktreesRoot:  "/var/crewflow/tele-worktrees",
			wantState:      "/var/crewflow/tele-state",
			wantWorktrees:  "/var/crewflow/tele-worktrees",
			wantStateNamed: true,
			wantNamed:      true,
		},
		{
			name:          "the placeholder of the repository keeps the projects apart",
			worktreesRoot: "~/spaces/{repo}",
			wantState:     proposed(ContourDev, "state"),
			wantWorktrees: filepath.Join(home, "spaces", "naghuale-crewflow"),
			wantNamed:     true,
		},
		{
			name:           "a root with a space around it is the root without it",
			stateRoot:      " ~/spaces/tele-state ",
			wantState:      filepath.Join(home, "spaces", "tele-state"),
			wantWorktrees:  proposed(ContourDev, "worktrees"),
			wantStateNamed: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := projectWithAnEnvironment(ContourDev, tc.stateRoot, tc.worktreesRoot)

			layout, err := cfg.ResolveContour(home)
			if err != nil {
				t.Fatalf("ResolveContour returned an error: %v", err)
			}

			if layout.State != tc.wantState {
				t.Errorf("the state root = %q, want %q", layout.State, tc.wantState)
			}
			if layout.Worktrees != tc.wantWorktrees {
				t.Errorf("the worktrees root = %q, want %q", layout.Worktrees, tc.wantWorktrees)
			}
			if layout.StateNamed != tc.wantStateNamed {
				t.Errorf("StateNamed = %t, want %t", layout.StateNamed, tc.wantStateNamed)
			}
			if layout.WorktreesNamed != tc.wantNamed {
				t.Errorf("WorktreesNamed = %t, want %t", layout.WorktreesNamed, tc.wantNamed)
			}
		})
	}
}

// TestResolveContourRefusesARootThatIsNoPath: a root that climbs out of the folder it
// starts in and a relative root are two mistakes of a person and not two file spaces. The
// resolver refuses them as the load does, because a Config built by hands of a program
// reaches it too, and it is the resolver that proposes the space.
func TestResolveContourRefusesARootThatIsNoPath(t *testing.T) {
	home := t.TempDir()
	for _, tc := range []struct {
		name, key, stateRoot, worktreesRoot string
	}{
		{"climbing out", "environment.state_root", "~/spaces/../etc", ""},
		{"climbing out with the other separator", "environment.state_root", `~/spaces\..\etc`, ""},
		{"climbing out of the home", "environment.worktrees_root", "", "../outside"},
		{"relative", "environment.state_root", "spaces/state", ""},
		{
			"a backslash of a windows path is not a separator on this machine",
			"environment.state_root", `spaces\state`, "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := projectWithAnEnvironment(ContourTest, tc.stateRoot, tc.worktreesRoot)

			_, err := cfg.ResolveContour(home)

			if err == nil {
				t.Fatalf("ResolveContour with the roots %q and %q returned no error, "+
					"want the refusal of a key that is no path", tc.stateRoot, tc.worktreesRoot)
			}
			if !strings.Contains(err.Error(), tc.key) {
				t.Errorf("error %q does not name the key %q", err, tc.key)
			}
		})
	}
}

// TestResolveContourRefusesTwoContoursInOneFolder is the collision of the peer-set: a root
// of this contour that is a folder of another contour is one space of files whatever the
// machine calls it, and it is refused under the key the person wrote. The peers are the
// two roots of every other contour crewflow knows and the two roots of a file that names
// no contour — the closed set the check sees, and the whole of it.
func TestResolveContourRefusesTwoContoursInOneFolder(t *testing.T) {
	home := t.TempDir()
	derivedOfDev := filepath.Join(home, ".crewflow", "dev", "worktrees", "naghuale-crewflow")
	derivedOfTest := filepath.Join(home, ".crewflow", "test", "worktrees", "naghuale-crewflow")
	legacyOfWorktrees := filepath.Join(home, ".crewflow", "worktrees", "naghuale-crewflow")
	for _, tc := range []struct {
		name, contour, stateRoot string
		want                     []string
	}{
		{
			name:      "the worktrees of another contour, word for word",
			contour:   ContourTest,
			stateRoot: "~/.crewflow/dev/worktrees/{repo}",
			want: []string{
				"environment.state_root", `state of the contour "test"`, `worktrees of the contour "dev"`,
				derivedOfDev,
			},
		},
		{
			name:      "a folder that holds the worktrees of another contour",
			contour:   ContourTest,
			stateRoot: "~/.crewflow/dev",
			want:      []string{"environment.state_root", `state of the contour "test"`, `state of the contour "dev"`},
		},
		{
			name:      "a folder inside the worktrees of another contour",
			contour:   ContourProd,
			stateRoot: "~/.crewflow/test/worktrees/{repo}/inner",
			want: []string{
				"environment.state_root", `state of the contour "prod"`,
				`worktrees of the contour "test"`, derivedOfTest,
			},
		},
		{
			name:      "the space of a file that names no contour",
			contour:   ContourDev,
			stateRoot: "~/.crewflow/worktrees/{repo}",
			want: []string{
				"environment.state_root", `state of the contour "dev"`,
				`worktrees of the contour "` + ContourLegacy + `"`, legacyOfWorktrees,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := projectWithAnEnvironment(tc.contour, tc.stateRoot, "")

			_, err := cfg.ResolveContour(home)

			if err == nil {
				t.Fatalf("ResolveContour with the state root %q returned no error, "+
					"want the refusal of two contours in one folder", tc.stateRoot)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

// TestResolveContourRefusesOneFolderForTheStateAndTheWorktrees: the two roots of one
// contour in one folder is refused as well, and for the same reason as between contours —
// the files of a state among the worktrees of a task are read and written by everything
// that works in that folder.
func TestResolveContourRefusesOneFolderForTheStateAndTheWorktrees(t *testing.T) {
	home := t.TempDir()
	cfg := projectWithAnEnvironment(ContourTest, "~/spaces/tele", "~/spaces/tele")

	_, err := cfg.ResolveContour(home)

	if err == nil {
		t.Fatal("ResolveContour with one folder for the state and the worktrees returned no error, want a refusal")
	}
	for _, want := range []string{
		"environment.state_root", `state of the contour "test"`, "worktrees", filepath.Join(home, "spaces", "tele"),
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// TestResolveContourAcceptsNestingInsideTheContourItself holds the other half of the rule:
// a folder inside another folder is not a collision, and refusing every nesting would
// refuse ordinary choices — the worktrees of a contour under its state, a project that
// keeps everything it works in under one folder of its own.
func TestResolveContourAcceptsNestingInsideTheContourItself(t *testing.T) {
	home := t.TempDir()
	for _, tc := range []struct {
		name, stateRoot, worktreesRoot string
	}{
		{"worktrees under the state of the same contour", "~/spaces/tele", "~/spaces/tele/worktrees"},
		{"state under the worktrees of the same contour", "~/spaces/tele/state", "~/spaces/tele"},
		{"everything under one folder of the project", "~/spaces/tele", "~/spaces/tele/repo"},
		{"a folder whose name only starts with the name of another one", "~/spaces/tele", "~/spaces/tele-test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := projectWithAnEnvironment(ContourTest, tc.stateRoot, tc.worktreesRoot)

			if _, err := cfg.ResolveContour(home); err != nil {
				t.Fatalf("ResolveContour returned an error: %v", err)
			}
		})
	}
}

// TestResolveContourTouchesNothingOfTheMachine: the resolver is a function of the file and
// of the home it was given. It creates no folder — the home it is given has none — and it
// reads nothing: the answer of a home that was never made is the same answer.
func TestResolveContourTouchesNothingOfTheMachine(t *testing.T) {
	home := filepath.Join(t.TempDir(), "a-home-nobody-made")
	cfg := projectWithAnEnvironment(ContourDev, "", "")

	layout, err := cfg.ResolveContour(home)
	if err != nil {
		t.Fatalf("ResolveContour of a home that does not exist returned an error: %v", err)
	}

	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Errorf("the resolver made %s, want nothing created: the error of the stat is %v", home, err)
	}
	want := filepath.Join(home, ".crewflow", "dev", "state", "naghuale-crewflow")
	if layout.State != want {
		t.Errorf("the state root = %q, want the answer computed out of the words alone (%q)", layout.State, want)
	}
}

// TestResolveContourLeavesTheConfigWhereItWas is the promise of the contour keys about the
// runtime: the resolver returns a separate value and writes nothing back. The paths of the
// runtime — `worktrees.root` first of them — stay what the file said, so `task run`,
// `task resume` and `merge` keep making a worktree where they always did.
func TestResolveContourLeavesTheConfigWhereItWas(t *testing.T) {
	cfg := projectWithAnEnvironment(ContourProd, "~/spaces/prod-state", "")
	before := cfg

	for range 3 {
		if _, err := cfg.ResolveContour(t.TempDir()); err != nil {
			t.Fatalf("ResolveContour returned an error: %v", err)
		}
	}

	if cfg.Worktrees.Root != before.Worktrees.Root {
		t.Errorf("worktrees.root = %q after the resolver, want %q: nothing of the runtime moves",
			cfg.Worktrees.Root, before.Worktrees.Root)
	}
	if cfg.Environment != before.Environment {
		t.Errorf("environment = %#v after the resolver, want %#v", cfg.Environment, before.Environment)
	}
}

// TestLoadingAConfigTouchesNothingOfTheMachine is the same promise on the side of the load:
// reading a file that names a contour is the same read as before the keys were there — the
// file itself and nothing else — so the home of the machine gains no folder, no file, no
// question of the network and no window of the store of secrets. Nothing is proposed, nothing
// is created and nothing is routed: the proposal is what [Config.ResolveContour] answers when
// somebody asks it.
func TestLoadingAConfigTouchesNothingOfTheMachine(t *testing.T) {
	home := t.TempDir()
	stubHome(t, home)
	path := filepath.Join(t.TempDir(), "crewflow.toml")
	writeFile(t, path, `
[project]
repo = "naghuale/crewflow"

[executor]
command = ["agent", "run", "{prompt}"]

[environment]
contour = "prod"
state_root = "~/spaces/prod-state"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned an error: %v", err)
	}
	if cfg.Environment.Contour != ContourProd {
		t.Errorf("environment.contour = %q, want %q", cfg.Environment.Contour, ContourProd)
	}

	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatalf("read the home of the test: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("the load of the file put %d entries into the home of the machine, want none", len(entries))
	}
	if cfg.Worktrees.Root != DefaultWorktreesRoot {
		t.Errorf("worktrees.root = %q, want %q: a contour in the file moves nothing of the runtime",
			cfg.Worktrees.Root, DefaultWorktreesRoot)
	}
}

// TestResolveContourAndExpandPathAgree is the tie between the resolver and the expansion
// the runtime uses: both expand "{repo}" and a leading "~/" the same way. The two walk
// different files of this package, so a word that one of them expands and the other does
// not would put the worktrees of a run and the proposal of a contour in different folders
// of the same project — and a test of agreement is what keeps that from happening
// unnoticed.
func TestResolveContourAndExpandPathAgree(t *testing.T) {
	home := t.TempDir()
	stubHome(t, home)
	for _, root := range []string{
		"~/spaces/state",
		"~/spaces/{repo}/inner",
		"/var/crewflow/{repo}",
	} {
		t.Run(root, func(t *testing.T) {
			cfg := projectWithAnEnvironment(ContourTest, root, "")

			layout, err := cfg.ResolveContour(home)
			if err != nil {
				t.Fatalf("ResolveContour returned an error: %v", err)
			}
			expanded, err := cfg.ExpandPath(root)
			if err != nil {
				t.Fatalf("ExpandPath(%q) returned an error: %v", root, err)
			}

			if layout.State != expanded {
				t.Errorf("the resolver read the root %q as %q and ExpandPath as %q, want the same answer",
					root, layout.State, expanded)
			}
			if layout.Contour != ContourTest {
				t.Errorf("contour = %q, want %q", layout.Contour, ContourTest)
			}
		})
	}
}

// TestTheClosedListOfContoursHoldsTheWordsOfTheFile: the list Validate reads and the list
// the resolver walks the peers by are one list, and the word of a file that names no
// contour is in neither — it is what crewflow answers instead of a word, and a project
// that could write it would be naming a fourth contour of a list of three.
func TestTheClosedListOfContoursHoldsTheWordsOfTheFile(t *testing.T) {
	want := []string{ContourDev, ContourTest, ContourProd}
	if !slices.Equal(contours, want) {
		t.Errorf("the closed list of the contours = %v, want %v", contours, want)
	}
	if slices.Contains(contours, ContourLegacy) {
		t.Errorf("%q is among the words of the file, want it to be an answer and not a word",
			ContourLegacy)
	}
}
