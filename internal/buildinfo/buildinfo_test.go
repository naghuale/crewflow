package buildinfo

import "testing"

func TestStringDefaults(t *testing.T) {
	if got, want := String(), "crewflow dev (commit unknown, built unknown)"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestStringStamped(t *testing.T) {
	version, commit, date := Version, Commit, Date
	t.Cleanup(func() { Version, Commit, Date = version, commit, date })

	Version, Commit, Date = "1.2.3", "deadbeef", "2026-09-28T09:00:00Z"
	want := "crewflow 1.2.3 (commit deadbeef, built 2026-09-28T09:00:00Z)"
	if got := String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
