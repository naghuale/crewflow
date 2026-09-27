package doctor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/naghuale/crewflow/internal/config"
)

// The probe asks the executor one tiny question and looks for a marker in the
// answer. The marker is what makes the answer proof: an agent that repeats it
// has answered, and a program that ran and even exited zero without it has not
// (docs/DESIGN.md §7a).
const (
	probePrompt = "Reply with exactly: crewflow-probe-ok"
	probeMarker = "crewflow-probe-ok"
)

// probeTimeout is how long the probe waits for the answer. It is a variable so
// that a test does not have to sit through the two minutes a person would.
var probeTimeout = 2 * time.Minute

// probe gives the executor one tiny question in an empty folder, because an
// installed program is not yet a working agent (docs/DESIGN.md §7b).
func (c *checker) probe(ctx context.Context, cfg config.Config) {
	const name = "executor probe"
	if c.env.TempDir == "" {
		c.add(Check{
			Name:   name,
			Status: Fail,
			Detail: "there is no empty folder to run the probe in",
			Hint:   "give Env.TempDir a folder of its own, or do not ask for the probe",
		})
		return
	}
	command, err := probeCommand(cfg.Executor.ExecutorSpec, c.env.TempDir)
	if err != nil {
		c.add(Check{
			Name:   name,
			Status: Fail,
			Detail: err.Error(),
			Hint:   fmt.Sprintf("change executor.command in %s", c.env.ConfigPath),
		})
		return
	}

	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	stdout, stderr, _, _ := c.env.Run(ctx, command[0], command[1:], c.env.TempDir, nil)

	// The exit code of the executor is not what is believed: a refused
	// permission leaves it at zero without a PR (docs/DESIGN.md §7a). The
	// answer is.
	switch {
	case bytes.Contains(stdout, []byte(probeMarker)):
		c.add(Check{Name: name, Status: OK, Detail: "answered with the marker"})
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		c.add(Check{
			Name:   name,
			Status: Fail,
			Detail: fmt.Sprintf("no answer in %s", probeTimeout),
			Hint:   c.runByHand(command),
		})
	default:
		detail := "the answer holds no marker"
		if line := firstLine(stdout, stderr); line != "" {
			detail = "the answer holds no marker: " + line
		}
		c.add(Check{Name: name, Status: Fail, Detail: detail, Hint: c.runByHand(command)})
	}
}

// probeCommand is the command of the executor with the placeholders of a task
// run substituted: the empty folder for the worktree, the question for the
// prompt, and the model of the project where it asked for one.
func probeCommand(spec config.ExecutorSpec, worktree string) ([]string, error) {
	if len(spec.Command) == 0 {
		return nil, errors.New("executor.command is empty")
	}
	command := substitute(spec.Command, worktree, spec.Model)
	// The flag for the model is added only when the project named a model, as
	// in a task run: an agent that was not told which model to use is the one
	// the project trusts.
	if spec.Model != "" && len(spec.ModelFlag) > 0 {
		command = append(command, substitute(spec.ModelFlag, worktree, spec.Model)...)
	}
	return command, nil
}

// substitute replaces the placeholders of a command line with the values of
// this run.
func substitute(command []string, worktree, model string) []string {
	values := strings.NewReplacer("{worktree}", worktree, "{prompt}", probePrompt, "{model}", model)
	substituted := make([]string, 0, len(command))
	for _, arg := range command {
		substituted = append(substituted, values.Replace(arg))
	}
	return substituted
}

// runByHand is how a person sees what the probe ran, so that they can run the
// same thing and watch it.
func (c *checker) runByHand(command []string) string {
	return fmt.Sprintf("run it by hand: %s", strings.Join(command, " "))
}
