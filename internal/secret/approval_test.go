package secret

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestTheWaitForTheKeychainIsSaidBeforeItBeginsAndHasAnEnd is the whole of the failure
// this package exists for: a program asks the keychain of macOS for a secret the keychain
// does not trust it with, the keychain opens a window of the system and stands in front
// of it, and the program says nothing at all for as long as it lasts (docs/DESIGN.md §7i).
//
// The line comes before the call — the call itself is the silence — and it names what is
// being waited for. The wait has an end: a run that stood in front of a window for an
// hour and a half looked like a machine that hangs, and the answer of a store whose wait
// is over is [ErrApproval] whatever the owner does with the window afterwards.
func TestTheWaitForTheKeychainIsSaidBeforeItBeginsAndHasAnEnd(t *testing.T) {
	var out bytes.Buffer
	store := Waited(unanswered{}, NewNotices(&out), 10*time.Millisecond)

	value, err := store.Get(Service, AppKey(5107052))

	if value != nil {
		t.Errorf("Get = %q, want no secret of a store that never answered", value)
	}
	if !errors.Is(err, ErrApproval) {
		t.Fatalf("Get = %v, want %v", err, ErrApproval)
	}
	for _, want := range []string{Service + "/" + AppKey(5107052), "keychain of macOS", "10ms"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the answer of the store is %q, want it to name %q", err, want)
		}
	}
	for _, want := range []string{"keychain of macOS", Service + "/" + AppKey(5107052), "10ms"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("what was said before the wait is %q, want it to name %q", out.String(), want)
		}
	}
}

// TestTheWaitForTheKeychainIsOverForEveryCallAndNotOnlyForReading: a report of a
// machine asks whether a secret is there without reading it, and a keychain that has
// not been trusted opens its window for that question as well. Every call of the store
// of the machine has an end, or the report of a machine stands in front of a window
// just as long (docs/DESIGN.md §7i).
func TestTheWaitForTheKeychainIsOverForEveryCallAndNotOnlyForReading(t *testing.T) {
	var out bytes.Buffer
	store := Waited(unanswered{}, NewNotices(&out), 10*time.Millisecond)
	presence, ok := store.(Presence)
	if !ok {
		t.Fatal("the store cannot say whether a secret is there, want the store of the machine behind the wait to be asked without reading it")
	}

	there, err := presence.Has(Service, AppKey(5107052))

	if there {
		t.Error("Has = true for a store that never answered, want false")
	}
	if !errors.Is(err, ErrApproval) {
		t.Errorf("Has = %v, want %v", err, ErrApproval)
	}
}

// TestAStoreThatTrustsThisProgramIsNeverAnnounced: a machine whose keychain knows this
// build of the program opens no window, and a line about a window that will not open is
// noise on every run of a machine that is set up right.
func TestAStoreThatTrustsThisProgramIsNeverAnnounced(t *testing.T) {
	var out bytes.Buffer
	machine := &answering{value: []byte("the key of the app"), trusted: true}
	store := Waited(machine, NewNotices(&out), time.Minute)

	value, err := store.Get(Service, AppKey(5107052))

	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(value) != "the key of the app" {
		t.Errorf("Get = %q, want the value the store holds", value)
	}
	if out.Len() != 0 {
		t.Errorf("what was said is %q, want nothing said to a program the store trusts", out.String())
	}
}

// TestAStoreThatDoesNotTrustThisProgramIsAskedOnceAndAnswersAtOnce: a run reads the key
// of the app more than once, and a person who is being asked about one window does not
// have to be told about it three times. The store behind the wait answers in the time it
// has, whoever answers the window.
func TestAStoreThatDoesNotTrustThisProgramIsAskedOnceAndAnswersAtOnce(t *testing.T) {
	var out bytes.Buffer
	machine := &answering{value: []byte("the key of the app"), trusted: false}
	store := Waited(machine, NewNotices(&out), time.Minute)

	for range 2 {
		if _, err := store.Get(Service, AppKey(5107052)); err != nil {
			t.Fatalf("Get: %v", err)
		}
	}

	if said := strings.Count(out.String(), "keychain of macOS"); said != 1 {
		t.Errorf("what was said names the keychain %d times, want it said once: %q", said, out.String())
	}
}

// TestAStoreWithNothingInItSaysNothing: there is no secret to be allowed for, and a
// store that is empty answers the question of the access of the program with "there is
// nothing" — which is a refusal to ask, not a wait.
func TestAStoreWithNothingInItSaysNothing(t *testing.T) {
	var out bytes.Buffer
	store := Waited(&answering{}, NewNotices(&out), time.Minute)

	_, err := store.Get(Service, AppKey(5107052))

	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get = %v, want %v", err, ErrNotFound)
	}
	if out.Len() != 0 {
		t.Errorf("what was said is %q, want nothing said about a secret that is not there", out.String())
	}
}

// TestTheWaitOfAStoreIsTheTwoMinutesOfAMachineAndNotMore: a caller that names no wait of
// its own gets the wait of a machine — long enough for an owner to come and answer the
// window, and short enough that a run is not left standing in front of it till the
// morning. The wait is read off the store and not waited out: a test of two minutes is a
// test nobody runs (docs/DESIGN.md §7i).
func TestTheWaitOfAStoreIsTheTwoMinutesOfAMachineAndNotMore(t *testing.T) {
	store, ok := Waited(unanswered{}, NewNotices(io.Discard), 0).(*waited)
	if !ok {
		t.Fatal("the store behind the wait is not the store crewflow made, want the wait to be readable")
	}
	if store.after != Waiting {
		t.Errorf("the wait of the store is %s, want the %s of a machine", store.after, Waiting)
	}
	if Waiting != 2*time.Minute {
		t.Errorf("the wait of a machine is %s, want two minutes", Waiting)
	}
}

// TestNoticesAreSaidToTheJournalOfTheAttemptOnlyWhileItIsOpen: the store of the machine
// is made before a run has a journal to write to, so a run puts its journal where the
// notices are said when it opens one and takes it away when the attempt is over. A
// journal of one attempt must not receive what is said in another (docs/DESIGN.md §7i).
func TestNoticesAreSaidToTheJournalOfTheAttemptOnlyWhileItIsOpen(t *testing.T) {
	var terminal, journal bytes.Buffer
	notices := NewNotices(&terminal)

	closed := notices.Listening(&journal)
	notices.Say("while the attempt is open")
	closed()
	notices.Say("after the attempt is over")

	if want := "while the attempt is open\n"; journal.String() != want {
		t.Errorf("the journal of the attempt holds %q, want %q", journal.String(), want)
	}
	if said := strings.Count(terminal.String(), "\n"); said != 2 {
		t.Errorf("the terminal holds %d lines, want both of them: %q", said, terminal.String())
	}
}

// TestNoticesOfNobodyAreSaidToNobody: a run of a machine of a test may be given nowhere
// to say what it waits for, and a store that has no notices says nothing rather than
// falling over on a place to write to.
func TestNoticesOfNobodyAreSaidToNobody(t *testing.T) {
	store := Waited(&answering{value: []byte("the key of the app")}, nil, time.Minute)

	value, err := store.Get(Service, AppKey(5107052))

	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(value) != "the key of the app" {
		t.Errorf("Get = %q, want the value the store holds", value)
	}
	if closed := (*Notices)(nil).Listening(io.Discard); closed == nil {
		t.Error("the notices of nobody have no way to be given a journal, want a function that does nothing")
	}
}

// unanswered is a store that never answers: the keychain of macOS in front of a window
// of the system that its owner has not answered, which is the machine of every run that
// stood still for an hour and a half with nothing in its journal.
type unanswered struct{}

// never is closed by nobody, and a call of a store that waits on it waits for ever.
var never = make(chan struct{})

func (unanswered) Get(string, string) ([]byte, error) {
	<-never
	return nil, nil
}

func (unanswered) Set(string, string, []byte) error {
	<-never
	return nil
}

func (unanswered) Has(string, string) (bool, error) {
	<-never
	return false, nil
}

// Allowed answers at once and says no: the keychain of macOS answers this question
// without a window, and the answer of a store that is in front of one is that this
// program may not read a secret of it until the owner says so.
func (unanswered) Allowed(string) (bool, error) { return false, nil }

// answering is a store that answers at once and can be asked the question of the access
// of the program: a keychain of macOS with a secret in it, which is what a test of this
// package can be about without the keychain of the person who runs the tests.
type answering struct {
	// value is what the store holds under every name, and trusted says whether this
	// program may read it without the owner answering a window first. A store with no
	// value holds nothing at all, which is a store that cannot refuse anybody.
	value   []byte
	trusted bool
	// read says whether the value itself was read, which is the question a report that
	// asks about a secret without reading it has to be able to answer.
	mu   sync.Mutex
	read bool
}

func (s *answering) Get(string, string) ([]byte, error) {
	s.mu.Lock()
	s.read = true
	s.mu.Unlock()
	if s.value == nil {
		return nil, ErrNotFound
	}
	return s.value, nil
}

func (s *answering) Set(_ string, _ string, value []byte) error {
	s.value = value
	return nil
}

func (s *answering) Has(string, string) (bool, error) {
	return s.value != nil, nil
}

func (s *answering) Allowed(string) (bool, error) {
	if s.value == nil {
		return false, ErrNotFound
	}
	return s.trusted, nil
}

// readIt says whether the value of a secret was read, and takes the answer with it: a
// test of a report asks it once.
func (s *answering) readIt() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	read := s.read
	s.read = false
	return read
}

// TestTheWrapperAnswersThePresenceWithoutReadingTheSecret: a report that says the key of
// an app is in the store of the machine says it without reading the key to say it
// (docs/DESIGN.md §7e). The store behind the wait has to be able to answer that question
// itself, or every report falls back to reading the key and opening the window of macOS
// on the way.
func TestTheWrapperAnswersThePresenceWithoutReadingTheSecret(t *testing.T) {
	machine := &answering{value: []byte("the key of the app"), trusted: true}
	store := Waited(machine, NewNotices(io.Discard), time.Minute)
	presence, ok := store.(Presence)
	if !ok {
		t.Fatal("the store behind the wait cannot say whether a secret is there")
	}

	there, err := presence.Has(Service, AppKey(5107052))

	if err != nil {
		t.Fatalf("Has: %v", err)
	}
	if !there {
		t.Error("Has = false for a store that holds the key, want true")
	}
	if machine.readIt() {
		t.Error("Has read the secret, want a report that says a secret is there without reading it")
	}
}
