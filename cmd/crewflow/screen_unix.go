//go:build unix

package main

import (
	"os"
	"syscall"
	"unsafe"
)

// columnsOf is how many columns wide the terminal of the machine is, and zero where
// the machine does not say. A terminal of a person is asked about its own size, and a
// terminal that will not answer — a terminal of a machine of a test, a terminal behind
// a multiplexer that keeps the size to itself — is read as wide as a person reads.
func columnsOf(file *os.File) int {
	var size struct {
		rows, columns, width, height uint16
	}
	// TIOCGWINSZ is the one question every terminal of a unix answers: how many lines
	// and how many columns it draws in. The answer is two numbers and nothing else.
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(),
		uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&size))); errno != 0 {
		return 0
	}
	return int(size.columns)
}
