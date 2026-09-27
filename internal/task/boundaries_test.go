package task

import (
	"slices"
	"testing"
)

// TestBoundariesOutside walks the three answers a check of the boundaries of a
// task can give: everything is inside, `**` reaches into subfolders, and a file
// the task was not to change is named.
func TestBoundariesOutside(t *testing.T) {
	cases := []struct {
		name       string
		boundaries Boundaries
		files      []string
		want       []string
	}{
		{
			name:       "every file is inside",
			boundaries: Boundaries{"cmd/crewflow/**", "internal/task/**"},
			files:      []string{"cmd/crewflow/main.go", "cmd/crewflow/task.go", "internal/task/task.go"},
			want:       nil,
		},
		{
			name:       "two stars cross folders",
			boundaries: Boundaries{"internal/**/*.go", "docs/**"},
			files:      []string{"internal/task/task.go", "internal/run/profile/opencode.go", "docs/DESIGN.md"},
			want:       nil,
		},
		{
			name:       "a star stays inside one folder",
			boundaries: Boundaries{"internal/*.go"},
			files:      []string{"internal/task/task.go"},
			want:       []string{"internal/task/task.go"},
		},
		{
			name:       "a file outside every boundary",
			boundaries: Boundaries{"internal/task/**"},
			files:      []string{"internal/task/task.go", "docs/DESIGN.md", "go.mod"},
			want:       []string{"docs/DESIGN.md", "go.mod"},
		},
		{
			name:       "a folder is not a file of it",
			boundaries: Boundaries{"cmd/crewflow/**"},
			files:      []string{"cmd/crewflow"},
			want:       []string{"cmd/crewflow"},
		},
		{
			name:       "a boundary of everything",
			boundaries: Boundaries{"**"},
			files:      []string{"cmd/crewflow/main.go", "go.mod", "README.md"},
			want:       nil,
		},
		{
			name:       "no boundaries at all",
			boundaries: nil,
			files:      []string{"cmd/crewflow/main.go"},
			want:       []string{"cmd/crewflow/main.go"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			outside, err := tc.boundaries.Outside(tc.files)
			if err != nil {
				t.Fatalf("Outside returned an error: %v", err)
			}
			if !slices.Equal(outside, tc.want) {
				t.Errorf("Outside(%v) = %v, want %v", tc.files, outside, tc.want)
			}
		})
	}
}

// TestBoundariesOutsideNothingChanged: a run that changed nothing is inside its
// boundaries, however few they are.
func TestBoundariesOutsideNothingChanged(t *testing.T) {
	outside, err := Boundaries{"cmd/crewflow/**"}.Outside(nil)
	if err != nil {
		t.Fatalf("Outside returned an error: %v", err)
	}
	if len(outside) != 0 {
		t.Errorf("Outside = %v, want nothing outside: nothing was changed", outside)
	}
}

// TestBoundariesFromTheTask checks that the paths a run is held to are the ones
// the task itself wrote, one per line of its code block.
func TestBoundariesFromTheTask(t *testing.T) {
	spec := Read(body("en"), "en")

	want := Boundaries{"cmd/crewflow/**", "internal/task/**"}
	if got := spec.Boundaries(); !slices.Equal(got, want) {
		t.Errorf("Boundaries() = %v, want %v", got, want)
	}
}
