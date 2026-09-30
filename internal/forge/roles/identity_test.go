package roles

import (
	"strings"
	"testing"

	"github.com/naghuale/crewflow/internal/forge/github"
	"github.com/naghuale/crewflow/internal/forge/github/app"
	"github.com/naghuale/crewflow/internal/secret"
)

// botConfig is the settings of a project whose executor works as an App: the mode,
// and the numbers that say which App it is. The key of the App is not in the file and
// never will be — it is in the store of the machine (docs/DESIGN.md §7e, §7i).
const botConfig = "\n[identity]\nmode = \"bot\"\n\n[identity.github_app]\napp_id = 5107052\ninstallation_id = 12345\n"

// TestNewGivesTheHostTheNumbersOfTheApp: the file of a project says which App its
// executor works as, and the adapter of GitHub is what a run asks — the core builds
// the role and hands the settings over, and knows nothing of an App itself (§7g, §7i).
func TestNewGivesTheHostTheNumbersOfTheApp(t *testing.T) {
	// The store of the machine of the test holds no key: nothing of an App is real
	// here, and a test that opened the keychain of a person would be a test of the
	// secrets of a person (docs/DESIGN.md §7i).
	m := newMachine()

	set, err := New(load(t, baseConfig+botConfig), m.env(t))
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}
	adapter, ok := set.Forge.(*github.Adapter)
	if !ok {
		t.Fatalf("the host is %T, want the adapter of GitHub", set.Forge)
	}
	if adapter.App() == nil {
		t.Fatal("the adapter of GitHub has no app, want the one the file of the project names")
	}
	if got := adapter.App().AppID; got != 5107052 {
		t.Errorf("the app of the project is %d, want 5107052", got)
	}
	if got := adapter.App().InstallationID; got != 12345 {
		t.Errorf("the installation of the app is %d, want 12345", got)
	}
}

// TestNewWithoutTheBotLeavesTheAdapterWithoutAnApp: a project that says nothing about
// [identity] works as the person who runs crewflow, and its adapter has no App: a
// report of it must not look for a key that nothing asked for (§7i).
func TestNewWithoutTheBotLeavesTheAdapterWithoutAnApp(t *testing.T) {
	set, err := New(load(t, baseConfig), newMachine().env(t))
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}
	adapter, ok := set.Forge.(*github.Adapter)
	if !ok {
		t.Fatalf("the host is %T, want the adapter of GitHub", set.Forge)
	}
	if adapter.App() != nil {
		t.Error("the adapter of a project in the mode of the owner has an app, want none")
	}
}

// TestNewGivesTheOpenerOnlyToTheBot: the change request of a run whose executor did
// not open one is opened by an account of the host, and only the mode of the bot has
// one. A run of the owner keeps the outcome it has always had (§7i).
func TestNewGivesTheOpenerOnlyToTheBot(t *testing.T) {
	owner, err := New(load(t, baseConfig), newMachine().env(t))
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}
	if owner.Opener != nil {
		t.Error("a project in the mode of the owner was given an opener, want none: its run opens nothing for itself")
	}

	bot, err := New(load(t, baseConfig+botConfig), newMachine().env(t))
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}
	if bot.Opener == nil {
		t.Error("a project in the mode of the bot was given no opener, want the account of the app")
	}
}

// TestNewAsksTheApiOfTheHostOfTheProject: the App of a project on a server of its own
// is asked through that server, and not through the public one: a token of an App is
// worth nothing outside the repository it was installed on, and asking the wrong
// server is a run refused for a reason nobody can act on (§7i).
func TestNewAsksTheApiOfTheHostOfTheProject(t *testing.T) {
	m := newMachine()
	cfg := load(t, baseConfig+botConfig+"\n[forge]\nkind = \"github\"\nhost = \"github.company.com\"\n")

	set, err := New(cfg, m.env(t))
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}
	adapter, ok := set.Forge.(*github.Adapter)
	if !ok {
		t.Fatalf("the host is %T, want the adapter of GitHub", set.Forge)
	}
	if got := adapter.App().BaseURL; !strings.HasPrefix(got, "https://github.company.com") {
		t.Errorf("the app is asked at %q, want the server of the project", got)
	}
}

// storeOfTheTest is a store of secrets that holds nothing: no test of crewflow opens
// the keychain of a person, and a project in the mode of the bot whose key was never
// imported has to be able to say so without touching a machine (docs/DESIGN.md §7i).
type storeOfTheTest struct {
	key []byte
}

// Get returns the key the test put in the store, and the error of an empty store
// when it has none, so that errors.Is says here what it says in a run.
func (s *storeOfTheTest) Get(service, account string) ([]byte, error) {
	if s.key == nil {
		return nil, secret.ErrNotFound
	}
	return s.key, nil
}

// Set keeps the key the test is given.
func (s *storeOfTheTest) Set(service, account string, value []byte) error {
	s.key = value
	return nil
}

// Has says whether a key is in the store, as the keychain of macOS can.
func (s *storeOfTheTest) Has(service, account string) (bool, error) { return s.key != nil, nil }

// orchestratorConfig is the settings of a project whose orchestrator works as an App
// of the host of its own, the second App of the project: the mode, and the numbers that
// say which App it is (docs/DESIGN.md §7i).
const orchestratorConfig = "\n[orchestrator]\nmode = \"separate\"\n\n[orchestrator.github_app]\napp_id = 5107053\ninstallation_id = 12346\n"

// TestAsOrchestratorGivesTheHostTheNumbersOfTheSecondApp: the file of a project says
// which App its orchestrator works as, and the adapter of GitHub is what a review and
// a merge ask — the App of the orchestrator, and not the App of the executor, which is
// the whole of §7i in two lines of settings.
func TestAsOrchestratorGivesTheHostTheNumbersOfTheSecondApp(t *testing.T) {
	m := newMachine()

	set, err := AsOrchestrator(load(t, baseConfig+botConfig+orchestratorConfig), m.env(t))
	if err != nil {
		t.Fatalf("AsOrchestrator returned an error: %v", err)
	}
	adapter, ok := set.Forge.(*github.Adapter)
	if !ok {
		t.Fatalf("the host is %T, want the adapter of GitHub", set.Forge)
	}
	if adapter.Orchestrator() == nil {
		t.Fatal("the adapter of the orchestrator has no app, want the one the file of the project names")
	}
	if got := adapter.Orchestrator().AppID; got != 5107053 {
		t.Errorf("the app of the orchestrator is %d, want 5107053", got)
	}
	if got := adapter.Orchestrator().InstallationID; got != 12346 {
		t.Errorf("the installation of the app of the orchestrator is %d, want 12346", got)
	}
	if got := adapter.Orchestrator().Role; got != app.Orchestrator {
		t.Errorf("the app of the orchestrator works as %q, want %q: its rights are not those of a run",
			got, app.Orchestrator)
	}
}

// TestAsOrchestratorOfAProjectInTheSharedModeHasNoSecondApp: a project that has not
// set the second App up keeps one login for the orchestrator and the owner, and the
// adapter it gets has no App of the orchestrator at all — a report of it must not look
// for a key nobody asked for (§7i).
func TestAsOrchestratorOfAProjectInTheSharedModeHasNoSecondApp(t *testing.T) {
	set, err := AsOrchestrator(load(t, baseConfig+botConfig), newMachine().env(t))
	if err != nil {
		t.Fatalf("AsOrchestrator returned an error: %v", err)
	}
	adapter, ok := set.Forge.(*github.Adapter)
	if !ok {
		t.Fatalf("the host is %T, want the adapter of GitHub", set.Forge)
	}
	if adapter.Orchestrator() != nil {
		t.Error("the adapter of a project in the shared mode has an app of the orchestrator, want none")
	}
}

// TestARunAndAReviewAreTwoSubjectsOfOneProject: the roles a run works under and the
// roles a review works under are two asks of the same project, and each of them gets
// the adapter of its own account: a run may push a branch and a review may not, and a
// review writes its record in the name of the orchestrator while a run writes nothing
// of the kind (§7i).
func TestARunAndAReviewAreTwoSubjectsOfOneProject(t *testing.T) {
	m := newMachine()
	cfg := load(t, baseConfig+botConfig+orchestratorConfig)

	asRun, err := New(cfg, m.env(t))
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}
	asReview, err := AsOrchestrator(cfg, m.env(t))
	if err != nil {
		t.Fatalf("AsOrchestrator returned an error: %v", err)
	}

	run, ok := asRun.Forge.(*github.Adapter)
	if !ok {
		t.Fatalf("the host of a run is %T, want the adapter of GitHub", asRun.Forge)
	}
	review, ok := asReview.Forge.(*github.Adapter)
	if !ok {
		t.Fatalf("the host of a review is %T, want the adapter of GitHub", asReview.Forge)
	}
	if review.Orchestrator() == nil {
		t.Error("a review of this project was given no app of the orchestrator, want the second one")
	}
	if run.Orchestrator() != nil {
		t.Error("a run of this project was given the app of the orchestrator, want none: a run is not a review")
	}
	// A run opens the change request of its own work, and a review opens nothing: the
	// work of a run is pushed by the executor and a review never pushes a branch.
	if asRun.Opener == nil || asReview.Opener != nil {
		t.Errorf("a run got the opener %v and a review %v, want them the other way round", asRun.Opener, asReview.Opener)
	}
}
