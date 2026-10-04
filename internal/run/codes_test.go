package run

import (
	"slices"
	"testing"

	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/secret"
)

// wholeTaskOfTheTest is a task of a test of the digest a point remembers it by: the digest is a
// function of the number, the title and the body of it, and a test of its form needs a task
// (docs/DESIGN.md §7h).
const wholeTaskOfTheTest = "## Что изменится\n\nТело задачи."

// TestTheBoundaryKnowsEveryWordOfTheListsOfTheQueue: a word of a closed list of §6a is read by
// a program that switches on it, and the boundary holds a copy of every such list to refuse a
// document that says a word the format does not name. A copy that went from the code is a
// document published against a list nobody maintains, so every list is held here word by word
// against the constants that write it (D-044, D-082, docs/DESIGN.md §6a).
func TestTheBoundaryKnowsEveryWordOfTheListsOfTheQueue(t *testing.T) {
	for _, tc := range []struct {
		path string
		doc  secret.Document
		want []string
	}{
		{doc: secret.DocumentTaskAttention, path: "attention[].attention_state", want: statesOfTheQueue()},
		{doc: secret.DocumentTaskAttention, path: "attention[].escalated_from", want: statesOfTheQueue()},
		{doc: secret.DocumentTaskAttention, path: "attention[].reason", want: Reasons()},
		{doc: secret.DocumentTaskAttention, path: "attention[].priority",
			want: []string{string(Critical), string(High), string(Normal)}},
		{doc: secret.DocumentTaskAttention, path: "attention[].actable",
			want: []string{string(ActNow), string(ActWatch), string(ActWait), string(ActUnknown)}},
		{doc: secret.DocumentTaskAttention, path: "attention[].next_actor",
			want: []string{ActorOrchestrator, ActorOwner, ActorNobody, ActorEither}},
		{doc: secret.DocumentTaskRun, path: "outcome", want: outcomesOfARun()},
		{doc: secret.DocumentTaskList, path: "runs[].outcome", want: outcomesOfARun()},
		{doc: secret.DocumentState, path: "attempts[].outcome", want: outcomesOfARun()},
	} {
		t.Run(string(tc.doc)+":"+tc.path, func(t *testing.T) {
			words, known := wordsOfTheBoundary(t, tc.doc, tc.path)
			if !known {
				t.Fatalf("the policy of %q does not name %q", tc.doc, tc.path)
			}
			if !sameWords(words, tc.want) {
				t.Errorf("the words of %q in %q are %q, want the words of the code: %q",
					tc.path, tc.doc, words, tc.want)
			}
		})
	}
}

// TestTheBoundaryHoldsTheModesOfAnIdentityAndTheReasonOfAPair: the mode of an identity is two
// words of §7i and the code of the reason of a run is one of the words of §6a, and both are
// read by a program that switches on them.
func TestTheBoundaryHoldsTheModesOfAnIdentityAndTheReasonOfAPair(t *testing.T) {
	if _, words, _ := secret.ClassOf(secret.DocumentState, "attempts[].identity"); !slices.Equal(words, []string{"owner", "bot"}) {
		t.Errorf("the words of the identity of an attempt are %q, want the two words of §7i", words)
	}
	_, settled, _ := secret.ClassOf(secret.DocumentState, "settled.by")
	for _, word := range settled {
		if !KnownReason(word) {
			t.Errorf("the word %q of `settled.by` is not a reason of §6a", word)
		}
	}
	form, known := secret.FormOf(secret.DocumentState, "checkpoint.assignment")
	if !known {
		t.Fatal("the policy of the state does not name the assignment of a point")
	}
	for _, task := range []forge.Task{{Number: 43, Title: "the run of a task", Body: wholeTaskOfTheTest}} {
		if !form(fingerprint(task)) {
			t.Errorf("the form of the assignment of a point refuses the digest the code writes: %q",
				fingerprint(task))
		}
	}
}

// TestTheBoundaryKnowsEveryCodeOfAReason: a reason of a run is a word of the
// canonical list of the codes and then the words of what happened behind a colon,
// and the list is closed: a word out of it is a sentence of a run, cut whole,
// and not a code a program switches on. The list is the reasons of the queue of
// §6a and the two names of the waits of a run that the queue does not read —
// the window of the keychain of the machine and the habit crewflow has already
// answered — and it is held here word by word against the constants that write
// it, so that a code added to the code and forgotten in the list is a code the
// boundary cuts out of a reason (D-044, R5-NEW-10, docs/DESIGN.md §6a, §7e, §7i).
func TestTheBoundaryKnowsEveryCodeOfAReason(t *testing.T) {
	want := append(Reasons(), reasonApproval, reasonRepeated)
	for _, tc := range []struct {
		doc  secret.Document
		path string
	}{
		{doc: secret.DocumentTaskRun, path: "reason"},
		{doc: secret.DocumentTaskResume, path: "reason"},
		{doc: secret.DocumentTaskCheckStalled, path: "[].reason"},
		{doc: secret.DocumentState, path: "attempts[].reason"},
	} {
		t.Run(string(tc.doc)+":"+tc.path, func(t *testing.T) {
			kind, codes, known := secret.ClassOf(tc.doc, tc.path)
			if !known {
				t.Fatalf("the policy of %q does not name %q", tc.doc, tc.path)
			}
			if kind != "reason" {
				t.Fatalf("the field %q of %q is %q, want a reason of the format",
					tc.path, tc.doc, kind)
			}
			if !sameWords(codes, want) {
				t.Errorf("the codes of %q in %q are %q, want the codes of the code: %q",
					tc.path, tc.doc, codes, want)
			}
		})
	}
}

// outcomesOfARun is every outcome of a run in the words the list of the package holds them, and
// statesOfTheQueue is the same for the states of §6a: the words of the constants are the words
// the boundary is held against.
func outcomesOfARun() []string {
	outcomes := make([]string, 0, len(kinds))
	for _, outcome := range kinds {
		outcomes = append(outcomes, string(outcome))
	}
	return outcomes
}

// statesOfTheQueue is every state of the queue in the words of the constants of the package.
func statesOfTheQueue() []string {
	states := make([]string, 0, len(States()))
	for _, state := range States() {
		states = append(states, string(state))
	}
	return states
}

// sameWords is whether two lists of words of the format hold the same words: the order a
// boundary reads a list in is its own, and the words of it are the words of the code.
func sameWords(one, other []string) bool {
	first := slices.Clone(one)
	second := slices.Clone(other)
	slices.Sort(first)
	slices.Sort(second)
	return slices.Equal(first, second)
}

// wordsOfTheBoundary is the closed list the boundary holds for a field of a document.
func wordsOfTheBoundary(t *testing.T, doc secret.Document, path string) ([]string, bool) {
	t.Helper()
	_, words, known := secret.ClassOf(doc, path)
	return words, known
}
