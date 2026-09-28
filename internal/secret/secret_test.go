package secret

import (
	"bytes"
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
		values []string
		want   string
	}{
		{
			name:   "a token in the middle of a line",
			text:   "GH_TOKEN=" + token + " PATH=/usr/bin\n",
			values: []string{token},
			want:   "GH_TOKEN=" + Redacted + " PATH=/usr/bin\n",
		},
		{
			name:   "the same token twice",
			text:   token + " and " + token + "\n",
			values: []string{token},
			want:   Redacted + " and " + Redacted + "\n",
		},
		{
			name:   "a value too short to be a secret of a machine",
			text:   "the run of a task\n",
			values: []string{"main"},
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

// TestRedactorHoldsBackWhatMayStillGrowIntoASecret: the executor writes to a journal
// in pieces, and a value may be split between two of them. A redactor that replaced
// only the values it saw whole would write half a token into a file and leave the
// other half for the next write, which is a journal that reads "[redacted]B4a" and
// the tail of a secret.
func TestRedactorHoldsBackWhatMayStillGrowIntoASecret(t *testing.T) {
	const token = "ghs_16C7e42F292c6912E7710c838347Ae178B4a"
	var out bytes.Buffer
	redactor := NewRedactor(&out, token)

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
	redactor := NewRedactor(&out, token)
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
