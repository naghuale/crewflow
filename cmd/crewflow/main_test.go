package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/naghuale/crewflow/internal/buildinfo"
	"github.com/naghuale/crewflow/internal/secret"
)

// TestMain points the home directory of the machine at a folder of the run of the
// tests, for every test of the package. A command of crewflow resolves "~" to the
// home of the person it runs on, and a test of it must not write there: what a test
// leaves in the home of a person outlives the test, and a journal or a state of
// another project in it makes a later run believe what never happened. A test that
// needs a home of its own sets one; this is the net under all of them.
//
// It also makes this binary the crewflow that git starts as the helper of the
// credentials: git names the program of the setting `credential.helper`, and when a
// test of this package builds that setting out of the adapter it names this binary. A
// binary of a test that is not the helper runs the tests; the same binary, started by
// git as `auth git-credential …`, is crewflow and does that and nothing else. The store
// and the API it works against are the ones the test named in the environment, so that a
// helper of a test signs a token of the test and never opens the keychain of the
// machine or asks GitHub for anything (docs/DESIGN.md §7i).
func TestMain(m *testing.M) {
	if helper, is := startedAsTheHelper(os.Args[1:]); is {
		os.Exit(helper)
	}
	// The home of the machine is remembered before it is replaced: a test that starts a
	// program of the machine — `go build` of the script that cuts a release — needs the
	// caches of the machine and not the empty ones of the folder of the test, and the
	// caches of the go toolchain are under the home it asks for (docs/DESIGN.md §7a).
	machineHome, _ = os.UserHomeDir()
	home, err := os.MkdirTemp("", "crewflow-test-home-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "crewflow tests: make a home of their own: %v\n", err)
		os.Exit(1)
	}
	for _, key := range []string{"HOME", "USERPROFILE"} {
		if err := os.Setenv(key, home); err != nil {
			fmt.Fprintf(os.Stderr, "crewflow tests: point %s at %s: %v\n", key, home, err)
			os.Exit(1)
		}
	}
	code := m.Run()
	if err := os.RemoveAll(home); err != nil {
		fmt.Fprintf(os.Stderr, "crewflow tests: take %s away: %v\n", home, err)
	}
	os.Exit(code)
}

// machineHome is the home of the machine the tests run on: every test gets a home of its
// own under TestMain's, and a test that starts a program of the machine asks go where the
// caches of the toolchain are through this one.
var machineHome string

// The names the tests of the helper of the credentials leave in the environment of the
// binary they start, and the words that make it the helper.
const (
	helperStoreVar  = "CREWFLOW_TEST_KEY"
	helperServerVar = "CREWFLOW_TEST_CA"
	helperSaidVar   = "CREWFLOW_TEST_SAID"
	helperOfGit     = "auth"
	helperItself    = "git-credential"
)

// startedAsTheHelper says whether this binary was started by git as the helper of the
// credentials, and answers as it should if it was: the arguments are the ones git wrote
// and they are handed to the command as they are, in the order git wrote them.
//
// What the helper said goes to the file the test named, because git says nothing about a
// helper that could not answer it: a test of the pairing of git and crewflow has to be
// able to read what each of them said, and this is where the words of crewflow end up
// (docs/DESIGN.md §7i).
func startedAsTheHelper(args []string) (int, bool) {
	if len(args) < 2 || args[0] != helperOfGit || args[1] != helperItself {
		return 0, false
	}
	secretsOfMachine = func() secret.Store { return storeNamedInTheEnvironment() }
	httpOfMachine = clientOfTheServerNamedInTheEnvironment()
	clockOfMachine = func() time.Time { return time.Date(2026, time.September, 28, 12, 0, 0, 0, time.UTC) }
	said := os.Stderr
	if path := os.Getenv(helperSaidVar); path != "" {
		file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			fmt.Fprintf(said, "crewflow tests: open %s: %v\n", path, err)
			os.Exit(1)
		}
		said = file
		defer file.Close()
	}
	// The request of git and the answer of the helper go to the same file as its words:
	// what git asked is half of the pairing, and a test that could not read it would be
	// a test of a helper that answers and of nothing else.
	var out io.Writer = os.Stdout
	if os.Getenv(helperSaidVar) != "" {
		out = io.MultiWriter(os.Stdout, said)
		authStdin = io.TeeReader(os.Stdin, said)
	}
	code := run(args, out, said)
	fmt.Fprintf(said, "crewflow auth git-credential: started with %v, answered with %d\n", args, code)
	return code, true
}

// storeNamedInTheEnvironment is the store of a helper of a test: the key the test wrote
// into a folder of its own, and nothing where the test named none — which is the case of
// a machine whose keychain holds no key of the app (docs/DESIGN.md §7i).
func storeNamedInTheEnvironment() secret.Store {
	store := &storeOfTheTest{}
	path := os.Getenv(helperStoreVar)
	if path == "" {
		return store
	}
	key, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "crewflow auth git-credential: read the key of the test: %v\n", err)
		os.Exit(1)
	}
	store.key = key
	return store
}

// clientOfTheServerNamedInTheEnvironment is the client of a helper of a test, which
// trusts the API of the test and nothing else: the server of the test holds a
// certificate of its own, and a helper that answered with a token of a real app would be
// a helper of a test asking GitHub (docs/DESIGN.md §7i).
func clientOfTheServerNamedInTheEnvironment() *http.Client {
	certificate, err := os.ReadFile(os.Getenv(helperServerVar))
	if err != nil {
		fmt.Fprintf(os.Stderr, "crewflow auth git-credential: read the certificate of the test: %v\n", err)
		os.Exit(1)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certificate) {
		fmt.Fprintf(os.Stderr, "crewflow auth git-credential: the certificate of the test is not one\n")
		os.Exit(1)
	}
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}},
	}
}

// TestRunVersion shows what the program on the machine is: the version and the commit it
// was built from, and the capabilities its code has — the list a project writes in
// `[crewflow] requires` and the list a command refuses without (F-178, docs.DESIGN.md §5).
func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(version) = %d, want 0 (stderr: %q)", code, stderr.String())
	}
	want := "crewflow dev (commit unknown, built unknown)\n" +
		"capabilities: " + strings.Join(buildinfo.Capabilities(), ", ") + "\n"
	if got := stdout.String(); got != want {
		t.Errorf("run(version) wrote %q, want %q", got, want)
	}
	for _, capability := range buildinfo.Capabilities() {
		if !strings.Contains(stdout.String(), capability) {
			t.Errorf("run(version) wrote %q, want it to name the capability %q", stdout.String(), capability)
		}
	}
	if stderr.Len() != 0 {
		t.Errorf("run(version) wrote %q to stderr, want nothing", stderr.String())
	}
}

func TestRunHelp(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		t.Run(arg, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run([]string{arg}, &stdout, &stderr); code != 0 {
				t.Fatalf("run(%s) = %d, want 0 (stderr: %q)", arg, code, stderr.String())
			}
			if !strings.Contains(stdout.String(), "Usage:") {
				t.Errorf("run(%s) wrote %q, want the usage", arg, stdout.String())
			}
			for _, sub := range []string{"doctor", "version", "help"} {
				if !strings.Contains(stdout.String(), sub) {
					t.Errorf("usage does not mention the %q subcommand", sub)
				}
			}
			if stderr.Len() != 0 {
				t.Errorf("run(%s) wrote %q to stderr, want nothing", arg, stderr.String())
			}
		})
	}
}

func TestRunUnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"bogus"}, &stdout, &stderr); code != exitUsage {
		t.Errorf("run(bogus) = %d, want %d", code, exitUsage)
	}
	if stdout.Len() != 0 {
		t.Errorf("run(bogus) wrote %q to stdout, want nothing", stdout.String())
	}
	for _, want := range []string{"bogus", "Usage:"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("run(bogus) wrote %q to stderr, want it to mention %q", stderr.String(), want)
		}
	}
}

func TestRunWithoutSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != exitUsage {
		t.Errorf("run() = %d, want %d", code, exitUsage)
	}
	if stdout.Len() != 0 {
		t.Errorf("run() wrote %q to stdout, want nothing", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Errorf("run() wrote %q to stderr, want the usage", stderr.String())
	}
}

func TestRunBadFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"version", "-nope"}, &stdout, &stderr); code != exitUsage {
		t.Errorf("run(version -nope) = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr.String(), "nope") {
		t.Errorf("run(version -nope) wrote %q to stderr, want it to mention the flag", stderr.String())
	}
}

// TestBinary runs the built binary, because that is the only way to see what
// -ldflags puts into it.
func TestBinary(t *testing.T) {
	stamped := build(t, strings.Join([]string{
		"-X github.com/naghuale/crewflow/internal/buildinfo.Version=1.2.3",
		"-X github.com/naghuale/crewflow/internal/buildinfo.Commit=deadbeef",
		"-X github.com/naghuale/crewflow/internal/buildinfo.Date=2026-09-28T09:00:00Z",
	}, " "))
	plain := build(t, "")

	t.Run("version without ldflags", func(t *testing.T) {
		stdout, stderr, code := execute(t, plain, "version")
		if code != 0 {
			t.Fatalf("crewflow version = %d, want 0 (stderr: %q)", code, stderr)
		}
		want := "crewflow dev (commit unknown, built unknown)\n" +
			"capabilities: " + strings.Join(buildinfo.Capabilities(), ", ") + "\n"
		if stdout != want {
			t.Errorf("crewflow version wrote %q, want %q", stdout, want)
		}
	})

	t.Run("version with ldflags", func(t *testing.T) {
		stdout, stderr, code := execute(t, stamped, "version")
		if code != 0 {
			t.Fatalf("crewflow version = %d, want 0 (stderr: %q)", code, stderr)
		}
		want := "crewflow 1.2.3 (commit deadbeef, built 2026-09-28T09:00:00Z)\n" +
			"capabilities: " + strings.Join(buildinfo.Capabilities(), ", ") + "\n"
		if stdout != want {
			t.Errorf("crewflow version wrote %q, want %q", stdout, want)
		}
	})

	t.Run("unknown subcommand", func(t *testing.T) {
		stdout, stderr, code := execute(t, plain, "bogus")
		if code != exitUsage {
			t.Errorf("crewflow bogus = %d, want %d", code, exitUsage)
		}
		if stdout != "" {
			t.Errorf("crewflow bogus wrote %q to stdout, want nothing", stdout)
		}
		if !strings.Contains(stderr, "Usage:") {
			t.Errorf("crewflow bogus wrote %q to stderr, want the usage", stderr)
		}
	})
}

// build compiles the binary into t.TempDir(), with ldflags if given.
func build(t *testing.T, ldflags string) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "crewflow")
	args := []string{"build", "-o", binary}
	if ldflags != "" {
		args = append(args, "-ldflags", ldflags)
	}
	args = append(args, ".")
	if out, err := exec.Command("go", args...).CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return binary
}

// execute runs the binary and returns its stdout, its stderr and its exit code.
func execute(t *testing.T, binary string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errOut bytes.Buffer
	cmd := exec.Command(binary, args...)
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	default:
		t.Fatalf("run %s: %v", binary, err)
	}
	return out.String(), errOut.String(), code
}
