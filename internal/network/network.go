// Package network says how the programs of crewflow reach the network: which of the
// named proxy profiles of a project a request goes through, what the child processes
// of crewflow are told about it, and what a check of one route found.
//
// A proxy is a resource like any other: its address is different on every machine and
// changes when the owner moves, so a project names the profiles ("home", "work") and
// the commands change them. Only the declaration is in the file of the project —
// whether a profile answers today is asked of the machine, one capability at a time,
// and it lives in that answer (docs/DESIGN.md §7d).
package network

import (
	"errors"
	"fmt"
	"maps"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/secret"
)

// EventProfileChanged is the event of the route of a project: a profile was added,
// changed, made active or taken away, and a person or a program reading a log of the
// project asks after the route by its name. It carries the name of the profile, what it
// speaks, where it listens and nothing else: a credential of a route is read for a
// connection and never written down, and an event that held one would be a copy of a
// password in a file kept for ever (docs/DESIGN.md §7e, §7i).
const EventProfileChanged = "PROXY_PROFILE_CHANGED"

// ErrPattern is the refusal of a rule of `[network] no_proxy` that crewflow cannot
// read one way: a rule a program of the machine would read as one thing and crewflow
// as another is a rule that sends traffic where nobody said, and it is refused rather
// than guessed at.
var ErrPattern = errors.New("a rule of [network] no_proxy must be a name, a domain suffix, an address or a network")

// Route is how one request of a program of crewflow goes out: straight to its target,
// or through one of the named profiles of the project.
type Route struct {
	// Direct says that the request goes out without a proxy. It is the only true thing
	// in the mode `direct` and in the mode `proxy` for a target of `[network] no_proxy`.
	Direct bool `json:"direct"`
	// Profile is the profile the request goes through, and Name is what the project
	// calls it. Both are empty in a direct route.
	Profile config.Proxy `json:"-"`
	Name    string       `json:"profile,omitempty"`
	// Fallback is the profile one more attempt of this request may go through, and it is
	// empty in every mode but `fallback`. It is a name and not a route: the second attempt
	// is made for the one request that failed and is not kept afterwards, so a route that
	// outlived its request would be a route nobody chose (docs/DESIGN.md §7d).
	Fallback string `json:"fallback,omitempty"`
	// Why is the rule of the file the route came out of, in the words of the file: a
	// report that says "through home" without saying why is a report a person has to
	// read the file to understand.
	Why string `json:"why"`
}

// URL is the address of the profile as a program of the machine is told it: the scheme
// the type names, the host, the port, and the credentials only when they were read for
// a connection that is really made.
func (r Route) URL(credentials string) (*url.URL, error) {
	if r.Direct || r.Profile.Host == "" {
		return nil, nil
	}
	address := &url.URL{
		Scheme: r.Profile.Type,
		Host:   net.JoinHostPort(r.Profile.Host, strconv.Itoa(r.Profile.Port)),
	}
	if credentials != "" {
		login, password, found := strings.Cut(credentials, ":")
		if !found || login == "" || password == "" {
			// The value in the store is not a pair, and a URL with half of it is an
			// address that says the wrong thing to whoever reads it.
			return nil, fmt.Errorf("the credentials of the profile %q are not a login and a password, as %q", r.Name, secret.Redacted)
		}
		address.User = url.UserPassword(login, password)
	}
	return address, nil
}

// Address is the profile as the environment of a child process names it, with the
// credentials when they were read for it.
func (r Route) Address(credentials string) (string, error) {
	address, err := r.URL(credentials)
	if err != nil || address == nil {
		return "", err
	}
	return address.String(), nil
}

// Choose is the route of a request to target in the configuration of the project, in
// the order the owner decided on 02.10.2026: a target of `no_proxy` goes straight out
// whatever the mode says, a straight mode goes straight out, the mode `proxy` goes
// through the active profile, and the mode `fallback` goes straight out and names the
// profile one more attempt of the same request may go through (docs/DESIGN.md §7d).
//
// A route of the mode `fallback` is a straight one: the executor of a run, the git of the
// run and every program crewflow starts afterwards go out as they always did, and only
// the request that could not reach the network is made again through the profile
// ([Fallback].Once).
//
// The target is what the request names: a host, a host with a port, or a whole address.
// It may be empty — the route of a program that reaches nothing in particular.
func Choose(cfg config.Config, target string) (Route, error) {
	if target != "" {
		excluded, err := NoProxy(cfg.Network.NoProxy, target)
		if err != nil {
			return Route{}, fmt.Errorf("network.no_proxy: %w", err)
		}
		if excluded {
			return Route{Direct: true, Why: "the target is in [network] no_proxy"}, nil
		}
	}
	switch cfg.Network.Mode {
	case "", "direct":
		return Route{Direct: true, Why: `network.mode = "direct"`}, nil
	case "proxy":
		if cfg.Network.ActiveProxy == "" {
			return Route{}, errors.New("network.mode = \"proxy\" and no network.active_proxy: " +
				"choose a profile with `crewflow network proxy use <name>`")
		}
		return Through(cfg, cfg.Network.ActiveProxy)
	case "fallback":
		// The mode without a profile is a promise of a second road there is none of. It is
		// refused here and not read as a project that happens to go straight out, because a
		// run whose fallback was silently a straight route is a run that failed and told
		// nobody that the second route was never there (§7d, §7e).
		if cfg.Network.ActiveProxy == "" {
			return Route{}, errors.New("network.mode = \"fallback\" and no network.active_proxy: " +
				"choose a profile with `crewflow network proxy use <name>`")
		}
		if _, err := Through(cfg, cfg.Network.ActiveProxy); err != nil {
			return Route{}, err
		}
		return Route{Direct: true, Fallback: cfg.Network.ActiveProxy,
			Why: `network.mode = "fallback" and one attempt through network.active_proxy`}, nil
	default:
		return Route{}, fmt.Errorf("network.mode: unknown value %q", cfg.Network.Mode)
	}
}

// Through is the route through the profile with this name, whether it is the active one
// or not: `crewflow network proxy test home` asks about a profile before anybody made it
// the active one, and a profile that is prepared in advance is prepared for that.
func Through(cfg config.Config, name string) (Route, error) {
	profile, known := cfg.Network.Proxies[name]
	if !known {
		return Route{}, fmt.Errorf("the profile %q is in no [network.proxies] of the project, "+
			"the profiles of the project are %s", name, Names(cfg))
	}
	why := fmt.Sprintf("the profile %q", name)
	if cfg.Network.ActiveProxy == name {
		why = fmt.Sprintf("network.active_proxy = %q", name)
	}
	return Route{Name: name, Profile: profile, Why: why}, nil
}

// Names are the profiles of the project, in the order a person reads them.
func Names(cfg config.Config) string {
	names := slices.Sorted(maps.Keys(cfg.Network.Proxies))
	if len(names) == 0 {
		return "none yet"
	}
	return strings.Join(names, ", ")
}

// Environment is what a child process of crewflow is told about the route, and nothing
// else: four names that every program of the machine already reads, and no setting
// anywhere outside the process. A straight route gets nothing — there is no proxy to
// point at, and a variable left over from the shell of a person is a route nobody chose
// (docs/DESIGN.md §7d).
//
// The credentials are in the environment only when they were read for a connection that
// is really made. A command that shows the route does not read them and passes "".
func (r Route) Environment(noProxy []string, credentials string) ([]string, error) {
	if r.Direct {
		return nil, nil
	}
	address, err := r.Address(credentials)
	if err != nil {
		return nil, err
	}
	environment := []string{
		"HTTP_PROXY=" + address,
		"HTTPS_PROXY=" + address,
		"ALL_PROXY=" + address,
	}
	if len(noProxy) > 0 {
		environment = append(environment, "NO_PROXY="+strings.Join(noProxy, ","))
	}
	return environment, nil
}

// EnvironmentOf is the route of a request to target and what a child process is told
// about it, in one call.
func EnvironmentOf(cfg config.Config, target, credentials string) ([]string, error) {
	route, err := Choose(cfg, target)
	if err != nil {
		return nil, err
	}
	return route.Environment(cfg.Network.NoProxy, credentials)
}

// Secrets are the values of this route that must not reach a file crewflow writes: the
// credentials as they were read out of the store, the password on its own, and the pair as
// the address a program is given writes it. The last one is not the same text as the first:
// an address writes the password percent-encoded, and the value an agent prints out of its
// own environment is the address and not the store — a value taken out of a text in one
// spelling and shown in another is a secret in every file a run writes (docs/DESIGN.md §7d,
// §7e).
//
// The address of the profile is not among them: it is in every report of a check of a
// route and in every event of a change of it, and a person who cannot see where the
// traffic of a project goes cannot say that it goes where the owner of the project said.
func (r Route) Secrets(credentials string) []string {
	if r.Direct || credentials == "" {
		return nil
	}
	values := []string{credentials}
	if _, password, found := strings.Cut(credentials, ":"); found && password != "" {
		values = append(values, password)
	}
	// The pair as the address writes it is worked out of that address and not out of a
	// second spelling of the same value: the address is the only text a program is given,
	// and it is that text a program can print.
	if address, err := r.URL(credentials); err == nil && address != nil && address.User != nil {
		if written := address.User.String(); written != "" {
			values = append(values, written)
		}
	}
	return values
}

// Terms are the two durations of the network of a project: how long a check of a route
// waits for an answer, and how long that answer is a fact about the machine.
func Terms(cfg config.Config) (connect, validFor time.Duration, err error) {
	connect, err = time.ParseDuration(cfg.Network.ConnectTimeout)
	if err != nil {
		return 0, 0, fmt.Errorf("network.connect_timeout: %w", err)
	}
	validFor, err = time.ParseDuration(cfg.Network.TestValidFor)
	if err != nil {
		return 0, 0, fmt.Errorf("network.test_valid_for: %w", err)
	}
	return connect, validFor, nil
}

// NoProxy reports whether target goes out straight whatever the mode of the project
// says. The rules are the rules of the standard NO_PROXY, because that is what a person
// who has one on another machine writes without thinking: an exact name, a domain
// suffix with or without its leading dot, an address, a network in CIDR, a `*` for
// everything, and an optional port after any of them.
//
// A rule with a scheme, a path, credentials or a `*` in the middle of a name is refused
// with [ErrPattern]: those rules mean different things to different programs, and a
// route chosen by a guess is a route nobody chose.
func NoProxy(rules []string, target string) (bool, error) {
	host, port, err := HostOf(target)
	if err != nil {
		return false, err
	}
	for i, rule := range rules {
		excluded, err := excludedBy(rule, host, port)
		if err != nil {
			return false, fmt.Errorf("network.no_proxy[%d]: %w", i, err)
		}
		if excluded {
			return true, nil
		}
	}
	return false, nil
}

// HostOf is the host and the port a request names, whatever it was written as: a host,
// a host with a port, or a whole address. A target crewflow cannot read is a refusal
// and not a guess: `no_proxy` is checked before the route is chosen, and a target it
// misread is a target that goes the wrong way.
func HostOf(target string) (host, port string, err error) {
	if index := strings.Index(target, "://"); index >= 0 {
		parsed, err := url.Parse(target)
		if err != nil || parsed.Host == "" {
			return "", "", fmt.Errorf("the target %q is not an address crewflow can read: %w", target, err)
		}
		host, port = parsed.Hostname(), parsed.Port()
	} else if name, given, split := strings.Cut(target, ":"); split && !isAddress(target) {
		host, port = name, given
	} else {
		host = target
	}
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" {
		return "", "", fmt.Errorf("the target %q names no host", target)
	}
	if _, err := netip.ParseAddr(host); err != nil && !validName(host) {
		return "", "", fmt.Errorf("the target %q is not a host crewflow can read", target)
	}
	return host, port, nil
}

// isAddress reports whether the target is an IPv6 address, where every colon belongs to
// the address itself and the last one is a port of nothing.
func isAddress(target string) bool {
	address, err := netip.ParseAddr(target)
	return err == nil && address.Is6() || strings.Count(target, ":") > 1
}

// validName is a host name crewflow is willing to compare: letters, digits, dots and
// dashes, and nothing that could mean something else in a rule of `no_proxy`.
func validName(host string) bool {
	for _, r := range host {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
		default:
			return false
		}
	}
	return host != "" && !strings.HasPrefix(host, ".") && !strings.HasSuffix(host, ".")
}

// excludedBy is whether one rule sends the target out straight.
func excludedBy(rule, host, port string) (bool, error) {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return false, fmt.Errorf("%w, got an empty one: it is a target that goes out straight", ErrPattern)
	}
	// A network is read first: it is the one rule with a slash in it, and a slash in
	// anything else is a path and not a target.
	if network, err := netip.ParsePrefix(rule); err == nil {
		address, err := netip.ParseAddr(host)
		if err != nil {
			return false, nil
		}
		return network.Contains(address), nil
	}
	if strings.Contains(rule, "://") {
		return false, fmt.Errorf("%w, got %q: a scheme is what a target does not carry, it is what the proxy is", ErrPattern, rule)
	}
	if strings.ContainsAny(rule, "/@") {
		return false, fmt.Errorf("%w, got %q: a path and credentials are not targets", ErrPattern, rule)
	}
	if rule == "*" {
		return true, nil
	}
	name, wanted := rule, port
	if prefix, given, found := strings.Cut(rule, ":"); found {
		name, wanted = prefix, given
		if _, err := strconv.Atoi(wanted); err != nil {
			return false, fmt.Errorf("%w, got %q: %q is not a port", ErrPattern, rule, wanted)
		}
	}
	if wanted != "" && wanted != port {
		return false, nil
	}
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	name = strings.TrimPrefix(name, "*.")
	name = strings.TrimPrefix(name, ".")
	if name == "" {
		return false, fmt.Errorf("%w, got %q", ErrPattern, rule)
	}
	if strings.Contains(name, "*") {
		return false, fmt.Errorf("%w, got %q: a star is a rule of its own or right before a domain suffix", ErrPattern, rule)
	}
	if !validName(name) {
		return false, fmt.Errorf("%w, got %q", ErrPattern, rule)
	}
	if address, err := netip.ParseAddr(host); err == nil {
		return address.String() == name, nil
	}
	return host == name || strings.HasSuffix(host, "."+name), nil
}
