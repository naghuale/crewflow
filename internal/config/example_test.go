package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// design is the design of crewflow itself, the one place a crewflow.toml is
// written down: §5 is the file a project is told to write.
const design = "../../docs/DESIGN.md"

// TestExampleIsTheExampleOfTheDesign keeps testdata/example.toml the example of
// DESIGN §5, letter for letter. A table that is added to the design and not to
// the example is a table no project learns about, and the other way round it is
// a key crewflow checks that the design does not promise.
func TestExampleIsTheExampleOfTheDesign(t *testing.T) {
	want := settingsExample(t)
	got, err := os.ReadFile(filepath.Join("testdata", "example.toml"))
	if err != nil {
		t.Fatalf("read testdata/example.toml: %v", err)
	}
	if string(got) != want {
		t.Errorf("testdata/example.toml is not the example of DESIGN §5:\n%s", difference(want, string(got)))
	}
}

// settingsExample is the first block of settings code of §5, which is the whole
// crewflow.toml of the design, written out.
func settingsExample(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(design)
	if err != nil {
		t.Fatalf("read %s: %v", design, err)
	}
	section, block, inSettings := false, []string{}, false
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case strings.HasPrefix(line, "## "):
			section, inSettings = strings.HasPrefix(line, "## 5."), false
		case section && strings.HasPrefix(line, "```toml"):
			inSettings = true
		case inSettings && line == "```":
			return strings.Join(block, "\n") + "\n"
		case inSettings:
			block = append(block, line)
		}
	}
	t.Fatalf("%s holds no crewflow.toml in §5, want the example of the design", design)
	return ""
}

// difference is the first line in which two texts are not the same, so that the
// test says which line is to be changed and leaves the rest to the reader.
func difference(want, got string) string {
	wantLines, gotLines := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := range max(len(wantLines), len(gotLines)) {
		w, g := lineAt(wantLines, i), lineAt(gotLines, i)
		if w != g {
			return fmt.Sprintf("line %d:\n  design: %q\n  file:   %q", i+1, w, g)
		}
	}
	return "the texts are the same line by line and differ in their end"
}

func lineAt(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return ""
}
