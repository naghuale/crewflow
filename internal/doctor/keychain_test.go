package doctor

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// said is what a report said while it was being made: a machine is asked in a goroutine
// of the test and its steps are read from the goroutine of the test, so the words of one
// are behind a lock.
type said struct {
	mu    sync.Mutex
	lines []string
}

func (s *said) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for line := range strings.Lines(string(p)) {
		if line = strings.TrimSpace(line); line != "" {
			s.lines = append(s.lines, line)
		}
	}
	return len(p), nil
}

func (s *said) said() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.lines, "\n")
}

// TestTheStepsOfAReportAreSaidWhileItIsBeingMade: `crewflow doctor` stood in front of
// the keychain of macOS for five minutes and printed nothing, and a report that is
// printed at the end cannot say where a machine is while it is being made. Every step is
// said before it is made, and what has been said is what a person is looking at while
// the machine takes its time (docs/DESIGN.md §7d, §7i).
func TestTheStepsOfAReportAreSaidWhileItIsBeingMade(t *testing.T) {
	m := newMachine().has("git", "gh", "agent").
		prints("git --version", "git version 2.47.1\n").
		prints("gh auth status", "github.com\n  - Logged in to github.com account octocat (keyring)\n")
	// The machine takes its time on one step, as the keychain of macOS does while the
	// owner of the machine has not answered the window of the system. Every call of gh
	// waits for the same answer, so the step is signalled once and not on each of them.
	started := make(chan struct{})
	release := make(chan struct{})
	signalled := sync.OnceFunc(func() { close(started) })
	m.run = m.answering("gh", func(context.Context) ([]byte, []byte, int, error) {
		signalled()
		<-release
		return []byte("gh version 2.62.0\n"), nil, 0, nil
	})
	env := m.env(t, writeConfig(t, baseConfig))
	steps := &said{}
	env.Out = steps

	done := make(chan Report, 1)
	go func() { done <- Run(t.Context(), env) }()
	<-started
	// The report is in the middle of the step of the roles of the project, and the steps
	// before it are said: a person reading the terminal knows where the report is.
	for _, want := range []string{"the file of the project", "git", "the roles of the project"} {
		if !strings.Contains(steps.said(), want) {
			t.Errorf("the report said %q, want it to have said %q before the step", steps.said(), want)
		}
	}
	close(release)
	report := <-done

	if check := checkOf(t, report, "config"); check.Status == Fail {
		t.Fatalf("check \"config\" = %q (%s), want the report to be whole", check.Status, check.Detail)
	}
	// Every step of the report is said, and the last of them is said before the report
	// is returned: a report that says the step it is in and then stops is a report that
	// is not being made any more.
	for _, want := range []string{"the executors", "the reading policy", "the gates", "the requirements"} {
		if !strings.Contains(steps.said(), want) {
			t.Errorf("the report said %q, want it to have said %q", steps.said(), want)
		}
	}
}

// TestAReportThatSaysItsStepsOnAReportOfNothing: a report with nowhere to say its steps
// is the report of a library and of a test: the steps are said wherever the caller of the
// report wants them, and a caller who named no place is told nothing about them rather
// than being handed a line of a terminal nobody is watching (docs/DESIGN.md §7d).
func TestAReportWithNowhereToSayItsStepsIsWholeAllTheSame(t *testing.T) {
	m := newMachine().has("git", "gh", "agent").
		prints("git --version", "git version 2.47.1\n").
		prints("gh --version", "gh version 2.62.0\n").
		prints("gh auth status", "github.com\n  - Logged in to github.com account octocat (keyring)\n")
	env := m.env(t, writeConfig(t, baseConfig))
	env.Out = nil

	report := Run(t.Context(), env)

	if !report.OK() {
		t.Errorf("report.OK() = false, want true: the checks are the checks whatever the steps were")
	}
}

// TestTheReportSaysThatTheKeychainOfThisMachineDoesNotTrustThisBuild: the access of a
// program to a secret of the keychain of macOS belongs to its signature, and a build of
// crewflow is a program the keychain has never seen. That can be known without a window:
// the keychain answers the question when it is told not to open one, and a report that
// knows says so instead of standing in front of the window (docs/DESIGN.md §7i).
func TestTheReportSaysThatTheKeychainOfThisMachineDoesNotTrustThisBuild(t *testing.T) {
	m := machineWithAnApp(t)
	m.holdsTheKey = true
	env := m.env(t, writeConfig(t, m.botProject()))
	env.Secrets = &untrusted{storeOfTheTest{key: keyOfTheTest(t)}}

	report := Run(t.Context(), env)

	keychain := checkOf(t, report, keychainCheck)
	if keychain.Status != Warn {
		t.Fatalf("check %q = %q (%s), want %q: macOS will ask the owner in a window", keychainCheck, keychain.Status, keychain.Detail, Warn)
	}
	for _, want := range []string{"window", "Always Allow", "SIGN_IDENTITY"} {
		if !strings.Contains(keychain.Detail+keychain.Hint, want) {
			t.Errorf("check %q = %q / %q, want it to hold %q", keychainCheck, keychain.Detail, keychain.Hint, want)
		}
	}
	if !report.OK() {
		t.Error("report.OK() = false, want true: nothing is missing here, a person has to do something about it")
	}
}

// TestTheReportSaysNothingAboutAKeychainWhereThereIsNothingToAsk: a project whose
// executor works as the login of the person keeps no secret of an App in the keychain,
// and a store with nothing in it has no window of macOS to open. A report that showed a
// warning about a window of a machine that has no secret to open would be crying wolf
// (docs/DESIGN.md §7i).
func TestTheReportSaysNothingAboutAKeychainWhereThereIsNothingToAsk(t *testing.T) {
	m := newMachine().has("git", "gh", "agent").
		prints("git --version", "git version 2.47.1\n").
		prints("gh --version", "gh version 2.62.0\n").
		prints("gh auth status", "github.com\n  - Logged in to github.com account octocat (keyring)\n")
	env := m.env(t, writeConfig(t, baseConfig))

	report := Run(t.Context(), env)

	if check, is := checkByName(report, keychainCheck); is {
		t.Errorf("the report holds the check %q = %q, want no check at all in the mode of the owner", keychainCheck, check.Detail)
	}
	// The same for a project in the mode of the bot whose key was never imported: the
	// check of the key itself is in the checks of the role of the host, and a check of
	// the access of the program to a store that holds nothing is a question about
	// nothing (§7i).
	bot := machineWithAnApp(t)
	env = bot.env(t, writeConfig(t, bot.botProject()))
	if check, is := checkByName(Run(t.Context(), env), keychainCheck); is {
		t.Errorf("the report holds the check %q = %q, want no check of an empty store", keychainCheck, check.Detail)
	}
}

// TestTheReportSaysThatThisBuildOfTheProgramIsSignedForThisBuildAlone: a program of a
// build of Go is signed by the linker for that build alone, and the keychain of macOS
// forgets the answer of the owner of the machine as soon as the program is another one.
// A report says that before a task starts, because the window that opens again after
// every build is the one that stops every run in the mode of the bot (docs/DESIGN.md §7i).
func TestTheReportSaysThatThisBuildOfTheProgramIsSignedForThisBuildAlone(t *testing.T) {
	const binary = "/usr/local/bin/crewflow"
	m := newMachine().has("git", "gh", "agent", "codesign").
		prints("git --version", "git version 2.47.1\n").
		prints("gh --version", "gh version 2.62.0\n").
		prints("gh auth status", "github.com\n  - Logged in to github.com account octocat (keyring)\n").
		// What the system says about a program of a build of Go: the linker signed it
		// for that build alone, which is the whole of why the owner of a machine is
		// asked again after every build of it.
		prints("codesign -d --verbose=1 "+binary,
			"Executable="+binary+"\nIdentifier=a.out\nFormat=Mach-O thin (arm64)\n"+
				"CodeDirectory v=20400 size=109086 flags=0x20002(adhoc,linker-signed) hashes=3406+0 location=embedded\n"+
				"Signature=adhoc\n")
	env := m.env(t, writeConfig(t, baseConfig))
	env.Executable = binary

	report := Run(t.Context(), env)

	signature := checkOf(t, report, signedCheck)
	if signature.Status != Warn {
		t.Fatalf("check %q = %q (%s), want %q: the access the owner gave is gone after the next build",
			signedCheck, signature.Status, signature.Detail, Warn)
	}
	for _, want := range []string{"this build alone", "SIGN_IDENTITY"} {
		if !strings.Contains(signature.Detail+signature.Hint, want) {
			t.Errorf("check %q = %q / %q, want it to hold %q", signedCheck, signature.Detail, signature.Hint, want)
		}
	}
	if !strings.Contains(signature.Detail, "adhoc") {
		t.Errorf("check %q detail = %q, want it to show what the system said about the program", signedCheck, signature.Detail)
	}
}

// TestTheReportSaysNothingWhenTheSignatureOfThisBuildCannotBeAsked: a machine without
// the program that knows about signatures, and a program crewflow cannot name, are both
// questions a report could not answer. A report that failed a machine for what it does
// not know is a report nobody reads (docs/DESIGN.md §7i).
func TestTheReportSaysNothingWhenTheSignatureOfThisBuildCannotBeAsked(t *testing.T) {
	m := newMachine().has("git", "gh", "agent").
		prints("git --version", "git version 2.47.1\n").
		prints("gh --version", "gh version 2.62.0\n").
		prints("gh auth status", "github.com\n  - Logged in to github.com account octocat (keyring)\n")
	env := m.env(t, writeConfig(t, baseConfig))
	env.Executable = "/usr/local/bin/crewflow"

	report := Run(t.Context(), env)

	if check, is := checkByName(report, signedCheck); is {
		t.Errorf("the report holds the check %q = %q, want no check on a machine without codesign",
			signedCheck, check.Detail)
	}
}

// untrusted is the keychain of macOS of a machine that holds the key of the app and does
// not trust this build of the program to read it. The question can be asked without a
// window of the system, and the answer is that a window is coming (docs/DESIGN.md §7i).
type untrusted struct {
	storeOfTheTest
}

func (*untrusted) Allowed(string) (bool, error) { return false, nil }

// TestTheReportSaysNothingAboutTheKeychainOfAMachineThatTrustsThisBuild: a keychain of
// macOS that lets this build of the program in opens no window, and a report that warned
// about a window that will not open is a report that cries wolf on a machine that is set
// up right (docs/DESIGN.md §7i).
func TestTheReportSaysNothingToWarnAboutOnAMachineThatTrustsThisBuild(t *testing.T) {
	m := machineWithAnApp(t)
	m.holdsTheKey = true
	config := writeConfig(t, m.botProject())

	report := Run(t.Context(), m.env(t, config))

	keychain := checkOf(t, report, keychainCheck)
	if keychain.Status != OK {
		t.Errorf("check %q = %q (%s), want %q: no window of the system is going to open",
			keychainCheck, keychain.Status, keychain.Detail, OK)
	}
	if !strings.Contains(keychain.Detail, "without a window") {
		t.Errorf("check %q detail = %q, want it to say that no window is opened", keychainCheck, keychain.Detail)
	}
}
