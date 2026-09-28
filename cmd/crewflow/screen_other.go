//go:build !unix

package main

import "os"

// columnsOf is zero on a machine whose terminals are not asked about their size: a
// list of runs is written as wide as a person reads, which is a table of letters in
// every terminal of it.
func columnsOf(*os.File) int {
	return 0
}
