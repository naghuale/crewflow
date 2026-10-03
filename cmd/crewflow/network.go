package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/doctor"
	"github.com/naghuale/crewflow/internal/network"
	"github.com/naghuale/crewflow/internal/secret"
)

// The machine the commands about the network work on: the store of the secrets of the
// machine, the clock, and the check of a route. They are variables so that a test of a
// command is a test of a store, a server and a clock of its own — no test of crewflow
// opens the keychain of the person who runs it, and no test asks the network of the
// machine the tests run on (docs/DESIGN.md §7d, §7e).
var (
	secretsOfTheNetwork = secret.System
	clockOfTheNetwork   = time.Now
	// checksOfARoute asks what a route can do, one capability at a time.
	checksOfARoute = func(ctx context.Context, out *secret.Out, cfg config.Config, route network.Route, store secret.Store) ([]network.State, error) {
		connect, validFor, err := network.Terms(cfg)
		if err != nil {
			return nil, err
		}
		machine := network.Factory(cfg)(network.System(route, cfg.Network.NoProxy, store, clockOfTheNetwork, connect, validFor))
		machine.Git = doctor.Git
		// The answers of the check are printed on the terminal of the person and are read
		// in issues, and the credentials of the profile were in the environment of every
		// program the check started: they are cleaned by the boundary of the command, which
		// is the boundary the check learns them into as well (docs.DESIGN.md §7e).
		machine.Out = out
		return network.Check(ctx, machine, route, cfg.Network.NoProxy)
	}
)

// runNetwork is `crewflow network`: the route of the programs of crewflow — straight out
// or through one of the named profiles of the project — and the profiles themselves, which
// are changed with commands because the address of a proxy is different on every machine
// and changes when its owner moves (docs/DESIGN.md §7d).
func runNetwork(out *secret.Out, args []string, stdout, stderr *secret.Writer) int {
	if len(args) == 0 {
		fmt.Fprintf(stderr, "crewflow network: nothing to do\n\n")
		usage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "proxy":
		return runNetworkProxy(out, args[1:], stdout, stderr)
	case "mode":
		return runNetworkMode(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		usage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "crewflow network: unknown subcommand %q\n\n", args[0])
		usage(stderr)
		return exitUsage
	}
}

// runNetworkProxy is `crewflow network proxy list|add|edit|use|remove|credentials|test`.
func runNetworkProxy(out *secret.Out, args []string, stdout, stderr *secret.Writer) int {
	if len(args) == 0 {
		fmt.Fprintf(stderr, "crewflow network proxy: what to do with a proxy?\n\n")
		usage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "list":
		return runNetworkList(args[1:], stdout, stderr)
	case "add":
		return runNetworkAdd(args[1:], stdout, stderr)
	case "edit":
		return runNetworkEdit(args[1:], stdout, stderr)
	case "use":
		return runNetworkUse(args[1:], stdout, stderr)
	case "remove":
		return runNetworkRemove(args[1:], stdout, stderr)
	case "credentials":
		return runNetworkCredentials(out, args[1:], stdout, stderr)
	case "test":
		return runNetworkTest(out, args[1:], stdout, stderr)
	case "help", "-h", "--help":
		usage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "crewflow network proxy: unknown subcommand %q\n\n", args[0])
		usage(stderr)
		return exitUsage
	}
}

// runNetworkMode is `crewflow network mode direct|proxy|fallback`: which route the programs
// of crewflow go out by. The three modes of §7d are the three that can be written, and the
// refusal of the command names them: a project cannot be left in a mode no program of it can
// work with, and a command that promised a route and then let the loader refuse the file
// would leave a person with the error of a load instead of the one of the command.
func runNetworkMode(args []string, stdout, stderr io.Writer) int {
	flags := networkFlags("network mode", stderr)
	configPath := flags.String("config", defaultConfigPath, "path to crewflow.toml")
	mode, rest := takeFirst(args)
	if err := flags.Parse(rest); err != nil {
		return exitUsage
	}
	if mode == "" || flags.NArg() > 0 {
		fmt.Fprintf(stderr, "crewflow network mode: the route of the project, direct, proxy or "+
			"fallback, as in `crewflow network mode proxy`\n\n")
		usage(stderr)
		return exitUsage
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return networkFailed(stderr, err)
	}
	// A route that names nothing is not a route, and the file is not changed before the
	// route is known to be one: a project whose mode says `proxy` — or `fallback`, whose
	// second attempt has to go through a profile — and whose active profile names nobody
	// is a file no program can work with (§7d).
	if (mode == "proxy" || mode == "fallback") && cfg.Network.ActiveProxy == "" {
		return networkFailed(stderr, fmt.Errorf("network.mode = %q and no network.active_proxy: "+
			"choose a profile with `crewflow network proxy use <name>`", mode))
	}
	if err := network.Mode(*configPath, mode); err != nil {
		return networkFailed(stderr, err)
	}
	sayRouteChanged(stderr, "mode", network.Route{Why: fmt.Sprintf("mode=%s", mode)})
	fmt.Fprintf(stdout, "the route of the project is %s\n", mode)
	switch mode {
	case "proxy":
		fmt.Fprintf(stdout, "through the profile %q\n", cfg.Network.ActiveProxy)
	case "fallback":
		// The second route is told here because a person who switched the mode has to know
		// which profile the one more attempt goes through, and where to look when it does
		// not answer (§7d).
		fmt.Fprintf(stdout, "straight out first, and one attempt through the profile %q when the network is not there\n",
			cfg.Network.ActiveProxy)
	}
	return exitOK
}

// profileOfTheTest is one profile as `crewflow network proxy list` shows it: what it is
// and where it listens, whether it is the active one, and whether its credentials are in
// the store of the machine — never the credentials themselves (docs/DESIGN.md §7e).
type profileShown struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Active     bool   `json:"active"`
	Declares   string `json:"credentials"`
	Configured bool   `json:"credentials_configured"`
}

// runNetworkList is `crewflow network proxy list`: the profiles of the project, one line
// each. A profile that is there is not a profile that works, and this command says nothing
// about that — `crewflow network proxy test` is what asks the machine (§7d).
func runNetworkList(args []string, stdout, stderr *secret.Writer) int {
	flags := networkFlags("network proxy list", stderr)
	configPath := flags.String("config", defaultConfigPath, "path to crewflow.toml")
	asJSON := flags.Bool("json", false, "print the profiles as JSON, for the orchestrator")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "crewflow network proxy list: unexpected argument %q\n\n", flags.Arg(0))
		usage(stderr)
		return exitUsage
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return networkFailed(stderr, err)
	}
	store := storeOfTheNetwork(stderr)
	profiles, err := shownProfiles(cfg, store)
	if err != nil {
		return networkFailed(stderr, err)
	}
	if *asJSON {
		if err := printJSON(stdout, secret.DocumentNetworkProxyList, profiles); err != nil {
			return networkFailed(stderr, err)
		}
		return exitOK
	}
	if len(profiles) == 0 {
		fmt.Fprintln(stdout, "no proxy profiles in the file of the project yet")
		return exitOK
	}
	fmt.Fprintf(stdout, "mode: %s\n", cfg.Network.Mode)
	for _, profile := range profiles {
		fmt.Fprintf(stdout, "%-10s %-6s %s:%d  credentials=%s  %s\n",
			profile.Name, profile.Type, profile.Host, profile.Port, profile.shown(), profile.active())
	}
	return exitOK
}

func (p profileShown) shown() string {
	if p.Declares != "secret-store" {
		return p.Declares
	}
	if p.Configured {
		return "secret-store (configured)"
	}
	return "secret-store (not configured)"
}

func (p profileShown) active() string {
	if p.Active {
		return "active"
	}
	return ""
}

// shownProfiles is what the report of the profiles says, each of them through the store of
// the machine only as far as whether it holds something under the name of the profile.
func shownProfiles(cfg config.Config, store secret.Store) ([]profileShown, error) {
	profiles := make([]profileShown, 0, len(cfg.Network.Proxies))
	for _, name := range slices.Sorted(maps.Keys(cfg.Network.Proxies)) {
		profile := cfg.Network.Proxies[name]
		shown := profileShown{
			Name: name, Type: profile.Type, Host: profile.Host, Port: profile.Port,
			Active: cfg.Network.ActiveProxy == name, Declares: profile.Credentials,
		}
		configured, err := network.Configured(store, network.Route{Name: name, Profile: profile})
		if err != nil {
			return nil, err
		}
		shown.Configured = configured
		profiles = append(profiles, shown)
	}
	return profiles, nil
}

// runNetworkAdd is `crewflow network proxy add <name> --type --host --port`: a profile
// written into the file of the project. It is written and not checked — a profile can be
// prepared before it answers, and `crewflow network proxy test` is what asks the machine.
func runNetworkAdd(args []string, stdout, stderr io.Writer) int {
	flags := networkFlags("network proxy add", stderr)
	configPath := flags.String("config", defaultConfigPath, "path to crewflow.toml")
	kind := flags.String("type", "", "what the proxy speaks: http, https or socks5")
	host := flags.String("host", "", "the host or the address of the proxy, without a scheme and without credentials")
	port := flags.Int("port", 0, "the port the proxy listens on")
	credentials := flags.String("credentials", "none", "where the credentials of the profile are: none or secret-store")
	name, rest := takeFirst(args)
	if err := flags.Parse(rest); err != nil {
		return exitUsage
	}
	if name == "" || flags.NArg() > 0 {
		fmt.Fprintf(stderr, "crewflow network proxy add: the name of the profile and where it listens, "+
			"as in `crewflow network proxy add home --type http --host 192.0.2.10 --port 1082`\n\n")
		usage(stderr)
		return exitUsage
	}
	if *kind == "" || *host == "" || *port == 0 {
		return networkFailed(stderr, fmt.Errorf("a profile needs --type, --host and --port: "+
			"a proxy without them is not one crewflow could talk to"))
	}

	profile := config.Proxy{Type: *kind, Host: *host, Port: *port, Credentials: *credentials}
	if err := network.Add(*configPath, name, profile); err != nil {
		return networkFailed(stderr, err)
	}
	sayProfileChanged(stderr, "add", name, profile)
	fmt.Fprintf(stdout, "the profile %q is in %s: %s://%s:%d\n", name, *configPath, profile.Type, profile.Host, profile.Port)
	fmt.Fprintf(stdout, "check that it answers with `crewflow network proxy test %s`\n", name)
	return exitOK
}

// runNetworkEdit is `crewflow network proxy edit <name> [--type] [--host] [--port]
// [--credentials]`: what a person changes when the proxy moved, which is often. The
// profile is written whole or not at all, and the availability of it is checked by
// `crewflow network proxy test` and not by this command: a profile may be prepared before
// it answers (§7d).
func runNetworkEdit(args []string, stdout, stderr io.Writer) int {
	flags := networkFlags("network proxy edit", stderr)
	configPath := flags.String("config", defaultConfigPath, "path to crewflow.toml")
	kind := flags.String("type", "", "what the proxy speaks: http, https or socks5")
	host := flags.String("host", "", "the host or the address of the proxy")
	port := flags.Int("port", 0, "the port the proxy listens on")
	credentials := flags.String("credentials", "", "where the credentials of the profile are: none or secret-store")
	name, rest := takeFirst(args)
	if err := flags.Parse(rest); err != nil {
		return exitUsage
	}
	if name == "" || flags.NArg() > 0 {
		fmt.Fprintf(stderr, "crewflow network proxy edit: the name of the profile and the keys to change, "+
			"as in `crewflow network proxy edit home --port 3128`\n\n")
		usage(stderr)
		return exitUsage
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return networkFailed(stderr, err)
	}
	current, known := cfg.Network.Proxies[name]
	if !known {
		return networkFailed(stderr, fmt.Errorf("the profile %q is in no [network.proxies] of %s", name, *configPath))
	}
	for what, named := range map[string]bool{"--type": *kind != "", "--host": *host != "", "--port": *port != 0} {
		if !named {
			continue
		}
		switch what {
		case "--type":
			current.Type = *kind
		case "--host":
			current.Host = *host
		case "--port":
			current.Port = *port
		}
	}
	if *credentials != "" {
		current.Credentials = *credentials
	}
	if *kind == "" && *host == "" && *port == 0 && *credentials == "" {
		return networkFailed(stderr, fmt.Errorf("nothing to change: name the keys, as in "+
			"`crewflow network proxy edit %s --port 3128`", name))
	}
	if err := network.Edit(*configPath, name, current); err != nil {
		return networkFailed(stderr, err)
	}
	sayProfileChanged(stderr, "edit", name, current)
	fmt.Fprintf(stdout, "the profile %q is now %s://%s:%d\n", name, current.Type, current.Host, current.Port)
	fmt.Fprintf(stdout, "it is not active unless it was: `crewflow network proxy use %s`\n", name)
	return exitOK
}

// runNetworkUse is `crewflow network proxy use <name>`: the profile a request goes
// through in the mode `proxy`. The mode is not changed here — a person who named a profile
// and did not switch the route to it has prepared for it and has not asked for it.
func runNetworkUse(args []string, stdout, stderr io.Writer) int {
	flags := networkFlags("network proxy use", stderr)
	configPath := flags.String("config", defaultConfigPath, "path to crewflow.toml")
	name, rest := takeFirst(args)
	if err := flags.Parse(rest); err != nil {
		return exitUsage
	}
	if name == "" || flags.NArg() > 0 {
		fmt.Fprintf(stderr, "crewflow network proxy use: the name of the profile, "+
			"as in `crewflow network proxy use home`\n\n")
		usage(stderr)
		return exitUsage
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return networkFailed(stderr, err)
	}
	if err := network.Use(*configPath, name); err != nil {
		return networkFailed(stderr, err)
	}
	sayRouteChanged(stderr, "use", network.Route{Why: fmt.Sprintf("active_proxy=%s", name), Name: name})
	fmt.Fprintf(stdout, "the active profile is %q\n", name)
	if cfg.Network.Mode != "proxy" {
		fmt.Fprintf(stdout, "the mode is still %q: `crewflow network mode proxy` to go through it\n", cfg.Network.Mode)
	}
	return exitOK
}

// runNetworkRemove is `crewflow network proxy remove <name>`: the profile is taken out of
// the file of the project, and the credentials of it are not — those are in the store of
// the machine under its name, and a profile that is added back in an hour wants the same
// proxy to answer. `crewflow network proxy credentials <name> remove` is the other half of
// this (§7d, §7e).
func runNetworkRemove(args []string, stdout, stderr io.Writer) int {
	flags := networkFlags("network proxy remove", stderr)
	configPath := flags.String("config", defaultConfigPath, "path to crewflow.toml")
	name, rest := takeFirst(args)
	if err := flags.Parse(rest); err != nil {
		return exitUsage
	}
	if name == "" || flags.NArg() > 0 {
		fmt.Fprintf(stderr, "crewflow network proxy remove: the name of the profile, "+
			"as in `crewflow network proxy remove home`\n\n")
		usage(stderr)
		return exitUsage
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return networkFailed(stderr, err)
	}
	profile := cfg.Network.Proxies[name]
	if err := network.Remove(*configPath, name); err != nil {
		return networkFailed(stderr, err)
	}
	sayProfileChanged(stderr, "remove", name, profile)
	fmt.Fprintf(stdout, "the profile %q is out of %s\n", name, *configPath)
	if profile.Credentials == "secret-store" {
		fmt.Fprintf(stdout, "its credentials are still in the store of this machine, as %q: "+
			"`crewflow network proxy credentials %s remove`\n", secret.ProxyKey(name), name)
	}
	return exitOK
}

// runNetworkCredentials is `crewflow network proxy credentials <name> set|remove|status`:
// the value of a login and a password of a profile, which lives in the store of secrets of
// the machine under the name of the profile and not in the file of the project. None of
// these three answers shows the value, and `status` answers whether there is one at all
// without reading it (§7e).
//
// The value comes from a file, as the key of an App does: a secret on the line of a
// command is a secret in the list of processes of the machine and in the history of a
// shell.
func runNetworkCredentials(out *secret.Out, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintf(stderr, "crewflow network proxy credentials: set, remove or status?\n\n")
		usage(stderr)
		return exitUsage
	}
	// The name of the profile comes first, as a person writes it, and what to do with its
	// credentials after it: `crewflow network proxy credentials work set -file <path>`.
	name, rest := takeFirst(args)
	operation, rest := takeFirst(rest)
	flags := networkFlags("network proxy credentials "+operation, stderr)
	configPath := flags.String("config", defaultConfigPath, "path to crewflow.toml")
	from := flags.String("file", "", "the file the credentials are read from")
	if err := flags.Parse(rest); err != nil {
		return exitUsage
	}
	if name == "" || operation == "" || flags.NArg() > 0 {
		fmt.Fprintf(stderr, "crewflow network proxy credentials: the name of the profile and what to do, "+
			"as in `crewflow network proxy credentials home status`\n\n")
		usage(stderr)
		return exitUsage
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return networkFailed(stderr, err)
	}
	profile, known := cfg.Network.Proxies[name]
	if !known {
		return networkFailed(stderr, fmt.Errorf("the profile %q is in no [network.proxies] of %s", name, *configPath))
	}
	route := network.Route{Name: name, Profile: profile}
	store := storeOfTheNetwork(stderr)
	switch operation {
	case "status":
		configured, err := network.Configured(store, route)
		if err != nil {
			return networkFailed(stderr, err)
		}
		fmt.Fprintf(stdout, "%s: credentials_configured: %t\n", name, configured)
		return exitOK
	case "remove":
		if err := takeAway(store, name); err != nil {
			return networkFailed(stderr, err)
		}
		sayProfileChanged(stderr, "credentials-remove", name, profile)
		fmt.Fprintf(stdout, "the credentials of %q are out of the store of this machine, as %q\n", name, secret.ProxyKey(name))
		if profile.Credentials == "secret-store" {
			fmt.Fprintf(stdout, "the profile still says credentials = \"secret-store\": "+
				"`crewflow network proxy edit %s --credentials none`\n", name)
		}
		return exitOK
	case "set":
		if profile.Credentials != "secret-store" {
			return networkFailed(stderr, fmt.Errorf("the profile %q says credentials = %q: "+
				"change it with `crewflow network proxy edit %s --credentials secret-store` first",
				name, profile.Credentials, name))
		}
		if *from == "" {
			return networkFailed(stderr, fmt.Errorf("the file the credentials are read from, "+
				"as in `crewflow network proxy credentials %s set -file ~/.proxy-password`", name))
		}
		value, err := os.ReadFile(*from)
		if err != nil {
			return networkFailed(stderr, fmt.Errorf("read %s: %w", *from, err))
		}
		login, password, pair := strings.Cut(strings.TrimSpace(string(value)), ":")
		if !pair || login == "" || password == "" {
			return networkFailed(stderr, fmt.Errorf("%s: a login and a password separated by a colon, "+
				"as in `ann:s3cret`, and nothing else", *from))
		}
		if err := store.Set(secret.Service, secret.ProxyKey(name), []byte(login+":"+password)); err != nil {
			return networkFailed(stderr, fmt.Errorf("put the credentials of %q away: %w", name, err))
		}
		sayProfileChanged(stderr, "credentials-set", name, profile)
		fmt.Fprintf(stdout, "the credentials of %q are in the store of this machine, as %q\n", name, secret.ProxyKey(name))
		fmt.Fprintf(stdout, "delete %s: it holds a password, and nothing of it belongs in a file of yours\n", *from)
		return exitOK
	default:
		fmt.Fprintf(stderr, "crewflow network proxy credentials: %q is set, remove or status\n\n", operation)
		usage(stderr)
		return exitUsage
	}
}

// takeAway is the deletion of the credentials of a profile from the store of the machine.
// It is a different operation from taking the profile out of the file of the project, and
// the two do not do each other's work (§7e).
func takeAway(store secret.Store, name string) error {
	if store == nil {
		return fmt.Errorf("this machine has no store of secrets, and the keychain of macOS is the store crewflow has")
	}
	away, ok := store.(interface {
		Delete(service, account string) error
	})
	if !ok {
		return fmt.Errorf("the store of this machine cannot take a secret away: "+
			"delete the item %q of it by hand", secret.ProxyKey(name))
	}
	return away.Delete(secret.Service, secret.ProxyKey(name))
}

// runNetworkTest is `crewflow network proxy test [name]`: what one route can do, one
// capability at a time, with the time each check took. Without a name it is the route of
// the project; with one it is the route through that profile, active or not — a profile
// can be prepared before anybody made it the active one (§7d).
//
// This is the one command that really connects through the profile, and it is therefore
// the one place where the credentials of it are read. It shows what came of that and never
// the value itself (§7e).
func runNetworkTest(out *secret.Out, args []string, stdout, stderr *secret.Writer) int {
	flags := networkFlags("network proxy test", stderr)
	configPath := flags.String("config", defaultConfigPath, "path to crewflow.toml")
	asJSON := flags.Bool("json", false, "print the capabilities as JSON, for the orchestrator")
	name, rest := takeFirst(args)
	if err := flags.Parse(rest); err != nil {
		return exitUsage
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "crewflow network proxy test: unexpected argument %q\n\n", flags.Arg(0))
		usage(stderr)
		return exitUsage
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return networkFailed(stderr, err)
	}
	var route network.Route
	if name == "" {
		route, err = network.Choose(cfg, "")
	} else {
		route, err = network.Through(cfg, name)
	}
	if err != nil {
		return networkFailed(stderr, err)
	}
	ctx, stop := stoppedBy()
	defer stop()
	states, err := checksOfARoute(ctx, out, cfg, route, storeOfTheNetwork(stderr))
	if err != nil {
		return networkFailed(stderr, err)
	}
	if *asJSON {
		answer := struct {
			Route        network.Route   `json:"route"`
			Capabilities []network.State `json:"capabilities"`
		}{Route: route, Capabilities: states}
		if err := printJSON(stdout, secret.DocumentNetworkProxyTest, answer); err != nil {
			return networkFailed(stderr, err)
		}
	} else {
		if route.Direct {
			fmt.Fprintf(stdout, "route: %s\n", route.Why)
		} else {
			fmt.Fprintf(stdout, "route: %s (%s://%s:%d)\n", route.Why, route.Profile.Type, route.Profile.Host, route.Profile.Port)
		}
		for _, state := range states {
			fmt.Fprintf(stdout, "%-15s %-11s %s%s\n", state.Capability, state.Result, reasonOf(state), timingOf(state))
		}
	}
	for _, state := range states {
		if state.Result == network.StateUnavailable {
			return exitFailure
		}
	}
	return exitOK
}

// reasonOf is the closed reason of a capability beside its state, for a person to read:
// `unavailable` without the reason is a failure of the route with nothing said about which
// one it is, and `unknown` without it is a check that learned nothing with no word to look
// for in a log (docs/DESIGN.md §7d).
func reasonOf(state network.State) string {
	if state.Reason == "" {
		return state.Detail
	}
	if state.Detail == "" {
		return state.Reason + ": nothing else to say"
	}
	return state.Reason + ": " + state.Detail
}

// timingOf is how long a check took, as a person reads a time next to what it found.
func timingOf(state network.State) string {
	if state.Duration == "" {
		return ""
	}
	return " (" + state.Duration + ")"
}

// sayProfileChanged is the event of the route of a project, as a journal and a program read
// it: what was changed, of which profile, and when. It holds the address of a profile and
// never a credential of it — the value of the store is not in an event, an event that
// carried it would be a copy of a password in a file kept for ever (§7e, §7i).
func sayProfileChanged(stderr io.Writer, action, name string, profile config.Proxy) {
	fmt.Fprintf(stderr, "crewflow: event %s action=%s profile=%s type=%s host=%s port=%d at=%s\n",
		network.EventProfileChanged, action, name, profile.Type, profile.Host, profile.Port,
		clockOfTheNetwork().Format(time.RFC3339))
}

// sayRouteChanged is the same event about the route itself — the mode and the active
// profile — with the two words a report reads it by and nothing secret in it.
func sayRouteChanged(stderr io.Writer, action string, route network.Route) {
	fmt.Fprintf(stderr, "crewflow: event %s action=%s %s at=%s\n",
		network.EventProfileChanged, action, route.Why, clockOfTheNetwork().Format(time.RFC3339))
}

// storeOfTheNetwork is the store of the secrets of the machine behind the wait of the
// keychain of macOS, saying what it is about to wait for to the notices of the command.
func storeOfTheNetwork(stderr io.Writer) secret.Store {
	if secretsOfTheNetwork == nil {
		return nil
	}
	return secret.Waited(secretsOfTheNetwork(), secret.NewNotices(stderr), waitForTheKeychain)
}

// networkFlags are the flags of a command of the network, and the usage with them.
func networkFlags(subcommand string, stderr io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet(subcommand, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { usage(stderr) }
	return flags
}

// networkFailed is what a command of the network says when it could not do what it was
// told.
func networkFailed(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "crewflow network: %v\n", err)
	return exitFailure
}
