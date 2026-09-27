// Package buildinfo holds the version, the commit and the build time that the
// build stamps into the binary.
package buildinfo

import "fmt"

// Version, Commit and Date are set at link time, for example:
//
//	go build -ldflags "-X github.com/naghuale/crewflow/internal/buildinfo.Version=1.2.3"
//
// A binary that was not stamped says so instead of guessing.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// String is the one line that "crewflow version" prints.
func String() string {
	return fmt.Sprintf("crewflow %s (commit %s, built %s)", Version, Commit, Date)
}
