//go:build !unix

package run

import (
	"fmt"
	"os"
)

// holdFile refuses: a machine that cannot lock a file cannot write a state of a task
// without taking a record of another command away — `UpdateState` would be a promise
// crewflow cannot keep there. The refusal says so rather than writing anyway, because a
// record lost without a word about it is worse than a record that was not written.
func holdFile(*os.File) (func(), error) {
	return nil, fmt.Errorf("this machine cannot lock a file, and a state of a task written without a lock " +
		"loses the record of the command that wrote beside it: crewflow writes no state of a task here")
}
