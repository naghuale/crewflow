package secret

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
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

// Report is the answer of a command as it may be published to the channel of the report,
// in the document a program reads it in: every string of the answer with the values of this
// boundary taken out of it, and everything else of it as it was made.
//
// The answer is written as JSON, read back as the tree of a document — objects, arrays and
// the leaves of them — the strings of the leaves are cut, and the tree is written again. The
// walk is over the tree of the document and not over the types the caller made the answer of:
// those types are a pointer, a type of its own, an interface, and a cleaned string belongs to
// none of them, so the walk put a plain `string` into a field of another type and the report
// of a command fell over instead of answering — a pointer took a string in place of the nil
// pointer built for it (R5-NEW-6), a named type took a string that was not of that type
// (R5-NEW-7). A document has no pointers and no types of its own, so the document of an
// answer is always valid and says what the answer said.
//
// The values are cut where the answer is put together and not out of the document that came
// out of it. A number of a report that happens to be a value of a run is a number of the
// report — the password of a proxy is three signs more often than one would like — and a
// document with `[redacted]` in the place of a number is a document no program can read
// (docs/DESIGN.md §7e).
func (o *Out) Report(answer any) ([]byte, error) {
	document, err := json.Marshal(answer)
	if err != nil {
		return nil, fmt.Errorf("the answer as a document: %w", err)
	}
	var tree any
	reader := json.NewDecoder(bytes.NewReader(document))
	// A number of the answer is a number of the document and not a float64 that prints
	// `1e+06` where the answer said 1000000.
	reader.UseNumber()
	if err := reader.Decode(&tree); err != nil {
		return nil, fmt.Errorf("the answer as a document: %w", err)
	}
	// What this boundary knows is asked of it once for the whole document: a document is one
	// publication, and a value a run learned while it was written belongs to the next one
	// (§7e).
	if values := o.Values(); len(values) > 0 {
		tree = cleanDocument(tree, "", func(said string) string { return Redact(said, values...) })
	}
	var published bytes.Buffer
	encoder := json.NewEncoder(&published)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(tree); err != nil {
		return nil, fmt.Errorf("write the document of the answer: %w", err)
	}
	return published.Bytes(), nil
}

// closed is what a field of a closed list of the format carries in its value. Its zero is the
// words of a run: a field that [closedFields] does not name is cut whatever it holds.
type closed uint8

const (
	// oneWord is a field whose whole value is a word out of a closed list of the format.
	oneWord closed = iota + 1
	// codeThenWords is a field that carries a word out of a closed list and then, behind a
	// colon, the words of what happened.
	codeThenWords
)

// closedFields are the fields of the documents of crewflow whose value is a word out of a
// closed list of the format, by the names the schema of a document gives them. A program reads
// these words to decide what a run did — the outcome of a run, the profile of an executor, a
// state of the queue, the code of a reason — and a value of a run that happens to be a part of
// one of them makes the document say something else about the run than the run said about
// itself: a password of `pr` turned `pr-opened` into `[redacted]-opened`, and no program can
// read that word (R5-NEW-8, docs/DESIGN.md §6a, §7e).
//
// A value of such a field is not a secret: it is a word of a fixed set, one of the words
// crewflow itself writes there. Every other field is cut whatever it holds, and a field of a
// closed list that this table does not name is cut as well — the table is the whole of the
// schema the boundary knows, and a row is what it takes to add a closed value of the format to
// it (D-044: one place).
var closedFields = map[string]closed{
	// The outcome of a run, of a merge and of a request of a person: the words of §7a, §7h
	// and §7i, which a program switches on.
	"outcome": oneWord,
	// The profile of the executor and the profile of a proxy: names out of the file of the
	// project, which a run of the same task goes on with (§7d, §7e).
	"profile": oneWord,
	// The state of a change and of a check on the host, and the states of the queue of
	// attention under the two names the schema gives them: §6a, §7g, §7h.
	"state":           oneWord,
	"attention_state": oneWord,
	"escalated_from":  oneWord,
	"merge_state":     oneWord,
	// What crewflow does with a key of the file of the project, how a run was settled, who
	// acts next, whether it may be acted on now, and what a check of it ended as: the words
	// of §5 and §6a.
	"status":     oneWord,
	"by":         oneWord,
	"next_actor": oneWord,
	"priority":   oneWord,
	"actable":    oneWord,
	"result":     oneWord,
	// What a record of a review decided and what the gate answered about one change: the
	// words of §7h.
	"decision": oneWord,
	// A reason is a word out of the closed list of the codes and then the words of what
	// happened behind it (§6a).
	"reason": codeThenWords,
}

// cleanDocument is the tree of a document of an answer with the values of a run taken out of
// its strings, and nothing else of it touched: a number, a flag, a moment and the shape of a
// document are not values a run must not write down (docs/DESIGN.md §7e).
//
// The field it stands under goes down with it, because whether a string is cut is a property
// of the place it is in and not of the string: a word of a closed list of the format is not
// cut wherever it stands.
func cleanDocument(node any, field string, text func(string) string) any {
	switch held := node.(type) {
	case string:
		return cleanValue(field, held, text)
	case map[string]any:
		cleaned := make(map[string]any, len(held))
		for name, value := range held {
			// A name in the keys of a document is as printable as a name in the values of
			// it, and a program reads a document by its keys (§7e).
			cleaned[text(name)] = cleanDocument(value, name, text)
		}
		return cleaned
	case []any:
		cleaned := make([]any, len(held))
		for at, item := range held {
			cleaned[at] = cleanDocument(item, field, text)
		}
		return cleaned
	default:
		return node
	}
}

// cleanValue is one string of a document as it may be published: cut where the words of a run
// are, kept whole where the schema says the value is a word of a closed list of the format
// (R5-NEW-8, docs/DESIGN.md §6a, §7e).
func cleanValue(field, said string, text func(string) string) string {
	switch closedFields[field] {
	case oneWord:
		return said
	case codeThenWords:
		return cleanReason(said, text)
	default:
		return text(said)
	}
}

// cleanReason is a reason as it may be published: a word out of the closed list of the codes
// and then, behind a colon, the words of what happened. The code is kept whole — a program
// reads a reason by the word before the colon, and a reason with a cut in it is not a reason
// of the format — and the words behind it are the words of a run and are cut as any other
// text (R5-NEW-8, docs.DESIGN.md §6a, §7e).
func cleanReason(reason string, text func(string) string) string {
	code, words, spoken := strings.Cut(reason, ":")
	switch {
	case !spoken && oneWordOfAList(reason):
		// The whole of it is one word: a code of the format and not a sentence of a run.
		return reason
	case spoken && oneWordOfAList(code):
		return code + ":" + text(words)
	default:
		// There is no code in it: the whole of it is a sentence of a run, and a sentence
		// of a run is cut whole.
		return text(reason)
	}
}

// oneWordOfAList is whether a value is one word, written as the codes of the closed lists of
// the format are written: no space and no colon in it. The words of what happened are a
// sentence — behind the colon of a reason, or in a field of their own where a person reads
// them (§6a).
func oneWordOfAList(said string) bool {
	return said != "" && !strings.ContainsAny(said, " \t\r\n:")
}

// Reason is a reason of §6a as it may be published to a channel: the word out of the closed
// list of the codes and then the words of what happened behind it, with the values of this
// boundary taken out of the words and not out of the code (docs/DESIGN.md §6a, §7e).
//
// It is a method of its own and not [Out.Text] because a reason is the one place of a
// document where a value of a run may stand inside a word of the format: the state of a task
// is a file kept for ever that programs read a reason out of, and the reason of an attempt is
// a reason of §6a with the words of the run behind it (R5-NEW-8).
func (o *Out) Reason(channel Channel, reason string) (string, error) {
	if !published(channel) {
		return "", fmt.Errorf("the %q of what was written: %w", channel, ErrNoSuchChannel)
	}
	return cleanReason(reason, o.Text), nil
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
	// grow into the beginning of a value. It is the cleaned text of it and not the text as
	// the program wrote it — a value that was cut out of it is cut out once, and the tail
	// that is looked at for the beginning of a value is the tail a person will read (§7e).
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
//
// The whole of what is held back is cleaned first, and only what is left of the cleaned
// text is looked at for the beginning of a value. The place of the cut used to be worked
// out over the text as the program wrote it, before the values were cut out of it, and a
// value whose tail is its own beginning — `abab`, where `ab` is both the end of it and
// the beginning of it — was cut in two by that order: the head of it went into the
// journal as the program wrote it, the tail was held back as a beginning of a value that
// was never finished, and the two halves stood next to each other in the file with the
// whole value between them (R5-NEW-2, §7e).
func (w *Writer) Write(p []byte) (int, error) {
	if w == nil || w.to == nil {
		return len(p), nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.held = append(w.held, p...)
	spellings := spellings(w.values())
	cleaned := redact(w.held, spellings)
	cut := len(cleaned) - pending(cleaned, spellings)
	if cut > 0 {
		if _, err := w.to.Write(cleaned[:cut]); err != nil {
			return 0, err
		}
	}
	w.held = slices.Clone(cleaned[cut:])
	return len(p), nil
}

// Flush publishes what Write held back, and is called when nothing more is coming: the
// last line of a journal is written without a newline of its own more often than a
// person would think, and the end of it is where a token of a run is (§7e).
func (w *Writer) Flush() error {
	if w == nil || w.to == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.flush()
}

// flush is [Writer.Flush] with the lock of the writer already held.
func (w *Writer) flush() error {
	if len(w.held) == 0 {
		return nil
	}
	_, err := w.to.Write(redact(w.held, spellings(w.values())))
	w.held = nil
	return err
}

// Report publishes the answer of a command — the document a program reads with `-json` —
// with the values of this boundary taken out of every string of it, and writes it whole.
//
// It is the boundary itself that writes the document into the stream of the machine and
// not a [Writer.Write] of it: a document that went through the boundary twice was cleaned
// twice, and the second cleaning is the one that cannot tell a string from a number — it
// takes the values out of the ready document byte by byte, and a number that happened to
// be the password of a person stopped being a number (R5-NEW-4, §7e).
func (w *Writer) Report(answer any) error {
	if w == nil || w.to == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	document, err := w.out.Report(answer)
	if err != nil {
		return err
	}
	// What was held back is the end of a line and the document begins with one: it goes
	// first, or the answer of the command would begin in the middle of a line.
	if err := w.flush(); err != nil {
		return err
	}
	_, err = w.to.Write(document)
	return err
}

// values are the values this boundary knows right now. They are asked for once per
// write and not held: a value learned while the executor works has to be held back from
// the next line of its journal, not only from the next run (§7e).
func (w *Writer) values() []Value {
	return w.out.Values()
}

// pending is how much of the end of the cleaned text is the beginning of a value that the
// next write may finish: the longest tail of it that is the beginning of one of the
// spellings and not all of one, which is a whole value and was cut out of it as it was.
func pending(text []byte, spellings []string) int {
	longest := 0
	for _, spelling := range spellings {
		for size := longest + 1; size < len(spelling); size++ {
			if bytes.HasSuffix(text, []byte(spelling[:size])) {
				longest = size
			}
		}
	}
	return longest
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
