package run

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/secret"
)

// TestARunThatWaitedForTheKeychainIsBlockedAndIsWrittenDown is the whole of the failure
// this is here for: the keychain of macOS asks the owner in a window of the system before
// it lets a program read a secret of it, and a run that stood in front of that window in
// silence for an hour and a half looked like a machine that hangs — four runs of the
// mode of the bot and a `crewflow doctor` that printed nothing at all (docs/DESIGN.md §7i).
//
// Such a run ends as `blocked: keychain-approval`, it says what it waited for where the
// run of the task is written, and the attempt is in the state of the task: an
// orchestrator reads a list of runs, and a list that hides a task waiting for a person
// is a list that hides the reason the task has not started.
func TestARunThatWaitedForTheKeychainIsBlockedAndIsWrittenDown(t *testing.T) {
	m := newMachine(t)
	// The terminal of the person who started the run, and the store of the machine
	// behind the wait: a keychain of macOS with a secret in it that does not trust this
	// build of the program, which opens a window of the system and waits.
	var terminal bytes.Buffer
	notices := secret.NewNotices(&terminal)
	env := m.env()
	env.Notices = notices
	env.Secrets = secret.Waited(unanswered{}, notices, 10*time.Millisecond)
	host := &host{task: taskOf(43), identity: theBot(), store: env.Secrets}
	cfg := inTheModeOfTheBot(projectOf(t, m.worktrees, ""))

	result, err := Run(t.Context(), env, cfg, host.set(), Request{Number: 43, RepoDir: m.repo})

	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}
	if result.Outcome != Blocked {
		t.Errorf("the outcome = %q, want %q: a run that waited for the keychain and was not answered is blocked",
			result.Outcome, Blocked)
	}
	if !strings.HasPrefix(result.Reason, reasonApproval) {
		t.Errorf("the reason = %q, want it to begin with %q, which is what an orchestrator reads first",
			result.Reason, reasonApproval)
	}
	for _, want := range []string{secret.Service + "/" + secret.AppKey(5107052), "keychain of macOS"} {
		if !strings.Contains(result.Reason, want) {
			t.Errorf("the reason = %q, want it to name %q", result.Reason, want)
		}
	}
	// The line comes before the wait, and it comes twice: once to the terminal of the
	// person who started the run and once to the journal of the attempt. A run that
	// says nothing while it waits is a run that looks like a machine that hangs, and a
	// journal nobody is watching is not enough for a person who is not there.
	for where, said := range map[string]string{
		"the terminal": terminal.String(),
		"the journal":  read(t, result.Journal),
	} {
		for _, want := range []string{"keychain of macOS", secret.Service + "/" + secret.AppKey(5107052), "10ms"} {
			if !strings.Contains(said, want) {
				t.Errorf("%s holds %q, want it to hold the line about the wait with %q in it", where, said, want)
			}
		}
	}
	// The way out of the run holds the reason as well: a person reading the journal of
	// the attempt is reading it after the window has closed and the owner has gone.
	if out := read(t, result.ErrorJournal); !strings.Contains(out, reasonApproval) &&
		!strings.Contains(out, "the keychain of macOS") {
		t.Errorf("the way out of the run holds %q, want it to hold why the run stopped", out)
	}
	// The executor was never started: there is no run of an agent here, and a machine
	// that started one would go on to sign a token with a key nobody allowed it to read.
	if m.askedFor("opencode") {
		t.Errorf("the executor was started %v, want nothing started", m.lines())
	}
	// The attempt is in the state of the task, and it ended the way the run ended: it is
	// the one attempt of §7 that is written down without an executor having run, and
	// `-continue` goes on in the worktree of the task through it.
	state := stateOf(t, m, 43)
	last := state.Attempts[len(state.Attempts)-1]
	if last.Outcome != Blocked {
		t.Errorf("the state holds the outcome %q of the attempt, want %q", last.Outcome, Blocked)
	}
	if last.EndedAt.IsZero() {
		t.Error("the attempt in the state of the task has no end, want the moment the run stopped waiting")
	}
	if last.Journal != result.Journal {
		t.Errorf("the state holds the journal %q, want %q", last.Journal, result.Journal)
	}
	if last.Identity.Mode != forge.ModeBot {
		t.Errorf("the state holds the mode %q, want %q: a project in the mode of the bot that was refused "+
			"its key is a run of the mode of the bot", last.Identity.Mode, forge.ModeBot)
	}
}

// TestARunInAMachineThatAllowsThisBuildGoesOnAsItAlwaysWent: a keychain of macOS that
// lets this build of the program in opens no window, and a run of a machine that is set up
// right is a run as it always was — the same attempt, the same executor, the same
// outcome, and not one line about a window that will not open (docs/DESIGN.md §7i).
func TestARunInAMachineThatAllowsThisBuildGoesOnAsItAlwaysWent(t *testing.T) {
	m := newMachine(t)
	m.answers["opencode"] = answer{stdout: theRun}
	m.answers["git config"] = answer{}
	var terminal bytes.Buffer
	notices := secret.NewNotices(&terminal)
	env := m.env()
	env.Notices = notices
	env.Secrets = secret.Waited(&answering{key: keyOfTheRun(t)}, notices, time.Minute)
	host := &host{task: taskOf(43), opened: true, store: env.Secrets}
	cfg := inTheModeOfTheBot(projectOf(t, m.worktrees, ""))

	result, err := Run(t.Context(), env, cfg, host.set(), Request{Number: 43, RepoDir: m.repo})

	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}
	if result.Outcome != ChangeRequestOpened {
		t.Errorf("the outcome = %q, want %q: a run of a machine that is set up right goes on as it always went",
			result.Outcome, ChangeRequestOpened)
	}
	if terminal.Len() != 0 {
		t.Errorf("the terminal holds %q, want nothing said about a window that will not open", terminal.String())
	}
	if said := read(t, result.Journal); strings.Contains(said, "keychain of macOS") {
		t.Errorf("the journal of the attempt holds %q, want no line about the wait", said)
	}
	if !m.askedFor("opencode") {
		t.Errorf("the machine was asked %v, want the executor of the run started", m.lines())
	}
}

// TestARunThatWasRefusedSomethingElseLeavesNothingBehind: a run that could not be given
// an account of the host for any other reason — no key imported, no store to read — is
// not a run, and the journals of the task hold nothing about it: an attempt of a task in
// a state that never went is a lie a person reads (docs/DESIGN.md §7i).
func TestARunThatWasRefusedSomethingElseLeavesNothingBehind(t *testing.T) {
	m := newMachine(t)
	env := m.env()
	env.Notices = secret.NewNotices(nil)
	env.Secrets = secret.Waited(unanswered{}, env.Notices, 10*time.Millisecond)
	host := &host{task: taskOf(43), identity: theBot(), noIdentity: errors.New("the private key of the app is not in the store")}
	cfg := inTheModeOfTheBot(projectOf(t, m.worktrees, ""))

	_, err := Run(t.Context(), env, cfg, host.set(), Request{Number: 43, RepoDir: m.repo})

	if err == nil {
		t.Fatal("Run returned no error, want the refusal of the host")
	}
	journals := newJournals(m.home, "naghuale-crewflow")
	if _, statErr := os.Stat(journals.JournalPath(43, 1)); !os.IsNotExist(statErr) {
		t.Errorf("the journal of an attempt that never was is there, want nothing left behind (%v)", statErr)
	}
}

// inTheModeOfTheBot is the settings of a project whose executor works as an App of the
// host: the mode is in the file of the project, and it is what the state of the task
// holds for a run of it whatever came of it (docs/DESIGN.md §7i).
func inTheModeOfTheBot(cfg config.Config) config.Config {
	cfg.Identity = config.Identity{Mode: forge.ModeBot, GitHubApp: config.GitHubApp{AppID: 5107052}}
	return cfg
}

// unanswered is the keychain of macOS of a run whose owner is not at the machine: it
// holds a secret of the app and does not trust this build of the program, and every
// call that is not the question of the access itself waits for a window nobody opens.
type unanswered struct{}

// noWindow is a window of the system that nobody answers, and a call of a store that
// waits on it waits for ever.
var noWindow = make(chan struct{})

func (unanswered) Get(string, string) ([]byte, error) {
	<-noWindow
	return nil, nil
}

func (unanswered) Set(string, string, []byte) error {
	<-noWindow
	return nil
}

func (unanswered) Has(string, string) (bool, error) {
	<-noWindow
	return false, nil
}

func (unanswered) Allowed(string) (bool, error) { return false, nil }

// answering is the keychain of macOS of a machine that is set up right: it holds the key
// of the app and it lets this build of the program read it, so a call of it answers at
// once and no window of the system opens (docs/DESIGN.md §7i).
type answering struct {
	key []byte
}

func (a *answering) Get(string, string) ([]byte, error) { return a.key, nil }

func (*answering) Set(string, string, []byte) error { return nil }

func (a *answering) Has(string, string) (bool, error) { return a.key != nil, nil }

func (a *answering) Allowed(string) (bool, error) { return true, nil }

// keyOfTheRun is a private key of a test, in the PEM a person downloads from GitHub: no
// run of a test signs a token with the key of a real app, and the host of a test only
// asks the store whether the key is there.
func keyOfTheRun(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate a key of the test: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal the key of the test: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}
