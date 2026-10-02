package task

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Boundaries are the paths a task may change, as globs. A task that changes a file
// outside of them went further than it was asked to, and that is an outcome of a
// run of its own: the work is not reviewed before it is found out
// (docs/DESIGN.md §7c).
type Boundaries []string

// Outside returns the files that none of the boundaries covers, in the order they
// were given. A file the run changed that no glob of the task covers is a file the
// task was not to change.
func (b Boundaries) Outside(files []string) ([]string, error) {
	globs, err := b.compile()
	if err != nil {
		return nil, err
	}
	var outside []string
	for _, file := range files {
		if !slices.ContainsFunc(globs, func(glob *regexp.Regexp) bool { return glob.MatchString(file) }) {
			outside = append(outside, file)
		}
	}
	return outside, nil
}

// Covers is whether one of the boundaries names the file: the question a run asks of
// every file it changed, and the question the admission of a pair asks of every file one
// of its tasks may change for the other (docs/DESIGN.md §7c).
func (b Boundaries) Covers(file string) bool {
	globs, err := b.compile()
	if err != nil {
		// A glob crewflow cannot compile is a glob nobody may be told a file is under,
		// and a boundary that says nothing covers nothing.
		return false
	}
	return slices.ContainsFunc(globs, func(glob *regexp.Regexp) bool { return glob.MatchString(file) })
}

// compile is every boundary as a matcher. A glob of a task is written by a person
// in a path syntax of the shell, where `**` is not special either, so crewflow
// spells out what it means by it: `**` crosses folders, `*` and `?` do not.
func (b Boundaries) compile() ([]*regexp.Regexp, error) {
	globs := make([]*regexp.Regexp, 0, len(b))
	for _, boundary := range b {
		glob, err := regexp.Compile(globPattern(boundary))
		if err != nil {
			return nil, fmt.Errorf("boundary %q is not a glob crewflow understands: %w", boundary, err)
		}
		globs = append(globs, glob)
	}
	return globs, nil
}

// globPattern is what a boundary means as a matcher, anchored at both ends so that
// a folder is not a part of a file's name: `internal/**` is a folder and everything
// under it, `internal/*.go` is the Go files of that folder and no subfolder of it.
func globPattern(boundary string) string {
	segments := strings.Split(boundary, "/")
	var out strings.Builder
	out.WriteString("^")
	for i, segment := range segments {
		last := i == len(segments)-1
		switch segment {
		case "**":
			if last {
				out.WriteString(".*")
			} else {
				// The slash of the segment after this one is part of the match, so
				// that `a/**/b` is `a/b` and `a/x/b` alike, and not `a/x//b`.
				out.WriteString("(?:[^/]+/)*")
				continue
			}
		default:
			out.WriteString(segmentPattern(segment))
		}
		if !last {
			out.WriteString("/")
		}
	}
	out.WriteString("$")
	return out.String()
}

// segmentPattern is one folder of a boundary: a star and a question mark stand for
// what they stand for in a path, and every other character is itself.
func segmentPattern(segment string) string {
	var out strings.Builder
	for _, char := range segment {
		switch char {
		case '*':
			out.WriteString("[^/]*")
		case '?':
			out.WriteString("[^/]")
		default:
			out.WriteString(regexp.QuoteMeta(string(char)))
		}
	}
	return out.String()
}
