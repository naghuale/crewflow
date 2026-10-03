package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	taskrun "github.com/naghuale/crewflow/internal/run"
	"github.com/naghuale/crewflow/internal/secret"
)

// TestRunTaskRunThatWaitedForTheKeychainSaysSoToThePersonAndToTheScript is the whole of
// the failure this is here for, at the level a person and an orchestrator read it: the
// keychain of macOS asks the owner of the machine in a window of the system, a run of the
// mode of the bot stands in front of that window, and the four runs of the pilot that
// stood still for an hour and a half had not one line in their journal to say why
// (docs/DESIGN.md §7i).
//
// So the run ends as `blocked: keychain-approval` with the reason of the wait in it, and
// both of them are where they are read: the report of `task run` for a person and the
// answer of `-json` for the orchestrator that has to decide what happens to the task.
func TestRunTaskRunThatWaitedForTheKeychainSaysSoToThePersonAndToTheScript(t *testing.T) {
	t.Run("the report of a person", func(t *testing.T) {
		host, project := hostWithoutTheKey(t)
		var stdout, stderr bytes.Buffer

		code := run([]string{"task", "run", "43", "-config", project}, &stdout, &stderr)

		if code != exitFailure {
			t.Fatalf("crewflow task run = %d, want %d: a run that was not answered is not a run that opened a change request",
				code, exitFailure)
		}
		for _, want := range []string{"task 43: blocked", "the run stopped: keychain-approval", "journal"} {
			if !strings.Contains(stdout.String(), want) {
				t.Errorf("crewflow task run wrote %q, want it to hold %q", stdout.String(), want)
			}
		}
		// The work of the task is untouched: the executor was never started, so there is
		// no work to lose.
		if len(host.started) != 0 {
			t.Errorf("the executor was started %v, want nothing started", host.started)
		}
	})
	t.Run("the answer of the orchestrator", func(t *testing.T) {
		_, project := hostWithoutTheKey(t)
		var stdout, stderr bytes.Buffer

		code := run([]string{"task", "run", "43", "-config", project, "-json"}, &stdout, &stderr)

		if code != exitFailure {
			t.Fatalf("crewflow task run = %d, want %d", code, exitFailure)
		}
		var answer taskrun.Result
		if err := json.Unmarshal(stdout.Bytes(), &answer); err != nil {
			t.Fatalf("the answer of -json is not the answer of a run: %v\n%s", err, stdout.String())
		}
		if answer.Outcome != taskrun.Blocked {
			t.Errorf("the outcome in the answer = %q, want %q", answer.Outcome, taskrun.Blocked)
		}
		if !strings.Contains(answer.Reason, "keychain-approval") {
			t.Errorf("the reason in the answer = %q, want it to name keychain-approval, which is what an orchestrator reads first",
				answer.Reason)
		}
		// The attempt is in the state of the task and the reason is in the way out of it:
		// a list of runs reads the first, and a person reading the journal of the attempt
		// afterwards reads the second, when the window has closed and the owner has gone
		// back to what they were doing.
		journal, err := os.ReadFile(answer.ErrorJournal)
		if err != nil {
			t.Fatalf("read the way out of the attempt %s: %v", answer.ErrorJournal, err)
		}
		for _, want := range []string{"keychain-approval", secret.Service + "/" + secret.AppKey(5107052)} {
			if !strings.Contains(string(journal), want) {
				t.Errorf("the way out of the attempt holds %q, want it to hold %q", journal, want)
			}
		}
	})
}

// hostWithoutTheKey is a host of a project whose executor works as an App of the host
// whose keychain of macOS never opened its window: the run is refused before the executor
// is started, and the refusal is the one of a machine whose owner is not at it
// (docs/DESIGN.md §7i).
func hostWithoutTheKey(t *testing.T) (*host, string) {
	t.Helper()
	host := &host{task: taskOf(43), opened: true, noIdentity: fmt.Errorf(
		"the private key of the app 5107052: the keychain of macOS asked the owner to allow this program "+
			"to read crewflow/github-app-5107052, and nobody answered in 2m0s: %w", secret.ErrApproval)}
	host.use(t)
	return host, host.config(t)
}

// TestTheStoreOfTheRolesIsBehindTheWaitOfTheKeychain: every command that reads a secret
// of the machine reads it through a store that says what it is about to wait for and gives
// the wait an end. A command that reads a key without that is a command that can stand in
// front of a window of the system for ever with nothing said and nothing written down
// (docs/DESIGN.md §7i).
func TestTheStoreOfTheRolesIsBehindTheWaitOfTheKeychain(t *testing.T) {
	useMachineOfTheTest(t, &windowOfNobody{}, nil)
	t.Cleanup(func() { waitForTheKeychain = secret.Waiting })
	waitForTheKeychain = 10 * time.Millisecond
	var said bytes.Buffer

	env := roleEnv("crewflow.toml", secret.NewNotices(&said), nil)
	started := time.Now()
	_, err := env.Secrets.Get(secret.Service, secret.AppKey(5107052))
	waited := time.Since(started)

	if !errors.Is(err, secret.ErrApproval) {
		t.Fatalf("the read of the key of the app = %v, want %v: the call of a window nobody answered ends", err, secret.ErrApproval)
	}
	if waited > time.Second {
		t.Errorf("the read of the key of the app took %s, want the wait of the store and not longer", waited)
	}
	for _, want := range []string{"keychain of macOS", secret.Service + "/" + secret.AppKey(5107052), "10ms"} {
		if !strings.Contains(said.String(), want) {
			t.Errorf("what was said before the wait is %q, want it to name %q", said.String(), want)
		}
	}
}

// windowOfNobody is the keychain of macOS of a machine whose owner is not at it: it holds
// a secret of the app, it does not trust this build of the program, and every call that
// is not the question of the access itself stands in front of a window of the system that
// nobody opens (docs/DESIGN.md §7i).
type windowOfNobody struct{}

var nobodyAnswers = make(chan struct{})

func (windowOfNobody) Get(string, string) ([]byte, error) {
	<-nobodyAnswers
	return nil, nil
}

func (windowOfNobody) Set(string, string, []byte) error {
	<-nobodyAnswers
	return nil
}

func (windowOfNobody) Has(string, string) (bool, error) {
	<-nobodyAnswers
	return false, nil
}

func (windowOfNobody) Allowed(string) (bool, error) { return false, nil }
