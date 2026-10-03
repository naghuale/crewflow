package network

import (
	"slices"
	"testing"

	"github.com/naghuale/crewflow/internal/secret"
)

// TestTheBoundaryKnowsEveryAbilityAndEveryStateOfACheckOfARoute: an answer of
// `network proxy test` and of `doctor network` names the ability it checked and the state that
// check ended as, and both are words of closed lists of §7d. The boundary holds a copy of both
// and the copy is held here against the lists of the package (D-044, D-082, §7d).
func TestTheBoundaryKnowsEveryAbilityAndEveryStateOfACheckOfARoute(t *testing.T) {
	_, abilities, known := secret.ClassOf(secret.DocumentNetworkProxyTest, "capabilities[].capability")
	if !known {
		t.Fatal("the policy of the test of a route does not name the ability of a check")
	}
	if !sameWords(abilities, Capabilities) {
		t.Errorf("the abilities of a route are %q, want the words of the code: %q", abilities, Capabilities)
	}
	_, states, known := secret.ClassOf(secret.DocumentNetworkProxyTest, "capabilities[].result")
	if !known {
		t.Fatal("the policy of the test of a route does not name the state of a check")
	}
	want := []string{StateAvailable, StateUnavailable, StateUnknown, StateInterrupted, StateStale}
	if !sameWords(states, want) {
		t.Errorf("the states of a check are %q, want the words of the code: %q", states, want)
	}
}

// sameWords is whether two lists of words of the format hold the same words: the order a boundary
// reads a list in is its own, and the words of it are the words of the code.
func sameWords(one, other []string) bool {
	first, second := slices.Clone(one), slices.Clone(other)
	slices.Sort(first)
	slices.Sort(second)
	return slices.Equal(first, second)
}
