// Package config reads and checks crewflow.toml, the one file in which a project
// tells crewflow how to work with it (docs/DESIGN.md §5).
package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config is a whole crewflow.toml: which project it is, which agent runs a task
// in which worktree, and which commands must pass before a commit is reviewed.
type Config struct {
	Project      Project      `toml:"project"`
	Forge        Forge        `toml:"forge"`
	Tracker      Tracker      `toml:"tracker"`
	Identity     Identity     `toml:"identity"`
	Orchestrator Orchestrator `toml:"orchestrator"`
	Executor     Executor     `toml:"executor"`
	Access       Access       `toml:"access"`
	Worktrees    Worktrees    `toml:"worktrees"`
	Network      Network      `toml:"network"`
	Gates        []Gate       `toml:"gates"`
	CI           CI           `toml:"ci"`
	Merge        Merge        `toml:"merge"`
	Acceptance   Acceptance   `toml:"acceptance"`
	Parallel     Parallel     `toml:"parallel"`
	Tasks        Tasks        `toml:"tasks"`
	Attention    Attention    `toml:"attention"`
	Requirements Requirements `toml:"requirements"`
	Isolation    Isolation    `toml:"isolation"`
	Capabilities []Capability `toml:"capabilities"`
}

// Project is the repository crewflow works on.
type Project struct {
	// Repo is the GitHub repository as "owner/name".
	Repo string `toml:"repo"`
	// DefaultBranch is the branch a task starts from and merges into.
	DefaultBranch string `toml:"default_branch"`
	// Language is the language of tasks, reviews and messages to the human.
	Language string `toml:"language"`
	// CommitStyle is how the messages of the commits of this project are written, in
	// the words of the project, as in "conventional: type(scope): subject". Empty
	// leaves the executor with the rule that holds in every repository: the style of
	// the last commits (docs/DESIGN.md §5).
	CommitStyle string `toml:"commit_style"`
}

// Forge is where the code of the project is hosted and reviewed: the requests to
// change it, their head and their comments, and a merge through its API when
// git is not allowed to push (docs/DESIGN.md §7g).
type Forge struct {
	// Kind is the host: "github", "gitlab", "bitbucket", "gitea", "azure" or
	// "none" for a project that is not hosted at all.
	Kind string `toml:"kind"`
	// Host is the server of the project's own host, such as
	// "gitlab.company.com". Empty is the public one.
	Host string `toml:"host"`
}

// Tracker is where the tasks of the project come from. By default they are the
// issues of the host, which is why this is a role of its own: they may also live
// in Jira, in Linear or in files of the repository (docs/DESIGN.md §7g).
type Tracker struct {
	// Kind is "forge", "jira", "linear" or "files".
	Kind string `toml:"kind"`
	// Project is the key of the project in the tracker, for a tracker in which the
	// repository is not the key.
	Project string `toml:"project"`
}

// Identity is whose name the executor of a task works under (docs/DESIGN.md §7i).
//
// The mode is the same word for every host — "owner" or "bot" — and how a bot of a
// host is written down is the business of the adapter of that host, in a table of its
// own: a GitHub App for GitHub, a project token for GitLab, an access token of a user
// for Bitbucket. A project that asks for a mode of a host crewflow has no bot for is
// told so when its file is read, and not in the middle of a task.
type Identity struct {
	// Mode is "owner" or "bot": the login of the person, which is what crewflow has
	// always done and what the powers of the executor are the powers of, or an
	// account of the host of its own, which is what separates them (§7i).
	Mode string `toml:"mode"`
	// GitHubApp is the app the executor of a project on GitHub works as, and the
	// only bot crewflow has. It is empty in the mode of the owner.
	GitHubApp GitHubApp `toml:"github_app"`
}

// GitHubApp is the GitHub App of a project: what identifies it to GitHub, and
// nothing else — the key of it lives in the keychain of the machine and is never in
// the file of a project (docs/DESIGN.md §7e, §7i).
type GitHubApp struct {
	// AppID is the number of the app, as it stands in the settings of GitHub. The
	// mode of the bot cannot be set up without it.
	AppID int64 `toml:"app_id"`
	// InstallationID is the number of the installation of the app on the repository
	// of the project. It is optional: empty is not a mistake, and the installation
	// is found through the repository the first time a run asks for a token.
	InstallationID int64 `toml:"installation_id"`
}

// The modes of the orchestrator, the words the file of a project is written in and the
// words the core of crewflow knows: the login of the person, which the owner works under
// as well, or an account of the host of the project of its own (docs/DESIGN.md §7i).
const (
	// ModeShared is the login of the person for both of them, which is what a project
	// gets without saying anything and what crewflow has always done.
	ModeShared = "shared"
	// ModeSeparate is an account of the host of the project of its own, which is what
	// lets a gate tell a decision of the owner from a record of the orchestrator.
	ModeSeparate = "separate"
)

// Orchestrator is whose name the orchestrator of a project works under, which is a
// question of its own: the record of a review and the push of a merge are the
// business of the orchestrator, and whether the gate may tell them from the records
// of the owner depends on it (docs/DESIGN.md §7i).
//
// The words are the same for every host — "shared" or "separate" — and how an
// account of a host is written down is the business of the adapter of that host, in
// a table of its own, exactly as for the executor.
type Orchestrator struct {
	// Mode is "shared" or "separate": the login of the person, which is the same one
	// the owner works under and which therefore cannot be told from it, or an account
	// of the host of the project of its own, which is what separates them (§7i).
	Mode string `toml:"mode"`
	// GitHubApp is the app the orchestrator of a project on GitHub works as, and the
	// second app of a project whose executor already has one. It is empty in the mode
	// of the shared login.
	GitHubApp GitHubApp `toml:"github_app"`
}

// Executor is the agent crewflow runs a task with, plus the ones to try in turn
// when it turns out to be unavailable (docs/DESIGN.md §7b).
type Executor struct {
	ExecutorSpec
	// ResumeWithin is how long the point a run of the task stands at while it waits
	// for a decision of a person stays a point to go on from with `crewflow task
	// resume`, as a duration such as "24h". It is here and not in the spec of the
	// agent because it is not a property of the agent: it is the term of the
	// checkpoint of a run, whatever agent runs the next attempt, and a point older
	// than this is refused rather than guessed at (docs/DESIGN.md §7i).
	ResumeWithin string `toml:"resume_within"`
	// Fallback lists the executors to try, in order, after the primary one.
	Fallback []ExecutorSpec `toml:"fallback"`
}

// ExecutorSpec is one agent: the command line to run, the model to ask for and
// the time limit of a run.
type ExecutorSpec struct {
	// Command is the agent's command line, where crewflow substitutes the
	// {worktree}, {prompt} and {model} placeholders. It has no default:
	// crewflow knows no agent, the project says which one it uses.
	Command []string `toml:"command"`
	// Model is the model to ask for. Empty leaves the choice to the agent.
	Model string `toml:"model"`
	// ModelFlag is how the agent takes the model, for example
	// ["--model", "{model}"]. It is added only when Model is set.
	ModelFlag []string `toml:"model_flag"`
	// Timeout is how long a run may take, as a duration such as "90m".
	Timeout string `toml:"timeout"`
	// StallAfter is how long a run may go without a sign of life before it is marked
	// as standing: the last line the executor wrote, or the last step of crewflow
	// itself. A run that has shown nothing for that long is a run a person has to
	// look at, and the mark says so much as the length of the silence
	// (docs/DESIGN.md §6, §7a).
	StallAfter string `toml:"stall_after"`
}

// Access is what the executor of a project may read outside the worktree of a task,
// and how crewflow finds out where those folders are (docs/DESIGN.md §7d).
type Access struct {
	// ReadFrom are the commands that print the folders the project keeps its
	// dependencies in, one path per line. A project says how to find them and not
	// where they are: the paths are different on every machine, and the tools that
	// know them already answer.
	ReadFrom [][]string `toml:"read_from"`
	// Read are the paths that may be read wherever they are, such as a system
	// library or an SDK. A leading "~/" stands for the home of the person.
	Read []string `toml:"read"`
}

// Worktrees says where the worktree of a task is created.
type Worktrees struct {
	// Root is the directory that holds one worktree per task. It may start with
	// "~/" and contain "{repo}".
	Root string `toml:"root"`
}

// Network is how the programs of crewflow reach the network: whether directly or
// through one of the named proxy profiles, and what goes to the network without a
// proxy even when the mode says otherwise (docs/DESIGN.md §7d).
//
// Only the declared configuration is in the file of a project. Whether a profile
// answers today is not: it is asked of the machine, one capability at a time, and
// what came of it lives in that answer (`crewflow doctor network`, `crewflow
// network proxy test`) — a file that stored the answer would be a file that says
// the route works on a machine where nobody asked.
type Network struct {
	// Mode is how a request of a program of crewflow goes out: "direct" for a
	// straight route, "proxy" for the active profile, "fallback" for a straight
	// route and one attempt through the profile. The last is described and not
	// written yet (#145).
	Mode string `toml:"mode"`
	// ActiveProxy is the name of the profile requests go through in the mode
	// "proxy". It names a profile of the table below and never a host: the address
	// of a proxy is different on every machine, and a file with an address in it
	// is a file of one machine.
	ActiveProxy string `toml:"active_proxy"`
	// NoProxy are the targets that go out directly whatever the mode says, with
	// the rules of the standard NO_PROXY: an exact name, a domain suffix, an
	// address and a network in CIDR. A rule crewflow cannot read one way is
	// refused rather than guessed at.
	NoProxy []string `toml:"no_proxy"`
	// ConnectTimeout is how long a check of a route waits for an answer, as a
	// duration such as "10s". It bounds a probe of a capability and nothing else.
	ConnectTimeout string `toml:"connect_timeout"`
	// TestValidFor is how long a check of a capability stays a fact about the
	// machine, as a duration such as "1h". A check older than it is stale, and a
	// stale check is not a fact about now (docs/DESIGN.md §7d).
	TestValidFor string `toml:"test_valid_for"`
	// Proxies are the named profiles of the project, by the name a person gives
	// them: "home", "work", "office".
	Proxies map[string]Proxy `toml:"proxies"`
}

// Proxy is one named way out to the network: what speaks to it, where it listens
// and whether it takes credentials (docs/DESIGN.md §7d).
type Proxy struct {
	// Type is the protocol crewflow speaks to the proxy over: "http", "https" or
	// "socks5". Only the protocols that are really supported are named here, and
	// anything else is refused while it is added or checked — a profile of a type
	// crewflow cannot talk to is a profile that does not work.
	Type string `toml:"type"`
	// Host is the host or the address of the proxy, without a scheme and without
	// credentials: a login and a password in an address of a proxy are a secret in
	// the file of a project, and they are in the store of secrets instead.
	Host string `toml:"host"`
	// Port is the port the proxy listens on, from 1 to 65535.
	Port int `toml:"port"`
	// Credentials says where the login and the password of the profile are:
	// "none" when it takes none, "secret-store" when they are kept in the store of
	// secrets of the machine under the name of the profile.
	Credentials string `toml:"credentials"`
}

// Gate is a command of the project that has to pass on the commit.
type Gate struct {
	// Name is how a report refers to the gate.
	Name string `toml:"name"`
	// Run is the command line, which passes when it exits zero.
	Run []string `toml:"run"`
	// Exclusive marks a gate that must not run together with another task's.
	Exclusive bool `toml:"exclusive"`
}

// CI is what crewflow expects from GitHub Actions on the commit.
type CI struct {
	// Kind is where the checks run: "forge", "jenkins", "command" or "none"
	// (docs/DESIGN.md §7g).
	Kind string `toml:"kind"`
	// Required makes crewflow wait for CI and refuse a red commit.
	Required bool `toml:"required"`
	// Timeout is how long crewflow waits for CI, as a duration such as "30m".
	Timeout string `toml:"timeout"`
}

// Merge says who merges an approved pull request and how.
type Merge struct {
	// By is who merges: the orchestrator, the executor or the human.
	By string `toml:"by"`
	// Strategy is how it merges. Fast-forward is the only one crewflow does.
	Strategy string `toml:"strategy"`
	// Via is what merges: "git-push" by default, and "forge" through the API of
	// the host when main is protected and only it may merge (docs/DESIGN.md §7g).
	Via string `toml:"via"`
	// Reviewers are the logins whose record of a review counts as an approval. A
	// record of anybody else — of the executor of a run, of an agent that was given
	// the right to write — is a comment and not an approval, whatever it says
	// (docs/DESIGN.md §7h). An empty list is the owner of the repository, which is
	// what a project gets without saying anything.
	Reviewers []string `toml:"reviewers"`
	// Owners are the logins whose records are the owner's own, under the rules an
	// approval is counted by: the owner of a project looks at the result of a task
	// himself, and only his own `ACCEPTED <sha>` takes the result in; and only his
	// own `SCOPE: ACCEPTED <sha>` takes a file outside the boundaries of the task
	// into his hands (docs/DESIGN.md §7h). The list is one and the check of it is one,
	// because both of those records are decisions of a person and not the work of the
	// orchestrator: an orchestrator apart from the owner is in neither of them, however
	// well it may review (§7i). An empty list is the owner of the repository, as it is
	// for the reviewers — and the two lists are still two lists, because a review is
	// the business of the orchestrator and a decision of the owner is not.
	Owners []string `toml:"owners"`
}

// Acceptance is what the owner of the project has to see himself before a change of a
// task may go in (docs/DESIGN.md §7f, §7h).
type Acceptance struct {
	// Label is the label a task is marked with to say that its result has to be
	// accepted: until there is a record `ACCEPTED <sha>` under its change request, of
	// one of [merge] owners, for exactly the head the change stands at, the gate
	// refuses to merge it (`owner-acceptance-missing`). A task without the label is
	// merged as it always was, and nothing about it changes.
	Label string `toml:"label"`
}

// Parallel limits how many tasks run at the same time.
type Parallel struct {
	// MaxTasks is the number of runs at once; 1 means strictly one after
	// another.
	MaxTasks int `toml:"max_tasks"`
}

// Tasks is how crewflow treats a task before it starts (docs/DESIGN.md §7f).
type Tasks struct {
	// OwnerApproval is whose approval a task waits for: "all" makes every task
	// wait, "risky" only the ones a person must look at, "none" none.
	OwnerApproval string `toml:"owner_approval"`
}

// Attention is when a task of the project that wants a person is put in front of one, and
// how long that person may be waited for. The silence after which a run is called standing
// is not here: it belongs to the executor that has to answer for it, and it is
// `[executor] stall_after` (docs/DESIGN.md §6, §6a, §7a).
//
// Every value is a duration a project agrees to put up with, and every one of them is a
// promise a person is owed: a task that waits longer than `escalate_after` is escalated and
// a record of it is left under the task, and `remind_after` is what keeps that record from
// becoming a line a minute (§6a).
type Attention struct {
	// TopAfter is how long a task may wait before it goes to the top of the queue of
	// attention even where nobody can act on it yet, such as a task that waits for a
	// resource of the project.
	TopAfter string `toml:"top_after"`
	// EscalateAfter is how long a task may wait before the waiting is escalated and a
	// record of it is left under the task on the host.
	EscalateAfter string `toml:"escalate_after"`
	// RemindAfter is how long the same state of the same task waits before the same
	// record may be left under it once more.
	RemindAfter string `toml:"remind_after"`
	// WeeklyAfter is how long a task may wait before the wait is called a long one and
	// goes into the weekly slice of the practice journal (#37).
	WeeklyAfter string `toml:"weekly_after"`
	// DeadlineAfter is how long one wait may last before the queue stops saying that
	// nobody is asked for anything and asks for a person instead: по истечении срока
	// ожидание становится «внимание требуется» с причиной `waited-too-long`, а не
	// остаётся бесконечным «ждёт» (D-049, §6a).
	DeadlineAfter string `toml:"deadline_after"`
}

// Requirements are the tools the project needs, with the command that checks
// each of them.
type Requirements struct {
	Tools []Tool `toml:"tools"`
}

// Tool is one required tool and how to check that it is there.
type Tool struct {
	// Name is what a report calls the tool.
	Name string `toml:"name"`
	// Check is the command line that proves the tool is usable.
	Check []string `toml:"check"`
	// Min is the lowest version the project works with, empty if it does not
	// care.
	Min string `toml:"min"`
}

// Isolation is how the agent is kept away from the rest of the machine.
type Isolation struct {
	// Mode is "host", "sandbox" or "container" (docs/DESIGN.md §7d).
	Mode string `toml:"mode"`
}

// Capability is access to secrets and outside systems that the project may need.
// Declaring it grants nothing: the human decides (docs/DESIGN.md §7e).
type Capability struct {
	// Name is what a report calls the capability.
	Name string `toml:"name"`
	// Use are the programs to use the capability through, without seeing the
	// secret.
	Use []string `toml:"use"`
	// Actions are the commands with consequences outside the machine, which are
	// always run only after a confirmation.
	Actions []string `toml:"actions"`
}

// Load reads the crewflow.toml at path, fills in the defaults and checks what
// came out, so that no caller ever works with a file it cannot use.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	meta, err := toml.Decode(string(data), &cfg)
	if err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if unknown := meta.Undecoded(); len(unknown) > 0 {
		keys := make([]string, 0, len(unknown))
		for _, key := range unknown {
			keys = append(keys, key.String())
		}
		return Config{}, fmt.Errorf("parse %s: unknown keys: %s", path, strings.Join(keys, ", "))
	}
	cfg.applyDefaults(&meta)
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	// The shape of the file is one thing and what crewflow can do with it another: a
	// value the design describes and the code has not written is refused here, where
	// the person who wrote the file reads what to write instead, and not in the middle
	// of a task that has already started (docs/DESIGN.md §5).
	if asked := cfg.asked(); len(asked) > 0 {
		return Config{}, fmt.Errorf("%s: %w", path, &NotImplemented{Settings: asked})
	}
	return cfg, nil
}
