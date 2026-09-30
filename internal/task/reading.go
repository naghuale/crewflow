package task

import (
	"fmt"
	"strings"
)

// readingFields is the part of the technical part that says what a task may read
// outside the work folder of its task: the heading a person wrote it under and the
// word every line of it starts with. A project writes the field in its own language,
// like every other heading of a task, and a task written in the other one is read in
// English like the rest of it.
var readingFields = map[string]struct{ heading, marker string }{
	"ru": {heading: "Что читать вне рабочей папки и зачем", marker: "читать:"},
	"en": {heading: "What to read outside the work folder and why", marker: "read:"},
}

// nothing is what a task says when it needs nothing read outside its work folder: the
// answer most tasks give, and the answer of the template of a task.
var nothing = map[string][]string{
	"ru": {"ничего", "нет"},
	"en": {"nothing", "none"},
}

// Reading is one folder a task asks the executor of its run to read outside the
// worktree of that worktree, and why a person wrote for it. The two are one entry and
// not two: a path without a reason is not an ask, and `crewflow task check` says so
// before a run of the task starts (docs/DESIGN.md §7f).
type Reading struct {
	// Path is what the task named, as the task named it: an absolute path of this
	// machine, or a "~" that stands for the home of the person.
	Path string
	// Reason is why the task needs it, in the words of a person. A review of the task
	// reads it, and so does the journal of the run.
	Reason string
}

// Readings is what a task asks to read outside the worktree of its work folder, and
// what crewflow could not read of the field. A field that is not there, or that says
// there is nothing to read, asks for nothing and says nothing: a task of a project
// that names its dependencies in its own file is the common case, and the run of it
// gets what the file gives (docs/DESIGN.md §7d, §7f).
func (s Spec) Readings() ([]Reading, []string) {
	field, ok := readingFields[s.language]
	if !ok {
		return nil, nil
	}
	var asked []Reading
	var complaints []string
	for _, section := range s.sections {
		if !section.technical || section.name != field.heading {
			continue
		}
		for raw := range strings.Lines(section.text) {
			line := strings.TrimSpace(raw)
			if line == "" || saysNothing(s.language, line) {
				continue
			}
			reading, complaint := readOne(field.marker, line)
			if complaint != "" {
				complaints = append(complaints, complaint)
				continue
			}
			asked = append(asked, reading)
		}
	}
	return asked, complaints
}

// readOne is one line of the field: the word of the field, the path, and the reason
// after it. A line that is none of those says so in the words a person acts upon,
// because a line crewflow cannot read is a run that would be refused a permission for
// a path nobody asked for.
func readOne(marker, line string) (Reading, string) {
	rest, isLine := strings.CutPrefix(line, marker)
	rest = strings.TrimSpace(rest)
	switch {
	case rest == "":
		return Reading{}, fmt.Sprintf("the line %q of the reading field names no path", line)
	case !isLine:
		return Reading{}, fmt.Sprintf("the line %q of the reading field does not start with %q", line, marker)
	}
	// The path is the first word of the line and the reason is everything after the
	// dash that stands between them, because a path has no spaces in it and a reason
	// is a sentence. A line with no dash is a path on its own, which is what a person
	// wrote when the reason was left out.
	path, reason := cutReason(rest)
	if reason == "" {
		return Reading{}, fmt.Sprintf("the path %q of the reading field has no reason: "+
			"a reason is written after a dash, as %q, because a review of the task reads it",
			path, marker+" "+path+" — why the task needs it")
	}
	return Reading{Path: path, Reason: reason}, ""
}

// cutReason is the path of a line and what stands after the dash between it and the
// reason. Both the em dash a person writes in a task and the plain hyphen of a
// keyboard are read as it, because a task is written by hand and the reason is what
// matters, not the mark.
func cutReason(line string) (path, reason string) {
	for _, dash := range []string{" — ", " – ", " - "} {
		if path, reason, isSplit := strings.Cut(line, dash); isSplit {
			return strings.TrimSpace(path), strings.TrimSpace(reason)
		}
	}
	path, _, _ = strings.Cut(line, " ")
	return path, ""
}

// saysNothing is whether a line of the field is the answer that there is nothing to
// read. A task is written in the language of its project, and the word of that answer
// is the word of that language: a task read in English says it in English.
func saysNothing(language, line string) bool {
	for _, word := range nothing[language] {
		if strings.EqualFold(strings.Trim(line, " .—-"), word) {
			return true
		}
	}
	return false
}
