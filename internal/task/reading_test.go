package task

import (
	"slices"
	"strings"
	"testing"
)

// TestReadingsIsWhatTheTaskAsks walks the field that says what a task may read outside
// its work folder, line by line: the common answer of a task is that it needs nothing,
// and a task that names a path names the reason along with it, because a review of the
// task is what a person reads to decide whether the folder is worth opening to a run
// of an agent (docs/DESIGN.md §7d, §7f).
//
// A line is written as "%s" for the word the language of the project writes the field
// with, so that the same case reads a Russian task and an English one.
func TestReadingsIsWhatTheTaskAsks(t *testing.T) {
	cases := []struct {
		name    string
		lines   []string
		want    []Reading
		without []string
	}{
		{
			name: "a task that needs nothing",
		},
		{
			name:  "a path and the reason a person wrote",
			lines: []string{"%s /usr/local/go — the source of the toolchain the task builds"},
			want:  []Reading{{Path: "/usr/local/go", Reason: "the source of the toolchain the task builds"}},
		},
		{
			name:  "two paths, each with its own reason",
			lines: []string{"%s /usr/local/go — the toolchain", "%s /opt/sdk — the headers of C"},
			want: []Reading{
				{Path: "/usr/local/go", Reason: "the toolchain"},
				{Path: "/opt/sdk", Reason: "the headers of C"},
			},
		},
		{
			name:  "a home-relative path",
			lines: []string{"%s ~/go/pkg/mod — the module cache of the project"},
			want:  []Reading{{Path: "~/go/pkg/mod", Reason: "the module cache of the project"}},
		},
		{
			name:  "a reason written with a plain hyphen",
			lines: []string{"%s /opt/sdk - the headers of C"},
			want:  []Reading{{Path: "/opt/sdk", Reason: "the headers of C"}},
		},
		{
			name:    "a task with no such field at all",
			without: []string{"field"},
		},
	}
	for _, language := range []string{"ru", "en"} {
		for _, tc := range cases {
			t.Run(language+"/"+tc.name, func(t *testing.T) {
				asked := taskOf(43, language, tc.without...)
				if len(tc.lines) > 0 {
					asked.Body = withLines(t, language, linesIn(tc.lines, language)...)
				}
				spec := Read(asked.Body, language)

				got, complaints := spec.Readings()

				if len(complaints) != 0 {
					t.Fatalf("the complaints = %v, want none", complaints)
				}
				if !slices.Equal(got, tc.want) {
					t.Errorf("the readings = %+v, want %+v", got, tc.want)
				}
			})
		}
	}
}

// TestReadingsRefusesAPathWithoutAReason: a path a task names with nothing to say about
// it is a folder crewflow would open to an agent with no one to ask why. A check of
// the task says so before a run of it starts, and the message names the path and shows
// the line to write, because what a person acts upon is what they can copy
// (docs/DESIGN.md §7f).
func TestReadingsRefusesAPathWithoutAReason(t *testing.T) {
	for _, language := range []string{"ru", "en"} {
		t.Run(language, func(t *testing.T) {
			marker := reading[language].marker
			spec := Read(withLines(t, language, marker+" /usr/local/go"), language)

			got, complaints := spec.Readings()

			if len(got) != 0 {
				t.Errorf("the readings = %+v, want none: a path without a reason is not an ask", got)
			}
			if len(complaints) != 1 {
				t.Fatalf("the complaints = %v, want one", complaints)
			}
			for _, want := range []string{`"/usr/local/go"`, "no reason", marker + " /usr/local/go — why the task needs it"} {
				if !strings.Contains(complaints[0], want) {
					t.Errorf("the complaint = %q, want it to hold %q", complaints[0], want)
				}
			}
		})
	}
}

// TestReadingsRefusesALineItCannotRead: a line of the field that is neither the word of
// the field nor a path is a line a run would be stopped by, and it is stopped before it
// starts rather than in the middle of the work (docs/DESIGN.md §7f).
func TestReadingsRefusesALineItCannotRead(t *testing.T) {
	cases := map[string]string{
		"a line without the word of the field": "the headers of C",
		"the word of the field and no path":    "%s",
	}
	for _, language := range []string{"ru", "en"} {
		for name, line := range cases {
			t.Run(language+"/"+name, func(t *testing.T) {
				spec := Read(withLines(t, language, linesIn([]string{line}, language)...), language)

				got, complaints := spec.Readings()

				if len(got) != 0 {
					t.Errorf("the readings = %+v, want none", got)
				}
				if len(complaints) != 1 || !strings.Contains(complaints[0], "reading field") {
					t.Errorf("the complaints = %v, want one that names the line and the field", complaints)
				}
			})
		}
	}
}

// TestCheckReadyRefusesAPathWithoutAReason is the promise the field makes to a person
// before a run: `crewflow task check` names what is missing about the field, and the
// run of a task with a reason in every line of it goes on (docs/DESIGN.md §7f).
func TestCheckReadyRefusesAPathWithoutAReason(t *testing.T) {
	for _, language := range []string{"ru", "en"} {
		t.Run(language, func(t *testing.T) {
			found := taskOf(43, language)
			found.Body = withLines(t, language, reading[language].marker+" /usr/local/go")

			err := CheckReady(found, projectOf(language, "none"))

			missing := missingOf(t, err)
			if len(missing) != 1 || !strings.Contains(missing[0], "has no reason") {
				t.Errorf("missing = %v, want the line of the field without a reason", missing)
			}
		})
	}
}

// TestCheckReadyATaskThatAsksForNothing: the field most tasks answer is not a field
// that has to be written. A task that says nothing, or that says there is nothing to
// read, is ready, and the run of it gets what the file of the project gives and
// nothing more (docs/DESIGN.md §7d, §7f).
func TestCheckReadyATaskThatAsksForNothing(t *testing.T) {
	for _, language := range []string{"ru", "en"} {
		for _, without := range []string{"", "field"} {
			t.Run(language+"/"+without, func(t *testing.T) {
				err := CheckReady(taskOf(43, language, without), projectOf(language, "none"))

				if err != nil {
					t.Errorf("CheckReady of a task that asks for nothing = %v, want no error", err)
				}
			})
		}
	}
}

// TestReadingsIsNotReadOutOfTheSpecification: the field belongs to the technical part,
// which is folded away for the executor, and a heading of the same name above the fold
// is a section a person writes for a person. Crewflow opens what the technical part
// says, and not what a person wrote above it (docs/DESIGN.md §7f).
func TestReadingsIsNotReadOutOfTheSpecification(t *testing.T) {
	heading := reading["en"].heading
	body := "## Why\n\na person needs it\n\n" +
		"### " + heading + "\n\n" + reading["en"].marker + " /usr/local/go — above the fold\n\n" +
		"<details>\n<summary>Technical part</summary>\n\n" +
		"### " + heading + "\n\n" + reading["en"].marker + " /opt/sdk — inside the fold\n\n" +
		"</details>\n"

	got, complaints := Read(body, "en").Readings()

	if len(complaints) != 0 {
		t.Errorf("the complaints = %v, want none", complaints)
	}
	want := []Reading{{Path: "/opt/sdk", Reason: "inside the fold"}}
	if !slices.Equal(got, want) {
		t.Errorf("the readings = %+v, want only the one of the technical part %+v", got, want)
	}
}

// withLines is a whole task of a project with the answer of its reading field written
// out by a person, where the template asks for "nothing".
func withLines(t *testing.T, language string, lines ...string) string {
	t.Helper()
	whole := taskOf(43, language).Body
	fold, answer, isFolded := strings.Cut(whole, nothing[language][0]+"\n\n</details>")
	if !isFolded {
		t.Fatalf("the body of a task has no reading field in it:\n%s", whole)
	}
	return fold + strings.Join(lines, "\n") + "\n\n</details>" + answer
}

// linesIn is the lines of a case with the word of the field of the language put in
// where the case wrote "%s".
func linesIn(lines []string, language string) []string {
	said := make([]string, 0, len(lines))
	for _, line := range lines {
		said = append(said, strings.ReplaceAll(line, "%s", reading[language].marker))
	}
	return said
}
