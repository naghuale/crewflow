package secret

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestRedactTakesTheValuesOutOfAText is the promise a journal of a run relies on:
// the value of a secret is in the environment of the executor, and an agent that
// prints its own environment must not put a token of an hour into a file that is kept
// for ever.
func TestRedactTakesTheValuesOutOfAText(t *testing.T) {
	const token = "ghs_16C7e42F292c6912E7710c838347Ae178B4a"
	cases := []struct {
		name   string
		text   string
		values []Value
		want   string
	}{
		{
			name:   "a token in the middle of a line",
			text:   "GH_TOKEN=" + token + " PATH=/usr/bin\n",
			values: Generated(token),
			want:   "GH_TOKEN=" + Redacted + " PATH=/usr/bin\n",
		},
		{
			name:   "the same token twice",
			text:   token + " and " + token + "\n",
			values: Generated(token),
			want:   Redacted + " and " + Redacted + "\n",
		},
		{
			name:   "a value too short to be a secret of a machine",
			text:   "the run of a task\n",
			values: Generated("main"),
			want:   "the run of a task\n",
		},
		{
			name:   "nothing to take out",
			text:   "the run of a task\n",
			values: nil,
			want:   "the run of a task\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Redact(tc.text, tc.values...); got != tc.want {
				t.Errorf("Redact(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}

// TestAValueAPersonChoseIsTakenOutWhateverItsLengthIs is the difference between the two
// kinds of value a run keeps out of its files. A token of an hour and a key of an App are
// written by a machine and are long, so a value shorter than that is not looked for: it would
// be taken out of every word of a journal. A password of a proxy was chosen by a person and is
// as long as that person made it — three signs is a password a proxy accepts — and a rule that
// skipped it for being short is the one leak a run may not have
// (D-068 RECHECK-FINDING-5, docs/DESIGN.md §7e).
func TestAValueAPersonChoseIsTakenOutWhateverItsLengthIs(t *testing.T) {
	const token = "ghs_16C7e42F292c6912E7710c838347Ae178B4a"
	cases := []struct {
		name   string
		text   string
		values []Value
		want   string
	}{
		{
			name:   "a password of three signs in the middle of a line",
			text:   "the password of the profile is abc\n",
			values: Chosen("abc"),
			want:   "the password of the profile is " + Redacted + "\n",
		},
		{
			name:   "the same three signs in the spelling of the address",
			text:   "socks5://u:abc@proxy.example.com:1080\n",
			values: Chosen("u:abc", "abc"),
			want:   "socks5://" + Redacted + "@proxy.example.com:1080\n",
		},
		{
			name:   "a value a machine wrote that is short is left alone",
			text:   "the branch is abc\n",
			values: Generated("abc"),
			want:   "the branch is abc\n",
		},
		{
			name:   "both kinds in one list",
			text:   "abc " + token + "\n",
			values: append(Chosen("abc"), Generated(token)...),
			want:   Redacted + " " + Redacted + "\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Redact(tc.text, tc.values...); got != tc.want {
				t.Errorf("Redact(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}

// TestTheRedactorOfAValueAPersonChoseHoldsBackWhatMayStillGrowIntoIt: a journal of a run is
// written while the run goes on, and a password of three signs is as likely to be split
// between two writes of it as a token of forty — the run cannot tell a short value from a
// long one before it is whole, and a redactor that wrote half of a password out is a journal
// that reads `[redacted]c` (D-068 RECHECK-FINDING-5, docs/DESIGN.md §7e).
func TestTheRedactorOfAValueAPersonChoseHoldsBackWhatMayStillGrowIntoIt(t *testing.T) {
	var out bytes.Buffer
	redactor := NewRedactor(&out, Chosen("abc")...)

	for _, piece := range []string{"the password is a", "bc and the address is socks5://u:ab", "c@proxy.example.com:1080\n"} {
		if _, err := redactor.Write([]byte(piece)); err != nil {
			t.Fatalf("Write(%q): %v", piece, err)
		}
	}
	if err := redactor.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	want := "the password is " + Redacted + " and the address is socks5://u:" + Redacted + "@proxy.example.com:1080\n"
	if got := out.String(); got != want {
		t.Errorf("the journal holds %q, want %q", got, want)
	}
}

// TestRedactorHoldsBackWhatMayStillGrowIntoASecret: the executor writes to a journal
// in pieces, and a value may be split between two of them. A redactor that replaced
// only the values it saw whole would write half a token into a file and leave the
// other half for the next write, which is a journal that reads "[redacted]B4a" and
// the tail of a secret.
func TestRedactorHoldsBackWhatMayStillGrowIntoASecret(t *testing.T) {
	const token = "ghs_16C7e42F292c6912E7710c838347Ae178B4a"
	var out bytes.Buffer
	redactor := NewRedactor(&out, Generated(token)...)

	// The token arrives in three pieces, and a line comes after it.
	for _, piece := range []string{"the token is gh", "s_16C7e42F292c", "6912E7710c838347Ae178B4a\nand after it\n"} {
		if _, err := redactor.Write([]byte(piece)); err != nil {
			t.Fatalf("Write(%q): %v", piece, err)
		}
	}
	if err := redactor.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	want := "the token is " + Redacted + "\nand after it\n"
	if got := out.String(); got != want {
		t.Errorf("the journal holds %q, want %q", got, want)
	}
	if strings.Contains(out.String(), token) {
		t.Error("the journal holds the token itself, want it taken out")
	}
}

// TestRedactorFlushesWhatWasHeldBack: a journal whose last line nothing closed is
// still a line, and the end of a run of an agent is where its environment is. What
// was written before the flush is a beginning of a line, and a beginning of a value
// is not written out.
func TestRedactorFlushesWhatWasHeldBack(t *testing.T) {
	const token = "ghs_16C7e42F292c6912E7710c838347Ae178B4a"
	var out bytes.Buffer
	redactor := NewRedactor(&out, Generated(token)...)
	if _, err := redactor.Write([]byte("GH_TOKEN=" + token)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if strings.Contains(out.String(), token) {
		t.Fatalf("the journal holds the token itself before anything is flushed:\n%s", out.String())
	}
	if err := redactor.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if want := "GH_TOKEN=" + Redacted; out.String() != want {
		t.Errorf("the journal holds %q, want %q", out.String(), want)
	}
}

// TestRedactorWithoutSecretsWritesWhatItIsGiven: a run in the mode of the owner has no
// token and no key, and its journal is the words of the agent, whole and as they are.
func TestRedactorWithoutSecretsWritesWhatItIsGiven(t *testing.T) {
	var out bytes.Buffer
	redactor := NewRedactor(&out)
	if _, err := redactor.Write([]byte("I did the work.")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := redactor.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if out.String() != "I did the work." {
		t.Errorf("the journal holds %q, want what the agent wrote", out.String())
	}
}

// TestAppKeyIsNamedAfterTheApp: a person who looks at the keychain of a machine sees
// which App a key belongs to, and not which project was run last.
func TestAppKeyIsNamedAfterTheApp(t *testing.T) {
	if got, want := AppKey(5107052), "github-app-5107052"; got != want {
		t.Errorf("AppKey(5107052) = %q, want %q", got, want)
	}
}

// keychainTestVariable is what the owner sets to let the one test of this package that
// is about the framework of the keychain touch it. The CI and a run of a task never set
// it, and the guard below lets no other test of this package reach the store of the
// machine (docs/DESIGN.md §7i). It is here and not in the test of the framework because
// the guard has to know the name on every machine, and a machine where there is no
// keychain must be able to read the guard as well.
const keychainTestVariable = "CREWFLOW_KEYCHAIN_TEST"

// noKeychainIsTheNameOfTheStoreOfThisMachine is the pattern of the guard below, written
// in two parts so that this test does not match itself.
var noKeychainIsTheNameOfTheStoreOfThisMachine = regexp.MustCompile(`\bSys` + `tem\(\)`)

// TestNoTestOfThisPackageTouchesTheKeychainUnasked: the store of this machine is the
// keychain of the person who runs the tests, and a test that read or wrote there would
// leave a key of a test run in a store kept for ever. A test of this package may reach
// it in exactly two ways: it is built only where the machine has no keychain at all, or
// it is skipped unless the owner says so with the variable of the test of the framework —
// which the CI and a run of a task never set. Every other test hands a run a store of
// its own, and this looks at the tests of the package to keep it that way instead of
// trusting a promise in a comment (docs/DESIGN.md §7i).
func TestNoTestOfThisPackageTouchesTheKeychainUnasked(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the folder of the package: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, "_test.go") || name == "secret_test.go" {
			continue
		}
		test, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !noKeychainIsTheNameOfTheStoreOfThisMachine.Match(test) {
			continue
		}
		// A test of the store of a machine that has none is built only where the keychain
		// of macOS is not there, and it may ask the store of the machine: what it checks
		// is the refusal, and there is nothing to refuse here. A test of the framework
		// itself is skipped unless the owner says so.
		switch text := string(test); {
		case strings.Contains(text, "!darwin"):
		case strings.Contains(text, keychainTestVariable):
		default:
			t.Errorf("%s asks the store of this machine and nothing tells the owner that it does: "+
				"build it only where there is no keychain, or skip it unless %s=1", name, keychainTestVariable)
		}
	}
}
