package network

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/naghuale/crewflow/internal/config"
)

// The keys of the network section of the file of a project, as a person writes them.
// The editor below knows them one by one and nothing else: it changes the declared
// route and never rewrites a file it did not have to touch.
const (
	modeKey        = "mode"
	activeKey      = "active_proxy"
	proxyTableHead = "network.proxies"
	networkTable   = "network"
)

// Add writes a named profile into the file of the project. A profile of that name is
// already there — refuse rather than overwrite it: `edit` is the command that changes a
// profile, and a person who meant to change one should see which keys they change
// (docs/DESIGN.md §7d).
func Add(path, name string, profile config.Proxy) error {
	return change(path, func(file *file) error {
		if file.table(proxyTableHead+"."+name) != nil {
			return fmt.Errorf("the profile %q is already in %s: change it with "+
				"`crewflow network proxy edit %s`", name, file.path, name)
		}
		file.append([]string{fmt.Sprintf("[%s.%s]", proxyTableHead, name)})
		writeProfile(file.table(proxyTableHead+"."+name), profile)
		return nil
	})
}

// Edit writes the profile as it stands after the keys a person named, and only into the
// table of that profile: a key that was not named is written with the value it already
// had, so the file says what the profile is and not what it was (docs/DESIGN.md §7d).
func Edit(path, name string, profile config.Proxy) error {
	return change(path, func(file *file) error {
		table := file.table(proxyTableHead + "." + name)
		if table == nil {
			return fmt.Errorf("the profile %q is in no [network.proxies] of %s", name, file.path)
		}
		writeProfile(table, profile)
		return nil
	})
}

// writeProfile is the profile in the lines of the file of a project: four keys, each of
// them written the way TOML writes it, so that one place turns a value into text.
func writeProfile(table *section, profile config.Proxy) {
	table.set("type", quote(profile.Type))
	table.set("host", quote(profile.Host))
	table.set("port", strconv.Itoa(profile.Port))
	table.set("credentials", quote(profile.Credentials))
}

// Use makes the profile the active one of the project. Nothing else changes: the mode
// stays what it is, and a project that named a profile without switching the route to
// it is a project that prepared for it and did not ask for it (docs/DESIGN.md §7d).
func Use(path, name string) error {
	return change(path, func(file *file) error {
		if file.table(proxyTableHead+"."+name) == nil {
			return fmt.Errorf("the profile %q is in no [network.proxies] of %s", name, file.path)
		}
		file.keyOfTable(networkTable, activeKey, quote(name))
		return nil
	})
}

// Remove takes a profile out of the file of the project. It never takes the active one
// out: a route that names a profile nobody defined is a file that does not load, and a
// person who wants another route has said which one with `crewflow network mode`.
//
// The credentials of the profile are not touched either. A profile of a file and a
// value in the store of secrets are two things, and removing one is not removing the
// other: a person who adds the profile back in an hour wants the same proxy to answer
// (docs/DESIGN.md §7d, §7e).
func Remove(path, name string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	if cfg.Network.ActiveProxy == name {
		return fmt.Errorf("the profile %q is the active one of %s: point the route elsewhere "+
			"with `crewflow network mode direct` or `crewflow network mode proxy` first", name, path)
	}
	return change(path, func(file *file) error {
		if file.table(proxyTableHead+"."+name) == nil {
			return fmt.Errorf("the profile %q is in no [network.proxies] of %s", name, file.path)
		}
		file.dropTable(proxyTableHead + "." + name)
		return nil
	})
}

// Mode writes the route of the project. Only the modes crewflow does are written: a command
// that promised a route and then let the loader refuse the file would leave a person with the
// error of a load instead of the one of the command (§7d).
func Mode(path, mode string) error {
	switch mode {
	case "direct", "proxy", "fallback":
	default:
		return fmt.Errorf("network.mode: unknown value %q: direct, proxy or fallback", mode)
	}
	return change(path, func(file *file) error {
		file.keyOfTable(networkTable, modeKey, quote(mode))
		return nil
	})
}

// change edits the file of the project at path and puts it in place whole or not at
// all: the new text is written through a file of its own next to it and moved over, and
// a file cut in half by an interruption is a file that says the wrong thing about the
// route of every run that follows.
//
// Nothing reaches the file before it is read back by the loader: a change that leaves a
// file the project cannot load is not saved, and the refusal of the load is what a
// person reads. That is the whole of "syntactically wrong is not saved" — the editor
// never guesses what the file should say, it asks the same loader every other command
// asks (docs/DESIGN.md §7d).
func change(path string, edit func(*file) error) error {
	current, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	edited := &file{path: path, lines: strings.Split(string(current), "\n")}
	if err := edit(edited); err != nil {
		return err
	}
	next := strings.Join(edited.lines, "\n")
	if next == string(current) {
		return nil
	}
	return put(path, next)
}

// put writes the text of the file of a project in place, and only if the loader reads
// it: the file the commands of every other run read is checked here, and a change that
// does not load is thrown away with the temporary file it was written to.
func put(path, text string) error {
	folder := filepath.Dir(path)
	temporary, err := os.CreateTemp(folder, filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("make a file in %s: %w", folder, err)
	}
	name := temporary.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := temporary.WriteString(text); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write %s: %w", name, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	if _, err := config.Load(name); err != nil {
		// The loader names the file it read, and the file it read is the temporary one:
		// a person who is told about `crewflow.toml.374120` learns nothing about their
		// own file.
		return fmt.Errorf("the change is not written to %s: %s", path,
			strings.ReplaceAll(err.Error(), name, path))
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("put %s in place of %s: %w", name, path, err)
	}
	return nil
}

// file is the file of a project as lines, with the tables of the network section of it
// found by their names. Everything else in the file is carried over untouched: a comment
// of a person is not crewflow's to lose, and a file that is rewritten is a file where
// the next change cannot be read.
type file struct {
	path  string
	lines []string
}

// table returns the table with this name, or nothing when the file has none.
func (f *file) table(name string) *section {
	start := -1
	for i, line := range f.lines {
		if header, found := headerOf(line); found && header == name {
			start = i
			break
		}
	}
	if start < 0 {
		return nil
	}
	end := len(f.lines)
	for i := start + 1; i < len(f.lines); i++ {
		if _, found := headerOf(f.lines[i]); found {
			end = i
			break
		}
	}
	return &section{name: name, file: f, start: start, end: end}
}

// keyOfTable writes one key of the table with this name, and the table itself when the
// file has none: a project that said nothing about the network gets the section that
// says what the person just asked for.
func (f *file) keyOfTable(name, key, value string) {
	table := f.table(name)
	if table == nil {
		f.append([]string{fmt.Sprintf("[%s]", name)})
		table = f.table(name)
	}
	table.set(key, value)
}

// append puts lines at the end of the file, keeping the file ending the way it was: a
// file that ends with one empty line ends with one empty line after the change too.
func (f *file) append(lines []string) {
	end := len(f.lines)
	for end > 0 && strings.TrimSpace(f.lines[end-1]) == "" {
		end--
	}
	f.lines = slices.Concat(f.lines[:end], lines, f.lines[end:])
}

// dropTable takes a table out of the file, header and keys together, and leaves the
// blank line that separated it from the one before.
func (f *file) dropTable(name string) {
	table := f.table(name)
	if table == nil {
		return
	}
	f.lines = slices.Concat(f.lines[:table.start], f.lines[table.end:])
}

// section is one table of the file of a project, as the lines between its header and the
// next one.
type section struct {
	name  string
	file  *file
	start int
	end   int
}

// set writes one key of the table: where the key is already, its line is replaced, and
// where it is not, it is added at the end of the keys of the table — above the table
// that follows it, which is what the file reads as.
func (s *section) set(key, value string) {
	written := key + " = " + value
	for i := s.start + 1; i < s.end; i++ {
		if existing, found := keyOf(s.file.lines[i]); found && existing == key {
			s.file.lines[i] = written
			return
		}
	}
	at := s.end
	for at > s.start+1 && strings.TrimSpace(s.file.lines[at-1]) == "" {
		at--
	}
	s.file.lines = slices.Concat(s.file.lines[:at], []string{written}, s.file.lines[at:])
	s.end++
}

// headerOf is the name of a table written on a line of the file, and whether the line
// is one at all: a key, a comment and a blank line are not.
//
// The comment after a header is read the way TOML reads it, and a header nobody
// recognised is a table nobody edited: the command wrote a table of its own, and the
// loader of every other command then refused the file of the project with «Key 'network'
// has already been defined» (docs/DESIGN.md §7d).
func headerOf(line string) (string, bool) {
	trimmed := withoutComment(strings.TrimSpace(line))
	if len(trimmed) < 2 || trimmed[0] != '[' || trimmed[len(trimmed)-1] != ']' {
		return "", false
	}
	name := strings.TrimSpace(trimmed[1 : len(trimmed)-1])
	name = strings.Trim(name, `"'`)
	if strings.HasPrefix(name, "[") || strings.ContainsAny(name, " \t") {
		return "", false
	}
	return name, true
}

// withoutComment is the text of a line up to the comment in it. A `#` outside a quoted
// name begins a comment, and inside one it is a character of the name — `[ "a # b" ]` is
// a table of a name with a hash in it, and there is nothing to cut out of it.
func withoutComment(line string) string {
	var quoted byte
	for i := range len(line) {
		switch c := line[i]; {
		case quoted != 0:
			if c == quoted {
				quoted = 0
			}
		case c == '"' || c == '\'':
			quoted = c
		case c == '#':
			return strings.TrimRight(line[:i], " \t")
		}
	}
	return line
}

// keyOf is the key a line writes, and whether the line writes one.
func keyOf(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "[") {
		return "", false
	}
	key, _, found := strings.Cut(trimmed, "=")
	if !found {
		return "", false
	}
	key = strings.TrimSpace(key)
	if key == "" || strings.ContainsAny(key, " \t\"'") {
		return "", false
	}
	return key, true
}

// quote is a string of the file of a project: TOML writes strings in quotes, and a host
// or a protocol of a proxy is one line of text whatever is in it.
func quote(value string) string {
	return strconv.Quote(value)
}
