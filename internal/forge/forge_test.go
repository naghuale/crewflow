package forge

import (
	"context"
	"errors"
	"testing"
)

// fake is a role of a system crewflow has no adapter for, in the tests: it
// answers every question with nothing and counts how often it was asked to check
// the machine.
type fake struct {
	name    string
	checked int
}

func (f *fake) Doctor(_ context.Context) []Check {
	f.checked++
	return []Check{{Name: f.name, Status: OK, Detail: "installed"}}
}

func (f *fake) Task(context.Context, int) (Task, error) { return Task{}, nil }

func (f *fake) FindChangeRequest(context.Context, string) (ChangeRequest, bool, error) {
	return ChangeRequest{}, false, nil
}

func (f *fake) ChangeRequest(context.Context, int) (ChangeRequest, error) {
	return ChangeRequest{}, nil
}

func (f *fake) Comments(context.Context, int) ([]Comment, error) { return nil, nil }

func (f *fake) Status(context.Context, string) (CheckState, error) { return CheckNone, nil }

// TestSetCheckersOneAdapter checks what the default settings lead to: the same
// adapter serves all three roles, and the machine is checked once and not three
// times.
func TestSetCheckersOneAdapter(t *testing.T) {
	adapter := &fake{name: "gh"}
	set := Set{Forge: adapter, Tracker: adapter, CI: adapter}

	checkers := set.Checkers()

	if len(checkers) != 1 {
		t.Fatalf("Checkers() = %d adapters, want the one that serves all three roles", len(checkers))
	}
	if got := checkers[0].Doctor(t.Context()); len(got) != 1 || got[0].Name != "gh" {
		t.Errorf("the checks of the adapter = %v, want the line it makes", got)
	}
	if adapter.checked != 1 {
		t.Errorf("the adapter was asked %d times, want once", adapter.checked)
	}
}

// TestSetCheckersInTheOrderOfTheRoles checks that the machine is checked in the
// order a report promises: the host first, the tasks after it, the CI last.
func TestSetCheckersInTheOrderOfTheRoles(t *testing.T) {
	hosting, tasks, checks := &fake{name: "gh"}, &fake{name: "jira"}, &fake{name: "jenkins"}

	checkers := Set{Forge: hosting, Tracker: tasks, CI: checks}.Checkers()

	if len(checkers) != 3 {
		t.Fatalf("Checkers() = %d adapters, want all three", len(checkers))
	}
	for i, want := range []string{"gh", "jira", "jenkins"} {
		if got := checkers[i].Doctor(t.Context()); got[0].Name != want {
			t.Errorf("adapter %d checks %q, want %q", i, got[0].Name, want)
		}
	}
}

// TestSetCheckersWithoutARole checks a project that has no host and no CI of a
// host: what is not there is not checked.
func TestSetCheckersWithoutARole(t *testing.T) {
	set := Set{Tracker: &fake{name: "files"}}

	checkers := set.Checkers()

	if len(checkers) != 1 {
		t.Fatalf("Checkers() = %d adapters, want only the one of the tasks", len(checkers))
	}
	if got := checkers[0].Doctor(t.Context()); got[0].Name != "files" {
		t.Errorf("the checks of the adapter = %v, want the lines of the files", got)
	}
}

// TestErrNotImplemented checks the answer for a kind of a role crewflow has no
// adapter for: it names the setting and the kind, so that a person knows which
// key to change, and a caller may tell it apart from any other error.
func TestErrNotImplemented(t *testing.T) {
	err := error(&ErrNotImplemented{Key: "forge.kind", Kind: "gitlab"})

	if want := "forge.kind: adapter gitlab is not implemented yet"; err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
	var notImplemented *ErrNotImplemented
	if !errors.As(err, &notImplemented) {
		t.Fatalf("errors.As did not find *ErrNotImplemented in %v", err)
	}
	if notImplemented.Key != "forge.kind" || notImplemented.Kind != "gitlab" {
		t.Errorf("error holds %+v, want the key forge.kind and the kind gitlab", notImplemented)
	}
}
