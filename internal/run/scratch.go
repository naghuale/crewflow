package run

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The folder inside the worktree of a task where the executor may keep what it works
// with, and the name of it in the local ignore of the repository. The scratch of a
// run is the one thing an agent writes outside of what the task is to change, and
// every worktree of a project shares one repository, so the whole `.scratch/` is
// ignored once for the repository and not for one worktree (docs/DESIGN.md §7a).
const (
	scratchFolder = ".scratch"
	scratchIgnore = scratchFolder + "/"
)

// Scratch is the folder of the worktree of a task that the executor may keep its
// temporary files in: `.scratch/tmp` in the worktree, made before the executor is
// started and named in the assignment. An agent that writes a temporary file is
// refused a permission for it, and a run that stops there has done nothing
// (docs/DESIGN.md §7a).
func Scratch(worktree string) string {
	return filepath.Join(worktree, scratchFolder, "tmp")
}

// tempEnv is the environment of the executor with its temporary files pointed at the
// scratch of its own worktree. The three names are the ones a program may look its
// temporary folder up under, and they are all of them: a program that finds one of
// them empty writes into the temporary folder of the machine, which is outside the
// worktree and therefore refused.
func tempEnv(worktree string) []string {
	folder := Scratch(worktree)
	return []string{"TMPDIR=" + folder, "TMP=" + folder, "TEMP=" + folder}
}

// scratch makes the folder of the temporary files of the executor in the worktree of
// the task and keeps the folder out of the repository of the project. The ignore is
// the local one of git, and not the `.gitignore` of the project: the work of a run
// is not the work of the project, and a repository a person owns is not changed
// because a task was run in it.
func (r *runner) scratch(ctx context.Context) error {
	if err := os.MkdirAll(Scratch(r.worktree), 0o700); err != nil {
		return fmt.Errorf("make %s: %w", Scratch(r.worktree), err)
	}
	common, err := r.output(ctx, r.worktree, "rev-parse", "--git-common-dir")
	if err != nil {
		return err
	}
	// An empty answer is a folder of its own: a path built out of nothing would be
	// written where crewflow happens to be called from.
	if strings.TrimSpace(common) == "" {
		return errors.New("git rev-parse --git-common-dir: named no repository, so there is nowhere to keep the scratch of the run out of")
	}
	return exclude(filepath.Join(strings.TrimSpace(common), "info", "exclude"))
}

// exclude puts the line into the local ignore of the repository, once: a run may be
// made again and again, and the file is read by every worktree of the project.
func exclude(path string) error {
	previous, err := os.ReadFile(path)
	switch {
	case err == nil && hasScratch(previous):
		return nil
	case err != nil && !os.IsNotExist(err):
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("make %s: %w", filepath.Dir(path), err)
	}
	line := scratchIgnore + "\n"
	// The ignore of git is a list of lines, and it may end without a newline of its
	// own: the line is added whole rather than to a line half written.
	if len(previous) > 0 && previous[len(previous)-1] != '\n' {
		line = "\n" + line
	}
	if err := os.WriteFile(path, append(previous, line...), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// hasScratch says whether the local ignore of a repository already holds the scratch
// as a pattern of its own: a line that mentions it in other words is another line.
func hasScratch(ignore []byte) bool {
	for raw := range strings.Lines(string(ignore)) {
		if strings.TrimSpace(raw) == scratchIgnore {
			return true
		}
	}
	return false
}
