package changelog

import (
	"errors"
	"strings"
	"testing"
)

// A fragment of a task as a person writes it: the sections of the journal and the lines
// for a human being, with the link to the task at the end of every line.
const fragmentOfTheTest = `### Добавлено

- **Фрагменты журнала.** Задача пишет свой файл и больше никого.
  (задача [#124](https://github.com/naghuale/crewflow/issues/124))

### Исправлено

- **Порядок строк.** Строка об изношенной резине.
  ([#125](https://github.com/naghuale/crewflow/pull/125), задача [#124](https://github.com/naghuale/crewflow/issues/124))
`

// A journal as a project has it: the head that says how it is written, the unreleased
// part with the sections, the version below and the link at the end.
const journalOfTheTest = `# Changelog

Здесь — что изменилось в каждой версии.

## [Не выпущено]

### Добавлено

- строка, написанная руками

## [v0.1.0] - 2026-10-01

### Добавлено

- строка выпуска
  ([#1](https://github.com/naghuale/crewflow/pull/1), задача [#1](https://github.com/naghuale/crewflow/issues/1))

[Не выпущено]: https://github.com/naghuale/crewflow/commits/main
`

// TestParse: the fragment of a task is the sections of the journal and the lines of a
// person, and every line keeps the link to its own task.
func TestParse(t *testing.T) {
	fragment, err := Parse("124.md", []byte(fragmentOfTheTest), Russian)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if fragment.Task != 124 {
		t.Errorf("Parse read the task %d, want 124", fragment.Task)
	}
	if fragment.Path != "changelog.d/124.md" {
		t.Errorf("Parse read the path %q, want changelog.d/124.md", fragment.Path)
	}
	want := []Entry{
		{Section: "Добавлено", Text: "- **Фрагменты журнала.** Задача пишет свой файл и больше никого.\n  (задача [#124](https://github.com/naghuale/crewflow/issues/124))", Line: 3},
		{Section: "Исправлено", Text: "- **Порядок строк.** Строка об изношенной резине.\n  ([#125](https://github.com/naghuale/crewflow/pull/125), задача [#124](https://github.com/naghuale/crewflow/issues/124))", Line: 8},
	}
	if len(fragment.Entries) != len(want) {
		t.Fatalf("Parse read %d lines, want %d: %+v", len(fragment.Entries), len(want), fragment.Entries)
	}
	for i, entry := range fragment.Entries {
		if entry != want[i] {
			t.Errorf("Parse read the line %d as %+v, want %+v", i, entry, want[i])
		}
	}
}

// TestParseOfEveryWrongFragment: a fragment that is not a fragment is refused with the
// file and the line in it, so that the person who wrote it sees what to fix.
func TestParseOfEveryWrongFragment(t *testing.T) {
	cases := []struct {
		name    string
		file    string
		content string
		want    string
	}{
		{
			name:    "a name that is not the number of a task",
			file:    "notes.md",
			content: fragmentOfTheTest,
			want:    "changelog.d/notes.md: the name of a fragment is the number of the task and \".md\"",
		},
		{
			name:    "a name that is not a number",
			file:    "v2.md",
			content: fragmentOfTheTest,
			want:    "changelog.d/v2.md: the name of a fragment is the number of the task and \".md\"",
		},
		{
			name:    "a heading of the file",
			file:    "124.md",
			content: "# Changelog\n\n" + fragmentOfTheTest,
			want:    "changelog.d/124.md:1: a fragment holds sections of the journal, not a heading",
		},
		{
			name:    "a section the journal has not got",
			file:    "124.md",
			content: "### Убрано\n\n- строка\n  (задача [#124](https://github.com/naghuale/crewflow/issues/124))\n",
			want:    `changelog.d/124.md:1: "Убрано" is not a section of the journal`,
		},
		{
			name:    "a line that is not a line of the journal",
			file:    "124.md",
			content: "### Добавлено\n\n- строка\n  (задача [#124](https://github.com/naghuale/crewflow/issues/124))\nа это не строка журнала\n",
			want:    "changelog.d/124.md:5: a line of the journal begins with \"- \"",
		},
		{
			name:    "a line without a link to its own task",
			file:    "124.md",
			content: "### Добавлено\n\n- строка без ссылки\n",
			want:    "changelog.d/124.md:3: the line names no link to the task [#124] it belongs to",
		},
		{
			name:    "a line with the link of another task only",
			file:    "124.md",
			content: "### Добавлено\n\n- строка\n  ([#77](https://github.com/naghuale/crewflow/issues/77))\n",
			want:    "changelog.d/124.md:3: the line names no link to the task [#124] it belongs to",
		},
		{
			name:    "an empty bullet",
			file:    "124.md",
			content: "### Добавлено\n\n- \n",
			want:    "changelog.d/124.md:3: the line names no link to the task [#124] it belongs to",
		},
		{
			name:    "a fragment without a line",
			file:    "124.md",
			content: "### Добавлено\n",
			want:    "changelog.d/124.md: the fragment holds no line of the journal",
		},
		{
			name:    "a fragment without a section",
			file:    "124.md",
			content: "- строка\n  (задача [#124](https://github.com/naghuale/crewflow/issues/124))\n",
			want:    "changelog.d/124.md: the fragment names no section of the journal",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(c.file, []byte(c.content), Russian)
			if err == nil {
				t.Fatalf("Parse(%s) read a fragment, want a refusal", c.file)
			}
			if !errors.Is(err, ErrFormat) {
				t.Errorf("Parse(%s) refused with %v, want the reason ErrFormat", c.file, err)
			}
			if got := err.Error(); got != c.want {
				t.Errorf("Parse(%s) refused with\n%q\nwant\n%q", c.file, got, c.want)
			}
		})
	}
}

// TestParseInEnglish: the sections of a journal of an English project are its own, and a
// Russian heading in such a fragment is a refusal — the journal of a project is read by
// the people of that project.
func TestParseInEnglish(t *testing.T) {
	fragment, err := Parse("7.md", []byte("### Added\n\n- a line\n  (task [#7](https://github.com/naghuale/crewflow/issues/7))\n"), English)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if fragment.Entries[0].Section != "Added" {
		t.Errorf("Parse read the section %q, want Added", fragment.Entries[0].Section)
	}
	if _, err := Parse("7.md", []byte("### Добавлено\n\n- a line\n  (task [#7](https://github.com/naghuale/crewflow/issues/7))\n"), English); !errors.Is(err, ErrFormat) {
		t.Errorf("Parse of a Russian section in an English journal refused with %v, want ErrFormat", err)
	}
}

// TestBodyWritesSectionsInTheOrderOfTheJournal: the sections stand as the journal keeps
// them whatever order the fragments name them in, and inside a section the lines stand in
// the order of the fragments.
func TestBodyWritesSectionsInTheOrderOfTheJournal(t *testing.T) {
	fragments := []Fragment{
		{Task: 128, Entries: []Entry{
			{Section: "Изменено", Text: "- второй, слит позже\n  (задача [#128](https://github.com/naghuale/crewflow/issues/128))"},
			{Section: "Добавлено", Text: "- первый, слит позже\n  (задача [#128](https://github.com/naghuale/crewflow/issues/128))"},
		}},
		{Task: 77, Entries: []Entry{
			{Section: "Добавлено", Text: "- третий, слит раньше\n  (задача [#77](https://github.com/naghuale/crewflow/issues/77))"},
			{Section: "Безопасность", Text: "- в безопасности\n  (задача [#77](https://github.com/naghuale/crewflow/issues/77))"},
		}},
	}
	want := `### Добавлено

- первый, слит позже
  (задача [#128](https://github.com/naghuale/crewflow/issues/128))

- третий, слит раньше
  (задача [#77](https://github.com/naghuale/crewflow/issues/77))

### Изменено

- второй, слит позже
  (задача [#128](https://github.com/naghuale/crewflow/issues/128))

### Безопасность

- в безопасности
  (задача [#77](https://github.com/naghuale/crewflow/issues/77))`
	if got := Body(fragments, Russian); got != want {
		t.Errorf("Body wrote\n%q\nwant\n%q", got, want)
	}
}

// TestBodyOfNothingIsNothing: a section no line went into is not written, so that a
// journal with nothing unreleased in it has no section under the heading, and a build
// over it writes the same bytes it wrote last time.
func TestBodyOfNothingIsNothing(t *testing.T) {
	if got := Body(nil, Russian); got != "" {
		t.Errorf("Body of no fragments wrote %q, want nothing", got)
	}
	only := []Fragment{{Task: 7, Entries: []Entry{{Section: "Исправлено", Text: "- только\n  (задача [#7](x))"}}}}
	if got, want := Body(only, Russian), "### Исправлено\n\n- только\n  (задача [#7](x))"; got != want {
		t.Errorf("Body wrote\n%q\nwant\n%q", got, want)
	}
}

// TestAssembleKeepsTheVersionsAndDropsTheHand: what stands above the unreleased heading
// and every version below it stay as they are, and what stands under the heading is
// written afresh — the journal is normalized by the build, so that a hand edit cannot
// travel into a release.
func TestAssembleKeepsTheVersionsAndDropsTheHand(t *testing.T) {
	body := "### Добавлено\n\n- своя строка\n  (задача [#124](https://github.com/naghuale/crewflow/issues/124))"
	assembled, err := Assemble([]byte(journalOfTheTest), body, Russian)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	want := strings.Replace(journalOfTheTest,
		"### Добавлено\n\n- строка, написанная руками\n",
		body+"\n", 1)
	if got := string(assembled); got != want {
		t.Errorf("Assemble wrote\n%q\nwant\n%q", got, want)
	}
}

// TestAssembleTwiceIsTheSame: the journal that was built is the journal a build over it
// writes again — nothing of the build depends on the moment of it or on the order of the
// files on the disk (CL-005, CA-006).
func TestAssembleTwiceIsTheSame(t *testing.T) {
	body := "### Добавлено\n\n- своя строка\n  (задача [#124](https://github.com/naghuale/crewflow/issues/124))"
	once, err := Assemble([]byte(journalOfTheTest), body, Russian)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	twice, err := Assemble(once, body, Russian)
	if err != nil {
		t.Fatalf("Assemble of the built journal: %v", err)
	}
	if string(once) != string(twice) {
		t.Errorf("the second build wrote\n%q\nwant\n%q", twice, once)
	}
}

// TestAssembleOfAJournalWithoutTheEnds: a build writes between the heading of the
// unreleased part and the first version, and a journal without one of those ends is
// refused instead of having one written for it.
func TestAssembleOfAJournalWithoutTheEnds(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{name: "no unreleased part", content: "# Changelog\n\n## [v0.1.0] - 2026-10-01\n\n### Добавлено\n\n- строка\n"},
		{name: "no version below", content: "# Changelog\n\n## [Не выпущено]\n\n### Добавлено\n\n- строка\n"},
		{name: "the heading of another language", content: "# Changelog\n\n## [Unreleased]\n\n## [v0.1.0] - 2026-10-01\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Assemble([]byte(c.content), "### Добавлено\n\n- строка", Russian)
			if !errors.Is(err, ErrNoUnreleased) {
				t.Errorf("Assemble refused with %v, want the reason ErrNoUnreleased", err)
			}
			if err != nil && !strings.Contains(err.Error(), c.content[:20]) && !strings.Contains(err.Error(), "CHANGELOG.md") {
				t.Errorf("Assemble refused with %q, want the file and the heading in it", err)
			}
		})
	}
}

// TestLanguageOf: a project that says nothing gets the English names, as everywhere else
// in crewflow, and a project that says Russian gets its own.
func TestLanguageOf(t *testing.T) {
	if got := LanguageOf("ru"); got != Russian {
		t.Errorf("LanguageOf(ru) = %q, want %q", got, Russian)
	}
	for _, language := range []string{"", "en", "de"} {
		if got := LanguageOf(language); got != English {
			t.Errorf("LanguageOf(%q) = %q, want %q", language, got, English)
		}
	}
	if got, want := Russian.Unreleased(), "## [Не выпущено]"; got != want {
		t.Errorf("the unreleased heading is %q, want %q", got, want)
	}
	if got, want := English.Unreleased(), "## [Unreleased]"; got != want {
		t.Errorf("the unreleased heading is %q, want %q", got, want)
	}
	if got := English.Sections(); len(got) != 4 || got[0] != "Added" {
		t.Errorf("the sections of an English journal are %q, want Added, Changed, Fixed, Security", got)
	}
}

// TestVersion: a release is a tag of the project, and a word that is not a version is
// refused — the number of a version is not something to guess at.
func TestVersion(t *testing.T) {
	for word, want := range map[string]string{
		"v0.1.0":    "0.1.0",
		"v10.20.30": "10.20.30",
	} {
		got, ok := Version(word)
		if !ok || got != want {
			t.Errorf("Version(%q) = %q, %v, want %q, true", word, got, ok, want)
		}
	}
	for _, word := range []string{"0.1.0", "v0.1", "v0.1.0.0", "vx.y.z", "v", "v0.1.0-rc1"} {
		if got, ok := Version(word); ok {
			t.Errorf("Version(%q) = %q, true, want a refusal", word, got)
		}
	}
}
