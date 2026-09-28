//go:build !darwin || !cgo

package secret

import (
	"errors"
	"strings"
	"testing"
)

// TestTheStoreOfAMachineThatHasNoneSaysSo: a mode of a run that works as a bot needs a
// key of an App to sign tokens with, and a machine without the keychain of macOS has
// nowhere to keep one. It is told so in the words of the machine, with the name of the
// service and of the account a person has to look for in the settings of the project
// (docs/DESIGN.md §7i).
func TestTheStoreOfAMachineThatHasNoneSaysSo(t *testing.T) {
	store := System()

	_, err := store.Get(Service, AppKey(5107052))
	if !errors.Is(err, ErrNotFound) {
		// The error of an empty store is not the error of a machine with no store at
		// all, and a caller tells them apart: one is a key to import, the other is a
		// machine that cannot hold it.
		if err == nil {
			t.Fatal("Get returned no error, want a machine with no store of secrets to say so")
		}
	}
	for _, want := range []string{"no store of secrets", Service, AppKey(5107052)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Get = %v, want it to mention %q", err, want)
		}
	}
	if err := store.Set(Service, AppKey(5107052), []byte("-----BEGIN PRIVATE KEY-----")); err == nil {
		t.Error("Set returned no error, want a machine with no store of secrets to refuse it")
	}
	if _, err := store.(Presence).Has(Service, AppKey(5107052)); err == nil {
		t.Error("Has returned no error, want a machine with no store of secrets to say that it cannot ask")
	}
}
