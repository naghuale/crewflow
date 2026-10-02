package secret

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// Waiting is how long crewflow waits for the owner of a machine to answer the window the
// keychain of macOS opens for it, when nobody names a wait of their own: long enough
// that an owner who is at the machine can come and answer it, and short enough that a
// run is not left standing in front of a window of the system till the morning
// (docs/DESIGN.md §7i).
const Waiting = 2 * time.Minute

// ErrApproval is the answer of a store that waited for the owner of the machine to allow
// this program to read a secret of it and was not answered in time. It is a sentinel and
// not a text, because a run that was refused it is an outcome of its own that an
// orchestrator reads before anything else, and the callers that have to tell "nobody
// answered the window" from "the keychain said no" are reports and states of tasks
// (docs/DESIGN.md §7i).
var ErrApproval = errors.New("the access was not allowed in time")

// The line a person, a journal and a watch read while the keychain of macOS is waiting
// for the owner of the machine. It names what is being read and what happens when nobody
// answers, because a person who is being asked in a window of the system has to know
// that a program of a terminal is the one who is waiting (docs/DESIGN.md §7i).
const waiting = "crewflow: the keychain of macOS will ask you in a window of the system to allow " +
	"this program to read %s/%s; answer there, and nobody answering in %s ends the wait"

// Waited is the store of the machine behind a store that says what it is about to ask
// before it asks and gives the answer time to come.
//
// The keychain of macOS asks the owner in a window of the system before it lets a program
// it does not trust read a secret, and the call says nothing at all while it waits: a
// run, a check and the person who started them all stand in front of a window in
// silence, and a silence of an hour looks like a machine that hangs. So the store behind
// this one asks first whether this program is allowed to read without the window — the
// one question the keychain answers without asking anybody ([Trust]) — says what it is
// about to wait for when the answer is no, and gives the wait an end: every call goes on
// in a goroutine of its own, and a wait that is out is [ErrApproval] whatever the owner
// does with the window afterwards (docs/DESIGN.md §7i).
//
// A wait of zero or less is [Waiting], and the store of a test that answers at once is
// never announced and never waited for.
func Waited(store Store, out *Notices, after time.Duration) Store {
	if after <= 0 {
		after = Waiting
	}
	return &waited{store: store, out: out, after: after, said: &sync.Once{}}
}

// waited is the store of a machine behind the wait: the store itself, the place its
// notices are said, how long a call of it may take, and the one time the notice was
// said.
type waited struct {
	store Store
	out   *Notices
	after time.Duration
	// said is said once and not once per call: a run reads the key of the app more than
	// once, and a person who is being asked about one window of the system does not have
	// to be told about it three times.
	said *sync.Once
}

// Get is the value kept under the service and the account, or [ErrApproval] when the
// store of the machine was not answered in time. The value of a secret that nobody
// allowed this program to read is never returned, not empty and not in part: there is
// nothing in the hands of the run but the refusal (docs/DESIGN.md §7i).
func (w *waited) Get(service, account string) ([]byte, error) {
	w.announce(service, account)
	return inTime(w.after, w.tired(service, account), func() ([]byte, error) {
		return w.store.Get(service, account)
	})
}

// Set keeps the value under the service and the account, and a keychain of macOS that is
// waiting for the owner of the machine is a window in front of a write as much as in
// front of a read: an import of a key of an app is a program of a terminal the owner
// started, and the window is where the owner answers it (docs/DESIGN.md §7i).
func (w *waited) Set(service, account string, value []byte) error {
	w.announce(service, account)
	_, err := inTime(w.after, w.tired(service, account), func() (bool, error) {
		return true, w.store.Set(service, account, value)
	})
	return err
}

// Has is whether a value is kept under the service and the account, and the call is a
// call of the store of the machine like any other: the keychain asks the owner for the
// status of an item as well, and a report that has to wait for the owner to be told that
// a secret is there is a report that hangs (docs/DESIGN.md §7i).
//
// A store that cannot answer the question without reading the value is asked with a
// read, which is what a caller of [Presence] would have done on its own: the question
// itself is not lost, only the way of answering it (docs/DESIGN.md §7e).
func (w *waited) Has(service, account string) (bool, error) {
	w.announce(service, account)
	if presence, ok := w.store.(Presence); ok {
		return inTime(w.after, w.tired(service, account), func() (bool, error) {
			return presence.Has(service, account)
		})
	}
	return inTime(w.after, w.tired(service, account), func() (bool, error) {
		_, err := w.store.Get(service, account)
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, ErrNotFound):
			return false, nil
		default:
			return false, err
		}
	})
}

// Allowed is the question of the store of the machine itself, asked the same way it
// would be asked of the store behind this one: it waits for nobody and asks nobody
// anything, which is the whole of why it is a question of its own. A store that cannot
// answer it keeps no secret of macOS, and nothing about it needs the answer of a person
// (docs/DESIGN.md §7i).
func (w *waited) Allowed(service string) (bool, error) {
	if trust, ok := w.store.(Trust); ok {
		return trust.Allowed(service)
	}
	return true, nil
}

// Delete takes a secret away through the wait of the keychain of macOS, and says nothing
// before it: taking a secret away is not a window of the system and is not a read, and the
// person who asked for it by name is not waiting for anything (docs/DESIGN.md §7e).
func (w *waited) Delete(service, account string) error {
	remover, ok := w.store.(Remover)
	if !ok {
		return fmt.Errorf("%s/%s: the store of this machine cannot take a secret away", service, account)
	}
	_, err := inTime(w.after, nil, func() (struct{}, error) {
		return struct{}{}, remover.Delete(service, account)
	})
	return err
}

// announce is what crewflow says before it asks the store of the machine for a secret,
// and it is said only where the store itself says a window of the system is coming: a
// program the keychain trusts is let in at once, and a line about a window that will not
// open is a line of noise on every run of a machine that is set up right
// (docs/DESIGN.md §7i).
func (w *waited) announce(service, account string) {
	trust, ok := w.store.(Trust)
	if !ok {
		return
	}
	allowed, err := trust.Allowed(service)
	if err != nil || allowed {
		return
	}
	w.said.Do(func() {
		w.out.Say(fmt.Sprintf(waiting, service, account, w.after))
	})
}

// tired is what a store that was not answered in time says, in the words of the store
// and of the machine: the name of the secret, the window nobody answered and how long
// it was open for.
func (w *waited) tired(service, account string) error {
	return fmt.Errorf("the keychain of macOS asked the owner to allow this program to read %s/%s, "+
		"and nobody answered in %s: %w", service, account, w.after, ErrApproval)
}

// inTime is one call of the store of the machine with an end to the waiting. The call
// goes on in a goroutine of its own, because a keychain that is waiting for the owner of
// the machine cannot be stopped — the framework is in the window and not in the program —
// and what crewflow can do is stop waiting for it. The answer is left in a channel that
// holds one, so that a goroutine which is answered after the wait is over puts the value
// down and goes instead of holding a key of an app for the rest of the process
// (docs/DESIGN.md §7i).
func inTime[T any](after time.Duration, tired error, call func() (T, error)) (T, error) {
	var nothing T
	type answer struct {
		value T
		err   error
	}
	done := make(chan answer, 1)
	go func() {
		value, err := call()
		done <- answer{value: value, err: err}
	}()
	timer := time.NewTimer(after)
	defer timer.Stop()
	select {
	case got := <-done:
		return got.value, got.err
	case <-timer.C:
		return nothing, tired
	}
}

// The stores behind the wait are stores, and the machine of macOS is the one that can
// say whether this program may read a secret of it without a window: a store of a test
// behind the wait keeps whatever its own store could answer.
var (
	_ Store    = &waited{}
	_ Presence = &waited{}
	_ Trust    = &waited{}
)
