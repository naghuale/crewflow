package run

import (
	"embed"
	"fmt"
	"strings"
	"text/template"

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
// works, what has to pass, and the task itself.
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
	// CommitStyle is how the messages of the commits of this project are written.
	CommitStyle string
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

// Prompt is what the executor of a run is asked to do: the rules of the run, the
// commands of the project that must pass, and the whole task. The task goes last,
// because the rules are what the agent has to hold while it works through it.
func Prompt(t forge.Task, cfg config.Config, branch, worktree string) (string, error) {
	return fill(assignment{
		Number:      t.Number,
		Title:       t.Title,
		Body:        t.Body,
		Branch:      branch,
		Worktree:    worktree,
		Temp:        Scratch(worktree),
		CommitStyle: commitStyle(cfg),
		Gates:       gates(cfg.Gates),
	})
}

// Continuation is what an executor is given when the run of a task goes on: the
// message of the orchestrator and the whole task. An agent that continues its own
// session holds the task already, and an agent that has no session to continue gets
// the task again, or it would begin from nothing (docs/DESIGN.md §7a).
func Continuation(message string, t forge.Task, cfg config.Config, branch, worktree string) (string, error) {
	assignment := assignment{
		Number:      t.Number,
		Title:       t.Title,
		Body:        t.Body,
		Branch:      branch,
		Worktree:    worktree,
		Temp:        Scratch(worktree),
		CommitStyle: commitStyle(cfg),
		Gates:       gates(cfg.Gates),
	}
	// The message comes before the task for the same reason the task comes last in
	// a first run: what the orchestrator asks for now is what the agent reads
	// first, and the task is what it works by.
	return fill(assignment, message+"\n\n")
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
