//go:build darwin && cgo

package secret

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"strconv"
	"testing"
)

// TestTheKeychainOfThisMachineWhenTheOwnerAsks is the one test of this package that
// writes into the keychain of the person who runs it, and it does nothing at all unless
// that person says so:
//
//	CREWFLOW_KEYCHAIN_TEST=1 go test -run TestTheKeychainOfThisMachine ./internal/secret
//
// It keeps a value under a service of its own with a random name, reads it, says that it
// is there, changes it, keeps a value that is not printable at all, and takes the item
// away again. It is the one check of the store of macOS that the CI cannot do for us:
// everything else in this package is about the interface, and this is about the
// framework (docs/DESIGN.md §7i).
func TestTheKeychainOfThisMachineWhenTheOwnerAsks(t *testing.T) {
	if os.Getenv(keychainTestVariable) != "1" {
		t.Skipf("%s is not set, and it must not be: this test writes into the keychain of this machine", keychainTestVariable)
	}
	// The service is not the one of crewflow: an item under the name of crewflow could
	// be mistaken for a key of an app by a person looking at their own keychain.
	service := Service + ".test." + strconv.FormatInt(randomOfTest(t), 10)
	account := AppKey(randomOfTest(t))
	store, keys := System(), keychain{}
	presence, ok := store.(Presence)
	if !ok {
		t.Fatal("the store of this machine cannot say whether a value is there, want the keychain of macOS")
	}
	trust, ok := store.(Trust)
	if !ok {
		t.Fatal("the store of this machine cannot say whether this program is allowed to read it, want the keychain of macOS")
	}
	// Whatever is under that name is taken away first and last: an item of a test that
	// outlived it is rubbish in a store of a person.
	if err := keys.Delete(service, account); err != nil {
		t.Fatalf("take the item of the test away before it starts: %v", err)
	}
	t.Cleanup(func() {
		if err := keys.Delete(service, account); err != nil {
			t.Errorf("take the item %s/%s away: %v", service, account, err)
		}
	})

	if _, err := store.Get(service, account); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get of a name that was never kept = %v, want %v", err, ErrNotFound)
	}
	if there, err := presence.Has(service, account); err != nil || there {
		t.Errorf("Has of a name that was never kept = %t (%v), want false and no error", there, err)
	}
	key := []byte("-----BEGIN PRIVATE KEY-----\nthe key of a test of the keychain\n-----END PRIVATE KEY-----\n")
	if err := store.Set(service, account, key); err != nil {
		t.Fatalf("Set: %v", err)
	}
	there, err := presence.Has(service, account)
	if err != nil {
		t.Fatalf("Has right after a Set: %v", err)
	}
	if !there {
		t.Error("Has = false right after a Set, want true")
	}
	kept, err := store.Get(service, account)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(kept) != string(key) {
		t.Errorf("Get = %q, want the value that was kept", kept)
	}
	// A second import changes the value in place: one item under this name, and the
	// last one is the value a run of this project signs with.
	second := []byte("-----BEGIN PRIVATE KEY-----\nthe new key of a test\n-----END PRIVATE KEY-----\n")
	if err := store.Set(service, account, second); err != nil {
		t.Fatalf("the second Set: %v", err)
	}
	kept, err = store.Get(service, account)
	if err != nil {
		t.Fatalf("Get after the second Set: %v", err)
	}
	if string(kept) != string(second) {
		t.Errorf("Get = %q, want the value of the second Set", kept)
	}
	// The question of the access of this program is the one question of the store of a
	// machine that is asked without a window of the system, and a service with nothing
	// in it has nothing to be allowed for: that is the answer every machine gives, and
	// it is the one a report of a machine reads as "there is nothing to ask about".
	if _, err := trust.Allowed(service + ".never-kept"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Allowed of a service that was never written to = %v, want %v", err, ErrNotFound)
	}
	// What this program may read of an item that is there is a question about the
	// signature of this binary and about the keychain of this machine, and the two are
	// not the same on every machine: a test binary of Go is not a signed program and a
	// keychain of a person may be locked. The question is asked without a window
	// either way, which is what matters here, and a machine that cannot answer it says
	// so instead of opening one.
	if allowed, err := trust.Allowed(service); err != nil {
		t.Errorf("Allowed of the service of the test: %v, want an answer and not a window", err)
	} else if allowed {
		t.Log("this machine lets the test binary read the item of the test without a window")
	}
	// A value that is not printable is a value of a secret, and it comes back as it
	// went in: the bytes of a key of an app are not lines of text, which is why the
	// store of macOS is the framework and not a program of a terminal.
	binary := []byte{0x00, 0x01, 0xff, 0xfe, '\n', 0x00}
	if err := store.Set(service, account, binary); err != nil {
		t.Fatalf("the Set of a value that is not printable: %v", err)
	}
	kept, err = store.Get(service, account)
	if err != nil {
		t.Fatalf("Get of a value that is not printable: %v", err)
	}
	if string(kept) != string(binary) {
		t.Errorf("Get = %q, want the bytes that were kept", kept)
	}
}

// randomOfTest is a name of its own for an item of a test, so that two tests on one
// machine never meet in one item of the keychain.
func randomOfTest(t *testing.T) int64 {
	t.Helper()
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		t.Fatalf("read the randomness of the machine: %v", err)
	}
	number, err := strconv.ParseInt(hex.EncodeToString(buffer), 16, 64)
	if err != nil {
		t.Fatalf("the name of the item of the test: %v", err)
	}
	return number
}
