// Package secret keeps what crewflow must not print and must not lose: the key of
// the GitHub App the executor of a project works as, and whatever else a role of a
// project will need a secret for (docs/DESIGN.md §7e, §7i).
//
// The store of the machine is behind one interface, and only the store touches the
// keychain: a test of crewflow hands a run a store of its own and never reaches the
// secrets of the person the tests run on. What a secret is worth is also this
// package's business: the values of a run that go into a journal are put through the
// redactor here, so that a token of an hour does not outlive it in a file a person
// pastes into an issue.
package secret

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"sync"
)

// Service is the name every secret of crewflow is kept under in the store of the
// machine: one name, so that a person who goes looking finds them all, and one name
// crewflow may delete later without hunting.
const Service = "crewflow"

// Store is where a secret is kept, behind one interface. The keychain of macOS is
// the store of that machine, a store of a test is a struct in the test, and there is
// no store anywhere else yet (docs/DESIGN.md §7i).
type Store interface {
	// Get returns the value kept under the service and the account, and
	// [ErrNotFound] when there is none: a key that was never imported is not the
	// same thing as a store that could not be read, and a report says so.
	Get(service, account string) ([]byte, error)
	// Set keeps the value under the service and the account, replacing what was
	// kept there before: importing a key of an App twice leaves the last one.
	Set(service, account string, value []byte) error
}

// Presence is what a store may answer without reading the value of the secret. It
// is a question of its own and not a part of Store, because a report may say that
// the key of an App is there without ever reading it (docs/DESIGN.md §7e).
type Presence interface {
	// Has reports whether a value is kept under the service and the account,
	// whatever the value is.
	Has(service, account string) (bool, error)
}

// Trust is what a store may answer about the access of this program to the secrets of
// a service without reading one of them and without asking anybody anything: the
// keychain of macOS can be asked exactly this question, and it answers it without
// opening the window that would ask the owner of the machine for permission. It is a
// question of its own and not a part of [Store], because a report asks it before it
// does anything else, and a report that stands in front of a window it could have
// asked a question about is a report that hangs (docs/DESIGN.md §7i).
type Trust interface {
	// Allowed reports whether this program may read a secret of the service without
	// the window of the system asking the owner first, and [ErrNotFound] when the
	// service holds nothing at all: there is nothing there to be allowed for, and an
	// empty store cannot have refused anybody.
	Allowed(service string) (bool, error)
}

// Remover is what a store may answer about a secret it keeps: take it away. It is a
// question of its own and not a part of [Store], because keeping a secret is what crewflow
// does with the key of an App and with the credentials of a proxy, while taking one away is
// something a person asks for by name — the item of a profile that is gone, and nothing
// else (docs/DESIGN.md §7e).
type Remover interface {
	// Delete takes the value kept under the service and the account away, and is not an
	// error where there is none: an item of a test has to be gone when the test is over,
	// and an item nobody ever imported is already where it should be.
	Delete(service, account string) error
}

// ErrNotFound is the answer of a store for a secret that is not in it. It is a
// sentinel and not a text, because the callers that must tell "the owner never
// imported the key" from "the keychain could not be read" are reports, and a report
// of a missing key and of a broken store leads a person to different places.
var ErrNotFound = errors.New("no such secret in the store")

// AppKey is the account the private key of the GitHub App with this number is kept
// under. It is named after the App and not after the project, so that a person who
// looks at the keychain of a machine sees which App a key belongs to.
func AppKey(appID int64) string {
	return "github-app-" + strconv.FormatInt(appID, 10)
}

// ProxyKey is the account the login and the password of the proxy profile with this
// name are kept under. The name of the profile is in it and not the address of the
// proxy: the address is different on every machine and changes when the owner moves, and
// the value in the keychain has to outlive the address in the file of a project
// (docs/DESIGN.md §7d, §7e).
func ProxyKey(name string) string {
	return "network-proxy/" + name
}

// Redacted is what the value of a secret is replaced with in everything a person
// reads: a journal, an error, a line of a report. It says that something was there
// and is not shown, which a report needs and a blank would not say.
const Redacted = "[redacted]"

// shortest is the length a value has to have to be worth looking for in a text. A
// token of GitHub and a key of an App are long, and a value shorter than this would
// be replaced wherever it appears — in every word of a journal — and a journal that
// says nothing of what happened is worse than one that shows a value of a secret
// nobody can use.
const shortest = 8

// Notices is where crewflow says that it is waiting for the owner of a machine, and
// who is there to read it: the terminal of the person who started the command and —
// as soon as there is one — the journal of the attempt. It is a value of its own and
// not a writer, because the store of the machine is made before a run has a journal to
// write to, and a run puts its journal here as soon as it has opened one
// (docs/DESIGN.md §7i).
//
// The zero Notices and a nil one say nothing: a run that was given nowhere to say
// what it waits for is a run of a machine of a test, and a program that cannot say
// where it would say it says nothing at all.
type Notices struct {
	// mu guards outs, which a run adds its journal to while the attempt is open and
	// takes away when it is over.
	mu   sync.Mutex
	outs []io.Writer
}

// NewNotices returns the notices that are said to the given writers, in the order they
// are given.
func NewNotices(outs ...io.Writer) *Notices {
	return &Notices{outs: outs}
}

// Listening puts one more writer where the notices are said, and answers the function
// that takes it away again: a run adds the journal of its attempt when it opens the
// journal and takes it away when the attempt is over, because what is said in the next
// attempt of the task belongs to the journal of that one (docs/DESIGN.md §7i).
func (n *Notices) Listening(out io.Writer) func() {
	nothing := func() {}
	if n == nil || out == nil {
		return nothing
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.outs = append(n.outs, out)
	return func() {
		n.mu.Lock()
		defer n.mu.Unlock()
		if last := len(n.outs) - 1; last >= 0 && n.outs[last] == out {
			n.outs = n.outs[:last]
		}
	}
}

// Say is what crewflow says about what it is waiting for, to everybody who is
// listening. A writer that fails is passed over: what a person is told is worth less
// than the run going on, and a run that lost a line of a notice has the reason of the
// wait in its report all the same (docs/DESIGN.md §7i).
func (n *Notices) Say(text string) {
	if n == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, out := range n.outs {
		if out == nil {
			continue
		}
		_, _ = fmt.Fprintln(out, text)
	}
}

// Redact is a text with the values of secrets taken out of it, for everything crewflow
// writes after a run: the value of a token is in the environment of the executor, and
// an agent that prints its own environment is a line of a journal that would carry a
// token of an hour into a file kept for ever.
func Redact(text string, values ...string) string {
	for _, value := range values {
		if len(value) < shortest {
			continue
		}
		text = string(bytes.ReplaceAll([]byte(text), []byte(value), []byte(Redacted)))
	}
	return text
}

// Redactor writes what a program wrote with the values of secrets taken out of it. A
// journal is written while a run goes on, and a value may be split between two writes
// of it, so the end of what was written is held back for as long as it is the
// beginning of a value that the next write may finish, and [Redactor.Flush] writes
// what is left when the run is over.
type Redactor struct {
	// out is where the text goes, and secrets are the values to take out of it.
	out     io.Writer
	secrets []string
	// held is what was written and not passed on yet: the end of a text that may
	// still grow into the beginning of a value.
	held []byte
}

// NewRedactor returns the writer that puts the text into out with the values taken
// out of it. Values too short to be a secret of a machine are left alone, and a text
// with no value in it is written as it is.
func NewRedactor(out io.Writer, values ...string) *Redactor {
	var secrets []string
	for _, value := range values {
		if len(value) >= shortest {
			secrets = append(secrets, value)
		}
	}
	return &Redactor{out: out, secrets: secrets}
}

// Write writes what a program wrote, with the values taken out, and holds back the
// end of it while it may still be the beginning of a value. It reports as many bytes
// as it was given: the program that writes to it is not told about the values in it.
func (r *Redactor) Write(p []byte) (int, error) {
	r.held = append(r.held, p...)
	cut := len(r.held) - r.pending()
	if cut == 0 {
		return len(p), nil
	}
	if _, err := r.out.Write(redact(r.held[:cut], r.secrets)); err != nil {
		return 0, err
	}
	r.held = slices.Clone(r.held[cut:])
	return len(p), nil
}

// pending is how much of the end of the text is the beginning of a value that the
// next write may finish: the longest tail of it that is the beginning of one of the
// values and not all of one, which is a whole value and is redacted as it is.
func (r *Redactor) pending() int {
	longest := 0
	for _, secret := range r.secrets {
		for size := longest + 1; size < len(secret); size++ {
			if bytes.HasSuffix(r.held, []byte(secret[:size])) {
				longest = size
			}
		}
	}
	return longest
}

// Flush writes what Write held back, and is called when nothing more is coming: the
// last line of a journal is written without a newline of its own more often than a
// person would think, and the end of it is where a token of a run is.
func (r *Redactor) Flush() error {
	if len(r.held) == 0 {
		return nil
	}
	_, err := r.out.Write(redact(r.held, r.secrets))
	r.held = nil
	return err
}

// redact is Redact over a byte slice, without a text and a back: a journal of a run
// is big, and making a string of every write of it is a copy of it for nothing.
func redact(text []byte, secrets []string) []byte {
	for _, secret := range secrets {
		text = bytes.ReplaceAll(text, []byte(secret), []byte(Redacted))
	}
	return text
}
