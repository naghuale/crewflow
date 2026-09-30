package gate

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/naghuale/crewflow/internal/forge"
)

// TestReadChangeOfAMergedChange is the facts of a change that has gone in, as a host
// holds it: `merged`, the head of the change still named, `Closes #N` in the body. A
// check after a merge is made of these and of no rule of the gate, and the head of a
// change nobody may merge any more is still the commit that was merged
// (docs/DESIGN.md §6, §7h).
func TestReadChangeOfAMergedChange(t *testing.T) {
	host := theHost()
	host.change.State = "merged"

	change := ReadChange(t.Context(), Deps{Forge: host, Task: 7}, 7)

	if change.Unavailable != "" {
		t.Fatalf("ReadChange could not read the change: %s", change.Unavailable)
	}
	if change.State != "merged" || change.Head != second || change.URL == "" {
		t.Errorf("ReadChange = %+v, want a merged change at the head %s", change, second)
	}
}

// TestReadChangeOfAChangeMergedElsewhere is a change of a task this machine kept no
// state of: the run that opened it was on another machine, and the only thing that
// still says which task it was of is the `Closes #7` in its body. A check that cannot
// find the task in a state file of this machine says the task is not known, and the
// cycle of that task goes unclosed for a run crewflow never saw (docs/DESIGN.md §7h).
func TestReadChangeOfAChangeMergedElsewhere(t *testing.T) {
	host := theHost()
	host.change.State = "merged"

	change := ReadChange(t.Context(), Deps{Forge: host}, 7)

	if change.Task != 7 {
		t.Errorf("the task of the change is %d, want 7: `Closes #7` is what the body says", change.Task)
	}
}

// TestReadChangeSaysWhatTheHostCouldNotBeAsked is the whole of what a change that was
// not read is worth: the reason, and not a change that names no head. A report that
// says the second instead of the first sends a person to look for a fault of a change
// that may not have one (docs/DESIGN.md §6, §7h).
func TestReadChangeSaysWhatTheHostCouldNotBeAsked(t *testing.T) {
	host := theHost()
	host.noChange = errors.New("gh pr view 7 -R naghuale/crewflow: exited with 1: could not reach the host")

	change := ReadChange(t.Context(), Deps{Forge: host, Task: 7}, 7)

	if !strings.Contains(change.Unavailable, "could not reach the host") {
		t.Errorf("the reason is %q, want what the host could not be asked", change.Unavailable)
	}
	if change.Head != "" {
		t.Errorf("the change is at %q, want no head of a change nobody read", change.Head)
	}
}

// TestReadChangeOfAProjectWithNoHost: a project that is not hosted anywhere has no
// change request to read, and the check says so instead of reading one of nothing
// (docs/DESIGN.md §7h).
func TestReadChangeOfAProjectWithNoHost(t *testing.T) {
	change := ReadChange(t.Context(), Deps{}, 7)

	if !strings.Contains(change.Unavailable, "no host of its code") {
		t.Errorf("the reason is %q, want the project to be said to have no host of its code", change.Unavailable)
	}
}

// TestReadChangeAsksTheHostOnlyForTheChange is that a check after the merge asks the
// host for the change and for nothing else: the rules of a gate are about a change that
// may go in, and a change that has gone in is not one. A host that answers nothing but
// its change requests is enough for such a check, and a check that asked it for more
// would be refused for a question it has no reason to ask (docs/DESIGN.md §7h).
func TestReadChangeAsksTheHostOnlyForTheChange(t *testing.T) {
	host := theHost()
	head := host.change.HeadSHA
	host.change.State = "merged"

	got := ReadChange(t.Context(), Deps{Forge: onlyChange{host}}, 7)

	if got.Unavailable != "" {
		t.Errorf("ReadChange could not read the change: %s", got.Unavailable)
	}
	if got.Head != head {
		t.Errorf("the change is at %q, want the head %s of a change that was merged", got.Head, head)
	}
}

// onlyChange is a host that says what a change request is and nothing else about it —
// no records under it, no files, no checks, no rules of a branch: the shape of a host
// after the change has gone in, where a check after the merge is the only thing that
// asks anything (docs/DESIGN.md §7h).
type onlyChange struct{ *host }

func (onlyChange) Comments(context.Context, int) ([]forge.Comment, error) {
	return nil, errors.New("there are no records to read under a change that has gone in")
}

func (onlyChange) ChangedFiles(context.Context, int) ([]string, error) {
	return nil, errors.New("a change that has gone in is diffed by nobody")
}

func (onlyChange) Checks(context.Context, string) ([]forge.CheckRun, error) {
	return nil, errors.New("a change that has gone in has no checks to ask for")
}

func (onlyChange) RequiredChecks(context.Context) ([]forge.RequiredCheck, error) {
	return nil, errors.New("a change that has gone in has no rules of a branch to ask for")
}

var _ forge.Forge = onlyChange{}
