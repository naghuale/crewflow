package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/doctor"
)

// defaultConfigPath is where a project keeps its crewflow.toml, so that
// "crewflow doctor" needs no flags at all inside a project.
const defaultConfigPath = "./crewflow.toml"

// systemEnv builds the environment doctor checks. It is a variable so that a
// test of the command checks a machine of its own instead of the machine it
// runs on.
var systemEnv = doctor.System

// marks are how a check looks to a person: in order, or not.
var marks = map[doctor.Status]string{
	doctor.OK:   "✓",
	doctor.Warn: "!",
	doctor.Fail: "✗",
}

// runDoctor checks the machine before a task starts, so that what is missing is
// found out here and not in the middle of a task.
func runDoctor(args []string, stdout, stderr io.Writer) int {
	// `crewflow doctor network` is a check of its own: it asks the machine whether the
	// route of the project works, one capability at a time, and a report of readiness
	// must not stand in front of a proxy of a project every time it runs (§7d).
	if len(args) > 0 && args[0] == "network" {
		return runDoctorNetwork(args[1:], stdout, stderr)
	}
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { usage(stderr) }
	configPath := flags.String("config", defaultConfigPath, "path to crewflow.toml")
	probe := flags.Bool("probe", false, "give the executor one tiny task, to see that it answers")
	asJSON := flags.Bool("json", false, "print the report as JSON, for the orchestrator")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "crewflow doctor: unexpected argument %q\n\n", flags.Arg(0))
		usage(stderr)
		return exitUsage
	}

	folder, removeFolder, err := probeFolder(*probe)
	if err != nil {
		fmt.Fprintf(stderr, "crewflow doctor: %v\n", err)
		return exitFailure
	}
	defer removeFolder()

	// The report is the answer of the command and is printed to stdout — it is the whole
	// of what `-json` gives an orchestrator — and everything crewflow says while the
	// report is being made goes to stderr: the step it is in, and the window of the
	// keychain of macOS it is waiting for the owner of the machine to answer
	// (docs/DESIGN.md §7d, §7i).
	report := doctor.Run(context.Background(), systemEnv(*configPath, folder, *probe, stderr))
	if *asJSON {
		encoder := json.NewEncoder(stdout)
		// The report is read by a person too, when something in it surprises
		// them and they ask for it again with -json.
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			fmt.Fprintf(stderr, "crewflow doctor: print the report: %v\n", err)
			return exitFailure
		}
	} else {
		printReport(stdout, report)
	}
	if report.OK() {
		return exitOK
	}
	return exitFailure
}

// runDoctorNetwork is `crewflow doctor network`: what the route of the project can do,
// with each capability apart from the others. A report that said one word for all three
// would say nothing about the one that is broken — and the success of the host of the code
// says nothing at all about the provider of the model (docs/DESIGN.md §7d).
func runDoctorNetwork(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("doctor network", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { usage(stderr) }
	configPath := flags.String("config", defaultConfigPath, "path to crewflow.toml")
	asJSON := flags.Bool("json", false, "print the checks as JSON, for the orchestrator")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "crewflow doctor network: unexpected argument %q\n\n", flags.Arg(0))
		usage(stderr)
		return exitUsage
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return doctorFailed(stderr, err)
	}
	folder, removeFolder, err := probeFolder(false)
	if err != nil {
		return doctorFailed(stderr, err)
	}
	defer removeFolder()
	env := systemEnv(*configPath, folder, false, stderr)
	checks := doctor.Network(context.Background(), env, cfg)
	if *asJSON {
		if err := printJSON(stdout, checks); err != nil {
			return doctorFailed(stderr, err)
		}
	} else {
		for _, check := range checks {
			fmt.Fprintf(stdout, "%s %-22s %s\n", marks[check.Status], check.Name, check.Detail)
			if check.Hint != "" {
				fmt.Fprintf(stdout, "  %s\n", check.Hint)
			}
		}
	}
	for _, check := range checks {
		if check.Status == doctor.Fail {
			return exitFailure
		}
	}
	return exitOK
}

// doctorFailed is what a check of the machine says when it could not be made at all: a
// check that could not be made is not a check that passed.
func doctorFailed(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "crewflow doctor: %v\n", err)
	return exitFailure
}

// printReport is the report for a person: a line for every check, and the hint
// under the line of a check that is not in order.
func printReport(w io.Writer, report doctor.Report) {
	for _, check := range report.Checks {
		fmt.Fprintf(w, "%s %-22s %s\n", marks[check.Status], check.Name, check.Detail)
		if check.Hint != "" {
			fmt.Fprintf(w, "  %s\n", check.Hint)
		}
	}
	printAuthority(w, report)
	fmt.Fprintf(w, "\n%s\n", verdict(report))
}

// printAuthority is the section of the separation of the subjects of the project: the
// three accounts, whether the owner and the orchestrator are one, and the accounts in
// both lists of the gate. It is printed under the checks and not among them, because it
// is five lines of one fact and a line of a check is one fact (docs/DESIGN.md §7i, §7k).
func printAuthority(w io.Writer, report doctor.Report) {
	authority := report.Authority
	fmt.Fprintf(w, "\nauthority separation:\n")
	fmt.Fprintf(w, "  %-14s %s\n", "owner:", accountOf(authority.Owner))
	fmt.Fprintf(w, "  %-14s %s (%s)\n", "orchestrator:", accountOf(authority.Orchestrator), report.Orchestrator.Mode)
	fmt.Fprintf(w, "  %-14s %s (%s)\n", "executor:", accountOf(authority.Executor), report.Identity.Mode)
	fmt.Fprintf(w, "  %-14s %s\n", "owner == orchestrator:", yesNo(authority.OwnerIsOrchestrator))
	fmt.Fprintf(w, "  %-14s %s\n", "owners ∩ reviewers:", listedOrNone(authority.Overlap))
	for _, list := range []struct {
		what  string
		which []doctor.Subject
	}{
		{"reviewers:", authority.Reviewers},
		{"owners:", authority.Owners},
	} {
		for _, subject := range list.which {
			fmt.Fprintf(w, "  %-16s %s\n", list.what, subject)
		}
	}
	if len(report.TrustDebt) > 0 {
		fmt.Fprintf(w, "  %-14s %s\n", "trust debt:", strings.Join(report.TrustDebt, ", "))
	}
}

// accountOf is the account as a person reads it, and says so where nobody named it: a
// section that printed an empty column would leave a person to guess whether the host
// could not be asked or the project has no account of its own.
func accountOf(account string) string {
	if account == "" {
		return "the host named no account"
	}
	return account
}

// yesNo is the answer of the section to a question a person asks in one word.
func yesNo(yes bool) string {
	if yes {
		return "YES"
	}
	return "NO"
}

// listedOrNone is the accounts in both lists of the gate, and says so where the lists
// do not meet.
func listedOrNone(accounts []doctor.Subject) string {
	if len(accounts) == 0 {
		return "none"
	}
	names := make([]string, 0, len(accounts))
	for _, account := range accounts {
		names = append(names, account.Login)
	}
	return strings.Join(names, ", ")
}

// verdict is the one line that says whether crewflow can work on this machine.
func verdict(report doctor.Report) string {
	failed, warned := 0, 0
	for _, check := range report.Checks {
		switch check.Status {
		case doctor.Fail:
			failed++
		case doctor.Warn:
			warned++
		}
	}
	switch {
	case failed > 0:
		return fmt.Sprintf("%d of %d checks failed: fix them before a task starts", failed, len(report.Checks))
	case warned > 0:
		return fmt.Sprintf("nothing is missing; %d of %d checks worth a look", warned, len(report.Checks))
	default:
		return "this machine is ready for a task"
	}
}

// probeFolder is the empty folder the probe runs the executor in: an agent must
// not be pointed at the folder of the person. Nothing is created without a
// probe, and what is created is removed again.
func probeFolder(probe bool) (string, func(), error) {
	nothing := func() {}
	if !probe {
		return "", nothing, nil
	}
	folder, err := os.MkdirTemp("", "crewflow-probe-")
	if err != nil {
		return "", nothing, fmt.Errorf("make a folder for the probe: %w", err)
	}
	return folder, func() { _ = os.RemoveAll(folder) }, nil
}
