package main

import (
	"io"
	"os"
	"time"

	taskrun "github.com/naghuale/crewflow/internal/run"
)

// taskScreen is the screen `crewflow task list` is written for: the terminal the
// answer goes into, if it goes into one, and the moment the list is written for, which
// is what every age in the list is counted from. It is a variable so that a test of the
// command writes its list on a screen of its own and never on the terminal of the
// machine it runs on.
var taskScreen = screenOf

// screenOf is the screen one writer is, as the machine says: a terminal of a person is
// as wide as the terminal is, is looked at rather than read, and reads the sixteen
// colours; a file and a pipe are none of those, and a person who asked for plain
// letters with NO_COLOR, or whose terminal says it takes no colour, is answered in
// plain letters whatever else is true (docs/DESIGN.md §6).
func screenOf(w io.Writer, at time.Time) taskrun.Screen {
	screen := taskrun.Screen{At: at}
	file, isFile := w.(*os.File)
	if !isFile {
		return screen
	}
	// A terminal is a character device and nothing else is: a file, a pipe and a
	// socket are not, and asking the machine is the only way to tell them apart
	// without a library of the host.
	info, err := file.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return screen
	}
	screen.Terminal = true
	screen.Columns = columnsOf(file)
	screen.Painted = wantsColour(os.Getenv)
	return screen
}

// wantsColour is whether a terminal of a person is to be given the colours of a list:
// NO_COLOR is a person saying no (https://no-color.org), and TERM=dumb is a terminal
// saying it takes none, which is the same answer from the other side.
func wantsColour(env func(string) string) bool {
	if env("NO_COLOR") != "" {
		return false
	}
	return env("TERM") != "dumb"
}
