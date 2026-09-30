package doctor

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/secret"
)

// The names a report calls the two checks of this machine by. Both are about the same
// fact seen from two sides — the keychain of macOS ties the access of a program to a
// secret of it to the signature of that program — and both are asked before anything
// opens a window of the system on its own (docs/DESIGN.md §7i).
const (
	keychainCheck = "keychain access"
	signedCheck   = "binary signature"
)

// codesign is the program of the system that says how a program is signed, and it is
// the only one that can: the keychain decides about the access of a program from the
// signature of it, and nothing crewflow holds in memory is that signature.
const codesign = "codesign"

// keychain is the check that this build of crewflow may read the secrets it keeps on
// this machine without the owner of the machine answering a window of the system. The
// keychain of macOS can be asked that question and answers it without opening one, which
// is what this check is for: a report that could have asked it and stood in front of a
// window instead is a report that hangs, and one that did hang is what the pilot saw
// (docs/DESIGN.md §7i).
//
// A project whose executor works as the login of the person keeps no secret of an App
// in the keychain, and a store that cannot be asked the question — the store of a test,
// a machine that keeps its secrets somewhere else — has no window of macOS to open. Both
// are left without a check: a check that could not be made is not a check that failed.
func (c *checker) keychain(cfg config.Config) {
	if cfg.Identity.Mode != forge.ModeBot {
		return
	}
	trust, ok := c.env.Secrets.(secret.Trust)
	if !ok {
		return
	}
	allowed, err := trust.Allowed(secret.Service)
	switch {
	case errors.Is(err, secret.ErrNotFound):
		// The store holds nothing of crewflow, and the check of the key itself is in
		// the checks of the role of the host (§7i).
		return
	case err != nil:
		c.add(Check{Name: keychainCheck, Status: Warn, Detail: err.Error(), Hint: ""})
	case !allowed:
		c.add(Check{
			Name:   keychainCheck,
			Status: Warn,
			Detail: "macOS will ask the owner of this machine to allow this program in a window of the system before it reads a secret of it",
			Hint: "answer \"Always Allow\" in that window, and sign the program with a certificate of your own " +
				"(`make install SIGN_IDENTITY=\"<name>\"`): the answer is given to a signature, and a new build of " +
				"the program is a program macOS has not seen",
		})
	default:
		c.add(Check{
			Name:   keychainCheck,
			Status: OK,
			Detail: "this program may read the secrets of crewflow on this machine without a window of macOS",
		})
	}
}

// signature is the check that this program is signed with a certificate of the owner of
// it, because that is what the keychain of macOS remembers: the access of a program to a
// secret of the keychain belongs to a signature and not to a path, and a signature of a
// build that exists for one build is forgotten by the next one (docs/DESIGN.md §7i).
//
// A program of a build of Go is signed for that build alone — the linker signs it, and
// the mark of it in the answer of codesign is "adhoc" — which is why every rebuild asks
// the owner of the machine the same question again. The program of the system is asked
// and not the signature read out of the program by crewflow itself: a report says what
// the machine that has the answer says it.
func (c *checker) signature(ctx context.Context) {
	if c.env.Executable == "" {
		return
	}
	path, err := c.env.LookPath(codesign)
	if err != nil {
		// A machine without the program that knows about signatures cannot be told
		// anything about one, and a report that fails a machine for a question it
		// could not ask is a report that cries wolf.
		return
	}
	stdout, stderr, code, err := c.env.Run(ctx, path,
		[]string{"-d", "--verbose=1", c.env.Executable}, "", nil)
	// What codesign says about the program is read whole and not line by line: the mark
	// of a program that is signed for one build only is somewhere in its answer, and a
	// machine where the program writes to stdout says it on a later line than the one a
	// report shows.
	answer := string(stderr) + string(stdout)
	switch {
	case err != nil || code != 0:
		c.signed(Warn, "this program is not signed at all", markedAs(answer))
	case strings.Contains(answer, "adhoc"):
		c.signed(Warn, "this program is signed for this build alone, and the next build is a program macOS has not seen", markedAs(answer))
	default:
		c.add(Check{Name: signedCheck, Status: OK, Detail: "this program is signed with a certificate of its own"})
	}
}

// markedAs is the line of what codesign said that names the signature of the program:
// "Signature=adhoc" on a program of a build of Go, the flags of the code directory of it
// on a program signed with a certificate, and the refusal of the system itself on a
// program that is not signed at all. A report shows one line and this is the line a
// person is looking for in it.
func markedAs(answer string) string {
	for line := range strings.Lines(answer) {
		if strings.Contains(line, "Signature=") || strings.Contains(line, "flags=") {
			return strings.TrimSpace(line)
		}
	}
	return strings.TrimSpace(firstLine([]byte(answer)))
}

// signed is a check of the signature of this program that a person has to do something
// about, and the one thing to do about it is the same however the program came to be
// signed: give it a certificate of your own, or the owner of the machine answers the
// same window of the system after every build of it (docs/DESIGN.md §7i).
func (c *checker) signed(status Status, detail, said string) {
	if said != "" {
		detail = fmt.Sprintf("%s: %s", detail, said)
	}
	c.add(Check{
		Name:   signedCheck,
		Status: status,
		Detail: detail,
		Hint: "sign the program with a certificate of your own, so that the access the owner of this machine gave " +
			"it to the keychain survives the next build: `make install SIGN_IDENTITY=\"<name>\"`, and the README " +
			"of the project says how to make one",
	})
}
