// Package changelog assembles the journal of a project out of the fragments its tasks
// leave, so that two tasks do not share one file and do not conflict over it
// (docs/DESIGN.md §6, журнал изменений).
//
// A task writes one file, `changelog.d/<N>.md`, and no other task writes it; the
// unreleased part of `CHANGELOG.md` is built out of every fragment that stands, in the
// order the changes were merged, and the parts of the versions below it are never
// touched. Nothing here reads the history except [Merged], and nothing here asks the
// host anything except [Project.Check]: a build is a file operation over the fragments
// and the journal, so that it answers the same on every machine.
package changelog

import (
	"errors"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"
)

// Dir is the folder of the fragments and File the journal they build, both from the
// root of the checkout. The folder is a name of its own in the root, and not a hidden
// one: a person reads the fragments of the tasks that are not released yet.
const (
	Dir  = "changelog.d"
	File = "CHANGELOG.md"
)

// Language is the language the journal of a project is written in: the names of the
// sections and the heading of the unreleased part are in it, because a person is the
// one who reads them. The numbers, the files and the commands are the same in every
// language.
type Language string

// The two languages a journal is written in here. A journal of any other language gets
// the English names: a project that says nothing gets the same, as everywhere else in
// crewflow (internal/task).
const (
	Russian Language = "ru"
	English Language = "en"
)

// LanguageOf is the language of a journal out of the `language` of a project.
func LanguageOf(language string) Language {
	if Language(language) == Russian {
		return Russian
	}
	return English
}

// sections are the sections of the journal a fragment writes into, in the order they
// stand in the journal — the four of Keep a Changelog, in the language of the project.
var sections = map[Language][]string{
	Russian: {"Добавлено", "Изменено", "Исправлено", "Безопасность"},
	English: {"Added", "Changed", "Fixed", "Security"},
}

// Sections are the sections of the journal, in the order they stand in it. A build
// writes them in this order whatever order the fragments of the tasks name them in.
func (l Language) Sections() []string {
	return sections[l]
}

// Unreleased is the heading of the part of the journal that no version has taken yet.
func (l Language) Unreleased() string {
	if l == Russian {
		return "## [Не выпущено]"
	}
	return "## [Unreleased]"
}

// heading is the section heading of the journal in this language, and whether the name
// is one of them at all.
func (l Language) heading(name string) (string, bool) {
	for _, section := range l.Sections() {
		if name == section {
			return section, true
		}
	}
	return "", false
}

// Section is one of the sections of the journal, in the language of the project.
type Section string

// Fragment is the piece of the journal one task leaves: the file `changelog.d/<N>.md`,
// where N is the number of the task. It is the only file of the journal the task
// touches, and the only one it may touch (правило 3 PROJECT_RULES.md).
type Fragment struct {
	// Task is the number of the task the file is named after, and the task every line
	// in it names.
	Task int
	// Path is the file from the root of the checkout, `changelog.d/<N>.md`.
	Path string
	// Entries are the lines for a person, in the order the fragment holds them.
	Entries []Entry
}

// Entry is one line of the journal: the bullet as a person wrote it, with the links to
// the task and to the change at its end, and the section of the journal it goes into.
type Entry struct {
	Section Section
	Text    string
	// Line is the line of the fragment the line of the journal starts on, so that a
	// refusal of a check names where the person wrote it.
	Line int
}

// Parse reads the fragment of a task out of the content of a file, whose name is the
// number of the task with the extension `.md`.
//
// A fragment holds the sections of the journal and the lines for a person, and nothing
// else: a heading of the file, a heading of a section the journal has not got, or a line
// outside of a line is a refusal that says what and where — the person who wrote it is
// the one who has to see it, and a build that passed over it would write a journal with
// a piece of it missing and say nothing.
func Parse(name string, content []byte, language Language) (Fragment, error) {
	task, err := TaskOf(name)
	if err != nil {
		return Fragment{}, err
	}
	fragment := Fragment{Task: task, Path: path.Join(Dir, name)}
	section := Section("")
	entry := -1
	for i, line := range strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "### "):
			name := strings.TrimSpace(strings.TrimPrefix(line, "### "))
			if _, known := language.heading(name); !known {
				return Fragment{}, badf(fragment.Path, i+1, "%q is not a section of the journal", name)
			}
			section, entry = Section(name), -1
		case strings.HasPrefix(line, "#"):
			return Fragment{}, badf(fragment.Path, i+1, "a fragment holds sections of the journal, not a heading")
		case strings.HasPrefix(line, "- "):
			fragment.Entries = append(fragment.Entries, Entry{Section: section, Text: line, Line: i + 1})
			entry = len(fragment.Entries) - 1
		case entry >= 0 && (line == "" || strings.HasPrefix(line, " ")):
			fragment.Entries[entry].Text += "\n" + line
		case line == "":
			// A blank line between two lines of the journal is how a person separates
			// them, and it belongs to neither: it is the build that puts one blank line
			// between the lines it writes together.
		default:
			return Fragment{}, badf(fragment.Path, i+1, "a line of the journal begins with %q", "- ")
		}
	}
	if section == "" {
		return Fragment{}, badf(fragment.Path, 0, "the fragment names no section of the journal")
	}
	for i, e := range fragment.Entries {
		if strings.TrimSpace(e.Text) == "- " || !namesTask(e.Text, task) {
			return Fragment{}, badf(fragment.Path, e.Line,
				"the line names no link to the task [#%d] it belongs to", task)
		}
		fragment.Entries[i].Text = strings.TrimRight(e.Text, "\n")
	}
	if len(fragment.Entries) == 0 {
		return Fragment{}, badf(fragment.Path, 0, "the fragment holds no line of the journal")
	}
	return fragment, nil
}

// TaskOf is the number of the task the fragment of this name belongs to: the name is
// the number and nothing else, so that the file of a task is told apart from the file
// of anything else in the folder of the fragments.
func TaskOf(name string) (int, error) {
	digits, ok := strings.CutSuffix(name, ".md")
	if !ok {
		return 0, badf(path.Join(Dir, name), 0, "the name of a fragment is the number of the task and \".md\"")
	}
	task, err := strconv.Atoi(digits)
	if err != nil || task < 1 {
		return 0, badf(path.Join(Dir, name), 0, "the name of a fragment is the number of the task and \".md\"")
	}
	return task, nil
}

// ErrFormat is a fragment that is not a fragment, and [Problem.Err] is it for every
// check that found one. A person is told the file and the line: the refusal of a
// journal names where the journal went wrong, as every refusal of crewflow does.
var ErrFormat = errors.New("the fragment is not a fragment")

// Bad is where a fragment went wrong: the file, the line in it and what was wrong.
type Bad struct {
	Path string
	Line int
	What string
}

func (b Bad) Error() string {
	if b.Line > 0 {
		return fmt.Sprintf("%s:%d: %s", b.Path, b.Line, b.What)
	}
	return fmt.Sprintf("%s: %s", b.Path, b.What)
}

// Unwrap tells the code that a bad fragment is [ErrFormat] and nothing else.
func (b Bad) Unwrap() error { return ErrFormat }

func badf(where string, line int, format string, args ...any) Bad {
	return Bad{Path: where, Line: line, What: fmt.Sprintf(format, args...)}
}

// namesTask is whether the line of the journal names its own task with a link, `[#N]`.
// The line is written for a person, and a person follows the link from it to the task
// and to the change: a line without one cannot be told from the line of another task
// that says about the same thing (правило 3 PROJECT_RULES.md).
func namesTask(text string, task int) bool {
	for _, label := range linkLabels(text) {
		if label == task {
			return true
		}
	}
	return false
}

// Read reads every fragment of the folder of the fragments, sorted by the name of the
// file. The order of the files on the disk is not an answer — it is not the same on
// every machine, and a journal that read itself in it would not be the same twice
// (CA-006).
func Read(root string, language Language) ([]Fragment, error) {
	names, err := os.ReadDir(path.Join(root, Dir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read the fragments: %w", err)
	}
	fragments := make([]Fragment, 0, len(names))
	for _, entry := range names {
		if entry.IsDir() {
			continue
		}
		content, err := os.ReadFile(path.Join(root, Dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path.Join(Dir, entry.Name()), err)
		}
		fragment, err := Parse(entry.Name(), content, language)
		if err != nil {
			return nil, err
		}
		fragments = append(fragments, fragment)
	}
	return fragments, nil
}

// Body renders the unreleased part of the journal out of the fragments, in the order
// they stand in: the sections in the order of the journal, and inside a section the
// lines in the order of the fragments.
//
// A section no line went into is not written at all, so that a journal with nothing
// unreleased in it is a journal with no section under the heading of the unreleased
// part, and a build over it writes the same bytes it wrote last time (CL-005).
func Body(fragments []Fragment, language Language) string {
	var out []string
	for _, name := range language.Sections() {
		var lines []string
		for _, fragment := range fragments {
			for _, entry := range fragment.Entries {
				if string(entry.Section) == name {
					lines = append(lines, entry.Text)
				}
			}
		}
		if len(lines) == 0 {
			continue
		}
		out = append(out, "### "+name+"\n\n"+strings.Join(lines, "\n\n"))
	}
	return strings.Join(out, "\n\n")
}

// Assemble returns the whole journal with its unreleased part built out of the
// fragments. Everything above the heading of the unreleased part and every version
// below it stand as they are, and what stands between the heading and the first
// version is written afresh — a journal that was edited by hand under that heading is
// normalized by the build, and a build that went on top of it would carry it into the
// release.
func Assemble(content []byte, body string, language Language) ([]byte, error) {
	above, released, err := split(content, language)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoUnreleased, err)
	}
	out := above + "\n"
	if body != "" {
		out += body + "\n\n"
	}
	return []byte(out + released), nil
}

// split is the journal in the two parts a build touches and does not: everything from the
// heading of the unreleased part, and everything from the first version on. What stands
// between them is the part that is built, and none of it is kept.
//
// A journal with no heading of the unreleased part, or with no version below it, is
// refused: those are the two ends a build writes between, and a journal without them is
// one a person has to look at.
func split(content []byte, language Language) (above, released string, err error) {
	text := string(content)
	heading := language.Unreleased()
	start := strings.Index(text, heading+"\n")
	if start < 0 {
		return "", "", fmt.Errorf("%s has no section %q", File, heading)
	}
	tail := text[start+len(heading)+1:]
	below := strings.Index(tail, "\n## ")
	if below < 0 {
		return "", "", fmt.Errorf("%s ends with %q and has no version below it", File, heading)
	}
	return text[:start] + heading + "\n", tail[below+1:], nil
}
