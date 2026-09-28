//go:build darwin

package secret

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestMain points the keychain of this machine away for every test of the package.
// The store of macOS is the keychain, and the keychain of the machine the tests run
// on holds the secrets of the person: a test that read or wrote there would either
// wait for a window of macOS or leave a key of a test run in a store kept for ever.
// Every test of this package starts a program of its own instead, and this is what
// makes that true of the store [System] hands out as well (docs/DESIGN.md §7i).
func TestMain(m *testing.M) {
	securityProgram = filepath.Join(os.TempDir(), "crewflow-test-security-does-not-exist")
	code := m.Run()
	securityProgram = "/usr/bin/security"
	os.Exit(code)
}

// TestKeychainKeepsAndReadsAValue is the whole of what the keychain is asked to do,
// with a program of the test in place of security: an item is written under the
// service and the account of a secret, and read back from there.
func TestKeychainKeepsAndReadsAValue(t *testing.T) {
	const key = "-----BEGIN PRIVATE KEY-----\nthe key of an App\n-----END PRIVATE KEY-----"
	store, asked := keychainOfTest(t, nil)

	if err := store.Set(Service, AppKey(5107052), []byte(key)); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if asked.last().program != securityProgram {
		t.Errorf("the store started %q, want the program of the test", asked.last().program)
	}
	want := []string{"add-generic-password", "-U", "-s", Service, "-a", AppKey(5107052), "-w"}
	if got := asked.last().args; !slices.Equal(got, want) {
		t.Errorf("the store ran security %q, want %q", line(got), line(want))
	}
	// The value of a secret is written to the standard input of security and is not
	// an argument of it: the arguments of a program are what `ps` shows to every
	// process of the machine.
	if got := strings.TrimRight(string(asked.last().stdin), "\n"); got != key {
		t.Errorf("the store wrote %q to the standard input of security, want the value of the secret", got)
	}

	store, asked = keychainOfTest(t, func(string, []string, []byte) ([]byte, []byte, int) {
		return []byte(key + "\n"), nil, 0
	})
	got, err := store.Get(Service, AppKey(5107052))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != key {
		t.Errorf("Get = %q, want the value that was kept", got)
	}
	wantGet := []string{"find-generic-password", "-s", Service, "-a", AppKey(5107052), "-w"}
	if got := asked.last().args; !slices.Equal(got, wantGet) {
		t.Errorf("the store ran security %q, want %q", line(got), line(wantGet))
	}
}

// TestKeychainKeepsASecondKeyInPlaceOfTheFirst: importing a key of an App twice is
// what an owner does when GitHub gives a new one, and the second is the one that has
// to be used — a second item under the same name would be a key nobody signs with.
func TestKeychainKeepsASecondKeyInPlaceOfTheFirst(t *testing.T) {
	store, asked := keychainOfTest(t, nil)
	for _, key := range []string{"the first key", "the second key"} {
		if err := store.Set(Service, AppKey(5107052), []byte(key)); err != nil {
			t.Fatalf("Set(%q): %v", key, err)
		}
	}
	if !slices.Contains(asked.last().args, "-U") {
		t.Errorf("the store ran security %q, want -U: the second key replaces the first", line(asked.last().args))
	}
}

// TestKeychainAsksWhetherAValueIsThereWithoutReadingIt: a report says that the key of
// an App is on this machine without reading it, and security is asked for the
// attributes of the item alone — with -w it would print the key.
func TestKeychainAsksWhetherAValueIsThereWithoutReadingIt(t *testing.T) {
	store, asked := keychainOfTest(t, func(string, []string, []byte) ([]byte, []byte, int) {
		return []byte("password: \"the key of an App\""), nil, 0
	})
	there, ok := store.(Presence)
	if !ok {
		t.Fatal("the store of this machine cannot say whether a value is there, want it to")
	}
	has, err := there.Has(Service, AppKey(5107052))
	if err != nil {
		t.Fatalf("Has: %v", err)
	}
	if !has {
		t.Error("Has = false, want true: the program found the item")
	}
	want := []string{"find-generic-password", "-s", Service, "-a", AppKey(5107052)}
	if got := asked.last().args; !slices.Equal(got, want) {
		t.Errorf("the store ran security %q, want %q, without -w: the key is not read", line(got), line(want))
	}
}

// TestKeychainTellsAnEmptyStoreFromABrokenOne: a key that was never imported and a
// keychain that could not be read lead a person to different places, and a report may
// only say the first one when it is the first one.
func TestKeychainTellsAnEmptyStoreFromABrokenOne(t *testing.T) {
	cases := []struct {
		name     string
		answer   func(string, []string, []byte) ([]byte, []byte, int)
		wantNone bool
	}{
		{
			name: "the item is not there",
			answer: func(string, []string, []byte) ([]byte, []byte, int) {
				return nil, []byte("security: SecKeychainSearchCopyNext: The specified item could not be found in the keychain.\n"), 44
			},
			wantNone: true,
		},
		{
			name: "the keychain could not be read",
			answer: func(string, []string, []byte) ([]byte, []byte, int) {
				return nil, []byte("security: SecKeychainOpen: User interaction is not allowed.\n"), 36
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, _ := keychainOfTest(t, tc.answer)
			_, err := store.Get(Service, AppKey(5107052))
			there, ok := store.(Presence)
			if !ok {
				t.Fatal("the store of this machine cannot say whether a value is there, want it to")
			}
			has, hasErr := there.Has(Service, AppKey(5107052))
			if tc.wantNone {
				if !errors.Is(err, ErrNotFound) {
					t.Fatalf("Get = %v, want %v", err, ErrNotFound)
				}
				if hasErr != nil {
					t.Fatalf("Has: %v", hasErr)
				}
				if has {
					t.Error("Has = true, want false: the item is not in the keychain")
				}
				return
			}
			// A keychain that could not be read is not an empty one: the error says
			// what security said, and it is not the error of a key that is not there.
			if errors.Is(err, ErrNotFound) {
				t.Fatalf("Get = %v, want the reason the keychain could not be read", err)
			}
			if err == nil {
				t.Error("Get returned no error, want the reason the keychain could not be read")
			}
			if hasErr == nil {
				t.Error("Has returned no error, want the reason the keychain could not be read")
			}
		})
	}
}

// TestSystemIsTheKeychainOfTheTest is the guard of this package: the store crewflow
// itself is given is the keychain, and its program is the one this package was
// pointed at — TestMain above — and not the security of the machine the tests run on.
func TestSystemIsTheKeychainOfTheTest(t *testing.T) {
	store, ok := System().(keychain)
	if !ok {
		t.Fatalf("System() = %T, want the keychain of macOS", System())
	}
	if store.program != securityProgram {
		t.Errorf("the store of the system starts %q, want the program of the test %q",
			store.program, securityProgram)
	}
}

// call is what security was asked, and what it was given on its standard input.
type call struct {
	program string
	args    []string
	stdin   []byte
}

// recorder is the security of a test: it writes down what it was asked and answers
// as the test tells it to.
type recorder struct {
	asked []call
	run   func(name string, args []string, stdin []byte) ([]byte, []byte, int)
}

// last is the last call that was made.
func (r *recorder) last() call { return r.asked[len(r.asked)-1] }

// keychainOfTest is the store of macOS with the program of security replaced by a
// program of the test, which answers as the test tells it to.
func keychainOfTest(t *testing.T, answer func(name string, args []string, stdin []byte) ([]byte, []byte, int)) (Store, *recorder) {
	t.Helper()
	security := &recorder{}
	security.run = func(name string, args []string, stdin []byte) ([]byte, []byte, int) {
		security.asked = append(security.asked, call{program: name, args: args, stdin: stdin})
		if answer == nil {
			return nil, nil, 0
		}
		return answer(name, args, stdin)
	}
	return keychain{program: securityProgram, run: security.run}, security
}

// line is a command line as a test shows it in a message.
func line(args []string) string { return strings.Join(args, " ") }
