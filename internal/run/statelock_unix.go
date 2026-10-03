//go:build unix

package run

import (
	"fmt"
	"os"
	"syscall"
)

// holdFile takes the lock of a file and answers how to give it back. The lock is the one of
// the machine — `flock` — and not a folder crewflow makes and forgets about: the machine
// takes the lock off when the process that holds it is gone, so a run of crewflow that was
// killed in the middle leaves nothing behind that the next one would wait for.
//
// Two writers of one file in one process exclude each other here, because the lock is on
// the open file and not on the process: a test that writes one state of a task from two
// goroutines at once is held back by the same lock two commands are.
func holdFile(file *os.File) (func(), error) {
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return nil, fmt.Errorf("flock: %w", err)
	}
	return func() { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN) }, nil
}
