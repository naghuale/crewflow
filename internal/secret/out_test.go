package secret

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// The two kinds of a value a run must not write down, as the cases of this file use them: a
// token of an hour a machine wrote, and a password of a proxy a person chose — one long, one
// of three signs (docs/DESIGN.md §7e).
const (
	canaryLong  = "ghs_16C7e42F292c6912E7710c838347Ae178B4a"
	canaryShort = "abc"
)

// boundaryOf is the boundary of the cases of this file with the values they name in it. They
// are one list and not one list per kind: a program of a run is started with all of them in
// its environment, so anything it prints may hold any of them (docs.DESIGN.md §7e).
func boundaryOf(values ...Value) *Out { return NewOut(values...) }

// theCanaries are the two kinds of a value a run must not write down, in one list: a token of
// an hour a machine wrote, and a password of a proxy a person chose — one of three signs,
// which is a password a proxy really takes (docs/DESIGN.md §7e).
func theCanaries() []Value {
	return append(Generated(canaryLong), Chosen(canaryShort)...)
}

// TestTheBoundaryTakesTheValuesOfARunOutOfEverythingItPublishes: the values of a run are
// taken out of the texts of all the channels and out of nothing else — the words of a
// failure stay, and only what of them is a secret is gone (docs/DESIGN.md §7e).
func TestTheBoundaryTakesTheValuesOfARunOutOfEverythingItPublishes(t *testing.T) {
	out := boundaryOf(theCanaries()...)

	for _, channel := range []Channel{ChannelTerminal, ChannelReport, ChannelEvent, ChannelState, ChannelJournal} {
		said, err := out.Publish(channel, "the proxy refused "+canaryShort+" for "+canaryLong)
		if err != nil {
			t.Fatalf("Publish(%q): %v", channel, err)
		}
		if strings.Contains(said, canaryShort) || strings.Contains(said, canaryLong) {
			t.Errorf("the text published to %s is %q, want the values of the run taken out of it", channel, said)
		}
		if !strings.Contains(said, "the proxy refused ") || !strings.Contains(said, " for ") {
			t.Errorf("the text published to %s is %q, want the words of the failure left in it", channel, said)
		}
	}
}

// TestTheBoundaryLearnsWhatTheRunFindsWhileItIsGoing: the credentials of the route of a run
// are read after the run began, and what a program printed before they were read is still
// published after them — a value learned halfway through a run holds back the next line of
// its journal, and the boundary is asked what it knows at the moment of the write
// (D-068 RECHECK-FINDING-5, docs/DESIGN.md §7e).
func TestTheBoundaryLearnsWhatTheRunFindsWhileItIsGoing(t *testing.T) {
	out := boundaryOf()
	var journal bytes.Buffer
	said := out.Writer(&journal)

	if _, err := said.Write([]byte("the route is straight out\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	out.Learn(Chosen(canaryShort)...)
	if _, err := said.Write([]byte("the second attempt went through " + canaryShort + "@proxy.example.com\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := said.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if strings.Contains(journal.String(), canaryShort) {
		t.Errorf("the journal holds %q, want the value learned after it was opened taken out of it", canaryShort)
	}
	if !strings.Contains(journal.String(), "the route is straight out\n") {
		t.Errorf("the journal is %q, want what was written before the value was learned", journal.String())
	}
}

// TestABoundaryRefusesToPublishToAChannelItDoesNotKnow: a way out that nobody named is a way
// out nobody cleared. The text is not returned at all — not in the clear and not cleaned —
// because a value nobody can publish has nowhere to be shown (docs.DESIGN.md §7e).
func TestABoundaryRefusesToPublishToAChannelItDoesNotKnow(t *testing.T) {
	out := boundaryOf(Chosen(canaryShort)...)

	said, err := out.Publish("webhook", "the password of the proxy is "+canaryShort)

	if !errors.Is(err, ErrNoSuchChannel) {
		t.Fatalf("Publish to a channel crewflow does not know = %v, want %v", err, ErrNoSuchChannel)
	}
	if said != "" {
		t.Errorf("Publish to a channel crewflow does not know returned %q, want no text at all", said)
	}
	if !strings.Contains(err.Error(), "webhook") {
		t.Errorf("the refusal %q does not name the channel, want a person to know which one", err)
	}
}

// TestCleaningATextTwiceIsCleaningItOnce: a text of a run goes through the boundary more than
// once — the report of a command and the terminal it is printed on, an event that is said to
// the journal and to the terminal — and a second cleaning must not cut it twice and leave two
// markers where there was one (docs.DESIGN.md §7e).
func TestCleaningATextTwiceIsCleaningItOnce(t *testing.T) {
	out := boundaryOf(theCanaries()...)
	said := "the proxy asked for " + canaryShort + " and the token is " + canaryLong

	once := out.Text(said)
	twice := out.Text(once)

	if twice != once {
		t.Errorf("cleaning a cleaned text = %q, want %q: a boundary a value got out of twice is not idempotent", twice, once)
	}
	if got, want := strings.Count(once, Redacted), 2; got != want {
		t.Errorf("the cleaned text holds %d %q, want one for each of the %d values", got, Redacted, want)
	}
}

// TestTheErrorOfTheBoundaryIsTheErrorTheProgramDecidesBy: what a person reads is the words
// with the values taken out of them, and what a program decides by is the error as it was
// given — `errors.Is` walks into it as it always did, because the logic of a decision reads
// the error and never these words (docs.DESIGN.md §7e, §7i).
func TestTheErrorOfTheBoundaryIsTheErrorTheProgramDecidesBy(t *testing.T) {
	out := boundaryOf(Chosen(canaryShort)...)
	refused := fmt.Errorf("the proxy %s@proxy.example.com:1080 asked for a login", canaryShort)

	said := out.Err(fmt.Errorf("gh pr list: %w", refused))

	if strings.Contains(said.Error(), canaryShort) {
		t.Errorf("the error of the boundary is %q, want the values of the run taken out of it", said)
	}
	if !strings.Contains(said.Error(), "gh pr list: ") {
		t.Errorf("the error of the boundary is %q, want the words of the command left in it", said)
	}
	if !errors.Is(said, refused) {
		t.Error("the error the boundary published cannot be told from the error it was given")
	}
	if out.Err(nil) != nil {
		t.Error("the boundary made an error out of no error")
	}
}

// TestTheZeroBoundaryPublishesWhatItIsGiven: a command with no run behind it has nothing to
// hold back, and a program that was given no boundary at all — a run of a machine of a test,
// an adapter of a caller that handed none — publishes what it was given instead of refusing
// to work (docs/DESIGN.md §7e).
func TestTheZeroBoundaryPublishesWhatItIsGiven(t *testing.T) {
	var out *Out

	if got := out.Text("the words of a program"); got != "the words of a program" {
		t.Errorf("the text of no boundary is %q, want what the program wrote", got)
	}
	if _, err := out.Publish(ChannelJournal, "the words of a program"); err != nil {
		t.Errorf("Publish of no boundary: %v, want the words of a program", err)
	}
	var journal bytes.Buffer
	said := out.Writer(&journal)
	if _, err := said.Write([]byte("the words of a program\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := said.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if journal.String() != "the words of a program\n" {
		t.Errorf("the journal of no boundary is %q, want what the program wrote", journal.String())
	}
	// A channel crewflow does not know is refused by every boundary, nil or not: what is
	// refused is the publishing and not the cleaning.
	if _, err := out.Publish("webhook", "the words of a program"); !errors.Is(err, ErrNoSuchChannel) {
		t.Errorf("Publish to a channel crewflow does not know = %v, want %v", err, ErrNoSuchChannel)
	}
}

// TestTheWriterOfAJournalIsSafeWhileTheRunWritesBesideIt: the journal of an attempt is
// written by the goroutine that reads the stream of the executor and by the run itself at the
// same time — an event of the route, the line about whose name the run works under — and one
// writer with a held-back tail behind two of them is a race and a torn value. The check is
// what `go test -race` reads: two goroutines into one writer of one boundary.
func TestTheWriterOfAJournalIsSafeWhileTheRunWritesBesideIt(t *testing.T) {
	out := boundaryOf(Chosen(canaryShort)...)
	var journal syncBuffer
	said := out.Writer(&journal)
	var waiting sync.WaitGroup
	for _, of := range []func(){
		func() {
			defer waiting.Done()
			for range 40 {
				_, _ = said.Write([]byte("the password is " + canaryShort + " and the line goes on\n"))
			}
			_ = said.Flush()
		},
		func() {
			defer waiting.Done()
			for range 40 {
				_, _ = said.Write([]byte("crewflow: event NETWORK_ROUTE_FAILED reason=dns-failed\n"))
			}
		},
	} {
		waiting.Add(1)
		go of()
	}
	waiting.Wait()

	if strings.Contains(journal.String(), canaryShort) {
		t.Errorf("the journal holds %q, want it taken out of it", canaryShort)
	}
	if !strings.Contains(journal.String(), "NETWORK_ROUTE_FAILED") {
		t.Errorf("the journal is %q, want the events of the run in it", journal.String())
	}
}

// syncBuffer is a buffer a test writes into from more than one goroutine.
type syncBuffer struct {
	mu   sync.Mutex
	held strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.held.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.held.String()
}
