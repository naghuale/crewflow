package changelog

import (
	"regexp"
	"strconv"
	"strings"
)

// link is the label of a link of a line of the journal: `[#128]`. The lines of the
// journal name the task and the change with links, so that a person follows the line to
// both of them and a check can tell which task a line belongs to.
var link = regexp.MustCompile(`\[#(\d+)\]`)

// linkLabels are the numbers the line of the journal names with a link, in the order
// they stand in it. A line may name the change of another task and its own: only its own
// says that the line came from there.
func linkLabels(text string) []int {
	var labels []int
	for _, match := range link.FindAllStringSubmatch(text, -1) {
		number, err := strconv.Atoi(match[1])
		if err != nil {
			continue
		}
		labels = append(labels, number)
	}
	return labels
}

// oneLine is a line of the journal as one text, with every run of spaces and every end
// of line in it turned into one space: two lines of the journal that say the same thing
// and are wrapped at another place are one line, and a check of duplicates has to see
// that they are the same one.
func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
