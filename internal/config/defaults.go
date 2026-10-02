package config

import "github.com/BurntSushi/toml"

// Defaults of the keys a project may leave out. None of them names an agent, a
// model or a language of the host: a project says all of that itself, and
// crewflow only fills in the parts that mean the same everywhere.
const (
	defaultBranch            = "main"
	defaultLanguage          = "en"
	defaultWorktreesRoot     = "~/.crewflow/worktrees/{repo}"
	defaultExecutorTimeout   = "90m"
	defaultStallAfter        = "10m"
	defaultResumeWithin      = "24h"
	defaultForgeKind         = "github"
	defaultTrackerKind       = "forge"
	defaultIdentityMode      = "owner"
	defaultOrchestratorMode  = ModeShared
	defaultCIKind            = "forge"
	defaultCIRequired        = true
	defaultCITimeout         = "30m"
	defaultMergeBy           = "orchestrator"
	defaultMergeStrategy     = "ff-only"
	defaultMergeVia          = "git-push"
	defaultMaxTasks          = 1
	defaultIsolationMode     = "host"
	defaultOwnerApproval     = "risky"
	defaultAcceptanceLabel   = "owner-check"
	defaultAttentionTop      = "30m"
	defaultAttentionEscalate = "24h"
	defaultAttentionRemind   = "24h"
	defaultAttentionWeekly   = "168h"
	// The route of a project that says nothing is a straight route, and the two
	// terms of it are the ones a person would name anyway (§7d).
	defaultNetworkMode    = "direct"
	defaultConnectTimeout = "10s"
	defaultTestValidFor   = "1h"
	defaultProxyType      = "http"
	defaultCredentials    = "none"
)

// DefaultWorktreesRoot is where the worktree of a task is made when the file of
// the project does not say: outside the folder of the person, whatever the
// worktrees of a run end up doing (docs/DESIGN.md §8). It is exported because a
// caller that builds the settings of a project by hand must land on the same
// place as one that reads them from a file.
const DefaultWorktreesRoot = defaultWorktreesRoot

// DefaultResumeWithin is how long the point a run of a task stands at stays a point to
// go on from when the file of the project does not say: a day, which is the length of a
// working day of a person and the same length the queue of attention waits before it
// escalates a task of its own (docs.DESIGN.md §6a, §7i). It is exported for the same
// reason [DefaultWorktreesRoot] is: a caller that builds the settings by hand must land
// on the same place as one that reads them from a file.
const DefaultResumeWithin = defaultResumeWithin

// applyDefaults fills in what the file does not say. A key written as false or as
// 0 is a decision, not a missing key, so for those the file itself is asked
// whether the key is there at all.
func (c *Config) applyDefaults(meta *toml.MetaData) {
	if c.Project.DefaultBranch == "" {
		c.Project.DefaultBranch = defaultBranch
	}
	if c.Project.Language == "" {
		c.Project.Language = defaultLanguage
	}
	if c.Forge.Kind == "" {
		c.Forge.Kind = defaultForgeKind
	}
	if c.Tracker.Kind == "" {
		c.Tracker.Kind = defaultTrackerKind
	}
	// The executor works as the person who runs crewflow until a project says
	// otherwise: a mode of a bot is something a project sets up on purpose, with an
	// app of its own and a key of its own (docs/DESIGN.md §7i).
	if c.Identity.Mode == "" {
		c.Identity.Mode = defaultIdentityMode
	}
	// The orchestrator shares the login of the person until a project sets up an
	// account of the host for it: a second app and a second key are work, and a
	// project that has not done it keeps the way crewflow has always worked — with
	// `doctor` saying what that costs (docs/DESIGN.md §7i).
	if c.Orchestrator.Mode == "" {
		c.Orchestrator.Mode = defaultOrchestratorMode
	}
	if c.Executor.Timeout == "" {
		c.Executor.Timeout = defaultExecutorTimeout
	}
	// A run of a project that says nothing about standing still is marked after ten
	// minutes of silence: an executor that works for a quarter of an hour says
	// something every few minutes, and ten minutes of nothing is a run nobody is
	// watching (docs/DESIGN.md §6, §7a).
	if c.Executor.StallAfter == "" {
		c.Executor.StallAfter = defaultStallAfter
	}
	// A point a run of a task stood at while it waited for a decision of a person is
	// good for a day: a person comes back to it within a day, and a point of a week
	// ago is a point about a machine and a task that are not the ones now
	// (docs/DESIGN.md §7i).
	if c.Executor.ResumeWithin == "" {
		c.Executor.ResumeWithin = defaultResumeWithin
	}
	// A fallback executor that says nothing about the time limit or about standing
	// still gets the ones of the executor it stands in for: both are the limits of the
	// run and not of the agent in it.
	for i := range c.Executor.Fallback {
		if c.Executor.Fallback[i].Timeout == "" {
			c.Executor.Fallback[i].Timeout = c.Executor.Timeout
		}
		if c.Executor.Fallback[i].StallAfter == "" {
			c.Executor.Fallback[i].StallAfter = c.Executor.StallAfter
		}
	}
	if c.Worktrees.Root == "" {
		c.Worktrees.Root = defaultWorktreesRoot
	}
	if !meta.IsDefined("ci", "required") {
		c.CI.Required = defaultCIRequired
	}
	if c.CI.Timeout == "" {
		c.CI.Timeout = defaultCITimeout
	}
	if c.CI.Kind == "" {
		c.CI.Kind = defaultCIKind
	}
	if c.Merge.By == "" {
		c.Merge.By = defaultMergeBy
	}
	if c.Merge.Strategy == "" {
		c.Merge.Strategy = defaultMergeStrategy
	}
	if c.Merge.Via == "" {
		c.Merge.Via = defaultMergeVia
	}
	if !meta.IsDefined("parallel", "max_tasks") {
		c.Parallel.MaxTasks = defaultMaxTasks
	}
	if c.Isolation.Mode == "" {
		c.Isolation.Mode = defaultIsolationMode
	}
	if c.Tasks.OwnerApproval == "" {
		c.Tasks.OwnerApproval = defaultOwnerApproval
	}
	// The label a task is marked with to have its result accepted by the owner is the
	// one crewflow reads of §5: a project that says nothing is a project whose tasks
	// are marked `owner-check`, and the gate refuses their merges until the owner has
	// looked at the head (docs/DESIGN.md §7h).
	if c.Acceptance.Label == "" {
		c.Acceptance.Label = defaultAcceptanceLabel
	}
	// The thresholds of the queue of attention are the ones a project that says nothing
	// gets, and they are the ones the design of §6a names: half an hour before a task
	// waits in front of a person whatever its class, a day before the waiting is escalated
	// and written under the task, a day before the same record may be written again, and
	// a week before the wait is called a long one and goes into the weekly slice (#37).
	// The silence after which a run is called standing is `[executor] stall_after` and
	// not a key of this table (§7a).
	if c.Attention.TopAfter == "" {
		c.Attention.TopAfter = defaultAttentionTop
	}
	if c.Attention.EscalateAfter == "" {
		c.Attention.EscalateAfter = defaultAttentionEscalate
	}
	if c.Attention.RemindAfter == "" {
		c.Attention.RemindAfter = defaultAttentionRemind
	}
	if c.Attention.WeeklyAfter == "" {
		c.Attention.WeeklyAfter = defaultAttentionWeekly
	}
	// A project that says nothing about the network goes straight out to it, and the
	// two terms of its route are the ones a person would name anyway: how long a check
	// of a route waits for an answer, and how long that answer is a fact about the
	// machine (docs/DESIGN.md §7d).
	if c.Network.Mode == "" {
		c.Network.Mode = defaultNetworkMode
	}
	if c.Network.ConnectTimeout == "" {
		c.Network.ConnectTimeout = defaultConnectTimeout
	}
	if c.Network.TestValidFor == "" {
		c.Network.TestValidFor = defaultTestValidFor
	}
	// A profile is written by a person and it always says all of itself: a profile
	// without a protocol cannot be talked to and one without a decision about
	// credentials leaves a question crewflow would have to guess at.
	for name, proxy := range c.Network.Proxies {
		if proxy.Type == "" {
			proxy.Type = defaultProxyType
		}
		if proxy.Credentials == "" {
			proxy.Credentials = defaultCredentials
		}
		c.Network.Proxies[name] = proxy
	}
}
