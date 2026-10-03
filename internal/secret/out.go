package secret

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
)

// Channel is where a text of crewflow is going to be read. A channel is named by the
// caller that publishes, because a way out that nobody named is a way out nobody
// cleared: the values of a run are taken out of a text at the boundary and at no other
// place, and a channel of its own asks for the boundary as much as the terminal does
// (docs/DESIGN.md §7e).
//
// The five channels below are the whole of them. A sixth one has to be written here
// first: a program of crewflow that publishes somewhere new and does not say which
// channel it is publishing to is a program that went around the boundary, and a
// boundary a thing can go around is not one (D-068 RECHECK-FINDING-5, §7e).
type Channel string

const (
	// ChannelTerminal is what the person who started the command reads on the terminal:
	// the report of a command and the lines of a run that are said as it goes.
	ChannelTerminal Channel = "terminal"
	// ChannelReport is what a command prints with `-json`, and every other answer of it
	// a program of the project reads: the checks of a route, a profile, a commit.
	ChannelReport Channel = "report"
	// ChannelEvent is one line of an event of a run, said into the journal of the
	// attempt and onto the terminal at the same time.
	ChannelEvent Channel = "event"
	// ChannelState is the state of a task: what `crewflow task list` and the queue of
	// attention read, and what a person opens when a run ended in a way nobody expects.
	ChannelState Channel = "state"
	// ChannelJournal is the journal of an attempt: what the executor wrote, what it said
	// on the way out, and what crewflow noted about a failure of the run itself.
	ChannelJournal Channel = "journal"
)

// ErrNoSuchChannel is the refusal of a text crewflow cannot say where it goes. It is a
// sentinel and not a sentence, because the caller has one thing to do about it: not
// publish the text. A text that cannot be published is not published in the clear
// either — a caller that has nowhere to clear a value has nowhere to show it
// (docs/DESIGN.md §7e, §7i).
var ErrNoSuchChannel = errors.New("this is not a way crewflow publishes to")

// Out is the one way out of crewflow: the terminal of the person, the journals of the
// attempts, the events, the states of the tasks and every answer of `-json` are put
// through it, and a value a run must not write down is taken out of them there and
// nowhere else (docs/DESIGN.md §7e).
//
// It is a value and not a writer because a run learns what it must not write down while
// it is going: the credentials of the profile the owner chose are read before the first
// program is started, and the credentials of the profile of a second attempt are read
// only when that attempt is really being made — a route has one set of credentials and
// a fallback operation has two, and the result of the first attempt has to be cleaned
// with the values of the second one as well, because what the second program printed may
// be what a person reads about the first failure (D-068 RECHECK-FINDING-5, §7e).
//
// A redactor made before it learned a value would not hold that value back. So the
// boundary is asked for the values it knows at the moment a text is published and not at
// the moment it was made, and [Out.Learn] is how a run adds what it found.
//
// The zero boundary and a nil one publish what they are given: a command with no run
// behind it has nothing to hold back, and a program that cannot say where it would say
// something says it as it is.
type Out struct {
	// mu guards values, which a run adds to while it is going and every publication
	// reads: the journal of an attempt is written from a goroutine of the executor while
	// the run reads a route and learns what that route keeps (§7e, §7i).
	mu sync.Mutex
	// values are the values this run must not write down: the ones a machine wrote for
	// itself, the ones a person chose, and the credentials of every route an operation
	// of this run went out by.
	values []Value
}

// NewOut returns the boundary of a run with the values it already knows.
func NewOut(values ...Value) *Out {
	out := &Out{}
	out.Learn(values...)
	return out
}

// Learn adds the values a run must not write down: the credentials of a route that was
// really made go out by, the token of the App the run works as, and everything else a
// program of the run may print back. It is how one run of a fallback operation ends up
// with the values of both of its routes, and it is called wherever a value is read and
// not only where the run begins (§7e).
//
// The same value learned twice is one value: a run that goes on through the same profile
// twice does not hold twice what it prints once, and a list that grows a value twice
// takes twice as long over every line of a journal for nothing.
func (o *Out) Learn(values ...Value) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, value := range values {
		if value.text == "" || slices.Contains(o.values, value) {
			continue
		}
		o.values = append(o.values, value)
	}
}

// Values are the values this boundary takes out of everything it publishes, in the
// order they were learned: the secrets of a run in the order a run found them, which is
// what a report of a boundary says (§7e).
func (o *Out) Values() []Value {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.values)
}

// Text is the text as it may be published: every value this boundary knows taken out of
// it, and nothing else of it. It is the whole text and not a part of it — a value cut
// out of a word leaves a word nobody can read back, and a journal is read by people.
//
// Cleaning a text twice is cleaning it once: what was taken out is no longer in it, so
// a text that goes through the boundary twice — a report of a command and the terminal
// it was printed on — is not cut twice and is not left with two markers in it (§7e).
func (o *Out) Text(text string) string {
	if o == nil {
		return text
	}
	values := o.Values()
	if len(values) == 0 {
		return text
	}
	return Redact(text, values...)
}

// Err is the error as it may be published: its words with the values of this boundary
// taken out of them, and the error itself under it, whole and unedited. The two belong
// to each other, because a program decides by the error it was given — `errors.Is` and
// `errors.As` walk into the cause as they always did — and a person reads the words
// (docs/DESIGN.md §7e, §7i).
//
// What a program decides by is the error and not these words: the words are for a person
// and for a report, and a text that has been cleaned must not go back into the logic
// that decides (§7e).
func (o *Out) Err(err error) error {
	if err == nil {
		return nil
	}
	return &said{words: o.Text(err.Error()), cause: err}
}

// Publish is the text of one channel with the values of this boundary taken out of it.
// A channel crewflow does not know is refused with [ErrNoSuchChannel] and the text is
// not returned at all: a value nobody can clear has nowhere to be shown, and publishing
// it in the clear is the one answer this boundary must never give (docs/DESIGN.md §7e).
func (o *Out) Publish(channel Channel, text string) (string, error) {
	if !published(channel) {
		return "", fmt.Errorf("the %q of what was written: %w", channel, ErrNoSuchChannel)
	}
	return o.Text(text), nil
}

// published is whether crewflow publishes to this channel at all. The five above are the
// whole list, and the list is closed on purpose: a program that publishes somewhere new
// and does not say where has to come here and be refused, rather than write a value of a
// run into a file kept for ever (docs/DESIGN.md §7e).
func published(channel Channel) bool {
	switch channel {
	case ChannelTerminal, ChannelReport, ChannelEvent, ChannelState, ChannelJournal:
		return true
	}
	return false
}

// Writer is where crewflow publishes what a program wrote as it wrote it: the journal of
// an attempt, the stream of the executor, the terminal of the person who started the
// command. Every text written to it goes through the boundary it was made from, with the
// values the boundary knows at the moment of the write.
//
// It holds back the end of what was written for as long as it is the beginning of a
// value the next write may finish: a value is split between two writes of a journal more
// often than a person would think, and [Writer.Flush] writes what is left when nothing
// more is coming (§7e).
type Writer struct {
	// mu guards held and the writing into to: the journal of an attempt is written by the
	// goroutine that reads the stream of the executor and by the run itself — an event of
	// the route, the line about the identity of the run — at the same time, and one
	// writer with a held-back tail behind two of them is a race and a torn value.
	mu sync.Mutex
	// out is the boundary every write of this writer goes through, and to is where the
	// text that came out of it goes.
	out *Out
	to  io.Writer
	// held is what was written and not passed on yet: the end of a text that may still
	// grow into the beginning of a value.
	held []byte
}

// Writer returns where the text of a stream is published: into `to`, with the values of
// this boundary taken out of it. The zero boundary publishes what it is given, and a
// writer of it is never nil — a caller that was given nowhere to publish has said so by
// having nothing to clean, not by having nothing to write (§7e).
func (o *Out) Writer(to io.Writer) *Writer {
	return &Writer{out: o, to: to}
}

// Write publishes what a program wrote, with the values taken out, and holds back the
// end of it while it may still be the beginning of a value. It reports as many bytes as
// it was given: the program that writes to it is not told about the values in it, and a
// program that is told would go and look for them (§7e).
func (w *Writer) Write(p []byte) (int, error) {
	if w == nil || w.to == nil {
		return len(p), nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.held = append(w.held, p...)
	cut := len(w.held) - w.pending()
	if cut == 0 {
		return len(p), nil
	}
	if _, err := w.to.Write(redact(w.held[:cut], spellings(w.values()))); err != nil {
		return 0, err
	}
	w.held = slices.Clone(w.held[cut:])
	return len(p), nil
}

// Flush publishes what Write held back, and is called when nothing more is coming: the
// last line of a journal is written without a newline of its own more often than a
// person would think, and the end of it is where a token of a run is (§7e).
func (w *Writer) Flush() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.held) == 0 {
		return nil
	}
	_, err := w.to.Write(redact(w.held, spellings(w.values())))
	w.held = nil
	return err
}

// values are the values this boundary knows right now. They are asked for once per
// write and not held: a value learned while the executor works has to be held back from
// the next line of its journal, not only from the next run (§7e).
func (w *Writer) values() []Value {
	return w.out.Values()
}

// pending is how much of the end of the text is the beginning of a value that the next
// write may finish: the longest tail of it that is the beginning of one of the values
// and not all of one, which is a whole value and is redacted as it is.
func (w *Writer) pending() int {
	longest := 0
	for _, value := range w.values() {
		if !worthLookingFor(value) {
			continue
		}
		for size := longest + 1; size < len(value.text); size++ {
			if bytes.HasSuffix(w.held, []byte(value.text[:size])) {
				longest = size
			}
		}
	}
	return longest
}

// spellings are the texts a set of values is looked for by in everything crewflow
// publishes. A value too short to be the secret of its own kind is left out of it: it is
// not looked for in a text at all, and a value that is not looked for must not be
// looked for twice (§7e).
func spellings(values []Value) []string {
	spellings := make([]string, 0, len(values))
	for _, value := range values {
		if worthLookingFor(value) {
			spellings = append(spellings, value.text)
		}
	}
	return spellings
}

// said is an error whose words are for a person and whose cause is for a program: the
// two are one error, and a program that walks into the cause finds the error it was
// given as it was (docs/DESIGN.md §7e, §7i).
type said struct {
	// words is the text of the error with the values of the boundary taken out of it.
	words string
	// cause is the error as it was given, with nothing of it edited.
	cause error
}

// Error is the words, which is what a report and a terminal print.
func (s *said) Error() string { return s.words }

// Unwrap is the error as it was given, which is what a program decides by.
func (s *said) Unwrap() error { return s.cause }
