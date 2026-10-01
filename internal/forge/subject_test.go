package forge

import (
	"strings"
	"testing"
)

// The subjects of the model of this package, as a host writes them: a person of a
// project of ours, the App of the orchestrator, and the App of the executor — the three
// accounts whose records the gate counts or does not count (docs/DESIGN.md §7h, §7i).
const (
	ownerLogin      = "naghuale"
	ownerID         = 93920024
	orchestrator    = "crewflow-orchestrator[bot]"
	slugOfApp       = "crewflow-orchestrator"
	orchestratorApp = 5140522
	executorApp     = 5107052
)

// TestID001AUserAndItsAccountNumberAreCounted: a record of a person is a record of that
// person when the host named the account and the number it keeps that account under —
// the login may be anything at all, because the login is not what a record is counted by
// (docs/DESIGN.md §7h, §7i).
func TestID001AUserAndItsAccountNumberAreCounted(t *testing.T) {
	author, err := SubjectFrom(ownerLogin, "User", ownerID, 0)
	if err != nil {
		t.Fatalf("SubjectFrom for a person returned an error: %v", err)
	}
	want := Subject{Kind: KindUser, ID: ownerID, Login: ownerLogin}
	if !author.Same(want) {
		t.Errorf("the subject of the record is %+v, want the owner %+v", author, want)
	}
	if author.Named() != "naghuale (User 93920024)" {
		t.Errorf("Named() = %q, want the login and the number the gate counts by", author.Named())
	}
}

// TestID002ABotAndTheNumberOfItsAppAreCounted: the record of an App is the record of the
// App and of nothing else, and the App is told apart by the number of the App — not by
// the login of its account, which changes when the App is renamed (docs/DESIGN.md §7h, §7i).
func TestID002ABotAndTheNumberOfItsAppAreCounted(t *testing.T) {
	author, err := SubjectFrom(orchestrator, "Bot", 336252606, orchestratorApp)
	if err != nil {
		t.Fatalf("SubjectFrom for an app returned an error: %v", err)
	}
	want := Subject{Kind: KindApp, ID: orchestratorApp, Login: orchestrator}
	if !author.Same(want) {
		t.Errorf("the subject of the record is %+v, want the app of the orchestrator %+v", author, want)
	}
}

// TestID003ToID005AnUnknownKindIsARefusal: an account whose kind the host writes and
// crewflow does not read is not a person and is not an App, and reading it as either
// hands its record to whoever shares its number — a kind crewflow does not know is a gap
// in what the host said, and a gap is what the gate refuses on (docs.DESIGN.md §7h, §7i).
func TestID003ToID005AnUnknownKindIsARefusal(t *testing.T) {
	cases := []struct {
		name string
		kind string
	}{
		{name: "a kind crewflow does not know", kind: "Mannequin"},
		{name: "no kind at all", kind: ""},
		{name: "the kind the host writes for an organization", kind: "Organization"},
		{name: "the kind the host writes for an application", kind: "Application"},
		{name: "the kind is null", kind: "null"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := SubjectFrom(orchestrator, tc.kind, 1, orchestratorApp); err == nil {
				t.Fatal("SubjectFrom read a record of an account of a kind crewflow does not know")
			}
		})
	}
}

// TestID006TheRightLoginAndAForeignAppIsARefusal: the login of the account of the App of
// the orchestrator is what the host writes for every App, and it is written for foreign
// Apps as well — a record that carries that login and the number of another App is a
// record of that other App, and it counts for nothing here (docs.DESIGN.md §7h, §7i).
func TestID006TheRightLoginAndAForeignAppIsARefusal(t *testing.T) {
	author, err := SubjectFrom(orchestrator, "Bot", 336252606, executorApp)
	if err != nil {
		t.Fatalf("SubjectFrom for an app returned an error: %v", err)
	}
	orchestratorOfProject := Subject{Kind: KindApp, ID: orchestratorApp, Login: orchestrator}
	if author.Same(orchestratorOfProject) {
		t.Error("a record of another App under the login of this one counts as its record")
	}
}

// TestID007TheRightAppUnderAnotherNameIsCounted: an App is renamed and its account with
// it, and the record of that App under its new name is the very record the gate counted
// before the rename — this is the case the whole of this model is for (F-081, §7h, §7i).
func TestID007TheRightAppUnderAnotherNameIsCounted(t *testing.T) {
	author, err := SubjectFrom("crewflow-orchestrator-renamed[bot]", "Bot", 336252606, orchestratorApp)
	if err != nil {
		t.Fatalf("SubjectFrom for a renamed app returned an error: %v", err)
	}
	orchestratorOfProject := Subject{Kind: KindApp, ID: orchestratorApp, Login: orchestrator}
	if !author.Same(orchestratorOfProject) {
		t.Error("the record of the app of the orchestrator under its new name is not its record")
	}
}

// TestID008APersonWhoseLoginIsCalledAnAppIsARefusal: the host does not give that login
// to a person, so an answer with it says that something is broken rather than that there
// is a person — and reading it as a person would hand a record to whoever wears that name
// (docs/DESIGN.md §7h, §7i).
func TestID008APersonWhoseLoginIsCalledAnAppIsARefusal(t *testing.T) {
	_, err := SubjectFrom(orchestrator, "User", 339150227, 0)
	if err == nil {
		t.Fatal("SubjectFrom read a person whose login is called an app")
	}
	if !strings.Contains(err.Error(), "called an app") {
		t.Errorf("the error %q does not say what was wrong with the answer of the host", err)
	}
}

// TestASubjectTheHostNamedNoNumberForIsNobody: a bot that did not act as an App is a
// record of the change and not a subject anybody may count by — it is not read as a
// person, and it does not match an App either (docs/DESIGN.md §7h).
func TestASubjectTheHostNamedNoNumberForIsNobody(t *testing.T) {
	author, err := SubjectFrom(orchestrator, "Bot", 336252606, 0)
	if err != nil {
		t.Fatalf("SubjectFrom for an app without a number returned an error: %v", err)
	}
	if author.Same(Subject{Kind: KindApp, ID: orchestratorApp, Login: orchestrator}) {
		t.Error("a record with no app number counts as the record of the app of the project")
	}
	if author.Same(Subject{Kind: KindUser, ID: 336252606, Login: ownerLogin}) {
		t.Error("a record with no app number counts as the record of a person")
	}
	if got := author.String(); got != orchestrator {
		t.Errorf("String() = %q, want the login the host wrote", got)
	}
}

// TestASubjectNobodyCouldNameIsSaidSoAndNotLeftBlank: a report that shows an empty column
// leaves a person to guess whether the host could not be asked or the project has no
// account of its own (docs.DESIGN.md §7i).
func TestASubjectNobodyCouldNameIsSaidSoAndNotLeftBlank(t *testing.T) {
	for _, subject := range []Subject{
		{},
		{Kind: KindUser, ID: ownerID},
		{Kind: KindApp, ID: orchestratorApp},
	} {
		if got := subject.String(); got == "" {
			t.Errorf("String() of %+v is blank, want what it is", subject)
		}
	}
	if got := (Subject{}).String(); got != "nobody" {
		t.Errorf("String() of a subject the host named nothing for = %q, want %q", got, "nobody")
	}
	if got := (Subject{Kind: KindUser, ID: ownerID}).Named(); got != "User 93920024" {
		t.Errorf("Named() of a subject with no login = %q, want the kind and the number", got)
	}
}
