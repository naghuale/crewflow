package secret

import (
	"crypto/rand"
	"encoding/binary"
	"testing"
)

// TestTheNameOfAnItemOfATestIsANumberThatEveryDrawCanWrite: the name of the item of the
// test of the keychain is written into the name of a service and of an account, and both
// of them are read as signed numbers (AppKey, strconv.FormatInt). Sixty-four random bits
// are not such a number: the top bit of a draw is one in half of all draws, and the name
// of those draws was refused, so the test of the keychain failed in half of its runs —
// where nobody saw it, because that test runs only when the owner says so with
// CREWFLOW_KEYCHAIN_TEST=1 (docs/DESIGN.md §7i). This test asks the name for a thousand
// draws on any machine and writes nothing into the keychain of the person who runs it:
// every draw must be a number the name of a service and of an account can be built from,
// and every draw must be a name of its own, or two tests on one machine meet in one item.
func TestTheNameOfAnItemOfATestIsANumberThatEveryDrawCanWrite(t *testing.T) {
	const draws = 1000
	seen := make(map[int64]bool, draws)
	for draw := range draws {
		name := randomOfTest(t)
		if name < 0 {
			t.Fatalf("draw %d: the name is %d, want a number the name of an item can be written from", draw, name)
		}
		if seen[name] {
			t.Fatalf("draw %d: the name %d came out for the second time, and two tests meet in one item of the keychain", draw, name)
		}
		seen[name] = true
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
	// The name of an item is read as a signed number (AppKey, strconv.FormatInt), and the
	// top bit of the bits of the machine is one in half of all draws. That bit is dropped
	// rather than the draw refused: a test that writes into the keychain of a person is
	// run now and then and must not fail for a reason no one can see.
	return int64(binary.BigEndian.Uint64(buffer) &^ (1 << 63))
}
