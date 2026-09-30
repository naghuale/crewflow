package task

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/naghuale/crewflow/internal/config"
	"github.com/naghuale/crewflow/internal/forge"
)

// headings are the words a project writes its sections in, by the role of the
// section (docs/DESIGN.md §7f). A test names a section by its role and gets the
// words of the language, so that the same test reads a Russian task and an
// English one.
var headings = map[string]map[string]string{
	"ru": {
		"why":   "Зачем",
		"what":  "Что изменится",
		"check": "Как проверить самому",
		"out":   "Что не входит",
		"risks": "Риски и решения",
	},
	"en": {
		"why":   "Why",
		"what":  "What changes",
		"check": "How to check it yourself",
		"out":   "Out of scope",
		"risks": "Risks and decisions",
	},
}

// technical are the words of the two parts of the technical part of a task.
var technical = map[string]map[string]string{
	"ru": {"criteria": "Критерии приёмки", "boundaries": "Границы"},
	"en": {"criteria": "Acceptance criteria", "boundaries": "Boundaries"},
}

// reading are the words of the field of the technical part that says what the task may
// read outside its work folder: the heading and the word every line of it starts with.
var reading = map[string]struct{ heading, marker string }{
	"ru": {heading: "Что читать вне рабочей папки и зачем", marker: "читать:"},
	"en": {heading: "What to read outside the work folder and why", marker: "read:"},
}

// texts stands in for the words of a person under every section: enough of them
// for a section to be filled, whatever the language it is written in.
var texts = map[string]string{
	"why":   "A person has to build a long command by hand for every task.",
	"what":  "The person runs one command and sees how the run ended.",
	"check": "1. Run the command on a task that is not there. 2. See the error.",
	"out":   "Review and merge are other commands.",
	"risks": "A run creates a branch and a worktree; main is not touched.",
}

// body assembles the body of a task as a person writes it: the sections of the
// specification, without the ones named in without, and the technical part
// folded away for the executor. A name may say what to leave out of a section
// instead of the section itself: "empty:check" leaves the section there with
// nothing under it but a comment, which is how a task that was not filled in
// looks. A name of "field" leaves out the field that says what the task may read
// outside its work folder, which is how a task written before that field looked.
func body(language string, without ...string) string {
	gone := make(map[string]bool, len(without))
	for _, name := range without {
		gone[name] = true
	}
	var out strings.Builder
	for _, role := range []string{"why", "what", "check", "out", "risks"} {
		if gone[role] {
			continue
		}
		fmt.Fprintf(&out, "## %s\n\n", headings[language][role])
		if gone["empty:"+role] {
			out.WriteString("<!-- the owner did not fill this in -->\n\n")
			continue
		}
		fmt.Fprintf(&out, "%s\n\n", texts[role])
	}
	if gone["details"] {
		return out.String()
	}
	out.WriteString("<details>\n<summary>Technical part for the executor</summary>\n\n")
	if !gone["criteria"] {
		fmt.Fprintf(&out, "### %s\n\n", technical[language]["criteria"])
		if gone["item"] {
			out.WriteString("- the work is done\n\n")
		} else {
			out.WriteString("- [ ] the run opens a change request\n\n")
		}
	}
	if !gone["boundaries"] {
		fmt.Fprintf(&out, "### %s\n\nPaths the task changes:\n\n```\n", technical[language]["boundaries"])
		if !gone["paths"] {
			out.WriteString("cmd/crewflow/**\ninternal/task/**\n")
		}
		out.WriteString("```\n\n")
	}
	if !gone["field"] {
		fmt.Fprintf(&out, "### %s\n\n%s\n\n", reading[language].heading, nothing[language][0])
	}
	out.WriteString("</details>\n")
	return out.String()
}

// taskOf returns the task a test runs: the body it assembled and nothing else,
// so that a test says what about the task it means.
func taskOf(number int, language string, without ...string) forge.Task {
	return forge.Task{
		Number: number,
		Title:  "crewflow task run",
		Body:   body(language, without...),
		State:  "open",
	}
}

// projectOf is the settings of a project that writes its tasks in the language
// and waits for no approval, which is what a test changes one key at a time.
func projectOf(language, approval string) config.Config {
	return config.Config{
		Project: config.Project{Repo: "naghuale/crewflow", Language: language},
		Tasks:   config.Tasks{OwnerApproval: approval},
	}
}

// TestCheckReadyWholeTask checks that a task written the way the template asks
// is ready in either language, and that nothing is said about it.
func TestCheckReadyWholeTask(t *testing.T) {
	for _, language := range []string{"ru", "en"} {
		t.Run(language, func(t *testing.T) {
			err := CheckReady(taskOf(43, language), projectOf(language, "none"))

			if err != nil {
				t.Errorf("CheckReady of a whole task = %v, want no error", err)
			}
		})
	}
}

// TestCheckReadyMissingSection walks the sections of the specification one by
// one: a section that is not there at all is what is missing, and the error
// names it as the project writes it, so that a person can go and write it.
func TestCheckReadyMissingSection(t *testing.T) {
	for _, language := range []string{"ru", "en"} {
		for _, role := range []string{"why", "what", "check", "out", "risks"} {
			t.Run(language+"/"+headings[language][role], func(t *testing.T) {
				err := CheckReady(taskOf(43, language, role), projectOf(language, "none"))

				missing := missingOf(t, err)
				if !slices.Equal(missing, []string{headings[language][role]}) {
					t.Errorf("missing = %v, want the section %q and nothing else", missing, headings[language][role])
				}
			})
		}
	}
}

// TestCheckReadyEmptySection: a section that is there and holds nothing but a
// comment is the section of a task nobody filled in, and it is missing just the
// same.
func TestCheckReadyEmptySection(t *testing.T) {
	err := CheckReady(taskOf(43, "ru", "empty:risks"), projectOf("ru", "none"))

	missing := missingOf(t, err)
	if !slices.Equal(missing, []string{headings["ru"]["risks"]}) {
		t.Errorf("missing = %v, want the empty section %q", missing, headings["ru"]["risks"])
	}
}

// TestCheckReadyTechnicalPart walks what the folded-away part of the task must
// hold: the block itself, the acceptance criteria with an item in them, and the
// boundaries with a path to change.
func TestCheckReadyTechnicalPart(t *testing.T) {
	cases := []struct {
		name    string
		without []string
		want    string
	}{
		{
			name:    "no technical part at all",
			without: []string{"details"},
			want:    "the technical part of the task, folded away in <details>",
		},
		{
			name:    "no acceptance criteria",
			without: []string{"criteria"},
			want:    "Acceptance criteria",
		},
		{
			name:    "acceptance criteria with no item to tick",
			without: []string{"item"},
			want:    "- [ ]",
		},
		{
			name:    "no boundaries",
			without: []string{"boundaries"},
			want:    "Boundaries",
		},
		{
			name:    "boundaries with no path in them",
			without: []string{"paths"},
			want:    "a code block with at least one path",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckReady(taskOf(43, "en", tc.without...), projectOf("en", "none"))

			missing := missingOf(t, err)
			if len(missing) != 1 || !strings.Contains(missing[0], tc.want) {
				t.Errorf("missing = %v, want one thing that mentions %q", missing, tc.want)
			}
		})
	}
}

// TestCheckReadyNamesThePartsInTheLanguageOfTheProject: a Russian task is
// reported in the words its author used, or a person has to guess which heading
// crewflow wants.
func TestCheckReadyNamesThePartsInTheLanguageOfTheProject(t *testing.T) {
	err := CheckReady(taskOf(43, "ru", "criteria", "paths"), projectOf("ru", "none"))

	missing := missingOf(t, err)
	for _, want := range []string{technical["ru"]["criteria"], technical["ru"]["boundaries"]} {
		if !slices.ContainsFunc(missing, func(entry string) bool { return strings.Contains(entry, want) }) {
			t.Errorf("missing = %v, want it to name %q", missing, want)
		}
	}
}

// TestCheckReadyApproval walks what [tasks] owner_approval means: a mode that
// waits for every task, one that waits only for the risky ones, and none at
// all, against the labels a task carries.
func TestCheckReadyApproval(t *testing.T) {
	cases := []struct {
		mode   string
		labels []string
		ready  bool
	}{
		{mode: "all", ready: false},
		{mode: "all", labels: []string{"risky"}, ready: false},
		{mode: "all", labels: []string{"approved"}, ready: true},
		{mode: "all", labels: []string{"risky", "approved"}, ready: true},
		{mode: "risky", ready: true},
		{mode: "risky", labels: []string{"risky"}, ready: false},
		{mode: "risky", labels: []string{"approved"}, ready: true},
		{mode: "risky", labels: []string{"risky", "approved"}, ready: true},
		{mode: "none", ready: true},
		{mode: "none", labels: []string{"risky"}, ready: true},
		{mode: "none", labels: []string{"approved"}, ready: true},
		{mode: "none", labels: []string{"risky", "approved"}, ready: true},
	}
	for _, tc := range cases {
		t.Run(tc.mode+"/"+strings.Join(tc.labels, "+"), func(t *testing.T) {
			task := taskOf(43, "en")
			task.Labels = tc.labels

			err := CheckReady(task, projectOf("en", tc.mode))

			if tc.ready {
				if err != nil {
					t.Errorf("CheckReady with the labels %v = %v, want no error", tc.labels, err)
				}
				return
			}
			missing := missingOf(t, err)
			if !slices.ContainsFunc(missing, func(entry string) bool {
				return strings.Contains(entry, "approved")
			}) {
				t.Errorf("missing = %v, want it to say that the owner has not approved the task", missing)
			}
		})
	}
}

// TestCheckReadyClosed: a closed task is already done, and there is nothing to
// run, however well it is written.
func TestCheckReadyClosed(t *testing.T) {
	task := taskOf(43, "en")
	task.State = "closed"

	err := CheckReady(task, projectOf("en", "none"))

	missing := missingOf(t, err)
	if len(missing) != 1 || !strings.Contains(missing[0], "closed") {
		t.Errorf("missing = %v, want only that the task is closed", missing)
	}
}

// TestCheckReadyUnknownLanguage: a project that says a language crewflow does
// not know is read the way the rest of the world writes a task, in English, so
// that a task is not refused for the words it is written in.
func TestCheckReadyUnknownLanguage(t *testing.T) {
	err := CheckReady(taskOf(43, "en"), projectOf("klingon", "none"))

	if err != nil {
		t.Errorf("CheckReady of an English task with an unknown language = %v, want no error", err)
	}
}

// TestReadHoldsTheBody checks that the sections of a task are read the way a
// person sees them: the text under the heading, and not the comments of the
// template.
func TestReadHoldsTheBody(t *testing.T) {
	spec := Read(body("en"), "en")

	text, ok := spec.Text("Why")
	if !ok {
		t.Fatalf("the section \"Why\" was not found in %v", spec.sections)
	}
	if text != texts["why"] {
		t.Errorf("the text of the section = %q, want %q", text, texts["why"])
	}
	if _, ok := spec.Text("Nothing here"); ok {
		t.Error("a section that is not in the body was found")
	}
}

// TestReadSectionsAfterComments: a comment of the template inside a section is
// not what a person reads, and it is not the text of the section either.
func TestReadCommentsAreNotText(t *testing.T) {
	raw := "## Why\n\n<!-- write it here -->\n\n" + texts["why"] + "\n\n## Out of scope\n\n<!-- -->\n\n"

	spec := Read(raw, "en")

	if text, _ := spec.Text("Why"); !strings.Contains(text, texts["why"]) {
		t.Errorf("the text of the section = %q, want the words of the person in it", text)
	}
	if text, _ := spec.Text("Out of scope"); strings.TrimSpace(text) != "" {
		t.Errorf("the text of a section with one comment = %q, want it empty", text)
	}
}

// TestReadFoldsTheTechnicalPartAway: the headings of the technical part belong
// to the executor, and the sections of the specification are not looked for
// there.
func TestReadTechnicalPartIsSeparate(t *testing.T) {
	spec := Read(body("en"), "en")

	if !spec.HasTechnicalPart() {
		t.Error("the technical part was not found in a body that has one")
	}
	if !spec.HasCriteria() {
		t.Error("the acceptance criteria were not found in a body that has them")
	}
	for _, section := range spec.sections {
		belongsToExecutor := section.name == "Acceptance criteria" ||
			section.name == "Boundaries" ||
			section.name == reading["en"].heading
		if section.technical != belongsToExecutor {
			t.Errorf("the section %q is technical = %t, want %t", section.name, section.technical, belongsToExecutor)
		}
	}
}

// missingOf returns what the error says is missing, and fails the test when the
// error is not about readiness at all.
func missingOf(t *testing.T, err error) []string {
	t.Helper()
	if err == nil {
		t.Fatal("CheckReady returned no error, want one that says what is missing")
	}
	var notReady *ErrNotReady
	if !errors.As(err, &notReady) {
		t.Fatalf("CheckReady = %v, want an *ErrNotReady", err)
	}
	if len(notReady.Missing) == 0 {
		t.Errorf("ErrNotReady.Missing is empty, want what is missing")
	}
	return notReady.Missing
}
