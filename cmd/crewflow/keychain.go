package main

import "github.com/naghuale/crewflow/internal/secret"

// The keychain of macOS keeps the secrets of a project, and it asks the owner of the
// machine in a window of the system before it lets a program it does not trust read one
// of them: the window is opened by the framework, the call says nothing while it waits,
// and a run, a check or a person is left standing in front of it without knowing why
// (docs/DESIGN.md §7i).
//
// Every command of crewflow that needs a secret of the machine therefore reads it through
// a store that says what it is about to wait for before it waits, and that stops waiting
// when nobody answers. It is one function for all of them, because a command that reads a
// key without the wait is a command that can hang with nothing in its journal.
//
// The wait is a variable and not a constant in the call because a test of a command must
// not stand in front of a window for the two minutes a person would: a test of the wait
// of the keychain is a test of package secret, and a test of a command is a test of what
// the command does with the answer (docs/DESIGN.md §7i).
var waitForTheKeychain = secret.Waiting

// storeOfSecrets is the store of the machine behind the wait, and it says what it is
// about to wait for to the notices of the command: the terminal of the person who
// started it, and — for a run — the journal of the attempt as well.
func storeOfSecrets(notices *secret.Notices) secret.Store {
	return secret.Waited(secretsOfMachine(), notices, waitForTheKeychain)
}
