// Package doctor checks that the machine can run crewflow at all, before a task
// starts: the project file is readable, the programs are installed, the tools
// the project requires answer and, when asked for, the executor answers too.
// Doctor installs nothing, it says what is missing and what to do about it
// (docs/DESIGN.md §7d).
package doctor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/naghuale/crewflow/internal/access"
	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/forge"
	"github.com/naghuale/crewflow/internal/forge/roles"
	"github.com/naghuale/crewflow/internal/secret"
)

// identityCheck is the name a report calls the mode of the executor by: whose powers
// the executor of a run of this project has (docs/DESIGN.md §7i).
const identityCheck = "executor identity"

// specifiedCheck is the name a report calls the list of the settings of the project
// by: the keys of the file that ask for a behaviour the design describes and the
// code has not written, which is why the file did not load (docs/DESIGN.md §5).
const specifiedCheck = "specified settings"

// orchestratorCheck is the name a report calls the mode of the orchestrator by, and it
// is a check of its own: the powers of a review and of a merge are not the powers of a
// run, and a report that showed one line about both would hide the very thing a person
// has to see before approving a task (docs/DESIGN.md §7h, §7i).
const orchestratorCheck = "orchestrator identity"

// separationCheck is the name a report calls the section of the separation of the
// subjects by: the one line of it is whether the accounts of the project are the three
// accounts §7i says they are, and the section under it names each of them (docs/DESIGN.md
// §7i, §7k).
const separationCheck = "authority separation"

// The debts of trust a project carries, as the words of the report and of its JSON:
// every decision of a person that is held on something other than a check of the code.
// They are named and not counted, because a script that reads the report has to be able
// to ask for the exact one it cares about (docs/DESIGN.md §7i, §7k).
const (
	// DebtOwnerOrchestrator is the debt of a project whose orchestrator works under
	// the login of the owner: the acceptance of the owner and the record of a review
	// are one signature, and the gate cannot tell them apart.
	DebtOwnerOrchestrator = "owner-orchestrator-overlap"
	// DebtOwnersReviewers is the debt of a project where the same account is among
	// the accounts of the owner and among the accounts whose record of a review
	// counts: it approves a change and accepts its result.
	DebtOwnersReviewers = "owners-reviewers-overlap"
)

// Status is how one check ended.
type Status string

// The three answers a check can give. Only Fail stops crewflow: a Warn is
// something a person should look at, and a task may still run.
const (
	// OK is a check that passed.
	OK Status = "ok"
	// Warn is a check that passed, but that a person should look at.
	Warn Status = "warn"
	// Fail is a check that did not pass: crewflow cannot work this way.
	Fail Status = "fail"
)

// Check is one line of the report.
type Check struct {
	// Name is what the report calls the check: the program it is about, or the
	// name a gate or a tool has in the project file.
	Name string `json:"name"`
	// Status is how the check ended.
	Status Status `json:"status"`
	// Detail is one line of what was found, without anything secret in it.
	Detail string `json:"detail"`
	// Hint is what to do about it, and is there only when something is wrong.
	Hint string `json:"hint,omitempty"`
}

// Report is every check of one run of doctor, in the order they were made.
type Report struct {
	Checks []Check `json:"checks"`
	// Identity is whose name the executor of a task of this project works under, and
	// it is in every report and not only in the ones about a bot: a person who reads a
	// report has to see whose powers a run has without asking anything (docs/DESIGN.md §7i).
	Identity Identity `json:"identity"`
	// Orchestrator is whose name the review and the merge of this project work under.
	// It is in every report for the same reason and because the record of a review and
	// the acceptance of the owner are two different signatures exactly when this line
	// says they are (docs/DESIGN.md §7h, §7i).
	Orchestrator Identity `json:"orchestrator"`
	// Access is what the executor of the project may read outside the worktree of a
	// task on this machine, and the paths crewflow would not open, because a refusal
	// of the next run is about a permission that was given here (docs/DESIGN.md §7d).
	Access Access `json:"access"`
	// Specified are the settings of the project that ask for a behaviour the design
	// describes and the code has not written. The file of a project that asks for one
	// does not load, and this is the list of what asked: a person reads it in one place
	// instead of taking the keys out of the words of an error (docs.DESIGN §5).
	Specified []config.Asked `json:"specified"`
	// Authority is the separation of the three subjects of the project as this report
	// sees it: the accounts of the owner, of the orchestrator and of the executor, and
	// whether the owner and the orchestrator are one account. It is in every report,
	// because the last place where trust stood where a check could stand is this one,
	// and a person who reads a report has to see whether it stands here or not
	// (docs.DESIGN §7i, §7k).
	Authority Authority `json:"authority"`
	// TrustDebt is every place of this project where a decision of a person is held on
	// trust instead of on a check of the code, named as in [DebtOwnerOrchestrator] and
	// [DebtOwnersReviewers]. It is a list and never a nil one: a project that carries no
	// debt has an empty list, and a script that reads the report must not have to tell
	// those two apart (docs.DESIGN §7i, §7k).
	TrustDebt []string `json:"trust_debt"`
}

// Authority is the separation of the subjects of a project, as a report of it reads
// them off the file and off the host (docs.DESIGN §7i, §7k).
type Authority struct {
	// Owner is the login of the owner of the repository of the project: the account
	// whose decision every record of the project is measured against.
	Owner string `json:"owner"`
	// Owners and Reviewers are the accounts whose records the gate counts — the first
	// as the decision of a person, the second as an approval. They are the accounts of
	// the file with the default of §5 filled in and each of them resolved into the
	// subject the host keeps it under: the name the file writes is what a person reads,
	// and the kind and the number are what the gate compares (docs/DESIGN.md §7h, §7i).
	Owners    []Subject `json:"owners"`
	Reviewers []Subject `json:"reviewers"`
	// Overlap are the accounts in both lists, which in the mode of a shared login is
	// the debt [DebtOwnersReviewers] and in the mode of a separate one is an error the
	// load of the file refuses (OR-008).
	Overlap []Subject `json:"overlap"`
	// Executor and Orchestrator are the accounts a run and a review of this project
	// work as, as the host names them.
	Executor     string `json:"executor"`
	Orchestrator string `json:"orchestrator"`
	// OwnerIsOrchestrator says whether the owner of the repository and the
	// orchestrator are one account: yes in the mode of a shared login, no in the mode
	// of an account of its own.
	OwnerIsOrchestrator bool `json:"owner_is_orchestrator"`
}

// Subject is one account of the project as the report shows the correspondence of it: the
// name the file of the project writes it under, the kind of account the host holds it as,
// and the number the gate counts a record of it by. Both halves are there because either
// alone is not enough: a name changes with a rename and an App may be renamed, and a
// number a person cannot act with (docs/DESIGN.md §7i, §7k).
type Subject struct {
	// Login is the name the file of the project writes the account under.
	Login string `json:"login"`
	// Kind is "User" for a person and "Bot" for an App of the host of its own — the
	// words the host itself writes, so that a report says what the answer of the API
	// said (docs.DESIGN.md §7i).
	Kind string `json:"kind"`
	// ID is the number the host keeps the account under: the number of the account for
	// a person and the number of the App for an App. Zero means the host did not say,
	// and an account of no number counts for nothing (docs.DESIGN.md §7h).
	ID int64 `json:"id"`
}

// String is the account as the lines of the report show it: the name and the kind and the
// number under it, so that a person can see at once which identifier the gate compares
// with what the file of the project names (docs.DESIGN.md §7i, §7k).
func (s Subject) String() string {
	return fmt.Sprintf("%s (%s %d)", s.Login, s.Kind, s.ID)
}

// subjectOf is the account as the report of it is written, and a refusal of the two kinds
// it does not show: an account the host named no kind for and an account the host named
// no number for are both a gap in what was said, and a report that showed one as an
// account would send a person to look for a fault of a file that has none (docs/DESIGN.md
// §7h, §7i).
func subjectOf(subject forge.Subject) Subject {
	return Subject{Login: subject.Login, Kind: string(subject.Kind), ID: subject.ID}
}

// subjectsOf are the accounts of a list as the report shows them, in the order the file of
// the project names them in, and an empty list where there is none: a field a script
// reads has to be there (docs/DESIGN.md §7i, §7k).
func subjectsOf(subjects []forge.Subject) []Subject {
	if len(subjects) == 0 {
		return []Subject{}
	}
	shown := make([]Subject, 0, len(subjects))
	for _, subject := range subjects {
		shown = append(shown, subjectOf(subject))
	}
	return shown
}

// Identity is the mode of one of the two subjects of a project and the one line a
// report shows for it: of a run it is "owner" or "bot" and of the orchestrator it is
// "shared" or "separate" (docs/DESIGN.md §7i).
type Identity struct {
	// Mode is "owner" or "bot" for the executor of a run, and "shared" or "separate"
	// for the orchestrator of the project: the two words the core knows for each of
	// them.
	Mode string `json:"mode"`
	// Description is the one line a person reads: "owner — the login gh naghuale
	// (shared rights)" or "separate — GitHub App crewflow-orchestrator (installation
	// 12346)".
	Description string `json:"description"`
	// Account is the login of the host the subject works as, as the host writes it.
	// It is empty where nobody could be asked, and the accounts of a project are
	// compared with it by the section of the separation (docs.DESIGN §7i, §7k).
	Account string `json:"account"`
}

// Access is the reading policy of a run as this machine works it out: the folders the
// commands of the project named, the places that are closed whatever the project
// asked for, and every path that was refused with the reason why.
type Access struct {
	// Read are the folders the executor of a run may read outside its worktree, each
	// with the ask behind it: the hand it came from and why it was made. A path alone
	// does not say it, and a person deciding about a run has to tell a folder the
	// project needs from a folder one task asked for (docs/DESIGN.md §7d).
	Read []access.Grant `json:"read"`
	// Deny are the places crewflow keeps closed to it, whatever the project asked for.
	Deny []string `json:"deny"`
	// Rejected are the paths that were asked for and were not opened, and why.
	Rejected []access.Problem `json:"rejected"`
}

// OK reports whether nothing failed, which is what decides the exit code.
func (r Report) OK() bool {
	return !slices.ContainsFunc(r.Checks, func(check Check) bool { return check.Status == Fail })
}

// Env is everything Run needs from the machine. It is passed in whole, so that
// a test can hand it a machine of its own and nothing here depends on what
// happens to be installed where the test runs.
type Env struct {
	// LookPath finds a program the way the shell does, in PATH.
	LookPath func(name string) (string, error)
	// Run starts a program in dir with a closed stdin and returns what it wrote
	// and the code it exited with. The extra environment is added to the one of
	// the process, which is how a role of a project talks to a host of its own
	// (GH_HOST and the like) without spelling out the whole environment.
	Run func(ctx context.Context, name string, args []string, dir string, extraEnv []string) (stdout, stderr []byte, exitCode int, err error)
	// ConfigPath is the crewflow.toml to read.
	ConfigPath string
	// Home is the home of the person crewflow runs for: what a leading "~/" in the
	// file of the project stands for, and where the places that hold secrets are. It
	// is given and not read from the process, so that a test of doctor resolves no
	// path against the home of the person who runs the tests.
	Home string
	// Environ is the environment crewflow itself was started with, which is where a
	// person may have put settings of their own for the agent. A report looks in it
	// for those: the files an agent takes its rights from are named by variables of
	// it, and a report that looked in one place only would miss a file in another
	// (docs/DESIGN.md §7d).
	Environ []string
	// TempDir is the empty folder the probe runs the executor in, so that a
	// probe touches nothing of the project.
	TempDir string
	// Probe asks the executor one tiny question. It is off unless it was asked
	// for, because a run of an agent spends its money and its limits.
	Probe bool
	// Secrets is where the key of the app of a project is kept, and HTTP and Now are
	// how a role that has an account of its own reaches the host. A test hands a
	// report a store, a client and a clock of its own, so that no report of crewflow
	// opens the keychain of a person or asks GitHub for a token (docs/DESIGN.md §7i).
	Secrets secret.Store
	HTTP    *http.Client
	Now     func() time.Time
	// Git is the git of the machine, asked with the given environment added to the one of
	// the process: the check of the capability of a route is the git of this machine and
	// not a git of a test (docs/DESIGN.md §7d). Its list and its complaint are two
	// streams, and a check reads the first as the answer and the second as the reason.
	Git func(ctx context.Context, args []string, environment []string) (stdout, stderr string, exitCode int, err error)
	// NetworkAPI is the address the check of the capability of the host asks, when the
	// machine of the report is not the one of the project: a test hands a report its own
	// server, so that no check of crewflow reaches the network of the machine the tests
	// run on (§7d). Empty is the API of the host of the project, which is what a report
	// of a machine asks.
	NetworkAPI string
	// Out is where the report says which step of itself it is making, and where the
	// store of the secrets says what it is waiting for. The report itself is printed
	// where the caller wants it — the answer of a command with `-json` is the report
	// and nothing else — and the steps go elsewhere, because a step is not part of the
	// answer (docs/DESIGN.md §7d, §7i).
	Out io.Writer
	// Executable is the program of this build, which the check of its signature asks
	// the system about: the keychain of macOS ties the access of a program to a secret
	// to the signature of that program, and there is no way to ask about it without
	// naming the file (docs/DESIGN.md §7i).
	Executable string
}

// Run checks the machine and reports what it found. The checks are made in the
// order of what they need: the file, then git, then the roles the file speaks
// of, then everything else the file says the project needs. A check that needs
// the file is skipped when the file is not readable, because a check that could
// not be made is not a check that failed.
//
// Every step of it is said on the way out, before it is made, wherever the report is
// being made: a check of the machine can take a long time — the keychain of macOS may
// stand in front of a window of the system until the owner of the machine answers it —
// and a report that prints nothing until the end says nothing at all about where it is
// while it lasts (docs/DESIGN.md §7d, §7i).
func Run(ctx context.Context, env Env) Report {
	c := &checker{env: env}
	c.step("the file of the project")
	cfg, readable := c.config()
	c.step("git")
	c.git(ctx)
	if !readable {
		return c.report()
	}
	// The two questions about the keychain of macOS and about the signature of this
	// program come before the roles of the project, because the checks of the roles read
	// the key of an App and that read is what makes macOS open a window of the system: a
	// report that has already said whether a window is going to open lets the owner of the
	// machine answer it, and one that says it afterwards has already stood in front of it
	// (docs/DESIGN.md §7i).
	c.step("the keychain of macOS")
	c.keychain(cfg)
	c.step("the signature of this program")
	c.signature(ctx)
	c.step("the roles of the project")
	c.roles(ctx, cfg)
	c.step("the separation of the subjects")
	c.separation(ctx, cfg)
	c.step("the executors")
	c.executors(cfg)
	c.step("the reading policy")
	c.reading(ctx, cfg)
	c.step("the route to the network")
	c.network(cfg)
	if env.Probe {
		c.step("the executor of the project")
		c.probe(ctx, cfg)
	}
	c.step("the gates")
	c.gates(cfg)
	c.step("the requirements")
	c.requirements(ctx, cfg)
	return c.report()
}

// step is the line a person reads before a check of the machine is made: what a report
// is doing right now, said where they are looking, because the alternative is a terminal
// that says nothing for as long as the machine takes (docs/DESIGN.md §7d, §7i).
func (c *checker) step(name string) {
	if c.env.Out == nil {
		return
	}
	fmt.Fprintf(c.env.Out, "crewflow doctor: %s…\n", name)
}

// checker makes the checks of one Run and keeps them in order, together with the
// reading policy of the project, which is not a check but a part of what a run is
// about to be given.
type checker struct {
	env          Env
	checks       []Check
	identity     Identity
	orchestrator Identity
	access       Access
	specified    []config.Asked
	authority    Authority
	trustDebt    []string
}

// add puts a check into the report.
func (c *checker) add(check Check) {
	c.checks = append(c.checks, check)
}

// report is what Run returns. The reading policy is there even when the file of the
// project could not be read, as three empty lists, and so is the list of the
// settings that ask for what is not written: an orchestrator reads them on every
// report and a field that is missing in one of the two is a field to guard against
// in a script.
func (c *checker) report() Report {
	if c.access.Read == nil {
		c.access = Access{Read: []access.Grant{}, Deny: []string{}, Rejected: []access.Problem{}}
	}
	if c.specified == nil {
		c.specified = []config.Asked{}
	}
	if c.trustDebt == nil {
		c.trustDebt = []string{}
	}
	c.authority.Overlap = emptyIfNil(c.authority.Overlap)
	c.authority.Owners = emptyIfNil(c.authority.Owners)
	c.authority.Reviewers = emptyIfNil(c.authority.Reviewers)
	return Report{
		Checks: c.checks, Identity: c.identity, Orchestrator: c.orchestrator,
		Access: c.access, Specified: c.specified, Authority: c.authority, TrustDebt: c.trustDebt,
	}
}

// emptyIfNil is the list a report holds where there is nothing in it: a field a script
// reads has to be there, and an empty list says that the same thing a nil one does
// without making every reader of it check.
func emptyIfNil(list []Subject) []Subject {
	if list == nil {
		return []Subject{}
	}
	return list
}

// config reads the project file, which every check below needs: which executor
// to run, which gates the project has and which tools it requires.
func (c *checker) config() (config.Config, bool) {
	cfg, err := config.Load(c.env.ConfigPath)
	if err != nil {
		c.add(Check{
			Name:   "config",
			Status: Fail,
			Detail: err.Error(),
			Hint:   "fix the file, or point -config at the crewflow.toml of the project",
		})
		c.specifiedFrom(err)
		return config.Config{}, false
	}
	c.add(Check{Name: "config", Status: OK, Detail: c.env.ConfigPath})
	return cfg, true
}

// specifiedFrom is the check for the settings the registry of the file refused: it
// is a check of its own because a person who asked for a sandbox should read which
// keys of the file asked and which task writes them, not only the words of the
// refusal inside the line of the file (docs/DESIGN.md §5).
func (c *checker) specifiedFrom(err error) {
	var refusal *config.NotImplemented
	if !errors.As(err, &refusal) || len(refusal.Settings) == 0 {
		return
	}
	c.specified = refusal.Settings
	c.add(Check{
		Name:   specifiedCheck,
		Status: Fail,
		Detail: detailOfTheSettings(refusal.Settings),
		Hint:   "a setting crewflow does not do yet is refused, not kept: take it out or write what the line says",
	})
}

// detailOfTheSettings is the list of the settings in one line: the key, the value
// and the task that writes it, one after another.
func detailOfTheSettings(settings []config.Asked) string {
	lines := make([]string, 0, len(settings))
	for _, setting := range settings {
		line := fmt.Sprintf("%s = %q", setting.Key, setting.Value)
		if setting.Task > 0 {
			line += fmt.Sprintf(" (crewflow#%d)", setting.Task)
		} else {
			line += " (no issue is open)"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, ", ")
}

// git checks that git is installed: a task happens in a worktree, so without git
// crewflow cannot do anything at all.
func (c *checker) git(ctx context.Context) {
	git, ok := c.lookUp("git", "git", "install git")
	if !ok {
		return
	}
	stdout, stderr, code, err := c.run(ctx, git, "--version")
	if code != 0 || err != nil {
		c.add(Check{
			Name:   "git",
			Status: Fail,
			Detail: "git --version " + exited(code, stdout, stderr),
			Hint:   "install git, or put the git of the project in PATH",
		})
		return
	}
	c.add(Check{Name: "git", Status: OK, Detail: firstLine(stdout, stderr)})
}

// roles asks the three roles of the project what they found on the machine
// (docs/DESIGN.md §7g): each has a program of its own and a login of its own, so
// each is checked where it is, and the adapter of the system is the one that
// knows how to be checked.
//
// The mode of the executor comes with them and is the line every report of a run
// shows: whose powers a run has is the first thing a person reads of it
// (docs/DESIGN.md §7i). The mode of the orchestrator is asked after them, under the
// roles of the orchestrator and not of a run: a report that showed the mode of a run
// and said nothing about the mode of a review would say nothing about the account the
// record of a review is written in (§7h, §7i).
func (c *checker) roles(ctx context.Context, cfg config.Config) {
	set, err := roles.New(cfg, c.rolesEnv())
	if err != nil {
		c.add(c.noRole(err))
		return
	}
	for _, role := range set.Checkers() {
		for _, check := range role.Doctor(ctx) {
			c.add(asLine(check))
		}
	}
	c.executorIdentity(ctx, set)
	c.orchestratorIdentity(ctx, cfg)
}

// orchestratorIdentity is the line of the mode of the orchestrator, which is the one
// thing a report of a machine has to say about the account a review is written in and a
// merge is pushed as (docs/DESIGN.md §7h, §7i).
//
// The mode of the shared login is a warning and not a failure: everything a review needs
// is there, and a project that has not set the second App up is a project crewflow has
// to work for. It is said anyway, because in that mode the acceptance of the owner and
// the record of the review are one signature — and the gate cannot tell them apart,
// which is the one thing §7i is about.
func (c *checker) orchestratorIdentity(ctx context.Context, cfg config.Config) {
	set, err := roles.AsOrchestrator(cfg, c.rolesEnv())
	if err != nil {
		c.add(c.noRole(err))
		return
	}
	if set.Forge == nil {
		return
	}
	// The accounts of the host the orchestrator works as are checked before the line of
	// the mode, the way the accounts of a run are: a report that said "separate" and
	// then found no key of the second app is a report whose first line was a promise
	// and not a fact (§7i).
	for _, check := range forge.AppChecksOf(ctx, set.Forge) {
		c.add(asLine(check))
	}
	identity, err := forge.DescribeOrchestrator(ctx, set.Forge)
	if err != nil {
		c.add(Check{
			Name:   orchestratorCheck,
			Status: Fail,
			Detail: err.Error(),
			Hint: fmt.Sprintf("a review and a merge are written as the account of the host of the project, "+
				"and the file names one: check orchestrator.mode and orchestrator.github_app in %s, and the key of "+
				"that app with `crewflow auth app import <file.pem>`", c.env.ConfigPath),
		})
		return
	}
	c.orchestrator = Identity{Mode: identity.Mode, Description: identity.Description, Account: identity.Account}
	if identity.Mode != forge.ModeSeparate {
		c.add(Check{
			Name:   orchestratorCheck,
			Status: Warn,
			Detail: identity.Description,
			Hint: fmt.Sprintf("the orchestrator and the owner are one login, so the acceptance of the owner, the label "+
				"`approved` and every bypass of a rule are held on the discipline of the one who writes them and the code "+
				"cannot tell: set [orchestrator] mode = %q in %s, install a second app with the rights of §7i and import "+
				"its key with `crewflow auth app import <file.pem>`", forge.ModeSeparate, c.env.ConfigPath),
		})
		return
	}
	c.add(Check{Name: orchestratorCheck, Status: OK, Detail: identity.Description})
}

// executorIdentity is the line of the mode of the executor, which is the one thing a
// report of a machine has to say about the powers of a run (docs/DESIGN.md §7i).
//
// The mode of the owner is a warning and not a failure: everything a run needs is
// there, and a personal project is a project crewflow has to work for. It is said
// anyway, because the powers of the executor of a run in that mode are the powers of
// the login of the person — the one thing the whole of §7i is about.
func (c *checker) executorIdentity(ctx context.Context, set forge.Set) {
	if set.Forge == nil {
		return
	}
	identity, err := forge.DescribeIdentity(ctx, set.Forge)
	if err != nil {
		c.add(Check{
			Name:   identityCheck,
			Status: Fail,
			Detail: err.Error(),
			Hint:   fmt.Sprintf("a run works as the account of the host of the project, and the file says one: fix [identity] in %s", c.env.ConfigPath),
		})
		return
	}
	c.identity = Identity{Mode: identity.Mode, Description: identity.Description, Account: identity.Account}
	if identity.Mode == forge.ModeOwner {
		c.add(Check{
			Name:   identityCheck,
			Status: Warn,
			Detail: identity.Description,
			Hint: fmt.Sprintf("the powers of the executor are the powers of that login; "+
				"the mode of the bot separates them: set [identity] mode = %q in %s and "+
				"import the key of an app with `crewflow auth app import <file.pem>`",
				forge.ModeBot, c.env.ConfigPath),
		})
		return
	}
	c.add(Check{Name: identityCheck, Status: OK, Detail: identity.Description})
}

// separation is the section of a report that names the three subjects of the project
// and says whether the mode of the orchestrator divides them: the owner, the
// orchestrator, the executor, whether the owner and the orchestrator are one account,
// and which accounts are in both lists of the gate (docs.DESIGN §7i, §7k).
//
// The two lists are shown as the correspondence they are: the name the file of the project
// writes each account under, and the kind and the number the gate counts a record of it
// by. Both halves are in the report because a person who is about to trust a decision of a
// gate has to see what it compares, and a login alone cannot say (docs.DESIGN.md §7i, §7k).
//
// It is a section and not another mode because it is five lines of one fact, and the
// fact is the one §7i is about: the report says it once, in the words of the design,
// instead of spread over the two lines of the modes and the two lists of the file.
//
// The check beside it is the verdict: a warning where a decision of a person is held
// on trust (DR-001), a failure where the mode promises a separation the accounts of the
// project do not give (DR-002, DR-003), a failure where an account of the file is one
// crewflow cannot number, and nothing to say where the three subjects are three
// accounts.
//
// The roles it is worked out with are the ones that show the state of the project: what
// the section compares is subjects, and a subject is a number the host keeps the account
// under and a number the file of the project names, so nothing in it has to read a key of an
// App. The line of the mode of the orchestrator above is the check of that key, and it asks
// for it on purpose (docs.DESIGN.md §7i, §7k).
func (c *checker) separation(ctx context.Context, cfg config.Config) {
	set, err := roles.ToShow(cfg, c.rolesEnv())
	if err != nil || set.Forge == nil {
		// Nothing to compare: the check of the mode has already said what is wrong, and a
		// section that guessed would put an account into a report that nobody checked.
		return
	}
	reviewers, err := roles.ReviewersOf(ctx, cfg, set)
	if err != nil {
		c.add(c.correspondenceCheck(err))
		return
	}
	owners, err := roles.OwnersOf(ctx, cfg, set)
	if err != nil {
		c.add(c.correspondenceCheck(err))
		return
	}
	authority := Authority{
		Owner:        ownerOf(cfg.Project.Repo),
		Owners:       subjectsOf(owners),
		Reviewers:    subjectsOf(reviewers),
		Overlap:      subjectsOf(overlapOf(owners, reviewers)),
		Executor:     c.identity.Account,
		Orchestrator: c.orchestrator.Account,
	}
	// The owner and the orchestrator are one account where the mode says they cannot
	// be told apart — the shared login, where the orchestrator is a person and nothing
	// in the file can tell his record from the owner's — and where the accounts say so:
	// an App of an orchestrator among the owners writes the acceptance of the owner
	// with its own pen (docs.DESIGN §7i, §7k).
	authority.OwnerIsOrchestrator = sharedMode(cfg) || isAmongAny(owners, signingAsOf(ctx, set))
	c.authority = authority
	c.trustDebt = debtsOf(cfg, authority)
	if refused := separationRefusal(cfg, owners, reviewers); refused != nil {
		c.add(Check{
			Name:   separationCheck,
			Status: Fail,
			Detail: refused.Error(),
			Hint: fmt.Sprintf("the mode of %q says the orchestrator works under an account of its own, and the "+
				"accounts of %s do not: fix merge.owners and merge.reviewers there, or put mode = %q back",
				config.ModeSeparate, c.env.ConfigPath, config.ModeShared),
		})
		return
	}
	if len(c.trustDebt) > 0 {
		c.add(Check{
			Name:   separationCheck,
			Status: Warn,
			Detail: strings.Join(c.trustDebt, " · ") + " — a decision of a person is held on trust here",
			Hint: fmt.Sprintf("the owner and the orchestrator are one account while [orchestrator] mode = %q in %s; "+
				"a second app with the rights of §7i and `crewflow auth app import <file.pem> -as orchestrator` "+
				"divide them, and then the debt is empty",
				config.ModeShared, c.env.ConfigPath),
		})
		return
	}
	c.add(Check{Name: separationCheck, Status: OK, Detail: "the owner, the orchestrator and the executor are three accounts"})
}

// separationRefusal is the rule of §7i asked twice, of two answers of the host.
//
// The first is the rule as [config.Separation] writes it, against the names the file of the
// project holds: a file that puts one login in both lists is a mistake in the file, and the
// load of it refuses it already. The second is the same rule against the accounts themselves,
// and it is here because the account of an App of the orchestrator is in no file and the roles
// this section is worked out with hold no key to be asked about it: the mode promises a
// separation, the App of the orchestrator is named among the owners, and the gate would count
// its record of a review as a decision of the owner (docs.DESIGN.md §7i, §7k).
//
// The overlap of the two lists is a debt of trust in the mode of a shared login and a refusal
// in the mode of a separate one — and the mode of a shared login has no second App to divide
// the subjects with, so the refusal is asked about where the mode promises (§7i).
func separationRefusal(cfg config.Config, owners, reviewers []forge.Subject) error {
	if refused := config.Separation(cfg.Orchestrator.Mode, loginsOf(owners), loginsOf(reviewers), cfg.Project.Repo); refused != nil {
		return refused
	}
	if sharedMode(cfg) {
		return nil
	}
	for _, owner := range owners {
		if isAmongAny(reviewers, owner) {
			return fmt.Errorf("separate mode requires role separation: %s is among the accounts of the reviewers "+
				"and among the accounts of the owners, and the gate would count the record of the orchestrator as a "+
				"decision of the owner; fix: remove overlapping identities or switch to shared mode", owner.Named())
		}
	}
	return nil
}

// correspondenceCheck is the refusal of an account the file of the project names and the
// host does not hold the way the file means it: a login of nobody, an account of a kind
// crewflow does not read, an App of a project this one is not. Each of them is a mistake
// in the file and not a gap in what the host said, so the check says what to do about it
// instead of leaving a section with a hole in it (docs/DESIGN.md §7h, §7i, §7k).
func (c *checker) correspondenceCheck(err error) Check {
	return Check{
		Name:   separationCheck,
		Status: Fail,
		Detail: err.Error(),
		Hint: fmt.Sprintf("the accounts of merge.owners and merge.reviewers are counted by the number the host "+
			"keeps them under, so a name here is a name the host must know: fix the two lists in %s",
			c.env.ConfigPath),
	}
}

// signingAsOf is the subject the orchestrator of this project writes as, and nothing where
// the host could not be asked: the check of the mode has already said what is wrong, and a
// section that guessed would name an account nobody checked (docs/DESIGN.md §7i, §7k).
func signingAsOf(ctx context.Context, set forge.Set) forge.Subject {
	signed, err := forge.SigningAs(ctx, set.Forge)
	if err != nil {
		return forge.Subject{}
	}
	return signed
}

// debtsOf are the debts of trust a project of these accounts carries, in the order a
// person reads them: the owner and the orchestrator are one account, and then the same
// account in both lists of the gate. In the mode of an account of its own and with the
// accounts the file names, both are impossible — and a project that got here with one of
// them is a project whose file the load refused, which is what [Check.Status] Fail of
// the section above says (docs.DESIGN §7i, §7k).
func debtsOf(cfg config.Config, authority Authority) []string {
	var debts []string
	if sharedMode(cfg) {
		debts = append(debts, DebtOwnerOrchestrator)
	}
	if len(authority.Overlap) > 0 {
		debts = append(debts, DebtOwnersReviewers)
	}
	return debts
}

// sharedMode is whether the file of the project leaves the orchestrator under the login
// of the person, which is what a project gets without saying anything about a second app.
func sharedMode(cfg config.Config) bool {
	return cfg.Orchestrator.Mode != config.ModeSeparate
}

// ownerOf is the login of the owner of a repository, and the empty string for a string
// that is not one.
func ownerOf(repository string) string {
	owner, _, of := strings.Cut(repository, "/")
	if !of {
		return ""
	}
	return owner
}

// overlapOf are the accounts in both lists of the gate, each of them once, in the order
// of the list of the owners: an account that both approves a change and accepts its
// result is one subject doing the work of two.
func overlapOf(owners, reviewers []forge.Subject) []forge.Subject {
	var overlap []forge.Subject
	for _, owner := range owners {
		if isAmongAny(reviewers, owner) {
			overlap = append(overlap, owner)
		}
	}
	return overlap
}

// isAmongAny is whether the account is in the list, by the kind of the account and the
// number the host keeps it under: the two lists of the gate are compared as accounts and
// not as names, which is the whole of the change this section of a report shows (F-081,
// docs/DESIGN.md §7h, §7i).
func isAmongAny(accounts []forge.Subject, one forge.Subject) bool {
	return slices.ContainsFunc(accounts, func(account forge.Subject) bool {
		return one.Same(account)
	})
}

// loginsOfSubjects are the names of the accounts as they were read off the host, for a
// report about the separation of §7i: the rule is checked against the file as it is
// written, and the subject of every account behind each name is in the section above
// (docs.DESIGN.md §7i, §7k).
func loginsOfSubjects(accounts []Subject) []string {
	names := make([]string, 0, len(accounts))
	for _, account := range accounts {
		names = append(names, account.Login)
	}
	return names
}

// loginsOf are the names the host writes the accounts under, as a report about the file
// reads them: the separation of §7i is checked against the file as it is written, and the
// subject of every account behind it is in the section above (docs.DESIGN.md §7i, §7k).
func loginsOf(accounts []forge.Subject) []string {
	names := make([]string, 0, len(accounts))
	for _, account := range accounts {
		names = append(names, account.Login)
	}
	return names
}

// rolesEnv is the environment of the machine as a role needs it: the same
// programs, the same way of starting them, the file a hint names, and the store of
// secrets where the key of an app of a project is kept (docs/DESIGN.md §7i).
func (c *checker) rolesEnv() forge.Env {
	return forge.Env{
		LookPath:   c.env.LookPath,
		Run:        c.env.Run,
		ConfigPath: c.env.ConfigPath,
		Secrets:    c.env.Secrets,
		HTTP:       c.env.HTTP,
		Now:        c.env.Now,
	}
}

// noRole is the check for a role of a kind crewflow has no adapter for. It is a
// failed check of the setting that asked for it: a person is to change that
// setting, and not to install anything (docs/DESIGN.md §7g).
func (c *checker) noRole(err error) Check {
	var notImplemented *forge.ErrNotImplemented
	if !errors.As(err, &notImplemented) {
		return Check{
			Name:   "config",
			Status: Fail,
			Detail: err.Error(),
			Hint:   fmt.Sprintf("the keys of the roles say different things, fix them in %s", c.env.ConfigPath),
		}
	}
	role, _, _ := strings.Cut(notImplemented.Key, ".")
	return Check{
		Name:   role,
		Status: Fail,
		Detail: err.Error(),
		Hint: fmt.Sprintf("crewflow has no %s adapter yet, or change %s in %s",
			notImplemented.Kind, notImplemented.Key, c.env.ConfigPath),
	}
}

// asLine is a line of a role as a line of the report: the same words and the
// same detail, and a failure of a role is a failure like any other.
func asLine(check forge.Check) Check {
	return Check{
		Name:   check.Name,
		Status: Status(check.Status),
		Detail: check.Detail,
		Hint:   check.Hint,
	}
}

// executors checks that the executor is installed: crewflow knows no agent of its
// own, so a project whose file does not name one has nothing to run a task with
// (§7b). A list of executors to try in turn is a promise of the same section that
// crewflow does not keep yet (#51), and the load refuses such a file before this
// check ever asks.
func (c *checker) executors(cfg config.Config) {
	c.executor("executor", "executor.command", cfg.Executor.ExecutorSpec)
}

// executor checks one executor, under the name a report calls it and the key a
// person fixes in the file.
func (c *checker) executor(name, key string, spec config.ExecutorSpec) {
	program, _ := commandProgram(spec.Command)
	path, ok := c.lookUp(name, program, c.changeHint(program, key))
	if !ok {
		return
	}
	c.add(Check{Name: name, Status: OK, Detail: path})
}

// gates checks that the program of every gate is installed, and stops at the
// shell: what a "sh -c" script calls is the job of [requirements] to say, not
// something a gate may hide from the report.
func (c *checker) gates(cfg config.Config) {
	for i, gate := range cfg.Gates {
		program, viaShell := commandProgram(gate.Run)
		name := "gate " + gate.Name
		path, ok := c.lookUp(name, program, c.changeHint(program, fmt.Sprintf("gates[%d].run", i)))
		if !ok {
			continue
		}
		detail := path
		if viaShell {
			detail = path + ", the script of the gate is not run here"
		}
		c.add(Check{Name: name, Status: OK, Detail: detail})
	}
}

// requirements checks every tool of [requirements]: that it is installed, that
// its check command passes and, when the project wrote a min, that the version
// it printed is not older (docs/DESIGN.md §7d).
func (c *checker) requirements(ctx context.Context, cfg config.Config) {
	for i, tool := range cfg.Requirements.Tools {
		c.requirement(ctx, i, tool)
	}
}

// requirement checks one tool, under the name a report calls it and the key a
// person fixes in the file.
func (c *checker) requirement(ctx context.Context, i int, tool config.Tool) {
	name := "tool " + tool.Name
	key := fmt.Sprintf("requirements.tools[%d].check", i)
	if len(tool.Check) == 0 {
		c.add(Check{
			Name:   name,
			Status: Fail,
			Detail: "no check command in the file",
			Hint:   fmt.Sprintf("add check to requirements.tools[%d] in %s", i, c.env.ConfigPath),
		})
		return
	}
	path, ok := c.lookUp(name, tool.Check[0], c.changeHint(tool.Check[0], key))
	if !ok {
		return
	}
	stdout, stderr, code, err := c.env.Run(ctx, path, tool.Check[1:], "", nil)
	switch {
	case code != 0 || err != nil:
		c.add(Check{
			Name:   name,
			Status: Fail,
			Detail: "the check " + exited(code, stdout, stderr),
			Hint:   fmt.Sprintf("make the check work by hand: %s", strings.Join(tool.Check, " ")),
		})
	case tool.Min != "":
		c.add(c.minimum(name, tool, stdout, stderr))
	default:
		// A check may say nothing at all and still pass: "test -e <file>"
		// is one of those.
		detail := firstLine(stdout, stderr)
		if detail == "" {
			detail = "the check passed and said nothing"
		}
		c.add(Check{Name: name, Status: OK, Detail: detail})
	}
}

// minimum compares the version a check printed with the min of the tool. A check
// that prints no version at all is a warning and not a failure: crewflow cannot
// see that the project is served, but a person is told instead of being left
// with a check that passed for no reason.
func (c *checker) minimum(name string, tool config.Tool, stdout, stderr []byte) Check {
	found := firstVersion(stdout)
	if found == "" {
		found = firstVersion(stderr)
	}
	if found == "" {
		return Check{
			Name:   name,
			Status: Warn,
			Detail: fmt.Sprintf("no version in the output, so the min %s was not compared", tool.Min),
			Hint:   c.minHint(tool, "let the check print the version"),
		}
	}
	enough, err := versionAtLeast(found, tool.Min)
	switch {
	case err != nil:
		return Check{
			Name:   name,
			Status: Warn,
			Detail: err.Error(),
			Hint:   c.minHint(tool, "write the min as numbers, for example 1.27"),
		}
	case !enough:
		return Check{
			Name:   name,
			Status: Fail,
			Detail: fmt.Sprintf("needs %s, found %s", tool.Min, found),
			Hint:   c.minHint(tool, "put a newer one in PATH, or lower the min"),
		}
	default:
		return Check{Name: name, Status: OK, Detail: firstLine(stdout, stderr)}
	}
}

// minHint is what to do about the min of a tool, whatever is wrong with it: what
// a person may do, and then the min, the tool and the file to do it in.
func (c *checker) minHint(tool config.Tool, what string) string {
	return fmt.Sprintf("%s, or fix the min %s of the tool %q in %s", what, tool.Min, tool.Name, c.env.ConfigPath)
}

// lookUp asks PATH for a program. When it is not there, the failed check is
// added and false is returned, so that a caller goes on with its next check.
func (c *checker) lookUp(name, program, hint string) (string, bool) {
	path, err := c.env.LookPath(program)
	if err != nil {
		c.add(Check{
			Name:   name,
			Status: Fail,
			Detail: fmt.Sprintf("%s is not in PATH", program),
			Hint:   hint,
		})
		return "", false
	}
	return path, true
}

// changeHint says how a program that is missing may be dealt with: installed,
// or the file changed so that crewflow looks for another one.
func (c *checker) changeHint(program, key string) string {
	return fmt.Sprintf("install %s, or change %s in %s", program, key, c.env.ConfigPath)
}

// run starts a command in the folder crewflow was called in: the checks of the
// machine itself do not depend on the project, only its file does. They also
// need nothing of the environment of the project: that is what a role is for.
func (c *checker) run(ctx context.Context, path string, args ...string) (stdout, stderr []byte, exitCode int, err error) {
	return c.env.Run(ctx, path, args, "", nil)
}

// exited is how a report says that a command did not do its job: the code it
// exited with and, if it said anything on the way out, what it said.
func exited(code int, outputs ...[]byte) string {
	detail := fmt.Sprintf("exited with %d", code)
	if line := firstLine(outputs...); line != "" {
		return detail + ": " + line
	}
	return detail
}

// shells run a script of the project instead of a program of the project, so a
// command of the shape ["sh", "-c", "…"] is checked by the shell alone.
var shells = []string{"sh", "bash", "zsh"}

// commandProgram is the program a command line runs, and whether that program
// is a shell that will be handed a script. What such a script calls is said by
// [requirements], not by the command that runs it.
func commandProgram(command []string) (program string, viaShell bool) {
	if len(command) == 0 {
		return "", false
	}
	program = command[0]
	viaShell = len(command) > 1 && command[1] == "-c" && slices.Contains(shells, filepath.Base(program))
	return program, viaShell
}

// firstLine is the first line that says something, of the outputs in order. A
// command that printed nothing has no line, and the report says that instead of
// showing a blank.
func firstLine(outputs ...[]byte) string {
	for _, output := range outputs {
		for line := range strings.Lines(string(output)) {
			if line = strings.TrimSpace(line); line != "" {
				return line
			}
		}
	}
	return ""
}
