package doctor

import (
	"context"
	"fmt"
	"strings"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/network"
	"github.com/naghuale/crewflow/internal/secret"
)

// networkCheck is the one line of a report that is about the route of the network, and
// it says what the project declared and nothing more: whether a profile answers today is
// asked of the machine by `crewflow doctor network` and by `crewflow network proxy test`,
// and a report that went to the network on its own would make every check of a machine
// wait for a route (§7d).
const networkCheck = "network route"

// network is the route of the project as the file declares it: how a request of a program
// of crewflow goes out, which profile it goes through, whether the credentials of that
// profile are in the store of the machine, and where a person checks whether the profile
// answers (docs/DESIGN.md §7d, §7e).
//
// The profile is named and not shown working: "a profile is there" and "a profile works"
// are two different facts, and only the second one is asked of the machine. The
// credentials are a yes or a no and never a value — the store of the machine is asked
// whether it holds something under the name of the profile and nothing more (§7e).
func (c *checker) network(cfg config.Config) {
	detail, hint, status := describedRoute(cfg, c.env.Secrets)
	c.add(Check{Name: networkCheck, Status: status, Detail: detail, Hint: hint})
}

// describedRoute is what the file of the project declares about its network, as a line of
// a report and the hint under it.
func describedRoute(cfg config.Config, store secret.Store) (detail, hint string, status Status) {
	route, err := network.Choose(cfg, "")
	if err != nil {
		return err.Error(), "the route of the project is in [network] of the file of the project", Fail
	}
	if route.Direct {
		return fmt.Sprintf("%s, nothing of a proxy is added to the programs of crewflow", route.Why),
			"", OK
	}
	details := []string{fmt.Sprintf("%s: %s", route.Why, route.Profile.Type+"://"+addressOf(route))}
	if route.Profile.Credentials == "secret-store" {
		configured, err := network.Configured(store, route)
		switch {
		case err != nil:
			details = append(details, "credentials: the store of the machine could not be asked")
			status = Warn
		case configured:
			details = append(details, fmt.Sprintf("credentials: configured, as %q", secret.ProxyKey(route.Name)))
		default:
			details = append(details, fmt.Sprintf("credentials: not configured, as %q", secret.ProxyKey(route.Name)))
			status = Warn
		}
	}
	return joinDetails(details), "check that it answers with `crewflow doctor network`", status
}

// addressOf is where a profile listens, in the words a person would say it.
func addressOf(route network.Route) string {
	if route.Profile.Port == 0 {
		return route.Profile.Host
	}
	return fmt.Sprintf("%s:%d", route.Profile.Host, route.Profile.Port)
}

// joinDetails is the line of a report out of the parts of it, so that the name of a
// profile, where it listens and whether its credentials are there are one line.
func joinDetails(parts []string) string {
	return strings.Join(parts, "; ")
}

// Network is what `crewflow doctor network` answers: the route of the project and what
// each of its capabilities came out as, one by one, with the time each check took. It
// asks the machine and is therefore a check of its own, apart from `crewflow doctor`:
// a report of readiness must not wait for a proxy of a project (§7d).
func Network(ctx context.Context, env Env, cfg config.Config) []Check {
	connect, validFor, err := network.Terms(cfg)
	if err != nil {
		return []Check{{Name: networkCheck, Status: Fail, Detail: err.Error()}}
	}
	route, err := network.Choose(cfg, "")
	if err != nil {
		return []Check{{Name: networkCheck, Status: Fail, Detail: err.Error()}}
	}
	machine := network.Factory(cfg)(network.System(route, cfg.Network.NoProxy, env.Secrets, env.Now, connect, validFor))
	machine.Git = env.Git
	if env.NetworkAPI != "" {
		machine.APIURL = env.NetworkAPI
	}
	states, err := network.Check(ctx, machine, route, cfg.Network.NoProxy)
	if err != nil {
		return []Check{{Name: networkCheck, Status: Fail, Detail: err.Error()}}
	}
	checks := make([]Check, 0, len(states)+1)
	checks = append(checks, routeCheck(route))
	for _, state := range states {
		checks = append(checks, capabilityCheck(state))
	}
	return checks
}

// routeCheck is the route the capabilities below were checked through: a report of what a
// route can do without saying which route it was is a report nobody can act on.
func routeCheck(route network.Route) Check {
	if route.Direct {
		return Check{
			Name:   networkCheck,
			Status: OK,
			Detail: fmt.Sprintf("%s: every capability is asked straight out", route.Why),
		}
	}
	return Check{
		Name:   networkCheck,
		Status: OK,
		Detail: fmt.Sprintf("%s: %s://%s", route.Why, route.Profile.Type, addressOf(route)),
	}
}

// capabilityCheck is one capability and what came of it. A capability nobody checked is a
// warning and not a failure: nothing was learned, and a check that could not be made is
// not a check that failed (§7d).
func capabilityCheck(state network.State) Check {
	check := Check{Name: "network " + state.Capability, Detail: state.Detail}
	switch state.Result {
	case network.StateAvailable:
		check.Status = OK
	case network.StateUnavailable:
		check.Status = Fail
		check.Hint = "try another profile, or `crewflow network mode direct`, and check again with `crewflow network proxy test`"
	default:
		check.Status = Warn
	}
	if state.Duration != "" {
		check.Detail += " (" + state.Duration + ")"
	}
	return check
}
