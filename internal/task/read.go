package task

import (
	"regexp"
	"strings"
)

// Spec is the body of a task as crewflow reads it: the sections of the
// specification a person controls with the text under them, and the technical part
// folded away for the executor (docs/DESIGN.md §7f).
type Spec struct {
	// language is the language the sections are looked for in, which is the
	// language of the project or English.
	language string
	// sections are the headings of the body with the text under them, in the
	// order they were written.
	sections []section
	// folded says that the body holds the block with the technical part in it.
	folded bool
}

// section is one heading of a body with what stands under it.
type section struct {
	// name is the heading as a person wrote it, in the language of the project.
	name string
	// text is what stands under it, which is what a person would see: the
	// comments of the template are not part of it.
	text string
	// technical says that the heading belongs to the technical part of the task
	// and not to the specification a person reads.
	technical bool
}

// Read reads the body of a task in the language the project writes its tasks in.
// A language crewflow does not know is read as English, so that a task is not
// refused for the words it is written in.
func Read(body, language string) Spec {
	language = languageOf(language)
	spec := Spec{language: language}

	// current is the section the lines below belong to, and the sections are
	// addressed by their place: a pointer into a slice that grows with every
	// heading would stop pointing at the section it named.
	current, folded := -1, 0
	for raw := range strings.Lines(body) {
		line := strings.TrimRight(raw, "\r\n")
		switch {
		case foldOpen.MatchString(line):
			folded++
			spec.folded = true
			// What is folded away is not part of the specification above it: the
			// text of a section ends where the technical part begins, or the
			// technical part would stand in for a section nobody wrote.
			current = -1
		case foldClose.MatchString(line):
			if folded > 0 {
				folded--
			}
			// The technical part ends with the tag that folds it away, and the tag is
			// not text of the last section in it: a person reads the field at the end
			// of the technical part and crewflow reads its lines, and neither of them
			// wrote `</details>` as a line of a field.
			current = -1
		}
		if name, isHeading := heading(line); isHeading {
			spec.sections = append(spec.sections, section{name: name, technical: folded > 0})
			current = len(spec.sections) - 1
			continue
		}
		if current >= 0 {
			spec.sections[current].text += line + "\n"
		}
	}
	for i := range spec.sections {
		spec.sections[i].text = withoutComments(spec.sections[i].text)
	}
	return spec
}

// Text returns what stands under the heading with that name, and whether there is
// such a heading at all. A section of the specification is one outside the
// technical part: inside it the words belong to the executor.
func (s Spec) Text(name string) (string, bool) {
	for _, section := range s.sections {
		if section.name == name && !section.technical {
			return strings.TrimSpace(section.text), true
		}
	}
	return "", false
}

// HasTechnicalPart reports whether the body holds the block with the technical part
// of the task in it: without it there is nothing to give the executor to work by.
func (s Spec) HasTechnicalPart() bool {
	return s.folded
}

// HasCriteria reports whether the technical part holds acceptance criteria with at
// least one item to tick. Criteria with nothing to tick are not criteria, and a
// task whose criteria hold no item has not said what "done" is.
func (s Spec) HasCriteria() bool {
	words := technicalSections[s.language].criteria
	for _, section := range s.sections {
		if section.technical && section.name == words && criteriaItem.MatchString(section.text) {
			return true
		}
	}
	return false
}

// Boundaries returns the paths a task may change, out of the code block of the
// technical part that says where they are (docs/DESIGN.md §7c). A task with no
// such block has no boundaries, and the check of a run then holds that it changed
// nothing.
func (s Spec) Boundaries() Boundaries {
	words := technicalSections[s.language].boundaries
	for _, section := range s.sections {
		if section.technical && section.name == words {
			return pathsIn(section.text)
		}
	}
	return nil
}

// The block the technical part of a task is folded away into, so that a person
// reads the task without wading through the part that is not for them.
var (
	foldOpen  = regexp.MustCompile(`(?i)<details[\s>]`)
	foldClose = regexp.MustCompile(`(?i)</details\s*>`)
)

// headingPattern finds a heading of markdown: the hashes, the space after them and
// the words of the heading, which a person closes with hashes or not.
var headingPattern = regexp.MustCompile(`^\s{0,3}#{1,6}\s+(.*)$`)

// criteriaItem finds an item of a list to tick, in either mark, ticked or not: a
// list of criteria that has none is a list of wishes.
var criteriaItem = regexp.MustCompile(`(?m)^[ \t]*[-*][ \t]+\[[ xX]\]`)

// commentPattern finds what the template of a task hides from a person: a
// comment, closed or not, since a task written by hand often leaves the closing
// out.
var commentPattern = regexp.MustCompile(`(?s)<!--.*?(-->|$)`)

// heading is the name of the heading on the line, and whether the line is a
// heading at all. A heading of deeper level ends the text of the one above it,
// which is what puts the two parts of the technical part apart.
func heading(line string) (string, bool) {
	match := headingPattern.FindStringSubmatch(line)
	if match == nil {
		return "", false
	}
	name := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(match[1]), "#"))
	return name, name != ""
}

// withoutComments is the text a person reads: the comments of the template are
// for the one who fills the task in, and a section that holds nothing but them is
// a section nobody filled in.
func withoutComments(text string) string {
	return commentPattern.ReplaceAllString(text, "")
}

// pathsIn reads the paths out of the first code block of a text, one per line. A
// line that says nothing and a line that begins with a hash are not paths: the
// first is how a code block ends a line, the second is a note about them.
func pathsIn(text string) Boundaries {
	var paths Boundaries
	inside := false
	for raw := range strings.Lines(text) {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "```"):
			if inside {
				return paths
			}
			inside = true
		case inside && line != "" && !strings.HasPrefix(line, "#"):
			paths = append(paths, line)
		}
	}
	return paths
}
