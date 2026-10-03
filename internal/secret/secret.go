// Package secret keeps what crewflow must not print and must not lose: the key of
// the GitHub App the executor of a project works as, and whatever else a role of a
// project will need a secret for (docs/DESIGN.md §7e, §7i).
//
// The store of the machine is behind one interface, and only the store touches the
// keychain: a test of crewflow hands a run a store of its own and never reaches the
// secrets of the person the tests run on. What a secret is worth is also this
// package's business: the values of a run that go into a journal are put through the
// boundary of this package — [Out] — on the way out of it, so that a token of an hour does
// not outlive the run in a file a person pastes into an issue.
package secret

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// A Value is one value a run must not write down, and the rule that says whether it is
// worth looking for in a text at all. There are two kinds of them, and the difference is who
// wrote the value down:
//
//   - a value a machine wrote — a token of an hour, a key of an App — is long by the way a
//     machine writes it, and a value shorter than [shortest] would be taken out of every word
//     of a journal: a journal that says nothing of what happened is worse than one that shows
//     a value nobody can use. Those are the values of [Generated];
//   - a value a person chose — the login and the password of a proxy of a project — is as long
//     as that person made it, and how long a secret is says nothing about whether it is one.
//     Those are the values of [Chosen], and the length of a value does not decide for them.
//
// Both kinds are taken out of a text the same way; the kind of a value says only whether a
// short one is looked for (docs/DESIGN.md §7e).
type Value struct {
	// text is the value as it is written down, looked for in a text whole.
	text string
	// always says that the value is worth looking for whatever its length is.
	always bool
}

// Generated is the list of the values a machine wrote for itself: the token of an hour and the
// key of an App. They are long by the way a machine writes them, and a value of another length
// than that is not looked for in a text at all (§7e).
func Generated(values ...string) []Value {
	return listOf(false, values)
}

// Chosen is the list of the values a person chose: the credentials of a proxy of a project.
// They are as long as that person made them — three signs is a password a proxy takes — and
// they are taken out of every text whatever their length is, because the run that skipped a
// password for being short would write it into a file kept for ever (§7e).
func Chosen(values ...string) []Value {
	return listOf(true, values)
}

// listOf is the values of one kind, without the empty ones: a value nobody wrote down is not a
// secret of anything, and an empty value in a list would be taken out of every character of a
// text and leave of it nothing at all.
func listOf(always bool, values []string) []Value {
	list := make([]Value, 0, len(values))
	for _, value := range values {
		if value != "" {
			list = append(list, Value{text: value, always: always})
		}
	}
	return list
}

// worthLookingFor is whether a value is looked for in a text at all: a value a person chose
// whatever its length is, and a value a machine wrote from the eighth sign on.
func worthLookingFor(value Value) bool {
	return value.always || len(value.text) >= shortest
}

// shortest is the length a value a machine wrote has to have to be worth looking for in a
// text. A token of GitHub and a key of an App are long, and a value shorter than this would
// be replaced wherever it appears — in every word of a journal — and a journal that says
// nothing of what happened is worse than one that shows a value of a secret nobody can use.
// It is the rule of those values alone: what a person chose is not measured by it, and a proxy
// that accepts a password of three signs is a proxy a person really uses (docs/DESIGN.md §7e).
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
//
// A value is looked for in every text a machine writes it in and not only in the text as
// it is written down: a proxy that says what it was asked for is quoted into an event of
// the route with `%q`, a report writes its strings with the escapes of JSON, and a value
// with a quote or a line break in it is in neither of those as the store keeps it
// (docs/DESIGN.md §7e).
func Redact(text string, values ...Value) string {
	return string(redact([]byte(text), spellings(values)))
}

// spellings are every text a set of values is looked for by in everything crewflow
// publishes: the value as it is written down and the two forms the machines of crewflow
// escape it into — what `%q` writes of it and what JSON writes of it, both without the
// quotes that go around them, because a value is looked for in a line and not in the
// quotes of it. A boundary that knew the value alone did not find it where a machine had
// escaped it, and a password of a person with a quote in it stood in an event whole
// (R5-NEW-3, docs/DESIGN.md §7e).
//
// A value too short to be the secret of its own kind is left out of it: it is not looked
// for in a text at all, and a value that is not looked for must not be looked for twice
// (§7e). A form that is the value itself is one form and not three: the same text looked
// for twice cuts a text twice for nothing.
func spellings(values []Value) []string {
	spellings := make([]string, 0, len(values))
	known := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !worthLookingFor(value) {
			continue
		}
		for _, form := range []string{value.text, quoted(value.text), encoded(value.text)} {
			if _, seen := known[form]; seen {
				continue
			}
			known[form] = struct{}{}
			spellings = append(spellings, form)
		}
	}
	return spellings
}

// quoted is the value as `%q` writes it inside a string: the escapes of Go, which are the
// ones a person reads in the words of an error and in the line of an event.
func quoted(text string) string {
	return inside(strconv.Quote(text))
}

// encoded is the value as JSON writes it inside a string of a document. It is not always
// what `%q` writes of it: `encoding/json` escapes `<`, `>` and `&` into six signs each, and a
// report of a command is a document of strings.
func encoded(text string) string {
	document, err := json.Marshal(text)
	if err != nil {
		// A string is never refused by the encoder, and a form that is not there is the
		// value itself: nothing is lost by looking for the one form more than the others.
		return text
	}
	return inside(string(document))
}

// inside is the text between the quotes of an encoded string.
func inside(encoded string) string {
	if len(encoded) < 2 {
		return encoded
	}
	return encoded[1 : len(encoded)-1]
}

// redact is Redact over a byte slice, without a text and a back: a journal of a run
// is big, and making a string of every write of it is a copy of it for nothing. What
// it is given is the spellings of the values and not the values themselves — see
// [Out.Writer], which is the one place crewflow cleans what a program wrote.
func redact(text []byte, secrets []string) []byte {
	for _, secret := range secrets {
		text = bytes.ReplaceAll(text, []byte(secret), []byte(Redacted))
	}
	return text
}
