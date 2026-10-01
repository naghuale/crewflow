package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/forge/github/app"
	"github.com/naghuale/crewflow/internal/secret"
)

// The machine as the roles of a project and the commands about the key of an app work
// on it. They are variables so that a test of a command is a test of a store, a server
// and a clock of its own: no test of crewflow opens the keychain of the person who runs
// it, and no test asks GitHub for a token of a real app (docs/DESIGN.md §7i).
var (
	// secretsOfMachine is where the key of an app of a project is kept.
	secretsOfMachine = secret.System
	// httpOfMachine and clockOfMachine are how a role talks to a host and when: a
	// token of an app is signed with the clock of the machine and lives an hour, and
	// a command that asks for one without meaning to is a command that holds a
	// credential it throws away.
	httpOfMachine  = &http.Client{Timeout: 30 * time.Second}
	clockOfMachine = time.Now
	// authStdin is where the helper of the credentials of git reads the request of
	// git: git starts a helper with the operation as its argument and the request on
	// the standard input of the process.
	authStdin io.Reader = os.Stdin
)

// runAuth runs the commands about the credentials of a run: putting the key of an app
// in the store of the machine, saying whether the app is set up, and answering git
// when it asks for a password (docs/DESIGN.md §7i).
func runAuth(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintf(stderr, "crewflow auth: nothing to do\n\n")
		usage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "app":
		return runAuthApp(args[1:], stdout, stderr)
	case "git-credential":
		return runGitCredential(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		usage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "crewflow auth: unknown subcommand %q\n\n", args[0])
		usage(stderr)
		return exitUsage
	}
}

// runAuthApp is `crewflow auth app import|check`: the key of the app of the project
// goes into the store of the machine once, and a check says whether a run in the mode
// of the bot can start at all.
func runAuthApp(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintf(stderr, "crewflow auth app: what to do with the app?\n\n")
		usage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "import":
		return runAuthAppImport(args[1:], stdout, stderr)
	case "check":
		return runAuthAppCheck(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		usage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "crewflow auth app: unknown subcommand %q\n\n", args[0])
		usage(stderr)
		return exitUsage
	}
}

// runAuthAppImport is `crewflow auth app import <file.pem>`: the private key a person
// downloaded from the settings of the app goes into the store of the machine, and the
// file it came from is theirs to delete.
//
// Nothing of the key is ever printed: not the key, not a part of it, and not a length
// of it. The key is read, checked to be the RSA key of an app, written to the store,
// and read back to be held as a key once more — a store that kept a part of it is a
// store a run of this project cannot sign a token with, and a person has to find that
// out here rather than in the middle of a task (docs/DESIGN.md §7e, §7i).
//
// `-as orchestrator` is the key of the second App of a project, and it goes into the store
// under its own name: a project with two Apps has two keys, and one item under one name is
// one of them (§7i).
func runAuthAppImport(args []string, stdout, stderr io.Writer) int {
	flags := authFlags("auth app import", stderr)
	configPath := flags.String("config", defaultConfigPath, "path to crewflow.toml")
	as := subjectFlag(flags, stderr)
	// The path of the key comes first, as a person writes it, and the flags of the
	// command after it: the flag package of Go stops at the first word that is not a
	// flag, so both orders have to mean the same thing.
	path, rest := takeFirst(args)
	if err := flags.Parse(rest); err != nil {
		return exitUsage
	}
	if path == "" || flags.NArg() > 0 {
		fmt.Fprintf(stderr, "crewflow auth app import: the file of the private key of the app, "+
			"one path, as in `crewflow auth app import ~/Downloads/crewflow-app.pem`\n\n")
		usage(stderr)
		return exitUsage
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return authFailed(stderr, err)
	}
	appID, err := appIDOf(cfg, *configPath, *as)
	if err != nil {
		return authFailed(stderr, err)
	}
	downloaded, err := os.ReadFile(path)
	if err != nil {
		return authFailed(stderr, fmt.Errorf("read %s: %w", path, err))
	}
	key, err := app.ReadKey(downloaded)
	if err != nil {
		return authFailed(stderr, fmt.Errorf("%s: %w", path, err))
	}
	account := secret.AppKey(appID)
	// The keychain of macOS asks the owner of the machine in a window of the system
	// before it lets a program write a secret of it, and an import is a program of a
	// terminal the owner is sitting at: the line about the window is said to the same
	// place as the rest of the answer, and the wait has an end (docs/DESIGN.md §7i).
	store := storeOfSecrets(secret.NewNotices(stderr))
	if err := store.Set(secret.Service, account, key.PEM()); err != nil {
		return authFailed(stderr, fmt.Errorf("put the key of the app %d away: %w", appID, err))
	}
	kept, err := store.Get(secret.Service, account)
	if err != nil {
		return authFailed(stderr, fmt.Errorf("read the key of the app %d back: %w", appID, err))
	}
	if _, err := app.ReadKey(kept); err != nil {
		return authFailed(stderr, fmt.Errorf("the key of the app %d that was kept is not one: %w; "+
			"import it again from the file GitHub gave you", appID, err))
	}
	fmt.Fprintf(stdout, "the key of the app %d is in the store of this machine\n", appID)
	fmt.Fprintf(stdout, "delete %s: it is a private key in a folder of your own, and nothing of it belongs in a file\n", path)
	return exitOK
}

// runAuthAppCheck is `crewflow auth app check`: the key is in the store of the machine,
// the app is installed on the repository of the project, and it may do no more than a
// run of it needs. The token of the app is not asked for and not printed: a check of a
// setup has nothing to do with a credential of an hour (docs/DESIGN.md §7i).
func runAuthAppCheck(args []string, stdout, stderr io.Writer) int {
	flags := authFlags("auth app check", stderr)
	configPath := flags.String("config", defaultConfigPath, "path to crewflow.toml")
	as := subjectFlag(flags, stderr)
	asJSON := flags.Bool("json", false, "print the check as JSON, for the orchestrator")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "crewflow auth app check: unexpected argument %q\n\n", flags.Arg(0))
		usage(stderr)
		return exitUsage
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return authFailed(stderr, err)
	}
	source, err := appOf(cfg, *configPath, *as, secret.NewNotices(stderr))
	if err != nil {
		return authFailed(stderr, err)
	}
	ctx := context.Background()
	// The key comes first: without it crewflow cannot be let in as the app, and a check
	// that cannot say whose name a run would work under says what is missing rather
	// than a mode with a hole in it.
	hasKey, err := source.HasKey()
	if err != nil {
		return authFailed(stderr, err)
	}
	answer := struct {
		Mode         string            `json:"mode"`
		Description  string            `json:"description"`
		Subject      string            `json:"subject"`
		AppID        int64             `json:"app_id"`
		Key          bool              `json:"key"`
		Installation *app.Installation `json:"installation,omitempty"`
	}{Mode: app.ModeBot, Subject: *as, AppID: source.AppID, Key: hasKey}
	if *as == "orchestrator" {
		// A check of the app of the orchestrator answers about the orchestrator, and a
		// report that said "bot" about it would be a report about the wrong account of
		// the host (§7i).
		answer.Mode = app.ModeSeparate
	}
	if hasKey {
		// The line of the subject the check is about: the executor of a run or the
		// orchestrator of the project, in the words of §7i for each of them.
		describe := source.Describe
		if *as == "orchestrator" {
			describe = source.DescribeOrchestrator
		}
		identity, err := describe(ctx)
		if err != nil {
			return authFailed(stderr, err)
		}
		answer.Mode, answer.Description = identity.Mode, identity.Description
	}
	if !hasKey {
		if !*asJSON {
			fmt.Fprintf(stdout, "key: not there in the store of this machine, as %q\n", secret.AppKey(answer.AppID))
		}
		if *asJSON {
			if err := printJSON(stdout, answer); err != nil {
				return authFailed(stderr, err)
			}
		}
		fmt.Fprintf(stderr, "crewflow auth app check: the key of the app %d is not in the store of this machine: "+
			"download it in the settings of the app and run `crewflow auth app import <file.pem>`\n", source.AppID)
		return exitFailure
	}
	if !*asJSON {
		fmt.Fprintf(stdout, "%s: %s\n", *as, answer.Description)
		fmt.Fprintf(stdout, "key: there in the store of this machine, as %q\n", secret.AppKey(answer.AppID))
	}
	installation, err := source.Installation(ctx)
	if err != nil {
		if *asJSON {
			_ = printJSON(stdout, answer)
		}
		fmt.Fprintf(stderr, "crewflow auth app check: %v\n", err)
		return exitFailure
	}
	answer.Installation = &installation
	if *asJSON {
		if err := printJSON(stdout, answer); err != nil {
			return authFailed(stderr, err)
		}
	} else {
		fmt.Fprintf(stdout, "installation: %d on %s\n", installation.ID, source.Repo)
		fmt.Fprintf(stdout, "rights: %s\n", strings.Join(installation.Granted(), ", "))
	}
	// The rights of an app wider than its work needs are said and are a failure of the
	// check: they are the powers of that account on every day of this project, and §7i
	// is about exactly those powers. They go to the error in both shapes of the answer,
	// because a person has to change them and a script has to fail on them.
	asked := app.RunRights()
	if *as == "orchestrator" {
		asked = app.OrchestratorRights()
	}
	if extra := installation.Beyond(asked); len(extra) > 0 {
		fmt.Fprintf(stderr, "crewflow auth app check: the app %d may do more than the %s of this project needs: %s; "+
			"change its permissions in the settings of GitHub\n", source.AppID, *as, strings.Join(extra, ", "))
		return exitFailure
	}
	return exitOK
}

// runGitCredential is the helper git calls in the worktree of a run: git starts it with
// the operation of the credentials in the line of the command and the request of the
// credentials on its standard input, and it needs a password for github.com and for the
// repository of the project and for nothing else (docs/DESIGN.md §7i).
//
// Git writes the operation at the end of that line, after the flags, and a person writes
// it before them; both are read, and only one word of the line may be an operation
// (docs/DESIGN.md §6).
//
// The password it answers with is a token it signs now, for this one push: a run of an
// agent is longer than the life of a token of an hour, and the executor of a run must
// not be handed one twice. The helper answers for the mode of the bot alone — a project
// in the mode of the owner pushes with the login of the person, and a helper that had
// something to say about that would be a helper in the way of the login of a person.
//
// `-as orchestrator` is the helper of the push of a merge in the mode of a separate
// login: a project has two Apps then, and the push of the approved commit has to be
// signed by the one of the orchestrator and not by the one of the executor (§7h, §7i).
func runGitCredential(args []string, stdout, stderr io.Writer) int {
	flags := authFlags("auth git-credential", stderr)
	configPath := flags.String("config", defaultConfigPath, "path to crewflow.toml")
	// Whom the helper signs a token for: the executor of a run by default, and the
	// orchestrator of the project where it is asked. The two are one word each because
	// they are the two subjects of a project and crewflow knows no third (§7i).
	as := flags.String("as", "executor", "whose account the token is for: executor | orchestrator")
	// The path of the key comes first, as a person writes it, and the flags of the
	// command after it: the flag package of Go stops at the first word that is not a
	// flag, so both orders have to mean the same thing.
	leading, rest := takeFirst(args)
	if err := flags.Parse(rest); err != nil {
		return exitUsage
	}
	operation, is := operationOf(leading, flags.Args())
	if !is {
		fmt.Fprintf(stderr, "crewflow auth git-credential: unexpected arguments after the flags: %v\n\n",
			flags.Args())
		usage(stderr)
		return exitUsage
	}
	switch *as {
	case "executor", "orchestrator":
	default:
		fmt.Fprintf(stderr, "crewflow auth git-credential: whose account is %q? executor or orchestrator\n\n", *as)
		usage(stderr)
		return exitUsage
	}
	switch operation {
	case "", "get":
	case "store", "erase":
		// `store` and `erase` are notes of git about itself, and a helper that kept them
		// would be a helper with a memory of the credentials of a person.
		return exitOK
	default:
		fmt.Fprintf(stderr, "crewflow auth git-credential: the operation is %q: get, store or erase\n\n", operation)
		usage(stderr)
		return exitUsage
	}
	request, err := readRequest(authStdin)
	if err != nil {
		return authFailed(stderr, err)
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return authFailed(stderr, err)
	}
	if !worksAsAnAccountOfItsOwn(cfg, *as) ||
		request["protocol"] != "https" ||
		request["host"] != hostOfProject(cfg) || !isTheRepositoryOf(cfg.Project.Repo, request["path"]) {
		// Nothing said is the right answer for anything that is not a push of this
		// project to this host, and for a push in a mode with no account of its own to
		// sign it: git then asks whoever else can answer, which is what it did before
		// crewflow had a helper to ask (§7i).
		return exitOK
	}
	source, err := appOf(cfg, *configPath, *as, secret.NewNotices(stderr))
	if err != nil {
		return authFailed(stderr, err)
	}
	token, err := source.Token(context.Background())
	if err != nil {
		return authFailed(stderr, err)
	}
	// The name of the user is the one GitHub itself documents for a token of an
	// installation, and the password is the token: git puts the two together and
	// stops asking about it.
	fmt.Fprintf(stdout, "username=x-access-token\npassword=%s\n", token.Value)
	return exitOK
}

// isTheRepositoryOf is whether the repository git named in its request is the repository
// of the project. Git writes the name as the host holds it — for the address
// `https://github.com/naghuale/crewflow.git` it asks about `naghuale/crewflow.git` — and
// the `.git` at the end and a `/` after it are not another repository (docs/DESIGN.md §6).
func isTheRepositoryOf(repo, path string) bool {
	name := strings.TrimSuffix(path, "/")
	return strings.TrimSuffix(name, ".git") == repo
}

// hostOfProject is the server git has to be talking to for the credentials of this
// project: github.com, or the server of the project when it has one of its own.
func hostOfProject(cfg config.Config) string {
	if cfg.Forge.Host == "" {
		return "github.com"
	}
	return cfg.Forge.Host
}

// readRequest is what git asked the helper for: lines of "key=value", one to a line,
// and nothing else. A request with lines crewflow does not know is a request of a git
// that has more to say, and the lines crewflow knows are read out of it as they are.
func readRequest(in io.Reader) (map[string]string, error) {
	if in == nil {
		return map[string]string{}, nil
	}
	request := map[string]string{}
	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		key, value, found := strings.Cut(scanner.Text(), "=")
		if found {
			request[key] = value
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read what git asked for: %w", err)
	}
	return request, nil
}

// subjectFlag is the flag every command about an app of a project has: whose account it
// is about, `executor` or `orchestrator`, the two subjects of §7i. The help is one line and
// the same everywhere, so that a person who has read it once knows it in the next command.
func subjectFlag(flags *flag.FlagSet, stderr io.Writer) *string {
	return flags.String("as", "executor", "whose account this is about: executor | orchestrator")
}

// appIDOf is the number of the App a subject of the project works as, and why there is
// none where the project names none: the keys of two Apps go into the store under two
// names, and a key put away for an app nobody named is a key of nothing (§7i).
func appIDOf(cfg config.Config, configPath, subject string) (int64, error) {
	switch subject {
	case "executor":
		if cfg.Identity.GitHubApp.AppID == 0 {
			return 0, fmt.Errorf("identity.github_app.app_id: the file of the project does not name an app: set it in %s", configPath)
		}
		return cfg.Identity.GitHubApp.AppID, nil
	case "orchestrator":
		if cfg.Orchestrator.GitHubApp.AppID == 0 {
			return 0, fmt.Errorf("orchestrator.github_app.app_id: the file of the project does not name an app: set it in %s", configPath)
		}
		return cfg.Orchestrator.GitHubApp.AppID, nil
	default:
		return 0, fmt.Errorf("whose account is %q? executor or orchestrator", subject)
	}
}

// worksAsAnAccountOfItsOwn is whether the subject works as an account of the host of
// the project at all: the executor in the mode of the bot and the orchestrator in the
// mode of a separate login, and nobody else. A project in the mode of the owner pushes
// with the login of the person, and a helper of an App in the way of that login would
// be a helper crewflow put there for nothing (§7i).
func worksAsAnAccountOfItsOwn(cfg config.Config, subject string) bool {
	switch subject {
	case "executor":
		return cfg.Identity.Mode == "bot"
	case "orchestrator":
		return cfg.Orchestrator.Mode == config.ModeSeparate
	default:
		return false
	}
}

// appOf is the app a subject of a project works as, as the commands about it see it:
// the settings of the project, the store of the machine where its key is, the HTTP to
// the host and the clock. Every field is a plain value, which is what makes a test of
// these commands a test of a store and a server of its own (docs/DESIGN.md §7i).
//
// The subject is "executor" or "orchestrator", the two of §7i: a project in the mode of
// the shared login has no app for the orchestrator, and a project that asks for the
// separate one has to name its second app.
//
// The store is behind the wait of the keychain of macOS and says what it is about to
// wait for to the notices of the command: a check that stands in front of a window of
// the system in silence for ever is a check that looks like a machine that hangs (§7i).
func appOf(cfg config.Config, configPath, subject string, notices *secret.Notices) (*app.Source, error) {
	switch subject {
	case "executor":
		if cfg.Identity.Mode != "bot" {
			return nil, fmt.Errorf("identity.mode: the executor of this project works as %q, "+
				"so there is no app of its own: set [identity] mode = %q in %s", cfg.Identity.Mode, "bot", configPath)
		}
		if cfg.Identity.GitHubApp.AppID == 0 {
			return nil, fmt.Errorf("identity.github_app.app_id: the file of the project does not name an app: set it in %s", configPath)
		}
		return sourceOfApp(cfg, configPath, notices, app.Executor,
			cfg.Identity.GitHubApp.AppID, cfg.Identity.GitHubApp.InstallationID), nil
	case "orchestrator":
		if cfg.Orchestrator.Mode != config.ModeSeparate {
			return nil, fmt.Errorf("orchestrator.mode: the orchestrator of this project works as %q, "+
				"so there is no app of its own: set [orchestrator] mode = %q in %s",
				cfg.Orchestrator.Mode, config.ModeSeparate, configPath)
		}
		if cfg.Orchestrator.GitHubApp.AppID == 0 {
			return nil, fmt.Errorf("orchestrator.github_app.app_id: the file of the project does not name an app: set it in %s", configPath)
		}
		return sourceOfApp(cfg, configPath, notices, app.Orchestrator,
			cfg.Orchestrator.GitHubApp.AppID, cfg.Orchestrator.GitHubApp.InstallationID), nil
	default:
		return nil, fmt.Errorf("whose account is %q? executor or orchestrator", subject)
	}
}

// sourceOfApp is the App of a subject of the project as a command signs a token with it:
// the numbers the file of the project names, the repository it is for, the address of
// the API of the host, the store of the machine where its key is, and the clock the
// token is signed with (docs/DESIGN.md §7i).
func sourceOfApp(cfg config.Config, configPath string, notices *secret.Notices,
	role app.Role, appID, installationID int64) *app.Source {
	return &app.Source{
		AppID:          appID,
		InstallationID: installationID,
		Role:           role,
		Repo:           cfg.Project.Repo,
		BaseURL:        app.API(cfg.Forge.Host),
		Store:          storeOfSecrets(notices),
		HTTP:           httpOfMachine,
		Now:            clockOfMachine,
	}
}

// takeFirst is the first word of the arguments that is not a flag, and the rest of them
// without it: a person writes the thing they name before the flags of the command, and
// the flag package of Go stops at the first word that is not one (docs/DESIGN.md §6).
func takeFirst(args []string) (string, []string) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return args[0], args[1:]
	}
	return "", args
}

// operationOf is the operation of the credentials in the words of the line of the helper,
// and there is one of them however the line was written: git adds the operation as the
// last word of the line, after the flags of the command, and a person writes it before
// them. Only the last word was read before, so git got `unexpected argument "get"` for
// the very push the helper exists to sign (F-085, 01.10.2026).
//
// It says no where the line names two operations or names words that are not one: a
// helper that guessed which of them was meant would sign on a guess.
func operationOf(leading string, left []string) (string, bool) {
	switch {
	case leading == "" && len(left) == 0:
		return "", true
	case leading == "" && len(left) == 1:
		return left[0], true
	case leading != "" && len(left) == 0:
		return leading, true
	default:
		return "", false
	}
}

// authFlags are the flags of a command of auth, and the usage that goes with them.
func authFlags(subcommand string, stderr io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet(subcommand, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { usage(stderr) }
	return flags
}

// authFailed is what a command of auth says when it could not do what it was told.
func authFailed(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "crewflow auth: %v\n", err)
	return exitFailure
}
