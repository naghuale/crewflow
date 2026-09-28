//go:build darwin

package secret

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// securityProgram is the program of the keychain of macOS, and the only way into it
// crewflow has: the framework itself is not called from Go, and a program of the
// system is a program of the person who installed macOS.
//
// It is a variable so that a test of this package starts a program of its own: the
// keychain of the machine the tests run on holds the secrets of the person, and a
// test that wrote into it would leave a key of a test run in it for ever
// (docs/DESIGN.md §7i).
var securityProgram = "/usr/bin/security"

// notFound is the code security exits with for an item that is not in the keychain:
// errSecItemNotFound of the framework, which crewflow has to tell apart from a
// keychain that could not be read.
const notFound = 44

// keychain is the store of macOS: one generic password per secret, all of them
// under the service crewflow, each under the account that says what it is of.
type keychain struct {
	// program starts the keychain, and run starts it with the bytes to write to
	// its standard input. Both are fields so that a test of this package reaches a
	// program of the test and never the keychain of a person.
	program string
	run     func(name string, args []string, stdin []byte) (stdout, stderr []byte, exitCode int)
}

// System returns the store of this machine: the keychain, which is the only store
// crewflow has a program for. Everything else is refused where it is asked, not here
// (docs/DESIGN.md §7i).
func System() Store {
	return keychain{program: securityProgram, run: start}
}

// Get returns what is kept under the service and the account, and [ErrNotFound] when
// there is nothing there. The value comes on the standard output of security, which
// with -w prints the password and nothing else, and is read here and nowhere else:
// the key of an App is read in the moment a token is signed with it, and at no other
// time (docs/DESIGN.md §7i).
func (k keychain) Get(service, account string) ([]byte, error) {
	stdout, stderr, code := k.run(k.program,
		[]string{"find-generic-password", "-s", service, "-a", account, "-w"}, nil)
	switch {
	case missing(code, stderr):
		return nil, fmt.Errorf("%s/%s: %w", service, account, ErrNotFound)
	case code != 0:
		return nil, fmt.Errorf("security find-generic-password: exited with %d: %s",
			code, firstLine(stderr))
	}
	return []byte(strings.TrimRight(string(stdout), "\r\n")), nil
}

// Set keeps the value under the service and the account, replacing what was there.
// The value is written to the standard input of security and is not an argument of
// it, because the arguments of a program are what `ps` shows to every process of the
// machine, and a key of an App is not a thing to be seen on a screen by accident. -U
// is what makes the item a replacement and not a second one under the same name, and
// -w without a value after it is what makes security ask for the value instead of
// taking it from its arguments.
//
// What security reads is checked by whoever wrote the value: an import reads the key
// back and holds it as the key of an App, and a run that cannot hold it says so
// rather than signing a token with half a key.
func (k keychain) Set(service, account string, value []byte) error {
	stdin := append(append([]byte{}, value...), '\n')
	_, stderr, code := k.run(k.program,
		[]string{"add-generic-password", "-U", "-s", service, "-a", account, "-w"}, stdin)
	if code != 0 {
		return fmt.Errorf("security add-generic-password: exited with %d: %s",
			code, firstLine(stderr))
	}
	return nil
}

// Has reports whether a value is kept under the service and the account, without
// reading it: security is asked for the attributes of the item alone, and a report
// that says the key of an App is there says all a person needs before a run.
func (k keychain) Has(service, account string) (bool, error) {
	_, stderr, code := k.run(k.program,
		[]string{"find-generic-password", "-s", service, "-a", account}, nil)
	switch {
	case missing(code, stderr):
		return false, nil
	case code != 0:
		return false, fmt.Errorf("security find-generic-password: exited with %d: %s",
			code, firstLine(stderr))
	}
	return true, nil
}

// The keychain of macOS answers whether a value is there without reading it, which is
// what a report about a key of an App asks for.
var _ Presence = keychain{}

// missing is whether security said that there is no such item, as opposed to having
// failed: both are codes that are not zero, and only the first is an empty store.
func missing(code int, stderr []byte) bool {
	return code == notFound || bytes.Contains(stderr, []byte("could not be found"))
}

// start runs a program of the machine with what it is to read on its standard input
// and returns what it wrote and the code it exited with. The stdin of the program is
// a pipe and not the terminal of the person: `security` asks for the value of a new
// item on a terminal of its own where it can, and crewflow has read the key from the
// file it was pointed at.
func start(name string, args []string, stdin []byte) (stdout, stderr []byte, exitCode int) {
	cmd := exec.Command(name, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		// A program that ran and said no is a code of it, and a report shows it.
		return out.Bytes(), errOut.Bytes(), exit.ExitCode()
	default:
		// A machine without the program at all: there is nothing to ask it, and the
		// reason is what it said.
		return out.Bytes(), []byte(err.Error()), 127
	}
	return out.Bytes(), errOut.Bytes(), 0
}

// firstLine is the first line of what a program said, which is what an error holds:
// the whole of it may be long, and an error is read by people.
func firstLine(output []byte) string {
	line, _, _ := strings.Cut(string(output), "\n")
	return strings.TrimSpace(line)
}
