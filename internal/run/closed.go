package run

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/naghuale/crewflow/internal/access"
	"github.com/naghuale/crewflow/internal/run/profile"
)

// The kinds of access a run can ask of a place of secrets, as a report and a journal
// name them, and one more for a refusal no call of the run says anything about. A place
// that is only named in the text of a command is named as such and is refused all the
// same: the command did not run, and crewflow does not claim to know what it would have
// done with it (docs/DESIGN.md §7d).
const (
	accessRead   = "read"
	accessWrite  = "write"
	accessCd     = "cd"
	accessText   = "named in text"
	accessSilent = "unknown"
)

// recoveryDisabled is what every refusal of a secret carries, in a report and in the
// journal of the run: a run that reached for a key is not one crewflow goes on by
// itself, whatever the habit of the command looks like, and the words say so where a
// person and a program read them (docs/DESIGN.md §7a.1, §8).
const recoveryDisabled = "recovery: disabled"

// reach is one place of secrets a run was refused, and what it asked of it: the place
// as the run wrote it, the kind of access the command refused asked of it, and the note
// a report and a journal hold about the refusal it came in with.
type reach struct {
	// place is the path of the secret, as the run named it.
	place string
	// kind is what the run asked of the place: read, write, cd, named in text, or
	// unknown where nothing the run wrote says.
	kind string
	// note is what a report shows after the refusal: the place, the kind of access and
	// the fact that crewflow does not go on with a run that asked.
	note string
}

// findings is what the refusals of a run reached: every place of secrets among them and
// in the commands they came in with, and the refusals themselves with a note on the ones
// that reached one. It is one answer and not two, because the outcome of a run that
// reached a secret and the report of the same run are one answer read twice
// (docs/DESIGN.md §7a.1, §7d, §8).
type findings struct {
	// marked are the refusals as a report and the journal of a run show them.
	marked []string
	// reached are the places of secrets the run reached, in the order it reached them.
	reached []reach
}

// line is what the journal of a run holds about a run that reached for a secret: every
// place, the kind of access, and that no run of it is continued by itself. It is written
// while the journal is open, so that a watch of the attempt shows it.
func (f findings) line() string {
	if len(f.reached) == 0 {
		return ""
	}
	var said strings.Builder
	fmt.Fprintf(&said, "crewflow: the executor reached for a secret: ")
	for i, one := range f.reached {
		if i > 0 {
			fmt.Fprintf(&said, ", ")
		}
		fmt.Fprintf(&said, "%s (%s)", one.place, one.kind)
	}
	fmt.Fprintf(&said, "; %s\n", recoveryDisabled)
	return said.String()
}

// closed is the answer of one question a run asks about what it was refused: did it
// reach a place of the machine that stays closed to the executor whatever the project
// wrote (docs/DESIGN.md §7d).
//
// The list is the one the access policy of the project resolved, and it is read back
// here for two reasons. The shapes of the habits a run may be answered by itself on are
// the shapes of a command, and a command that reads a key has the shape of a habit as
// easily as a heredoc that edits a file of the project has. And an attempt to reach a
// secret is an outcome of a run of its own: whatever ended the attempt and whatever the
// habit of the command looks like, it stops the run and reaches a person (§7a.1, §8).
type closed struct {
	// folders are the places of secrets as this machine holds them, in every spelling
	// of the machine: a folder under a link is one place in two names, and a run is
	// refused for the one it wrote.
	folders []string
	// patterns are the places that are the same on every machine — `**/.env` and what
	// it stands for — and a path is one of them by its last folder.
	patterns []string
	// home is the home of the person: what a leading "~" of a path of a run stands
	// for, and where the secrets of the machine are.
	home string
}

// newClosed is the places that stay closed on this machine, as the policy of the
// project of a run resolved them and as the run will read them: the folders of the
// machine and the patterns of every machine, with the home of the person to read a path
// of a run in the spelling a run wrote it in.
func newClosed(policy access.Policy, home string) closed {
	places := closed{home: home}
	for _, place := range policy.Deny {
		if _, tail, isPattern := strings.Cut(place, "**/"); isPattern {
			places.patterns = append(places.patterns, tail)
			continue
		}
		places.folders = append(places.folders, place)
	}
	return places
}

// found is what the refusals of a run reached among the places that stay closed, and the
// refusals themselves with a note on the ones that reached one: a report that shows a
// refusal of a key among refusals of a scratch folder reads as a habit, and the
// orchestrator decides on what it reads (docs/DESIGN.md §7a.1, §7d).
func (c closed) found(refusals []string, calls []profile.Call, worktree string) findings {
	answer := findings{marked: make([]string, 0, len(refusals))}
	for _, refusal := range refusals {
		reached := c.reached(refusedPath(refusal), calls, worktree)
		if len(reached) > 0 {
			refusal += reached[0].note
			answer.reached = append(answer.reached, reached...)
		}
		answer.marked = append(answer.marked, refusal)
	}
	return answer
}

// reached is every place of secrets one refusal of a run is about: the refused path
// itself, and the places the commands of the run that hold it name. A refused path is
// looked at as the run wrote it and as the machine holds it, and the commands are
// looked at too, because a command of a shell is refused whole: a run that was refused
// its scratch folder for a command that reads a key has been refused the key with it
// (docs/DESIGN.md §7a.1, §7d).
//
// The commands are looked for by the path without the glob an agent was refused — a
// refusal of `/tmp/*` is a refusal of every file under it, and the command that named
// one of them is the command the refusal came in with.
func (c closed) reached(path string, calls []profile.Call, worktree string) []reach {
	if path == "" {
		return nil
	}
	var found []reach
	if c.secret(path, worktree) {
		found = append(found, c.reachOf(path, calls, true))
	}
	named := plain(path)
	if named == "" {
		return found
	}
	for _, call := range calls {
		if !strings.Contains(call.Argument, named) {
			continue
		}
		for _, place := range c.placesIn(call.Argument) {
			if c.secret(place, worktree) {
				found = append(found, c.reachOf(place, calls, false))
			}
		}
	}
	return found
}

// reachOf is one place of secrets a run was refused, with the note a report holds about
// the refusal: the place, the kind of access the run asked of it, and that crewflow does
// not go on with a run that asked. inRefusal says whether the place is the path of the
// refusal itself or only named in the command the refusal came in with.
func (c closed) reachOf(place string, calls []profile.Call, inRefusal bool) reach {
	one := reach{place: place, kind: c.kindOf(place, calls)}
	if inRefusal {
		one.note = fmt.Sprintf(" — a secret, closed to the executor whatever the project wrote: %s (%s); %s",
			place, one.kind, recoveryDisabled)
		return one
	}
	// The refusal names something else — the scratch folder of the machine, most often —
	// and what a person has to see is the place inside the command.
	one.note = fmt.Sprintf(" — the command refused names a secret: %s (%s); %s",
		place, one.kind, recoveryDisabled)
	return one
}

// kindOf is what a run asked of a place of secrets, as the calls of the run that name it
// show, and unknown where none of them does: a refusal crewflow cannot read the kind of
// is a refusal crewflow refuses all the same (docs/DESIGN.md §7d).
func (c closed) kindOf(place string, calls []profile.Call) string {
	written, named := place, plain(place)
	for _, call := range calls {
		if !strings.Contains(call.Argument, written) && !strings.Contains(call.Argument, named) {
			continue
		}
		if kind := kindOfCall(call, place); kind != accessSilent {
			return kind
		}
	}
	return accessSilent
}

// placesIn are the places of secrets a command of a run names, in the order it names
// them: every word of it is looked through, and a word that holds a place in its middle
// — a quoted path of a program, a path in the text of a heredoc — is looked through too.
func (c closed) placesIn(command string) []string {
	var places []string
	for _, word := range strings.Fields(command) {
		for _, place := range c.placesOf(word) {
			if !slices.Contains(places, place) {
				places = append(places, place)
			}
		}
	}
	return places
}

// placesOf are the places of secrets one word of a command names. A word is a path of
// the machine, a path from the home of the person, or a piece of text with such a path
// in it; the place of a secret is the path itself, so that a report names the file the
// run asked for and not only the folder it is in.
func (c closed) placesOf(word string) []string {
	if !strings.ContainsAny(word, "/.~") {
		return nil
	}
	bare := bareWord(word)
	var places []string
	for _, place := range c.folders {
		for _, name := range c.spelled(place) {
			if name == "" {
				continue
			}
			if bare == name || strings.HasPrefix(bare, name+"/") {
				places = append(places, bare)
				continue
			}
			if at := strings.Index(word, name); at >= 0 {
				places = append(places, pathIn(word[at:]))
			}
		}
	}
	for _, pattern := range c.patterns {
		if matched, err := filepath.Match(pattern, filepath.Base(bare)); err == nil && matched {
			places = append(places, bare)
		}
	}
	return places
}

// shellMarks are the characters a shell writes around its words, which are not a part
// of the path between them: a word of a command is the path in it with these taken off.
const shellMarks = " \t\r\n'\"`;:()&|<>,="

// bare is a word of a command as a path: the path in it without the marks of a shell
// around it, which is what a path of the machine looks like in what a run wrote.
func bareWord(word string) string {
	return strings.Trim(word, shellMarks)
}

// pathIn is the path in the beginning of a piece of text of a command, up to the first
// mark of a shell: a quoted path of a program is a path with a quote on either side of
// it, and what is beyond the quote is the rest of the text and not a part of it.
func pathIn(text string) string {
	if end := strings.IndexAny(text, shellMarks); end > 0 {
		return text[:end]
	}
	return text
}

// kindOfCall is what one call of a run asked of the place it names: a shell that changed
// folder into it, a shell that wrote into it, a shell that only wrote it down in the
// text of the command, and a tool of the agent by what it does to a file. A place that
// a command names in the text of a word — `python3 -c "open('~/.aws/credentials')"` —
// is named in text, because what the program would have done with it is not written
// down in the command and the refusal is the same one all the same (docs/DESIGN.md §7d).
func kindOfCall(call profile.Call, place string) string {
	if !isShell(call.Tool) {
		return kindOfTool(call.Tool)
	}
	command := call.Argument
	switch {
	case foldsInto(place, command):
		return accessCd
	case writesInto(place, command):
		return accessWrite
	case namedInText(place, command):
		return accessText
	case strings.Contains(command, place) || strings.Contains(command, bareWord(place)):
		return accessRead
	default:
		return accessSilent
	}
}

// The tools of an agent by what they do to a file, for the call that is not a shell: the
// tools that change a file write, and everything else opens it. An agent may call a tool
// crewflow does not know, and a tool it does not know opens a file rather than changes
// it — a wrong kind of access in a report is a worse thing than a cautious one.
var writers = map[string]bool{"edit": true, "write": true, "patch": true, "apply_patch": true, "multiedit": true}

func kindOfTool(tool string) string {
	if writers[strings.ToLower(strings.TrimSpace(tool))] {
		return accessWrite
	}
	return accessRead
}

// foldsInto says whether a command of a shell changed folder into a place: the word
// after the `cd` of it is the place, and nothing else.
func foldsInto(place, command string) bool {
	folder := folderChangedInto(command)
	return folder != "" && (folder == place || folder == bareWord(place))
}

// folderChangedInto is the folder a command of a shell changes into, whether the `cd` of
// it is the first word of the command or comes after a `&&` or a `;`.
func folderChangedInto(command string) string {
	words := strings.Fields(command)
	for i, word := range words {
		if word != "cd" && word != "pushd" && word != "chdir" {
			continue
		}
		if i > 0 && words[i-1] != "&&" && words[i-1] != ";" {
			continue
		}
		if i+1 < len(words) {
			return bareWord(words[i+1])
		}
	}
	return ""
}

// writesInto says whether a command of a shell wrote into a place: the place is what a
// redirect or a tee of the command wrote into, or the file it edits where it stands.
func writesInto(place, command string) bool {
	bare := bareWord(place)
	for _, target := range targets(command) {
		if target == place || bareWord(target) == bare {
			return true
		}
	}
	if !editsInPlace.MatchString(command) {
		return false
	}
	for _, word := range strings.Fields(command) {
		if word == place || bareWord(word) == bare {
			return true
		}
	}
	return false
}

// namedInText says whether a place is written down in the text of a command and is not
// a word of it: in the body of a heredoc, or in the middle of a quoted word — the path
// of a file a program opens, `open('~/.aws/credentials')` — rather than being the whole
// of a word, which a shell passes on to the program as a path (docs/DESIGN.md §7d).
func namedInText(place, command string) bool {
	if place == "" || !strings.Contains(command, place) {
		return false
	}
	spans := inText(command)
	for at := 0; ; {
		spot := strings.Index(command[at:], place)
		if spot < 0 {
			return true
		}
		from := at + spot
		if !slices.ContainsFunc(spans, func(span text) bool {
			if from < span.from || from+len(place) > span.to {
				return false
			}
			return span.heredoc || !wholeWord(command, span, from)
		}) {
			return false
		}
		at = from + len(place)
	}
}

// wholeWord says whether a quoted span is a word of the command whole: a path in quotes
// of its own is a path the command uses, and a path in the middle of a quoted word is
// text of a program that crewflow does not read.
func wholeWord(command string, span text, at int) bool {
	start := strings.LastIndexAny(command[:at], " \t") + 1
	end := strings.IndexAny(command[span.to:], " \t")
	if end < 0 {
		end = len(command)
	} else {
		end += span.to
	}
	return bareWord(command[start:end]) == command[span.from:span.to]
}

// text is a span of a command that is its text and not its words: what is inside quotes,
// and the body of every heredoc of it. A command is read for what it does, and a path of
// the machine in the middle of a line of a file it writes is written down, not opened
// (docs/DESIGN.md §7d).
type text struct {
	from, to int
	// heredoc says that the span is the body of a heredoc, where every line is text
	// and no word of the command is in it.
	heredoc bool
}

func inText(command string) []text {
	var spans []text
	for i := 0; i < len(command); i++ {
		switch mark := command[i]; mark {
		case '\'', '"', '`':
			end := i + 1 + strings.IndexByte(command[i+1:], mark)
			if end < i+1 {
				end = len(command)
			}
			spans = append(spans, text{from: i + 1, to: end})
			i = end
		case '<':
			if body, end, isHeredoc := heredocOf(command, i); isHeredoc {
				spans = append(spans, text{from: body, to: end, heredoc: true})
				i = end
			}
		}
	}
	return spans
}

// heredocOf is the body of the heredoc of a command that begins at the `<<` at or, and
// the end of that body: the body is what is between the end of the line the marker is on
// and the line that is the marker and nothing else.
func heredocOf(command string, at int) (body, end int, isHeredoc bool) {
	rest := command[at:]
	if !strings.HasPrefix(rest, "<<") {
		return 0, 0, false
	}
	word := markerOf(rest[2:])
	if word == "" {
		return 0, 0, false
	}
	newline := strings.IndexByte(rest, '\n')
	if newline < 0 {
		return 0, 0, false
	}
	body = at + newline + 1
	line, from := command[body:], body
	for {
		text, after, isLine := strings.Cut(line, "\n")
		if strings.TrimSpace(text) == word {
			return body, from, true
		}
		if !isLine {
			// A heredoc with no line of its marker in it: the rest of the command is
			// its body, and a command that was cut short is read as far as it goes.
			return body, len(command), true
		}
		line, from = after, from+len(text)+1
	}
}

// markerOf is the word a heredoc of a command ends at, in the marks a shell writes it
// in: `<<EOF`, `<<-'EOF'`, `<< "EOF"`. It is the first word after the marks and nothing
// of the rest of the line, which is where the body of the heredoc begins.
func markerOf(after string) string {
	after = strings.TrimLeft(strings.TrimLeft(after, "-"), `'"`)
	if end := strings.IndexAny(after, " \t\r\n'\""); end > 0 {
		after = after[:end]
	}
	if strings.ContainsAny(after, " \t\r\n'\"") {
		return ""
	}
	return after
}

// secret is whether a refused path is a place of secrets or lies in one. A `~` in the
// path is the home of the person and a relative path is the path it has from the
// worktree, because a shell of a run stands there and a path of `..` that climbs to a
// key is a key however it was written (docs/DESIGN.md §7d, §8).
func (c closed) secret(path, worktree string) bool {
	written := []string{plain(path), resolved(path, worktree, c.home)}
	if written[0] == "" {
		return false
	}
	if written[0] == written[1] {
		written = written[:1]
	}
	for _, place := range c.folders {
		if c.beneath(written, place) {
			return true
		}
	}
	for _, place := range written {
		if c.pattern(place) {
			return true
		}
	}
	return false
}

// beneath says that one of the paths a run was refused is the place of secrets or
// something under it, in the spelling of either: a folder of the machine and the same
// folder written from the home of the person are one place in two words.
func (c closed) beneath(paths []string, place string) bool {
	for _, name := range c.spelled(place) {
		for _, path := range paths {
			if under(name, path) {
				return true
			}
		}
	}
	return false
}

// spelled is a place of secrets in every way a run may write it: as this machine holds
// it, and from the home of the person with a "~" in front of it. A machine that cannot
// say where the home of the person is has no spelling to add, and the place is left as
// it is written.
func (c closed) spelled(place string) []string {
	home := strings.TrimSuffix(c.home, string(filepath.Separator))
	switch {
	case home == "":
		return []string{place}
	case place == home:
		return []string{place, "~"}
	case strings.HasPrefix(place, home+string(filepath.Separator)):
		return []string{place, "~/" + filepath.ToSlash(strings.TrimPrefix(place, home+string(filepath.Separator)))}
	default:
		return []string{place}
	}
}

// pattern is whether a path is one of the patterns of the closed places: its last
// folder against the last part of the pattern, because "**" is a pattern of the same
// name on every machine and a `.env` is a `.env` wherever it is written.
func (c closed) pattern(path string) bool {
	if path == "" {
		return false
	}
	last := filepath.Base(filepath.FromSlash(path))
	for _, pattern := range c.patterns {
		if matched, err := filepath.Match(pattern, last); err == nil && matched {
			return true
		}
	}
	return false
}

// resolved is a path of a run as the machine holds it: a "~" in front of it is the home
// of the person, and a relative path is the path it has from the worktree. What a glob
// adds to it is left as it is: a place of secrets is a place whatever a pattern of it
// covers.
func resolved(path, worktree, home string) string {
	place := plain(path)
	switch {
	case place == "~":
		return home
	case strings.HasPrefix(place, "~/"):
		if home == "" {
			return place
		}
		return filepath.Join(home, filepath.FromSlash(strings.TrimPrefix(place, "~/")))
	case !filepath.IsAbs(place):
		return filepath.Join(worktree, place)
	}
	return place
}
