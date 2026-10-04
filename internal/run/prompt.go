package run

import (
	"embed"
	"fmt"
	"strings"
	"text/template"

	"github.com/naghuale/crewflow/internal/access"
	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/forge"
)

// promptTemplate is the assignment of a run. It is in the binary rather than in a
// file next to it: an executor has to be given the rules of the run whatever was
// installed and wherever crewflow runs, and a template that is not there is a run
// that goes on without them.
//
// It is in English, whatever the language of the project: the tools crewflow runs
// read English, and the task of the project says the same rules in the words of
// the project.
var promptTemplate = template.Must(template.ParseFS(promptFiles, "prompt.tmpl"))

//go:embed prompt.tmpl
var promptFiles embed.FS

// assignment is what the template of a run is filled with: where the executor
// works, what it may read and write and why, what has to pass, and the task itself.
type assignment struct {
	// Number is the task being run, which the change request has to close.
	Number int
	// Title and Body are the task as a person wrote it, whole.
	Title string
	Body  string
	// Branch and Worktree are where the work is to be done.
	Branch   string
	Worktree string
	// Temp is the folder in the worktree where the executor may keep its temporary
	// files, named as a path: an agent that was told where it may write keeps its
	// temporary files there and does not have to be refused a permission for it.
	Temp string
	// Write are the folders of the machine the executor may write in, each with why:
	// the worktree of the task and the scratch of the run, and nothing else.
	Write []access.Grant
	// Read are the folders it may read outside the worktree, each with where the ask
	// came from and why it was made.
	Read []access.Grant
	// Deny are the places of the machine that stay closed whatever the project and the
	// task wrote.
	Deny []string
	// CommitStyle is how the messages of the commits of this project are written.
	CommitStyle string
	// Timeout is how long this attempt may take, as the project writes it: an executor
	// that is not told the limit of its attempt cannot commit what is finished before it,
	// and the commit of what is finished is what makes the interruption of a run cost at
	// most one step of work (D-088, docs/DESIGN.md §7a).
	Timeout string
	// Gates is what the project says has to pass before the work is reviewed.
	Gates string
}

// defaultCommitStyle is the rule the executor is given when the project says nothing
// about the style of its commits: the style of the last commits of the repository
// itself. The pilot of M1.4 wrote a commit with a paragraph of text in it because
// the assignment said nothing about the style, and a person who writes the messages of
// the commits is the only one who knows what the project calls a message.
const defaultCommitStyle = "follow the style of the last commits of the project " +
	"(`git log --oneline -20`): a short first line of at most 72 characters, an empty line, and then the body"

// Prompt is what the executor of a run is asked to do: the rules of the run, the map
// of the access it has been given, the commands of the project that must pass, and the
// whole task. The task goes last, because the rules are what the agent has to hold
// while it works through it.
//
// The map is the same policy that is written into the rights of the agent, and it is
// here in words because an agent that is refused a permission for a path does not go
// and read a table of them: it either knows beforehand that the path is open or it
// stops there, and a run that stopped there has done nothing (docs/DESIGN.md §7d).
func Prompt(t forge.Task, cfg config.Config, branch, worktree string, policy access.Policy) (string, error) {
	return fill(newAssignment(t, cfg, branch, worktree, policy))
}

// Continuation is what an executor is given when the run of a task goes on: the
// message of the orchestrator and the whole task. An agent that continues its own
// session holds the task already, and an agent that has no session to continue gets
// the task again, or it would begin from nothing (docs/DESIGN.md §7a).
func Continuation(message string, t forge.Task, cfg config.Config, branch, worktree string, policy access.Policy) (string, error) {
	// The message comes before the task for the same reason the task comes last in
	// a first run: what the orchestrator asks for now is what the agent reads
	// first, and the task is what it works by.
	return fill(newAssignment(t, cfg, branch, worktree, policy), message+"\n\n")
}

// newAssignment is the whole of what the template of a run is filled with. It is one
// place so that a first run and a continuation are given the same map of the access
// and the same rules: a continuation that was told less than the run it goes on with
// is a continuation that works by less.
func newAssignment(t forge.Task, cfg config.Config, branch, worktree string, policy access.Policy) assignment {
	temp := Scratch(worktree)
	return assignment{
		Number: t.Number,
		Title:  t.Title,
		Body:   t.Body,
		Branch: branch,
		// The worktree and the scratch of the run are the two folders a run may write
		// in, and the reason of each is the same answer in two words: they are what
		// this run is for.
		Worktree:    worktree,
		Temp:        temp,
		Write:       writable(worktree, temp),
		Read:        policy.Read,
		Deny:        policy.Deny,
		CommitStyle: commitStyle(cfg),
		Timeout:     timeoutOf(cfg),
		Gates:       gates(cfg.Gates),
	}
}

// timeoutOf is how long the attempt may take, as the project writes it. It is said as the
// project wrote it and not worked out into words: the number in `crewflow.toml` is the
// number the gates of the project run, and an executor that is told "an hour and a half"
// where the file says "90m" is an executor that has to trust the sentence more than the
// file (D-088, docs.DESIGN.md §7a).
func timeoutOf(cfg config.Config) string {
	if timeout := strings.TrimSpace(cfg.Executor.Timeout); timeout != "" {
		return timeout
	}
	return "no limit is written in the settings of the project"
}

// writable is the map of what a run may write in: the worktree of the task and the
// scratch crewflow made in it, and nothing else of the machine. Both are named as
// paths and not as patterns, because a run writes in a worktree of its own and not in
// the next one (docs/DESIGN.md §7d).
func writable(worktree, temp string) []access.Grant {
	return []access.Grant{
		{Path: worktree, Source: access.SourceCrewflow, Reason: "the worktree of this task"},
		{Path: temp, Source: access.SourceCrewflow, Reason: "the scratch of this run: TMPDIR, TMP and TEMP point at it"},
	}
}

// commitStyle is the rule the executor is given about the messages of the commits:
// what the project wrote in its settings, or the style of its last commits.
func commitStyle(cfg config.Config) string {
	if style := strings.TrimSpace(cfg.Project.CommitStyle); style != "" {
		return style
	}
	return defaultCommitStyle
}

// fill writes the template of the run into the text that goes before it, and is the
// one place that knows the two belong together.
func fill(a assignment, before ...string) (string, error) {
	var out strings.Builder
	for _, text := range before {
		out.WriteString(text)
	}
	if err := promptTemplate.Execute(&out, a); err != nil {
		return "", fmt.Errorf("the assignment of the run: %w", err)
	}
	return out.String(), nil
}

// gates is what the executor is told to run before it opens the change request:
// every gate of the project with the command of it, so that the agent does not have
// to read the settings of the project to find out what "done" means here.
func gates(gates []config.Gate) string {
	if len(gates) == 0 {
		return "The project names no gates, so there is nothing to run before the change request."
	}
	var list strings.Builder
	list.WriteString("These commands of the project must pass before the change request is opened:\n")
	for _, gate := range gates {
		fmt.Fprintf(&list, "\n- %s: %s", gate.Name, strings.Join(gate.Run, " "))
	}
	return list.String()
}
