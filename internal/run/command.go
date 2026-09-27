package run

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/forge"
)

// slugLimit is how much of the title of a task goes into the name of its branch:
// enough to tell two tasks of one project apart in a list, and short enough that
// the name of a branch stays a name a shell and a host take as it is.
const slugLimit = 40

// Branch is the branch a task is worked on: the number says which task it is and
// the slug of the title says what it is about, so that a person reading a list of
// branches needs nothing else to tell them apart.
func Branch(t forge.Task) string {
	return "crewflow/" + strconv.Itoa(t.Number) + "-" + Slug(t.Title)
}

// Slug is the title of a task as a branch may hold it: lower case letters, digits
// and dashes, and not more than slugLimit of them. A title in a language of the
// project may hold nothing a branch may, and such a task is called "task": the
// number beside it is what tells it from the others.
func Slug(title string) string {
	var slug strings.Builder
	dashed := false
	for _, char := range strings.ToLower(title) {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9':
			slug.WriteRune(char)
			dashed = false
		case !dashed && slug.Len() > 0:
			slug.WriteRune('-')
			dashed = true
		}
	}
	slugged := strings.Trim(slug.String(), "-")
	if len(slugged) > slugLimit {
		slugged = slugged[:slugLimit]
		// A slug that ends in the middle of a word says less about the task than
		// one that stops before it, and costs nothing to keep.
		if last := strings.LastIndex(slugged, "-"); last > 0 {
			slugged = slugged[:last]
		}
	}
	if slugged == "" {
		return "task"
	}
	return slugged
}

// Worktree is the folder of the worktree of a task: one worktree per task under
// the root the project named, and never in the folder of the person, whatever they
// do (docs/DESIGN.md §8).
func Worktree(cfg config.Config, number int) (string, error) {
	root := cfg.Worktrees.Root
	if root == "" {
		root = config.DefaultWorktreesRoot
	}
	expanded, err := cfg.ExpandPath(root)
	if err != nil {
		return "", fmt.Errorf("worktrees root %q: %w", root, err)
	}
	return filepath.Join(expanded, strconv.Itoa(number)), nil
}

// Command is the command line a run starts the executor with: the command of the
// project with the placeholders of this run substituted, and the flag of the model
// only when the project named a model. An agent that was not told which model to
// use is the one the project trusts (docs/DESIGN.md §5).
func Command(spec config.ExecutorSpec, worktree, prompt string) ([]string, error) {
	if len(spec.Command) == 0 {
		return nil, errors.New("executor.command: must not be empty, there is no agent to run the task with")
	}
	command := substitute(spec.Command, worktree, prompt, spec.Model)
	if spec.Model != "" && len(spec.ModelFlag) > 0 {
		command = append(command, substitute(spec.ModelFlag, worktree, prompt, spec.Model)...)
	}
	return command, nil
}

// substitute replaces the placeholders of a command line with the values of this
// run, in every argument: a project may put the worktree and the text of the task
// into one argument of its own (docs/DESIGN.md §5).
func substitute(command []string, worktree, prompt, model string) []string {
	values := strings.NewReplacer("{worktree}", worktree, "{prompt}", prompt, "{model}", model)
	substituted := make([]string, 0, len(command))
	for _, arg := range command {
		substituted = append(substituted, values.Replace(arg))
	}
	return substituted
}
