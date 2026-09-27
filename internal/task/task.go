// Package task reads a task the way a person wrote it and checks that it is ready
// to be run at all (docs/DESIGN.md §7f).
//
// A task is two parts: the specification for a person, in the language of the
// project, and the technical part for the executor, folded away so that a person
// reads the first without wading through the second. crewflow looks for both by
// their headings, because a heading is what a person wrote and a file format would
// be a second dialect to translate.
package task

import (
	"fmt"
	"slices"
	"strings"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/forge"
)

// The labels a task is decided by: one asks the owner for approval, the other
// says that the owner has given it (docs/DESIGN.md §7f).
const (
	approvedLabel = "approved"
	riskyLabel    = "risky"
)

// humanSections are the sections of the specification a person reads, by the
// language a project writes them in. The order is the order of the template: a
// person reads the task from the problem to the risks.
var humanSections = map[string][]string{
	"ru": {"Зачем", "Что изменится", "Как проверить самому", "Что не входит", "Риски и решения"},
	"en": {"Why", "What changes", "How to check it yourself", "Out of scope", "Risks and decisions"},
}

// technicalSections are the two parts of the technical part crewflow must find in
// every task: without them there is nothing to give the executor to work by.
var technicalSections = map[string]struct{ criteria, boundaries string }{
	"ru": {criteria: "Критерии приёмки", boundaries: "Границы"},
	"en": {criteria: "Acceptance criteria", boundaries: "Boundaries"},
}

// defaultLanguage is what a project that names a language crewflow does not know
// is read in: an English task is the one language every person of the team can be
// expected to read, and a task in it must not be refused for the language of the
// setting.
const defaultLanguage = "en"

// ErrNotReady says what a task is missing before it may be run. It is a type and
// not a text because a caller has to tell readiness from a task the tracker could
// not give, and because the list of what is missing is what the owner acts upon
// (docs/DESIGN.md §7f).
type ErrNotReady struct {
	// Number is the task that is not ready.
	Number int
	// Missing is what it is missing, one entry per thing, each of which a person
	// can go and write.
	Missing []string
}

// Error names the task and what it lacks, so that the owner is not left to work
// out which of the five sections it was.
func (e *ErrNotReady) Error() string {
	return fmt.Sprintf("task %d is not ready: %s", e.Number, strings.Join(e.Missing, "; "))
}

// CheckReady reports whether the task may be run at all, and says what is missing
// when it may not. Nothing is created and nobody is started for a task that is not
// ready: the owner is told what to write instead of what half a run of an executor
// did with it (docs/DESIGN.md §7f).
func CheckReady(t forge.Task, cfg config.Config) error {
	if t.State == "closed" {
		return &ErrNotReady{Number: t.Number, Missing: []string{"the task is closed"}}
	}
	language := languageOf(cfg.Project.Language)
	spec := Read(t.Body, language)

	var missing []string
	for _, name := range humanSections[language] {
		if text, ok := spec.Text(name); !ok || strings.TrimSpace(text) == "" {
			missing = append(missing, name)
		}
	}
	missing = append(missing, technicalGaps(spec, language)...)

	if needsApproval(cfg.Tasks.OwnerApproval, t.Labels) && !slices.Contains(t.Labels, approvedLabel) {
		missing = append(missing, "the owner approved it: the label `approved`")
	}
	if len(missing) == 0 {
		return nil
	}
	return &ErrNotReady{Number: t.Number, Missing: missing}
}

// technicalGaps says what the technical part of the task lacks, in the words the
// project writes its headings in. A task with no technical part at all is only
// told about the part itself: listing its two halves as well would name headings
// that were never meant to be there.
func technicalGaps(spec Spec, language string) []string {
	words := technicalSections[language]
	if !spec.HasTechnicalPart() {
		return []string{"the technical part of the task, folded away in <details>"}
	}
	var missing []string
	if !spec.HasCriteria() {
		missing = append(missing,
			fmt.Sprintf("acceptance criteria (%s) with at least one `- [ ]` item", words.criteria))
	}
	if len(spec.Boundaries()) == 0 {
		missing = append(missing,
			fmt.Sprintf("boundaries (%s): a code block with at least one path", words.boundaries))
	}
	return missing
}

// needsApproval says whether a task waits for the approval of the owner, which is
// what [tasks] owner_approval says: a mode crewflow does not know is read as
// "all", because waiting for a person is the safe side of a mistake in the file.
func needsApproval(mode string, labels []string) bool {
	switch mode {
	case "none":
		return false
	case "risky":
		return slices.Contains(labels, riskyLabel)
	default:
		return true
	}
}

// languageOf is the language a task is read in: the one of the project, or
// English when the project names one crewflow does not know.
func languageOf(language string) string {
	if _, known := humanSections[language]; !known {
		return defaultLanguage
	}
	return language
}
