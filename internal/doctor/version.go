package doctor

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// versionPattern finds the first version in what a check command printed, which
// is the shape they all have: "go version go1.27.1 darwin/arm64",
// "golangci-lint has version 2.14.0 …". The numbers are what matters; the words
// around them are the tool's own.
var versionPattern = regexp.MustCompile(`\d+(\.\d+)+`)

// firstVersion is the first version in the output of a check command, or an
// empty string when the output holds no version to compare.
func firstVersion(output []byte) string {
	return versionPattern.FindString(string(output))
}

// versionAtLeast reports whether found is not older than min. The numbers are
// compared one part at a time, so that 1.27.1 satisfies 1.27 and 1.27 does not
// satisfy 1.27.1. It is an error when min holds no numbers to compare, which
// is a mistake in the project file and not something to guess about.
func versionAtLeast(found, min string) (bool, error) {
	want := numbersOf(min)
	if len(want) == 0 {
		return false, fmt.Errorf("no version in the min %q", min)
	}
	got := numbersOf(found)
	if len(got) == 0 {
		return false, fmt.Errorf("no version in %q", found)
	}
	for i, part := range want {
		if i >= len(got) {
			// A part the found version does not have is older than the min:
			// 1.27 is older than 1.27.1.
			return false, nil
		}
		if got[i] != part {
			return got[i] > part, nil
		}
	}
	return true, nil
}

// numbersOf are the parts of a version, as they are compared.
func numbersOf(version string) []int {
	match := versionPattern.FindString(version)
	if match == "" {
		return nil
	}
	parts := strings.Split(match, ".")
	numbers := make([]int, 0, len(parts))
	for _, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil {
			return nil
		}
		numbers = append(numbers, number)
	}
	return numbers
}
