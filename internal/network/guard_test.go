package network

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"testing"
)

// TestMain keeps the tests of this package off the network of the machine they run on: the
// proxy a person has in the environment of their shell is cleared, so that no program of
// crewflow a test starts — gh, git, an executor — can reach it, and a check of a route that
// wanted to would find nothing where a proxy was (docs/DESIGN.md §7d, §7e).
//
// It is here because one test of this package did exactly that: the check of a route through
// a profile dialed the proxy the file of the project named, and the machine of a person asked
// a window about it before answering. A test that reaches somebody's network is a test of
// somebody's network, and it reports the answer of that network as a fact about a route.
func TestMain(m *testing.M) {
	for _, name := range []string{
		"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY",
		"http_proxy", "https_proxy", "all_proxy", "no_proxy",
	} {
		_ = os.Unsetenv(name)
	}
	os.Exit(m.Run())
}

// dialOfTheTest is the dialer of every check of this package: it connects to a loopback
// address, where the servers of a test listen, and refuses everything else with a failure
// that names the address and says which of the two it saw. A refused dial fails the test that
// asked for it — the dial happens in the goroutine of the HTTP of a check, and `t.Errorf` is
// the only thing a goroutine of a test may say about the test that started it.
//
// This is the guard of the whole package, not of one test: every [Machine] a case builds
// goes through [machine], and a case that builds one by hand has this dialer to hand.
func dialOfTheTest(t *testing.T) func(ctx context.Context, network, address string) (net.Conn, error) {
	t.Helper()
	dialer := &net.Dialer{}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			t.Errorf("a check of a route dialled %q, which is not an address with a port: "+
				"the network of a test is 127.0.0.1 and ::1 and nothing else", address)
			return nil, fmt.Errorf("no test of crewflow dials %q", address)
		}
		if _, err := aPort(port); err != nil {
			t.Errorf("a check of a route dialled %q, and %q is not a port", address, port)
			return nil, fmt.Errorf("no test of crewflow dials %q", address)
		}
		if where, notAPrefix := netip.ParseAddr(host); notAPrefix == nil && where.IsLoopback() {
			return dialer.DialContext(ctx, network, net.JoinHostPort(host, port))
		}
		if host == "localhost" {
			return dialer.DialContext(ctx, network, net.JoinHostPort("127.0.0.1", port))
		}
		t.Errorf("a check of a route dialled %q: only 127.0.0.1 and ::1 are the network of a test, "+
			"and a name would be a lookup in the resolver of the machine as well", address)
		return nil, fmt.Errorf("no test of crewflow dials %q", address)
	}
}

// aPort is the check that what a dial names after the last colon is a port at all: a
// test that dialled `proxy.example.com` would ask the resolver of the machine, and that
// lookup is the network too.
func aPort(port string) (int, error) {
	number := 0
	for _, r := range port {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("%q is not a number", port)
		}
		number = number*10 + int(r-'0')
	}
	if number < 1 || number > 65535 {
		return 0, fmt.Errorf("%q is not a port", port)
	}
	return number, nil
}
