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
func runAuthAppImport(args []string, stdout, stderr io.Writer) int {
	flags := authFlags("auth app import", stderr)
	configPath := flags.String("config", defaultConfigPath, "path to crewflow.toml")
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
	appID := cfg.Identity.GitHubApp.AppID
	if appID == 0 {
		return authFailed(stderr, fmt.Errorf("identity.github_app.app_id: the file of the project does not name an app, "+
			"so there is no key to put away: set it in %s", *configPath))
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
	if err := secretsOfMachine().Set(secret.Service, account, key.PEM()); err != nil {
		return authFailed(stderr, fmt.Errorf("put the key of the app %d away: %w", appID, err))
	}
	kept, err := secretsOfMachine().Get(secret.Service, account)
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
	source, err := appOf(cfg, *configPath)
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
		AppID        int64             `json:"app_id"`
		Key          bool              `json:"key"`
		Installation *app.Installation `json:"installation,omitempty"`
	}{Mode: app.ModeBot, AppID: source.AppID, Key: hasKey}
	if hasKey {
		identity, err := source.Describe(ctx)
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
		fmt.Fprintf(stdout, "executor: %s\n", answer.Description)
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
	// The rights of an app wider than a run needs are said and are a failure of the
	// check: they are the powers of the executor of every run of this project, and §7i
	// is about exactly those powers. They go to the error in both shapes of the answer,
	// because a person has to change them and a script has to fail on them.
	if extra := installation.BeyondARun(); len(extra) > 0 {
		fmt.Fprintf(stderr, "crewflow auth app check: the app %d may do more than a run of this project needs: %s; "+
			"change its permissions in the settings of GitHub\n", source.AppID, strings.Join(extra, ", "))
		return exitFailure
	}
	return exitOK
}

// runGitCredential is the helper git calls in the worktree of a run: git starts it with
// the operation as its argument and the request of the credentials on its standard
// input, and it needs a password for github.com and for the repository of the project
// and for nothing else (docs/DESIGN.md §7i).
//
// The password it answers with is a token it signs now, for this one push: a run of an
// agent is longer than the life of a token of an hour, and the executor of a run must
// not be handed one twice. The helper answers for the mode of the bot alone — a project
// in the mode of the owner pushes with the login of the person, and a helper that had
// something to say about that would be a helper in the way of the login of a person.
func runGitCredential(args []string, stdout, stderr io.Writer) int {
	flags := authFlags("auth git-credential", stderr)
	configPath := flags.String("config", defaultConfigPath, "path to crewflow.toml")
	// Git says what it wants with the operation alone, and it comes first because that
	// is how git writes the command line: `crewflow auth git-credential get`. A person
	// who runs it by hand may put the flags before the operation, and both orders mean
	// the same thing.
	operation, rest := takeFirst(args)
	if err := flags.Parse(rest); err != nil {
		return exitUsage
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "crewflow auth git-credential: unexpected argument %q\n\n", flags.Arg(0))
		usage(stderr)
		return exitUsage
	}
	// `store` and `erase` are notes of git about itself, and a helper that kept them
	// would be a helper with a memory of the credentials of a person.
	if operation != "" && operation != "get" {
		return exitOK
	}
	request, err := readRequest(authStdin)
	if err != nil {
		return authFailed(stderr, err)
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return authFailed(stderr, err)
	}
	if cfg.Identity.Mode != "bot" || request["protocol"] != "https" ||
		request["host"] != hostOfProject(cfg) || request["path"] != cfg.Project.Repo {
		// Nothing said is the right answer for anything that is not a push of this
		// project to this host: git then asks whoever else can answer, which is what
		// it did before crewflow had a helper to ask.
		return exitOK
	}
	source, err := appOf(cfg, *configPath)
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

// appOf is the app of a project as the commands about it see it: the settings of the
// project, the store of the machine where its key is, the HTTP to the host and the
// clock. Every field is a plain value, which is what makes a test of these commands a
// test of a store and a server of its own (docs/DESIGN.md §7i).
func appOf(cfg config.Config, configPath string) (*app.Source, error) {
	if cfg.Identity.Mode != "bot" {
		return nil, fmt.Errorf("identity.mode: the executor of this project works as %q, "+
			"so there is no app of its own: set [identity] mode = %q in %s", cfg.Identity.Mode, "bot", configPath)
	}
	if cfg.Identity.GitHubApp.AppID == 0 {
		return nil, fmt.Errorf("identity.github_app.app_id: the file of the project does not name an app: set it in %s", configPath)
	}
	return &app.Source{
		AppID:          cfg.Identity.GitHubApp.AppID,
		InstallationID: cfg.Identity.GitHubApp.InstallationID,
		Repo:           cfg.Project.Repo,
		BaseURL:        app.API(cfg.Forge.Host),
		Store:          secretsOfMachine(),
		HTTP:           httpOfMachine,
		Now:            clockOfMachine,
	}, nil
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
