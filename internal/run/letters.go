package run

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ellipsis is what stands where the part of a title that did not fit was cut off.
const ellipsis = "…"

// escape is a sequence of characters a terminal reads as a command and not as letters:
// the colours, the moving of the cursor and the wiping of the screen. A title of a
// task is a line of text off the host, and one that carries a command in it is one
// that paints over whatever the person who reads the list is looking at.
var escape = regexp.MustCompile("\x1b(?:[@-Z\\\\-_]|\\[[0-?]*[ -/]*[@-~])")

// width is how many columns of a terminal a word takes. A Cyrillic letter is a letter
// and takes one, an emoji is a picture and takes two, and what is joined to what stands
// beside it takes no room of its own: a table of titles in any language is a table of
// words, and the columns of it have to line up.
func width(word string) int {
	var m measure
	for _, letter := range word {
		m.of(letter)
	}
	return m.columns
}

// measure is the width of a word in the columns of a terminal, taken letter by letter,
// and the state of it that a letter is read in: what is joined to what stands beside it
// is one picture and not two, a tone is a shade of the letter before it and not a
// letter of its own, and a keycap is a sign in a box as wide as an emoji is.
type measure struct {
	columns int
	joined  bool
}

// of is one letter more of the word and the room it takes of it.
func (m *measure) of(letter rune) int {
	switch {
	case letter == joiner:
		m.joined = true
		return 0
	case m.joined:
		m.joined = false
		return 0
	case isTone(letter):
		return 0
	case letter == keycap && m.columns > 0:
		m.columns++
		return 1
	}
	room := letterWidth(letter)
	m.columns += room
	return room
}

// joiner is what glues the emoji of one picture together, a tone is what shades the
// emoji it follows, and a keycap is the box around a sign.
const (
	joiner   = '\u200d'
	toneFrom = '\U0001f3fb'
	toneTo   = '\U0001f3ff'
	keycap   = '\u20e3'
)

// isTone is whether the rune is one of the shades a person may have, which stand on the
// emoji before them and are not a picture of their own.
func isTone(letter rune) bool {
	return letter >= toneFrom && letter <= toneTo
}

// letterWidth is how many columns one rune takes in a terminal.
func letterWidth(letter rune) int {
	switch {
	case letter < 0x20, letter == 0x7f:
		// A letter that is a command to the terminal is no letter at all.
		return 0
	case unicode.In(letter, unicode.Mn, unicode.Me, unicode.Cf):
		// A mark on a letter, a selector of how a letter looks and an instruction
		// between letters are all drawn on top of the letters around them.
		return 0
	case isWide(letter):
		return 2
	default:
		return 1
	}
}

// wide is the letters a terminal draws in two columns: the scripts of the wide and full
// width classes of Unicode and the emoji. It is not every letter of the wide classes —
// a rare one is drawn in one and only mislines a table that has it in a title.
var wide = []rune{
	0x1100, 0x115f, // Hangul Jamo
	0x2e80, 0x303e, // CJK radicals, Kangxi, CJK symbols and punctuation
	0x3041, 0x33ff, // Kana, Bopomofo, Hangul compatibility, CJK compatibility
	0x3400, 0x4dbf, // CJK unified ideographs, extension A
	0x4e00, 0x9fff, // CJK unified ideographs
	0xa000, 0xa4cf, // Yi
	0xa960, 0xa97f, // Hangul Jamo, extended A
	0xac00, 0xd7a3, // Hangul syllables
	0xf900, 0xfaff, // CJK compatibility ideographs
	0xfe10, 0xfe19, // vertical forms
	0xfe30, 0xfe6f, // CJK compatibility forms, small form variants
	0xff00, 0xff60, // fullwidth forms
	0xffe0, 0xffe6, // fullwidth signs
	0x1f000, 0x1faff, // the emoji, and the symbols that are drawn like them
	0x20000, 0x3fffd, // CJK unified ideographs, extensions B to F
}

// isWide is whether a terminal draws the letter in two columns.
func isWide(letter rune) bool {
	if letter > utf8.MaxRune {
		return false
	}
	for pair := 0; pair < len(wide); pair += 2 {
		if letter > wide[pair+1] {
			continue
		}
		return letter >= wide[pair]
	}
	return false
}

// cut is a cell of a table that is not wider than the column it stands in: what does
// not fit is cut off and an ellipsis stands where it was. The cut is at the end of a
// whole word wherever a whole word is what fits, because a title that stops in the
// middle of a word is a title nobody can read, and one that stops without saying so is
// a title a bug has shortened. A title of one word longer than its column is cut in the
// middle of that word, for there is no whole word in it to stop at.
func cut(text string, columns int) string {
	if columns <= 0 || width(text) <= columns {
		return text
	}
	room := columns - width(ellipsis)
	if whole := strings.TrimSpace(wholeWords(text, room)); whole != "" {
		return whole + ellipsis
	}
	return strings.TrimRight(hardCut(text, room), " ") + ellipsis
}

// wholeWords is as much of a text as fits in so many columns at the ends of whole
// words, and nothing at all where not even its first word fits.
func wholeWords(text string, columns int) string {
	whole := ""
	for _, word := range strings.Split(text, " ") {
		candidate := word
		if whole != "" {
			candidate = whole + " " + word
		}
		if width(candidate) > columns {
			return whole
		}
		whole = candidate
	}
	return whole
}

// hardCut is as much of a text as fits in so many columns, letter by letter. A letter
// of two columns is not cut in half: what does not fit is not taken at all.
func hardCut(text string, columns int) string {
	var out strings.Builder
	soFar := measure{}
	for _, letter := range text {
		next := soFar
		next.of(letter)
		if next.columns > columns {
			break
		}
		soFar = next
		out.WriteRune(letter)
	}
	return out.String()
}

// line is a cell of a table that holds a title: a word of it is a word, and whatever
// else a title holds — a newline, a tab, a command to the terminal — is put in its
// place or taken out of it, because a title that breaks the line breaks every column
// under it, and a title that paints over them is a title a person never reads.
func line(text string) string {
	return strings.Map(func(letter rune) rune {
		if unicode.IsControl(letter) {
			return ' '
		}
		return letter
	}, escape.ReplaceAllString(text, ""))
}
