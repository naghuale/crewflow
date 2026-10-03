package secret

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Document is a kind of document crewflow publishes as JSON, with the version of the format
// of it. The caller names the document: a program knows what it publishes, and a boundary
// that guessed the document from the values in it would be a boundary that a value of a run
// could name (docs/DESIGN.md §7e, D-082).
//
// A kind of document carries a policy of its own, chosen by (document, version) and not by
// the name of a field: `reason` is a code of the closed list in the document of the queue of
// attention and the words of a run in the document of a run, and one list for both would cut
// a code of the format or leave a sentence of a run in a file (R224-4, §6a, §7e).
type Document string

// The whole list of the documents crewflow publishes as JSON. A document outside it is not
// published at all: the caller names it, so a document crewflow has no policy for is a
// program that publishes somewhere nobody looked (D-082: `output-document-policy-missing`).
const (
	// DocumentTaskList is `crewflow task list -json`: the canonical document of §6a with
	// the runs of the project and the queue of attention in it.
	DocumentTaskList Document = "task-list/v1"
	// DocumentTaskAttention is `crewflow task attention -json`: the same document with
	// the queue in it and the runs empty.
	DocumentTaskAttention Document = "task-attention/v1"
	// DocumentTaskAttentionAll is `crewflow task attention -json -all`: the same document
	// over every project of the machine.
	DocumentTaskAttentionAll Document = "task-attention-all/v1"
	// DocumentTaskCheck is `crewflow task check <N> -json`: what the task is missing
	// before it may be run.
	DocumentTaskCheck Document = "task-check/v1"
	// DocumentTaskRun is `crewflow task run <N> -json`: the result of a run (§7i).
	DocumentTaskRun Document = "task-run/v1"
	// DocumentTaskResume is `crewflow task resume <N> -json`: the result of a run that
	// went on from the point of the task (§7i). It is the same document as the result of a
	// run, published by another command, and its policy is the one policy of that document.
	DocumentTaskResume Document = "task-resume/v1"
	// DocumentTaskAdmit is `crewflow task admit <N> <N> -json`: the record of the pair and
	// the file it is in (§7c).
	DocumentTaskAdmit Document = "task-admit/v1"
	// DocumentTaskCheckStalled is `crewflow task check-stalled -json`: the runs of the
	// project that are standing (§6a).
	DocumentTaskCheckStalled Document = "task-check-stalled/v1"
	// DocumentReview is `crewflow review <PR> -json`: the facts of the change and the
	// answer of the gate about it (§7h).
	DocumentReview Document = "review/v1"
	// DocumentMerge is `crewflow merge <PR> -json`: what came of the merge (§7h, §7i).
	DocumentMerge Document = "merge/v1"
	// DocumentVerify is `crewflow verify <PR> -json`: what is true about the change after
	// the merge (§7h).
	DocumentVerify Document = "verify/v1"
	// DocumentDoctor is `crewflow doctor -json`: the report of the machine (§7d, §7e).
	DocumentDoctor Document = "doctor/v1"
	// DocumentDoctorNetwork is `crewflow doctor --network -json`: the checks of the route
	// of the project (§7d).
	DocumentDoctorNetwork Document = "doctor-network/v1"
	// DocumentNetworkProxyList is `crewflow network proxy list -json`: the profiles of the
	// project (§7d).
	DocumentNetworkProxyList Document = "network-proxy-list/v1"
	// DocumentNetworkProxyTest is `crewflow network proxy test <name> -json`: the route and
	// what came of checking it (§7d).
	DocumentNetworkProxyTest Document = "network-proxy-test/v1"
	// DocumentChangelogCheck is `crewflow changelog check -json`: the problems of the
	// journal of the project (§7h).
	DocumentChangelogCheck Document = "changelog-check/v1"
	// DocumentAuthAppCheck is `crewflow auth app check -json`: the key of the App and where
	// it is installed (§7e).
	DocumentAuthAppCheck Document = "auth-app-check/v1"
	// DocumentState is the state of a task as it is kept in the file of it: a record of
	// what crewflow did, written after the actions of a run, and not a publication of it
	// (R224-16, §7h).
	DocumentState Document = "state/v1"
)

// ErrDocumentPolicyMissing is the refusal of a document this boundary has no policy for. A
// caller that named a document crewflow does not know named something the program publishes
// that nobody looked at, and the document is not published at all: an answer without a policy
// is an answer nobody can say what was left out of it (D-082, docs/DESIGN.md §7e).
var ErrDocumentPolicyMissing = errors.New("output-document-policy-missing")

// ErrRedactionPathUnknown is the refusal of a field the policy of the document does not name.
// Every field of a published document is classified in the policy of that document, and a
// field nobody classified is a field nobody looked at: publishing it silently is how a value
// of a run gets out of a document and nobody knows which field of it (D-082,
// docs/DESIGN.md §7e).
var ErrRedactionPathUnknown = errors.New("output-redaction-path-unknown")

// ErrProtectedFieldInvalid is the refusal of a document whose protected value is not what
// the format says it may be: a word out of a closed list, an identifier not of the form its
// own field holds. The value is kept as it is and the document is not published: a protected
// value the format does not name is a document a program cannot act on (D-082,
// docs/DESIGN.md §6a, §7e).
var ErrProtectedFieldInvalid = errors.New("output-protected-field-invalid")

// class is what one field of a document is: the words of a run (text), a word out of a closed
// list of the format (enum), a name a program reads as a name (identifier), a number, a flag,
// or a container whose fields are classified apart. A field outside the policy of its document
// is none of them and stops the publication (D-082, docs/DESIGN.md §7e).
type class uint8

const (
	// text is the words of a run, of a person or of a program: every known form of a value
	// of the run is taken out of them and the words are left.
	text class = iota
	// enumerated is a word out of a closed list of the format. It is kept whole, and a
	// word out of the list stops the publication.
	enumerated
	// identifier is a name the format has a shape for: a commit, a branch, a repository,
	// a session. It is kept whole, and a name not of its shape stops the publication.
	identifier
	// numbered is a number: a number of a report that happens to be the password of a
	// person is a number of the report and not a text (§7e).
	numbered
	// boolean is a flag.
	boolean
	// container is a field with fields of its own, classified under their own paths.
	container
)

// validator is whether a protected value is of the form its own field holds. It is a
// predicate and not a list of the values: a list is what the format declares about a closed
// set of words, and a name of a run is not a closed set — its form is what a program reads it
// by (D-082, docs/DESIGN.md §7e).
type validator func(string) bool

// The forms of the protected names of the format. They are strict on purpose: a name the
// format reads a program by is not something to guess at, and a value that is not of the form
// stops the publication rather than being published as something a program will read wrongly
// (D-082, docs.DESIGN.md §7e).
var (
	// aCommit is a commit as a report names it: the whole of it or the first seven signs
	// of it, and nothing else.
	aCommit = regexp.MustCompile(`^[0-9a-f]{7,40}$`).MatchString
	// aSHA is a commit in full, where the document says a whole one.
	aSHA = regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString
	// aDigest is the digest of a record of a pair, as the record holds it: a digest of the
	// record itself, kept to sixteen signs (§7c).
	aDigest = regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString
	// aFingerprint is the digest a point of a task remembers the task by: a digest of the
	// number, the title and the body of it, kept short because a state file is read by a
	// person who is looking for the branch and not for a hash of an issue (§7h).
	aFingerprint = regexp.MustCompile(`^[0-9a-f]{12}$`).MatchString
	// aRepository is a project the way a person writes it: `owner/name`.
	aRepository = regexp.MustCompile(`^[^/\s]+/[^/\s]+$`).MatchString
	// aMoment is a time of a machine in the words of RFC 3339, which is what every
	// marshaller of a time of this project writes.
	aMoment = func(said string) bool {
		_, err := time.Parse(time.RFC3339, said)
		return err == nil
	}
	// aName is a name the file of a project or the state of a run holds: no signs of a
	// control and nothing a program cannot carry as a name.
	aName = noControl
	// aWord is one word of the format as a host names it: letters, digits and hyphens.
	aWord = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*$`).MatchString
	// aPath is a path of the machine or an address of a host: no signs of a control, and
	// nothing a program would read as two paths.
	aPath = noControl
	// aBody is the words of a host in a field the format declares a paragraph of: no
	// signs of a control. It is where the body of a change request is kept.
	aBody = noControl
	// aURL is the address of a change, a run or a document as the host writes it.
	aURL = func(said string) bool {
		return said == "" || (noControl(said) && (strings.HasPrefix(said, "http://") ||
			strings.HasPrefix(said, "https://")))
	}
)

// noControl is whether a value is without the signs a terminal reads as a command: a value
// of a document is read by a person and written into a file, and a sign of a control in it is
// a sign nobody asked for (docs/DESIGN.md §7e).
func noControl(said string) bool {
	return !strings.ContainsFunc(said, func(sign rune) bool { return sign < 0x20 || sign == 0x7f })
}

// rule is what one field of a document is: the class of it and, where the class asks for one,
// the closed list of its words or the form its value has to hold. A rule with a list and a
// validator is not a field of any document, and the walk refuses the document it stands at.
type rule struct {
	class  class
	words  []string
	format validator
	// code says that the field is a reason of §6a: a word out of the closed list of the
	// codes and then the words of what happened. The class of it is text, and the word
	// before the colon is kept whole: the queue reads a reason by it, and a reason with a
	// cut in it is not a reason of the format (R224-001, §6a).
	code bool
}

// word is the rule of a field whose value is a word out of a closed list of the format.
func ofList(words ...string) rule { return rule{class: enumerated, words: words} }

// name is the rule of a field whose value is a name of the format and has to hold its form.
func ofForm(format validator) rule { return rule{class: identifier, format: format} }

// said is the rule of a field of the words of a run, of a person or of a program.
func freeText() rule { return rule{class: text} }

// reason is the rule of the one field of the documents that carries a word of the format and
// then the words of a run behind it (§6a).
func ofWords() rule { return rule{class: text, code: true} }

// counted is the rule of a field that is a number.
func aNumber() rule { return rule{class: numbered} }

// yesOrNo is the rule of a field that is a flag.
func aFlag() rule { return rule{class: boolean} }

// fields is the rule of a field with fields of its own.
func aContainer() rule { return rule{class: container} }

// ClassOf is the class of a field of a document by the name the class has in the words of the
// format, with the closed list of its words where the field carries one. It is how a test of
// the package that owns a closed list holds the boundary against it, word by word: a list of
// words is a statement about the format, and a copy of it that went from the code is a
// document published against a list nobody maintains (D-044, docs/DESIGN.md §6a).
func ClassOf(doc Document, path string) (string, []string, bool) {
	what, known := policies[doc][path]
	if !known {
		return "", nil, false
	}
	switch what.class {
	case text:
		if what.code {
			return "reason", nil, true
		}
		return "text", nil, true
	case enumerated:
		return "enum", slices.Clone(what.words), true
	case identifier:
		return "identifier", nil, true
	case numbered:
		return "number", nil, true
	case boolean:
		return "bool", nil, true
	default:
		return "container", nil, true
	}
}

// Documents are the kinds of document crewflow publishes, in the order the list of them is
// read: it is what a test walks to hold the policies against the types the commands publish.
func Documents() []Document {
	docs := make([]Document, 0, len(policies))
	for doc := range policies {
		docs = append(docs, doc)
	}
	slices.Sort(docs)
	return docs
}

// cleanDocumentOf is the tree of a document of an answer with the values of a run taken out of
// the strings of it, and every field of it held to the policy of its document: a field the
// policy does not name stops the publication, a protected value the format does not admit
// stops it, and a value of a kind the policy does not name stops it as well — a document a
// program reads by its fields is a document whose fields have to be the fields the format
// says (D-082, docs/DESIGN.md §7e).
//
// What it did not change it hands back as it was, and the answer of the caller is never
// touched: the answer is written as a document, and the tree of that document is what is read
// back, cut and written again (R5-NEW-6, R5-NEW-7, R224-013, §7e).
func cleanDocumentOf(doc Document, node any, path string, cut func(string) string, gaps *[]string) (any, error) {
	rows, known := policies[doc]
	if !known {
		return nil, missingPolicy(doc)
	}
	return fieldOfDocument(doc, rows, node, path, cut, gaps)
}

// fieldOfDocument is one field of a document, at the path it stands at, held to what the
// policy of the document says of it.
func fieldOfDocument(doc Document, rows map[string]rule, node any, path string,
	cut func(string) string, gaps *[]string) (any, error) {
	if node == nil {
		// Nothing is in the field, and what is not in it cannot be cut and cannot be wrong.
		return nil, nil
	}
	what, classified := rows[path]
	if !classified {
		if gaps != nil {
			// A field the policy of the document does not name is cleaned as free text and
			// the gap is said as an event: this is the state of a task, a record written
			// after the actions of a run, and a record is never lost for the sake of a
			// policy (R224-16, D-082).
			*gaps = append(*gaps, path)
			return cut(node.(string)), nil
		}
		return nil, unknownPath(doc, path)
	}
	switch what.class {
	case container:
		return containerOfDocument(doc, rows, node, path, cut, gaps)
	case text:
		said, is := node.(string)
		if !is {
			return nil, wrongKind(doc, path)
		}
		if what.code {
			return cleanReason(said, cut), nil
		}
		return cut(said), nil
	case enumerated, identifier:
		said, is := node.(string)
		if !is {
			return nil, wrongKind(doc, path)
		}
		// An empty protected value is the absence of the value and not a word of the
		// format: a state of a task written before the identity of a run was in it holds
		// `"identity": ""`, and a field of the format that is empty is a field nobody
		// filled in (§6a, §7h).
		if said == "" {
			return said, nil
		}
		if what.class == enumerated && !slices.Contains(what.words, said) {
			return nil, invalidValue(doc, path)
		}
		if what.class == identifier && !what.format(said) {
			return nil, invalidValue(doc, path)
		}
		return said, nil
	case numbered:
		if _, is := node.(json.Number); !is {
			return nil, wrongKind(doc, path)
		}
		return node, nil
	default:
		if _, is := node.(bool); !is {
			return nil, wrongKind(doc, path)
		}
		return node, nil
	}
}

// pathOf is the path of a field of a document: the segments of the path are joined by a dot,
// and the root of the document is the empty path — a document of the format is read by its
// names, and a path is how a policy names them (`a.b`, `a[].c`, `a.*`).
func pathOf(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

// containerOfDocument is a field with fields of its own: the classification of each of them is
// its own row of the policy, under the path of this one. The names of the fields are kept whole
// in every case — a document is read by its names, and a name with a cut in it is a document
// nobody can read by it (R224-001, D-082).
func containerOfDocument(doc Document, rows map[string]rule, node any, path string,
	cut func(string) string, gaps *[]string) (any, error) {
	switch held := node.(type) {
	case map[string]any:
		cleaned := make(map[string]any, len(held))
		// A map the schema declares as dynamic is one row for all of its keys: its keys are
		// names and its values are what the row names, and a key nobody looked at is a key
		// the document does not say what it holds (D-082).
		any := path + ".*"
		for name, value := range held {
			under := pathOf(path, name)
			if _, known := rows[under]; !known {
				if _, wild := rows[any]; wild {
					under = any
				}
			}
			field, err := fieldOfDocument(doc, rows, value, under, cut, gaps)
			if err != nil {
				return nil, err
			}
			cleaned[name] = field
		}
		return cleaned, nil
	case []any:
		cleaned := make([]any, len(held))
		for at, value := range held {
			field, err := fieldOfDocument(doc, rows, value, path+"[]", cut, gaps)
			if err != nil {
				return nil, err
			}
			cleaned[at] = field
		}
		return cleaned, nil
	default:
		return nil, wrongKind(doc, path)
	}
}

// missingPolicy is the refusal of a document this boundary has no policy for: the caller named
// a document crewflow does not know, and publishing it without a policy is publishing it
// without knowing what was left out of it (D-082, docs/DESIGN.md §7e).
func missingPolicy(doc Document) error {
	return fmt.Errorf("the document %q is not one crewflow has a policy for: %w", doc, ErrDocumentPolicyMissing)
}

// unknownPath is the refusal of a field the policy of the document does not name. It carries
// the document and the path and nothing else: the value of the field is not in the refusal,
// because a refusal is a text a person reads and a terminal pastes (D-082, §7e, R224-012).
func unknownPath(doc Document, path string) error {
	return fmt.Errorf("the field %q of the document %q is not named by its policy: %w",
		path, doc, ErrRedactionPathUnknown)
}

// invalidValue is the refusal of a protected value the format does not admit: a word out of a
// closed list that is not in it, or a name not of the form its own field holds. The value is
// kept as it is and the document is not published — a protected value the format does not name
// is a document a program cannot act on (D-082, docs.DESIGN.md §6a, §7e).
func invalidValue(doc Document, path string) error {
	return fmt.Errorf("the protected field %q of the document %q holds a value the format does not admit: %w",
		path, doc, ErrProtectedFieldInvalid)
}

// wrongKind is the refusal of a value of a kind the policy does not name for that field: a
// number where the policy says a word, a word where it says a number. It is what a document
// that stopped being the document of the format looks like from here (D-082, §7e).
func wrongKind(doc Document, path string) error {
	return fmt.Errorf("the field %q of the document %q holds a value of a kind its policy does not name: %w",
		path, doc, ErrJSONSchemaInvalid)
}

// ErrJSONSchemaInvalid is the refusal of a document whose field holds a value of another kind
// than the policy of that field names: a number where the format says a word, a word where it
// says a number, a list where it says a field. It is the kind of the document that stopped
// being the document the format describes (D-082, docs/DESIGN.md §7e).
var ErrJSONSchemaInvalid = errors.New("output-json-schema-invalid")

// StateDocument is the state of a task as it may be written down in the file of it: every
// field of it classified by the policy of [DocumentState], and a field the policy does not name
// cleaned as free text and named in the gaps instead of stopping the write. A state is a record
// of what crewflow did, written after the actions of a run; losing it for the sake of a policy
// loses the recovery of the run with it, and the gap is said as an event rather than paid for
// with the record (R224-16, D-082, docs/DESIGN.md §7h).
func (o *Out) StateDocument(value any) ([]byte, []string, error) {
	document, err := json.Marshal(value)
	if err != nil {
		return nil, nil, fmt.Errorf("the state of a task as a document: %w", err)
	}
	tree, err := treeOfDocument(document)
	if err != nil {
		return nil, nil, err
	}
	var gaps []string
	cleaned, err := cleanDocumentOf(DocumentState, tree, "", o.cutting(), &gaps)
	if err != nil {
		return nil, nil, err
	}
	published, err := asDocument(cleaned)
	if err != nil {
		return nil, nil, err
	}
	return published, gaps, nil
}

// treeOfDocument is the tree of a document of an answer: the objects, the lists and the leaves
// of it, with every number of it the number the answer wrote. A number that is read as a
// float64 loses its last signs above 2^53, and the number of a repository of the host is above
// it (R224-010, docs/DESIGN.md §7e).
func treeOfDocument(document []byte) (any, error) {
	reader := json.NewDecoder(bytes.NewReader(document))
	reader.UseNumber()
	var tree any
	if err := reader.Decode(&tree); err != nil {
		return nil, fmt.Errorf("the answer as a document: %w", err)
	}
	return tree, nil
}

// asDocument is a tree of a document written back as the document a program reads: the same
// shape, two signs of a space, and a trailing line break.
func asDocument(tree any) ([]byte, error) {
	var published bytes.Buffer
	encoder := json.NewEncoder(&published)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(tree); err != nil {
		return nil, fmt.Errorf("write the document of the answer: %w", err)
	}
	return published.Bytes(), nil
}

// cutting is the function that takes the values of this boundary out of a string, asked for
// once for a whole document: a document is one publication, and a value a run learned while it
// was written belongs to the next one (§7e).
func (o *Out) cutting() func(string) string {
	values := o.Values()
	if len(values) == 0 {
		return func(said string) string { return said }
	}
	return func(said string) string { return Redact(said, values...) }
}
