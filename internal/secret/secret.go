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
	"io"
	"slices"
	"strconv"
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
